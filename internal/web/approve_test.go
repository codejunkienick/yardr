package web_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"yardr.dev/yardr/internal/pack"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
	"yardr.dev/yardr/internal/web"
)

// approveYard is a yard in JWT mode with the operator jo and the member
// sam, the depot home and the crew member yardmaster, served for a
// browser. The store and the view share the gateway's 15-minute window.
type approveYard struct {
	t    *testing.T
	st   store.API
	own  *store.Store
	ts   *httptest.Server
	srv  *web.Server
	keys jwtTestKeys
	jwks *jwksStub
}

func newApproveYard(t *testing.T, hosted bool) *approveYard {
	t.Helper()
	ctx := context.Background()
	keys := mustJWTTestKeys(t)
	jwks := newJWKStub(t, jwksDoc(t, rsaJWK("a", &keys.a.PublicKey)))
	st, own := storetest.NewOwned(t)
	for _, name := range []string{"jo", "sam"} {
		if _, err := st.AddUser(ctx, name, "jo"); err != nil {
			t.Fatal(err)
		}
	}
	must(t, st.SetSetting(ctx, store.OperatorSetting, "jo", "jo"))
	must(t, st.SetSetting(ctx, store.HostedSetting, strconv.FormatBool(hosted), "jo"))
	must(t, st.SetSetting(ctx, store.WebJWTPrivilegedSessionMaxAgeSetting, "15m", "jo"))
	must(t, st.PutDepot(ctx, store.Depot{Name: "home", Base: "origin/main"}, "jo"))
	_, err := st.PutCrew(ctx, store.CrewMember{Name: "yardmaster", Runner: "herdr", Dir: "/yard",
		Config: map[string]any{"kind": "claude"}}, "jo")
	must(t, err)
	users := map[string]string{"jo@example.net": "jo", "sam@example.net": "sam"}
	srv, _ := jwtServer(t, st, jwks, "", users, func(c *web.JWTConfig) { c.PrivilegedSessionMaxAge = 15 * time.Minute })
	ts := httptest.NewServer(srv)
	t.Cleanup(func() { shut(ts) })
	return &approveYard{t: t, st: st, own: own, ts: ts, srv: srv, keys: keys, jwks: jwks}
}

// token signs a gateway token for email, carrying what each of set writes
// beside the valid claims.
func (y *approveYard) token(email string, set ...func(jwt.MapClaims)) string {
	y.t.Helper()
	claims := jwtClaims(email)
	for _, f := range set {
		f(claims)
	}
	return signRSA(y.t, y.keys.a, "a", claims)
}

// authAt writes the gateway's authentication time, in whole seconds as a
// gateway writes it.
func authAt(at time.Time) func(jwt.MapClaims) {
	return func(c jwt.MapClaims) { c["auth_time"] = at.Unix() }
}

