package cli

import (
	"fmt"
	"io"
	"os"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"yardr.dev/yardr/internal/store"
)

// flowSetStageCmd is yardr flow set-stage: the stage file of a store flow's
// stage, created or replaced in the yard's home by the store (PutStageFile),
// which holds the write to the operator, to the stage the flow has and to the
// file as it was read. What a crew member or a user proposes instead is the
// card of the same name (yardr bead ask, with R: flow set-stage ... on stdin).
func (a *app) flowSetStageCmd() *cobra.Command {
	var file, digest string
	cmd := &cobra.Command{
		Use:   "set-stage <flow> <stage> -f <file>",
		Short: "Set (create or replace) a store flow's stage file",
		Long: `Set the stage file of a stage of a flow in the store: the markdown whoever works
a bead in the stage is shown, in every depot that follows the flow, which is
the plain file <home>/flows/<flow>/<stage>.md (a stage whose name holds a
slash has its file a directory deeper). The text is put in whole, in one
step, at the mode of the file it replaces, and a stage the flow does not have
is refused. Only the operator sets it; a crew member proposes it as a card,
yardr bead ask <id> - --run-flow <file>, with R: flow set-stage <flow> <stage>
on stdin.

--digest holds the write to the file as you read it, so that a change made
since is not written over: none creates the file and is refused where one
stands, and the SHA-256 of the file, as sha256sum prints it (shasum -a 256 on
macOS), replaces only that text. With neither, whatever stands is replaced.
With -f -, the text is stdin as it is, trailing newlines included, so that
its digest is the file's.

A save keeps the text it replaces: before the file is replaced, the yard puts
that text in <home>/stage-history/<sha256>, a read-only file named by the
SHA-256 of the text, and refuses the save where it cannot. Saving that file
puts the text back: yardr flow set-stage <flow> <stage> -f
<home>/stage-history/<sha256>. --json says the digest of the text saved and,
for the text it replaced, prior_digest and prior_text.

A flow in a depot's repository has its stage files there, to be changed on a
branch and committed: ` + store.RepoFlowDir + `/<flow>/<stage>.md. Nothing is written for
one, and the refusal names the path.`,
		Example: `  yardr flow set-stage code review -f review.md
  yardr flow set-stage code review -f review.md --digest none
  yardr flow set-stage code qa/smoke -f - < smoke.md`,
		Args: cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			if file == "" {
				return fmt.Errorf("-f is required (- reads stdin)")
			}
			var raw []byte
			var err error
			if file == "-" {
				raw, err = io.ReadAll(c.InOrStdin())
			} else {
				raw, err = os.ReadFile(file)
			}
			if err != nil {
				return err
			}
			// Refused here, whichever way the store is reached: carried over
			// the socket as JSON, bytes that are no UTF-8 would arrive as
			// another text, and its digest as another file's.
			if !utf8.Valid(raw) {
				return fmt.Errorf("%s is not valid UTF-8 text: a stage file is text", store.OneLine(file))
			}
			st, err := a.store(c.Context())
			if err != nil {
				return err
			}
			flow, stage := args[0], args[1]
			sf, err := st.PutStageFile(c.Context(), flow, stage, string(raw), digest, a.who())
			if err != nil {
				return err
			}
			// Beside the result, so --json stays the stage file.
			if sf.Warning != "" {
				fmt.Fprintf(c.ErrOrStderr(), "warning: stage file %s\n", sf.Warning)
			}
			return a.out(c.OutOrStdout(), sf, func(w io.Writer) { fmt.Fprintf(w, "stage file %s/%s: %s\n", flow, stage, sf.Path) })
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "stage file text (- reads stdin)")
	cmd.Flags().StringVar(&digest, "digest", "", "write only over the file with this SHA-256, or only where none stands (none)")
	return cmd
}
