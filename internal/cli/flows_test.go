package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/config"
	"yardr.dev/yardr/internal/limits"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
)

func TestFlowViews(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.PutDepot(ctx, store.Depot{Name: "yard"}, "me"))
	must(st.PutGroup(ctx, store.Group{Name: "builders", Runner: "manual"}, "me"))
	must(st.PutGroup(ctx, store.Group{Name: "deciders", Runner: "manual", Members: []string{"mckean", "yardmaster"}}, "me"))
	depot, decision := "yard", "decision"
	_, err := st.AddRoute(ctx, store.Route{Stage: "new", Depot: &depot, Group: "builders"}, "me")
	must(err)
	_, err = st.AddRoute(ctx, store.Route{Stage: "open", Type: &decision, Group: "deciders"}, "me")
	must(err)

	f, err := store.ParseFlow("new -> review\nreview -> merged | new\nmerged terminal")
	must(err)
	f.Name = "code"
	_, err = st.PutFlow(ctx, *f, "me")
	must(err)
	must(st.SetFlowPointer(ctx, "yard", "", "code", "me"))
	d, err := store.ParseFlow("open -> done\ndone terminal")
	must(err)
	d.Name = "decision"
	_, err = st.PutFlow(ctx, *d, "me")
	must(err)
	must(st.SetFlowPointer(ctx, "yard", "decision", "decision", "me"))

	for _, nb := range []store.NewBead{
		{Title: "a", Depot: "yard"}, {Title: "b", Depot: "yard"}, {Title: "c", Depot: "yard", Stage: "review"},
		{Title: "q", Depot: "yard", Type: "decision", Stage: "open"},
	} {
		_, err := st.CreateBead(ctx, nb, "me")
		must(err)
	}

	views, err := flowViews(ctx, st, "yard", "")
	must(err)
	if len(views) != 2 || views[0].Type != "" || views[1].Type != "decision" || views[1].Source.String() != "store, type decision" {
		t.Fatalf("views = %+v", views)
	}
	rv := views[0]
	want := []stageView{
		{Stage: "new", Group: "builders", Next: []string{"review"}, Open: 2},
		{Stage: "review", Next: []string{"merged", "new"}, Open: 1},
		{Stage: "merged", Next: []string{}, Terminal: true},
	}
	for i, w := range want {
		g := rv.Stages[i]
		if g.Stage != w.Stage || g.Group != w.Group || !slices.Equal(g.Next, w.Next) || g.Terminal != w.Terminal || g.Open != w.Open {
			t.Errorf("stage %d = %+v, want %+v", i, g, w)
		}
	}
	// review has no route; the terminal stage needs none.
	if len(rv.Warnings) != 1 || rv.Warnings[0] != "stage review has no route: beads there wait for a human" {
		t.Errorf("warnings = %v", rv.Warnings)
	}
	// The decision bead counts under its own flow, not the depot's.
	dv := views[1]
	if dv.Stages[0].Group != "deciders" || dv.Stages[0].Open != 1 || len(dv.Warnings) != 0 {
		t.Errorf("decision view = %+v", dv)
	}
	// Human stages list their members; a manual group without members is
	// still human, open to anyone.
	if !dv.Stages[0].Human || !slices.Equal(dv.Stages[0].Members, []string{"mckean", "yardmaster"}) {
		t.Errorf("deciders stage = %+v", dv.Stages[0])
	}
	if !rv.Stages[0].Human || rv.Stages[0].Members != nil || rv.Stages[1].Human {
		t.Errorf("depot stages = %+v", rv.Stages)
	}

	if _, err := flowViews(ctx, st, "nope", ""); err == nil {
		t.Error("unknown depot accepted")
	}

	// flow list: each named flow with what it governs; unused ones govern nothing.
	unused, err := store.ParseFlow("a -> b")
	must(err)
	unused.Name = "spare"
	_, err = st.PutFlow(ctx, *unused, "me")
	must(err)
	var warned bytes.Buffer
	rows, err := flowList(&warned, ctx, st)
	must(err)
	if warned.String() != "" {
		t.Errorf("flow list warned:\n%s", warned.String())
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r.Name] = r.Source + ": " + strings.Join(r.Governs, ", ")
	}
	wantRows := map[string]string{"code": "store: depot yard", "decision": "store: depot yard type decision", "spare": "store: "}
	if !maps.Equal(got, wantRows) {
		t.Errorf("flow list = %v, want %v", got, wantRows)
	}
}

