package assembly

// These tests cover what merge-local does with YARDR_PUSH before it
// rebases: it fetches the remote and fast-forwards the local base to the
// remote's branch, so the bead is tested against and lands on the remote's
// commits. A diverged base fails the landing; anything less (a fetch that
// fails, a remote with no such branch, a base that is simply not pushed
// yet) lands on the base as it is.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// pushYard is newYard with a bare origin that has main, pushed from the
// depot, and YARDR_PUSH naming it.
func pushYard(t *testing.T) (*yard, string) {
	t.Helper()
	y := newYard(t)
	remote := filepath.Join(t.TempDir(), "origin.git")
	y.git(".", "init", "-q", "--bare", remote)
	y.git(y.repo, "remote", "add", "origin", remote)
	y.git(y.repo, "push", "-q", "origin", "main")
	y.git(remote, "symbolic-ref", "HEAD", "refs/heads/main")
	y.push = "origin"
	return y, remote
}

// pushRemote commits file in a fresh clone of remote and pushes it, as a
// commit that reached the remote without the yard would.
func pushRemote(t *testing.T, y *yard, remote, file, content string) string {
	t.Helper()
	other := filepath.Join(t.TempDir(), "other")
	y.git(".", "clone", "-q", remote, other)
	y.commit(other, file, content)
	y.git(other, "push", "-q", "origin", "main")
	return y.git(other, "rev-parse", "main")
}

// When the remote's branch moved, the landing brings the base up to it
// before the rebase, and the bead lands on the remote's commits.
func TestMergeLocalBringsBaseUpToPush(t *testing.T) {
	y, remote := pushYard(t)
	y.commit(y.wt, "b.txt", "bead\n")
	pushed := pushRemote(t, y, remote, "m.txt", "laptop\n")

	y.assembly()

	b := y.bead()
	if b.Stage != "merged" || b.Status != "closed" {
		t.Fatalf("bead %s/%s, want merged and closed", b.Stage, b.Status)
	}
	main := y.git(y.repo, "rev-parse", "main")
	if tip := y.git(y.wt, "rev-parse", "HEAD"); tip != main {
		t.Fatalf("main %s is not the rebased branch %s", main, tip)
	}
	// The rebase stood on the fresh base: the remote's commit is in.
	y.git(y.repo, "merge-base", "--is-ancestor", pushed, "main")
	for _, f := range []string{"b.txt", "m.txt"} {
		if _, err := os.Stat(filepath.Join(y.repo, f)); err != nil {
			t.Fatalf("checkout of main lacks %s: %v", f, err)
		}
	}
	if got := y.git(remote, "rev-parse", "main"); got != main {
		t.Fatalf("origin main %s, want %s", got, main)
	}
	if !strings.Contains(y.log, "bringing main up to origin/main") {
		t.Fatalf("the log does not say the base was brought up:\n%s", y.log)
	}
}

// The same with the base checked out nowhere: the ref alone moves.
func TestMergeLocalBringsUpABaseThatIsCheckedOutNowhere(t *testing.T) {
	y, remote := pushYard(t)
	y.commit(y.wt, "b.txt", "bead\n")
	pushed := pushRemote(t, y, remote, "m.txt", "laptop\n")
	y.git(y.repo, "checkout", "-q", "--detach")

	y.assembly()

	b := y.bead()
	if b.Stage != "merged" || b.Status != "closed" {
		t.Fatalf("bead %s/%s, want merged and closed", b.Stage, b.Status)
	}
	y.git(y.repo, "merge-base", "--is-ancestor", pushed, "main")
	if !strings.Contains(y.log, "bringing main up to origin/main") {
		t.Fatalf("the log does not say the base was brought up:\n%s", y.log)
	}
}

// A base that diverged from the remote's branch fails the landing, with a
// note saying so: joining the two is a person's, never merged or forced
// here. Nothing is rebased or landed.
func TestMergeLocalDivergedBaseFails(t *testing.T) {
	y, remote := pushYard(t)
	y.commit(y.wt, "b.txt", "bead\n")
	y.commit(y.repo, "c.txt", "local\n")
	local := y.git(y.repo, "rev-parse", "main")
	branch := y.git(y.repo, "rev-parse", "yard/r-1")
	pushed := pushRemote(t, y, remote, "m.txt", "laptop\n")

	y.assembly()

	b := y.bead()
	if b.Stage != "new" || b.Status != "open" {
		t.Fatalf("bead %s/%s, want it failed back to new", b.Stage, b.Status)
	}
	notes := y.notes()
	for _, want := range []string{"diverged from origin/main", "Join them by hand"} {
		if !strings.Contains(notes, want) {
			t.Fatalf("note lacks %q:\n%s", want, notes)
		}
	}
	if got := y.git(y.repo, "rev-parse", "main"); got != local {
		t.Fatalf("main moved to %s, want it left at %s", got, local)
	}
	if got := y.git(y.repo, "rev-parse", "yard/r-1"); got != branch {
		t.Fatalf("the branch moved to %s, want it left at %s", got, branch)
	}
	if got := y.git(remote, "rev-parse", "main"); got != pushed {
		t.Fatalf("origin main moved to %s", got)
	}
}

