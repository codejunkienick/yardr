package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/config"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
)

// sourcesYard is stageYard's depot "web" with a role and a stage file of its
// own on the base, and an agent group its review stage is routed to.
func sourcesYard(t *testing.T) (a *app, home, sha string) {
	t.Helper()
	a, repo, home, git, write := stageYard(t)
	ctx := context.Background()
	write(repo, map[string]string{
		".yardr/flows/default/review.md":    "the depot's review\n",
		".yardr/flows/default/new.md":       "the depot's new\n",
		".yardr/flows/type/decision/ask.md": "the depot's ask\n",
		".yardr/roles/builder.md":           "the depot's builder\n",
		".yardr/roles/reviewer.md":          "the depot's reviewer\n",
	})
	git("add", "-A")
	git("commit", "-q", "-m", "roles and stage files")
	for _, g := range []store.Group{
		{Name: "web-builders", Runner: "herdr", Config: map[string]any{"kind": "claude", "prompt": "roles/builder.md"}, Limit: 1},
		{Name: "web-reviewers", Runner: "herdr", Config: map[string]any{"kind": "claude", "prompt": "roles/reviewer.md"}, Limit: 1},
	} {
		if err := a.st.PutGroup(ctx, g, "me"); err != nil {
			t.Fatal(err)
		}
	}
	// Imports run as the operator here, as an operator's import does.
	if err := a.st.SetSetting(ctx, store.OperatorSetting, "me", "me"); err != nil {
		t.Fatal(err)
	}
	depot := "web"
	for stage, group := range map[string]string{"new": "web-builders", "review": "web-reviewers"} {
		if _, err := a.st.AddRoute(ctx, store.Route{Stage: stage, Depot: &depot, Group: group}, "me"); err != nil {
			t.Fatal(err)
		}
	}
	return a, home, git("rev-parse", "HEAD")[:7]
}

// differsWords is what a line says of a flow or stage file in the store that
// reads other than the repository's file it stands in front of.
const differsWords = "differs from the repository"

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// sourceLines are the listing's lines with their columns joined by one space, so
// a test does not depend on the widths.
func sourceLines(t *testing.T, out string) []string {
	t.Helper()
	var got []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n")[1:] {
		got = append(got, strings.Join(strings.Fields(l), " "))
	}
	return got
}

