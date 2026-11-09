package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// newSessionSnoozeCmd is yardr session snooze: the run's own session says it
// waits, and on what, so that the yard counts no idle spell for it meanwhile.
func (a *app) newSessionSnoozeCmd() *cobra.Command {
	return &cobra.Command{
		Use: "snooze <dur> <why|->", Short: "Say this session waits, not stuck, for a bounded time", Args: cobra.ExactArgs(2),
		Long: `Says this session's agent waits, not stuck, for dur (10m, 90s):
while the snooze lasts the yard counts no idle spell for the session
and sends it no idle prod; when the snooze ends the idle count starts
fresh. Each snooze writes a session_snoozed event with the duration and
why, so yardr events shows it.

A why of - reads it from stdin: write one that quotes code or file names as a
heredoc with a quoted delimiter, since inside a double-quoted argument the
shell runs every backtick span and $( ) and puts what it prints in the text.
A why with no words on stdin is refused, and nothing is snoozed.

A snooze waits 15m at most, and 3 in a row without the pane changing;
past either the snooze is refused and the idle and age limits apply as
they always did. The command runs inside a bead session, as yardr bead
done does, and is refused outside one.`,
		RunE: func(c *cobra.Command, args []string) error {
			st, err := a.store(c.Context())
			if err != nil {
				return err
			}
			sess := a.identity().session
			if sess == "" {
				return fmt.Errorf("yardr session snooze runs inside a bead session; outside one there is nothing to snooze")
			}
			dur, err := time.ParseDuration(args[0])
			if err != nil {
				return fmt.Errorf("snooze %q: %w", args[0], err)
			}
			// Read before the write, so that a why with no words snoozes
			// nothing, and in words that name stdin, not the store's.
			why, err := stdinWords(c, args[1])
			if err != nil {
				return err
			}
			if args[1] == "-" && strings.TrimSpace(why) == "" {
				return fmt.Errorf("nothing to write: stdin gave no words for what the session waits on, so it is not snoozed")
			}
			until, err := st.SnoozeSession(c.Context(), sess, dur, why, a.who())
			if err != nil {
				return err
			}
			return a.out(c.OutOrStdout(), map[string]any{
				"session": sess, "until": until, "duration": dur.String(), "why": why,
			}, func(w io.Writer) {
				fmt.Fprintf(w, "snoozed %s until %s: %s\n", args[0], until.Local().Format("15:04"), why)
			})
		},
	}
}
