package cli

// A stage a person moves whose file has no ## Ask puts no card in front of
// anyone: a bead waiting there is in no one's Decide, and only yardr review at
// a terminal moves it (docs/flows.md, The ask template). flow show and flow
// set say so as a warning, never a refusal, since a stage may be meant for
// yardr review alone.

import (
	"context"
	"fmt"
	"io"

	"yardr.dev/yardr/internal/dispatch"
	"yardr.dev/yardr/internal/store"
)

// routing is the yard's routes and groups, read once to say which group takes
// a bead at a stage.
type routing struct {
	routes   []store.Route // in the order they match
	groups   []store.Group
	byName   map[string]store.Group
	operator string
}

func readRouting(ctx context.Context, st store.API) (*routing, error) {
	routes, err := st.Routes(ctx)
	if err != nil {
		return nil, err
	}
	dispatch.SortRoutes(routes)
	settings, err := st.Settings(ctx)
	if err != nil {
		return nil, err
	}
	groups, err := st.Groups(ctx)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]store.Group, len(groups))
	for _, g := range groups {
		byName[g.Name] = g
	}
	return &routing{routes: routes, groups: groups, byName: byName, operator: settings[store.OperatorSetting]}, nil
}

// take is the route a bead of type typ in depot would take at stage, nil when
// none does. Routes may match on type, so a flow with no type of its own is
// probed as the default one, and the probe names no originator, so it reads as
// the operator's.
func (rt *routing) take(depot *string, typ, stage string) *store.Route {
	probe := store.Bead{Stage: stage, Type: or(typ, "task"), Depot: depot, Priority: store.DefaultPriority}
	return dispatch.Match(rt.routes, probe, rt.operator, rt.byName)
}

// waitsForPerson says whether a bead in the stage sv describes of f waits
// for a person: the stage is past the flow's first, where a backlog waits and
// no person is waited for (docs/web.md, attention), it is not terminal, and
// every group its route offers, the first and those after else, is manual.
func waitsForPerson(f *store.Flow, sv stageView, byName map[string]store.Group) bool {
	if sv.Group == "" || sv.Terminal || sv.Stage == f.Stages[0] {
		return false
	}
	for _, g := range append([]string{sv.Group}, sv.Else...) {
		if byName[g].Runner != "manual" {
			return false
		}
	}
	return true
}

// noAskWarning is the warning for a stage that waits for a person and has no
// ## Ask.
func noAskWarning(stage string) string {
	return "stage " + stage + " waits for a person but has no ## Ask: its beads reach no one's Decide (docs/flows.md, The ask template)"
}

// warnStagesWithoutAsk says on warn which stages of the store flow f wait for a
// person and have no ## Ask in the yard's home, each as a warning. The flow is
// saved when it is asked, so what it cannot read is a warning too, not a
// failure.
func (a *app) warnStagesWithoutAsk(warn io.Writer, ctx context.Context, st store.API, f *store.Flow) {
	files := map[string]*store.StageFile{}
	for _, s := range f.Stages {
		sf, err := store.HomeStageFile(a.home(), f.Name, s)
		if err != nil {
			fmt.Fprintf(warn, "warning: %v\n", err)
			return
		}
		files[s] = sf
	}
	stages, err := stagesWithoutAsk(ctx, st, f, files)
	if err != nil {
		fmt.Fprintf(warn, "warning: %v\n", err)
		return
	}
	for _, s := range stages {
		fmt.Fprintf(warn, "warning: %s\n", noAskWarning(s))
	}
}

// stagesWithoutAsk are the stages of the store flow f where a bead waits for a
// person and files, the flow's stage files by stage, have no ## Ask, in the
// flow's order. A named flow has no depot to ask the routes of, so a stage is
// read for each depot and type that follow f (store.FlowPointer), and for a
// bead of no depot when none does, which only a route with no depot filter
// takes.
func stagesWithoutAsk(ctx context.Context, st store.API, f *store.Flow, files map[string]*store.StageFile) ([]string, error) {
	rt, err := readRouting(ctx, st)
	if err != nil {
		return nil, err
	}
	all, err := st.FlowPointers(ctx)
	if err != nil {
		return nil, err
	}
	var follow []store.FlowPointer
	for _, p := range all {
		if p.Flow == f.Name {
			follow = append(follow, p)
		}
	}
	if len(follow) == 0 {
		follow = []store.FlowPointer{{}}
	}
	var out []string
	for _, s := range f.Stages {
		if files[s].HasAsk() {
			continue
		}
		for _, p := range follow {
			var depot *string
			if p.Depot != "" {
				depot = &p.Depot
			}
			r := rt.take(depot, p.Type, s)
			if r == nil {
				continue
			}
			if sv := (stageView{Stage: s, Group: r.Worker(), Else: r.Further(), Terminal: f.IsTerminal(s)}); waitsForPerson(f, sv, rt.byName) {
				out = append(out, s)
				break
			}
		}
	}
	return out, nil
}
