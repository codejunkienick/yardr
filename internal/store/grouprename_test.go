package store

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// renameYard is runYard (operator mckean, the crew member yardmaster, the user
// ada) with what a rename is read against: the groups builders (limit 1) and
// reviewers, the depot web, and three routes that name builders, one of them
// after reviewers, each group with a condition of its own. It answers the
// routes' ids, in the order they were added.
func renameYard(t *testing.T) (*askYard, []string) {
	t.Helper()
	y, _ := runYard(t)
	for _, g := range []Group{
		{Name: "builders", Runner: "herdr", Limit: 1, Config: map[string]any{"kind": "claude", "model": "opus"}},
		{Name: "reviewers", Runner: "herdr", Limit: 2, Config: map[string]any{"kind": "claude"}},
	} {
		if err := y.PutGroup(y.ctx, g, "mckean"); err != nil {
			t.Fatal(err)
		}
	}
	if err := y.PutDepot(y.ctx, Depot{Name: "web", Base: "origin/main"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	bug := "bug"
	var ids []string
	for _, r := range []Route{
		{Stage: "new", Group: "builders"},
		{Stage: "review", Group: "reviewers", When: "Weekly<80", Else: []RouteGroup{{Name: "builders", When: "5h<90"}}},
		{Stage: "new", Type: &bug, Group: "builders"},
	} {
		got, err := y.AddRoute(y.ctx, r, "mckean")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, got.ID)
	}
	return y, ids
}

// claim opens a bead in stage new and claims it for group: a live session.
func (y *askYard) claim(t *testing.T, group string) (*Bead, *Session) {
	t.Helper()
	b, err := y.CreateBead(y.ctx, NewBead{Title: "work for " + group, Stage: "new"}, "mckean")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := y.Claim(y.ctx, b.ID, group, "new", "yard")
	if err != nil {
		t.Fatalf("claim for %s: %v", group, err)
	}
	return b, sess
}

// Renaming a group with a live session and three routes leaves the session
// running and the bead it works with it, the routes naming the new name, their
// further groups and conditions as they were, and no group under the old one:
// where add, moving the routes and rm released the session's bead. The live
// index moved with the session, so the group is still at its limit.
func TestRenameGroupLeavesItsSessionRunning(t *testing.T) {
	t.Parallel()
	y, ids := renameYard(t)
	bead, sess := y.claim(t, "builders")
	other, err := y.CreateBead(y.ctx, NewBead{Title: "waits", Stage: "new"}, "mckean")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := y.Claim(y.ctx, other.ID, "builders", "new", "yard"); !errors.Is(err, ErrAtLimit) {
		t.Fatalf("a second claim of the group at its limit = %v, want ErrAtLimit", err)
	}
	before, err := y.GetSession(y.ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}

	got, err := y.RenameGroup(y.ctx, "builders", "makers", "mckean")
	if err != nil {
		t.Fatal(err)
	}
	if got.From != "builders" || got.To != "makers" || !slices.Equal(got.Routes, slices.Sorted(slices.Values(ids))) || got.Beads != 1 || got.Sessions != 1 {
		t.Errorf("the rename answers %+v, want the three routes, one bead and one session", got)
	}

	if _, err := y.GetGroup(y.ctx, "builders"); !errors.Is(err, ErrNotFound) {
		t.Errorf("group builders after the rename = %v, want not found", err)
	}
	g, err := y.GetGroup(y.ctx, "makers")
	if err != nil || g.Runner != "herdr" || g.Limit != 1 || g.Config["model"] != "opus" {
		t.Errorf("group makers = %+v, %v: its settings and limit are builders'", g, err)
	}

	routes, err := y.Routes(y.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 3 {
		t.Fatalf("the yard has %d routes, want the same 3", len(routes))
	}
	for _, r := range routes {
		switch r.ID {
		case ids[1]:
			if r.Group != "reviewers" || r.When != "Weekly<80" || len(r.Else) != 1 || r.Else[0] != (RouteGroup{Name: "makers", When: "5h<90"}) {
				t.Errorf("the route with a chain = %+v, want reviewers@Weekly<80 else makers@5h<90", r)
			}
		default:
			if r.Group != "makers" || r.When != "" || len(r.Else) != 0 {
				t.Errorf("route %s = %+v, want makers", r.ID, r)
			}
		}
	}

	after, err := y.GetSession(y.ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Group != "makers" || !after.live() || after.State != before.State || after.EndedAt != nil ||
		!after.StartedAt.Equal(before.StartedAt) || !sameJSON(after.Handle, before.Handle) {
		t.Errorf("the session was %+v and is %+v: it runs on, under the new name", before, after)
	}
	b, err := y.GetBead(y.ctx, bead.ID)
	if err != nil {
		t.Fatal(err)
	}
	if b.Group == nil || *b.Group != "makers" || b.Session == nil || *b.Session != sess.ID || b.Status != "open" {
		t.Errorf("the bead the session works = %+v: it is makers' and the session's still", b)
	}
	// The live index holds it under the new name alone: a claim counts the
	// group's sessions by it.
	if err := y.read(func(tx *bolt.Tx) error {
		if old, err := y.liveSessions(tx, "builders"); err != nil || len(old) != 0 {
			t.Errorf("live sessions under the old name = %+v, %v", old, err)
		}
		if now, err := y.liveSessions(tx, "makers"); err != nil || len(now) != 1 || now[0].ID != sess.ID {
			t.Errorf("live sessions under the new name = %+v, %v", now, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := y.Claim(y.ctx, other.ID, "makers", "new", "yard"); !errors.Is(err, ErrAtLimit) {
		t.Errorf("a claim of makers = %v, want it at its limit of 1 with the renamed session", err)
	}
	if _, err := y.Claim(y.ctx, other.ID, "builders", "new", "yard"); err == nil {
		t.Error("a claim of the old name succeeded: no group is called that")
	}

	// One event says it, and nothing was released or removed.
	for kind, want := range map[string]int{"group_renamed": 1, "released": 0, "group_removed": 0} {
		evs, err := y.EventsOfKind(y.ctx, kind, 10)
		if err != nil || len(evs) != want {
			t.Errorf("%d %s events, %v; want %d", len(evs), kind, err, want)
		}
	}
	evs, _ := y.EventsOfKind(y.ctx, "group_renamed", 1)
	if e := evs[0]; e.Actor != "mckean" || e.Data["from"] != "builders" || e.Data["to"] != "makers" || e.Data["beads"] != float64(1) || e.Data["sessions"] != float64(1) {
		t.Errorf("group_renamed = %+v", e)
	}
}

// sameJSON says two values hold the same data, as a store reads them back.
func sameJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// A rename is refused, with a sentence for each, for a group that is not
// there, the same name, a group of the name already, a name pack imports give
// the groups they make for a depot of the yard, and what no new group could be
// called; and it is the operator's alone. Nothing is written by a refusal. A
// name that only holds a depot's, or an old name in that namespace, is no
// refusal: one leaves the namespace by renaming.
func TestRenameGroupRefusals(t *testing.T) {
	t.Parallel()
	y, ids := renameYard(t)
	if err := y.PutGroup(y.ctx, Group{Name: "web-assembly", Runner: "exec", Config: map[string]any{"cmd": "true"}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, to, actor, want string }{
		{"nobody", "makers", "mckean", "group nobody: not found"},
		{"builders", "builders", "mckean", "group builders has that name already"},
		{"builders", "reviewers", "mckean", "group reviewers is there already: a rename never replaces a group"},
		{"builders", "web-builders", "mckean", "web-builders is the name pack imports give the groups they make for depot web"},
		{"builders", "web-", "mckean", "choose a name that does not begin with web-"},
		{"builders", "wait", "mckean", "wait ends a route"},
		{"builders", "Make Rs", "mckean", "use letters, digits"},
		{"builders", "ada", "mckean", "that is a user's name"},
		{"builders", "yardmaster", "mckean", "that is a crew member's name"},
		{"builders", "makers", "yardmaster", "only the operator (mckean) may rename a group; this is yardmaster"},
		{"builders", "makers", "ada", "only the operator (mckean) may rename a group; this is ada"},
	} {
		if _, err := y.RenameGroup(y.ctx, c.name, c.to, c.actor); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("rename %s to %s as %s = %v, want %q", c.name, c.to, c.actor, err, c.want)
		}
	}
	if evs, err := y.EventsOfKind(y.ctx, "group_renamed", 10); err != nil || len(evs) != 0 {
		t.Errorf("the refusals logged %d group_renamed events, %v", len(evs), err)
	}
	if rs, err := y.Routes(y.ctx); err != nil || len(rs) != len(ids) || rs[0].Group == "makers" {
		t.Errorf("the refusals left routes %+v, %v", rs, err)
	}
	// The namespace is for new names: a group named in it leaves it, and one
	// that only holds a depot's name inside its own is free.
	if _, err := y.RenameGroup(y.ctx, "web-assembly", "assembly", "mckean"); err != nil {
		t.Errorf("renaming a group out of the importer's namespace = %v", err)
	}
	if _, err := y.RenameGroup(y.ctx, "builders", "makers-web-builders", "mckean"); err != nil {
		t.Errorf("a name that holds the depot's inside it = %v", err)
	}
}

// What else names a group follows it: the grants its sessions hold, in place
// of grants a group of the new name left behind (removing one takes none); the
// subscriptions that filter on it, a crew member's, a user's and a group's, a
// filter of several groups changing one of them; its arrival estimates; the
// records of the bead it sent to another yard while the stay is open; and the
// pack imports that recorded it, in each place a record holds a name, none
// left naming the old one. What names another group, or is another source's
// filter, is left as it was.
func TestRenameGroupMovesWhatNamesIt(t *testing.T) {
	t.Parallel()
	y, _ := renameYard(t)
	ctx := y.ctx
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	// Grants: builders' own, and one a deleted group called makers left.
	_, err := y.PutSource(ctx, "crm.yaml", runCRM, "mckean")
	must(err)
	must(y.PutGroup(ctx, Group{Name: "makers", Runner: "herdr", Limit: 1}, "mckean"))
	_, err = y.GrantSource(ctx, "crm", "note", Grantee{Group: "makers"}, "", "mckean")
	must(err)
	_, err = y.DeleteGroup(ctx, "makers", "mckean")
	must(err)
	left, err := y.SourceGrants(ctx, "crm")
	if must(err); len(left) != 1 || left[0].Group != "makers" {
		t.Fatalf("grants after removing a group = %+v: the test reads them as the group's, left behind", left)
	}
	_, err = y.GrantSource(ctx, "crm", GrantRead, Grantee{Group: "builders"}, "", "mckean")
	must(err)
	_, err = y.GrantSource(ctx, "crm", GrantRead, Grantee{Group: "reviewers"}, "", "mckean")
	must(err)

	// Subscriptions: filters on builders, on several groups, on another, and
	// another source's own key named group.
	filter := func(groups ...string) Subscription {
		return Subscription{Source: SourceYardr, Scope: ScopeAll, Where: map[string]string{"group": JoinWhere(groups), "stage": "new"}}
	}
	must(y.AddSubscription(ctx, "yardmaster", filter("builders", "reviewers"), "mckean"))
	must(y.AddSubscription(ctx, "ada", filter("builders"), "ada"))
	must(y.AddSubscription(ctx, "ada", filter("reviewers"), "ada"))
	must(y.PutGroup(ctx, Group{Name: "approvers", Runner: "manual", Members: []string{"mckean"}}, "mckean"))
	must(y.AddSubscriptionKind(ctx, "approvers", "group", filter("builders"), "mckean"))

	// The estimates of builders, and the stale ones a group of the new name left.
	d := time.Hour
	must(y.write(func(w *wtx) error {
		if err := recordETA(w.Tx, "builders", "-", "new", &d, &d, false); err != nil {
			return err
		}
		return recordETA(w.Tx, "makers", "-", "new", nil, nil, true)
	}))

	// A bead sent to another yard by builders, still away, and one that came back.
	must(y.write(func(w *wtx) error {
		settled := time.Now()
		if err := put(w.Tx, bPeerSent, "away", peerSent{Bead: "away", Group: "builders"}); err != nil {
			return err
		}
		return put(w.Tx, bPeerSent, "back", peerSent{Bead: "back", Group: "builders", Settled: &settled})
	}))

	packSub := filter("builders")
	// Pack imports: one that made builders (the pack calls it build) and
	// took reviewers, in a depot, and one of the yard that names neither.
	_, err = y.RecordPack(ctx, PackImport{Pack: "ci", Depot: "web", Source: "x",
		Groups: map[string]string{"build": "builders", "check": "reviewers"},
		Objects: []PackObject{
			{Kind: "group", Name: "builders", Binds: "reviewers"},
			{Kind: "group", Name: "other", Binds: "builders"},
			{Kind: "route", Stage: "new", Group: "builders"},
			{Kind: "route", Stage: "review", Group: "reviewers"},
			{Kind: "subscription", CrewMember: "yardmaster", Subscription: &packSub},
		},
		Calls: map[string]map[string]bool{"group builders": {"crm:read": true}, "group reviewers": {"crm:read": true}}}, true, "mckean")
	must(err)
	_, err = y.RecordPack(ctx, PackImport{Pack: "notes", Source: "x", Groups: map[string]string{"x": "reviewers"}}, true, "mckean")
	must(err)
	wasOther, err := y.Packs(ctx)
	must(err)

	got, err := y.RenameGroup(ctx, "builders", "makers", "mckean")
	must(err)
	if got.Grants != 1 || !slices.Equal(got.Subscriptions, []string{"ada", "approvers", "yardmaster"}) || !slices.Equal(got.Packs, []string{"ci in web"}) {
		t.Errorf("the rename answers %+v", got)
	}

	grants, err := y.SourceGrants(ctx, "crm")
	must(err)
	for _, g := range grants {
		switch {
		case g.Group == "builders":
			t.Errorf("a grant is still the old name's: %+v", g)
		case g.Group == "makers" && !slices.Equal(g.Calls, []string{GrantRead}):
			t.Errorf("makers holds %v: what the deleted group left is not the renamed one's", g.Calls)
		}
	}
	if len(grants) != 2 {
		t.Errorf("%d grants, want makers' and reviewers'", len(grants))
	}

	holds := func(subs []Subscription, groups ...string) bool {
		return slices.ContainsFunc(subs, func(s Subscription) bool { return s.Where["group"] == JoinWhere(groups) && s.Where["stage"] == "new" })
	}
	crew, err := y.GetCrew(ctx, "yardmaster")
	must(err)
	ada, err := y.GetUser(ctx, "ada")
	must(err)
	approvers, err := y.GetGroup(ctx, "approvers")
	must(err)
	if !holds(crew.Subscriptions, "makers", "reviewers") || !holds(ada.Subscriptions, "makers") || !holds(ada.Subscriptions, "reviewers") || !holds(approvers.Subscriptions, "makers") {
		t.Errorf("the filters read %+v, %+v, %+v", crew.Subscriptions, ada.Subscriptions, approvers.Subscriptions)
	}

	cell, err := y.ETACell(ctx, "makers", "-", "new")
	must(err)
	if cell.Stays != 1 || cell.EntryN != 1 || cell.SendBacks != 0 {
		t.Errorf("makers' estimates = %+v, want builders' one stay and none of what the stale cell held", cell)
	}
	if cell, err := y.ETACell(ctx, "builders", "-", "new"); err != nil || cell.Stays != 0 {
		t.Errorf("builders' estimates = %+v, %v, want none", cell, err)
	}

	must(y.read(func(tx *bolt.Tx) error {
		for key, want := range map[string]string{"away": "makers", "back": "builders"} {
			rec, err := peerSentOf(tx, key)
			if err != nil || rec.Group != want {
				t.Errorf("the bead %s was sent by %+v, %v: want %s", key, rec, err, want)
			}
		}
		return nil
	}))

	packs, err := y.Packs(ctx)
	must(err)
	text, err := json.Marshal(packs)
	must(err)
	if strings.Contains(string(text), "builders") {
		t.Errorf("a pack record names the old group still: %s", text)
	}
	for _, p := range packs {
		if p.Pack == "notes" {
			if !sameJSON(p, wasOther[0]) && !sameJSON(p, wasOther[1]) {
				t.Errorf("a record that named another group changed: %+v", p)
			}
			continue
		}
		if p.Groups["build"] != "makers" || p.Groups["check"] != "reviewers" {
			t.Errorf("the pack's groups = %v, want build -> makers and check -> reviewers", p.Groups)
		}
		o := p.Objects
		if o[0].Name != "makers" || o[0].Binds != "reviewers" || o[1].Binds != "makers" || o[2].Group != "makers" || o[3].Group != "reviewers" ||
			o[4].Subscription.Where["group"] != "makers" {
			t.Errorf("the pack's objects = %+v", o)
		}
		if p.Calls["group makers"]["crm:read"] != true || len(p.Calls["group builders"]) != 0 || p.Calls["group reviewers"]["crm:read"] != true {
			t.Errorf("the pack's calls = %v", p.Calls)
		}
	}
}

// A filter of another source that has a key named group is that source's, and
// the filter of a yardr subscription keeps what it does not name: each key and
// each other group in it, in the order a filter holds them, and the map the
// subscription had is not written into, since another may share it.
func TestRenamedGroupFilter(t *testing.T) {
	t.Parallel()
	shared := map[string]string{"group": JoinWhere([]string{"builders", "zed"}), "stage": "new"}
	subs := []Subscription{
		{Source: SourceYardr, Scope: ScopeAll, Where: shared},
		{Source: "linear", Scope: ScopeAll, Where: map[string]string{"group": "builders"}},
		{Source: SourceYardr, Scope: ScopeAll, Where: map[string]string{"stage": "new"}},
		{Source: SourceYardr, Scope: ScopeAll, Where: map[string]string{"group": "reviewers"}},
		{Source: SourceYardr, Scope: ScopeAll, Where: map[string]string{"group": strings.Join([]string{"builders", "builders", "zed"}, whereSep)}},
	}
	if !renamedGroupFilter(subs, "builders", "makers") {
		t.Fatal("no filter changed")
	}
	if got := WhereValues(subs[0].Where["group"]); !slices.Equal(got, []string{"makers", "zed"}) || subs[0].Where["stage"] != "new" {
		t.Errorf("the filter of two groups = %q", subs[0].Where)
	}
	if got := WhereValues(subs[4].Where["group"]); !slices.Equal(got, []string{"makers", "zed"}) {
		t.Errorf("the filter with a repeated group = %q, want the new name once and no old one", subs[4].Where["group"])
	}
	if shared["group"] != JoinWhere([]string{"builders", "zed"}) {
		t.Errorf("the map the subscription shared was written into: %q", shared)
	}
	if subs[1].Where["group"] != "builders" || subs[2].Where["group"] != "" || subs[3].Where["group"] != "reviewers" {
		t.Errorf("filters that name no builders of the yard changed: %+v", subs[1:])
	}
	if renamedGroupFilter(subs, "builders", "makers") {
		t.Error("the second pass changed a filter: the name is gone")
	}
}

// The beads a group owns and the stays it claimed follow it, so that a stay
// that ends after the rename is the group's under its new name and keeps what
// it cost: its durations are in the new name's cell, paired as a stay with its
// claimant is. A claim kept under the old name would end as one its claimant
// had let go of, and count nothing.
func TestRenameGroupKeepsAStayPaired(t *testing.T) {
	// Sequential: the package's fake clock.
	y := newETATest(t)
	ctx := context.Background()
	t0 := y.now
	b := y.create(t, "work", "new")
	y.at(t0.Add(10*time.Minute)).claim(t, b.ID, "builders", "new")
	if _, err := y.at(t0.Add(20*time.Minute)).RenameGroup(ctx, "builders", "makers", "me"); err != nil {
		t.Fatal(err)
	}
	var row *beadRow
	if err := y.read(func(tx *bolt.Tx) (err error) { row, err = loadBead(tx, b.ID); return }); err != nil {
		t.Fatal(err)
	}
	if row.Group == nil || *row.Group != "makers" || row.StayClaimGroup != "makers" || row.StayClaimAt == nil || !row.StayClaimAt.Equal(t0.Add(10*time.Minute)) || !row.StayStart.Equal(t0) {
		t.Fatalf("the bead's stay = group %v, claimed by %q at %v from %v", row.Group, row.StayClaimGroup, row.StayClaimAt, row.StayStart)
	}
	y.at(t0.Add(time.Hour)).advance(t, b.ID, "review")
	cell, err := y.ETACell(ctx, "makers", "-", "new")
	if err != nil {
		t.Fatal(err)
	}
	if cell.Stays != 1 || cell.EntryN != 1 || cell.ClaimN != 1 || cell.EntryMedian != time.Hour || cell.ClaimMedian != 50*time.Minute {
		t.Errorf("makers' cell = %+v, want the stay in full: an hour from entry, fifty minutes from the claim", cell)
	}
	if old, err := y.ETACell(ctx, "builders", "-", "new"); err != nil || old.Stays != 0 {
		t.Errorf("builders' cell = %+v, %v, want none", old, err)
	}
}

// A session that ended but is not done with (the runner's pane is up, or its
// worktree is left) is read through its group by the daemon's teardown and
// clean-up, so it follows the name, and the one that was done with keeps the
// name it ran under, as the log does.
func TestRenameGroupFollowsTheSessionsTheDaemonIsNotDoneWith(t *testing.T) {
	t.Parallel()
	y, _ := renameYard(t)
	_, running := y.claim(t, "reviewers")
	ended, pending := y.claim(t, "builders")
	if _, err := y.Release(y.ctx, ended.ID, "test", Expect{}, "mckean"); err != nil {
		t.Fatal(err)
	}
	stopped, done := y.claim(t, "builders")
	if _, err := y.Release(y.ctx, stopped.ID, "test", Expect{}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if err := y.MarkStopped(y.ctx, done.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := y.RenameGroup(y.ctx, "builders", "makers", "mckean"); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{pending.ID: "makers", done.ID: "builders", running.ID: "reviewers"} {
		sess, err := y.GetSession(y.ctx, id)
		if err != nil || sess.Group != want {
			t.Errorf("session %s is of %+v, %v: want %s", id, sess, err, want)
		}
	}
	unstopped, err := y.Unstopped(y.ctx)
	if err != nil || len(unstopped) != 1 || unstopped[0].ID != pending.ID || unstopped[0].Group != "makers" {
		t.Errorf("the sessions the daemon has yet to stop = %+v, %v: one, makers'", unstopped, err)
	}
}

// A yard default names its group by the depot template, which a rename does not
// touch, so the depot whose group it was stops being served by it: the result
// names that depot and the default, as group rm does. A depot the default did
// not serve before, for want of a group of its own, is no news, and one whose
// group stands is served as it was.
func TestRenameGroupNamesTheDefaultsThatStopServing(t *testing.T) {
	t.Parallel()
	y, _ := renameYard(t)
	for _, depot := range []string{"api", "lab"} {
		if err := y.PutDepot(y.ctx, Depot{Name: depot, Base: "origin/main"}, "mckean"); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"web-builders", "api-builders"} {
		if err := y.PutGroup(y.ctx, Group{Name: name, Runner: "herdr"}, "mckean"); err != nil {
			t.Fatal(err)
		}
	}
	def, err := y.AddRoute(y.ctx, Route{Stage: "build", Group: "{depot}-builders"}, "mckean")
	if err != nil {
		t.Fatal(err)
	}
	got, err := y.RenameGroup(y.ctx, "web-builders", "builders-web", "mckean")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Unserved) != 1 || got.Unserved[0].Depot != "web" || got.Unserved[0].Route.ID != def.ID {
		t.Fatalf("the rename says %+v stop being served, want the default for web alone", got.Unserved)
	}
	if len(got.Routes) != 0 {
		t.Errorf("the rename moved the routes %v: the default names the template, and no route names web-builders", got.Routes)
	}
	if r := y.routeOf(t, def.ID); r.Group != "{depot}-builders" {
		t.Errorf("the default now names %q, want the template as it was", r.Group)
	}
	for _, want := range []string{"default " + def.ID + " (build -> {depot}-builders) no longer serves web: no group web-builders"} {
		if !strings.Contains(got.String(), want) {
			t.Errorf("the rename tells %q, want it to say %q", got.String(), want)
		}
	}
}

// routeOf is the route id as stored.
func (y *askYard) routeOf(t *testing.T, id string) Route {
	t.Helper()
	rs, err := y.Routes(y.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rs {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no route %s", id)
	return Route{}
}

// A script group's sessions count as the group where another group lists it
// among its members (memberAllowed), so its rename changes the name in those
// lists, and the card says which groups' lists: without it the script could no
// longer move the beads of their stages. The name of a group of another runner
// in a list is a user's or a crew member's, and no rename of that group
// touches it.
func TestRenameAScriptGroupFollowsTheMembersThatNameIt(t *testing.T) {
	t.Parallel()
	y, _ := renameYard(t)
	for _, g := range []Group{
		{Name: "release", Runner: "exec", Config: map[string]any{"cmd": "true"}},
		{Name: "backlog", Runner: "manual", Members: []string{"mckean", "release", "yardmaster"}},
		{Name: "ready", Runner: "manual", Members: []string{"release"}},
		{Name: "decide", Runner: "manual", Members: []string{"ada", "reviewers"}},
	} {
		if err := y.PutGroup(y.ctx, g, "mckean"); err != nil {
			t.Fatal(err)
		}
	}
	members := func(name string) []string {
		t.Helper()
		g, err := y.GetGroup(y.ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		return g.Members
	}

	seq := y.ask(t, listAsk("mckean", "group rename release shipping"), "yardmaster")
	card := strings.Join(y.asked(t, seq).Ask.Run.Does, "\n")
	if want := "the groups backlog, ready list it among their members, which is how its script may move the beads of their stages: they list shipping."; !strings.Contains(card, want) {
		t.Errorf("the card says %q, want it to say %q", card, want)
	}
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(done.Result, "groups whose members listed it list the new name: backlog, ready") {
		t.Errorf("the approval ran %q", done.Result)
	}
	if got := members("backlog"); !slices.Equal(got, []string{"mckean", "shipping", "yardmaster"}) {
		t.Errorf("backlog's members = %v", got)
	}
	if got := members("ready"); !slices.Equal(got, []string{"shipping"}) {
		t.Errorf("ready's members = %v", got)
	}
	evs, err := y.EventsOfKind(y.ctx, "group_renamed", 1)
	if err != nil || len(evs) != 1 || !sameJSON(evs[0].Data["members"], []string{"backlog", "ready"}) {
		t.Errorf("group_renamed = %+v, %v: want the two lists in members", evs, err)
	}

	// An agent group named in a list is nobody's script: it is left as it is.
	got, err := y.RenameGroup(y.ctx, "reviewers", "checkers", "mckean")
	if err != nil || len(got.Members) != 0 {
		t.Fatalf("renaming an agent group = %+v, %v: no list is its", got, err)
	}
	if got := members("decide"); !slices.Equal(got, []string{"ada", "reviewers"}) {
		t.Errorf("decide's members = %v, want them as they were", got)
	}
}
