// The --yes half of the pack apply row (design yardr-6maq, 3.3): where
// the plan's Edited list names installed files that were changed by
// hand, the card shows each one whole and approval overwrites exactly
// those copies, held to the hashes the card showed.
package store

import (
	"fmt"
	"strings"
)

// packYesEdited decides the plan's Edited list for a card asked with yes:
// the edited file objects the card shows whole, or the refusal. Without
// --yes any edited file refuses, as Missing, Asks, Refused and NonText do
// with or without it; a --yes that overwrites nothing is refused in words
// that say to propose the apply without it. A dropped file edited by hand
// refuses either way: the card cannot show the pack's bytes whole where
// there are none, and without a prune approval would leave the edit
// standing rather than overwrite it.
func packYesEdited(name, depot string, plan *AskPlan, yes bool) ([]AskPlanObject, error) {
	for _, o := range plan.Objects {
		if o.Kind == "file" && o.Edited && o.Action != "create" && o.Action != "change" {
			return nil, packEditedTerminal(name, depot, plan.Edited)
		}
	}
	var shown []AskPlanObject
	for _, o := range plan.Objects {
		if o.Kind == "file" && o.Edited {
			shown = append(shown, o)
		}
	}
	switch {
	case len(shown) == 0 && len(plan.Edited) == 0:
		if !yes {
			return nil, nil
		}
		return nil, packYesNothing(name)
	case len(shown) == 0 || !yes:
		return nil, packEditedTerminal(name, depot, plan.Edited)
	default:
		return shown, nil
	}
}

// packYesNothing refuses --yes on a pack whose plan names no edited
// file: a card's approval is the confirmation, so the flag answers
// nothing there.
func packYesNothing(name string) error {
	return fmt.Errorf("pack %s: --yes overwrites a copy edited by hand, and its plan names none: propose the apply without it", name)
}

// packEditedTerminal refuses a plan with edited files the card cannot
// show: the pack is the operator's to apply in a terminal, where pack
// check shows its plan.
func packEditedTerminal(name, depot string, edited []string) error {
	lines := edited
	if len(lines) == 0 {
		lines = []string{"a file edited by hand"}
	}
	return fmt.Errorf("pack %s: its plan asks or refuses, so no card can apply it: %s: it is the operator's to type in a terminal (yardr pack check %s --depot %s shows its plan there)", name, strings.Join(lines, "; "), name, depot)
}

// packYesDoes appends the card's own sentences for the edited files it
// shows whole: approval overwrites each edited copy and keeps no other
// copy of it. Each file's line above already shows its path, both hashes
// and its diff, counted in the payload bound as any changed file's.
func packYesDoes(does []string, shown []AskPlanObject) []string {
	for _, o := range shown {
		does = append(does, fmt.Sprintf("file %s: approval overwrites the copy edited by hand with the pack's bytes and keeps no other copy of the edit.", o.Name))
	}
	return does
}

// packYesWeight weighs the edited files the card shows whole as changed
// files: code where one is a program, a role or a script the yard runs,
// so overwriting a hand-edited program is strong and takes the strong
// doors. A plan that shows none weighs nothing here.
func packYesWeight(run AskRun) []AskWeight {
	if run.Pack == nil || run.Pack.Plan == nil {
		return nil
	}
	var edited []string
	for _, o := range run.Pack.Plan.Objects {
		if o.Kind == "file" && o.Edited && o.Runs && (o.Action == "create" || o.Action == "change") {
			edited = append(edited, o.Name)
		}
	}
	if len(edited) == 0 {
		return nil
	}
	return []AskWeight{weighs(WeightCode, fmt.Sprintf("the hand-edited file %s of pack %s, overwritten with the pack's bytes", andList(edited), run.Pack.Name))}
}
