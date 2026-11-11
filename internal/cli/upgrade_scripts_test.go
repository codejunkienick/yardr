package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"yardr.dev/yardr/internal/config"
	"yardr.dev/yardr/internal/store"
)

// scriptSource is the path in the checkout of the source named name.
func scriptSource(t *testing.T, name string) string {
	t.Helper()
	for _, rel := range upgradeScriptSources {
		if filepath.Base(rel) == name {
			return rel
		}
	}
	t.Fatalf("no script source is named %s", name)
	return ""
}

// scriptsCheckout is a depot checkout holding these scripts, by name, at the
// paths the upgrade reads them from.
func scriptsCheckout(t *testing.T, bodies map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range bodies {
		path := filepath.Join(dir, filepath.FromSlash(scriptSource(t, name)))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// scriptsHome is a yard's home whose scripts directory holds these, by name.
// They are not executable, so that a replacement which makes them so shows.
func scriptsHome(t *testing.T, bodies map[string]string) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, "scripts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range bodies {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func scriptText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func scriptStat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

// scriptFiles are the names in a yard's scripts directory, to see that a run
// left nothing but what it should.
func scriptFiles(t *testing.T, home string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(home, "scripts"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func wantFiles(t *testing.T, home string, want ...string) {
	t.Helper()
	if got := scriptFiles(t, home); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("%s/scripts holds %v, want %v", home, got, want)
	}
}

const (
	scriptOld   = "#!/bin/sh\necho old\n"
	scriptNew   = "#!/bin/sh\necho new\n"
	scriptBroke = "#!/bin/sh\nif true; then\n  echo unfinished\n"
)

// A script whose bytes are the checkout's is left alone: the same file, not
// rewritten with the same bytes, and no copy kept of what did not change.
func TestUpgradeScriptsEqualIsUntouched(t *testing.T) {
	t.Parallel()
	checkout := scriptsCheckout(t, map[string]string{"merge-local": scriptNew})
	home := scriptsHome(t, map[string]string{"merge-local": scriptNew})
	live := filepath.Join(home, "scripts", "merge-local")
	before := scriptStat(t, live)

	var warn strings.Builder
	s := upgradeScriptsInstall(context.Background(), checkout, []string{home}, &warn)

	after := scriptStat(t, live)
	if !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime()) || after.Mode().Perm() != 0o644 {
		t.Fatalf("the equal script was rewritten: %v -> %v", before, after)
	}
	wantFiles(t, home, "merge-local")
	if got := s.summary(); got != "current" || len(s.replaced) != 0 || warn.Len() != 0 {
		t.Fatalf("summary %q, replaced %v, warned %q; want current, none, nothing", got, s.replaced, warn.String())
	}
}

// A script that differs is replaced by a new file, mode 0755, with the old
// bytes kept as name.previous. The old file itself is not written to: a
// landing that is running holds it open and sh reads it as it goes.
func TestUpgradeScriptsDifferingIsReplaced(t *testing.T) {
	t.Parallel()
	checkout := scriptsCheckout(t, map[string]string{"merge-local": scriptNew})
	home := scriptsHome(t, map[string]string{"merge-local": scriptOld})
	live := filepath.Join(home, "scripts", "merge-local")
	running, err := os.Open(live)
	if err != nil {
		t.Fatal(err)
	}
	defer running.Close()
	before := scriptStat(t, live)

	var warn strings.Builder
	s := upgradeScriptsInstall(context.Background(), checkout, []string{home}, &warn)

	after := scriptStat(t, live)
	if got := scriptText(t, live); got != scriptNew {
		t.Fatalf("merge-local = %q, want the checkout's", got)
	}
	if after.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v, want 0755", after.Mode().Perm())
	}
	if os.SameFile(before, after) {
		t.Fatalf("merge-local is the same file: it was written in place")
	}
	if got := scriptText(t, live+".previous"); got != scriptOld {
		t.Fatalf("merge-local.previous = %q, want the replaced bytes", got)
	}
	if old, err := io.ReadAll(running); err != nil || string(old) != scriptOld {
		t.Fatalf("the open file now reads %q, %v: the running landing's script changed under it", old, err)
	}
	wantFiles(t, home, "merge-local", "merge-local.previous")
	if got := s.summary(); got != "merge-local refreshed" || warn.Len() != 0 {
		t.Fatalf("summary %q, warned %q", got, warn.String())
	}
}

// Only a script the yard already has is refreshed: one it lacks is not
// added, and a home with no scripts directory does not get one.
func TestUpgradeScriptsAbsentIsNotAdded(t *testing.T) {
	t.Parallel()
	checkout := scriptsCheckout(t, map[string]string{
		"merge-local":     scriptNew,
		"integrate-train": scriptNew,
		"deploy-dokku":    scriptNew,
	})
	partial := scriptsHome(t, map[string]string{"integrate-train": scriptOld})
	bare := t.TempDir()

	var warn strings.Builder
	s := upgradeScriptsInstall(context.Background(), checkout, []string{partial, bare}, &warn)

	wantFiles(t, partial, "integrate-train", "integrate-train.previous")
	if _, err := os.Stat(filepath.Join(bare, "scripts")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a home with no scripts directory got one: %v", err)
	}
	if got := s.summary(); got != "integrate-train refreshed" || warn.Len() != 0 {
		t.Fatalf("summary %q, warned %q", got, warn.String())
	}
}

// A checkout without a script has nothing to give: the yard's copy is kept
// as it is and nothing is said, since not every depot is a full yardr tree.
func TestUpgradeScriptsMissingFromTheCheckout(t *testing.T) {
	t.Parallel()
	checkout := scriptsCheckout(t, nil)
	home := scriptsHome(t, map[string]string{"merge-local": scriptOld})

	var warn strings.Builder
	s := upgradeScriptsInstall(context.Background(), checkout, []string{home}, &warn)

	if got := scriptText(t, filepath.Join(home, "scripts", "merge-local")); got != scriptOld {
		t.Fatalf("merge-local = %q, want it kept", got)
	}
	wantFiles(t, home, "merge-local")
	if got := s.summary(); got != "current" || warn.Len() != 0 {
		t.Fatalf("summary %q, warned %q", got, warn.String())
	}
}

// A new file that the shell refuses is not installed: the yard's script is
// kept as it was (the same file, no copy, no stray temporary), the script is
// named with the shell's words, and the others are refreshed all the same.
func TestUpgradeScriptsSyntaxErrorIsKept(t *testing.T) {
	t.Parallel()
	checkout := scriptsCheckout(t, map[string]string{"merge-local": scriptBroke, "pr-merge": scriptNew})
	home := scriptsHome(t, map[string]string{"merge-local": scriptOld, "pr-merge": scriptOld})
	live := filepath.Join(home, "scripts", "merge-local")
	before := scriptStat(t, live)

	var warn strings.Builder
	s := upgradeScriptsInstall(context.Background(), checkout, []string{home}, &warn)

	if got := scriptText(t, live); got != scriptOld || !os.SameFile(before, scriptStat(t, live)) {
		t.Fatalf("merge-local = %q: the script that fails sh -n replaced the yard's", got)
	}
	wantFiles(t, home, "merge-local", "pr-merge", "pr-merge.previous")
	// The shell's own words differ by shell (bash says "syntax error", dash
	// "Syntax error"), so the case is not part of what is asked.
	for _, want := range []string{live, "kept as it is", "sh -n", "syntax error"} {
		if !strings.Contains(strings.ToLower(warn.String()), strings.ToLower(want)) {
			t.Fatalf("the warning lacks %q:\n%s", want, warn.String())
		}
	}
	if got := s.summary(); got != "pr-merge refreshed" {
		t.Fatalf("summary %q, want the one that was refreshed", got)
	}

	// Where nothing could be refreshed the line does not say "current", and
	// does not end in "refreshed", which merge-local would read names from.
	only := scriptsHome(t, map[string]string{"merge-local": scriptOld})
	got := upgradeScriptsInstall(context.Background(), checkout, []string{only}, io.Discard).summary()
	if got != "kept as they were, see the warnings above" || strings.HasSuffix(got, " refreshed") {
		t.Fatalf("summary %q, want the one that says they were kept", got)
	}
}

// A script that cannot be read, in the yard or in the checkout, is kept as it
// is and said, and the upgrade goes on to the others. A directory stands in
// for the file that cannot be read.
func TestUpgradeScriptsUnreadableIsKept(t *testing.T) {
	t.Parallel()
	checkout := scriptsCheckout(t, map[string]string{"merge-local": scriptNew, "pr-merge": scriptNew})
	home := scriptsHome(t, map[string]string{"pr-merge": scriptOld, "integrate-train": scriptOld, "pr-train": scriptOld})

	// The yard's merge-local is a directory, and the checkout's pr-train too.
	if err := os.Mkdir(filepath.Join(home, "scripts", "merge-local"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(checkout, filepath.FromSlash(scriptSource(t, "pr-train"))), 0o755); err != nil {
		t.Fatal(err)
	}

	var warn strings.Builder
	s := upgradeScriptsInstall(context.Background(), checkout, []string{home}, &warn)

	for _, want := range []string{filepath.Join(home, "scripts", "merge-local") + " could not be read", scriptSource(t, "pr-train") + " in " + checkout + " could not be read"} {
		if !strings.Contains(warn.String(), want) {
			t.Fatalf("the warnings lack %q:\n%s", want, warn.String())
		}
	}
	if fi := scriptStat(t, filepath.Join(home, "scripts", "merge-local")); !fi.IsDir() {
		t.Fatalf("the yard's merge-local is no longer what it was: %v", fi.Mode())
	}
	if got := scriptText(t, filepath.Join(home, "scripts", "pr-train")); got != scriptOld {
		t.Fatalf("pr-train = %q, want the yard's kept", got)
	}
	if got := s.summary(); got != "pr-merge refreshed" {
		t.Fatalf("summary %q, want the one script that could be refreshed", got)
	}
}

// The new file is 0755 whatever the umask of the process that upgrades, as
// install(1), which the docs have the operator use, leaves it. The umask is
// the process's, so this test does not run beside the others.
func TestUpgradeScriptsModeIgnoresUmask(t *testing.T) {
	was := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(was) })
	checkout := scriptsCheckout(t, map[string]string{"merge-local": scriptNew})
	home := scriptsHome(t, map[string]string{"merge-local": scriptOld})

	upgradeScriptsInstall(context.Background(), checkout, []string{home}, io.Discard)

	if mode := scriptStat(t, filepath.Join(home, "scripts", "merge-local")).Mode().Perm(); mode != 0o755 {
		t.Fatalf("mode = %v under umask 077, want 0755", mode)
	}
}

