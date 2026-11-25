package dispatch

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"sync"
	"time"

	"yardr.dev/yardr/internal/agents"
	"yardr.dev/yardr/internal/store"
)

// A harness keeps a record of every session it ran, and some keep a lot (Codex
// keeps each thread's history in a database of its own, gigabytes of it). Once
// a session of the yard has ended, its tokens are counted and its kind's file
// says enough days have passed (store.ForgetSource), the yard makes the harness
// forget it, by the harness's own command. Nothing else is touched: the
// sessions are the ones the yard started and kept the harness's own id of
// (agent_session), so a person's own sessions with the harness are never asked
// about; a manual group's claim, a peer group's and an exec group's have no
// agent to forget; and a crew member's sessions are not sessions of the store.
//
// The pass looks once an hour (forgetEvery) and starts one goroutine for what
// it finds, as it does for the removal of a GOCACHE (gocache.go): the first look
// on a yard that has run for weeks finds the sessions of all of them, thousands,
// and each is a program started. The goroutine forgets them one at a time,
// oldest first, and the pass goes on without waiting for it; a look that finds
// it running leaves it.
//
// A session is forgotten once. A try that fails (the program exited with an
// error, or ran past its time) is logged as session_forget_failed and left for
// the next look, for forgetGiveUp tries: a harness cannot tell a session it no
// longer holds, which no try will remove, from a failure (Codex says "failed to
// delete session" to both), and without a bound such a session would fail every
// hour for ever, each with an event on its bead. The record says why it was
// given up.
//
// A run stops early, and the rest wait for the next look, where the fault is
// not a session's: the harness's program is not found (it is not on the
// daemon's PATH, or not installed), which no try of any session could mend and
// which is not counted against one; and forgetFailRun failures in a row, which
// a harness that is down or locked gives to every session alike. Without them
// the first look of a yard with thousands to forget would fail them all, tell
// each on its bead, and in three hours have given every one up for a fault of
// the machine's.

// Forgetter is implemented by runners that make an agent's harness forget a
// session that ended, by the id the dispatcher kept on its handle (AgentSession),
// without asking the agent's runtime anything: it no longer knows an agent whose
// tab is closed. A runner that starts no agent (manual, exec, peer) does not
// implement it.
//
// missing is why there is nothing to forget and none will be ("no forget", "no
// session id"): the record keeps it. err is for what to try again on a later
// look: the harness's command failed.
type Forgetter interface {
	Forget(ctx context.Context, handle map[string]any) (missing string, err error)
}

// The keys of a session's handle the forget step writes. Forgotten is the end of
// the session's forgetting, whether it was done or there was nothing to do:
// {at, missing}, missing only where it was not done and the reason is why (also
// "gave up after 3 tries: <the last error>"). ForgetTries is how many tries have
// failed so far, until then.
const (
	Forgotten   = "forgotten"
	ForgetTries = "forget_tries"
)

const (
	// forgetEvery is how often the pass looks for sessions to forget.
	forgetEvery = time.Hour
	// forgetGiveUp is how many failed tries end a session's forgetting.
	forgetGiveUp = 3
	// forgetFailRun is how many failed tries in a row end a run.
	forgetFailRun = 5
)

// forgetKeeping is the daemon's making harnesses forget old sessions.
type forgetKeeping struct {
	// last is when the pass last looked for sessions to forget, the zero time
	// before the first look. The pass owns it, as it does its siblings in
	// Dispatcher.
	last time.Time

	// mu guards done, which the goroutine of a run closes.
	mu sync.Mutex
	// done is open while a run is in flight, and nil otherwise.
	done chan struct{}
}

// forgetJob is a session to forget, and the runner that started it.
type forgetJob struct {
	s store.Session
	r Forgetter
}

// forgetSessions is the pass's step: once an hour it looks for the sessions to
// forget, and starts a run for them.
func (d *Dispatcher) forgetSessions(ctx context.Context, groups map[string]store.Group) {
	f := &d.forgetting
	f.mu.Lock()
	defer f.mu.Unlock()
	now := d.now()
	if f.done != nil || now.Sub(f.last) < forgetEvery {
		return
	}
	jobs, err := d.forgettable(ctx, groups, now)
	if err != nil {
		// The next pass looks again: a session is forgotten late rather than not.
		d.Log.Warn("sessions to forget", "err", err)
		return
	}
	f.last = now
	if len(jobs) == 0 {
		return
	}
	f.done = make(chan struct{})
	go d.forgetRun(ctx, f.done, jobs)
}

