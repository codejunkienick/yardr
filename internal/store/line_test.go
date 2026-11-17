package store_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
)

// lineStage finds a stage of a flow of a Line answer.
func lineStage(t *testing.T, line *store.Line, flow, stage string) store.LineStage {
	t.Helper()
	for _, f := range line.Flows {
		if f.Name != flow {
			continue
		}
		for _, s := range f.Stages {
			if s.Stage == stage {
				return s
			}
		}
	}
	t.Fatalf("no stage %s of flow %s in %+v", stage, flow, line.Flows)
	return store.LineStage{}
}

func lineFlow(t *testing.T, line *store.Line, name, typ string) store.LineFlow {
	t.Helper()
	for _, f := range line.Flows {
		if f.Name == name && f.Type == typ {
			return f
		}
	}
	t.Fatalf("no flow %s type %q in %+v", name, typ, line.Flows)
	return store.LineFlow{}
}

// TestLineGitDepotRepoFlow pins the Line of a git depot whose flow stands
// in its repository: the source at the base commit, stages in order with
// next, outcomes and terminal flags, the representative route whole with
// the filtered rules behind it, each scope named, the stage file's text,
// a depot prompt, and open counts counted once under the representative.
func TestLineGitDepotRepoFlow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api, _ := storetest.NewOwnedIn(t, t.TempDir())
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	notYardDigest := strings.Repeat("a", 64)
	dir := gitRepo(t, map[string]string{
		".yardr/flows/default.flow":                      "backlog -> new\nnew -> review on done | backlog\nreview -> merged on done\nmerged terminal\n",
		".yardr/flows/default/new.md":                    "Build the bead.\n",
		".yardr/roles/builder.md":                        "Depot builder prompt.",
		".yardr/roles/not-yard." + notYardDigest + ".md": "A depot file with a digest-shaped name.",
		".yardr/roles/missing-home.md":                   "not the home copy",
	})
	sha := runGit(t, dir, "rev-parse", "HEAD")
	must(api.PutDepot(ctx, store.Depot{Name: "web", Path: dir, Base: "main"}, "me"))
	must(api.PutGroup(ctx, store.Group{Name: "builders", Runner: "herdr", Limit: 3,
		Config: map[string]any{"kind": "claude", "model": "opus", "prompt": "roles/builder.md", "env_file": "/secret/builder.env"}}, "me"))
	must(api.PutGroup(ctx, store.Group{Name: "reviewers", Runner: "herdr", Limit: 2,
		Config: map[string]any{"kind": "codex", "prompt": "roles/not-yard." + notYardDigest + ".md"}}, "me"))
	must(api.PutGroup(ctx, store.Group{Name: "scripter", Runner: "exec", Limit: 1,
		Config: map[string]any{"cmd": "scripts/run --fast"}}, "me"))
	web := "web"
	own, err := api.AddRoute(ctx, store.Route{Stage: "new", Depot: &web, Group: "builders"}, "me")
	must(err)
	label := "difficulty:3"
	filtered, err := api.AddRoute(ctx, store.Route{Stage: "new", Depot: &web, Label: &label, Group: "reviewers"}, "me")
	must(err)
	prio := 1
	wide, err := api.AddRoute(ctx, store.Route{Stage: "new", BeadPriority: &prio, Group: "scripter"}, "me")
	must(err)
	review, err := api.AddRoute(ctx, store.Route{Stage: "review", Group: "reviewers",
		Else: []store.RouteGroup{{Name: "builders", When: "Weekly<80"}}}, "me")
	must(err)
	_ = review
	must(api.PutGroup(ctx, store.Group{Name: "landers", Runner: "exec", Limit: 1,
		Config: map[string]any{"cmd": "scripts/land --squash"}}, "me"))
	_, err = api.AddRoute(ctx, store.Route{Stage: "merged", Group: "landers"}, "me")
	must(err)
	for _, id := range []string{"web-a", "web-b"} {
		_, err := api.CreateBead(ctx, store.NewBead{Title: id, ID: id, Stage: "new", Depot: "web"}, "me")
		must(err)
	}
	_, err = api.CreateBead(ctx, store.NewBead{Title: "web-c", ID: "web-c", Stage: "review", Depot: "web"}, "me")
	must(err)

	line, err := api.Line(ctx, "web")
	must(err)
	if line.Depot != "web" || line.Kind != store.DepotGit {
		t.Errorf("line = %s/%s, want web/git", line.Depot, line.Kind)
	}
	f := lineFlow(t, line, "default", "")
	if want := "repo@" + sha[:7]; f.Source != want {
		t.Errorf("source = %q, want %q", f.Source, want)
	}
	var stages []string
	for _, s := range f.Stages {
		stages = append(stages, s.Stage)
	}
	if strings.Join(stages, ",") != "backlog,new,review,merged" {
		t.Errorf("stages = %v, want backlog,new,review,merged", stages)
	}
	backlog := lineStage(t, line, "default", "backlog")
	if backlog.Route != nil || len(backlog.Warnings) != 1 || !strings.Contains(backlog.Warnings[0], "has no route") {
		t.Errorf("backlog = %+v, want no route and its warning", backlog)
	}
	merged := lineStage(t, line, "default", "merged")
	if !merged.Terminal {
		t.Errorf("merged is not terminal: %+v", merged)
	}
	if merged.Route == nil || len(merged.Route.Chain) != 1 || merged.Route.Chain[0].Runner != "exec" || merged.Route.Chain[0].Cmd != "scripts/land --squash" {
		t.Errorf("merged route = %+v, want the exec group with its cmd whole", merged.Route)
	}
	nw := lineStage(t, line, "default", "new")
	if nw.Open != 2 {
		t.Errorf("new open = %d, want 2", nw.Open)
	}
	if len(nw.Next) != 2 || nw.Next[0] != "review" || len(nw.Outcomes) != 1 || nw.Outcomes[0].Name != "done" || nw.Outcomes[0].To != "review" {
		t.Errorf("new next/outcomes = %v/%v, want review on done", nw.Next, nw.Outcomes)
	}
	if nw.StageFile == nil || nw.StageFile.Path != ".yardr/flows/default/new.md" || nw.StageFile.Text != "Build the bead.\n" {
		t.Errorf("new stage file = %+v, want the repository file whole", nw.StageFile)
	}
	if nw.Route == nil {
		t.Fatalf("new has no representative route: %+v", nw)
	}
	rep := nw.Route
	if rep.ID != own.ID || rep.Scope != store.ScopeOwn {
		t.Errorf("representative = %s/%q, want %s/this depot's own", rep.ID, rep.Scope, own.ID)
	}
	if rep.Filters.Type != "" || len(rep.Filters.Labels) != 0 || rep.Filters.Priority != "" || rep.Filters.Originator != "" {
		t.Errorf("representative filters = %+v, want every filter unset, shown", rep.Filters)
	}
	if rep.Limit == nil || *rep.Limit != 3 || len(rep.Chain) != 1 {
		t.Fatalf("representative limit/chain = %v/%+v, want limit 3, one group", rep.Limit, rep.Chain)
	}
	g := rep.Chain[0]
	if g.Name != "builders" || g.Runner != "herdr" || g.Kind != "claude" || g.Model != "opus" {
		t.Errorf("chain group = %+v, want builders herdr claude/opus", g)
	}
	if g.Prompt == nil {
		t.Fatalf("builders carry no prompt: %+v", g)
	}
	p := g.Prompt
	if p.Path != "roles/builder.md" || p.Source != store.PromptDepot || p.Found != ".yardr/roles/builder.md" || p.Commit != sha || p.Text != "Depot builder prompt." {
		t.Errorf("prompt = %+v, want the depot's at the base commit", p)
	}
	if p.Carriable || !strings.Contains(p.Why, "relative path") {
		t.Errorf("prompt carriage = %v/%q, want refused as a relative path", p.Carriable, p.Why)
	}
	if len(nw.OtherRoutes) != 2 {
		t.Fatalf("other routes = %+v, want the labelled and the priority rule", nw.OtherRoutes)
	}
	byID := map[string]store.LineOtherRoute{}
	for _, o := range nw.OtherRoutes {
		byID[o.ID] = o
	}
	if o := byID[filtered.ID]; o.Scope != store.ScopeOwn || len(o.Filters.Labels) != 1 || o.Filters.Labels[0] != "difficulty:3" || o.Group != "reviewers" {
		t.Errorf("labelled rule behind = %+v, want own difficulty:3 to reviewers", o)
	}
	if o := byID[wide.ID]; o.Scope != store.ScopeYardWide || o.Filters.Priority != "P0-P1" || o.Group != "scripter" {
		t.Errorf("priority rule behind = %+v, want yard-wide P0-P1 to scripter", o)
	}
	rv := lineStage(t, line, "default", "review")
	if rv.Route == nil || rv.Route.Scope != store.ScopeYardWide || rv.Open != 1 {
		t.Errorf("review = %+v, want a yard-wide representative and 1 open", rv)
	}
	if rv.Route != nil {
		if len(rv.Route.Chain) != 2 || rv.Route.Chain[1].Name != "builders" || rv.Route.Chain[1].When != "Weekly<80" {
			t.Errorf("review chain = %+v, want reviewers then builders where Weekly<80", rv.Route.Chain)
		}
		if p := rv.Route.Chain[0].Prompt; p == nil || p.Digest != "" {
			t.Errorf("depot prompt digest = %+v, want no yard-written digest", p)
		}
	}
	raw, err := json.Marshal(line)
	must(err)
	if strings.Contains(string(raw), "env_file") || strings.Contains(string(raw), "/secret/builder.env") {
		t.Errorf("the line carries an env file path: %s", raw)
	}
	if _, err := api.Line(ctx, "nope"); err == nil {
		t.Error("Line of a depot the yard has not answered without an error")
	}
}

