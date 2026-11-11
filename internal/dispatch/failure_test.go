package dispatch_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"yardr.dev/yardr/internal/dispatch"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
)

// downRunner fails every Start as its runtime would when down, until up.
type downRunner struct {
	*fakeRunner
	mu       sync.Mutex
	up       bool
	attempts int
}

type beadBlockedRunner struct{ *fakeRunner }

func (r *beadBlockedRunner) Start(ctx context.Context, j dispatch.Job) (map[string]any, error) {
	return map[string]any{"session": j.Session.ID, "worktree": "/tmp/partial"},
		dispatch.BeadBlocked(errors.New("pull request https://github.com/acme/web/pull/1: refs/pull/1/head is gone"))
}

func (r *downRunner) Start(ctx context.Context, j dispatch.Job) (map[string]any, error) {
	r.mu.Lock()
	r.attempts++
	up := r.up
	r.mu.Unlock()
	if !up {
		return nil, dispatch.Runtime(errors.New("herdr: executable file not found in $PATH"))
	}
	return r.fakeRunner.Start(ctx, j)
}

func (r *downRunner) tries() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.attempts
}

// groupEvents counts events of kind per group (bead-less events carry the group
// in their data).
func groupEvents(t *testing.T, s store.API, kind string) map[string]int {
	t.Helper()
	evs, err := s.EventsSince(context.Background(), 0, 0)
	must(t, err)
	n := map[string]int{}
	for _, e := range evs {
		if e.Kind == kind {
			n[fmt.Sprint(e.Data["group"])]++
		}
	}
	return n
}

// With the runtime down, no bead counts a failure or gets held, however many
// passes run, and each group reports runtime_down once; once it is back the
// beads start and runtime_up says so.
func TestRuntimeDownHoldsNoBead(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	r := &downRunner{fakeRunner: &fakeRunner{dead: map[string]bool{}}}
	d := &dispatch.Dispatcher{Store: s, Runners: map[string]dispatch.Runner{"fake": r},
		Backoff: time.Nanosecond, RuntimeBackoff: time.Nanosecond, MaxAttempts: 2}
	ctx := context.Background()
	for _, g := range []string{"builders", "reviewers"} {
		must(t, s.PutGroup(ctx, store.Group{Name: g, Runner: "fake", Limit: 2}, "me"))
	}
	_, _ = s.AddRoute(ctx, store.Route{Stage: "new", Group: "builders"}, "me")
	_, _ = s.AddRoute(ctx, store.Route{Stage: "review", Group: "reviewers"}, "me")
	var beads []*store.Bead
	for i, stage := range []string{"new", "new", "new", "review"} {
		b, err := s.CreateBead(ctx, store.NewBead{Title: fmt.Sprint("b", i), Stage: stage}, "me")
		must(t, err)
		beads = append(beads, b)
	}

	for range 6 {
		must(t, d.Reconcile(ctx))
		time.Sleep(time.Millisecond)
	}
	if r.tries() < 6 {
		t.Fatalf("starts tried = %d; the outage was not retried", r.tries())
	}
	for _, b := range beads {
		got, _ := s.GetBead(ctx, b.ID)
		if got.Hold || got.Group != nil {
			t.Fatalf("bead %s touched by the outage: hold=%v group=%v", b.ID, got.Hold, got.Group)
		}
		if n, _ := s.FailureCount(ctx, b.ID, b.Stage); n != 0 {
			t.Fatalf("bead %s has %d failures from the outage", b.ID, n)
		}
	}
	if got := groupEvents(t, s, "runtime_down"); got["builders"] != 1 || got["reviewers"] != 1 || len(got) != 2 {
		t.Fatalf("runtime_down per group = %v, want one each", got)
	}

	r.mu.Lock()
	r.up = true
	r.mu.Unlock()
	time.Sleep(time.Millisecond)
	// One start per group per pass (starts.go): builders' two beads take
	// two passes, the reviewers' one the first of them.
	must(t, d.Reconcile(ctx))
	must(t, d.Reconcile(ctx))
	if n := len(r.starts()); n != 3 { // builders' limit 2, reviewers 1
		t.Fatalf("starts after recovery = %d, want 3", n)
	}
	if got := groupEvents(t, s, "runtime_up"); got["builders"] != 1 || got["reviewers"] != 1 {
		t.Fatalf("runtime_up per group = %v", got)
	}
}

