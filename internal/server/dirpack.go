package server

import (
	"context"
	"fmt"

	"yardr.dev/yardr/internal/pack"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/version"
)

// A pack apply card of a directory is carded from the directory as it
// stands, read again at approval and held to its digest: the store judges
// the directory against the yard's depots before this reading is asked
// (runask_packdir.go), and this reading trusts the directory it is given.
// The label says where it was read from, as the embedded reading's says
// whose binary it was: the directory, with the build beside it, so an
// approval can tell a build that moved alone from bytes that moved.

// directoryReaderFor is the reading of a pack in a directory that the
// server registers on its store (store.SetDirectoryPackReader): a pack
// apply run ask of a directory is carded from it, and the carrying out
// of an approved one applies from the same reading, held to the card's
// digest. home is the yard's, as packReaderFor's.
func directoryReaderFor(home string) store.PackReader {
	return func(ctx context.Context, dir string, depot string, mappings store.Mappings, yard store.API) (*store.PackReading, error) {
		return readDirectoryPack(ctx, dir, depot, mappings, yard, home)
	}
}

// readDirectoryPack reads the pack in the directory dir: its kind, each
// declared source's whole text with each command file's bytes, and for a
// depot kind the plan built against the named depot with the named
// mappings, or, naming neither, against the depot that already holds the
// pack, as a re-apply always has. The directory stands resolved, as its
// caller judged it (store.ResolvePackDir).
func readDirectoryPack(ctx context.Context, dir string, depot string, mappings store.Mappings, yard store.API, home string) (*store.PackReading, error) {
	p, err := pack.LoadDir(dir)
	if err != nil {
		return nil, err
	}
	label := fmt.Sprintf("directory %s in yardr %s", p.Dir, version.Describe())
	return readPack(ctx, p, p.ReadFile, label, dir, depot, mappings, yard, home)
}