// TestLineDirDepotDirectoryAndStoreFlows pins a dir depot told apart by
// source: its directory flow reads repo (directory) with the directory
// named and its stage files and roles from the directory, while a store
// flow on the same depot reads store.
func TestLineDirDepotDirectoryAndStoreFlows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api, _ := storetest.NewOwnedIn(t, t.TempDir())
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	must(os.MkdirAll(filepath.Join(dir, ".yardr", "flows", "default"), 0o755))
	must(os.MkdirAll(filepath.Join(dir, ".yardr", "roles"), 0o755))
	must(os.WriteFile(filepath.Join(dir, ".yardr", "flows", "default.flow"), []byte("inbox -> done\ndone terminal\n"), 0o644))
	must(os.WriteFile(filepath.Join(dir, ".yardr", "flows", "default", "inbox.md"), []byte("Take it in.\n"), 0o644))
	must(os.WriteFile(filepath.Join(dir, ".yardr", "roles", "worker.md"), []byte("Dir worker prompt."), 0o644))
	must(api.PutDepot(ctx, store.Depot{Name: "docs", Path: dir, Kind: store.DepotDir}, "me"))
	must(api.PutGroup(ctx, store.Group{Name: "workers", Runner: "herdr", Limit: 1,
		Config: map[string]any{"kind": "claude", "prompt": "roles/worker.md"}}, "me"))
	_, err := api.AddRoute(ctx, store.Route{Stage: "inbox", Group: "workers"}, "me")
	must(err)
	bugs, err := store.ParseFlow("triage -> fixed\nfixed terminal\n")
	must(err)
	bugs.Name = "bugs"
	_, err = api.PutFlow(ctx, *bugs, "me")
	must(err)
	must(api.SetFlowPointer(ctx, "docs", "bug", "bugs", "me"))

	line, err := api.Line(ctx, "docs")
	must(err)
	if line.Kind != store.DepotDir {
		t.Errorf("kind = %q, want dir", line.Kind)
	}
	f := lineFlow(t, line, "default", "")
	if f.Source != "repo (directory)" || f.Directory != dir {
		t.Errorf("directory flow = %q in %q, want repo (directory) in %s", f.Source, f.Directory, dir)
	}
	inbox := lineStage(t, line, "default", "inbox")
	if inbox.StageFile == nil || inbox.StageFile.Text != "Take it in.\n" {
		t.Errorf("inbox stage file = %+v, want the directory's", inbox.StageFile)
	}
	if inbox.Route == nil || len(inbox.Route.Chain) != 1 || inbox.Route.Chain[0].Prompt == nil {
		t.Fatalf("inbox route = %+v, want workers with a prompt", inbox.Route)
	}
	p := inbox.Route.Chain[0].Prompt
	if p.Source != store.PromptDepot || p.Text != "Dir worker prompt." || p.Commit != "" {
		t.Errorf("prompt = %+v, want the directory's role at no commit", p)
	}
	sf := lineFlow(t, line, "bugs", "bug")
	if sf.Source != "store" || sf.Directory != "" {
		t.Errorf("store flow = %q in %q, want store with no directory", sf.Source, sf.Directory)
	}
	// A bead the directory flow leaves behind when it loses its stage
	// stands in the flow's warnings, as flow show prints it.
	_, err = api.CreateBead(ctx, store.NewBead{Title: "docs-a", ID: "docs-a", Stage: "inbox", Depot: "docs"}, "me")
	must(err)
	must(os.WriteFile(filepath.Join(dir, ".yardr", "flows", "default.flow"), []byte("done terminal\n"), 0o644))
	line, err = api.Line(ctx, "docs")
	must(err)
	f = lineFlow(t, line, "default", "")
	if len(f.Warnings) != 1 || !strings.Contains(f.Warnings[0], "docs-a") {
		t.Errorf("directory flow warnings = %v, want the stranded docs-a", f.Warnings)
	}
}

