// The general run card: any yardr command no specific card fits, filed
// only by the yardmaster while the general_cards setting stands true,
// shown whole and unchecked, and carried out by the server as typed
// (internal/server/carry_general.go). The writer sends the command as
// verbatim words (AskRun.General); no line is split and no quoting is
// parsed, ever, so a command with spaces rides as one word. What the
// list carries is refused here, naming its own card as the only door;
// what the verbatim never-list names is refused before any row is
// read; an unknown form files and fails closed at the carrying out
// through the CLI's own validation.
package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// generalDescription is the general card's one help line, fixed words.
const generalDescription = "a yardr command no card fits, shown whole and unchecked"

// generalTTL is how long a general card is good for.
const generalTTL = time.Hour

// generalRow is the general card's row, out of the list (runList): the
// general card stays out of RunCommands, and a build without it refuses
// the stored argv through its own unknownRun and alone paths, since
// filing refused every form a row carries.
var generalRow = runCommand{
	use:         "yardr <command> (a general card)",
	description: generalDescription,
	cardRun:     (*wtx).generalCard,
	stale:       (*wtx).generalStale,
	weigh:       generalWeight,
	runRun:      runGeneral,
}

// rowFor is the row of the stored run was with the command as it runs:
// the general row for a general card, and parseRun's row for any other.
func rowFor(was AskRun) (*runCommand, []string, error) {
	if was.General != nil {
		return &generalRow, was.General, nil
	}
	return parseRun(was.Argv)
}

// generalWeight weighs every general card code: approving runs a
// command the yard has not checked, as the operator. It is strong
// (StrongWeight) on that class alone.
func generalWeight(_ *wtx, run AskRun) ([]AskWeight, error) {
	return []AskWeight{weighs(WeightCode, "a yardr command no card fits, shown whole and unchecked: "+runCommandLine(run.Argv))}, nil
}

// runGeneral records the approval's carrying out: the server runs the
// command after this write (needsCarry), so the answer stands pending.
func runGeneral(_ context.Context, _ *wtx, _ AskRun, _ string, _ *approvedOn) (string, error) {
	return CarryPending, nil
}

// generalCard is the general row's card: at the asking, good for
// generalTTL from now; at an approval, held to the deadline the ask
// stored, which generalStale enforces.
func (w *wtx) generalCard(run AskRun, asking bool) (AskRun, error) {
	words := run.General
	if words == nil {
		words = run.Argv
	}
	if asking {
		return generalCardFor(words, now().Add(generalTTL)), nil
	}
	expiry, err := time.Parse(time.RFC3339, run.Seen["general expires"])
	if err != nil {
		return AskRun{}, fmt.Errorf("the card holds no general expiry the yard wrote")
	}
	return generalCardFor(words, expiry), nil
}

// generalCardFor is the stored general card of words, good until
// expiry: the words whole as both Argv and General, the fixed
// description, the unchecked sentences with the words numbered, and
// the deadline alone in Seen. Nothing the writer sent beside the words
// stands.
func generalCardFor(words []string, expiry time.Time) AskRun {
	words = slices.Clone(words)
	return AskRun{
		Argv:        words,
		General:     words,
		Description: generalDescription,
		Does:        generalDoes(words, expiry),
		Seen:        map[string]string{"general expires": expiry.UTC().Format(time.RFC3339)},
	}
}

// generalDoes are the general card's sentences: the yard has not
// checked the command, no way back is shown, the good-until time, what
// the command prints is told to the asker, then the words, one a line
// as n: (len) word, each through Letters, a word past 128 characters
// cut with its head and tail and the cut marked, and a log= or prompt=
// word marked not read by the yard.
func generalDoes(words []string, expiry time.Time) []string {
	does := []string{
		"The yard has not checked this command: approving runs it as typed, as the operator.",
		"No way back is shown: nothing here says how to undo it.",
		fmt.Sprintf("It is good until %s.", expiry.Local().Format("15:04:05")),
		"What the command prints is told to the asker.",
	}
	for i, word := range words {
		does = append(does, fmt.Sprintf("%d: (%d) %s", i+1, utf8.RuneCountInString(word), generalWordShown(word)))
	}
	return does
}

// generalWordShown is one word of a general card as it is shown: through
// Letters, cut past 128 characters to its first and last 64 with the
// cut and the full length marked, and a log= or prompt= word marked as
// not read by the yard.
func generalWordShown(word string) string {
	shown := Letters(word)
	if runes := []rune(shown); len(runes) > 128 {
		shown = string(runes[:64]) + fmt.Sprintf("…[+%d cut]…", len(runes)-128) + string(runes[len(runes)-64:])
	}
	if _, ok := generalFileWord(word); ok {
		shown += " (not read by the yard)"
	}
	return shown
}

// generalFileWord is the value of a log= or prompt= word, a --set
// word's included: the file the word names, which the card marks unread.
func generalFileWord(word string) (string, bool) {
	candidate := word
	if value, ok := strings.CutPrefix(candidate, "--set="); ok {
		candidate = value
	}
	for _, key := range []string{"log=", "prompt="} {
		if value, ok := strings.CutPrefix(candidate, key); ok {
			return value, true
		}
	}
	return "", false
}

