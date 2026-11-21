package store

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
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
	tellOutside(t, h.Store, h.ctx)
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
	if hasDigits(text, want) || strings.Contains(text, code) {
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
	dashless := strings.ReplaceAll(code, "-", "")
	for _, w := range written {
		switch {
		case strings.Contains(w, code):
			t.Errorf("the code is written (code arm, code %q, want %s): %q", code, want, w)
		case strings.Contains(w, dashless):
			t.Errorf("the code is written (dashless arm, code %q, want %s): %q", code, want, w)
		case hasDigits(w, want):
			t.Errorf("the typed digits are written (digits arm, code %q, want %s): %q", code, want, w)
		}
	}
	assertNoPairAlarm(t, h.Store, h.ctx)
}

func toText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// hasDigits reports whether want stands in s as a value rather than as a
// substring: with a non-digit that is no hex letter, or the string's edge,
// on both sides, outside a longer digit run. The check's typed half is
// three random digits, which coincide with fixed digits of the yard's own
// words (the recorded collision: the fixed "url=http://10.0.0.5:8790"
// line); a run glued to hex letters is a pairing id or session, both
// random hex, not a recording (the recorded collision: three of an id's
// hex digits). Glued to any other letter still counts. The code and its
// dashless form stay plain substrings: long and random, they coincide with
// nothing in practice.
func hasDigits(s, want string) bool {
	for at := 0; at+len(want) <= len(s); {
		i := strings.Index(s[at:], want)
		if i < 0 {
			return false
		}
		i += at
		edge := func(j int) bool {
			if j < 0 || j >= len(s) {
				return true
			}
			return (s[j] < '0' || s[j] > '9') && !isHexLetter(s[j])
		}
		if edge(i-1) && edge(i+len(want)) {
			return true
		}
		at = i + 1
	}
	return false
}

// isHexLetter reports whether c is a hexadecimal letter, either case.
func isHexLetter(c byte) bool {
	return c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// The digit-boundary check the leak scan trusts (yardr-cex8, yardr-xnhk):
// the fixed URL's digits are no recording of the typed half, while the
// half spaced, bare, glued to non-hex letters or against punctuation is. A
// run glued to hex letters is a pairing id or session, both random hex,
// not a recording: the forced collision below is three of an id's hex
// digits at hex glue. The URL holds two of the wants as substrings, so
// this table fails under the substring check it replaces and passes under
// the boundary one.
func TestHasDigits(t *testing.T) {
	// Sequential, like the file's tests.
	url := "url=http://10.0.0.5:8790"
	for _, c := range []struct {
		s, want string
		leak    bool
	}{
		{url, "879", false},
		{url, "790", false},
		{url, "900", false},
		{"confirmed 879", "879", true},
		{"879", "879", true},
		{"digits:879.", "879", true},
		{"xy879wz", "879", true},
		{"ab879cd", "879", false},
		{"pairing=ae41d879c0b2e3f4a5b6c7d8e9f0a1b2", "879", false},
		{`{"question":"Pair this yard with laptop?","pair":"ae41d879c0b2e3f4a5b6c7d8e9f0a1b2"}`, "879", false},
		{"pairing=0123456789abcdef0123456789abc879", "879", false},
		{"pairing=AE41D879C0B2E3F4A5B6C7D8E9F0A1B2", "879", false},
		{"8790", "879", false},
		{"1879", "879", false},
	} {
		if got := hasDigits(c.s, c.want); got != c.leak {
			t.Errorf("hasDigits(%q, %q) = %v, want %v", c.s, c.want, got, c.leak)
		}
	}
	for _, want := range []string{"879", "790"} {
		if !strings.Contains(url, want) {
			t.Errorf("the fixture URL holds no %q: the table proves nothing against substrings", want)
		}
	}
}

// A check whose two halves coincide in its first hash would have a card
// show what it asks for: the three it displays are the three it wants
// typed, so copying one's own screen would pass and the leak scan of
// TestPairingCardConfirms fires on the showing itself (yardr-1h41). The
// halves of the check a card splits therefore never coincide. This
// forces a transcript whose first hash does coincide (about one in a
// thousand, searched for here by that hash alone), then holds its
// check and its card to the split. No earlier test drives a colliding
// check, so this is a new function rather than a case of one.
func TestCardCheckHalvesDiffer(t *testing.T) {
	joiner := Party{Nonce: NewNonce(), Name: "laptop", Key: newPublicKey(t)}
	host := Party{Nonce: NewNonce(), Name: "north", Key: newPublicKey(t), URL: "http://10.0.0.5:8790"}
	var tr Transcript
	var raw string
	for range 100_000 {
		joiner.Nonce = NewNonce()
		tr = Transcript{Commit: Commit(joiner), Joiner: joiner, Host: host}
		n := binary.BigEndian.Uint64(sum(tr.fields("yardr-pair-check-1")...)) % 1_000_000
		raw = fmt.Sprintf("%03d %03d", n/1000, n%1000)
		if shown, want := CheckHalves(raw); shown == want {
			break
		}
		raw = ""
	}
	if raw == "" {
		t.Fatal("no transcript in 100000 tries has coinciding first-hash halves: the forced collision proves nothing")
	}
	shown, want := CheckHalves(tr.Check())
	if shown == want {
		t.Errorf("the check of a transcript whose first hash is %q is %q: its halves coincide", raw, tr.Check())
	}
	p := &hostPairing{
		Pairing: Pairing{Calls: "laptop", Peer: "laptop", Key: joiner.Key, Check: tr.Check(), Expires: time.Date(2026, 10, 5, 13, 0, 0, 0, time.UTC)},
		card:    &pairCard{to: "jo", word: "opened by jo"},
	}
	ask := p.cardAsk()
	text := strings.Join(append([]string{ask.Question}, ask.Context...), "\n")
	if !strings.Contains(text, shown) {
		t.Errorf("the card does not show %q:\n%s", shown, text)
	}
	if hasDigits(text, want) {
		t.Errorf("the card shows what it asks for (%q, from a first hash of %q):\n%s", want, raw, text)
	}
}

// callYards are two yards that pair with a card on the calling side: host
// shows the code in a terminal, join calls with it and holds the call for
// its operator, jo. One clock drives both.
type callYards struct {
	host *pairHost
	join *pairHost
	at   time.Time
	ctx  context.Context
}

func newCallYards(t *testing.T) *callYards {
	t.Helper()
	y := &callYards{host: newPairHost(t), join: newPairHost(t), ctx: context.Background()}
	// At real now, at the microsecond the store keeps: the carried
	// answer waits on a context deadline, and contexts keep real time
	// while the package's clock is fake. now() truncates its clock to
	// microseconds, and a Linux clock carries nanoseconds the Mac's
	// does not give, so a y.at with nanoseconds falls short of a
	// call's hour by them (yardr-t5v4).
	y.at = time.Now().Truncate(time.Microsecond)
	clock = func() time.Time { return y.at }
	for _, name := range []string{"jo", "ada"} {
		if _, err := y.join.AddUser(y.ctx, name, "jo"); err != nil {
			t.Fatal(err)
		}
	}
	if err := y.join.SetSetting(y.ctx, OperatorSetting, "jo", "jo"); err != nil {
		t.Fatal(err)
	}
	return y
}

// call is the joiner's call with the code host shows: host's two messages,
// as its side makes them, and the offer the call registers.
func (y *callYards) call(t *testing.T, code string) (Transcript, PairCallOffer) {
	t.Helper()
	me := Party{Nonce: NewNonce(), Name: "laptop", Key: newPublicKey(t)}
	tr := Transcript{Commit: Commit(me), Joiner: me}
	k := PairingKey(code, tr.Commit)
	offer, err := y.host.PairHello(y.ctx, NewHello(k, me))
	if err != nil {
		t.Fatal(err)
	}
	tr.Host = offer.Party
	if _, err := y.host.PairReveal(y.ctx, NewReveal(k, tr)); err != nil {
		t.Fatal(err)
	}
	return tr, PairCallOffer{URL: "http://10.0.0.5:8790", Peer: "north", Transcript: tr}
}

// register calls with host's code and registers the call on join as the
// operator.
func (y *callYards) register(t *testing.T) (Transcript, *PairCall) {
	t.Helper()
	opened, err := y.host.PairOpen(y.ctx, PairOffer{Name: "north", Key: y.host.key, URL: "http://10.0.0.5:8790"}, "jo")
	if err != nil {
		t.Fatal(err)
	}
	code, _ := CleanCode(opened.Code)
	tr, offer := y.call(t, code)
	reg, err := y.join.PairCallRegister(y.ctx, offer, "jo")
	if err != nil {
		t.Fatal(err)
	}
	return tr, reg
}

// worded answers the sender a held call's answer is carried with: what the
// host is told, as its side takes it, a yes waited on until it is no longer
// waiting.
func (y *callYards) worded(t *testing.T, told *[]bool) PairCallSender {
	t.Helper()
	return func(ctx context.Context, _ string, tr Transcript, yes bool) (*PairingOutcome, error) {
		*told = append(*told, yes)
		if !yes {
			return y.host.PairConfirmed(ctx, tr.Session(), false)
		}
		for {
			out, err := y.host.PairConfirmed(ctx, tr.Session(), true)
			if err != nil || out.State != Waiting {
				return out, err
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Millisecond):
			}
		}
	}
}

