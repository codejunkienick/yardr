package packs_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"yardr.dev/yardr/internal/scripttest"
	"yardr.dev/yardr/internal/source"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/packs"
)

// The image call builds its batchUpdate per call. With a find its build
// script reads the document, finds the one place the placeholder stands in
// the body of a tab, and prints a deleteContentRange of it and an
// insertInlineImage at its start, pinned to the revision read; with none it
// prints one insertInlineImage at the end of the body and reads nothing.
// Indices are UTF-16 code units: an astral character counts double, and a
// placeholder split across text runs is found where it starts. Every index
// below is counted by hand from the document the stand-in serves, not worked
// out the way the script does. No test calls Google.
func TestGoogleDocsImageBuilds(t *testing.T) {
	const uri = "https://example.com/logo.png"
	size := func(width string) string {
		if width == "" {
			return ""
		}
		return `,"objectSize":{"width":{"magnitude":` + width + `,"unit":"PT"}}`
	}
	for _, tc := range []struct {
		name       string
		doc        string // the stand-in's doc
		find       string
		width      string // "" leaves width out
		tab        string // the tab the placeholder stands in
		start, end int    // the placeholder's range
	}{
		{name: "a placeholder in a paragraph", doc: "one", find: "{{logo}}", tab: "t.0", start: 7, end: 15},
		{name: "with a width", doc: "one", find: "{{logo}}", width: "120", tab: "t.0", start: 7, end: 15},
		{name: "a placeholder split by a style change", doc: "split", find: "{{logo}}", tab: "t.0", start: 7, end: 15},
		{name: "an emoji before it counts double", doc: "emoji", find: "{{logo}}", tab: "t.0", start: 10, end: 18},
		{name: "an emoji placeholder spans two units", doc: "emoji", find: "🍜", tab: "t.0", start: 7, end: 9},
		{name: "a placeholder in a table cell", doc: "cell", find: "{{logo}}", tab: "t.0", start: 10, end: 18},
		{name: "after an inline picture the index is the run's own", doc: "gap", find: "go}}", tab: "t.0", start: 6, end: 10},
		{name: "a placeholder in a child tab", doc: "second", find: "{{logo}}", tab: "t.1.1", start: 1, end: 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			standin, srv := imageStandin(t)
			defer srv.Close()
			docs := googleDeclaration(t, "google-docs.yaml", "https://docs.googleapis.com", srv.URL)
			params := map[string]string{"docId": standin.doc(tc.doc), "uri": uri, "find": tc.find}
			if tc.width != "" {
				params["width"] = tc.width
			}
			body, err := source.BuildBody(context.Background(), docs, "image", params, "tok", imageHome(t), store.ReadSecretFile)
			if err != nil {
				t.Fatal(err)
			}
			revision := map[string]string{
				"one": "rev-one", "split": "rev-split", "emoji": "rev-emoji", "cell": "rev-cell",
				"gap": "rev-gap", "second": "rev-second",
			}[tc.doc]
			rng := `"startIndex":` + strconv.Itoa(tc.start) + `,"endIndex":` + strconv.Itoa(tc.end) + `,"tabId":"` + tc.tab + `"`
			golden := `{"requests":[{"deleteContentRange":{"range":{` + rng + `}}},` +
				`{"insertInlineImage":{"location":{"index":` + strconv.Itoa(tc.start) + `,"tabId":"` + tc.tab + `"},"uri":"` + uri + `"` + size(tc.width) + `}}],` +
				`"writeControl":{"requiredRevisionId":"` + revision + `"}}`
			var got, want any
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("built body = %s: %v", body, err)
			}
			if err := json.Unmarshal([]byte(golden), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("built body = %s, want %s", body, golden)
			}
			// The pre-read the index came from: the doc, read with the
			// call's credential, as the replace call reads it.
			gotReqs := standin.requests()
			if len(gotReqs) != 1 {
				t.Fatalf("the stand-in heard %d requests, want the one pre-read", len(gotReqs))
			}
			pre := gotReqs[0]
			if pre.method != "GET" || pre.path != "/v1/documents/"+standin.doc(tc.doc) {
				t.Fatalf("pre-read = %s %s", pre.method, pre.path)
			}
			if pre.auth != "Bearer tok" {
				t.Fatalf("pre-read Authorization = %q, want the call's credential", pre.auth)
			}
			if pre.query.Get("fields") != "revisionId,tabs" || pre.query.Get("includeTabsContent") != "true" {
				t.Fatalf("pre-read query = %v", pre.query)
			}
		})
	}
	// With no find the picture goes at the end of the body: one request,
	// no index to work out, no revision to pin, and the stand-in is not
	// asked for the document at all.
	for _, width := range []string{"", "96"} {
		t.Run("the end of the body, width "+strconv.Quote(width), func(t *testing.T) {
			standin, srv := imageStandin(t)
			defer srv.Close()
			docs := googleDeclaration(t, "google-docs.yaml", "https://docs.googleapis.com", srv.URL)
			params := map[string]string{"docId": standin.doc("one"), "uri": uri}
			if width != "" {
				params["width"] = width
			}
			body, err := source.BuildBody(context.Background(), docs, "image", params, "tok", imageHome(t), store.ReadSecretFile)
			if err != nil {
				t.Fatal(err)
			}
			golden := `{"requests":[{"insertInlineImage":{"endOfSegmentLocation":{},"uri":"` + uri + `"` + size(width) + `}}]}`
			var got, want any
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("built body = %s: %v", body, err)
			}
			if err := json.Unmarshal([]byte(golden), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("built body = %s, want %s", body, golden)
			}
			if n := len(standin.requests()); n != 0 {
				t.Fatalf("the stand-in heard %d requests, want none: the end of the body needs no read", n)
			}
		})
	}
}

