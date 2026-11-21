// The depot add row of the run-ask list: a first depot for an operator
// with no terminal, cloned from a repository on approval. Adding a depot
// is the operator's (PutDepot, ActAddDepot), and this row proposes it:
// the card names the depot, the fetch and the test command it will hold,
// and the yard's server clones into DepotsDir after approval, since a
// clone is no store write (carry.go). Only a git depot is added this way:
// a directory depot names a path on this machine, which the yard cannot
// see from a card, so it stays typed in a terminal, as do removing a
// depot, re-cloning one and moving one.
//
// The row is registered here, beside its functions, and not in runList's
// literal: runask.go stands at the file-size ceiling the test gate holds
// (.yardr/test).
package store

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// depotAddUse is how a refusal names the depot add a run ask takes. The
// test command rides beside argv, in the ask's --run-test, as a depot
// test's does, since it needs the characters --run quotes nothing of.
const depotAddUse = "yardr depot add <name> --clone <url> [--kind git] [--base <branch>]"

// depotAddDescription is the help line of depot add that the card keeps
// (internal/cli holds it to the command's Short).
const depotAddDescription = "Register (or update) a depot: an existing clone, --clone a URL, or a directory"

// depotAddDefaultBase is the base a depot add names where the command
// names none: depot add's own default in a terminal.
const depotAddDefaultBase = "origin/main"

func init() {
	runList = append(runList, runCommand{
		words:       []string{"depot", "add"},
		use:         depotAddUse,
		description: depotAddDescription,
		parse:       parseDepotAdd,
		checkRun:    checkDepotAdd,
		cardRun:     (*wtx).depotAddCard,
		stale:       (*wtx).depotAddStale,
		weigh:       depotAddWeight,
		runRun:      runDepotAdd,
	})
}

// IsDepotAddCarry says whether argv is a depot add carrying out: an
// approved depot add the yard's server clones and records.
func IsDepotAddCarry(argv []string) bool {
	return len(argv) >= 2 && argv[0] == "depot" && argv[1] == "add"
}

// parseDepotAdd reads the words of depot add <name> --clone <url>
// [--kind <kind>] [--base <branch>]: the name, the fetch, the kind and
// the base, in one stored order however they were typed. The kind is
// read here so that dir is refused in the row's own words; the base
// defaults as the terminal's does.
func parseDepotAdd(rest []string) ([]string, error) {
	args, values, err := runWords(depotAddUse, "depot add", "the operator's", rest, map[string]bool{"--clone": true, "--kind": true, "--base": true})
	if err != nil {
		return nil, err
	}
	if len(args) != 1 {
		return nil, fmt.Errorf("--run takes %s: one depot, not %d words", depotAddUse, len(args))
	}
	if len(values["--clone"]) != 1 {
		return nil, fmt.Errorf("--run takes %s: one --clone <url>, not %d", depotAddUse, len(values["--clone"]))
	}
	kind := DepotGit
	if len(values["--kind"]) > 1 {
		return nil, fmt.Errorf("--run takes %s: --kind once, not %d times", depotAddUse, len(values["--kind"]))
	}
	if len(values["--kind"]) == 1 {
		kind = values["--kind"][0]
	}
	switch kind {
	case DepotGit:
	case DepotDir:
		return nil, fmt.Errorf("--run takes %s, and a directory depot names a path on this machine, which a card cannot see cloned: add it in a terminal with yardr depot add <name> <path> --kind dir", depotAddUse)
	default:
		return nil, fmt.Errorf("--run takes %s: --kind %q: give %s", depotAddUse, kind, DepotGit)
	}
	base := depotAddDefaultBase
	if len(values["--base"]) > 1 {
		return nil, fmt.Errorf("--run takes %s: --base once, not %d times", depotAddUse, len(values["--base"]))
	}
	if len(values["--base"]) == 1 {
		base = values["--base"][0]
	}
	return []string{args[0], "--clone", values["--clone"][0], "--kind", kind, "--base", base}, nil
}

