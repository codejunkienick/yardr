package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// askLabels is what a refusal of an ask's stdin says the labels are, so that
// whoever read none of the help is told what to write.
const askLabels = "Q: the question, C: a line of context, O: an option as <label>: <consequence>, W: why the recommended option, R: the command a run ask proposes, G: a word of a general run ask's command, T: its depot test command, S: a says line of a hook add, M: a makes line of one"

// askStdin is the ask that stdin carried: the texts bead ask otherwise takes
// as a quoted argument and as the flags --context, --option, --why, --run,
// --run-word, --run-test, --run-says and --run-makes, written once where the
// shell expands nothing.
type askStdin struct {
	question          string
	context, options  []string
	why, run, runTest string
	says, makes       []string
	general           []string
}

// askLine is one non-blank line of stdin with the number it has there, for a
// refusal to point at.
type askLine struct {
	n    int
	text string
}

// parseAskStdin reads an ask from its stdin, in one of two forms by the first
// line. A first line that starts with a label (a letter, a colon and a space)
// makes every line a labeled field: Q: the question, once and required; C: a
// line of context; O: an option as <label>: <consequence>; W: why the
// recommended option, at most once; R: the command a run ask proposes, at most
// once; G: a word of a general run ask's command, repeatable in order and
// never with R:; T: its depot test command, at most once and only with R:; S: a says
// line of a hook add, and M: a makes line of one, each repeatable in
// order and only with R:. Any other first line is the question, and each
// line after it a line of context, as
// bead create takes the first line of its stdin for the title and the rest for
// the body. The two are never mixed, and a field is one line: nothing
// continues on the next, so a text that wrapped is refused where it can be
// seen and not stored cut short. Blank lines separate and are skipped; the
// number of context lines and the shape of an option are the store's to judge,
// as for the flags.
func parseAskStdin(text string) (askStdin, error) {
	var lines []askLine
	for i, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, askLine{i + 1, line})
		}
	}
	if len(lines) == 0 {
		return askStdin{}, fmt.Errorf("nothing to ask: stdin gave no question, so nothing was asked")
	}
	var got askStdin
	if _, _, labeled := askLabel(lines[0].text); !labeled {
		got.question = lines[0].text
		for _, l := range lines[1:] {
			if label, _, labeled := askLabel(l.text); labeled {
				return askStdin{}, fmt.Errorf("stdin line %d starts with the label %c:, and the first line has none: label every line, with Q: first, or none (%s)", l.n, label, askLabels)
			}
			got.context = append(got.context, l.text)
		}
		return got, nil
	}
	seen := map[byte]int{}
	for _, l := range lines {
		label, value, labeled := askLabel(l.text)
		switch {
		case !labeled:
			return askStdin{}, fmt.Errorf("stdin line %d has no label: the first line has one, so every line is a field, and a field is one line (%s)", l.n, askLabels)
		case !strings.Contains("QCOWRTSMG", string(label)):
			return askStdin{}, fmt.Errorf("stdin line %d has the label %c:, which is none of them (%s)", l.n, label, askLabels)
		case value == "":
			return askStdin{}, fmt.Errorf("stdin line %d is %c: with no words after it (%s)", l.n, label, askLabels)
		case label != 'C' && label != 'O' && label != 'S' && label != 'M' && label != 'G' && seen[label] != 0:
			return askStdin{}, fmt.Errorf("stdin has the label %c: on lines %d and %d, and it is given once", label, seen[label], l.n)
		}
		seen[label] = l.n
		switch label {
		case 'Q':
			got.question = value
		case 'C':
			got.context = append(got.context, value)
		case 'O':
			got.options = append(got.options, value)
		case 'W':
			got.why = value
		case 'R':
			got.run = value
		case 'T':
			got.runTest = value
		case 'S':
			got.says = append(got.says, value)
		case 'M':
			got.makes = append(got.makes, value)
		case 'G':
			got.general = append(got.general, value)
		}
	}
	switch {
	case got.question == "":
		return askStdin{}, fmt.Errorf("stdin has no Q: line, and the question is the one line every ask needs (%s)", askLabels)
	case len(got.general) > 0 && got.run != "":
		return askStdin{}, fmt.Errorf("stdin has R: and G:, and a run ask proposes a command of the list or a general card's words: one of the two")
	case got.runTest != "" && got.run == "":
		return askStdin{}, fmt.Errorf("stdin has T: with no R: after it, and the test command goes beside the depot test command it is for")
	case len(got.says) > 0 && got.run == "":
		return askStdin{}, fmt.Errorf("stdin has S: with no R: after it, and a says line goes beside the hook add command it is for")
	case len(got.makes) > 0 && got.run == "":
		return askStdin{}, fmt.Errorf("stdin has M: with no R: after it, and a makes line goes beside the hook add command it is for")
	}
	return got, nil
}

