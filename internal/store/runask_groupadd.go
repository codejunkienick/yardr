// The group add row of the run list (runList): a new group, proposed on a
// card and added only on the operator's approval. What the group is rides
// argv as the terminal's own words, so the card's command pastes into a
// terminal: its runner and limit, and cmd, dir, kind, args, prompt and
// worktrees as --set key=value. The run-ask line splits at spaces and
// quotes nothing, so cmd and args are one word each; a longer command is
// the operator's to type. A prompt is a file under the yard's home: its
// path rides argv and its text rides beside it (AskRun.Role), read by the
// asker's own command, so the card shows the text the ask carried. The
// yard reads no file for an asker's eyes, since the asker reads its own ask
// back. It reads the prompt file only to hold it to that text, when the
// card is written and again at the approval, so a file that holds other
// text by then is refused. That file is no more than where the text was
// read from: the approval writes the carried text to a file of the yard's
// own (runask_promptfile.go) and the group saves that path. What defines
// whose environment a group runs in, and every group that hands beads to
// another yard, stay out, in words that say where they go instead, so no
// check downstream can miss them.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"
)

// groupAddUse is how a refusal names the group add row.
const groupAddUse = "yardr group add <name> --runner <runner> [--limit <n>] [--set k=v ...]"

// groupAddKeys are the settings the row carries as --set key=value, in the
// order its stored form writes them.
var groupAddKeys = []string{"cmd", "dir", "kind", "args", "prompt", "worktrees"}

// groupAddSpec is a group add proposal as the yard holds it: the group to
// add, with its limit and config resolved as group add resolves them, and
// the prompt text the approval writes to its file.
type groupAddSpec struct {
	name   string
	runner string
	limit  int
	config map[string]any // dir, worktrees and prompt as absolute paths
	prompt string         // the text the approval writes to the file config["prompt"] names; "" for none
}

// group is the group the proposal adds, as the terminal's group add saves it.
func (s groupAddSpec) group() Group {
	return Group{Name: s.name, Runner: s.runner, Limit: s.limit, Config: maps.Clone(s.config)}
}

