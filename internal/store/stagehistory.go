// The text a stage file's save replaces. A stage file is a plain file in the
// yard's home (stagewrite.go) that a save puts the new text over, and an event
// holds no prose, so what a save replaced would be gone from the yard. It is
// kept first, in a flat directory of the home: one read-only file for each
// text, named by the SHA-256 of its bytes, which is what the save's flow_set
// event records (prev_digest) and what a stage file's --digest names. Putting
// the text back is one more save, of that file (yardr flow set-stage <flow>
// <stage> -f <home>/stage-history/<sha256>).
//
// The directory stands outside flows/, where HomeStageFiles would take a file
// in it for a stage file named after no stage, and outside blobs/, the one
// directory Store.Prune sweeps, so nothing but its owner removes a kept text.
package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"unicode/utf8"

	bolt "go.etcd.io/bbolt"
)

// stageHistoryMode is the mode of a kept text: read-only, as the prompt files
// the yard writes are (yardPromptMode).
const stageHistoryMode = 0o400

// stageHistoryName matches the name of a kept text: the SHA-256 of its bytes
// in 64 lower case hex digits, as stageDigest names a file's. Nothing else is
// a name there, so a digest cannot lead out of the directory.
var stageHistoryName = regexp.MustCompile(`^[0-9a-f]{64}$`)

// StageHistoryDir is the directory under home where the yard keeps the text
// a save of a stage file replaced: flat, a file for each text.
func StageHistoryDir(home string) string { return filepath.Join(home, "stage-history") }

// StageHistoryPath is the file under home that keeps the text whose SHA-256
// is digest, and "" for a digest that is no such name (stageNone among them),
// so that what a caller passes cannot lead out of StageHistoryDir.
func StageHistoryPath(home, digest string) string {
	if !stageHistoryName.MatchString(digest) {
		return ""
	}
	return filepath.Join(StageHistoryDir(home), digest)
}

// stageWriteState is what a stage file's write leaves in wtx.stageWrote,
// which outlives one attempt of Store.write: a batch that runs the write again
// finds the file its first run wrote already on disk, and must still tell
// what that run replaced (putStageFile).
type stageWriteState struct {
	// key names what a run of the write has written, "" before one has: "stage
	// <flow> <stage>" for a stage file, or the marker of a copy of repository
	// flows (copyRepoFlows), which begins "copy-repo " so that neither kind of
	// write takes the other's for its own. A copy leaves nothing in the fields
	// below.
	key string
	// prevDigest and prevText are the file that run replaced, "" where it
	// created the file or replaced one with nothing in it.
	prevDigest, prevText string
}

// openStageHistory opens the kept text at path for reading, without following
// a symbolic link and without blocking, and judged by the open file, as
// promptStands reads one. It answers nil where none stands, and an error where
// something stands that is no regular file or cannot be opened.
func openStageHistory(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil
	case errors.Is(err, syscall.ELOOP):
		return nil, fmt.Errorf("%s is a symbolic link: the yard keeps the text of a stage file only in a plain file", path)
	case err != nil:
		return nil, fmt.Errorf("%s is not a file the yard can use: %s", path, OneLine(err.Error()))
	}
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("%s is not a regular file: the yard keeps the text of a stage file only in one", path)
	}
	return f, nil
}

