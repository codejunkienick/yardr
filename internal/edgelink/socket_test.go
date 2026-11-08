package edgelink

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"yardr.dev/yardr/internal/standin"
)

// socketCalls is a SocketHandler that echoes what it is sent, holds a
// socket whose path names hold without ever reading it, and closes from
// its own side after one message where the path names close-after. Every
// call, message and end is kept for the test.
type socketCalls struct {
	mu    sync.Mutex
	calls []*SocketRequest
	got   []socketCallsMsg
	ended map[string]error
	endCh chan string
}

type socketCallsMsg struct {
	id, kind string
	body     []byte
}

func newSocketCalls() *socketCalls {
	return &socketCalls{ended: map[string]error{}, endCh: make(chan string, 16)}
}

// ServeHTTP serves no request: the handler takes sockets alone, as the
// supervisor's mux will take both.
func (h *socketCalls) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.NotFound(w, r)
}

func (h *socketCalls) ServeSocket(req *SocketRequest, s *Socket) {
	h.mu.Lock()
	h.calls = append(h.calls, req)
	h.mu.Unlock()
	switch {
	case strings.Contains(req.Path, "/reject-101"):
		_ = s.Reject(http.StatusSwitchingProtocols, nil, nil)
		return
	case strings.Contains(req.Path, "/reject-large"):
		_ = s.Reject(http.StatusForbidden, nil, bytes.Repeat([]byte("x"), maxFrame))
		return
	case strings.Contains(req.Path, "/panic-early"):
		panic("the handler failed before its head")
	}
	protocol := ""
	if len(req.Protocols) > 0 {
		protocol = req.Protocols[0]
	}
	if err := s.Accept(protocol); err != nil {
		h.end(req.ID, err)
		return
	}
	if strings.Contains(req.Path, "/panic-late") {
		panic("the handler failed after its head")
	}
	if strings.Contains(req.Path, "/hold") {
		// Never read: the queue towards this socket fills, and its end
		// is the context's. What was queued is drained on the way out,
		// as a handler returning under its close would meet it.
		<-s.Context().Done()
		for {
			if _, _, err := s.ReadMessage(); err != nil {
				h.end(req.ID, err)
				return
			}
		}
	}
	for {
		kind, body, err := s.ReadMessage()
		if err != nil {
			h.end(req.ID, err)
			return
		}
		h.mu.Lock()
		h.got = append(h.got, socketCallsMsg{id: req.ID, kind: kind, body: append([]byte(nil), body...)})
		h.mu.Unlock()
		if err := s.WriteMessage(kind, body); err != nil {
			h.end(req.ID, err)
			return
		}
		if strings.Contains(req.Path, "/close-after") {
			_ = s.Close(1001, "the app is going away")
			h.end(req.ID, nil)
			return
		}
	}
}

func (h *socketCalls) end(id string, err error) {
	h.mu.Lock()
	h.ended[id] = err
	h.mu.Unlock()
	select {
	case h.endCh <- id:
	default:
	}
}

func (h *socketCalls) callCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.calls)
}

func (h *socketCalls) call(i int) *SocketRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls[i]
}

func (h *socketCalls) messages(id string) []socketCallsMsg {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []socketCallsMsg
	for _, m := range h.got {
		if m.id == id {
			out = append(out, m)
		}
	}
	return out
}

// endedErr waits for the socket with id to end at the handler and says
// how it ended there.
func (h *socketCalls) endedErr(t *testing.T, id string) error {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		h.mu.Lock()
		err, done := h.ended[id]
		h.mu.Unlock()
		if done {
			return err
		}
		select {
		case <-h.endCh:
		case <-deadline:
			t.Fatalf("the socket %s never ended at the handler", id)
			return nil
		}
	}
}

func openSocket(t *testing.T, l *standin.EdgeLink, f standin.EdgeSocket) standin.EdgeSocketHead {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	head, err := l.OpenSocket(ctx, f)
	if err != nil {
		t.Fatalf("socket %s to %s was not answered: %v", f.ID, f.Path, err)
	}
	return head
}

func sendSocketData(t *testing.T, l *standin.EdgeLink, id, kind string, body []byte, more bool) {
	t.Helper()
	if err := l.SendSocketData(context.Background(), id, kind, body, more); err != nil {
		t.Fatal(err)
	}
}