// parseGroupAdd reads the words of group add <name> with its flags. It
// reads no record of the yard. The stored form is the terminal's own:
// --runner, --limit if named, then each setting as --set key=value. What
// this row never carries refuses here, in words that say where it goes
// instead.
func parseGroupAdd(rest []string) ([]string, error) {
	var name, runner, limit, peerKey string
	sets := map[string]string{}
	set := func(kv string) error {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return fmt.Errorf("--run takes %s: --set %q: want key=value", groupAddUse, kv)
		}
		if _, dup := sets[k]; dup {
			return fmt.Errorf("--run takes %s: %s is set twice", groupAddUse, k)
		}
		switch k {
		case "cmd", "dir", "kind", "args", "prompt", "worktrees":
			var js any
			switch {
			case v == "":
				return fmt.Errorf("--run takes %s: %s needs its value", groupAddUse, k)
			case json.Unmarshal([]byte(v), &js) == nil:
				// The terminal stores what reads as JSON as that, a number
				// or a list, and a card cannot write a string as JSON.
				return fmt.Errorf("--run takes %s: %s=%s reads as JSON, which the terminal stores as a value of its own type and not as text: name a value that is plain text", groupAddUse, k, v)
			}
			sets[k] = v
		case "env_file", "env_command":
			return fmt.Errorf("--run takes %s: %s is the operator's alone, typed in a terminal and never proposed as a run ask: env is the operator's alone, so add the group without it and give it one in a terminal with yardr group set <name> env_file=<file>", groupAddUse, k)
		case "log":
			return fmt.Errorf("--run takes %s: log is a file the yard appends to, and a file path rides no card: add the group without it and set it in a terminal with yardr group set <name> log=<file>, in the same trip as env_file", groupAddUse)
		case GroupPeer, GroupRemote, GroupTries:
			// Said once the runner is known: it is a peer group's.
			peerKey = k
		default:
			return fmt.Errorf("--run takes %s: %s is no setting of this card: add the group without it and set it afterwards with yardr group set <name> %s=<value>", groupAddUse, k, k)
		}
		return nil
	}
	for i := 0; i < len(rest); i++ {
		word := rest[i]
		if !strings.HasPrefix(word, "-") {
			switch {
			case strings.Contains(word, "="):
				return nil, fmt.Errorf("--run takes %s: %q is no group name: a setting goes as --set key=value", groupAddUse, word)
			case name != "":
				return nil, fmt.Errorf("--run takes %s: one group, not %s and %s", groupAddUse, name, word)
			}
			name = word
			continue
		}
		flag, value, joined := strings.Cut(word, "=")
		switch flag {
		case "--runner", "--limit", "--set":
		case "--members":
			return nil, fmt.Errorf("--run takes %s, and group add with --members adds a group of people, which anyone adds: run it directly", groupAddUse)
		default:
			return nil, fmt.Errorf("--run takes %s, and group add with %s is the operator's to type in a terminal", groupAddUse, flag)
		}
		if !joined {
			if i+1 == len(rest) {
				return nil, fmt.Errorf("--run takes %s: %s needs its value", groupAddUse, flag)
			}
			i++
			value = rest[i]
		}
		switch flag {
		case "--runner":
			if runner != "" {
				return nil, fmt.Errorf("--run takes %s: --runner once", groupAddUse)
			}
			runner = value
		case "--limit":
			if limit != "" {
				return nil, fmt.Errorf("--run takes %s: --limit once", groupAddUse)
			}
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 || strings.Trim(value, "0123456789") != "" {
				return nil, fmt.Errorf("--run takes %s: --limit %q is no whole number of 1 or more: a group starts with a limit of 1 or more, and is paused after with yardr group set", groupAddUse, value)
			}
			limit = strconv.Itoa(n)
		case "--set":
			if err := set(value); err != nil {
				return nil, err
			}
		}
	}
	switch {
	case name == "":
		return nil, fmt.Errorf("--run takes %s: it names no group", groupAddUse)
	case runner == "":
		return nil, fmt.Errorf("--run takes %s: it names no runner: name one as --runner manual, herdr or exec", groupAddUse)
	case runner == PeerRunner:
		return nil, fmt.Errorf("--run takes %s: a peer group hands its beads to another yard, and no card carries it: it needs no approval, so run yardr group add <name> --runner peer --set peer=<peer> directly", groupAddUse)
	case peerKey != "":
		return nil, fmt.Errorf("--run takes %s: %s is a peer group's, which hands its beads to another yard, and no card carries it: a peer group needs no approval, so run yardr group add <name> --runner peer --set peer=<peer> directly", groupAddUse, peerKey)
	case runner != "manual" && runner != "herdr" && runner != "exec":
		return nil, fmt.Errorf("--run takes %s: group %s: %q is none of the runners a card adds (manual, herdr, exec)", groupAddUse, name, runner)
	}
	out := []string{name, "--runner", runner}
	if limit != "" {
		out = append(out, "--limit", limit)
	}
	for _, k := range groupAddKeys {
		if v, ok := sets[k]; ok {
			out = append(out, "--set", k+"="+v)
		}
	}
	return out, nil
}

// groupAddOf reads a stored group add argv: the group, its runner, its
// limit (0 where none was named) and each setting.
func groupAddOf(argv []string) (name, runner string, limit int, sets map[string]string) {
	name, sets = argv[2], map[string]string{}
	for i := 3; i+1 < len(argv); i += 2 {
		switch argv[i] {
		case "--runner":
			runner = argv[i+1]
		case "--limit":
			limit, _ = strconv.Atoi(argv[i+1])
		case "--set":
			k, v, _ := strings.Cut(argv[i+1], "=")
			sets[k] = v
		}
	}
	return name, runner, limit, sets
}

// groupAddName holds the name of a group to add: well shaped, free, and no
// user's or crew member's, since mail to a name reaches one party. A name
// in use is refused whatever else is wrong, since this adds and never
// replaces; an approval reads it again, so a name taken since is refused.
func (w *wtx) groupAddName(name string) error {
	switch {
	case !peerMailName.MatchString(name):
		return fmt.Errorf("group %q: use letters, digits, '.', '_', '-' or '/' (starting with a letter or digit)", name)
	case strings.Contains(name, ".."):
		return fmt.Errorf("group %s: that name has a dot path part: choose another", name)
	case name == RouteWait:
		return fmt.Errorf("group wait: wait ends a route (--else wait), so no group takes the name")
	case w.Bucket(bGroups).Get([]byte(name)) != nil:
		return fmt.Errorf("%w: there is a group %s already: this adds, it never replaces; change it with yardr group set %s, or pick another name", ErrConflict, name, name)
	case w.Bucket(bUsers).Get([]byte(name)) != nil:
		return fmt.Errorf("group %s: that is a user's name, and mail to a name reaches one party: choose another", name)
	case w.Bucket(bCrew).Get([]byte(name)) != nil:
		return fmt.Errorf("group %s: that is a crew member's name, and one name is one party: choose another", name)
	}
	return nil
}

