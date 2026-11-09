package docs

import (
	"io/fs"
	"strings"
	"testing"
)

// Every page the binary carries is listed, and every listed name is a
// page: a .md added beside docs.go without its row in README.md's
// index, or a rename that leaves one behind, fails here.
func TestOrderListsEveryPage(t *testing.T) {
	entries, err := fs.ReadDir(FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	var embedded []string
	for _, e := range entries {
		embedded = append(embedded, strings.TrimSuffix(e.Name(), ".md"))
	}
	if len(Order) != len(embedded) {
		t.Fatalf("Order lists %d pages, the binary carries %d: %q against %q", len(Order), len(embedded), Order, embedded)
	}
	seen := map[string]bool{}
	for _, n := range Order {
		seen[n] = true
	}
	for _, n := range embedded {
		if !seen[n] {
			t.Errorf("the binary carries %s.md, which Order does not list", n)
		}
	}
	for _, n := range Order {
		if text, ok := Read(n); !ok || text == "" {
			t.Errorf("Read(%q) = %d bytes, %v", n, len(text), ok)
		} else if title, ok := Title(n); !ok || title == "" {
			t.Errorf("Title(%q) = %q, %v: every page starts with a heading", n, title, ok)
		}
	}
}

// Lookup is what a name on the command line goes through: it finds a
// page however it is cased, and refuses an unknown name and a walk
// out of the pages.
func TestLookup(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"flows", "flows"},
		{"README", "README"},
		{"readme", "README"},
		{"Line", "line"},
	} {
		if got, ok := Lookup(tc.in); !ok || got != tc.want {
			t.Errorf("Lookup(%q) = %q, %v, want %q, true", tc.in, got, ok, tc.want)
		}
	}
	for _, in := range []string{"", "flow", "../go.mod", "flows.md", "design/notes"} {
		if got, ok := Lookup(in); ok || got != "" {
			t.Errorf("Lookup(%q) = %q, %v, want no page", in, got, ok)
		}
		if _, ok := Read(in); ok {
			t.Errorf("Read(%q) reads a page for an unknown name", in)
		}
	}
}

// Section prints one heading's part of a page: the match ignores case,
// the section runs to the next heading of the same or higher level,
// and deeper headings stay in. A line in a code fence is no heading, so
// a sample's `# comment` or a template's `## Ask` ends no section.
func TestSection(t *testing.T) {
	text, ok := Section("groups", "runners")
	if !ok {
		t.Fatal("Section(groups, runners) finds nothing")
	}
	if !strings.HasPrefix(text, "## Runners\n") {
		t.Fatalf("Section(groups, runners) starts %q", firstLine(text))
	}
	if strings.Contains(text, "\n## ") {
		t.Errorf("Section(groups, runners) runs past its next ## heading:\n%s", lastLines(text, 3))
	}
	if _, ok := Section("groups", "groups and runners"); ok {
		t.Errorf("Section(groups, groups and runners) matches the title, which would print the whole page")
	}
	if _, ok := Section("groups", "no such heading"); ok {
		t.Errorf("Section(groups, no such heading) finds a section")
	}
	if _, ok := Section("no such page", "runners"); ok {
		t.Errorf("Section(no such page, runners) finds a section")
	}
	if _, ok := Section("groups", "  "); ok {
		t.Errorf("Section(groups, blank) finds a section")
	}

	// The ask template's `## Ask` and an integration pack's
	// `# packs/linear/pack.yaml` stand in fences of their sections, which run
	// on past them: to the fence's close, and the text after it. (yardr-z53c:
	// each section ended at that line, with its fence open.)
	for _, c := range []struct{ page, words, fenced string }{
		{"flows", "ask template", "\n## Ask\nkind: choice\n"},
		{"packs", "integration pack", "\n# packs/linear/pack.yaml\nname: linear\n"},
	} {
		section, ok := Section(c.page, c.words)
		if !ok {
			t.Errorf("Section(%s, %s) finds nothing", c.page, c.words)
			continue
		}
		_, inFence, ok := strings.Cut(section, c.fenced)
		if !ok {
			t.Errorf("Section(%s, %s) stops before %q, a line in a fence:\n%s", c.page, c.words, strings.TrimSpace(c.fenced), lastLines(section, 3))
			continue
		}
		if _, after, closed := strings.Cut(inFence, "\n```\n"); !closed || strings.TrimSpace(after) == "" {
			t.Errorf("Section(%s, %s) ends in the fence that holds %q, or at its close:\n%s", c.page, c.words, strings.TrimSpace(c.fenced), lastLines(section, 3))
		}
	}

	// The rules, on texts no page carries: a fence's lines end no section
	// and start none, and a fence left open runs to the end of the page.
	for _, c := range []struct{ name, page, words, want string }{
		{"a fenced heading ends no section",
			"# T\n\n### Sample\n\n```\n## Ask\n# comment\n```\n\nafter\n\n### Next\n\nrest\n", "sample",
			"### Sample\n\n```\n## Ask\n# comment\n```\n\nafter\n"},
		{"a fenced heading starts no section",
			"# T\n\n## Intro\n\n```\n## Sample in a fence\n```\n\n## Sample\n\ntext\n", "sample",
			"## Sample\n\ntext\n"},
		{"an open fence runs to the end of the page",
			"# T\n\n## Sample\n\n```\n## Next\ntext\n", "sample",
			"## Sample\n\n```\n## Next\ntext\n"},
	} {
		if got, ok := sectionOf(c.page, c.words); !ok || got != c.want {
			t.Errorf("%s: sectionOf(%q, %q) = %q, %v, want %q", c.name, c.page, c.words, got, ok, c.want)
		}
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
