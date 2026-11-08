package preview

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"yardr.dev/yardr/internal/previewtest"
	"yardr.dev/yardr/internal/store"
)

// A preview's app in these tests is this binary again (previewtest).
func TestMain(m *testing.M) {
	previewtest.MaybeServe()
	os.Exit(m.Run())
}

// freePort reserves a loopback port and frees it for the preview.
func freePort(t *testing.T) int {
	return freePortAt(t, "127.0.0.1")
}

func freePortAt(t *testing.T, host string) int {
	t.Helper()
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// interfaceHost is an IPv4 address of one of the machine's interfaces that is
// neither loopback nor link-local, or "" where it has none. A listener bound to
// it is not answered on loopback, so only the supervisor's scan of the
// interfaces (nonLoopbackBind) finds it.
func interfaceHost() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		ip, _, err := net.ParseCIDR(addr.String())
		if err == nil && ip.To4() != nil && ip.IsGlobalUnicast() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
			return ip.String()
		}
	}
	return ""
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	waitUntil(t, what, 15*time.Second, cond)
}

func waitUntil(t *testing.T, what string, within time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

type yard struct {
	st   *store.Store
	home string
	sup  *Supervisor
}

func newYard(t *testing.T) *yard {
	t.Helper()
	home := t.TempDir()
	st, err := store.Open(filepath.Join(home, "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(context.Background(), store.OperatorSetting, "ada", "ada"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close(context.Background()) })
	sup := New(st, home, nil)
	sup.Poll = 10 * time.Millisecond
	// BindWait stays the supervisor's own: the app listens at once, and a
	// figure shorter than the product's would only make a start depend on load.
	sup.Backoffs = []time.Duration{0, 10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond}
	y := &yard{st: st, home: home, sup: sup}
	// A wait that times out says what it waited for; the cards say why it
	// never came (a preview the supervisor gave up on, and what it logged).
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(y.cards())
		}
	})
	return y
}

// cards is what the registry shows of each preview and what it logged last.
func (y *yard) cards() string {
	all, err := y.st.Previews(context.Background())
	if err != nil {
		return "the registry is not read: " + err.Error()
	}
	if len(all) == 0 {
		return "no preview in the registry"
	}
	var b strings.Builder
	for _, p := range all {
		fmt.Fprintf(&b, "preview %s: %s %q, %d restarts\n", p.App, p.State, p.StateDetail, p.RestartCount)
		lines, _ := LogTail(y.sup.logPath(p.App), 20)
		for _, l := range lines {
			fmt.Fprintf(&b, "    %s\n", l)
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// startWait is how long a preview may take to answer once it runs. The
// supervisor gives a start BindWait to listen (watchBind), and the proxy waits
// that and defaultStartWait more for the port of a start it makes itself
// (waitPort); this is as long as the proxy waits, so a preview that has not
// answered by then has been given up on, however loaded the machine is. A wait
// for a start is that bound, not a figure of the test's.
func (y *yard) startWait() time.Duration { return y.sup.bindWait() + defaultStartWait }

func (y *yard) waitStart(t *testing.T, what string, cond func() bool) {
	t.Helper()
	waitUntil(t, what, y.startWait(), cond)
}

func (y *yard) run(t *testing.T) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	go y.sup.Run(ctx)
	t.Cleanup(cancel)
	return cancel
}

func (y *yard) propose(t *testing.T, app, command string, port int, approve bool) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("hello preview"), 0o600); err != nil {
		t.Fatal(err)
	}
	y.proposeIn(t, app, command, dir, port, approve)
}

// proposeIn proposes the preview to run in dir, and approves it when asked.
func (y *yard) proposeIn(t *testing.T, app, command, dir string, port int, approve bool) {
	t.Helper()
	ctx := context.Background()
	if _, err := y.st.ProposePreview(ctx, store.NewPreview{App: app, Command: command, Dir: dir, Port: port}, "builder"); err != nil {
		t.Fatal(err)
	}
	if approve {
		if _, err := y.st.ApprovePreview(ctx, app, "ada"); err != nil {
			t.Fatal(err)
		}
	}
}

// gitTree is a directory with one commit, for a preview to read its depot
// tree from; the second result runs git in it.
func gitTree(t *testing.T) (string, func(args ...string) string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q")
	run("config", "user.email", "a@b.c")
	run("config", "user.name", "a")
	run("commit", "-q", "--allow-empty", "-m", "one")
	return dir, run
}

