package cli

import (
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/spf13/cobra"

	"yardr.dev/yardr/internal/source"
)

// packInstallResult is what yardr pack install did, for --json: the
// pack, and the programs it installed, each now on this machine.
type packInstallResult struct {
	Pack      string   `json:"pack"`
	Installed []string `json:"installed"`
}

// packInstallCmd is yardr pack install <pack>: the terminal form of
// the pack install run card. It installs what the pack's declarations
// name under needs that this machine lacks, and nothing else; the
// yard never installs by itself, so on a card the same command waits
// for the operator's approval (yardr bead ask --run).
func (a *app) packInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install <pack>",
		Short: "Install the programs a pack declares it needs that this machine lacks",
		Long: `Install the programs the pack's declarations name under needs that
this machine lacks, as the declarations say to get them: each
install is an argv run without a shell, with no credential in its
environment, for at most ` + source.InstallTimeout.String() + `, and it counts as installed only
where the program stands after. A program that is here, by the
variable its declaration names or on PATH, is left alone, and a pack
that needs nothing this machine lacks changes nothing.

<pack> is read as pack apply reads it: a bare word is a pack this
yard has imported or one yardr carries, a path with a separator is
its directory, and a URL is fetched when the command runs. The same
command rides a run card (the pack install row yardr bead ask --run
takes), which shows each install file whole with its sha256 and
runs exactly those bytes when the operator approves; an install that
names an absolute program is the operator's own and runs here alone.`,
		Example: `  yardr pack install google-mail`,
		Args:    cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return a.packInstallRun(c, args[0])
		},
	}
}

// packInstallRun installs what the pack arg names needs and the
// machine lacks, printing each install as it runs.
func (a *app) packInstallRun(c *cobra.Command, arg string) error {
	ctx := c.Context()
	st, err := a.store(ctx)
	if err != nil {
		return err
	}
	cfg, err := a.config()
	if err != nil {
		return err
	}
	p, err := a.loadPack(c.OutOrStdout(), ctx, st, arg, "", "", cfg.Home)
	if err != nil {
		return err
	}
	type job struct {
		decl *source.Declaration
		need source.Need
		file string // the declaration file, for a relative install's bytes
	}
	var jobs []job
	seenProg := map[string]bool{}
	for _, d := range p.Declared {
		if d.Declaration == nil {
			continue
		}
		for _, n := range d.Declaration.MissingNeeds() {
			if seenProg[n.Program] {
				continue
			}
			seenProg[n.Program] = true
			jobs = append(jobs, job{decl: d.Declaration, need: n, file: d.File})
		}
	}
	res := packInstallResult{Pack: p.Name, Installed: []string{}}
	if len(jobs) == 0 {
		return a.out(c.OutOrStdout(), res, func(w io.Writer) {
			fmt.Fprintf(w, "pack %s: every program it declares it needs is on this machine\n", p.Name)
		})
	}
	for _, j := range jobs {
		var data []byte
		if !strings.HasPrefix(j.need.Install[0], "/") {
			rel := j.need.Install[0]
			if dir := path.Dir(j.file); dir != "." {
				rel = path.Join(dir, rel)
			}
			data, err = p.ReadFile(rel)
			if err != nil {
				return fmt.Errorf("pack %s: the install file %s of %s: %w", p.Name, j.need.Install[0], j.need.Program, err)
			}
		}
		argv, cleanup, err := source.InstallArgv(j.need, data)
		if err != nil {
			return err
		}
		if !a.json {
			fmt.Fprintf(c.OutOrStdout(), "installing %s for pack %s (%s)...\n", j.need.Program, p.Name, strings.Join(j.need.Install, " "))
		}
		err = source.InstallNeed(ctx, j.decl, j.need, argv)
		cleanup()
		if err != nil {
			return fmt.Errorf("pack %s: %w", p.Name, err)
		}
		res.Installed = append(res.Installed, j.need.Program)
	}
	return a.out(c.OutOrStdout(), res, func(w io.Writer) {
		for _, program := range res.Installed {
			fmt.Fprintf(w, "installed %s for pack %s: it is on this machine now\n", program, res.Pack)
		}
	})
}
