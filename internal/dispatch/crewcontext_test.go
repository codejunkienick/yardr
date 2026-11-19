package dispatch_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"maps"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/dispatch"
	"yardr.dev/yardr/internal/store"
)

// Past 60% of the window an idle crew session is asked once for a
// handoff, and never restarted for it: no grace, no forced restart. The
// nudge writes crew_context_asked, the reading is recorded for crew show,
// and a new session is asked again.
func TestContextAsksOnceAndNeverForces(t *testing.T) {
	t.Parallel()
	s, f, d := crewSetup(t, store.CrewMember{HandoffGrace: "1ms"})
	ctx := context.Background()
	f.ctxOK, f.ctxUsed = true, 120000 // 60% of the 200k window
	must(t, d.Reconcile(ctx))         // start
	must(t, d.Reconcile(ctx))         // idle at 60%: asked
	if len(f.nudges) != 1 || !strings.HasPrefix(f.nudges[0], "yardmaster-1: Your context is 60% of the window. ") {
		t.Fatalf("nudges %v", f.nudges)
	}
	r, _ := s.GetCrew(ctx, "yardmaster")
	if r.ContextAskedAt == nil || r.ContextPct != 60 || r.ContextWindow != 200000 || r.ContextAt == nil {
		t.Fatalf("after the ask: %+v", r.CrewSession)
	}
	if r.StartedAt == nil || !f.ctxStartedAt.Equal(*r.StartedAt) {
		t.Fatalf("context start = %v, want %v", f.ctxStartedAt, r.StartedAt)
	}
	evs := eventKinds(t, s, r.Journal, "crew_context_asked")
	if len(evs) != 1 || evs[0].Data["pct"] != float64(60) || evs[0].Data["window"] != float64(200000) {
		t.Fatalf("crew_context_asked events = %+v", evs)
	}
	f.ctxUsed = 190000 // heavier still: asked already, and no restart after any grace
	time.Sleep(5 * time.Millisecond)
	must(t, d.Reconcile(ctx))
	must(t, d.Reconcile(ctx))
	if len(f.nudges) != 1 || f.n != 1 {
		t.Fatalf("nudges %v starts %d, want the one ask and no restart", f.nudges, f.n)
	}
	// A new session is asked again.
	_, err := s.Journal(ctx, "yardmaster", store.NoteHandoff, "Next: nothing", "me")
	must(t, err)
	must(t, d.Reconcile(ctx)) // restart requested ...
	must(t, d.Reconcile(ctx)) // ... and carried out
	must(t, d.Reconcile(ctx)) // the new session, idle at 95%: asked
	if len(f.nudges) != 2 || f.n != 2 {
		t.Fatalf("nudges %v starts %d, want the new session asked", f.nudges, f.n)
	}
}

