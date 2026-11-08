package preview

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"yardr.dev/yardr/internal/edgelink"
	"yardr.dev/yardr/internal/previewtest"
	"yardr.dev/yardr/internal/standin"
	"yardr.dev/yardr/internal/store"
)

// socketLink starts a preview link from the supervisor's handler to a
// stand-in edge, as KeepEdge does in the server, and returns the link
// the edge welcomed: the way a socket frame arrives at the supervisor.
func socketLink(t *testing.T, y *yard) *standin.EdgeLink {
	t.Helper()
	edge, err := standin.NewEdge()
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(edge)
	t.Cleanup(srv.Close)
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	link := &edgelink.Link{
		Edge: store.Edge{URL: srv.URL, Key: edge.PublicKey(), Handle: "ada"},
		Key:  key, Handler: y.sup.Handler(), HTTP: srv.Client(),
		Backoff: 10 * time.Millisecond, BackoffMax: 20 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		link.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Errorf("the preview link never stopped")
		}
	})
	select {
	case l := <-edge.Links():
		return l
	case <-time.After(10 * time.Second):
		t.Fatal("the preview link never came up at the edge")
		return nil
	}
}

func openPreviewSocket(t *testing.T, l *standin.EdgeLink, path string, header http.Header, protocols []string) standin.EdgeSocketHead {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	head, err := l.OpenSocket(ctx, l.Socket(path, header, protocols))
	if err != nil {
		t.Fatalf("socket to %s was not answered: %v", path, err)
	}
	return head
}

func sendPreviewSocket(t *testing.T, l *standin.EdgeLink, id, kind string, body []byte) {
	t.Helper()
	if err := l.SendSocketData(context.Background(), id, kind, body, false); err != nil {
		t.Fatal(err)
	}
}

func nextPreviewSocketMessage(t *testing.T, l *standin.EdgeLink, id string) (string, []byte) {
	t.Helper()
	var body []byte
	kind := ""
	for {
		select {
		case d := <-l.SocketDataEvents():
			if d.ID != id {
				t.Fatalf("a chunk for %s while %s was waited on", d.ID, id)
			}
			if len(d.Body) > 64<<10 {
				t.Fatalf("a chunk of %d bytes, over the link's chunk bound", len(d.Body))
			}
			kind = d.Kind
			body = append(body, d.Body...)
			if !d.More {
				return kind, body
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("no whole message for %s; %d bytes so far", id, len(body))
			return "", nil
		}
	}
}

func previewSocketCounts(y *yard, app string) (sockets, inflight int) {
	y.sup.mu.Lock()
	defer y.sup.mu.Unlock()
	return y.sup.sockets[app], y.sup.inflight[app]
}

// A socket frame over the link is dialled to the app, a message
// crosses both ways under the subprotocol the app chose, the socket
// counts in flight, and the app is dialled with no edge credential
// and the loopback Origin of the dial's target.
func TestSocketThroughSupervisorRelaysUnderChosenSubprotocol(t *testing.T) {
	y := newYard(t)
	y.run(t)
	port := freePort(t)
	y.propose(t, "demo", previewtest.App("127.0.0.1", port), port, true)
	waitFor(t, "preview running", func() bool { return y.state("demo").State == store.PreviewRunning })
	l := socketLink(t, y)

	header := http.Header{
		"Origin":       {"https://p-demo.preview.example.net"},
		"X-Edge-Token": {"edge-secret"},
		"X-Edge-Jwt":   {"jwt-secret"},
		"Edge-Yard":    {"yard-secret"},
		"Cookie":       {"app=one; ory_kratos_session=kratos-secret; edge_yard=yard-secret; __Host-edge_preview=edge-secret; theme=dark"},
	}
	head := openPreviewSocket(t, l, "/preview/demo/ws?x=1", header, []string{"chat", "other"})
	if head.Status != http.StatusSwitchingProtocols || head.Protocol != "chat" {
		t.Fatalf("the head is %+v, want 101 with the app's chosen subprotocol chat", head)
	}
	if sockets, inflight := previewSocketCounts(y, "demo"); sockets != 1 || inflight != 1 {
		t.Fatalf("counts with a socket open = %d sockets, %d in flight, want 1 and 1", sockets, inflight)
	}
	sendPreviewSocket(t, l, head.ID, edgelink.SocketText, []byte("hello"))
	if kind, body := nextPreviewSocketMessage(t, l, head.ID); kind != edgelink.SocketText || string(body) != "hello" {
		t.Errorf("the echo is %s %q, want text hello", kind, body)
	}
	sendPreviewSocket(t, l, head.ID, edgelink.SocketBinary, []byte{0, 1, 2})
	if kind, body := nextPreviewSocketMessage(t, l, head.ID); kind != edgelink.SocketBinary || string(body) != "\x00\x01\x02" {
		t.Errorf("the binary echo is %s %q, want the bytes sent", kind, body)
	}
	waitFor(t, "the app's handshake in the preview log", func() bool {
		raw, err := os.ReadFile(y.sup.logPath("demo"))
		if err != nil {
			return false
		}
		log := string(raw)
		return strings.Contains(log, `origin="http://127.0.0.1:`+strconv.Itoa(port)+`"`) &&
			strings.Contains(log, `edge-token=""`) && strings.Contains(log, `edge-jwt=""`) &&
			strings.Contains(log, `cookie="app=one; theme=dark"`) && strings.Contains(log, `preview-app="demo"`)
	})
	if raw, _ := os.ReadFile(y.sup.logPath("demo")); strings.Contains(string(raw), "secret") {
		t.Errorf("an edge credential reached the app; its log is %q", raw)
	}
	if err := l.SendSocketClose(context.Background(), head.ID, 1000, "the browser is done"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the socket's count given back", func() bool {
		sockets, inflight := previewSocketCounts(y, "demo")
		return sockets == 0 && inflight == 0
	})
}

