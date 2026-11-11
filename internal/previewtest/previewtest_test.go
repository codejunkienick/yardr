package previewtest

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestMain(m *testing.M) {
	MaybeServe()
	os.Exit(m.Run())
}

// The app is the test binary and nothing else. It starts with an empty
// environment, where there is no PATH for a python3 to be found on and no
// resolver setting to wait on, answers on a literal address (IPv6 included,
// which the command has to quote) with the files of its directory, and logs
// each request, which is what a preview's card shows of it.
func TestAppAnswersFromItsDirectoryWithNothingButTheTestBinary(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "::1"} {
		t.Run(host, func(t *testing.T) {
			// The probe only decides the skip, as FreePort fails a test whose host cannot
			// be bound. The app does not take the probe's port: the kernel dealt it from
			// its own range, where another process's server can be handed it before the
			// app binds it.
			probe, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
			if err != nil {
				t.Skipf("%s is unavailable: %v", host, err)
			}
			probe.Close()
			port := FreePort(t, host)
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("hello app"), 0o600); err != nil {
				t.Fatal(err)
			}
			logPath := filepath.Join(t.TempDir(), "app.log")
			logged, err := os.Create(logPath)
			if err != nil {
				t.Fatal(err)
			}
			defer logged.Close()
			cmd := exec.Command("sh", "-c", App(host, port))
			cmd.Dir = dir
			cmd.Env = []string{}
			cmd.Stdout, cmd.Stderr = logged, logged
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				_ = cmd.Wait()
			})

			var body string
			waitFor(t, "the app answering", func() bool {
				resp, err := http.Get("http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/")
				if err != nil {
					return false
				}
				defer resp.Body.Close()
				raw, _ := io.ReadAll(resp.Body)
				body = string(raw)
				return resp.StatusCode == http.StatusOK
			})
			if body != "hello app" {
				t.Errorf("the app answered %q, want the directory's index.html", body)
			}
			// The line is written before the answer, so it is there now.
			if out, _ := os.ReadFile(logPath); !strings.Contains(string(out), " GET /\n") {
				t.Errorf("the app's log = %q, want a line for the request", out)
			}
		})
	}
}