// generalEnvFileWord says whether the word opens env_file=, a --set
// word's included: a file of secrets no card can show.
func generalEnvFileWord(word string) bool {
	candidate := word
	if value, ok := strings.CutPrefix(candidate, "--set="); ok {
		candidate = value
	}
	return strings.HasPrefix(candidate, "env_file=")
}

// GeneralCardsSetting says the yardmaster may file a general run
// card (this file): any yardr command no specific card fits, shown
// whole and unchecked, for the operator to approve. Unset it is off;
// turning it on is the operator's in a terminal, and it never rides a
// card (settingAsk leaves it Alone).
const GeneralCardsSetting = "general_cards"

// checkGeneralCards takes the general_cards setting's two values.
func checkGeneralCards(v string) error {
	if v != "true" && v != "false" {
		return fmt.Errorf("%s %q: true or false", GeneralCardsSetting, v)
	}
	return nil
}

// askedGeneral checks a general run ask written by actor for to, and
// answers it as the ask stores it: the verbatim words whole as both
// Argv and General, with the yard's unchecked sentences. It runs the
// pane and known-actor guards askedRun runs, then the yardmaster and
// general_cards gates, the form rules, the verbatim never-list, the
// alone match on the verbatim words and refusal (a), in that order.
// Beside-argv fields the writer sent are thrown away, as askedRun
// throws the others.
func (w *wtx) askedGeneral(ctx context.Context, run AskRun, to, actor string) (*AskRun, error) {
	if len(run.Argv) > 0 {
		return nil, fmt.Errorf("a general run ask takes its command as verbatim words alone (General), with Argv empty: file it with --run-word, one word a use, or as G: lines on stdin, not with --run")
	}
	const whose = "a command for the operator to approve is proposed by a crew member or a user of the yard"
	sess, err := paneSession(w.Tx, callerOf(ctx))
	if err != nil {
		return nil, err
	}
	if sess != nil && !sess.Manual() {
		return nil, fmt.Errorf("%s; this is the session %s of a bead's agent: say what is needed in a note on the bead (yardr bead note)", whose, sess.ID)
	}
	if err := startingRefused(w.Tx, sessionOf(ctx)); err != nil {
		return nil, err
	}
	if w.Bucket(bCrew).Get([]byte(actor)) == nil && w.Bucket(bUsers).Get([]byte(actor)) == nil {
		return nil, fmt.Errorf("%s; this is %s: say what is needed in a note on the bead (yardr bead note)", whose, or(actor, "nobody"))
	}
	ym, err := yardmasterIn(w.Tx)
	if err != nil {
		return nil, err
	}
	if actor != ym {
		return nil, fmt.Errorf("a general run card is the yardmaster's (%s) to file: this is %s", ym, or(actor, "nobody"))
	}
	if w.settingValue(GeneralCardsSetting) != "true" {
		return nil, fmt.Errorf("general run cards are off: the operator turns them on in a terminal with yardr settings set %s true", GeneralCardsSetting)
	}
	ops, err := operatorsIn(w.Tx)
	if err != nil {
		return nil, err
	}
	if ops.none() {
		return nil, fmt.Errorf("a command is the operator's to approve, and this yard has none: yardr settings set operator <name>")
	}
	if !ops.is(to) {
		return nil, fmt.Errorf("a general run card is %s to approve (%s): the yard runs it as them, so %s cannot be asked to", ops.article(), ops, to)
	}
	words := slices.Clone(run.General)
	if len(words) > 0 && words[0] == "yardr" {
		words = words[1:]
	}
	if len(words) == 0 {
		return nil, fmt.Errorf("a general run card names no command: give the command as verbatim words, one word a --run-word or a G: line")
	}
	if strings.HasPrefix(words[0], "-") {
		// The never-list and the alone match read the command from the
		// first words; the CLI finds it past a flag, and the carrying out
		// refuses any flag among them (internal/cli/generalcard.go).
		return nil, fmt.Errorf("a general run card names its command first, with no flag before it: put %s after the command's words", words[0])
	}
	for _, word := range words {
		if strings.ContainsFunc(word, unicode.IsControl) {
			return nil, fmt.Errorf("a general run card shows every word whole, and a word holds a control character the card cannot show: %s", runCommandLine(words))
		}
		if word == "-" {
			return nil, fmt.Errorf("a general run card carries no standard input: a lone - word reads it, and the yard carries none, so name the words the command takes instead")
		}
		if word == "--actor" || word == "--home" || strings.HasPrefix(word, "--actor=") || strings.HasPrefix(word, "--home=") {
			return nil, fmt.Errorf("a general run card never names --actor or --home: the yard runs the command as the operator who approves it, from the yard's home")
		}
	}
	for _, word := range words {
		if value, ok := generalFileWord(word); ok && !strings.HasPrefix(value, "/") {
			return nil, fmt.Errorf("a general run card names a file the yard has not read only as an absolute path, so the card and the run read the one file: %s is relative", word)
		}
		if generalEnvFileWord(word) {
			return nil, fmt.Errorf("a general run card never names env_file: that file holds secrets no card can show, so set it in a terminal")
		}
	}
	if err := generalNever(words); err != nil {
		return nil, err
	}
	if err := w.matchAlone(ctx, words, actor, true); err != nil {
		return nil, err
	}
	if c, _, err := parseRow(words); err == nil {
		return nil, fmt.Errorf("%s has its own card (%s): the specific card is the only door, so file it with --run, not as a general card", runCommandLine(words), c.use)
	}
	card := generalCardFor(words, now().Add(generalTTL))
	return &card, nil
}

