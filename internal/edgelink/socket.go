package edgelink

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// A socket is one WebSocket carried on the link as a stream kind beside a
// request (design yardr-hosted-1xkc section 1): the edge opens it with a
// socket frame, the yard answers one socket-head, the two ends exchange
// whole messages as socket-data chunks, and socket-close ends it. The
// frames serve the supervisor's served-app endpoint, not previews as a
// kind (section 7), so the link dispatches by what its Handler can do and
// never by the link's role.
const (
	// MaxSocketMessage is the most bytes one socket message carries,
	// assembled from its chunks in either direction. A message past it
	// closes its socket with 1009 at the hop that assembles it.
	MaxSocketMessage = 1 << 20
	// maxSocketChunk is the most bytes one socket-data frame carries
	// before its base64, so a chunk with its frame's own bytes stays
	// under the link's frame bound as a data frame's does.
	maxSocketChunk = 64 << 10
	// maxSocketQueue is the most bytes one socket queues in one
	// direction while the other side is not taking them. The accounting
	// includes per-message or per-frame bookkeeping so empty messages are
	// bounded too.
	maxSocketQueue = 256 << 10
	// socketQueueOverhead accounts for the message or frame kept beside
	// its body, so a queue of empty messages still has a finite bound.
	socketQueueOverhead = 64
	// socketGrace is how long a socket's queue may stay full before the
	// socket is closed with 1013. Link.SocketGrace shortens it in tests.
	socketGrace = 30 * time.Second
)

// The kinds a socket message carries, as socket-data names them.
const (
	SocketText   = "text"
	SocketBinary = "binary"
)

// ErrSocketClosed is a read or write on a socket that has ended, where the
// end carried no close code of the other side's to report instead.
var ErrSocketClosed = errors.New("the socket is closed")

// SocketCloseError is how a socket's end reaches the side that did not
// close it: the code and reason the closing side closed with, the 1009 or
// 1013 of a bound the yard closed it under, or 1012 when the link went
// down under it.
type SocketCloseError struct {
	Code   int
	Reason string
}

func (e *SocketCloseError) Error() string {
	return fmt.Sprintf("the socket closed (%d %s)", e.Code, orText(text(e.Reason), "no reason"))
}

// SocketRequest is one upgrade as the edge took it: the yard-side path a
// preview request already takes, query included, the sanitised headers,
// and the subprotocols the browser offered, in its order.
type SocketRequest struct {
	ID        string
	Path      string
	Header    http.Header
	Protocols []string
}

// SocketHandler is what the link's Handler may also implement to be
// offered each socket the edge opens. ServeSocket runs in a goroutine of
// its own per socket; it answers the head with Accept or Reject before it
// relays, and returns when the socket ends. A Handler that does not
// implement it is answered a 501 head and no socket is opened.
type SocketHandler interface {
	ServeSocket(*SocketRequest, *Socket)
}

// socketFrame is every socket frame of the link as the yard reads one: a
// request frame's fields, which a socket frame is numbered and signed
// with, and the fields the four kinds add. Body is base64, as
// encoding/json writes bytes.
type socketFrame struct {
	frameIn
	Protocols []string `json:"protocols"`
	Kind      string   `json:"kind"`
	More      bool     `json:"more"`
	Code      int      `json:"code"`
	Reason    string   `json:"reason"`
}

type socketHeadFrame struct {
	Type     string      `json:"type"`
	ID       string      `json:"id"`
	Status   int         `json:"status"`
	Header   http.Header `json:"header"`
	Protocol string      `json:"protocol,omitempty"`
	Body     []byte      `json:"body"`
}

type socketDataFrame struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Body []byte `json:"body"`
	More bool   `json:"more,omitempty"`
	cost int    `json:"-"`
}

type socketCloseFrame struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	Code   int    `json:"code"`
	Reason string `json:"reason"`
}

// socketSet is the sockets open on one link, by the id the edge named
// them. The link's drop ends every one of them.
type socketSet struct {
	mu   sync.Mutex
	byID map[string]*Socket
}

func (l *Link) sockets() *socketSet {
	l.liveMu.Lock()
	defer l.liveMu.Unlock()
	if l.socks == nil {
		l.socks = &socketSet{byID: map[string]*Socket{}}
	}
	return l.socks
}

