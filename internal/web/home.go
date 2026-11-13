package web

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"yardr.dev/yardr/internal/store"
)

// Home is the yard's home page: the overview (what waits for a person, what
// runs now, the depots, what closed today) above the feed of what happened.
type Home struct {
	Yard string `json:"yard"`
	// As is the user the view acts as, "" when it does not act: an asked
	// need then answers with buttons.
	As     string      `json:"as,omitempty"`
	Depots []HomeDepot `json:"depots"`
	// Attention are the open beads that wait for a person: those something
	// stops (held, blocked on a prompt, stranded) and those parked in a
	// stage of a manual group past their flow's first. A bead parked where
	// it starts (a backlog) waits on no one in particular and is on /line
	// only.
	Attention []Waiting `json:"attention"`
	// Running are the sessions running now, the oldest first, as on
	// /sessions but without the claims of manual groups, which run nothing.
	Running []SessionRow `json:"running"`
	// Closed are the beads that closed today, the newest first, at most ten.
	Closed []HomeClosed `json:"closed"`
	Feed
	// All says the feed shows every entry (?all=1), not the kept lines.
	// HTML-only: the JSON names every entry either way.
	All bool `json:"-"`
	// Kept are the feed's entries that stand on home, newest first: set
	// unless All. HTML-only, as All is.
	Kept []Entry `json:"-"`
	// yard is what the counts were made from, for the line, which counts
	// the same beads and sessions.
	yard *yard
}

// HomeClosed is one bead that closed today.
type HomeClosed struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// ClosedAt is when it closed, today.
	ClosedAt time.Time `json:"closed_at"`
}

// yard is the yard's open work as one request reads it, once: what the page
// lists and what the shell's line counts are then the same beads, and no
// bead is listed for each of them again.
type yard struct {
	depots []store.Depot
	// beads are the open beads, of every depot and of none.
	beads []store.Bead
	// sessions are the live ones.
	sessions []store.Session
}

func (s *Server) readYard(ctx context.Context) (*yard, error) {
	var y yard
	var err error
	if y.depots, err = s.st.Depots(ctx); err != nil {
		return nil, err
	}
	if y.sessions, err = s.st.Sessions(ctx, false); err != nil {
		return nil, err
	}
	if y.beads, err = s.st.ListBeads(ctx, store.BeadFilter{Viewer: s.acting(ctx)}); err != nil {
		return nil, err
	}
	return &y, nil
}

// in reports whether b is a bead of depot.
func in(b *store.Bead, depot string) bool { return b.Depot != nil && *b.Depot == depot }

// depotFlows reads the flows that govern each of depots, all at once: each
// read starts git to ask where the depot's base stands, and none of them
// has to wait for another's.
func (s *Server) depotFlows(ctx context.Context, depots []store.Depot) ([][]store.DepotFlow, []error) {
	flows, errs := make([][]store.DepotFlow, len(depots)), make([]error, len(depots))
	var wg sync.WaitGroup
	for i, depot := range depots {
		wg.Go(func() { flows[i], errs[i] = s.st.DepotFlows(ctx, depot.Name) })
	}
	wg.Wait()
	return flows, errs
}

// HomeDepot is one depot's open beads, counted per stage of the flows that
// govern the depot.
type HomeDepot struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Open, Working and Held count the depot's open beads: all of them,
	// those an agent's or a command's session has claimed, and those held.
	// A manual group's claim is a person's turn and no work in progress.
	Open    int        `json:"open"`
	Working int        `json:"working"`
	Held    int        `json:"held"`
	Flows   []HomeFlow `json:"flows"`
	// Elsewhere counts the open beads in a stage their depot's flow lacks: a
	// bead pinned to another flow, or one a changed flow left behind.
	Elsewhere []StageCount `json:"elsewhere,omitempty"`
	// Error is why the depot's flows could not be read (a checkout git cannot
	// read); its beads are then all counted in Elsewhere.
	Error string `json:"error,omitempty"`
}

