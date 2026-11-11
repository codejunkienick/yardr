// The hook add row's table: a replacement of the hook that stands, or a
// new hook carrying --says, --makes, --map or --as. The --run word holds
// no spaces, -> or free-text notes, so says and makes lines ride beside
// argv (AskHookTable), while --as and --map are words of the command
// itself. The card is weighed trust as the safe shape's is, and shows the
// hook now and after: each table old-against-new as one unified diff,
// whole, in the pack and source replacement cards' pattern (Unified,
// AskFileDiff), with --as, the source where it changes, the --unknown
// word and the secret file now and after. A table past the card's caps is
// refused, not summarised. The secret file is never proposed: a
// replacement keeps the file it stands with, and a file of the asker's
// own stays in a terminal.
package store

import (
	"context"
	"fmt"
	"slices"
	"strings"

	bolt "go.etcd.io/bbolt"

	"yardr.dev/yardr/internal/source"
)

// AskHookTable is the says and makes lines a hook add ask carries beside
// argv, each in the order given: the first line an event matches counts,
// so order is part of what approval installs.
type AskHookTable struct {
	Says  []string `json:"says,omitempty"`
	Makes []string `json:"makes,omitempty"`
}

// MaxRunHookTable is the most table text a hook add ask carries: its
// says, makes and map lines with its --as crew member together. Past it
// the hook is typed in a terminal, where hook add takes any table.
const MaxRunHookTable = 8192

// hookTableEmpty reports whether run proposes no table: no --as, no
// --map, and no carried says or makes line. Such an ask is the safe
// shape's, and its card is the safe shape's.
func hookTableEmpty(run AskRun) bool {
	_, _, _, as, mapping := hookAddParts(run.Argv)
	if as != "" || len(mapping) > 0 {
		return false
	}
	return run.HookTable == nil || (len(run.HookTable.Says) == 0 && len(run.HookTable.Makes) == 0)
}

// hookTableOf reads a hook add's proposed table: --as and --map from
// argv, says and makes from what the ask carries. It refuses a table past
// the byte bound, a line of no shape, and a map that maps one key twice
// to two states, as the terminal does. What needs the yard's records (the
// flows' edges, the crew member's identity) is checkHookTable's.
func hookTableOf(run AskRun) (as string, mapping map[string]string, says []Say, makes []Make, err error) {
	_, _, _, as, mapLines := hookAddParts(run.Argv)
	var saysLines, makesLines []string
	if run.HookTable != nil {
		saysLines, makesLines = run.HookTable.Says, run.HookTable.Makes
	}
	total := len(as)
	for _, line := range mapLines {
		total += len(line)
	}
	for _, line := range saysLines {
		total += len(line)
	}
	for _, line := range makesLines {
		total += len(line)
	}
	if total > MaxRunHookTable {
		name, _ := hookAddOf(run.Argv)
		return "", nil, nil, nil, fmt.Errorf("hook %s: its table is %d bytes: a card holds at most %d, so this is the operator's to type in a terminal (yardr hook add %s --source <source> --secret-file <file>)", name, total, MaxRunHookTable, name)
	}
	mapping = map[string]string{}
	for _, line := range mapLines {
		key, state, ok := strings.Cut(strings.TrimSpace(line), "=")
		typ, stage, colon := strings.Cut(key, ":")
		if !ok || !colon || typ == "" || stage == "" || state == "" {
			return "", nil, nil, nil, fmt.Errorf("--map %q: want <bead type>:<stage>=<state>, e.g. task:review=review", line)
		}
		if was, dup := mapping[key]; dup && was != state {
			return "", nil, nil, nil, fmt.Errorf("--map %s: %s is mapped to %s already", line, key, was)
		}
		mapping[key] = state
	}
	if len(mapping) == 0 {
		mapping = nil
	}
	for _, line := range saysLines {
		say, err := ParseSay(line)
		if err != nil {
			return "", nil, nil, nil, fmt.Errorf("--says %w", err)
		}
		says = append(says, say)
	}
	for _, line := range makesLines {
		m, err := ParseMake(line)
		if err != nil {
			return "", nil, nil, nil, fmt.Errorf("--makes %w", err)
		}
		makes = append(makes, m)
	}
	return as, mapping, says, makes, nil
}

