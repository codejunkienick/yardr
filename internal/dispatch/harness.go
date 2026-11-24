package dispatch

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"

	"yardr.dev/yardr/internal/agents"
	"yardr.dev/yardr/internal/limits"
	"yardr.dev/yardr/internal/store"
)

// A session whose agent's harness stopped on an error of its own: the model
// is at capacity, the network dropped, the login ran out, a usage limit
// was hit. The harness prints the error and waits for input, which herdr
// reports as idle, so to
// the yard it looks like an agent that is thinking about its answer or
// waiting on a human, and nobody hears of it before the idle limit (30m).
// The error is in the pane, though. So the end of an idle agent's pane is
// read at the start of each idle spell, and a line there that is a known
// error of its harness raises session_harness_error at once, with a reason
// and the pane's last lines.
//
// What yardr then does itself is prompt the agent again, in the two cases
// where it knows both when and what to say. Everything else is left as it
// is: whether to wait, to get the agent going in its pane or to end the
// session is the yardmaster's to decide. A login does not come back by
// asking again, and neither does an error no pattern tells apart.
//
// At capacity, on a first prompt (yardr-f7aw): on 6 Oct 2026 a reviewer's
// harness answered its first prompt with "Selected model is at capacity",
// and all the yardmaster did was give it the same prompt again by hand. So
// a session that stopped on at_capacity and has done nothing yet is given
// its first prompt again: harnessRetryWait after the error is seen, and
// again that long after each try while the error still ends its pane. Each
// try is a session_harness_retry event. Only when the tries are used up is
// session_harness_error raised, with how often yardr tried (`tries`): the
// wait is 20 minutes at most, so nobody is told while it lasts. A session
// that has left anything behind (worked, as for a stall) stopped mid-work,
// where "continue" may be right or its last message may: that one is raised
// at once.
//
// A dropped or unopened network connection (yardr-9js1): a builder's
// harness answered "network connection could not be opened", a crew
// member's "Connection dropped (ECONNRESET)", and both ask for the message
// to be sent again. So a session or a crew member stopped on network is
// given a short resume prompt again, mid-work or not: the same wait after
// the error is seen, and again that long after each try while the error
// still ends its pane, within the same HarnessRetries the at_capacity
// tries count in. Only when the tries are used up is
// session_harness_error raised, with how often yardr tried (`tries`).
//
// A usage limit whose window resets within harnessResetMax (yardr-w41f): the
// same evening the crew member brakeman stopped at 23:20 with a review
// waiting, its window opened at 00:02, and it sat until a person prompted
// it. A source of the yard says which window is used up and when it resets
// (harness_limits.go), so the time is known. The event is raised at once,
// here, since the wait can be hours and changes what is worth starting; it
// says when yardr looks again (`wakes_at`), and the look is kept on the
// handle. See harness_reset.go for the wait, the ask at its end, what the
// agent is told and how yardr sees that it took it. A window that resets
// later than that (a weekly one), one whose reset no source says, and a
// limit no source knows of are the yardmaster's as before. While a session
// waits, its group starts no other with its model (holdBack, stall.go).
//
// Whatever the case, yardr types into a pane only when the error still ends
// it, its input is empty and no first prompt is on its way, and
// HarnessRetries times in a session's life, both cases counted together:
// an agent that works for a moment after each prompt is not prompted for
// ever.

// Reasons a session_harness_error gives: those an error line of a harness
// file may give (store.Reasons).
const (
	ReasonAtCapacity  = store.ReasonAtCapacity
	ReasonNetwork     = store.ReasonNetwork
	ReasonNotLoggedIn = store.ReasonNotLoggedIn
	ReasonRateLimited = store.ReasonRateLimited
	ReasonOther       = store.ReasonOther
)

// HarnessErrorKey is a handle key: the reason of the session_harness_error
// raised in the session's current idle spell. It is kept on the handle, not
// in memory, so a daemon that replaces itself does not tell the yardmaster
// again, and it is taken off when the agent is seen doing something.
const HarnessErrorKey = "harness_error"

// HarnessLoginKey is a handle key: the line of the harness's login screen
// the agent sits at, while it sits there. It is kept on the handle so the
// crew's status and yardr doctor say the agent is not logged in (Crew
// says it as login_screen, and the store raises crew_login when it is put
// on or taken off); the event that told of it is told once per spell from
// memory (see loginAt). It is taken off when the agent is seen past it:
// working, typed to, or with its first prompt arrived.
const HarnessLoginKey = store.HarnessLoginKey

