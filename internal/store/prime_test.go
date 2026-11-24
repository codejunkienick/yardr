package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
)

// Prime answers the role of a crew member (its prompt under its dir, as the
// config names it, else as it stands), what a prime says of the operator,
// the memory titles and each depot's rules. A member the yard has not is
// refused as CrewPrimeFor refuses it, and a role that cannot be read fails
// the read in the prime's own words.
func TestPrimeAnswersWhatASessionReadsAtPrime(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, text string) {
		t.Helper()
		must(os.MkdirAll(filepath.Dir(path), 0o755))
		must(os.WriteFile(path, []byte(text), 0o644))
	}
	put := func(name, dir, prompt string) {
		t.Helper()
		conf := map[string]any{"kind": "claude"}
		if prompt != "" {
			conf["prompt"] = prompt
		}
		_, err := s.PutCrew(ctx, store.CrewMember{Name: name, Runner: "herdr", Dir: dir, Config: conf}, "me")
		must(err)
	}

	// The role: a relative prompt is read under the member's dir, an
	// absolute one as it stands, and the text is trimmed as the prime trims
	// it.
	dir := t.TempDir()
	write(filepath.Join(dir, "roles", "dispatcher.md"), "\n# Dispatcher\n\nKeep the lamps lit.\n\n")
	kept := filepath.Join(t.TempDir(), "keeper.md")
	write(kept, "Keep the books.\n")
	put("dispatcher", dir, "roles/dispatcher.md")
	put("keeper", "", kept)
	put("clerk", "", "")
	put("lost", dir, "roles/gone.md")
	for _, c := range []struct {
		crew string
		want *store.PrimeFile
	}{
		{"dispatcher", &store.PrimeFile{Path: filepath.Join(dir, "roles", "dispatcher.md"), Text: "# Dispatcher\n\nKeep the lamps lit."}},
		{"keeper", &store.PrimeFile{Path: kept, Text: "Keep the books."}},
		{"clerk", nil},
	} {
		p, err := s.Prime(ctx, c.crew)
		if err != nil {
			t.Fatalf("Prime %s: %v", c.crew, err)
		}
		if (p.Role == nil) != (c.want == nil) || p.Role != nil && *p.Role != *c.want {
			t.Errorf("the role of %s is %+v, want %+v", c.crew, p.Role, c.want)
		}
	}
	if _, err := s.Prime(ctx, "lost"); err == nil || !strings.Contains(err.Error(), "crew member lost prompt:") || !strings.Contains(err.Error(), "no such file") {
		t.Errorf("a role that is gone: %v, want the prime's words and the file's absence", err)
	}
	if _, err := s.Prime(ctx, "nobody"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a crew member the yard has not: %v, want it not found", err)
	}

	// The operator: nothing on a yard with neither setting; each style
	// stands for its answer and what it asks of an agent; the note is
	// trimmed and may stand alone.
	if p, err := s.Prime(ctx, "clerk"); err != nil || p.Operator != nil {
		t.Errorf("a yard with neither setting says %+v, %v of the operator", p.Operator, err)
	}
	must(s.SetSetting(ctx, store.OperatorSetting, "jo", "me")) // the first setting is anyone's; the rest are the operator's
	must(s.SetSetting(ctx, store.NoteSetting, "I read diffs, not prose", "jo"))
	if p, err := s.Prime(ctx, "clerk"); err != nil || p.Operator == nil || *p.Operator != (store.PrimeOperator{Name: "jo", Note: "I read diffs, not prose"}) {
		t.Errorf("a note alone: %+v, %v", p.Operator, err)
	}
	for _, style := range store.OperatorStyles {
		must(s.SetSetting(ctx, store.StyleSetting, style, "jo"))
		p, err := s.Prime(ctx, "clerk")
		if err != nil || p.Operator == nil {
			t.Fatalf("style %s: %+v, %v", style, p.Operator, err)
		}
		if o := p.Operator; o.Name != "jo" || o.Style != style || o.Label == "" || o.Tells == "" || o.Note != "I read diffs, not prose" {
			t.Errorf("style %s: %+v", style, o)
		}
	}

	// The memories: the member's own and the yard's, titles only.
	_, err := s.Remember(ctx, store.NewMemory{Title: "Lamps are lit at dusk", Body: "the body stays in the yard", Scope: "all"}, "me")
	must(err)
	_, err = s.Remember(ctx, store.NewMemory{Title: "The box is shut", Scope: "crew:dispatcher"}, "me")
	must(err)
	_, err = s.Remember(ctx, store.NewMemory{Title: "Someone else's", Scope: "crew:keeper"}, "me")
	must(err)
	p, err := s.Prime(ctx, "dispatcher")
	must(err)
	titles := map[string]bool{}
	for _, m := range p.Memories {
		titles[m.Title] = true
	}
	if len(p.Memories) != 2 || !titles["The box is shut"] || !titles["Lamps are lit at dusk"] || p.MemoriesOmitted != 0 {
		t.Errorf("memories = %+v, omitted %d, want the two it sees", p.Memories, p.MemoriesOmitted)
	}
	if !slices.IsSortedFunc(p.Memories, func(a, b store.MemoryInfo) int { return b.UpdatedAt.Compare(a.UpdatedAt) }) {
		t.Errorf("memories = %+v, want the newest first", p.Memories)
	}
	if raw, err := json.Marshal(p); err != nil || strings.Contains(string(raw), "the body stays in the yard") {
		t.Errorf("the read carries a memory's body: %s, %v", raw, err)
	}
	if p, err := s.Prime(ctx, "clerk"); err != nil || p.Memories == nil || len(p.Memories) != 1 {
		t.Errorf("the memories of a member with one of its own scope: %+v, %v", p.Memories, err)
	}

	// The rules: AGENTS.md then CLAUDE.md at the root of each checkout, as
	// regular files; a link is followed while it stays inside the checkout;
	// a depot with none, or whose checkout is gone, has none and no error.
	depot := func(name string, files map[string]string, links map[string]string) store.Depot {
		t.Helper()
		root := t.TempDir()
		for file, text := range files {
			write(filepath.Join(root, file), text)
		}
		for link, target := range links {
			must(os.Symlink(target, filepath.Join(root, link)))
		}
		d := store.Depot{Name: name, Path: root, Kind: store.DepotDir}
		must(s.PutDepot(ctx, d, "jo"))
		return d
	}
	outside := filepath.Join(t.TempDir(), "secret.md")
	write(outside, "not a depot's to show")
	both := depot("both", map[string]string{"AGENTS.md": "Agents rule.\n", "CLAUDE.md": "@AGENTS.md\n", ".yardr/depot.md": "How the yard works it.\n"}, nil)
	claude := depot("claudeonly", map[string]string{"CLAUDE.md": "Claude rules.\n"}, nil)
	depot("none", nil, nil)
	linked := depot("linked", map[string]string{"AGENTS.md": "Linked rules.\n"}, map[string]string{"CLAUDE.md": "AGENTS.md"})
	escaping := depot("escaping", map[string]string{"AGENTS.md": "Kept.\n"}, map[string]string{"CLAUDE.md": outside})
	depot("directory", map[string]string{"AGENTS.md/inner.md": "a directory is no rules file\n"}, nil)
	gone := depot("gone", map[string]string{"AGENTS.md": "Gone with it.\n"}, nil)
	must(os.RemoveAll(gone.Path))
	file := func(d store.Depot, name, text string) store.PrimeFile {
		return store.PrimeFile{Path: filepath.Join(d.Path, name), Text: text}
	}
	p, err = s.Prime(ctx, "clerk")
	must(err)
	got := map[string][]store.PrimeFile{}
	var order []string
	for _, d := range p.Depots {
		if d.Rules == nil {
			t.Errorf("depot %s has no rules as null, want an empty list", d.Name)
		}
		got[d.Name] = d.Rules
		order = append(order, d.Name)
	}
	if want := []string{"both", "claudeonly", "directory", "escaping", "gone", "linked", "none"}; strings.Join(order, " ") != strings.Join(want, " ") {
		t.Errorf("depots = %v, want every depot by name %v", order, want)
	}
	for name, want := range map[string][]store.PrimeFile{
		"both":       {file(both, "AGENTS.md", "Agents rule.\n"), file(both, "CLAUDE.md", "@AGENTS.md\n")},
		"claudeonly": {file(claude, "CLAUDE.md", "Claude rules.\n")},
		"none":       {},
		"linked":     {file(linked, "AGENTS.md", "Linked rules.\n"), file(linked, "CLAUDE.md", "Linked rules.\n")},
		"escaping":   {file(escaping, "AGENTS.md", "Kept.\n")},
		"directory":  {},
		"gone":       {},
	} {
		if len(got[name]) != len(want) {
			t.Errorf("depot %s has rules %+v, want %+v", name, got[name], want)
			continue
		}
		for i := range want {
			if got[name][i] != want[i] {
				t.Errorf("depot %s rule %d = %+v, want %+v", name, i, got[name][i], want[i])
			}
		}
	}

	// A rules file no session could take refuses the read, naming it, and
	// no other part is answered with a depot short of what its agents read.
	big := depot("big", map[string]string{"AGENTS.md": strings.Repeat("x", 1<<20+1)}, nil)
	if _, err := s.Prime(ctx, "clerk"); err == nil || !strings.Contains(err.Error(), "depot big") || !strings.Contains(err.Error(), filepath.Join(big.Path, "AGENTS.md")) {
		t.Errorf("a rules file over the bound: %v, want the depot and the file named", err)
	}
}

