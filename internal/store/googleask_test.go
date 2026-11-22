package store

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The stand-in export a person pastes from their own machine, and the
// refresh token inside it: what a giving must never print.
const (
	googleExport  = "{\n  \"client_id\": \"123-fake.apps.googleusercontent.com\",\n  \"client_secret\": \"GOCSPX-fake-secret\",\n  \"refresh_token\": \"1//fake-refresh\",\n  \"type\": \"authorized_user\"\n}"
	googleRefresh = "1//fake-refresh"
)

func googleAsk(account, to string) Ask {
	return Ask{Question: "The Google sign-in?", To: to,
		Context: []string{"sign in on your machine, then paste the export"},
		Secret:  &AskSecret{Google: account}}
}

func googlePersonAsk(account string) Ask {
	a := googleAsk(account, "ada")
	a.Secret.As = "ada"
	return a
}

// A yard Google sign-in is asked of the operator, stored with the one
// name and the whole service set, and given into the yard's own
// credentials file at 0600; the pasted value is in no note.
func TestGoogleYardSignInByCard(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	y, _ := runYard(t)
	seq := y.ask(t, googleAsk("yard@example.com", "mckean"), "yardmaster")
	got := y.asked(t, seq)
	if got.Ask.Secret == nil || got.Ask.Secret.Google != "yard@example.com" ||
		!slices.Equal(got.Ask.Secret.Env, []string{googleCredentialsEnv}) ||
		!slices.Equal(got.Ask.Secret.Scopes, googleServices) {
		t.Fatalf("the ask stores %+v", got.Ask.Secret)
	}
	lines := strings.Join(got.Lines(), "\n")
	if !strings.Contains(lines, "the sign-in for the yard's Google account yard@example.com") {
		t.Fatalf("the card:\n%s", lines)
	}
	wantReachInCard(t, lines, got.Ask.Secret)

	keyed, err := y.GiveSecret(y.ctx, y.bead, seq,
		map[string]string{googleCredentialsEnv: googleExport}, "mckean", true)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(home, ".config", "gws", "credentials.json")
	if keyed.Google != "yard@example.com" || keyed.As != "" || keyed.File != file {
		t.Fatalf("keyed = %+v", keyed)
	}
	st, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("the credentials file is mode %04o, want 0600", st.Mode().Perm())
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "\n") || !strings.Contains(string(raw), googleRefresh) {
		t.Fatalf("the credentials file holds %q", raw)
	}
	for _, n := range mustNotes(t, y) {
		if strings.Contains(n.Text, googleRefresh) || strings.Contains(n.Text, "GOCSPX-fake-secret") {
			t.Fatalf("note %d holds the pasted value", n.Seq)
		}
	}
	e := lastAnswer(t, y)
	if line, _ := e.Data["answer"].(string); line != "mckean gave GOOGLE_CREDENTIALS, the sign-in for the yard's Google account yard@example.com (ask "+itoa(seq)+" on "+y.bead+")" {
		t.Fatalf("the sentence = %q", line)
	}
}

