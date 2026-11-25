package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
)

// stageDigest is what --digest names a stage file by: the SHA-256 of its bytes.
func stageDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// stagePathIn is the path a stage file's write printed for flow/stage.
func stagePathIn(t *testing.T, out, flowStage string) string {
	t.Helper()
	_, rest, ok := strings.Cut(out, "stage file "+flowStage+": ")
	if !ok {
		t.Fatalf("no %q in:\n%s", "stage file "+flowStage+": ", out)
	}
	path, _, _ := strings.Cut(rest, "\n")
	return path
}

// flow set-stage writes the stage file of a stage of a store flow from a
// file or from stdin as it is, held to none or to the file's digest where
// it is told to, refuses what the store refuses in its words, warns of a
// broken ## Ask beside the result, and is the operator's alone.
func TestFlowSetStage(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := context.Background()
	f, err := store.ParseFlow("a -> b\nb -> qa/c\nqa/c -> d\nd terminal\n")
	if err != nil {
		t.Fatal(err)
	}
	f.Name = "code"
	if _, err := st.PutFlow(ctx, *f, "me"); err != nil {
		t.Fatal(err)
	}
	a := &app{st: st, actor: "me"}
	set := func(stdin string, args ...string) (stdout, stderr string, err error) {
		t.Helper()
		c := a.flowCmd()
		var errBuf bytes.Buffer
		c.SetErr(&errBuf)
		c.SetIn(strings.NewReader(stdin))
		out, err := run(t, c, append([]string{"set-stage"}, args...)...)
		return out, errBuf.String(), err
	}
	read := func(path string) string {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}

	// From a file: the path is printed, and the file is the text, at rw-r--r--.
	text := "Build it.\n"
	file := filepath.Join(t.TempDir(), "b.md")
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errs, err := set("", "code", "b", "-f", file)
	if err != nil || errs != "" {
		t.Fatalf("flow set-stage -f <file>: %v\n%s\n%s", err, out, errs)
	}
	path := stagePathIn(t, out, "code/b")
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o644 || read(path) != text {
		t.Fatalf("the stage file is %v, %v holding %q, want the text at rw-r--r--", info, err, read(path))
	}

	// From stdin as it is, trailing newlines too, held to the file's digest.
	next := "Build it, twice.\n\n\n"
	if _, _, err := set(next, "code", "b", "-f", "-", "--digest", strings.ToUpper(stageDigest(text))); err != nil {
		t.Fatalf("flow set-stage -f - --digest: %v", err)
	}
	if got := read(path); got != next {
		t.Errorf("the stage file holds %q, want stdin as it was, %q", got, next)
	}

	// What the store holds it to is refused in its words, and changes nothing.
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"code", "b", "-f", "-", "--digest", "none"}, "stands (sha256 " + stageDigest(next) + "), though --digest none says none does"},
		{[]string{"code", "b", "-f", "-", "--digest", stageDigest(text)}, "holds sha256 " + stageDigest(next) + ", not the " + stageDigest(text)},
		{[]string{"code", "b", "-f", "-", "--digest", "abc"}, `digest "abc"`},
		{[]string{"code", "e", "-f", "-"}, "flow code has no stage e; stages: a, b, qa/c, d"},
		{[]string{"ghost", "b", "-f", "-"}, "flow ghost: not found"},
		{[]string{"code", "b"}, "-f is required"},
		{[]string{"code"}, "accepts 2 arg(s), received 1"},
	} {
		if _, _, err := set("Other.\n", c.args...); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("flow set-stage %v = %v, want %q", c.args, err, c.want)
		}
	}
	if got := read(path); got != next {
		t.Errorf("a refused write left %q", got)
	}
	if _, _, err := set("Build \xff it.\n", "code", "b", "-f", "-"); err == nil || !strings.Contains(err.Error(), "is not valid UTF-8 text") {
		t.Errorf("flow set-stage of bytes that are no text = %v", err)
	}
	if _, _, err := set("", "code", "b", "-f", filepath.Join(t.TempDir(), "none.md")); err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Errorf("flow set-stage of a file that is not there = %v", err)
	}

	// A stage whose name holds a slash has its file a directory deeper; a
	// stage file that breaks its ## Ask is warned of, beside the result.
	broken := "Check it.\n\n## Ask\nkind: choice\nquestion: Accept it?\ncontext:\n- Why.\ndone: Go.\nbacklog: No.\n"
	out, errs, err = set(broken, "code", "qa/c", "-f", "-", "--digest", "none")
	if err != nil {
		t.Fatal(err)
	}
	if got := stagePathIn(t, out, "code/qa/c"); !strings.HasSuffix(got, filepath.Join("flows", "code", "qa", "c.md")) || read(got) != broken {
		t.Errorf("the stage file of qa/c is %s holding %q", got, read(got))
	}
	if want := "warning: stage file " + stagePathIn(t, out, "code/qa/c") + ": ask line "; !strings.HasPrefix(errs, want) {
		t.Errorf("stderr = %q, want it to begin %q", errs, want)
	}

	// --json is the stage file alone.
	a.json = true
	out, errs, err = set("Build it, thrice.\n", "code", "b", "-f", "-")
	a.json = false
	var got store.StageFile
	if err != nil || errs != "" || json.Unmarshal([]byte(out), &got) != nil || got.Path != path || got.Text != "Build it, thrice.\n" {
		t.Errorf("flow set-stage --json = %q, %q, %v: want the stage file alone", out, errs, err)
	}

	// The operator's alone, once the yard has one.
	if err := st.SetSetting(ctx, store.OperatorSetting, "jo", "jo"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := set("Other.\n", "code", "b", "-f", "-"); err == nil || !strings.Contains(err.Error(), "only the operator (jo) may change a flow's stages; this is me") {
		t.Errorf("flow set-stage as someone else = %v, want it the operator's alone", err)
	}
	if read(path) != "Build it, thrice.\n" {
		t.Errorf("a refused write left %q", read(path))
	}
	a = &app{st: st, actor: "jo"}
	if _, _, err := set("The operator's.\n", "code", "b", "-f", "-"); err != nil || read(path) != "The operator's.\n" {
		t.Errorf("flow set-stage as the operator = %v, holding %q", err, read(path))
	}
}