// TestLineStoreFlowPackAndPlain pins the Packs join: a pack-managed store
// flow reads pack <name>, a plain one store, on one depot.
func TestLineStoreFlowPackAndPlain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api, _ := storetest.NewOwnedIn(t, t.TempDir())
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(api.PutDepot(ctx, store.Depot{Name: "shop"}, "me"))
	for _, name := range []string{"plain", "packed"} {
		f, err := store.ParseFlow("new -> done\ndone terminal\n")
		must(err)
		f.Name = name
		_, err = api.PutFlow(ctx, *f, "me")
		must(err)
	}
	must(api.SetFlowPointer(ctx, "shop", "", "plain", "me"))
	must(api.SetFlowPointer(ctx, "shop", "bug", "packed", "me"))
	if _, err := api.RecordPack(ctx, store.PackImport{Pack: "starter", Depot: "shop",
		Objects: []store.PackObject{{Kind: "flow", Name: "packed"}}}, true, "me"); err != nil {
		t.Fatal(err)
	}
	line, err := api.Line(ctx, "shop")
	must(err)
	if got := lineFlow(t, line, "plain", "").Source; got != "store" {
		t.Errorf("plain source = %q, want store", got)
	}
	if got := lineFlow(t, line, "packed", "bug").Source; got != "pack starter" {
		t.Errorf("packed source = %q, want pack starter", got)
	}
}

