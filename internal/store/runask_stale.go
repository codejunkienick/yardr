// A run ask that went stale tells its asker, so the card is asked again
// before the person meets a dead one. An approval is held to what the card
// saw (AskRun.Seen), and a source the card named may change under it: the
// approval is then refused and records nothing, so without this tell the
// asker learns of it only from the person who met the dead card. The tell
// fires when the source changes, not when the approval is refused: a
// tap-time tell reaches the asker after the person already met it.
//
// The tell is transition-only, from the before and after bytes of the write
// that changed the source: an ask fresh before the write and stale after it
// gets one staleness event, noted on its bead and delivered to its asker
// through the answer-delivery path (deliverAskStale). An ask already stale
// gets none more, and an ask born fresh by construction (its Seen written
// from the current bytes in the same transaction) gets none at creation.
// No new durable state: the event row is the standard event write.
package store

import (
	"fmt"
)

// tellSourceStale tells the askers of the open run asks naming the source
// name whose source digest moved between before and after, the source as the
// write found it and as it leaves it (nil for none: a source added or
// removed). Actor is who made the change, for the event and its sentence.
//
// It recomputes the ask's "source <name>" Seen against the new bytes with
// the row's own digest function (staleFP), and the same for the pack-target
// key a named depot's card holds for a mapped source. Where the fingerprints
// are one no ask can have transitioned, so the scan is skipped: a limit-only
// set, a same-text put and a value-only keying cost no scan. Otherwise every
// open bead's open run asks are read, as retireMappingCards reads them, and
// each ask fresh before the write and stale after gets one ask_stale event
// on its bead, naming the ask and what changed. The event goes through
// w.event in the transaction, so a batch that runs the write twice ends the
// same.
func (w *wtx) tellSourceStale(name string, before, after *Source, actor string) error {
	key := "source " + name
	target := packTargetKey + "source " + name
	beforeFP, err := w.staleFP(name, before, false)
	if err != nil {
		return err
	}
	afterFP, err := w.staleFP(name, after, false)
	if err != nil {
		return err
	}
	beforeTarget, err := w.staleFP(name, before, true)
	if err != nil {
		return err
	}
	afterTarget, err := w.staleFP(name, after, true)
	if err != nil {
		return err
	}
	if beforeFP == afterFP && beforeTarget == afterTarget {
		return nil
	}
	for _, uuid := range keys(w.Tx, bBeadOpen, "") {
		notes, err := beadNotes(w.Tx, uuid)
		if err != nil {
			return err
		}
		for _, r := range ResolveAsks(notes, now()) {
			if r.AskErr != nil || r.Ask.Run == nil {
				continue
			}
			if r.State != AskOpen && r.State != AskAside {
				continue
			}
			moved := ""
			if fp, ok := r.Ask.Run.Seen[key]; ok && fp == beforeFP && fp != afterFP {
				moved = key
			} else if fp, ok := r.Ask.Run.Seen[target]; ok && fp == beforeTarget && fp != afterTarget {
				moved = target
			}
			if moved == "" {
				continue
			}
			if err := w.event(uuid, "ask_stale", actor, map[string]any{
				"ask": r.Note.Seq, "source": name, "seen": moved,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// staleFP is the fingerprint a run ask holds for the source name under the
// source key (target false) or the pack-target key (target true), when the
// yard holds src of it, nil for none. A source that stands uses the row's
// own function (sourceSeen). One that stands not is "" where no command
// copies stand, else seen("no source", inv) as the add and apply cards hold
// it, and always "" under the target key as packTargetsSeen holds it.
func (w *wtx) staleFP(name string, src *Source, target bool) (string, error) {
	if src != nil {
		return w.sourceSeen(src)
	}
	if target {
		return "", nil
	}
	inv, _, err := w.inventoryOf(name, false)
	if err != nil {
		return "", err
	}
	if len(inv) == 0 {
		return "", nil
	}
	return seen("no source", inv), nil
}

// deliverAskStale delivers the ask_stale event e on row to its ask's asker,
// as an answer is delivered (deliver): the sentence reads in words wherever
// it lands, the asker hears it without subscribing when they are a crew
// member or a user of this yard and are not the one who changed the source,
// and the asking session routes a crew asker's tell to its slice. An asker
// with no inbox hears nothing: nobody stands to ask again. A corrupt ask
// reference delivers to the subscriptions alone, as a corrupt answer does.
func (w *wtx) deliverAskStale(e *Event, row *beadRow, out, told map[string]any) error {
	var seq uint64
	switch n := e.Data["ask"].(type) {
	case uint64:
		seq = n
	case float64:
		seq = uint64(n)
	default:
		return nil
	}
	var n Note
	if ok, err := get(w.Tx, bNotes, join(row.UUID, seqKey(seq)), &n); err != nil {
		return err
	} else if !ok || n.Kind != NoteAsk {
		return nil
	}
	source, _ := e.Data["source"].(string)
	moved, _ := e.Data["seen"].(string)
	e.Data["answer"] = staleAskSentence(e.Actor, row.ID, seq, source, moved)
	if n.Session != "" {
		e.Data["ask_session"] = n.Session
	}
	if asker := n.Author; asker != e.Actor {
		if w.Bucket(bCrew).Get([]byte(asker)) != nil {
			prev, _ := out[asker].(string)
			out[asker] = best(prev, ReasonAnswered)
		} else if w.Bucket(bUsers).Get([]byte(asker)) != nil {
			prev, _ := told[asker].(string)
			told[asker] = best(prev, ReasonAnswered)
		}
	}
	return nil
}

// staleAskSentence is a stale ask as one line of an inbox: who changed the
// source, which ask is stale, and to ask again.
func staleAskSentence(actor, bead string, seq uint64, source, moved string) string {
	return oneLine(fmt.Sprintf("%s changed the source %s: ask %d on %s is stale (%s moved). Ask again.", actor, source, seq, bead, moved))
}