// flow show says a siding under the table, a pair of lines for each stage
// whose manual group is one (yardr-tywp): what it folds by and where the
// carrier goes, then what waits there now and when it folds. A group that
// works two stages gets a pair under each with that stage's own count, a
// stage no edge leaves has the warning in place of the second line, and
// --json carries the same.
func TestFlowShowSiding(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.PutDepot(ctx, store.Depot{Name: "yard"}, "me"))
	members := []string{"mckean", "yardmaster"}
	// siding fills at 4 beads or 8h, at 6 points: all three at their defaults
	// but the first, which is stated. pairing works two stages and states all
	// three. lonely states one, and works a stage nothing leaves.
	for name, config := range map[string]map[string]any{
		"siding":  {"fold": 4},
		"pairing": {"fold": 3, "fold_after": "2h", "fold_max": 8},
		"lonely":  {"fold_max": 4},
		"people":  nil,
	} {
		must(st.PutGroup(ctx, store.Group{Name: name, Runner: "manual", Members: members, Limit: 9, Config: config}, "me"))
	}
	depot := "yard"
	for stage, group := range map[string]string{"siding": "siding", "triage": "pairing", "ready": "pairing", "dead": "lonely", "backlog": "people"} {
		_, err := st.AddRoute(ctx, store.Route{Stage: stage, Depot: &depot, Group: group}, "me")
		must(err)
	}
	f, err := store.ParseFlow("backlog -> siding | triage | dead\nsiding -> ready | backlog\ntriage -> ready\nready -> new on done | backlog on question\nnew -> merged\nmerged terminal")
	must(err)
	f.Name = "code"
	_, err = st.PutFlow(ctx, *f, "me")
	must(err)
	must(st.SetFlowPointer(ctx, "yard", "", "code", "me"))

	newBead := func(stage string, labels ...string) *store.Bead {
		t.Helper()
		b, err := st.CreateBead(ctx, store.NewBead{Title: "part", Depot: "yard", Stage: stage, Labels: labels}, "mckean")
		must(err)
		return b
	}
	// Three wait in the siding, weighing 1, 1 and 2. The first is held by the
	// siding's manual group, as the dispatcher has a manual group hold the
	// beads of its stage: nothing works it, so a fold would take it. The
	// second is held by a person, which a fold would leave. Two wait in
	// triage, weighing 1 and 2. The ready stage of the same group is empty,
	// and one bead waits in dead.
	_, err = st.Claim(ctx, newBead("siding", "size:small").ID, "siding", "siding", "yard")
	must(err)
	held := newBead("siding", "size:small")
	_, err = st.SetHold(ctx, held.ID, true, store.Expect{}, "me")
	must(err)
	newBead("siding", "size:medium")
	newBead("triage", "size:small")
	last := newBead("triage", "size:medium")
	newBead("dead")
	newBead("backlog")

	a := &app{st: st, actor: "me"}
	views, err := flowViews(ctx, st, "yard", "")
	must(err)
	if len(views) != 1 {
		t.Fatalf("views = %+v", views)
	}
	sidings := map[string]*sidingView{}
	for _, sv := range views[0].Stages {
		if sv.Siding != nil {
			sidings[sv.Stage] = sv.Siding
		}
	}
	for stage, want := range map[string]sidingView{
		"siding": {Fold: 4, FoldAfter: "8h", FoldMax: 6, To: "ready", Waiting: 3, Points: 4, Stuck: 1},
		"triage": {Fold: 3, FoldAfter: "2h", FoldMax: 8, To: "ready", Waiting: 2, Points: 3},
		"ready":  {Fold: 3, FoldAfter: "2h", FoldMax: 8, To: "new"},
		"dead":   {Fold: 4, FoldAfter: "8h", FoldMax: 4, Waiting: 1, Points: 2, Warning: "dead has fold=4 but no next stage; nothing will fold here"},
	} {
		got := sidings[stage]
		if got == nil {
			t.Errorf("stage %s is no siding in the view", stage)
			continue
		}
		at, shown := got.FoldsAt, *got
		shown.FoldsAt = nil
		if shown != want {
			t.Errorf("siding %s = %+v, want %+v", stage, shown, want)
		}
		// When it folds: the hours after the latest arrival, none with
		// nothing waiting.
		switch {
		case want.Waiting == 0 && at != nil:
			t.Errorf("siding %s folds at %v with nothing waiting", stage, at)
		case want.Waiting > 0 && at == nil:
			t.Errorf("siding %s has no time it folds at", stage)
		}
	}
	if len(sidings) != 4 {
		t.Errorf("sidings = %v, want siding, triage, ready and dead only", slices.Sorted(maps.Keys(sidings)))
	}
	// The clock runs from the latest bead to arrive.
	if at := sidings["triage"].FoldsAt; at == nil || !at.Equal(last.CreatedAt.Add(2*time.Hour)) {
		t.Errorf("triage folds at %v, want 2h after the last to arrive, %v", at, last.CreatedAt)
	}

	// The text: the pairs stand in the flow's order under the table, before
	// any warning of the flow's own.
	out, err := run(t, a.flowCmd(), "show", "yard")
	must(err)
	var backlog, ready string
	for line := range strings.Lines(out) {
		switch {
		case strings.HasPrefix(line, "backlog"):
			backlog = line
		case strings.HasPrefix(line, "ready"):
			ready = line
		}
	}
	if !strings.Contains(backlog, "(back)") {
		t.Errorf("backlog row = %q, want (back) in its NEXT", backlog)
	}
	if strings.Contains(ready, "(back)") {
		t.Errorf("ready row = %q, want no (back)", ready)
	}
	_, lines, found := strings.Cut(out, "(terminal: closes the bead)\n")
	if !found {
		t.Fatalf("flow show printed no table:\n%s", out)
	}
	clock := func(at *time.Time) string { return limits.Clock(*at, time.Now()) }
	want := "siding: folds at 4 beads, or 8h after the latest arrives; at most 6 points; the carrier goes to ready\n" +
		"        now 3 waiting, 4 points, 1 stuck; folds at 4 or at " + clock(sidings["siding"].FoldsAt) + "\n" +
		"triage: folds at 3 beads, or 2h after the latest arrives; at most 8 points; the carrier goes to ready\n" +
		"        now 2 waiting, 3 points; folds at 3 or at " + clock(sidings["triage"].FoldsAt) + "\n" +
		"dead: folds at 4 beads, or 8h after the latest arrives; at most 4 points\n" +
		"warning: dead has fold=4 but no next stage; nothing will fold here\n" +
		"ready: folds at 3 beads, or 2h after the latest arrives; at most 8 points; the carrier goes to new\n" +
		"       now 0 waiting, 0 points; folds at 3\n" +
		// Each manual stage past the first has no stage file here, so none
		// has an ask: they warn in the flow's order, before the stage with
		// no route.
		"warning: " + noAskWarning("siding") + "\n" +
		"warning: " + noAskWarning("triage") + "\n" +
		"warning: " + noAskWarning("dead") + "\n" +
		"warning: " + noAskWarning("ready") + "\n" +
		"warning: stage new has no route: beads there wait for a human\n"
	if lines != want {
		t.Errorf("under the table:\n%s\nwant\n%s", lines, want)
	}

	// --json carries the same, and no stage that is no siding has the key.
	var shown []struct {
		Stages []map[string]json.RawMessage `json:"stages"`
	}
	must(json.Unmarshal([]byte(mustAs(t, st, "me", "flow", "show", "yard", "--json")), &shown))
	var withKey []string
	for _, sv := range shown[0].Stages {
		if _, ok := sv["siding"]; ok {
			withKey = append(withKey, strings.Trim(string(sv["stage"]), `"`))
		}
	}
	if !slices.Equal(withKey, []string{"siding", "triage", "dead", "ready"}) {
		t.Errorf("stages with a siding in --json: %v", withKey)
	}
	var siding struct {
		Siding map[string]any `json:"siding"`
	}
	for _, sv := range shown[0].Stages {
		if strings.Trim(string(sv["stage"]), `"`) != "siding" {
			continue
		}
		whole, _ := json.Marshal(sv)
		must(json.Unmarshal(whole, &siding))
	}
	for key, want := range map[string]any{"fold": 4.0, "fold_after": "8h", "fold_max": 6.0, "to": "ready", "waiting": 3.0, "points": 4.0, "stuck": 1.0} {
		if siding.Siding[key] != want {
			t.Errorf("--json siding.%s = %v, want %v", key, siding.Siding[key], want)
		}
	}
	if _, ok := siding.Siding["folds_at"].(string); !ok {
		t.Errorf("--json siding.folds_at = %v, want a time", siding.Siding["folds_at"])
	}
}