// Handle keys of what yardr does for an agent whose harness stopped. They
// are on the handle, as a pending prompt's are (prompt.go), so a daemon that
// replaces itself neither starts the count again nor forgets a wait.
const (
	harnessRetries = "harness_retries"  // how often yardr prompted the agent again
	harnessRetryAt = "harness_retry_at" // when the pane is next looked at, for a try or for what a prompt did (RFC 3339); only while one waits

	// Of a usage limit (harness_reset.go):
	harnessResetAt     = "harness_reset_at"     // when the window is asked about again (RFC 3339); only while the agent waits for it
	harnessResetWindow = "harness_reset_window" // the window it waits for, as the provider labels it
	harnessResetModel  = "harness_reset_model"  // the model a session waits with, "" for its agent's own: its group starts no other with it meanwhile (holdBack)
	harnessPrompted    = "harness_prompted"     // what yardr typed at the reset, until it is known whether the agent took it

	// Of a crew member's model fallback (harness_fallback.go):
	harnessFallbackModel    = "harness_fallback_model"     // the fallback model the session runs on while its own model's window is used up; "" never stored
	harnessFallbackReturnAt = "harness_fallback_return_at" // when that window resets and the session restarts back on its own model (RFC 3339)

	// Of a snooze the session's agent asked for (store.SnoozeSession,
	// limits.go): the store names them, beside the record that carries
	// the handle, since it writes them and the dispatcher only reads.
	snoozeUntil = store.SnoozeUntil // when the snooze ends (RFC 3339); only while the agent waits
	snoozeCount = store.SnoozeCount // snoozes in a row without the pane changing
)

// HarnessRetries is how often yardr prompts an agent whose harness stopped
// again in its session's life: a session at capacity with its first prompt
// or on a dropped network connection, before the yardmaster hears of it,
// and an agent at a usage limit when the window has reset.
const HarnessRetries = 3

// harnessRetryWait is how long the error stands before each try, and after
// the last one before it is raised: capacity that is gone is not back in
// seconds, and four waits still end before the idle limit (30m).
const harnessRetryWait = 5 * time.Minute

// FirstPrompter is implemented by runners that can say what a session's
// agent was first told (a herdr agent is pointed at its brief), so that it
// can be told again. "" is a session whose first prompt is not known.
type FirstPrompter interface {
	FirstPrompt(handle map[string]any) string
}

const (
	// harnessLook is how long into an idle spell the pane is read on each
	// pass. A harness prints its error before it goes idle, so the first
	// pass sees it; the passes after cover a screen drawn late. An agent
	// that idles for hours is not read for hours.
	harnessLook = 2 * time.Minute
	// harnessLines is how many of the pane's last lines are looked at. The
	// error that stopped a harness is the last thing it printed, above its
	// input box and status line; further up is what the agent wrote while
	// it still worked, which may well quote an error. It is all of the tail
	// an event carries (stallTail): a harness may break its error into lines
	// of the pane's width itself, and in a pane of 60 columns one's usage
	// limit began 12 lines from the end, under the box and the status
	// (yardr-r9sh; harnesses/kimi.yaml).
	harnessLines = 20
)

// The error lines each harness kind (a group's `kind`) is known to stop on
// are in the kind's file, harnesses/<kind>.yaml, under `errors`, with a
// shared set of provider-agnostic network lines (agents.NetworkErrors) tried
// for every kind after its own non-other lines (not_logged_in, rate_limited,
// at_capacity, network) and before its `other` lines. Within each group the
// first pattern that matches gives the reason, so a line that says two things
// (an API error that ends in "Please run /login") is listed under the one
// that matters before the general one.
//
// To add a kind-specific line: take the line as the harness prints it (its
// source or docs, or the `tail` of a session_idle that should have been this
// event), and add a pattern to the kind's list. A pattern is matched against
// one line of the pane with the marks a TUI puts before a message taken off
// (the file's `marks.error`: Claude Code's ⏺ and ⎿, Codex's ■ and •, a box's
// edge), so begin it with ^ unless the text comes later in the line: an
// agent that writes about an error mid-sentence is then not taken for a
// harness that stopped on it. Add a case to TestHarnessErrorLines with the
// line, and write docs/groups.md's table again (TestGroupsDocTables in
// internal/agents says how).

// HarnessError finds, in the end of a pane, the error line a harness of
// kind stopped on: the reason, and the line as it stands in the pane. The
// last such line counts until a message the agent took since ends the scan,
// making an error above it history; the last input line may hold a draft.
func HarnessError(kind, tail string) (reason, line string, ok bool) {
	h, _ := agents.Lookup(kind)
	if len(h.Errors) == 0 && len(agents.NetworkErrors) == 0 {
		return "", "", false
	}
	marks := " \t" + h.Marks.Error
	lines := strings.Split(tail, "\n")
	input := false
	for i, seen := len(lines)-1, 0; i >= 0 && seen < harnessLines; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		seen++
		if h.Marks.Input != "" && !input && strings.HasPrefix(l, h.Marks.Input) {
			input = true
			continue
		} else if h.Marks.Message != "" && strings.HasPrefix(l, h.Marks.Message) &&
			strings.TrimSpace(strings.TrimPrefix(l, h.Marks.Message)) != "" {
			return "", "", false
		}
		said := strings.TrimLeft(l, marks)
		if reason, matched := matchErrorLine(h.Kind, h.Errors, agents.NetworkErrors, l, said); matched {
			return reason, l, true
		}
	}
	return "", "", false
}

