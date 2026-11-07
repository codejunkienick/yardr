package rpc

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/yardkey"
)

// pairYard is a yard with the operator jo, the user sam and no device, on a
// listener of its own at 127.0.0.1:0, and a clock for its device pairings
// that the test moves. It holds a channel key too, in a file of its own,
// as a yard that pairs by QR does, and an identity key, whose public half
// is what the edge's map holds and a voucher is checked against.
type pairYard struct {
	st       *store.Store
	u        *UserServer
	base     string
	channel  yardkey.ChannelKey
	identity ed25519.PrivateKey
	late     atomic.Int64 // how far the clock is ahead, in nanoseconds
}

func newPairYard(t *testing.T) *pairYard {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "yard.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close(context.Background()) })
	for _, name := range []string{"jo", "sam"} {
		if _, err := st.AddUser(ctx, name, "jo"); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetSetting(ctx, store.OperatorSetting, "jo", "jo"); err != nil {
		t.Fatal(err)
	}
	y := &pairYard{st: st, u: NewUserServer(st, func(context.Context) string { return "south" }, nil)}
	channelPath := filepath.Join(t.TempDir(), "channel.key")
	channel, _, err := yardkey.EnsureChannel(channelPath)
	if err != nil {
		t.Fatal(err)
	}
	y.channel = channel
	y.u.SetChannelKeyFile(channelPath)
	identityPath := filepath.Join(t.TempDir(), "peer.key")
	if y.identity, _, err = yardkey.Ensure(identityPath); err != nil {
		t.Fatal(err)
	}
	y.u.SetIdentityKeyFile(identityPath)
	st.SetDevicePairClock(func() time.Time { return time.Now().Add(time.Duration(y.late.Load())) })
	y.base = listen(t, y.u, 5*time.Second)
	return y
}

// open opens a pairing for the device of the user and answers its code.
func (y *pairYard) open(t *testing.T, user, device string) string {
	t.Helper()
	p, err := y.st.DevicePairOpen(context.Background(), user, device, "jo")
	if err != nil {
		t.Fatal(err)
	}
	return p.Code
}

// post sends body to path over the yard's listener and answers the status
// and the body, with the token when there is one.
func (y *pairYard) post(t *testing.T, path, token, body string) (int, string) {
	t.Helper()
	return y.do(t, http.MethodPost, path, token, nil, body)
}

// do is post for any method, with the headers of header besides.
func (y *pairYard) do(t *testing.T, method, path, token string, header http.Header, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, y.base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for name, values := range header {
		req.Header[name] = values
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	answer, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(answer))
}

func (y *pairYard) redeem(t *testing.T, code string) (int, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"code": code})
	return y.post(t, PairPath, "", string(body))
}

// redeemAs is redeem with the edge's word for who presents it: one
// PresenterHeader line for each of presenters, so none is redeem.
func (y *pairYard) redeemAs(t *testing.T, code string, presenters ...string) (int, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"code": code})
	return y.do(t, http.MethodPost, PairPath, "", http.Header{PresenterHeader: presenters}, string(body))
}

// checkVoucher checks that voucher verifies against the yard's public key,
// as the edge's map holds it, at the time at; that it states the user and
// device it was issued for, the yard's name and an expiry VoucherLife from
// at; and that it verifies nowhere from that expiry on.
func (y *pairYard) checkVoucher(t *testing.T, voucher, user, device string, at time.Time) {
	t.Helper()
	public := store.Public(y.identity)
	v, err := yardkey.VerifyVoucher(public, voucher, at)
	if err != nil {
		t.Fatalf("the voucher %q does not verify against the yard's public key: %v", voucher, err)
	}
	if want := at.Add(VoucherLife).Truncate(time.Second); v.User != user || v.Device != device || v.Yard != "south" || !v.Expires.Equal(want) {
		t.Errorf("the voucher states %+v, want %s's %s at south until %s", v, user, device, want.UTC())
	}
	if _, err := yardkey.VerifyVoucher(public, voucher, v.Expires); !errors.Is(err, yardkey.ErrVoucherExpired) {
		t.Errorf("the voucher at its expiry = %v, want it expired", err)
	}
}

// wrong is a code that is one, and not the code.
func wrong(code string) string {
	if strings.HasPrefix(code, "0") {
		return "1" + code[1:]
	}
	return "0" + code[1:]
}

// A device that calls with the code of the open pairing is answered with
// its token, once: the token then calls as the user the pairing was opened
// for, the store has its hash and never the token, and the device is as one
// added by hand, with the event saying which pairing it came by. The yard
// had no device before, so nothing else was under /user/.
func TestDevicePairRedemptionForms(t *testing.T) {
	t.Run("plain", testDevicePairedByItsCode)
	t.Run("sealed", testDevicePairedBySealedCode)
	t.Run("sealed, yard cannot sign a voucher", testDevicePairedWhereNoVoucherCanBeSigned)
	t.Run("pake", testDevicePairedByPAKECode)
}

func testDevicePairedByItsCode(t *testing.T) {
	y := newPairYard(t)
	ctx := context.Background()
	if code, _ := y.post(t, PairPath, "", `{"code":"K7QM-2XPD"}`); code != http.StatusNotFound {
		t.Fatalf("POST %s with no pairing open, in a yard with no device = %d, want 404", PairPath, code)
	}
	code := y.open(t, "sam", "phone")
	// As it is typed: without the dash, in small letters.
	status, body := y.redeem(t, strings.ToLower(strings.ReplaceAll(code, "-", "")))
	var got Paired
	if err := json.Unmarshal([]byte(body), &got); status != http.StatusOK || err != nil {
		t.Fatalf("the code = %d %s (%v)", status, body, err)
	}
	if !strings.HasPrefix(got.Token, store.DeviceTokenPrefix) || got.User != "sam" || got.Device != "phone" || got.Yard != "south" {
		t.Fatalf("the code is answered %+v", got)
	}
	// A device that pinned no channel key is given no voucher: the plain
	// answer is exactly what it was.
	if got.Voucher != "" || strings.Contains(body, "voucher") {
		t.Errorf("the plain answer holds a voucher: %s", body)
	}
	if status, me := y.get(t, "/user/me", got.Token); status != http.StatusOK || me != `{"user":"sam","said_by":"phone","yard":"south","yardmaster":"yardmaster"}` {
		t.Errorf("/user/me with the token = %d %s", status, me)
	}
	devices, err := y.st.Devices(ctx, "")
	if err != nil || len(devices) != 1 || devices[0].User != "sam" || devices[0].Name != "phone" || devices[0].CreatedBy != "jo" {
		t.Fatalf("devices = %+v, %v", devices, err)
	}
	added := eventsOfKind(t, y.st, "device_added")
	if len(added) != 1 || added[0].Actor != "jo" || added[0].Data["user"] != "sam" || added[0].Data["device"] != "phone" || added[0].Data["pairing"] == "" {
		t.Fatalf("device_added = %+v", added)
	}
	opened := eventsOfKind(t, y.st, "device_pair_opened")
	if len(opened) != 1 || opened[0].Data["pairing"] != added[0].Data["pairing"] {
		t.Fatalf("device_pair_opened = %+v", opened)
	}
	// Neither the code nor the token is in any event.
	all, err := y.st.Events(ctx, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	said, _ := json.Marshal(all)
	for _, secret := range []string{code, strings.ReplaceAll(code, "-", ""), got.Token} {
		if strings.Contains(string(said), secret) {
			t.Errorf("the events have %q", secret)
		}
	}
}

func (y *pairYard) get(t *testing.T, path, token string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, y.base+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	answer, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(answer))
}

