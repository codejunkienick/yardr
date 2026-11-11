// A stage ask that offers a maintainer's yes to a pull request's head (the
// contributors pack's triage stage: a choice template with accepts:) shows the
// head the bead tracks and records the head it showed as the bead's
// accepted_head when the accepting outcome is picked. The yes is of one
// commit, so the head is frozen into the card as it is put (Ask.Head), and a
// pick after the pull request has another head accepts nothing: it is refused
// whole, as a pick on a card of an earlier stay is.
//
// What the card shows comes from the bead and nothing else: the head is the
// bead's pr_head and the facts of it (who pushed it, when, how large) are its
// pr_head_told, both written by the dispatcher from the hook deliveries it
// judged, before the move that puts the card. Nothing of the pull request is
// fetched or run to draw it.
package store

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	// MetaPRHead is the head commit of the pull request a bead tracks, as the
	// last delivery that told one gave it. The dispatcher writes it before it
	// moves the bead on that delivery, so the card the move puts names the
	// head that brought the bead there, and not the one before it.
	MetaPRHead = "pr_head"
	// MetaPRHeadTold is HeadTold: what the delivery that told that head said
	// of it.
	MetaPRHeadTold = "pr_head_told"
)

// HeadTold is what the delivery that told a pull request's head said of it:
// the commit it is of, who acted and what they did (the delivery's action:
// synchronize for a push, opened), when the yard heard, and how large the
// change is where the delivery said. A card shows only what it records, and
// only for the head the bead tracks now.
type HeadTold struct {
	Sha    string    `json:"sha"`
	At     time.Time `json:"at"`
	By     string    `json:"by,omitempty"`
	Action string    `json:"action,omitempty"`
	Size   *HeadSize `json:"size,omitempty"`
}

// HeadSize is the size of a pull request's change as its delivery gave it.
type HeadSize struct {
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
	Files     int `json:"files"`
}

// AskHead is the head a stage ask's card shows, frozen as the card is put:
// the commit the pick would accept (empty where the bead had none, which a
// pick refuses) and the outcome whose pick accepts it. The yard's own value:
// newAsk clears whatever a caller sends.
type AskHead struct {
	Sha     string `json:"sha,omitempty"`
	Accepts string `json:"accepts"`
}

// headOf is the head the bead tracks, "" where no delivery has told one.
func headOf(b *Bead) string {
	sha, _ := b.Metadata[MetaPRHead].(string)
	return sha
}

// headTold reads what the bead records of its head's delivery, and only
// where it is of the head the bead tracks: a head written by hand with the
// facts of another is shown without them.
func headTold(b *Bead) (HeadTold, bool) {
	raw, ok := b.Metadata[MetaPRHeadTold]
	if !ok {
		return HeadTold{}, false
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return HeadTold{}, false
	}
	var told HeadTold
	if json.Unmarshal(data, &told) != nil || !strings.EqualFold(told.Sha, headOf(b)) {
		return HeadTold{}, false
	}
	return told, true
}

// headVerbs are what a delivery's action says was done to the head: a push
// is the one the card is for, and the others name the delivery that told the
// head first.
var headVerbs = map[string]string{
	"synchronize":      "pushed",
	"opened":           "opened",
	"reopened":         "reopened",
	"ready_for_review": "marked ready",
}

