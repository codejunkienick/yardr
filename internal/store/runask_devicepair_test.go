package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// devicePairYard is a run yard where ada has a phone, and the pairing
// address is the one the command names, since no server owns the store
// to give it one of its own.
func devicePairYard(t *testing.T) *askYard {
	t.Helper()
	y, _ := runYard(t)
	if _, err := y.AddDevice(y.ctx, "ada", "phone", "mckean"); err != nil {
		t.Fatal(err)
	}
	return y
}

func devicePairAsk(command, to string) Ask {
	return Ask{Question: "Pair the new tablet?", To: to, Context: []string{"ada's tablet arrived"},
		Run: &AskRun{Argv: []string{command}}}
}

// A device pair card is strong (trust): it names the user, the device
// and the address whole, every device the user then has with its last
// use, and what approval opens. Approval answers the transient once to
// the approver, opens the pairing inside its own write, and keeps only
// the safe sentence: the named device then pairs with the answered
// code, and no stored answer, note or event holds it.
func TestRunAskDevicePair(t *testing.T) {
	y := devicePairYard(t)
	channelCalls := 0
	y.SetPairChannelKey(func() string {
		channelCalls++
		return "CHANNEL"
	})
	seq := y.ask(t, devicePairAsk("yardr user device pair ada tablet --url https://mac.example.net", "mckean"), "yardmaster")
	got := y.asked(t, seq)
	run := got.Ask.Run
	if want := []string{"user", "device", "pair", "ada", "tablet", "--url", "https://mac.example.net"}; !slices.Equal(run.Argv, want) {
		t.Fatalf("the ask stores %v, want %v", run.Argv, want)
	}
	if run.Description != "Enrol a device by a short code, or a QR code, with no token to copy" {
		t.Errorf("description %q", run.Description)
	}
	joined := strings.Join(run.Does, "\n")
	for _, want := range []string{
		"opens a pairing for the device tablet of ada, reached at https://mac.example.net.",
		"ada has 1 device: phone (never used).",
		"good for ten minutes and for one device",
		"Nothing in the yard keeps the code after it is used or runs out.",
		"ada will then have 2 devices.",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the card does not say %q; it says %q", want, run.Does)
		}
	}
	if !strings.Contains(run.Back, "user device pair --end") || !strings.Contains(run.Back, "user device rm ada tablet") {
		t.Errorf("the card is taken back by %q", run.Back)
	}
	if len(got.Ask.Weight) != 1 || got.Ask.Weight[0].Class != WeightTrust {
		t.Fatalf("the ask weighs %+v, want trust alone", got.Ask.Weight)
	}
	// A weighty card is approved only by a call that says its person
	// saw the weight, and asking opened nothing.
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil); err == nil {
		t.Fatal("an unweighed approval ran")
	}
	if y.DevicePairOpened(y.ctx) {
		t.Fatal("asking opened a pairing")
	}
	var policyCalls int
	y.SetPolicy(storePolicyFunc(func(ctx context.Context, tx *bolt.Tx, actor string, act Action, obj Object) error {
		if act == ActApproveOwn || act == ActAddDevice {
			policyCalls++
		}
		return CorePolicy.May(ctx, tx, actor, act, obj)
	}))
	oldProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(oldProcs)
	y.db.MaxBatchSize, y.db.MaxBatchDelay = 2, 10*time.Second
	reader := bytes.NewReader(make([]byte, 64))
	oldReader := rand.Reader
	rand.Reader = reader
	defer func() { rand.Reader = oldReader }()
	type approval struct {
		got *AskApproved
		err error
	}
	approved := make(chan approval, 1)
	go func() {
		got, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
		approved <- approval{got: got, err: err}
	}()
	for y.writing.Load() == 0 {
		runtime.Gosched()
	}
	// Give the first approval call time to enter bbolt's batch before the
	// second call fills it. The one P makes the first writer run until its
	// Batch call blocks, then this goroutine adds the failing call.
	runtime.Gosched()
	boom := errors.New("retry the approval batch")
	if err := y.db.Batch(func(*bolt.Tx) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("the deliberately failing batch call = %v", err)
	}
	result := <-approved
	rand.Reader = oldReader
	done, err := result.got, result.err
	if err != nil {
		t.Fatal(err)
	}
	if policyCalls != 4 {
		t.Fatalf("approval policy ran %d times, want both gates across the failed and committed batch attempts", policyCalls)
	}
	if reader.Len() != 20 {
		t.Fatalf("approval consumed %d random bytes, want 44 for one code and pairing id plus both transaction attempts", 64-reader.Len())
	}
	runtime.GOMAXPROCS(oldProcs)
	p := done.Pairing
	if p == nil {
		t.Fatal("the approval answered no pairing")
	}
	if channelCalls != 2 {
		t.Fatalf("the channel key provider ran %d times across the refused and approved calls, want once per approval attempt", channelCalls)
	}
	if _, err := y.DevicePairState(y.ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an approval-only pairing was readable by the operator: %v", err)
	}
	if _, err := y.DevicePairCodeSelf(y.ctx, "ada"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an approval-only pairing was readable by its user: %v", err)
	}
	if p.User != "ada" || p.Device != "tablet" || p.Address != "https://mac.example.net" || p.Yard != "" || p.ChannelKey != "CHANNEL" {
		t.Errorf("the transient is %+v", p)
	}
	if len(p.Code) != 9 || p.Code[4] != '-' {
		t.Errorf("the code is %q, want XXXX-XXXX", p.Code)
	}
	if p.QR != "https://mac.example.net#"+p.Code+"##CHANNEL" {
		t.Errorf("the QR string is %q", p.QR)
	}
	if !p.Expires.After(time.Now()) || p.Expires.After(time.Now().Add(TTL+time.Minute)) {
		t.Errorf("the pairing runs out at %v", p.Expires)
	}
	if strings.Contains(done.Result, p.Code) || !strings.Contains(done.Result, "shown to the approver alone and is kept nowhere") {
		t.Errorf("the recorded result is %q", done.Result)
	}
	if !y.DevicePairOpened(y.ctx) {
		t.Fatal("the approval opened no pairing")
	}
	// The code rests nowhere the yard keeps: not in the bead's notes,
	// not in its events, and a second approval answers nothing again.
	notes, err := y.Notes(y.ctx, y.bead)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range notes {
		if strings.Contains(n.Text, p.Code) {
			t.Fatalf("note %d holds the code: %q", n.Seq, n.Text)
		}
	}
	evs, err := y.EventsOfKind(y.ctx, "device_pair_opened", 2)
	if err != nil || len(evs) != 1 {
		t.Fatalf("device_pair_opened events = %+v, %v", evs, err)
	}
	if evs[0].Data["user"] != "ada" || evs[0].Data["device"] != "tablet" {
		t.Errorf("the opened event says %+v", evs[0].Data)
	}
	for _, v := range evs[0].Data {
		if s, ok := v.(string); ok && strings.Contains(s, p.Code) {
			t.Fatalf("the opened event holds the code in %q", s)
		}
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil ||
		!strings.Contains(err.Error(), "was approved already") {
		t.Fatalf("a second approval = %v", err)
	}
	// Nor in the bead as any later read shows it: its stored answer
	// holds the safe sentence alone.
	view, err := y.Bead(y.ctx, y.bead, "mckean")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range view.Asks {
		if a.Ran != nil && strings.Contains(a.Ran.Result, p.Code) {
			t.Fatalf("the bead's stored answer holds the code: %q", a.Ran.Result)
		}
	}
	// The named device pairs with the answered code, plainly typed.
	made, err := y.DevicePairRedeem(y.ctx, p.Code)
	if err != nil {
		t.Fatal(err)
	}
	if made.User != "ada" || made.Name != "tablet" || made.Token == "" {
		t.Errorf("the redemption made %+v", made.Device)
	}
	if got := deviceNames(t, y, "ada"); !slices.Equal(got, []string{"phone", "tablet"}) {
		t.Fatalf("ada holds %v", got)
	}

	t.Run("self approval", runAskDevicePairSelf)
	t.Run("busy and expiry", runAskDevicePairBusyAndExpiry)
	t.Run("refusals", runAskDevicePairRefusals)
	t.Run("address", runAskDevicePairAddress)
	t.Run("QR string", testDevicePairQR)
}