// A start failure scoped to one bead is reported and held at once, without
// charging a failed attempt or marking the whole group down.
func TestBeadBlockedStartHoldsBeadWithReason(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	r := &beadBlockedRunner{fakeRunner: &fakeRunner{dead: map[string]bool{}}}
	d := &dispatch.Dispatcher{Store: s, Runners: map[string]dispatch.Runner{"fake": r},
		Backoff: time.Nanosecond, RuntimeBackoff: time.Nanosecond, MaxAttempts: 1}
	ctx := context.Background()
	must(t, s.PutGroup(ctx, store.Group{Name: "builders", Runner: "fake", Limit: 1}, "me"))
	_, err := s.AddRoute(ctx, store.Route{Stage: "new", Group: "builders"}, "me")
	must(t, err)
	b, err := s.CreateBead(ctx, store.NewBead{Title: "review PR"}, "me")
	must(t, err)
	must(t, d.Reconcile(ctx))

	got, err := s.GetBead(ctx, b.ID)
	must(t, err)
	if !got.Hold || got.HeldBy != "yard" {
		t.Fatalf("bead not held by the yard after blocked start: %+v", got)
	}
	if n, err := s.FailureCount(ctx, b.ID, b.Stage); err != nil || n != 0 {
		t.Fatalf("failure count = %d, %v; want 0", n, err)
	}
	blocked := eventKinds(t, s, b.ID, "session_blocked")
	if len(blocked) != 1 || !strings.Contains(fmt.Sprint(blocked[0].Data["reason"]), "refs/pull/1/head is gone") {
		t.Fatalf("session_blocked = %v; want one event with the git error", blocked)
	}
	if down := groupEvents(t, s, "runtime_down"); len(down) != 0 {
		t.Fatalf("bead-specific failure marked the group down: %v", down)
	}
}

// While a group's runtime is down its beads are not tried again before the
// backoff ends; an error of the bead's own still counts toward the hold.
// Each bead that waits for the down runtime gets one group_wait event
// naming the group, and none again on the next pass.
func TestRuntimeDownBacksOff(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	r := &downRunner{fakeRunner: &fakeRunner{dead: map[string]bool{}}}
	d := &dispatch.Dispatcher{Store: s, Runners: map[string]dispatch.Runner{"fake": r},
		Backoff: time.Nanosecond, RuntimeBackoff: time.Hour, MaxAttempts: 2}
	ctx := context.Background()
	must(t, s.PutGroup(ctx, store.Group{Name: "g", Runner: "fake", Limit: 3}, "me"))
	_, _ = s.AddRoute(ctx, store.Route{Stage: "new", Group: "g"}, "me")
	var ids []string
	for i := range 3 {
		b, err := s.CreateBead(ctx, store.NewBead{Title: fmt.Sprint("b", i)}, "me")
		must(t, err)
		ids = append(ids, b.ID)
	}
	must(t, d.Reconcile(ctx))
	must(t, d.Reconcile(ctx))
	if n := r.tries(); n != 1 {
		t.Fatalf("starts tried = %d, want 1: the pass should stop at the first outage and back off", n)
	}
	must(t, d.Reconcile(ctx))
	for i, id := range ids {
		evs := eventKinds(t, s, id, "group_wait")
		if len(evs) != 1 {
			t.Fatalf("group_wait events of %s = %d, want 1 over three passes", id, len(evs))
		}
		// The bead whose start failed is told of the outage the next
		// pass records; the beads behind it were first told their group
		// was starting a session, since the outage is only recognised
		// when that start's completion is drained (starts.go). Each
		// bead's one group_wait keeps the first reason it was told.
		want := "runtime is down"
		if i > 0 {
			want = "starting a session"
		}
		if reason, _ := evs[0].Data["reason"].(string); !strings.Contains(reason, want) {
			t.Fatalf("group_wait reason of %s = %q, want %q", id, reason, want)
		}
	}

	// A bead-specific failure is unchanged: it counts, and holds.
	s2, f, d2 := setup(t)
	d2.MaxAttempts = 1
	must(t, s2.PutGroup(ctx, store.Group{Name: "g", Runner: "fake"}, "me"))
	_, _ = s2.AddRoute(ctx, store.Route{Stage: "new", Group: "g"}, "me")
	b, _ := s2.CreateBead(ctx, store.NewBead{Title: "bad brief"}, "me")
	f.failNext = errors.New("group g prompt: no such file")
	must(t, d2.Reconcile(ctx))
	time.Sleep(time.Millisecond)
	must(t, d2.Reconcile(ctx))
	if got, _ := s2.GetBead(ctx, b.ID); !got.Hold {
		t.Fatalf("bead-specific failure did not hold: %+v", got)
	}
	if got := groupEvents(t, s2, "runtime_down"); len(got) != 0 {
		t.Fatalf("bead failure reported as runtime_down: %v", got)
	}
}

