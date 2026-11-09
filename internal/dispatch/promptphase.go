package dispatch

import (
	"context"
	"time"
)

// promptPhase is where a pending first prompt stands, as the handle stored
// it: pendingPrompt reads it before it looks at the pane, so that no look,
// least of all one that failed, overrides what an earlier pass found out
// (yardr-w6w3: each of four review rounds found a look read before the
// stored state, and an unknown look taken as evidence).
type promptPhase int

const (
	// phaseDelivering: sent or not, and not known to be taken.
	phaseDelivering promptPhase = iota
	// phaseQueued: taken into the harness's queue (PromptQueuedAt).
	phaseQueued
	// phaseGivenUp: given up (prompt_lost).
	phaseGivenUp
)

func (p promptPhase) String() string {
	return [...]string{"delivering", "queued", "given_up"}[p]
}

// promptPhaseOf derives the phase from the keys a handle already carries,
// so that a handle a daemon stored before this reads the same: given up
// over queued over delivering.
func promptPhaseOf(h map[string]any) promptPhase {
	switch {
	case h[promptLost] == true:
		return phaseGivenUp
	case h[PromptQueuedAt] != nil:
		return phaseQueued
	}
	return phaseDelivering
}

// look is what one look at a pane found: unknown when it failed or was not
// taken, which is no evidence either way.
type look int

const (
	lookUnknown look = iota
	lookNo
	lookYes
)

func lookOf(v, known bool) look {
	switch {
	case !known:
		return lookUnknown
	case v:
		return lookYes
	}
	return lookNo
}

// promptLooks is what a pass saw of the prompt: echoed (shown), in the
// input (typed), in the harness's queue (queued), whether the harness's
// screen is up, and whether it is at a question drawn in place of its input
// (dialog), which hides the queue as it hides the input.
type promptLooks struct{ shown, typed, queued, screen, dialog look }

// promptCase is all a decision on a queued or given-up prompt rests on.
type promptCase struct {
	phase  promptPhase
	looks  promptLooks
	status string
	// sent: the dispatcher sent it, or the runner did and could not confirm it.
	sent bool
	now  time.Time
	// queuedAt is when it was first seen queued; budget is how long a queued
	// prompt waits for its turn from then, and wait the time to the next look.
	queuedAt     time.Time
	budget, wait time.Duration
	// ends: a give-up ends the session (a bead's); a crew member's is left.
	ends bool
}

type promptAct int

const (
	actDeliver promptAct = iota // the delivering phase's own rules decide
	actWait                     // nothing is typed, nothing arrives; look again at until, if set
	actArrive                   // the agent has the prompt
	actGiveUp                   // give it up now: store prompt_lost, then end a bead's session
	actEnd                      // given up already: end the bead's session
	actDialog                   // at a question, or not telling: today's trust/blocked handling
)

type promptDecision struct {
	act   promptAct
	until time.Time
	// shown and left say how it arrived: echoed, or gone from a readable
	// queue; neither when the agent was seen working.
	shown, left bool
}

// decidePrompt decides a queued or given-up prompt from its phase first and
// the looks second. It does no I/O: pendingPrompt looks and acts. Nothing it
// returns types anything or presses Enter; a look it needs that is unknown
// waits.
func decidePrompt(c promptCase) promptDecision {
	switch c.phase {
	case phaseGivenUp:
		if c.ends {
			// Terminal: ended on every pass until the end lands.
			return promptDecision{act: actEnd}
		}
		// A crew member is left to a human, still watched for the prompt it
		// is given by hand, as before.
		switch {
		case c.looks.shown == lookYes:
			return promptDecision{act: actArrive, shown: true}
		case (c.sent || c.looks.typed == lookYes || !c.queuedAt.IsZero()) && c.status == "working",
			c.sent && c.looks.shown == lookUnknown:
			return promptDecision{act: actArrive}
		}
		return promptDecision{act: actWait}
	case phaseQueued:
		l, again := c.looks, promptDecision{act: actWait, until: c.now.Add(c.wait)}
		switch {
		case l.shown == lookYes:
			return promptDecision{act: actArrive, shown: true}
		case c.status == "working":
			return promptDecision{act: actArrive}
		case c.status != "idle" && c.status != "done":
			return promptDecision{act: actDialog}
		case l.shown == lookUnknown, l.typed != lookNo, l.queued == lookUnknown, l.screen != lookYes, l.dialog != lookNo:
			// Typed means someone took it back out of the queue: no Enter.
			// A dialog has no queue to read.
			return again
		case l.queued == lookNo:
			return promptDecision{act: actArrive, left: true}
		}
		if due := c.queuedAt.Add(c.budget); c.now.Before(due) {
			return promptDecision{act: actWait, until: due}
		}
		return promptDecision{act: actGiveUp}
	}
	return promptDecision{act: actDeliver}
}