// A rollback puts back each script the run replaced, the old bytes in the old
// mode, and the copy it came from is spent.
func TestUpgradeScriptsRollbackPutsBack(t *testing.T) {
	t.Parallel()
	checkout := scriptsCheckout(t, map[string]string{"merge-local": scriptNew, "pr-train": scriptNew})
	home := scriptsHome(t, map[string]string{"merge-local": scriptOld, "pr-train": scriptOld, "pr-merge": scriptOld})
	s := upgradeScriptsInstall(context.Background(), checkout, []string{home}, io.Discard)
	if got := s.summary(); got != "merge-local, pr-train refreshed" {
		t.Fatalf("summary %q", got)
	}

	var warn strings.Builder
	names := s.putBack(&warn)

	if strings.Join(names, ",") != "merge-local,pr-train" || warn.Len() != 0 {
		t.Fatalf("put back %v, warned %q", names, warn.String())
	}
	for _, name := range []string{"merge-local", "pr-train", "pr-merge"} {
		path := filepath.Join(home, "scripts", name)
		if got := scriptText(t, path); got != scriptOld {
			t.Fatalf("%s = %q, want the old bytes", name, got)
		}
		if mode := scriptStat(t, path).Mode().Perm(); mode != 0o644 {
			t.Fatalf("%s mode = %v, want the old 0644", name, mode)
		}
	}
	wantFiles(t, home, "merge-local", "pr-merge", "pr-train")
}

