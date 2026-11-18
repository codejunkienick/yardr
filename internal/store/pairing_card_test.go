package store_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/peer"
	"yardr.dev/yardr/internal/store"
)

// cardOffer is the offer a yard opens a pairing for a card with, expecting a
// yard it will save as laptop.
func cardOffer(t *testing.T) store.PairOffer {
	t.Helper()
	return store.PairOffer{Name: "home", Key: peerKey(t), URL: "http://10.0.0.5:8790", As: "laptop", Card: true}
}

// wordOf is ctx carrying a delegated opening for user on mail.
func wordOf(ctx context.Context, user string, mail uint64) context.Context {
	return store.WithDelegation(ctx, store.DelegatedCall{For: user, Mail: mail, Command: "yardr peer pair --card --name laptop"})
}

// yardmasterLine is the yardmaster's line session as the store knows it: a
// call from its pane, whose user is empty. Delegated card pairings must come
// from this session, not just a caller naming itself yardmaster.
func yardmasterLine(t *testing.T, y *delegationYard) context.Context {
	t.Helper()
	me := os.Getpid()
	ps, err := peer.Describe(me)
	if err != nil {
		t.Skipf("this process cannot be described: %v", err)
	}
	if err := y.own.CrewStarted(y.ctx, "yardmaster", map[string]any{"shell_pid": me, "shell_start": ps.Start}); err != nil {
		t.Fatal(err)
	}
	return store.WithCaller(y.ctx, me)
}

// cardRecord is the newest delegated record as jo reads it through the
// owner's store: after yardmasterLine registers this process as the
// yardmaster's pane, the socket reads every call as the yardmaster's, who
// may not read an operator's record.
func cardRecord(t *testing.T, y *delegationYard) *store.Delegation {
	t.Helper()
	for id := uint64(64); id > 0; id-- {
		if d, err := y.own.Delegation(y.ctx, id, "jo"); err == nil {
			return d
		}
	}
	t.Fatal("no delegated record")
	return nil
}

