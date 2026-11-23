package dispatch_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/dispatch"
	"yardr.dev/yardr/internal/store"
)

// fallbackYard is a crew member at its own model's limit, ready for the
// failover's pass: started on kimi-code/k3, mail unread in its inbox, the
// pane ending in the limit line, and the provider saying only the model
// window (Weekly Fable, resetting at fable) is used up. operator jo is
// the yard's operator. The stopped session writes no handoff, as a
// session on a used-up model cannot: the restart it gets asked for
// must not wait on one.
func fallbackYard(t *testing.T, cfg map[string]any) (s store.API, f *crewRunner, d *dispatch.Dispatcher, p *standIn, now *time.Time, fable time.Time) {
	t.Helper()
	s, f, d = crewSetup(t, store.CrewMember{Config: cfg})
	ctx := context.Background()
	p = newStandIn(t, s)
	if _, err := s.AddUser(ctx, "jo", "me"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, store.OperatorSetting, "jo", "me"); err != nil {
		t.Fatal(err)
	}
	at := noon()
	now = &at
	dispatch.SetClock(d, func() time.Time { return *now })
	fable = now.Add(2 * time.Hour)
	p.say(kimiFable(0, now.Add(5*time.Hour), fable))
	must(t, d.Reconcile(ctx))
	must(t, d.Reconcile(ctx))
	f.show(kimiIdle)
	must(t, s.Mail(ctx, "yardmaster", "review yardr-1my8", "chris"))
	must(t, d.Reconcile(ctx)) // nudged
	primeLimits(t, d, "kimi", "", nil)
	f.show(kimiLimit)
	return s, f, d, p, now, fable
}

func crewMember(t *testing.T, s store.API) *store.CrewMember {
	t.Helper()
	m, err := s.GetCrew(context.Background(), "yardmaster")
	must(t, err)
	return m
}