// HomeFlow is one flow of a depot: the depot's own (Type "") or a bead type's.
type HomeFlow struct {
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
	// Src is where the flow comes from, as `yardr flow show` says it:
	// "store", "repo@0f415ee".
	Src string `json:"src"`
	// Stages are the flow's stages an open bead can sit in, in the flow's
	// order: a terminal stage closes the bead that enters it.
	Stages []StageCount `json:"stages"`
	// Terminal are the flow's terminal stages, in the flow's order: the
	// stages Stages leaves out, which the depot's page draws its strip
	// with.
	Terminal []string `json:"terminal,omitempty"`
}

type StageCount struct {
	Stage string `json:"stage"`
	Open  int    `json:"open"`
}

// overview counts the yard for the home page and for the shell's line. y is
// the yard when the handler has read it already, nil otherwise.
func (s *Server) overview(ctx context.Context, y *yard) (*Home, error) {
	if y == nil {
		var err error
		if y, err = s.readYard(ctx); err != nil {
			return nil, err
		}
	}
	out := &Home{Yard: s.yardName(ctx), As: s.acting(ctx), Depots: []HomeDepot{},
		Attention: []Waiting{}, Running: []SessionRow{}, Closed: []HomeClosed{}, yard: y}
	manual := manualClaims(y.sessions)
	byDepot := map[string][]*store.Bead{}
	for i := range y.beads {
		if b := &y.beads[i]; b.Depot != nil {
			byDepot[*b.Depot] = append(byDepot[*b.Depot], b)
		}
	}
	allFlows, errs := s.depotFlows(ctx, y.depots)
	for i, depot := range y.depots {
		hr := HomeDepot{Name: depot.Name, Kind: depot.Kind, Flows: []HomeFlow{}}
		// One depot whose repository cannot be read must not take the page
		// down with it: the others still show.
		flows := allFlows[i]
		if errs[i] != nil {
			hr.Error = errs[i].Error()
		}
		for _, rf := range flows {
			hf := HomeFlow{Name: rf.Flow.Name, Type: rf.Type, Src: rf.Source.String(), Stages: []StageCount{}}
			for _, st := range rf.Flow.Stages {
				if rf.Flow.IsTerminal(st) {
					hf.Terminal = append(hf.Terminal, st)
				} else {
					hf.Stages = append(hf.Stages, StageCount{Stage: st})
				}
			}
			hr.Flows = append(hr.Flows, hf)
		}
		for _, b := range byDepot[depot.Name] {
			hr.Open++
			if working(b, manual) {
				hr.Working++
			}
			if b.Hold {
				hr.Held++
			}
			i := hr.flowIndex(b.Type)
			if i < 0 || !count(hr.Flows[i].Stages, b.Stage) {
				if !count(hr.Elsewhere, b.Stage) {
					hr.Elsewhere = append(hr.Elsewhere, StageCount{Stage: b.Stage, Open: 1})
				}
			}
		}
		slices.SortFunc(hr.Elsewhere, func(a, b StageCount) int {
			if a.Stage < b.Stage {
				return -1
			}
			return 1
		})
		out.Depots = append(out.Depots, hr)
	}
	return out, nil
}

// count adds one to stage's count in counts, and reports whether it is there.
func count(counts []StageCount, stage string) bool {
	for i := range counts {
		if counts[i].Stage == stage {
			counts[i].Open++
			return true
		}
	}
	return false
}

// flowIndex is the index of the flow that governs beads of typ in the
// depot: its type's flow when there is one, else the depot's own, -1 when
// no flow governs them. The overview and the sidebar assign beads to flows
// by this one rule.
func (d *HomeDepot) flowIndex(typ string) int {
	own := -1
	for j := range d.Flows {
		if f := &d.Flows[j]; f.Type == typ {
			if typ != "" {
				return j
			}
			if own < 0 {
				own = j
			}
		} else if f.Type == "" && own < 0 {
			own = j
		}
	}
	return own
}

