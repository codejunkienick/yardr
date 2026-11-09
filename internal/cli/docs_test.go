package cli

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"yardr.dev/yardr/docs"
	"yardr.dev/yardr/packs"
)

// yardr docs lists every page the binary carries, each with its first
// heading, in the index's order: on a home with no yard, creating
// nothing there.
func TestDocsListNamesEveryPage(t *testing.T) {
	noHome(t)
	requireNonInteractive(t)
	out, code := executeOut(&app{}, "docs")
	if code != 0 {
		t.Fatalf("yardr docs: exit %d\n%s", code, out)
	}
	at := 0
	for _, name := range docs.Order {
		title, ok := docs.Title(name)
		if !ok {
			t.Fatalf("docs.Title(%q) finds nothing", name)
		}
		i := strings.Index(out[at:], name)
		j := strings.Index(out[at:], title)
		if i < 0 || j < 0 || j < i {
			t.Fatalf("yardr docs lists no %q under %q after %d:\n%s", title, name, at, out)
		}
		at += j + len(title)
	}
	var listed struct {
		Docs []struct {
			Name  string `json:"name"`
			Title string `json:"title"`
		} `json:"docs"`
	}
	out, code = executeOut(&app{}, "docs", "--json")
	if code != 0 {
		t.Fatalf("yardr docs --json: exit %d\n%s", code, out)
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("yardr docs --json is no JSON: %v\n%s", err, out)
	}
	if len(listed.Docs) != len(docs.Order) {
		t.Fatalf("yardr docs --json lists %d pages, want %d", len(listed.Docs), len(docs.Order))
	}
}

// yardr docs <name> prints the page, in any case; an unknown name is
// refused with the names there are.
func TestDocsShowPrintsOnePage(t *testing.T) {
	noHome(t)
	requireNonInteractive(t)
	out, code := executeOut(&app{}, "docs", "flows")
	if code != 0 {
		t.Fatalf("yardr docs flows: exit %d\n%s", code, out)
	}
	want, _ := docs.Read("flows")
	if out != want {
		t.Fatalf("yardr docs flows prints %d bytes, the page holds %d", len(out), len(want))
	}
	if out, code := executeOut(&app{}, "docs", "Readme"); code != 0 || !strings.HasPrefix(out, "# yardr documentation\n") {
		t.Fatalf("yardr docs Readme: exit %d, %.40q", code, out)
	}
	var errs bytes.Buffer
	out, code = executeOut(&app{stderr: &errs}, "docs", "flow")
	if code == 0 {
		t.Fatalf("yardr docs flow: exit 0\n%s", out)
	}
	for _, name := range []string{"flows", "groups", "line", "glossary"} {
		if !strings.Contains(errs.String(), name) {
			t.Errorf("yardr docs flow names no %q:\n%s", name, errs.String())
		}
	}
}

// yardr docs <name> <words> prints only the section whose heading
// matches: the runners without the thousand lines of groups.md.
func TestDocsShowPrintsOneSection(t *testing.T) {
	noHome(t)
	requireNonInteractive(t)
	out, code := executeOut(&app{}, "docs", "groups", "runners")
	if code != 0 {
		t.Fatalf("yardr docs groups runners: exit %d\n%s", code, out)
	}
	want, _ := docs.Section("groups", "runners")
	if out != want || !strings.HasPrefix(out, "## Runners\n") {
		t.Fatalf("yardr docs groups runners prints %.60q", out)
	}
	var errs bytes.Buffer
	out, code = executeOut(&app{stderr: &errs}, "docs", "groups", "no", "such", "heading")
	if code == 0 || !strings.Contains(errs.String(), "no section matching") {
		t.Fatalf("yardr docs groups no such heading: exit %d\n%s%s", code, out, errs.String())
	}
}

