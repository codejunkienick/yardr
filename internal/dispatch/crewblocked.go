package dispatch

import (
	"context"
	"fmt"
	"time"
)

// Notifier is implemented by runners that can show a notification to a
// human at this machine (herdr). The dispatcher tells through it beside
// yard-mail, which is what reaches away from the machine; a failure is
// logged where it is attempted, never returned.
type Notifier interface {
	Notify(ctx context.Context, title, body string) error
}

// crewNotified is the event logged on a crew member's journal once a
// human was told its session stands blocked. It is the memory behind the
// once-per-block rule, kept in the store so a daemon that replaces itself
// does not tell again. The kind is runner.Notified's, named here as a
// literal: runner imports dispatch, and dispatch names every other event
// kind it writes literally too.
const crewNotified = "human_notified"

// crewStatusLookback asks for every crew_status event: statuses are global,
// so another session can put a blocked session's latest transition any
// distance behind it.
const crewStatusLookback = int(^uint(0) >> 1)

// crewTellLookback bounds the journal read for a tell of the current
// spell, as AlreadyNotified's bounds a bead's: a blocked session is told
// nothing meanwhile, so its spell holds few events, and a tell once
// written sits near the journal's head.
const crewTellLookback = 1000

// crewBlocked tells the operator of the crew session s whose agent stands
// blocked, once per block: a blocked crew member halts the line, and a
// pane that waits on a permission prompt waits on a person. It is called
// on every pass that finds the session live, and judges in stall's shape:
// the spell's start is the session's latest crew_status, and the tell goes
// out once the spell is older than DefaultCrewBlockedLimit and carries no
// tell yet. A spell that ends resets by construction: the next spell's
// start stands past every earlier tell.
//
// The tell is yard-mail to the operator, which he reads where he reads
// his mail, beside a best-effort notification at this machine; then the
// event on the member's journal that is the memory. No operator setting
// means no mail to write: the pass logs and tells beside the machine
// only. Nothing here fails the pass: a tell that could not go out is
// tried again on the next.
func (d *Dispatcher) crewBlocked(ctx context.Context, runner Runner, s *memberSession, status string) {
	if status != "blocked" {
		return
	}
	start, at, ok := d.blockedSpellStart(ctx, s)
	if !ok {
		return
	}
	blockedFor := d.now().Sub(at)
	if blockedFor < DefaultCrewBlockedLimit {
		return
	}
	told, err := d.blockedTold(ctx, s, start)
	if err != nil || told {
		if err != nil {
			d.Log.Warn("blocked tell memory", "crew", s.member.Name, "session", s.label(), "err", err)
		}
		return
	}
	line := fmt.Sprintf("%s's %s has been blocked for %s; answer it in its pane, or run %s",
		s.member.Name, s.what(), round(blockedFor), s.restartCmd())
	if operator := d.operatorName(ctx); operator != "" {
		if err := d.Store.Mail(ctx, operator, line, actor); err != nil {
			d.Log.Warn("tell of a blocked crew session", "crew", s.member.Name, "session", s.label(), "err", err)
			return
		}
	} else {
		d.Log.Info("blocked crew session, no operator to mail", "crew", s.member.Name, "session", s.label())
	}
	if n, ok := runner.(Notifier); ok {
		if err := n.Notify(ctx, "yardr: "+s.member.Name+" blocked", line); err != nil {
			d.Log.Warn("blocked tell notification", "crew", s.member.Name, "session", s.label(), "err", err)
		}
	}
	data := s.who(map[string]any{"blocked": round(blockedFor).String(), "tail": d.blockedTail(ctx, runner, s)})
	if err := d.Store.LogDispatch(ctx, s.member.Journal, crewNotified, actor, data); err != nil {
		d.Log.Warn("blocked tell event", "crew", s.member.Name, "session", s.label(), "err", err)
	}
}

// blockedSpellStart is the current blocked spell's start: the seq and time
// of the session's latest crew_status, which open a spell only when that
// status is blocked. ok is false where the session's latest status in the
// window is not blocked, or the spell cannot be dated: without a start no
// tell goes out.
func (d *Dispatcher) blockedSpellStart(ctx context.Context, s *memberSession) (start uint64, at time.Time, ok bool) {
	evs, err := d.Store.EventsOfKind(ctx, "crew_status", crewStatusLookback)
	if err != nil {
		d.Log.Warn("blocked spell start", "crew", s.member.Name, "session", s.label(), "err", err)
		return 0, time.Time{}, false
	}
	for i := len(evs) - 1; i >= 0; i-- {
		e := evs[i]
		if !crewSessionEvent(e, s.member.Name, s.user) {
			continue
		}
		if e.Data["status"] != "blocked" {
			return 0, time.Time{}, false
		}
		return e.Seq, e.At, true
	}
	return 0, time.Time{}, false
}

// blockedTold reports whether the spell that started at start was told
// already: a human_notified for the session on the member's journal past
// the spell's start. The scan stops at the spell's start; a window that
// ends inside the spell has not shown the whole of it, and the tell goes
// out: the event it writes is then the memory, so at most one tell stands
// untold twice.
func (d *Dispatcher) blockedTold(ctx context.Context, s *memberSession, start uint64) (bool, error) {
	evs, err := d.Store.Events(ctx, s.member.Journal, crewTellLookback)
	if err != nil {
		return false, err
	}
	for i := len(evs) - 1; i >= 0; i-- {
		e := evs[i]
		if e.Seq <= start {
			return false, nil
		}
		if e.Kind == crewNotified && crewSessionEvent(e, s.member.Name, s.user) {
			return true, nil
		}
	}
	return false, nil
}

// blockedTail is the end of the pane of s's agent, as the tell's event
// carries it: "" where the runner has no pane to read.
func (d *Dispatcher) blockedTail(ctx context.Context, runner Runner, s *memberSession) string {
	t, ok := runner.(Tailer)
	if !ok {
		return ""
	}
	done := askTimer(ctx, askPane)
	tail, err := t.Tail(ctx, s.st.Handle, stallTail)
	done()
	if err != nil {
		return "(pane not readable: " + err.Error() + ")"
	}
	return tail
}