// matchErrorLine tries kind's own non-other lines first, then the shared
// network lines when the line itself is an error line (agents.IsErrorLine)
// without an explicit non-gateway HTTP status (agents.HasNonGatewayHTTPStatus),
// and finally kind's own `other` lines, so auth/rate-limit/capacity/status
// errors never become network and ordinary prose never matches shared patterns.
func matchErrorLine(kind string, own, shared []store.ErrorLine, raw, said string) (string, bool) {
	for _, e := range own {
		if e.Reason != ReasonOther && e.Matches(said) {
			return e.Reason, true
		}
	}
	if agents.IsErrorLine(kind, raw, said) && !agents.HasNonGatewayHTTPStatus(said) {
		for _, e := range shared {
			if e.Matches(said) {
				return e.Reason, true
			}
		}
	}
	for _, e := range own {
		if e.Reason == ReasonOther && e.Matches(said) {
			return e.Reason, true
		}
	}
	return "", false
}

// backgroundLines is how many of the pane's last lines are looked at for
// background work: what a harness draws around its input, the line that
// ended the turn above it and the status under it, with a notice between.
// Further up is what the agent wrote, which may say the same of a shell
// that is long gone.
const backgroundLines = 6

// HarnessBackground finds, in the end of a pane, the line with which a
// harness of kind says that work its agent started in the background still
// runs (the `background` of the kind's file). Such an agent waits for its
// own work and its harness wakes it when that ends: it is idle to herdr and
// not to the idle limit (limits.go). A kind that names no such line never
// has one.
func HarnessBackground(kind, tail string) (line string, ok bool) {
	h, _ := agents.Lookup(kind)
	if len(h.Background) == 0 {
		return "", false
	}
	lines := strings.Split(tail, "\n")
	for i, seen := len(lines)-1, 0; i >= 0 && seen < backgroundLines; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		seen++
		for _, b := range h.Background {
			if b.Matches(l) {
				return l, true
			}
		}
	}
	return "", false
}

// paneTail is the end of the pane of s's agent, as an event carries it: the
// lines a session_stalled has always had. A pane that cannot be read says
// so in their place. ok is false for a runner that has no pane to read.
func (d *Dispatcher) paneTail(ctx context.Context, r Runner, s store.Session) (tail string, ok bool) {
	t, ok := r.(Tailer)
	if !ok {
		return "", false
	}
	done := askTimer(ctx, askPane)
	tail, err := t.Tail(ctx, s.Handle, stallTail)
	done()
	if err != nil {
		tail = "(pane not readable: " + err.Error() + ")"
	}
	return tail, true
}

// handleTime reads the time a handle keeps under key, and whether it keeps
// one.
func handleTime(h map[string]any, key string) (at time.Time, ok bool) {
	text, _ := h[key].(string)
	at, err := time.Parse(time.RFC3339Nano, text)
	return at, err == nil
}

// harnessWaits reports whether a look at s's pane waits (for a try, see
// harnessRetry, or for what a prompt at a reset did), and when it is due.
func harnessWaits(s *store.Session) (due time.Time, waits bool) {
	return handleTime(s.Handle, harnessRetryAt)
}

// harnessKind is the harness an agent of cfg, a group's or a crew member's
// config, runs.
func harnessKind(cfg map[string]any) string {
	return agents.Kind(cfg)
}

// harnessLooks reports whether the pane of s, idle for idleFor, is read on
// this pass for an error of its harness: while the spell is young, and when
// a try that waits is due. Until it is due, Run is told when to look.
func (d *Dispatcher) harnessLooks(s *store.Session, idleFor time.Duration) bool {
	if due, waits := harnessWaits(s); waits {
		if d.now().Before(due) {
			d.lookAt(due)
			return false
		}
		return true
	}
	return idleFor <= harnessLook && s.Handle[HarnessErrorKey] == nil
}

// lineReset reads the reset a rate_limited line itself names, where it
// names one as a Go duration ("Resets in 2h15m47s", the trailing stop of
// the sentence trimmed): a window labelled for the line that carried it,
// resetting that long after now. A harness whose subscription no source
// of the yard answers for (agy has none, and none is wanted) can still
// say when its window opens, in its own words, and a stop that waits for
// it is prompted as one whose source spoke. The words are the harness's,
// so no kind is named here: any rate_limited line that carries such a
// reset rides the same path. Nothing here keys on the line's text being
// the same twice — agy's countdown ticks on every redraw — since the
// line matched on this pass is the only one read.
func lineReset(line string, now time.Time) (*limits.Window, bool) {
	_, after, found := strings.Cut(line, "Resets in ")
	if !found {
		return nil, false
	}
	fields := strings.Fields(after)
	if len(fields) == 0 {
		return nil, false
	}
	dur, err := time.ParseDuration(strings.TrimSuffix(fields[0], "."))
	if err != nil || dur < 0 {
		return nil, false
	}
	at := now.Add(dur)
	return &limits.Window{Label: "Individual", ResetsAt: &at}, true
}