// What the image call refuses to build: nothing is posted, and the caller
// reads the script's own words. The stand-in hears the pre-read where the
// refusal needed the document, and never a batchUpdate; where the refusal
// is of the parameters alone, it hears nothing at all.
func TestGoogleDocsImageRefusals(t *testing.T) {
	standin, srv := imageStandin(t)
	defer srv.Close()
	docs := googleDeclaration(t, "google-docs.yaml", "https://docs.googleapis.com", srv.URL)
	home := imageHome(t)
	const uri = "https://example.com/logo.png"
	const notHTTPS = "uri is no https address: Google fetches the picture itself, so it needs a public https URL"
	for _, tc := range []struct {
		name   string
		doc    string
		params map[string]string
		words  string
		pre    bool // the refusal came after the pre-read
	}{
		{"an http uri", "one", map[string]string{"uri": "http://example.com/logo.png", "find": "{{logo}}"}, notHTTPS, false},
		{"a file uri", "one", map[string]string{"uri": "file:///tmp/logo.png"}, notHTTPS, false},
		{"a path for a uri", "one", map[string]string{"uri": "/tmp/logo.png", "find": "{{logo}}"}, notHTTPS, false},
		{"a scheme and no more", "one", map[string]string{"uri": "https://"}, notHTTPS, false},
		{"a uri with a space", "one", map[string]string{"uri": "https://example.com/my logo.png"}, "uri holds a space or control character", false},
		{"a uri with a tab", "one", map[string]string{"uri": "https://example.com/a\tb.png"}, "uri holds a space or control character", false},
		{"a width of none", "one", map[string]string{"uri": uri, "width": "0"}, "width is a whole number of points above 0", false},
		{"a negative width", "one", map[string]string{"uri": uri, "find": "{{logo}}", "width": "-5"}, "width is a whole number of points above 0", false},
		{"an empty find", "one", map[string]string{"uri": uri, "find": ""}, "find is empty: leave it out to place the picture at the end of the body", false},
		{"a find with a line break", "one", map[string]string{"uri": uri, "find": "Lunch\nFriday"}, "find holds a line break: a placeholder is one line of text", false},
		{"an unknown placeholder", "one", map[string]string{"uri": uri, "find": "{{nope}}"}, "no match: find is in no text of the body", true},
		{"a placeholder of another case", "one", map[string]string{"uri": uri, "find": "{{LOGO}}"}, "no match: find is in no text of the body", true},
		{"a placeholder only in a header", "headed", map[string]string{"uri": uri, "find": "{{logo}}"}, "no match: find is in no text of the body", true},
		{"a placeholder cut by an inline picture", "gap", map[string]string{"uri": uri, "find": "{{logo}}"}, "no match: find is in no text of the body", true},
		{"a second match", "twice", map[string]string{"uri": uri, "find": "{{logo}}"}, "2 matches of find in the body: a picture goes in one place, so make find match exactly one", true},
		{"a second match in a table cell", "tabled", map[string]string{"uri": uri, "find": "{{logo}}"}, "2 matches of find in the body", true},
		{"a second match in another tab", "twotabs", map[string]string{"uri": uri, "find": "{{logo}}"}, "2 matches of find in the body", true},
		{"no such doc", "missing", map[string]string{"uri": uri, "find": "{{logo}}"}, "could not be read", true},
		{"no revision id", "no-revision", map[string]string{"uri": uri, "find": "Lunch"}, "answered no revision id", true},
		{"no tabs", "no-tabs", map[string]string{"uri": uri, "find": "Lunch"}, "answered no tabs", true},
		{"runs that overlap", "overlap", map[string]string{"uri": uri, "find": "{{logo}}"}, "answered text runs whose indices overlap", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(standin.requests())
			params := map[string]string{"docId": standin.doc(tc.doc)}
			for k, v := range tc.params {
				params[k] = v
			}
			_, err := source.BuildBody(context.Background(), docs, "image", params, "tok", home, store.ReadSecretFile)
			var refused *source.CallError
			if !errors.As(err, &refused) || refused.Reason != source.BuildRefused || !strings.Contains(err.Error(), tc.words) {
				t.Fatalf("err = %v, want %s containing %q", err, source.BuildRefused, tc.words)
			}
			want := 0
			if tc.pre {
				want = 1
			}
			past := standin.requests()[before:]
			if len(past) != want {
				t.Fatalf("the stand-in heard %d requests past the %d before, want %d", len(past), before, want)
			}
			for _, r := range past {
				if r.method == "POST" {
					t.Fatalf("the stand-in was sent %s %s: a refusal sends nothing", r.method, r.path)
				}
			}
		})
	}
}

