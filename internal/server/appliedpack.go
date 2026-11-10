package server

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"yardr.dev/yardr/internal/pack"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/version"
)

// A setup card of a pack yardr does not embed is carded from the directory
// the operator applied the pack from, and from nothing else: a pack the yard
// has no record of, one applied from a URL and one whose directory is no
// longer what was applied are the operator's to apply in a terminal. What
// the record keeps of the apply is the pin: the git commit the directory was
// at and that it was clean. A directory at that commit and still clean holds
// the bytes the operator applied, whoever has written to the repository
// since, so the card is of what the operator already did, and it shows
// every byte of it for their approval as a card of an embedded pack does.

// appliedReaderFor is the reading of a pack the operator applied from a
// directory that the server registers on its store
// (store.SetAppliedPackReader): a setup card is carded from it where
// packReaderFor says yardr embeds no pack of that name, and a pack apply
// card never is. home is the yard's, as packReaderFor's.
func appliedReaderFor(home string) store.PackReader {
	return func(ctx context.Context, name string, depot string, mappings store.Mappings, yard store.API) (*store.PackReading, error) {
		reading, _, err := readApplied(ctx, name, depot, mappings, yard, home)
		return reading, err
	}
}

// readApplied is the reading of the pack of a name the operator applied from
// a directory, with the pack it was read from: the carrying out of an
// approved setup applies that same load, so what the card was held to, the
// plan and the files it copies are one reading of each file (Pack keeps its
// first read). A pack that is not of kind source is read for its kind
// alone: a setup card refuses it in the words it has for any such pack, and
// nothing of it is carded, so nothing of it needs pinning.
func readApplied(ctx context.Context, name string, depot string, mappings store.Mappings, yard store.API, home string) (*store.PackReading, *pack.Pack, error) {
	p, label, err := appliedPack(ctx, name, yard)
	if err != nil {
		return nil, nil, err
	}
	if p.Kind != pack.KindSource {
		return &store.PackReading{Name: p.Name, Kind: p.Kind, Label: label, Build: version.Describe()}, p, nil
	}
	reading, err := readPack(ctx, p, p.ReadFile, label, name, depot, mappings, yard, home)
	return reading, p, err
}

// appliedPack loads the pack of a name from the directory the yard's record
// of its apply names, held to what the record kept: the directory is still
// at the commit the apply found it at, and clean, as it was. The label says
// where it was read from, as the embedded reading's says whose binary it
// was: the directory and the commit.
func appliedPack(ctx context.Context, name string, yard store.API) (*pack.Pack, string, error) {
	recs, err := yard.Packs(ctx)
	if err != nil {
		return nil, "", err
	}
	// A pack of kind source is applied into the yard and no depot, whose
	// record stands first; any other record is of a pack for a depot, which
	// no setup card is of.
	var rec *store.PackImport
	for i := range recs {
		if recs[i].Pack == name && (rec == nil || recs[i].Depot == "") {
			rec = &recs[i]
		}
	}
	const not = "yardr embeds no pack of that name"
	switch {
	case rec == nil:
		return nil, "", fmt.Errorf("pack %s: %s, and this yard has no record of applying one from a directory: the operator applies it from its directory in a terminal first (yardr pack apply <directory>), and a setup card is asked after", name, not)
	case pack.IsURL(rec.Source):
		return nil, "", fmt.Errorf("pack %s: %s, and this yard applied it from the URL %s: a pack at a URL is the operator's to apply in a terminal (yardr pack apply <url>), and no setup card is asked of one", name, not, rec.Source)
	case !filepath.IsAbs(rec.Source):
		return nil, "", fmt.Errorf("pack %s: %s, and this yard's record of it names %q, which is no directory it was applied from: the operator applies it from its directory in a terminal (yardr pack apply <directory>), and a setup card is asked after", name, not, rec.Source)
	}
	dir, terminal := rec.Source, "yardr pack apply "+rec.Source
	p, err := pack.LoadDir(dir)
	if err != nil {
		return nil, "", fmt.Errorf("pack %s: the directory %s it was applied from does not hold it now: %w: the operator applies it again in a terminal (%s) and a setup card is asked after", name, dir, err, terminal)
	}
	if p.Name != name {
		return nil, "", fmt.Errorf("pack %s: the directory %s it was applied from holds the pack %s now: the operator applies it again in a terminal (%s) and a setup card is asked after", name, dir, p.Name, terminal)
	}
	label := dir
	if rec.Commit != "" {
		label += " at " + commit12(rec.Commit)
	}
	// What is carded of a pack that is not of kind source is its kind alone.
	if p.Kind != pack.KindSource {
		return p, label, nil
	}
	// The record is the card's trust, and anyone of the crew may write
	// one over the socket: only the operator's own apply in a terminal
	// is trusted. A yard with no operator holds nothing to anyone, as
	// the operator's checks do.
	users, err := yard.Users(ctx)
	if err != nil {
		return nil, "", err
	}
	ops := store.Operators(users)
	if len(ops) == 0 {
		var settings map[string]string
		if settings, err = yard.Settings(ctx); err != nil {
			return nil, "", err
		}
		if by := settings[store.OperatorSetting]; by != "" {
			ops = []string{by}
		}
	}
	if len(ops) > 0 && !slices.Contains(ops, rec.By) {
		by := rec.By
		if by == "" {
			by = "nobody"
		}
		return nil, "", fmt.Errorf("pack %s: this yard's record of it says %s applied it from %s, and a setup card is of a pack %s applied in a terminal: the operator applies it again in a terminal (%s) and a setup card is asked after", name, by, dir, store.OperatorWords(ops), terminal)
	}
	switch {
	case rec.Commit == "":
		return nil, "", fmt.Errorf("pack %s: it was applied from %s, which was in no git repository then, so there is no commit to pin a setup card to: the operator keeps the pack in a repository and applies it again in a terminal (%s)", name, dir, terminal)
	case rec.Dirty:
		return nil, "", fmt.Errorf("pack %s: it was applied from %s with uncommitted changes, so no commit says what was applied: the operator commits them and applies it again in a terminal (%s)", name, dir, terminal)
	}
	sha, dirty := pack.Commit(dir)
	switch {
	case sha == "":
		return nil, "", fmt.Errorf("pack %s: %s is in no git repository now, or git cannot say, so nothing pins it to the commit %s it was applied at: the operator applies it again in a terminal (%s)", name, dir, commit12(rec.Commit), terminal)
	case sha != rec.Commit:
		return nil, "", fmt.Errorf("pack %s: %s is at the commit %s now, not the %s it was applied at: the operator applies it again in a terminal (%s) and a setup card is asked after", name, dir, commit12(sha), commit12(rec.Commit), terminal)
	case dirty:
		return nil, "", fmt.Errorf("pack %s: %s has uncommitted changes now, and a setup card is held to the commit %s it was applied at: the operator commits or drops them, or applies it again in a terminal (%s)", name, dir, commit12(rec.Commit), terminal)
	}
	return p, label, nil
}

// commit12 is a commit as the card's label and yardr pack list name it: its
// first twelve characters.
func commit12(sha string) string { return sha[:min(12, len(sha))] }