// Holding a bead ends its session and frees the group's slot for the next
// bead; unholding routes it again, with no failure counted.
func TestHoldFreesTheSlot(t *testing.T) {
	t.Parallel()
	s, f, d := setup(t)
	ctx := context.Background()
	must(t, s.PutGroup(ctx, store.Group{Name: "g", Runner: "fake", Limit: 1}, "me"))
	_, _ = s.AddRoute(ctx, store.Route{Stage: "new", Group: "g"}, "me")
	a, _ := s.CreateBead(ctx, store.NewBead{Title: "a", Priority: 1}, "me")
	b, _ := s.CreateBead(ctx, store.NewBead{Title: "b", Priority: 2}, "me")

	must(t, d.Reconcile(ctx))
	if got := f.starts(); !slices.Equal(got, []string{a.ID}) {
		t.Fatalf("starts = %v, want only %s (limit 1)", got, a.ID)
	}
	held, _ := s.GetBead(ctx, a.ID)
	sess := *held.Session

	// The agent holds its own bead, from its own session.
	_, err := s.SetHold(ctx, a.ID, true, store.Expect{Session: sess}, "agent")
	must(t, err)
	held, _ = s.GetBead(ctx, a.ID)
	if held.Group != nil || held.Session != nil {
		t.Fatalf("held bead still owned: %+v", held)
	}
	if got, _ := s.GetSession(ctx, sess); got.State != "held" {
		t.Fatalf("session state = %s, want held", got.State)
	}
	live, _ := s.Sessions(ctx, false)
	if len(live) != 0 {
		t.Fatalf("live sessions after hold = %d, want 0", len(live))
	}

	must(t, d.Reconcile(ctx))
	if !slices.Contains(f.stopped, sess) {
		t.Fatalf("held session not stopped: %v", f.stopped)
	}
	if got := f.starts(); !slices.Equal(got, []string{a.ID, b.ID}) {
		t.Fatalf("starts = %v; the freed slot did not go to %s", got, b.ID)
	}

	_, err = s.CloseBead(ctx, b.ID, "done", store.Expect{}, "me")
	must(t, err)
	_, err = s.SetHold(ctx, a.ID, false, store.Expect{}, "yardmaster")
	must(t, err)
	must(t, d.Reconcile(ctx))
	if got := f.starts(); !slices.Equal(got, []string{a.ID, b.ID, a.ID}) {
		t.Fatalf("starts = %v; unhold did not route %s again", got, a.ID)
	}
	if n, _ := s.FailureCount(ctx, a.ID, "new"); n != 0 {
		t.Fatalf("hold counted as %d failures", n)
	}
}

