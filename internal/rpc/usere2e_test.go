package rpc

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

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

// call makes one enveloped whole call and opens its sealed answer,
// answering the inner status, headers and body beside the outer
// status. sealMarker and wireMarker are told apart so a test can
// rewrite the marker between the device and the yard.
func (d *e2eDevice) call(t *testing.T, method, path string, header http.Header, body []byte, seal, wire *yardkey.FirstBinding) (outer int, outerHeader http.Header, status int, innerHeader http.Header, innerBody []byte) {
	t.Helper()
	rawHead, err := json.Marshal(e2eRequestHead{Method: method, Path: path, Header: header})
	if err != nil {
		t.Fatal(err)
	}
	call, err := yardkey.NewCallID()
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
	outer, outerHeader = w.Code, w.Result().Header
	if outer != http.StatusOK {
		return outer, outerHeader, 0, nil, w.Body.Bytes()
	}
	plain, err := d.session.OpenWhole(w.Body.Bytes(), call, seal)
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

// Enveloped calls in flight at once on one session all open: each
// message carries its own number and opens with its own copied cipher
// state in its serving goroutine, in no sealing order.
func TestE2EConcurrentCallsOnOneSession(t *testing.T) {
	y := newPairYard(t)
	d := newE2EDevice(t, y, "sam", "phone")
	plain := bindFor("", false)
	const n = 16
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