// A script whose copy has gone cannot be put back: it is said, with where
// the old bytes were, and the ones that can be still are.
func TestUpgradeScriptsPutBackWithoutTheCopy(t *testing.T) {
	t.Parallel()
	checkout := scriptsCheckout(t, map[string]string{"merge-local": scriptNew, "pr-train": scriptNew})
	home := scriptsHome(t, map[string]string{"merge-local": scriptOld, "pr-train": scriptOld})
	s := upgradeScriptsInstall(context.Background(), checkout, []string{home}, io.Discard)
	if err := os.Remove(filepath.Join(home, "scripts", "merge-local.previous")); err != nil {
		t.Fatal(err)
	}

	var warn strings.Builder
	names := s.putBack(&warn)

	if strings.Join(names, ",") != "pr-train" {
		t.Fatalf("put back %v, want only pr-train", names)
	}
	if !strings.Contains(warn.String(), filepath.Join(home, "scripts", "merge-local")) || !strings.Contains(warn.String(), "merge-local.previous") {
		t.Fatalf("the warning does not name the script and its copy:\n%s", warn.String())
	}
}

// Every covered yard has its own scripts directory refreshed: each that has
// the script, once the name is told, and none that lacks it.
func TestUpgradeScriptsEveryCoveredYard(t *testing.T) {
	t.Parallel()
	checkout := scriptsCheckout(t, map[string]string{"merge-local": scriptNew})
	first := scriptsHome(t, map[string]string{"merge-local": scriptOld})
	second := scriptsHome(t, map[string]string{"merge-local": scriptOld + "# the second yard's\n"})
	third := scriptsHome(t, nil)

	s := upgradeScriptsInstall(context.Background(), checkout, []string{first, second, third}, io.Discard)

	for _, home := range []string{first, second} {
		if got := scriptText(t, filepath.Join(home, "scripts", "merge-local")); got != scriptNew {
			t.Fatalf("%s: merge-local = %q, want the checkout's", home, got)
		}
	}
	wantFiles(t, third)
	if got := s.summary(); got != "merge-local refreshed" || len(s.replaced) != 2 {
		t.Fatalf("summary %q, %d replaced; want the name once and two replaced", got, len(s.replaced))
	}
	if names := s.putBack(io.Discard); len(names) != 1 {
		t.Fatalf("put back %v, want the name once", names)
	}
	if got := scriptText(t, filepath.Join(second, "scripts", "merge-local")); got != scriptOld+"# the second yard's\n" {
		t.Fatalf("the second yard's merge-local = %q, want its own bytes back", got)
	}
}

