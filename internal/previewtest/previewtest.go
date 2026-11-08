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
	"sync"
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

// afterRequest is how long WideAfterRequest's app waits, once it has answered
// its first request, before it listens outside. The supervisor looks for a bind
// outside in passes over the machine's addresses, and a look that has been told
// to stop still finishes the pass it is in, so a bind made at once could be
// seen by that pass. This is longer than a pass, so only a look that goes on
// sees it.
const afterRequest = 1500 * time.Millisecond

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

// WideAfterRequest is the command of an app that answers from its directory on
// loopback, as App does, and listens on every address instead a while after
// the first request it has answered: a preview that binds outside after the
// request that started it is over. A caller that connects and asks for
// nothing, as the supervisor's own look at the port does, is no request, so
// where LateWide moves on that look, this moves only on a person's request.
func WideAfterRequest(port int) string {
	return command("wide-after-request", strconv.Itoa(port))
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

// MaybeServe returns at once in a process that App, LateWide and WideAfterRequest
// did not start. In one they did, it serves until it is stopped, and does not
// return.
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
		case "wide-after-request":
			err = wideAfterRequest(os.Args[3])
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
	return http.Serve(ln, files(nil))
}

// files is what serve answers with: the files of the directory the app runs
// in, and a line to stderr for each request, before the answer. answered, when
// there is one, is called once the files have been written.
func files(answered func()) http.Handler {
	dir := http.FileServer(http.Dir("."))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(os.Stderr, "%s %s %s\n", r.RemoteAddr, r.Method, r.URL.Path)
		dir.ServeHTTP(w, r)
		if answered != nil {
			answered()
		}
	})
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
	return goWide(local, port)
}

// wideAfterRequest serves on loopback as serve does, and afterRequest after the
// first request it has answered, listens on every address instead, answering
// none of them. A caller that asks for nothing is not a request: the handler
// is not reached until one has asked.
func wideAfterRequest(port string) error {
	local, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		return err
	}
	answered := make(chan struct{})
	var once sync.Once
	go http.Serve(local, files(func() { once.Do(func() { close(answered) }) }))
	<-answered
	time.Sleep(afterRequest)
	return goWide(local, port)
}

// goWide gives up the loopback listener for one on every address, and answers
// none of the callers it gets.
func goWide(local net.Listener, port string) error {
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
