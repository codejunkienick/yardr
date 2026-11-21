package store

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	bolt "go.etcd.io/bbolt"
)

// ActRemoveBead is removing a bead for good: the operator's alone, as
// removing a depot or a user is. It stands here rather than in policy.go's
// list because that file is at its size ceiling; ActRenameGroup stands in
// its own file the same way. It is not delegable and no run ask may propose
// it (no Ask on its Object): removal is unrecoverable, so it is never
// carried out from a card. The delegated pack leg beside the operator's
// (packable) names no bead removal among its deeds, so it lets none
// through, as with the operator's other removals.
const ActRemoveBead Action = "remove a bead"

func init() { coreRules[ActRemoveBead] = packable(ruleOperator) }

// BeadRemoval is what RemoveBead took out of the store with the bead: its
// handle, how many of its notes went with it, and how many of its events
// had text redacted (their rows stay, see RemoveBead).
type BeadRemoval struct {
	Bead           string `json:"bead"`
	Notes          int    `json:"notes"`
	EventsRedacted int    `json:"events_redacted"`
}

// beadTextKeys are the event data keys that carry a bead's own words, across
// the kinds a bead's log holds (eventtold.go's bead rows): its title and
// body, at creation and in an update (whose "was" holds the ones before),
// the reason of a close or an unfold, the sentence of an answer, and a
// note's or hook's text where one is carried as words. A key is redacted
// only where its value is a string: "note" on a noted event is the note's
// seq, a number, and stays, since the log's order is not the bead's words.
var beadTextKeys = []string{"title", "body", "reason", "answer", "note", "text"}

// removedText stands where a removed bead's words stood in its events.
const removedText = "[removed]"

// redactEventData replaces the text values in one event's data with
// removedText, in place, and reports whether any went. The old values an
// update kept under "was" are redacted with the new, or the removal would
// leave the history it exists to erase.
func redactEventData(data map[string]any) bool {
	changed := false
	for k, v := range data {
		if s, ok := v.(string); ok && s != "" && s != removedText && slices.Contains(beadTextKeys, k) {
			data[k] = removedText
			changed = true
		}
	}
	if was, ok := data["was"].(map[string]any); ok && redactEventData(was) {
		changed = true
	}
	return changed
}

