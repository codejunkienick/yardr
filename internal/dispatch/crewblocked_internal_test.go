package dispatch

import (
	"context"
	"testing"
	"time"

	"yardr.dev/yardr/internal/store"
)

type crewStatusEvents struct {
	store.API
	events []store.Event
}

func (s crewStatusEvents) EventsOfKind(_ context.Context, kind string, limit int) ([]store.Event, error) {
	if kind != "crew_status" {
		return nil, nil
	}
	if limit > 0 && len(s.events) > limit {
		return s.events[len(s.events)-limit:], nil
	}
	return s.events, nil
}

func TestBlockedSpellStartFindsTransitionBehindOtherSessions(t *testing.T) {
	at := time.Now().Add(-6 * time.Minute)
	events := make([]store.Event, 101)
	events[0] = store.Event{Seq: 1, At: at, Data: map[string]any{"crew": "yardmaster", "status": "blocked"}}
	for i := 1; i < len(events); i++ {
		events[i] = store.Event{Seq: uint64(i + 1), At: at.Add(time.Duration(i) * time.Second),
			Data: map[string]any{"crew": "another-member", "status": "working"}}
	}
	d := &Dispatcher{Store: crewStatusEvents{events: events}}
	s := &memberSession{member: store.CrewMember{Name: "yardmaster"}}

	seq, gotAt, ok := d.blockedSpellStart(context.Background(), s)
	if !ok || seq != 1 || !gotAt.Equal(at) {
		t.Fatalf("blocked spell start = (%d, %s, %t), want (1, %s, true)", seq, gotAt, ok, at)
	}
}
