package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"yardr.dev/yardr/internal/pack"
)

// packShowCmd is yardr pack show: what a pack says of itself that its plan
// does not print. naming is the paragraph on how a pack is named that check,
// apply and show share.
func (a *app) packShowCmd(naming string) *cobra.Command {
	var sf packFlags
	return &cobra.Command{
		Use: "show <pack>", Short: "Print who published a pack, its setup notes, presets and script options",
		Long: `Prints the pack's setup notes (pack.yaml's setup): its author's words
about what a setup takes — where in the service a webhook is made and which
events it needs, what an identity wants, how to test. They are the pack's,
not checked by yardr, and are marked so, as an answer from a source is
marked as data. A pack without any says so. Then the pack's presets
(pack.yaml's presets), if it has any: its suggested grants, numbered in
file order, each with the calls it grants of each source. Then the env of
its script groups (groups.<group>.env), if it has any: the options an
import sets in the cmd of a group it creates.

When the pack says who published it (pack.yaml's author and id: a name, and
the address it is published at), that comes first, marked as the pack's own
words, which yardr does not check.
` + naming,
		Example: `  yardr pack show linear`,
		Args:    cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			// As check: a directory pack is read before the yard is
			// asked anything, and a name is the yard's to resolve.
			_, _, p, err := a.openPack(c.OutOrStdout(), c.ErrOrStderr(), c.Context(), args[0], &sf, func() error { return a.onboard(c) }, false)
			if err != nil {
				return err
			}
			env := map[string]map[string]string{}
			for _, g := range p.Groups {
				if len(g.Env) > 0 {
					env[g.Name] = g.Env
				}
			}
			v := map[string]any{"pack": p.Name}
			if p.Author != "" {
				v["author"] = p.Author
			}
			if p.ID != "" {
				v["id"] = p.ID
			}
			if p.Setup == "" && len(p.Presets) == 0 && len(env) == 0 {
				return a.out(c.OutOrStdout(), v, func(w io.Writer) {
					printPublisher(w, p)
					fmt.Fprintf(w, "pack %s carries no setup notes\n", p.Name)
				})
			}
			if p.Setup != "" {
				v["file"], v["setup"] = p.Setup, p.SetupText
			}
			if len(p.Presets) > 0 {
				v["presets"] = p.Presets
			}
			if len(env) > 0 {
				v["env"] = env
			}
			return a.out(c.OutOrStdout(), v, func(w io.Writer) {
				printPublisher(w, p)
				if p.Setup == "" {
					fmt.Fprintf(w, "pack %s carries no setup notes\n", p.Name)
				} else {
					printSetup(w, p.Name, p.Setup, p.SetupText)
				}
				if len(p.Presets) > 0 {
					fmt.Fprintln(w)
					printPresets(w, p)
				}
				if len(env) > 0 {
					fmt.Fprintln(w)
					printEnv(w, p)
				}
			})
		},
	}
}

// printPublisher prints who published a pack and where, as the pack says, in
// the paragraph pack show leads with: nothing for a pack that says neither.
// They are marked as the pack's own words, as its setup notes are, since
// nothing says that the pack is the author's or is served at the address.
func printPublisher(w io.Writer, p *pack.Pack) {
	var said []string
	if p.Author != "" {
		said = append(said, "by "+p.Author)
	}
	if p.ID != "" {
		said = append(said, "published at "+p.ID)
	}
	if len(said) > 0 {
		fmt.Fprintf(w, "pack %s, %s (from the pack, not checked by yardr)\n\n", p.Name, strings.Join(said, ", "))
	}
}