// resetWakeFor is resetWake for a stop whose source named w, with the
// reset the matched line names where the source named none: w's own
// reset, where it gave one, is the provider's word and is never replaced
// or shortened by the line's. A line that carries no readable reset
// leaves the wait as it was, untimed. The window and reset the line gave
// are the event's as a source's are (data window, resets_at).
func (d *Dispatcher) resetWakeFor(r Runner, handle map[string]any, w *limits.Window, line string, data map[string]any) (wake map[string]any, said string) {
	wake, said = d.resetWake(r, handle, w, data)
	if wake != nil || (w != nil && w.ResetsAt != nil) {
		return wake, said
	}
	lw, ok := lineReset(line, d.now())
	if !ok {
		return wake, said
	}
	data["window"] = lw.Label
	data["resets_at"] = lw.ResetsAt.UTC().Format(time.RFC3339)
	return d.resetWake(r, handle, lw, data)
}

// harnessError raises session_harness_error for the idle session s when
// tail, the end of its pane, shows a known error of its harness, once per
// idle spell. One that is at_capacity is first left to harnessRetry, and
// one yardr prompted at a reset to harnessTaken. s.Handle is updated with
// the marks it stores.
func (d *Dispatcher) harnessError(ctx context.Context, r Runner, s *store.Session, g store.Group, tail string) {
	kind := harnessKind(g.Config)
	provider, _ := g.Config["provider"].(string)
	if s.Handle[harnessPrompted] != nil && !d.harnessTaken(ctx, r, s, kind, tail) {
		return
	}
	if s.Handle[HarnessErrorKey] != nil {
		return
	}
	reason, line, ok := HarnessError(kind, tail)
	if !ok {
		// The error a try waited on has left the pane.
		if _, waits := harnessWaits(s); waits {
			d.markHarnessError(ctx, s, "", nil)
		}
		d.harnessDialog(ctx, r, s, g, tail)
		return
	}
	data := map[string]any{"session": s.ID, "group": s.Group, "stage": s.Stage, "kind": kind,
		"reason": reason, "line": line}
	if (reason == ReasonAtCapacity || reason == ReasonNetwork) && d.harnessRetry(ctx, r, s, reason, data) {
		return
	}
	d.Log.Warn("session's harness stopped on an error", "session", s.ID, "bead", s.Bead, "group", s.Group,
		"kind", kind, "reason", reason, "line", line)
	data["tail"] = tail
	data["hint"] = "the agent's harness stopped on an error and sits idle; the line is in line, its pane's last " +
		"lines in tail. The session is left as it is: get the agent going in its pane, or end the " +
		"session with `yardr bead release " + s.Bead + "`"
	tries := handleInt(s.Handle[harnessRetries])
	var wake map[string]any
	if reason == ReasonRateLimited {
		said, w := d.sessionLimits(ctx, g, kind, provider, data)
		var wakes string
		wake, wakes = d.resetWakeFor(r, s.Handle, w, line, data)
		if wake != nil || w == nil || w.ResetsAt == nil {
			// An unreported reset does not make the stopped model available.
			if wake == nil {
				wake = map[string]any{}
				wakes += "the group starts no further session with this model until this session ends or gets going; "
			} else {
				wakes += "the group starts no further session with this model until then; "
			}
			model := ""
			if b, err := d.Store.GetBead(ctx, s.Bead); err == nil {
				model = modelFor(g, *b)
			}
			wake[harnessResetModel], data["model"] = model, model
		}
		data["hint"] = said + wakes + data["hint"].(string)
		if tries > 0 {
			data["tries"] = tries
			data["hint"] = fmt.Sprintf("yardr has prompted the agent again %d times in this session; ", tries) + data["hint"].(string)
		}
	}
	if (reason == ReasonAtCapacity || reason == ReasonNetwork) && tries > 0 {
		data["tries"] = tries
		if reason == ReasonAtCapacity {
			data["hint"] = fmt.Sprintf("yardr gave the agent its first prompt again %d times, %s apart; ", tries, harnessRetryWait) +
				data["hint"].(string)
		} else {
			data["hint"] = fmt.Sprintf("yardr prompted the agent again %d times, %s apart; ", tries, harnessRetryWait) +
				data["hint"].(string)
		}
	}
	if err := d.Store.LogDispatch(ctx, s.Bead, "session_harness_error", actor, data); err != nil {
		d.Log.Warn("session_harness_error event", "session", s.ID, "err", err)
	}
	d.markHarnessError(ctx, s, reason, wake)
}

