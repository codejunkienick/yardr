package serveryard

// The binaries the tests run, built once.
//
// Every test that sets a yard up runs yardr from the tree and the fake herdr
// beside it, and each used to build both for itself: the pair five times over
// in one run of the package, though the tree does not change under a run. So
// each binary is built once, by the first test that needs it, into a root of
// its own, and every test is lent it. That is the shape of the template yards
// of contrib/assembly (fixture_test.go there): the root lives until the tests
// end, TestMain takes it away after them, and a build that fails fails its
// borrowers with the template's name instead of building again.
//
// A test is lent the file itself, linked at the path the script finds it by,
// D/bin/yardr and D/bin/herdr, and not a copy of it. On macOS the first run of
// an executable file that was not there before waits behind the machine's
// other first runs (yardr-c3nm; internal/scripttest has the measurement), which
// is why the suite runs every file it makes once, where no fuse times it. A
// copy is such a file, so each test would run its copies once more, and the
// first runs of a loaded machine are what the suite's time goes on. A link is
// the file that was run when it was built, so that one run serves every test;
// the hook cache of internal/runner links for the same reason, and writes the
// bytes where it cannot, as a yard here is given a copy and runs it once. A
// link is also a regular file at the same path as before, which a symlink is
// not: yardr writes its own path into the service files it makes
// (os.Executable), and on Linux that is the symlink's target.
//
// A link shares the file, so nothing opens it for writing: the template makes
// it read-only, which stops a write by anyone but root, and a test that wants a
// binary of its own makes a new file and renames it into place.

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"testing"
)

// binTemplate is one binary of the suite, built into a root of its own by the
// first test that borrows it.
type binTemplate struct {
	name string
	// build makes the binary at path and runs it once.
	build func(t *testing.T, path string)
	// run runs a file of the binary once, where no fuse times it: the build
	// does it for the template's own, and a yard that is lent a copy for the
	// copy, which is a file that was not run before.
	run  func(path string)
	once sync.Once
	mu   sync.Mutex
	// path is the built file. It is set only by a build that went through, so a
	// later borrower finds it empty after a build that failed its builder, and
	// fails fast: the failure is systemic, and a rebuild would fail the same way.
	path string
}

// goBinary is the template of what the package pkg builds, run once with args.
func goBinary(name, pkg string, args ...string) *binTemplate {
	tpl := &binTemplate{name: name}
	// version needs no home: the one it is given is only where it would write.
	tpl.run = func(path string) { warm(path, []string{"HOME=" + filepath.Dir(path)}, args...) }
	tpl.build = func(t *testing.T, path string) {
		goBuild(t, repoRoot(t), path, pkg)
		tpl.run(path)
	}
	return tpl
}

// yardrTpl and herdrTpl are the two binaries every yard of the suite runs. The
// herdr stand-in, run with no state and no argument, answers nothing and logs
// nothing.
var (
	yardrTpl = goBinary("yardr", "yardr.dev/yardr", "version")
	herdrTpl = goBinary("herdr", "yardr.dev/yardr/contrib/server-yard/fake")
)

// linkFile is how a yard is lent a binary. A test replaces it with one that
// fails, to see the copy that stands in where a link cannot be made.
var linkFile = os.Link

// tplRoots are the roots built in this run, for TestMain to remove after it.
var tplRoots struct {
	mu   sync.Mutex
	dirs []string
}

// removeTemplates takes away every root built in this run. TestMain calls it
// after m.Run.
func removeTemplates() {
	tplRoots.mu.Lock()
	defer tplRoots.mu.Unlock()
	for _, dir := range tplRoots.dirs {
		os.RemoveAll(dir)
	}
	tplRoots.dirs = nil
}

// repoRoot is the repository the tests run in.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// borrow returns the path of the binary, built by the first borrower. The file
// lives until the tests end; borrowers run it, and never write to it.
func (tpl *binTemplate) borrow(t *testing.T) string {
	t.Helper()
	tpl.once.Do(func() {
		// In /tmp, as the yards are, so that a link from one to the other
		// never crosses a filesystem.
		root, err := os.MkdirTemp("/tmp", "syb.")
		if err != nil {
			t.Fatal(err)
		}
		tplRoots.mu.Lock()
		tplRoots.dirs = append(tplRoots.dirs, root)
		tplRoots.mu.Unlock()
		path := filepath.Join(root, tpl.name)
		// A build that fails the builder ends this through t.Fatal's defers,
		// and Do counts it as done all the same, with no path set.
		tpl.build(t, path)
		if err := os.Chmod(path, 0o555); err != nil {
			t.Fatal(err)
		}
		tpl.mu.Lock()
		tpl.path = path
		tpl.mu.Unlock()
	})
	tpl.mu.Lock()
	defer tpl.mu.Unlock()
	if tpl.path == "" {
		t.Fatalf("template %s did not build", tpl.name)
	}
	return tpl.path
}

// lend puts the template's binary at path, in the bin of a test's yard: a link
// to the file that was run, or, where none can be made, a copy of it, run once.
func (tpl *binTemplate) lend(t *testing.T, path string) {
	t.Helper()
	from := tpl.borrow(t)
	if linkFile(from, path) == nil {
		return
	}
	if err := copyExecutable(from, path); err != nil {
		t.Fatalf("lending %s to a yard: %v", tpl.name, err)
	}
	tpl.run(path)
}