func eventKinds(t *testing.T, s store.API, bead, kind string) []store.Event {
	t.Helper()
	evs, err := s.Events(context.Background(), bead, 1000)
	must(t, err)
	var out []store.Event
	for _, e := range evs {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// An agent idle past its group's limit raises session_idle once per idle
// spell; with a grace set it is then stopped.
func TestIdleSessionIsReported(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	r := &statusRunner{fakeRunner: &fakeRunner{dead: map[string]bool{}}, status: "working"}
	d := &dispatch.Dispatcher{Store: s, Runners: map[string]dispatch.Runner{"fake": r}, Backoff: time.Hour}
	ctx := context.Background()
	must(t, s.PutGroup(ctx, store.Group{Name: "g", Runner: "fake", Config: map[string]any{"idle": "30ms"}}, "me"))
	_, _ = s.AddRoute(ctx, store.Route{Stage: "new", Group: "g"}, "me")
	b, _ := s.CreateBead(ctx, store.NewBead{Title: "x"}, "me")
	set := func(st string) {
		r.mu.Lock()
		r.status = st
		r.mu.Unlock()
	}

	must(t, d.Reconcile(ctx)) // start
	set("idle")
	must(t, d.Reconcile(ctx)) // idle from here
	if n := len(eventKinds(t, s, b.ID, "session_idle")); n != 0 {
		t.Fatalf("session_idle before the limit: %d", n)
	}
	time.Sleep(40 * time.Millisecond)
	must(t, d.Reconcile(ctx))
	must(t, d.Reconcile(ctx))
	evs := eventKinds(t, s, b.ID, "session_idle")
	if len(evs) != 1 {
		t.Fatalf("session_idle events = %d, want 1", len(evs))
	}
	got, _ := s.GetBead(ctx, b.ID)
	if evs[0].Data["session"] != *got.Session || evs[0].Data["group"] != "g" {
		t.Fatalf("session_idle data = %v", evs[0].Data)
	}

	// Working again ends the spell; the next one is reported again.
	set("working")
	must(t, d.Reconcile(ctx))
	set("done")
	must(t, d.Reconcile(ctx))
	time.Sleep(40 * time.Millisecond)
	must(t, d.Reconcile(ctx))
	if n := len(eventKinds(t, s, b.ID, "session_idle")); n != 2 {
		t.Fatalf("session_idle events = %d, want 2", n)
	}
	if got, _ := s.GetBead(ctx, b.ID); got.Session == nil {
		t.Fatal("session stopped without a grace configured")
	}

	// With a grace, a session idle past limit and grace is stopped.
	must(t, s.PutGroup(ctx, store.Group{Name: "g", Runner: "fake",
		Config: map[string]any{"idle": "30ms", "grace": "0"}}, "me"))
	must(t, d.Reconcile(ctx))
	got, _ = s.GetBead(ctx, b.ID)
	if got.Session != nil {
		t.Fatalf("idle session not stopped after grace: %+v", got)
	}
	if n := len(eventKinds(t, s, b.ID, "session_failed")); n != 1 {
		t.Fatalf("session_failed events = %d", n)
	}
}

// A session running past its group's max_age raises session_aged once, even
// across daemon restarts; a status-less runner gets no limit unless set.
func TestAgedSessionIsReportedOnce(t *testing.T) {
	t.Parallel()
	s, f, d := setup(t)
	ctx := context.Background()
	// The hard cap without a grace is twice max_age (4s here): the three
	// passes below must all run before it, and under -race with the rest
	// of the suite alongside one pass has taken 300ms. The long backoff
	// makes a cap that fires early fail loudly (no session) instead of
	// passing on the fresh session the bead would otherwise restart on.
	d.Backoff = time.Hour
	must(t, s.PutGroup(ctx, store.Group{Name: "g", Runner: "fake", Config: map[string]any{"max_age": "2s"}}, "me"))
	must(t, s.PutGroup(ctx, store.Group{Name: "plain", Runner: "fake"}, "me"))
	_, _ = s.AddRoute(ctx, store.Route{Stage: "new", Group: "g"}, "me")
	_, _ = s.AddRoute(ctx, store.Route{Stage: "other", Group: "plain"}, "me")
	b, _ := s.CreateBead(ctx, store.NewBead{Title: "long"}, "me")
	p, _ := s.CreateBead(ctx, store.NewBead{Title: "plain", Stage: "other"}, "me")

	must(t, d.Reconcile(ctx))
	time.Sleep(2050 * time.Millisecond)
	must(t, d.Reconcile(ctx))
	must(t, d.Reconcile(ctx))
	// A new daemon (no memory of the first) does not tell again.
	d2 := &dispatch.Dispatcher{Store: s, Runners: map[string]dispatch.Runner{"fake": f}}
	must(t, d2.Reconcile(ctx))
	aged := eventKinds(t, s, b.ID, "session_aged")
	if len(aged) != 1 {
		t.Fatalf("session_aged events = %d, want 1", len(aged))
	}
	if aged[0].Data["stop_after"] != "2s" {
		t.Fatalf("session_aged without a grace must say when the cap stops it: %v", aged[0].Data)
	}
	if n := len(eventKinds(t, s, p.ID, "session_aged")); n != 0 {
		t.Fatalf("status-less runner got the default age limit")
	}
	got, _ := s.GetBead(ctx, b.ID)
	if got.Session == nil {
		t.Fatal("aged session stopped without a grace, before the cap")
	}
	sess := *got.Session

	// With a grace it is stopped as failed.
	must(t, s.PutGroup(ctx, store.Group{Name: "g", Runner: "fake",
		Config: map[string]any{"max_age": "2s", "grace": "1ms"}}, "me"))
	must(t, d.Reconcile(ctx))
	if st, _ := s.GetSession(ctx, sess); st.State != "failed" {
		t.Fatalf("aged session state = %s after grace, want failed", st.State)
	}
}

// With no grace set, a session that has run twice its max_age is stopped
// anyway (failed, so repeats still hold the bead), and the bead is released
// with a note saying why, so the next agent and the yardmaster see it.
func TestAgedSessionIsCappedWithoutGrace(t *testing.T) {
	t.Parallel()
	s, _, d := setup(t)
	d.Backoff = time.Hour // keep the bead released, not restarted, for the checks
	ctx := context.Background()
	must(t, s.PutGroup(ctx, store.Group{Name: "g", Runner: "fake", Config: map[string]any{"max_age": "20ms"}}, "me"))
	_, _ = s.AddRoute(ctx, store.Route{Stage: "new", Group: "g"}, "me")
	b, _ := s.CreateBead(ctx, store.NewBead{Title: "long"}, "me")

	must(t, d.Reconcile(ctx))
	got, _ := s.GetBead(ctx, b.ID)
	sess := *got.Session
	time.Sleep(50 * time.Millisecond)
	must(t, d.Reconcile(ctx))

	st, err := s.GetSession(ctx, sess)
	must(t, err)
	if st.State != "failed" || st.Error == nil || !strings.Contains(*st.Error, "hard cap") {
		t.Fatalf("capped session = %s %v, want failed at the hard cap", st.State, st.Error)
	}
	got, _ = s.GetBead(ctx, b.ID)
	if got.Session != nil || got.Group != nil || got.Hold {
		t.Fatalf("bead not released: %+v", got)
	}
	notes, err := s.Notes(ctx, b.ID)
	must(t, err)
	var found bool
	for _, n := range notes {
		if strings.Contains(n.Text, sess) && strings.Contains(n.Text, "max_age") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no note on the bead names the capped session: %+v", notes)
	}
	if n := len(eventKinds(t, s, b.ID, "session_aged")); n != 1 {
		t.Fatalf("session_aged events = %d, want 1", n)
	}
}

// blockedRunner refuses every Start as the herdr runner does when its
// group's env_file is refused.
type blockedRunner struct{ *fakeRunner }

func (r *blockedRunner) Start(context.Context, dispatch.Job) (map[string]any, error) {
	return nil, dispatch.Blocked(errors.New("group builders: env_file /x/b.env is open to others (mode 0644); chmod 600 it"))
}

// A group blocked by its own config is not the bead's failure: the bead is
// neither counted nor held, and the group reports why once.
func TestBlockedGroupHoldsNoBead(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	r := &blockedRunner{fakeRunner: &fakeRunner{dead: map[string]bool{}}}
	d := &dispatch.Dispatcher{Store: s, Runners: map[string]dispatch.Runner{"fake": r},
		Backoff: time.Nanosecond, RuntimeBackoff: time.Nanosecond, MaxAttempts: 2}
	ctx := context.Background()
	must(t, s.PutGroup(ctx, store.Group{Name: "builders", Runner: "fake", Limit: 1}, "me"))
	_, _ = s.AddRoute(ctx, store.Route{Stage: "new", Group: "builders"}, "me")
	b, err := s.CreateBead(ctx, store.NewBead{Title: "b", Stage: "new"}, "me")
	must(t, err)
	for range 4 {
		must(t, d.Reconcile(ctx))
		time.Sleep(time.Millisecond)
	}
	got, _ := s.GetBead(ctx, b.ID)
	if n, _ := s.FailureCount(ctx, b.ID, b.Stage); got.Hold || n != 0 {
		t.Fatalf("blocked group failed the bead: hold=%v failures=%d", got.Hold, n)
	}
	if got := groupEvents(t, s, "runtime_down"); got["builders"] != 1 {
		t.Fatalf("runtime_down = %v, want one for builders", got)
	}
}

// A bead held after repeated failures and then un-held is not held again
// once the log that recorded the unhold is pruned: the attempts since the
// unhold live on the bead, not in the event.
func TestUnheldBeadSurvivesPruningTheUnheldEvent(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	f := &fakeRunner{dead: map[string]bool{}}
	d := &dispatch.Dispatcher{Store: s, Runners: map[string]dispatch.Runner{"fake": f},
		Backoff: time.Nanosecond, MaxAttempts: 2}
	ctx := context.Background()
	must(t, s.PutGroup(ctx, store.Group{Name: "g", Runner: "fake"}, "me"))
	_, _ = s.AddRoute(ctx, store.Route{Stage: "new", Group: "g"}, "me")
	b, _ := s.CreateBead(ctx, store.NewBead{Title: "flaky"}, "me")
	for range 2 {
		must(t, d.Reconcile(ctx))
		got, _ := s.GetBead(ctx, b.ID)
		if got.Session == nil {
			t.Fatalf("bead not started: %+v", got)
		}
		f.dead[*got.Session] = true
		time.Sleep(time.Millisecond)
	}
	must(t, d.Reconcile(ctx))
	if got, _ := s.GetBead(ctx, b.ID); !got.Hold {
		t.Fatalf("bead not held after %d deaths: %+v", d.MaxAttempts, got)
	}
	_, err := s.SetHold(ctx, b.ID, false, store.Expect{}, "human")
	must(t, err)
	must(t, s.AddNote(ctx, b.ID, "cause fixed, retrying", "human")) // the newest event stays, so this one does

	// Everything so far ages out at once: the unheld event and the dead
	// sessions (stopped by the passes above) go.
	time.Sleep(time.Millisecond)
	res, err := s.Prune(ctx, store.Retention{Events: time.Nanosecond}, "me")
	must(t, err)
	if res.Sessions != 2 || res.HeldBy != "" {
		t.Fatalf("prune = %+v, want both dead sessions gone", res)
	}
	if evs, _ := s.Events(ctx, b.ID, 0); len(evs) != 1 || evs[0].Kind != "noted" {
		t.Fatalf("bead events after prune = %+v, want only the note", evs)
	}

	must(t, d.Reconcile(ctx))
	got, _ := s.GetBead(ctx, b.ID)
	if got.Hold || got.Session == nil {
		t.Fatalf("un-held bead re-held without a new failure: %+v", got)
	}
	if n := len(f.starts()); n != 3 {
		t.Fatalf("starts = %d, want 3", n)
	}
	if n, _ := s.FailureCount(ctx, b.ID, "new"); n != 0 {
		t.Fatalf("failures after unhold and prune = %d, want 0", n)
	}
}

// checkoutRunner is a runner whose agents work in their bead's checkout, as
// herdr's do (one worktree per bead, opened again by every later session of
// it), and run until a Stop of theirs lands or a person closes their pane.
// beside names each start into a checkout another agent still held: two
// agents in one checkout.
type checkoutRunner struct {
	*fakeRunner
	live      map[string]string // session -> its checkout, while its agent runs
	beside    []string
	stopErr   error // every Stop fails with it while it is set
	afterOpen error // the next Start opens its agent, then fails with it
}

// open puts session's agent in bead's checkout and returns the handle that
// says so.
func (c *checkoutRunner) open(session, bead string) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	at := "/worktrees/" + bead
	for other, held := range c.live {
		if held == at {
			c.beside = append(c.beside, session+" beside "+other)
		}
	}
	c.live[session] = at
	return map[string]any{"session": session, "worktree": at}
}

func (c *checkoutRunner) Start(ctx context.Context, j dispatch.Job) (map[string]any, error) {
	if _, err := c.fakeRunner.Start(ctx, j); err != nil {
		return nil, err
	}
	h := c.open(j.Session.ID, j.Bead.ID)
	c.mu.Lock()
	err := c.afterOpen
	c.afterOpen = nil
	c.mu.Unlock()
	return h, err
}

func (c *checkoutRunner) Stop(ctx context.Context, h map[string]any) error {
	c.mu.Lock()
	err := c.stopErr
	if err == nil {
		delete(c.live, h["session"].(string))
	}
	c.mu.Unlock()
	if err != nil {
		return err
	}
	return c.fakeRunner.Stop(ctx, h)
}

// runs says whether session's agent is still there.
func (c *checkoutRunner) runs(session string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.live[session]
	return ok
}

// closePane is a person closing the pane of session's agent.
func (c *checkoutRunner) closePane(session string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.live, session)
}

