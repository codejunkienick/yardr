package pack

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"html"
	"net/http"
	"slices"
	"strings"

	"yardr.dev/yardr/internal/store"
)

// byteOrderMark is what a browser takes off the front of a page, and what
// the scan of a frame skips (scanFrame).
var byteOrderMark = []byte("\xef\xbb\xbf")

// Dress makes a page of a frame, a fragment and a title: one complete HTML
// document made from the frame's bytes by three substitutions and no others
// (design yardr-7434, section 3.3). The title marker becomes the title as
// HTML text, the content marker becomes the fragment as markup, and what
// the frame put between <title> and </title> becomes the same title as
// element text, so the browser's title and the page's heading cannot
// disagree.
//
// Dress holds the frame to what pack check holds it to (a size, the title
// contract) because the bytes it is given are the installed copy, which may
// have been edited since its pack was checked; and it holds the words to
// what a page's words are: a title of one line, and a fragment that is the
// page's body content and no document of its own, since the frame is the
// document. The three places are found by the scan that checked the frame
// and filled in one pass over it, so a title or a fragment that holds a
// marker's bytes is never substituted again. The same frame bytes, fragment
// and title make the same page, byte for byte.
func Dress(frame ResolvedFrame, fragment []byte, title string) ([]byte, error) {
	if err := store.CheckPageTitle(title); err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(fragment)) == 0 {
		return nil, errors.New("the fragment is empty: it is the page's content, written against the frame's components (yardr page show)")
	}
	if doc := scanFrame(fragment).document; len(doc) > 0 {
		return nil, fmt.Errorf("the fragment is a whole document, with %s in it: give the page's body content alone, since the frame is the document and yardr puts the title in it (yardr page show)", doc[0])
	}
	s, problems := checkedFrame(frame.Bytes)
	if len(problems) > 0 {
		where := ""
		if frame.Path != "" {
			where = " (installed at " + frame.Path + ")"
		}
		return nil, fmt.Errorf("the frame that dresses pages here, %s of pack %s%s, does not meet what a frame is held to; an installed copy may have been edited since its pack was checked, so mend it or apply the pack again:\n  %s",
			frame.File, frame.Pack, where, strings.Join(problems, "\n  "))
	}

	titleAt, contentAt, titleText := -1, -1, -1
	for off := range s.real {
		switch {
		case bytes.HasPrefix(s.src[off:], []byte(titleMarker)):
			titleAt = off
		case bytes.HasPrefix(s.src[off:], []byte(contentMarker)):
			contentAt = off
		}
	}
	var text span
	for _, sp := range s.spans {
		if sp.kind == spanRaw && sp.elem == "title" {
			text, titleText = sp, sp.start
			break
		}
	}
	if titleAt < 0 || contentAt < 0 || titleText < 0 {
		return nil, fmt.Errorf("the frame %s of pack %s met its contract but its title and content places were not found, which is a mistake of yardr, not of the frame", frame.File, frame.Pack)
	}

	type edit struct {
		from, to int
		with     []byte
	}
	escaped := []byte(html.EscapeString(title))
	edits := []edit{
		{titleAt, titleAt + len(titleMarker), escaped},
		{contentAt, contentAt + len(contentMarker), fragment},
		{text.start, text.end, escaped},
	}
	slices.SortFunc(edits, func(a, b edit) int { return cmp.Compare(a.from, b.from) })
	out := make([]byte, 0, len(frame.Bytes)+len(fragment)+2*len(escaped))
	out = append(out, frame.Bytes[:len(frame.Bytes)-len(s.src)]...)
	at := 0
	for _, e := range edits {
		out = append(out, s.src[at:e.from]...)
		out = append(out, e.with...)
		at = e.to
	}
	out = append(out, s.src[at:]...)
	// The yard reads what a blob is from its first bytes, so a page that
	// would not read as HTML is a file that downloads: refused here, with
	// the reason, before anything is put.
	if !strings.HasPrefix(http.DetectContentType(out), "text/html") {
		return nil, fmt.Errorf("a page made in the frame %s of pack %s would not be read as a page, since the yard tells a page from the first bytes it holds: a frame opens with <!DOCTYPE html>, <html> or a comment, and no byte order mark", frame.File, frame.Pack)
	}
	return out, nil
}

// checkedFrame scans text and answers the scan with the ways text breaks
// what a frame is held to, in the words pack check gives them: a size over
// MaxFrame, and each fault of the title contract with its line. A frame over
// the bound is not scanned: it is refused for its size, as a pack's is.
func checkedFrame(text []byte) (*frameScan, []string) {
	if len(text) > MaxFrame {
		return nil, []string{fmt.Sprintf("holds %d bytes: at most %d, since it is copied into every page made under it", len(text), MaxFrame)}
	}
	s := scanFrame(text)
	var out []string
	for i, f := range s.faults {
		if i == maxFrameFaults {
			out = append(out, fmt.Sprintf("%d more faults than these: mend these, and check again", len(s.faults)-i))
			break
		}
		if f.line == 0 {
			out = append(out, f.msg)
			continue
		}
		out = append(out, fmt.Sprintf("line %d: %s", f.line, f.msg))
	}
	return s, out
}

// FrameProblems are the ways a frame's bytes break what a frame is held to,
// each as one line of words (checkedFrame): none for a frame that dresses a
// page. An installed frame is checked again where it is read, since a hand
// edit is the operator's and is used as it stands.
func FrameProblems(text []byte) []string {
	_, problems := checkedFrame(text)
	return problems
}

// Contract is the text of the comment a frame opens with, which is the
// contract it declares for the authors of fragments: its component classes,
// its tokens and its two markers, in the words of whoever wrote the frame.
// It is found as a browser finds a comment, by the scan's own rule for where
// one ends, and has the comment's delimiters and the space around it taken
// off. "" for a frame that opens with none. The text is the pack's, not
// yardr's, and is data wherever it is shown.
func Contract(frame []byte) string {
	b := bytes.TrimLeft(bytes.TrimPrefix(frame, byteOrderMark), " \t\n\f\r")
	if !bytes.HasPrefix(b, []byte("<!--")) {
		return ""
	}
	body := string(b[len("<!--") : len("<!--")+commentLen(b[len("<!--"):])])
	body = strings.TrimSuffix(body, ">")
	if rest, ok := strings.CutSuffix(body, "--!"); ok {
		body = rest
	} else {
		body = strings.TrimSuffix(body, "--")
	}
	return strings.TrimSpace(body)
}