// post sends form to target as the token's user, following no redirect,
// so a 303 is seen as one. headers are more request headers, each a name
// and a value.
func (y *approveYard) post(target, token string, form url.Values, headers ...string) resp {
	y.t.Helper()
	req, err := http.NewRequest(http.MethodPost, y.ts.URL+target, strings.NewReader(form.Encode()))
	if err != nil {
		y.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(web.DefaultJWTHeader, token)
	req.Header.Set("Origin", y.ts.URL)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, err := client.Do(req)
	if err != nil {
		y.t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return resp{r.StatusCode, r.Header, string(b)}
}

// getNoFollow gets target as the token's user, following no redirect.
// headers are more request headers, each a name and a value.
func (y *approveYard) getNoFollow(target, token string, headers ...string) resp {
	y.t.Helper()
	req, err := http.NewRequest(http.MethodGet, y.ts.URL+target, nil)
	if err != nil {
		y.t.Fatal(err)
	}
	req.Header.Set(web.DefaultJWTHeader, token)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, err := client.Do(req)
	if err != nil {
		y.t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return resp{r.StatusCode, r.Header, string(b)}
}

func (y *approveYard) bead(title string) *store.Bead {
	y.t.Helper()
	b, err := y.st.CreateBead(context.Background(), store.NewBead{Title: title, Stage: "new", Depot: "home"}, "yardmaster")
	must(y.t, err)
	return b
}

func (y *approveYard) askRun(beadID, to string, argv ...string) uint64 {
	y.t.Helper()
	seq, err := y.st.AddAsk(context.Background(), beadID, store.Ask{
		Question: "Run it?", To: to, Context: []string{"the work needs it"},
		Run: &store.AskRun{Argv: argv}}, "yardmaster")
	must(y.t, err)
	return seq
}

func (y *approveYard) revision(beadID string) string {
	y.t.Helper()
	b, err := y.st.GetBead(context.Background(), beadID)
	must(y.t, err)
	return b.Revision
}

// approveForm is the approval the bead page's form posts for ask.
func (y *approveYard) approveForm(beadID string, seq uint64) url.Values {
	return url.Values{
		"rev":    {y.revision(beadID)},
		"ask":    {strconv.FormatUint(seq, 10)},
		"action": {"approve"},
	}
}

func (y *approveYard) askSecret(beadID, to string) uint64 {
	y.t.Helper()
	seq, err := y.st.AddAsk(context.Background(), beadID, store.Ask{
		Question: "The API key for the CRM", To: to, Context: []string{"the work needs it"},
		Secret: &store.AskSecret{Source: "crm"}}, "yardmaster")
	must(y.t, err)
	return seq
}

// giveForm is the giving the bead page's form posts for a secret ask.
func (y *approveYard) giveForm(beadID string, seq uint64, value string) url.Values {
	return url.Values{
		"rev":              {y.revision(beadID)},
		"ask":              {strconv.FormatUint(seq, 10)},
		"secret-CRM_TOKEN": {value},
		"action":           {"give"},
	}
}

// askGoogle asks for the sign-in of the Google account account, of the
// yard's own where as is "" and of the user as where it names one.
func (y *approveYard) askGoogle(beadID, to, account, as string) uint64 {
	y.t.Helper()
	seq, err := y.st.AddAsk(context.Background(), beadID, store.Ask{
		Question: "The Google sign-in", To: to, Context: []string{"sign in on your machine, then paste the export"},
		Secret: &store.AskSecret{Google: account, As: as}}, "yardmaster")
	must(y.t, err)
	return seq
}

// giveGoogleForm is the giving the bead page's form posts for a Google
// sign-in ask: the pasted export under its one name.
func (y *approveYard) giveGoogleForm(beadID string, seq uint64, value string) url.Values {
	return url.Values{
		"rev":                       {y.revision(beadID)},
		"ask":                       {strconv.FormatUint(seq, 10)},
		"secret-GOOGLE_CREDENTIALS": {value},
		"action":                    {"give"},
	}
}

// googleWebExport is the stand-in export an operator pastes from their
// own machine, and its refresh token what no page or note may show.
const (
	googleWebExport  = "{\n  \"client_id\": \"123-fake.apps.googleusercontent.com\",\n  \"client_secret\": \"GOCSPX-fake-secret\",\n  \"refresh_token\": \"1//fake-refresh\",\n  \"type\": \"authorized_user\"\n}"
	googleWebRefresh = "1//fake-refresh"
)

// secretEnv points crm's credential at a file in a temp dir and answers
// it, so a giving's file and its mode can be read back.
func (y *approveYard) secretEnv() string {
	y.t.Helper()
	env := filepath.Join(y.t.TempDir(), "crm.env")
	_, err := y.st.SetSource(context.Background(), "crm", store.SourceEnvFile, env, "jo")
	must(y.t, err)
	return env
}

// askedState is the ask's state as the bead's page reads it for user.
func (y *approveYard) askedState(beadID string, seq uint64, user string) string {
	y.t.Helper()
	v, err := y.st.Bead(context.Background(), beadID, user)
	must(y.t, err)
	for _, a := range v.Asks {
		if a.Ask == seq {
			return a.State
		}
	}
	y.t.Fatalf("bead %s has no ask %d", beadID, seq)
	return ""
}

// witness is the newest witness any event claims, "" where none does.
func (y *approveYard) witness() string {
	y.t.Helper()
	evs, err := y.st.Events(context.Background(), "", 30)
	must(y.t, err)
	for i := len(evs) - 1; i >= 0; i-- {
		if w, _ := evs[i].Data["witness"].(string); w != "" {
			return w
		}
	}
	return ""
}

// approveSurfaces are the places a card stands: its bead's page, Home's
// needs rows and the line's waits (whose right column is the same
// partial). Each posts the card's answers to the bead's handler.
var approveSurfaces = []struct {
	name string
	path func(bead string) string
}{
	{"the bead's page", func(bead string) string { return "/beads/" + bead }},
	{"home", func(string) string { return "/" }},
	{"the line", func(string) string { return "/line" }},
}

const (
	// approveButton is the button a strong card's approval stands behind,
	// in the yard these tests name depot.
	approveButton = `<button type="submit" name="action" value="approve">Approve in depot</button>`
	// noGateLine is what a weighty card says where its window holds no gate
	// for it.
	noGateLine = "This card is weighty and this window has no gate."
	// wordsField is the field a card's words ride in, a decline's reason
	// among them, and declineButton the button that sends the decline.
	wordsField    = `<input type="text" name="words" placeholder="Words with the answer, or why not" aria-label="Words">`
	declineButton = `<button type="submit" name="action" value="decline" title="Say no to all options. The asker finds another way.">Decline</button>`
)

// surfaceBody is the page at path as the token's user reads it.
func (y *approveYard) surfaceBody(path, token string) string {
	y.t.Helper()
	r := y.getNoFollow(path, token)
	if r.status != http.StatusOK {
		y.t.Fatalf("GET %s: %d: %s", path, r.status, r.body)
	}
	return r.body
}

// weighed is the classes the yard stored on the ask.
func (y *approveYard) weighed(beadID string, seq uint64) []string {
	y.t.Helper()
	v, err := y.st.Bead(context.Background(), beadID, "jo")
	must(y.t, err)
	var out []string
	for _, a := range v.Asks {
		if a.Ask == seq {
			for _, w := range a.Weight {
				out = append(out, w.Class)
			}
		}
	}
	return out
}

// approveCRM is the source the weighty-card tests change: one read call
// and the credential the yard holds under CRM_TOKEN. approveCRMList is it
// with one more read call, which a standing grant of read reaches once a
// declaration with it replaces crm's.
const (
	approveCRM     = "name: crm\nbase: https://crm.example.com/api\ncredential: { env: CRM_TOKEN }\ncalls:\n  ping: { kind: read, request: { method: GET, path: /me } }\n"
	approveCRMList = approveCRM + "  list: { kind: read, request: { method: GET, path: /lists } }\n"
)

// putCRM puts the source approveCRM on the yard, with a standing grant of
// read to the yardmaster where grant is set.
func (y *approveYard) putCRM(grant bool) {
	y.t.Helper()
	ctx := context.Background()
	_, err := y.st.PutSource(ctx, "crm.yaml", approveCRM, "jo")
	must(y.t, err)
	if grant {
		_, err = y.st.GrantSource(ctx, "crm", store.GrantRead, store.Grantee{Crew: "yardmaster"}, "", "jo")
		must(y.t, err)
	}
}

// askSetup asks for setupmail's setup for the yardmaster, as the yard's
// first install of it: a credential, an outside and a code.
func (y *approveYard) askSetup(beadID string) uint64 {
	y.t.Helper()
	seq, err := y.st.AddAsk(context.Background(), beadID, store.Ask{Question: "Set up setupmail for the yardmaster?", To: "jo",
		Context: []string{"the yardmaster reads mail to work beads"},
		Setup:   &store.AskSetup{Pack: "setupmail", To: []store.Grantee{{Crew: "yardmaster"}}}}, "yardmaster")
	must(y.t, err)
	return seq
}

// A routine run card approves on the bead page as it always was: the
// button stands beside the card, the approval runs it, and the answer
// claims no witness. Home and the line leave a routine card's approval
// where it is: they offer none.
func TestWebApprovesARoutineRunCard(t *testing.T) {
	t.Parallel()
	y := newApproveYard(t, true)
	ctx := context.Background()
	must(t, func() error { _, err := y.st.AddUser(ctx, "gone", "jo"); return err }())
	must(t, y.st.SetSetting(ctx, store.NameSetting, "depot", "jo"))
	b := y.bead("take back a user")
	seq := y.askRun(b.ID, "jo", "user", "rm", "gone")
	tok := y.token("jo@example.net")

	page := jwtGet(t, y.ts, "/beads/"+b.ID, web.DefaultJWTHeader, tok)
	if page.status != 200 {
		t.Fatalf("GET the bead: %d: %s", page.status, page.body)
	}
	if !strings.Contains(page.body, `value="approve"`) {
		t.Fatalf("the routine card shows no approve control: %s", page.body)
	}
	// The card reads as in the other places: the ask line, the asker, the
	// run sentence naming the yard that runs the command and the user the
	// approval is called as, and Ask about this to the crew asker's
	// conversation.
	for _, want := range []string{
		`<p class="ask-line">` + b.ID + ` · ask ` + strconv.FormatUint(seq, 10) + `</p>`,
		`<p class="ask-asker">asked by yardmaster</p>`,
		`<p>in depot, when you approve, the yard runs this as jo, and nothing else:</p>`,
		`<pre class="text file">yardr user rm gone</pre>`,
		`<a class="ask-about" href="/talk?with=yardmaster">Ask about this</a>`,
	} {
		if !strings.Contains(page.body, want) {
			t.Errorf("the routine card lacks %q:\n%s", want, page.body)
		}
	}
	for _, s := range approveSurfaces[1:] {
		if body := y.surfaceBody(s.path(b.ID), tok); strings.Contains(body, `value="approve"`) {
			t.Errorf("%s offers a routine card's approval:\n%s", s.name, body)
		}
	}
	r := y.post("/beads/"+b.ID, tok, y.approveForm(b.ID, seq))
	if r.status != http.StatusSeeOther || r.header.Get("Location") != "/beads/"+b.ID {
		t.Fatalf("POST the approval: %d %q: %s", r.status, r.header.Get("Location"), r.body)
	}
	if _, err := y.st.GetUser(ctx, "gone"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the approval left the user: %v", err)
	}
	if got := y.askedState(b.ID, seq, "jo"); got != "answered" {
		t.Fatalf("the ask is %s after the approval, want it answered", got)
	}
	if w := y.witness(); w != "" {
		t.Fatalf("the routine approval is witnessed %q, want none", w)
	}
}

// A strong card's approval starts the gateway re-authentication: the view
// answers with the step to the edge, bound to this card, and the gateway's
// return approves it under the fresh sign-in. The return state is one-use:
// replayed, it approves nothing. A post htmx made (the bead page's forms
// post with it) is answered 200 with HX-Redirect to the same step, since
// htmx follows a 303 as an XHR and would swap the sign-in page into the
// card's place; the state it carries is the one followed through the return
// here.
// A pairing approval in the hosted web answers in its own response:
// the QR page with the code, marked never to be stored, the code in no
// URL, and gone on the next visit to the bead. The signed-in user
// approves their own first pairing here under the fresh sign-in.
func webPairingApprovalAnswersInItsResponse(t *testing.T) {
	y := newApproveYard(t, true)
	ctx := context.Background()
	b := y.bead("pair the watch")
	seq := y.askRun(b.ID, "sam", "user", "device", "pair", "sam", "watch", "--url", "https://mac.example.net")
	// The card says this door's sentence before approval.
	page := y.surfaceBody("/beads/"+b.ID, y.token("sam@example.net"))
	if !strings.Contains(page, "whoever has it within its minutes, the carrier of this approval included, could spend it first") {
		t.Fatalf("the bead page lacks the door's sentence: %s", page)
	}
	r := y.post("/beads/"+b.ID, y.token("sam@example.net"), y.approveForm(b.ID, seq))
	if r.status != http.StatusSeeOther {
		t.Fatalf("POST the approval: %d: %s", r.status, r.body)
	}
	loc, err := url.Parse(r.header.Get("Location"))
	if err != nil || loc.Path != "/edge/reauth" {
		t.Fatalf("the approval goes to %q, want the gateway re-authentication", r.header.Get("Location"))
	}
	back := loc.Query().Get("return_to")
	fresh := y.token("sam@example.net", authAt(time.Now().Add(5*time.Second)))
	r = y.getNoFollow(back, fresh)
	if r.status != http.StatusOK {
		t.Fatalf("GET the return: %d: %s", r.status, r.body)
	}
	if cc := r.header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Fatalf("the pairing response is stored: Cache-Control %q", cc)
	}
	marker := "code <code>"
	_, after, ok := strings.Cut(r.body, marker)
	if !ok {
		t.Fatalf("the pairing response has no code field: %s", r.body)
	}
	code, _, ok := strings.Cut(after, "</code>")
	if !ok || code == "" {
		t.Fatalf("the pairing response has no code: %s", r.body)
	}
	if !strings.Contains(r.body, code) || !strings.Contains(r.body, "https://mac.example.net") {
		t.Fatalf("the pairing response lacks the code or address: %s", r.body)
	}
	if strings.Contains(r.header.Get("Location"), code) {
		t.Fatal("the code stands in a redirect target")
	}
	if _, err := y.own.DevicePairCodeSelf(ctx, "sam"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the approval code could be read again by the user: %v", err)
	}
	if _, err := y.own.DevicePairState(ctx); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the approval code could be read again by an operator: %v", err)
	}
	// Gone on the next visit: the bead shows only the stored sentence.
	page = y.surfaceBody("/beads/"+b.ID, y.token("sam@example.net"))
	if strings.Contains(page, code) {
		t.Fatal("the bead page holds the code after the approval")
	}
	if !strings.Contains(page, "shown to the approver alone") {
		t.Fatalf("the bead page lacks the safe sentence: %s", page)
	}
	if _, err := y.own.DevicePairRedeem(ctx, code); err != nil {
		t.Fatalf("the answered code %q does not pair: %v", code, err)
	}
}

func TestWebStrongCardChallengeAndReturn(t *testing.T) {
	t.Parallel()
	t.Run("pairing approval answers in its response", webPairingApprovalAnswersInItsResponse)
	y := newApproveYard(t, true)
	ctx := context.Background()
	b := y.bead("add a user")
	seq := y.askRun(b.ID, "jo", "user", "add", "bob")

	r := y.post("/beads/"+b.ID, y.token("jo@example.net"), y.approveForm(b.ID, seq))
	if r.status != http.StatusSeeOther {
		t.Fatalf("POST the approval: %d: %s", r.status, r.body)
	}
	loc, err := url.Parse(r.header.Get("Location"))
	if err != nil || loc.Path != "/edge/reauth" {
		t.Fatalf("the approval goes to %q, want the gateway re-authentication", r.header.Get("Location"))
	}
	back := loc.Query().Get("return_to")
	if want := "/beads/" + b.ID + "/fresh/" + strconv.FormatUint(seq, 10) + "/"; !strings.HasPrefix(back, want) || back == want {
		t.Fatalf("the return state is %q, want one-use state bound to ask %d", back, seq)
	}
	// The same post from htmx names the same step with a one-use state of
	// its own, and no redirect for the XHR to follow.
	hx := y.post("/beads/"+b.ID, y.token("jo@example.net"), y.approveForm(b.ID, seq), "HX-Request", "true")
	if hx.status != http.StatusOK || hx.header.Get("Location") != "" {
		t.Fatalf("POST the approval with htmx: %d %q: %s", hx.status, hx.header.Get("Location"), hx.body)
	}
	hxLoc, err := url.Parse(hx.header.Get("HX-Redirect"))
	if err != nil || hxLoc.Path != "/edge/reauth" {
		t.Fatalf("the htmx approval goes to %q, want the gateway re-authentication", hx.header.Get("HX-Redirect"))
	}
	hxBack := hxLoc.Query().Get("return_to")
	if want := "/beads/" + b.ID + "/fresh/" + strconv.FormatUint(seq, 10) + "/"; !strings.HasPrefix(hxBack, want) || hxBack == want || hxBack == back {
		t.Fatalf("the htmx return state is %q, want a state of its own bound to ask %d", hxBack, seq)
	}
	// Up to the state each post mints, it is the URL the 303 names.
	if hxStep, step := strings.TrimSuffix(hx.header.Get("HX-Redirect"), path.Base(hxBack)), strings.TrimSuffix(r.header.Get("Location"), path.Base(back)); hxStep != step {
		t.Fatalf("the htmx approval goes to %q, the 303 to %q, want the same step", hxStep, step)
	}
	if _, err := y.st.GetUser(ctx, "bob"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the challenge added the user: %v", err)
	}
	// The gateway authenticates after the challenge: whole seconds, as a
	// gateway writes them, past the skew only a clock may be ahead by. The
	// return followed is the htmx post's, the one a browser on the bead's
	// page carries.
	fresh := y.token("jo@example.net", authAt(time.Now().Add(5*time.Second)))
	r = y.getNoFollow(hxBack, fresh)
	if r.status != http.StatusSeeOther || r.header.Get("Location") != "/beads/"+b.ID {
		t.Fatalf("GET the return: %d %q: %s", r.status, r.header.Get("Location"), r.body)
	}
	if _, err := y.st.GetUser(ctx, "bob"); err != nil {
		t.Fatalf("the return approved nothing: %v", err)
	}
	if got := y.askedState(b.ID, seq, "jo"); got != "answered" {
		t.Fatalf("the ask is %s after the return, want it answered", got)
	}
	if w := y.witness(); w != store.WitnessFreshSignIn {
		t.Fatalf("the approval is witnessed %q, want a fresh sign-in", w)
	}
	r = y.getNoFollow(hxBack, fresh)
	if r.status != 200 || !strings.Contains(r.body, "used already") {
		t.Fatalf("replaying the return: %d: %s", r.status, r.body)
	}

	const botDecl = `
name: bot
base: http://127.0.0.1:9
credential: { env: BOT_TOKEN, header: "Authorization: Bearer {credential}" }
calls:
  setwebhook: { kind: update, params: { url: {type: string, max: 2048}, secret_token: {type: string, max: 256}, project: {type: string, pattern: "^[0-9]+$", max: 20} }, request: { method: POST, path: /setWebhook, body: { url: "{url}", secret_token: "{secret_token}", project: "{project}" } } }
hook:
  verify: { scheme: secret, signature: { header: X-Test-Secret } }
  bodies: unique
  normalize: [bot/normalize]
  keys: [chat]
  register: { call: setwebhook, url: url, secret: secret_token, ask: [project] }
`
	_, err = y.st.PutSource(ctx, "bot.yaml", botDecl, "jo")
	must(t, err)
	env := filepath.Join(t.TempDir(), "bot.env")
	must(t, os.WriteFile(env, []byte("BOT_TOKEN=the-token\n"), 0o600))
	_, err = y.st.SetSource(ctx, "bot", store.SourceEnvFile, env, "jo")
	must(t, err)
	must(t, y.st.SetSetting(ctx, store.HookPublicURLSetting, "https://yard.example/hooks", "jo"))
	hookBead := y.bead("set the bot hook up")
	hookAsk := y.askRun(hookBead.ID, "jo", "hook", "add", "bot", "--source", "bot")
	if !slices.ContainsFunc(y.weighed(hookBead.ID, hookAsk), func(class string) bool {
		return class == store.WeightTrust || class == store.WeightCode
	}) {
		t.Fatalf("the hook card weighs %v, want a strong class", y.weighed(hookBead.ID, hookAsk))
	}
	standing := y.token("jo@example.net")
	for _, s := range approveSurfaces {
		body := y.surfaceBody(s.path(hookBead.ID), standing)
		for _, want := range []string{
			`<input type="text" name="value-project" placeholder="project" aria-label="project">`,
			approveButton,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: hook card lacks %q:\n%s", s.name, want, body)
			}
		}
	}
	r = y.post("/beads/"+hookBead.ID, standing, y.approveForm(hookBead.ID, hookAsk))
	if r.status != http.StatusSeeOther {
		t.Fatalf("POST the hook approval: %d: %s", r.status, r.body)
	}
	loc, err = url.Parse(r.header.Get("Location"))
	if err != nil || loc.Path != "/edge/reauth" {
		t.Fatalf("the hook approval goes to %q, want the gateway re-authentication", r.header.Get("Location"))
	}
	missing := y.token("jo@example.net", authAt(time.Now().Add(5*time.Second)))
	r = y.getNoFollow(loc.Query().Get("return_to"), missing)
	if r.status != http.StatusOK || !strings.Contains(r.body, "asks for project in the clear") {
		t.Fatalf("the hook return without project: %d: %s", r.status, r.body)
	}
	if got := y.askedState(hookBead.ID, hookAsk, "jo"); got != "open" {
		t.Fatalf("the hook ask is %s after the refusal, want it open", got)
	}
	if _, err := y.st.GetHook(ctx, "bot"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the refused approval made the hook: %v", err)
	}
}