func (c *checkoutRunner) sharing() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.beside)
}

// checkoutYard is a yard whose group g (with config cfg) works stage new and
// group reviewers stage review, both with agents in per-bead checkouts, with
// one bead in stage new and a clock the test moves.
type checkoutYard struct {
	t    *testing.T
	s    store.API
	r    *checkoutRunner
	d    *dispatch.Dispatcher
	bead store.Bead
	now  time.Time
}

func newCheckoutYard(t *testing.T, cfg map[string]any) *checkoutYard {
	t.Helper()
	s := storetest.New(t)
	r := &checkoutRunner{fakeRunner: &fakeRunner{dead: map[string]bool{}}, live: map[string]string{}}
	y := &checkoutYard{t: t, s: s, r: r, now: time.Now(),
		d: &dispatch.Dispatcher{Store: s, Runners: map[string]dispatch.Runner{"fake": r}, Backoff: time.Nanosecond}}
	dispatch.SetClock(y.d, func() time.Time { return y.now })
	ctx := context.Background()
	for group, stage := range map[string]string{"g": "new", "reviewers": "review"} {
		must(t, s.PutGroup(ctx, store.Group{Name: group, Runner: "fake", Config: cfg}, "me"))
		_, err := s.AddRoute(ctx, store.Route{Stage: stage, Group: group}, "me")
		must(t, err)
	}
	y.bead = newBead(t, s, "feature")
	return y
}

