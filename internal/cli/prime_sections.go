package cli

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"unicode"

	"yardr.dev/yardr/internal/pack"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/packs"
)

// primeSectionBudget is the most section bytes a crew prime prints
// whole, over every applied pack together: past it a section is its
// first line and the command that reads the rest, as a prime's memories
// are their titles and yardr recall. With a section of at most
// maxPrimeSection bytes, four always stand whole.
const primeSectionBudget = 8192

// A pack's yardmaster section is held to what its import was held to
// (plan_crew.go's maxSection and maxSectionFirstLine), again where
// prime reads it: the record of the import is only a path.
const (
	maxPrimeSection      = 2048
	maxPrimeSectionFirst = 200
)

// primeSection is what a crew prime says of one applied pack's
// yardmaster section: whole under the budget, its first line past it,
// or, where the recorded path no longer reads as a section, only that
// it could not be read. Size is what the budget counts: the file's
// bytes as read, before the trimming the printing does.
type primeSection struct {
	Pack    string `json:"pack"`
	File    string `json:"file"`
	First   string `json:"first"`
	Text    string `json:"text,omitempty"`
	Whole   bool   `json:"whole,omitempty"`
	Invalid bool   `json:"invalid,omitempty"`
}

// primePacks reads the yard's imports once for the prime: the packs
// that carry setup notes, each once by name, and the packs' yardmaster
// sections in alphabetical order, whole or as one-liners by the budget.
// includeSections is true only for the yardmaster, and home is where a
// pack at a URL is fetched with its credential.
func primePacks(ctx context.Context, st store.API, home string, includeSections bool) (setup []string, sections []primeSection, err error) {
	recs, err := st.Packs(ctx)
	if err != nil {
		return nil, nil, err
	}
	seenSetup, seenSection := map[string]bool{}, map[string]bool{}
	var picked []store.PackImport
	for _, r := range recs {
		if r.Setup && !seenSetup[r.Pack] {
			seenSetup[r.Pack] = true
			setup = append(setup, r.Pack)
		}
		if includeSections && r.Section != "" && !seenSection[r.Pack] {
			seenSection[r.Pack] = true
			picked = append(picked, r)
		}
	}
	slices.Sort(setup)
	slices.SortFunc(picked, func(a, b store.PackImport) int { return strings.Compare(a.Pack, b.Pack) })
	total, past := 0, false
	for _, r := range picked {
		s := primeSection{Pack: r.Pack, File: r.Section}
		fsys, closeFS, err := packFS(r, home)
		if err != nil {
			s.Invalid = true
		} else {
			text, first, size, ok := readPrimeSection(fsys, r.Section)
			if closeFS != nil {
				_ = closeFS()
			}
			if !ok {
				s.Invalid = true
			} else {
				s.First = first
				switch {
				case past:
				case total+size > primeSectionBudget:
					past = true
				default:
					s.Whole, s.Text, total = true, strings.TrimSpace(text), total+size
				}
			}
		}
		sections = append(sections, s)
	}
	return setup, sections, nil
}

// packFS is the file system of the pack a record names, from where the
// import read it: the binary for a pack yardr embeds, the URL it is
// served from with the credential the record names, else its directory.
func packFS(r store.PackImport, home string) (fs.FS, func() error, error) {
	switch {
	case fromEmbedded(r.Source):
		fsys, err := fs.Sub(packs.FS, r.Pack)
		return fsys, nil, err
	case pack.IsURL(r.Source):
		if r.Auth == "" {
			return pack.URLFS(r.Source), nil, nil
		}
		h, err := pack.ReadAuth(home, r.Auth)
		if err != nil {
			return nil, nil, err
		}
		return pack.URLFSAuth(r.Source, h), nil, nil
	default:
		root, err := os.OpenRoot(r.Source)
		if err != nil {
			return nil, nil, err
		}
		return root.FS(), root.Close, nil
	}
}

