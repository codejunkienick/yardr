// Crew-first stage asks: where a manual group sets ask_crew_first, a
// stage's ask goes to the group's first crew member alone, and the group's
// people are asked only when that member passes the ask up (it takes its
// ask back with its words, which stand as the authored answer note) or
// stays silent past the group's wait (FallThroughStageAsk, which the
// dispatcher's waits scan calls: internal/dispatch/waits.go). A group
// with no crew member, or that does not set the key, asks its people as
// it always did.
package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// defaultCrewWait is how long a crew member's silence lasts before the
// ask falls through where the group sets no wait: silence must still
// fall, or the bead waits unseen for good.
const defaultCrewWait = time.Hour

// AskCrewFirst reports whether the group asks its crew member first: its
// ask_crew_first setting, read as a ConfigSwitch is (notify's reader in
// internal/runner reads it the same way). A group of any other runner
// reads nothing: ConfigKeys refuses the key there.
func (g Group) AskCrewFirst() bool {
	switch v := g.Config["ask_crew_first"].(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "1" || v == "yes"
	}
	return false
}

// CrewWait is how long the group's crew member may stay silent before
// the ask falls through: its wait, and defaultCrewWait where it sets
// none. A wait that does not parse is no wait (ConfigProblems refuses
// such a value where it is written). The dispatcher's waits scan reads
// it too, to know when a call can fall through.
func (g Group) CrewWait() time.Duration {
	if s, _ := g.Config["wait"].(string); s != "" {
		if wait, err := ParseSpan(s); err == nil {
			return wait
		}
	}
	return defaultCrewWait
}

// ruleTakeBackAsk refuses taking ask Object.Seq back to anyone but who
// asked it (Object.Whose), the operator, and a stage ask's addressee
// (stageAskPassUp). It lives here, not in policy.go, which has no room:
// its crew-first case is this file's to say.
func ruleTakeBackAsk(ctx context.Context, tx *bolt.Tx, actor string, _ Action, obj Object) error {
	asker := obj.Whose
	if actor == asker {
		return nil
	}
	if up, err := stageAskPassUp(tx, obj, actor); err != nil || up {
		return err
	}
	operator, this, err := notOperator(ctx, tx, actor)
	if err != nil || this == "" {
		return err
	}
	return fmt.Errorf("only %s, who asked it, or %s may take ask %d back; this is %s", asker, operator, obj.Seq, this)
}

// stageAskPassUp reports whether actor taking the ask obj.Seq on the bead
// obj.Name back is a crew member passing a stage ask up: the ask is a
// stage ask addressed to actor, and actor is a crew member. A stage
// ask's person addressee, and a hand ask's, declines instead, as before:
// this is the crew member's door alone. Who else may take an ask back is
// ruleTakeBackAsk's. False where the bead or the ask is not there: the
// refusal above is then the answer, as it was.
func stageAskPassUp(tx *bolt.Tx, obj Object, actor string) (bool, error) {
	if tx.Bucket(bCrew).Get([]byte(actor)) == nil {
		return false, nil
	}
	row, err := loadBead(tx, obj.Name)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	notes, err := beadNotes(tx, row.UUID)
	if err != nil {
		return false, err
	}
	for _, r := range ResolveAsks(notes, now()) {
		if r.Note.Seq == obj.Seq && r.AskErr == nil {
			return r.Ask.Stage != "" && r.Ask.To == actor, nil
		}
	}
	return false, nil
}

// firstCrewMember is the first crew member the group's members name, in
// members order: the one a crew-first ask goes to. A name that is no crew
// member of this yard names nobody.
func (w *wtx) firstCrewMember(g *Group) (string, bool) {
	for _, member := range g.Members {
		if w.isCrewMember(member) {
			return member, true
		}
	}
	return "", false
}

// isCrewMember reports whether name is a crew member of this yard. A
// member naming another yard is a person of that yard, never crew here.
func (w *wtx) isCrewMember(name string) bool {
	return !strings.Contains(name, "@") && w.Bucket(bCrew).Get([]byte(name)) != nil
}

// putPersonStageAsks puts the stage ask askFor builds to each person
// member of g, authored yard: one per user-member, and one addressed
// across per remote member (putRemoteAsk). Crew members and names that
// are no user are skipped. It reports how many members got an ask.
func (w *wtx) putPersonStageAsks(row *beadRow, g *Group, stage string, askFor func(to string) Ask) (int, error) {
	got := 0
	for _, member := range g.Members {
		if strings.Contains(member, "@") {
			ok, err := w.putRemoteAsk(row, askFor(member), stage, member)
			if err != nil {
				return 0, err
			}
			if ok {
				got++
			}
			continue
		}
		if w.Bucket(bCrew).Get([]byte(member)) != nil {
			continue
		}
		if w.Bucket(bUsers).Get([]byte(member)) == nil {
			continue
		}
		if err := w.putLocalStageAsk(row, askFor(member)); err != nil {
			return 0, err
		}
		got++
	}
	return got, nil
}

