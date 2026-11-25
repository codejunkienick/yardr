package store_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
)

const (
	driftDefault = "backlog -> new\nnew -> review\nreview -> done\ndone terminal\n"
	driftWagon   = "wait -> ride\nride -> done\ndone terminal\n"
	driftTrain   = "load -> go\ngo -> done\ndone terminal\n"
)

// driftFiles are the files of a depot's repository whose flows the Line tests
// copy into the store.
var driftFiles = map[string]string{
	".yardr/flows/default.flow":       driftDefault,
	".yardr/flows/default/new.md":     "Build it.\n",
	".yardr/flows/default/review.md":  "Review it.\n",
	".yardr/flows/type/wagon.flow":    driftWagon,
	".yardr/flows/type/wagon/ride.md": "Ride it.\n",
}

// lineDrift names every flow and stage of line that says it differs from the
// repository, as "<flow>" and "<flow>:<stage>", in the Line's order.
func lineDrift(line *store.Line) []string {
	var got []string
	for _, f := range line.Flows {
		if f.DiffersFromRepo {
			got = append(got, f.Name)
		}
		for _, s := range f.Stages {
			if s.DiffersFromRepo {
				got = append(got, f.Name+":"+s.Stage)
			}
		}
	}
	return got
}

func driftFlow(t *testing.T, name, text string) *store.Flow {
	t.Helper()
	f, err := store.ReadFlow(name, text)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// driftFixture is the three store flows of a depot's copy (yardr.default, the
// wagon type's yardr.wagon, and the train type's yardr.train.local, copied from
// the repository's named flow yardr.train) beside the repository files they
// were copied from, built without a store: FlowDiffersFromRepo reads only the
// home's files and the repository it is given.
type driftFixture struct {
	t       *testing.T
	home    string
	repo    *store.RepoFlows
	pointed map[string]bool
	flows   map[string]store.DepotFlow // by type
}

func newDriftFixture(t *testing.T) *driftFixture {
	t.Helper()
	d := &driftFixture{t: t, home: t.TempDir(), repo: &store.RepoFlows{Depot: "yardr"},
		pointed: map[string]bool{"": true, "wagon": true, "train": true}, flows: map[string]store.DepotFlow{}}
	for _, c := range []struct{ typ, copied, path, name, text string }{
		{"", "yardr.default", ".yardr/flows/default.flow", "default", driftDefault},
		{"wagon", "yardr.wagon", ".yardr/flows/type/wagon.flow", "type/wagon", driftWagon},
		{"train", "yardr.train.local", ".yardr/flows/yardr.train.flow", "yardr.train", driftTrain},
	} {
		rf := store.RepoFlow{Path: c.path, Flow: driftFlow(t, c.name, c.text)}
		if c.typ == "wagon" {
			rf.Type = c.typ
		} else {
			rf.Name = c.name
		}
		d.repo.Flows = append(d.repo.Flows, rf)
		d.flows[c.typ] = store.DepotFlow{Type: c.typ, Flow: driftFlow(t, c.copied, c.text), Source: store.FlowSource{Kind: "store"}}
	}
	for _, c := range []struct{ flow, copied, stage, text string }{
		{"default", "yardr.default", "new", "Build it.\n"},
		{"default", "yardr.default", "review", "Review it.\n"},
		{"type/wagon", "yardr.wagon", "ride", "Ride it.\n"},
		{"yardr.train", "yardr.train.local", "go", "Go.\n"},
	} {
		d.repoStage(c.flow, c.stage, c.text)
		d.homeStage(c.copied, c.stage, c.text)
	}
	return d
}

// repoStage puts text in the repository's stage file of stage in flow, named
// as the repository's flow is (default, type/wagon, yardr.train).
func (d *driftFixture) repoStage(flow, stage, text string) {
	d.dropRepoStage(flow, stage)
	d.repo.StageFiles = append(d.repo.StageFiles, store.StageFile{Path: store.StageFilePath(flow, stage), Text: text})
}

func (d *driftFixture) dropRepoStage(flow, stage string) {
	p := store.StageFilePath(flow, stage)
	d.repo.StageFiles = slices.DeleteFunc(d.repo.StageFiles, func(sf store.StageFile) bool { return sf.Path == p })
}

// homeStage writes the home's stage file of stage in the store flow flow.
func (d *driftFixture) homeStage(flow, stage, text string) {
	d.t.Helper()
	p := filepath.Join(store.HomeStageDir(d.home, flow), stage+".md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		d.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		d.t.Fatal(err)
	}
}

func (d *driftFixture) dropHomeStage(flow, stage string) {
	d.t.Helper()
	if err := os.Remove(filepath.Join(store.HomeStageDir(d.home, flow), stage+".md")); err != nil {
		d.t.Fatal(err)
	}
}

// repoFlow is the repository's flow file at path.
func (d *driftFixture) repoFlow(path string) *store.RepoFlow {
	d.t.Helper()
	for i := range d.repo.Flows {
		if d.repo.Flows[i].Path == path {
			return &d.repo.Flows[i]
		}
	}
	d.t.Fatalf("the repository has no %s", path)
	return nil
}

// drift is what FlowDiffersFromRepo says of the store flow of type typ: whether
// the flow differs, and the stages whose files do, sorted.
type drift struct {
	flow   bool
	stages []string
}

func (d *driftFixture) differs(typ string) drift {
	d.t.Helper()
	flow, stages, err := store.FlowDiffersFromRepo(d.home, d.flows[typ], d.repo, d.pointed)
	if err != nil {
		d.t.Fatal(err)
	}
	var names []string
	for stage := range stages {
		if stages[stage] {
			names = append(names, stage)
		}
	}
	slices.Sort(names)
	return drift{flow, names}
}

// A store flow differs from the repository where its canonical text does, where
// a stage's file does (byte for byte; none and a file with nothing in it are
// one), where the file it stands in front of does not parse, and, for the
// depot's own flow, where the repository has a type's file that no pointer
// answers for. Every case starts from the copy as it was made, which says
// nothing, and the flows it does not name say nothing afterwards either.
func TestFlowDiffersFromRepo(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		change func(d *driftFixture)
		want   map[string]drift // by type; a type not named differs from nothing
	}{
		{"a copy as it was made", func(d *driftFixture) {}, nil},
		{"a home stage file edited", func(d *driftFixture) {
			d.homeStage("yardr.default", "new", "Build it, then test it.\n")
		}, map[string]drift{"": {true, []string{"new"}}}},
		{"a repository stage file edited", func(d *driftFixture) {
			d.repoStage("default", "review", "Review it twice.\n")
		}, map[string]drift{"": {true, []string{"review"}}}},
		{"a stage file one byte apart", func(d *driftFixture) {
			d.homeStage("yardr.default", "new", "Build it.")
		}, map[string]drift{"": {true, []string{"new"}}}},
		{"a stage file only the home has", func(d *driftFixture) {
			d.homeStage("yardr.default", "backlog", "Wait here.\n")
		}, map[string]drift{"": {true, []string{"backlog"}}}},
		{"a stage file only the repository has", func(d *driftFixture) {
			d.repoStage("default", "backlog", "Wait here.\n")
		}, map[string]drift{"": {true, []string{"backlog"}}}},
		{"a home stage file taken away", func(d *driftFixture) {
			d.dropHomeStage("yardr.default", "new")
		}, map[string]drift{"": {true, []string{"new"}}}},
		{"a repository stage file taken away", func(d *driftFixture) {
			d.dropRepoStage("default", "review")
		}, map[string]drift{"": {true, []string{"review"}}}},
		{"a stage file with nothing in it is none", func(d *driftFixture) {
			d.homeStage("yardr.default", "backlog", " \n")
			d.repoStage("default", "done", "\n")
		}, nil},
		{"the repository's flow edited", func(d *driftFixture) {
			d.repoFlow(".yardr/flows/default.flow").Flow = driftFlow(d.t, "default", "backlog -> new\nnew -> review | backlog\nreview -> done\ndone terminal\n")
		}, map[string]drift{"": {true, nil}}},
		{"the repository's flow reworded but not changed", func(d *driftFixture) {
			d.repoFlow(".yardr/flows/default.flow").Flow = driftFlow(d.t, "default", "# the line\n\nbacklog  ->  new\nnew -> review\n\nreview -> done\ndone terminal\n")
		}, nil},
		{"the repository's flow does not parse", func(d *driftFixture) {
			rf := d.repoFlow(".yardr/flows/default.flow")
			rf.Flow, rf.Error = nil, "flow line 1: want 'stage -> next on outcome | other' or 'stage terminal'"
		}, map[string]drift{"": {true, nil}}},
		{"a type's file that no pointer answers for", func(d *driftFixture) {
			d.repo.Flows = append(d.repo.Flows, store.RepoFlow{Path: ".yardr/flows/type/release.flow", Type: "release", Flow: driftFlow(d.t, "type/release", "plan -> ship\nship terminal\n")})
		}, map[string]drift{"": {true, nil}}},
		{"a type's file its own pointer answers for", func(d *driftFixture) {
			d.repo.Flows = append(d.repo.Flows, store.RepoFlow{Path: ".yardr/flows/type/release.flow", Type: "release", Flow: driftFlow(d.t, "type/release", "plan -> ship\nship terminal\n")})
			d.pointed["release"] = true
		}, nil},
		{"a type's home stage file edited", func(d *driftFixture) {
			d.homeStage("yardr.wagon", "ride", "Ride it fast.\n")
		}, map[string]drift{"wagon": {true, []string{"ride"}}}},
		{"a type's file edited in the repository", func(d *driftFixture) {
			d.repoFlow(".yardr/flows/type/wagon.flow").Flow = driftFlow(d.t, "type/wagon", "wait -> ride\nride -> done | wait\ndone terminal\n")
		}, map[string]drift{"wagon": {true, nil}}},
		{"a named flow copied beside its original, its stage file edited", func(d *driftFixture) {
			d.repoStage("yardr.train", "go", "Go slowly.\n")
		}, map[string]drift{"train": {true, []string{"go"}}}},
		{"a named flow copied beside its original, its flow edited", func(d *driftFixture) {
			d.repoFlow(".yardr/flows/yardr.train.flow").Flow = driftFlow(d.t, "yardr.train", "load -> go\ngo -> done | load\ndone terminal\n")
		}, map[string]drift{"train": {true, nil}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d := newDriftFixture(t)
			for typ := range d.flows {
				if got := d.differs(typ); got.flow || len(got.stages) != 0 {
					t.Fatalf("the copy of type %q differs from the files it was copied from: %+v", typ, got)
				}
			}
			c.change(d)
			for typ := range d.flows {
				if got, want := d.differs(typ), c.want[typ]; got.flow != want.flow || !slices.Equal(got.stages, want.stages) {
					t.Errorf("type %q differs = %+v, want %+v", typ, got, want)
				}
			}
		})
	}
}

