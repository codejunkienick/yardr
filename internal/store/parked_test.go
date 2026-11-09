package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
)

// parkedFlow: building is an agent's; ready ends by the outcome done, and
// review by a finish and a send back; siding has two ways out and names no
// outcome, so it offers its moves.
const parkedFlow = `
backlog  -> building | siding
siding   -> ready | review
building -> ready on done
ready    -> review on done | backlog
review   -> merged on done | ready on changes
merged terminal
`

// parkedYard is a yard whose depot shop follows parkedFlow. ada, hal and eve
// are its users, ada the operator. The manual group people (ada and hal)
// works backlog and ready, solo (ada alone) review, and crowd, which lists
// nobody, siding; an agent group works building. One bead stands in each
// stage, made in this order, oldest first, and a crew journal, which is no
// work, in ready after them: it never stands in a queue.
func parkedYard(t *testing.T) (store.API, *store.Store, context.Context) {
	t.Helper()
	s, own := storetest.NewOwned(t)
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.AddUser(ctx, "ada", "ada")
	must(err)
	must(s.SetSetting(ctx, store.OperatorSetting, "ada", "ada"))
	for _, u := range []string{"hal", "eve"} {
		_, err := s.AddUser(ctx, u, "ada")
		must(err)
	}
	must(s.PutDepot(ctx, store.Depot{Name: "shop", Base: "origin/main"}, "ada"))
	putFlow(t, s, "shop", "", parkedFlow)
	must(s.PutGroup(ctx, store.Group{Name: "builders", Runner: "herdr"}, "ada"))
	must(s.PutGroup(ctx, store.Group{Name: "people", Runner: "manual", Members: []string{"ada", "hal"}}, "ada"))
	must(s.PutGroup(ctx, store.Group{Name: "solo", Runner: "manual", Members: []string{"ada"}}, "ada"))
	must(s.PutGroup(ctx, store.Group{Name: "crowd", Runner: "manual"}, "ada"))
	for stage, group := range map[string]string{"backlog": "people", "building": "builders", "ready": "people", "review": "solo", "siding": "crowd"} {
		_, err := s.AddRoute(ctx, store.Route{Stage: stage, Group: group}, "ada")
		must(err)
	}
	for _, nb := range []store.NewBead{
		{ID: "pk-ready", Title: "Publish the autumn notes", Stage: "ready"},
		{ID: "pk-backlog", Title: "Sort the cupboard", Stage: "backlog"},
		{ID: "pk-building", Title: "Build the demo", Stage: "building"},
		{ID: "pk-siding", Title: "Tidy the small ones", Stage: "siding"},
		{ID: "pk-review", Title: "Check the tidied ones", Stage: "review"},
		{ID: "pk-journal", Title: "A crew journal", Type: store.TypeJournal, Stage: "ready"},
	} {
		nb.Depot = "shop"
		_, err := s.CreateBead(ctx, nb, "ada")
		must(err)
	}
	return s, own, ctx
}

// waitingIDs are the beads of the reader's queue, in its order.
func waitingIDs(t *testing.T, s store.API, ctx context.Context, name string, viewer ...string) []string {
	t.Helper()
	rows, err := s.Waiting(ctx, name, name, viewer...)
	if err != nil {
		t.Fatalf("Waiting(%s): %v", name, err)
	}
	ids := []string{}
	for _, r := range rows {
		ids = append(ids, r.Bead)
	}
	return ids
}

// sameBeads reports whether got and want name the same beads, in any order:
// a note or an ask changes a bead and with it the place it has in the queue.
func sameBeads(got, want []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want)))
}

