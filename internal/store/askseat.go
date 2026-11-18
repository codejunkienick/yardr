package store

import (
	"context"
	"fmt"
	"strings"
)

// The yard's login card on a crew journal, and the seat it waits on: moved
// out of asks.go, which stands at the file-size ceiling the test gate holds
// (.yardr/test), with the answer path it guards unchanged.

// seatLeftRefusal refuses an answer to the yard's login card on a crew
// journal after its seat left the login screen: the answer made after the
// leave, before the next pass takes the card back, would lead a login
// nothing waits on. Dispatch learns of the leave at the next pass, which
// is the window itself, so only the answer path sees the answer in time,
// at write time here.
//
// The card is an ask the yard authored on a journal bead, and the answer
// a pick or words: declining the dead card ends it, and putting it aside
// leaves it to the sweep. The seat is any session of the crew member the
// journal is of — the line session and every conversation, as seatsAtLogin
// counts them — live and marked at the screen; a session seated while the
// card's own left still takes the answer, since no ask names its seat.
// Ordinary answers pay one string comparison; the notes and the crew are
// read only on the rare daemon-ask path. With the refusal it answers
// RefusedAskSeatLeft, as answerable answers its own; the local caller
// keeps the words alone.
func (s *Store) seatLeftRefusal(ctx context.Context, id string, ans Answer) (string, error) {
	if !strings.HasSuffix(id, "-journal") || ans.Decline || ans.Later {
		return "", nil
	}
	notes, err := s.Notes(ctx, id)
	if err != nil {
		return "", err
	}
	yard := false
	for _, n := range notes {
		if n.Seq == ans.Ask && n.Kind == NoteAsk && n.Author == "yard" {
			yard = true
			break
		}
	}
	if !yard {
		return "", nil
	}
	crew, err := s.Crew(ctx)
	if err != nil {
		return "", err
	}
	for _, r := range crew {
		if r.Journal != id {
			continue
		}
		if screen, _ := r.CrewSession.login(); screen {
			return "", nil
		}
		for _, cs := range r.Conversations {
			if screen, _ := cs.login(); screen {
				return "", nil
			}
		}
		break
	}
	// The journal remains after its crew member is deleted. Its login card
	// still waits on a seat that no longer exists, so refuse it as well.
	return RefusedAskSeatLeft, fmt.Errorf("ask %d waits on a seat that left its login screen: the yard takes the card back", ans.Ask)
}
