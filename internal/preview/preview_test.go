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
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

// freePort picks a loopback port for a preview's app; previewtest.FreePort says
// from where, and why.
func freePort(t *testing.T) int {
	t.Helper()
	return previewtest.FreePort(t, "127.0.0.1")
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

// cards is what the registry shows of each preview and what it logged last, and
// whether something answers on its port that the supervisor holds no process
// for: that is another process's server, or an app on its way out, and the
// preview's wait for its app takes whatever answers there for the app.
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
		if host := loopbackHost(p.Port); host != "" && !y.sup.Running(p.App) {
			fmt.Fprintf(&b, "    port %d answers on %s, and the supervisor holds no process for the preview\n", p.Port, host)
		}
		lines, _ := LogTail(y.sup.logPath(p.App), 20)
		for _, l := range lines {
			fmt.Fprintf(&b, "    %s\n", l)
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// startWait is how long a preview may take to answer once it runs. The
// supervisor gives a start BindWait to listen (watchBind), and the proxy waits
// that and defaultStartWait more for the port of a preview that runs and has
// not bound it (waitPort); this is as long as the proxy waits, so a preview
// that has not answered by then has been given up on, however loaded the
// machine is. A wait for a start is that bound, not a figure of the test's.
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

// heldPID is the pid of the process the supervisor holds for app, 0 where it
// holds none.
func (y *yard) heldPID(app string) int {
	y.sup.mu.Lock()
	defer y.sup.mu.Unlock()
	if pr := y.sup.running[app]; pr != nil {
		return pr.cmd.Process.Pid
	}
	return 0
}

// backdateStart makes the process the supervisor holds for app as old as one
// that started at: the moment its idle and age rules count from, which is the
// supervisor's own record of the start and no row's. A pass reads it without the
// lock, so this is for a yard that no Run passes over.
func (y *yard) backdateStart(app string, at time.Time) {
	y.sup.mu.Lock()
	defer y.sup.mu.Unlock()
	y.sup.running[app].started = at
}

// flipGate is a store whose flips of a row to approved wait to be let go, one
// each time let is sent to, so a test says what is written before a flip lands.
// Closing let lets go every flip that waits and every later one.
type flipGate struct {
	Store
	arrived atomic.Int32 // flips that have come to the gate
	let     chan struct{}
}

func (g *flipGate) UpdatePreview(ctx context.Context, app string, upd store.PreviewUpdate) (*store.Preview, error) {
	if upd.State == store.PreviewApproved {
		g.arrived.Add(1)
		<-g.let
	}
	return g.Store.UpdatePreview(ctx, app, upd)
}

// rowGate is a store whose reads of the registry's rows, once it is armed, read
// first and then wait to be let go, one each time let is sent to, so a test says
// what happens between a pass's read of the rows and the rest of the pass.
// Closing let lets go every read that waits and every later one. Reads before it
// is armed pass, and a test sets it as the supervisor's store before anything
// runs, as it does flipGate.
type rowGate struct {
	Store
	armed   atomic.Bool
	arrived atomic.Int32 // reads that have come to the gate armed
	let     chan struct{}
}

func (g *rowGate) Previews(ctx context.Context) ([]store.Preview, error) {
	rows, err := g.Store.Previews(ctx)
	if g.armed.Load() {
		g.arrived.Add(1)
		<-g.let
	}
	return rows, err
}

// A failing test's card says when something answers on a preview's port that the
// supervisor holds no process for, which is what a request taken for the app's
// by that answer looks like from the test's side.
func TestCardSaysWhenSomethingElseAnswersOnAPreviewsPort(t *testing.T) {
	y := newYard(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	y.propose(t, "demo", "true", port, false)
	if card, want := y.cards(), fmt.Sprintf("port %d answers on 127.0.0.1", port); !strings.Contains(card, want) {
		t.Fatalf("with a server on the preview's port the card says %q, want it to say %q", card, want)
	}
	ln.Close()
	if card := y.cards(); strings.Contains(card, "answers") {
		t.Fatalf("with nothing on the preview's port the card says %q, want no answer on it", card)
	}
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
		probe.Close()
		port6 := previewtest.FreePort(t, "::1")
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

// Starts asked together for one preview run one process, and the supervisor
// holds it. Each start decides whether to go on where the starts take turns,
// so those that waited their turn find the preview running and leave it as it
// is. Decided before the turn, each of them had spawned one more process, put
// it in the first one's place in running and, as the registry refused to record
// it, killed it there: the first was left running with no one holding it, and
// every request after that was told it had no process.
func TestStartsAskedTogetherRunOneProcess(t *testing.T) {
	y := newYard(t)
	t.Cleanup(func() { y.sup.kill("demo") })
	// The app never listens: a start is judged by the process it holds, and the
	// test is over long before the supervisor's wait for a bind is.
	y.proposeIn(t, "demo", "exec sleep 60", t.TempDir(), freePort(t), true)
	const callers = 8
	var asked sync.WaitGroup
	together := make(chan struct{})
	errs := make([]error, callers)
	for i := range errs {
		asked.Go(func() {
			<-together
			errs[i] = y.sup.Start(context.Background(), "demo")
		})
	}
	close(together)
	asked.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("start %d of %d err = %v, want the running preview left as it is", i+1, callers, err)
		}
	}
	y.sup.mu.Lock()
	held := y.sup.running["demo"]
	y.sup.mu.Unlock()
	if held == nil {
		t.Fatalf("after %d starts the supervisor holds no process; the row is %+v", callers, y.state("demo"))
	}
	if got := y.state("demo"); got.State != store.PreviewRunning || got.PID != held.cmd.Process.Pid {
		t.Fatalf("after %d starts = %+v, want running as the process the supervisor holds, %d", callers, got, held.cmd.Process.Pid)
	}
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
			port := previewtest.FreePort(t, host)
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

// The scan of the interface addresses dials them all at once and answers with the
// first of them, in the order given, that answers. Dialled in turn, the addresses
// that do not answer cost their timeouts one after another; and the first to
// answer of dials made at once would be another address from call to call, in the
// detail of the refusal that records it. The dials here are held and let go by
// channels, so no clock decides either: each waits until all of them are in
// flight, then for the one after it to have answered, which makes the last answer
// first and the first last. A scan that dials in turn never gets past its first
// dial and meets the fuse; one that returns the first to answer returns the last.
func TestFirstAnsweringDialsAtOnce(t *testing.T) {
	targets := []string{"a", "b", "c", "d"}
	// The fuse only turns a hang into a failure.
	fuse, stop := context.WithTimeout(context.Background(), 15*time.Second)
	defer stop()
	// until is true where c is closed, false where the fuse runs out first.
	until := func(c <-chan struct{}) bool {
		select {
		case <-c:
			return true
		case <-fuse.Done():
			return false
		}
	}
	var started atomic.Int32
	allStarted := make(chan struct{})
	answered := make([]chan struct{}, len(targets))
	for i := range answered {
		answered[i] = make(chan struct{})
	}
	got := firstAnswering(targets, func(target string) bool {
		i := slices.Index(targets, target)
		if started.Add(1) == int32(len(targets)) {
			close(allStarted)
		}
		if !until(allStarted) || (i+1 < len(targets) && !until(answered[i+1])) {
			return false
		}
		close(answered[i])
		return true
	})
	if got != targets[0] {
		t.Fatalf("firstAnswering = %q, want %q: it dials all at once (dialled in turn, the first waits out the fuse) and returns the first in order, not the first to answer, which is %q", got, targets[0], targets[len(targets)-1])
	}
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

	// The person's restart, asked for in a crash's backoff, starts the app again
	// before the backoff is over. The timer that then fires finds a process held
	// and the row running, as the crash left it and the new process has made it
	// again: the restart it was to make is made. Flipping that row to approved
	// would leave the process under a row that does not say it runs, never stopped
	// as idle and its exit never recorded.
	t.Run("a restart asked for in a backoff outlasts the timer", func(t *testing.T) {
		ctx := context.Background()
		y := newYard(t)
		y.sup.Backoffs = []time.Duration{time.Hour}
		y.runJoined(t)
		port := freePort(t)
		// The first start crashes; the second, which the person's restart makes, serves.
		y.propose(t, "demo", "if [ -e ran ]; then exec "+loopbackApp(port)+"; fi; touch ran; exit 1", port, true)
		waitFor(t, "first crash enters its backoff", func() bool {
			p := y.state("demo")
			return p.RestartCount == 1 && p.State == store.PreviewRunning
		})
		if _, err := y.st.RestartPreview(ctx, "demo", "ada"); err != nil {
			t.Fatal(err)
		}
		y.waitStart(t, "the restart the person asked for to run the app", func() bool {
			pid := y.heldPID("demo")
			return pid != 0 && y.state("demo").PID == pid
		})
		first := y.heldPID("demo")
		y.sup.restart(ctx, "demo") // the timer of the crash's backoff runs out
		if got := y.state("demo"); got.State != store.PreviewRunning || got.PID != first || y.heldPID("demo") != first {
			t.Fatalf("after the timer the row says %s with pid %d and the supervisor holds pid %d, want running as the process the person's restart started, %d", got.State, got.PID, y.heldPID("demo"), first)
		}
	})

	// A restart that finds no process held and the row running flips the row and
	// starts the app. A start recorded between its check and its flip would be
	// flipped over, as the one the person's restart makes above would be; what
	// keeps one from being recorded there is the lock the starts take their turns
	// by, which the restart holds from its check to its start. The gate holds the
	// restart at its flip, where the lock must be held.
	t.Run("a restart holds the starts' lock from its check to its start", func(t *testing.T) {
		ctx := context.Background()
		y := newYard(t)
		t.Cleanup(func() { y.sup.kill("demo") })
		gate := &flipGate{Store: y.sup.Store, let: make(chan struct{})}
		y.sup.Store = gate
		port := freePort(t)
		y.propose(t, "demo", loopbackApp(port), port, true)
		// The row a crash leaves in its backoff: running, with no process.
		if _, err := y.st.UpdatePreview(ctx, "demo", store.PreviewUpdate{State: store.PreviewRunning}); err != nil {
			t.Fatal(err)
		}
		restarted := make(chan struct{})
		go func() {
			defer close(restarted)
			y.sup.restart(ctx, "demo")
		}()
		// Whichever way the test ends, the restart is let go and has returned before
		// the app it may have started is killed.
		defer func() {
			close(gate.let)
			<-restarted
		}()
		waitFor(t, "the restart to come to its flip", func() bool { return gate.arrived.Load() == 1 })
		if y.sup.startMu.TryLock() {
			y.sup.startMu.Unlock()
			t.Fatal("the restart is between its check and its flip without the starts' lock, so a start could be recorded there")
		}
		gate.let <- struct{}{}
		y.waitStart(t, "the restart to run the app", func() bool {
			pid := y.heldPID("demo")
			return pid != 0 && y.state("demo").PID == pid
		})
		if got := y.state("demo"); got.State != store.PreviewRunning {
			t.Fatalf("after the restart the row says %s, want running", got.State)
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
	ctx := context.Background()
	// No Run: the passes are this test's own, so that each finds the preview as
	// the test has just left it and none comes between two of its steps.
	y := newYard(t)
	port := freePort(t)
	y.propose(t, "demo", previewtest.App("127.0.0.1", port), port, true)
	t.Cleanup(func() { y.sup.kill("demo") })
	// The row is an old one, approved again: its last request is older than the
	// default idle bound. A start writes no request, and the bound counts from the
	// later of the start and the last request, so the passes after the one that
	// starts it do not stop what it started.
	old := time.Now().Add(-5 * time.Hour)
	if _, err := y.st.UpdatePreview(ctx, "demo", store.PreviewUpdate{LastRequest: &old}); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		y.sup.reconcile(ctx, false)
	}
	if got := y.state("demo"); got.State != store.PreviewRunning || got.StateDetail != "" || y.heldPID("demo") == 0 {
		t.Fatalf("after passes over a preview started for a last request 5 h old = %+v, want it running and held, with no reason to stop", got)
	}
	// A request after the start is the later moment: it keeps a process that
	// started hours ago.
	recent := time.Now().Add(-time.Minute)
	y.backdateStart("demo", old)
	if _, err := y.st.UpdatePreview(ctx, "demo", store.PreviewUpdate{LastRequest: &recent}); err != nil {
		t.Fatal(err)
	}
	y.sup.reconcile(ctx, false)
	if got := y.state("demo"); got.State != store.PreviewRunning {
		t.Fatalf("a process that started 5 h ago, asked a minute ago, was stopped: %+v", got)
	}
	// One that started that long ago and was last asked as long ago is idle, and
	// stops once no request is in flight.
	if _, err := y.st.UpdatePreview(ctx, "demo", store.PreviewUpdate{LastRequest: &old}); err != nil {
		t.Fatal(err)
	}
	y.sup.mu.Lock()
	y.sup.inflight["demo"] = 1
	y.sup.mu.Unlock()
	y.sup.reconcile(ctx, false)
	if got := y.state("demo"); got.State != store.PreviewRunning {
		t.Fatalf("active response was stopped as idle: %+v", got)
	}
	y.sup.leave("demo")
	y.sup.reconcile(ctx, false)
	if got := y.state("demo"); got.State != store.PreviewStopped || got.StateDetail != "idle" {
		t.Fatalf("idle preview = %+v, want it stopped as idle", got)
	}
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

// A pass reads what is in flight before it reads the rows. What ends a request,
// a socket or a stream writes its last moment to the row before it gives its
// count back, so a count of zero that the pass saw means the row it read after
// holds that moment. Read the other way round, a pass that has read the rows
// and then waits finds the count at zero once what was in flight has ended, with
// a row from before the moment, a last request older than the idle bound and
// nothing in flight, and stops a preview that was asked for as it was read. The
// gate holds the pass after its rows are read, while what is in flight ends.
func TestPassReadsWhatIsInFlightBeforeItsRows(t *testing.T) {
	ctx := context.Background()
	// Older than the idle bound, which nothing here changes from its default.
	old := time.Now().Add(-2 * store.DefaultPreviewIdle)
	for _, tc := range []struct {
		name string
		// start puts something in flight on a preview that is idle by its row, and
		// returns the app and what ends it, once its count is given back.
		start func(t *testing.T, y *yard) (app string, end func())
	}{
		{"a socket's close", func(t *testing.T, y *yard) (string, func()) {
			port := freePort(t)
			y.propose(t, "demo", previewtest.App("127.0.0.1", port), port, true)
			t.Cleanup(func() { y.sup.kill("demo") })
			must(t, y.sup.Start(ctx, "demo"))
			l := socketLink(t, y)
			head := openPreviewSocket(t, l, "/preview/demo/ws", nil, nil)
			if head.Status != http.StatusSwitchingProtocols {
				t.Fatalf("the head is %+v, want 101", head)
			}
			// The socket has sat quiet longer than the idle bound: the process
			// started and the row was last written before it.
			y.backdateStart("demo", old)
			_, err := y.st.UpdatePreview(ctx, "demo", store.PreviewUpdate{LastRequest: &old})
			must(t, err)
			return "demo", func() {
				must(t, l.SendSocketClose(ctx, head.ID, 1000, "done"))
				waitFor(t, "the socket's counts given back", func() bool {
					sockets, inflight := previewSocketCounts(y, "demo")
					return sockets == 0 && inflight == 0
				})
			}
		}},
		{"a watch stream's end", func(t *testing.T, y *yard) (string, func()) {
			y.sup.WatchPoll, y.sup.WatchSettle = 20*time.Millisecond, 50*time.Millisecond
			p, _, _ := y.watching(t, map[string]string{"index.html": "<body>one</body>"})
			srv := httptest.NewServer(y.sup.Handler())
			t.Cleanup(srv.Close)
			lines, closeIt := stream(t, srv.URL+"/preview/"+p.App+"/"+watchEvents)
			<-lines // the stream is open
			_, err := y.st.UpdatePreview(ctx, p.App, store.PreviewUpdate{LastRequest: &old})
			must(t, err)
			return p.App, func() {
				closeIt()
				waitFor(t, "the stream's count given back", func() bool {
					y.sup.mu.Lock()
					defer y.sup.mu.Unlock()
					return y.sup.inflight[p.App] == 0
				})
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// No Run: the pass is this test's own, held after its rows are read.
			y := newYard(t)
			gate := &rowGate{Store: y.sup.Store, let: make(chan struct{})}
			y.sup.Store = gate
			app, end := tc.start(t, y)
			gate.armed.Store(true)
			// Whichever way the test ends, the read is let go, so the pass ends.
			defer close(gate.let)
			passed := make(chan struct{})
			go func() {
				defer close(passed)
				y.sup.reconcile(ctx, false)
			}()
			waitFor(t, "the pass to have read the rows", func() bool { return gate.arrived.Load() == 1 })
			end()
			// The pass holds the row from before the end; the row now holds its moment.
			if at := y.state(app).LastRequestAt; at == nil || time.Since(*at) > time.Minute {
				t.Fatalf("the row of %s holds %v after the end, want the moment it ended", app, at)
			}
			gate.let <- struct{}{}
			waitFor(t, "the pass to end", func() bool {
				select {
				case <-passed:
					return true
				default:
					return false
				}
			})
			if got := y.state(app); got.State != store.PreviewRunning {
				t.Fatalf("a pass that read its rows before %s stopped the preview, though the row holds the moment it ended: %+v", tc.name, got)
			}
		})
	}
	t.Run("a request admitted after the count snapshot", func(t *testing.T) {
		ctx := context.Background()
		y := newYard(t)
		port := freePort(t)
		y.propose(t, "demo", previewtest.App("127.0.0.1", port), port, true)
		t.Cleanup(func() { y.sup.kill("demo") })
		y.sup.reconcile(ctx, false)
		if got := y.state("demo"); got.State != store.PreviewRunning {
			t.Fatalf("preview did not start: %+v", got)
		}
		old := time.Now().Add(-2 * store.DefaultPreviewIdle)
		y.backdateStart("demo", old)
		if _, err := y.st.UpdatePreview(ctx, "demo", store.PreviewUpdate{LastRequest: &old}); err != nil {
			t.Fatal(err)
		}
		gate := &rowGate{Store: y.sup.Store, let: make(chan struct{})}
		y.sup.Store = gate
		gate.armed.Store(true)
		defer close(gate.let)
		passed := make(chan struct{})
		go func() {
			defer close(passed)
			y.sup.reconcile(ctx, false)
		}()
		waitFor(t, "the pass to have read the rows", func() bool { return gate.arrived.Load() == 1 })
		if !y.sup.enter("demo") {
			t.Fatal("request was not admitted")
		}
		defer y.sup.leave("demo")
		gate.let <- struct{}{}
		waitFor(t, "the pass to end", func() bool {
			select {
			case <-passed:
				return true
			default:
				return false
			}
		})
		if got := y.state("demo"); got.State != store.PreviewRunning {
			t.Fatalf("a request admitted after the count snapshot was stopped as idle: %+v", got)
		}
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

	// Two visitors arrive together at a stopped preview and each reads its row
	// stopped. One flips it to approved and starts the app; the other's flip
	// comes after that start. The flip holds only while the row still says what
	// its request read, so it finds the row running and changes nothing: written
	// over, the row would say approved under a process that runs on, never stopped
	// as idle and its exit never recorded. Left to themselves the two flips land
	// long before either start is recorded, so the gate holds both until both
	// requests have read the row, lets one go, and lets the other go once the
	// first request's start is recorded.
	t.Run("a flip that comes after another request's start leaves the row running", func(t *testing.T) {
		y := newYard(t) // no Run: the requests alone start it
		t.Cleanup(func() { y.sup.kill("demo") })
		gate := &flipGate{Store: y.sup.Store, let: make(chan struct{})}
		y.sup.Store = gate
		port := freePort(t)
		y.propose(t, "demo", previewtest.App("127.0.0.1", port), port, true)
		if _, err := y.st.PreviewStop(context.Background(), "demo", "ada"); err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer(y.sup.Handler())
		defer srv.Close()
		// What is left waiting at the gate is let go before the server waits for
		// its requests, whichever way the test ends.
		defer close(gate.let)
		type answer struct {
			code int
			body string
		}
		answers := make(chan answer, 2)
		for range 2 {
			go func() {
				resp, err := http.Get(srv.URL + "/preview/demo/")
				if err != nil {
					answers <- answer{body: err.Error()}
					return
				}
				defer resp.Body.Close()
				raw, _ := io.ReadAll(resp.Body)
				answers <- answer{resp.StatusCode, string(raw)}
			}()
		}
		waitFor(t, "both requests to come to their flip", func() bool { return gate.arrived.Load() == 2 })
		gate.let <- struct{}{}
		y.waitStart(t, "the first request's start to be recorded", func() bool {
			return y.heldPID("demo") != 0 && y.state("demo").State == store.PreviewRunning
		})
		gate.let <- struct{}{}
		for range 2 {
			select {
			case a := <-answers:
				if a.code != http.StatusOK || !strings.Contains(a.body, "hello preview") {
					t.Errorf("a request's answer = %d %q, want the app's: 200 hello preview", a.code, a.body)
				}
			case <-time.After(y.startWait()):
				t.Fatal("a request was not answered")
			}
		}
		pid := y.heldPID("demo")
		if got := y.state("demo"); pid == 0 || got.State != store.PreviewRunning || got.PID != pid {
			t.Fatalf("after both flips the row says %s with pid %d and the supervisor holds pid %d, want running as that process", got.State, got.PID, pid)
		}
	})

	// A person arrives at a preview that was stopped as idle: its last request is
	// older than the idle bound, and the process their request starts is moments
	// old. The bound counts from the later of the start and the last request, so a
	// pass that comes while the request waits for the app to bind leaves the app
	// alone. Counted from the last request alone, that pass stopped the app the
	// request had just started, and the person was told it was starting. The
	// supervisor's look at the port finds nothing until the test lets it, so that
	// the passes the test makes come while the request still waits, however fast
	// the machine is. The app is the plain one and binds as soon as it starts: an
	// app that bound later would leave its port named for it and held by no one
	// all that time, and a server that another process started on the port then
	// would be taken for the app and answer the request in its place.
	t.Run("an app a request starts is not stopped as idle for a last request older than the bound", func(t *testing.T) {
		ctx := context.Background()
		y := newYard(t) // no Run: the request alone starts it, and the passes are the test's
		t.Cleanup(func() { y.sup.kill("demo") })
		open, probed := make(chan struct{}), make(chan struct{}, 1)
		y.sup.portProbe = func(port int) string {
			select {
			case <-open:
				return loopbackHost(port)
			default:
			}
			select {
			case probed <- struct{}{}:
			default:
			}
			return ""
		}
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("hello"), 0o600); err != nil {
			t.Fatal(err)
		}
		port := freePort(t)
		y.proposeIn(t, "demo", previewtest.App("127.0.0.1", port), dir, port, true)
		// The row as the idle stop of the last visit leaves it.
		old, idle := time.Now().Add(-5*time.Hour), "idle"
		if _, err := y.st.UpdatePreview(ctx, "demo", store.PreviewUpdate{State: store.PreviewStopped, StateDetail: &idle, LastRequest: &old}); err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer(y.sup.Handler())
		defer srv.Close()
		// The request is let go before the server waits for it, whichever way the
		// test ends.
		let := sync.OnceFunc(func() { close(open) })
		defer let()
		type answer struct {
			code int
			body string
		}
		answered := make(chan answer, 1)
		go func() {
			resp, err := http.Get(srv.URL + "/preview/demo/")
			if err != nil {
				answered <- answer{body: err.Error()}
				return
			}
			defer resp.Body.Close()
			raw, _ := io.ReadAll(resp.Body)
			answered <- answer{resp.StatusCode, string(raw)}
		}()
		select {
		case <-probed:
		case <-time.After(y.startWait()):
			t.Fatal("the request did not come to wait for the app's port")
		}
		for range 3 {
			y.sup.reconcile(ctx, false)
		}
		let()
		select {
		case a := <-answered:
			if a.code != http.StatusOK || a.body != "hello" {
				t.Fatalf("the request's answer = %d %q, want the app's: 200 hello", a.code, a.body)
			}
		case <-time.After(y.startWait()):
			t.Fatal("the request was not answered")
		}
		pid := y.heldPID("demo")
		if got := y.state("demo"); pid == 0 || got.State != store.PreviewRunning || got.StateDetail != "" || got.PID != pid {
			t.Fatalf("after the answer the row says %s %q with pid %d and the supervisor holds pid %d, want running as that process", got.State, got.StateDetail, got.PID, pid)
		}
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
