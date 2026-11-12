package web

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"rsc.io/qr"

	"yardr.dev/yardr/internal/store"
)

// The answer to a pairing approval in the hosted web (design
// yardr-6maq 3.4): the approval's own response, in its sitting alone,
// holding the transient the approving call answered — the QR code drawn
// from it, the code beside it and its minutes counted. It is no standing
// pairing page: it is served by no route a visitor can open first, the
// code stands in no URL, form or redirect target, the response is
// marked never to be stored (respond's Cache-Control: no-store), and
// nothing of it is read from the bead on any later visit, where the bead
// shows only its stored safe sentence. The withdrawn pairing page
// (pairing.go) stands untouched beside it.

// pairingApprovedPage is what the pairing approval's response shows.
type pairingApprovedPage struct {
	Bead    string `json:"bead"`
	User    string `json:"user"`
	Device  string `json:"device"`
	Code    string `json:"code"`
	Address string `json:"address"`
	Yard    string `json:"yard,omitempty"`
	Expires string `json:"expires"`
	Minutes int    `json:"minutes"`
	QR      string `json:"qr"`
	// SVG is the QR code of QR, drawn here from the transient.
	SVG template.HTML `json:"-"`
}

// pairingApproved answers the approval that opened the pairing p on
// bead with the pairing page, in place of the redirect to the bead.
func (s *Server) pairingApproved(w http.ResponseWriter, r *http.Request, bead string, p *store.AskPairing) {
	d := pairingApprovedPage{Bead: bead, User: p.User, Device: p.Device, Code: p.Code, Address: p.Address,
		Yard: p.Yard, Expires: p.Expires.Local().Format("15:04:05"), QR: p.QR,
		Minutes: int(time.Until(p.Expires).Minutes())}
	if svg, err := pairingQR(p.QR); err == nil {
		d.SVG = svg
	}
	s.respond(w, r, Page{Template: "pairingapproved", Title: "Pair a device", Data: d})
}

// pairingQR draws text as a QR code in SVG: a white ground, the quiet
// zone a reader wants around it, and one path of its dark modules.
func pairingQR(text string) (template.HTML, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", err
	}
	const quiet = 4
	size := code.Size + 2*quiet
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="256" height="256" role="img" aria-label="QR code of the pairing"><rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, size, size, size, size)
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; x++ {
			if code.Black(x, y) {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+quiet, y+quiet)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return template.HTML(b.String()), nil
}
