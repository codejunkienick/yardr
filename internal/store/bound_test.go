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
		yardAddr:             {Yard: true},
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
	// On a card, as typed: the yard's address for ada.
	seq := y.ask(t, userSecretAsk("bia", "bia"), "yardmaster")
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
}
