package assembly

// Template yards: the setups every test repeats, built once and copied per
// test.
//
// Nearly every test in this package starts from one of a few identical
// states: a yard with a depot and a bead (newYard), a train yard with two
// children (newTrainYard), one with both children landed (landedTrainYard),
// one with its train integrated and its pull request open (integratedYard),
// that one reviewed and in stage land (landYard), or a bare repository with a
// branch in its worktree (newWatcherRun). Building each from git and the store
// API costs seconds under -race, and the suite builds them a hundred times
// over; the R1 budget (docs/testing.md) goes on that repeated setup, not on
// what the tests assert.
//
// So each state is built once, by the first test that needs it, and every
// test copies it into its own temp dir and fixes what names an absolute
// path: the depot's path in the store, the worktrees git registered, the
// origin remote, and the fake gh's call log. The copy is otherwise
// bit-identical to a fresh setup, so every assertion reads the same state:
// sessions are matched by id with no clock the tests trip over, the gh calls
// a landYard caller counts are only its own run's (pr merge is never called
// while the template is built), an integratedYard caller's log has the
// integration's own calls, as a run of the script in the test would have left
// them, and no test asserts a depot's put history.
//
// The template lives until the tests end: borrowers overlap, but need not,
// so no borrower's cleanup removes it; TestMain takes every built root away
// after the run. A build that fails fails its borrowers with the template's
// name instead of building again.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"yardr.dev/yardr/internal/config"
	"yardr.dev/yardr/internal/peer"
	"yardr.dev/yardr/internal/rpc"
	"yardr.dev/yardr/internal/scripttest"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
)

// yardTemplate is one shared setup, built into a root of its own by the
// first test that borrows it.
type yardTemplate struct {
	name  string
	build func(t *testing.T, root string)
	once  sync.Once
	mu    sync.Mutex
	// attempted is set by the build even when it fails the builder, so a
	// later borrower fails fast instead of building again: the failure is
	// systemic, and a rebuild would fail the same way.
	attempted bool
	ok        bool
	root      string
}

// tplRoots are the template roots built in this run, for TestMain to remove
// after it.
var tplRoots struct {
	mu   sync.Mutex
	dirs []string
}

// removeTemplates takes away every template root built in this run. TestMain
// calls it after m.Run.
func removeTemplates() {
	tplRoots.mu.Lock()
	defer tplRoots.mu.Unlock()
	for _, dir := range tplRoots.dirs {
		os.RemoveAll(dir)
	}
	tplRoots.dirs = nil
}

// borrow returns the template's root, built by the first borrower. The root
// lives until the tests end; borrowers copy out of it, and never write to it.
func (tpl *yardTemplate) borrow(t *testing.T) string {
	t.Helper()
	tpl.once.Do(func() {
		tpl.mu.Lock()
		failed := tpl.attempted && !tpl.ok
		tpl.mu.Unlock()
		if failed {
			t.Fatalf("template %s did not build", tpl.name)
		}
		// A build that fails the builder runs this too: t.Fatal ends the
		// goroutine through its defers, so the flag is set either way.
		defer func() {
			tpl.mu.Lock()
			tpl.attempted = true
			tpl.mu.Unlock()
		}()
		root, err := os.MkdirTemp("", "yrtpl")
		if err != nil {
			t.Fatal(err)
		}
		tplRoots.mu.Lock()
		tplRoots.dirs = append(tplRoots.dirs, root)
		tplRoots.mu.Unlock()
		tpl.mu.Lock()
		tpl.root = root
		tpl.mu.Unlock()
		tpl.build(t, root)
		tpl.mu.Lock()
		tpl.ok = true
		tpl.mu.Unlock()
	})
	tpl.mu.Lock()
	defer tpl.mu.Unlock()
	if !tpl.ok {
		t.Fatalf("template %s did not build", tpl.name)
	}
	return tpl.root
}