// Where the yard's one Google source words the sign-in, a paste card
// for its account speaks those words above its field, a person's form
// included; an unrelated source's sign-in neither leaks onto nor hides
// it, and where no Google source or several differing ones stand, the
// card reads as it did.
func TestGooglePasteCardSpeaksThePacksSignIn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	y, _ := runYard(t)
	plain := y.asked(t, y.ask(t, googleAsk("yard@example.com", "mckean"), "yardmaster"))
	if sec := plain.Ask.Secret; sec == nil || len(sec.LoginLines()) != 0 {
		t.Fatalf("a paste with no pack behind it shows a sign-in: %+v", sec)
	}
	const otherFile = `name: acme
base: https://acme.example.com
credential:
  env: ACME_TOKEN
  header: "Authorization: Bearer {credential}"
  mint: [ACME_ACCOUNT]
  login:
    run: [acme auth login, acme auth export]
    paste: paste the export into the acme card
calls:
  ping: { kind: read, request: { method: GET, path: /me } }
`
	if _, err := y.PutSource(y.ctx, "acme.yaml", otherFile, "mckean"); err != nil {
		t.Fatal(err)
	}
	withOther := y.asked(t, y.ask(t, googleAsk("yard@example.com", "mckean"), "yardmaster"))
	if sec := withOther.Ask.Secret; sec == nil || len(sec.LoginLines()) != 0 {
		t.Fatalf("a non-Google source's sign-in leaked onto a Google paste card: %+v", sec)
	}
	const mailFile = `name: google-mail
base: https://gmail.googleapis.com
credential:
  env: GOOGLE_TOKEN
  header: "Authorization: Bearer {credential}"
  mint: [GOOGLE_ACCOUNT]
  login:
    run: [gws auth login -s gmail, gws auth export --unmasked]
    paste: paste the whole output of gws auth export --unmasked into the Google sign-in card for this account
calls:
  search: { kind: read, request: { method: GET, path: /messages } }
`
	if _, err := y.PutSource(y.ctx, "google-mail.yaml", mailFile, "mckean"); err != nil {
		t.Fatal(err)
	}
	got := y.asked(t, y.ask(t, googleAsk("yard@example.com", "mckean"), "yardmaster"))
	lines := strings.Join(got.Lines(), "\n")
	for _, want := range []string{
		"the pack's sign-in, shown only and never run by the yard: run gws auth login -s gmail; then gws auth export --unmasked",
		"the pack's sign-in: paste the whole output of gws auth export --unmasked into the Google sign-in card for this account",
	} {
		if !strings.Contains(lines, want) {
			t.Errorf("the paste card lacks %q:\n%s", want, lines)
		}
	}
	person := y.asked(t, y.ask(t, googlePersonAsk("ada@example.com"), "yardmaster"))
	if sec := person.Ask.Secret; sec == nil || len(sec.LoginLines()) != 2 || !sec.GoogleSignInOf("ada") {
		t.Errorf("a person's paste card = %+v", sec)
	}
	const driveFile = `name: google-drive
base: https://www.googleapis.com
credential:
  env: GOOGLE_TOKEN
  header: "Authorization: Bearer {credential}"
  mint: [GOOGLE_ACCOUNT]
  login:
    run: [gws auth login -s drive, gws auth export --unmasked]
    paste: paste the whole output of gws auth export --unmasked into the Google sign-in card for this account
calls:
  files: { kind: read, request: { method: GET, path: /drive/v3/files } }
`
	if _, err := y.PutSource(y.ctx, "google-drive.yaml", driveFile, "mckean"); err != nil {
		t.Fatal(err)
	}
	both := y.asked(t, y.ask(t, googleAsk("yard@example.com", "mckean"), "yardmaster"))
	if sec := both.Ask.Secret; sec == nil || len(sec.LoginLines()) != 0 {
		t.Errorf("two differing Google packs still picked a sign-in: %+v", sec)
	}
}

// A person's Google sign-in is asked of them alone and given by them
// alone: the operator can neither be asked for it nor give it.
func TestGooglePersonSignInByCard(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	y, _ := runYard(t)
	person := googlePersonAsk("ada@example.com")
	person.To = "mckean"
	if _, err := y.AddAsk(y.ctx, y.bead, person, "yardmaster"); err == nil ||
		!strings.Contains(err.Error(), "theirs to give alone") {
		t.Fatalf("asking the operator for a person's sign-in = %v", err)
	}
	person.To = "ada"
	seq, err := y.AddAsk(y.ctx, y.bead, person, "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	card := y.asked(t, seq)
	if len(card.Ask.Weight) != 2 || card.Ask.Weight[1].Class != WeightAsUser ||
		card.Ask.Weight[1].Line != "lets calls or words count as a person's: a Google call made --as ada reads the sign-in for ada@example.com's own account" {
		t.Fatalf("the person's Google card weight = %+v", card.Ask.Weight)
	}
	if _, err := y.GiveSecret(y.ctx, y.bead, seq,
		map[string]string{googleCredentialsEnv: googleExport}, "mckean", true); err == nil ||
		!strings.Contains(err.Error(), "only ada may") {
		t.Fatalf("the operator giving a person's sign-in = %v", err)
	}
	keyed, err := y.GiveSecret(y.ctx, y.bead, seq,
		map[string]string{googleCredentialsEnv: googleExport}, "ada", true)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(home, ".config", "gws-accounts", "ada@example.com", "credentials.json")
	if keyed.Google != "ada@example.com" || keyed.As != "ada" || keyed.File != file {
		t.Fatalf("keyed = %+v", keyed)
	}
	if st, err := os.Stat(file); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("the credentials file: %v", err)
	}
	e := lastAnswer(t, y)
	if line, _ := e.Data["answer"].(string); line != "ada gave GOOGLE_CREDENTIALS, the sign-in for ada's Google account ada@example.com (ask "+itoa(seq)+" on "+y.bead+")" {
		t.Fatalf("the sentence = %q", line)
	}
}

