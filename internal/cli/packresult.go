package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"yardr.dev/yardr/internal/pack"
	"yardr.dev/yardr/internal/store"
)

// printResult says what an import did, after pack apply wrote it: its counts,
// what was pruned, left and kept, and then what is still owed for each of its
// answers, sources and stage maps.
func printResult(w io.Writer, plan *pack.Plan, res *pack.Result, pruning bool, answers pack.Answers, open *openAnswers, calls *callAnswers, expose *exposeAnswers, refused map[string]string, sets *notifySets) {
	fmt.Fprintln(w)
	if res.Wrote() {
		fmt.Fprintf(w, "applied pack %s to %s: %d created, %d changed, %d unchanged", res.Pack, inDepot(res.Depot), res.Created, res.Changed, res.Unchanged)
		if len(res.Pruned) > 0 {
			fmt.Fprintf(w, ", %d pruned", len(res.Pruned))
		}
		if len(res.Kept) > 0 {
			fmt.Fprintf(w, ", %d kept", len(res.Kept))
		}
		fmt.Fprintln(w)
	} else {
		fmt.Fprintln(w, appliedLine(plan, res))
	}
	for _, s := range res.Pruned {
		fmt.Fprintln(w, "  pruned  "+s)
	}
	// What the yard changed is left as it is, and the way to the pack's
	// version is in each line.
	for _, s := range res.Kept {
		fmt.Fprintln(w, "  kept: "+s)
	}
	if len(res.Left) > 0 {
		hint := "yardr pack apply … --prune removes them"
		if pruning {
			hint = "held, or their removal failed"
		}
		fmt.Fprintf(w, "no longer in the pack and left in the yard (%s):\n", hint)
		for _, s := range res.Left {
			fmt.Fprintln(w, "  "+s)
		}
	}
	for _, e := range res.PruneErrors {
		fmt.Fprintln(w, "  not removed: "+e)
	}
	for _, o := range plan.Objects {
		id := o.Kind + "." + o.Name + ".args"
		if !slices.Contains(o.Asks, id) || strings.TrimSpace(answers[id]) != "" {
			continue
		}
		fmt.Fprintf(w, "%s %s is new and has no agent arguments: an unattended agent stops at every permission prompt until it is given them (yardr %s add, the form)\n", kindWord(o.Kind), o.Name, o.Kind)
	}
	for _, g := range res.Granted {
		fmt.Fprintf(w, "crew member %s may now open threads in %s\n", g.CrewMember, strings.Join(g.Sources, ", "))
	}
	// A no changes nothing in the yard, so it is said: it is on record.
	for _, name := range sortedKeys(open.Answers) {
		var no []string
		for _, src := range sortedKeys(open.Answers[name]) {
			if !open.Answers[name][src] {
				no = append(no, src)
			}
		}
		if len(no) > 0 {
			fmt.Fprintf(w, "crew member %s was not let open threads in %s: the no is recorded and not asked again (--open-threads %s=%s grants it after all)\n",
				name, strings.Join(no, ", "), packName(plan.Crew, name), strings.Join(no, ","))
		}
	}
	for _, n := range open.Notes {
		fmt.Fprintln(w, n)
	}
	printExposed(w, res, expose)
	for _, m := range res.Mapped {
		if m.As != "" {
			fmt.Fprintf(w, "hook %s now moves tracked %s issues as %s: %s\n", m.Hook, m.Source, m.As, strings.Join(pack.MapLines(m.Map), " "))
		}
		if len(m.Says) > 0 {
			fmt.Fprintf(w, "hook %s's events now say of a bead:\n", m.Hook)
			for _, l := range store.SayLines(m.Says) {
				fmt.Fprintf(w, "  %s\n", l)
			}
		}
		if len(m.Makes) > 0 {
			fmt.Fprintf(w, "hook %s's events now make a bead for a thread none tracks:\n", m.Hook)
			for _, l := range store.MakeLines(m.Makes) {
				fmt.Fprintf(w, "  %s\n", l)
			}
		}
	}
	// What the import did not do for a stage map, each with who does it and
	// how: it makes no hook, gives no identity and grants no set_state. A
	// source the yard has nothing of is not said again: the plan said it
	// once, as optional.
	optional := map[string]bool{}
	for _, o := range plan.Optional {
		optional[o.Source] = true
	}
	for _, m := range plan.Maps {
		if optional[m.Source] {
			continue
		}
		subject := "the stage map of " + m.Source + " was"
		if m.Pack == "" {
			subject = "what the events of " + m.Source + " say was"
		}
		if m.Skipped != "" {
			fmt.Fprintf(w, "%s skipped: %s\n", subject, m.Skipped)
			continue
		}
		if why := refused[m.Source]; why != "" {
			fmt.Fprintf(w, "%s not set on hook %s: %s. The rest of the import does not depend on it\n", subject, m.Hook, why)
		}
		for _, what := range []string{"hook", "identity"} {
			if detail := m.Missing(what); detail != "" {
				fmt.Fprintf(w, "%s not set: %s\n", subject, detail)
			}
		}
		if detail := m.Missing(store.SetState); detail != "" {
			fmt.Fprintln(w, "note: "+detail)
		}
	}
	// A declared source: what was added, what --source-env and --notify
	// set for it, and what the import leaves to the operator, each with
	// its command.
	for _, d := range plan.Declared {
		switch {
		case refused[d.Source] != "":
			fmt.Fprintf(w, "the source %s was not added: %s. The rest of the import does not depend on it\n", d.Source, refused[d.Source])
		case slices.Contains(res.Declared, d.Source):
			fmt.Fprintf(w, "source %s is declared: %s under %s (yardr source show %s)\n", d.Source, plural(len(d.Calls), "call"), d.Base, d.Source)
		}
		if refused[d.Source] == "" && slices.Contains(res.Declared, d.Source) {
			var files []string
			for _, c := range d.Commands {
				if c.SHA256 != "" && !slices.Contains(files, c.Program) {
					files = append(files, c.Program)
				}
			}
			if len(files) > 0 {
				fmt.Fprintf(w, "  its commands' files are under %s, each mode 0700: %s\n", filepath.Join(plan.Home, "sources", d.Source), strings.Join(files, ", "))
			}
		}
		if sets != nil {
			for _, line := range sets.Set[d.Source] {
				fmt.Fprintln(w, line)
			}
			for _, line := range sets.Skip[d.Source] {
				fmt.Fprintln(w, line)
			}
		}
		if refused[d.Source] != "" {
			continue
		}
		for _, n := range d.Needs {
			if n.Status != "ok" {
				fmt.Fprintf(w, "next, for source %s: %s\n", d.Source, n.Detail)
			}
		}
	}
	// After the sources: a call is granted of one the yard has.
	printCallResult(w, plan, res, calls)
	for _, r := range res.Restart {
		fmt.Fprintf(w, "crew member %s is running and was not restarted: its role or subscriptions changed, so restart it (yardr crew restart %s)\n", r, r)
	}
	switch {
	case plan.SetupFile == "":
	case res.Wrote():
		fmt.Fprintln(w)
		printSetup(w, plan.Pack, plan.SetupFile, plan.Setup)
	default:
		// Nothing changed: name the command that prints the notes,
		// not the text.
		fmt.Fprintln(w)
		fmt.Fprintf(w, "pack %s has setup notes: yardr pack show %s prints them\n", plan.Pack, plan.Pack)
	}
}