// The built body posted: an image sends its requests in one batchUpdate and
// the answer carries one reply per request, the picture's object id among
// them. At a placeholder the stand-in hears the pre-read and the post; at
// the end of the body, the post alone.
func TestGoogleDocsImageRoundTrip(t *testing.T) {
	standin, srv := imageStandin(t)
	defer srv.Close()
	docs := googleDeclaration(t, "google-docs.yaml", "https://docs.googleapis.com", srv.URL)
	home := imageHome(t)
	for _, tc := range []struct {
		name    string
		find    string
		heard   int
		replies string
	}{
		{"at a placeholder", "{{logo}}", 2, `[{},{"insertInlineImage":{"objectId":"kix.picture"}}]`},
		{"at the end of the body", "", 1, `[{"insertInlineImage":{"objectId":"kix.picture"}}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(standin.requests())
			params := map[string]string{"docId": standin.doc("one"), "uri": "https://example.com/logo.png"}
			if tc.find != "" {
				params["find"] = tc.find
			}
			body, err := source.BuildBody(context.Background(), docs, "image", params, "tok", home, store.ReadSecretFile)
			if err != nil {
				t.Fatal(err)
			}
			res, err := source.DoKeyedBody(context.Background(), docs, "image", params, "tok", "", body, store.ReadSecretFile)
			if err != nil {
				t.Fatal(err)
			}
			gotReqs := standin.requests()[before:]
			if len(gotReqs) != tc.heard {
				t.Fatalf("the stand-in heard %d requests, want %d", len(gotReqs), tc.heard)
			}
			post := gotReqs[len(gotReqs)-1]
			if post.method != "POST" || post.path != "/v1/documents/"+standin.doc("one")+":batchUpdate" {
				t.Fatalf("post = %s %s", post.method, post.path)
			}
			if post.auth != "Bearer tok" {
				t.Fatalf("post Authorization = %q", post.auth)
			}
			var got, want any
			if err := json.Unmarshal(post.body, &got); err != nil {
				t.Fatalf("posted body = %s: %v", post.body, err)
			}
			if err := json.Unmarshal(body, &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("posted body = %s, want the built %s", post.body, body)
			}
			if want := `{"documentId":"` + standin.doc("one") + `","replies":` + tc.replies + `}`; string(res.Answer) != want {
				t.Fatalf("answer = %s, want %s", res.Answer, want)
			}
		})
	}
}

// No argument of a process the script starts holds the credential or the
// picture's address, whose signature is as good as a password, or the
// placeholder: an argument is readable by every process on the machine while
// it runs, so the script hands curl its header as a config line on its stdin
// and jq the parameters and the document on its own. A stand-in curl and a
// stand-in jq first on PATH record their arguments and delegate to the real
// ones; both ways of calling are run, the placeholder's with its read and
// the end of the body's without.
func TestGoogleDocsImageHandsNoSecretToAnyArgument(t *testing.T) {
	bin := t.TempDir()
	argv := filepath.Join(bin, "argv")
	for _, tool := range []string{"curl", "jq"} {
		real, err := exec.LookPath(tool)
		if err != nil {
			t.Skip(tool + " is not installed, and the image build script runs it")
		}
		scripttest.Write(t, filepath.Join(bin, tool),
			"printf '%s\n' \"$@\" >> \""+argv+"\"\nexec \""+real+"\" \"$@\"\n")
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	standin, srv := imageStandin(t)
	defer srv.Close()
	docs := googleDeclaration(t, "google-docs.yaml", "https://docs.googleapis.com", srv.URL)
	home := imageHome(t)
	const signed = "https://example.com/logo.png?sig=s3cr3t-signature"
	for _, find := range []string{"{{logo}}", ""} {
		params := map[string]string{"docId": standin.doc("one"), "uri": signed}
		if find != "" {
			params["find"] = find
		}
		if _, err := source.BuildBody(context.Background(), docs, "image", params, "the-secret-token", home, store.ReadSecretFile); err != nil {
			t.Fatal(err)
		}
	}
	recorded, err := os.ReadFile(argv)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"the-secret-token", "s3cr3t-signature", "{{logo}}"} {
		if strings.Contains(string(recorded), secret) {
			t.Fatalf("a process was called with %q in an argument: %s", secret, recorded)
		}
	}
	// The read is the one that took the credential, out of the arguments.
	if reqs := standin.requests(); len(reqs) != 1 || reqs[0].auth != "Bearer the-secret-token" {
		t.Fatalf("the stand-in heard %+v, want the one pre-read with the credential handed over out of the arguments", reqs)
	}
}

// imageHome installs the image call's build script from the embedded pack, as
// a source add copies it, and returns the home it stands under. The script
// reads docs with curl and lays their text out with jq.
func imageHome(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl is not installed, and the image build script reads the doc with it")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is not installed, and the image build script finds the placeholder with it")
	}
	script, err := fs.ReadFile(packs.FS, "google-docs/google-docs/build-image")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err := source.WriteCopies(home, "google-docs", []source.FileCopy{{Program: "google-docs/build-image", Data: script}}); err != nil {
		t.Fatal(err)
	}
	scripttest.Relink(t, filepath.Join(home, "sources", "google-docs"))
	return home
}

// imageDoc is one doc the image stand-in serves: its id, the
// documents.get answer it is read with, and, for a doc a table is put in,
// the revision of the read past its place, which answers the table the
// first batchUpdate placed. A doc whose batchUpdate fails fails it.
type imageDoc struct {
	id        string
	get       string
	placedRev string // "" serves get to every read
	failPost  bool
}

// imageDocs answers documents.get for docs whose text runs carry the indices
// Docs gives them, and batchUpdate with one reply per request, as Docs does:
// an insertInlineImage answers the new picture's object id and every other
// request an empty reply. For a doc with a placed revision, a read past its
// first batchUpdate answers the table it placed, with empty cells. It keeps
// every request it was sent.
type imageDocs struct {
	t    *testing.T
	mu   sync.Mutex
	got  []googleRequest
	docs map[string]imageDoc
}

func imageStandin(t *testing.T) (*imageDocs, *httptest.Server) {
	t.Helper()
	itoa := strconv.Itoa
	// run is one text run at the indices Docs gives it. Each doc below
	// writes its indices by hand: they are what the tests above hold the
	// script to, so they are never worked out from the text.
	run := func(start, end int, content string) string {
		return `{"startIndex":` + itoa(start) + `,"endIndex":` + itoa(end) + `,"textRun":{"content":` + strconv.Quote(content) + `}}`
	}
	para := func(start, end int, elements ...string) string {
		return `{"startIndex":` + itoa(start) + `,"endIndex":` + itoa(end) + `,"paragraph":{"elements":[` + strings.Join(elements, ",") + `]}}`
	}
	// A body opens with the section break at index 0, as every doc does.
	body := func(content ...string) string {
		return `{"content":[{"endIndex":1,"sectionBreak":{}},` + strings.Join(content, ",") + `]}`
	}
	// A table of one row and one cell, from the index it starts at.
	cellOf := func(start int, paragraph string) string {
		return `{"startIndex":` + itoa(start) + `,"endIndex":` + itoa(start+13) + `,"table":{"tableRows":[{"startIndex":` + itoa(start+1) +
			`,"endIndex":` + itoa(start+12) + `,"tableCells":[{"startIndex":` + itoa(start+2) + `,"endIndex":` + itoa(start+12) +
			`,"content":[` + paragraph + `]}]}]}}`
	}
	tab := func(id, documentTab string, children ...string) string {
		kids := ""
		if len(children) > 0 {
			kids = `,"childTabs":[` + strings.Join(children, ",") + `]`
		}
		return `{"tabProperties":{"tabId":` + strconv.Quote(id) + `},"documentTab":` + documentTab + kids + `}`
	}
	doc := func(revision string, tabs ...string) string {
		return `{"revisionId":` + strconv.Quote(revision) + `,"tabs":[` + strings.Join(tabs, ",") + `]}`
	}
	only := func(content ...string) string { return `{"body":` + body(content...) + `}` }
	one := doc("rev-one", tab("t.0", only(
		para(1, 23, run(1, 7, "Lunch "), run(7, 15, "{{logo}}"), run(15, 23, " places\n")),
		para(23, 39, run(23, 39, "Friday we ship.\n")))))
	split := doc("rev-split", tab("t.0", only(
		para(1, 23, run(1, 7, "Lunch "), run(7, 10, "{{l"), run(10, 15, "ogo}}"), run(15, 23, " places\n")))))
	emoji := doc("rev-emoji", tab("t.0", only(
		para(1, 26, run(1, 26, "Lunch 🍜 {{logo}} places\n")))))
	cell := doc("rev-cell", tab("t.0", only(
		para(1, 7, run(1, 7, "Quote\n")),
		cellOf(7, para(10, 19, run(10, 19, "{{logo}}\n"))))))
	gap := doc("rev-gap", tab("t.0", only(
		para(1, 11, run(1, 5, "{{lo"),
			`{"startIndex":5,"endIndex":6,"inlineObjectElement":{"inlineObjectId":"kix.inline"}}`,
			run(6, 11, "go}}\n")))))
	second := doc("rev-second",
		tab("t.0", only(para(1, 7, run(1, 7, "Intro\n")))),
		tab("t.1", only(para(1, 8, run(1, 8, "Second\n"))),
			tab("t.1.1", only(para(1, 10, run(1, 10, "{{logo}}\n"))))))
	twice := doc("rev-twice", tab("t.0", only(
		para(1, 10, run(1, 10, "{{logo}}\n")),
		para(10, 30, run(10, 30, "Again {{logo}} here\n")))))
	tabled := doc("rev-tabled", tab("t.0", only(
		para(1, 10, run(1, 10, "{{logo}}\n")),
		cellOf(10, para(13, 22, run(13, 22, "{{logo}}\n"))))))
	twotabs := doc("rev-twotabs",
		tab("t.0", only(para(1, 10, run(1, 10, "{{logo}}\n")))),
		tab("t.1", only(para(1, 10, run(1, 10, "{{logo}}\n")))))
	// The header's paragraph starts at 0 of its own segment, and the body
	// holds no placeholder: a script that searched it would find one.
	headed := doc("rev-headed", tab("t.0", `{"body":`+body(para(1, 7, run(1, 7, "Notes\n")))+
		`,"headers":{"h1":{"content":[`+para(0, 9, run(0, 9, "{{logo}}\n"))+`]}}}`))
	// The second run starts inside the first: indices no script can trust.
	overlap := doc("rev-overlap", tab("t.0", only(
		para(1, 12, run(1, 7, "abcdef"), run(4, 12, "{{logo}}")))))
	norev := `{"tabs":[` + tab("t.0", only(para(1, 7, run(1, 7, "Lunch\n")))) + `]}`
	notabs := `{"revisionId":"rev-no-tabs"}`
	// plain and reject read like one: plain answers no placed table, so
	// the table call's second read finds none, and reject fails the
	// batchUpdate that would place it.
	plain := doc("rev-plain", tab("t.0", only(
		para(1, 23, run(1, 7, "Lunch "), run(7, 15, "{{logo}}"), run(15, 23, " places\n")),
		para(23, 39, run(23, 39, "Friday we ship.\n")))))
	reject := doc("rev-reject", tab("t.0", only(
		para(1, 23, run(1, 7, "Lunch "), run(7, 15, "{{logo}}"), run(15, 23, " places\n")),
		para(23, 39, run(23, 39, "Friday we ship.\n")))))
	s := &imageDocs{t: t, docs: map[string]imageDoc{
		"one":         {id: "1ImgOne0123456789abcdefghijklmno", get: one, placedRev: "rev-one-placed"},
		"split":       {id: "1ImgSplit0123456789abcdefghijklm", get: split, placedRev: "rev-split-placed"},
		"emoji":       {id: "1ImgEmoji0123456789abcdefghijkl", get: emoji, placedRev: "rev-emoji-placed"},
		"cell":        {id: "1ImgCell0123456789abcdefghijklmn", get: cell, placedRev: "rev-cell-placed"},
		"gap":         {id: "1ImgGap0123456789abcdefghijklmno", get: gap, placedRev: "rev-gap-placed"},
		"second":      {id: "1ImgSecond0123456789abcdefghijkl", get: second, placedRev: "rev-second-placed"},
		"twice":       {id: "1ImgTwice0123456789abcdefghijklm", get: twice},
		"tabled":      {id: "1ImgTabled0123456789abcdefghijkl", get: tabled},
		"twotabs":     {id: "1ImgTwoTabs0123456789abcdefghijk", get: twotabs},
		"headed":      {id: "1ImgHeaded0123456789abcdefghijkl", get: headed},
		"overlap":     {id: "1ImgOverlap0123456789abcdefghijk", get: overlap},
		"no-revision": {id: "1ImgNoRev0123456789abcdefghijklm", get: norev},
		"no-tabs":     {id: "1ImgNoTabs0123456789abcdefghijkl", get: notabs},
		"plain":       {id: "1ImgPlain0123456789abcdefghijklm", get: plain},
		"reject":      {id: "1ImgReject0123456789abcdefghijk", get: reject, failPost: true},
	}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.got = append(s.got, googleRequest{method: r.Method, path: r.URL.Path, query: r.URL.Query(), auth: r.Header.Get("Authorization"), contentType: r.Header.Get("Content-Type"), body: body})
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		for _, d := range s.docs {
			switch {
			case r.Method == "GET" && r.URL.Path == "/v1/documents/"+d.id:
				if placed := s.placed(d); placed != "" {
					io.WriteString(w, placed)
					return
				}
				io.WriteString(w, d.get)
				return
			case r.Method == "POST" && r.URL.Path == "/v1/documents/"+d.id+":batchUpdate":
				if d.failPost {
					w.WriteHeader(http.StatusBadRequest)
					io.WriteString(w, `{"error":{"code":400,"message":"the table does not fit"}}`)
					return
				}
				var posted struct {
					Requests []map[string]json.RawMessage `json:"requests"`
				}
				if err := json.Unmarshal(body, &posted); err != nil {
					http.Error(w, `{"error":{"code":400,"message":"no requests"}}`, http.StatusBadRequest)
					return
				}
				var replies []string
				for _, req := range posted.Requests {
					if _, ok := req["insertInlineImage"]; ok {
						replies = append(replies, `{"insertInlineImage":{"objectId":"kix.picture"}}`)
					} else {
						replies = append(replies, `{}`)
					}
				}
				io.WriteString(w, `{"documentId":"`+d.id+`","replies":[`+strings.Join(replies, ",")+`],"writeControl":{"requiredRevisionId":"9"}}`)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"error":{"code":404,"message":"no such doc"}}`)
	}))
	return s, srv
}

