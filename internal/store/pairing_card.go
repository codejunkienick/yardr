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

// cardTo judges the card gate again for the write that files a card's
// pairing, and answers who opens it, who the card is put to, the word it
// names and the delegation it was opened on, if one. The host's opening
// and the joiner's call share it: both are held to what the write sees.
func (s *Store) cardTo(ctx context.Context, w *wtx, actor string) (who, to, word string, d *DelegatedCall, err error) {
	if err := w.may(ctx, actor, ActPairCard, Object{Kind: KindPeer}); err != nil {
		return "", "", "", nil, err
	}
	if who, err = paneAuthor(w.Tx, callerOf(ctx), actor); err != nil {
		return "", "", "", nil, err
	}
	ops, err := operatorsIn(w.Tx)
	if err != nil {
		return "", "", "", nil, err
	}
	if d, err = delegatedFor(ctx, w.Tx, actor, true); err != nil {
		return "", "", "", nil, err
	}
	to, word = who, "opened by "+who
	if d != nil {
		to, word = d.For, fmt.Sprintf("opened by %s on %s's word, their mail %d", who, d.For, d.Mail)
	}
	if !ops.is(to) {
		// The operator confirms: the one who opened it, or the one whose word
		// opened it, when they are one; otherwise the yard's first.
		if ops.none() {
			return "", "", "", nil, fmt.Errorf("a card is put to the operator, and this yard has none: yardr settings set operator <name>")
		}
		to = ops[0]
	}
	return who, to, word, d, nil
}