// A code is good once: the second call with it finds no pairing, and so no
// path. The yard has a device by then, so the path is answered as every
// path under /user/ is without a credential, and as one that is not served
// with one. No second device is added.
func TestDevicePairReplayForms(t *testing.T) {
	t.Run("plain", testDevicePairCodeIsGoodOnce)
	t.Run("sealed", testDevicePairSealedReplay)
	t.Run("pake", testDevicePairPAKEReplay)
}

func testDevicePairCodeIsGoodOnce(t *testing.T) {
	y := newPairYard(t)
	code := y.open(t, "jo", "phone")
	status, body := y.redeem(t, code)
	var got Paired
	if err := json.Unmarshal([]byte(body), &got); status != http.StatusOK || err != nil {
		t.Fatalf("the code = %d %s (%v)", status, body, err)
	}
	if status, body := y.redeem(t, code); status != http.StatusUnauthorized || strings.Contains(body, "token") {
		t.Errorf("the code again = %d %s, want 401", status, body)
	}
	if status, body := y.post(t, PairPath, got.Token, `{"code":"`+code+`"}`); status != http.StatusNotFound || strings.Contains(body, "yardr-dev-") {
		t.Errorf("the code again, as the device = %d %s, want 404", status, body)
	}
	if devices, err := y.st.Devices(context.Background(), ""); err != nil || len(devices) != 1 {
		t.Fatalf("devices = %+v, %v", devices, err)
	}
	if got := eventsOfKind(t, y.st, "device_pair_refused"); len(got) != 0 {
		t.Errorf("a pairing that was used is logged as refused: %+v", got)
	}
}

// A wrong code is refused and counted, and the fifth ends the pairing: the
// right code is good for nothing then. What is no code at all is no try.
func TestDevicePairWrongCodesAndRewrites(t *testing.T) {
	t.Run("plain", testDevicePairWrongCodeFiveTimes)
	t.Run("sealed", testDevicePairSealedWrongCodeAndGarbage)
	t.Run("pake", testDevicePairPAKERelayRewrites)
}

func testDevicePairWrongCodeFiveTimes(t *testing.T) {
	y := newPairYard(t)
	code := y.open(t, "jo", "phone")
	for _, body := range []string{``, `[]`, `{"code":"K7QM"}`, `{"code":"K7QM-2XPD-9"}`, `{"code":17}`, `{}`} {
		if status, answer := y.post(t, PairPath, "", body); status != http.StatusBadRequest {
			t.Errorf("POST %s %s = %d %s, want 400", PairPath, body, status, answer)
		}
	}
	for try := 1; try <= store.MaxTries; try++ {
		if status, body := y.redeem(t, wrong(code)); status != http.StatusForbidden || body != `{"refused":"wrong_code"}` {
			t.Fatalf("wrong code %d = %d %s, want 403 wrong_code", try, status, body)
		}
	}
	// No device in the yard, and no pairing: nothing is under /user/.
	if status, body := y.redeem(t, code); status != http.StatusNotFound || strings.Contains(body, "yardr-dev-") {
		t.Errorf("the right code after %d wrong ones = %d %s, want 404", store.MaxTries, status, body)
	}
	if devices, err := y.st.Devices(context.Background(), ""); err != nil || len(devices) != 0 {
		t.Fatalf("devices = %+v, %v", devices, err)
	}
	ended := eventsOfKind(t, y.st, "device_pair_refused")
	if len(ended) != 1 || ended[0].Data["reason"] != store.WrongCode || ended[0].Data["wrong_codes"] != float64(store.MaxTries) ||
		ended[0].Data["user"] != "jo" || ended[0].Data["device"] != "phone" {
		t.Fatalf("device_pair_refused = %+v", ended)
	}

	// Four wrong ones leave the code good, and another pairing may be opened
	// once this one has ended.
	code = y.open(t, "jo", "phone")
	for try := 1; try < store.MaxTries; try++ {
		y.redeem(t, wrong(code))
	}
	if status, body := y.redeem(t, code); status != http.StatusOK {
		t.Errorf("the right code after %d wrong ones = %d %s", store.MaxTries-1, status, body)
	}
}

// A redemption that names who presents it (PresenterHeader), as the edge
// names the signed-in account of one it forwards, is the pairing's user's
// alone: the address is mapped to a user as web_jwt_users has it, whatever
// its case. Any other presenter is refused as a wrong code is, with the same
// answer and the same count, and the code is neither looked at nor used, so
// the user it was opened for can still redeem it, but for an address that
// maps to nobody where the pairing's user maps to nobody too, which redeems
// as with no presenter. The code is still required of the right presenter.
// The header is read on this call alone.
func TestDevicePairPresenters(t *testing.T) {
	t.Run("plain", testDevicePairPlainPresenter)
	t.Run("sealed", testDevicePairSealedPresenter)
	t.Run("pake", testDevicePairPAKEPresenter)
}