// The copy is one generation, overwritten each time: after a second refresh
// it holds what the second replaced, not the first's.
func TestUpgradeScriptsPreviousIsOneGeneration(t *testing.T) {
	t.Parallel()
	home := scriptsHome(t, map[string]string{"merge-local": scriptOld})
	live := filepath.Join(home, "scripts", "merge-local")
	for _, body := range []string{"#!/bin/sh\necho two\n", "#!/bin/sh\necho three\n"} {
		checkout := scriptsCheckout(t, map[string]string{"merge-local": body})
		upgradeScriptsInstall(context.Background(), checkout, []string{home}, io.Discard)
	}
	if got := scriptText(t, live); got != "#!/bin/sh\necho three\n" {
		t.Fatalf("merge-local = %q", got)
	}
	if got := scriptText(t, live+".previous"); got != "#!/bin/sh\necho two\n" {
		t.Fatalf("merge-local.previous = %q, want only the last generation", got)
	}
}

// A temporary left by a run that died is replaced, not written through: here
// it is a link to a file that must stay as it is.
func TestUpgradeScriptsLeftoverIsNotWrittenThrough(t *testing.T) {
	t.Parallel()
	checkout := scriptsCheckout(t, map[string]string{"merge-local": scriptNew})
	home := scriptsHome(t, map[string]string{"merge-local": scriptOld})
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(elsewhere, []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(home, "scripts", ".merge-local.new")); err != nil {
		t.Fatal(err)
	}

	upgradeScriptsInstall(context.Background(), checkout, []string{home}, io.Discard)

	if got := scriptText(t, elsewhere); got != "keep me\n" {
		t.Fatalf("the link's target now reads %q", got)
	}
	if got := scriptText(t, filepath.Join(home, "scripts", "merge-local")); got != scriptNew {
		t.Fatalf("merge-local = %q, want the checkout's", got)
	}
	wantFiles(t, home, "merge-local", "merge-local.previous")
}