func (l *Link) addSocket(s *Socket) bool {
	set := l.sockets()
	set.mu.Lock()
	defer set.mu.Unlock()
	if _, held := set.byID[s.req.ID]; held {
		return false
	}
	set.byID[s.req.ID] = s
	return true
}

func (l *Link) forgetSocket(id string, s *Socket) {
	set := l.sockets()
	set.mu.Lock()
	defer set.mu.Unlock()
	if set.byID[id] == s {
		delete(set.byID, id)
	}
}

func (l *Link) socketByID(id string) *Socket {
	set := l.sockets()
	set.mu.Lock()
	defer set.mu.Unlock()
	return set.byID[id]
}

// endSockets ends every socket the link carries, as its drop does: each
// handler sees a close and not a hang, and nothing is queued for the
// link's return. No close frame is sent; the link that would carry it is
// the one that ended.
func (l *Link) endSockets() {
	set := l.sockets()
	set.mu.Lock()
	open := make([]*Socket, 0, len(set.byID))
	for _, s := range set.byID {
		open = append(open, s)
	}
	set.mu.Unlock()
	for _, s := range open {
		s.terminate(websocket.StatusServiceRestart, "the link is down", false)
	}
}

// openSocket serves one socket frame the check has already taken: it is
// offered to the Handler where the Handler takes sockets, and refused in
// a head where it does not, where its path is no request's, or where its
// id is already open on this link.
func (l *Link) openSocket(lctx context.Context, c *websocket.Conn, f socketFrame) {
	head := func(status int, body string) {
		herr := l.write(lctx, c, socketHeadFrame{Type: "socket-head", ID: f.ID, Status: status,
			Header: http.Header{}, Body: []byte(body)})
		if herr != nil && lctx.Err() == nil {
			l.Log.Warn("edge link: a socket's refusal not sent", "id", text(f.ID), "err", herr)
		}
	}
	if _, err := url.ParseRequestURI(f.Path); err != nil || !strings.HasPrefix(f.Path, "/") {
		head(http.StatusBadRequest, "the socket's path is no request's")
		return
	}
	handler, ok := l.Handler.(SocketHandler)
	if !ok {
		head(http.StatusNotImplemented, "this yard does not carry sockets")
		return
	}
	req := &SocketRequest{ID: f.ID, Path: f.Path, Protocols: append([]string(nil), f.Protocols...)}
	req.Header = http.Header{}
	for name, values := range f.Header {
		for _, v := range values {
			req.Header.Add(name, v)
		}
	}
	s := newSocket(l, lctx, c, req)
	if !l.addSocket(s) {
		head(http.StatusConflict, "a socket of that id is already open")
		return
	}
	go s.pump()
	defer l.forgetSocket(f.ID, s)
	defer s.finish()
	// A handler that panics fails its own socket, as serveOne fails a
	// request's: what a browser sends through the edge must not end the
	// yard's server. finish then refuses a head never answered.
	defer func() {
		if p := recover(); p != nil {
			l.Log.Error("edge link: a socket's handler panicked", "id", text(f.ID), "panic", fmt.Sprint(p))
			s.mu.Lock()
			head := s.head
			s.mu.Unlock()
			if head {
				s.terminate(websocket.StatusInternalError, "the socket's handler failed", true)
			}
		}
	}()
	handler.ServeSocket(req, s)
}

// socketData hands one socket-data frame to the socket it names. A frame
// for an id no socket holds is dropped: the socket ended, and its close
// already said so.
func (l *Link) socketData(f socketFrame) {
	s := l.socketByID(f.ID)
	if s == nil {
		l.Log.Warn("edge link: socket data for no open socket, dropped", "id", text(f.ID))
		return
	}
	s.feed(f.Kind, f.Body, f.More)
}

// socketClosed hands the edge's socket-close for an id to its socket: it
// is final, and no socket-data follows it for that id.
func (l *Link) socketClosed(f socketFrame) {
	s := l.socketByID(f.ID)
	if s == nil {
		return
	}
	code := f.Code
	if code == 0 {
		code = int(websocket.StatusNoStatusRcvd)
	}
	s.terminate(websocket.StatusCode(code), f.Reason, false)
}