// Below the threshold nothing is asked, but the reading is still
// recorded. A custom threshold and a 1m window are read from the member,
// and 0 asks never.
func TestContextThresholds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t.Run("below 60 nothing is asked", func(t *testing.T) {
		t.Parallel()
		s, f, d := crewSetup(t, store.CrewMember{})
		f.ctxOK, f.ctxUsed = true, 100000 // 50%
		must(t, d.Reconcile(ctx))
		must(t, d.Reconcile(ctx))
		if len(f.nudges) != 0 {
			t.Fatalf("nudges %v", f.nudges)
		}
		if r, _ := s.GetCrew(ctx, "yardmaster"); r.ContextAskedAt != nil || r.ContextPct != 50 || r.ContextAt == nil {
			t.Fatalf("reading = %+v", r.CrewSession)
		}
	})
	t.Run("a custom threshold", func(t *testing.T) {
		t.Parallel()
		_, f, d := crewSetup(t, store.CrewMember{ContextHandoff: "80"})
		f.ctxOK, f.ctxUsed = true, 120000 // 60%: under it
		must(t, d.Reconcile(ctx))
		must(t, d.Reconcile(ctx))
		f.ctxUsed = 160000 // 80%: asked
		must(t, d.Reconcile(ctx))
		if len(f.nudges) != 1 || !strings.Contains(f.nudges[0], "Your context is 80% of the window. ") {
			t.Fatalf("nudges %v", f.nudges)
		}
	})
	t.Run("a 1m window", func(t *testing.T) {
		t.Parallel()
		s, f, d := crewSetup(t, store.CrewMember{Config: map[string]any{"model": "opus[1m]"}})
		f.ctxOK, f.ctxUsed = true, 600000 // 60% of a million
		must(t, d.Reconcile(ctx))
		must(t, d.Reconcile(ctx))
		if len(f.nudges) != 1 {
			t.Fatalf("nudges %v", f.nudges)
		}
		if r, _ := s.GetCrew(ctx, "yardmaster"); r.ContextPct != 60 || r.ContextWindow != 1000000 {
			t.Fatalf("reading = %+v", r.CrewSession)
		}
	})
	t.Run("a declared window takes precedence", func(t *testing.T) {
		t.Parallel()
		s, f, d := crewSetup(t, store.CrewMember{Config: map[string]any{"kind": "muse", "model": "example[200k]"}})
		f.ctxOK, f.ctxUsed = true, 600000 // 59% of Muse's window, below the default threshold.
		f.ctxWindow = 1007997
		must(t, d.Reconcile(ctx))
		must(t, d.Reconcile(ctx))
		if len(f.nudges) != 0 {
			t.Fatalf("nudges %v", f.nudges)
		}
		if r, _ := s.GetCrew(ctx, "yardmaster"); r.ContextPct != 59 || r.ContextWindow != 1007997 {
			t.Fatalf("reading = %+v", r.CrewSession)
		}
	})
	t.Run("0 asks never", func(t *testing.T) {
		t.Parallel()
		s, f, d := crewSetup(t, store.CrewMember{ContextHandoff: "0"})
		f.ctxOK, f.ctxUsed = true, 190000
		must(t, d.Reconcile(ctx))
		must(t, d.Reconcile(ctx))
		must(t, d.Reconcile(ctx))
		if len(f.nudges) != 0 {
			t.Fatalf("nudges %v", f.nudges)
		}
		if r, _ := s.GetCrew(ctx, "yardmaster"); r.ContextAskedAt != nil || r.ContextPct != 95 {
			t.Fatalf("reading = %+v", r.CrewSession)
		}
	})
	t.Run("a reported window follows the current call", func(t *testing.T) {
		t.Parallel()
		s, f, d := crewSetup(t, store.CrewMember{Config: map[string]any{"kind": "codex", "model": "example[1m]"}})
		f.ctxOK, f.ctxUsed, f.ctxWindow = true, 190000, 475000 // 40%, regardless of the model name.
		must(t, d.Reconcile(ctx))
		must(t, d.Reconcile(ctx))
		if r, _ := s.GetCrew(ctx, "yardmaster"); r.ContextPct != 40 || r.ContextWindow != 475000 || len(f.nudges) != 0 {
			t.Fatalf("first window: %+v; nudges %v", r.CrewSession, f.nudges)
		}
		f.ctxUsed, f.ctxWindow = 155040, 258400 // The next call holds 60% of its own window.
		must(t, d.Reconcile(ctx))
		if r, _ := s.GetCrew(ctx, "yardmaster"); r.ContextPct != 60 || r.ContextWindow != 258400 || r.ContextAskedAt == nil || len(f.nudges) != 1 {
			t.Fatalf("changed window: %+v; nudges %v", r.CrewSession, f.nudges)
		}
	})
}

// A kind with no context source is never read for one and never nudged,
// whatever the session holds: the 24h failsafe alone.
func TestContextWithoutASourceNeverNudges(t *testing.T) {
	t.Parallel()
	s, f, d := crewSetup(t, store.CrewMember{})
	ctx := context.Background()
	f.ctxUsed = 190000 // ... but the kind names no source
	must(t, d.Reconcile(ctx))
	must(t, d.Reconcile(ctx))
	must(t, d.Reconcile(ctx))
	if len(f.nudges) != 0 {
		t.Fatalf("nudges %v", f.nudges)
	}
	if r, _ := s.GetCrew(ctx, "yardmaster"); r.ContextAskedAt != nil || r.ContextAt != nil {
		t.Fatalf("a reading without a source: %+v", r.CrewSession)
	}
}

