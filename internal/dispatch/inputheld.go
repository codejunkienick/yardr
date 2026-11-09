package dispatch

import (
	"context"
	"fmt"
	"strings"
	"time"

	"yardr.dev/yardr/internal/agents"
	"yardr.dev/yardr/internal/store"
)

// InputReader is implemented by runners that can read what is typed in the
// input of a session's agent and not sent (a herdr pane shows it). known is
// false when the runner cannot tell the agent's input from the rest of its
// pane, and an error says it could not look; both tell nothing.
type InputReader interface {
	InputText(ctx context.Context, handle map[string]any) (text string, known bool, err error)
}

// DialogReader is implemented by runners that know questions a harness
// draws in place of its input (the herdr runner, from the kind's file):
// dialog is the word for the one the agent of handle is at and line its
// first line, "" when it is at none. An error says the runner could not
// look, which tells nothing.
type DialogReader interface {
	InputDialog(ctx context.Context, handle map[string]any) (dialog, line string, err error)
}

// LoginReader is implemented by runners that know the login screen a
// harness draws in place of its input (the herdr runner, from the kind's
// file): line is the line of it the pane of handle shows, "" when it shows
// none. An error says the runner could not look, which tells nothing.
type LoginReader interface {
	InputLogin(ctx context.Context, handle map[string]any) (line string, err error)
}

// draft is text seen in an agent's input on a pass that had something to
// type there.
type draft struct {
	since time.Time // when it was first seen
	said  bool      // the nudge_held event of this wait is logged
}

// hold is what the one nudge_held event of a wait has to say, on the pass
// that is to say it (see inputHeld): the text has been in the input for
// waited, the input is a dialog of the harness, known by its word, with
// line its first, or the pane shows the harness's login screen, with login
// its line. The zero hold says nothing.
type hold struct {
	waited       time.Duration
	dialog, line string
	login        string
}

func (h hold) tells() bool { return h.waited > 0 || h.dialog != "" || h.login != "" }

type ownNudge struct {
	text string
	at   time.Time
}

// inputHeld reports whether the input of the session of handle holds text,
// so that nothing may be typed there on this pass; key names the session
// in the dispatcher's memory. why tells on the one pass of a wait that is
// to say so: the text has then been there for DraftWait, or it is a dialog
// of the harness, which is said at once.
//
// Found live (yardr-ps05): a person was typing in the yardmaster's pane when
// the yardmaster was nudged about its inbox. The nudge was typed after their
// words and its Enter sent both as one message; what they had still meant to
// write was lost. Text in an input is someone's, so yardr types nothing after
// it and looks again on the next pass: what it had to say stays due.
//
// Text that stays is a draft someone left. It is never cleared and never
// typed after, however long it sits: both would send or destroy words a
// person wrote, to deliver a line that is still true and still due once
// they are gone. What a long wait costs is that the agent is not told, so
// after DraftWait one nudge_held event says who is waiting on whose input,
// once per wait.
//
// The one text yardr sends is its own, alone in the input because its Enter
// was not taken: that is ownInput's, which looks before anything is typed.
//
// Found live (yardr-vsen): what holds the input may be the harness's own
// question, drawn where the input is. One asks an agent that sat idle past
// its model's cache how to go on, and marks the answer Enter would choose
// as an input is marked (the dialog cache_menu of harnesses/kimi.yaml). Nobody is typing there, so nobody will
// clear it, and the agent sits until someone answers: that is said at
// once, and DraftWait is not waited for (see dialogAt).
//
// Found live (yardr-m519): a harness that is not logged in draws its login
// screen where its input is, and Claude Code's menu of methods carries the
// mark of the input before its selected option. Read as an input it is
// text someone left, and read for its screen it is up, so the first prompt
// was typed into the menu; with another status the daemon waited for a
// human. A pane at a login screen is its own state (see loginAt): nothing
// is typed there, and the event names the login, not the text.
//
// A runner that cannot look, or a harness whose input it cannot tell, says
// nothing: yardr then types as it did before it looked.
func (d *Dispatcher) inputHeld(ctx context.Context, r Runner, key string, handle map[string]any) (held bool, why hold) {
	if at, why := d.loginAt(ctx, r, key, handle); at {
		return true, why
	}
	ir, ok := r.(InputReader)
	if !ok {
		return false, hold{}
	}
	done := askTimer(ctx, askPane)
	text, known, err := ir.InputText(ctx, handle)
	done()
	if err != nil || !known || strings.TrimSpace(text) == "" {
		delete(d.drafts, key)
		if err == nil {
			delete(d.dialogs, key) // no mark, or nothing after it: no dialog is there
		}
		return false, hold{}
	}
	now := d.now()
	w := d.drafts[key]
	if w == nil {
		w = &draft{since: now}
		d.drafts[key] = w
	}
	if at, why := d.dialogAt(ctx, r, key, handle); at {
		w.said = true // the dialog's event is this wait's
		return true, why
	}
	if !w.said && now.Sub(w.since) >= d.DraftWait {
		w.said = true
		return true, hold{waited: now.Sub(w.since)}
	}
	return true, hold{}
}

