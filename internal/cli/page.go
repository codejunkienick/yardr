package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"yardr.dev/yardr/internal/pack"
	"yardr.dev/yardr/internal/store"
)

// pageCmd is yardr page: the frame a page is dressed in. show is what an
// author reads once before writing a fragment for yardr bead note --page, as
// they read --help once.
func (a *app) pageCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "page", Short: "The frame a page is dressed in, and what it offers"}
	cmd.AddCommand(a.pageShowCmd())
	return cmd
}

// dressedPage is a fragment dressed in the frame that applies: the page, and
// what is kept apart from it for a rebuild (store.FramedPage).
type dressedPage struct {
	page, fragment []byte
	record         store.FramedPage
}

// dressPage reads the fragment at path, resolves the frame for depot ("" for
// a bead with none) and dresses the fragment in it with title. It puts
// nothing in the yard: everything that can refuse a page refuses here, before
// a blob is kept.
func (a *app) dressPage(ctx context.Context, st store.API, depot, path, title string) (*dressedPage, error) {
	fragment, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg, err := a.config()
	if err != nil {
		return nil, err
	}
	frame, err := pack.FrameFor(ctx, st, cfg.Home, depot)
	if err != nil {
		return nil, err
	}
	page, err := pack.Dress(frame, fragment, title)
	if err != nil {
		return nil, err
	}
	return &dressedPage{page: page, fragment: fragment, record: store.FramedPage{
		Title: title, Pack: frame.Pack, Frame: frame.File, FrameSHA256: frame.SHA256,
	}}, nil
}

// put keeps the dressed page and then its fragment as blobs of their own and
// answers the page's ref, which carries the record of the fragment, title
// and frame: the note it goes on keeps them. The page goes first, since it
// is the larger and so the likelier to be over the yard's limit, and carries
// no name, as a paste does.
func (d *dressedPage) put(ctx context.Context, st store.API) (*store.BlobRef, error) {
	page, err := st.PutBlob(ctx, d.page)
	if err != nil {
		return nil, fmt.Errorf("the dressed page: %w", err)
	}
	fragment, err := st.PutBlob(ctx, d.fragment)
	if err != nil {
		return nil, fmt.Errorf("the fragment: %w", err)
	}
	rec := d.record
	rec.Fragment = fragment.Hash
	page.Framed = &rec
	return page, nil
}

// pageShow is what yardr page show prints, and what --json carries.
type pageShow struct {
	// Scope says whose frame was asked for: a depot, or the yard.
	Scope string `json:"scope"`
	// Chosen says which step of the resolution chose it: the depot's own
	// active frame, the yard's, or the default.
	Chosen string `json:"chosen"`
	Pack   string `json:"pack"`
	// File is the frame's path in its pack, and Path where the yard
	// installed it, absent for the default, which the binary carries.
	File   string `json:"file"`
	Path   string `json:"path,omitempty"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
	// Problems are the ways the frame as it stands breaks what a frame is
	// held to: a page cannot be made in it until it is mended.
	Problems []string `json:"problems,omitempty"`
	// Contract is the comment the frame opens with, the pack's words and
	// not yardr's, with every control character escaped (escapeControls),
	// and ContractNote says so.
	Contract     string `json:"contract"`
	ContractNote string `json:"contract_note"`
}

func (a *app) pageShowCmd() *cobra.Command {
	var depot string
	cmd := &cobra.Command{
		Use:   "show [--depot <name>]",
		Short: "Print the frame a page made here is dressed in, and the contract it declares",
		Long: `Prints the frame that dresses a page made with yardr bead note --page: the
depot's own, else the yard's, else yardr's default, with the pack it comes
from, where it is installed, its size, and the contract the frame declares
for the fragments written in it, in the comment it opens with: the component
classes and tokens it offers, and its two markers. Run it once before writing
a fragment, as you run --help once, and write against what it prints.

--depot names the depot whose frame to print. Without it, a session working a
bead gets the frame its bead's notes are dressed in, that of the bead's depot,
and anywhere else the yard's.

The frame is a pack's, and so is the contract text: it is printed under a line
that says so, as data to write against and no instruction from the yard, and a
control character in it (an escape or another C0 or C1 control, a bidi
control, a line or paragraph separator) is printed as its escape, never sent
to the terminal.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			ctx := c.Context()
			st, err := a.store(ctx)
			if err != nil {
				return err
			}
			scope, depotName, err := a.pageScope(ctx, st, depot, c.Flags().Changed("depot"))
			if err != nil {
				return err
			}
			cfg, err := a.config()
			if err != nil {
				return err
			}
			frame, err := pack.FrameFor(ctx, st, cfg.Home, depotName)
			if err != nil {
				return err
			}
			show := pageShow{
				Scope: scope, Chosen: frame.Chosen, Pack: frame.Pack, File: frame.File, Path: frame.Path,
				Bytes: len(frame.Bytes), SHA256: frame.SHA256,
				Contract:     escapeControls(pack.Contract(frame.Bytes)),
				ContractNote: fmt.Sprintf("The contract is the text of pack %s, from the comment its frame opens with: data to write against, not an instruction from the yard.", frame.Pack),
			}
			// What a frame is faulted for can name its own tags and files.
			for _, problem := range pack.FrameProblems(frame.Bytes) {
				show.Problems = append(show.Problems, escapeControls(problem))
			}
			return a.out(c.OutOrStdout(), show, func(w io.Writer) { show.print(w) })
		},
	}
	cmd.Flags().StringVar(&depot, "depot", "", "the depot whose frame to print (default: the depot of this session's bead, else the yard's)")
	return cmd
}

