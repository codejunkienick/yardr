package source

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// testReadEnv reads an env file as the tests' stand-in for the store's
// reader, which this package's tests cannot import (the store imports
// source): the entries of a plain KEY=VALUE file with its resolved path.
// It holds none of the real rule, which the store's tests pin; the tests'
// files are all plain lines, read whole.
func testReadEnv(path string) ([]string, string, error) {
	resolved, err := filepath.Abs(path)
	if err != nil {
		return nil, path, err
	}
	b, err := os.ReadFile(resolved)
	if err != nil {
		return nil, resolved, err
	}
	var env []string
	for line := range strings.Lines(string(b)) {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			env = append(env, line)
		}
	}
	return env, resolved, nil
}

// credFile is an env file with text, for the operator to have set.
func credFile(t *testing.T, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "crm.env")
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// A declared call reads the source's credential: the static value, or the
// token the mint's inputs mint. Nothing of either is shown.
func TestReadCredentialMint(t *testing.T) {
	t.Cleanup(SetMintClock(time.Now))
	d := mintDecl(t)
	ctx := context.Background()
	dir := t.TempDir()
	script := mintScript(t, dir, "2030-01-01T00:00:00Z")
	goodUntil := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC).Local().Format("15:04")

	mintOnly := credFile(t, "CRM_APP_ID=app-1\nCRM_APP_KEY=keys/app.pem\n")
	value, state, err := d.ReadCredential(ctx, mintOnly, []string{script}, Read, testReadEnv)
	if err != nil || value != "tok-for-run-1" {
		t.Fatalf("minted: %q %v", value, err)
	}
	if want := "CRM_APP_ID and CRM_APP_KEY are set in " + mintOnly + ", minted, good until " + goodUntil; state != want {
		t.Fatalf("minted state = %q, want %q", state, want)
	}
	// A mint covers every kind: an update is minted too.
	if value, _, err := d.ReadCredential(ctx, mintOnly, []string{script}, Update, testReadEnv); err != nil || value != "tok-for-run-1" {
		t.Fatalf("an update: %q %v", value, err)
	}
	// With both set the mint is what a call uses.
	both := credFile(t, "CRM_APP_ID=app-1\nCRM_APP_KEY=keys/app.pem\nCRM_TOKEN=tok-static\n")
	if value, _, err := d.ReadCredential(ctx, both, []string{script}, Read, testReadEnv); err != nil || value != "tok-for-run-1" {
		t.Fatalf("both set: %q %v", value, err)
	}
	// Else the static variable, used as it is set.
	one := credFile(t, "CRM_TOKEN=tok-static\n")
	if value, state, err := d.ReadCredential(ctx, one, []string{script}, Read, testReadEnv); err != nil || value != "tok-static" || state != "CRM_TOKEN is set in "+one {
		t.Fatalf("static: %q %q %v", value, state, err)
	}
	// One of the mint's names alone is refused, and not passed over.
	part := credFile(t, "CRM_APP_ID=app-1\nCRM_TOKEN=tok-static\n")
	if _, _, err := d.ReadCredential(ctx, part, []string{script}, Read, testReadEnv); err == nil || !strings.Contains(err.Error(), "CRM_APP_ID is set without CRM_APP_KEY") {
		t.Fatalf("one name alone: %v", err)
	}
	// Nothing set: the file and what would set it.
	empty := credFile(t, "OTHER=x\n")
	if _, _, err := d.ReadCredential(ctx, empty, []string{script}, Read, testReadEnv); err == nil || !strings.Contains(err.Error(), "env_file "+empty+" does not set CRM_APP_ID and CRM_APP_KEY, or CRM_TOKEN") {
		t.Fatalf("nothing set: %v", err)
	}
	// A mint with no command fails naming the setting that fixes it.
	var noCommand *NoMintCommand
	if _, _, err := d.ReadCredential(ctx, mintOnly, nil, Read, testReadEnv); !errors.As(err, &noCommand) || !strings.Contains(err.Error(), "yardr source set crm mint_command=<program> [args...]") {
		t.Fatalf("no mint_command: %v", err)
	}
	// No file: the command that writes it first, with every name, and an
	// agent's way on from a pane.
	if _, _, err := d.ReadCredential(ctx, "", []string{script}, Read, testReadEnv); err == nil ||
		!strings.Contains(err.Error(), "CRM_APP_ID and CRM_APP_KEY, or CRM_TOKEN is not set up: yardr source key crm gives CRM_APP_ID and CRM_APP_KEY, or CRM_TOKEN") ||
		!strings.Contains(err.Error(), "an agent asks from its pane: yardr bead ask <bead> - --secret crm, with the question and where to get it on stdin") {
		t.Fatalf("no env_file: %v", err)
	}
	// A file that names no credential reads nothing.
	bare, err := Parse("q.yaml", []byte("name: q\nbase: https://q.test\ncalls:\n  ping: { kind: read, request: { method: GET, path: /me } }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if value, state, err := bare.ReadCredential(ctx, "", nil, Read, testReadEnv); err != nil || value != "" || state != "none: the file names no credential" {
		t.Fatalf("no credential: %q %q %v", value, state, err)
	}
}

