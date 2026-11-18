package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	bolt "go.etcd.io/bbolt"
)

// Who opens a pairing whose operator confirms it on a card (pairing_card.go).
// The gate is an Action of its own, ActPairCard, and not ActPair with a leg
// added: a terminal's pairing stays the operator's alone, as it was, and
// this one adds the yardmaster for a person of the yard who has no terminal
// to type it in. The operator is not the one who opens it in that case, and
// is the one who confirms: the card names the opener and whose word it
// opened on, and the digits typed on it are the operator's own.
//
// It is declared and registered here and not in policy.go, which stands at
// the file-size ceiling the test gate holds (.yardr/test), as runask.go's
// rows are registered beside their functions.

// ActPairCard is opening a pairing the operator confirms on a card.
const ActPairCard Action = "open a pairing for a card"

func init() { coreRules[ActPairCard] = rulePairCard }

// rulePairCard is who may open a pairing for a card: the operator, as for any
// pairing, and beside them the yardmaster's line session, on the cited mail
// of a user of the yard (the delegation leg, delegatedFor with any user: the
// person whose word opens it need not be the operator, who confirms it
// after). Never another agent, a conversation session of the yardmaster,
// mail from another yard, a session, a timer or a thread, or a call that
// came through a peer's reach; and never a word that was used before or has
// gone stale (freshWord), so that an opening that failed is not tried again
// on the same mail.
func rulePairCard(ctx context.Context, tx *bolt.Tx, actor string, act Action, obj Object) error {
	if reach := reachOf(ctx); reach != "" {
		return fmt.Errorf("a pairing is not opened through the reach of %s: a peer does not pair this yard on its own word", reach)
	}
	err := ruleOperator(ctx, tx, actor, act, obj)
	var only *OperatorOnlyError
	if err == nil || !errors.As(err, &only) {
		return err
	}
	d, derr := delegatedFor(ctx, tx, actor, true)
	if derr != nil || d == nil {
		// Where no delegation is made, or the switch is off, the refusal
		// stands in the words of any pairing's.
		if derr != nil {
			return derr
		}
		return err
	}
	line, user, err := paneCrewSession(tx, callerOf(ctx))
	if err != nil {
		return err
	}
	if line == nil {
		return fmt.Errorf("%s is the yardmaster's line session's, on a user's fresh word: this is no crew pane", obj.does(act))
	}
	if user != "" {
		return fmt.Errorf("%s is the yardmaster's line session's, on a user's fresh word: this is its conversation with %s", obj.does(act), user)
	}
	return freshWord(tx, d)
}

// freshWord holds the mail a delegated opening cites to being a fresh word:
// at most CardTTL old, and not named by an earlier opening (a
// peer_pair_opened event of the same mail). A pairing is opened on one word:
// the yardmaster that tries again after the first one failed or lapsed asks
// the person again, and does not open another on the mail it has used. The
// delegation held the mail to being a mail from a user to the yardmaster
// already (delegatedFor).
func freshWord(tx *bolt.Tx, d *DelegatedCall) error {
	var e Event
	if ok, err := get(tx, bEvents, seqKey(d.Mail), &e); err != nil || !ok {
		return err
	}
	if age := now().Sub(e.At); age > CardTTL {
		return fmt.Errorf("%s's mail %d is %s old, and a pairing is opened on a fresh word, one from the last %s: ask them again", d.For, d.Mail, minutes(age), minutes(CardTTL))
	}
	// An opening can only cite a mail that came before it, so the events
	// after the mail are the ones to look through.
	prefix := []byte(join("peer_pair_opened", ""))
	c := tx.Bucket(bEventsKd).Cursor()
	for k, _ := c.Seek([]byte(join("peer_pair_opened", seqKey(d.Mail+1)))); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
		var opened Event
		if ok, err := get(tx, bEvents, string(k[len(prefix):]), &opened); err != nil {
			return err
		} else if !ok {
			continue
		}
		if mail, _ := opened.Data["mail"].(float64); uint64(mail) == d.Mail {
			return fmt.Errorf("%s's mail %d opened a pairing already (event %d): a pairing is opened on a fresh word, so ask them again", d.For, d.Mail, opened.Seq)
		}
	}
	return nil
}
