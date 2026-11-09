package store

import (
	"context"
	"fmt"
	"strings"
)

// MovedBead is what MoveBead answers: the bead, the stage it entered, and
// whether entering it closed the bead.
type MovedBead struct {
	Bead  string `json:"bead"`
	Stage string `json:"stage"`
	// Closed is true where the stage the move entered is a terminal one.
	Closed bool `json:"closed,omitempty"`
}

// MoveBead makes one move Waiting offered the actor, with its note, from
// the revision the row was read at, and nothing else: it is the write a
// client that is not yardr moves a parked bead with. Exactly one of
// outcome (a done move) and to (an advance move) names the move, as the
// web's move form gives them.
//
// The yard checks, before any write, that the bead still stands at the
// revision, that the move is one Waiting serves the actor for this bead,
// and that a note stands where the move needs one; each refusal writes
// nothing, the note included. The move itself is Done or AdvanceNote with
// Expect{Stage: the bead's stage as read under the revision, Revision:
// revision}, in one write with the note, and inside that write moveBead's
// ActMoveOut check judges the same actor against the stage's group, so a
// non-member is refused there too.
//
// Following Waiting exactly is the rule for what is offered: a bead with
// no row in the actor's queue offers no move here. A held bead, and one
// stranded in a stage its flow lacks, never stand in Waiting, so neither
// can be moved with this call, though the store refuses no move for a
// held bead by Done or AdvanceNote: release the hold, or mend the flow
// (yardr review offers a stranded bead its move to the first stage), read
// Waiting again, and then move. A bead at its flow's first stage, one
// that carries an open ask to the actor, and one the actor has answered
// with another member left are likewise not offered: the first is shaped,
// not moved, the second is answered, and the third is the other member's
// to move.
func (s *Store) MoveBead(ctx context.Context, bead, outcome, to, note, revision, actor string) (*MovedBead, error) {
	switch {
	case outcome != "" && to != "":
		return nil, fmt.Errorf("bead %s: a move names an outcome or a stage, not both (outcome %q, to %q)", bead, outcome, to)
	case outcome == "" && to == "":
		return nil, fmt.Errorf("bead %s: a move names an outcome or a stage: give outcome for a done move, or to for an advance", bead)
	}
	b, err := s.GetBead(ctx, bead)
	if err != nil {
		return nil, err
	}
	// A bead the actor may not see is not found, as Bead answers it: the
	// refusals below name its revision and what it waits with, which a
	// device that guessed its id must not learn.
	if err := s.mayRead(ctx, actor, ActSee, Object{Kind: KindBead, Name: b.ID}); err != nil {
		return nil, fmt.Errorf("bead %s: %w", bead, ErrNotFound)
	}
	if revision == "" || revision != b.Revision {
		return nil, fmt.Errorf("%w: bead %s is at revision %s, not %s; read it again", ErrConflict, b.ID, b.Revision, or(revision, "none"))
	}
	// The offered moves are the actor's own Waiting row for the bead, read
	// as the actor: no row, no move. The queue's gate is the actor reading
	// their own, which the bound actor always passes as themself.
	rows, err := s.Waiting(ctx, actor, actor, actor)
	if err != nil {
		return nil, err
	}
	var row *WaitingBead
	for i := range rows {
		if rows[i].Bead == b.ID {
			row = &rows[i]
			break
		}
	}
	if row == nil {
		return nil, fmt.Errorf("bead %s does not wait for %s; read it again", b.ID, actor)
	}
	if row.Revision != revision {
		return nil, fmt.Errorf("%w: bead %s is at revision %s, not %s; read it again", ErrConflict, b.ID, row.Revision, revision)
	}
	if row.FlowError != "" {
		return nil, fmt.Errorf("bead %s: no move is allowed until the flow is fixed: %s", b.ID, row.FlowError)
	}
	var want *Move
	for i := range row.Actions {
		m := &row.Actions[i]
		if outcome != "" && m.Kind == MoveDone && m.Outcome == outcome {
			want = m
		}
		if outcome == "" && m.Kind == MoveAdvance && m.To == to {
			want = m
		}
	}
	if want == nil {
		labels := make([]string, len(row.Actions))
		for i, m := range row.Actions {
			labels[i] = m.Label
		}
		return nil, fmt.Errorf("bead %s does not wait for %s with that move; it waits with %s; read it again", b.ID, actor, strings.Join(labels, ", "))
	}
	if want.NoteRequired && strings.TrimSpace(note) == "" {
		return nil, fmt.Errorf("sending back to %s needs a note saying what should change", want.To)
	}
	exp := Expect{Stage: row.Stage, Revision: revision}
	var moved *Bead
	if want.Kind == MoveDone {
		moved, err = s.Done(ctx, b.ID, want.Outcome, note, exp, actor)
	} else {
		moved, err = s.AdvanceNote(ctx, b.ID, want.To, note, exp, actor)
	}
	if err != nil {
		return nil, err
	}
	return &MovedBead{Bead: moved.ID, Stage: moved.Stage, Closed: moved.Status == "closed"}, nil
}