// firstStage reports whether stage is the first stage of the flow that
// governs beads of typ in depot: where its beads start. What no flow
// governs (no depot, no flows, an empty flow) has no first stage.
func (h *Home) firstStage(depot, typ, stage string) bool {
	for i := range h.Depots {
		if d := &h.Depots[i]; d.Name == depot {
			if j := d.flowIndex(typ); j >= 0 {
				st := d.Flows[j].Stages
				return len(st) > 0 && st[0].Stage == stage
			}
			return false
		}
	}
	return false
}

// attention is what waits for a person: everything something stops, and
// the parked beads past their flow's first whose turn is not already
// answered, in the line's order, faults first. Home lists it in its
// centre; every other page counts it in its header.
func attention(ov *Home, l *Line) []Waiting {
	byID := map[string]*store.Bead{}
	for i := range ov.yard.beads {
		byID[ov.yard.beads[i].ID] = &ov.yard.beads[i]
	}
	out := append([]Waiting{}, l.Waits...)
	for _, w := range l.Parked {
		if b := byID[w.Bead]; b != nil && b.Depot != nil && ov.firstStage(*b.Depot, b.Type, w.Stage) {
			continue
		}
		if w.AnsweredOnly() {
			continue
		}
		out = append(out, w)
	}
	return out
}

// needsShown is how many waiting beads the home page names before the "All
// waiting N" row carries the rest to /line: five, as decided on yardr-8y0f.
// The JSON names them all either way.
const needsShown = 5

// Shown are the waiting beads the home page names: the first needsShown of
// Attention, in the line's order, faults first. The rest are one tap away
// behind "All waiting N".
func (h Home) Shown() []Waiting { return h.Attention[:min(len(h.Attention), needsShown)] }

// Extra is how many waiting beads hide behind "All waiting N": none when the
// list fits.
func (h Home) Extra() int { return len(h.Attention) - len(h.Shown()) }

// Answer is the home page's first line: whether anything needs the person,
// with the faults counted apart. One waiting bead counts once, whether its
// reason is a plain hold or a give-up.
func (h Home) Answer() string {
	n := len(h.Attention)
	if n == 0 {
		return "Nothing needs you."
	}
	faults := 0
	for _, w := range h.Attention {
		if w.Fault() {
			faults++
		}
	}
	out := fmt.Sprintf("%d need you.", n)
	if n == 1 {
		out = "1 needs you."
	}
	if faults > 0 {
		out += fmt.Sprintf(" %d fault", faults)
		if faults != 1 {
			out += "s"
		}
		out += "."
	}
	return out
}

// YardOpen, YardWorking and YardHeld count the yard's open beads: all of
// them, those an agent's or a command's session has claimed, and those
// held. The home page's Yard row names them and links them to the panel;
// the per-depot counts they are summed from stay in the JSON.
func (h Home) YardOpen() int {
	n := 0
	for _, d := range h.Depots {
		n += d.Open
	}
	return n
}

// YardWorking counts the yard's open beads an agent's or a command's
// session has claimed: the Yard row's working count.
func (h Home) YardWorking() int {
	n := 0
	for _, d := range h.Depots {
		n += d.Working
	}
	return n
}

// YardHeld counts the yard's open beads on hold: the Yard row's held count.
func (h Home) YardHeld() int {
	n := 0
	for _, d := range h.Depots {
		n += d.Held
	}
	return n
}

// OldestAge is how long the oldest running session has run: what the home
// page's Running row names beside its count. Running holds the oldest
// first, so it is the first row's age; empty when nothing runs.
func (h Home) OldestAge() string {
	if len(h.Running) == 0 {
		return ""
	}
	return h.Running[0].Age
}

// feedShown is how many kept events home's feed names before "All
// activity" carries the rest to /?all=1: ten, as decided on yardr-8y0f.
// The JSON names every entry either way.
const feedShown = 10

// FeedLines are the kept events home's feed names: the first feedShown of
// Kept, newest first. The rest are one tap away behind "All activity".
func (h Home) FeedLines() []Entry { return h.Kept[:min(len(h.Kept), feedShown)] }

