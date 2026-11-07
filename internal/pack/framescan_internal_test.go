package pack

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// frameOf is a frame with <title> on line 4, head on line 5 and the body's
// first line on line 8, so a row's lines can be told from where it stands.
func frameOf(head, body string) string {
	return "<!DOCTYPE html>\n<html>\n<head>\n<title>t</title>\n" + head + "\n</head>\n<body>\n" + body + "\n</body>\n</html>\n"
}

const (
	titleBlock   = "<h1><!--yardr:title--></h1>"
	contentBlock = "<main><!--yardr:content--></main>"
	// met is a body that meets the contract: its markers are on lines 8 and 9.
	met = titleBlock + "\n" + contentBlock
)

// A fault a row expects: the line, and words of its message.
type want struct {
	line int
	msg  string
}

// frameRows are the frames a browser reads one way, as rows of what
// frameFaults says of each. They are written from the HTML standard's
// tokenizer and tree rules, not from what the scan happens to do, and each
// row the scan could get wrong in a way a search for the marker's text would
// not stands beside one that shows the difference.
var frameRows = []struct {
	name string
	html string
	want []want
}{
	// What a browser reads as the contract asks.
	{"a frame that meets the contract", frameOf("", met), nil},
	{"no html, head or body tags", "<!DOCTYPE html>\n<title>t</title>\n" + titleBlock + "\n<div><!--yardr:content--></div>\n", nil},
	{"a byte order mark in front", "\xef\xbb\xbf" + frameOf("", met), nil},
	{"lines ended with carriage returns", strings.ReplaceAll(frameOf("", met), "\n", "\r\n"), nil},
	{"tags in capitals", "<!DOCTYPE html>\n<HTML><HEAD><TITLE>t</TITLE></HEAD><BODY><H1><!--yardr:title--></H1><MAIN><!--yardr:content--></MAIN></BODY></HTML>\n", nil},
	{"a marker after the empty comments <!--> and <!--->", frameOf("", "<!--><!---><h1><!--><!--yardr:title--></h1>\n"+contentBlock), nil},
	{"a marker after a comment that --!> ends", frameOf("", "<h1><!-- a --!><!--yardr:title--></h1>\n"+contentBlock), nil},
	{"a marker after a nested <!-- that the first > ends", frameOf("", "<h1><!-- <!--> <!--yardr:title--></h1>\n"+contentBlock), nil},
	{"a > in quoted attribute values", frameOf("", "<h1 title=\"a > b\"><!--yardr:title--></h1>\n<main data-x='<b>'><!--yardr:content--></main>"), nil},
	{"an unquoted attribute value that ends in a slash", frameOf("", "<h1><a href=/x/><!--yardr:title--></a></h1>\n"+contentBlock), nil},
	{"an svg that has a <title> of its own", frameOf("", met+"\n<svg><title>icon</title><text>x</text></svg>"), nil},
	{"an inner svg end tag leaves its outer svg open", frameOf("", titleBlock+"\n<svg><svg></svg><!--yardr:content--></svg>"), []want{{9, "inside an <svg> or <math> element"}}},
	{"an inner math end tag leaves its outer math open", frameOf("", titleBlock+"\n<math><math></math><!--yardr:content--></math>"), []want{{9, "inside an <svg> or <math> element"}}},
	{"a div in an svg foreignObject stays in the svg", frameOf("", titleBlock+"\n<svg><foreignObject><div><!--yardr:content--></div></foreignObject></svg>"), []want{{9, "inside an <svg> or <math> element"}}},
	{"a breakout after a closed foreignObject leaves the svg", frameOf("", titleBlock+"\n<svg><foreignObject></foreignObject><div><!--yardr:content--></div></svg>"), nil},
	{"a div at a MathML text integration point stays in math", frameOf("", titleBlock+"\n<math><mi><div><!--yardr:content--></div></mi></math>"), []want{{9, "inside an <svg> or <math> element"}}},
	{"a div in a MathML HTML annotation stays in math", frameOf("", titleBlock+"\n<math><annotation-xml encoding='text&#x2F;html'><div><!--yardr:content--></div></annotation-xml></math>"), []want{{9, "inside an <svg> or <math> element"}}},
	{"a div breaks out of a MathML XML annotation", frameOf("", titleBlock+"\n<math><annotation-xml encoding=application/xml></annotation-xml><div><!--yardr:content--></div></math>"), nil},
	{"an svg end tag that closes no math", frameOf("", titleBlock+"\n<math></svg><title>x</title></math>\n"+contentBlock), nil},
	{"a marker after a tag that leaves an svg", frameOf("", titleBlock+"\n<svg><p>x</p><!--yardr:content--></svg>"), nil},
	{"a script that quotes <!-- and -->", frameOf(`<script>var a = "<!--", b = "-->";</script>`, met), nil},
	{"a script that ends in capitals and a space", frameOf("<SCRIPT>x</SCRIPT >", met), nil},
	{"a noscript that holds a style", frameOf("<noscript><style>a{}</style></noscript>", met), nil},
	{"a template with markup of its own", frameOf("<template><p>x</p></template>", met), nil},
	{"void tags in the head", frameOf(`<meta charset="utf-8"><link rel=stylesheet href=x>`, met), nil},
	{"the content in a layout table's div", frameOf("", titleBlock+"\n<table><tr><td><div><!--yardr:content--></div></td></tr></table>"), nil},
	{"the title in a table cell", frameOf("", "<table><tr><td><!--yardr:title--></td></tr></table>\n"+contentBlock), nil},
	{"a paragraph closed before the content", frameOf("", titleBlock+"\n<p>intro</p>\n"+contentBlock), nil},

	// What a browser does with markup it mends, which a scan that closes
	// elements in its own way gets wrong in a way a refusal does not show.
	{"a paragraph that a block start closes", frameOf("", titleBlock+"\n<p>intro\n"+contentBlock), nil},
	{"a paragraph that an end tag of the div around it closes", frameOf("", titleBlock+"\n<div><p>intro</div>\n"+contentBlock), nil},
	{"the body brought back by a tag after </body>", "<!DOCTYPE html>\n<html>\n<head>\n<title>t</title>\n</head>\n<body>\n" + titleBlock + "\n</body>\n<main><!--yardr:content--></main>\n</html>\n", nil},
	{"a table part where no table is", frameOf("", "<h1><tr><!--yardr:title--></h1>\n"+contentBlock), nil},
	{"an end tag that closes through an svg left open", frameOf("", "<h1><!--yardr:title--><svg></h1>\n"+contentBlock), nil},
	{"a stray < begins the body", "<!DOCTYPE html>\n<<title>t</title>\n" + met + "\n", []want{{2, "the <title> element stands in the body"}}},
	{"a table part begins the body though it is ignored", "<!DOCTYPE html>\n<td><title>t</title>\n" + met + "\n", []want{{2, "the <title> element stands in the body"}}},
	{"an end tag that does not look past a table cell", frameOf("", titleBlock+"\n<button><table><tr><td></button><!--yardr:content-->"), []want{{9, "content marker stands inside a <button> element"}}},
	{"a second <body> that a browser ignores", "<!DOCTYPE html>\n<html>\n<head>\n<title>t</title>\n<table></head>\n<body>\n" + titleBlock + "\n<!--yardr:content-->\n</body>\n</html>\n", []want{{8, "content marker stands directly in a <table> element"}}},
	{"an end tag closes the innermost of two nested elements", frameOf("", titleBlock+"\n<p><span><span></span><!--yardr:content--></span></p>"), []want{{9, "inside a <p> element"}}},
	{"a link that nobody closes goes on in what follows", frameOf("", "<h1><a href=x><!--yardr:title--></h1>\n"+contentBlock), []want{{9, "content marker stands inside a <a> element"}}},
	{"a template that </head> and <body> do not leave", "<!DOCTYPE html>\n<html>\n<head>\n<title>t</title>\n<template>\n</head>\n<p>x</p><body>\n" + met + "\n</body>\n</html>\n", []want{{8, "title marker stands inside a <template> element"}, {9, "content marker stands inside a <template> element"}}},

	// What a scan must stay fast on, and a browser reads another way.
	{"elements nested 500 deep", frameOf("", strings.Repeat("<div>", 500)+met+strings.Repeat("</div>", 500)), nil},
	{"elements nested more than 512 deep", frameOf("", strings.Repeat("<div>", 600)+met+strings.Repeat("</div>", 600)), []want{{8, "nests elements more than 512 deep"}}},
	{"svgs nested more than 512 deep", frameOf("", met+"\n"+strings.Repeat("<svg>", 600)+strings.Repeat("</svg>", 600)), []want{{10, "nests elements more than 512 deep"}}},

	// What the contract counts.
	{"no title marker", frameOf("", contentBlock), []want{{0, "has no <!--yardr:title-->"}}},
	{"a title marker twice", frameOf("", titleBlock+"\n"+titleBlock+"\n"+contentBlock), []want{{9, "title marker <!--yardr:title--> stands twice (the first is on line 8)"}}},
	{"no content marker", frameOf("", titleBlock), []want{{0, "has no <!--yardr:content-->"}}},
	{"a content marker twice", frameOf("", met+"\n"+contentBlock), []want{{10, "content marker <!--yardr:content--> stands twice (the first is on line 9)"}}},
	{"no <title> element", "<!DOCTYPE html>\n<html>\n<head>\n</head>\n<body>\n" + met + "\n</body>\n</html>\n", []want{{0, "has no <title> element"}}},
	{"a second <title> element", frameOf("<title>again</title>", met), []want{{5, "a second <title> element (the first is on line 4)"}}},
	{"a <title> element in a template", frameOf("<template><title>x</title></template>", met), []want{{5, "the <title> element stands inside a <template> element"}}},
	{"a <title> element only in a template", "<!DOCTYPE html>\n<html>\n<head>\n<template><title>x</title></template>\n</head>\n<body>\n" + met + "\n</body>\n</html>\n", []want{{0, "has no <title> element"}, {4, "inside a <template> element, which is no title of the page"}}},
	{"a <title> element in the body", "<!DOCTYPE html>\n<html>\n<head>\n</head>\n<body>\n<title>t</title>\n" + met + "\n</body>\n</html>\n", []want{{6, "the <title> element stands in the body"}}},
	{"the title marker in the <title> element", "<!DOCTYPE html>\n<html>\n<head>\n<title><!--yardr:title--></title>\n</head>\n<body>\n<h1>x</h1>\n" + contentBlock + "\n</body>\n</html>\n", []want{{4, "the title marker stands inside the <title> element"}}},
	{"the content marker in the <title> element", "<!DOCTYPE html>\n<html>\n<head>\n<title><!--yardr:content--></title>\n</head>\n<body>\n" + titleBlock + "\n</body>\n</html>\n", []want{{4, "the content marker stands in the text of a <title> element"}}},

	// Where a browser reads the marker's bytes as text.
	{"the content marker in a script", frameOf("", titleBlock+"\n<script><!--yardr:content--></script>"), []want{{9, "in the text of a <script> element"}}},
	{"the title marker in a script", frameOf("", contentBlock+"\n<script><!--yardr:title--></script>"), []want{{9, "title marker stands in the text of a <script> element"}}},
	{"the content marker in a style", frameOf("", titleBlock+"\n<style><!--yardr:content--></style>"), []want{{9, "in the text of a <style> element"}}},
	{"the title marker in a style", frameOf("", contentBlock+"\n<style><!--yardr:title--></style>"), []want{{9, "title marker stands in the text of a <style> element"}}},
	{"the content marker in a textarea", frameOf("", titleBlock+"\n<textarea><!--yardr:content--></textarea>"), []want{{9, "in the text of a <textarea> element"}}},
	{"the title marker in a textarea", frameOf("", contentBlock+"\n<textarea><!--yardr:title--></textarea>"), []want{{9, "title marker stands in the text of a <textarea> element"}}},
	{"the content marker in an iframe", frameOf("", titleBlock+"\n<iframe><!--yardr:content--></iframe>"), []want{{9, "in the text of a <iframe> element"}}},
	{"the content marker in an xmp", frameOf("", titleBlock+"\n<xmp><!--yardr:content--></xmp>"), []want{{9, "in the text of a <xmp> element"}}},
	{"the content marker in a noembed", frameOf("", titleBlock+"\n<noembed><!--yardr:content--></noembed>"), []want{{9, "in the text of a <noembed> element"}}},
	{"the content marker in a noframes", frameOf("", titleBlock+"\n<noframes><!--yardr:content--></noframes>"), []want{{9, "in the text of a <noframes> element"}}},
	{"the content marker in a noscript", frameOf("", titleBlock+"\n<noscript><!--yardr:content--></noscript>"), []want{{9, "in the text of a <noscript> element"}}},
	{"the content marker after a plaintext", frameOf("", titleBlock+"\n<plaintext><!--yardr:content-->"), []want{{9, "in the text of a <plaintext> element"}}},
	{"the content marker in a script a double escape keeps open", frameOf("", titleBlock+"\n<script>var a = \"<!--<script>\"; </script>\n"+contentBlock), []want{{10, "in the text of a <script> element"}}},
	{"the content marker in a script that <!-- escapes and --> returns", frameOf("", titleBlock+"\n<script>\"<!--\"; \"-->\"</script>\n"+contentBlock), nil},
	{"a script end tag with attributes", frameOf("", titleBlock+"\n<script></script x=\">\">"+contentBlock), nil},
	{"a script end tag that a longer name is not", frameOf("", titleBlock+"\n<script></scripts><!--yardr:content--></script>"), []want{{9, "in the text of a <script> element"}}},

	// Where the bytes are no comment of their own.
	{"the content marker in a longer comment", frameOf("", titleBlock+"\n<!-- <!--yardr:content--> -->"), []want{{9, "content marker is part of a longer comment"}}},
	{"the title marker in a longer comment", frameOf("", contentBlock+"\n<!-- the title: <!--yardr:title--> -->"), []want{{9, "title marker is part of a longer comment"}}},
	{"the content marker in a quoted attribute value", frameOf("", titleBlock+"\n<div data-x=\"<!--yardr:content-->\"></div>"), []want{{9, "inside a tag or a declaration"}}},
	{"the content marker in a single-quoted attribute value", frameOf("", titleBlock+"\n<div data-x='<!--yardr:content-->'></div>"), []want{{9, "inside a tag or a declaration"}}},
	{"the content marker as an attribute name", frameOf("", titleBlock+"\n<div <!--yardr:content-->></div>"), []want{{9, "inside a tag or a declaration"}}},
	{"the content marker in an unquoted attribute value", frameOf("", titleBlock+"\n<div data-x=<!--yardr:content-->></div>"), []want{{9, "inside a tag or a declaration"}}},
	{"the content marker in an end tag", frameOf("", titleBlock+"\n<div></div <!--yardr:content-->>"), []want{{9, "inside a tag or a declaration"}}},
	{"the content marker in a doctype", frameOf("", titleBlock+"\n<!DOCTYPE <!--yardr:content-->>"), []want{{9, "inside a tag or a declaration"}}},
	{"the content marker in a bogus comment", frameOf("", titleBlock+"\n<?php <!--yardr:content--> ?>"), []want{{9, "inside a tag or a declaration"}}},
	{"the content marker in a CDATA section of the page", frameOf("", titleBlock+"\n<![CDATA[<!--yardr:content-->]]>"), []want{{9, "inside a tag or a declaration"}}},
	{"the content marker in a CDATA section of an svg", frameOf("", titleBlock+"\n<svg><![CDATA[<!--yardr:content-->]]></svg>"), []want{{9, "in a CDATA section"}}},
	{"the content marker in an svg", frameOf("", titleBlock+"\n<svg><!--yardr:content--></svg>"), []want{{9, "inside an <svg> or <math> element"}}},
	{"the content marker in a math", frameOf("", titleBlock+"\n<math><!--yardr:content--></math>"), []want{{9, "inside an <svg> or <math> element"}}},
	{"the content marker in a template", frameOf("", titleBlock+"\n<template><!--yardr:content--></template>"), []want{{9, "inside a <template> element"}}},

	// Where the title and the content cannot stand.
	{"the title marker in the head", frameOf("<!--yardr:title-->", contentBlock), []want{{5, "title marker stands in the head, before the body begins"}}},
	{"the content marker in the head", frameOf("<!--yardr:content-->", titleBlock), []want{{5, "content marker stands in the head, before the body begins"}}},
	{"the content marker after the body", "<!DOCTYPE html>\n<html>\n<head>\n<title>t</title>\n</head>\n<body>\n" + titleBlock + "\n</body>\n<!--yardr:content-->\n</html>\n", []want{{9, "after the end of the body"}}},
	{"the content marker in a paragraph", frameOf("", titleBlock+"\n<p><!--yardr:content--></p>"), []want{{9, "inside a <p> element"}}},
	{"the content marker in a span in a paragraph", frameOf("", titleBlock+"\n<p><span><!--yardr:content--></span></p>"), []want{{9, "inside a <p> element"}}},
	{"the content marker in a heading", frameOf("", titleBlock+"\n<h2><!--yardr:content--></h2>"), []want{{9, "directly in a <h2> element"}}},
	{"the content marker in a button", frameOf("", titleBlock+"\n<button><!--yardr:content--></button>"), []want{{9, "inside a <button> element"}}},
	{"the content marker in a link", frameOf("", titleBlock+"\n<a href=x><!--yardr:content--></a>"), []want{{9, "inside a <a> element"}}},
	{"the content marker in a list item", frameOf("", titleBlock+"\n<ul><li><!--yardr:content--></li></ul>"), []want{{9, "directly in a <li> element"}}},
	{"the content marker in a list", frameOf("", titleBlock+"\n<ol><!--yardr:content--></ol>"), []want{{9, "directly in a <ol> element"}}},
	{"the content marker in a table cell", frameOf("", titleBlock+"\n<table><tr><td><!--yardr:content--></td></tr></table>"), []want{{9, "directly in a <td> element"}}},
	{"the content marker in a table", frameOf("", titleBlock+"\n<table><!--yardr:content--></table>"), []want{{9, "directly in a <table> element"}}},
	{"the title marker in a table row", frameOf("", contentBlock+"\n<table><tr><!--yardr:title--></tr></table>"), []want{{9, "title marker stands directly in a <tr> element"}}},
}