// TestLinePrompts pins prompts found in the home and in a pack, a digest
// prompt a card can carry, and an unreadable prompt that names the paths
// tried and fails nothing else.
func TestLinePrompts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api, st := storetest.NewOwnedIn(t, t.TempDir())
	home := st.HomeDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	dir := gitRepo(t, map[string]string{".yardr/flows/default.flow": "a -> b\nb -> c\nc -> d\nd terminal\n"})
	must(api.PutDepot(ctx, store.Depot{Name: "web", Path: dir, Base: "main"}, "me"))
	must(os.MkdirAll(filepath.Join(home, "roles"), 0o755))
	must(os.WriteFile(filepath.Join(home, "roles", "home.md"), []byte("Home prompt."), 0o644))
	packRole := filepath.Join(home, "packs", "starter", "roles", "packy.md")
	must(os.MkdirAll(filepath.Dir(packRole), 0o755))
	must(os.WriteFile(packRole, []byte("Pack prompt."), 0o644))
	digestText := "Digest prompt."
	sum := sha256.Sum256([]byte(digestText))
	digestPath := filepath.Join(home, "roles", "keepers."+hex.EncodeToString(sum[:])+".md")
	must(os.WriteFile(digestPath, []byte(digestText), 0o400))
	externalPath := filepath.Join(t.TempDir(), "external."+hex.EncodeToString(sum[:])+".md")
	must(os.WriteFile(externalPath, []byte(digestText), 0o400))
	groups := []store.Group{
		{Name: "homers", Runner: "herdr", Limit: 1, Config: map[string]any{"kind": "claude", "prompt": "roles/home.md"}},
		{Name: "packers", Runner: "herdr", Limit: 1, Config: map[string]any{"kind": "claude", "prompt": packRole}},
		{Name: "keepers", Runner: "herdr", Limit: 1, Config: map[string]any{"kind": "claude", "prompt": digestPath}},
		{Name: "external", Runner: "herdr", Limit: 1, Config: map[string]any{"kind": "claude", "prompt": externalPath}},
	}
	for _, g := range groups {
		must(api.PutGroup(ctx, g, "me"))
	}
	for i, stage := range []string{"a", "b", "c", "d"} {
		_, err := api.AddRoute(ctx, store.Route{Stage: stage, Group: groups[i].Name}, "me")
		must(err)
	}
	line, err := api.Line(ctx, "web")
	must(err)
	pa := lineStage(t, line, "default", "a").Route.Chain[0].Prompt
	if pa == nil || pa.Source != store.PromptHome || pa.Text != "Home prompt." || pa.Found != filepath.Join(home, "roles", "home.md") {
		t.Errorf("home prompt = %+v, want the home's copy", pa)
	}
	pb := lineStage(t, line, "default", "b").Route.Chain[0].Prompt
	if pb == nil || pb.Source != "pack starter" || pb.Text != "Pack prompt." || !pb.Carriable {
		t.Errorf("pack prompt = %+v, want the pack's copy, carriable", pb)
	}
	pc := lineStage(t, line, "default", "c").Route.Chain[0].Prompt
	if pc == nil || pc.Digest != hex.EncodeToString(sum[:]) || !pc.Carriable || pc.Why != "" || pc.Text != digestText {
		t.Errorf("digest prompt = %+v, want carriable with its digest", pc)
	}
	pd := lineStage(t, line, "default", "d").Route.Chain[0].Prompt
	if pd == nil || pd.Digest != "" || pd.Carriable {
		t.Errorf("external prompt = %+v, want no yard-written digest and no carriage", pd)
	}
}

