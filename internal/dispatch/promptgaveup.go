package dispatch

import (
	"context"
	"fmt"

	"yardr.dev/yardr/internal/store"
)

// promptGaveUp ends the bead session s whose first prompt pass gave up on:
// failed, so the bead is released and routed again after the backoff, and
// held once its stage has failed MaxAttempts times, as for any failed
// session. A note on the bead says why, once the end has landed; a pass
// that finds prompt_lost stored ends a session an earlier end missed. It
// returns the end's error: a session it could not end still runs.
//
// Found live (yardr-w6w3): five Codex sessions gave up on their first
// prompt within a minute of their start, and each stayed the owner of its
// bead, idle and silent: the stall check passes over a session whose prompt
// is still on its handle, and the idle limit only tells, with no grace set,
// so nothing ended them, and one sat six hours until a person released it.
// An agent that never got its first prompt has done nothing that a fresh
// start loses. A crew member has no bead to give back and is left as it
// was, reported.
func (d *Dispatcher) promptGaveUp(ctx context.Context, s store.Session, pass promptPass) error {
	why := fmt.Sprintf("the first prompt was sent %d times and never showed in the agent's pane", pass.sends)
	switch {
	case pass.queued:
		why = "the first prompt was taken into the harness's queue and the harness never started a turn on it"
	case pass.unsent:
		why = fmt.Sprintf("the first prompt stayed in the agent's input after Enter was sent %d times", pass.submits)
	case !pass.gaveUp:
		why = "the first prompt was given up on an earlier pass (session_prompt_gave_up), which could not end the session"
	}
	if err := d.end(ctx, s, "failed", why); err != nil {
		return err
	}
	note := fmt.Sprintf("Session %s stopped by the yard: %s. The bead is released for a fresh start; "+
		"session_prompt_gave_up has what its pane showed.", s.ID, why)
	if err := d.Store.AddNote(ctx, s.Bead, note, actor); err != nil {
		d.Log.Warn("note on a session whose first prompt was given up", "session", s.ID, "bead", s.Bead, "err", err)
	}
	return nil
}