// A call made as a user reads the user's file with the same precedence
// and the same mint: the static value, or the token the mint's inputs
// mint. What is not set names the card that gives it. Nothing of either
// is shown.
func TestReadUserCredential(t *testing.T) {
	t.Cleanup(SetMintClock(time.Now))
	d := mintDecl(t)
	ctx := context.Background()
	dir := t.TempDir()
	script := mintScript(t, dir, "2030-01-01T00:00:00Z")
	goodUntil := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC).Local().Format("15:04")

	mintOnly := credFile(t, "CRM_APP_ID=app-1\nCRM_APP_KEY=keys/app.pem\n")
	value, state, err := d.ReadUserCredential(ctx, "ada", mintOnly, []string{script}, Read, testReadEnv)
	if err != nil || value != "tok-for-run-1" {
		t.Fatalf("minted: %q %v", value, err)
	}
	if want := "CRM_APP_ID and CRM_APP_KEY are set in " + mintOnly + ", minted, good until " + goodUntil; state != want {
		t.Fatalf("minted state = %q, want %q", state, want)
	}
	// The static variable, used as it is set; an update reads its own.
	one := credFile(t, "CRM_TOKEN=tok-static\n")
	if value, state, err := d.ReadUserCredential(ctx, "ada", one, []string{script}, Read, testReadEnv); err != nil || value != "tok-static" || state != "CRM_TOKEN is set in "+one {
		t.Fatalf("static: %q %q %v", value, state, err)
	}
	two, err := Parse("crm.yaml", []byte(keyed))
	if err != nil {
		t.Fatal(err)
	}
	file := credFile(t, "CRM_TOKEN=tok-read\nCRM_WRITE_TOKEN=tok-write\n")
	if value, _, err := two.ReadUserCredential(ctx, "ada", file, nil, Update, testReadEnv); err != nil || value != "tok-write" {
		t.Fatalf("an update: %q %v", value, err)
	}
	// Nothing set: the file, what it does not set, and the card that
	// gives it. No file at all: the names unprompted, and the card.
	empty := credFile(t, "OTHER=x\n")
	if _, _, err := d.ReadUserCredential(ctx, "ada", empty, []string{script}, Read, testReadEnv); err == nil ||
		!strings.Contains(err.Error(), "env_file "+empty+" does not set CRM_APP_ID and CRM_APP_KEY, or CRM_TOKEN") ||
		!strings.Contains(err.Error(), `yardr bead ask <bead> - --secret crm --as ada, with the question and where to get it on stdin, asks ada for it`) {
		t.Fatalf("nothing set: %v", err)
	}
	if _, _, err := d.ReadUserCredential(ctx, "ada", "", []string{script}, Read, testReadEnv); err == nil ||
		!strings.Contains(err.Error(), "CRM_APP_ID and CRM_APP_KEY is not set for ada: ") ||
		!strings.Contains(err.Error(), "--secret crm --as ada") {
		t.Fatalf("no file: %v", err)
	}
	// A file that names no credential reads nothing.
	bare, err := Parse("q.yaml", []byte("name: q\nbase: https://q.test\ncalls:\n  ping: { kind: read, request: { method: GET, path: /me } }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if value, state, err := bare.ReadUserCredential(ctx, "ada", "", nil, Read, testReadEnv); err != nil || value != "" || state != "none: the file names no credential" {
		t.Fatalf("no credential: %q %q %v", value, state, err)
	}

	// Held to the bound inputs, a value another holder holds refuses
	// before anything mints from it; ada's own and nobody's mint. The
	// refusal names whose it is and the giving that frees it, never the
	// value.
	bd, err := Parse("crm.yaml", []byte(strings.Replace(minted, "mint: [CRM_APP_ID, CRM_APP_KEY]",
		"mint:\n    - CRM_APP_ID\n    - { name: CRM_APP_KEY, secret: false, bound: true }", 1)))
	if err != nil {
		t.Fatal(err)
	}
	bdir := t.TempDir()
	bscript := mintScript(t, bdir, "2030-01-01T00:00:00Z")
	bfile := credFile(t, "CRM_APP_ID=app-1\nCRM_APP_KEY=keys/app.pem\n")
	holder := func(held Holder) BoundHolder {
		return func(name, value string, except Holder) (Holder, error) {
			if name != "CRM_APP_KEY" || value != "keys/app.pem" {
				t.Errorf("asked for %s, never the value in words", name)
			}
			if held == except {
				return Holder{}, nil
			}
			return held, nil
		}
	}
	boundKey := map[string]bool{"CRM_APP_KEY": true}
	if _, _, err := bd.ReadUserCredentialBound(ctx, "ada", bfile, []string{bscript}, Read, testReadEnv, holder(Holder{Yard: true}), boundKey); err == nil ||
		!strings.Contains(err.Error(), "CRM_APP_KEY in "+bfile+" names the yard's account, not ada's") ||
		!strings.Contains(err.Error(), "a bound input belongs to one holder; ada gives a different CRM_APP_KEY with yardr source key crm --as ada") ||
		strings.Contains(err.Error(), "keys/app.pem") {
		t.Fatalf("the yard's value as ada = %v", err)
	}
	// A user literally named "yard" does not collide with the yard itself.
	if _, _, err := bd.ReadUserCredentialBound(ctx, "yard", bfile, []string{bscript}, Read, testReadEnv, holder(Holder{Yard: true}), boundKey); err == nil ||
		!strings.Contains(err.Error(), "names the yard's account, not yard's") {
		t.Fatalf("the yard's value as user named yard = %v", err)
	}
	if _, _, err := bd.ReadUserCredentialBound(ctx, "ada", bfile, []string{bscript}, Read, testReadEnv, holder(Holder{User: "yard"}), boundKey); err == nil ||
		!strings.Contains(err.Error(), "names yard's account, not ada's") {
		t.Fatalf("user yard's value as ada = %v", err)
	}
	if _, _, err := bd.ReadUserCredentialBound(ctx, "ada", bfile, []string{bscript}, Read, testReadEnv, holder(Holder{User: "bia"}), boundKey); err == nil ||
		!strings.Contains(err.Error(), "names bia's account, not ada's") || strings.Contains(err.Error(), "keys/app.pem") {
		t.Fatalf("bia's value as ada = %v", err)
	}
	// A yard holding on another source is named in the whose-clause, with
	// no freeing giving pointed at that source; a holding on this source
	// reads as it always has.
	if _, _, err := bd.ReadUserCredentialBound(ctx, "ada", bfile, []string{bscript}, Read, testReadEnv, holder(Holder{Yard: true, Source: "gog"}), boundKey); err == nil ||
		!strings.Contains(err.Error(), "CRM_APP_KEY in "+bfile+" names the yard's (on source gog) account, not ada's") ||
		strings.Contains(err.Error(), "yardr source key gog") || strings.Contains(err.Error(), "keys/app.pem") {
		t.Fatalf("the yard's value on gog as ada = %v", err)
	}
	if _, _, err := bd.ReadUserCredentialBound(ctx, "ada", bfile, []string{bscript}, Read, testReadEnv, holder(Holder{Yard: true, Source: "crm"}), boundKey); err == nil ||
		!strings.Contains(err.Error(), "CRM_APP_KEY in "+bfile+" names the yard's account, not ada's") {
		t.Fatalf("the yard's value on crm as ada = %v", err)
	}
	// Widened at the yard: this source leaves CRM_APP_KEY unbound, and
	// the name is still held where the yard binds it elsewhere, with the
	// same whose-clause; bound on the target is not what holds it.
	ub, err := Parse("crm.yaml", []byte(minted))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ub.ReadUserCredentialBound(ctx, "ada", bfile, []string{bscript}, Read, testReadEnv, holder(Holder{Yard: true, Source: "gog"}), boundKey); err == nil ||
		!strings.Contains(err.Error(), "CRM_APP_KEY in "+bfile+" names the yard's (on source gog) account, not ada's") ||
		strings.Contains(err.Error(), "keys/app.pem") {
		t.Fatalf("an unbound take of a yard-bound name as ada = %v", err)
	}
	if n := mintRuns(t, bdir); n != 0 {
		t.Fatalf("a refused bound value minted %d times", n)
	}
	for _, held := range []Holder{{}, {User: "ada"}} {
		if value, _, err := bd.ReadUserCredentialBound(ctx, "ada", bfile, []string{bscript}, Read, testReadEnv, holder(held), boundKey); err != nil || value != "tok-for-run-1" {
			t.Fatalf("held by %+v: %q %v", held, value, err)
		}
	}
	// A name bound nowhere is never asked for, however widely its value
	// is shared: the holder is not consulted and the call mints.
	neverAsked := func(name, value string, except Holder) (Holder, error) {
		t.Errorf("asked for %s, bound nowhere", name)
		return Holder{Yard: true}, nil
	}
	if value, _, err := bd.ReadUserCredentialBound(ctx, "ada", bfile, []string{bscript}, Read, testReadEnv, neverAsked, map[string]bool{}); err != nil || value != "tok-for-run-1" {
		t.Fatalf("a value of a name bound nowhere: %q %v", value, err)
	}
	// A holder that cannot answer fails closed, and the unchecked read
	// and a static value read as they always have.
	broken := func(string, string, Holder) (Holder, error) {
		return Holder{}, errors.New("the holders cannot be read")
	}
	if _, _, err := bd.ReadUserCredentialBound(ctx, "ada", bfile, []string{bscript}, Read, testReadEnv, broken, boundKey); err == nil ||
		!strings.Contains(err.Error(), "the holders cannot be read") {
		t.Fatalf("an unreadable holder = %v", err)
	}
	if value, _, err := bd.ReadUserCredential(ctx, "ada", bfile, []string{bscript}, Read, testReadEnv); err != nil || value != "tok-for-run-1" {
		t.Fatalf("unchecked: %q %v", value, err)
	}
	oneBound := credFile(t, "CRM_TOKEN=tok-static\n")
	if value, _, err := bd.ReadUserCredentialBound(ctx, "ada", oneBound, []string{bscript}, Read, testReadEnv, holder(Holder{Yard: true}), boundKey); err != nil || value != "tok-static" {
		t.Fatalf("static beside a bound mint: %q %v", value, err)
	}
}

