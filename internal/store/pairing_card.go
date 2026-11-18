package store

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// A pairing the operator confirms on a card (docs/peers.md, "Pairing from
// footplate"). It is the host's pairing of pairing.go with three things
// changed: it is opened for CardTTL end to end, so the operator it asks has
// an hour from the opening and not minutes from the call; once the other
// yard has called, the yard files a card for the operator, a words ask on a
// question bead of its own; and the operator confirms by typing the last
// three digits of the check, which the other operator reads out, in place of
// answering yes in a terminal.
//
// The check is split so that what is shown and what is typed are disjoint:
// the card shows the first three digits and asks for the last three. A
// card that showed six and asked for six would pass on copying one's own
// screen, which is the habit the typing is there to break. What a try is
// worth is a thousandth, and what keeps that from being ground at is that
// the fifth wrong one ends the pairing (MaxCheckTries), that every opening
// needs a word of its own (freshWord), and that the card says who opened it
// and on whose word before it asks for anything.
//
// The check the digits are compared with is the pairing's, in server memory,
// and never anything the ask says: the ask holds the shown half as card text
// and the typed half nowhere, and the answer it records says that the
// digits were right, not what they were. The code is not on the card, the
// carrier, an answer or an event: PairOpen returns it, once.

// pairCard is what a card pairing holds beside the pairing.
type pairCard struct {
	// to is the operator the card is put to, and word who opened the
	// pairing and on whose word, as the card says it.
	to, word string
	// bead is the question bead that carries the card, and ask the card's
	// seq on it, once the other yard has called.
	bead string
	ask  uint64
	// tries counts the wrong digits typed.
	tries int
}

// cardOpened logs the opening of a card pairing, and where the yardmaster
// opened it on a user's word, records that as a delegation: both in one write
// that judges the gate again, so that what the opening is held to is what the
// write sees. It fills in who the card is put to and the word it names.
func (s *Store) cardOpened(ctx context.Context, p *hostPairing, url, actor string) error {
	return s.write(func(w *wtx) error {
		if err := w.may(ctx, actor, ActPairCard, Object{Kind: KindPeer}); err != nil {
			return err
		}
		who, err := paneAuthor(w.Tx, callerOf(ctx), actor)
		if err != nil {
			return err
		}
		ops, err := operatorsIn(w.Tx)
		if err != nil {
			return err
		}
		d, err := delegatedFor(ctx, w.Tx, actor, true)
		if err != nil {
			return err
		}
		to, word := who, "opened by "+who
		data := p.event(map[string]any{"expires": p.Expires, "url": url})
		if d != nil {
			to, word = d.For, fmt.Sprintf("opened by %s on %s's word, their mail %d", who, d.For, d.Mail)
			data["for"], data["mail"] = d.For, d.Mail
		}
		if !ops.is(to) {
			// The operator confirms: the one who opened it, or the one whose word
			// opened it, when they are one; otherwise the yard's first.
			if ops.none() {
				return fmt.Errorf("a card is put to the operator, and this yard has none: yardr settings set operator <name>")
			}
			to = ops[0]
		}
		p.card.to, p.card.word = to, word
		if err := w.event("", "peer_pair_opened", who, data); err != nil || d == nil {
			return err
		}
		return w.recordDelegated(d, "a pairing for a card", DelegatedBody{
			Changes: []DelegatedChange{{Key: "pairing", Is: p.ID, Has: true}, {Key: "peer", Is: p.offer.As, Has: true}},
			Back:    "the operator ends it in a terminal (yardr peer pair --end), or declines its card when it comes",
		})
	})
}

// minutes is d as it is said in a refusal: "60 minutes".
func minutes(d time.Duration) string { return fmt.Sprintf("%d minutes", int(d.Minutes())) }

// reopenWords is the command that opens the pairing again, as a card and a
// refusal say it.
func (p *hostPairing) reopenWords() string {
	return "yardr peer pair --card --name " + p.Peer
}

