package store

import (
	"context"
	"fmt"
	"time"

	bolt "go.etcd.io/bbolt"
)

// The grinding controls for a pairing the operator confirms on a card
// (pairing_card.go): what bounds a middle's budget to induced pairings at a
// thousandth a side. Every card attempt needs its operator's word already
// (freshWord, in the ActPairCard gate); beside it this file keeps the
// durable record of the last ended attempt per expected peer, refuses the
// cooldown on it, and tells the operator directly when an attempt ends
// one-sided.
//
// A terminal's pairing is neither held here nor checked: the yardmaster
// cannot open one, so it is not the grinding vector, and its ends stay
// exactly as they were.

// bPairAttempts holds the last ended card-pairing attempt per expected
// peer, <peer>: pairAttempt. One record per name, overwritten by each end,
// so the bucket stays bounded with no pruning. (Schema 37.)
var bPairAttempts = []byte("pairing_attempts")

func init() { allBuckets = append(allBuckets, bPairAttempts) }

// pairCooldown is how long after a card-pairing attempt for one yard ended
// that another card open or call naming it is refused. Ten minutes bounds
// induction without punishing honest retries: a lapsed card re-opens after
// it.
const pairCooldown = 10 * time.Minute

// pairAttempt is the last card-pairing attempt for one expected peer that
// ended without a link.
type pairAttempt struct {
	// At is when it ended, by the yard's clock; Reason why, one of
	// pairing's reasons; Role which side this yard was, host or joiner;
	// Pairing the pairing or call id, for the record.
	At      time.Time `json:"at"`
	Reason  string    `json:"reason"`
	Role    string    `json:"role"`
	Pairing string    `json:"pairing"`
}

// recordPairEnd keeps the ended attempt for peer: another card open or call
// naming it waits pairCooldown. It overwrites the last, whatever it was.
func (w *wtx) recordPairEnd(peer string, a pairAttempt) error {
	return put(w.Tx, bPairAttempts, peer, a)
}

// lastPairEnd is the last ended attempt for peer, nil when none was kept.
func lastPairEnd(tx *bolt.Tx, peer string) (*pairAttempt, error) {
	var a pairAttempt
	if ok, err := get(tx, bPairAttempts, peer, &a); err != nil || !ok {
		return nil, err
	}
	return &a, nil
}

// cardCooldown refuses a card open or call naming peer within pairCooldown
// of the last ended attempt for it, naming when to try again. role is this
// attempt's side, host or joiner. The refusal is kept on the record, as
// attempts and ends are: a cooldown that fired silently could be ground at
// quietly.
func (s *Store) cardCooldown(ctx context.Context, peer, role, actor string) error {
	var rec *pairAttempt
	if err := s.read(func(tx *bolt.Tx) error {
		var err error
		rec, err = lastPairEnd(tx, peer)
		return err
	}); err != nil {
		return err
	}
	if rec == nil {
		return nil
	}
	// A clock moved backwards cannot lift the cooldown early.
	elapsed := now().Sub(rec.At)
	if elapsed >= pairCooldown {
		return nil
	}
	lifts := rec.At.Add(pairCooldown)
	// The attempt has ended whether or not the refusal is written.
	_ = s.LogEvent(ctx, "", "peer_pair_cooldown", actor, map[string]any{
		"role": role, "peer": peer, "retry_after": lifts,
	})
	return fmt.Errorf("a pairing with %s is in the %s cooldown between card pairings with one yard: try again after %s",
		peer, minutes(pairCooldown), lifts.Local().Format("15:04"))
}

// ReasonPairEnded is why the operator is notified of a card pairing that
// ended without a link: told directly, without subscribing, as an ask's
// asker is told of its answer (ReasonAnswered).
const ReasonPairEnded = "pairing_ended"

// pairAlarms reports whether the end of a card pairing for reason reaches
// the operator directly: declined, timed out, or wrong digits typed. A
// confirmed pairing tells nothing: both sides saved.
func pairAlarms(reason string) bool {
	return reason == Declined || reason == TimedOut || reason == WrongCheck
}

