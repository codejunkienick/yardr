package dispatch_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/agents"
	"yardr.dev/yardr/internal/dispatch"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
	"yardr.dev/yardr/internal/yardkey"
)

// forgetRunner stands in for the herdr runner on what forgetting a session needs
// of it. It starts sessions of the kind their group names, keeps their agent's
// id, says what the agent used (so a session is counted at once), and, asked to
// forget a session, writes down the id it was asked about and answers as
// answer does. The same one stands in for the other runners too, so that a
// session of another runner that was forgotten would show.
type forgetRunner struct {
	*fakeRunner
	forgot  []string                                    // the agent ids asked about, in order
	answer  func(id string) (missing string, err error) // nil: done
	hold    chan struct{}                               // not nil: Forget waits for it to close
	entered chan struct{}                               // not nil: Forget sends on it as it starts

	stopFails bool // Stop fails, so a session that ended is not stopped
}

func (r *forgetRunner) Start(ctx context.Context, j dispatch.Job) (map[string]any, error) {
	h, err := r.fakeRunner.Start(ctx, j)
	if err == nil {
		h["kind"] = agents.Kind(j.Group.Config)
	}
	return h, err
}

func (r *forgetRunner) Stop(ctx context.Context, h map[string]any) error {
	r.mu.Lock()
	fails := r.stopFails
	r.mu.Unlock()
	if fails {
		return errors.New("the agent would not stop")
	}
	return r.fakeRunner.Stop(ctx, h)
}

func (r *forgetRunner) SessionID(_ context.Context, h map[string]any, _ time.Time) (string, error) {
	session, _ := h["session"].(string)
	return agentID(session), nil
}

func (r *forgetRunner) Usage(context.Context, map[string]any, time.Time, bool) (store.Usage, bool, string, error) {
	return store.Usage{Input: 100, FreshInput: 100, Output: 5}, false, "", nil
}

func (r *forgetRunner) Forget(ctx context.Context, h map[string]any) (string, error) {
	id, _ := h[dispatch.AgentSession].(string)
	r.mu.Lock()
	r.forgot = append(r.forgot, id)
	answer, hold, entered := r.answer, r.hold, r.entered
	r.mu.Unlock()
	if entered != nil {
		entered <- struct{}{}
	}
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if answer != nil {
		return answer(id)
	}
	return "", nil
}

// asked is the ids of the agent sessions Forget was asked about so far.
func (r *forgetRunner) asked() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.forgot)
}

// forgetSetup is a yard with the one runner under the names of the runners
// whose sessions may be asked about, so that the dispatcher's own choice of
// which runner is what the tests see.
func forgetSetup(t *testing.T) (store.API, *forgetRunner, *dispatch.Dispatcher) {
	t.Helper()
	s := storetest.New(t)
	r := &forgetRunner{fakeRunner: &fakeRunner{dead: map[string]bool{}}}
	d := &dispatch.Dispatcher{Store: s, Backoff: time.Nanosecond,
		Runners: map[string]dispatch.Runner{"herdr": r, "exec": r, store.PeerRunner: r}}
	return s, r, d
}

// forgetGroup makes the group name of the runner, with agents of kind where it
// is herdr's, and sends the stage of that name to it.
func forgetGroup(t *testing.T, s store.API, name, runner, kind string) {
	t.Helper()
	cfg := map[string]any{}
	switch runner {
	case "herdr":
		cfg["kind"] = kind
	case "exec":
		cfg["cmd"] = "true"
	case store.PeerRunner:
		cfg[store.GroupPeer] = "airy"
		key, _, err := yardkey.Ensure(filepath.Join(t.TempDir(), "airy.key"))
		must(t, err)
		_, err = s.AddPeer(context.Background(), store.Peer{Name: "airy", Key: store.Public(key)}, "me")
		must(t, err)
		_, err = s.AllowPeer(context.Background(), "airy", store.PeerAllow{Send: []string{store.PeerBead}}, "me")
		must(t, err)
	}
	ctx := context.Background()
	must(t, s.PutGroup(ctx, store.Group{Name: name, Runner: runner, Limit: 10, Config: cfg}, "me"))
	_, err := s.AddRoute(ctx, store.Route{Stage: name, Group: name}, "me")
	must(t, err)
}