// Working sessions record their use without a nudge; an idle session asks
// only once its input is clear, not while a person is writing.
func TestContextReadWhenWorkingAskedWhenIdleAndTyped(t *testing.T) {
	t.Parallel()
	// One window covers it: crewContext takes no branch on the kind, and
	// the fake reader returns the use and window it was given.
	const window = 1007997
	s, f, d := crewSetup(t, store.CrewMember{Config: map[string]any{"kind": "muse", "model": "muse-wonder-1.0"}})
	ctx := context.Background()
	now := time.Now()
	dispatch.SetClock(d, func() time.Time { return now })
	f.ctxOK, f.ctxUsed, f.ctxWindow = true, (window*50+99)/100, window
	must(t, d.Reconcile(ctx)) // start
	agent := agentOf(t, s)
	f.set(agent, "working")
	must(t, d.Reconcile(ctx))
	r, err := s.GetCrew(ctx, "yardmaster")
	must(t, err)
	if f.ctxCalls != 1 || r.ContextAt == nil || r.ContextPct != 50 || r.ContextWindow != window {
		t.Fatalf("working reading: %+v; calls %d", r.CrewSession, f.ctxCalls)
	}
	if r.StartedAt == nil || !f.ctxStartedAt.Equal(*r.StartedAt) {
		t.Fatalf("context start = %v, want %v", f.ctxStartedAt, r.StartedAt)
	}
	readAt := *r.ContextAt
	f.ctxUsed = (window*95 + 99) / 100
	must(t, d.Reconcile(ctx)) // still inside the working cadence: not read, not written
	r, err = s.GetCrew(ctx, "yardmaster")
	must(t, err)
	if f.ctxCalls != 1 || r.ContextPct != 50 || r.ContextAt == nil || !r.ContextAt.Equal(readAt) || r.ContextAskedAt != nil || len(f.nudges) != 0 {
		t.Fatalf("reading inside the cadence: %+v; calls %d, nudges %v", r.CrewSession, f.ctxCalls, f.nudges)
	}
	now = now.Add(dispatch.ContextWhileWorking())
	must(t, d.Reconcile(ctx)) // working above the threshold: record, never ask
	r, err = s.GetCrew(ctx, "yardmaster")
	must(t, err)
	if f.ctxCalls != 2 || r.ContextPct != 95 || r.ContextAskedAt != nil || len(f.nudges) != 0 || f.n != 1 || f.stops != 0 {
		t.Fatalf("working above threshold: %+v; calls %d, nudges %v, starts %d, stops %d", r.CrewSession, f.ctxCalls, f.nudges, f.n, f.stops)
	}
	f.set(agent, "idle")
	f.input = "half a thought" // the nudge is held, and nothing recorded as asked
	must(t, d.Reconcile(ctx))  // idle reads even inside the working cadence
	if f.ctxCalls != 3 {
		t.Fatalf("idle reads = %d, want one more than the working reads", f.ctxCalls)
	}
	if r, _ := s.GetCrew(ctx, "yardmaster"); r.ContextAskedAt != nil || r.ContextPct != 95 || len(f.nudges) != 0 {
		t.Fatalf("held nudge: %+v; nudges %v", r.CrewSession, f.nudges)
	}
	f.input = ""
	must(t, d.Reconcile(ctx))
	if r, _ := s.GetCrew(ctx, "yardmaster"); len(f.nudges) != 1 || r.ContextAskedAt == nil || f.ctxCalls != 4 {
		t.Fatalf("nudges %v, asked at %v, calls %d", f.nudges, r.ContextAskedAt, f.ctxCalls)
	}
}

// A context read that fails on a working pass still clears the idle spell's
// marks, and the failure is said once rather than on every wake, and again
// when a read that succeeded has ended it.
func TestContextFailureStillClearsWorkingState(t *testing.T) {
	t.Parallel()
	s, f, d := crewSetup(t, store.CrewMember{})
	ctx := context.Background()
	var log bytes.Buffer
	d.Log = slog.New(slog.NewTextHandler(&log, nil))
	now := time.Now()
	dispatch.SetClock(d, func() time.Time { return now })
	f.ctxOK, f.ctxUsed = true, 100000 // 50%: under the threshold, so the idle pass reaches the inbox
	must(t, d.Reconcile(ctx))
	agent := agentOf(t, s)
	f.dialog, f.line, f.input = "auto_mode_setup", "Enable auto mode?", "half a thought"
	must(t, s.Mail(ctx, "yardmaster", "deploy freeze", "chris"))
	must(t, d.Reconcile(ctx))
	r, err := s.GetCrew(ctx, "yardmaster")
	must(t, err)
	if n, _ := kindCount(t, s, r.Journal, "nudge_held"); n != 1 {
		t.Fatalf("nudge_held events = %d, want the dialog said once", n)
	}
	h := maps.Clone(r.Handle)
	h[dispatch.HarnessDialogKey] = "kimi"
	h[dispatch.HarnessLoginKey] = "Select login method:"
	must(t, s.SetCrewHandleFor(ctx, "yardmaster", "", h))

	f.set(agent, "working")
	f.ctxErr = errors.New("context unreadable")
	calls := f.ctxCalls
	for range 3 {
		must(t, d.Reconcile(ctx))
	}
	r, err = s.GetCrew(ctx, "yardmaster")
	must(t, err)
	if _, ok := r.Handle[dispatch.HarnessDialogKey]; ok {
		t.Fatalf("dialog mark = %v; a failed read left the idle spell's mark", r.Handle[dispatch.HarnessDialogKey])
	}
	if _, ok := r.Handle[dispatch.HarnessLoginKey]; ok {
		t.Fatalf("login mark = %v; a failed read left it", r.Handle[dispatch.HarnessLoginKey])
	}
	if !r.Live() || f.ctxCalls != calls+1 || strings.Count(log.String(), `msg="crew context"`) != 1 || strings.Count(log.String(), "level=ERROR") != 0 {
		t.Fatalf("live %v, calls %d (was %d), log %s", r.Live(), f.ctxCalls, calls, log.String())
	}
	now = now.Add(dispatch.ContextWhileWorking())
	must(t, d.Reconcile(ctx)) // due again, same error: tried, not said again
	if f.ctxCalls != calls+2 || strings.Count(log.String(), `msg="crew context"`) != 1 {
		t.Fatalf("calls %d, log %s", f.ctxCalls, log.String())
	}

	f.ctxErr = nil
	f.set(agent, "idle")
	must(t, d.Reconcile(ctx)) // the dialog memory was cleared, so the spell is told again
	if n, _ := kindCount(t, s, r.Journal, "nudge_held"); n != 2 {
		t.Fatalf("nudge_held events = %d, want the dialog said again after the working pass", n)
	}

	// The idle read that answered ended the failure said before it: the same
	// failure on a later working read is a new one and is said again.
	f.ctxErr = errors.New("context unreadable")
	f.set(agent, "working")
	now = now.Add(dispatch.ContextWhileWorking())
	must(t, d.Reconcile(ctx))
	if got := strings.Count(log.String(), `msg="crew context"`); got != 2 {
		t.Fatalf("failures said = %d, want the one after an idle read that succeeded said again; log %s", got, log.String())
	}
}