// A Socket is one socket the edge opened on the link, as its yard end
// hands it to the socket handler: whole messages in both directions, the
// link's chunking reassembled before they are handed over, and one close.
// Its methods are safe together: a handler relays with one goroutine
// reading and one writing, as it would a WebSocket of its own.
type Socket struct {
	req *SocketRequest
	l   *Link
	c   *websocket.Conn
	// ctx ends with the link, with the socket's own close, and with the
	// other side's: what a handler waits on beside its reads.
	ctx    context.Context
	cancel context.CancelFunc
	// lctx is the link's own context, for the frames the socket sends as
	// it ends, which its own cancelled context would not carry.
	lctx context.Context

	mu   sync.Mutex
	cond sync.Cond
	// Socket frames share the link's WebSocket. This orders a final close
	// after an in-flight data frame without canceling the shared connection.
	writeMu sync.Mutex
	// The messages reassembled and not yet read, and their bytes.
	inQ     []socketMessage
	inBytes int
	// inDropped marks that an incoming message could not fit. Since the
	// link reader cannot wait for this socket, its grace still ends the
	// socket even if later reads free some queue space.
	inDropped bool
	// The message the chunks arriving are assembling.
	asmOpen bool
	asmKind string
	asm     []byte
	asmCost int
	// The chunk frames written and not yet taken by the link, and
	// their bytes.
	outQ     []socketDataFrame
	outBytes int
	// head is whether the socket-head has gone; closed is whether the
	// socket has ended either way, and end what its reads then answer.
	head   bool
	closed bool
	end    error
	// The grace timers of a full queue in each direction, and when the
	// outbound queue filled, which a blocked write waits against.
	inTimer, outTimer *time.Timer
	outFullAt         time.Time
}

type socketMessage struct {
	kind string
	body []byte
	cost int
}

func newSocket(l *Link, lctx context.Context, c *websocket.Conn, req *SocketRequest) *Socket {
	ctx, cancel := context.WithCancel(lctx)
	s := &Socket{req: req, l: l, c: c, ctx: ctx, cancel: cancel, lctx: lctx}
	s.cond.L = &s.mu
	return s
}

// Request is the upgrade the socket was opened with.
func (s *Socket) Request() *SocketRequest { return s.req }

// Context ends when the socket does, or when the link under it does.
func (s *Socket) Context() context.Context { return s.ctx }

func (s *Socket) grace() time.Duration { return or(s.l.SocketGrace, socketGrace) }

// Accept answers the socket's head with 101 and the subprotocol the
// handler chose, empty for none: the upgrade is taken. It is called once,
// before or beside the relaying; a Write or Read needs no Accept first,
// and the chunks of a message sent before it wait in their queue.
func (s *Socket) Accept(protocol string) error {
	s.mu.Lock()
	if s.head || s.closed {
		s.mu.Unlock()
		return ErrSocketClosed
	}
	s.head = true
	s.mu.Unlock()
	err := s.l.write(s.lctx, s.c, socketHeadFrame{Type: "socket-head", ID: s.req.ID,
		Status: http.StatusSwitchingProtocols, Header: http.Header{}, Protocol: protocol})
	if err != nil {
		s.terminate(websocket.StatusServiceRestart, "the link is down", false)
		return err
	}
	return nil
}

// Reject answers the socket's head with a refusal of its own: the status,
// headers and short body the edge puts on its refusal answer. The socket
// is over; the handler returns after it.
func (s *Socket) Reject(status int, header http.Header, body []byte) error {
	s.mu.Lock()
	if s.head || s.closed {
		s.mu.Unlock()
		return ErrSocketClosed
	}
	s.head = true
	s.mu.Unlock()
	if header == nil {
		header = http.Header{}
	}
	head := socketHeadFrame{Type: "socket-head", ID: s.req.ID, Status: status, Header: header, Body: body}
	fallback := socketHeadFrame{Type: "socket-head", ID: s.req.ID, Status: http.StatusBadGateway,
		Header: http.Header{}, Body: []byte("the socket refusal could not be carried")}
	var problem error
	if status < http.StatusOK || status > 599 {
		problem = fmt.Errorf("a socket refusal must have a final status from 200 through 599, got %d", status)
		head = fallback
	} else if encoded, err := json.Marshal(head); err != nil {
		problem = fmt.Errorf("a socket refusal could not be encoded: %w", err)
		head = fallback
	} else if len(encoded) > maxFrame {
		problem = fmt.Errorf("a socket refusal exceeds the link frame limit")
		head = fallback
	}
	err := s.l.write(s.lctx, s.c, head)
	s.terminateSilent()
	if err != nil {
		return err
	}
	return problem
}

