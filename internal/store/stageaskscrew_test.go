package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/store"
)

// crewFirstYard is a stage-ask yard whose reviewers group asks its crew
// member clerk first, with ada and mckean as its people and the given
// extra config (a wait, beside ask_crew_first).
func crewFirstYard(t *testing.T, extra map[string]any) (store.API, context.Context) {
	t.Helper()
	s, ctx := stageAskYard(t, stageAskChoice)
	if _, err := s.PutCrew(ctx, store.CrewMember{Name: "clerk", Runner: "herdr"}, "jo"); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{"ask_crew_first": true}
	for k, v := range extra {
		cfg[k] = v
	}
	if err := s.PutGroup(ctx, store.Group{Name: "reviewers", Runner: "manual",
		Members: []string{"clerk", "ada", "mckean"}, Config: cfg}, "jo"); err != nil {
		t.Fatal(err)
	}
	return s, ctx
}

func crewEnter(t *testing.T, s store.API, ctx context.Context) *store.Bead {
	t.Helper()
	b, err := s.CreateBead(ctx, store.NewBead{Title: "x", Depot: "web", Stage: "new"}, "me")
	if err != nil {
		t.Fatal(err)
	}
	if b, err = s.Advance(ctx, b.ID, "review", store.Expect{}, "me"); err != nil {
		t.Fatal(err)
	}
	return b
}

// asksTo are the bead's asks to name, oldest first.
func asksTo(got []store.ResolvedAsk, name string) []store.ResolvedAsk {
	var out []store.ResolvedAsk
	for _, r := range got {
		if r.Ask.To == name {
			out = append(out, r)
		}
	}
	return out
}

// A crew member who passes its ask up, or stays silent past the group's
// wait, leaves the asking to the group's people: the dispatcher calls
// FallThroughStageAsk, and the store places the people's asks in its
// write. Before the wait, silence asks nobody new; a person's decline is
// never re-placed; a second stay falls through anew.
func TestStageAskCrewFallThrough(t *testing.T) {
	t.Parallel()
	s, ctx := crewFirstYard(t, map[string]any{"wait": "1h"})
	now := time.Now()
	fall := func(id string, at time.Time, want bool) {
		t.Helper()
		if placed, err := s.FallThroughStageAsk(ctx, id, at); err != nil || placed != want {
			t.Fatalf("FallThroughStageAsk(%s, %v) = %v, %v; want %v", id, at, placed, err, want)
		}
	}

	// Silence: nobody new before the wait; the people after it, with the
	// crew ask taken back by the yard naming the silence, and no second
	// fall-through once the people stand asked.
	b := crewEnter(t, s, ctx)
	crew := asksTo(stageAsks(t, s, b.ID), "clerk")
	if len(crew) != 1 || crew[0].State != store.AskOpen {
		t.Fatalf("asks on entry = %+v, want clerk's open ask", stageAsks(t, s, b.ID))
	}
	fall(b.ID, now.Add(30*time.Minute), false)
	if got := stageAsks(t, s, b.ID); len(got) != 1 {
		t.Fatalf("asks before the wait = %d, want clerk's alone", len(got))
	}
	fall(b.ID, now.Add(61*time.Minute), true)
	got := stageAsks(t, s, b.ID)
	crew, ada, mckean := asksTo(got, "clerk"), asksTo(got, "ada"), asksTo(got, "mckean")
	if len(crew) != 1 || crew[0].State != store.AskTakenBack || crew[0].Current == nil ||
		crew[0].Current.Note.Author != "yard" ||
		!strings.Contains(crew[0].Current.Answer.Words, "no answer from clerk within 1h") {
		t.Fatalf("crew ask after the fall-through = %+v", crew)
	}
	for name, person := range map[string][]store.ResolvedAsk{"ada": ada, "mckean": mckean} {
		if len(person) != 1 || person[0].State != store.AskOpen || person[0].Ask.Stage != "review" ||
			person[0].Ask.Entry != crew[0].Ask.Entry || person[0].Ask.Question != crew[0].Ask.Question {
			t.Fatalf("%s's ask after the fall-through = %+v", name, person)
		}
	}
	fall(b.ID, now.Add(3*time.Hour), false)
	if got := stageAsks(t, s, b.ID); len(got) != 3 {
		t.Fatalf("asks after a second fall-through = %d, want 3", len(got))
	}
	// A person's decline is respected: never re-placed.
	if err := s.DeclineAsk(ctx, b.ID, ada[0].Note.Seq, "not mine", "ada"); err != nil {
		t.Fatal(err)
	}
	fall(b.ID, now.Add(4*time.Hour), false)
	if ada := asksTo(stageAsks(t, s, b.ID), "ada"); len(ada) != 1 || ada[0].State != store.AskDeclined {
		t.Fatalf("ada's asks after declining = %+v", ada)
	}

	// Pass-up: the crew member takes its ask back with its words, which
	// stand as its authored answer note, and the people are asked at
	// once, before any wait.
	b = crewEnter(t, s, ctx)
	crew = asksTo(stageAsks(t, s, b.ID), "clerk")
	words := "a person should decide this: it changes what the card promises"
	if err := s.TakeBackAsk(ctx, b.ID, crew[0].Note.Seq, words, "clerk"); err != nil {
		t.Fatal(err)
	}
	if got := stageAsks(t, s, b.ID); len(got) != 1 {
		t.Fatalf("asks after the pass-up, before the fall-through = %d, want clerk's alone", len(got))
	}
	fall(b.ID, now, true)
	got = stageAsks(t, s, b.ID)
	crew = asksTo(got, "clerk")
	if len(crew) != 1 || crew[0].State != store.AskTakenBack || crew[0].Current == nil ||
		crew[0].Current.Note.Author != "clerk" || crew[0].Current.Answer.Words != words {
		t.Fatalf("the passed-up crew ask = %+v", crew)
	}
	if ada := asksTo(got, "ada"); len(ada) != 1 || ada[0].State != store.AskOpen {
		t.Fatalf("ada's asks after the pass-up = %+v", ada)
	}

	// A second stay falls through anew: out of the stage and back, the
	// crew member is asked alone again, and its silence asks the people
	// again.
	b = crewEnter(t, s, ctx)
	fall(b.ID, now.Add(61*time.Minute), true)
	if _, err := s.Advance(ctx, b.ID, "new", store.Expect{}, "ada"); err != nil {
		t.Fatal(err)
	}
	if b, err := s.Advance(ctx, b.ID, "review", store.Expect{}, "me"); err != nil {
		t.Fatal(err)
	} else {
		_ = b
	}
	got = stageAsks(t, s, b.ID)
	var openCrew, openAda int
	var secondEntry uint64
	for _, r := range got {
		if r.State != store.AskOpen {
			continue
		}
		switch r.Ask.To {
		case "clerk":
			openCrew++
			secondEntry = r.Ask.Entry
		case "ada":
			openAda++
		}
	}
	if openCrew != 1 || openAda != 0 {
		t.Fatalf("second stay on entry: %d open crew asks, %d open ada asks", openCrew, openAda)
	}
	fall(b.ID, now.Add(61*time.Minute), true)
	got = stageAsks(t, s, b.ID)
	openAda = 0
	for _, r := range got {
		if r.Ask.To == "ada" && r.State == store.AskOpen && r.Ask.Entry == secondEntry {
			openAda++
		}
	}
	if openAda != 1 {
		t.Fatalf("second stay after its wait: %d open ada asks of the stay", openAda)
	}
}

