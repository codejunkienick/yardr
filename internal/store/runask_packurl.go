// The repository-URL half of the pack apply row (design yardr-6maq, 3.3):
// `pack apply <url> --sha256 <digest>` where the URL is the packs
// repository's alone, carded in the embedded card's shape from the
// fetched, digest-held bytes. The server fetches through the standing
// PackFetcher outside the ask's write (fetchPackURL), holds the bytes to
// the digest before anything is read of them, and reads the card from
// those bytes once, never from a second answer. Every other URL refuses
// at the ask, before any fetch. Called from the card path in
// runask_pack.go, whose weight, caps, stale and carrying out are the
// embedded row's.
package store

import (
	"context"
	"fmt"
	"strings"
)

// IsPackURL reports whether the pack word names a pack by where it is
// served: as pack.IsURL reads it, which the store does not import.
func IsPackURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// packURLNoFetcher refuses a pack of the packs repository where no
// server registered its fetching of it on the store.
func packURLNoFetcher(url string) error {
	return fmt.Errorf("pack %s: no server has registered its fetching of the packs repository on this store, so no card can be written from what it serves: a pack is proposed from a yard its server runs", url)
}

type urlPackKey struct{}

// urlPackOf is the packs repository's reading the write was given before
// it (prewriteRun), nil where the row reads none.
func urlPackOf(ctx context.Context) PackReader {
	read, _ := ctx.Value(urlPackKey{}).(PackReader)
	return read
}

// fetchPackURL is the fetching a pack apply card of a URL is written
// from (runCommand.fetch): the packs repository's pack at the URL, held
// to the digest the command pins, fetched before the write that stores
// the ask and again before the approval's. The store has one writer, and
// a slow origin must not stall every other write of the yard. Anything
// but a URL is none; a URL without its pin, or where no server
// registered its fetching, is refused before anything is fetched. What
// is not the repository's, is not the pinned one, or is not called what
// its directory is, the fetching itself refuses — before any request
// where the URL is none of its (PackFetcher).
func fetchPackURL(ctx context.Context, s *Store, argv []string) (PackReader, error) {
	if len(argv) < 3 || !IsPackURL(argv[2]) {
		return nil, nil
	}
	_, _, _, _, sha, err := packApplyWords(argv[2:])
	if err != nil {
		return nil, err
	}
	if s.packFetcher == nil {
		return nil, packURLNoFetcher(argv[2])
	}
	return s.packFetcher(ctx, argv[2], sha)
}

// packURLCardReading is packCardReading's reading of a pack of the packs
// repository: through the fetching the write was given before it
// (fetchPackURL), held to the digest the asker named. asking is the
// write that stores the ask; an approval reads it again, and a pack the
// origin no longer serves at that digest reads as the stale it is, with
// nothing run.
func (w *wtx) packURLCardReading(ctx context.Context, url, depot string, mappings Mappings, asking bool) (*PackReading, error) {
	read := w.urlPack
	if read == nil {
		return nil, packURLNoFetcher(url)
	}
	// No claimed name: the pack's name comes from the fetched bytes,
	// held to its directory's by the fetching itself.
	reading, err := read(ctx, "", depot, mappings, w.store)
	if err != nil {
		if asking {
			return nil, fmt.Errorf("pack %s: %s: it is the operator's to type in a terminal (yardr pack check %s shows its plan there)", url, OneLine(err.Error()), url)
		}
		return nil, fmt.Errorf("was written from the pack at %s as it stood, and it holds no pack now: %s: the card is from before that changed", url, OneLine(err.Error()))
	}
	if reading == nil {
		return nil, fmt.Errorf("pack %s: the server's reading named no pack: nothing was stored", url)
	}
	return reading, nil
}
