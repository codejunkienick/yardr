package store_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	bolt "go.etcd.io/bbolt"

	"yardr.dev/yardr/internal/store"
)

// turnAsk is a plain ask for mckean with two options, as an ordinary card.
func turnAsk(question string) store.Ask {
	return store.Ask{Question: question, To: "mckean", Context: []string{"the links file declares the new source."},
		Options: []store.AskOption{{Label: "Move them now", Consequence: "the links move tonight"},
			{Label: "After Friday", Consequence: "the links move after the release"}}}
}

// turnBead is a bead outside every flow's moves with the asks asked, by
// yardmaster, each for mckean; it returns the bead and the asks' seqs.
func turnBead(t *testing.T, s store.API, ctx context.Context, asks ...string) (string, []uint64) {
	t.Helper()
	b, err := s.CreateBead(ctx, store.NewBead{Title: "Move the links"}, "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	var seqs []uint64
	for _, q := range asks {
		seq, err := s.AddAsk(ctx, b.ID, turnAsk(q), "yardmaster")
		if err != nil {
			t.Fatal(err)
		}
		seqs = append(seqs, seq)
	}
	return b.ID, seqs
}

// beadAsk reads the ask seq of the bead id as Bead serves it.
func beadAsk(t *testing.T, s store.API, ctx context.Context, id string, seq uint64) store.BeadAsk {
	t.Helper()
	v, err := s.Bead(ctx, id, "mckean")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range v.Asks {
		if a.Ask == seq {
			return a
		}
	}
	t.Fatalf("bead %s serves no ask %d", id, seq)
	return store.BeadAsk{}
}

// answersOn counts the answer notes on the ask seq of the bead id.
func answersOn(t *testing.T, s store.API, ctx context.Context, id string, seq uint64) int {
	t.Helper()
	notes, err := s.Notes(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, note := range notes {
		if a, err := store.ParseAnswer(note); note.Kind == store.NoteAnswer && err == nil && a.Ask == seq {
			n++
		}
	}
	return n
}

// The stale write of an ordinary card: an answer made elsewhere (option 2
// with words) lands between the client's read and its own pick of option 1.
// The guarded pick is refused as a conflict and writes nothing, so the
// answer made elsewhere stands with its words, as the one answer note, and
// the sibling ask stays open; the unguarded pick replaces it, as it always
// has.
func TestGuardedAnswerKeepsAnExternalAnswer(t *testing.T) {
	t.Parallel()
	s, ctx := stageAskYard(t, stageAskChoice)
	id, seqs := turnBead(t, s, ctx, "Move this yard's links now?", "Close sketch PR #3?")
	ask, sibling := seqs[0], seqs[1]
	read := beadAsk(t, s, ctx, id, ask)
	if read.State != "open" || read.Turn != 0 {
		t.Fatalf("the card as read: %s at turn %d, want open at 0", read.State, read.Turn)
	}
	before, err := s.GetBead(ctx, id)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.AnswerAsk(ctx, id, ask, 2, "external answer", "mckean", false); err != nil {
		t.Fatal(err)
	}
	err = s.AnswerAskExpected(ctx, id, ask, read.Turn, 1, "", "mckean", false, 0)
	if !errors.Is(err, store.ErrConflict) || !strings.Contains(err.Error(), "at turn 1, not 0") {
		t.Fatalf("the stale guarded pick = %v, want a conflict at turn 1", err)
	}
	got := beadAsk(t, s, ctx, id, ask)
	if got.State != "answered" || got.Option != 2 || got.Words != "external answer" || got.PicksBefore != 0 || got.Turn != 1 {
		t.Fatalf("after the stale pick: %s option %d words %q picks_before %d turn %d, want the external answer alone",
			got.State, got.Option, got.Words, got.PicksBefore, got.Turn)
	}
	if n := answersOn(t, s, ctx, id, ask); n != 1 {
		t.Fatalf("answer notes on the ask = %d, want 1", n)
	}
	if sib := beadAsk(t, s, ctx, id, sibling); sib.State != "open" || sib.Turn != 0 {
		t.Fatalf("the sibling ask = %s at turn %d, want open at 0", sib.State, sib.Turn)
	}
	after, err := s.GetBead(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Stage != before.Stage || after.Status != before.Status {
		t.Fatalf("the bead went from %s/%s to %s/%s", before.Stage, before.Status, after.Stage, after.Status)
	}

	// The unguarded call keeps its contract: a newer pick replaces.
	if err := s.AnswerAsk(ctx, id, ask, 1, "", "mckean", false); err != nil {
		t.Fatal(err)
	}
	got = beadAsk(t, s, ctx, id, ask)
	if got.Option != 1 || got.Words != "" || got.PicksBefore != 1 || got.Turn != 2 {
		t.Fatalf("the unguarded repick: option %d words %q picks_before %d turn %d, want option 1 replacing at turn 2",
			got.Option, got.Words, got.PicksBefore, got.Turn)
	}
	if n := answersOn(t, s, ctx, id, ask); n != 2 {
		t.Fatalf("answer notes after the unguarded repick = %d, want 2", n)
	}
}

// Every write made elsewhere since the read, whatever it was, refuses each
// guarded write drawn from the read, and leaves the ask as that write left it.
func TestGuardedAnswerRefusesEveryStaleTurn(t *testing.T) {
	t.Parallel()
	s, ctx := stageAskYard(t, stageAskChoice)
	external := map[string]func(id string, seq uint64) error{
		"a pick": func(id string, seq uint64) error {
			return s.AnswerAsk(ctx, id, seq, 2, "external answer", "mckean", false)
		},
		"put aside": func(id string, seq uint64) error {
			return s.DeferAsk(ctx, id, seq, "mckean")
		},
		"a decline": func(id string, seq uint64) error { return s.DeclineAsk(ctx, id, seq, "not mine", "mckean") },
		"taken back": func(id string, seq uint64) error {
			return s.TakeBackAsk(ctx, id, seq, "asked no more", "yardmaster")
		},
		"a guarded pick": func(id string, seq uint64) error {
			return s.AnswerAskExpected(ctx, id, seq, 0, 2, "", "mckean", false, 0)
		},
	}
	guarded := map[string]func(id string, seq uint64) error{
		"pick": func(id string, seq uint64) error {
			return s.AnswerAskExpected(ctx, id, seq, 0, 1, "", "mckean", false, 0)
		},
		"defer":   func(id string, seq uint64) error { return s.DeferAskExpected(ctx, id, seq, 0, "mckean") },
		"decline": func(id string, seq uint64) error { return s.DeclineAskExpected(ctx, id, seq, 0, "stale", "mckean", 0) },
	}
	for ename, ext := range external {
		for gname, call := range guarded {
			id, seqs := turnBead(t, s, ctx, "Move this yard's links now?")
			if err := ext(id, seqs[0]); err != nil {
				t.Fatalf("%s: %v", ename, err)
			}
			want := beadAsk(t, s, ctx, id, seqs[0])
			if err := call(id, seqs[0]); !errors.Is(err, store.ErrConflict) {
				t.Errorf("a guarded %s after %s = %v, want a conflict", gname, ename, err)
			}
			got := beadAsk(t, s, ctx, id, seqs[0])
			if got.State != want.State || got.Option != want.Option || got.Words != want.Words || got.Turn != want.Turn || got.Turn != 1 {
				t.Errorf("a guarded %s after %s left %+v, want %+v", gname, ename, got, want)
			}
			if n := answersOn(t, s, ctx, id, seqs[0]); n != 1 {
				t.Errorf("a guarded %s after %s: %d answer notes, want 1", gname, ename, n)
			}
		}
	}
}

// Two clients that read the same open card and write at once: one settles,
// the other is refused as a conflict, and the ask holds one answer.
func TestGuardedAnswerRace(t *testing.T) {
	t.Parallel()
	s, ctx := stageAskYard(t, stageAskChoice)
	calls := []func(id string, seq uint64) error{
		func(id string, seq uint64) error {
			return s.AnswerAskExpected(ctx, id, seq, 0, 1, "", "mckean", false, 0)
		},
		func(id string, seq uint64) error { return s.AnswerAskExpected(ctx, id, seq, 0, 2, "", "jo", false, 0) },
		func(id string, seq uint64) error { return s.DeferAskExpected(ctx, id, seq, 0, "mckean") },
		func(id string, seq uint64) error {
			return s.DeclineAskExpected(ctx, id, seq, 0, "not mine", "mckean", 0)
		},
	}
	for i := range calls {
		for j := range calls {
			if i == j {
				continue
			}
			id, seqs := turnBead(t, s, ctx, "Move this yard's links now?")
			errs := make([]error, 2)
			var wg sync.WaitGroup
			for k, call := range []func(string, uint64) error{calls[i], calls[j]} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					errs[k] = call(id, seqs[0])
				}()
			}
			wg.Wait()
			settled := 0
			for _, err := range errs {
				switch {
				case err == nil:
					settled++
				case !errors.Is(err, store.ErrConflict):
					t.Errorf("calls %d and %d: %v, want nil or a conflict", i, j, err)
				}
			}
			if settled != 1 {
				t.Errorf("calls %d and %d: %d settled (%v), want 1", i, j, settled, errs)
			}
			if got := beadAsk(t, s, ctx, id, seqs[0]); got.Turn != 1 || answersOn(t, s, ctx, id, seqs[0]) != 1 {
				t.Errorf("calls %d and %d left turn %d, want one answer", i, j, got.Turn)
			}
		}
	}
}

// The turn read is the turn written to: a pick drawn from the answered card
// changes it knowingly, a put-aside card is answered from its own turn and
// stays at it when it comes back, a retry of a write that landed is refused
// rather than written twice, and the queue serves the turn as Bead does.
func TestGuardedAnswerTurns(t *testing.T) {
	t.Parallel()
	s, ctx := stageAskYard(t, stageAskChoice)
	id, seqs := turnBead(t, s, ctx, "Move this yard's links now?", "Close sketch PR #3?", "Send them to build?")
	pick, aside, decline := seqs[0], seqs[1], seqs[2]

	if err := s.AnswerAskExpected(ctx, id, pick, 0, 2, "after friday", "mckean", false, 0); err != nil {
		t.Fatal(err)
	}
	// The reply was lost: the client sends it again, and the retry is
	// refused, so the pick is written once.
	if err := s.AnswerAskExpected(ctx, id, pick, 0, 2, "after friday", "mckean", false, 0); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("the retried pick = %v, want a conflict", err)
	}
	if n := answersOn(t, s, ctx, id, pick); n != 1 {
		t.Fatalf("answer notes after the retry = %d, want 1", n)
	}
	read := beadAsk(t, s, ctx, id, pick)
	if err := s.AnswerAskExpected(ctx, id, pick, read.Turn, 1, "", "mckean", false, 0); err != nil {
		t.Fatalf("a pick drawn from the answered card = %v", err)
	}
	if got := beadAsk(t, s, ctx, id, pick); got.Option != 1 || got.PicksBefore != 1 || got.Turn != 2 {
		t.Fatalf("the informed repick = %+v", got)
	}

	if err := s.DeferAskExpected(ctx, id, aside, 0, "mckean"); err != nil {
		t.Fatal(err)
	}
	read = beadAsk(t, s, ctx, id, aside)
	if read.State != "put aside" || read.Turn != 1 {
		t.Fatalf("the put-aside card = %s at turn %d", read.State, read.Turn)
	}
	// The asker writes on the bead: the card is back, open, at the turn of
	// its put-aside, so a read from before it is stale still.
	if err := s.AddNote(ctx, id, "the release moved", "yardmaster"); err != nil {
		t.Fatal(err)
	}
	if got := beadAsk(t, s, ctx, id, aside); got.State != "open" || got.Turn != 1 {
		t.Fatalf("the card come back = %s at turn %d, want open at 1", got.State, got.Turn)
	}
	q, err := s.UserAsks(ctx, "mckean", "mckean")
	if err != nil {
		t.Fatal(err)
	}
	turns := map[uint64]int{}
	for _, o := range q.Open {
		turns[o.Ask] = o.Turn
	}
	for _, d := range q.Decided {
		turns[d.Ask] = d.Turn + 100
	}
	if turns[aside] != 1 || turns[decline] != 0 || turns[pick] != 102 {
		t.Fatalf("the queue's turns (decided +100) = %v, want aside open at 1, decline open at 0, pick decided at 2", turns)
	}
	if err := s.AnswerAskExpected(ctx, id, aside, 0, 1, "", "mckean", false, 0); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("a pick drawn from before the put-aside = %v, want a conflict", err)
	}
	if err := s.AnswerAskExpected(ctx, id, aside, 1, 1, "", "mckean", false, 0); err != nil {
		t.Fatalf("a pick drawn from the card come back = %v", err)
	}

	if err := s.DeclineAskExpected(ctx, id, decline, 0, "not mine", "mckean", 0); err != nil {
		t.Fatal(err)
	}
	if got := beadAsk(t, s, ctx, id, decline); got.State != "declined" || got.Words != "not mine" || got.Turn != 1 {
		t.Fatalf("the guarded decline = %+v", got)
	}
}