// cardAsk is the card for a pairing the other yard has called: a words ask
// to the operator that says who the other yard is, what was expected, who
// opened it on whose word, and what it takes. The yard's own sentences, each
// one line: what the other yard calls itself and its key are checked names
// and keys (Party.Valid), nothing a person typed.
func (p *hostPairing) cardAsk() Ask {
	shown, _ := CheckHalves(p.Check)
	return Ask{
		Question: fmt.Sprintf("Pair this yard with %s? Type the last three digits of the check its operator reads out", p.Calls),
		To:       p.card.to,
		Context: []string{
			fmt.Sprintf("The check is six digits. This card shows you the first three, %s, and asks for the last three, which the other operator reads out to you. Read them the first three as well.", shown),
			fmt.Sprintf("A yard that calls itself %s called with the code, and its key is %s. This yard expected %s, and saves it under that name.", p.Calls, p.Key, p.Peer),
			fmt.Sprintf("Pairing %s, good until %s.", p.card.word, p.Expires.Local().Format("15:04")),
			"Decline a pairing you did not ask for, or one whose digits you cannot compare: nothing is saved on either yard until the digits are right and the other operator has confirmed too.",
			fmt.Sprintf("Five wrong tries end it. If it lapses, or the server restarts, it is gone: open another with %s.", p.reopenWords()),
		},
		Pair: p.ID,
	}
}

// fileCard files the card of p, which the other yard has just called: a
// question bead to carry it (the ask --new precedent: nothing routes it) and
// the ask on it, authored by the yard, in one write. The operator is told as
// of any ask put to them. It fills in where the card is.
func (s *Store) fileCard(ctx context.Context, p *hostPairing) error {
	ask := p.cardAsk()
	body := fmt.Sprintf("The card of a pairing: the yard that calls itself %s asked to be this yard's peer, and this yard expected %s. Pairing %s. "+
		"Nothing routes this bead: it carries the question, and the yard closes it when the pairing ends.", p.Calls, p.Peer, p.card.word)
	create, err := s.newBead(ctx, NewBead{Title: ask.Question, Body: body, Type: TypeQuestion, Labels: []string{"topic:pairing"}}, "yard")
	if err != nil {
		return err
	}
	return s.write(func(w *wtx) error {
		row, err := create(w)
		if err != nil {
			return err
		}
		// Weighed by the yard from the card it wrote, as any ask is.
		if ask.Weight, err = w.weighAsk(ask); err != nil {
			return err
		}
		text, err := json.Marshal(ask)
		if err != nil {
			return err
		}
		n, err := w.addNoted(row, NoteAsk, string(text), "yard")
		if err != nil {
			return err
		}
		if err := w.queueAskPush(ask.To, row.UUID, n.Seq, ""); err != nil {
			return err
		}
		if err := w.queueFootAsk(ask.To, row.UUID, n.Seq, ""); err != nil {
			return err
		}
		p.card.bead, p.card.ask = row.ID, n.Seq
		return nil
	})
}

// pairWeight weighs a pairing card: its answer makes the other yard a peer,
// which the yard then listens to and answers, so it is trust, and a strong
// card with it (StrongWeight).
func pairWeight() []AskWeight {
	return []AskWeight{weighs(WeightTrust, "typing the digits saves the yard named on the card as a peer: the link answers its ping, and it can then be given more")}
}

// retireCard ends a card: its ask, when it still waits, is taken back with
// why, and the question bead that carries it is closed, by the yard that
// wrote them. A pairing has ended whether or not its card could be cleared,
// so it fails nothing: a card left is answered with the reason it is gone.
func (s *Store) retireCard(bead string, seq uint64, why string) {
	_ = s.write(func(w *wtx) error {
		row, err := w.loadBead(bead)
		if err != nil || row.Status == "closed" {
			return err
		}
		notes, err := beadNotes(w.Tx, row.UUID)
		if err != nil {
			return err
		}
		for _, r := range ResolveAsks(notes, now()) {
			if r.Note.Seq != seq || (r.State != AskOpen && r.State != AskAside) {
				continue
			}
			text, err := json.Marshal(Answer{Ask: seq, Words: why, TakeBack: true})
			if err != nil {
				return err
			}
			if _, err := w.addNoted(row, NoteAnswer, string(text), "yard"); err != nil {
				return err
			}
		}
		if row, err = w.loadBead(bead); err != nil {
			return err
		}
		return w.closeCard(row, why, "yard")
	})
}

