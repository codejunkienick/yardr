package store

import (
	"context"
	"fmt"
)

// An ask's turn is how many answers it has taken: every pick, words answer,
// put-aside, decline and take-back written on it, one an expired or
// written-over put-aside among them. An ask note is never rewritten once
// written, so its seq pins the proposal (question, options, recipient, and a
// stage ask's stage and stay); what changes after is only its answers, each
// a new note, and so its turn. The turn only grows, so a read of turn n is
// stale once anyone else has written on the ask, even where the state reads
// the same again (put aside, then back).
//
// A guarded answer (AnswerAskExpected, DeferAskExpected,
// DeclineAskExpected) names the turn its caller read, and is refused with
// ErrConflict, writing nothing, when the ask is at another: a client that
// read an ask open cannot replace a pick someone else made in the gap, nor
// put aside or decline what was settled meanwhile. The turn is checked in
// the answer's own write, ahead of the rules of who may answer and what the
// ask takes, which stand as the unguarded answer's: who may answer (answerable, stageAnswer), the weighty
// word (weighedRefusal), and what the answer moves. The unguarded calls are
// unchanged: a newer pick still replaces an older one there.

// Turn is the ask's turn: how many answers it has taken.
func (r ResolvedAsk) Turn() int { return len(r.Answers) }

// answerExpect is what a guarded answer expects, checked in the answer's
// write: the bead's revision (AddAnswerExpected), or the ask's turn when
// turned. The zero value expects nothing, as the unguarded calls do.
type answerExpect struct {
	revision string
	turn     int
	turned   bool
}

// guards reports whether e expects anything.
func (e answerExpect) guards() bool { return e.revision != "" || e.turned }

// expectTurn is the expectation of an answer drawn from the ask at turn.
func expectTurn(turn int) (answerExpect, error) {
	if turn < 0 {
		return answerExpect{}, fmt.Errorf("turn %d: an ask's turn counts its answers, from 0", turn)
	}
	return answerExpect{turn: turn, turned: true}, nil
}

// check refuses the answer to the ask seq on the bead row, whose notes are
// notes, when e no longer holds. An ask that is none is answerable's to
// refuse.
func (e answerExpect) check(row *beadRow, notes []Note, seq uint64) error {
	if e.revision != "" && row.Revision != e.revision {
		return fmt.Errorf("%w: bead %s is at revision %s, not %s; read it again", ErrConflict, row.ID, row.Revision, e.revision)
	}
	if !e.turned {
		return nil
	}
	for _, r := range ResolveAsks(notes, now()) {
		if r.Note.Seq == seq && r.AskErr == nil && r.Turn() != e.turn {
			return fmt.Errorf("%w: ask %d on bead %s is at turn %d, not %d: it was answered, put aside, declined or taken back since; read it again",
				ErrConflict, seq, row.ID, r.Turn(), e.turn)
		}
	}
	return nil
}

// AnswerAskExpected is AnswerAsk drawn from the ask at turn, the Turn its
// caller read: refused with ErrConflict, and nothing written, when the ask
// has taken another answer since.
func (s *Store) AnswerAskExpected(ctx context.Context, id string, ask uint64, turn, option int, words, actor string, weighed bool) error {
	expect, err := expectTurn(turn)
	if err != nil {
		return err
	}
	return s.addAnswer(ctx, id, Answer{Ask: ask, Option: option, Words: words}, actor, expect, weighed)
}

// DeferAskExpected is DeferAsk drawn from the ask at turn, refused as
// AnswerAskExpected is.
func (s *Store) DeferAskExpected(ctx context.Context, id string, ask uint64, turn int, actor string) error {
	expect, err := expectTurn(turn)
	if err != nil {
		return err
	}
	return s.addAnswer(ctx, id, Answer{Ask: ask, Later: true}, actor, expect, false)
}

// DeclineAskExpected is DeclineAsk drawn from the ask at turn, refused as
// AnswerAskExpected is.
func (s *Store) DeclineAskExpected(ctx context.Context, id string, ask uint64, turn int, words, actor string) error {
	expect, err := expectTurn(turn)
	if err != nil {
		return err
	}
	return s.addAnswer(ctx, id, Answer{Ask: ask, Words: words, Decline: true}, actor, expect, false)
}