// groupAddProposal holds a group add ask to the yard as it stands: the name
// is free, the config is one the store would save (configProblems, as
// putGroup holds it), a kind is one the yard knows, a directory is a place
// the yard knows and a prompt is a file under the yard's home that holds
// the text the ask carries. It answers the proposal resolved: the default
// limit and the yard's default_kind as group add gives them, paths
// absolute (the prompt's is the file the approval writes), and the prompt
// text.
func (w *wtx) groupAddProposal(run AskRun) (groupAddSpec, error) {
	name, runner, limit, sets := groupAddOf(run.Argv)
	if err := w.groupAddName(name); err != nil {
		return groupAddSpec{}, err
	}
	if limit == 0 {
		limit = DefaultLimit(runner)
	}
	config := map[string]any{}
	for k, v := range sets {
		config[k] = v
	}
	// A herdr group that names no kind gets the yard's default_kind, as the
	// terminal's group add gives it.
	if _, named := sets["kind"]; !named && runner == "herdr" {
		if kind := w.settingValue(DefaultKindSetting); kind != "" {
			config["kind"] = kind
		}
	}
	if ps := configProblems(GroupConfig, name, runner, config, nil); len(ps) > 0 {
		return groupAddSpec{}, ps[0]
	}
	if kind, named := sets["kind"]; named {
		if _, ok := HarnessLabels[kind]; !ok {
			have := mapsKeys(HarnessLabels)
			slices.Sort(have)
			return groupAddSpec{}, fmt.Errorf("group %s: kind %q is no harness kind the yard knows (it knows: %s): name one as --set kind=<kind>", name, kind, strings.Join(have, ", "))
		}
	}
	spec := groupAddSpec{name: name, runner: runner, limit: limit, config: config}
	for _, key := range []string{"dir", "worktrees"} {
		if dir, ok := sets[key]; ok {
			clean, err := w.knownDir("group "+name, key, dir, "--set "+key+"=<dir>")
			if err != nil {
				return groupAddSpec{}, err
			}
			config[key] = clean
		}
	}
	if path, ok := sets["prompt"]; ok {
		resolved, err := w.groupPrompt(name, path, run.Role)
		if err != nil {
			return groupAddSpec{}, err
		}
		config["prompt"], spec.prompt = resolved, run.Role
	} else if run.Role != "" {
		return groupAddSpec{}, fmt.Errorf("group %s: the ask carries prompt text but names no prompt: name the file it was read from as --set prompt=<path>", name)
	}
	return spec, nil
}

// groupPrompt holds the prompt file of the group name that path names to the
// text the ask carries (heldPrompt), and answers the path the group saves.
func (w *wtx) groupPrompt(name, path, text string) (string, error) {
	return w.heldPrompt(name, "group "+name, path, text, promptWords{
		otherwise: "the group is added in a terminal", spelled: "--set prompt=<path>"})
}

// promptWords are the words of a refusal about a prompt that depend on the
// command it is for: where the change is made instead, for a text a card
// cannot carry, and how the command spells the path.
type promptWords struct{ otherwise, spelled string }

