package dispatch

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"
	"unicode"

	"yardr.dev/yardr/internal/limits"
)

// A crew member stopped on its own model's usage window need not sit
// until that window resets when another model of its kind still
// carries: on 22 Nov 2026 the yardmaster (claude, Fable) sat on its
// Fable limit from 23:31 to 07:52, mail unanswered, while the shared
// weekly window stood at 83% and Opus was free (yardr-z1cf). So a crew
// member may name fallback_model beside model, of the same kind (the
// store refuses another kind's at set time), and a model-window stop
// with the shared windows open restarts it on that model instead of
// prompting it at the reset:
//
//   - The stop is crewHarnessError's, as ever. Its window is
//     UsedUpForStop's, which names a model window only when no shared
//     window is used up, so w.Model != "" is the whole trigger beside
//     fallback_model being named and the session not already being on
//     its fallback. No provider word ("Fable") is matched against the
//     config's model id: the fresh limit error in this agent's pane
//     proves this agent stopped while only a model window is used up.
//   - The restart is a crew restart as yardr crew restart asks for it
//     (RequestRestartFor), never a /model typed into the pane: /model
//     opens the harness's interactive picker, which yardr never
//     answers. And it never waits on a handoff from the stopped
//     session: any answer it gave would draw on the used-up model, and
//     the harness shows the limit line instead of running the turn.
//     The next session primes from the last journal handoff and the
//     unread inbox, as a forced rotation's does.
//   - A start with no fallback keys on the session it replaces — a
//     handoff restart, say — has no pane error to prove its model is
//     the used-up one, so crewFallbackJob reads the cached limits
//     itself: a model window used up whose word matches the config's
//     model id (windowModelMatches) starts it on its fallback at
//     once, told once as a start. A shared window, a window of
//     another model, an open window and an unknown one all start it
//     on its own model as before, and a later stop is the stop
//     path's business as ever.
//   - The state is two handle keys (harnessFallbackModel,
//     harnessFallbackReturnAt, harness.go): set on the stopped session
//     with the error's mark, carried by the start that replaces it
//     (crewFallbackJob, crewstarts.go) and written onto the new
//     session's handle with CrewStartedFor itself. The member's config
//     is never swapped, so the model it returns to is always the
//     config's.
//   - While on the fallback, crewFallbackReturn schedules a wake at
//     the return time on every pass (lookDue resets each pass). Once
//     that time arrives it asks for a handoff as a rotation does — a
//     session on its fallback can write one — and restarts it back on
//     its own model after the grace if no handoff comes. Either way,
//     the start whose old handle is past its return time runs on the
//     config's model and carries nothing, clearing the spell and
//     telling the operator once.
//   - Each move is told to the operator once, in crewBlocked's shape:
//     mail, then the human_notified on the journal. Once is the
//     standing mark's doing for the failover (the branch runs once per
//     spell, crewHarnessError) and the cleared handle's for the return.
//
// With no fallback named the stop stands as it always has — the reset
// prompt of harness_reset.go, past harnessResetMax no wake at all —
// plus the one mail that names what would have rescued it.

// fallbackRestartPrefix marks a restart asked for by crewFallbackWake
// so crewFallbackJob recognises a failover restart even when the
// provider gave no reset time (and therefore no return-at key stands on
// the handle): any later restart then returns to the primary model.
const fallbackRestartPrefix = "model limit: "

