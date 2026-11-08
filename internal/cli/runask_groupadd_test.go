package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bead ask --run "group add ..." carries the text of the file its --set
// prompt= names beside the ask: prime shows the weighty card whole, the
// approval is refused while the file holds other text, and the weighed
// approval adds the group with the path of that file.
func TestBeadAskRunGroupAdd(t *testing.T) {
	t.Parallel()
	key := airyKey(t)
	y, b := runYardCLI(t, "groupadd", key)
	ctx := context.Background()
	// The store's home, as its blobs say: the prompt stands under it.
	home := filepath.Dir(filepath.Dir(y.own.BlobPath("x")))
	if err := os.MkdirAll(filepath.Join(home, "roles"), 0o700); err != nil {
		t.Fatal(err)
	}
	const prompt = "You are a reviewer.\n\nRead the diff and say what is wrong with it.\nBring decisions to the operator.\n"
	file := filepath.Join(home, "roles", "reviewer.md")
	if err := os.WriteFile(file, []byte(prompt), 0o600); err != nil {
		t.Fatal(err)
	}
	saved, err := filepath.EvalSymlinks(file)
	if err != nil {
		t.Fatal(err)
	}
	command := "group add reviewers --runner herdr --limit 2 --set kind=claude --set args=--auto --set prompt=" + file
	out, err := y.as(t, "yardmaster", "ask", b.ID, "Add the reviewers?", "--context", "the yard needs reviewers", "--run", command)
	if err != nil {
		t.Fatal(err)
	}
	if want := "ask 1 on " + b.ID + " for jo: to approve, yardr group add reviewers --runner herdr --limit 2 --set kind=claude --set args=--auto --set prompt=" + file + "\n"; out != want {
		t.Fatalf("bead ask --run group add printed %q, want %q", out, want)
	}
	shown, err := run(t, y.a.primeCmd(), "--bead", b.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"WEIGHTY · code:",
		"adds the group reviewers on the herdr runner, with limit 2.",
		`It starts them with "--auto".`,
		"Its prompt is the file " + saved,
		"Read the diff and say what is wrong with it.",
	} {
		if !strings.Contains(shown, want) {
			t.Errorf("prime lacks %q:\n%s", want, shown)
		}
	}
	// A file that holds other text by the approval is refused, and nothing
	// is made.
	if err := os.WriteFile(file, []byte("changed after the ask\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := y.as(t, "jo", "answer", b.ID, "1", "--approve"); err == nil || !strings.Contains(err.Error(), "holds other text than the ask carries") {
		t.Fatalf("approval over a changed prompt = %v, want it refused", err)
	}
	if _, err := y.st.GetGroup(ctx, "reviewers"); err == nil {
		t.Fatal("a refused approval made the group")
	}
	if err := os.WriteFile(file, []byte(prompt), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err = y.as(t, "jo", "answer", b.ID, "1", "--approve")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "group reviewers (herdr · claude · limit 2)") {
		t.Fatalf("the approval printed:\n%s", out)
	}
	g, err := y.st.GetGroup(ctx, "reviewers")
	if err != nil {
		t.Fatal(err)
	}
	if g.Runner != "herdr" || g.Limit != 2 || g.Config["kind"] != "claude" || g.Config["args"] != "--auto" || g.Config["prompt"] != saved {
		t.Fatalf("the group holds %+v", g)
	}

	// A file that cannot be read, or is no text, is refused before anything
	// is stored, and --run-role is a crew add's.
	invalid := filepath.Join(home, "roles", "invalid.md")
	if err := os.WriteFile(invalid, []byte{0xff}, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ command, want string }{
		{"group add cooks --runner herdr --set kind=claude --set prompt=" + filepath.Join(home, "roles", "gone.md"), "no such file"},
		{"group add cooks --runner herdr --set kind=claude --set prompt=" + invalid, "not valid UTF-8"},
		{"group add cooks --runner herdr --set kind=claude --set prompt=" + filepath.Join(home, "roles"), "not a regular file"},
	} {
		if _, err := y.as(t, "yardmaster", "ask", b.ID, "Add the cooks?", "--context", "the yard needs cooks", "--run", c.command); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s = %v, want it refused saying %q", c.command, err, c.want)
		}
	}
	if _, err := y.as(t, "yardmaster", "ask", b.ID, "Add the cooks?", "--context", "the yard needs cooks",
		"--run", "group add cooks --runner herdr --set kind=claude --set prompt="+file, "--run-role", file); err == nil || !strings.Contains(err.Error(), "--run-role is the role file a --run \"crew add") {
		t.Errorf("group add with --run-role = %v, want it refused", err)
	}
	// An exec group carries no file: the command rides argv whole.
	if _, err := y.as(t, "yardmaster", "ask", b.ID, "Add the poster?", "--context", "the yard needs a poster",
		"--run", "group add poster --runner exec --set cmd=/srv/bin/post"); err != nil {
		t.Errorf("group add of an exec group = %v", err)
	}
}
