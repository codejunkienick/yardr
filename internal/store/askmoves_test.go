package store_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/store"
)

// askMovesFlow is the shape of the depot's own flow: work stages send a bead
// to decide on the outcome question, and decide leaves by bare edges, to
// backlog or back to new, the stage that asked allowed back alongside.
const askMovesFlow = "backlog -> new | decide\n" +
	"new -> review on done | decide on question | backlog\n" +
	"review -> approved on done | new on changes | decide on question\n" +
	"decide -> backlog | new\n" +
	"approved -> merged\n" +
	"merged terminal\n"

// An agent that sends its bead to decide with a question of options says what
// each option does (--move), and the stage's card takes the question over:
// its options are the card's choices, each firing its move, the
// recommendation marked, and the pick reaches whoever works the bead next as
// the option picked. A question that named none is quoted on the stage's card
// of moves; a move no longer offered falls back to the same, saying so. Either
// way the question is taken back, naming the card.
func askMovesTakeOverTheQuestion(t *testing.T) {
	t.Helper()
	y := stageMovesYardOf(t, askMovesFlow, map[string]string{"decide": stageMovesFile},
		map[string][]string{"decide": {"ada", "hal"}})
	newBead := func(title, stage string) string {
		t.Helper()
		b, err := y.api.CreateBead(y.ctx, store.NewBead{Title: title, Depot: "web", Stage: stage}, "me")
		if err != nil {
			t.Fatal(err)
		}
		return b.ID
	}
	file := func(id string, ask store.Ask) uint64 {
		t.Helper()
		seq, err := y.api.AddAsk(y.ctx, id, ask, "the-agent")
		if err != nil {
			t.Fatal(err)
		}
		return seq
	}
	toDecide := func(id string) {
		t.Helper()
		if _, err := y.api.Done(y.ctx, id, "question", "", store.Expect{}, "me"); err != nil {
			t.Fatal(err)
		}
	}
	other := newBead("the other", "new")
	ship := store.Ask{
		Question: "Ship it so, add a fallback, or drop it?", To: "ada",
		Context: []string{"The export ignores the locale setting.", "A fallback costs a day."},
		Options: []store.AskOption{
			{Label: "Ship it so", Consequence: "it ships as built"},
			{Label: "Add a fallback", Consequence: "a fallback is added before it ships"},
			{Label: "Drop it", Consequence: "the export is dropped"},
		},
		Recommend: 2, Why: "the fallback is a day, and the locale is the point",
		About: []string{other}, Move: []string{"new", "new", "backlog"},
	}

	// Three options with their moves and a recommendation: one card for each
	// of decide's people, in place of the question, with the agent's options as
	// its choices, each saying in the yard's words where it sends the bead, and
	// no You decide.
	id := newBead("ship it?", "new")
	seq := file(id, ship)
	if got := stageAsks(t, y.api, id); len(got) != 1 || !slices.Equal(got[0].Ask.Move, ship.Move) || len(got[0].Ask.Options) != 4 {
		t.Fatalf("the question as filed: %+v, want its moves kept and You decide last", got)
	}
	toDecide(id)
	got := stageAsks(t, y.api, id)
	if len(got) != 3 {
		t.Fatalf("asks on arrival: %+v, want the question and a card for each of two people", got)
	}
	hand, cards := got[0], got[1:]
	if hand.Note.Seq != seq || hand.State != store.AskTakenBack {
		t.Fatalf("the question on arrival: ask %d %s, want ask %d taken back", hand.Note.Seq, hand.State, seq)
	}
	if lines := strings.Join(hand.Lines(), "\n"); !strings.Contains(lines,
		fmt.Sprintf("taken back by yard: asks %d and %d are the question now, with its options", cards[0].Note.Seq, cards[1].Note.Seq)) {
		t.Fatalf("the question reads:\n%s", lines)
	}
	wantCard := [][3]string{
		{"Ship it so", "->new", "it ships as built (the bead goes back to new, with your words as the note)"},
		{"Add a fallback", "->new", "a fallback is added before it ships (the bead goes back to new, with your words as the note)"},
		{"Drop it", "->backlog", "the export is dropped (the bead goes back to backlog, with your words as the note)"},
	}
	for i, who := range []string{"ada", "hal"} {
		c := cards[i].Ask
		if c.To != who || c.Stage != "decide" || c.Entry == 0 || c.Moves || cards[i].State != store.AskOpen {
			t.Fatalf("%s's card = %+v (%s)", who, c, cards[i].State)
		}
		if want := ship.Question + " — " + id + ": ship it?"; c.Question != want {
			t.Fatalf("%s's card asks %q, want %q", who, c.Question, want)
		}
		wantContext := []string{"Bead: " + id + " — ship it? (branch yard/" + id + ")", ship.Context[0], ship.Context[1]}
		if !slices.Equal(c.Context, wantContext) {
			t.Fatalf("%s's card context %q, want %q", who, c.Context, wantContext)
		}
		if diff := cardOf(c); !slices.Equal(diff, wantCard) {
			t.Fatalf("%s's card offers\n%q\nwant\n%q", who, diff, wantCard)
		}
		if c.Recommend != 2 || c.Why != ship.Why || !slices.Equal(c.About, []string{other}) {
			t.Fatalf("%s's card recommends %d (%q) about %q", who, c.Recommend, c.Why, c.About)
		}
	}

	// A pick moves the bead by the option's move, in the answer's write, and
	// is recorded as the option picked, with the words beside it.
	if err := y.api.AddAnswer(y.ctx, id, store.Answer{Ask: cards[0].Note.Seq, Option: 2, Words: "as you suggest"}, "ada"); err != nil {
		t.Fatal(err)
	}
	if y.stage(t, id) != "new" {
		t.Fatalf("after the pick of the second option the bead is in %s, want new", y.stage(t, id))
	}
	after := stageAsks(t, y.api, id)
	if c := after[1]; c.State != store.AskAnswered || c.Current.Answer.Option != 2 || c.Current.Answer.Words != "as you suggest" {
		t.Fatalf("the card after the pick: %s, %+v", c.State, c.Current)
	}
	if lines := strings.Join(after[1].Lines(), "\n"); !strings.Contains(lines, "ada picked 2 (Add a fallback): as you suggest") {
		t.Fatalf("the card reads:\n%s", lines)
	}
	if after[2].State != store.AskTakenBack {
		t.Fatalf("the other person's card is %s, want it taken back with the move", after[2].State)
	}
	if err := y.api.AddAnswer(y.ctx, id, store.Answer{Ask: seq, Option: 1}, "ada"); err == nil || !strings.Contains(err.Error(), "is asked no more") {
		t.Fatalf("a pick on the question the card took over: %v", err)
	}

	// The stage that asked is a move too, and Later and Decline are what they
	// always were: they move nothing.
	id = newBead("from review", "new")
	if _, err := y.api.Advance(y.ctx, id, "review", store.Expect{}, "me"); err != nil {
		t.Fatal(err)
	}
	back := ship
	back.Options, back.Recommend, back.Why, back.About, back.Move = ship.Options[:2], 0, "", nil, []string{"review", "backlog"}
	file(id, back)
	toDecide(id)
	cards = stageAsks(t, y.api, id)[1:]
	if len(cards) != 2 || cards[0].Ask.Move[0] != "->review" || cards[0].Ask.Move[1] != "->backlog" || cards[0].Ask.Recommend != 0 {
		t.Fatalf("the cards of a question from review: %+v, want its moves back to review and on to backlog", cards)
	}
	if err := y.api.AddAnswer(y.ctx, id, store.Answer{Ask: cards[0].Note.Seq, Decline: true, Words: "not mine to say"}, "ada"); err != nil {
		t.Fatal(err)
	}
	if err := y.api.AddAnswer(y.ctx, id, store.Answer{Ask: cards[1].Note.Seq, Later: true}, "hal"); err != nil {
		t.Fatal(err)
	}
	if now := stageAsks(t, y.api, id)[1:]; y.stage(t, id) != "decide" || now[0].State != store.AskDeclined || now[1].State != store.AskAside {
		t.Fatalf("after a decline and a later the bead is in %s with cards %s and %s", y.stage(t, id), now[0].State, now[1].State)
	}
	if err := y.api.AddAnswer(y.ctx, id, store.Answer{Ask: cards[1].Note.Seq, Option: 1}, "hal"); err != nil {
		t.Fatal(err)
	}
	if y.stage(t, id) != "review" {
		t.Fatalf("after the put-aside card's pick of the first option the bead is in %s, want review", y.stage(t, id))
	}

	// A question that named no moves is quoted on the stage's card of moves,
	// recommendation marked, and taken back naming the card.
	id = newBead("no moves", "new")
	plain := ship
	plain.Move, plain.About = nil, nil
	file(id, plain)
	toDecide(id)
	got = stageAsks(t, y.api, id)
	if len(got) != 3 || got[0].State != store.AskTakenBack || !got[1].Ask.Moves {
		t.Fatalf("asks after a question with no moves: %+v, want it taken back and the cards of moves", got)
	}
	wantQuote := []string{
		"Bead: " + id + " — no moves (branch yard/" + id + ")",
		"The question: " + ship.Question,
		ship.Context[0], ship.Context[1],
		"1. Ship it so - it ships as built",
		"2. Add a fallback - a fallback is added before it ships (recommended: " + ship.Why + ")",
		"3. Drop it - the export is dropped",
		"The bead waits for a person; the options are the moves of this stage.",
	}
	if c := got[1].Ask; !slices.Equal(c.Context, wantQuote) || len(cardOf(c)) != 2 || c.Move[0] != "->backlog" || c.Move[1] != "->new" {
		t.Fatalf("the card of moves: context %q, moves %q, want the question quoted and decide's two moves", c.Context, c.Move)
	}

	// A move the stage no longer offers falls back to the same, and says so:
	// the bead was moved to decide by hand, which brings the asking stage no
	// way back, though the question named it.
	id = newBead("moved by hand", "new")
	if _, err := y.api.Advance(y.ctx, id, "review", store.Expect{}, "me"); err != nil {
		t.Fatal(err)
	}
	file(id, back)
	if _, err := y.api.Advance(y.ctx, id, "decide", store.Expect{}, "me"); err != nil {
		t.Fatal(err)
	}
	got = stageAsks(t, y.api, id)
	if len(got) != 3 || got[0].State != store.AskTakenBack || !got[1].Ask.Moves {
		t.Fatalf("asks after a move no longer offered: %+v, want it taken back and the cards of moves", got)
	}
	last := got[1].Ask.Context[len(got[1].Ask.Context)-2]
	if want := "The moves it named are no longer offered here (review): pick where the bead goes."; last != want {
		t.Fatalf("the card says %q, want %q", last, want)
	}

	// The moves move nothing but through the card: the question answered where
	// the bead is, as a hand ask always was, records the pick.
	id = newBead("answered early", "new")
	seq = file(id, ship)
	if err := y.api.AddAnswer(y.ctx, id, store.Answer{Ask: seq, Option: 3}, "ada"); err != nil {
		t.Fatal(err)
	}
	if got := stageAsks(t, y.api, id); y.stage(t, id) != "new" || got[0].State != store.AskAnswered || got[0].Current.Answer.Option != 3 {
		t.Fatalf("a pick before the bead arrived: bead in %s, ask %s", y.stage(t, id), got[0].State)
	}
}

