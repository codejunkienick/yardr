package rpc

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/yardkey"
)

// The edge enrols a connection for its signed-in account's email over the
// link mark, and again over the hosted router-peer mark, each enrolment
// signed with the edge's key, and is answered once each time with the
// yard-minted token; a second enrolment for another user completes beside
// the first, a second connection of the name for the same user is refused
// as a duplicate device is, and a replay of an enrolment dies as that same
// duplicate. A signed-in session's own call on either mark — with no
// signature, a made-up one, one of another key's, a stale one, or one made
// for another yard's key — enrols nothing, as does a signed call at a yard
// with no pinned edge key; every such refusal reads the same. A device's
// token and a socket caller are answered as for a method the yard does not
// have, and without a credential from elsewhere the path is refused before
// anything is read.
func TestEdgeEnrolsAConnection(t *testing.T) {
	st, u, token := userYard(t)
	ctx := context.Background()
	h := u.Handler()
	if err := st.SetSetting(ctx, store.WebJWTUsersSetting, "jo@example.net=jo, sam@example.net=sam", "jo"); err != nil {
		t.Fatal(err)
	}
	// The edge's key, pinned as yardr edge connect pins it, and the
	// yard's own, told as the server tells it.
	_, edge, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pinned := store.Edge{URL: "https://edge.example.net", Key: store.Public(edge), Handle: "jo", Name: "south"}
	if err := st.SetSetting(ctx, store.EdgeSetting, pinned.String(), "jo"); err != nil {
		t.Fatal(err)
	}
	identityPath := filepath.Join(t.TempDir(), "peer.key")
	yard, _, err := yardkey.Ensure(identityPath)
	if err != nil {
		t.Fatal(err)
	}
	u.SetIdentityKeyFile(identityPath)
	yardPub := store.Public(yard)
	enrol := func(remote, proto, auth, body string, headers http.Header) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, EdgeEnrolPath, strings.NewReader(body))
		r.RemoteAddr = remote
		if proto != "" {
			r.Header.Set("X-Forwarded-Proto", proto)
		}
		if auth != "" {
			r.Header.Set("Authorization", "Bearer "+auth)
		}
		for key, values := range headers {
			for _, v := range values {
				r.Header.Add(key, v)
			}
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	signed := func(from string, key ed25519.PrivateKey, to, body string, at time.Time) http.Header {
		t.Helper()
		ts, sig := yardkey.SignRequest(key, to, http.MethodPost, EdgeEnrolPath, at, []byte(body))
		headers := http.Header{}
		headers.Set(yardkey.HeaderKey, from)
		headers.Set(yardkey.HeaderTime, ts)
		headers.Set(yardkey.HeaderSig, sig)
		return headers
	}
	edgeSigned := func(body string) http.Header {
		t.Helper()
		return signed(pinned.Key, edge, yardPub, body, time.Now())
	}
	var answered struct {
		Result *store.NewDevice `json:"result"`
		Error  *wireError       `json:"error"`
	}
	// Over the link mark, for sam's signed-in account.
	first := `{"email":"sam@example.net","device":"openclaw"}`
	firstHeaders := edgeSigned(first)
	w := enrol(linkRemoteAddr, "", "", first, firstHeaders)
	if w.Code != http.StatusOK {
		t.Fatalf("the link enrolment = %d %s", w.Code, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &answered); err != nil || answered.Result == nil || answered.Error != nil {
		t.Fatalf("the link enrolment answered %s (%v)", w.Body, err)
	}
	samToken := answered.Result.Token
	if answered.Result.User != "sam" || answered.Result.Name != "openclaw" {
		t.Fatalf("the link enrolment answered %+v", answered.Result.Device)
	}
	if secret, ok := strings.CutPrefix(samToken, store.DeviceTokenPrefix); !ok || len(secret) != 43 {
		t.Fatalf("the token %q: want %s and 43 characters", samToken, store.DeviceTokenPrefix)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("the token's answer caches as %q, want no-store", w.Header().Get("Cache-Control"))
	}
	// The token calls as the device.
	me := httptest.NewRequest(http.MethodGet, "/user/me", nil)
	me.Header.Set("Authorization", "Bearer "+samToken)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, me)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"user":"sam"`) || !strings.Contains(w.Body.String(), `"said_by":"openclaw"`) {
		t.Fatalf("/user/me on the enrolled token = %d %s", w.Code, w.Body)
	}
	// Over the hosted router-peer mark, for jo, beside sam's: two
	// enrolments in the same seconds both complete, since no pairing
	// stands between them.
	if err := st.SetSetting(ctx, store.RouterAddrSetting, "192.0.2.15", "jo"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, store.HostedSetting, "true", "jo"); err != nil {
		t.Fatal(err)
	}
	second := `{"email":"jo@example.net","device":"claude-ai"}`
	w = enrol("192.0.2.15:40000", "https", "", second, edgeSigned(second))
	answered = struct {
		Result *store.NewDevice `json:"result"`
		Error  *wireError       `json:"error"`
	}{}
	if err := json.Unmarshal(w.Body.Bytes(), &answered); err != nil || answered.Result == nil || answered.Result.Token == samToken {
		t.Fatalf("the router enrolment answered %d %s (%v)", w.Code, w.Body, err)
	}
	if answered.Result.User != "jo" || answered.Result.Name != "claude-ai" {
		t.Fatalf("the router enrolment answered %+v", answered.Result.Device)
	}
	// Without the https word the router peer enrols nothing: it is no
	// credentialed caller, so it is refused before its body is read.
	if w := enrol("192.0.2.15:40000", "", "", `{"email":"jo@example.net","device":"other"}`, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("the router peer over plain = %d, want 401", w.Code)
	}
	if w := enrol("192.0.2.15:40000", "http", "", `{"email":"jo@example.net","device":"other"}`, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("the router peer that says plain = %d, want 401", w.Code)
	}
	// A second connection of the name for the same user is refused as
	// a duplicate device is today.
	duplicate := func(w *httptest.ResponseRecorder, what string) {
		t.Helper()
		answered = struct {
			Result *store.NewDevice `json:"result"`
			Error  *wireError       `json:"error"`
		}{}
		if err := json.Unmarshal(w.Body.Bytes(), &answered); err != nil || answered.Result != nil || answered.Error == nil {
			t.Fatalf("%s answered %d %s (%v)", what, w.Code, w.Body, err)
		}
		if answered.Error.Code != "conflict" || !strings.Contains(answered.Error.Message, "sam has a device openclaw already") {
			t.Errorf("%s answered %+v", what, answered.Error)
		}
	}
	duplicate(enrol(linkRemoteAddr, "", "", first, edgeSigned(first)), "the duplicate enrolment")
	// A replay of the first enrolment, the same bytes with the same
	// signature inside its window, dies as that same duplicate: no
	// replay store stands behind the door.
	duplicate(enrol(linkRemoteAddr, "", "", first, firstHeaders), "the replayed enrolment")
	// A signed-in session's own call, carried down the link or from the
	// router peer, enrols nothing: with no signature, a made-up one, one
	// signed by another key, a stale one, or one made for another yard's
	// key.
	_, other, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	attack := `{"email":"jo@example.net","device":"smuggled"}`
	fake := edgeSigned(attack)
	fake.Set(yardkey.HeaderSig, "a-made-up-signature")
	unsigned := map[string]http.Header{
		"no signature":        nil,
		"a made-up signature": fake,
		"another key's":       signed(store.Public(other), other, yardPub, attack, time.Now()),
		"a stale signature":   signed(pinned.Key, edge, yardPub, attack, time.Now().Add(-10*time.Minute)),
		"another yard's":      signed(pinned.Key, edge, store.Public(other), attack, time.Now()),
	}
	var refused []string
	for _, route := range []struct{ name, remote, proto string }{
		{"down the link", linkRemoteAddr, ""},
		{"from the router peer", "192.0.2.15:40000", "https"},
	} {
		for name, headers := range unsigned {
			w := enrol(route.remote, route.proto, "", attack, headers)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("%s with %s = %d %s, want 401 and no token", route.name, name, w.Code, w.Body)
				continue
			}
			refused = append(refused, w.Body.String())
		}
	}
	// A signed call at a yard with no pinned edge key is refused: fail
	// closed.
	if err := st.SetSetting(ctx, store.EdgeSetting, "", "jo"); err != nil {
		t.Fatal(err)
	}
	w = enrol(linkRemoteAddr, "", "", attack, edgeSigned(attack))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("no pinned key = %d %s, want 401", w.Code, w.Body)
	} else {
		refused = append(refused, w.Body.String())
	}
	if err := st.SetSetting(ctx, store.EdgeSetting, pinned.String(), "jo"); err != nil {
		t.Fatal(err)
	}
	// Every signature refusal reads the same: none names what was wrong.
	for _, body := range refused {
		if body != refused[0] {
			t.Errorf("refusals differ: %q against %q", body, refused[0])
		}
	}
	// A device's token on the same path, down the link or direct, is
	// answered as for a method the yard does not have: the edge's
	// request carries no credential, so one that carries any falls
	// through to the generic call, which has no such name.
	none, _ := json.Marshal(noSuchMethod(EdgeEnrolMethod))
	if got := enrol(linkRemoteAddr, "", token, `{"email":"sam@example.net","device":"openclaw"}`, nil); strings.TrimSpace(got.Body.String()) != string(none) {
		t.Errorf("the operator's device down the link is answered %s, want %s", got.Body, none)
	}
	// Without a credential from elsewhere the path is refused before
	// anything is read.
	if w := enrol("203.0.113.9:40000", "", "", `{"email":"sam@example.net","device":"openclaw"}`, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("no credential from elsewhere = %d, want 401", w.Code)
	}
	// The socket answers the name as no method at all.
	sock := httptest.NewRequest(http.MethodPost, "/call/"+EdgeEnrolMethod, strings.NewReader(`[]`))
	w = httptest.NewRecorder()
	Handler(st, nil).ServeHTTP(w, sock)
	if strings.TrimSpace(w.Body.String()) != string(none) {
		t.Errorf("the socket answers %s, want %s", w.Body, none)
	}
	// Nothing refused left a device: sam holds the one enrolment, and
	// the attacks on jo's account enrolled nothing beside hers.
	if list, _ := st.Devices(ctx, "sam"); len(list) != 1 || list[0].Name != "openclaw" {
		t.Errorf("sam's devices = %+v", list)
	}
	if list, _ := st.Devices(ctx, "jo"); len(list) != 2 {
		t.Errorf("jo's devices = %+v, want phone and claude-ai alone", list)
	}
}
