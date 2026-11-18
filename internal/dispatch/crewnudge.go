package dispatch

import (
	"context"
	"time"

	"yardr.dev/yardr/internal/store"
)

// renudgeAfter is how long after the last nudge typed to a crew session
// what it was nudged about is told again, while that stands unread and the
// session sits idle.
//
// Found live (yardr-rx51): a yardmaster's pane got no nudge for half an hour
// while mail stood unread. A nudge is typed once and marked (Notified), so
// one that the agent did not act on (it came as a turn began, or its Enter
// was lost) was never said again until something newer landed, and the
// events that did land, with the session idle, were not newer than it.
//
// The interval runs from each typing and is kept while the agent works, so a
// session that answers every nudge and acks nothing is typed to once in this
// time, not at the end of each turn.
const renudgeAfter = 5 * time.Minute

// crewNudgeDue reports whether the inbox of s is due a nudge on this pass,
// page being its unread. It is when something is unread that no nudge told
// of, and again when what the last nudge told of still stands: this daemon
// knows of no nudge to the session (it restarted, which may have been blind
// to the last one, or the session is a new one, whose prime showed it the
// slice at its start) or the last was typed renudgeAfter ago. A nudge that
// is due may still not be typed: the gates after it hold it (crewNudgeHeld,
// and nudge for text in the input, a dialog, a login screen and a prompt
// given at a usage limit's reset that is not known to be taken), and what is
// not typed is not stamped, so it stays due.
//
// One not due yet is asked for again at its time, so that the pass wakes
// then: a standing unread wakes nothing of its own, since nothing is written.
func (d *Dispatcher) crewNudgeDue(s *memberSession, page *store.InboxPage) bool {
	if page.Last > s.st.Notified {
		return true
	}
	at, ok := d.nudgeAt[s.key()]
	if !ok {
		return true
	}
	if due := at.Add(renudgeAfter); d.now().Before(due) {
		d.lookAt(due)
		return false
	}
	return true
}

// stampNudge records that a nudge was typed to the session of key at at.
func (d *Dispatcher) stampNudge(key string, at time.Time) {
	if d.nudgeAt == nil {
		d.nudgeAt = map[string]time.Time{}
	}
	d.nudgeAt[key] = at
}

// crewInboxNudge types msg, the nudge about the inbox of s whose newest
// unread is last, and records what it said. A nudge about events no nudge
// told of marks them told (Notified); one that says again what was told does
// not, since the mark is where it was and a write of it would only wake the
// pass.
func (d *Dispatcher) crewInboxNudge(ctx context.Context, runner Runner, s *memberSession, msg string, last uint64) error {
	typed, err := d.nudge(ctx, runner, s, msg)
	if err != nil || !typed {
		return err
	}
	d.crewHarnessLook(s)
	if last <= s.st.Notified {
		return nil
	}
	return d.Store.MarkNotifiedFor(ctx, s.member.Name, s.user, last)
}