// The user whose pairing it is approves their own next device by the
// self leg, with no ActAddDevice; another user's approval is refused
// before anything runs, and no other row's approval carries a pairing.
func runAskDevicePairSelf(t *testing.T) {
	y := devicePairYard(t)
	if _, err := y.AddUser(y.ctx, "eve", "mckean"); err != nil {
		t.Fatal(err)
	}
	seq := y.ask(t, devicePairAsk("user device pair ada tablet --url https://mac.example.net", "ada"), "yardmaster")
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "eve", true, nil); err == nil {
		t.Fatal("eve approved ada's pairing")
	}
	if y.DevicePairOpened(y.ctx) {
		t.Fatal("a refused approval opened a pairing")
	}
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "ada", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if done.Pairing == nil || done.Pairing.Device != "tablet" {
		t.Fatalf("ada's own approval answered %+v", done.Pairing)
	}
	if _, err := y.DevicePairCloseSelf(y.ctx, "ada"); err != nil {
		t.Fatalf("ada could not end the approval-opened pairing from her own device: %v", err)
	}
	// No other row carries the field: a device removal approved on the
	// same yard answers none.
	seq = y.ask(t, deviceRmAsk("user device rm ada phone", "mckean"), "yardmaster")
	done, err = y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if done.Pairing != nil {
		t.Fatalf("a removal answered a pairing %+v", done.Pairing)
	}
}