// card is the card the call filed: its bead, its seq and the ask.
func (y *callYards) card(t *testing.T) (bead string, seq uint64, ask Ask) {
	t.Helper()
	q, err := y.join.UserAsks(y.ctx, "jo", "jo", "jo")
	if err != nil || len(q.Open) != 1 {
		t.Fatalf("jo's open asks = %+v, %v: want the card", q, err)
	}
	notes, err := y.join.Notes(y.ctx, q.Open[0].Bead)
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

// first is the digits a joiner's card asks for, from the check the other
// operator reads out.
func first(tr Transcript) string {
	first, _ := CheckHalves(tr.Check())
	return first
}

// wrongFirst are three digits that are not the check's first three.
func wrongFirst(tr Transcript) string {
	if first(tr) == "000" {
		return "111"
	}
	return "000"
}

// A call registered for a card is good for an hour from the call, files its
// card and its carrier at once, and the card shows the last three digits of
// the check and never the first three, the code or anything of the carrier's
// that a person typed. The digits the operator then types, once the host has
// said yes, save the link on both sides as a terminal's pairing does, and
// the answer says they were right and not what they were.
func TestPairCallConfirms(t *testing.T) {
	// Sequential: the package's fake clock and Stretch (via newPairHost).
	y := newCallYards(t)
	tellOutside(t, y.join.Store, y.ctx)
	var told []bool
	y.join.SetPairCallSender(y.worded(t, &told))
	tr, reg := y.register(t)
	if got := reg.Expires.Sub(y.at); got != CardTTL || CardTTL != time.Hour {
		t.Fatalf("a call is good for %s", got)
	}
	if reg.State != PairCalled || reg.Peer != "north" || reg.Calls != "north" || reg.Bead == "" || reg.Ask == 0 {
		t.Fatalf("the call = %+v", reg)
	}
	bead, seq, ask := y.card(t)
	if bead != reg.Bead || seq != reg.Ask {
		t.Fatalf("the card is on %s ask %d, the call says %s ask %d", bead, seq, reg.Bead, reg.Ask)
	}
	want, shown := CheckHalves(tr.Check())
	text := strings.Join(append([]string{ask.Question}, ask.Context...), "\n")
	if ask.To != "jo" || ask.Pair != reg.ID || len(ask.Options) != 0 {
		t.Fatalf("the card = %+v", ask)
	}
	for _, line := range []string{shown, "north", tr.Host.Key, "opened by jo", "Decline a pairing you did not ask for", "ask the other yard's operator for a new code and call again"} {
		if !strings.Contains(text, line) {
			t.Errorf("the card does not say %q:\n%s", line, text)
		}
	}
	if hasDigits(text, want) {
		t.Errorf("the card shows what it asks for (%s):\n%s", want, text)
	}
	if !StrongWeight(ask.Weight) {
		t.Errorf("the card's weight = %+v, want strong", ask.Weight)
	}
	b, err := y.join.GetBead(y.ctx, bead)
	if err != nil || b.Type != TypeQuestion || b.Depot != nil {
		t.Fatalf("the carrier = %+v, %v: want a question bead of no depot", b, err)
	}
	var opened *Event
	evs, _ := y.join.Events(y.ctx, "", 100)
	for i, e := range evs {
		if e.Kind == "peer_pair_opened" {
			opened = &evs[i]
		}
	}
	if opened == nil || opened.Data["role"] != "joiner" || opened.Data["pairing"] != reg.ID || opened.Data["card"] != true {
		t.Fatalf("the opening's event = %+v", evs)
	}

	// The shape is refused and not counted: the whole check, the three on
	// the card and a word are no answer to it.
	for _, words := range []string{tr.Check(), want + shown, "abc", "12", ""} {
		if err := y.join.AnswerAsk(y.ctx, bead, seq, 0, words, "jo", false); err == nil {
			t.Fatalf("the card took %q", words)
		}
	}
	y.join.pair.mu.Lock()
	tries := y.join.pair.calls[reg.ID].tries
	y.join.pair.mu.Unlock()
	if tries != 0 {
		t.Fatalf("%d tries counted for answers of the wrong shape", tries)
	}

	// The host says yes first; the joiner's digits then save the link.
	if _, err := y.host.PairAnswer(y.ctx, "", true, "jo"); err != nil {
		t.Fatal(err)
	}
	if ps, _ := y.join.Peers(y.ctx); len(ps) != 0 {
		t.Fatalf("peers before the digits = %+v", ps)
	}
	if err := y.join.AnswerAsk(y.ctx, bead, seq, 0, want[:1]+" "+want[1:], "jo", false); err != nil {
		t.Fatalf("the right digits, spaced = %v", err)
	}
	if len(told) != 1 || !told[0] {
		t.Fatalf("the host was told %v", told)
	}
	peers, err := y.join.Peers(y.ctx)
	if err != nil || len(peers) != 1 || peers[0].Name != "north" || peers[0].Key != tr.Host.Key || peers[0].URL != "http://10.0.0.5:8790" ||
		strings.Join(peers[0].Send, ",") != PeerPing || strings.Join(peers[0].Receive, ",") != PeerPing {
		t.Fatalf("peers = %+v, %v: want north with ping both ways and nothing else", peers, err)
	}
	if ps, _ := y.host.Peers(y.ctx); len(ps) != 1 || ps[0].Name != "laptop" {
		t.Fatalf("the host's peers = %+v", ps)
	}

	// The carrier is closed with the pairing done, and nothing of the call
	// is anywhere the yard wrote: not the code, the check's typed half, or
	// what the digits are checked against.
	if b, _ := y.join.GetBead(y.ctx, bead); b.Status != "closed" {
		t.Errorf("the carrier is %s after the pairing was done", b.Status)
	}
	notes, _ := y.join.Notes(y.ctx, bead)
	evs, _ = y.join.Events(y.ctx, "", 200)
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
		if hasDigits(w, want) || strings.Contains(w, tr.Commit) || strings.Contains(w, tr.Joiner.Nonce) || strings.Contains(w, tr.Host.Nonce) {
			t.Errorf("the typed digits %s or the transcript are written: %q", want, w)
		}
	}

	// A confirmed reply may arrive as the local deadline fires. The host
	// already saved this yard, so this side must still save it too.
	if err := y.join.DeletePeer(y.ctx, "north", "jo"); err != nil {
		t.Fatal(err)
	}
	if err := y.host.DeletePeer(y.ctx, "laptop", "jo"); err != nil {
		t.Fatal(err)
	}
	y.at = y.at.Add(time.Second)
	tr, reg = y.register(t)
	bead, _, _ = y.card(t)
	if _, err := y.host.PairAnswer(y.ctx, "", true, "jo"); err != nil {
		t.Fatal(err)
	}
	out, err := y.host.PairConfirmed(y.ctx, tr.Session(), true)
	if err != nil || out == nil || out.State != Confirmed {
		t.Fatalf("the host's confirmed reply = %+v, %v", out, err)
	}
	y.join.pair.mu.Lock()
	jc := y.join.pair.calls[reg.ID]
	if jc != nil {
		jc.actor = "jo"
		delete(y.join.pair.calls, reg.ID)
	}
	y.join.pair.mu.Unlock()
	if jc == nil {
		t.Fatal("the registered call is gone")
	}
	if err := y.join.finishJoin(y.ctx, jc, out, nil, true, false); err != nil {
		t.Fatalf("the confirmed reply at expiry = %v", err)
	}
	if ps, _ := y.join.Peers(y.ctx); len(ps) != 1 || ps[0].Name != "north" {
		t.Fatalf("the joiner's peers = %+v", ps)
	}
	if ps, _ := y.host.Peers(y.ctx); len(ps) != 1 || ps[0].Name != "laptop" {
		t.Fatalf("the host's peers = %+v", ps)
	}
	if b, _ := y.join.GetBead(y.ctx, bead); b.Status != "closed" {
		t.Fatalf("the confirmed card's carrier is %s", b.Status)
	}
	assertNoPairAlarm(t, y.join.Store, y.ctx)
}

