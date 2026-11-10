package assembly

// push-fix the way an exec group runs it for a pr bead in push: merge-pr's
// yard (the contributors pack's pr flow, the group's env_file, stand-ins for
// gh and github-mint), a repository where the pull request's commits are made,
// and a bare repository for the fork the push lands on, which a stand-in git
// reaches by rewriting the https URL, as GitHub would take it: fast-forward
// only, refused in git's words otherwise.
//
// Each case has a yard of its own (a copy of the template yard, which is
// cheaper than a run of the script), so the cases run beside one another. A
// bead in push carries the maintainer's pick (fix-ok, fix_head), the heads a
// maintainer accepted and its reviewer read (accepted_head, reviewed_head),
// and the fix its builder committed (fix_commit), as the pr flow's bead does.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/config"
	"yardr.dev/yardr/internal/rpc"
	"yardr.dev/yardr/internal/scripttest"
	"yardr.dev/yardr/internal/store"
)

// fakePushGit is git as far as push-fix uses it. Every call but a push to an
// https URL runs as the real git; a push to one goes to the test's fork
// instead, by pointing git's global config at a file that rewrites the URL,
// as GitHub would take it. Every call's argv is logged in git-calls, which
// carries no secret: the token travels in git's environment alone, so a push
// records only whether it carried the Authorization header, never its value.
const fakePushGit = `#!/bin/sh
d=$FAKE_GH
printf '%s\n' "$*" >>"$d/git-calls"
push=0; url=
for a in "$@"; do
  if [ "$a" = push ]; then push=1; fi
  case $a in https://github.com/*) url=$a ;; esac
done
if [ "$push" = 1 ] && [ -n "$url" ]; then
  if [ -n "${GIT_CONFIG_VALUE_0:-}" ]; then printf 'auth-carried\n' >>"$d/git-calls"; else printf 'NO-AUTH\n' >>"$d/git-calls"; fi
  printf '[url "%s/fork.git"]\n\tinsteadOf = %s\n' "$d" "$url" >"$d/fake.gitconfig"
  export GIT_CONFIG_GLOBAL="$d/fake.gitconfig"
fi
exec {git} "$@"
`

// fakePushDate speeds up the script's configured wait without changing the
// timeout it reports: the first epoch read starts the clock and the next
// jumps just past five minutes.
const fakePushDate = `#!/bin/sh
if [ "$1" = +%s ]; then
  n=$(cat "$FAKE_GH/date-count" 2>/dev/null || echo 0)
  n=$((n + 1))
  printf '%s\n' "$n" >"$FAKE_GH/date-count"
  if [ "$n" -eq 1 ]; then echo 1000; else echo 1301; fi
else
  exec {date} "$@"
fi
`

// The yard's App as the stand-in GitHub names it: the bot's login, the id of
// its user, and the address a commit of the App carries, which is what push-fix
// reads from GitHub as the token and holds each commit of a fix to.
const (
	appLogin = "yard-app[bot]"
	appID    = "12345678"
	appIdent = appLogin + " <" + appID + "+" + appLogin + "@users.noreply.github.com>"
	// someone is whoever else a commit is by.
	someone = "Jo Doe <jo@example.com>"
)

// fakePushGh is gh in front of the merge stand-in, for the two things push-fix
// asks that merge-pr never does, who its token is: GraphQL's viewer, which is the
// App's bot (the login in viewer-login, yard-app[bot] unless written, or the
// error in viewer-fail), and the users API's answer for that bot (its id in
// bot-id, 12345678 unless written, its type in bot-type, Bot unless written, or
// the error in user-fail). A call whose GH_TOKEN is not the mint's is refused as
// GitHub refuses a bad credential, and every call is logged as the merge
// stand-in logs its own. Any other call is the merge stand-in's.
const fakePushGh = `#!/bin/sh
d=$FAKE_GH
case "$1 $2" in
"api graphql" | "api users/"*)
  echo "$*" >>"$d/calls"
  if [ "$GH_TOKEN" != ` + mintToken + ` ]; then echo "HTTP 401: Bad credentials" >&2; exit 1; fi
  jq= prev=
  for a in "$@"; do
    [ "$prev" = --jq ] && jq=$a
    prev=$a
  done
  login=$(cat "$d/viewer-login" 2>/dev/null || echo ` + "'" + appLogin + "'" + `)
  case "$2" in
  graphql)
    if [ -e "$d/viewer-fail" ]; then cat "$d/viewer-fail" >&2; exit 1; fi
    json=$(printf '{"data":{"viewer":{"login":"%s"}}}' "$login") ;;
  *)
    if [ -e "$d/user-fail" ]; then cat "$d/user-fail" >&2; exit 1; fi
    want=$(printf 'users/%s' "$login" | sed 's/\[/%5B/g; s/\]/%5D/g')
    if [ "$2" != "$want" ]; then echo "HTTP 404: Not Found" >&2; exit 1; fi
    json=$(printf '{"login":"%s","id":%s,"type":"%s"}' "$login" "$(cat "$d/bot-id" 2>/dev/null || echo ` + appID + `)" "$(cat "$d/bot-type" 2>/dev/null || echo Bot)") ;;
  esac
  if [ -n "$jq" ]; then printf '%s' "$json" | jq -r "$jq"; else printf '%s\n' "$json"; fi ;;
*) exec {gh} "$@" ;;
esac
`

// pushEndsAt is the yardr the script is given: the real one, that first notes
// in $FAKE_GH/ended where the script is, and that it made no auxiliary
// worktree, whenever the script ends its session by reporting or holding.
// The report watch separately proves the script's temporary repository is
// gone before this call. It also plays the yard hearing its own push, which the
// script waits for: the bead's head is made the head of the fork's branch
// (YARDR_TEST_BRANCH), the commit the script pushed, which is not one the test
// has before the run, since the script makes it. YARDR_TEST_ARRIVAL says when:
// now, at each look at the bead, so the push is heard at once; late, from the
// third look (the wait's second poll), so that the wait sees the old head, sleeps,
// and then the new one, with no sleep of the test's; never, the default, when the
// yard hears nothing.
const pushEndsAt = `#!/bin/sh
if [ "$1" = bead ] && [ "$2" = show ]; then
  n=$(cat "$FAKE_GH/shows" 2>/dev/null || echo 0)
  n=$((n + 1))
  echo "$n" >"$FAKE_GH/shows"
  hear=
  case "${YARDR_TEST_ARRIVAL:-never}" in
  now) hear=1 ;;
  late) [ "$n" -lt 3 ] || hear=1 ;;
  esac
  if [ -n "$hear" ]; then
    head=$(git -C "$FAKE_GH/fork.git" rev-parse "refs/heads/$YARDR_TEST_BRANCH" 2>/dev/null)
    [ -z "$head" ] || {yardr} bead update "$3" --meta pr_head="$head" >/dev/null 2>&1
  fi
fi
case "$1 $2" in
"bead done" | "bead hold")
  printf '%s: in %s, no auxiliary worktree\n' "$1 $2" "$(pwd)" >>"$FAKE_GH/ended"
  ;;
esac
exec {yardr} "$@"
`

