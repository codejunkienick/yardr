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
	var ackSeq, replaceSeq uint64
	var fileN int
	var noteFiles, pages []string
	var title string
	note := &cobra.Command{
		Use:   "note <id> <text> [--file <path>]... [--page <fragment> --title <title>] | note <id> --ack <seq> | note <id> --replace <seq> [--file-n <n>] (--page <fragment> --title <title> | --file <path>)",
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
"◆", as "1 image: <path>", "1 page: <path>" or "1 file: <path> (name)";
yardr bead files <id> lists every file a bead's notes carry.
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
and takes no text, no --file and no --page. A note told to a session that has
ended may be acked by the bead's live session, and bead show then says that
session read it; a note told to a session still live is that session's to ack.

--replace <seq> replaces the page of note <seq> in place and takes no text:
the new page is given as --page dresses one or as one --file page, held to
the same refusals as an attach (the title's rules, the yard's limit, and
HTML bytes alone). --file-n <n> names the file it replaces, counting from
1 as a note's files are numbered; without it the note's only page is the
one, and a note with no page or with more than one is refused. The note
keeps its words, the old bytes are kept by nothing and go with the prune,
and no version is kept: the stable address /blobs/note/<seq>/<n> serves
the new bytes, and one note_replaced event is the record of the swap.`,
		Args: func(c *cobra.Command, args []string) error {
			if c.Flags().Changed("ack") && c.Flags().Changed("replace") {
				return fmt.Errorf("note takes one form: --ack records a read, --replace replaces a page")
			}
			if c.Flags().Changed("ack") {
				if c.Flags().Changed("file") || c.Flags().Changed("page") || c.Flags().Changed("title") || c.Flags().Changed("file-n") {
					return fmt.Errorf("note --ack records that this session read the note: it takes no --file, --page or --title")
				}
				if len(args) != 1 {
					return fmt.Errorf("note --ack records that this session read the note: it takes the bead and no text")
				}
				return nil
			}
			if c.Flags().Changed("replace") {
				if len(args) != 1 {
					return fmt.Errorf("note --replace replaces a note's page: it takes the bead and no text")
				}
				switch {
				case len(pages) > 1:
					return fmt.Errorf("a note dresses one fragment, not %d: two pages are two notes", len(pages))
				case len(pages) == 1 && len(noteFiles) > 0:
					return fmt.Errorf("note --replace takes one new page: give --page or --file, not both")
				case len(pages) == 0 && len(noteFiles) == 0:
					return fmt.Errorf("note --replace needs the new page: give --page <fragment> --title <title> or --file <path>")
				case len(noteFiles) > 1:
					return fmt.Errorf("note --replace takes one file, not %d", len(noteFiles))
				case len(pages) == 1 && !c.Flags().Changed("title"):
					return fmt.Errorf("--page needs --title: it stands in the page's title block and in its <title>")
				case len(pages) == 0 && c.Flags().Changed("title"):
					return fmt.Errorf("--title is the title of the page --page dresses: give --page with it")
				case c.Flags().Changed("file-n") && fileN < 1:
					return fmt.Errorf("--file-n counts a note's files from 1, not %d", fileN)
				}
				return nil
			}
			if c.Flags().Changed("file-n") {
				return fmt.Errorf("--file-n names the file --replace replaces: give --replace with it")
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
			if c.Flags().Changed("replace") {
				if replaceSeq == 0 {
					return fmt.Errorf("--replace <seq>: a note's seq starts at 1")
				}
				if foreign {
					// Refused as a note on another yard's question is,
					// before any file is put.
					_, err := st.ReplaceNotePage(c.Context(), id, replaceSeq, fileN, store.BlobFile{}, a.who())
					return err
				}
				// Built exactly as an attach builds its page, so what can
				// refuse it refuses before the swap: the frame and title,
				// then the yard's limit on the put.
				var file store.BlobFile
				if len(pages) > 0 {
					dressed, err := a.dressPage(c.Context(), st, depot, pages[0], title)
					if err != nil {
						return err
					}
					page, err := dressed.put(c.Context(), st)
					if err != nil {
						return err
					}
					file = store.BlobFile{Hash: page.Hash, Framed: page.Framed}
				} else {
					refs, err := putFiles(c.Context(), st, noteFiles)
					if err != nil {
						return err
					}
					file = store.BlobFile{Hash: refs[0].Hash, Name: refs[0].Name}
				}
				ref, err := st.ReplaceNotePage(c.Context(), id, replaceSeq, fileN, file, a.who())
				if err != nil {
					return err
				}
				return a.out(c.OutOrStdout(), map[string]any{"bead": id, "note": replaceSeq, "replaced": "ok", "files": []store.BlobRef{*ref}}, func(w io.Writer) {
					fmt.Fprintln(w, store.WithFiles(fmt.Sprintf("note %d on %s replaced", replaceSeq, id), a.mailHome(), []store.BlobRef{*ref}))
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
	note.Flags().Uint64Var(&replaceSeq, "replace", 0, "replace note <seq>'s page in place, with --page or --file and no text")
	note.Flags().IntVar(&fileN, "file-n", 0, "with --replace, the file it replaces, counting from 1 (default: the note's only page)")
	note.Flags().StringArrayVar(&noteFiles, "file", nil, "a file the note carries (repeatable)")
	note.Flags().StringArrayVar(&pages, "page", nil, "a fragment of body content to dress in the frame that applies, and attach as a page (once; needs --title; see yardr page show)")
	note.Flags().StringVar(&title, "title", "", "the title of the page --page dresses, one line of plain text")
	return note
}