func wantLines(t *testing.T, out string, want ...string) {
	t.Helper()
	got := sourceLines(t, out)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("depot sources:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A depot governed by its repository alone: every flow, stage file and role
// is the depot's, at the base commit, and nothing is shadowed.
func TestDepotSourcesRepositoryAlone(t *testing.T) {
	t.Parallel()
	a, _, sha := sourcesYard(t)
	out, err := run(t, a.depotSourcesCmd(), "web")
	if err != nil {
		t.Fatal(err)
	}
	wantLines(t, out,
		"flow default depot (.yardr/flows/default.flow@"+sha+")",
		"stage default:new depot (.yardr/flows/default/new.md@"+sha+")",
		"stage default:review depot (.yardr/flows/default/review.md@"+sha+")",
		"flow type/decision depot (.yardr/flows/type/decision.flow@"+sha+")",
		"stage decision:ask depot (.yardr/flows/type/decision/ask.md@"+sha+")",
		"role web-builders depot (.yardr/roles/builder.md@"+sha+")",
		"role web-reviewers depot (.yardr/roles/reviewer.md@"+sha+")",
		"rules web none",
	)
	if strings.Contains(out, "shadows") {
		t.Errorf("a depot governed by its repository shadows nothing:\n%s", out)
	}
}

// A depot with a pack applied: the pack's flow, and its roles, stand in front
// of the depot's files, and each such file is named where it is shadowed: the
// flow file and the type's, the stage files, the roles.
func TestDepotSourcesAppliedPack(t *testing.T) {
	t.Parallel()
	a, home, _ := sourcesYard(t)
	requireNonInteractive(t)
	if out, err := run(t, a.packCmd(), append([]string{"apply", "../../packs/starter", "--depot", "web", "--yes"}, starterFlags...)...); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// The pack's stage files are in the yard's home; one is taken away, so
	// a stage of the pack's flow without a file is seen beside those with.
	stages := filepath.Join(home, "flows", "starter.default")
	trainStages := filepath.Join(home, "flows", "starter.train")
	stageFile := filepath.Join(stages, "review.md")
	if err := os.Remove(filepath.Join(stages, "new.md")); err != nil {
		t.Fatal(err)
	}
	packs := config.At(home).PackRoles("starter")

	out, err := run(t, a.depotSourcesCmd(), "web")
	if err != nil {
		t.Fatal(err)
	}
	wantLines(t, out,
		"flow default pack starter (store starter.default) shadows .yardr/flows/default.flow, .yardr/flows/type/decision.flow, .yardr/flows/type/decision/ask.md; "+differsWords,
		"stage default:backlog pack starter ("+filepath.Join(stages, "backlog.md")+") "+differsWords,
		"stage default:new none shadows .yardr/flows/default/new.md; "+differsWords,
		"stage default:review pack starter ("+stageFile+") shadows .yardr/flows/default/review.md; "+differsWords,
		"stage default:review-security pack starter ("+filepath.Join(stages, "review-security.md")+") "+differsWords,
		"stage default:clear pack starter ("+filepath.Join(stages, "clear.md")+") "+differsWords,
		"stage default:approved pack starter ("+filepath.Join(stages, "approved.md")+") "+differsWords,
		"stage default:decide pack starter ("+filepath.Join(stages, "decide.md")+") "+differsWords,
		"flow type/train pack starter (store starter.train)",
		"stage train:backlog none", "stage train:open none", "stage train:review none", "stage train:review-security none", "stage train:approved none",
		"stage train:in-pr pack starter ("+filepath.Join(trainStages, "in-pr.md")+")", "stage train:land none", "stage train:merge pack starter ("+filepath.Join(trainStages, "merge.md")+")", "stage train:decide none",
		"role web-builders pack starter ("+filepath.Join(packs, "builder.md")+") shadows .yardr/roles/builder.md",
		"role web-reviewers pack starter ("+filepath.Join(packs, "reviewer.md")+") shadows .yardr/roles/reviewer.md",
		"role web-security-reviewers pack starter ("+filepath.Join(packs, "review-security.md")+")",
		"role boss none",
		"rules web none",
	)

	a.json = true
	out, _ = run(t, a.depotSourcesCmd(), "web")
	a.json = false
	var lines []depotSource
	if err := json.Unmarshal([]byte(out), &lines); err != nil || len(lines) != 23 {
		t.Fatalf("--json: %v\n%s", err, out)
	}
	if l := slices.IndexFunc(lines, func(l depotSource) bool {
		return l.Kind == "stage" && l.Name == "review" && l.Flow == "starter.default"
	}); l < 0 || lines[l].Source != sourcePack || lines[l].Pack != "starter" ||
		lines[l].Path != stageFile || len(lines[l].Shadows) != 1 || lines[l].Shadows[0] != ".yardr/flows/default/review.md" || !lines[l].Differs {
		t.Errorf("--json review not found: %+v", lines)
	}
}

// A mix: the pack's flow governs, and the depot keeps its own roles
// (--depot-roles). The role's line says the pack has a copy; a crew member
// is the yard's, and the starter sets no prompt for its yardmaster either
// way. Clearing the pointer gives the flow
// back to the repository, and the line says the pack's is still there.
func TestDepotSourcesPackFlowDepotRoles(t *testing.T) {
	t.Parallel()
	a, home, sha := sourcesYard(t)
	requireNonInteractive(t)
	if out, err := run(t, a.packCmd(), append([]string{"apply", "../../packs/starter", "--depot", "web", "--yes", "--depot-roles"}, starterFlags...)...); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// The pack's stage files come with its flow, whoever's the roles are.
	stages := filepath.Join(home, "flows", "starter.default")
	trainStages := filepath.Join(home, "flows", "starter.train")
	out, err := run(t, a.depotSourcesCmd(), "web")
	if err != nil {
		t.Fatal(err)
	}
	wantLines(t, out,
		"flow default pack starter (store starter.default) shadows .yardr/flows/default.flow, .yardr/flows/type/decision.flow, .yardr/flows/type/decision/ask.md; "+differsWords,
		"stage default:backlog pack starter ("+filepath.Join(stages, "backlog.md")+") "+differsWords,
		"stage default:new pack starter ("+filepath.Join(stages, "new.md")+") shadows .yardr/flows/default/new.md; "+differsWords,
		"stage default:review pack starter ("+filepath.Join(stages, "review.md")+") shadows .yardr/flows/default/review.md; "+differsWords,
		"stage default:review-security pack starter ("+filepath.Join(stages, "review-security.md")+") "+differsWords,
		"stage default:clear pack starter ("+filepath.Join(stages, "clear.md")+") "+differsWords,
		"stage default:approved pack starter ("+filepath.Join(stages, "approved.md")+") "+differsWords,
		"stage default:decide pack starter ("+filepath.Join(stages, "decide.md")+") "+differsWords,
		"flow type/train pack starter (store starter.train)",
		"stage train:backlog none", "stage train:open none", "stage train:review none", "stage train:review-security none", "stage train:approved none",
		"stage train:in-pr pack starter ("+filepath.Join(trainStages, "in-pr.md")+")", "stage train:land none", "stage train:merge pack starter ("+filepath.Join(trainStages, "merge.md")+")", "stage train:decide none",
		"role web-builders depot (.yardr/roles/builder.md@"+sha+") pack starter has a copy",
		"role web-reviewers depot (.yardr/roles/reviewer.md@"+sha+") pack starter has a copy",
		"role web-security-reviewers pack starter ("+filepath.Join(config.At(home).PackRoles("starter"), "review-security.md")+")",
		"role boss none",
		"rules web none",
	)

	if err := a.st.SetFlowPointer(context.Background(), "web", "", "", "me"); err != nil {
		t.Fatal(err)
	}
	if out, err = run(t, a.depotSourcesCmd(), "web"); err != nil {
		t.Fatal(err)
	}
	if got := sourceLines(t, out)[0]; got != "flow default depot (.yardr/flows/default.flow@"+sha+") pack starter has flow starter.default" {
		t.Errorf("after clearing the pointer: %s", got)
	}
}

// A pointer at a store flow no pack brought is the store's, its stage files
// the home's; a prompt under the home's roles is the home's, and shadows the
// depot's role of the name when it is named by its path.
func TestDepotSourcesStoreFlowAndHomeRole(t *testing.T) {
	t.Parallel()
	a, home, _ := sourcesYard(t)
	ctx := context.Background()
	if err := a.st.SetFlowPointer(ctx, "web", "", "code", "me"); err != nil {
		t.Fatal(err)
	}
	stageFile := filepath.Join(home, "flows", "code", "new.md")
	role := filepath.Join(home, "roles", "builder.md")
	writeFile(t, stageFile, "the yard's new\n")
	writeFile(t, role, "the yard's builder\n")
	g, err := a.st.GetGroup(ctx, "web-builders")
	if err != nil {
		t.Fatal(err)
	}
	g.Config["prompt"] = role
	if err := a.st.PutGroup(ctx, *g, "me"); err != nil {
		t.Fatal(err)
	}
	g.Name, g.Config = "web-lost", map[string]any{"kind": "claude", "prompt": "roles/nobody.md"}
	if err := a.st.PutGroup(ctx, *g, "me"); err != nil {
		t.Fatal(err)
	}
	depot := "web"
	if _, err := a.st.AddRoute(ctx, store.Route{Stage: "elsewhere", Depot: &depot, Group: "web-lost"}, "me"); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, a.depotSourcesCmd(), "web")
	if err != nil {
		t.Fatal(err)
	}
	got := sourceLines(t, out)
	for i, want := range []string{
		"flow default store (store code) shadows .yardr/flows/default.flow, .yardr/flows/type/decision.flow, .yardr/flows/type/decision/ask.md",
		"stage new home (" + stageFile + ") shadows .yardr/flows/default/new.md",
		"stage review none shadows .yardr/flows/default/review.md",
		"role web-builders home (" + role + ") shadows .yardr/roles/builder.md",
		"role web-reviewers depot (.yardr/roles/reviewer.md@",
	} {
		if i >= len(got) || !strings.HasPrefix(got[i], want) {
			t.Fatalf("line %d: want %q in:\n%s", i, want, strings.Join(got, "\n"))
		}
	}
	// A prompt nobody has is said, not an error of the whole listing.
	if last := got[len(got)-2]; !strings.HasPrefix(last, "role web-lost none: roles/nobody.md not found") {
		t.Errorf("a missing prompt: %s", last)
	}
	if last := got[len(got)-1]; last != "rules web none" {
		t.Errorf("a depot without the file: %s", last)
	}
}

// A flow copied from the repository reads as the repository's does, and its
// lines say nothing of it. Once the yard's copy or the repository moves, the
// flow's line and the line of each stage whose file differs say "differs from
// the repository" (and --json says differs), and they say nothing again where
// the two read alike: a stage file the yard has and the repository has none
// of, and one the repository has and the yard has none of, included.
func TestDepotSourcesSaysWhereACopyDiffersFromTheRepository(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	st := storetest.NewIn(t, home)
	dir := copyRepoFiles(t, map[string]string{
		"default.flow":       "backlog -> new\nnew -> review\nreview -> done\ndone terminal\n",
		"default/backlog.md": "Wait here.\n",
		"default/new.md":     "Build it.\n",
	})
	if err := st.PutDepot(context.Background(), store.Depot{Name: "web", Kind: store.DepotDir, Path: dir}, "me"); err != nil {
		t.Fatal(err)
	}
	a := &app{st: st, actor: "me", cfg: &config.Config{Home: home}}
	if out, err := run(t, a.flowCmd(), "copy-repo", "web"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	copied := func(stage string) string { return filepath.Join(home, "flows", "web.default", stage+".md") }
	repo := func(stage string) string { return filepath.Join(dir, store.RepoFlowDir, "default", stage+".md") }
	sources := func() string {
		t.Helper()
		out, err := run(t, a.depotSourcesCmd(), "web")
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	// differing names, as --json has them, the lines that say they differ.
	differing := func() (names []string, raw string) {
		t.Helper()
		a.json = true
		defer func() { a.json = false }()
		raw = sources()
		var lines []depotSource
		if err := json.Unmarshal([]byte(raw), &lines); err != nil {
			t.Fatalf("--json: %v\n%s", err, raw)
		}
		for _, l := range lines {
			if l.Differs {
				names = append(names, l.Kind+" "+l.Name)
			}
		}
		return names, raw
	}
	want := func(names ...string) {
		t.Helper()
		if got, raw := differing(); !slices.Equal(got, names) {
			t.Errorf("the lines that differ are %v, want %v:\n%s", got, names, raw)
		}
	}

	same := []string{
		"flow default store (store web.default) shadows .yardr/flows/default.flow",
		"stage backlog home (" + copied("backlog") + ") shadows .yardr/flows/default/backlog.md",
		"stage new home (" + copied("new") + ") shadows .yardr/flows/default/new.md",
		"stage review none",
		"rules web none",
	}
	wantLines(t, sources(), same...)
	if names, raw := differing(); len(names) != 0 || strings.Contains(raw, `"differs"`) {
		t.Errorf("a copy that is the repository's says it differs: %v\n%s", names, raw)
	}

	// The yard's copy is edited, and then put back.
	writeFile(t, copied("new"), "Build it, then test it.\n")
	wantLines(t, sources(),
		same[0]+"; "+differsWords, same[1], same[2]+"; "+differsWords, same[3], same[4])
	want("flow default", "stage new")
	writeFile(t, copied("new"), "Build it.\n")
	want()

	// The repository moves, and moves back.
	writeFile(t, repo("backlog"), "Wait over there.\n")
	want("flow default", "stage backlog")
	writeFile(t, repo("backlog"), "Wait here.\n")
	want()

	// The yard has a file for a stage the repository has none for, and has
	// none for a stage the repository has one.
	writeFile(t, copied("review"), "Review it.\n")
	if err := os.Remove(copied("backlog")); err != nil {
		t.Fatal(err)
	}
	wantLines(t, sources(),
		same[0]+"; "+differsWords,
		"stage backlog none shadows .yardr/flows/default/backlog.md; "+differsWords,
		same[2],
		"stage review home ("+copied("review")+") "+differsWords,
		same[4])
	want("flow default", "stage backlog", "stage review")
}

func TestDepotSourcesIncludesAllPotentialRouteGroups(t *testing.T) {
	t.Parallel()
	a, _, _ := sourcesYard(t)
	ctx := context.Background()
	for _, name := range []string{"web-specialists", "yard-general", "yard-unlisted"} {
		if err := a.st.PutGroup(ctx, store.Group{Name: name, Runner: "herdr", Config: map[string]any{"kind": "claude", "prompt": "roles/reviewer.md"}}, "me"); err != nil {
			t.Fatal(err)
		}
	}
	depot := "web"
	label := "special"
	for _, r := range []store.Route{
		// A label-specific route may win for beads that carry this label,
		// even though the ordinary route also matches the review stage.
		{Stage: "review", Depot: &depot, Label: &label, Group: "web-specialists"},
		// Unscoped routes apply to beads in this depot too, including a stage
		// outside the current flow.
		{Stage: "review", Group: "yard-general"},
		{Stage: "unlisted", Group: "yard-unlisted"},
	} {
		if _, err := a.st.AddRoute(ctx, r, "me"); err != nil {
			t.Fatal(err)
		}
	}
	out, err := run(t, a.depotSourcesCmd(), "web")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(sourceLines(t, out), "\n")
	for _, name := range []string{"web-specialists", "yard-general", "yard-unlisted"} {
		if !strings.Contains(got, "role "+name+" depot (.yardr/roles/reviewer.md@") {
			t.Errorf("potential route group %s is missing:\n%s", name, got)
		}
	}
}

// rulesLine is the listing's rules line, the one per depot.
func rulesLine(t *testing.T, out string) string {
	t.Helper()
	for _, l := range sourceLines(t, out) {
		if strings.HasPrefix(l, "rules ") {
			return l
		}
	}
	t.Fatalf("no rules line in:\n%s", out)
	return ""
}

// rulesJSON is the listing's rules entry in --json.
func rulesJSON(t *testing.T, out string) depotSource {
	t.Helper()
	var lines []depotSource
	if err := json.Unmarshal([]byte(out), &lines); err != nil {
		t.Fatalf("--json: %v\n%s", err, out)
	}
	if i := slices.IndexFunc(lines, func(l depotSource) bool { return l.Kind == "rules" }); i >= 0 {
		return lines[i]
	}
	t.Fatalf("--json has no rules entry:\n%s", out)
	return depotSource{}
}

// The depot's own standing rules are one line per depot: the file on the
// base, at its commit, or none where the depot keeps no file, and --json
// carries the same. A dir depot's file is read from its directory, at no
// commit.
func TestDepotSourcesRules(t *testing.T) {
	t.Parallel()
	a, repo, _, git, write := stageYard(t)
	ctx := context.Background()

	sources := func(depot string) (text, js string) {
		t.Helper()
		out, err := run(t, a.depotSourcesCmd(), depot)
		if err != nil {
			t.Fatal(err)
		}
		a.json = true
		jout, err := run(t, a.depotSourcesCmd(), depot)
		a.json = false
		if err != nil {
			t.Fatal(err)
		}
		return out, jout
	}

	out, jout := sources("web")
	if got := rulesLine(t, out); got != "rules web none" {
		t.Errorf("without the file: %s", got)
	}
	if l := rulesJSON(t, jout); l.Name != "web" || l.Source != sourceNone || l.Path != "" || l.Commit != "" {
		t.Errorf("without the file, --json: %+v", l)
	}

	write(repo, map[string]string{".yardr/depot.md": "work clean\n"})
	git("add", "-A")
	git("commit", "-q", "-m", "depot rules")
	sha := git("rev-parse", "HEAD")

	out, jout = sources("web")
	if got := rulesLine(t, out); got != "rules web depot (.yardr/depot.md@"+sha[:7]+")" {
		t.Errorf("with the file: %s", got)
	}
	if l := rulesJSON(t, jout); l.Name != "web" || l.Source != sourceDepot || l.Path != ".yardr/depot.md" || l.Commit != sha {
		t.Errorf("with the file, --json: %+v", l)
	}

	api, err := a.st.GetDepot(ctx, "api")
	if err != nil {
		t.Fatal(err)
	}
	write(api.Path, map[string]string{".yardr/depot.md": "dir rules\n"})
	out, jout = sources("api")
	if got := rulesLine(t, out); got != "rules api depot (.yardr/depot.md)" {
		t.Errorf("a dir depot: %s", got)
	}
	if l := rulesJSON(t, jout); l.Name != "api" || l.Source != sourceDepot || l.Path != ".yardr/depot.md" || l.Commit != "" {
		t.Errorf("a dir depot, --json: %+v", l)
	}
}