func (y *yard) state(app string) store.Preview {
	p, err := y.st.GetPreview(context.Background(), app)
	if err != nil {
		return store.Preview{State: "gone"}
	}
	return *p
}

func TestApprovedPreviewStartsAndAnswersThroughEndpoint(t *testing.T) {
	y := newYard(t)
	y.run(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	busyPort := ln.Addr().(*net.TCPAddr).Port
	y.propose(t, "busy", "true", busyPort, true)
	waitFor(t, "busy preview failed without exposing the existing listener", func() bool {
		return y.state("busy").State == store.PreviewFailed
	})
	if got := y.state("busy"); !strings.Contains(got.StateDetail, "port already in use") {
		t.Fatalf("busy port detail = %q", got.StateDetail)
	}
	ln.Close()
	port := freePort(t)
	t.Setenv("PORT", "9000")
	t.Setenv("PREVIEW_APP", "wrong")
	t.Setenv("PREVIEW_PORT", "9001")
	for _, k := range []string{"SSH_AUTH_SOCK", "GPG_AGENT_SOCKET", "DB_PASSWORD", "GOOGLE_APPLICATION_CREDENTIALS", "GH_TOKEN"} {
		t.Setenv(k, "held")
	}
	env := previewEnv("demo", port)
	for _, kv := range env {
		if strings.HasSuffix(kv, "=held") {
			t.Errorf("preview environment keeps %s", kv)
		}
	}
	for key, want := range map[string]string{
		"PORT": strconv.Itoa(port), "PREVIEW_APP": "demo", "PREVIEW_PORT": strconv.Itoa(port),
	} {
		var values []string
		for _, kv := range env {
			k, v, _ := strings.Cut(kv, "=")
			if k == key {
				values = append(values, v)
			}
		}
		if len(values) != 1 || values[0] != want {
			t.Errorf("preview environment %s = %q, want one value %q", key, values, want)
		}
	}
	y.propose(t, "demo", previewtest.App("127.0.0.1", port), port, true)

	waitFor(t, "preview running", func() bool { return y.state("demo").State == store.PreviewRunning })
	srv := httptest.NewServer(y.sup.Handler())
	defer srv.Close()
	var body string
	y.waitStart(t, "answer through the supervisor endpoint", func() bool {
		resp, err := http.Get(srv.URL + "/preview/demo/")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		body = string(raw)
		return resp.StatusCode == http.StatusOK && strings.Contains(body, "hello preview")
	})
	if got := y.state("demo"); got.LastRequestAt == nil {
		t.Fatalf("last request not recorded: %+v", got)
	}
	waitFor(t, "output in the preview log", func() bool {
		lines, err := LogTail(filepath.Join(y.home, "previews", "demo.log"), 100)
		return err == nil && len(lines) > 0
	})
	// A direct call to the app's loopback port answers too: the
	// supervisor's endpoint is a proxy in front of it, not a copy.
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("direct loopback status = %d", resp.StatusCode)
	}

	// The yard-side endpoint removes edge credentials while keeping the
	// app's own cookies.
	headers := make(http.Header)
	headers.Set("X-Edge-Token", "token")
	headers.Set("X-Edge-JWT", "jwt")
	headers.Set("Edge-Yard", "yard")
	headers.Set("Cookie", "app=one; ory_kratos_session=kratos; edge_yard=yard; __Host-edge_preview=edge; theme=dark")
	stripEdgeCredentials(headers)
	for _, name := range edgeHeaders {
		if headers.Get(name) != "" {
			t.Errorf("edge header %s reached the app: %q", name, headers.Get(name))
		}
	}
	if got, want := headers.Get("Cookie"), "app=one; theme=dark"; got != want {
		t.Errorf("forwarded cookies = %q, want %q", got, want)
	}

	// IPv6 loopback is an allowed bind and must be reachable through the
	// same yard-side endpoint.
	t.Run("ipv6 loopback", func(t *testing.T) {
		probe, err := net.Listen("tcp", net.JoinHostPort("::1", "0"))
		if err != nil {
			t.Skipf("IPv6 loopback is unavailable: %v", err)
		}
		port6 := probe.Addr().(*net.TCPAddr).Port
		probe.Close()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("hello ipv6"), 0o600); err != nil {
			t.Fatal(err)
		}
		y.proposeIn(t, "v6", previewtest.App("::1", port6), dir, port6, true)
		waitFor(t, "IPv6 preview running", func() bool { return y.state("v6").State == store.PreviewRunning })
		y.waitStart(t, "IPv6 preview answer", func() bool {
			resp, err := http.Get(srv.URL + "/preview/v6/")
			if err != nil {
				return false
			}
			defer resp.Body.Close()
			raw, _ := io.ReadAll(resp.Body)
			return resp.StatusCode == http.StatusOK && strings.Contains(string(raw), "hello ipv6")
		})
	})
}

