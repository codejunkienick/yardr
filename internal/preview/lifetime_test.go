package preview

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"yardr.dev/yardr/internal/previewtest"
	"yardr.dev/yardr/internal/store"
)

// loopbackApp is the command of an app that answers on loopback with the files
// of its directory.
func loopbackApp(port int) string { return previewtest.App("127.0.0.1", port) }

// watched is a yard with one approved preview, demo, which the starter the
// test chose brings up: a request, or Run's own pass over the registry. Its
// directory holds an index.html of "hello", which is what its app answers.
type watched struct {
	*yard
	app, dir  string
	port      int
	srv       *httptest.Server
	byRequest bool
	portWait  chan struct{}
	gate      *os.File // the test's end of the app's bind gate (newBindGate), nil where it has none
}

// bindGateName is the FIFO in its directory that a gated app waits on.
const bindGateName = "bind.gate"

// gateWait is the shell's wait for the release of a bind gate: one line from the
// gate's FIFO.
const gateWait = "read -r _ <" + bindGateName

// gatedApp is the command of an app that answers as loopbackApp's does, once its
// test has released it (releaseBindGate): until then it listens on nothing.
func gatedApp(port int) string { return gateWait + "; exec " + loopbackApp(port) }

// newBindGate makes the gate's FIFO in dir and returns the test's end of it, held
// open for reading and writing until the test ends. A release is a line written
// to it (releaseBindGate), not the open and close of a writer. On macOS a reader
// that already waits on a FIFO can miss a writer that opens and closes it, and
// then waits for ever in its read: an app released that way is let through only
// when the two come in the right order (yardr-eseg). A line stays in the FIFO for
// as long as the test holds it, whichever comes first, and a FIFO opened for
// reading and writing opens at once on macOS and Linux alike, so a release waits
// for no reader either.
func newBindGate(t *testing.T, dir string) *os.File {
	t.Helper()
	path := filepath.Join(dir, bindGateName)
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	gate, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { gate.Close() })
	return gate
}

