package packs_test

import (
	"html"
	"io/fs"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/pack"
	"yardr.dev/yardr/packs"
)

// maxPageFrame is what the default frame may weigh. It is copied into every
// page made in it, so what it weighs, it weighs once for each page.
const maxPageFrame = 16 << 10

// pageFrame is the default frame as the binary carries it.
func pageFrame(t *testing.T) string {
	t.Helper()
	b, err := fs.ReadFile(packs.FS, packs.PageFrame+"/frames/page.html")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// frameElement is what stands in the frame's one element of that tag, which
// has no attributes.
func frameElement(t *testing.T, frame, tag string) string {
	t.Helper()
	m := regexp.MustCompile(`(?s)<` + tag + `>(.*?)</` + tag + `>`).FindStringSubmatch(frame)
	if m == nil {
		t.Fatalf("the frame has no <%s> element", tag)
	}
	return m[1]
}

// frameComment is the comment that opens the frame: the contract its authors
// write against, which a page's author is shown.
func frameComment(t *testing.T, frame string) string {
	t.Helper()
	if !strings.HasPrefix(frame, "<!-- ") {
		t.Fatalf("the frame does not open with a comment of its own: %.40q", frame)
	}
	end := strings.Index(frame, "-->")
	if end < 0 {
		t.Fatal("the frame's opening comment does not end")
	}
	return frame[len("<!--"):end]
}

type cssRule struct{ selector, body string }

// cssRules are the rules of a stylesheet that hold declarations, those inside
// an @media block as rules of their own, with the selector as written.
func cssRules(css string) []cssRule {
	var rules []cssRule
	for _, m := range regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`).FindAllStringSubmatch(css, -1) {
		rules = append(rules, cssRule{strings.TrimSpace(m[1]), m[2]})
	}
	return rules
}

// declarations are the name and value pairs of a rule's body, without the
// spaces after a colon.
func declarations(body string) map[string]string {
	out := map[string]string{}
	for _, d := range strings.Split(body, ";") {
		name, value, ok := strings.Cut(d, ":")
		if ok {
			out[strings.TrimSpace(name)] = strings.TrimSpace(value)
		}
	}
	return out
}

// dress puts a title and a fragment in a frame by the three substitutions the
// frame's contract names, which is as much of a page's making as these tests
// need: the title in the title element and where its marker stands, and the
// fragment where its marker stands.
func dress(frame, fragment, title string) string {
	esc := html.EscapeString(title)
	i, j := strings.Index(frame, "<title>"), strings.Index(frame, "</title>")
	s := frame[:i+len("<title>")] + esc + frame[j:]
	s = strings.Replace(s, "<!--yardr:title-->", esc, 1)
	return strings.Replace(s, "<!--yardr:content-->", fragment, 1)
}

// The default frame is a pack of a frame alone, read as any pack is: the
// title contract the pack check holds every frame to is met, and the frame
// weighs what its budget allows.
func TestPageFrameIsAFrameAlone(t *testing.T) {
	sub, err := fs.Sub(packs.FS, packs.PageFrame)
	if err != nil {
		t.Fatal(err)
	}
	p, err := pack.Load(sub, packs.Embedded)
	if err != nil {
		t.Fatal(err)
	}
	frame := pageFrame(t)
	if p.Name != packs.PageFrame || p.API != 4 || p.Kind != "" {
		t.Errorf("pack %q, API %d, kind %q: want pageframe, API 4, of no kind", p.Name, p.API, p.Kind)
	}
	if p.Frame == nil || p.Frame.File != "frames/page.html" || p.Frame.Bytes != len(frame) {
		t.Errorf("frame %+v: want frames/page.html, %d bytes", p.Frame, len(frame))
	}
	if !p.YardWide() || len(p.Sources)+len(p.Declared)+len(p.Presets) != 0 || p.Setup != "" {
		t.Errorf("the pack carries more than a frame: %d flows, %d groups, %d crew, %d sources, %d declared, setup %q",
			len(p.Flows), len(p.Groups), len(p.Crew), len(p.Sources), len(p.Declared), p.Setup)
	}
	if len(frame) > maxPageFrame {
		t.Errorf("the frame is %d bytes, over its budget of %d: every page made in it carries them", len(frame), maxPageFrame)
	}
}

// The frame's colours and type are the yardr design system's, copied as
// values, and its light theme sits under the reader's own setting; a colour
// that is not the system's, or a missing light value, fails here. The pairs
// are the system's tokens/colors.css and tokens/typography.css, resolved.
func TestPageFrameTokensAreTheDesignSystems(t *testing.T) {
	css := frameElement(t, pageFrame(t), "style")
	var roots []map[string]string
	for _, r := range cssRules(css) {
		if r.selector == ":root" {
			roots = append(roots, declarations(r.body))
		}
	}
	if len(roots) != 2 {
		t.Fatalf("%d :root rules, want the dark tokens and the light theme's", len(roots))
	}
	dark, light := roots[0], roots[1]
	for name, want := range map[string]string{
		"--surface-app": "#161718", "--surface-raised": "#1C1D1F", "--surface-sunken": "#101112", "--surface-hover": "#232426",
		"--border-subtle": "#27282B", "--border-strong": "#333538",
		"--text-primary": "#E9E9EA", "--text-secondary": "#ABADB0", "--text-accent": "#E2794A", "--accent": "#D3602F", "--focus-ring": "#E2794A",
		"--font-sans":    `"IBM Plex Sans","Noto Sans","Cantarell","Segoe UI",system-ui,sans-serif`,
		"--font-mono":    `"IBM Plex Mono","JetBrains Mono","DejaVu Sans Mono",ui-monospace,monospace`,
		"--type-title":   "600 20px/28px var(--font-sans)",
		"--type-section": "600 16px/24px var(--font-sans)",
		"--type-body":    "400 16px/24px var(--font-sans)",
		"--type-label":   "500 14px/20px var(--font-sans)",
		"--type-meta":    "400 13px/18px var(--font-sans)",
		"--type-code":    "400 14px/20px var(--font-mono)",
		"color-scheme":   "dark",
	} {
		if dark[name] != want {
			t.Errorf("dark %s = %q, want %q", name, dark[name], want)
		}
	}
	for name, want := range map[string]string{
		"--surface-app": "#F7F7F6", "--surface-raised": "#FFFFFF", "--surface-sunken": "#EDEDEB", "--surface-hover": "#EDEDEB",
		"--border-subtle": "#E2E2DF", "--border-strong": "#CFCFCB",
		"--text-primary": "#161718", "--text-secondary": "#5B5E64", "--text-accent": "#B54E22", "--focus-ring": "#B54E22",
		"color-scheme": "light",
	} {
		if light[name] != want {
			t.Errorf("light %s = %q, want %q", name, light[name], want)
		}
	}
	if !strings.Contains(css, "@media (prefers-color-scheme:light){:root{") {
		t.Error("the light theme does not follow the reader's own setting")
	}
	// The column is the worked page's 1120 px, fluid below it.
	if !regexp.MustCompile(`\.page\{[^}]*max-width:1120px`).MatchString(css) {
		t.Error("the page's column is not 1120px wide")
	}
}

// What a page is made in carries no way out: the page policies refuse every
// fetch but an inline one, so a frame that names a file, a font or a host
// would show the break at once, and a script that reaches for the network, a
// window or storage is not what a reader chose to run. The words the script
// may not hold are the ones a security review asks of it.
func TestPageFrameFetchesAndReachesNothing(t *testing.T) {
	frame := pageFrame(t)
	script := frameElement(t, frame, "script")
	if m := regexp.MustCompile(`(?i)\b(?:https?|wss?|ftp):|\burl\(|@import|\b(?:src|href|action|srcset|poster)\s*=|<(?:link|img|iframe|frame|object|embed|form|base|video|audio|source)\b|<meta[^>]+http-equiv`).FindString(frame); m != "" {
		t.Errorf("the frame names something to fetch or follow: %q", m)
	}
	if m := regexp.MustCompile(`\b(?:fetch|XMLHttpRequest|WebSocket|EventSource|sendBeacon|Worker|SharedWorker|import|RTCPeerConnection|open|location|history|eval|Function|innerHTML|outerHTML|insertAdjacentHTML|write|postMessage|localStorage|sessionStorage|cookie|indexedDB|navigator|document\.domain)\b`).FindString(script); m != "" {
		t.Errorf("the frame's script reaches for %q", m)
	}
}

// With scripts off, every static open, a page reads whole: the frame hides
// nothing of its own. Only once its script has marked the document scripted
// may a rule hide, and the script hides only after that mark.
func TestPageFrameReadsWholeWithScriptsOff(t *testing.T) {
	frame := pageFrame(t)
	hides := regexp.MustCompile(`display:none|visibility:hidden|content-visibility:hidden|opacity:0(?:;|$)|clip-path|height:0(?:;|$)`)
	var scoped int
	for _, r := range cssRules(frameElement(t, frame, "style")) {
		if !hides.MatchString(strings.ReplaceAll(r.body, " ", "")) {
			continue
		}
		for _, sel := range strings.Split(r.selector, ",") {
			if !strings.Contains(sel, "[data-scripted]") {
				t.Errorf("%s hides something with no script to have marked the document: it would hide for a reader whose page does not run", strings.TrimSpace(sel))
			}
		}
		scoped++
	}
	if scoped == 0 {
		t.Error("no rule hides a panel the script has hidden: a panel set hidden would show, or not be a panel at all")
	}
	script := frameElement(t, frame, "script")
	mark, hidden := strings.Index(script, "'data-scripted'"), strings.Index(script, ".hidden=")
	if mark < 0 || hidden < mark {
		t.Errorf("the script marks the document scripted at %d and first hides at %d: the mark comes first", mark, hidden)
	}
}

// The comment that opens the frame is what a page's author reads, so it names
// what the frame has, no more and no less: the tokens it declares, and the
// classes it styles or its script reads. It names the markers in words, since
// a marker written as a comment would end it.
func TestPageFrameCommentIsItsContract(t *testing.T) {
	frame := pageFrame(t)
	comment := frameComment(t, frame)
	css, script := frameElement(t, frame, "style"), frameElement(t, frame, "script")
	if strings.Contains(comment, "yardr:title-") || strings.Contains(comment, "yardr:content-") {
		t.Error("the comment writes a marker as a comment, which ends it there")
	}
	for _, want := range []string{"yardr:title", "yardr:content", "data-hl"} {
		if !strings.Contains(comment, want) {
			t.Errorf("the comment does not name %s", want)
		}
	}
	var named []string
	for _, line := range strings.Split(comment, "\n") {
		if rest, ok := strings.CutPrefix(line, "Tokens:"); ok {
			named = strings.Fields(rest)
		}
	}
	var declared []string
	for _, r := range cssRules(css) {
		if r.selector == ":root" {
			for name := range declarations(r.body) {
				if strings.HasPrefix(name, "--") && !slices.Contains(declared, name) {
					declared = append(declared, name)
				}
			}
			break
		}
	}
	slices.Sort(named)
	slices.Sort(declared)
	if !slices.Equal(named, declared) {
		t.Errorf("the comment names tokens %v, the frame declares %v", named, declared)
	}
	classes := regexp.MustCompile(`(?:^|\s)\.([a-z]+)\b`).FindAllStringSubmatch(comment, -1)
	if len(classes) < 15 {
		t.Fatalf("the comment names %d classes: too few to be the contract", len(classes))
	}
	for _, c := range classes {
		if !regexp.MustCompile(`\.` + c[1] + `\b`).MatchString(css + script) {
			t.Errorf("the comment names .%s, which the frame neither styles nor reads", c[1])
		}
	}
}

// A hand-written fragment, written against that contract, makes a page: the
// title stands in the title element and the h1 once each, escaped, the
// fragment stands once, whole, and the page reads as a page to the yard,
// which tells a blob's type from its bytes. It also holds the fragment to
// what the frame asks of one: tabs that open with their own headings, the
// figures of a chart in a table beside it, and nothing hidden.
func TestPageFrameDressesTheTestFragment(t *testing.T) {
	frame := pageFrame(t)
	raw, err := os.ReadFile("testdata/pageframe-fragment.html")
	if err != nil {
		t.Fatal(err)
	}
	fragment := string(raw)
	const title = `Beads & depots, 14 days <draft>`
	page := dress(frame, fragment, title)
	const escaped = `Beads &amp; depots, 14 days &lt;draft&gt;`
	if n := strings.Count(page, "<title>"+escaped+"</title>"); n != 1 {
		t.Errorf("the title stands in %d title elements, want 1", n)
	}
	if n := strings.Count(page, "<h1>"+escaped+"</h1>"); n != 1 {
		t.Errorf("the title stands in %d h1 elements, want 1", n)
	}
	if strings.Contains(page, "<!--yardr:") || strings.Count(page, fragment) != 1 {
		t.Error("a marker is left in the page, or the fragment is not there once, whole")
	}
	if got := http.DetectContentType([]byte(page)); got != "text/html; charset=utf-8" {
		t.Errorf("the page reads as %s to the yard, not as a page", got)
	}
	if regexp.MustCompile(`\shidden[\s>=]`).MatchString(fragment) {
		t.Error("the fragment hides something of its own: a reader whose page does not run would never see it")
	}
	if n := strings.Count(fragment, `<section class="tab">`); n != len(regexp.MustCompile(`<section class="tab">\s*<h[234]>`).FindAllString(fragment, -1)) || n < 2 {
		t.Errorf("%d tabs, and not every one opens with its own heading", n)
	}
	// Every mark of a chart is also a row of a table, so the chart is not the
	// only place its figures are.
	rows := regexp.MustCompile(`<tr data-hl="([^"]+)"`).FindAllStringSubmatch(fragment, -1)
	marks := regexp.MustCompile(`<(?:rect|circle) class="bar" data-hl="([^"]+)"`).FindAllStringSubmatch(fragment, -1)
	if len(rows) == 0 || len(marks) == 0 {
		t.Fatal("the fragment has no charted figures, or no rows holding them")
	}
	for _, m := range marks {
		if !strings.Contains(fragment, `<tr data-hl="`+m[1]+`"`) {
			t.Errorf("a mark of key %s has no row of figures", m[1])
		}
	}
	if figs, caps, boxes := strings.Count(fragment, `<figure class="chart">`), strings.Count(fragment, "<figcaption>"), strings.Count(fragment, `<svg viewBox="`); figs != caps || figs != boxes || figs == 0 {
		t.Errorf("%d charts, %d captions, %d svgs with a viewBox: each chart has one of each", figs, caps, boxes)
	}
	// What the fragment writes against is what the comment names.
	comment := frameComment(t, frame)
	for _, m := range regexp.MustCompile(`class="([^"]+)"`).FindAllStringSubmatch(fragment, -1) {
		for _, class := range strings.Fields(m[1]) {
			if !regexp.MustCompile(`\.` + class + `\b`).MatchString(comment) {
				t.Errorf("the fragment uses class %s, which the frame's comment does not name", class)
			}
		}
	}
}
