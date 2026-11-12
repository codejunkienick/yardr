package cli

import (
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"yardr.dev/yardr/internal/dispatch"
	"yardr.dev/yardr/internal/limits"
	"yardr.dev/yardr/internal/store"
)

func (a *app) sessionCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "session", Short: "Runner sessions working beads"}
	var running bool
	var bead string
	var cut listCut
	list := &cobra.Command{
		Use: "list", Short: "List live sessions", Args: cobra.NoArgs,
		Long: `List live sessions: one for each bead a group has claimed. ORIGINATOR
is the user the bead is for, beside the group; LOGIN is whose login the
group burns, its login_for else the operator's, so a subsidy route shows
another user's work on the operator's login.

A manual group's claim is among them, though nothing runs in it: the bead
waits for a person. --running leaves those out, so the list is what an
agent (herdr) or a script (exec) is working on now.

Under the table is a line for each session whose agent stopped on a usage
limit and waits for the window to reset: until when the daemon waits before
it prompts the agent, and the model its group starts no further session
with meanwhile (docs/groups.md). --json has it on the session as reset_wait:
until, window, model. It is read from the session, and asks no provider.

-a lists the ended sessions too. A yard keeps those until they are pruned,
so that listing is the newest ` + strconv.Itoa(listLimit) + ` by start: -n <N> shows more or
fewer, and --all every session there is, which is what a script wants.
--bead keeps one bead's sessions and --since those started in that time
(7d, 12h); the limit takes the newest of what they keep. Without -a or
--all the sessions are the live ones, however many.`,
		RunE: func(c *cobra.Command, _ []string) error {
			st, err := a.store(c.Context())
			if err != nil {
				return err
			}
			// Running leaves out a manual group's claim: a bead waiting for
			// a person, with nothing running behind it (yardr-7r3h).
			f := store.SessionFilter{Bead: bead, Running: running}
			if f.All, f.Limit, f.Since, err = cut.read(c); err != nil {
				return err
			}
			ss, err := st.ListSessions(c.Context(), f)
			if err != nil {
				return err
			}
			cut.say(c.ErrOrStderr(), len(ss), "sessions")
			listed := dispatch.Listed(ss)
			now := time.Now()
			return a.out(c.OutOrStdout(), listed, func(w io.Writer) {
				rows := [][]string{}
				for _, s := range listed {
					// Which runner owns it, and what it is running there.
					runner := s.RunnerName()
					if v, ok := s.Handle["agent"]; ok {
						runner += " " + show(v)
					} else if v, ok := s.Handle["pid"]; ok {
						runner += " pid " + show(v)
					}
					rows = append(rows, []string{s.ID, s.Bead, s.Train, s.Stage, s.Group, s.Originator, s.Login, s.State, or(runner, "-"),
						s.StartedAt.Local().Format("01-02 15:04")})
				}
				table(w, "ID\tBEAD\tTRAIN\tSTAGE\tGROUP\tORIGINATOR\tLOGIN\tSTATE\tRUNNER\tSTARTED", rows)
				for _, s := range listed {
					if s.ResetWait != nil {
						fmt.Fprintln(w, resetWaitLine(*s.ResetWait, s.Session, now))
					}
				}
			})
		},
	}
	cut.flags(list, "ended", "ended sessions")
	list.Flags().StringVar(&bead, "bead", "", "only this bead's sessions")
	list.Flags().BoolVar(&running, "running", false, "only sessions an agent or a script is behind: leave out what a person holds and what another yard works (plain session list lists both)")
	cmd.AddCommand(list, a.newSessionSnoozeCmd())
	return cmd
}

// resetWaitLine says the wait w of s in a line, for a person.
func resetWaitLine(w dispatch.ResetWaiting, s store.Session, now time.Time) string {
	model := "the agent's own model"
	if w.Model != "" {
		model = "model " + w.Model
	}
	when := "until " + limits.Clock(w.Until, now)
	if left := w.Until.Sub(now); left >= 0 {
		when += " (in " + limits.Span(left) + ")"
	} else {
		// The daemon prompts the agent on its next pass, or is not running.
		when += " (" + limits.Span(-left) + " ago)"
	}
	window := "the window"
	if w.Window != "" {
		window = "the " + w.Window + " window"
	}
	return fmt.Sprintf("%s starts no further session with %s %s: session %s (bead %s) waits for %s of its usage limit to reset",
		s.Group, model, when, s.ID, s.Bead, window)
}
