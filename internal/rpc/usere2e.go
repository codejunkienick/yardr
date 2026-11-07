package rpc

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"yardr.dev/yardr/internal/yardkey"
)

// The yard opens envelopes where its link hands it calls (docs/design/
// 2026-11-05-yardr-device-e2e.md, section 3.2, candidate 2B): a device
// that paired by QR holds a channel session with the yard, founded by
// a Noise_IK handshake to the yard's channel key, and sends each of
// its calls as one whole inner call — method, path, headers and body —
// sealed in records (internal/yardkey, ChannelSession) inside one
// outer call on the fixed path E2EPath. The yard opens the envelope
// after the link's frame check (internal/edgelink), serves the inner
// call exactly as its listener serves it, and seals the whole answer
// the same way: the outer head the link carries back shows only the
// envelope's own status (200 when the yard sealed an answer, whatever
// the inner answer holds) and headers that name the envelope and the
// call, never the inner status or an inner header.
//
// The channel key, not the token, authenticates an enveloped call: no
// Authorization is read here, the handshake proved the device's pinned
// static key, and each new call's first record re-reads the device
// record its session names, so a call is judged against the device
// list as it stands when the call begins. Enveloped calls are taken
// only down the link (RemoteAddr is the link's): at the yard's own
// listener /user/e2e is what it was before, answered as any path under
// /user/ is, so the socket API and the LAN behaviour are unchanged.
//
// Whole calls were E2E 2a's (yardr-j2nm) scope; E2E 2b seals streamed
// answers — the events stream among them — record by record as they
// flush, every answer closed by its sealed final record.
const (
	// E2EPath is the one fixed outer path of an enveloped call.
	E2EPath = "/user/e2e"
	// E2ECallHeader names a call's id on its outer call, as the device
	// sends it and as the router copies it into the link frame's
	// header unchanged: sixteen random bytes, base64url. It is a claim
	// to be checked against the records' sealed headers, never a
	// warrant, as a frame's id is.
	E2ECallHeader = "X-Edge-E2E-Call"
	// E2EStreamHeader is the outer stream marker in the clear: the
	// device sets it exactly when the inner answer may sit idle
	// without bound, and the router copies its presence and value into
	// the link frame's header beside the call id, dropping the
	// device's own. The yard binds what the frame carried into the
	// first record's associated data and so checks, never trusts, it.
	E2EStreamHeader = "X-Edge-E2E-Stream"
	// E2EStreamLongLived is the marker's one value today.
	E2EStreamLongLived = "long-lived"
	// e2eEnvelopeHeader names an outer answer as a sealed envelope,
	// with the call it answers named by E2ECallHeader beside it: all
	// the outer head says of what is inside.
	e2eEnvelopeHeader = "X-Edge-E2E"
	// maxE2EBody bounds one outer call's record run: a request's
	// plaintext budget (1 MiB, the edge's own) with its records'
	// headers and tags over it.
	maxE2EBody = (1 << 20) + (64 << 10)
	// maxE2EAnswer bounds the sealed answer body the edge carries. The
	// inner head, body, record headers and tags all count toward it.
	maxE2EAnswer = 16 << 20
)

// e2eSession is one live channel session a handshake founded: its
// transport keys in the yard's hands, and the device record it names,
// as the handshake proved it. It is a cached credential, not a copy of
// the record: every call re-reads the record (e2eDevice) before it is
// served.
type e2eSession struct {
	keys       *yardkey.ChannelSession
	user       string
	device     string
	channelKey string
	lastUsed   time.Time // last handshake, call or streamed record, for e2eSessionMaxIdle
}

const (
	// e2eSessionMaxIdle is how long a channel session may sit unused
	// in server memory before it expires: a session no call or record
	// has touched in a day is gone, and its next call handshakes again.
	e2eSessionMaxIdle = 24 * time.Hour
	// e2eSessionsPerDevice caps the channel sessions one device holds
	// resident: one. A fresh handshake from a device replaces its old
	// session, so repeated app restarts leave at most this many.
	e2eSessionsPerDevice = 1
)

func (u *UserServer) e2eNow() time.Time {
	if u.now != nil {
		return u.now()
	}
	return time.Now()
}

