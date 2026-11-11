package store

import (
	"slices"
	"strings"
)

// The kinds of Move.
const (
	// MoveDone reports an outcome the flow names, which the flow sends the
	// bead on by.
	MoveDone = "done"
	// MoveAdvance sends the bead to a stage the flow allows next.
	MoveAdvance = "advance"
)

// Move is one way a person may send a bead on from the stage it waits in, as
// yardr review offers it, the web's bead page and the Waiting call: an
// outcome the flow names (MoveDone), or a stage it allows next (MoveAdvance).
type Move struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	// Outcome is the outcome a done move reports; To the stage either enters.
	Outcome string `json:"outcome,omitempty"`
	To      string `json:"to"`
	// NoteRequired: sending a bead back needs a note saying what to change,
	// or whoever picks it up next has nothing to go on.
	NoteRequired bool `json:"note_required,omitempty"`
	// Terminal is a stage that closes the bead entering it.
	Terminal bool `json:"terminal,omitempty"`
}

// MovesFor is what a person may do with b, whose NextStages is next and whose
// yard has routes. Under a flow that names outcomes for b's stage: those
// outcomes, as they stand for the bead, since a round limit can send the
// report somewhere the flow's edge does not name. Under a flow without: the
// stages it allows for this bead. Returning to a question's asker needs no
// new note because the answer is already on the bead; other earlier stages
// need one. Without a flow: the stages with a route that could take the
// bead, matching its type, depot, labels, priority and originator, once
// each and sorted; nil when no route could. Advancing by hand stays free
// to name any stage. The labels are the yard's words for each move, the
// same on the terminal, the web and the socket, so the three can never
// offer one bead two different moves. Nil where there is none.
func MovesFor(b *Bead, next *NextStages, routes []Route, operator string) []Move {
	if next == nil || next.Flow == nil {
		bm := viewBead(*b)
		var stages []string
		for _, r := range routes {
			if r.Stage == b.Stage || slices.Contains(stages, r.Stage) {
				continue
			}
			if !MatchesExceptStage(viewRoute(r), bm, operator) {
				continue
			}
			stages = append(stages, r.Stage)
		}
		slices.Sort(stages)
		var out []Move
		for _, to := range stages {
			out = append(out, Move{Kind: MoveAdvance, To: to, Label: "Move to " + to})
		}
		return out
	}
	f := next.Flow
	here := slices.Index(f.Stages, b.Stage)
	// move describes going to s; its label starts lower-case to follow an outcome.
	move := func(to string) Move {
		m := Move{Kind: MoveAdvance, To: to, Terminal: f.IsTerminal(to)}
		switch {
		case to == next.Back:
			m.Label = "return to " + to
		case m.Terminal:
			m.Label = "finish as " + to + " (closes the bead)"
		case here >= 0 && slices.Index(f.Stages, to) < here:
			m.Label, m.NoteRequired = "send back to "+to+" (note required)", true
		default:
			m.Label = "move to " + to
		}
		return m
	}
	var out []Move
	if len(next.Outcomes) > 0 {
		for _, o := range next.Outcomes {
			m := move(o.Goes)
			m.Kind, m.Outcome, m.Label = MoveDone, o.Name, o.Name+": "+m.Label
			if note := o.RoundNote(""); note != "" {
				m.Label += " (" + note + ")"
			}
			out = append(out, m)
		}
	} else {
		for _, to := range next.Stages {
			m := move(to)
			m.Label = strings.ToUpper(m.Label[:1]) + m.Label[1:]
			out = append(out, m)
		}
	}
	// A question can make a stage an additional move for this bead, even when
	// the stage's flow lists outcomes instead of bare moves.
	if next.Back != "" && !slices.ContainsFunc(out, func(m Move) bool { return m.To == next.Back }) {
		m := move(next.Back)
		m.Label = "Return to " + next.Back
		out = append(out, m)
	}
	return out
}

// FoldMoves reports whether moves fold behind one "Move to ..." control:
// more than six of them, where every one is a plain advance, with no
// note required and no terminal. The predicate is uniformity, not flow
// presence: a flow bead whose moves are all plain stages folds too, and
// a flow outcome (kind done) never does.
func FoldMoves(moves []Move) bool {
	if len(moves) <= 6 {
		return false
	}
	for _, m := range moves {
		if m.Kind != MoveAdvance || m.NoteRequired || m.Terminal {
			return false
		}
	}
	return true
}

// ManualGroup is the manual group whose turn b waits for: the group working
// it, else the one its routes send it to (a manual group at its limit leaves
// beads waiting unclaimed). It is false when neither is a manual group.
// routes is the yard's routes in any order, groups its groups by name, and
// operator its operator setting, for the templated defaults (MatchRoute).
// Who of the group may move the bead on is its Members: none means anyone.
func ManualGroup(routes []Route, b Bead, operator string, groups map[string]Group) (Group, bool) {
	name := ""
	if b.Group != nil {
		name = *b.Group
	} else if r := MatchRoute(routes, b, operator, groups); r != nil {
		name = r.Worker()
	}
	g, ok := groups[name]
	return g, ok && g.Runner == "manual"
}
