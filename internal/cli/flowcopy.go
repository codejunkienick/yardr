package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"yardr.dev/yardr/internal/store"
)

// flowCopyCmd is yardr flow copy-repo: the flows of a depot's repository that
// govern its beads, copied into the yard's store by the store (CopyRepoFlows),
// which holds the copy to the operator and makes it in one write. What a crew
// member or a user proposes instead is the card of the same name (yardr bead
// ask, with R: flow copy-repo <depot> on stdin).
func (a *app) flowCopyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "copy-repo <depot>",
		Short: "Copy a depot's repository flows into the yard store",
		Long: `Copy the flows of a depot's repository that govern its beads into the store,
with their stage files, so that yardr flow set and yardr flow set-stage can
change them. The depot's own flow (default.flow), the flow of each type that
has a file (type/<type>.flow) and a named flow that a pointer of the depot
leads to are copied together, as <depot>.default and <depot>.<type> (with
.local after a name the repository has a flow of), their stage files to
<home>/flows/<flow>/<stage>.md, and the depot, and each of those types, are
pointed at the copies in one step: a pointer is read before any of the
repository's files, so a depot pointed at one copy alone would stop reading
the others.

A flow of the depot that is in the store already stays as it is, and a depot
with nothing to copy is refused, naming what it has. From then on a change
that lands in the repository's ` + store.RepoFlowDir + ` does not reach the flows copied:
they are changed in the yard only. The commands that point each pointer back
at the repository are printed, the depot's first, and leave the copy in the
store, unused: copying again replaces it, with its stage files. A store flow
of a copy's name that a depot, a type or an open bead's pin follows, or that a
pack manages, is never written over: the whole copy is refused, naming what
follows it, and nothing is written.

Only the operator copies; a crew member or a user proposes it as a card,
yardr bead ask <id> -, with R: flow copy-repo <depot> on stdin.`,
		Example: `  yardr flow copy-repo web
  yardr flow copy-repo web --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			st, err := a.store(c.Context())
			if err != nil {
				return err
			}
			// A terminal has no second gate to pass: the command is the
			// person's choice to copy, so it says weighed. What holds the copy
			// to the operator is the store, whoever types the command.
			res, err := st.CopyRepoFlows(c.Context(), args[0], a.who(), true)
			if err != nil {
				return err
			}
			return a.out(c.OutOrStdout(), res, func(w io.Writer) { printCopiedFlows(w, res) })
		},
	}
}

// printCopiedFlows says what a copy did: each file and the store flow it
// became, what stayed in the store, and the commands that point each pointer
// back, in the order they work in (the depot's pointer stands in front of the
// types' files, so it is pointed back first).
func printCopiedFlows(w io.Writer, res *store.CopiedFlows) {
	fmt.Fprintf(w, "copied the flows of depot %s into the store, from %s:\n", res.Depot, res.Source)
	for _, f := range res.Flows {
		note := plural(f.StageFiles, "stage file")
		if f.Replaced {
			note += ", replacing an unused earlier copy"
		}
		fmt.Fprintf(w, "  %s -> %s (%s)\n", f.From, f.Flow, note)
	}
	if len(res.Kept) > 0 {
		fmt.Fprintf(w, "left as they are, in the store already: %s\n", strings.Join(res.Kept, ", "))
	}
	fmt.Fprintln(w, "to point back at the repository, in this order:")
	for _, f := range res.Flows {
		fmt.Fprintf(w, "  %s\n", f.Back)
	}
}
