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
	"os"
	"os/exec"
	"path/filepath"
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

// pushEndsAt is the yardr the script is given: the real one, that first notes
// in $FAKE_GH/ended where the script is, and that it made no auxiliary
// worktree, whenever the script ends its session by reporting or holding.
// The report watch separately proves the script's temporary repository is
// gone before this call. Where YARDR_TEST_FLIP_HEAD names a commit,
// the hook arrives while the script waits: the third look at the bead (the
// wait's second poll) makes its head that commit first, so the wait sees the
// old head, sleeps, and then the new one, with no sleep of the test's.
const pushEndsAt = `#!/bin/sh
if [ "$1" = bead ] && [ "$2" = show ]; then
  n=$(cat "$FAKE_GH/shows" 2>/dev/null || echo 0)
  n=$((n + 1))
  echo "$n" >"$FAKE_GH/shows"
  if [ "$n" -ge 3 ] && [ -n "${YARDR_TEST_FLIP_HEAD:-}" ]; then
    {yardr} bead update "$3" --meta pr_head="$YARDR_TEST_FLIP_HEAD" >/dev/null 2>&1
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
// it, detached, leaving the branch where it was.
func (c *fixYard) commitOn(base string, files map[string]string) string {
	c.t.Helper()
	c.git(c.work, "checkout", "-q", "--detach", base)
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(c.work, name), []byte(text), 0o644); err != nil {
			c.t.Fatal(err)
		}
	}
	c.git(c.work, "add", "-A")
	// Detached, so the commit's message names what it is on.
	c.git(c.work, "commit", "-q", "--allow-empty", "-m", "on "+base)
	sha := c.git(c.work, "rev-parse", "HEAD")
	c.git(c.work, "checkout", "-q", "main")
	return sha
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

// push-fix pushes the builder's recorded fix commit onto the pull request's
// branch as the yard's App, fast-forward only, and reports done once the
// bead's head is the pushed commit: the bead goes back to triage for a fresh
// accept of the new head. The remote and the ref are the live pull request's,
// never local config's; the token is minted for the repository alone,
// narrowed to Contents write and Pull requests read, and reaches git as an
// Authorization header in git's environment alone; hooks are off; no git runs
// in the builder's worktree. Where the head never arrives the run holds and
// names the wait, with the push on the branch; where the push went out and an
// earlier run ended before it reported, this one waits for the head and pushes
// nothing again.
func TestPushFixPushesTheFix(t *testing.T) {
	t.Parallel() // as its subtests are: see TestMain
	for _, tc := range []struct {
		what    string
		triaged bool   // the bead came the pr flow's way, through triage; else made in push directly
		prhead  string // the bead's pr_head, the picked head unless written: F the fix, flip arriving while it waits
		live    string // what GitHub says the head is, the picked head unless written F
		fork    string // where the fork's branch is, the picked head unless written F
		opts    string // more for the group's cmd
		filter  bool   // the fix carries an attribute that would run a depot-configured smudge filter on checkout
		clock   bool   // make the timeout elapse immediately after its first epoch read
		stage   string // where the bead ends
		hold    bool
		wants   []string // in its notes, {fixshort} being the fix
	}{
		{what: "green, triaged", triaged: true, prhead: "F", stage: "triage",
			wants: []string{"pushed {fixshort} onto forkowner/forkrepo their-branch", "fresh accept", "public already"}},
		{what: "green, never triaged", prhead: "F", stage: "triage",
			wants: []string{"pushed {fixshort} onto forkowner/forkrepo their-branch"}},
		{what: "its head arrives while it waits", triaged: true, prhead: "flip", stage: "triage",
			wants: []string{"pushed {fixshort} onto forkowner/forkrepo their-branch"}},
		{what: "its head never arrives", triaged: true, opts: "YARDR_PUSH_WAIT=1", stage: "push", hold: true,
			wants: []string{"waited 1 s for the bead's head to become the pushed {fixshort}", "the push is on forkowner/forkrepo their-branch", "Advance to triage by hand"}},
		{what: "the configured wait cannot exceed five minutes", triaged: true, opts: "YARDR_PUSH_WAIT=301", clock: true, stage: "push", hold: true,
			wants: []string{"waited 300 s for the bead's head to become the pushed {fixshort}"}},
		{what: "depot Git config and PR attributes cannot affect the push", filter: true, prhead: "F", stage: "triage",
			wants: []string{"pushed {fixshort} onto forkowner/forkrepo their-branch"}},
		{what: "already pushed when it runs", triaged: true, live: "F", fork: "F", prhead: "F", stage: "triage",
			wants: []string{"pushed {fixshort} onto forkowner/forkrepo their-branch"}},
	} {
		t.Run(tc.what, func(t *testing.T) {
			c := newFixYard(t)
			id, num, branch := "r-p", 20, "their-branch"
			H := c.head(map[string]string{"feature.txt": "theirs\n"})
			fixFiles := map[string]string{"feature.txt": "fixed\n"}
			if tc.filter {
				fixFiles[".gitattributes"] = "feature.txt filter=review\n"
			}
			F := c.commitOn(H, fixFiles)
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
			fill := strings.NewReplacer("{sha}", H, "{short}", H[:7], "{fix}", F, "{fixshort}", F[:7], "{id}", id)
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
			opts := c.opts
			if tc.opts != "" {
				c.opts += " " + tc.opts
			}
			if tc.prhead == "flip" {
				c.opts += " YARDR_TEST_FLIP_HEAD=" + F
			}
			if tc.clock {
				date, err := exec.LookPath("date")
				if err != nil {
					t.Fatal(err)
				}
				scripttest.Write(t, filepath.Join(c.gh, "bin", "date"), strings.ReplaceAll(fakePushDate, "{date}", date))
			}
			c.push(id, mergeOpts{head: live}, map[string]string{"head-owner": "forkowner", "head-name": "forkrepo", "head-ref": branch})
			c.opts = opts

			c.has(id, tc.what, tc.stage, tc.hold)
			for _, want := range tc.wants {
				c.noted(id, tc.what, fill.Replace(want))
			}
			c.clean(id, tc.what)
			if got := c.forkHead(branch); got != F {
				t.Errorf("%s: the fork's branch is at %s, want the fix %s", tc.what, got, F)
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
			pushed := "push https://github.com/forkowner/forkrepo.git " + F + ":refs/heads/" + branch
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
			for _, call := range ghCalls(t, c.gh) {
				if call != "" && !strings.HasPrefix(call, "pr view ") {
					t.Errorf("%s: gh was asked more than to read the pull request: %q", tc.what, call)
				}
			}
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
// pull request address; a push GitHub refuses, in git's words. The temporary
// repository is not made for cases rejected before it is.
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
		commit   string            // fix_commit, F unless written: - none, H the picked head, F2 off main, G unknown
		live     string            // what GitHub says the head is, H unless written: B another
		fork     string            // where the fork's branch is, H unless written: B another
		stand    map[string]string // the stand-in's, besides the head
		change   func(c *fixYard)  // changes the group for this run
		tried    bool              // the run pushed and GitHub refused it
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
		{what: "a head that moved since the pick", live: "B", fork: "B",
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
		{what: "a push GitHub refuses", fork: "B", tried: true,
			wants: []string{"refused the push", "[rejected]", "their-branch", "Resume by hand to triage"}},
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
			sha := func(v, def string) string {
				switch v {
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
			fill := strings.NewReplacer("{id}", id, "{sha}", H, "{short}", H[:7], "{fix}", F, "{fixshort}", F[:7])
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
			if _, err := os.Stat(filepath.Join(c.gh, "calls")); tc.unasked == (err == nil) {
				t.Errorf("%s: GitHub asked = %v, want %v:\n%v", tc.what, err == nil, !tc.unasked, ghCalls(t, c.gh))
			}
			for _, call := range ghCalls(t, c.gh) {
				if call != "" && !strings.HasPrefix(call, "pr view ") {
					t.Errorf("%s: gh was asked more than to read the pull request: %q", tc.what, call)
				}
			}
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