// A role leaves its reference in the docs and points at the page and the
// section that hold it (`yardr docs chat questions`). A pointer whose words
// no heading holds any more sends the yardmaster to an error at the moment
// it needs the page, so every pointer in a shipped role resolves: the page
// is one the binary carries and the words, where there are any, match a
// section's heading. For the pointers of the starter yardmaster role whose
// reference a cut moved out of the role (design yardr-2vy0 3.2, 3.5), the
// section also holds what stood only in the role, so a heading over text that
// never landed fails.
func TestShippedRolePointersResolve(t *testing.T) {
	t.Parallel()
	roles, err := fs.Glob(packs.FS, "*/roles/*.md")
	if err != nil || len(roles) == 0 {
		t.Fatalf("no shipped role found: %v", err)
	}
	// What each moved pointer's section holds, as words of the section read
	// as one line (a wrapped paragraph breaks nothing), and the words it must
	// not hold. The other packs' roles are cut by their own beads and carry
	// no such pointer yet, so only the starter's role is held to the list.
	type moved struct{ has, lacks []string }
	const starter = "starter/roles/yardmaster.md"
	want := map[string]moved{
		"setup as a conversation": {has: []string{
			"reads `yardr doctor` when it starts",
			"from the first step not done, never from the top",
			"every line marked `FAULT`",
			"`yardr mail <operator> - <<'MAIL'`",
			"It stays by mail for as long as they write by mail",
			"`yardr pack check <pack> --depot <depot>`",
			"`--bind builders=<kind>[:<model>] --bind reviewers=<kind>`",
			"`--members backlog= --members decisions=<them>`",
			"`--crew yardmaster=<the yardmaster's own name>`",
			"`depot test <depot>` with `--run-test`",
			"`yardr depot add <depot> <path> --base <base> --test '<command>'`",
			"a depot with none lands untested",
			"`yardr bead create - -b - --depot <depot> <<'BEAD'`",
			"The setup is over when that bead is merged",
		}},
		"flows designing a process": {has: []string{
			"One bead type with its own flow",
			"`yardr depot flow <depot> --type <type> <flow>`",
			"one bead per run",
			"An exec group per scripted step",
			"each script carrying only that step",
			"A manual group where a person says yes",
			"`decide` catches a step that fails",
			"sources (`yardr call`) reach outside services",
			"is the operator's to set",
			"with each such command whole",
			"An ask stays for a one-off decision, never for a gate that recurs",
		}},
		"flows ask template": {has: []string{
			"a stage a person moves carries an `## Ask`",
			"a card in footplate's Decide, with the stage's moves as buttons",
			"`yardr review` is the terminal's way to the same moves, not the only one",
			`never a "waiting on you" list in chat, in a pane or by mail`,
		}},
		"line a chain or a train": {has: []string{
			"is a chain unless it has to be a train",
			"`yardr dep add <blocker> <bead>` gives the order",
			"`yardr bead create - -t train -d <depot>`",
			"`yardr dep add <train> <child> -k parent`",
			"half of the work must not be on the base",
			"several children are built in parallel in one area and need one review of the whole",
			"until the whole train is reviewed and merged",
			"A directory depot has no branches",
			"read `yardr flow show <depot>`",
		}},
		"line wagons": {has: []string{
			"A depot has this lane when it has a flow for type `wagon`",
			"names each wagon in its body or in a note",
			"it is the only review they get",
		}},
		"groups routing by estimate and quota": {has: []string{
			"`difficulty:1` follows a pattern the code already has",
			"`difficulty:2` is new behaviour inside one mechanism",
			"`difficulty:3` is work whose shape is costly to get wrong",
			"`size:small`", "`size:medium`", "`size:large`",
			"`kind:<feat|fix|docs|refactor|test|perf|chore>`",
			"`feat` gives a user something new",
			"`fix` corrects what was wrong",
			"`docs` is a document alone",
			"`refactor` changes no behaviour",
			"`test` adds tests alone",
			"`perf` makes something faster or smaller",
			"`chore` is the rest",
			"A bead with none lands as `fix` when its type is bug and as `chore` otherwise",
			"takes one more label, `eyes`: a page, a window, the web view",
			"Its builder attaches a screenshot to its note and its reviewer opens it",
			"A refactor, a doc, a flag take none: nothing to see",
		}, lacks: []string{"yardmaster.md"}},
	}
	seen := map[string]bool{}
	// A span of the form `yardr docs <page> [words]`. The text is folded to
	// single spaces first, so a pointer the role wraps across lines is read.
	pointer := regexp.MustCompile("`yardr docs ([A-Za-z]+)((?: [^`\"]+)?)`")
	var found int
	for _, path := range roles {
		text, err := fs.ReadFile(packs.FS, path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range pointer.FindAllStringSubmatch(strings.Join(strings.Fields(string(text)), " "), -1) {
			found++
			page, words := m[1], strings.TrimSpace(m[2])
			switch _, ok := docs.Lookup(page); {
			case !ok:
				t.Errorf("%s points at %q, which is no page the binary carries (yardr docs lists them)", path, m[0])
			case words != "":
				section, ok := docs.Section(page, words)
				if !ok {
					t.Errorf("%s points at %q, and doc %s has no section matching %q", path, m[0], page, words)
					break
				}
				w, held := want[page+" "+words]
				if path != starter || !held {
					break
				}
				seen[page+" "+words] = true
				flat := strings.Join(strings.Fields(section), " ")
				for _, s := range w.has {
					if !strings.Contains(flat, s) {
						t.Errorf("%s points at %q, and the section lacks %q, which the role no longer carries", path, m[0], s)
					}
				}
				for _, s := range w.lacks {
					if strings.Contains(flat, s) {
						t.Errorf("%s points at %q, and the section holds %q", path, m[0], s)
					}
				}
			}
		}
	}
	for key := range want {
		if !seen[key] {
			t.Errorf("%s carries no pointer %q, whose section the test holds to the words the role dropped", starter, "yardr docs "+key)
		}
	}
	// Not for want of looking: the roles point at the docs.
	if found < 6 {
		t.Fatalf("%d pointers read in %d roles", found, len(roles))
	}
}

// The crew prime's How you work points at the docs the binary carries,
// which a crew member in another yard has no other way to read.
func TestPrimeCrewHowYouWorkNamesDocs(t *testing.T) {
	t.Parallel()
	st := userYard(t)
	out := mustAs(t, st, "yardmaster", "prime", "--crew", "yardmaster")
	const want = "yardr docs [name [section]]: how the yard works: flows, groups, the line, the glossary"
	if i, j := strings.Index(out, "## How you work"), strings.Index(out, want); i < 0 || j < i {
		t.Fatalf("prime --crew names no docs line after How you work:\n%s", out)
	}
}
