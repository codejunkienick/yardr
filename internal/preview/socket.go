package preview

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"yardr.dev/yardr/internal/edgelink"
	"yardr.dev/yardr/internal/store"
)

// A preview's WebSocket is carried on the preview link as a socket
// (design yardr-hosted-1xkc sections 1, 3 and 5): the edge holds the
// browser's handshake, sends a socket frame, and the supervisor here
// dials the app's loopback port as a WebSocket client, answers the
// head with the app's choice, and relays whole messages both ways.
// The frames serve the supervisor's served-app endpoint, not previews
// as a kind (section 7): the path names the app as a request's does,
// and only a watch, which runs nothing, is refused by kind.

const (
	// maxSocketsPerPreview is how many sockets one preview may hold
	// open at once (section 3); the eleventh is refused naming it.
	maxSocketsPerPreview = 10
	// defaultSocketRefresh is how often a socket's frames may refresh
	// its preview's last-request moment, so the store is not written
	// per frame (section 3). Supervisor.SocketRefresh shortens it.
	defaultSocketRefresh = time.Minute
)

func (s *Supervisor) socketRefresh() time.Duration {
	if s.SocketRefresh > 0 {
		return s.SocketRefresh
	}
	return defaultSocketRefresh
}

// serveSocket serves one socket the edge opened on the link, as
// previewHandler offers it. It returns when the socket ends.
func (s *Supervisor) serveSocket(req *edgelink.SocketRequest, sock *edgelink.Socket) {
	ctx := sock.Context()
	u, err := url.Parse(req.Path)
	if err != nil || !strings.HasPrefix(u.Path, "/preview/") {
		_ = sock.Reject(http.StatusNotFound, nil, []byte("not found"))
		return
	}
	app, rest, ok := strings.Cut(strings.TrimPrefix(u.Path, "/preview/"), "/")
	if !ok {
		rest = ""
	}
	if app == "" || store.CheckPreviewApp(app) != nil {
		_ = sock.Reject(http.StatusNotFound, nil, []byte("not found"))
		return
	}
	p, err := s.Store.GetPreview(ctx, app)
	if err != nil {
		_ = sock.Reject(http.StatusNotFound, nil, []byte("not found"))
		return
	}
	if p.Kind == store.PreviewKindWatch {
		// A watch runs nothing to dial: its page already follows files
		// by server-sent events, and an upgrade to it is refused 501
		// by the supervisor's own answer (design, Left out).
		_ = sock.Reject(http.StatusNotImplemented, nil, []byte("preview "+app+" is a watch and carries no WebSockets"))
		return
	}
	if status, body, refused := socketRefusal(p); refused {
		_ = sock.Reject(status, nil, []byte(body))
		return
	}
	if !s.Running(app) {
		// On-demand start of an approved, stopped preview, as a
		// request makes it (proxy.go): its person has arrived.
		if p.Approval == nil {
			_ = sock.Reject(http.StatusNotFound, nil, []byte("not found"))
			return
		}
		if p.State != store.PreviewRunning {
			if p.State == store.PreviewStopped || p.State == store.PreviewApproved {
				d := ""
				if _, err := s.Store.UpdatePreview(ctx, app, store.PreviewUpdate{ExpectedState: p.State, State: store.PreviewApproved, StateDetail: &d}); err != nil {
					s.Log.Debug("preview: on-demand flip not made", "app", app, "err", err)
				}
			}
			if err := s.Start(ctx, app); err != nil {
				s.Log.Warn("preview: on-demand start not started", "app", app, "err", err)
				_ = sock.Reject(http.StatusServiceUnavailable, nil, []byte("preview "+app+" cannot start"))
				return
			}
		}
	}
	host := s.waitPort(ctx, app, p.Port)
	if host == "" {
		if cur, err := s.Store.GetPreview(ctx, app); err != nil {
			_ = sock.Reject(http.StatusNotFound, nil, []byte("not found"))
		} else if status, body, refused := socketRefusal(cur); refused {
			_ = sock.Reject(status, nil, []byte(body))
		} else {
			_ = sock.Reject(http.StatusServiceUnavailable, nil, []byte("preview "+app+" is starting"))
		}
		return
	}
	if refusal := s.enterSocket(app); refusal != "" {
		_ = sock.Reject(http.StatusServiceUnavailable, nil, []byte(refusal))
		return
	}
	defer func() {
		// The in-flight count is given back last, once the close's moment
		// is in the row, and even if that write panics (the link recovers
		// a handler's panic): a count that left with the socket would let
		// a pass find nothing in flight while the row is still as old as
		// the last frame, and stop the preview as idle at once.
		defer s.leave(app)
		if s.leaveSocket(app) {
			// The last socket is gone: the idle clock runs from
			// now, a full bound after it, not from a frame that
			// may be as old as the refresh throttle.
			s.touchSocket(context.WithoutCancel(ctx), app, true)
		}
	}()
	// The opening counts as a request does.
	s.touchSocket(ctx, app, true)

	header := req.Header.Clone()
	if header == nil {
		header = http.Header{}
	}
	// The yard drops the edge's credential names again, as it does
	// for a request, so this does not depend on the router having
	// stripped first (section 5). The browser hop's handshake names
	// are the edge's to have consumed; the yard makes its own hop.
	stripEdgeCredentials(header)
	for _, name := range []string{"Connection", "Upgrade", "Sec-Websocket-Key", "Sec-Websocket-Version", "Sec-Websocket-Extensions", "Sec-Websocket-Protocol"} {
		header.Del(name)
	}
	target := url.URL{Scheme: "ws", Host: net.JoinHostPort(host, strconv.Itoa(p.Port)), Path: "/" + rest, RawQuery: u.RawQuery}
	header.Set("X-Preview-App", app)
	// The dial carries the loopback Origin of its own target, not the
	// browser's true Origin from the frame: the app sees a loopback
	// Host, and a library that checks Origin against Host by default
	// refuses any other Origin with 403. Host stays loopback, as a
	// request's does, so Vite dev servers answer.
	header.Set("Origin", "http://"+target.Host)
	appConn, resp, err := websocket.Dial(ctx, target.String(), &websocket.DialOptions{
		HTTPHeader: header, Subprotocols: append([]string(nil), req.Protocols...),
		CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		if resp != nil && resp.StatusCode >= http.StatusBadRequest {
			// The app answered the dial with a refusal of its own:
			// its status and short body answer the head. A dial
			// that failed its handshake checks (a 101 that does
			// not verify, a page served instead) is not a refusal
			// the app chose, and is the supervisor's 502 below.
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			_ = sock.Reject(resp.StatusCode, resp.Header.Clone(), body)
			return
		}
		_ = sock.Reject(http.StatusBadGateway, nil, []byte("preview "+app+" is not answering"))
		return
	}
	defer appConn.CloseNow()
	appConn.SetReadLimit(edgelink.MaxSocketMessage)
	if err := sock.Accept(appConn.Subprotocol()); err != nil {
		return
	}
	s.relaySocket(ctx, app, sock, appConn)
}

// socketRefusal is answerFromRow for a socket head: a proposal or a
// refused preview is not there, and a crashed or failed one is down,
// with no more than its state.
func socketRefusal(p *store.Preview) (status int, body string, refused bool) {
	switch p.State {
	case store.PreviewPending, store.PreviewRefused:
		return http.StatusNotFound, "not found", true
	case store.PreviewCrashed, store.PreviewFailed:
		return http.StatusServiceUnavailable, fmt.Sprintf("preview %s is %s", p.App, p.State), true
	}
	return 0, "", false
}

// enterSocket counts one socket for app in flight for its life, as a
// request is counted (proxy.go), and among its sockets: past ten
// sockets, or past the in-flight cap the two share, it refuses with
// words that name the preview. leaveSocket gives the socket back, and
// serveSocket gives the in-flight count back with leave.
func (s *Supervisor) enterSocket(app string) (refusal string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sockets == nil {
		s.sockets = map[string]int{}
	}
	if s.sockets[app] >= maxSocketsPerPreview {
		return fmt.Sprintf("preview %s: too many sockets open", app)
	}
	if s.inflight[app] >= store.PreviewMaxInFlight {
		return fmt.Sprintf("preview %s: too many requests in flight", app)
	}
	s.sockets[app]++
	s.inflight[app]++
	return ""
}

// leaveSocket gives back the socket enterSocket counted, and says
// whether that was the preview's last. The in-flight count enterSocket
// also took is left for the caller to give back, after the last
// socket's close is written to the row.
func (s *Supervisor) leaveSocket(app string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sockets[app] > 0 {
		s.sockets[app]--
	}
	return s.sockets[app] == 0
}

// touchSocket records a socket's activity as its preview's last
// request, at most once per refresh unless forced: the opening and
// the last close are forced, frames between are throttled. The
// throttle is judged on the row's own moment, so a moment an earlier
// pass left old is refreshed by the next frame however soon it
// follows the opening's write.
func (s *Supervisor) touchSocket(ctx context.Context, app string, force bool) {
	now := time.Now()
	if !force {
		if p, err := s.Store.GetPreview(ctx, app); err == nil && p.LastRequestAt != nil &&
			now.Sub(*p.LastRequestAt) < s.socketRefresh() {
			return
		}
		s.mu.Lock()
		if s.touched == nil {
			s.touched = map[string]time.Time{}
		}
		if last, ok := s.touched[app]; ok && now.Sub(last) < s.socketRefresh() {
			s.mu.Unlock()
			return
		}
		s.touched[app] = now
		s.mu.Unlock()
	} else {
		s.mu.Lock()
		if s.touched == nil {
			s.touched = map[string]time.Time{}
		}
		s.touched[app] = now
		s.mu.Unlock()
	}
	_, _ = s.Store.UpdatePreview(ctx, app, store.PreviewUpdate{LastRequest: &now})
}

// relaySocket carries whole messages between the link socket and the
// app socket until either ends, and ends the other with the same
// close. Each hop answers its own pings; none crosses (section 1).
// The app hop's context is the socket's without its cancellation: a
// read on it with that context would close the hop at once when the
// link socket ends, before the loop below can end it with the
// edge's close code.
func (s *Supervisor) relaySocket(ctx context.Context, app string, sock *edgelink.Socket, appConn *websocket.Conn) {
	relayCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer cancel()
		for {
			typ, body, err := appConn.Read(relayCtx)
			if err != nil {
				code, reason := appCloseCode(err)
				_ = sock.Close(code, reason)
				return
			}
			s.touchSocket(relayCtx, app, false)
			kind := edgelink.SocketText
			if typ == websocket.MessageBinary {
				kind = edgelink.SocketBinary
			}
			if err := sock.WriteMessage(kind, body); err != nil {
				return
			}
		}
	}()
	for {
		kind, body, err := sock.ReadMessage()
		if err != nil {
			code, reason := edgeCloseCode(err)
			_ = appConn.Close(websocket.StatusCode(code), closeReason(reason))
			break
		}
		s.touchSocket(ctx, app, false)
		typ := websocket.MessageText
		if kind == edgelink.SocketBinary {
			typ = websocket.MessageBinary
		}
		if err := appConn.Write(relayCtx, typ, body); err != nil {
			break
		}
	}
	cancel()
	_ = appConn.CloseNow()
	<-done
}