// Every source is a file of this tree, so a typo in the list cannot leave a
// script that is never refreshed. The test reads only that the file is
// there; a moved or removed source then fails when that source's row selects
// this package, or when the test gate selects the whole suite.
func TestUpgradeScriptSourcesAreInTheTree(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, rel := range upgradeScriptSources {
		if fi, err := os.Stat(filepath.Join("..", "..", filepath.FromSlash(rel))); err != nil || !fi.Mode().IsRegular() {
			t.Errorf("%s is not a file of this tree: %v", rel, err)
		}
		if name := filepath.Base(rel); seen[name] {
			t.Errorf("two sources install as %s", name)
		} else {
			seen[name] = true
		}
	}
}

// upgradeScene is a yard that can be upgraded with nothing started or built:
// a depot checkout holding the new scripts, a yard script directory holding
// the old, and every outward step of the run stood in for. The "binary" it
// runs from and the one a build writes are copies of the test's own, the one
// file the run reads the build of; the gate's answer is the test's.
type upgradeScene struct {
	a        *app
	home     string
	st       store.API
	self     string // the test binary, which a build writes
	target   string // the binary the yard runs from, which the install replaces
	scripts  string // the yard's merge-local
	previous string // its copy
	// gate answers the installed binary's --check: nil passes.
	gate func() error
}

// build stands in for go build: it writes the test binary where it is asked.
func (sc *upgradeScene) build(_ context.Context, _, file string) (string, error) {
	return "", copyFile(sc.self, file)
}

func newUpgradeScene(t *testing.T) *upgradeScene {
	t.Helper()
	ctx := context.Background()
	home, st, mk := upgradeYard(t)
	if err := st.SetSetting(ctx, store.OperatorSetting, "jo", "jo"); err != nil {
		t.Fatal(err)
	}
	// The scene's store is in its own temp dir while the run backs up into
	// the yard home's, as the daemon does; the store writes a backup only
	// in its directories of them, so the home's is named. The store stays
	// out of the home on purpose: the rollback path opens the home's store
	// file itself, which must not be this open one.
	if err := st.SetSetting(ctx, store.BackupDirSetting, filepath.Join(home, "backups"), "jo"); err != nil {
		t.Fatal(err)
	}
	checkout := scriptsCheckout(t, map[string]string{"merge-local": scriptNew})
	if err := st.PutDepot(ctx, store.Depot{Name: "yardr", Path: checkout}, "jo"); err != nil {
		t.Fatal(err)
	}
	// The yard runs from a copy of the test binary, by its resolved path.
	target := filepath.Join(realTempDir(t), "yardr")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := copyFile(self, target); err != nil {
		t.Fatal(err)
	}
	stub(t, &upgradeExecutable, func() (string, error) { return target, nil })
	stub(t, &lookPath, func(string) (string, error) { return "/bin/go", nil })
	// Nothing is signed unless a test says so: the scene sets no identity
	// and is not darwin.
	stub(t, &upgradeCodesign, func(context.Context, string, string) (string, error) {
		t.Error("upgradeCodesign called with no identity set, or off darwin")
		return "", nil
	})
	// No login service names another yard, and none restarts a view.
	stub(t, &goos, "linux")
	stub(t, &serviceDir, func() (string, error) { return t.TempDir(), nil })
	// The server answers, so none is started and none is stopped for real.
	stub(t, &serverRunning, func(context.Context, *config.Config) bool { return true })
	stub(t, &upgradeServerStart, func(context.Context, *config.Config) error { return nil })
	stub(t, &upgradeServerStop, func(*config.Config) error { return nil })
	stub(t, &upgradeStopDaemon, func(*config.Config, time.Duration) (int, error) { return 0, nil })

	sc := &upgradeScene{a: mk(), home: home, st: st, self: self, target: target, scripts: filepath.Join(home, "scripts", "merge-local")}
	sc.previous = sc.scripts + ".previous"
	stub(t, &upgradeGoBuild, sc.build)
	if err := os.MkdirAll(filepath.Dir(sc.scripts), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sc.scripts, []byte(scriptOld), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sc.scripts, 0o755); err != nil {
		t.Fatal(err)
	}
	stub(t, &upgradeRunInstalled, func(_ context.Context, _, _ string, args ...string) (string, error) {
		if sc.gate == nil {
			return "", nil
		}
		if err := sc.gate(); err != nil {
			return "server    fail: " + err.Error(), err
		}
		return "", nil
	})
	return sc
}