// Waiting is the beads parked in a person's manual stages, and the moves
// they have: a bead past its flow's first stage, in a stage a manual group
// works, the person one of its members where it lists any, longest waiting
// first, each row read at the bead's revision.
func TestWaitingStandsTheParkedBeads(t *testing.T) {
	t.Parallel()
	s, _, ctx := parkedYard(t)

	// The backlog, where beads start, never stands, though people works it;
	// building is an agent's. ada is one of people and the one of solo.
	if got, want := waitingIDs(t, s, ctx, "ada"), []string{"pk-ready", "pk-siding", "pk-review"}; !slices.Equal(got, want) {
		t.Fatalf("ada's queue = %v, want %v", got, want)
	}
	// hal is no member of solo; eve is of neither, and a group that lists
	// nobody is anyone's.
	if got, want := waitingIDs(t, s, ctx, "hal"), []string{"pk-ready", "pk-siding"}; !slices.Equal(got, want) {
		t.Fatalf("hal's queue = %v, want %v", got, want)
	}
	if got, want := waitingIDs(t, s, ctx, "eve"), []string{"pk-siding"}; !slices.Equal(got, want) {
		t.Fatalf("eve's queue = %v, want %v", got, want)
	}

	// The row says where the bead stands and what to name in a move.
	rows, err := s.Waiting(ctx, "ada", "ada")
	if err != nil {
		t.Fatal(err)
	}
	ready, err := s.GetBead(ctx, "pk-ready")
	if err != nil {
		t.Fatal(err)
	}
	r := rows[0]
	if r.Bead != "pk-ready" || r.Title != "Publish the autumn notes" || r.Depot != "shop" || r.Stage != "ready" || r.Group != "people" ||
		!r.Since.Equal(ready.UpdatedAt) || r.Revision != ready.Revision || r.FlowError != "" || r.TemplateError != "" {
		t.Errorf("ada's first row = %+v, want pk-ready in ready, worked by people, since %v at revision %s", r, ready.UpdatedAt, ready.Revision)
	}
	if got, want := r.Actions, []store.Move{{Kind: store.MoveDone, Label: "done: move to review", Outcome: "done", To: "review"}}; !slices.Equal(got, want) {
		t.Errorf("ready's moves = %+v, want %+v", got, want)
	}
	// The labels are review's: a finish closes the bead, an earlier stage
	// needs a note, and a stage that names no outcome offers its moves.
	if got, want := rows[2].Actions, []store.Move{
		{Kind: store.MoveDone, Label: "done: finish as merged (closes the bead)", Outcome: "done", To: "merged", Terminal: true},
		{Kind: store.MoveDone, Label: "changes: send back to ready (note required)", Outcome: "changes", To: "ready", NoteRequired: true},
	}; !slices.Equal(got, want) {
		t.Errorf("review's moves = %+v, want %+v", got, want)
	}
	if got, want := rows[1].Actions, []store.Move{
		{Kind: store.MoveAdvance, Label: "Move to ready", To: "ready"},
		{Kind: store.MoveAdvance, Label: "Move to review", To: "review"},
	}; !slices.Equal(got, want) {
		t.Errorf("siding's moves = %+v, want %+v", got, want)
	}

	// Longest waiting first: a change to the oldest bead puts it last, and
	// its row is read at the revision the change made.
	if err := s.AddNote(ctx, "pk-ready", "checked the proofs", "hal"); err != nil {
		t.Fatal(err)
	}
	if got, want := waitingIDs(t, s, ctx, "ada"), []string{"pk-siding", "pk-review", "pk-ready"}; !slices.Equal(got, want) {
		t.Errorf("ada's queue after a note = %v, want %v", got, want)
	}
	if ready, err = s.GetBead(ctx, "pk-ready"); err != nil {
		t.Fatal(err)
	}
	if rows, err = s.Waiting(ctx, "ada", "ada"); err != nil || rows[2].Revision != ready.Revision || !rows[2].Since.Equal(ready.UpdatedAt) {
		t.Errorf("the row after a note is %+v (%v), want revision %s since %v", rows[2], err, ready.Revision, ready.UpdatedAt)
	}

	// A bead leaves the queue as it leaves its stage, or ends.
	if _, err := s.AdvanceNote(ctx, "pk-review", "merged", "", store.Expect{}, "ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdvanceNote(ctx, "pk-siding", "ready", "", store.Expect{}, "ada"); err != nil {
		t.Fatal(err)
	}
	if got := waitingIDs(t, s, ctx, "ada"); !sameBeads(got, []string{"pk-ready", "pk-siding"}) {
		t.Errorf("ada's queue after the moves = %v, want pk-ready and pk-siding, now both in ready", got)
	}
	if got := waitingIDs(t, s, ctx, "eve"); len(got) != 0 {
		t.Errorf("eve's queue after the siding bead moved on = %v, want none", got)
	}
}