// serveTemplate serves the template home's store on its socket, as serveYard
// does, and returns its closer. The server is closed before the template is
// copied: a copy of an open store is not a store. Nothing is registered on
// the test: the builder owns the template, and the close is explicit, since
// a cleanup would close it a second time when the builder ends.
//
// The home's pidfile names this process while it serves, as a yardr server's
// does. A yardr whose ping goes unanswered for its one second (a loaded
// machine does that) starts a server of its own unless the pidfile names a
// live one, which it waits for instead: with no pidfile, the one it started
// took the store the moment the closer let go of it and listened on a new
// socket after the close, in a template every borrower then failed to copy
// (yardr-fjj6). wrap puts a handler of a test's own in front of the served one.
func serveTemplate(t *testing.T, home string, wrap ...func(http.Handler) http.Handler) func() {
	t.Helper()
	cfg := &config.Config{Home: home}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	st.SetDaemonChecker(config.DaemonChecker(home))
	// This template owns the home, so a socket file left behind is stale.
	_ = os.Remove(cfg.Socket())
	ln, err := net.Listen("unix", cfg.Socket())
	if err != nil {
		_ = st.Close(context.Background())
		t.Fatal(err)
	}
	if err := os.Chmod(cfg.Socket(), 0o600); err != nil {
		ln.Close()
		_ = st.Close(context.Background())
		t.Fatal(err)
	}
	watch := &reportWatch{}
	reportWatches.Store(home, watch)
	handler := watch.handler(rpc.Handler(st, nil))
	for _, w := range wrap {
		handler = w(handler)
	}
	srv := &http.Server{Handler: handler, ConnContext: rpc.ConnContext}
	go func() { _ = srv.Serve(ln) }()
	recordServer(t, cfg)
	return func() {
		_ = srv.Close()
		reportWatches.Delete(home)
		_ = st.Close(context.Background())
		// What the template's home holds of a server goes with it, and a node
		// that cannot be taken away fails the build here, not its borrowers at
		// their copy. The socket is gone already: closing its listener unlinks
		// it, which is a removal done, not one that failed.
		for _, file := range []string{cfg.PIDFile(), cfg.PIDStart(), cfg.Socket()} {
			if err := os.Remove(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("take %s out of the template's home: %v", filepath.Base(file), err)
			}
		}
		// A server of the CLIs' own is no state to copy: it takes the store and
		// a socket of its own as soon as this one lets go of them. Its log is
		// there before it runs (server.SpawnDetached), so a build that started
		// one fails now, with what it printed, and not as a socket in a copy.
		if _, err := os.Stat(cfg.LogFile()); err == nil {
			log, _ := os.ReadFile(cfg.LogFile())
			t.Fatalf("a yardr server was started on the template's home %s during the build; its log so far:\n%s", home, log)
		}
	}
}