// The pass that restarts a session for its age still records the reading,
// and does not ask for a context handoff. A failed read does not hold the
// restart. The ages are read off the dispatcher's clock, which the test
// advances, so nothing waits.
func TestContextRecordedOnRotationRestart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// clock gives d a clock the test moves: each advance is a minute, far past
	// the 1ms max age and the 1ms grace the members are set with.
	clock := func(d *dispatch.Dispatcher) (advance func()) {
		now := time.Now()
		dispatch.SetClock(d, func() time.Time { return now })
		return func() { now = now.Add(time.Minute) }
	}
	s, f, d := crewSetup(t, store.CrewMember{MaxAge: "1ms", HandoffGrace: "1ms"})
	advance := clock(d)
	f.ctxOK, f.ctxUsed = true, 100000 // 50% on the passes before the restart
	must(t, d.Reconcile(ctx))
	agent := agentOf(t, s)
	advance()
	must(t, d.Reconcile(ctx)) // past max age, idle: asked for its age, not its use
	f.set(agent, "working")
	f.ctxUsed = 190000
	calls := f.ctxCalls
	advance()
	must(t, d.Reconcile(ctx)) // past the grace, still working: record, then restart
	r, err := s.GetCrew(ctx, "yardmaster")
	must(t, err)
	if r.ContextPct != 95 || r.ContextAskedAt != nil || r.RestartRequested != "rotation (no handoff within grace)" || f.ctxCalls != calls+1 {
		t.Fatalf("restart reading: %+v; calls %d (was %d)", r.CrewSession, f.ctxCalls, calls)
	}
	if len(f.nudges) != 1 || !strings.Contains(f.nudges[0], "due for rotation") {
		t.Fatalf("nudges %v, want only the rotation ask", f.nudges)
	}

	s, f, d = crewSetup(t, store.CrewMember{MaxAge: "1ms", HandoffGrace: "1ms"})
	advance = clock(d)
	f.ctxOK, f.ctxUsed = true, 100000
	must(t, d.Reconcile(ctx))
	advance()
	must(t, d.Reconcile(ctx))
	f.ctxErr = errors.New("context unreadable")
	advance()
	must(t, d.Reconcile(ctx))
	r, err = s.GetCrew(ctx, "yardmaster")
	must(t, err)
	if r.RestartRequested != "rotation (no handoff within grace)" || r.ContextAskedAt != nil {
		t.Fatalf("restart after a failed read: %+v", r.CrewSession)
	}
}

// A session already asked for its age is not asked for its use too: the
// rotation's nudge says it.
func TestContextNotAskedPastMaxAge(t *testing.T) {
	t.Parallel()
	s, f, d := crewSetup(t, store.CrewMember{MaxAge: "1ms", HandoffGrace: "1h"})
	ctx := context.Background()
	f.ctxOK, f.ctxUsed = true, 190000
	must(t, d.Reconcile(ctx))
	time.Sleep(5 * time.Millisecond)
	must(t, d.Reconcile(ctx)) // past max_age: asked for its age
	must(t, d.Reconcile(ctx)) // ... and not for its use
	if len(f.nudges) != 1 || !strings.Contains(f.nudges[0], "due for rotation") {
		t.Fatalf("nudges %v", f.nudges)
	}
	if r, _ := s.GetCrew(ctx, "yardmaster"); r.RotationAskedAt == nil || r.ContextAskedAt != nil || r.ContextPct != 95 {
		t.Fatalf("asked = %+v", r.CrewSession)
	}
}
