package rpc

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/yardkey"
)

// e2eDevice is a stand-in device of the yard y: paired by the sealed
// redemption with its own channel key, then holding a channel session
// it handshook for. It speaks only envelopes; no token crosses.
type e2eDevice struct {
	y       *pairYard
	handler http.Handler
	key     yardkey.ChannelKey
	session *yardkey.ChannelSession
}

func newE2EDevice(t *testing.T, y *pairYard, user, name string) *e2eDevice {
	t.Helper()
	key, err := yardkey.GenerateChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	code := y.open(t, user, name)
	pairFirst, _, err := yardkey.StartPairingHandshake(key, y.channel.Public, code)
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := json.Marshal(map[string]string{"sealed": base64.RawURLEncoding.EncodeToString(pairFirst)})
	if status, body := y.post(t, PairPath, "", string(sealed)); status != http.StatusOK {
		t.Fatalf("the sealed pairing = %d %s", status, body)
	}
	handler := y.u.Handler()
	d := &e2eDevice{y: y, key: key, handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = linkRemoteAddr
		handler.ServeHTTP(w, r)
	})}
	// The session handshake: a first message with no call named.
	first, opening, err := yardkey.StartChannelHandshake(key, y.channel.Public)
	if err != nil {
		t.Fatal(err)
	}
	w := d.post(t, first, "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("the channel handshake = %d %s", w.Code, w.Body)
	}
	d.session, err = opening.OpenAnswer(w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// post sends one outer call down the simulated link: no credential,
// from the link's address.
func (d *e2eDevice) post(t *testing.T, body []byte, call string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, E2EPath, bytes.NewReader(body))
	r.RemoteAddr = linkRemoteAddr
	for name, values := range header {
		r.Header[name] = values
	}
	if call != "" {
		r.Header.Set(E2ECallHeader, call)
	}
	w := httptest.NewRecorder()
	d.handler.ServeHTTP(w, r)
	return w
}

// callRaw makes one enveloped call and answers its outer status and
// the sealed answer as it crossed, with the call id it was made
// under: a test opens the answer whole (call) or record by record,
// and may drop or add records of it first, as a carrier could.
func (d *e2eDevice) callRaw(t *testing.T, method, path string, header http.Header, body []byte, seal, wire *yardkey.FirstBinding) (outer int, outerHeader http.Header, sealed []byte, call []byte) {
	t.Helper()
	rawHead, err := json.Marshal(e2eRequestHead{Method: method, Path: path, Header: header})
	if err != nil {
		t.Fatal(err)
	}
	call, err = yardkey.NewCallID()
	if err != nil {
		t.Fatal(err)
	}
	run, err := d.session.SealWhole(call, seal, joinE2EHead(rawHead, body))
	if err != nil {
		t.Fatal(err)
	}
	wireHeader := http.Header{}
	if wire != nil && wire.Present {
		wireHeader.Set(E2EStreamHeader, wire.Marker)
	}
	w := d.post(t, run, base64.RawURLEncoding.EncodeToString(call), wireHeader)
	return w.Code, w.Result().Header, w.Body.Bytes(), call
}

// call makes one enveloped whole call and opens its sealed answer,
// answering the inner status, headers and body beside the outer
// status. sealMarker and wireMarker are told apart so a test can
// rewrite the marker between the device and the yard.
func (d *e2eDevice) call(t *testing.T, method, path string, header http.Header, body []byte, seal, wire *yardkey.FirstBinding) (outer int, outerHeader http.Header, status int, innerHeader http.Header, innerBody []byte) {
	t.Helper()
	outer, outerHeader, sealed, call := d.callRaw(t, method, path, header, body, seal, wire)
	if outer != http.StatusOK {
		return outer, outerHeader, 0, nil, sealed
	}
	plain, err := d.session.OpenWhole(sealed, call, seal)
	if err != nil {
		t.Fatalf("the sealed answer does not open: %v", err)
	}
	head, innerBody, err := splitE2EHead(plain)
	if err != nil {
		t.Fatal(err)
	}
	var answer e2eAnswerHead
	if err := json.Unmarshal(head, &answer); err != nil {
		t.Fatal(err)
	}
	return outer, outerHeader, answer.Status, answer.Header, innerBody
}

func bindFor(marker string, present bool) *yardkey.FirstBinding {
	return &yardkey.FirstBinding{Path: E2EPath, Marker: marker, Present: present}
}

