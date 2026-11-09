// The file the yard writes for a prompt a card sets (group add, group set and
// crew set). A prompt config names a file each session reads when it starts,
// so a card that saved the asker's own path would hold the prompt to the text
// it showed only until the next write to that path: the approval would
// approve bytes nothing keeps. The yard writes the carried text itself, to a
// file it names by the text's digest, and saves that path instead. The file
// the ask names is where the text was read from, and nothing live.
//
// The limit is stated: a process of the yard's own OS user can still chmod
// what the yard wrote and overwrite it. What this stops is an asker's chosen
// path being the live prompt, not a determined local writer.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// yardPromptMode is the mode of a prompt file the yard writes: read-only, and
// private as a crew member's role is.
const yardPromptMode = 0o400

// yardPromptName matches the end of the name of a prompt file the yard
// writes: the digest of its text, which it captures, then .md. The name alone
// decides, whatever stands on the disk, and case does not, since a file
// system that does not tell case apart reaches the same file by either
// spelling.
var yardPromptName = regexp.MustCompile(`(?i)\.([0-9a-f]{64})\.md$`)

// promptRewritten says whether the file at path, which holds text, is named
// as the yard names the files it writes and holds other text than the digest
// in that name: it was rewritten in place since, which its read-only mode
// does not stop a process of the yard's own user from doing (the limit stated
// above). A file named otherwise is never said to be rewritten, since its
// name says nothing of its text.
func promptRewritten(path, text string) bool {
	sum := sha256.Sum256([]byte(text))
	return promptDigestRewritten(path, sum[:])
}

// promptDigestRewritten says whether a digest-bearing yard prompt name does
// not match the SHA-256 of the whole file, even when the card cannot show
// that file's text.
func promptDigestRewritten(path string, sum []byte) bool {
	m := yardPromptName.FindStringSubmatch(path)
	return m != nil && len(sum) != 0 && !strings.EqualFold(m[1], hex.EncodeToString(sum))
}

// yardPromptPath is the file the approval of a prompt writes the text of
// name's prompt to, and the path the definition saves: the yard's roles
// directory, by the name and the SHA-256 of the text, so that one text has one
// file, and another text another, which leaves the file of the prompt before
// it where it stood for the card's Back to name. A slash in a group's name
// becomes an underscore, so that the file is one name in the directory and the
// name cannot lead out of it.
func (w *wtx) yardPromptPath(subject, name, text string) (string, error) {
	_, home, err := w.homeDirs(subject)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(text))
	file := strings.ReplaceAll(name, "/", "_") + "." + hex.EncodeToString(sum[:]) + ".md"
	return filepath.Join(home, "roles", file), nil
}

// promptStands says whether the file at path holds exactly text, and refuses
// a file that stands there and is not the yard's to use: one that holds other
// text, or that is no regular file. A symlink is the one to fear, since
// reading through it would make the asker's own file the live prompt. Nothing
// at path is no refusal, since the approval writes it.
func promptStands(subject, path, text string) (bool, error) {
	// Opened without following a symlink and without blocking, and judged by
	// the open file, as homeFile reads one.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("%s: %s stands where the yard writes the prompt and is not one it can use: %s", subject, path, OneLine(err.Error()))
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return false, fmt.Errorf("%s: %s stands where the yard writes the prompt and is not a regular file", subject, path)
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(len(text))+1))
	if err != nil {
		return false, fmt.Errorf("%s: %s: %s", subject, path, OneLine(err.Error()))
	}
	if string(data) != text {
		return false, fmt.Errorf("%s: %s stands where the yard writes the prompt and holds other text than its name says: it was changed or put there since: remove it, or carry other text", subject, path)
	}
	return true, nil
}

// writePrompt makes the file at path hold text, the prompt an approval saves:
// written whole or not at all and over nothing, read-only (writeRoleFile). A
// file that stands there already is kept when it holds exactly the text, since
// the same text comes back (a prompt set again after another, a write the
// store runs twice), and is refused otherwise (promptStands).
func (w *wtx) writePrompt(subject, path, text string) error {
	_, home, err := w.homeDirs(subject)
	if err != nil {
		return err
	}
	err = writeRoleFile(home, path, text, yardPromptMode)
	if errors.Is(err, fs.ErrExist) {
		stands, standErr := promptStands(subject, path, text)
		if standErr != nil {
			return standErr
		}
		if stands {
			return nil
		}
	}
	if err != nil {
		return fmt.Errorf("%s: write its prompt to %s: %w", subject, path, err)
	}
	return nil
}