// stayEntryAt is when the stay entry names began: the At of its event.
// Zero where the log no longer reaches it (pruned), which counts as
// long past any wait.
func stayEntryAt(tx *bolt.Tx, uuid string, entry uint64) (time.Time, error) {
	for _, k := range keys(tx, bEventsBy, uuid+"\x00") {
		var e Event
		if ok, err := get(tx, bEvents, k[len(uuid)+1:], &e); err != nil {
			return time.Time{}, err
		} else if ok && e.Seq == entry {
			return e.At, nil
		}
	}
	return time.Time{}, nil
}

// FallThroughStageAsk asks the people of the bead's stage group, in one
// write, where the group asks its crew member first and that member has
// passed the stage's ask up or stayed silent: the dispatcher's waits
// scan calls it by its own clock (at), and the store decides again here
// what stands. It reports whether anyone was asked.
//
// What stands, of the stay the bead is in (the asks whose Entry is
// latestStayEntry, of its stage): any ask to a person, open or decided,
// ends it, so a person's decline is respected and never re-placed, and
// the placed asks are the memory that keeps a later call a no-op. A crew
// ask the member took back is a pass-up: the people are asked at once,
// and the member's words already stand as the authored answer note of
// its take-back (via.go), so nothing is written for them here. A crew
// ask still open or put aside past the group's wait (CrewWait) is taken
// back by the yard, with the silence named, and the people asked. Before
// the wait, silence asks nobody new. A closed bead, a bead whose group
// is not a manual crew-first one, and a stay with no crew ask (the
// people were asked on entry, or the stage put no ask) are all left as
// they are. The people's asks are the crew member's card with the
// address changed: the same question the member was asked, frozen at
// entry, whatever the template says now.
func (s *Store) FallThroughStageAsk(ctx context.Context, id string, at time.Time) (bool, error) {
	if at.IsZero() {
		at = now()
	}
	placed := false
	err := s.write(func(w *wtx) error {
		row, err := w.loadBead(id)
		if err != nil {
			return err
		}
		if row.Status == "closed" {
			return nil
		}
		g, err := stageGroup(w.Tx, row)
		if err != nil {
			return err
		}
		if g == nil || g.Runner != "manual" || !g.AskCrewFirst() {
			return nil
		}
		entry, err := latestStayEntry(w.Tx, row.UUID)
		if err != nil {
			return err
		}
		notes, err := beadNotes(w.Tx, row.UUID)
		if err != nil {
			return err
		}
		var crew *ResolvedAsk
		standing, passed := false, false
		for _, r := range ResolveAsks(notes, now()) {
			if r.AskErr != nil || r.Ask.Stage != row.Stage || r.Ask.Entry != entry {
				continue
			}
			if !w.isCrewMember(r.Ask.To) {
				// A person was asked this stay, open or decided:
				// nothing falls through over their answer.
				return nil
			}
			c := r
			crew = &c
			// One crew member is asked per stay, so one crew ask
			// stands; the latest of them is the stay's, if a group
			// was changed under it.
			standing, passed = false, false
			switch r.State {
			case AskOpen, AskAside:
				standing = true
			case AskTakenBack:
				passed = true
			}
		}
		if crew == nil || (!standing && !passed) {
			return nil
		}
		if standing && !passed {
			since, err := stayEntryAt(w.Tx, row.UUID, entry)
			if err != nil {
				return err
			}
			wait := g.CrewWait()
			if at.Sub(since) < wait {
				return nil
			}
			reason := fmt.Sprintf("no answer from %s within %s: the ask goes to the group's people", crew.Ask.To, Span(wait))
			if err := w.yardTakesBack(row, crew.Note.Seq, reason); err != nil {
				return err
			}
		}
		base := crew.Ask
		got, err := w.putPersonStageAsks(row, g, row.Stage, func(to string) Ask {
			ask := base
			ask.To, ask.Fallback = to, false
			return ask
		})
		if err != nil {
			return err
		}
		if got == 0 {
			operator, err := operatorIn(w.Tx)
			if err != nil || operator == "" {
				return err
			}
			ask := base
			ask.To, ask.Fallback = operator, true
			if err := w.putLocalStageAsk(row, ask); err != nil {
				return err
			}
		}
		placed = true
		return nil
	})
	return placed, err
}