// headLine is the context line of a card that shows the head of the bead b:
// the short commit with what is recorded of who pushed it, when and how large
// it is, or, for a bead with no head, that the card has none to accept and how
// the yes is given instead. The time is absolute: the line is frozen into the
// card, and "an hour ago" would be wrong by the time it is read.
func headLine(b *Bead) string {
	sha := headOf(b)
	if sha == "" {
		return fmt.Sprintf("Head: none told. No delivery of this pull request has named its head, so this card has none to accept: put the label accepted on the pull request, or record the commit you read and move the bead by hand (yardr bead update %s --meta accepted_head=<sha>, then yardr bead advance %s --to review).", b.ID, b.ID)
	}
	short := sha
	if len(short) > 7 {
		short = short[:7]
	}
	told, ok := headTold(b)
	if !ok {
		return fmt.Sprintf("Head: %s. The delivery that told it said nothing of who pushed it, when or how large it is.", short)
	}
	var did []string
	if verb := headVerbs[told.Action]; verb != "" || told.By != "" {
		said := or(verb, "told")
		if told.By != "" {
			said += " by " + told.By
		}
		did = append(did, said)
	}
	if !told.At.IsZero() {
		did = append(did, "at "+told.At.UTC().Format("2006-01-02 15:04 UTC"))
	}
	var facts []string
	if len(did) > 0 {
		facts = append(facts, strings.Join(did, " "))
	}
	if told.Size != nil {
		facts = append(facts, fmt.Sprintf("+%d -%d in %s", told.Size.Additions, told.Size.Deletions, countNoun(told.Size.Files, "file")))
	}
	if len(facts) == 0 {
		return fmt.Sprintf("Head: %s. The delivery that told it said nothing of who pushed it, when or how large it is.", short)
	}
	return fmt.Sprintf("Head: %s, %s, as the delivery that told it said.", short, strings.Join(facts, ", "))
}

// askHead is the head the template's card shows, as framed put it in: nil for
// a template that offers no yes to a head.
func (t *AskTemplate) askHead() *AskHead {
	if t.Accepts == "" {
		return nil
	}
	return &AskHead{Sha: t.head, Accepts: t.Accepts}
}

// headCurrent refuses a pick of the outcome move on the card ask when it would
// accept a head the pull request no longer has, or none: the yes is of the
// commit the person was shown, so a push after the card was put, or a card
// that showed no head, accepts nothing. seq is the card's number. A pick of any
// other outcome, close or leave, takes no head and is never refused here.
func headCurrent(row *beadRow, ask Ask, seq uint64, move string) error {
	if ask.Head == nil || move == "" || move != ask.Head.Accepts {
		return nil
	}
	byHand := fmt.Sprintf("accept the head you read with the label accepted on the pull request, or yardr bead update %s --meta accepted_head=<sha> and yardr bead advance %s --to review", row.ID, row.ID)
	now := headOf(&row.Bead)
	switch {
	case ask.Head.Sha == "":
		return fmt.Errorf("ask %d showed no head, so picking %s accepts nothing: %s", seq, move, byHand)
	case !strings.EqualFold(ask.Head.Sha, now):
		return fmt.Errorf("ask %d showed the head %s, and the pull request has %s now: a push came after the card was put, and nothing was accepted. Look at the new head, then %s",
			seq, shortOf(ask.Head.Sha), or(shortOf(now), "none"), byHand)
	}
	return nil
}

// shortOf is the first seven characters of a commit, as a card shows it.
func shortOf(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// stagePickOf is stagePick for the pick of option move on the stage ask seq of
// the bead row, which also records the head the card showed as the bead's
// accepted_head when move is the outcome the card accepts with: in the row the
// move writes and in the move's own event, so the yes and the move are one
// write and a refusal rolls both back, as a mapping card's pick writes its
// identity in the answer's write. headCurrent has checked, in this write, that
// the head is still the pull request's. Any other pick is stagePick's.
func (w *wtx) stagePickOf(row *beadRow, seq uint64, move string) func(f *Flow, b *beadRow, data map[string]any) (string, error) {
	pick := stagePick(move)
	return func(f *Flow, b *beadRow, data map[string]any) (string, error) {
		to, err := pick(f, b, data)
		if err != nil {
			return "", err
		}
		notes, err := beadNotes(w.Tx, row.UUID)
		if err != nil {
			return "", err
		}
		i := slices.IndexFunc(notes, func(n Note) bool { return n.Seq == seq && n.Kind == NoteAsk })
		if i < 0 {
			return to, nil
		}
		ask, err := ParseAsk(notes[i])
		if err != nil || ask.Head == nil || move != ask.Head.Accepts {
			return to, nil
		}
		if b.Metadata == nil {
			b.Metadata = map[string]any{}
		}
		b.Metadata[metaAcceptedHead] = ask.Head.Sha
		data[metaAcceptedHead] = ask.Head.Sha
		return to, nil
	}
}