func testDevicePairPlainPresenter(t *testing.T) {
	y := newPairYard(t)
	ctx := context.Background()
	if err := y.st.SetSetting(ctx, store.WebJWTUsersSetting, "jo@example.net=jo, Sam@Example.NET=sam", "jo"); err != nil {
		t.Fatal(err)
	}
	refused := func(what string, status int, body string) {
		t.Helper()
		if status != http.StatusForbidden || body != `{"refused":"wrong_code"}` {
			t.Errorf("%s = %d %s, want 403 wrong_code", what, status, body)
		}
	}
	devices := func(want int) {
		t.Helper()
		if got, err := y.st.Devices(ctx, ""); err != nil || len(got) != want {
			t.Fatalf("devices = %+v, %v, want %d", got, err, want)
		}
	}

	// The right code, presented by another user of the yard and by an
	// address nobody is mapped to, is refused and leaves the code good.
	code := y.open(t, "sam", "phone")
	status, body := y.redeemAs(t, code, "jo@example.net")
	refused("sam's code presented by jo", status, body)
	status, body = y.redeemAs(t, code, "eve@example.net")
	refused("sam's code presented by an address no user is mapped to", status, body)
	devices(0)

	status, body = y.redeemAs(t, code, "SAM@example.net")
	var got Paired
	if err := json.Unmarshal([]byte(body), &got); status != http.StatusOK || err != nil || got.User != "sam" || got.Device != "phone" {
		t.Fatalf("sam's code presented by sam = %d %s (%v)", status, body, err)
	}
	devices(1)

	// They count as wrong codes do, the fifth ending the pairing whichever
	// they are, and a header that is no address names nobody: sent empty,
	// or twice.
	code = y.open(t, "sam", "laptop")
	for _, try := range []struct {
		what       string
		code       string
		presenters []string
	}{
		{"jo", code, []string{"jo@example.net"}},
		{"an address no user is mapped to", code, []string{"eve@example.net"}},
		{"a header with no address", code, []string{""}},
		{"a header twice, sam both times", code, []string{"sam@example.net", "sam@example.net"}},
		{"sam, and a code that is not the code", wrong(code), []string{"sam@example.net"}},
	} {
		status, body := y.redeemAs(t, try.code, try.presenters...)
		refused(try.what, status, body)
	}
	if status, body := y.redeemAs(t, code, "sam@example.net"); status != http.StatusUnauthorized || strings.Contains(body, "yardr-dev-") {
		t.Errorf("sam's code after %d refusals = %d %s, want 401", store.MaxTries, status, body)
	}
	devices(1)
	ended := eventsOfKind(t, y.st, "device_pair_refused")
	if len(ended) != 1 || ended[0].Data["reason"] != store.WrongCode || ended[0].Data["wrong_codes"] != float64(store.MaxTries) ||
		ended[0].Data["user"] != "sam" || ended[0].Data["device"] != "laptop" {
		t.Fatalf("device_pair_refused = %+v", ended)
	}

	// No other call reads it: the device just paired calls as sam, whoever
	// the header says presents the call.
	if status, me := y.do(t, http.MethodGet, "/user/me", got.Token, http.Header{PresenterHeader: {"jo@example.net"}}, ""); status != http.StatusOK ||
		me != `{"user":"sam","said_by":"phone","yard":"south","yardmaster":"yardmaster"}` {
		t.Errorf("/user/me naming jo as the presenter = %d %s", status, me)
	}

	// Where the presenter maps to nobody, a pairing whose user the mapping
	// names stays bound to its opener, while a pairing for an unmapped user
	// redeems as with no presenter.
	if err := y.st.SetSetting(ctx, store.WebJWTUsersSetting, "sam@example.net=sam", "jo"); err != nil {
		t.Fatal(err)
	}
	code = y.open(t, "sam", "tablet")
	status, body = y.redeemAs(t, code, "eve@example.net")
	refused("sam's code presented by an address no user is mapped to, sam mapped", status, body)
	status, body = y.redeemAs(t, code, "sam@example.net")
	if err := json.Unmarshal([]byte(body), &got); status != http.StatusOK || err != nil || got.User != "sam" || got.Device != "tablet" {
		t.Fatalf("sam's code presented by sam = %d %s (%v)", status, body, err)
	}
	devices(2)
	code = y.open(t, "jo", "tablet")
	status, body = y.redeemAs(t, code, "jo@example.net")
	if err := json.Unmarshal([]byte(body), &got); status != http.StatusOK || err != nil || got.User != "jo" || got.Device != "tablet" {
		t.Fatalf("jo's code presented by an unmapped address, jo unmapped = %d %s (%v)", status, body, err)
	}
	devices(3)

	// With no mapping at all every address names nobody, and the code
	// redeems for an unmapped user; with no header the code alone redeems,
	// as it did.
	if err := y.st.SetSetting(ctx, store.WebJWTUsersSetting, "", "jo"); err != nil {
		t.Fatal(err)
	}
	code = y.open(t, "jo", "watch")
	status, body = y.redeemAs(t, code, "owner@example.com")
	if err := json.Unmarshal([]byte(body), &got); status != http.StatusOK || err != nil || got.User != "jo" || got.Device != "watch" {
		t.Fatalf("jo's code presented by an address when web_jwt_users is unset = %d %s (%v)", status, body, err)
	}
	devices(4)
	code = y.open(t, "jo", "buds")
	status, body = y.redeem(t, code)
	if err := json.Unmarshal([]byte(body), &got); status != http.StatusOK || err != nil || got.User != "jo" || got.Device != "buds" {
		t.Fatalf("jo's code with no presenter named = %d %s (%v)", status, body, err)
	}
	devices(5)

	// Accepted: a teammate whose line was removed while the edge still
	// holds them active redeems the owner's pairing with its code, since
	// neither maps to a user then. The code is still required.
	if err := y.st.SetSetting(ctx, store.WebJWTUsersSetting, "sam@example.net=sam", "jo"); err != nil {
		t.Fatal(err)
	}
	code = y.open(t, "jo", "earbuds")
	if err := y.st.SetSetting(ctx, store.WebJWTUsersSetting, "", "jo"); err != nil {
		t.Fatal(err)
	}
	status, body = y.redeemAs(t, code, "sam@example.net")
	if err := json.Unmarshal([]byte(body), &got); status != http.StatusOK || err != nil || got.User != "jo" || got.Device != "earbuds" {
		t.Fatalf("jo's code presented by a removed teammate's address = %d %s (%v)", status, body, err)
	}
	devices(6)
}

// A code runs out after store.TTL: it is good a second before, and after
// it the path is gone and no device is added.
func TestDevicePairCodeRunsOut(t *testing.T) {
	y := newPairYard(t)
	code := y.open(t, "jo", "phone")
	y.late.Store(int64(store.TTL + time.Second))
	if status, body := y.redeem(t, code); status != http.StatusNotFound || strings.Contains(body, "yardr-dev-") {
		t.Errorf("the code after %s = %d %s, want 404", store.TTL, status, body)
	}
	if devices, err := y.st.Devices(context.Background(), ""); err != nil || len(devices) != 0 {
		t.Fatalf("devices = %+v, %v", devices, err)
	}
	if ended := eventsOfKind(t, y.st, "device_pair_refused"); len(ended) != 1 || ended[0].Data["reason"] != store.TimedOut {
		t.Fatalf("device_pair_refused = %+v", ended)
	}
	code = y.open(t, "jo", "phone")
	y.late.Add(int64(store.TTL - time.Second))
	if status, body := y.redeem(t, code); status != http.StatusOK {
		t.Errorf("the code a second before it runs out = %d %s", status, body)
	}
}

// The path takes a pairing's code and nothing else, and only as a POST:
// with a pairing open, every other path under /user/ is as it was, and the
// body of a request that is refused there is still not read.
func TestDevicePairPathIsTheOnlyOneOpened(t *testing.T) {
	st, u, token := userYard(t)
	h := u.Handler()
	if _, err := st.DevicePairOpen(context.Background(), "sam", "laptop", "jo"); err != nil {
		t.Fatal(err)
	}
	for _, p := range append([][2]string{{http.MethodGet, PairPath}, {http.MethodPut, PairPath}, {http.MethodPost, PairPath + "/"}, {http.MethodPost, PairPath + "/more"}}, userPaths...) {
		if w := ask(h, p[0], p[1], "", unread{t}); w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a credential, a pairing open = %d, want 401", p[0], p[1], w.Code)
		}
	}
	if w := ask(h, http.MethodGet, "/user/me", token, nil); w.Code != http.StatusOK {
		t.Errorf("/user/me as a device, a pairing open = %d", w.Code)
	}
	// Ended by the operator, the path is as the others.
	if _, err := st.DevicePairClose(context.Background(), "jo"); err != nil {
		t.Fatal(err)
	}
	if w := ask(h, http.MethodPost, PairPath, "", unread{t}); w.Code != http.StatusUnauthorized {
		t.Errorf("POST %s after the pairing was ended = %d, want 401", PairPath, w.Code)
	}
}