// harnessRetry takes one step for s, idle with a transient error
// (reason, at_capacity or network) at the end of its pane: it starts the
// wait for a try, leaves a wait that is not over alone, or, with the wait
// over, prompts the agent again and waits once more. data is what the
// try's event says of the error. It reports false when the error is to be
// raised instead: the tries are used up, or s is not a session yardr may
// prompt (see harnessPrompt).
//
// The wait after a try is on the handle with the try, not started by the
// next look at the pane: an agent that never left idle is past harnessLook
// by then and would not be looked at again. And the error is raised at the
// end of a wait, not at the try, so the last try has had as long as the
// others to show what it did; until the harness prints something, the old
// error still ends its pane.
func (d *Dispatcher) harnessRetry(ctx context.Context, r Runner, s *store.Session, reason string, data map[string]any) bool {
	now := d.now()
	due, waits := harnessWaits(s)
	if waits && now.Before(due) {
		return true
	}
	tries := handleInt(s.Handle[harnessRetries])
	if tries >= HarnessRetries {
		return false
	}
	prompt := d.harnessPrompt(ctx, r, s, reason)
	if prompt == "" {
		return false
	}
	next := now.Add(harnessRetryWait)
	h := withHandle(s.Handle, harnessRetryAt, next.UTC().Format(time.RFC3339Nano))
	if waits {
		// One the agent did not start on was typed all the same: ownInput
		// finds it in the input on a later pass.
		if _, err := typeText(ctx, r.(Nudger), s.Handle, prompt); err != nil {
			d.Log.Warn("give a session its prompt again", "session", s.ID, "reason", reason, "err", err)
			return true // try again next pass
		}
		tries++
		h[harnessRetries] = tries
	}
	if err := d.Store.SetHandle(ctx, s.ID, h); err != nil {
		d.Log.Warn("mark session's harness retry", "session", s.ID, "err", err)
		return !waits // a try that is not counted is not made again: raised
	}
	s.Handle = h
	d.lookAt(next)
	if !waits {
		return true
	}
	d.Log.Warn("session's harness stopped on a transient error: prompted again", "session", s.ID, "bead", s.Bead,
		"reason", reason, "try", tries, "of", HarnessRetries)
	data = maps.Clone(data)
	data["try"], data["of"] = tries, HarnessRetries
	if err := d.Store.LogDispatch(ctx, s.Bead, "session_harness_retry", actor, data); err != nil {
		d.Log.Warn("session_harness_retry event", "session", s.ID, "err", err)
	}
	return true
}

// harnessPrompt is the prompt a try gives s's agent for reason, "" when
// yardr may not prompt it. At capacity it is the first prompt, of a
// session that has done nothing with it yet (worked, as a stall asks) and
// whose input is empty. Whether it did anything is asked before each try,
// so a session a person got going and that stopped again mid-work is not
// sent back to its brief. On network it is a short resume prompt, mid-work
// or not, since the harness asks for the message to be sent again.
func (d *Dispatcher) harnessPrompt(ctx context.Context, r Runner, s *store.Session, reason string) string {
	if reason == ReasonNetwork {
		if !mayType(r, s.Handle) || !inputEmpty(ctx, r, s.Handle) {
			return ""
		}
		return networkResumeLine()
	}
	prompt := d.firstPromptAgain(ctx, r, s)
	if prompt == "" || !inputEmpty(ctx, r, s.Handle) {
		return ""
	}
	return prompt
}

// networkResumeLine is what an agent stopped on a dropped or unopened
// network connection is told, a crew member's as a session's: its harness
// asks for the message to be sent again, so yardr sends this one.
func networkResumeLine() string {
	return "yardr: your harness stopped on a dropped network connection. Go on where you stopped."
}

// firstPromptAgain is s's first prompt when it is the right thing to give
// s's agent again: r can type it and knows it, it is not still on its way,
// and the session has done nothing with it. "" otherwise.
func (d *Dispatcher) firstPromptAgain(ctx context.Context, r Runner, s *store.Session) string {
	fp, ok := r.(FirstPrompter)
	if !ok || !mayType(r, s.Handle) {
		return ""
	}
	prompt := fp.FirstPrompt(s.Handle)
	if prompt == "" {
		return ""
	}
	if worked, err := d.worked(ctx, r, *s); err != nil || worked {
		return ""
	}
	return prompt
}

// mayType reports whether yardr can type a prompt of its own into the pane
// of handle at all: r can, and no first prompt is still on its way there.
func mayType(r Runner, handle map[string]any) bool {
	_, nudges := r.(Nudger)
	return nudges && handle[store.PendingPrompt] == nil
}

