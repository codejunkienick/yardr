// Writing the stage file of a store flow's stage. It is a plain file in the
// yard's home (HomeStageFile), which no record of the store holds, so the
// write is one the store makes to the disk itself: it names what stands
// there by a digest (--digest on the command, Seen on the card), puts the
// new file in whole or not at all, and never reads or writes through a
// symbolic link, so a link in the flows directory cannot send the write
// elsewhere.
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
	"strings"
	"syscall"
	"unicode/utf8"
)

// errNoStageHome refuses a stage file's write where the store has no home to
// keep one in: the path would be read against whatever the process runs in.
var errNoStageHome = errors.New("this store has no home to keep stage files in")

// stageNone is the digest of a stage file that stands as none: no file, or
// one with nothing in it, which is how HomeStageFile reads it.
const stageNone = "none"

// stageDigest is how a stage file's bytes are named: stageNone for a file
// the readers take as none, else the SHA-256 of its bytes in lower case hex,
// as sha256sum prints it.
func stageDigest(raw []byte) string {
	if strings.TrimSpace(string(raw)) == "" {
		return stageNone
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// stageExpect reads the digest a write is held to: "" for none (the write
// is unconditional), stageNone for a write that creates the file, in any
// case, or the SHA-256 of the file it replaces in 64 hex digits, which is
// returned in lower case so that one digest is one string.
func stageExpect(s string) (string, error) {
	low := strings.ToLower(s)
	if s == "" || low == stageNone {
		return low, nil
	}
	if raw, err := hex.DecodeString(low); err == nil && len(raw) == sha256.Size {
		return low, nil
	}
	return "", fmt.Errorf("digest %q: it is the SHA-256 of the stage file that stands, in 64 hex digits, or %s where none stands", s, stageNone)
}

// stageRel is where a stage's file stands under its flow's directory, and
// refuses a stage name that would stand elsewhere: a slash puts the file a
// directory deeper (qa/smoke), and a part that leads out or back, an empty
// one or an absolute path is no stage file's.
func stageRel(stage string) (string, error) {
	rel := filepath.FromSlash(stage) + ".md"
	if !validStage.MatchString(stage) || !filepath.IsLocal(rel) || filepath.Clean(rel) != rel {
		return "", fmt.Errorf("stage %q is no name of a stage file: use letters, digits, '.', '_', '-' and '/', and no empty, '.' or '..' part", stage)
	}
	return rel, nil
}

// stageText refuses text that is no stage file: nothing but white space is
// what the readers take as none, and a file is text.
func stageText(text string) error {
	switch {
	case strings.TrimSpace(text) == "":
		return errors.New("a stage file with nothing in it is no stage file: the yard reads it as none. To take a stage's file away, remove the file in a terminal")
	case !utf8.ValidString(text):
		return errors.New("a stage file is text: this is not valid UTF-8")
	}
	return nil
}

// stageUnshown says why a card cannot show text whole and as written, "" where
// it can: the lines are the card's, and a character it leaves out or
// reorders, or a mark the yard puts on its own lines, is no longer what the
// person saw (promptCarriage's rule). How long it may be is the caller's.
func stageUnshown(text string) string {
	if !utf8.ValidString(text) || strings.IndexFunc(text, crewRoleHidden) >= 0 || strings.Contains(text, NoteFilesMark) || strings.Contains(text, WeightMark) {
		return "it is not text a card shows as written"
	}
	return ""
}

// stageStand is what stands at the stage file of a store flow's stage, as
// the write reads it.
type stageStand struct {
	// path is the file as the yard names it, under the home as it is given
	// (what HomeStageFile and yardr flow show print), and disk the same
	// file under the home's real path, which is what is read and written;
	// root is the flows directory there, where the walk down to the file
	// begins.
	path, disk, root string
	// digest is stageNone or the SHA-256 of the file's bytes.
	digest string
	// mode is what the file is kept at when it is replaced: its own where
	// one stands, rw-r--r-- where none does.
	mode fs.FileMode
	// text is what the file holds, "" where none stands.
	text string
}

// standStage reads what stands at the stage file of stage in the store flow
// flow under home, both already checked as names. A symbolic link or a file
// where a directory should be, anywhere from the flows directory down, a
// symbolic link or anything but a regular file at the stage file itself, and
// a file the yard cannot read are refused: the write that follows would
// otherwise land where the person did not mean. Nothing at all, or nothing
// but a missing directory, is no refusal: the file is created.
func standStage(home, flow, stage string) (*stageStand, error) {
	rel, err := stageRel(stage)
	if err != nil {
		return nil, err
	} else if home == "" {
		return nil, errNoStageHome
	}
	subject := fmt.Sprintf("stage file of %s in flow %s", stage, flow)
	named, real, err := homeDirsAt(home, subject)
	if err != nil {
		return nil, err
	}
	st := &stageStand{
		path:   filepath.Join(HomeStageDir(named, flow), rel),
		disk:   filepath.Join(HomeStageDir(real, flow), rel),
		root:   FlowsDir(real),
		digest: stageNone,
		mode:   0o644,
	}
	have, err := stageDirs(st.root, filepath.Dir(st.disk), false)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", subject, err)
	}
	if !have {
		return st, nil
	}
	// Opened without following a symbolic link and without blocking, and
	// judged by the open file, as promptStands reads one.
	f, err := os.OpenFile(st.disk, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return st, nil
	case errors.Is(err, syscall.ELOOP):
		return nil, fmt.Errorf("%s: %s is a symbolic link: the yard writes a stage file only where a plain file stands", subject, st.path)
	case err != nil:
		return nil, fmt.Errorf("%s: %s is not a file the yard can use: %s", subject, st.path, OneLine(err.Error()))
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: %s is not a regular file", subject, st.path)
	}
	raw, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %s: %s", subject, st.path, OneLine(err.Error()))
	}
	st.digest, st.mode, st.text = stageDigest(raw), info.Mode().Perm(), string(raw)
	return st, nil
}

