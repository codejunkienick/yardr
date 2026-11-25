package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const (
	copyDefault = "backlog -> new\nnew -> review | backlog\nreview -> done | new\ndone terminal\n"
	copyRelease = "plan -> ship\nship -> done\ndone terminal\n"
	copyWagon   = "wait -> ride\nride -> done\ndone terminal\n"
	copyTrain   = "load -> go\ngo -> done\ndone terminal\n"
)

// writeFlowFiles writes files under the .yardr/flows of a depot's directory:
// the keys are paths there (default.flow, type/wagon.flow, default/new.md).
func writeFlowFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		p := filepath.Join(dir, RepoFlowDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// dirDepotWith registers the dir depot name, whose directory holds files as
// writeFlowFiles puts them, and answers the directory.
func (y *askYard) dirDepotWith(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	writeFlowFiles(t, dir, files)
	if err := y.PutDepot(y.ctx, Depot{Name: name, Kind: DepotDir, Path: dir}, "mckean"); err != nil {
		t.Fatal(err)
	}
	return dir
}

// gitDepotWith registers the git depot name, whose repository holds files on
// its base main, committed.
func (y *askYard) gitDepotWith(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	writeFlowFiles(t, dir, files)
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "flows")
	if err := y.PutDepot(y.ctx, Depot{Name: name, Path: dir, Base: "main"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	return dir
}

// copyFlow stores the flow name from its text, as the operator.
func (y *askYard) copyFlow(t *testing.T, name, text string) {
	t.Helper()
	f, err := ReadFlow(name, text)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := y.PutFlow(y.ctx, *f, "mckean"); err != nil {
		t.Fatal(err)
	}
}

// homeFile is the stage file of stage in the store flow flow as the disk has
// it: its text and its mode, and "" and 0 where none stands.
func (y *askYard) homeFile(t *testing.T, flow, stage string) (string, os.FileMode) {
	t.Helper()
	p := filepath.Join(y.home, "flows", flow, filepath.FromSlash(stage)+".md")
	raw, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return "", 0
	} else if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw), info.Mode().Perm()
}