// The lines under the table put the time a siding folds at as a person reads
// it on the day of now (the clock alone for today, with its day otherwise),
// say nothing of a time when nothing waits, and count in the singular.
func TestSidingLines(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 23, 7, 0, 0, 0, time.Local)
	at := func(day, hour, minute int) *time.Time {
		t := time.Date(2026, 10, day, hour, minute, 0, 0, time.Local)
		return &t
	}
	rows := []struct {
		name string
		view sidingView
		want []string
	}{
		{"today", sidingView{Fold: 4, FoldAfter: "8h", FoldMax: 6, To: "ready", Waiting: 3, Points: 4, FoldsAt: at(23, 9, 40)}, []string{
			"siding: folds at 4 beads, or 8h after the latest arrives; at most 6 points; the carrier goes to ready",
			"        now 3 waiting, 4 points; folds at 4 or at 09:40"}},
		{"another day", sidingView{Fold: 4, FoldAfter: "1d", FoldMax: 6, To: "ready", Waiting: 1, Points: 1, FoldsAt: at(24, 6, 5)}, []string{
			"siding: folds at 4 beads, or 1d after the latest arrives; at most 6 points; the carrier goes to ready",
			"        now 1 waiting, 1 point; folds at 4 or at Sat 24 Oct 06:05"}},
		{"stuck", sidingView{Fold: 2, FoldAfter: "30m", FoldMax: 2, To: "new", Waiting: 2, Points: 2, Stuck: 2, FoldsAt: at(23, 7, 30)}, []string{
			"siding: folds at 2 beads, or 30m after the latest arrives; at most 2 points; the carrier goes to new",
			"        now 2 waiting, 2 points, 2 stuck; folds at 2 or at 07:30"}},
		{"nothing waiting", sidingView{Fold: 4, FoldAfter: "8h", FoldMax: 6, To: "ready"}, []string{
			"siding: folds at 4 beads, or 8h after the latest arrives; at most 6 points; the carrier goes to ready",
			"        now 0 waiting, 0 points; folds at 4"}},
		{"no next stage", sidingView{Fold: 4, FoldAfter: "8h", FoldMax: 6, Waiting: 1, Points: 2, FoldsAt: at(23, 9, 40),
			Warning: "siding has fold=4 but no next stage; nothing will fold here"}, []string{
			"siding: folds at 4 beads, or 8h after the latest arrives; at most 6 points",
			"warning: siding has fold=4 but no next stage; nothing will fold here"}},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			if got := sidingLines("siding", &r.view, now); !slices.Equal(got, r.want) {
				t.Errorf("lines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(r.want, "\n"))
			}
		})
	}
}

