package cli

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// YARDR_DAEMON_PPROF has the daemon serve its profiles on a unix socket
// at that path, for as long as the daemon runs; without it the daemon
// listens on nothing of its own.
func TestDaemonPprofServedOnlyWhenAsked(t *testing.T) {
	get := func(sock string) (int, error) {
		hc := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", sock)
			},
		}}
		resp, err := hc.Get("http://pprof/debug/pprof/")
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, nil
	}

	// The trial's server reads this process too: it must not serve its
	// own profiles into the socket count below.
	t.Setenv("YARDR_PPROF", "")
	// A short directory of its own: t.TempDir names the test, and the
	// socket's path would not fit sun_path on macOS.
	dir, err := os.MkdirTemp("", "ydpp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	t.Setenv("YARDR_DAEMON_PPROF", sock)
	y := daemonUp(t)
	if code, err := get(sock); err != nil || code != http.StatusOK {
		t.Fatalf("GET /debug/pprof/ on %s = %d, %v; want 200", sock, code, err)
	}
	if fi, err := os.Stat(sock); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("the daemon pprof socket: %v, %v; want mode 0600", fi, err)
	}
	y.end(func() { y.cancel() })
	if _, err := get(sock); err == nil {
		t.Fatal("the daemon pprof socket still answers after the daemon stopped")
	}

	// Unset: the home holds the server's own socket and no other.
	t.Setenv("YARDR_DAEMON_PPROF", "")
	y = daemonUp(t)
	socks := 0
	filepath.WalkDir(y.home, func(_ string, d os.DirEntry, _ error) error {
		if d != nil && d.Type()&os.ModeSocket != 0 {
			socks++
		}
		return nil
	})
	if socks != 1 {
		t.Fatalf("%d sockets in the home of a daemon without YARDR_DAEMON_PPROF; want the server's only", socks)
	}
	y.end(func() { y.cancel() })
}
