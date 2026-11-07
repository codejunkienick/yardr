package pack_test

import (
	"bytes"
	"io/fs"
	"net/http"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/pack"
	"yardr.dev/yardr/packs"
)

// dressing is a frame as FrameFor hands it over: its bytes with the identity
// a refusal names.
func dressing(frame string) pack.ResolvedFrame {
	return pack.ResolvedFrame{Pack: "brand", File: "frames/page.html", Path: "/home/packs/brand/frames/page.html", SHA256: sumOf(frame), Bytes: []byte(frame)}
}

// Dress makes a page by the three substitutions and no others: the title
// marker, the content marker and what stands between <title> and </title>
// are the places, each filled once from the frame as it stands, so the same
// bytes make the same page, a title is escaped as text in both of its
// places, and words that hold a marker's bytes are never substituted again.
func TestDressMakesAPageByThreeSubstitutions(t *testing.T) {
	const title = `Fish & Chips <b>"x"</b> --> <!--yardr:content-->`
	const escaped = `Fish &amp; Chips &lt;b&gt;&#34;x&#34;&lt;/b&gt; --&gt; &lt;!--yardr:content--&gt;`
	const fragment = `<p>one <!--yardr:title--> two</p><script>if (a < b && c) { go("</p>") }</script>`

	got, err := pack.Dress(dressing(pageFrame), []byte(fragment), title)
	if err != nil {
		t.Fatal(err)
	}
	// Written without Dress's own scan: the frame has one of each place.
	want := strings.NewReplacer(
		"<title>untitled</title>", "<title>"+escaped+"</title>",
		"<!--yardr:title-->", escaped,
		"<!--yardr:content-->", fragment,
	).Replace(pageFrame)
	if string(got) != want {
		t.Fatalf("the page is\n%s\nwant\n%s", got, want)
	}
	if n := strings.Count(string(got), escaped); n != 2 {
		t.Errorf("the title stands %d times, in its <title> and its heading", n)
	}
	// The fragment's own marker stays where its author wrote it, and the
	// title that spelled the content marker is text, not a place.
	if n := strings.Count(string(got), "<!--yardr:title-->"); n != 1 {
		t.Errorf("the fragment's comment stands %d times", n)
	}
	again, err := pack.Dress(dressing(pageFrame), []byte(fragment), title)
	if err != nil || !bytes.Equal(got, again) {
		t.Errorf("the same words make another page: %v", err)
	}

	// Which marker comes first in the file does not matter.
	flipped := strings.Replace(strings.Replace(pageFrame, "<h1><!--yardr:title--></h1>\n<!--yardr:content-->", "<!--yardr:content--><h1><!--yardr:title--></h1>", 1), "untitled", "x", 1)
	got, err = pack.Dress(dressing(flipped), []byte("<p>hi</p>"), "Hello")
	if err != nil || !strings.Contains(string(got), "<title>Hello</title>") || !strings.Contains(string(got), "<p>hi</p><h1>Hello</h1>") {
		t.Errorf("a frame with its content first made %q, %v", got, err)
	}
}