// dialogAt reports whether the agent of handle is at a dialog its harness
// drew in place of its input, one the runner knows (DialogReader). why
// tells of it on the first pass that finds it, and on no later one: the
// dialog is remembered under key until a look finds the input without it,
// or the agent is seen at work again. So one event tells of a dialog,
// however many nudges come to it while it is open.
//
// yardr answers no such dialog except two the runner cancels with Esc:
// one that only offers to change the operator's settings, and Codex's
// offer to switch models at its rate limit, which Esc dismisses without
// choosing, so the session keeps its model. The cache menu asks whether
// to compact a session or send its whole history again at full price,
// which is for whoever runs the agent to choose.
func (d *Dispatcher) dialogAt(ctx context.Context, r Runner, key string, handle map[string]any) (at bool, why hold) {
	dr, ok := r.(DialogReader)
	if !ok {
		return false, hold{}
	}
	done := askTimer(ctx, askPane)
	dialog, line, err := dr.InputDialog(ctx, handle)
	done()
	switch {
	case err != nil:
		return false, hold{}
	case dialog == "":
		delete(d.dialogs, key)
		return false, hold{}
	case d.dialogs[key] == dialog:
		return true, hold{}
	}
	d.dialogs[key] = dialog
	return true, hold{dialog: dialog, line: line}
}

// loginAt reports whether the agent of handle sits at its harness's login
// screen, one the runner knows (LoginReader). why tells of it on the first
// pass that finds it, and on no later one: the login is remembered under
// key, with its line, until a look finds the pane without it. So one event
// tells of a login screen, however many nudges come to it while it sits
// there. The line is kept fresh while it sits there, for the mark on the
// handle (HarnessLoginKey), which is set from it whenever the pane is at
// the screen, told or not.
//
// yardr types nothing into a login screen: the keys would answer the
// harness's menu. Logging in is the person's to do, in the pane or in
// another terminal (see nudgeHeld).
func (d *Dispatcher) loginAt(ctx context.Context, r Runner, key string, handle map[string]any) (at bool, why hold) {
	lr, ok := r.(LoginReader)
	if !ok {
		return false, hold{}
	}
	done := askTimer(ctx, askPane)
	line, err := lr.InputLogin(ctx, handle)
	done()
	switch {
	case err != nil:
		return false, hold{}
	case line == "":
		delete(d.logins, key)
		return false, hold{}
	case d.logins[key] != "":
		d.logins[key] = line
		return true, hold{}
	}
	d.logins[key] = line
	return true, hold{login: line}
}

