package pack

import (
	"bytes"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// The two markers a page frame reserves: where the page's title block stands
// and where its content goes. Each is one whole HTML comment, and a frame has
// each once.
const (
	titleMarker   = "<!--yardr:title-->"
	contentMarker = "<!--yardr:content-->"
)

// markers are the two, as the faults name them, with what a frame that has
// neither is told.
var markers = [...]struct{ bytes, name, lacks string }{
	{titleMarker, "title marker", "has no " + titleMarker + ": a frame has exactly one, where the page's title block stands (inside its <h1>, for one)"},
	{contentMarker, "content marker", "has no " + contentMarker + ": a frame has exactly one, where the page's content stands"},
}

// maxDepth is how deep a frame may nest elements. A browser keeps no more
// than a few hundred open, and reads what is deeper another way, so the scan
// refuses it instead of guessing; it also keeps the scan's work in step with
// the frame's size, since what it looks up in the open elements is short.
const maxDepth = 512

// frameFault is one way a frame breaks the contract that gives its title and
// its content a place. line is where in the frame file it stands, and 0 for
// what is wrong with the file as a whole: a marker or a <title> it lacks.
type frameFault struct {
	line int
	msg  string
}

// frameFaults reads the frame as a browser reads an HTML document, as far as
// the contract needs, and returns every way it breaks it, in file order.
//
// A marker is only a marker where a browser reads its bytes as a comment, so
// the scan follows the tokenizer the HTML standard gives browsers, not a
// search for the text: the same bytes are text inside a script, a style, a
// textarea, a title, an iframe, an xmp, a noembed, a noframes, a noscript and
// a plaintext, a quoted attribute value, a longer comment or an svg's CDATA,
// and a comment that `<!-->` or `--!>` ends early leaves what follows it to
// be read afresh. Every marker's bytes found anywhere are counted, so a
// marker that is not a comment is a fault of its own and not a missing one.
//
// Where a marker stands is judged on the elements the frame has open. They are
// kept by the standard's rules for the tags that close one another (a
// paragraph a block starts in, an item the next item ends, a link in a link)
// and for how far an end tag looks, and no further: what the scan does not
// follow it keeps open, so it holds at least what a browser holds open. It may
// refuse a frame a browser would have mended, and it is not meant to accept
// one that a browser reads otherwise. noscript is read as a browser with
// scripts on reads it, which is the reading a frame's own script runs under.
func frameFaults(text []byte) []frameFault { return scanFrame(text).faults }

// scanFrame runs the scan frameFaults describes and answers it finished: the
// faults it found, and where in the file its markers and its <title> stand,
// which Dress puts a page's words at.
func scanFrame(text []byte) *frameScan {
	// A browser takes a byte order mark off the front, and it is no text.
	s := &frameScan{src: bytes.TrimPrefix(text, byteOrderMark), real: map[int]bool{}}
	for i, c := range s.src {
		if c == '\n' {
			s.nl = append(s.nl, i)
		}
	}
	for s.pos < len(s.src) {
		if s.src[s.pos] == '<' {
			s.markup()
		} else {
			s.text()
		}
	}
	s.finish()
	return s
}

// A span is a stretch of the frame in which a marker's bytes are not a
// marker, by what a browser reads it as.
type span struct {
	start, end int
	kind       spanKind
	elem       string // of spanRaw: the element whose text it is
}

type spanKind int

const (
	spanComment spanKind = iota // a comment that is not a marker
	spanTag                     // a tag, a doctype or a declaration read as a comment
	spanRaw                     // the text of a raw text, RCDATA or script data element
	spanCDATA                   // a CDATA section of svg or math
)

type foreignRoot struct {
	name  string
	depth int
}

type frameScan struct {
	src []byte
	pos int
	nl  []int // where each line of src ends, for line
	// open are the elements of HTML open, outermost first.
	open []string
	// foreign are the svg and math elements open, outermost first, each with
	// how many elements of HTML were open around it. Inside one a browser
	// reads no raw text element and keeps CDATA sections, and no marker may
	// stand there.
	foreign []foreignRoot
	// body says the body has begun, by its tag or by the first thing that
	// belongs in it; closed that its end tag has come.
	body, closed bool
	sawHead      bool // a <head> has been opened, and a second is ignored
	deep         bool // the frame nests more than maxDepth deep, which is said once
	spans        []span
	real         map[int]bool // where the markers that are comments are
	titles       []int        // the line of each <title> element
	faults       []frameFault
	// document is what the scan read that only a whole document has: a
	// doctype, the start tags of html, head and body and the end tags of body
	// and html. Dress refuses a fragment that holds any, since the frame is
	// the document.
	document []string
}

// line is the line of the file the byte at off is on, from 1.
func (s *frameScan) line(off int) int { return sort.SearchInts(s.nl, off) + 1 }

func (s *frameScan) fault(line int, format string, args ...any) {
	s.faults = append(s.faults, frameFault{line, fmt.Sprintf(format, args...)})
}

// text is the characters up to the next tag. Characters other than
// whitespace belong in the body: a browser starts it for them as for a tag.
func (s *frameScan) text() {
	n := bytes.IndexByte(s.src[s.pos:], '<')
	if n < 0 {
		n = len(s.src) - s.pos
	}
	if len(bytes.TrimLeft(s.src[s.pos:s.pos+n], " \t\n\f\r")) > 0 {
		s.content()
	}
	s.pos += n
}

// content is what a character that belongs in the body does: it begins the
// body, and brings it back after its end tag, since a browser reads whatever
// follows </body> as the body's own. What stands in a <template> is its own
// document fragment and begins nothing.
func (s *frameScan) content() {
	if !slices.Contains(s.open, "template") {
		s.begin()
	}
	s.closed = false
}

// markup reads what stands at a '<': a comment, a declaration, a tag, or a
// character that opens none of them and is text.
func (s *frameScan) markup() {
	rest := s.src[s.pos:]
	switch {
	case bytes.HasPrefix(rest, []byte("<!--")):
		s.comment()
	case bytes.HasPrefix(rest, []byte("<!")):
		s.declaration()
	case len(rest) > 2 && rest[1] == '/' && htmlAlpha(rest[2]):
		s.endTag()
	case bytes.HasPrefix(rest, []byte("</>")):
		s.pos += 3
	case bytes.HasPrefix(rest, []byte("</")), bytes.HasPrefix(rest, []byte("<?")):
		s.declaration()
	case len(rest) > 1 && htmlAlpha(rest[1]):
		s.startTag()
	default:
		// A '<' that opens nothing is a character of the page's text.
		s.content()
		s.pos++
	}
}

// comment reads the comment at the '<!--' s is at. It is a marker only when
// the whole comment is one: `<!-- <!--yardr:title-->` is a longer comment
// that happens to end in the bytes.
func (s *frameScan) comment() {
	start := s.pos
	end := start + len("<!--") + commentLen(s.src[start+len("<!--"):])
	s.pos = end
	for which, m := range markers {
		if string(s.src[start:end]) == m.bytes {
			s.real[start] = true
			s.place(which, start)
			return
		}
	}
	s.spans = append(s.spans, span{start: start, end: end, kind: spanComment})
}

// commentLen is how many bytes a comment takes after its '<!--': to the '>'
// that ends it, or the end of the file. It runs the comment states of the
// tokenizer, because they end a comment in more ways than a search for
// '-->' does: `<!-->` and `<!--->` are whole, a nested `<!--` ends at the
// first '>' after it, and `--!>` closes a comment too.
func commentLen(b []byte) int {
	const (
		start = iota
		startDash
		text
		lessThan
		lessThanBang
		lessThanBangDash
		lessThanBangDashDash
		endDash
		end
		endBang
	)
	state := start
	for i := 0; i < len(b); {
		c := b[i]
		switch state {
		case start:
			switch c {
			case '-':
				state = startDash
			case '>':
				return i + 1
			default:
				state = text
				continue
			}
		case startDash:
			switch c {
			case '-':
				state = end
			case '>':
				return i + 1
			default:
				state = text
				continue
			}
		case text:
			switch c {
			case '<':
				state = lessThan
			case '-':
				state = endDash
			}
		case lessThan:
			switch c {
			case '!':
				state = lessThanBang
			case '<':
			default:
				state = text
				continue
			}
		case lessThanBang:
			if c != '-' {
				state = text
				continue
			}
			state = lessThanBangDash
		case lessThanBangDash:
			if c != '-' {
				state = endDash
				continue
			}
			state = lessThanBangDashDash
		case lessThanBangDashDash:
			// A '>' here ends the comment, and anything else is a nested
			// comment the standard reads on in the same way.
			state = end
			continue
		case endDash:
			if c != '-' {
				state = text
				continue
			}
			state = end
		case end:
			switch c {
			case '>':
				return i + 1
			case '!':
				state = endBang
			case '-':
			default:
				state = text
				continue
			}
		case endBang:
			switch c {
			case '-':
				state = endDash
			case '>':
				return i + 1
			default:
				state = text
				continue
			}
		}
		i++
	}
	return len(b)
}

// declaration reads what a browser reads as one comment or as a doctype, up
// to the first '>': <!DOCTYPE ...>, <!x>, <?x>, </ x>, and <![CDATA[ in the
// HTML of a page. In svg and math the same <![CDATA[ opens a section that
// runs to ]]>.
func (s *frameScan) declaration() {
	start, rest := s.pos, s.src[s.pos:]
	if len(rest) >= len("<!doctype") && strings.EqualFold(string(rest[:len("<!doctype")]), "<!doctype") {
		s.document = append(s.document, "a doctype")
	}
	end, kind := len(s.src), spanTag
	if len(s.foreign) > 0 && bytes.HasPrefix(rest, []byte("<![CDATA[")) {
		kind = spanCDATA
		if i := bytes.Index(rest[len("<![CDATA["):], []byte("]]>")); i >= 0 {
			end = start + len("<![CDATA[") + i + len("]]>")
		}
	} else if i := bytes.IndexByte(rest[2:], '>'); i >= 0 {
		end = start + 2 + i + 1
	}
	s.spans = append(s.spans, span{start: start, end: end, kind: kind})
	s.pos = end
}

// startTag reads a start tag, which opens an element unless the file ends in
// it: the tokenizer drops a tag that has no end.
func (s *frameScan) startTag() {
	start := s.pos
	name, nameEnd := tagName(s.src, start+1)
	end, selfClosing, whole := tagEnd(s.src, nameEnd)
	s.spans = append(s.spans, span{start: start, end: end, kind: spanTag})
	s.pos = end
	if whole {
		if name == "html" || name == "head" || name == "body" {
			s.document = append(s.document, "a <"+name+"> tag")
		}
		s.opened(name, selfClosing, start)
	}
}

// closeElement closes the innermost element open that the end tag name
// names, with everything open inside it, where a browser would: an end tag
// looks no further than the elements the standard has it stop at (a cell, a
// table or a template for the end tags of a page's own elements, any special
// element for the rest). What a browser would not close stays open here, which
// is the side the scan errs on.
func (s *frameScan) closeElement(name string) {
	if name == "template" {
		s.closeWhere(func(e string) bool { return e == name }, func(string) bool { return false })
		return
	}
	stops := special
	switch {
	case name == "p", name == "li", isHeading(name), scopedEnd[name]:
		stops = scopeStops
	case tableParts[name]:
		stops = tableStops
	}
	for i := len(s.open) - 1; i >= 0; i-- {
		switch el := s.open[i]; {
		case el == name || isHeading(name) && isHeading(el):
			s.truncate(i)
			return
		case isForeignSentinel(el):
			return
		case stops[el], name == "p" && el == "button", name == "li" && (el == "ol" || el == "ul"):
			return
		}
	}
}

// implied closes what a browser closes of its own when the start tag name
// opens: the paragraph that a block starts in, a link in a link, a button in
// a button, an item in an item. Without it the scan would hold open what is
// closed, and refuse a content marker for a paragraph nobody closed.
func (s *frameScan) implied(name string) {
	switch {
	case closesP[name]:
		s.closeWhere(func(e string) bool { return e == "p" }, func(e string) bool { return scopeStops[e] || e == "button" })
	case name == "a", name == "button":
		s.closeWhere(func(e string) bool { return e == name }, func(e string) bool { return scopeStops[e] })
	}
	switch name {
	case "li", "dd", "dt":
		match := func(e string) bool { return e == "li" }
		if name != "li" {
			match = func(e string) bool { return e == "dd" || e == "dt" }
		}
		s.closeWhere(match, func(e string) bool { return special[e] && e != "address" && e != "div" && e != "p" })
	}
}

// closeWhere closes the innermost element open that match says yes to, and
// what is inside it, unless an element that stop says yes to is in the way.
func (s *frameScan) closeWhere(match, stop func(string) bool) {
	for i := len(s.open) - 1; i >= 0; i-- {
		switch el := s.open[i]; {
		case match(el):
			s.truncate(i)
			return
		case isForeignSentinel(el), stop(el):
			return
		}
	}
}

// truncate closes the open elements from i up. A link that is closed this
// way and not by an end tag of its own stays open: a browser opens it again
// around what comes next, so the content marker that follows an unclosed
// <a> is in it. A cell, a caption and the like end their links with them.
// A drawing inside what closes is closed with it.
func (s *frameScan) truncate(i int) {
	var links []string
	if !scopeStops[s.open[i]] {
		for _, e := range s.open[i+1:] {
			if e == "a" {
				links = append(links, e)
			}
		}
	}
	s.open = append(s.open[:i], links...)
	for n := len(s.foreign); n > 0 && s.foreign[n-1].depth > len(s.open); n-- {
		s.foreign = s.foreign[:n-1]
	}
}

// nameSet makes a set of the names in words.
func nameSet(words string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(words) {
		m[w] = true
	}
	return m
}

func isHeading(name string) bool {
	return len(name) == 2 && name[0] == 'h' && '1' <= name[1] && name[1] <= '6'
}

// full reports whether the elements open are as many as the scan keeps,
// and says so once: the element at at is not kept.
func (s *frameScan) full(at int) bool {
	if len(s.open) < maxDepth && len(s.foreign) < maxDepth {
		return false
	}
	if !s.deep {
		s.deep = true
		s.fault(s.line(at), "the frame nests elements more than %d deep, which is deeper than a browser reads them", maxDepth)
	}
	return true
}

// breakout are the tags that a browser reads as HTML even inside svg or math,
// where they end it: a heading, a paragraph, a table, a list, a div or a span
// is no element of a drawing.
var breakout = nameSet("b big blockquote body br center code dd div dl dt em embed h1 h2 h3 h4 h5 h6 head hr i img li listing menu meta nobr ol p pre ruby s small span strong strike sub sup table tt u ul var")

// voidElements have no content and no end tag.
var voidElements = nameSet("area base basefont bgsound br col embed frame hr img input keygen link meta param source track wbr")

// The sets the standard gives for how far an end tag looks and what a start
// tag closes: the elements no end tag of a page looks past (scopeStops, and
// tableStops for the end tags of a table's parts), the elements an end tag of
// any other name may not close through (special), the names whose end tags
// look past anything else (scopedEnd, with p, li and the headings, and
// tableParts), and the start tags that close a paragraph (closesP).
var (
	scopeStops = nameSet("applet caption html table td th marquee object template")
	tableStops = nameSet("html table template")
	special    = nameSet("address applet area article aside base basefont bgsound blockquote body br button caption center col colgroup dd details dir div dl dt embed fieldset figcaption figure footer form frame frameset h1 h2 h3 h4 h5 h6 head header hgroup hr html iframe img input keygen li link listing main marquee menu meta nav noembed noframes noscript object ol p param plaintext pre script search section select source style summary table tbody td template textarea tfoot th thead title tr track ul wbr xmp")
	scopedEnd  = nameSet("address applet article aside blockquote button center details dialog dir div dl dd dt fieldset figcaption figure footer header hgroup listing main marquee menu nav object ol pre search section summary ul a b big code em font i nobr s small strike strong tt u")
	tableParts = nameSet("table tbody thead tfoot tr td th caption colgroup")
	closesP    = nameSet("address article aside blockquote center details dialog dir div dl fieldset figcaption figure footer form header hgroup hr li dd dt listing main menu ol p plaintext pre search section summary ul xmp h1 h2 h3 h4 h5 h6")
)

// mode begins the body at the first start tag that belongs in it, as a
// browser does when a frame leaves out <body>. What stands in a <template>
// is its own document fragment and moves nothing.
func (s *frameScan) mode(name string) {
	if s.body || slices.Contains(s.open, "template") {
		return
	}
	switch name {
	case "html", "head", "base", "basefont", "bgsound", "link", "meta", "noframes", "noscript", "script", "style", "template", "title":
		return
	}
	s.begin()
}

// begin starts the body, which ends the head it may have left open.
func (s *frameScan) begin() {
	if s.body {
		return
	}
	s.body = true
	if i := slices.Index(s.open, "head"); i >= 0 {
		s.open = s.open[:i]
	}
}

type rawKind int

const (
	rawtext    rawKind = iota // style, xmp, iframe, noembed, noframes and noscript
	rcdata                    // title and textarea
	scriptData                // script, whose text has escapes of its own
)

// rawText reads the text of the element just opened, up to its end tag,
// which is the only markup in it, and then the end tag.
func (s *frameScan) rawText(name string, kind rawKind) {
	from := s.pos
	var at int
	if kind == scriptData {
		at = scriptEnd(s.src, from)
	} else {
		at = rawEnd(s.src, from, name)
	}
	if at < 0 {
		s.spans = append(s.spans, span{start: from, end: len(s.src), kind: spanRaw, elem: name})
		s.pos = len(s.src)
		return
	}
	s.spans = append(s.spans, span{start: from, end: at, kind: spanRaw, elem: name})
	_, nameEnd := tagName(s.src, at+2)
	end, _, _ := tagEnd(s.src, nameEnd)
	s.spans = append(s.spans, span{start: at, end: end, kind: spanTag})
	s.pos = end
}

// rawEnd is where the end tag of the raw text or RCDATA element name starts:
// the first </name, in any case, that a space, a '/' or a '>' follows. A
// longer name or the end of the file after it is text. -1 when there is none.
func rawEnd(b []byte, from int, name string) int {
	for i := from; i+2 < len(b); i++ {
		if b[i] == '<' && b[i+1] == '/' && endsTag(b, i+2, name) {
			return i
		}
	}
	return -1
}

// endsTag reports whether name, in any case, stands at b[i:] and is followed
// by what ends a tag's name.
func endsTag(b []byte, i int, name string) bool {
	j := i + len(name)
	return j < len(b) && bytes.EqualFold(b[i:j], []byte(name)) && (htmlSpace(b[j]) || b[j] == '/' || b[j] == '>')
}

// scriptEnd is where the end tag that closes a script starts, by the script
// data states of the tokenizer. A `<!--` in a script makes it escaped, and a
// `<script` inside that double escaped, where a </script> only leaves the
// second level and the script goes on; a `-->` returns to the first state.
// Without them a frame whose script quotes such text would be read as ending
// where its browser reads it on. -1 when the file ends first.
func scriptEnd(b []byte, from int) int {
	const (
		data = iota
		escaped
		escapedDash
		escapedDashDash
		doubleEscaped
		doubleDash
		doubleDashDash
	)
	state := data
	for i := from; i < len(b); i++ {
		c := b[i]
		switch state {
		case data:
			switch {
			case c != '<':
			case i+1 < len(b) && b[i+1] == '/' && endsTag(b, i+2, "script"):
				return i
			case bytes.HasPrefix(b[i:], []byte("<!--")):
				state = escapedDashDash
				i += len("<!--") - 1
			}
		case escaped, escapedDash, escapedDashDash:
			switch {
			case c == '-':
				state = min(state+1, escapedDashDash)
			case c == '>' && state == escapedDashDash:
				state = data
			case c != '<':
				state = escaped
			case i+1 < len(b) && b[i+1] == '/' && endsTag(b, i+2, "script"):
				return i
			case endsTag(b, i+1, "script"):
				state = doubleEscaped
				i += len("<script")
			default:
				state = escaped
			}
		default:
			switch {
			case c == '-':
				state = min(state+1, doubleDashDash)
			case c == '>' && state == doubleDashDash:
				state = data
			case c != '<':
				state = doubleEscaped
			case i+1 < len(b) && b[i+1] == '/' && endsTag(b, i+2, "script"):
				state = escaped
				i += len("</script")
			default:
				state = doubleEscaped
			}
		}
	}
	return -1
}

// tagName is the lower-cased name of the tag whose name starts at b[i], and
// where it ends: at whitespace, a '/' or a '>'.
func tagName(b []byte, i int) (string, int) {
	j := i
	for j < len(b) && !htmlSpace(b[j]) && b[j] != '/' && b[j] != '>' {
		j++
	}
	return strings.ToLower(string(b[i:j])), j
}

// tagEnd reads a tag's attributes from i, just after its name, and returns
// where the tag ends, after its '>'. whole is false when the file ends in it.
// The attribute states matter for one reason: a '>' or a marker inside a
// quoted value belongs to the tag.
func tagEnd(b []byte, i int) (end int, selfClosing, whole bool) {
	for i < len(b) {
		switch c := b[i]; {
		case htmlSpace(c):
			i++
		case c == '>':
			return i + 1, false, true
		case c == '/' && i+1 < len(b) && b[i+1] == '>':
			return i + 2, true, true
		case c == '/':
			i++
		default:
			i = attributeEnd(b, i)
		}
	}
	return len(b), false, false
}

// attributeEnd is where the attribute whose name starts at b[i] ends. Its
// first character belongs to the name even if it is an '='; a value is
// quoted with '"' or "'", where only the same quote ends it, or unquoted,
// where whitespace or '>' does and a '/' is part of it.
func attributeEnd(b []byte, i int) int {
	i++
	for i < len(b) && !htmlSpace(b[i]) && b[i] != '/' && b[i] != '>' && b[i] != '=' {
		i++
	}
	j := i
	for j < len(b) && htmlSpace(b[j]) {
		j++
	}
	if j >= len(b) || b[j] != '=' {
		return i
	}
	for j++; j < len(b) && htmlSpace(b[j]); j++ {
	}
	if j >= len(b) {
		return len(b)
	}
	switch b[j] {
	case '"', '\'':
		k := bytes.IndexByte(b[j+1:], b[j])
		if k < 0 {
			return len(b)
		}
		return j + 1 + k + 1
	case '>':
		return j
	}
	for j < len(b) && !htmlSpace(b[j]) && b[j] != '>' {
		j++
	}
	return j
}

func htmlSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r' }

func htmlAlpha(c byte) bool { return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' }

// place judges where a marker that is a comment stands: the title marker
// must be in the body where text stands, and the content marker where flow
// content does, because it is replaced by text and by markup with headings,
// tiles and tables in it.
func (s *frameScan) place(which, off int) {
	line := s.line(off)
	bad := func(format string, args ...any) {
		s.fault(line, "the %s stands %s", markers[which].name, fmt.Sprintf(format, args...))
	}
	switch {
	case len(s.foreign) > 0:
		bad("inside an <svg> or <math> element, which a browser reads as drawing or formulas")
		return
	case slices.Contains(s.open, "template"):
		bad("inside a <template> element, whose content a page never shows")
		return
	case !s.body:
		bad("in the head, before the body begins: it belongs in the body")
		return
	case s.closed:
		bad("after the end of the body: it belongs inside the body")
		return
	}
	parent := ""
	if n := len(s.open); n > 0 {
		parent = s.open[n-1]
	}
	if which == 0 {
		if textless[parent] {
			bad("directly in a <%s> element, where a browser puts text out in front of it and not in it: put it in a heading or a <div>", parent)
		}
		return
	}
	// A paragraph, a link or a button is closed by the markup that the
	// fragment is, whatever else stands between.
	for i := len(s.open) - 1; i >= 0; i-- {
		if e := s.open[i]; e == "p" || e == "a" || e == "button" {
			bad("inside a <%s> element, which cannot hold the headings, tiles and tables the content is made of: put it in the body, or in a <main>, <section> or <div>", e)
			return
		}
	}
	if noFlow[parent] {
		bad("directly in a <%s> element, which cannot hold the headings, tiles and tables the content is made of: put it in the body, or in a <main>, <section> or <div>", parent)
	}
}

// textless are the elements a browser does not keep text in: it moves it out
// in front of a table, and drops it from a select.
var textless = nameSet("table thead tbody tfoot tr colgroup select optgroup")

// noFlow are the elements the content marker may not be the child of: a
// heading, a list or one of its items, a table and its parts, a select.
var noFlow = nameSet("h1 h2 h3 h4 h5 h6 ul ol li dl dt dd table thead tbody tfoot tr td th caption colgroup select optgroup option")

// finish counts the markers' bytes wherever they stand, judges the ones that
// are not comments by what a browser reads them as, and says what a frame
// lacks. Faults come out in file order, those of the whole file first.
func (s *frameScan) finish() []frameFault {
	for which, m := range markers {
		var at []int
		for from := 0; ; {
			i := bytes.Index(s.src[from:], []byte(m.bytes))
			if i < 0 {
				break
			}
			at = append(at, from+i)
			from += i + len(m.bytes)
		}
		if len(at) == 0 {
			s.fault(0, "%s", m.lacks)
		}
		for k, off := range at {
			if !s.real[off] {
				s.misplaced(which, off)
			}
			if k > 0 {
				s.fault(s.line(off), "the %s %s stands twice (the first is on line %d): a frame has exactly one", m.name, m.bytes, s.line(at[0]))
			}
		}
	}
	if len(s.titles) == 0 {
		s.fault(0, "has no <title> element: a frame has exactly one, in its head, and the page's title is put in it")
	}
	sort.SliceStable(s.faults, func(i, j int) bool { return s.faults[i].line < s.faults[j].line })
	return s.faults
}

// misplaced says why the marker's bytes at off, which are no comment, are
// none to a browser.
func (s *frameScan) misplaced(which, off int) {
	name := markers[which].name
	i := sort.Search(len(s.spans), func(i int) bool { return s.spans[i].end > off })
	if i == len(s.spans) || s.spans[i].start > off {
		s.fault(s.line(off), "the %s stands where a browser does not read it as a comment", name)
		return
	}
	sp := s.spans[i]
	switch {
	case sp.kind == spanRaw && sp.elem == "title" && which == 0:
		s.fault(s.line(off), "the %s stands inside the <title> element: the page's title block and its <title> are two places, and the title is put in each", name)
	case sp.kind == spanRaw:
		s.fault(s.line(off), "the %s stands in the text of a <%s> element, which a browser reads as text and not as a comment", name, sp.elem)
	case sp.kind == spanCDATA:
		s.fault(s.line(off), "the %s stands in a CDATA section, which a browser reads as text and not as a comment", name)
	case sp.kind == spanComment:
		s.fault(s.line(off), "the %s is part of a longer comment: the marker is the comment %s and nothing else", name, markers[which].bytes)
	default:
		s.fault(s.line(off), "the %s stands inside a tag or a declaration, which a browser does not read as a comment", name)
	}
}