// pass runs one pass an hour on: the backoff of a session that ended is over.
func (y *checkoutYard) pass() {
	y.t.Helper()
	y.now = y.now.Add(time.Hour)
	passes(y.t, y.d, 1)
}

// claims is how many times the bead has been claimed, one for each session
// the yard started for it.
func (y *checkoutYard) claims() int {
	y.t.Helper()
	return len(eventKinds(y.t, y.s, y.bead.ID, "claimed"))
}

func (y *checkoutYard) held() *store.Bead {
	y.t.Helper()
	b, err := y.s.GetBead(context.Background(), y.bead.ID)
	must(y.t, err)
	return b
}

// failedEnds are the places that end a session as failed with its agent in the
// checkout, each starting the bead's first session and returning it: the
// pass's reap, which fails a start that no worker owns after five minutes (a
// daemon died halfway through it) and a running session whose first prompt was
// given up on, and the start's own worker, which fails a start that opened the
// agent and then went wrong. The fence stands in the route that every one of
// them leaves the bead to, not at any of them.
var failedEnds = []struct {
	name string
	why  string // what the error of the session ended says, so that each row is known to take its own way
	end  func(y *checkoutYard) string
}{
	{"an interrupted start", "start interrupted", func(y *checkoutYard) string {
		ctx := context.Background()
		sess, err := y.s.Claim(ctx, y.bead.ID, "g", y.bead.Stage, "yard")
		must(y.t, err)
		must(y.t, y.s.SetHandle(ctx, sess.ID, y.r.open(sess.ID, y.bead.ID)))
		y.pass()
		return sess.ID
	}},
	{"a first prompt given up on", "first prompt", func(y *checkoutYard) string {
		ctx := context.Background()
		sess, err := y.s.Claim(ctx, y.bead.ID, "g", y.bead.Stage, "yard")
		must(y.t, err)
		h := y.r.open(sess.ID, y.bead.ID)
		h["prompt_lost"] = true
		must(y.t, y.s.MarkRunning(ctx, sess.ID, h))
		passes(y.t, y.d, 1)
		return sess.ID
	}},
	{"a failed start", "the pane is gone", func(y *checkoutYard) string {
		y.r.afterOpen = errors.New("sending the first prompt: the pane is gone")
		passes(y.t, y.d, 1)
		return y.r.jobs[0].Session.ID
	}},
}

