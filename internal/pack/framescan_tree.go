package pack

import (
	"html"
	"slices"
	"strings"
)

const foreignSentinelPrefix = "\x00yardr:foreign:"

func foreignSentinel(name string) string { return foreignSentinelPrefix + name }

func foreignSentinelName(name string) (string, bool) {
	if !strings.HasPrefix(name, foreignSentinelPrefix) {
		return "", false
	}
	return strings.TrimPrefix(name, foreignSentinelPrefix), true
}

func isForeignSentinel(name string) bool {
	return strings.HasPrefix(name, foreignSentinelPrefix)
}

// integration finds the innermost SVG or MathML integration point still open
// in the foreign element stack, and the root it stands inside.
func (s *frameScan) integration() (point, root int, ok bool) {
	for point = len(s.open) - 1; point >= 0; point-- {
		if !isForeignSentinel(s.open[point]) {
			continue
		}
		for root = len(s.foreign) - 1; root >= 0; root-- {
			if s.foreign[root].depth <= point {
				return point, root, true
			}
		}
	}
	return 0, 0, false
}

// htmlContext reports whether a start tag at the current position is handled
// by the HTML rules inside a foreign integration point. A nested svg or math
// element becomes current again until its own start tag is closed.
func (s *frameScan) htmlContext() bool {
	if _, _, ok := s.integration(); !ok || len(s.foreign) == 0 || len(s.open) == 0 {
		return false
	}
	if isForeignSentinel(s.open[len(s.open)-1]) {
		return len(s.open)-1 >= s.foreign[len(s.foreign)-1].depth
	}
	return len(s.open) > s.foreign[len(s.foreign)-1].depth
}

// htmlChild reports whether a foreign integration point has an HTML element
// above it. End tags in that subtree use HTML rules and cannot close the SVG
// or MathML ancestor just because its name appears in the token.
func (s *frameScan) htmlChild() bool {
	return s.htmlContext() && !isForeignSentinel(s.open[len(s.open)-1])
}

// leaveForeignToHTML pops nested drawings but keeps the SVG or MathML root
// that owns the integration point. A breakout tag is then read by the HTML
// rules inside that still-open foreign element.
func (s *frameScan) leaveForeignToHTML() {
	if _, root, ok := s.integration(); ok {
		s.foreign = s.foreign[:root+1]
		return
	}
	s.clearForeign()
}

// clearForeign leaves all open SVG or MathML roots, trimming the HTML stack
// back to the point where the outermost one began.
func (s *frameScan) clearForeign() {
	if len(s.foreign) == 0 {
		return
	}
	depth := s.foreign[0].depth
	if len(s.open) > depth {
		s.open = s.open[:depth]
	}
	s.foreign = nil
}

// integrationPoint says whether this foreign element switches HTML start
// tags into the body rules. Treating every annotation-xml without an HTML
// encoding as an integration point would only refuse some valid frames, but
// reading its attribute here keeps the scan aligned with a browser.
func (s *frameScan) integrationPoint(name string, at int) bool {
	if len(s.foreign) == 0 {
		return false
	}
	switch s.foreign[len(s.foreign)-1].name {
	case "svg":
		return name == "foreignobject" || name == "desc" || name == "title"
	case "math":
		switch name {
		case "mi", "mo", "mn", "ms", "mtext":
			return true
		case "annotation-xml":
			value, ok := startAttribute(s.src[at:s.pos], "encoding")
			return ok && (strings.EqualFold(html.UnescapeString(value), "text/html") || strings.EqualFold(html.UnescapeString(value), "application/xhtml+xml"))
		}
	}
	return false
}

// startAttribute returns the first value for name from a start tag. Duplicate
// attributes are ignored by the HTML tokenizer after the first one.
func startAttribute(tag []byte, name string) (string, bool) {
	i := 1
	for i < len(tag) && !htmlSpace(tag[i]) && tag[i] != '/' && tag[i] != '>' {
		i++
	}
	for i < len(tag) {
		for i < len(tag) && htmlSpace(tag[i]) {
			i++
		}
		if i >= len(tag) || tag[i] == '>' || tag[i] == '/' && i+1 < len(tag) && tag[i+1] == '>' {
			return "", false
		}
		start := i
		for i < len(tag) && !htmlSpace(tag[i]) && tag[i] != '/' && tag[i] != '>' && tag[i] != '=' {
			i++
		}
		if start == i {
			i++
			continue
		}
		attr := strings.ToLower(string(tag[start:i]))
		for i < len(tag) && htmlSpace(tag[i]) {
			i++
		}
		value := ""
		if i < len(tag) && tag[i] == '=' {
			i++
			for i < len(tag) && htmlSpace(tag[i]) {
				i++
			}
			if i < len(tag) && (tag[i] == '\'' || tag[i] == '"') {
				quote := tag[i]
				i++
				start = i
				for i < len(tag) && tag[i] != quote {
					i++
				}
				value = string(tag[start:i])
				if i < len(tag) {
					i++
				}
			} else {
				start = i
				for i < len(tag) && !htmlSpace(tag[i]) && tag[i] != '>' {
					i++
				}
				value = string(tag[start:i])
			}
		}
		if attr == name {
			return value, true
		}
	}
	return "", false
}

