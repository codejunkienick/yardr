package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"yardr.dev/yardr/internal/config"
	"yardr.dev/yardr/internal/notify"
	"yardr.dev/yardr/internal/peer"
	"yardr.dev/yardr/internal/server"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/version"
)

// The upgrade install's tunables, as the design has them.
const (
	// upgradePollEvery is how often the gate samples each covered yard.
	upgradePollEvery = 5 * time.Second
	// upgradeDefaultTimeout bounds the whole multi-yard gate run: one
	// deadline for every yard, so the rewind window does not grow with
	// the number of yards.
	upgradeDefaultTimeout = 5 * time.Minute
)

// upgradeServerStart starts the home's server; tests replace it.
var upgradeServerStart = server.Start

// upgradeServerStop stops the home's server; tests replace it.
var upgradeServerStop = stopServer

// upgradeStopDaemon stops the home's daemon; tests replace it.
var upgradeStopDaemon = stopDaemon

// upgradeStartDaemon starts the home's daemon in the background; tests
// replace it.
var upgradeStartDaemon = func(environ []string, cfg *config.Config) (int, error) {
	return kindOf(svcDaemon).start(environ, cfg, nil, "", false)
}

// upgradeExecutable is this process's binary; tests replace it.
var upgradeExecutable = os.Executable

