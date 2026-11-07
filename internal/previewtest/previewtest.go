// Package previewtest is the app a preview's tests run: the test binary
// itself, started again by the supervisor as a listener that answers from the
// directory it starts in and logs each request.
//
// It stands where `python3 -m http.server` stood. That server binds, asks the
// resolver for its own name (socket.getfqdn), and only then listens. A macOS
// runner's resolver can answer that after more seconds than the supervisor lets
// a start take (Supervisor.BindWait); the supervisor then gives the preview up
// as "no listen", and every test that waits for the app fails.
// This app names no host and starts no other program, so when it listens
// depends on the machine's load alone. It is no new executable file either:
// macOS assesses the first run of one, for seconds on a busy machine
// (docs/testing.md), and the test binary has run already.
//
// A package whose tests start it calls MaybeServe first in its TestMain.
package previewtest

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// mark is the first argument of a test binary started as the app. A test
// binary's own flags all begin -test., so no run of the tests carries it.
const mark = "-previewtest-app"

// lifetime ends an app nobody stopped. The supervisor stops its previews when
// its context ends, but a test binary that exits first leaves the copy it
// started running; this is the longest a run of tests lasts, go test's own
// timeout.
const lifetime = 10 * time.Minute

// App is the command, for the supervisor's `sh -c`, of the app listening on
// host and port. The host is an address, not a name: "127.0.0.1", "::1",
// "0.0.0.0" or an interface's own.
func App(host string, port int) string {
	return command("serve", net.JoinHostPort(host, strconv.Itoa(port)))
}

// LateWide is the command of an app that listens on loopback until its first
// caller has been let in, and then on every address instead: a preview that
// binds outside later than it starts. Its first caller is the supervisor's own
// look at the port, so the app moves when the supervisor has seen it bound and
// not after a time of the test's.
func LateWide(port int) string {
	return command("late-wide", strconv.Itoa(port))
}

func command(mode, arg string) string {
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}
	words := []string{exe, mark, mode, arg}
	for i, w := range words {
		words[i] = shellWord(w)
	}
	return strings.Join(words, " ")
}

// shellWord quotes w for sh unless it is made of characters sh leaves alone,
// so that a card shows the command as it reads and a page that draws it has
// nothing to escape.
func shellWord(w string) string {
	plain := w != ""
	for _, r := range w {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_@%+=:,./-", r)) {
			plain = false
		}
	}
	if plain {
		return w
	}
	return "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
}

// MaybeServe returns at once in a process that App and LateWide did not start.
// In one they did, it serves until it is stopped, and does not return.
func MaybeServe() {
	if len(os.Args) < 2 || os.Args[1] != mark {
		return
	}
	var err error
	if len(os.Args) != 4 {
		err = fmt.Errorf("want a mode and its argument, got %q", os.Args[2:])
	} else {
		time.AfterFunc(lifetime, func() { os.Exit(0) })
		switch os.Args[2] {
		case "serve":
			err = serve(os.Args[3])
		case "late-wide":
			err = lateWide(os.Args[3])
		default:
			err = fmt.Errorf("no mode %q", os.Args[2])
		}
	}
	fmt.Fprintln(os.Stderr, "previewtest:", err)
	os.Exit(1)
}

// serve answers on addr with the files of the directory it runs in, and logs
// one line to stderr for each request, before the answer. It logs nothing
// before the first request.
func serve(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	files := http.FileServer(http.Dir("."))
	return http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(os.Stderr, "%s %s %s\n", r.RemoteAddr, r.Method, r.URL.Path)
		files.ServeHTTP(w, r)
	}))
}

// lateWide listens on loopback alone until one caller has connected, and
// then on every address, answering none of them.
func lateWide(port string) error {
	local, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		return err
	}
	first, err := local.Accept()
	if err != nil {
		return err
	}
	first.Close()
	local.Close()
	wide, err := net.Listen("tcp", net.JoinHostPort("0.0.0.0", port))
	if err != nil {
		return err
	}
	for {
		c, err := wide.Accept()
		if err != nil {
			return err
		}
		c.Close()
	}
}