// appCloseCode is the close an app socket ended with, as a code the
// link socket may carry: a reserved code becomes a normal closure,
// and a hop that dropped without a close frame (its process went,
// as a stop kills it) is a service restart, as the design names it.
func appCloseCode(err error) (int, string) {
	if errors.Is(err, websocket.ErrMessageTooBig) {
		return int(websocket.StatusMessageTooBig), "the message is larger than a socket carries"
	}
	code := websocket.CloseStatus(err)
	if code == -1 || code == websocket.StatusAbnormalClosure {
		return int(websocket.StatusServiceRestart), ""
	}
	var ce websocket.CloseError
	reason := ""
	if errors.As(err, &ce) {
		reason = ce.Reason
	}
	return validCloseCode(int(code)), reason
}

// edgeCloseCode is the close the link socket ended with.
func edgeCloseCode(err error) (int, string) {
	var sce *edgelink.SocketCloseError
	if errors.As(err, &sce) {
		return validCloseCode(sce.Code), sce.Reason
	}
	return int(websocket.StatusNormalClosure), ""
}

func validCloseCode(code int) int {
	switch {
	case code == 1000, code >= 1001 && code <= 1014 && code != 1004 && code != 1005 && code != 1006,
		code >= 3000 && code <= 4999:
		return code
	}
	return int(websocket.StatusNormalClosure)
}

// closeReason cuts a close reason to what a WebSocket close frame
// carries (125 bytes at most), on a character boundary.
func closeReason(reason string) string {
	if len(reason) <= 123 {
		return reason
	}
	runes := []rune(reason)
	for len(string(runes)) > 123 {
		runes = runes[:len(runes)-1]
	}
	return string(runes)
}
