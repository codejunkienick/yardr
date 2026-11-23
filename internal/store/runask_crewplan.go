// The crew set row of the run list (runList) for the strong few: a crew
// member's prompt or a permission preset that never asks, changed on a card
// and set only on the operator's approval (design yardr-m90n 3.5), as
// runask_groupset.go does for a group. It is a row of the set-a-field engine
// (runask_setfield.go): this file plans it and words its fields, and the
// engine cards the plan, holds the approval to it and asks again when it
// reads otherwise.
//
// It is the second row to begin with crew set: the first is a member's
// grants (runask_crewgrant.go), which answers errNotRow for words that name
// a prompt, a preset or one of crewOwnKeys, and this row for words that name
// none of them. The yardmaster's own entry is proposable here, and nowhere
// else: its prompt is the instructions it obeys and its preset what it may
// do without asking, which no telling after the run can police, since the
// teller is the changed party (3.3). The delegated leg never carries that
// entry; a card shown to the operator is the door, and so it is the door to
// the rest of that entry too, which the leg carries for every other member:
// its kind, model and directory, and any preset of its kind (the strong ones
// are the card's for every member). Words that name those for another member
// are refused where the yard's records are read.
//
// A prompt rides as a crew add's does, beside argv (AskRun.Role), but as a
// group set's names it: a file under the yard's home, held to the text at
// the card and at the approval, which writes the text to a file of the
// yard's own (runask_promptfile.go) for the member to read at each start.
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
)

// crewSetUse is how a refusal names the crew set row of the strong few.
const crewSetUse = "yardr crew set <name> prompt=<file> | preset=<preset> | kind=<kind> | model=<model> | dir=<dir>"

// crewSetKeys are the settings the row carries, in the order its stored form
// writes them.
var crewSetKeys = []string{"prompt", "preset", "kind", "model", "dir"}

// crewOwnKeys are the settings of the row that only the yardmaster's own
// entry carries: the delegated leg carries them for any other member
// (crewDelegates) and never for that entry (3.3).
var crewOwnKeys = []string{"kind", "model", "dir"}

func init() {
	runList = append(runList, setRow[crewSetSpec]{
		words:       []string{"crew", "set"},
		use:         crewSetUse,
		description: "Change some settings of a crew member and keep the rest",
		parse:       parseCrewSet,
		command:     "crew set",
		kind:        "crew_defined",
		key:         "crew",
		opens:       "changes the %s: what each of its sessions runs, as the lines below say, and nothing else of it.",
		plan:        (*wtx).crewSetPlan,
		check: func(ctx context.Context, w *wtx, spec crewSetSpec, actor string) error {
			return w.checkCrewDefine(ctx, spec.have, spec.want, true, actor)
		},
		lines: map[string]func(*wtx, AskRun, RunPlan, PlanChange) []string{
			"prompt": func(w *wtx, run AskRun, _ RunPlan, c PlanChange) []string {
				return w.promptLines(c.Before, c.After, run.Role)
			},
			"kind":  crewKindLines,
			"model": crewModelLines,
			"--dir": func(_ *wtx, _ AskRun, _ RunPlan, c PlanChange) []string {
				return []string{fmt.Sprintf("It works in %s now.", or(c.Before, "the yard's home")), fmt.Sprintf("It will work in %s.", c.After)}
			},
			"args": crewPresetLines,
		},
		order:     []string{"prompt", "kind", "model", "--dir", "args"},
		backOrder: []string{"prompt", "args", "kind", "model"},
		backBy:    "only the operator",
		seen: func(run AskRun, spec crewSetSpec) map[string]string {
			_, sets := strongSetOf(run.Argv)
			_, model := sets["model"]
			_, dir := sets["dir"]
			return map[string]string{"crew " + spec.name: crewSetSeen(&spec.have, model, dir)}
		},
		weigh: crewSetWeight,
		apply: runCrewSet,
	}.row())
}

// crewSetNamesStrong says whether the words of a crew set name the strong
// few, which are this row's and not the grants row's.
func crewSetNamesStrong(rest []string) bool {
	for _, word := range rest {
		if strongKey(word, crewSetKeys) {
			return true
		}
	}
	return false
}

// parseCrewSet reads the words of crew set <name> that carry the strong few.
// It reads no record of the yard. Words that carry none are not this row's:
// they are the grants row's, or the operator's alone.
func parseCrewSet(rest []string) ([]string, error) {
	name, sets, err := strongSetWords(crewSetUse, "crew set", "crew member", crewSetKeys, rest)
	if err != nil {
		return nil, err
	}
	return strongSetArgv(name, sets, crewSetKeys), nil
}

// crewSetSpec is what the crew set plan keeps for its apply: the member as
// it stands and as the approval would leave it (its directory included,
// which is no config key), the config keys the approval writes and the
// prompt text its file was held to.
type crewSetSpec struct {
	name   string
	have   CrewMember
	want   CrewMember
	set    map[string]any
	prompt string
}

