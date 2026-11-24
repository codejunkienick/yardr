package dispatch

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// ContextReader is implemented by runners that read the context a
// session's agent holds, in tokens (herdr reads the agent's own session
// file). ok is false when the session's kind names no source, or its
// file says no use yet. window is declared or read with the use, 0 for
// the model name's window. startedAt matches a session file by its worktree
// when herdr names no session id.
type ContextReader interface {
	Context(ctx context.Context, handle map[string]any, startedAt time.Time) (used, window int, ok bool, err error)
}

// defaultContextHandoff is the percent of the context window at which an
// idle crew session is asked for a handoff when its member sets none.
const defaultContextHandoff = 60

// contextThreshold is the percent of the context window at which an idle
// crew session is asked for a handoff: the member's setting, the default
// when unset, 0 when the member never asks. What the store refused to
// save reads as the default.
func contextThreshold(s string) int {
	if s == "" {
		return defaultContextHandoff
	}
	if n, err := strconv.Atoi(s); err == nil && n >= 0 && n <= 100 {
		return n
	}
	return defaultContextHandoff
}

// contextWhileWorking is how long a working session's context read stands
// before the next. A dispatch pass can wake on every store write, and each
// read may ask herdr which session the agent is and then write the reading.
// An idle pass always reads: the handoff decision needs the use the session
// holds then. 30s is the default pass interval.
const contextWhileWorking = 30 * time.Second

// crewContext reads the session's context use and, when idle past its
// threshold, asks once for a handoff. asked is true when the nudge went
// out on this pass. The ask is never forced: no grace, no restart,
// and it is asked again only in a new session. A session already asked
// for its age is not asked for its use too. idle is false on a working
// pass and on the pass that restarts a session for its age: the reading
// is recorded and nothing is asked. The caller decides which passes read;
// a working pass reads on a cadence (contextWhileWorking).
func (d *Dispatcher) crewContext(ctx context.Context, runner Runner, s *memberSession, idle bool) (asked bool, err error) {
	def := s.member
	r, ok := runner.(ContextReader)
	if !ok {
		return false, nil
	}
	var startedAt time.Time
	if s.st.StartedAt != nil {
		startedAt = *s.st.StartedAt
	}
	used, window, ok, err := r.Context(ctx, s.st.Handle, startedAt)
	if err != nil {
		return false, err
	}
	// A read that answered ends the failure said before it, on whichever pass
	// it was made: the same words after it are a new failure, said again.
	d.noteContextErr(s, nil)
	if !ok {
		return false, nil
	}
	model, _ := def.Config["model"].(string)
	if window == 0 {
		window = ContextWindow(model)
	}
	pct := used * 100 / window
	if err := d.Store.SetContextFor(ctx, def.Name, s.user, pct, window); err != nil {
		return false, err
	}
	threshold := contextThreshold(def.ContextHandoff)
	if !idle || threshold <= 0 || pct < threshold || s.st.ContextAskedAt != nil || s.st.RotationAskedAt != nil {
		return false, nil
	}
	typed, err := d.nudge(ctx, runner, s, fmt.Sprintf(
		"Your context is %d%% of the window. Write your handoff when this conversation allows: yardr crew handoff %s - with the words on stdin: what you are doing, what is next, what to watch.",
		pct, def.Name))
	if err != nil || !typed {
		return false, err // not asked yet: the reading above is still recorded
	}
	return true, d.Store.MarkContextAskedFor(ctx, def.Name, s.user, pct, window)
}

// contextFailKey and contextAtKey are d.status keys for one session, beside
// its own status: the last context-read error already logged, and when the
// last working read was attempted. The separator keeps them from being a
// session key. They travel with the session's crew memory, since the keep
// runs on a clone.
func contextFailKey(key string) string { return key + "\x00context" }
func contextAtKey(key string) string   { return key + "\x00context-at" }

// forgetContextNote drops the session's context-read memory. A stopped
// session's key is reused by the next one, which must read at once and say
// a new failure.
func (d *Dispatcher) forgetContextNote(key string) {
	if d.status == nil {
		return
	}
	delete(d.status, contextFailKey(key))
	delete(d.status, contextAtKey(key))
}

// contextDue reports whether a working session's context is read on this
// pass. The first read is due at once. A later one waits out
// contextWhileWorking from the last attempt, failed or not, so a reader
// that stays down is not asked on every wake.
func (d *Dispatcher) contextDue(s *memberSession) bool {
	raw, ok := d.status[contextAtKey(s.key())]
	if !ok || raw == "" {
		return true
	}
	at, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return true
	}
	return !d.now().Before(at.Add(contextWhileWorking))
}

// stampContextRead marks that a working session's context was just asked for.
func (d *Dispatcher) stampContextRead(s *memberSession) {
	if d.status == nil {
		d.status = map[string]string{}
	}
	d.status[contextAtKey(s.key())] = d.now().UTC().Format(time.RFC3339Nano)
}

// noteContextErr says a context read failed, once per session until the
// error changes or a later read succeeds (crewContext ends the note then,
// with a nil error). The keep itself does not fail: the pass still clears the
// working state, and the next due read tries again.
func (d *Dispatcher) noteContextErr(s *memberSession, err error) {
	key := contextFailKey(s.key())
	if err == nil {
		delete(d.status, key)
		return
	}
	msg := err.Error()
	if d.status[key] == msg {
		return
	}
	if d.status == nil {
		d.status = map[string]string{}
	}
	d.status[key] = msg
	d.Log.Warn("crew context", "crew", s.member.Name, "session", s.label(), "err", err)
}

// crewWorkingPass reads context use when it is due, then clears the working
// state whether or not the read succeeded. Nothing is asked: a handoff waits
// for an idle pass.
func (d *Dispatcher) crewWorkingPass(ctx context.Context, runner Runner, s *memberSession, status string) {
	if d.contextDue(s) {
		_, err := d.crewContext(ctx, runner, s, false)
		d.stampContextRead(s)
		if err != nil {
			d.noteContextErr(s, err)
		}
	}
	d.crewWorkingOver(ctx, s, status)
}

// recordContextOnRestart reads context on the pass that restarts a session
// for its age, without asking for a handoff, and clears a working session's
// marks. A failed read is said once and does not hold the restart.
func (d *Dispatcher) recordContextOnRestart(ctx context.Context, runner Runner, s *memberSession, status string) {
	if _, err := d.crewContext(ctx, runner, s, false); err != nil {
		d.noteContextErr(s, err)
	}
	if status != "idle" && status != "done" {
		d.crewWorkingOver(ctx, s, status)
	}
}

// crewWorkingOver drops what a working session no longer sits at: a draft or
// dialog remembered from an idle spell, the login mark when the runtime says
// working (a blocked status can still be a login screen, so that mark stays),
// and the harness marks of the idle spell, the try's wait only on observed
// work (an unknown status says nothing of it, so it stands). It runs whether
// or not this pass read context, so a read that keeps failing cannot leave
// those marks in place.
func (d *Dispatcher) crewWorkingOver(ctx context.Context, s *memberSession, status string) {
	key := s.key()
	delete(d.drafts, key) // what was in its input was sent
	delete(d.dialogs, key)
	if status == "working" {
		d.markCrewLogin(ctx, s, "")
	}
	d.crewHarnessDialogOver(ctx, s)
	d.crewHarnessOver(ctx, s, status, false)
}
