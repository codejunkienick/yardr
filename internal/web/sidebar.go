package web

import (
	"fmt"
	"strconv"
)

// The shell's sidebar: the yard's chip and name, Arrivals and Decide with
// their counts, Crew bare, the depots with their flows, and Settings at the
// foot. On a phone the rail's bottom bar stands instead, as before. Every
// page renders inside it unchanged; it links only to routes the view has.
//
// The counts regroup what the shell already read: the overview's open beads
// and the line's waits. Arrivals is the beads in transit, totalled over
// depots: open beads not sitting in their flow's first stage, with beads of
// no depot and beads off their flow counted in the total. Decide is the
// rail's Decide pill, the number it replaces: the line's beads carrying an
// open ask for the viewer, one per bead, held or not — never the shell's
// Needs number, which counts fault and manual rows that carry no ask too.
// A flow's dot uses the same predicate: a bead of that flow with an open
// ask. Crew carries no count: the reads behind a session count ignore the
// viewer. An error page renders the sidebar bare: the yard, the fixed rows,
// no counts, no depots.

// sidebarFlow is one flow's row under its depot: its name, and its dot
// where a bead of the flow carries an open ask for the viewer. The row
// shows its dot or nothing: no per-flow count.
type sidebarFlow struct {
	Name string
	// Dot is whether a bead of the flow carries an open ask.
	Dot bool
	// Title is the row's title and aria-label: the flow, with the wait in
	// words where the dot stands.
	Title string
}

// sidebarDepot is one depot's row: its name and its beads in transit,
// opening to its flows. Quiet flows fold into one row.
type sidebarDepot struct {
	Name string
	// Transit counts the depot's open beads in transit: those past their
	// flow's first stage, and those off their flow.
	Transit int
	// Title is the row's title and aria-label: the depot, with the count
	// in words.
	Title string
	Flows []sidebarFlow
	// Quiet are the depot's quiet flows' names: nothing in transit, no
	// backlog, no open ask. QuietLabel is the folded row's words.
	Quiet      []string
	QuietLabel string
}

// sidebar is the shell's sidebar for the page.
type sidebar struct {
	// Yard is the yard's name, the chip's words.
	Yard string
	// Chip are its first two letters, on the chip.
	Chip string
	// Shade is the chip's rust shade, 0 to 2, as the rail's.
	Shade int
	// Bare is an error page's sidebar: the yard and the fixed rows, with
	// no counts and no depots.
	Bare bool
	// Arrivals counts the yard's open beads in transit. ArrivalsTitle is
	// the row's title and aria-label, with the count in words.
	Arrivals      int
	ArrivalsTitle string
	// Decide counts the line's beads carrying an open ask. DecidePill is
	// the pill's numeral, "" at zero, as the rail's; DecideTitle is the
	// row's title and aria-label, with the count in words.
	Decide      int
	DecidePill  string
	DecideTitle string
	Depots      []sidebarDepot
	// Quiet are the quiet depots' names: nothing in transit, no backlog.
	// QuietLabel is the folded row's words.
	Quiet      []string
	QuietLabel string
}

// transitWords is n beads in transit in words, for a row's title and
// aria-label.
func transitWords(n int) string {
	if n == 1 {
		return "1 in transit"
	}
	return fmt.Sprintf("%d in transit", n)
}

// sidebarFor is the shell's sidebar for the page: ov is the overview, l
// the shell's line. Either nil renders it bare, for an error page, as the
// rail with a nil line carries no pills.
func sidebarFor(yard string, ov *Home, l *Line) sidebar {
	out := sidebar{Yard: yard, Chip: chipLetters(yard), Shade: yardSlot(yard)}
	if ov == nil || l == nil {
		out.Bare = true
		out.ArrivalsTitle, out.DecideTitle = "Arrivals", "Decide"
		return out
	}
	byID := map[string]int{}
	for i := range ov.yard.beads {
		byID[ov.yard.beads[i].ID] = i
	}
	// The flows an open ask lights: the asked bead's flow by the
	// overview's rule, joined in memory through the overview. A wait is
	// one row per bead however many asks it holds, as the rail counts.
	dotted := map[int]map[int]bool{}
	decide := 0
	asked := func(w Waiting) {
		if !w.HasAsk() {
			return
		}
		decide++
		i, ok := byID[w.Bead]
		if !ok {
			return
		}
		b := &ov.yard.beads[i]
		if b.Depot == nil {
			return
		}
		for di := range ov.Depots {
			if d := &ov.Depots[di]; d.Name == *b.Depot {
				if j := d.flowIndex(b.Type); j >= 0 {
					if dotted[di] == nil {
						dotted[di] = map[int]bool{}
					}
					dotted[di][j] = true
				}
				return
			}
		}
	}
	for _, w := range l.Waits {
		asked(w)
	}
	for _, w := range l.Parked {
		asked(w)
	}
	out.Decide = decide
	out.DecideTitle = "Decide"
	if decide > 0 {
		out.DecidePill = pillLabel(decide)
		out.DecideTitle = "Decide — " + decideWords(decide)
	}
	byDepot := map[string][]int{}
	for i := range ov.yard.beads {
		if b := &ov.yard.beads[i]; b.Depot != nil {
			byDepot[*b.Depot] = append(byDepot[*b.Depot], i)
		} else {
			// An open bead with no depot counts in the total, and
			// under no depot's row.
			out.Arrivals++
		}
	}
	for di := range ov.Depots {
		d := &ov.Depots[di]
		transit, backlog := 0, 0
		inTransit, inBacklog := make([]int, len(d.Flows)), make([]int, len(d.Flows))
		for _, i := range byDepot[d.Name] {
			b := &ov.yard.beads[i]
			j := d.flowIndex(b.Type)
			if j < 0 || !count(d.Flows[j].Stages, b.Stage) {
				// Off its flow — a bead pinned to another flow, or
				// one a changed flow left behind — counts as in
				// transit, as a first stage it cannot sit in would.
				transit++
				continue
			}
			if d.Flows[j].Stages[0].Stage == b.Stage {
				backlog++
				inBacklog[j]++
			} else {
				transit++
				inTransit[j]++
			}
		}
		out.Arrivals += transit
		if transit == 0 && backlog == 0 {
			out.Quiet = append(out.Quiet, d.Name)
			continue
		}
		sd := sidebarDepot{Name: d.Name, Transit: transit,
			Title: d.Name + " — " + transitWords(transit)}
		for j := range d.Flows {
			if inTransit[j] == 0 && inBacklog[j] == 0 && !dotted[di][j] {
				sd.Quiet = append(sd.Quiet, d.Flows[j].Name)
				continue
			}
			sf := sidebarFlow{Name: d.Flows[j].Name, Dot: dotted[di][j], Title: d.Flows[j].Name}
			if sf.Dot {
				sf.Title += " — waits for you"
			}
			sd.Flows = append(sd.Flows, sf)
		}
		if len(sd.Quiet) > 0 {
			sd.QuietLabel = strconv.Itoa(len(sd.Quiet)) + " quiet flows"
			if len(sd.Quiet) == 1 {
				sd.QuietLabel = "1 quiet flow"
			}
		}
		out.Depots = append(out.Depots, sd)
	}
	if len(out.Quiet) > 0 {
		out.QuietLabel = "Quiet depots · " + strconv.Itoa(len(out.Quiet))
	}
	out.ArrivalsTitle = "Arrivals — " + transitWords(out.Arrivals)
	return out
}