// flow set -f - reads the flow from the command's stdin.
func TestFlowSetStdin(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := context.Background()
	a := &app{st: st, actor: "me"}
	c := a.flowCmd()
	c.SetIn(strings.NewReader("a -> b\nb terminal\n"))
	if out, err := run(t, c, "set", "pipe", "-f", "-"); err != nil {
		t.Fatalf("flow set -f -: %v\n%s", err, out)
	}
	f, err := st.GetFlow(ctx, "pipe")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.Stages, []string{"a", "b"}) {
		t.Fatalf("flow stages = %+v", f.Stages)
	}
}

// A stage a manual group serves, past the flow's first and not terminal, whose
// stage file has no ## Ask puts a card in no one's Decide: flow show of the
// depot and of the flow, and flow set, warn of it (yardr-v0dv), in the same
// words, and never refuse. A backlog, a stage a script or agents work, a
// terminal stage, a stage no route takes (its own warning says so) and a stage
// with an ask do not, and a broken ask has the warning of its own.
func TestFlowWarnsOfAManualStageWithNoAsk(t *testing.T) {
	t.Parallel()
	const text = "backlog -> approve\napprove -> merged | backlog\nmerged terminal"
	const ask = "Approve it.\n\n## Ask\nkind: moves\nquestion: Ship it?\ncontext:\n- The release waits for you.\n"
	const broken = "Approve it.\n\n## Ask\nkind: choce\nquestion: Ship it?\ncontext:\n- The release waits for you.\n"
	groups := []store.Group{
		{Name: "people", Runner: "manual"},
		{Name: "script", Runner: "exec", Limit: 1, Config: map[string]any{"cmd": "/srv/step"}},
		{Name: "robots", Runner: "herdr", Config: map[string]any{"kind": "claude"}},
		{Name: "spare", Runner: "herdr", Config: map[string]any{"kind": "claude"}},
	}
	// noAsk reads the stages the warnings among lines name.
	noAsk := func(lines string) []string {
		var out []string
		for line := range strings.Lines(lines) {
			line = strings.TrimSuffix(strings.TrimPrefix(line, "warning: "), "\n")
			if rest, ok := strings.CutPrefix(line, "stage "); ok && strings.Contains(rest, " waits for a person but has no ## Ask: ") {
				name, _, _ := strings.Cut(rest, " ")
				out = append(out, name)
			}
		}
		return out
	}
	for _, c := range []struct {
		name    string
		routes  []store.Route // by stage and group: the case gives them its depot
		file    string        // the approve stage's file, none when empty
		nodepot bool          // no depot follows the flow, and its routes name none
		want    []string      // the stages that warn
	}{
		{name: "a manual stage past the first with no stage file", routes: []store.Route{{Stage: "approve", Group: "people"}}, want: []string{"approve"}},
		{name: "a stage file with no ask", routes: []store.Route{{Stage: "approve", Group: "people"}}, file: "Approve it.\n", want: []string{"approve"}},
		{name: "a stage file with an ask", routes: []store.Route{{Stage: "approve", Group: "people"}}, file: ask},
		{name: "a broken ask has its own warning", routes: []store.Route{{Stage: "approve", Group: "people"}}, file: broken},
		{name: "the flow's first stage, a backlog", routes: []store.Route{{Stage: "backlog", Group: "people"}, {Stage: "approve", Group: "robots"}}},
		{name: "a stage a script works", routes: []store.Route{{Stage: "approve", Group: "script"}}},
		{name: "a stage agents work", routes: []store.Route{{Stage: "approve", Group: "robots"}}},
		{name: "a stage agents work, with a group after else", routes: []store.Route{{Stage: "approve", Group: "robots", Else: []store.RouteGroup{{Name: "spare"}}}}},
		{name: "a terminal stage a manual group serves", routes: []store.Route{{Stage: "approve", Group: "robots"}, {Stage: "merged", Group: "people"}}},
		{name: "a stage no route takes", routes: nil},
		{name: "a flow no depot follows reads the routes that name no depot", routes: []store.Route{{Stage: "approve", Group: "people"}}, nodepot: true, want: []string{"approve"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			st := storetest.New(t)
			ctx := context.Background()
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			home := t.TempDir()
			if c.file != "" {
				dir := store.HomeStageDir(home, "release")
				must(os.MkdirAll(dir, 0o755))
				must(os.WriteFile(filepath.Join(dir, "approve.md"), []byte(c.file), 0o644))
			}
			a := &app{st: st, actor: "me", cfg: &config.Config{Home: home}}
			must(st.PutDepot(ctx, store.Depot{Name: "yard"}, "me"))
			for _, g := range groups {
				must(st.PutGroup(ctx, g, "me"))
			}
			depot := "yard"
			for _, r := range c.routes {
				if !c.nodepot {
					r.Depot = &depot
				}
				_, err := st.AddRoute(ctx, r, "me")
				must(err)
			}
			f, err := store.ParseFlow(text)
			must(err)
			f.Name = "release"
			_, err = st.PutFlow(ctx, *f, "me")
			must(err)
			if !c.nodepot {
				must(st.SetFlowPointer(ctx, "yard", "", "release", "me"))
			}
			check := func(where, got string) {
				t.Helper()
				if stages := noAsk(got); !slices.Equal(stages, c.want) {
					t.Errorf("%s warns of stages %v, want %v:\n%s", where, stages, c.want, got)
				}
			}
			// flow show <depot>, in the view and as printed.
			if !c.nodepot {
				views, err := flowViews(ctx, st, "yard", home)
				must(err)
				if len(views) != 1 {
					t.Fatalf("views = %+v", views)
				}
				check("the view of depot yard", strings.Join(views[0].Warnings, "\n"))
				out, err := run(t, a.flowCmd(), "show", "yard")
				must(err)
				check("flow show yard", out)
			}
			// flow show <flow>, as printed and in --json.
			out, err := run(t, a.flowCmd(), "show", "release")
			must(err)
			check("flow show release", out)
			a.json = true
			out, err = run(t, a.flowCmd(), "show", "release")
			a.json = false
			must(err)
			var shown struct {
				Stages []string `json:"stages_without_ask"`
			}
			must(json.Unmarshal([]byte(out), &shown))
			if !slices.Equal(shown.Stages, c.want) {
				t.Errorf("flow show release --json has stages_without_ask %v, want %v", shown.Stages, c.want)
			}
			// flow set puts the warnings beside its result, on the error
			// stream, so that --json stays the flow.
			var want strings.Builder
			for _, s := range c.want {
				want.WriteString("warning: " + noAskWarning(s) + "\n")
			}
			for _, asJSON := range []bool{false, true} {
				set := a.flowCmd()
				var errs bytes.Buffer
				set.SetErr(&errs)
				set.SetIn(strings.NewReader(text))
				a.json = asJSON
				out, err := run(t, set, "set", "release", "-f", "-")
				a.json = false
				must(err)
				if errs.String() != want.String() {
					t.Errorf("flow set (json %v) says %q on its error stream, want %q", asJSON, errs.String(), want.String())
				}
				if strings.Contains(out, "warning") {
					t.Errorf("flow set (json %v) prints a warning on its output: %q", asJSON, out)
				}
			}
		})
	}
}