// A bead is not started again while a session of it that ended as failed has
// its agent in the checkout, whatever ended it: when the Stop of that agent
// fails the bead waits past its backoff, and starts once the stop lands, with
// the old agent gone. Before, the backoff alone released it into the checkout
// of an agent that still ran.
func TestFailedSessionsBeadWaitsUntilItsAgentIsStopped(t *testing.T) {
	t.Parallel()
	for _, site := range failedEnds {
		t.Run(site.name, func(t *testing.T) {
			t.Parallel()
			y := newCheckoutYard(t, nil)
			y.r.stopErr = errors.New("herdr: closing the tab failed")
			session := site.end(y)
			sess, how := getSession(t, y.s, session), ""
			if sess.Error != nil {
				how = *sess.Error
			}
			if sess.State != "failed" || !strings.Contains(how, site.why) {
				t.Fatalf("session ended %s: %q, want failed with %q in its error", sess.State, how, site.why)
			}
			for range 3 {
				y.pass()
			}
			if n, beside := y.claims(), y.r.sharing(); n != 1 || len(beside) != 0 || !y.r.runs(session) {
				t.Fatalf("claims = %d, agents beside another = %v, old agent running = %v: the bead was started again while its failed session was not stopped",
					n, beside, y.r.runs(session))
			}
			if got := y.held(); got.Hold {
				t.Fatalf("bead held: it only waits for the stop: %+v", got)
			}
			waits := eventKinds(t, y.s, y.bead.ID, "backoff")
			if len(waits) != 1 || !strings.Contains(fmt.Sprint(waits[0].Data["reason"]), session) {
				t.Fatalf("backoff events = %+v, want one that names %s", waits, session)
			}

			y.r.stopErr = nil
			y.pass()
			if n, beside := y.claims(), y.r.sharing(); n != 2 || len(beside) != 0 || y.r.runs(session) {
				t.Fatalf("after the stop landed: claims = %d (want 2), agents beside another = %v, old agent running = %v", n, beside, y.r.runs(session))
			}
		})
	}
}

