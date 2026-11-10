// What the group set and crew set rows of the run list (runList) share: the
// strong few of a definition (design yardr-m90n 3.5), its prompt, an exec
// group's command and a permission preset that never asks, read from the
// words of a run ask and drawn on a card with what stands now beside what
// the approval would set, each whole. Every later session of the group or
// the member runs what the change sets, so none of it is told after it runs
// (the delegated leg refuses all three): it is a card the operator approves
// first, weighed code. The rows are in runask_groupset.go and
// runask_crewset.go.
package store

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// strongApart says where what a card of the strong few does not carry is
// changed instead.
const strongApart = "the yardmaster runs the delegated settings for the operator on their mail (--for, --mail), and the rest are the operator's to type in a terminal"

// strongKey says whether word is key=value for one of keys.
func strongKey(word string, keys []string) bool {
	k, _, ok := strings.Cut(word, "=")
	return ok && slices.Contains(keys, k)
}

// strongSetWords reads the words of group set or crew set <name> that name
// the strong few, keys in the order the stored form writes them. It answers
// errNotRow for words that name none of them: those are another row's of the
// same command, or the operator's alone. Beside one that does, any other
// word is refused, naming it, so that a card shows all that it sets and
// nothing rides in with it. The preset is kept in its one-word form
// (PresetLabel); whether it is a strong preset of the kind is for the yard's
// records to say, and so is every other thing about the words.
func strongSetWords(use, command, noun string, keys, rest []string) (string, map[string]string, error) {
	if !slices.ContainsFunc(rest, func(word string) bool { return strongKey(word, keys) }) {
		return "", nil, errNotRow
	}
	var name string
	sets := map[string]string{}
	for i, word := range rest {
		switch {
		case i == 0 && (strings.Contains(word, "=") || strings.HasPrefix(word, "-")):
			return "", nil, fmt.Errorf("--run takes %s: %q is no %s name", use, word, noun)
		case i == 0:
			name = word
		case strings.HasPrefix(word, "-"):
			flag, _, _ := strings.Cut(word, "=")
			return "", nil, fmt.Errorf("--run takes %s, and %s with %s is no part of this card, which carries %s and nothing else: %s", use, command, flag, strings.Join(keys, ", "), strongApart)
		default:
			k, v, ok := strings.Cut(word, "=")
			_, dup := sets[k]
			var js any
			switch {
			case !ok || k == "":
				return "", nil, fmt.Errorf("--run takes %s: %q is no key=value", use, word)
			case !slices.Contains(keys, k):
				return "", nil, fmt.Errorf("--run takes %s: %s is no part of this card, which carries %s and nothing else: %s", use, k, strings.Join(keys, ", "), strongApart)
			case dup:
				return "", nil, fmt.Errorf("--run takes %s: %s is set twice", use, k)
			case v == "":
				return "", nil, fmt.Errorf("--run takes %s: %s needs its value", use, k)
			case json.Unmarshal([]byte(v), &js) == nil:
				// The terminal stores what reads as JSON as that, a number
				// or a list, and a card cannot write a string as JSON.
				return "", nil, fmt.Errorf("--run takes %s: %s=%s reads as JSON, which the terminal stores as a value of its own type and not as text: name a value that is plain text", use, k, v)
			}
			if k == "preset" {
				v = PresetLabel(v)
			}
			sets[k] = v
		}
	}
	return name, sets, nil
}

// strongSetArgv is the stored form of group set or crew set: the name, then
// each setting of keys that was given, in the order of keys.
func strongSetArgv(name string, sets map[string]string, keys []string) []string {
	out := []string{name}
	for _, k := range keys {
		if v, ok := sets[k]; ok {
			out = append(out, k+"="+v)
		}
	}
	return out
}

// strongSetOf reads a stored group set or crew set argv: the name and each
// setting.
func strongSetOf(argv []string) (name string, sets map[string]string) {
	name, sets = argv[2], map[string]string{}
	for _, kv := range argv[3:] {
		k, v, _ := strings.Cut(kv, "=")
		sets[k] = v
	}
	return name, sets
}

