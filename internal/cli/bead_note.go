package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"io"
	"yardr.dev/yardr/internal/store"
)

func (a *app) newBeadNoteCmd() *cobra.Command {
	var ackSeq uint64
	var noteFiles, pages []string
	var title string
	note := &cobra.Command{
		Use:   "note <id> <text> [--file <path>]... [--page <fragment> --title <title>] | note <id> --ack <seq>",
		Short: "Attach a note (evidence) to a bead",
		Long: `Attaches a note (evidence) to a bead: what was done, seen or decided, in
words. The bead's live session is told of it, unless the writer is that
session itself.

<text> may be - to read the words from stdin, which is how to write a note
that quotes code or file names: inside a double-quoted argument the shell
runs every backtick span and $( ) and puts what it prints in the note,
where a heredoc with a quoted delimiter expands nothing:

  yardr bead note <id> - <<'NOTE'
  the words, as they are
  NOTE

A note with no words, empty or only white space, is refused, with --file
or without: a shell may have emptied it.

--file <path> attaches a file to the note, and may be given more than once
(up to ` + strconv.Itoa(store.MaxNoteFiles) + `). The yard keeps the file in its
home, in blobs/ under the SHA-256 of its bytes, once however often it is
attached, and where the note is read (yardr prime, yardr review, yardr bead
show) it stands under the note's words, on a line of its own starting with
"◆", as "1 image: <path>", "1 page: <path>" or "1 file: <path> (name)".
The note still needs its words: say what they show. What kind it is is read
from its bytes: an image is a PNG or a JPEG, a page is HTML, anything else
a file, which carries the path's base name; one over the yard's limit
(yardr settings get ` + store.BlobMaxSetting + `, 8 MiB unless set) is refused.

--page <fragment> --title <title> attaches a page made in the frame that
applies to this bead's depot: the depot's own, else the yard's, else yardr's
default. <fragment> is a file of the page's body content, such as headings,
prose, tiles, a table or an inline SVG chart, and not a whole HTML document;
the title goes in the page's title block and in its <title>. The page opens
and reads as --file's pages do. It is given once for a note, and the
fragment, the title and the frame it was made in are kept apart from the
page. yardr page show prints the frame, and what it offers a fragment, before
you write one. --title is required with --page and refused without it.

--ack <seq> writes nothing: it records that this session read note <seq>,
and takes no text, no --file and no --page.`,
		Args: func(c *cobra.Command, args []string) error {
			if c.Flags().Changed("ack") {
				if c.Flags().Changed("file") || c.Flags().Changed("page") || c.Flags().Changed("title") {
					return fmt.Errorf("note --ack records that this session read the note: it takes no --file, --page or --title")
				}
				if len(args) != 1 {
					return fmt.Errorf("note --ack records that this session read the note: it takes the bead and no text")
				}
				return nil
			}
			switch {
			case len(pages) > 1:
				return fmt.Errorf("a note dresses one fragment, not %d: two pages are two notes", len(pages))
			case len(pages) == 1 && !c.Flags().Changed("title"):
				return fmt.Errorf("--page needs --title: it stands in the page's title block and in its <title>")
			case len(pages) == 0 && c.Flags().Changed("title"):
				return fmt.Errorf("--title is the title of the page --page dresses: give --page with it")
			case len(noteFiles)+len(pages) > store.MaxNoteFiles:
				return fmt.Errorf("a note carries at most %d files, not %d", store.MaxNoteFiles, len(noteFiles)+len(pages))
			}
			return cobra.ExactArgs(2)(c, args)
		},
		RunE: func(c *cobra.Command, args []string) error {
			st, err := a.store(c.Context())
			if err != nil {
				return err
			}
			// A bead@peer ref stands on no bead of this yard; it goes to
			// AddNote as typed, which refuses it with the way to mail
			// the asker.
			id := args[0]
			_, _, foreign := strings.Cut(id, "@")
			depot := ""
			if !foreign {
				b, err := st.GetBead(c.Context(), id)
				if err != nil {
					return err
				}
				id = b.ID
				if b.Depot != nil {
					depot = *b.Depot
				}
			}
			if c.Flags().Changed("ack") {
				if ackSeq == 0 {
					return fmt.Errorf("--ack <seq>: a note's seq starts at 1")
				}
				n, err := st.AckNote(c.Context(), id, ackSeq, a.identity().session, a.who())
				if err != nil {
					return err
				}
				return a.out(c.OutOrStdout(), map[string]any{"bead": id, "note": n.Seq, "acked": "ok"}, func(w io.Writer) {
					fmt.Fprintf(w, "note %d on %s acked\n", n.Seq, id)
				})
			}
			text := args[1]
			if text == "-" {
				b, err := io.ReadAll(c.InOrStdin())
				if err != nil {
					return err
				}
				text = strings.TrimRight(string(b), "\n")
			}
			if foreign && (len(noteFiles) > 0 || len(pages) > 0) {
				// A note on another yard's question is always refused;
				// say so before any file is put.
				_, err := st.AddNotedAs(c.Context(), id, text, a.who(), a.identity().session)
				return err
			}
			if len(noteFiles) > 0 && strings.TrimSpace(text) == "" {
				return fmt.Errorf("a note with --file needs words: say what they show")
			}
			if len(pages) > 0 && strings.TrimSpace(text) == "" {
				return fmt.Errorf("a note with --page needs words: say what the page shows")
			}
			// What can refuse the page is settled before any blob is put, so
			// a refusal leaves nothing in the yard. It is put before the
			// files: the yard's size limit is likelier to refuse it, the
			// frame counting in it, and nothing of the files is left behind.
			var page *store.BlobRef
			if len(pages) > 0 {
				dressed, err := a.dressPage(c.Context(), st, depot, pages[0], title)
				if err != nil {
					return err
				}
				if page, err = dressed.put(c.Context(), st); err != nil {
					return err
				}
			}
			refs, err := putFiles(c.Context(), st, noteFiles)
			if err != nil {
				return err
			}
			files := blobFiles(refs)
			if page != nil {
				refs = append(refs, *page)
				files = append(files, store.BlobFile{Hash: page.Hash, Framed: page.Framed})
			}
			n, err := st.AddNotedAs(c.Context(), id, text, a.who(), a.identity().session, files...)
			if err != nil {
				return err
			}
			out := map[string]any{"bead": id, "noted": "ok"}
			if n.Notify != "" {
				out["notify"] = n.Notify
			}
			if len(refs) > 0 {
				out["files"] = refs
			}
			return a.out(c.OutOrStdout(), out, func(w io.Writer) {
				said := fmt.Sprintf("note %d on %s", n.Seq, id)
				if n.Notify != "" {
					said += fmt.Sprintf(" (session %s will be told)", n.Notify)
				}
				fmt.Fprintln(w, store.WithFiles(said, a.mailHome(), refs))
			})
		},
	}
	note.Flags().Uint64Var(&ackSeq, "ack", 0, "record that this session read note <seq>, instead of writing one")
	note.Flags().StringArrayVar(&noteFiles, "file", nil, "a file the note carries (repeatable)")
	note.Flags().StringArrayVar(&pages, "page", nil, "a fragment of body content to dress in the frame that applies, and attach as a page (once; needs --title; see yardr page show)")
	note.Flags().StringVar(&title, "title", "", "the title of the page --page dresses, one line of plain text")
	return note
}