// A paste that is no gws export is refused, naming the fix and never
// the value, and writes nothing; the ask stays open for another try.
func TestGoogleMalformedPasteRefused(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	y, _ := runYard(t)
	seq := y.ask(t, googleAsk("yard@example.com", "mckean"), "yardmaster")
	for _, bad := range []string{
		"not json at all",
		`{"type":"authorized_user","client_id":"x"}`,
		`{"type":"service_account","client_id":"x","client_secret":"y","refresh_token":"z"}`,
		`{"client_id":"x","client_secret":"y","refresh_token":"z"}`,
	} {
		_, err := y.GiveSecret(y.ctx, y.bead, seq,
			map[string]string{googleCredentialsEnv: bad}, "mckean", true)
		if err == nil || !strings.Contains(err.Error(), "gws auth export --unmasked") {
			t.Fatalf("paste %q = %v", bad, err)
		}
		if strings.Contains(err.Error(), "GOCSPX") || strings.Contains(err.Error(), bad) && bad != "not json at all" {
			t.Fatalf("the refusal repeats the paste: %v", err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "gws", "credentials.json")); !os.IsNotExist(err) {
		t.Fatalf("a refused paste wrote a file: %v", err)
	}
}

// A credentials file that breaks the rule it is read under refuses
// the giving as a failure of the yard's own file (OperationalError),
// as an env file's write marks its own, so a device that is no
// operator's is told no path of the yard's machine; nothing is
// written through the link, and the ask stays open.
func TestGoogleCredentialsFileErrorIsOperational(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	y, _ := runYard(t)
	seq := y.ask(t, googleAsk("yard@example.com", "mckean"), "yardmaster")
	dir := filepath.Join(home, ".config", "gws")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, "elsewhere.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "credentials.json")); err != nil {
		t.Fatal(err)
	}
	_, err := y.GiveSecret(y.ctx, y.bead, seq,
		map[string]string{googleCredentialsEnv: googleExport}, "mckean", true)
	var op *OperationalError
	if err == nil || !errors.As(err, &op) {
		t.Fatalf("giving through a symbolic link = %v, want an OperationalError", err)
	}
	if !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("the refusal = %v", err)
	}
	if raw, err := os.ReadFile(target); err != nil || string(raw) != "{}" {
		t.Fatalf("the link's file holds %q (%v)", raw, err)
	}
}

// Who may give a Google sign-in is held as an approval one user alone
// gives is (mayApproveSole): a crew member and a session still
// starting are refused for a person's own account, and the yard's
// own stays the operator's.
func TestGoogleGivingRefusesCrewAndStartingSessions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	y, _ := runYard(t)
	person := Object{Kind: KindUser, Name: "ada@example.com", To: "ada"}
	if err := y.May(y.ctx, "ada", ActGiveGoogleCredentials, person); err != nil {
		t.Fatalf("ada giving her own sign-in: %v", err)
	}
	if err := y.May(y.ctx, "yardmaster", ActGiveGoogleCredentials, person); err == nil ||
		!strings.Contains(err.Error(), "crew member") {
		t.Fatalf("a crew member giving ada's sign-in = %v", err)
	}
	starting := WithSession(y.ctx, "s-not-started")
	if err := y.May(starting, "ada", ActGiveGoogleCredentials, person); err == nil ||
		!strings.Contains(err.Error(), "is starting and not on record yet") {
		t.Fatalf("a starting session giving ada's sign-in = %v", err)
	}
	yard := Object{Kind: KindUser, Name: "yard@example.com"}
	if err := y.May(y.ctx, "mckean", ActGiveGoogleCredentials, yard); err != nil {
		t.Fatalf("the operator giving the yard's sign-in: %v", err)
	}
	if err := y.May(y.ctx, "ada", ActGiveGoogleCredentials, yard); err == nil {
		t.Fatal("ada giving the yard's sign-in was allowed")
	}
}