// A failed session of a group that keeps its agents running (keep) leaves the
// agent in the checkout, since the group exists to have it looked at, so its
// bead is held with a note naming the session and what to do, and routed again
// only when a person unholds it. Teardown marks a kept session stopped without
// stopping it, so only the hold keeps the bead from the agent's checkout.
func TestFailedSessionOfKeptGroupHoldsItsBead(t *testing.T) {
	t.Parallel()
	for _, site := range failedEnds {
		t.Run(site.name, func(t *testing.T) {
			t.Parallel()
			y := newCheckoutYard(t, map[string]any{"keep": true})
			session := site.end(y)
			for range 3 {
				y.pass()
			}
			if n, beside := y.claims(), y.r.sharing(); n != 1 || len(beside) != 0 {
				t.Fatalf("claims = %d, agents beside another = %v: the bead was started again beside the kept agent", n, beside)
			}
			got := y.held()
			if !got.Hold || got.HeldBy != "yard" {
				t.Fatalf("bead not held by the yard: %+v", got)
			}
			if sess := getSession(t, y.s, session); !sess.Stopped || !y.r.runs(session) || len(y.r.stopped) != 0 {
				t.Fatalf("session = %+v, agent running %v, stopped %v: want it kept, marked stopped and left running", sess, y.r.runs(session), y.r.stopped)
			}
			notes, err := y.s.Notes(context.Background(), y.bead.ID)
			must(t, err)
			var said bool
			for _, n := range notes {
				said = said || n.Author == "yard" && strings.Contains(n.Text, session) && strings.Contains(n.Text, "keep") && strings.Contains(n.Text, "yardr bead unhold "+y.bead.ID)
			}
			if !said {
				t.Fatalf("no note by the yard names %s, keep and the unhold: %+v", session, notes)
			}
			// The note is the last thing before the hold, by the same actor: that
			// is where the web view reads a hold's reason.
			evs, err := y.s.Events(context.Background(), y.bead.ID, 0)
			must(t, err)
			for i, e := range evs {
				if e.Kind == "held" && (i == 0 || evs[i-1].Kind != "noted" || evs[i-1].Actor != e.Actor) {
					t.Fatalf("the hold is not right after the yard's note: %+v", evs)
				}
			}

			// A person looks at the agent, closes its pane and unholds: the
			// bead starts again with fresh attempts.
			y.r.closePane(session)
			_, err = y.s.SetHold(context.Background(), y.bead.ID, false, store.Expect{}, "yardmaster")
			must(t, err)
			y.pass()
			if n, beside := y.claims(), y.r.sharing(); n != 2 || len(beside) != 0 {
				t.Fatalf("after the unhold: claims = %d (want 2), agents beside another = %v", n, beside)
			}
			if n, _ := y.s.FailureCount(context.Background(), y.bead.ID, "new"); n != 0 {
				t.Fatalf("failures after the unhold = %d, want a fresh start", n)
			}
		})
	}
}

// A session that ended any way but failed leaves its bead to be routed as it
// was, whatever its group keeps and whether its Stop lands: the fence is for a
// failed end whose agent may act, not for a session that finished (done) or
// whose runner says its agent is gone (died).
func TestSessionsThatEndedDoneOrDiedDoNotFenceTheirBead(t *testing.T) {
	t.Parallel()
	keep := map[string]any{"keep": true}
	stops := errors.New("herdr: closing the tab failed")
	died := func(y *checkoutYard, session string) { y.r.dead[session] = true }
	done := func(y *checkoutYard, _ string) {
		_, err := y.s.Advance(context.Background(), y.bead.ID, "review", store.Expect{}, "builder")
		must(y.t, err)
	}
	for _, row := range []struct {
		name    string
		cfg     map[string]any
		stopErr error
		end     func(y *checkoutYard, session string)
	}{
		{"died in a group that keeps its agents", keep, nil, died},
		{"died with a Stop that fails", nil, stops, died},
		{"done in a group that keeps its agents", keep, nil, done},
		{"done with a Stop that fails", nil, stops, done},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			y := newCheckoutYard(t, row.cfg)
			y.r.stopErr = row.stopErr
			passes(t, y.d, 1)
			row.end(y, y.r.jobs[0].Session.ID)
			for range 2 {
				y.pass()
			}
			if got := y.held(); got.Hold || y.claims() != 2 {
				t.Fatalf("held = %v, claims = %d: want the bead routed again, not held", got.Hold, y.claims())
			}
		})
	}
}
