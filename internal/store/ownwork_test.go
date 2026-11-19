package store_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	bolt "go.etcd.io/bbolt"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
)

// ownSetup is a yard with two users, ada the operator, and bob.
func ownSetup(t *testing.T) (store.API, *store.Store, context.Context) {
	t.Helper()
	api, owned := storetest.NewOwned(t)
	ctx := context.Background()
	for _, u := range []string{"ada", "bob"} {
		if _, err := api.AddUser(ctx, u, "ada"); err != nil {
			t.Fatal(err)
		}
	}
	if err := api.SetSetting(ctx, store.OperatorSetting, "ada", "ada"); err != nil {
		t.Fatal(err)
	}
	return api, owned, ctx
}

func ownDepot(t *testing.T, api store.API, ctx context.Context, name string) {
	t.Helper()
	if err := api.PutDepot(ctx, store.Depot{Name: name, Kind: store.DepotDir, Path: t.TempDir()}, "ada"); err != nil {
		t.Fatal(err)
	}
}

func countBeads(t *testing.T, api store.API, ctx context.Context) int {
	t.Helper()
	beads, err := api.ListBeads(ctx, store.BeadFilter{All: true, Knowledge: true})
	if err != nil {
		t.Fatal(err)
	}
	return len(beads)
}

// Filing is for the caller alone: a task with no stage named, in the
// depot the project names. The refusal lists the person's projects, and
// files nothing.
func TestFileBead(t *testing.T) {
	api, _, ctx := ownSetup(t)
	// No depot at all: the same refusal, and nothing filed.
	before := countBeads(t, api, ctx)
	if _, err := api.FileBead(ctx, "Write the report", "", "", "ada"); err == nil ||
		!strings.Contains(err.Error(), "Your projects are: none yet") {
		t.Fatalf("no depot: %v", err)
	}
	if got := countBeads(t, api, ctx); got != before {
		t.Fatalf("the refusal filed %d beads", got-before)
	}
	ownDepot(t, api, ctx, "yardr")
	// The one depot needs no project, and the bead starts where its
	// flow starts: no flow governs it, so "new".
	filed, err := api.FileBead(ctx, "Write the report", "a page the team reads", "", "ada")
	if err != nil {
		t.Fatal(err)
	}
	if filed.Type != "task" || filed.Stage != "new" || filed.Originator != "ada" ||
		filed.Depot == nil || *filed.Depot != "yardr" || filed.Priority != store.DefaultPriority {
		t.Fatalf("filed %+v, want ada's task at P%d in yardr's first stage", filed, store.DefaultPriority)
	}
	if filed.Body != "a page the team reads" {
		t.Fatalf("filed body %q", filed.Body)
	}
	if filed.CreatedBy == nil || *filed.CreatedBy != "ada" {
		t.Fatalf("filed by %+v, want ada", filed.CreatedBy)
	}
	// Filing for oneself alone: bob's filing is bob's.
	bobs, err := api.FileBead(ctx, "Bob's errand", "", "", "bob")
	if err != nil {
		t.Fatal(err)
	}
	if bobs.Originator != "bob" {
		t.Fatalf("bob filed %+v", bobs)
	}
	// A name that is not the one depot refuses the same way.
	if _, err := api.FileBead(ctx, "Other", "", "shop", "ada"); err == nil ||
		!strings.Contains(err.Error(), "Your projects are: yardr") {
		t.Fatalf("wrong project: %v", err)
	}
	// A bead needs a title, whatever the project says.
	if _, err := api.FileBead(ctx, "  ", "", "yardr", "ada"); err == nil || err.Error() != "a bead needs a title" {
		t.Fatalf("blank title: %v", err)
	}
	ownDepot(t, api, ctx, "shop")
	// Several depots: missing names none, an exact name files there in
	// any case, and a partial name files where it picks exactly one.
	if _, err := api.FileBead(ctx, "Other", "", "", "ada"); err == nil ||
		!strings.Contains(err.Error(), "Your projects are:") ||
		!strings.Contains(err.Error(), "yardr") || !strings.Contains(err.Error(), "shop") {
		t.Fatalf("several, unnamed: %v", err)
	}
	for _, tc := range []struct{ project, depot string }{
		{"Shop", "shop"},
		{"sho", "shop"},
	} {
		filed, err := api.FileBead(ctx, "Stock the shelves", "", tc.project, "ada")
		if err != nil {
			t.Fatalf("%s: %v", tc.project, err)
		}
		if *filed.Depot != tc.depot {
			t.Fatalf("%s filed in %s, want %s", tc.project, *filed.Depot, tc.depot)
		}
	}
	// Matching none and matching several both file nothing and list the
	// projects.
	ownDepot(t, api, ctx, "yard")
	// An exact name wins over another depot's partial match.
	filed, err = api.FileBead(ctx, "Paint the gate", "", "yard", "ada")
	if err != nil {
		t.Fatal(err)
	}
	if *filed.Depot != "yard" {
		t.Fatalf("yard filed in %s", *filed.Depot)
	}
	before = countBeads(t, api, ctx)
	for _, project := range []string{"storage", "yar"} {
		if _, err := api.FileBead(ctx, "Stock the shelves", "", project, "ada"); err == nil ||
			!strings.Contains(err.Error(), "Your projects are:") ||
			!strings.Contains(err.Error(), "yardr") || !strings.Contains(err.Error(), "shop") ||
			!strings.Contains(err.Error(), "yard") {
			t.Fatalf("%s: %v", project, err)
		}
	}
	if got := countBeads(t, api, ctx); got != before {
		t.Fatalf("the refusals filed %d beads", got-before)
	}
	// A filed bead starts where its depot's flow starts: the flow's first
	// stage, which is no rule of the filing but the flow's own.
	flow, err := store.ParseFlow("inbox -> new\nnew -> merged\nmerged terminal\n")
	if err != nil {
		t.Fatal(err)
	}
	flow.Name = "default"
	if _, err := api.PutFlow(ctx, *flow, "ada"); err != nil {
		t.Fatal(err)
	}
	if err := api.SetFlowPointer(ctx, "yardr", "", "default", "ada"); err != nil {
		t.Fatal(err)
	}
	filed, err = api.FileBead(ctx, "Use the flow", "", "yardr", "ada")
	if err != nil {
		t.Fatal(err)
	}
	if filed.Stage != "inbox" {
		t.Fatalf("filed at %q, want the flow's first stage", filed.Stage)
	}
}