// fixYard is merge-pr's yard with what push-fix asks of a repository: a
// place where the pull request's commits are made, and the fork the push
// lands on.
type fixYard struct {
	*yard
	gh   string // the stand-ins' state directory
	work string // where the pull request's commits are made
	fork string // the fork, where the push lands
}

func newFixYard(t *testing.T) *fixYard {
	t.Helper()
	y, gh := mergeYard(t)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	scripttest.Write(t, filepath.Join(gh, "bin", "git"), strings.ReplaceAll(fakePushGit, "{git}", realGit))
	// The merge stand-in keeps its place behind the one that answers who the
	// token is: merge-pr's cases, which share it, never ask that.
	mergeGh := filepath.Join(gh, "bin", "gh-merge")
	scripttest.Write(t, mergeGh, fakeMergeGh)
	scripttest.Write(t, filepath.Join(gh, "bin", "gh"), strings.ReplaceAll(fakePushGh, "{gh}", mergeGh))
	c := &fixYard{yard: y, gh: gh, work: filepath.Join(t.TempDir(), "work"), fork: filepath.Join(gh, "fork.git")}
	wrapper := filepath.Join(t.TempDir(), "yardr")
	scripttest.Write(t, wrapper, strings.ReplaceAll(pushEndsAt, "{yardr}", shellQuote(yardrBin)))
	y.bin = wrapper
	y.git(".", "init", "-q", "--bare", "-b", "main", c.fork)
	y.git(".", "init", "-q", "-b", "main", c.work)
	y.commit(c.work, "track.txt", "base\n")
	// A hook that fails, in the hooks of the depot's own repository: the
	// push runs with hooks off, so it never fires, and a run that let them
	// on would fail here.
	hook := "#!/bin/sh\necho pre-push ran >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(y.repo, ".git", "hooks", "pre-push"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	return c
}

// head makes a commit on main in the work repository and returns it: the pull
// request's head as the tests start from it.
func (c *fixYard) head(files map[string]string) string {
	c.t.Helper()
	c.git(c.work, "checkout", "-q", "main")
	return c.commitOn("HEAD", files)
}

// commitOn makes a commit of files on base in the work repository and returns
// it, detached, leaving the branch where it was: by the tests' own name, as a
// pull request's commits are.
func (c *fixYard) commitOn(base string, files map[string]string) string {
	return c.commitBy("", "", base, files)
}

// fixOn is commitOn as a builder is told to make a fix: as the App.
func (c *fixYard) fixOn(base string, files map[string]string) string {
	return c.commitBy(appIdent, appIdent, base, files)
}

// commitBy is commitOn with the commit's author and committer given as
// "name <address>", the tests' own name where one is empty.
func (c *fixYard) commitBy(author, committer, base string, files map[string]string) string {
	c.t.Helper()
	c.git(c.work, "checkout", "-q", "--detach", base)
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(c.work, name), []byte(text), 0o644); err != nil {
			c.t.Fatal(err)
		}
	}
	c.git(c.work, "add", "-A")
	// Detached, so the commit's message names what it is on.
	c.gitAs(author, committer, "commit", "-q", "--allow-empty", "-m", "on "+base)
	sha := c.git(c.work, "rev-parse", "HEAD")
	c.git(c.work, "checkout", "-q", "main")
	return sha
}

// mergeBy makes a merge commit of other into base, base its first parent, in the
// work repository, by who as author and committer, and returns it, detached. It
// is a merge even where base could be fast-forwarded to other.
func (c *fixYard) mergeBy(who, base, other string) string {
	c.t.Helper()
	c.git(c.work, "checkout", "-q", "--detach", base)
	c.gitAs(who, who, "merge", "-q", "--no-ff", "-m", "merge "+other[:7], other)
	sha := c.git(c.work, "rev-parse", "HEAD")
	c.git(c.work, "checkout", "-q", "main")
	return sha
}

