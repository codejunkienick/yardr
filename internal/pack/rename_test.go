package pack_test

import (
	"slices"
	"testing"

	"yardr.dev/yardr/internal/pack"
)

// A group the pack made is renamed, in the one write that moves what names it
// (Store.RenameGroup), and the next import of the pack finds it by its new
// name: the plan creates, changes and drops nothing, the groups the yard has
// are the same ones, and the name the depot would have given it, which is the
// old one, comes back nowhere. The record of the import is what says so: a
// record that kept the old name would find no group by it, and make one.
func TestImportAfterAGroupWasRenamedBringsTheOldNameBackNowhere(t *testing.T) {
	st, o := yard(t)
	withLinear(t, st)
	dir := copyStarter(t)
	apply(t, st, build(t, st, load(t, dir), o), answers)
	groups := func() []string {
		t.Helper()
		gs, err := st.Groups(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, g := range gs {
			names = append(names, g.Name)
		}
		slices.Sort(names)
		return names
	}
	before := groups()

	// The pack's reviewers, which the starter's routes send work to.
	if _, err := st.RenameGroup(ctx, "web-reviewers", "reviewers", "me"); err != nil {
		t.Fatal(err)
	}
	renamed := slices.Clone(before)
	renamed[slices.Index(renamed, "web-reviewers")] = "reviewers"
	slices.Sort(renamed)
	if got := groups(); !slices.Equal(got, renamed) {
		t.Fatalf("the yard's groups after the rename = %v, want %v", got, renamed)
	}

	plan := build(t, st, load(t, dir), o)
	if c, ch, n := actions(plan, pack.Create), actions(plan, pack.Change), actions(plan, pack.NotInPack); len(c)+len(ch)+len(n) != 0 {
		t.Fatalf("an import after the rename would create %v, change %v, and finds %v not in the pack", c, ch, n)
	}
	if len(plan.Questions) != 0 {
		t.Fatalf("the import asks %v", plan.Questions)
	}
	if g := find(t, plan, "group", "reviewers"); g.Action != pack.Unchanged || g.Group.Name != "reviewers" {
		t.Fatalf("the pack's reviewers are %s, group %+v: the renamed one, unchanged", g.Action, g.Group)
	}
	for _, o := range plan.Objects {
		if o.Kind == "group" && o.Name == "web-reviewers" {
			t.Fatalf("the plan has a group web-reviewers: %+v", o)
		}
	}
	if res := apply(t, st, plan, nil); res.Wrote() {
		t.Fatalf("the import after the rename wrote %+v", res)
	}
	if got := groups(); !slices.Equal(got, renamed) {
		t.Fatalf("the yard's groups after the import = %v, want %v: the old name is not back", got, renamed)
	}
	routes, err := st.Routes(ctx)
	if err != nil || len(routes) != 11 {
		t.Fatalf("%d routes, %v, want the same 11", len(routes), err)
	}
	for _, r := range routes {
		if r.Names("web-reviewers") {
			t.Errorf("route %s names the old name: %+v", r.ID, r)
		}
	}
}
