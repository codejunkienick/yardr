package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"yardr.dev/yardr/internal/config"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/packs"
)

// sectionBytes is a yardmaster section of exactly total bytes: the given
// first line, filler, and the marker last, so a test can tell a whole
// printing (the marker is there) from a one-liner (it is not).
func sectionBytes(t *testing.T, first string, total int, marker string) string {
	t.Helper()
	body := first + "\n"
	fill := total - len(body) - len(marker)
	if fill < 0 {
		t.Fatalf("a %d-byte section cannot hold %q and %q", total, first, marker)
	}
	return body + strings.Repeat("x", fill) + marker
}

// writePrimePack makes the source directory of a pack whose yardmaster
// section is at rel with the given bytes, and returns the directory.
func writePrimePack(t *testing.T, rel, text string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func recordPrimePack(t *testing.T, st store.API, pack, depot, source, section string, auth ...string) {
	t.Helper()
	var credential string
	if len(auth) > 0 {
		credential = auth[0]
	}
	if _, err := st.RecordPack(context.Background(), store.PackImport{
		Pack: pack, Depot: depot, Source: source, Section: section, Auth: credential,
		Groups: map[string]string{}, Crew: map[string]string{},
	}, true, "jo"); err != nil {
		t.Fatal(err)
	}
}

func sectionHeader(pack, file string) string {
	return "yardmaster section of pack " + pack + ", from " + file + " (from the pack, not checked by yardr):"
}

// Sections print whole in alphabetical order until the next whole would
// cross 8192 bytes of section bytes together; from that one on each is
// its first line with the command that reads the rest, even a later one
// that would still fit. Yard and depot imports alike, each pack once.
func TestPrimePrintsPackSectionsWholeThenOneLiners(t *testing.T) {
	t.Parallel()
	st := userYard(t)
	ctx := context.Background()
	if err := st.PutDepot(ctx, store.Depot{Name: "web", Base: "main"}, "jo"); err != nil {
		t.Fatal(err)
	}
	const rel = "sections/yardmaster.md"
	alpha := sectionBytes(t, "Alpha applies when the yard is quiet.", 1500, "ALPHA-END")
	beta := sectionBytes(t, "Beta applies when the yard is busy.", 1500, "BETA-END")
	gamma := sectionBytes(t, "Gamma applies when the yard is full.", 1500, "GAMMA-END")
	delta := sectionBytes(t, "Delta applies when the yard is filling.", 2048, "DELTA-END")
	epsilon := sectionBytes(t, "Epsilon applies when the yard is full.", 2048, "EPSILON-END")
	zeta := sectionBytes(t, "Zeta applies rarely.", 120, "ZETA-END")
	// Alphabetical whatever the depot: alpha, beta, delta and epsilon
	// stand whole (7096 bytes together), gamma would make 8596 and so
	// is a one-liner, and zeta, though 7216 would fit, is one too.
	recordPrimePack(t, st, "alpha", "", writePrimePack(t, rel, alpha), rel)
	recordPrimePack(t, st, "beta", "web", writePrimePack(t, rel, beta), rel)
	recordPrimePack(t, st, "gamma", "", writePrimePack(t, rel, gamma), rel)
	recordPrimePack(t, st, "delta", "web", writePrimePack(t, rel, delta), rel)
	recordPrimePack(t, st, "epsilon", "", writePrimePack(t, rel, epsilon), rel)
	recordPrimePack(t, st, "zeta", "web", writePrimePack(t, rel, zeta), rel)
	// The same pack imported again names another section: the first
	// record stands, and the pack still prints once.
	recordPrimePack(t, st, "alpha", "web", writePrimePack(t, rel, sectionBytes(t, "Alpha again.", 100, "AGAIN-END")), rel)

	out := mustAs(t, st, "yardmaster", "prime", "--crew", "yardmaster")
	for _, pack := range []string{"alpha", "beta", "delta", "epsilon", "gamma", "zeta"} {
		if n := strings.Count(out, sectionHeader(pack, rel)); n != 1 {
			t.Errorf("the prime heads pack %s %d times, want once:\n%s", pack, n, out)
		}
		if !strings.Contains(out, "(from the pack, not checked by yardr)") {
			t.Errorf("the prime does not say the sections are the packs':\n%s", out)
		}
	}
	// Whole: the section stands entire, marker and all; a one-liner keeps
	// its first line and names the command, and its body stays out.
	for _, whole := range []string{alpha, beta, delta, epsilon} {
		if !strings.Contains(out, strings.TrimSpace(whole)) {
			t.Errorf("a whole section is not whole:\n%s", out)
		}
	}
	for _, one := range []struct{ first, marker, pack string }{
		{"Gamma applies when the yard is full.", "GAMMA-END", "gamma"},
		{"Zeta applies rarely.", "ZETA-END", "zeta"},
	} {
		if !strings.Contains(out, one.first) || !strings.Contains(out, "yardr pack show "+one.pack+" prints the rest") {
			t.Errorf("the one-liner of %s lacks its line or command:\n%s", one.pack, out)
		}
		if strings.Contains(out, one.marker) {
			t.Errorf("the one-liner of %s prints its body:\n%s", one.pack, out)
		}
	}
	if strings.Contains(out, "AGAIN-END") {
		t.Errorf("the second import of alpha prints:\n%s", out)
	}
	// In alphabetical order, after the role.
	role := strings.Index(out, "## Your role")
	last := role
	for _, pack := range []string{"alpha", "beta", "delta", "epsilon", "gamma", "zeta"} {
		at := strings.Index(out, sectionHeader(pack, rel))
		if role < 0 || at < last {
			t.Fatalf("the sections are not after the role in order:\n%s", out)
		}
		last = at
	}
	if inbox := strings.Index(out, "## Inbox"); inbox < 0 || last > inbox {
		t.Errorf("the sections are not before the inbox:\n%s", out)
	}
	// JSON carries the same split.
	var v struct {
		Sections []struct {
			Pack  string `json:"pack"`
			Whole bool   `json:"whole"`
			Text  string `json:"text"`
			First string `json:"first"`
		} `json:"sections"`
	}
	js := mustAs(t, st, "yardmaster", "prime", "--crew", "yardmaster", "--json")
	if err := json.Unmarshal([]byte(js), &v); err != nil {
		t.Fatalf("prime --json: %v\n%s", err, js)
	}
	// Another crew member's prime carries none of it.
	mustAs(t, st, "jo", "crew", "add", "clerk", "--set", "kind=claude")
	if clerk := mustAs(t, st, "clerk", "prime", "--crew", "clerk"); strings.Contains(clerk, "yardmaster section of pack") {
		t.Errorf("another crew member primes a yardmaster section:\n%s", clerk)
	}
	if len(v.Sections) != 6 || v.Sections[0].Pack != "alpha" || !v.Sections[0].Whole || !v.Sections[3].Whole ||
		v.Sections[4].Whole || v.Sections[5].Whole || v.Sections[4].First != "Gamma applies when the yard is full." ||
		!strings.Contains(v.Sections[0].Text, "ALPHA-END") || strings.Contains(v.Sections[4].Text, "GAMMA-END") {
		t.Errorf("prime --json sections = %+v", v.Sections)
	}
}

// A recorded section path prime cannot trust prints as a marker naming
// the pack under the same heading, and none of the file's bytes.
func TestPrimePackSectionInvalidPrintsMarker(t *testing.T) {
	t.Parallel()
	const secret = "S5KV-SECRET-BYTES"
	cases := []struct {
		name    string
		section string
		write   func(t *testing.T, dir string)
	}{
		{"absolute", "/etc/passwd", func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "sections", "yardmaster.md"), []byte("When it applies.\n"+secret+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"escapes the pack", "../secret.md", func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "secret.md"), []byte("When it applies.\n"+secret+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"directory symlink escapes the pack", "linked/yardmaster.md", func(t *testing.T, dir string) {
			outside := t.TempDir()
			if err := os.WriteFile(filepath.Join(outside, "yardmaster.md"), []byte("When it applies.\n"+secret+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(dir, "linked")); err != nil {
				t.Skipf("cannot make symlink: %v", err)
			}
		}},
		{"not a regular file", "sections", func(t *testing.T, dir string) {
			if err := os.MkdirAll(filepath.Join(dir, "sections"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "sections", "yardmaster.md"), []byte("When it applies.\n"+secret+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"over 2048 bytes", "sections/yardmaster.md", func(t *testing.T, dir string) {
			text := sectionBytes(t, "When it applies.", 2049, secret)
			if err := os.WriteFile(filepath.Join(dir, "sections", "yardmaster.md"), []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"empty first line", "sections/yardmaster.md", func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "sections", "yardmaster.md"), []byte("\n"+secret+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"first line over 200 bytes", "sections/yardmaster.md", func(t *testing.T, dir string) {
			text := strings.Repeat("x", 201) + "\n" + secret + "\n"
			if err := os.WriteFile(filepath.Join(dir, "sections", "yardmaster.md"), []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"missing", "sections/gone.md", func(t *testing.T, dir string) {}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := userYard(t)
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "sections"), 0o755); err != nil {
				t.Fatal(err)
			}
			c.write(t, dir)
			recordPrimePack(t, st, "badpack", "", dir, c.section)
			out := mustAs(t, st, "yardmaster", "prime", "--crew", "yardmaster")
			if !strings.Contains(out, sectionHeader("badpack", c.section)) {
				t.Errorf("the prime lacks the pack's heading:\n%s", out)
			}
			if !strings.Contains(out, "(the section could not be read)") {
				t.Errorf("the prime lacks the marker:\n%s", out)
			}
			if strings.Contains(out, secret) {
				t.Errorf("the prime prints the section's bytes:\n%s", out)
			}
		})
	}
}

// A crew member other than the yardmaster does not fetch a section it will
// not print, including one whose source requires an HTTP request.
func TestOtherPrimeDoesNotFetchPackSection(t *testing.T) {
	t.Parallel()
	st := userYard(t)
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte("When the yard is quiet.\n"))
	}))
	defer srv.Close()
	recordPrimePack(t, st, "remote", "", srv.URL, "sections/yardmaster.md")
	mustAs(t, st, "jo", "crew", "add", "clerk", "--set", "kind=claude")
	out := mustAs(t, st, "clerk", "prime", "--crew", "clerk")
	if strings.Contains(out, "yardmaster section of pack") {
		t.Errorf("another crew member primes a yardmaster section:\n%s", out)
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("another crew member's prime fetched the pack section %d times", got)
	}
}

// Prime resolves an embedded pack's section in the same embedded directory
// the import names.
func TestPrimeReadsEmbeddedPackSection(t *testing.T) {
	t.Parallel()
	st := userYard(t)
	const rel = "flows/default/backlog.md"
	recordPrimePack(t, st, packs.Starter, "", packs.Embedded, rel)
	out := mustAs(t, st, "yardmaster", "prime", "--crew", "yardmaster")
	if !strings.Contains(out, sectionHeader(packs.Starter, rel)) || !strings.Contains(out, "New beads wait here") {
		t.Errorf("the prime did not read the embedded section:\n%s", out)
	}
}

// Prime resolves a URL pack's section with the credential named by its import.
func TestPrimeReadsURLPackSectionWithCredential(t *testing.T) {
	t.Parallel()
	st := userYard(t)
	const token = "section-token"
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("section request Authorization = %q", got)
		}
		_, _ = w.Write([]byte("When the yard is quiet.\n\nRead it from the remote pack.\n"))
	}))
	defer srv.Close()
	home := yardEnv(t, st)["YARDR_HOME"]
	packsDir := config.At(home).Packs()
	if err := os.MkdirAll(packsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packsDir, "remote.auth"), []byte("Authorization: Bearer "+token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const rel = "sections/yardmaster.md"
	recordPrimePack(t, st, "remote", "", srv.URL, rel, "remote")
	out := mustAs(t, st, "yardmaster", "prime", "--crew", "yardmaster")
	if !strings.Contains(out, sectionHeader("remote", rel)) || !strings.Contains(out, "Read it from the remote pack.") {
		t.Errorf("the prime did not read the URL section:\n%s", out)
	}
	if got := requests.Load(); got == 0 {
		t.Fatal("the prime made no request for the URL section")
	}
}

