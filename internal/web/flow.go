package web

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"yardr.dev/yardr/internal/store"
)

// FlowPage is one flow's list: its open beads grouped by stage in flow
// order, the empty stages folded into one row, and the flow's landing
// terminal as the last day's log above every other closed bead.
type FlowPage struct {
	Depot string `json:"depot"`
	Flow  string `json:"flow"`
	// Tag is the depot's own flow's repository commit, shortened
	// (repo@52e22d5); "" for a dir depot or one with no repo flow, as on
	// the depot's page.
	Tag string `json:"tag,omitempty"`
	// Groups are the flow's open stages that hold a bead, in flow order;
	// Empty the ones that hold none, folded into one row.
	Groups []FlowGroup `json:"groups"`
	Empty  []FlowGroup `json:"empty,omitempty"`
	// Landing is the flow's landing terminal with what landed in the last
	// 24 hours, nil when the flow ends in no landing terminal.
	Landing *FlowTerminal `json:"landing,omitempty"`
	// Closed is every other closed bead of the flow.
	Closed FlowTerminal `json:"closed"`
}

// EmptyLabel is the folded empty-stages row's words.
func (p FlowPage) EmptyLabel() string {
	if len(p.Empty) == 1 {
		return "1 empty stage"
	}
	return fmt.Sprintf("%d empty stages", len(p.Empty))
}

// EmptyNames is the folded row's stage names, in flow order.
func (p FlowPage) EmptyNames() string {
	names := make([]string, 0, len(p.Empty))
	for _, g := range p.Empty {
		names = append(names, g.Stage)
	}
	return strings.Join(names, " · ")
}

// FlowGroup is one open stage's group: its beads, the newest first.
type FlowGroup struct {
	Stage string `json:"stage"`
	Count int    `json:"count"`
	// Next is the following stage in the flow's order including the
	// terminal: the last open stage names the flow's actual terminal.
	Next string `json:"next"`
	// Note is what the header carries past the count: "waits on shaping"
	// on the flow's first stage when it is named backlog.
	Note string    `json:"note,omitempty"`
	Rows []FlowRow `json:"rows"`
	// More hides behind "Show N more", 0 when the group fits.
	More     int    `json:"more,omitempty"`
	MoreLink string `json:"more_link,omitempty"`
}

// FlowRow is one open bead's row: its title, linking to its bead page.
type FlowRow struct {
	Bead  string `json:"bead"`
	Title string `json:"title"`
}

// FlowTerminal is a terminal group: the landing terminal's log, or every
// other closed bead. Its rows are plain, as drawn.
type FlowTerminal struct {
	Stage string       `json:"stage"`
	Count int          `json:"count"`
	Rows  []FlowClosed `json:"rows"`
	// More hides behind "Show N more", 0 when the group fits.
	More     int    `json:"more,omitempty"`
	MoreLink string `json:"more_link,omitempty"`
}

// FlowClosed is one closed bead's row.
type FlowClosed struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Verb is how the row reads its close: "landed" in the landing log,
	// its real close stage under closed ("landed" for merged and live,
	// "done" for done, "closed" anywhere else).
	Verb string `json:"verb"`
	// Age is how long ago it closed, in the feed's words.
	Age      string    `json:"age"`
	ClosedAt time.Time `json:"closed_at"`
}

// ShortID is the bead's short id for the row.
func (r FlowClosed) ShortID() string { return shortBead(r.ID) }

// Words is the row's right side: how it closed, and how long ago.
func (r FlowClosed) Words() string { return r.Verb + " " + r.Age }

// flowRowsShown is how many rows a group names before "Show N more"
// carries the rest to the filtered bead list.
const flowRowsShown = 6

// landingTerminal is the flow's landing terminal: the first of merged,
// done or live among its terminals in flow order, "" when it ends in
// none of them. Not every flow ends in merged: the dir pack's ends in
// done and the website pack's in live.
func landingTerminal(terminal []string) string {
	for _, t := range terminal {
		if t == "merged" || t == "done" || t == "live" {
			return t
		}
	}
	return ""
}

// closeVerb is how a row under closed reads its real close stage.
func closeVerb(stage string) string {
	switch stage {
	case "merged", "live":
		return "landed"
	case "done":
		return "done"
	default:
		return "closed"
	}
}