// InputMarker is implemented by runners that find an agent's input by the
// mark its harness begins it with and know that mark for some kinds of
// harness only. InputMarked reports whether the agent of handle is of such
// a kind. In the pane of any other, a line that begins like an input may be
// one the harness printed: enough to type nothing after it (inputHeld), not
// enough to send Enter for it (ownInput).
type InputMarker interface {
	InputMarked(handle map[string]any) bool
}

// nudgeHeld logs the one event of a wait on text in an agent's input (see
// inputHeld): what, a nudge or the first prompt, is not typed, for whom
// (a crew member or a bead's session, as who names it), on bead. For a
// dialog of the harness the reason is the word "dialog", `dialog` the
// dialog's own word and `text` its first line. For the harness's login
// screen the reason is not_logged_in and `line` its line, with the command
// that logs in.
func (d *Dispatcher) nudgeHeld(ctx context.Context, bead string, who, handle map[string]any, what string, why hold) {
	waited := why.waited
	data := map[string]any{
		"waited": waited.Round(time.Second).String(),
		"reason": fmt.Sprintf("the agent's input has held text for %s, so %s is not typed there; "+
			"it is typed once the input is empty. Send or clear the text in its pane",
			waited.Round(time.Second), what)}
	if why.dialog != "" {
		hint := fmt.Sprintf("the agent's harness asks this in place of its input, and yardr answers no dialog of a harness: "+
			"%s waits until it is answered in the agent's pane", what)
		if why.dialog == "auto_mode_setup" {
			hint = fmt.Sprintf("the agent's harness asks this in place of its input, and yardr cancels the auto mode offer with Esc: "+
				"%s waits until the dialog is gone", what)
		}
		if why.dialog == "rate_limit_switch" {
			hint = fmt.Sprintf("the agent's harness asks this in place of its input, and yardr declines the offer to switch models with Esc: "+
				"%s waits until the dialog is gone", what)
		}
		data = map[string]any{"reason": "dialog", "dialog": why.dialog, "text": why.line,
			"hint": hint}
	}
	if why.login != "" {
		kind, exe := loginKind(handle)
		fresh := fmt.Sprintf("start it fresh with `yardr bead release %s`", bead)
		if crew, _ := who["crew"].(string); crew != "" {
			fresh = fmt.Sprintf("start it fresh with `yardr crew restart %s`", crew)
		}
		data = map[string]any{"reason": ReasonNotLoggedIn, "line": why.login,
			"hint": fmt.Sprintf("%s is not logged in: its pane shows its login screen, and yardr types nothing there. "+
				"%s waits until it is logged in: run %s in another terminal and log in there, then %s; or answer the login in its pane. "+
				"For a crew member of a yard with an operator, the yard leads the login itself and asks the operator (a login_led event names the ask).",
				kind, what, exe, fresh)}
	}
	if pane, _ := handle["pane"].(string); pane != "" {
		data["pane"] = pane
	}
	for k, v := range who {
		data[k] = v
	}
	switch {
	case why.login != "":
		kind, _ := loginKind(handle)
		d.Log.Warn(kind+" is not logged in: "+what+" is not typed into its login screen", "bead", bead, "who", who, "line", why.login)
	case why.dialog != "":
		d.Log.Warn("a dialog of the agent's harness holds back "+what, "bead", bead, "who", who, "dialog", why.dialog, "line", why.line)
	default:
		d.Log.Warn("text in the agent's input holds back "+what, "bead", bead, "who", who, "waited", waited.Round(time.Second))
	}
	_ = d.Store.LogDispatch(ctx, bead, "nudge_held", actor, data)
}

// loginKind is the harness kind of the agent of handle and the command that
// logs it in: the kind the runner started it as (the handle's "kind", as
// the herdr runner writes it), or the default when the handle names none,
// and that kind's executable.
func loginKind(handle map[string]any) (kind, exe string) {
	kind, _ = handle["kind"].(string)
	if kind == "" {
		kind = agents.DefaultKind
	}
	return kind, store.Executable(kind)
}