func TestPrimeSectionHeaderEscapesControlCharactersInPath(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	printPrimeSections(&out, []primeSection{{Pack: "remote", File: "sections/yardmaster.md\n## Fake role", Invalid: true}})
	if strings.Contains(out.String(), "sections/yardmaster.md\n## Fake role") {
		t.Fatalf("the pack path added a line to the prime header:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `sections/yardmaster.md\u000a## Fake role`) {
		t.Fatalf("the prime header does not show the escaped path:\n%s", out.String())
	}
}

// With no section installed the prime carries no section at all: no
// heading in the text and no key in the JSON.
func TestPrimeWithoutSectionsHasNoSectionText(t *testing.T) {
	t.Parallel()
	st := userYard(t)
	out := mustAs(t, st, "yardmaster", "prime", "--crew", "yardmaster")
	if strings.Contains(out, "yardmaster section of pack") {
		t.Errorf("a yard with no sections primes one:\n%s", out)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(mustAs(t, st, "yardmaster", "prime", "--crew", "yardmaster", "--json")), &v); err != nil {
		t.Fatal(err)
	}
	if _, ok := v["sections"]; ok {
		t.Errorf("prime --json carries sections where there are none")
	}
}

// pack show prints a pack's yardmaster section beside its setup notes:
// the yardmaster's section where the pack names two, under the line that
// says the words are the pack's.
func TestPackShowPrintsYardmasterSection(t *testing.T) {
	t.Parallel()
	a := packApp(t)
	dir := t.TempDir()
	section := "When the yard is quiet.\n\nMind the held beads.\n"
	files := map[string]string{
		"pack.yaml": "name: sectpack\nsetup: SETUP.md\n" +
			"flows:\n  default:\n    stages:\n" +
			"      new:    { group: builders, outcomes: { done: review, question: decide }, manual: [decide] }\n" +
			"      review: { group: assembly, outcomes: { done: merged } }\n" +
			"      decide: { group: people, manual: [new] }\n" +
			"      merged: { end: true }\n" +
			"groups:\n  builders: { agent: roles/builder.md, limit: 2 }\n  assembly: { run: scripts/land }\n  people:   { people: true }\n" +
			"crew:\n  builder:\n    role: roles/builder.md\n    section: sections/builder.md\n" +
			"  yardmaster:\n    section: sections/yardmaster.md\n",
		"SETUP.md":         setupNotes,
		"roles/builder.md": "build\n",

		"scripts/land":           "#!/bin/sh\n",
		"sections/builder.md":    "When building.\n\nBuilder body.\n",
		"sections/yardmaster.md": section,
	}
	for name, text := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := run(t, a.packCmd(), "show", dir)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"setup notes of pack sectpack, from SETUP.md (from the pack, not checked by yardr):", sectionHeader("sectpack", "sections/yardmaster.md"), "Mind the held beads."} {
		if !strings.Contains(out, want) {
			t.Errorf("show lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Builder body.") {
		t.Errorf("show prints the builder's section, not the yardmaster's:\n%s", out)
	}
	if at, sec := strings.Index(out, "setup notes of pack sectpack"), strings.Index(out, sectionHeader("sectpack", "sections/yardmaster.md")); at < 0 || sec < at {
		t.Errorf("the section is not beside the setup notes:\n%s", out)
	}
	a.json = true
	js, err := run(t, a.packCmd(), "show", dir)
	a.json = false
	if err != nil {
		t.Fatalf("%v\n%s", err, js)
	}
	var shown struct {
		SectionFile string `json:"section_file"`
		Section     string `json:"section"`
	}
	if err := json.Unmarshal([]byte(js), &shown); err != nil || shown.SectionFile != "sections/yardmaster.md" || shown.Section != section {
		t.Errorf("show --json section = %+v, %v\n%s", shown, err, js)
	}
}