// gitAs runs git in the work repository as the author and committer
// "name <address>" given, the tests' own where one is empty.
func (c *fixYard) gitAs(author, committer string, args ...string) string {
	c.t.Helper()
	// Dates well in the past, apart, and in zones of their own: a commit made again
	// that took the time it was made at, or the zone of the machine, would show.
	env := append(slices.Clone(c.env), "GIT_AUTHOR_DATE=1700000000 +0100", "GIT_COMMITTER_DATE=1700000123 -0500")
	for _, w := range []struct{ who, kind string }{{author, "AUTHOR"}, {committer, "COMMITTER"}} {
		if w.who == "" {
			continue
		}
		name, rest, _ := strings.Cut(w.who, " <")
		env = append(env, "GIT_"+w.kind+"_NAME="+name, "GIT_"+w.kind+"_EMAIL="+strings.TrimSuffix(rest, ">"))
	}
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env = c.work, env
	out, err := cmd.CombinedOutput()
	if err != nil {
		c.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// commitObject is a commit as git stores it: its headers in order, each with
// the continuation lines of a signature joined to it, and its message.
type commitObject struct {
	tree      string
	parents   []string
	author    string // "name <address> <seconds> <zone>", as the header has it
	committer string
	extra     []string // the names of the headers besides those four
	message   string
}

// commitIn reads commit sha of repo as its stored bytes, not as git shows it.
func (c *fixYard) commitIn(repo, sha string) commitObject {
	c.t.Helper()
	cmd := exec.Command("git", "-C", repo, "cat-file", "commit", sha)
	cmd.Env = c.env
	raw, err := cmd.Output()
	if err != nil {
		c.t.Fatalf("git cat-file commit %s in %s: %v", sha, repo, err)
	}
	head, msg, _ := strings.Cut(string(raw), "\n\n")
	o := commitObject{message: msg}
	var name, value string
	flush := func() {
		switch name {
		case "":
		case "tree":
			o.tree = value
		case "parent":
			o.parents = append(o.parents, value)
		case "author":
			o.author = value
		case "committer":
			o.committer = value
		default:
			o.extra = append(o.extra, name)
		}
	}
	for _, line := range strings.Split(head, "\n") {
		if strings.HasPrefix(line, " ") {
			value += "\n" + line
			continue
		}
		flush()
		name, value, _ = strings.Cut(line, " ")
	}
	flush()
	return o
}

// ident is the "name <address>" of a commit header, without its date, and when
// is the "<seconds> <zone>" it ends with.
func ident(header string) (who, when string) {
	i := strings.LastIndex(header, "> ")
	if i < 0 {
		return header, ""
	}
	return header[:i+1], header[i+2:]
}

// dateOf is the "<seconds> <zone>" a commit header ends with.
func dateOf(header string) string {
	_, when := ident(header)
	return when
}

// asTheApp asserts that the commits from pick to tip, by first parents, in the
// fork are the commits of fix, in the work repository, made again as the App:
// one for one, each with the App as author and committer, name and address, and
// the builder's tree, message and dates; its first parent the commit before it,
// and every other parent as the builder had it; and no header past the parents,
// the author and the committer, a signature among them. It returns the commits
// of fix and of tip, oldest first, for what a row asserts beyond that.
func (c *fixYard) asTheApp(what, pick, fix, tip string) (was, now []string) {
	c.t.Helper()
	chain := func(repo, top string) []string {
		return strings.Fields(c.git(repo, "rev-list", "--first-parent", "--reverse", pick+".."+top))
	}
	was, now = chain(c.work, fix), chain(c.fork, tip)
	if len(was) != len(now) || len(now) == 0 {
		c.t.Fatalf("%s: the fix has %d commits on its first parents and what was pushed has %d", what, len(was), len(now))
	}
	prev := pick
	for i := range was {
		b, n := c.commitIn(c.work, was[i]), c.commitIn(c.fork, now[i])
		for kind, got := range map[string]string{"author": n.author, "committer": n.committer} {
			if who, _ := ident(got); who != appIdent {
				c.t.Errorf("%s: commit %d of what was pushed has %s %q, want the App %q", what, i+1, kind, who, appIdent)
			}
		}
		for kind, dates := range map[string][2]string{"author": {b.author, n.author}, "committer": {b.committer, n.committer}} {
			if _, built := ident(dates[0]); dateOf(dates[1]) != built {
				c.t.Errorf("%s: commit %d has %s %q, the builder's was %q: the dates are kept", what, i+1, kind, dates[1], dates[0])
			}
		}
		if n.tree != b.tree || n.message != b.message {
			c.t.Errorf("%s: commit %d has tree %s and message %q, the builder's has %s and %q", what, i+1, n.tree, n.message, b.tree, b.message)
		}
		if len(n.parents) == 0 || n.parents[0] != prev || !slices.Equal(n.parents[1:], b.parents[1:]) {
			c.t.Errorf("%s: commit %d has parents %v, want %s first and the builder's others %v", what, i+1, n.parents, prev, b.parents[1:])
		}
		if len(n.extra) != 0 {
			c.t.Errorf("%s: commit %d carries headers %v past the parents, the author and the committer", what, i+1, n.extra)
		}
		prev = now[i]
	}
	return was, now
}

// signedOn makes a commit on base that carries a signature, as git writes one: a
// header of its own, the lines after its first beginning with a space. The
// signature verifies against nothing, as it would not once the commit's headers
// change. It returns the commit, in the work repository.
func (c *fixYard) signedOn(base string, files map[string]string) string {
	c.t.Helper()
	plain := c.commitBy("", "", base, files)
	o := c.commitIn(c.work, plain)
	raw := "tree " + o.tree + "\nparent " + base + "\nauthor " + o.author + "\ncommitter " + o.committer +
		"\ngpgsig -----BEGIN PGP SIGNATURE-----\n \n iQEzBAABCAAdFiEE0000000000000000000000000000000000AAAAAAA\n -----END PGP SIGNATURE-----\n\n" + o.message
	return c.craft(raw)
}

// craft stores raw as a commit object of the work repository, as given, and
// returns it: a commit as git's own commands would not make it.
func (c *fixYard) craft(raw string) string {
	c.t.Helper()
	cmd := exec.Command("git", "-C", c.work, "hash-object", "-t", "commit", "-w", "--stdin")
	cmd.Env, cmd.Stdin = c.env, strings.NewReader(raw)
	out, err := cmd.Output()
	if err != nil {
		c.t.Fatalf("git hash-object: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// depotFetch brings commit sha, with its history, into the depot, as the
// builder's commit in its worktree is in the depot's objects.
func (c *fixYard) depotFetch(sha string) {
	c.t.Helper()
	c.git(c.repo, "fetch", "-q", c.work, sha)
}

// forkTo puts the fork's branch on sha, as the pull request's branch stands.
func (c *fixYard) forkTo(branch, sha string) {
	c.t.Helper()
	c.git(c.work, "push", "-q", c.fork, sha+":refs/heads/"+branch)
}

// forkHead is the commit the fork's branch is on.
func (c *fixYard) forkHead(branch string) string {
	c.t.Helper()
	return c.git(c.fork, "rev-parse", "refs/heads/"+branch)
}

// pushBead adds pr bead id in stage, tracking the pull request at url (none
// when it is empty) and carrying labels and metadata as given.
func (c *fixYard) pushBead(id, url, stage string, labels []string, meta map[string]any) {
	c.t.Helper()
	nb := store.NewBead{Title: "fix", ID: id, Depot: "r", Type: "pr", Stage: stage, Priority: store.DefaultPriority, Labels: labels, Metadata: meta}
	if url != "" {
		nb.Links = []store.NewLink{{Kind: store.LinkTracks, Source: "github", URL: url}}
	}
	if _, err := rpc.NewClient((&config.Config{Home: c.home}).Socket()).CreateBead(context.Background(), nb, "test"); err != nil {
		c.t.Fatal(err)
	}
}

// roundToPush brings pr bead id to push the way the pr flow does for a pull
// request a maintainer let in and picked to fix: made in triage, accepted
// into review with accepted_head, read by its reviewer with reviewed_head,
// sent back changes to contributor, labelled fix-ok with fix_head and moved
// to fix by hand, and reported done by its builder with fix_commit. Empty
// records none, as a round that skipped it.
func (c *fixYard) roundToPush(id, url, accepted, reviewed, picked, fix string) {
	c.t.Helper()
	ctx, st := context.Background(), rpc.NewClient((&config.Config{Home: c.home}).Socket())
	c.prBeadIn(id, url, "triage", false)
	set := func(meta map[string]any) {
		c.t.Helper()
		if _, err := st.UpdateBead(ctx, id, store.BeadPatch{Metadata: meta}, store.Expect{}, "test"); err != nil {
			c.t.Fatal(err)
		}
	}
	done := func(outcome, stage string) {
		c.t.Helper()
		if _, err := st.Done(ctx, id, outcome, "", store.Expect{Stage: stage}, "test"); err != nil {
			c.t.Fatal(err)
		}
	}
	if accepted != "" {
		set(map[string]any{"accepted_head": accepted})
	}
	done("accepted", "triage")
	if reviewed != "" {
		set(map[string]any{"reviewed_head": reviewed})
	}
	done("changes", "review")
	done("changes", "review-post")
	if picked != "" {
		if _, err := st.UpdateBead(ctx, id, store.BeadPatch{AddLabels: []string{"fix-ok"}, Metadata: map[string]any{"fix_head": picked}}, store.Expect{}, "test"); err != nil {
			c.t.Fatal(err)
		}
	}
	if _, err := st.Advance(ctx, id, "fix", store.Expect{}, "test"); err != nil {
		c.t.Fatal(err)
	}
	if fix != "" {
		set(map[string]any{"fix_commit": fix})
	}
	done("done", "fix")
	if b := c.beadOf(id); b.Stage != "push" {
		c.t.Fatalf("the round ends in %s, want push:\n%s", b.Stage, c.notesOf(id))
	}
}

// push runs push-fix on bead id once: with the stand-in as o and files make
// it. The fork's branch and head repository are the stand-in's, so a case
// that pushes names them in files.
func (c *fixYard) push(id string, o mergeOpts, files map[string]string) {
	c.t.Helper()
	for _, name := range []string{"git-calls", "ended", "shows"} {
		if err := os.Remove(filepath.Join(c.gh, name)); err != nil && !os.IsNotExist(err) {
			c.t.Fatal(err)
		}
	}
	standIn(c.t, c.gh, o, files)
	c.exec("push-fix", "push-fix", id)
}

// has asserts that bead id ended the run for case what in stage, held or not
// as hold says, still open, having tried no move the flow refuses (a script
// that reports only what the stage offers leaves none); and returns it.
func (c *fixYard) has(id, what, stage string, hold bool) store.Bead {
	c.t.Helper()
	b := c.beadOf(id)
	if b.Stage != stage || b.Hold != hold || b.Status == "closed" {
		c.t.Fatalf("%s: %s %s/%s hold=%v, want open in %s with hold=%v:\n%s", what, id, b.Stage, b.Status, b.Hold, stage, hold, c.notesOf(id))
	}
	if got := c.refusedMoves(id); len(got) != 0 {
		c.t.Errorf("%s: push-fix tried moves the flow refuses: %v", what, got)
	}
	return b
}

// noted asserts that the notes of bead id for case what have every one of wants.
func (c *fixYard) noted(id, what string, wants ...string) {
	c.t.Helper()
	notes := c.notesOf(id)
	for _, want := range wants {
		if !strings.Contains(notes, want) {
			c.t.Errorf("%s: no note with %q:\n%s", what, want, notes)
		}
	}
}

// clean asserts that the script ended outside its temporary directory and
// left no auxiliary worktree. The report watch checks that the temporary bare
// repository is gone before the script reports.
func (c *fixYard) clean(id, what string) {
	c.t.Helper()
	if ended := strings.TrimSpace(readFile(c.t, filepath.Join(c.gh, "ended"))); !strings.HasSuffix(ended, ": in /, no auxiliary worktree") {
		c.t.Errorf("%s: the script ended its session %q, want it out of its temporary directory with no auxiliary worktree", what, ended)
	}
	c.noAuxiliaryWorktree(id, what)
}

func (c *fixYard) noAuxiliaryWorktree(id, what string) {
	c.t.Helper()
	wt := filepath.Join(c.home, "worktrees", "r", id+"-push")
	if _, err := os.Stat(wt); err == nil {
		c.t.Errorf("%s: an auxiliary worktree remains at %s", what, wt)
	}
	if list := c.git(c.repo, "worktree", "list", "--porcelain"); strings.Contains(list, id+"-push") {
		c.t.Errorf("%s: the depot still lists an auxiliary worktree for %s:\n%s", what, id, list)
	}
}

// gitCalls is the stand-in git's argv, one call per line, with auth-carried
// where a push carried the Authorization header.
func gitCalls(t *testing.T, state string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(state, "git-calls"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// short7 is the first seven digits of a commit, as the script names one, empty
// for none.
func short7(sha string) string {
	if len(sha) < 7 {
		return sha
	}
	return sha[:7]
}

// asksOnly asserts that gh was asked, besides to read the pull request, only who
// the App is, and that many times each of the token's login and the id of that
// bot's user: once each in a run that reached the check of a fix's commits, and
// not at all in one that did not.
func (c *fixYard) asksOnly(what string, login, id int) {
	c.t.Helper()
	for _, call := range ghCalls(c.t, c.gh) {
		if call != "" && !strings.HasPrefix(call, "pr view ") && !strings.HasPrefix(call, "api graphql ") && !strings.HasPrefix(call, "api users/") {
			c.t.Errorf("%s: gh was asked more than to read the pull request and who the App is: %q", what, call)
		}
	}
	if n := countCalls(c.t, c.gh, "api graphql "); n != login {
		c.t.Errorf("%s: gh was asked for the token's login %d times, want %d:\n%v", what, n, login, ghCalls(c.t, c.gh))
	}
	if n := countCalls(c.t, c.gh, "api users/"); n != id {
		c.t.Errorf("%s: gh was asked for the id of the bot's user %d times, want %d:\n%v", what, n, id, ghCalls(c.t, c.gh))
	}
}

// push-fix pushes the builder's recorded fix commit, made again as the yard's
// App, onto the pull request's branch, fast-forward only, records that commit as
// the bead's fix_commit once the push is out, and reports done once the bead's
// head is the pushed commit: the bead goes back to triage for a fresh accept of
// the new head. The remote and the ref are the live pull request's, never local
// config's; the token is minted for the repository alone, narrowed to Contents
// write and Pull requests read, and reaches git as an Authorization header in
// git's environment alone; hooks are off; no git runs in the builder's worktree.
// A fix made under the builder's own name goes out as the App with the builder's
// tree, message and dates, commit for commit; one that merges the base in keeps
// the base's commits, which are second parents and not the fix's; a lower commit
// that is the App's already is kept, and a fix that is the App's already goes out
// as it is; a signature is not carried over. Where the head never arrives the run
// holds and names the wait, with the push on the branch and recorded; where the
// push went out and an earlier run ended before it reported, this one waits for
// the head and pushes nothing again, and where it ended before it recorded the
// commit, this one makes the same commit again and records it.
func TestPushFixPushesTheFix(t *testing.T) {
	t.Parallel() // as its subtests are: see TestMain
	for _, tc := range []struct {
		what       string
		triaged    bool   // the bead came the pr flow's way, through triage; else made in push directly
		prhead     string // when the yard hears its own push: at once unless written; late, from the third look at the bead; never; F, the bead's head is the fix already
		live       string // what GitHub says the head is, the picked head unless written F
		fork       string // where the fork's branch is, the picked head unless written F
		opts       string // more for the group's cmd
		filter     bool   // the fix carries an attribute that would run a depot-configured smudge filter on checkout
		shape      string // how the fix is made, one commit under the machine's own name unless written: app, one commit by the App; two, two commits; mixed, two of which the lower is the App's, mixed2, two of which the upper is; base, a merge of the base in; signed, a commit that carries a signature; wrongid, a commit by the App's name under another id
		unrecorded bool   // then a run on a bead of the same fix, with what the first run pushed already on the branch and the bead's fix_commit still the builder's
		clock      bool   // make the timeout elapse immediately after its first epoch read
		stage      string // where the bead ends
		hold       bool
		wants      []string // in its notes, {fixshort} being the builder's commit and {tipshort} what was pushed
		not        []string // and not
	}{
		{what: "green, triaged", triaged: true, stage: "triage",
			wants: []string{"pushed {tipshort} (the builder's {fixshort} made again as the App, and recorded as the bead's fix_commit) onto forkowner/forkrepo their-branch", "fresh accept", "public already"}},
		{what: "green, never triaged", stage: "triage",
			wants: []string{"pushed {tipshort} (the builder's {fixshort} made again as the App, and recorded as the bead's fix_commit) onto forkowner/forkrepo their-branch"}},
		{what: "its head arrives while it waits", triaged: true, prhead: "late", stage: "triage",
			wants: []string{"pushed {tipshort} (the builder's {fixshort} made again as the App, and recorded as the bead's fix_commit) onto forkowner/forkrepo their-branch"}},
		{what: "its head never arrives", triaged: true, prhead: "never", opts: "YARDR_PUSH_WAIT=1", stage: "push", hold: true,
			wants: []string{"waited 1 s for the bead's head to become the pushed {tipshort}", "the push is on forkowner/forkrepo their-branch", "Advance to triage by hand"}},
		{what: "the configured wait cannot exceed five minutes", triaged: true, prhead: "never", opts: "YARDR_PUSH_WAIT=301", clock: true, stage: "push", hold: true,
			wants: []string{"waited 300 s for the bead's head to become the pushed {tipshort}"}},
		{what: "depot Git config and PR attributes cannot affect the push", filter: true, stage: "triage",
			wants: []string{"pushed {tipshort}", "onto forkowner/forkrepo their-branch"}},
		{what: "already pushed when it runs", triaged: true, live: "F", fork: "F", prhead: "F", stage: "triage",
			wants: []string{"pushed {fixshort} onto forkowner/forkrepo their-branch"}, not: []string{"made again"}},
		{what: "a fix of two commits", triaged: true, shape: "two", stage: "triage",
			wants: []string{"pushed {tipshort} (the builder's {fixshort} made again as the App"}},
		{what: "a fix whose lower commit is the App's already", triaged: true, shape: "mixed", stage: "triage",
			wants: []string{"pushed {tipshort} (the builder's {fixshort} made again as the App"}},
		{what: "a fix whose upper commit is the App's already, on a lower one that is not", shape: "mixed2", stage: "triage",
			wants: []string{"pushed {tipshort} (the builder's {fixshort} made again as the App"}},
		{what: "a fix that is the App's already goes out as it is", shape: "app", stage: "triage",
			wants: []string{"pushed {fixshort} onto forkowner/forkrepo their-branch"}, not: []string{"made again"}},
		{what: "a fix that merges the base in", triaged: true, shape: "base", stage: "triage",
			wants: []string{"pushed {tipshort} (the builder's {fixshort} made again as the App"}},
		{what: "a fix with a signature", shape: "signed", stage: "triage",
			wants: []string{"pushed {tipshort} (the builder's {fixshort} made again as the App"}},
		{what: "a fix by the App's name under another id", shape: "wrongid", stage: "triage",
			wants: []string{"pushed {tipshort} (the builder's {fixshort} made again as the App"}},
		{what: "a push that went out and was not recorded", triaged: true, unrecorded: true, stage: "triage",
			wants: []string{"pushed {tipshort} (the builder's {fixshort} made again as the App"}},
	} {
		t.Run(tc.what, func(t *testing.T) {
			c := newFixYard(t)
			id, num, branch := "r-p", 20, "their-branch"
			H := c.head(map[string]string{"feature.txt": "theirs\n"})
			fixFiles := map[string]string{"feature.txt": "fixed\n"}
			if tc.filter {
				fixFiles[".gitattributes"] = "feature.txt filter=review\n"
			}
			again := map[string]string{"feature.txt": "fixed, and again\n"}
			var F, moved string
			switch tc.shape {
			case "app":
				F = c.fixOn(H, fixFiles)
			case "two":
				F = c.commitOn(c.commitOn(H, fixFiles), again)
			case "mixed":
				F = c.commitOn(c.fixOn(H, fixFiles), again)
			case "mixed2":
				F = c.fixOn(c.commitOn(H, fixFiles), again)
			case "base":
				// The base moved on under the pull request, by another hand, and the
				// builder merged it into its checkout: the merge is the builder's, the
				// base's commit is its second parent and is not the fix's.
				moved = c.commitBy(someone, someone, c.git(c.work, "rev-parse", "main"), map[string]string{"base.txt": "moved on\n"})
				F = c.mergeBy("", c.commitOn(H, fixFiles), moved)
			case "signed":
				F = c.signedOn(H, fixFiles)
				if got := c.commitIn(c.work, F).extra; !slices.Contains(got, "gpgsig") {
					t.Fatalf("the signed fix has headers %v, want a gpgsig among them", got)
				}
			case "wrongid":
				wrong := appLogin + " <99999+" + appLogin + "@users.noreply.github.com>"
				F = c.commitBy(wrong, wrong, H, fixFiles)
			default:
				F = c.commitOn(H, fixFiles)
			}
			c.depotFetch(F)
			filterRan := filepath.Join(c.gh, "smudge-ran")
			if tc.filter {
				c.git(c.repo, "config", "filter.review.smudge", "printf ran > "+filterRan)
				c.git(c.repo, "config", "push.followTags", "true")
				c.git(c.repo, "tag", "-a", "private-tag", "-m", "local-only", F)
			}
			forkAt := H
			if tc.fork == "F" {
				forkAt = F
			}
			c.forkTo(branch, forkAt)
			live := H
			if tc.live == "F" {
				live = F
			}
			prhead := H
			if tc.prhead == "F" {
				prhead = F
			}
			ctx, st := context.Background(), rpc.NewClient((&config.Config{Home: c.home}).Socket())
			if tc.triaged {
				c.roundToPush(id, prURLn(num), H, H, H, F)
				if _, err := st.UpdateBead(ctx, id, store.BeadPatch{Metadata: map[string]any{"pr_head": prhead}}, store.Expect{}, "test"); err != nil {
					t.Fatal(err)
				}
			} else {
				c.pushBead(id, prURLn(num), "push", []string{"fix-ok"},
					map[string]any{"fix_head": H, "reviewed_head": H, "fix_commit": F, "pr_head": prhead})
			}
			depot := c.git(c.repo, "status", "--porcelain") + c.git(c.repo, "rev-parse", "HEAD")
			refs := c.git(c.repo, "for-each-ref")
			if tc.opts != "" {
				c.opts += " " + tc.opts
			}
			// The yard hears its own push at once, unless the row says it hears it late or never.
			arrival := map[string]string{"": "now", "late": "late", "never": "never", "F": "now"}[tc.prhead]
			c.opts += " YARDR_TEST_ARRIVAL=" + arrival + " YARDR_TEST_BRANCH=" + branch
			if tc.clock {
				date, err := exec.LookPath("date")
				if err != nil {
					t.Fatal(err)
				}
				scripttest.Write(t, filepath.Join(c.gh, "bin", "date"), strings.ReplaceAll(fakePushDate, "{date}", date))
			}
			standing := map[string]string{"head-owner": "forkowner", "head-name": "forkrepo", "head-ref": branch}
			c.push(id, mergeOpts{head: live}, standing)

			// What was pushed: the builder's commit as it was where the fix is the
			// App's already or was pushed already, else the fix made again as the App.
			tip := c.forkHead(branch)
			fill := strings.NewReplacer("{sha}", H, "{short}", H[:7], "{fix}", F, "{fixshort}", F[:7], "{tipshort}", tip[:7], "{id}", id)
			c.has(id, tc.what, tc.stage, tc.hold)
			for _, want := range tc.wants {
				c.noted(id, tc.what, fill.Replace(want))
			}
			for _, not := range tc.not {
				if notes := c.notesOf(id); strings.Contains(notes, not) {
					t.Errorf("%s: a note has %q:\n%s", tc.what, not, notes)
				}
			}
			c.clean(id, tc.what)
			wasApp := tc.what == "already pushed when it runs" || tc.shape == "app"
			if wasApp {
				if tip != F {
					t.Errorf("%s: the fork's branch is at %s, want the builder's commit %s as it was", tc.what, tip, F)
				}
			} else {
				was, now := c.asTheApp(tc.what, H, F, tip)
				if tip == F {
					t.Errorf("%s: the commit was pushed as the builder made it", tc.what)
				}
				switch tc.shape {
				case "mixed":
					if now[0] != was[0] || now[1] == was[1] {
						t.Errorf("%s: the lower commit %s was made again as %s, and the upper %s as %s: want the lower kept and the upper made again", tc.what, was[0], now[0], was[1], now[1])
					}
				case "base":
					merge := c.commitIn(c.fork, tip)
					if len(merge.parents) != 2 || merge.parents[1] != moved {
						t.Errorf("%s: the merge has parents %v, want its second parent %s, the base's commit, as it was", tc.what, merge.parents, moved)
					}
					if who, _ := ident(c.commitIn(c.fork, moved).author); who != someone {
						t.Errorf("%s: the base's commit is by %q after the push, want %q: it is not the fix's and is not made again", tc.what, who, someone)
					}
				}
			}
			// The recorded commit is the one on the branch.
			if got := fmt.Sprint(c.beadOf(id).Metadata["fix_commit"]); got != tip {
				t.Errorf("%s: the bead's fix_commit is %s, want the pushed %s", tc.what, got, tip)
			}
			if tc.filter {
				if _, err := os.Stat(filterRan); !os.IsNotExist(err) {
					t.Errorf("%s: checking out the pull request ran its configured smudge filter (stat: %v)", tc.what, err)
				}
				if tags := c.git(c.fork, "for-each-ref", "--format=%(refname)", "refs/tags"); tags != "" {
					t.Errorf("%s: the push added local tags to the pull request's repository: %s", tc.what, tags)
				}
			}
			// The push names the live pull request's URL and ref, fast-forward
			// only: no force flag, the Authorization header carried in git's
			// environment, hooks off, and no git in the builder's worktree.
			calls := strings.Join(gitCalls(t, c.gh), "\n")
			pushed := "push https://github.com/forkowner/forkrepo.git " + tip + ":refs/heads/" + branch
			if tc.what == "already pushed when it runs" {
				if strings.Contains(calls, " push https://") {
					t.Errorf("%s: the script pushed again:\n%s", tc.what, calls)
				}
			} else {
				if n := strings.Count(calls, pushed); n != 1 {
					t.Errorf("%s: the push %q runs %d times, want once:\n%s", tc.what, pushed, n, calls)
				}
				if !strings.Contains(calls, "auth-carried") || strings.Contains(calls, "NO-AUTH") {
					t.Errorf("%s: the push carried no Authorization header:\n%s", tc.what, calls)
				}
				if !strings.Contains(calls, "core.hooksPath=") {
					t.Errorf("%s: the push ran with hooks on:\n%s", tc.what, calls)
				}
				// The push is fast-forward only: no force flag on its line.
				for _, call := range gitCalls(t, c.gh) {
					if !strings.Contains(call, " push https://") {
						continue
					}
					for _, force := range []string{"--force", " -f ", "+refs/heads/"} {
						if strings.Contains(call, force) {
							t.Errorf("%s: the push carries %q: %q", tc.what, force, call)
						}
					}
				}
			}
			// No git runs with the builder's worktree as its directory: every
			// -C names the depot or the script's temporary bare repository.
			wt := filepath.Join(c.home, "worktrees", "r", id)
			for _, call := range gitCalls(t, c.gh) {
				if strings.Contains(call, "-C "+wt+" ") || strings.HasSuffix(call, "-C "+wt) {
					t.Errorf("%s: git ran in the builder's worktree: %q", tc.what, call)
				}
			}
			// GitHub was asked once, to read the pull request, with a token
			// minted for the repository alone and narrowed to the push and the
			// read; the token is nowhere but gh's and git's environments.
			if n := countCalls(t, c.gh, "pr view "); n != 1 {
				t.Errorf("%s: the pull request was read %d times, want once:\n%v", tc.what, n, ghCalls(t, c.gh))
			}
			// Who the App is is asked once, as the token, by a run that makes the
			// fix again: one that finds the recorded commit on the branch asks
			// nothing.
			asks := 1
			if tc.what == "already pushed when it runs" {
				asks = 0
			}
			c.asksOnly(tc.what, asks, asks)
			if asked := readFile(t, filepath.Join(c.gh, "mint-stdin")); !strings.Contains(asked, `"scope":"o/r"`) || !strings.Contains(asked, `"permissions":{"contents":"write","pull_requests":"read"}`) {
				t.Errorf("%s: the mint was asked %s, want the scope o/r narrowed to contents write and pull_requests read", tc.what, asked)
			}
			for what, text := range map[string]string{"the log": c.log, "the notes": c.notesOf(id), "gh's calls": readFile(t, filepath.Join(c.gh, "calls")), "git's calls": calls} {
				if strings.Contains(text, mintToken) {
					t.Errorf("%s: %s hold the token", tc.what, what)
				}
			}
			if after := c.git(c.repo, "status", "--porcelain") + c.git(c.repo, "rev-parse", "HEAD"); after != depot {
				t.Errorf("%s: the depot's checkout changed:\n%s\n%s", tc.what, depot, after)
			}
			if after := c.git(c.repo, "for-each-ref"); after != refs {
				t.Errorf("%s: the depot's refs changed:\n%s\n%s", tc.what, refs, after)
			}

			// A run on a bead of the same fix after the push went out and was not
			// recorded: the pull request is at what the first run pushed, and the
			// bead's fix_commit is still the builder's. The fix is made again as the
			// same commit, which the head is, so nothing is pushed twice, and it is
			// recorded.
			if tc.unrecorded {
				id2 := "r-q"
				c.roundToPush(id2, prURLn(num+1), H, H, H, F)
				c.push(id2, mergeOpts{head: tip}, standing)
				c.has(id2, tc.what+", run again", "triage", false)
				c.noted(id2, tc.what+", run again", fill.Replace("pushed {tipshort} (the builder's {fixshort} made again as the App"))
				if got := fmt.Sprint(c.beadOf(id2).Metadata["fix_commit"]); got != tip {
					t.Errorf("%s: the run again recorded fix_commit %s, want %s", tc.what, got, tip)
				}
				if again := strings.Join(gitCalls(t, c.gh), "\n"); strings.Contains(again, " push https://") {
					t.Errorf("%s: the run again pushed a second time:\n%s", tc.what, again)
				}
				if got := c.forkHead(branch); got != tip {
					t.Errorf("%s: the run again moved the fork's branch to %s from %s", tc.what, got, tip)
				}
				c.asksOnly(tc.what+", run again", 1, 1)
				c.clean(id2, tc.what+", run again")
			}
		})
	}
}

// What is no push of the fix holds the bead, with a note that says why and
// how to go on, and nothing reaches the branch: a bead that is not in push,
// where no builder has fixed the picked head; no pick, or one whose heads
// disagree; no fix commit, one that is the picked head itself, one the depot
// has not got, or one that is not on the picked head; a head that moved; a
// branch that forbids maintainer edits; a pull request that is not open or
// that GitHub will not show; a token that cannot be had; a link that is no
// pull request address; a push GitHub refuses, in git's words; a fix with a
// commit that is not the App's, by author or by committer, name or address, at
// the tip or lower down, or whose first parents never meet the pick; and a
// token whose own identity GitHub will not say, or says is no bot's. The
// temporary repository is not made for cases rejected before it is.
func TestPushFixHoldsWhatItCannotPush(t *testing.T) {
	t.Parallel() // as its subtests are: see TestMain
	zzz := strings.Repeat("z", 40)
	for _, tc := range []struct {
		what     string
		triaged  bool              // the bead came the pr flow's way, through triage; else made in push directly
		stage    string            // direct stage, push unless written
		url      string            // its link, its pull request's unless written
		noLabel  bool              // direct bead carries no fix-ok
		pick     string            // fix_head, H unless written: - none, z no sha, B another head
		reviewed string            // reviewed_head, H unless written: - none, B another
		accepted string            // accepted_head, for triaged beads: H unless written, - none, B another
		commit   string            // fix_commit, F unless written: - none, H the picked head, F2 off main, G unknown; R the pick merged into the base, E a commit that names an encoding of its own
		live     string            // what GitHub says the head is, H unless written: B another
		fork     string            // where the fork's branch is, H unless written: B another
		stand    map[string]string // the stand-in's, besides the head
		change   func(c *fixYard)  // changes the group for this run
		tried    bool              // the run pushed and GitHub refused it
		app      string            // how far the run got in asking who the App is, not at all unless written: login (its login alone) or both
		unasked  bool              // GitHub is not asked about the pull request
		wants    []string          // in its notes, {id} and {short} being the bead and the picked head
		not      []string          // and not
	}{
		{what: "a bead no builder has fixed the picked head of", stage: "fix", unasked: true,
			wants: []string{"is not in push", "nowhere before it"}},
		{what: "a bead with no pick", noLabel: true, unasked: true,
			wants: []string{"does not carry fix-ok"}},
		{what: "a pick with no head", pick: "-", wants: []string{"has no fix_head"}},
		{what: "a picked head that is no sha, and not copied to the bead", pick: "z",
			wants: []string{"has no fix_head"}, not: []string{zzz}},
		{what: "a pick the reviewer did not read", reviewed: "B",
			wants: []string{"is not the reviewed", "Advance the bead to triage"}},
		{what: "a pick no reviewer recorded", reviewed: "-",
			wants: []string{"has no reviewed_head"}},
		{what: "a pick no maintainer accepted", triaged: true, accepted: "B",
			wants: []string{"is not the accepted", "Advance the bead to triage"}},
		{what: "a pick whose acceptance recorded no head", triaged: true, accepted: "-",
			wants: []string{"waited in triage and has no accepted_head"}},
		{what: "a fix its builder recorded none of", commit: "-",
			wants: []string{"has no fix_commit"}},
		{what: "a fix that is the picked head itself", commit: "H",
			wants: []string{"is the picked head itself"}},
		{what: "a fix the depot has not got", commit: "G",
			wants: []string{"has no commit"}},
		{what: "a fix that is not on the picked head", commit: "F2",
			wants: []string{"is not on the picked"}},
		{what: "a head that moved since the pick", live: "B", fork: "B", app: "both",
			wants: []string{"is not the picked", "Advance the bead to triage"}},
		{what: "a branch that forbids maintainer edits", stand: map[string]string{"edits-off": ""},
			wants: []string{"forbids maintainer edits", "unhold"}},
		{what: "a branch with no answer about maintainer edits", stand: map[string]string{"edits-absent": ""},
			wants: []string{"gave no maintainerCanModify"}},
		{what: "a pull request that is closed", stand: map[string]string{"state": "CLOSED"},
			wants: []string{"is in state CLOSED, not OPEN"}},
		{what: "a pull request gh cannot read", stand: map[string]string{"view-fail": "HTTP 404: Not Found"},
			wants: []string{"reading pull request", "failed: HTTP 404: Not Found"}},
		{what: "a head that is no commit sha", stand: map[string]string{"head": "../../etc"},
			wants: []string{"no head commit"}},
		{what: "a branch that is no branch name", stand: map[string]string{"head-ref": "../evil"},
			wants: []string{"no branch name"}},
		{what: "a head repository that is no owner and name", stand: map[string]string{"head-owner": "??"},
			wants: []string{"no head repository"}},
		{what: "a mint that fails", stand: map[string]string{"mint-fail": "PRIVATE_KEY_VALUE_SHOULD_NOT_ESCAPE"}, unasked: true,
			wants: []string{"minting a token for o/r failed", "not pushing the fix"}},
		{what: "no env_file", change: func(c *fixYard) { c.envFile, c.opts = "", c.opts+" GITHUB_APP_ID= GITHUB_APP_PRIVATE_KEY_FILE=" }, unasked: true,
			wants: []string{"has no GITHUB_APP_ID and GITHUB_APP_PRIVATE_KEY_FILE", "env_file"}},
		{what: "a link that is no pull request address", url: "https://github.com/o/r/pull/31/files", unasked: true,
			wants: []string{"which is not a pull request address", "not pushing the fix"}},
		{what: "a push GitHub refuses", fork: "B", tried: true, app: "both",
			wants: []string{"refused the push", "[rejected]", "their-branch", "Resume by hand to triage"}},
		{what: "a fix that merges the pick into the base", commit: "R", app: "both",
			wants: []string{"never meet the picked", "with the base merged into it, never the other way"}},
		{what: "a fix with a commit that names an encoding of its own", commit: "E", app: "both",
			wants: []string{"commit {recshort} of the fix names an encoding of its own", "Make the commit again in UTF-8"}},
		{what: "a token whose login is no bot's", stand: map[string]string{"viewer-login": "jo"}, app: "login",
			wants: []string{"GitHub names the token's login as jo", "no GitHub App's bot login", "cannot be made as the App"}},
		{what: "GitHub cannot say who the token is", stand: map[string]string{"viewer-fail": "HTTP 403: Resource not accessible by integration"}, app: "login",
			wants: []string{"asking GitHub who the App is", "HTTP 403: Resource not accessible by integration", "cannot be made as the App"}},
		{what: "a bot user GitHub gives no id for", stand: map[string]string{"bot-type": "User"}, app: "both",
			wants: []string{"GitHub gave no id for the bot user yard-app[bot] that is a number"}},
		{what: "a bot user GitHub cannot show", stand: map[string]string{"user-fail": "HTTP 404: Not Found"}, app: "both",
			wants: []string{"asking GitHub for the id of the user yard-app[bot] failed", "HTTP 404: Not Found"}},
	} {
		t.Run(tc.what, func(t *testing.T) {
			c := newFixYard(t)
			id, num, branch := "r-h", 30, "their-branch"
			H := c.head(map[string]string{"feature.txt": "theirs\n"})
			F := c.commitOn(H, map[string]string{"feature.txt": "fixed\n"})
			B := c.commitOn(H, map[string]string{"feature.txt": "theirs, pushed\n"})
			base := c.git(c.work, "rev-parse", "main")
			F2 := c.commitOn(base, map[string]string{"feature.txt": "another line\n"})
			G := c.commitOn(H, map[string]string{"feature.txt": "never fetched\n"})
			c.depotFetch(F)
			c.depotFetch(F2)
			// The fixes of the rows that are made another way, made when a row asks
			// for one, and in the depot as a builder's commits are.
			variants := map[string]string{}
			variant := func(v string) string {
				if made, ok := variants[v]; ok {
					return made
				}
				var made string
				switch v {
				case "R":
					// The pick merged into the base, not the base into the pick.
					made = c.mergeBy("", base, H)
				case "E":
					plain := c.commitIn(c.work, c.commitOn(H, map[string]string{"feature.txt": "fix E\n"}))
					made = c.craft("tree " + plain.tree + "\nparent " + H + "\nauthor " + plain.author + "\ncommitter " + plain.committer + "\nencoding ISO-8859-1\n\n" + plain.message)
				default:
					c.t.Fatalf("no fix made as %q", v)
				}
				c.depotFetch(made)
				variants[v] = made
				return made
			}
			sha := func(v, def string) string {
				switch v {
				case "R", "E":
					return variant(v)
				case "B":
					return B
				case "F":
					return F
				case "F2":
					return F2
				case "G":
					return G
				case "H":
					return H
				case "z":
					return zzz
				case "-":
					return ""
				default:
					return def
				}
			}
			pick, reviewed, accepted := sha(tc.pick, H), sha(tc.reviewed, H), sha(tc.accepted, H)
			commit, live, forkAt := sha(tc.commit, F), sha(tc.live, H), sha(tc.fork, H)
			c.forkTo(branch, forkAt)
			fill := strings.NewReplacer("{id}", id, "{sha}", H, "{short}", H[:7], "{fix}", F, "{fixshort}", F[:7], "{recshort}", short7(commit))
			stage, url := "push", prURLn(num)
			if tc.stage != "" {
				stage = tc.stage
			}
			if tc.url != "" {
				url = tc.url
			}
			ctx, st := context.Background(), rpc.NewClient((&config.Config{Home: c.home}).Socket())
			if tc.triaged {
				c.roundToPush(id, url, accepted, reviewed, pick, commit)
				if _, err := st.UpdateBead(ctx, id, store.BeadPatch{Metadata: map[string]any{"pr_head": H}}, store.Expect{}, "test"); err != nil {
					t.Fatal(err)
				}
			} else {
				meta := map[string]any{"pr_head": H}
				if pick != "" {
					meta["fix_head"] = pick
				}
				if reviewed != "" {
					meta["reviewed_head"] = reviewed
				}
				if commit != "" {
					meta["fix_commit"] = commit
				}
				labels := []string{"fix-ok"}
				if tc.noLabel {
					labels = nil
				}
				c.pushBead(id, url, stage, labels, meta)
			}
			depot := c.git(c.repo, "status", "--porcelain") + c.git(c.repo, "rev-parse", "HEAD")
			refs := c.git(c.repo, "for-each-ref")
			envFile, opts := c.envFile, c.opts
			if tc.change != nil {
				tc.change(c)
			}
			stand := map[string]string{"head-owner": "forkowner", "head-name": "forkrepo", "head-ref": branch}
			for name, text := range tc.stand {
				stand[name] = text
			}
			c.push(id, mergeOpts{head: live}, stand)
			c.envFile, c.opts = envFile, opts

			c.has(id, tc.what, stage, true)
			for _, want := range tc.wants {
				c.noted(id, tc.what, fill.Replace(want))
			}
			for _, not := range tc.not {
				if notes := c.notesOf(id); strings.Contains(notes, not) {
					t.Errorf("%s: a note has %q:\n%s", tc.what, not, notes)
				}
			}
			c.clean(id, tc.what)
			if got := c.forkHead(branch); got != forkAt {
				t.Errorf("%s: the fork's branch is at %s, want it left at %s", tc.what, got, forkAt)
			}
			calls := strings.Join(gitCalls(t, c.gh), "\n")
			if pushed := strings.Contains(calls, " push https://"); pushed != tc.tried {
				t.Errorf("%s: pushed = %v, want %v:\n%s", tc.what, pushed, tc.tried, calls)
			}
			// Nothing is recorded as the bead's fix_commit for a push that did not go
			// out: it is the builder's still.
			if got := fmt.Sprint(c.beadOf(id).Metadata["fix_commit"]); commit != "" && got != commit {
				t.Errorf("%s: the bead's fix_commit is %s, want the builder's %s", tc.what, got, commit)
			}
			if _, err := os.Stat(filepath.Join(c.gh, "calls")); tc.unasked == (err == nil) {
				t.Errorf("%s: GitHub asked = %v, want %v:\n%v", tc.what, err == nil, !tc.unasked, ghCalls(t, c.gh))
			}
			login, botID := 0, 0
			switch tc.app {
			case "login":
				login = 1
			case "both":
				login, botID = 1, 1
			}
			c.asksOnly(tc.what, login, botID)
			if tc.what == "a mint that fails" {
				for where, text := range map[string]string{"log": c.log, "notes": c.notesOf(id)} {
					if strings.Contains(text, "PRIVATE_KEY_VALUE_SHOULD_NOT_ESCAPE") {
						t.Errorf("the mint's stderr reached the %s", where)
					}
				}
			}
			for what, text := range map[string]string{"the log": c.log, "the notes": c.notesOf(id), "git's calls": calls} {
				if strings.Contains(text, mintToken) {
					t.Errorf("%s: %s hold the token", tc.what, what)
				}
			}
			if after := c.git(c.repo, "status", "--porcelain") + c.git(c.repo, "rev-parse", "HEAD"); after != depot {
				t.Errorf("%s: the depot's checkout changed:\n%s\n%s", tc.what, depot, after)
			}
			if after := c.git(c.repo, "for-each-ref"); after != refs {
				t.Errorf("%s: the depot's refs changed:\n%s\n%s", tc.what, refs, after)
			}
		})
	}
}

// push-fix takes its binding of a bead to its pull request, and its token,
// from merge-pr's text, copied: the scripts are installed one file each, so
// there is none to source. A change to one is made in all three, as to
// pr-ci's (TestPRCIBindsAsMergePRDoes).
func TestPushFixBindsAsMergePRDoes(t *testing.T) {
	t.Parallel()
	want, got := markedBlock(t, "merge-pr", bindingOpen, bindingClose), markedBlock(t, "push-fix", bindingOpen, bindingClose)
	if got != want {
		t.Errorf("push-fix's github_binding differs from merge-pr's: change all three\nmerge-pr:\n%s\npush-fix:\n%s", want, got)
	}
}