func TestStartWithoutApprovalIsRefused(t *testing.T) {
	y := newYard(t)
	port := freePort(t)
	y.propose(t, "demo", "true", port, false)
	if err := y.sup.Start(context.Background(), "demo"); err == nil || !strings.Contains(err.Error(), "no approval") {
		t.Fatalf("start err = %v", err)
	}
	if got := y.state("demo"); got.State != store.PreviewPending {
		t.Fatalf("state = %+v", got)
	}
}

// A start runs the approved command only on the depot tree the approval
// was given on. An unchanged tree starts as it always did; one whose
// commit has moved is not started: the approval is taken back, the card
// waits pending for a new one, and the command runs once it is given. The
// command writes its marker outside the tree, so it leaves the tree as it
// found it. (The dirty mark is the store's rule, in its own test.)
func TestStartRunsOnlyOnTheApprovedTree(t *testing.T) {
	ctx := context.Background()
	// start runs the approved form and waits for the command to have run
	// and exited; marker is the file the command touches.
	start := func(t *testing.T, y *yard, marker string) {
		t.Helper()
		if err := y.sup.Start(ctx, "demo"); err != nil {
			t.Fatalf("start: %v", err)
		}
		waitFor(t, "the approved command to run and exit", func() bool {
			_, err := os.Stat(marker)
			return err == nil && y.state("demo").StateDetail == "exited"
		})
	}

	t.Run("an unchanged tree starts", func(t *testing.T) {
		y := newYard(t)
		dir, _ := gitTree(t)
		marker := filepath.Join(t.TempDir(), "ran")
		y.proposeIn(t, "demo", "touch "+marker, dir, freePort(t), true)
		start(t, y, marker)
	})

	t.Run("another tree waits for a new approval", func(t *testing.T) {
		y := newYard(t)
		dir, git := gitTree(t)
		marker := filepath.Join(t.TempDir(), "ran")
		y.proposeIn(t, "demo", "touch "+marker, dir, freePort(t), true)
		git("commit", "-q", "--allow-empty", "-m", "two")
		err := y.sup.Start(ctx, "demo")
		if err == nil || !strings.Contains(err.Error(), "approval is taken back") {
			t.Fatalf("start on another tree err = %v, want the approval taken back", err)
		}
		head := git("rev-parse", "HEAD")
		got := y.state("demo")
		if got.State != store.PreviewPending || got.StateDetail != store.PreviewTreeChangedDetail || got.Approval != nil || got.Commit != head {
			t.Fatalf("after the refused start = %+v, want pending at %q with no approval", got, head)
		}
		// No pass over the registry, and no second start, runs a card that
		// waits for its person.
		y.sup.reconcile(ctx, false)
		if err := y.sup.Start(ctx, "demo"); err == nil {
			t.Fatal("start of a card with no approval succeeded")
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatal("the command ran on a tree its approval was not given on")
		}
		if _, err := y.st.ApprovePreview(ctx, "demo", "ada"); err != nil {
			t.Fatal(err)
		}
		start(t, y, marker)
	})

	// A preview that runs when the tree moves and its restart is asked for
	// goes back to pending too, so what ran is stopped with the approval.
	t.Run("a running preview whose approval is taken back is stopped", func(t *testing.T) {
		y := newYard(t)
		y.run(t)
		dir, git := gitTree(t)
		port := freePort(t)
		y.proposeIn(t, "demo", previewtest.App("127.0.0.1", port), dir, port, true)
		waitFor(t, "preview running", func() bool { return y.state("demo").State == store.PreviewRunning })
		git("commit", "-q", "--allow-empty", "-m", "two")
		if _, err := y.st.RestartPreview(ctx, "demo", "ada"); err == nil || !strings.Contains(err.Error(), "approval is taken back") {
			t.Fatalf("restart on another tree err = %v, want the approval taken back", err)
		}
		waitFor(t, "the taken-back preview's process to be gone", func() bool {
			return !y.sup.Running("demo") && listeningAddress(port) == ""
		})
		if got := y.state("demo"); got.State != store.PreviewPending || got.Approval != nil {
			t.Fatalf("after the process stopped = %+v, want pending with no approval", got)
		}
	})
}