// forgettable are the sessions to forget at now, the oldest first: ones that
// ended, are stopped and counted, and are old enough by their kind's file, of
// the herdr runner that started them (the session's own record says which; the
// group may have been moved to another since), whose kind names a forget and
// that were not left running for a person (a group's keep: the count says kept,
// and the agent may be in use, writing to its record, however long after).
// None is listed that was forgotten or given up.
func (d *Dispatcher) forgettable(ctx context.Context, groups map[string]store.Group, now time.Time) ([]forgetJob, error) {
	// All of them and no Since: the store prunes an ended session at 90 days on
	// its own, and these tests pick the sessions.
	all, err := d.Store.ListSessions(ctx, store.SessionFilter{All: true})
	if err != nil {
		return nil, err
	}
	var jobs []forgetJob
	for _, s := range all {
		// A session of any other runner is left out here: a manual group's claim
		// (Manual), a peer group's and an exec group's start no agent.
		if !s.Stopped || s.EndedAt == nil || s.RunnerName() != "herdr" {
			continue
		}
		name, _ := s.Handle[store.HandleKind].(string)
		harness, ok := agents.Lookup(name)
		if !ok || harness.Forget == nil || !s.EndedAt.Before(now.AddDate(0, 0, -harness.Forget.AfterDays)) {
			continue
		}
		usage, counted := s.Handle[store.HandleUsage].(map[string]any)
		if kept, _ := usage["kept"].(bool); !counted || kept || s.Handle[Forgotten] != nil {
			continue
		}
		if r, ok := d.runnerFor(s, groups).(Forgetter); ok {
			jobs = append(jobs, forgetJob{s: s, r: r})
		}
	}
	slices.SortStableFunc(jobs, func(a, b forgetJob) int { return a.s.EndedAt.Compare(*b.s.EndedAt) })
	return jobs, nil
}

// forgetRun is one run on its goroutine: it forgets the jobs one at a time. A
// daemon that stops or replaces itself in the middle leaves the rest, which the
// next one lists again; so does a run that stops early (see above).
func (d *Dispatcher) forgetRun(ctx context.Context, done chan struct{}, jobs []forgetJob) {
	defer func() {
		f := &d.forgetting
		f.mu.Lock()
		f.done = nil
		f.mu.Unlock()
		close(done)
	}()
	failed := 0
	for _, j := range jobs {
		if ctx.Err() != nil {
			return
		}
		missing, err := j.r.Forget(ctx, j.s.Handle)
		switch {
		case ctx.Err() != nil:
			return // the daemon is stopping, and the session was not judged
		case errors.Is(err, exec.ErrNotFound):
			d.Log.Warn("sessions not forgotten: the harness's program is not found", "session", j.s.ID, "err", err)
			return
		}
		if !d.forgetRecord(ctx, j.s, missing, err) {
			failed = 0
			continue
		}
		if failed++; failed >= forgetFailRun {
			d.Log.Warn("sessions not forgotten: the harness failed on several in a row", "tries", failed)
			return
		}
	}
}

// forgetRecord records how making the harness forget the session s went, and
// says whether the try failed: the mark that ends its forgetting when it was
// done, when there was nothing to do, and when the tries are used up; else the
// count of tries and an event.
func (d *Dispatcher) forgetRecord(ctx context.Context, s store.Session, missing string, err error) (failed bool) {
	// The handle as it is now: a run is minutes long, and a write replaces the
	// whole of it.
	cur, gerr := d.Store.GetSession(ctx, s.ID)
	if gerr != nil {
		d.Log.Warn("record session forget", "session", s.ID, "bead", s.Bead, "err", gerr)
		return err != nil
	}
	h := cur.Handle
	if err != nil {
		tries := forgetTriesOf(h) + 1
		d.Log.Warn("session forget failed", "session", s.ID, "bead", s.Bead, "tries", tries, "err", err)
		kind, _ := h[store.HandleKind].(string)
		if lerr := d.Store.LogDispatch(ctx, s.Bead, "session_forget_failed", actor, map[string]any{
			"session": s.ID, "kind": kind, "tries": tries, "reason": err.Error()}); lerr != nil {
			d.Log.Warn("session_forget_failed event", "session", s.ID, "err", lerr)
		}
		if tries < forgetGiveUp {
			if serr := d.Store.SetHandle(ctx, s.ID, withHandle(h, ForgetTries, tries)); serr != nil {
				d.Log.Warn("record session forget", "session", s.ID, "bead", s.Bead, "err", serr)
			}
			return true
		}
		missing = fmt.Sprintf("gave up after %d tries: %v", tries, err)
	}
	mark := map[string]any{"at": d.now().UTC().Format(time.RFC3339)}
	if missing != "" {
		mark["missing"] = missing
	}
	if serr := d.Store.SetHandle(ctx, s.ID, withHandle(h, Forgotten, mark)); serr != nil {
		d.Log.Warn("record session forget", "session", s.ID, "bead", s.Bead, "err", serr)
	}
	return err != nil
}

// forgetTriesOf is how many tries have failed for the session whose handle is
// h. The store keeps a handle as JSON, which reads a number back as a float.
func forgetTriesOf(h map[string]any) int {
	switch n := h[ForgetTries].(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}
