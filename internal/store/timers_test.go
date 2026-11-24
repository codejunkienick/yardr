package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/store"
)

// A timer removed from a crew member's pane is removed as that crew member,
// whatever --actor says: its own timer goes and timer_removed names the pane,
// not the operator the command typed, and another's is refused as the pane,
// not allowed as the operator. From no pane the actor stands as given. The
// owning store, not the socket's client, so the pane is the test's own in
// both modes.
func TestPaneTimerRemovalIsThePanesActor(t *testing.T) {
	t.Parallel()
	_, own := operatorYard(t)
	ctx := context.Background()
	set := func(actor string) *store.Timer {
		t.Helper()
		tm, err := own.AddTimer(ctx, store.NewTimer{Reason: "look at the shed", Due: time.Now().Add(time.Hour)}, actor)
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}
	removed := func() []store.Event {
		t.Helper()
		evs, err := own.EventsOfKind(ctx, "timer_removed", 10)
		if err != nil {
			t.Fatal(err)
		}
		return evs
	}
	// Both are set from no pane, before the pane is on record: the pane
	// would make the second one clerk's.
	ownTimer, otherTimer := set("clerk"), set("yardmaster")
	pane := crewPane(t, own, ctx, "clerk")

	if _, err := own.RemoveTimer(pane, ownTimer.ID, "jo"); err != nil {
		t.Fatalf("clerk removing its own timer from its pane, naming the operator: %v", err)
	}
	if evs := removed(); len(evs) != 1 || evs[0].Actor != "clerk" {
		t.Fatalf("timer_removed after clerk's own removal naming jo = %+v, want one, by clerk", evs)
	}

	// Naming the operator does not make the removal the operator's.
	_, err := own.RemoveTimer(pane, otherTimer.ID, "jo")
	if err == nil || !strings.Contains(err.Error(),
		"only the operator (jo) may remove the timer "+otherTimer.ID+", which yardmaster set for yardmaster; this is clerk") {
		t.Fatalf("clerk removing yardmaster's timer from its pane, naming the operator = %v", err)
	}
	if evs := removed(); len(evs) != 1 {
		t.Fatalf("timer_removed after the refusal = %+v, want clerk's alone", evs)
	}
	if live, err := own.Timers(ctx, "yardmaster"); err != nil || len(live) != 1 {
		t.Fatalf("yardmaster's live timers after the refusal = %+v, %v, want its one", live, err)
	}

	// From no pane the actor stands as given, and the operator removes any.
	if _, err := own.RemoveTimer(ctx, otherTimer.ID, "jo"); err != nil {
		t.Fatalf("the operator removing yardmaster's timer from no pane: %v", err)
	}
	if evs := removed(); len(evs) != 2 || evs[1].Actor != "jo" {
		t.Fatalf("timer_removed after the operator's removal from no pane = %+v, want clerk's, then jo's", evs)
	}
}
