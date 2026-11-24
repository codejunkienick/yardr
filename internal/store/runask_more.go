// The rows of the list after the first five (runList): a user added, a
// setting, a peer renamed and the yard brought up; a crew member's
// grants are in runask_crewgrant.go. Each has what a row has: how its
// words are read into one stored form, the command's own check as the
// asker, the card the yard writes from its records, why an approval of
// an older card is refused, and the store function that makes the
// change. Two helpers the identity cards share stand here too
// (identitySays, foldIdentityKey), read by the user identity row
// (runask_mapping.go).
package store

import (
	"context"
	"fmt"
	"slices"
	"strings"

	bolt "go.etcd.io/bbolt"

	"yardr.dev/yardr/internal/source"
)

// parseUserAdd reads the words of user add <name>.
func parseUserAdd(rest []string) ([]string, error) {
	const use = "yardr user add <name>"
	if len(rest) != 1 {
		return nil, fmt.Errorf("--run takes %s: one user, not %d words", use, len(rest))
	}
	return []string{rest[0]}, nil
}

// checkUserAdd is adding a user's own check as the asker.
func checkUserAdd(ctx context.Context, w *wtx, argv []string, actor string) error {
	return w.may(ctx, actor, ActAddUser, Object{Kind: KindUser, Name: argv[2], Ask: Askable})
}

// userAddCard is the card of user add <name>: what adding holds is what
// AddUser refuses, in its words.
func (w *wtx) userAddCard(argv []string, asking bool) (AskRun, error) {
	name := argv[2]
	if err := checkUserName(name); err != nil {
		return AskRun{}, err
	}
	if name == OriginatorAny {
		return AskRun{}, fmt.Errorf("user %s: that is a route keyword, not a user, and mail to a name reaches one party: choose another", name)
	}
	switch {
	case w.Bucket(bUsers).Get([]byte(name)) != nil:
		return AskRun{}, fmt.Errorf("%w: there is a user %s already", ErrConflict, name)
	case w.Bucket(bCrew).Get([]byte(name)) != nil:
		return AskRun{}, fmt.Errorf("user %s: that is a crew member's name, and mail to a name reaches one party: choose another", name)
	}
	return AskRun{
		Argv:   argv,
		Does:   []string{fmt.Sprintf("adds the user %s: they read mail from now on, and are told nothing from before.", name)},
		Back:   runCommandLine([]string{"user", "rm", name}),
		BackBy: "only the operator",
		Seen:   map[string]string{},
	}, nil
}

// userAddStale is never told apart: the card names nothing the yard
// holds, and a user added since is refused by the card itself.
func (w *wtx) userAddStale(seq uint64, was, now AskRun) error {
	return staleCard(seq, was, now)
}

// runUserAdd adds the user as actor through insertUser, and answers what
// the command prints at a terminal.
func runUserAdd(ctx context.Context, w *wtx, argv []string, actor string, by *approvedOn) (string, error) {
	u, err := w.insertUser(ctx, argv[2], actor, by)
	if err != nil {
		return "", err
	}
	return "user " + u.Name + " added", nil
}

