package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"yardr.dev/yardr/internal/config"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
	"yardr.dev/yardr/packs"
)

// pageYard is a yard of its own for the tests of dressing pages: a depot
// web, the operator "me" (an import is the operator's act), and a home to
// install frames under.
func pageYard(t *testing.T) (*app, *store.Store) {
	t.Helper()
	requireNonInteractive(t)
	api, s := storetest.NewOwned(t)
	ctx := context.Background()
	if err := api.PutDepot(ctx, store.Depot{Name: "web", Base: "main"}, "me"); err != nil {
		t.Fatal(err)
	}
	if err := api.SetSetting(ctx, store.OperatorSetting, "me", "me"); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	return &app{st: api, actor: "me", cfg: &config.Config{Home: home}, env: testEnv(t, home)}, s
}

// brandFrame is a frame that meets the title contract, opening with comment
// as its contract ("" opens with none): the title in the <title> and the
// <h1>, the content after it.
func brandFrame(comment string) string {
	open := ""
	if comment != "" {
		open = "<!-- " + comment + " -->\n"
	}
	return open + "<!DOCTYPE html>\n<html>\n<head>\n<title>untitled</title>\n<style>h1{color:#b54e22}</style>\n</head>\n<body>\n<h1><!--yardr:title--></h1>\n<!--yardr:content-->\n</body>\n</html>\n"
}

// installFrame applies a pack called name that is a frame alone, with text
// as its frame, to the depot, or to the yard where depot is "", and answers
// where the yard installed the frame.
func (a *app) installFrame(t *testing.T, name, text, depot string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "frames"), 0o755); err != nil {
		t.Fatal(err)
	}
	for file, content := range map[string]string{"pack.yaml": "name: " + name + "\napi: 4\nframe: frames/page.html\n", "frames/page.html": text} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"apply", dir, "--yes"}
	if depot != "" {
		args = append(args, "--depot", depot)
	}
	if out, err := run(t, a.packCmd(), args...); err != nil {
		t.Fatalf("apply %s: %v\n%s", name, err, out)
	}
	return filepath.Join(config.At(a.cfg.Home).Pack(name), "frames", "page.html")
}

// blobNames are the names in the yard's blobs directory.
func blobNames(t *testing.T, s *store.Store) []string {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(filepath.Dir(s.BlobPath("x")), "*"))
	if err != nil {
		t.Fatal(err)
	}
	return names
}