// crewFallbackWake is the failover half of crewHarnessError's
// rate-limited branch: it reports whether it took the stop over, with
// the wake the error's mark begins with (the two fallback keys, no
// reset wait: a typed prompt cannot switch models) and the words for
// the event's hint. With no fallback named it tells the operator so
// and reports false, and the caller plans the reset wake as ever. A
// restart it could not ask for is likewise the caller's again.
func (d *Dispatcher) crewFallbackWake(ctx context.Context, s *memberSession, w *limits.Window, data map[string]any) (wake map[string]any, said string, handled bool) {
	if w == nil || w.Model == "" {
		return nil, "", false // no window, or a shared one: the reset's business
	}
	if on, _ := s.st.Handle[harnessFallbackModel].(string); on != "" {
		return nil, "", false // already on its fallback: no failing over twice
	}
	fallback, _ := s.member.Config["fallback_model"].(string)
	primary, _ := s.member.Config["model"].(string)
	if fallback == "" {
		d.tellCrewFallback(ctx, s, fmt.Sprintf("%s's %s stopped on its model's usage window (%s) and names no fallback_model, so it waits for the reset as before; "+
			"setting one (yardr crew set %s fallback_model=<a model of its kind>) would restart it on that model at the next such stop",
			s.member.Name, s.what(), w.Label, s.member.Name),
			map[string]any{"fallback": "none", "window": w.Label})
		return nil, "", false
	}
	if primary != "" && fallback == primary {
		d.Log.Warn("crew session's fallback_model is the same as its model; skipping failover",
			"crew", s.member.Name, "session", s.label(), "model", primary)
		return nil, "", false
	}
	reason := fmt.Sprintf("%sits %s window is used up: restarting on fallback model %s", fallbackRestartPrefix, w.Label, fallback)
	if err := d.Store.RequestRestartFor(ctx, s.member.Name, s.user, reason, "", actor); err != nil {
		d.Log.Warn("ask for a crew session's model fallback restart", "crew", s.member.Name, "session", s.label(), "err", err)
		return nil, "", false
	}
	wake = map[string]any{harnessFallbackModel: fallback}
	var mailLine, hint string
	if w.ResetsAt != nil {
		now := d.now()
		at := *w.ResetsAt
		if at.Before(now) {
			at = now
		}
		at = at.Add(harnessResetLag)
		wake[harnessFallbackReturnAt] = at.UTC().Format(time.RFC3339Nano)
		d.lookAt(at)
		returns := "at " + limits.Clock(at, now)
		mailLine = fmt.Sprintf("%s's %s stopped on its model's usage window (%s) and yardr is restarting it on its fallback model %s; "+
			"it returns to %s %s, from its last handoff and its unread inbox",
			s.member.Name, s.what(), w.Label, fallback, orModel(primary), returns)
		hint = fmt.Sprintf("yardr is restarting it on its fallback model %s, and back on %s %s; ", fallback, orModel(primary), returns)
	} else {
		mailLine = fmt.Sprintf("%s's %s stopped on its model's usage window (%s) and yardr is restarting it on its fallback model %s, "+
			"from its last handoff and its unread inbox; the provider does not say when that window resets, so run `%s` when ready to return to %s",
			s.member.Name, s.what(), w.Label, fallback, s.restartCmd(), orModel(primary))
		hint = fmt.Sprintf("yardr is restarting it on its fallback model %s (no reset time given; run `%s` to return to %s); ",
			fallback, s.restartCmd(), orModel(primary))
	}
	data["fallback_model"] = fallback
	d.tellCrewFallback(ctx, s, mailLine,
		map[string]any{"fallback": "failover", "window": w.Label, "fallback_model": fallback})
	return wake, hint, true
}

// orModel is a crew member's own model as a mail names it: the config's,
// or its harness's own where it names none.
func orModel(model string) string {
	if model == "" {
		return "its own model"
	}
	return "the model " + model
}

// tellCrewFallback tells the operator of a fallback move of s, once by
// its caller's construction (see the head of this file): yard-mail
// where an operator is set, the log where none is, and the
// human_notified on the member's journal, as crewBlocked's tells go.
// A mail that could not go out is logged and the journal event still
// written: the move itself has happened, and the event is its record.
func (d *Dispatcher) tellCrewFallback(ctx context.Context, s *memberSession, line string, data map[string]any) {
	if operator := d.operatorName(ctx); operator != "" {
		if err := d.Store.Mail(ctx, operator, line, actor); err != nil {
			d.Log.Warn("tell of a crew session's model fallback", "crew", s.member.Name, "session", s.label(), "err", err)
		}
	} else {
		d.Log.Info("crew session's model fallback, no operator to mail", "crew", s.member.Name, "session", s.label(), "line", line)
	}
	if err := d.Store.LogDispatch(ctx, s.member.Journal, crewNotified, actor, s.who(data)); err != nil {
		d.Log.Warn("crew model fallback tell event", "crew", s.member.Name, "session", s.label(), "err", err)
	}
}