// RemoveBead removes a memory for good: its bead row, its place in the open
// index, its handle, its notes, its links and its ask-copy records go, and
// the text of its events is redacted where they stand. No tombstone is left:
// the bead's id and handle name nothing from this write on, and a reference
// to either fails as any unknown bead's does (resolve). Worktrees are not
// touched: a memory is never built, and a live session on one is refused
// below before any of this.
//
// The event rows stay, with their seqs and their kind and bead indexes, so
// the log's order and every inbox entry that names one stand as they were;
// an event of the bead keeps its kind, its actor and its time, and loses
// only the bead's words (redactEventData), which is the record that a
// removal happened and who did the things before it. Files a note carried
// are named by nothing once its row goes, and the blob prune frees them
// after their grace as it frees any unnamed blob (pruneBlobs): this write
// touches no file. Copies on other yards, in backups and in device caches
// are not this store's to take back and stay as they are.
//
// Only a memory goes this way (TypeMemory): a memory is a leaf of title,
// body and notes that no train folds, no claim routes and no assembly
// lands, so the refusals below close its reach without machinery per type.
// Removing any other bead is a later bead's work, and this one refuses it
// rather than half-remove it. The refusals, each naming what holds the
// bead: a dep edge either way (the other bead named), an ask on another
// bead that cites this one in its About, a snapshot of such a cite that
// another yard's ask brought here (peerAskBead), a live session on the
// bead, and membership of a train. Each is a thing that would be left
// naming a bead that is not there; the operator clears it first.
//
// Removing a bead is the operator's alone (ActRemoveBead, operatorOnly), and
// not merely a close by another name: a close keeps everything and can be
// reopened, and checkCloseOwn lets a bead's own agent close its bead. What
// is removed cannot be brought back, and an agent that could remove beads
// could remove the evidence against its own work, so no session's pane may
// do it whatever actor it names (paneAuthor decides who asks, as at every
// gate).
func (s *Store) RemoveBead(ctx context.Context, id, actor string) (*BeadRemoval, error) {
	var out *BeadRemoval
	err := s.write(func(w *wtx) error {
		resolved, err := paneAuthor(w.Tx, callerOf(ctx), actor)
		if err != nil {
			return err
		}
		actor = resolved
		row, err := w.loadBead(id)
		if err != nil {
			return err
		}
		// Batch reruns this closure when another call in the batch fails, so
		// what was removed starts over with each attempt.
		out = &BeadRemoval{Bead: row.ID}
		if row.Type != TypeMemory {
			return fmt.Errorf("bead %s is a %s, not a memory: bead rm removes memories alone, and a %s is closed instead (yardr bead close %s)", row.ID, row.Type, row.Type, row.ID)
		}
		if err := w.may(ctx, actor, ActRemoveBead, Object{Kind: KindBead, Name: row.ID}); err != nil {
			return err
		}
		if err := s.checkRemovable(w.Tx, row); err != nil {
			return err
		}
		// The notes go with the bead, files and all: the rows are all that
		// names their blobs, so deleting them unnames the files for the
		// prune (see the head comment). Any ask-copy records and issue
		// links of the bead go with it too, so nothing is left pointing at
		// a deleted UUID or blocking another bead from tracking that issue.
		noteKeys := keys(w.Tx, bNotes, row.UUID+"\x00")
		for _, k := range noteKeys {
			if err := w.Bucket(bNotes).Delete([]byte(k)); err != nil {
				return err
			}
		}
		out.Notes = len(noteKeys)
		for _, k := range keys(w.Tx, bAskCopies, row.UUID+"\x00") {
			if err := w.Bucket(bAskCopies).Delete([]byte(k)); err != nil {
				return err
			}
		}
		links, err := beadLinks(w.Tx, row.UUID)
		if err != nil {
			return err
		}
		for i := range links {
			if err := w.dropLink(&links[i]); err != nil {
				return err
			}
		}
		// No dep row can name the bead now (checkRemovable refused while
		// one did), so the row, its open place and its handle are all that
		// is left to delete.
		if err := w.Bucket(bBeadOpen).Delete([]byte(row.UUID)); err != nil {
			return err
		}
		if err := w.Bucket(bHandles).Delete([]byte(row.ID)); err != nil {
			return err
		}
		if err := w.Bucket(bBeads).Delete([]byte(row.UUID)); err != nil {
			return err
		}
		// The bead's events stay, their text redacted: read through the
		// bead index so exactly its rows are touched, and put back under
		// the same seq key, so the log and its indexes are as they were
		// but for the words.
		for _, k := range keys(w.Tx, bEventsBy, row.UUID+"\x00") {
			seq := k[len(row.UUID)+1:]
			var e Event
			ok, err := get(w.Tx, bEvents, seq, &e)
			if err != nil {
				return err
			}
			if ok && redactEventData(e.Data) {
				if err := put(w.Tx, bEvents, seq, e); err != nil {
					return err
				}
				out.EventsRedacted++
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Removal appends no event, so the event head did not move: drop any
	// cached board answers that still hold an ask or row from this bead.
	s.asksCache.clear()
	s.waitsCache.clear()
	return out, nil
}

// checkRemovable refuses removing row while anything still names it: a dep
// edge either way, an open ask that cites it, a live session on it, or a
// train it is a child of. Each refusal names what holds it, so the operator
// can clear that first; nothing is written by a refusal.
func (s *Store) checkRemovable(tx *bolt.Tx, row *beadRow) error {
	// Train membership first: a child of a train also has dep rows (its
	// parent edge, and the blocks edge that edge brings), and the train is
	// the holder to name for them.
	train, err := parentTrain(tx, row.UUID)
	if err != nil {
		return fmt.Errorf("%s: %w", row.ID, err)
	}
	if train != nil {
		return fmt.Errorf("bead %s is a child of train %s; take it out of the train first (yardr dep rm %s %s -k parent)", row.ID, train.ID, train.ID, row.ID)
	}
	h := handlesIn(tx)
	var edgesHeld []string
	for _, k := range keys(tx, bDepFrom, row.UUID+"\x00") {
		var d depRow
		ok, err := get(tx, bDeps, k[len(row.UUID)+1:], &d)
		if err != nil {
			return err
		}
		if ok {
			edgesHeld = append(edgesHeld, fmt.Sprintf("%s -%s-> %s", row.ID, d.Kind, h.of(d.To)))
		}
	}
	for _, k := range keys(tx, bDepTo, row.UUID+"\x00") {
		var d depRow
		ok, err := get(tx, bDeps, k[len(row.UUID)+1:], &d)
		if err != nil {
			return err
		}
		if ok {
			edgesHeld = append(edgesHeld, fmt.Sprintf("%s -%s-> %s", h.of(d.From), d.Kind, row.ID))
		}
	}
	if len(edgesHeld) > 0 {
		return fmt.Errorf("bead %s has dep edges: %s; remove them first (yardr dep list %s)", row.ID, strings.Join(edgesHeld, ", "), row.ID)
	}
	cite, err := askCite(tx, row)
	if err != nil {
		return err
	}
	if cite != "" {
		return fmt.Errorf("bead %s is cited by %s; take the ask back or let it be answered first", row.ID, cite)
	}
	live, err := s.liveSessions(tx, "")
	if err != nil {
		return err
	}
	for _, sess := range live {
		if sess.Bead == row.UUID {
			return fmt.Errorf("bead %s has live session %s; end it first (yardr bead hold %s ends it, or let it finish)", row.ID, sess.ID, row.ID)
		}
	}
	return nil
}

// askCite names an open or put-aside ask that cites row in its About, or ""
// when none does: "ask <seq> on <bead>" for one of this yard's asks, or for
// one another yard brought here, whose about-beads are a snapshot taken when
// it was asked (peerAskBead). An ask on row itself is no cite: it goes with
// the bead. An ask that was answered, declined or taken back is settled and
// no longer holds the bead, so following the refusal's remedy clears it. A
// note whose ask does not parse cites nothing it can name.
func askCite(tx *bolt.Tx, row *beadRow) (string, error) {
	at := now()
	var cite string
	resolved := map[string][]ResolvedAsk{}
	err := each(tx, bNotes, "", func(_ []byte, n *Note) error {
		if cite != "" || n.Kind != NoteAsk || n.Bead == row.UUID {
			return nil
		}
		var ask Ask
		if json.Unmarshal([]byte(n.Text), &ask) != nil || !slices.Contains(ask.About, row.ID) {
			return nil
		}
		asks, ok := resolved[n.Bead]
		if !ok {
			var notes []Note
			if err := each(tx, bNotes, n.Bead+"\x00", func(_ []byte, m *Note) error {
				notes = append(notes, *m)
				return nil
			}); err != nil {
				return err
			}
			asks = ResolveAsks(notes, at)
			resolved[n.Bead] = asks
		}
		for _, r := range asks {
			if r.Note.Seq == n.Seq && (r.State == AskOpen || r.State == AskAside) {
				cite = fmt.Sprintf("ask %d on %s", n.Seq, handlesIn(tx).of(n.Bead))
				break
			}
		}
		return nil
	})
	if err != nil || cite != "" {
		return cite, err
	}
	err = each(tx, bPeerAsks, "", func(_ []byte, f *foreignAsk) error {
		if cite != "" || f.Outcome != "" {
			return nil
		}
		cited := slices.Contains(f.Ask.About, row.ID) ||
			slices.ContainsFunc(f.About, func(b peerAskBead) bool { return b.ID == row.ID })
		if !cited {
			return nil
		}
		if st := f.resolved(f.Bead, f.From, at).State; st == AskOpen || st == AskAside {
			cite = fmt.Sprintf("ask %d on %s, brought from another yard", f.Seq, f.Bead)
		}
		return nil
	})
	return cite, err
}
