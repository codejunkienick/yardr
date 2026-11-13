package web

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"yardr.dev/yardr/internal/store"
)

// DepotPage is one depot's page: its four counts, its flows with their
// stage strips, what waits on the viewer in this depot, and what landed
// in the last day.
type DepotPage struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Tag is the depot's own flow's repository commit, shortened
	// (repo@52e22d5); "" for a dir depot or one with no repo flow.
	Tag string `json:"tag,omitempty"`
	// ActiveFlows counts the depot's flows with something in transit, in
	// backlog or asked; QuietFlows the rest.
	ActiveFlows int `json:"active_flows"`
	QuietFlows  int `json:"quiet_flows"`
	// Waiting counts the depot's open beads carrying an open ask for the
	// viewer, Transit those past their flow's first stage with those off
	// their flow, Backlog those sitting in it, Landed those that landed
	// in the last 24 hours.
	Waiting int `json:"waiting"`
	Transit int `json:"transit"`
	Backlog int `json:"backlog"`
	Landed  int `json:"landed"`
	// Flows are the depot's flows that are not quiet, in the depot's
	// order; Quiet are the quiet ones' names, folded into one row.
	Flows []DepotFlowRow `json:"flows"`
	Quiet []string       `json:"quiet,omitempty"`
	// Waits are the depot's open beads carrying an open ask for the
	// viewer, the newest first; Landings the beads that landed in the
	// last 24 hours, the newest first, at most landedShown.
	Waits    []DepotWait    `json:"waits"`
	Landings []DepotLanding `json:"landings"`
	// Error is why the depot's flows could not be read; its beads are
	// then all counted in transit, as the overview counts them elsewhere.
	Error string `json:"error,omitempty"`
}

// Subhead is the title block's line under the depot's name: what it is,
// its flows, and its repository commit.
func (p DepotPage) Subhead() string {
	flows := "active flows"
	if p.ActiveFlows == 1 {
		flows = "active flow"
	}
	s := fmt.Sprintf("Depot · %d %s, %d quiet", p.ActiveFlows, flows, p.QuietFlows)
	if p.Tag != "" {
		s += " · " + p.Tag
	}
	return s
}

// QuietLabel is the folded quiet-flows row's words, the sidebar's.
func (p DepotPage) QuietLabel() string { return quietFlowsLabel(len(p.Quiet)) }

// DepotFlowRow is one of the depot's flows: its strip and its four
// counts, the depot's sliced by flow.
type DepotFlowRow struct {
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
	// Src is where the flow comes from: "<type> · " with its source for
	// a bead type's flow, the source alone for the depot's own.
	Src     string       `json:"src"`
	Stages  []DepotStage `json:"stages"`
	OnYou   int          `json:"on_you"`
	Transit int          `json:"transit"`
	Backlog int          `json:"backlog"`
	Landed  int          `json:"landed"`
}

// A strip segment's kind: ask holds the viewer's open asks, merged is
// the landed stage, first the flow's first stage, closed the dropped
// one, open any other non-empty stage, empty a stage with no bead.
const (
	depotStageAsk    = "ask"
	depotStageMerged = "merged"
	depotStageFirst  = "first"
	depotStageClosed = "closed"
	depotStageOpen   = "open"
	depotStageEmpty  = "empty"
)

// DepotStage is one stage of a flow's strip: its count and its kind.
type DepotStage struct {
	Stage string `json:"stage"`
	// Open counts the stage's open beads; merged also counts recent landings.
	Open int    `json:"open"`
	Kind string `json:"kind"`
}

// Class is the segment's class: the kind, with the first stage and
// closed sharing the grey.
func (s DepotStage) Class() string {
	switch s.Kind {
	case depotStageAsk:
		return "ask"
	case depotStageMerged:
		return "merged"
	case depotStageFirst, depotStageClosed:
		return "muted"
	case depotStageOpen:
		return "open"
	default:
		return "empty"
	}
}

// Title is the segment's title: the stage with its count, or that it is
// empty.
func (s DepotStage) Title() string {
	if s.Open == 0 {
		return s.Stage + " · empty"
	}
	return fmt.Sprintf("%s · %d", s.Stage, s.Open)
}

// stripKind is the strip's kind for a stage with open beads, asked
// telling whether it holds the viewer's open asks and first whether it
// is the flow's first stage. The ask orange wins over every other kind;
// merged and closed are the stages those names close a bead in.
func stripKind(stage string, open int, asked, first bool) string {
	switch {
	case open == 0:
		return depotStageEmpty
	case asked:
		return depotStageAsk
	case stage == "merged":
		return depotStageMerged
	case first:
		return depotStageFirst
	case stage == "closed":
		return depotStageClosed
	default:
		return depotStageOpen
	}
}