// flowPage regroups the overview and the closed read into one flow's list.
// d is the depot's overview row, j its flow's index, open the yard's open
// beads and all what ListBeads{All: true} read, both viewer-filtered.
func flowPage(d *HomeDepot, j int, open []store.Bead, all []store.Bead, now time.Time) *FlowPage {
	f := &d.Flows[j]
	out := &FlowPage{Depot: d.Name, Flow: f.Name, Tag: depotTag(d),
		Groups: []FlowGroup{}, Empty: []FlowGroup{},
		Closed: FlowTerminal{Stage: "closed", Rows: []FlowClosed{}}}
	moreLink := func(stage string, closed bool) string {
		q := url.Values{}
		if closed {
			q.Set("all", "1")
		}
		q.Set("depot", d.Name)
		q.Set("stage", stage)
		return "/beads?" + q.Encode()
	}
	// The flow's stage order with its terminals last, as the depot's
	// strip draws it: next is the following stage, the last open stage's
	// the flow's actual terminal.
	order := make([]string, 0, len(f.Stages)+len(f.Terminal))
	for _, sc := range f.Stages {
		order = append(order, sc.Stage)
	}
	order = append(order, f.Terminal...)
	next := map[string]string{}
	for i, st := range order {
		if i+1 < len(order) {
			next[st] = order[i+1]
		}
	}
	byStage := map[string][]store.Bead{}
	for i := range open {
		b := &open[i]
		if !in(b, d.Name) || d.flowIndex(b.Type) != j {
			continue
		}
		byStage[b.Stage] = append(byStage[b.Stage], *b)
	}
	for i, sc := range f.Stages {
		beads := byStage[sc.Stage]
		// The newest first, by Since as the line orders them.
		slices.SortStableFunc(beads, func(a, b store.Bead) int {
			if c := b.UpdatedAt.Compare(a.UpdatedAt); c != 0 {
				return c
			}
			return strings.Compare(a.ID, b.ID)
		})
		g := FlowGroup{Stage: sc.Stage, Count: len(beads), Next: next[sc.Stage], Rows: []FlowRow{}}
		if i == 0 && sc.Stage == "backlog" {
			g.Note = "waits on shaping"
		}
		for _, b := range beads[:min(len(beads), flowRowsShown)] {
			g.Rows = append(g.Rows, FlowRow{Bead: b.ID, Title: b.Title})
		}
		if len(beads) > flowRowsShown {
			g.More = len(beads) - flowRowsShown
			g.MoreLink = moreLink(sc.Stage, false)
		}
		if len(beads) == 0 {
			out.Empty = append(out.Empty, g)
		} else {
			out.Groups = append(out.Groups, g)
		}
	}
	// The landing log and every other closed bead, the newest first: the
	// depot's landed read sliced to this flow, so the two pages never
	// disagree on what landed.
	landedIDs := map[string]bool{}
	var landed, closed []store.Bead
	for _, b := range landedSince(all, d, now) {
		if d.flowIndex(b.Type) == j {
			landedIDs[b.ID] = true
			landed = append(landed, b)
		}
	}
	for i := range all {
		b := &all[i]
		if b.Status != "closed" || b.ClosedAt == nil || !in(b, d.Name) || d.flowIndex(b.Type) != j {
			continue
		}
		if !landedIDs[b.ID] {
			closed = append(closed, *b)
		}
	}
	slices.SortStableFunc(closed, func(a, b store.Bead) int {
		if c := b.ClosedAt.Compare(*a.ClosedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	if landing := landingTerminal(f.Terminal); landing != "" {
		t := FlowTerminal{Stage: landing, Count: len(landed), Rows: []FlowClosed{}}
		for _, b := range landed[:min(len(landed), flowRowsShown)] {
			t.Rows = append(t.Rows, FlowClosed{ID: b.ID, Title: b.Title,
				Verb: "landed", Age: age(now.Sub(*b.ClosedAt)), ClosedAt: *b.ClosedAt})
		}
		if len(landed) > flowRowsShown {
			t.More = len(landed) - flowRowsShown
			t.MoreLink = moreLink(landing, true)
		}
		out.Landing = &t
	}
	out.Closed.Count = len(closed)
	for _, b := range closed[:min(len(closed), flowRowsShown)] {
		out.Closed.Rows = append(out.Closed.Rows, FlowClosed{ID: b.ID, Title: b.Title,
			Verb: closeVerb(b.Stage), Age: age(now.Sub(*b.ClosedAt)), ClosedAt: *b.ClosedAt})
	}
	if len(closed) > flowRowsShown {
		out.Closed.More = len(closed) - flowRowsShown
		out.Closed.MoreLink = moreLink("closed", true)
	}
	return out
}

func (s *Server) handleFlow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name, flow := r.PathValue("name"), r.PathValue("flow")
	// The head first, as on a page: the shell's stream after it brings
	// whatever changed while the flow was read.
	head, err := s.st.Head(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ov, err := s.overview(ctx, nil)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var d *HomeDepot
	for i := range ov.Depots {
		if ov.Depots[i].Name == name {
			d = &ov.Depots[i]
			break
		}
	}
	if d == nil {
		s.fail(w, r, fmt.Errorf("depot %s: %w", name, store.ErrNotFound))
		return
	}
	j := -1
	for i := range d.Flows {
		if d.Flows[i].Name == flow {
			j = i
			break
		}
	}
	if j < 0 {
		s.fail(w, r, fmt.Errorf("flow %s: %w", flow, store.ErrNotFound))
		return
	}
	// The landing log and the closed beads: every closed bead, as
	// /beads?all=1 lists them, filtered in memory; no new store call.
	all, err := s.st.ListBeads(ctx, store.BeadFilter{All: true, Viewer: s.acting(ctx)})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.respond(w, r, Page{Template: "flow", Title: flow, Menu: "depot",
		Data: flowPage(d, j, ov.yard.beads, all, time.Now()), ov: ov, head: head, depot: name, flow: flow})
}