// ended runs a bead of the stage through a session that ends and is stopped
// and, where its runner counts, counted: the pass starts it, the id of its
// agent is kept, the bead is closed, and two passes tear it down and read what
// it used. It is the id of the session.
func ended(t *testing.T, s store.API, d *dispatch.Dispatcher, stage string) string {
	t.Helper()
	ctx := context.Background()
	b, err := s.CreateBead(ctx, store.NewBead{Title: "a bead of " + stage, Stage: stage}, "me")
	must(t, err)
	passes(t, d, 2) // started, and asked for the id of its agent
	got, err := s.GetBead(ctx, b.ID)
	must(t, err)
	if got.Session == nil {
		// A manual group's bead waits for a person, who claims it.
		sess, err := s.Claim(ctx, b.ID, stage, b.Stage, "me")
		must(t, err)
		got.Session = &sess.ID
	}
	endSession(t, s, b.ID)
	passes(t, d, 2) // torn down, then counted
	return *got.Session
}

// reshaped changes the handle of the session as f says and keeps it so.
func reshaped(t *testing.T, s store.API, session string, f func(h map[string]any)) {
	t.Helper()
	h := maps.Clone(getSession(t, s, session).Handle)
	if h == nil {
		h = map[string]any{}
	}
	f(h)
	must(t, s.SetHandle(context.Background(), session, h))
}

// asCountedCodex is how a session of a runner that starts no agent is made to
// look like a counted session of codex: it gives the handle everything one has,
// so that the runner's name is all that holds it back.
func asCountedCodex(session string) func(h map[string]any) {
	return func(h map[string]any) {
		h["kind"] = "codex"
		h[dispatch.AgentSession] = agentID(session)
		h[store.HandleUsage] = map[string]any{"input": 100, "read_at": "2026-11-25T10:00:00Z"}
	}
}

// look runs the pass the clock says and waits for the run it started.
func look(t *testing.T, d *dispatch.Dispatcher, at time.Time) {
	t.Helper()
	dispatch.SetClock(d, func() time.Time { return at })
	passes(t, d, 1)
	dispatch.WaitForgetIdle(d)
}

// forgottenOf is the mark that ends a session's forgetting, nil for none.
func forgottenOf(t *testing.T, s store.API, session string) map[string]any {
	t.Helper()
	m, _ := getSession(t, s, session).Handle[dispatch.Forgotten].(map[string]any)
	return m
}

