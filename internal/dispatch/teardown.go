package dispatch

import (
	"context"
	"errors"
	"fmt"

	"yardr.dev/yardr/internal/store"
)

// teardown stops runner resources for sessions that ended, unless the group
// asks to keep them (config keep=true) for inspection; the bead of a session
// that ended as failed in such a group is held instead (holdKept). On the way
// it marks the count of a session whose agent is counted as due
// (markCountDue), having asked the runner once more for the id of the agent's
// session while its tab is still open (lastAgentSession).
//
// A Stop that fails leaves the session unstopped, and the next pass tries
// again. Until it lands route does not start the bead again if the session
// ended as failed (failedUnstopped).
func (d *Dispatcher) teardown(ctx context.Context, groups map[string]store.Group) error {
	ended, err := d.Store.Unstopped(ctx)
	if err != nil {
		return err
	}
	for _, s := range ended {
		g := groups[s.Group]
		r := d.runnerFor(s, groups)
		id := d.lastAgentSession(ctx, r, s)
		if r != nil && len(s.Handle) > 0 {
			if g.Config["keep"] == true {
				if err := d.holdKept(ctx, s); err != nil {
					return err
				}
			} else if err := r.Stop(ctx, s.Handle); err != nil {
				d.Log.Warn("stop", "session", s.ID, "err", err)
				continue
			}
		}
		if err := d.markCountDue(ctx, r, s, g, id); err != nil {
			return err
		}
		if err := d.Store.MarkStopped(ctx, s.ID); err != nil {
			return err
		}
	}
	return nil
}

// holdKept holds the bead of s, a session that ended as failed in a group that
// keeps its agents running. The agent is still there and, for all the yard
// knows, at work in the bead's checkout (a herdr group opens one worktree for
// a bead, and every session of it works there), and nothing here stops it, so
// an agent started beside it would share its files. Teardown marks a kept
// session stopped as it passes, so the hold is made here, not by a wait for
// the stop as for a group that stops its agents (failedUnstopped).
//
// The hold is the dispatcher's own, as the one after repeated failures is
// (route). The note before it says why and how to go on, and the waits page
// shows it as the hold's reason. A person or a crew member lifts the hold, with
// yardr bead unhold, once they have looked at the agent and closed its pane:
// an agent cannot lift one. A session that ended any other way is left alone:
// an agent that finished (done) is not waited for, nor is one its runner says
// is gone (died).
//
// It runs before the session is marked stopped and returns a store error, so
// that the session is listed again on the next pass and the bead is not
// started in between.
func (d *Dispatcher) holdKept(ctx context.Context, s store.Session) error {
	if s.State != "failed" {
		return nil
	}
	b, err := d.Store.GetBead(ctx, s.Bead)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	// A closed bead is not routed again, and a held one already waits for
	// someone.
	if b.Status == "closed" || b.Hold {
		return nil
	}
	why := ""
	if s.Error != nil && *s.Error != "" {
		why = ": " + *s.Error
	}
	d.Log.Warn("holding bead: the agent of its failed session is kept running", "bead", b.ID, "session", s.ID, "group", s.Group)
	note := fmt.Sprintf("Session %s ended as failed%s. Its group %s keeps its agents running (keep), so the agent may still be at work in the bead's worktree, "+
		"and the bead is held so that no second agent starts beside it. Look at what the agent did and close its pane, "+
		"then run `yardr bead unhold %s`: the bead starts again from its stage with fresh attempts.", s.ID, why, s.Group, b.ID)
	if err := d.Store.AddNote(ctx, b.ID, note, actor); err != nil {
		return err
	}
	_, err = d.Store.SetHold(ctx, b.ID, true, store.Expect{}, actor)
	return err
}

// failedUnstopped lists the beads that have a session that ended as failed and
// is not stopped yet, each with that session: its agent may still be at work in
// the bead's checkout, so route does not start the bead again until a later
// teardown stops it, however long the backoff that end set was. It reads the
// store and not what ended the session, so it covers every place that fails one
// (the limits, reap, the start's own worker), a failed Stop tried again by a
// daemon that replaced the one that ended it, and a place added later. A
// session with no handle opened nothing to stop. One that ended any other way
// (done, died, aborted, released, held) is not waited for.
func (d *Dispatcher) failedUnstopped(ctx context.Context) (map[string]store.Session, error) {
	ended, err := d.Store.Unstopped(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]store.Session{}
	for _, s := range ended {
		if s.State == "failed" && len(s.Handle) > 0 {
			out[s.Bead] = s
		}
	}
	return out, nil
}