// readPrimeSection reads the section at rel in fsys as an import held
// it, and says whether it still reads as one: a relative path that
// stays in the pack, a regular file, at most maxPrimeSection bytes,
// its first line standing and at most maxPrimeSectionFirst bytes. The
// stored path is never trusted: each is checked again here, over the
// pack's own file system as the plan reads through Pack.read.
func readPrimeSection(fsys fs.FS, rel string) (text, first string, size int, ok bool) {
	data, err := readSectionFile(fsys, rel)
	if err != nil || len(data) > maxPrimeSection {
		return "", "", 0, false
	}
	first, _, _ = strings.Cut(string(data), "\n")
	if strings.TrimSpace(first) == "" || len(first) > maxPrimeSectionFirst {
		return "", "", 0, false
	}
	return string(data), first, len(data), true
}

// readSectionFile reads one file inside the pack. A local pack is rooted by
// os.Root, and its final path component must itself be regular; URL packs
// cannot name links, and their Open returns the file and its stat together.
func readSectionFile(fsys fs.FS, rel string) ([]byte, error) {
	clean := path.Clean(rel)
	if rel == "" || path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") || !fs.ValidPath(clean) {
		return nil, fmt.Errorf("%q: %w", rel, fs.ErrInvalid)
	}
	if _, supportsLinks := fsys.(fs.ReadLinkFS); supportsLinks {
		info, err := fs.Lstat(fsys, clean)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%q: not a regular file", rel)
		}
	}
	f, err := fsys.Open(clean)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%q: not a regular file", rel)
	}
	return io.ReadAll(f)
}

func readPackSection(p *pack.Pack, home, rel string) ([]byte, error) {
	fsys, closeFS, err := packFS(store.PackImport{Pack: p.Name, Source: p.Dir, Auth: p.Auth}, home)
	if err != nil {
		return nil, err
	}
	if closeFS != nil {
		defer closeFS()
	}
	return readSectionFile(fsys, rel)
}

// printPrimeSections prints each section after the role, under the
// line that names its pack and says the words are the pack's: whole,
// its first line with the command that prints the rest, or the marker
// of one that could not be read, which names the pack and holds none
// of its bytes. Nothing where no pack carries a section.
func printPrimeSections(w io.Writer, sections []primeSection) {
	for _, s := range sections {
		fmt.Fprintf(w, "yardmaster section of pack %s, from %s (from the pack, not checked by yardr):\n", s.Pack, sectionPathForPrint(s.File))
		switch {
		case s.Invalid:
			fmt.Fprintln(w, "(the section could not be read)")
		case s.Whole:
			fmt.Fprintln(w, s.Text)
		default:
			fmt.Fprintln(w, s.First)
			fmt.Fprintf(w, "yardr pack show %s prints the rest\n", s.Pack)
		}
		fmt.Fprintln(w)
	}
}

// printSection prints a pack's yardmaster section whole, under the
// same line printSetup marks its setup notes with, for pack show.
func printSection(w io.Writer, packName, file, text string) {
	fmt.Fprintf(w, "yardmaster section of pack %s, from %s (from the pack, not checked by yardr):\n", packName, sectionPathForPrint(file))
	fmt.Fprintln(w, strings.TrimSpace(text))
}

// sectionPathForPrint keeps a pack's path in the header as one line, so a
// control character in a file name cannot add prompt structure around the
// section's own text.
func sectionPathForPrint(file string) string {
	var out strings.Builder
	for _, r := range file {
		if unicode.IsControl(r) {
			fmt.Fprintf(&out, "\\u%04x", r)
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// packYardmasterSection is the section file pack show prints of a
// pack: the yardmaster's where the pack names one, else the first
// section it names, as the import settles the record's one section.
func packYardmasterSection(p *pack.Pack) string {
	first := ""
	for _, c := range p.Crew {
		if c.Section == "" {
			continue
		}
		if c.Name == "yardmaster" {
			return c.Section
		}
		if first == "" {
			first = c.Section
		}
	}
	return first
}