// endCard clears the card of a pairing that ended, saying why in words that
// name what to do next. Nothing is done for one whose card was never filed.
func (s *Store) endCard(p *hostPairing, reason string) {
	if p.card == nil || p.card.ask == 0 {
		return
	}
	why := fmt.Sprintf("the pairing ended (%s): nothing was saved on either yard", reason)
	switch reason {
	case TimedOut:
		why = fmt.Sprintf("the pairing lapsed at %s, %s after it opened: nothing was saved on either yard. Open another with %s", p.Expires.Local().Format("15:04"), minutes(CardTTL), p.reopenWords())
	case WrongCheck:
		why = fmt.Sprintf("%d wrong tries at the digits ended the pairing: nothing was saved on either yard. Open another with %s", MaxCheckTries, p.reopenWords())
	case Declined:
		why = "declined: nothing was saved on either yard"
	case PeerDeclined:
		why = "the other yard's operator declined, or that yard could not add this one: nothing was saved on either yard"
	case Closed:
		why = "the pairing was ended: nothing was saved on either yard"
	case AlreadyPeer, BadName, Own:
		why = p.Detail
	}
	s.retireCard(p.card.bead, p.card.ask, strings.ReplaceAll(why, "\n", " "))
}

// confirmHost is this yard's operator's yes on the check: the peer is saved
// once the other yard's operator has confirmed too. PairAnswer's yes and the
// digits typed on a card both end here.
func (s *Store) confirmHost(ctx context.Context, p *hostPairing, actor string) {
	p.Confirmed, p.actor = true, actor
	if p.PeerConfirmed {
		s.finishPairing(ctx, p)
	}
}

// CheckHalves splits a check as Transcript.Check shows it, "482 913", into the
// digits a card shows and the digits it asks for: the first three and the
// last three.
func CheckHalves(check string) (shown, typed string) {
	if len(check) != 7 {
		return check, ""
	}
	return check[:3], check[4:]
}

// cardDigits reads what an operator typed on a card as the three digits it
// asks for: digits and spacing between them, and no more or fewer.
func cardDigits(words string) (string, bool) {
	var b strings.Builder
	for _, r := range words {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-':
		default:
			return "", false
		}
	}
	return b.String(), b.Len() == 3
}

// cardOutcome is what an answer to a card did to its pairing, which settle
// carries out once the answer is written.
type cardOutcome int

const (
	cardNothing cardOutcome = iota
	cardConfirmed
	cardDeclined
)

// wrongDigits is the refusal of digits that are not the check's, which
// settle counts: counting it where it is judged would count again each time
// the write runs again.
type wrongDigits struct{}

func (wrongDigits) Error() string { return "those are not the last three digits of the check" }

// errCardGone is the refusal of an answer to a card whose pairing is no
// more: it ended with the server that held it, or past its hour.
var errCardGone = errors.New("that pairing is gone")

// cardAnswer is the hold an answer to a pairing card keeps on the pairing it
// confirms. The pairing's lock is taken before the answer's write, which is
// the order its lock is always taken in (pairs), and held until the answer is
// settled. p is the pairing as it stands, nil where there is none that waits
// on this card.
type cardAnswer struct {
	s       *Store
	bead    string
	seq     uint64
	p       *hostPairing
	actor   string
	outcome cardOutcome
}

// holdCard takes the lock of the pairing the ask seq on the bead id confirms,
// if it is a pairing card, and answers the hold; nil for any other ask, which
// takes no lock. A pairing whose time has run out is ended here, as by
// whoever looks first.
func (s *Store) holdCard(ctx context.Context, id string, seq uint64) *cardAnswer {
	if strings.Contains(id, "@") {
		return nil
	}
	var bead, pair string
	_ = s.read(func(tx *bolt.Tx) error {
		row, err := loadBead(tx, id)
		if err != nil {
			return nil
		}
		notes, err := beadNotes(tx, row.UUID)
		if err != nil {
			return nil
		}
		for _, n := range notes {
			if n.Seq != seq || n.Kind != NoteAsk || n.Author != "yard" {
				continue
			}
			if a, err := ParseAsk(n); err == nil {
				bead, pair = row.ID, a.Pair
			}
		}
		return nil
	})
	if pair == "" {
		return nil
	}
	s.pair.mu.Lock()
	c := &cardAnswer{s: s, bead: bead, seq: seq}
	if p := s.pairing(ctx); p != nil && p.ID == pair && p.State == PairCalled && p.card != nil {
		c.p = p
	}
	return c
}