// A harness that names a forget (Codex's, 7 days) is made to forget the sessions
// of the yard that its herdr runner started, once they ended, are stopped and
// counted, and the days have passed; and no other. The runner is the session's
// own record's: a group moved to another runner since changes neither way.
func TestForgetSelectsTheSessionsToForget(t *testing.T) {
	t.Parallel()
	s, r, d := forgetSetup(t)
	forgetGroup(t, s, "codex", "herdr", "codex")
	forgetGroup(t, s, "claude", "herdr", "claude") // a kind whose file names no forget
	forgetGroup(t, s, "tomove", "herdr", "codex")  // a group that is exec's by the end
	forgetGroup(t, s, "toherdr", "exec", "")       // a group that is herdr's by the end
	forgetGroup(t, s, "script", "exec", "")
	forgetGroup(t, s, "peers", store.PeerRunner, "")
	must(t, s.PutGroup(context.Background(), store.Group{Name: "person", Runner: "manual"}, "me"))
	_, err := s.AddRoute(context.Background(), store.Route{Stage: "person", Group: "person"}, "me")
	must(t, err)

	done := ended(t, s, d, "codex")
	uncounted := ended(t, s, d, "codex")
	already := ended(t, s, d, "codex")
	kept := ended(t, s, d, "codex")
	noForget := ended(t, s, d, "claude")
	moved := ended(t, s, d, "tomove")
	wasExec := ended(t, s, d, "toherdr")
	script := ended(t, s, d, "script")
	peer := ended(t, s, d, "peers")
	manual := ended(t, s, d, "person")
	// Its agent will not stop, so the session is over and not stopped; last, so
	// that no other is held by it.
	r.mu.Lock()
	r.stopFails = true
	r.mu.Unlock()
	unstopped := ended(t, s, d, "codex")
	if got := getSession(t, s, unstopped); got.Stopped || got.EndedAt == nil {
		t.Fatalf("the session whose agent will not stop: stopped %v, ended %v", got.Stopped, got.EndedAt)
	}

	// Every one of these has the id and the kind and the count of a session of
	// codex, so that what holds it back is the one thing it is made to differ in.
	for _, session := range []string{wasExec, script, peer, manual, unstopped} {
		reshaped(t, s, session, asCountedCodex(session))
	}
	reshaped(t, s, uncounted, func(h map[string]any) { delete(h, store.HandleUsage) })
	reshaped(t, s, already, func(h map[string]any) {
		h[dispatch.Forgotten] = map[string]any{"at": "2026-11-18T10:00:00Z"}
	})
	reshaped(t, s, kept, func(h map[string]any) {
		u := maps.Clone(h[store.HandleUsage].(map[string]any))
		u["kept"] = true
		h[store.HandleUsage] = u
	})
	// The groups move: what began as herdr's is exec's, and the other way.
	ctx := context.Background()
	must(t, s.PutGroup(ctx, store.Group{Name: "tomove", Runner: "exec", Limit: 10, Config: map[string]any{"cmd": "true"}}, "me"))
	must(t, s.PutGroup(ctx, store.Group{Name: "toherdr", Runner: "herdr", Limit: 10, Config: map[string]any{"kind": "codex"}}, "me"))

	// The ones that are to be forgotten are counted, and have their agent's id.
	for name, session := range map[string]string{"done": done, "moved": moved} {
		h := getSession(t, s, session).Handle
		if h[store.HandleUsage] == nil || h[dispatch.AgentSession] != agentID(session) || h["kind"] != "codex" {
			t.Fatalf("%s: the handle %v is not that of a counted session of codex", name, h)
		}
	}

	// Six days after they ended is not yet seven.
	afterEnds := time.Now() // the sessions above ended before it
	look(t, d, afterEnds.Add(6*24*time.Hour))
	if got := r.asked(); len(got) != 0 {
		t.Fatalf("Forget was asked about %q 6 days after the sessions ended", got)
	}
	latest := done
	if getSession(t, s, moved).EndedAt.After(*getSession(t, s, done).EndedAt) {
		latest = moved
	}
	cutoff := getSession(t, s, latest).EndedAt.AddDate(0, 0, 7)
	look(t, d, cutoff)
	if got := r.asked(); slices.Contains(got, agentID(latest)) {
		t.Fatalf("Forget was asked about %q exactly at the seven-day cutoff", agentID(latest))
	}
	look(t, d, cutoff.Add(time.Hour))

	// Past seven days the next look makes the harness forget the two that are
	// counted, stopped and its own, and no other.
	got := r.asked()
	slices.Sort(got)
	want := []string{agentID(done), agentID(moved)}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("Forget was asked about %q, want %q", got, want)
	}
	for name, session := range map[string]string{"done": done, "moved": moved} {
		m := forgottenOf(t, s, session)
		if at, _ := m["at"].(string); m == nil || at == "" || m["missing"] != nil {
			t.Errorf("%s: forgotten = %v, want {at}", name, m)
		}
	}
	for name, session := range map[string]string{"uncounted": uncounted, "kept": kept, "claude": noForget, "exec": script, "peer": peer, "manual": manual, "exec then herdr": wasExec, "unstopped": unstopped} {
		if m := forgottenOf(t, s, session); m != nil {
			t.Errorf("%s: forgotten = %v, want it left alone", name, m)
		}
	}
	if m := forgottenOf(t, s, already); m["at"] != "2026-11-18T10:00:00Z" {
		t.Errorf("a session forgotten already: mark %v, want the first one kept", m)
	}

	// Once is once: a later look asks about none of them again.
	look(t, d, afterEnds.Add(10*24*time.Hour))
	if got := r.asked(); len(got) != 2 {
		t.Errorf("Forget was asked %d times in all, want 2", len(got))
	}
}