// placed is the documents.get answer for a doc whose table the stand-in
// was already sent: the table the first batchUpdate placed, with empty
// cells, one past the placeholder it deleted, under the doc's placed
// revision. It follows the Tables how-to's index table: the table stands
// one index past its location, an empty cell runs two units, and a row
// one past its cells. "" where the doc takes no table, or none was sent
// yet.
func (s *imageDocs) placed(d imageDoc) string {
	if d.placedRev == "" {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.got {
		if r.method != "POST" || r.path != "/v1/documents/"+d.id+":batchUpdate" {
			continue
		}
		var posted struct {
			Requests []struct {
				DeleteContentRange struct {
					Range struct {
						StartIndex int    `json:"startIndex"`
						TabID      string `json:"tabId"`
					} `json:"range"`
				} `json:"deleteContentRange"`
				InsertTable struct {
					Rows    int `json:"rows"`
					Columns int `json:"columns"`
				} `json:"insertTable"`
			} `json:"requests"`
		}
		if err := json.Unmarshal(r.body, &posted); err != nil {
			continue
		}
		if len(posted.Requests) < 2 || posted.Requests[1].InsertTable.Rows == 0 {
			continue
		}
		at := posted.Requests[0].DeleteContentRange.Range
		in := posted.Requests[1].InsertTable
		return placedTable(d.placedRev, at.TabID, at.StartIndex+1, in.Rows, in.Columns)
	}
	return ""
}

// placedTable is a documents.get answer for a doc holding one table of
// empty cells at tableStart in tab, under revision rev.
func placedTable(rev, tab string, tableStart, rows, cols int) string {
	itoa := strconv.Itoa
	end := tableStart + 2 + rows*(1+2*cols)
	var rs []string
	for r := 0; r < rows; r++ {
		rowStart := tableStart + 1 + r*(1+2*cols)
		var cs []string
		for c := 0; c < cols; c++ {
			cellStart := rowStart + 1 + 2*c
			para := cellStart + 1
			cs = append(cs, `{"startIndex":`+itoa(cellStart)+`,"endIndex":`+itoa(cellStart+2)+
				`,"content":[{"startIndex":`+itoa(para)+`,"endIndex":`+itoa(para+1)+
				`,"paragraph":{"elements":[{"startIndex":`+itoa(para)+`,"endIndex":`+itoa(para+1)+
				`,"textRun":{"content":"\n"}}]}}]}`)
		}
		rs = append(rs, `{"startIndex":`+itoa(rowStart)+`,"endIndex":`+itoa(rowStart+1+2*cols)+
			`,"tableCells":[`+strings.Join(cs, ",")+`]}`)
	}
	return `{"revisionId":` + strconv.Quote(rev) + `,"tabs":[{"tabProperties":{"tabId":` + strconv.Quote(tab) +
		`},"documentTab":{"body":{"content":[{"endIndex":1,"sectionBreak":{}},` +
		`{"startIndex":` + itoa(tableStart) + `,"endIndex":` + itoa(end) + `,"table":{"tableRows":[` +
		strings.Join(rs, ",") + `]}}]}}}]}`
}

// doc is the id of the image stand-in's named doc; "missing" is one it has
// no answer for.
func (s *imageDocs) doc(name string) string {
	if d, ok := s.docs[name]; ok {
		return d.id
	}
	return "1Missing0123456789abcdefghijklmno"
}

func (s *imageDocs) requests() []googleRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]googleRequest(nil), s.got...)
}