// inputEmpty reports whether nothing is typed in the input of handle's
// agent. Text there is someone's (see inputHeld): nothing is typed after
// it, and the session is theirs. A runner that cannot look says nothing.
func inputEmpty(ctx context.Context, r Runner, handle map[string]any) bool {
	if ir, ok := r.(InputReader); ok {
		if text, known, err := ir.InputText(ctx, handle); err == nil && known && strings.TrimSpace(text) != "" {
			return false
		}
	}
	return true
}

// harnessOver ends the spell a session_harness_error was raised in, or a
// try waited in: s's agent is doing something again, so its next error is
// told again. A saved quota hold can also end at idle, when the pane can be
// read without its quota error. Tries count for the session's life, or an
// agent that works for a moment after each would be prompted for ever.
// A dialog told of ends with it.
func (d *Dispatcher) harnessOver(ctx context.Context, r Runner, s *store.Session, g store.Group) {
	if quotaHeld(s.Handle) {
		// A fresh runner may call an unchanged quota-stop pane working.
		t, ok := r.(Tailer)
		if !ok {
			return
		}
		done := askTimer(ctx, askPane)
		tail, err := t.Tail(ctx, s.Handle, stallTail)
		done()
		if err != nil {
			return
		}
		if reason, _, _ := HarnessError(harnessKind(g.Config), tail); reason == ReasonRateLimited {
			return
		}
	}
	for _, key := range []string{HarnessErrorKey, HarnessDialogKey, harnessRetryAt, harnessResetAt, harnessPrompted} {
		if s.Handle[key] != nil {
			d.markHarnessError(ctx, s, "", nil)
			return
		}
	}
}

// markHarnessError keeps reason on s's handle, or takes the mark off for "".
// Either ends every wait there was, for a try, for a reset or for what a
// prompt did, and a dialog told of; wake is the wait for a reset that
// begins with the mark (see resetWake), nil for none.
func (d *Dispatcher) markHarnessError(ctx context.Context, s *store.Session, reason string, wake map[string]any) {
	h := maps.Clone(s.Handle)
	if h == nil {
		h = map[string]any{}
	}
	for _, key := range []string{HarnessDialogKey, harnessRetryAt, harnessResetAt, harnessResetWindow, harnessResetModel, harnessPrompted} {
		delete(h, key)
	}
	maps.Copy(h, wake)
	if reason == "" {
		delete(h, HarnessErrorKey)
	} else {
		h[HarnessErrorKey] = reason
	}
	if err := d.Store.SetHandle(ctx, s.ID, h); err != nil {
		d.Log.Warn("mark session's harness error", "session", s.ID, "err", err)
		return
	}
	s.Handle = h
}

