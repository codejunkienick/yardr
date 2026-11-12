// The server's carrying out of an approved general run card
// (internal/store/runask_general.go): the command as typed, run once
// as the operator who approved it, through its own CLI path under its
// own checks. It never holds the apply lock: it writes no pack or
// source files of the yard's, and a general command that itself
// applies one takes that lock as any terminal command does. The one
// watcher carries it in the same lane as every other carrying out, so
// it holds those up for at most its timeout.
package server

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"yardr.dev/yardr/internal/config"
	"yardr.dev/yardr/internal/store"
)

// generalTimeout bounds one general carrying out.
var generalTimeout = 2 * time.Minute

// generalMax is the most a general command may print on stdout for
// its answer to hold.
const generalMax = 65536

// generalDropped are the yardr variables the re-exec never inherits:
// the session and bead would misattribute the run to a pane or a
// bead, the actor and home are superseded by the flags it is given,
// and the crew would silently default a --crew the card never showed.
var generalDropped = []string{"YARDR_SESSION", "YARDR_BEAD", "YARDR_ACTOR", "YARDR_HOME", "YARDR_CREW"}

// scrubbedGeneralEnv is environ without generalDropped.
func scrubbedGeneralEnv(environ []string) []string {
	return slices.DeleteFunc(slices.Clone(environ), func(kv string) bool {
		key, _, _ := strings.Cut(kv, "=")
		return slices.Contains(generalDropped, key)
	})
}

// carryGeneralClaimed finishes a claimed general record without the
// apply lock, beside carryClaimed: it runs the command and records
// what it printed, or the failure, under the claiming boot.
func (c *carrier) carryGeneralClaimed(ctx context.Context, rec *store.Carry) (bool, error) {
	result, failure := c.carryGeneral(ctx, rec)
	if failure != "" {
		if err := c.st.FailCarry(ctx, rec.Bead, rec.Ask, c.boot, failure); err != nil {
			return true, err
		}
		c.log.Warn("carrying out a general ask failed", "bead", rec.Bead, "ask", rec.Ask, "failure", failure)
		return true, nil
	}
	if err := c.st.CompleteCarry(ctx, rec.Bead, rec.Ask, c.boot, result); err != nil {
		return true, err
	}
	c.log.Info("carried out a general ask", "bead", rec.Bead, "ask", rec.Ask, "command", store.AskRun{Argv: rec.Argv}.Command())
	return true, nil
}

// carryGeneral runs the general record's command as its approver: this
// yard's binary with the yard's home and the approver named, the words
// in order, from the yard's home, with the scrubbed environment, stdin
// closed, the group killed at the timeout and stdout held to the cap.
// It answers what the command printed, or the failure with the stderr
// tail and the terminal line.
func (c *carrier) carryGeneral(ctx context.Context, rec *store.Carry) (string, string) {
	terminal := store.AskRun{Argv: rec.Argv}.Command()
	self, err := executable()
	if err != nil {
		return "", failWords(fmt.Sprintf("the yard's own binary is not found: %v.", err), terminal)
	}
	argv := append([]string{self, "--home", c.home, "--actor", rec.Actor}, rec.Argv...)
	// YARDR_GENERAL_CARD has the CLI refuse words whose command cobra
	// finds past a flag (internal/cli/generalcard.go): the store read the
	// command from the first words when it refused the card.
	env := append(scrubbedGeneralEnv(os.Environ()), "YARDR_GENERAL_CARD=1")
	out, _, err := config.Command{
		What:    fmt.Sprintf("ask %d on %s", rec.Ask, rec.Bead),
		Argv:    argv,
		Dir:     c.home,
		Env:     env,
		Timeout: generalTimeout,
		Max:     generalMax,
	}.OutputWithStderr(ctx)
	if err != nil {
		return "", failWords(err.Error()+".", terminal)
	}
	return string(out), ""
}