// Where no person of the stage can be given a card, it goes to the operator as
// every stage card does, and the question is taken over all the same.
func askMovesTakeOverForTheOperator(t *testing.T) {
	t.Helper()
	y := stageMovesYardOf(t, askMovesFlow, map[string]string{"decide": stageMovesFile},
		map[string][]string{"decide": {"bob@elsewhere"}})
	b, err := y.api.CreateBead(y.ctx, store.NewBead{Title: "work", Depot: "web", Stage: "new"}, "me")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := y.api.AddAsk(y.ctx, b.ID, store.Ask{
		Question: "Which?", To: "ada", Context: []string{"Two ways."},
		Options: []store.AskOption{{Label: "A", Consequence: "goes one way"}, {Label: "B", Consequence: "goes the other"}},
		Move:    []string{"new", "backlog"},
	}, "the-agent"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.api.Done(y.ctx, b.ID, "question", "", store.Expect{}, "me"); err != nil {
		t.Fatal(err)
	}
	got := stageAsks(t, y.api, b.ID)
	if len(got) != 2 || got[0].State != store.AskTakenBack || got[1].State != store.AskOpen ||
		got[1].Ask.To != "jo" || !got[1].Ask.Fallback || got[1].Ask.Moves || len(got[1].Ask.Options) != 2 {
		t.Fatalf("asks for an operator's card: %+v, want the question taken back and the card for jo, as fallback", got)
	}
}

// A move is refused when it is filed, with what the stage offers, so that the
// agent that mistyped it is told then and not when the bead arrives: all
// options name one or none does, a name must be one decide's card offers from
// the stage the bead is in, and a bead that no outcome of its stage sends to
// decide, or that no flow governs, or that is made with its question, has no
// moves to name.
func askMovesRefusals(t *testing.T) {
	t.Helper()
	y := stageMovesYardOf(t, askMovesFlow, map[string]string{"decide": stageMovesFile},
		map[string][]string{"decide": {"ada"}})
	ask := func(moves ...string) store.Ask {
		return store.Ask{
			Question: "Which?", To: "ada", Context: []string{"Two ways."},
			Options: []store.AskOption{{Label: "A", Consequence: "goes one way"}, {Label: "B", Consequence: "goes the other"}},
			Move:    moves,
		}
	}
	work, err := y.api.CreateBead(y.ctx, store.NewBead{Title: "work", Depot: "web", Stage: "new"}, "me")
	if err != nil {
		t.Fatal(err)
	}
	waiting, err := y.api.CreateBead(y.ctx, store.NewBead{Title: "waiting", Depot: "web", Stage: "backlog"}, "me")
	if err != nil {
		t.Fatal(err)
	}
	free, err := y.api.CreateBead(y.ctx, store.NewBead{Title: "no flow"}, "me")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		id   string
		ask  store.Ask
		want string
	}{
		{"a name decide does not offer", work.ID, ask("new", "nowhere"), `--move "nowhere" (option 2): stage decide offers backlog, new`},
		{"the asking stage is offered, not another", work.ID, ask("review", "new"), `--move "review" (option 1): stage decide offers backlog, new`},
		{"a stage no outcome sends to decide", waiting.ID, ask("new", "backlog"), "has none"},
		{"a bead no flow governs", free.ID, ask("new", "backlog"), "no flow governs bead " + free.ID},
		{"one move of two", work.ID, ask("new"), "the ask has 2 options and 1 moves"},
		{"a blank move", work.ID, ask("new", " "), "--move 2 is empty"},
	}
	for _, c := range cases {
		if _, err := y.api.AddAsk(y.ctx, c.id, c.ask, "the-agent"); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
	if _, err := y.api.AskNew(y.ctx, store.NewBead{Title: "Which?", Depot: "web", Stage: "decide"}, ask("new", "backlog"), "the-agent"); err == nil ||
		!strings.Contains(err.Error(), "--new makes the bead with the ask") {
		t.Errorf("a question asked into decide with moves: %v", err)
	}
	for _, id := range []string{work.ID, waiting.ID, free.ID} {
		if got := stageAsks(t, y.api, id); len(got) != 0 {
			t.Fatalf("refused asks left %+v on %s", got, id)
		}
	}
	// Moves that are offered are kept as the writer named them.
	seq, err := y.api.AddAsk(y.ctx, work.ID, ask(" backlog", "new "), "the-agent")
	if err != nil {
		t.Fatal(err)
	}
	if got := stageAsks(t, y.api, work.ID); len(got) != 1 || got[0].Note.Seq != seq || !slices.Equal(got[0].Ask.Move, []string{"backlog", "new"}) {
		t.Fatalf("the ask with moves as filed: %+v", got)
	}
}
