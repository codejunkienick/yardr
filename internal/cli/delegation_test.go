package cli

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/store"
)

// The yardmaster runs a delegated command from its CLI with --for and
// --mail, naming a preset where args would go; the store keeps the record,
// and yardr delegation show reads it for an operator alone.
func TestDelegatedRunFromTheCLI(t *testing.T) {
	t.Parallel()
	st := userYard(t)
	ctx := context.Background()
	mustAs(t, st, "jo", "user", "add", "ada")
	mustAs(t, st, "jo", "settings", "set", store.DelegationSetting, "true")
	mustAs(t, st, "jo", "group", "add", "builders", "--runner", "herdr", "--set", "kind=claude")
	mustAs(t, st, "jo", "mail", "yardmaster", "give the builders the accept-edits preset")
	head, err := st.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mail := strconv.FormatUint(head, 10)

	if _, err := as(t, st, "yardmaster", "group", "set", "builders", "preset=accept-edits"); err == nil {
		t.Fatal("the yardmaster set a group's args with no delegation")
	}
	if _, err := as(t, st, "yardmaster", "group", "set", "builders", "preset=accept-edits", "--mail", mail); err == nil || !strings.Contains(err.Error(), "goes with --for") {
		t.Errorf("--mail alone: %v", err)
	}
	if _, err := as(t, st, "yardmaster", "group", "set", "builders", `args=["--yolo"]`, "--for", "jo", "--mail", mail); err == nil || !strings.Contains(err.Error(), "never carries free args") {
		t.Errorf("free args on a delegated run: %v", err)
	}
	mustAs(t, st, "yardmaster", "group", "set", "builders", "preset=accept-edits", "--for", "jo", "--mail", mail)
	accept, _ := store.HarnessPreset("claude", "accept edits")
	g, err := st.GetGroup(ctx, "builders")
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	for _, a := range g.Config["args"].([]any) {
		args = append(args, a.(string))
	}
	if !reflect.DeepEqual(args, accept.Args) {
		t.Errorf("builders' args = %v, want the accept-edits preset's %v", args, accept.Args)
	}

	show := mustAs(t, st, "jo", "delegation", "show", "1")
	for _, want := range []string{"record 1: yardmaster for jo", "on mail " + mail + " from jo", "ran: yardr group set builders preset=accept-edits", "args: (unset) -> ", "taken back by: yardr group set builders --unset args"} {
		if !strings.Contains(show, want) {
			t.Errorf("delegation show lacks %q:\n%s", want, show)
		}
	}
	for _, reader := range []string{"ada", "yardmaster"} {
		if out, err := as(t, st, reader, "delegation", "show", "1"); err == nil || !strings.Contains(err.Error(), "not found") {
			t.Errorf("%s reads record 1: %q, %v", reader, out, err)
		}
	}

	// depot test and depot base change one field each, for the operator at
	// a terminal and for the yardmaster's delegated run alike.
	mustAs(t, st, "jo", "depot", "add", "docs", t.TempDir(), "--kind", "dir")
	mustAs(t, st, "jo", "depot", "test", "docs", "make test")
	mustAs(t, st, "yardmaster", "depot", "test", "docs", "sh .yardr/test", "--for", "jo", "--mail", mail)
	if d, err := st.GetDepot(ctx, "docs"); err != nil || d.Test != "sh .yardr/test" {
		t.Errorf("docs = %+v, %v", d, err)
	}
	if show := mustAs(t, st, "jo", "delegation", "show", "2"); !strings.Contains(show, "taken back by: yardr depot test docs 'make test'") {
		t.Errorf("the depot test's record:\n%s", show)
	}
	if err := st.PutGroup(ctx, store.Group{Name: "inherited", Runner: "herdr"}, "jo"); err != nil {
		t.Fatal(err)
	}
	mustAs(t, st, "yardmaster", "group", "set", "inherited", "preset=accept-edits", "--for", "jo", "--mail", mail)
	mustAs(t, st, "yardmaster", "group", "add", "reviewers", "--runner", "herdr", "--set", "preset=accept-edits", "--for", "jo", "--mail", mail)
	if _, err := as(t, st, "yardmaster", "hook", "add", "delegated", "--source", "token", "--secret-file", t.TempDir(), "--for", "jo", "--mail", mail); err == nil || !strings.Contains(err.Error(), "--secret-file is the operator's") {
		t.Errorf("explicit secret file in a delegated hook add: %v", err)
	}
}