// generalNever refuses the verbatim words the general card never
// carries, before and independent of parseRow: what settles another
// ask, what rewrites the approval machinery, and what acts on a seq,
// thread, file or credential the card does not show. An unknown form
// is no refusal here: it files and fails closed at the carrying out
// through the CLI's own validation.
func generalNever(words []string) error {
	line := runCommandLine(words)
	has := func(flag string) bool { return slices.Contains(words, flag) }
	flagWord := func(flag string) bool {
		return slices.ContainsFunc(words, func(word string) bool { return word == flag || strings.HasPrefix(word, flag+"=") })
	}
	switch {
	case len(words) >= 2 && words[0] == "bead" && words[1] == "answer":
		return fmt.Errorf("a general card never carries %s: an ask that settles another ask is answered in person, never carried", line)
	case len(words) >= 2 && words[0] == "bead" && words[1] == "ask" && has("--take-back"):
		return fmt.Errorf("a general card never carries %s: an ask that settles another ask is answered in person, never carried", line)
	case words[0] == "upgrade":
		return fmt.Errorf("a general card never carries %s: upgrading rewrites the yard itself, and is the operator's in a terminal", line)
	case words[0] == "watch":
		return fmt.Errorf("a general card never carries %s: watch never returns, and the yard carries one command at a time", line)
	case words[0] == "down" || words[0] == "uninstall" || words[0] == "daemon" || words[0] == "server":
		return fmt.Errorf("a general card never carries %s: a carried stop kills the server that carries it, and it would run again at the next start", line)
	case words[0] == "reply" || words[0] == "react" || words[0] == "inbox":
		return fmt.Errorf("a general card never carries %s: it acts on a seq or thread the card does not show, and as the operator it reads as their own word", line)
	case len(words) >= 2 && words[0] == "user" && words[1] == "key":
		return fmt.Errorf("a general card never carries %s: a user's address is typed unseen, and no card shows it", line)
	}
	if len(words) >= 1 && words[0] == "mail" && (flagWord("--with") || flagWord("--file")) {
		return fmt.Errorf("a general card never carries %s: --with and --file name a thread or a file the card does not show", line)
	}
	if words[0] == "call" && flagWord("--as") {
		return fmt.Errorf("a general card never carries %s: --as spends another person's credential, which no unchecked card carries", line)
	}
	if len(words) >= 2 && words[0] == "settings" && words[1] == "set" {
		if len(words) < 3 {
			return fmt.Errorf("a general card never carries %s: it names no setting", line)
		}
		key, _, _ := strings.Cut(words[2], "=")
		if _, ok := SettingOf(key); !ok {
			return fmt.Errorf("no setting %q; there is: %s", key, strings.Join(SettingKeys(), ", "))
		}
		if settingAsk(key) == Alone {
			return fmt.Errorf("a general card never carries %s: setting %s is the operator's alone, typed in a terminal and never proposed as a run ask", line, key)
		}
	}
	if len(words) >= 2 && words[0] == "source" && words[1] == "set" {
		for _, word := range words {
			candidate := word
			if value, ok := strings.CutPrefix(candidate, "--set="); ok {
				candidate = value
			}
			if candidate == "env_file" || candidate == "mint_command" ||
				strings.HasPrefix(candidate, "env_file=") || strings.HasPrefix(candidate, "mint_command=") {
				return fmt.Errorf("a general card never carries %s: env_file and mint_command name a credential's file or mint no card can show", line)
			}
		}
		if !slices.ContainsFunc(words, func(word string) bool { return strings.Contains(word, "=") }) {
			return fmt.Errorf("a general card never carries %s: it names no setting of the source", line)
		}
	}
	return nil
}

// generalStale says why the approval of the general ask seq is refused:
// its deadline has passed. There is no yard-changed rule: before the
// deadline the approval is held to the fresh card and carries on.
func (w *wtx) generalStale(seq uint64, was, _ AskRun) error {
	expiry, err := time.Parse(time.RFC3339, was.Seen["general expires"])
	if err != nil {
		return fmt.Errorf("ask %d holds no general expiry the yard wrote. %s", seq, nothingRan)
	}
	if !now().Before(expiry) {
		return fmt.Errorf("ask %d was good until %s and that time has passed: the general card has expired. %s", seq, expiry.Local().Format("15:04:05"), nothingRan)
	}
	return nil
}
