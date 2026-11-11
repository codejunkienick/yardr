package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// groupRenameCmd is yardr group rename: a group called by another name in one
// write, with everything that names it following. It is a file of its own
// because ops.go stands at the file-size ceiling the test gate holds.
func (a *app) groupRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <old> <new>",
		Short: "Rename a group: its routes, beads, sessions and records follow",
		Args:  cobra.ExactArgs(2),
		Long: `Changes the name of a group, and everything that names it follows in the same
write:
  - its routes, as their first group or after it, with their conditions;
  - the beads it owns and their live sessions, which go on running under the new
    name: nothing is released and no runner is touched (a new group, each route
    moved and group rm would release what its live sessions hold);
  - its settings, members, limit and subscriptions, and the grants of source
    calls its sessions hold;
  - the subscriptions that filter on it (--where group=<name>);
  - for a script (exec) group, the lists of members of other groups that name
    it, since its sessions count as the group where a list does;
  - what its arrivals have cost, so that the estimates stand;
  - the pack imports that recorded it, so that the next pack apply finds the
    renamed group and brings no group called <old> back.

The old name is free afterwards and reaches nothing. The event log and the
notes keep it, and so do the sessions that ended; what names it outside the
yard's records (a script, a role prompt, a pack's own files) is for you to
change. A yard default route that names its group by the depot template
({depot}-builders) is not renamed with it, and stops serving the depot whose
group this was: the command says which.

Refused when <old> is no group, when <new> is a group already (a rename never
replaces one), and when <new> is a name pack imports give the groups they make
for a depot of this yard, <depot>-<name>, which the next import would take for
its own. The rest of a name is held to what a new group's is.

Only the operator may rename a group. The yardmaster asks on a card, a run ask
(yardr bead ask --help), which names what follows; the operator approves it.`,
		Example: `  yardr group rename webapp-builders builders`,
		RunE: func(c *cobra.Command, args []string) error {
			st, err := a.store(c.Context())
			if err != nil {
				return err
			}
			out, err := st.RenameGroup(c.Context(), args[0], args[1], a.who())
			if err != nil {
				return err
			}
			return a.out(c.OutOrStdout(), out, func(w io.Writer) { fmt.Fprintln(w, out) })
		},
	}
}
