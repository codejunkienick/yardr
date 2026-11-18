package rpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/yardkey"
)

// The edge's door to enrol a connection at the yard (design yardr-ts4d,
// sections 3.1 and 3.4): one named connection per call, enrolled as a
// device through the AddDevice minting path, its token answered once for
// the edge to hold as its backend credential. It sits beside the pair
// path, which also takes no credential, and likewise is answered before
// anything else (uncredentialed), so a yard with no device yet enrols
// its first connection here. What proves the sender is the edge's
// signature over the request (edgeSigned), not the route it came by.

// EdgeEnrolMethod is the method the edge enrols a connection by. It is
// in neither Calls nor OperatorCalls, so a device is answered as for a
// method the yard does not have, as a socket-only call is; and it is no
// method of the store's API, so the socket answers it so too. Only the
// edge's own request, without a credential, from the edge's marks and
// signed with the edge's key, is served, by enrolEdge.
const EdgeEnrolMethod = "EnrolEdgeDevice"

// EdgeEnrolPath is where the edge calls it: the call path, as a device's
// call would name it, so both refusals read as the same not-there.
const EdgeEnrolPath = "/user/call/" + EdgeEnrolMethod

// maxEdgeEnrol bounds the body of an enrolment: an account email and a
// connection name, as a pairing's code is bounded.
const maxEdgeEnrol = 4 << 10

// uncredentialed serves the paths that take no credential, and reports
// whether it served: an enveloped call down the link, a pairing's
// redemption while one is open, and the edge's enrolment of a
// connection. Anything it does not serve is as any other path here:
// with no device in the yard it is not there, and without a credential
// that resolves it is refused.
func (u *UserServer) uncredentialed(w http.ResponseWriter, r *http.Request) bool {
	// An enveloped call down the link is opened by its channel, not
	// by a token, and only from the link's address (usere2e.go).
	if r.Method == http.MethodPost && r.URL.Path == E2EPath && r.RemoteAddr == linkRemoteAddr {
		u.e2e(w, r)
		return true
	}
	// A device that has no token yet comes for one with a pairing's
	// code. Asked first: the device a pairing is opened for may be the
	// yard's first. With no pairing open the path is as any other here.
	if r.Method == http.MethodPost && r.URL.Path == PairPath && u.st.DevicePairOpened(r.Context()) {
		u.pair(w, r)
		return true
	}
	// The edge enrols a connection for its signed-in account, with no
	// credential of its own: the link and router marks say the request
	// came by the edge's route, and enrolEdge then asks for the edge's
	// signature, which is what proves the sender. A request that carries
	// a credential is a device's, down the link or direct, and falls
	// through to be refused as one, as for a method the yard does not
	// have.
	if r.Method == http.MethodPost && r.URL.Path == EdgeEnrolPath && u.fromEdge(r) && len(r.Header.Values("Authorization")) == 0 {
		u.enrolEdge(w, r)
		return true
	}
	return false
}

// fromEdge reports whether r came by the edge's route: down the yard's
// standing link, or from the yard's router peer saying it came over
// https. The marks say the route and no more — the edge carries every
// signed-in session's /user/ call down that same route — so they open
// nothing alone: enrolEdge serves only what the edge signed.
func (u *UserServer) fromEdge(r *http.Request) bool {
	if r.RemoteAddr == linkRemoteAddr {
		return true
	}
	return fromRouter(r, u.routerAddr(r.Context()))
}

// enrolEdge enrols one named connection for the signed-in account email
// the edge sends, as {"email": ..., "device": ...}, and answers the
// yard-minted token once, as a call's result. Only what the edge signed
// is served (edgeSigned): a carried session's call on the same route,
// signed by nobody or by another key, enrols nothing. A second enrolment
// for another user completes beside it: no pairing stands behind the
// call, so there is no one-at-a-time slot and nothing a restart loses.
// The answer holds the token, so it is kept out of caches as a pairing's
// is; a refusal after a signature passed is answered as any call's error
// is, with nothing of the token in it, since none was made.
func (u *UserServer) enrolEdge(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxEdgeEnrol))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	if !u.edgeSigned(w, r, body) {
		return
	}
	var asked struct {
		Email  string `json:"email"`
		Device string `json:"device"`
	}
	if err := json.Unmarshal(body, &asked); err != nil {
		writeJSON(w, reply{Error: toWire(fmt.Errorf("arguments: %w", err))})
		return
	}
	d, err := u.st.EnrolEdgeDevice(r.Context(), asked.Email, asked.Device)
	if err != nil {
		writeJSON(w, reply{Error: toWire(err)})
		return
	}
	u.log.Info("a connection was enrolled by the edge", "user", d.User, "device", d.Name)
	// The one answer that holds the token: nothing between keeps it.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, reply{Result: d})
}

// edgeSigned reports whether the enrolment is the edge's own: signed with
// the edge's key this yard pinned, for this yard's key, over the request
// as made — its method, the bare path, the time and the body. The pinned
// key is the edge setting's, the one yardr edge connect pinned and the
// link checks its challenge against; the yard's is read from its identity
// file for each enrolment, never held, as a voucher's is. A yard that holds
// no pinned key enrols nothing, as does a request whose signature is
// missing, stale or wrong, or made for another yard's key: every failure
// answers the same 401 with nothing of why, and the reason is the
// receiver's log's alone. There is no replay store: a signature is good
// for yardkey.MaxSkew, and a replay inside it is still the same bytes for
// the same device, so it dies as the duplicate-name refusal in putDevice.
func (u *UserServer) edgeSigned(w http.ResponseWriter, r *http.Request, body []byte) bool {
	refuse := func(err error) bool {
		u.log.Warn("an edge enrolment was refused", "err", err)
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return false
	}
	all, err := u.st.Settings(r.Context())
	if err != nil {
		return refuse(err)
	}
	edge, connected, err := store.EdgeOf(all)
	if err != nil || !connected {
		if err == nil {
			err = fmt.Errorf("the %s setting names no edge", store.EdgeSetting)
		}
		return refuse(err)
	}
	if u.identityPath == "" {
		return refuse(fmt.Errorf("this yard was told no identity key file"))
	}
	identity, err := store.Load(u.identityPath)
	if err != nil {
		return refuse(err)
	}
	if r.Header.Get(yardkey.HeaderKey) != edge.Key {
		return refuse(fmt.Errorf("%w: not the key this yard pinned for its edge", store.ErrSignature))
	}
	if err := yardkey.VerifyRequest(edge.Key, store.Public(identity), r.Method, EdgeEnrolPath, r.Header.Get(yardkey.HeaderTime), r.Header.Get(yardkey.HeaderSig), body, u.now()); err != nil {
		return refuse(err)
	}
	return true
}