// Five wrong tries end the call with wrong_check, tell the host no and save
// nothing on either yard; a decline ends it declined the same way. The host
// sees peer_declined, as from a terminal that said no.
func TestPairCallWrongDigitsEndIt(t *testing.T) {
	// Sequential: the package's fake clock and Stretch (via newPairHost).
	y := newCallYards(t)
	tellOutside(t, y.join.Store, y.ctx)
	var told []bool
	y.join.SetPairCallSender(y.worded(t, &told))
	tr, _ := y.register(t)
	bead, seq, _ := y.card(t)
	for try := 1; try <= MaxCheckTries; try++ {
		err := y.join.AnswerAsk(y.ctx, bead, seq, 0, wrongFirst(tr), "jo", false)
		if err == nil || !strings.Contains(err.Error(), "not the first three digits") {
			t.Fatalf("try %d = %v", try, err)
		}
		if try < MaxCheckTries && !strings.Contains(err.Error(), "tries are left") {
			t.Fatalf("try %d does not say what is left: %v", try, err)
		}
	}
	if len(told) != 1 || told[0] {
		t.Fatalf("the host was told %v: want one no", told)
	}
	if out, _ := y.host.PairConfirmed(y.ctx, tr.Session(), true); out.State != Ended || out.Reason != PeerDeclined {
		t.Fatalf("the host's pairing = %+v", out)
	}
	err := y.join.AnswerAsk(y.ctx, bead, seq, 0, first(tr), "jo", false)
	if err == nil || !strings.Contains(err.Error(), "wrong tries at the digits ended the pairing") {
		t.Fatalf("the right digits after the fifth = %v: want the card refused naming why it ended", err)
	}
	if ps, _ := y.join.Peers(y.ctx); len(ps) != 0 {
		t.Fatalf("peers = %+v", ps)
	}
	if b, _ := y.join.GetBead(y.ctx, bead); b.Status != "closed" {
		t.Errorf("the carrier is %s", b.Status)
	}
	if q, _ := y.join.UserAsks(y.ctx, "jo", "jo", "jo"); len(q.Open) != 0 {
		t.Errorf("the card still waits: %+v", q.Open)
	}
	evs := y.join.refusals(t)
	if len(evs) != 1 || evs[0].Data["reason"] != WrongCheck || evs[0].Data["role"] != "joiner" {
		t.Fatalf("refusals = %+v", evs)
	}
	assertPairAlarm(t, y.join.Store, y.ctx, evs[0], "pairing with north ended after 5 wrong tries at the digits: nothing was saved on this yard. If the other yard shows a link from this pairing, its operator can remove this yard there (yardr peer rm <name>) and pair again.")

	// A decline tells the host no too, with nothing saved. Past the
	// cooldown the wrong digits put on the name.
	y.at = y.at.Add(pairCooldown + time.Minute)
	tr, _ = y.register(t)
	bead, seq, _ = y.card(t)
	if err := y.join.DeclineAsk(y.ctx, bead, seq, "I did not ask for this", "jo"); err != nil {
		t.Fatal(err)
	}
	if len(told) != 2 || told[1] {
		t.Fatalf("the host was told %v", told)
	}
	if out, _ := y.host.PairConfirmed(y.ctx, tr.Session(), true); out.State != Ended || out.Reason != PeerDeclined {
		t.Fatalf("the host's pairing = %+v", out)
	}
	if ps, _ := y.join.Peers(y.ctx); len(ps) != 0 {
		t.Fatalf("peers = %+v", ps)
	}
	if b, _ := y.join.GetBead(y.ctx, bead); b.Status != "closed" {
		t.Errorf("the carrier is %s", b.Status)
	}
	evs = y.join.refusals(t)
	if len(evs) != 2 || evs[1].Data["reason"] != Declined {
		t.Fatalf("refusals = %+v", evs)
	}
	assertPairAlarm(t, y.join.Store, y.ctx, evs[1], "pairing with north ended (declined): nothing was saved on this yard. If the other yard shows a link from this pairing, its operator can remove this yard there (yardr peer rm <name>) and pair again.")
}

