// The crew set row of the run list (runList) for the strong few: a crew
// member's prompt or a permission preset that never asks, changed on a card
// and set only on the operator's approval (design yardr-m90n 3.5), as
// runask_groupset.go does for a group. It is the second row to begin with
// crew set: the first is a member's grants (runask_crewgrant.go), which
// answers errNotRow for words that name a prompt, a preset or one of
// crewOwnKeys, and this row for words that name none of them. The
// yardmaster's own entry is proposable here, and nowhere else: its prompt is
// the instructions it obeys and its preset what it may do without asking,
// which no telling after the run can police, since the teller is the changed
// party (3.3). The delegated leg never carries that entry; a card shown to
// the operator is the door, and so it is the door to the rest of that entry
// too, which the leg carries for every other member: its kind, model and
// directory, and any preset of its kind (the strong ones are the card's for
// every member). Words that name those for another member are refused where
// the yard's records are read.
//
// A prompt rides as a crew add's does, beside argv (AskRun.Role), but as a
// group set's names it: a file under the yard's home, held to the text at
// the card and at the approval, which writes the text to a file of the
// yard's own (runask_promptfile.go) for the member to read at each start.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"reflect"
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
	runList = append(runList, runCommand{
		words:       []string{"crew", "set"},
		use:         crewSetUse,
		description: "Change some settings of a crew member and keep the rest",
		parse:       parseCrewSet,
		checkRun:    checkCrewSet,
		cardRun:     (*wtx).crewSetCard,
		stale:       (*wtx).crewSetStale,
		weigh:       crewSetWeight,
		runRun:      runCrewSet,
	})
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

// crewSetSpec is a crew set proposal as the yard holds it: the member as it
// stands and as the approval would leave it (its directory included, which is
// no config key), the config keys the approval writes, the prompt text its
// file was held to, the kind its agents start as now (was) and as the
// approval would leave them, and the preset resolved to the flags of that
// kind.
type crewSetSpec struct {
	name      string
	have      CrewMember
	want      CrewMember
	set       map[string]any
	prompt    string
	was, kind string
	preset    Permission
}

// crewSetProposal holds a crew set ask to the yard as it stands: the member
// is there, a prompt is a file under the yard's home that holds the text the
// ask carries, a kind is one the yard has a harness for, a model is no flag,
// a directory is under a place the yard knows, a preset is one of the kind
// the agents will start as (that of any other member's that never asks, any
// of the yardmaster's own entry), and the config that results is one the
// store would save (configProblems, as putCrew holds it). Kind, model and
// directory are the yardmaster's own entry's alone.
func (w *wtx) crewSetProposal(run AskRun) (crewSetSpec, error) {
	name, sets := strongSetOf(run.Argv)
	spec := crewSetSpec{name: name, set: map[string]any{}}
	if ok, err := get(w.Tx, bCrew, name, &spec.have); err != nil {
		return crewSetSpec{}, err
	} else if !ok {
		return crewSetSpec{}, fmt.Errorf("crew member %s: %w", name, ErrNotFound)
	}
	subject := "crew member " + name
	own, err := w.ownEntry(name)
	if err != nil {
		return crewSetSpec{}, err
	}
	for _, key := range crewOwnKeys {
		if _, ok := sets[key]; ok && !own {
			return crewSetSpec{}, fmt.Errorf("%s: %s is on this card for the yardmaster's own entry alone, which the delegated leg never carries: %s", subject, key, strongApart)
		}
	}
	spec.want = spec.have
	if path, ok := sets["prompt"]; ok {
		saved, err := w.heldPrompt(name, subject, path, run.Role, promptWords{
			otherwise: "the prompt is set in a terminal, with yardr crew set " + name + " prompt=<file>", spelled: "prompt=<path>"})
		if err != nil {
			return crewSetSpec{}, err
		}
		spec.set["prompt"], spec.prompt = saved, run.Role
	} else if run.Role != "" {
		return crewSetSpec{}, fmt.Errorf("%s: the ask carries prompt text but names no prompt: name the file it was read from as prompt=<path>", subject)
	}
	if spec.was, err = w.kindOf(spec.have.Config); err != nil {
		return crewSetSpec{}, err
	}
	spec.kind = spec.was
	if kind, ok := sets["kind"]; ok {
		if err := checkHarnessKind(subject, kind); err != nil {
			return crewSetSpec{}, err
		}
		spec.set["kind"], spec.kind = kind, kind
	}
	if model, ok := sets["model"]; ok {
		if err := checkCardModel(subject, model); err != nil {
			return crewSetSpec{}, err
		}
		spec.set["model"] = model
	}
	if dir, ok := sets["dir"]; ok {
		clean, err := w.knownDir(subject, "dir", dir, "dir=<dir>")
		if err != nil {
			return crewSetSpec{}, err
		}
		spec.want.Dir = clean
	}
	if label, ok := sets["preset"]; ok {
		var p Permission
		if own {
			var found bool
			if p, found = HarnessPreset(spec.kind, label); !found {
				return crewSetSpec{}, noPreset(subject, spec.kind, label, "one")
			}
		} else if p, err = strongPreset(subject, spec.kind, label); err != nil {
			return crewSetSpec{}, err
		}
		args := make([]any, len(p.Args))
		for i, a := range p.Args {
			args[i] = a
		}
		spec.preset, spec.set["args"] = p, args
	}
	spec.want.Config = maps.Clone(spec.have.Config)
	if spec.want.Config == nil {
		spec.want.Config = map[string]any{}
	}
	maps.Copy(spec.want.Config, spec.set)
	if ps := configProblems(CrewConfig, name, spec.have.Runner, spec.want.Config, spec.have.Config); len(ps) > 0 {
		return crewSetSpec{}, ps[0]
	}
	return spec, nil
}