// sealedCode begins a pairing handshake to the yard's channel key with
// code as its payload, for the device key, and answers the first
// message and the opening that reads the answer.
func (y *pairYard) sealedCode(t *testing.T, device yardkey.ChannelKey, code string) (first []byte, opening *yardkey.PairingOpening) {
	t.Helper()
	first, opening, err := yardkey.StartPairingHandshake(device, y.channel.Public, code)
	if err != nil {
		t.Fatal(err)
	}
	return first, opening
}

// postSealed sends one sealed first message to the pair path, with the
// edge's presenter word where presenters are given.
func (y *pairYard) postSealed(t *testing.T, first []byte, presenters ...string) (int, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"sealed": base64.RawURLEncoding.EncodeToString(first)})
	if len(presenters) == 0 {
		return y.post(t, PairPath, "", string(body))
	}
	return y.do(t, http.MethodPost, PairPath, "", http.Header{PresenterHeader: presenters}, string(body))
}

// openSealed reads a sealed answer body and opens it with the opening
// that began the handshake.
func openSealed(t *testing.T, opening *yardkey.PairingOpening, body string) Paired {
	t.Helper()
	var sealed struct {
		Sealed string `json:"sealed"`
	}
	if err := json.Unmarshal([]byte(body), &sealed); err != nil || sealed.Sealed == "" {
		t.Fatalf("the sealed answer = %s (%v)", body, err)
	}
	reply, err := base64.RawURLEncoding.DecodeString(sealed.Sealed)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := opening.OpenAnswer(reply)
	if err != nil {
		t.Fatalf("the device could not open its answer: %v", err)
	}
	var got Paired
	if err := json.Unmarshal(plain, &got); err != nil {
		t.Fatalf("the opened answer = %q (%v)", plain, err)
	}
	return got
}

// A device that pairs by the QR's channel key redeems its code inside
// a Noise handshake to that key: neither the code nor the token
// crosses readable, the answer opens only with the device key the
// handshake proved, and the device is recorded with that key pinned
// beside its token's hash. Its token then calls as its user.
func testDevicePairedBySealedCode(t *testing.T) {
	y := newPairYard(t)
	ctx := context.Background()
	at := time.Now()
	y.u.now = func() time.Time { return at }
	device, err := yardkey.GenerateChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	code := y.open(t, "sam", "phone")
	first, opening := y.sealedCode(t, device, code)
	status, body := y.postSealed(t, first)
	if status != http.StatusOK || strings.Contains(body, code) || strings.Contains(body, "yardr-dev-") || strings.Contains(body, yardkey.VoucherPrefix) {
		t.Fatalf("the sealed code = %d %s, want 200 with neither the code, a token nor a voucher readable", status, body)
	}
	got := openSealed(t, opening, body)
	if !strings.HasPrefix(got.Token, store.DeviceTokenPrefix) || got.User != "sam" || got.Device != "phone" || got.Yard != "south" {
		t.Fatalf("the sealed code is answered %+v", got)
	}
	// The answer holds the yard's registration voucher for the device,
	// which verifies against the yard's public key and says, in the open,
	// whose device it is and when it lapses.
	y.checkVoucher(t, got.Voucher, "sam", "phone", at)
	// The answer opens with that device's key alone.
	stranger, err := yardkey.GenerateChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	_, strangerOpening := y.sealedCode(t, stranger, code)
	var sealed struct {
		Sealed string `json:"sealed"`
	}
	if err := json.Unmarshal([]byte(body), &sealed); err != nil {
		t.Fatal(err)
	}
	reply, _ := base64.RawURLEncoding.DecodeString(sealed.Sealed)
	if _, err := strangerOpening.OpenAnswer(reply); err == nil {
		t.Error("another device's key opened the sealed answer")
	}
	if status, me := y.get(t, "/user/me", got.Token); status != http.StatusOK || me != `{"user":"sam","said_by":"phone","yard":"south","yardmaster":"yardmaster"}` {
		t.Errorf("/user/me with the sealed token = %d %s", status, me)
	}
	devices, err := y.st.Devices(ctx, "")
	if err != nil || len(devices) != 1 || devices[0].ChannelKey != yardkey.ChannelPublic(device) {
		t.Fatalf("devices = %+v, %v, want sam's phone with the device key pinned", devices, err)
	}
}

// A yard that cannot sign a voucher — told no identity key file, one it
// cannot read, or a name no voucher can carry — still answers the pairing
// with the token, which is never lost for want of an extra, and none of
// the three leaves the device unpaired; the answer holds no voucher, which
// the device asks for when it registers.
func testDevicePairedWhereNoVoucherCanBeSigned(t *testing.T) {
	for name, spoil := range map[string]func(*pairYard){
		"no identity key file told": func(y *pairYard) { y.u.SetIdentityKeyFile("") },
		"an identity key file that is gone": func(y *pairYard) {
			y.u.SetIdentityKeyFile(filepath.Join(t.TempDir(), "gone.key"))
		},
		"a yard name with a line break": func(y *pairYard) {
			y.u.yard = func(context.Context) string { return "so\nuth" }
		},
	} {
		t.Run(name, func(t *testing.T) {
			y := newPairYard(t)
			spoil(y)
			device, err := yardkey.GenerateChannelKey()
			if err != nil {
				t.Fatal(err)
			}
			code := y.open(t, "sam", "phone")
			first, opening := y.sealedCode(t, device, code)
			status, body := y.postSealed(t, first)
			if status != http.StatusOK {
				t.Fatalf("the sealed code = %d %s, want 200", status, body)
			}
			got := openSealed(t, opening, body)
			if !strings.HasPrefix(got.Token, store.DeviceTokenPrefix) || got.User != "sam" || got.Device != "phone" || got.Voucher != "" {
				t.Errorf("the sealed code is answered %+v, want the token and no voucher", got)
			}
			if devices, err := y.st.Devices(context.Background(), ""); err != nil || len(devices) != 1 {
				t.Errorf("devices = %+v, %v, want sam's phone paired", devices, err)
			}
		})
	}
}

// A sealed transcript recorded and replayed buys nothing: the code it
// carries was used by the first redemption, so the replay is answered
// as a used code is and adds no device and answers no token; replayed
// against the next pairing it counts as the wrong code it now is.
func testDevicePairSealedReplay(t *testing.T) {
	y := newPairYard(t)
	ctx := context.Background()
	device, err := yardkey.GenerateChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	code := y.open(t, "sam", "phone")
	first, _ := y.sealedCode(t, device, code)
	if status, body := y.postSealed(t, first); status != http.StatusOK {
		t.Fatalf("the sealed code = %d %s", status, body)
	}
	if status, body := y.postSealed(t, first); status != http.StatusUnauthorized || strings.Contains(body, "yardr-dev-") {
		t.Errorf("the sealed transcript again = %d %s, want 401 and no token", status, body)
	}
	// Against a fresh pairing the same first message opens — it is a
	// well-made handshake — and its spent code counts as a wrong one.
	y.open(t, "jo", "phone")
	if status, body := y.postSealed(t, first); status != http.StatusForbidden || body != `{"refused":"wrong_code"}` {
		t.Errorf("the sealed transcript against a new pairing = %d %s, want 403 wrong_code", status, body)
	}
	if devices, err := y.st.Devices(ctx, ""); err != nil || len(devices) != 1 {
		t.Fatalf("devices after the replays = %+v, %v, want still the one", devices, err)
	}
}