func TestNonLoopbackBindIsRefused(t *testing.T) {
	hosts := []string{"0.0.0.0"}
	if host := interfaceHost(); host != "" {
		hosts = append(hosts, host)
	}
	for i, host := range hosts {
		t.Run(host, func(t *testing.T) {
			y := newYard(t)
			y.run(t)
			app := "wide"
			if i > 0 {
				app = "specific"
			}
			port := freePortAt(t, host)
			y.propose(t, app, previewtest.App(host, port), port, true)
			y.waitStart(t, "preview refused for an outside bind", func() bool {
				return y.state(app).State == store.PreviewRefused
			})
			if got := y.state(app); !strings.HasPrefix(got.StateDetail, "bound ") {
				t.Fatalf("refusal detail = %q", got.StateDetail)
			}
		})
	}
	t.Run("binds outside after loopback", func(t *testing.T) {
		y := newYard(t)
		y.run(t)
		port := freePort(t)
		y.propose(t, "late-wide", previewtest.LateWide(port), port, true)
		y.waitStart(t, "later outside bind refused", func() bool {
			return y.state("late-wide").State == store.PreviewRefused
		})
	})
}

func TestCrashLoopEndsCrashed(t *testing.T) {
	y := newYard(t)
	y.run(t)
	port := freePort(t)
	y.propose(t, "flap", "echo crashing; exit 1", port, true)
	waitFor(t, "preview crashed after five exits", func() bool {
		return y.state("flap").State == store.PreviewCrashed
	})
	if got := y.state("flap"); got.RestartCount < 3 {
		t.Fatalf("restarts = %d, want the backoff restarts recorded", got.RestartCount)
	}
	lines, err := LogTail(filepath.Join(y.home, "previews", "flap.log"), 100)
	if err != nil || len(lines) == 0 || !strings.Contains(strings.Join(lines, "\n"), "crashing") {
		t.Fatalf("crash log = %v, %v", lines, err)
	}

	t.Run("a stop survives an outstanding backoff", func(t *testing.T) {
		y := newYard(t)
		y.sup.Backoffs = []time.Duration{time.Hour}
		y.run(t)
		y.propose(t, "stoppable", "echo crashing; exit 1", freePort(t), true)
		waitFor(t, "first crash enters its backoff", func() bool {
			p := y.state("stoppable")
			return p.RestartCount == 1 && p.State == store.PreviewRunning
		})
		if _, err := y.st.PreviewStop(context.Background(), "stoppable", "builder"); err != nil {
			t.Fatal(err)
		}
		y.sup.restart(context.Background(), "stoppable")
		if got := y.state("stoppable"); got.State != store.PreviewStopped || y.sup.Running("stoppable") {
			t.Fatalf("stop lost to a delayed restart: %+v", got)
		}
	})
}

func TestCleanExitIsNotRestarted(t *testing.T) {
	y := newYard(t)
	y.run(t)
	port := freePort(t)
	y.propose(t, "done", "echo finished; exit 0", port, true)
	waitFor(t, "clean exit stopped", func() bool {
		got := y.state("done")
		return got.State == store.PreviewStopped && got.StateDetail == "exited"
	})
	if got := y.state("done"); got.RestartCount != 0 {
		t.Fatalf("clean exit restarted %d times", got.RestartCount)
	}
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	y.propose(t, "orphan", "sleep 60 & echo $! > "+strconv.Quote(pidFile)+"; exit 0", freePort(t), true)
	waitFor(t, "clean exit stopped and killed its background child", func() bool {
		return y.state("orphan").State == store.PreviewStopped
	})
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read background child pid: %v", err)
	}
	child, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse background child pid %q: %v", data, err)
	}
	waitFor(t, "background child is gone", func() bool {
		return errors.Is(syscall.Kill(child, 0), syscall.ESRCH)
	})
}

