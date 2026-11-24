package dispatch

import (
	"context"
	"time"

	"yardr.dev/yardr/internal/store"
)

// Waited is the event raised on a bead that has been in a stage of a manual
// group for longer than the group's `wait`. Nothing runs in a manual group,
// so no session of it goes idle or ages: without this a bead nobody was
// told of waits unseen. It is of the class attention, and it is its own
// memory: one in the bead's log since the bead entered its stage means the
// stay was told of, so a daemon that replaces itself does not tell again,
// and a bead that leaves the stage and comes back is told of anew.
const Waited = "waited"

// stay is what the dispatcher remembers of a bead that waits in a manual
// group with a wait: when it entered its stage and whether that was told
// of, as its log had it at rev. The log is read again only when the bead
// has changed, not on every pass. entry is the stay's entry event, which
// its stage asks name; fell is that entry where a crew-first fall-through
// was already tried for the stay and asked nobody (FallThroughStageAsk
// asked no one it could: it is not tried again until the bead changes).
type stay struct {
	rev   string
	since time.Time
	told  bool
	entry uint64
	fell  uint64
}

// waits raises Waited, once per stay, on each open bead that has been
// longer than its manual group's wait in the stage it is in, by the
// dispatcher's clock. The group is the one that holds the bead, else the
// one its stage is routed to: a manual group at its limit leaves beads
// waiting unclaimed. A group that sets no wait raises nothing. beads and
// routes are the pass's, listed once for waits and sidings together.
//
// A manual group that asks its crew member first (ask_crew_first) is
// scanned beside them, wait or none: where its crew member has passed
// the stage's ask up, or stayed silent past the group's wait, the ask
// falls through to the group's people (crewFallThrough).
func (d *Dispatcher) waits(ctx context.Context, groups map[string]store.Group, beads []store.Bead, routes []store.Route, operator string) {
	limits := map[string]time.Duration{}
	crewFirst := map[string]bool{}
	for _, g := range groups {
		if g.Runner != "manual" {
			continue
		}
		// The store took the value as ParseSpan reads it; one from before
		// config was checked that does not parse is no wait.
		if s, _ := g.Config["wait"].(string); s != "" {
			if wait, err := store.ParseSpan(s); err == nil {
				limits[g.Name] = wait
			}
		}
		if g.AskCrewFirst() {
			crewFirst[g.Name] = true
		}
	}
	if len(limits) == 0 && len(crewFirst) == 0 {
		d.stays = nil
		return
	}
	var crew map[string]bool
	if len(crewFirst) > 0 {
		crew = map[string]bool{}
		if members, err := d.Store.Crew(ctx); err != nil {
			d.Log.Warn("crew-first beads", "err", err)
		} else {
			for _, m := range members {
				crew[m.Name] = true
			}
		}
	}
	now := d.now()
	stays := map[string]stay{}
	for _, b := range beads {
		group := ""
		if b.Group != nil {
			group = *b.Group
		} else if r := Match(routes, b, operator, groups); r != nil {
			group = r.Worker()
		}
		wait, hasWait := limits[group]
		crewGroup := crewFirst[group]
		if !hasWait && !crewGroup {
			continue
		}
		// A bead younger than the wait has not been anywhere that long, and
		// its log is not read to find out. A crew-first bead is read at any
		// age: a pass-up is due as soon as it stands.
		if hasWait && !crewGroup && now.Sub(b.CreatedAt) < wait {
			continue
		}
		st, known := d.stays[b.ID]
		if !known || st.rev != b.Revision {
			evs, err := d.Store.Events(ctx, b.ID, 1000)
			if err != nil {
				d.Log.Warn("waiting bead", "bead", b.ID, "err", err)
				continue
			}
			st = stayOf(evs, b)
		}
		if hasWait {
			if waited := now.Sub(st.since); !st.told && waited >= wait {
				d.Log.Warn("bead waited", "bead", b.ID, "group", group, "stage", b.Stage, "waited", round(waited))
				err := d.Store.LogDispatch(ctx, b.ID, Waited, actor, map[string]any{"group": group, "stage": b.Stage,
					"since": st.since.UTC().Format(time.RFC3339), "waited": round(waited).String()})
				if err != nil {
					d.Log.Warn(Waited+" event", "bead", b.ID, "err", err)
				}
				// One that could not be logged is tried again on the next pass.
				st.told = err == nil
			}
		}
		if crewGroup && crew != nil {
			d.crewFallThrough(ctx, b, groups[group], crew, &st, now)
		}
		stays[b.ID] = st
	}
	d.stays = stays
}

// crewFallThrough calls the store's fall-through for the bead where its
// crew-first stage ask has been passed up or has stood silent past the
// group's CrewWait, and remembers a try that asked nobody for the stay
// (st.fell), so it is not made on every pass. The store decides again
// what stands in its write; what is read here (the bead's asks at its
// stay's entry) only keeps a call that can ask nobody from being made,
// and silence before the wait asks nobody new. A person asked this stay,
// open or decided, is never called over.
func (d *Dispatcher) crewFallThrough(ctx context.Context, b store.Bead, g store.Group, crew map[string]bool, st *stay, now time.Time) {
	notes, err := d.Store.Notes(ctx, b.ID)
	if err != nil {
		d.Log.Warn("crew-first bead", "bead", b.ID, "err", err)
		return
	}
	entry := st.entry
	asks := store.ResolveAsks(notes, now)
	if entry == 0 {
		// The log no longer reaches the stay's entry: the latest stay's
		// asks are the ones with the largest entry for the stage.
		for _, r := range asks {
			if r.AskErr == nil && r.Ask.Stage == b.Stage && r.Ask.Entry > entry {
				entry = r.Ask.Entry
			}
		}
	}
	standing, passed := false, false
	for _, r := range asks {
		if r.AskErr != nil || r.Ask.Stage != b.Stage || r.Ask.Entry != entry {
			continue
		}
		if !crew[r.Ask.To] {
			return // a person was asked this stay: never re-placed
		}
		standing, passed = false, false
		switch r.State {
		case store.AskOpen, store.AskAside:
			standing = true
		case store.AskTakenBack:
			passed = true
		}
	}
	if !passed && !(standing && now.Sub(st.since) >= g.CrewWait()) {
		return
	}
	if st.fell == entry && entry != 0 {
		return
	}
	placed, err := d.Store.FallThroughStageAsk(ctx, b.ID, now)
	if err != nil {
		d.Log.Warn("crew-first fall-through", "bead", b.ID, "err", err)
		return
	}
	st.fell = entry
	if placed {
		d.Log.Info("stage ask fell through", "bead", b.ID, "group", g.Name, "stage", b.Stage)
	}
}

// stayOf reads b's stay in its stage from its log, evs, oldest first: when
// the bead entered the stage (and that entry's seq), and whether a Waited
// was raised since. A bead
// whose log no longer reaches back to that (pruned, or longer than was
// read) has been there since it was made, as far as anyone can tell.
func stayOf(evs []store.Event, b store.Bead) stay {
	st := stay{rev: b.Revision, since: b.CreatedAt}
	for i := len(evs) - 1; i >= 0; i-- {
		switch e := evs[i]; e.Kind {
		case Waited:
			st.told = true
		case "advanced", "reopened", "created":
			st.since, st.entry = e.At, e.Seq
			return st
		}
	}
	return st
}
