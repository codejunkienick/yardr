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
// put aside or decline what was settled meanwhile. The turn says that
// someone wrote on the ask, which only a caller who may answer it is told, so
// it is checked in the answer's own write after the rules of who may answer
// (guard), and ahead of the rest, which stand as the unguarded answer's:
// what the ask takes (answerable), the weighty word (weighedRefusal), and
// what the answer moves (stageAnswer). The unguarded calls are unchanged: a
// newer pick still replaces an older one there.
//
// A guarded pick or decline on a mapping card names the held count its
// caller read too: syncHeldCount rewrites the card's count in place, with
// no new note, so the turn alone cannot say a delivery was held since,
// and a pick would deliver, or a decline drop, what the rendering the
// person answered did not count. A stale put-aside is harmless, the card
// coming back current, so only the pick and the decline carry the count.

// Turn is the ask's turn: how many answers it has taken.
func (r ResolvedAsk) Turn() int { return len(r.Answers) }

// answerExpect is what a guarded answer expects, checked in the answer's
// write: the bead's revision (AddAnswerExpected), or the ask's turn when
// turned, with the held count the caller read when heldSet, on a mapping
// card. The zero value expects nothing, as the unguarded calls do.
type answerExpect struct {
	revision string
	turn     int
	turned   bool
	held     int
	heldSet  bool
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

// expectTurnHeld is the expectation of a pick or a decline drawn from the
// mapping card read at turn, holding held deliveries.
func expectTurnHeld(turn, held int) (answerExpect, error) {
	if turn < 0 {
		return answerExpect{}, fmt.Errorf("turn %d: an ask's turn counts its answers, from 0", turn)
	}
	if held < 0 {
		return answerExpect{}, fmt.Errorf("held %d: a card's held count counts its waiting deliveries, from 0", held)
	}
	return answerExpect{turn: turn, turned: true, held: held, heldSet: true}, nil
}

// check refuses the answer to the ask r on the bead row when e no longer
// holds. The held count is a mapping card's alone: any other ask takes
// the pick or the decline whatever held it names.
func (e answerExpect) check(row *beadRow, r *ResolvedAsk) error {
	if e.revision != "" && row.Revision != e.revision {
		return fmt.Errorf("%w: bead %s is at revision %s, not %s; read it again", ErrConflict, row.ID, row.Revision, e.revision)
	}
	if e.turned && r.Turn() != e.turn {
		return fmt.Errorf("%w: ask %d on bead %s is at turn %d, not %d: it was answered, put aside, declined or taken back since; read it again",
			ErrConflict, r.Note.Seq, row.ID, r.Turn(), e.turn)
	}
	if id := r.Ask.Identity; e.heldSet && id != nil && id.Held != e.held {
		return fmt.Errorf("%w: ask %d on bead %s holds %d deliveries, not %d: one was held or released since; read it again",
			ErrConflict, r.Note.Seq, row.ID, id.Held, e.held)
	}
	return nil
}

// guard refuses the answer ans, by actor, to the ask it names on the bead
// row, whose notes are notes, when e no longer holds (check). A conflict
// names the revision the bead is at or the turn the ask has reached, which
// tells that someone wrote since, and only a caller who may answer the ask is
// to be told that. So the policy speaks first, in the same write: a bead
// actor may not see is not found, as MoveBead has it, and the gate the answer
// meets further on is asked now, so that a caller it turns away is refused as
// the unguarded answer refuses it, whatever it read. Then e is checked, ahead
// of what answerable says of an ask answered, declined or taken back since, so
// that a stale caller who may answer is told its read is stale, and not what
// the ask would be refused for.
//
// The gate is the ask kind's own, as the answer meets it: the group's members
// for a stage ask (stageAnswerAuth, which stageAnswer asks), the operator the
// card asks for a mapping card (mayAnswerMapping, which answerMapping asks),
// and for any other ask its addressee or an operator (mayAnswer, which
// answerable asks). That last holds a pick or words on a card that takes an
// approval too, which answerable refuses whoever gives them. An ask that is
// none takes no guard: answerable refuses it.
func (w *wtx) guard(ctx context.Context, e answerExpect, row *beadRow, notes []Note, ans Answer, actor string, mayAnswer func(to string) error) error {
	if !e.guards() {
		return nil
	}
	if err := w.may(ctx, actor, ActSee, Object{Kind: KindBead, Name: row.ID}); err != nil {
		return fmt.Errorf("bead %s: %w", row.ID, ErrNotFound)
	}
	var r *ResolvedAsk
	resolved := ResolveAsks(notes, now())
	for i := range resolved {
		if resolved[i].Note.Seq == ans.Ask && resolved[i].AskErr == nil {
			r = &resolved[i]
		}
	}
	if r == nil {
		return nil
	}
	var err error
	switch ask := r.Ask; {
	case ask.Stage != "":
		err = w.stageAnswerAuth(ctx, row, ask, actor)
	case ask.Identity != nil:
		err = w.mayAnswerMapping(ctx, row, ask, ans, actor)
	default:
		err = mayAnswer(ask.To)
	}
	if err != nil {
		return err
	}
	return e.check(row, r)
}

// AnswerAskExpected is AnswerAsk drawn from the ask at turn, the Turn its
// caller read: refused with ErrConflict, and nothing written, when the ask
// has taken another answer since. On a mapping card held is the Held its
// caller read, and a card that holds another count is refused so too,
// before anything it holds is delivered; on any other ask held is ignored.
func (s *Store) AnswerAskExpected(ctx context.Context, id string, ask uint64, turn, option int, words, actor string, weighed bool, held int) error {
	expect, err := expectTurnHeld(turn, held)
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
// AnswerAskExpected is, with its held count on a mapping card: a card that
// holds another count drops nothing.
func (s *Store) DeclineAskExpected(ctx context.Context, id string, ask uint64, turn int, words, actor string, held int) error {
	expect, err := expectTurnHeld(turn, held)
	if err != nil {
		return err
	}
	return s.addAnswer(ctx, id, Answer{Ask: ask, Words: words, Decline: true}, actor, expect, false)
}