// A sealed code is held as a plain one is: a wrong code inside a
// handshake is counted and the fifth ends the pairing. What never
// opens — bytes that are no handshake, a handshake to another yard's
// key, base64 of neither — is 400 and no try, so it cannot end a
// pairing it never read a code from.
func testDevicePairSealedWrongCodeAndGarbage(t *testing.T) {
	y := newPairYard(t)
	device, err := yardkey.GenerateChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	code := y.open(t, "jo", "phone")
	otherYard, err := yardkey.GenerateChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	foreign, _, err := yardkey.StartPairingHandshake(device, otherYard.Public, code)
	if err != nil {
		t.Fatal(err)
	}
	for what, first := range map[string][]byte{"no handshake": []byte("not a handshake"), "another yard's key": foreign} {
		if status, body := y.postSealed(t, first); status != http.StatusBadRequest {
			t.Errorf("a sealed body that is %s = %d %s, want 400", what, status, body)
		}
	}
	if status, body := y.post(t, PairPath, "", `{"sealed":"!!!"}`); status != http.StatusBadRequest {
		t.Errorf("a sealed body that is no base64 = %d %s, want 400", status, body)
	}
	for try := 1; try < store.MaxTries; try++ {
		first, _ := y.sealedCode(t, device, wrong(code))
		if status, body := y.postSealed(t, first); status != http.StatusForbidden || body != `{"refused":"wrong_code"}` {
			t.Fatalf("sealed wrong code %d = %d %s, want 403 wrong_code", try, status, body)
		}
	}
	// The garbage above was no try: the right code still redeems, as
	// the fifth refusal would have ended the pairing.
	first, opening := y.sealedCode(t, device, code)
	status, body := y.postSealed(t, first)
	if status != http.StatusOK {
		t.Fatalf("the sealed right code after garbage and %d wrong ones = %d %s", store.MaxTries-1, status, body)
	}
	if got := openSealed(t, opening, body); got.User != "jo" || got.Device != "phone" {
		t.Fatalf("the sealed right code is answered %+v", got)
	}

	// And the fifth wrong sealed code ends a pairing as a plain one.
	code = y.open(t, "jo", "laptop")
	for try := 1; try <= store.MaxTries; try++ {
		first, _ := y.sealedCode(t, device, wrong(code))
		y.postSealed(t, first)
	}
	first, _ = y.sealedCode(t, device, code)
	if status, body := y.postSealed(t, first); status != http.StatusUnauthorized || strings.Contains(body, "yardr-dev-") {
		t.Errorf("the sealed right code after %d wrong ones = %d %s, want 401", store.MaxTries, status, body)
	}
}

// A sealed redemption that names who presents it is the pairing's
// user's alone, as a plain one is: another presenter is refused as a
// wrong code, and the code is still good for its user, whose device
// is then recorded with its key pinned.
func testDevicePairSealedPresenter(t *testing.T) {
	y := newPairYard(t)
	ctx := context.Background()
	if err := y.st.SetSetting(ctx, store.WebJWTUsersSetting, "jo@example.net=jo, sam@example.net=sam", "jo"); err != nil {
		t.Fatal(err)
	}
	device, err := yardkey.GenerateChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	code := y.open(t, "sam", "phone")
	first, _ := y.sealedCode(t, device, code)
	if status, body := y.postSealed(t, first, "jo@example.net"); status != http.StatusForbidden || body != `{"refused":"wrong_code"}` {
		t.Fatalf("sam's sealed code presented by jo = %d %s, want 403 wrong_code", status, body)
	}
	first, opening := y.sealedCode(t, device, code)
	status, body := y.postSealed(t, first, "sam@example.net")
	if status != http.StatusOK {
		t.Fatalf("sam's sealed code presented by sam = %d %s", status, body)
	}
	if got := openSealed(t, opening, body); got.User != "sam" || got.Device != "phone" {
		t.Fatalf("sam's sealed code is answered %+v", got)
	}
	devices, err := y.st.Devices(ctx, "")
	if err != nil || len(devices) != 1 || devices[0].ChannelKey != yardkey.ChannelPublic(device) {
		t.Fatalf("devices = %+v, %v, want sam's phone with its key pinned", devices, err)
	}
}

// postPAKE sends one PAKE step to the pair path, with the edge's
// presenter word where presenters are given, and answers the status
// and the step the yard replied with.
func (y *pairYard) postPAKE(t *testing.T, ask pakeAsk, presenters ...string) (int, pakeReply, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]pakeAsk{"pake": ask})
	var raw string
	var status int
	if len(presenters) == 0 {
		status, raw = y.post(t, PairPath, "", string(body))
	} else {
		status, raw = y.do(t, http.MethodPost, PairPath, "", http.Header{PresenterHeader: presenters}, string(body))
	}
	var got struct {
		PAKE pakeReply `json:"pake"`
	}
	_ = json.Unmarshal([]byte(raw), &got)
	return status, got.PAKE, raw
}

// pakeBegin starts a PAKE run for device on code, as a device that
// typed it, and answers the session and the opening that continues
// it.
func (y *pairYard) pakeBegin(t *testing.T, device yardkey.ChannelKey, code string) (string, *yardkey.PairingPAKEOpening, pakeReply) {
	t.Helper()
	first, opening, err := yardkey.StartPairingPAKE(device, code)
	if err != nil {
		t.Fatal(err)
	}
	status, reply, raw := y.postPAKE(t, pakeAsk{Step: "start", Msg: base64.RawURLEncoding.EncodeToString(first)})
	if status != http.StatusOK || reply.Step != "exchange" || reply.Session == "" {
		t.Fatalf("the PAKE start = %d %s, want 200 exchange with a session", status, raw)
	}
	return reply.Session, opening, reply
}

// pakeRun runs a whole PAKE redemption for device on code and
// answers what the device opened: the token, its user and device,
// the yard, and the yard channel key it pins.
func (y *pairYard) pakeRun(t *testing.T, device yardkey.ChannelKey, code string, presenters ...string) pakeAnswer {
	t.Helper()
	session, opening, reply := y.pakeBegin(t, device, code)
	second, err := base64.RawURLEncoding.DecodeString(reply.Msg)
	if err != nil {
		t.Fatal(err)
	}
	confirm, err := opening.Finish(second)
	if err != nil {
		t.Fatal(err)
	}
	status, reply, raw := y.postPAKE(t, pakeAsk{Step: "confirm", Session: session, Confirm: base64.RawURLEncoding.EncodeToString(confirm)}, presenters...)
	if status != http.StatusOK || reply.Step != "confirmed" {
		t.Fatalf("the PAKE confirm = %d %s, want 200 confirmed", status, raw)
	}
	yardConfirm, err := base64.RawURLEncoding.DecodeString(reply.Confirm)
	if err != nil {
		t.Fatal(err)
	}
	if err := opening.Verify(yardConfirm); err != nil {
		t.Fatal(err)
	}
	sealedKey, err := opening.SealKey()
	if err != nil {
		t.Fatal(err)
	}
	status, reply, raw = y.postPAKE(t, pakeAsk{Step: "finish", Session: session, Key: base64.RawURLEncoding.EncodeToString(sealedKey)}, presenters...)
	if status != http.StatusOK || reply.Step != "done" {
		t.Fatalf("the PAKE finish = %d %s, want 200 done", status, raw)
	}
	sealed, err := base64.RawURLEncoding.DecodeString(reply.Answer)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := opening.OpenAnswer(sealed)
	if err != nil {
		t.Fatalf("the device could not open its PAKE answer: %v", err)
	}
	var got pakeAnswer
	if err := json.Unmarshal(plain, &got); err != nil {
		t.Fatalf("the opened PAKE answer = %q (%v)", plain, err)
	}
	return got
}

