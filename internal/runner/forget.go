package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"yardr.dev/yardr/internal/agents"
	"yardr.dev/yardr/internal/dispatch"
	"yardr.dev/yardr/internal/store"
)

var _ dispatch.Forgetter = (*Herdr)(nil)

// missingNoForget is the reason a session is not forgotten when its kind's
// file names no way to (store.Harness.Forget): the harness keeps what it keeps.
const missingNoForget = "no forget"

// forgetTimeout bounds one forget. A harness answers a delete in well under a
// second (Codex removed a thread of 60 MB from a 1.7 GB database in half a
// second), so a minute is a program that is stuck, and it is stopped.
var forgetTimeout = time.Minute

// forgetStopWait is how long after a stopped program the call waits for the
// output it left open, so that a child of it that outlives it does not hold the
// call.
const forgetStopWait = 5 * time.Second

// forgetLine is the longest the one line of a program's error is kept.
const forgetLine = 200

// Forget makes the agent's harness forget the session handle names: it runs the
// kind's own program (store.Executable) with the arguments of the kind's
// forget, the id the dispatcher kept on the handle (agent_session) put where
// {session} stands. It is the session's record in the harness that goes, by the
// harness's own command, since removing the session file alone leaves what the
// harness keeps beside it (Codex keeps each thread in a database of its own).
//
// The program is started by name, never through a shell, with nothing on its
// standard input and, for a kind whose file names the variable its directory is
// moved by (store.ModelSource.HomeEnv), that variable set to the directory the
// agent used (agentHome), so that it forgets from the home the session was in.
// What it printed is not kept but a failure's first line.
//
// missing is why there is nothing to run and none will be: the kind has no file
// or names no forget ("no forget"), or the handle holds no id that can stand for
// a session ("no session id"). An id with a separator or a pattern character in
// it names another session, or none; one that begins with "-" would be an option
// of the program, which no id of a harness does. err is a program that failed,
// or ran past forgetTimeout: the caller tries again, as it may be a moment's
// trouble (a database held by another process) and not the session's.
func (h *Herdr) Forget(ctx context.Context, handle map[string]any) (missing string, err error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	name, _ := handle[harnessKind].(string)
	kind, ok := agents.Lookup(name)
	if !ok || kind.Forget == nil {
		return missingNoForget, nil
	}
	id, _ := handle[agentSession].(string)
	if !plainSessionID(id) || strings.HasPrefix(id, "-") {
		return missingNoID, nil
	}
	program := store.Executable(kind.Kind)
	runCtx, cancel := context.WithTimeout(ctx, forgetTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, program, kind.Forget.ArgsFor(id)...)
	cmd.WaitDelay = forgetStopWait
	if kind.Model != nil && kind.Model.HomeEnv != "" {
		// Last of the environment, which the last of a repeated name wins.
		cmd.Env = append(os.Environ(), kind.Model.HomeEnv+"="+h.agentHome(kind))
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	switch {
	case runErr == nil:
		return "", nil
	case ctx.Err() != nil:
		return "", ctx.Err() // the daemon is stopping, not the program failing
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		return "", fmt.Errorf("%s ran past %s and was stopped", program, forgetTimeout)
	}
	if line := firstLine(stderr.String(), forgetLine); line != "" {
		return "", fmt.Errorf("%s: %w: %s", program, runErr, line)
	}
	return "", fmt.Errorf("%s: %w", program, runErr)
}

// firstLine is the first line of text that is not blank, its white space made
// single spaces and cut to at most n runes, marking the cut: the one line of a
// program's complaint, whatever else it printed after it.
func firstLine(text string, n int) string {
	for _, l := range strings.Split(text, "\n") {
		if l = strings.Join(strings.Fields(l), " "); l != "" {
			if r := []rune(l); len(r) > n {
				return string(r[:n-1]) + "…"
			}
			return l
		}
	}
	return ""
}