// depotAddOf is the name, the fetch URL, the kind and the base of a
// parsed depot add, each read by flag name.
func depotAddOf(argv []string) (name, cloneURL, kind, base string) {
	if len(argv) > 2 {
		name = argv[2]
	}
	kind, base = DepotGit, depotAddDefaultBase
	for i := 3; i+1 < len(argv); i += 2 {
		switch argv[i] {
		case "--clone":
			cloneURL = argv[i+1]
		case "--kind":
			kind = argv[i+1]
		case "--base":
			base = argv[i+1]
		}
	}
	return name, cloneURL, kind, base
}

// DepotAddOf is depotAddOf for the yard's server, which clones what an
// approved card names: ok is false for a command that is no depot add.
func DepotAddOf(argv []string) (name, cloneURL, kind, base string, ok bool) {
	if !IsDepotAddCarry(argv) || len(argv) < 4 {
		return "", "", "", "", false
	}
	name, cloneURL, kind, base = depotAddOf(argv)
	return name, cloneURL, kind, base, cloneURL != ""
}

// checkDepotAdd is adding a depot as the asker: the check PutDepot
// refuses with, which a run ask may propose.
func checkDepotAdd(ctx context.Context, w *wtx, run AskRun, actor string) error {
	name, _, _, _ := depotAddOf(run.Argv)
	return w.may(ctx, actor, ActAddDepot, Object{Kind: KindDepot, Name: name, Ask: Askable})
}

// depotAddTest is the test command a depot add ask carries beside argv,
// whole or none: past what a card shows whole, and one with a line break
// or a control character in it, are refused as a depot test's are. None
// names no command: the depot is added now and its test named later.
func depotAddTest(name string, run AskRun) (string, error) {
	proposed := run.Test
	if proposed == "" {
		return "", nil
	}
	switch {
	case strings.TrimSpace(proposed) == "":
		return "", fmt.Errorf("depot add %s carries no test command: the command goes beside --run, in --run-test <command> (T: on stdin), and the depot is added with none where it names none", name)
	case len(proposed) > MaxRunDepotTest:
		return "", fmt.Errorf("depot add %s: the test command it carries is %d bytes: a card shows it whole and holds at most %d, so this is the operator's to type in a terminal", name, len(proposed), MaxRunDepotTest)
	case claimPlain(proposed) != proposed:
		return "", fmt.Errorf("depot add %s: the test command it carries holds a line break or a control character, which no card shows: keep it to one line, or type it in a terminal", name)
	}
	return proposed, nil
}

// validCloneURL says whether raw names a fetch git can clone: a URL with
// a scheme the yard clones by, a scp-like host:path, or a path on this
// machine. A URL with a scheme names its host, and a file URL its path.
func validCloneURL(raw string) bool {
	s := strings.TrimSpace(raw)
	if s == "" || strings.HasPrefix(s, "-") || len(s) > 2048 || cloneURLHasInlineCredential(s) || strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return false
	}
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || u.Scheme == "" {
			return false
		}
		switch strings.ToLower(u.Scheme) {
		case "file":
			return u.Path != ""
		case "https", "http", "git", "ssh":
			return u.Hostname() != "" && u.Path != "" && u.Path != "/"
		}
		return false
	}
	return true
}

// cloneURLHasInlineCredential says whether a clone URL puts credentials
// or hidden query text in the fetch itself: the card stores and the server
// passes that URL as an argument, so HTTPS answers use the source helper
// and SSH answers use the yard's configured access instead.
func cloneURLHasInlineCredential(raw string) bool {
	s := strings.TrimSpace(raw)
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return false
		}
		return u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.HasSuffix(s, "#")
	}
	// Git's scp-like syntax accepts user@host:path, but not a password in
	// the user part. A colon before the @ in that authority would otherwise
	// keep the password in the card and the git process arguments.
	at := strings.IndexByte(s, '@')
	colon := strings.IndexByte(s, ':')
	slash := strings.IndexAny(s, "/\\")
	return at > 0 && colon >= 0 && colon < at && (slash < 0 || at < slash)
}

// isHTTPSFetch says whether raw fetches over https: the one scheme a
// push source gives git a credential for, and so the one a card holds
// to a source and its token.
func isHTTPSFetch(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && strings.EqualFold(u.Scheme, "https")
}