// A stand-in device and a real yard exchange a call sealed end to end:
// the inner call is served as its user, the outer head shows only the
// envelope's own status and headers, and no inner word crosses in the
// clear of the outer body. A body that needs several records, in and
// back, crosses whole.
func TestE2EWholeCallSealedEndToEnd(t *testing.T) {
	y := newPairYard(t)
	d := newE2EDevice(t, y, "sam", "phone")
	plain := bindFor("", false)

	outer, outerHeader, status, _, body := d.call(t, http.MethodGet, "/user/me", nil, nil, plain, plain)
	if outer != http.StatusOK || status != http.StatusOK {
		t.Fatalf("GET /user/me outer %d, inner %d (%s)", outer, status, body)
	}
	if strings.TrimSpace(string(body)) != `{"user":"sam","said_by":"phone","yard":"south","yardmaster":"yardmaster"}` {
		t.Errorf("the inner answer = %s", body)
	}
	for name := range outerHeader {
		switch name {
		case "Content-Type", http.CanonicalHeaderKey(e2eEnvelopeHeader), http.CanonicalHeaderKey(E2ECallHeader):
		default:
			t.Errorf("the outer head carries %q, an inner or unknown header", name)
		}
	}
	if outerHeader.Get("Content-Type") != "application/octet-stream" || outerHeader.Get(e2eEnvelopeHeader) != "sealed" {
		t.Errorf("the outer head = %v", outerHeader)
	}

	// Several records in, several back: a blob of 200 KiB.
	blob := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("the shed, painted; "), 12000)...)
	args, _ := json.Marshal(map[string]string{"data": base64.StdEncoding.EncodeToString(blob)})
	outer, _, status, _, body = d.call(t, http.MethodPost, "/user/call/PutBlob", nil, args, plain, plain)
	if outer != http.StatusOK || status != http.StatusOK {
		t.Fatalf("PutBlob outer %d, inner %d (%s)", outer, status, body)
	}
	var reply struct {
		Result struct {
			Hash string `json:"hash"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &reply); err != nil || len(reply.Result.Hash) != 64 {
		t.Fatalf("PutBlob answered %s (%v)", body, err)
	}
	// The sealed outer body of that call held nothing readable.
	outer, outerHeader, status, innerHeader, body := d.call(t, http.MethodGet, "/user/blobs/"+reply.Result.Hash, nil, nil, plain, plain)
	if outer != http.StatusOK || status != http.StatusOK || !bytes.Equal(body, blob) {
		t.Fatalf("the blob back outer %d, inner %d, %d of %d bytes", outer, status, len(body), len(blob))
	}
	if innerHeader.Get("Content-Type") != "image/png" || outerHeader.Get("Content-Type") == "image/png" {
		t.Errorf("inner type %q, outer type %q: the inner head travels only sealed", innerHeader.Get("Content-Type"), outerHeader.Get("Content-Type"))
	}
}

// An inner refusal is sealed like any answer: the outer head is 200
// and shows nothing of the inner 404, which the device alone reads.
func TestE2EInnerStatusStaysSealed(t *testing.T) {
	y := newPairYard(t)
	d := newE2EDevice(t, y, "sam", "phone")
	plain := bindFor("", false)
	outer, outerHeader, status, _, _ := d.call(t, http.MethodGet, "/user/blobs/"+strings.Repeat("0", 64), nil, nil, plain, plain)
	if outer != http.StatusOK || status != http.StatusNotFound {
		t.Fatalf("a missing blob outer %d, inner %d, want 200 and 404", outer, status)
	}
	if got := outerHeader.Get(e2eEnvelopeHeader); got != "sealed" {
		t.Errorf("the outer head = %q, want the envelope's word alone", got)
	}

	// The edge's limit is on the sealed body. A plaintext body that
	// fits by itself can still exceed it once the inner head and the
	// records' framing and tags are sealed.
	if err := y.st.SetSetting(context.Background(), store.BlobMaxSetting, "16777216", "jo"); err != nil {
		t.Fatal(err)
	}
	large := bytes.Repeat([]byte("x"), maxE2EAnswer)
	ref, err := y.st.PutBlob(store.WithVia(context.Background(), "sam", "phone"), large)
	if err != nil {
		t.Fatal(err)
	}
	outer, outerHeader, status, _, body := d.call(t, http.MethodGet, "/user/blobs/"+ref.Hash, nil, nil, plain, plain)
	if outer != http.StatusOK || status != http.StatusRequestEntityTooLarge || string(body) != http.StatusText(http.StatusRequestEntityTooLarge)+"\n" {
		t.Fatalf("an answer above the ciphertext bound = outer %d, inner %d, %q", outer, status, body)
	}
	if outerHeader.Get(e2eEnvelopeHeader) != "sealed" || outerHeader.Get("Content-Type") != "application/octet-stream" {
		t.Errorf("an oversized answer's outer head = %v", outerHeader)
	}
}

// A first record whose stream marker was rewritten between the device
// and the yard — dropped, as here — fails its opening and its call,
// and the honest call after it on the same session still opens.
func TestE2EMarkerRewrittenFailsTheCall(t *testing.T) {
	y := newPairYard(t)
	d := newE2EDevice(t, y, "sam", "phone")
	sealed := bindFor(E2EStreamLongLived, true)
	outer, _, _, _, _ := d.call(t, http.MethodGet, "/user/me", nil, nil, sealed, bindFor("", false))
	if outer != http.StatusBadRequest {
		t.Fatalf("a call whose marker was dropped on the way = %d, want 400", outer)
	}
	plain := bindFor("", false)
	outer, _, status, _, _ := d.call(t, http.MethodGet, "/user/me", nil, nil, plain, plain)
	if outer != http.StatusOK || status != http.StatusOK {
		t.Fatalf("the honest call after it = outer %d, inner %d", outer, status)
	}
}

// A request whose run ends without its sealed final record is
// refused unserved — its opening fails whole — and the honest call
// after it on the same session still opens.
func TestE2ETruncatedRequestRefusedUnserved(t *testing.T) {
	y := newPairYard(t)
	d := newE2EDevice(t, y, "sam", "phone")
	plain := bindFor("", false)

	rawHead, err := json.Marshal(e2eRequestHead{Method: http.MethodGet, Path: "/user/me"})
	if err != nil {
		t.Fatal(err)
	}
	call, err := yardkey.NewCallID()
	if err != nil {
		t.Fatal(err)
	}
	run, err := d.session.SealWhole(call, plain, joinE2EHead(rawHead, nil))
	if err != nil {
		t.Fatal(err)
	}
	records, err := yardkey.SplitRecords(run)
	if err != nil || len(records) < 2 {
		t.Fatalf("records = %d, %v", len(records), err)
	}
	var truncated []byte
	for _, m := range records[:len(records)-1] {
		truncated = yardkey.FrameRecord(truncated, m)
	}
	if w := d.post(t, truncated, base64.RawURLEncoding.EncodeToString(call), nil); w.Code != http.StatusBadRequest {
		t.Fatalf("a truncated request = %d, want 400 unserved", w.Code)
	}
	if outer, _, status, _, _ := d.call(t, http.MethodGet, "/user/me", nil, nil, plain, plain); outer != http.StatusOK || status != http.StatusOK {
		t.Fatalf("the honest call after it = outer %d, inner %d", outer, status)
	}
}

// The channel key, not the token, authenticates an enveloped call:
// a handshake from a key no device pins founds no session, and a call
// on a session whose device record has been removed — re-read at the
// call's first record — is refused unserved, while a second device's
// session is untouched.
func TestE2EChannelAuthenticatesNotTheToken(t *testing.T) {
	y := newPairYard(t)
	d := newE2EDevice(t, y, "sam", "phone")
	other := newE2EDevice(t, y, "jo", "tablet")

	stranger, err := yardkey.GenerateChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := yardkey.StartChannelHandshake(stranger, y.channel.Public)
	if err != nil {
		t.Fatal(err)
	}
	if w := d.post(t, first, "", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("a handshake from an unpinned key = %d, want 401", w.Code)
	}

	if _, err := y.st.RemoveDevice(context.Background(), "sam", "phone", "jo"); err != nil {
		t.Fatal(err)
	}
	plain := bindFor("", false)
	if outer, _, _, _, _ := d.call(t, http.MethodGet, "/user/me", nil, nil, plain, plain); outer != http.StatusUnauthorized {
		t.Errorf("a call after its device was removed = %d, want 401", outer)
	}
	if outer, _, status, _, _ := other.call(t, http.MethodGet, "/user/me", nil, nil, plain, plain); outer != http.StatusOK || status != http.StatusOK {
		t.Errorf("the other device's call = outer %d, inner %d", outer, status)
	}
}

// A device asks for a registration voucher inside its channel, as often as
// it registers, and is given one for its own user and device that verifies
// against the yard's public key and states, in the open, whose it is and
// when it lapses. Each asking is fresh, and no body names another device
// than the one the channel proved. A plain call is refused, down the link
// and at the listener, since the edge read every token a device sent before
// it held a channel; a voucher opens nothing at the yard; and a device that
// was removed, or a yard that cannot sign, is given none.
func TestE2EPushVoucherOnRequest(t *testing.T) {
	y := newPairYard(t)
	d := newE2EDevice(t, y, "sam", "phone")
	other := newE2EDevice(t, y, "jo", "tablet")
	plain := bindFor("", false)
	base := time.Now()
	var later atomic.Int64
	y.u.now = func() time.Time { return base.Add(time.Duration(later.Load())) }
	ask := func(dev *e2eDevice, body []byte) string {
		t.Helper()
		outer, _, status, header, answer := dev.call(t, http.MethodPost, "/user/push/voucher", nil, body, plain, plain)
		if outer != http.StatusOK || status != http.StatusOK {
			t.Fatalf("POST /user/push/voucher outer %d, inner %d (%s)", outer, status, answer)
		}
		var got struct {
			Voucher string `json:"voucher"`
		}
		if err := json.Unmarshal(answer, &got); err != nil || got.Voucher == "" {
			t.Fatalf("the voucher answer = %s (%v)", answer, err)
		}
		if header.Get("Cache-Control") != "no-store" {
			t.Errorf("the voucher answer's Cache-Control = %q, want no-store", header.Get("Cache-Control"))
		}
		return got.Voucher
	}

	first := ask(d, nil)
	y.checkVoucher(t, first, "sam", "phone", base)
	// It crossed sealed: nothing of the outer call or its answer holds one.
	if outer, _, sealed, _ := d.callRaw(t, http.MethodPost, "/user/push/voucher", nil, nil, plain, plain); outer != http.StatusOK || bytes.Contains(sealed, []byte(yardkey.VoucherPrefix)) {
		t.Errorf("the outer answer = %d, %d bytes of it holding a voucher in the clear: %v", outer, len(sealed), bytes.Contains(sealed, []byte(yardkey.VoucherPrefix)))
	}

	// Each asking is fresh, the earlier one standing until its own expiry.
	later.Store(int64(3 * time.Minute))
	second := ask(d, nil)
	if second == first {
		t.Error("a second asking was given the first voucher again")
	}
	y.checkVoucher(t, second, "sam", "phone", base.Add(3*time.Minute))
	public := store.Public(y.identity)
	if _, err := yardkey.VerifyVoucher(public, first, base.Add(VoucherLife-time.Second)); err != nil {
		t.Errorf("the first voucher just before its expiry: %v", err)
	}
	if _, err := yardkey.VerifyVoucher(public, first, base.Add(VoucherLife)); !errors.Is(err, yardkey.ErrVoucherExpired) {
		t.Errorf("the first voucher at its expiry = %v, want it expired", err)
	}
	if _, err := yardkey.VerifyVoucher(public, second, base.Add(VoucherLife)); err != nil {
		t.Errorf("the second voucher when the first has lapsed: %v", err)
	}

	// The channel proves the device: a body that names another is no
	// say, and a second device is given its own.
	named, _ := json.Marshal(map[string]string{"user": "sam", "device": "phone", "yard": "north"})
	y.checkVoucher(t, ask(other, named), "jo", "tablet", base.Add(3*time.Minute))

	// A plain call, with a token that is its device's, is refused: down
	// the link as at the listener.
	status, body := y.redeem(t, y.open(t, "sam", "slate"))
	var paired Paired
	if err := json.Unmarshal([]byte(body), &paired); status != http.StatusOK || err != nil {
		t.Fatalf("the plain pairing = %d %s (%v)", status, body, err)
	}
	if status, answer := y.post(t, "/user/push/voucher", paired.Token, ""); status != http.StatusForbidden || strings.Contains(answer, yardkey.VoucherPrefix) {
		t.Errorf("a plain call at the listener = %d %s, want 403 and no voucher", status, answer)
	}
	r := httptest.NewRequest(http.MethodPost, "/user/push/voucher", nil)
	r.RemoteAddr = linkRemoteAddr
	r.Header.Set("Authorization", "Bearer "+paired.Token)
	w := httptest.NewRecorder()
	y.u.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), yardkey.VoucherPrefix) || !strings.Contains(w.Body.String(), "sealed") {
		t.Errorf("a plain call down the link = %d %s, want 403 naming the fix", w.Code, w.Body)
	}

	// A voucher is no credential at the yard.
	if status, answer := y.get(t, "/user/me", first); status != http.StatusUnauthorized || strings.Contains(answer, "sam") {
		t.Errorf("/user/me with a voucher as the bearer = %d %s, want 401", status, answer)
	}

	// A device removed since is given none, and a yard that cannot sign
	// answers the sealed call 500 and no voucher.
	if _, err := y.st.RemoveDevice(context.Background(), "sam", "phone", "jo"); err != nil {
		t.Fatal(err)
	}
	if outer, _, _, _, answer := d.call(t, http.MethodPost, "/user/push/voucher", nil, nil, plain, plain); outer != http.StatusUnauthorized || bytes.Contains(answer, []byte(yardkey.VoucherPrefix)) {
		t.Errorf("a removed device's request = outer %d, want 401 and no voucher", outer)
	}
	y.u.SetIdentityKeyFile("")
	outer, _, status, _, answer := other.call(t, http.MethodPost, "/user/push/voucher", nil, nil, plain, plain)
	if outer != http.StatusOK || status != http.StatusInternalServerError || bytes.Contains(answer, []byte(yardkey.VoucherPrefix)) {
		t.Errorf("a yard that cannot sign = outer %d, inner %d %s, want inner 500 and no voucher", outer, status, answer)
	}
}

// Enveloped calls and streams in flight at once on one session all
// open: 64 of them — the link's own bound — held open together, each
// message carrying its own number and opened with its own copied
// cipher state in its serving goroutine, in no sealing order.
func TestE2EConcurrentCallsOnOneSession(t *testing.T) {
	y := newPairYard(t)
	d := newE2EDevice(t, y, "sam", "phone")
	plain := bindFor("", false)
	ctx := context.Background()

	// Sixteen marked events streams first, held open while the calls
	// below are in flight: 48 calls and 16 streams make the 64.
	head, err := y.st.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	marked := bindFor(E2EStreamLongLived, true)
	path := "/user/events?after=" + strconv.FormatUint(head, 10)
	type held struct {
		s        *e2eStream
		cancel   context.CancelFunc
		finished chan struct{}
	}
	var streams []held
	for range StreamsPerCredential {
		s, _, cancel, finished := d.openStream(t, http.MethodGet, path, marked, marked)
		if status, _ := s.readHead(); status != http.StatusOK {
			t.Fatalf("a stream's head = %d", status)
		}
		streams = append(streams, held{s, cancel, finished})
	}
	defer func() {
		for _, h := range streams {
			h.s.close(h.cancel, h.finished)
		}
	}()

	const n = 48
	// Sealed here, served all at once below, opened after: the yard's
	// goroutines meet the messages in no sealing order.
	runs := make([][]byte, n)
	calls := make([][]byte, n)
	rawHead, err := json.Marshal(e2eRequestHead{Method: http.MethodGet, Path: "/user/me"})
	if err != nil {
		t.Fatal(err)
	}
	for i := range n {
		calls[i], err = yardkey.NewCallID()
		if err != nil {
			t.Fatal(err)
		}
		runs[i], err = d.session.SealWhole(calls[i], plain, joinE2EHead(rawHead, nil))
		if err != nil {
			t.Fatal(err)
		}
	}
	answers := make([]*httptest.ResponseRecorder, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			answers[i] = d.post(t, runs[i], base64.RawURLEncoding.EncodeToString(calls[i]), nil)
		})
	}
	wg.Wait()
	for i := range n {
		if answers[i].Code != http.StatusOK {
			t.Fatalf("call %d outer = %d", i, answers[i].Code)
		}
		opened, err := d.session.OpenWhole(answers[i].Body.Bytes(), calls[i], plain)
		if err != nil {
			t.Fatalf("call %d's answer does not open: %v", i, err)
		}
		if !strings.Contains(string(opened), `"sam"`) {
			t.Errorf("call %d answered %s", i, opened)
		}
	}

	// And an event crosses one of the streams held open throughout.
	if err := y.st.LogEvent(ctx, "", "noted", "jo", map[string]any{"bead": "yardr-33vh"}); err != nil {
		t.Fatal(err)
	}
	streams[0].s.readUntil(`"kind":"noted"`)
}

// e2eStreamWriter is a streamed outer answer's other end: a pipe the
// yard's handler writes its sealed records into as they are sealed,
// as the link's data frames would carry them to the device.
type e2eStreamWriter struct {
	header http.Header
	status int
	pw     *io.PipeWriter
}

func (w *e2eStreamWriter) Header() http.Header { return w.header }

func (w *e2eStreamWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *e2eStreamWriter) Write(b []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	return w.pw.Write(b)
}

func (w *e2eStreamWriter) Flush() {}

// e2eStream is the device's end of one streamed sealed answer: its
// records read as they cross and opened in their call's order, its
// inner head read first, its body what follows.
type e2eStream struct {
	t      *testing.T
	reader *yardkey.RecordReader
	opener *yardkey.CallOpener
	plain  []byte // opened plaintext not yet taken
	body   []byte // body taken so far
	status int
	header http.Header
	done   bool // the final record opened
}

// openStream makes one enveloped call whose answer streams, served in
// its own goroutine into a pipe, and answers the device's end of it
// with the outer writer the yard is writing and the way to end it.
func (d *e2eDevice) openStream(t *testing.T, method, path string, seal, wire *yardkey.FirstBinding) (*e2eStream, *e2eStreamWriter, context.CancelFunc, chan struct{}) {
	t.Helper()
	rawHead, err := json.Marshal(e2eRequestHead{Method: method, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	call, err := yardkey.NewCallID()
	if err != nil {
		t.Fatal(err)
	}
	run, err := d.session.SealWhole(call, seal, joinE2EHead(rawHead, nil))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest(http.MethodPost, E2EPath, bytes.NewReader(run))
	r = r.WithContext(ctx)
	r.RemoteAddr = linkRemoteAddr
	r.Header.Set(E2ECallHeader, base64.RawURLEncoding.EncodeToString(call))
	if wire != nil && wire.Present {
		r.Header.Set(E2EStreamHeader, wire.Marker)
	}
	pr, pw := io.Pipe()
	outer := &e2eStreamWriter{header: http.Header{}, pw: pw}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		defer pw.Close()
		d.handler.ServeHTTP(outer, r)
	}()
	s := &e2eStream{t: t, reader: yardkey.NewRecordReader(pr), opener: d.session.NewCallOpener(call, seal)}
	return s, outer, cancel, finished
}

// record reads and opens the stream's next record, holding its
// plaintext to be taken.
func (s *e2eStream) record() {
	s.t.Helper()
	msg, err := s.reader.ReadMessage()
	if err != nil {
		s.t.Fatalf("the stream's next record: %v", err)
	}
	plain, final, err := s.opener.Open(msg)
	if err != nil {
		s.t.Fatalf("a stream record does not open: %v", err)
	}
	s.plain = append(s.plain, plain...)
	s.done = s.done || final
}

// readHead opens records until the sealed inner head stands read, and
// answers its status and headers.
func (s *e2eStream) readHead() (int, http.Header) {
	s.t.Helper()
	for s.status == 0 {
		if len(s.plain) >= 4 {
			n := int(s.plain[0])<<24 | int(s.plain[1])<<16 | int(s.plain[2])<<8 | int(s.plain[3])
			if len(s.plain) >= 4+n {
				var head e2eAnswerHead
				if err := json.Unmarshal(s.plain[4:4+n], &head); err != nil {
					s.t.Fatal(err)
				}
				s.status, s.header = head.Status, head.Header
				s.plain = s.plain[4+n:]
				break
			}
		}
		if s.done {
			s.t.Fatal("the stream closed before its head")
		}
		s.record()
	}
	return s.status, s.header
}

// readUntil opens records until the body holds want, and answers the
// body so far.
func (s *e2eStream) readUntil(want string) []byte {
	s.t.Helper()
	if s.status == 0 {
		s.readHead()
	}
	for !bytes.Contains(s.body, []byte(want)) {
		if s.done {
			s.t.Fatalf("the stream closed with body %q, want %q in it", s.body, want)
		}
		if len(s.plain) > 0 {
			s.body = append(s.body, s.plain...)
			s.plain = nil
			continue
		}
		s.record()
	}
	return s.body
}

// close ends the caller's end and reads the stream out: the yard's
// handler returns, its final record crosses, and the pipe ends.
func (s *e2eStream) close(cancel context.CancelFunc, finished chan struct{}) {
	s.t.Helper()
	cancel()
	for {
		msg, err := s.reader.ReadMessage()
		if err == io.EOF {
			break
		}
		if err != nil {
			s.t.Fatalf("the stream's end: %v", err)
		}
		if _, _, err := s.opener.Open(msg); err != nil {
			s.t.Fatalf("the stream's last records do not open: %v", err)
		}
	}
	<-finished
	if err := s.opener.Close(); err != nil {
		s.t.Fatalf("a stream the yard ended = %v, want its final record", err)
	}
}

// A streamed blob and the events stream go sealed end to end: a blob
// too large for one record crosses in several, closed by its final
// record, and an events stream's head and events cross record by
// record as they are written. A record dropped in the middle is
// refused at the gap with nothing past it delivered, an outer ending
// with no final record is a truncation a part blob is never saved
// from, and a zero-length beat record in the run changes nothing.
func TestE2EStreamsSealedEndToEnd(t *testing.T) {
	y := newPairYard(t)
	d := newE2EDevice(t, y, "jo", "phone")
	plain := bindFor("", false)
	ctx := context.Background()

	// The blob, put sealed and fetched sealed: several records each way.
	blob := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("the shed, painted; "), 12000)...)
	args, _ := json.Marshal(map[string]string{"data": base64.StdEncoding.EncodeToString(blob)})
	if _, _, status, _, body := d.call(t, http.MethodPost, "/user/call/PutBlob", nil, args, plain, plain); status != http.StatusOK {
		t.Fatalf("PutBlob inner = %d (%s)", status, body)
	} else {
		var reply struct {
			Result struct{ Hash string }
		}
		if err := json.Unmarshal(body, &reply); err != nil || len(reply.Result.Hash) != 64 {
			t.Fatalf("PutBlob answered %s (%v)", body, err)
		}
		// Each variant fetches the blob anew: a fetch's records open
		// once on the device's session, and replay nowhere, so no
		// variant may re-open another's run.
		fetch := func() ([][]byte, []byte, []byte) {
			t.Helper()
			outer, _, sealed, call := d.callRaw(t, http.MethodGet, "/user/blobs/"+reply.Result.Hash, nil, nil, plain, plain)
			if outer != http.StatusOK {
				t.Fatalf("the blob's outer = %d", outer)
			}
			records, err := yardkey.SplitRecords(sealed)
			if err != nil || len(records) < 4 {
				t.Fatalf("the blob crossed in %d records, %v: want several and a final", len(records), err)
			}
			return records, call, sealed
		}
		// The device's saving of it: joined only once the final record
		// closes the call, so a part is never saved as a whole.
		save := func(call []byte, run []byte) ([]byte, error) {
			opener := d.session.NewCallOpener(call, plain)
			reader := yardkey.NewRecordReader(bytes.NewReader(run))
			var joined []byte
			for {
				msg, err := reader.ReadMessage()
				if err == io.EOF {
					break
				}
				if err != nil {
					return nil, err
				}
				piece, _, err := opener.Open(msg)
				if err != nil {
					return nil, err
				}
				joined = append(joined, piece...)
			}
			if err := opener.Close(); err != nil {
				return nil, err
			}
			return joined, nil
		}
		join := func(ms [][]byte) []byte {
			var out []byte
			for _, m := range ms {
				out = yardkey.FrameRecord(out, m)
			}
			return out
		}
		_, call, sealed := fetch()
		whole, err := save(call, sealed)
		if err != nil {
			t.Fatalf("the streamed blob does not open: %v", err)
		}
		if head, got, err := splitE2EHead(whole); err != nil || !bytes.Equal(got, blob) {
			t.Fatalf("the streamed blob = %d of %d bytes (%v, head %s)", len(got), len(blob), err, head)
		}
		// A beat forged into the run: read past, the blob unchanged.
		records, call, _ := fetch()
		withBeat := join([][]byte{records[0], nil, records[1]})
		withBeat = append(withBeat, join(records[2:])...)
		if whole, err := save(call, withBeat); err != nil || len(whole) == 0 {
			t.Errorf("a run with a beat in it = %v", err)
		} else if _, got, _ := splitE2EHead(whole); !bytes.Equal(got, blob) {
			t.Errorf("a beat changed the blob: %d of %d bytes", len(got), len(blob))
		}
		// The final record dropped: a truncation, nothing saved.
		records, call, _ = fetch()
		if saved, err := save(call, join(records[:len(records)-1])); !errors.Is(err, yardkey.ErrTruncated) || saved != nil {
			t.Errorf("a blob with no final record saved %d bytes, %v", len(saved), err)
		}
		// A middle record dropped: refused at the gap, nothing saved,
		// and nothing past the gap in what was delivered.
		records, call, _ = fetch()
		if saved, err := save(call, join(append([][]byte{records[0]}, records[2:]...))); !errors.Is(err, yardkey.ErrRecordGap) || saved != nil {
			t.Errorf("a blob past a gap saved %d bytes, %v", len(saved), err)
		}
	}

	// A streamed answer that crosses the edge's ciphertext bound is
	// left open. In particular, neither a data tail rejected at the
	// bound nor a final record that would cross it can close the part.
	t.Run("stream limit leaves the answer truncated", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			sealed  int
			pending []byte
		}{
			{name: "pending tail", sealed: maxE2EAnswer - 1, pending: []byte("tail")},
			{name: "final record", sealed: maxE2EAnswer - 50},
		} {
			t.Run(tc.name, func(t *testing.T) {
				call, err := yardkey.NewCallID()
				if err != nil {
					t.Fatal(err)
				}
				outer := httptest.NewRecorder()
				writer := newE2EAnswerWriter(outer, &e2eSession{keys: d.session}, call, plain, false)
				writer.started, writer.wrote = true, true
				writer.sealed, writer.pending = tc.sealed, tc.pending
				if err := writer.finish(); err != nil {
					t.Fatal(err)
				}
				if !writer.capped || outer.Body.Len() != 0 {
					t.Fatalf("the capped stream ended with capped=%t and %d tail bytes", writer.capped, outer.Body.Len())
				}
			})
		}
	})
	t.Run("pre-flush overflow stays a whole refusal", func(t *testing.T) {
		call, err := yardkey.NewCallID()
		if err != nil {
			t.Fatal(err)
		}
		outer := httptest.NewRecorder()
		d.y.u.mu.Lock()
		session := d.y.u.e2eSessions[string(d.session.Tag())]
		d.y.u.mu.Unlock()
		if session == nil {
			t.Fatal("the yard's channel session is missing")
		}
		writer := newE2EAnswerWriter(outer, session, call, plain, false)
		if _, err := writer.Write(make([]byte, maxE2EAnswer+1)); err != nil {
			t.Fatal(err)
		}
		writer.Flush()
		if writer.started || outer.Body.Len() != 0 {
			t.Fatalf("the first flush sent an incomplete answer: started=%t, %d bytes", writer.started, outer.Body.Len())
		}
		if err := writer.finish(); err != nil {
			t.Fatal(err)
		}
		opened, err := d.session.OpenWhole(outer.Body.Bytes(), call, plain)
		if err != nil {
			t.Fatalf("the oversized answer's refusal does not open: %v", err)
		}
		head, body, err := splitE2EHead(opened)
		if err != nil {
			t.Fatal(err)
		}
		var answer e2eAnswerHead
		if err := json.Unmarshal(head, &answer); err != nil {
			t.Fatal(err)
		}
		if answer.Status != http.StatusRequestEntityTooLarge || string(body) != http.StatusText(http.StatusRequestEntityTooLarge)+"\n" {
			t.Fatalf("an oversized answer after Flush = %d, %q; want a whole 413 refusal", answer.Status, body)
		}
	})

	// The events stream, marked as the router marks it.
	head, err := y.st.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	marked := bindFor(E2EStreamLongLived, true)
	s, outer, cancel, finished := d.openStream(t, http.MethodGet,
		"/user/events?after="+strconv.FormatUint(head, 10), marked, marked)
	status, header := s.readHead()
	if status != http.StatusOK || header.Get("Content-Type") != "application/x-ndjson" {
		t.Fatalf("the events head sealed inside = %d %v", status, header)
	}
	if outer.status != http.StatusOK || outer.header.Get(e2eEnvelopeHeader) != "sealed" ||
		outer.header.Get("Content-Type") != "application/octet-stream" {
		t.Errorf("the outer head = %d %v, want the envelope's alone", outer.status, outer.header)
	}
	if err := y.st.LogEvent(ctx, "", "noted", "jo", map[string]any{"bead": "yardr-33vh"}); err != nil {
		t.Fatal(err)
	}
	if body := s.readUntil(`"kind":"noted"`); !bytes.Contains(body, []byte(`"kind":"noted"`)) {
		t.Fatalf("the event crossed as %q", body)
	}
	s.close(cancel, finished)
}

// A marked call whose opened inner call is any other than the events
// stream is refused sealed and unserved, and holds no stream slot: a
// device still opens its sixteen marked streams after one, and the
// seventeenth marked stream is refused as the seventeenth plain one
// is (TestUserStreamsAreBounded holds the plain side).
func TestE2EMarkedCallOpensOnlyTheEventsStream(t *testing.T) {
	y := newPairYard(t)
	d := newE2EDevice(t, y, "sam", "phone")
	marked := bindFor(E2EStreamLongLived, true)
	ctx := context.Background()

	outer, _, status, _, body := d.call(t, http.MethodGet, "/user/me", nil, nil, marked, marked)
	if outer != http.StatusOK || status != http.StatusForbidden || !strings.Contains(string(body), "marked long-lived") {
		t.Fatalf("a marked call to /user/me = outer %d, inner %d (%s)", outer, status, body)
	}

	head, err := y.st.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	path := "/user/events?after=" + strconv.FormatUint(head, 10)
	type held struct {
		s        *e2eStream
		cancel   context.CancelFunc
		finished chan struct{}
	}
	var streams []held
	for range StreamsPerCredential {
		s, _, cancel, finished := d.openStream(t, http.MethodGet, path, marked, marked)
		if status, _ := s.readHead(); status != http.StatusOK {
			t.Fatalf("a marked stream's head = %d", status)
		}
		streams = append(streams, held{s, cancel, finished})
	}
	outer, _, status, _, body = d.call(t, http.MethodGet, path, nil, nil, marked, marked)
	if outer != http.StatusOK || status != http.StatusServiceUnavailable || !strings.Contains(string(body), "too many event streams") {
		t.Fatalf("the seventeenth marked stream = outer %d, inner %d (%s)", outer, status, body)
	}
	for _, h := range streams {
		h.s.close(h.cancel, h.finished)
	}
}

// rehandshake founds a fresh channel session for the same device, as
// an app that restarted does, and answers the device holding it.
func (d *e2eDevice) rehandshake(t *testing.T) *e2eDevice {
	t.Helper()
	return d.rehandshakeTo(t, d.y.channel.Public)
}

// rehandshakeTo is rehandshake to the yard key yardPublic, which a device
// that holds no copy of the yard's own has from an answer it was given.
func (d *e2eDevice) rehandshakeTo(t *testing.T, yardPublic []byte) *e2eDevice {
	t.Helper()
	first, opening, err := yardkey.StartChannelHandshake(d.key, yardPublic)
	if err != nil {
		t.Fatal(err)
	}
	w := d.post(t, first, "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("the fresh handshake = %d %s", w.Code, w.Body)
	}
	session, err := opening.OpenAnswer(w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return &e2eDevice{y: d.y, handler: d.handler, key: d.key, session: session}
}

func sessionsFor(y *pairYard, user, device string) int {
	y.u.mu.Lock()
	defer y.u.mu.Unlock()
	n := 0
	for _, s := range y.u.e2eSessions {
		if s.user == user && s.device == device {
			n++
		}
	}
	return n
}

func sessionEntry(t *testing.T, y *pairYard, d *e2eDevice) *e2eSession {
	t.Helper()
	y.u.mu.Lock()
	defer y.u.mu.Unlock()
	s := y.u.e2eSessions[string(d.session.Tag())]
	if s == nil {
		t.Fatal("the yard's channel session is missing")
	}
	return s
}

// A call replayed whole down the link, and one whose first record's
// sequence was rewritten, are each refused and mark nothing: the
// honest call, and the honest record, still open after them.
func TestE2EReplayedCallRefusedAtTheYard(t *testing.T) {
	y := newPairYard(t)
	d := newE2EDevice(t, y, "sam", "phone")
	plain := bindFor("", false)
	build := func() ([]byte, []byte) {
		t.Helper()
		rawHead, err := json.Marshal(e2eRequestHead{Method: http.MethodGet, Path: "/user/me"})
		if err != nil {
			t.Fatal(err)
		}
		call, err := yardkey.NewCallID()
		if err != nil {
			t.Fatal(err)
		}
		run, err := d.session.SealWhole(call, plain, joinE2EHead(rawHead, nil))
		if err != nil {
			t.Fatal(err)
		}
		return run, call
	}
	run, call := build()
	if w := d.post(t, run, base64.RawURLEncoding.EncodeToString(call), nil); w.Code != http.StatusOK {
		t.Fatalf("the first call = %d", w.Code)
	}
	if w := d.post(t, run, base64.RawURLEncoding.EncodeToString(call), nil); w.Code != http.StatusBadRequest {
		t.Errorf("a call replayed whole = %d, want 400", w.Code)
	}
	// Sequence rewritten in the first record's clear header.
	run, call = build()
	records, err := yardkey.SplitRecords(run)
	if err != nil || len(records) == 0 {
		t.Fatal(err)
	}
	rewritten := append([]byte(nil), records[0]...)
	at := yardkey.E2ETagSize + yardkey.E2ECallSize
	binary.BigEndian.PutUint64(rewritten[at:at+8], binary.BigEndian.Uint64(rewritten[at:at+8])+1000)
	var forged []byte
	forged = yardkey.FrameRecord(forged, rewritten)
	for _, m := range records[1:] {
		forged = yardkey.FrameRecord(forged, m)
	}
	if w := d.post(t, forged, base64.RawURLEncoding.EncodeToString(call), nil); w.Code != http.StatusBadRequest {
		t.Errorf("a call with its sequence rewritten = %d, want 400", w.Code)
	}
	if w := d.post(t, run, base64.RawURLEncoding.EncodeToString(call), nil); w.Code != http.StatusOK {
		t.Errorf("the honest call after a rewritten one = %d, want 200", w.Code)
	}
}

// Sessions in server memory are capped per device, replaced by a
// fresh handshake, and expire when idle: repeated restarts leave at
// most the cap resident, the replaced session is refused, and a
// session left past its idle bound is refused and dropped.
func TestE2ESessionsReplacedCappedAndExpired(t *testing.T) {
	y := newPairYard(t)
	d := newE2EDevice(t, y, "sam", "phone")
	plain := bindFor("", false)
	old := sessionEntry(t, y, d)
	// A replaced session ends its quiet events stream at the next
	// liveness check and releases the stream slot it was holding.
	y.u.recheck = 20 * time.Millisecond
	head, err := y.st.Head(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	marked := bindFor(E2EStreamLongLived, true)
	path := "/user/events?after=" + strconv.FormatUint(head, 10)
	oldStream, _, stopOld, oldFinished := d.openStream(t, http.MethodGet, path, marked, marked)
	defer stopOld()
	if status, _ := oldStream.readHead(); status != http.StatusOK {
		t.Fatalf("the old session's events head = %d", status)
	}
	fresh := d
	for range 4 {
		fresh = fresh.rehandshake(t)
		if n := sessionsFor(y, "sam", "phone"); n != e2eSessionsPerDevice {
			t.Fatalf("sessions resident after a restart = %d, want the cap %d", n, e2eSessionsPerDevice)
		}
	}
	select {
	case <-oldFinished:
	case <-time.After(5 * time.Second):
		t.Fatal("the replaced session's events stream did not end")
	}
	for {
		msg, err := oldStream.reader.ReadMessage()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := oldStream.opener.Open(msg); err != nil {
			t.Fatal(err)
		}
	}
	if err := oldStream.opener.Close(); !errors.Is(err, yardkey.ErrTruncated) {
		t.Errorf("the replaced session's stream closed with %v, want no final record", err)
	}
	y.u.mu.Lock()
	streams := y.u.streams["sam\x00phone"]
	y.u.mu.Unlock()
	if streams != 0 {
		t.Errorf("stream slots held after session replacement = %d, want none", streams)
	}
	if y.u.touchE2ESessionIfLive(old) {
		t.Error("a replaced session was still live for another record")
	}
	if outer, _, _, _, _ := d.call(t, http.MethodGet, "/user/me", nil, nil, plain, plain); outer != http.StatusUnauthorized {
		t.Errorf("a call on the replaced session = %d, want 401", outer)
	}
	if outer, _, status, _, _ := fresh.call(t, http.MethodGet, "/user/me", nil, nil, plain, plain); outer != http.StatusOK || status != http.StatusOK {
		t.Errorf("a call on the fresh session = outer %d, inner %d", outer, status)
	}
	y.u.now = func() time.Time { return time.Now().Add(e2eSessionMaxIdle + time.Hour) }
	defer func() { y.u.now = time.Now }()
	if outer, _, _, _, _ := fresh.call(t, http.MethodGet, "/user/me", nil, nil, plain, plain); outer != http.StatusUnauthorized {
		t.Errorf("a call on an idle-expired session = %d, want 401", outer)
	}
	if n := sessionsFor(y, "sam", "phone"); n != 0 {
		t.Errorf("sessions resident after expiry = %d, want none", n)
	}
}

// Removal purges the device's sessions and refuses what comes next —
// a call on the old session, a live stream's next record, a fresh
// handshake — leaving a second device untouched. A whole call
// admitted before the removal commits finishes; a streamed record
// after it is no new admission's to seal.
func TestE2ERemovalPurgesSessionsAndAdmittedFinishes(t *testing.T) {
	t.Run("startup reconciliation catches an earlier removal", func(t *testing.T) {
		y := newPairYard(t)
		newE2EDevice(t, y, "sam", "phone")
		if _, err := y.st.RemoveDevice(context.Background(), "sam", "phone", "jo"); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		defer func() {
			cancel()
			<-done
		}()
		go func() {
			y.u.watchE2ERemovals(ctx)
			close(done)
		}()
		for deadline := time.Now().Add(5 * time.Second); sessionsFor(y, "sam", "phone") != 0 && time.Now().Before(deadline); {
			time.Sleep(10 * time.Millisecond)
		}
		if n := sessionsFor(y, "sam", "phone"); n != 0 {
			t.Fatalf("sessions resident after startup reconciliation = %d, want none", n)
		}
	})

	y := newPairYard(t)
	d := newE2EDevice(t, y, "sam", "phone")
	other := newE2EDevice(t, y, "jo", "tablet")
	plain := bindFor("", false)
	ctx := context.Background()
	watchCtx, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	go y.u.watchE2ERemovals(watchCtx)

	head, err := y.st.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	marked := bindFor(E2EStreamLongLived, true)
	s, _, cancel, finished := d.openStream(t, http.MethodGet,
		"/user/events?after="+strconv.FormatUint(head, 10), marked, marked)
	if status, _ := s.readHead(); status != http.StatusOK {
		t.Fatalf("the stream's head = %d", status)
	}

	// Admitted before the removal: a whole answer not yet sealed, and
	// a stream whose first record has crossed.
	session := sessionEntry(t, y, d)
	admittedCall, err := yardkey.NewCallID()
	if err != nil {
		t.Fatal(err)
	}
	admittedOuter := httptest.NewRecorder()
	admitted := newE2EAnswerWriter(admittedOuter, session, admittedCall, plain, false)
	admitted.WriteHeader(http.StatusOK)
	if _, err := admitted.Write([]byte("admitted result")); err != nil {
		t.Fatal(err)
	}
	streamCall, err := yardkey.NewCallID()
	if err != nil {
		t.Fatal(err)
	}
	streamOuter := httptest.NewRecorder()
	streamed := newE2EAnswerWriter(streamOuter, session, streamCall, plain, false)
	liveReq := httptest.NewRequest(http.MethodGet, "/user/me", nil)
	streamed.live = func() bool { return y.u.touchE2ESessionIfLive(session) && y.u.e2eDevice(liveReq, session) }
	streamed.WriteHeader(http.StatusOK)
	if _, err := streamed.Write([]byte("first")); err != nil {
		t.Fatal(err)
	}
	streamed.Flush()
	sealedFirst := streamOuter.Body.Len()
	if sealedFirst == 0 {
		t.Fatal("the admitted stream sealed nothing before the removal")
	}
	if _, err := streamed.Write([]byte("second")); err != nil {
		t.Fatal(err)
	}

	if _, err := y.st.RemoveDevice(ctx, "sam", "phone", "jo"); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); sessionsFor(y, "sam", "phone") != 0 && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	if n := sessionsFor(y, "sam", "phone"); n != 0 {
		t.Errorf("sessions resident after the removal = %d, want purged", n)
	}
	if n := sessionsFor(y, "jo", "tablet"); n != 1 {
		t.Errorf("the second device's sessions after the removal = %d, want its one untouched", n)
	}

	// The streamed next record and final are refused; the stream the
	// device held ends with no event the removal preceded and no final.
	streamed.Flush()
	if err := streamed.finish(); err != nil {
		t.Fatal(err)
	}
	if streamOuter.Body.Len() != sealedFirst {
		t.Errorf("a record was sealed after the removal: %d bytes over %d", streamOuter.Body.Len(), sealedFirst)
	}
	for {
		msg, err := s.reader.ReadMessage()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.opener.Open(msg); err != nil {
			t.Fatal(err)
		}
	}
	<-finished
	if err := s.opener.Close(); !errors.Is(err, yardkey.ErrTruncated) {
		t.Errorf("the removed device's stream closed with %v, want no final record", err)
	}
	cancel()

	// The whole call admitted before the commit finishes sealed.
	if err := admitted.finish(); err != nil {
		t.Fatal(err)
	}
	opened, err := d.session.OpenWhole(admittedOuter.Body.Bytes(), admittedCall, plain)
	if err != nil || !bytes.Contains(opened, []byte("admitted result")) {
		t.Errorf("the admitted call's answer opened %q, %v", opened, err)
	}

	if outer, _, _, _, _ := d.call(t, http.MethodGet, "/user/me", nil, nil, plain, plain); outer != http.StatusUnauthorized {
		t.Errorf("a call on the pre-removal session = %d, want 401", outer)
	}
	first, _, err := yardkey.StartChannelHandshake(d.key, y.channel.Public)
	if err != nil {
		t.Fatal(err)
	}
	if w := d.post(t, first, "", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("a handshake after the removal = %d, want 401", w.Code)
	}
	if n := sessionsFor(y, "sam", "phone"); n != 0 {
		t.Errorf("sessions resident after a refused handshake = %d, want none", n)
	}
	if outer, _, status, _, _ := other.call(t, http.MethodGet, "/user/me", nil, nil, plain, plain); outer != http.StatusOK || status != http.StatusOK {
		t.Errorf("the second device's call = outer %d, inner %d", outer, status)
	}
}

// A user removed with their devices loses their sessions the same
// way (user_removed carries the removal the watcher purges by), and
// another user's device is untouched.
func TestE2EUserRemovalPurgesSessions(t *testing.T) {
	y := newPairYard(t)
	d := newE2EDevice(t, y, "sam", "phone")
	other := newE2EDevice(t, y, "jo", "tablet")
	plain := bindFor("", false)
	ctx := context.Background()
	watchCtx, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	go y.u.watchE2ERemovals(watchCtx)
	if err := y.st.DeleteUser(ctx, "sam", "jo"); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); sessionsFor(y, "sam", "phone") != 0 && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	if n := sessionsFor(y, "sam", "phone"); n != 0 {
		t.Errorf("sessions resident after the user removal = %d, want purged", n)
	}
	if outer, _, _, _, _ := d.call(t, http.MethodGet, "/user/me", nil, nil, plain, plain); outer != http.StatusUnauthorized {
		t.Errorf("a call after its user was removed = %d, want 401", outer)
	}
	if outer, _, status, _, _ := other.call(t, http.MethodGet, "/user/me", nil, nil, plain, plain); outer != http.StatusOK || status != http.StatusOK {
		t.Errorf("the other user's call = outer %d, inner %d", outer, status)
	}
}

// At the yard's own listener /user/e2e is what it was: without a
// credential 401, with one 404, since no token authenticates there and
// the path was never served on the LAN.
func TestE2EPathUnchangedOnTheLAN(t *testing.T) {
	_, u, token := userYard(t)
	h := u.Handler()
	if w := ask(h, http.MethodPost, E2EPath, "", strings.NewReader("x")); w.Code != http.StatusUnauthorized {
		t.Errorf("POST %s on the LAN with no credential = %d, want 401", E2EPath, w.Code)
	}
	if w := ask(h, http.MethodPost, E2EPath, token, strings.NewReader("x")); w.Code != http.StatusNotFound {
		t.Errorf("POST %s on the LAN with a token = %d, want 404", E2EPath, w.Code)
	}
}