func TestIdleStopAndRebootRule(t *testing.T) {
	y := newYard(t)
	port := freePort(t)
	y.propose(t, "demo", previewtest.App("127.0.0.1", port), port, true)
	// Backdate the last request past the default idle bound once
	// running: the next pass owes it the idle stop.
	y.run(t)
	waitFor(t, "preview running", func() bool { return y.state("demo").State == store.PreviewRunning })
	old := time.Now().Add(-5 * time.Hour)
	if _, err := y.st.UpdatePreview(context.Background(), "demo", store.PreviewUpdate{LastRequest: &old}); err != nil {
		t.Fatal(err)
	}
	y.sup.mu.Lock()
	y.sup.inflight["demo"] = 1
	y.sup.mu.Unlock()
	y.sup.reconcile(context.Background(), false)
	if got := y.state("demo"); got.State != store.PreviewRunning {
		t.Fatalf("active response was stopped as idle: %+v", got)
	}
	y.sup.leave("demo")
	waitFor(t, "idle preview stopped", func() bool {
		got := y.state("demo")
		return got.State == store.PreviewStopped && got.StateDetail == "idle"
	})
	// A row left running when the supervisor starts is the reboot
	// rule: stopped, not restarted.
	y2 := newYard(t)
	y2.propose(t, "demo", "true", freePort(t), true)
	started := time.Now()
	if _, err := y2.st.UpdatePreview(context.Background(), "demo", store.PreviewUpdate{
		State: store.PreviewRunning, PID: 424242, StartedAt: &started}); err != nil {
		t.Fatal(err)
	}
	y2.run(t)
	waitFor(t, "reboot-stopped preview", func() bool {
		got := y2.state("demo")
		return got.State == store.PreviewStopped && got.StateDetail == "reboot"
	})
}

// A restart the person asks for on the card stops the running process
// and starts the approved form again: the ask is a stopped row the
// supervisor flips back to approved, and the next pass starts it. A stop
// that wins while the process stops stands instead of the restart.
func TestRestartAskStopsAndStartsAgain(t *testing.T) {
	ctx := context.Background()
	y := newYard(t)
	y.run(t)
	port := freePort(t)
	y.propose(t, "demo", previewtest.App("127.0.0.1", port), port, true)
	waitFor(t, "preview running", func() bool { return y.state("demo").State == store.PreviewRunning })
	first := y.state("demo").PID
	if _, err := y.st.RestartPreview(ctx, "demo", "ada"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "restarted preview running", func() bool {
		got := y.state("demo")
		return got.State == store.PreviewRunning && got.PID != 0 && got.PID != first
	})
	if got := y.state("demo"); got.StateDetail != "" {
		t.Fatalf("after restart detail = %q", got.StateDetail)
	}
	// The completion reads the row again: a stop that won while the
	// process stopped stands.
	y2 := newYard(t)
	y2.propose(t, "held", "true", freePort(t), true)
	if _, err := y2.st.RestartPreview(ctx, "held", "ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := y2.st.PreviewStop(ctx, "held", "ada"); err != nil {
		t.Fatal(err)
	}
	y2.sup.finishRestart(ctx, "held")
	if got := y2.state("held"); got.State != store.PreviewStopped || got.StateDetail != "stopped" {
		t.Fatalf("after winning stop = %+v", got)
	}
}

func TestMemoryWatcherStopsOverCap(t *testing.T) {
	y := newYard(t)
	y.sup.MemLimit = 1 // every process is over a one-byte cap
	y.sup.MemoryTick = 10 * time.Millisecond
	y.sup.MemoryHold = 20 * time.Millisecond
	y.run(t)
	port := freePort(t)
	y.propose(t, "demo", previewtest.App("127.0.0.1", port), port, true)
	waitFor(t, "preview stopped for memory", func() bool {
		got := y.state("demo")
		return got.State == store.PreviewStopped && got.StateDetail == "memory"
	})
}

