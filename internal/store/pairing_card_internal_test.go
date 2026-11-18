package store

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/peer"
)

// cardHost is a pairHost whose yard has an operator, jo, a member, ada, and
// the yardmaster, for pairings opened for a card. Its clock is the
// package's fake one (newPairHost), so these tests are sequential too.
type cardHost struct {
	*pairHost
	ctx context.Context
}

func newCardHost(t *testing.T) *cardHost {
	t.Helper()
	h := &cardHost{pairHost: newPairHost(t), ctx: context.Background()}
	for _, name := range []string{"jo", "ada"} {
		if _, err := h.AddUser(h.ctx, name, "jo"); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.SetSetting(h.ctx, OperatorSetting, "jo", "jo"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.PutCrew(h.ctx, CrewMember{Name: "yardmaster", Runner: "herdr"}, "jo"); err != nil {
		t.Fatal(err)
	}
	return h
}

// openCard opens a pairing for a card as the operator, expecting a yard
// saved as laptop.
func (h *cardHost) openCard(t *testing.T) (*PairingCode, string) {
	t.Helper()
	p, err := h.PairOpen(h.ctx, PairOffer{Name: "north", Key: h.key, URL: "http://10.0.0.5:8790", As: "laptop", Card: true}, "jo")
	if err != nil {
		t.Fatal(err)
	}
	code, _ := CleanCode(p.Code)
	return p, code
}

// card is the card the pairing's call filed: its bead, its seq and the ask.
func (h *cardHost) card(t *testing.T) (bead string, seq uint64, ask Ask) {
	t.Helper()
	q, err := h.UserAsks(h.ctx, "jo", "jo", "jo")
	if err != nil || len(q.Open) != 1 {
		t.Fatalf("jo's open asks = %+v, %v: want the card", q, err)
	}
	notes, err := h.Notes(h.ctx, q.Open[0].Bead)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range notes {
		if n.Seq == q.Open[0].Ask {
			ask, err = ParseAsk(n)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	return q.Open[0].Bead, q.Open[0].Ask, ask
}

// typed is the digits a card asks for, from the check the other operator
// reads out.
func typed(tr Transcript) string {
	_, last := CheckHalves(tr.Check())
	return last
}

// wrongDigits are three digits that are not the check's last three.
func wrongDigits3(tr Transcript) string {
	if typed(tr) == "000" {
		return "111"
	}
	return "000"
}

// A pairing opened for a card is good for an hour from the opening, the call
// files a card and its carrier, and the card shows the first three digits of
// the check and never the last three, the code or anything of the carrier's
// that a person typed. The digits the operator then types, once the other
// yard has said yes, save the link on both sides as a terminal's pairing
// does, and the answer says they were right and not what they were.
func TestPairingCardConfirms(t *testing.T) {
	// Sequential: the package's fake clock and Stretch (via newPairHost).
	h := newCardHost(t)
	opened, code := h.openCard(t)
	if got := opened.Expires.Sub(h.at); got != CardTTL || CardTTL != time.Hour {
		t.Fatalf("a card pairing is good for %s", got)
	}
	if _, err := h.PairOpen(h.ctx, PairOffer{Name: "north", Key: h.key, URL: "http://10.0.0.5:8790", Card: true}, "jo"); err == nil || !strings.Contains(err.Error(), "--name") {
		t.Fatalf("a card pairing that names no yard it expects = %v", err)
	}

	// The call comes late in the hour, and the hour is not shortened to the
	// confirmation's five minutes.
	h.at = h.at.Add(40 * time.Minute)
	tr, err := h.call(code, "laptop", newPublicKey(t))
	if err != nil {
		t.Fatal(err)
	}
	p, err := h.GetPairing(h.ctx, opened.ID)
	if err != nil || p.State != PairCalled || !p.Expires.Equal(opened.Expires) {
		t.Fatalf("after the call: %+v, %v", p, err)
	}
	if p.Check != "" {
		t.Fatalf("the pairing's read holds the check: %q", p.Check)
	}
	bead, seq, ask := h.card(t)
	shown, want := CheckHalves(tr.Check())
	text := strings.Join(append([]string{ask.Question}, ask.Context...), "\n")
	if ask.To != "jo" || ask.Pair != opened.ID || len(ask.Options) != 0 {
		t.Fatalf("the card = %+v", ask)
	}
	for _, line := range []string{shown, "laptop", tr.Joiner.Key, "opened by jo", "Decline a pairing you did not ask for", "yardr peer pair --card --name laptop"} {
		if !strings.Contains(text, line) {
			t.Errorf("the card does not say %q:\n%s", line, text)
		}
	}
	if strings.Contains(text, want) || strings.Contains(text, code) {
		t.Errorf("the card shows what it asks for or the code:\n%s", text)
	}
	if !StrongWeight(ask.Weight) {
		t.Errorf("the card's weight = %+v, want strong", ask.Weight)
	}
	b, err := h.GetBead(h.ctx, bead)
	if err != nil || b.Type != TypeQuestion || b.Depot != nil {
		t.Fatalf("the carrier = %+v, %v: want a question bead of no depot", b, err)
	}

	// The shape is refused and not counted: the whole check, the three on
	// the card and a word are no answer to it.
	for _, words := range []string{tr.Check(), shown + want, "abc", "12", ""} {
		if err := h.AnswerAsk(h.ctx, bead, seq, 0, words, "jo", false); err == nil {
			t.Fatalf("the card took %q", words)
		}
	}
	if h.pair.cur.card.tries != 0 {
		t.Fatalf("%d tries counted for answers of the wrong shape", h.pair.cur.card.tries)
	}

	// A terminal's yes confirms nothing of it, and its name is not changed
	// after the card named it: the digits are the confirmation.
	if _, err := h.PairAnswer(h.ctx, opened.ID, true, "jo"); err == nil || !strings.Contains(err.Error(), "confirmed on its card") {
		t.Fatalf("PairAnswer yes on a card pairing = %v", err)
	}
	if _, err := h.PairName(h.ctx, opened.ID, "other", "jo"); err == nil || !strings.Contains(err.Error(), "under the name it was opened with") {
		t.Fatalf("PairName on a card pairing = %v", err)
	}

	// The other yard says yes first; the host's digits then save the link.
	if out, err := h.PairConfirmed(h.ctx, tr.Session(), true); err != nil || out.State != Waiting {
		t.Fatalf("the caller's yes = %+v, %v", out, err)
	}
	if ps, _ := h.Peers(h.ctx); len(ps) != 0 {
		t.Fatalf("peers before the digits = %+v", ps)
	}
	if err := h.AnswerAsk(h.ctx, bead, seq, 0, want[:1]+" "+want[1:], "jo", false); err != nil {
		t.Fatalf("the right digits, spaced = %v", err)
	}
	if out, _ := h.PairConfirmed(h.ctx, tr.Session(), true); out.State != Confirmed {
		t.Fatalf("the caller is told %+v", out)
	}
	peers, err := h.Peers(h.ctx)
	if err != nil || len(peers) != 1 || peers[0].Name != "laptop" || peers[0].Key != tr.Joiner.Key ||
		strings.Join(peers[0].Send, ",") != PeerPing || strings.Join(peers[0].Receive, ",") != PeerPing {
		t.Fatalf("peers = %+v, %v: want laptop with ping both ways and nothing else", peers, err)
	}

	// The carrier is closed with the pairing done, the answer holds no
	// digits, and nothing of the code or the check's typed half is anywhere
	// the yard wrote.
	if b, _ := h.GetBead(h.ctx, bead); b.Status != "closed" {
		t.Errorf("the carrier is %s after the pairing was done", b.Status)
	}
	notes, _ := h.Notes(h.ctx, bead)
	evs, _ := h.Events(h.ctx, "", 200)
	var written []string
	for _, n := range notes {
		written = append(written, n.Text)
	}
	for _, e := range evs {
		written = append(written, e.Kind)
		for k, v := range e.Data {
			written = append(written, k+"="+toText(v))
		}
	}
	for _, w := range written {
		if strings.Contains(w, code) || strings.Contains(w, strings.ReplaceAll(code, "-", "")) || strings.Contains(w, want) {
			t.Errorf("the code or the typed digits are written: %q", w)
		}
	}
}

func toText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// Five wrong tries end the pairing with wrong_check, take the card back and
// save nothing on either yard; the joiner is told the pairing ended, and a
// right try after the fifth is no longer taken.
func TestPairingCardWrongDigitsEndIt(t *testing.T) {
	// Sequential: the package's fake clock and Stretch (via newPairHost).
	h := newCardHost(t)
	opened, code := h.openCard(t)
	tr, err := h.call(code, "laptop", newPublicKey(t))
	if err != nil {
		t.Fatal(err)
	}
	bead, seq, _ := h.card(t)
	for try := 1; try <= MaxCheckTries; try++ {
		err := h.AnswerAsk(h.ctx, bead, seq, 0, wrongDigits3(tr), "jo", false)
		if err == nil || !strings.Contains(err.Error(), "not the last three digits") {
			t.Fatalf("try %d = %v", try, err)
		}
		if try < MaxCheckTries && !strings.Contains(err.Error(), "tries are left") {
			t.Fatalf("try %d does not say what is left: %v", try, err)
		}
	}
	p, err := h.GetPairing(h.ctx, opened.ID)
	if err != nil || p.State != PairEnded || p.Reason != WrongCheck {
		t.Fatalf("the pairing = %+v, %v", p, err)
	}
	if out, _ := h.PairConfirmed(h.ctx, tr.Session(), true); out.State != Ended || out.Reason != WrongCheck {
		t.Fatalf("the caller is told %+v", out)
	}
	err = h.AnswerAsk(h.ctx, bead, seq, 0, typed(tr), "jo", false)
	if err == nil || !strings.Contains(err.Error(), "wrong tries at the digits ended the pairing") {
		t.Fatalf("the right digits after the fifth = %v: want the card refused naming why it ended", err)
	}
	if ps, _ := h.Peers(h.ctx); len(ps) != 0 {
		t.Fatalf("peers = %+v", ps)
	}
	if b, _ := h.GetBead(h.ctx, bead); b.Status != "closed" {
		t.Errorf("the carrier is %s", b.Status)
	}
	if q, _ := h.UserAsks(h.ctx, "jo", "jo", "jo"); len(q.Open) != 0 {
		t.Errorf("the card still waits: %+v", q.Open)
	}
	evs := h.refusals(t)
	if len(evs) != 1 || evs[0].Data["reason"] != WrongCheck || evs[0].Data["wrong_checks"] != float64(MaxCheckTries) || evs[0].Data["card"] != true {
		t.Fatalf("refusals = %+v", evs)
	}
}

// A decline on the card ends the pairing declined, with the carrier closed
// and nothing saved; the other yard is told its operator said no.
func TestPairingCardDecline(t *testing.T) {
	// Sequential: the package's fake clock and Stretch (via newPairHost).
	h := newCardHost(t)
	opened, code := h.openCard(t)
	tr, err := h.call(code, "laptop", newPublicKey(t))
	if err != nil {
		t.Fatal(err)
	}
	bead, seq, _ := h.card(t)
	if err := h.DeclineAsk(h.ctx, bead, seq, "I did not ask for this", "jo"); err != nil {
		t.Fatal(err)
	}
	if p, _ := h.GetPairing(h.ctx, opened.ID); p.State != PairEnded || p.Reason != Declined {
		t.Fatalf("the pairing = %+v", p)
	}
	if out, _ := h.PairConfirmed(h.ctx, tr.Session(), true); out.State != Ended || out.Reason != Declined {
		t.Fatalf("the caller is told %+v", out)
	}
	if b, _ := h.GetBead(h.ctx, bead); b.Status != "closed" {
		t.Errorf("the carrier is %s", b.Status)
	}
	if ps, _ := h.Peers(h.ctx); len(ps) != 0 {
		t.Fatalf("peers = %+v", ps)
	}

	// A no said in a terminal ends it the same way, and takes the card back.
	_, code = h.openCard(t)
	tr, err = h.call(code, "laptop", newPublicKey(t))
	if err != nil {
		t.Fatal(err)
	}
	bead, seq, _ = h.card(t)
	if p, err := h.PairAnswer(h.ctx, "", false, "jo"); err != nil || p.State != PairEnded || p.Reason != Declined {
		t.Fatalf("a terminal's no = %+v, %v", p, err)
	}
	if err := h.AnswerAsk(h.ctx, bead, seq, 0, typed(tr), "jo", false); err == nil || !strings.Contains(err.Error(), "taken back by yard: declined") {
		t.Fatalf("an answer to the card of a pairing declined in a terminal = %v", err)
	}
	if b, _ := h.GetBead(h.ctx, bead); b.Status != "closed" {
		t.Errorf("the carrier is %s", b.Status)
	}
}

// An answer after the hour is refused naming the lapse and the command that
// opens another, and the card is taken back; so is one after the server's
// memory of the pairing is gone, which a restart does.
func TestPairingCardLapses(t *testing.T) {
	// Sequential: the package's fake clock and Stretch (via newPairHost).
	h := newCardHost(t)
	opened, code := h.openCard(t)
	tr, err := h.call(code, "laptop", newPublicKey(t))
	if err != nil {
		t.Fatal(err)
	}
	bead, seq, _ := h.card(t)
	h.at = h.at.Add(CardTTL)
	err = h.AnswerAsk(h.ctx, bead, seq, 0, typed(tr), "jo", false)
	if err == nil || !strings.Contains(err.Error(), "lapsed at "+opened.Expires.Local().Format("15:04")) || !strings.Contains(err.Error(), "yardr peer pair --card --name laptop") {
		t.Fatalf("an answer after the hour = %v: want the lapse and the command to open another", err)
	}
	if ps, _ := h.Peers(h.ctx); len(ps) != 0 {
		t.Fatalf("peers = %+v", ps)
	}
	if q, _ := h.UserAsks(h.ctx, "jo", "jo", "jo"); len(q.Open) != 0 {
		t.Errorf("the card still waits after the lapse: %+v", q.Open)
	}

	// A restart: the card stands in the store, the pairing is gone.
	h.at = h.at.Add(time.Minute)
	_, code = h.openCard(t)
	tr, err = h.call(code, "laptop", newPublicKey(t))
	if err != nil {
		t.Fatal(err)
	}
	bead, seq, _ = h.card(t)
	h.pair.cur = nil
	err = h.AnswerAsk(h.ctx, bead, seq, 0, typed(tr), "jo", false)
	if err == nil || !strings.Contains(err.Error(), "that pairing is gone") || !strings.Contains(err.Error(), "yardr peer pair --card") {
		t.Fatalf("an answer after a restart = %v", err)
	}
	if b, _ := h.GetBead(h.ctx, bead); b.Status != "closed" {
		t.Errorf("the carrier of a pairing that is gone is %s", b.Status)
	}
}

// Nobody but the operator answers the card: not a bead's agent or a crew
// member from its pane, whatever name it gives, and not a call that came
// through a peer's reach; none of them is counted as a wrong try, and the
// card is answered after by the operator as before.
func TestPairingCardIsAnsweredByThePerson(t *testing.T) {
	// Sequential: the package's fake clock and Stretch (via newPairHost).
	h := newCardHost(t)
	_, code := h.openCard(t)
	tr, err := h.call(code, "laptop", newPublicKey(t))
	if err != nil {
		t.Fatal(err)
	}
	bead, seq, _ := h.card(t)

	me := os.Getpid()
	ps, err := peer.Describe(me)
	if err != nil {
		t.Skipf("this process cannot be described: %v", err)
	}
	if err := h.CrewStarted(h.ctx, "yardmaster", map[string]any{"shell_pid": me, "shell_start": ps.Start}); err != nil {
		t.Fatal(err)
	}
	pane := WithCaller(h.ctx, me)
	for _, actor := range []string{"jo", "yardmaster"} {
		// The pane names its crew member whatever actor is given, so the ask's
		// own rule refuses it before the card is read.
		if err := h.AnswerAsk(pane, bead, seq, 0, typed(tr), actor, false); err == nil || !strings.Contains(err.Error(), "this is yardmaster") {
			t.Errorf("the right digits from the yardmaster's pane as %s = %v", actor, err)
		}
		if err := h.AnswerAsk(pane, bead, seq, 0, wrongDigits3(tr), actor, false); err == nil || strings.Contains(err.Error(), "digits") {
			t.Errorf("wrong digits from the yardmaster's pane as %s = %v: want the pane refused, not counted", actor, err)
		}
		if err := h.DeclineAsk(pane, bead, seq, "no", actor); err == nil {
			t.Errorf("a decline from the yardmaster's pane as %s", actor)
		}
	}
	reached := WithReach(h.ctx, "airy")
	if err := h.AnswerAsk(reached, bead, seq, 0, typed(tr), "jo", false); err == nil || !strings.Contains(err.Error(), "reach of airy") {
		t.Errorf("the right digits through a peer's reach = %v", err)
	}
	if err := h.AnswerAsk(h.ctx, bead, seq, 0, typed(tr), "ada", false); err == nil || !strings.Contains(err.Error(), "ask") {
		t.Errorf("the right digits from a member who is not the operator = %v", err)
	}
	if h.pair.cur.card.tries != 0 || h.pair.cur.State != PairCalled || h.pair.cur.Confirmed {
		t.Fatalf("refused answers changed the pairing: %+v (%d tries)", h.pair.cur.Pairing, h.pair.cur.card.tries)
	}
	if err := h.AnswerAsk(h.ctx, bead, seq, 0, typed(tr), "jo", false); err != nil {
		t.Fatalf("the operator's digits = %v", err)
	}
	if !h.pair.cur.Confirmed {
		t.Fatalf("the pairing = %+v", h.pair.cur.Pairing)
	}
}