// ReadMessage takes the next whole message the edge sent, reassembled
// from its chunks: its kind, text or binary, and its body. It waits while
// none has come. The socket's end is its error: a *SocketCloseError with
// the other side's code, the bound's, or the link's, after every message
// queued before it has been read.
func (s *Socket) ReadMessage() (string, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.inQ) == 0 && !s.closed {
		s.cond.Wait()
	}
	if len(s.inQ) > 0 {
		m := s.inQ[0]
		s.inQ[0] = socketMessage{}
		s.inQ = s.inQ[1:]
		s.inBytes -= m.cost
		if s.inBytes < maxSocketQueue && !s.inDropped {
			s.disarm(&s.inTimer)
		}
		return m.kind, m.body, nil
	}
	return "", nil, s.end
}

// WriteMessage sends one whole message to the edge, in chunks of at most
// 64 KiB the edge reassembles. It waits while the socket's queue towards
// the edge is full, which is the backpressure the app behind the handler
// feels; a queue still full when the grace is out closes the socket with
// 1013 and the write answers ErrSocketClosed. A message past
// MaxSocketMessage closes the socket with 1009 instead of crossing.
func (s *Socket) WriteMessage(kind string, body []byte) error {
	if kind != SocketText && kind != SocketBinary {
		return fmt.Errorf("a socket message's kind is %q or %q, not %q", SocketText, SocketBinary, text(kind))
	}
	if len(body) > MaxSocketMessage {
		s.terminate(websocket.StatusMessageTooBig, "the message is larger than a socket carries", true)
		return fmt.Errorf("a socket message is at most %d bytes", MaxSocketMessage)
	}
	frames := chunkMessage(s.req.ID, kind, body)
	for _, f := range frames {
		if err := s.enqueue(f); err != nil {
			return err
		}
	}
	return nil
}

// chunkMessage is body as the socket-data frames that carry it: chunks of
// at most maxSocketChunk, more on each but the last, and one empty frame
// for an empty message.
func chunkMessage(id, kind string, body []byte) []socketDataFrame {
	if len(body) == 0 {
		return []socketDataFrame{{Type: "socket-data", ID: id, Kind: kind,
			cost: socketQueueCost(id, kind, nil)}}
	}
	var frames []socketDataFrame
	for len(body) > 0 {
		n := min(len(body), maxSocketChunk)
		chunk := body[:n]
		frames = append(frames, socketDataFrame{Type: "socket-data", ID: id, Kind: kind,
			Body: chunk, More: len(body) > n, cost: socketQueueCost(id, kind, chunk)})
		body = body[n:]
	}
	return frames
}

func socketQueueCost(id, kind string, body []byte) int {
	return len(body) + len(id) + len(kind) + socketQueueOverhead
}

func (s *Socket) enqueue(f socketDataFrame) error {
	s.mu.Lock()
	for !s.closed && s.outBytes+f.cost > maxSocketQueue && s.outBytes > 0 {
		if s.outFullAt.IsZero() {
			s.outFullAt = time.Now()
			s.armOut()
		}
		s.cond.Wait()
		if !s.closed && !s.outFullAt.IsZero() && time.Since(s.outFullAt) >= s.grace() {
			s.mu.Unlock()
			s.terminate(websocket.StatusTryAgainLater, "the other side is not taking its messages", true)
			return ErrSocketClosed
		}
	}
	if s.closed {
		s.mu.Unlock()
		return ErrSocketClosed
	}
	s.outQ = append(s.outQ, f)
	s.outBytes += f.cost
	if s.outBytes >= maxSocketQueue && s.outTimer == nil {
		if s.outFullAt.IsZero() {
			s.outFullAt = time.Now()
		}
		s.armOut()
	}
	s.cond.Broadcast()
	s.mu.Unlock()
	return nil
}

