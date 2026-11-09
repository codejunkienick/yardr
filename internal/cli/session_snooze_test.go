package cli

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
)

// yardr session snooze says the run's own session waits, not stuck: the
// wait goes on its handle and a session_snoozed event says what it waits
// on. Outside a session, and past the bounds, it is refused.
func TestSessionSnooze(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := context.Background()
	if err := st.PutGroup(ctx, store.Group{Name: "builders", Runner: "herdr"}, "me"); err != nil {
		t.Fatal(err)
	}
	b, err := st.CreateBead(ctx, store.NewBead{Title: "x"}, "me")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := st.Claim(ctx, b.ID, "builders", "new", "yard")
	if err != nil {
		t.Fatal(err)
	}
	// The session's pane is this process: over the socket every call carries
	// the test's pid, and a snooze from no pane is refused. No start time is
	// recorded, so the store trusts the pid alone while the session lives.
	if err := st.MarkRunning(ctx, sess.ID, map[string]any{"agent": "yard-x-new", "shell_pid": os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	a := testApp(t, st, t.TempDir(), "YARDR_SESSION="+sess.ID, "YARDR_ACTOR=yard-x-new")

	out, err := run(t, a.sessionCmd(), "snooze", "10m", "test gate")
	if err != nil {
		t.Fatalf("session snooze: %v", err)
	}
	if !strings.Contains(out, "snoozed 10m") || !strings.Contains(out, "test gate") {
		t.Errorf("session snooze printed %q, want the wait and the why", out)
	}
	got, err := st.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Handle[store.SnoozeUntil] == nil {
		t.Errorf("handle = %v, want the snooze kept", got.Handle)
	}
	evs, err := st.Events(ctx, b.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	var snoozed bool
	for _, e := range evs {
		if e.Kind == "session_snoozed" && e.Data["why"] == "test gate" {
			snoozed = true
		}
	}
	if !snoozed {
		t.Errorf("no session_snoozed event saying what the session waits on")
	}

	// The why reads - from stdin, as typed: a code span and $( ) stay text,
	// and one with no words is refused before anything is snoozed.
	const why = "the `go vet` run, $(not run)"
	feed := func(stdin string) (string, error) {
		c := a.sessionCmd()
		c.SetIn(strings.NewReader(stdin))
		return run(t, c, "snooze", "5m", "-")
	}
	got, err = st.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	before := got.Handle[store.SnoozeCount]
	for _, stdin := range []string{"", " \n\n"} {
		if _, err := feed(stdin); err == nil || !strings.Contains(err.Error(), "stdin gave no words for what the session waits on") {
			t.Fatalf("session snooze 5m - with %q on stdin err = %v, want refused", stdin, err)
		}
	}
	if got, err = st.GetSession(ctx, sess.ID); err != nil || got.Handle[store.SnoozeCount] != before {
		t.Fatalf("snooze count after the refusals = %v, want the %v it was: %v", got.Handle[store.SnoozeCount], before, err)
	}
	if out, err = feed(why + "\n\n"); err != nil || !strings.Contains(out, "snoozed 5m") || !strings.Contains(out, why) {
		t.Fatalf("session snooze 5m - printed %q, %v, want the wait and the why as typed", out, err)
	}
	if evs, err = st.Events(ctx, b.ID, 100); err != nil {
		t.Fatal(err)
	}
	snoozed = false
	for _, e := range evs {
		if e.Kind == "session_snoozed" && e.Data["why"] == why {
			snoozed = true
		}
	}
	if !snoozed {
		t.Errorf("no session_snoozed event with the why from stdin, as typed")
	}

	out, err = run(t, a.rootCmd(), "session", "snooze", "5m", "gate", "--json")
	if err != nil {
		t.Fatalf("session snooze --json: %v", err)
	}
	var js map[string]any
	if err := json.Unmarshal([]byte(out), &js); err != nil {
		t.Fatalf("session snooze --json printed %q: %v", out, err)
	}
	if js["session"] != sess.ID || js["duration"] != "5m0s" || js["why"] != "gate" || js["until"] == nil {
		t.Errorf("session snooze --json = %v, want the session, until, duration and why", js)
	}

	// Outside a session there is nothing to snooze; past the bounds the
	// store refuses with the limit.
	alone := testApp(t, st, t.TempDir(), "YARDR_ACTOR=mckean")
	if _, err := run(t, alone.sessionCmd(), "snooze", "10m", "test gate"); err == nil ||
		!strings.Contains(err.Error(), "inside a bead session") {
		t.Fatalf("session snooze outside a session err = %v, want refused", err)
	}
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"no duration", []string{"snooze"}, "accepts 2 arg(s)"},
		{"no why", []string{"snooze", "10m"}, "accepts 2 arg(s)"},
		{"bad duration", []string{"snooze", "ten", "test gate"}, `snooze "ten"`},
		{"past 15m", []string{"snooze", "16m", "test gate"}, "15m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := run(t, a.sessionCmd(), tc.args...); err == nil ||
				!strings.Contains(err.Error(), tc.want) {
				t.Fatalf("session %s err = %v, want %q", strings.Join(tc.args, " "), err, tc.want)
			}
		})
	}
}
