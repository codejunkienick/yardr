package dispatch

import (
	"context"
	"strconv"
	"strings"

	"yardr.dev/yardr/internal/store"
)

// What the bead of a pull request records of the head its events tell. The
// card a maintainer decides on in triage (store's stagehead.go) names the head
// and who pushed it, when and how large it is, and is put in the write of the
// move that brings the bead there, so the bead must hold them by then: they
// are written before the move, and after it with the event marked seen, so a
// bead the event does not move has them too. They come from the delivery and
// from nothing else: no read of the pull request is made to draw a card.

// headSize is the size of the change a delivery told, nil when it told none
// or not all of the three counts. field is the envelope's first value of a key.
func headSize(field func(string) string) *store.HeadSize {
	var n [3]int
	for i, key := range []string{"additions", "deletions", "changed_files"} {
		v, err := strconv.Atoi(field(key))
		if err != nil || v < 0 {
			return nil
		}
		n[i] = v
	}
	return &store.HeadSize{Additions: n[0], Deletions: n[1], Files: n[2]}
}

// headMeta is what the bead records of the head ev told: the head, as it always
// has, and, for a pull request's bead, what the delivery said of it when the
// head is a new one. Another delivery of the head the bead has already (a label
// put on it, a review of it) says nothing of who pushed it, so it leaves the
// first delivery's facts as they are. An event that tells no head is the
// caller's to filter.
func headMeta(bead *store.Bead, ev ruleEvent) map[string]any {
	meta := map[string]any{metaPRHead: ev.sha}
	if bead.Type != "pr" {
		return meta
	}
	if cur, _ := bead.Metadata[metaPRHead].(string); strings.EqualFold(cur, ev.sha) {
		return meta
	}
	meta[store.MetaPRHeadTold] = store.HeadTold{Sha: ev.sha, At: ev.at, By: ev.by, Action: ev.action, Size: ev.size}
	return meta
}

// recordHead writes the head ev told to the bead before the move the event
// makes, so that the card the move puts names the head that brought the bead
// there and not the one before it. Only a pull request's bead has a card: any
// other bead, a train's, is written as it always was, after the move. A write
// that fails is logged and the move goes on: the card then shows the earlier
// head, and a pick of it is refused as stale, which is safe; the write after
// the move tries again.
func (d *Dispatcher) recordHead(ctx context.Context, bead *store.Bead, ev ruleEvent) {
	if bead.Type != "pr" {
		return
	}
	if _, err := d.Store.UpdateBead(ctx, bead.ID, store.BeadPatch{Metadata: headMeta(bead, ev)}, store.Expect{}, actor); err != nil {
		d.Log.Warn("hook event: recording the head before the move", "bead", bead.ID, "seq", ev.seq, "err", err)
	}
}