// newWatched is the yard of one case: command is the app's, given the port the
// yard chose for it. A backoff of zero is the yard's own; a bind wait of zero
// is the watched minute.
func newWatched(t *testing.T, byRequest bool, command func(port int) string, backoff, bindWait time.Duration, watchPortWait, bindGate, waitTimeout bool) *watched {
	t.Helper()
	w := &watched{yard: newYard(t), app: "demo", dir: t.TempDir(), port: freePort(t), byRequest: byRequest}
	if bindGate {
		w.gate = newBindGate(t, w.dir)
	}
	if watchPortWait {
		w.portWait = make(chan struct{}, 1)
		w.sup.portProbe = func(port int) (string, portState) {
			host, state := probeLoopback(port)
			if port == w.port && state != portBound {
				select {
				case w.portWait <- struct{}{}:
				default:
				}
			}
			return host, state
		}
	}
	if waitTimeout {
		// The waiter's looks time out throughout, as a timeout through
		// probeLoopback reports unknown; watchBind keeps its real dials, so
		// its no-listen verdict still lands at the bind wait.
		w.sup.portProbe = func(port int) (string, portState) {
			if port == w.port {
				return "", portUnknown
			}
			return probeLoopback(port)
		}
	}
	if backoff > 0 {
		w.sup.Backoffs = []time.Duration{backoff}
	}
	// A start may take a minute to bind, not the supervisor's own ten seconds
	// as in newYard: the app sits held at the FIFO while start, request, probe,
	// release, exec and bind run in turn, and on a loaded machine that staging
	// outlasts the product's patience, so watchBind's verdict would kill an app
	// the test was about to release. Production apps are never gated, so the
	// room is the test's own; a case that stages the verdict itself gives its
	// own wait.
	w.sup.BindWait = time.Minute
	if bindWait > 0 {
		w.sup.BindWait = bindWait
	}
	if err := os.WriteFile(filepath.Join(w.dir, "index.html"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if byRequest {
		// Passes of Run an hour apart leave the start to the request. The row
		// is the one a person arrives at: approved, and stopped since.
		w.sup.Poll = time.Hour
		w.proposeIn(t, w.app, command(w.port), w.dir, w.port, true)
		if _, err := w.st.PreviewStop(context.Background(), w.app, "ada"); err != nil {
			t.Fatal(err)
		}
		w.runJoined(t)
		waitFor(t, "Run to begin", w.runBegun)
	} else {
		w.runJoined(t)
		w.proposeIn(t, w.app, command(w.port), w.dir, w.port, true)
	}
	w.srv = httptest.NewServer(w.sup.Handler())
	t.Cleanup(w.srv.Close)
	return w
}

// runJoined runs the supervisor until the test ends, and waits then for it to
// have stopped what it started, so no app outlives the test. stop does that at
// once.
func (y *yard) runJoined(t *testing.T) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		y.sup.Run(ctx)
	}()
	stop = func() {
		cancel()
		waitFor(t, "Run to return", func() bool {
			select {
			case <-done:
				return true
			default:
				return false
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

// runBegun says whether Run has begun, which it shows by keeping its context.
func (y *yard) runBegun() bool {
	select {
	case <-y.sup.RunReady():
		return true
	default:
		return false
	}
}

// up waits for the preview to be started as its starter does it. A request
// starts its own, so there is nothing to wait for; Run's start is waited for,
// and for the app to listen where it is to.
func (w *watched) up(t *testing.T, listens bool) {
	t.Helper()
	if w.byRequest {
		return
	}
	waitFor(t, "Run to start the preview", func() bool { return w.state(w.app).State == store.PreviewRunning })
	if listens {
		waitFor(t, "the app to listen", func() bool { return loopbackHost(w.port) != "" })
	}
}

// get asks the supervisor's endpoint for the preview and says what it
// answered.
func (w *watched) get(t *testing.T) (int, string) {
	t.Helper()
	resp, err := http.Get(w.srv.URL + "/preview/" + w.app + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

type previewAnswer struct {
	code int
	body string
	err  error
}

// getAsync asks the supervisor without making the test goroutine wait for its
// answer, so the test can release or stop an app while the request waits.
func (w *watched) getAsync() <-chan previewAnswer {
	answer := make(chan previewAnswer, 1)
	go func() {
		resp, err := http.Get(w.srv.URL + "/preview/" + w.app + "/")
		if err != nil {
			answer <- previewAnswer{err: err}
			return
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		answer <- previewAnswer{code: resp.StatusCode, body: string(raw), err: err}
	}()
	return answer
}

// waitForPortWait proves the request made a failed port probe before the test
// releases or stops the app. An early answer means it did not wait.
func (w *watched) waitForPortWait(t *testing.T, answer <-chan previewAnswer) {
	t.Helper()
	select {
	case <-w.portWait:
	case got := <-answer:
		t.Fatalf("request answered before waiting for the port: %d %q (%v)", got.code, got.body, got.err)
	case <-time.After(15 * time.Second):
		t.Fatal("request did not check the unbound port")
	}
}

// releaseBindGate lets the app held at its gate go on to bind.
func (w *watched) releaseBindGate(t *testing.T) {
	t.Helper()
	if w.gate == nil {
		t.Fatal("no bind gate")
	}
	if _, err := w.gate.WriteString("go\n"); err != nil {
		t.Fatal(err)
	}
}

func takePreviewAnswer(t *testing.T, answer <-chan previewAnswer) previewAnswer {
	t.Helper()
	select {
	case got := <-answer:
		if got.err != nil {
			t.Fatal(got.err)
		}
		return got
	case <-time.After(15 * time.Second):
		t.Fatal("request did not finish")
		return previewAnswer{}
	}
}

// answered asks once and wants the app's own answer, then waits for the
// request to be over: the count in flight is the last thing the handler gives
// back before net/http ends the request's context.
func (w *watched) answered(t *testing.T) {
	t.Helper()
	if code, body := w.get(t); code != http.StatusOK || body != "hello" {
		t.Fatalf("the app's answer = %d %q, want 200 hello", code, body)
	}
	waitFor(t, "the request to end", func() bool {
		w.sup.mu.Lock()
		defer w.sup.mu.Unlock()
		return w.sup.inflight[w.app] == 0
	})
}

// A preview that a request starts is watched as one Run starts. The request
// ends with its answer, and what watches the process has to go on without it:
// the guard that it binds loopback alone, the handling of its exit and the
// verdict on an app that never listens. Each case runs against a preview each
// starter starts and asks the same of both.
func TestOnDemandStartIsWatchedAsRunStartsIt(t *testing.T) {
	cases := []struct {
		name          string
		command       func(port int) string
		listens       bool          // the app listens, so Run's start waits for it to
		backoff       time.Duration // what a crash waits before its restart, in place of the yard's
		bindWait      time.Duration // what a start may take to bind, in place of the watched minute
		watchPortWait bool
		bindGate      bool
		waitTimeout   bool
		check         func(t *testing.T, w *watched)
	}{
		{
			// The app binds outside after the request is over, and later than the
			// pass of the wide-bind scan that a watcher whose request has ended
			// still finishes (previewtest.WideAfterRequest), so only a watcher
			// that outlives the request sees it.
			name: "a wide bind after the request ends is refused", command: previewtest.WideAfterRequest, listens: true,
			check: func(t *testing.T, w *watched) {
				w.answered(t)
				waitFor(t, "the wide bind to be refused", func() bool { return w.state(w.app).State == store.PreviewRefused })
				if got := w.state(w.app); !strings.HasPrefix(got.StateDetail, "bound ") || w.sup.Running(w.app) {
					t.Fatalf("after the wide bind = %+v running %v, want refused with the address it bound and the app stopped", got, w.sup.Running(w.app))
				}
			},
		},
		{
			// The restart waits out a backoff that a request comes in however
			// loaded the machine is: one that came after the restart would meet an
			// app that runs and has not bound yet, and wait for its bind, not for
			// the restart this case is about.
			name: "a killed app restarts under the crash rule", command: loopbackApp, listens: true,
			backoff: time.Second,
			check: func(t *testing.T, w *watched) {
				w.answered(t)
				first := w.state(w.app).PID
				if first <= 1 {
					// A kill of group 0 or -1 would reach more than the app.
					t.Fatalf("the running preview's pid = %d, want the process to kill", first)
				}
				// The process group is the one the supervisor made: its leader's pid.
				if err := syscall.Kill(-first, syscall.SIGKILL); err != nil {
					t.Fatal(err)
				}
				// Once the crash is recorded no process runs until the restart has
				// been made, and the row says running all the while. A request that
				// comes in that time waits for the restart, and is not told the app
				// cannot start.
				waitFor(t, "the crash to be recorded", func() bool { return w.state(w.app).RestartCount >= 1 })
				w.answered(t)
				waitFor(t, "the killed app to run again", func() bool {
					got := w.state(w.app)
					return got.RestartCount >= 1 && got.State == store.PreviewRunning && got.PID != 0 && got.PID != first
				})
				waitFor(t, "the restarted app to listen", func() bool { return loopbackHost(w.port) != "" })
				w.answered(t)
			},
		},
		{
			// The request waits for a restart that is under way: the row says
			// running or approved, though no process runs for a moment. That is
			// not giving the app up.
			name: "an app that crashes at its first start is served by its restart", listens: true,
			command: func(port int) string {
				return "if [ -e ran ]; then exec " + loopbackApp(port) + "; fi; touch ran; exit 1"
			},
			check: func(t *testing.T, w *watched) {
				w.answered(t)
				if got := w.state(w.app); got.RestartCount < 1 {
					t.Fatalf("after the answer = %+v, want the crash of the first start recorded", got)
				}
			},
		},
		{
			// The request arrives while the process runs but its app is held at a
			// FIFO until the test knows the request has found the unbound port.
			name:          "an app that binds after the request comes is waited for",
			command:       gatedApp,
			watchPortWait: true, bindGate: true,
			check: func(t *testing.T, w *watched) {
				answer := w.getAsync()
				w.waitForPortWait(t, answer)
				w.releaseBindGate(t)
				got := takePreviewAnswer(t, answer)
				if got.code != http.StatusOK || got.body != "hello" {
					t.Fatalf("the app's answer = %d %q, want 200 hello", got.code, got.body)
				}
				waitFor(t, "the request to end", func() bool {
					w.sup.mu.Lock()
					defer w.sup.mu.Unlock()
					return w.sup.inflight[w.app] == 0
				})
			},
		},
		{
			name:          "an app given up while the request waits for its bind",
			command:       func(int) string { return "sleep 60" },
			watchPortWait: true,
			check: func(t *testing.T, w *watched) {
				answer := w.getAsync()
				w.waitForPortWait(t, answer)
				// Stop the held process and record the supervisor's no-listen
				// verdict while the request is inside the wait.
				w.sup.kill(w.app)
				detail := "no listen"
				if _, err := w.st.UpdatePreview(context.Background(), w.app, store.PreviewUpdate{
					ExpectedState: store.PreviewRunning,
					State:         store.PreviewFailed,
					StateDetail:   &detail,
					PID:           -1,
				}); err != nil {
					t.Fatal(err)
				}
				got := takePreviewAnswer(t, answer)
				if got.code != http.StatusServiceUnavailable || !strings.Contains(got.body, "preview demo is failed") {
					t.Fatalf("the answer = %d %q, want the row's: 503 preview demo is failed", got.code, got.body)
				}
				if row := w.state(w.app); row.State != store.PreviewFailed || row.StateDetail != "no listen" || w.sup.Running(w.app) {
					t.Fatalf("after the answer = %+v running %v, want failed with no listen and the app stopped", row, w.sup.Running(w.app))
				}
			},
		},
		{
			// The gate is held and never released, so the no-listen verdict is
			// certain and the waiter's answer is the row's, pinned with the bind
			// wait lowered instead of any loaded rerun. The case above stages the
			// same answer by hand while the request waits; this one lets the
			// supervisor give it.
			name:     "an app that never binds is failed while the request waits for it",
			command:  gatedApp,
			bindWait: 5 * time.Second, bindGate: true,
			check: func(t *testing.T, w *watched) {
				answer := w.getAsync()
				got := takePreviewAnswer(t, answer)
				if got.code != http.StatusServiceUnavailable || !strings.Contains(got.body, "preview demo is failed") {
					t.Fatalf("the answer = %d %q, want the row's: 503 preview demo is failed", got.code, got.body)
				}
				if row := w.state(w.app); row.State != store.PreviewFailed || row.StateDetail != "no listen" || w.sup.Running(w.app) {
					t.Fatalf("after the answer = %+v running %v, want failed with no listen and the app stopped", row, w.sup.Running(w.app))
				}
				waitFor(t, "the request to end", func() bool {
					w.sup.mu.Lock()
					defer w.sup.mu.Unlock()
					return w.sup.inflight[w.app] == 0
				})
			},
		},
		{
			// The same verdict as the case above, with the waiter's looks
			// timing out throughout: a timeout is unknown to waitPort, so it
			// keeps waiting for the verdict instead of proxying into a
			// refused dial and answering 502 while the row still runs.
			name:     "an app that never binds is failed while the request waits through dial timeouts",
			command:  gatedApp,
			bindWait: 5 * time.Second, bindGate: true, waitTimeout: true,
			check: func(t *testing.T, w *watched) {
				answer := w.getAsync()
				got := takePreviewAnswer(t, answer)
				if got.code != http.StatusServiceUnavailable || !strings.Contains(got.body, "preview demo is failed") {
					t.Fatalf("the answer = %d %q, want the row's: 503 preview demo is failed", got.code, got.body)
				}
				if row := w.state(w.app); row.State != store.PreviewFailed || row.StateDetail != "no listen" || w.sup.Running(w.app) {
					t.Fatalf("after the answer = %+v running %v, want failed with no listen and the app stopped", row, w.sup.Running(w.app))
				}
				waitFor(t, "the request to end", func() bool {
					w.sup.mu.Lock()
					defer w.sup.mu.Unlock()
					return w.sup.inflight[w.app] == 0
				})
			},
		},
	}
	for _, byRequest := range []bool{true, false} {
		name := "started by Run"
		if byRequest {
			name = "started by a request"
		}
		t.Run(name, func(t *testing.T) {
			// Each case owns its yard, its port and its app, so they run together.
			t.Parallel()
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					w := newWatched(t, byRequest, tc.command, tc.backoff, tc.bindWait, tc.watchPortWait, tc.bindGate, tc.waitTimeout)
					w.up(t, tc.listens)
					tc.check(t, w)
				})
			}
		})
	}
}

// A release of a bind gate waits for no app and is kept for the one that comes
// later; one made as the app waits lets it through too. The release that was the
// open and close of a writer did neither: it waited for a reader to open the FIFO,
// and a reader that waited already could miss it (yardr-eseg). The app here is
// gatedApp's own wait, with a mark before it and nothing started after it.
func TestBindGateReleaseIsKeptAndWaitsForNoApp(t *testing.T) {
	dir := t.TempDir()
	w := &watched{gate: newBindGate(t, dir)}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// The app says that it has started before it comes to the gate.
	app := func() *exec.Cmd {
		cmd := exec.CommandContext(ctx, "sh", "-c", ": >started; "+gateWait)
		cmd.Dir = dir
		return cmd
	}
	// The release comes first, with no app to read it, and the app that comes
	// after is let through at once.
	w.releaseBindGate(t)
	if err := app().Run(); err != nil {
		t.Fatalf("the app that came after the release: %v, want it let through", err)
	}
	// The release comes once the app has started, as it comes to the gate or
	// waits there.
	if err := os.Remove(filepath.Join(dir, "started")); err != nil {
		t.Fatal(err)
	}
	waiting := app()
	if err := waiting.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the app to start", func() bool {
		_, err := os.Stat(filepath.Join(dir, "started"))
		return err == nil
	})
	w.releaseBindGate(t)
	if err := waiting.Wait(); err != nil {
		t.Fatalf("the app waiting at the gate: %v, want it let through", err)
	}
}

// Run stops what it started as it returns, and nothing is left to watch what
// a later start would run: that start is refused, and the command does not
// run.
func TestStartAfterRunEndsIsRefused(t *testing.T) {
	y := newYard(t)
	stop := y.runJoined(t)
	waitFor(t, "Run to begin", y.runBegun)
	stop()
	marker := filepath.Join(t.TempDir(), "ran")
	y.proposeIn(t, "late", "touch "+marker, t.TempDir(), freePort(t), true)
	err := y.sup.Start(context.Background(), "late")
	if err == nil || !strings.Contains(err.Error(), "supervisor has stopped") {
		t.Fatalf("start after Run ended err = %v, want it refused", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the command ran with no supervisor left to watch it")
	}
	if got := y.state("late"); got.State != store.PreviewApproved {
		t.Fatalf("after the refused start = %+v, want the row as it was, approved", got)
	}
}
