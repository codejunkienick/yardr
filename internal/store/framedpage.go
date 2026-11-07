package store

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// FramedPage is what the yard keeps of a page made through a frame
// (docs/design/2026-11-02-yardr-7434-page-frame.md, section 3.7), on the
// dressed page's own BlobRef beside the blob every surface already serves:
// the fragment it was dressed from, the title it was given and which frame
// dressed it. The fragment is a blob of its own that no letter names as a
// file, so no surface offers it and only this record keeps it from the
// sweep (nameKept). A page made whole, with --file, has no record and keeps
// nothing apart from its blob: it is never dressed again.
//
// The record is stored as it is written. A rebuild only appends to
// Versions, so what a page made today keeps is all a later rebuild needs.
type FramedPage struct {
	// Fragment is the SHA-256 of the fragment's bytes, a blob the yard holds.
	Fragment string `json:"fragment"`
	// Title is the title as the author gave it, which the frame put in the
	// page's <title> and in its title block.
	Title string `json:"title"`
	// Pack, Frame and FrameSHA256 name the frame the page was made with:
	// the pack it came from, the frame's slash path in that pack, and the
	// SHA-256 of the frame bytes actually read when the page was made. An
	// installed frame edited by hand since its import is used as it stands,
	// so this sum can differ from the one the import recorded; the page
	// names what it was made from.
	Pack        string `json:"pack"`
	Frame       string `json:"frame"`
	FrameSHA256 string `json:"frame_sha256"`
	// Versions are the dressed pages this one superseded, each with the
	// full identity of the frame that made it. Only a rebuild writes them.
	Versions []PageVersion `json:"versions,omitempty"`
}

// PageVersion is a superseded version of a framed page: the dressed page's
// hash and the identity of the frame that made it, the same three parts
// the standing record carries, so that a version made before a pack moved
// its frame path still names the frame that made it.
type PageVersion struct {
	Hash        string `json:"hash"`
	Pack        string `json:"pack"`
	Frame       string `json:"frame"`
	FrameSHA256 string `json:"frame_sha256"`
}

// MaxPageTitle is how many bytes a framed page's title may have. It is
// kept on the note's record in the store, so it is bounded.
const MaxPageTitle = 200

// CheckPageTitle holds a title to what a framed page may be given: one line
// of plain text, not empty, at most MaxPageTitle bytes of UTF-8 with no
// control character (the line break among them), bidi control or line or
// paragraph separator, as a file's name is held. The frame puts it in the
// page twice, escaped, and the record keeps it as given.
func CheckPageTitle(title string) error {
	switch {
	case strings.TrimSpace(title) == "":
		return errors.New("the title is empty: it stands in the page's title block and in its <title>")
	case len(title) > MaxPageTitle:
		return fmt.Errorf("the title is at most %d bytes, not %d", MaxPageTitle, len(title))
	case !utf8.ValidString(title):
		return fmt.Errorf("the title is UTF-8: %q", title)
	}
	for _, r := range title {
		if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) || r == ' ' || r == ' ' {
			return fmt.Errorf("the title is one line of plain text, with no line break or control character: %q", title)
		}
	}
	return nil
}

// nameKept marks in named the blobs a file of a letter holds: its own, and
// for a framed page the fragment it was dressed from and every dressed page
// a rebuild superseded. What the sweep (pruneBlobs) does not name it
// removes once it is past BlobGrace, so a fragment kept only by a record
// that went unread would be gone within the hour.
func nameKept(named map[string]bool, f BlobRef) {
	named[f.Hash] = true
	if f.Framed == nil {
		return
	}
	named[f.Framed.Fragment] = true
	for _, v := range f.Framed.Versions {
		named[v.Hash] = true
	}
}

// framedRecord is the record a note keeps for the page ref, as its caller
// wrote it: refused where a note cannot carry it, never mended. A record
// belongs to a page, names a fragment the yard holds (a note never names a
// file nobody can read), and a frame by its pack, path and sum. A new page
// has no superseded version: only a rebuild makes one, and a caller that
// wrote one would be naming blobs for the sweep to keep.
func (s *Store) framedRecord(ref *BlobRef, rec *FramedPage) (*FramedPage, error) {
	switch {
	case !ref.IsPage():
		return nil, fmt.Errorf("a framed page is a page: %s is a %s", ref.Hash, ref.Kind())
	case len(rec.Versions) > 0:
		return nil, fmt.Errorf("a new framed page has no superseded version: %s names %d", ref.Hash, len(rec.Versions))
	case !blobHash.MatchString(rec.Fragment):
		return nil, fmt.Errorf("a framed page's fragment is a blob hash, not %q", rec.Fragment)
	case !blobHash.MatchString(rec.FrameSHA256):
		return nil, fmt.Errorf("a framed page's frame sum is a SHA-256, not %q", rec.FrameSHA256)
	}
	if err := CheckPageTitle(rec.Title); err != nil {
		return nil, err
	}
	for _, n := range []struct{ what, v string }{{"pack", rec.Pack}, {"frame path", rec.Frame}} {
		if n.v == "" || len(n.v) > MaxBlobName || !utf8.ValidString(n.v) || strings.ContainsFunc(n.v, unicode.IsControl) {
			return nil, fmt.Errorf("a framed page's %s is a name of at most %d bytes with no control character, not %q", n.what, MaxBlobName, n.v)
		}
	}
	if _, err := s.Blob(rec.Fragment); errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("%w: the yard does not hold the fragment; put it first (PutBlob)", err)
	} else if err != nil {
		return nil, err
	}
	out := *rec
	return &out, nil
}