// A base holding commits the remote lacks is simply not pushed yet, from a
// landing whose push failed: the bead lands, and the push carries them.
func TestMergeLocalUnpushedBaseStillLands(t *testing.T) {
	y, remote := pushYard(t)
	y.commit(y.wt, "b.txt", "bead\n")
	y.commit(y.repo, "c.txt", "landed but not pushed\n")

	y.assembly()

	b := y.bead()
	if b.Stage != "merged" || b.Status != "closed" {
		t.Fatalf("bead %s/%s, want merged and closed", b.Stage, b.Status)
	}
	main := y.git(y.repo, "rev-parse", "main")
	if got := y.git(remote, "rev-parse", "main"); got != main {
		t.Fatalf("origin main %s, want the landed %s", got, main)
	}
	if !strings.Contains(y.log, "holds commits origin lacks; the push below carries them") {
		t.Fatalf("the log does not say the base was ahead:\n%s", y.log)
	}
}

// A fetch that fails lands on the base as it is: the bead still merges,
// and the push fails as it would have. A failed push is recorded before the
// landing note's trailing test time, and the log times it.
func TestMergeLocalFailedFetchLandsOnBaseAsIs(t *testing.T) {
	y := newYard(t)
	y.tests("true")
	y.push = "missing-remote"
	y.commit(y.wt, "b.txt", "bead\n")

	y.assembly()

	b := y.bead()
	if b.Stage != "merged" || b.Status != "closed" {
		t.Fatalf("bead %s/%s, want merged and closed", b.Stage, b.Status)
	}
	if !strings.Contains(y.log, "could not fetch missing-remote, landing on main as it is") {
		t.Fatalf("the log does not say the fetch failed:\n%s", y.log)
	}
	notes := y.notes()
	if !regexp.MustCompile(`(?m)^.*merged .*, tested with the command true; pushing main to missing-remote failed \(not forced\) in \d+ s$`).MatchString(notes) {
		t.Fatalf("the failed push note does not end with the seconds the tests took:\n%s", notes)
	}
	if !regexp.MustCompile(`(?m)^== r-1 took: the tests \d+ s, push \d+ s$`).MatchString(y.log) {
		t.Fatalf("the log does not time the failed push:\n%s", y.log)
	}
}

// An empty branch over a base the fetch left up to date has nothing of
// its own to certify: the suite never runs, and the landing still merges
// with the skip note, pushes and installs as any other landing does.
func TestMergeLocalEmptyBranchUpToDateSkipsSuite(t *testing.T) {
	y, remote := pushYard(t)
	ran := filepath.Join(y.home, "ran")
	y.tests("echo ran >>" + ran)
	y.upgradeFrom("r")
	upgraded := filepath.Join(y.home, "upgraded")
	y.bin = writeScript(t, t.TempDir(), "yardr", `#!/bin/sh
if [ "$1" = "upgrade" ] && [ "$2" = "--help" ]; then
  printf '%s\n' 'Usage:' '  yardr upgrade [flags]'
  exit 0
fi
if [ "$1" = "upgrade" ]; then
  echo upgraded >>`+upgraded+`
  printf '%s\n' 'install    /usr/local/bin/yardr at deadbeefcd12' 'health     ok in 1 s'
  exit 0
fi
exec `+shellQuote(yardrBin)+` "$@"
`)
	base := y.git(y.repo, "rev-parse", "main")
	short := y.git(y.repo, "rev-parse", "--short", "main")

	y.assembly()

	if b := y.bead(); b.Stage != "merged" || b.Status != "closed" {
		t.Fatalf("bead %s/%s, want merged and closed:\n%s", b.Stage, b.Status, y.notes())
	}
	if _, err := os.Stat(ran); !os.IsNotExist(err) {
		t.Fatalf("the test command ran on an empty branch over an up-to-date base (stat ran: %v)", err)
	}
	wantNote := "no commits over base " + short + " (fetch: up to date), suite skipped; install ok in "
	if !strings.Contains(y.notes(), wantNote) {
		t.Fatalf("note lacks %q:\n%s", wantNote, y.notes())
	}
	if _, err := os.Stat(upgraded); err != nil {
		t.Fatalf("the install did not run on the skip path: %v", err)
	}
	for _, want := range []string{"is up to date with origin", "suite skipped", "pushed main to origin"} {
		if !strings.Contains(y.log, want) {
			t.Fatalf("the log lacks %q:\n%s", want, y.log)
		}
	}
	if got := y.git(y.repo, "rev-parse", "main"); got != base {
		t.Fatalf("main moved to %s, want it left at %s", got, base)
	}
	if got := y.git(remote, "rev-parse", "main"); got != base {
		t.Fatalf("origin main %s, want %s", got, base)
	}
}

