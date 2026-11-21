package dispatch

import (
	"context"
	"strings"
)

// A harness that is handed a flag it does not take prints its help in its
// pane and exits, and herdr, which waited for the agent to come up, fails
// the start with its own words alone ("timeout: timed out waiting for
// agent startup"). The session_failed that lands says nothing of why, and
// the daemon log has no more, so whoever reads the failure is sent to a
// pane that teardown is about to close. Where the end of that pane is the
// harness's help, the failure names it instead: the usage lines are put
// on the reason the session ends with, which its event, the feed and the
// yard's mail all carry.
//
// Only the help's shape is taken, never the pane around it: a line counts
// only as a usage marker (see usageExcerpt), so prose, an env dump and
// anything else an agent printed at its start stays out of an event that
// leaves the yard. The excerpt is capped short for the same reason: a
// recognizer, not a transcript. A pane with no such line, one that cannot
// be read, and a runner with no pane to read all leave the reason exactly
// as it was, so every other start failure reads as it does today.

const (
	// startUsageLines and startUsageRunes cap the excerpt a start failure
	// carries, and startUsageLineRunes one line of it: enough of a help
	// screen to name the harness and the flag shape it takes, no more.
	startUsageLines     = 4
	startUsageRunes     = 240
	startUsageLineRunes = 120
)

// startFailureReason is reason with the usage excerpt of the pane handle
// names put after it, where that pane's end holds one: what a session
// that died at its start ends with (runStart, and reap's start
// interrupted), so the help its harness printed is named in the failure
// itself. The pane is read now, before the session ends: teardown closes
// it after. Anything that keeps the excerpt from being read or found
// leaves reason as it is.
func (d *Dispatcher) startFailureReason(ctx context.Context, r Runner, handle map[string]any, reason string) string {
	if r == nil || len(handle) == 0 {
		return reason
	}
	t, ok := r.(Tailer)
	if !ok {
		return reason
	}
	done := askTimer(ctx, askPane)
	tail, err := t.Tail(ctx, handle, stallTail)
	done()
	if err != nil {
		return reason // a pane that cannot be read says nothing either way
	}
	if excerpt := usageExcerpt(tail); excerpt != "" {
		return reason + " (pane: " + excerpt + ")"
	}
	return reason
}