// A device that typed its code, with no key from any QR, redeems it
// through the PAKE: the run is relayed step by step (here, recorded
// at each pass, as the edge would carry it), neither the code, the
// token nor either static key crosses readable, the device is
// recorded with its key pinned beside its token's hash, the answer
// gives it the yard's channel key to pin, and its token then calls
// as its user.
func testDevicePairedByPAKECode(t *testing.T) {
	y := newPairYard(t)
	ctx := context.Background()
	at := time.Now()
	y.u.now = func() time.Time { return at }
	device, err := yardkey.GenerateChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	code := y.open(t, "sam", "phone")
	var carried []string
	relay := func(ask pakeAsk) (int, pakeReply, string) {
		status, reply, raw := y.postPAKE(t, ask)
		body, _ := json.Marshal(map[string]pakeAsk{"pake": ask})
		carried = append(carried, string(body), raw)
		return status, reply, raw
	}
	first, opening, err := yardkey.StartPairingPAKE(device, strings.ToLower(strings.ReplaceAll(code, "-", "")))
	if err != nil {
		t.Fatal(err)
	}
	status, reply, raw := relay(pakeAsk{Step: "start", Msg: base64.RawURLEncoding.EncodeToString(first)})
	if status != http.StatusOK {
		t.Fatalf("the PAKE start = %d %s", status, raw)
	}
	session := reply.Session
	second, err := base64.RawURLEncoding.DecodeString(reply.Msg)
	if err != nil {
		t.Fatal(err)
	}
	confirm, err := opening.Finish(second)
	if err != nil {
		t.Fatal(err)
	}
	status, reply, raw = relay(pakeAsk{Step: "confirm", Session: session, Confirm: base64.RawURLEncoding.EncodeToString(confirm)})
	if status != http.StatusOK {
		t.Fatalf("the PAKE confirm = %d %s", status, raw)
	}
	yardConfirm, err := base64.RawURLEncoding.DecodeString(reply.Confirm)
	if err != nil {
		t.Fatal(err)
	}
	if err := opening.Verify(yardConfirm); err != nil {
		t.Fatal(err)
	}
	sealedKey, err := opening.SealKey()
	if err != nil {
		t.Fatal(err)
	}
	status, reply, raw = relay(pakeAsk{Step: "finish", Session: session, Key: base64.RawURLEncoding.EncodeToString(sealedKey)})
	if status != http.StatusOK {
		t.Fatalf("the PAKE finish = %d %s", status, raw)
	}
	sealed, err := base64.RawURLEncoding.DecodeString(reply.Answer)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := opening.OpenAnswer(sealed)
	if err != nil {
		t.Fatal(err)
	}
	var got pakeAnswer
	if err := json.Unmarshal(plain, &got); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.Token, store.DeviceTokenPrefix) || got.User != "sam" || got.Device != "phone" || got.Yard != "south" {
		t.Fatalf("the PAKE code is answered %+v", got)
	}
	if got.ChannelKey != yardkey.ChannelPublic(y.channel) {
		t.Fatalf("the PAKE answer pins yard channel key %q, want %q", got.ChannelKey, yardkey.ChannelPublic(y.channel))
	}
	y.checkVoucher(t, got.Voucher, "sam", "phone", at)
	// Nothing the relay carried reads: not the code in either of its
	// forms, not the token, not the voucher, not either key in either of
	// its forms.
	all := strings.Join(carried, "\n")
	for _, secret := range []string{code, strings.ReplaceAll(code, "-", ""), got.Token, got.Voucher,
		yardkey.ChannelPublic(device), base64.RawURLEncoding.EncodeToString(device.Public),
		yardkey.ChannelPublic(y.channel), base64.RawURLEncoding.EncodeToString(y.channel.Public)} {
		if strings.Contains(all, secret) {
			t.Errorf("the relay carried %q readable", secret)
		}
	}
	if status, me := y.get(t, "/user/me", got.Token); status != http.StatusOK || me != `{"user":"sam","said_by":"phone","yard":"south","yardmaster":"yardmaster"}` {
		t.Errorf("/user/me with the PAKE token = %d %s", status, me)
	}
	devices, err := y.st.Devices(ctx, "")
	if err != nil || len(devices) != 1 || devices[0].ChannelKey != yardkey.ChannelPublic(device) {
		t.Fatalf("devices = %+v, %v, want sam's phone with the device key pinned", devices, err)
	}
}