// MoreActivity reports whether entries hide behind "All activity": the
// filter or the cap left some of the page out.
func (h Home) MoreActivity() bool { return len(h.FeedLines()) < len(h.Events) }

// closedShown is how many beads that closed today the home page names.
const closedShown = 10

// closedToday are the beads of beads that closed on now's date, the newest
// first, at most closedShown. The date is this machine's, as the pages show
// times; the store keeps close times in UTC, so they are read back in the
// clock's zone before the date is taken.
func closedToday(beads []store.Bead, now time.Time) []HomeClosed {
	out := []HomeClosed{}
	loc := now.Location()
	y, m, d := now.In(loc).Date()
	for i := range beads {
		b := &beads[i]
		if b.Status != "closed" || b.ClosedAt == nil {
			continue
		}
		if cy, cm, cd := b.ClosedAt.In(loc).Date(); cy != y || cm != m || cd != d {
			continue
		}
		out = append(out, HomeClosed{ID: b.ID, Title: b.Title, ClosedAt: *b.ClosedAt})
	}
	slices.SortStableFunc(out, func(a, b HomeClosed) int {
		return cmp.Or(b.ClosedAt.Compare(a.ClosedAt), cmp.Compare(a.ID, b.ID))
	})
	return out[:min(len(out), closedShown)]
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	var before uint64
	if v := r.URL.Query().Get("before"); v != "" {
		var err error
		if before, err = strconv.ParseUint(v, 10, 64); err != nil || before == 0 {
			s.respond(w, r, Page{Status: http.StatusBadRequest, Template: "error", Title: "Bad request",
				Data: errorData{Error: "before is an event's seq, not " + v}})
			return
		}
	}
	// The feed first: the line is read after it, so a bead the feed shows
	// created is never missing from the counts beside it.
	feed, err := s.feed(r.Context(), before)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ov, err := s.overview(r.Context(), nil)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ov.Feed = *feed
	// ?all=1, new on home after the /beads convention: the full page, and
	// an unfiltered stream. Else the feed keeps the kept lines, and the
	// first page's stream carries them.
	ov.All = all(r)
	if !ov.All {
		ov.Kept = s.keptHome(r.Context(), feed)
	}
	l, err := s.line(r.Context(), ov)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Home's aside is the line panel alone: the waits stand at the top of
	// its centre already.
	l.Home = true
	ov.Attention = append(ov.Attention, attention(ov, l)...)
	// What runs now: the live sessions but the manual groups' claims.
	rd := s.newReader(nil)
	for _, sess := range ov.yard.sessions {
		if row, manual := sessionRow(r.Context(), rd, sess); !manual {
			ov.Running = append(ov.Running, row)
		}
	}
	// What closed today: every closed bead, as /beads?all=1 lists them.
	all, err := s.st.ListBeads(r.Context(), store.BeadFilter{All: true, Viewer: s.acting(r.Context())})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ov.Closed = closedToday(all, time.Now())
	stream := ""
	if !ov.All && before == 0 {
		stream = "home"
	}
	s.respond(w, r, Page{Template: "home", Title: "Home", Menu: "home", Data: ov, line: l, ov: ov, head: feed.Head, stream: stream})
}

// Unbuilt is the Data of a bar page no child of the train has built yet.
type Unbuilt struct {
	Page  string `json:"page"`
	Built bool   `json:"built"`
}

func (s *Server) handleUnbuilt(name string) http.HandlerFunc {
	title := name
	if len(title) > 0 {
		title = strings.ToUpper(title[:1]) + title[1:]
	}
	return func(w http.ResponseWriter, r *http.Request) {
		// 501, not 200: a client reading JSON must not take the stub for
		// an empty list.
		s.respond(w, r, Page{Status: http.StatusNotImplemented, Template: "unbuilt", Title: title, Menu: name,
			Data: Unbuilt{Page: name}})
	}
}
