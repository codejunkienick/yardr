package web_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/web"
)

// pairingYard serves a yard in JWT mode where ada and bob are signed in,
// connected to an edge: the pairing page's home. It hands back the fixture
// and each user's token.
func pairingYard(t *testing.T) (f *fixture, ada, bob string) {
	t.Helper()
	keys := mustJWTTestKeys(t)
	jwks := newJWKStub(t, jwksDoc(t, rsaJWK("a", &keys.a.PublicKey)))
	f = jwtFixture(t, jwks, "", map[string]string{"ada@example.net": "ada", "bob@example.net": "bob"})
	ctx := context.Background()
	for _, name := range []string{"ada", "bob"} {
		if _, err := f.st.AddUser(ctx, name, "me"); err != nil {
			t.Fatal(err)
		}
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	edge := store.Edge{URL: "https://my.yardr.dev", Key: store.Public(key), Handle: "mckean", Name: "edgeyard"}
	if err := f.st.SetSetting(ctx, store.EdgeSetting, edge.String(), "me"); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetSetting(ctx, store.NameSetting, "depot", "me"); err != nil {
		t.Fatal(err)
	}
	ada = signRSA(t, keys.a, "a", jwtClaims("ada@example.net"))
	bob = signRSA(t, keys.a, "a", jwtClaims("bob@example.net"))
	return f, ada, bob
}

// pairingDo sends one request to the pairing yard's view as the token's
// user, with a form when form is not nil, and does not follow redirects.
// headers are header name and value in pairs, as the fixture's own do.
func pairingDo(t *testing.T, f *fixture, token, method, target string, form url.Values, headers ...string) resp {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, f.ts.URL+target, body)
	if err != nil {
		t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", f.ts.URL)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}
	if token != "" {
		req.Header.Set(web.DefaultJWTHeader, token)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return resp{r.StatusCode, r.Header, string(b)}
}

func pairingGet(t *testing.T, f *fixture, token, target string) resp {
	t.Helper()
	return pairingDo(t, f, token, http.MethodGet, target, nil)
}

// pairingNotice is what the page tells a signed-in visitor, in the
// words it is told in: pairing here no longer gives an app that calls
// through the edge, and the operator's own screen is where its QR is
// opened. pairingNoticeOnPage is it as the page's HTML writes it.
const pairingNotice = "Pairing from this page no longer gives an app that can call through the edge. " +
	"Ask the yard's operator to open a pairing QR on the yard's own screen and scan that."
const pairingNoticeOnPage = "Pairing from this page no longer gives an app that can call through the edge. " +
	"Ask the yard&#39;s operator to open a pairing QR on the yard&#39;s own screen and scan that."

// The pairing page presents no QR and no code as a way to pair an app
// that calls through the edge: it tells the signed-in member that
// pairing from this page no longer gives such an app, and to ask the
// yard's operator to open a pairing QR on the yard's own screen and
// scan that. Its former forms open and end nothing, and its QR
// endpoint serves no QR.
func TestPairingPageSaysWherePairingIsOpened(t *testing.T) {
	t.Parallel()
	f, ada, _ := pairingYard(t)

	r := pairingGet(t, f, ada, "/pairing")
	if r.status != 200 || !strings.Contains(r.body, pairingNoticeOnPage) ||
		strings.Contains(r.body, "pairing-code") || strings.Contains(r.body, `src="/pairing.png"`) ||
		strings.Contains(r.body, "Open pairing") || strings.Contains(r.body, "Pair one of your own devices") ||
		!strings.Contains(r.body, `href="/pairing" aria-current="page"`) {
		t.Fatalf("the pairing page: status %d: %s", r.status, r.body)
	}
	// A reload is the same page.
	if again := pairingGet(t, f, ada, "/pairing"); again.body != r.body {
		t.Fatalf("a reload differs:\n%s\n%s", again.body, r.body)
	}
	// No QR is served here, with the reason in the answer.
	r = pairingGet(t, f, ada, "/pairing.png")
	if r.status != http.StatusNotFound || !strings.Contains(r.body, pairingNotice) {
		t.Fatalf("the QR endpoint: status %d: %s", r.status, r.body)
	}
	// The former open form opens nothing, and says so on the page.
	r = pairingDo(t, f, ada, http.MethodPost, "/pairing", url.Values{"action": {"open"}, "device": {"phone"}})
	if r.status != 200 || !strings.Contains(r.body, pairingNoticeOnPage) || !strings.Contains(r.body, "no pairing was opened or ended") {
		t.Fatalf("the former open form: status %d: %s", r.status, r.body)
	}
	if _, err := f.st.DevicePairCodeSelf(context.Background(), "ada"); err == nil {
		t.Fatal("the pairing page opened a pairing")
	}
	// In JSON the page says the same, and holds no code.
	r = pairingDo(t, f, ada, http.MethodPost, "/pairing", url.Values{"action": {"open"}, "device": {"phone"}}, "Accept", "application/json")
	var got web.Pairing
	if r.status != 200 || r.header.Get("Content-Type") != "application/json" ||
		json.Unmarshal([]byte(r.body), &got) != nil || got.As != "ada" || got.Notice != pairingNotice || got.Error == "" {
		t.Fatalf("the former open form as JSON: status %d, type %q: %s", r.status, r.header.Get("Content-Type"), r.body)
	}
	if _, err := f.st.DevicePairCodeSelf(context.Background(), "ada"); err == nil {
		t.Fatal("the pairing page opened a pairing in JSON")
	}
}

// Every signed-in user is told the same, member and operator alike,
// and no one's POST opens a pairing for anyone: the page has no
// per-user pairing left to show, open or end.
func TestPairingPageSecondUser(t *testing.T) {
	t.Parallel()
	f, ada, bob := pairingYard(t)
	for name, token := range map[string]string{"ada": ada, "bob": bob} {
		r := pairingGet(t, f, token, "/pairing")
		if r.status != 200 || !strings.Contains(r.body, pairingNoticeOnPage) || strings.Contains(r.body, "pairing-code") {
			t.Fatalf("%s's page: status %d: %s", name, r.status, r.body)
		}
		r = pairingDo(t, f, token, http.MethodPost, "/pairing", url.Values{"action": {"open"}, "device": {"phone"}})
		if r.status != 200 || !strings.Contains(r.body, "no pairing was opened or ended") {
			t.Fatalf("%s's opening: status %d: %s", name, r.status, r.body)
		}
		if r := pairingGet(t, f, token, "/pairing.png"); r.status != http.StatusNotFound {
			t.Fatalf("%s's QR: status %d, want 404", name, r.status)
		}
		if _, err := f.st.DevicePairCodeSelf(context.Background(), name); err == nil {
			t.Fatalf("the pairing page opened a pairing for %s", name)
		}
	}
}

// A member-mapped user — the yard user an invite made, with the
// invited address mapped to them in web_jwt_users — is told the same
// as anyone: their pairing is the operator-opened QR now, and this
// page opens none for them either.
func TestPairingPageMemberMappedUser(t *testing.T) {
	t.Parallel()
	keys := mustJWTTestKeys(t)
	jwks := newJWKStub(t, jwksDoc(t, rsaJWK("a", &keys.a.PublicKey)))
	f := jwtFixture(t, jwks, "", map[string]string{"ada@example.net": "ada", "eve@example.net": "eve"})
	ctx := context.Background()
	if _, err := f.st.AddUser(ctx, "ada", "me"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.AddUser(ctx, "eve", "me"); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetSetting(ctx, store.WebJWTUsersSetting, "eve@example.net=eve", "me"); err != nil {
		t.Fatal(err)
	}
	if u, err := f.st.GetUser(ctx, "eve"); err != nil || u.IsOperator() {
		t.Fatalf("the member user = %+v, %v", u, err)
	}
	eve := signRSA(t, keys.a, "a", jwtClaims("eve@example.net"))

	r := pairingGet(t, f, eve, "/pairing")
	if r.status != 200 || !strings.Contains(r.body, pairingNoticeOnPage) || strings.Contains(r.body, "pairing-code") {
		t.Fatalf("the member's page: status %d: %s", r.status, r.body)
	}
	r = pairingDo(t, f, eve, http.MethodPost, "/pairing", url.Values{"action": {"open"}, "device": {"phone"}})
	if r.status != 200 || !strings.Contains(r.body, "no pairing was opened or ended") {
		t.Fatalf("the member's opening: status %d: %s", r.status, r.body)
	}
	if _, err := f.st.DevicePairCodeSelf(ctx, "eve"); err == nil {
		t.Fatal("the pairing page opened a pairing for the member")
	}
}

// The pairing routes are the signed-in view's alone: a view with no JWT
// mode refuses the page, its forms and the QR, in ActAs dress too.
func TestPairingRefusedWithoutJWT(t *testing.T) {
	t.Parallel()
	plain := newFixture(t)
	r := plain.get("/pairing")
	if r.status != http.StatusForbidden || !strings.Contains(r.body, "signed-in view only") || strings.Contains(r.body, `href="/pairing"`) {
		t.Errorf("a plain view's page: status %d: %s", r.status, r.body)
	}
	if r := plain.get("/pairing.png"); r.status != http.StatusForbidden {
		t.Errorf("a plain view's QR: status %d, want 403", r.status)
	}
	// An acting view registers the POST route, and refuses it the same way.
	srv, err := web.NewServer(plain.st, plain.home, nil)
	if err != nil {
		t.Fatal(err)
	}
	token := srv.ActAs("ada")
	acting := servePagesFixture(t, plain.st, srv, plain.home)
	if r := acting.get("/pairing"); r.status != http.StatusForbidden || strings.Contains(r.body, `href="/pairing"`) {
		t.Errorf("an acting view's page: status %d, want 403", r.status)
	}
	actingUser := &fixture{t: t, client: testClient(t), st: plain.st, ts: acting.view, home: plain.home}
	actingUser.signIn(srv, token)
	r = actingUser.postForm("/pairing", url.Values{"action": {"open"}, "device": {"phone"}}, "Origin", acting.view.URL)
	if r.status != http.StatusForbidden || !strings.Contains(r.body, "signed-in view only") {
		t.Errorf("an acting view's open: status %d, want 403: %s", r.status, r.body)
	}
	if _, err := plain.st.DevicePairCodeSelf(context.Background(), "ada"); err == nil {
		t.Error("an acting view opened a pairing without JWT mode")
	}
}