// DepotWait is one of the depot's beads carrying an open ask for the
// viewer.
type DepotWait struct {
	Bead  string `json:"bead"`
	Title string `json:"title"`
	Flow  string `json:"flow,omitempty"`
	Stage string `json:"stage"`
}

// ShortID is the bead's short id for the row.
func (w DepotWait) ShortID() string { return shortBead(w.Bead) }

// Where is where the bead waits: its flow and stage.
func (w DepotWait) Where() string {
	if w.Flow == "" {
		return w.Stage
	}
	return w.Flow + " · " + w.Stage
}

// DepotLanding is one bead that landed in the last 24 hours.
type DepotLanding struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Age is how long ago it landed, in the feed's words.
	Age      string    `json:"age"`
	ClosedAt time.Time `json:"closed_at"`
}

// ShortID is the bead's short id for the row.
func (l DepotLanding) ShortID() string { return shortBead(l.ID) }

// shortBead is the bead's short id: what stands after the last dash, the
// whole id when there is none.
func shortBead(id string) string {
	if i := strings.LastIndex(id, "-"); i >= 0 {
		return id[i+1:]
	}
	return id
}

// landedShown is how many landed beads the depot page names.
const landedShown = 5

// landedWithin is the landed window: the last rolling 24 hours.
const landedWithin = 24 * time.Hour

// hasStage reports whether stage is among counts, without counting it.
func hasStage(counts []StageCount, stage string) bool {
	return slices.ContainsFunc(counts, func(c StageCount) bool { return c.Stage == stage })
}