// An upgrade for a stopped approved preview starts it and is
// answered; one for a refused preview is 404 and starts nothing.
func TestSocketStartsStoppedApprovedAndRefusesRefused(t *testing.T) {
	t.Run("a stopped approved preview is started", func(t *testing.T) {
		y := newYard(t)
		y.run(t)
		port := freePort(t)
		y.propose(t, "demo", previewtest.App("127.0.0.1", port), port, true)
		waitFor(t, "preview running", func() bool { return y.state("demo").State == store.PreviewRunning })
		if _, err := y.st.PreviewStop(context.Background(), "demo", "ada"); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "preview stopped with no process", func() bool {
			return y.state("demo").State == store.PreviewStopped && !y.sup.Running("demo") && listeningAddress(port) == ""
		})
		l := socketLink(t, y)
		head := openPreviewSocket(t, l, "/preview/demo/ws", nil, []string{"chat"})
		if head.Status != http.StatusSwitchingProtocols || head.Protocol != "chat" {
			t.Fatalf("the head is %+v, want 101 chat from the started app", head)
		}
		if got := y.state("demo"); got.State != store.PreviewRunning {
			t.Fatalf("after the upgrade the preview is %+v, want running", got)
		}
		sendPreviewSocket(t, l, head.ID, edgelink.SocketText, []byte("started"))
		if _, body := nextPreviewSocketMessage(t, l, head.ID); string(body) != "started" {
			t.Errorf("the started app echoed %q", body)
		}
	})

	t.Run("a refused preview is 404 with nothing started", func(t *testing.T) {
		y := newYard(t)
		y.run(t)
		port := freePort(t)
		y.propose(t, "demo", previewtest.App("127.0.0.1", port), port, false)
		if _, err := y.st.RefusePreview(context.Background(), "demo", "ada"); err != nil {
			t.Fatal(err)
		}
		l := socketLink(t, y)
		head := openPreviewSocket(t, l, "/preview/demo/ws", nil, nil)
		if head.Status != http.StatusNotFound {
			t.Fatalf("the head is %+v, want 404 for a refused preview", head)
		}
		if got := y.state("demo"); got.State != store.PreviewRefused || y.sup.Running("demo") || listeningAddress(port) != "" {
			t.Fatalf("after the refused upgrade = %+v running %v, want refused with nothing started", got, y.sup.Running("demo"))
		}
	})
}