// stageDirs walks the directories from root, the flows directory, down to
// dir, each checked without following a link: one that is a symbolic link or
// no directory is refused. It answers whether dir stands; with create the
// ones that do not are made, so that it answers true or an error.
func stageDirs(root, dir string, create bool) (bool, error) {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return false, err
	}
	parts := []string{}
	if rel != "." {
		parts = strings.Split(rel, string(filepath.Separator))
	}
	cur := root
	for i := 0; ; i++ {
		info, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) && create {
			if err = os.Mkdir(cur, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
				return false, err
			}
			info, err = os.Lstat(cur)
		}
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return false, nil
		case err != nil:
			return false, err
		case info.Mode()&fs.ModeSymlink != 0:
			return false, fmt.Errorf("%s is a symbolic link: the yard writes a stage file only under directories that are plain ones", cur)
		case !info.IsDir():
			return false, fmt.Errorf("%s is not a directory", cur)
		}
		if i == len(parts) {
			return true, nil
		}
		cur = filepath.Join(cur, parts[i])
	}
}

// held says why the file as it stands is not the one expect names: expect
// is "" for a write that holds itself to none, stageNone for one that
// creates the file, or the digest of the file it replaces. "" where it is.
func (st *stageStand) held(expect string) error {
	switch {
	case expect == "":
	case expect == stageNone && st.digest != stageNone:
		return fmt.Errorf("the stage file %s stands (sha256 %s), though --digest %s says none does: it appeared since it was read", st.path, st.digest, stageNone)
	case expect != stageNone && st.digest == stageNone:
		return fmt.Errorf("the stage file %s does not stand (or holds nothing), though --digest %s names the text of one: it was removed or emptied since it was read", st.path, expect)
	case expect != stageNone && st.digest != expect:
		return fmt.Errorf("the stage file %s holds sha256 %s, not the %s that --digest names: it changed since it was read", st.path, st.digest, expect)
	}
	return nil
}

// write puts text where the stage file stands, whole or not at all: it is
// written to a file of the same directory, set to the mode the stage file is
// kept at, and renamed over it, so a reader sees the old file or the new and
// never half of one. The directories that are missing are made.
func (st *stageStand) write(text string) (err error) {
	dir := filepath.Dir(st.disk)
	if _, err := stageDirs(st.root, dir, true); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".yardr-stage-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if _, err = f.WriteString(text); err != nil {
		return err
	}
	if err = f.Chmod(st.mode); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, st.disk)
}