// The sessions of a look are forgotten by how long ago they ended, the oldest
// first, whichever way the store lists them (by their start): a yard with
// thousands to forget at its first look takes the longest ago first.
func TestForgetTakesTheOldestFirst(t *testing.T) {
	t.Parallel()
	s, r, d := forgetSetup(t)
	forgetGroup(t, s, "codex", "herdr", "codex")
	ctx := context.Background()
	// Started one after the other, and ended the other way round.
	first, err := s.CreateBead(ctx, store.NewBead{Title: "first", Stage: "codex"}, "me")
	must(t, err)
	passes(t, d, 2)
	second, err := s.CreateBead(ctx, store.NewBead{Title: "second", Stage: "codex"}, "me")
	must(t, err)
	passes(t, d, 2)
	sessions := map[string]string{}
	for name, b := range map[string]store.Bead{"first": *first, "second": *second} {
		got, err := s.GetBead(ctx, b.ID)
		must(t, err)
		sessions[name] = *got.Session
	}
	endSession(t, s, second.ID)
	endSession(t, s, first.ID)
	passes(t, d, 2)

	look(t, d, time.Now().Add(8*24*time.Hour))
	if got, want := r.asked(), []string{agentID(sessions["second"]), agentID(sessions["first"])}; !slices.Equal(got, want) {
		t.Fatalf("Forget was asked about %q, want %q: the one that ended first, first", got, want)
	}
}

// An answer that is a reason there is nothing to forget ends it and says why;
// one that is an error leaves the session for the next look, is told on the
// bead, and after three tries ends it with the reason it gave up. The looks are
// an hour apart and no nearer: the sessions are not asked about on every pass.
func TestForgetRetriesAFailureAndGivesUp(t *testing.T) {
	t.Parallel()
	s, r, d := forgetSetup(t)
	forgetGroup(t, s, "codex", "herdr", "codex")
	ctx := context.Background()
	b, err := s.CreateBead(ctx, store.NewBead{Title: "x", Stage: "codex"}, "me")
	must(t, err)
	passes(t, d, 2)
	got, err := s.GetBead(ctx, b.ID)
	must(t, err)
	session := *got.Session
	endSession(t, s, b.ID)
	passes(t, d, 2)

	var nothing bool
	r.mu.Lock()
	r.answer = func(string) (string, error) {
		if nothing {
			return "no session id", nil
		}
		return "", fmt.Errorf("codex: exit status 1: Error: failed to delete session")
	}
	r.mu.Unlock()
	at := time.Now().Add(8 * 24 * time.Hour)

	// The first try fails: no mark, a count of one, and an event on the bead.
	look(t, d, at)
	if n := len(r.asked()); n != 1 {
		t.Fatalf("Forget was asked %d times at the first look, want 1", n)
	}
	h := getSession(t, s, session).Handle
	if h[dispatch.Forgotten] != nil || h[dispatch.ForgetTries] != float64(1) {
		t.Fatalf("after one failure the handle holds forgotten %v and tries %v, want no mark and 1", h[dispatch.Forgotten], h[dispatch.ForgetTries])
	}
	events := eventKinds(t, s, b.ID, "session_forget_failed")
	if len(events) != 1 || events[0].Data["session"] != session || events[0].Data["kind"] != "codex" || events[0].Data["tries"] != float64(1) ||
		!strings.Contains(fmt.Sprint(events[0].Data["reason"]), "failed to delete session") {
		t.Fatalf("session_forget_failed events = %+v, want the one failure", events)
	}

	// Nearer than an hour is not another look, an hour is.
	look(t, d, at.Add(59*time.Minute))
	if n := len(r.asked()); n != 1 {
		t.Fatalf("Forget was asked %d times 59 minutes later, want still 1", n)
	}
	look(t, d, at.Add(time.Hour))
	if n := len(r.asked()); n != 2 {
		t.Fatalf("Forget was asked %d times an hour later, want 2", n)
	}
	if h := getSession(t, s, session).Handle; h[dispatch.Forgotten] != nil || h[dispatch.ForgetTries] != float64(2) {
		t.Fatalf("after two failures the handle holds forgotten %v and tries %v", h[dispatch.Forgotten], h[dispatch.ForgetTries])
	}

	// The third failure is the last: the session is marked, with why.
	look(t, d, at.Add(2*time.Hour))
	m := forgottenOf(t, s, session)
	if reason, _ := m["missing"].(string); !strings.HasPrefix(reason, "gave up after 3 tries: ") || !strings.Contains(reason, "failed to delete session") {
		t.Fatalf("forgotten = %v after three failures, want it to say it gave up", m)
	}
	if n := len(eventKinds(t, s, b.ID, "session_forget_failed")); n != 3 {
		t.Errorf("%d session_forget_failed events, want one for each try", n)
	}
	look(t, d, at.Add(5*time.Hour))
	if n := len(r.asked()); n != 3 {
		t.Errorf("Forget was asked %d times in all, want 3: a session given up is not asked again", n)
	}

	// A reason there is nothing to forget ends it at the first look, and no
	// event is made of it.
	b2, err := s.CreateBead(ctx, store.NewBead{Title: "y", Stage: "codex"}, "me")
	must(t, err)
	dispatch.SetClock(d, time.Now)
	passes(t, d, 2)
	got2, err := s.GetBead(ctx, b2.ID)
	must(t, err)
	endSession(t, s, b2.ID)
	passes(t, d, 2)
	nothing = true
	look(t, d, at.Add(7*24*time.Hour))
	if m := forgottenOf(t, s, *got2.Session); m["missing"] != "no session id" || m["at"] == nil {
		t.Errorf("a session with nothing to forget: forgotten = %v, want {at, missing}", m)
	}
	if n := len(eventKinds(t, s, b2.ID, "session_forget_failed")); n != 0 {
		t.Errorf("%d session_forget_failed events for a reason, want none", n)
	}
}

