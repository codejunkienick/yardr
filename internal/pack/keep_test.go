package pack_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/config"
	"yardr.dev/yardr/internal/pack"
	"yardr.dev/yardr/internal/store"
)

// recordedFlow is the digest the yard's record of the starter keeps for the
// flow name, "" when it keeps none.
func recordedFlow(t *testing.T, st store.API, name string) string {
	t.Helper()
	for _, po := range record(t, st).Objects {
		if po.Kind == "flow" && po.Name == name {
			return po.SHA256
		}
	}
	t.Fatalf("the record has no flow %s: %+v", name, record(t, st).Objects)
	return ""
}

// storedDigest is the digest of the flow the store holds under name.
func storedDigest(t *testing.T, st store.API, name string) string {
	t.Helper()
	f, err := st.GetFlow(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	d, err := f.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// yardEdit changes the flow the store holds under name, as a person does
// with yardr flow set, and says nothing to any import.
func yardEdit(t *testing.T, st store.API, name string, change func(f *store.Flow)) {
	t.Helper()
	f, err := st.GetFlow(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	change(f)
	if _, err := st.PutFlow(ctx, *f, "me"); err != nil {
		t.Fatal(err)
	}
}

// movePack changes what the starter's decide stage may move a bead to, as
// TestChangedFlowIsShownByStage does: a change of the pack's flow.
func movePack(t *testing.T, dir string) {
	t.Helper()
	edit(t, dir, "manual: [backlog, new] }", "manual: [backlog] }")
}

// A pack is a starting point. A flow the yard changed is kept by an import,
// which says so, in the plan and in its result, and leaves the pack's version
// until it is taken by name: --yes does not take it. Where the pack moved too
// the line says DIVERGED, and the record takes the pack's version either way,
// so that the next plan tells a pack that moved since from one that did not.
func TestFlowTheYardChangedIsKeptUntilTaken(t *testing.T) {
	st, o := yard(t)
	dir := copyStarter(t)
	first := build(t, st, load(t, dir), o)
	apply(t, st, first, answers)

	// The record holds the digest of the flow as the store then held it.
	want, err := find(t, first, "flow", "starter.default").Flow.Digest()
	if err != nil || recordedFlow(t, st, "starter.default") != want || storedDigest(t, st, "starter.default") != want {
		t.Fatalf("record %q, store %q, pack %q (%v)", recordedFlow(t, st, "starter.default"), storedDigest(t, st, "starter.default"), want, err)
	}
	if again := build(t, st, load(t, dir), o); len(again.Objects) != len(actions(again, pack.Unchanged)) {
		t.Fatalf("a second import is not all unchanged: create %v change %v kept %v", actions(again, pack.Create), actions(again, pack.Change), actions(again, pack.Kept))
	}

	// The yard adds a stage, and a bead sits in it: the pack's flow, which
	// lacks the stage, is not what the bead follows once the import leaves it.
	yardEdit(t, st, "starter.default", func(f *store.Flow) {
		f.Transitions["backlog"] = append(f.Transitions["backlog"], "triage")
		f.Transitions["triage"] = []string{"new"}
	})
	bead, err := st.CreateBead(ctx, store.NewBead{Title: "to sort", Depot: "web", Stage: "triage"}, "ann")
	if err != nil {
		t.Fatal(err)
	}
	plan := build(t, st, load(t, dir), o)
	f := find(t, plan, "flow", "starter.default")
	if f.Action != pack.Kept || f.Edited || len(f.Keeps) != 1 || len(f.Changes) == 0 || len(plan.EditedFiles()) != 0 || len(plan.Refused) != 0 ||
		!strings.Contains(f.Keeps[0], "changed since the last import (the pack's has not changed since)") || !strings.Contains(f.Keeps[0], "--take flow/starter.default") || strings.Contains(f.Keeps[0], "DIVERGED") {
		t.Fatalf("flow after a change of the yard's = %+v, refused %v", f, plan.Refused)
	}
	// What the pack would change is shown, so taking is a choice made with
	// the difference in view.
	if want := (pack.Diff{Field: "stage triage", Old: "-> new", New: "no such stage"}); !slices.Contains(f.Changes, want) {
		t.Fatalf("changes = %v, want %v among them", f.Changes, want)
	}
	before := head(t, st)
	for _, opts := range []pack.ApplyOptions{{Actor: "me"}, {Actor: "me", Overwrite: true}} {
		res, err := pack.Apply(ctx, st, plan, pack.ApplyOptions{Answers: answered(t, plan, nil), Actor: opts.Actor, Overwrite: opts.Overwrite})
		if err != nil || res.Changed != 0 || res.Wrote() || !slices.Equal(res.Kept, []string{"flow starter.default: " + f.Keeps[0]}) {
			t.Fatalf("applying over the yard's flow with %+v: %+v, %v", opts, res, err)
		}
		if got := storedDigest(t, st, "starter.default"); got == want {
			t.Fatalf("applying with %+v wrote the pack's flow", opts)
		}
	}
	// Nothing was written and the record is as it was: one event says so, per
	// apply.
	if got := kinds(t, st, before); !slices.Equal(got, []string{"pack_unchanged", "pack_unchanged"}) {
		t.Fatalf("events = %v", got)
	}

	// The pack moves while the yard's change stands: its fix would drop
	// unseen, so the line says so. The record then takes the pack's version.
	if _, err := st.CloseBead(ctx, bead.ID, "sorted", store.Expect{}, "me"); err != nil {
		t.Fatal(err)
	}
	movePack(t, dir)
	moved := build(t, st, load(t, dir), o)
	m := find(t, moved, "flow", "starter.default")
	if m.Action != pack.Kept || len(m.Keeps) != 1 || !strings.Contains(m.Keeps[0], "DIVERGED") {
		t.Fatalf("flow after the pack moved too = %+v", m)
	}
	res := apply(t, st, moved, nil)
	if len(res.Kept) != 1 || !res.Recorded || res.Wrote() {
		t.Fatalf("applying while diverged: %+v", res)
	}
	moveDigest, _ := find(t, moved, "flow", "starter.default").Flow.Digest()
	if got := recordedFlow(t, st, "starter.default"); got != moveDigest {
		t.Fatalf("the record holds %s, not the pack's version %s", got, moveDigest)
	}
	if again := find(t, build(t, st, load(t, dir), o), "flow", "starter.default"); again.Action != pack.Kept || strings.Contains(again.Keeps[0], "DIVERGED") || !strings.Contains(again.Keeps[0], "the pack's has not changed since") {
		t.Fatalf("the plan after it = %+v", again)
	}

	// Taken by name, the pack's version is written, and recorded as written.
	taking := build(t, st, load(t, dir), o)
	res, err = pack.Apply(ctx, st, taking, pack.ApplyOptions{Answers: answered(t, taking, nil), Actor: "me", Take: []string{"flow/starter.default"}})
	if err != nil || res.Changed != 1 || len(res.Kept) != 0 {
		t.Fatalf("taking the flow: %+v, %v", res, err)
	}
	if got := storedDigest(t, st, "starter.default"); got != moveDigest {
		t.Fatalf("the store holds %s after the take, the pack's is %s", got, moveDigest)
	}
	if after := build(t, st, load(t, dir), o); find(t, after, "flow", "starter.default").Action != pack.Unchanged || len(actions(after, pack.Kept)) != 0 {
		t.Fatalf("a plan after the take keeps %v", actions(after, pack.Kept))
	}
}

// Where nothing recorded what an import wrote of the flow (the yard had a flow
// of the name before the pack came, or its record is older than the digest),
// a flow that differs is kept: whose change it is cannot be told, and the
// yard's version is the one that can be lost. It is visible, says so, and is
// taken by name; the record then holds the pack's version.
func TestFlowWithNoRecordedDigestIsKeptAsUnknown(t *testing.T) {
	st, o := yard(t)
	dir := copyStarter(t)
	// A flow of the pack's store name, made by hand before any import.
	pre := store.Flow{Name: "starter.default", Stages: []string{"backlog", "new", "merged"},
		Transitions: map[string][]string{"backlog": {"new"}, "new": {"merged"}}, Terminal: []string{"merged"}}
	if _, err := st.PutFlow(ctx, pre, "me"); err != nil {
		t.Fatal(err)
	}
	plan := build(t, st, load(t, dir), o)
	f := find(t, plan, "flow", "starter.default")
	if f.Action != pack.Kept || len(f.Keeps) != 1 || !strings.Contains(f.Keeps[0], "no import recorded what it wrote") || strings.Contains(f.Keeps[0], "DIVERGED") {
		t.Fatalf("a flow of the name with no record = %+v", f)
	}
	if res := apply(t, st, plan, answers); len(res.Kept) != 1 || res.Created == 0 {
		t.Fatalf("the first import over it: %+v", res)
	}
	if got := storedDigest(t, st, "starter.default"); got == recordedFlow(t, st, "starter.default") {
		t.Fatal("the first import wrote over the flow it found")
	}
	// The record has a baseline now, so the next plan knows the pack did not move.
	f = find(t, build(t, st, load(t, dir), o), "flow", "starter.default")
	if f.Action != pack.Kept || !strings.Contains(f.Keeps[0], "the pack's has not changed since") {
		t.Fatalf("the next plan = %+v", f)
	}
	taking := build(t, st, load(t, dir), o)
	if res, err := pack.Apply(ctx, st, taking, pack.ApplyOptions{Answers: answered(t, taking, nil), Actor: "me", Take: []string{"flow/starter.default"}}); err != nil || res.Changed != 1 {
		t.Fatalf("taking it: %+v, %v", res, err)
	}

	// A record older than the digest: the yard's flow is untouched and the
	// pack moved, which only a record of what was written could tell from the
	// yard's own change. It is kept, and takeable.
	rec := record(t, st)
	for i := range rec.Objects {
		if rec.Objects[i].Kind == "flow" {
			rec.Objects[i].SHA256 = ""
		}
	}
	if _, err := st.RecordPack(ctx, rec, true, "me"); err != nil {
		t.Fatal(err)
	}
	movePack(t, dir)
	moved := build(t, st, load(t, dir), o)
	if f := find(t, moved, "flow", "starter.default"); f.Action != pack.Kept || !strings.Contains(f.Keeps[0], "no import recorded what it wrote") {
		t.Fatalf("flow with an old record after the pack moved = %+v", f)
	}
	if res := apply(t, st, moved, nil); len(res.Kept) != 1 || recordedFlow(t, st, "starter.default") == "" {
		t.Fatalf("applying it: %+v, record %q", res, recordedFlow(t, st, "starter.default"))
	}
}

// What --take names must be a kept object of the plan, or the import is
// refused before it writes anything: a declared source stays as declared, a
// name the plan lacks and one it does not keep are told apart, and so is a
// name that is no kind/name.
func TestTakeRefusesWhatIsNotKept(t *testing.T) {
	st, o := yard(t)
	dir := copyStarter(t)
	apply(t, st, build(t, st, load(t, dir), o), answers)
	yardEdit(t, st, "starter.default", func(f *store.Flow) { f.Transitions["decide"] = append(f.Transitions["decide"], "merged") })
	plan := build(t, st, load(t, dir), o)
	if got := actions(plan, pack.Kept); !slices.Equal(got, []string{"flow starter.default"}) {
		t.Fatalf("kept = %v", got)
	}
	for take, want := range map[string]string{
		"source/linear":          "--take source/linear: a declared source stays as the pack declares it, and nothing of it is kept",
		"flow/starter.nothing":   "--take flow/starter.nothing: the plan has no such object (it keeps flow/starter.default)",
		"file/roles/builder.md":  "--take file/roles/builder.md: the plan does not keep it, so there is nothing to take (it is unchanged in the plan; it keeps flow/starter.default)",
		"starter.default":        "--take starter.default: name an object as the plan prints it, <kind>/<name>",
		"flow/":                  "--take flow/: name an object as the plan prints it, <kind>/<name>",
		"flow/starter.train":     "--take flow/starter.train: the plan does not keep it, so there is nothing to take (it is unchanged in the plan; it keeps flow/starter.default)",
		"pointer/web -> starter": "--take pointer/web -> starter: the plan has no such object",
	} {
		before := head(t, st)
		_, err := pack.Apply(ctx, st, plan, pack.ApplyOptions{Answers: answered(t, plan, nil), Actor: "me", Take: []string{"flow/starter.default", take}})
		if err == nil || !strings.HasPrefix(err.Error(), "nothing was changed: ") || !strings.Contains(err.Error(), want) {
			t.Errorf("--take %s: %v, want it to say %q", take, err, want)
		}
		if len(kinds(t, st, before)) != 0 {
			t.Errorf("--take %s: the refused import wrote: %v", take, kinds(t, st, before))
		}
	}
	// The refusal leaves the yard's flow and the taken name alike.
	if got := storedDigest(t, st, "starter.default"); got == recordedFlow(t, st, "starter.default") {
		t.Fatal("a refused import took the pack's flow")
	}
	// A valid take must not change the plan if another check refuses the
	// import. Retrying that plan without --take still leaves the yard's flow.
	_, err := pack.Apply(ctx, st, plan, pack.ApplyOptions{
		Answers:     answered(t, plan, nil),
		Actor:       "me",
		Take:        []string{"flow/starter.default"},
		OpenThreads: map[string]map[string]bool{"unknown": {"linear": true}},
	})
	if err == nil || !strings.Contains(err.Error(), "the plan does not ask whether crew member unknown may open threads in linear") {
		t.Fatalf("invalid grant with a valid take: %v", err)
	}
	if got := find(t, plan, "flow", "starter.default"); got.Action != pack.Kept {
		t.Fatalf("the refused take changed the plan to %s", got.Action)
	}
	res, err := pack.Apply(ctx, st, plan, pack.ApplyOptions{Answers: answered(t, plan, nil), Actor: "me"})
	if err != nil || len(res.Kept) != 1 || res.Changed != 0 {
		t.Fatalf("retrying without the take: %+v, %v", res, err)
	}
}

// A flow the pack has dropped that the yard changed since an import wrote it
// stops the import, as a file edited by hand does, and with it the flow's
// pointer and its stage files: a prune would remove the yard's change. With
// --yes they all go. A record that holds no digest says nothing, as for a file,
// and the flow goes as it always did.
func testEditedFlowThePackDroppedStopsThePrune(t *testing.T) {
	const duo = `name: duo
flows:
  default:
    stages:
      new:  { group: people, file: flows/default/new.md, manual: [done] }
      done: { end: true }
  trains:
    type: train
    stages:
      open:   { group: people, file: flows/trains/open.md, manual: [landed] }
      landed: { end: true }
groups:
  people: { people: true }
`
	const without = `name: duo
flows:
  default:
    stages:
      new:  { group: people, file: flows/default/new.md, manual: [done] }
      done: { end: true }
groups:
  people: { people: true }
`
	for _, stripped := range []bool{false, true} {
		st, o := yard(t)
		dir := writePack(t, map[string]string{"pack.yaml": duo, "flows/default/new.md": "new\n", "flows/trains/open.md": "open\n"})
		apply(t, st, build(t, st, load(t, dir), o), nil)
		if stripped {
			rec, err := st.Packs(ctx)
			if err != nil || len(rec) != 1 {
				t.Fatalf("records = %+v, %v", rec, err)
			}
			for i := range rec[0].Objects {
				if rec[0].Objects[i].Kind == "flow" {
					rec[0].Objects[i].SHA256 = ""
				}
			}
			if _, err := st.RecordPack(ctx, rec[0], true, "me"); err != nil {
				t.Fatal(err)
			}
		}
		yardEdit(t, st, "duo.trains", func(f *store.Flow) { f.Transitions["open"] = append(f.Transitions["open"], "open") })
		if err := os.WriteFile(filepath.Join(dir, "pack.yaml"), []byte(without), 0o644); err != nil {
			t.Fatal(err)
		}
		stage := filepath.Join(o.Home, "flows", "duo.trains", "open.md")
		plan := build(t, st, load(t, dir), o)
		gone := []string{"file flows/duo.trains/open.md", "flow duo.trains", "pointer depot web type train -> duo.trains", "route depot web stage open type train -> people"}
		if got := actions(plan, pack.Dropped); !slices.Equal(slices.Sorted(slices.Values(got)), gone) {
			t.Fatalf("stripped %v: dropped = %v, want %v", stripped, got, gone)
		}
		if f := find(t, plan, "flow", "duo.trains"); f.Edited == stripped {
			t.Fatalf("stripped %v: the dropped flow = %+v", stripped, f)
		}
		if stripped {
			// Nothing says the yard changed it: it goes, as a file with no record does.
			res, err := pack.Apply(ctx, st, plan, pack.ApplyOptions{Answers: answered(t, plan, nil), Prune: true, Actor: "me"})
			if err != nil || len(res.Pruned) != len(gone) {
				t.Fatalf("pruning with no digest recorded: %+v, %v", res, err)
			}
			continue
		}
		if got := plan.EditedFiles(); len(got) != 1 || !strings.HasPrefix(got[0], "flow duo.trains was edited since the last import, and the import would remove it") {
			t.Fatalf("edited = %q", got)
		}
		before := head(t, st)
		for _, prune := range []bool{true, false} {
			_, err := pack.Apply(ctx, st, plan, pack.ApplyOptions{Answers: answered(t, plan, nil), Prune: prune, Actor: "me"})
			if err == nil || !strings.HasPrefix(err.Error(), "nothing was changed: flow duo.trains was edited since the last import") {
				t.Fatalf("prune %v over the yard's change: %v", prune, err)
			}
		}
		ptrs, _ := st.FlowPointers(ctx)
		if _, err := st.GetFlow(ctx, "duo.trains"); err != nil || !slices.Contains(ptrs, store.FlowPointer{Depot: "web", Type: "train", Flow: "duo.trains"}) || len(kinds(t, st, before)) != 0 {
			t.Fatalf("the refused import left the flow %v, pointers %v, events %v", err, ptrs, kinds(t, st, before))
		}
		if _, err := os.Stat(stage); err != nil {
			t.Fatalf("the refused import removed the stage file: %v", err)
		}
		// Told it may, it removes the flow, its pointer and its stage file
		// and the route of its stage, the yard's change with them.
		res, err := pack.Apply(ctx, st, plan, pack.ApplyOptions{Answers: answered(t, plan, nil), Prune: true, Overwrite: true, Actor: "me"})
		if err != nil || len(res.Pruned) != len(gone) {
			t.Fatalf("pruning with --yes: %+v, %v", res, err)
		}
		if _, err := st.GetFlow(ctx, "duo.trains"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("flow after the prune: %v", err)
		}
		if _, err := os.Stat(stage); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stage file after the prune: %v", err)
		}
	}
}

// A role the yard changed is kept, the pack's is taken by name, and --yes does
// not take it. The record moves to the pack's version each import, so a pack
// that moves under the yard's change reads DIVERGED once, and a plan after it
// says the pack has not moved since.
func TestHandEditedRoleIsKeptUntilTaken(t *testing.T) {
	st, o := yard(t)
	dir := copyStarter(t)
	apply(t, st, build(t, st, load(t, dir), o), answers)
	to := filepath.Join(config.At(o.Home).PackRoles("starter"), "builder.md")
	from := filepath.Join(dir, "roles", "builder.md")
	if err := os.WriteFile(to, []byte("edited in the yard\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan := build(t, st, load(t, dir), o)
	f := find(t, plan, "file", "roles/builder.md")
	if f.Action != pack.Kept || f.Edited || len(f.Keeps) != 1 || !strings.Contains(f.Keeps[0], "the yard's copy, changed since the last import (the pack's has not changed since)") || !strings.Contains(f.Keeps[0], "--take file/roles/builder.md") {
		t.Fatalf("builder.md after a hand edit = %+v", f)
	}
	res, err := pack.Apply(ctx, st, plan, pack.ApplyOptions{Answers: answered(t, plan, nil), Actor: "me", Overwrite: true})
	if err != nil || len(res.Kept) != 1 || res.Wrote() {
		t.Fatalf("applying over it with --yes: %+v, %v", res, err)
	}
	if text, _ := os.ReadFile(to); string(text) != "edited in the yard\n" {
		t.Fatalf("--yes took the pack's role: %q", text)
	}

	// The pack moves under the yard's change.
	if err := os.WriteFile(from, []byte("the pack's, third edition\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	moved := build(t, st, load(t, dir), o)
	if f := find(t, moved, "file", "roles/builder.md"); f.Action != pack.Kept || !strings.Contains(f.Keeps[0], "DIVERGED") {
		t.Fatalf("builder.md after the pack moved too = %+v", f)
	}
	if res := apply(t, st, moved, nil); len(res.Kept) != 1 || !res.Recorded {
		t.Fatalf("applying while diverged: %+v", res)
	}
	if f := find(t, build(t, st, load(t, dir), o), "file", "roles/builder.md"); f.Action != pack.Kept || strings.Contains(f.Keeps[0], "DIVERGED") {
		t.Fatalf("builder.md after the record moved = %+v", f)
	}

	taking := build(t, st, load(t, dir), o)
	res, err = pack.Apply(ctx, st, taking, pack.ApplyOptions{Answers: answered(t, taking, nil), Actor: "me", Take: []string{"file/roles/builder.md"}})
	if err != nil || res.Changed != 1 || len(res.Kept) != 0 {
		t.Fatalf("taking it: %+v, %v", res, err)
	}
	if text, _ := os.ReadFile(to); string(text) != "the pack's, third edition\n" {
		t.Fatalf("after the take: %q", text)
	}
	if got := actions(build(t, st, load(t, dir), o), pack.Kept); len(got) != 0 {
		t.Fatalf("a plan after the take keeps %v", got)
	}
}

// Only the plan says what is kept: a plan read back as JSON, which is what a
// card or a script holds, carries the action and the lines.
func TestKeptObjectIsInThePlanAsData(t *testing.T) {
	st, o := yard(t)
	dir := copyStarter(t)
	apply(t, st, build(t, st, load(t, dir), o), answers)
	yardEdit(t, st, "starter.default", func(f *store.Flow) { f.Transitions["decide"] = append(f.Transitions["decide"], "merged") })
	f := find(t, build(t, st, load(t, dir), o), "flow", "starter.default")
	if f.TakeName() != "flow/starter.default" || !reflect.DeepEqual(f.Keeps, []string{
		"the yard's flow, changed since the last import (the pack's has not changed since); --take flow/starter.default takes the pack's"}) {
		t.Fatalf("flow = %q, keeps %q", f.TakeName(), f.Keeps)
	}
}