// Close ends the socket with a WebSocket close code and reason, sent to
// the edge as socket-close: final, with no data after it. What the
// handler has written and not yet seen taken by the link goes first, so
// a message written beside its close is not lost under it. The handler
// returns after it; a second Close is nothing.
func (s *Socket) Close(code int, reason string) error {
	s.mu.Lock()
	for len(s.outQ) > 0 && !s.closed {
		s.cond.Wait()
	}
	s.mu.Unlock()
	s.terminate(websocket.StatusCode(code), reason, true)
	return nil
}

// finish is the socket's end where its handler returned without one: a
// head never answered is refused, and an open socket is closed normally.
func (s *Socket) finish() {
	s.mu.Lock()
	head, closed := s.head, s.closed
	s.mu.Unlock()
	switch {
	case closed:
	case !head:
		_ = s.Reject(http.StatusBadGateway, nil, []byte("the socket's handler answered nothing"))
	default:
		_ = s.Close(int(websocket.StatusNormalClosure), "")
	}
}

// terminateSilent ends the socket with nothing more sent on it: its reads
// answer ErrSocketClosed.
func (s *Socket) terminateSilent() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed, s.end = true, ErrSocketClosed
	s.stopTimers()
	s.mu.Unlock()
	s.cancel()
	s.mu.Lock()
	s.cond.Broadcast()
	s.mu.Unlock()
	s.l.forgetSocket(s.req.ID, s)
}

// terminate ends the socket once: its reads answer a *SocketCloseError of
// the code and reason, its context ends, and where send holds a
// socket-close of that code goes to the edge first. A socket already
// ended keeps the end it has.
func (s *Socket) terminate(code websocket.StatusCode, reason string, send bool) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.end = &SocketCloseError{Code: int(code), Reason: reason}
	s.stopTimers()
	s.mu.Unlock()
	s.cancel()
	s.mu.Lock()
	s.cond.Broadcast()
	s.mu.Unlock()
	if send {
		s.writeMu.Lock()
		_ = s.l.write(s.lctx, s.c, socketCloseFrame{Type: "socket-close", ID: s.req.ID, Code: int(code), Reason: reason})
		s.writeMu.Unlock()
	}
	s.l.forgetSocket(s.req.ID, s)
}

func (s *Socket) stopTimers() {
	s.disarm(&s.inTimer)
	s.disarm(&s.outTimer)
}

// disarm stops a grace timer; the caller holds mu.
func (s *Socket) disarm(t **time.Timer) {
	if *t != nil {
		(*t).Stop()
		*t = nil
	}
}

// armOut arms the outbound grace: a queue still full when it fires closes
// the socket with 1013, and a write blocked on the queue wakes to see it.
// The caller holds mu and has set outFullAt.
func (s *Socket) armOut() {
	if s.outTimer != nil {
		return
	}
	s.outTimer = time.AfterFunc(s.grace(), func() {
		s.mu.Lock()
		full := !s.closed && s.outBytes >= maxSocketQueue
		s.cond.Broadcast()
		s.mu.Unlock()
		if full {
			s.terminate(websocket.StatusTryAgainLater, "the other side is not taking its messages", true)
		}
	})
}