// askLabel splits a trimmed line into its label and the text after it: a
// letter, a colon, then a space or the end of the line, so that a path
// (C:\yard) or a URL is no label. A letter that is none of the ask's labels is
// still one, so that a refusal can name it.
func askLabel(line string) (label byte, value string, ok bool) {
	if len(line) < 2 || line[1] != ':' || len(line) > 2 && line[2] != ' ' {
		return 0, "", false
	}
	if letter := line[0] | 0x20; letter < 'a' || letter > 'z' {
		return 0, "", false
	}
	return line[0], strings.TrimSpace(line[2:]), true
}

// askFromStdin is bead ask <id> - and bead ask --new -: the ask is read from
// stdin, once, and its texts are set as the flags they stand for, so that
// every check that follows sees the ask as if it were typed. The question
// takes the place of the - in args, which RunE reads (cobra hands PreRunE
// and RunE the same slice). With -, every text goes on stdin: a text flag
// beside it is refused, and so is --new's -b -, which would read the stdin
// the ask is on a second time. The flags that name no prose (--to, --secret,
// --setup, --crew and the rest) go beside it as ever, and the secret ones take
// a name, never stdin, so that nothing of a secret is in the shell's history.
// --take-back reads its own reason (takeBackAsk).
func askFromStdin(c *cobra.Command, args []string) error {
	if len(args) == 0 || args[len(args)-1] != "-" || c.Flags().Changed("take-back") {
		return nil
	}
	for _, name := range []string{"context", "option", "why", "run", "run-word", "run-test", "run-says", "run-makes"} {
		if c.Flags().Changed(name) {
			return fmt.Errorf("with -, put every text on stdin: --%s is given beside it (%s)", name, askLabels)
		}
	}
	if body, _ := c.Flags().GetString("body"); body == "-" {
		return fmt.Errorf("with --new -, stdin holds the ask, and -b - would read it a second time: make the bead with yardr bead create - -b -, and ask on it")
	}
	b, err := io.ReadAll(c.InOrStdin())
	if err != nil {
		return err
	}
	got, err := parseAskStdin(string(b))
	if err != nil {
		return err
	}
	args[len(args)-1] = got.question
	for _, set := range []struct {
		flag  string
		texts []string
	}{
		{"context", got.context}, {"option", got.options}, {"why", []string{got.why}},
		{"run", []string{got.run}}, {"run-word", got.general}, {"run-test", []string{got.runTest}},
		{"run-says", got.says}, {"run-makes", got.makes},
	} {
		for _, text := range set.texts {
			if text == "" {
				continue
			}
			if err := c.Flags().Set(set.flag, text); err != nil {
				return err
			}
		}
	}
	return nil
}

// answerStdinWords is the words and the decline of an answer where either is
// -: read from stdin, trailing newlines trimmed, as note reads its words.
// Stdin is read once, so one of the two is -, not both. Words that stdin gave
// none of are refused here, in words that name stdin and say the ask is
// still open, before anything is written: a shell may have emptied them.
func answerStdinWords(c *cobra.Command, id string, seq uint64, words, decline string) (string, string, error) {
	switch {
	case words == "-" && decline == "-":
		return "", "", fmt.Errorf("one - per answer: --words and --decline would each read stdin, which is read once")
	case words != "-" && decline != "-":
		return words, decline, nil
	}
	text, err := stdinWords(c, "-")
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(text) == "" {
		return "", "", fmt.Errorf("nothing to write: stdin gave no words, so ask %d on %s is still open", seq, id)
	}
	if words == "-" {
		return text, decline, nil
	}
	return words, text, nil
}