// A bead no flow governs has no first stage to stand past, and offers a move
// to every other stage a route works, as review does.
func TestWaitingBeadWithNoFlow(t *testing.T) {
	t.Parallel()
	s, _, ctx := parkedYard(t)
	if _, err := s.CreateBead(ctx, store.NewBead{ID: "pk-free", Title: "No depot", Stage: "ready"}, "ada"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Waiting(ctx, "hal", "hal")
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(rows, func(r store.WaitingBead) bool { return r.Bead == "pk-free" })
	if i < 0 {
		t.Fatalf("hal's queue = %+v, want the bead no flow governs", rows)
	}
	var moves []string
	for _, m := range rows[i].Actions {
		moves = append(moves, m.Label)
	}
	if want := []string{"Move to backlog", "Move to building", "Move to review", "Move to siding"}; !slices.Equal(moves, want) || rows[i].Depot != "" || rows[i].FlowError != "" {
		t.Errorf("the free bead's row = %+v, want %v and no depot", rows[i], want)
	}
}

// A bead that carries an open ask to the person stands among their
// decisions and never twice. Where another member is left to move it, the
// person's own answer, decline or putting an ask aside takes it from their
// queue; an ask its asker took back is nobody's answer and takes it from no
// one's. A sole member still moves it themselves, and an ask put aside
// leaves it standing.
func TestWaitingAnAskedBeadStandsAmongDecisionsAlone(t *testing.T) {
	t.Parallel()
	s, _, ctx := parkedYard(t)
	ask := func(bead, to, asker string) uint64 {
		t.Helper()
		seq, err := s.AddAsk(ctx, bead, store.Ask{Question: "Is it ready to go?", To: to, Context: []string{"the proofs are in."},
			Options: []store.AskOption{{Label: "Yes", Consequence: "it goes"}}}, asker)
		if err != nil {
			t.Fatal(err)
		}
		return seq
	}
	answer := func(bead string, a store.Answer, who string) {
		t.Helper()
		if err := s.AddAnswer(ctx, bead, a, who); err != nil {
			t.Fatal(err)
		}
	}
	queue := func(who string, want ...string) {
		t.Helper()
		if got := waitingIDs(t, s, ctx, who); !sameBeads(got, want) {
			t.Errorf("%s's queue = %v, want %v", who, got, want)
		}
	}
	// ada has an ask on pk-ready: it is hers to answer, not a bead to move.
	// An ask to ada is no part of hal's.
	seq := ask("pk-ready", "ada", "hal")
	queue("ada", "pk-siding", "pk-review")
	queue("hal", "pk-ready", "pk-siding")
	// Answered, ada's part is done, and hal is the other member of people:
	// the bead is his to move, not hers.
	answer("pk-ready", store.Answer{Ask: seq, Option: 1}, "ada")
	queue("ada", "pk-siding", "pk-review")
	queue("hal", "pk-ready", "pk-siding")
	// solo lists ada alone, so no other member is left: an ask open on its
	// bead takes it from her queue, put aside it stands while the question
	// sleeps, and answered she moves it herself.
	seq = ask("pk-review", "ada", "hal")
	queue("ada", "pk-siding")
	answer("pk-review", store.Answer{Ask: seq, Later: true}, "ada")
	queue("ada", "pk-siding", "pk-review")
	// (Another asker: the one who put the first aside writing on the bead
	// again would bring it back.)
	seq = ask("pk-review", "ada", "eve")
	queue("ada", "pk-siding")
	answer("pk-review", store.Answer{Ask: seq, Option: 1}, "ada")
	queue("ada", "pk-siding", "pk-review")
	// What the person did with an ask decides whether their part is in.
	// Answering it, declining it and putting it aside are: ada is the member
	// left to move the bead. Its asker taking it back is nobody's answer,
	// though UserAsks lists it among the decided: hal never took part, and
	// the bead stays in his queue as it does in ada's.
	inQueue := func(who, bead string) bool {
		t.Helper()
		return slices.Contains(waitingIDs(t, s, ctx, who), bead)
	}
	for _, c := range []struct {
		way  string
		deal func(bead string, seq uint64)
		kept bool // the bead stays in hal's queue
	}{
		{"answered", func(bead string, seq uint64) { answer(bead, store.Answer{Ask: seq, Option: 1}, "hal") }, false},
		{"put aside", func(bead string, seq uint64) { answer(bead, store.Answer{Ask: seq, Later: true}, "hal") }, false},
		{"declined", func(bead string, seq uint64) {
			answer(bead, store.Answer{Ask: seq, Decline: true, Words: "not mine"}, "hal")
		}, false},
		{"taken back", func(bead string, seq uint64) {
			if err := s.TakeBackAsk(ctx, bead, seq, "asked in error", "eve"); err != nil {
				t.Fatal(err)
			}
		}, true},
	} {
		bead := "pk-" + strings.ReplaceAll(c.way, " ", "-")
		if _, err := s.CreateBead(ctx, store.NewBead{ID: bead, Title: "One " + c.way, Depot: "shop", Stage: "ready"}, "ada"); err != nil {
			t.Fatal(err)
		}
		seq := ask(bead, "hal", "eve")
		if inQueue("hal", bead) || !inQueue("ada", bead) {
			t.Errorf("%s: with the ask open, hal's queue holds it = %v and ada's = %v, want the first not and the second", c.way, inQueue("hal", bead), inQueue("ada", bead))
		}
		c.deal(bead, seq)
		if got := inQueue("hal", bead); got != c.kept {
			t.Errorf("%s: hal's queue holds the bead = %v, want %v", c.way, got, c.kept)
		}
		if !inQueue("ada", bead) {
			t.Errorf("%s: ada, who was asked nothing, lost the bead from her queue", c.way)
		}
	}
}

// A bead that leaves a templated stage takes the stage asks still open on it
// back as it moves, and that is nobody's answer: in the next stage, worked by
// the same group, it waits in every member's queue. The rule that counted a
// taken-back ask as the person's own left it in nobody's, though nobody had
// answered anything.
func TestWaitingStageAsksTakenBackOnMoveAreNobodysAnswer(t *testing.T) {
	t.Parallel()
	s, ctx := stageAskYard(t, stageAskChoice)
	if _, err := s.AddRoute(ctx, store.Route{Stage: "approved", Group: "reviewers"}, "jo"); err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateBead(ctx, store.NewBead{Title: "x", Depot: "web", Stage: "new"}, "me")
	if err != nil {
		t.Fatal(err)
	}
	// In review each member holds the stage ask, open: the bead is among
	// their decisions and in no queue.
	if b, err = s.Advance(ctx, b.ID, "review", store.Expect{}, "me"); err != nil {
		t.Fatal(err)
	}
	for _, who := range []string{"ada", "mckean"} {
		if got := waitingIDs(t, s, ctx, who); len(got) != 0 {
			t.Errorf("%s's queue in review = %v, want the bead among their decisions alone", who, got)
		}
	}
	// A hand move takes both asks back, the mover's own among them: neither
	// member answered.
	if b, err = s.Advance(ctx, b.ID, "approved", store.Expect{}, "ada"); err != nil {
		t.Fatal(err)
	}
	for _, r := range stageAsks(t, s, b.ID) {
		if r.State != store.AskTakenBack {
			t.Fatalf("the stage ask to %s is %s, want it taken back by the move", r.Ask.To, r.State)
		}
	}
	for _, who := range []string{"ada", "mckean"} {
		if got := waitingIDs(t, s, ctx, who); !slices.Equal(got, []string{b.ID}) {
			t.Errorf("%s's queue in approved = %v, want %s: nobody answered", who, got, b.ID)
		}
	}
}

// A held bead is a fault, to mend and not a move to make, and the web's
// waits keep it apart from the parked beads this call serves: a bead both
// parked and held stands nowhere here until it is released.
func TestWaitingAHeldBeadIsNotParked(t *testing.T) {
	t.Parallel()
	s, _, ctx := parkedYard(t)
	if _, err := s.SetHold(ctx, "pk-ready", true, store.Expect{}, "ada"); err != nil {
		t.Fatal(err)
	}
	if got := waitingIDs(t, s, ctx, "ada"); !sameBeads(got, []string{"pk-siding", "pk-review"}) {
		t.Fatalf("ada's queue with pk-ready held = %v, want pk-siding and pk-review", got)
	}
	if _, err := s.SetHold(ctx, "pk-ready", false, store.Expect{}, "ada"); err != nil {
		t.Fatal(err)
	}
	if got := waitingIDs(t, s, ctx, "ada"); !sameBeads(got, []string{"pk-ready", "pk-siding", "pk-review"}) {
		t.Errorf("ada's queue after the release = %v, want pk-ready back", got)
	}
}

// The viewer's sight bounds the queue, as it bounds every listing; the gate
// is the inbox's: the user reads their own, the operator anyone's.
func TestWaitingTheViewerAndTheGate(t *testing.T) {
	t.Parallel()
	s, own, ctx := parkedYard(t)
	own.SetPolicy(policyFunc(func(ctx context.Context, tx *bolt.Tx, actor string, act store.Action, obj store.Object) error {
		if act == store.ActSee && obj.Kind == store.KindBead && obj.Name == "pk-ready" && actor == "hal" {
			return errors.New("hidden from hal")
		}
		return store.CorePolicy.May(ctx, tx, actor, act, obj)
	}))
	if got := waitingIDs(t, s, ctx, "hal", "hal"); !sameBeads(got, []string{"pk-siding"}) {
		t.Errorf("hal's queue as hal sees it = %v, want pk-siding", got)
	}
	if got := waitingIDs(t, s, ctx, "hal"); !sameBeads(got, []string{"pk-ready", "pk-siding"}) {
		t.Errorf("hal's queue as the yard sees it = %v, want pk-ready and pk-siding", got)
	}
	// The operator reads hal's queue; hal does not read ada's; a name that
	// is no user's is refused.
	if _, err := s.Waiting(ctx, "hal", "ada"); err != nil {
		t.Errorf("the operator reading hal's queue: %v", err)
	}
	if _, err := s.Waiting(ctx, "ada", "hal"); err == nil || !strings.Contains(err.Error(), "ada's inbox") {
		t.Errorf("hal reading ada's queue = %v, want it refused", err)
	}
	if _, err := s.Waiting(ctx, "ghost", "ghost"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a queue for no user = %v, want not found", err)
	}
}

// Each depot's repository is read once for a call, however many of its
// beads wait, where NextStages for each would read it once a bead; and what
// the repository's flows make of a bead is told in its row: one in a stage
// its flow lacks is stranded, a fault, and is not there; one whose pinned
// flow the repository no longer carries stands with the reason and no
// move; and a stage whose ask template broke says so, where its ask would
// stand, and still moves by hand.
func TestWaitingReadsEachRepositoryOnce(t *testing.T) {
	t.Parallel()
	dir := gitRepo(t, map[string]string{
		".yardr/flows/default.flow": "backlog -> ready\nready -> old | merged\nold -> ready\nmerged terminal\n",
		".yardr/flows/alt.flow":     "backlog -> parked\nparked -> merged\nmerged terminal\n",
	})
	s, own := storetest.NewOwned(t)
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.AddUser(ctx, "ada", "ada")
	must(err)
	must(s.SetSetting(ctx, store.OperatorSetting, "ada", "ada"))
	must(s.PutDepot(ctx, store.Depot{Name: "shop", Path: dir, Base: "main"}, "ada"))
	must(s.PutGroup(ctx, store.Group{Name: "people", Runner: "manual", Members: []string{"ada"}}, "ada"))
	for _, stage := range []string{"backlog", "ready", "old", "parked"} {
		_, err := s.AddRoute(ctx, store.Route{Stage: stage, Group: "people"}, "ada")
		must(err)
	}
	const n = 5
	for i := range n {
		id := fmt.Sprintf("pk-r%d", i)
		_, err := s.CreateBead(ctx, store.NewBead{ID: id, Title: "ready " + id, Depot: "shop", Stage: "ready"}, "ada")
		must(err)
	}
	_, err = s.CreateBead(ctx, store.NewBead{ID: "pk-old", Title: "old", Depot: "shop", Stage: "old"}, "ada")
	must(err)
	_, err = s.CreateBead(ctx, store.NewBead{ID: "pk-pin", Title: "pinned", Depot: "shop", Stage: "parked",
		Metadata: map[string]any{"flow": "alt"}}, "ada")
	must(err)
	// One that waits in backlog and enters ready later, after the template
	// there breaks.
	_, err = s.CreateBead(ctx, store.NewBead{ID: "pk-new", Title: "new", Depot: "shop", Stage: "backlog"}, "ada")
	must(err)

	read := func() map[string]store.WaitingBead {
		t.Helper()
		before := own.RepoReads()
		rows, err := s.Waiting(ctx, "ada", "ada")
		if err != nil {
			t.Fatal(err)
		}
		if got := own.RepoReads() - before; got != 1 {
			t.Errorf("a queue of %d beads read the repository %d times, want once", len(rows), got)
		}
		byBead := map[string]store.WaitingBead{}
		for _, r := range rows {
			byBead[r.Bead] = r
		}
		return byBead
	}
	rows := read()
	if len(rows) != n+2 {
		t.Fatalf("the queue has %d beads, want the %d ready, the old and the pinned: %+v", len(rows), n, rows)
	}
	for _, r := range rows {
		if r.FlowError != "" || len(r.Actions) == 0 {
			t.Errorf("%s: flow error %q and %d moves, want its moves", r.Bead, r.FlowError, len(r.Actions))
		}
	}

	// The repository moves on: old is no stage of its flow, alt is gone, and
	// the ready template is broken for a bead that enters it now.
	must(os.Remove(filepath.Join(dir, ".yardr", "flows", "alt.flow")))
	commitFiles(t, dir, map[string]string{
		".yardr/flows/default.flow":     "backlog -> ready\nready -> merged\nmerged terminal\n",
		".yardr/flows/default/ready.md": "Ready.\n\n## Ask\nkind: chek\n",
	})
	_, err = s.AdvanceNote(ctx, "pk-new", "ready", "", store.Expect{}, "ada")
	must(err)
	rows = read()
	if _, stranded := rows["pk-old"]; stranded {
		t.Errorf("the queue holds pk-old, stranded in a stage its flow lacks: %+v", rows["pk-old"])
	}
	if pin := rows["pk-pin"]; !strings.Contains(pin.FlowError, "pins flow alt") || len(pin.Actions) != 0 || pin.Actions == nil {
		t.Errorf("the pinned bead's row = %+v, want its flow's error and an empty list of moves", pin)
	}
	if fresh := rows["pk-new"]; !strings.Contains(fresh.TemplateError, "ask line") || len(fresh.Actions) != 1 || fresh.Actions[0].Label != "done: finish as merged (closes the bead)" {
		t.Errorf("the bead that met the broken template = %+v, want the error and its move by hand", fresh)
	}
	if early := rows["pk-r0"]; early.TemplateError != "" || len(rows) != n+2 {
		t.Errorf("a bead that entered before the template broke = %+v in a queue of %d, want none of its error in a queue of %d", early, len(rows), n+2)
	}

	// A depot whose checkout cannot be read leaves its beads' flows unknown,
	// so none is known to be stranded or at its first stage: each stands with
	// the reason and no move, and the repository is still read once.
	depot, err := s.GetDepot(ctx, "shop")
	must(err)
	depot.Path = t.TempDir()
	must(s.PutDepot(ctx, *depot, "ada"))
	rows = read()
	if len(rows) != n+3 {
		t.Errorf("the queue has %d beads with the checkout unreadable, want all %d of the depot's", len(rows), n+3)
	}
	for _, r := range rows {
		if !strings.Contains(r.FlowError, "depot shop") || len(r.Actions) != 0 || r.Actions == nil {
			t.Errorf("%s with the checkout unreadable: flow error %q and moves %v, want the reason and an empty list", r.Bead, r.FlowError, r.Actions)
		}
	}
}
