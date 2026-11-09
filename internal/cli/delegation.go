package cli

// A delegated run from the CLI (design yardr-m90n 3): the yardmaster runs
// one of an operator's defining commands in its own pane with --for
// <operator> --mail <seq>, the seq of that operator's mail it acts on. The
// flags ride the call's context to the store (store.WithDelegation), which
// holds them to its own records in the write and keeps the record; the
// CLI decides nothing. preset=<label> names a harness kind's permission
// preset where args would go, so a delegated run never carries free args.

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"yardr.dev/yardr/internal/store"
)

// delegable are the commands the yardmaster may run for an operator,
// by their path under yardr (design yardr-m90n 3.2).
var delegable = [][]string{{"group", "add"}, {"group", "set"}, {"crew", "set"}, {"depot", "test"}, {"depot", "base"}, {"user", "set"}, {"hook", "add"}, {"pack", "apply"}}

// delegate gives each delegable command under root its --for and --mail,
// and has it run with the delegation on its context where --for is given,
// its preset=<label> words read first.
func (a *app) delegate(root *cobra.Command) {
	for _, path := range delegable {
		cmd, _, err := root.Find(path)
		if err != nil || cmd == root {
			continue
		}
		cmd.Flags().String("for", "", "the yardmaster runs this for the operator named, on their mail --mail <seq>: recorded (yardr delegation show) and mailed to them")
		cmd.Flags().Uint64("mail", 0, "with --for: the seq of the operator's own mail to the yardmaster this runs on")
		run := cmd.RunE
		cmd.RunE = func(c *cobra.Command, args []string) error {
			// The words as typed, before preset= is read into args.
			typed := commandWords(c, args)
			args, err := a.presetWords(c, path, args)
			if err != nil {
				return err
			}
			if who, _ := c.Flags().GetString("for"); who != "" {
				mail, _ := c.Flags().GetUint64("mail")
				c.SetContext(store.WithDelegation(c.Context(), store.DelegatedCall{For: who, Mail: mail, Command: typed}))
			} else if c.Flags().Changed("mail") {
				return fmt.Errorf("--mail names the operator's mail a delegated run cites: it goes with --for <operator>")
			}
			return run(c, args)
		}
	}
}

// delegatedRun says whether c runs as a delegated run (--for).
func delegatedRun(c *cobra.Command) bool {
	who, _ := c.Flags().GetString("for")
	return who != ""
}

// commandWords is the command c runs, as typed: its path, its words and
// its own flags it was given, --for and --mail aside, each quoted as a
// terminal reads it. The record keeps it whole; the root's flags (--actor,
// --json) say who ran it and how it printed, which the record says apart.
func commandWords(c *cobra.Command, args []string) string {
	words := strings.Fields(c.CommandPath())
	for _, w := range args {
		words = append(words, shellWord(w))
	}
	c.LocalNonPersistentFlags().Visit(func(f *pflag.Flag) {
		if f.Name == "for" || f.Name == "mail" {
			return
		}
		values := []string{f.Value.String()}
		if s, ok := f.Value.(pflag.SliceValue); ok {
			values = s.GetSlice()
		}
		for _, v := range values {
			words = append(words, "--"+f.Name, shellWord(v))
		}
	})
	return strings.Join(words, " ")
}

// shellWord is s as one word of a terminal line.
func shellWord(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789/._-+:,@%=") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// presetWords reads preset=<label> where a group or crew set's words, or
// group add's --set, carry it: args set to the flags of that permission
// preset of the kind the definition runs on (named in the same command,
// else the one it has).
func (a *app) presetWords(c *cobra.Command, path []string, args []string) ([]string, error) {
	cmd := strings.Join(path, " ")
	if cmd != "group add" && cmd != "group set" && cmd != "crew set" {
		return args, nil
	}
	words := args
	if cmd == "group add" {
		words, _ = c.Flags().GetStringArray("set")
	} else if len(args) > 0 {
		words = args[1:]
	}
	label, kind := "", ""
	for _, w := range words {
		k, v, _ := strings.Cut(w, "=")
		switch k {
		case "preset":
			label = v
		case "kind":
			kind = v
		}
	}
	if label == "" {
		return args, nil
	}
	if kind == "" {
		st, err := a.store(c.Context())
		if err != nil {
			return nil, err
		}
		if len(args) > 0 && cmd != "group add" {
			var cfg map[string]any
			if cmd == "group set" {
				g, err := st.GetGroup(c.Context(), args[0])
				if err != nil {
					return nil, err
				}
				cfg = g.Config
			} else {
				r, err := st.GetCrew(c.Context(), args[0])
				if err != nil {
					return nil, err
				}
				cfg = r.Config
			}
			kind, _ = cfg["kind"].(string)
		}
		if kind == "" {
			kind = defaultKind(c.Context(), st)
		}
		if kind == "" {
			setting, _ := store.SettingOf(store.DefaultKindSetting)
			kind = setting.Default
		}
	}
	p, ok := store.HarnessPreset(kind, label)
	if !ok {
		return nil, fmt.Errorf("preset=%s: %s has no such permission preset (it has: %s)", label, or(kind, "this kind"),
			or(strings.Join(store.HarnessPresetLabels(kind), ", "), "none"))
	}
	flags, err := json.Marshal(p.Args)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(words))
	for _, w := range words {
		if strings.HasPrefix(w, "preset=") {
			w = "args=" + string(flags)
		}
		out = append(out, w)
	}
	if cmd == "group add" {
		return args, c.Flags().Lookup("set").Value.(pflag.SliceValue).Replace(out)
	}
	return append([]string{args[0]}, out...), nil
}