// From a bead agent's pane, filing is refused as any filing for another
// is: the pane files for its own bead's originator alone.
func TestFileBeadFromAnAgentsPane(t *testing.T) {
	api, owned, ctx := ownSetup(t)
	ownDepot(t, api, ctx, "yardr")
	if err := api.PutGroup(ctx, store.Group{Name: "g", Runner: "fake"}, "ada"); err != nil {
		t.Fatal(err)
	}
	pane, err := api.CreateBead(ctx, store.NewBead{Title: "the pane's bead", For: "bob"}, "ada")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := api.Claim(ctx, pane.ID, "g", "new", "yard")
	if err != nil {
		t.Fatal(err)
	}
	const panePID = 1 << 30
	if err := api.MarkRunning(ctx, sess.ID, map[string]any{"agent": "pane-new", "shell_pid": panePID}); err != nil {
		t.Fatal(err)
	}
	// The API may be the socket client, which carries no caller; the
	// pane is the owner's to read.
	if _, err := owned.FileBead(store.WithCaller(ctx, panePID), "Filed from the pane", "", "", "bob"); err == nil ||
		!strings.Contains(err.Error(), "files for its own bead's originator alone") {
		t.Fatalf("filing from the pane: %v", err)
	}
	// A refused ambiguous project must not reveal depot names from the
	// pane, which cannot list depots directly.
	ownDepot(t, api, ctx, "another")
	if _, err := owned.FileBead(store.WithCaller(ctx, panePID), "Filed from the pane", "", "missing", "bob"); err == nil ||
		!strings.Contains(err.Error(), "files for its own bead's originator alone") ||
		strings.Contains(err.Error(), "Your projects are:") {
		t.Fatalf("filing with an unknown project from the pane: %v", err)
	}
}

