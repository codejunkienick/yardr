// yardr crew set: some settings of a crew member's definition changed and
// the rest kept, and the lines that say what a definition's write changed,
// which group set prints too.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"yardr.dev/yardr/internal/store"
)

// crewSetCmd is yardr crew set: some settings of a definition
// changed, the rest kept. crew add cannot do that, as it writes the
// whole definition, and a grant of one permission made with it cost a
// running crew member everything that was not retyped.
func (a *app) crewSetCmd() *cobra.Command {
	var unset, on []string
	var dir, maxAge, grace, contextHandoff, rev string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "set <name> [key=value ...]",
		Short: "Change some settings of a crew member and keep the rest",
		Long: `Changes the settings named and keeps the rest of the crew member's definition.
key=value is a runner config key, as --set on yardr crew add takes it
(kind, model, fallback_model, args, prompt, env_file, env_command, for, open_threads,
set_state, peers); --unset <key> removes one. --dir, --on, --max-age,
--handoff-grace and --context-handoff change what the same flags of add set.

It prints each setting changed, as it was and as it is. The value of a key
named like a secret (token, secret, password, key) is not printed.

open_threads, set_state and peers follow the rule of add: only the
operator (the yard's operator setting, else the user running the command)
may allow a crew member a source or a peer it was not allowed before. Narrowing
one, or removing it with --unset, is anyone's.

A running crew member keeps its sessions. What it may do (open_threads,
set_state, peers), whom it serves (for), fallback_model, --on, --max-age and
--context-handoff hold from the next time they are looked at; what a session
starts with (kind, model, args, prompt, its environment, --dir) is read when
the next one starts: yardr crew restart <name>, or --for <user> for a conversation session.

--dry-run writes nothing and prints the plan of a card of the same settings
(yardr bead ask --run): each field the yard would change, as it stands and as
the approval would leave it, read from the yard's records now, and the
command that takes it back. It takes the settings a card carries: prompt,
preset, kind, model and dir (or --dir). A card's prompt is a file the yard
writes from the text, named by its digest, and the plan names that file.`,
		Example: `  yardr crew set yardmaster set_state=linear
  yardr crew set yardmaster model=opus 'args=["--permission-mode","auto"]' --max-age 24h
  yardr crew set yardmaster --unset open_threads
  yardr crew set yardmaster preset=bypass --dry-run`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			ctx := c.Context()
			st, err := a.store(ctx)
			if err != nil {
				return err
			}
			name := args[0]
			// parseSets would speak of --set, which this command has not.
			for _, kv := range args[1:] {
				if k, _, ok := strings.Cut(kv, "="); !ok || k == "" {
					return fmt.Errorf("%q: want key=value (--unset <key> removes a setting)", kv)
				}
			}
			fl := c.Flags()
			if dryRun {
				// The words as typed: the run reads preset= into args before
				// it gets here (delegate), and the card takes the preset.
				return a.crewSetDryRun(c, st, name, c.Flags().Args()[1:], dir)
			}
			sets, err := store.ParseSets(args[1:])
			if err != nil {
				return err
			}
			if len(sets) == 0 && len(unset) == 0 && !fl.Changed("dir") && !fl.Changed("on") && !fl.Changed("max-age") && !fl.Changed("handoff-grace") && !fl.Changed("context-handoff") {
				return fmt.Errorf("nothing to set: give key=value, --unset <key>, --dir, --on, --max-age, --handoff-grace or --context-handoff")
			}
			for _, k := range unset {
				if _, both := sets[k]; both {
					return fmt.Errorf("%s is both set and unset", k)
				}
			}
			if fl.Changed("dir") {
				if dir, err = absDir(dir); err != nil {
					return err
				}
			}
			// The operator rule looks at what this command sets, not at
			// the settings it keeps: those were checked when they were saved.
			if err := a.mayDefine(ctx, st, store.CrewMember{Name: name, Config: sets}, true); err != nil {
				return err
			}
			var r *store.CrewMember
			var changes []defChange
			// The daemon's bookkeeping re-mints a running crew member's revision
			// between the read and the write now and then. Without --revision
			// that is no reason to refuse: read again and apply the same
			// change. With it, the caller asked to be refused.
			for try := 0; ; try++ {
				have, err := st.GetCrew(ctx, name)
				if err != nil {
					return err
				}
				def := *have
				if def.Config, err = store.MergeConfig(name, have.Config, sets, unset); err != nil {
					return err
				}
				if fl.Changed("dir") {
					def.Dir = dir
				}
				if fl.Changed("on") {
					def.On = on
				}
				if fl.Changed("max-age") {
					def.MaxAge = maxAge
				}
				if fl.Changed("handoff-grace") {
					def.HandoffGrace = grace
				}
				if fl.Changed("context-handoff") {
					def.ContextHandoff = contextHandoff
				}
				if changes = defChanges(*have, def); len(changes) == 0 {
					r = have
					break
				}
				def.Revision = or(rev, have.Revision)
				if r, err = st.PutCrew(ctx, def, a.who()); err == nil {
					break
				}
				if rev != "" || try == 4 || !errors.Is(err, store.ErrConflict) {
					return bareSetHint(err)
				}
			}
			return a.out(c.OutOrStdout(), r, func(w io.Writer) {
				if len(changes) == 0 {
					fmt.Fprintf(w, "crew member %s unchanged: it is set so already\n", name)
					return
				}
				fmt.Fprintf(w, "crew member %s saved\n%s\n", name, changeLines(changes))
				var atStart []string
				for _, ch := range changes {
					if !slices.Contains([]string{store.OpenThreads, store.SetState, store.MailPeers, "fallback_model", "--on", "--max-age", "--handoff-grace", "--context-handoff"}, ch.Key) {
						atStart = append(atStart, ch.Key)
					}
				}
				if r.Live() && len(atStart) > 0 {
					fmt.Fprintf(w, "%s is running and keeps its session: its next one starts with the new %s (yardr crew restart %s)\n",
						name, strings.Join(atStart, ", "), name)
				}
			})
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&unset, "unset", nil, "remove this runner config key (repeatable)")
	f.StringVar(&dir, "dir", "", "directory the crew member works in (empty: the yard home)")
	f.StringSliceVar(&on, "on", nil, "event kinds delivered to its inbox, or "+store.KindAttention+", the kinds that need someone; replaces the list, so name every kind wanted")
	f.StringVar(&maxAge, "max-age", "", "ask for a handoff after this long, e.g. 24h (empty: never)")
	f.StringVar(&grace, "handoff-grace", "", "how long a requested handoff may take before a forced restart")
	f.StringVar(&contextHandoff, "context-handoff", "", "ask for a handoff at this percent of the context window (default: 60, 0: never)")
	f.StringVar(&rev, "revision", "", "refuse unless the crew member is still at this revision (from crew show --json)")
	f.BoolVar(&dryRun, "dry-run", false, "write nothing: print what a card of these settings would change, before and after")
	return cmd
}