// A crew member's harness stops on the same errors, and it has no idle
// limit to be told at: on 6 Oct 2026 the crew member brakeman (kimi) sat at
// its usage limit for half an hour with a review waiting, and nobody heard
// (yardr-r9sh). So the end of its pane is read too, when it is idle with
// something unread in its inbox: one that is idle with nothing to do is
// what a crew member is most of the time, and is left alone.
//
// crewHarnessError raises session_harness_error on r's journal, with the
// crew member named (`crew`) where a session's names its session. The pane
// is read on each pass in the first harnessLook of a spell of being idle
// with an unread inbox, and again for as long after each nudge typed there
// (see crewHarnessLook): the nudge is what a stopped harness answers with
// its error again.
//
// The reason told is kept on r's handle (HarnessErrorKey), as a session's
// is, and the same reason is not told again while it stands. It stays
// through the agent working, unlike a session's: a nudge makes a stopped
// harness work for a moment and stop on the same error, so every new item
// in its inbox would raise the event again. The mark goes when a look finds
// the pane without an error, or the inbox empty (see crewHarnessOver).
// While it stands no inbox nudge is typed (crewNudgeHeld): a nudge starts the
// round that failed again, as the one that met the error did, so the items
// stay unread and the event is what tells of them. The nudge is typed on the
// pass that finds the mark gone.
// The one thing yardr does for a crew member is prompt it when the window of
// a usage limit has reset (harness_reset.go); at capacity it has no first
// prompt to be given again. On network it is tried again as a session is,
// with the same wait and budget (see crewHarnessRetry).
func (d *Dispatcher) crewHarnessError(ctx context.Context, runner Runner, s *memberSession) {
	t, ok := runner.(Tailer)
	if !ok {
		return
	}
	key := s.key()
	since, ok := d.idleSince[key]
	if !ok {
		since = d.now()
		d.idleSince[key] = since
	}
	// A try that waits is looked at when it is due, whatever the spell's
	// age: otherwise the pane would not be read again for it.
	due, waits := handleTime(s.st.Handle, harnessRetryAt)
	tryDue := waits && !d.now().Before(due)
	if d.now().Sub(since) > harnessLook && !tryDue {
		if waits {
			d.lookAt(due)
		}
		return
	}
	done := askTimer(ctx, askPane)
	tail, err := t.Tail(ctx, s.st.Handle, stallTail)
	done()
	if err != nil {
		return // a pane that cannot be read says nothing either way
	}
	kind := harnessKind(s.member.Config)
	provider, _ := s.member.Config["provider"].(string)
	reason, line, _ := HarnessError(kind, tail)
	if reason == "" {
		// The error a try waited on has left the pane.
		if _, waits := handleTime(s.st.Handle, harnessRetryAt); waits {
			d.markCrewHarnessError(ctx, s, "", nil)
		}
	} else if reason == ReasonNetwork {
		data := s.who(map[string]any{"kind": kind, "reason": reason, "line": line})
		if d.crewHarnessRetry(ctx, runner, s, data) {
			return
		}
	}
	if told, _ := s.st.Handle[HarnessErrorKey].(string); reason == told {
		return
	}
	var wake map[string]any
	if reason != "" {
		d.Log.Warn("crew session's harness stopped on an error", "crew", s.member.Name, "session", s.label(), "kind", kind, "reason", reason, "line", line)
		data := s.who(map[string]any{"kind": kind, "reason": reason, "line": line, "tail": tail,
			"hint": "the crew member's harness stopped on an error and sits idle with its inbox unread; the line is " +
				"in line, its pane's last lines in tail. It is left as it is: get the agent going in its pane, or " +
				"start a fresh session with `" + s.restartCmd() + "`"})
		if reason == ReasonRateLimited {
			// A crew member's error reads the machine's login: no group
			// judges it, so no group's env is joined.
			said, w := d.harnessLimits(ctx, kind, provider, nil, data)
			var wakes string
			if fwake, fsaid, handled := d.crewFallbackWake(ctx, s, w, data); handled {
				wake, wakes = fwake, fsaid
			} else {
				wake, wakes = d.resetWakeFor(runner, s.st.Handle, w, line, data)
			}
			data["hint"] = said + wakes + data["hint"].(string)
			if tries := handleInt(s.st.Handle[harnessRetries]); tries > 0 {
				data["tries"] = tries
				data["hint"] = fmt.Sprintf("yardr has prompted the agent again %d times in this session; ", tries) + data["hint"].(string)
			}
		}
		if reason == ReasonNetwork {
			if tries := handleInt(s.st.Handle[harnessRetries]); tries > 0 {
				data["tries"] = tries
				data["hint"] = fmt.Sprintf("yardr prompted the agent again %d times, %s apart; ", tries, harnessRetryWait) + data["hint"].(string)
			}
		}
		if err := d.Store.LogDispatch(ctx, s.member.Journal, "session_harness_error", actor, data); err != nil {
			d.Log.Warn("session_harness_error event", "crew", s.member.Name, "session", s.label(), "err", err)
		}
	}
	d.markCrewHarnessError(ctx, s, reason, wake)
}

// crewHarnessRetry takes one step for s, a crew member idle with its inbox
// unread and a dropped network connection at the end of its pane: it starts
// the wait for a try, leaves a wait that is not over alone, or, with the
// wait over, types the resume prompt and waits once more. data is what the
// try's event says of the error. It reports false when the error is to be
// raised instead: the tries are used up, or the prompt cannot be typed.
func (d *Dispatcher) crewHarnessRetry(ctx context.Context, runner Runner, s *memberSession, data map[string]any) bool {
	now := d.now()
	due, waits := handleTime(s.st.Handle, harnessRetryAt)
	if waits {
		if now.Before(due) {
			d.lookAt(due)
			return true
		}
	}
	tries := handleInt(s.st.Handle[harnessRetries])
	if tries >= HarnessRetries || !mayType(runner, s.st.Handle) {
		return false
	}
	next := now.Add(harnessRetryWait)
	h := maps.Clone(s.st.Handle)
	if h == nil {
		h = map[string]any{}
	}
	h[harnessRetryAt] = next.UTC().Format(time.RFC3339Nano)
	if waits {
		typed, err := d.nudge(ctx, runner, s, networkResumeLine())
		if err != nil {
			d.Log.Warn("give a crew session its resume prompt again", "crew", s.member.Name, "session", s.label(), "err", err)
			return true // try again next pass
		}
		if !typed {
			return true // held back: the wait stands, nothing counted
		}
		tries++
		h[harnessRetries] = tries
		d.crewHarnessLook(s)
	}
	d.setCrewHandle(ctx, s, h)
	d.lookAt(next)
	if !waits {
		return true
	}
	d.Log.Warn("crew session's harness stopped on a dropped network connection: prompted again", "crew", s.member.Name,
		"session", s.label(), "try", tries, "of", HarnessRetries)
	data = maps.Clone(data)
	data["try"], data["of"] = tries, HarnessRetries
	if err := d.Store.LogDispatch(ctx, s.member.Journal, "session_harness_retry", actor, data); err != nil {
		d.Log.Warn("session_harness_retry event", "crew", s.member.Name, "session", s.label(), "err", err)
	}
	return true
}