// What a title, a fragment and a frame are each refused for, with why: the
// refusal names the thing and what to do, and makes no page.
func TestDressRefuses(t *testing.T) {
	frameWith := func(old, new string) string { return strings.Replace(pageFrame, old, new, 1) }
	frag := "<p>words</p>"
	for _, c := range []struct {
		name, frame, fragment, title string
		want                         string
	}{
		{"no title", pageFrame, frag, "  ", "the title is empty"},
		{"a title of two lines", pageFrame, frag, "one\ntwo", "one line of plain text"},
		{"a title with a control character", pageFrame, frag, "one\x1b[31mtwo", "one line of plain text"},
		{"a title with a bidi control", pageFrame, frag, "evil\u202egnp", "one line of plain text"},
		{"a long title", pageFrame, frag, strings.Repeat("x", 201), "at most 200 bytes, not 201"},
		{"an empty fragment", pageFrame, " \n\t", "T", "the fragment is empty"},
		{"a fragment that is a whole page", pageFrame, "<!DOCTYPE html><html><head><title>x</title></head><body><p>hi</p></body></html>", "T", "a whole document, with a doctype in it"},
		{"a lower-case doctype", pageFrame, "<!doctype html><p>hi</p>", "T", "a whole document, with a doctype in it"},
		{"a fragment with a body tag", pageFrame, `<body onload="go()"><p>hi</p>`, "T", "with a <body> tag in it"},
		{"a fragment with an html tag", pageFrame, `<HTML><p>hi</p>`, "T", "with a <html> tag in it"},
		{"a fragment with a head tag", pageFrame, `<head></head><p>hi</p>`, "T", "with a <head> tag in it"},
		{"a fragment that closes the body", pageFrame, `<p>hi</p></body>`, "T", "with a </body> tag in it"},
		{"a frame with no title marker", frameWith("<h1><!--yardr:title--></h1>", "<h1>x</h1>"), frag, "T", "has no <!--yardr:title-->"},
		{"a frame with no content marker", frameWith("<!--yardr:content-->", ""), frag, "T", "has no <!--yardr:content-->"},
		{"a frame with two title markers", frameWith("<!--yardr:content-->", "<!--yardr:title--><!--yardr:content-->"), frag, "T", "line 9: the title marker <!--yardr:title--> stands twice"},
		{"a frame with two content markers", frameWith("<!--yardr:content-->", "<!--yardr:content--><!--yardr:content-->"), frag, "T", "the content marker <!--yardr:content--> stands twice"},
		{"a frame with no title element", frameWith("<title>untitled</title>\n", ""), frag, "T", "has no <title> element"},
		{"a content marker in a script", frameWith("if (who)", "if (who) /* <!--yardr:content--> */"), frag, "T", "in the text of a <script> element"},
		{"a title marker in an attribute value", frameWith("<body>", `<body data-t="<!--yardr:title-->">`), frag, "T", "inside a tag or a declaration"},
		{"a title marker in the head", strings.Replace(frameWith("<title>untitled</title>", "<title>untitled</title><!--yardr:title-->"), "<h1><!--yardr:title--></h1>", "<h1></h1>", 1), frag, "T", "the title marker stands in the head"},
		{"a content marker in a heading", frameWith("<!--yardr:content-->", "<h2><!--yardr:content--></h2>"), frag, "T", "directly in a <h2> element"},
		{"a content marker in a button", frameWith("<!--yardr:content-->", "<button><!--yardr:content--></button>"), frag, "T", "inside a <button> element"},
		{"a frame with a second title element", frameWith("</head>", "<title>again</title></head>"), frag, "T", "a second <title> element"},
		{"the title marker in the title element", frameWith("<title>untitled</title>", "<title><!--yardr:title--></title>"), frag, "T", "the title marker stands inside the <title> element"},
		{"a content marker in a paragraph", frameWith("<!--yardr:content-->", "<p><!--yardr:content--></p>"), frag, "T", "inside a <p> element"},
		{"a frame over the bound", pageFrame + "<!-- " + strings.Repeat("x", pack.MaxFrame) + " -->", frag, "T", "holds 262"},
		{"a frame no page is read from", "\xef\xbb\xbf" + pageFrame, frag, "T", "would not be read as a page"},
		{"a frame that opens with a meta tag", frameWith("<!DOCTYPE html>\n<html>\n<head>\n", "<meta charset=utf-8>\n<html>\n<head>\n"), frag, "T", "would not be read as a page"},
	} {
		got, err := pack.Dress(dressing(c.frame), []byte(c.fragment), c.title)
		if err == nil || !strings.Contains(err.Error(), c.want) || len(got) != 0 {
			t.Errorf("%s: Dress = %d bytes, %v; want a refusal with %q", c.name, len(got), err, c.want)
			continue
		}
		// A frame's own fault names the frame and where its installed copy is,
		// since the operator mends that one.
		if strings.Contains(err.Error(), "the frame that dresses pages here") &&
			(!strings.Contains(err.Error(), "frames/page.html of pack brand") || !strings.Contains(err.Error(), "/home/packs/brand/frames/page.html")) {
			t.Errorf("%s: the refusal does not name the frame: %v", c.name, err)
		}
	}

	// What reads like a document and is not one: the tags in a comment, a
	// script, an attribute, escaped, or in an svg's own element names.
	for _, ok := range []string{
		"<!-- <body> and <!DOCTYPE html> -->\n<p>hi</p>",
		`<script>document.write("<body></body></html>")</script><p>hi</p>`,
		`<p title="<body>">&lt;html&gt;</p><textarea></body></textarea>`,
		`<svg viewBox="0 0 4 4"><title>a chart</title><rect width="2" height="2"/></svg>`,
	} {
		if _, err := pack.Dress(dressing(pageFrame), []byte(ok), "T"); err != nil {
			t.Errorf("fragment %q refused: %v", ok, err)
		}
	}
}

// The default frame makes a page the yard reads as a page, from a fragment
// with no <h1> of its own, and Contract is the comment it opens with: the
// frame author's own words, found where a browser ends a comment.
func TestDressTheDefaultFrameAndItsContract(t *testing.T) {
	text, err := fs.ReadFile(packs.FS, packs.PageFrame+"/frames/page.html")
	if err != nil {
		t.Fatal(err)
	}
	if problems := pack.FrameProblems(text); len(problems) > 0 {
		t.Fatalf("the default frame: %v", problems)
	}
	got, err := pack.Dress(pack.ResolvedFrame{Pack: packs.PageFrame, File: "frames/page.html", Bytes: text}, []byte(`<p class="lede">hello</p>`), "Two weeks <in> the yard")
	if err != nil {
		t.Fatal(err)
	}
	if typ := http.DetectContentType(got); typ != "text/html; charset=utf-8" {
		t.Errorf("the dressed page reads as %s", typ)
	}
	page := string(got)
	if strings.Count(page, "Two weeks &lt;in&gt; the yard") != 2 || strings.Count(page, "<h1>") != 1 || !strings.Contains(page, `<p class="lede">hello</p>`) || strings.Contains(page, "<!--yardr:") {
		t.Errorf("the dressed page:\n%.1500s", page)
	}

	contract := pack.Contract(text)
	if !strings.HasPrefix(contract, "yardr page frame (pageframe), the default.") || !strings.Contains(contract, "Classes:") || !strings.Contains(contract, "yardr:title") || strings.Contains(contract, "-->") {
		t.Errorf("the default frame's contract:\n%s", contract)
	}
	for in, want := range map[string]string{
		"<p>no comment</p>":                    "",
		"  \n\t<!-- two\nlines -->\n<p>x</p>":  "two\nlines",
		"\xef\xbb\xbf<!--bom-->":               "bom",
		"<!-- ends the other way --!> <p>x":    "ends the other way",
		"<!-- a <!-- nested --> b -->":         "a <!-- nested",
		"<!-- never ends":                      "never ends",
		"<!-- a - dash -->":                    "a - dash",
		"<!----><p>empty</p>":                  "",
		"<!DOCTYPE html><!-- not leading -->":  "",
		"<!-- one --><!-- two --><p>x</p>":     "one",
		"<!--yardr:content--> is no contract.": "yardr:content",
	} {
		if got := pack.Contract([]byte(in)); got != want {
			t.Errorf("Contract(%q) = %q, want %q", in, got, want)
		}
	}
}
