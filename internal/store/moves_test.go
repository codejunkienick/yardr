package store_test

import (
	"slices"
	"testing"

	"yardr.dev/yardr/internal/store"
)

// Without a flow a bead is offered only the stages with a route that could
// take it: its type, depot, labels, priority and originator all matching,
// once each and sorted, never its own stage. A bead no route could take is
// offered none.
func TestMovesForWithoutFlowOffersMatchingStages(t *testing.T) {
	t.Parallel()
	yard, shop := "yard", "shop"
	bug, task := "bug", "task"
	large := "size:large"
	one := 1
	hal, any := "hal", store.OriginatorAny
	routes := []store.Route{
		{Stage: "new"},
		{Stage: "review", Depot: &yard},
		{Stage: "decide", Type: &bug},
		{Stage: "build", Label: &large},
		{Stage: "qa", BeadPriority: &one},
		{Stage: "sign", Originator: &hal},
		{Stage: "verify", Depot: &shop, Type: &task},
		{Stage: "publish", Originator: &any},
		{Stage: "new", Type: &task},
	}
	for _, c := range []struct {
		name string
		bead store.Bead
		next *store.NextStages
		want []string // stages, or nil for no move
	}{
		{
			name: "every filter matching",
			bead: store.Bead{Stage: "backlog", Type: "bug", Depot: &yard,
				Labels: []string{"size:large"}, Priority: 1, Originator: "ada"},
			next: &store.NextStages{},
			want: []string{"build", "decide", "new", "publish", "qa", "review"},
		},
		{
			name: "depot and type scope the stages",
			bead: store.Bead{Stage: "new", Type: "task", Depot: &shop, Priority: 3},
			next: &store.NextStages{},
			want: []string{"publish", "verify"},
		},
		{
			// Nil NextStages takes the same branch.
			name: "a bead with no depot takes the defaults alone",
			bead: store.Bead{Stage: "backlog", Type: "task", Priority: 2, Originator: "ada"},
			next: nil,
			want: []string{"new", "publish"},
		},
		{
			name: "another person's bead takes their routes alone",
			bead: store.Bead{Stage: "backlog", Type: "bug", Depot: &yard,
				Labels: []string{"size:large"}, Priority: 0, Originator: "hal"},
			next: &store.NextStages{},
			want: []string{"publish", "sign"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var want []store.Move
			for _, to := range c.want {
				want = append(want, store.Move{Kind: store.MoveAdvance, To: to, Label: "Move to " + to})
			}
			if got := store.MovesFor(&c.bead, c.next, routes, "ada"); !slices.Equal(got, want) {
				t.Fatalf("MovesFor = %+v, want %+v", got, want)
			}
		})
	}
	// A bead matching none is offered none: the views show no move
	// buttons, and closing and answering asks are unchanged.
	for _, c := range []struct {
		name   string
		bead   store.Bead
		routes []store.Route
	}{
		{"no route matches", store.Bead{Stage: "backlog", Type: "task", Depot: &shop, Priority: 3},
			[]store.Route{{Stage: "review", Depot: &yard}}},
		{"its own stage alone", store.Bead{Stage: "new"}, []store.Route{{Stage: "new"}}},
		{"no routes", store.Bead{Stage: "backlog"}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := store.MovesFor(&c.bead, &store.NextStages{}, c.routes, "ada"); got != nil {
				t.Fatalf("MovesFor = %+v, want nil", got)
			}
		})
	}
}

// Long uniform lists fold behind one control; six stay flat, and anything
// but a plain advance — an outcome, a note, a terminal — keeps the list.
func TestFoldMoves(t *testing.T) {
	t.Parallel()
	plain := func(n int) []store.Move {
		var out []store.Move
		for i := 0; i < n; i++ {
			out = append(out, store.Move{Kind: store.MoveAdvance, To: "s"})
		}
		return out
	}
	seven := plain(7)
	withDone := append(slices.Clone(seven[:6]),
		store.Move{Kind: store.MoveDone, Outcome: "done", To: "s"})
	withNote := append(slices.Clone(seven[:6]),
		store.Move{Kind: store.MoveAdvance, To: "s", NoteRequired: true})
	withTerminal := append(slices.Clone(seven[:6]),
		store.Move{Kind: store.MoveAdvance, To: "s", Terminal: true})
	for _, c := range []struct {
		name  string
		moves []store.Move
		want  bool
	}{
		{"seven plain folds", seven, true},
		{"six plain stays flat", plain(6), false},
		{"an outcome keeps the list", withDone, false},
		{"a note keeps the list", withNote, false},
		{"a terminal keeps the list", withTerminal, false},
		{"none stays flat", nil, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := store.FoldMoves(c.moves); got != c.want {
				t.Fatalf("FoldMoves(%d moves) = %v, want %v", len(c.moves), got, c.want)
			}
		})
	}
}