// An account that names no credentials file and a service gws has
// none of are refused at the ask, and nothing is stored.
func TestGoogleAskRefusals(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	y, _ := runYard(t)
	for _, tc := range []struct {
		ask  Ask
		want string
	}{
		{googleAsk("no-at-sign", "mckean"), "name the account's address"},
		{googleAsk("a/b@example.com", "mckean"), "name the account's address"},
		{googleAsk("a\nb@example.com", "mckean"), "name the account's address"},
		{googleAsk("   ", "mckean"), "name the account's address"},
		{googleAsk("yard@example.com", "ada"), "cannot be asked for one"},
	} {
		if _, err := y.AddAsk(y.ctx, y.bead, tc.ask, "yardmaster"); err == nil ||
			!strings.Contains(err.Error(), tc.want) {
			t.Errorf("ask %+v = %v, want %q", tc.ask.Secret, err, tc.want)
		}
	}
	bad := googleAsk("yard@example.com", "mckean")
	bad.Secret.Scopes = []string{"bogus"}
	if _, err := y.AddAsk(y.ctx, y.bead, bad, "yardmaster"); err == nil ||
		!strings.Contains(err.Error(), "gws has no service bogus") {
		t.Errorf("ask with a bogus service = %v", err)
	}
}

// A pasted sign-in is for an account its giver holds or nobody does: an
// account another holder holds as a bound input (bound.go) is refused,
// for a person and for the yard alike, naming the holder and never the
// paste, and that holder's credentials file is left as it stands. A
// fresh address lands.
func TestGoogleSignInRefusesAnAccountHeldElsewhere(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	y := boundYard(t)
	const yardAddr, biaAddr = "yard@example.com", "bia@example.com"
	if _, err := y.KeySource(y.ctx, "gog", map[string]string{"GOG_ACCOUNT": yardAddr}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.KeyUserCredential(y.ctx, "bia", "gog", map[string]string{"GOG_ACCOUNT": biaAddr}, "bia"); err != nil {
		t.Fatal(err)
	}
	biaFile := filepath.Join(home, ".config", "gws-accounts", biaAddr, "credentials.json")
	if err := os.MkdirAll(filepath.Dir(biaFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(biaFile, []byte("bia's own"), 0o600); err != nil {
		t.Fatal(err)
	}
	give := func(ask Ask, by string) error {
		t.Helper()
		seq := y.ask(t, ask, "yardmaster")
		_, err := y.GiveSecret(y.ctx, y.bead, seq, map[string]string{googleCredentialsEnv: googleExport}, by, true)
		return err
	}
	for _, c := range []struct {
		name string
		ask  Ask
		by   string
		want string
	}{
		{"ada for bia's account", googlePersonAsk(biaAddr), "ada", "held by bia already"},
		{"ada for the yard's account", googlePersonAsk(yardAddr), "ada", "held by the yard already"},
		{"the yard for bia's account", googleAsk(biaAddr, "mckean"), "mckean", "held by bia already"},
	} {
		err := give(c.ask, c.by)
		if err == nil || !strings.Contains(err.Error(), c.want) ||
			!strings.Contains(err.Error(), "belongs to one holder") ||
			strings.Contains(err.Error(), googleRefresh) || strings.Contains(err.Error(), "GOCSPX-fake-secret") {
			t.Errorf("%s = %v, want a refusal saying %q and no value", c.name, err, c.want)
		}
	}
	if raw, err := os.ReadFile(biaFile); err != nil || string(raw) != "bia's own" {
		t.Fatalf("bia's credentials file after the refusals: %q, %v", raw, err)
	}
	for _, f := range []string{
		filepath.Join(home, ".config", "gws", "credentials.json"),
		filepath.Join(home, ".config", "gws-accounts", yardAddr, "credentials.json"),
	} {
		if _, err := os.Stat(f); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("a refused paste wrote %s: %v", f, err)
		}
	}
	for _, n := range mustNotes(t, y) {
		if strings.Contains(n.Text, googleRefresh) {
			t.Fatalf("note %d holds the pasted value", n.Seq)
		}
	}
	if err := give(googlePersonAsk("ada@example.com"), "ada"); err != nil {
		t.Fatalf("ada's paste for an address nobody holds: %v", err)
	}
}