// The turn adds a check and takes none away: who may answer, what the ask
// takes and whether it is asked still are refused as the unguarded calls
// refuse them, writing nothing; a bead of another yard takes no turn; and a
// turn is never negative. Who may answer is asked first, so a caller the
// policy turns away is refused in its words whatever turn it read, and is
// told neither the ask's turn nor the bead's revision: by the core's rule of
// an ask's addressee (ada), by a policy that turns an operator away (jo), and
// by one that hides the bead from a caller it lets answer (kim). A caller who
// may answer is told, ahead of the refusals of an ask that is taken back.
func TestGuardedAnswerKeepsTheRules(t *testing.T) {
	t.Parallel()
	s, own, ctx := stageAskYardOwned(t, stageAskChoice)
	own.SetPolicy(policyFunc(func(ctx context.Context, tx *bolt.Tx, actor string, act store.Action, obj store.Object) error {
		switch {
		case actor == "jo" && act == store.ActAnswerAsk:
			return errors.New("the policy turns jo away")
		case actor == "kim" && act == store.ActSee:
			return errors.New("the policy hides the bead from kim")
		case actor == "kim" && act == store.ActAnswerAsk:
			return nil
		}
		return store.CorePolicy.May(ctx, tx, actor, act, obj)
	}))
	id, seqs := turnBead(t, s, ctx, "Move this yard's links now?", "Close sketch PR #3?", "Send them to build?")
	ask, back, moved := seqs[0], seqs[1], seqs[2]
	if err := s.TakeBackAsk(ctx, id, back, "asked no more", "yardmaster"); err != nil {
		t.Fatal(err)
	}
	// The third ask took an answer after the clients below read it at turn 0.
	if err := s.AnswerAsk(ctx, id, moved, 2, "external answer", "mckean", false); err != nil {
		t.Fatal(err)
	}
	bad := []struct {
		name string
		call func() error
		want string
	}{
		{"another user's pick", func() error { return s.AnswerAskExpected(ctx, id, ask, 0, 1, "", "ada", false, 0) }, "mckean"},
		{"another user's put-aside", func() error { return s.DeferAskExpected(ctx, id, ask, 0, "ada") }, "mckean"},
		{"another user's decline", func() error { return s.DeclineAskExpected(ctx, id, ask, 0, "no", "ada", 0) }, "mckean"},
		{"words alone on options", func() error { return s.AnswerAskExpected(ctx, id, ask, 0, 0, "a thought", "mckean", false, 0) }, "leave the ask open"},
		{"an option it has not", func() error { return s.AnswerAskExpected(ctx, id, ask, 0, 9, "", "mckean", false, 0) }, "has options 1..3"},
		{"a decline without words", func() error { return s.DeclineAskExpected(ctx, id, ask, 0, "", "mckean", 0) }, "needs words"},
		{"an ask taken back, at its turn", func() error { return s.AnswerAskExpected(ctx, id, back, 1, 1, "", "mckean", false, 0) }, "asked no more"},
		{"no such ask", func() error { return s.AnswerAskExpected(ctx, id, 999999, 0, 1, "", "mckean", false, 0) }, "has no ask"},
		{"another yard's bead", func() error {
			return s.AnswerAskExpected(ctx, "yd-ledger@north", 9, 0, 1, "", "mckean", false, 0)
		}, "another yard's bead"},
		{"a negative turn", func() error { return s.DeferAskExpected(ctx, id, ask, -1, "mckean") }, "from 0"},
		{"another user's stale pick", func() error { return s.AnswerAskExpected(ctx, id, moved, 0, 1, "", "ada", false, 0) }, "mckean"},
		{"another user's stale put-aside", func() error { return s.DeferAskExpected(ctx, id, moved, 0, "ada") }, "mckean"},
		{"another user's stale decline", func() error { return s.DeclineAskExpected(ctx, id, moved, 0, "no", "ada", 0) }, "mckean"},
		{"another user's stale revision", func() error {
			return s.AddAnswerExpected(ctx, id, store.Answer{Ask: moved, Option: 1}, "ada", "01STALE")
		}, "mckean"},
		{"a stale pick by the operator a policy turns away", func() error { return s.AnswerAskExpected(ctx, id, moved, 0, 1, "", "jo", false, 0) }, "turns jo away"},
		{"a stale pick on a bead hidden from the caller", func() error { return s.AnswerAskExpected(ctx, id, moved, 0, 1, "", "kim", false, 0) }, "not found"},
		{"a stale revision on a bead hidden from the caller", func() error {
			return s.AddAnswerExpected(ctx, id, store.Answer{Ask: moved, Option: 1}, "kim", "01STALE")
		}, "not found"},
		// An ask that is none takes no guard, so no caller is told the
		// revision by naming one.
		{"a stale revision on no ask", func() error {
			return s.AddAnswerExpected(ctx, id, store.Answer{Ask: 999999, Option: 1}, "mckean", "01STALE")
		}, "has no ask"},
	}
	for _, c := range bad {
		err := c.call()
		if err == nil || errors.Is(err, store.ErrConflict) || !strings.Contains(err.Error(), c.want) ||
			strings.Contains(err.Error(), "at turn") || strings.Contains(err.Error(), "at revision") {
			t.Errorf("%s = %v, want a refusal saying %q and no turn or revision", c.name, err, c.want)
		}
	}
	if got := beadAsk(t, s, ctx, id, ask); got.State != "open" || got.Turn != 0 {
		t.Fatalf("after the refusals the ask is %s at turn %d, want open at 0", got.State, got.Turn)
	}
	if got := beadAsk(t, s, ctx, id, moved); got.State != "answered" || got.Option != 2 || got.Turn != 1 {
		t.Fatalf("after the stale reads the third ask is %s with option %d at turn %d, want the external answer alone at 1", got.State, got.Option, got.Turn)
	}

	// The addressee is told the same reads are stale, and one drawn from
	// before the ask was taken back is a conflict, not what it is refused for.
	for name, call := range map[string]func() error{
		"a stale pick": func() error { return s.AnswerAskExpected(ctx, id, moved, 0, 1, "", "mckean", false, 0) },
		"a stale revision": func() error {
			return s.AddAnswerExpected(ctx, id, store.Answer{Ask: moved, Option: 1}, "mckean", "01STALE")
		},
		"a pick from before the ask was taken back": func() error { return s.AnswerAskExpected(ctx, id, back, 0, 1, "", "mckean", false, 0) },
	} {
		if err := call(); !errors.Is(err, store.ErrConflict) {
			t.Errorf("%s by the addressee = %v, want a conflict", name, err)
		}
	}
}