// e2eSessionFor answers the live session tag names, touching it: an
// entry past its idle bound is dropped and answers none.
func (u *UserServer) e2eSessionFor(tag string) *e2eSession {
	u.mu.Lock()
	defer u.mu.Unlock()
	s := u.e2eSessions[tag]
	if s == nil {
		return nil
	}
	if u.e2eNow().Sub(s.lastUsed) > e2eSessionMaxIdle {
		delete(u.e2eSessions, tag)
		return nil
	}
	s.lastUsed = u.e2eNow()
	return s
}

// touchE2ESessionIfLive marks s used now while confirming that it is
// still held: a stream's next record is admitted against the current
// session entry, not one a handshake replaced between two checks.
func (u *UserServer) touchE2ESessionIfLive(s *e2eSession) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	now := u.e2eNow()
	for tag, live := range u.e2eSessions {
		if live != s {
			continue
		}
		if now.Sub(s.lastUsed) > e2eSessionMaxIdle {
			delete(u.e2eSessions, tag)
			return false
		}
		s.lastUsed = now
		return true
	}
	return false
}

// storeE2ESession keeps s under tag, replacing every session its
// device already held (e2eSessionsPerDevice is one): an app that
// restarts and handshakes again leaves its old session refused.
func (u *UserServer) storeE2ESession(tag string, s *e2eSession) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.e2eSessions == nil {
		u.e2eSessions = map[string]*e2eSession{}
	}
	u.expireE2ELocked()
	for old, live := range u.e2eSessions {
		if live.user == s.user && live.device == s.device {
			delete(u.e2eSessions, old)
		}
	}
	u.e2eSessions[tag] = s
}

// expireE2ESessions drops every session past its idle bound.
func (u *UserServer) expireE2ESessions() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.expireE2ELocked()
}

func (u *UserServer) expireE2ELocked() {
	now := u.e2eNow()
	for tag, s := range u.e2eSessions {
		if now.Sub(s.lastUsed) > e2eSessionMaxIdle {
			delete(u.e2eSessions, tag)
		}
	}
}

// purgeE2EDevice drops every session entry the device user/device
// founded, and no other device's: on its removal, the device's next
// call, its live streams' next records and its next handshake (which
// finds no device record to pin) are each refused.
func (u *UserServer) purgeE2EDevice(user, device string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for tag, s := range u.e2eSessions {
		if s.user == user && s.device == device {
			delete(u.e2eSessions, tag)
		}
	}
}

// purgeE2EUser drops every session of a user who was removed, whose
// devices went with them.
func (u *UserServer) purgeE2EUser(user string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for tag, s := range u.e2eSessions {
		if s.user == user {
			delete(u.e2eSessions, tag)
		}
	}
}

// dropE2ESession drops the one entry tag names, as a call whose device
// record is gone drops the entry it came on.
func (u *UserServer) dropE2ESession(tag string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	delete(u.e2eSessions, tag)
}

// watchE2ERemovals purges channel sessions as device and user
// removals commit, for as long as ctx runs (UserServer.Run): it is
// the backstop behind the first-record re-read (e2eDevice), which
// refuses a call whose first record crosses the commit whatever this
// watch has yet to reach. An operation admitted before the commit —
// its first record already re-read — is not recalled by a purge: a
// whole call so admitted finishes, and no new call or streamed record
// is admitted after it.
func (u *UserServer) watchE2ERemovals(ctx context.Context) {
	after, err := u.st.Head(ctx)
	if err != nil {
		return
	}
	// Sessions whose device already went, before this watch stood
	// up, go now. The head came first so a removal during this check
	// remains in the subscription's backlog instead of slipping into
	// a gap between reconciliation and subscription.
	u.mu.Lock()
	held := make([]*e2eSession, 0, len(u.e2eSessions))
	for _, s := range u.e2eSessions {
		held = append(held, s)
	}
	u.mu.Unlock()
	for _, s := range held {
		if !u.e2eDeviceCtx(ctx, s) {
			u.purgeE2EDevice(s.user, s.device)
		}
	}
	for ctx.Err() == nil {
		events, err := u.st.Subscribe(ctx, after)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		for e := range events {
			after = e.Seq
			user, _ := e.Data["user"].(string)
			switch e.Kind {
			case "device_removed":
				device, _ := e.Data["device"].(string)
				u.purgeE2EDevice(user, device)
			case "user_removed":
				u.purgeE2EUser(user)
			}
		}
	}
}

