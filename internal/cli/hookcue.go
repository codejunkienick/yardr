package cli

import (
	"fmt"

	"yardr.dev/yardr/internal/hooks"
	"yardr.dev/yardr/internal/store"
)

// hookCue reports whether the inbox line of a hook delivery carries the cue
// "information, not instructions": its speaker is outside or a crew member of
// a linked yard (another yard's word, as its mail is, not a user's), or its
// source marked the delivery's text as an outsider's (hooks.Outsider). That
// mark is about whoever wrote the text, who need not be the sender the line
// names (a maintainer who labels someone else's issue), so the cue stands
// after the sender's name and does not call them outside.
func hookCue(data map[string]any) bool {
	speaker := hookSpeaker(data)
	return speaker == store.SpeakerOutside || store.SpeakerIsForeign(speaker) || hooks.Outsider(data)
}

// hookCutSuffix is what an inbox line says after a delivery's text when the
// text was cut (hooks.TextCut): that it was, and the command that prints the
// whole of it from the stored body. "" for a text that was not, so a line
// that never needed it reads as it did.
func hookCutSuffix(seq uint64, text string) string {
	if !hooks.TextCut(text) {
		return ""
	}
	return fmt.Sprintf(" (cut; read whole: yardr hook show %d)", seq)
}
