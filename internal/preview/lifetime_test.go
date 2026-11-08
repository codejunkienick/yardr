package preview

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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
	bindGate  bool
}

// newWatched is the yard of one case: command is the app's, given the port the
// yard chose for it. A backoff of zero is the yard's own.
func newWatched(t *testing.T, byRequest bool, command func(port int) string, backoff time.Duration, watchPortWait, bindGate bool) *watched {
	t.Helper()
	w := &watched{yard: newYard(t), app: "demo", dir: t.TempDir(), port: freePort(t), byRequest: byRequest}
	if bindGate {
		if err := syscall.Mkfifo(filepath.Join(w.dir, "bind.gate"), 0o600); err != nil {
			t.Fatal(err)
		}
		w.bindGate = true
	}
	if watchPortWait {
		w.portWait = make(chan struct{}, 1)
		w.sup.portProbe = func(port int) string {
			host := loopbackHost(port)
			if port == w.port && host == "" {
				select {
				case w.portWait <- struct{}{}:
				default:
				}
			}
			return host
		}
	}
	if backoff > 0 {
		w.sup.Backoffs = []time.Duration{backoff}
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

func (w *watched) releaseBindGate(t *testing.T) {
	t.Helper()
	if !w.bindGate {
		t.Fatal("no bind gate")
	}
	f, err := os.OpenFile(filepath.Join(w.dir, "bind.gate"), os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
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
		watchPortWait bool
		bindGate      bool
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
			command:       func(port int) string { return "cat bind.gate >/dev/null; exec " + loopbackApp(port) },
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
					w := newWatched(t, byRequest, tc.command, tc.backoff, tc.watchPortWait, tc.bindGate)
					w.up(t, tc.listens)
					tc.check(t, w)
				})
			}
		})
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