// crewFallbackReturn is the return half, called on every pass of a
// live crew session: while the return time is still ahead it schedules
// a wake for it (lookDue is cleared at the start of every pass). Once
// the reset has come it asks for a handoff as a rotation asks (the
// session runs on the fallback and can write one), and after the grace
// asks for a restart back on its own model. It reports whether it dealt
// with the session, so the caller types nothing else into it this pass.
// Both paths — a handoff written within grace and a forced restart
// after grace — return through crewFallbackJob, which clears the spell
// and tells the operator once.
func (d *Dispatcher) crewFallbackReturn(ctx context.Context, runner Runner, s *memberSession, status string, grace time.Duration) bool {
	fallback, _ := s.st.Handle[harnessFallbackModel].(string)
	if fallback == "" {
		return false
	}
	due, ok := handleTime(s.st.Handle, harnessFallbackReturnAt)
	if !ok {
		return false
	}
	if d.now().Before(due) {
		d.lookAt(due)
		return false
	}
	if s.st.RestartRequested != "" {
		return true // the restart asked for carries it back; nothing else this pass
	}
	def := s.member
	primary, _ := def.Config["model"].(string)
	if s.st.RotationAskedAt == nil {
		if status != "idle" && status != "done" {
			return false // asked when it next idles, as a rotation is
		}
		typed, err := d.nudge(ctx, runner, s, fmt.Sprintf(
			"yardr: the usage window of the model you were moved off has reset. Write your handoff now: yardr crew handoff %s - with the words on stdin: what you are doing, what is next, what to watch. "+
				"yardr then starts your next session on %s.", def.Name, orModel(primary)))
		if err != nil || !typed {
			return err != nil // typed nowhere yet: looked at again next pass
		}
		if err := d.Store.MarkRotationAskedFor(ctx, def.Name, s.user); err != nil {
			d.Log.Warn("mark crew session's fallback return asked", "crew", def.Name, "session", s.label(), "err", err)
		}
		return true
	}
	if left := s.st.RotationAskedAt.Add(grace); !d.now().After(left) {
		d.lookAt(left)
		return false // the handoff's grace runs; it works on as it is
	}
	if err := d.Store.RequestRestartFor(ctx, def.Name, s.user,
		fmt.Sprintf("its model's usage window has reset: returning from fallback model %s", fallback), "", actor); err != nil {
		d.Log.Warn("ask for a crew session's return from its fallback", "crew", def.Name, "session", s.label(), "err", err)
		return false
	}
	return true
}

// windowModelMatches reports whether the provider's word for a model
// window (Window.Model, in its own words: "Fable") names the model id
// a member is configured with ("claude-fable-5-2"): the word, lowercased,
// stands among the id's words, the id split on runs of what is neither
// a letter nor a digit and lowercased too. It is the one comparison a
// start may make: no table of provider words to model ids, and no
// looser match, since a failover onto another model's window returns
// only at that window's reset. An empty word or an empty id (the
// harness's own default) matches nothing.
func windowModelMatches(word, id string) bool {
	word = strings.ToLower(strings.TrimSpace(word))
	if word == "" || id == "" {
		return false
	}
	for _, w := range strings.FieldsFunc(strings.ToLower(id), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if w == word {
			return true
		}
	}
	return false
}

// crewFallbackJob is the start keepSession hands over for s: on a
// member cloned with its fallback as its model, where the session it
// replaces was on its fallback (or failing over to it) and its return
// time is still ahead, with the two keys for the new session's handle
// on the job. When the return time has passed — whether from a handoff
// written within grace, a forced restart after grace, or an operator's
// restart — or when no return time was known and this is no longer the
// failover restart itself, the member is as the store has it and no key
// is carried: the new session runs on the config's model, the spell is
// over, and the operator is told once.
//
// Where the session it replaces carries no fallback keys at all, the
// start itself is judged (see the head of this file): the cached
// limits of the member's kind, read as a stop reads them, and the
// member starts on its fallback where that names its own model's used
// up window.
func (d *Dispatcher) crewFallbackJob(ctx context.Context, s *memberSession, runner Runner, starter CrewRunner) crewJob {
	job := crewJob{session: *s, runner: runner, starter: starter}
	fallback, _ := s.st.Handle[harnessFallbackModel].(string)
	if fallback == "" {
		return d.crewFallbackStartJob(ctx, s, job)
	}
	due, hasDue := handleTime(s.st.Handle, harnessFallbackReturnAt)
	if hasDue && !d.now().Before(due) {
		primary, _ := s.member.Config["model"].(string)
		d.tellCrewFallback(ctx, s, fmt.Sprintf("%s's %s is returning to %s: its model's usage window has reset, and yardr is restarting it from its fallback model %s",
			s.member.Name, s.what(), orModel(primary), fallback),
			map[string]any{"fallback": "return", "fallback_model": fallback})
		return job
	}
	if !hasDue && !strings.HasPrefix(s.st.RestartRequested, fallbackRestartPrefix) {
		primary, _ := s.member.Config["model"].(string)
		d.tellCrewFallback(ctx, s, fmt.Sprintf("%s's %s is returning to %s: yardr is restarting it from its fallback model %s",
			s.member.Name, s.what(), orModel(primary), fallback),
			map[string]any{"fallback": "return", "fallback_model": fallback})
		return job
	}
	member := s.member
	cfg := maps.Clone(member.Config)
	if cfg == nil {
		cfg = map[string]any{}
	}
	cfg["model"] = fallback
	member.Config = cfg
	job.session.member = member
	job.fallbackModel = fallback
	job.fallbackReturnAt, _ = s.st.Handle[harnessFallbackReturnAt].(string)
	return job
}