// hookDepots are the yard's depots' names, in bucket order, which is by
// name.
func hookDepots(tx *bolt.Tx) ([]string, error) {
	var out []string
	if err := each(tx, bDepots, "", func(_ []byte, d *Depot) error {
		out = append(out, d.Name)
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// hookDepotFlows are the flows that govern depot's beads, the depot's own
// first and then one per type that has its own, as DepotFlows lists them:
// each type's own where it has one, else the depot's. The repository's
// half was read before the write (readyHookAdd), since git does not run
// inside one.
func hookDepotFlows(tx *bolt.Tx, repos map[string]*RepoFlows, depot string) ([]DepotFlow, error) {
	repo := repos[depot]
	var types []string
	if err := each(tx, bFlowPtrs, join(depot, ""), func(_ []byte, p *FlowPointer) error {
		if p.Depot == depot && p.Type != "" {
			types = append(types, p.Type)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if repo != nil {
		for _, rf := range repo.Flows {
			if rf.Type != "" {
				types = append(types, rf.Type)
			}
		}
	}
	slices.Sort(types)
	var out []DepotFlow
	for _, t := range append([]string{""}, slices.Compact(types)...) {
		f, src, err := resolveFlow(tx, &depot, t, "", repo)
		if err != nil {
			return nil, err
		}
		if f != nil && src.Type == t {
			out = append(out, DepotFlow{Type: t, Flow: f, Source: *src})
		}
	}
	return out, nil
}

// addHookOutcomes unions the outcomes of flow f into out, by stage, over
// the stages a bead can report from: no terminal stage.
func addHookOutcomes(out map[string][]string, f *Flow) {
	if f == nil {
		return
	}
	for _, stage := range f.Stages {
		if f.IsTerminal(stage) {
			continue
		}
		if _, has := out[stage]; !has {
			out[stage] = nil
		}
		for _, o := range f.OutcomesFrom(stage) {
			if !slices.Contains(out[stage], o.Name) {
				out[stage] = append(out[stage], o.Name)
			}
		}
	}
}

// hookOutcomes are the outcomes a bead can report, by the stage it is in,
// under any flow of the yard's depots, as the terminal holds an untyped
// says line: the bead an event is about may be of any type.
func (w *wtx) hookOutcomes() (map[string][]string, error) {
	depots, err := hookDepots(w.Tx)
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, depot := range depots {
		flows, err := hookDepotFlows(w.Tx, w.repos, depot)
		if err != nil {
			return nil, fmt.Errorf("the flows of depot %s: %w", depot, err)
		}
		for _, df := range flows {
			addHookOutcomes(out, df.Flow)
		}
	}
	return out, nil
}

// hookOutcomesFor are the outcomes a bead of type typ can report, by the
// stage it is in, under the flow that type follows in each of the yard's
// depots, as the terminal holds a says line filtered on that type.
func (w *wtx) hookOutcomesFor(typ string) (map[string][]string, error) {
	depots, err := hookDepots(w.Tx)
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, depot := range depots {
		flows, err := hookDepotFlows(w.Tx, w.repos, depot)
		if err != nil {
			return nil, fmt.Errorf("the flows of depot %s: %w", depot, err)
		}
		var own *Flow
		for _, df := range flows {
			if df.Type == typ && df.Flow != nil {
				addHookOutcomes(out, df.Flow)
				own = nil
				break
			}
			if df.Type == "" {
				own = df.Flow
			}
		}
		if own != nil {
			addHookOutcomes(out, own)
		}
	}
	return out, nil
}

// hookMakeStages are the stages a bead of type typ can be made in in
// depot, as the terminal holds a makes line: the non-terminal stages of
// the flow the type follows there. Where no flow governs them any stage
// will do.
func (w *wtx) hookMakeStages(depot, typ string) (stages []string, governed bool, err error) {
	if w.Bucket(bDepots).Get([]byte(depot)) == nil {
		return nil, false, fmt.Errorf("depot %s: %w", depot, ErrNotFound)
	}
	flows, err := hookDepotFlows(w.Tx, w.repos, depot)
	if err != nil {
		return nil, false, fmt.Errorf("its flows: %w", err)
	}
	var f *Flow
	for _, df := range flows {
		if df.Flow != nil && (df.Type == typ || (df.Type == "" && f == nil)) {
			f = df.Flow
		}
	}
	if f == nil {
		return nil, false, nil
	}
	for _, stage := range f.Stages {
		if !f.IsTerminal(stage) {
			stages = append(stages, stage)
		}
	}
	return stages, true, nil
}

// checkHookTable refuses a proposed hook the terminal refuses: a line no
// bead could take the outcome of or be made in, a map without the crew
// member it acts as, and a crew member the yard has not or without an
// identity in the hook's source. Whether the approver may speak as the
// crew member is the run's own check (putHook), as the actor there, not
// the table's: the card is written in the asker's write.
func (w *wtx) checkHookTable(h Hook) error {
	keys := whereKeysTx(w.Tx, h.Source)
	if err := h.checkSays(append(slices.Clone(keys), SayStage)); err != nil {
		return err
	}
	if err := h.checkMakes(keys); err != nil {
		return err
	}
	if err := h.checkMap(); err != nil {
		return err
	}
	if err := h.checkCI(w.Tx); err != nil {
		return err
	}
	for _, say := range h.Says {
		line := say.String()
		if typ, ok := say.Where[SayType]; ok {
			for _, t := range WhereValues(typ) {
				typed, err := w.hookOutcomesFor(t)
				if err != nil {
					return err
				}
				if err := say.Unrouted(typed); err != nil {
					return fmt.Errorf("--says %q: no bead of type %s of the yard's depots could take it: %w", line, t, err)
				}
			}
			continue
		}
		stages, err := w.hookOutcomes()
		if err != nil {
			return err
		}
		if err := say.Unrouted(stages); err != nil {
			return fmt.Errorf("--says %q: no bead of the yard's depots could take it: %w", line, err)
		}
	}
	for _, m := range h.Makes {
		line := m.String()
		in := []string{m.Depot}
		if m.Depot == "" {
			depots, err := hookDepots(w.Tx)
			if err != nil {
				return err
			}
			if len(depots) == 0 {
				return fmt.Errorf("--makes %q: the yard has no depot to make a bead in", line)
			}
			in = depots
		}
		var why []string
		for _, depot := range in {
			stages, governed, err := w.hookMakeStages(depot, m.Type)
			if err == nil && governed {
				err = m.Unstaged(stages)
			}
			if err == nil {
				why = nil
				break
			}
			why = append(why, fmt.Sprintf("depot %s: %v", depot, err))
		}
		if why != nil {
			return fmt.Errorf("--makes %q: no bead of type %s could be made there: %s", line, m.Type, strings.Join(why, "; "))
		}
	}
	if h.As == "" {
		return nil
	}
	var r CrewMember
	if ok, err := get(w.Tx, bCrew, h.As, &r); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("hook %s: --as %s: crew member %s: %w", h.Name, h.As, h.As, ErrNotFound)
	}
	if r.Identities[h.Source].zero() {
		return fmt.Errorf("hook %s: --as %s: crew member %s has no identity in %s, so the delivery of a change made as it would reach its own inbox: yardr crew identity %s --source %s --id <its id there>",
			h.Name, h.As, h.As, h.Source, h.As, h.Source)
	}
	return nil
}

// hookMapLines are a hook's map as --map takes it, sorted, so a map
// reads the same however it was typed.
func hookMapLines(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	slices.Sort(out)
	return out
}

// hookTableDiff is one table old-against-new as its card section: the
// summary with its insertions and deletions counted, and the unified
// diff whole under it, each line its own. Empty where no line differs.
func hookTableDiff(kind, noun string, old, new []string) []string {
	diff := Unified(strings.Join(old, "\n"), strings.Join(new, "\n"))
	if diff == "" {
		return nil
	}
	ins, del := Counts(diff)
	out := []string{fmt.Sprintf("its %s changes from %s to %s (%s, %s):",
		kind, countNoun(len(old), noun), countNoun(len(new), noun), countNoun(ins, "insertion"), countNoun(del, "deletion"))}
	return append(out, strings.Split(strings.TrimSuffix(diff, "\n"), "\n")...)
}

// hookTableSection renders the proposed table against the standing one,
// nil for a new hook: the --as crew member now and after, and each
// table's changes, whole. A table that stands as it is says so in one
// line; one that is empty both ways says nothing.
func hookTableSection(standing *Hook, proposed Hook) []string {
	var out []string
	wasAs := ""
	if standing != nil {
		wasAs = standing.As
	}
	switch {
	case proposed.As != "" && wasAs == "":
		out = append(out, fmt.Sprintf("it acts as the crew member %s (now it acts as none).", proposed.As))
	case proposed.As != "" && proposed.As == wasAs:
		out = append(out, fmt.Sprintf("it acts as the crew member %s, as now.", proposed.As))
	case proposed.As != "":
		out = append(out, fmt.Sprintf("it acts as the crew member %s (now: %s).", proposed.As, wasAs))
	case wasAs != "":
		out = append(out, fmt.Sprintf("it acts as no crew member (now: %s).", wasAs))
	}
	var wasSays, wasMakes []string
	var wasMap map[string]string
	if standing != nil {
		wasSays, wasMakes, wasMap = SayLines(standing.Says), MakeLines(standing.Makes), standing.Map
	}
	is, maps := SayLines(proposed.Says), hookMapLines(proposed.Map)
	if diff := hookTableDiff("says table", "line", wasSays, is); diff != nil {
		out = append(out, diff...)
	} else if len(is) > 0 {
		out = append(out, fmt.Sprintf("its says table of %s stands as it is.", countNoun(len(is), "line")))
	}
	makes := MakeLines(proposed.Makes)
	if diff := hookTableDiff("makes table", "line", wasMakes, makes); diff != nil {
		out = append(out, diff...)
	} else if len(makes) > 0 {
		out = append(out, fmt.Sprintf("its makes table of %s stands as it is.", countNoun(len(makes), "line")))
	}
	if diff := hookTableDiff("map", "line", hookMapLines(wasMap), maps); diff != nil {
		out = append(out, diff...)
	} else if len(maps) > 0 {
		out = append(out, fmt.Sprintf("its map of %s stands as it is.", countNoun(len(maps), "line")))
	}
	return out
}

// hookTableTerminal ends a refusal of a hook's card for what a card cannot
// show: the hook is the operator's to type in a terminal, where hook add
// takes any table.
func hookTableTerminal(name, source string) string {
	return fmt.Sprintf("this is the operator's to type in a terminal (yardr hook add %s --source %s --secret-file <file>)", name, source)
}

// hookTableCard is the card of hook add <name> with a table: a replacement
// of the hook that stands, or a new hook carrying --says, --makes, --map
// or --as. It shows the hook now and after, whole: the source where it
// changes, the --unknown word where the source hears by identity, the
// secret file the hook names, and each table's diff. A replacement keeps
// the secret file it stands with. The approval is held to the hook whole
// and to the source (Seen).
func (w *wtx) hookTableCard(run AskRun, asking bool) (AskRun, error) {
	name, sourceName, unknownWord, _, _ := hookAddParts(run.Argv)
	as, mapping, says, makes, err := hookTableOf(run)
	if err != nil {
		return AskRun{}, err
	}
	if !hookName.MatchString(name) {
		return AskRun{}, fmt.Errorf("hook name %q: use lowercase letters, digits, - and _", name)
	}
	src, d, err := sourceIn(w.Tx, sourceName)
	if err != nil {
		return AskRun{}, err
	}
	d = d.WithBase(src.Base)
	if d.Hook == nil {
		return AskRun{}, fmt.Errorf("source %s: its declaration has no hook key, so the yard hears nothing from it", sourceName)
	}
	if unknownWord != "" && !ValidHookUnknown(unknownWord) {
		return AskRun{}, fmt.Errorf("--run takes %s: --unknown %q: want %s", hookAddUse, unknownWord, strings.Join(HookUnknowns, ", "))
	}
	identityHearing := d.HearsByIdentity()
	if unknownWord != "" && !identityHearing {
		return AskRun{}, fmt.Errorf("source %s: --unknown %s for a source with no identity hearing: source %s hears no sender by identity, so there is no unmapped sender to name a policy for", sourceName, unknownWord, sourceName)
	}
	proposed := Hook{Name: name, Source: sourceName, Unknown: unknownWord, As: as, Map: mapping, Says: says, Makes: makes}
	if err := w.checkHookTable(proposed); err != nil {
		return AskRun{}, err
	}
	effectiveUnknown := unknownWord
	if effectiveUnknown == "" && identityHearing {
		effectiveUnknown = HookUnknownUnknown
	}
	var standing Hook
	had, err := get(w.Tx, bHooks, name, &standing)
	if err != nil {
		return AskRun{}, err
	}
	fingerprint, err := w.sourceSeen(src)
	if err != nil {
		return AskRun{}, err
	}
	// A replacement that moves the source across a registration keeps
	// its secret and its URL but stands registered nowhere, or leaves
	// a webhook behind: registering is the terminal's.
	if had && standing.Source != sourceName {
		_, wasD, err := sourceIn(w.Tx, standing.Source)
		if err == nil && (wasD.Hook.RegistersSecret() || d.Hook.RegistersSecret()) {
			return AskRun{}, fmt.Errorf("hook %s: its source would move from %s to %s, and one of them registers its webhooks itself: %s", name, standing.Source, sourceName, hookTableTerminal(name, sourceName))
		}
	}
	var does []string
	seenMap := map[string]string{"source " + sourceName: fingerprint}
	if !had {
		seenMap["hook "+name] = ""
		if identityHearing {
			seenMap["hook "+name+" unknown"] = seen(effectiveUnknown)
		}
		if d.Hook.RegistersSecret() {
			register, publicURL, err := w.hookTableRegisterDoes(name, sourceName, d, effectiveUnknown, nil, proposed)
			if err != nil {
				return AskRun{}, err
			}
			does = register
			seenMap["setting "+HookPublicURLSetting] = seen(publicURL)
			return hookTableSized(run, name, sourceName, does, runCommandLine([]string{"hook", "rm", name}), "only the operator", seenMap, slices.Clone(d.Hook.Register.Ask))
		}
		does = []string{
			fmt.Sprintf("it adds the hook %s: POST /hooks/%s takes deliveries from the source %s (%s).", name, name, sourceName, sourceHost(d)),
		}
		if identityHearing {
			does = append(does, hookUnknownSays(effectiveUnknown))
		}
		does = append(does, hookTableSection(nil, proposed)...)
		does = append(does,
			hookSays(d),
			fmt.Sprintf("the signing secret is given on the hook's own secret card for %s and kept in %s (mode 0600); the hook names that file, and the value is in no note, event or answer.", name, HookSecretFile(w.home, name)),
		)
		return hookTableSized(run, name, sourceName, does, runCommandLine([]string{"hook", "rm", name}), "only the operator", seenMap, nil)
	}
	seenMap["hook "+name] = seen(standing.Source, standing.SecretFile, standing.Unknown, standing.As, standing.Map, standing.Says, standing.Makes)
	first := fmt.Sprintf("it replaces the hook %s: POST /hooks/%s takes deliveries from the source %s (%s).", name, name, sourceName, sourceHost(d))
	if standing.Source != sourceName {
		first += fmt.Sprintf(" It serves the source %s now.", standing.Source)
	}
	does = []string{first, hookSays(d)}
	wasUnknown, wasIdentity := w.hookUnknownState(standing)
	if identityHearing {
		line := hookUnknownSays(effectiveUnknown)
		switch {
		case wasIdentity && wasUnknown != effectiveUnknown:
			line += fmt.Sprintf(" Before, it stored --unknown %s.", wasUnknown)
		case !wasIdentity && wasUnknown != "":
			line += fmt.Sprintf(" Before, it did not hear by identity (stored --unknown %s).", wasUnknown)
		case !wasIdentity:
			line += " Before, it had no --unknown policy."
		}
		does = append(does, line)
	} else if wasIdentity {
		does = append(does, fmt.Sprintf("it has no --unknown policy now; before, it stored --unknown %s.", wasUnknown))
	} else if wasUnknown != "" {
		does = append(does, fmt.Sprintf("it has no identity hearing now; its stored --unknown %s was inactive.", wasUnknown))
	}
	does = append(does,
		fmt.Sprintf("the signing secret stays in %s (mode 0600), the file the hook stands with; the value is in no note, event or answer, and rotating it is the operator's in a terminal.", standing.SecretFile),
	)
	does = append(does, hookTableSection(&standing, proposed)...)
	return hookTableSized(run, name, sourceName, does, hookTableBack(standing), "only the operator", seenMap, nil)
}

// hookUnknownState is the policy a standing hook carries and whether its
// source hears it: an inactive value still matters as stored configuration,
// but only identity hearing gives it an effect.
func (w *wtx) hookUnknownState(h Hook) (string, bool) {
	_, d, err := sourceIn(w.Tx, h.Source)
	if err == nil && d.HearsByIdentity() {
		return h.UnknownPolicy(), true
	}
	return h.Unknown, false
}

// hookTableRegisterDoes is the card of a new hook with a table the yard
// registers itself: the register sentences with the table's section after
// what the hook stores for an unmapped sender. The phased approval reads
// the same composition, so what it compares is what the card showed.
func (w *wtx) hookTableRegisterDoes(name, sourceName string, d *source.Declaration, effectiveUnknown string, standing *Hook, proposed Hook) ([]string, string, error) {
	does, publicURL, err := w.hookRegisterDoes(name, sourceName, d)
	if err != nil {
		return nil, "", err
	}
	at := 1
	if d.HearsByIdentity() {
		does = append([]string{does[0], hookUnknownSays(effectiveUnknown)}, does[1:]...)
		at = 2
	}
	table := hookTableSection(standing, proposed)
	does = append(does[:at], append(table, does[at:]...)...)
	return does, publicURL, nil
}

// hookTableRestoreArgv is the hook add command carrying the table h
// stands with: --source with --unknown, --as and --map, for a card that
// brings the table back, with S: and M: lines beside it.
func hookTableRestoreArgv(h Hook) []string {
	argv := []string{"hook", "add", h.Name, "--source", h.Source}
	if h.Unknown != "" {
		argv = append(argv, "--unknown", h.Unknown)
	}
	if h.As != "" {
		argv = append(argv, "--as", h.As)
	}
	for _, line := range hookMapLines(h.Map) {
		argv = append(argv, "--map", line)
	}
	return argv
}

// hookTableBack is the way back from replacing standing: another hook add
// card carrying the table that stands now, with its lines on stdin beside
// the command, as this one carried its own.
func hookTableBack(standing Hook) string {
	back := "yardr bead ask <bead> - with Q: <question> and R: " + runCommandLine(hookTableRestoreArgv(standing))
	if len(standing.Says) > 0 || len(standing.Makes) > 0 {
		back += " and S: and M: lines of the table that stands now (yardr hook show " + standing.Name + " shows it)"
	}
	return back + " on stdin"
}

// hookTableSized refuses a hook table card past the card's line cap: a
// card is read whole, so a table it cannot hold is typed in a terminal.
func hookTableSized(run AskRun, name, source string, does []string, back, backBy string, seenMap map[string]string, ask []string) (AskRun, error) {
	if len(does) > MaxRunSourceCardLines {
		return AskRun{}, fmt.Errorf("hook %s: its card would run to %d lines: a card is read whole, so %s", name, len(does), hookTableTerminal(name, source))
	}
	out := AskRun{
		Argv:   run.Argv,
		Does:   does,
		Back:   back,
		BackBy: backBy,
		Seen:   seenMap,
		Ask:    ask,
	}
	if run.HookTable != nil {
		out.HookTable = &AskHookTable{Says: slices.Clone(run.HookTable.Says), Makes: slices.Clone(run.HookTable.Makes)}
	}
	return out, nil
}

// hookTableStale says why the ask seq, written as a replacement of the
// hook name, is approved no more: the hook is gone, another under its
// name, or the source it was written about is another.
func (w *wtx) hookTableStale(seq uint64, name, sourceName string, was, now AskRun) error {
	if err := w.staleSource(seq, sourceName, was, now); err != nil {
		return err
	}
	key := "hook " + name
	switch {
	case was.Seen[key] == "" && now.Seen[key] != "":
		e := lastEvent(w.Tx, "hook_added", naming("hook", name))
		return fmt.Errorf("ask %d was written when the hook %s was not in the yard: it was added since%s. %s", seq, name, since(e), nothingRan)
	case was.Seen[key] != "" && now.Seen[key] == "":
		e := lastEvent(w.Tx, "hook_removed", naming("hook", name))
		return fmt.Errorf("ask %d was written when the hook %s was in the yard: it was removed since%s. %s", seq, name, since(e), nothingRan)
	case was.Seen[key] != now.Seen[key]:
		e := lastEvent(w.Tx, "hook_added", naming("hook", name))
		return fmt.Errorf("ask %d was written when the hook %s was another: it was added again or changed since%s. %s", seq, name, since(e), nothingRan)
	}
	return staleCard(seq, was, now)
}

// readyHookAdd reads, before the write, the repository flows of every
// depot: the card holds each says line to the flow edges the yard's
// depots have, and each makes line to the stages they can be made in, and
// git does not run inside a write. An ask carrying no says or makes line
// reads none, so a broken checkout elsewhere breaks no safe-shape ask.
func readyHookAdd(ctx context.Context, s *Store, run AskRun, _ []string) (map[string]*RepoFlows, error) {
	if run.HookTable == nil || (len(run.HookTable.Says) == 0 && len(run.HookTable.Makes) == 0) {
		return nil, nil
	}
	depots, err := s.Depots(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]*RepoFlows{}
	for _, depot := range depots {
		repo, err := s.RepoFlows(ctx, depot.Name)
		if err != nil {
			return nil, err
		}
		out[depot.Name] = repo
	}
	return out, nil
}