// The operator ends a held call, which tells the host no and takes the card
// back; a stranger to the pairing may not, and no one ends what is gone. A
// yard with no server still ends its calls locally: a no that cannot be
// told is a no that failed.
func TestPairCallClose(t *testing.T) {
	// Sequential: the package's fake clock and Stretch (via newPairHost).
	y := newCallYards(t)
	var told []bool
	y.join.SetPairCallSender(y.worded(t, &told))
	tr, reg := y.register(t)
	bead, seq, _ := y.card(t)
	if err := y.join.PairCallClose(y.ctx, "", "ada"); err == nil || !strings.Contains(err.Error(), "only the operator") {
		t.Fatalf("a close by a member who is not the operator = %v", err)
	}
	if err := y.join.PairCallClose(y.ctx, "", "jo"); err != nil {
		t.Fatal(err)
	}
	if len(told) != 1 || told[0] {
		t.Fatalf("the host was told %v: want one no", told)
	}
	if out, _ := y.host.PairConfirmed(y.ctx, tr.Session(), true); out.State != Ended || out.Reason != PeerDeclined {
		t.Fatalf("the host's pairing = %+v", out)
	}
	if err := y.join.AnswerAsk(y.ctx, bead, seq, 0, first(tr), "jo", false); err == nil || !strings.Contains(err.Error(), "taken back by yard") {
		t.Fatalf("an answer after the close = %v", err)
	}
	if err := y.join.PairCallClose(y.ctx, reg.ID, "jo"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a close of what is gone = %v", err)
	}
	if evs := y.join.refusals(t); len(evs) != 1 || evs[0].Data["reason"] != Closed {
		t.Fatalf("refusals = %+v", evs)
	}

	// With no server the call still ends locally, but a verified answer is
	// refused rather than half carried: the card stays open. Past the
	// cooldown the close put on the name.
	y.join.SetPairCallSender(nil)
	y.at = y.at.Add(pairCooldown + time.Minute)
	tr, _ = y.register(t)
	bead, seq, _ = y.card(t)
	if err := y.join.AnswerAsk(y.ctx, bead, seq, 0, first(tr), "jo", false); err == nil || !strings.Contains(err.Error(), "carries a card's answer") {
		t.Fatalf("verified digits with no server = %v", err)
	}
	if q, _ := y.join.UserAsks(y.ctx, "jo", "jo", "jo"); len(q.Open) != 1 {
		t.Fatalf("the card does not wait: %+v", q.Open)
	}
	if err := y.join.PairCallClose(y.ctx, "", "jo"); err != nil {
		t.Fatal(err)
	}
	if len(told) != 1 {
		t.Fatalf("the host was told %v", told)
	}
	if b, _ := y.join.GetBead(y.ctx, bead); b.Status != "closed" {
		t.Errorf("the carrier is %s", b.Status)
	}
	if evs := y.join.refusals(t); len(evs) != 2 || evs[1].Data["reason"] != Closed {
		t.Fatalf("refusals = %+v", evs)
	}

	// With two calls held, no address names which. Past the cooldown the
	// close put on the name.
	y.join.SetPairCallSender(y.worded(t, &told))
	if err := y.host.PairClose(y.ctx, "", "jo"); err != nil {
		t.Fatal(err)
	}
	y.at = y.at.Add(pairCooldown + time.Minute)
	_, firstReg := y.register(t)
	if err := y.host.PairClose(y.ctx, "", "jo"); err != nil {
		t.Fatal(err)
	}
	_, secondReg := y.register(t)
	if err := y.join.PairCallClose(y.ctx, "", "jo"); err == nil || !strings.Contains(err.Error(), "more than one call") {
		t.Fatalf("a close that names no call = %v", err)
	}
	for _, id := range []string{firstReg.ID, secondReg.ID} {
		if err := y.join.PairCallClose(y.ctx, id, "jo"); err != nil {
			t.Fatal(err)
		}
	}
}

// An answer after the hour is refused naming the lapse and the new code to
// ask for, and the host is told no; so is one after the server's memory of
// the call is gone, which a restart does. A call that lapsed unanswered is
// ended when another is registered.
func TestPairCallLapses(t *testing.T) {
	// Sequential: the package's fake clock and Stretch (via newPairHost).
	y := newCallYards(t)
	tellOutside(t, y.join.Store, y.ctx)
	var told []bool
	y.join.SetPairCallSender(y.worded(t, &told))
	tr, reg := y.register(t)
	bead, seq, _ := y.card(t)
	y.at = y.at.Add(CardTTL)
	err := y.join.AnswerAsk(y.ctx, bead, seq, 0, first(tr), "jo", false)
	if err == nil || !strings.Contains(err.Error(), "lapsed at "+reg.Expires.Local().Format("15:04")) || !strings.Contains(err.Error(), "ask the other yard's operator for a new code") {
		t.Fatalf("an answer after the hour = %v: want the lapse and the new code to ask for", err)
	}
	if len(told) != 1 || told[0] {
		t.Fatalf("the host was told %v: want one no", told)
	}
	if ps, _ := y.join.Peers(y.ctx); len(ps) != 0 {
		t.Fatalf("peers = %+v", ps)
	}
	if q, _ := y.join.UserAsks(y.ctx, "jo", "jo", "jo"); len(q.Open) != 0 {
		t.Errorf("the card still waits after the lapse: %+v", q.Open)
	}
	if evs := y.join.refusals(t); len(evs) != 1 || evs[0].Data["reason"] != TimedOut {
		t.Fatalf("refusals = %+v", evs)
	} else {
		assertPairAlarm(t, y.join.Store, y.ctx, evs[0], "pairing with north lapsed: nothing was saved on this yard. If the other yard shows a link from this pairing, its operator can remove this yard there (yardr peer rm <name>) and pair again.")
	}

	// A restart: the card stands in the store, the call is gone. Past the
	// cooldown the lapse put on the name.
	y.at = y.at.Add(pairCooldown + time.Minute)
	tr, reg = y.register(t)
	bead, seq, _ = y.card(t)
	y.join.pair.mu.Lock()
	delete(y.join.pair.calls, reg.ID)
	y.join.pair.mu.Unlock()
	err = y.join.AnswerAsk(y.ctx, bead, seq, 0, first(tr), "jo", false)
	if err == nil || !strings.Contains(err.Error(), "a call lives in the yard's server memory") || !strings.Contains(err.Error(), "Ask the other yard's operator for a new code") {
		t.Fatalf("an answer after a restart = %v", err)
	}
	if b, _ := y.join.GetBead(y.ctx, bead); b.Status != "closed" {
		t.Errorf("the carrier of a call that is gone is %s", b.Status)
	}

	// Lapsed unanswered, and ended when another call is registered.
	y.at = y.at.Add(ConfirmTTL + time.Minute)
	_, _ = y.register(t)
	q, _ := y.join.UserAsks(y.ctx, "jo", "jo", "jo")
	if len(q.Open) != 1 {
		t.Fatalf("open asks = %+v: want the new card alone", q.Open)
	}
	y.at = y.at.Add(CardTTL)
	opened, err := y.host.PairOpen(y.ctx, PairOffer{Name: "north", Key: y.host.key, URL: "http://10.0.0.5:8790"}, "jo")
	if err != nil {
		t.Fatal(err)
	}
	code, _ := CleanCode(opened.Code)
	_, offer := y.call(t, code)
	if _, err := y.join.PairCallRegister(y.ctx, offer, "jo"); err == nil || !strings.Contains(err.Error(), "cooldown") {
		t.Fatalf("a call that discovers the expired attempt = %v: want the cooldown", err)
	}
	if len(told) != 2 || told[1] {
		t.Fatalf("the host was told %v", told)
	}
	if evs := y.join.refusals(t); len(evs) != 2 || evs[1].Data["reason"] != TimedOut {
		t.Fatalf("refusals = %+v", evs)
	} else {
		assertPairAlarm(t, y.join.Store, y.ctx, evs[1], "pairing with north lapsed: nothing was saved on this yard. If the other yard shows a link from this pairing, its operator can remove this yard there (yardr peer rm <name>) and pair again.")
	}
	if q, _ := y.join.UserAsks(y.ctx, "jo", "jo", "jo"); len(q.Open) != 0 {
		t.Fatalf("open asks = %+v: the expired card should be taken back before the retry is refused", q.Open)
	}
}

