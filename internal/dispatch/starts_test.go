package dispatch_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"yardr.dev/yardr/internal/config"
	"yardr.dev/yardr/internal/dispatch"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
	"yardr.dev/yardr/internal/testload"
)

// gateRunner is a fakeRunner whose starts for one group wait to be
// released, so a test can hold one group's start open while it watches
// the others. It records how often each group was started, and whether
// the context it was handed carried a deadline.
type gateRunner struct {
	*fakeRunner
	gateMu     sync.Mutex
	blockGroup string
	entered    chan string
	release    chan struct{}
	err        error
	calls      map[string]int
	deadline   bool
}

func newGateRunner(blockGroup string) *gateRunner {
	return &gateRunner{
		fakeRunner: &fakeRunner{dead: map[string]bool{}},
		blockGroup: blockGroup,
		entered:    make(chan string, 16),
		release:    make(chan struct{}),
		calls:      map[string]int{},
	}
}

func (r *gateRunner) Start(ctx context.Context, j dispatch.Job) (map[string]any, error) {
	r.gateMu.Lock()
	r.calls[j.Group.Name]++
	blocked := j.Group.Name == r.blockGroup
	if _, ok := ctx.Deadline(); ok {
		r.deadline = true
	}
	r.gateMu.Unlock()
	if !blocked {
		return r.fakeRunner.Start(ctx, j)
	}
	r.entered <- j.Bead.ID
	select {
	case <-r.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	r.gateMu.Lock()
	err := r.err
	r.gateMu.Unlock()
	if err != nil {
		return nil, err
	}
	return r.fakeRunner.Start(ctx, j)
}

func (r *gateRunner) callsFor(group string) int {
	r.gateMu.Lock()
	defer r.gateMu.Unlock()
	return r.calls[group]
}

func (r *gateRunner) sawDeadline() bool {
	r.gateMu.Lock()
	defer r.gateMu.Unlock()
	return r.deadline
}

type cancelStartRunner struct {
	*fakeRunner
	started  chan struct{}
	canceled chan struct{}
	release  chan struct{}
}

func (r *cancelStartRunner) Start(ctx context.Context, _ dispatch.Job) (map[string]any, error) {
	close(r.started)
	select {
	case <-ctx.Done():
		close(r.canceled)
		return nil, ctx.Err()
	case <-r.release:
		return nil, errors.New("test released start")
	}
}

// waitSessionState polls until bead's session is in state: the effect a
// test waits on while a start runs beside its passes, never the wall
// time the start takes.
func waitSessionState(t *testing.T, s store.API, bead, state string) store.Session {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(testload.Scale(30 * time.Second))
	for time.Now().Before(deadline) {
		b, err := s.GetBead(ctx, bead)
		must(t, err)
		if b.Session != nil {
			sess, err := s.GetSession(ctx, *b.Session)
			must(t, err)
			if sess.State == state {
				return *sess
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	b, _ := s.GetBead(ctx, bead)
	t.Fatalf("bead %s session never became %s (bead %+v)", bead, state, b)
	return store.Session{}
}

// runDaemon runs d's loop until the returned stop is called, as the
// daemon runs in the yard: the tests below watch the store while starts
// run beside its passes, and stop it by the caller's hand.
func runDaemon(t *testing.T, d *dispatch.Dispatcher) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	return func() {
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

// wakePass makes a store change no route takes, so Run's loop runs a
// pass for what a test changed beside it (the dispatcher's clock, here)
// without starting anything for it, and waits until that pass has run:
// the pass's own stamp advancing past this call is the effect waited
// on, not any one of the pass's writes for a bead. The stamp is the
// pass's completion signal, read with config.ReadPassStamp, so the wait
// holds whatever the pass walks or writes on the way; the wake bead is
// only the prod, and its priority is none. The dispatcher needs a home
// for its passes to stamp.
func wakePass(t *testing.T, d *dispatch.Dispatcher, s store.API) {
	t.Helper()
	since := time.Now()
	if _, err := s.CreateBead(context.Background(), store.NewBead{Title: "wake", Stage: "limbo"}, "me"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(testload.Scale(30 * time.Second))
	for time.Now().Before(deadline) {
		stamp, err := config.ReadPassStamp(d.Home)
		if err == nil && stamp.At.After(since) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the woken pass never ran")
}

// One group's runner blocked past any pass's patience leaves the pass
// free: another group's bead is claimed and running from the same pass,
// the blocked group's next bead is still unclaimed with no session in
// the store, and its first bead's session is starting and counted (a
// later pass neither ends it nor claims behind it).
func TestBlockedStartDoesNotBlockOtherGroup(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	r := newGateRunner("builders")
	d := &dispatch.Dispatcher{Store: s, Runners: map[string]dispatch.Runner{"fake": r},
		Backoff: time.Nanosecond, Interval: time.Hour}
	d.Home = t.TempDir() // wakePass waits on the pass stamp
	ctx := context.Background()
	must(t, s.PutGroup(ctx, store.Group{Name: "builders", Runner: "fake", Limit: 5}, "me"))
	must(t, s.PutGroup(ctx, store.Group{Name: "reviewers", Runner: "fake", Limit: 5}, "me"))
	_, err := s.AddRoute(ctx, store.Route{Stage: "new", Group: "builders"}, "me")
	must(t, err)
	_, err = s.AddRoute(ctx, store.Route{Stage: "review", Group: "reviewers"}, "me")
	must(t, err)
	b1, err := s.CreateBead(ctx, store.NewBead{Title: "b1", Priority: 1}, "me")
	must(t, err)
	b2, err := s.CreateBead(ctx, store.NewBead{Title: "b2", Priority: 2}, "me")
	must(t, err)
	r1, err := s.CreateBead(ctx, store.NewBead{Title: "r1", Stage: "review"}, "me")
	must(t, err)
	stop := runDaemon(t, d)
	defer stop()

	waitSessionState(t, s, r1.ID, "running")
	if got := <-r.entered; got != b1.ID {
		t.Fatalf("blocked start is for %s, want %s", got, b1.ID)
	}
	first := waitSessionState(t, s, b1.ID, "starting")
	if got, _ := s.GetBead(ctx, b2.ID); got.Group != nil || got.Session != nil {
		t.Fatalf("blocked group's next bead was claimed behind its start: %+v", got)
	}
	if live, _ := s.Sessions(ctx, false); len(live) != 2 {
		t.Fatalf("live sessions = %v, want the starting and the running one", live)
	}
	// b2 is the pass's last bead: its wait is written after the effects
	// waited on above are visible, so wait for the event itself, as long as
	// the fuse against a hang (dispatch.WaitFuse, in until) allows.
	var waits []store.Event
	until(t, "next bead's group_wait", func() bool {
		waits = eventKinds(t, s, b2.ID, "group_wait")
		return len(waits) > 0
	})
	if len(waits) != 1 || !strings.Contains(waits[0].Data["reason"].(string), "starting a session") {
		t.Fatalf("next bead's group_wait = %+v, want one for the busy worker", waits)
	}

	// A later pass still neither ends the owned start nor claims behind it.
	wakePass(t, d, s)
	if got, _ := s.GetSession(ctx, first.ID); got.State != "starting" {
		t.Fatalf("owned starting session = %s, want starting", got.State)
	}
	if got, _ := s.GetBead(ctx, b2.ID); got.Group != nil || got.Session != nil {
		t.Fatalf("next bead claimed while its group starts: %+v", got)
	}
	if n := r.callsFor("builders"); n != 1 {
		t.Fatalf("builders starts = %d, want 1 while blocked", n)
	}

	close(r.release)
	waitSessionState(t, s, b1.ID, "running")
	waitSessionState(t, s, b2.ID, "running")
	if n := r.callsFor("builders"); n != 2 {
		t.Fatalf("builders starts = %d, want 2 once the first landed", n)
	}
}

func TestStandaloneReconcileUsesPassContext(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	r := &cancelStartRunner{
		fakeRunner: &fakeRunner{dead: map[string]bool{}},
		started:    make(chan struct{}),
		canceled:   make(chan struct{}),
		release:    make(chan struct{}),
	}
	d := &dispatch.Dispatcher{Store: s, Runners: map[string]dispatch.Runner{"fake": r}, Interval: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	t.Cleanup(func() { close(r.release) })
	must(t, s.PutGroup(context.Background(), store.Group{Name: "g", Runner: "fake", Limit: 2}, "me"))
	_, err := s.AddRoute(context.Background(), store.Route{Stage: "new", Group: "g"}, "me")
	must(t, err)
	_, err = s.CreateBead(context.Background(), store.NewBead{Title: "cancelled start"}, "me")
	must(t, err)

	pass := make(chan error, 1)
	go func() { pass <- d.Reconcile(ctx) }()
	select {
	case <-r.started:
	case <-time.After(testload.Scale(30 * time.Second)):
		t.Fatal("standalone pass did not start the runner")
	}
	cancel()
	select {
	case <-r.canceled:
	case <-time.After(testload.Scale(30 * time.Second)):
		t.Fatal("runner start did not receive the pass cancellation")
	}
	select {
	case err := <-pass:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("standalone pass after cancellation: %v", err)
		}
	case <-time.After(testload.Scale(30 * time.Second)):
		t.Fatal("standalone pass did not settle its cancelled start")
	}
	if groups := dispatch.StartsInFlight(d); len(groups) != 0 {
		t.Fatalf("standalone pass left starts in flight: %v", groups)
	}
}

func TestStandaloneReconcileStartsSuccessiveBeads(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	r := &fakeRunner{dead: map[string]bool{}}
	d := &dispatch.Dispatcher{Store: s, Runners: map[string]dispatch.Runner{"fake": r}, Interval: time.Hour}
	ctx := context.Background()
	must(t, s.PutGroup(ctx, store.Group{Name: "g", Runner: "fake", Limit: 5}, "me"))
	_, err := s.AddRoute(ctx, store.Route{Stage: "new", Group: "g"}, "me")
	must(t, err)
	beads := make([]*store.Bead, 3)
	for i := range beads {
		beads[i], err = s.CreateBead(ctx, store.NewBead{Title: "successive"}, "me")
		must(t, err)
	}
	for i := range beads {
		must(t, d.Reconcile(ctx))
		waitSessionState(t, s, beads[i].ID, "running")
	}
}

// gatedEndStore holds EndSession's return, once the store holds the end,
// until release is closed or the context ends. The worker's abort commit is
// then visible, and has woken the loop, while its completion is not yet
// posted: the gap between the two (runStart commits, its caller posts) that
// a busy box leaves open for a pass, held open here until the test has seen
// what a pass in it does.
type gatedEndStore struct {
	store.API
	release chan struct{}
}

func (s *gatedEndStore) EndSession(ctx context.Context, id, state, reason string) error {
	err := s.API.EndSession(ctx, id, state, reason)
	select {
	case <-s.release:
	case <-ctx.Done():
	}
	return err
}

// lateWakeStore holds the loop's wake subscription back until release is
// closed or the context ends, as a starved process or a slow socket can leave
// it beside the first pass. Store.Wake delivers the commits after it comes up
// and none before it, so what is committed meanwhile is announced by no wake.
type lateWakeStore struct {
	store.API
	release chan struct{}
}

func (s *lateWakeStore) Wake(ctx context.Context) (<-chan struct{}, error) {
	select {
	case <-s.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return s.API.Wake(ctx)
}

// The same blocked group's first start then fails as a runtime error:
// the runner has been called once, the second bead is still unclaimed —
// it is considered only by a later pass, under the recorded outage,
// never by the worker on its own motion.
//
// The worker commits the abort inside runStart and posts its completion
// after runStart returns, and the commit wakes the loop. A pass that runs
// between the two sees the group's start still in flight and no outage
// recorded, so the bead's one group_wait says the group is starting a
// session; one that runs after the drain says the runtime is down. Which
// comes first is up to the machine, so the first row takes either reason;
// the second holds the worker between the two until a pass has reported,
// so the stale reason is the one it must get.
//
// The third row holds the loop's wake subscription back until the abort is
// committed, as a starved process can: the first pass's writes and the abort
// are announced by no wake, and the pass in the gap is the one the loop runs
// when the subscription comes up.
func TestRuntimeStartFailureLeavesNextBeadToALaterPass(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		// gap holds the worker between its abort commit and its completion
		// until a pass has run in between.
		gap bool
		// lateWake holds the wake subscription back until the abort is
		// committed.
		lateWake bool
		reasons  []string
	}{
		{"either order", false, false, []string{"runtime is down", "starting a session"}},
		{"pass in the gap", true, false, []string{"starting a session"}},
		{"subscription up after the abort", true, true, []string{"starting a session"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := storetest.New(t)
			r := newGateRunner("builders")
			r.err = dispatch.Runtime(errors.New("herdr is down"))
			var daemonStore store.API = s
			var gap, wakeUp chan struct{}
			if tc.lateWake {
				wakeUp = make(chan struct{})
				daemonStore = &lateWakeStore{API: daemonStore, release: wakeUp}
			}
			if tc.gap {
				gap = make(chan struct{})
				daemonStore = &gatedEndStore{API: daemonStore, release: gap}
			}
			d := &dispatch.Dispatcher{Store: daemonStore, Runners: map[string]dispatch.Runner{"fake": r},
				Backoff: time.Nanosecond, RuntimeBackoff: time.Hour, Interval: time.Hour}
			d.Home = t.TempDir() // wakePass waits on the pass stamp
			ctx := context.Background()
			must(t, s.PutGroup(ctx, store.Group{Name: "builders", Runner: "fake", Limit: 5}, "me"))
			_, err := s.AddRoute(ctx, store.Route{Stage: "new", Group: "builders"}, "me")
			must(t, err)
			b1, err := s.CreateBead(ctx, store.NewBead{Title: "b1", Priority: 1}, "me")
			must(t, err)
			b2, err := s.CreateBead(ctx, store.NewBead{Title: "b2", Priority: 2}, "me")
			must(t, err)
			stop := runDaemon(t, d)
			defer stop()

			if got := <-r.entered; got != b1.ID {
				t.Fatalf("blocked start is for %s, want %s", got, b1.ID)
			}
			first := waitSessionState(t, s, b1.ID, "starting")
			close(r.release)
			deadline := time.Now().Add(testload.Scale(30 * time.Second))
			for time.Now().Before(deadline) {
				if got, _ := s.GetSession(ctx, first.ID); got.State == "aborted" {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if got, _ := s.GetSession(ctx, first.ID); got.State != "aborted" {
				t.Fatalf("runtime-failed session = %s, want aborted", got.State)
			}
			for _, b := range []string{b1.ID, b2.ID} {
				if got, _ := s.GetBead(ctx, b); got.Group != nil || got.Session != nil {
					t.Fatalf("bead %s claimed behind the failed start: %+v", b, got)
				}
				if n, _ := s.FailureCount(ctx, b, "new"); n != 0 {
					t.Fatalf("bead %s counted %d failures from an outage", b, n)
				}
			}
			if n := r.callsFor("builders"); n != 1 {
				t.Fatalf("builders starts = %d, want 1: the worker retried on its own", n)
			}
			if tc.lateWake {
				// Everything the loop has done so far went unannounced; the
				// subscription comes up now, after it all.
				close(wakeUp)
			}
			if tc.gap {
				// The abort is committed and the worker is held before its
				// post. The commit woke the loop, so a pass runs now, finds
				// the start in flight and tells the bead so; the worker goes
				// on only once that is on record.
				until(t, "a pass between the abort and the completion", func() bool {
					return len(eventKinds(t, s, b1.ID, "group_wait")) > 0
				})
				close(gap)
			}
			// The completion's pass records the outage, and only then considers
			// the beads again — under it, so nothing more is tried.
			for time.Now().Before(deadline) && groupEvents(t, s, "runtime_down")["builders"] == 0 {
				time.Sleep(5 * time.Millisecond)
			}
			if got := groupEvents(t, s, "runtime_down"); got["builders"] != 1 {
				t.Fatalf("runtime_down = %v, want one for builders", got)
			}
			wakePass(t, d, s)
			if n := r.callsFor("builders"); n != 1 {
				t.Fatalf("builders starts after the outage was recorded = %d, want 1", n)
			}
			// A bead hears of its wait once per stage and route, so the count
			// holds whichever pass spoke first; the reason is that pass's, and
			// the outage's own wait is dropped behind it.
			waits := eventKinds(t, s, b1.ID, "group_wait")
			if len(waits) != 1 {
				t.Fatalf("group_wait events after the outage = %+v, want one", waits)
			}
			reason := waits[0].Data["reason"].(string)
			if !slices.ContainsFunc(tc.reasons, func(want string) bool { return strings.Contains(reason, want) }) {
				t.Fatalf("group_wait after the outage says %q, want one of %q", reason, tc.reasons)
			}
		})
	}
}

// A blocked, a runtime and a plain failure from a worker end their
// sessions in the store exactly as the pass ends them today, before any
// later pass drains the completion.
func TestWorkerFailureClassesEndSessionsAsThePassDid(t *testing.T) {
	t.Parallel()
	const timeout = "timeout: timed out waiting for agent startup"
	longUsage := "Usage: opencode " + strings.Repeat("--very-long-flag <value> ", 20)
	for _, tc := range []struct {
		name        string
		err         error
		state       string
		tail        string
		tailErr     error
		want        []string
		absent      []string
		exactReason string
		capped      bool
	}{
		{name: "bead blocked", err: dispatch.BeadBlocked(errors.New("its pull request is gone")), state: "aborted"},
		{name: "runtime", err: dispatch.Runtime(errors.New("herdr is down")), state: "aborted"},
		{name: "blocked group", err: dispatch.Blocked(errors.New("env_file is open to others")), state: "aborted"},
		{name: "plain", err: errors.New("the brief cannot be written"), state: "failed"},
		{name: "usage in pane", err: errors.New(timeout), state: "failed",
			tail: "shell noise while starting\nAPI_KEY=sk-live-secret\n" +
				"Usage: opencode [OPTIONS] [COMMAND]\nopencode <command> [options]\n" +
				"Options:\n  --model <model>  the model to run\n  --token sk-live-secret\n",
			want:   []string{"Usage: opencode [OPTIONS] [COMMAND]", "opencode <command> [options]", "Options:", "--model <model>"},
			absent: []string{"sk-live-secret", "shell noise", "--token"}},
		{name: "literal values in usage and flag rows are left out", err: errors.New(timeout), state: "failed",
			tail: "Usage: opencode --token sk-live-secret\nOptions:\n" +
				"  --token sk-live-secret\n  --model <model>  the model to run\n",
			want:   []string{"Usage:", "Options:", "--model <model>"},
			absent: []string{"sk-live-secret", "--token"}},
		{name: "no usage in pane reads exactly as today", err: errors.New(timeout), state: "failed",
			tail: "agent starting\nAPI_KEY=sk-live-secret\nstill thinking\n", exactReason: timeout,
			absent: []string{"sk-live-secret"}},
		{name: "unreadable pane reads exactly as today", err: errors.New(timeout), state: "failed",
			tailErr: errors.New("the pane is gone"), exactReason: timeout},
		{name: "long help is capped short", err: errors.New(timeout), state: "failed",
			tail: longUsage + "\nOptions:\n  --a <a>\n  --b <b>\n  --c <c>\n  --d <d>\n",
			want: []string{"Usage: opencode"}, absent: []string{"--d <d>"}, capped: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := storetest.New(t)
			r := newGateRunner("g")
			r.err = tc.err
			close(r.release) // the gated start fails at once
			var runner dispatch.Runner = r
			if tc.tail != "" || tc.tailErr != nil {
				runner = &usageRunner{fakeRunner: r.fakeRunner, tail: tc.tail, tailErr: tc.tailErr, err: tc.err}
			}
			d := &dispatch.Dispatcher{Store: s, Runners: map[string]dispatch.Runner{"fake": runner},
				Backoff: time.Nanosecond, RuntimeBackoff: time.Hour, MaxAttempts: 3}
			ctx := context.Background()
			must(t, s.PutGroup(ctx, store.Group{Name: "g", Runner: "fake", Limit: 2}, "me"))
			_, err := s.AddRoute(ctx, store.Route{Stage: "new", Group: "g"}, "me")
			must(t, err)
			b, err := s.CreateBead(ctx, store.NewBead{Title: "b"}, "me")
			must(t, err)
			must(t, d.Reconcile(ctx))
			var claimed store.Session
			deadline := time.Now().Add(testload.Scale(10 * time.Second))
			for claimed.ID == "" && time.Now().Before(deadline) {
				all, err := s.Sessions(ctx, true)
				must(t, err)
				for _, sess := range all {
					if sess.Bead == b.ID {
						claimed = sess
					}
				}
				time.Sleep(5 * time.Millisecond)
			}
			if claimed.ID == "" {
				t.Fatal("no session was claimed for the bead")
			}
			dispatch.WaitStartsIdle(d)
			got, err := s.GetSession(ctx, claimed.ID)
			must(t, err)
			if got.State != tc.state {
				t.Fatalf("session = %s, want %s", got.State, tc.state)
			}
			if tc.exactReason != "" && (got.Error == nil || *got.Error != tc.exactReason) {
				t.Fatalf("reason = %v, want exactly %q", got.Error, tc.exactReason)
			}
			if len(tc.want) > 0 || len(tc.absent) > 0 || tc.capped {
				if got.Error == nil {
					t.Fatal("failed start has no reason")
				}
				for _, want := range tc.want {
					if !strings.Contains(*got.Error, want) {
						t.Errorf("reason = %q, want it to name %q", *got.Error, want)
					}
				}
				for _, absent := range tc.absent {
					if strings.Contains(*got.Error, absent) {
						t.Errorf("reason = %q, want none of the pane's %q in it", *got.Error, absent)
					}
				}
				if !strings.HasPrefix(*got.Error, timeout) {
					t.Errorf("reason = %q, want it to keep the start's own words %q first", *got.Error, timeout)
				}
				if tc.capped {
					if n := len([]rune(*got.Error)); n > len([]rune(timeout))+len(" (pane: )")+240 {
						t.Errorf("reason is %d runes, want the excerpt held to its cap: %q", n, *got.Error)
					}
					if !strings.Contains(*got.Error, "…") {
						t.Errorf("reason = %q, want a cut marked in it", *got.Error)
					}
				}
			}
			if got.State == "failed" {
				evs := eventKinds(t, s, b.ID, "session_failed")
				if len(evs) != 1 || got.Error == nil || evs[0].Data["reason"] != *got.Error {
					t.Fatalf("session_failed = %v, want one event with the session's reason %v", evs, got.Error)
				}
			}
			switch tc.name {
			case "bead blocked":
				got, _ := s.GetBead(ctx, b.ID)
				if !got.Hold {
					t.Fatalf("bead not held after a blocked start: %+v", got)
				}
				if n := len(eventKinds(t, s, b.ID, "session_blocked")); n != 1 {
					t.Fatalf("session_blocked events = %d, want 1", n)
				}
				if n, _ := s.FailureCount(ctx, b.ID, "new"); n != 0 {
					t.Fatalf("blocked start counted %d failures", n)
				}
			default:
				want := 0
				if tc.state == "failed" {
					want = 1
				}
				if n, _ := s.FailureCount(ctx, b.ID, "new"); n != want {
					t.Fatalf("failure count = %d, want %d", n, want)
				}
			}
		})
	}
}

// usageRunner is a fakeRunner whose starts fail with err from a pane
// that shows tail (or cannot be read, with tailErr): the stand-in pane a
// harness that printed its help at its start leaves behind.
type usageRunner struct {
	*fakeRunner
	tail    string
	tailErr error
	err     error
}

func (r *usageRunner) Start(_ context.Context, j dispatch.Job) (map[string]any, error) {
	return map[string]any{"session": j.Session.ID, "pane": "p1"}, r.err
}

func (r *usageRunner) Tail(_ context.Context, handle map[string]any, _ int) (string, error) {
	if pane, _ := handle["pane"].(string); pane == "" {
		return "", errors.New("handle has no pane")
	}
	return r.tail, r.tailErr
}

// A starting session its worker still owns is counted live at any age;
// one no worker owns — a daemon restarted over it — is failed as start
// interrupted after five minutes, and teardown closes its handle.
func TestStartingSessionsOwnedAndUnowned(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Owned: blocked past five minutes by the dispatcher's clock.
	s := storetest.New(t)
	r := newGateRunner("g")
	d := &dispatch.Dispatcher{Store: s, Runners: map[string]dispatch.Runner{"fake": r},
		Backoff: time.Nanosecond, Interval: time.Hour}
	d.Home = t.TempDir() // wakePass waits on the pass stamp
	must(t, s.PutGroup(ctx, store.Group{Name: "g", Runner: "fake", Limit: 2}, "me"))
	_, err := s.AddRoute(ctx, store.Route{Stage: "new", Group: "g"}, "me")
	must(t, err)
	b, err := s.CreateBead(ctx, store.NewBead{Title: "slow"}, "me")
	must(t, err)
	stop := runDaemon(t, d)
	defer stop()
	owned := waitSessionState(t, s, b.ID, "starting")
	<-r.entered
	// The claiming pass may still be running its tail: wait for its
	// stamp, so the stamp wakePass waits for below belongs to a pass
	// that ran under the moved clock. SetClock is a side channel, not a
	// store write, so the loop's serial order alone does not place the
	// pass after it.
	until(t, "the claiming pass", func() bool {
		_, err := config.ReadPassStamp(d.Home)
		return err == nil
	})
	dispatch.SetClock(d, func() time.Time { return time.Now().Add(6 * time.Minute) })
	wakePass(t, d, s)
	if got, _ := s.GetSession(ctx, owned.ID); got.State != "starting" {
		t.Fatalf("owned session aged past five minutes = %s, want starting", got.State)
	}
	// Together the start's blocked wait passes four minutes (and the
	// five above) with no cut: the worker's context carries no deadline
	// and is not cancelled by the clock moving.
	if r.sawDeadline() {
		t.Fatal("the worker's start context carries a deadline")
	}
	close(r.release)
	waitSessionState(t, s, b.ID, "running")

	// Unowned: claimed by a dispatcher that is gone before it started.
	s2, f2, d2 := setup(t)
	// Its startup pane survives the old dispatcher, so reap can name the
	// same usage that a worker's failed Start would have named.
	d2.Runners["fake"] = &usageRunner{fakeRunner: f2,
		tail: "Usage: opencode [OPTIONS] [COMMAND]\n"}
	must(t, s2.PutGroup(ctx, store.Group{Name: "g", Runner: "fake", Limit: 2}, "me"))
	_, err = s2.AddRoute(ctx, store.Route{Stage: "new", Group: "g"}, "me")
	must(t, err)
	b2, err := s2.CreateBead(ctx, store.NewBead{Title: "orphaned"}, "me")
	must(t, err)
	sess, err := s2.Claim(ctx, b2.ID, "g", "new", "yard")
	must(t, err)
	// No pane: the old failure reason stays exactly as it was.
	must(t, s2.SetHandle(ctx, sess.ID, map[string]any{"session": sess.ID}))
	b3, err := s2.CreateBead(ctx, store.NewBead{Title: "orphaned with usage"}, "me")
	must(t, err)
	withPane, err := s2.Claim(ctx, b3.ID, "g", "new", "yard")
	must(t, err)
	must(t, s2.SetHandle(ctx, withPane.ID, map[string]any{"session": withPane.ID, "pane": "p1"}))
	// Young, it is counted live; past five minutes, it is interrupted.
	dispatch.SetClock(d2, func() time.Time { return time.Now().Add(4 * time.Minute) })
	must(t, d2.Reconcile(ctx))
	if got, _ := s2.GetSession(ctx, sess.ID); got.State != "starting" {
		t.Fatalf("young unowned session = %s, want starting", got.State)
	}
	if got, _ := s2.GetSession(ctx, withPane.ID); got.State != "starting" {
		t.Fatalf("young unowned session with pane = %s, want starting", got.State)
	}
	dispatch.SetClock(d2, func() time.Time { return time.Now().Add(6 * time.Minute) })
	must(t, d2.Reconcile(ctx))
	got, _ := s2.GetSession(ctx, sess.ID)
	if got.State != "failed" || got.Error == nil || *got.Error != "start interrupted" {
		t.Fatalf("unowned session past five minutes = %+v, want failed start interrupted", got)
	}
	got, _ = s2.GetSession(ctx, withPane.ID)
	if got.State != "failed" || got.Error == nil ||
		!strings.Contains(*got.Error, "start interrupted (pane: Usage: opencode") {
		t.Fatalf("unowned session past five minutes = %+v, want failed start interrupted naming its usage", got)
	}
	f2.mu.Lock()
	stopped := append([]string(nil), f2.stopped...)
	f2.mu.Unlock()
	found := map[string]bool{}
	for _, id := range stopped {
		found[id] = true
	}
	if !found[sess.ID] || !found[withPane.ID] {
		t.Fatalf("interrupted sessions' handles not stopped: %v", stopped)
	}
}
