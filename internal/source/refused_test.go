package source

import (
	"strings"
	"testing"
)

// Only a 401 is told, in the words curl gives a service's answer under --fail
// and --fail-with-body; every other failure is left as it is.
func TestRefused401(t *testing.T) {
	for _, c := range []struct {
		name string
		text string
		want bool
	}{
		{"curl's line", "thread.reply of source linear: exit status 22; its stderr ended: curl: (22) The requested URL returned error: 401", true},
		{"curl's older line", "its stderr ended: curl: (22) The requested URL returned error: 401 Unauthorized", true},
		{"a 403", "its stderr ended: curl: (22) The requested URL returned error: 403", false},
		{"a 429", "its stderr ended: curl: (22) The requested URL returned error: 429", false},
		{"a 500", "its stderr ended: curl: (22) The requested URL returned error: 500", false},
		{"a 4010", "its stderr ended: curl: (22) The requested URL returned error: 4010", false},
		{"a bare exit status", "thread.reply of source linear: exit status 22", false},
		{"a 401 only named in the command's own words", "thread.state of source acme: exit status 1; its stderr ended: this team has no status for done (401 teams)", false},
		{"nothing", "", false},
	} {
		if got := Refused401(c.text); got != c.want {
			t.Errorf("%s: Refused401(%q) = %v, want %v", c.name, c.text, got, c.want)
		}
	}
}

// The words name the source, what was sent and where it was read from, how a
// call carried it, and the one line that sets it right, and no value.
func TestRefusedWords(t *testing.T) {
	const static = `name: crm
base: https://crm.example.com
credential: { env: CRM_TOKEN, update_env: CRM_UPDATE_TOKEN, header: "Authorization: Bearer {credential}" }
calls:
  customer: { kind: read, request: { method: GET, path: /customers } }
`
	minted := strings.Replace(static, "update_env: CRM_UPDATE_TOKEN,", "mint: [CRM_APP_ID, CRM_APP_KEY],", 1)
	for _, c := range []struct {
		name string
		decl string
		r    Refusal
		want string
	}{
		{"a static key a call sent", static, Refusal{Kind: Read, Header: true, From: "as set in the source's env_file /h/sources/crm.env", Fix: "give it again"},
			"crm refused the credential (401): CRM_TOKEN, as set in the source's env_file /h/sources/crm.env, sent as Authorization: Bearer <credential>; to fix: give it again"},
		{"the key of an update", static, Refusal{Kind: Update, Header: true, From: "as set in the source's env_file /h/sources/crm.env", Fix: "give it again"},
			"crm refused the credential (401): CRM_UPDATE_TOKEN, as set in the source's env_file /h/sources/crm.env, sent as Authorization: Bearer <credential>; to fix: give it again"},
		{"a token minted for a call", minted, Refusal{Kind: Read, Header: true, Minted: true, From: "as set in ada's env_file /h/users/ada.env", Fix: "give its inputs again"},
			"crm refused the credential (401): the token minted from CRM_APP_ID and CRM_APP_KEY, as set in ada's env_file /h/users/ada.env, sent as Authorization: Bearer <credential>; to fix: give its inputs again"},
		// A command builds its own header: none is named for it.
		{"a static key a call's command sent", static, Refusal{Kind: Update, From: "as set in the source's env_file /h/sources/crm.env", Fix: "give it again"},
			"crm refused the credential (401): CRM_UPDATE_TOKEN, as set in the source's env_file /h/sources/crm.env; to fix: give it again"},
		{"a static key a thread command sent", static, Refusal{From: "as read from crew member yardmaster's env_file /h/crew/yardmaster.env when its session started", Fix: "give it again"},
			"crm refused the credential (401): CRM_TOKEN, as read from crew member yardmaster's env_file /h/crew/yardmaster.env when its session started; to fix: give it again"},
		{"a token minted for a thread command", minted, Refusal{Minted: true, From: "as read from crew member yardmaster's env_file /h/crew/yardmaster.env when its session started", Fix: "give its inputs again"},
			"crm refused the credential (401): the token minted from CRM_APP_ID and CRM_APP_KEY, as read from crew member yardmaster's env_file /h/crew/yardmaster.env when its session started; to fix: give its inputs again"},
	} {
		d, err := Parse("crm.yaml", []byte(c.decl))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := d.RefusedWords(c.r); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
	none, err := Parse("none.yaml", []byte("name: none\nbase: https://none.example.com\ncalls:\n  ping: { kind: read, request: { method: GET, path: /ping } }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := none.RefusedWords(Refusal{From: "x", Fix: "y"}); got != "" {
		t.Errorf("a source with no credential: %q, want none", got)
	}
}
