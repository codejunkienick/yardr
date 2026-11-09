package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
)

// moveState is a bead as a refused MoveBead must leave it: its stage,
// revision and notes, and the yard's head, which no event may move.
type moveState struct {
	head     uint64
	stage    string
	revision string
	status   string
	notes    int
}

func beadState(t *testing.T, s store.API, ctx context.Context, id string) moveState {
	t.Helper()
	b, err := s.GetBead(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	notes, err := s.Notes(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	head, err := s.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return moveState{head: head, stage: b.Stage, revision: b.Revision, status: b.Status, notes: len(notes)}
}

// refusedMove makes the move, wants it refused saying want, and wants the
// bead and the yard exactly as they were: a refused move writes nothing,
// not even its note.
func refusedMove(t *testing.T, s store.API, ctx context.Context, id, outcome, to, note, revision, actor, want string) {
	t.Helper()
	before := beadState(t, s, ctx, id)
	if _, err := s.MoveBead(ctx, id, outcome, to, note, revision, actor); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("MoveBead(%s, outcome %q, to %q, actor %s) = %v, want a refusal saying %q", id, outcome, to, actor, err, want)
	}
	if after := beadState(t, s, ctx, id); after != before {
		t.Errorf("a refused MoveBead of %s left %+v, want %+v: nothing is written", id, after, before)
	}
}

// waitingRevision is the revision of id's row in who's queue: the revision
// a client that read the queue moves from.
func waitingRevision(t *testing.T, s store.API, ctx context.Context, id, who string) string {
	t.Helper()
	rows, err := s.Waiting(ctx, who, who)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Bead == id {
			return r.Revision
		}
	}
	t.Fatalf("%s has no row for %s in %v", who, id, rows)
	return ""
}