// A run is one at a time and the pass does not wait for it: while the harness
// is still forgetting, a later look starts no second run, and the pass that
// found it running went on. The run ends with the session marked.
func TestForgetRunsOneAtATimeAndThePassGoesOn(t *testing.T) {
	t.Parallel()
	s, r, d := forgetSetup(t)
	forgetGroup(t, s, "codex", "herdr", "codex")
	session := ended(t, s, d, "codex")
	r.mu.Lock()
	r.hold, r.entered = make(chan struct{}), make(chan struct{}, 4)
	r.mu.Unlock()

	at := time.Now().Add(8 * 24 * time.Hour)
	dispatch.SetClock(d, func() time.Time { return at })
	passes(t, d, 1) // returns while the run is held in Forget
	select {
	case <-r.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the run never asked the harness")
	}
	// Two hours on, with the run still in flight, the look finds it running.
	at = at.Add(2 * time.Hour)
	passes(t, d, 2)
	if n := len(r.asked()); n != 1 {
		t.Fatalf("Forget was asked %d times with a run in flight, want 1", n)
	}
	if forgottenOf(t, s, session) != nil {
		t.Fatal("the session was marked before the harness answered")
	}
	close(r.hold)
	dispatch.WaitForgetIdle(d)
	if forgottenOf(t, s, session) == nil {
		t.Fatal("the session was not marked once the harness answered")
	}
	if n := len(r.asked()); n != 1 {
		t.Errorf("Forget was asked %d times in all, want 1", n)
	}
}

// The mark goes on the handle as it is when the harness has answered, not as
// the listing read it: a run is minutes long, and a write of the handle
// replaces the whole of it, so a key that another write added meanwhile would
// be lost to the mark.
func TestForgetKeepsWhatTheHandleHoldsNow(t *testing.T) {
	t.Parallel()
	s, r, d := forgetSetup(t)
	forgetGroup(t, s, "codex", "herdr", "codex")
	session := ended(t, s, d, "codex")
	r.mu.Lock()
	r.answer = func(string) (string, error) {
		ctx := context.Background()
		cur, err := s.GetSession(ctx, session)
		if err != nil {
			t.Errorf("reading the session while the harness works: %v", err)
			return "", nil
		}
		if err := s.SetHandle(ctx, session, maps.Clone(withKey(cur.Handle, "added_meanwhile", true))); err != nil {
			t.Errorf("writing the handle while the harness works: %v", err)
		}
		return "", nil
	}
	r.mu.Unlock()

	look(t, d, time.Now().Add(8*24*time.Hour))
	h := getSession(t, s, session).Handle
	if h["added_meanwhile"] != true || h[dispatch.Forgotten] == nil || h[store.HandleUsage] == nil {
		t.Fatalf("the handle = %v, want the mark beside the key written meanwhile and the count", h)
	}
}

// withKey is h and the key.
func withKey(h map[string]any, key string, v any) map[string]any {
	out := maps.Clone(h)
	out[key] = v
	return out
}