// The list is the person's own work alone: open work whole, finished
// work inside the window, and nothing of anyone else's. It keeps no
// cursor: the same window answers the same, twice.
func TestOwnBeads(t *testing.T) {
	api, _, ctx := ownSetup(t)
	ownDepot(t, api, ctx, "yardr")
	adaOpen, err := api.CreateBead(ctx, store.NewBead{Title: "Ada's open work", Depot: "yardr", For: "ada"}, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.CreateBead(ctx, store.NewBead{Title: "Bob's open work", For: "bob"}, "bob"); err != nil {
		t.Fatal(err)
	}
	finished, err := api.CreateBead(ctx, store.NewBead{Title: "Ada's finished work", For: "ada", Labels: []string{"kind:fix"}}, "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.CloseBead(ctx, finished.ID, "landed", store.Expect{}, "ada"); err != nil {
		t.Fatal(err)
	}
	// A memory of ada's is no work, and the list leaves it out.
	if _, err := api.Remember(ctx, store.NewMemory{Title: "Ada remembers", Body: "the gate sticks"}, "ada"); err != nil {
		t.Fatal(err)
	}
	mine := func(since time.Time) []store.OwnBead {
		t.Helper()
		own, err := api.OwnBeads(ctx, "ada", since, "ada")
		if err != nil {
			t.Fatal(err)
		}
		return own
	}
	ids := func(own []store.OwnBead) []string {
		var out []string
		for _, b := range own {
			out = append(out, b.ID)
		}
		return out
	}
	// By default, the last 24 hours: the open work and the work just
	// finished, and nothing of bob's and no memory.
	own := mine(time.Time{})
	if got := ids(own); len(got) != 2 || got[0] != adaOpen.ID || got[1] != finished.ID {
		t.Fatalf("own = %v, want [%s %s]", got, adaOpen.ID, finished.ID)
	}
	if own[0].Depot == nil || *own[0].Depot != "yardr" || own[0].Status != "open" || own[0].Type != "task" {
		t.Fatalf("open row = %+v", own[0])
	}
	if own[0].CreatedBy == nil || *own[0].CreatedBy != "ada" {
		t.Fatalf("open row filed by %+v, want ada", own[0].CreatedBy)
	}
	if own[1].ClosedAt == nil || own[1].CloseReason == nil || *own[1].CloseReason != "landed" {
		t.Fatalf("finished row = %+v", own[1])
	}
	if own[1].Labels == nil || len(own[1].Labels) != 1 || own[1].Labels[0] != "kind:fix" {
		t.Fatalf("finished row labels = %+v", own[1].Labels)
	}
	// An earlier since widens the finished work; a later one narrows it
	// to nothing, while the open work stands whatever the window.
	if got := ids(mine(time.Now().Add(-time.Hour))); len(got) != 2 {
		t.Fatalf("since an hour ago = %v, want the open work and the finished", got)
	}
	if got := ids(mine(time.Now().Add(time.Hour))); len(got) != 1 || got[0] != adaOpen.ID {
		t.Fatalf("since an hour hence = %v, want the open work alone", got)
	}
	// No cursor and no last-view state: the same call answers the same.
	again := mine(time.Time{})
	if len(again) != len(own) || again[0].ID != own[0].ID || again[1].ID != own[1].ID {
		t.Fatalf("a second call = %v, want %v again", ids(again), ids(own))
	}
	// Bob's list is his own work alone.
	bobs, err := api.OwnBeads(ctx, "bob", time.Time{}, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if len(bobs) != 1 || bobs[0].Title != "Bob's open work" {
		t.Fatalf("bob's = %+v", bobs)
	}
}

// Only the user reads their list, and an operator any user's; a name no
// user holds is no list at all.
func TestOwnBeadsReadsAsThePerson(t *testing.T) {
	api, _, ctx := ownSetup(t)
	if _, err := api.CreateBead(ctx, store.NewBead{Title: "Bob's work", For: "bob"}, "bob"); err != nil {
		t.Fatal(err)
	}
	if _, err := api.OwnBeads(ctx, "ada", time.Time{}, "bob"); err == nil {
		t.Fatal("bob reads ada's list")
	}
	if _, err := api.OwnBeads(ctx, "bob", time.Time{}, "ada"); err != nil {
		t.Fatalf("the operator reads bob's list: %v", err)
	}
	// Left out, the reader is the user.
	if _, err := api.OwnBeads(ctx, "bob", time.Time{}, ""); err != nil {
		t.Fatalf("no actor reads bob's list: %v", err)
	}
	if _, err := api.OwnBeads(ctx, "nobody", time.Time{}, "ada"); err == nil {
		t.Fatal("nobody has a list")
	}
}

// What the reader may not see is left out of their list, as if it were
// not there.
func TestOwnBeadsLeavesOutWhatTheReaderMayNotSee(t *testing.T) {
	api, owned, ctx := ownSetup(t)
	seen, err := api.CreateBead(ctx, store.NewBead{Title: "Seen work", For: "ada"}, "ada")
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := api.CreateBead(ctx, store.NewBead{Title: "Hidden work", For: "ada"}, "ada")
	if err != nil {
		t.Fatal(err)
	}
	owned.SetPolicy(policyFunc(func(_ context.Context, _ *bolt.Tx, actor string, act store.Action, obj store.Object) error {
		if act == store.ActSee && obj.Kind == store.KindBead && obj.Name == hidden.ID {
			return fmt.Errorf("hidden from %s", actor)
		}
		return nil
	}))
	own, err := api.OwnBeads(ctx, "ada", time.Time{}, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if len(own) != 1 || own[0].ID != seen.ID {
		t.Fatalf("own = %+v, want the seen work alone", own)
	}
}

// ownFile files ada's request to withdraw or reword, at the stage given.
func ownFile(t *testing.T, api store.API, ctx context.Context, title, stage string) string {
	t.Helper()
	b, err := api.CreateBead(ctx, store.NewBead{Title: title, Depot: "yardr", Stage: stage, For: "ada"}, "ada")
	if err != nil {
		t.Fatal(err)
	}
	return b.ID
}

// ownHeld claims bead for group, as the dispatcher's claim does.
func ownHeld(t *testing.T, api store.API, ctx context.Context, bead, group string) {
	t.Helper()
	b, err := api.GetBead(ctx, bead)
	if err != nil {
		t.Fatal(err)
	}
	if err := api.PutGroup(ctx, store.Group{Name: group, Runner: "herdr", Limit: 5}, "ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := api.Claim(ctx, bead, group, b.Stage, "yard"); err != nil {
		t.Fatal(err)
	}
}

// ownBlind holds a call to the rule both own-work writes share: work that
// is not a request the person filed (another person's, the crew's filing
// for them, a name that matches nothing, a prefix that matches several)
// is refused in one set of words, which say nothing of whether such work
// exists, wrap no sentinel, and leave the work as it was.
func ownBlind(t *testing.T, api store.API, ctx context.Context, try func(work string) error) {
	t.Helper()
	for _, id := range []string{"bobs-abcd1", "bobs-abcd2"} {
		if _, err := api.CreateBead(ctx, store.NewBead{ID: id, Title: "Bob's " + id, Type: "task", Depot: "yardr", For: "bob"}, "bob"); err != nil {
			t.Fatal(err)
		}
	}
	bobs, err := api.FileBead(ctx, "Bob's request", "", "", "bob")
	if err != nil {
		t.Fatal(err)
	}
	crews, err := api.CreateBead(ctx, store.NewBead{Title: "Filed for ada by the crew", Depot: "yardr", For: "ada"}, "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	others := map[string]string{
		"another person's":           bobs.ID,
		"filed by the crew for them": crews.ID,
		"a prefix matching several":  "bobs-abcd",
	}
	nothing := try("yardr-nosuch")
	if nothing == nil || !strings.Contains(nothing.Error(), "I can't find that among the requests you filed") {
		t.Fatalf("a name that matches nothing was answered %v", nothing)
	}
	for name, work := range others {
		var before string
		if b, err := api.GetBead(ctx, work); err == nil {
			before = b.Revision
		}
		err := try(work)
		if err == nil || err.Error() != nothing.Error() {
			t.Errorf("work %s: %v, want the words for a name that matches nothing: %v", name, err, nothing)
		}
		for _, sentinel := range []error{store.ErrNotFound, store.ErrAmbiguous, store.ErrConflict} {
			if errors.Is(err, sentinel) {
				t.Errorf("work %s wraps %v: %v", name, sentinel, err)
			}
		}
		if before != "" {
			if after, err := api.GetBead(ctx, work); err != nil || after.Revision != before {
				t.Errorf("work %s was changed: %+v, %v", name, after, err)
			}
		}
	}
}

// Withdrawing is closing a request the person filed, with their reason as
// the close reason: the same close CloseBead makes, refused while the
// request is landing.
func TestWithdrawBead(t *testing.T) {
	api, owned, ctx := ownSetup(t)
	ownDepot(t, api, ctx, "yardr")
	// An open request of the person's own is closed, with their reason as
	// it stands, trimmed, as the close reason, in the work and in its
	// history, by them. No session held it, so none was stopped.
	const reason = "the figures are in already"
	own := ownFile(t, api, ctx, "Write the report", "new")
	withdrawn, err := api.WithdrawBead(ctx, own, "  "+reason+"\n", "ada")
	if err != nil {
		t.Fatal(err)
	}
	if withdrawn.Bead.ID != own || withdrawn.Bead.Status != "closed" || withdrawn.Stopped {
		t.Fatalf("withdrawn %+v, want %s closed with no session stopped", withdrawn.Bead, own)
	}
	if withdrawn.Bead.CloseReason == nil || *withdrawn.Bead.CloseReason != reason {
		t.Fatalf("close reason %+v, want %q", withdrawn.Bead.CloseReason, reason)
	}
	evs, err := api.Events(ctx, own, 20)
	if err != nil {
		t.Fatal(err)
	}
	var closedBy string
	for _, e := range evs {
		if e.Kind == "closed" && e.Data["reason"] == reason {
			closedBy = e.Actor
		}
	}
	if closedBy != "ada" {
		t.Errorf("the history has the close by %q, want ada", closedBy)
	}
	// A request closed already is refused, and the reason it was closed
	// with stands, which a second close would write over.
	if _, err := api.WithdrawBead(ctx, own, "a second thought", "ada"); err == nil ||
		err.Error() != "That one is finished or withdrawn already, so there is nothing to change" {
		t.Fatalf("withdrawing a closed request = %v", err)
	}
	if again, err := api.GetBead(ctx, own); err != nil || again.CloseReason == nil || *again.CloseReason != reason {
		t.Fatalf("the close reason after a second withdrawal is %+v, %v", again, err)
	}
	// Another's request and a name that matches nothing are refused alike.
	ownBlind(t, api, ctx, func(work string) error {
		_, err := api.WithdrawBead(ctx, work, "no longer wanted", "ada")
		return err
	})
	// Without the work or the reason nothing is closed, in the adapters'
	// words: the yard is the authority behind their early refusal.
	open := ownFile(t, api, ctx, "Still wanted", "new")
	for _, c := range []struct{ work, reason, want string }{
		{open, "  ", "say why you are withdrawing it, in your words"},
		{" ", "no longer wanted", "say which piece of work: its work name, from an earlier answer"},
	} {
		if _, err := api.WithdrawBead(ctx, c.work, c.reason, "ada"); err == nil || err.Error() != c.want {
			t.Errorf("WithdrawBead(%q, %q) = %v, want %q", c.work, c.reason, err, c.want)
		}
	}
	if got, err := api.GetBead(ctx, open); err != nil || got.Status != "open" {
		t.Fatalf("a withdrawal without its reason closed the request: %+v, %v", got, err)
	}
	// While it is landing it is refused: it waits in the stage the
	// assembly lands from, or the depot's assembly holds it. Nothing
	// changes, and the assembly's session goes on.
	for _, c := range []struct{ name, stage, group string }{
		{"waiting to be landed", "approved", ""},
		{"held by the assembly", "new", "yardr-assembly"},
	} {
		work := ownFile(t, api, ctx, "Landing, "+c.name, c.stage)
		if c.group != "" {
			ownHeld(t, api, ctx, work, c.group)
		}
		before, err := api.GetBead(ctx, work)
		if err != nil {
			t.Fatal(err)
		}
		_, err = api.WithdrawBead(ctx, work, "changed my mind", "ada")
		if err == nil || !strings.Contains(err.Error(), "It is landing") || !strings.Contains(err.Error(), "Write to the yardmaster") {
			t.Errorf("withdrawing a request %s = %v", c.name, err)
			continue
		}
		if after, err := api.GetBead(ctx, work); err != nil || after.Revision != before.Revision || after.Status != "open" {
			t.Errorf("a request %s was changed: %+v, %v", c.name, after, err)
		}
		if c.group != "" {
			sess, err := api.GetBead(ctx, work)
			if err != nil || sess.Session == nil {
				t.Fatalf("a request %s lost its session: %+v, %v", c.name, sess, err)
			}
			if live, err := api.GetSession(ctx, *sess.Session); err != nil || live.EndedAt != nil {
				t.Errorf("the assembly's session after the refusal: %+v, %v", live, err)
			}
		}
	}
	// Under a builder's session it goes through: the close ends the
	// session as any close does, and the answer says so.
	building := ownFile(t, api, ctx, "Under a builder", "new")
	ownHeld(t, api, ctx, building, "yardr-builders")
	held, err := api.GetBead(ctx, building)
	if err != nil || held.Session == nil {
		t.Fatal("the builder holds nothing")
	}
	withdrawn, err = api.WithdrawBead(ctx, building, "not needed any more", "ada")
	if err != nil {
		t.Fatal(err)
	}
	if !withdrawn.Stopped {
		t.Errorf("the withdrawal under a session stopped nothing: %+v", withdrawn)
	}
	if got, err := api.GetBead(ctx, building); err != nil || got.Status != "closed" || got.Session != nil {
		t.Errorf("the request withdrawn under a session is %+v, %v", got, err)
	}
	if ended, err := api.GetSession(ctx, *held.Session); err != nil || ended.State != "done" || ended.EndedAt == nil {
		t.Errorf("the session after the withdrawal: %+v, %v; want it ended", ended, err)
	}
	// The rules are checked in the write they guard: one the assembly
	// claimed since the caller read is refused as landing, not closed
	// from under the assembly, and no revision crosses the call to carry
	// the standing.
	raced := ownFile(t, api, ctx, "Raced by the assembly", "new")
	read, err := api.GetBead(ctx, raced)
	if err != nil || read.Session != nil {
		t.Fatal("the raced request is held already")
	}
	ownHeld(t, api, ctx, raced, "yardr-assembly")
	if _, err := api.WithdrawBead(ctx, raced, "changed my mind", "ada"); err == nil || !strings.Contains(err.Error(), "It is landing") {
		t.Errorf("withdrawing a request claimed under the call = %v", err)
	}
	if got, err := api.GetBead(ctx, raced); err != nil || got.Status != "open" || got.Session == nil {
		t.Errorf("the request after the refused withdrawal is %+v, %v", got, err)
	}
	// Through a device the close is marked as the device's user's: what
	// came that way says so in the history. The mark is the door's
	// statement on the call's context, which the socket client does not
	// carry, so the call is the owner's.
	away := ownFile(t, api, ctx, "Withdrawn away from the yard", "new")
	if _, err := owned.WithdrawBead(store.WithVia(ctx, "ada", "phone"), away, "done from the train", "ada"); err != nil {
		t.Fatal(err)
	}
	evs, err = api.Events(ctx, away, 5)
	if err != nil {
		t.Fatal(err)
	}
	var via string
	for _, e := range evs {
		if e.Kind == "closed" {
			via = store.Via(e)
		}
	}
	if via != "phone" {
		t.Errorf("the close through the device is via %q, want phone", via)
	}
	// From a bead agent's pane the call reaches what the agent filed
	// itself and no other: the pane's author is who the filer check judges.
	if err := api.PutGroup(ctx, store.Group{Name: "g", Runner: "fake"}, "ada"); err != nil {
		t.Fatal(err)
	}
	pane, err := api.CreateBead(ctx, store.NewBead{Title: "the pane's bead", For: "bob"}, "ada")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := api.Claim(ctx, pane.ID, "g", "new", "yard")
	if err != nil {
		t.Fatal(err)
	}
	const panePID = 1 << 30
	if err := api.MarkRunning(ctx, sess.ID, map[string]any{"agent": "pane-new", "shell_pid": panePID}); err != nil {
		t.Fatal(err)
	}
	paneCtx := store.WithCaller(ctx, panePID)
	followup, err := owned.CreateBead(paneCtx, store.NewBead{Title: "the agent's follow-up"}, "pane-new")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owned.WithdrawBead(paneCtx, followup.ID, "overtaken", "pane-new"); err != nil {
		t.Errorf("withdrawing the pane's own filing: %v", err)
	}
	if _, err := owned.WithdrawBead(paneCtx, open, "not mine", "pane-new"); err == nil ||
		!strings.Contains(err.Error(), "I can't find that among the requests you filed") {
		t.Errorf("withdrawing ada's request from the pane = %v", err)
	}
}

// Rewording replaces the title, the body or both of a request the person
// filed, and nothing else about it changes. It is refused while a session
// holds the request.
func TestRewordBead(t *testing.T) {
	api, owned, ctx := ownSetup(t)
	ownDepot(t, api, ctx, "yardr")
	// The title and the body are replaced with the words given, trimmed,
	// and nothing else about the request changes. It starts with what a
	// reword must leave alone set to something the filing did not set: a
	// priority, labels and metadata, as the yardmaster sets them.
	own := ownFile(t, api, ctx, "Write the report", "new")
	priority := 1
	if _, err := api.UpdateBead(ctx, own, store.BeadPatch{
		Priority: &priority, AddLabels: []string{"parked", "size:small"}, Metadata: map[string]any{"origin": "chat"},
	}, store.Expect{}, "yardmaster"); err != nil {
		t.Fatal(err)
	}
	before, err := api.GetBead(ctx, own)
	if err != nil {
		t.Fatal(err)
	}
	reworded, err := api.RewordBead(ctx, own, " Write the annual report ", "What done looks like: a page and a chart.\n\nNot a slide deck.\n", "ada")
	if err != nil {
		t.Fatal(err)
	}
	if !reworded.Changed || reworded.Bead.ID != own {
		t.Fatalf("reworded %+v, want %s changed", reworded, own)
	}
	after, err := api.GetBead(ctx, own)
	if err != nil {
		t.Fatal(err)
	}
	if after.Title != "Write the annual report" || after.Body != "What done looks like: a page and a chart.\n\nNot a slide deck." {
		t.Fatalf("after the reword: %+v", after)
	}
	if after.Priority != 1 || len(after.Labels) != 2 || after.Metadata["origin"] != "chat" || after.Stage != before.Stage {
		t.Fatalf("after the reword the rest changed: %+v", after)
	}
	// The event is the update's own: what each was before it is kept
	// under was, and a body changed is carried as true, never the body.
	evs, err := api.Events(ctx, own, 20)
	if err != nil {
		t.Fatal(err)
	}
	var updated *store.Event
	for i := range evs {
		if evs[i].Kind == "updated" {
			updated = &evs[i]
		}
	}
	if updated == nil {
		t.Fatal("the reword logged no updated event")
	}
	was, _ := updated.Data["was"].(map[string]any)
	if updated.Data["title"] != "Write the annual report" || updated.Data["body"] != true ||
		was["title"] != before.Title || was["body"] != before.Body || updated.Actor != "ada" {
		t.Fatalf("the updated event is %+v", updated)
	}
	// A title alone leaves the details as they are, and the details alone
	// leave the title.
	if _, err := api.RewordBead(ctx, own, "Write the yearly report", "", "ada"); err != nil {
		t.Fatal(err)
	}
	if got, err := api.GetBead(ctx, own); err != nil || got.Title != "Write the yearly report" || got.Body != after.Body {
		t.Fatalf("after the title alone: %+v, %v", got, err)
	}
	if _, err := api.RewordBead(ctx, own, "", "Just a page.", "ada"); err != nil {
		t.Fatal(err)
	}
	if got, err := api.GetBead(ctx, own); err != nil || got.Title != "Write the yearly report" || got.Body != "Just a page." {
		t.Fatalf("after the details alone: %+v, %v", got, err)
	}
	// The words it already has change nothing and log nothing.
	logged := func() int {
		t.Helper()
		evs, err := api.Events(ctx, own, 100)
		if err != nil {
			t.Fatal(err)
		}
		return len(evs)
	}
	events := logged()
	reworded, err = api.RewordBead(ctx, own, "Write the yearly report", "Just a page.", "ada")
	if err != nil {
		t.Fatal(err)
	}
	if reworded.Changed {
		t.Errorf("rewording to the words it has changed it: %+v", reworded)
	}
	if got := logged(); got != events {
		t.Errorf("a reword that changed nothing logged %d events (were %d)", got, events)
	}
	// Both blank, and a title of more than one line, are refused in the
	// adapters' words: the yard is the authority behind their early
	// refusal. A blank title or body alone is one not given, and a long
	// title is cut as filing cuts it, to 160 runes with an ellipsis.
	for _, c := range []struct{ title, body, want string }{
		{"", "", "say the new title, the new details, or both, in your words"},
		{"  ", "\n", "say the new title, the new details, or both, in your words"},
		{"One line\nand another", "", "title: say it as one line, and put the rest in the details"},
	} {
		if _, err := api.RewordBead(ctx, own, c.title, c.body, "ada"); err == nil || err.Error() != c.want {
			t.Errorf("RewordBead(%q, %q) = %v, want %q", c.title, c.body, err, c.want)
		}
	}
	if _, err := api.RewordBead(ctx, " ", "A title", "", "ada"); err == nil ||
		err.Error() != "say which piece of work: its work name, from an earlier answer" {
		t.Errorf("rewording no work = %v", err)
	}
	long := strings.Repeat("word ", 40) + "end"
	reworded, err = api.RewordBead(ctx, own, long, "", "ada")
	if err != nil {
		t.Fatal(err)
	}
	if want := store.CutTitle(long); reworded.Bead.Title != want {
		t.Fatalf("a long title was kept as %q, want the yard's cut %q", reworded.Bead.Title, want)
	}
	if got := reworded.Bead.Title; !strings.HasSuffix(got, "…") || utf8.RuneCountInString(got) > 161 || !strings.HasPrefix(long, strings.TrimSuffix(got, "…")) {
		t.Fatalf("a long title was kept as %q, want 160 runes with an ellipsis", got)
	}
	// Another's request and a name that matches nothing are refused alike.
	ownBlind(t, api, ctx, func(work string) error {
		_, err := api.RewordBead(ctx, work, "A new title", "", "ada")
		return err
	})
	// A request finished or withdrawn already is refused, and is left as
	// it was.
	gone := ownFile(t, api, ctx, "Withdrawn before long", "new")
	if _, err := api.WithdrawBead(ctx, gone, "no longer wanted", "ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := api.RewordBead(ctx, gone, "Back again", "", "ada"); err == nil ||
		err.Error() != "That one is finished or withdrawn already, so there is nothing to change" {
		t.Fatalf("rewording a closed request = %v", err)
	}
	if got, err := api.GetBead(ctx, gone); err != nil || got.Title != "Withdrawn before long" {
		t.Fatalf("a closed request was reworded: %+v, %v", got, err)
	}
	// Once a session holds it, the words are not changed under it.
	building := ownFile(t, api, ctx, "Under a builder", "new")
	ownHeld(t, api, ctx, building, "yardr-builders")
	held, err := api.GetBead(ctx, building)
	if err != nil || held.Session == nil {
		t.Fatal("the builder holds nothing")
	}
	_, err = api.RewordBead(ctx, building, "A new title", "", "ada")
	if err == nil || !strings.Contains(err.Error(), "Someone is working on it right now") ||
		!strings.Contains(err.Error(), "Write to the yardmaster") {
		t.Fatalf("rewording a request a session holds = %v", err)
	}
	if got, err := api.GetBead(ctx, building); err != nil || got.Revision != held.Revision || got.Title != "Under a builder" {
		t.Fatalf("the request a session holds after the refusal is %+v, %v", got, err)
	}
	// The rules are checked in the write they guard: one a session
	// claimed since the caller read is refused in those words, not
	// reworded under the builder that has just begun, and no revision
	// crosses the call to carry the standing. A claim that lands after
	// the write instead lands on the new words: the write and the claim
	// each stand.
	raced := ownFile(t, api, ctx, "Raced by a builder", "new")
	read, err := api.GetBead(ctx, raced)
	if err != nil || read.Session != nil {
		t.Fatal("the raced request is held already")
	}
	ownHeld(t, api, ctx, raced, "yardr-builders")
	if _, err := api.RewordBead(ctx, raced, "Changed under the builder", "", "ada"); err == nil ||
		!strings.Contains(err.Error(), "Someone is working on it right now") {
		t.Errorf("rewording a request claimed under the call = %v", err)
	}
	if got, err := api.GetBead(ctx, raced); err != nil || got.Title != "Raced by a builder" {
		t.Errorf("the request after the refused reword is %+v, %v", got, err)
	}
	first := ownFile(t, api, ctx, "Reworded, then claimed", "new")
	if _, err := api.RewordBead(ctx, first, "The new words", "", "ada"); err != nil {
		t.Fatal(err)
	}
	ownHeld(t, api, ctx, first, "yardr-builders")
	if got, err := api.GetBead(ctx, first); err != nil || got.Title != "The new words" || got.Session == nil {
		t.Errorf("the request reworded before the claim is %+v, %v", got, err)
	}
	// Through a device the write is marked as the device's user's: what
	// came that way says so in the history. The mark is the door's
	// statement on the call's context, which the socket client does not
	// carry, so the call is the owner's.
	away := ownFile(t, api, ctx, "Reworded away from the yard", "new")
	if _, err := owned.RewordBead(store.WithVia(ctx, "ada", "phone"), away, "Reworded from the train", "", "ada"); err != nil {
		t.Fatal(err)
	}
	evs, err = api.Events(ctx, away, 5)
	if err != nil {
		t.Fatal(err)
	}
	var via string
	for _, e := range evs {
		if e.Kind == "updated" {
			via = store.Via(e)
		}
	}
	if via != "phone" {
		t.Errorf("the reword through the device is via %q, want phone", via)
	}
}