// A crew member stopped on its own model's window, the shared windows
// open and a fallback named, is restarted on the fallback at once —
// with no handoff waited for, no prompt typed for the model window,
// and one mail — and at the window's reset it is restarted back on its
// own model (whether it writes a handoff or times out its grace), told
// once. Its config's model never changes.
func TestCrewMemberOnAModelLimitRestartsOnItsFallbackAndBack(t *testing.T) {
	t.Parallel()
	cfg := map[string]any{
		"kind": "kimi", "model": "kimi-code/k3", "fallback_model": "kimi-code/kimi-for-coding",
	}

	failOver := func(t *testing.T) (store.API, *crewRunner, *dispatch.Dispatcher, *standIn, *time.Time, time.Time) {
		t.Helper()
		s, f, d, p, now, fable := fallbackYard(t, cfg)
		ctx := context.Background()
		wantReturn := fable.Add(time.Minute).UTC().Format(time.RFC3339Nano)

		must(t, d.Reconcile(ctx)) // the stop: failover asked for, not yet carried out
		m := crewMember(t, s)
		evs := eventKinds(t, s, m.Journal, "session_harness_error")
		if len(evs) != 1 || evs[0].Data["window"] != "Weekly Fable" || evs[0].Data["fallback_model"] != "kimi-code/kimi-for-coding" ||
			!strings.Contains(evs[0].Data["hint"].(string), "restarting it on its fallback model kimi-code/kimi-for-coding") {
			t.Fatalf("session_harness_error = %v, want one naming the fallback", evs)
		}
		if m.Handle["harness_reset_at"] != nil || m.Handle["harness_fallback_model"] != "kimi-code/kimi-for-coding" ||
			m.Handle["harness_fallback_return_at"] != wantReturn {
			t.Fatalf("handle = %v, want the fallback keys (return %s) and no reset wait", m.Handle, wantReturn)
		}
		if !strings.Contains(m.RestartRequested, "fallback model kimi-code/kimi-for-coding") || f.n != 1 {
			t.Fatalf("restart %q after %d starts, want the fallback asked for and not yet started", m.RestartRequested, f.n)
		}
		mails := mailsTo(t, s, "jo")
		if len(mails) != 1 || !strings.Contains(mails[0].Data["text"].(string), "fallback model kimi-code/kimi-for-coding") {
			t.Fatalf("mail to the operator = %v, want one naming the fallback", mails)
		}

		// The stopped session wrote nothing — no handoff stands on its
		// journal — and the restart carries out all the same, from its
		// last handoff (none) and its unread inbox.
		prime, err := s.CrewPrime(ctx, "yardmaster")
		must(t, err)
		if prime.Handoff != nil {
			t.Fatalf("prime handoff = %+v, want none written by the stopped session", prime.Handoff)
		}
		f.show(kimiIdle)
		must(t, d.Reconcile(ctx))
		must(t, d.Reconcile(ctx))
		m = crewMember(t, s)
		if f.n != 2 || f.stops != 1 {
			t.Fatalf("starts %d stops %d, want the fallback session started", f.n, f.stops)
		}
		if got := f.startModels(); len(got) != 2 || got[0] != "kimi-code/k3" || got[1] != "kimi-code/kimi-for-coding" {
			t.Fatalf("start models = %v, want the primary then the fallback", got)
		}
		if m.Config["model"] != "kimi-code/k3" || m.Handle["harness_fallback_model"] != "kimi-code/kimi-for-coding" ||
			m.Handle["harness_fallback_return_at"] != wantReturn {
			t.Fatalf("member = %v handle %v, want the config's model kept and the spell on the new handle", m.Config, m.Handle)
		}
		for _, nudge := range f.nudges {
			if strings.Contains(nudge, "has reset. Go on where you stopped") {
				t.Fatalf("nudges = %q, want no reset prompt typed for the model window", f.nudges)
			}
		}
		if mails := mailsTo(t, s, "jo"); len(mails) != 1 {
			t.Fatalf("mail after the restart = %d, want the failover's one", len(mails))
		}
		return s, f, d, p, now, fable
	}

	assertReturned := func(t *testing.T, s store.API, f *crewRunner, d *dispatch.Dispatcher) {
		t.Helper()
		ctx := context.Background()
		must(t, d.Reconcile(ctx))
		must(t, d.Reconcile(ctx))
		m := crewMember(t, s)
		if f.n != 3 {
			t.Fatalf("starts %d, want the session back on its primary", f.n)
		}
		if got := f.startModels(); len(got) != 3 || got[2] != "kimi-code/k3" {
			t.Fatalf("start models = %v, want the third back on the primary", got)
		}
		if m.Handle["harness_fallback_model"] != nil || m.Handle["harness_fallback_return_at"] != nil {
			t.Fatalf("handle = %v, want the spell cleared on the primary session", m.Handle)
		}
		mails := mailsTo(t, s, "jo")
		if len(mails) != 2 || !strings.Contains(mails[1].Data["text"].(string), "returning to the model kimi-code/k3") {
			t.Fatalf("mail = %v, want the failover's and the return's, once each", mails)
		}
		if tells := journalTells(t, s, m.Journal); len(tells) != 2 {
			t.Fatalf("human_notified = %d, want the two tells", len(tells))
		}
		must(t, d.Reconcile(ctx))
		if f.n != 3 || len(mailsTo(t, s, "jo")) != 2 {
			t.Fatalf("after the return: starts %d, mail %d; want it done once", f.n, len(mailsTo(t, s, "jo")))
		}
	}

	t.Run("returns after handoff", func(t *testing.T) {
		t.Parallel()
		s, f, d, _, now, fable := failOver(t)
		ctx := context.Background()
		// Before reset + lag, the session stays on its fallback.
		*now = fable.Add(30 * time.Second)
		must(t, d.Reconcile(ctx))
		if f.n != 2 || crewMember(t, s).RotationAskedAt != nil {
			t.Fatalf("before reset lag: starts %d rotation_asked %v, want still on fallback", f.n, crewMember(t, s).RotationAskedAt)
		}
		// At reset + lag, the fallback session is asked for its handoff and writes one.
		*now = fable.Add(2 * time.Minute)
		must(t, d.Reconcile(ctx))
		asked := false
		for _, nudge := range f.nudges {
			asked = asked || strings.Contains(nudge, "Write your handoff now: yardr crew handoff yardmaster")
		}
		if !asked || f.n != 2 {
			t.Fatalf("at the reset: nudges %q starts %d, want the handoff asked for and no restart yet", f.nudges, f.n)
		}
		if _, err := s.Journal(ctx, "yardmaster", store.NoteHandoff, "done on fallback, handing off to primary", "yardmaster"); err != nil {
			t.Fatal(err)
		}
		assertReturned(t, s, f, d)
	})

	t.Run("returns after grace without handoff", func(t *testing.T) {
		t.Parallel()
		s, f, d, _, now, fable := failOver(t)
		ctx := context.Background()
		*now = fable.Add(2 * time.Minute)
		must(t, d.Reconcile(ctx))
		must(t, d.Reconcile(ctx)) // the grace (the store's asked-at against this clock) is past: return asked for
		m := crewMember(t, s)
		if !strings.Contains(m.RestartRequested, "returning from fallback model") || f.n != 2 {
			t.Fatalf("restart %q after %d starts, want the return asked for", m.RestartRequested, f.n)
		}
		assertReturned(t, s, f, d)
	})

	t.Run("does not double-failover while on fallback", func(t *testing.T) {
		t.Parallel()
		s, f, d, _, _, _ := failOver(t)
		ctx := context.Background()
		must(t, s.Mail(ctx, "yardmaster", "second mail on fallback", "chris"))
		must(t, d.Reconcile(ctx)) // nudged
		primeLimits(t, d, "kimi", "", nil)
		f.show(kimiLimit)
		must(t, d.Reconcile(ctx))
		m := crewMember(t, s)
		if m.RestartRequested != "" || f.n != 2 || len(mailsTo(t, s, "jo")) != 1 {
			t.Fatalf("while on fallback: restart %q starts %d mails %d, want no second failover", m.RestartRequested, f.n, len(mailsTo(t, s, "jo")))
		}
	})

	t.Run("past reset still fails over", func(t *testing.T) {
		t.Parallel()
		s, f, d, p, now, _ := fallbackYard(t, cfg)
		ctx := context.Background()
		*now = now.Add(2 * time.Minute) // expire the primed limits answer
		p.say(kimiFable(0, now.Add(5*time.Hour), now.Add(-30*time.Second)))
		primeLimits(t, d, "kimi", "", nil)
		must(t, d.Reconcile(ctx)) // failover asked for with return at now + lag
		wantReturn := now.Add(time.Minute).UTC().Format(time.RFC3339Nano)
		if m := crewMember(t, s); m.Handle["harness_fallback_return_at"] != wantReturn {
			t.Fatalf("handle = %v, want return at now + lag (%s)", m.Handle, wantReturn)
		}
		f.show(kimiIdle)
		must(t, d.Reconcile(ctx))
		must(t, d.Reconcile(ctx))
		if got := f.startModels(); len(got) != 2 || got[1] != "kimi-code/kimi-for-coding" {
			t.Fatalf("start models = %v, want fallback started even with a past provider reset", got)
		}
	})

	t.Run("unknown reset fails over and operator restart returns", func(t *testing.T) {
		t.Parallel()
		s, f, d, p, now, _ := fallbackYard(t, cfg)
		ctx := context.Background()
		*now = now.Add(2 * time.Minute) // expire the primed limits answer
		p.say(`{"provider":"Kimi","windows":[{"label":"5h","used_percent":0},{"label":"Weekly Fable","model":"Fable","used_percent":100}]}`)
		primeLimits(t, d, "kimi", "", nil)
		must(t, d.Reconcile(ctx))
		mails := mailsTo(t, s, "jo")
		if len(mails) != 1 || !strings.Contains(mails[0].Data["text"].(string), "yardr crew restart yardmaster") {
			t.Fatalf("mail = %v, want failover mail naming restart command when reset time is unknown", mails)
		}
		f.show(kimiIdle)
		must(t, d.Reconcile(ctx))
		must(t, d.Reconcile(ctx))
		if got := f.startModels(); len(got) != 2 || got[1] != "kimi-code/kimi-for-coding" {
			t.Fatalf("start models = %v, want fallback started without a reset time", got)
		}
		*now = now.Add(time.Hour)
		must(t, s.RequestRestart(ctx, "yardmaster", "operator return", "", "jo"))
		must(t, d.Reconcile(ctx))
		must(t, d.Reconcile(ctx))
		if got := f.startModels(); len(got) != 3 || got[2] != "kimi-code/k3" {
			t.Fatalf("start models = %v, want operator restart to return to primary", got)
		}
		if m := crewMember(t, s); m.Handle["harness_fallback_model"] != nil {
			t.Fatalf("handle = %v, want fallback cleared after return", m.Handle)
		}
	})
}

