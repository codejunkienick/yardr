package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"yardr.dev/yardr/internal/store"
)

// takeBackAsk is bead ask --take-back <id> <ask> <reason>, with the three
// args the command has counted: the asker's open ask, or the operator's,
// taken back with the reason the person reads in its place. A reason of - is
// read from stdin, so that one quoting code or file names is written as a
// heredoc and no shell expands it.
func (a *app) takeBackAsk(c *cobra.Command, st store.API, args []string) error {
	// A bead@peer ref is the store's to refuse, with whose the question is to
	// take back.
	id := args[0]
	if !strings.Contains(id, "@") {
		b, err := st.GetBead(c.Context(), id)
		if err != nil {
			return err
		}
		id = b.ID
	}
	seq, err := strconv.ParseUint(args[1], 10, 64)
	if err != nil || seq == 0 {
		return fmt.Errorf("ask %q: want the ask's seq, as prime, review and chat print it", args[1])
	}
	// Read before the write, so that a reason with no words takes nothing
	// back, and in words that name stdin, not the store's.
	reason, err := stdinWords(c, args[2])
	if err != nil {
		return err
	}
	if args[2] == "-" && strings.TrimSpace(reason) == "" {
		return fmt.Errorf("nothing to write: stdin gave no reason, so ask %d on %s is still open", seq, id)
	}
	if err := st.TakeBackAsk(c.Context(), id, seq, reason, a.who()); err != nil {
		return err
	}
	return a.out(c.OutOrStdout(), map[string]any{"bead": id, "ask": seq, "state": askState(c.Context(), st, id, seq)},
		func(w io.Writer) { printAsk(w, c.ErrOrStderr(), c.Context(), st, id, seq) })
}