// copyState is what a copy may change, as one string: the store's flows
// and pointers, the files and directories under the home's flows with what
// they hold, and how many flow events there are. A refused copy leaves it as
// it found it.
func (y *askYard) copyState(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	flows, err := y.ListFlows(y.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range flows {
		fmt.Fprintf(&b, "flow %s %v %v\n", f.Name, f.Stages, f.Transitions)
	}
	ptrs, err := y.FlowPointers(y.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ptrs {
		fmt.Fprintf(&b, "pointer %s|%s|%s\n", p.Depot, p.Type, p.Flow)
	}
	err = filepath.WalkDir(filepath.Join(y.home, "flows"), func(p string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		} else if err != nil {
			return err
		}
		rel, _ := filepath.Rel(y.home, p)
		if d.IsDir() {
			fmt.Fprintf(&b, "dir %s\n", rel)
			return nil
		}
		raw, err := os.ReadFile(p)
		fmt.Fprintf(&b, "file %s %q %v\n", rel, raw, err)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"flow_set", "flow_pointer_set"} {
		evs, err := y.EventsOfKind(y.ctx, kind, 200)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "%s %d\n", kind, len(evs))
	}
	return b.String()
}

// refuses holds the copy of depot, asked as actor, to a refusal that says
// want and to no change at all.
func (y *askYard) copyRefused(t *testing.T, depot, actor, want string) {
	t.Helper()
	before := y.copyState(t)
	if res, err := y.CopyRepoFlows(y.ctx, depot, actor); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("CopyRepoFlows(%s) as %s = %+v, %v, want it refused with %q", depot, actor, res, err, want)
	}
	if after := y.copyState(t); after != before {
		t.Errorf("a refused copy changed the yard:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// A git depot's default, type and stage files are copied together, with open
// beads of two types in stages only their own type's flow has: every pointer
// stands before any bead is looked at, or the default's copy alone would
// strand the wagon and the release. Each flow is the repository's, as the
// store has it, its stage files are plain files at rw-r--r--, and one with
// nothing in it or named after no stage is no copy.
func TestCopyRepoFlowsOfAGitDepotWithOpenBeads(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	dir := y.gitDepotWith(t, "web", map[string]string{
		"default.flow": copyDefault, "type/release.flow": copyRelease, "type/wagon.flow": copyWagon,
		"default/new.md": "Build it.\n", "default/review.md": "Review it.\n", "default/done.md": " \n",
		"type/release/ship.md": "Ship it.\n", "type/wagon/ride.md": "Ride it.\n", "type/wagon/stray.md": "No stage of the flow.\n",
	})
	var open []string
	for _, typ := range []string{"wagon", "release", ""} {
		b, err := y.CreateBead(y.ctx, NewBead{Title: "open " + typ, Depot: "web", Type: typ}, "mckean")
		if err != nil {
			t.Fatal(err)
		}
		open = append(open, b.ID)
	}

	res, err := y.CopyRepoFlows(y.ctx, "web", "mckean")
	if err != nil {
		t.Fatal(err)
	}
	sha := gitRun(t, dir, "rev-parse", "HEAD")
	want := &CopiedFlows{Depot: "web", Source: "repo@" + sha[:7], Flows: []CopiedFlow{
		{From: ".yardr/flows/default.flow", Flow: "web.default", StageFiles: 2, Back: "yardr depot source web flow depot"},
		{Type: "release", From: ".yardr/flows/type/release.flow", Flow: "web.release", StageFiles: 1, Back: "yardr depot source web flow --type release depot"},
		{Type: "wagon", From: ".yardr/flows/type/wagon.flow", Flow: "web.wagon", StageFiles: 1, Back: "yardr depot source web flow --type wagon depot"},
	}}
	if !reflect.DeepEqual(res, want) {
		t.Errorf("CopyRepoFlows = %+v, want %+v", res, want)
	}

	for name, text := range map[string]string{"web.default": copyDefault, "web.release": copyRelease, "web.wagon": copyWagon} {
		want, err := ReadFlow(name, text)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := y.GetFlow(y.ctx, name); err != nil || got.Name != name || !flowsEqual(*got, *want) {
			t.Errorf("the store has %+v, %v for %s, want the repository's flow", got, err, name)
		}
	}
	ptrs, err := y.FlowPointers(y.ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantPtrs := []FlowPointer{{Depot: "web", Flow: "web.default"}, {Depot: "web", Type: "release", Flow: "web.release"}, {Depot: "web", Type: "wagon", Flow: "web.wagon"}}
	if !slices.Equal(ptrs, wantPtrs) {
		t.Errorf("the pointers are %+v, want %+v", ptrs, wantPtrs)
	}
	for flow, stages := range map[string]map[string]string{
		"web.default": {"new": "Build it.\n", "review": "Review it.\n", "done": ""},
		"web.release": {"ship": "Ship it.\n"},
		"web.wagon":   {"ride": "Ride it.\n", "stray": ""},
	} {
		for stage, text := range stages {
			mode := os.FileMode(0o644)
			if text == "" {
				mode = 0
			}
			if got, gotMode := y.homeFile(t, flow, stage); got != text || gotMode != mode {
				t.Errorf("the stage file of %s in %s holds %q at %v, want %q at %v", stage, flow, got, gotMode, text, mode)
			}
		}
	}
	// What governs now is the store's, whatever the repository says.
	flows, err := y.DepotFlows(y.ctx, "web")
	if err != nil {
		t.Fatal(err)
	}
	var governing []string
	for _, df := range flows {
		if !df.Source.InStore() || len(df.StageFiles) != 0 {
			t.Errorf("%s still governs from %+v", df.Flow.Name, df.Source)
		}
		governing = append(governing, df.Type+"="+df.Flow.Name)
	}
	if !slices.Equal(governing, []string{"=web.default", "release=web.release", "wagon=web.wagon"}) {
		t.Errorf("the flows that govern are %v", governing)
	}
	if stranded, err := y.Stranded(y.ctx, "web"); err != nil || len(stranded) != 0 {
		t.Errorf("the copy stranded %v, %v of %v", stranded, err, open)
	}
	// One flow_set and one flow_pointer_set for each flow, as the operator.
	for kind, key := range map[string]string{"flow_set": "flow", "flow_pointer_set": "flow"} {
		evs, err := y.EventsOfKind(y.ctx, kind, 50)
		if err != nil {
			t.Fatal(err)
		}
		var flows []string
		for _, e := range evs {
			if e.Actor != "mckean" {
				t.Errorf("%s was recorded by %s", kind, e.Actor)
			}
			if kind == "flow_pointer_set" && e.Data["depot"] != "web" {
				t.Errorf("%s names depot %v", kind, e.Data["depot"])
			}
			flows = append(flows, fmt.Sprint(e.Data[key]))
		}
		slices.Sort(flows)
		if !slices.Equal(flows, []string{"web.default", "web.release", "web.wagon"}) {
			t.Errorf("%s events name %v", kind, flows)
		}
	}
	// Nothing is left to copy: every flow that governs the depot is the store's.
	y.copyRefused(t, "web", "mckean", "every flow that governs depot web is in the store already (default (flow web.default), type/release (flow web.release), type/wagon (flow web.wagon))")
}

// A repository flow a pointer leads to is copied as its type's, under a name
// that leaves the repository's own flow its name: the live patterns are a
// named flow under the depot's own name (yardr's train, copied beside it as
// .local), one under a name of its own (box), and one where the depot's own
// flow is the store's already (post, with the default kept), and a depot
// whose repository has a flow named like the copy. A pin that names the
// repository's flow still reads it from there.
func TestCopyRepoFlowsOfNamedRepositoryFlows(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, depot, pin string
		files            map[string]string
		stored           map[string]string
		pointers         [][3]string // depot, type, flow
		want             []CopiedFlow
		kept             []string
		governing        []string
		home             map[string]string // "<flow>/<stage>" -> text
	}{{
		name: "a pointer to a flow named like its copy", depot: "yardr", pin: "yardr.train",
		files:    map[string]string{"default.flow": copyDefault, "yardr.train.flow": copyTrain, "default/new.md": "Build it.\n", "yardr.train/go.md": "Go.\n"},
		pointers: [][3]string{{"yardr", "train", "yardr.train"}},
		want: []CopiedFlow{
			{From: ".yardr/flows/default.flow", Flow: "yardr.default", StageFiles: 1, Back: "yardr depot source yardr flow depot"},
			{Type: "train", From: ".yardr/flows/yardr.train.flow", Flow: "yardr.train.local", StageFiles: 1, Back: "yardr depot flow yardr --type train yardr.train"},
		},
		governing: []string{"=yardr.default", "train=yardr.train.local"},
		home:      map[string]string{"yardr.default/new": "Build it.\n", "yardr.train.local/go": "Go.\n"},
	}, {
		name: "a pointer to a flow of its own name", depot: "crates", pin: "box",
		files:     map[string]string{"box.flow": copyRelease, "box/ship.md": "Ship it.\n"},
		pointers:  [][3]string{{"crates", "box", "box"}},
		want:      []CopiedFlow{{Type: "box", From: ".yardr/flows/box.flow", Flow: "crates.box", StageFiles: 1, Back: "yardr depot flow crates --type box box"}},
		governing: []string{"box=crates.box"},
		home:      map[string]string{"crates.box/ship": "Ship it.\n"},
	}, {
		name: "a depot whose own flow is the store's", depot: "mailroom", pin: "outbox.post",
		files:     map[string]string{"default.flow": copyDefault, "outbox.post.flow": copyWagon, "outbox.post/ride.md": "Post it.\n", "default/new.md": "Not copied.\n"},
		stored:    map[string]string{"starter.default": copyTrain},
		pointers:  [][3]string{{"mailroom", "", "starter.default"}, {"mailroom", "post", "outbox.post"}},
		want:      []CopiedFlow{{Type: "post", From: ".yardr/flows/outbox.post.flow", Flow: "mailroom.post", StageFiles: 1, Back: "yardr depot flow mailroom --type post outbox.post"}},
		kept:      []string{"default"},
		governing: []string{"=starter.default", "post=mailroom.post"},
		home:      map[string]string{"mailroom.post/ride": "Post it.\n"},
	}, {
		name: "a depot whose repository has a flow named like the copy", depot: "web", pin: "web.default",
		files:     map[string]string{"default.flow": copyDefault, "web.default.flow": copyRelease, "default/new.md": "Build it.\n"},
		want:      []CopiedFlow{{From: ".yardr/flows/default.flow", Flow: "web.default.local", StageFiles: 1, Back: "yardr depot source web flow depot"}},
		governing: []string{"=web.default.local"},
		home:      map[string]string{"web.default.local/new": "Build it.\n"},
	}} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			y := listYard(t)
			y.dirDepotWith(t, c.depot, c.files)
			for name, text := range c.stored {
				y.copyFlow(t, name, text)
			}
			for _, p := range c.pointers {
				if err := y.SetFlowPointer(y.ctx, p[0], p[1], p[2], "mckean"); err != nil {
					t.Fatal(err)
				}
			}
			res, err := y.CopyRepoFlows(y.ctx, c.depot, "mckean")
			if err != nil {
				t.Fatal(err)
			}
			if want := (&CopiedFlows{Depot: c.depot, Source: "repo (directory)", Flows: c.want, Kept: c.kept}); !reflect.DeepEqual(res, want) {
				t.Errorf("CopyRepoFlows = %+v, want %+v", res, want)
			}
			flows, err := y.DepotFlows(y.ctx, c.depot)
			if err != nil {
				t.Fatal(err)
			}
			var governing []string
			for _, df := range flows {
				if !df.Source.InStore() {
					t.Errorf("%s still governs from %+v", df.Flow.Name, df.Source)
				}
				governing = append(governing, df.Type+"="+df.Flow.Name)
			}
			if !slices.Equal(governing, c.governing) {
				t.Errorf("the flows that govern are %v, want %v", governing, c.governing)
			}
			for place, text := range c.home {
				flow, stage, _ := strings.Cut(place, "/")
				if got, mode := y.homeFile(t, flow, stage); got != text || mode != 0o644 {
					t.Errorf("the stage file of %s in %s holds %q at %v, want %q at rw-r--r--", stage, flow, got, mode, text)
				}
			}
			// The repository's flow keeps its name, and so for whoever pins it.
			b, err := y.CreateBead(y.ctx, NewBead{Title: "pinned", Depot: c.depot, Metadata: map[string]any{"flow": c.pin}}, "mckean")
			if err != nil {
				t.Fatal(err)
			}
			if next, err := y.NextStages(y.ctx, b.ID); err != nil || next.Flow == nil || next.Flow.Name != c.pin || next.Source.InStore() || next.Source.Kind != "pin" {
				t.Errorf("a bead pinned to %s is governed by %+v from %+v, %v, want the repository's flow", c.pin, next.Flow, next.Source, err)
			}
			y.copyRefused(t, c.depot, "mckean", "is in the store already")
		})
	}
}

