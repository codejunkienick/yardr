package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// historyPath is where the yard keeps the text it replaced that is text.
func (y *askYard) historyPath(text string) string {
	return StageHistoryPath(y.home, sha256Hex(text))
}

// historyIs holds the stage history to keep text whole, as one plain file
// named by its digest, read-only.
func (y *askYard) historyIs(t *testing.T, text string) {
	t.Helper()
	p := y.historyPath(text)
	info, err := os.Lstat(p)
	if err != nil {
		t.Errorf("the stage history keeps no %q: %v", text, err)
		return
	}
	if info.Mode() != 0o400 {
		t.Errorf("the stage history keeps %q as %v, want a plain file at -r--------", text, info.Mode())
	}
	if got, err := os.ReadFile(p); err != nil || string(got) != text {
		t.Errorf("the stage history keeps %q holding %q, %v", text, got, err)
	}
}

// historyNames are the names in the stage history, sorted, none where it does
// not stand. Every one is a plain file: the history is flat.
func (y *askYard) historyNames(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(StageHistoryDir(y.home))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if !e.Type().IsRegular() {
			t.Errorf("the stage history holds %s, which is no plain file", e.Name())
		}
		out = append(out, e.Name())
	}
	return out
}

// historyHolds holds the stage history to exactly the texts, each under its
// digest and no other.
func (y *askYard) historyHolds(t *testing.T, texts ...string) {
	t.Helper()
	var want []string
	for _, text := range texts {
		want = append(want, sha256Hex(text))
	}
	slices.Sort(want)
	if got := y.historyNames(t); !slices.Equal(got, want) {
		t.Errorf("the stage history holds %v, want %v", got, want)
	}
}

// sha8 is the first eight digits of the digest of text, as stageWrites shows
// a digest.
func sha8(text string) string { return sha256Hex(text)[:8] }

// stageWrites is each flow_set of a stage file, oldest first, as "<stage>
// <digest> <- <prev_digest>" with the digests in their first eight digits,
// and "-" where the write replaced nothing.
func (y *askYard) stageWrites(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, e := range y.stageEvents(t) {
		digest, _ := e.Data["digest"].(string)
		prev, _ := e.Data["prev_digest"].(string)
		if prev == "" {
			prev = "-"
		}
		out = append(out, fmt.Sprintf("%v %.8s <- %.8s", e.Data["stage"], digest, prev))
	}
	return out
}

// A stage history file is named by a digest and nothing else: no name that
// leads out of the directory, a digest in capitals, or none.
func TestStageHistoryPath(t *testing.T) {
	t.Parallel()
	good := sha256Hex("Build it.\n")
	if got, want := StageHistoryPath("/home", good), filepath.Join("/home", "stage-history", good); got != want {
		t.Errorf("StageHistoryPath = %q, want %q", got, want)
	}
	if got, want := StageHistoryDir("/home"), filepath.Join("/home", "stage-history"); got != want {
		t.Errorf("StageHistoryDir = %q, want %q", got, want)
	}
	for _, bad := range []string{"", stageNone, strings.ToUpper(good), good[:63], good + "0", "../" + good[3:], "a/" + good[2:], strings.Repeat("g", 64), "..", "."} {
		if got := StageHistoryPath("/home", bad); got != "" {
			t.Errorf("StageHistoryPath(%q) = %q, want none", bad, got)
		}
	}
}