// countNoun is n with the noun after it, plural as it is.
func countNoun(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// crewGrantKeys are the crew settings a run ask takes: the grants. The
// rest of a definition is set directly, and a definition whole is the
// operator's alone.
var crewGrantKeys = []string{OpenThreads, SetState, MailPeers}

// crewGrantUse is how a refusal names the crew set a run ask takes.
const crewGrantUse = "yardr crew set <name> open_threads=<sources> | set_state=<sources> | peers=<peers>"

// identitySays is the id and/or key as a card names them.
func identitySays(id, key string) string {
	switch {
	case id != "" && key != "":
		return id + " / " + key
	case id != "":
		return id
	default:
		return key
	}
}

// foldIdentityKey is the key as the source's deliveries carry it: a source
// the yard declares whose identity folds lower keeps one spelling however
// it was typed. The CLI folds the key before the store by the same rule
// (hooks.KeyFolderFor, which the store does not read); folding here too
// keeps what a run ask approves as a terminal would store it. A source the
// yard declares not, or whose declaration this yardr does not read, keeps
// the key as given.
func foldIdentityKey(tx *bolt.Tx, name, key string) string {
	if key == "" {
		return ""
	}
	src := new(Source)
	if ok, err := get(tx, bSources, name, src); err != nil || !ok {
		return key
	}
	d, err := src.Declaration()
	if err != nil || d.Identity == nil || d.Identity.Fold != source.FoldLower {
		return key
	}
	return strings.ToLower(key)
}

// parseSettingSet reads the words of settings set <key> <value>: the value
// is the words past the key joined with single spaces, so a yard's
// description rides a card unquoted. What is no setting refuses the
// command as setting itself refuses it. One word is refused apart:
// unsetting one takes an empty value, which no split line holds.
func parseSettingSet(rest []string) ([]string, error) {
	const use = "yardr settings set <key> <value>"
	if len(rest) == 1 {
		return nil, fmt.Errorf("--run takes %s: one key and one value; unsetting one takes an empty value, and is typed in a terminal", use)
	}
	if len(rest) < 2 {
		return nil, fmt.Errorf("--run takes %s: one key and one value, not %d words", use, len(rest))
	}
	if _, ok := SettingOf(rest[0]); !ok {
		return nil, fmt.Errorf("no setting %q; there is: %s", rest[0], strings.Join(SettingKeys(), ", "))
	}
	// A value word in a flag's or an assignment's shape is not taken:
	// nothing re-reads the line, so the card would show a flag where
	// the operator reads words.
	for _, word := range rest[1:] {
		for _, mark := range []string{"--", "="} {
			if strings.HasPrefix(word, mark) {
				return nil, fmt.Errorf("--run takes %s: a value word that begins with %q is not taken; type the command in a terminal", use, mark)
			}
		}
	}
	return []string{rest[0], strings.Join(rest[1:], " ")}, nil
}

// checkSettingSet is setting a setting's own check as the asker.
func checkSettingSet(ctx context.Context, w *wtx, argv []string, actor string) error {
	return w.may(ctx, actor, ActChangeSetting, Object{Kind: KindSetting, Name: argv[2], Ask: settingSetAsk(argv[2], argv[3])})
}

// settingSetCard is the card of settings set <key> <value>: the value it
// sets, against what is stored now, as it was typed and not as it is
// stored. The value is checked as setting checks it.
func (w *wtx) settingSetCard(argv []string, asking bool) (AskRun, error) {
	key, value := argv[2], argv[3]
	set, ok := SettingOf(key)
	if !ok {
		return AskRun{}, fmt.Errorf("no setting %q; there is: %s", key, strings.Join(SettingKeys(), ", "))
	}
	if value != "" {
		if err := set.Check(value); err != nil {
			return AskRun{}, err
		}
	}
	stored := w.Bucket(bSettings).Get([]byte(key))
	has, held := stored != nil, settingValue(stored)
	if asking && has && held == value {
		return AskRun{}, fmt.Errorf("setting %s is %q already, so the command would change nothing", key, value)
	}
	now := "unset"
	if has {
		now = held
	} else if set.Default != "" {
		now = "unset (default " + set.Default + ")"
	}
	back := runCommandLineWithShellWord([]string{"settings", "set", key}, shellQuote(held))
	if !has {
		back = runCommandLine([]string{"settings", "set", key, `""`})
	}
	does := []string{fmt.Sprintf("setting %s becomes %s (now: %s).", key, value, now)}
	if key == HookPublicURLSetting {
		// The address's one effect is the URL a register call sends
		// to a service, so the card names it whole: what the operator
		// approves is where hooks are registered. Trimmed as the
		// hook's URL is (hookPublicURL), so the card shows it.
		does = append(does, fmt.Sprintf("a hook whose source names a register call is registered at this address when its card is approved: the URL of hook <name> is %s/<name>.", strings.TrimRight(value, "/")))
	}
	return AskRun{
		Argv:   argv,
		Does:   does,
		Back:   back,
		BackBy: "only the operator",
		Seen:   map[string]string{"setting " + key: seen(has, string(stored))},
	}, nil
}

// settingSetStale says why the ask seq for settings set <key> <value> is
// approved no more: the setting was changed since.
func (w *wtx) settingSetStale(seq uint64, was, now AskRun) error {
	key := was.Argv[2]
	if seen := "setting " + key; was.Seen[seen] != now.Seen[seen] {
		e := lastEvent(w.Tx, "setting_changed", naming("key", key))
		return fmt.Errorf("ask %d was written when setting %s was another: it was changed since%s. %s", seq, key, since(e), nothingRan)
	}
	return staleCard(seq, was, now)
}

// runSettingSet sets the setting as actor through setSetting, and
// answers what the command prints at a terminal.
func runSettingSet(ctx context.Context, w *wtx, argv []string, actor string, by *approvedOn) (string, error) {
	key, value := argv[2], argv[3]
	if set, ok := SettingOf(key); ok && value != "" {
		if err := set.Check(value); err != nil {
			return "", err
		}
	}
	if err := w.setSetting(ctx, key, value, true, actor, by); err != nil {
		return "", err
	}
	return key + " = " + value, nil
}

// parsePeerRename reads the words of peer rename <old> <new>.
func parsePeerRename(rest []string) ([]string, error) {
	const use = "yardr peer rename <old> <new>"
	if len(rest) != 2 {
		return nil, fmt.Errorf("--run takes %s: two names, not %d words", use, len(rest))
	}
	return []string{rest[0], rest[1]}, nil
}

// checkPeerRename is renaming a peer's own check as the asker.
func checkPeerRename(ctx context.Context, w *wtx, argv []string, actor string) error {
	return w.may(ctx, actor, ActRenamePeer, Object{Kind: KindPeer, Name: argv[2], Ask: Askable})
}

// peerRenameCard is the card of peer rename <old> <new>: what follows
// the name is named as renaming moves it, in the store's own words for
// what it refuses.
func (w *wtx) peerRenameCard(argv []string, asking bool) (AskRun, error) {
	name, to := argv[2], argv[3]
	p, err := loadPeer(w.Tx, name)
	if err != nil {
		return AskRun{}, err
	}
	if err := checkName("peer", to); err != nil {
		return AskRun{}, err
	}
	switch {
	case to == name:
		return AskRun{}, fmt.Errorf("peer %s has that name already", name)
	case w.Bucket(bPeers).Get([]byte(to)) != nil:
		return AskRun{}, fmt.Errorf("peer %s is added already: two links cannot share a name. Rename or remove that one first", to)
	}
	if err := outboxOf(w.Tx, p); err != nil {
		return AskRun{}, err
	}
	var may []string
	if err := each(w.Tx, bCrew, "", func(_ []byte, r *CrewMember) error {
		if slices.Contains(r.MayMail(), name) {
			may = append(may, r.Name)
		}
		return nil
	}); err != nil {
		return AskRun{}, err
	}
	sentences := []string{fmt.Sprintf("the link with %s (%s) becomes %s: its key, its address and what it carries stay the same.", name, peerAs(p), to)}
	if p.Waiting > 0 {
		sentences = append(sentences, fmt.Sprintf("%d messages that wait for it move with it, and are delivered.", p.Waiting))
	}
	if len(may) > 0 {
		sentences = append(sentences, fmt.Sprintf("%s may mail it as before, and get mail from the yard that says so.", strings.Join(may, ", ")))
	}
	return AskRun{
		Argv:   argv,
		Does:   sentences,
		Back:   runCommandLine([]string{"peer", "rename", to, name}),
		BackBy: "only the operator",
		Seen:   map[string]string{"peer " + name: p.Key},
	}, nil
}

// peerRenameStale says why the ask seq for peer rename <old> <new> is
// approved no more: the link is another one under the same name, or what
// follows it is not what the card says.
func (w *wtx) peerRenameStale(seq uint64, was, now AskRun) error {
	if err := w.peerKeyStale(seq, was.Argv[2], was, now); err != nil {
		return err
	}
	return staleCard(seq, was, now)
}

// runPeerRename renames the peer as actor through renamePeer, and
// answers what the command prints at a terminal.
func runPeerRename(ctx context.Context, w *wtx, argv []string, actor string, by *approvedOn) (string, error) {
	name, to := argv[2], argv[3]
	var may []string
	if err := each(w.Tx, bCrew, "", func(_ []byte, r *CrewMember) error {
		if slices.Contains(r.MayMail(), name) {
			may = append(may, r.Name)
		}
		return nil
	}); err != nil {
		return "", err
	}
	p, err := w.renamePeer(ctx, name, to, actor, by)
	if err != nil {
		return "", err
	}
	out := fmt.Sprintf("peer %s is %s now: its crew are <name>@%s, and nothing is reached as <name>@%s", name, p.Name, p.Name, name)
	if p.Waiting > 0 {
		out += fmt.Sprintf("\n%d messages that waited for it still wait, and are delivered", p.Waiting)
	}
	if len(may) > 0 {
		out += fmt.Sprintf("\n%s may mail it as before, and got mail from the yard that says so", strings.Join(may, ", "))
	}
	return out, nil
}

// parseUp reads the words of yardr up: none. With a flag the command is
// the operator's to type in a terminal, where the operator chooses what
// its flags do.
func parseUp(rest []string) ([]string, error) {
	if len(rest) > 0 {
		return nil, fmt.Errorf("--run takes yardr up, and up with %s is the operator's to type in a terminal", strings.Join(rest, " "))
	}
	return nil, nil
}

// checkUp is bringing the yard up as the asker: the operator's alone,
// since it starts the yard's own processes.
func checkUp(ctx context.Context, w *wtx, argv []string, actor string) error {
	return w.may(ctx, actor, ActBringUp, Object{Kind: KindYard, Ask: Askable})
}

// upCard is the card of yardr up: what starts, and that what runs is left
// alone. The command is safe to repeat, so the card holds to nothing: an
// approval is never stale.
func (w *wtx) upCard(argv []string, asking bool) (AskRun, error) {
	return AskRun{
		Argv: argv,
		Does: []string{
			"starts what the yard needs and is not running: herdr's server, the yard's server and the daemon, with the web view and the BDP server where their addresses are set.",
			"what runs already is left alone.",
		},
		Back:   runCommandLine([]string{"down"}),
		BackBy: "",
		Seen:   map[string]string{},
	}, nil
}

// upStale is never told apart: up is safe to repeat, so a card written
// while the yard was down approves the same once it is up.
func (w *wtx) upStale(seq uint64, was, now AskRun) error {
	return staleCard(seq, was, now)
}

// runUp never runs in the approval's write: the yard's server carries an
// approved up out after it, as it carries an approved pack apply, and
// nothing of the yard is started here.
func runUp(ctx context.Context, w *wtx, argv []string, actor string, by *approvedOn) (string, error) {
	if err := w.may(ctx, actor, ActBringUp, Object{Kind: KindYard, Ask: Askable}); err != nil {
		return "", err
	}
	return CarryPending, nil
}
