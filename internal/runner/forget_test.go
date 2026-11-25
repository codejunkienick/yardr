package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/scripttest"
	"yardr.dev/yardr/internal/testload"
)

// Forget runs the kind's own program, by the arguments its file names, for the
// session the dispatcher kept the id of: never through a shell, with nothing on
// its input and the harness's own directory in its environment, and says why it
// runs nothing where there is nothing to run. The program is a stand-in for
// codex, first on PATH, that writes what it was started with; the harness file
// is the real one.
//
// It sets PATH and an environment variable, so it does not run side by side
// with the others.
func TestHerdrForgetRunsTheKindsOwnProgram(t *testing.T) {
	ctx := context.Background()
	bin, home, out := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "ran")
	// The stand-ins sleep by absolute path, as agents' tests do: PATH holds the
	// stand-in first and the system's tools after it.
	t.Setenv("PATH", bin+":/bin:/usr/bin")
	// What a daemon that has a directory of its own in its environment must not
	// use for a session that was started in another.
	t.Setenv("CODEX_HOME", "/elsewhere")
	h := &Herdr{AgentHomes: map[string]string{"codex": home}}

	// program is the stand-in codex: it records its arguments one to a line, the
	// directory it was told to use and whether its input has anything on it, then
	// says what its body does.
	program := func(body string) {
		t.Helper()
		scripttest.Write(t, filepath.Join(bin, "codex"), `#!/bin/sh
{
  echo "argc=$#"
  for a in "$@"; do echo "arg=$a"; done
  echo "home=$CODEX_HOME"
  if read -r line; then echo "stdin=data"; else echo "stdin=eof"; fi
} > '`+out+`'
`+body)
	}
	ran := func() []string {
		t.Helper()
		b, err := os.ReadFile(out)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSpace(string(b)), "\n")
	}
	forget := func(handle map[string]any) (string, error) {
		t.Helper()
		if err := os.Remove(out); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		return h.Forget(ctx, handle)
	}
	codexSession := func(id any) map[string]any {
		return map[string]any{harnessKind: "codex", agentSession: id}
	}

	// The session's own record goes: delete, without asking, the id, from the
	// directory the agent used and not the daemon's own; nothing was typed.
	program("exit 0\n")
	if missing, err := forget(codexSession("01a2-b3")); missing != "" || err != nil {
		t.Fatalf("Forget = %q, %v, want it done", missing, err)
	}
	want := []string{"argc=3", "arg=delete", "arg=--force", "arg=01a2-b3", "home=" + home, "stdin=eof"}
	if got := ran(); !slices.Equal(got, want) {
		t.Errorf("the program was started with %q, want %q", got, want)
	}

	// An id that is odd but plain is one word of the program's arguments, as it
	// stands: no shell reads it, so nothing in it is expanded or run, and its
	// spaces split no word.
	oddly := "a b;$HOME `id`"
	if missing, err := forget(codexSession(oddly)); missing != "" || err != nil {
		t.Fatalf("Forget of an odd id = %q, %v", missing, err)
	}
	if got := ran(); len(got) < 4 || got[0] != "argc=3" || got[3] != "arg="+oddly {
		t.Errorf("an odd id was passed as %q, want one word of three, as it stands", got)
	}

	// Nothing is run where the kind has no way to forget, or the id is none.
	for name, c := range map[string]struct {
		handle map[string]any
		want   string
	}{
		"a handle with no kind":          {map[string]any{agentSession: "01a2-b3"}, missingNoForget},
		"a kind with no file":            {map[string]any{harnessKind: "cursor", agentSession: "01a2-b3"}, missingNoForget},
		"a kind whose file names none":   {map[string]any{harnessKind: "claude", agentSession: "01a2-b3"}, missingNoForget},
		"no id kept":                     {map[string]any{harnessKind: "codex"}, missingNoID},
		"an empty id":                    {codexSession(""), missingNoID},
		"an id that is no string":        {codexSession(42), missingNoID},
		"an id with a separator":         {codexSession("a/b"), missingNoID},
		"an id with a backslash":         {codexSession(`a\b`), missingNoID},
		"an id with a pattern":           {codexSession("a*b"), missingNoID},
		"an id with a question mark":     {codexSession("a?b"), missingNoID},
		"an id with a bracket":           {codexSession("a[b"), missingNoID},
		"an id that is an option":        {codexSession("-h"), missingNoID},
		"an id that is the force flag":   {codexSession("--force"), missingNoID},
		"a path that climbs":             {codexSession("../x"), missingNoID},
		"an id of a kind with no file":   {map[string]any{harnessKind: "cursor", agentSession: ""}, missingNoForget},
		"an id of a kind with no forget": {map[string]any{harnessKind: "muse", agentSession: "-x"}, missingNoForget},
	} {
		missing, err := forget(c.handle)
		if missing != c.want || err != nil {
			t.Errorf("%s: Forget = %q, %v, want %q", name, missing, err, c.want)
		}
		if got := ran(); got != nil {
			t.Errorf("%s: the program ran: %q", name, got)
		}
	}

	// A program that fails is an error, with the first line of what it said, to
	// be tried again, and is no reason to leave the session alone.
	program("echo 'Error: failed to delete session' >&2\necho 'a second line' >&2\nexit 1\n")
	missing, err := forget(codexSession("01a2-b3"))
	if missing != "" || err == nil || !strings.Contains(err.Error(), "codex") || !strings.Contains(err.Error(), "exit status 1") ||
		!strings.HasSuffix(err.Error(), "Error: failed to delete session") {
		t.Errorf("a failing program: Forget = %q, %v, want its first line of error", missing, err)
	}
	program("exit 3\n")
	if _, err := forget(codexSession("01a2-b3")); err == nil || !strings.HasSuffix(err.Error(), "exit status 3") {
		t.Errorf("a program that says nothing: Forget = %v, want its exit status", err)
	}
	program("echo '" + strings.Repeat("x", 500) + "' >&2\nexit 1\n")
	if _, err := forget(codexSession("01a2-b3")); err == nil || len([]rune(err.Error())) > 300 || !strings.HasSuffix(err.Error(), "…") {
		t.Errorf("a long complaint: Forget = %v, want it cut to a line", err)
	}

	// A program that hangs is stopped, and that is an error too. It execs its
	// sleep, so the one that is stopped is the one that holds the output.
	program("exec /bin/sleep 30\n")
	old := forgetTimeout
	forgetTimeout = 100 * time.Millisecond
	defer func() { forgetTimeout = old }()
	result := make(chan error, 1)
	go func() {
		_, err := forget(codexSession("01a2-b3"))
		result <- err
	}()
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "ran past 100ms") {
			t.Errorf("a program that hangs: Forget = %v, want that it ran past its time", err)
		}
	case <-time.After(testload.Scale(20 * time.Second)):
		t.Fatal("a program that hangs held Forget past its time")
	}
	forgetTimeout = old

	// A daemon that is stopping is told so, not that a program failed, and starts none.
	stopping, cancel := context.WithCancel(ctx)
	cancel()
	if err := os.Remove(out); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if _, err := h.Forget(stopping, codexSession("01a2-b3")); !errors.Is(err, context.Canceled) || ran() != nil {
		t.Errorf("Forget with a stopped context = %v, ran %q; want the context's error and no run", err, ran())
	}

	// The harness not installed is an error to try again, as any failure of the
	// program is; it is not a session with nothing to forget.
	t.Setenv("PATH", t.TempDir())
	if missing, err := forget(codexSession("01a2-b3")); missing != "" || err == nil || !strings.Contains(err.Error(), "executable file not found") {
		t.Errorf("a harness not installed: Forget = %q, %v", missing, err)
	}
}