// The yardmaster opens a pairing for a card on the cited local mail of a
// user of the yard, the operator or another, and the opening is recorded
// and told as any delegated run is. The mail is a fresh word: one that
// opened a pairing before is not taken again, and neither is one that has
// gone stale. The operator needs no word.
func TestPairingCardIsOpenedOnAFreshWord(t *testing.T) {
	// Sequential: the clock is the package's, which a stale word moves.
	y := newDelegationYard(t)
	ctx := y.ctx
	// The later words first: yardmasterLine registers this process as the
	// yardmaster's pane, and over the socket every later call reads as the
	// yardmaster's, so mail sent after it is not its sender's.
	adaMail := y.mailFrom(t, "ada", "yardmaster")
	stale := y.mailFrom(t, "jo", "yardmaster")
	line := yardmasterLine(t, y)
	api := y.api

	// Without a word the yardmaster is refused in the words of any pairing's,
	// and a terminal's pairing is the operator's still.
	if _, err := api.PairOpen(ctx, cardOffer(t), "yardmaster"); err == nil || !strings.Contains(err.Error(), "only the operator (jo) may open a pairing for a card; this is yardmaster") {
		t.Fatalf("an uncited opening by the yardmaster = %v", err)
	}
	terminal := cardOffer(t)
	terminal.Card = false
	if _, err := api.PairOpen(wordOf(ctx, "jo", y.mail), terminal, "yardmaster"); err == nil || !strings.Contains(err.Error(), "only the operator (jo) may pair this yard with another") {
		t.Fatalf("a terminal's pairing opened by the yardmaster on a word = %v", err)
	}

	// On the operator's word: opened, for CardTTL, recorded and told.
	opened, err := api.PairOpen(wordOf(line, "jo", y.mail), cardOffer(t), "yardmaster")
	if err != nil {
		t.Fatalf("the yardmaster's opening on jo's mail: %v", err)
	}
	if opened.Code == "" || time.Until(opened.Expires) < store.CardTTL-time.Minute {
		t.Fatalf("the pairing = %+v", opened)
	}
	rec := cardRecord(t, y)
	if rec.For != "jo" || rec.Yardmaster != "yardmaster" || rec.Mail != y.mail || rec.Of != "a pairing for a card" || rec.Command != "yardr peer pair --card --name laptop" {
		t.Errorf("record = %+v", rec)
	}
	evs, err := api.Events(ctx, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	var told bool
	for _, e := range evs {
		if e.Kind == "peer_pair_opened" && (e.Data["for"] != "jo" || e.Data["mail"] != float64(y.mail) || e.Data["card"] != true || e.Actor != "yardmaster") {
			t.Errorf("peer_pair_opened = %+v", e)
		}
		if e.Kind == "mail" && e.Actor == "yard" && store.MailReceiver(e) == "jo" {
			told = strings.Contains(e.Data["text"].(string), "a pairing for a card")
		}
		for k, v := range e.Data {
			if s, _ := v.(string); strings.Contains(s, opened.Code) || strings.Contains(s, strings.ReplaceAll(opened.Code, "-", "")) {
				t.Errorf("the code is in event %d (%s): %s", e.Seq, k, s)
			}
		}
	}
	if !told {
		t.Error("jo was not told what ran")
	}

	// The same word does not open another, after the first ended: a try
	// again asks the person for another.
	if err := y.own.PairClose(ctx, opened.ID, "jo"); err != nil {
		t.Fatal(err)
	}
	if _, err := api.PairOpen(wordOf(line, "jo", y.mail), cardOffer(t), "yardmaster"); err == nil || !strings.Contains(err.Error(), "opened a pairing already") {
		t.Fatalf("the same mail again = %v", err)
	}
	// A new word for the same yard is refused naming the cooldown: the
	// pairing that ended cools its name down.
	if _, err := api.PairOpen(wordOf(line, "ada", adaMail), cardOffer(t), "yardmaster"); err == nil || !strings.Contains(err.Error(), "cooldown") {
		t.Fatalf("a new word inside the cooldown = %v", err)
	}
	evs, err = api.Events(ctx, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	var cooled *store.Event
	for i, e := range evs {
		if e.Kind == "peer_pair_cooldown" {
			cooled = &evs[i]
		}
	}
	if cooled == nil || cooled.Data["role"] != "host" || cooled.Data["peer"] != "laptop" || cooled.Actor != "yardmaster" {
		t.Fatalf("the cooldown's event = %+v", cooled)
	}
	// A new word from a user who is not the operator opens one, for
	// another yard: the card will still be put to the operator, whose
	// digits confirm it.
	secondOffer := cardOffer(t)
	secondOffer.As = "laptop2"
	second, err := api.PairOpen(wordOf(line, "ada", adaMail), secondOffer, "yardmaster")
	if err != nil {
		t.Fatalf("the yardmaster's opening on ada's mail: %v", err)
	}
	if err := y.own.PairClose(ctx, second.ID, "jo"); err != nil {
		t.Fatal(err)
	}

	// The operator opens without one, through the owner's store: over the
	// socket the call would read as the yardmaster's pane's.
	thirdOffer := cardOffer(t)
	thirdOffer.As = "laptop3"
	third, err := y.own.PairOpen(ctx, thirdOffer, "jo")
	if err != nil {
		t.Fatalf("the operator's opening: %v", err)
	}
	if err := y.own.PairClose(ctx, third.ID, "jo"); err != nil {
		t.Fatal(err)
	}

	// A word that has gone stale is not a word: an hour is the longest.
	t.Cleanup(func() { store.SetClockForTest(time.Now) })
	store.SetClockForTest(func() time.Time { return time.Now().Add(store.CardTTL + time.Minute) })
	if _, err := api.PairOpen(wordOf(line, "jo", stale), cardOffer(t), "yardmaster"); err == nil || !strings.Contains(err.Error(), "fresh word") || !strings.Contains(err.Error(), "old") {
		t.Fatalf("a stale mail = %v", err)
	}
	store.SetClockForTest(func() time.Time { return time.Now().Add(store.CardTTL - time.Minute) })
	if p, err := api.PairOpen(wordOf(line, "jo", stale), cardOffer(t), "yardmaster"); err != nil {
		t.Fatalf("a mail a minute short of the hour = %v", err)
	} else if err := y.own.PairClose(ctx, p.ID, "jo"); err != nil {
		t.Fatal(err)
	}
}

// What the leg is not for: a mail that is no local mail of a user to the
// yardmaster (another yard's, a bead agent's, a timer's, one written as
// someone's), an event that is no mail (a thread's or an issue's), mail to
// another, a user who is none, another crew member's call, a call through a
// peer's reach, and a yard that has turned delegation off.
func TestPairingCardIsNotOpenedOnWhatIsNoWord(t *testing.T) {
	t.Parallel()
	y := newDelegationYard(t)
	ctx := y.ctx
	// The mails first: yardmasterLine registers this process as the
	// yardmaster's pane, and over the socket every later call reads as the
	// yardmaster's, so mail sent after it is not its sender's.
	toClerk := y.mailFrom(t, "jo", "clerk")
	adas := y.mailFrom(t, "ada", "yardmaster")
	jos := y.mailFrom(t, "jo", "yardmaster")
	line := yardmasterLine(t, y)
	try := func(name string, ctx context.Context, actor string, want string) {
		t.Helper()
		// The owner's store: a reach is a mark the door puts on the call's
		// context, which the socket does not carry.
		_, err := y.own.PairOpen(ctx, cardOffer(t), actor)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s = %v, want it refused with %q", name, err, want)
		}
	}
	for _, mark := range []string{"from_peer", "from_peer_unlinked", "as", "from_bead", "timer"} {
		seq := y.logEvent(t, "mail", map[string]any{"to": "yardmaster", "text": "pair us", mark: "x"})
		try("mail marked "+mark, wordOf(line, "jo", seq), "yardmaster", "no local mail")
	}
	hook := y.logEvent(t, "hook", map[string]any{"to": "yardmaster", "text": "pair us", "thread": "x"})
	try("a thread's or an issue's event", wordOf(line, "jo", hook), "yardmaster", "not mail")
	try("mail to another crew member", wordOf(line, "jo", toClerk), "yardmaster", "not mail to the yardmaster")
	try("a user who is none", wordOf(line, "zed", y.mail), "yardmaster", "is none")
	try("another crew member's", wordOf(ctx, "jo", y.mail), "clerk", "the yardmaster's (yardmaster) alone")
	try("a call through a peer's reach", store.WithReach(wordOf(line, "jo", y.mail), "north"), "jo", "reach of north")
	try("a mail of ada's cited as jo's", wordOf(line, "jo", adas), "yardmaster", "from ada, not from jo")
	// Through the owner's store: over the socket the call would read as
	// the yardmaster's pane's, and not as jo's.
	if err := y.own.SetSetting(ctx, store.DelegationSetting, "false", "jo"); err != nil {
		t.Fatal(err)
	}
	try("a yard with delegation off", wordOf(line, "jo", jos), "yardmaster", "only the operator (jo) may open a pairing for a card")
}

// The yardmaster opens from its line session and from no other: a
// conversation session of it, which is one person's talk with it, is
// refused whatever mail it cites, and the line's is taken.
func TestPairingCardIsOpenedFromTheLineSession(t *testing.T) {
	t.Parallel()
	me := os.Getpid()
	ps, err := peer.Describe(me)
	if err != nil {
		t.Skipf("this process cannot be described: %v", err)
	}
	handle := map[string]any{"shell_pid": me, "shell_start": ps.Start}

	conversation := newDelegationYard(t)
	serveYardmaster(t, conversation.api, conversation.ctx, "", map[string]string{"ada": "/tmp/ada.env"})
	// The mail first: from the pane's own process, the socket would take it
	// for the yardmaster's.
	adaMail := conversation.mailFrom(t, "ada", "yardmaster")
	if _, err := conversation.own.PairOpen(wordOf(conversation.ctx, "ada", adaMail), cardOffer(t), "yardmaster"); err == nil || !strings.Contains(err.Error(), "no crew pane") {
		t.Fatalf("an opening outside the yardmaster's pane = %v", err)
	}
	if err := conversation.own.CrewStartedFor(conversation.ctx, "yardmaster", "ada", handle); err != nil {
		t.Fatal(err)
	}
	pane := store.WithCaller(conversation.ctx, me)
	_, err = conversation.own.PairOpen(wordOf(pane, "ada", adaMail), cardOffer(t), "yardmaster")
	if err == nil || !strings.Contains(err.Error(), "line session's") || !strings.Contains(err.Error(), "conversation with ada") {
		t.Fatalf("an opening from the yardmaster's conversation with ada = %v", err)
	}

	line := newDelegationYard(t)
	if err := line.own.CrewStarted(line.ctx, "yardmaster", handle); err != nil {
		t.Fatal(err)
	}
	pane = store.WithCaller(line.ctx, me)
	// From the pane the actor is the pane's, whatever name the call gives.
	opened, err := line.own.PairOpen(wordOf(pane, "jo", line.mail), cardOffer(t), "jo")
	if err != nil {
		t.Fatalf("an opening from the yardmaster's line session = %v", err)
	}
	if err := line.own.PairClose(line.ctx, opened.ID, "jo"); err != nil {
		t.Fatal(err)
	}
	// The line session's call without a word is the yardmaster's as any.
	if _, err := line.own.PairOpen(pane, cardOffer(t), "jo"); err == nil || !strings.Contains(err.Error(), "may open a pairing for a card; this is yardmaster") {
		t.Fatalf("an uncited opening from the line session = %v", err)
	}
}

// A bead's agent does not answer the card either, whatever name it gives:
// the digits are the operator's, and an agent that could type them could
// copy them. Its refused answers are not counted against the pairing, and the
// operator's own, from outside its pane, is taken after them.
func TestPairingCardIsNotAnsweredFromABeadsPane(t *testing.T) {
	// Sequential: Stretch is the package's, shortened for the pairing's keys.
	stretch := store.Stretch
	store.Stretch = 16
	t.Cleanup(func() { store.Stretch = stretch })
	s, b := paneYard(t)
	ctx := context.Background()
	offer := cardOffer(t)
	opened, err := s.PairOpen(ctx, offer, "jo")
	if err != nil {
		t.Fatal(err)
	}
	code, _ := store.CleanCode(opened.Code)
	me := store.Party{Nonce: store.NewNonce(), Name: "laptop", Key: peerKey(t)}
	tr := store.Transcript{Commit: store.Commit(me), Joiner: me}
	key := store.PairingKey(code, tr.Commit)
	hello, err := s.PairHello(ctx, store.NewHello(key, me))
	if err != nil {
		t.Fatal(err)
	}
	tr.Host = hello.Party
	if _, err := s.PairReveal(ctx, store.NewReveal(key, tr)); err != nil {
		t.Fatal(err)
	}
	q, err := s.UserAsks(ctx, "jo", "jo", "jo")
	if err != nil || len(q.Open) != 1 {
		t.Fatalf("jo's open asks = %+v, %v: want the card", q, err)
	}
	card := q.Open[0]
	_, last := store.CheckHalves(tr.Check())
	wrong := "000"
	if last == wrong {
		wrong = "111"
	}

	sess, err := s.Claim(ctx, b.ID, "builders", "new", "yard")
	if err != nil {
		t.Fatal(err)
	}
	me2 := os.Getpid()
	handle := map[string]any{"pid": me2}
	if ps, err := peer.Describe(me2); err == nil {
		handle["start"] = ps.Start
	}
	if err := s.MarkRunning(ctx, sess.ID, handle); err != nil {
		t.Fatal(err)
	}
	pane := store.WithCaller(ctx, me2)
	for _, actor := range []string{"jo", sess.ID} {
		for _, words := range []string{last, wrong} {
			if err := s.AnswerAsk(pane, card.Bead, card.Ask, 0, words, actor, false); err == nil || strings.Contains(err.Error(), "digits") {
				t.Errorf("%q from a bead's pane as %s = %v: want the pane refused, not the digits judged", words, actor, err)
			}
		}
	}
	if p, err := s.GetPairing(ctx, opened.ID); err != nil || p.State != store.PairCalled || p.Confirmed {
		t.Fatalf("the pairing after the agent's answers = %+v, %v", p, err)
	}
	// Not one try was spent: five wrong ones from the operator are still
	// needed to end it, and the right ones before them are taken.
	if err := s.AnswerAsk(ctx, card.Bead, card.Ask, 0, wrong, "jo", false); err == nil || !strings.Contains(err.Error(), "4 tries are left") {
		t.Fatalf("the first wrong try after the agent's answers = %v: want four left", err)
	}
	if err := s.AnswerAsk(ctx, card.Bead, card.Ask, 0, last, "jo", false); err != nil {
		t.Fatalf("the operator's digits = %v", err)
	}
}

// callOffer is the offer a yard registers a call with, to save the yard
// that answered as south.
func callOffer(t *testing.T) store.PairCallOffer {
	t.Helper()
	me := store.Party{Nonce: store.NewNonce(), Name: "laptop", Key: peerKey(t)}
	host := store.Party{Nonce: store.NewNonce(), Name: "south", Key: peerKey(t), URL: "http://10.0.0.5:8790"}
	return store.PairCallOffer{URL: "http://10.0.0.5:8790", Peer: "south", Transcript: store.Transcript{Commit: store.Commit(me), Joiner: me, Host: host}}
}

// callOf is ctx carrying a delegated call for user on mail, cited as the
// call typed it: the address it called, and no code.
func callOf(ctx context.Context, user string, mail uint64) context.Context {
	return store.WithDelegation(ctx, store.DelegatedCall{For: user, Mail: mail, Command: "yardr peer pair http://10.0.0.5:8790 <code> --card"})
}

// The yardmaster calls for a card on the cited local mail of a user of the
// yard, and the call is recorded and told as any delegated run is. The mail
// is a fresh word across both sides: one that opened a pairing opens no
// call either. The operator needs no word.
func TestPairCallIsRegisteredOnAFreshWord(t *testing.T) {
	// Sequential: the clock is the package's, which a stale word moves.
	y := newDelegationYard(t)
	ctx := y.ctx
	// The later words first: yardmasterLine registers this process as the
	// yardmaster's pane, and over the socket every later call reads as the
	// yardmaster's, so mail sent after it is not its sender's.
	adaMail := y.mailFrom(t, "ada", "yardmaster")
	stale := y.mailFrom(t, "jo", "yardmaster")
	line := yardmasterLine(t, y)
	api := y.api

	// Without a word the yardmaster is refused in the words of any pairing's.
	if _, err := api.PairCallRegister(ctx, callOffer(t), "yardmaster"); err == nil || !strings.Contains(err.Error(), "only the operator (jo) may open a pairing for a card; this is yardmaster") {
		t.Fatalf("an uncited call by the yardmaster = %v", err)
	}

	// On the operator's word: registered, for CardTTL, recorded and told.
	reg, err := api.PairCallRegister(callOf(line, "jo", y.mail), callOffer(t), "yardmaster")
	if err != nil {
		t.Fatalf("the yardmaster's call on jo's mail: %v", err)
	}
	if time.Until(reg.Expires) < store.CardTTL-time.Minute || reg.Bead == "" || reg.Ask == 0 {
		t.Fatalf("the call = %+v", reg)
	}
	rec := cardRecord(t, y)
	if rec.For != "jo" || rec.Yardmaster != "yardmaster" || rec.Mail != y.mail || rec.Of != "a pairing for a card" ||
		rec.Command != "yardr peer pair http://10.0.0.5:8790 <code> --card" {
		t.Errorf("record = %+v: want the call as cited, and no code", rec)
	}
	evs, err := api.Events(ctx, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	var told bool
	for _, e := range evs {
		if e.Kind == "peer_pair_opened" && (e.Data["role"] != "joiner" || e.Data["for"] != "jo" || e.Data["mail"] != float64(y.mail) || e.Data["card"] != true || e.Actor != "yardmaster") {
			t.Errorf("peer_pair_opened = %+v", e)
		}
		if e.Kind == "mail" && e.Actor == "yard" && store.MailReceiver(e) == "jo" {
			told = strings.Contains(e.Data["text"].(string), "a pairing for a card")
		}
	}
	if !told {
		t.Error("jo was not told what ran")
	}

	// The same word registers no second call, and opens no pairing either:
	// a word is spent across both sides.
	if err := y.own.PairCallClose(ctx, reg.ID, "jo"); err != nil {
		t.Fatal(err)
	}
	if _, err := api.PairCallRegister(callOf(line, "jo", y.mail), callOffer(t), "yardmaster"); err == nil || !strings.Contains(err.Error(), "opened a pairing already") {
		t.Fatalf("the same mail again = %v", err)
	}
	if _, err := api.PairOpen(wordOf(line, "jo", y.mail), cardOffer(t), "yardmaster"); err == nil || !strings.Contains(err.Error(), "opened a pairing already") {
		t.Fatalf("an opening on the mail a call spent = %v", err)
	}
	// A new word for the same yard is refused naming the cooldown: the
	// call that ended cools its name down.
	if _, err := api.PairCallRegister(callOf(line, "ada", adaMail), callOffer(t), "yardmaster"); err == nil || !strings.Contains(err.Error(), "cooldown") {
		t.Fatalf("a new word inside the cooldown = %v", err)
	}
	// A new word from a user who is not the operator registers one, for
	// another yard.
	secondOffer := callOffer(t)
	secondOffer.Peer = "south2"
	second, err := api.PairCallRegister(callOf(line, "ada", adaMail), secondOffer, "yardmaster")
	if err != nil {
		t.Fatalf("the yardmaster's call on ada's mail: %v", err)
	}
	if err := y.own.PairCallClose(ctx, second.ID, "jo"); err != nil {
		t.Fatal(err)
	}

	// The operator registers without one, through the owner's store: over
	// the socket the call would read as the yardmaster's pane's.
	thirdOffer := callOffer(t)
	thirdOffer.Peer = "south3"
	third, err := y.own.PairCallRegister(ctx, thirdOffer, "jo")
	if err != nil {
		t.Fatalf("the operator's call: %v", err)
	}
	if err := y.own.PairCallClose(ctx, third.ID, "jo"); err != nil {
		t.Fatal(err)
	}

	// A word that has gone stale is not a word: an hour is the longest.
	t.Cleanup(func() { store.SetClockForTest(time.Now) })
	store.SetClockForTest(func() time.Time { return time.Now().Add(store.CardTTL + time.Minute) })
	if _, err := api.PairCallRegister(callOf(line, "jo", stale), callOffer(t), "yardmaster"); err == nil || !strings.Contains(err.Error(), "fresh word") || !strings.Contains(err.Error(), "old") {
		t.Fatalf("a stale mail = %v", err)
	}
	store.SetClockForTest(func() time.Time { return time.Now().Add(store.CardTTL - time.Minute) })
	if reg, err := api.PairCallRegister(callOf(line, "jo", stale), callOffer(t), "yardmaster"); err != nil {
		t.Fatalf("a mail a minute short of the hour = %v", err)
	} else if err := y.own.PairCallClose(ctx, reg.ID, "jo"); err != nil {
		t.Fatal(err)
	}
}
