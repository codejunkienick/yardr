// The directory half of the pack apply row (design yardr-6maq, 3.3): a
// pack read from a directory inside a registered depot's main checkout,
// carded whole and pinned by its digest, approved under the standing
// strong doors. Called from the card path in runask_pack.go, which holds
// the whole-card bounds and refuses past them; there is no summary shape
// for a directory. A directory outside every checkout, a worktree, a URL
// and an earlier import's record are each refused at the ask.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// IsPackDir reports whether the pack word names a directory: as the
// terminal's pack apply reads it (internal/cli packDir), a word with a
// path separator in it, or a lone dot or dot-dot. A bare word is never
// a directory, so what a name means does not depend on where the
// command is run.
func IsPackDir(arg string) bool {
	return strings.ContainsRune(arg, filepath.Separator) || strings.Contains(arg, "/") || arg == "." || arg == ".."
}

// isPackURL reports whether the pack word names a pack by where it is
// served: as pack.IsURL reads it, which the store does not import.
func isPackURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// packDirNoReader refuses a directory pack where no server registered its
// directory reading on the store.
func packDirNoReader(dir string) error {
	return fmt.Errorf("pack %s: no server has registered its reading of a directory pack on this store, so no card can be written from the directory: a pack is proposed from a yard its server runs", dir)
}

// packCardReading is packApplyCard's reading of its pack: a directory
// through the server's directory reading, after the depot-checkout rule
// judges it, anything else through the embedded reading. sha is the
// digest the asker named from pack check, "" for none: it must name the
// directory as read. At approval a directory that holds no pack reads as
// the stale it is, and nothing runs.
func (w *wtx) packCardReading(ctx context.Context, name, depot string, mappings Mappings, sha string, asking bool) (*PackReading, bool, error) {
	if !IsPackDir(name) {
		read := w.packReaderOf()
		if read == nil {
			return nil, false, fmt.Errorf("pack %s: no server has registered its reading of the packs yardr embeds on this store, so no card can be written from the binary that runs the yard: a pack is proposed from a yard its server runs", name)
		}
		reading, err := read(ctx, name, depot, mappings, w.store)
		if err != nil {
			if errors.Is(err, ErrNotEmbedded) {
				if rec := w.packRecordOf(name); rec != nil {
					return nil, false, packRecordRefusal(name, rec)
				}
			}
			return nil, false, err
		}
		if reading == nil || reading.Name != name {
			return nil, false, fmt.Errorf("pack %s: the server's reading named another pack: nothing was stored", name)
		}
		return reading, false, nil
	}
	read := w.directoryReaderOf()
	if read == nil {
		return nil, true, packDirNoReader(name)
	}
	resolved, err := w.packDirCheckout(name)
	if err != nil {
		return nil, true, err
	}
	reading, err := read(ctx, resolved, depot, mappings, w.store)
	if err != nil {
		if asking {
			return nil, true, fmt.Errorf("pack %s: %s: it is the operator's to type in a terminal (yardr pack check %s shows its plan there)", name, OneLine(err.Error()), name)
		}
		return nil, true, fmt.Errorf("was written from the directory %s as it stood, and it holds no pack now: %s: the card is from before that changed", name, OneLine(err.Error()))
	}
	if reading == nil {
		return nil, true, fmt.Errorf("pack %s: the server's reading named no pack: nothing was stored", name)
	}
	if sha != "" {
		if err := packDirPin(name, resolved, sha, reading.Digest); err != nil {
			return nil, true, err
		}
	}
	return reading, true, nil
}

// ResolvePackDir is dir as a directory pack's reading is of it:
// absolutized, with symlinks followed. A path that resolves to nothing
// stands as written: it is in no checkout, and where its absence is the
// only thing wrong the read refuses it with the terminal road. The card
// and the carrying out resolve the same word the same way, so the label
// the card pins names the directory the carrying out reads.
func ResolvePackDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}
	return abs, nil
}

// packDirCheckout resolves dir against the yard's depots: an absolute
// path, with symlinks followed, it must stand inside a registered
// depot's main checkout, resolved the same way. A worktree of a depot is
// elsewhere and fails the rule, as does any other directory outside
// every checkout. A relative path means nothing to the yard's server,
// whose directory is not the asker's: the CLI makes one absolute where
// the ask is typed, and anything else names one. It answers the resolved
// directory the reading is of.
func (w *wtx) packDirCheckout(dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("pack %s: %q is no absolute directory: a card names the directory as an absolute path inside a registered depot's main checkout (yardr pack apply %s)", dir, dir, dir)
	}
	resolved, err := ResolvePackDir(dir)
	if err != nil {
		return "", err
	}
	inside, nested := false, false
	if err := each(w.Tx, bDepots, "", func(_ []byte, d *Depot) error {
		if inside || d.Path == "" {
			return nil
		}
		root, err := ResolvePackDir(d.Path)
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return nil
		}
		isNested, err := packDirNestedCheckout(resolved, root)
		if err != nil {
			return err
		}
		if isNested {
			nested = true
		} else {
			inside = true
		}
		return nil
	}); err != nil {
		return "", err
	}
	if !inside && nested {
		return "", fmt.Errorf("pack %s: %s is inside a nested checkout, not the registered depot's main checkout (a worktree of a depot is not its main checkout): it is the operator's to apply in a terminal (yardr pack apply %s)", dir, resolved, dir)
	}
	if !inside {
		return "", fmt.Errorf("pack %s: %s is not inside a registered depot's main checkout of this yard (a worktree of a depot is not its main checkout): a card applies a pack yardr embeds or a directory inside one; it is the operator's to apply in a terminal (yardr pack apply %s)", dir, resolved, dir)
	}
	return resolved, nil
}

