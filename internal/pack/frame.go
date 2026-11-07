package pack

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"yardr.dev/yardr/internal/config"
)

// MaxFrame bounds a pack's page frame. It is copied into every page made
// under it, so what it weighs, it weighs once for each of them.
const MaxFrame = 256 << 10

// maxFrameFaults is how many faults of a frame a check names: a frame can
// have one for every few bytes, and the rest is said as a count.
const maxFrameFaults = 25

// FrameFile is a pack's page frame (pack.yaml's frame): the one HTML file new
// pages are made in. File is its slash path in the pack and Bytes what it
// holds, which the plan says beside the digest that pins it.
type FrameFile struct {
	File  string `json:"file"`
	Bytes int    `json:"bytes"`
}

// frameFile is the path of the pack's frame, "" for a pack without one.
func (p *Pack) frameFile() string {
	if p.Frame == nil {
		return ""
	}
	return p.Frame.File
}

// isFrame reports whether rel is the pack's frame.
func (p *Pack) isFrame(rel string) bool { return p.Frame != nil && p.Frame.File == rel }

// script reports whether the pack's file rel is a script: what a run: group
// names, which runs as the user who runs the yard, and so is copied
// executable. The frame is not one, though nothing is filled in it either: a
// reader's browser runs its script, and only when the reader chooses.
func (p *Pack) script(rel string) bool {
	return slices.ContainsFunc(p.Groups, func(g Group) bool { return g.Run == rel })
}

// frame reads the pack's frame key, where e is it: the file as rel reads a
// role, then what only this file is held to. It must have something in it,
// hold at most MaxFrame bytes and meet the title contract, which names where
// the page's title and content go (frameFaults). Its path may be no other
// file's of the pack, since the frame is copied as it stands and a role is
// filled in, a script run, so one path read two ways would be one of them
// wrongly. A pack of kind source has its own refusal of the key, and the file
// is not read.
//
// A mistake of the whole file is told at the frame key, where the pack names
// it. One at a place in it is told at that line of the frame, for the author
// who has the frame open.
func (r *reader) frame(p *Pack, e *entry) *FrameFile {
	if e == nil || p.Kind == KindSource {
		return nil
	}
	clean := r.rel(e.v, e.path, "frame")
	if clean == "" {
		return nil
	}
	if uses := p.pathUses(clean); len(uses) > 0 {
		r.errf(e.v, e.path, "frame %s is also %s: a path is one thing in a pack, and the frame is copied as it stands where that would have it filled in or run", clean, strings.Join(uses, " and "))
	}
	text, err := r.reads.file(clean)
	switch {
	case err != nil:
		r.errf(e.v, e.path, "frame %s: %v", clean, err)
		return nil
	case strings.TrimSpace(string(text)) == "":
		r.errf(e.v, e.path, "frame %s is empty: a frame is a whole page, with a place for its title and one for its content", clean)
		return nil
	case len(text) > MaxFrame:
		r.errf(e.v, e.path, "frame %s holds %d bytes: at most %d, since it is copied into every page made under it", clean, len(text), MaxFrame)
		return nil
	}
	faults := frameFaults(text)
	for i, f := range faults {
		if i == maxFrameFaults {
			r.errf(e.v, e.path, "frame %s has %d more faults than these: mend these, and check again", clean, len(faults)-i)
			break
		}
		if f.line == 0 {
			r.errf(e.v, e.path, "frame %s %s", clean, f.msg)
			continue
		}
		r.late = append(r.late, Error{File: joinName(r.dir, clean), Line: f.line, Key: e.path, Msg: f.msg})
	}
	return &FrameFile{File: clean, Bytes: len(text)}
}

// pathUses says what else the pack's file rel is: the role of a group or a
// crew member, the script of a group, a stage file, the setup notes, a
// declaration or a program of one.
func (p *Pack) pathUses(rel string) []string {
	var uses []string
	for _, g := range p.Groups {
		if g.Agent == rel {
			uses = append(uses, "the role of group "+g.Name)
		}
		if g.Run == rel {
			uses = append(uses, "the script of group "+g.Name)
		}
	}
	for _, m := range p.Crew {
		if m.Role == rel {
			uses = append(uses, "the role of crew member "+m.Name)
		}
	}
	for _, f := range p.Flows {
		for _, s := range f.Stages {
			if s.File == rel {
				uses = append(uses, fmt.Sprintf("the stage file of stage %s of flow %s", s.Name, f.Name))
			}
		}
	}
	if p.Setup == rel {
		uses = append(uses, "the setup notes")
	}
	for _, d := range p.Declared {
		if d.File == rel {
			uses = append(uses, "the declaration of source "+d.Name)
		}
		for _, c := range d.Declaration.Commands() {
			if use := "a program of the declaration of source " + d.Name; c.Relative() && path.Join(declDir(d.File), c.Argv[0]) == rel && !slices.Contains(uses, use) {
				uses = append(uses, use)
			}
		}
	}
	return uses
}

// frames lists, for each pack the depot imported that brought a page frame,
// the frame as left in the yard: an export does not write one, because which
// frame a depot uses is not yet recorded for it to write. It would be
// dropped unsaid, which Left is there to prevent. The record of an import
// does not say which of its files is the frame, so it is the installed file
// that holds a marker: one that only quotes a marker costs a line too many,
// and a frame left out would cost the line it needs.
func (e *exporter) frames() error {
	all, err := e.st.Packs(e.ctx)
	if err != nil {
		return err
	}
	for _, rec := range all {
		if rec.Depot != e.depot.Name {
			continue
		}
		dir := config.At(e.o.Home).Pack(rec.Pack)
		for _, po := range rec.Objects {
			rel, err := filepath.Rel(dir, po.Name)
			if po.Kind != "file" || err != nil || !filepath.IsLocal(rel) {
				continue
			}
			if text, err := os.ReadFile(po.Name); err == nil && (bytes.Contains(text, []byte(titleMarker)) || bytes.Contains(text, []byte(contentMarker))) {
				e.left("frame: %s: not in the pack: pack %s brought the page frame %s, which an export does not carry: it cannot yet write the frame a depot uses as its own", rec.Pack, rec.Pack, filepath.ToSlash(rel))
			}
		}
	}
	return nil
}
