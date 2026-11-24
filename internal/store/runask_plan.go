// The plan of a run ask: what its command would change, field by field, as
// the yard's records hold each field now and as the approval would leave it.
// A row of the generic set-a-field engine (runask_setfield.go) makes its card
// from its plan, and the plan rides on the card (AskRun.Plan) as Does does.
//
// The plan is the yard's, never the writer's: askedRun keeps none of what a
// writer sent beside argv but the carried bytes, and the row computes the
// plan in the write that stores the ask. An approval plans again, in the
// write that would run the command; a plan that reads otherwise refuses the
// approval, and the yard asks again with the plan it holds now (askedAgain):
// one replacement for one refused approval, so nothing loops but a person
// approving again.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// RunPlan is what a run ask's command changes, as the yard read it.
type RunPlan struct {
	// Of names what the command changes, in the yard's words: "crew member
	// yardmaster".
	Of string `json:"of"`
	// Changes are the fields the command changes, each as it stands and
	// as the approval would leave it. A field it names and leaves as it is
	// is no change.
	Changes []PlanChange `json:"changes,omitempty"`
	// Holds are what the plan was made under and does not change, by name:
	// what the card's sentences read beside the changes (the kind a
	// member's agents start as, the preset a list of flags is), so that a
	// plan reads the same only where the card would too.
	Holds map[string]string `json:"holds,omitempty"`
}