func TestFrameFaults(t *testing.T) {
	t.Parallel()
	for _, c := range frameRows {
		t.Run(c.name, func(t *testing.T) {
			got := frameFaults([]byte(c.html))
			var lines []string
			for _, f := range got {
				lines = append(lines, fmt.Sprintf("  line %d: %s", f.line, f.msg))
			}
			if len(got) != len(c.want) {
				t.Fatalf("faults =\n%s\nwant %d: %v\nframe:\n%s", strings.Join(lines, "\n"), len(c.want), c.want, c.html)
			}
			for i, w := range c.want {
				if got[i].line != w.line || !strings.Contains(got[i].msg, w.msg) {
					t.Errorf("fault %d = line %d: %s\nwant line %d containing %q\nframe:\n%s", i, got[i].line, got[i].msg, w.line, w.msg, c.html)
				}
			}
		})
	}
}

// Each of those frames, as the frame of a pack: a fault of the file as a
// whole is told at the frame key of pack.yaml, and one at a place in it at
// that line of the frame, and both name the key, so that whoever checks the
// pack finds the line to mend.
func TestFrameRefusalsNameFileLineAndKey(t *testing.T) {
	t.Parallel()
	for _, c := range frameRows {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "frames"), 0o755); err != nil {
				t.Fatal(err)
			}
			for name, text := range map[string]string{"pack.yaml": "name: framed\napi: 4\nframe: frames/page.html\n", "frames/page.html": c.html} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			p, err := LoadDir(dir)
			if len(c.want) == 0 {
				if err != nil || p.Frame == nil || *p.Frame != (FrameFile{File: "frames/page.html", Bytes: len(c.html)}) {
					t.Fatalf("a frame that meets the contract: %+v, %v", p, err)
				}
				return
			}
			var errs Errors
			if !errors.As(err, &errs) || len(errs) != len(c.want) {
				t.Fatalf("errors = %v, want %d: %v", err, len(c.want), c.want)
			}
			for i, w := range c.want {
				file, line := filepath.Join(dir, "frames", "page.html"), w.line
				if w.line == 0 {
					file, line = filepath.Join(dir, "pack.yaml"), 3
				}
				if e := errs[i]; e.File != file || e.Line != line || e.Key != "frame" || !strings.Contains(e.Msg, w.msg) {
					t.Errorf("error %d = %s\nwant %s:%d frame, containing %q", i, e, file, line, w.msg)
				}
			}
		})
	}
}

// A frame with a fault for every few bytes is named for the first of them and
// counted for the rest, so that a check of it prints a page and no more.
func TestFrameFaultsAreCapped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "frames"), 0o755); err != nil {
		t.Fatal(err)
	}
	frame := frameOf("", titleBlock+"\n<script>"+strings.Repeat("<!--yardr:content-->\n", 100)+"</script>")
	for name, text := range map[string]string{"pack.yaml": "name: framed\napi: 4\nframe: frames/page.html\n", "frames/page.html": frame} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var errs Errors
	if _, err := LoadDir(dir); !errors.As(err, &errs) || len(errs) != maxFrameFaults+1 {
		t.Fatalf("errors = %d (%v), want %d named and one that counts the rest", len(errs), err, maxFrameFaults)
	}
	// 100 markers in the script fault, and so do 99 of them for standing twice.
	if last := errs[0]; last.File != filepath.Join(dir, "pack.yaml") || !strings.Contains(last.Msg, "has 174 more faults than these") {
		t.Errorf("the count = %s", last)
	}
}