// feed takes one socket-data chunk from the edge: it is assembled with
// the chunks of its message before it, and the whole message queued for
// ReadMessage. A chunk past the chunk bound, a message past
// MaxSocketMessage, or a chunk of another kind mid-message closes the
// socket here, at the hop that assembles it.
func (s *Socket) feed(kind string, body []byte, more bool) {
	if kind != SocketText && kind != SocketBinary {
		s.terminate(websocket.StatusPolicyViolation, "a socket chunk's kind is text or binary", true)
		return
	}
	if len(body) > maxSocketChunk {
		s.terminate(websocket.StatusMessageTooBig, "a socket chunk is larger than the link carries", true)
		return
	}
	s.mu.Lock()
	if s.closed || s.inDropped {
		s.mu.Unlock()
		return
	}
	if !s.asmOpen {
		s.asmKind = kind
		s.asmOpen = true
	} else if kind != s.asmKind {
		s.mu.Unlock()
		s.terminate(websocket.StatusPolicyViolation, "a socket message changed kind between its chunks", true)
		return
	}
	s.asm = append(s.asm, body...)
	s.asmCost += socketQueueCost(s.req.ID, kind, body)
	if len(s.asm) > MaxSocketMessage {
		s.asm, s.asmOpen, s.asmKind, s.asmCost = nil, false, "", 0
		s.mu.Unlock()
		s.terminate(websocket.StatusMessageTooBig, "the message is larger than a socket carries", true)
		return
	}
	if more {
		s.mu.Unlock()
		return
	}
	msg := socketMessage{kind: s.asmKind, body: s.asm, cost: s.asmCost}
	s.asm, s.asmOpen, s.asmKind, s.asmCost = nil, false, "", 0
	// The queue holds whole messages; one message larger than the queue
	// still crosses an empty queue, since nothing ahead of it is held.
	if s.inBytes > 0 && s.inBytes+msg.cost > maxSocketQueue {
		// The link's read loop is never stalled by one socket: the
		// message is dropped, and the socket closes after its grace: the
		// handler must not continue after a message was lost.
		s.inDropped = true
		s.armIn()
		s.mu.Unlock()
		return
	}
	s.inQ = append(s.inQ, msg)
	s.inBytes += msg.cost
	if s.inBytes >= maxSocketQueue {
		s.armIn()
	}
	s.cond.Broadcast()
	s.mu.Unlock()
}

// armIn arms the inbound grace. Its caller holds mu.
func (s *Socket) armIn() {
	if s.inTimer != nil {
		return
	}
	s.inTimer = time.AfterFunc(s.grace(), func() {
		s.mu.Lock()
		full := !s.closed && (s.inBytes >= maxSocketQueue || s.inDropped)
		s.mu.Unlock()
		if full {
			s.terminate(websocket.StatusTryAgainLater, "the other side is not taking its messages", true)
		}
	})
}

// pump writes the socket's queued chunk frames to the link in order. Its
// writes use the link context because cancelling one socket cannot close
// the WebSocket shared by the other streams.
func (s *Socket) pump() {
	s.pumpWith(func(ctx context.Context, f socketDataFrame) error {
		return s.l.write(ctx, s.c, f)
	})
}

func (s *Socket) pumpWith(write func(context.Context, socketDataFrame) error) {
	for {
		s.mu.Lock()
		for len(s.outQ) == 0 && !s.closed {
			s.cond.Wait()
		}
		if s.closed {
			s.discardOutLocked()
			s.cond.Broadcast()
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()

		s.writeMu.Lock()
		s.mu.Lock()
		if s.closed {
			s.discardOutLocked()
			s.cond.Broadcast()
			s.mu.Unlock()
			s.writeMu.Unlock()
			return
		}
		if len(s.outQ) == 0 {
			s.mu.Unlock()
			s.writeMu.Unlock()
			continue
		}
		f := s.outQ[0]
		s.mu.Unlock()
		err := write(s.lctx, f)
		s.mu.Lock()
		// The frame written is the queue's head; nothing else takes from
		// this queue.
		s.outQ[0] = socketDataFrame{}
		s.outQ = s.outQ[1:]
		s.outBytes -= f.cost
		if s.closed {
			s.discardOutLocked()
		} else if s.outBytes < maxSocketQueue {
			s.disarm(&s.outTimer)
			s.outFullAt = time.Time{}
		}
		closed := s.closed
		s.cond.Broadcast()
		s.mu.Unlock()
		s.writeMu.Unlock()
		if err != nil {
			if !closed {
				s.terminate(websocket.StatusServiceRestart, "the link is down", false)
			}
			return
		}
		if closed {
			return
		}
	}
}

// discardOutLocked clears frames after a socket ended. The caller holds mu.
func (s *Socket) discardOutLocked() {
	for i := range s.outQ {
		s.outQ[i] = socketDataFrame{}
	}
	s.outQ = nil
	s.outBytes = 0
	s.outFullAt = time.Time{}
	s.disarm(&s.outTimer)
}