// upgradeGoBuild runs go build -buildmode=exe -o file . in dir; tests replace
// it. The build mode is named because a checkout whose root package is no
// program would otherwise build to its package archive, written to file with
// exit status 0, and the install would put that over the binary.
var upgradeGoBuild = func(ctx context.Context, dir, file string) (string, error) {
	cmd := exec.CommandContext(ctx, "go", "build", "-buildmode=exe", "-o", file, ".")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// upgradeCodesignIdentifier is the signing identifier every signed yardr
// carries: fixed, so a Developer Tools entry naming it holds across
// reinstalls. It is no setting: one yardr binary means one identifier.
const upgradeCodesignIdentifier = "dev.yardr.yardr"

// upgradeCodesign signs target with identity under upgradeCodesignIdentifier;
// tests replace it.
var upgradeCodesign = func(ctx context.Context, identity, target string) (string, error) {
	cmd := exec.CommandContext(ctx, "codesign", "--force", "--sign", identity, "--identifier", upgradeCodesignIdentifier, target)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// upgradeRunInstalled runs the installed binary with --home and args;
// tests replace it to script the gate.
var upgradeRunInstalled = func(ctx context.Context, bin, home string, args ...string) (string, error) {
	full := append([]string{"--home", home}, args...)
	cmd := exec.CommandContext(ctx, bin, full...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// upgradeNotifySend tells a user through their source; tests replace it.
var upgradeNotifySend = notify.Send

// upgradeReadBuild reads the build of the binary at a path; tests replace it.
var upgradeReadBuild = version.OfFile

// upgradeCurrentBuild is the build this process runs; tests replace it.
var upgradeCurrentBuild = version.CurrentBuild

// upgradeTestBuildEnv names the revision a black-box fixture's fake build
// stands for: its fake go copies the test binary's own bytes, stamped with
// the yardr checkout's commit and never the stand-in depot's tip, and no
// build it can run stamps those bytes with the tip (go stamps from VCS,
// which no flag sets). The pin and the newness guard read this name instead
// of the bytes' own stamp; the install line still names the stamp. It is set
// in those fixtures alone (contrib/assembly): set it by hand and the install
// trusts the name, not the bytes.
const upgradeTestBuildEnv = "YARDR_TEST_UPGRADE_BUILD"

// upgradeInstallTarget is where the upgrade writes the build: the binary this
// yard runs from, symlinks resolved. The server and the daemon watch that path
// for a newer build, so bytes written anywhere else would verify nothing, and
// where go install would write is no part of it: a yardr kept outside go's bin
// directory upgrades where it is. A link to the binary stays a link; the file
// it names is replaced.
func upgradeInstallTarget() (string, error) {
	self, err := upgradeExecutable()
	if err != nil {
		return "", err
	}
	return resolveProgram(self), nil
}

// upgradeInstall builds the checkout in dir to a temp file beside target and
// renames it over target, so the server's and the daemon's watch of the path
// sees one complete new file and never a build still being linked. The temp is
// removed on every path out: a failed build leaves target as it was and
// nothing beside it. The build's output comes back for the failure to quote.
// A build of the running revision is already current and succeeds without
// replacing target; any other build that is not newer is refused, so a
// downgrade never lands untold (a told one installs by hand).
func upgradeInstall(ctx context.Context, dir, target string) (string, error) {
	return upgradeInstallPinned(ctx, dir, target, "")
}

// upgradeInstallPinned builds like upgradeInstall, and where wantTip names the
// preflight's tip the build must be of it: anything else means the base moved
// during the upgrade, and is refused before the rename with the old binary
// whole.
func upgradeInstallPinned(ctx context.Context, dir, target, wantTip string) (string, error) {
	fi, err := os.Stat(target)
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".build-*")
	if err != nil {
		return "", fmt.Errorf("the new build cannot be written beside %s: %w", target, err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if out, err := upgradeGoBuild(ctx, dir, tmp.Name()); err != nil {
		return out, fmt.Errorf("go build . in %s failed: %w", dir, err)
	}
	// However go put its output there, the new file takes the mode of the one
	// it replaces, as a copy of it would (copyFile).
	if err := os.Chmod(tmp.Name(), fi.Mode()); err != nil {
		return "", err
	}
	built, err := upgradeReadBuild(tmp.Name())
	if err != nil {
		return "", fmt.Errorf("the new build cannot be read: %w", err)
	}
	if asserted := strings.TrimSpace(os.Getenv(upgradeTestBuildEnv)); asserted != "" {
		// The fixture names what its fake bytes stand for; they are ordered
		// by the tmp file's time below, as an unstamped build is.
		built.Revision, built.Time = asserted, time.Time{}
	}
	if wantTip != "" {
		if rev := strings.TrimSuffix(built.Revision, "-modified"); rev == "" || !strings.HasPrefix(wantTip, rev) {
			return "", fmt.Errorf("the base moved during the upgrade: the build is of %s, not the tip %s; run yardr upgrade again", version.Show(built.Revision), shortSHA(wantTip))
		}
	}
	// A build without a stamp is ordered by its file's time, as the watch
	// orders it (version.Binary): the running build's by the installed
	// file's, the new one's by the temp's.
	running := upgradeCurrentBuild()
	if running.Time.IsZero() {
		running.Time = fi.ModTime()
	}
	if built.Time.IsZero() {
		if tfi, err := os.Stat(tmp.Name()); err == nil {
			built.Time = tfi.ModTime()
		}
	}
	if reason := version.Newer(running, built); reason != "" {
		if sameBuildRevision(running.Revision, built.Revision) {
			// The running build, built again: nothing to do, and the
			// deferred remove takes the temp. Every other rejection
			// stays an error.
			return fmt.Sprintf("already current at %s", version.Show(built.Revision)), nil
		}
		return "", fmt.Errorf("the build of %s is not installed: %s; install it by hand if that is what you want", version.Show(built.Revision), reason)
	}
	return "", os.Rename(tmp.Name(), target)
}

// sameBuildRevision mirrors version.Newer's identity arm: a candidate of
// the running build's own revision, dirty or not, is the running build,
// not a newer one. Newer itself is shared and stays untouched.
func sameBuildRevision(running, built string) bool {
	base := func(rev string) string { return strings.TrimSuffix(rev, "-modified") }
	return built != "" && base(built) == base(running)
}

// upgradePreflightDepot checks the depot's checkout stands where an install
// builds the landed commit from: on the depot's base branch, at its tip, with
// a clean worktree. It returns the tip the build is pinned to. A dir depot,
// or one with no base, has no landed tip, so there is nothing to pin and it
// returns "". It reads only: the run fetches nothing, since merge-local just
// moved the tip, and tells the operator to fetch, put the checkout back on
// the base and run yardr upgrade again.
func upgradePreflightDepot(ctx context.Context, depot *store.Depot) (string, error) {
	if depot.IsDir() || depot.Base == "" {
		return "", nil
	}
	path, base := depot.Path, depot.Base
	again := fmt.Sprintf("fetch, put the checkout of %s back on %s and run yardr upgrade again", depot.Name, base)
	if _, err := git(ctx, path, "rev-parse", "--show-toplevel"); err != nil {
		return "", fmt.Errorf("the checkout of %s at %s is not a git checkout; %s", depot.Name, path, again)
	}
	head, err := git(ctx, path, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("the checkout of %s at %s has no commit checked out; %s", depot.Name, path, again)
	}
	branch, err := git(ctx, path, "symbolic-ref", "-q", "HEAD")
	if err != nil {
		return "", fmt.Errorf("the checkout of %s is detached at %s, not on %s; %s", depot.Name, shortSHA(head), base, again)
	}
	if short := strings.TrimPrefix(branch, "refs/heads/"); short != base && short != baseBranch(base) {
		return "", fmt.Errorf("the checkout of %s is on %s, not on %s; %s", depot.Name, short, base, again)
	}
	tip, err := git(ctx, path, "rev-parse", "--verify", "--quiet", "--end-of-options", base)
	if err != nil {
		return "", fmt.Errorf("the base %s of depot %s has no commit in its checkout at %s; %s", base, depot.Name, path, again)
	}
	if head != tip {
		return "", fmt.Errorf("the checkout of %s is at %s, not the tip of %s at %s; %s", depot.Name, shortSHA(head), base, shortSHA(tip), again)
	}
	status, err := git(ctx, path, "status", "--porcelain")
	if err != nil {
		return "", fmt.Errorf("the checkout of %s at %s cannot be checked for changes: %v; %s", depot.Name, path, err, again)
	}
	if status != "" {
		return "", fmt.Errorf("the checkout of %s has uncommitted changes; set them aside, then %s", depot.Name, again)
	}
	return tip, nil
}

// baseBranch is the local branch a base stands on: the base itself, or the
// branch after the remote's slash, since a checkout can never stand on a
// remote (origin/main is main checked out).
func baseBranch(base string) string {
	if _, branch, ok := strings.Cut(base, "/"); ok {
		return branch
	}
	return base
}

// shortSHA is the sha as a refusal names it: 12 characters, as a build's
// revision has it, or whole where shorter.
func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// upgradeCoveredYards are the yards the install covers: its own home,
// always, plus every yard registered on this machine whose login service
// runs the shared binary — the homes the daemon services name in
// WorkingDirectory whose program is the install target. A registered yard
// on another binary is left out, and a yard with no login service is
// covered only when it is the upgrading yard.
func upgradeCoveredYards(cfg *config.Config, target string) []string {
	seen := map[string]bool{cfg.Home: true}
	out := []string{cfg.Home}
	manager := managerOf()
	if manager == "" {
		return out
	}
	dir, err := serviceDir()
	if err != nil {
		return out
	}
	pattern := "dev.yardr." + svcDaemon + ".*.plist"
	if manager == systemd {
		pattern = "yardr-" + svcDaemon + "-*.service"
	}
	paths, _ := filepath.Glob(filepath.Join(dir, pattern))
	want := resolveProgram(target)
	for _, p := range paths {
		home, prog := parseServiceHomeAndProgram(p)
		if home == "" || home == cfg.Home || seen[home] {
			continue
		}
		if prog == "" || resolveProgram(prog) != want {
			continue
		}
		seen[home] = true
		out = append(out, home)
	}
	slices.Sort(out[1:])
	return out
}

// resolveProgram is the program for comparison: absolute, with symlinks
// followed where the file exists.
func resolveProgram(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// parseServiceHomeAndProgram reads a daemon service file's home
// (WorkingDirectory) and the binary it runs first (ProgramArguments on
// launchd, ExecStart on systemd).
func parseServiceHomeAndProgram(path string) (home, prog string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	if m := workingDir.FindSubmatch(b); m != nil {
		home = strings.TrimSpace(string(m[1]) + string(m[2]))
		home = strings.ReplaceAll(home, "%%", "%")
	}
	if strings.HasSuffix(path, ".plist") {
		prog = plistProgram(b)
	} else {
		prog = unitProgram(b)
	}
	return home, prog
}

// plistProgram is the first string of the plist's ProgramArguments.
func plistProgram(b []byte) string {
	// <key>ProgramArguments</key><array><string>/path/yardr</string>...
	i := bytes.Index(b, []byte("ProgramArguments"))
	if i < 0 {
		return ""
	}
	rest := b[i:]
	j := bytes.Index(rest, []byte("<string>"))
	if j < 0 {
		return ""
	}
	rest = rest[j+len("<string>"):]
	k := bytes.Index(rest, []byte("</string>"))
	if k < 0 {
		return ""
	}
	return strings.TrimSpace(string(rest[:k]))
}

// unitProgram is the first word of the unit's ExecStart.
func unitProgram(b []byte) string {
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		v, ok := strings.CutPrefix(line, "ExecStart=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		// The program is the first word, quoted or not.
		if strings.HasPrefix(v, `"`) || strings.HasPrefix(v, `'`) {
			q := v[:1]
			if i := strings.Index(v[1:], q); i >= 0 {
				return v[1 : 1+i]
			}
			return ""
		}
		if i := strings.IndexAny(v, " \t"); i >= 0 {
			v = v[:i]
		}
		return v
	}
	return ""
}

// copyFile copies src to dst, replacing it, keeping src.
func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	fi, err := os.Stat(src)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), fi.Mode()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// upgradeGateStart is this process's start time for the gate file, 0 where
// the OS tells none.
func upgradeGateStart() uint64 {
	if st, err := peer.Describe(os.Getpid()); err == nil {
		return st.Start
	}
	return 0
}

// upgradeYardWas is what ran in a covered yard before the upgrade: the gate
// starts a stopped server to verify it, and the rollback restarts only
// what ran.
type upgradeYardWas struct {
	home      string
	server    bool
	daemon    bool
	head      uint64
	backup    string
	startedBy bool // the upgrade started its server for the gate
}

// upgradePollResult is what one covered yard came to in the gate.
type upgradePollResult struct {
	home string
	ok   bool
	why  string
}

// upgradePoll checks every covered yard in parallel under one deadline: each
// sample runs the installed binary's upgrade --check as a subprocess, so
// the predicate always runs in the build it asserts, passing the install's
// start as --since. A yard the gate cannot check fails it, named.
func upgradePoll(ctx context.Context, bin string, yards []string, since string, deadline time.Time) []upgradePollResult {
	// A deadline that only guards the polling loop is not enough: one
	// installed CLI can hang while checking a yard. Give every yard's
	// subprocess the same deadline too, so one stuck check cannot extend the
	// rewind window or hold up the other results.
	pollCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	out := make([]upgradePollResult, len(yards))
	done := make(chan int, len(yards))
	for i, home := range yards {
		go func(i int, home string) {
			out[i] = upgradePollOne(pollCtx, bin, home, since, deadline)
			done <- i
		}(i, home)
	}
	// Every yard answers: a cancelled context ends each poll promptly,
	// which still sends its result.
	for range yards {
		<-done
	}
	return out
}

// upgradePollWithRestartFailures makes a failed service restart fail the
// install gate even when --check sees no recorded view and skips that part.
func upgradePollWithRestartFailures(results []upgradePollResult, restartErrors []error) []upgradePollResult {
	for i, err := range restartErrors {
		if err == nil || i >= len(results) {
			continue
		}
		results[i].ok = false
		if results[i].why != "" {
			results[i].why += "; "
		}
		results[i].why += "web view restart failed: " + err.Error()
	}
	return results
}

// upgradePollOne polls one yard until it passes or the deadline passes.
func upgradePollOne(ctx context.Context, bin, home, since string, deadline time.Time) upgradePollResult {
	last := "the yard could not be checked"
	for {
		if err := ctx.Err(); err != nil {
			if err == context.DeadlineExceeded || !time.Now().Before(deadline) {
				last = "the health check deadline expired"
			}
			return upgradePollResult{home: home, why: last}
		}
		out, err := upgradeRunInstalled(ctx, bin, home, "upgrade", "--check", "--since", since)
		if ctx.Err() == context.DeadlineExceeded || !time.Now().Before(deadline) {
			return upgradePollResult{home: home, why: "the health check deadline expired"}
		}
		if err == nil {
			return upgradePollResult{home: home, ok: true}
		}
		if ctx.Err() != nil {
			return upgradePollResult{home: home, why: last}
		}
		last = strings.TrimSpace(out)
		if last == "" {
			last = err.Error()
		}
		// Keep only the failing lines, so the marker names the yard with why.
		if lines := strings.Split(last, "\n"); len(lines) > 1 {
			var fails []string
			for _, l := range lines {
				if strings.Contains(l, "fail:") {
					fails = append(fails, strings.TrimSpace(l))
				}
			}
			if len(fails) > 0 {
				last = strings.Join(fails, "; ")
			}
		}
		select {
		case <-ctx.Done():
			return upgradePollResult{home: home, why: last}
		case <-time.After(upgradePollEvery):
		}
	}
}

// upgradeRestoreOn reports whether the run restores refused stores: the
// --no-restore flag overrides the setting, and the setting reads through
// its environment variable.
func upgradeRestoreOn(stored map[string]string, noRestore bool) bool {
	if noRestore {
		return false
	}
	set, ok := store.SettingOf(store.UpgradeRestoreSetting)
	if !ok {
		return true
	}
	v, _ := set.Effective(stored, config.Getenv)
	return v != "false"
}

// webRestartWait is how long a restarted web view gets to answer /health
// before the upgrade goes on to the gate, which fails it there.
var webRestartWait = 30 * time.Second

// upgradeRestartViews restarts the yard's web view where it runs as a
// login service, so the view serves the build the install just wrote, and
// waits until /health answers. With no service installed there is nothing
// to restart: a view started by hand is the operator's to restart, and the
// check names it. Tests replace serviceCtl and serve /health.
func upgradeRestartViews(ctx context.Context, cfg *config.Config) error {
	if !serviceInstalled(cfg, svcWeb) {
		return nil
	}
	manager, name, _, err := unitFile(cfg, svcWeb)
	if err != nil {
		return nil
	}
	if err := ctl(ctx, (&unit{Manager: manager, Name: name}).restartCommand()); err != nil {
		return err
	}
	addr := serviceAddr(cfg, svcWeb)
	if addr == "" {
		return nil
	}
	health := "http://" + dialAddr(addr) + "/health"
	for deadline := time.Now().Add(webRestartWait); time.Now().Before(deadline); {
		if code, err := getStatus(health); err == nil && code != http.StatusNotFound {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollEvery / 5):
		}
	}
	return fmt.Errorf("the web view did not answer %s within %s after the restart", health, webRestartWait)
}