// A save that replaces a file with text in it keeps that text in the one flat
// directory of the stage history, read-only and named by its digest, before
// the file is replaced; the event says which text was written and which was
// replaced, by digest alone; and the answer carries both. A file that was
// created, or replaced one that stood as none, leaves nothing to keep, and the
// history is neither a stage file nor anything Prune sweeps.
func TestPutStageFileKeepsWhatItReplaces(t *testing.T) {
	t.Parallel()
	y := stageYard(t)
	v1, v2, v3 := "One.\n", "Two.\n", "Three.\n"
	// save puts text in stage, and holds the answer to the path and text it
	// wrote, its digest and the text it replaced.
	save := func(stage, text, digest, replaced string) *LineStageFile {
		t.Helper()
		got, err := y.PutStageFile(y.ctx, "code", stage, text, digest, "mckean", true)
		if err != nil {
			t.Fatal(err)
		}
		want := LineStageFile{Path: y.stagePath(stage), Text: text, Digest: sha256Hex(text)}
		if replaced != "" {
			want.PriorDigest, want.PriorText = sha256Hex(replaced), replaced
		}
		if *got != want {
			t.Errorf("saving %q answered %+v, want %+v", text, *got, want)
		}
		return got
	}

	// Created where none stands: nothing to keep, and no directory made for it.
	save("work", v1, stageNone, "")
	if _, err := os.Lstat(StageHistoryDir(y.home)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a save that replaced nothing made the stage history: %v", err)
	}
	// Replaced, over the digest it was read by, or over whatever stands.
	save("work", v2, sha256Hex(v1), v1)
	y.historyIs(t, v1)
	save("work", v3, "", v2)
	y.historyHolds(t, v1, v2)
	// A text put back is replaced in turn, and the file that stands for it
	// is kept as it stands: one file for one text, whoever asks.
	save("work", v1, sha256Hex(v3), v3)
	y.historyHolds(t, v1, v2, v3)
	save("work", v2, "", v1)
	y.historyHolds(t, v1, v2, v3)
	// A file that holds the text already is left as it is, and replaces none.
	save("work", v2, "", "")
	y.stageIs(t, "work", v2, 0o644)
	y.historyHolds(t, v1, v2, v3)

	// A stage whose name holds a slash has its file a directory deeper, and its
	// history in the same flat directory as any other.
	save("qa/smoke", "Smoke.\n", stageNone, "")
	save("qa/smoke", "Smoked.\n", "", "Smoke.\n")
	y.historyIs(t, "Smoke.\n")
	y.historyHolds(t, v1, v2, v3, "Smoke.\n")

	// A file with nothing in it stands as none: its write keeps nothing.
	y.putStage(t, "in", " \n\t\n", 0o640)
	save("in", v1, stageNone, "")
	y.stageIs(t, "in", v1, 0o640)
	y.historyHolds(t, v1, v2, v3, "Smoke.\n")

	if want := []string{
		"work " + sha8(v1) + " <- -", "work " + sha8(v2) + " <- " + sha8(v1), "work " + sha8(v3) + " <- " + sha8(v2),
		"work " + sha8(v1) + " <- " + sha8(v3), "work " + sha8(v2) + " <- " + sha8(v1), "work " + sha8(v2) + " <- -",
		"qa/smoke " + sha8("Smoke.\n") + " <- -", "qa/smoke " + sha8("Smoked.\n") + " <- " + sha8("Smoke.\n"),
		"in " + sha8(v1) + " <- -",
	}; !slices.Equal(y.stageWrites(t), want) {
		t.Errorf("the writes recorded\n%q\nwant\n%q", y.stageWrites(t), want)
	}
	for _, e := range y.stageEvents(t) {
		for k, v := range e.Data {
			if s, ok := v.(string); ok && strings.Contains(s, "One.") {
				t.Errorf("flow_set holds the text of a stage file in %s: %q", k, s)
			}
		}
	}

	// The history is no stage file of the flow, and no sweep of Prune takes it,
	// though one takes an orphan of blobs/ with it.
	f, err := y.GetFlow(y.ctx, "code")
	if err != nil {
		t.Fatal(err)
	}
	if have, stray, err := HomeStageFiles(y.home, f); err != nil || len(stray) != 0 || len(have) != 3 {
		t.Errorf("HomeStageFiles = %v, %v, %v, want the stage files of work, qa/smoke and in, and no stray", have, stray, err)
	}
	orphan := filepath.Join(y.home, BlobsDir, "orphan")
	if err := os.MkdirAll(filepath.Dir(orphan), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orphan, []byte("Nobody names me.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := now().Add(-2 * BlobGrace)
	if err := os.Chtimes(orphan, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := y.Prune(y.ctx, Retention{}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(orphan); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Prune left the orphan of blobs/: %v", err)
	}
	y.historyHolds(t, v1, v2, v3, "Smoke.\n")
}

// A write that would replace a text it cannot keep is refused, and replaces
// nothing: the name its digest gives holds a link, something that is no plain
// file or other text than the name says, or the directory itself is a link or
// no directory. Nothing is written through a link, and once what stands in the
// way is gone the same save goes through.
func TestPutStageFileIsRefusedWhereItCannotKeepWhatItReplaces(t *testing.T) {
	t.Parallel()
	v1, v2 := "One.\n", "Two.\n"
	for _, c := range []struct {
		name, want string
		// plant puts the obstacle in place and returns where it stands.
		plant func(t *testing.T, y *askYard, other string) string
	}{
		{"a link at the name", "is a symbolic link", func(t *testing.T, y *askYard, other string) string {
			if err := os.MkdirAll(StageHistoryDir(y.home), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(other, "decoy"), y.historyPath(v1)); err != nil {
				t.Fatal(err)
			}
			return y.historyPath(v1)
		}},
		{"a directory at the name", "is not a regular file", func(t *testing.T, y *askYard, other string) string {
			if err := os.MkdirAll(y.historyPath(v1), 0o755); err != nil {
				t.Fatal(err)
			}
			return y.historyPath(v1)
		}},
		{"a pipe at the name", "is not a regular file", func(t *testing.T, y *askYard, other string) string {
			if err := os.MkdirAll(StageHistoryDir(y.home), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(y.historyPath(v1), 0o600); err != nil {
				t.Fatal(err)
			}
			return y.historyPath(v1)
		}},
		{"other text at the name", "holds other text than its name says", func(t *testing.T, y *askYard, other string) string {
			if err := os.MkdirAll(StageHistoryDir(y.home), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(y.historyPath(v1), []byte("Else.\n"), 0o400); err != nil {
				t.Fatal(err)
			}
			return y.historyPath(v1)
		}},
		{"a longer text at the name", "holds other text than its name says", func(t *testing.T, y *askYard, other string) string {
			if err := os.MkdirAll(StageHistoryDir(y.home), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(y.historyPath(v1), []byte(v1+"More.\n"), 0o400); err != nil {
				t.Fatal(err)
			}
			return y.historyPath(v1)
		}},
		{"a link for the directory", "is a symbolic link", func(t *testing.T, y *askYard, other string) string {
			if err := os.Symlink(other, StageHistoryDir(y.home)); err != nil {
				t.Fatal(err)
			}
			return StageHistoryDir(y.home)
		}},
		{"a file for the directory", "is not a directory", func(t *testing.T, y *askYard, other string) string {
			if err := os.WriteFile(StageHistoryDir(y.home), []byte("a file\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			return StageHistoryDir(y.home)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			y := stageYard(t)
			if _, err := y.PutStageFile(y.ctx, "code", "work", v1, stageNone, "mckean", true); err != nil {
				t.Fatal(err)
			}
			writes := y.stageWrites(t)
			other := t.TempDir()
			if err := os.WriteFile(filepath.Join(other, "decoy"), []byte("Not here.\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			stands := c.plant(t, y, other)

			_, err := y.PutStageFile(y.ctx, "code", "work", v2, "", "mckean", true)
			if err == nil || !strings.Contains(err.Error(), "keep the text it replaces") || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("PutStageFile = %v, want it refused, with %q", err, c.want)
			}
			// The same through a card, whose approval is the write.
			seq := y.ask(t, stageAsk(v2, "code", "work"), "yardmaster")
			if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil || !strings.Contains(err.Error(), "keep the text it replaces") {
				t.Errorf("ApproveAsk = %v, want it refused to keep the text it replaces", err)
			}
			y.stageIs(t, "work", v1, 0o644)
			if got := y.stageWrites(t); !slices.Equal(got, writes) {
				t.Errorf("the refused writes left the events %q, want %q", got, writes)
			}
			if left, _ := filepath.Glob(filepath.Join(StageHistoryDir(y.home), ".yardr-history-*")); len(left) > 0 {
				t.Errorf("the refused writes left %v", left)
			}
			if names, _ := os.ReadDir(other); len(names) != 1 {
				t.Errorf("something was written through the link: %v", names)
			} else if got, _ := os.ReadFile(filepath.Join(other, "decoy")); string(got) != "Not here.\n" {
				t.Errorf("the file the link names holds %q", got)
			}

			if err := os.RemoveAll(stands); err != nil {
				t.Fatal(err)
			}
			if _, err := y.PutStageFile(y.ctx, "code", "work", v2, "", "mckean", true); err != nil {
				t.Fatalf("PutStageFile once nothing stands in the way = %v", err)
			}
			y.historyIs(t, v1)
		})
	}
}

// The helper keeps what it is given whole, read-only, and as one file for one
// text; it refuses what stands at the name unless that is exactly the text, and
// a digest that is no name of the history.
func TestArchiveStageFile(t *testing.T) {
	t.Parallel()
	raw := []byte("Kept.\n")
	digest := sha256Hex(string(raw))
	real := func(t *testing.T) string {
		t.Helper()
		home, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return home
	}
	home := real(t)
	if err := archiveStageFile(home, digest, raw); err != nil {
		t.Fatal(err)
	}
	path := StageHistoryPath(home, digest)
	if info, err := os.Lstat(path); err != nil || info.Mode() != 0o400 {
		t.Errorf("the kept file is %v, %v, want a plain file at -r--------", info, err)
	}
	if info, err := os.Lstat(filepath.Dir(path)); err != nil || !info.IsDir() {
		t.Errorf("the history directory is %v, %v, want a plain directory", info, err)
	}
	// The same text again keeps the file that stands, whatever mode it has.
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := archiveStageFile(home, digest, raw); err != nil {
		t.Errorf("keeping text kept already: %v", err)
	}
	if info, _ := os.Lstat(path); info == nil || info.Mode() != 0o600 {
		t.Errorf("keeping text kept already changed the file to %v", info)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".yardr-history-*")); len(left) > 0 {
		t.Errorf("keeping left %v", left)
	}
	for _, bad := range []string{"", stageNone, strings.ToUpper(digest), "../" + digest[3:]} {
		if err := archiveStageFile(home, bad, raw); err == nil || !strings.Contains(err.Error(), "is no SHA-256 of a stage file's text") {
			t.Errorf("archiveStageFile(%q) = %v, want it refused", bad, err)
		}
	}
}

// What the history reads back is the text its name says, and anything else is
// no text: a link, a file that is no plain one, a rewrite or a truncation, bytes
// that are no text, a text longer than a card carries, a link for the
// directory, and a name that is no digest. The read does without it and fails
// nothing.
func TestReadStageHistory(t *testing.T) {
	t.Parallel()
	text := "Kept.\n"
	digest := sha256Hex(text)
	long := strings.Repeat("x", MaxRunFlow+1)
	noText := "Kept \xff.\n"
	// Each case builds what stands in dir, an empty stage history directory,
	// and names the digest that is read.
	for _, c := range []struct {
		name  string
		build func(t *testing.T, dir string) string
		want  bool
	}{
		{"kept", func(t *testing.T, dir string) string { writeHistory(t, dir, digest, text); return digest }, true},
		{"none kept", func(t *testing.T, dir string) string { return digest }, false},
		{"no directory", func(t *testing.T, dir string) string { os.Remove(dir); return digest }, false},
		{"a link", func(t *testing.T, dir string) string {
			other := filepath.Join(t.TempDir(), "other")
			if err := os.WriteFile(other, []byte(text), 0o400); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other, filepath.Join(dir, digest)); err != nil {
				t.Fatal(err)
			}
			return digest
		}, false},
		{"a directory", func(t *testing.T, dir string) string {
			if err := os.Mkdir(filepath.Join(dir, digest), 0o755); err != nil {
				t.Fatal(err)
			}
			return digest
		}, false},
		{"a pipe", func(t *testing.T, dir string) string {
			if err := syscall.Mkfifo(filepath.Join(dir, digest), 0o600); err != nil {
				t.Fatal(err)
			}
			return digest
		}, false},
		{"rewritten", func(t *testing.T, dir string) string { writeHistory(t, dir, digest, "Forged.\n"); return digest }, false},
		{"truncated", func(t *testing.T, dir string) string { writeHistory(t, dir, digest, "Kept."); return digest }, false},
		{"emptied", func(t *testing.T, dir string) string { writeHistory(t, dir, digest, ""); return digest }, false},
		{"no text", func(t *testing.T, dir string) string {
			writeHistory(t, dir, sha256Hex(noText), noText)
			return sha256Hex(noText)
		}, false},
		{"longer than a card carries", func(t *testing.T, dir string) string {
			writeHistory(t, dir, sha256Hex(long), long)
			return sha256Hex(long)
		}, false},
		{"as long as a card carries", func(t *testing.T, dir string) string {
			full := long[:MaxRunFlow]
			writeHistory(t, dir, sha256Hex(full), full)
			return sha256Hex(full)
		}, true},
		{"a link for the directory", func(t *testing.T, dir string) string {
			os.Remove(dir)
			elsewhere := t.TempDir()
			writeHistory(t, elsewhere, digest, text)
			if err := os.Symlink(elsewhere, dir); err != nil {
				t.Fatal(err)
			}
			return digest
		}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			dir := StageHistoryDir(home)
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			read := c.build(t, dir)
			got, ok := readStageHistory(home, read)
			if ok != c.want || (ok && sha256Hex(got) != read) || (!ok && got != "") {
				t.Errorf("readStageHistory = %.20q, %v, want a text: %v", got, ok, c.want)
			}
		})
	}
	if got, ok := readStageHistory("", digest); ok || got != "" {
		t.Errorf("readStageHistory of no home = %q, %v", got, ok)
	}
	for _, bad := range []string{"", stageNone, strings.ToUpper(digest), "../" + digest[3:]} {
		home := t.TempDir()
		writeHistory(t, StageHistoryDir(home), digest, text)
		if got, ok := readStageHistory(home, bad); ok || got != "" {
			t.Errorf("readStageHistory(%q) = %q, %v", bad, got, ok)
		}
	}
}

// writeHistory makes the stage history dir where it is missing and puts a
// file named name in it holding text, read-only.
func writeHistory(t *testing.T, dir, name, text string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o400); err != nil {
		t.Fatal(err)
	}
}

// The Line carries a store flow's stage file as a save answers with it: the
// digest it is saved by, the text the latest save replaced where that is kept,
// and a broken ## Ask section as a warning, which a save is never refused for.
// A prior that is gone, a link, rewritten, or the text that stands is left
// out, and the read goes on.
func TestLineStageFileOfAStoreFlow(t *testing.T) {
	t.Parallel()
	y := stageYard(t)
	if err := y.PutDepot(y.ctx, Depot{Name: "web"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if err := y.SetFlowPointer(y.ctx, "web", "", "code", "mckean"); err != nil {
		t.Fatal(err)
	}
	// line is the stage file of work as the Line reads it.
	line := func() *LineStageFile {
		t.Helper()
		l, err := y.Line(y.ctx, "web")
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range l.Flows {
			for _, s := range f.Stages {
				if f.Name == "code" && s.Stage == "work" {
					return s.StageFile
				}
			}
		}
		t.Fatal("the Line has no stage work of the flow code")
		return nil
	}
	save := func(text, digest string) *LineStageFile {
		t.Helper()
		sf, err := y.PutStageFile(y.ctx, "code", "work", text, digest, "mckean", true)
		if err != nil {
			t.Fatal(err)
		}
		return sf
	}
	same := func(what string, got, want *LineStageFile) {
		t.Helper()
		if got == nil || *got != *want {
			t.Errorf("%s: the Line says %+v, the save said %+v", what, got, want)
		}
	}
	v1, v2 := "One.\n", "Two.\n"
	if sf := line(); sf != nil {
		t.Fatalf("a stage with no file has %+v", sf)
	}
	created := save(v1, stageNone)
	same("a file created", line(), created)
	if sf := line(); sf.Digest != sha256Hex(v1) || sf.PriorDigest != "" || sf.PriorText != "" || sf.Warning != "" {
		t.Errorf("a file created says %+v, want its digest alone", sf)
	}
	replaced := save(v2, sha256Hex(v1))
	same("a file replaced", line(), replaced)
	if sf := line(); sf.PriorDigest != sha256Hex(v1) || sf.PriorText != v1 {
		t.Errorf("a file replaced says %+v, want the text it replaced", sf)
	}
	// Unrelated flow_set events do not hide the stage's prior when its save is
	// older than the bounded event lookup used for refusal messages.
	if err := y.write(func(w *wtx) error {
		for range 201 {
			if err := w.event("", "flow_set", "mckean", map[string]any{"flow": "other", "stage": "work"}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if sf := line(); sf.PriorDigest != sha256Hex(v1) || sf.PriorText != v1 {
		t.Errorf("after unrelated flow writes the Line says %+v, want the prior still", sf)
	}
	// A save that changed nothing replaced nothing, and hides nothing.
	if sf := save(v2, ""); sf.PriorDigest != "" || sf.PriorText != "" {
		t.Errorf("a save of the same text answered with a prior: %+v", sf)
	}
	if sf := line(); sf.PriorDigest != sha256Hex(v1) || sf.PriorText != v1 {
		t.Errorf("after a save of the same text the Line says %+v, want the prior still", sf)
	}

	// What is kept is read as it stands: a text that is not what its name says,
	// a link, a text that is gone, are left out, and the rest is read.
	kept := y.historyPath(v1)
	for _, c := range []struct {
		name  string
		spoil func()
		mend  func()
	}{
		{"rewritten", func() {
			if err := os.Chmod(kept, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(kept, []byte("Forged.\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, func() {
			if err := os.WriteFile(kept, []byte(v1), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(kept, 0o400); err != nil {
				t.Fatal(err)
			}
		}},
		{"gone", func() { os.Remove(kept) }, func() { writeHistory(t, filepath.Dir(kept), filepath.Base(kept), v1) }},
		{"a link", func() {
			os.Remove(kept)
			other := filepath.Join(t.TempDir(), "other")
			if err := os.WriteFile(other, []byte(v1), 0o400); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other, kept); err != nil {
				t.Fatal(err)
			}
		}, func() {
			os.Remove(kept)
			writeHistory(t, filepath.Dir(kept), filepath.Base(kept), v1)
		}},
	} {
		c.spoil()
		if sf := line(); sf == nil || sf.Text != v2 || sf.Digest != sha256Hex(v2) || sf.PriorDigest != "" || sf.PriorText != "" {
			t.Errorf("with the kept text %s the Line says %+v, want the file and no prior", c.name, sf)
		}
		c.mend()
		if sf := line(); sf == nil || sf.PriorText != v1 {
			t.Errorf("with the kept text mended after %s the Line says %+v, want the prior again", c.name, sf)
		}
	}

	// A file put back by hand as the text the latest save replaced has no prior
	// before it to offer.
	y.putStage(t, "work", v1, 0o644)
	if sf := line(); sf.Digest != sha256Hex(v1) || sf.PriorDigest != "" || sf.PriorText != "" {
		t.Errorf("a file put back by hand says %+v, want no prior", sf)
	}

	// A broken ## Ask section is warned of beside the file, in the save's
	// answer and in the Line, and refuses nothing.
	broken := "Build it.\n\n## Ask\nkind: choice\nquestion: Accept it?\ncontext:\n- Why.\ndone: Go.\nbacklog: No.\n"
	sf := save(broken, "")
	if !strings.HasPrefix(sf.Warning, y.stagePath("work")+": ask line ") {
		t.Errorf("the save of a broken ## Ask section warned %q", sf.Warning)
	}
	same("a broken ## Ask section", line(), sf)
	y.stageIs(t, "work", broken, 0o644)
	good := "Build it.\n\n## Ask\nkind: input\nquestion: What was decided?\ncontext:\n- The bead waits.\nmove: none\n"
	if sf := save(good, ""); sf.Warning != "" || line().Warning != "" {
		t.Errorf("a good ## Ask section warned %q", sf.Warning)
	}
}