// A socket alone holds its preview past a shortened idle bound; the
// bound runs again from its last socket's close.
func TestSocketHoldsPreviewPastIdleBoundUntilLastSocketCloses(t *testing.T) {
	ctx := context.Background()
	y := newYard(t)
	// The loop makes the passes, as it does in the server: the one that
	// comes as the last socket closes must not stop the preview before the
	// row holds the moment it closed.
	y.run(t)
	port := freePort(t)
	y.propose(t, "demo", previewtest.App("127.0.0.1", port), port, true)
	waitFor(t, "preview running", func() bool { return y.state("demo").State == store.PreviewRunning })
	l := socketLink(t, y)
	head := openPreviewSocket(t, l, "/preview/demo/ws", nil, nil)
	if head.Status != http.StatusSwitchingProtocols {
		t.Fatalf("the head is %+v, want 101", head)
	}
	opened := y.state("demo").LastRequestAt
	if opened == nil {
		t.Fatal("the socket opening did not set the preview's last-request moment")
	}
	// The bound is shortened only now: the loop counts a preview nobody has
	// asked for from its start, so a slow link could have it stopped before
	// the socket was there.
	const idle = 300 * time.Millisecond
	if err := y.st.SetSetting(ctx, store.PreviewIdleSetting, idle.String(), "ada"); err != nil {
		t.Fatal(err)
	}
	// Past the idle bound with the socket as the only activity.
	waitFor(t, "the idle bound past the socket's opening", func() bool { return time.Since(*opened) > idle })
	if got := y.state("demo"); got.State != store.PreviewRunning {
		t.Fatalf("a preview held by a socket alone was stopped as idle: %+v", got)
	}
	if err := l.SendSocketClose(ctx, head.ID, 1000, "done"); err != nil {
		t.Fatal(err)
	}
	var closedAt time.Time
	waitFor(t, "the idle clock refreshed at the socket's close", func() bool {
		last := y.state("demo").LastRequestAt
		if last == nil || !last.After(*opened) {
			return false
		}
		closedAt = *last
		return true
	})
	waitFor(t, "the socket's count given back", func() bool {
		sockets, inflight := previewSocketCounts(y, "demo")
		return sockets == 0 && inflight == 0
	})
	// A slow machine gets this test to the preview late, so it does not
	// ask whether the preview still runs. It reads when the stop happened
	// from the stop's own event: load can only put that later than the
	// bound, never sooner.
	var stoppedAt time.Time
	waitFor(t, "the preview stopped idle", func() bool {
		if got := y.state("demo"); got.State != store.PreviewStopped || got.StateDetail != "idle" {
			return false
		}
		evs, err := y.st.EventsOfKind(ctx, "preview_stopped", 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range evs {
			if e.Data["app"] == "demo" && e.Data["detail"] == "idle" {
				stoppedAt = e.At
				return true
			}
		}
		return false
	})
	if gap := stoppedAt.Sub(closedAt); gap < idle {
		t.Fatalf("the preview stopped %v after its last socket closed, want no sooner than the idle bound %v", gap, idle)
	}
}

// touchGate is a store whose writes of a preview's last-request moment wait to
// be let go once it is armed, one each time let is sent to, so a test says what
// a pass finds while a moment is on its way to the row. Closing let lets go
// every write that waits and every later one. Writes before it is armed pass,
// so a test arms it after the socket it is about is open, and sets it as the
// supervisor's store before anything runs, as flipGate's users do.
type touchGate struct {
	Store
	armed   atomic.Bool
	arrived atomic.Int32 // writes that have come to the gate armed
	let     chan struct{}
}

func (g *touchGate) UpdatePreview(ctx context.Context, app string, upd store.PreviewUpdate) (*store.Preview, error) {
	if upd.LastRequest != nil && g.armed.Load() {
		g.arrived.Add(1)
		<-g.let
	}
	return g.Store.UpdatePreview(ctx, app, upd)
}

// The idle clock of a preview restarts at its last socket's close, and the
// socket keeps the preview from idling until it does: a pass that comes after
// the socket is gone and before the close's moment is in the row finds a row a
// bound old, so it must still find the socket in flight and leave the preview
// alone. The gate holds the close's write while that pass is made.
func TestSocketLastCloseHoldsItsPreviewUntilItsMomentIsWritten(t *testing.T) {
	ctx := context.Background()
	// No Run: the pass is this test's own, made while the write is held.
	y := newYard(t)
	gate := &touchGate{Store: y.sup.Store, let: make(chan struct{})}
	y.sup.Store = gate
	port := freePort(t)
	y.propose(t, "demo", previewtest.App("127.0.0.1", port), port, true)
	t.Cleanup(func() { y.sup.kill("demo") })
	if err := y.sup.Start(ctx, "demo"); err != nil {
		t.Fatal(err)
	}
	l := socketLink(t, y)
	head := openPreviewSocket(t, l, "/preview/demo/ws", nil, nil)
	if head.Status != http.StatusSwitchingProtocols {
		t.Fatalf("the head is %+v, want 101", head)
	}
	// The socket has sat quiet longer than the idle bound (the default, which
	// nothing here changes): the process started and the row was last written
	// before it. Frames refresh the row at most once a minute, and pings do
	// not cross.
	old := time.Now().Add(-2 * store.DefaultPreviewIdle)
	y.backdateStart("demo", old)
	if _, err := y.st.UpdatePreview(ctx, "demo", store.PreviewUpdate{LastRequest: &old}); err != nil {
		t.Fatal(err)
	}
	gate.armed.Store(true)
	// Whichever way the test ends, the write is let go, so the socket's close
	// returns before the link is shut.
	defer close(gate.let)
	if err := l.SendSocketClose(ctx, head.ID, 1000, "done"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the close's moment to come to its write", func() bool { return gate.arrived.Load() == 1 })
	y.sup.reconcile(ctx, false)
	if got := y.state("demo"); got.State != store.PreviewRunning {
		t.Fatalf("a preview whose last socket is closing, its moment not yet in the row, was stopped: %+v", got)
	}
	gate.let <- struct{}{}
	waitFor(t, "the socket's count given back", func() bool {
		sockets, inflight := previewSocketCounts(y, "demo")
		return sockets == 0 && inflight == 0
	})
	// Counts at zero, the moment is in the row: the clock runs from the close.
	y.sup.reconcile(ctx, false)
	if got := y.state("demo"); got.State != store.PreviewRunning {
		t.Fatalf("a preview whose last socket has just closed was stopped as idle: %+v", got)
	}
}

// The eleventh socket of a preview is refused, naming the preview.
func TestEleventhSocketRefusedNamingPreview(t *testing.T) {
	y := newYard(t)
	y.run(t)
	port := freePort(t)
	y.propose(t, "demo", previewtest.App("127.0.0.1", port), port, true)
	waitFor(t, "preview running", func() bool { return y.state("demo").State == store.PreviewRunning })
	l := socketLink(t, y)
	var heads []standin.EdgeSocketHead
	for i := range maxSocketsPerPreview {
		head := openPreviewSocket(t, l, "/preview/demo/ws", nil, nil)
		if head.Status != http.StatusSwitchingProtocols {
			t.Fatalf("socket %d answered %+v, want 101", i+1, head)
		}
		heads = append(heads, head)
	}
	head := openPreviewSocket(t, l, "/preview/demo/ws", nil, nil)
	if head.Status != http.StatusServiceUnavailable || !strings.Contains(string(head.Body), "preview demo: too many sockets open") {
		t.Fatalf("the eleventh head is %+v, want 503 naming the preview and its sockets", head)
	}
	for _, h := range heads {
		_ = l.SendSocketClose(context.Background(), h.ID, 1000, "done")
	}
}

// A watch's upgrade is refused 501 by the supervisor.
func TestSocketForWatchRefused501(t *testing.T) {
	y := newYard(t)
	p, _, _ := y.watching(t, map[string]string{"index.html": "<body>one</body>"})
	l := socketLink(t, y)
	head := openPreviewSocket(t, l, "/preview/"+p.App+"/ws", nil, nil)
	if head.Status != http.StatusNotImplemented {
		t.Fatalf("a watch's head is %+v, want 501", head)
	}
}

// Where the app refuses the dial, its own status and body answer the
// head, and the dial carried the loopback Origin of its target with
// no edge credential to the app that refused it.
func TestSocketAnsweredWithAppsRefusal(t *testing.T) {
	y := newYard(t)
	seen := make(chan http.Header, 1)
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Clone()
		http.Error(w, "no upgrades here", http.StatusForbidden)
	}))
	defer app.Close()
	u, err := url.Parse(app.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	y.propose(t, "demo", "true", port, true)
	if _, err := y.st.UpdatePreview(context.Background(), "demo", store.PreviewUpdate{State: store.PreviewRunning}); err != nil {
		t.Fatal(err)
	}
	l := socketLink(t, y)
	header := http.Header{"Origin": {"https://p-demo.preview.example.net"}, "X-Edge-Token": {"edge-secret"}}
	head := openPreviewSocket(t, l, "/preview/demo/ws", header, []string{"chat"})
	if head.Status != http.StatusForbidden || !strings.Contains(string(head.Body), "no upgrades here") {
		t.Fatalf("the head is %+v, want the app's 403 and its words", head)
	}
	select {
	case h := <-seen:
		if h.Get("Origin") != app.URL || h.Get("X-Edge-Token") != "" {
			t.Errorf("the app was dialled with origin %q and an edge token %q, want the loopback Origin %q and none", h.Get("Origin"), h.Get("X-Edge-Token"), app.URL)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the app was never dialled")
	}
	// The counts are given back as serveSocket returns, which is after the
	// head went out: the head can be here before they are.
	waitFor(t, "the refused dial's counts given back", func() bool {
		sockets, inflight := previewSocketCounts(y, "demo")
		return sockets == 0 && inflight == 0
	})
}

// An app that checks Origin the way the library does by default takes
// the dial: the supervisor dials with the loopback Origin of its own
// target, which matches the loopback Host the app sees, not the
// browser's true Origin forwarded on the socket frame.
func TestSocketDialAcceptedByDefaultOriginCheck(t *testing.T) {
	y := newYard(t)
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
		if err != nil {
			return
		}
		defer c.CloseNow()
		// Hold the hop open until the supervisor ends it.
		_, _, _ = c.Read(context.Background())
	}))
	defer app.Close()
	manualSocketPreview(t, y, app.URL)
	l := socketLink(t, y)
	header := http.Header{"Origin": {"https://p-demo.preview.example.net"}}
	head := openPreviewSocket(t, l, "/preview/demo/ws", header, nil)
	if head.Status != http.StatusSwitchingProtocols {
		t.Fatalf("the head is %+v, want 101 from the app with the default origin check", head)
	}
	if err := l.SendSocketClose(context.Background(), head.ID, 1000, "done"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the socket's count given back", func() bool {
		sockets, inflight := previewSocketCounts(y, "demo")
		return sockets == 0 && inflight == 0
	})
}

