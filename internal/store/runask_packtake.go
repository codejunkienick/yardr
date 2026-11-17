// The --take half of the pack apply row. Where a plan keeps a flow, a stage
// file or a role the yard changed (the pack is a starting point), a card
// asked with --take <kind>/<name> sets the pack's version of exactly those
// objects: it shows each whole, as it shows a changed file, and approval
// overwrites what the yard has, held to the hash of what the card showed.
// Like --yes, a take is a whole card's: it is never summarised, and it keeps
// no other copy of what it overwrites. The terminal's own --take is the
// pack's (internal/pack, keep.go); this is the card's door to it.
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// packTakeKey is the context key of the names a pack card takes.
type packTakeKey struct{}

// WithPackTake gives a pack reading the names the card takes, as the
// terminal's --take names them: the plan is built the same for any take, but
// a reader refuses a name its plan cannot carry out (Plan.CheckTake) and
// carries each kept object it takes whole, which a card that takes must
// show. It rides the context, as a URL pack's fetching does, so that the
// readers of the embedded packs, of directories and of the repository stay
// as they are for a card that takes nothing.
func WithPackTake(ctx context.Context, take []string) context.Context {
	if len(take) == 0 {
		return ctx
	}
	return context.WithValue(ctx, packTakeKey{}, take)
}

// PackTakeOf are the names a reading is asked to take (WithPackTake), nil
// for none.
func PackTakeOf(ctx context.Context) []string {
	take, _ := ctx.Value(packTakeKey{}).([]string)
	return take
}

// packTakeNames reads the --take words of pack apply into the card's one
// form: each a <kind>/<name> as the plan prints an object (the same rule
// Plan.CheckTake starts from), none twice, sorted, so that the same take
// typed in another order is the same card. Whether the plan keeps what a name
// names is the reading's to say.
func packTakeNames(words []string) ([]string, error) {
	var out []string
	for _, name := range words {
		if kind, rest, ok := strings.Cut(name, "/"); !ok || kind == "" || rest == "" {
			return nil, fmt.Errorf("--run takes %s: --take %s is not <kind>/<name>, as the plan prints an object (flow/starter.default, file/flows/default/new.md)", PackApplyUse, name)
		}
		if slices.Contains(out, name) {
			return nil, fmt.Errorf("--run takes %s: --take names %s twice", PackApplyUse, name)
		}
		out = append(out, name)
	}
	slices.Sort(out)
	return out, nil
}

// packTaken says whether a card sets the pack's version of o: it is a kept
// object and take names it.
func packTaken(take []string, o AskPlanObject) bool {
	return o.Action == "kept" && slices.Contains(take, o.Kind+"/"+o.Name)
}

// packTakeWhole says whether the reading carried a kept object whole, as a
// card must show what it overwrites: a file with the hashes of both its
// copies and its text, a flow with the digest of the flow it would replace.
// Another kind has none to take (a declared source stays as the pack says).
func packTakeWhole(o AskPlanObject) bool {
	switch o.Kind {
	case "file":
		return o.FileNewSHA != "" && o.FileOldSHA != ""
	case "flow":
		return o.FlowOldSHA != ""
	}
	return false
}

// packSourceWords refuses the flags a pack of kind source takes none of:
// it keeps nothing, so --yes has no edited file to overwrite and --take no
// kept object to set.
func packSourceWords(name string, yes bool, take []string) error {
	if yes {
		return packYesNothing(name)
	}
	if len(take) > 0 {
		return fmt.Errorf("pack %s: --take sets the pack's version of a flow, stage file or role the yard changed, and a pack of kind source keeps none: propose the apply without it", name)
	}
	return nil
}

