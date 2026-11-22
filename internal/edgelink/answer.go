package edgelink

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"

	"yardr.dev/yardr/internal/hooks"
)

// ackable says whether an answer to a hook delivery ends it: it recorded
// the delivery (2xx, a duplicate or a drop included), or refused it for
// good (4xx: a bad signature, a malformed body, a hook there is not —
// sending the same bytes again fails the same way). A 5xx, the yard's
// own faults (its store, a blocked secret file), records nothing and may
// succeed when sent again, and no status at all never proves a record:
// neither is an ack, on the queued path or as a live answer the edge
// counts as one (see answer below).
func ackable(status int) bool {
	return status >= http.StatusOK && status < http.StatusInternalServerError
}

// held says whether a live hook delivery's answer is held — sent as
// neither a response nor an end — because it is no ack (ackable): the
// edge counts any late non-stream answer as its queued entry's ack, so a
// retryable answer would delete the entry unrecorded. Held, the entry
// stays queued and is sent again on the next connect. An answer already
// streaming (its head went) is not held: the edge takes no stream as an
// answer for a hook, and keeps the entry as it does a held one. Only
// hooks are held; every other live request is answered as it was, since
// only a webhook's delivery is kept.
func held(f frameIn, w *streamWriter) bool {
	return !w.head && strings.HasPrefix(f.Path, "/hooks/") && !ackable(w.status)
}

// logPath strips any query string from a frame's path before logging it,
// so a token or secret carried in a query never lands in the yard's log.
func logPath(raw string) string {
	path, _, _ := strings.Cut(raw, "?")
	return text(path)
}

// answer serves one request with the listener's handler and sends its
// answer back: a queued request as an ack once it is recorded or refused,
// a live one as a stream (finish). An answer that is no ack (see ackable
// above) is sent on neither path, so an unrecorded delivery stays
// queued at the edge and is sent again on the next connect.
func (l *Link) answer(lctx context.Context, c *websocket.Conn, f frameIn, reqCtx context.Context) {
	defer l.untrack(f.ID)
	if f.Queued {
		w := &answerWriter{header: http.Header{}}
		l.serveOne(w, f, reqCtx, func() { w.status = http.StatusInternalServerError })
		if !ackable(w.status) {
			l.Log.Warn("edge link: a queued delivery not acked, kept for the edge to send again", "id", text(f.ID), "path", logPath(f.Path), "status", w.status)
			return
		}
		if err := l.write(lctx, c, ackFrame{Type: "ack", ID: f.ID}); err != nil && lctx.Err() == nil {
			l.Log.Warn("edge link: an ack not sent", "id", text(f.ID), "err", err)
		}
		return
	}
	w := &streamWriter{l: l, c: c, ctx: lctx, id: f.ID, header: http.Header{}}
	l.serveOne(w, f, reqCtx, w.fail)
	if held(f, w) {
		l.Log.Warn("edge link: a live delivery not answered, kept for the edge to send again", "id", text(f.ID), "path", logPath(f.Path), "status", w.status)
		return
	}
	w.finish()
}

// serveOne builds f's request and serves it into w: a frame that is no
// request's is a 400, and a handler that panics is logged and failed.
func (l *Link) serveOne(w http.ResponseWriter, f frameIn, reqCtx context.Context, failed func()) {
	defer func() {
		if p := recover(); p != nil && p != http.ErrAbortHandler {
			l.Log.Error("edge link: a request's handler panicked", "path", f.Path, "panic", fmt.Sprint(p))
			failed()
		}
	}()
	req, err := l.request(reqCtx, f)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	l.Handler.ServeHTTP(w, req)
}

// request is the frame as the request a handler serves. Its headers are
// put in Go's form, so a handler finds a name however the sender wrote it;
// what was signed was the frame's own. A queued frame's request is marked
// with the edge-signed time the edge received it, which a declared
// source's window is checked against instead of the yard's clock; a queued
// frame whose received is no time is served unmarked, as a live one is.
func (l *Link) request(ctx context.Context, f frameIn) (*http.Request, error) {
	u, err := url.ParseRequestURI(f.Path)
	if err != nil || !strings.HasPrefix(f.Path, "/") {
		return nil, fmt.Errorf("path %q is no request's", text(f.Path))
	}
	if f.Queued {
		if received, err := time.Parse(time.RFC3339, f.Received); err == nil {
			ctx = hooks.WithEdgeReceived(ctx, received)
		} else {
			l.Log.Warn("edge link: a queued frame whose received is no time, served as live", "id", text(f.ID), "received", text(f.Received))
		}
	}
	req, err := http.NewRequestWithContext(ctx, f.Method, u.String(), bytes.NewReader(f.Body))
	if err != nil {
		return nil, err
	}
	req.URL, req.RequestURI = u, f.Path
	if host, err := url.Parse(l.Edge.URL); err == nil {
		req.Host = host.Host
	}
	req.RemoteAddr = RemoteAddr
	req.Header = http.Header{}
	for name, values := range f.Header {
		for _, v := range values {
			req.Header.Add(name, v)
		}
	}
	// An enveloped call is handed the call id its frame registered
	// (e2e.go), which is the frame header's claim as serve took it.
	if call := l.registeredE2E(f.ID); call != "" {
		req.Header.Set(e2eCallHeader, call)
	}
	return req, nil
}

// answerWriter keeps a queued request's status and headers while its
// handler runs; its body is discarded because the ack carries no answer and
// the edge answered its sender long ago.
type answerWriter struct {
	header http.Header
	status int
}

func (w *answerWriter) Header() http.Header { return w.header }