// manualSocketPreview marks the preview of the httptest app at port
// running without a process of the supervisor's, so a socket's dial
// finds it answering on its port, as a running preview's app does.
func manualSocketPreview(t *testing.T, y *yard, appURL string) {
	t.Helper()
	u, err := url.Parse(appURL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	y.propose(t, "demo", "true", port, true)
	if _, err := y.st.UpdatePreview(context.Background(), "demo", store.PreviewUpdate{State: store.PreviewRunning}); err != nil {
		t.Fatal(err)
	}
}

func nextPreviewSocketClose(t *testing.T, l *standin.EdgeLink, id string) standin.EdgeSocketClose {
	t.Helper()
	for {
		select {
		case cl := <-l.SocketCloseEvents():
			if cl.ID != id {
				t.Fatalf("a close for %s while %s was waited on: %+v", cl.ID, id, cl)
			}
			return cl
		case <-time.After(10 * time.Second):
			t.Fatalf("the socket %s was never closed by the yard", id)
			return standin.EdgeSocketClose{}
		}
	}
}

// A message from the app past 1 MiB closes its socket with 1009 at
// the supervisor, which assembles it, and is never carried to the edge.
func TestSocketMessagePastBoundClosesWith1009(t *testing.T) {
	y := newYard(t)
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true, CompressionMode: websocket.CompressionDisabled})
		if err != nil {
			return
		}
		defer c.CloseNow()
		_ = c.Write(context.Background(), websocket.MessageBinary, make([]byte, (1<<20)+1))
		// Hold the hop open until the supervisor ends it.
		_, _, _ = c.Read(context.Background())
	}))
	defer app.Close()
	manualSocketPreview(t, y, app.URL)
	l := socketLink(t, y)
	head := openPreviewSocket(t, l, "/preview/demo/ws", nil, nil)
	if head.Status != http.StatusSwitchingProtocols {
		t.Fatalf("the head is %+v, want 101", head)
	}
	if cl := nextPreviewSocketClose(t, l, head.ID); cl.Code != 1009 {
		t.Errorf("the socket closed with %+v, want 1009 for the message past 1 MiB", cl)
	}
	if data := l.SocketData(); len(data) != 0 {
		t.Errorf("the edge was carried %d chunks of the message past the bound", len(data))
	}
}