// recordServer writes the pidfile and its start file as a yardr server does
// (server.writePID): this process, and the start time the OS reports for it,
// which tells it from a later process with its pid. They are written before
// any call to the server is made, and the closer takes them away.
func recordServer(t *testing.T, cfg *config.Config) {
	t.Helper()
	if peer.HaveStat() {
		st, err := peer.Describe(os.Getpid())
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cfg.PIDStart(), []byte(strconv.FormatUint(st.Start, 10)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(cfg.PIDFile(), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A yardr that finds the template's server slow to answer waits for it and
// starts none of its own, and a template closed after such a call holds
// nothing of a server. The first ping is held until its caller gives up, which
// is what a loaded machine does to the one second the yardr allows it (the
// request's context ends when the caller hangs up, so no clock stands in the
// test's way). A server started in its place took the store from the closer and
// put a socket back in the template's home (yardr-fjj6), which only a loaded
// run showed: the borrowers' copyTree failed on it.
func TestAStalledPingStartsNoServerOnATemplateHome(t *testing.T) {
	t.Parallel()
	// The home is short, not the test's temp dir: it holds the store's unix
	// socket, whose path the kernel bounds.
	home, err := os.MkdirTemp("", "yr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	var hold atomic.Bool // the next ping goes unanswered
	held := make(chan struct{})
	closeTemplate := serveTemplate(t, home, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/ping" && hold.CompareAndSwap(true, false) {
				close(held)
				<-r.Context().Done()
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	y := &yard{t: t, home: home}
	y.env = append(os.Environ(), "YARDR_HOME="+home, "YARDR_ACTOR=test")
	y.initStore()

	hold.Store(true)
	if got := y.yardr("settings", "get", "yardmaster"); got != "yardmaster" {
		t.Fatalf("settings get yardmaster is %q, want what the template's server holds", got)
	}
	select {
	case <-held:
	default:
		t.Fatal("no ping was held, so the call met no stall")
	}

	// The closer fails the test itself where a server was started; the files
	// are asked for again here, so that a closer that stopped looking cannot
	// let this pass.
	closeTemplate()
	cfg := &config.Config{Home: home}
	for _, file := range []string{cfg.LogFile(), cfg.PIDFile(), cfg.PIDStart(), cfg.Socket()} {
		if _, err := os.Lstat(file); err == nil {
			t.Errorf("%s is left in the template's home after its server closed", filepath.Base(file))
		}
	}
}

// copyTree copies src into dst, which must not exist: directories, regular
// files with their modes and mtimes (so git finds its index valid), and
// symlinks as links. Anything else is a bug, not a state: the templates
// hold no sockets past their close (serveTemplate's closer fails the build
// that leaves one), and no fifos or devices at all. A file copies as a
// file, so a template's loose scripts copy beside its trees.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	if err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(out, info.Mode().Perm())
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(target, out)
		case info.Mode().IsRegular():
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if err := os.WriteFile(out, b, info.Mode().Perm()); err != nil {
				return err
			}
			return os.Chtimes(out, info.ModTime(), info.ModTime())
		default:
			return fmt.Errorf("copyTree: %s is %v", path, info.Mode())
		}
	}); err != nil {
		t.Fatal(err)
	}
}

// relinkWorktrees makes the linked worktrees wts of a copied repository the
// copy's own. A copy of a repository and of its worktree is bit-identical, so
// the worktree's .git file still names the template's admin directory and the
// admin directory still names the template's worktree: a commit made in the
// copy would land in the shared template. Two files tie a linked worktree to
// its repository, and this writes both as git worktree add does: the id is
// read from the copied .git file, as git infers it, and the paths are
// resolved, as git writes them (macOS's temp dir is behind a symlink).
//
// git worktree repair is no way to do it: before git 2.48 it trusts the .git
// file, so it rewrites the template's links to name the copy and leaves the
// copy naming the template, and the next borrower copies a template that
// names the last borrower's gone temp dir (yardr-9r6w).
func relinkWorktrees(t *testing.T, repo string, wts ...string) {
	t.Helper()
	repo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	write := func(file, content string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(content+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, wt := range wts {
		wt, err := filepath.EvalSymlinks(wt)
		if err != nil {
			t.Fatal(err)
		}
		dotgit := filepath.Join(wt, ".git")
		b, err := os.ReadFile(dotgit)
		if err != nil {
			t.Fatal(err)
		}
		old, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir: ")
		if !ok {
			t.Fatalf("relink %s: %s is not a gitfile: %q", wt, dotgit, b)
		}
		admin := filepath.Join(repo, ".git", "worktrees", filepath.Base(old))
		write(dotgit, "gitdir: "+admin)
		write(filepath.Join(admin, "gitdir"), dotgit)
	}
}

// A repository and its linked worktree, copied and relinked, belong to the
// copy: its links name the copy, the template's are as they were, and a commit
// made in the copy's worktree lands on the copy's branch, not the template's.
// Every borrower relies on it. git before 2.48 (Debian 13 ships 2.47) left the
// copy naming the template, and most of this package's tests failed there
// (yardr-9r6w).
func TestRelinkedWorktreesBelongToTheCopyNotTheTemplate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tplRepo, tplWT := filepath.Join(dir, "tpl", "repo"), filepath.Join(dir, "tpl", "wt")
	cpRepo, cpWT := filepath.Join(dir, "copy", "repo"), filepath.Join(dir, "copy", "wt")
	watcherGit(t, dir, "init", "-q", "-b", "main", tplRepo)
	watcherGit(t, tplRepo, "commit", "-q", "--allow-empty", "-m", "base")
	watcherGit(t, tplRepo, "branch", "yard/r-1")
	watcherGit(t, tplRepo, "worktree", "add", "-q", tplWT, "yard/r-1")
	links := func(repo, wt string) [2]string {
		dotgit, err := os.ReadFile(filepath.Join(wt, ".git"))
		if err != nil {
			t.Fatal(err)
		}
		admin, err := os.ReadFile(filepath.Join(repo, ".git", "worktrees", "wt", "gitdir"))
		if err != nil {
			t.Fatal(err)
		}
		return [2]string{string(dotgit), string(admin)}
	}
	tplLinks := links(tplRepo, tplWT)
	copyTree(t, tplRepo, cpRepo)
	copyTree(t, tplWT, cpWT)
	relinkWorktrees(t, cpRepo, cpWT)

	// Resolved, since git writes the links so and the temp dir may sit behind a
	// symlink.
	realCpRepo, err := filepath.EvalSymlinks(cpRepo)
	if err != nil {
		t.Fatal(err)
	}
	realCpWT, err := filepath.EvalSymlinks(cpWT)
	if err != nil {
		t.Fatal(err)
	}
	want := [2]string{
		"gitdir: " + filepath.Join(realCpRepo, ".git", "worktrees", "wt") + "\n",
		filepath.Join(realCpWT, ".git") + "\n",
	}
	if got := links(cpRepo, cpWT); got != want {
		t.Errorf("the copy's links are %q, want %q", got, want)
	}
	if got := links(tplRepo, tplWT); got != tplLinks {
		t.Errorf("the template's links are now %q, were %q", got, tplLinks)
	}
	// What git itself makes of them: the copy's repository registers the copy's
	// worktree, and a commit made there stays in the copy.
	var listed []string
	for _, line := range strings.Split(watcherGit(t, cpRepo, "worktree", "list", "--porcelain"), "\n") {
		if path, ok := strings.CutPrefix(line, "worktree "); ok {
			listed = append(listed, path)
		}
	}
	if wantListed := []string{realCpRepo, realCpWT}; !slices.Equal(listed, wantListed) {
		t.Errorf("the copy's repository lists the worktrees %q, want %q", listed, wantListed)
	}
	watcherGit(t, cpWT, "commit", "-q", "--allow-empty", "-m", "in the copy")
	if got := watcherGit(t, cpRepo, "log", "-1", "--format=%s", "yard/r-1"); got != "in the copy" {
		t.Errorf("the copy's yard/r-1 is at %q after a commit in its worktree", got)
	}
	if got := watcherGit(t, tplRepo, "log", "-1", "--format=%s", "yard/r-1"); got != "base" {
		t.Errorf("the template's yard/r-1 is at %q, a commit in the copy's worktree reached it", got)
	}
}

var yardTpl = &yardTemplate{name: "yard", build: buildYardTpl}

// buildYardTpl is newYard's setup, once, into root: a yard with depot r over
// a fresh repository and bead r-1 in approved on yard/r-1 in its worktree.
func buildYardTpl(t *testing.T, root string) {
	t.Helper()
	home, repo := filepath.Join(root, "home"), filepath.Join(root, "repo")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	y := &yard{t: t, home: home, repo: repo, wt: filepath.Join(home, "worktrees", "r", "r-1")}
	y.env = append(os.Environ(), "YARDR_HOME="+home, "YARDR_ACTOR=test")
	y.git(".", "init", "-q", "-b", "main", y.repo)
	y.commit(y.repo, "a.txt", "one\n")
	close := serveTemplate(t, home)
	defer close()
	y.initStore()
	y.putDepot("r", y.repo, "main", "test ! -e RED")
	y.putFlow("f", flow)
	y.pointFlow("r", "", "f")
	y.createBead("work", "r-1", "r", "", "approved")
	y.git(y.repo, "branch", "yard/r-1")
	y.git(y.repo, "worktree", "add", "-q", y.wt, "yard/r-1")
}

// borrowYard is newYard from the template: the same yard, copied into the
// test's own temp dir, with the worktree relinked to the copy and the depot
// pointing at the copy.
func borrowYard(t *testing.T) *yard {
	t.Helper()
	t.Parallel()
	root := yardTpl.borrow(t)
	// The home is short, not the test's temp dir: it holds the store's
	// unix socket, whose path the kernel bounds.
	home, err := os.MkdirTemp("", "yr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	repo := filepath.Join(t.TempDir(), "repo")
	copyTree(t, filepath.Join(root, "home"), home)
	copyTree(t, filepath.Join(root, "repo"), repo)
	y := &yard{t: t, home: home, repo: repo, wt: filepath.Join(home, "worktrees", "r", "r-1")}
	y.env = append(os.Environ(), "YARDR_HOME="+home, "YARDR_ACTOR=test")
	relinkWorktrees(t, y.repo, y.wt)
	serveYard(t, home)
	y.putDepot("r", y.repo, "main", "test ! -e RED")
	return y
}

var trainTpl = &yardTemplate{name: "train", build: buildTrainTpl}

// buildTrainTpl is newTrainYard's setup, once, into root: a yard with depot
// r over a fresh repository pushed to a bare origin, train r-e in open, and
// its children r-1 and r-2 in approved, each one commit ahead of the train's
// branch in its worktree.
func buildTrainTpl(t *testing.T, root string) {
	t.Helper()
	home, repo := filepath.Join(root, "home"), filepath.Join(root, "repo")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	y := &yard{t: t, home: home, repo: repo}
	y.env = append(os.Environ(), "YARDR_HOME="+home, "YARDR_ACTOR=test")
	y.git(".", "init", "-q", "-b", "main", y.repo)
	y.commit(y.repo, "a.txt", "one\n")
	remote := filepath.Join(root, "origin.git")
	y.git(".", "init", "-q", "--bare", remote)
	y.git(y.repo, "remote", "add", "origin", remote)
	y.git(y.repo, "push", "-q", "origin", "main")
	close := serveTemplate(t, home)
	defer close()
	y.initStore()
	y.putDepot("r", y.repo, "main", "test ! -e RED")
	y.putFlow("code", childFlow)
	y.pointFlow("r", "", "code")
	y.putFlowFile("yard.train", storetest.RepoFlow(t, "yardr.train"))
	y.pointFlow("r", "train", "yard.train")
	y.createBead("the train", "r-e", "r", "train", "open")
	y.git(y.repo, "branch", "yard/r-e")
	for i, id := range []string{"r-1", "r-2"} {
		y.createBead("child", id, "r", "", "approved")
		y.addParent("r-e", id)
		wt := filepath.Join(home, "worktrees", "r", id)
		y.git(y.repo, "worktree", "add", "-q", "-b", "yard/"+id, wt, "yard/r-e")
		y.commit(wt, "child"+string(rune('1'+i))+".txt", id+"\n")
	}
}

// borrowTrainTpl is a train-shaped template copied into the test's own temp
// dir: the worktrees relinked to the copy, the origin set to the copied remote,
// and the depot pointing at the copy with the test's own test command.
func borrowTrainTpl(t *testing.T, tpl *yardTemplate, check string) *yard {
	t.Helper()
	t.Parallel()
	root := tpl.borrow(t)
	// The home is short, not the test's temp dir: it holds the store's
	// unix socket, whose path the kernel bounds.
	home, err := os.MkdirTemp("", "yr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	repo := filepath.Join(t.TempDir(), "repo")
	origin := filepath.Join(t.TempDir(), "origin.git")
	copyTree(t, filepath.Join(root, "home"), home)
	copyTree(t, filepath.Join(root, "repo"), repo)
	copyTree(t, filepath.Join(root, "origin.git"), origin)
	y := &yard{t: t, home: home, repo: repo}
	y.env = append(os.Environ(), "YARDR_HOME="+home, "YARDR_ACTOR=test")
	y.push = "origin"
	relinkWorktrees(t, y.repo,
		filepath.Join(home, "worktrees", "r", "r-1"),
		filepath.Join(home, "worktrees", "r", "r-2"))
	y.git(y.repo, "remote", "set-url", "origin", origin)
	serveYard(t, home)
	y.putDepot("r", y.repo, "main", check)
	return y
}

// borrowTrain is newTrainYard from the template.
func borrowTrain(t *testing.T, check string) *yard {
	t.Helper()
	return borrowTrainTpl(t, trainTpl, check)
}

var landedTpl = &yardTemplate{name: "landed", build: buildLandedTpl}

// buildLandedTpl is landedTrainYard's setup, once, into root: the train
// template with both children landed on the train's branch. The runs' logs
// are dropped: they belong to the template, and a builder that fails later
// must not show them as its own.
func buildLandedTpl(t *testing.T, root string) {
	t.Helper()
	train := trainTpl.borrow(t)
	home := filepath.Join(root, "home")
	repo := filepath.Join(root, "repo")
	origin := filepath.Join(root, "origin.git")
	copyTree(t, filepath.Join(train, "home"), home)
	copyTree(t, filepath.Join(train, "repo"), repo)
	copyTree(t, filepath.Join(train, "origin.git"), origin)
	y := &yard{t: t, home: home, repo: repo}
	y.env = append(os.Environ(), "YARDR_HOME="+home, "YARDR_ACTOR=test")
	y.push = "origin"
	relinkWorktrees(t, y.repo,
		filepath.Join(home, "worktrees", "r", "r-1"),
		filepath.Join(home, "worktrees", "r", "r-2"))
	y.git(y.repo, "remote", "set-url", "origin", origin)
	close := serveTemplate(t, home)
	defer close()
	y.putDepot("r", y.repo, "main", "true")
	y.exec("merge-local", "assembly", "r-1")
	y.exec("merge-local", "assembly", "r-2")
	y.runs = nil
}

// borrowLanded is landedTrainYard from the template, with check as the depot's
// test command for the runs that follow. The children landed under the
// template's own, true: no test reads what that left in their notes, and the
// tests that borrow the yard assert on the train.
func borrowLanded(t *testing.T, check string) *yard {
	t.Helper()
	return borrowTrainTpl(t, landedTpl, check)
}

var integratedTpl = &yardTemplate{name: "integrated", build: buildIntegratedTpl}

// buildIntegratedTpl is integratedYard's setup, once, into root: the train
// template with both children landed, the train integrated and pushed, and its
// pull request open and the train bound to it, in stage review. It layers on
// the train template, not the landed one: the children land with YARDR_PR=1 and
// the depot's own test command, as the template must. The fake gh lives in the
// template, not the builder's temp dir, which ends with the builder.
func buildIntegratedTpl(t *testing.T, root string) {
	t.Helper()
	train := trainTpl.borrow(t)
	home := filepath.Join(root, "home")
	repo := filepath.Join(root, "repo")
	origin := filepath.Join(root, "origin.git")
	gh := filepath.Join(root, "gh")
	copyTree(t, filepath.Join(train, "home"), home)
	copyTree(t, filepath.Join(train, "repo"), repo)
	copyTree(t, filepath.Join(train, "origin.git"), origin)
	if err := os.MkdirAll(filepath.Join(gh, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	scripttest.Write(t, filepath.Join(gh, "bin", "gh"), fakeGh)
	fakeGhs.Store(t, gh)
	t.Cleanup(func() { fakeGhs.Delete(t) })
	y := &yard{t: t, home: home, repo: repo}
	y.env = append(os.Environ(), "YARDR_HOME="+home, "YARDR_ACTOR=test")
	y.push = "origin"
	y.opts = "YARDR_PR=1"
	relinkWorktrees(t, y.repo,
		filepath.Join(home, "worktrees", "r", "r-1"),
		filepath.Join(home, "worktrees", "r", "r-2"))
	y.git(y.repo, "remote", "set-url", "origin", origin)
	close := serveTemplate(t, home)
	defer close()
	y.putDepot("r", y.repo, "main", "test ! -e RED")
	y.exec("merge-local", "assembly", "r-1")
	y.exec("merge-local", "assembly", "r-2")
	y.exec("integrate-train", "yard-integrators", "r-e")
	if e := y.beadOf("r-e"); e.Stage != "review" {
		t.Fatalf("train in %s, want review", e.Stage)
	}
	y.runs = nil
}

var landTpl = &yardTemplate{name: "land", build: buildLandTpl}

// buildLandTpl is landYard's setup, once, into root: the integrated template
// with the train reviewed twice and pushed by pr-train, in stage land.
func buildLandTpl(t *testing.T, root string) {
	t.Helper()
	integrated := integratedTpl.borrow(t)
	home := filepath.Join(root, "home")
	repo := filepath.Join(root, "repo")
	origin := filepath.Join(root, "origin.git")
	gh := filepath.Join(root, "gh")
	copyTree(t, filepath.Join(integrated, "home"), home)
	copyTree(t, filepath.Join(integrated, "repo"), repo)
	copyTree(t, filepath.Join(integrated, "origin.git"), origin)
	copyTree(t, filepath.Join(integrated, "gh"), gh)
	fakeGhs.Store(t, gh)
	t.Cleanup(func() { fakeGhs.Delete(t) })
	y := &yard{t: t, home: home, repo: repo}
	y.env = append(os.Environ(), "YARDR_HOME="+home, "YARDR_ACTOR=test")
	y.push = "origin"
	y.opts = "YARDR_PR=1"
	relinkWorktrees(t, y.repo,
		filepath.Join(home, "worktrees", "r", "r-1"),
		filepath.Join(home, "worktrees", "r", "r-2"),
		filepath.Join(home, "worktrees", "r", "r-e"))
	y.git(y.repo, "remote", "set-url", "origin", origin)
	close := serveTemplate(t, home)
	defer close()
	y.putDepot("r", y.repo, "main", "test ! -e RED")
	y.yardr("bead", "done", "r-e")
	y.yardr("bead", "done", "r-e")
	y.exec("pr-train", "yard-pr", "r-e")
	y.yardr("bead", "done", "r-e")
	if e := y.beadOf("r-e"); e.Stage != "land" {
		t.Fatalf("train in %s, want land", e.Stage)
	}
	y.runs = nil
}

// borrowLand is landYard from the template: the same yard and fake gh,
// copied, with the gh calls the template made truncated away. The callers
// count only their own run's calls, and the template never called pr merge.
func borrowLand(t *testing.T) (*yard, string) {
	t.Helper()
	y, gh := borrowShipped(t, landTpl, "r-1", "r-2")
	if err := os.WriteFile(filepath.Join(gh, "calls"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return y, gh
}

// borrowIntegrated is integratedYard from the template: the same yard and fake
// gh, copied, with the gh calls of the integration that made it left in the
// log, as a run of integrate-train in the test would have left them.
func borrowIntegrated(t *testing.T) (*yard, string) {
	t.Helper()
	return borrowShipped(t, integratedTpl, "r-1", "r-2", "r-e")
}

// borrowShipped copies a template with a pull request in it (the root's home,
// repository, remote and fake gh) into the test's own temp dir, relinks the
// worktrees named and points the depot and the remote at the copy.
func borrowShipped(t *testing.T, tpl *yardTemplate, worktrees ...string) (*yard, string) {
	t.Helper()
	t.Parallel()
	root := tpl.borrow(t)
	// The home is short, not the test's temp dir: it holds the store's
	// unix socket, whose path the kernel bounds.
	home, err := os.MkdirTemp("", "yr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	repo := filepath.Join(t.TempDir(), "repo")
	origin := filepath.Join(t.TempDir(), "origin.git")
	gh := filepath.Join(t.TempDir(), "gh")
	copyTree(t, filepath.Join(root, "home"), home)
	copyTree(t, filepath.Join(root, "repo"), repo)
	copyTree(t, filepath.Join(root, "origin.git"), origin)
	copyTree(t, filepath.Join(root, "gh"), gh)
	y := &yard{t: t, home: home, repo: repo}
	y.env = append(os.Environ(), "YARDR_HOME="+home, "YARDR_ACTOR=test")
	y.push = "origin"
	y.opts = "YARDR_PR=1"
	wts := make([]string, len(worktrees))
	for i, id := range worktrees {
		wts[i] = filepath.Join(home, "worktrees", "r", id)
	}
	relinkWorktrees(t, y.repo, wts...)
	y.git(y.repo, "remote", "set-url", "origin", origin)
	serveYard(t, home)
	y.putDepot("r", y.repo, "main", "test ! -e RED")
	fakeGhs.Store(t, gh)
	t.Cleanup(func() { fakeGhs.Delete(t) })
	return y, gh
}

var watcherTpl = &yardTemplate{name: "watcher", build: buildWatcherTpl}

// buildWatcherTpl is newWatcherRun's repository, once, into root: base main
// with branch yard/r-1 one empty commit ahead in its worktree, the stub
// yardr and the recording sleep. The run's own temp dir, record and fds stay
// per test.
func buildWatcherTpl(t *testing.T, root string) {
	t.Helper()
	repo, wt := filepath.Join(root, "repo"), filepath.Join(root, "wt")
	watcherGit(t, root, "init", "-q", "-b", "main", repo)
	watcherGit(t, repo, "commit", "-q", "--allow-empty", "-m", "base")
	watcherGit(t, repo, "branch", "yard/r-1")
	watcherGit(t, repo, "worktree", "add", "-q", wt, "yard/r-1")
	watcherGit(t, wt, "commit", "-q", "--allow-empty", "-m", "bead")
	stub := filepath.Join(root, "stub")
	scripttest.Write(t, stub, "#!/bin/sh\ncat >/dev/null 2>&1; exit 0\n")
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := "#!/bin/sh\n{ echo \"argv: $*\"; ls /dev/fd; } >\"$FAKE_SLEEP_RECORD.$$\"\nexec /bin/sleep \"$@\"\n"
	scripttest.Write(t, filepath.Join(bin, "sleep"), fake)
}