// checkCrewSet is setting a crew member as the asker: what defines what the
// yard runs is the operator's, which a run ask may propose, the yardmaster's
// own entry included. The proposal is held first, so a prompt, preset or
// config the yard would refuse is refused in its own words whoever asks. The
// gate then is the store's own (checkCrewDefine), which marks what it holds
// to the operator alone: this row is what proposes it, so the mark is
// changed here and nowhere else.
func checkCrewSet(ctx context.Context, w *wtx, run AskRun, actor string) error {
	spec, err := w.crewSetProposal(run)
	if err != nil {
		return err
	}
	// The gate lets a change of nothing through as it lets anyone's, which
	// would read as a command to run yourself.
	if reflect.DeepEqual(asStored(spec.have.Config), asStored(spec.want.Config)) && spec.have.Dir == spec.want.Dir {
		return fmt.Errorf("crew member %s is set so already, so the command would change nothing", spec.name)
	}
	err = w.checkCrewDefine(ctx, spec.have, spec.want, true, actor)
	var o *OperatorOnlyError
	if errors.As(err, &o) {
		return &OperatorOnlyError{msg: o.msg, ask: Askable}
	}
	return err
}

// crewSetCard is the card of crew set <name> for the strong few: the member,
// then for each setting the card carries what stands now and what the
// approval sets, each whole. Asking refuses a proposal the yard would refuse
// and one that would change nothing (checkCrewSet); an approval reads it
// again, and stale says what moved. The card holds the member's fingerprint
// and the new prompt's SHA-256 in Seen, and the prompt text in Role.
func (w *wtx) crewSetCard(run AskRun, _ bool) (AskRun, error) {
	spec, err := w.crewSetProposal(run)
	if err != nil {
		return AskRun{}, err
	}
	_, sets := strongSetOf(run.Argv)
	does := []string{fmt.Sprintf("changes the crew member %s: what each of its sessions runs, as the lines below say, and nothing else of it.", spec.name)}
	c := asStored(spec.have.Config)
	// What the member held of the settings the card carries beyond the first
	// four is kept only by a card that carries them, so that a card written
	// before the row carried them is the card it was.
	held := []any{spec.have.Runner, c["prompt"], c["kind"], c["args"]}
	if _, ok := sets["model"]; ok {
		held = append(held, c["model"])
	}
	if _, ok := sets["dir"]; ok {
		held = append(held, spec.have.Dir)
	}
	seenMap := map[string]string{"crew " + spec.name: seen(held...)}
	if _, ok := sets["prompt"]; ok {
		was, _ := spec.have.Config["prompt"].(string)
		path, _ := spec.set["prompt"].(string)
		does = append(does, w.promptLines(was, path, spec.prompt)...)
		sum := sha256.Sum256([]byte(spec.prompt))
		seenMap["prompt "+path] = hex.EncodeToString(sum[:])
	}
	if _, ok := sets["kind"]; ok {
		does = append(does, spec.kindLines(sets)...)
	}
	if _, ok := sets["model"]; ok {
		now := "the agent's own default model"
		if was, had := spec.have.Config["model"]; had && grantSettingText(was, had) != "" {
			now = "the model " + grantSettingText(was, had)
		}
		does = append(does,
			fmt.Sprintf("It starts them with %s now.", now),
			fmt.Sprintf("It will start them with the model %s.", spec.set["model"]))
	}
	var flags []string
	if _, ok := sets["dir"]; ok {
		does = append(does,
			fmt.Sprintf("It works in %s now.", or(spec.have.Dir, "the yard's home")),
			fmt.Sprintf("It will work in %s.", spec.want.Dir))
		flags = []string{"--dir", shellQuote(spec.have.Dir)}
	}
	if _, ok := sets["preset"]; ok {
		does = append(does, presetLines(spec.was, spec.kind, spec.have.Config["args"], spec.preset)...)
	}
	return AskRun{
		Argv:   run.Argv,
		Does:   does,
		Back:   strongBack("crew set", spec.name, spec.have.Config, spec.set, flags...),
		BackBy: "only the operator",
		Seen:   seenMap,
		Role:   spec.prompt,
	}, nil
}