// One fresh sign-in witnesses a second strong card inside the window: the
// card approves without a new sign-in, whichever card started the window.
func TestWebSecondStrongCardSkipsSignIn(t *testing.T) {
	t.Parallel()
	y := newApproveYard(t, true)
	ctx := context.Background()
	first := y.bead("add a user")
	seq := y.askRun(first.ID, "jo", "user", "add", "ann")
	r := y.post("/beads/"+first.ID, y.token("jo@example.net"), y.approveForm(first.ID, seq))
	back, err := url.Parse(r.header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	fresh := y.token("jo@example.net", authAt(time.Now().Add(5*time.Second)))
	if r := y.getNoFollow(back.Query().Get("return_to"), fresh); r.status != http.StatusSeeOther {
		t.Fatalf("GET the first return: %d: %s", r.status, r.body)
	}
	second := y.bead("add another user")
	carol := y.askRun(second.ID, "jo", "user", "add", "carol")
	r = y.post("/beads/"+second.ID, fresh, y.approveForm(second.ID, carol))
	if r.status != http.StatusSeeOther || r.header.Get("Location") != "/beads/"+second.ID {
		t.Fatalf("POST the second approval: %d %q: %s", r.status, r.header.Get("Location"), r.body)
	}
	if _, err := y.st.GetUser(ctx, "carol"); err != nil {
		t.Fatalf("the second approval ran nothing: %v", err)
	}
	if w := y.witness(); w != store.WitnessFreshSignIn {
		t.Fatalf("the second approval is witnessed %q, want a fresh sign-in", w)
	}
}

// A secret ask on a hosted bead page draws its concealed fields and
// gives under a fresh sign-in: submitting starts the gateway
// re-authentication bound to the ask, the return gives with the fresh
// witness and writes the credential file, and a second secret ask inside
// the window gives at once. Off the bead's page, and off a hosted yard,
// no field stands.
func TestWebSecretGiveUnderAFreshSignIn(t *testing.T) {
	t.Parallel()
	y := newApproveYard(t, true)
	ctx := context.Background()
	must(t, y.st.SetSetting(ctx, store.NameSetting, "depot", "jo"))
	y.putCRM(false)
	env := y.secretEnv()
	b := y.bead("the crm key")
	seq := y.askSecret(b.ID, "jo")
	tok := y.token("jo@example.net")

	page := y.surfaceBody("/beads/"+b.ID, tok)
	for _, want := range []string{
		`<input type="password" name="secret-CRM_TOKEN" placeholder="CRM_TOKEN" aria-label="CRM_TOKEN" autocomplete="off">`,
		`<button type="submit" name="action" value="give">Give in depot</button>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the hosted bead page lacks %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, noGateLine) {
		t.Errorf("the hosted bead page shows the no-gate line beside its giving:\n%s", page)
	}
	for _, s := range approveSurfaces[1:] {
		if body := y.surfaceBody(s.path(b.ID), tok); strings.Contains(body, `type="password"`) || strings.Contains(body, `value="give"`) {
			t.Errorf("%s offers a secret giving:\n%s", s.name, body)
		}
	}
	off := newApproveYard(t, false)
	must(t, off.st.SetSetting(ctx, store.NameSetting, "depot", "jo"))
	off.putCRM(false)
	ob := off.bead("the crm key")
	off.askSecret(ob.ID, "jo")
	if body := off.surfaceBody("/beads/"+ob.ID, off.token("jo@example.net")); strings.Contains(body, `type="password"`) ||
		strings.Contains(body, `value="give"`) || !strings.Contains(body, noGateLine) {
		t.Errorf("a bead page off a hosted yard offers a secret giving, or lacks the no-gate line:\n%s", body)
	}

	const value = "the-crm-secret"
	r := y.post("/beads/"+b.ID, tok, y.giveForm(b.ID, seq, value))
	if r.status != http.StatusSeeOther {
		t.Fatalf("POST the giving: %d: %s", r.status, r.body)
	}
	loc, err := url.Parse(r.header.Get("Location"))
	if err != nil || loc.Path != "/edge/reauth" {
		t.Fatalf("the giving goes to %q, want the gateway re-authentication", r.header.Get("Location"))
	}
	// The same post from htmx answers with HX-Redirect to the same step,
	// with a state of its own; the return followed is that one's.
	hx := y.post("/beads/"+b.ID, tok, y.giveForm(b.ID, seq, value), "HX-Request", "true")
	if hx.status != http.StatusOK || hx.header.Get("Location") != "" {
		t.Fatalf("POST the giving with htmx: %d %q: %s", hx.status, hx.header.Get("Location"), hx.body)
	}
	hxLoc, err := url.Parse(hx.header.Get("HX-Redirect"))
	if err != nil || hxLoc.Path != "/edge/reauth" {
		t.Fatalf("the htmx giving goes to %q, want the gateway re-authentication", hx.header.Get("HX-Redirect"))
	}
	back := hxLoc.Query().Get("return_to")
	if want := "/beads/" + b.ID + "/fresh/" + strconv.FormatUint(seq, 10) + "/"; !strings.HasPrefix(back, want) || back == want {
		t.Fatalf("the return state is %q, want one-use state bound to ask %d", back, seq)
	}
	if _, err := os.Stat(env); !os.IsNotExist(err) {
		t.Fatalf("the challenge wrote the credential file: %v", err)
	}
	fresh := y.token("jo@example.net", authAt(time.Now().Add(5*time.Second)))
	r = y.getNoFollow(back, fresh)
	if r.status != http.StatusSeeOther || r.header.Get("Location") != "/beads/"+b.ID {
		t.Fatalf("GET the return: %d %q: %s", r.status, r.header.Get("Location"), r.body)
	}
	if got := y.askedState(b.ID, seq, "jo"); got != "answered" {
		t.Fatalf("the ask is %s after the return, want it answered", got)
	}
	if w := y.witness(); w != store.WitnessFreshSignIn {
		t.Fatalf("the giving is witnessed %q, want a fresh sign-in", w)
	}
	data, err := os.ReadFile(env)
	must(t, err)
	if string(data) != "CRM_TOKEN="+value+"\n" {
		t.Fatalf("the credential file holds %q", data)
	}
	if st, err := os.Stat(env); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("the credential file's mode = %v, %v", st.Mode(), err)
	}
	notes, err := y.st.Notes(ctx, b.ID)
	must(t, err)
	for _, n := range notes {
		if strings.Contains(n.Text, value) {
			t.Errorf("note %d holds the value: %q", n.Seq, n.Text)
		}
	}
	if body := y.surfaceBody("/beads/"+b.ID, fresh); strings.Contains(body, value) {
		t.Errorf("the bead page holds the value after the giving:\n%s", body)
	}
	answers := len(notes)
	if r := y.getNoFollow(back, fresh); r.status != 200 || !strings.Contains(r.body, "used already") || strings.Contains(r.body, value) {
		t.Fatalf("replaying the return: %d: %s", r.status, r.body)
	}
	if notes, err := y.st.Notes(ctx, b.ID); err != nil || len(notes) != answers {
		t.Fatalf("the replay left %d notes, want %d (%v)", len(notes), answers, err)
	}

	// A second secret ask inside the fresh window gives at once, with no
	// new sign-in.
	second := y.bead("the crm key again")
	seq2 := y.askSecret(second.ID, "jo")
	const secondValue = "the-second-secret"
	r = y.post("/beads/"+second.ID, fresh, y.giveForm(second.ID, seq2, secondValue))
	if r.status != http.StatusSeeOther || r.header.Get("Location") != "/beads/"+second.ID {
		t.Fatalf("POST the second giving: %d %q: %s", r.status, r.header.Get("Location"), r.body)
	}
	if got := y.askedState(second.ID, seq2, "jo"); got != "answered" {
		t.Fatalf("the second ask is %s after its giving, want it answered", got)
	}
	if data, err := os.ReadFile(env); err != nil || string(data) != "CRM_TOKEN="+secondValue+"\n" {
		t.Fatalf("the credential file holds %q after the second giving (%v)", data, err)
	}
	if w := y.witness(); w != store.WitnessFreshSignIn {
		t.Fatalf("the second giving is witnessed %q, want a fresh sign-in", w)
	}
}

// A secret giving's return fails closed: a stale or timeless sign-in, a
// return as another user or for another ask, and a state that was never
// minted each give nothing, echo no value, and leave the ask open. A
// member's return is no operator's fresh session either.
func TestWebSecretGiveReturnRefusals(t *testing.T) {
	t.Parallel()
	y := newApproveYard(t, true)
	ctx := context.Background()
	must(t, y.st.SetSetting(ctx, store.NameSetting, "depot", "jo"))
	y.putCRM(false)
	env := y.secretEnv()
	b := y.bead("the crm key")
	seq := y.askSecret(b.ID, "jo")
	const value = "the-refused-secret"
	r := y.post("/beads/"+b.ID, y.token("jo@example.net"), y.giveForm(b.ID, seq, value))
	if r.status != http.StatusSeeOther {
		t.Fatalf("POST the giving: %d: %s", r.status, r.body)
	}
	loc, err := url.Parse(r.header.Get("Location"))
	if err != nil || loc.Path != "/edge/reauth" {
		t.Fatalf("the giving goes to %q, want the gateway re-authentication", r.header.Get("Location"))
	}
	back := loc.Query().Get("return_to")
	stale := y.token("jo@example.net", authAt(time.Now().Add(-time.Hour)))
	for _, c := range []struct {
		name  string
		path  string
		token string
		want  string
	}{
		{"a stale sign-in", back, stale, "not fresh"},
		{"no authentication time", back, y.token("jo@example.net"), "not fresh"},
		{"another user", back, y.token("sam@example.net", authAt(time.Now().Add(5*time.Second))), "another card"},
		{"another ask", strings.Replace(back, "/fresh/"+strconv.FormatUint(seq, 10)+"/", "/fresh/99/", 1), y.token("jo@example.net", authAt(time.Now().Add(5*time.Second))), "another card"},
		{"a state never minted", "/beads/" + b.ID + "/fresh/" + strconv.FormatUint(seq, 10) + "/never-minted", y.token("jo@example.net", authAt(time.Now().Add(5*time.Second))), "used already"},
	} {
		if r := y.getNoFollow(c.path, c.token); r.status != 200 || !strings.Contains(r.body, c.want) || strings.Contains(r.body, value) {
			t.Errorf("%s: GET the return: %d, want %q and no value:\n%s", c.name, r.status, c.want, r.body)
		}
	}
	// A member's secret card has no hosted-web giving control, and a
	// crafted post is refused before a challenge holds its value. The
	// member's ask is for their own login's input, which they give from a
	// paired device.
	memberBead := y.bead("a member's login")
	memberSeq, err := y.st.AddAsk(ctx, memberBead.ID, store.Ask{
		Question: "The code the sign-in page showed", To: "sam", Context: []string{"the login waits"},
		Secret: &store.AskSecret{Login: "claude-mem1", As: "sam"}}, "yardmaster")
	must(t, err)
	memberForm := url.Values{
		"rev":          {y.revision(memberBead.ID)},
		"ask":          {strconv.FormatUint(memberSeq, 10)},
		"secret-INPUT": {value},
		"action":       {"give"},
	}
	memberPage := y.surfaceBody("/beads/"+memberBead.ID, y.token("sam@example.net"))
	if strings.Contains(memberPage, `type="password"`) || strings.Contains(memberPage, `value="give"`) {
		t.Fatalf("a member's hosted page offers secret giving:\n%s", memberPage)
	}
	r = y.post("/beads/"+memberBead.ID, y.token("sam@example.net"), memberForm)
	if r.status != http.StatusOK || !strings.Contains(r.body, "only the operator can give a secret") || strings.Contains(r.body, value) {
		t.Fatalf("POST a member's giving: %d, want an operator-only refusal and no value: %s", r.status, r.body)
	}
	if _, err := os.Stat(env); !os.IsNotExist(err) {
		t.Fatalf("a refused giving wrote the credential file: %v", err)
	}
	for bead, s := range map[string]uint64{b.ID: seq, memberBead.ID: memberSeq} {
		user := "jo"
		if bead == memberBead.ID {
			user = "sam"
		}
		if got := y.askedState(bead, s, user); got != "open" {
			t.Errorf("the ask on %s is %s after the refusals, want it open", bead, got)
		}
		notes, err := y.st.Notes(ctx, bead)
		must(t, err)
		for _, n := range notes {
			if strings.Contains(n.Text, value) {
				t.Errorf("note %d on %s holds the value: %q", n.Seq, bead, n.Text)
			}
		}
	}
	if w := y.witness(); w != "" {
		t.Fatalf("a refused giving is witnessed %q, want none", w)
	}
}

// The yard's own Google sign-in gives on its bead's page through the
// same fresh sign-in as any secret: the operator pastes a stand-in
// export, a stale or foreign return and a replay give nothing and show
// no value back, the fresh return writes the account's credentials
// file at 0600, and a malformed paste inside the window refuses naming
// the export and writes nothing new. Serial for its HOME seam: the
// credentials file stands under the home the test names.
func TestWebGoogleYardSignInUnderAFreshSignIn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	y := newApproveYard(t, true)
	ctx := context.Background()
	must(t, y.st.SetSetting(ctx, store.NameSetting, "depot", "jo"))
	b := y.bead("the yard's google sign-in")
	seq := y.askGoogle(b.ID, "jo", "yard@example.com", "")
	tok := y.token("jo@example.net")
	file := filepath.Join(home, ".config", "gws", "credentials.json")

	page := y.surfaceBody("/beads/"+b.ID, tok)
	for _, want := range []string{
		`<input type="password" name="secret-GOOGLE_CREDENTIALS" placeholder="GOOGLE_CREDENTIALS" aria-label="GOOGLE_CREDENTIALS" autocomplete="off">`,
		`<button type="submit" name="action" value="give">Give in depot</button>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the hosted bead page lacks %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, noGateLine) {
		t.Errorf("the hosted bead page shows the no-gate line beside its giving:\n%s", page)
	}

	r := y.post("/beads/"+b.ID, tok, y.giveGoogleForm(b.ID, seq, googleWebExport))
	if r.status != http.StatusSeeOther {
		t.Fatalf("POST the giving: %d: %s", r.status, r.body)
	}
	loc, err := url.Parse(r.header.Get("Location"))
	if err != nil || loc.Path != "/edge/reauth" {
		t.Fatalf("the giving goes to %q, want the gateway re-authentication", r.header.Get("Location"))
	}
	back := loc.Query().Get("return_to")
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("the challenge wrote the credentials file: %v", err)
	}
	stale := y.token("jo@example.net", authAt(time.Now().Add(-time.Hour)))
	for _, c := range []struct {
		name  string
		path  string
		token string
		want  string
	}{
		{"a stale sign-in", back, stale, "not fresh"},
		{"another user", back, y.token("sam@example.net", authAt(time.Now().Add(5*time.Second))), "another card"},
	} {
		if r := y.getNoFollow(c.path, c.token); r.status != 200 || !strings.Contains(r.body, c.want) || strings.Contains(r.body, googleWebRefresh) {
			t.Errorf("%s: GET the return: %d, want %q and no value:\n%s", c.name, r.status, c.want, r.body)
		}
	}
	if got := y.askedState(b.ID, seq, "jo"); got != "open" {
		t.Fatalf("the ask is %s after the refused returns, want it open", got)
	}

	fresh := y.token("jo@example.net", authAt(time.Now().Add(5*time.Second)))
	r = y.getNoFollow(back, fresh)
	if r.status != http.StatusSeeOther || r.header.Get("Location") != "/beads/"+b.ID {
		t.Fatalf("GET the return: %d %q: %s", r.status, r.header.Get("Location"), r.body)
	}
	if got := y.askedState(b.ID, seq, "jo"); got != "answered" {
		t.Fatalf("the ask is %s after the return, want it answered", got)
	}
	if w := y.witness(); w != store.WitnessFreshSignIn {
		t.Fatalf("the giving is witnessed %q, want a fresh sign-in", w)
	}
	data, err := os.ReadFile(file)
	must(t, err)
	if strings.Contains(string(data), "\n") || !strings.Contains(string(data), googleWebRefresh) {
		t.Fatalf("the credentials file holds %q", data)
	}
	if st, err := os.Stat(file); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("the credentials file's mode = %v, %v", st.Mode(), err)
	}
	notes, err := y.st.Notes(ctx, b.ID)
	must(t, err)
	for _, n := range notes {
		if strings.Contains(n.Text, googleWebRefresh) {
			t.Errorf("note %d holds the value: %q", n.Seq, n.Text)
		}
	}
	if body := y.surfaceBody("/beads/"+b.ID, fresh); strings.Contains(body, googleWebRefresh) {
		t.Errorf("the bead page holds the value after the giving:\n%s", body)
	}
	answers := len(notes)
	if r := y.getNoFollow(back, fresh); r.status != 200 || !strings.Contains(r.body, "used already") || strings.Contains(r.body, googleWebRefresh) {
		t.Fatalf("replaying the return: %d: %s", r.status, r.body)
	}
	if notes, err := y.st.Notes(ctx, b.ID); err != nil || len(notes) != answers {
		t.Fatalf("the replay left %d notes, want %d (%v)", len(notes), answers, err)
	}

	// A malformed paste inside the fresh window gives at once and
	// refuses at once: it names the export, shows no value back, and
	// the file the good giving wrote stands.
	second := y.bead("the yard's google sign-in again")
	seq2 := y.askGoogle(second.ID, "jo", "yard@example.com", "")
	const badRefresh = "the-malformed-refresh"
	bad := "{\n  \"client_id\": \"x\",\n  \"client_secret\": \"y\",\n  \"refresh_token\": \"" + badRefresh + "\",\n  \"type\": \"service_account\"\n}"
	r = y.post("/beads/"+second.ID, fresh, y.giveGoogleForm(second.ID, seq2, bad))
	if r.status != http.StatusOK || !strings.Contains(r.body, "gws auth export --unmasked") || strings.Contains(r.body, badRefresh) {
		t.Fatalf("POST a malformed paste: %d, want the export named and no value: %s", r.status, r.body)
	}
	if got := y.askedState(second.ID, seq2, "jo"); got != "open" {
		t.Fatalf("the second ask is %s after a malformed paste, want it open", got)
	}
	if data2, err := os.ReadFile(file); err != nil || string(data2) != string(data) {
		t.Fatalf("a malformed paste changed the credentials file: %q (%v)", data2, err)
	}
}