// With no fallback named a model-window stop waits as it always has —
// the reset wake on its handle, no restart — and the operator is told
// once what would have rescued it.
func TestCrewMemberOnAModelLimitWithoutAFallbackWaitsAndIsToldOnce(t *testing.T) {
	t.Parallel()
	s, f, d, _, _, fable := fallbackYard(t, map[string]any{"kind": "kimi", "model": "kimi-code/k3"})
	ctx := context.Background()

	must(t, d.Reconcile(ctx))
	must(t, d.Reconcile(ctx))
	m := crewMember(t, s)
	evs := eventKinds(t, s, m.Journal, "session_harness_error")
	if len(evs) != 1 || evs[0].Data["fallback_model"] != nil {
		t.Fatalf("session_harness_error = %v, want one with no fallback named", evs)
	}
	if m.Handle["harness_reset_at"] != fable.Add(time.Minute).UTC().Format(time.RFC3339Nano) ||
		m.Handle["harness_fallback_model"] != nil || m.RestartRequested != "" || f.n != 1 {
		t.Fatalf("handle %v restart %q starts %d, want the reset wait as before and no restart", m.Handle, m.RestartRequested, f.n)
	}
	mails := mailsTo(t, s, "jo")
	if len(mails) != 1 || !strings.Contains(mails[0].Data["text"].(string), "yardr crew set yardmaster fallback_model=") {
		t.Fatalf("mail = %v, want one naming fallback_model", mails)
	}
	must(t, d.Reconcile(ctx))
	if mails := mailsTo(t, s, "jo"); len(mails) != 1 || f.n != 1 {
		t.Fatalf("a pass on: mail %d starts %d, want the tell once and still waiting", len(mails), f.n)
	}
}