// joinEndReason is this yard's refused reason for what the host says ended
// a call, as joinSaidNo records it.
func joinEndReason(reason string) string {
	switch reason {
	case PeerDeclined:
		return Declined
	case WrongCheck, TimedOut, Closed, AlreadyPeer:
		return reason
	default:
		return PeerDeclined
	}
}

// pairAlarmText is the end of the card pairing for peer in one line: what
// ended, and what to do when the other side shows a link from it. The line
// is docs/peers.md's too.
func pairAlarmText(peer, reason string) string {
	var head string
	switch reason {
	case Declined:
		head = fmt.Sprintf("pairing with %s ended (declined)", peer)
	case TimedOut:
		head = fmt.Sprintf("pairing with %s lapsed", peer)
	case WrongCheck:
		head = fmt.Sprintf("pairing with %s ended after %d wrong tries at the digits", peer, MaxCheckTries)
	default:
		head = fmt.Sprintf("pairing with %s ended (%s)", peer, reason)
	}
	return head + ": nothing was saved on this yard. " +
		"If the other yard shows a link from this pairing, its operator can remove this yard there (yardr peer rm <name>) and pair again."
}

// cardPeer is the yard a card pairing expects: the name it saves it under
// once it has called, the name it was opened with before.
func (p *hostPairing) cardPeer() string {
	if p.Peer != "" {
		return p.Peer
	}
	return p.offer.As
}

// endHostCard logs the end of the hosted card pairing p for reason: the
// refused event, the attempt on the record, and the alarm where one is
// owed, in one write, so the notification tells of the event it names.
func (s *Store) endHostCard(ctx context.Context, p *hostPairing, reason, actor string) {
	peer := p.cardPeer()
	_ = s.write(func(w *wtx) error {
		data := p.event(map[string]any{"reason": reason})
		if pairAlarms(reason) {
			data["notified"] = map[string]any{p.card.to: ReasonPairEnded}
		}
		if err := w.event("", "peer_pair_refused", actor, data); err != nil {
			return err
		}
		seq := w.Bucket(bEvents).Sequence()
		if err := w.recordPairEnd(peer, pairAttempt{At: now(), Reason: reason, Role: "host", Pairing: p.ID}); err != nil {
			return err
		}
		if !pairAlarms(reason) {
			return nil
		}
		n := Notification{Seq: seq, At: now(), Source: SourceYardr, Kind: "peer_pair_refused",
			Text: pairAlarmText(peer, reason)}
		return w.notify(n, n, map[string]any{p.card.to: ReasonPairEnded})
	})
}

// endJoinCallWrite logs the end of the call jc for reason without a link:
// the refused event as PairRefused writes it, the attempt on the record,
// and the alarm where one is owed, in one write. The call's name passed
// Valid at registration, as its reason passes pairRefusals at every caller.
func (s *Store) endJoinCallWrite(ctx context.Context, jc *pairCall, reason, actor string) {
	_ = s.write(func(w *wtx) error {
		data := map[string]any{"role": "joiner", "reason": reason}
		if jc.Calls != "" {
			data["calls"] = jc.Calls
		}
		if pairAlarms(reason) {
			data["notified"] = map[string]any{jc.to: ReasonPairEnded}
		}
		if err := w.event("", "peer_pair_refused", actor, data); err != nil {
			return err
		}
		seq := w.Bucket(bEvents).Sequence()
		if err := w.recordPairEnd(jc.Peer, pairAttempt{At: now(), Reason: reason, Role: "joiner", Pairing: jc.ID}); err != nil {
			return err
		}
		if !pairAlarms(reason) {
			return nil
		}
		n := Notification{Seq: seq, At: now(), Source: SourceYardr, Kind: "peer_pair_refused",
			Text: pairAlarmText(jc.Peer, reason)}
		return w.notify(n, n, map[string]any{jc.to: ReasonPairEnded})
	})
}

// finishJoinCall tells the host no and takes back the card after its end is
// already on the record.
func (s *Store) finishJoinCall(ctx context.Context, jc *pairCall, reason string) {
	if send := s.pairCallSender; send != nil {
		told, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_, _ = send(told, jc.url, jc.transcript, false)
	}
	s.retireCard(jc.Bead, jc.Ask, joinEndWords(jc, reason))
}