// cloneHost is the host a fetch URL names, "" for one that names none.
func cloneHost(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// isLocalFetch says whether raw fetches from this machine: a file URL
// or a path with no scheme.
func isLocalFetch(raw string) bool {
	if !strings.Contains(raw, "://") {
		return true
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && strings.EqualFold(u.Scheme, "file")
}

// cloneDisplay is a fetch URL as a card names it: scheme, host and path,
// with any userinfo the URL carries left out, so no credential a URL was
// written with stands in a card, an event or a log. A fetch with no
// scheme (a scp-like address, a path) stands as it was typed.
func cloneDisplay(raw string) string {
	if isLocalFetch(raw) {
		return raw
	}
	if strings.Contains(raw, "://") {
		return originDisplay(raw)
	}
	return raw
}

// depotClonePath is where the clone of the depot name goes: DepotsDir of
// the yard's home, as depot add --clone names it in a terminal.
func (w *wtx) depotClonePath(name string) string {
	home := w.home
	if home == "" && w.store != nil {
		home = w.store.home
	}
	return filepath.Join(home, DepotsDir, name)
}

// depotPushFor reads the push source whose host the fetch URL is on and
// whether its token stands, as a depot-push card reads them: found is
// false for a fetch no source gives git a credential for, which clones
// with none.
func (w *wtx) depotPushFor(cloneURL string) (ps DepotPushSource, found, token bool, err error) {
	cands, err := w.pushSources()
	if err != nil {
		return DepotPushSource{}, false, false, err
	}
	ps, found = pushSourceIn(cands, cloneURL)
	if !found {
		return DepotPushSource{}, false, false, nil
	}
	home := w.home
	if home == "" && w.store != nil {
		home = w.store.home
	}
	return ps, true, pushTokenStanding(home, ps), nil
}

// depotAddCard is the card of depot add <name> --clone <url>: the depot,
// its kind, its fetch, the place its clone goes, its base and its test
// command whole or none, and what credential the clone answers with.
// Asking refuses a name that is no depot's, a depot or a clone place the
// yard has already, a fetch or a base git takes none of, a test command
// no card shows whole, and an https fetch no push source answers for or
// whose token does not stand: an https fetch is a private one until a
// source of the yard says otherwise, and a file, git, ssh or local fetch
// clones with no source. An approval reads the card again and stale says
// the push source or its token moved since (depotAddStale); a depot or a
// clone place taken since refuses it whole, here.
func (w *wtx) depotAddCard(run AskRun, asking bool) (AskRun, error) {
	name, cloneURL, kind, base := depotAddOf(run.Argv)
	if err := checkName("depot name", name); err != nil {
		return AskRun{}, err
	}
	if kind != DepotGit {
		return AskRun{}, fmt.Errorf("depot %s: a %s depot is added in a terminal with its path: a card clones a git repository only", name, kind)
	}
	if cloneURLHasInlineCredential(cloneURL) {
		return AskRun{}, fmt.Errorf("depot %s: a clone URL must not carry credentials or hidden query text; use a push source for https, or the yard's configured SSH access", name)
	}
	if !validCloneURL(cloneURL) {
		return AskRun{}, fmt.Errorf("depot %s: the fetch URL names no fetch git can clone: give an https, git, ssh or file URL, a host:path or a path on this machine", name)
	}
	if !validBase(base) {
		return AskRun{}, fmt.Errorf("depot %s: base %q is not a branch name", name, base)
	}
	test, err := depotAddTest(name, run)
	if err != nil {
		return AskRun{}, err
	}
	if w.Bucket(bDepots).Get([]byte(name)) != nil {
		return AskRun{}, fmt.Errorf("%w: there is a depot %s already, and a card never re-clones or moves one: add the new repository under another name", ErrConflict, name)
	}
	path := w.depotClonePath(name)
	if _, err := os.Stat(path); err == nil {
		return AskRun{}, fmt.Errorf("depot %s: %s already stands where its clone would go: move it away, or add the depot under another name", name, path)
	} else if err != nil && !os.IsNotExist(err) {
		return AskRun{}, err
	}
	ps, found, token, err := w.depotPushFor(cloneURL)
	if err != nil {
		return AskRun{}, err
	}
	fingerprint := ""
	if isHTTPSFetch(cloneURL) && !found {
		return AskRun{}, fmt.Errorf("depot %s: no source gives git a credential for %s, so a clone of %s has nothing to answer with: add a source for that host first (yardr source add), and give its credential on a secret card", name, cloneHost(cloneURL), cloneDisplay(cloneURL))
	}
	if found {
		if !token {
			return AskRun{}, fmt.Errorf("depot %s: no %s stands for the source %s, so a clone of %s would have no credential to answer with: give it on a secret card for the source %s first", name, ps.TokenName, ps.Name, cloneDisplay(cloneURL), ps.Name)
		}
		fingerprint = DepotPushSourceFingerprint(ps)
	}
	display := cloneDisplay(cloneURL)
	does := []string{fmt.Sprintf("adds the depot %s (git): it clones %s into %s, and bead branches start from %s.", name, display, path, base)}
	if test != "" {
		does = append(does, fmt.Sprintf("its test command will be %s, whole.", test))
	} else {
		does = append(does, "it sets no test command: the depot is added now, and its tests are named later, on a depot test card.")
	}
	switch {
	case found:
		does = append(does, fmt.Sprintf("the clone answers with the credential of the source %s for https://%s: the carrying out wires the pack's helper into the depot's own git config alone, and no token stands in the card, the config or a log.", ps.Name, ps.Host))
	case isLocalFetch(cloneURL):
		does = append(does, "the clone is from this machine, and needs no credential.")
	default:
		does = append(does, fmt.Sprintf("no source gives git a credential for %s, so the clone goes with none: a repository that asks for one fails, and the card is filed again.", display))
	}
	return AskRun{
		Argv:   run.Argv,
		Does:   does,
		Back:   runCommandLine([]string{"depot", "rm", name}),
		BackBy: "only the operator",
		Seen:   map[string]string{"push source": fingerprint, "push token": seen(token)},
		Test:   test,
	}, nil
}

// depotAddStale says why the ask seq for depot add is approved no more:
// the push source its fetch answers with, or its token, stands otherwise
// than the card was written from. A depot or a clone place taken since
// refuses the card itself, as depotAddCard writes it.
func (w *wtx) depotAddStale(seq uint64, was, now AskRun) error {
	name, cloneURL, _, _ := depotAddOf(was.Argv)
	if was.Seen["push source"] != now.Seen["push source"] {
		return fmt.Errorf("ask %d was written when the push source for %s stood otherwise: the source that gives git a credential for its host was changed since. %s", seq, cloneDisplay(cloneURL), nothingRan)
	}
	if was.Seen["push token"] != now.Seen["push token"] {
		return fmt.Errorf("ask %d was written while the token for the clone of depot %s stood otherwise: it was changed since. %s", seq, name, nothingRan)
	}
	return staleCard(seq, was, now)
}

// depotAddWeight weighs a depot add: the fetch brings code from outside
// the yard always, and a test command the yard will run beside it.
func depotAddWeight(_ *wtx, run AskRun) ([]AskWeight, error) {
	name, cloneURL, _, _ := depotAddOf(run.Argv)
	out := []AskWeight{weighs(WeightOutside, fmt.Sprintf("the yard fetches %s as the depot %s", cloneDisplay(cloneURL), name))}
	if run.Test != "" {
		out = append(out, weighs(WeightCode, fmt.Sprintf("the test command of the depot %s: a bead's prime prints it, and a script group's command gets it as YARDR_TEST", name)))
	}
	return out, nil
}

// runDepotAdd never clones in the approval's write: it holds the run to
// the add's own check and answers CarryPending, and the yard's server
// clones and records the depot after it, as it carries an approved up.
func runDepotAdd(ctx context.Context, w *wtx, run AskRun, actor string, by *approvedOn) (string, error) {
	if err := checkDepotAdd(ctx, w, run, actor); err != nil {
		return "", err
	}
	name, _, _, _ := depotAddOf(run.Argv)
	if _, err := depotAddTest(name, run); err != nil {
		return "", err
	}
	return CarryPending, nil
}