// Every empty landing whose fetch did not confirm the base was already up
// to date tests exactly as before: a base brought up by the fetch, a base
// ahead of the remote, a missing remote branch, a failed fetch, or no
// remote at all.
func TestMergeLocalEmptyBranchWithoutUpToDateFetchStillTests(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		setup func(t *testing.T) (*yard, string)
	}{
		{
			name: "moved_base",
			setup: func(t *testing.T) (*yard, string) {
				y, remote := pushYard(t)
				pushed := pushRemote(t, y, remote, "m.txt", "laptop\n")
				return y, pushed
			},
		},
		{
			name: "base_ahead",
			setup: func(t *testing.T) (*yard, string) {
				y, _ := pushYard(t)
				y.commit(y.repo, "c.txt", "landed but not pushed\n")
				return y, y.git(y.repo, "rev-parse", "main")
			},
		},
		{
			name: "no_remote_branch",
			setup: func(t *testing.T) (*yard, string) {
				y, remote := pushYard(t)
				y.git(remote, "update-ref", "-d", "refs/heads/main")
				return y, y.git(y.repo, "rev-parse", "main")
			},
		},
		{
			name: "failed_fetch",
			setup: func(t *testing.T) (*yard, string) {
				y := newYard(t)
				y.push = "missing-remote"
				return y, y.git(y.repo, "rev-parse", "main")
			},
		},
		{
			name: "no_push",
			setup: func(t *testing.T) (*yard, string) {
				y := newYard(t)
				return y, y.git(y.repo, "rev-parse", "main")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			y, wantMain := tc.setup(t)
			ran := filepath.Join(y.home, "ran")
			y.tests("echo ran >>" + ran)

			y.assembly()

			if b := y.bead(); b.Stage != "merged" || b.Status != "closed" {
				t.Fatalf("bead %s/%s, want merged and closed:\n%s", b.Stage, b.Status, y.notes())
			}
			if n := runs(t, ran); n != 1 {
				t.Fatalf("the test command ran %d times on an empty branch (%s), want 1", n, tc.name)
			}
			if strings.Contains(y.notes(), "suite skipped") {
				t.Fatalf("the note says the suite was skipped (%s):\n%s", tc.name, y.notes())
			}
			if !strings.Contains(y.notes(), "tested with the command") {
				t.Fatalf("the note does not say the base was tested (%s):\n%s", tc.name, y.notes())
			}
			if got := y.git(y.repo, "rev-parse", "main"); got != wantMain {
				t.Fatalf("main %s, want %s", got, wantMain)
			}
		})
	}
}

// The skip is for empty branches alone: a bead's own commit over an
// up-to-date base is tested as before.
func TestMergeLocalNonEmptyBranchUpToDateStillTests(t *testing.T) {
	y, _ := pushYard(t)
	ran := filepath.Join(y.home, "ran")
	y.tests("echo ran >>" + ran)
	y.commit(y.wt, "b.txt", "bead\n")

	y.assembly()

	if b := y.bead(); b.Stage != "merged" || b.Status != "closed" {
		t.Fatalf("bead %s/%s, want merged and closed:\n%s", b.Stage, b.Status, y.notes())
	}
	if n := runs(t, ran); n != 1 {
		t.Fatalf("the test command ran %d times on a non-empty branch, want 1", n)
	}
	if strings.Contains(y.notes(), "suite skipped") {
		t.Fatalf("the note says the suite was skipped on a non-empty branch:\n%s", y.notes())
	}
}

// A deleted remote branch must not be mistaken for its stale
// remote-tracking ref: landing starts from the local base as it is.
func TestMergeLocalDeletedRemoteBranchLandsOnBaseAsIs(t *testing.T) {
	y, remote := pushYard(t)
	base := y.git(y.repo, "rev-parse", "main")
	y.commit(y.wt, "b.txt", "bead\n")
	pushed := pushRemote(t, y, remote, "m.txt", "laptop\n")
	y.git(y.repo, "fetch", "origin")
	y.git(y.repo, "reset", "--hard", base)
	y.git(remote, "update-ref", "-d", "refs/heads/main")

	y.assembly()

	b := y.bead()
	if b.Stage != "merged" || b.Status != "closed" {
		t.Fatalf("bead %s/%s, want merged and closed", b.Stage, b.Status)
	}
	main := y.git(y.repo, "rev-parse", "main")
	if tip := y.git(y.wt, "rev-parse", "HEAD"); tip != main {
		t.Fatalf("main %s is not the rebased branch %s", main, tip)
	}
	if err := exec.Command("git", "-C", y.repo, "merge-base", "--is-ancestor", pushed, "main").Run(); err == nil {
		t.Fatalf("deleted remote commit %s unexpectedly reached main %s", pushed, main)
	}
	if !strings.Contains(y.log, "has no branch main") {
		t.Fatalf("the log does not say the remote branch is missing:\n%s", y.log)
	}
}