// crewHarnessLook starts the time the session's pane is read in anew:
// something was typed there, and a harness that is stopped says so again
// under it; or the nudge due was held back for the error it stands on, and
// the pane is looked at again to see it gone (crewNudgeHeld).
func (d *Dispatcher) crewHarnessLook(s *memberSession) {
	d.idleSince[s.key()] = d.now()
}

// crewNudgeHeld reports whether the inbox nudge due for s is held back:
// its harness stopped on an error and sits idle (the mark crewHarnessError
// keeps on its handle), and a nudge would start the round that failed
// again. The session_harness_error raised for it is the telling. Nothing is
// typed and Notified stays where it is, so the items stay unread and the
// nudge is typed on the pass that finds the mark gone. A network retry owns
// the pane the same way while its try waits, before the error is raised.
// That pass has to look at the pane, and the spell's first minutes are long
// past when a person got the agent going: the look is started anew on each
// pass the nudge is held.
func (d *Dispatcher) crewNudgeHeld(s *memberSession) bool {
	if s.st.Handle[HarnessErrorKey] == nil && s.st.Handle[harnessRetryAt] == nil {
		return false
	}
	d.crewHarnessLook(s)
	return true
}

// crewHarnessOver ends the spell the session's pane was read in: its agent
// works, or is idle with nothing unread. A try that waited is over when
// work was seen or the inbox is empty, so its deadline goes then; what was
// told and the session's tries stand. An unknown status says nothing of
// the try, so its wait stands for the next idle pass. What yardr still
// watches of a prompt it gave at a reset keeps its deadline through a
// working pass: the deadline is the reset check's, and crewPromptTaken ends
// that watch. With its inbox empty the error told of is over as well, since
// an agent that handled its inbox is not stopped: so is what yardr still
// watched of a prompt it gave at a reset.
func (d *Dispatcher) crewHarnessOver(ctx context.Context, s *memberSession, status string, handled bool) {
	delete(d.idleSince, s.key())
	if !handled {
		if status != "working" {
			return
		}
		if s.st.Handle[harnessPrompted] != nil {
			return
		}
		if s.st.Handle[harnessRetryAt] != nil {
			h := maps.Clone(s.st.Handle)
			delete(h, harnessRetryAt)
			d.setCrewHandle(ctx, s, h)
		}
		return
	}
	for _, key := range []string{HarnessErrorKey, harnessRetryAt, harnessResetAt, harnessPrompted} {
		if s.st.Handle[key] != nil {
			h := maps.Clone(s.st.Handle)
			for _, key := range []string{HarnessErrorKey, harnessRetryAt, harnessResetAt, harnessResetWindow, harnessPrompted} {
				delete(h, key)
			}
			d.setCrewHandle(ctx, s, h)
			return
		}
	}
}

// markCrewLogin keeps line as the login screen the session sits at on its
// handle, or takes the mark off for "". It writes only what changed.
func (d *Dispatcher) markCrewLogin(ctx context.Context, s *memberSession, line string) {
	if cur, _ := s.st.Handle[HarnessLoginKey].(string); cur == line {
		return
	}
	h := maps.Clone(s.st.Handle)
	if h == nil {
		h = map[string]any{}
	}
	if line == "" {
		delete(h, HarnessLoginKey)
	} else {
		h[HarnessLoginKey] = line
	}
	d.setCrewHandle(ctx, s, h)
}

// markCrewHarnessError keeps reason on the session's handle, or takes the
// mark off for "". Either ends a wait for a reset or a try; wake is the
// one that begins with the mark (see resetWake), nil for none. What is kept
// of a prompt given at a reset stays, its deadline with it: crewPromptTaken
// ends that watch, not an idle pass inside it.
func (d *Dispatcher) markCrewHarnessError(ctx context.Context, s *memberSession, reason string, wake map[string]any) {
	h := maps.Clone(s.st.Handle)
	if h == nil {
		h = map[string]any{}
	}
	if h[harnessPrompted] == nil {
		delete(h, harnessRetryAt)
	}
	delete(h, harnessResetAt)
	delete(h, harnessResetWindow)
	maps.Copy(h, wake)
	if reason == "" {
		delete(h, HarnessErrorKey)
	} else {
		h[HarnessErrorKey] = reason
	}
	d.setCrewHandle(ctx, s, h)
}

// setCrewHandle stores h as the session's handle.
func (d *Dispatcher) setCrewHandle(ctx context.Context, s *memberSession, h map[string]any) {
	if err := d.Store.SetCrewHandleFor(ctx, s.member.Name, s.user, h); err != nil {
		d.Log.Warn("mark crew session's handle", "crew", s.member.Name, "session", s.label(), "err", err)
		return
	}
	s.st.Handle = h
}