// The app echoes a WebSocket's messages under the first subprotocol
// offered, which is the stand-in a preview's socket is dialled to.
func TestAppEchoesWebSocketUnderFirstOfferedSubprotocol(t *testing.T) {
	port := FreePort(t, "127.0.0.1")
	cmd := exec.Command("sh", "-c", App("127.0.0.1", port))
	cmd.Dir = t.TempDir()
	cmd.Env = []string{}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var conn *websocket.Conn
	waitFor(t, "the app accepting a socket", func() bool {
		c, _, err := websocket.Dial(ctx, "ws://"+net.JoinHostPort("127.0.0.1", strconv.Itoa(port))+"/ws",
			&websocket.DialOptions{Subprotocols: []string{"chat", "other"}, CompressionMode: websocket.CompressionDisabled})
		if err != nil {
			return false
		}
		conn = c
		return true
	})
	defer conn.CloseNow()
	if got := conn.Subprotocol(); got != "chat" {
		t.Fatalf("the chosen subprotocol is %q, want chat", got)
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	typ, body, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if typ != websocket.MessageText || string(body) != "hello" {
		t.Errorf("the echo is %v %q, want text hello", typ, body)
	}
}

// The command goes through sh, so what it quotes has to come out of sh as it
// went in; and what needs no quoting is left alone, so that a card, and a
// page that draws it, shows the command with nothing to escape.
func TestShellWordSurvivesSh(t *testing.T) {
	for _, w := range []string{"", "has space", "it's", `a"b`, "[::1]:80", "$HOME", "`x`", "a;b", "a&b<c>"} {
		// One word, which is w: an empty one must not vanish, nor a long one split.
		out, err := exec.Command("sh", "-c", `set -- `+shellWord(w)+`; printf '%d:%s' $# "$1"`).Output()
		if err != nil || string(out) != "1:"+w {
			t.Errorf("sh read %q as %q (%v), want one word, %q", shellWord(w), out, err, w)
		}
	}
	for _, plain := range []string{"/var/tmp/go-build1/b001/preview.test", "127.0.0.1:8080", "late-wide", "-previewtest-app"} {
		if got := shellWord(plain); got != plain {
			t.Errorf("shellWord(%q) = %q, want it left alone", plain, got)
		}
	}
}

// A port a test names for a preview's app is picked from below the range in which
// the kernel gives out ports by itself, which starts at 32768 on Linux and higher
// on macOS, so that no server another process binds on port 0 is handed it before
// the app binds it.
func TestFreePortIsPickedBelowTheKernelsOwnRange(t *testing.T) {
	const dynamicFrom = 32768
	if quietPortsFrom+quietPortsCount > dynamicFrom {
		t.Fatalf("the ports FreePort picks from reach %d, into the range from %d that the kernel gives out by itself", quietPortsFrom+quietPortsCount-1, dynamicFrom)
	}
	for range 20 {
		if port := FreePort(t, "127.0.0.1"); port < quietPortsFrom || port >= quietPortsFrom+quietPortsCount {
			t.Fatalf("FreePort = %d, want one from %d up to %d", port, quietPortsFrom, quietPortsFrom+quietPortsCount)
		}
	}
}

// A port that a listener on every address holds is no free port, though a bind of
// loopback beside it succeeds where Go sets SO_REUSEADDR (macOS). The supervisor
// refuses a start on it, as a dial of loopback is answered, so a test that names
// the port for a preview fails with "port already in use". A listener of one
// family answers on that family's loopback address alone, and a host of the
// other family binds beside it too, so the draw dials both. The range is the
// held port and nothing else, which makes the held port the only draw there is;
// once the listener is gone the same range gives the port, so it is the held
// port that is refused and not every port.
//
// That last draw races every other process on the machine. The listener's port
// came from the kernel, which deals it again to the next process that binds port
// 0: a server of another package's tests, or another copy of this test. That
// process can hold the port through all the hundred tries of the draw, and the
// draw is right to refuse it. After a failed draw, the test checks with plain
// binds and dials whether a process still holds the port; only a refusal with no
// current holder is the draw's fault.
func TestFreePortSkipsAPortAWildcardListenerHolds(t *testing.T) {
	for _, tc := range []struct{ name, network, addr, host string }{
		{"dual-stack", "tcp", ":0", "127.0.0.1"},
		{"IPv4 only", "tcp4", "0.0.0.0:0", "::1"},
		{"IPv6 only", "tcp6", "[::]:0", "127.0.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wide, err := net.Listen(tc.network, tc.addr)
			if err != nil {
				t.Skipf("%s is unavailable: %v", tc.addr, err)
			}
			defer wide.Close()
			held := wide.Addr().(*net.TCPAddr).Port
			// Check before pickPort fills the listener's accept queue. A timeout
			// from a later dial would not distinguish a full queue from a stalled
			// test process.
			if heldNow(held) == "" {
				t.Fatalf("plain bind and dial checks did not find the listener on %s holding port %d", wide.Addr(), held)
			}
			start := time.Now()
			got, err := pickPort(tc.host, held, 1)
			if err == nil {
				// A draw that took as long as a dial's timeout read a timeout as no
				// answer: this process was not run for that long, and the listener never
				// showed itself.
				t.Fatalf("pickPort(%s, %d, 1) = %d after %v, want no free port: a listener on %s holds it", tc.host, held, got, time.Since(start).Round(time.Millisecond), wide.Addr())
			}
			if !errors.Is(err, errNoFreePort) {
				// The host cannot be bound at all, which FreePort fails a test for and
				// this one skips for, as the app's does.
				t.Skipf("%s is unavailable: %v", tc.host, err)
			}
			wide.Close()
			got, err = pickPort(tc.host, held, 1)
			if err == nil && got == held {
				return
			}
			if why := heldNow(held); why != "" {
				t.Skipf("another process holds port %d once the listener is gone: %s", held, why)
			}
			t.Errorf("pickPort(%s, %d, 1) = %d, %v once the listener is gone, though plain binds and dials find no holder, want the port", tc.host, held, got, err)
		})
	}
}

// heldNow says what shows that a process holds port: a bind of the same
// loopback address or wildcard address is refused because it is in use, or a
// dial of either loopback address connects. A dial timeout is inconclusive: a
// listener with a full queue and a stalled test process both cause one. It
// makes these calls itself, not the draw's, so a draw that refuses a port
// nobody holds is not excused by its own reading of the port.
func heldNow(port int) string {
	for _, endpoint := range []struct{ network, host string }{
		{"tcp", "127.0.0.1"},
		{"tcp", "::1"},
		{"tcp4", "0.0.0.0"},
		{"tcp6", "::"},
	} {
		addr := net.JoinHostPort(endpoint.host, strconv.Itoa(port))
		ln, err := net.Listen(endpoint.network, addr)
		if errors.Is(err, syscall.EADDRINUSE) {
			return "a bind of " + addr + " fails: " + err.Error()
		}
		if err == nil {
			ln.Close()
		}
	}
	for _, h := range []string{"127.0.0.1", "::1"} {
		addr := net.JoinHostPort(h, strconv.Itoa(port))
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			c.Close()
			return "a dial of " + addr + " connects"
		}
	}
	return ""
}

// waitFor polls cond until it holds. The deadline is a fuse against a hang,
// not a measurement.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