// cardAnswerer refuses an answer to a card from anyone but a person at their
// own terminal or screen: an agent's pane, whatever name it gives, and a call
// that came through a peer's reach. The yard saves another yard as a peer on
// the strength of it, and what an agent or another yard may do to that is
// what the digits are there to keep out; an agent that could type them could
// copy them.
func cardAnswerer(ctx context.Context, tx *bolt.Tx, actor string) error {
	const why = "a pairing card is answered by the operator it is put to, at their own terminal or screen: it saves another yard as a peer"
	pane, err := agentPane(ctx, tx)
	if err != nil {
		return err
	}
	if pane != "" {
		return fmt.Errorf("%s; this is %s", why, pane)
	}
	if reach := reachOf(ctx); reach != "" {
		return fmt.Errorf("%s; this answer arrived through the reach of %s, and a peer does not confirm a pairing", why, reach)
	}
	if tx.Bucket(bCrew).Get([]byte(actor)) != nil {
		return fmt.Errorf("%s; %s is a crew member", why, actor)
	}
	return nil
}

// admit judges an answer to the card inside the write that records it, after
// the ask's own checks (whose it is, and that it still waits): the answerer
// first, then, for words, that the pairing is still there and the digits are
// its check's. It answers the answer to record, which says that the digits
// were right and never what they were, and what the pairing is to do with
// it once the write has committed (settle).
func (c *cardAnswer) admit(ctx context.Context, w *wtx, ans Answer, actor string) (Answer, error) {
	c.actor = actor
	if err := cardAnswerer(ctx, w.Tx, actor); err != nil {
		return ans, err
	}
	switch {
	case ans.Decline:
		c.outcome = cardDeclined
		return ans, nil
	case ans.Later:
		return ans, nil
	case c.p == nil:
		return ans, fmt.Errorf("%w: a pairing lives in the yard's server memory, and lasts %s from when it opened, whichever is first. Open another with yardr peer pair --card --name <the other yard>", errCardGone, minutes(CardTTL))
	}
	typed, ok := cardDigits(ans.Words)
	if !ok {
		return ans, errors.New("this card asks for three digits: the last three of the check, which the other operator reads out to you. The whole check, or the three that stand on the card, is not what it asks for: copying your own screen would pass")
	}
	_, want := CheckHalves(c.p.Check)
	if subtle.ConstantTimeCompare([]byte(typed), []byte(want)) != 1 {
		return ans, wrongDigits{}
	}
	c.outcome = cardConfirmed
	ans.Words = "typed the last three digits of the check, and they were the other operator's"
	return ans, nil
}

// settle carries out what the answer did, now that its write is done and the
// ask's lock is released, and releases the pairing's: wrong digits are
// counted, and the fifth ends the pairing; right ones confirm it for this
// yard; a decline ends it. A card whose pairing is gone is cleared. It
// answers the error the answer is refused with, in words that say what is left.
func (c *cardAnswer) settle(ctx context.Context, err error) error {
	if c == nil {
		return err
	}
	defer c.s.pair.mu.Unlock()
	var wrong wrongDigits
	switch {
	case errors.As(err, &wrong):
		c.p.card.tries++
		if left := MaxCheckTries - c.p.card.tries; left > 0 {
			return fmt.Errorf("%w: %d tries are left before the pairing ends", wrong, left)
		}
		c.s.endPairing(ctx, c.p, WrongCheck, "", c.actor)
		return fmt.Errorf("%w: %d wrong tries ended the pairing, and nothing was saved on either yard. Open another with %s", wrong, MaxCheckTries, c.p.reopenWords())
	case errors.Is(err, errCardGone):
		c.s.retireCard(c.bead, c.seq, "the pairing is gone: it ended with the server that held it, or lapsed. Open another with yardr peer pair --card --name <the other yard>")
	case err != nil:
	case c.outcome == cardConfirmed:
		c.s.confirmHost(ctx, c.p, c.actor)
	case c.outcome == cardDeclined && c.p != nil:
		c.s.endPairing(ctx, c.p, Declined, "", c.actor)
	case c.outcome == cardDeclined:
		c.s.retireCard(c.bead, c.seq, "declined: nothing was saved on either yard")
	}
	return err
}