// CredentialState says in words where the names are set, the mint's and the
// command that mints them included, without running anything.
func TestCredentialStateMint(t *testing.T) {
	d := mintDecl(t)
	script := filepath.Join(t.TempDir(), "mint")
	mintOnly := credFile(t, "CRM_APP_ID=app-1\nCRM_APP_KEY=keys/app.pem\n")

	state, err := d.CredentialState(mintOnly, []string{script}, testReadEnv)
	if err != nil || state != "CRM_APP_ID and CRM_APP_KEY are set in "+mintOnly+", minted with "+script {
		t.Fatalf("minted: %q %v", state, err)
	}
	// Set up but for the command: the names, and what is missing.
	state, err = d.CredentialState(mintOnly, nil, testReadEnv)
	var noCommand *NoMintCommand
	if !errors.As(err, &noCommand) || state != "CRM_APP_ID and CRM_APP_KEY are set in "+mintOnly {
		t.Fatalf("no mint_command: %q %v", state, err)
	}
	// Static alone, and static with a second credential for updates.
	one := credFile(t, "CRM_TOKEN=tok-static\n")
	if state, err := d.CredentialState(one, nil, testReadEnv); err != nil || state != "CRM_TOKEN is set in "+one {
		t.Fatalf("static: %q %v", state, err)
	}
	two, err := Parse("crm.yaml", []byte(keyed))
	if err != nil {
		t.Fatal(err)
	}
	file := credFile(t, "CRM_TOKEN=tok-read\nCRM_WRITE_TOKEN=tok-write\n")
	if state, err := two.CredentialState(file, nil, testReadEnv); err != nil || state != "CRM_TOKEN is set in "+file+"; for an update, CRM_WRITE_TOKEN is set in "+file {
		t.Fatalf("two: %q %v", state, err)
	}
	if _, err := two.CredentialState(one, nil, testReadEnv); err == nil || !strings.Contains(err.Error(), "does not set CRM_WRITE_TOKEN") {
		t.Fatalf("without the update's: %v", err)
	}
}