// With no operator setting a prime names the login as the CLI's does, so far
// as the store can: the first of $USER and $LOGNAME that a user may be
// called, else "operator" (the OS's own lookup is config's, which the store
// may not import).
func TestPrimeNamesTheLoginWhereNoOperatorIsSet(t *testing.T) {
	s := storetest.New(t)
	ctx := context.Background()
	if _, err := s.PutCrew(ctx, store.CrewMember{Name: "clerk", Runner: "herdr", Config: map[string]any{"kind": "claude"}}, "me"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, store.NoteSetting, "I am on call", "me"); err != nil { // the first setting is anyone's
		t.Fatal(err)
	}
	for _, c := range []struct{ user, logname, want string }{
		{"ada", "hal", "ada"},
		{"not a name", "hal", "hal"},
		{"", "hal", "hal"},
		{"", "", "operator"},
	} {
		t.Setenv("USER", c.user)
		t.Setenv("LOGNAME", c.logname)
		p, err := s.Prime(ctx, "clerk")
		if err != nil || p.Operator == nil || p.Operator.Name != c.want {
			t.Errorf("USER %q, LOGNAME %q: the operator is %+v, %v, want %s", c.user, c.logname, p.Operator, err, c.want)
		}
	}
}

// A role's First run section is folded once the setup is done, and not
// before: a yard with no depot and no work that needs one is set up with
// none; work with no depot starts the depot steps; a depot whose flow the
// store holds needs its routes, a bead through to the flow's end and, being
// a directory, no route for approved. internal/cli holds the same walk to
// what `yardr prime --crew` prints, with a repository's depot too.
func TestPrimeFoldsTheFirstRunSectionOnceTheSetupIsDone(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	role := filepath.Join(t.TempDir(), "role.md")
	whole := "# Yardmaster\n\nGo on.\n\n## First run\n\nSet the yard up.\n\n## The work\n\nKeep it moving.\n"
	must(os.WriteFile(role, []byte(whole), 0o644))
	_, err := s.PutCrew(ctx, store.CrewMember{Name: "yardmaster", Runner: "herdr", Config: map[string]any{"kind": "claude", "prompt": role}}, "me")
	must(err)
	folded := "# Yardmaster\n\nGo on.\n\n" + "The setup is done (`yardr doctor`); the steps are in `yardr docs setup`, should a depot be added." + "\n\n## The work\n\nKeep it moving."
	check := func(when string, want string) {
		t.Helper()
		p, err := s.Prime(ctx, "yardmaster")
		if err != nil {
			t.Fatalf("%s: %v", when, err)
		}
		if p.Role == nil || p.Role.Text != want {
			t.Errorf("%s: the role is %+v, want\n%s", when, p.Role, want)
		}
	}
	check("a fresh yard", folded)

	_, err = s.CreateBead(ctx, store.NewBead{Title: "work before the depot"}, "me")
	must(err)
	whole = strings.TrimSpace(whole)
	check("work with no depot", whole)

	docs := store.Depot{Name: "docs", Path: t.TempDir(), Kind: store.DepotDir}
	must(s.PutDepot(ctx, docs, "me"))
	check("a depot with no flow", whole)

	_, err = s.PutFlow(ctx, store.Flow{Name: "docs", Stages: []string{"new", "review", "done"},
		Transitions: map[string][]string{"new": {"review"}, "review": {"done"}}, Terminal: []string{"done"}}, "me")
	must(err)
	must(s.SetFlowPointer(ctx, "docs", "", "docs", "me"))
	check("a depot that follows a flow, with no route", whole)

	for _, g := range []string{"builders", "reviewers"} {
		must(s.PutGroup(ctx, store.Group{Name: g, Runner: "herdr", Config: map[string]any{"kind": "claude"}}, "me"))
	}
	for stage, group := range map[string]string{"new": "builders", "review": "reviewers"} {
		_, err := s.AddRoute(ctx, store.Route{Stage: stage, Depot: &docs.Name, Group: group}, "me")
		must(err)
	}
	check("routes for new and review, and no bead through", whole)

	b, err := s.CreateBead(ctx, store.NewBead{Title: "first", Depot: "docs"}, "me")
	must(err)
	_, err = s.Advance(ctx, b.ID, "review", store.Expect{}, "me") // born at the flow's first stage, new
	must(err)
	check("a bead at review", whole)
	_, err = s.Advance(ctx, b.ID, "done", store.Expect{}, "me")
	must(err)
	check("a bead through to the flow's end", folded)
}
