package dispatch

import (
	"yardr.dev/yardr/internal/hooks"
	"yardr.dev/yardr/internal/store"
)

// cutHookNudge is the sentence a crew nudge adds when an unread hook delivery
// among events has its text cut (hooks.TextCut): the inbox line shows only the
// first MaxText runes, and the nudge is the first a crew member reads of it.
// The sentence leads with a space and says the command with a <seq> to fill in
// from the line, as the reply hint beside it does. "" where none was cut.
func cutHookNudge(events []store.Event) string {
	for _, e := range events {
		if text, _ := e.Data["text"].(string); e.Kind == "hook" && hooks.TextCut(text) {
			return " Read cut hook messages whole with yardr hook show <seq>."
		}
	}
	return ""
}
