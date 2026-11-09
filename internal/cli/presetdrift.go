package cli

import (
	"fmt"
	"slices"
	"strings"

	"yardr.dev/yardr/internal/agents"
	"yardr.dev/yardr/internal/config"
	"yardr.dev/yardr/internal/store"
)

// presetDriftNotes are what yardr doctor notes of a herdr group whose
// arguments were set from one of its kind's permission presets and are now
// behind it. A harness file can give a preset another argument later (the
// Codex `read only` preset gained a writable temporary directory), but a
// group keeps the list it was given: a pack's import leaves a group's
// arguments as they are, so nothing else brings it up, and its sessions run
// in the sandbox of the older list. The group records no label of the
// preset, so it is taken to have been set from one when its arguments hold
// one of the preset's markers (presetMarker) and not every argument the
// preset has now. The note names the arguments the group lacks and the
// command that sets the preset's; that command replaces the group's own, so
// the note also names those it would drop. Notes, not faults: the yard runs
// as it did, and a group that left an argument out on purpose is named on
// every run. def is the yard's default_kind, which a group that names no
// kind starts.
func presetDriftNotes(g store.Group, def, yardr string) []string {
	if g.Runner != "herdr" {
		return nil
	}
	kind := or(g.Kind(def), agents.DefaultKind)
	h, ok := agents.Lookup(kind)
	if !ok {
		return nil
	}
	have := config.StringList(g.Config["args"])
	var notes []string
	for _, p := range h.Flags.Permissions {
		if !slices.ContainsFunc(have, func(arg string) bool { return presetMarker(h.Flags.Permissions, p, arg) }) {
			continue
		}
		lacks := runsNotIn(p.Args, have)
		if len(lacks) == 0 {
			continue
		}
		replaces := ""
		if dropped := runsNotIn(have, p.Args); len(dropped) > 0 {
			replaces = fmt.Sprintf(" (which replace its own, dropping %s)", quotedRuns(dropped))
		}
		notes = append(notes, fmt.Sprintf("group %s starts %s agents with arguments behind the %s preset: they lack %s, which the preset has. Set the preset's arguments%s: %s group set %s preset=%s",
			g.Name, kind, p.Label, quotedRuns(lacks), replaces, yardr, g.Name, store.PresetLabel(p.Label)))
	}
	return notes
}

// presetMarker says whether arg is a marker of the preset p among all the
// presets of its kind: one of p's arguments that assigns a setting
// (key=value) and that no other preset carries, so that a group holding it
// was set from p. A bare flag or word (-c, --enable, auto) is not one: an
// operator passes those for reasons of their own. A preset that changes one
// setting is still known by the settings it did not change.
func presetMarker(all []store.Permission, p store.Permission, arg string) bool {
	return strings.Contains(arg, "=") && slices.Contains(p.Args, arg) &&
		!slices.ContainsFunc(all, func(q store.Permission) bool { return q.Label != p.Label && slices.Contains(q.Args, arg) })
}

// runsNotIn are the words of words that in does not hold, a word as often as
// it is lacking: a flag the preset has five times and a group has four is
// lacking once, though the group holds the word. They come in runs, the words
// lacking one after another, so that a flag is named with its value.
func runsNotIn(words, in []string) [][]string {
	held := make(map[string]int, len(in))
	for _, w := range in {
		held[w]++
	}
	var runs [][]string
	after := false // the word before this one is lacking
	for _, w := range words {
		if held[w] > 0 {
			held[w]--
			after = false
			continue
		}
		if after {
			runs[len(runs)-1] = append(runs[len(runs)-1], w)
		} else {
			runs = append(runs, []string{w})
		}
		after = true
	}
	return runs
}

// quotedRuns are runs as a terminal would take them: each word one word, the
// words of a run after one another and the runs apart by commas.
func quotedRuns(runs [][]string) string {
	parts := make([]string, len(runs))
	for i, run := range runs {
		quoted := make([]string, len(run))
		for j, w := range run {
			quoted[j] = shellQuote(w)
		}
		parts[i] = strings.Join(quoted, " ")
	}
	return strings.Join(parts, ", ")
}