// crewSetPlan plans a crew set ask against the yard as it stands: the member
// is there, a prompt is a file under the yard's home that holds the text the
// ask carries, a kind is one the yard has a harness for, a model is no flag,
// a directory is under a place the yard knows, a preset is one of the kind
// the agents will start as (that of any other member's that never asks, any
// of the yardmaster's own entry), and the config that results is one the
// store would save (configProblems, as putCrew holds it). Kind, model and
// directory are the yardmaster's own entry's alone.
//
// The plan changes the prompt, the args a preset names, the kind, the model
// and the directory, each where it stands otherwise now, and holds what the
// card's sentences read beside them: the member's runner, the prompt, kind
// and flags it runs now, the kind its agents start as (the yard's
// default_kind where it names none), the preset a card names, and what the
// prompt file it runs now holds.
func (w *wtx) crewSetPlan(run AskRun) (RunPlan, crewSetSpec, error) {
	name, sets := strongSetOf(run.Argv)
	spec := crewSetSpec{name: name, set: map[string]any{}}
	subject := "crew member " + name
	if ok, err := get(w.Tx, bCrew, name, &spec.have); err != nil {
		return RunPlan{}, spec, err
	} else if !ok {
		return RunPlan{}, spec, fmt.Errorf("%s: %w", subject, ErrNotFound)
	}
	own, err := w.ownEntry(name)
	if err != nil {
		return RunPlan{}, spec, err
	}
	for _, key := range crewOwnKeys {
		if _, ok := sets[key]; ok && !own {
			return RunPlan{}, spec, fmt.Errorf("%s: %s is on this card for the yardmaster's own entry alone, which the delegated leg never carries: %s", subject, key, strongApart)
		}
	}
	spec.want = spec.have
	if path, ok := sets["prompt"]; ok {
		saved, err := w.heldPrompt(name, subject, path, run.Role, promptWords{
			otherwise: "the prompt is set in a terminal, with yardr crew set " + name + " prompt=<file>", spelled: "prompt=<path>"})
		if err != nil {
			return RunPlan{}, spec, err
		}
		spec.set["prompt"], spec.prompt = saved, run.Role
	} else if run.Role != "" {
		return RunPlan{}, spec, fmt.Errorf("%s: the ask carries prompt text but names no prompt: name the file it was read from as prompt=<path>", subject)
	}
	was, err := w.kindOf(spec.have.Config)
	if err != nil {
		return RunPlan{}, spec, err
	}
	kind := was
	if k, ok := sets["kind"]; ok {
		if err := checkHarnessKind(subject, k); err != nil {
			return RunPlan{}, spec, err
		}
		spec.set["kind"], kind = k, k
	}
	if model, ok := sets["model"]; ok {
		if err := checkCardModel(subject, model); err != nil {
			return RunPlan{}, spec, err
		}
		spec.set["model"] = model
	}
	if dir, ok := sets["dir"]; ok {
		clean, err := w.knownDir(subject, "dir", dir, "dir=<dir>")
		if err != nil {
			return RunPlan{}, spec, err
		}
		spec.want.Dir = clean
	}
	if label, ok := sets["preset"]; ok {
		var p Permission
		if own {
			var found bool
			if p, found = HarnessPreset(kind, label); !found {
				return RunPlan{}, spec, noPreset(subject, kind, label, "one")
			}
		} else if p, err = strongPreset(subject, kind, label); err != nil {
			return RunPlan{}, spec, err
		}
		args := make([]any, len(p.Args))
		for i, a := range p.Args {
			args[i] = a
		}
		spec.set["args"] = args
	}
	spec.want.Config = maps.Clone(spec.have.Config)
	if spec.want.Config == nil {
		spec.want.Config = map[string]any{}
	}
	maps.Copy(spec.want.Config, spec.set)
	if ps := configProblems(CrewConfig, name, spec.have.Runner, spec.want.Config, spec.have.Config); len(ps) > 0 {
		return RunPlan{}, spec, ps[0]
	}
	have, want := asStored(spec.have.Config), asStored(spec.want.Config)
	value := func(c map[string]any, key string) string { v, ok := c[key]; return planValue(v, ok) }
	plan := RunPlan{Of: subject, Holds: map[string]string{
		"runner": spec.have.Runner, "prompt": value(have, "prompt"), "kind": value(have, "kind"),
		"args": value(have, "args"), "agents": was,
	}}
	if label, ok := sets["preset"]; ok {
		plan.Holds["preset"] = label
	}
	for _, key := range []string{"prompt", "args", "kind", "model"} {
		plan.Changes = planned(plan.Changes, key, value(have, key), value(want, key))
	}
	plan.Changes = planned(plan.Changes, "--dir", spec.have.Dir, spec.want.Dir)
	// The card shows what the prompt file it runs now holds, which no
	// record keeps: the plan holds its reading, so a file rewritten since
	// reads as another plan.
	if c, ok := plan.change("prompt"); ok && c.Before != "" {
		text, why, rewritten := w.promptBefore(c.Before)
		plan.Holds["prompt text"] = seen(text, why, rewritten)
	}
	return plan, spec, nil
}