// A flow that is the repository's own, a store flow that stands in front of no
// file, and a repository that was not read differ from nothing, whatever the
// files hold; a stage file the home has that cannot be read is the read's
// error.
func TestFlowDiffersFromRepoOfWhatShadowsNothing(t *testing.T) {
	t.Parallel()
	d := newDriftFixture(t)
	d.homeStage("yardr.default", "new", "Not the repository's.\n")
	d.repoStage("type/wagon", "ride", "Not the home's.\n")
	repoFlow := store.DepotFlow{Flow: driftFlow(t, "default", driftDefault), Source: store.FlowSource{Kind: "repo", Path: ".yardr/flows/default.flow"}}
	pointedAtRepo := store.DepotFlow{Type: "wagon", Flow: driftFlow(t, "type/wagon", driftWagon), Source: store.FlowSource{Kind: "store", Path: ".yardr/flows/type/wagon.flow"}}
	noFile := store.DepotFlow{Type: "bug", Flow: driftFlow(t, "yardr.bug", "triage -> fixed\nfixed terminal\n"), Source: store.FlowSource{Kind: "store"}}
	for name, c := range map[string]struct {
		df   store.DepotFlow
		repo *store.RepoFlows
	}{
		"a repository flow":                 {repoFlow, d.repo},
		"a pointer at a repository flow":    {pointedAtRepo, d.repo},
		"a store flow with no file to read": {noFile, d.repo},
		"no repository":                     {d.flows[""], nil},
	} {
		if flow, stages, err := store.FlowDiffersFromRepo(d.home, c.df, c.repo, d.pointed); flow || len(stages) != 0 || err != nil {
			t.Errorf("%s: differs = %v, %v, %v, want none", name, flow, stages, err)
		}
	}

	// A directory where the stage file should be is a read that fails, which a
	// Line then fails with as it does without the comparison.
	d.dropHomeStage("yardr.default", "new")
	if err := os.Mkdir(filepath.Join(store.HomeStageDir(d.home, "yardr.default"), "new.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if flow, stages, err := store.FlowDiffersFromRepo(d.home, d.flows[""], d.repo, d.pointed); err == nil || !strings.Contains(err.Error(), "stage file of new in flow yardr.default") || flow || len(stages) != 0 {
		t.Errorf("an unreadable stage file: differs = %v, %v, %v, want its error alone", flow, stages, err)
	}
}

// ShadowedRepoFlow names the repository's file a store flow stands in front of:
// default.flow for the depot's own flow, a type's file for a type's, and for a
// type with none the named flow the copy was made from, whose name flow
// copy-repo ends in .local where the repository has a flow of the copy's own
// name.
func TestShadowedRepoFlow(t *testing.T) {
	t.Parallel()
	repo := &store.RepoFlows{Flows: []store.RepoFlow{
		{Path: ".yardr/flows/default.flow", Name: "default"},
		{Path: ".yardr/flows/type/wagon.flow", Type: "wagon"},
		{Path: ".yardr/flows/yardr.train.flow", Name: "yardr.train"},
	}}
	namedOnly := &store.RepoFlows{Flows: []store.RepoFlow{{Path: ".yardr/flows/yardr.train.flow", Name: "yardr.train"}}}
	inStore, inRepo := store.FlowSource{Kind: "store"}, store.FlowSource{Kind: "repo", Path: ".yardr/flows/type/wagon.flow"}
	of := func(typ, name string, src store.FlowSource) store.DepotFlow {
		return store.DepotFlow{Type: typ, Flow: &store.Flow{Name: name}, Source: src}
	}
	for _, c := range []struct {
		name string
		repo *store.RepoFlows
		df   store.DepotFlow
		want string // the file's path, "" for none
	}{
		{"the depot's own flow", repo, of("", "yardr.default", inStore), ".yardr/flows/default.flow"},
		{"a type with a file of its own", repo, of("wagon", "yardr.wagon", inStore), ".yardr/flows/type/wagon.flow"},
		{"a named flow copied beside its original", repo, of("train", "yardr.train.local", inStore), ".yardr/flows/yardr.train.flow"},
		{"a named flow of the copy's own name", repo, of("train", "yardr.train", inStore), ".yardr/flows/yardr.train.flow"},
		{"a type with no file of any name", repo, of("bug", "yardr.bug", inStore), ""},
		{"the depot's own flow, whose repository has no default", namedOnly, of("", "yardr.train.local", inStore), ""},
		{"a repository flow", repo, of("wagon", "type/wagon", inRepo), ""},
		{"no repository", nil, of("", "yardr.default", inStore), ""},
	} {
		got := ""
		if rf := store.ShadowedRepoFlow(c.repo, c.df); rf != nil {
			got = rf.Path
		}
		if got != c.want {
			t.Errorf("%s: shadows %q, want %q", c.name, got, c.want)
		}
	}
}

// A type is pointed where a pointer in the store answers for it, also where the
// pointer leads to a flow that only the repository has: that pointer stands in
// front of the type's own file as well, so the file is no drift of the
// depot's flow. A type the repository's file governs is not.
func TestPointedTypes(t *testing.T) {
	t.Parallel()
	got := store.PointedTypes([]store.DepotFlow{
		{Type: "", Source: store.FlowSource{Kind: "store"}},
		{Type: "wagon", Source: store.FlowSource{Kind: "store", Path: ".yardr/flows/yardr.train.flow"}},
		{Type: "release", Source: store.FlowSource{Kind: "repo", Path: ".yardr/flows/type/release.flow"}},
	})
	for typ, want := range map[string]bool{"": true, "wagon": true, "release": false, "bug": false} {
		if got[typ] != want {
			t.Errorf("type %q is pointed = %v, want %v", typ, got[typ], want)
		}
	}
}

// driftYard is a yard with the depot web, whose repository is a directory or a
// git repository on its base main, holding driftFiles, and whose flows are
// copied into the store.
type driftYard struct {
	t    *testing.T
	api  store.API
	own  *store.Store
	home string
	dir  string
	git  bool
}

func newDriftYard(t *testing.T, git bool) *driftYard {
	t.Helper()
	home := t.TempDir()
	api, own := storetest.NewOwnedIn(t, home)
	d := &driftYard{t: t, api: api, own: own, home: home, dir: t.TempDir(), git: git}
	depot := store.Depot{Name: "web", Path: d.dir, Kind: store.DepotDir}
	if git {
		runGit(t, d.dir, "init", "-q", "-b", "main")
		depot = store.Depot{Name: "web", Path: d.dir, Base: "main"}
	}
	d.write(driftFiles)
	if err := api.PutDepot(context.Background(), depot, "me"); err != nil {
		t.Fatal(err)
	}
	if _, err := api.CopyRepoFlows(context.Background(), "web", "me", true); err != nil {
		t.Fatal(err)
	}
	return d
}

// write puts files in the repository, committed on its base where it is a git
// one. A file that is written again has to change its size: a directory depot
// reads its files again where a file's mtime or size moved.
func (d *driftYard) write(files map[string]string) {
	d.t.Helper()
	if d.git {
		commitFiles(d.t, d.dir, files)
		return
	}
	for name, text := range files {
		p := filepath.Join(d.dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			d.t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			d.t.Fatal(err)
		}
	}
}

func (d *driftYard) remove(names ...string) {
	d.t.Helper()
	for _, name := range names {
		if err := os.Remove(filepath.Join(d.dir, filepath.FromSlash(name))); err != nil {
			d.t.Fatal(err)
		}
	}
	if d.git {
		commitFiles(d.t, d.dir, nil)
	}
}

// drift is what the Line of the depot says differs, as lineDrift names it.
func (d *driftYard) drift() []string {
	d.t.Helper()
	line, err := d.api.Line(context.Background(), "web")
	if err != nil {
		d.t.Fatal(err)
	}
	return lineDrift(line)
}

// A copy of a repository's flows says nothing of it; once the yard's copy or
// the repository's file moves, the Line says which flow and which stage, and
// once the two read alike again it says nothing, since it is read as both
// stand now. It is so for a directory depot and for a git one, whose base is
// read at its tip, and the Line reads the repository no more often than it
// does for a depot that follows its files.
func TestLineSaysWhereAStoreFlowDiffersFromTheRepository(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"dir", "git"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			d := newDriftYard(t, kind == "git")
			if got := d.drift(); len(got) != 0 {
				t.Fatalf("right after the copy the Line says %v differ", got)
			}
			// A save of a stage file, as flow set-stage makes it, and its undoing.
			if _, err := d.api.PutStageFile(ctx, "web.default", "new", "Build it, then test it.\n", "", "me", true); err != nil {
				t.Fatal(err)
			}
			if got, want := d.drift(), []string{"web.default", "web.default:new"}; !slices.Equal(got, want) {
				t.Errorf("after a save of a stage file the Line says %v differ, want %v", got, want)
			}
			if _, err := d.api.PutStageFile(ctx, "web.default", "new", "Build it.\n", "", "me", true); err != nil {
				t.Fatal(err)
			}
			if got := d.drift(); len(got) != 0 {
				t.Errorf("after the save is put back the Line says %v differ", got)
			}

			// What lands on the base, and what lands there and is taken away.
			for _, c := range []struct {
				name    string
				change  func()
				restore func()
				want    []string
			}{
				{"a stage file changed", func() {
					d.write(map[string]string{".yardr/flows/default/review.md": "Review it twice.\n"})
				}, func() {
					d.write(map[string]string{".yardr/flows/default/review.md": driftFiles[".yardr/flows/default/review.md"]})
				}, []string{"web.default", "web.default:review"}},
				{"a type's file nothing points at", func() {
					d.write(map[string]string{".yardr/flows/type/release.flow": "plan -> ship\nship terminal\n"})
				}, func() { d.remove(".yardr/flows/type/release.flow") }, []string{"web.default"}},
				{"the flow's file broken", func() {
					d.write(map[string]string{".yardr/flows/default.flow": "this is no flow\n"})
				}, func() { d.write(map[string]string{".yardr/flows/default.flow": driftDefault}) }, []string{"web.default"}},
			} {
				c.change()
				if got := d.drift(); !slices.Equal(got, c.want) {
					t.Errorf("%s: the Line says %v differ, want %v", c.name, got, c.want)
				}
				c.restore()
				if got := d.drift(); len(got) != 0 {
					t.Errorf("%s, put back: the Line says %v differ", c.name, got)
				}
			}

			// The comparison is made on the repository the Line has read already.
			plain := t.TempDir()
			if err := os.MkdirAll(filepath.Join(plain, ".yardr", "flows"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(plain, ".yardr", "flows", "default.flow"), []byte(driftDefault), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := d.api.PutDepot(ctx, store.Depot{Name: "plain", Path: plain, Kind: store.DepotDir}, "me"); err != nil {
				t.Fatal(err)
			}
			reads := func(depot string) int64 {
				t.Helper()
				before := d.own.RepoReads()
				if _, err := d.api.Line(ctx, depot); err != nil {
					t.Fatal(err)
				}
				return d.own.RepoReads() - before
			}
			if copied, followed := reads("web"), reads("plain"); copied != followed {
				t.Errorf("the Line of a copy read the repository %d times, that of a depot following its files %d", copied, followed)
			}
		})
	}
}
