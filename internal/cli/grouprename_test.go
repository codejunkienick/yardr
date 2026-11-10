package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
)

// yardr group rename answers what followed the name, as the card of the same
// words does; it is the operator's alone; it refuses a group that is not there,
// a name in use and a name pack imports give a depot's groups, each in the
// store's words; and the group is shown, listed and routed to under the new
// name alone, its session and bead going on.
func TestGroupRenameCommand(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.SetSetting(ctx, store.OperatorSetting, "jo", "jo"))
	for _, g := range []store.Group{
		{Name: "builders", Runner: "herdr", Limit: 1, Config: map[string]any{"kind": "claude"}},
		{Name: "reviewers", Runner: "herdr", Limit: 1},
	} {
		must(st.PutGroup(ctx, g, "jo"))
	}
	must(st.PutDepot(ctx, store.Depot{Name: "web", Base: "origin/main"}, "jo"))
	route, err := st.AddRoute(ctx, store.Route{Stage: "new", Group: "builders"}, "jo")
	must(err)
	bead, err := st.CreateBead(ctx, store.NewBead{Title: "work", Stage: "new"}, "jo")
	must(err)
	sess, err := st.Claim(ctx, bead.ID, "builders", "new", "yard")
	must(err)

	rename := func(args ...string) []string { return append([]string{"group", "rename"}, args...) }
	refusedAs(t, st, "yardmaster", "only the operator (jo) may rename a group; this is yardmaster", rename("builders", "makers")...)
	for want, args := range map[string][]string{
		"group nobody: not found":              {"nobody", "makers"},
		"group builders has that name already": {"builders", "builders"},
		"group reviewers is there already":     {"builders", "reviewers"},
		"the groups they make for depot web":   {"builders", "web-builders"},
		"use letters, digits":                  {"builders", "Mak Ers"},
		"accepts 2 arg(s), received 1":         {"builders"},
		"accepts 2 arg(s), received 3":         {"builders", "a", "b"},
	} {
		refusedAs(t, st, "jo", want, rename(args...)...)
	}
	if g, err := st.GetGroup(ctx, "builders"); err != nil || g.Name != "builders" {
		t.Fatalf("the refusals left the group as %+v, %v", g, err)
	}

	wantText(t, mustAs(t, st, "jo", rename("builders", "makers")...),
		"group builders is makers now: the old name is free, and reaches nothing",
		"routes that name it now: "+route.ID,
		"its arrival estimates from the old name stand under the new one",
		"its open beads (1) and live sessions (1) go on under the new name; none is released")
	refusedAs(t, st, "jo", "group builders: not found", "group", "show", "builders")
	wantText(t, mustAs(t, st, "jo", "group", "show", "makers"), "makers", "route "+route.ID+": stage new")
	if list := mustAs(t, st, "jo", "group", "list"); !listsGroup(list, "makers") || listsGroup(list, "builders") {
		t.Errorf("group list after the rename:\n%s", list)
	}
	b, err := st.GetBead(ctx, bead.ID)
	must(err)
	if b.Group == nil || *b.Group != "makers" || b.Session == nil || *b.Session != sess.ID {
		t.Errorf("the bead after the rename = %+v, want makers' and the session's still", b)
	}

	var got store.GroupRenamed
	must(json.Unmarshal([]byte(mustAs(t, st, "jo", append(rename("makers", "east"), "--json")...)), &got))
	if got.From != "makers" || got.To != "east" || len(got.Routes) != 1 || got.Routes[0] != route.ID || got.Beads != 1 || got.Sessions != 1 {
		t.Errorf("--json answers %+v", got)
	}
}

// listsGroup says whether a group list has a row for the group.
func listsGroup(list, name string) bool {
	for _, line := range strings.Split(list, "\n") {
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] == name {
			return true
		}
	}
	return false
}
