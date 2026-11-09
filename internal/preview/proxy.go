package preview

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"yardr.dev/yardr/internal/edgelink"
	"yardr.dev/yardr/internal/store"
)

// Handler is the supervisor's yard-side endpoint: the edge reaches a
// preview through it, never directly. It serves /preview/<app>/... —
// the path past the app is forwarded whole, so an ordinary web app
// written for / runs unmodified — checks the app is running (starting
// an approved, stopped one on demand) and answering (waiting for one
// that has not bound its port yet), counts the preview's requests
// in flight, records the request's moment, and streams the answer
// back, WebSockets included. No edge credential is forwarded: the
// router strips its own names, and this drops the same names itself,
// so the yard-side rule does not depend on the router having stripped
// first. A watch runs nothing to forward to: the supervisor serves its
// files itself (watch.go), under the same counting. The handler also
// takes a socket's frames on the link (socket.go): the link offers
// each socket to a Handler that implements edgelink.SocketHandler,
// which this one does, for the served-app endpoint the frames name.
func (s *Supervisor) Handler() http.Handler {
	return previewHandler{sup: s}
}

// previewHandler is Handler's value: an http.Handler for requests and
// an edgelink.SocketHandler for sockets, both served by the supervisor.
type previewHandler struct{ sup *Supervisor }

func (h previewHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.sup.servePreview(w, r)
}

func (h previewHandler) ServeSocket(req *edgelink.SocketRequest, sock *edgelink.Socket) {
	h.sup.serveSocket(req, sock)
}

// edgeHeaders are the names a request through the edge carries that must
// never reach the app.
var edgeHeaders = []string{"X-Edge-Token", "X-Edge-JWT", "Edge-Yard", "X-Edge-Refusal"}

var edgeCookies = []string{"ory_kratos_session", "edge_yard", "__Host-edge_preview"}

func (s *Supervisor) servePreview(w http.ResponseWriter, r *http.Request) {
	app, rest, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/preview/"), "/")
	if !ok {
		rest = ""
	}
	if app == "" || store.CheckPreviewApp(app) != nil {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	p, err := s.Store.GetPreview(ctx, app)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if p.Kind == store.PreviewKindWatch {
		s.servePreviewWatch(w, r, p, rest)
		return
	}
	if answerFromRow(w, r, p) {
		return
	}
	if !s.Running(app) {
		// On-demand start of an approved, stopped preview: its person has
		// arrived, so start it.
		if p.Approval == nil {
			http.NotFound(w, r)
			return
		}
		// A row that says running while no process is held is not one to start,
		// for the supervisor is not done with it: the process has left and what
		// follows is not recorded yet, and a crash leaves its row running until
		// the restart has been made (reconcile leaves it alone for the same
		// reason). Start refuses it, which would turn the visitor away from a
		// restart that is a moment off, so the request waits for the port, as the
		// one that made the start does.
		if p.State != store.PreviewRunning {
			if p.State == store.PreviewStopped || p.State == store.PreviewApproved {
				// The flip acts on the row this request read, not on the row as it is
				// by now: another request's start may have made it running since, and
				// approved written over that would leave the process under a row that
				// does not say it runs. A flip that finds the row changed lost to a
				// writer that went on from it; what follows reads the row as it stands.
				d := ""
				if _, err := s.Store.UpdatePreview(ctx, app, store.PreviewUpdate{ExpectedState: p.State, State: store.PreviewApproved, StateDetail: &d}); err != nil {
					s.Log.Debug("preview: on-demand flip not made", "app", app, "err", err)
				}
			}
			if err := s.Start(ctx, app); err != nil {
				// The reason names what the machine holds, a path or an interface
				// address: the operator reads it in the log and, for a start that
				// failed, on the row (failStart), and a visitor is told that the
				// preview cannot start and no more. That is true of every refusal,
				// though not every one leaves the row failed, so the answer is not
				// the row's (answerFromRow).
				s.Log.Warn("preview: on-demand start not started", "app", app, "err", err)
				http.Error(w, "preview "+app+" cannot start", http.StatusServiceUnavailable)
				return
			}
		}
	}
	// A process that is held is not an app that answers: one the supervisor has
	// just started (a restart's, or its own pass's after a server restart or the
	// card's Restart) has not bound its port yet. Whoever made the start, the
	// request waits for the bind and is answered by the app; proxied at once it
	// would meet a port that refuses it. A port that answers is found at the
	// first look, so a preview that is up costs the request that one dial. The
	// wait is not the request's time in flight, so it comes before the cap.
	host := s.waitPort(ctx, app, p.Port)
	if host == "" {
		// The supervisor may have given the app up while the request
		// waited (a wide bind refused, a start that never listened, too
		// many crashes): its row says so, and answers as it does a request
		// that comes after.
		if cur, err := s.Store.GetPreview(ctx, app); err != nil {
			http.NotFound(w, r)
		} else if !answerFromRow(w, r, cur) {
			http.Error(w, "preview "+app+" is starting", http.StatusServiceUnavailable)
		}
		return
	}
	leave, ok := s.admit(w, r, app)
	if !ok {
		return
	}
	defer leave()
	now := time.Now()
	_, _ = s.Store.UpdatePreview(ctx, app, store.PreviewUpdate{LastRequest: &now})

	target, _ := url.Parse("http://" + net.JoinHostPort(host, strconv.Itoa(p.Port)))
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.URL.Path = "/" + rest
			stripEdgeCredentials(pr.Out.Header)
			pr.Out.Header.Set("X-Preview-App", app)
		},
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "preview "+app+" is not answering", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}