func (w *answerWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *answerWriter) Write(b []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	return len(b), nil
}

// streamWriter is a live request's answer as the edge takes a stream: the
// head goes as a response marked stream, each flushed piece as data, and end
// when the handler returns (finish). A handler that never flushes is answered
// whole. Writes go under the link's context, so they outlive the request's
// own cancel.
type streamWriter struct {
	l   *Link
	c   *websocket.Conn
	ctx context.Context
	id  string

	header  http.Header
	status  int
	since   []byte // written since the last flush
	head    bool   // the response went
	quiet   bool   // a send failed: the link is going, and later sends are skipped
	refused bool   // the head alone was too large; its 413 carries no body
	failed  bool   // the handler panicked: the tail is dropped
}

func (w *streamWriter) Header() http.Header { return w.header }

func (w *streamWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *streamWriter) Write(b []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	if !w.quiet && !w.refused {
		w.since = append(w.since, b...)
	}
	return len(b), nil
}

// Flush sends the head on the first call, marked as a stream's, then what
// the handler wrote since the last flush as data. A flush with nothing new
// sends the head alone, so the edge has it while the stream is still quiet.
func (w *streamWriter) Flush() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.quiet {
		w.since = nil
		return
	}
	if !w.head {
		w.head = true
		w.refused = !w.sendResponse(w.status, w.header, nil, true)
	}
	if len(w.since) > 0 && !w.refused {
		w.sendData(w.since)
	}
	w.since = nil
}

// fail drops a panicked handler's tail; when the head never went, finish
// answers 500 for it.
func (w *streamWriter) fail() {
	if !w.head {
		w.status = http.StatusInternalServerError
	}
	w.failed = true
}

// finish sends what the handler's return leaves: the whole answer when it
// never flushed, as a stream when it will not fit one frame, any piece it
// wrote since its last flush, and the end.
func (w *streamWriter) finish() {
	if w.quiet {
		return
	}
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if !w.head {
		body := w.since
		if w.failed {
			body = nil
		}
		w.sendResponse(w.status, w.header, body, false)
	} else if len(w.since) > 0 && !w.failed && !w.refused {
		w.sendData(w.since)
	}
	if w.quiet {
		return
	}
	if err := w.l.write(w.ctx, w.c, endFrame{Type: "end", ID: w.id}); err != nil && w.ctx.Err() == nil {
		w.l.Log.Warn("edge link: an end not sent", "id", text(w.id), "err", err)
	}
}

// sendResponse sends the head and says whether its frame went as asked. A
// flushed answer's head is asked with stream: the edge opens a stream for a
// call only at a marked head, and answers at an unmarked one whole. A whole
// answer that encodes past maxFrame is never sent in one frame: it goes as
// a stream too, the head marked and empty, then data frames, so one large
// answer crosses the link instead of closing it. Only a head that alone is
// too large, its headers, is answered 413, with a short error. A stream it
// made and a 413 both report false, so the caller sends no more after either.
func (w *streamWriter) sendResponse(status int, header http.Header, body []byte, stream bool) bool {
	b, err := json.Marshal(responseFrame{Type: "response", ID: w.id, Status: status, Header: header, Body: body, Stream: stream})
	if err != nil {
		w.quiet = true
		w.l.Log.Warn("edge link: an answer that encodes to nothing, dropped", "id", text(w.id), "err", err)
		return false
	}
	if len(b) <= maxFrame {
		return w.writeResponse(b)
	}
	head, herr := json.Marshal(responseFrame{Type: "response", ID: w.id, Status: status, Header: header, Stream: true})
	if herr != nil || len(head) > maxFrame {
		w.l.Log.Warn("edge link: an answer whose head alone is larger than one frame carries, answered 413", "id", text(w.id), "bytes", len(b))
		b, err = json.Marshal(responseFrame{Type: "response", ID: w.id, Status: http.StatusRequestEntityTooLarge,
			Header: http.Header{}, Body: []byte("the answer's head is larger than one frame of the link carries")})
		if err != nil || len(b) > maxFrame {
			// The id alone fills a frame: nothing truthful fits in one.
			w.quiet = true
			return false
		}
		// A 413 carries no body: report it unwhole, as a stream, so the
		// caller sends no data after it.
		w.writeResponse(b)
		return false
	}
	if !w.writeResponse(head) {
		return false
	}
	w.sendData(body)
	return false
}

// writeResponse sends b as the response frame it already encodes.
func (w *streamWriter) writeResponse(b []byte) bool {
	if err := w.l.writeBytes(w.ctx, w.c, b); err != nil {
		w.quiet = true
		if w.ctx.Err() == nil {
			w.l.Log.Warn("edge link: an answer not sent", "id", text(w.id), "err", err)
		}
		return false
	}
	return true
}

// sendData sends one flushed piece as data frames, split where one frame
// would not carry it.
func (w *streamWriter) sendData(piece []byte) {
	for len(piece) > 0 {
		n := min(len(piece), maxDataBody)
		var b []byte
		for {
			if n == 0 {
				// The id alone fills a frame: nothing of the piece fits in one.
				w.quiet = true
				return
			}
			var err error
			b, err = json.Marshal(dataFrame{Type: "data", ID: w.id, Body: piece[:n]})
			if err == nil && len(b) <= maxFrame {
				break
			}
			n /= 2
		}
		if err := w.l.writeBytes(w.ctx, w.c, b); err != nil {
			w.quiet = true
			if w.ctx.Err() == nil {
				w.l.Log.Warn("edge link: a stream's piece not sent", "id", text(w.id), "err", err)
			}
			return
		}
		piece = piece[n:]
	}
}