// endTag reads an end tag and closes the element it names, with everything
// open inside it. One that closes nothing is ignored, as a browser ignores it.
func (s *frameScan) endTag() {
	start := s.pos
	name, nameEnd := tagName(s.src, start+2)
	end, _, whole := tagEnd(s.src, nameEnd)
	s.spans = append(s.spans, span{start: start, end: end, kind: spanTag})
	s.pos = end
	if !whole {
		return
	}
	if len(s.foreign) > 0 {
		if s.htmlChild() {
			s.closeElement(name)
			return
		}
		if name == "svg" || name == "math" {
			// A foreign end tag closes the innermost root of its name and
			// everything nested inside it, but leaves an outer one open.
			for i := len(s.foreign) - 1; i >= 0; i-- {
				if s.foreign[i].name == name {
					depth := s.foreign[i].depth
					s.foreign = s.foreign[:i]
					if len(s.open) > depth {
						s.open = s.open[:depth]
					}
					return
				}
			}
			return
		}
		if len(s.open) > 0 && len(s.foreign) > 0 {
			if point, ok := foreignSentinelName(s.open[len(s.open)-1]); ok && point == name && len(s.open)-1 >= s.foreign[len(s.foreign)-1].depth {
				s.open = s.open[:len(s.open)-1]
				return
			}
		}
		if point, root, ok := s.integration(); ok {
			if p, is := foreignSentinelName(s.open[point]); is && p == name {
				// A foreign end tag can close an integration point through a
				// nested drawing, but not through an open HTML child.
				s.foreign = s.foreign[:root+1]
				s.open = s.open[:point]
				return
			}
		}
		if name == "br" || name == "p" {
			s.leaveForeignToHTML()
			s.closeElement(name)
			return
		}
		if !slices.Contains(s.open, name) {
			return
		}
	}
	if name == "body" || name == "html" {
		// Nothing is closed by it, as in a browser, which reads on in the
		// body at the next tag. A table, a cell, a template or an integration
		// point still open is no place for the end of the body.
		if !slices.ContainsFunc(s.open, func(e string) bool { return isForeignSentinel(e) || scopeStops[e] && e != "html" }) {
			s.begin()
			s.closed = true
		}
		return
	}
	s.closeElement(name)
}

// opened is what a start tag does to the document the scan keeps: it may
// begin the body, it may open an element, and for a raw text element it reads
// the text that follows, which is not markup.
func (s *frameScan) opened(name string, selfClosing bool, at int) {
	if len(s.foreign) > 0 && !s.htmlContext() {
		if !breakout[name] {
			// A foreign integration point keeps the drawing as an ancestor but
			// lets its children use HTML rules. Other foreign elements stay out
			// of the HTML stack, except for a nested drawing or integration point.
			if (name == "svg" || name == "math") && !selfClosing && !s.full(at) {
				s.foreign = append(s.foreign, foreignRoot{name, len(s.open)})
				return
			}
			if s.integrationPoint(name, at) && !selfClosing && !s.full(at) {
				s.open = append(s.open, foreignSentinel(name))
			}
			return
		}
		// A breakout leaves nested drawings, stopping at the integration
		// point that owns any HTML subtree still open around it.
		s.leaveForeignToHTML()
	}
	switch name {
	case "html":
		// A second <html> adds nothing to the one a browser has.
		if s.body || slices.Contains(s.open, "html") {
			return
		}
	case "head":
		if s.body || s.sawHead {
			return
		}
		s.sawHead = true
	case "body":
		if s.body {
			return
		}
	case "caption", "col", "colgroup", "tbody", "td", "tfoot", "th", "thead", "tr":
		// Outside a table a browser ignores these start tags, after they
		// have ended the head, as any tag that is not the head's does.
		if !slices.Contains(s.open, "table") && !slices.Contains(s.open, "template") {
			s.mode(name)
			s.closed = false
			return
		}
	}
	s.mode(name)
	s.closed = false
	s.implied(name)
	// A self-closing slash on an HTML element is ignored: <script/> opens a
	// script all the same.
	switch name {
	case "svg", "math":
		if !selfClosing && !s.full(at) {
			s.foreign = append(s.foreign, foreignRoot{name, len(s.open)})
		}
	case "title":
		line := s.line(at)
		switch {
		case slices.Contains(s.open, "template"):
			// Its own fragment's, which is no title of the page, so the page
			// still lacks one.
			s.fault(line, "the <title> element stands inside a <template> element, which is no title of the page")
		case len(s.titles) > 0:
			s.fault(line, "a second <title> element (the first is on line %d): a frame has exactly one, in its head", s.titles[0])
			s.titles = append(s.titles, line)
		default:
			if s.body {
				s.fault(line, "the <title> element stands in the body: a frame has it in its head")
			}
			s.titles = append(s.titles, line)
		}
		s.rawText(name, rcdata)
	case "textarea":
		s.rawText(name, rcdata)
	case "style", "xmp", "iframe", "noembed", "noframes", "noscript":
		s.rawText(name, rawtext)
	case "script":
		s.rawText(name, scriptData)
	case "plaintext":
		s.spans = append(s.spans, span{start: s.pos, end: len(s.src), kind: spanRaw, elem: name})
		s.pos = len(s.src)
	default:
		if !voidElements[name] && !s.full(at) {
			s.open = append(s.open, name)
		}
	}
}