// cardOpened logs the opening of a card pairing, and where the yardmaster
// opened it on a user's word, records that as a delegation. It fills in who
// the card is put to and the word it names.
func (s *Store) cardOpened(ctx context.Context, p *hostPairing, url, actor string) error {
	return s.write(func(w *wtx) error {
		who, to, word, d, err := s.cardTo(ctx, w, actor)
		if err != nil {
			return err
		}
		data := p.event(map[string]any{"expires": p.Expires, "url": url})
		if d != nil {
			data["for"], data["mail"] = d.For, d.Mail
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

// filePairCard files a pairing card: a question bead to carry it (the ask
// --new precedent: nothing routes it) and the ask on it, authored by the
// yard, in one write. The operator is told as of any ask put to them. It
// answers where the card is.
func (s *Store) filePairCard(ctx context.Context, ask Ask, body string) (string, uint64, error) {
	create, err := s.newBead(ctx, NewBead{Title: ask.Question, Body: body, Type: TypeQuestion, Labels: []string{"topic:pairing"}}, "yard")
	if err != nil {
		return "", 0, err
	}
	var bead string
	var seq uint64
	err = s.write(func(w *wtx) error {
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
		bead, seq = row.ID, n.Seq
		return nil
	})
	return bead, seq, err
}

// fileCard files the card of p, which the other yard has just called. It
// fills in where the card is.
func (s *Store) fileCard(ctx context.Context, p *hostPairing) error {
	ask := p.cardAsk()
	body := fmt.Sprintf("The card of a pairing: the yard that calls itself %s asked to be this yard's peer, and this yard expected %s. Pairing %s. "+
		"Nothing routes this bead: it carries the question, and the yard closes it when the pairing ends.", p.Calls, p.Peer, p.card.word)
	bead, seq, err := s.filePairCard(ctx, ask, body)
	if err != nil {
		return err
	}
	p.card.bead, p.card.ask = bead, seq
	return nil
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

// The joiner's side of a card pairing (docs/peers.md, "Pairing from
// footplate"): this yard called the host with its code, from no terminal,
// and holds the call for a card. The command that called registers the
// transcript here and returns; its exit closes nothing, and the call lapses
// an hour after it was made (CardTTL), like a hosted card pairing.
//
// The card is the host card's mirror image: it shows the last three digits
// of the check and asks for the first three, which the other operator reads
// out. Each operator reads out the three on their own card and types the
// three the other reads: typed and shown stay disjoint on both sides. On a
// verified answer the store sends yes to the host, waits for the outcome
// and saves the peer as a terminal's pairing saves it; on a decline, a
// lapse or the fifth wrong try it sends no and refuses, exactly as the
// terminal does, so the host sees peer_declined.

// PairAnswering is a call whose verified answer is being carried to the
// host: held while the store sends yes and waits for the outcome, so a
// close waits it out instead of ending under it.
const PairAnswering = "answering"

// pairCallPrefix starts the id of a call, so a card's answer knows its
// pairing's side from the id alone, after a restart took the tables.
const pairCallPrefix = "call-"

// PairCallOffer is what the yard that called registers its call with: where
// it called, the name the other yard is saved under, and everything that
// crossed, from which both sides compute the check. It holds no code.
type PairCallOffer struct {
	URL        string     `json:"url"`
	Peer       string     `json:"peer"`
	Transcript Transcript `json:"transcript"`
}

// PairCall is a registered call as the command that registered it sees it:
// what answered, where the card is, and when the call lapses. It never
// holds the check: the digits are the two operators'.
type PairCall struct {
	ID      string    `json:"id"`
	State   string    `json:"state"`
	Expires time.Time `json:"expires"`
	Peer    string    `json:"peer"`
	Calls   string    `json:"calls"`
	Bead    string    `json:"bead"`
	Ask     uint64    `json:"ask"`
}

// pairCall is a pairing this yard called another yard for, held for a card
// in server memory beside the pairing it hosts. The check it answers is
// computed from the transcript, which is forgotten when the call ends.
type pairCall struct {
	PairCall
	url        string
	transcript Transcript
	actor      string
	// to, word and tries are the card's: who it is put to, who called on
	// whose word, and the wrong digits typed.
	to, word string
	tries    int
}

// PairCallSender carries the joiner's word on the check to the host the
// call reached: yes, waiting for the outcome once the host's operator has
// answered too, or no, which ends the pairing there. It is registered by
// the process that owns the store (SetPairCallSender), which alone holds
// the yard's key and dials.
type PairCallSender func(ctx context.Context, url string, t Transcript, yes bool) (*PairingOutcome, error)

// SetPairCallSender tells the store how a card's answer reaches the yard
// its call reached.
func (s *Store) SetPairCallSender(f PairCallSender) { s.pairCallSender = f }

// PairCallRegister registers the call the yard just made with the host's
// code: it files the call's card at once and returns, and the command that
// called ends here. Who may register is who may open a card pairing
// (ActPairCard): the operator, or the yardmaster's line session on a
// user's fresh word. The call lapses an hour after it is made.
func (s *Store) PairCallRegister(ctx context.Context, o PairCallOffer, actor string) (*PairCall, error) {
	if err := s.mayRead(ctx, actor, ActPairCard, Object{Kind: KindPeer}); err != nil {
		return nil, err
	}
	url, err := CleanURL(o.URL)
	if err != nil {
		return nil, err
	}
	if err := checkName("peer", o.Peer); err != nil {
		return nil, err
	}
	if err := o.Transcript.Joiner.Valid(); err != nil {
		return nil, err
	}
	host := o.Transcript.Host
	if err := host.Valid(); err != nil {
		return nil, err
	}
	if host.URL == "" || !isHash(o.Transcript.Commit) {
		return nil, errors.New("pairing: that is no call the yard made")
	}
	if host.Key == o.Transcript.Joiner.Key {
		return nil, errors.New("a yard is not paired with itself")
	}
	// Said now, not when the operator has typed the digits: a name taken
	// meanwhile fails the call before its card is filed.
	if err := s.CheckPeer(ctx, Peer{Name: o.Peer, Key: host.Key, Enc: host.Enc}); err != nil {
		return nil, err
	}
	s.pair.mu.Lock()
	// Lapsed calls are claimed under the lock and ended past it: the host
	// is told, and nothing of a call crosses the lock held.
	var swept []*pairCall
	defer func() {
		s.pair.mu.Unlock()
		for _, old := range swept {
			s.endJoinCall(ctx, old, TimedOut, "yard")
		}
	}()
	swept = s.sweepCallsLocked()
	jc := &pairCall{
		PairCall:   PairCall{ID: pairCallPrefix + newPeerMessageID(), State: PairCalled, Expires: now().Add(CardTTL), Peer: o.Peer, Calls: host.Name},
		url:        url,
		transcript: o.Transcript,
	}
	if err := s.callOpened(ctx, jc, url, actor); err != nil {
		return nil, err
	}
	if err := s.fileCallCard(ctx, jc); err != nil {
		return nil, err
	}
	if s.pair.calls == nil {
		s.pair.calls = map[string]*pairCall{}
	}
	s.pair.calls[jc.ID] = jc
	out := jc.PairCall
	return &out, nil
}

// callOpened logs the registration of a call, and where the yardmaster
// called on a user's word, records that as a delegation. The word is spent
// as a hosted opening spends it: the event is a peer_pair_opened, so a word
// that opened a pairing opens no call either. It fills in who the card is
// put to and the word it names.
func (s *Store) callOpened(ctx context.Context, jc *pairCall, url, actor string) error {
	return s.write(func(w *wtx) error {
		who, to, word, d, err := s.cardTo(ctx, w, actor)
		if err != nil {
			return err
		}
		data := map[string]any{"pairing": jc.ID, "role": "joiner", "card": true,
			"peer": jc.Peer, "calls": jc.Calls, "key": jc.transcript.Host.Key,
			"url": url, "expires": jc.Expires}
		if d != nil {
			data["for"], data["mail"] = d.For, d.Mail
		}
		jc.to, jc.word = to, word
		if err := w.event("", "peer_pair_opened", who, data); err != nil || d == nil {
			return err
		}
		return w.recordDelegated(d, "a pairing for a card", DelegatedBody{
			Changes: []DelegatedChange{{Key: "pairing", Is: jc.ID, Has: true}, {Key: "peer", Is: jc.Peer, Has: true}},
			Back:    "the operator declines its card",
		})
	})
}

// PairCallClose ends the call id names: the host is told no, so its
// operator is not left to wait, and nothing is saved. "" is the call the
// yard holds, where it holds one. It is the operator's, as ending a hosted
// pairing is; a call whose verified answer is being carried waits that
// out instead.
func (s *Store) PairCallClose(ctx context.Context, id, actor string) error {
	if err := s.mayRead(ctx, actor, ActPair, Object{Kind: KindPeer}); err != nil {
		return err
	}
	s.pair.mu.Lock()
	jc := s.pair.calls[id]
	if id == "" {
		jc = nil
		for _, c := range s.pair.calls {
			if jc != nil {
				s.pair.mu.Unlock()
				return errors.New("more than one call is held: a call id names which")
			}
			jc = c
		}
	}
	if jc == nil {
		s.pair.mu.Unlock()
		return fmt.Errorf("that call: %w (a call lives in the server's memory, and ends with it)", ErrNotFound)
	}
	if jc.State == PairAnswering {
		s.pair.mu.Unlock()
		return errors.New("an answer to this call's card is being carried to the other yard: the call ends with the pairing")
	}
	delete(s.pair.calls, jc.ID)
	s.pair.mu.Unlock()
	s.endJoinCall(ctx, jc, Closed, actor)
	return nil
}

// sweepCallsLocked drops the calls whose hour has run out, claimed for the
// caller to end outside the lock. A call whose answer is being carried is
// the answer's to end, not the sweep's.
func (s *Store) sweepCallsLocked() []*pairCall {
	var out []*pairCall
	for id, jc := range s.pair.calls {
		if jc.State == PairCalled && !now().Before(jc.Expires) {
			delete(s.pair.calls, id)
			out = append(out, jc)
		}
	}
	return out
}

// endJoinCall ends the call without a link, for reason: the host is told
// no, this yard logs why, and the card is taken back. It runs outside the
// pairing lock, on a call claimed under it.
func (s *Store) endJoinCall(ctx context.Context, jc *pairCall, reason, actor string) {
	if send := s.pairCallSender; send != nil {
		told, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_, _ = send(told, jc.url, jc.transcript, false)
	}
	_ = s.PairRefused(ctx, reason, jc.Calls, actor)
	s.retireCard(jc.Bead, jc.Ask, joinEndWords(jc, reason))
}

// joinEndWords says why a call ended without a link, in words that name
// what to do next: another code from the other operator.
func joinEndWords(jc *pairCall, reason string) string {
	again := fmt.Sprintf("ask the other yard's operator for a new code and call again (yardr peer pair %s <code> --card)", jc.url)
	switch reason {
	case TimedOut:
		return fmt.Sprintf("the call lapsed at %s, %s after it was made: nothing was saved on either yard. %s", jc.Expires.Local().Format("15:04"), minutes(CardTTL), again)
	case WrongCheck:
		return fmt.Sprintf("%d wrong tries at the digits ended the pairing: nothing was saved on either yard. %s", MaxCheckTries, again)
	case Declined:
		return "declined: nothing was saved on either yard"
	case Closed:
		return "the pairing was ended: nothing was saved on either yard"
	}
	return fmt.Sprintf("the pairing ended (%s): nothing was saved on either yard", reason)
}

// callAsk is the card for a call this yard made: a words ask to the
// operator that says who answered, what this yard saves it as, who called
// on whose word, and what it takes. The host card's mirror image: this one
// shows the last three digits of the check and asks for the first three,
// which the other operator reads out.
func (jc *pairCall) callAsk() Ask {
	_, shown := CheckHalves(jc.transcript.Check())
	return Ask{
		Question: fmt.Sprintf("Pair this yard with %s? Type the first three digits of the check its operator reads out", jc.Calls),
		To:       jc.to,
		Context: []string{
			fmt.Sprintf("The check is six digits. This card shows you the last three, %s, and asks for the first three, which the other operator reads out to you. Read them the last three as well.", shown),
			fmt.Sprintf("A yard that calls itself %s answered with the code, and its key is %s. This yard saves it as %s.", jc.Calls, jc.transcript.Host.Key, jc.Peer),
			fmt.Sprintf("Pairing %s, good until %s.", jc.word, jc.Expires.Local().Format("15:04")),
			"Decline a pairing you did not ask for, or one whose digits you cannot compare: nothing is saved on either yard until the digits are right and the other operator has confirmed too.",
			fmt.Sprintf("Five wrong tries end it. If it lapses, or the server restarts, it is gone: ask the other yard's operator for a new code and call again (yardr peer pair %s <code> --card).", jc.url),
		},
		Pair: jc.ID,
	}
}

// fileCallCard files the card of jc, whose call just reached the host. It
// fills in where the card is.
func (s *Store) fileCallCard(ctx context.Context, jc *pairCall) error {
	ask := jc.callAsk()
	body := fmt.Sprintf("The card of a pairing: this yard called the yard that calls itself %s with its code, to save it as %s. Pairing %s. "+
		"Nothing routes this bead: it carries the question, and the yard closes it when the pairing ends.", jc.Calls, jc.Peer, jc.word)
	bead, seq, err := s.filePairCard(ctx, ask, body)
	if err != nil {
		return err
	}
	jc.Bead, jc.Ask = bead, seq
	return nil
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
// the write runs again. The host's card asks for the last three, the
// joiner's for the first three.
type wrongDigits struct{ first bool }

func (w wrongDigits) Error() string {
	if w.first {
		return "those are not the first three digits of the check"
	}
	return "those are not the last three digits of the check"
}

// errCardGone is the refusal of an answer to a card whose pairing is no
// more: it ended with the server that held it, or past its hour.
var errCardGone = errors.New("that pairing is gone")

// cardAnswer is the hold an answer to a pairing card keeps on the pairing it
// confirms. The pairing's lock is taken before the answer's write, which is
// the order its lock is always taken in (pairs), and held until the answer is
// settled. p is the pairing as it stands, nil where there is none that waits
// on this card; jc the call, for a joiner's card. lapsed is a call whose
// hour ran out, claimed for settle to end outside the lock.
type cardAnswer struct {
	s       *Store
	bead    string
	seq     uint64
	p       *hostPairing
	jc      *pairCall
	lapsed  *pairCall
	join    bool
	actor   string
	outcome cardOutcome
}

// holdCard takes the lock of the pairing the ask seq on the bead id confirms,
// if it is a pairing card, and answers the hold; nil for any other ask, which
// takes no lock. A pairing whose time has run out is ended here, as by
// whoever looks first; a call whose hour has run out is claimed here and
// ended in settle, past the lock, since the host is told.
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
	c := &cardAnswer{s: s, bead: bead, seq: seq, join: strings.HasPrefix(pair, pairCallPrefix)}
	if p := s.pairing(ctx); p != nil && p.ID == pair && p.State == PairCalled && p.card != nil {
		c.p = p
		return c
	}
	if jc := s.pair.calls[pair]; jc != nil {
		if jc.State == PairCalled && !now().Before(jc.Expires) {
			delete(s.pair.calls, pair)
			c.lapsed = jc
		} else {
			c.jc = jc
		}
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
	case c.p == nil && c.jc == nil:
		if c.join {
			return ans, c.gone()
		}
		return ans, fmt.Errorf("%w: a pairing lives in the yard's server memory, and lasts %s from when it opened, whichever is first. Open another with yardr peer pair --card --name <the other yard>", errCardGone, minutes(CardTTL))
	}
	typed, ok := cardDigits(ans.Words)
	if !ok {
		if c.join {
			return ans, errors.New("this card asks for three digits: the first three of the check, which the other operator reads out to you. The whole check, or the three that stand on the card, is not what it asks for: copying your own screen would pass")
		}
		return ans, errors.New("this card asks for three digits: the last three of the check, which the other operator reads out to you. The whole check, or the three that stand on the card, is not what it asks for: copying your own screen would pass")
	}
	var want string
	if c.join {
		if c.s.pairCallSender == nil {
			return ans, errors.New("the yard's server carries a card's answer to the other yard, and this store has none: start it, and answer again")
		}
		want, _ = CheckHalves(c.jc.transcript.Check())
	} else {
		_, want = CheckHalves(c.p.Check)
	}
	if subtle.ConstantTimeCompare([]byte(typed), []byte(want)) != 1 {
		return ans, wrongDigits{first: c.join}
	}
	c.outcome = cardConfirmed
	ans.Words = "typed the last three digits of the check, and they were the other operator's"
	if c.join {
		ans.Words = "typed the first three digits of the check, and they were the other operator's"
	}
	return ans, nil
}

// gone refuses an answer to a joiner's card whose call is no more: it ended
// with the server that held it, or past its hour.
func (c *cardAnswer) gone() error {
	if c.lapsed != nil {
		return fmt.Errorf("the call lapsed at %s, %s after it was made: ask the other yard's operator for a new code and call again", c.lapsed.Expires.Local().Format("15:04"), minutes(CardTTL))
	}
	return fmt.Errorf("%w: a call lives in the yard's server memory, and lasts %s from when it was made, whichever is first. Ask the other yard's operator for a new code and call again", errCardGone, minutes(CardTTL))
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
	if c.join {
		// A joiner's answer is told to the host outside the lock, but its
		// local transition is claimed under the lock first.
		return c.settleJoin(ctx, err)
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

// settleJoin carries out what the answer to a joiner's card did. It is called
// with the pairing lock held from holdCard: wrong digits are counted, and the
// fifth ends the call; a decline ends it; right ones claim the call before
// being carried to the host outside the lock. A call whose hour ran out is
// ended lapsed, whatever the answer was.
func (c *cardAnswer) settleJoin(ctx context.Context, err error) error {
	s := c.s
	if c.lapsed != nil {
		s.pair.mu.Unlock()
		s.endJoinCall(ctx, c.lapsed, TimedOut, c.actor)
		if err == nil && c.outcome == cardDeclined {
			// The decline stands as the operator's word; the call had lapsed.
			return nil
		}
		return err
	}
	var wrong wrongDigits
	switch {
	case errors.As(err, &wrong):
		if c.jc == nil {
			s.pair.mu.Unlock()
			return err
		}
		c.jc.tries++
		fifth := c.jc.tries >= MaxCheckTries
		left := MaxCheckTries - c.jc.tries
		if fifth {
			delete(s.pair.calls, c.jc.ID)
		}
		s.pair.mu.Unlock()
		if !fifth {
			return fmt.Errorf("%w: %d tries are left before the pairing ends", wrong, left)
		}
		s.endJoinCall(ctx, c.jc, WrongCheck, c.actor)
		return fmt.Errorf("%w: %d wrong tries ended the pairing, and nothing was saved on either yard. Ask the other operator for a new code and call again", wrong, MaxCheckTries)
	case errors.Is(err, errCardGone):
		s.pair.mu.Unlock()
		s.retireCard(c.bead, c.seq, "the call is gone: it ended with the server that held it, or lapsed. Ask the other yard's operator for a new code and call again")
		return err
	case err != nil:
		s.pair.mu.Unlock()
		return err
	case c.outcome == cardConfirmed:
		if c.jc == nil {
			s.pair.mu.Unlock()
			return errors.New("the pairing was ended: nothing was saved on either yard")
		}
		jc, ok := s.pair.calls[c.jc.ID]
		if !ok || jc.State != PairCalled {
			s.pair.mu.Unlock()
			if ok {
				return errors.New("an answer to this card is being carried to the other yard: it ends with the pairing")
			}
			return errors.New("the pairing was ended: nothing was saved on either yard")
		}
		jc.State, jc.actor = PairAnswering, c.actor
		s.pair.mu.Unlock()
		return c.confirmJoin(ctx, jc)
	case c.outcome == cardDeclined:
		if c.jc == nil {
			s.pair.mu.Unlock()
			s.retireCard(c.bead, c.seq, "declined: nothing was saved on either yard")
			return nil
		}
		jc, live := s.pair.calls[c.jc.ID]
		if live && jc.State == PairCalled {
			delete(s.pair.calls, jc.ID)
		} else {
			live = false
		}
		s.pair.mu.Unlock()
		if !live {
			s.retireCard(c.bead, c.seq, "declined: nothing was saved on either yard")
			return nil
		}
		s.endJoinCall(ctx, c.jc, Declined, c.actor)
		return nil
	}
	s.pair.mu.Unlock()
	return err
}

// confirmJoin carries a verified answer to the host: yes, then the wait for
// its word, then what the outcome saves. The call is already marked
// PairAnswering under the lock, so a close or a fifth wrong try cannot take
// it while the wait crosses the lock released.
func (c *cardAnswer) confirmJoin(ctx context.Context, jc *pairCall) error {
	s := c.s
	wait, cancel := context.WithDeadline(context.WithoutCancel(ctx), jc.Expires)
	defer cancel()
	out, err := s.pairCallSender(wait, jc.url, jc.transcript, true)
	expired := wait.Err() != nil

	s.pair.mu.Lock()
	if cur, ok := s.pair.calls[jc.ID]; !ok || cur.State != PairAnswering {
		// Ended under the wait, which only a close does, and a close
		// waits the answer out instead. The wait's answer is dropped.
		s.pair.mu.Unlock()
		return errors.New("the pairing was ended while the answer was carried: nothing was saved on this yard")
	}
	delete(s.pair.calls, jc.ID)
	s.pair.mu.Unlock()
	return s.finishJoin(ctx, jc, out, err, expired)
}

// finishJoin saves what a carried answer's outcome says: the peer on yes
// from both sides, as a terminal's pairing saves it, and nothing on
// anything else.
func (s *Store) finishJoin(ctx context.Context, jc *pairCall, out *PairingOutcome, err error, expired bool) error {
	switch {
	case err == nil && out != nil && out.State == Confirmed:
		// A confirmed response means the host saved this yard. Its signed
		// reply can arrive as the local deadline fires, and must still be
		// saved here.
	case expired:
		s.endJoinCall(ctx, jc, TimedOut, jc.actor)
		return fmt.Errorf("%s's operator did not confirm within %s of the call: nothing was saved on either yard", jc.Peer, minutes(CardTTL))
	case err != nil:
		// The other yard may have saved this one: its answer did not arrive.
		words := fmt.Sprintf("asking %s how the pairing stands failed: this yard saved nothing. If %s shows this yard as a peer, remove it there (yardr peer rm) and pair again", jc.Peer, jc.Peer)
		s.retireCard(jc.Bead, jc.Ask, words)
		return fmt.Errorf("asking %s how the pairing stands: %w\nThis yard saved nothing. If %s shows this yard as a peer, remove it there (yardr peer rm) and pair again", jc.Peer, err, jc.Peer)
	case out == nil:
		words := fmt.Sprintf("asking %s how the pairing stands returned no outcome: this yard saved nothing", jc.Peer)
		s.retireCard(jc.Bead, jc.Ask, words)
		return errors.New(words)
	default:
		return s.joinSaidNo(ctx, jc, out.Reason)
	}
	peer := Peer{Name: jc.Peer, Key: jc.transcript.Host.Key, Enc: jc.transcript.Host.Enc, URL: jc.url, Calls: jc.Calls}
	if _, err := s.pairPeer(peer, jc.actor, map[string]any{"role": "joiner", "pairing": jc.ID}); err != nil {
		words := fmt.Sprintf("%s has saved this yard, and this yard could not save it: %s. Remove this yard there (yardr peer rm) and pair again", jc.Peer, err)
		s.retireCard(jc.Bead, jc.Ask, words)
		return errors.New(words)
	}
	s.retireCard(jc.Bead, jc.Ask, "paired: "+jc.Peer+" is saved as a peer on both yards")
	return nil
}

// clipReason keeps the other yard's reason short and plain where it is
// shown: a terminal's pairing keeps it the same way (cli/clipText), so a
// reason no yardr sends cannot carry newlines or a faked pairing line onto
// the card.
func clipReason(s string) string {
	s = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r == '_' {
			return r
		}
		return -1
	}, s)
	if len(s) > 32 {
		s = s[:32]
	}
	return s
}

// joinSaidNo ends a call the host ended without a link, and logs it here.
// The reason is that yard's: its "declined" is its own operator's.
func (s *Store) joinSaidNo(ctx context.Context, jc *pairCall, reason string) error {
	mine, text := PeerDeclined, fmt.Sprintf("%s ended the pairing (%s)", jc.Peer, clipReason(reason))
	switch reason {
	case Declined:
		text = fmt.Sprintf("%s's operator declined", jc.Peer)
	case PeerDeclined:
		mine, text = Declined, "declined"
	case WrongCheck:
		mine, text = WrongCheck, fmt.Sprintf("%s's operator typed the wrong digits %d times, and the pairing ended: the check was not compared with yours, or something between the two yards changed it", jc.Peer, MaxCheckTries)
	case TimedOut:
		mine, text = TimedOut, "the two operators did not both confirm in time"
	case Closed:
		mine, text = Closed, fmt.Sprintf("%s's operator ended the pairing", jc.Peer)
	case AlreadyPeer:
		mine, text = AlreadyPeer, fmt.Sprintf("%s could not save this yard: its name or its key became a peer's there meanwhile", jc.Peer)
	}
	_ = s.PairRefused(ctx, mine, jc.Calls, jc.actor)
	s.retireCard(jc.Bead, jc.Ask, text+": nothing was saved on either yard")
	return errors.New(text + ": nothing was saved on either yard")
}