// A shared window used up is no failover, fallback named or not: the
// stop waits for its reset as before, and nobody is mailed.
func TestCrewMemberOnASharedLimitDoesNotFailOver(t *testing.T) {
	t.Parallel()
	s, f, d := crewSetup(t, store.CrewMember{Config: map[string]any{
		"kind": "kimi", "model": "kimi-code/k3", "fallback_model": "kimi-code/kimi-for-coding"}})
	ctx := context.Background()
	p := newStandIn(t, s)
	if _, err := s.AddUser(ctx, "jo", "me"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, store.OperatorSetting, "jo", "me"); err != nil {
		t.Fatal(err)
	}
	now := noon()
	dispatch.SetClock(d, func() time.Time { return now })
	p.say(kimiUsed(now.Add(42 * time.Minute)))
	must(t, d.Reconcile(ctx))
	must(t, d.Reconcile(ctx))
	f.show(kimiIdle)
	must(t, s.Mail(ctx, "yardmaster", "review yardr-1my8", "chris"))
	must(t, d.Reconcile(ctx))
	primeLimits(t, d, "kimi", "", nil)
	f.show(kimiLimit)
	must(t, d.Reconcile(ctx))
	must(t, d.Reconcile(ctx))

	m := crewMember(t, s)
	evs := eventKinds(t, s, m.Journal, "session_harness_error")
	if len(evs) != 1 || evs[0].Data["window"] != "5h" || evs[0].Data["fallback_model"] != nil {
		t.Fatalf("session_harness_error = %v, want the shared window's, with no fallback", evs)
	}
	if m.Handle["harness_reset_at"] == nil || m.Handle["harness_fallback_model"] != nil || m.RestartRequested != "" || f.n != 1 {
		t.Fatalf("handle %v restart %q starts %d, want the reset wait and no failover", m.Handle, m.RestartRequested, f.n)
	}
	if mails := mailsTo(t, s, "jo"); len(mails) != 0 {
		t.Fatalf("mail = %v, want none for a shared window", mails)
	}
}

