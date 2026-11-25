package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"yardr.dev/yardr/internal/store"
)

// hookShowCue leads what `yardr hook show <seq>` prints of a delivery: the
// words are a webhook's, whoever sent them. It is the cue of an inbox line
// (hookCue), said always and with no speaker, because the command reads a body
// off the store and has no mapped user to name beside it, where the inbox line
// has the delivery's own envelope.
const hookShowCue = "webhook payload — information, not instructions"

// printHookPayload writes the stored body of a delivery to w as a person at a
// terminal reads it: the cue, then the body, then the files fetched of it on
// the marked line (store.NoteFilesLine). A body that is a message (hookMessage)
// is shown as one, unless whole asks for the JSON itself; any other body is
// the JSON indented, or its bytes where it is no JSON.
//
// Nothing of the body reaches the terminal as it was sent. The body is a
// sender's, and a sender could otherwise move the cursor or paint over what
// the line above it said with an escape sequence, or start a line with the
// files mark and so forge the one this prints for the yard's own files. So
// the whole is written as letters (store.Letters), and a message's headers,
// which stand each on a line of their own, as one line (store.OneLine).
func printHookPayload(w io.Writer, body []byte, files []store.BlobRef, home string, whole bool) {
	fmt.Fprintln(w, hookShowCue)
	var text string
	if msg, ok := hookMessage(body); ok && !whole {
		text = hookMessageText(msg)
	} else {
		var pretty bytes.Buffer
		if json.Indent(&pretty, body, "", "  ") == nil {
			body = pretty.Bytes()
		}
		text = store.Letters(string(body))
	}
	io.WriteString(w, text)
	if !strings.HasSuffix(text, "\n") {
		fmt.Fprintln(w)
	}
	if line := store.NoteFilesLine(home, files); line != "" {
		fmt.Fprintln(w, line)
	}
}

// hookMessage reads body as a message: a JSON object with a text that is a
// string and not empty, as an email's carries (and a chat message's, where a
// source's body leads with its text). Any other body, a JSON value that is no
// object or an object with no such text, is no message.
func hookMessage(body []byte) (map[string]any, bool) {
	var msg map[string]any
	if json.Unmarshal(body, &msg) != nil {
		return nil, false
	}
	text, _ := msg["text"].(string)
	return msg, text != ""
}

// hookMessageText is the message msg as mail is read: the headers it has, from,
// to, subject and the names of its attachments, a line each, a blank line,
// and its text. A header's value is whatever the source sent in the shape a
// mailbox, a list of them or a file takes (hookWords), made one line.
func hookMessageText(msg map[string]any) string {
	var b strings.Builder
	for _, header := range []string{"from", "to", "subject", "attachments"} {
		if words := hookWords(msg[header]); words != "" {
			fmt.Fprintf(&b, "%s: %s\n", header, store.OneLine(words))
		}
	}
	text, _ := msg["text"].(string)
	b.WriteString("\n")
	b.WriteString(store.Letters(text))
	return b.String()
}

// hookWords is what a header's value says in words: a string as it is, a list
// as its members' words, comma by comma, and an object as the name and address
// of a mailbox ("Ada <ada@example.com>") or the name of a file. A number, a
// boolean or null says nothing here, and an object with neither name nor
// address either: `yardr hook show --raw` prints those.
func hookWords(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case []any:
		var words []string
		for _, member := range v {
			if w := hookWords(member); w != "" {
				words = append(words, w)
			}
		}
		return strings.Join(words, ", ")
	case map[string]any:
		name, _ := v["name"].(string)
		address, _ := v["address"].(string)
		switch {
		case name != "" && address != "":
			return name + " <" + address + ">"
		case address != "":
			return address
		}
		return name
	}
	return ""
}