// upgrade runs yardr upgrade as the operator on the scene's yard, with more
// flags if given. The gate's bound starts before the install, so a run that
// must pass leaves it at its default; one that must fail gives it a short one,
// since a failing check is only judged at the bound.
func (sc *upgradeScene) upgrade(t *testing.T, flags ...string) (string, error) {
	t.Helper()
	return run(t, sc.a.rootCmd(), append([]string{"--actor", "jo", "upgrade", "--from", "yardr"}, flags...)...)
}

// The upgrade refreshes scripts and signs only when configured; failed
// signing puts the previous binary back before the gates come down.
func TestUpgradeRunRefreshesScripts(t *testing.T) {
	t.Run("refreshes scripts", func(t *testing.T) { testUpgradeRunRefreshesScripts(t) })
	t.Run("prints stale pack copies", func(t *testing.T) { testUpgradeRunPrintsStalePackCopies(t) })
	t.Run("signs installed binary on darwin", func(t *testing.T) { testUpgradeRunSignsInstalledBinaryOnDarwin(t) })
	t.Run("leaves binary unsigned", func(t *testing.T) { testUpgradeRunLeavesBinaryUnsigned(t) })
	t.Run("sign failure restores previous binary", func(t *testing.T) { testUpgradeRunSignFailureRestoresPrevious(t) })
}

func testUpgradeRunRefreshesScripts(t *testing.T) {
	sc := newUpgradeScene(t)

	out, err := sc.upgrade(t)
	if err != nil {
		t.Fatalf("upgrade: %v\n%s", err, out)
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	var seen []string
	for _, l := range lines {
		seen = append(seen, strings.Fields(l)[0])
	}
	if strings.Join(seen, " ") != "backup keep install scripts health" {
		t.Fatalf("the output's lines are %v:\n%s", seen, out)
	}
	if want := "scripts    merge-local refreshed"; lines[3] != want {
		t.Fatalf("scripts line = %q, want %q", lines[3], want)
	}
	if got := scriptText(t, sc.scripts); got != scriptNew {
		t.Fatalf("merge-local = %q, want the checkout's", got)
	}
	if got := scriptText(t, sc.previous); got != scriptOld {
		t.Fatalf("merge-local.previous = %q, want the replaced bytes", got)
	}
}

// A second upgrade on the same checkout has nothing to refresh, and says so.
func TestUpgradeRunScriptsCurrent(t *testing.T) {
	sc := newUpgradeScene(t)
	if out, err := sc.upgrade(t); err != nil {
		t.Fatalf("first upgrade: %v\n%s", err, out)
	}
	out, err := sc.upgrade(t)
	if err != nil {
		t.Fatalf("second upgrade: %v\n%s", err, out)
	}
	if !strings.Contains(out, "scripts    current\n") {
		t.Fatalf("no current line:\n%s", out)
	}
}

// When the gate fails the upgrade rolls back, and the scripts go back with
// the binary: the yard ends on the old bytes, in the old mode, and the
// output says so.
func TestUpgradeRunRollbackPutsScriptsBack(t *testing.T) {
	sc := newUpgradeScene(t)
	sc.gate = func() error { return errors.New("the new build does not answer") }

	out, err := sc.upgrade(t, "--timeout", "50ms")

	if err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("upgrade = %v, want a rollback\n%s", err, out)
	}
	if got := scriptText(t, sc.scripts); got != scriptOld {
		t.Fatalf("merge-local = %q after the rollback, want the old bytes", got)
	}
	if mode := scriptStat(t, sc.scripts).Mode().Perm(); mode != 0o755 {
		t.Fatalf("merge-local mode = %v, want the old 0755", mode)
	}
	if _, err := os.Stat(sc.previous); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the spent copy is still there: %v", err)
	}
	for _, want := range []string{"scripts    merge-local refreshed\n", "scripts    merge-local put back\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the output lacks %q:\n%s", want, out)
		}
	}
}