// A crew member that starts while its own model's window is already
// used up — a handoff restart's start among them, with no fallback
// keys on the session it replaces and no pane error to prove the
// model — starts on its fallback at once, with the return stamped
// and the operator told once as a start. No other start does: an
// open window, a shared window used up, a window of another model,
// and limits that are cold or unknown all start it on its own model
// with no failover and no tell, leaving a later stop to the stop
// path. No test above drives a start with no fallback keys, so this
// one is new beside them.
func TestCrewMemberStartingWhileItsModelsWindowIsUsedUpStartsOnItsFallback(t *testing.T) {
	t.Parallel()
	const (
		primary  = "kimi-fable-5"
		fallback = "kimi-code/kimi-for-coding"
	)
	cfg := map[string]any{"kind": "kimi", "model": primary, "fallback_model": fallback}

	// start is one fresh yard whose member's first start is judged
	// against report: primed into the cache where prime holds, failed
	// where fail holds, and left cold where neither holds.
	start := func(t *testing.T, report func(now, fable time.Time) string, prime, fail bool) (store.API, *crewRunner, *dispatch.Dispatcher, time.Time) {
		t.Helper()
		s, f, d := crewSetup(t, store.CrewMember{Config: cfg})
		ctx := context.Background()
		p := newStandIn(t, s)
		if _, err := s.AddUser(ctx, "jo", "me"); err != nil {
			t.Fatal(err)
		}
		if err := s.SetSetting(ctx, store.OperatorSetting, "jo", "me"); err != nil {
			t.Fatal(err)
		}
		now := noon()
		dispatch.SetClock(d, func() time.Time { return now })
		fable := now.Add(2 * time.Hour)
		p.say(report(now, fable))
		if fail {
			p.fail()
		}
		if prime {
			primeLimits(t, d, "kimi", "", nil)
		}
		must(t, d.Reconcile(ctx))
		must(t, d.Reconcile(ctx))
		return s, f, d, fable
	}

	assertOnPrimary := func(t *testing.T, s store.API, f *crewRunner) {
		t.Helper()
		m := crewMember(t, s)
		if got := f.startModels(); len(got) != 1 || got[0] != primary || f.n != 1 {
			t.Fatalf("start models = %v starts %d, want the one start on %s", got, f.n, primary)
		}
		if m.Config["model"] != primary || m.Handle["harness_fallback_model"] != nil || m.Handle["harness_fallback_return_at"] != nil {
			t.Fatalf("member = %v handle %v, want the config's model and no fallback keys", m.Config, m.Handle)
		}
		if mails := mailsTo(t, s, "jo"); len(mails) != 0 {
			t.Fatalf("mail = %v, want none", mails)
		}
		if tells := journalTells(t, s, m.Journal); len(tells) != 0 {
			t.Fatalf("human_notified = %v, want none", tells)
		}
	}

	t.Run("used up model window starts on fallback", func(t *testing.T) {
		t.Parallel()
		s, f, d, fable := start(t, func(now, fable time.Time) string {
			return kimiFable(0, now.Add(5*time.Hour), fable)
		}, true, false)
		ctx := context.Background()
		wantReturn := fable.Add(time.Minute).UTC().Format(time.RFC3339Nano)
		m := crewMember(t, s)
		if got := f.startModels(); len(got) != 1 || got[0] != fallback || f.n != 1 {
			t.Fatalf("start models = %v starts %d, want the one start on the fallback %s", got, f.n, fallback)
		}
		if m.Config["model"] != primary || m.Handle["harness_fallback_model"] != fallback ||
			m.Handle["harness_fallback_return_at"] != wantReturn {
			t.Fatalf("member = %v handle %v, want the config's model kept, the fallback and its return %s", m.Config, m.Handle, wantReturn)
		}
		mails := mailsTo(t, s, "jo")
		if len(mails) != 1 || !strings.Contains(mails[0].Data["text"].(string), "starting on its fallback model "+fallback) {
			t.Fatalf("mail to the operator = %v, want one naming the start on the fallback", mails)
		}
		tells := journalTells(t, s, m.Journal)
		if len(tells) != 1 || tells[0].Data["fallback"] != "start" || tells[0].Data["fallback_model"] != fallback ||
			tells[0].Data["window"] != "Weekly Fable" {
			t.Fatalf("human_notified = %v, want one start tell naming the fallback and its window", tells)
		}
		must(t, d.Reconcile(ctx))
		if f.n != 1 || len(mailsTo(t, s, "jo")) != 1 {
			t.Fatalf("a pass on: starts %d mail %d, want the start told once", f.n, len(mailsTo(t, s, "jo")))
		}
	})

	t.Run("handoff restart starts on fallback", func(t *testing.T) {
		t.Parallel()
		// Started on its own model while its window was open, then
		// restarted for a handoff once the provider says the window
		// is used up: the replacing start carries no fallback keys
		// and fails over all the same.
		s, f, d := crewSetup(t, store.CrewMember{Config: cfg})
		ctx := context.Background()
		p := newStandIn(t, s)
		if _, err := s.AddUser(ctx, "jo", "me"); err != nil {
			t.Fatal(err)
		}
		if err := s.SetSetting(ctx, store.OperatorSetting, "jo", "me"); err != nil {
			t.Fatal(err)
		}
		now := noon()
		dispatch.SetClock(d, func() time.Time { return now })
		p.say(kimiOpen)
		primeLimits(t, d, "kimi", "", nil)
		must(t, d.Reconcile(ctx))
		must(t, d.Reconcile(ctx))
		if got := f.startModels(); len(got) != 1 || got[0] != primary {
			t.Fatalf("start models = %v, want the first start on %s", got, primary)
		}
		fable := now.Add(2 * time.Hour)
		now = now.Add(2 * time.Minute) // expire the primed open answer
		p.say(kimiFable(0, now.Add(5*time.Hour), fable))
		primeLimits(t, d, "kimi", "", nil)
		must(t, s.RequestRestart(ctx, "yardmaster", "handoff", "", "jo"))
		f.show(kimiIdle)
		must(t, d.Reconcile(ctx))
		must(t, d.Reconcile(ctx))
		m := crewMember(t, s)
		wantReturn := fable.Add(time.Minute).UTC().Format(time.RFC3339Nano)
		if got := f.startModels(); len(got) != 2 || got[1] != fallback {
			t.Fatalf("start models = %v, want the restart on the fallback %s", got, fallback)
		}
		if m.Handle["harness_fallback_model"] != fallback || m.Handle["harness_fallback_return_at"] != wantReturn {
			t.Fatalf("handle = %v, want the fallback and its return %s", m.Handle, wantReturn)
		}
		if mails := mailsTo(t, s, "jo"); len(mails) != 1 {
			t.Fatalf("mail = %d, want the start's one", len(mails))
		}
	})

	t.Run("open window starts on primary", func(t *testing.T) {
		t.Parallel()
		s, f, _, _ := start(t, func(_, _ time.Time) string { return kimiOpen }, true, false)
		assertOnPrimary(t, s, f)
	})

	t.Run("shared window used up starts on primary", func(t *testing.T) {
		t.Parallel()
		s, f, _, _ := start(t, func(now, _ time.Time) string {
			return kimiUsed(now.Add(42 * time.Minute))
		}, true, false)
		assertOnPrimary(t, s, f)
	})

	t.Run("another model's window starts on primary", func(t *testing.T) {
		t.Parallel()
		s, f, _, _ := start(t, func(now, fable time.Time) string {
			return fmt.Sprintf(`{"provider":"Kimi","windows":[`+
				`{"label":"5h","used_percent":0,"resets_at":%q},`+
				`{"label":"Weekly Opus","model":"Opus","used_percent":100,"resets_at":%q}]}`,
				now.Add(5*time.Hour).Format(time.RFC3339), fable.Format(time.RFC3339))
		}, true, false)
		assertOnPrimary(t, s, f)
	})

	t.Run("cold limits start on primary", func(t *testing.T) {
		t.Parallel()
		s, f, _, _ := start(t, func(now, fable time.Time) string {
			return kimiFable(0, now.Add(5*time.Hour), fable)
		}, false, false)
		assertOnPrimary(t, s, f)
	})

	t.Run("unknown limits start on primary", func(t *testing.T) {
		t.Parallel()
		s, f, _, _ := start(t, func(now, fable time.Time) string {
			return kimiFable(0, now.Add(5*time.Hour), fable)
		}, true, true)
		assertOnPrimary(t, s, f)
	})
}