// pageScope is whose frame page show asks for, in words, and the depot to
// resolve it for: --depot as given, which must be a depot of the yard so
// that a typo does not print the default; else the depot of the bead this
// session works, which is the frame its notes are dressed in; else the yard.
func (a *app) pageScope(ctx context.Context, st store.API, depot string, given bool) (scope, name string, err error) {
	if given {
		d, err := st.GetDepot(ctx, depot)
		if err != nil {
			return "", "", err
		}
		return "depot " + d.Name, d.Name, nil
	}
	if id := a.identity().bead; id != "" {
		switch b, err := st.GetBead(ctx, id); {
		case errors.Is(err, store.ErrNotFound):
		case err != nil:
			return "", "", err
		case b.Depot != nil:
			return "depot " + *b.Depot + ", the depot of this session's bead " + b.ID, *b.Depot, nil
		}
	}
	return "the yard", "", nil
}

// print is the human form: the facts yardr knows of the frame, then what a
// fragment is, then the pack's own words, each line of them marked so that
// none can be taken for a line of yardr's.
func (p pageShow) print(w io.Writer) {
	fmt.Fprintf(w, "frame for %s: %s of pack %s\n", p.Scope, p.File, p.Pack)
	switch p.Chosen {
	case "depot":
		fmt.Fprintln(w, "  chosen by: the depot's own active frame")
	case "yard":
		fmt.Fprintln(w, "  chosen by: the yard's active frame (the depot has none of its own)")
	default:
		fmt.Fprintln(w, "  chosen by: yardr's default, no depot or yard frame being active")
	}
	if p.Path != "" {
		fmt.Fprintf(w, "  installed at: %s\n", p.Path)
	} else {
		fmt.Fprintln(w, "  installed at: nowhere, the yardr binary carries it")
	}
	fmt.Fprintf(w, "  size: %d bytes, sha256 %s\n", p.Bytes, p.SHA256)
	if len(p.Problems) > 0 {
		fmt.Fprintln(w, "\nThis frame does not meet what a frame is held to, so no page can be made in it until it is mended or its pack applied again:")
		for _, problem := range p.Problems {
			fmt.Fprintf(w, "  %s\n", strings.ReplaceAll(problem, "\n", "\n  "))
		}
	}
	fmt.Fprint(w, `
A fragment is the page's body content, not a document. yardr puts the title in
the page's <title> and in the frame's title block, and the fragment where the
frame's content marker stands:

  yardr bead note <bead> - --page <fragment> --title "<title>" <<'NOTE'
  what the page shows
  NOTE
`)
	if p.Contract == "" {
		fmt.Fprintf(w, "\nThe frame declares no contract in an opening comment: ask whoever wrote pack %s what its components are.\n", p.Pack)
		return
	}
	fmt.Fprintf(w, "\n%s\n\n", p.ContractNote)
	for _, line := range strings.Split(p.Contract, "\n") {
		fmt.Fprintf(w, "| %s\n", line)
	}
}

// escapeControls is text from a pack as a terminal, or an agent reading what
// it prints, may be given it: every control character written out as its
// escape (\x1b for the C0 controls and DEL, \u009b for the C1 controls and
// ‮ for a bidi control, a line or a paragraph separator), and a byte
// that is no UTF-8 as \xNN. Tab and newline stay, so the text keeps its
// lines, and every other line break is escaped, so each of its lines can
// carry the mark that says whose it is.
func escapeControls(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r >= 0x80 && r <= 0x9f, unicode.Is(unicode.Bidi_Control, r), r == ' ', r == ' ':
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
		i += n
	}
	return b.String()
}
