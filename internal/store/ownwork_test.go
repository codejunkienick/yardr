package store_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

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