// While a pairing stands, an approval that would open another is
// refused in the pairing's own words and the ask stays open; one whose
// time has run out is ended in the transaction that opens the next.
func runAskDevicePairBusyAndExpiry(t *testing.T) {
	y := devicePairYard(t)
	if _, err := y.DevicePairOpen(y.ctx, "ada", "watch", "mckean"); err != nil {
		t.Fatal(err)
	}
	seq := y.ask(t, devicePairAsk("user device pair ada tablet --url https://mac.example.net", "mckean"), "yardmaster")
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil ||
		!strings.Contains(err.Error(), "a pairing is already open for ada, try again in a few minutes") {
		t.Fatalf("approving over a standing pairing = %v", err)
	}
	// The ask stayed open: ending the standing pairing lets it through.
	if _, err := y.DevicePairClose(y.ctx, "mckean"); err != nil {
		t.Fatal(err)
	}
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
	if err != nil || done.Pairing == nil {
		t.Fatalf("approving once the pairing ended = %+v, %v", done, err)
	}
	// Run the clock past this pairing: the next approval ends it in its
	// own transaction and opens the next.
	y.SetDevicePairClock(func() time.Time { return time.Now().Add(TTL + time.Minute) })
	seq = y.ask(t, devicePairAsk("user device pair ada laptop --url https://mac.example.net", "mckean"), "yardmaster")
	done, err = y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
	if err != nil || done.Pairing == nil || done.Pairing.Device != "laptop" {
		t.Fatalf("approving past an expired pairing = %+v, %v", done, err)
	}
	evs, err := y.EventsOfKind(y.ctx, "device_pair_refused", 4)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range evs {
		if e.Data["reason"] == TimedOut && e.Data["device"] == "tablet" {
			found = true
		}
		if s, ok := e.Data["device"].(string); ok && strings.Contains(s, done.Pairing.Code) {
			t.Fatalf("a refused event holds a code: %+v", e.Data)
		}
	}
	if !found {
		t.Fatalf("no timed-out refusal ended the expired pairing: %+v", evs)
	}
}