// strongChanged names what a card of the strong few changes, for its weight.
func strongChanged(sets map[string]string) []string {
	var out []string
	for _, c := range []struct{ key, what string }{{"prompt", "its prompt"}, {"cmd", "its command"}, {"kind", "its kind"}, {"model", "its model"}, {"dir", "its directory"}, {"preset", "its permission flags"}} {
		if _, ok := sets[c.key]; ok {
			out = append(out, c.what)
		}
	}
	return out
}

// strongPreset is the permission preset label of the harness kind, when it is
// one that skips the agent's permission prompts (Permission.Strong): a card
// is the only door to one. A preset that asks as it did is no card's, and
// refuses in words that say where it is set instead.
func strongPreset(subject, kind, label string) (Permission, error) {
	p, ok := HarnessPreset(kind, label)
	switch {
	case !ok:
		return Permission{}, noPreset(subject, kind, label, "a strong one")
	case !p.Strong:
		return Permission{}, fmt.Errorf("%s: the %s preset %s does not skip the agent's permission prompts, so it is no card of the strong few: %s. A card carries a preset that never asks", subject, kind, p.Label, strongApart)
	}
	return p, nil
}

// noPreset refuses a permission preset that the kind does not have, in words
// that name those it has. which says what the card takes of them: "a strong
// one", or "one" where it takes any.
func noPreset(subject, kind, label, which string) error {
	return fmt.Errorf("%s: %s has no permission preset %q (it has: %s): name %s as preset=<preset> in its one-word form (a dash for a space), as yardr harness show %s lists them", subject, kind, label, or(strings.Join(HarnessPresetLabels(kind), ", "), "none"), which, kind)
}

// promptBefore is the text of the prompt file a definition holds now, as a
// card shows it whole, and why not where it cannot: the file does not stand
// as a regular file under the yard's home, is longer than a card holds, or is
// no text a card shows as written. A card says so then, and the approval is
// still held to the text it does show, which is the one that will run.
func (w *wtx) promptBefore(path string) (text, why string, rewritten bool) {
	if !strings.HasPrefix(path, "/") {
		return "", "it is a relative path, read against each depot", false
	}
	real, data, sum, err := w.homeFile("", path, MaxRunCrewRole, true)
	digestName := real
	if yardPromptName.MatchString(path) {
		digestName = path
	}
	switch {
	case err != nil:
		return "", "it does not stand as a regular file under the yard's home", false
	case len(data) > MaxRunCrewRole:
		return "", fmt.Sprintf("it is longer than the %d bytes a card shows", MaxRunCrewRole), promptDigestRewritten(digestName, sum)
	case !utf8.Valid(data) || strings.IndexFunc(string(data), crewRoleHidden) >= 0 || strings.Contains(string(data), NoteFilesMark) || strings.Contains(string(data), WeightMark):
		return "", "it is not text a card shows as written", promptDigestRewritten(digestName, sum)
	}
	return string(data), "", promptDigestRewritten(digestName, sum)
}

// textLines are the lines of a prompt's text as a card shows it.
func textLines(text string) []string { return strings.Split(strings.TrimSuffix(text, "\n"), "\n") }

// promptWritten is the card's line for the file the approval of a prompt
// writes (yardPromptPath): the text below it is what the file will hold. It
// says that the file the ask names is no part of what runs, since the
// approval is of the bytes the card shows and a later write to that file
// is nobody's to approve.
func promptWritten(path string) string {
	return fmt.Sprintf("On approval the yard writes the text below to the file %s, read-only. Each session reads that file when it starts, so changing the file the ask names later changes no session:", path)
}

// promptLines are the card's lines for a prompt that changes from the file
// was (empty for none) to the file path, which the approval writes from
// text: what stands now and what will, each whole. A file the yard wrote
// that holds other text than its name says is said so, above the text: it was
// rewritten in place, and a card that only showed the text would pass it off
// as the prompt the yard wrote. That is no refusal: the approval is still
// held to the text the card shows.
func (w *wtx) promptLines(was, path, text string) []string {
	var lines []string
	if was == "" {
		lines = append(lines, "It has no prompt now.")
	} else if before, why, rewritten := w.promptBefore(was); why == "" {
		if rewritten {
			lines = append(lines, fmt.Sprintf("Its prompt is the file %s, which each session reads when it starts.", was),
				"The yard names the files it writes by the SHA-256 of their text, and this file no longer holds the text its name says. It holds now:")
		} else {
			lines = append(lines, fmt.Sprintf("Its prompt is the file %s, which each session reads when it starts, and which holds now:", was))
		}
		lines = append(lines, textLines(before)...)
	} else {
		if rewritten {
			lines = append(lines, fmt.Sprintf("Its prompt is the file %s, which each session reads when it starts.", was),
				"The yard names the files it writes by the SHA-256 of their text, and this file no longer holds the text its name says.",
				fmt.Sprintf("This card cannot show it: %s.", why))
		} else {
			lines = append(lines, fmt.Sprintf("Its prompt is the file %s, which each session reads when it starts. This card cannot show it: %s.", was, why))
		}
	}
	lines = append(lines, promptWritten(path))
	return append(lines, textLines(text)...)
}