// routes is the mux of the paths under /user/ a device calls, as
// Handler serves them; an enveloped inner call is served by the same
// mux, with its caller already resolved by its channel.
func (u *UserServer) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /user/call/{method}", u.call)
	mux.HandleFunc("GET /user/events", u.events)
	mux.HandleFunc("GET /user/blobs/{hash}", u.userBlob)
	mux.HandleFunc("GET /user/me", func(w http.ResponseWriter, r *http.Request) {
		c := callerOf(r)
		writeJSON(w, struct {
			Caller
			Yard       string `json:"yard"`
			Yardmaster string `json:"yardmaster"`
		}{*c, u.yard(r.Context()), u.yardmaster(r.Context())})
	})
	mux.HandleFunc("POST /user/push/voucher", u.pushVoucher)
	return mux
}

// e2e serves one outer call that came down the link: a channel
// handshake when it names no call, an enveloped whole call when it
// does. It is answered before any token is looked at, and only for a
// request whose RemoteAddr is the link's, which Handler checks.
func (u *UserServer) e2e(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxE2EBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	if r.Header.Get(E2ECallHeader) == "" {
		u.e2eHandshake(w, r, body)
		return
	}
	u.e2eCall(w, r, body)
}

// e2eHandshake opens a channel handshake's first message with the
// yard's channel key and founds its session: the device the message
// proves must be one a live device record pins, by that key, or no
// session is made. The answer is the handshake's second message, whose
// payload is the session tag the yard minted.
func (u *UserServer) e2eHandshake(w http.ResponseWriter, r *http.Request, first []byte) {
	channel, err := u.pairingChannel()
	var opened *yardkey.ChannelHandshake
	if err == nil {
		opened, err = yardkey.OpenChannelHandshake(channel, first)
	}
	if err != nil {
		u.log.Warn("a channel handshake refused", "err", err)
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	device, err := u.st.DeviceByChannelKey(r.Context(), opened.DeviceKey)
	if err != nil {
		u.log.Error("a channel handshake's device could not be read", "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	if device == nil {
		u.log.Warn("a channel handshake from a key no device pins")
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}
	tag, err := yardkey.NewSessionTag()
	var reply []byte
	var keys *yardkey.ChannelSession
	if err == nil {
		reply, keys, err = opened.SealAnswer(tag)
	}
	if err != nil {
		u.log.Error("a channel handshake answer could not be sealed", "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	tagString := string(tag)
	session := &e2eSession{keys: keys, user: device.User, device: device.Name, channelKey: opened.DeviceKey, lastUsed: u.e2eNow()}
	u.storeE2ESession(tagString, session)
	// The first lookup proved the key before the handshake was answered,
	// but removal may commit while the handshake is being sealed. Check
	// again after storing so a session cannot be installed behind the
	// removal watch's event; a later removal is caught by that watch.
	current, err := u.st.DeviceByChannelKey(r.Context(), opened.DeviceKey)
	if err != nil {
		u.dropE2ESession(tagString)
		u.log.Error("a channel handshake's device could not be read", "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	if current == nil || current.User != device.User || current.Name != device.Name {
		u.dropE2ESession(tagString)
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}
	// The answer founds a session and holds its tag: nothing between
	// keeps it readable but the device.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set(e2eEnvelopeHeader, "handshake")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(reply)
}

// e2eDevice re-reads the device record session names, as it stands
// now: the call is refused when the record is gone or pins another
// key. One store read a call is the price, and it is the read a plain
// call already pays to resolve its token.
func (u *UserServer) e2eDevice(r *http.Request, s *e2eSession) bool {
	return u.e2eDeviceCtx(r.Context(), s)
}

func (u *UserServer) e2eDeviceCtx(ctx context.Context, s *e2eSession) bool {
	devices, err := u.st.Devices(ctx, s.user)
	if err != nil {
		u.log.Error("an enveloped call's device could not be read", "err", err)
		return false
	}
	for _, d := range devices {
		if d.Name == s.device {
			return d.ChannelKey == s.channelKey
		}
	}
	return false
}

// e2eCall opens one enveloped call and seals its answer: whole when
// its handler never flushes, record by record as it flushes when it
// does, and closed by the sealed final record either way.
// Its outer head is the envelope's alone: 200 with the envelope's
// headers when an answer was sealed, the envelope's own refusal
// otherwise, and in neither an inner status or header.
func (u *UserServer) e2eCall(w http.ResponseWriter, r *http.Request, run []byte) {
	call, err := base64.RawURLEncoding.DecodeString(r.Header.Get(E2ECallHeader))
	if err != nil || len(call) != yardkey.E2ECallSize {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	msgs, err := yardkey.SplitRecords(run)
	if err != nil || len(msgs) == 0 || len(msgs[0]) < yardkey.E2ETagSize {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	tag := string(msgs[0][:yardkey.E2ETagSize])
	session := u.e2eSessionFor(tag)
	if session == nil {
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}
	// Each new call's first record re-reads the device record its
	// session names, before anything of the call is served: this
	// re-read is the call's admission. A call admitted here — before
	// a removal of its device commits — may finish; a first record
	// that crosses the removal finds no device, drops the session
	// entry it came on, and is refused, as every later call is.
	if !u.e2eDevice(r, session) {
		u.dropE2ESession(tag)
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}
	// A handshake may have replaced this entry after e2eSessionFor
	// found it but before the device re-read admitted the call.
	if !u.touchE2ESessionIfLive(session) {
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}
	binding := e2eBinding(r)
	// Every message opens with its own copied cipher state, here in the
	// call's serving goroutine, under the number it carries.
	plain, err := session.keys.OpenWhole(run, call, binding)
	if err != nil {
		u.log.Warn("an enveloped call refused at its opening", "err", err)
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	head, innerBody, err := splitE2EHead(plain)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	var reqHead e2eRequestHead
	if err := json.Unmarshal(head, &reqHead); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	writer := newE2EAnswerWriter(w, session, call, binding, isEventsCall(reqHead))
	// A streamed answer's every record is a new admission: before it
	// seals, its session entry must still stand and its device still
	// be there, so a removal since the call was admitted seals no
	// next record and no final. A whole answer is the admitted call
	// finishing, and is not rechecked (finish, below).
	writer.live = func() bool {
		return u.touchE2ESessionIfLive(session) && u.e2eDevice(r, session)
	}
	if binding.Present && !writer.events {
		// A marked call is checked only now, after the envelope has
		// opened: a mark buys a long-lived slot for the events stream
		// alone, and any other inner call is refused sealed, unserved,
		// and holding no stream slot.
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		writer.WriteHeader(http.StatusForbidden)
		_, _ = writer.Write([]byte("a call marked long-lived may open only the events stream\n"))
	} else {
		u.serveInner(writer, r, session, reqHead, innerBody)
	}
	if err := writer.finish(); err != nil {
		u.log.Error("an enveloped answer could not be sealed", "err", err)
		if !writer.wrote {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
	}
}

// isEventsCall says whether an opened inner call is the events
// stream: the one inner call a marked outer call may carry, and the
// one stream the per-credential cap and the long-lived rules name.
func isEventsCall(head e2eRequestHead) bool {
	path := head.Path
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	return head.Method == http.MethodGet && path == "/user/events"
}

// e2eBinding is the first record's binding as this yard checks it: the
// outer path the frame actually named, and the stream marker the frame
// carried beside the call id — what the router copied there, which is
// what the device sent unless it was rewritten on the way.
func e2eBinding(r *http.Request) *yardkey.FirstBinding {
	_, present := r.Header[http.CanonicalHeaderKey(E2EStreamHeader)]
	return &yardkey.FirstBinding{Path: r.URL.Path, Marker: r.Header.Get(E2EStreamHeader), Present: present}
}

// e2eRequestHead is an inner call's head as it travels sealed: its
// method, its path with its query, and its headers. The device token
// is not among them — the channel key is the call's credential — and
// any Authorization a client sealed anyway is dropped at serveInner.
type e2eRequestHead struct {
	Method string      `json:"method"`
	Path   string      `json:"path"`
	Header http.Header `json:"header"`
}

// e2eAnswerHead is an inner answer's head as it travels sealed: its
// status and its headers, told to the device alone.
type e2eAnswerHead struct {
	Status int         `json:"status"`
	Header http.Header `json:"header"`
}

// joinE2EHead is a sealed plaintext whole: the head as a
// length-prefixed JSON block, then the body as it stands.
func joinE2EHead(head []byte, body []byte) []byte {
	out := binary.BigEndian.AppendUint32(nil, uint32(len(head)))
	out = append(out, head...)
	return append(out, body...)
}

// splitE2EHead is joinE2EHead read back.
func splitE2EHead(plain []byte) (head, body []byte, err error) {
	if len(plain) < 4 {
		return nil, nil, fmt.Errorf("a sealed call with no head")
	}
	n := int(binary.BigEndian.Uint32(plain))
	if n > len(plain)-4 {
		return nil, nil, fmt.Errorf("a sealed head of %d bytes in %d", n, len(plain))
	}
	return plain[4 : 4+n], plain[4+n:], nil
}

// serveInner serves one opened inner call by the listener's own mux,
// as the caller its channel resolved, into w: the sealed answer's
// writer, which seals whole or record by record as the handler
// flushes. The inner request is served from the link's address, as
// the outer one came, so what a call may do down the link is judged
// as the listener judges it.
func (u *UserServer) serveInner(w http.ResponseWriter, outer *http.Request, session *e2eSession, head e2eRequestHead, body []byte) {
	answer := func(status int, header http.Header, answerBody []byte) {
		for name, values := range header {
			for _, v := range values {
				w.Header().Add(name, v)
			}
		}
		w.WriteHeader(status)
		_, _ = w.Write(answerBody)
	}
	if !strings.HasPrefix(head.Path, "/user/") && head.Path != "/user" {
		answer(http.StatusNotFound, http.Header{}, []byte("not found\n"))
		return
	}
	target, err := url.ParseRequestURI(head.Path)
	if err != nil {
		answer(http.StatusBadRequest, http.Header{}, []byte("bad request\n"))
		return
	}
	inner, err := http.NewRequestWithContext(outer.Context(), head.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		answer(http.StatusBadRequest, http.Header{}, []byte("bad request\n"))
		return
	}
	inner.URL, inner.RequestURI = target, head.Path
	inner.Host, inner.RemoteAddr = outer.Host, outer.RemoteAddr
	inner.Header = http.Header{}
	for name, values := range head.Header {
		// The channel is the credential: a token sealed inside is read
		// by nobody, and the link's own envelope headers name the outer
		// call, never the inner one.
		if strings.EqualFold(name, "Authorization") || strings.EqualFold(name, E2ECallHeader) || strings.EqualFold(name, E2EStreamHeader) {
			continue
		}
		for _, v := range values {
			inner.Header.Add(name, v)
		}
	}
	caller := &Caller{User: session.user, SaidBy: session.device, device: true}
	inner = inner.WithContext(context.WithValue(inner.Context(), callerKey{}, caller))
	// What came sealed says so: the calls only a sealed caller may make
	// (pushVoucher) ask for the session, which no plain call has.
	inner = inner.WithContext(context.WithValue(inner.Context(), e2eSessionKey{}, session))
	if isEventsCall(head) {
		// The stream was opened by its channel, not by a token: its
		// liveness check also confirms the channel session is still
		// held. A fresh handshake replaces it, and the next check ends
		// the old stream so it releases its per-device slot.
		inner = inner.WithContext(context.WithValue(inner.Context(), e2eStillKey{},
			func() bool { return u.touchE2ESessionIfLive(session) && u.e2eDevice(outer, session) }))
	}
	u.routes().ServeHTTP(w, inner)
}

// e2eStillKey carries an enveloped stream's liveness check in its
// request context, where events asks for it (streamStill, below).
type e2eStillKey struct{}

// e2eSessionKey carries the channel session an inner call came on in its
// request context. Only serveInner sets it, never anything a client sends,
// so a call that has it came sealed.
type e2eSessionKey struct{}

// streamStill is an events stream's liveness check: an enveloped
// stream's own check where its context carries one, else the plain
// stream's — the credential it opened with, resolved again, must
// still name the same caller.
func (u *UserServer) streamStill(r *http.Request, c *Caller, credential string) bool {
	if check, ok := r.Context().Value(e2eStillKey{}).(func() bool); ok && check != nil {
		return check()
	}
	now, _ := u.resolveIn(r.Context(), credential, remote(r))
	return now != nil && *now == *c
}

// e2eTooLargeAnswer is the inner refusal when a whole response cannot fit
// in the edge's ciphertext bound. It is sealed like any other inner answer.
func e2eTooLargeAnswer() []byte {
	head, _ := json.Marshal(e2eAnswerHead{Status: http.StatusRequestEntityTooLarge, Header: http.Header{
		"Content-Type":           {"text/plain; charset=utf-8"},
		"X-Content-Type-Options": {"nosniff"},
	}})
	return joinE2EHead(head, []byte(http.StatusText(http.StatusRequestEntityTooLarge)+"\n"))
}

// e2eAnswerWriter is an inner answer as the yard seals it: held
// whole until its handler returns when the handler never flushes, and
// sealed record by record as it flushes when it does — the events
// stream's own way. Either way the answer closes with the sealed
// final record (yardkey.CallSealer), the outer head is written once,
// with the first sealed bytes or with the whole run, and shows only
// the envelope's own status and headers. A whole answer stops
// retaining bytes at the ciphertext budget and answers with a sealed
// refusal; a streamed answer that is no events stream and crosses
// that budget is left unclosed, so its receiver reports a truncation
// and a part blob is never taken for a whole one.
type e2eAnswerWriter struct {
	outer   http.ResponseWriter
	session *e2eSession
	call    []byte
	binding *yardkey.FirstBinding
	sealer  *yardkey.CallSealer
	events  bool // the events stream: no answer bound, no deadline

	header   http.Header
	status   int
	pending  []byte // inner body written, not yet sealed
	started  bool   // the outer head and first records are sent
	wrote    bool   // the outer head is sent, whole or streamed
	overflow bool   // a whole answer past the retention bound
	capped   bool   // a streamed answer past the bound: left unclosed
	sealed   int    // framed bytes sent of a streamed answer
	err      error  // a sealing or sending failure, kept whole
	live     func() bool
	gone     bool // the session went mid-stream: no next record, no final
}

// streamLive admits a streamed answer's next record: nil live (a
// writer a test drives whole) always admits; a failed check is kept,
// so no later record of the stream is admitted either.
func (w *e2eAnswerWriter) streamLive() bool {
	if w.live == nil {
		return true
	}
	if w.gone {
		return false
	}
	if !w.live() {
		w.gone = true
		return false
	}
	return true
}

func newE2EAnswerWriter(outer http.ResponseWriter, session *e2eSession, call []byte, binding *yardkey.FirstBinding, events bool) *e2eAnswerWriter {
	return &e2eAnswerWriter{outer: outer, session: session, call: call, binding: binding,
		sealer: session.keys.NewCallSealer(call, binding), events: events, header: http.Header{}}
}

func (w *e2eAnswerWriter) Header() http.Header { return w.header }

func (w *e2eAnswerWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *e2eAnswerWriter) Write(b []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	if w.err != nil || w.capped || w.gone {
		return len(b), nil
	}
	if !w.started && (w.overflow || len(b) > maxE2EAnswer-len(w.pending)) {
		w.overflow = true
		return len(b), nil
	}
	w.pending = append(w.pending, b...)
	if w.started && len(w.pending) >= yardkey.MaxRecordPlain {
		w.sealPending()
	}
	return len(b), nil
}

// Flush seals what the handler has written as the answer's next
// records, sending the outer head first when the stream begins: the
// inner head travels in the first record, sealed, and the outer head
// the link carries back shows the envelope's alone.
func (w *e2eAnswerWriter) Flush() {
	if w.err != nil || w.capped || w.overflow || w.gone {
		return
	}
	if !w.started {
		w.start()
	} else {
		w.sealPending()
	}
	if w.err == nil && !w.capped && !w.gone {
		_ = http.NewResponseController(w.outer).Flush()
	}
}

// SetWriteDeadline passes a stream's per-write deadline to the outer
// answer, as the events stream sets one on each event it writes.
func (w *e2eAnswerWriter) SetWriteDeadline(t time.Time) error {
	return http.NewResponseController(w.outer).SetWriteDeadline(t)
}

// Unwrap is the outer answer, for a response controller that looks
// past this writer for what it does not itself offer.
func (w *e2eAnswerWriter) Unwrap() http.ResponseWriter { return w.outer }

// start sends the outer head and seals the inner head with what body
// already stands behind it as the stream's first records.
func (w *e2eAnswerWriter) start() {
	if !w.streamLive() {
		return
	}
	raw, err := json.Marshal(e2eAnswerHead{Status: w.statusOrOK(), Header: w.header})
	if err != nil {
		w.err = err
		return
	}
	chunk := joinE2EHead(raw, w.pending)
	w.pending = nil
	w.started, w.wrote = true, true
	w.outer.Header().Set("Content-Type", "application/octet-stream")
	w.outer.Header().Set(e2eEnvelopeHeader, "sealed")
	w.outer.Header().Set(E2ECallHeader, base64.RawURLEncoding.EncodeToString(w.call))
	w.outer.WriteHeader(http.StatusOK)
	run, err := w.sealer.Seal(chunk)
	if err != nil {
		w.err = err
		return
	}
	w.sendRun(run)
}

// sealPending seals the body written since the last flush.
func (w *e2eAnswerWriter) sealPending() {
	if len(w.pending) == 0 {
		return
	}
	if !w.streamLive() {
		w.pending = nil
		return
	}
	run, err := w.sealer.Seal(w.pending)
	w.pending = nil
	if err != nil {
		w.err = err
		return
	}
	w.sendRun(run)
}

// sendRun writes one framed run of a streamed answer, holding the
// edge's ciphertext bound over a stream that is no events stream: a
// run that would cross it is never sent and the answer is left
// unclosed, which its receiver reports as truncated.
func (w *e2eAnswerWriter) sendRun(run []byte) {
	if len(run) == 0 {
		return
	}
	if !w.events {
		if w.sealed+len(run) > maxE2EAnswer {
			w.capped = true
			return
		}
		w.sealed += len(run)
	}
	if _, err := w.outer.Write(run); err != nil && w.err == nil {
		w.err = err
	}
}

func (w *e2eAnswerWriter) statusOrOK() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

// finish closes the answer when its handler has returned: sealed
// whole, under the bound checked on the ciphertext the edge carries
// (e2eCall's refusal, sealed), when it never flushed; and, when it
// streamed, sealed with whatever body is left and the final record.
func (w *e2eAnswerWriter) finish() error {
	if w.err != nil {
		return w.err
	}
	if w.gone {
		// Sealed nothing of the stream, and nothing stands to finish
		// it with: the outer head was never the yard's to send.
		if !w.wrote {
			return fmt.Errorf("a streamed answer's session is gone")
		}
		return nil // a stream cut by its removal: no final record
	}
	if !w.started {
		answer := e2eTooLargeAnswer()
		if !w.overflow {
			raw, err := json.Marshal(e2eAnswerHead{Status: w.statusOrOK(), Header: w.header})
			if err != nil {
				return err
			}
			answer = joinE2EHead(raw, w.pending)
		}
		sealed, err := w.session.keys.SealWhole(w.call, w.binding, answer)
		if err != nil {
			return err
		}
		if len(sealed) > maxE2EAnswer {
			// The edge's bound is on the ciphertext it carries, as
			// e2eCall has always refused it: inside the envelope.
			if sealed, err = w.session.keys.SealWhole(w.call, w.binding, e2eTooLargeAnswer()); err != nil {
				return err
			}
		}
		w.wrote = true
		w.outer.Header().Set("Content-Type", "application/octet-stream")
		w.outer.Header().Set(e2eEnvelopeHeader, "sealed")
		w.outer.Header().Set(E2ECallHeader, base64.RawURLEncoding.EncodeToString(w.call))
		w.outer.WriteHeader(http.StatusOK)
		_, err = w.outer.Write(sealed)
		return err
	}
	if w.capped {
		return nil // left unclosed on purpose, as sendRun says
	}
	w.sealPending()
	if w.err != nil {
		return w.err
	}
	if w.capped || w.gone {
		return nil // the tail was not sent, so its final cannot be sent either
	}
	if !w.streamLive() {
		return nil // the final is a new record too: none after a removal
	}
	final, err := w.sealer.SealFinal()
	if err != nil {
		return err
	}
	if !w.events {
		if w.sealed+len(final) > maxE2EAnswer {
			w.capped = true
			return nil // the final is part of the ciphertext bound too
		}
		w.sealed += len(final)
	}
	if _, err := w.outer.Write(final); err != nil {
		return err
	}
	_ = http.NewResponseController(w.outer).Flush()
	return nil
}