// heldPrompt holds the prompt file of subject ("group builders", name
// "builders") that path names to the text the ask carries: a regular file
// that stands under the yard's home once its symlinks are resolved, holding
// exactly that text, which a card shows whole. That file is where the text
// was read from and is not the prompt: it answers the file the approval
// writes the text to (yardPromptPath), which is the path the definition
// saves, so that a write to the named file after the approval reaches no
// session. A file the yard wrote for a prompt is not one to name, so a
// rewritten one cannot stand as both what the card says is there and what
// it will be. The named file is read only as far as the carried text goes and
// is never shown, so nothing of it reaches an asker who cannot already write
// it out.
func (w *wtx) heldPrompt(name, subject, path, text string, say promptWords) (string, error) {
	switch {
	case strings.TrimSpace(text) == "":
		return "", fmt.Errorf("%s: the ask carries no prompt text: the text goes beside --run, read from the file prompt= names, which yardr bead ask does", subject)
	case len(text) > MaxRunCrewRole:
		return "", fmt.Errorf("%s: the prompt text it carries is %d bytes, past the %d a card holds and shows whole: %s", subject, len(text), MaxRunCrewRole, say.otherwise)
	case !utf8.ValidString(text):
		return "", fmt.Errorf("%s: the prompt text is not valid UTF-8, so a card cannot carry it whole", subject)
	case strings.IndexFunc(text, crewRoleHidden) >= 0 || strings.Contains(text, NoteFilesMark) || strings.Contains(text, WeightMark):
		return "", fmt.Errorf("%s: the prompt text holds control characters or a mark the yard draws on a card itself, which a card would not show as written: %s", subject, say.otherwise)
	case !filepath.IsAbs(path):
		return "", fmt.Errorf("%s: prompt %q is no absolute path: a relative one is read against the yard's own directory, which a card cannot show: name the file the text was read from, under the yard's home, as %s", subject, path, say.spelled)
	}
	real, held, err := w.homeFile(subject, path, len(text))
	if err != nil {
		return "", err
	}
	if yardPromptName.MatchString(real) {
		return "", fmt.Errorf("%s: prompt file %s is one the yard wrote when it approved a prompt, and it stays as it was written: a new prompt is a new file, so name one under the yard's home as %s", subject, real, say.spelled)
	}
	if string(held) != text {
		return "", fmt.Errorf("%s: the prompt file %s holds other text than the ask carries: it was changed since the ask was written, or the ask was written from another file", subject, real)
	}
	saved, err := w.yardPromptPath(subject, name, text)
	if err != nil {
		return "", err
	}
	// A file that stands at the yard's name with other text is refused here,
	// at the card, and not found at the approval: a card whose file is not
	// the text it shows cannot be approved.
	if _, err := promptStands(subject, saved, text); err != nil {
		return "", err
	}
	return saved, nil
}

// homeDirs is the yard's home as named and as its symlinks resolve it, in the
// words of a refusal that names subject.
func (w *wtx) homeDirs(subject string) (home, real string, err error) {
	if home, err = filepath.Abs(w.home); err != nil {
		return "", "", fmt.Errorf("%s: resolve the yard's home: %w", subject, err)
	}
	if real, err = filepath.EvalSymlinks(home); err != nil {
		return "", "", fmt.Errorf("%s: resolve the yard's home: %w", subject, err)
	}
	return home, real, nil
}

// homeFile is the regular file at path that stands under the yard's home
// once its symlinks are resolved, as that path, and as much of it as limit
// bytes and one more: a prompt file, in the words of a refusal that names
// subject. A path outside the home is refused before the disk is asked
// about it, so the refusal tells nothing of what stands elsewhere.
func (w *wtx) homeFile(subject, path string, limit int) (real string, data []byte, err error) {
	home, homeReal, err := w.homeDirs(subject)
	if err != nil {
		return "", nil, err
	}
	under := func(p string) bool {
		return strings.HasPrefix(p, filepath.Clean(home)+string(filepath.Separator)) || strings.HasPrefix(p, filepath.Clean(homeReal)+string(filepath.Separator))
	}
	if !under(filepath.Clean(path)) {
		return "", nil, fmt.Errorf("%s: prompt file %s is outside the yard's home: a card carries a prompt that stands under it", subject, filepath.Clean(path))
	}
	real, err = filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", nil, fmt.Errorf("%s: prompt file %s: %s", subject, path, OneLine(err.Error()))
	}
	if !strings.HasPrefix(real, filepath.Clean(homeReal)+string(filepath.Separator)) {
		return "", nil, fmt.Errorf("%s: prompt file %s resolves to %s, outside the yard's home: a card carries a prompt that stands under it", subject, path, real)
	}
	// Opened without blocking and judged by the open file, so a file that
	// became a pipe since it was named cannot hold the write.
	f, err := os.OpenFile(real, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", nil, fmt.Errorf("%s: prompt file %s: %s", subject, real, OneLine(err.Error()))
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("%s: prompt file %s is not a regular file", subject, real)
	}
	data, err = io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return "", nil, fmt.Errorf("%s: prompt file %s: %s", subject, real, OneLine(err.Error()))
	}
	return real, data, nil
}

// checkGroupAdd is adding a group as the asker: what defines what the
// yard runs is the operator's, which a run ask may propose. The proposal
// is held first, so a config, directory or prompt the yard would refuse is
// refused in its own words whoever asks. The gate then is the store's own
// (checkGroupDefine), which marks what it holds to the operator alone: this
// row is what proposes it, so the mark is changed here and nowhere else. A
// group it holds to no one, a manual one, needs no approval.
func checkGroupAdd(ctx context.Context, w *wtx, run AskRun, actor string) error {
	spec, err := w.groupAddProposal(run)
	if err != nil {
		return err
	}
	err = checkGroupDefine(ctx, w, Group{}, spec.group(), actor)
	var o *OperatorOnlyError
	if errors.As(err, &o) {
		return &OperatorOnlyError{msg: o.msg, ask: Askable}
	}
	return err
}