// archiveStageFile keeps raw, the bytes of a stage file that is about to be
// replaced, as the file of the home's stage history that digest names: the
// SHA-256 the caller read raw as. homeReal is the home as its symbolic links
// resolve it, and the directory is made where it is missing, but never made
// through a link. It is written whole or not at all, read-only, and a file
// that stands there already is kept when it holds exactly raw, since the same
// text comes back (a file set to what it was, a save the store runs twice).
// Anything else there, a link, a file that is not a regular one or one that
// holds other text than its name says, is refused, and nothing is written over
// it: the save that asked is refused with it, so a text is never replaced
// unkept.
func archiveStageFile(homeReal, digest string, raw []byte) (err error) {
	path := StageHistoryPath(homeReal, digest)
	if path == "" {
		return fmt.Errorf("digest %q is no SHA-256 of a stage file's text", digest)
	}
	dir := filepath.Dir(path)
	if _, err := stageDirs(dir, dir, true); err != nil {
		return err
	}
	f, err := openStageHistory(path)
	if err != nil {
		return err
	} else if f != nil {
		defer f.Close()
		// One byte more than raw holds, so that a longer file is no match.
		got, err := io.ReadAll(io.LimitReader(f, int64(len(raw))+1))
		if err != nil {
			return fmt.Errorf("%s: %s", path, OneLine(err.Error()))
		} else if !bytes.Equal(got, raw) {
			return fmt.Errorf("%s holds other text than its name says: it was changed or put there since: remove it, and the yard keeps the text again", path)
		}
		return nil
	}
	tmp, err := os.CreateTemp(dir, ".yardr-history-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(name)
		}
	}()
	if _, err = tmp.Write(raw); err != nil {
		return err
	}
	if err = tmp.Chmod(stageHistoryMode); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// readStageHistory reads the text the stage history keeps under the SHA-256
// digest, for a caller that can do without it: "", false where none is kept,
// and where what stands there cannot be taken for the text its name says (a
// link, a file that is not a regular one, one that is longer than a card
// carries (MaxRunFlow), is no UTF-8, or hashes to another digest, which a
// rewrite or a truncation of the file does).
func readStageHistory(home, digest string) (string, bool) {
	if home == "" || StageHistoryPath(home, digest) == "" {
		return "", false
	}
	_, real, err := homeDirsAt(home, "stage history")
	if err != nil {
		return "", false
	}
	dir := StageHistoryDir(real)
	if have, err := stageDirs(dir, dir, false); err != nil || !have {
		return "", false
	}
	f, err := openStageHistory(StageHistoryPath(real, digest))
	if err != nil || f == nil {
		return "", false
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, MaxRunFlow+1))
	if err != nil || len(raw) > MaxRunFlow || !utf8.Valid(raw) {
		return "", false
	}
	if sum := sha256.Sum256(raw); hex.EncodeToString(sum[:]) != digest {
		return "", false
	}
	return string(raw), true
}

// stagePrior is the text the latest save of a store flow's stage file
// replaced, with its digest, as the Line carries it for the file whose digest
// is digest: the prev_digest of the latest flow_set of that stage that has
// one, unless it names the text that stands (a file put back by hand, which
// has nothing before it to offer), and the text the history keeps under it.
// A save that created the file or changed nothing has none, and does not
// hide the one before it. Both are "" where the event is gone (the log is
// pruned), or the text is not kept or is not what its name says: the read
// does without them.
func (s *Store) stagePrior(flow, stage, digest string) (prevDigest, prevText string) {
	var prev string
	err := s.read(func(tx *bolt.Tx) error {
		prefix := []byte("flow_set\x00")
		end := []byte("flow_set\x01")
		c := tx.Bucket(bEventsKd).Cursor()
		k, _ := c.Seek(end)
		if k == nil {
			k, _ = c.Last()
			for k != nil && bytes.Compare(k, end) >= 0 {
				k, _ = c.Prev()
			}
		} else {
			k, _ = c.Prev()
		}
		for ; k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Prev() {
			var e Event
			ok, err := get(tx, bEvents, string(k[len(prefix):]), &e)
			if err != nil || !ok || e.Data["flow"] != flow || e.Data["stage"] != stage {
				continue
			}
			p, _ := e.Data["prev_digest"].(string)
			if p != "" && p != digest {
				prev = p
				break
			}
		}
		return nil
	})
	if err != nil || prev == "" {
		return "", ""
	}
	if text, ok := readStageHistory(s.home, prev); ok {
		return prev, text
	}
	return "", ""
}
