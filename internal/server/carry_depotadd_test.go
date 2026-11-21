package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/store"
)

func depotAddGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// depotOrigin is a fixture repository with one commit on main, cloned
// by file:// with no network.
func depotOrigin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	depotAddGit(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "readme.md"), []byte("the web depot\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	depotAddGit(t, dir, "add", "readme.md")
	depotAddGit(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-qm", "one")
	return dir
}

func depotAddAskOf(command, test string) store.Ask {
	ask := store.Ask{Question: "Add the depot?", To: "mckean", Context: []string{"the team has no shell"},
		Run: &store.AskRun{Argv: []string{command}}}
	if test != "" {
		ask.Run.Test = test
	}
	return ask
}

// An approved depot add is carried out by the server: it clones the
// file:// fixture into the yard's depots directory and records the
// depot with its base and test command, and completes the answer.
func TestCarryDepotAddClonesAndRecords(t *testing.T) {
	t.Parallel()
	st, home, bead := carryYard(t)
	ctx := context.Background()
	origin := depotOrigin(t)
	seq := askApprove(t, st, bead, depotAddAskOf("depot add web --clone file://"+origin, "sh .yardr/test"))
	if rec := carryState(t, st, bead, seq); rec.State != store.CarryApproved {
		t.Fatalf("the approved record stands %q", rec.State)
	}
	drain(t, st, home, "boot-one")
	d, err := st.GetDepot(ctx, "web")
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(home, store.DepotsDir, "web")
	if d.Path != wantPath || d.Kind != store.DepotGit || d.Base != "origin/main" || d.Test != "sh .yardr/test" {
		t.Fatalf("the depot holds %+v, want path %s, git, origin/main, its test", d, wantPath)
	}
	if _, err := os.Stat(filepath.Join(wantPath, "readme.md")); err != nil {
		t.Fatalf("the clone holds no readme: %v", err)
	}
	if rec := carryState(t, st, bead, seq); rec.State != store.CarryDone {
		t.Fatalf("the carried record stands %q", rec.State)
	}
	lines := resolvedLines(t, st, bead, seq)
	if !strings.Contains(lines, "depot web → "+wantPath+" (git, base origin/main)") || !strings.Contains(lines, "test: sh .yardr/test") {
		t.Fatalf("the completed card reads:\n%s", lines)
	}
}

// A clone that fails, and one whose base resolves nowhere, fail the
// carrying out with the reason, record no depot and leave no clone
// where a card filed again would go.
func TestCarryDepotAddFailuresLeaveNothing(t *testing.T) {
	t.Parallel()
	st, home, bead := carryYard(t)
	ctx := context.Background()
	seq := askApprove(t, st, bead, depotAddAskOf("depot add web --clone file:///no-such-repo-xyz", ""))
	drain(t, st, home, "boot-one")
	if rec := carryState(t, st, bead, seq); rec.State != store.CarryFailed || !strings.Contains(rec.Result, "cloning file:///no-such-repo-xyz") {
		t.Fatalf("the failed record = %+v", rec)
	}
	if _, err := st.GetDepot(ctx, "web"); err == nil {
		t.Fatal("a failed clone recorded the depot")
	}
	if _, err := os.Stat(filepath.Join(home, store.DepotsDir, "web")); !os.IsNotExist(err) {
		t.Fatalf("a failed clone left its place: %v", err)
	}
	if lines := resolvedLines(t, st, bead, seq); !strings.Contains(lines, "approved, failed: yardr depot add web") {
		t.Fatalf("the failed card reads:\n%s", lines)
	}

	origin := depotOrigin(t)
	seq = askApprove(t, st, bead, depotAddAskOf("depot add api --clone file://"+origin+" --base origin/nope", ""))
	drain(t, st, home, "boot-one")
	if rec := carryState(t, st, bead, seq); rec.State != store.CarryFailed || !strings.Contains(rec.Result, "base origin/nope does not resolve") {
		t.Fatalf("the failed record = %+v", rec)
	}
	if _, err := st.GetDepot(ctx, "api"); err == nil {
		t.Fatal("a clone with no base recorded the depot")
	}
	if _, err := os.Stat(filepath.Join(home, store.DepotsDir, "api")); !os.IsNotExist(err) {
		t.Fatalf("a clone with no base left its place: %v", err)
	}
}

const depotAddDecl = `name: github
base: https://api.github.com
credential: { env: GITHUB_TOKEN }
git: { credential: [github/git-credential] }
link:
  host: github.com
  issue: '^/([A-Za-z0-9](?:-?[A-Za-z0-9])*)/([A-Za-z0-9._-]+)/issues/([1-9][0-9]*)(?:/.*)?$'
  fixed: "https://github.com/{1}/{2}/issues/{3}"
  thread: "{1}/{2}#{3}"
calls:
  viewer:
    kind: read
    request: { method: GET, path: /user }
`

func depotAddPrivateYard(t *testing.T, st *store.Store, home string) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.PutSource(ctx, "github.yaml", depotAddDecl, "mckean"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "sources"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "sources", "github.env"), []byte("GITHUB_TOKEN=stand-in-token-7f3a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A private clone answers with its push source's token: the carrying
// out re-checks the fingerprint, clones with the pack's helper and
// wires it into the clone's own git config, where no token stands. The
// https fetch is rewritten to the file fixture by the test's own git
// config, so no network is touched and no credential is asked for.
func TestCarryDepotAddPrivateWiresHelper(t *testing.T) {
	st, home, bead := carryYard(t)
	ctx := context.Background()
	depotAddPrivateYard(t, st, home)
	origin := depotOrigin(t)
	gitHome := t.TempDir()
	config := "[url \"file://" + origin + "\"]\n\tinsteadOf = https://github.com/ada/web.git\n"
	if err := os.WriteFile(filepath.Join(gitHome, ".gitconfig"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", gitHome)
	seq := askApprove(t, st, bead, depotAddAskOf("depot add web --clone https://github.com/ada/web.git", ""))
	drain(t, st, home, "boot-one")
	d, err := st.GetDepot(ctx, "web")
	if err != nil {
		t.Fatal(err)
	}
	helpers, ok := gitConfigGetAll(d.Path, "credential.https://github.com.helper", true)
	if !ok || len(helpers) != 1 || !strings.Contains(helpers[0], filepath.Join("sources", "github")) {
		t.Fatalf("the clone's helpers = %q, %v", helpers, ok)
	}
	if rec := carryState(t, st, bead, seq); rec.State != store.CarryDone {
		t.Fatalf("the carried record stands %q: %s", rec.State, rec.Result)
	}
}

// A push source changed after approval, and a token taken back after
// it, each fail the carrying out before any clone, with the reason.
func TestCarryDepotAddPrivateRechecks(t *testing.T) {
	t.Parallel()
	st, home, bead := carryYard(t)
	ctx := context.Background()
	depotAddPrivateYard(t, st, home)
	seq := askApprove(t, st, bead, depotAddAskOf("depot add web --clone https://github.com/ada/web.git", ""))
	changed := strings.Replace(depotAddDecl, "https://api.github.com", "https://api.github.example.com", 1)
	if _, err := st.PutSource(ctx, "github.yaml", changed, "mckean"); err != nil {
		t.Fatal(err)
	}
	drain(t, st, home, "boot-one")
	if rec := carryState(t, st, bead, seq); rec.State != store.CarryFailed || !strings.Contains(rec.Result, "changed after approval") {
		t.Fatalf("the record with the source changed = %+v", rec)
	}
	if _, err := st.GetDepot(ctx, "web"); err == nil {
		t.Fatal("a refused carrying out recorded the depot")
	}

	st2, home2, bead2 := carryYard(t)
	depotAddPrivateYard(t, st2, home2)
	seq = askApprove(t, st2, bead2, depotAddAskOf("depot add web --clone https://github.com/ada/web.git", ""))
	if err := os.WriteFile(filepath.Join(home2, "sources", "github.env"), []byte("OTHER=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	drain(t, st2, home2, "boot-one")
	if rec := carryState(t, st2, bead2, seq); rec.State != store.CarryFailed || !strings.Contains(rec.Result, "no GITHUB_TOKEN stands for the source github") {
		t.Fatalf("the record with the token gone = %+v", rec)
	}
	if _, err := st2.GetDepot(context.Background(), "web"); err == nil {
		t.Fatal("a refused carrying out recorded the depot")
	}
}