// promptShown reports whether r sees prompt in the session of handle, and
// whether it could look at all.
func promptShown(ctx context.Context, r Runner, handle map[string]any, prompt string) (shown, known bool) {
	w, ok := r.(PromptWatcher)
	if !ok {
		return false, false
	}
	done := askTimer(ctx, askPane)
	shown, err := w.PromptShown(ctx, handle, prompt)
	done()
	return shown && err == nil, err == nil
}

// promptLook looks for prompt in the pane of handle as the delivering phase
// reads it: whether it is echoed, then typed, then queued, each with
// whether that look could be taken. A look not taken because an earlier one
// answered says false and known; a runner with no input or queue to read
// has the prompt in neither.
func (d *Dispatcher) promptLook(ctx context.Context, r Runner, handle map[string]any, p string) (shown, known, typed, typedKnown, queued, queuedKnown bool) {
	shown, known = promptShown(ctx, r, handle, p)
	typedKnown, queuedKnown = known, true
	if sub, ok := r.(PromptSubmitter); ok && known && !shown {
		done := askTimer(ctx, askPane)
		var err error
		typed, err = sub.PromptTyped(ctx, handle, p)
		done()
		typedKnown = err == nil
	}
	if known && !shown && !typed {
		queued, queuedKnown = promptQueued(ctx, r, handle, p)
	}
	return shown, known, typed, typedKnown, queued, queuedKnown
}

// looksOf is promptLook's answer as looks: one that was not taken, because
// the pane could not be read or an earlier look answered, is unknown.
func looksOf(shown, known, typed, typedKnown, queued, queuedKnown bool) promptLooks {
	l := promptLooks{shown: lookOf(shown, known)}
	if l.shown == lookNo {
		l.typed = lookOf(typed, typedKnown)
		if !typed {
			l.queued = lookOf(queued, queuedKnown)
		}
	}
	return l
}

// promptLooks is the looks of the prompt in the pane of handle, the screen's
// left unknown.
func (d *Dispatcher) promptLooks(ctx context.Context, r Runner, handle map[string]any, p string) promptLooks {
	return looksOf(d.promptLook(ctx, r, handle, p))
}

// screenLook is whether the harness of handle has drawn its screen: a
// runner that cannot tell says nothing.
func (d *Dispatcher) screenLook(ctx context.Context, r Runner, handle map[string]any) look {
	sw, ok := r.(ScreenWatcher)
	if !ok {
		return lookUnknown
	}
	done := askTimer(ctx, askPane)
	up, known := sw.HarnessScreen(ctx, handle)
	done()
	return lookOf(up, known)
}

// dialogLook is whether the harness of handle is at a question it draws in
// place of its input (DialogReader): a runner that knows none says no.
func (d *Dispatcher) dialogLook(ctx context.Context, r Runner, handle map[string]any) look {
	dr, ok := r.(DialogReader)
	if !ok {
		return lookNo
	}
	done := askTimer(ctx, askPane)
	dialog, _, err := dr.InputDialog(ctx, handle)
	done()
	return lookOf(dialog != "", err == nil)
}

// promptDecided acts on dec, decided for the prompt of handle, on pass; ends
// says a give-up ends the session.
// What a give-up allows is stored first: prompt_lost is on the handle the
// caller stores before it ends the session.
func (d *Dispatcher) promptDecided(ctx context.Context, r Runner, key string, handle map[string]any, pass promptPass, dec promptDecision, ends bool) promptPass {
	switch dec.act {
	case actArrive:
		delete(d.promptIdle, key)
		delete(d.drafts, key)
		delete(d.dialogs, key)
		delete(d.logins, key)
		pass.arrived, pass.shown, pass.left, pass.handle = true, dec.shown, dec.left, withoutPending(handle)
	case actWait:
		if !dec.until.IsZero() {
			d.lookAt(dec.until)
		}
	case actGiveUp:
		pass.gaveUp, pass.queued, pass.handle = true, true, withHandle(handle, promptLost, true)
		pass.end = ends
		if t, ok := r.(Tailer); ok {
			done := askTimer(ctx, askPane)
			pass.tail, _ = t.Tail(ctx, handle, stallTail)
			done()
		}
	case actEnd:
		pass.end = true
	}
	return pass
}