// A Google sign-in's typed values wait at the gateway no longer than
// any secret's: past the fifteen minutes the challenge and its values
// are gone, and the return gives nothing. Serial for its HOME seam.
func TestWebGoogleSignInWaitsNoLongerThanASecret(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	y := newApproveYard(t, true)
	ctx := context.Background()
	must(t, y.st.SetSetting(ctx, store.NameSetting, "depot", "jo"))
	b := y.bead("the yard's google sign-in")
	seq := y.askGoogle(b.ID, "jo", "yard@example.com", "")
	r := y.post("/beads/"+b.ID, y.token("jo@example.net"), y.giveGoogleForm(b.ID, seq, googleWebExport))
	loc, err := url.Parse(r.header.Get("Location"))
	if r.status != http.StatusSeeOther || err != nil || loc.Path != "/edge/reauth" {
		t.Fatalf("POST the giving: %d %q: %s", r.status, r.header.Get("Location"), r.body)
	}
	y.srv.AgeFreshPending(16 * time.Minute)
	fresh := y.token("jo@example.net", authAt(time.Now().Add(5*time.Second)))
	r = y.getNoFollow(loc.Query().Get("return_to"), fresh)
	if r.status != http.StatusOK || !strings.Contains(r.body, "used already, expired, or never started") || strings.Contains(r.body, googleWebRefresh) {
		t.Fatalf("GET the return past the short bound: %d %q: %s", r.status, r.header.Get("Location"), r.body)
	}
	if got := y.askedState(b.ID, seq, "jo"); got != "open" {
		t.Fatalf("the ask is %s after a return past the short bound, want it open", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "gws", "credentials.json")); !os.IsNotExist(err) {
		t.Fatalf("an expired giving wrote the credentials file: %v", err)
	}
}

// A person's own Google sign-in is given on the web by nobody: the
// member's page and the operator's page alike draw no field for it,
// the card names the terminal command, and a crafted post from either
// is refused before any value is held, with no file written. Serial
// for its HOME seam.
func TestWebGooglePersonSignInIsNotGivenOnTheWeb(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	y := newApproveYard(t, true)
	ctx := context.Background()
	must(t, y.st.SetSetting(ctx, store.NameSetting, "depot", "jo"))
	b := y.bead("sam's google sign-in")
	seq := y.askGoogle(b.ID, "sam", "sam@example.com", "sam")
	// The member's page is the card's own: it draws no field, and it
	// names the terminal giving. The operator's page holds no card
	// asked of sam at all.
	memberPage := y.surfaceBody("/beads/"+b.ID, y.token("sam@example.net"))
	if strings.Contains(memberPage, `type="password"`) || strings.Contains(memberPage, `value="give"`) {
		t.Errorf("the member's page offers a Google giving:\n%s", memberPage)
	}
	if !strings.Contains(memberPage, "yardr bead answer "+b.ID+" "+strconv.FormatUint(seq, 10)+" --secret") {
		t.Errorf("the member's page names no terminal giving:\n%s", memberPage)
	}
	if body := y.surfaceBody("/beads/"+b.ID, y.token("jo@example.net")); strings.Contains(body, `type="password"`) || strings.Contains(body, `value="give"`) {
		t.Errorf("the operator's page offers a Google giving:\n%s", body)
	}
	for _, c := range []struct {
		name  string
		token string
		want  string
	}{
		{"the operator", y.token("jo@example.net"), "asked of another"},
		{"the member", y.token("sam@example.net"), "only sam may give it"},
	} {
		r := y.post("/beads/"+b.ID, c.token, y.giveGoogleForm(b.ID, seq, googleWebExport))
		if r.status != http.StatusOK || !strings.Contains(r.body, c.want) || strings.Contains(r.body, googleWebRefresh) {
			t.Errorf("POST by %s: %d, want %q and no value: %s", c.name, r.status, c.want, r.body)
		}
	}
	if got := y.askedState(b.ID, seq, "sam"); got != "open" {
		t.Fatalf("the ask is %s after the refused posts, want it open", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "gws-accounts", "sam@example.com", "credentials.json")); !os.IsNotExist(err) {
		t.Fatalf("a refused web giving wrote the credentials file: %v", err)
	}
	if w := y.witness(); w != "" {
		t.Fatalf("a refused giving is witnessed %q, want none", w)
	}
}

// mappingCard offers a mapping card for sender in talk on y's yard and
// answers its bead and ask, as the operator's queue holds them. putTalk
// declares the identity-hearing source first.
func (y *approveYard) putTalk() {
	y.t.Helper()
	_, err := y.st.PutSource(context.Background(), "talk.yaml", mappingActSource, "jo")
	must(y.t, err)
}

func (y *approveYard) mappingCard(sender string) (string, uint64) {
	y.t.Helper()
	ctx := context.Background()
	created, err := y.own.ClaimIdentity(ctx, "talk", sender, "C1", "hi")
	if err != nil || !created {
		y.t.Fatalf("ClaimIdentity for %s = %v, %v", sender, created, err)
	}
	for _, user := range []string{"jo", "sam"} {
		q, err := y.st.UserAsks(ctx, user, user)
		must(y.t, err)
		for _, o := range q.Open {
			if o.Identity != nil && o.Identity.Sender == sender {
				return o.Bead, o.Ask
			}
		}
	}
	y.t.Fatalf("no queue holds a card for %s", sender)
	return "", 0
}

// pickOf is the number of the option label on the mapping ask seq.
func (y *approveYard) pickOf(beadID string, seq uint64, label string) int {
	y.t.Helper()
	v, err := y.st.Bead(context.Background(), beadID)
	must(y.t, err)
	for _, a := range v.Asks {
		if a.Ask == seq {
			for i, o := range a.Options {
				if o.Label == label {
					return i + 1
				}
			}
		}
	}
	y.t.Fatalf("ask %d on %s offers no %q", seq, beadID, label)
	return 0
}

// pickForm is the pick the bead page's form posts for a mapping ask,
// with words where the pick takes them (someone new's name).
func (y *approveYard) pickForm(beadID string, seq uint64, option int, words string) url.Values {
	v := url.Values{
		"rev":    {y.revision(beadID)},
		"ask":    {strconv.FormatUint(seq, 10)},
		"option": {strconv.Itoa(option)},
	}
	if words != "" {
		v.Set("words", words)
	}
	return v
}

// A mapping card in a hosted window offers its picks to an operator, on
// its bead's page and on the needs rows and waits alike: picking starts
// the gateway re-authentication bound to the card, the return picks
// under the fresh sign-in and records it, and a second mapping card
// inside the window picks at once. Every numbered pick takes the
// sign-in, one that writes no identity included.
func TestWebMappingPickUnderAFreshSignIn(t *testing.T) {
	t.Parallel()
	y := newApproveYard(t, true)
	ctx := context.Background()
	must(t, y.st.SetSetting(ctx, store.NameSetting, "depot", "jo"))
	y.putTalk()
	bID, seq := y.mappingCard("U777")
	pick := y.pickOf(bID, seq, "sam")
	tok := y.token("jo@example.net")

	for _, s := range approveSurfaces {
		body := y.surfaceBody(s.path(bID), tok)
		if !strings.Contains(body, `name="option"`) {
			t.Errorf("%s offers no mapping pick to the operator:\n%s", s.name, body)
		}
		if strings.Contains(body, noGateLine) {
			t.Errorf("%s shows the no-gate line beside its picks:\n%s", s.name, body)
		}
	}
	// Off a hosted yard, even in JWT mode, no mapping pick stands anywhere
	// and the no-gate line stays.
	off := newApproveYard(t, false)
	must(t, off.st.SetSetting(ctx, store.NameSetting, "depot", "jo"))
	off.putTalk()
	offID, _ := off.mappingCard("U777")
	for _, s := range approveSurfaces {
		body := off.surfaceBody(s.path(offID), off.token("jo@example.net"))
		if strings.Contains(body, `name="option"`) || !strings.Contains(body, noGateLine) {
			t.Errorf("%s off a hosted yard offers a mapping pick, or lacks the no-gate line:\n%s", s.name, body)
		}
	}
	r := y.post("/beads/"+bID, tok, y.pickForm(bID, seq, pick, ""))
	if r.status != http.StatusSeeOther {
		t.Fatalf("POST the pick: %d: %s", r.status, r.body)
	}
	loc, err := url.Parse(r.header.Get("Location"))
	if err != nil || loc.Path != "/edge/reauth" {
		t.Fatalf("the pick goes to %q, want the gateway re-authentication", r.header.Get("Location"))
	}
	back := loc.Query().Get("return_to")
	if want := "/beads/" + bID + "/fresh/" + strconv.FormatUint(seq, 10) + "/"; !strings.HasPrefix(back, want) || back == want {
		t.Fatalf("the return state is %q, want one-use state bound to ask %d", back, seq)
	}
	if u, err := y.st.GetUser(ctx, "sam"); err != nil || u.IsIn("talk", "U777") {
		t.Fatalf("the challenge mapped the sender: %+v, %v", u, err)
	}
	// The same post from htmx answers with HX-Redirect to the same step,
	// with a state of its own; the return followed is that one's.
	hx := y.post("/beads/"+bID, tok, y.pickForm(bID, seq, pick, ""), "HX-Request", "true")
	if hx.status != http.StatusOK || hx.header.Get("Location") != "" {
		t.Fatalf("POST the pick with htmx: %d %q: %s", hx.status, hx.header.Get("Location"), hx.body)
	}
	hxLoc, err := url.Parse(hx.header.Get("HX-Redirect"))
	if err != nil || hxLoc.Path != "/edge/reauth" {
		t.Fatalf("the htmx pick goes to %q, want the gateway re-authentication", hx.header.Get("HX-Redirect"))
	}
	hxBack := hxLoc.Query().Get("return_to")
	if want := "/beads/" + bID + "/fresh/" + strconv.FormatUint(seq, 10) + "/"; !strings.HasPrefix(hxBack, want) || hxBack == want || hxBack == back {
		t.Fatalf("the htmx return state is %q, want a state of its own bound to ask %d", hxBack, seq)
	}
	fresh := y.token("jo@example.net", authAt(time.Now().Add(5*time.Second)))
	r = y.getNoFollow(hxBack, fresh)
	if r.status != http.StatusSeeOther || r.header.Get("Location") != "/beads/"+bID {
		t.Fatalf("GET the return: %d %q: %s", r.status, r.header.Get("Location"), r.body)
	}
	if u, err := y.st.GetUser(ctx, "sam"); err != nil || !u.IsIn("talk", "U777") {
		t.Fatalf("the return mapped nobody: %+v, %v", u, err)
	}
	if got := y.askedState(bID, seq, "jo"); got != "answered" {
		t.Fatalf("the ask is %s after the return, want it answered", got)
	}
	if w := y.witness(); w != store.WitnessFreshSignIn {
		t.Fatalf("the pick is witnessed %q, want a fresh sign-in", w)
	}
	notes, err := y.st.Notes(ctx, bID)
	must(t, err)
	if r := y.getNoFollow(hxBack, fresh); r.status != 200 || !strings.Contains(r.body, "used already") {
		t.Fatalf("replaying the return: %d: %s", r.status, r.body)
	}
	if after, err := y.st.Notes(ctx, bID); err != nil || len(after) != len(notes) {
		t.Fatalf("the replay left %d notes, want %d (%v)", len(after), len(notes), err)
	}

	// A second mapping card inside the fresh window picks at once, with
	// no new sign-in: a let-in pick, which writes no identity, takes the
	// same one rule.
	b2, seq2 := y.mappingCard("U888")
	r = y.post("/beads/"+b2, fresh, y.pickForm(b2, seq2, y.pickOf(b2, seq2, store.MappingLetIn), ""))
	if r.status != http.StatusSeeOther || r.header.Get("Location") != "/beads/"+b2 {
		t.Fatalf("POST the second pick: %d %q: %s", r.status, r.header.Get("Location"), r.body)
	}
	if got := y.askedState(b2, seq2, "jo"); got != "answered" {
		t.Fatalf("the second ask is %s after its pick, want it answered", got)
	}
	if w := y.witness(); w != store.WitnessFreshSignIn {
		t.Fatalf("the second pick is witnessed %q, want a fresh sign-in", w)
	}

	// Someone new's name rides the challenge and the return picks them:
	// the name stands in no redirect, and the new user is mapped.
	b3, seq3 := y.mappingCard("U999")
	r = y.post("/beads/"+b3, tok, y.pickForm(b3, seq3, y.pickOf(b3, seq3, store.MappingSomeoneNew), "hal"))
	if r.status != http.StatusSeeOther {
		t.Fatalf("POST someone new: %d: %s", r.status, r.body)
	}
	loc, err = url.Parse(r.header.Get("Location"))
	if err != nil || strings.Contains(r.header.Get("Location"), "hal") {
		t.Fatalf("someone new goes to %q, want the re-authentication without the name", r.header.Get("Location"))
	}
	if _, err := y.st.GetUser(ctx, "hal"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the challenge added the user: %v", err)
	}
	r = y.getNoFollow(loc.Query().Get("return_to"), y.token("jo@example.net", authAt(time.Now().Add(5*time.Second))))
	if r.status != http.StatusSeeOther || r.header.Get("Location") != "/beads/"+b3 {
		t.Fatalf("GET someone new's return: %d %q: %s", r.status, r.header.Get("Location"), r.body)
	}
	if u, err := y.st.GetUser(ctx, "hal"); err != nil || !u.IsIn("talk", "U999") {
		t.Fatalf("someone new was not mapped: %+v, %v", u, err)
	}

	// Later and Decline on a hosted mapping card take no fresh sign-in.
	b4, seq4 := y.mappingCard("U1000")
	r = y.post("/beads/"+b4, tok, url.Values{
		"rev": {y.revision(b4)}, "ask": {strconv.FormatUint(seq4, 10)}, "action": {"later"},
	})
	if r.status != http.StatusSeeOther || r.header.Get("Location") != "/beads/"+b4 {
		t.Fatalf("POST later on a hosted mapping card: %d %q: %s", r.status, r.header.Get("Location"), r.body)
	}
	if got := y.askedState(b4, seq4, "jo"); got != "put aside" {
		t.Fatalf("the ask is %s after later, want put aside", got)
	}
	b5, seq5 := y.mappingCard("U1001")
	r = y.post("/beads/"+b5, tok, url.Values{
		"rev": {y.revision(b5)}, "ask": {strconv.FormatUint(seq5, 10)}, "action": {"decline"}, "words": {"not now"},
	})
	if r.status != http.StatusSeeOther || r.header.Get("Location") != "/beads/"+b5 {
		t.Fatalf("POST decline on a hosted mapping card: %d %q: %s", r.status, r.header.Get("Location"), r.body)
	}
	if got := y.askedState(b5, seq5, "jo"); got != "declined" {
		t.Fatalf("the ask is %s after decline, want declined", got)
	}
}