// crewSetDryRun prints the plan of a card of crew set name with the words
// sets, as the store draws it (PlanRun), so that the dry run and the card
// read one truth. The words are the card's: a prompt's text is read from its
// file here, as yardr bead ask reads it beside the ask, and --dir is the
// card's dir=. Any other flag is no card's and has no plan.
func (a *app) crewSetDryRun(c *cobra.Command, st store.API, name string, sets []string, dir string) error {
	var others []string
	// VisitAll and Changed, as commandWords reads them: the copy lists the
	// flags it holds and not the ones that were set.
	c.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) {
		if f.Changed && f.Name != "dry-run" && f.Name != "dir" {
			others = append(others, "--"+f.Name)
		}
	})
	if len(others) > 0 {
		return fmt.Errorf("--dry-run plans what a card of crew set carries (prompt, preset, kind, model, dir), and %s is none of it", strings.Join(others, ", "))
	}
	argv := append([]string{"crew", "set", name}, sets...)
	if c.Flags().Changed("dir") {
		abs, err := absDir(dir)
		if err != nil {
			return err
		}
		argv = append(argv, "dir="+abs)
	}
	var role string
	for _, kv := range sets {
		if path, ok := strings.CutPrefix(kv, "prompt="); ok {
			text, err := readCrewRoleFile(path)
			if err != nil {
				return err
			}
			role = string(text)
		}
	}
	card, err := st.PlanRun(c.Context(), store.AskRun{Argv: argv, Role: role})
	if err != nil {
		return err
	}
	return a.out(c.OutOrStdout(), card.Plan, func(w io.Writer) {
		if len(card.Plan.Changes) == 0 {
			fmt.Fprintf(w, "%s is set so already: a card of %s would change nothing\n", card.Plan.Of, card.Command())
			return
		}
		fmt.Fprintf(w, "%s, as a card of %s would change it (nothing is written):\n", card.Plan.Of, card.Command())
		for _, line := range card.Plan.Lines() {
			fmt.Fprintln(w, line)
		}
		fmt.Fprintf(w, "taken back with: %s\n", card.Back)
	})
}