// groupAddCard is the card of group add <name>: the runner and limit, the
// command or the agent it starts, and its whole prompt text, each as the
// yard holds them. Asking refuses a group the yard would refuse and a
// name in use already, since this adds and never replaces; an approval
// reads it again, and a prompt file that holds other text by then is
// refused there, while stale says what else moved. The card holds the
// prompt's SHA-256 in Seen and its text in Role.
func (w *wtx) groupAddCard(run AskRun, _ bool) (AskRun, error) {
	spec, err := w.groupAddProposal(run)
	if err != nil {
		return AskRun{}, err
	}
	_, _, limit, sets := groupAddOf(run.Argv)
	byDefault := ""
	if limit == 0 {
		byDefault = ", the runner's default"
	}
	str := func(key string) string { s, _ := spec.config[key].(string); return s }
	does := []string{fmt.Sprintf("adds the group %s on the %s runner, with limit %d%s.", spec.name, spec.runner, spec.limit, byDefault)}
	switch spec.runner {
	case "exec":
		does = append(does, fmt.Sprintf("Each session runs sh -c with %s, as its program stands when the session starts.", strconv.Quote(str("cmd"))))
		if dir := str("dir"); dir != "" {
			does = append(does, "The command runs in "+dir+".")
		} else {
			does = append(does, "The command runs in the depot of the bead.")
		}
	case "herdr":
		kind := str("kind")
		switch {
		case kind == "":
			does = append(does, "It starts claude agents, the herdr runner's own default.")
		case sets["kind"] == "":
			does = append(does, fmt.Sprintf("It starts %s (%s) agents, the yard's default_kind.", kind, or(HarnessLabels[kind], kind)))
		default:
			does = append(does, fmt.Sprintf("It starts %s (%s) agents.", kind, HarnessLabels[kind]))
		}
		if args := str("args"); args != "" {
			does = append(does, fmt.Sprintf("It starts them with %s.", strconv.Quote(args)))
			if presetSkipsPermissions(Permission{Args: []string{args}}) {
				does = append(does, "It skips permissions: it runs without asking for permission.")
			}
		}
	}
	if dir := str("worktrees"); dir != "" {
		does = append(does, "Its bead worktrees are made under "+dir+".")
	}
	does = append(does, fmt.Sprintf("It sets no env_file: the operator gives one in a terminal, with yardr group set %s env_file=<file>.", spec.name))
	seenMap := map[string]string{}
	if spec.prompt != "" {
		sum := sha256.Sum256([]byte(spec.prompt))
		seenMap["prompt "+str("prompt")] = hex.EncodeToString(sum[:])
		does = append(does, promptWritten(str("prompt")))
		does = append(does, textLines(spec.prompt)...)
	}
	return AskRun{
		Argv:   run.Argv,
		Does:   does,
		Back:   runCommandLine([]string{"group", "rm", spec.name}),
		BackBy: "the operator or the yardmaster",
		Seen:   seenMap,
		Role:   spec.prompt,
	}, nil
}

// groupAddStale says why the ask seq for group add is approved no more:
// the yard would not write its card as it stands now.
func (w *wtx) groupAddStale(seq uint64, was, now AskRun) error {
	return staleCard(seq, was, now)
}

// runGroupAdd adds the group as actor, through the store function the
// terminal's group add calls (putGroup), so the checks that function makes
// are the same: the approver is the operator, who may define a group. The
// proposal is held once more in this write, so what is saved is what the
// card showed.
func runGroupAdd(ctx context.Context, w *wtx, run AskRun, actor string, _ *approvedOn) (string, error) {
	spec, err := w.groupAddProposal(run)
	if err != nil {
		return "", err
	}
	g := spec.group()
	if err := putGroup(ctx, w, &g, actor, false); err != nil {
		return "", err
	}
	if path, ok := spec.config["prompt"].(string); ok {
		if err := w.writePrompt("group "+g.Name, path, spec.prompt); err != nil {
			return "", err
		}
	}
	parts := []string{g.Runner}
	if kind, _ := g.Config["kind"].(string); kind != "" {
		parts = append(parts, kind)
	}
	return fmt.Sprintf("group %s (%s · limit %d)", g.Name, strings.Join(parts, " · "), g.Limit), nil
}