// TestLinePromptCarriage pins the card's verdict on crew prompts (the
// third prompt kind): one a card can carry, and one refused for each of
// promptBefore's reasons beside the relative group prompt above.
func TestLinePromptCarriage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api, st := storetest.NewOwnedIn(t, t.TempDir())
	home := st.HomeDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, text string) string {
		t.Helper()
		must(os.MkdirAll(filepath.Dir(path), 0o755))
		must(os.WriteFile(path, []byte(text), 0o644))
		return path
	}
	good := write(filepath.Join(home, "roles", "good.md"), "Good prompt.")
	outside := write(filepath.Join(t.TempDir(), "outside.md"), "Outside prompt.")
	long := write(filepath.Join(home, "roles", "long.md"), strings.Repeat("x", store.MaxRunCrewRole+1))
	hidden := write(filepath.Join(home, "roles", "hidden.md"), "bad\x01text")
	members := []store.CrewMember{
		{Name: "good", Runner: "herdr", Config: map[string]any{"kind": "claude", "prompt": good}},
		{Name: "rel", Runner: "herdr", Config: map[string]any{"kind": "claude", "prompt": "roles/good.md"}},
		{Name: "out", Runner: "herdr", Config: map[string]any{"kind": "claude", "prompt": outside}},
		{Name: "long", Runner: "herdr", Config: map[string]any{"kind": "claude", "prompt": long}},
		{Name: "hidden", Runner: "herdr", Config: map[string]any{"kind": "claude", "prompt": hidden}},
		{Name: "gone", Runner: "herdr", Config: map[string]any{"kind": "claude", "prompt": filepath.Join(home, "roles", "gone.md")}},
	}
	for _, m := range members {
		if _, err := api.PutCrew(ctx, m, "me"); err != nil {
			t.Fatal(err)
		}
	}
	must(api.PutDepot(ctx, store.Depot{Name: "web"}, "me"))
	line, err := api.Line(ctx, "web")
	must(err)
	prompts := map[string]*store.LinePrompt{}
	for _, c := range line.Crew {
		prompts[c.Name] = c.Prompt
	}
	cases := []struct {
		name, why string
		carriable bool
	}{
		{"good", "", true},
		{"rel", "relative path", false},
		{"out", "does not stand as a regular file under the yard's home", false},
		{"long", "longer than the 8192 bytes a card shows", false},
		{"hidden", "not text a card shows as written", false},
	}
	for _, c := range cases {
		p := prompts[c.name]
		if p == nil {
			t.Errorf("%s: no prompt in %+v", c.name, line.Crew)
			continue
		}
		if p.Carriable != c.carriable || !strings.Contains(p.Why, c.why) {
			t.Errorf("%s carriage = %v/%q, want %v saying %q", c.name, p.Carriable, p.Why, c.carriable, c.why)
		}
	}
	if p := prompts["gone"]; p == nil || p.Error == "" || len(p.Tried) != 1 || !strings.HasSuffix(p.Tried[0], "gone.md") || p.Text != "" {
		t.Errorf("gone prompt = %+v, want the tried path and no text", p)
	}
	if p := prompts["good"]; p == nil || p.Text != "Good prompt." || p.Source != store.PromptFile {
		t.Errorf("good prompt = %+v, want its text read as a file", p)
	}
}