// A mapping pick's return fails closed: a stale or timeless sign-in, a
// return as another user or for another ask, a state that was never
// minted or has expired, and a bead that moved while its operator
// signed in each pick nothing and leave the card open. A member's
// hosted page offers no mapping pick, and a crafted click is refused
// before any round trip.
func TestWebMappingPickReturnRefusals(t *testing.T) {
	t.Parallel()
	y := newApproveYard(t, true)
	ctx := context.Background()
	must(t, y.st.SetSetting(ctx, store.NameSetting, "depot", "jo"))
	y.putTalk()
	bID, seq := y.mappingCard("U777")
	pick := y.pickOf(bID, seq, "sam")
	r := y.post("/beads/"+bID, y.token("jo@example.net"), y.pickForm(bID, seq, pick, ""))
	if r.status != http.StatusSeeOther {
		t.Fatalf("POST the pick: %d: %s", r.status, r.body)
	}
	loc, err := url.Parse(r.header.Get("Location"))
	if err != nil || loc.Path != "/edge/reauth" {
		t.Fatalf("the pick goes to %q, want the gateway re-authentication", r.header.Get("Location"))
	}
	back := loc.Query().Get("return_to")
	stale := y.token("jo@example.net", authAt(time.Now().Add(-time.Hour)))
	for _, c := range []struct {
		name  string
		path  string
		token string
		want  string
	}{
		{"a stale sign-in", back, stale, "not fresh"},
		{"no authentication time", back, y.token("jo@example.net"), "not fresh"},
		{"another user", back, y.token("sam@example.net", authAt(time.Now().Add(5*time.Second))), "another card"},
		{"another ask", strings.Replace(back, "/fresh/"+strconv.FormatUint(seq, 10)+"/", "/fresh/99/", 1), y.token("jo@example.net", authAt(time.Now().Add(5*time.Second))), "another card"},
		{"a state never minted", "/beads/" + bID + "/fresh/" + strconv.FormatUint(seq, 10) + "/never-minted", y.token("jo@example.net", authAt(time.Now().Add(5*time.Second))), "used already"},
	} {
		if r := y.getNoFollow(c.path, c.token); r.status != 200 || !strings.Contains(r.body, c.want) {
			t.Errorf("%s: GET the return: %d, want %q:\n%s", c.name, r.status, c.want, r.body)
		}
	}
	// A challenge past its hour is spent: the return is as one never
	// minted.
	r = y.post("/beads/"+bID, y.token("jo@example.net"), y.pickForm(bID, seq, pick, ""))
	loc, err = url.Parse(r.header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	y.srv.AgeFreshPending(2 * time.Hour)
	if r := y.getNoFollow(loc.Query().Get("return_to"), y.token("jo@example.net", authAt(time.Now().Add(5*time.Second)))); r.status != 200 ||
		!strings.Contains(r.body, "used already") {
		t.Errorf("an expired challenge: GET the return: %d:\n%s", r.status, r.body)
	}
	// A bead that moved while its operator signed in is read again
	// instead of picked, at the revision its form was drawn from.
	r = y.post("/beads/"+bID, y.token("jo@example.net"), y.pickForm(bID, seq, pick, ""))
	loc, err = url.Parse(r.header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if err := y.st.AddNote(ctx, bID, "while you were signing in", "yardmaster"); err != nil {
		t.Fatal(err)
	}
	if r := y.getNoFollow(loc.Query().Get("return_to"), y.token("jo@example.net", authAt(time.Now().Add(5*time.Second)))); r.status != 200 ||
		!strings.Contains(r.body, "read it again") {
		t.Errorf("a moved bead: GET the return: %d:\n%s", r.status, r.body)
	}
	// A new delivery held on the card while its operator signed in changes
	// the held count without bumping the bead revision; AnswerAskExpected
	// catches it and leaves the card open.
	r = y.post("/beads/"+bID, y.token("jo@example.net"), y.pickForm(bID, seq, pick, ""))
	loc, err = url.Parse(r.header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := y.own.HoldIdentity(ctx, "talk", "talk", "U777", "C1", map[string]any{"text": "arrived during reauth"}, []byte("arrived during reauth"), nil); err != nil {
		t.Fatal(err)
	}
	if r := y.getNoFollow(loc.Query().Get("return_to"), y.token("jo@example.net", authAt(time.Now().Add(5*time.Second)))); r.status != 200 ||
		!strings.Contains(r.body, "held or released since") {
		t.Errorf("a delivery held during reauth: GET the return: %d:\n%s", r.status, r.body)
	}
	if u, err := y.st.GetUser(ctx, "sam"); err != nil || u.IsIn("talk", "U777") {
		t.Fatalf("a refused pick mapped the sender: %+v, %v", u, err)
	}
	if got := y.askedState(bID, seq, "jo"); got != "open" {
		t.Fatalf("the ask is %s after the refusals, want it open", got)
	}
	if w := y.witness(); w != "" {
		t.Fatalf("a refused pick is witnessed %q, want none", w)
	}

	// A member's mapping card (asked of them while they were the
	// operator) offers no pick in a hosted window, and a crafted click
	// is refused as an operator's card is, before any round trip.
	must(t, y.st.SetSetting(ctx, store.OperatorSetting, "sam", "jo"))
	mID, mSeq := y.mappingCard("U111")
	must(t, y.st.SetSetting(ctx, store.OperatorSetting, "jo", "jo"))
	// Naming sam the operator gave them its role with the setting; a
	// member is what the page reads, so give it back.
	if _, err := y.st.SetUser(ctx, "sam", map[string]string{store.UserRole: store.RoleMember}, "jo"); err != nil {
		t.Fatal(err)
	}
	sam := y.token("sam@example.net")
	if body := y.surfaceBody("/beads/"+mID, sam); strings.Contains(body, `name="option"`) || !strings.Contains(body, noGateLine) {
		t.Errorf("a member's hosted page offers a mapping pick, or lacks its line:\n%s", body)
	}
	r = y.post("/beads/"+mID, sam, y.pickForm(mID, mSeq, y.pickOf(mID, mSeq, "jo"), ""))
	if r.status != http.StatusOK || !strings.Contains(r.body, "sign-in is what this page takes") || !strings.Contains(r.body, "footplate") {
		t.Fatalf("POST a member's pick: %d, want an operator-only refusal:\n%s", r.status, r.body)
	}
	if got := y.askedState(mID, mSeq, "sam"); got != "open" {
		t.Fatalf("the member's ask is %s after the refusal, want it open", got)
	}
}

// A standing login never approves a strong card directly: a stale session
// is challenged, and a return under a stale sign-in, with no
// authentication time, or as another user refuses and leaves the card open.
func TestWebStrongCardReturnRefusals(t *testing.T) {
	t.Parallel()
	y := newApproveYard(t, true)
	ctx := context.Background()
	b := y.bead("add a user")
	seq := y.askRun(b.ID, "jo", "user", "add", "bob")

	stale := y.token("jo@example.net", authAt(time.Now().Add(-time.Hour)))
	r := y.post("/beads/"+b.ID, stale, y.approveForm(b.ID, seq))
	if r.status != http.StatusSeeOther {
		t.Fatalf("POST under a stale sign-in: %d: %s", r.status, r.body)
	}
	loc, err := url.Parse(r.header.Get("Location"))
	if err != nil || loc.Path != "/edge/reauth" {
		t.Fatalf("a stale session goes to %q, want a new challenge", r.header.Get("Location"))
	}
	back := loc.Query().Get("return_to")
	for _, c := range []struct {
		name  string
		token string
		want  string
	}{
		{"a stale sign-in", stale, "not fresh"},
		{"no authentication time", y.token("jo@example.net"), "not fresh"},
		{"another user", y.token("sam@example.net", authAt(time.Now().Add(5*time.Second))), "another card"},
	} {
		if r := y.getNoFollow(back, c.token); r.status != 200 || !strings.Contains(r.body, c.want) {
			t.Errorf("%s: GET the return: %d: %s", c.name, r.status, r.body)
		}
	}
	if _, err := y.st.GetUser(ctx, "bob"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a refused return added the user: %v", err)
	}
	if got := y.askedState(b.ID, seq, "jo"); got != "open" {
		t.Fatalf("the ask is %s after the refusals, want it open", got)
	}
	if w := y.witness(); w != "" {
		t.Fatalf("a refused return is witnessed %q, want none", w)
	}
}

// Anywhere but the hosted web a strong card stays terminal-only: off a
// hosted yard, and on a view with no gateway at all.
func TestWebStrongCardStaysTerminalOffHosted(t *testing.T) {
	t.Parallel()
	y := newApproveYard(t, false)
	b := y.bead("add a user")
	seq := y.askRun(b.ID, "jo", "user", "add", "bob")
	fresh := y.token("jo@example.net", authAt(time.Now()))
	// The window holds no gate, so the card reads as any weighty card with
	// none, wherever it stands: its line and its terminal command, and no
	// control to approve it by.
	for _, s := range approveSurfaces {
		body := y.surfaceBody(s.path(b.ID), fresh)
		if strings.Contains(body, `value="approve"`) || !strings.Contains(body, noGateLine) ||
			!strings.Contains(body, "yardr bead answer "+b.ID+" "+strconv.FormatUint(seq, 10)+" --approve") {
			t.Errorf("%s offers a strong card's approval off a hosted yard, or lacks its terminal command:\n%s", s.name, body)
		}
	}
	r := y.post("/beads/"+b.ID, fresh, y.approveForm(b.ID, seq))
	if r.status != 200 || !strings.Contains(r.body, "not approved in this window") ||
		!strings.Contains(r.body, "at a terminal on the yard&#39;s machine") {
		t.Fatalf("POST off a hosted yard: %d: %s", r.status, r.body)
	}
	// A setup's command at the terminal carries the host typed on the form,
	// where it is one word of what a host is made of, and drops what is not
	// rather than print it into a command.
	servesSetupmailOf(y.own, strings.Replace(approveSetupDecl, "base: https://setupmail.example.com/v1\n",
		"base: https://setupmail.example.com/v1\nbase_template: https://{host}/v1\n", 1))
	setupBead := y.bead("set up mail on a server of our own")
	setupSeq := y.askSetup(setupBead.ID)
	for host, want := range map[string]string{
		"192.168.1.20:8443": "&lt;n&gt; --secret --host 192.168.1.20:8443",
		"crm.example.net":   "&lt;n&gt; --secret --host crm.example.net",
		"x; echo hi":        "&lt;n&gt; --secret",
		"":                  "&lt;n&gt; --secret",
	} {
		form := y.approveForm(setupBead.ID, setupSeq)
		form.Set("option", "1")
		form.Set("secret-SETUP_ACCOUNT", "ops@example.com")
		form.Set("host", host)
		r := y.post("/beads/"+setupBead.ID, fresh, form)
		said := "yardr bead answer " + setupBead.ID + " " + strconv.FormatUint(setupSeq, 10) + " " + want
		if r.status != 200 || !strings.Contains(r.body, said) {
			t.Errorf("POST a setup with the host %q off a hosted yard: %d, want the command %q in:\n%s", host, r.status, said, r.body)
		}
		if !strings.Contains(want, "--host") && strings.Contains(r.body, said+" --host") {
			t.Errorf("the command carries the host %q, which is not one word of what a host is made of:\n%s", host, r.body)
		}
	}

	f, _ := talkFixture(t, "jo")
	ctx := context.Background()
	must(t, f.st.SetSetting(ctx, store.OperatorSetting, "jo", "jo"))
	must(t, f.st.PutDepot(ctx, store.Depot{Name: "home", Base: "origin/main"}, "jo"))
	_, err := f.st.PutCrew(ctx, store.CrewMember{Name: "yardmaster", Runner: "herdr", Dir: "/yard",
		Config: map[string]any{"kind": "claude"}}, "jo")
	must(t, err)
	bead, err := f.st.CreateBead(ctx, store.NewBead{Title: "add a user", Stage: "new", Depot: "home"}, "yardmaster")
	must(t, err)
	n, err := f.st.AddAsk(ctx, bead.ID, store.Ask{Question: "Run it?", To: "jo",
		Context: []string{"the work needs it"},
		Run:     &store.AskRun{Argv: []string{"user", "add", "hal"}}}, "yardmaster")
	must(t, err)
	rev, err := f.st.GetBead(ctx, bead.ID)
	must(t, err)
	for _, s := range approveSurfaces {
		if body := f.get(s.path(bead.ID)).body; strings.Contains(body, `value="approve"`) || !strings.Contains(body, noGateLine) {
			t.Errorf("%s with no gateway offers a strong card's approval, or lacks the line:\n%s", s.name, body)
		}
	}
	r = f.postForm("/beads/"+bead.ID, url.Values{
		"rev":    {rev.Revision},
		"ask":    {strconv.FormatUint(n, 10)},
		"action": {"approve"},
	}, "Origin", f.ts.URL)
	if r.status != 200 || !strings.Contains(r.body, "not approved in this window") {
		t.Fatalf("POST with no gateway: %d: %s", r.status, r.body)
	}
}

// A card's approval stands wherever the card does, class by class. A
// weighty card (any class in its weight, alone or beside other classes —
// trust, code, or a middle class: outside, as-user, reads or credential
// on a run or setup card) reads "Approve in <yard>" on its bead's page, on
// Home and on the line, for an operator, and approving it from any of them
// starts the gateway re-authentication, even for a session that signed in
// a moment ago; the return approves it under the fresh sign-in and the
// answer is witnessed a fresh sign-in. A credential card alone is a secret
// ask: it approves nowhere, but gives on its bead's page under the same
// re-authentication (the give row).
func TestWebCardApprovalPerClassOfWeight(t *testing.T) {
	t.Parallel()
	const freshDecl = "name: fresh\nbase: https://fresh.example.com/v1\ncredential: { env: FRESH_TOKEN, header: \"Authorization: Bearer {credential}\" }\nthread:\n  reply: [fresh/thread, reply]\ncalls:\n  ping: { kind: read, request: { method: GET, path: /me } }\n"
	const freshThread = "#!/bin/sh\n# thread\n"
	threadSum := sha256.Sum256([]byte(freshThread))
	// askAs puts ask on the bead as the yardmaster.
	askAs := func(y *approveYard, bead string, ask store.Ask) uint64 {
		y.t.Helper()
		ask.To, ask.Context = "jo", []string{"the work needs it"}
		seq, err := y.st.AddAsk(context.Background(), bead, ask, "yardmaster")
		must(y.t, err)
		return seq
	}
	for _, c := range []struct {
		name    string
		classes []string
		weighty bool
		// give is a secret ask's row: no approval anywhere, but a giving
		// on its bead's page alone, through the same re-authentication.
		give bool
		// ask puts the card on the bead and returns its number, with the
		// fields its approval posts beside the usual three.
		ask func(y *approveYard, bead string) (uint64, url.Values)
		// complete follows the sign-in's return and approves the card.
		complete bool
		// intact checks that nothing ran, before the return.
		intact func(t *testing.T, y *approveYard)
	}{
		{name: "trust", classes: []string{store.WeightTrust}, weighty: true, complete: true,
			ask: func(y *approveYard, bead string) (uint64, url.Values) {
				return y.askRun(bead, "jo", "user", "add", "bob"), nil
			},
			intact: func(t *testing.T, y *approveYard) {
				if _, err := y.st.GetUser(context.Background(), "bob"); !errors.Is(err, store.ErrNotFound) {
					t.Errorf("the challenge added the user: %v", err)
				}
			}},
		{name: "code", classes: []string{store.WeightCode}, weighty: true,
			ask: func(y *approveYard, bead string) (uint64, url.Values) {
				return askAs(y, bead, store.Ask{Question: "Add fresh?", Run: &store.AskRun{Argv: []string{"source add fresh.yaml"},
					Carried: []store.AskSource{{Name: "fresh", Text: freshDecl,
						Files: []store.AskFile{{Program: "fresh/thread", Text: freshThread, SHA256: hex.EncodeToString(threadSum[:])}}}}}}), nil
			}},
		{name: "code beside reads", classes: []string{store.WeightCode, store.WeightReads}, weighty: true,
			ask: func(y *approveYard, bead string) (uint64, url.Values) {
				y.putCRM(true)
				return askAs(y, bead, store.Ask{Question: "Replace crm?", Run: &store.AskRun{Argv: []string{"source add crm.yaml"},
					Carried: []store.AskSource{{Name: "crm", Text: approveCRMList}}}}), nil
			}},
		{name: "credential, outside and code", classes: []string{store.WeightCredential, store.WeightOutside, store.WeightCode}, weighty: true, complete: true,
			ask: func(y *approveYard, bead string) (uint64, url.Values) {
				servesSetupmail(y.own)
				return y.askSetup(bead), url.Values{"option": {"2"}, "secret-SETUP_ACCOUNT": {"ops@example.com"}}
			}},
		{name: "credential", classes: []string{store.WeightCredential}, give: true, complete: true,
			ask: func(y *approveYard, bead string) (uint64, url.Values) {
				y.putCRM(false)
				return askAs(y, bead, store.Ask{Question: "The API key for the CRM", Secret: &store.AskSecret{Source: "crm"}}),
					url.Values{"action": {"give"}, "secret-CRM_TOKEN": {"s3cret"}}
			}},
		{name: "credential on a setup", classes: []string{store.WeightCredential}, weighty: true, complete: true,
			ask: func(y *approveYard, bead string) (uint64, url.Values) {
				servesLightSetupmail(y.own)
				return y.askSetup(bead), url.Values{"option": {"1"}, "secret-SETUP_TOKEN": {"setup-token"}}
			}},
		{name: "outside", classes: []string{store.WeightOutside}, weighty: true, complete: true,
			ask: func(y *approveYard, bead string) (uint64, url.Values) {
				y.putCRM(false)
				return y.askRun(bead, "jo", "source", "set", "crm", "updates_per_hour=6000"), nil
			},
			intact: func(t *testing.T, y *approveYard) {
				src, err := y.st.GetSource(context.Background(), "crm")
				must(t, err)
				if src.UpdatesPerHour != 0 {
					t.Errorf("the refused approval raised the update limit to %d", src.UpdatesPerHour)
				}
			}},
		{name: "as-user", classes: []string{store.WeightAsUser}, weighty: true, complete: true,
			ask: func(y *approveYard, bead string) (uint64, url.Values) {
				y.putCRM(false)
				return y.askRun(bead, "jo", "user", "identity", "sam", "--source", "crm", "--id", "7"), nil
			}},
		{name: "reads", classes: []string{store.WeightReads}, weighty: true, complete: true,
			ask: func(y *approveYard, bead string) (uint64, url.Values) {
				y.putCRM(true)
				y.own.SetPackReader(func(ctx context.Context, name string, depot string, mappings store.Mappings, yard store.API) (*store.PackReading, error) {
					if name != "crm-pack" {
						return nil, errors.New("no pack " + name)
					}
					return &store.PackReading{Name: name, Kind: pack.KindSource, Label: "embedded in yardr test1", Build: "test1",
						Sources: []store.AskSource{{Name: "crm", Text: approveCRMList}}}, nil
				})
				return askAs(y, bead, store.Ask{Question: "Apply crm-pack?", Run: &store.AskRun{Argv: []string{"pack apply crm-pack"}}}), nil
			}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			y := newApproveYard(t, true)
			b := y.bead(c.name)
			seq, extra := c.ask(y, b.ID)
			if got := y.weighed(b.ID, seq); !slices.Equal(got, c.classes) {
				t.Fatalf("the card weighs %v, want %v", got, c.classes)
			}
			// A session that signed in a moment ago: a standing login, which
			// never approves a weighty card.
			tok := y.token("jo@example.net", authAt(time.Now()))
			rev := y.revision(b.ID)
			for _, s := range approveSurfaces {
				body := y.surfaceBody(s.path(b.ID), tok)
				// Wherever the card stands it answers to the bead's handler,
				// from the revision it was drawn at.
				for _, want := range []string{
					`action="/beads/` + b.ID + `"`,
					`<input type="hidden" name="rev" value="` + rev + `">`,
					`<input type="hidden" name="ask" value="` + strconv.FormatUint(seq, 10) + `">`,
				} {
					if !strings.Contains(body, want) {
						t.Errorf("%s: the card lacks %q:\n%s", s.name, want, body)
					}
				}
				if c.weighty {
					if !strings.Contains(body, approveButton) || strings.Contains(body, noGateLine) {
						t.Errorf("%s: the weighty card is withheld, or says it has no gate:\n%s", s.name, body)
					}
					continue
				}
				if c.give && s.name == "the bead's page" {
					if !strings.Contains(body, `value="give"`) || !strings.Contains(body, `type="password"`) ||
						strings.Contains(body, `value="approve"`) || strings.Contains(body, noGateLine) {
						t.Errorf("%s: the secret card withholds its giving, approves, or says it has no gate:\n%s", s.name, body)
					}
					continue
				}
				if strings.Contains(body, `value="approve"`) || strings.Contains(body, `value="give"`) ||
					strings.Contains(body, `type="password"`) || !strings.Contains(body, noGateLine) {
					t.Errorf("%s: the weighty card offers a control, or lacks its line:\n%s", s.name, body)
				}
			}
			form := y.approveForm(b.ID, seq)
			for k, v := range extra {
				form[k] = v
			}
			r := y.post("/beads/"+b.ID, tok, form)
			if !c.weighty && !c.give {
				if r.status != http.StatusOK || !strings.Contains(r.body, "weighty") {
					t.Fatalf("POST at the handler: %d: %s", r.status, r.body)
				}
			} else {
				loc, err := url.Parse(r.header.Get("Location"))
				if r.status != http.StatusSeeOther || err != nil || loc.Path != "/edge/reauth" {
					t.Fatalf("POST the approval: %d %q, want the gateway re-authentication", r.status, r.header.Get("Location"))
				}
			}
			if c.intact != nil {
				c.intact(t, y)
			}
			if got := y.askedState(b.ID, seq, "jo"); got != "open" {
				t.Fatalf("the ask is %s after the post, want it open", got)
			}
			if w := y.witness(); w != "" {
				t.Fatalf("the post is witnessed %q, want none", w)
			}
			if !c.complete {
				return
			}
			// The gateway authenticates after the challenge, and the return
			// lands on the bead, where the answered card shows.
			loc, _ := url.Parse(r.header.Get("Location"))
			back := loc.Query().Get("return_to")
			if want := "/beads/" + b.ID + "/fresh/" + strconv.FormatUint(seq, 10) + "/"; !strings.HasPrefix(back, want) {
				t.Fatalf("the return is %q, want state bound to ask %d", back, seq)
			}
			fresh := y.token("jo@example.net", authAt(time.Now().Add(5*time.Second)))
			if r := y.getNoFollow(back, fresh); r.status != http.StatusSeeOther || r.header.Get("Location") != "/beads/"+b.ID {
				t.Fatalf("GET the return: %d %q: %s", r.status, r.header.Get("Location"), r.body)
			}
			if got := y.askedState(b.ID, seq, "jo"); got != "answered" {
				t.Fatalf("the ask is %s after the return, want it answered", got)
			}
			if w := y.witness(); w != store.WitnessFreshSignIn {
				t.Fatalf("the approval is witnessed %q, want a fresh sign-in", w)
			}
		})
	}
}

// claimChimeDecl is a source that tells a user and hears them back, so a
// dropped delivery offers a user key claim (store.ClaimAddress).
const claimChimeDecl = `name: chime
base: https://chime.example.com
credential: { env: CHIME_TOKEN }
calls:
  message:
    kind: update
    params: { chat: { type: string }, text: { type: string } }
    request: { method: POST, path: /send, body: { chat: "{chat}", text: "{text}" } }
notify:
  call: message
  address: { param: chat, env: CHIME_CHAT }
  text: text
hook:
  verify: { scheme: secret, signature: { header: Chime-Secret } }
  bodies: unique
  normalize: [normalize]
  keys: [chat]
hear:
  field: chat
  address: { env: CHIME_CHAT }
  from: operator
  subscribe: { type: message }
`

// userkeyClaim puts a user key claim card for user on its own bead and
// returns the bead and the ask, as a dropped delivery offers one: the
// claim names the yard's operator at claim time, so for a member the
// operator setting names them for the claim and is then put back, and
// their role is a member's again (SetUser), as it stood.
func (y *approveYard) userkeyClaim(user, address string) (string, uint64) {
	y.t.Helper()
	ctx := context.Background()
	if _, err := y.st.PutSource(ctx, "chime.yaml", claimChimeDecl, "jo"); err != nil {
		y.t.Fatal(err)
	}
	if user != "jo" {
		must(y.t, y.st.SetSetting(ctx, store.OperatorSetting, user, "jo"))
	}
	created, err := y.own.ClaimAddress(ctx, "chime", address, "is this you")
	must(y.t, err)
	if !created {
		y.t.Fatalf("no claim was offered for %s", user)
	}
	if user != "jo" {
		must(y.t, y.st.SetSetting(ctx, store.OperatorSetting, "jo", "jo"))
		if _, err := y.st.SetUser(ctx, user, map[string]string{store.UserRole: store.RoleMember}, "jo"); err != nil {
			y.t.Fatal(err)
		}
	}
	q, err := y.st.UserAsks(ctx, user, user)
	must(y.t, err)
	for _, o := range q.Open {
		if o.Run != nil && store.IsUserKeyRun(o.Run.Argv) {
			return o.Bead, o.Ask
		}
	}
	y.t.Fatalf("no user key claim waits for %s", user)
	return "", 0
}

// A member approves their own user key card on the hosted web under a
// fresh sign-in, as their own first-device pairing: the button stands on
// the bead's page and on the rows, the click starts the re-authentication,
// and the return approves it, witnessed a fresh sign-in. A stale or
// another user's return, and a replayed one, approve nothing. Inside the
// fresh window a second own card (their pairing) approves at once. A
// weighty card that is not their own offers a member no button, keeps the
// no-gate line, and a crafted click is refused before any round trip in
// words that say an operator's sign-in is what the page takes; an
// operator-raised user key card asked of the operator keeps the operator
// rule, and a member's crafted click on it is refused as asked of another.
func TestWebMemberOwnUserkeyCard(t *testing.T) {
	t.Parallel()
	y := newApproveYard(t, true)
	ctx := context.Background()
	must(t, y.st.SetSetting(ctx, store.NameSetting, "depot", "jo"))
	beadID, seq := y.userkeyClaim("sam", "sam-chat-1")
	sam := y.token("sam@example.net")

	// The member's own card draws the approval wherever it stands.
	for _, s := range approveSurfaces {
		body := y.surfaceBody(s.path(beadID), sam)
		if !strings.Contains(body, approveButton) || strings.Contains(body, noGateLine) {
			t.Errorf("%s: a member's own user key card is withheld, or says it has no gate:\n%s", s.name, body)
		}
	}
	// A stale return, a return as another user, and a state never minted
	// each approve nothing.
	r := y.post("/beads/"+beadID, sam, y.approveForm(beadID, seq))
	if r.status != http.StatusSeeOther {
		t.Fatalf("POST the approval: %d: %s", r.status, r.body)
	}
	loc, err := url.Parse(r.header.Get("Location"))
	if err != nil || loc.Path != "/edge/reauth" {
		t.Fatalf("the approval goes to %q, want the gateway re-authentication", r.header.Get("Location"))
	}
	back := loc.Query().Get("return_to")
	for _, c := range []struct {
		name, path, token, want string
	}{
		{"a stale sign-in", back, y.token("sam@example.net", authAt(time.Now().Add(-time.Hour))), "not fresh"},
		{"another user", back, y.token("jo@example.net", authAt(time.Now().Add(5*time.Second))), "another card"},
		{"a state never minted", "/beads/" + beadID + "/fresh/" + strconv.FormatUint(seq, 10) + "/never-minted", y.token("sam@example.net", authAt(time.Now().Add(5*time.Second))), "used already"},
	} {
		if r := y.getNoFollow(c.path, c.token); r.status != 200 || !strings.Contains(r.body, c.want) {
			t.Errorf("%s: GET the return: %d: %s", c.name, r.status, r.body)
		}
	}
	if got := y.askedState(beadID, seq, "sam"); got != "open" {
		t.Fatalf("the ask is %s after the refusals, want it open", got)
	}
	freshSam := y.token("sam@example.net", authAt(time.Now().Add(5*time.Second)))
	if r := y.getNoFollow(back, freshSam); r.status != http.StatusSeeOther || r.header.Get("Location") != "/beads/"+beadID {
		t.Fatalf("GET the return: %d %q: %s", r.status, r.header.Get("Location"), r.body)
	}
	if got := y.askedState(beadID, seq, "sam"); got != "answered" {
		t.Fatalf("the ask is %s after the return, want it answered", got)
	}
	if w := y.witness(); w != store.WitnessFreshSignIn {
		t.Fatalf("the approval is witnessed %q, want a fresh sign-in", w)
	}
	if r := y.getNoFollow(back, freshSam); r.status != 200 || !strings.Contains(r.body, "used already") {
		t.Fatalf("replaying the return: %d: %s", r.status, r.body)
	}

	// Inside the fresh window the member's own pairing approves at once,
	// with no new sign-in.
	pairBead := y.bead("pair the member's watch")
	pairSeq := y.askRun(pairBead.ID, "sam", "user", "device", "pair", "sam", "watch", "--url", "https://mac.example.net")
	if body := y.surfaceBody("/beads/"+pairBead.ID, sam); !strings.Contains(body, approveButton) {
		t.Fatalf("a member's own pairing card is withheld:\n%s", body)
	}
	r = y.post("/beads/"+pairBead.ID, freshSam, y.approveForm(pairBead.ID, pairSeq))
	if r.status != http.StatusOK || !strings.Contains(r.body, "code <code>") {
		t.Fatalf("POST the pairing inside the window: %d: %s", r.status, r.body)
	}
	if got := y.askedState(pairBead.ID, pairSeq, "sam"); got != "answered" {
		t.Fatalf("the pairing ask is %s, want it answered", got)
	}

	// A weighty card asked of the member that is not their own: no button
	// anywhere, and a crafted click refuses before any round trip.
	other := y.bead("the member's identity, asked of the member")
	y.putCRM(false)
	otherSeq := y.askRun(other.ID, "sam", "user", "identity", "sam", "--source", "crm", "--id", "7")
	for _, s := range approveSurfaces {
		body := y.surfaceBody(s.path(other.ID), sam)
		if strings.Contains(body, `value="approve"`) || !strings.Contains(body, noGateLine) {
			t.Errorf("%s: a member's non-own weighty card offers approval, or lacks its line:\n%s", s.name, body)
		}
	}
	// The operator's view of the same surfaces is unchanged: no card drawn
	// for the operator here, since the ask is the member's.
	r = y.post("/beads/"+other.ID, sam, y.approveForm(other.ID, otherSeq))
	if r.status != http.StatusOK || !strings.Contains(r.body, "sign-in is what this page takes") ||
		!strings.Contains(r.body, "footplate") {
		t.Fatalf("POST a non-own approval as a member: %d: %s", r.status, r.body)
	}
	if u, err := y.st.GetUser(ctx, "sam"); err != nil || u.IsIn("crm", "7") {
		t.Fatalf("the refused approval wrote the member's identity: %+v, %v", u, err)
	}
	if got := y.askedState(other.ID, otherSeq, "sam"); got != "open" {
		t.Fatalf("the ask is %s after the refusal, want it open", got)
	}

	// An operator-raised user key card asked of the operator keeps the
	// operator rule: the operator approves it under a fresh sign-in, and
	// the member's page draws no card for it at all.
	opBead, opSeq := y.userkeyClaim("jo", "jo-chat-1")
	jo := y.token("jo@example.net")
	if body := y.surfaceBody("/beads/"+opBead, jo); !strings.Contains(body, approveButton) {
		t.Fatalf("the operator's own user key card is withheld:\n%s", body)
	}
	// It is no member's own card: a member's crafted click on it is
	// refused as a card asked of another, and the ask stays open.
	if r := y.post("/beads/"+opBead, sam, y.approveForm(opBead, opSeq)); r.status != http.StatusOK ||
		!strings.Contains(r.body, "asked of another") {
		t.Fatalf("POST the operator's card as a member: %d: %s", r.status, r.body)
	}
	if got := y.askedState(opBead, opSeq, "jo"); got != "open" {
		t.Fatalf("the operator's ask is %s after the member's click, want it open", got)
	}
	r = y.post("/beads/"+opBead, jo, y.approveForm(opBead, opSeq))
	loc, err = url.Parse(r.header.Get("Location"))
	if r.status != http.StatusSeeOther || err != nil || loc.Path != "/edge/reauth" {
		t.Fatalf("POST the operator's approval: %d %q: %s", r.status, r.header.Get("Location"), r.body)
	}
	freshJo := y.token("jo@example.net", authAt(time.Now().Add(5*time.Second)))
	if r := y.getNoFollow(loc.Query().Get("return_to"), freshJo); r.status != http.StatusSeeOther {
		t.Fatalf("GET the operator's return: %d: %s", r.status, r.body)
	}
	if got := y.askedState(opBead, opSeq, "jo"); got != "answered" {
		t.Fatalf("the operator's ask is %s after the return, want it answered", got)
	}
}

// approveSetupDecl is the one minted source of the setup pack the tests'
// reader serves, with a read call, an update call, and a mint command: a
// first install is strong.
const approveSetupDecl = `name: setupmail
base: https://setupmail.example.com/v1
credential:
  env: SETUP_TOKEN
  header: "Authorization: Bearer {credential}"
  mint: [SETUP_ACCOUNT]
  mint_command: [setup-mint]
calls:
  search: { kind: read, request: { method: GET, path: /messages } }
  draft: { kind: update, request: { method: POST, path: /drafts } }
`

const approveSetupMint = "#!/bin/sh\n# mint a token from the account\n"

// servesSetupmail has the store read the one pack the setup tests apply:
// setupmail, whose source mints its token from an account (a credential),
// brings the mint command (code) and offers a preset that updates
// (outside).
func servesSetupmail(own *store.Store) { servesSetupmailOf(own, approveSetupDecl) }

// servesSetupmailOf is servesSetupmail with the declaration decl in place of
// the pack's own.
func servesSetupmailOf(own *store.Store, decl string) {
	sum := sha256.Sum256([]byte(approveSetupMint))
	own.SetPackReader(func(ctx context.Context, name string, depot string, mappings store.Mappings, yard store.API) (*store.PackReading, error) {
		if name != "setupmail" {
			return nil, errors.New("no pack " + name)
		}
		return &store.PackReading{Name: "setupmail", Kind: pack.KindSource,
			Label: "embedded in yardr test1", Build: "test1",
			Sources: []store.AskSource{{Name: "setupmail", Text: decl,
				Files: []store.AskFile{{Program: "setup-mint", Text: approveSetupMint, SHA256: hex.EncodeToString(sum[:])}}}},
			Presets: []store.AskPreset{
				{Name: "read", Grants: []store.AskPresetGrant{{Source: "setupmail", Calls: []string{"search"}}}},
				{Name: "drafts", Grants: []store.AskPresetGrant{{Source: "setupmail", Calls: []string{"search", "draft"}}}},
			}}, nil
	})
}

// servesLightSetupmail has the store read a setupmail with no command
// file and a read-only preset, so its setup card weighs credential alone:
// a middle setup card.
func servesLightSetupmail(own *store.Store) {
	const decl = "name: setupmail\nbase: https://setupmail.example.com/v1\ncredential: { env: SETUP_TOKEN, header: \"Authorization: Bearer {credential}\" }\ncalls:\n  search: { kind: read, request: { method: GET, path: /messages } }\n"
	own.SetPackReader(func(ctx context.Context, name string, depot string, mappings store.Mappings, yard store.API) (*store.PackReading, error) {
		if name != "setupmail" {
			return nil, errors.New("no pack " + name)
		}
		return &store.PackReading{Name: "setupmail", Kind: pack.KindSource,
			Label: "embedded in yardr test1", Build: "test1",
			Sources: []store.AskSource{{Name: "setupmail", Text: decl}},
			Presets: []store.AskPreset{
				{Name: "read", Grants: []store.AskPresetGrant{{Source: "setupmail", Calls: []string{"search"}}}},
			}}, nil
	})
}

// A strong setup approves on the bead page as a run card does: the card
// reads with the pack and the yard's sentences, the approval starts the
// gateway re-authentication, and the return carries the setup out under
// the fresh sign-in. Home and the line carry the same form: the presets,
// the account typed unseen and the candidate, posting to the bead's
// handler with no script, and no pick or note beside it.
func TestWebApprovesAStrongSetupCard(t *testing.T) {
	t.Parallel()
	y := newApproveYard(t, true)
	servesSetupmail(y.own)
	b := y.bead("set up mail")
	seq := y.askSetup(b.ID)

	tok := y.token("jo@example.net")
	page := jwtGet(t, y.ts, "/beads/"+b.ID, web.DefaultJWTHeader, tok)
	if page.status != 200 {
		t.Fatalf("GET the bead: %d: %s", page.status, page.body)
	}
	if !strings.Contains(page.body, "setupmail") || !strings.Contains(page.body, "need-approve") {
		t.Fatalf("the setup card shows no approval: %s", page.body)
	}
	for _, want := range []string{
		`<p class="ask-says">yardmaster says:</p>`,
		`<p>When you approve, the yard applies the pack setupmail as jo, and nothing else:</p>`,
		approveButton + `</form>`,
		// On its bead's page the form answers in place.
		`<form class="need-approve" method="post" action="/beads/` + b.ID + `" hx-post="/beads/` + b.ID + `" hx-target="#bead"`,
	} {
		if !strings.Contains(page.body, want) {
			t.Errorf("the setup card lacks %q:\n%s", want, page.body)
		}
	}
	for _, s := range approveSurfaces[1:] {
		body := y.surfaceBody(s.path(b.ID), tok)
		for _, want := range []string{
			`<form class="need-approve" method="post" action="/beads/` + b.ID + `"><input type="hidden" name="rev"`,
			`<input type="radio" name="option" value="1">read</label>`,
			`<input type="radio" name="option" value="2">drafts</label>`,
			`<input type="password" name="secret-SETUP_ACCOUNT" placeholder="SETUP_ACCOUNT" aria-label="SETUP_ACCOUNT" autocomplete="off">`,
			`<input type="text" name="account" placeholder="Candidate source, or type above" aria-label="Candidate source">`,
			approveButton + `</form>`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: the setup card lacks %q:\n%s", s.name, want, body)
			}
		}
		if strings.Contains(body, `name="host"`) {
			t.Errorf("%s: the card of a pack with no template asks a host:\n%s", s.name, body)
		}
		// A preset is picked in the approval, never answered by a button,
		// and a setup takes no words as a note.
		for _, foreign := range []string{`<button type="submit" name="option"`, `value="send"`, `hx-target="#bead"`} {
			if strings.Contains(body, foreign) {
				t.Errorf("%s: the setup card carries %q:\n%s", s.name, foreign, body)
			}
		}
	}
	missing := y.bead("set up mail without account")
	missingSeq := y.askSetup(missing.ID)
	missingForm := y.approveForm(missing.ID, missingSeq)
	missingForm.Set("option", "2")
	r := y.post("/beads/"+missing.ID, y.token("jo@example.net"), missingForm)
	if r.status != http.StatusSeeOther {
		t.Fatalf("POST setup without account: %d: %s", r.status, r.body)
	}
	missingLoc, err := url.Parse(r.header.Get("Location"))
	if err != nil || missingLoc.Path != "/edge/reauth" {
		t.Fatalf("setup without account goes to %q, want the gateway re-authentication", r.header.Get("Location"))
	}
	missingFresh := y.token("jo@example.net", authAt(time.Now().Add(5*time.Second)))
	r = y.getNoFollow(missingLoc.Query().Get("return_to"), missingFresh)
	if r.status != http.StatusOK || !strings.Contains(r.body, "SETUP_ACCOUNT was not given") {
		t.Fatalf("the setup return without account: %d: %s", r.status, r.body)
	}
	if got := y.askedState(missing.ID, missingSeq, "jo"); got != "open" {
		t.Fatalf("the setup ask is %s after the refusal, want it open", got)
	}
	if _, err := y.st.GetSource(context.Background(), "setupmail"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the refused setup wrote the source: %v", err)
	}
	form := y.approveForm(b.ID, seq)
	form.Set("option", "2")
	form.Set("secret-SETUP_ACCOUNT", "ops@example.com")
	r = y.post("/beads/"+b.ID, tok, form)
	if r.status != http.StatusSeeOther {
		t.Fatalf("POST the approval: %d: %s", r.status, r.body)
	}
	loc, err := url.Parse(r.header.Get("Location"))
	if err != nil || loc.Path != "/edge/reauth" {
		t.Fatalf("the setup approval goes to %q, want the gateway re-authentication", r.header.Get("Location"))
	}
	fresh := y.token("jo@example.net", authAt(time.Now().Add(5*time.Second)))
	r = y.getNoFollow(loc.Query().Get("return_to"), fresh)
	if r.status != http.StatusSeeOther || r.header.Get("Location") != "/beads/"+b.ID {
		t.Fatalf("GET the return: %d %q: %s", r.status, r.header.Get("Location"), r.body)
	}
	if got := y.askedState(b.ID, seq, "jo"); got != "answered" {
		t.Fatalf("the ask is %s after the return, want it answered", got)
	}
	if w := y.witness(); w != store.WitnessFreshSignIn {
		t.Fatalf("the setup approval is witnessed %q, want a fresh sign-in", w)
	}
	t.Run("host", testWebSetupCardAsksAHostAndCarriesItThroughTheSignIn)
	t.Run("site", testWebSetupCardAsksASiteAndCarriesItThroughTheSignIn)
}

// A setup whose source declares a template asks the host above the account on
// its form, on the bead's page and from Home and the line alike, and the host
// posted with the approval goes through the fresh sign-in with the rest of it
// and reaches the carrying record.
func testWebSetupCardAsksAHostAndCarriesItThroughTheSignIn(t *testing.T) {
	y := newApproveYard(t, true)
	servesSetupmailOf(y.own, strings.Replace(approveSetupDecl, "base: https://setupmail.example.com/v1\n",
		"base: https://setupmail.example.com/v1\nbase_template: https://{host}/v1\n", 1))
	b := y.bead("set up mail on a server of our own")
	seq := y.askSetup(b.ID)
	tok := y.token("jo@example.net")
	const hostField = `<input type="text" name="host" placeholder="Host of a server of your own: https://{host}/v1" aria-label="Host" autocomplete="off" spellcheck="false">`
	for _, s := range approveSurfaces {
		body := y.surfaceBody(s.path(b.ID), tok)
		at, account := strings.Index(body, hostField), strings.Index(body, `<input type="password" name="secret-SETUP_ACCOUNT"`)
		if at < 0 || account < 0 || at > account {
			t.Errorf("%s: the host field stands at %d and the account's at %d, want the host's above the account's:\n%s", s.name, at, account, body)
		}
	}
	form := y.approveForm(b.ID, seq)
	form.Set("option", "1")
	form.Set("secret-SETUP_ACCOUNT", "ops@example.com")
	form.Set("host", " 192.168.1.20:8443 ")
	r := y.post("/beads/"+b.ID, tok, form)
	loc, err := url.Parse(r.header.Get("Location"))
	if r.status != http.StatusSeeOther || err != nil || loc.Path != "/edge/reauth" {
		t.Fatalf("POST the approval: %d %q: %s", r.status, r.header.Get("Location"), r.body)
	}
	fresh := y.token("jo@example.net", authAt(time.Now().Add(5*time.Second)))
	r = y.getNoFollow(loc.Query().Get("return_to"), fresh)
	if r.status != http.StatusSeeOther || r.header.Get("Location") != "/beads/"+b.ID {
		t.Fatalf("GET the return: %d %q: %s", r.status, r.header.Get("Location"), r.body)
	}
	if got := y.askedState(b.ID, seq, "jo"); got != "answered" {
		t.Fatalf("the ask is %s after the return, want it answered", got)
	}
	if rec, err := y.own.CarryOf(context.Background(), b.ID, seq); err != nil || rec.Host != "192.168.1.20:8443" {
		t.Fatalf("the carrying record = %+v, %v", rec, err)
	}
}

// A setup whose source declares a path template asks the site above the
// account on its form, as it asks a host, and the site posted with the
// approval goes through the fresh sign-in and reaches the carrying record.
func testWebSetupCardAsksASiteAndCarriesItThroughTheSignIn(t *testing.T) {
	y := newApproveYard(t, true)
	servesSetupmailOf(y.own, strings.Replace(approveSetupDecl, "base: https://setupmail.example.com/v1\n",
		"base: https://setupmail.example.com/ex/example/v1\npath_template: https://setupmail.example.com/ex/{site}/v1\n", 1))
	b := y.bead("set up mail for a site of our own")
	seq := y.askSetup(b.ID)
	tok := y.token("jo@example.net")
	const siteField = `<input type="text" name="site" placeholder="Site of your own: https://setupmail.example.com/ex/{site}/v1" aria-label="Site" autocomplete="off" spellcheck="false">`
	for _, s := range approveSurfaces {
		body := y.surfaceBody(s.path(b.ID), tok)
		at, account := strings.Index(body, siteField), strings.Index(body, `<input type="password" name="secret-SETUP_ACCOUNT"`)
		if at < 0 || account < 0 || at > account {
			t.Errorf("%s: the site field stands at %d and the account's at %d, want the site's above the account's:\n%s", s.name, at, account, body)
		}
	}
	form := y.approveForm(b.ID, seq)
	form.Set("option", "1")
	form.Set("secret-SETUP_ACCOUNT", "ops@example.com")
	form.Set("site", " cloud-1 ")
	r := y.post("/beads/"+b.ID, tok, form)
	loc, err := url.Parse(r.header.Get("Location"))
	if r.status != http.StatusSeeOther || err != nil || loc.Path != "/edge/reauth" {
		t.Fatalf("POST the approval: %d %q: %s", r.status, r.header.Get("Location"), r.body)
	}
	fresh := y.token("jo@example.net", authAt(time.Now().Add(5*time.Second)))
	r = y.getNoFollow(loc.Query().Get("return_to"), fresh)
	if r.status != http.StatusSeeOther || r.header.Get("Location") != "/beads/"+b.ID {
		t.Fatalf("GET the return: %d %q: %s", r.status, r.header.Get("Location"), r.body)
	}
	if got := y.askedState(b.ID, seq, "jo"); got != "answered" {
		t.Fatalf("the ask is %s after the return, want it answered", got)
	}
	if rec, err := y.own.CarryOf(context.Background(), b.ID, seq); err != nil || rec.Site != "cloud-1" {
		t.Fatalf("the carrying record = %+v, %v", rec, err)
	}
}

// answerForm is the card's answer form in body, from its tag to its end:
// what a person has in front of them to answer with.
func answerForm(t *testing.T, body string) string {
	t.Helper()
	_, rest, ok := strings.Cut(body, `<form class="need-answer"`)
	if !ok {
		t.Fatalf("the page has no answer form:\n%s", body)
	}
	form, _, _ := strings.Cut(rest, `</form>`)
	return form
}

// A run or setup card declines from its bead's page as it does from Home
// and the line. The yard refuses a decline without its reason, so the
// words field stands beside Decline on all three, though the card takes no
// words as an answer and offers no button for one. A decline posted
// without words is still refused, with the ask open; with them it ends the
// ask and keeps the reason.
func TestWebDeclinesARunOrSetupCard(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		ask  func(y *approveYard, bead string) uint64
	}{
		{"run", func(y *approveYard, bead string) uint64 {
			return y.askRun(bead, "jo", "user", "add", "bob")
		}},
		{"setup", func(y *approveYard, bead string) uint64 {
			servesSetupmail(y.own)
			return y.askSetup(bead)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			y := newApproveYard(t, true)
			b := y.bead("decline a " + c.name + " card")
			seq := c.ask(y, b.ID)
			ask := strconv.FormatUint(seq, 10)
			tok := y.token("jo@example.net")
			for _, s := range approveSurfaces {
				form := answerForm(t, y.surfaceBody(s.path(b.ID), tok))
				if !strings.Contains(form, wordsField) || !strings.Contains(form, declineButton) {
					t.Errorf("%s: the card's form lacks the words field or Decline:\n%s", s.name, form)
				}
				// Neither card takes words as its answer or as a note.
				for _, foreign := range []string{`name="action" value="answer"`, `name="action" value="send"`} {
					if strings.Contains(form, foreign) {
						t.Errorf("%s: the card's form offers %q:\n%s", s.name, foreign, form)
					}
				}
			}
			form := url.Values{"rev": {y.revision(b.ID)}, "ask": {ask}, "action": {"decline"}}
			r := y.post("/beads/"+b.ID, tok, form)
			if want := "declining ask " + ask + " needs words"; r.status != http.StatusOK || !strings.Contains(r.body, want) {
				t.Fatalf("POST the decline without words: %d, want the page redrawn with %q:\n%s", r.status, want, r.body)
			}
			if got := y.askedState(b.ID, seq, "jo"); got != "open" {
				t.Fatalf("the ask is %s after the refused decline, want it open", got)
			}
			form.Set("words", "not this one")
			r = y.post("/beads/"+b.ID, tok, form)
			if r.status != http.StatusSeeOther || r.header.Get("Location") != "/beads/"+b.ID {
				t.Fatalf("POST the decline with words: %d %q: %s", r.status, r.header.Get("Location"), r.body)
			}
			v, err := y.st.Bead(context.Background(), b.ID, "jo")
			must(t, err)
			if len(v.Asks) != 1 || v.Asks[0].State != "declined" || v.Asks[0].Words != "not this one" {
				t.Fatalf("the ask: %+v, want it declined with the words", v.Asks)
			}
		})
	}
}

