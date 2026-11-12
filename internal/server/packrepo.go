package server

import (
	"context"
	"fmt"

	"yardr.dev/yardr/internal/pack"
	"yardr.dev/yardr/internal/store"
)

// packFetcherFor is the fetching of the packs repository that the server
// registers on its store (store.SetPackFetcher): a delegated pack apply
// names a pack of that repository by its URL and its digest, and the store
// takes the pack from here and nowhere else. origin is where the
// repository serves from (pack.Repository; a test hands a stand-in's) and
// home is the yard's, where a depot plan reads the installed files it
// would change.
//
// The fetch holds the pack to the pinned digest before anything is read of
// it, so the reading answered is of exactly the bytes the digest names, and
// every file the reading and the plan read is read from the first answer of
// the server (Pack.ReadFile): a repository that answers differently the
// second time changes nothing the store holds the run to.
func packFetcherFor(home, origin string) store.PackFetcher {
	return func(ctx context.Context, from, digest string) (store.PackReader, error) {
		// One spelling of a pack's URL stands in the yard's records, and a
		// URL that is not the repository's is refused before any request.
		if _, url, err := pack.RepositoryPack(origin, from); err != nil {
			return nil, err
		} else if from != url {
			return nil, fmt.Errorf("pack URL %s: write it as %s", from, url)
		}
		p, err := pack.LoadRepository(origin, from, digest)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, name string, depot string, mappings store.Mappings, yard store.API) (*store.PackReading, error) {
			// A run ask of the URL claims no name: it takes the pack's
			// from the fetched bytes, held to its directory's by the
			// load above.
			if name != "" && name != p.Name {
				return nil, fmt.Errorf("pack %s: the pack fetched from %s is %s: nothing was written", name, p.Dir, p.Name)
			}
			reading, err := readPack(ctx, p, p.ReadFile, p.Dir, name, depot, mappings, yard, home)
			if err != nil {
				return nil, err
			}
			// A later write of the run cannot fetch the pack again inside the
			// store's write, so a declared source's text is held on the record
			// by its hash (store.TextSum, packSourceWrite).
			if reading.Targets == nil {
				reading.Targets = map[string]string{}
			}
			for _, s := range reading.Sources {
				reading.Targets["source\x00"+s.Name] = store.TextSum(s.Text)
			}
			return reading, nil
		}, nil
	}
}