// nextSocketMessage reads the yard's chunks for id until one whole
// message has come, and returns its kind and body. Every chunk is held
// to the link's chunk bound on the way.
func nextSocketMessage(t *testing.T, l *standin.EdgeLink, id string) (string, []byte) {
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

func nextSocketClose(t *testing.T, l *standin.EdgeLink, id string) standin.EdgeSocketClose {
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

// A signed socket frame over a real link is answered with the app's
// subprotocol, a message crosses both ways — the edge's sent in chunks
// and reassembled before the handler sees it, the handler's larger than
// one chunk and chunked before the edge sees it — and the edge's close
// ends the handler's read with its code. A second socket the handler
// closes ends at the edge with its code.
func TestLinkCarriesASocketBothWaysAndClosesWithItsCode(t *testing.T) {
	t.Parallel()
	h := newSocketCalls()
	y := linkTo(t, func(l *Link) { l.Handler = h }, nil)
	l := y.next(t)

	header := http.Header{"Origin": {"https://p-1.preview.example.net"}}
	f := l.Socket("/preview/demo/ws?x=1", header, []string{"chat", "other"})
	head := openSocket(t, l, f)
	if head.ID != f.ID || head.Status != http.StatusSwitchingProtocols || head.Protocol != "chat" {
		t.Fatalf("the head is %+v, want 101 with the first offered protocol", head)
	}
	if h.callCount() != 1 {
		t.Fatalf("%d socket calls, want 1", h.callCount())
	}
	if req := h.call(0); req.Path != "/preview/demo/ws?x=1" || req.Header.Get("Origin") != "https://p-1.preview.example.net" ||
		len(req.Protocols) != 2 || req.Protocols[0] != "chat" || req.Protocols[1] != "other" {
		t.Errorf("the handler was called with %+v", req)
	}

	sendSocketData(t, l, f.ID, SocketText, []byte("hello"), false)
	if kind, body := nextSocketMessage(t, l, f.ID); kind != SocketText || string(body) != "hello" {
		t.Errorf("the echo is %s %q, want text hello", kind, body)
	}

	large := bytes.Repeat([]byte("01234567"), 20<<10) // 160 KiB
	for sent := 0; sent < len(large); {
		n := min(64<<10, len(large)-sent)
		sendSocketData(t, l, f.ID, SocketBinary, large[sent:sent+n], sent+n < len(large))
		sent += n
	}
	if kind, body := nextSocketMessage(t, l, f.ID); kind != SocketBinary || !bytes.Equal(body, large) {
		t.Errorf("the large echo is %s of %d bytes, want the %d sent", kind, len(body), len(large))
	}
	if msgs := h.messages(f.ID); len(msgs) != 2 || !bytes.Equal(msgs[1].body, large) {
		t.Errorf("the handler saw %d messages, the second of %d bytes: the chunks were not reassembled", len(msgs), len(msgs[len(msgs)-1].body))
	}

	if err := l.SendSocketClose(context.Background(), f.ID, 1000, "the browser is done"); err != nil {
		t.Fatal(err)
	}
	var closed *SocketCloseError
	if err := h.endedErr(t, f.ID); !errors.As(err, &closed) || closed.Code != 1000 || closed.Reason != "the browser is done" {
		t.Errorf("the socket ended at the handler with %v, want its close code and reason", err)
	}

	f2 := l.Socket("/preview/demo/close-after", nil, nil)
	if head := openSocket(t, l, f2); head.Status != http.StatusSwitchingProtocols || head.Protocol != "" {
		t.Fatalf("the second head is %+v, want 101 with no protocol", head)
	}
	sendSocketData(t, l, f2.ID, SocketText, []byte("ping"), false)
	if _, body := nextSocketMessage(t, l, f2.ID); string(body) != "ping" {
		t.Errorf("the second echo is %q", body)
	}
	if cl := nextSocketClose(t, l, f2.ID); cl.Code != 1001 || cl.Reason != "the app is going away" {
		t.Errorf("the yard closed with %+v, want the handler's code and reason", cl)
	}

	broken := l.Socket("/preview/demo/ws", nil, nil)
	openSocket(t, l, broken)
	sendSocketData(t, l, broken.ID, SocketText, nil, true)
	sendSocketData(t, l, broken.ID, SocketBinary, []byte("wrong kind"), false)
	if cl := nextSocketClose(t, l, broken.ID); cl.Code != 1008 {
		t.Errorf("the yard closed the mixed-kind message with %+v, want 1008", cl)
	}
	var brokenEnd *SocketCloseError
	if err := h.endedErr(t, broken.ID); !errors.As(err, &brokenEnd) || brokenEnd.Code != 1008 {
		t.Errorf("the malformed message reached the handler: %v", err)
	}
	if msgs := h.messages(broken.ID); len(msgs) != 0 {
		t.Errorf("the handler received %d messages from an empty continuation with another kind", len(msgs))
	}

	for _, tc := range []struct {
		path string
	}{
		{path: "/preview/demo/reject-101"},
		{path: "/preview/demo/reject-large"},
	} {
		refused := l.Socket(tc.path, nil, nil)
		head := openSocket(t, l, refused)
		if head.Status != http.StatusBadGateway || string(head.Body) != "the socket refusal could not be carried" {
			t.Errorf("the refusal for %s is %+v, want a bounded 502", tc.path, head)
		}
	}

	stillOpen := l.Socket("/preview/demo/ws", nil, nil)
	if head := openSocket(t, l, stillOpen); head.Status != http.StatusSwitchingProtocols {
		t.Fatalf("the socket after the refusals answered %+v", head)
	}
	sendSocketData(t, l, stillOpen.ID, SocketText, []byte("still open"), false)
	if _, body := nextSocketMessage(t, l, stillOpen.ID); string(body) != "still open" {
		t.Errorf("the socket after the refusals echoed %q", body)
	}
}

// A socket frame signed by another key is refused and no handler is
// called: no head answers it, and the link serves the next socket.
func TestLinkRefusesASocketNotTheEdges(t *testing.T) {
	t.Parallel()
	h := newSocketCalls()
	y := linkTo(t, func(l *Link) { l.Handler = h }, nil)
	l := y.next(t)

	bad := l.Socket("/preview/demo/ws", nil, nil)
	bad.Sig = l.SignSocket(newKey(t), bad)
	if err := l.SendSocket(context.Background(), bad); err != nil {
		t.Fatal(err)
	}
	good := l.Socket("/preview/demo/ws", nil, nil)
	if head := openSocket(t, l, good); head.Status != http.StatusSwitchingProtocols {
		t.Fatalf("the socket after the refused one answered %+v", head)
	}
	if h.callCount() != 1 || h.call(0).ID != good.ID {
		t.Errorf("%d socket calls, want the good one alone", h.callCount())
	}
	for _, head := range l.SocketHeads() {
		if head.ID == bad.ID {
			t.Errorf("the refused socket was answered: %+v", head)
		}
	}
}

// A link driven down mid-socket ends it at both ends: the handler's read
// answers 1012 instead of hanging, the edge's side of the link is gone,
// and a socket on the link the yard dials next still answers.
func TestLinkEndsItsSocketsWhenItDrops(t *testing.T) {
	t.Parallel()
	h := newSocketCalls()
	y := linkTo(t, func(l *Link) { l.Handler = h }, nil)
	l := y.next(t)

	f := l.Socket("/preview/demo/ws", nil, nil)
	if head := openSocket(t, l, f); head.Status != http.StatusSwitchingProtocols {
		t.Fatalf("the head is %+v", head)
	}
	l.Close()
	select {
	case <-l.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the edge's side of the link never ended")
	}
	var closed *SocketCloseError
	if err := h.endedErr(t, f.ID); !errors.As(err, &closed) || closed.Code != 1012 {
		t.Errorf("the socket ended at the handler with %v, want 1012 for the dropped link", err)
	}

	second := y.next(t)
	f2 := second.Socket("/preview/demo/ws", nil, nil)
	if head := openSocket(t, second, f2); head.Status != http.StatusSwitchingProtocols {
		t.Fatalf("the head on the new link is %+v", head)
	}
	sendSocketData(t, second, f2.ID, SocketText, []byte("back"), false)
	if _, body := nextSocketMessage(t, second, f2.ID); string(body) != "back" {
		t.Errorf("the echo on the new link is %q", body)
	}
}

// A message past 1 MiB closes its socket with 1009 at the yard, which
// assembles it, and is never handed to the handler; a second socket on
// the same link still answers.
func TestLinkClosesASocketWhoseMessagePassesTheBound(t *testing.T) {
	t.Parallel()
	h := newSocketCalls()
	y := linkTo(t, func(l *Link) { l.Handler = h }, nil)
	l := y.next(t)

	f := l.Socket("/preview/demo/ws", nil, nil)
	openSocket(t, l, f)
	chunk := make([]byte, 64<<10)
	for i := 0; i < 17; i++ { // 17 chunks assemble past 1 MiB
		sendSocketData(t, l, f.ID, SocketBinary, chunk, true)
	}
	if cl := nextSocketClose(t, l, f.ID); cl.Code != 1009 {
		t.Errorf("the yard closed with %+v, want 1009 for the message past 1 MiB", cl)
	}
	if msgs := h.messages(f.ID); len(msgs) != 0 {
		t.Errorf("the handler was handed %d messages of the one past the bound", len(msgs))
	}
	var closed *SocketCloseError
	if err := h.endedErr(t, f.ID); !errors.As(err, &closed) || closed.Code != 1009 {
		t.Errorf("the socket ended at the handler with %v, want 1009", err)
	}

	f2 := l.Socket("/preview/demo/ws", nil, nil)
	openSocket(t, l, f2)
	sendSocketData(t, l, f2.ID, SocketText, []byte("still here"), false)
	if _, body := nextSocketMessage(t, l, f2.ID); string(body) != "still here" {
		t.Errorf("the second socket's echo is %q", body)
	}
}

// A socket whose queue towards a handler that never reads stays full
// past its grace closes with 1013, while a second socket on the same
// link still answers.
func TestLinkClosesASocketWhoseQueueOutlastsItsGrace(t *testing.T) {
	t.Parallel()
	h := newSocketCalls()
	y := linkTo(t, func(l *Link) {
		l.Handler = h
		l.SocketGrace = 150 * time.Millisecond
	}, nil)
	l := y.next(t)

	f := l.Socket("/preview/demo/hold", nil, nil)
	openSocket(t, l, f)
	chunk := make([]byte, 64<<10)
	for i := 0; i < 5; i++ { // five messages fill the 256 KiB queue and pass it
		sendSocketData(t, l, f.ID, SocketBinary, chunk, false)
	}
	cl := nextSocketClose(t, l, f.ID)
	if cl.Code != 1013 || cl.Reason != "the other side is not taking its messages" {
		t.Errorf("the yard closed with %+v, want 1013 for the slow consumer", cl)
	}
	var closed *SocketCloseError
	if err := h.endedErr(t, f.ID); !errors.As(err, &closed) || closed.Code != 1013 {
		t.Errorf("the held socket ended at the handler with %v, want 1013", err)
	}

	f2 := l.Socket("/preview/demo/ws", nil, nil)
	openSocket(t, l, f2)
	sendSocketData(t, l, f2.ID, SocketText, []byte("still here"), false)
	if _, body := nextSocketMessage(t, l, f2.ID); string(body) != "still here" {
		t.Errorf("the second socket's echo is %q", body)
	}

	overflow := l.Socket("/preview/demo/hold?partial", nil, nil)
	openSocket(t, l, overflow)
	for i := 0; i < 3; i++ {
		sendSocketData(t, l, overflow.ID, SocketBinary, chunk, false)
	}
	tooLarge := make([]byte, 100<<10)
	for sent := 0; sent < len(tooLarge); {
		n := min(64<<10, len(tooLarge)-sent)
		sendSocketData(t, l, overflow.ID, SocketBinary, tooLarge[sent:sent+n], sent+n < len(tooLarge))
		sent += n
	}
	if cl := nextSocketClose(t, l, overflow.ID); cl.Code != 1013 {
		t.Errorf("the partially full queue closed with %+v, want 1013", cl)
	}
	var overflowEnd *SocketCloseError
	if err := h.endedErr(t, overflow.ID); !errors.As(err, &overflowEnd) || overflowEnd.Code != 1013 {
		t.Errorf("the partially full queue ended at the handler with %v, want 1013", err)
	}

	empty := l.Socket("/preview/demo/hold?empty", nil, nil)
	openSocket(t, l, empty)
	for i := 0; i < 4000; i++ {
		sendSocketData(t, l, empty.ID, SocketText, nil, false)
	}
	if cl := nextSocketClose(t, l, empty.ID); cl.Code != 1013 {
		t.Errorf("the empty-message queue closed with %+v, want 1013", cl)
	}
	var emptyEnd *SocketCloseError
	if err := h.endedErr(t, empty.ID); !errors.As(err, &emptyEnd) || emptyEnd.Code != 1013 {
		t.Errorf("the empty-message queue ended at the handler with %v, want 1013", err)
	}

	// Socket termination cancels the handler context but keeps the link
	// context used by the shared WebSocket writer alive. The queued frame
	// in flight finishes, then the pump discards the rest.
	socketCtx, socketCancel := context.WithCancel(context.Background())
	pumped := &Socket{req: &SocketRequest{ID: "pump-test"}, l: y.link, ctx: socketCtx, cancel: socketCancel,
		lctx: context.Background(), outQ: []socketDataFrame{{cost: 1}, {cost: 1}}, outBytes: 2}
	pumped.cond.L = &pumped.mu
	started, release, pumpedDone := make(chan context.Context, 1), make(chan struct{}), make(chan struct{})
	writes := 0
	go func() {
		pumped.pumpWith(func(ctx context.Context, _ socketDataFrame) error {
			writes++
			if writes == 1 {
				started <- ctx
				<-release
			}
			return nil
		})
		close(pumpedDone)
	}()
	linkCtx := <-started
	pumped.terminate(1000, "the edge closed", false)
	if linkCtx.Err() != nil {
		t.Errorf("closing one socket cancelled its link context: %v", linkCtx.Err())
	}
	close(release)
	select {
	case <-pumpedDone:
	case <-time.After(10 * time.Second):
		t.Fatal("the socket pump did not stop after its socket ended")
	}
	if writes != 1 || pumped.outBytes != 0 {
		t.Errorf("the ended socket pump wrote %d frames and retained %d bytes, want one in-flight frame and no queue", writes, pumped.outBytes)
	}
}

// A yard whose handler takes no sockets refuses the upgrade in its head
// and calls nothing.
func TestLinkRefusesASocketItsHandlerDoesNotTake(t *testing.T) {
	t.Parallel()
	y := linkTo(t, nil, nil)
	l := y.next(t)
	f := l.Socket("/preview/demo/ws", nil, nil)
	head := openSocket(t, l, f)
	if head.Status != http.StatusNotImplemented || !strings.Contains(string(head.Body), "does not carry sockets") {
		t.Errorf("the head is %+v, want a 501 that says the yard carries no sockets", head)
	}
}

// A socket handler that panics fails its own socket, before its head with
// a 502 and after it with 1011, and the link, and the server it runs in,
// go on serving the next socket.
func TestLinkSurvivesASocketHandlerThatPanics(t *testing.T) {
	t.Parallel()
	h := newSocketCalls()
	y := linkTo(t, func(l *Link) { l.Handler = h }, nil)
	l := y.next(t)

	early := l.Socket("/preview/demo/panic-early", nil, nil)
	if head := openSocket(t, l, early); head.Status != http.StatusBadGateway {
		t.Errorf("the head of a handler that panicked is %+v, want 502", head)
	}
	late := l.Socket("/preview/demo/panic-late", nil, nil)
	if head := openSocket(t, l, late); head.Status != http.StatusSwitchingProtocols {
		t.Fatalf("the head is %+v, want 101 before the panic", head)
	}
	if cl := nextSocketClose(t, l, late.ID); cl.Code != 1011 {
		t.Errorf("the yard closed the panicked socket with %+v, want 1011", cl)
	}
	next := l.Socket("/preview/demo/ws", nil, nil)
	if head := openSocket(t, l, next); head.Status != http.StatusSwitchingProtocols {
		t.Fatalf("the socket after the panics answered %+v", head)
	}
	sendSocketData(t, l, next.ID, SocketText, []byte("still here"), false)
	if _, body := nextSocketMessage(t, l, next.ID); string(body) != "still here" {
		t.Errorf("the socket after the panics echoed %q", body)
	}
}