// usageExcerpt is the usage-looking lines of a pane's end, in the order
// they stand, capped as startUsageLines and its runes say: "" where the
// end holds none. A line is usage-looking by its shape alone: a usage
// marker (a Usage line, a help header, a "--help" hint, an invocation
// synopsis), or, after one of those, a help entry (a flag line or an
// invocation). Nothing else is taken, however the pane reads: a line
// that names a value (KEY=value, --flag=value) is the shape of a setting
// or a credential, never of help, whose values are placeholders, so it
// is left out even where it follows a marker.
func usageExcerpt(tail string) string {
	var lines []string
	seen := false
	for _, line := range strings.Split(tail, "\n") {
		if len(lines) >= startUsageLines {
			break
		}
		text := strings.TrimSpace(line)
		if text == "" || (strings.Contains(text, "=") && !placeholderValue(text)) {
			continue
		}
		switch lower := strings.ToLower(text); {
		case isUsageMarker(lower):
			seen = true
			if marker := safeUsageMarker(text); marker != "" {
				lines = append(lines, marker)
			}
		case seen:
			if entry := safeHelpEntry(text); entry != "" {
				lines = append(lines, entry)
			}
		case isSynopsis(text):
			// A synopsis whose Usage line scrolled above the lines read
			// still opens the help it belongs to.
			seen = true
			lines = append(lines, strings.Join(strings.Fields(text), " "))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	kept := make([]string, len(lines))
	for i, line := range lines {
		kept[i] = cutRunes(line, startUsageLineRunes)
	}
	return cutRunes(strings.Join(kept, "; "), startUsageRunes)
}

// isUsageMarker reports whether the lower-cased, trimmed line opens a
// harness's help: its Usage line, one of its section headers, or the hint
// at its --help that a refused flag prints with its error.
func isUsageMarker(lower string) bool {
	if strings.HasPrefix(lower, "usage:") || strings.HasPrefix(lower, "usage ") ||
		strings.HasPrefix(lower, "usage of ") || lower == "usage" {
		return true
	}
	switch lower {
	case "commands:", "available commands:", "options:", "flags:",
		"global options:", "global flags:", "arguments:", "examples:",
		"example:", "help:":
		return true
	}
	return strings.HasPrefix(lower, "try ") && strings.Contains(lower, "--help")
}

// safeUsageMarker keeps the usage syntax or header from a marker line.
// Help output can contain values beside a flag, so the marker is never
// copied as a whole line when its arguments are not placeholders or flags.
func safeUsageMarker(text string) string {
	lower := strings.ToLower(text)
	switch {
	case strings.HasPrefix(lower, "usage:"):
		line := strings.TrimSpace(text[len("usage:"):])
		if safe, ok := safeInvocation(line); ok {
			return "Usage: " + safe
		}
		return "Usage:"
	case strings.HasPrefix(lower, "usage ") || lower == "usage":
		return "Usage"
	case strings.HasPrefix(lower, "try "):
		return safeHelpHint(text)
	default:
		// Section headers have a fixed vocabulary and no free-form value.
		return text
	}
}

// safeHelpHint keeps a help hint's command and --help marker, leaving its
// prose and any other words out of the reason.
func safeHelpHint(text string) string {
	fields := strings.Fields(text)
	if len(fields) < 3 {
		return ""
	}
	for i, field := range fields[2:] {
		if strings.Contains(strings.Trim(field, "'\"`:,;()"), "--help") && i+2 < len(fields) {
			command := strings.Trim(fields[i+1], "'\"`:,;()")
			if isCommandWord(command) {
				return "Try " + command + " --help"
			}
		}
	}
	return ""
}

// safeHelpEntry keeps only an option's syntax column or a placeholder
// invocation from a help entry. Descriptions and literal values have no
// role in naming the harness and could carry text the pane must not share.
func safeHelpEntry(text string) string {
	if strings.HasPrefix(text, "-") {
		// CLI help aligns descriptions after the syntax column. Keeping only
		// that column leaves flag names and placeholders, never their values.
		if i := strings.Index(text, "  "); i >= 0 {
			text = text[:i]
		}
		fields := strings.Fields(text)
		if len(fields) == 0 {
			return ""
		}
		for _, field := range fields {
			if !isFlagToken(field) && !isPlaceholderToken(field) {
				return ""
			}
		}
		return strings.Join(fields, " ")
	}
	if safe, ok := safeInvocation(text); ok {
		return safe
	}
	return ""
}

// isSynopsis reports whether text is an invocation syntax line, with a
// command followed only by flags and placeholders. That is enough to name
// a harness even when its Usage line has scrolled off the pane.
func isSynopsis(text string) bool {
	_, ok := safeInvocation(text)
	return ok
}

// safeInvocation returns text as invocation syntax only when every word
// after the command is a flag or a placeholder, so a printed argument is
// never mistaken for help.
func safeInvocation(text string) (string, bool) {
	fields := strings.Fields(text)
	if len(fields) < 2 || !isCommandWord(fields[0]) {
		return "", false
	}
	for _, field := range fields[1:] {
		if !isFlagToken(field) && !isPlaceholderToken(field) {
			return "", false
		}
	}
	return strings.Join(fields, " "), true
}

// isCommandWord reports whether w is a bare command or subcommand name:
// a letter, then letters, digits and the marks a harness's name carries.
// A word with a path, an assignment or a pane's own marks in it is none.
func isCommandWord(w string) bool {
	for i, r := range w {
		switch {
		case 'a' <= r && r <= 'z', '0' <= r && r <= '9' && i > 0,
			i > 0 && (r == '-' || r == '_' || r == '.'):
		default:
			return false
		}
	}
	return w != ""
}

// placeholderValue reports whether text's assignment, if it holds one,
// gives a placeholder for its value ("--config=<file>", "KEY=[x]"): a
// help line's shape. A literal after the "=" is a value, and the line
// that carries it is no help line, whatever else it looks like.
func placeholderValue(text string) bool {
	_, value, found := strings.Cut(text, "=")
	return found && isPlaceholderToken(value)
}

// isPlaceholderToken recognizes a bracketed argument name, not arbitrary
// text inside brackets, which may itself be a printed value.
func isPlaceholderToken(text string) bool {
	if len(text) < 3 || !((text[0] == '<' && text[len(text)-1] == '>') ||
		(text[0] == '[' && text[len(text)-1] == ']')) {
		return false
	}
	for _, r := range text[1 : len(text)-1] {
		if !isWordRune(r) {
			return false
		}
	}
	return true
}

// isFlagToken recognizes a flag name, optionally with a placeholder value.
func isFlagToken(text string) bool {
	if strings.Contains(text, "=") {
		name, value, ok := strings.Cut(text, "=")
		return ok && isFlagName(name) && isPlaceholderToken(value)
	}
	return isFlagName(strings.TrimSuffix(text, ","))
}

// isFlagName accepts the short and long option spellings used by CLI help.
func isFlagName(text string) bool {
	if !strings.HasPrefix(text, "-") || text == "-" || text == "--" {
		return false
	}
	body := strings.TrimPrefix(text, "--")
	if body == text {
		body = text[1:]
	}
	if body == "" || strings.HasPrefix(body, "-") {
		return false
	}
	for _, r := range body {
		if !(('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') ||
			('0' <= r && r <= '9') || r == '-') {
			return false
		}
	}
	return true
}

func isWordRune(r rune) bool {
	return ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') ||
		('0' <= r && r <= '9') || r == '-' || r == '_' || r == '.'
}

// cutRunes is s held to at most n runes, with an ellipsis where it was
// cut: a bound an event's reader is given, as cutTail bounds a tail.
func cutRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