// A relay that rewrites what it carries holds nothing: a byte turned
// in the first message is 400 and no try, in the second the device
// cannot finish its run, in a confirmation one wrong code counted
// against the pairing, and in the sealed device key 400 with the
// pairing still open — and through it all no device is added until
// an unturned run completes.
func testDevicePairPAKERelayRewrites(t *testing.T) {
	y := newPairYard(t)
	ctx := context.Background()
	device, err := yardkey.GenerateChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	turn := func(s string) string {
		b, err := base64.RawURLEncoding.DecodeString(s)
		if err != nil {
			t.Fatal(err)
		}
		b[len(b)/2] ^= 0xff
		return base64.RawURLEncoding.EncodeToString(b)
	}
	devices := func(want int) {
		t.Helper()
		if got, err := y.st.Devices(ctx, ""); err != nil || len(got) != want {
			t.Fatalf("devices = %+v, %v, want %d", got, err, want)
		}
	}

	// A turned first message, and what is no message at all: 400,
	// and the right code still redeems after them, so they were
	// no try.
	code := y.open(t, "jo", "phone")
	first, _, err := yardkey.StartPairingPAKE(device, code)
	if err != nil {
		t.Fatal(err)
	}
	good := base64.RawURLEncoding.EncodeToString(first)
	for what, msg := range map[string]string{"turned": turn(good), "no base64": "!!!", "no point": base64.RawURLEncoding.EncodeToString([]byte("not a point"))} {
		if status, _, raw := y.postPAKE(t, pakeAsk{Step: "start", Msg: msg}); status != http.StatusBadRequest {
			t.Errorf("a PAKE start that is %s = %d %s, want 400", what, status, raw)
		}
	}
	if status, _, raw := y.postPAKE(t, pakeAsk{Step: "dance", Msg: good}); status != http.StatusBadRequest {
		t.Errorf("a PAKE step that is none = %d %s, want 400", status, raw)
	}
	if got := y.pakeRun(t, device, code); got.User != "jo" || got.Device != "phone" {
		t.Fatalf("the right code after the garbage is answered %+v", got)
	}
	devices(1)

	// A turned second message (as the device receives it) fails
	// the device's finish, so no confirmation is ever sent: the
	// pairing is untouched and the code still redeems.
	// Each device that is paired brings a key of its own: one another
	// device pins already is not added.
	if device, err = yardkey.GenerateChannelKey(); err != nil {
		t.Fatal(err)
	}
	code = y.open(t, "jo", "laptop")
	session, opening, reply := y.pakeBegin(t, device, code)
	if _, err := opening.Finish(mustTurn(t, reply.Msg)); err == nil {
		t.Error("the device finished a turned second message")
	}
	_ = session
	if got := y.pakeRun(t, device, code); got.Device != "laptop" {
		t.Fatalf("the right code after a turned second message is answered %+v", got)
	}
	devices(2)

	// A turned confirmation is the one online guess, spent: 403
	// wrong_code, the run is gone, and the code is still good for
	// a clean run — four more turned ones end the pairing.
	code = y.open(t, "jo", "tablet")
	staleID, staleOpening, staleReply := y.pakeBegin(t, device, wrong(code))
	stale, ok := y.u.pakeSessionFor(&pakeAsk{Session: staleID})
	if !ok {
		t.Fatal("the first PAKE run is not live")
	}
	session, opening, reply = y.pakeBegin(t, device, code)
	staleSecond, err := base64.RawURLEncoding.DecodeString(staleReply.Msg)
	if err != nil {
		t.Fatal(err)
	}
	staleConfirm, err := staleOpening.Finish(staleSecond)
	if err != nil {
		t.Fatal(err)
	}
	if _, live, err := y.u.confirmPAKESession(stale, staleConfirm); live || err != nil {
		t.Fatalf("a replaced PAKE confirmation = live %t, %v, want no live step and no try", live, err)
	}
	second, err := base64.RawURLEncoding.DecodeString(reply.Msg)
	if err != nil {
		t.Fatal(err)
	}
	confirm, err := opening.Finish(second)
	if err != nil {
		t.Fatal(err)
	}
	turned := base64.RawURLEncoding.EncodeToString(confirm)
	if status, _, raw := y.postPAKE(t, pakeAsk{Step: "confirm", Session: session, Confirm: turn(turned)}); status != http.StatusForbidden || raw != `{"refused":"wrong_code"}` {
		t.Fatalf("a turned confirmation = %d %s, want 403 wrong_code", status, raw)
	}
	if status, _, raw := y.postPAKE(t, pakeAsk{Step: "confirm", Session: session, Confirm: turned}); status != http.StatusBadRequest {
		t.Errorf("the spent run's confirmation again = %d %s, want 400", status, raw)
	}
	// Four clean wrong-code runs follow the turned one: the fifth
	// refusal in all, after which the right code finds no pairing.
	for try := 2; try <= store.MaxTries; try++ {
		first, opening, err := yardkey.StartPairingPAKE(device, wrong(code))
		if err != nil {
			t.Fatal(err)
		}
		status, reply, raw := y.postPAKE(t, pakeAsk{Step: "start", Msg: base64.RawURLEncoding.EncodeToString(first)})
		if status != http.StatusOK {
			t.Fatalf("wrong-code run %d start = %d %s", try, status, raw)
		}
		second, err := base64.RawURLEncoding.DecodeString(reply.Msg)
		if err != nil {
			t.Fatal(err)
		}
		confirm, err := opening.Finish(second)
		if err != nil {
			t.Fatal(err)
		}
		if status, _, raw := y.postPAKE(t, pakeAsk{Step: "confirm", Session: reply.Session, Confirm: base64.RawURLEncoding.EncodeToString(confirm)}); status != http.StatusForbidden || raw != `{"refused":"wrong_code"}` {
			t.Fatalf("wrong-code run %d confirm = %d %s, want 403 wrong_code", try, status, raw)
		}
	}
	devices(2)
	first, _, err = yardkey.StartPairingPAKE(device, code)
	if err != nil {
		t.Fatal(err)
	}
	if status, _, raw := y.postPAKE(t, pakeAsk{Step: "start", Msg: base64.RawURLEncoding.EncodeToString(first)}); status != http.StatusUnauthorized {
		t.Errorf("the right code after %d wrong PAKE runs = %d %s, want 401, no pairing", store.MaxTries, status, raw)
	}

	// A turned sealed device key, and a finish before its run is
	// confirmed: 400, no device, and the pairing lives for a
	// clean run.
	// Each device that is paired brings a key of its own: one another
	// device pins already is not added.
	if device, err = yardkey.GenerateChannelKey(); err != nil {
		t.Fatal(err)
	}
	code = y.open(t, "jo", "watch")
	session, opening, reply = y.pakeBegin(t, device, code)
	if status, _, raw := y.postPAKE(t, pakeAsk{Step: "finish", Session: session, Key: base64.RawURLEncoding.EncodeToString(make([]byte, 60))}); status != http.StatusBadRequest {
		t.Errorf("a finish before confirmation = %d %s, want 400", status, raw)
	}
	second, err = base64.RawURLEncoding.DecodeString(reply.Msg)
	if err != nil {
		t.Fatal(err)
	}
	confirm, err = opening.Finish(second)
	if err != nil {
		t.Fatal(err)
	}
	status, reply, raw := y.postPAKE(t, pakeAsk{Step: "confirm", Session: session, Confirm: base64.RawURLEncoding.EncodeToString(confirm)})
	if status != http.StatusOK {
		t.Fatalf("the confirm = %d %s", status, raw)
	}
	firstConfirm := reply.Confirm
	status, reply, raw = y.postPAKE(t, pakeAsk{Step: "confirm", Session: session, Confirm: base64.RawURLEncoding.EncodeToString(confirm)})
	if status != http.StatusOK || reply.Confirm != firstConfirm {
		t.Fatalf("the repeated valid confirm = %d %s, want the same confirmation", status, raw)
	}
	yardConfirm, err := base64.RawURLEncoding.DecodeString(reply.Confirm)
	if err != nil {
		t.Fatal(err)
	}
	if err := opening.Verify(yardConfirm); err != nil {
		t.Fatal(err)
	}
	sealedKey, err := opening.SealKey()
	if err != nil {
		t.Fatal(err)
	}
	if status, _, raw := y.postPAKE(t, pakeAsk{Step: "finish", Session: session, Key: turn(base64.RawURLEncoding.EncodeToString(sealedKey))}); status != http.StatusBadRequest {
		t.Errorf("a turned sealed device key = %d %s, want 400", status, raw)
	}
	devices(2)
	// The finish spent that run; a fresh clean run redeems.
	if got := y.pakeRun(t, device, code); got.Device != "watch" {
		t.Fatalf("the right code after a turned key is answered %+v", got)
	}
	devices(3)
}

// mustTurn answers s with a byte turned, for a message a test
// receives and passes on as a rewriting relay would.
func mustTurn(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)/2] ^= 0xff
	return b
}

