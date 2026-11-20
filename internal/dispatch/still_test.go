package dispatch_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/dispatch"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
)

// The pane of a builder (muse) that stopped on something no error line
// knows: herdr kept calling it working, so no check that reads an idle
// agent's pane ever ran, and nobody heard (yardr-q7ek). A harness at work
// redraws its screen; this one stands.
const museUnknownError = "◆ response parse error: unexpected token (request id:\n" +
	" 9a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d)\n" +
	"── Voice input (alt(option)+v to start) ──…\n" +
	"❯ \n" +
	"───…\n" +
	"    muse-spark-1.3 · max · /private/tmp/lyr0/work · Auto-review"

// An agent herdr calls working whose pane's end has stood unchanged for
// the group's still limit counts as idle, whatever it shows: its unknown
// error raises no session_harness_error, and session_idle fires at the
// idle limit with the pane's end in tail.
func TestStillWorkingPaneCountsAsIdle(t *testing.T) {
	t.Parallel()
	s, r, d, b := harnessSetup(t, "muse", map[string]any{"still": "30ms", "idle": "30ms", "stall": "off"})
	ctx := context.Background()
	now := time.Now()
	dispatch.SetClock(d, func() time.Time { return now })
	r.show("working", museUnknownError)
	must(t, d.Reconcile(ctx)) // the pane stands, short of the still limit
	if n := len(eventKinds(t, s, b.ID, "session_idle")); n != 0 {
		t.Fatalf("session_idle before the still limit: %d", n)
	}
	now = now.Add(31 * time.Millisecond)
	must(t, d.Reconcile(ctx)) // still past the limit: the idle spell starts
	if n := len(eventKinds(t, s, b.ID, "session_idle")); n != 0 {
		t.Fatalf("session_idle before the idle limit: %d", n)
	}
	now = now.Add(31 * time.Millisecond)
	must(t, d.Reconcile(ctx))
	evs := eventKinds(t, s, b.ID, "session_idle")
	if len(evs) != 1 || evs[0].Data["tail"] != museUnknownError {
		t.Fatalf("session_idle = %v, want one with the still pane in tail", evs)
	}
	if n := len(eventKinds(t, s, b.ID, "session_harness_error")); n != 0 {
		t.Fatalf("session_harness_error for an error no line knows: %d", n)
	}

	// The pane moves again: the spell is over, and nothing more is told
	// until a pane stands past the still limit anew.
	r.show("working", "◆ Thinking (3s · esc to interrupt)")
	now = now.Add(time.Millisecond)
	must(t, d.Reconcile(ctx))
	if n := len(eventKinds(t, s, b.ID, "session_idle")); n != 1 {
		t.Fatalf("session_idle events = %d, want 1 once the pane moves", n)
	}
}

// The session of event 235073 stood on agy's quota line while herdr
// called it working: the pane stood still, so the still path counts it
// idle, raises session_harness_error with the line's reset, keeps the
// wait and model hold past the idle limit even if the countdown redraws,
// and prompts at the wake.
func TestAgyQuotaIsReportedWhileHerdrSaysWorking(t *testing.T) {
	t.Parallel()
	r := &retryRunner{agentRunner: &agentRunner{statusRunner: &statusRunner{
		fakeRunner: &fakeRunner{dead: map[string]bool{}}, status: "working"}, worked: true}}
	s, d, b, _, now := resetYardOf(t, r, map[string]any{
		"kind": "agy", "still": "30ms", "idle": "30ms", "stall": "off", "model": "k3",
	})
	ctx := context.Background()
	r.show("working", agyQuotaTail)
	must(t, d.Reconcile(ctx)) // the pane stands, short of the still limit
	if n := len(eventKinds(t, s, b.ID, "session_harness_error")); n != 0 {
		t.Fatalf("session_harness_error before the still limit: %d", n)
	}
	*now = now.Add(31 * time.Millisecond)
	must(t, d.Reconcile(ctx)) // still past the limit: the idle spell starts
	resets := now.Add(2*time.Hour + 15*time.Minute + 47*time.Second)
	wakes := resets.Add(time.Minute)
	evs := eventKinds(t, s, b.ID, "session_harness_error")
	if len(evs) != 1 || evs[0].Data["reason"] != dispatch.ReasonRateLimited || evs[0].Data["kind"] != "agy" ||
		evs[0].Data["model"] != "k3" || evs[0].Data["line"] != agyQuotaLine || evs[0].Data["window"] != "Individual" ||
		evs[0].Data["resets_at"] != resets.UTC().Format(time.RFC3339) || evs[0].Data["wakes_at"] != wakes.UTC().Format(time.RFC3339) {
		t.Fatalf("session_harness_error = %v, want one, rate_limited, with agy's quota line and reset", evs)
	}
	if until, window, model, ok := dispatch.ResetWait(handleOfBead(t, s, b)); !ok || !until.Equal(wakes) || window != "Individual" || model != "k3" {
		t.Fatalf("wait = %v %q %q %v, want until %v for Individual/k3", until, window, model, ok, wakes)
	}

	// Past the idle limit while herdr still calls it working: no
	// session_idle, no nudge, no second error.
	*now = now.Add(40 * time.Minute)
	must(t, d.Reconcile(ctx))
	if len(r.nudges) != 0 || len(eventKinds(t, s, b.ID, "session_idle")) != 0 || len(eventKinds(t, s, b.ID, "session_harness_error")) != 1 {
		t.Fatalf("in the wait: typed %q, session_idle %d, harness errors %d; want silence",
			r.nudges, len(eventKinds(t, s, b.ID, "session_idle")), len(eventKinds(t, s, b.ID, "session_harness_error")))
	}

	// A redrawn countdown while herdr still calls it working keeps the
	// wait and model hold; once the redrawn pane stands past the wake,
	// the agent is prompted.
	ticked := strings.Replace(agyQuotaTail, "2h15m47s", "1h35m47s", 1)
	r.show("working", ticked)
	must(t, d.Reconcile(ctx))
	if _, _, _, ok := dispatch.ResetWait(handleOfBead(t, s, b)); !ok {
		t.Fatalf("wait cleared on countdown redraw: %v", handleOfBead(t, s, b))
	}
	*now = wakes.Add(time.Second)
	must(t, d.Reconcile(ctx))
	const agyResetLine = "yardr: the usage limit your harness stopped on (agy, Individual window) has reset. Go on where you stopped."
	if len(r.nudges) != 1 || r.nudges[0] != agyResetLine {
		t.Fatalf("typed %q at the wake, want %q once", r.nudges, agyResetLine)
	}
}

