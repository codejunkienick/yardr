package previewtest

import (
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