// PlanChange is one field a plan changes. Before and After are the values
// as the terminal reads them back (planValue), "" for none. A field that
// begins with "--" is no config key but the terminal's flag for it (--dir).
type PlanChange struct {
	Field  string `json:"field"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

// change is the plan's change of field, and whether it changes it.
func (p RunPlan) change(field string) (PlanChange, bool) {
	for _, c := range p.Changes {
		if c.Field == field {
			return c, true
		}
	}
	return PlanChange{}, false
}

// Lines are the plan as a terminal prints it: one line for each change,
// what stands and what the approval leaves.
func (p RunPlan) Lines() []string {
	lines := make([]string, len(p.Changes))
	for i, c := range p.Changes {
		lines[i] = fmt.Sprintf("  %s: %s -> %s", c.Field, or(c.Before, "(not set)"), or(c.After, "(dropped)"))
	}
	return lines
}

// planValue is a config value as a plan holds it, which is how the terminal
// reads it back from key=value: a string as it is, unless it reads as JSON,
// which the terminal would store as a value of its own type, so it is quoted;
// anything else as JSON. Not set is "".
func planValue(v any, set bool) string {
	if !set || v == nil {
		return ""
	}
	if s, ok := v.(string); ok && !json.Valid([]byte(s)) {
		return s
	}
	text, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(text)
}

// planned adds to changes the field, where after is not what stands.
func planned(changes []PlanChange, field, before, after string) []PlanChange {
	if before == after {
		return changes
	}
	return append(changes, PlanChange{Field: field, Before: before, After: after})
}

// samePlan says whether two plans read the same, as they are stored: an
// empty map and none are one plan.
func samePlan(a, b *RunPlan) bool {
	if a == nil || b == nil {
		return a == b
	}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// planMoved says in a refusal what reads otherwise in the plan now than in
// the plan was: the first field whose value stands otherwise.
func planMoved(was, now *RunPlan) string {
	if was == nil {
		return "the card is from a yard that kept no plan of it"
	}
	before := func(p *RunPlan, field string) (string, bool) {
		if c, ok := p.change(field); ok {
			return c.Before, true
		}
		v, ok := p.Holds[field]
		return v, ok
	}
	var fields []string
	for _, p := range []*RunPlan{was, now} {
		for _, c := range p.Changes {
			fields = append(fields, c.Field)
		}
	}
	for _, p := range []*RunPlan{was, now} {
		fields = append(fields, sortedKeys(p.Holds)...)
	}
	for _, f := range fields {
		a, _ := before(was, f)
		b, _ := before(now, f)
		if a != b {
			held := func(v string) string {
				if v == "" {
					return "no " + f
				}
				return f + " " + v
			}
			return fmt.Sprintf("it held %s, and holds %s now", held(a), held(b))
		}
	}
	for _, f := range fields {
		a, _ := was.change(f)
		b, _ := now.change(f)
		if a.After != b.After {
			return fmt.Sprintf("the card set its %s to %s, and would set %s now", f, or(a.After, "nothing"), or(b.After, "nothing"))
		}
	}
	return "the card would read otherwise now"
}

// sortedKeys are the keys of m in order.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// planBack is the command that takes the plan back, in the terminal's words:
// command and name, then each config field put back to what stood (in the
// order order gives), then each flag field, then --unset for each config
// field that stood unset.
func planBack(command, name string, p RunPlan, order []string) string {
	// key=value as one word: bare where the value is, the whole word quoted
	// otherwise, as the terminal reads it.
	word := func(k, v string) string {
		if shellQuote(v) == v {
			return k + "=" + v
		}
		return shellQuote(k + "=" + v)
	}
	changes := slices.Clone(p.Changes)
	slices.SortStableFunc(changes, func(a, b PlanChange) int {
		return slices.Index(order, a.Field) - slices.Index(order, b.Field)
	})
	back := []string{"yardr " + command, name}
	var flags, unset []string
	for _, c := range changes {
		switch {
		case strings.HasPrefix(c.Field, "--"):
			flags = append(flags, c.Field, shellQuote(c.Before))
		case c.Before == "":
			unset = append(unset, "--unset", c.Field)
		default:
			back = append(back, word(c.Field, c.Before))
		}
	}
	return strings.Join(append(append(back, flags...), unset...), " ")
}

// replanError refuses an approval whose plan reads otherwise now: why says
// what moved, and the refusal ends in how the yard asked again (askedAgain).
type replanError struct {
	seq uint64
	why string
}

func (e *replanError) Error() string { return e.why + ". " + nothingRan }

// askedAgain is what ApproveAsk answers for the refusal err of the approval
// of the ask seq on the bead id by actor: err itself, unless it refused a
// plan that reads otherwise now. Then the refusing write took nothing back,
// and one write more asks again: the same command, with what its writer
// carried beside it, from the same asker to the same person, with the card
// the row draws now, and the old ask taken back naming the new one. The
// replacement is filed as AddAsk would file it for the asker, so every check
// a new ask meets it meets: a command the asker may now run themselves, or
// may no longer propose, is not asked again, and the refusal says why.
func (s *Store) askedAgain(ctx context.Context, id string, seq uint64, actor string, err error) error {
	var stale *replanError
	if !errors.As(err, &stale) {
		return err
	}
	again, asker, err := s.openRunAsk(ctx, id, seq)
	if err != nil {
		return fmt.Errorf("%s, and the yard could not ask again: %w. %s", stale.why, err, nothingRan)
	}
	// The replacement is the asker's ask and not the approver's call: it
	// names no caller and no session of the approver's, so the asker's
	// gates are the asker's.
	quiet := WithSession(WithCaller(ctx, 0), "")
	add, err := s.newAsk(quiet, again, asker)
	if err != nil {
		return fmt.Errorf("%s, and the yard could not ask again: %w. %s", stale.why, err, nothingRan)
	}
	defer s.askLocks.lock(askLockKey(id, seq))()
	var fresh uint64
	err = s.writeVia(ctx, func(w *wtx) error {
		who, err := paneAuthor(w.Tx, callerOf(ctx), actor)
		if err != nil {
			return err
		}
		row, err := w.loadBead(id)
		if err != nil {
			return err
		}
		notes, err := beadNotes(w.Tx, row.UUID)
		if err != nil {
			return err
		}
		// Another call may have ended the ask between the two writes.
		resolved := ResolveAsks(notes, now())
		i := slices.IndexFunc(resolved, func(r ResolvedAsk) bool { return r.Note.Seq == seq })
		if i < 0 || resolved[i].State != AskOpen && resolved[i].State != AskAside {
			return fmt.Errorf("ask %d is open no more", seq)
		}
		r := resolved[i]
		if err := w.may(ctx, who, ActTakeBackAsk, Object{Kind: KindAsk, Name: row.ID, Seq: seq, Whose: r.Note.Author}); err != nil {
			return err
		}
		if fresh, err = add(w, id); err != nil {
			return err
		}
		if row, err = w.loadBead(row.UUID); err != nil {
			return err
		}
		ans := Answer{Ask: seq, Words: fmt.Sprintf("asked again as ask %d: the yard no longer holds what this card was written from", fresh), TakeBack: true}
		text, err := json.Marshal(ans)
		if err != nil {
			return err
		}
		if _, err := w.addNoted(row, NoteAnswer, string(text), who); err != nil {
			return err
		}
		return w.settleCopies(row, ans, nil, "")
	})
	if err != nil {
		return fmt.Errorf("%s, and the yard could not ask again: %w. %s", stale.why, err, nothingRan)
	}
	return fmt.Errorf("%s. Nothing ran: the yard took ask %d back and asked again as ask %d, with the card as the yard reads it now", stale.why, seq, fresh)
}

// openRunAsk is the run ask seq on the bead id as its writer would send it
// again, with its asker: the question, the person and the context as asked,
// and of the command only what a writer sends (argv and what it carries
// beside it), since the rest is the yard's to write.
func (s *Store) openRunAsk(ctx context.Context, id string, seq uint64) (Ask, string, error) {
	notes, err := s.Notes(ctx, id)
	if err != nil {
		return Ask{}, "", err
	}
	for _, n := range notes {
		if n.Seq != seq || n.Kind != NoteAsk {
			continue
		}
		ask, err := ParseAsk(n)
		if err != nil {
			return Ask{}, "", err
		}
		if ask.Run == nil {
			break
		}
		was := *ask.Run
		again := Ask{Question: ask.Question, To: ask.To, Context: ask.Context, Run: &AskRun{
			Argv: was.Argv, Carried: was.Carried, Test: was.Test, Role: was.Role, HookTable: was.HookTable, Flow: was.Flow,
		}}
		return again, n.Author, nil
	}
	return Ask{}, "", fmt.Errorf("bead %s has no run ask %d", id, seq)
}