// A pane whose end changes within the still limit is a harness at work:
// nothing is told of it, however long it runs. Neither is a pane that
// stands when the group turned the check off, nor one no pane can be read
// of.
func TestMovingWorkingPaneCountsAsWorking(t *testing.T) {
	t.Parallel()
	s, r, d, b := harnessSetup(t, "muse", map[string]any{"still": "30ms", "idle": "30ms", "stall": "off"})
	ctx := context.Background()
	now := time.Now()
	dispatch.SetClock(d, func() time.Time { return now })
	for _, tail := range []string{"◆ Thinking (3s · esc to interrupt)", "◆ Thinking (65s · esc to interrupt)",
		"◆ Thinking (3s · esc to interrupt)", "◆ Thinking (65s · esc to interrupt)"} {
		r.show("working", tail)
		must(t, d.Reconcile(ctx))
		now = now.Add(20 * time.Millisecond)
	}
	// Each redraw arrived before the unchanged-pane limit, even though the
	// whole sequence ran longer than it.
	must(t, d.Reconcile(ctx))
	if n := len(eventKinds(t, s, b.ID, "session_idle")); n != 0 {
		t.Fatalf("session_idle for a pane that redraws: %d", n)
	}
	if n := len(eventKinds(t, s, b.ID, "session_harness_error")); n != 0 {
		t.Fatalf("session_harness_error for a pane that redraws: %d", n)
	}
}

// A failed pane read cannot prove that the pane stayed unchanged. When it
// becomes readable again, the still interval starts with that observation.
func TestUnreadableWorkingPaneResetsStillObservation(t *testing.T) {
	t.Parallel()
	s, r, d, b := harnessSetup(t, "muse", map[string]any{"still": "30ms", "idle": "30ms", "stall": "off"})
	ctx := context.Background()
	now := time.Now()
	dispatch.SetClock(d, func() time.Time { return now })
	r.show("working", museUnknownError)
	must(t, d.Reconcile(ctx)) // first readable observation

	now = now.Add(31 * time.Millisecond)
	r.setTailError(errors.New("pane read failed"))
	must(t, d.Reconcile(ctx)) // no observation: reset the interval

	now = now.Add(31 * time.Millisecond)
	r.setTailError(nil)
	must(t, d.Reconcile(ctx)) // same tail, but it has only just become readable
	now = now.Add(31 * time.Millisecond)
	must(t, d.Reconcile(ctx)) // the idle spell starts now, after a full still interval
	if n := len(eventKinds(t, s, b.ID, "session_idle")); n != 0 {
		t.Fatalf("session_idle while the recovered pane was only just deemed still: %d", n)
	}
}

func TestStillWorkingPaneWithNoCheckIsWorking(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		cfg  map[string]any
		pane bool // the runner has a pane to read
		tail string
	}{
		"still off":       {cfg: map[string]any{"still": "off", "idle": "30ms", "stall": "off"}, pane: true, tail: museUnknownError},
		"no pane to read": {cfg: map[string]any{"still": "30ms", "idle": "30ms", "stall": "off"}},
	} {
		t.Run(name, func(t *testing.T) {
			now := time.Now()
			s := storetest.New(t)
			ctx := context.Background()
			var r dispatch.Runner
			if tc.pane {
				r = &agentRunner{statusRunner: &statusRunner{fakeRunner: &fakeRunner{dead: map[string]bool{}}, status: "working"}, worked: true}
			} else {
				r = &statusRunner{fakeRunner: &fakeRunner{dead: map[string]bool{}}, status: "working"}
			}
			d := &dispatch.Dispatcher{Store: s, Runners: map[string]dispatch.Runner{"fake": r}, Backoff: time.Hour}
			dispatch.SetClock(d, func() time.Time { return now })
			conf := map[string]any{"kind": "muse"}
			for k, v := range tc.cfg {
				conf[k] = v
			}
			must(t, s.PutGroup(ctx, store.Group{Name: "g", Runner: "fake", Config: conf}, "me"))
			_, err := s.AddRoute(ctx, store.Route{Stage: "new", Group: "g"}, "me")
			must(t, err)
			b, err := s.CreateBead(ctx, store.NewBead{Title: "x"}, "me")
			must(t, err)
			must(t, d.Reconcile(ctx)) // start
			if a, ok := r.(*agentRunner); ok {
				a.show("working", tc.tail)
			}
			must(t, d.Reconcile(ctx))
			now = now.Add(time.Minute)
			must(t, d.Reconcile(ctx))
			now = now.Add(time.Minute)
			must(t, d.Reconcile(ctx))
			if n := len(eventKinds(t, s, b.ID, "session_idle")); n != 0 {
				t.Fatalf("session_idle = %d, want none", n)
			}
			if n := len(eventKinds(t, s, b.ID, "session_harness_error")); n != 0 {
				t.Fatalf("session_harness_error = %d, want none", n)
			}
		})
	}
}