// packCardWhole decides what a depot card shows whole beside the objects
// it creates and changes: the edited files --yes overwrites
// (packYesEdited) and the kept objects --take sets, or the refusal. A take
// is held to what the reading carried, whatever reader made it, so a card
// never says it takes an object it does not show, and a flow it takes is
// held to the open beads that follow it (packTakeStrays).
func (w *wtx) packCardWhole(name string, reading *PackReading, yes bool, take []string) ([]AskPlanObject, error) {
	shown, err := packYesEdited(name, reading.Depot, reading.Plan, yes)
	if err != nil {
		return nil, err
	}
	for _, t := range take {
		i := slices.IndexFunc(reading.Plan.Objects, func(o AskPlanObject) bool { return o.Kind+"/"+o.Name == t })
		switch {
		case i < 0 || reading.Plan.Objects[i].Action != "kept":
			return nil, fmt.Errorf("pack %s: --take %s: the plan does not keep it, so there is nothing to take (yardr pack check %s --depot %s shows what it keeps)", name, t, name, reading.Depot)
		case !packTakeWhole(reading.Plan.Objects[i]):
			return nil, fmt.Errorf("pack %s: --take %s: the reading carries nothing of it to show whole, so this is the operator's to type in a terminal (yardr pack apply %s --depot %s --take %s)", name, t, name, reading.Depot, t)
		}
	}
	return shown, w.packTakeStrays(name, reading, take)
}

// packTakeStrays refuses a take of a flow that would strand an open bead,
// naming the beads, before anything is approved: the plan holds a flow it
// keeps to the flow the yard has, so only the pack's own flow, which a take
// sets, can lack a stage a bead stands in. The reading's target for the
// flow is the pack's, whole, and flowCardStrays reads it as the flow set
// card does. The store refuses the same write when the import makes it
// (PutFlow), mid-import, and still does; this refusal is the card's
// own, before anything runs and again at approval.
func (w *wtx) packTakeStrays(name string, reading *PackReading, take []string) error {
	for _, t := range take {
		kind, flow, _ := strings.Cut(t, "/")
		raw, ok := reading.Targets[kind+"\x00"+flow]
		if kind != "flow" || !ok {
			continue
		}
		var want Flow
		if err := json.Unmarshal([]byte(raw), &want); err != nil {
			return fmt.Errorf("pack %s: --take %s: the reading's flow is unreadable: %w", name, t, err)
		}
		strays, err := w.flowCardStrays(want)
		if err != nil {
			return err
		}
		if len(strays) > 0 {
			return fmt.Errorf("pack %s: --take %s: the pack's flow lacks the stage of open beads %s; advance them to a stage it has, or pin them to another flow (yardr bead update <id> --meta flow=<name>), then ask again",
				name, t, strings.Join(strays, "; "))
		}
	}
	return nil
}

// packTakeDoes appends the card's own sentence for each object it takes:
// approval sets the pack's version over what the yard changed and keeps no
// other copy of that change, as packYesDoes says of an edited file. The
// object's own line above shows what changes, whole, and packCardWhole has
// held each name to an object of the plan that is shown so.
func packTakeDoes(does []string, take []string) []string {
	for _, t := range take {
		kind, name, _ := strings.Cut(t, "/")
		if kind == "file" {
			does = append(does, fmt.Sprintf("file %s: approval overwrites the yard's changed copy with the pack's bytes and keeps no other copy of the change.", name))
			continue
		}
		does = append(does, fmt.Sprintf("%s %s: approval sets the pack's version over the yard's changed one and keeps no other copy of the change.", kind, name))
	}
	return does
}

// packTakeWeight weighs a take as code, whatever it takes: a flow decides
// what runs on every bead that follows it, a role briefs the agents the yard
// runs, and a stage file is the brief and the gate of its stage. A card that
// sets the pack's version of any of them over the yard's change takes the
// strong doors, with or without a depot named.
func packTakeWeight(run AskRun) []AskWeight {
	if run.Pack == nil || len(run.Pack.Take) == 0 {
		return nil
	}
	return []AskWeight{weighs(WeightCode, fmt.Sprintf("the pack %s's version of %s, set over the yard's change to it: a flow, a role and a stage file are what the yard runs and what its agents are briefed by", run.Pack.Name, andList(run.Pack.Take)))}
}