// A setup card's typed account waits at the gateway no longer than a secret
// ask's values do: every setup card is weighty, so each one approved in the
// hosted web holds what was typed in this view's memory until the sign-in
// returns, and a return past the short bound finds it gone and approves
// nothing.
func TestWebSetupChallengeHoldsItsAccountNoLongerThanASecret(t *testing.T) {
	t.Parallel()
	y := newApproveYard(t, true)
	servesSetupmail(y.own)
	b := y.bead("set up mail")
	seq := y.askSetup(b.ID)
	form := y.approveForm(b.ID, seq)
	form.Set("option", "1")
	form.Set("secret-SETUP_ACCOUNT", "ops@example.com")
	r := y.post("/beads/"+b.ID, y.token("jo@example.net"), form)
	loc, err := url.Parse(r.header.Get("Location"))
	if r.status != http.StatusSeeOther || err != nil || loc.Path != "/edge/reauth" {
		t.Fatalf("POST the approval: %d %q: %s", r.status, r.header.Get("Location"), r.body)
	}
	y.srv.AgeFreshPending(16 * time.Minute)
	fresh := y.token("jo@example.net", authAt(time.Now().Add(5*time.Second)))
	r = y.getNoFollow(loc.Query().Get("return_to"), fresh)
	if r.status != http.StatusOK || !strings.Contains(r.body, "used already, expired, or never started") {
		t.Fatalf("GET the return past the short bound: %d %q: %s", r.status, r.header.Get("Location"), r.body)
	}
	if got := y.askedState(b.ID, seq, "jo"); got != "open" {
		t.Fatalf("the ask is %s after a return past the short bound, want it open", got)
	}
}
