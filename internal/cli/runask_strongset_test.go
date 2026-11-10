package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/store"
)

// bead ask --run "group set ..." and "crew set ..." carry the text of the
// file their prompt= names beside the ask, as group add does: prime shows the
// weighty card whole with what stands now beside what will, the approval is
// refused while the file holds other text, and the weighed approval sets the
// file the yard writes from that text and the flags of the preset. The
// yardmaster's own entry is proposed the same way.
func TestBeadAskRunSetsThePromptAndPreset(t *testing.T) {
	t.Parallel()
	key := airyKey(t)
	y, b := runYardCLI(t, "strongset", key)
	ctx := context.Background()
	// The store's home, as its blobs say: the prompts stand under it.
	home := filepath.Dir(filepath.Dir(y.own.BlobPath("x")))
	if err := os.MkdirAll(filepath.Join(home, "roles"), 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name, text string) (file, saved string) {
		t.Helper()
		file = filepath.Join(home, "roles", name+".md")
		if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		saved, err := filepath.EvalSymlinks(file)
		if err != nil {
			t.Fatal(err)
		}
		return file, saved
	}
	const oldPrompt, newPrompt, ownPrompt = "You cook.\n", "You cook with care.\nTaste before you serve.\n", "You run the yard.\nAsk before you spend.\n"
	_, oldSaved := write("cooks-old", oldPrompt)
	file, _ := write("cooks", newPrompt)
	ownFile, _ := write("yardmaster", ownPrompt)
	yardFile, ownYard := yardPromptFile(t, home, "cooks", newPrompt), yardPromptFile(t, home, "yardmaster", ownPrompt)
	if err := y.st.PutGroup(ctx, store.Group{Name: "cooks", Runner: "herdr", Config: map[string]any{"kind": "claude", "prompt": oldSaved}}, "jo"); err != nil {
		t.Fatal(err)
	}
	command := "group set cooks prompt=" + file + " preset=bypass"
	out, err := y.as(t, "yardmaster", "ask", b.ID, "Change the cooks?", "--context", "the cooks need a new brief", "--run", command)
	if err != nil {
		t.Fatal(err)
	}
	if want := "ask 1 on " + b.ID + " for jo: to approve, yardr " + command + "\n"; out != want {
		t.Fatalf("bead ask --run group set printed %q, want %q", out, want)
	}
	// The yardmaster proposes a change of its own entry.
	ownCommand := "crew set yardmaster prompt=" + ownFile + " preset=bypass"
	if _, err := y.as(t, "yardmaster", "ask", b.ID, "Change my own brief?", "--context", "I would run the yard better", "--run", ownCommand); err != nil {
		t.Fatal(err)
	}
	shown, err := run(t, y.a.primeCmd(), "--bead", b.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"WEIGHTY · code:",
		"changes the group cooks on the herdr runner",
		"Its prompt is the file " + oldSaved + ", which each session reads when it starts, and which holds now:",
		"You cook.",
		"On approval the yard writes the text below to the file " + yardFile + ", read-only.",
		"Taste before you serve.",
		`It will start them with "--dangerously-skip-permissions": the bypass preset`,
		"changes the crew member yardmaster",
		"Ask before you spend.",
	} {
		if !strings.Contains(shown, want) {
			t.Errorf("prime lacks %q:\n%s", want, shown)
		}
	}
	// A file that holds other text by the approval is refused, and nothing
	// is set.
	if err := os.WriteFile(file, []byte("changed after the ask\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := y.as(t, "jo", "answer", b.ID, "1", "--approve"); err == nil || !strings.Contains(err.Error(), "holds other text than the ask carries") {
		t.Fatalf("approval over a changed prompt = %v, want it refused", err)
	}
	if g, err := y.st.GetGroup(ctx, "cooks"); err != nil || g.Config["prompt"] != oldSaved || g.Config["args"] != nil {
		t.Fatalf("a refused approval changed the group: %+v, %v", g, err)
	}
	if err := os.WriteFile(file, []byte(newPrompt), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err = y.as(t, "jo", "answer", b.ID, "1", "--approve"); err != nil || !strings.Contains(out, "group cooks saved") {
		t.Fatalf("the approval printed %q, %v", out, err)
	}
	g, err := y.st.GetGroup(ctx, "cooks")
	if err != nil {
		t.Fatal(err)
	}
	if g.Config["prompt"] != yardFile || fmt.Sprint(g.Config["args"]) != "[--dangerously-skip-permissions]" || g.Config["kind"] != "claude" {
		t.Fatalf("the group holds %+v", g)
	}
	if out, err = y.as(t, "jo", "answer", b.ID, "2", "--approve"); err != nil || !strings.Contains(out, "crew member yardmaster saved") {
		t.Fatalf("the approval of the yardmaster's own entry printed %q, %v", out, err)
	}
	m, err := y.st.GetCrew(ctx, "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	if m.Config["prompt"] != ownYard || fmt.Sprint(m.Config["args"]) != "[--dangerously-skip-permissions]" {
		t.Fatalf("the yardmaster holds %+v", m)
	}

	// A file that cannot be read, or is no text, is refused before anything
	// is stored, and --run-role is a crew add's.
	invalid := filepath.Join(home, "roles", "invalid.md")
	if err := os.WriteFile(invalid, []byte{0xff}, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ command, want string }{
		{"group set cooks prompt=" + filepath.Join(home, "roles", "gone.md"), "no such file"},
		{"crew set yardmaster prompt=" + invalid, "not valid UTF-8"},
		{"crew set yardmaster prompt=" + filepath.Join(home, "roles"), "not a regular file"},
	} {
		if _, err := y.as(t, "yardmaster", "ask", b.ID, "Change it?", "--context", "it needs changing", "--run", c.command); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s = %v, want it refused saying %q", c.command, err, c.want)
		}
	}
	if _, err := y.as(t, "yardmaster", "ask", b.ID, "Change it?", "--context", "it needs changing",
		"--run", "group set cooks prompt="+file, "--run-role", file); err == nil || !strings.Contains(err.Error(), "--run-role is the role file the run ask crew add") {
		t.Errorf("group set with --run-role = %v, want it refused", err)
	}
	// A command with a preset alone carries no file.
	if _, err := y.as(t, "yardmaster", "ask", b.ID, "Change it?", "--context", "it needs changing", "--run", "group set cooks preset=bypass"); err == nil ||
		!strings.Contains(err.Error(), "group cooks is set so already") {
		t.Errorf("group set of a preset it has = %v", err)
	}
	// An effort is a word of no quotes, so the card carries it where the
	// flags of args never could, and it weighs nothing (yardr-jkcy): the
	// yardmaster proposes it, the operator approves it, and the group's
	// sessions are given the effort. The yardmaster cannot set it itself.
	out, err = y.as(t, "yardmaster", "ask", b.ID, "Think harder?", "--context", "the cooks cut corners", "--run", "group set cooks effort=max")
	if err != nil {
		t.Fatal(err)
	}
	var seq int
	if _, err := fmt.Sscanf(out, "ask %d on", &seq); err != nil || !strings.Contains(out, "to approve, yardr group set cooks effort=max\n") {
		t.Fatalf("bead ask --run group set effort printed %q, %v", out, err)
	}
	if _, err := y.st.SetGroup(ctx, "cooks", store.GroupEdit{Set: map[string]any{"effort": "max"}}, "yardmaster"); err == nil || !strings.Contains(err.Error(), "only the operator") {
		t.Errorf("the yardmaster set the effort itself: %v", err)
	}
	if out, err = y.as(t, "jo", "answer", b.ID, fmt.Sprint(seq), "--approve"); err != nil || !strings.Contains(out, "group cooks saved") || !strings.Contains(out, "effort: (not set) -> max") {
		t.Fatalf("the approval of the effort printed %q, %v", out, err)
	}
	if g, err := y.st.GetGroup(ctx, "cooks"); err != nil || g.Config["effort"] != "max" || g.Config["prompt"] != yardFile {
		t.Fatalf("the group holds %+v, %v", g, err)
	}
}
