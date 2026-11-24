// Taking stage asks back: on leaving a stage, on closing, and when a hand
// ask takes over a card of moves. Moved out of stageasks.go, which had no
// room left, unchanged.
package store

import (
	"encoding/json"
)

// takeBackStageAsks takes back the open and put-aside asks put for stage on
// the bead row, in the same write, authored yard with reason: the bead left
// the stage. Answered and declined asks stay as history. Where an ask also
// stands in another yard, addressed across, that yard is told in the same
// write.
func (w *wtx) takeBackStageAsks(row *beadRow, stage, reason string) error {
	return w.takeBackStage(row, reason, func(ask Ask) bool { return ask.Stage == stage })
}

// handAskStands reports whether an ask someone put by hand is open or put
// aside on the bead whose notes are given: no stage ask, which the yard
// puts and takes back with the stage.
func handAskStands(notes []Note) bool {
	for _, r := range ResolveAsks(notes, now()) {
		if r.AskErr == nil && r.Ask.Stage == "" && (r.State == AskOpen || r.State == AskAside) {
			return true
		}
	}
	return false
}

// withdrawMoves takes back the card of moves standing on the bead row, in
// the write that puts a hand ask on it, authored yard with reason. The card
// was put for a bead nobody had asked about (putStageAsks); this keeps the
// person's phone from holding two cards for one decision. It takes back no
// other stage ask, whose question the hand ask does not answer.
func (w *wtx) withdrawMoves(row *beadRow, reason string) error {
	return w.takeBackStage(row, reason, func(ask Ask) bool { return ask.Moves })
}

// takeBackAllStageAsks takes back every open and put-aside stage ask on the
// bead row, whatever stage it was put for: the bead closed.
func (w *wtx) takeBackAllStageAsks(row *beadRow, reason string) error {
	return w.takeBackStage(row, reason, func(ask Ask) bool { return ask.Stage != "" })
}

func (w *wtx) takeBackStage(row *beadRow, reason string, match func(Ask) bool) error {
	notes, err := beadNotes(w.Tx, row.UUID)
	if err != nil {
		return err
	}
	for _, r := range ResolveAsks(notes, now()) {
		if r.AskErr != nil || !match(r.Ask) || (r.State != AskOpen && r.State != AskAside) {
			continue
		}
		if err := w.yardTakesBack(row, r.Note.Seq, reason); err != nil {
			return err
		}
	}
	return nil
}

// yardTakesBack ends the ask seq on the bead row as asked no more, in the
// same write, authored yard with reason, and tells the other yards the ask
// also stands in.
func (w *wtx) yardTakesBack(row *beadRow, seq uint64, reason string) error {
	ans := Answer{Ask: seq, Words: reason, TakeBack: true}
	text, err := json.Marshal(ans)
	if err != nil {
		return err
	}
	if _, err = w.addNoted(row, NoteAnswer, string(text), stageAskAuthor); err != nil {
		return err
	}
	return w.settleCopies(row, ans, nil, "")
}
