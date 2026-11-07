package pack

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"yardr.dev/yardr/internal/config"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/packs"
)

// ResolvedFrame is the frame that dresses a new page in one scope: the
// pack it came from, where its bytes stand, and the SHA-256 of the bytes
// read. Bytes are those bytes, so the sum is always of what a page would
// be made from, never of what an import once installed.
type ResolvedFrame struct {
	// Pack is the name of the pack the frame came from: the pack whose
	// import is its scope's active one, or pageframe, the embedded
	// default.
	Pack string `json:"pack"`
	// Path is the installed frame's absolute path under the yard's home.
	// It is empty for the embedded default, whose bytes are read from the
	// binary (packs.FS) and stand in no installed file.
	Path string `json:"path,omitempty"`
	// SHA256 is the sum of Bytes as read. An installed frame edited by
	// hand since its import is used as it stands, so this sum can differ
	// from the one the import recorded on its file object (the checked
	// and imported bytes): the two name different bytes, and each test
	// holds them apart.
	SHA256 string `json:"sha256"`
	// Bytes are the frame's bytes as read, the ones SHA256 sums and the
	// ones a page is dressed from.
	Bytes []byte `json:"-"`
}

// FrameFor is frameFor for a depot ("" for a bead with no depot, and for
// a mail page, which likewise takes the yard's frame): the depot's active
// frame, then the yard's, then the embedded default of the pageframe
// pack. It reads the store's records and the one installed file they
// name, never a pack's source and never a URL: a pack read from a URL was
// installed by its apply from the bytes its plan hashed, so the installed
// copy is the only copy wrapping ever reads.
func FrameFor(ctx context.Context, st store.API, home, depot string) (ResolvedFrame, error) {
	return frameFor(ctx, st, home, depot)
}

// frameFor resolves the frame for depot as FrameFor says. The read that
// sections 3.3 and 3.7 of the page-frame design use lives here, beside
// planner, so the store is never asked to read a pack file.
func frameFor(ctx context.Context, st store.API, home, depot string) (ResolvedFrame, error) {
	recs, err := st.Packs(ctx)
	if err != nil {
		return ResolvedFrame{}, err
	}
	if depot != "" {
		if rec, ok := activeFrame(recs, depot); ok {
			return readInstalled(home, rec)
		}
	}
	if rec, ok := activeFrame(recs, ""); ok {
		return readInstalled(home, rec)
	}
	// The embedded default: read from the binary as it stands, so every
	// page can always be made, imported or not.
	sub, err := fs.Sub(packs.FS, packs.PageFrame)
	if err != nil {
		return ResolvedFrame{}, err
	}
	p, err := Load(sub, packs.Embedded)
	if err != nil {
		return ResolvedFrame{}, fmt.Errorf("the embedded default frame does not read as a pack, which is a mistake of yardr, not of the yard: %w", err)
	}
	if p.Frame == nil {
		return ResolvedFrame{}, fmt.Errorf("the embedded pack %s carries no frame, which is a mistake of yardr, not of the yard", packs.PageFrame)
	}
	text, err := fs.ReadFile(sub, p.Frame.File)
	if err != nil {
		return ResolvedFrame{}, err
	}
	return ResolvedFrame{Pack: p.Name, SHA256: sum(text), Bytes: text}, nil
}

// activeFrame is the record of scope (a depot, or "" for the yard) whose
// frame is the chosen one, by the written FrameActive flag. At most one
// stands per scope, because RecordPack clears the rest; a record with no
// frame path is never the choice, whatever an old record says.
func activeFrame(recs []store.PackImport, scope string) (store.PackImport, bool) {
	for _, r := range recs {
		if r.Depot == scope && r.FrameActive && r.Frame != "" {
			return r, true
		}
	}
	return store.PackImport{}, false
}