// depotFieldCmds are depot test and depot base: a depot's test command or
// base changed alone, as the cards of the same words change them, for the
// operator at a terminal and for the yardmaster's delegated run.
func (a *app) depotFieldCmds() []*cobra.Command {
	set := func(c *cobra.Command, name string, change func(*store.Depot)) error {
		st, err := a.store(c.Context())
		if err != nil {
			return err
		}
		d, err := st.GetDepot(c.Context(), name)
		if err != nil {
			return err
		}
		change(d)
		if err := st.PutDepot(c.Context(), *d, a.who()); err != nil {
			return err
		}
		return a.out(c.OutOrStdout(), d, func(w io.Writer) {
			fmt.Fprintf(w, "depot %s: test: %s; base: %s\n", d.Name, or(d.Test, "none"), or(d.Base, "none"))
		})
	}
	test := &cobra.Command{
		Use:   "test <depot> <command>",
		Short: "Change a depot's test command",
		Long: `Sets the depot's test command, the one a bead's prime prints and a script
group gets as YARDR_TEST. yardr never runs it itself. Clearing it is yardr
depot add with the depot whole.`,
		Example: `  yardr depot test web 'sh .yardr/test'`,
		Args:    cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			if strings.TrimSpace(args[1]) == "" {
				return fmt.Errorf("depot test %s: name a command; clearing it is yardr depot add with the depot whole", args[0])
			}
			return set(c, args[0], func(d *store.Depot) { d.Test = args[1] })
		},
	}
	base := &cobra.Command{
		Use:     "base <depot> <base>",
		Short:   "Change a depot's base",
		Long:    `Sets the ref the depot's bead branches start from, as yardr depot add --base does.`,
		Example: `  yardr depot base web origin/trunk`,
		Args:    cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			return set(c, args[0], func(d *store.Depot) { d.Base = args[1] })
		},
	}
	return []*cobra.Command{test, base}
}

// delegationCmd is yardr delegation: the records of delegated runs, an
// operator's alone to read.
func (a *app) delegationCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "delegation", Short: "Records of what the yardmaster ran for an operator"}
	show := &cobra.Command{
		Use:   "show <id>",
		Short: "Show a delegated run's record (an operator's alone)",
		Long: `Shows the record of a delegated run: what the yardmaster ran for an operator,
on which of their mails, what each setting it changed held before, and how
it is taken back. Only an operator reads a record; anyone else is told it is
not there. The mail the yard sent the operator for the run names its id.`,
		Example: `  yardr delegation show 12`,
		Args:    cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			id, err := strconv.ParseUint(args[0], 10, 64)
			if err != nil || id == 0 {
				return fmt.Errorf("delegated record %q: an id is a number", args[0])
			}
			st, err := a.store(c.Context())
			if err != nil {
				return err
			}
			rec, err := st.Delegation(c.Context(), id, a.who())
			if err != nil {
				return err
			}
			if rec.Blob != nil {
				body, err := st.DelegationBlob(c.Context(), id, a.who())
				if err != nil {
					return err
				}
				rec.DelegatedBody = *body
			}
			return a.out(c.OutOrStdout(), rec, func(w io.Writer) { writeDelegation(w, rec) })
		},
	}
	cmd.AddCommand(show)
	return cmd
}

// writeDelegation prints a record as yardr delegation show reads it.
func writeDelegation(w io.Writer, rec *store.Delegation) {
	fmt.Fprintf(w, "record %d: %s for %s, %s\n", rec.ID, rec.Yardmaster, rec.For, rec.At.Local().Format("2006-01-02 15:04"))
	fmt.Fprintf(w, "on mail %d from %s\n", rec.Mail, rec.For)
	fmt.Fprintf(w, "changed: %s\n", rec.Of)
	fmt.Fprintf(w, "ran: %s\n", or(rec.Command, "(no command words were sent)"))
	for _, ch := range rec.Changes {
		fmt.Fprintf(w, "  %s: %s -> %s\n", ch.Key, delegatedValue(ch.Was, ch.Had), delegatedValue(ch.Is, ch.Has))
	}
	if rec.State != "" {
		state := rec.State
		if state == "started" {
			state = "started (unfinished)"
		}
		fmt.Fprintf(w, "state: %s\n", state)
	}
	if rec.Pack != "" {
		fmt.Fprintf(w, "pack %s, digest sha256:%s\n", rec.Pack, rec.Digest)
		if rec.From != "" {
			prior := "none known"
			if rec.Prior != "" {
				prior = "sha256:" + rec.Prior
			}
			fmt.Fprintf(w, "from: %s\nprior digest: %s\n", rec.From, prior)
		}
		for _, o := range rec.DelegatedBody.Objects {
			fmt.Fprintf(w, "  %s %s: %s\n", o.Kind, o.Name, o.Action)
			if o.Diff != "" {
				fmt.Fprintf(w, "    diff: %s\n", o.Diff)
			}
			if o.Had {
				fmt.Fprintf(w, "    before: %s\n", o.Before)
			}
		}
		if len(rec.Written) > 0 {
			fmt.Fprintf(w, "written: %s\n", strings.Join(rec.Written, ", "))
		}
		if rec.Error != "" {
			fmt.Fprintf(w, "failed: %s\n", rec.Error)
		}
	}
	if rec.Back != "" {
		fmt.Fprintf(w, "taken back by: %s\n", rec.Back)
	}
}

// delegatedValue is a value a record holds, "(unset)" where none stood.
func delegatedValue(v string, set bool) string {
	if !set {
		return "(unset)"
	}
	return v
}