// A recorded PAKE transcript buys nothing replayed: its finish
// again finds no pairing, its start against a new pairing is a new
// run its recorded confirmation does not confirm, and no second
// device is ever added. A yard told no channel key file serves no
// PAKE start at all.
func testDevicePairPAKEReplay(t *testing.T) {
	y := newPairYard(t)
	ctx := context.Background()
	device, err := yardkey.GenerateChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	code := y.open(t, "sam", "phone")
	first, opening, err := yardkey.StartPairingPAKE(device, code)
	if err != nil {
		t.Fatal(err)
	}
	recordedStart := pakeAsk{Step: "start", Msg: base64.RawURLEncoding.EncodeToString(first)}
	status, reply, raw := y.postPAKE(t, recordedStart)
	if status != http.StatusOK {
		t.Fatalf("the PAKE start = %d %s", status, raw)
	}
	session := reply.Session
	second, err := base64.RawURLEncoding.DecodeString(reply.Msg)
	if err != nil {
		t.Fatal(err)
	}
	confirm, err := opening.Finish(second)
	if err != nil {
		t.Fatal(err)
	}
	recordedConfirm := pakeAsk{Step: "confirm", Session: session, Confirm: base64.RawURLEncoding.EncodeToString(confirm)}
	status, reply, raw = y.postPAKE(t, recordedConfirm)
	if status != http.StatusOK {
		t.Fatalf("the PAKE confirm = %d %s", status, raw)
	}
	yardConfirm, err := base64.RawURLEncoding.DecodeString(reply.Confirm)
	if err != nil {
		t.Fatal(err)
	}
	if err := opening.Verify(yardConfirm); err != nil {
		t.Fatal(err)
	}
	sealedKey, err := opening.SealKey()
	if err != nil {
		t.Fatal(err)
	}
	recordedFinish := pakeAsk{Step: "finish", Session: session, Key: base64.RawURLEncoding.EncodeToString(sealedKey)}
	if status, _, raw := y.postPAKE(t, recordedFinish); status != http.StatusOK {
		t.Fatalf("the PAKE finish = %d %s", status, raw)
	}
	if status, _, raw := y.postPAKE(t, recordedFinish); status != http.StatusUnauthorized || strings.Contains(raw, "yardr-dev-") {
		t.Errorf("the recorded finish again = %d %s, want 401 and no token", status, raw)
	}
	// Against a fresh pairing the recorded start opens a new run,
	// whose session the recorded confirmation does not hold in:
	// it is refused as the wrong code it now proves, and counted.
	y.open(t, "jo", "phone")
	status, reply, raw = y.postPAKE(t, recordedStart)
	if status != http.StatusOK {
		t.Fatalf("the recorded start against a new pairing = %d %s", status, raw)
	}
	replayed := recordedConfirm
	replayed.Session = reply.Session
	if status, _, raw := y.postPAKE(t, replayed); status != http.StatusForbidden || raw != `{"refused":"wrong_code"}` {
		t.Errorf("the recorded confirmation in a new run = %d %s, want 403 wrong_code", status, raw)
	}
	if devices, err := y.st.Devices(ctx, ""); err != nil || len(devices) != 1 {
		t.Fatalf("devices after the replays = %+v, %v, want still the one", devices, err)
	}

	// No channel key file told: no PAKE run is served, and the
	// pairing it named is untouched by the refusal.
	if _, err := y.st.DevicePairClose(ctx, "jo"); err != nil {
		t.Fatal(err)
	}
	// Each device that is paired brings a key of its own: one another
	// device pins already is not added.
	if device, err = yardkey.GenerateChannelKey(); err != nil {
		t.Fatal(err)
	}
	y.u.channelPath = ""
	code = y.open(t, "jo", "laptop")
	first, _, err = yardkey.StartPairingPAKE(device, code)
	if err != nil {
		t.Fatal(err)
	}
	if status, _, raw := y.postPAKE(t, pakeAsk{Step: "start", Msg: base64.RawURLEncoding.EncodeToString(first)}); status != http.StatusBadRequest {
		t.Errorf("a PAKE start at a yard with no channel key = %d %s, want 400", status, raw)
	}
	y.u.channelPath = filepath.Join(t.TempDir(), "channel.key")
	channel, _, err := yardkey.EnsureChannel(y.u.channelPath)
	if err != nil {
		t.Fatal(err)
	}
	y.channel = channel
	if got := y.pakeRun(t, device, code); got.Device != "laptop" || got.ChannelKey != yardkey.ChannelPublic(channel) {
		t.Fatalf("the code at the yard whose channel key returned is answered %+v", got)
	}
}

// A PAKE redemption that names who presents it is the pairing's
// user's alone, as every form's is: another presenter is refused at
// its finish as a wrong code, counted with them, and the code is
// still good for its user, whose device is recorded with its key
// pinned.
func testDevicePairPAKEPresenter(t *testing.T) {
	y := newPairYard(t)
	ctx := context.Background()
	if err := y.st.SetSetting(ctx, store.WebJWTUsersSetting, "jo@example.net=jo, sam@example.net=sam", "jo"); err != nil {
		t.Fatal(err)
	}
	device, err := yardkey.GenerateChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	code := y.open(t, "sam", "phone")
	run := func(presenter string) (int, pakeReply, string) {
		t.Helper()
		first, opening, err := yardkey.StartPairingPAKE(device, code)
		if err != nil {
			t.Fatal(err)
		}
		status, reply, raw := y.postPAKE(t, pakeAsk{Step: "start", Msg: base64.RawURLEncoding.EncodeToString(first)}, presenter)
		if status != http.StatusOK {
			t.Fatalf("the PAKE start presented by %s = %d %s", presenter, status, raw)
		}
		second, err := base64.RawURLEncoding.DecodeString(reply.Msg)
		if err != nil {
			t.Fatal(err)
		}
		confirm, err := opening.Finish(second)
		if err != nil {
			t.Fatal(err)
		}
		session := reply.Session
		status, reply, raw = y.postPAKE(t, pakeAsk{Step: "confirm", Session: session, Confirm: base64.RawURLEncoding.EncodeToString(confirm)}, presenter)
		if status != http.StatusOK {
			t.Fatalf("the PAKE confirm presented by %s = %d %s", presenter, status, raw)
		}
		yardConfirm, err := base64.RawURLEncoding.DecodeString(reply.Confirm)
		if err != nil {
			t.Fatal(err)
		}
		if err := opening.Verify(yardConfirm); err != nil {
			t.Fatal(err)
		}
		sealedKey, err := opening.SealKey()
		if err != nil {
			t.Fatal(err)
		}
		return y.postPAKE(t, pakeAsk{Step: "finish", Session: session, Key: base64.RawURLEncoding.EncodeToString(sealedKey)}, presenter)
	}
	if status, _, raw := run("jo@example.net"); status != http.StatusForbidden || raw != `{"refused":"wrong_code"}` {
		t.Fatalf("sam's PAKE code presented by jo = %d %s, want 403 wrong_code", status, raw)
	}
	if devices, err := y.st.Devices(ctx, ""); err != nil || len(devices) != 0 {
		t.Fatalf("devices = %+v, %v, want none", devices, err)
	}
	if got := y.pakeRun(t, device, code, "sam@example.net"); got.User != "sam" || got.Device != "phone" {
		t.Fatalf("sam's PAKE code presented by sam is answered %+v", got)
	}
	devices, err := y.st.Devices(ctx, "")
	if err != nil || len(devices) != 1 || devices[0].ChannelKey != yardkey.ChannelPublic(device) {
		t.Fatalf("devices = %+v, %v, want sam's phone with its key pinned", devices, err)
	}
}