// kindLines are the card's lines for a kind that changes: the kind its agents
// start as now (the yard's default_kind, where the member names none) and as
// they will. The flags it holds are not the kind's to translate: where the
// card sets no preset the card says they stay as they are.
func (spec crewSetSpec) kindLines(sets map[string]string) []string {
	now := fmt.Sprintf("It starts %s (%s) agents now", spec.was, or(HarnessLabels[spec.was], spec.was))
	if _, named := spec.have.Config["kind"]; !named {
		now += ", the yard's default_kind"
	}
	lines := []string{now + ".", fmt.Sprintf("It will start %s (%s) agents.", spec.kind, or(HarnessLabels[spec.kind], spec.kind))}
	if _, ok := sets["preset"]; !ok {
		if words := argWords(spec.have.Config["args"]); len(words) > 0 {
			lines = append(lines, fmt.Sprintf("It starts them with %s, and this card does not change those flags.", quotedWords(words)))
		}
	}
	return lines
}

// crewSetStale says why the ask seq for crew set is approved no more: the
// member's definition was set since, or the yard would not write its card as
// it stands now.
func (w *wtx) crewSetStale(seq uint64, was, now AskRun) error {
	name := was.Argv[2]
	if key := "crew " + name; was.Seen[key] != now.Seen[key] {
		e := lastEvent(w.Tx, "crew_defined", naming("crew", name))
		return fmt.Errorf("ask %d was written when the crew member %s was another: its definition was set since%s. %s", seq, name, since(e), nothingRan)
	}
	return staleCard(seq, was, now)
}

// crewSetWeight weighs a crew set card: the member's sessions run a program
// the card shows, its prompt, the agent it starts and where, or the flags it
// starts it with.
func crewSetWeight(_ *wtx, run AskRun) ([]AskWeight, error) {
	name, sets := strongSetOf(run.Argv)
	return []AskWeight{weighs(WeightCode, fmt.Sprintf("%s of the crew member %s, which each session the yard starts for it then runs as the card shows", andList(strongChanged(sets)), name))}, nil
}

// runCrewSet sets the member as actor, through the store function the
// terminal's crew set calls (putCrew), so the checks that function makes are
// the same: the approver is the operator, who may define a crew member. The
// proposal is held once more in this write, so what is saved is what the
// card showed. It answers the lines the command prints for what changed.
func runCrewSet(ctx context.Context, w *wtx, run AskRun, actor string, by *approvedOn) (string, error) {
	spec, err := w.crewSetProposal(run)
	if err != nil {
		return "", err
	}
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