// defChange is one setting of a crew member's or a group's definition that
// a write changes: a runner config key, or the flag of a field (--dir). Old
// and New are the values as printed, "" for none.
type defChange struct{ Key, Old, New string }

// defChanges lists what saving def over old changes, the fields first and
// the config keys by name. The order of --on is not a change.
func defChanges(old, def store.CrewMember) []defChange {
	var out []defChange
	add := func(key, was, is string) {
		if was != is {
			out = append(out, defChange{key, was, is})
		}
	}
	kinds := func(on []string) string {
		on = slices.Clone(on)
		slices.Sort(on)
		return strings.Join(on, ",")
	}
	add("--runner", old.Runner, def.Runner)
	add("--dir", old.Dir, def.Dir)
	add("--on", kinds(old.On), kinds(def.On))
	add("--max-age", old.MaxAge, def.MaxAge)
	add("--handoff-grace", old.HandoffGrace, def.HandoffGrace)
	add("--context-handoff", old.ContextHandoff, def.ContextHandoff)
	return append(out, configChanges(old.Config, def.Config)...)
}

// configChanges lists the runner config keys that differ between old and
// def, by name.
func configChanges(old, def map[string]any) []defChange {
	var out []defChange
	keys := sortedKeys(old)
	for _, k := range sortedKeys(def) {
		if _, had := old[k]; !had {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		was, had := old[k]
		is, has := def[k]
		// Compared as printed, which is how a value reads after the store:
		// a list given as []string and read back as []any is the same list.
		a, b := settingText(was, had), settingText(is, has)
		if had != has || a != b {
			if secretSetting(k) {
				a, b = hidden(had), hidden(has)
			}
			out = append(out, defChange{k, a, b})
		}
	}
	return out
}

// settingText is a config value as a change prints it: a string as it is,
// anything else as JSON.
func settingText(v any, set bool) string {
	if !set {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// secretSetting reports whether a config key is named like one that holds
// a secret. No setting yardr reads does (env_file names a file, and
// env_command is stored to be shown), and the store refuses a key nothing
// reads (store.ConfigKeys), but a yard set up before it did may hold one,
// and what a change prints ends up in a terminal's scrollback and an agent's
// transcript.
func secretSetting(key string) bool {
	k := strings.ToLower(key)
	for _, part := range []string{"token", "secret", "password", "passwd", "credential", "apikey", "api_key"} {
		if strings.Contains(k, part) {
			return true
		}
	}
	return k == "key" || strings.HasSuffix(k, "_key")
}

func hidden(set bool) string {
	if !set {
		return ""
	}
	return "(a value, not shown)"
}

// bareSetHint is the store's refusal of a config value as yardr group set
// and yardr crew set word it: the store's hint is --set's, which those
// commands have not, taking key=value bare.
func bareSetHint(err error) error {
	if err == nil || !strings.Contains(err.Error(), "write --set '") {
		return err
	}
	return errors.New(strings.Replace(err.Error(), "write --set '", "write '", 1))
}

// changeLines prints changes one to a line, indented: "model: opus -> sonnet".
func changeLines(changes []defChange) string {
	lines := make([]string, len(changes))
	for i, ch := range changes {
		lines[i] = fmt.Sprintf("  %s: %s -> %s", ch.Key, or(ch.Old, "(not set)"), or(ch.New, "(dropped)"))
	}
	return strings.Join(lines, "\n")
}