func TestOnDemandStartThroughEndpoint(t *testing.T) {
	y := newYard(t) // no Run: the request alone must start it
	t.Cleanup(func() { y.sup.kill("demo") })
	dir, git := gitTree(t)
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("hello preview"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "index.html")
	git("commit", "-q", "-m", "index")
	port := freePort(t)
	y.proposeIn(t, "demo", previewtest.App("127.0.0.1", port), dir, port, true)
	srv := httptest.NewServer(y.sup.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/preview/demo/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), "hello preview") {
		t.Fatalf("on-demand answer = %d %s", resp.StatusCode, raw)
	}
	if got := y.state("demo"); got.State != store.PreviewRunning || got.LastRequestAt == nil {
		t.Fatalf("after on-demand start = %+v", got)
	}
	if _, err := y.st.PreviewStop(context.Background(), "demo", "ada"); err != nil {
		t.Fatal(err)
	}
	y.sup.kill("demo")
	git("commit", "-q", "--allow-empty", "-m", "two")
	resp, err = http.Get(srv.URL + "/preview/demo/")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || string(raw) != "preview demo cannot start\n" {
		t.Fatalf("on-demand start on another tree = %d %q, want 503 and no more than that it cannot start", resp.StatusCode, raw)
	}
	got := y.state("demo")
	if got.State != store.PreviewPending || got.StateDetail != store.PreviewTreeChangedDetail || got.Approval != nil || got.Commit != git("rev-parse", "HEAD") {
		t.Fatalf("after refused on-demand start = %+v, want pending at the new tree without approval", got)
	}
	if y.sup.Running("demo") || listeningAddress(port) != "" {
		t.Fatal("on-demand start ran the command on another tree")
	}
	if _, err := y.st.ApprovePreview(context.Background(), "demo", "ada"); err != nil {
		t.Fatal(err)
	}
	resp, err = http.Get(srv.URL + "/preview/demo/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), "hello preview") {
		t.Fatalf("on-demand answer after a new approval = %d %s", resp.StatusCode, raw)
	}

	// A visitor whose arrival starts a preview that then fails to start is told
	// that it cannot start and no more. The reason names what the machine holds,
	// an interface address or a path under the yard's home, and is for the
	// operator: the row holds it, and so does the log. cannotStart asks for the
	// approved preview app, stopped as its person's last visit left it, and
	// checks all three; leak is what the reason names.
	cannotStart := func(t *testing.T, y *yard, app, leak string) {
		t.Helper()
		var logged bytes.Buffer
		y.sup.Log = slog.New(slog.NewTextHandler(&logged, nil))
		if _, err := y.st.PreviewStop(context.Background(), app, "ada"); err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer(y.sup.Handler())
		defer srv.Close()
		resp, err := http.Get(srv.URL + "/preview/" + app + "/")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if want := "preview " + app + " cannot start\n"; resp.StatusCode != http.StatusServiceUnavailable || string(raw) != want {
			t.Fatalf("answer to the start that failed = %d %q, want 503 %q", resp.StatusCode, raw, want)
		}
		got := y.state(app)
		if got.State != store.PreviewFailed || !strings.Contains(got.StateDetail, leak) {
			t.Fatalf("after the start that failed = %+v, want failed with a detail that names %q", got, leak)
		}
		if !strings.Contains(logged.String(), got.StateDetail) {
			t.Fatalf("the log = %q, want the reason the row holds: %q", logged.String(), got.StateDetail)
		}
	}
	t.Run("a port held on an interface address", func(t *testing.T) {
		host := interfaceHost()
		if host == "" {
			t.Skip("the machine has no interface address besides loopback")
		}
		ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
		if err != nil {
			t.Skipf("the machine's interface address %s takes no bind: %v", host, err)
		}
		t.Cleanup(func() { ln.Close() })
		port := ln.Addr().(*net.TCPAddr).Port
		addr := net.JoinHostPort(host, strconv.Itoa(port))
		if got := listeningAddress(port); got != addr {
			t.Skipf("the machine finds no listener on its interface address: %q, want %q", got, addr)
		}
		y := newYard(t)
		y.propose(t, "held", "true", port, true)
		cannotStart(t, y, "held", addr)
	})
	t.Run("a log directory that cannot be made", func(t *testing.T) {
		y := newYard(t)
		// A file stands where the directory is to be.
		if err := os.WriteFile(filepath.Join(y.home, "previews"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		y.propose(t, "nolog", "true", freePort(t), true)
		cannotStart(t, y, "nolog", y.home)
	})
}

func TestInFlightCap(t *testing.T) {
	y := newYard(t)
	for i := 0; i < store.PreviewMaxInFlight; i++ {
		if !y.sup.enter("demo") {
			t.Fatalf("enter %d refused under the cap", i)
		}
	}
	if y.sup.enter("demo") {
		t.Fatal("21st request in flight accepted")
	}
	y.sup.leave("demo")
	if !y.sup.enter("demo") {
		t.Fatal("request after one left refused")
	}
}