// Nobody but the operator answers the card: not a bead's agent or a crew
// member from its pane, whatever name it gives, and not a call that came
// through a peer's reach; none of them is counted as a wrong try, and the
// card is answered after by the operator as before.
func TestPairCallIsAnsweredByThePerson(t *testing.T) {
	// Sequential: the package's fake clock and Stretch (via newPairHost).
	y := newCallYards(t)
	var told []bool
	y.join.SetPairCallSender(y.worded(t, &told))
	tr, _ := y.register(t)
	bead, seq, _ := y.card(t)

	if _, err := y.join.PutCrew(y.ctx, CrewMember{Name: "yardmaster", Runner: "herdr"}, "jo"); err != nil {
		t.Fatal(err)
	}
	me := os.Getpid()
	ps, err := peer.Describe(me)
	if err != nil {
		t.Skipf("this process cannot be described: %v", err)
	}
	if err := y.join.CrewStarted(y.ctx, "yardmaster", map[string]any{"shell_pid": me, "shell_start": ps.Start}); err != nil {
		t.Fatal(err)
	}
	pane := WithCaller(y.ctx, me)
	for _, actor := range []string{"jo", "yardmaster"} {
		// The pane names its crew member whatever actor is given, so the ask's
		// own rule refuses it before the card is read.
		if err := y.join.AnswerAsk(pane, bead, seq, 0, first(tr), actor, false); err == nil || !strings.Contains(err.Error(), "this is yardmaster") {
			t.Errorf("the right digits from the yardmaster's pane as %s = %v", actor, err)
		}
		if err := y.join.AnswerAsk(pane, bead, seq, 0, wrongFirst(tr), actor, false); err == nil || strings.Contains(err.Error(), "digits") {
			t.Errorf("wrong digits from the yardmaster's pane as %s = %v: want the pane refused, not counted", actor, err)
		}
		if err := y.join.DeclineAsk(pane, bead, seq, "no", actor); err == nil {
			t.Errorf("a decline from the yardmaster's pane as %s", actor)
		}
	}
	reached := WithReach(y.ctx, "airy")
	if err := y.join.AnswerAsk(reached, bead, seq, 0, first(tr), "jo", false); err == nil || !strings.Contains(err.Error(), "reach of airy") {
		t.Errorf("the right digits through a peer's reach = %v", err)
	}
	if err := y.join.AnswerAsk(y.ctx, bead, seq, 0, first(tr), "ada", false); err == nil || !strings.Contains(err.Error(), "ask") {
		t.Errorf("the right digits from a member who is not the operator = %v", err)
	}
	y.join.pair.mu.Lock()
	var tries int
	var state string
	for _, jc := range y.join.pair.calls {
		tries, state = jc.tries, jc.State
	}
	y.join.pair.mu.Unlock()
	if tries != 0 || state != PairCalled {
		t.Fatalf("refused answers changed the call: %d tries, %s", tries, state)
	}
	if _, err := y.host.PairAnswer(y.ctx, "", true, "jo"); err != nil {
		t.Fatal(err)
	}
	if err := y.join.AnswerAsk(y.ctx, bead, seq, 0, first(tr), "jo", false); err != nil {
		t.Fatalf("the operator's digits = %v", err)
	}
	if ps, _ := y.join.Peers(y.ctx); len(ps) != 1 {
		t.Fatalf("peers = %+v", ps)
	}
}

// A verified answer completed after the host ended saves nothing and says
// the pairing ended there: on the host's no, on its time running out, and
// where the host answers nothing at all.
func TestPairCallVerifiedAfterHostEnded(t *testing.T) {
	// Sequential: the package's fake clock and Stretch (via newPairHost).
	y := newCallYards(t)
	tellOutside(t, y.join.Store, y.ctx)
	var told []bool
	y.join.SetPairCallSender(y.worded(t, &told))

	tr, _ := y.register(t)
	bead, seq, _ := y.card(t)
	if _, err := y.host.PairAnswer(y.ctx, "", false, "jo"); err != nil {
		t.Fatal(err)
	}
	err := y.join.AnswerAsk(y.ctx, bead, seq, 0, first(tr), "jo", false)
	if err == nil || !strings.Contains(err.Error(), "north's operator declined") || !strings.Contains(err.Error(), "nothing was saved on either yard") {
		t.Fatalf("the digits after the host declined = %v", err)
	}
	if len(told) != 1 || !told[0] {
		t.Fatalf("the host was told %v: want the yes, answered ended", told)
	}
	if ps, _ := y.join.Peers(y.ctx); len(ps) != 0 {
		t.Fatalf("peers = %+v", ps)
	}
	if b, _ := y.join.GetBead(y.ctx, bead); b.Status != "closed" {
		t.Errorf("the carrier is %s", b.Status)
	}
	if evs := y.join.refusals(t); len(evs) != 1 || evs[0].Data["reason"] != PeerDeclined {
		t.Fatalf("refusals = %+v", evs)
	}
	assertNoPairAlarm(t, y.join.Store, y.ctx)

	// The host's minutes to confirm run out first. Past the cooldown the
	// decline put on the name.
	y.at = y.at.Add(pairCooldown + time.Minute)
	tr, _ = y.register(t)
	bead, seq, _ = y.card(t)
	y.at = y.at.Add(ConfirmTTL + time.Minute)
	err = y.join.AnswerAsk(y.ctx, bead, seq, 0, first(tr), "jo", false)
	if err == nil || !strings.Contains(err.Error(), "did not both confirm in time") {
		t.Fatalf("the digits after the host's time ran out = %v", err)
	}
	if ps, _ := y.join.Peers(y.ctx); len(ps) != 0 {
		t.Fatalf("peers = %+v", ps)
	}
	if evs := y.join.refusals(t); len(evs) != 2 || evs[1].Data["reason"] != TimedOut {
		t.Fatalf("refusals = %+v", evs)
	} else {
		assertPairAlarm(t, y.join.Store, y.ctx, evs[1], "pairing with north lapsed: nothing was saved on this yard. If the other yard shows a link from this pairing, its operator can remove this yard there (yardr peer rm <name>) and pair again.")
	}

	// The host answers nothing at all: this yard saves nothing, and is told
	// to look there. Past the cooldown the timeout put on the name.
	y.at = y.at.Add(pairCooldown + time.Minute)
	tr, _ = y.register(t)
	bead, seq, _ = y.card(t)
	y.join.SetPairCallSender(func(context.Context, string, Transcript, bool) (*PairingOutcome, error) {
		return nil, errors.New("no yard there")
	})
	err = y.join.AnswerAsk(y.ctx, bead, seq, 0, first(tr), "jo", false)
	if err == nil || !strings.Contains(err.Error(), "asking north how the pairing stands") || !strings.Contains(err.Error(), "This yard saved nothing") {
		t.Fatalf("the digits with no host to tell = %v", err)
	}
	if ps, _ := y.join.Peers(y.ctx); len(ps) != 0 {
		t.Fatalf("peers = %+v", ps)
	}
	if b, _ := y.join.GetBead(y.ctx, bead); b.Status != "closed" {
		t.Errorf("the carrier is %s", b.Status)
	}
	if evs := y.join.refusals(t); len(evs) != 2 {
		t.Fatalf("refusals = %+v: want none logged for an answer that never arrived", evs)
	}
	if len(alarmed(t, y.join.Store, y.ctx)) != 1 {
		t.Fatalf("jo's notifications = %+v: want no alarm without a host outcome", alarmed(t, y.join.Store, y.ctx))
	}

	// The host never answers at all: the wait runs out with the call, tells
	// the host no and saves nothing.
	never := newCallYards(t)
	tellOutside(t, never.join.Store, never.ctx)
	var neverTold []bool
	never.join.SetPairCallSender(never.worded(t, &neverTold))
	// Truncated as newCallYards is: the clock stays at the precision
	// the store keeps.
	never.at = time.Now().Truncate(time.Microsecond).Add(-CardTTL + 2*time.Second)
	tr, _ = never.register(t)
	bead, seq, _ = never.card(t)
	err = never.join.AnswerAsk(never.ctx, bead, seq, 0, first(tr), "jo", false)
	if err == nil || !strings.Contains(err.Error(), "did not confirm within 60 minutes of the call") {
		t.Fatalf("the digits with a host that never answers = %v", err)
	}
	if len(neverTold) != 2 || !neverTold[0] || neverTold[1] {
		t.Fatalf("the host was told %v: want the yes and the no after it", neverTold)
	}
	if ps, _ := never.join.Peers(never.ctx); len(ps) != 0 {
		t.Fatalf("peers = %+v", ps)
	}
	if evs := never.join.refusals(t); len(evs) != 1 || evs[0].Data["reason"] != TimedOut {
		t.Fatalf("refusals = %+v", evs)
	} else {
		assertPairAlarm(t, never.join.Store, never.ctx, evs[0], "pairing with north lapsed: nothing was saved on this yard. If the other yard shows a link from this pairing, its operator can remove this yard there (yardr peer rm <name>) and pair again.")
	}
}