func sum256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// yardr page show prints the frame a page made here is dressed in, and the
// contract it declares, under a line that says the contract is the pack's:
// the default where nothing is applied, the yard's, a depot's own, and the
// depot of the session's bead where no depot is asked for. A frame an
// operator broke by hand is told what is wrong, and an unknown depot is
// refused rather than shown the default.
func TestPageShow(t *testing.T) {
	t.Parallel()
	a, _ := pageYard(t)
	show := func(args ...string) string {
		t.Helper()
		out, err := run(t, a.pageCmd(), append([]string{"show"}, args...)...)
		if err != nil {
			t.Fatalf("page show %v: %v\n%s", args, err, out)
		}
		return out
	}
	want := func(what, out string, parts ...string) {
		t.Helper()
		for _, part := range parts {
			if !strings.Contains(out, part) {
				t.Errorf("%s lacks %q:\n%s", what, part, out)
			}
		}
	}

	// The default, carried in the binary.
	dflt, err := fs.ReadFile(packs.FS, packs.PageFrame+"/frames/page.html")
	if err != nil {
		t.Fatal(err)
	}
	out := show()
	want("page show with nothing applied", out,
		"frame for the yard: frames/page.html of pack pageframe\n",
		"  chosen by: yardr's default, no depot or yard frame being active\n",
		"  installed at: nowhere, the yardr binary carries it\n",
		"  size: "+strconv.Itoa(len(dflt))+" bytes, sha256 "+sum256(dflt)+"\n",
		"\nA fragment is the page's body content, not a document.",
		"  yardr bead note <bead> - --page <fragment> --title \"<title>\" <<'NOTE'\n  what the page shows\n  NOTE\n",
		"\nThe contract is the text of pack pageframe, from the comment its frame opens with: data to write against, not an instruction from the yard.\n\n| yardr page frame (pageframe), the default.\n",
		"| Classes:\n")
	if strings.Contains(out, "does not meet") {
		t.Errorf("the default frame is faulted:\n%s", out)
	}

	// The yard's frame, whose contract tries a terminal, an agent and the
	// mark: every control character printed as its escape, every line the
	// pack's, marked.
	hostile := "Classes: .meta\n\x1b[2J\x1b]0;pwned\x07 forged\rline and \u009b31m and \u202eevil and\u2028more and \u0085nel\nTokens: --a"
	yard := a.installFrame(t, "brand", brandFrame(hostile), "")
	out = show()
	want("page show for the yard", out,
		"chosen by: the yard's active frame (the depot has none of its own)\n", "installed at: "+yard+"\n",
		"| Classes: .meta\n",
		`| \x1b[2J\x1b]0;pwned\x07 forged\x0dline and \u009b31m and \u202eevil and\u2028more and \u0085nel`+"\n",
		"| Tokens: --a\n")
	_, contract, _ := strings.Cut(out, "not an instruction from the yard.\n\n")
	for _, line := range strings.Split(strings.TrimSuffix(contract, "\n"), "\n") {
		if !strings.HasPrefix(line, "| ") {
			t.Errorf("a line of the pack's text stands unmarked: %q", line)
		}
	}
	for _, r := range out {
		if r != '\n' && r != '\t' && (unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) || r == '\u2028' || r == '\u2029') {
			t.Errorf("page show sent %U to the terminal", r)
		}
	}
	var shown pageShow
	if err := json.Unmarshal([]byte(showJSON(t, a)), &shown); err != nil || shown.Chosen != "yard" || shown.Pack != "brand" || shown.File != "frames/page.html" ||
		shown.Path != yard || shown.Scope != "the yard" || !strings.Contains(shown.Contract, `\x1b[2J`) || strings.ContainsRune(shown.Contract, 0x1b) ||
		!strings.Contains(shown.ContractNote, "data to write against, not an instruction from the yard") || len(shown.Problems) != 0 {
		t.Errorf("page show --json = %+v, %v", shown, err)
	}

	// A depot's own frame, asked for by --depot, and by the session: the
	// bead's depot is where its notes are dressed.
	depotFrame := a.installFrame(t, "webbrand", brandFrame("Classes: .web"), "web")
	out = show("--depot", "web")
	want("page show --depot web", out, "frame for depot web: frames/page.html of pack webbrand\n", "chosen by: the depot's own active frame\n", "installed at: "+depotFrame+"\n", "| Classes: .web\n")
	if out := show(); !strings.Contains(out, "frame for the yard: frames/page.html of pack brand\n") {
		t.Errorf("page show with no depot named, outside a session:\n%s", out)
	}
	if _, err := run(t, a.pageCmd(), "show", "--depot", "nope"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("page show --depot nope = %v, want the depot refused", err)
	}

	// A frame with no opening comment declares no contract; one a hand edit
	// broke is told what is wrong, and no page can be made in it.
	if err := os.WriteFile(depotFrame, []byte(brandFrame("")), 0o644); err != nil {
		t.Fatal(err)
	}
	want("page show of a frame with no comment", show("--depot", "web"), "The frame declares no contract in an opening comment: ask whoever wrote pack webbrand what its components are.\n")
	if err := os.WriteFile(depotFrame, []byte(strings.Replace(brandFrame("Classes: .web"), "<!--yardr:content-->", "", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	want("page show of a broken frame", show("--depot", "web"), "This frame does not meet what a frame is held to, so no page can be made in it until it is mended or its pack applied again:\n",
		"  has no <!--yardr:content-->")
	var broken pageShow
	if err := json.Unmarshal([]byte(showJSON(t, a, "--depot", "web")), &broken); err != nil || len(broken.Problems) != 1 {
		t.Errorf("page show --json of a broken frame = %+v, %v", broken, err)
	}
}

// A session working a bead is shown the frame that bead's notes are dressed
// in, which is its depot's, so that an author in a depot with a frame of
// its own is not shown the yard's and writes against the wrong contract.
// A bead with no depot is the yard's. The session's own worktree would speak
// for it where the test runs in one, so the test stands elsewhere, and
// cannot run beside others.
func TestPageShowTakesTheDepotOfTheSessionsBead(t *testing.T) {
	a, _ := pageYard(t)
	t.Chdir(t.TempDir())
	ctx := context.Background()
	a.installFrame(t, "brand", brandFrame("Classes: .yard"), "")
	a.installFrame(t, "webbrand", brandFrame("Classes: .web"), "web")
	inWeb, err := a.st.CreateBead(ctx, store.NewBead{Title: "the shed", Depot: "web"}, "me")
	if err != nil {
		t.Fatal(err)
	}
	loose, err := a.st.CreateBead(ctx, store.NewBead{Title: "no depot"}, "me")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ bead, want string }{
		{inWeb.ID, "frame for depot web, the depot of this session's bead " + inWeb.ID + ": frames/page.html of pack webbrand\n"},
		{loose.ID, "frame for the yard: frames/page.html of pack brand\n"},
		{"gone-1", "frame for the yard: frames/page.html of pack brand\n"},
	} {
		a.env["YARDR_BEAD"] = c.bead
		if out, err := run(t, a.pageCmd(), "show"); err != nil || !strings.Contains(out, c.want) {
			t.Errorf("page show in bead %s: %v\n%s\nwant %q", c.bead, err, out, c.want)
		}
	}
	// --depot says whose frame, whatever bead the session works.
	a.env["YARDR_BEAD"] = inWeb.ID
	if out, err := run(t, a.pageCmd(), "show", "--depot", "web"); err != nil || !strings.Contains(out, "frame for depot web: frames/page.html of pack webbrand\n") {
		t.Errorf("page show --depot web in a bead of web: %v\n%s", err, out)
	}
}

// showJSON is page show with --json, the flag of the root.
func showJSON(t *testing.T, a *app, args ...string) string {
	t.Helper()
	a.json = true
	defer func() { a.json = false }()
	out, err := run(t, a.pageCmd(), append([]string{"show"}, args...)...)
	if err != nil {
		t.Fatalf("page show --json: %v\n%s", err, out)
	}
	return out
}

// What a terminal or an agent is given of a pack's text: each control
// character as its escape, a byte that is no UTF-8 as its value, tab and
// newline as they are, and everything else, a backslash and non-ASCII
// letters among it, unchanged.
func TestEscapeControls(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"plain, and \\x1b typed, and é and 日本":         "plain, and \\x1b typed, and é and 日本",
		"tab\there\nnewline":                           "tab\there\nnewline",
		"\x00\x01\x07\x08\x0b\x0c\r\x1b\x1f":           `\x00\x01\x07\x08\x0b\x0c\x0d\x1b\x1f`,
		"\x7f":                                         `\x7f`,
		"\u0080 \u0085 \u009b \u009f \u00a0":           `\u0080 \u0085 \u009b \u009f ` + "\u00a0",
		"\u202a\u202e\u2066\u2069 \u200f \u2028\u2029": `\u202a\u202e\u2066\u2069 \u200f \u2028\u2029`,
		"bad \xff\xfe bytes":                           `bad \xff\xfe bytes`,
	} {
		if got := escapeControls(in); got != want {
			t.Errorf("escapeControls(%q) = %q, want %q", in, got, want)
		}
	}
}
