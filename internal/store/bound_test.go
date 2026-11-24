package store

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"yardr.dev/yardr/internal/source"
)

// boundFile is a source whose one mint input is bound: GOG_ACCOUNT names
// its holder's account, as the google packs' GOOGLE_ACCOUNT does, for a
// service that is not Google (the generality proof).
const boundFile = `name: gog
base: https://gog.example.com
credential:
  env: GOG_TOKEN
  header: "Authorization: Bearer {credential}"
  mint:
    - name: GOG_ACCOUNT
      secret: false
      bound: true
calls:
  ping: { kind: read, request: { method: GET, path: /me } }
`

// boundYard is a secret yard with the bound source gog and the user bia
// beside ada.
func boundYard(t *testing.T) *askYard {
	t.Helper()
	y, _ := secretYard(t)
	if _, err := y.PutSource(y.ctx, "gog.yaml", boundFile, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.AddUser(y.ctx, "bia", "mckean"); err != nil {
		t.Fatal(err)
	}
	return y
}

// A bound value belongs to one holder: giving it to another, for a user
// or for the yard, typed or on a card, is refused naming the holder and
// the giving that frees it, and writes nothing. Its own holder gives it
// again, and an unbound source takes one value for yard and user alike.
func TestBoundGiveHoldsOneHolder(t *testing.T) {
	t.Parallel()
	y := boundYard(t)
	const yardAddr, adaAddr = "yard@example.com", "ada@example.com"

	if _, err := y.KeySource(y.ctx, "gog", map[string]string{"GOG_ACCOUNT": yardAddr}, "mckean"); err != nil {
		t.Fatal(err)
	}
	for value, want := range map[string]source.Holder{
		yardAddr:             {Yard: true, Source: "gog"},
		adaAddr:              {},
		"nobody@example.com": {},
	} {
		held, err := y.BoundHolder(y.ctx, "gog", "GOG_ACCOUNT", value, source.Holder{})
		if err != nil || held != want {
			t.Errorf("BoundHolder(%s) = %+v, %v, want %+v", value, held, err, want)
		}
	}
	// Passing except skips that holder so an own-address check sees nobody else.
	if held, err := y.BoundHolder(y.ctx, "gog", "GOG_ACCOUNT", yardAddr, source.Holder{Yard: true}); err != nil || !held.IsZero() {
		t.Fatalf("BoundHolder(yard's, except yard) = %+v, %v", held, err)
	}
	// Cross-source: a second bound source sharing the input name. The
	// yard holds yardAddr on gog alone, yet asking for gog2 reports the
	// yard on gog, and giving it for gog2 refuses naming gog and pointing
	// the freeing giving at gog.
	gog2File := strings.Replace(boundFile, "name: gog", "name: gog2", 1)
	if _, err := y.PutSource(y.ctx, "gog2.yaml", gog2File, "mckean"); err != nil {
		t.Fatal(err)
	}
	if held, err := y.BoundHolder(y.ctx, "gog2", "GOG_ACCOUNT", yardAddr, source.Holder{}); err != nil || held != (source.Holder{Yard: true, Source: "gog"}) {
		t.Fatalf("BoundHolder(gog2, yard's on gog) = %+v, %v", held, err)
	}
	if _, err := y.KeyUserCredential(y.ctx, "ada", "gog2", map[string]string{"GOG_ACCOUNT": yardAddr}, "ada"); err == nil ||
		!strings.Contains(err.Error(), "source gog2: GOG_ACCOUNT is held by the yard already (on source gog)") ||
		!strings.Contains(err.Error(), "give a different GOG_ACCOUNT with yardr source key gog2 --as ada") ||
		!strings.Contains(err.Error(), "or have the yard give a different GOG_ACCOUNT with yardr source key gog and give this again") ||
		strings.Contains(err.Error(), yardAddr) {
		t.Fatalf("the yard's address on gog given to ada for gog2 = %v", err)
	}
	if u, err := y.GetUser(y.ctx, "ada"); err != nil || u.EnvFile != "" {
		t.Fatalf("a refused cross-source giving set ada's file: %+v, %v", u, err)
	}
	// On a card for the second source, as typed: the yard's address for bia.
	crossAsk := userSecretAsk("bia", "bia")
	crossAsk.Secret.Source = "gog2"
	seq := y.ask(t, crossAsk, "yardmaster")
	if _, err := y.GiveSecret(y.ctx, y.bead, seq, map[string]string{"GOG_ACCOUNT": yardAddr}, "bia", true); err == nil ||
		!strings.Contains(err.Error(), "GOG_ACCOUNT is held by the yard already (on source gog)") ||
		!strings.Contains(err.Error(), "yardr source key gog and give this again") {
		t.Fatalf("the yard's address given to bia on a card for gog2 = %v", err)
	}
	// The yard re-giving its own value on the second source stands
	// (except-self), leaving the value held on both sources; a lookup
	// then reports the target source first.
	gog2Keyed, err := y.KeySource(y.ctx, "gog2", map[string]string{"GOG_ACCOUNT": yardAddr}, "mckean")
	if err != nil {
		t.Fatalf("the yard's own again on gog2: %v", err)
	}
	if held, err := y.BoundHolder(y.ctx, "gog2", "GOG_ACCOUNT", yardAddr, source.Holder{}); err != nil || held != (source.Holder{Yard: true, Source: "gog2"}) {
		t.Fatalf("BoundHolder(double-held, target gog2) = %+v, %v", held, err)
	}
	if held, err := y.BoundHolder(y.ctx, "gog", "GOG_ACCOUNT", yardAddr, source.Holder{}); err != nil || held != (source.Holder{Yard: true, Source: "gog"}) {
		t.Fatalf("BoundHolder(double-held, target gog) = %+v, %v", held, err)
	}
	if held, err := y.BoundHolder(y.ctx, "gog2", "GOG_ACCOUNT", yardAddr, source.Holder{Yard: true}); err != nil || !held.IsZero() {
		t.Fatalf("BoundHolder(double-held, except yard) = %+v, %v", held, err)
	}
	// An unreadable second yard file fails closed, even for a value
	// nobody holds: it may hold the value unseen.
	if err := os.Remove(gog2Keyed.File); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(gog2Keyed.File, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := y.BoundHolder(y.ctx, "gog", "GOG_ACCOUNT", "nobody@example.com", source.Holder{}); err == nil {
		t.Fatalf("BoundHolder with an unreadable gog2 file = no error")
	}
	if err := os.Remove(gog2Keyed.File); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gog2Keyed.File, []byte("GOG_ACCOUNT="+yardAddr+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// On a card, as typed: the yard's address for ada.
	seq = y.ask(t, userSecretAsk("bia", "bia"), "yardmaster")
	if _, err := y.GiveSecret(y.ctx, y.bead, seq, map[string]string{"GOG_ACCOUNT": yardAddr}, "bia", true); err == nil ||
		!strings.Contains(err.Error(), "GOG_ACCOUNT is held by the yard already") ||
		!strings.Contains(err.Error(), "a bound input belongs to one holder") ||
		!strings.Contains(err.Error(), "give this again") || strings.Contains(err.Error(), yardAddr) {
		t.Fatalf("the yard's address given to bia on a card = %v", err)
	}
	if n := len(answerNotes(t, y)); n != 0 {
		t.Fatalf("a refused giving left %d answer notes", n)
	}
	if _, err := y.KeyUserCredential(y.ctx, "ada", "gog", map[string]string{"GOG_ACCOUNT": yardAddr}, "ada"); err == nil ||
		!strings.Contains(err.Error(), "GOG_ACCOUNT is held by the yard already") ||
		!strings.Contains(err.Error(), "or have the yard give a different GOG_ACCOUNT with yardr source key gog and give this again") ||
		strings.Contains(err.Error(), yardAddr) {
		t.Fatalf("the yard's address given to ada = %v", err)
	}
	if u, err := y.GetUser(y.ctx, "ada"); err != nil || u.EnvFile != "" {
		t.Fatalf("a refused giving set ada's file: %+v, %v", u, err)
	}

	adaKeyed, err := y.KeyUserCredential(y.ctx, "ada", "gog", map[string]string{"GOG_ACCOUNT": adaAddr}, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if held, err := y.BoundHolder(y.ctx, "gog", "GOG_ACCOUNT", adaAddr, source.Holder{}); err != nil || held != (source.Holder{User: "ada"}) {
		t.Fatalf("BoundHolder(ada's) = %+v, %v", held, err)
	}
	if _, err := y.KeyUserCredential(y.ctx, "bia", "gog", map[string]string{"GOG_ACCOUNT": adaAddr}, "bia"); err == nil ||
		!strings.Contains(err.Error(), "GOG_ACCOUNT is held by ada already") || strings.Contains(err.Error(), adaAddr) {
		t.Fatalf("ada's address given to bia = %v", err)
	}
	if _, err := y.KeySource(y.ctx, "gog", map[string]string{"GOG_ACCOUNT": adaAddr}, "mckean"); err == nil ||
		!strings.Contains(err.Error(), "GOG_ACCOUNT is held by ada already") ||
		!strings.Contains(err.Error(), "give a different GOG_ACCOUNT with yardr source key gog,") {
		t.Fatalf("ada's address given to the yard = %v", err)
	}
	// Cross-source, user-held: giving ada's address for gog2, to another
	// user and to the yard alike, is refused as for gog (a user's file is
	// already global, so its holding names no source).
	if held, err := y.BoundHolder(y.ctx, "gog2", "GOG_ACCOUNT", adaAddr, source.Holder{}); err != nil || held != (source.Holder{User: "ada"}) {
		t.Fatalf("BoundHolder(gog2, ada's) = %+v, %v", held, err)
	}
	if _, err := y.KeyUserCredential(y.ctx, "bia", "gog2", map[string]string{"GOG_ACCOUNT": adaAddr}, "bia"); err == nil ||
		!strings.Contains(err.Error(), "source gog2: GOG_ACCOUNT is held by ada already") || strings.Contains(err.Error(), adaAddr) {
		t.Fatalf("ada's address given to bia for gog2 = %v", err)
	}
	if _, err := y.KeySource(y.ctx, "gog2", map[string]string{"GOG_ACCOUNT": adaAddr}, "mckean"); err == nil ||
		!strings.Contains(err.Error(), "GOG_ACCOUNT is held by ada already") {
		t.Fatalf("ada's address given to the yard for gog2 = %v", err)
	}
	// Each holder gives its own again.
	if _, err := y.KeyUserCredential(y.ctx, "ada", "gog", map[string]string{"GOG_ACCOUNT": adaAddr}, "ada"); err != nil {
		t.Fatalf("ada's own again: %v", err)
	}
	if _, err := y.KeySource(y.ctx, "gog", map[string]string{"GOG_ACCOUNT": yardAddr}, "mckean"); err != nil {
		t.Fatalf("the yard's own again: %v", err)
	}
	// A user literally named "yard" cannot take the yard's account, and
	// its own account is reported as yard's (not the yard's).
	if _, err := y.AddUser(y.ctx, "yard", "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.KeyUserCredential(y.ctx, "yard", "gog", map[string]string{"GOG_ACCOUNT": yardAddr}, "yard"); err == nil ||
		!strings.Contains(err.Error(), "GOG_ACCOUNT is held by the yard already") {
		t.Fatalf("the yard's address given to user named yard = %v", err)
	}
	if _, err := y.KeyUserCredential(y.ctx, "yard", "gog", map[string]string{"GOG_ACCOUNT": "user-yard@example.com"}, "yard"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.KeyUserCredential(y.ctx, "bia", "gog", map[string]string{"GOG_ACCOUNT": "user-yard@example.com"}, "bia"); err == nil ||
		!strings.Contains(err.Error(), "GOG_ACCOUNT is held by yard already") ||
		!strings.Contains(err.Error(), "yardr source key gog --as yard") {
		t.Fatalf("user yard's address given to bia = %v", err)
	}
	// Pre-existing shared value (yard + ada): re-giving to the yard is refused too.
	if _, _, err := WriteEnvFile(adaKeyed.File, map[string]string{"GOG_ACCOUNT": yardAddr}); err != nil {
		t.Fatal(err)
	}
	if _, err := y.KeySource(y.ctx, "gog", map[string]string{"GOG_ACCOUNT": yardAddr}, "mckean"); err == nil ||
		!strings.Contains(err.Error(), "GOG_ACCOUNT is held by ada already") {
		t.Fatalf("re-giving yard's address while ada also holds it = %v", err)
	}
	// Leading space after '=' in a hand-edited file still matches on give.
	if err := os.WriteFile(adaKeyed.File, []byte("GOG_ACCOUNT= "+adaAddr+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := y.KeyUserCredential(y.ctx, "bia", "gog", map[string]string{"GOG_ACCOUNT": adaAddr}, "bia"); err == nil ||
		!strings.Contains(err.Error(), "GOG_ACCOUNT is held by ada already") {
		t.Fatalf("space-padded ada address given to bia = %v", err)
	}
	// No bound input, no binding: crm takes one value for both.
	if _, err := y.KeySource(y.ctx, "crm", map[string]string{"CRM_TOKEN": "tok-shared"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.KeyUserCredential(y.ctx, "ada", "crm", map[string]string{"CRM_TOKEN": "tok-shared"}, "ada"); err != nil {
		t.Fatalf("an unbound value for yard and user: %v", err)
	}
}

// boundGog is boundFile against the stand-in at base, for a call.
func boundGog(base string) string {
	return strings.Replace(boundFile, "https://gog.example.com", base, 1)
}

// A call made as a user whose file names another holder's bound value is
// refused before anything mints or is sent, a file edited by hand as a
// given one is; the holder's own call and the yard's own mint as they
// always have.
func TestBoundCallHoldsOneHolder(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/me" || r.Header.Get("Authorization") != "Bearer tok-bound-minted" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"id":"app"}`)
	}))
	t.Cleanup(srv.Close)

	y, _ := secretYard(t)
	if _, err := y.PutSource(y.ctx, "gog.yaml", boundGog(srv.URL), "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.AddUser(y.ctx, "bia", "mckean"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	script := filepath.Join(dir, "mint")
	body := "#!/bin/sh\ncat >/dev/null\necho x >>" + runs + "\nprintf '{\"token\":\"tok-bound-minted\",\"expires_at\":\"2999-01-01T00:00:00Z\"}'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := y.SetSource(y.ctx, "gog", SourceMintCommand, script, "mckean"); err != nil {
		t.Fatal(err)
	}
	const yardAddr, adaAddr, biaAddr = "yard@example.com", "ada@example.com", "bia@example.com"
	if _, err := y.KeySource(y.ctx, "gog", map[string]string{"GOG_ACCOUNT": yardAddr}, "mckean"); err != nil {
		t.Fatal(err)
	}
	adaKeyed, err := y.KeyUserCredential(y.ctx, "ada", "gog", map[string]string{"GOG_ACCOUNT": adaAddr}, "ada")
	if err != nil {
		t.Fatal(err)
	}
	biaKeyed, err := y.KeyUserCredential(y.ctx, "bia", "gog", map[string]string{"GOG_ACCOUNT": biaAddr}, "bia")
	if err != nil {
		t.Fatal(err)
	}
	// By hand, past every giving: bia's file now names ada's account.
	// Both bia and ada (which sorts before bia) are refused while it is shared.
	if _, _, err := WriteEnvFile(biaKeyed.File, map[string]string{"GOG_ACCOUNT": adaAddr}); err != nil {
		t.Fatal(err)
	}
	_, err = y.CallSource(y.ctx, SourceCall{Source: "gog", Call: "ping", As: "bia", Actor: "mckean"})
	if err == nil || !strings.Contains(err.Error(), "GOG_ACCOUNT in "+biaKeyed.File+" names ada's account, not bia's") ||
		!strings.Contains(err.Error(), "a bound input belongs to one holder") || strings.Contains(err.Error(), adaAddr) {
		t.Fatalf("a call as bia on ada's account = %v", err)
	}
	_, err = y.CallSource(y.ctx, SourceCall{Source: "gog", Call: "ping", As: "ada", Actor: "mckean"})
	if err == nil || !strings.Contains(err.Error(), "GOG_ACCOUNT in "+adaKeyed.File+" names bia's account, not ada's") {
		t.Fatalf("a call as ada while bia also holds ada's account = %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("a refused call reached the service %d times", hits.Load())
	}
	if _, err := os.Stat(runs); !os.IsNotExist(err) {
		t.Fatalf("a refused call minted: %v", err)
	}
	// Once bia holds her own address again, ada's own and the yard's own mint as they always have.
	if _, _, err := WriteEnvFile(biaKeyed.File, map[string]string{"GOG_ACCOUNT": biaAddr}); err != nil {
		t.Fatal(err)
	}
	res, err := y.CallSource(y.ctx, SourceCall{Source: "gog", Call: "ping", As: "ada", Actor: "mckean"})
	if err != nil || string(res.Answer) != `{"id":"app"}` {
		t.Fatalf("a call as ada on her own account = %+v, %v", res, err)
	}
	res, err = y.CallSource(y.ctx, SourceCall{Source: "gog", Call: "ping", Actor: "mckean"})
	if err != nil || string(res.Answer) != `{"id":"app"}` || hits.Load() != 2 {
		t.Fatalf("the yard's own call = %+v, %v, after %d hits", res, err, hits.Load())
	}
	// The hole itself, by hand: ada's file names the yard's account.
	if _, _, err := WriteEnvFile(adaKeyed.File, map[string]string{"GOG_ACCOUNT": yardAddr}); err != nil {
		t.Fatal(err)
	}
	_, err = y.CallSource(y.ctx, SourceCall{Source: "gog", Call: "ping", As: "ada", Actor: "mckean"})
	if err == nil || !strings.Contains(err.Error(), "names the yard's account, not ada's") || hits.Load() != 2 {
		t.Fatalf("a call as ada on the yard's account = %v, after %d hits", err, hits.Load())
	}
	// Cross-source, the same hole on a second bound source: ada's file
	// names the yard's account, which the yard holds on gog alone, and a
	// call on gog2 refuses naming gog as the holding source.
	gog2Bound := strings.Replace(boundGog(srv.URL), "name: gog", "name: gog2", 1)
	if _, err := y.PutSource(y.ctx, "gog2.yaml", gog2Bound, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.SetSource(y.ctx, "gog2", SourceMintCommand, script, "mckean"); err != nil {
		t.Fatal(err)
	}
	_, err = y.CallSource(y.ctx, SourceCall{Source: "gog2", Call: "ping", As: "ada", Actor: "mckean"})
	if err == nil || !strings.Contains(err.Error(), "GOG_ACCOUNT in "+adaKeyed.File+" names the yard's (on source gog) account, not ada's") ||
		!strings.Contains(err.Error(), "a bound input belongs to one holder") || strings.Contains(err.Error(), yardAddr) || hits.Load() != 2 {
		t.Fatalf("a call as ada on gog2 on the yard's account on gog = %v, after %d hits", err, hits.Load())
	}
	// Alphabetically earlier user (ada) hand-edited to a later user's (bia's)
	// account must still be refused: lookup skips self so it sees bia.
	if _, _, err := WriteEnvFile(adaKeyed.File, map[string]string{"GOG_ACCOUNT": biaAddr}); err != nil {
		t.Fatal(err)
	}
	_, err = y.CallSource(y.ctx, SourceCall{Source: "gog", Call: "ping", As: "ada", Actor: "mckean"})
	if err == nil || !strings.Contains(err.Error(), "names bia's account, not ada's") || hits.Load() != 2 {
		t.Fatalf("a call as ada on bia's account = %v, after %d hits", err, hits.Load())
	}
	// Leading whitespace after '=' in a hand-edited file still matches
	// Credential.Select's trimmed value and is refused.
	if err := os.WriteFile(adaKeyed.File, []byte("GOG_ACCOUNT= "+yardAddr+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = y.CallSource(y.ctx, SourceCall{Source: "gog", Call: "ping", As: "ada", Actor: "mckean"})
	if err == nil || !strings.Contains(err.Error(), "names the yard's account, not ada's") || hits.Load() != 2 {
		t.Fatalf("a call as ada on space-padded yard account = %v, after %d hits", err, hits.Load())
	}
	// Hosted mint calls made as a user are also held to bound inputs before
	// the hosted minter runs.
	var hostedCalls atomic.Int32
	y.SetHostedMint(func(ctx context.Context, addr, src, account string) (string, time.Time, error) {
		hostedCalls.Add(1)
		return "tok-bound-minted", time.Now().Add(time.Hour), nil
	})
	if _, err := y.SetSource(y.ctx, "gog", SourceMintCommand, "yardr edge mint-google https://edge.example.com gog", "mckean"); err != nil {
		t.Fatal(err)
	}
	_, err = y.CallSource(y.ctx, SourceCall{Source: "gog", Call: "ping", As: "ada", Actor: "mckean"})
	if err == nil || !strings.Contains(err.Error(), "names the yard's account, not ada's") || hostedCalls.Load() != 0 {
		t.Fatalf("a hosted call as ada on the yard's account = %v, hostedCalls=%d", err, hostedCalls.Load())
	}
	if _, err := y.SetSource(y.ctx, "gog2", SourceMintCommand, "yardr edge mint-google https://edge.example.com gog2", "mckean"); err != nil {
		t.Fatal(err)
	}
	_, err = y.CallSource(y.ctx, SourceCall{Source: "gog2", Call: "ping", As: "ada", Actor: "mckean"})
	if err == nil || !strings.Contains(err.Error(), "names the yard's (on source gog) account, not ada's") || hostedCalls.Load() != 0 {
		t.Fatalf("a hosted call as ada on gog2 on the yard's account on gog = %v, hostedCalls=%d", err, hostedCalls.Load())
	}
}

// unboundFile is boundFile with the same mint input left unbound: what a
// second source that mints with the yard-bound name but binds nothing
// declares.
const unboundFile = `name: gogu
base: https://gog.example.com
credential:
  env: GOG_TOKEN
  header: "Authorization: Bearer {credential}"
  mint:
    - name: GOG_ACCOUNT
      secret: false
calls:
  ping: { kind: read, request: { method: GET, path: /me } }
`

// sharedFile mints from two names no source binds, as a shared
// application id and secret are: every holder may hold the same value.
const sharedFile = `name: lin
base: https://lin.example.com
credential:
  env: LIN_TOKEN
  header: "Authorization: Bearer {credential}"
  mint: [LINEAR_CLIENT_ID, LINEAR_SECRET]
calls:
  ping: { kind: read, request: { method: GET, path: /me } }
`

// An unbound mint input cannot take a value held as bound on another
// source: giving the yard's bound address through a source that leaves
// the name unbound is refused with the bound sentences, one pinned
// whole, and a name bound nowhere takes one value for every holder.
func TestBoundGiveUnboundTake(t *testing.T) {
	t.Parallel()
	y := boundYard(t)
	if _, err := y.PutSource(y.ctx, "gogu.yaml", unboundFile, "mckean"); err != nil {
		t.Fatal(err)
	}
	const yardAddr, adaAddr = "yard@example.com", "ada@example.com"
	if _, err := y.KeySource(y.ctx, "gog", map[string]string{"GOG_ACCOUNT": yardAddr}, "mckean"); err != nil {
		t.Fatal(err)
	}
	// The yard's bound address taken through the unbound source, pinned
	// whole: the landed refusal, with the holding source named.
	_, err := y.KeyUserCredential(y.ctx, "ada", "gogu", map[string]string{"GOG_ACCOUNT": yardAddr}, "ada")
	want := "source gogu: GOG_ACCOUNT is held by the yard already (on source gog): a bound input belongs to one holder; give a different GOG_ACCOUNT with yardr source key gogu --as ada, or have the yard give a different GOG_ACCOUNT with yardr source key gog and give this again"
	if err == nil || err.Error() != want {
		t.Fatalf("the yard's bound address given to ada through gogu =\n%v\nwant\n%s", err, want)
	}
	if u, err := y.GetUser(y.ctx, "ada"); err != nil || u.EnvFile != "" {
		t.Fatalf("a refused unbound giving set ada's file: %+v, %v", u, err)
	}
	// The yard's own value through the unbound source stands (except-self),
	// and ada's own through it stands; bia taking ada's through it is
	// refused as for the bound source, naming no source for a user hit.
	if _, err := y.KeySource(y.ctx, "gogu", map[string]string{"GOG_ACCOUNT": yardAddr}, "mckean"); err != nil {
		t.Fatalf("the yard's own through gogu: %v", err)
	}
	if _, err := y.KeyUserCredential(y.ctx, "ada", "gog", map[string]string{"GOG_ACCOUNT": adaAddr}, "ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.KeyUserCredential(y.ctx, "bia", "gogu", map[string]string{"GOG_ACCOUNT": adaAddr}, "bia"); err == nil ||
		!strings.Contains(err.Error(), "source gogu: GOG_ACCOUNT is held by ada already") || strings.Contains(err.Error(), adaAddr) {
		t.Fatalf("ada's address given to bia through gogu = %v", err)
	}
	if _, err := y.KeyUserCredential(y.ctx, "ada", "gogu", map[string]string{"GOG_ACCOUNT": adaAddr}, "ada"); err != nil {
		t.Fatalf("ada's own through gogu: %v", err)
	}
	// A name bound nowhere: one shared value for yard and users alike,
	// at give as at call (the call row is TestBoundCallUnboundTake's).
	if _, err := y.PutSource(y.ctx, "lin.yaml", sharedFile, "mckean"); err != nil {
		t.Fatal(err)
	}
	shared := map[string]string{"LINEAR_CLIENT_ID": "app-shared", "LINEAR_SECRET": "sec-shared"}
	if _, err := y.KeySource(y.ctx, "lin", shared, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.KeyUserCredential(y.ctx, "ada", "lin", shared, "ada"); err != nil {
		t.Fatalf("a shared value of a name bound nowhere, for ada: %v", err)
	}
	if _, err := y.KeyUserCredential(y.ctx, "bia", "lin", shared, "bia"); err != nil {
		t.Fatalf("a shared value of a name bound nowhere, for bia: %v", err)
	}
}

// A call made as a user whose file names the yard's bound value through
// a source that leaves the name unbound is refused before anything
// mints, as through the bound source; the holder's own call through it
// mints, and a value of a name bound nowhere mints for every holder.
func TestBoundCallUnboundTake(t *testing.T) {
	t.Parallel()
	tok := "tok-bound-minted"
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/me" || r.Header.Get("Authorization") != "Bearer "+tok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"id":"app"}`)
	}))
	t.Cleanup(srv.Close)

	y, _ := secretYard(t)
	unboundGog := strings.Replace(strings.Replace(boundGog(srv.URL), "name: gog", "name: gogu", 1), "      bound: true\n", "", 1)
	for file, text := range map[string]string{"gog.yaml": boundGog(srv.URL), "gogu.yaml": unboundGog} {
		if _, err := y.PutSource(y.ctx, file, text, "mckean"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := y.AddUser(y.ctx, "bia", "mckean"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	script := filepath.Join(dir, "mint")
	body := "#!/bin/sh\ncat >/dev/null\necho x >>" + runs + "\nprintf '{\"token\":\"" + tok + "\",\"expires_at\":\"2999-01-01T00:00:00Z\"}'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gog", "gogu"} {
		if _, err := y.SetSource(y.ctx, name, SourceMintCommand, script, "mckean"); err != nil {
			t.Fatal(err)
		}
	}
	const yardAddr, adaAddr = "yard@example.com", "ada@example.com"
	if _, err := y.KeySource(y.ctx, "gog", map[string]string{"GOG_ACCOUNT": yardAddr}, "mckean"); err != nil {
		t.Fatal(err)
	}
	adaKeyed, err := y.KeyUserCredential(y.ctx, "ada", "gog", map[string]string{"GOG_ACCOUNT": adaAddr}, "ada")
	if err != nil {
		t.Fatal(err)
	}
	// By hand, past every giving: ada's file names the yard's account,
	// and a call through the unbound source refuses naming the bound one.
	if _, _, err := WriteEnvFile(adaKeyed.File, map[string]string{"GOG_ACCOUNT": yardAddr}); err != nil {
		t.Fatal(err)
	}
	_, err = y.CallSource(y.ctx, SourceCall{Source: "gogu", Call: "ping", As: "ada", Actor: "mckean"})
	if err == nil || !strings.Contains(err.Error(), "GOG_ACCOUNT in "+adaKeyed.File+" names the yard's (on source gog) account, not ada's") ||
		!strings.Contains(err.Error(), "a bound input belongs to one holder") || strings.Contains(err.Error(), yardAddr) {
		t.Fatalf("a call as ada through gogu on the yard's account = %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("a refused unbound call reached the service %d times", hits.Load())
	}
	if _, err := os.Stat(runs); !os.IsNotExist(err) {
		t.Fatalf("a refused unbound call minted: %v", err)
	}
	// Ada's own value through the unbound source mints as it always has.
	if _, _, err := WriteEnvFile(adaKeyed.File, map[string]string{"GOG_ACCOUNT": adaAddr}); err != nil {
		t.Fatal(err)
	}
	res, err := y.CallSource(y.ctx, SourceCall{Source: "gogu", Call: "ping", As: "ada", Actor: "mckean"})
	if err != nil || string(res.Answer) != `{"id":"app"}` || hits.Load() != 1 {
		t.Fatalf("a call as ada through gogu on her own account = %+v, %v, after %d hits", res, err, hits.Load())
	}
	// A name bound nowhere, shared by yard and user: the call mints.
	linFile := strings.Replace(sharedFile, "https://lin.example.com", srv.URL, 1)
	if _, err := y.PutSource(y.ctx, "lin.yaml", linFile, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.SetSource(y.ctx, "lin", SourceMintCommand, script, "mckean"); err != nil {
		t.Fatal(err)
	}
	shared := map[string]string{"LINEAR_CLIENT_ID": "app-shared", "LINEAR_SECRET": "sec-shared"}
	if _, err := y.KeySource(y.ctx, "lin", shared, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.KeyUserCredential(y.ctx, "ada", "lin", shared, "ada"); err != nil {
		t.Fatal(err)
	}
	res, err = y.CallSource(y.ctx, SourceCall{Source: "lin", Call: "ping", As: "ada", Actor: "mckean"})
	if err != nil || string(res.Answer) != `{"id":"app"}` || hits.Load() != 2 {
		t.Fatalf("a call as ada on a shared name bound nowhere = %+v, %v, after %d hits", res, err, hits.Load())
	}
}

// A source whose declaration does not read while the yard's bound
// names are worked out refuses the giving and the call alike (fail
// closed): its bound names would be unseen, where the pasted sign-in's
// check skips such a source, which mints nothing.
func TestBoundUnparseableRefuses(t *testing.T) {
	t.Parallel()
	y := boundYard(t)
	const yardAddr, adaAddr = "yard@example.com", "ada@example.com"
	if _, err := y.KeySource(y.ctx, "gog", map[string]string{"GOG_ACCOUNT": yardAddr}, "mckean"); err != nil {
		t.Fatal(err)
	}
	adaKeyed, err := y.KeyUserCredential(y.ctx, "ada", "gog", map[string]string{"GOG_ACCOUNT": adaAddr}, "ada")
	if err != nil {
		t.Fatal(err)
	}
	_ = adaKeyed
	// A stored declaration no build here reads: put past PutSource,
	// which would have refused it, as an older yard's stands.
	if err := y.write(func(w *wtx) error {
		return put(w.Tx, bSources, "broken", Source{Name: "broken", Text: "name: broken\n"})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := y.KeyUserCredential(y.ctx, "bia", "gog", map[string]string{"GOG_ACCOUNT": "bia@example.com"}, "bia"); err == nil ||
		!strings.Contains(err.Error(), "broken") {
		t.Fatalf("a giving with an unreadable declaration in the yard = %v", err)
	}
	if u, err := y.GetUser(y.ctx, "bia"); err != nil || u.EnvFile != "" {
		t.Fatalf("a refused giving set bia's file: %+v, %v", u, err)
	}
	if _, err := y.KeySource(y.ctx, "gog", map[string]string{"GOG_ACCOUNT": "other@example.com"}, "mckean"); err == nil ||
		!strings.Contains(err.Error(), "broken") {
		t.Fatalf("a yard giving with an unreadable declaration in the yard = %v", err)
	}
	_, err = y.CallSource(y.ctx, SourceCall{Source: "gog", Call: "ping", As: "ada", Actor: "mckean"})
	if err == nil || !strings.Contains(err.Error(), "broken") {
		t.Fatalf("a call with an unreadable declaration in the yard = %v", err)
	}
	// The set itself: the bound name of a readable yard, and the refusal.
	if set, err := YardBoundNames([]Source{{Name: "gog", Text: boundFile}}); err != nil || !set["GOG_ACCOUNT"] || len(set) != 1 {
		t.Fatalf("YardBoundNames(gog) = %v, %v", set, err)
	}
	if _, err := YardBoundNames([]Source{{Name: "broken", Text: "name: broken\n"}}); err == nil {
		t.Fatalf("YardBoundNames(broken) = no error")
	}
}
