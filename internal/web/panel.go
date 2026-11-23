package web

import (
	"net/http"
)

// panel reports whether the request asks for the yard panel (?panel=1),
// the flows as a list, after ?all=1 on /beads.
func panel(r *http.Request) bool {
	switch r.URL.Query().Get("panel") {
	case "", "0", "false":
		return false
	}
	return true
}

// PanelDepot is one depot as the panel shows it: each flow that governs it,
// and the open beads in no stage of them for the off-the-line row. It is
// HTML-only: the JSON of /flows is Flows either way.
type PanelDepot struct {
	Name  string
	Kind  string
	Flows []PanelFlow
	// Elsewhere are the depot's open beads in no stage of the flows above,
	// the newest change first, as on /flows.
	Elsewhere []Card
	Error     string
}

// PanelFlow is one flow of a depot as the panel lists it.
type PanelFlow struct {
	Name   string
	Type   string
	Source string
}

// Panel is the yard panel: every depot's flows as a list. The template
// calls it; the JSON never carries it.
func (d *Flows) Panel() []PanelDepot {
	out := make([]PanelDepot, 0, len(d.Depots))
	for _, depot := range d.Depots {
		pd := PanelDepot{Name: depot.Name, Kind: depot.Kind, Flows: []PanelFlow{},
			Elsewhere: depot.Elsewhere, Error: depot.Error}
		for _, col := range depot.Flows {
			pd.Flows = append(pd.Flows, PanelFlow{Name: col.Name, Type: col.Type, Source: col.Source})
		}
		out = append(out, pd)
	}
	return out
}