// packsCopy installs body as the home's installed pack copy of the pack's
// script name, as a pack import leaves it.
func packsCopy(t *testing.T, home, pack, name, body string) {
	t.Helper()
	dir := filepath.Join(home, "packs", pack, "scripts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// The stale-pack line names each installed pack copy of a refreshed script
// whose bytes differ, with the exact command that re-applies its pack: a
// matching copy, a script the run did not refresh, and a yard with no
// packs directory are no entry. A pack imported into the yard itself
// re-applies with no --depot; the first recorded depot is the command's
// where several imported the pack; a copy with no recorded import is named
// with no command.
func TestUpgradeStalePackLine(t *testing.T) {
	t.Parallel()
	checkout := scriptsCheckout(t, map[string]string{"pr-ci": scriptNew, "merge-local": scriptNew})
	home := scriptsHome(t, map[string]string{"pr-ci": scriptOld})
	packsCopy(t, home, "contributors", "pr-ci", scriptOld)
	packsCopy(t, home, "starter", "pr-ci", scriptNew)
	packsCopy(t, home, "contributors", "merge-local", scriptOld)
	packsOf := func(home string) ([]store.PackImport, string, bool) {
		return []store.PackImport{
			{Pack: "contributors", Depot: "zebra"},
			{Pack: "contributors", Depot: "alpha"},
			{Pack: "linear", Depot: ""},
		}, "yardr", true
	}
	for _, tc := range []struct {
		name  string
		names []string
		want  string
	}{
		{"stale names the copy and the first depot's command, and a matching copy is no entry", []string{"pr-ci"},
			"packs/contributors/scripts/pr-ci is stale until the pack is re-applied: yardr pack apply contributors --depot zebra"},
		{"unrefreshed script is no entry", []string{"pr-train"}, ""},
		{"unknown name is no entry", []string{"nosuch"}, ""},
		{"no names is no entry", nil, ""},
	} {
		if got := upgradeStalePackLine(checkout, []string{home}, tc.names, packsOf); got != tc.want {
			t.Errorf("%s: line = %q, want %q", tc.name, got, tc.want)
		}
	}

	// Two stale copies join on one line, in the order of the names.
	if got, want := upgradeStalePackLine(checkout, []string{home}, []string{"pr-ci", "merge-local"}, packsOf),
		"packs/contributors/scripts/pr-ci is stale until the pack is re-applied: yardr pack apply contributors --depot zebra; packs/contributors/scripts/merge-local is stale until the pack is re-applied: yardr pack apply contributors --depot zebra"; got != want {
		t.Errorf("two stale copies: line = %q, want %q", got, want)
	}

	// A pack imported into the yard itself re-applies with no --depot.
	linear := scriptsHome(t, map[string]string{"pr-ci": scriptOld})
	packsCopy(t, linear, "linear", "pr-ci", scriptOld)
	if got, want := upgradeStalePackLine(checkout, []string{linear}, []string{"pr-ci"}, packsOf),
		"packs/linear/scripts/pr-ci is stale until the pack is re-applied: yardr pack apply linear"; got != want {
		t.Errorf("yard-wide pack: line = %q, want %q", got, want)
	}

	// A stale copy with no recorded import is named with no command.
	hand := scriptsHome(t, map[string]string{"pr-ci": scriptOld})
	packsCopy(t, hand, "handmade", "pr-ci", scriptOld)
	if got, want := upgradeStalePackLine(checkout, []string{hand}, []string{"pr-ci"}, packsOf),
		"packs/handmade/scripts/pr-ci is stale; pack handmade has no recorded import, so re-apply it by hand"; got != want {
		t.Errorf("unrecorded pack: line = %q, want %q", got, want)
	}

	// A yard with no packs directory has no stale copy.
	bare := scriptsHome(t, map[string]string{"pr-ci": scriptOld})
	if got := upgradeStalePackLine(checkout, []string{bare}, []string{"pr-ci"}, packsOf); got != "" {
		t.Errorf("bare yard: line = %q, want empty", got)
	}

	// A failed records read names the stale copy but does not claim that no
	// import exists or invent a re-apply command.
	if got, want := upgradeStalePackLine(checkout, []string{home}, []string{"pr-ci"}, func(string) ([]store.PackImport, string, bool) {
		return nil, "yardr", false
	}), "packs/contributors/scripts/pr-ci is stale; pack imports for yardr could not be read, so the re-apply command is unknown"; got != want {
		t.Errorf("unreadable pack records: line = %q, want %q", got, want)
	}

	// An installed copy that cannot be read is named as unknown, not treated
	// as absent or stale.
	unreadable := scriptsHome(t, map[string]string{"pr-ci": scriptOld})
	badCopy := filepath.Join(unreadable, "packs", "broken", "scripts", "pr-ci")
	if err := os.MkdirAll(badCopy, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := upgradeStalePackLine(checkout, []string{unreadable}, []string{"pr-ci"}, func(string) ([]store.PackImport, string, bool) {
		return nil, "yardr", true
	}), "packs/broken/scripts/pr-ci could not be read, so its staleness is unknown"; got != want {
		t.Errorf("unreadable pack copy: line = %q, want %q", got, want)
	}

	// A packs directory that cannot be listed also reports an unknown state.
	unlistable := scriptsHome(t, map[string]string{"pr-ci": scriptOld})
	if err := os.WriteFile(filepath.Join(unlistable, "packs"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := upgradeStalePackLine(checkout, []string{unlistable}, []string{"pr-ci"}, func(string) ([]store.PackImport, string, bool) {
		return nil, "yardr", true
	}), "pack copies of pr-ci in yardr could not be listed, so staleness is unknown"; got != want {
		t.Errorf("unlistable pack copies: line = %q, want %q", got, want)
	}

	// Where several yards are covered the line uses each yard's name rather
	// than exposing its home path.
	first := scriptsHome(t, map[string]string{"pr-ci": scriptOld})
	second := scriptsHome(t, map[string]string{"pr-ci": scriptOld})
	packsCopy(t, second, "contributors", "pr-ci", scriptOld)
	packsOfSeveral := func(home string) ([]store.PackImport, string, bool) {
		if home == second {
			return []store.PackImport{{Pack: "contributors", Depot: "r"}}, "second-yard", true
		}
		return nil, "first-yard", true
	}
	want := "yard second-yard: packs/contributors/scripts/pr-ci is stale until the pack is re-applied: yardr pack apply contributors --depot r"
	if got := upgradeStalePackLine(checkout, []string{first, second}, []string{"pr-ci"}, packsOfSeveral); got != want {
		t.Errorf("several yards: line = %q, want %q", got, want)
	}
	duplicateNames := func(home string) ([]store.PackImport, string, bool) {
		if home == second {
			return []store.PackImport{{Pack: "contributors", Depot: "r"}}, "yardr", true
		}
		return nil, "yardr", true
	}
	want = "yard yardr (2): packs/contributors/scripts/pr-ci is stale until the pack is re-applied: yardr pack apply contributors --depot r"
	if got := upgradeStalePackLine(checkout, []string{first, second}, []string{"pr-ci"}, duplicateNames); got != want {
		t.Errorf("duplicate yard names: line = %q, want %q", got, want)
	}
}

// The upgrade prints the stale-pack line after the scripts line when a
// refreshed script's installed pack copy differs, and no such line when
// none does.
func testUpgradeRunPrintsStalePackCopies(t *testing.T) {
	ctx := context.Background()
	plain := newUpgradeScene(t)
	out, err := plain.upgrade(t)
	if err != nil {
		t.Fatalf("upgrade: %v\n%s", err, out)
	}
	if strings.Contains(out, "packscripts") {
		t.Fatalf("the output names stale copies with none installed:\n%s", out)
	}

	sc := newUpgradeScene(t)
	packsCopy(t, sc.home, "contributors", "merge-local", scriptOld)
	if _, err := sc.st.RecordPack(ctx, store.PackImport{Pack: "contributors", Depot: "yardr", Source: "embedded test"}, true, "jo"); err != nil {
		t.Fatal(err)
	}
	out, err = sc.upgrade(t)
	if err != nil {
		t.Fatalf("upgrade: %v\n%s", err, out)
	}
	want := "packscripts  packs/contributors/scripts/merge-local is stale until the pack is re-applied: yardr pack apply contributors --depot yardr\n"
	if !strings.Contains(out, want) {
		t.Fatalf("the output lacks %q:\n%s", want, out)
	}
}
