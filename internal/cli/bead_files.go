package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"yardr.dev/yardr/internal/store"
)

// beadFile is one file a bead's notes carry as bead files lists it: the note
// it is on (the number bead show gives the note), the file's number there,
// and what the yard keeps of the blob. ref is what a text row says of it,
// the same words the note's marked line says, and no part of the array.
type beadFile struct {
	Seq   uint64 `json:"seq"`
	N     int    `json:"n"`
	Hash  string `json:"hash"`
	Type  string `json:"type"`
	Bytes int64  `json:"bytes"`
	Name  string `json:"name"`
	ref   store.BlobRef
}

func (a *app) newBeadFilesCmd() *cobra.Command {
	files := &cobra.Command{
		Use:   "files <id>",
		Short: "List the files attached to a bead's notes",
		Long: `List every file attached to the bead's notes, one row each, the oldest
note first, in the words of the "◆" line bead show prints under a note:

  <seq> <n> <kind> <hash> <path>

<seq> is the note's number, as bead show gives it, and <n> the file's number in
that note, counted from 1 as bead note --replace --file-n counts. <kind> is
image, page or file, as the file's bytes say. <hash> names the file in blobs/
under the yard's home, and <path> is where the yard keeps it, with the name it
was attached under in parentheses after it. A bead whose notes carry no file
prints nothing.

--json prints an array of {seq, n, hash, type, bytes, name} instead, one object
for each file in the same order, type being the file's media type, and [] for a
bead with none. It prints no file's bytes: the path holds them, and the web
serves them at /blobs/<hash>.`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			st, err := a.store(c.Context())
			if err != nil {
				return err
			}
			b, err := st.GetBead(c.Context(), args[0])
			if err != nil {
				return err
			}
			notes, err := st.Notes(c.Context(), b.ID)
			if err != nil {
				return err
			}
			// The store answers a bead's notes in the order they were written,
			// which is their number's. An array, never nil, so --json says [].
			list := []beadFile{}
			for _, n := range notes {
				for i, f := range n.Files {
					list = append(list, beadFile{n.Seq, i + 1, f.Hash, f.Type, f.Bytes, f.Name, f})
				}
			}
			home := a.mailHome()
			return a.out(c.OutOrStdout(), list, func(w io.Writer) {
				for _, f := range list {
					fmt.Fprintf(w, "%d %d %s %s %s\n", f.Seq, f.N, f.ref.Kind(), f.Hash, store.FileWords(home, f.ref))
				}
			})
		},
	}
	return files
}