// packDirNestedCheckout reports whether dir or one of its parents below
// root starts another checkout. A nested checkout has a .git file or
// directory at its root, and is not part of the registered depot's main
// checkout even when its path is below that root.
func packDirNestedCheckout(dir, root string) (bool, error) {
	for at := dir; at != root; at = filepath.Dir(at) {
		if at == filepath.Dir(at) {
			return false, nil
		}
		_, err := os.Lstat(filepath.Join(at, ".git"))
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, os.ErrNotExist):
			continue
		default:
			return false, fmt.Errorf("pack %s: could not check whether it is inside a nested checkout: %w", dir, err)
		}
	}
	return false, nil
}

// packRecordOf is the yard's record of an import of the pack name, if it
// holds one: the name is an earlier import's, read again from where it
// was imported from, and no card is written from it.
func (w *wtx) packRecordOf(name string) *PackImport {
	var found *PackImport
	_ = each(w.Tx, bPacks, "", func(_ []byte, p *PackImport) error {
		if p.Pack == name && found == nil {
			rec := *p
			found = &rec
		}
		return nil
	})
	return found
}

// packRecordRefusal refuses a bare pack name the yard imported before: a
// card is of a pack the binary embeds or a directory in a checkout, and
// an earlier import is read again from where it came from.
func packRecordRefusal(name string, rec *PackImport) error {
	from := rec.Source
	if from == "" {
		from = "an import the record names nowhere"
	}
	return fmt.Errorf("pack %s: this yard imported it from %s, and a card applies a pack yardr embeds or a directory inside a depot's main checkout: it is the operator's to apply in a terminal (yardr pack apply %s)", name, from, name)
}

// packURLRefusal refuses a pack at a URL: no card fetches one.
func packURLRefusal(name string) error {
	return fmt.Errorf("pack %s: a pack at a URL is the operator's to apply in a terminal (yardr pack apply %s): a card applies a pack yardr embeds or a directory inside a depot's main checkout", name, name)
}

// packDirDigest reads the digest a --sha256 flag names, as
// pack.ParseDigest does, which the store does not import: 64 hex digits,
// in either case, with or without the sha256: pack check prints.
func packDirDigest(pin string) (string, error) {
	hexDigits := pin
	if len(pin) > len("sha256:") && strings.EqualFold(pin[:len("sha256:")], "sha256:") {
		hexDigits = pin[len("sha256:"):]
	}
	raw, err := hex.DecodeString(hexDigits)
	if err != nil || len(raw) != sha256.Size {
		return "", errors.New("not a digest: want the 64 hex digits of the pack's sha256, as pack check prints it")
	}
	return hex.EncodeToString(raw), nil
}

// packDirPin holds the directory's reading to the digest the asker named
// from pack check: it must name the directory as read, and refuses the
// ask where it names another.
func packDirPin(name, resolved, want, have string) error {
	pin, err := packDirDigest(want)
	if err != nil {
		return fmt.Errorf("pack %s: --sha256 %q: %w", name, want, err)
	}
	if have != pin {
		return fmt.Errorf("pack %s: the pin is sha256:%s, and the directory %s as read has sha256:%s: the ask names the digest pack check prints for the directory (yardr pack check %s)", name, pin, resolved, have, name)
	}
	return nil
}

// packDirDigestLine prints the digest the card pins on its first line,
// whole: a depot card where a summary prints it, after its counts, and a
// source card after its label.
func packDirDigestLine(does []string, digest string, depot bool) []string {
	if len(does) == 0 || digest == "" {
		return does
	}
	const askedNothing = ". It asks nothing."
	if depot {
		if head, ok := strings.CutSuffix(does[0], askedNothing); ok {
			does[0] = head + ", digest sha256:" + digest + askedNothing
			return does
		}
	} else if i := strings.Index(does[0], ")"); i >= 0 {
		does[0] = does[0][:i+1] + ", digest sha256:" + digest + does[0][i+1:]
		return does
	}
	return append(does, "digest sha256:"+digest+".")
}