// copyExecutable makes path an executable of its own with the bytes of from. It
// holds syscall.ForkLock for reading from the open to the close: Linux does not
// start a file while any process holds it open for writing (text file busy), and
// a fork made beside this write carries its descriptor until the child execs,
// which the run that follows would find (internal/scripttest, writeLauncher).
func copyExecutable(from, path string) error {
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		return err
	}
	_, werr := f.Write(b)
	return cmp.Or(werr, f.Close())
}

// builds counts the go builds of this run by package, which TestMain holds to
// one each: the templates are the only builders, and a test that builds a
// binary for itself puts the cost back where nothing else would say so.
var builds = struct {
	mu sync.Mutex
	n  map[string]int
}{n: map[string]int{}}

// builtAgain names the packages counts holds more than one build of, with how
// many, in order.
func builtAgain(counts map[string]int) []string {
	var again []string
	for pkg, n := range counts {
		if n > 1 {
			again = append(again, fmt.Sprintf("%s (%d builds)", pkg, n))
		}
	}
	slices.Sort(again)
	return again
}

// checkBuilds is builtAgain of this run's builds, for TestMain to fail the
// package on after m.Run.
func checkBuilds() []string {
	builds.mu.Lock()
	defer builds.mu.Unlock()
	return builtAgain(builds.n)
}

// TestABinaryIsBuiltOnceAndLentAsALinkOrACopy pins what a yard relies on of a
// template, with a build that makes a file and nothing else: however many
// borrow it, it is built once; a yard is lent the template's own file,
// read-only, at the path asked for, so that the one run that warmed it warmed it
// for every yard; and where no link can be made the yard gets a file of its
// own with the same bytes, run once.
func TestABinaryIsBuiltOnceAndLentAsALinkOrACopy(t *testing.T) {
	made := 0
	var ran []string
	tpl := &binTemplate{name: "prog",
		build: func(t *testing.T, path string) {
			made++
			if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
		run: func(path string) { ran = append(ran, path) },
	}
	built := tpl.borrow(t)
	t.Run("a second test borrows it", func(t *testing.T) {
		if got := tpl.borrow(t); got != built {
			t.Errorf("a second borrower got %s, want the first's %s", got, built)
		}
	})
	dir, err := os.MkdirTemp("/tmp", "sy.")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Cleanup(func() { linkFile = os.Link })
	own, err := os.Stat(built)
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range []struct {
		name string
		link func(old, new string) error
		same bool // what is lent is the template's own file
		ran  int  // runs of what is lent
	}{
		{"a link", os.Link, true, 0},
		{"where no link can be made", func(string, string) error { return errors.New("no links here") }, false, 1},
	} {
		linkFile = c.link
		ran = nil
		path := filepath.Join(dir, fmt.Sprint(i))
		tpl.lend(t, path)
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := os.SameFile(own, fi); got != c.same {
			t.Errorf("%s: what is lent is the template's file: %v, want %v", c.name, got, c.same)
		}
		// A link shares the file, so it is as read-only as the template made it;
		// a copy is the yard's own, and runs.
		perm := fi.Mode().Perm()
		if perm&0o100 == 0 {
			t.Errorf("%s: what is lent is mode %04o, which does not run", c.name, perm)
		}
		if got := perm&0o222 == 0; got != c.same {
			t.Errorf("%s: what is lent is read-only: %v (mode %04o), want %v", c.name, got, perm, c.same)
		}
		if got, want := string(mustRead(t, path)), string(mustRead(t, built)); got != want {
			t.Errorf("%s: what is lent holds %q, want %q", c.name, got, want)
		}
		if len(ran) != c.ran || (c.ran > 0 && ran[0] != path) {
			t.Errorf("%s: ran %q after the lend, want %d run of %s", c.name, ran, c.ran, path)
		}
	}
	if made != 1 {
		t.Errorf("the build ran %d times for the borrowers and two lends, want once", made)
	}
}

// TestBuiltAgainNamesWhatWasBuiltMoreThanOnce pins what TestMain fails the
// package on: a package built more than once in a run, with how many times.
func TestBuiltAgainNamesWhatWasBuiltMoreThanOnce(t *testing.T) {
	for _, c := range []struct {
		name   string
		counts map[string]int
		want   []string
	}{
		{"nothing built", nil, nil},
		{"each once", map[string]int{"yardr.dev/yardr": 1, "yardr.dev/yardr/contrib/server-yard/fake": 1}, nil},
		{"one built for each test", map[string]int{"yardr.dev/yardr": 5, "yardr.dev/yardr/contrib/server-yard/fake": 1},
			[]string{"yardr.dev/yardr (5 builds)"}},
		{"both again, in order", map[string]int{"yardr.dev/yardr": 2, "yardr.dev/yardr/contrib/server-yard/fake": 6},
			[]string{"yardr.dev/yardr (2 builds)", "yardr.dev/yardr/contrib/server-yard/fake (6 builds)"}},
	} {
		if got := builtAgain(c.counts); !slices.Equal(got, c.want) {
			t.Errorf("%s: builtAgain = %q, want %q", c.name, got, c.want)
		}
	}
}