// Going back (the command of each copy's Back) clears the pointer and leaves
// the copy, in the store and in the home, unused; copying again replaces it:
// the flow as the repository has it now, its stage files written over, and
// the files of the old copy that the new one has none for gone, a file that
// is no stage file left alone. A copy that is followed, by a depot's
// pointer or an open bead's pin, or that a pack manages, is no leftover: the
// whole copy is refused, naming who, and nothing is written.
func TestCopyRepoFlowsAfterSwitchingBackReplacesTheEarlierCopy(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	dir := y.dirDepotWith(t, "web", map[string]string{
		"default.flow": copyDefault, "type/wagon.flow": copyWagon,
		"default/new.md": "Build it.\n", "default/review.md": "Review it.\n", "type/wagon/ride.md": "Ride it.\n",
	})
	first, err := y.CopyRepoFlows(y.ctx, "web", "mckean")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range first.Flows {
		if f.Replaced {
			t.Errorf("the first copy of %s replaced something", f.Flow)
		}
	}
	// Back, the depot's before the type's, as the copy says.
	back := func() {
		t.Helper()
		for _, p := range [][2]string{{"", ""}, {"wagon", ""}} {
			if err := y.SetFlowPointer(y.ctx, "web", p[0], p[1], "mckean"); err != nil {
				t.Fatal(err)
			}
		}
	}
	back()
	if _, err := y.GetFlow(y.ctx, "web.default"); err != nil {
		t.Errorf("going back removed the copy: %v", err)
	}
	// What the earlier copy holds beside the repository's files, and the
	// repository since: another stage, new text, a stage file gone.
	home := filepath.Join(y.home, "flows", "web.default")
	for name, text := range map[string]string{"stale.md": "Old.\n", "qa/old.md": "Old qa.\n", "notes.txt": "Mine.\n"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(home, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeFlowFiles(t, dir, map[string]string{
		"default.flow":   "backlog -> new\nnew -> review | backlog\nreview -> qa | new\nqa -> done\ndone terminal\n",
		"default/new.md": "Build it again.\n", "default/qa.md": "Check it.\n",
	})
	if err := os.Remove(filepath.Join(dir, RepoFlowDir, "default", "review.md")); err != nil {
		t.Fatal(err)
	}

	res, err := y.CopyRepoFlows(y.ctx, "web", "mckean")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Flows) != 2 || !res.Flows[0].Replaced || !res.Flows[1].Replaced || res.Flows[0].StageFiles != 2 || res.Flows[1].StageFiles != 1 {
		t.Errorf("the second copy = %+v, want two flows, replaced, of 2 stage files and 1", res.Flows)
	}
	if f, err := y.GetFlow(y.ctx, "web.default"); err != nil || !slices.Contains(f.Stages, "qa") {
		t.Errorf("web.default is %+v, %v, want the repository's flow as it is now", f, err)
	}
	for stage, text := range map[string]string{"new": "Build it again.\n", "qa": "Check it.\n", "review": "", "stale": "", "qa/old": ""} {
		if got, mode := y.homeFile(t, "web.default", stage); got != text || (text != "" && mode != 0o644) {
			t.Errorf("the stage file of %s in web.default holds %q at %v, want %q", stage, got, mode, text)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(home, "notes.txt")); err != nil || string(raw) != "Mine.\n" {
		t.Errorf("notes.txt, no stage file, holds %q, %v: the copy left it alone", raw, err)
	}

	// In use: the whole copy is refused while any of its flows is, and the
	// flows that were free are not written either (copyRefused).
	back()
	y.dirDepotWith(t, "other", nil)
	if err := y.SetFlowPointer(y.ctx, "other", "", "web.wagon", "mckean"); err != nil {
		t.Fatal(err)
	}
	y.copyRefused(t, "web", "mckean", "flow web.wagon stands in the store and is in use by depot other: a copy would write over a flow they follow, so nothing is copied")
	if err := y.SetFlowPointer(y.ctx, "other", "", "", "mckean"); err != nil {
		t.Fatal(err)
	}
	pinned, err := y.CreateBead(y.ctx, NewBead{Title: "pinned", Depot: "other", Metadata: map[string]any{"flow": "web.default"}}, "mckean")
	if err != nil {
		t.Fatal(err)
	}
	y.copyRefused(t, "web", "mckean", "flow web.default stands in the store and is in use by bead "+pinned.ID)
	// A closed bead follows nothing.
	if _, err := y.CloseBead(y.ctx, pinned.ID, "done", Expect{}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if res, err = y.CopyRepoFlows(y.ctx, "web", "mckean"); err != nil || !res.Flows[0].Replaced {
		t.Fatalf("the copy over a copy a closed bead pinned = %+v, %v", res, err)
	}
	back()
	// A pack's flow is the pack's, not a leftover.
	if _, err := y.RecordPack(y.ctx, PackImport{Pack: "starter", Depot: "web", Objects: []PackObject{{Kind: "flow", Name: "web.default"}}}, true, "mckean"); err != nil {
		t.Fatal(err)
	}
	y.copyRefused(t, "web", "mckean", "flow web.default stands in the store and is the pack starter's, imported into depot web")
}

// What a copy may not do it refuses before it has changed anything, in the
// store or on disk: a depot that is none or has nothing to copy, a governing
// file that does not parse (one a depot's pointer stands in front of is read
// by nobody, and neither is it copied), two files to one name, and a link or
// a file where the copy would write or remove.
func TestCopyRepoFlowsRefusals(t *testing.T) {
	t.Parallel()
	flows := map[string]string{"default.flow": copyDefault, "default/new.md": "Build it.\n"}
	with := func(more map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range flows {
			out[k] = v
		}
		for k, v := range more {
			out[k] = v
		}
		return out
	}
	elsewhere := func(t *testing.T) string { return t.TempDir() }
	for _, c := range []struct {
		name  string
		depot string
		files map[string]string
		setup func(t *testing.T, y *askYard)
		want  string
	}{
		{name: "a depot that is none", depot: "ghost", want: "depot ghost: not found"},
		{name: "no flow in the repository", depot: "web", want: "depot web has no flow in its repository (.yardr/flows in "},
		{name: "a governing type file that does not parse", depot: "web", files: with(map[string]string{"type/wagon.flow": "wait => ride\n"}),
			want: ".yardr/flows/type/wagon.flow: "},
		{name: "a type file the depot's pointer stands in front of", depot: "web", files: map[string]string{"type/wagon.flow": copyWagon},
			setup: func(t *testing.T, y *askYard) {
				y.copyFlow(t, "starter.default", copyTrain)
				if err := y.SetFlowPointer(y.ctx, "web", "", "starter.default", "mckean"); err != nil {
					t.Fatal(err)
				}
			}, want: "every flow that governs depot web is in the store already (default (flow starter.default))"},
		{name: "two files to one name", depot: "web", files: with(map[string]string{"type/default.flow": copyWagon}),
			want: "the flows of default and type/default of depot web would both be copied to the store flow web.default"},
		{name: "a link where the flow's directory goes", depot: "web", files: flows, setup: func(t *testing.T, y *askYard) {
			if err := os.MkdirAll(filepath.Join(y.home, "flows"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(elsewhere(t), filepath.Join(y.home, "flows", "web.default")); err != nil {
				t.Fatal(err)
			}
		}, want: "is a symbolic link"},
		{name: "a link where the flows directory goes", depot: "web", files: flows, setup: func(t *testing.T, y *askYard) {
			if err := os.Symlink(elsewhere(t), filepath.Join(y.home, "flows")); err != nil {
				t.Fatal(err)
			}
		}, want: "is a symbolic link"},
		{name: "a link where a stage file goes", depot: "web", files: flows, setup: func(t *testing.T, y *askYard) {
			other := filepath.Join(elsewhere(t), "other.md")
			if err := os.WriteFile(other, []byte("Not here.\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(y.home, "flows", "web.default"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other, filepath.Join(y.home, "flows", "web.default", "new.md")); err != nil {
				t.Fatal(err)
			}
		}, want: "is a symbolic link"},
		{name: "a link among the files of the copy it replaces", depot: "web", files: flows, setup: func(t *testing.T, y *askYard) {
			if err := os.MkdirAll(filepath.Join(y.home, "flows", "web.default"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(elsewhere(t), filepath.Join(y.home, "flows", "web.default", "old.md")); err != nil {
				t.Fatal(err)
			}
		}, want: "web.default/old.md is a symbolic link: the yard removes a stage file only where a plain file stands"},
		{name: "a file where the flows directory goes", depot: "web", files: flows, setup: func(t *testing.T, y *askYard) {
			if err := os.WriteFile(filepath.Join(y.home, "flows"), []byte("a file\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, want: "is not a directory"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			y := listYard(t)
			if c.depot != "ghost" {
				y.dirDepotWith(t, c.depot, c.files)
			}
			if c.setup != nil {
				c.setup(t, y)
			}
			y.copyRefused(t, c.depot, "mckean", c.want)
		})
	}
	t.Run("a copy would make a broken open pin resolve", func(t *testing.T) {
		t.Parallel()
		y := listYard(t)
		y.dirDepotWith(t, "web", flows)
		y.copyFlow(t, "web.default", copyDefault)
		pinned, err := y.CreateBead(y.ctx, NewBead{Title: "broken pin", Metadata: map[string]any{"flow": "web.default"}}, "mckean")
		if err != nil {
			t.Fatal(err)
		}
		if err := y.write(func(w *wtx) error { return w.Bucket(bFlows).Delete([]byte("web.default")) }); err != nil {
			t.Fatal(err)
		}
		want := "flow name web.default is in use by bead " + pinned.ID
		if _, err := y.AddAsk(y.ctx, y.bead, copyAsk("web"), "yardmaster"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("a card that would make the broken pin resolve = %v, want it refused with %q", err, want)
		}
		y.copyRefused(t, "web", "mckean", want)
	})
}

// An open bead the copy would leave in a stage its flow lacks refuses the
// whole copy, naming the bead, as pointing the depot at that flow would: here
// the repository dropped the stage a bead is in, and what the copy says of the
// flow is what the repository says now. Nothing is written until the bead has
// moved to a stage the flow has.
func TestCopyRepoFlowsRefusesToStrandAnOpenBead(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	dir := y.dirDepotWith(t, "web", map[string]string{"default.flow": copyDefault, "default/new.md": "Build it.\n"})
	b, err := y.CreateBead(y.ctx, NewBead{Title: "in review", Depot: "web", Stage: "review"}, "mckean")
	if err != nil {
		t.Fatal(err)
	}
	writeFlowFiles(t, dir, map[string]string{"default.flow": "backlog -> new\nnew -> done\ndone terminal\n"})
	if _, err := y.AddAsk(y.ctx, y.bead, copyAsk("web"), "yardmaster"); err == nil || !strings.Contains(err.Error(), b.ID+" (review)") {
		t.Errorf("a card for a copy that would strand an open bead = %v, want it refused with %s", err, b.ID)
	}
	y.copyRefused(t, "web", "mckean", "flow web.default lacks the stage of open beads in depot web: "+b.ID+" (review); advance them to a stage the flow has")
	if _, err := y.Advance(y.ctx, b.ID, "backlog", Expect{}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if res, err := y.CopyRepoFlows(y.ctx, "web", "mckean"); err != nil || len(res.Flows) != 1 {
		t.Errorf("the copy once the bead has moved = %+v, %v", res, err)
	}
}

// Copying a depot's flows is the operator's, as a flow's stages are: a crew
// member or a user is refused, and nothing is written.
func TestCopyRepoFlowsIsTheOperatorsAlone(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	y.dirDepotWith(t, "web", map[string]string{"default.flow": copyDefault, "default/new.md": "Build it.\n"})
	for _, actor := range []string{"yardmaster", "ada"} {
		y.copyRefused(t, "web", actor, "only the operator (mckean) may change a flow's stages; this is "+actor)
	}
	if _, err := y.CopyRepoFlows(y.ctx, "web", "mckean"); err != nil {
		t.Errorf("the operator's own copy: %v", err)
	}
}

// A batch may run a write twice, and the files its first run wrote stay on
// disk when its transaction is rolled back: the retry finds them, leaves what
// it would write, answers what the first run would have (a copy of a flow
// that replaced nothing does not turn into one that did), and ends in the
// state a write that ran once would.
func TestCopyRepoFlowsRunTwiceEndsTheSame(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"default.flow": copyDefault, "type/wagon.flow": copyWagon,
		"default/new.md": "Build it.\n", "type/wagon/ride.md": "Ride it.\n",
	}
	// yard is a yard with the depot web, in the state a copy starts from: with
	// the leftover of an earlier copy where replacing says so.
	yard := func(t *testing.T, replacing bool) *askYard {
		y := listYard(t)
		y.dirDepotWith(t, "web", files)
		if replacing {
			if _, err := y.CopyRepoFlows(y.ctx, "web", "mckean"); err != nil {
				t.Fatal(err)
			}
			for _, typ := range []string{"", "wagon"} {
				if err := y.SetFlowPointer(y.ctx, "web", typ, "", "mckean"); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(y.home, "flows", "web.default", "stale.md"), []byte("Old.\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return y
	}
	for _, replacing := range []bool{false, true} {
		t.Run(fmt.Sprintf("replacing=%v", replacing), func(t *testing.T) {
			t.Parallel()
			once := yard(t, replacing)
			wantRes, err := once.CopyRepoFlows(once.ctx, "web", "mckean")
			if err != nil {
				t.Fatal(err)
			}

			y := yard(t, replacing)
			repo, err := y.RepoFlows(y.ctx, "web")
			if err != nil {
				t.Fatal(err)
			}
			boom := errors.New("another write of the batch failed")
			runs := 0
			var got *CopiedFlows
			err = y.write(func(w *wtx) error {
				runs++
				var err error
				if got, err = w.copyRepoFlows(y.ctx, repo, "mckean"); err != nil {
					return err
				}
				if runs == 1 {
					return boom
				}
				return nil
			})
			if err != nil || runs != 2 {
				t.Fatalf("the write ran %d times and ended %v, want 2 runs and nil", runs, err)
			}
			if !reflect.DeepEqual(got, wantRes) {
				t.Errorf("the retry answered %+v, want what a single run answers, %+v", got, wantRes)
			}
			if got.Flows[0].Replaced != replacing {
				t.Errorf("Replaced is %v, want %v", got.Flows[0].Replaced, replacing)
			}
			if a, b := y.copyState(t), once.copyState(t); a != b {
				t.Errorf("the retried copy left\n%s\nwhere a copy that ran once leaves\n%s", a, b)
			}
		})
	}
}

// copyAsk is the ask of flow copy-repo with the words after the command's own.
func copyAsk(words ...string) Ask {
	return Ask{Question: "Copy the flows?", To: "mckean", Context: []string{"the stages are to be edited in the yard"},
		Run: &AskRun{Argv: append([]string{"flow", "copy-repo"}, words...)}}
}

// A flow copy-repo card is weighty code: it says which file becomes which
// store flow, with how many stage files, for whose beads, what stays as it is,
// that the repository's files stop governing, and the commands that point
// each pointer back, the depot's first. Approving makes the copy as the
// terminal would, as the operator who approved it.
func TestRunAskFlowCopyRepoCardsTheCopy(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	dir := y.gitDepotWith(t, "web", map[string]string{
		"default.flow": copyDefault, "type/wagon.flow": copyWagon,
		"default/new.md": "Build it.\n", "default/review.md": "Review it.\n", "type/wagon/ride.md": "Ride it.\n",
	})
	sha := gitRun(t, dir, "rev-parse", "HEAD")
	seq := y.ask(t, copyAsk("web"), "yardmaster")
	r := y.asked(t, seq)
	run := r.Ask.Run
	if len(r.Ask.Weight) != 1 || r.Ask.Weight[0].Class != WeightCode || !StrongWeight(r.Ask.Weight) {
		t.Fatalf("weight = %+v, want one code line, which is strong", r.Ask.Weight)
	}
	if want := []string{"flow", "copy-repo", "web"}; !slices.Equal(run.Argv, want) {
		t.Errorf("the command is stored as %q, want %q", run.Argv, want)
	}
	if run.Description != "Copy a depot's repository flows into the yard store" {
		t.Errorf("the card describes it as %q", run.Description)
	}
	strongHas(t, run.Does,
		"copies the flows that govern depot web from its repository, repo@"+sha[:7]+", into the yard store with their stage files, and points the depot at the copies, all at once.",
		".yardr/flows/default.flow becomes the store flow web.default, for the depot's beads, with 2 stage files under "+filepath.Join(y.home, "flows", "web.default")+".",
		".yardr/flows/type/wagon.flow becomes the store flow web.wagon, for its beads of type wagon, with 1 stage file under "+filepath.Join(y.home, "flows", "web.wagon")+".",
		"While the copies stand, the repository's files above no longer govern those flows: a change that lands in .yardr/flows is not read for them",
		"A flow pointed back at the repository leaves its copy in the store, unused, and a later copy replaces it.")
	strongLacks(t, run.Does, "replaced whole", "Already in the store")
	if want := "yardr depot source web flow depot; yardr depot source web flow --type wagon depot"; run.Back != want || run.BackBy != "only the operator" {
		t.Errorf("taken back by %q (%s), want %q", run.Back, run.BackBy, want)
	}
	for _, key := range []string{"repo web", "copy web"} {
		if run.Seen[key] == "" {
			t.Errorf("the card saw nothing under %q: %v", key, run.Seen)
		}
	}

	before := y.copyState(t)
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil); err == nil || !strings.Contains(err.Error(), "weighty") {
		t.Fatalf("unweighed approval = %v, want a weighty refusal", err)
	}
	if after := y.copyState(t); after != before {
		t.Errorf("a refused approval changed the yard:\n%s\n%s", before, after)
	}
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "depot web: copied 2 flows from repo@" + sha[:7] + " into the store: web.default, web.wagon"; done.Result != want {
		t.Errorf("approval printed %q, want %q", done.Result, want)
	}
	ptrs, err := y.FlowPointers(y.ctx)
	if err != nil || !slices.Equal(ptrs, []FlowPointer{{Depot: "web", Flow: "web.default"}, {Depot: "web", Type: "wagon", Flow: "web.wagon"}}) {
		t.Errorf("the pointers are %+v, %v", ptrs, err)
	}
	if got, _ := y.homeFile(t, "web.wagon", "ride"); got != "Ride it.\n" {
		t.Errorf("the stage file of ride in web.wagon holds %q", got)
	}
	evs, err := y.EventsOfKind(y.ctx, "flow_pointer_set", 10)
	if err != nil || len(evs) != 2 || evs[0].Actor != "mckean" || evs[1].Data["type"] != "wagon" {
		t.Errorf("the approval left the events %+v, %v, want two flow_pointer_set by mckean", evs, err)
	}

	// The next card finds nothing to copy, and the one for a copy that
	// replaces an unused leftover says so, and says it removes files.
	if _, err := y.AddAsk(y.ctx, y.bead, copyAsk("web"), "yardmaster"); err == nil || !strings.Contains(err.Error(), "is in the store already") {
		t.Errorf("a second card = %v, want it refused: every flow is the store's", err)
	}
	for _, typ := range []string{"", "wagon"} {
		if err := y.SetFlowPointer(y.ctx, "web", typ, "", "mckean"); err != nil {
			t.Fatal(err)
		}
	}
	again := y.asked(t, y.ask(t, copyAsk("web"), "yardmaster")).Ask.Run
	strongHas(t, again.Does,
		".yardr/flows/default.flow becomes the store flow web.default",
		"A store flow of that name stands in no use, the leftover of an earlier copy: it is replaced whole, and so are its stage files, any of which the repository has no file for being removed.",
		".yardr/flows/type/wagon.flow becomes the store flow web.wagon",
		"A store flow of that name stands in no use")
}

// A card for a depot of named pointers says what each pointer led to, keeps
// the flows that are the store's out of its copy, and takes each back as the
// pointer was, not by clearing it.
func TestRunAskFlowCopyRepoCardsNamedPointersAndWhatStays(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	y.dirDepotWith(t, "mailroom", map[string]string{"outbox.post.flow": copyWagon, "outbox.post/ride.md": "Post it.\n"})
	y.copyFlow(t, "starter.default", copyTrain)
	for _, p := range [][3]string{{"mailroom", "", "starter.default"}, {"mailroom", "post", "outbox.post"}} {
		if err := y.SetFlowPointer(y.ctx, p[0], p[1], p[2], "mckean"); err != nil {
			t.Fatal(err)
		}
	}
	run := y.asked(t, y.ask(t, copyAsk("mailroom"), "yardmaster")).Ask.Run
	strongHas(t, run.Does,
		"from its repository, repo (directory), into the yard store",
		".yardr/flows/outbox.post.flow becomes the store flow mailroom.post, for its beads of type post, with 1 stage file",
		"The pointer that leads its beads of type post to the repository's flow outbox.post is replaced by one to the copy.",
		"Already in the store, and left as they are: default (flow starter.default).")
	if want := "yardr depot flow mailroom --type post outbox.post"; run.Back != want {
		t.Errorf("taken back by %q, want %q", run.Back, want)
	}
}

// The approval is held to the repository and the yard the card saw: a commit
// that landed, a depot pointer set, a leftover copy that appeared and a
// directory depot's file that changed each make it stale, naming which, and
// nothing is copied; a flow a copy would write over that is followed since is
// refused in the card's own words.
func TestRunAskFlowCopyRepoIsStaleWhereTheYardMoved(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	files := map[string]string{"default.flow": copyDefault, "default/new.md": "Build it.\n"}
	dir := y.gitDepotWith(t, "web", files)
	oldSHA := gitRun(t, dir, "rev-parse", "HEAD")
	// Whatever the approval was refused for, no flow of the depot was copied.
	nothingCopied := func() {
		t.Helper()
		if _, err := y.GetFlow(y.ctx, "web.default"); !errors.Is(err, ErrNotFound) {
			t.Errorf("web.default is %v after a refused approval, want none", err)
		}
		if got, err := y.FlowPointers(y.ctx); err != nil || slices.ContainsFunc(got, func(p FlowPointer) bool { return p.Depot == "web" && p.Flow == "web.default" }) {
			t.Errorf("a refused approval left the pointers %+v, %v", got, err)
		}
	}

	// A commit landed.
	seq := y.ask(t, copyAsk("web"), "yardmaster")
	writeFlowFiles(t, dir, map[string]string{"default/new.md": "Build it, then say so.\n"})
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "changed")
	newSHA := gitRun(t, dir, "rev-parse", "HEAD")
	y.refusedStale(t, seq, "ask "+fmt.Sprint(seq)+" was written from the repository of depot web at repo@"+oldSHA[:7]+", and it is at repo@"+newSHA[:7]+" now: a commit landed since, so what it copies is another. ")
	nothingCopied()

	// A pointer of the depot was set.
	seq = y.ask(t, copyAsk("web"), "yardmaster")
	y.copyFlow(t, "starter.wagon", copyWagon)
	if err := y.SetFlowPointer(y.ctx, "web", "wagon", "starter.wagon", "mckean"); err != nil {
		t.Fatal(err)
	}
	y.refusedStale(t, seq, "was written when the flows that point depot web, or the store flows the copy would write over, were another: they were changed since (flow_pointer_set, ")
	nothingCopied()
	if err := y.SetFlowPointer(y.ctx, "web", "wagon", "", "mckean"); err != nil {
		t.Fatal(err)
	}

	// A leftover copy appeared where the card said none stood.
	seq = y.ask(t, copyAsk("web"), "yardmaster")
	if err := os.MkdirAll(filepath.Join(y.home, "flows", "web.default"), 0o755); err != nil {
		t.Fatal(err)
	}
	y.refusedStale(t, seq, "were another: they were changed since")
	if err := os.Remove(filepath.Join(y.home, "flows", "web.default")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(y.home, "flows")); err != nil {
		t.Fatal(err)
	}
	nothingCopied()

	// A directory depot's own files changed.
	other := y.dirDepotWith(t, "plain", files)
	seq = y.ask(t, copyAsk("plain"), "yardmaster")
	writeFlowFiles(t, other, map[string]string{"default/new.md": "Build it, then say so.\n"})
	y.refusedStale(t, seq, "was written from the flow files of depot plain's directory as they were: they were changed since, so what it copies is another. ")

	// A copy that stands since the ask, and is followed: the card's own
	// refusal at approval, not a staleness.
	if _, err := y.CopyRepoFlows(y.ctx, "plain", "mckean"); err != nil {
		t.Fatal(err)
	}
	if err := y.SetFlowPointer(y.ctx, "plain", "", "", "mckean"); err != nil {
		t.Fatal(err)
	}
	seq = y.ask(t, copyAsk("plain"), "yardmaster")
	y.dirDepotWith(t, "third", nil)
	if err := y.SetFlowPointer(y.ctx, "third", "", "plain.default", "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil || !strings.Contains(err.Error(), "flow plain.default stands in the store and is in use by depot third") {
		t.Errorf("approval over a copy somebody follows now = %v, want it refused, naming depot third", err)
	}
	if got, err := y.FlowPointers(y.ctx); err != nil || slices.ContainsFunc(got, func(p FlowPointer) bool { return p.Depot == "plain" }) {
		t.Errorf("the refused approval left the pointers %+v, %v", got, err)
	}
}

// What a card is refused at the ask: the words it takes, a depot that is
// none or has nothing to copy, and the copies that are followed.
func TestRunAskFlowCopyRepoRefusals(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	y.dirDepotWith(t, "web", map[string]string{"default.flow": copyDefault, "default/new.md": "Build it.\n"})
	y.dirDepotWith(t, "empty", nil)
	for _, c := range []struct {
		words []string
		want  string
	}{
		{nil, "--run takes yardr flow copy-repo <depot>: one depot, not 0 words"},
		{[]string{"web", "empty"}, "one depot, not 2 words"},
		{[]string{"web", "--force"}, "--force"},
		{[]string{"ghost"}, "depot ghost: not found"},
		{[]string{"empty"}, "depot empty has no flow in its repository"},
	} {
		if _, err := y.AddAsk(y.ctx, y.bead, copyAsk(c.words...), "yardmaster"); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("AddAsk(%q) = %v, want it refused with %q", c.words, err, c.want)
		}
	}
	// A copy somebody follows is refused where it is asked.
	if _, err := y.CopyRepoFlows(y.ctx, "web", "mckean"); err != nil {
		t.Fatal(err)
	}
	if err := y.SetFlowPointer(y.ctx, "web", "", "", "mckean"); err != nil {
		t.Fatal(err)
	}
	y.dirDepotWith(t, "other", nil)
	if err := y.SetFlowPointer(y.ctx, "other", "", "web.default", "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.AddAsk(y.ctx, y.bead, copyAsk("web"), "yardmaster"); err == nil || !strings.Contains(err.Error(), "flow web.default stands in the store and is in use by depot other") {
		t.Errorf("a card over a copy depot other follows = %v, want it refused", err)
	}
}

// A batch may run an approval twice, with what its first run wrote on disk: a
// card that said a copy was new is still the card at the retry, and the
// approval commits.
func TestRunAskFlowCopyRepoApprovalRunTwiceEndsTheSame(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	y.dirDepotWith(t, "web", map[string]string{"default.flow": copyDefault, "default/new.md": "Build it.\n"})
	seq := y.ask(t, copyAsk("web"), "yardmaster")
	repo, err := y.RepoFlows(y.ctx, "web")
	if err != nil {
		t.Fatal(err)
	}
	boom := errors.New("another write of the batch failed")
	runs := 0
	var done *AskApproved
	err = y.write(func(w *wtx) error {
		runs++
		w.repos = map[string]*RepoFlows{"web": repo}
		var err error
		if done, err = w.approveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err != nil {
			return err
		}
		if runs == 1 {
			return boom
		}
		return nil
	})
	if err != nil || runs != 2 {
		t.Fatalf("the approval ran %d times and ended %v, want 2 runs and nil", runs, err)
	}
	if want := "depot web: copied 1 flow from repo (directory) into the store: web.default"; done == nil || done.Result != want {
		t.Errorf("the retried approval printed %+v, want %q", done, want)
	}
	if got, _ := y.homeFile(t, "web.default", "new"); got != "Build it.\n" {
		t.Errorf("the stage file holds %q", got)
	}
	if evs, err := y.EventsOfKind(y.ctx, "flow_pointer_set", 10); err != nil || len(evs) != 1 {
		t.Errorf("the retried approval left the events %+v, %v, want one", evs, err)
	}
}