// The other yard's reason is its own text: where the card shows it, it is
// kept short and plain, as a terminal's pairing keeps it (cli/clipText), so
// a reason no yardr sends cannot carry newlines or a faked pairing line
// onto the card.
func TestPairCallClipsTheOtherYardsReason(t *testing.T) {
	// Sequential: the package's fake clock (via newCallYards).
	y := newCallYards(t)
	y.join.SetPairCallSender(func(context.Context, string, Transcript, bool) (*PairingOutcome, error) {
		return &PairingOutcome{State: Ended, Reason: "declined\npaired: north is saved as a peer on both yards, yes really"}, nil
	})
	tr, _ := y.register(t)
	bead, seq, _ := y.card(t)
	err := y.join.AnswerAsk(y.ctx, bead, seq, 0, first(tr), "jo", false)
	if err == nil {
		t.Fatal("the digits with a hostile reason ended silently")
	}
	if strings.Contains(err.Error(), "\n") || strings.Contains(err.Error(), "paired:") || strings.Contains(err.Error(), "yes really") {
		t.Errorf("the other yard's text is answered raw: %q", err)
	}
	if !strings.Contains(err.Error(), "declinedpairednorthissaved") {
		t.Errorf("the clipped reason is not answered: %q", err)
	}
	closed, _ := y.join.GetBead(y.ctx, bead)
	var why string
	if closed.CloseReason != nil {
		why = *closed.CloseReason
	}
	if strings.Contains(why, "\n") || strings.Contains(why, "paired:") || strings.Contains(why, "yes really") {
		t.Errorf("the other yard's text closes the carrier raw: %q", why)
	}
	if !strings.Contains(why, "declinedpairednorthissaved") {
		t.Errorf("the clipped reason does not close the carrier: %q", why)
	}
	if ps, _ := y.join.Peers(y.ctx); len(ps) != 0 {
		t.Fatalf("peers = %+v", ps)
	}
	if evs := y.join.refusals(t); len(evs) != 1 || evs[0].Data["reason"] != PeerDeclined {
		t.Fatalf("refusals = %+v", evs)
	}
}

// A call registers what the yard called with: a bad address, a bad name, a
// yard that is none, a call of its own key, and a name taken meanwhile are
// all refused before any card is filed.
func TestPairCallRegisterRefusesWhatIsNoCall(t *testing.T) {
	// Sequential: the package's fake clock and Stretch (via newPairHost).
	y := newCallYards(t)
	opened, err := y.host.PairOpen(y.ctx, PairOffer{Name: "north", Key: y.host.key, URL: "http://10.0.0.5:8790"}, "jo")
	if err != nil {
		t.Fatal(err)
	}
	code, _ := CleanCode(opened.Code)
	_, offer := y.call(t, code)
	try := func(name string, change func(*PairCallOffer), want string) {
		t.Helper()
		offer := offer
		change(&offer)
		if _, err := y.join.PairCallRegister(y.ctx, offer, "jo"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s = %v, want %q", name, err, want)
		}
	}
	try("a bad address", func(o *PairCallOffer) { o.URL = "north.example" }, "want http://host:port")
	try("a bad name", func(o *PairCallOffer) { o.Peer = "a/b" }, "use lowercase letters")
	try("a yard that is none", func(o *PairCallOffer) { o.Transcript.Host.Name = "a b" }, "no yard's name")
	try("a host with no URL", func(o *PairCallOffer) { o.Transcript.Host.URL = "" }, "no call the yard made")
	try("a call of its own key", func(o *PairCallOffer) { o.Transcript.Host.Key = o.Transcript.Joiner.Key }, "not paired with itself")
	if _, err := y.join.AddPeer(y.ctx, Peer{Name: "south", Key: newPublicKey(t)}, "jo"); err != nil {
		t.Fatal(err)
	}
	try("a name taken meanwhile", func(o *PairCallOffer) { o.Peer = "south" }, "peer south is added already")
	if q, _ := y.join.UserAsks(y.ctx, "jo", "jo", "jo"); len(q.Open) != 0 {
		t.Fatalf("a refused call filed a card: %+v", q.Open)
	}
}

// The joiner's digits wait for the host's word: the answer is carried while
// its operator still deliberates, and saves once they confirm. A close
// meanwhile waits the answer out instead of ending under it.
func TestPairCallWaitsForTheHost(t *testing.T) {
	// Sequential: the package's fake clock and Stretch (via newPairHost).
	y := newCallYards(t)
	yesSent := make(chan struct{})
	var once bool
	y.join.SetPairCallSender(func(ctx context.Context, _ string, tr Transcript, yes bool) (*PairingOutcome, error) {
		if !yes {
			return y.host.PairConfirmed(ctx, tr.Session(), false)
		}
		if !once {
			once = true
			close(yesSent)
		}
		for {
			out, err := y.host.PairConfirmed(ctx, tr.Session(), true)
			if err != nil || out.State != Waiting {
				return out, err
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Millisecond):
			}
		}
	})
	tr, _ := y.register(t)
	bead, seq, _ := y.card(t)
	done := make(chan error, 1)
	go func() {
		done <- y.join.AnswerAsk(y.ctx, bead, seq, 0, first(tr), "jo", false)
	}()
	select {
	case <-yesSent:
	case <-time.After(10 * time.Second):
		t.Fatal("the answer was not carried to the host")
	}
	if err := y.join.PairCallClose(y.ctx, "", "jo"); err == nil || !strings.Contains(err.Error(), "being carried") {
		t.Fatalf("a close while an answer is carried = %v", err)
	}
	if _, err := y.host.PairAnswer(y.ctx, "", true, "jo"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the carried answer = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the carried answer never came back")
	}
	if ps, _ := y.join.Peers(y.ctx); len(ps) != 1 || ps[0].Name != "north" {
		t.Fatalf("peers = %+v", ps)
	}
	if ps, _ := y.host.Peers(y.ctx); len(ps) != 1 || ps[0].Name != "laptop" {
		t.Fatalf("the host's peers = %+v", ps)
	}
}