// flow show <name> shows a repository named flow by name, not only a store
// one (yardr-kq2c): every depot whose repository carries the file, each
// under its own depot heading, depots sorted, in the view flow show
// <depot> prints for that flow — the governing one when a pointer names
// it, the file itself when nothing does yet. A name nowhere keeps the
// store miss's error, and a store flow still shows with who points at it.
func TestFlowShowNamedRepoFlow(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	home := t.TempDir()
	a := &app{st: st, actor: "me", cfg: &config.Config{Home: home}}
	const text = "draft -> review\nreview -> merged\nmerged terminal\n"
	write := func(dir, name, text string) {
		t.Helper()
		p := filepath.Join(dir, ".yardr", "flows", name)
		must(os.MkdirAll(filepath.Dir(p), 0o755))
		must(os.WriteFile(p, []byte(text), 0o644))
	}
	// Dir depots carry the flow files on disk, no git: three depots with
	// the same flow name, and a stage file in alpha's repository alone.
	aDir, bDir, gDir, gitDir := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	for _, dir := range []string{aDir, bDir, gDir} {
		write(dir, "posting.post.flow", text)
	}
	write(aDir, filepath.Join("posting.post", "draft.md"), "Draft it.\n")
	write(gDir, filepath.Join("posting.post", "orphan.md"), "Not a stage.\n")
	write(gDir, filepath.Join("unrelated", "orphan.md"), "Not this flow.\n")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", gitDir, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	write(gitDir, "posting.post.flow", text)
	write(gitDir, filepath.Join("posting.post", "draft.md"), "Draft it.\n")
	git("add", "-A")
	git("commit", "-q", "-m", "add posting flow")
	must(st.PutDepot(ctx, store.Depot{Name: "alpha", Path: aDir, Kind: store.DepotDir}, "me"))
	must(st.PutDepot(ctx, store.Depot{Name: "beta", Path: bDir, Kind: store.DepotDir}, "me"))
	must(st.PutDepot(ctx, store.Depot{Name: "gamma", Path: gDir, Kind: store.DepotDir}, "me"))
	must(st.PutDepot(ctx, store.Depot{Name: "git-source", Path: gitDir, Base: "main", Kind: store.DepotGit}, "me"))
	// Alpha's post beads follow the file by a type pointer, beta's beads
	// by the depot's own, and git-source's by its type pointer; gamma
	// points nothing at it yet.
	must(st.SetFlowPointer(ctx, "alpha", "post", "posting.post", "me"))
	must(st.SetFlowPointer(ctx, "beta", "", "posting.post", "me"))
	must(st.SetFlowPointer(ctx, "git-source", "post", "posting.post", "me"))
	must(st.PutGroup(ctx, store.Group{Name: "crew", Runner: "manual", Members: []string{"ada"}}, "me"))
	_, err := st.AddRoute(ctx, store.Route{Stage: "draft", Group: "crew"}, "me")
	must(err)

	// The pointed depots' sections are exactly the views flow show
	// <depot> prints for the flow, under depot headings, sorted; gamma's
	// section shows the unpointed file, from its own repository.
	showAlpha, err := run(t, a.flowCmd(), "show", "alpha")
	must(err)
	showBeta, err := run(t, a.flowCmd(), "show", "beta")
	must(err)
	showGit, err := run(t, a.flowCmd(), "show", "git-source")
	must(err)
	out, err := run(t, a.flowCmd(), "show", "posting.post")
	must(err)
	heads := []string{"depot alpha:\n" + showAlpha, "depot beta:\n" + showBeta}
	if !strings.HasPrefix(out, heads[0]+"\n"+heads[1]+"\ndepot gamma:\n") {
		t.Fatalf("flow show posting.post:\n%s\nwant the alpha and beta depot views, then gamma and git-source", out)
	}
	rest := out[len(heads[0]+"\n"+heads[1]+"\ndepot gamma:\n"):]
	if !strings.HasPrefix(rest, "flow posting.post (repo (directory))") {
		t.Errorf("gamma shows %q, want the repository flow with its stages", strings.SplitN(rest, "\n", 2)[0])
	}
	for _, stage := range []string{"draft", "review", "merged"} {
		if !strings.Contains(rest, stage) {
			t.Errorf("gamma view lacks stage %s:\n%s", stage, rest)
		}
	}
	if !strings.Contains(rest, "stage file .yardr/flows/posting.post/orphan.md") {
		t.Errorf("gamma view lacks its stray stage-file warning:\n%s", rest)
	}
	if strings.Contains(rest, ".yardr/flows/unrelated/orphan.md") {
		t.Errorf("gamma view includes another flow's stray stage-file warning:\n%s", rest)
	}
	if !strings.Contains(out, "depot git-source:\n"+showGit) {
		t.Errorf("flow show posting.post's git section differs from flow show git-source:\n%s", out)
	}
	// The FILE column reads each depot's own repository: alpha's draft
	// has a stage file, beta's and gamma's do not.
	fileOf := func(section, stage string) string {
		t.Helper()
		for line := range strings.Lines(section) {
			if f := strings.Fields(line); len(f) >= 6 && f[0] == stage {
				return f[4]
			}
		}
		t.Fatalf("no %s row:\n%s", stage, section)
		return ""
	}
	sections := strings.Split(out, "depot ")
	if len(sections) != 5 || !strings.HasPrefix(sections[1], "alpha:") ||
		!strings.HasPrefix(sections[2], "beta:") || !strings.HasPrefix(sections[3], "gamma:") ||
		!strings.HasPrefix(sections[4], "git-source:") {
		t.Fatalf("want four depot-headed sections, sorted:\n%s", out)
	}
	if got := []string{fileOf(sections[1], "draft"), fileOf(sections[2], "draft"), fileOf(sections[3], "draft"), fileOf(sections[4], "draft")}; !slices.Equal(got, []string{"yes", "-", "-", "yes"}) {
		t.Errorf("draft FILE = %v, want [yes - - yes]:\n%s", got, out)
	}
	// The crew works draft in every depot's view, pointed or not.
	for i, section := range sections[1:] {
		if !strings.Contains(section, "crew") || !strings.Contains(section, "ada") {
			t.Errorf("section %d names no crew:\n%s", i, section)
		}
	}
	if _, err := run(t, a.flowCmd(), "show", "posting.post", "--svg"); err == nil || !strings.Contains(err.Error(), "depots alpha, beta, gamma, git-source") {
		t.Errorf("flow show posting.post --svg err = %v, want the sorted depot choices", err)
	}
	soloDir := t.TempDir()
	write(soloDir, "solo.flow", text)
	must(st.PutDepot(ctx, store.Depot{Name: "source", Path: soloDir, Kind: store.DepotDir}, "me"))
	svg, err := run(t, a.flowCmd(), "show", "solo", "--svg")
	must(err)
	if !strings.HasPrefix(strings.TrimSpace(svg), "<svg") {
		t.Errorf("flow show solo --svg = %q, want an SVG", svg)
	}

	// --json serves each depot beside its view, sorted.
	a.json = true
	out, err = run(t, a.flowCmd(), "show", "posting.post")
	a.json = false
	must(err)
	var shown []namedFlowView
	must(json.Unmarshal([]byte(out), &shown))
	if len(shown) != 4 || shown[0].Depot != "alpha" || shown[1].Depot != "beta" || shown[2].Depot != "gamma" || shown[3].Depot != "git-source" {
		t.Fatalf("--json depots = %v, want [alpha beta gamma git-source]:\n%s", shown, out)
	}
	for _, v := range shown {
		if v.Flow.Name != "posting.post" || len(v.Stages) != 3 {
			t.Errorf("--json view = %+v, want the posting.post flow with 3 stages", v)
		}
	}

	// A store flow still shows with who points at it.
	f, err := store.ParseFlow("new -> merged\nmerged terminal")
	must(err)
	f.Name = "code"
	_, err = st.PutFlow(ctx, *f, "me")
	must(err)
	out, err = run(t, a.flowCmd(), "show", "code")
	must(err)
	if !strings.Contains(out, "flow code: 2 stages, pointed at by nothing") {
		t.Errorf("flow show code:\n%s", out)
	}

	// A name nowhere keeps today's error.
	_, err = run(t, a.flowCmd(), "show", "nosuch")
	if err == nil || !strings.Contains(err.Error(), "nosuch is neither a depot nor a named flow") {
		t.Errorf("flow show nosuch err = %v, want the neither-a-depot error", err)
	}
}