// TestLineUnreadableGroupPrompt pins a group prompt that cannot be read:
// it names the paths tried and the rest of the stage still answers.
func TestLineUnreadableGroupPrompt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api, st := storetest.NewOwnedIn(t, t.TempDir())
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	dir := gitRepo(t, map[string]string{
		".yardr/flows/default.flow": "new -> other\nother -> done\ndone terminal\n",
		".yardr/roles/worker.md":    "Base prompt.",
	})
	must(api.PutDepot(ctx, store.Depot{Name: "web", Path: dir, Base: "main"}, "me"))
	must(api.PutGroup(ctx, store.Group{Name: "lost", Runner: "herdr", Limit: 1,
		Config: map[string]any{"kind": "claude", "prompt": "roles/missing.md"}}, "me"))
	_, err := api.AddRoute(ctx, store.Route{Stage: "new", Group: "lost"}, "me")
	must(err)
	must(api.PutGroup(ctx, store.Group{Name: "plain", Runner: "herdr", Limit: 1,
		Config: map[string]any{"kind": "claude", "prompt": "plain.txt"}}, "me"))
	_, err = api.AddRoute(ctx, store.Route{Stage: "other", Group: "plain"}, "me")
	must(err)
	must(os.MkdirAll(filepath.Join(st.HomeDir(), "roles", "missing.md"), 0o755))
	must(os.MkdirAll(filepath.Join(st.HomeDir(), "plain.txt"), 0o755))
	line, err := api.Line(ctx, "web")
	must(err)
	stage := lineStage(t, line, "default", "new")
	if stage.Route == nil || len(stage.Route.Chain) != 1 {
		t.Fatalf("stage = %+v, want the route still answered", stage)
	}
	p := stage.Route.Chain[0].Prompt
	if p == nil || p.Text != "" || p.Error == "" || len(p.Tried) != 2 ||
		!strings.Contains(p.Tried[0], "main:.yardr/roles/missing.md") || !strings.Contains(p.Tried[1], "roles/missing.md") {
		t.Errorf("prompt = %+v, want the two paths tried and no text", p)
	}
	plain := lineStage(t, line, "default", "other").Route.Chain[0].Prompt
	if plain == nil || plain.Text != "" || plain.Error == "" || len(plain.Tried) != 2 ||
		!strings.Contains(plain.Tried[0], "plain.txt") || !strings.Contains(plain.Tried[1], "plain.txt") {
		t.Errorf("plain prompt = %+v, want both paths tried after a read error", plain)
	}
	// A canceled base read must not fall through to an uncommitted role in
	// a git depot: a brief and the Line read both use the base, never its tree.
	must(os.WriteFile(filepath.Join(dir, ".yardr", "roles", "worker.md"), []byte("Working tree prompt."), 0o644))
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	text, _, _, err := store.LookupPrompt(canceled, "roles/worker.md", &store.Depot{
		Name: "web", Path: dir, Kind: store.DepotGit, Base: "HEAD",
	}, st.HomeDir())
	if !errors.Is(err, context.Canceled) || len(text) != 0 {
		t.Errorf("canceled base prompt read = %q, %v; want cancellation without the working-tree prompt", text, err)
	}
}