// Five wrong tries end the pairing with wrong_check, take the card back and
// save nothing on either yard; the joiner is told the pairing ended, and a
// right try after the fifth is no longer taken.
func TestPairingCardWrongDigitsEndIt(t *testing.T) {
	// Sequential: the package's fake clock and Stretch (via newPairHost).
	h := newCardHost(t)
	tellOutside(t, h.Store, h.ctx)
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
	assertPairAlarm(t, h.Store, h.ctx, evs[0], "pairing with laptop ended after 5 wrong tries at the digits: nothing was saved on this yard. If the other yard shows a link from this pairing, its operator can remove this yard there (yardr peer rm <name>) and pair again.")
}

// A decline on the card ends the pairing declined, with the carrier closed
// and nothing saved; the other yard is told its operator said no.
func TestPairingCardDecline(t *testing.T) {
	// Sequential: the package's fake clock and Stretch (via newPairHost).
	h := newCardHost(t)
	tellOutside(t, h.Store, h.ctx)
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
	if evs := h.refusals(t); len(evs) != 1 || evs[0].Data["reason"] != Declined {
		t.Fatalf("refusals = %+v", evs)
	} else {
		assertPairAlarm(t, h.Store, h.ctx, evs[0], "pairing with laptop ended (declined): nothing was saved on this yard. If the other yard shows a link from this pairing, its operator can remove this yard there (yardr peer rm <name>) and pair again.")
	}

	// A no said in a terminal ends it the same way, and takes the card back.
	// Past the cooldown the decline put on the name.
	h.at = h.at.Add(pairCooldown + time.Minute)
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
	if evs := h.refusals(t); len(evs) != 2 || evs[1].Data["reason"] != Declined {
		t.Fatalf("refusals = %+v", evs)
	} else {
		assertPairAlarm(t, h.Store, h.ctx, evs[1], "pairing with laptop ended (declined): nothing was saved on this yard. If the other yard shows a link from this pairing, its operator can remove this yard there (yardr peer rm <name>) and pair again.")
	}
}