// argWords are the words of an args config value, a list or a string split
// at spaces as the runner reads it.
func argWords(v any) []string {
	switch args := v.(type) {
	case string:
		return strings.Fields(args)
	case []any:
		var out []string
		for _, a := range args {
			out = append(out, fmt.Sprint(a))
		}
		return out
	case []string:
		return args
	}
	return nil
}

// quotedWords are words as a card shows flags, each quoted, or none.
func quotedWords(words []string) string {
	if len(words) == 0 {
		return "no flags of their own"
	}
	quoted := make([]string, len(words))
	for i, word := range words {
		quoted[i] = strconv.Quote(word)
	}
	return strings.Join(quoted, " ")
}

// presetLines are the card's lines for a permission preset set where the
// flags old stand now: the agents of the kind was, the flags they start with
// now (and the preset they are, if they are one) and the preset's, which are
// of the kind they will be, kind (the card of the yardmaster's own entry can
// change the kind beside it). The sentence that it skips permissions follows
// the preset's Strong mark, the one rule of the cards and the leg.
func presetLines(was, kind string, old any, p Permission) []string {
	words := argWords(old)
	now := quotedWords(words)
	for _, q := range HarnessPresets[was] {
		if len(words) > 0 && slices.Equal(q.Args, words) {
			now += " (the " + q.Label + " preset)"
		}
	}
	will := "them"
	if kind != was {
		will = fmt.Sprintf("%s (%s) agents", kind, or(HarnessLabels[kind], kind))
	}
	lines := []string{
		fmt.Sprintf("It starts %s (%s) agents with %s now.", was, or(HarnessLabels[was], was), now),
		fmt.Sprintf("It will start %s with %s: the %s preset, %s.", will, quotedWords(p.Args), p.Label, p.Note),
	}
	if p.Strong {
		lines = append(lines, "It skips permissions: it runs without asking for permission.")
	}
	return lines
}

// strongBack is the command that takes a card of the strong few back, in the
// terminal's words: each key the card set, put back to what the config old
// held, or unset where it held none. Args are restored as their flags rather
// than a preset label, since the kind may come from default_kind and change
// before someone takes the card back. flags are words of the terminal's own
// that put back what no config key holds (the directory of a member, --dir).
func strongBack(command, name string, old map[string]any, set map[string]any, flags ...string) string {
	// key=value as one word: bare where the value is, the whole word quoted
	// otherwise, as the terminal reads it.
	word := func(k, v string) string {
		if shellQuote(v) == v {
			return k + "=" + v
		}
		return shellQuote(k + "=" + v)
	}
	back := []string{"yardr " + command, name}
	var unset []string
	for _, key := range []string{"prompt", "cmd", "args", "kind", "model", "effort"} {
		if _, ok := set[key]; !ok {
			continue
		}
		was, had := old[key]
		switch {
		case !had:
			unset = append(unset, key)
		case key == "args":
			words := argWords(was)
			text, _ := json.Marshal(words)
			back = append(back, shellQuote("args="+string(text)))
		default:
			value := fmt.Sprint(was)
			// ParseSets reads a value that is valid JSON as another type. Keep
			// a string such as "true" a string when the command is pasted back.
			if json.Valid([]byte(value)) {
				text, _ := json.Marshal(value)
				value = string(text)
			}
			back = append(back, word(key, value))
		}
	}
	back = append(back, flags...)
	for _, key := range unset {
		back = append(back, "--unset", key)
	}
	return strings.Join(back, " ")
}