// MoveBead makes the moves Waiting offers: a done move with its note in
// the same write, an advance move, and a move into a terminal stage,
// which closes the bead and says so.
func TestMoveBeadMakesAnOfferedMove(t *testing.T) {
	t.Parallel()
	s, _, ctx := parkedYard(t)

	rev := waitingRevision(t, s, ctx, "pk-ready", "ada")
	moved, err := s.MoveBead(ctx, "pk-ready", "done", "", "the proofs are in", rev, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if *moved != (store.MovedBead{Bead: "pk-ready", Stage: "review"}) {
		t.Errorf("the done move answered %+v, want pk-ready in review, not closed", moved)
	}
	b, err := s.GetBead(ctx, "pk-ready")
	if err != nil || b.Stage != "review" {
		t.Fatalf("pk-ready stands at %+v, %v, want in review", b, err)
	}
	notes, err := s.Notes(ctx, "pk-ready")
	if err != nil || len(notes) == 0 || notes[len(notes)-1].Text != "the proofs are in" {
		t.Errorf("pk-ready's notes are %v, %v, want the move's note last", notes, err)
	}

	// An advance move: siding names no outcome, so its stages are offered.
	rev = waitingRevision(t, s, ctx, "pk-siding", "hal")
	moved, err = s.MoveBead(ctx, "pk-siding", "", "ready", "", rev, "hal")
	if err != nil {
		t.Fatal(err)
	}
	if *moved != (store.MovedBead{Bead: "pk-siding", Stage: "ready"}) {
		t.Errorf("the advance answered %+v, want pk-siding in ready", moved)
	}

	// Entering a terminal stage closes the bead, and the answer says so.
	rev = waitingRevision(t, s, ctx, "pk-review", "ada")
	moved, err = s.MoveBead(ctx, "pk-review", "done", "", "", rev, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if *moved != (store.MovedBead{Bead: "pk-review", Stage: "merged", Closed: true}) {
		t.Errorf("the terminal move answered %+v, want pk-review in merged, closed", moved)
	}
	if b, err = s.GetBead(ctx, "pk-review"); err != nil || b.Status != "closed" || b.Stage != "merged" {
		t.Errorf("pk-review stands at %+v, %v, want closed in merged", b, err)
	}
}

// A move back needs its note in the same call; without one nothing is
// written, with one the note stands on the bead in its new stage.
func TestMoveBeadAMoveBackNeedsItsNote(t *testing.T) {
	t.Parallel()
	s, _, ctx := parkedYard(t)
	rev := waitingRevision(t, s, ctx, "pk-review", "ada")
	refusedMove(t, s, ctx, "pk-review", "changes", "", "", rev, "ada", "sending back to ready needs a note saying what should change")
	moved, err := s.MoveBead(ctx, "pk-review", "changes", "", "page 2 fails the check", rev, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if *moved != (store.MovedBead{Bead: "pk-review", Stage: "ready"}) {
		t.Errorf("the move back answered %+v, want pk-review in ready", moved)
	}
	notes, err := s.Notes(ctx, "pk-review")
	if err != nil || len(notes) == 0 || notes[len(notes)-1].Text != "page 2 fails the check" {
		t.Errorf("pk-review's notes are %v, %v, want the move's note last", notes, err)
	}
}

// Each refusal of the call, beside the moves made above: the move's shape,
// a stale revision, a move not offered to the actor, a bound non-member,
// and a bead whose flow does not resolve.
func TestMoveBeadRefusalsWriteNothing(t *testing.T) {
	t.Parallel()
	s, _, ctx := parkedYard(t)
	rev := waitingRevision(t, s, ctx, "pk-ready", "ada")

	// Exactly one of outcome and to names the move.
	refusedMove(t, s, ctx, "pk-ready", "done", "review", "", rev, "ada", "a move names an outcome or a stage, not both")
	refusedMove(t, s, ctx, "pk-ready", "", "", "", rev, "ada", "a move names an outcome or a stage")

	// A revision the bead has moved past, and none at all, are stale.
	if _, err := s.MoveBead(ctx, "pk-ready", "done", "", "", "a-revision-that-was", "ada"); !errors.Is(err, store.ErrConflict) ||
		!strings.Contains(err.Error(), "is at revision "+rev+", not a-revision-that-was; read it again") {
		t.Errorf("a stale move is refused %v, want a conflict naming the revision", err)
	}
	refusedMove(t, s, ctx, "pk-ready", "done", "", "", "", "ada", "is at revision "+rev+", not none; read it again")

	// A stage a done move enters is no advance move, and an outcome the
	// flow does not name is no done move: the refusal names what is offered.
	refusedMove(t, s, ctx, "pk-ready", "", "review", "", rev, "ada", "bead pk-ready does not wait for ada with that move; it waits with done: move to review")
	refusedMove(t, s, ctx, "pk-ready", "bogus", "", "", rev, "ada", "bead pk-ready does not wait for ada with that move")

	// hal is one of people but not of solo, which works review: a valid
	// move of pk-review, at its revision, is not offered to him.
	review, err := s.GetBead(ctx, "pk-review")
	if err != nil {
		t.Fatal(err)
	}
	refusedMove(t, s, ctx, "pk-review", "done", "", "", review.Revision, "hal", "bead pk-review does not wait for hal; read it again")

	// No such bead is not found, as every bead call answers it.
	if _, err := s.MoveBead(ctx, "pk-nope", "done", "", "", rev, "ada"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a move of no bead is refused %v, want not found", err)
	}
}

// MoveBead follows Waiting for what stands in the queue at all: the
// backlog, a held bead, a stranded bead, an asked bead and one the actor
// answered with another member left offer the actor no move, though
// Done and AdvanceNote refuse none of them for the hold or the stage.
func TestMoveBeadFollowsWaiting(t *testing.T) {
	t.Parallel()
	s, _, ctx := parkedYard(t)
	at := func(id string) string {
		t.Helper()
		b, err := s.GetBead(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return b.Revision
	}

	// The backlog is where beads start: shaped, not moved from the queue.
	refusedMove(t, s, ctx, "pk-backlog", "", "siding", "", at("pk-backlog"), "ada", "bead pk-backlog does not wait for ada")

	// A held bead stands nowhere in the queue until the hold is lifted,
	// at whatever revision the hold left it.
	if _, err := s.SetHold(ctx, "pk-ready", true, store.Expect{}, "ada"); err != nil {
		t.Fatal(err)
	}
	refusedMove(t, s, ctx, "pk-ready", "done", "", "", at("pk-ready"), "ada", "bead pk-ready does not wait for ada")
	if _, err := s.SetHold(ctx, "pk-ready", false, store.Expect{}, "ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MoveBead(ctx, "pk-ready", "done", "", "", at("pk-ready"), "ada"); err != nil {
		t.Errorf("pk-ready released and read again does not move: %v", err)
	}
	// (pk-ready stands in review from here.)

	// An open ask to ada on pk-siding is hers to answer, not a move. Once
	// she has answered it she moves the bead herself: crowd lists nobody,
	// so no other member is left for the answered rule to leave it to.
	seq, err := s.AddAsk(ctx, "pk-siding", store.Ask{Question: "Is it ready to go?", To: "ada", Context: []string{"the small ones are tidied."},
		Options: []store.AskOption{{Label: "Yes", Consequence: "it goes"}}}, "hal")
	if err != nil {
		t.Fatal(err)
	}
	refusedMove(t, s, ctx, "pk-siding", "", "ready", "", at("pk-siding"), "ada", "bead pk-siding does not wait for ada")
	if err := s.AddAnswer(ctx, "pk-siding", store.Answer{Ask: seq, Option: 1}, "ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MoveBead(ctx, "pk-siding", "", "ready", "", at("pk-siding"), "ada"); err != nil {
		t.Errorf("pk-siding after its ask is answered does not move for ada: %v", err)
	}

	// In a group of several the answered rule leaves the bead to the other
	// member: hal asks ada on a bead in ready, ada answers, and the move
	// is hal's, not ada's.
	if _, err := s.CreateBead(ctx, store.NewBead{ID: "pk-extra", Title: "One more", Depot: "shop", Stage: "ready"}, "ada"); err != nil {
		t.Fatal(err)
	}
	seq, err = s.AddAsk(ctx, "pk-extra", store.Ask{Question: "Is it ready to go?", To: "ada", Context: []string{"one more proof."},
		Options: []store.AskOption{{Label: "Yes", Consequence: "it goes"}}}, "hal")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddAnswer(ctx, "pk-extra", store.Answer{Ask: seq, Option: 1}, "ada"); err != nil {
		t.Fatal(err)
	}
	refusedMove(t, s, ctx, "pk-extra", "done", "", "", at("pk-extra"), "ada", "bead pk-extra does not wait for ada")
	if _, err := s.MoveBead(ctx, "pk-extra", "done", "", "", at("pk-extra"), "hal"); err != nil {
		t.Errorf("pk-extra does not move for hal, the member left: %v", err)
	}
}

// A bead stranded in a stage its flow no longer has is a fault, not a
// move; and a bead whose depot's checkout cannot be read stands in the
// queue with the reason and no move, and moving it says why. Neither
// refusal writes anything.
func TestMoveBeadStrandedAndBrokenFlow(t *testing.T) {
	t.Parallel()
	dir := gitRepo(t, map[string]string{
		".yardr/flows/default.flow": "backlog -> ready\nready -> old | merged\nold -> ready\nmerged terminal\n",
	})
	s, _ := storetest.NewOwned(t)
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
	for _, stage := range []string{"backlog", "ready", "old"} {
		_, err := s.AddRoute(ctx, store.Route{Stage: stage, Group: "people"}, "ada")
		must(err)
	}
	for _, nb := range []store.NewBead{
		{ID: "pk-old", Title: "Old", Depot: "shop", Stage: "old"},
		{ID: "pk-ready", Title: "Ready", Depot: "shop", Stage: "ready"},
	} {
		_, err := s.CreateBead(ctx, nb, "ada")
		must(err)
	}

	// The repository's flow moves on without old: pk-old is stranded in
	// a stage it lacks, and offers ada no move, to its first stage or
	// anywhere else.
	commitFiles(t, dir, map[string]string{
		".yardr/flows/default.flow": "backlog -> ready\nready -> merged\nmerged terminal\n",
	})
	b, err := s.GetBead(ctx, "pk-old")
	must(err)
	refusedMove(t, s, ctx, "pk-old", "", "backlog", "", b.Revision, "ada", "bead pk-old does not wait for ada")

	// With the checkout unreadable, pk-ready's flow is unknown: its row
	// carries the reason, and a move of it says why.
	depot, err := s.GetDepot(ctx, "shop")
	must(err)
	depot.Path = t.TempDir()
	must(s.PutDepot(ctx, *depot, "ada"))
	b, err = s.GetBead(ctx, "pk-ready")
	must(err)
	refusedMove(t, s, ctx, "pk-ready", "done", "", "", b.Revision, "ada", "no move is allowed until the flow is fixed")
}

// A bead the actor may not see stands in no queue of theirs, and no move
// of it is offered to them, member or not: it is not found, as Bead
// answers it, and a stale revision does not draw its revision out.
func TestMoveBeadAHiddenBeadIsNotOffered(t *testing.T) {
	t.Parallel()
	s, own, ctx := parkedYard(t)
	own.SetPolicy(policyFunc(func(ctx context.Context, tx *bolt.Tx, actor string, act store.Action, obj store.Object) error {
		if act == store.ActSee && obj.Kind == store.KindBead && obj.Name == "pk-ready" && actor == "hal" {
			return errors.New("hidden from hal")
		}
		return store.CorePolicy.May(ctx, tx, actor, act, obj)
	}))
	b, err := s.GetBead(ctx, "pk-ready")
	if err != nil {
		t.Fatal(err)
	}
	refusedMove(t, s, ctx, "pk-ready", "done", "", "", b.Revision, "hal", "bead pk-ready: not found")
	before := beadState(t, s, ctx, "pk-ready")
	_, err = s.MoveBead(ctx, "pk-ready", "done", "", "", "stale", "hal")
	if !errors.Is(err, store.ErrNotFound) || strings.Contains(err.Error(), b.Revision) {
		t.Errorf("MoveBead of a bead hidden from hal, from a stale revision = %v, want not found, naming no revision", err)
	}
	if after := beadState(t, s, ctx, "pk-ready"); after != before {
		t.Errorf("a refused MoveBead of pk-ready left %+v, want %+v", after, before)
	}
	if _, err := s.MoveBead(ctx, "pk-ready", "done", "", "", b.Revision, "ada"); err != nil {
		t.Errorf("pk-ready does not move for ada, who sees it: %v", err)
	}
}