// An answer after the hour is refused naming the lapse and the command that
// opens another, and the card is taken back; so is one after the server's
// memory of the pairing is gone, which a restart does.
func TestPairingCardLapses(t *testing.T) {
	// Sequential: the package's fake clock and Stretch (via newPairHost).
	h := newCardHost(t)
	tellOutside(t, h.Store, h.ctx)
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
	if evs := h.refusals(t); len(evs) != 1 || evs[0].Data["reason"] != TimedOut {
		t.Fatalf("refusals = %+v", evs)
	} else {
		assertPairAlarm(t, h.Store, h.ctx, evs[0], "pairing with laptop lapsed: nothing was saved on this yard. If the other yard shows a link from this pairing, its operator can remove this yard there (yardr peer rm <name>) and pair again.")
	}

	// A restart: the card stands in the store, the pairing is gone. Past
	// the cooldown the lapse put on the name.
	h.at = h.at.Add(pairCooldown + time.Minute)
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

// A card pairing that ends cools its name down: another card open naming
// the same yard within pairCooldown is refused naming the cooldown, and
// kept on the record beside the attempt and its end, while another yard
// opens at once; past the cooldown it opens again. A confirmed pairing
// cools nothing.
func TestPairingCardCoolsDown(t *testing.T) {
	// Sequential: the package's fake clock and Stretch (via newPairHost).
	h := newCardHost(t)
	opened, code := h.openCard(t)
	if _, err := h.call(code, "laptop", newPublicKey(t)); err != nil {
		t.Fatal(err)
	}
	bead, seq, _ := h.card(t)
	if err := h.DeclineAsk(h.ctx, bead, seq, "I did not ask for this", "jo"); err != nil {
		t.Fatal(err)
	}
	var rec *pairAttempt
	if err := h.read(func(tx *bolt.Tx) error {
		var err error
		rec, err = lastPairEnd(tx, "laptop")
		return err
	}); err != nil || rec == nil || rec.Reason != Declined || rec.Role != "host" || rec.Pairing != opened.ID || !rec.At.Equal(h.at) {
		t.Fatalf("the record = %+v, %v", rec, err)
	}

	// A re-open on the name is refused naming the cooldown and when to
	// try again, and kept on the record beside the attempt and its end.
	lifts := h.at.Add(pairCooldown)
	offer := PairOffer{Name: "north", Key: h.key, URL: "http://10.0.0.5:8790", As: "laptop", Card: true}
	if _, err := h.PairOpen(h.ctx, offer, "jo"); err == nil || !strings.Contains(err.Error(), "cooldown") ||
		!strings.Contains(err.Error(), "try again after "+lifts.Local().Format("15:04")) {
		t.Fatalf("a re-open inside the cooldown = %v", err)
	}
	h.at = rec.At.Add(-time.Minute)
	if _, err := h.PairOpen(h.ctx, offer, "jo"); err == nil || !strings.Contains(err.Error(), "cooldown") {
		t.Fatalf("a clock moved behind the attempt = %v: want the cooldown to hold", err)
	}
	h.at = rec.At
	evs, _ := h.Events(h.ctx, "", 100)
	var attempt, end, cooled *Event
	for i, e := range evs {
		switch e.Kind {
		case "peer_pair_opened":
			attempt = &evs[i]
		case "peer_pair_refused":
			end = &evs[i]
		case "peer_pair_cooldown":
			cooled = &evs[i]
		}
	}
	if attempt == nil || end == nil || cooled == nil {
		t.Fatalf("the record holds %+v: want the attempt, its end and the cooldown's refusal", evs)
	}
	if cooled.Data["role"] != "host" || cooled.Data["peer"] != "laptop" {
		t.Fatalf("the cooldown's event = %+v", cooled)
	}
	retry, err := time.Parse(time.RFC3339Nano, cooled.Data["retry_after"].(string))
	if err != nil || !retry.Equal(lifts) {
		t.Fatalf("the cooldown lifts %v, %v: want %s", cooled.Data["retry_after"], err, lifts)
	}

	// Another yard opens at once, and its close cools its own name.
	offer.As = "desktop"
	if _, err := h.PairOpen(h.ctx, offer, "jo"); err != nil {
		t.Fatalf("another yard inside the cooldown = %v", err)
	}
	if p, err := h.GetPairing(h.ctx, ""); err != nil || p.State != PairOpened {
		t.Fatalf("the pairing = %+v, %v", p, err)
	}
	if err := h.PairClose(h.ctx, "", "jo"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.PairOpen(h.ctx, offer, "jo"); err == nil || !strings.Contains(err.Error(), "cooldown") {
		t.Fatalf("a re-open after the close = %v: want the cooldown, which a close starts too", err)
	}

	// Past the cooldown the name opens again, and a pairing that confirms
	// cools nothing: once the peer is removed it opens at once.
	h.at = h.at.Add(pairCooldown + time.Minute)
	opened, code = h.openCard(t)
	tr, err := h.call(code, "laptop", newPublicKey(t))
	if err != nil {
		t.Fatal(err)
	}
	bead, seq, _ = h.card(t)
	if out, err := h.PairConfirmed(h.ctx, tr.Session(), true); err != nil || out.State != Waiting {
		t.Fatalf("the caller's yes = %+v, %v", out, err)
	}
	if err := h.AnswerAsk(h.ctx, bead, seq, 0, typed(tr), "jo", false); err != nil {
		t.Fatalf("the right digits = %v", err)
	}
	if p, _ := h.GetPairing(h.ctx, opened.ID); p.State != PairDone {
		t.Fatalf("the pairing = %+v", p)
	}
	if ps, _ := h.Peers(h.ctx); len(ps) != 1 || ps[0].Name != "laptop" {
		t.Fatalf("peers = %+v", ps)
	}
	if err := h.DeletePeer(h.ctx, "laptop", "jo"); err != nil {
		t.Fatal(err)
	}
	offer.As = "laptop"
	if _, err := h.PairOpen(h.ctx, offer, "jo"); err != nil {
		t.Fatalf("a re-open after a confirmed pairing = %v: want no cooldown", err)
	}
	if err := h.PairClose(h.ctx, "", "jo"); err != nil {
		t.Fatal(err)
	}

	// Opening after a card lapsed first ends and records that attempt, then
	// refuses the same name during its cooldown.
	lapsed := newCardHost(t)
	lapsed.openCard(t)
	lapsed.at = lapsed.at.Add(CardTTL)
	if _, err := lapsed.PairOpen(lapsed.ctx, PairOffer{Name: "north", Key: lapsed.key, URL: "http://10.0.0.5:8790", As: "laptop", Card: true}, "jo"); err == nil || !strings.Contains(err.Error(), "cooldown") {
		t.Fatalf("a re-open that discovers a lapsed card = %v: want the cooldown", err)
	}
	if err := lapsed.read(func(tx *bolt.Tx) error {
		var err error
		rec, err = lastPairEnd(tx, "laptop")
		return err
	}); err != nil || rec == nil || rec.Reason != TimedOut {
		t.Fatalf("the lapsed attempt = %+v, %v: want it recorded before the re-open", rec, err)
	}
}

// A call that ends cools its name down the same way: another call naming
// the same yard within pairCooldown is refused naming the cooldown, and
// past it the call goes through. The cooldown is the peer pair's, not the
// side's: a hosted opening for the name waits too.
func TestPairCallCoolsDown(t *testing.T) {
	// Sequential: the package's fake clock and Stretch (via newPairHost).
	y := newCallYards(t)
	var told []bool
	y.join.SetPairCallSender(y.worded(t, &told))
	_, reg := y.register(t)
	bead, seq, _ := y.card(t)
	if err := y.join.DeclineAsk(y.ctx, bead, seq, "I did not ask for this", "jo"); err != nil {
		t.Fatal(err)
	}
	var rec *pairAttempt
	if err := y.join.read(func(tx *bolt.Tx) error {
		var err error
		rec, err = lastPairEnd(tx, "north")
		return err
	}); err != nil || rec == nil || rec.Reason != Declined || rec.Role != "joiner" || rec.Pairing != reg.ID {
		t.Fatalf("the record = %+v, %v", rec, err)
	}

	// Another call on the name is refused naming the cooldown, and kept on
	// the record as the joiner's.
	opened, err := y.host.PairOpen(y.ctx, PairOffer{Name: "north", Key: y.host.key, URL: "http://10.0.0.5:8790"}, "jo")
	if err != nil {
		t.Fatal(err)
	}
	code, _ := CleanCode(opened.Code)
	_, offer := y.call(t, code)
	if _, err := y.join.PairCallRegister(y.ctx, offer, "jo"); err == nil || !strings.Contains(err.Error(), "cooldown") ||
		!strings.Contains(err.Error(), "try again after") {
		t.Fatalf("another call inside the cooldown = %v", err)
	}
	evs, _ := y.join.Events(y.ctx, "", 100)
	var cooled *Event
	for i, e := range evs {
		if e.Kind == "peer_pair_cooldown" {
			cooled = &evs[i]
		}
	}
	if cooled == nil || cooled.Data["role"] != "joiner" || cooled.Data["peer"] != "north" || cooled.Data["retry_after"] == "" {
		t.Fatalf("the cooldown's event = %+v", cooled)
	}
	// A hosted opening for the name waits too: the cooldown is the peer
	// pair's.
	offered := PairOffer{Name: "laptop", Key: y.join.key, URL: "http://10.0.0.5:8790", As: "north", Card: true}
	if _, err := y.join.PairOpen(y.ctx, offered, "jo"); err == nil || !strings.Contains(err.Error(), "cooldown") {
		t.Fatalf("a hosted opening inside the call's cooldown = %v", err)
	}

	// Past the cooldown the call goes through, and the host answers it.
	y.at = y.at.Add(pairCooldown + time.Minute)
	if err := y.host.PairClose(y.ctx, "", "jo"); err != nil {
		t.Fatal(err)
	}
	tr, reg := y.register(t)
	bead, seq, _ = y.card(t)
	if _, err := y.host.PairAnswer(y.ctx, "", true, "jo"); err != nil {
		t.Fatal(err)
	}
	if err := y.join.AnswerAsk(y.ctx, bead, seq, 0, first(tr), "jo", false); err != nil {
		t.Fatalf("the right digits past the cooldown = %v", err)
	}
	if ps, _ := y.join.Peers(y.ctx); len(ps) != 1 || ps[0].Name != "north" {
		t.Fatalf("peers = %+v", ps)
	}
	_ = reg
}

// alarmed is jo's notifications, oldest first.
func alarmed(t *testing.T, s *Store, ctx context.Context) []Notification {
	t.Helper()
	page, err := s.UserNotifications(ctx, "jo", 0, "jo")
	if err != nil {
		t.Fatal(err)
	}
	return page.Notifications
}

// tellOutside names a source that tells jo outside the yard, written
// directly: the alarm's write is what is tested, not the source.
func tellOutside(t *testing.T, s *Store, ctx context.Context) {
	t.Helper()
	err := s.write(func(w *wtx) error {
		u, err := getUser(w.Tx, "jo")
		if err != nil {
			return err
		}
		u.Notify = "pushover"
		return put(w.Tx, bUsers, "jo", u)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// alarmPush is whether jo has a notification push queued for seq: the alarm
// told outside the yard.
func alarmPush(t *testing.T, s *Store, ctx context.Context, seq uint64) bool {
	t.Helper()
	pushes, err := s.Pushes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pushes {
		if p.User == "jo" && p.Part == PushNotification && p.Seq == seq {
			return true
		}
	}
	return false
}

func assertPairAlarm(t *testing.T, s *Store, ctx context.Context, event Event, want string) {
	t.Helper()
	if Notified(event, "jo") != ReasonPairEnded {
		t.Fatalf("event %d notified %+v: want jo told %s", event.Seq, event.Data["notified"], ReasonPairEnded)
	}
	for _, n := range alarmed(t, s, ctx) {
		if n.Seq != event.Seq {
			continue
		}
		if n.Kind != "peer_pair_refused" || n.Reason != ReasonPairEnded || n.Text != want {
			t.Fatalf("the alarm = %+v, want %q", n, want)
		}
		if !alarmPush(t, s, ctx, event.Seq) {
			t.Fatalf("no push queued for the alarm on event %d", event.Seq)
		}
		return
	}
	t.Fatalf("no notification for event %d", event.Seq)
}

func assertNoPairAlarm(t *testing.T, s *Store, ctx context.Context) {
	t.Helper()
	if got := alarmed(t, s, ctx); len(got) != 0 {
		t.Fatalf("notifications = %+v: want no pairing alarm", got)
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