// A device pair ask is refused, storing nothing, for what the row
// refuses, and its approval is held to the card: a device added since,
// a name taken since and a user removed since each leave no answer, no
// event and no pairing.
func runAskDevicePairRefusals(t *testing.T) {
	y := devicePairYard(t)
	if _, err := y.AddUser(y.ctx, "eve", "mckean"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, command, to, asker, want string
	}{
		{"no such user", "user device pair ghost tablet --url https://mac.example.net", "mckean", "yardmaster", "user ghost: not found"},
		{"device taken", "user device pair ada phone --url https://mac.example.net", "mckean", "yardmaster", "has a device phone already"},
		{"bad device name", "user device pair ada 'bad name' --url https://mac.example.net", "mckean", "yardmaster", "none of their characters"},
		{"one word", "user device pair ada", "mckean", "yardmaster", "one user and one device, not 1 words"},
		{"qr flag", "user device pair ada tablet --qr", "mckean", "yardmaster", "the operator's"},
		{"end flag", "user device pair --end", "mckean", "yardmaster", "the operator's"},
		{"no address", "user device pair ada tablet", "mckean", "yardmaster", "no listener a device can call"},
		{"the operator asks", "user device pair ada tablet --url https://mac.example.net", "mckean", "mckean", "needs no approval: run it yourself"},
		{"another user is asked", "user device pair ada tablet --url https://mac.example.net", "eve", "yardmaster", "this command is ada's to approve, or the operator's (mckean) for them"},
		{"nobody", "user device pair ada tablet --url https://mac.example.net", "mckean", "stranger", "a command for the operator to approve is proposed by a crew member or a user of the yard"},
	} {
		if _, err := y.AddAsk(y.ctx, y.bead, devicePairAsk(c.command, c.to), c.asker); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s = %v, want %q", c.name, err, c.want)
		}
	}
	// Stale: a device added since the card was written.
	seq := y.ask(t, devicePairAsk("user device pair ada tablet --url https://mac.example.net", "mckean"), "yardmaster")
	if _, err := y.AddDevice(y.ctx, "ada", "watch", "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil ||
		!strings.Contains(err.Error(), "had other devices") {
		t.Fatalf("approving with another device added since = %v", err)
	}
	if y.DevicePairOpened(y.ctx) {
		t.Fatal("a stale approval opened a pairing")
	}
	if _, err := y.RemoveDevice(y.ctx, "ada", "watch", "mckean"); err != nil {
		t.Fatal(err)
	}
	// Taken since: the name the card names is a device now.
	seq = y.ask(t, devicePairAsk("user device pair ada tablet --url https://mac.example.net", "mckean"), "yardmaster")
	if _, err := y.AddDevice(y.ctx, "ada", "tablet", "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil ||
		!strings.Contains(err.Error(), "has a device tablet already") {
		t.Fatalf("approving a name taken since = %v", err)
	}
	if _, err := y.RemoveDevice(y.ctx, "ada", "tablet", "mckean"); err != nil {
		t.Fatal(err)
	}
	// Removed since: the user the card names is gone.
	seq = y.ask(t, devicePairAsk("user device pair eve tablet --url https://mac.example.net", "mckean"), "yardmaster")
	if err := y.DeleteUser(y.ctx, "eve", "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil ||
		!strings.Contains(err.Error(), "user eve: not found") {
		t.Fatalf("approving for a removed user = %v", err)
	}
	if y.DevicePairOpened(y.ctx) {
		t.Fatal("a refused approval opened a pairing")
	}
	if evs, err := y.EventsOfKind(y.ctx, "device_pair_opened", 4); err != nil || len(evs) != 0 {
		t.Fatalf("refused approvals left opened events %+v, %v", evs, err)
	}
}

// Where the owning process gives the yard an address of its own, a card
// that names none takes it, and the approval answers it; at the edge's
// address the yard is named beside it in the transient and its QR.
func runAskDevicePairAddress(t *testing.T) {
	y := devicePairYard(t)
	y.SetPairAddress(func() (string, error) { return "http://192.0.2.8:8791", nil })
	seq := y.ask(t, devicePairAsk("user device pair ada tablet", "mckean"), "yardmaster")
	if joined := strings.Join(y.asked(t, seq).Ask.Run.Does, "\n"); !strings.Contains(joined, "reached at http://192.0.2.8:8791.") {
		t.Errorf("the card says %q", joined)
	}
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if done.Pairing.Address != "http://192.0.2.8:8791" || done.Pairing.Yard != "" {
		t.Errorf("the transient is %+v", done.Pairing)
	}
	if _, err := y.DevicePairClose(y.ctx, "mckean"); err != nil {
		t.Fatal(err)
	}
	edge := Edge{URL: "https://edge.example.net", Key: Public(newKey(t)), Handle: "ada", Name: "home"}
	if err := y.SetSetting(y.ctx, EdgeSetting, edge.String(), "mckean"); err != nil {
		t.Fatal(err)
	}
	y.SetPairAddress(func() (string, error) { return "https://edge.example.net", nil })
	seq = y.ask(t, devicePairAsk("user device pair ada laptop", "ada"), "yardmaster")
	done, err = y.ApproveAsk(y.ctx, y.bead, seq, "ada", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if done.Pairing.Yard != "ada/home" || done.Pairing.QR != "https://edge.example.net#"+done.Pairing.Code+"#ada/home" {
		t.Errorf("the edge transient is %+v", done.Pairing)
	}
}

// The QR string of a pairing holds the address, the code, the yard
// where the address is the edge's and the channel key as its fourth
// field, the yard field empty before it where none is named.
func testDevicePairQR(t *testing.T) {
	for _, c := range []struct{ address, code, yard, channel, want string }{
		{"https://a.example.net", "K7QM-2XPD", "", "", "https://a.example.net#K7QM-2XPD"},
		{"https://a.example.net", "K7QM-2XPD", "ada/home", "", "https://a.example.net#K7QM-2XPD#ada/home"},
		{"https://a.example.net", "K7QM-2XPD", "ada/home", "KEY", "https://a.example.net#K7QM-2XPD#ada/home#KEY"},
		{"https://a.example.net", "K7QM-2XPD", "", "KEY", "https://a.example.net#K7QM-2XPD##KEY"},
	} {
		if got := PairingQR(c.address, c.code, c.yard, c.channel); got != c.want {
			t.Errorf("PairingQR(%q, %q, %q, %q) = %q, want %q", c.address, c.code, c.yard, c.channel, got, c.want)
		}
	}
	if !IsDevicePairRun([]string{"user", "device", "pair", "ada", "tablet"}) ||
		IsDevicePairRun([]string{"user", "device", "rm", "ada", "tablet"}) {
		t.Error("IsDevicePairRun misreads its rows")
	}
}