// UserCredentialState says in words where the names a call as the user
// reads are set in the user's file, without the mint command, which is
// the source's; what is not set names the card that gives it.
func TestUserCredentialState(t *testing.T) {
	d := mintDecl(t)
	mintOnly := credFile(t, "CRM_APP_ID=app-1\nCRM_APP_KEY=keys/app.pem\n")

	state, err := d.UserCredentialState("ada", mintOnly, testReadEnv)
	if err != nil || state != "CRM_APP_ID and CRM_APP_KEY are set in "+mintOnly {
		t.Fatalf("minted: %q %v", state, err)
	}
	// Static alone, and static with a second credential for updates.
	one := credFile(t, "CRM_TOKEN=tok-static\n")
	if state, err := d.UserCredentialState("ada", one, testReadEnv); err != nil || state != "CRM_TOKEN is set in "+one {
		t.Fatalf("static: %q %v", state, err)
	}
	two, err := Parse("crm.yaml", []byte(keyed))
	if err != nil {
		t.Fatal(err)
	}
	file := credFile(t, "CRM_TOKEN=tok-read\nCRM_WRITE_TOKEN=tok-write\n")
	if state, err := two.UserCredentialState("ada", file, testReadEnv); err != nil || state != "CRM_TOKEN is set in "+file+"; for an update, CRM_WRITE_TOKEN is set in "+file {
		t.Fatalf("two: %q %v", state, err)
	}
	if _, err := two.UserCredentialState("ada", one, testReadEnv); err == nil ||
		!strings.Contains(err.Error(), "does not set CRM_WRITE_TOKEN") ||
		!strings.Contains(err.Error(), `yardr bead ask <bead> - --secret crm --as ada, with the question and where to get it on stdin, asks ada for it`) {
		t.Fatalf("without the update's: %v", err)
	}
	// A file without the names, and no file at all: the card that gives
	// them. The mint's inputs are what the card asks for unprompted.
	empty := credFile(t, "OTHER=x\n")
	if _, err := d.UserCredentialState("ada", empty, testReadEnv); err == nil ||
		!strings.Contains(err.Error(), "env_file "+empty+" does not set CRM_APP_ID and CRM_APP_KEY, or CRM_TOKEN") ||
		!strings.Contains(err.Error(), "--secret crm --as ada") {
		t.Fatalf("nothing set: %v", err)
	}
	if _, err := d.UserCredentialState("ada", "", testReadEnv); err == nil ||
		!strings.Contains(err.Error(), "CRM_APP_ID and CRM_APP_KEY is not set for ada: ") ||
		!strings.Contains(err.Error(), `yardr bead ask <bead> - --secret crm --as ada, with the question and where to get it on stdin, asks ada for it`) {
		t.Fatalf("no file: %v", err)
	}
	// A file that names no credential reads nothing.
	bare, err := Parse("q.yaml", []byte("name: q\nbase: https://q.test\ncalls:\n  ping: { kind: read, request: { method: GET, path: /me } }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if state, err := bare.UserCredentialState("ada", "", testReadEnv); err != nil || state != "none: the file names no credential" {
		t.Fatalf("no credential: %q %v", state, err)
	}
	// The names a user's credential goes by unprompted: the mint's
	// inputs where the credential is minted, else the static ones.
	if got := d.Credential.MintOrStatic(); !slices.Equal(got, []string{"CRM_APP_ID", "CRM_APP_KEY"}) {
		t.Fatalf("minted MintOrStatic = %q", got)
	}
	if got := two.Credential.MintOrStatic(); !slices.Equal(got, []string{"CRM_TOKEN", "CRM_WRITE_TOKEN"}) {
		t.Fatalf("static MintOrStatic = %q", got)
	}
}

// SelectCredential reads the file and applies the precedence, running
// nothing: the value, or the mint's inputs for Mint.
func TestSelectCredential(t *testing.T) {
	d := mintDecl(t)
	mintOnly := credFile(t, "CRM_APP_ID=app-1\nCRM_APP_KEY=keys/app.pem\n")
	sel, err := d.SelectCredential(mintOnly, Read, testReadEnv)
	if err != nil || sel.Value != "" || sel.Inputs["CRM_APP_ID"] != "app-1" || sel.File != mintOnly {
		t.Fatalf("minted: %+v %v", sel, err)
	}
	one := credFile(t, "CRM_TOKEN=tok-static\n")
	if sel, err := d.SelectCredential(one, Read, testReadEnv); err != nil || sel.Value != "tok-static" || sel.Inputs != nil {
		t.Fatalf("static: %+v %v", sel, err)
	}
	if _, err := d.SelectCredential("", Read, testReadEnv); err == nil || !strings.Contains(err.Error(), "is not set up: yardr source key crm gives") {
		t.Fatalf("no env_file: %v", err)
	}
	bare, err := Parse("q.yaml", []byte("name: q\nbase: https://q.test\ncalls:\n  ping: { kind: read, request: { method: GET, path: /me } }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if sel, err := bare.SelectCredential("", Read, testReadEnv); err != nil || sel.Value != "" || sel.Inputs != nil {
		t.Fatalf("no credential: %+v %v", sel, err)
	}
}
