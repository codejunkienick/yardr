package rpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/store"
)

// turnYard is a yard with the operator jo and the user ada, each with a
// device, and a bead holding an ordinary card for jo per question.
type turnYard struct {
	st          *store.Store
	bead        string
	asks        []uint64
	phone, tabs string // jo's and ada's device tokens
}

func newTurnYard(t *testing.T, questions ...string) turnYard {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "yard.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close(context.Background()) })
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = st.AddUser(ctx, "jo", "jo")
	must(err)
	must(st.SetSetting(ctx, store.OperatorSetting, "jo", "jo"))
	_, err = st.AddUser(ctx, "ada", "jo")
	must(err)
	phone, err := st.AddDevice(ctx, "jo", "phone", "jo")
	must(err)
	tablet, err := st.AddDevice(ctx, "ada", "tablet", "jo")
	must(err)
	b, err := st.CreateBead(ctx, store.NewBead{Title: "Move the links"}, "yardmaster")
	must(err)
	y := turnYard{st: st, bead: b.ID, phone: phone.Token, tabs: tablet.Token}
	for _, q := range questions {
		seq, err := st.AddAsk(ctx, b.ID, store.Ask{Question: q, To: "jo", Context: []string{"the links."},
			Options: []store.AskOption{{Label: "Move them now", Consequence: "tonight"}, {Label: "After Friday", Consequence: "later"}}}, "yardmaster")
		must(err)
		y.asks = append(y.asks, seq)
	}
	return y
}

// ask reads the card seq as Bead serves it.
func (y turnYard) ask(t *testing.T, seq uint64) store.BeadAsk {
	t.Helper()
	v, err := y.st.Bead(context.Background(), y.bead)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range v.Asks {
		if a.Ask == seq {
			return a
		}
	}
	t.Fatalf("no ask %d", seq)
	return store.BeadAsk{}
}

// Over the socket the guarded calls are the store's, by name and through
// the generated client alike: a stale turn comes back as the conflict code,
// which the client reads as store.ErrConflict, and the answer made in the
// gap stands. Over the listener a device answers as its own user, as the
// unguarded calls do.
func TestGuardedAnswerOverTheWire(t *testing.T) {
	ctx := context.Background()
	y := newTurnYard(t, "Move the links now?", "Close sketch PR #3?", "Send them to build?")
	sock := socket(t)
	serve(t, sock, Handler(y.st, nil).ServeHTTP)
	hc, c := overSocket(sock), NewClient(sock)

	read := y.ask(t, y.asks[0])
	if err := c.AnswerAsk(ctx, y.bead, y.asks[0], 2, "external answer", "jo", false); err != nil {
		t.Fatal(err)
	}
	stale := fmt.Sprintf(`{"bead":%q,"ask":%d,"turn":%d,"option":1,"actor":"jo"}`, y.bead, y.asks[0], read.Turn)
	if got := string(post(t, hc, "AnswerAskExpected", stale)); !strings.Contains(got, `"code":"conflict"`) || !strings.Contains(got, "at turn 1, not 0") {
		t.Fatalf("the stale guarded pick by name = %s, want a conflict", got)
	}
	if err := c.AnswerAskExpected(ctx, y.bead, y.asks[0], read.Turn, 1, "", "jo", false); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("the stale guarded pick through the client = %v, want store.ErrConflict", err)
	}
	if got := y.ask(t, y.asks[0]); got.Option != 2 || got.Words != "external answer" || got.Turn != 1 {
		t.Fatalf("after the stale picks the card is %+v, want the external answer alone", got)
	}
	if err := c.DeferAskExpected(ctx, y.bead, y.asks[1], 0, "jo"); err != nil {
		t.Fatal(err)
	}
	if got := string(post(t, hc, "DeclineAskExpected", fmt.Sprintf(`["%s", %d, 0, "stale", "jo"]`, y.bead, y.asks[1]))); !strings.Contains(got, `"code":"conflict"`) {
		t.Fatalf("a stale decline in an array = %s, want a conflict", got)
	}

	h := NewUserServer(y.st, func(context.Context) string { return "south" }, nil).Handler()
	pick := fmt.Sprintf(`{"bead":%q,"ask":%d,"turn":0,"option":1}`, y.bead, y.asks[2])
	if got := call(t, h, y.tabs, "AnswerAskExpected", pick); !strings.Contains(got, `"error"`) || strings.Contains(got, "conflict") {
		t.Fatalf("ada's device answering jo's card = %s, want it refused as not hers", got)
	}
	asJo := fmt.Sprintf(`{"bead":%q,"ask":%d,"turn":0,"option":1,"actor":"jo"}`, y.bead, y.asks[2])
	if got := call(t, h, y.tabs, "AnswerAskExpected", asJo); !strings.Contains(got, `"error"`) {
		t.Fatalf("ada's device answering as jo = %s, want it refused", got)
	}
	if got := call(t, h, y.phone, "AnswerAskExpected", pick); got != "{}" {
		t.Fatalf("jo's device = %s, want {}", got)
	}
	if got := call(t, h, y.phone, "AnswerAskExpected", pick); !strings.Contains(got, `"code":"conflict"`) {
		t.Fatalf("jo's device again from the same read = %s, want a conflict", got)
	}
	// The card is at turn 1 now, and ada's device still holds its read of
	// turn 0: she is refused as before and told nothing of the turn.
	if got := call(t, h, y.tabs, "AnswerAskExpected", pick); !strings.Contains(got, `"error"`) || strings.Contains(got, "conflict") || strings.Contains(got, "at turn") {
		t.Fatalf("ada's device from a stale read = %s, want it refused as not hers, and no turn", got)
	}
	if got := y.ask(t, y.asks[2]); got.Option != 1 || got.Turn != 1 {
		t.Fatalf("the card jo's device answered = %+v", got)
	}
}