// answerFromRow answers a request for a preview whose row says it cannot be
// reached, and reports whether it did: a proposal or a refused one is not
// there to anyone, and a crashed or failed one says it is down, with no more
// than its state, since its detail can name a path or an interface address of
// the machine and this answers visitors.
func answerFromRow(w http.ResponseWriter, r *http.Request, p *store.Preview) bool {
	switch p.State {
	case store.PreviewPending, store.PreviewRefused:
		http.NotFound(w, r)
		return true
	case store.PreviewCrashed, store.PreviewFailed:
		http.Error(w, fmt.Sprintf("preview %s is %s", p.App, p.State), http.StatusServiceUnavailable)
		return true
	}
	return false
}

// waitPort waits until the preview's port answers, the start wait runs out,
// or the supervisor gives the app up: no process runs for it, and its row no
// longer says it is to (it was refused, never listened, crashed too often or
// exited). A crash that restarts is not given up: its row still says running
// or approved until the restart has been made, and the request waits for it.
// The start wait is the supervisor's own wait for a bind and defaultStartWait
// beyond it, so the verdict on a start that never listens, which is due at the
// bind wait, is on the row before the request stops waiting. It returns the
// host the port answers on, or "" where the wait ended without an answer.
func (s *Supervisor) waitPort(ctx context.Context, app string, port int) string {
	deadline := time.Now().Add(s.bindWait() + defaultStartWait)
	probe := s.portProbe
	if probe == nil {
		probe = loopbackHost
	}
	for time.Now().Before(deadline) {
		if host := probe(port); host != "" {
			return host
		}
		if !s.Running(app) {
			if cur, err := s.Store.GetPreview(ctx, app); err != nil || (cur.State != store.PreviewRunning && cur.State != store.PreviewApproved) {
				return ""
			}
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(25 * time.Millisecond):
		}
	}
	return ""
}

// admit counts the request in flight for app, as enter does, and says
// what to call when it is done. The 21st request in flight waits two
// seconds for room, then is answered with the supervisor's own 503; ok is
// false for it, and for a request whose caller left meanwhile.
func (s *Supervisor) admit(w http.ResponseWriter, r *http.Request, app string) (leave func(), ok bool) {
	if !s.enter(app) {
		select {
		case <-r.Context().Done():
			return nil, false
		case <-time.After(2 * time.Second):
		}
		if !s.enter(app) {
			http.Error(w, "preview "+app+": too many requests in flight", http.StatusServiceUnavailable)
			return nil, false
		}
	}
	return func() { s.leave(app) }, true
}

// enter counts one more request in flight for app, refusing past the
// cap.
func (s *Supervisor) enter(app string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inflight[app] >= store.PreviewMaxInFlight {
		return false
	}
	s.inflight[app]++
	return true
}

func (s *Supervisor) leave(app string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inflight[app] > 0 {
		s.inflight[app]--
	}
}

// hasInFlight reports whether app has a request in flight now. A reconcile
// pass uses its earlier copy to catch a release before its row read, and this
// live read catches a request admitted after that copy.
func (s *Supervisor) hasInFlight(app string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inflight[app] > 0
}

// stripEdgeCredentials removes edge headers and cookies from a request
// before forwarding it, keeping the app's own cookies.
func stripEdgeCredentials(h http.Header) {
	for _, name := range edgeHeaders {
		h.Del(name)
	}
	if c := stripEdgeCookies(h.Get("Cookie")); c != "" {
		h.Set("Cookie", c)
	} else {
		h.Del("Cookie")
	}
}

func stripEdgeCookies(v string) string {
	var kept []string
	for _, part := range strings.Split(v, ";") {
		name, _, _ := strings.Cut(strings.TrimSpace(part), "=")
		if !slices.Contains(edgeCookies, name) {
			kept = append(kept, strings.TrimSpace(part))
		}
	}
	return strings.Join(kept, "; ")
}