// readInstalled reads the installed frame rec names, as it stands: a hand
// edit is the operator's, so the bytes and their sum are of the file as
// found, not of the bytes the import recorded. The record is written over
// the socket by any Admin caller, a crew member's pane among them, and the
// bytes go into pages others read, so the path is read only inside the
// pack's own installed directory, through a root that refuses a .. or a
// symlink leading out of it: a record naming another file (a key, the
// yard's config) is refused, never read.
func readInstalled(home string, rec store.PackImport) (ResolvedFrame, error) {
	dir := config.At(home).Pack(rec.Pack)
	rel, err := filepath.Rel(dir, rec.Frame)
	if err != nil || filepath.Base(rec.Pack) != rec.Pack || !filepath.IsLocal(rec.Pack) || !filepath.IsLocal(rel) {
		return ResolvedFrame{}, fmt.Errorf("the frame of pack %s, %s: not a file of the pack's installed directory %s", rec.Pack, rec.Frame, dir)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return ResolvedFrame{}, fmt.Errorf("the frame of pack %s, %s: %w", rec.Pack, rec.Frame, err)
	}
	defer root.Close()
	text, err := root.ReadFile(rel)
	if err != nil {
		return ResolvedFrame{}, fmt.Errorf("the frame of pack %s, %s: %w", rec.Pack, rec.Frame, err)
	}
	return ResolvedFrame{Pack: rec.Pack, Path: rec.Frame, SHA256: sum(text), Bytes: text}, nil
}

// FrameOnly reports whether the pack is a page frame and nothing beside
// it: the frame, and with name and api at most the setup notes, with no
// kind, flows, groups, crew, sources or presets. Such a pack is YardWide
// without being of kind source, so it alone beside a source pack may be
// applied with no --depot, as the yard's frame. A frame beside anything
// more is applied per depot as any pack is, or its frame is split into a
// frame-only pack for the yard.
func (p *Pack) FrameOnly() bool {
	return p.Frame != nil && p.Kind == "" && len(p.Flows) == 0 && len(p.Groups) == 0 && len(p.Crew) == 0 &&
		len(p.Sources) == 0 && len(p.Declared) == 0 && len(p.Presets) == 0
}

// frameObject plans the pack's frame as the one file of a yard-wide plan
// (buildWithoutDepot), copied and hashed exactly as planner.files copies
// a depot pack's frame: planner.file gives it mode 0644, since Pack.script
// spares the frame, and nothing is filled in it.
func (b *planner) frameObject() error {
	if b.p.Frame == nil {
		return nil
	}
	op := &FileOp{To: b.installed(b.p.Frame.File), Fill: b.p.Filled(b.p.Frame.File)}
	return b.file(b.p.Frame.File, op)
}

// frameUse settles the plan's frame lines from the scope's active record
// before anything is written, never from the newest import's At: the line
// that says whose frame the scope would use, and, where the plan changes
// the frame's bytes, the other scopes the one installed copy serves.
func (b *planner) frameUse() {
	if b.p.Frame == nil {
		return
	}
	scope := b.o.Depot
	was := packs.PageFrame
	if rec, ok := activeFrame(b.all, scope); ok {
		if rec.Pack == b.p.Name {
			was = "" // already the scope's choice: no move to say
		} else {
			was = rec.Pack
		}
	}
	if was != "" {
		b.plan.FrameUse = fmt.Sprintf("frame: %s would use %s's frame (was %s's)", inDepot(scope), b.p.Name, was)
	}
	// One installed copy serves every scope whose active record names
	// this path, so changed or restored bytes reach them together; the
	// plan names them before anything is written. A pack can change its
	// frame path between imports, leaving its scopes on different files.
	installed := b.plan.installedFrame()
	for _, o := range b.plan.Objects {
		if o.Kind != "file" || o.Name != b.p.Frame.File || (o.Action != Create && o.Action != Change) {
			continue
		}
		for _, r := range b.all {
			if r.Depot != scope && r.FrameActive && r.Frame == installed && !slices.Contains(b.plan.FrameShared, inDepot(r.Depot)) {
				b.plan.FrameShared = append(b.plan.FrameShared, inDepot(r.Depot))
			}
		}
	}
	slices.Sort(b.plan.FrameShared)
}

// installedFrame is the absolute path an import of this plan would leave
// the pack's frame at, under the home the plan was made for.
func (p *Plan) installedFrame() string {
	if p.Frame == nil {
		return ""
	}
	return filepath.Join(config.At(p.Home).Pack(p.Pack), filepath.FromSlash(p.Frame.File))
}