// crewFallbackStartJob is crewFallbackJob for a start whose session
// carries no fallback keys: it starts on its fallback where the cached
// limits name its own model's window used up, and on its own model
// otherwise. The read is harnessLimits as a stop makes it (the
// machine's login, no group env), so a cold cache, a provider that
// gave no answer and a limit no source knows of all answer no window
// and the start is as it was: the stop path catches a later stop. A
// shared window is UsedUpForStop's first word and names no model, so
// it fails over nowhere, as at a stop; only a model window whose word
// matches the config's model id (windowModelMatches) does, with
// fallback_model named and not the model itself, as crewFallbackWake
// holds a stop to. The job is the failover branch's own — the member
// cloned on the fallback, the return stamped from the window's reset
// plus the lag — and the operator is told once, as a start.
func (d *Dispatcher) crewFallbackStartJob(ctx context.Context, s *memberSession, job crewJob) crewJob {
	fallback, _ := s.member.Config["fallback_model"].(string)
	if fallback == "" {
		return job
	}
	primary, _ := s.member.Config["model"].(string)
	if primary != "" && fallback == primary {
		d.Log.Warn("crew session's fallback_model is the same as its model; skipping failover",
			"crew", s.member.Name, "session", s.label(), "model", primary)
		return job
	}
	kind := harnessKind(s.member.Config)
	provider, _ := s.member.Config["provider"].(string)
	data := map[string]any{}
	_, w := d.harnessLimits(ctx, kind, provider, nil, data)
	if w == nil || w.Model == "" || !windowModelMatches(w.Model, primary) {
		return job
	}
	var returnAt string
	var line string
	if w.ResetsAt != nil {
		now := d.now()
		at := *w.ResetsAt
		if at.Before(now) {
			at = now
		}
		at = at.Add(harnessResetLag)
		returnAt = at.UTC().Format(time.RFC3339Nano)
		d.lookAt(at)
		line = fmt.Sprintf("%s's %s is starting on its fallback model %s: its model's usage window (%s) is used up, and it returns to %s at %s",
			s.member.Name, s.what(), fallback, w.Label, orModel(primary), limits.Clock(at, now))
	} else {
		line = fmt.Sprintf("%s's %s is starting on its fallback model %s: its model's usage window (%s) is used up; "+
			"the provider does not say when that window resets, so run `%s` when ready to return to %s",
			s.member.Name, s.what(), fallback, w.Label, s.restartCmd(), orModel(primary))
	}
	member := s.member
	cfg := maps.Clone(member.Config)
	if cfg == nil {
		cfg = map[string]any{}
	}
	cfg["model"] = fallback
	member.Config = cfg
	job.session.member = member
	job.fallbackModel = fallback
	job.fallbackReturnAt = returnAt
	d.tellCrewFallback(ctx, s, line,
		map[string]any{"fallback": "start", "window": w.Label, "fallback_model": fallback})
	return job
}

// withCrewFallback returns a clone of handle with the session's
// fallback keys stamped onto it, ready to pass to CrewStartedFor so a
// daemon restart cannot lose the spell between start and drain.
func withCrewFallback(handle map[string]any, fallback, returnAt string) map[string]any {
	h := maps.Clone(handle)
	if h == nil {
		h = map[string]any{}
	}
	h[harnessFallbackModel] = fallback
	if returnAt != "" {
		h[harnessFallbackReturnAt] = returnAt
	} else {
		delete(h, harnessFallbackReturnAt)
	}
	return h
}
