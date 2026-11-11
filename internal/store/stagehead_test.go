package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
)

// stageHeadFlow is the shape of the contributors pack's pr triage: a pull
// request waits there for a maintainer, a push while it waits is a fresh stay
// (pushed), the yes goes to review and a close ends it.
const stageHeadFlow = "triage -> triage on pushed | review on accepted | closed on dropped\nreview -> merged\nmerged terminal\nclosed terminal\n"

// stageHeadTriage is a triage stage file whose card offers the yes to the
// head it shows (accepts:), as the pack's does.
const stageHeadTriage = `A pull request waits here for a maintainer.

## Ask
kind: choice
question: Accept this pull request at the head shown?
accepts: accepted
context:
- Nothing of it has been fetched, built or run.
accepted: The bead goes to review, with the head shown recorded as accepted_head.
dropped: The bead closes.
`

const (
	headA = "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d"
	headB = "9f8e7d6c5b4a39281706f5e4d3c2b1a09f8e7d6c"
)

// stageHeadYard is a depot governed by stageHeadFlow, the operator jo, users
// ada and mckean, and a manual group maintainers working triage.
func stageHeadYard(t *testing.T) (store.API, *store.Store, context.Context) {
	t.Helper()
	dir := gitRepo(t, map[string]string{
		".yardr/flows/default.flow":      stageHeadFlow,
		".yardr/flows/default/triage.md": stageHeadTriage,
	})
	s, own := storetest.NewOwned(t)
	ctx := context.Background()
	if err := s.PutDepot(ctx, store.Depot{Name: "web", Path: dir, Base: "main"}, "me"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, store.OperatorSetting, "jo", "jo"); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"ada", "mckean"} {
		if _, err := s.AddUser(ctx, u, "jo"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PutGroup(ctx, store.Group{Name: "maintainers", Runner: "manual", Members: []string{"ada", "mckean"}}, "jo"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRoute(ctx, store.Route{Stage: "triage", Group: "maintainers"}, "jo"); err != nil {
		t.Fatal(err)
	}
	return s, own, ctx
}

// headTold is what the dispatcher records of the delivery that told sha.
func headTold(sha string, at time.Time) map[string]any {
	return map[string]any{
		store.MetaPRHead: sha,
		store.MetaPRHeadTold: store.HeadTold{Sha: sha, At: at, By: "hal", Action: "synchronize",
			Size: &store.HeadSize{Additions: 120, Deletions: 30, Files: 4}},
	}
}

func stageHeadAccepted(t *testing.T, s store.API, ctx context.Context, id string) (string, bool) {
	t.Helper()
	b, err := s.GetBead(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	sha, ok := b.Metadata["accepted_head"].(string)
	return sha, ok
}

// A card in triage shows the head the bead tracks and what is recorded of it,
// and freezes it; the pick of the accepting outcome records that head as the
// bead's accepted_head in the write that moves it to review, and the other
// pick records none.
func TestStageHeadCardAndPick(t *testing.T) {
	t.Parallel()
	s, _, ctx := stageHeadYard(t)
	at := time.Date(2026, 11, 10, 9, 41, 0, 0, time.UTC)
	b, err := s.CreateBead(ctx, store.NewBead{Title: "a fix", Depot: "web", Stage: "triage", Metadata: headTold(headA, at)}, "me")
	if err != nil {
		t.Fatal(err)
	}
	got := stageAsks(t, s, b.ID)
	if len(got) != 2 {
		t.Fatalf("asks = %d, want one per maintainer", len(got))
	}
	a := got[0].Ask
	wantContext := []string{
		"Bead: " + b.ID + " — a fix (branch yard/" + b.ID + ")",
		"Head: 1a2b3c4, pushed by hal at 2026-11-10 09:41 UTC, +120 -30 in 4 files, as the delivery that told it said.",
		"Nothing of it has been fetched, built or run.",
	}
	if strings.Join(a.Context, "\n") != strings.Join(wantContext, "\n") {
		t.Errorf("context:\n%s\nwant:\n%s", strings.Join(a.Context, "\n"), strings.Join(wantContext, "\n"))
	}
	if a.Head == nil || a.Head.Sha != headA || a.Head.Accepts != "accepted" {
		t.Fatalf("head = %+v, want %s accepted by the pick of accepted", a.Head, headA)
	}
	if len(a.Options) != 2 || a.Options[0].Label != "accepted" || a.Options[1].Label != "dropped" {
		t.Fatalf("options = %+v: the card offers the yes and the close, and leaving is Later and Decline", a.Options)
	}

	// The pick records the head that was shown and moves the bead, in one
	// write: the move's own event names the head, too.
	if err := s.AddAnswer(ctx, b.ID, store.Answer{Ask: got[0].Note.Seq, Option: 1}, "ada"); err != nil {
		t.Fatal(err)
	}
	cur, err := s.GetBead(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cur.Stage != "review" {
		t.Fatalf("after the yes the bead is in %s, want review", cur.Stage)
	}
	if sha, _ := stageHeadAccepted(t, s, ctx, b.ID); sha != headA {
		t.Fatalf("accepted_head = %q, want the head shown %s", sha, headA)
	}
	evs, err := s.Events(ctx, b.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	var onEvent bool
	for _, e := range evs {
		if e.Kind == "advanced" && e.Data["to"] == "review" && e.Data["accepted_head"] == headA && e.Data["outcome"] == "accepted" {
			onEvent = true
		}
	}
	if !onEvent {
		t.Errorf("no advanced event into review names the outcome accepted and the head %s", headA)
	}

	// Closing takes no head: nothing is accepted and the bead ends.
	closing, err := s.CreateBead(ctx, store.NewBead{Title: "no thanks", Depot: "web", Stage: "triage", Metadata: headTold(headA, at)}, "me")
	if err != nil {
		t.Fatal(err)
	}
	asks := stageAsks(t, s, closing.ID)
	if err := s.AddAnswer(ctx, closing.ID, store.Answer{Ask: asks[0].Note.Seq, Option: 2}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if cur, err = s.GetBead(ctx, closing.ID); err != nil || cur.Stage != "closed" {
		t.Fatalf("after the close the bead is %+v, %v", cur, err)
	}
	if sha, ok := stageHeadAccepted(t, s, ctx, closing.ID); ok {
		t.Errorf("a close recorded accepted_head %q", sha)
	}
}

// A pick after the pull request has another head accepts nothing, and a card
// that showed no head cannot accept one: the answer is refused whole, the bead
// stays and the card stays open. Closing is no acceptance and still goes
// through.
func TestStageHeadStalePickAcceptsNothing(t *testing.T) {
	t.Parallel()
	s, _, ctx := stageHeadYard(t)
	at := time.Date(2026, 11, 10, 9, 41, 0, 0, time.UTC)
	b, err := s.CreateBead(ctx, store.NewBead{Title: "a fix", Depot: "web", Stage: "triage", Metadata: headTold(headA, at)}, "me")
	if err != nil {
		t.Fatal(err)
	}
	got := stageAsks(t, s, b.ID)
	// The author pushes after the card is put: the dispatcher records the
	// new head, and the card in Decide is of the old one.
	if _, err := s.UpdateBead(ctx, b.ID, store.BeadPatch{Metadata: headTold(headB, at.Add(time.Hour))}, store.Expect{}, "yard"); err != nil {
		t.Fatal(err)
	}
	before := stageAnswerNotes(t, s, b.ID)
	err = s.AddAnswer(ctx, b.ID, store.Answer{Ask: got[0].Note.Seq, Option: 1}, "ada")
	if err == nil || !strings.Contains(err.Error(), "showed the head 1a2b3c4, and the pull request has 9f8e7d6 now") ||
		!strings.Contains(err.Error(), "nothing was accepted") {
		t.Fatalf("the pick of a stale card: %v", err)
	}
	if cur, err := s.GetBead(ctx, b.ID); err != nil || cur.Stage != "triage" {
		t.Fatalf("after the refused pick the bead is %+v, %v", cur, err)
	}
	if sha, ok := stageHeadAccepted(t, s, ctx, b.ID); ok {
		t.Errorf("a stale pick recorded accepted_head %q", sha)
	}
	if after := stageAnswerNotes(t, s, b.ID); len(after) != len(before) {
		t.Errorf("a refused pick left %d notes, was %d: the answer must roll back", len(after), len(before))
	}
	if got := stageAsks(t, s, b.ID); got[0].State != store.AskOpen || got[1].State != store.AskOpen {
		t.Errorf("the cards = %s, %s, want both still open", got[0].State, got[1].State)
	}

	// A card put for a bead with no head says so and cannot accept one.
	bare, err := s.CreateBead(ctx, store.NewBead{Title: "unknown", Depot: "web", Stage: "triage"}, "me")
	if err != nil {
		t.Fatal(err)
	}
	asks := stageAsks(t, s, bare.ID)
	if !strings.HasPrefix(asks[0].Ask.Context[1], "Head: none told.") || asks[0].Ask.Head == nil || asks[0].Ask.Head.Sha != "" {
		t.Fatalf("a card with no head: context %q, head %+v", asks[0].Ask.Context, asks[0].Ask.Head)
	}
	// A head told after the card was put is no head the card showed.
	if _, err := s.UpdateBead(ctx, bare.ID, store.BeadPatch{Metadata: headTold(headB, at)}, store.Expect{}, "yard"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddAnswer(ctx, bare.ID, store.Answer{Ask: asks[0].Note.Seq, Option: 1}, "ada"); err == nil || !strings.Contains(err.Error(), "showed no head") {
		t.Fatalf("the pick of a card that showed no head: %v", err)
	}
	if cur, err := s.GetBead(ctx, bare.ID); err != nil || cur.Stage != "triage" {
		t.Fatalf("after the refused pick the bead is %+v, %v", cur, err)
	}
	if err := s.AddAnswer(ctx, bare.ID, store.Answer{Ask: asks[0].Note.Seq, Option: 2}, "ada"); err != nil {
		t.Fatalf("closing a card with no head: %v", err)
	}
}

// A push while the card waits is a fresh stay (the pack's pushed): the old card
// is taken back, a fresh one names the new head, and only it accepts: the pick
// of the old card is refused, and the pick of the new one records the new head.
func TestStageHeadPushMakesAFreshCard(t *testing.T) {
	t.Parallel()
	s, _, ctx := stageHeadYard(t)
	at := time.Date(2026, 11, 10, 9, 41, 0, 0, time.UTC)
	b, err := s.CreateBead(ctx, store.NewBead{Title: "a fix", Depot: "web", Stage: "triage", Metadata: headTold(headA, at)}, "me")
	if err != nil {
		t.Fatal(err)
	}
	old := stageAsks(t, s, b.ID)
	// The dispatcher records the head before it moves the bead, so that the
	// card the move puts names the head that brought the bead there.
	if _, err := s.UpdateBead(ctx, b.ID, store.BeadPatch{Metadata: headTold(headB, at.Add(time.Hour))}, store.Expect{}, "yard"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Done(ctx, b.ID, "pushed", "new commits on the PR", store.Expect{Stage: "triage"}, "ada"); err != nil {
		t.Fatal(err)
	}
	all := stageAsks(t, s, b.ID)
	var fresh []store.ResolvedAsk
	for _, r := range all {
		switch r.Ask.Head.Sha {
		case headA:
			if r.State != store.AskTakenBack {
				t.Errorf("the card of the old head is %s, want taken back", r.State)
			}
		case headB:
			fresh = append(fresh, r)
		}
	}
	if len(fresh) != 2 || fresh[0].State != store.AskOpen {
		t.Fatalf("fresh cards of the new head = %+v, want one open per maintainer", fresh)
	}
	if !strings.Contains(strings.Join(fresh[0].Ask.Context, "\n"), "Head: 9f8e7d6, pushed by hal at 2026-11-10 10:41 UTC") {
		t.Errorf("the fresh card reads %q", fresh[0].Ask.Context)
	}
	if err := s.AddAnswer(ctx, b.ID, store.Answer{Ask: old[0].Note.Seq, Option: 1}, "ada"); err == nil ||
		!strings.Contains(err.Error(), "is asked no more") {
		t.Fatalf("the pick of the old card: %v", err)
	}
	if sha, ok := stageHeadAccepted(t, s, ctx, b.ID); ok {
		t.Fatalf("the old card recorded accepted_head %q", sha)
	}
	if err := s.AddAnswer(ctx, b.ID, store.Answer{Ask: fresh[0].Note.Seq, Option: 1}, "ada"); err != nil {
		t.Fatal(err)
	}
	if sha, _ := stageHeadAccepted(t, s, ctx, b.ID); sha != headB {
		t.Fatalf("accepted_head = %q, want the new head %s", sha, headB)
	}

	// A bead that waits in triage with no card (it was there before the stage
	// had a template) gets one by entering the stage again: advance to the stage
	// it is in is a fresh stay, and the card is of the head the bead tracks.
	waiting, err := s.CreateBead(ctx, store.NewBead{Title: "waiting", Depot: "web", Stage: "triage", Metadata: headTold(headA, at)}, "me")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range stageAsks(t, s, waiting.ID) {
		if err := s.TakeBackAsk(ctx, waiting.ID, r.Note.Seq, "gone before the template was", "yard"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Advance(ctx, waiting.ID, "triage", store.Expect{}, "ada"); err != nil {
		t.Fatal(err)
	}
	var open int
	for _, r := range stageAsks(t, s, waiting.ID) {
		if r.State == store.AskOpen && r.Ask.Head != nil && r.Ask.Head.Sha == headA {
			open++
		}
	}
	if open != 2 {
		t.Fatalf("after advancing to the stage it is in, %s has %d open cards of %s, want one per maintainer", waiting.ID, open, headA)
	}
}

// Only the stage's members' picks count: a user who is no maintainer is
// refused whole, and nothing is accepted.
func TestStageHeadPickIsTheMaintainersAlone(t *testing.T) {
	t.Parallel()
	s, _, ctx := stageHeadYard(t)
	if _, err := s.AddUser(ctx, "eve", "jo"); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 10, 9, 41, 0, 0, time.UTC)
	b, err := s.CreateBead(ctx, store.NewBead{Title: "a fix", Depot: "web", Stage: "triage", Metadata: headTold(headA, at)}, "me")
	if err != nil {
		t.Fatal(err)
	}
	got := stageAsks(t, s, b.ID)
	if err := s.AddAnswer(ctx, b.ID, store.Answer{Ask: got[0].Note.Seq, Option: 1}, "eve"); err == nil {
		t.Fatal("a pick by a user outside the group was taken")
	}
	if cur, err := s.GetBead(ctx, b.ID); err != nil || cur.Stage != "triage" {
		t.Fatalf("after the refused pick the bead is %+v, %v", cur, err)
	}
	if sha, ok := stageHeadAccepted(t, s, ctx, b.ID); ok {
		t.Fatalf("a non-member's pick recorded accepted_head %q", sha)
	}
}

// A pick that arrives over a link is held to the same: a remote maintainer's
// yes records the head the card showed, and one for a head the pull request
// has left is refused and moves nothing.
func TestStageHeadPickFromLink(t *testing.T) {
	t.Parallel()
	s, own, ctx := stageHeadYard(t)
	if err := s.PutGroup(ctx, store.Group{Name: "maintainers", Runner: "manual", Members: []string{"ada", "mckean@north"}}, "jo"); err != nil {
		t.Fatal(err)
	}
	key := stageAnswerPeer(t, s, own, ctx, store.PeerAllow{})
	at := time.Date(2026, 11, 10, 9, 41, 0, 0, time.UTC)
	stale, err := s.CreateBead(ctx, store.NewBead{Title: "stale", Depot: "web", Stage: "triage", Metadata: headTold(headA, at)}, "me")
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.CreateBead(ctx, store.NewBead{Title: "current", Depot: "web", Stage: "triage", Metadata: headTold(headA, at)}, "me")
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.PeerOutbox(ctx, "north", 0)
	if err != nil || len(out) != 2 {
		t.Fatalf("outbox = %+v, %v", out, err)
	}
	remote := func(id string) store.ResolvedAsk {
		t.Helper()
		for _, r := range stageAsks(t, s, id) {
			if r.Ask.To == "mckean@north" {
				return r
			}
		}
		t.Fatalf("no ask for the remote maintainer on %s", id)
		return store.ResolvedAsk{}
	}
	staleAsk := remote(stale.ID)
	if _, err := s.UpdateBead(ctx, stale.ID, store.BeadPatch{Metadata: headTold(headB, at.Add(time.Hour))}, store.Expect{}, "yard"); err != nil {
		t.Fatal(err)
	}
	if taken := stageAnswerFromLink(t, own, ctx, key, "ans1", out[0].ID, "mckean", stale.ID, staleAsk.Note.Seq,
		map[string]any{"option": 1}); taken.Refused == "" {
		t.Fatalf("the remote yes to a head the pull request left was taken: %+v", taken)
	}
	if cur, err := s.GetBead(ctx, stale.ID); err != nil || cur.Stage != "triage" {
		t.Fatalf("after the refused remote pick the bead is %+v, %v", cur, err)
	}
	if sha, ok := stageHeadAccepted(t, s, ctx, stale.ID); ok {
		t.Fatalf("a refused remote pick recorded accepted_head %q", sha)
	}

	currentAsk := remote(current.ID)
	if taken := stageAnswerFromLink(t, own, ctx, key, "ans2", out[1].ID, "mckean", current.ID, currentAsk.Note.Seq,
		map[string]any{"option": 1}); taken.Refused != "" {
		t.Fatalf("the remote yes = %+v", taken)
	}
	if cur, err := s.GetBead(ctx, current.ID); err != nil || cur.Stage != "review" {
		t.Fatalf("after the remote yes the bead is %+v, %v", cur, err)
	}
	if sha, _ := stageHeadAccepted(t, s, ctx, current.ID); sha != headA {
		t.Fatalf("accepted_head = %q, want %s", sha, headA)
	}
}
