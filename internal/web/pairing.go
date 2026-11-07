package web

import (
	"net/http"
)

// The pairing page once enrolled one of the signed-in user's own
// devices here, with a code and a QR the page showed. It does so no
// longer for an app that calls through the edge: this page reaches its
// visitor through the edge, so the edge can read or rewrite any code
// or QR it showed, and a device paired from it would pin a key the
// edge chose. What the page does now is say so, and say where a
// pairing that does seal is opened: the yard's own screen, by its
// operator (`yardr user device pair --qr` there), whose QR carries
// the yard's channel key from a place the edge cannot rewrite
// (docs/users.md, "Enrolled by a code"; the design's sections 3.1 and
// 3.4 — that loss of a self-service page is accepted for now, and a
// member ceremony whose secret never crosses the edge is a later
// design). It exists in JWT mode alone: the signed-in session names
// the user it speaks to. The store's Self pairings stand as they were
// for any other caller; this page opens none.

// pairingNotice is what the page tells every signed-in visitor, in
// the words the change is described in wherever it is described.
const pairingNotice = "Pairing from this page no longer gives an app that can call through the edge. " +
	"Ask the yard's operator to open a pairing QR on the yard's own screen and scan that."

// Pairing is the pairing page: who it speaks to, and why a POST of
// its former forms changed nothing, where one is sent.
type Pairing struct {
	// As is the yard user the visitor is signed in as.
	As string `json:"as"`
	// Notice is what the page says: pairing here no longer gives an
	// app that calls through the edge, and where such a pairing is
	// opened instead.
	Notice string `json:"notice"`
	// Error is why a POST opened or ended nothing, where one was
	// sent to the page's former forms.
	Error string `json:"error,omitempty"`
}

// pairingPage is the pairing page as it stands for user.
func pairingPage(user string) Pairing {
	return Pairing{As: user, Notice: pairingNotice}
}

// refusePairing answers a pairing request on a view that is not in JWT
// mode: the page is the signed-in visitor's, and no other view has one.
func (s *Server) refusePairing(w http.ResponseWriter, r *http.Request) {
	s.respond(w, r, Page{Status: http.StatusForbidden, Template: "error", Title: "Forbidden",
		Data: errorData{Error: "device pairing is on the signed-in view only: this view has no signed-in user to pair a device for"}})
}

func (s *Server) handlePairing(w http.ResponseWriter, r *http.Request) {
	if s.jwt == nil {
		s.refusePairing(w, r)
		return
	}
	s.respond(w, r, Page{Template: "pairing", Title: "Pair a device", Menu: "pairing", Data: pairingPage(s.acting(r.Context()))})
}

// handlePairingPost took the pairing page's forms. The page has none
// now, and a POST opens and ends nothing: it is answered with the
// page and the reason, as 200 so a form puts it in place. It refuses
// every view but JWT mode's before anything is read, as it did, and
// takes a POST only from the view's own pages.
func (s *Server) handlePairingPost(w http.ResponseWriter, r *http.Request) {
	if s.jwt == nil {
		s.refusePairing(w, r)
		return
	}
	if origin := r.Header.Get("Origin"); !ownOrigin(origin, r.Host) {
		http.Error(w, "yardr web takes a pairing only from its own pages: the request's origin is "+quoted(origin)+", not this view's. A page on another site cannot pair a device as this view's user",
			http.StatusForbidden)
		return
	}
	d := pairingPage(s.acting(r.Context()))
	d.Error = "no pairing was opened or ended: " + pairingNotice
	s.respond(w, r, Page{Template: "pairing", Title: "Pair a device", Menu: "pairing", Data: d})
}

// handlePairingPNG served the QR code of the visitor's own pairing.
// There is no QR here now, for the page's reason: a QR this page
// served would cross the edge that must not be able to rewrite it.
// It answers 404, as a page with nothing at it does, with that reason
// in its body.
func (s *Server) handlePairingPNG(w http.ResponseWriter, r *http.Request) {
	if s.jwt == nil {
		http.Error(w, "device pairing is on the signed-in view only", http.StatusForbidden)
		return
	}
	http.Error(w, "no pairing QR is served here: "+pairingNotice, http.StatusNotFound)
}