// A close from either side ends the other with its own code: the app
// closing reaches the edge, and the edge closing reaches the app.
func TestSocketCloseCrossesBothWaysWithItsCode(t *testing.T) {
	t.Run("the app's close reaches the edge", func(t *testing.T) {
		y := newYard(t)
		app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true, CompressionMode: websocket.CompressionDisabled})
			if err != nil {
				return
			}
			defer c.CloseNow()
			typ, body, err := c.Read(context.Background())
			if err != nil {
				return
			}
			_ = c.Write(context.Background(), typ, body)
			_ = c.Close(websocket.StatusGoingAway, "the app is going away")
		}))
		defer app.Close()
		manualSocketPreview(t, y, app.URL)
		l := socketLink(t, y)
		head := openPreviewSocket(t, l, "/preview/demo/ws", nil, nil)
		if head.Status != http.StatusSwitchingProtocols {
			t.Fatalf("the head is %+v, want 101", head)
		}
		sendPreviewSocket(t, l, head.ID, edgelink.SocketText, []byte("ping"))
		if _, body := nextPreviewSocketMessage(t, l, head.ID); string(body) != "ping" {
			t.Fatalf("the echo is %q, want ping", body)
		}
		if cl := nextPreviewSocketClose(t, l, head.ID); cl.Code != 1001 || cl.Reason != "the app is going away" {
			t.Errorf("the edge was closed with %+v, want the app's 1001 and its reason", cl)
		}
	})

	t.Run("the edge's close reaches the app", func(t *testing.T) {
		y := newYard(t)
		closed := make(chan websocket.StatusCode, 1)
		app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true, CompressionMode: websocket.CompressionDisabled})
			if err != nil {
				return
			}
			defer c.CloseNow()
			for {
				typ, body, err := c.Read(context.Background())
				if err != nil {
					closed <- websocket.CloseStatus(err)
					return
				}
				if err := c.Write(context.Background(), typ, body); err != nil {
					closed <- websocket.CloseStatus(err)
					return
				}
			}
		}))
		defer app.Close()
		manualSocketPreview(t, y, app.URL)
		l := socketLink(t, y)
		head := openPreviewSocket(t, l, "/preview/demo/ws", nil, nil)
		if head.Status != http.StatusSwitchingProtocols {
			t.Fatalf("the head is %+v, want 101", head)
		}
		sendPreviewSocket(t, l, head.ID, edgelink.SocketText, []byte("ping"))
		if _, body := nextPreviewSocketMessage(t, l, head.ID); string(body) != "ping" {
			t.Fatalf("the echo is %q, want ping", body)
		}
		if err := l.SendSocketClose(context.Background(), head.ID, 1000, "the browser is done"); err != nil {
			t.Fatal(err)
		}
		select {
		case code := <-closed:
			if code != websocket.StatusNormalClosure {
				t.Errorf("the app was closed with %v, want 1000", code)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the app never saw its socket close")
		}
	})
}