// A card the yard put for a stage takes a guarded pick as it takes any: the
// pick moves the bead in its write, and a pick drawn from the same read by
// another member is refused, so the bead moves once. Leaving the stage
// takes the other cards back, so their reads are stale too. A stale read
// from one who is no member is refused as the group's rule refuses them, and
// names no turn.
func TestGuardedAnswerOnAStageAsk(t *testing.T) {
	t.Parallel()
	s, ctx := stageAskYard(t, stageAskChoice)
	b, err := s.CreateBead(ctx, store.NewBead{Title: "x", Depot: "web", Stage: "new"}, "me")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(ctx, b.ID, "review", store.Expect{}, "me"); err != nil {
		t.Fatal(err)
	}
	cards := map[string]store.BeadAsk{}
	v, err := s.Bead(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range v.Asks {
		cards[a.To] = a
	}
	ada, mckean := cards["ada"], cards["mckean"]
	if ada.Turn != 0 || mckean.Turn != 0 {
		t.Fatalf("the stage cards = %+v", cards)
	}
	// ada puts mckean's card aside, so that a read of it at turn 0 is stale
	// while the bead is in its stage still.
	if err := s.DeferAsk(ctx, b.ID, mckean.Ask, "ada"); err != nil {
		t.Fatal(err)
	}
	err = s.AnswerAskExpected(ctx, b.ID, mckean.Ask, mckean.Turn, 1, "", "kim", false, 0)
	if err == nil || errors.Is(err, store.ErrConflict) || !strings.Contains(err.Error(), "only members of group reviewers") || strings.Contains(err.Error(), "at turn") {
		t.Fatalf("kim, no member, from a stale read = %v, want the group's refusal and no turn", err)
	}
	if err := s.AnswerAskExpected(ctx, b.ID, mckean.Ask, mckean.Turn, 1, "", "mckean", false, 0); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("mckean from the same stale read = %v, want a conflict", err)
	}
	if err := s.AnswerAskExpected(ctx, b.ID, ada.Ask, ada.Turn, 2, "", "ada", false, 0); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetBead(ctx, b.ID); err != nil || got.Stage != "new" {
		t.Fatalf("after ada's changes the bead is %+v, %v, want in new", got, err)
	}
	// mckean's card went with the stay: taken back by the yard, its turn
	// moved, and the pick drawn from the read before is refused.
	if err := s.AnswerAskExpected(ctx, b.ID, ada.Ask, 0, 1, "", "ada", false, 0); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("ada's second pick from the same read = %v, want a conflict", err)
	}
	if err := s.AnswerAskExpected(ctx, b.ID, mckean.Ask, mckean.Turn, 1, "", "mckean", false, 0); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("mckean's pick from before the move = %v, want a conflict", err)
	}
	if got, err := s.GetBead(ctx, b.ID); err != nil || got.Stage != "new" {
		t.Fatalf("after the refused picks the bead is %+v, %v, want in new still", got, err)
	}
	if n := answersOn(t, s, ctx, b.ID, ada.Ask); n != 1 {
		t.Fatalf("answer notes on ada's card = %d, want 1", n)
	}
}
