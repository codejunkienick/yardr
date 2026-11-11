package store

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode"

	"yardr.dev/yardr/internal/source"
)

// hookLinear declares the hook of a Linear-like source: deliveries
// signed with a secret from a file, read into one envelope.
const hookLinear = `name: linear
base: https://api.linear.app
credential: { env: LINEAR_API_KEY }
hook:
  verify:
    scheme: hmac-sha256
    signed: body
    signature: { header: Linear-Signature, encoding: hex }
    delivery: { header: Linear-Delivery }
    timestamp: { body: webhookTimestamp, unit: ms, window: 1m }
  bodies: unique
  normalize: [linear/normalize]
  keys: [team, project, label]
calls:
  viewer: { kind: read, request: { method: GET, path: /viewer } }
`

const hookSecretValue = "whsec-test-signing-secret-0123456789"

func hookYard(t *testing.T) *askYard {
	t.Helper()
	y, _ := runYard(t)
	if _, err := y.PutSource(y.ctx, "linear.yaml", hookLinear, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.PutSource(y.ctx, "crm.yaml", runCRM, "mckean"); err != nil {
		t.Fatal(err)
	}
	return y
}

func hookSecretAsk(source, hook string) Ask {
	return Ask{Question: "The signing secret for it?", To: "mckean",
		Context: []string{"it is shown once where the webhook is made"},
		Secret:  &AskSecret{Source: source, Hook: hook}}
}

func hookRunAsk(command string) Ask {
	return Ask{Question: "Set up the webhook?", To: "mckean",
		Context: []string{"the signing secret is asked for on the ask before this one; give it first"},
		Run:     &AskRun{Argv: []string{command}}}
}

// The hook card names what the signature covers from the
// declaration: the body alone, or the template over timestamp and body
// together, and an unset signed reads as the body. The pack-apply card
// and the hook-add card print the same sentence.
func TestHookSaysNamesWhatTheSignatureCovers(t *testing.T) {
	t.Parallel()
	// The body alone covers a timestamp in the body; one in a header is
	// signed only by a template naming it.
	decl := func(signed, timestamp string) string {
		return `name: chat
base: https://chat.example.com
credential: { env: CHAT_TOKEN }
hook:
  verify:
    scheme: hmac-sha256
    signed: ` + signed + `
    signature: { header: Chat-Signature, encoding: hex, prefix: "v0=" }
    delivery: { body: event_id }
    timestamp: ` + timestamp + `
  bodies: repeat
  normalize: [chat/normalize]
  keys: [sender]
calls:
  viewer: { kind: read, request: { method: GET, path: /viewer } }
`
	}
	const inBody, inHeader = "{ body: sent_at, unit: s, window: 5m }", "{ header: Chat-Timestamp, unit: s, window: 5m }"
	for _, tc := range []struct {
		name      string
		signed    string
		timestamp string
		want      string
	}{
		{"the body alone", "body", inBody, "HMAC-SHA256 over the body (scheme hmac-sha256, signed: body)"},
		{"a template over timestamp and body", `"v0:{timestamp}:{body}"`, inHeader, "HMAC-SHA256 over v0:{timestamp}:{body} (scheme hmac-sha256, signed: v0:{timestamp}:{body})"},
	} {
		d, err := source.Parse("chat.yaml", []byte(decl(tc.signed, tc.timestamp)))
		if err != nil {
			t.Fatal(err)
		}
		if got := hookSays(d); !strings.Contains(got, tc.want) {
			t.Errorf("%s: the card says:\n%s\nwant %q", tc.name, got, tc.want)
		}
	}
	// Unset reads as the body alone: the reader refuses a missing
	// signed, so only a declaration built by hand arrives without one.
	d, err := source.Parse("chat.yaml", []byte(decl("body", inBody)))
	if err != nil {
		t.Fatal(err)
	}
	d.Hook.Verify.Signed = ""
	if got := hookSays(d); !strings.Contains(got, "HMAC-SHA256 over the body (scheme hmac-sha256, signed: body)") {
		t.Errorf("unset signed: the card says:\n%s", got)
	}
	// What an operator approves with a signature made by the delivery id
	// too, several at once, and a key the secret writes is said, and said
	// only when the declaration says it: the cards above carry none of it.
	swish, err := source.Parse("chat.yaml", []byte(`name: chat
hook:
  verify:
    scheme: hmac-sha256
    signed: "{delivery}.{timestamp}.{body}"
    signature: { header: Chat-Signature, encoding: base64, list: " ", prefix: "v1," }
    key: { prefix: "whsec_", encoding: base64 }
    delivery: { header: Chat-Id }
    timestamp: { header: Chat-Timestamp, unit: s, window: 5m }
  bodies: repeat
  normalize: [chat/normalize]
`))
	if err != nil {
		t.Fatal(err)
	}
	got := hookSays(swish)
	for _, want := range []string{
		"HMAC-SHA256 over {delivery}.{timestamp}.{body} (scheme hmac-sha256, signed: {delivery}.{timestamp}.{body})",
		`the signature in the header Chat-Signature as base64, one or more separated by " ", each past the prefix "v1,", any one of them passing`,
		`; the HMAC key is what follows the prefix "whsec_" in the secret, read as base64 (not the secret's own bytes); a delivery is named by the header Chat-Id`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the card says:\n%s\nwant %q", got, want)
		}
	}
	swish.Hook.Verify.Signature.Prefix = ""
	swish.Hook.Verify.Key.Prefix = ""
	got = hookSays(swish)
	for _, want := range []string{"each with no prefix, any one of them passing", "the HMAC key is the whole secret, read as base64"} {
		if !strings.Contains(got, want) {
			t.Errorf("with no prefix the card says:\n%s\nwant %q", got, want)
		}
	}
	d, err = source.Parse("chat.yaml", []byte(decl("body", inBody)))
	if err != nil {
		t.Fatal(err)
	}
	if got := hookSays(d); strings.Contains(got, "separated by") || strings.Contains(got, "HMAC key") {
		t.Errorf("a declaration with no list and no key: the card says:\n%s", got)
	}
	// The hash is the sentence's HMAC and, where the declaration picks another
	// than SHA-256, among the words it quotes of the declaration, which the
	// operator approves with them. A declaration that says sha256 outright
	// reads exactly as one that says nothing: they are one hash.
	said := hookSays(d)
	for _, tc := range []struct{ algorithm, want string }{
		{"sha1", "HMAC-SHA1 over the body (scheme hmac-sha256, algorithm: sha1, signed: body)"},
		{"sha512", "HMAC-SHA512 over the body (scheme hmac-sha256, algorithm: sha512, signed: body)"},
	} {
		other, err := source.Parse("chat.yaml", []byte(strings.Replace(decl("body", inBody), "    signed: body\n", "    signed: body\n    algorithm: "+tc.algorithm+"\n", 1)))
		if err != nil {
			t.Fatal(err)
		}
		if got := hookSays(other); !strings.Contains(got, tc.want) || strings.Contains(got, "SHA256") {
			t.Errorf("algorithm %s: the card says:\n%s\nwant %q and no SHA256", tc.algorithm, got, tc.want)
		}
	}
	explicit, err := source.Parse("chat.yaml", []byte(strings.Replace(decl("body", inBody), "    signed: body\n", "    signed: body\n    algorithm: sha256\n", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if got := hookSays(explicit); got != said || strings.Contains(got, "algorithm") {
		t.Errorf("algorithm sha256: the card says:\n%s\nwant the card of a declaration that says nothing:\n%s", got, said)
	}
	// A single prefix is a word of the declaration's like the list's and the
	// key's, which the card always quotes. This one is quoted where "=" or ","
	// is in it, and most prefixes end in "=".
	if got := hookSays(d); !strings.Contains(got, `as hex past the prefix "v0="`) {
		t.Errorf("a single prefix: the card says:\n%s", got)
	}
	// The reader takes the literal text around the placeholders, a prefix and
	// a dot path as written, so a declaration's author can put a newline and
	// an escape into each. The card quotes them, so the sentence itself holds
	// none of it and shows it spelled out.
	hostile, err := source.Parse("chat.yaml", []byte(`name: chat
hook:
  verify:
    scheme: hmac-sha256
    signed: "v0:{timestamp}:{body}\n\x1b[2K"
    signature: { header: Chat-Signature, encoding: hex, prefix: "v0=\n\x1b[2K" }
    delivery: { body: "id\n\x1b[2K" }
    timestamp: { body: "sent\n\x1b[2K", unit: s, window: 5m }
  bodies: repeat
  normalize: [chat/normalize]
`))
	if err != nil {
		t.Fatal(err)
	}
	got = hookSays(hostile)
	if i := strings.IndexFunc(got, unicode.IsControl); i >= 0 {
		t.Errorf("a hostile declaration: the card holds a control character at byte %d:\n%q", i, got)
	}
	for _, want := range []string{
		`HMAC-SHA256 over "v0:{timestamp}:{body}\n\x1b[2K" (scheme hmac-sha256, signed: "v0:{timestamp}:{body}\n\x1b[2K")`,
		`as hex past the prefix "v0=\n\x1b[2K"`,
		`a delivery is named by "id\n\x1b[2K" in its body`,
		`its time arrives at "sent\n\x1b[2K", in s within 5m`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("a hostile declaration: the card says:\n%q\nwant %s", got, want)
		}
	}
}

// A hook is set up by two asks on one bead, the secret first: the
// operator gives the signing secret into the file the yard owns for
// the hook, then approves the hook that names it. The value stands in
// that file alone: in no note, event or answer, and nowhere in the
// store file.
func TestHookAskSetup(t *testing.T) {
	t.Parallel()
	y := hookYard(t)
	secretSeq := y.ask(t, hookSecretAsk("linear", "linear"), "yardmaster")
	runSeq := y.ask(t, hookRunAsk("hook add linear --source linear"), "yardmaster")
	if secretSeq >= runSeq {
		t.Fatalf("the secret ask %d is not before the hook ask %d", secretSeq, runSeq)
	}

	secretAsk := y.asked(t, secretSeq)
	if secretAsk.Ask.Secret == nil || secretAsk.Ask.Secret.Hook != "linear" ||
		secretAsk.Ask.Secret.Source != "linear" ||
		!slices.Equal(secretAsk.Ask.Secret.Env, []string{"LINEAR_SIGNING_SECRET"}) {
		t.Fatalf("the secret ask stores %+v", secretAsk.Ask.Secret)
	}
	if lines := strings.Join(secretAsk.Lines(), "\n"); !strings.Contains(lines, "a secret: the signing secret for the hook linear (POST /hooks/linear), for the source linear (https://api.linear.app)") {
		t.Fatalf("the secret card:\n%s", lines)
	}
	runAsk := y.asked(t, runSeq)
	if runAsk.Ask.Run == nil || !slices.Equal(runAsk.Ask.Run.Argv, []string{"hook", "add", "linear", "--source", "linear"}) {
		t.Fatalf("the run ask stores %+v", runAsk.Ask.Run)
	}
	does := strings.Join(runAsk.Ask.Run.Does, "\n")
	for _, want := range []string{
		"it adds the hook linear: POST /hooks/linear takes deliveries from the source linear (https://api.linear.app).",
		"what it sends in: deliveries are verified by HMAC-SHA256 over the body (scheme hmac-sha256, signed: body), the signature in the header Linear-Signature as hex past the prefix none",
		"the signing secret is given on the hook's own secret card for linear and kept in " + HookSecretFile(y.home, "linear") + " (mode 0600); the hook names that file, and the value is in no note, event or answer.",
	} {
		if !strings.Contains(does, want) {
			t.Fatalf("the card says:\n%s\nwant %q", does, want)
		}
	}
	if runAsk.Ask.Run.Description != "Create a webhook endpoint POST /hooks/<name>" ||
		runAsk.Ask.Run.Back != "yardr hook rm linear" {
		t.Fatalf("the run ask = %+v", runAsk.Ask.Run)
	}

	// The hook runs only with its secret standing: approving first
	// refuses and makes nothing.
	if _, err := y.ApproveAsk(y.ctx, y.bead, runSeq, "mckean", true, nil); err == nil ||
		!strings.Contains(err.Error(), "give its signing secret on the hook's secret card for linear first") {
		t.Fatalf("approving before the giving = %v", err)
	}
	if _, err := y.GetHook(y.ctx, "linear"); err == nil {
		t.Fatal("the refused approval made the hook")
	}

	keyed, err := y.GiveSecret(y.ctx, y.bead, secretSeq,
		map[string]string{"LINEAR_SIGNING_SECRET": hookSecretValue}, "mckean", true)
	if err != nil {
		t.Fatal(err)
	}
	file := HookSecretFile(y.home, "linear")
	if keyed.Hook != "linear" || keyed.File != file || !slices.Equal(keyed.Given, []string{"LINEAR_SIGNING_SECRET"}) {
		t.Fatalf("keyed = %+v", keyed)
	}
	st, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("the secret file is mode %04o, want 0600", st.Mode().Perm())
	}
	if raw, err := os.ReadFile(file); err != nil || string(raw) != hookSecretValue {
		t.Fatalf("the secret file holds %q, %v", raw, err)
	}
	if lines := strings.Join(y.asked(t, secretSeq).Lines(), "\n"); !strings.Contains(lines, "mckean gave LINEAR_SIGNING_SECRET") {
		t.Fatalf("the given card:\n%s", lines)
	}

	done, err := y.ApproveAsk(y.ctx, y.bead, runSeq, "mckean", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if done.Result != "hook linear saved: POST /hooks/linear (linear)" {
		t.Fatalf("the approval answers %+v", done)
	}
	hook, err := y.GetHook(y.ctx, "linear")
	if err != nil {
		t.Fatal(err)
	}
	if hook.Source != "linear" || hook.SecretFile != file || hook.Blocked != "" {
		t.Fatalf("the hook = %+v", hook)
	}

	// The value is in no note, event or answer, and nowhere in the
	// store file: the file alone holds it.
	notes, err := y.Notes(y.ctx, y.bead)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range notes {
		if strings.Contains(n.Text, hookSecretValue) {
			t.Fatalf("note %d holds the secret", n.Seq)
		}
	}
	evs, err := y.EventsOfKind(y.ctx, "hook_added", 10)
	if err != nil || len(evs) != 1 {
		t.Fatalf("hook_added events = %+v, %v", evs, err)
	}
	y.approved(t, evs[0], runSeq)
	if raw, err := os.ReadFile(filepath.Join(y.home, "y.db")); err != nil {
		t.Fatal(err)
	} else if bytes.Contains(raw, []byte(hookSecretValue)) {
		t.Fatal("the store file holds the secret")
	}
}

// A private file left at the conventional path does not stand in for the
// secret ask on this bead, and a secret asked for another source cannot
// authorize this hook.
func TestHookAskNeedsItsAnsweredSecretCard(t *testing.T) {
	t.Parallel()
	t.Run("preexisting file", func(t *testing.T) {
		y := hookYard(t)
		file := HookSecretFile(y.home, "linear")
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(hookSecretValue), 0o600); err != nil {
			t.Fatal(err)
		}
		seq := y.ask(t, hookRunAsk("hook add linear --source linear"), "yardmaster")
		if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil ||
			!strings.Contains(err.Error(), "answered ask for source linear on this bead") {
			t.Fatalf("approval with only a preexisting file = %v", err)
		}
		if _, err := y.GetHook(y.ctx, "linear"); err == nil {
			t.Fatal("approval without its secret card made a hook")
		}
	})

	t.Run("wrong source", func(t *testing.T) {
		y := hookYard(t)
		if _, err := y.PutSource(y.ctx, "tracker.yaml", trackerDecl, "mckean"); err != nil {
			t.Fatal(err)
		}
		secretSeq := y.ask(t, hookSecretAsk("tracker", "linear"), "yardmaster")
		if _, err := y.GiveSecret(y.ctx, y.bead, secretSeq,
			map[string]string{"LINEAR_SIGNING_SECRET": hookSecretValue}, "mckean", true); err != nil {
			t.Fatal(err)
		}
		runSeq := y.ask(t, hookRunAsk("hook add linear --source linear"), "yardmaster")
		if _, err := y.ApproveAsk(y.ctx, y.bead, runSeq, "mckean", true, nil); err == nil ||
			!strings.Contains(err.Error(), "answered ask for source linear on this bead") {
			t.Fatalf("approval with a secret card for another source = %v", err)
		}
		if _, err := y.GetHook(y.ctx, "linear"); err == nil {
			t.Fatal("approval with a secret card for another source made a hook")
		}
	})

	t.Run("secret asked after hook", func(t *testing.T) {
		y := hookYard(t)
		runSeq := y.ask(t, hookRunAsk("hook add linear --source linear"), "yardmaster")
		secretSeq := y.ask(t, hookSecretAsk("linear", "linear"), "yardmaster")
		if _, err := y.GiveSecret(y.ctx, y.bead, secretSeq,
			map[string]string{"LINEAR_SIGNING_SECRET": hookSecretValue}, "mckean", true); err != nil {
			t.Fatal(err)
		}
		if _, err := y.ApproveAsk(y.ctx, y.bead, runSeq, "mckean", true, nil); err == nil ||
			!strings.Contains(err.Error(), "answered ask for source linear on this bead") {
			t.Fatalf("approval with a later secret ask = %v", err)
		}
	})
}

// Hook names may begin with a digit. Its input name still has to be a
// valid KEY for the non-terminal NAME=value path.
func TestHookAskDigitLeadingName(t *testing.T) {
	t.Parallel()
	y := hookYard(t)
	secretSeq := y.ask(t, hookSecretAsk("linear", "3jira"), "yardmaster")
	if got := strings.Join(y.asked(t, secretSeq).Ask.Secret.Env, ","); got != "_3JIRA_SIGNING_SECRET" {
		t.Fatalf("the secret input name is %q", got)
	}
	if _, err := y.GiveSecret(y.ctx, y.bead, secretSeq,
		map[string]string{"_3JIRA_SIGNING_SECRET": hookSecretValue}, "mckean", true); err != nil {
		t.Fatal(err)
	}
	runSeq := y.ask(t, hookRunAsk("hook add 3jira --source linear"), "yardmaster")
	if _, err := y.ApproveAsk(y.ctx, y.bead, runSeq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
}

// Hook secret writes refuse unsafe existing files without touching them or
// repeating the value in the refusal.
func TestWriteHookSecretRefusesUnsafeFiles(t *testing.T) {
	t.Parallel()
	const old = "old-hook-secret"
	for _, mode := range []struct {
		name string
		perm os.FileMode
		want string
	}{
		{name: "open to others", perm: 0o644, want: "is open to others (mode 0644); chmod 600 it"},
	} {
		t.Run(mode.name, func(t *testing.T) {
			home := t.TempDir()
			file := HookSecretFile(home, "linear")
			if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte(old), mode.perm); err != nil {
				t.Fatal(err)
			}
			_, renamed, err := writeHookSecret(home, "linear", hookSecretValue)
			if err == nil || renamed || !strings.Contains(err.Error(), mode.want) || strings.Contains(err.Error(), hookSecretValue) {
				t.Fatalf("writeHookSecret = renamed %v, err %v", renamed, err)
			}
			if got, err := os.ReadFile(file); err != nil || string(got) != old {
				t.Fatalf("the refused write changed the file: %q, %v", got, err)
			}
			if _, err := os.Lstat(filepath.Join(filepath.Dir(file), ".linear.secret.new")); !os.IsNotExist(err) {
				t.Fatalf("the refused write left a temporary file: %v", err)
			}
		})
	}

	t.Run("symbolic link", func(t *testing.T) {
		home := t.TempDir()
		file := HookSecretFile(home, "linear")
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(t.TempDir(), "target.secret")
		if err := os.WriteFile(target, []byte(old), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, file); err != nil {
			t.Fatal(err)
		}
		_, renamed, err := writeHookSecret(home, "linear", hookSecretValue)
		if err == nil || renamed || !strings.Contains(err.Error(), "is a symbolic link") || strings.Contains(err.Error(), hookSecretValue) {
			t.Fatalf("writeHookSecret = renamed %v, err %v", renamed, err)
		}
		if got, err := os.ReadFile(target); err != nil || string(got) != old {
			t.Fatalf("the symlink target changed: %q, %v", got, err)
		}
	})
}

// If recording fails after the private file is renamed, the ask stays open
// and the refusal identifies the file and name without repeating the value.
func TestGiveHookSecretNotRecorded(t *testing.T) {
	// Sequential: overrides afterKeyRename.
	y := hookYard(t)
	seq := y.ask(t, hookSecretAsk("linear", "linear"), "yardmaster")
	full := errors.New("no space left on device")
	afterKeyRename = func() error { return full }
	t.Cleanup(func() { afterKeyRename = nil })
	_, err := y.GiveSecret(y.ctx, y.bead, seq,
		map[string]string{"LINEAR_SIGNING_SECRET": hookSecretValue}, "mckean", true)
	var half *KeyNotRecorded
	if !errors.As(err, &half) || !errors.Is(err, full) || strings.Contains(err.Error(), hookSecretValue) {
		t.Fatalf("err = %v, want KeyNotRecorded with no secret", err)
	}
	file := HookSecretFile(y.home, "linear")
	if raw, err := os.ReadFile(file); err != nil || string(raw) != hookSecretValue {
		t.Fatalf("the file holds %q, %v", raw, err)
	}
	if r := y.resolved(t)[0]; r.State != AskOpen || len(r.Answers) != 0 {
		t.Fatalf("the ask is %s with %d answers, want open with none", r.State, len(r.Answers))
	}
	if n := len(answerNotes(t, y)); n != 0 {
		t.Fatalf("%d answer notes after a giving that was not recorded", n)
	}
}

// Each refusal of the two asks, by test: an undeclared source, a
// source whose declaration has no hook key, a hook the yard has, and
// the flags that stay in a terminal.
func TestHookAskRefusals(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		ask  Ask
		want string
	}{
		{"a secret for an undeclared source", hookSecretAsk("nope", "linear"), "source nope: not found"},
		{"a secret for a source with no hook key", hookSecretAsk("crm", "crmhook"), "its declaration has no hook key"},
		{"a secret with a bad hook name", hookSecretAsk("linear", "Linear"), `hook name "Linear"`},
		{"a run for an undeclared source", hookRunAsk("hook add linear --source nope"), "source nope: not found"},
		{"a run for a source with no hook key", hookRunAsk("hook add fresh --source crm"), "its declaration has no hook key"},
		{"a run with a shapeless --map", hookRunAsk("hook add linear --source linear --map action=open"), `--map "action=open": want <bead type>:<stage>=<state>`},
		{"a run with --as and no identity", hookRunAsk("hook add linear --source linear --as yardmaster"), "crew member yardmaster has no identity in linear"},
		{"a run with --says", hookRunAsk("hook add linear --source linear --says event=issues"), "hook add with --says is the operator's to type in a terminal"},
		{"a run with --makes", hookRunAsk("hook add linear --source linear --makes event=issues"), "hook add with --makes is the operator's to type in a terminal"},
		{"a run with --secret-file", hookRunAsk("hook add linear --source linear --secret-file /tmp/x"), "hook add with --secret-file is the operator's to type in a terminal"},
		{"a run with no source", hookRunAsk("hook add linear"), "--source once"},
	} {
		t.Run(c.name, func(t *testing.T) {
			y := hookYard(t)
			if _, err := y.AddAsk(y.ctx, y.bead, c.ask, "yardmaster"); err == nil ||
				!strings.Contains(err.Error(), c.want) {
				t.Fatalf("AddAsk = %v, want %q", err, c.want)
			}
			if notes, err := y.Notes(y.ctx, y.bead); err != nil || len(notes) != 0 {
				t.Fatalf("the refused ask left %d notes, %v", len(notes), err)
			}
		})
	}

	// A hook the yard has: the secret ask is not taken, but the run ask
	// is, as a replacement; the secret giving that races its making is
	// refused too.
	y := hookYard(t)
	if _, err := y.PutHook(y.ctx, Hook{Name: "linear", Source: "linear", SecretFile: filepath.Join(y.home, "elsewhere.secret")}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.AddAsk(y.ctx, y.bead, hookSecretAsk("linear", "linear"), "yardmaster"); err == nil ||
		!strings.Contains(err.Error(), "hook linear is in the yard already") {
		t.Fatalf("a secret ask for a hook the yard has = %v", err)
	}
	replaced := y.ask(t, hookRunAsk("hook add linear --source linear"), "yardmaster")
	if does := strings.Join(y.asked(t, replaced).Ask.Run.Does, "\n"); !strings.Contains(does, "it replaces the hook linear:") {
		t.Fatalf("a run ask for a hook the yard has says:\n%s", does)
	}

	y = hookYard(t)
	seq := y.ask(t, hookSecretAsk("linear", "linear"), "yardmaster")
	if _, err := y.PutHook(y.ctx, Hook{Name: "linear", Source: "linear", SecretFile: filepath.Join(y.home, "elsewhere.secret")}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.GiveSecret(y.ctx, y.bead, seq,
		map[string]string{"LINEAR_SIGNING_SECRET": hookSecretValue}, "mckean", true); err == nil ||
		!strings.Contains(err.Error(), "hook linear is in the yard already") {
		t.Fatalf("giving after the hook was made = %v", err)
	}
	if _, err := os.Lstat(HookSecretFile(y.home, "linear")); !os.IsNotExist(err) {
		t.Fatalf("the refused giving wrote the file: %v", err)
	}

	// Giving is the operator's alone and names its names exactly,
	// without saying back a value.
	y = hookYard(t)
	seq = y.ask(t, hookSecretAsk("linear", "linear"), "yardmaster")
	for _, c := range []struct {
		actor  string
		values map[string]string
		want   string
	}{
		{"yardmaster", map[string]string{"LINEAR_SIGNING_SECRET": hookSecretValue}, "only the operator (mckean) may give a hook's signing secret"},
		{"ada", map[string]string{"LINEAR_SIGNING_SECRET": hookSecretValue}, "only the operator (mckean) may give a hook's signing secret"},
		{"mckean", map[string]string{"OTHER": hookSecretValue}, "asks for LINEAR_SIGNING_SECRET, and was given a name it did not ask for"},
		{"mckean", map[string]string{"LINEAR_SIGNING_SECRET": ""}, "the signing secret is empty"},
	} {
		if _, err := y.GiveSecret(y.ctx, y.bead, seq, c.values, c.actor, true); err == nil ||
			!strings.Contains(err.Error(), c.want) || strings.Contains(err.Error(), hookSecretValue) {
			t.Errorf("giving as %s with %v = %v, want %q and no value", c.actor, c.values, err, c.want)
		}
	}
	if _, err := os.Lstat(HookSecretFile(y.home, "linear")); !os.IsNotExist(err) {
		t.Fatalf("refused givings wrote the file: %v", err)
	}
}

const trackerDecl = `name: tracker
base: https://tracker.example.com
credential: { env: TRACKER_TOKEN }
hook:
  verify: { scheme: secret, signature: { header: X-Tracker-Secret } }
  bodies: unique
  normalize: [tracker/normalize]
  keys: [project]
calls:
  ping: { kind: read, request: { method: GET, path: /me } }
`

// The row is for any declared source with a hook key: the card's words
// about the source come from its declaration (here the secret scheme
// and its header), and the same two asks set the hook up.
func TestHookAskGenericSource(t *testing.T) {
	t.Parallel()
	y, _ := runYard(t)
	if _, err := y.PutSource(y.ctx, "tracker.yaml", trackerDecl, "mckean"); err != nil {
		t.Fatal(err)
	}
	secretSeq := y.ask(t, hookSecretAsk("tracker", "tracker"), "yardmaster")
	runSeq := y.ask(t, hookRunAsk("hook add tracker --source tracker"), "yardmaster")
	does := strings.Join(y.asked(t, runSeq).Ask.Run.Does, "\n")
	if !strings.Contains(does, "deliveries are verified by the header X-Tracker-Secret holding the hook's secret itself (scheme secret)") {
		t.Fatalf("the card says:\n%s", does)
	}
	if _, err := y.GiveSecret(y.ctx, y.bead, secretSeq,
		map[string]string{"TRACKER_SIGNING_SECRET": hookSecretValue}, "mckean", true); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, runSeq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	hook, err := y.GetHook(y.ctx, "tracker")
	if err != nil || hook.Source != "tracker" {
		t.Fatalf("the hook = %+v, %v", hook, err)
	}
	bodyDecl := strings.Replace(trackerDecl, "name: tracker\n", "name: chat\n", 1)
	bodyDecl = strings.Replace(bodyDecl, "signature: { header: X-Tracker-Secret }", "signature: { body: data.token }", 1)
	if _, err := y.PutSource(y.ctx, "chat.yaml", bodyDecl, "mckean"); err != nil {
		t.Fatal(err)
	}
	bodySeq := y.ask(t, hookRunAsk("hook add chat --source chat"), "yardmaster")
	bodyDoes := strings.Join(y.asked(t, bodySeq).Ask.Run.Does, "\n")
	if !strings.Contains(bodyDoes, "deliveries are verified by the body field data.token holding the hook's secret itself (scheme secret)") {
		t.Fatalf("the body-secret card says:\n%s", bodyDoes)
	}
}

// A chmod of a command copy under the hook's source after the ask
// makes the approval stale: the card's source fingerprint holds the
// whole destination inventory (path, sha256, mode), as a source set or
// grant card's does.
func TestHookAskStaleOnChmod(t *testing.T) {
	t.Parallel()
	y, _ := runYard(t)
	dir := plantWired(t, y)
	seq := y.ask(t, hookRunAsk("hook add wired --source wired"), "yardmaster")
	if err := os.Chmod(filepath.Join(dir, "wired", "normalize"), 0o600); err != nil {
		t.Fatal(err)
	}
	y.refusedStale(t, seq, "was written from another declaration of the source wired: it was replaced since")
}

// A source the loose road added (a source add run ask) takes a hook
// the same way once it stands declared.
func TestHookAskAfterSourceAdd(t *testing.T) {
	t.Parallel()
	y, _ := runYard(t)
	add := sourceAddAsk(carriedOf(t, trackerDecl, map[string]string{"tracker/normalize": "#!/bin/sh\ncat >/dev/null\n"}))
	add.Run.Argv = []string{"source add tracker.yaml"}
	addSeq := y.ask(t, add, "yardmaster")
	if _, err := y.ApproveAsk(y.ctx, y.bead, addSeq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	secretSeq := y.ask(t, hookSecretAsk("tracker", "tracker"), "yardmaster")
	runSeq := y.ask(t, hookRunAsk("hook add tracker --source tracker"), "yardmaster")
	if _, err := y.GiveSecret(y.ctx, y.bead, secretSeq,
		map[string]string{"TRACKER_SIGNING_SECRET": hookSecretValue}, "mckean", true); err != nil {
		t.Fatal(err)
	}
	done, err := y.ApproveAsk(y.ctx, y.bead, runSeq, "mckean", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if done.Result != "hook tracker saved: POST /hooks/tracker (tracker)" {
		t.Fatalf("the approval answers %+v", done)
	}
}

// chatxDecl declares a source hearing by identity: fields.sender holds
// a member id, as slack's declaration does.
const chatxDecl = `name: chatx
base: https://chatx.example.com
credential: { env: CHATX_TOKEN }
hook:
  verify:
    scheme: hmac-sha256
    signed: body
    signature: { header: Chatx-Signature, encoding: hex }
    delivery: { header: Chatx-Delivery }
  bodies: unique
  normalize: [chatx/normalize]
  keys: [sender, channel]
hear:
  field: sender
  by: identity
calls:
  viewer: { kind: read, request: { method: GET, path: /viewer } }
`

func chatxYard(t *testing.T) *askYard {
	t.Helper()
	y, _ := runYard(t)
	if _, err := y.PutSource(y.ctx, "chatx.yaml", chatxDecl, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.PutSource(y.ctx, "linear.yaml", hookLinear, "mckean"); err != nil {
		t.Fatal(err)
	}
	return y
}

// giveChatxSecret gives the signing secret for hook name on y's bead.
func giveChatxSecret(t *testing.T, y *askYard, hook string) {
	t.Helper()
	seq := y.ask(t, hookSecretAsk("chatx", hook), "yardmaster")
	if _, err := y.GiveSecret(y.ctx, y.bead, seq,
		map[string]string{"CHATX_SIGNING_SECRET": hookSecretValue}, "mckean", true); err != nil {
		t.Fatal(err)
	}
}

// The hook add row takes --unknown for a source hearing by identity:
// the typed command parses, the card states the word, and approval
// stores it. Without the flag it stores unknown, repeated and
// out-of-list words and a word for a source with no identity hearing
// are refused at asking, --map, --as and --secret-file stay refused,
// and a card whose word was changed meanwhile is stale.
func TestHookAskUnknown(t *testing.T) {
	t.Parallel()
	// Typed, stated, and stored, for the word given.
	y := chatxYard(t)
	giveChatxSecret(t, y, "chatx")
	runSeq := y.ask(t, hookRunAsk("hook add chatx --source chatx --unknown unknown"), "yardmaster")
	runAsk := y.asked(t, runSeq)
	if runAsk.Ask.Run == nil || !slices.Equal(runAsk.Ask.Run.Argv,
		[]string{"hook", "add", "chatx", "--source", "chatx", "--unknown", "unknown"}) {
		t.Fatalf("the run ask stores %+v", runAsk.Ask.Run)
	}
	if does := strings.Join(runAsk.Ask.Run.Does, "\n"); !strings.Contains(does, "it stores --unknown unknown for an unmapped sender") {
		t.Fatalf("the card says:\n%s\nwant the --unknown unknown sentence", does)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, runSeq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	if hook, err := y.GetHook(y.ctx, "chatx"); err != nil || hook.Unknown != HookUnknownUnknown {
		t.Fatalf("the hook = %+v, %v, want unknown %q", hook, err, HookUnknownUnknown)
	}

	// Each word stores as given, and without the flag stores unknown.
	for _, c := range []struct{ command, stored string }{
		{"hook add chatx --source chatx --unknown allow", HookUnknownAllow},
		{"hook add chatx --source chatx --unknown ignore", HookUnknownIgnore},
		{"hook add chatx --source chatx", HookUnknownUnknown},
	} {
		y := chatxYard(t)
		giveChatxSecret(t, y, "chatx")
		runSeq := y.ask(t, hookRunAsk(c.command), "yardmaster")
		if does := strings.Join(y.asked(t, runSeq).Ask.Run.Does, "\n"); !strings.Contains(does, "it stores --unknown "+c.stored) {
			t.Fatalf("%s: the card says:\n%s\nwant --unknown %s", c.command, does, c.stored)
		}
		if _, err := y.ApproveAsk(y.ctx, y.bead, runSeq, "mckean", true, nil); err != nil {
			t.Fatalf("%s: %v", c.command, err)
		}
		if hook, err := y.GetHook(y.ctx, "chatx"); err != nil || hook.Unknown != c.stored {
			t.Fatalf("%s: the hook = %+v, %v, want %q", c.command, hook, err, c.stored)
		}
	}

	// Refused at asking, each leaving no note.
	for _, c := range []struct{ name, command, want string }{
		{"repeated --unknown", "hook add chatx --source chatx --unknown allow --unknown ignore", "--unknown once"},
		{"a word outside the three", "hook add chatx --source chatx --unknown shrug", `--unknown "shrug": want unknown, allow, ignore`},
		{"a word for no identity hearing", "hook add linear --source linear --unknown allow", "for a source with no identity hearing"},
		{"--map with --unknown", "hook add chatx --source chatx --unknown allow --map action=open", `--map "action=open": want <bead type>:<stage>=<state>`},
		{"--as with --unknown", "hook add chatx --source chatx --unknown allow --as yardmaster", "crew member yardmaster has no identity in chatx"},
		{"--secret-file with --unknown", "hook add chatx --source chatx --unknown allow --secret-file /tmp/x", "hook add with --secret-file is the operator's to type in a terminal"},
	} {
		t.Run(c.name, func(t *testing.T) {
			y := chatxYard(t)
			if _, err := y.AddAsk(y.ctx, y.bead, hookRunAsk(c.command), "yardmaster"); err == nil ||
				!strings.Contains(err.Error(), c.want) {
				t.Fatalf("AddAsk %q = %v, want %q", c.command, err, c.want)
			}
			if notes, err := y.Notes(y.ctx, y.bead); err != nil || len(notes) != 0 {
				t.Fatalf("the refused ask left %d notes, %v", len(notes), err)
			}
		})
	}

	// A card whose word was changed meanwhile is stale and runs
	// nothing: the hook made in a terminal with another word stands.
	y = chatxYard(t)
	giveChatxSecret(t, y, "chatx")
	runSeq = y.ask(t, hookRunAsk("hook add chatx --source chatx --unknown allow"), "yardmaster")
	if _, err := y.PutHook(y.ctx, Hook{Name: "chatx", Source: "chatx",
		SecretFile: filepath.Join(y.home, "elsewhere.secret"), Unknown: HookUnknownIgnore}, "mckean"); err != nil {
		t.Fatal(err)
	}
	y.refusedStale(t, runSeq, "its word was changed meanwhile")
	if hook, err := y.GetHook(y.ctx, "chatx"); err != nil || hook.Unknown != HookUnknownIgnore {
		t.Fatalf("the hook after a stale approval = %+v, %v, want ignore kept", hook, err)
	}
}

// A source that registers its hook itself takes another approval path:
// it must still store the card's --unknown word when that path makes the
// hook before calling the source.
func TestHookAskUnknownRegisteredHook(t *testing.T) {
	t.Parallel()
	var hits []registerHit
	status := http.StatusOK
	y := registerYard(t, &hits, &status)
	src, err := y.GetSource(y.ctx, "bot")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(src.Text, "calls:\n", "hear:\n  field: chat\n  by: identity\ncalls:\n", 1)
	if text == src.Text {
		t.Fatal("test source has no calls key before which to add identity hearing")
	}
	if _, err := y.PutSource(y.ctx, "bot.yaml", text, "mckean"); err != nil {
		t.Fatal(err)
	}
	seq := y.ask(t, hookRunAsk("hook add bot --source bot --unknown allow"), "yardmaster")
	if does := strings.Join(y.asked(t, seq).Ask.Run.Does, "\n"); !strings.Contains(does, "it stores --unknown allow for an unmapped sender") {
		t.Fatalf("the register card says:\n%s\nwant its unknown policy", does)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	hook, err := y.GetHook(y.ctx, "bot")
	if err != nil || hook.UnknownPolicy() != HookUnknownAllow {
		t.Fatalf("registered hook = %+v, %v; want unknown allow", hook, err)
	}
}

// hookTableFlow is the flow the hook table tests' depot follows: beads
// report done, failed and changes, and task beads are made in new and review.
const hookTableFlow = `
new -> review on done | new on failed
review -> merged on done | new on changes
merged terminal
`

// hookTableYard is a hook yard whose depot web follows hookTableFlow, with
// crew member yardmaster holding an identity in linear.
func hookTableYard(t *testing.T) *askYard {
	t.Helper()
	y := hookYard(t)
	if err := y.PutDepot(y.ctx, Depot{Name: "web", Base: "origin/main"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	f, err := ParseFlow(hookTableFlow)
	if err != nil {
		t.Fatal(err)
	}
	f.Name = "web"
	if _, err := y.PutFlow(y.ctx, *f, "mckean"); err != nil {
		t.Fatal(err)
	}
	if err := y.SetFlowPointer(y.ctx, "web", "", "web", "mckean"); err != nil {
		t.Fatal(err)
	}
	if err := y.SetIdentity(y.ctx, "yardmaster", "linear", Identity{ID: "u-yardmaster"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	return y
}

// hookTableAsk is a hook add run ask carrying says and makes lines beside argv.
func hookTableAsk(command string, says, makes []string) Ask {
	ask := hookRunAsk(command)
	if len(says) > 0 || len(makes) > 0 {
		ask.Run.HookTable = &AskHookTable{Says: says, Makes: makes}
	}
	return ask
}

// hookTableSecret writes the signing secret file a replacement stands with.
func hookTableSecret(t *testing.T, file string) {
	t.Helper()
	if err := os.WriteFile(file, []byte(hookSecretValue), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A run ask replaces the hook that stands with the table it carries: the
// card shows each table now and after as a diff, whole, with --as and the
// secret file, and the approval stands the hook as the card showed, keeping
// the secret file. The card is weighed trust, and approved only weighed.
func TestHookTableReplacement(t *testing.T) {
	t.Parallel()
	y := hookTableYard(t)
	file := filepath.Join(t.TempDir(), "linear.secret")
	hookTableSecret(t, file)
	standing := Hook{Name: "linear", Source: "linear", SecretFile: file, As: "yardmaster",
		Map: map[string]string{"task:new": "started"},
		Says: []Say{
			{Where: map[string]string{"action": "opened", "event": "issues"}, Outcome: "done"},
			{Where: map[string]string{"action": "closed", "event": "issues"}, Outcome: "changes", Note: "Closed out of turn"},
		},
		Makes: []Make{{Where: map[string]string{"action": "opened", "event": "issues"}, Type: "task", Stage: "new"}},
	}
	if _, err := y.PutHook(y.ctx, standing, "mckean"); err != nil {
		t.Fatal(err)
	}
	seq := y.ask(t, hookTableAsk("hook add linear --source linear --as yardmaster --map task:new=started --map task:review=review",
		[]string{
			"action=opened event=issues -> done",
			"action=closed event=issues -> failed: CI failed it",
			"action=added event=team -> changes",
		}, nil), "yardmaster")
	got := y.asked(t, seq)
	run := got.Ask.Run
	does := strings.Join(run.Does, "\n")
	for _, want := range []string{
		"it replaces the hook linear: POST /hooks/linear takes deliveries from the source linear (https://api.linear.app).",
		"the signing secret stays in " + file + " (mode 0600), the file the hook stands with",
		"it acts as the crew member yardmaster, as now.",
		"its says table changes from 2 lines to 3 lines (2 insertions, 1 deletion):",
		"--- was",
		"+++ is",
		"-action=closed event=issues -> changes: Closed out of turn",
		"+action=closed event=issues -> failed: CI failed it",
		"+action=added event=team -> changes",
		"its makes table changes from 1 line to 0 lines (0 insertions, 1 deletion):",
		"-action=opened event=issues -> task:new",
		"its map changes from 1 line to 2 lines (1 insertion, 0 deletions):",
		"+task:review=review",
	} {
		if !strings.Contains(does, want) {
			t.Errorf("the card says:\n%s\nwant %q", does, want)
		}
	}
	if want := "yardr bead ask <bead> - with Q: <question> and R: yardr hook add linear --source linear --as yardmaster --map task:new=started and S: and M: lines of the table that stands now (yardr hook show linear shows it) on stdin"; run.Back != want || run.BackBy != "only the operator" {
		t.Errorf("the card is taken back by %q, %q; want %q", run.Back, run.BackBy, want)
	}
	if len(got.Ask.Weight) != 1 || got.Ask.Weight[0].Class != WeightTrust || !StrongWeight(got.Ask.Weight) {
		t.Fatalf("the ask weighs %+v, want trust, which is strong", got.Ask.Weight)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil); err == nil || !strings.Contains(err.Error(), "weighty (trust)") {
		t.Fatalf("approving without the weight seen = %v", err)
	}
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if done.Result != "hook linear saved: POST /hooks/linear (linear)" {
		t.Fatalf("the approval recorded %q", done.Result)
	}
	hook, err := y.GetHook(y.ctx, "linear")
	if err != nil {
		t.Fatal(err)
	}
	if hook.Source != "linear" || hook.SecretFile != file || hook.As != "yardmaster" {
		t.Fatalf("the hook stands as %+v", hook)
	}
	if !SameSays(hook.Says, []Say{
		{Where: map[string]string{"action": "opened", "event": "issues"}, Outcome: "done"},
		{Where: map[string]string{"action": "closed", "event": "issues"}, Outcome: "failed", Note: "CI failed it"},
		{Where: map[string]string{"action": "added", "event": "team"}, Outcome: "changes"},
	}) {
		t.Fatalf("its says stand as %q", SayLines(hook.Says))
	}
	if len(hook.Makes) != 0 {
		t.Fatalf("its makes stand as %q", MakeLines(hook.Makes))
	}
	if len(hook.Map) != 2 || hook.Map["task:new"] != "started" || hook.Map["task:review"] != "review" {
		t.Fatalf("its map stands as %v", hook.Map)
	}
	if raw, err := os.ReadFile(file); err != nil || string(raw) != hookSecretValue {
		t.Fatalf("the secret file holds %q, %v", raw, err)
	}
}

// Order counts on a hook's tables: moving a line shows as removed and
// added, and the approval stands the new order.
func TestHookTableOrderDiff(t *testing.T) {
	t.Parallel()
	y := hookTableYard(t)
	file := filepath.Join(t.TempDir(), "linear.secret")
	hookTableSecret(t, file)
	if _, err := y.PutHook(y.ctx, Hook{Name: "linear", Source: "linear", SecretFile: file,
		Says: []Say{
			{Where: map[string]string{"action": "opened", "event": "issues"}, Outcome: "done"},
			{Where: map[string]string{"action": "closed", "event": "issues"}, Outcome: "changes"},
		},
	}, "mckean"); err != nil {
		t.Fatal(err)
	}
	seq := y.ask(t, hookTableAsk("hook add linear --source linear",
		[]string{
			"action=closed event=issues -> changes",
			"action=opened event=issues -> done",
		}, nil), "yardmaster")
	does := strings.Join(y.asked(t, seq).Ask.Run.Does, "\n")
	for _, want := range []string{
		"its says table changes from 2 lines to 2 lines (1 insertion, 1 deletion):",
		"-action=opened event=issues -> done",
		" action=closed event=issues -> changes",
		"+action=opened event=issues -> done",
	} {
		if !strings.Contains(does, want) {
			t.Fatalf("the card says:\n%s\nwant %q", does, want)
		}
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	hook, err := y.GetHook(y.ctx, "linear")
	if err != nil {
		t.Fatal(err)
	}
	if !SameSays(hook.Says, []Say{
		{Where: map[string]string{"action": "closed", "event": "issues"}, Outcome: "changes"},
		{Where: map[string]string{"action": "opened", "event": "issues"}, Outcome: "done"},
	}) {
		t.Fatalf("its says stand as %q", SayLines(hook.Says))
	}
}

// A hook add carrying a table is refused as the terminal refuses it: a
// line no bead could take the outcome of or be made in, a map without
// the crew member it acts as, a crew member the yard has not or without
// an identity in the source, a line of no shape, and a table past the
// card's byte and line caps. Each leaves no note.
func TestHookTableRefusals(t *testing.T) {
	t.Parallel()
	long := make([]string, 35)
	for i := range long {
		long[i] = fmt.Sprintf("action=opened event=issues label=l%d -> done", i)
	}
	for _, c := range []struct {
		name    string
		bare    bool
		command string
		says    []string
		makes   []string
		want    string
	}{
		{"an outcome no flow edge has", false, "hook add linear --source linear",
			[]string{"action=closed event=issues -> shipped"}, nil, "no stage has an outcome shipped"},
		{"an outcome the stage has not", false, "hook add linear --source linear",
			[]string{"action=closed event=issues stage=new -> changes"}, nil, "stage new has no outcome changes"},
		{"an outcome no bead of the type could take", false, "hook add linear --source linear",
			[]string{"action=closed event=issues type=task -> shipped"}, nil, "no bead of type task of the yard's depots could take it"},
		{"a stage no bead can be made in", false, "hook add linear --source linear",
			nil, []string{"action=opened event=issues -> task:merged"}, "no stage merged a bead can be made in"},
		{"a depot the yard has not", false, "hook add linear --source linear",
			nil, []string{"action=opened event=issues -> task:new@gone"}, "depot gone: not found"},
		{"a map without its crew member", false, "hook add linear --source linear --map task:new=started",
			nil, nil, "a map needs the crew member it acts as"},
		{"a crew member the yard has not", false, "hook add linear --source linear --as ghost",
			nil, nil, "crew member ghost: not found"},
		{"a says line of no shape", false, "hook add linear --source linear",
			[]string{"nonsense"}, nil, "--says"},
		{"a makes line of no shape", false, "hook add linear --source linear",
			nil, []string{"nonsense"}, "--makes"},
		{"a map line twice with two states", false, "hook add linear --source linear --as yardmaster --map task:new=started --map task:new=review",
			nil, nil, "is mapped to"},
		{"a table past the byte bound", false, "hook add linear --source linear",
			[]string{strings.Repeat("a", MaxRunHookTable+1)}, nil, "a card holds at most 8192"},
		{"a table past the line cap", false, "hook add linear --source linear",
			long, nil, "a card is read whole"},
		{"a makes line with no depot to make in", true, "hook add linear --source linear",
			nil, []string{"action=opened event=issues -> task:new"}, "the yard has no depot to make a bead in"},
		{"a says line with no depot to take it", true, "hook add linear --source linear",
			[]string{"action=closed event=issues -> done"}, nil, "no bead of the yard's depots could take it"},
	} {
		t.Run(c.name, func(t *testing.T) {
			y := hookTableYard(t)
			if c.bare {
				y = hookYard(t)
			}
			if _, err := y.AddAsk(y.ctx, y.bead, hookTableAsk(c.command, c.says, c.makes), "yardmaster"); err == nil ||
				!strings.Contains(err.Error(), c.want) {
				t.Fatalf("AddAsk = %v, want %q", err, c.want)
			}
			if notes, err := y.Notes(y.ctx, y.bead); err != nil || len(notes) != 0 {
				t.Fatalf("the refused ask left %d notes, %v", len(notes), err)
			}
		})
	}
}

// A new hook carrying a table is set up as the safe shape is, with its
// secret given first: the card shows the table against nothing, and the
// approval makes the hook with it.
func TestHookTableNewWithTable(t *testing.T) {
	t.Parallel()
	y := hookTableYard(t)
	secretSeq := y.ask(t, hookSecretAsk("linear", "atlas"), "yardmaster")
	if _, err := y.GiveSecret(y.ctx, y.bead, secretSeq,
		map[string]string{"ATLAS_SIGNING_SECRET": hookSecretValue}, "mckean", true); err != nil {
		t.Fatal(err)
	}
	seq := y.ask(t, hookTableAsk("hook add atlas --source linear --as yardmaster --map task:new=started",
		[]string{"action=opened event=issues -> done"},
		[]string{"action=opened event=issues -> task:new"}), "yardmaster")
	does := strings.Join(y.asked(t, seq).Ask.Run.Does, "\n")
	for _, want := range []string{
		"it adds the hook atlas: POST /hooks/atlas takes deliveries from the source linear (https://api.linear.app).",
		"it acts as the crew member yardmaster (now it acts as none).",
		"its says table changes from 0 lines to 1 line (1 insertion, 0 deletions):",
		"+action=opened event=issues -> done",
		"its makes table changes from 0 lines to 1 line (1 insertion, 0 deletions):",
		"+action=opened event=issues -> task:new",
		"its map changes from 0 lines to 1 line (1 insertion, 0 deletions):",
		"+task:new=started",
	} {
		if !strings.Contains(does, want) {
			t.Fatalf("the card says:\n%s\nwant %q", does, want)
		}
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	hook, err := y.GetHook(y.ctx, "atlas")
	if err != nil {
		t.Fatal(err)
	}
	if hook.Source != "linear" || hook.SecretFile != HookSecretFile(y.home, "atlas") || hook.As != "yardmaster" {
		t.Fatalf("the hook stands as %+v", hook)
	}
	if !SameSays(hook.Says, []Say{{Where: map[string]string{"action": "opened", "event": "issues"}, Outcome: "done"}}) {
		t.Fatalf("its says stand as %q", SayLines(hook.Says))
	}
	if !SameMakes(hook.Makes, []Make{{Where: map[string]string{"action": "opened", "event": "issues"}, Type: "task", Stage: "new"}}) {
		t.Fatalf("its makes stand as %q", MakeLines(hook.Makes))
	}
	if len(hook.Map) != 1 || hook.Map["task:new"] != "started" {
		t.Fatalf("its map stands as %v", hook.Map)
	}
}

// A replacement may move the hook's source: the card shows the source
// now and after, and the approval keeps the secret file it stands with.
func TestHookTableSourceChange(t *testing.T) {
	t.Parallel()
	y := hookTableYard(t)
	text := strings.Replace(hookLinear, "name: linear", "name: lin2", 1)
	if _, err := y.PutSource(y.ctx, "lin2.yaml", text, "mckean"); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "linear.secret")
	hookTableSecret(t, file)
	if _, err := y.PutHook(y.ctx, Hook{Name: "linear", Source: "linear", SecretFile: file}, "mckean"); err != nil {
		t.Fatal(err)
	}
	seq := y.ask(t, hookTableAsk("hook add linear --source lin2", nil, nil), "yardmaster")
	does := strings.Join(y.asked(t, seq).Ask.Run.Does, "\n")
	if !strings.Contains(does, "takes deliveries from the source lin2 (https://api.linear.app). It serves the source linear now.") {
		t.Fatalf("the card says:\n%s", does)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	hook, err := y.GetHook(y.ctx, "linear")
	if err != nil || hook.Source != "lin2" || hook.SecretFile != file {
		t.Fatalf("the hook stands as %+v, %v", hook, err)
	}

	t.Run("identity hearing removed", func(t *testing.T) {
		y := chatxYard(t)
		file := filepath.Join(t.TempDir(), "chatx.secret")
		hookTableSecret(t, file)
		if _, err := y.PutHook(y.ctx, Hook{Name: "chatx", Source: "chatx", SecretFile: file, Unknown: HookUnknownAllow}, "mckean"); err != nil {
			t.Fatal(err)
		}
		seq := y.ask(t, hookTableAsk("hook add chatx --source linear", nil, nil), "yardmaster")
		does := strings.Join(y.asked(t, seq).Ask.Run.Does, "\n")
		if !strings.Contains(does, "it has no --unknown policy now; before, it stored --unknown allow.") {
			t.Fatalf("the card says:\n%s", does)
		}
		if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err != nil {
			t.Fatal(err)
		}
		if hook, err := y.GetHook(y.ctx, "chatx"); err != nil || hook.Source != "linear" || hook.Unknown != "" {
			t.Fatalf("the hook stands as %+v, %v", hook, err)
		}
	})

	t.Run("identity hearing added", func(t *testing.T) {
		y := chatxYard(t)
		file := filepath.Join(t.TempDir(), "move.secret")
		hookTableSecret(t, file)
		if _, err := y.PutHook(y.ctx, Hook{Name: "move", Source: "linear", SecretFile: file}, "mckean"); err != nil {
			t.Fatal(err)
		}
		seq := y.ask(t, hookTableAsk("hook add move --source chatx", nil, nil), "yardmaster")
		does := strings.Join(y.asked(t, seq).Ask.Run.Does, "\n")
		if !strings.Contains(does, "it stores --unknown unknown for an unmapped sender") ||
			!strings.Contains(does, "Before, it had no --unknown policy.") {
			t.Fatalf("the card says:\n%s", does)
		}
		if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err != nil {
			t.Fatal(err)
		}
		if hook, err := y.GetHook(y.ctx, "move"); err != nil || hook.Source != "chatx" || hook.Unknown != HookUnknownUnknown {
			t.Fatalf("the hook stands as %+v, %v", hook, err)
		}
	})
}

// A table ask is held to the hook as its card saw it: changed, removed,
// or added since, the approval runs nothing and says what moved.
func TestHookTableStale(t *testing.T) {
	t.Parallel()
	t.Run("changed since", func(t *testing.T) {
		y := hookTableYard(t)
		file := filepath.Join(t.TempDir(), "linear.secret")
		hookTableSecret(t, file)
		if _, err := y.PutHook(y.ctx, Hook{Name: "linear", Source: "linear", SecretFile: file}, "mckean"); err != nil {
			t.Fatal(err)
		}
		seq := y.ask(t, hookTableAsk("hook add linear --source linear",
			[]string{"action=opened event=issues -> done"}, nil), "yardmaster")
		if _, err := y.PutHook(y.ctx, Hook{Name: "linear", Source: "linear", SecretFile: file,
			Says: []Say{{Where: map[string]string{"action": "opened", "event": "issues"}, Outcome: "done"}}}, "mckean"); err != nil {
			t.Fatal(err)
		}
		if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil ||
			!strings.Contains(err.Error(), "it was added again or changed since") {
			t.Fatalf("approving after the hook changed = %v", err)
		}
	})
	t.Run("removed since", func(t *testing.T) {
		y := hookTableYard(t)
		file := filepath.Join(t.TempDir(), "linear.secret")
		hookTableSecret(t, file)
		if _, err := y.PutHook(y.ctx, Hook{Name: "linear", Source: "linear", SecretFile: file}, "mckean"); err != nil {
			t.Fatal(err)
		}
		seq := y.ask(t, hookTableAsk("hook add linear --source linear",
			[]string{"action=opened event=issues -> done"}, nil), "yardmaster")
		if err := y.DeleteHook(y.ctx, "linear", "mckean"); err != nil {
			t.Fatal(err)
		}
		if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil ||
			!strings.Contains(err.Error(), "it was removed since") {
			t.Fatalf("approving after the hook was removed = %v", err)
		}
	})
	t.Run("added since", func(t *testing.T) {
		y := hookTableYard(t)
		seq := y.ask(t, hookTableAsk("hook add atlas --source linear",
			[]string{"action=opened event=issues -> done"}, nil), "yardmaster")
		file := filepath.Join(t.TempDir(), "atlas.secret")
		hookTableSecret(t, file)
		if _, err := y.PutHook(y.ctx, Hook{Name: "atlas", Source: "linear", SecretFile: file}, "mckean"); err != nil {
			t.Fatal(err)
		}
		if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil ||
			!strings.Contains(err.Error(), "it was added since") {
			t.Fatalf("approving after the hook was added = %v", err)
		}
	})
}

// What the card refused at asking it refuses again at approval: an
// identity or a flow edge gone since, and the approval runs nothing.
func TestHookTableApprovalRechecks(t *testing.T) {
	t.Parallel()
	t.Run("identity gone since", func(t *testing.T) {
		y := hookTableYard(t)
		file := filepath.Join(t.TempDir(), "linear.secret")
		hookTableSecret(t, file)
		if _, err := y.PutHook(y.ctx, Hook{Name: "linear", Source: "linear", SecretFile: file}, "mckean"); err != nil {
			t.Fatal(err)
		}
		seq := y.ask(t, hookTableAsk("hook add linear --source linear --as yardmaster", nil, nil), "yardmaster")
		if err := y.SetIdentity(y.ctx, "yardmaster", "linear", Identity{}, "mckean"); err != nil {
			t.Fatal(err)
		}
		if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil ||
			!strings.Contains(err.Error(), "has no identity in linear") {
			t.Fatalf("approving after the identity went = %v", err)
		}
		if hook, err := y.GetHook(y.ctx, "linear"); err != nil || hook.As != "" {
			t.Fatalf("the refused approval changed the hook: %+v, %v", hook, err)
		}
	})
	t.Run("flow edge gone since", func(t *testing.T) {
		y := hookTableYard(t)
		file := filepath.Join(t.TempDir(), "linear.secret")
		hookTableSecret(t, file)
		if _, err := y.PutHook(y.ctx, Hook{Name: "linear", Source: "linear", SecretFile: file}, "mckean"); err != nil {
			t.Fatal(err)
		}
		seq := y.ask(t, hookTableAsk("hook add linear --source linear",
			[]string{"action=closed event=issues -> changes"}, nil), "yardmaster")
		narrower, err := ParseFlow("new -> merged on done\nmerged terminal\n")
		if err != nil {
			t.Fatal(err)
		}
		narrower.Name = "web"
		if _, err := y.PutFlow(y.ctx, *narrower, "mckean"); err != nil {
			t.Fatal(err)
		}
		if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil ||
			!strings.Contains(err.Error(), "no bead of the yard's depots could take it") {
			t.Fatalf("approving after the flow edge went = %v", err)
		}
		if hook, err := y.GetHook(y.ctx, "linear"); err != nil || len(hook.Says) != 0 {
			t.Fatalf("the refused approval changed the hook: %+v, %v", hook, err)
		}
	})
}