// bead ask --run flow set-stage carries the stage file's text from the
// file --run-flow names, also beside an ask on stdin: the card shows it whole
// and weighs code, and the approval writes exactly those bytes. A file the
// card could not show, a missing one and F: lines are refused where it is
// asked, naming the terminal command or the flag.
func TestBeadAskRunFlowSetStage(t *testing.T) {
	t.Parallel()
	key := airyKey(t)
	y, b := runYardCLI(t, "stage", key)
	ctx := context.Background()
	f, err := store.ParseFlow("in -> review\nreview -> done\ndone terminal\n")
	if err != nil {
		t.Fatal(err)
	}
	f.Name = "code"
	if _, err := y.st.PutFlow(ctx, *f, "jo"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	const text = "Review it.\n\nSay what is wrong.\n"
	file := write("review.md", []byte(text))

	// From the file: the card is shown whole, and the file changed after
	// the ask changes nothing.
	out, err := y.as(t, "yardmaster", "ask", b.ID, "Set the review stage file?", "--context", "the stage reads differently",
		"--run", "flow set-stage code review --digest NONE", "--run-flow", file)
	if err != nil {
		t.Fatal(err)
	}
	if want := "ask 1 on " + b.ID + " for jo: to approve, yardr flow set-stage code review --digest none\n"; out != want {
		t.Fatalf("bead ask --run flow set-stage printed %q, want %q", out, want)
	}
	shown, err := run(t, y.a.primeCmd(), "--bead", b.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"WEIGHTY · code:", "adds the stage file of stage review of the flow code, where none stands: ", "Say what is wrong."} {
		if !strings.Contains(shown, want) {
			t.Errorf("prime lacks %q:\n%s", want, shown)
		}
	}
	if err := os.WriteFile(file, []byte("changed after the ask\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err = y.as(t, "jo", "answer", b.ID, "1", "--approve")
	if err != nil {
		t.Fatal(err)
	}
	path := stagePathIn(t, out, "code/review")
	if got, err := os.ReadFile(path); err != nil || string(got) != text {
		t.Fatalf("the approval wrote %q, %v; want the text as asked, %q", got, err, text)
	}

	// Beside an ask on stdin, as a role file is, and held to the file that
	// stands: the text goes whole, a blank line at its end included.
	const next = "Review it twice.\n\n"
	other := write("next.md", []byte(next))
	y.a.actor = "yardmaster"
	c := y.a.beadCmd()
	c.SetIn(strings.NewReader("Q: Replace the review stage file?\nC: it says too little\nR: flow set-stage code review --digest " + stageDigest(text) + "\n"))
	out, err = run(t, c, "ask", b.ID, "-", "--run-flow", other)
	y.a.actor = "jo"
	if err != nil {
		t.Fatalf("an ask on stdin beside --run-flow: %v", err)
	}
	var seq int
	if _, err := fmt.Sscanf(out, "ask %d", &seq); err != nil {
		t.Fatalf("the ask on stdin printed %q: %v", out, err)
	}
	if _, err = y.as(t, "jo", "answer", b.ID, fmt.Sprint(seq), "--approve"); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != next {
		t.Fatalf("the second approval wrote %q, %v; want %q", got, err, next)
	}

	// Refused where it is asked.
	before := len(beadNotes(t, y, b.ID))
	long := write("long.md", bytes.Repeat([]byte("x"), store.MaxRunFlow+1))
	invalid := write("invalid.md", []byte{0xff})
	for _, c := range []struct {
		name  string
		stdin string
		args  []string
		want  string
	}{
		{"no file", "", []string{"--run", "flow set-stage code review"}, "carries its text beside the ask, in --run-flow <file>"},
		{"a file past the card", "", []string{"--run", "flow set-stage code review", "--run-flow", long}, "exceeds 32768 bytes: a run ask carries at most that, so set it with yardr flow set-stage in a terminal"},
		{"a file that is no text", "", []string{"--run", "flow set-stage code review", "--run-flow", invalid}, "is not valid UTF-8 text"},
		{"a directory", "", []string{"--run", "flow set-stage code review", "--run-flow", dir}, "the stage file is not a regular file"},
		{"a file that is not there", "", []string{"--run", "flow set-stage code review", "--run-flow", filepath.Join(dir, "none.md")}, "no such file"},
		{"a command that takes none", "", []string{"--run", "settings set max_sessions 20", "--run-flow", file}, "it goes with one of those commands"},
		{"F: lines", "Q: Set it?\nC: why.\nR: flow set-stage code review\nF: some text\n", []string{"-"}, "--run-flow-line is a line of the flow the run ask flow set <name> proposes"},
		{"a file and F: lines", "Q: Set it?\nC: why.\nR: flow set-stage code review\nF: some text\n", []string{"-", "--run-flow", file}, "one of the two"},
		{"a stage the flow lacks", "", []string{"--run", "flow set-stage code qa/smoke", "--run-flow", file}, "flow code has no stage qa/smoke; stages: in, review, done"},
	} {
		args := append([]string{"ask", b.ID}, c.args...)
		if c.stdin == "" {
			args = append([]string{"ask", b.ID, "Set it?", "--context", "why."}, c.args...)
		}
		y.a.actor = "yardmaster"
		c2 := y.a.beadCmd()
		c2.SetIn(strings.NewReader(c.stdin))
		_, err := run(t, c2, args...)
		y.a.actor = "jo"
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: ask = %v, want it refused with %q", c.name, err, c.want)
		}
	}
	if after := len(beadNotes(t, y, b.ID)); after != before {
		t.Errorf("the refused asks left %d notes", after-before)
	}
}

// beadNotes is the notes of the bead, for a test that holds a refused ask to
// leave none.
func beadNotes(t *testing.T, y *cliYard, id string) []store.Note {
	t.Helper()
	notes, err := y.st.Notes(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return notes
}