// A guarded answer never lands unguarded. A yard from before the guarded
// calls answers each as a method it does not have, and the client returns
// that error; and the unguarded calls refuse a turn, by name or as a further
// argument in an array, so a client that sends one to them is refused too.
// The card stays open through all of it.
func TestGuardedAnswerFailsClosedOnAnOlderYard(t *testing.T) {
	ctx := context.Background()
	y := newTurnYard(t, "Move the links now?")
	seq := y.asks[0]

	// The yard from before: the same server without the three calls.
	older := newAPI(y.st, time.Second)
	for _, name := range []string{"AnswerAskExpected", "DeferAskExpected", "DeclineAskExpected"} {
		delete(older.methods, name)
	}
	oldSock := socket(t)
	serve(t, oldSock, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		older.call(w, r, strings.TrimPrefix(r.URL.Path, "/call/"), body)
	})
	old := NewClient(oldSock)
	for name, err := range map[string]error{
		"AnswerAskExpected":  old.AnswerAskExpected(ctx, y.bead, seq, 0, 1, "", "jo", false),
		"DeferAskExpected":   old.DeferAskExpected(ctx, y.bead, seq, 0, "jo"),
		"DeclineAskExpected": old.DeclineAskExpected(ctx, y.bead, seq, 0, "no", "jo"),
	} {
		if err == nil || !strings.Contains(err.Error(), "no such method "+name) {
			t.Errorf("%s on the older yard = %v, want no such method", name, err)
		}
	}

	sock := socket(t)
	serve(t, sock, Handler(y.st, nil).ServeHTTP)
	hc := overSocket(sock)
	for _, c := range []struct{ method, body, want string }{
		{"AnswerAsk", fmt.Sprintf(`{"bead":%q,"ask":%d,"turn":0,"option":1,"actor":"jo"}`, y.bead, seq), `argument \"turn\": AnswerAsk has none of that name`},
		{"DeferAsk", fmt.Sprintf(`{"bead":%q,"ask":%d,"turn":0,"actor":"jo"}`, y.bead, seq), `argument \"turn\": DeferAsk has none of that name`},
		{"DeclineAsk", fmt.Sprintf(`{"bead":%q,"ask":%d,"turn":0,"words":"no","actor":"jo"}`, y.bead, seq), `argument \"turn\": DeclineAsk has none of that name`},
		{"AnswerAsk", fmt.Sprintf(`["%s", %d, 0, 1, "", "jo", false]`, y.bead, seq), "want 6 arguments, got 7"},
		{"DeferAsk", fmt.Sprintf(`["%s", %d, 0, "jo"]`, y.bead, seq), "want 3 arguments, got 4"},
		{"DeclineAsk", fmt.Sprintf(`["%s", %d, 0, "no", "jo"]`, y.bead, seq), "want 4 arguments, got 5"},
	} {
		if got := string(post(t, hc, c.method, c.body)); !strings.Contains(got, c.want) {
			t.Errorf("%s %s = %s, want %s", c.method, c.body, got, c.want)
		}
	}
	if got := y.ask(t, seq); got.State != "open" || got.Turn != 0 {
		t.Fatalf("the card is %s at turn %d, want open at 0", got.State, got.Turn)
	}
}