// A harness's program that is not found is not a session's fault, and no try of
// any session would mend it: the run ends at the first, no try is counted
// against it and none is told, and the next look goes on when the program is
// there. Thousands of sessions at a first look would otherwise all be told as
// failed, and given up in three hours for the machine's PATH.
func TestForgetStopsWhereTheProgramIsNotFound(t *testing.T) {
	t.Parallel()
	s, r, d := forgetSetup(t)
	forgetGroup(t, s, "codex", "herdr", "codex")
	sessions := []string{ended(t, s, d, "codex"), ended(t, s, d, "codex")}
	r.mu.Lock()
	r.answer = func(string) (string, error) {
		return "", fmt.Errorf("codex: %w", &exec.Error{Name: "codex", Err: exec.ErrNotFound})
	}
	r.mu.Unlock()

	at := time.Now().Add(8 * 24 * time.Hour)
	look(t, d, at)
	if n := len(r.asked()); n != 1 {
		t.Fatalf("Forget was asked %d times with its program not found, want 1: the run ends at the first", n)
	}
	for _, session := range sessions {
		sess := getSession(t, s, session)
		if sess.Handle[dispatch.Forgotten] != nil || sess.Handle[dispatch.ForgetTries] != nil {
			t.Errorf("session %s holds %v and %v after a program that was not found: no try was its own", session, sess.Handle[dispatch.Forgotten], sess.Handle[dispatch.ForgetTries])
		}
		if evs := eventKinds(t, s, sess.Bead, "session_forget_failed"); len(evs) != 0 {
			t.Errorf("session %s: %d session_forget_failed events for a program not found, want none", session, len(evs))
		}
	}

	// The program is there at the next look: both are forgotten, from the start.
	r.mu.Lock()
	r.answer = nil
	r.mu.Unlock()
	look(t, d, at.Add(time.Hour))
	for _, session := range sessions {
		if forgottenOf(t, s, session) == nil {
			t.Errorf("session %s was not forgotten once its program was found", session)
		}
	}
}

// Failures in a row end a run, whatever they say: a harness that is down or
// locked fails every session alike, and a run that went on would tell each of
// thousands. The ones that were tried keep their try, the oldest first, and the
// rest wait for the next look. A try that succeeds in between starts the count
// again, so what fails one in a few is not a harness that is down.
func TestForgetStopsAfterFailuresInARow(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		sessions int
		okAt     int // the nth try succeeds, 0 for none
		asked    int
	}{
		{"all fail", 7, 0, 5},
		{"a success between", 9, 5, 9},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, r, d := forgetSetup(t)
			forgetGroup(t, s, "codex", "herdr", "codex")
			var sessions []string
			for range c.sessions {
				sessions = append(sessions, ended(t, s, d, "codex"))
			}
			n := 0
			r.mu.Lock()
			r.answer = func(string) (string, error) {
				n++
				if n == c.okAt {
					return "", nil
				}
				return "", fmt.Errorf("codex: exit status 1: Error: failed to delete session")
			}
			r.mu.Unlock()

			look(t, d, time.Now().Add(8*24*time.Hour))
			if got := r.asked(); !slices.Equal(got, agentIDs(sessions[:c.asked])) {
				t.Fatalf("Forget was asked about %q, want the oldest %d: %q", got, c.asked, agentIDs(sessions[:c.asked]))
			}
			for i, session := range sessions {
				h := getSession(t, s, session).Handle
				switch {
				case i >= c.asked:
					if h[dispatch.ForgetTries] != nil || h[dispatch.Forgotten] != nil {
						t.Errorf("session %d was not tried and holds %v and %v", i, h[dispatch.ForgetTries], h[dispatch.Forgotten])
					}
				case i+1 == c.okAt:
					if h[dispatch.Forgotten] == nil {
						t.Errorf("session %d answered and was not marked", i)
					}
				case h[dispatch.ForgetTries] != float64(1):
					t.Errorf("session %d failed once and holds tries %v", i, h[dispatch.ForgetTries])
				}
			}
		})
	}
}

// agentIDs is the ids of the agents of the sessions.
func agentIDs(sessions []string) []string {
	ids := make([]string, len(sessions))
	for i, session := range sessions {
		ids[i] = agentID(session)
	}
	return ids
}