// landedSince are the beads of all that landed in the depot in the day
// before now, the newest first: closed at their flow's landing terminal,
// merged, done or live, whichever the flow ends in. A bead no flow governs
// keeps the old rule, Stage merged, so a depot whose flows cannot be read
// still counts what landed. A bead put aside stands in dropped instead,
// and an older landing outside the window.
func landedSince(all []store.Bead, d *HomeDepot, now time.Time) []store.Bead {
	since := now.Add(-landedWithin)
	var out []store.Bead
	for i := range all {
		b := &all[i]
		if b.Status != "closed" || b.ClosedAt == nil || !in(b, d.Name) {
			continue
		}
		want := "merged"
		if j := d.flowIndex(b.Type); j >= 0 {
			want = landingTerminal(d.Flows[j].Terminal)
		}
		if b.Stage != want || b.ClosedAt.Before(since) {
			continue
		}
		out = append(out, *b)
	}
	slices.SortStableFunc(out, func(a, b store.Bead) int {
		if c := b.ClosedAt.Compare(*a.ClosedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

// depotPage regroups the overview, the line and the closed read into one
// depot's page. d is the depot's overview row; open its yard's open
// beads, all what ListBeads{All: true} read, both viewer-filtered.
func depotPage(d *HomeDepot, open []store.Bead, l *Line, all []store.Bead, now time.Time) *DepotPage {
	out := &DepotPage{Name: d.Name, Kind: d.Kind, Flows: []DepotFlowRow{}, Waits: []DepotWait{},
		Landings: []DepotLanding{}, Error: d.Error}
	byID := make(map[string]*store.Bead, len(open))
	for i := range open {
		byID[open[i].ID] = &open[i]
	}
	// The depot's open beads, per flow: backlog where a bead sits in its
	// flow's first stage, transit everywhere else. A bead no flow
	// governs counts in the depot's transit and under no flow, as under
	// the sidebar, whose per-depot counts these equal.
	inTransit, inBacklog := make([]int, len(d.Flows)), make([]int, len(d.Flows))
	terminalOpen := make([]map[string]int, len(d.Flows))
	for i := range open {
		b := &open[i]
		if !in(b, d.Name) {
			continue
		}
		j := d.flowIndex(b.Type)
		if j < 0 {
			out.Transit++
			continue
		}
		if first := d.Flows[j].Stages; len(first) > 0 && first[0].Stage == b.Stage {
			out.Backlog++
			inBacklog[j]++
		} else {
			out.Transit++
			inTransit[j]++
		}
		// A bead reopened into a terminal stage counts as in transit,
		// as under the sidebar, and stands on its strip's segment.
		if !hasStage(d.Flows[j].Stages, b.Stage) && slices.Contains(d.Flows[j].Terminal, b.Stage) {
			if terminalOpen[j] == nil {
				terminalOpen[j] = map[string]int{}
			}
			terminalOpen[j][b.Stage]++
		}
	}
	// What waits on the viewer in this depot: the rail pill's predicate
	// restricted to it, the newest first. A bead with no depot stands
	// under no row, as in the sidebar.
	var waits []Waiting
	for _, ws := range [][]Waiting{l.Waits, l.Parked} {
		for _, w := range ws {
			if w.HasAsk() && w.Depot == d.Name {
				waits = append(waits, w)
			}
		}
	}
	slices.SortStableFunc(waits, func(a, b Waiting) int { return b.Since.Compare(a.Since) })
	out.Waiting = len(waits)
	onYou := make([]int, len(d.Flows))
	askedStage := make([]map[string]bool, len(d.Flows))
	for _, w := range waits {
		flow := ""
		// The asked bead's flow by the overview's rule, joined in
		// memory through the overview, as the sidebar joins it.
		if b := byID[w.Bead]; b != nil {
			if j := d.flowIndex(b.Type); j >= 0 {
				flow = d.Flows[j].Name
				onYou[j]++
				if askedStage[j] == nil {
					askedStage[j] = map[string]bool{}
				}
				askedStage[j][w.Stage] = true
			}
		}
		out.Waits = append(out.Waits, DepotWait{Bead: w.Bead, Title: w.Title, Flow: flow, Stage: w.Stage})
	}
	// What landed in the last day, the newest first.
	landed := landedSince(all, d, now)
	out.Landed = len(landed)
	landedPerFlow := make([]int, len(d.Flows))
	for i := range landed {
		if j := d.flowIndex(landed[i].Type); j >= 0 {
			landedPerFlow[j]++
		}
	}
	for _, b := range landed[:min(len(landed), landedShown)] {
		out.Landings = append(out.Landings, DepotLanding{ID: b.ID, Title: b.Title,
			Age: age(now.Sub(*b.ClosedAt)), ClosedAt: *b.ClosedAt})
	}
	for j := range d.Flows {
		f := &d.Flows[j]
		row := DepotFlowRow{Name: f.Name, Type: f.Type, Src: f.Src, Stages: []DepotStage{},
			OnYou: onYou[j], Transit: inTransit[j], Backlog: inBacklog[j], Landed: landedPerFlow[j]}
		if f.Type != "" {
			row.Src = f.Type + " · " + f.Src
		}
		if inTransit[j] == 0 && inBacklog[j] == 0 && onYou[j] == 0 {
			// Quiet, as under the sidebar: nothing in transit, no
			// backlog, no open ask. Folded into one row.
			out.Quiet = append(out.Quiet, f.Name)
			continue
		}
		first := ""
		if len(f.Stages) > 0 {
			first = f.Stages[0].Stage
		}
		for _, sc := range f.Stages {
			count := sc.Open
			if sc.Stage == "merged" {
				count += landedPerFlow[j]
			}
			row.Stages = append(row.Stages, DepotStage{Stage: sc.Stage, Open: count,
				Kind: stripKind(sc.Stage, count, askedStage[j][sc.Stage], sc.Stage == first)})
		}
		for _, t := range f.Terminal {
			n := terminalOpen[j][t]
			if t == "merged" {
				n += landedPerFlow[j]
			}
			row.Stages = append(row.Stages, DepotStage{Stage: t, Open: n,
				Kind: stripKind(t, n, askedStage[j][t], false)})
		}
		out.Flows = append(out.Flows, row)
	}
	out.ActiveFlows = len(out.Flows)
	out.QuietFlows = len(out.Quiet)
	out.Tag = depotTag(d)
	return out
}

// depotTag is the title block's tag: the depot's own flow's commit. A dir
// depot's source reads "repo (directory)", and a store flow names no
// commit, so neither carries one. The depot's page and its flows' lists
// carry the same tag.
func depotTag(d *HomeDepot) string {
	for j := range d.Flows {
		if d.Flows[j].Type == "" {
			if src := d.Flows[j].Src; strings.HasPrefix(src, "repo@") {
				return src
			}
			break
		}
	}
	return ""
}

func (s *Server) handleDepot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := r.PathValue("name")
	// The head first, as on a page: the shell's stream after it brings
	// whatever changed while the depot was read.
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
	l, err := s.line(ctx, ov)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// What landed in the last day: every closed bead, as /beads?all=1
	// lists them, filtered in memory; no new store call.
	all, err := s.st.ListBeads(ctx, store.BeadFilter{All: true, Viewer: s.acting(ctx)})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.respond(w, r, Page{Template: "depot", Title: name, Menu: "depot",
		Data: depotPage(d, ov.yard.beads, l, all, time.Now()), line: l, ov: ov, head: head, depot: name})
}