// With no wait set, silence still falls through, after an hour: the
// group's wait unset must not hang the bead on a silent crew member.
func TestStageAskCrewFallThroughDefaultWait(t *testing.T) {
	t.Parallel()
	s, ctx := crewFirstYard(t, nil)
	b := crewEnter(t, s, ctx)
	now := time.Now()
	if placed, err := s.FallThroughStageAsk(ctx, b.ID, now.Add(30*time.Minute)); err != nil || placed {
		t.Fatalf("before the hour = %v, %v; want nobody asked", placed, err)
	}
	if placed, err := s.FallThroughStageAsk(ctx, b.ID, now.Add(61*time.Minute)); err != nil || !placed {
		t.Fatalf("after the hour = %v, %v; want the people asked", placed, err)
	}
	if ada := asksTo(stageAsks(t, s, b.ID), "ada"); len(ada) != 1 || ada[0].State != store.AskOpen {
		t.Fatalf("ada's asks = %+v", ada)
	}
}

// A crew member taking a stage ask back passes it up (ruleTakeBackAsk);
// a person addressed by one, and anyone taking a hand ask back who did
// not ask it, are refused as before.
func TestCrewMemberPassesAStageAskUp(t *testing.T) {
	t.Parallel()
	s, ctx := crewFirstYard(t, nil)
	b := crewEnter(t, s, ctx)
	crew := asksTo(stageAsks(t, s, b.ID), "clerk")
	if err := s.TakeBackAsk(ctx, b.ID, crew[0].Note.Seq, "not mine to decide", "ada"); err == nil ||
		!strings.Contains(err.Error(), "may take ask") {
		t.Fatalf("a person taking the crew ask back = %v", err)
	}
	if err := s.TakeBackAsk(ctx, b.ID, crew[0].Note.Seq, "not mine to decide", "clerk"); err != nil {
		t.Fatalf("the crew member taking its ask back = %v", err)
	}
	if got := asksTo(stageAsks(t, s, b.ID), "clerk"); got[0].State != store.AskTakenBack ||
		got[0].Current.Note.Author != "clerk" || got[0].Current.Answer.Words != "not mine to decide" {
		t.Fatalf("the passed-up ask = %+v", got)
	}
	// The operator still may, on a fresh bead.
	b = crewEnter(t, s, ctx)
	crew = asksTo(stageAsks(t, s, b.ID), "clerk")
	if err := s.TakeBackAsk(ctx, b.ID, crew[0].Note.Seq, "asked again below", "jo"); err != nil {
		t.Fatalf("the operator taking the crew ask back = %v", err)
	}
}