// heldArgs is a plan's args value read back as config holds it: a list from
// its JSON, a string as it is.
func heldArgs(text string) any {
	var v any
	if json.Unmarshal([]byte(text), &v) == nil {
		return v
	}
	return text
}

// crewAgents is the kind the plan's member will start its agents as.
func crewAgents(p RunPlan) string {
	if c, ok := p.change("kind"); ok {
		return c.After
	}
	return p.Holds["agents"]
}

// crewKindLines are the card's lines for a kind that changes: the kind its
// agents start as now (the yard's default_kind, where the member names none)
// and as they will. The flags it holds are not the kind's to translate:
// where the card sets no preset the card says they stay as they are.
func crewKindLines(_ *wtx, _ AskRun, p RunPlan, c PlanChange) []string {
	was := p.Holds["agents"]
	now := fmt.Sprintf("It starts %s (%s) agents now", was, or(HarnessLabels[was], was))
	if c.Before == "" {
		now += ", the yard's default_kind"
	}
	lines := []string{now + ".", fmt.Sprintf("It will start %s (%s) agents.", c.After, or(HarnessLabels[c.After], c.After))}
	if _, ok := p.Holds["preset"]; !ok {
		if words := argWords(heldArgs(p.Holds["args"])); len(words) > 0 {
			lines = append(lines, fmt.Sprintf("It starts them with %s, and this card does not change those flags.", quotedWords(words)))
		}
	}
	return lines
}

// crewModelLines are the card's lines for a model that changes.
func crewModelLines(_ *wtx, _ AskRun, _ RunPlan, c PlanChange) []string {
	now := "the agent's own default model"
	if c.Before != "" {
		now = "the model " + c.Before
	}
	return []string{fmt.Sprintf("It starts them with %s now.", now), fmt.Sprintf("It will start them with the model %s.", c.After)}
}

// crewPresetLines are the card's lines for the flags a preset sets: the
// preset is one of the kind the agents will start as, which the plan read it
// of.
func crewPresetLines(_ *wtx, _ AskRun, p RunPlan, c PlanChange) []string {
	kind := crewAgents(p)
	preset, _ := HarnessPreset(kind, p.Holds["preset"])
	return presetLines(p.Holds["agents"], kind, heldArgs(c.Before), preset)
}

// crewSetWeight weighs a crew set card: the member's sessions run a program
// the card shows, its prompt, the agent it starts and where, or the flags it
// starts it with.
func crewSetWeight(run AskRun) []AskWeight {
	name, sets := strongSetOf(run.Argv)
	return []AskWeight{weighs(WeightCode, fmt.Sprintf("%s of the crew member %s, which each session the yard starts for it then runs as the card shows", andList(strongChanged(sets)), name))}
}

// runCrewSet sets the member as actor, through the store function the
// terminal's crew set calls (putCrew), so the checks that function makes are
// the same: the approver is the operator, who may define a crew member. The
// plan was made once more in this write and held to the card's, so what is
// saved is what the card showed. It answers the lines the command prints for
// what changed.
func runCrewSet(ctx context.Context, w *wtx, _ AskRun, spec crewSetSpec, actor string, by *approvedOn) (string, error) {
	r, err := w.putCrew(ctx, spec.want, actor, by)
	if err != nil {
		return "", err
	}
	if path, ok := spec.set["prompt"].(string); ok {
		if err := w.writePrompt("crew member "+spec.name, path, spec.prompt); err != nil {
			return "", err
		}
	}
	lines := []string{fmt.Sprintf("crew member %s saved", spec.name)}
	var atStart []string
	// The terminal's order: the field first, then the config keys by name.
	if spec.want.Dir != spec.have.Dir {
		lines = append(lines, fmt.Sprintf("  --dir: %s -> %s", or(spec.have.Dir, "(not set)"), or(spec.want.Dir, "(dropped)")))
		atStart = append(atStart, "--dir")
	}
	for _, key := range []string{"args", "kind", "model", "prompt"} {
		was, had := spec.have.Config[key]
		is, has := spec.want.Config[key]
		if a, b := grantSettingText(was, had), grantSettingText(is, has); had != has || a != b {
			lines = append(lines, fmt.Sprintf("  %s: %s -> %s", key, or(a, "(not set)"), or(b, "(dropped)")))
			atStart = append(atStart, key)
		}
	}
	// A session that runs keeps what it started with: the change is its next
	// session's, as the terminal's crew set says.
	if r.Live() && len(atStart) > 0 {
		lines = append(lines, fmt.Sprintf("%s is running and keeps its session: its next one starts with the new %s (yardr crew restart %s)", spec.name, strings.Join(atStart, ", "), spec.name))
	}
	return strings.Join(lines, "\n"), nil
}
