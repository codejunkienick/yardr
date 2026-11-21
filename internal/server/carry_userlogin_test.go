package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"

	"yardr.dev/yardr/internal/store"
)

// userLoginYard is a carry yard with the user ada and an operator route
// to a herdr group, which a first login copies for her.
func userLoginYard(t *testing.T) (*store.Store, string, string) {
	t.Helper()
	st, home, bead := carryYard(t)
	ctx := context.Background()
	if _, err := st.AddUser(ctx, "ada", "mckean"); err != nil {
		t.Fatal(err)
	}
	if err := st.PutGroup(ctx, store.Group{Name: "builders", Runner: "herdr", Config: map[string]any{"kind": "claude", "prompt": "roles/builder.md"}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddRoute(ctx, store.Route{Stage: "new", Group: "builders"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	return st, home, bead
}

// fakeCarryClaude puts a stand-in claude on PATH for the shared login
// planner. The tests that use it stay sequential: PATH is the process's.
func fakeCarryClaude(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

type loginSpawn struct {
	home, actor, name, bead string
	err                     error
	calls                   int
}

func (s *loginSpawn) spawn(home, actor, name, bead string) (int, error) {
	s.calls++
	s.home, s.actor, s.name, s.bead = home, actor, name, bead
	if s.err != nil {
		return 0, s.err
	}
	return 4242, nil
}

func userLoginAskOf() store.Ask {
	return store.Ask{Question: "Give ada a login of their own?", To: "mckean",
		Context: []string{"ada builds on the operator's seat today"},
		Run:     &store.AskRun{Argv: []string{"user login ada"}}}
}

func carryOneLogin(t *testing.T, c *carrier) {
	t.Helper()
	did, err := c.carryNext(context.Background())
	if err != nil || !did {
		t.Fatalf("carrying one login out = %v, %v", did, err)
	}
}

// An approved user login is carried out by the server: the folder, the
// env file, the mirrored group and its twin route, the sign-in's bead
// (open, so the daemon offers no operator login card for the new seat)
// and the yardmaster's conversation stand, the sign-in is launched
// detached adopting that bead, and the carry completes without waiting
// for the sign-in.
func TestCarryUserLoginSetup(t *testing.T) {
	fakeCarryClaude(t)
	st, home, bead := userLoginYard(t)
	ctx := context.Background()
	seq := askApprove(t, st, bead, userLoginAskOf())
	if rec := carryState(t, st, bead, seq); rec.State != store.CarryApproved {
		t.Fatalf("the approved record stands %q", rec.State)
	}
	spawn := &loginSpawn{}
	c := testCarrier(st, home, "boot-one")
	c.login = spawn.spawn
	carryOneLogin(t, c)

	folder := filepath.Join(home, "logins", "ada")
	env := filepath.Join(home, "groups", "ada.env")
	if fi, err := os.Stat(folder); err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Errorf("the login folder: %v, %v", fi, err)
	}
	if fi, err := os.Stat(env); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("the env file: %v, %v", fi, err)
	}
	if b, _ := os.ReadFile(env); string(b) != "CLAUDE_CONFIG_DIR="+folder+"\n" {
		t.Errorf("the env file holds %q", b)
	}
	g, err := st.GetGroup(ctx, "ada-builders")
	if err != nil || g.LoginFor() != "ada" || g.Config["env_file"] != env || g.Config["kind"] != "claude" {
		t.Errorf("ada's group = %+v, %v", g, err)
	}
	rs, err := st.Routes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var twins int
	for _, r := range rs {
		if r.Originator != nil && *r.Originator == "ada" && r.Group == "ada-builders" {
			twins++
		}
	}
	if twins != 1 {
		t.Errorf("%d twin routes for ada, want 1", twins)
	}
	ym, err := st.GetCrew(ctx, "yardmaster")
	if err != nil || ym.EnvFor("ada") != env {
		t.Errorf("the yardmaster after: %+v, %v", ym.Config, err)
	}
	beads, err := st.ListBeads(ctx, store.BeadFilter{Type: store.TypeQuestion})
	if err != nil || len(beads) != 1 {
		t.Fatalf("the sign-in beads = %+v, %v", beads, err)
	}
	signIn := beads[0]
	if signIn.Status != "open" || signIn.Originator != "ada" || signIn.Metadata[store.SignInMeta] != "claude" {
		t.Errorf("the sign-in bead = %+v", signIn)
	}
	if !store.SigningIn(beads, "ada", "claude", time.Now()) {
		t.Error("the open bead reads as no sign-in in progress")
	}
	// The sign-in bead must have been opened BEFORE yardmaster was updated,
	// so SigningIn suppresses the operator-login card while ada signs in.
	evs, err := st.Events(ctx, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	var beadCreatedSeq, ymUpdatedSeq uint64
	for _, ev := range evs {
		if ev.Kind == "created" && ev.Bead != nil && *ev.Bead == signIn.ID {
			beadCreatedSeq = ev.Seq
		}
		if ev.Kind == "crew_defined" && ev.Data["crew"] == "yardmaster" {
			ymUpdatedSeq = ev.Seq
		}
	}
	if beadCreatedSeq == 0 || ymUpdatedSeq == 0 || beadCreatedSeq >= ymUpdatedSeq {
		t.Errorf("sign-in bead created at seq %d, yardmaster updated at seq %d: want sign-in opened before yardmaster", beadCreatedSeq, ymUpdatedSeq)
	}
	if spawn.calls != 1 || spawn.name != "ada" || spawn.actor != "mckean" || spawn.bead != signIn.ID || spawn.home != home {
		t.Errorf("the detached sign-in was launched as %+v, want once for ada as mckean adopting %s", spawn, signIn.ID)
	}
	if rec := carryState(t, st, bead, seq); rec.State != store.CarryDone ||
		!strings.Contains(rec.Result, "given a login of their own") ||
		!strings.Contains(rec.Result, "groups ada-builders and 1 twin route") {
		t.Fatalf("the carried record = %+v", rec)
	}

	// Again, once that sign-in has ended: the card now opens the
	// sign-in alone, writes no group or route, and launches a second
	// detached sign-in on a new bead.
	if _, err := st.CloseBead(ctx, signIn.ID, "the sign-in of ada ended", store.Expect{}, "mckean"); err != nil {
		t.Fatal(err)
	}
	seq = askApprove(t, st, bead, userLoginAskOf())
	carryOneLogin(t, c)
	if groups, err := st.Groups(ctx); err != nil || len(groups) != 2 {
		t.Errorf("the groups after again: %+v, %v", groups, err)
	}
	if rs, err := st.Routes(ctx); err != nil || len(rs) != 2 {
		t.Errorf("the routes after again: %d, %v", len(rs), err)
	}
	if spawn.calls != 2 || spawn.bead == signIn.ID {
		t.Errorf("the second launch = %+v, want a new bead", spawn)
	}
	if rec := carryState(t, st, bead, seq); rec.State != store.CarryDone || !strings.Contains(rec.Result, "sign-in opened again") {
		t.Fatalf("the carried record = %+v", rec)
	}
}

// A folder that stands by the time the carry is taken refuses it whole:
// nothing is made, nothing is launched, and the record fails. A launch
// that fails closes the bead it opened and fails the carry.
func TestCarryUserLoginFailures(t *testing.T) {
	fakeCarryClaude(t)
	st, home, bead := userLoginYard(t)
	ctx := context.Background()
	seq := askApprove(t, st, bead, userLoginAskOf())
	if err := os.MkdirAll(filepath.Join(home, "logins", "ada"), 0o700); err != nil {
		t.Fatal(err)
	}
	spawn := &loginSpawn{}
	c := testCarrier(st, home, "boot-one")
	c.login = spawn.spawn
	carryOneLogin(t, c)
	if rec := carryState(t, st, bead, seq); rec.State != store.CarryFailed || !strings.Contains(rec.Result, "exists") {
		t.Fatalf("the failed record = %+v", rec)
	}
	if spawn.calls != 0 {
		t.Errorf("a refused carry launched %d sign-ins", spawn.calls)
	}
	if _, err := st.GetGroup(ctx, "ada-builders"); err == nil {
		t.Error("a refused carry made ada's group")
	}

	st, home, bead = userLoginYard(t)
	seq = askApprove(t, st, bead, userLoginAskOf())
	spawn = &loginSpawn{err: errors.New("no binary")}
	c = testCarrier(st, home, "boot-one")
	c.login = spawn.spawn
	carryOneLogin(t, c)
	if rec := carryState(t, st, bead, seq); rec.State != store.CarryFailed ||
		!strings.Contains(rec.Result, "the login setup for ada stands") ||
		!strings.Contains(rec.Result, "was not launched") {
		t.Fatalf("the failed record = %+v", rec)
	}
	if _, err := st.GetGroup(ctx, "ada-builders"); err != nil {
		t.Errorf("a launch failure undid the setup's group: %v", err)
	}
	for _, path := range []string{filepath.Join(home, "logins", "ada"), filepath.Join(home, "groups", "ada.env")} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("a launch failure undid %s: %v", path, err)
		}
	}
	beads, err := st.ListBeads(ctx, store.BeadFilter{Type: store.TypeQuestion})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range beads {
		if b.Originator == "ada" && b.Status == "open" {
			t.Errorf("the failed launch left its bead open: %+v", b)
		}
	}

	// An env file standing by carry time refuses the setup whole.
	st, home, bead = userLoginYard(t)
	seq = askApprove(t, st, bead, userLoginAskOf())
	if err := os.MkdirAll(filepath.Join(home, "groups"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "groups", "ada.env"), []byte("CLAUDE_CONFIG_DIR=/old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	spawn = &loginSpawn{}
	c = testCarrier(st, home, "boot-one")
	c.login = spawn.spawn
	carryOneLogin(t, c)
	if rec := carryState(t, st, bead, seq); rec.State != store.CarryFailed || !strings.Contains(rec.Result, "exists") {
		t.Fatalf("the existing-env record = %+v", rec)
	}
	if spawn.calls != 0 {
		t.Errorf("an existing env file launched %d sign-ins", spawn.calls)
	}

	// Multiple env files added after a re-login approval fail closed.
	st, home, bead = userLoginYard(t)
	if err := st.PutGroup(ctx, store.Group{Name: "ada-builders", Runner: "herdr", Config: map[string]any{"kind": "claude", "login_for": "ada", "env_file": filepath.Join(home, "groups", "ada.env")}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	seq = askApprove(t, st, bead, userLoginAskOf())
	if err := st.PutGroup(ctx, store.Group{Name: "ada-other", Runner: "herdr", Config: map[string]any{"kind": "claude", "login_for": "ada", "env_file": filepath.Join(home, "groups", "ada-other.env")}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	spawn = &loginSpawn{}
	c = testCarrier(st, home, "boot-one")
	c.login = spawn.spawn
	carryOneLogin(t, c)
	if rec := carryState(t, st, bead, seq); rec.State != store.CarryFailed || !strings.Contains(rec.Result, "different env files") {
		t.Fatalf("the multi-env record = %+v", rec)
	}
	if spawn.calls != 0 {
		t.Errorf("multiple env files launched %d sign-ins", spawn.calls)
	}

	// A re-login approval (not strong) whose login group is deleted before
	// carry must fail closed and NEVER escalate to first-time setup.
	st, home, bead = userLoginYard(t)
	if err := st.PutGroup(ctx, store.Group{Name: "ada-builders", Runner: "herdr", Config: map[string]any{"kind": "claude", "login_for": "ada", "env_file": filepath.Join(home, "groups", "ada.env")}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	seq = askApprove(t, st, bead, userLoginAskOf())
	if _, err := st.DeleteGroup(ctx, "ada-builders", "mckean"); err != nil {
		t.Fatal(err)
	}
	spawn = &loginSpawn{}
	c = testCarrier(st, home, "boot-one")
	c.login = spawn.spawn
	carryOneLogin(t, c)
	if rec := carryState(t, st, bead, seq); rec.State != store.CarryFailed || !strings.Contains(rec.Result, "changed since the card was approved") {
		t.Fatalf("the mode-flipped record = %+v", rec)
	}
	if spawn.calls != 0 {
		t.Errorf("a mode-flipped carry launched %d sign-ins", spawn.calls)
	}
	if _, err := os.Stat(filepath.Join(home, "logins", "ada")); !os.IsNotExist(err) {
		t.Errorf("a mode-flipped re-login created the login folder: %v", err)
	}
	if _, err := st.GetGroup(ctx, "ada-builders"); err == nil {
		t.Error("a mode-flipped re-login created ada-builders")
	}

	// A setup approval whose operator routes drift before carry fails closed.
	st, home, bead = userLoginYard(t)
	seq = askApprove(t, st, bead, userLoginAskOf())
	if _, err := st.AddRoute(ctx, store.Route{Stage: "review", Group: "builders"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	spawn = &loginSpawn{}
	c = testCarrier(st, home, "boot-one")
	c.login = spawn.spawn
	carryOneLogin(t, c)
	if rec := carryState(t, st, bead, seq); rec.State != store.CarryFailed || !strings.Contains(rec.Result, "changed since the card was approved") {
		t.Fatalf("the route-drifted record = %+v", rec)
	}
	if spawn.calls != 0 {
		t.Errorf("a route-drifted carry launched %d sign-ins", spawn.calls)
	}
	if _, err := os.Stat(filepath.Join(home, "logins", "ada")); !os.IsNotExist(err) {
		t.Errorf("a route-drifted carry created the login folder: %v", err)
	}
}

// denyPolicy refuses one named action on one object and hands every
// other gate to the core policy: it makes one write of a carry fail
// where the yard would otherwise allow it.
type denyPolicy struct {
	act  store.Action
	name string
}

func (p denyPolicy) May(ctx context.Context, tx *bolt.Tx, actor string, act store.Action, obj store.Object) error {
	if act == p.act && obj.Name == p.name {
		return fmt.Errorf("%s %s may not be changed", p.act, p.name)
	}
	return store.CorePolicy.May(ctx, tx, actor, act, obj)
}

// A setup that fails at its second twin route (a route planted after
// approval already takes ada's beads for that stage) is undone whole:
// the first twin route already written, the group, the folder and the
// env file all go, the failure says the card may be filed again with no
// taking back, and a card filed again once the plant is gone plans the
// setup again, not a re-login.
func TestCarryUserLoginRouteFailureUndoesSetup(t *testing.T) {
	fakeCarryClaude(t)
	st, home, bead := userLoginYard(t)
	ctx := context.Background()
	if _, err := st.AddRoute(ctx, store.Route{Stage: "review", Group: "builders"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	seq := askApprove(t, st, bead, userLoginAskOf())
	ada := "ada"
	planted, err := st.AddRoute(ctx, store.Route{Stage: "review", Group: "builders", Originator: &ada}, "mckean")
	if err != nil {
		t.Fatal(err)
	}
	spawn := &loginSpawn{}
	c := testCarrier(st, home, "boot-one")
	c.login = spawn.spawn
	carryOneLogin(t, c)
	rec := carryState(t, st, bead, seq)
	if rec.State != store.CarryFailed || !strings.Contains(rec.Result, "a route for ada's beads was not made") ||
		!strings.Contains(rec.Result, "Nothing stands from it, so the card may be filed again") ||
		strings.Contains(rec.Result, "Take back what stands") {
		t.Fatalf("the route-failed record = %+v", rec)
	}
	if spawn.calls != 0 {
		t.Errorf("a route-failed carry launched %d sign-ins", spawn.calls)
	}
	for _, path := range []string{filepath.Join(home, "logins", "ada"), filepath.Join(home, "groups", "ada.env")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("the undone setup left %s: %v", path, err)
		}
	}
	if _, err := st.GetGroup(ctx, "ada-builders"); err == nil {
		t.Error("the undone setup left group ada-builders")
	}
	if rs, err := st.Routes(ctx); err != nil || len(rs) != 3 {
		t.Errorf("the routes after the undo = %+v, %v, want the operator's two and the planted one", rs, err)
	}
	if err := st.DeleteRoute(ctx, planted.ID, "mckean"); err != nil {
		t.Fatal(err)
	}
	seq = askApprove(t, st, bead, userLoginAskOf())
	carryOneLogin(t, c)
	if rec := carryState(t, st, bead, seq); rec.State != store.CarryDone || !strings.Contains(rec.Result, "given a login of their own") {
		t.Fatalf("the re-filed record = %+v", rec)
	}
	if _, err := st.GetGroup(ctx, "ada-builders"); err != nil {
		t.Errorf("the re-filed setup made no group: %v", err)
	}
}

// A setup that fails at its second mirrored group, or at updating the
// yardmaster after all groups, routes and the sign-in bead are written,
// is undone whole: the sign-in bead closes, the routes, groups, env
// file and folder all go, and a card filed again plans the setup again.
func TestCarryUserLoginSecondGroupFailureUndoesSetup(t *testing.T) {
	fakeCarryClaude(t)
	st, home, bead := userLoginYard(t)
	ctx := context.Background()
	if err := st.PutGroup(ctx, store.Group{Name: "workers", Runner: "herdr", Config: map[string]any{"kind": "claude", "prompt": "roles/builder.md"}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddRoute(ctx, store.Route{Stage: "review", Group: "workers"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	seq := askApprove(t, st, bead, userLoginAskOf())
	st.SetPolicy(denyPolicy{act: store.ActChangeGroup, name: "ada-workers"})
	spawn := &loginSpawn{}
	c := testCarrier(st, home, "boot-one")
	c.login = spawn.spawn
	carryOneLogin(t, c)
	st.SetPolicy(nil)
	rec := carryState(t, st, bead, seq)
	if rec.State != store.CarryFailed || !strings.Contains(rec.Result, "group ada-workers was not made") ||
		!strings.Contains(rec.Result, "Nothing stands from it, so the card may be filed again") ||
		strings.Contains(rec.Result, "Take back what stands") {
		t.Fatalf("the group-failed record = %+v", rec)
	}
	if spawn.calls != 0 {
		t.Errorf("a group-failed carry launched %d sign-ins", spawn.calls)
	}
	for _, name := range []string{"ada-builders", "ada-workers"} {
		if _, err := st.GetGroup(ctx, name); err == nil {
			t.Errorf("the undone setup left group %s", name)
		}
	}
	if rs, err := st.Routes(ctx); err != nil || len(rs) != 2 {
		t.Errorf("the routes after the undo = %+v, %v, want the operator's two", rs, err)
	}
	for _, path := range []string{filepath.Join(home, "logins", "ada"), filepath.Join(home, "groups", "ada.env")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("the undone setup left %s: %v", path, err)
		}
	}

	// Failing at serveUserLogin after both groups, both routes and the
	// sign-in bead were written undoes every piece and closes the bead.
	seq = askApprove(t, st, bead, userLoginAskOf())
	st.SetPolicy(denyPolicy{act: store.ActChangeCrew, name: "yardmaster"})
	carryOneLogin(t, c)
	st.SetPolicy(nil)
	rec = carryState(t, st, bead, seq)
	if rec.State != store.CarryFailed || !strings.Contains(rec.Result, "conversation with ada was not served") ||
		!strings.Contains(rec.Result, "Nothing stands from it, so the card may be filed again") ||
		strings.Contains(rec.Result, "Take back what stands") {
		t.Fatalf("the serve-failed record = %+v", rec)
	}
	if spawn.calls != 0 {
		t.Errorf("a serve-failed carry launched %d sign-ins", spawn.calls)
	}
	if beads, err := st.ListBeads(ctx, store.BeadFilter{Type: store.TypeQuestion}); err != nil || store.SigningIn(beads, "ada", "claude", time.Now()) {
		t.Errorf("a serve-failed carry left a sign-in bead open: %+v, %v", beads, err)
	}
	for _, name := range []string{"ada-builders", "ada-workers"} {
		if _, err := st.GetGroup(ctx, name); err == nil {
			t.Errorf("the serve-failed undo left group %s", name)
		}
	}
	if rs, err := st.Routes(ctx); err != nil || len(rs) != 2 {
		t.Errorf("the routes after the serve-failed undo = %+v, %v, want the operator's two", rs, err)
	}
	for _, path := range []string{filepath.Join(home, "logins", "ada"), filepath.Join(home, "groups", "ada.env")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("the serve-failed undo left %s: %v", path, err)
		}
	}

	seq = askApprove(t, st, bead, userLoginAskOf())
	carryOneLogin(t, c)
	if rec := carryState(t, st, bead, seq); rec.State != store.CarryDone || !strings.Contains(rec.Result, "given a login of their own") {
		t.Fatalf("the re-filed record = %+v", rec)
	}
	if _, err := st.GetGroup(ctx, "ada-workers"); err != nil {
		t.Errorf("the re-filed setup made no group ada-workers: %v", err)
	}
}

// A sign-in opened between a card's approval and its carrying out
// fails the carry closed, naming the standing bead: nothing is
// written and no second sign-in is launched. One left open past
// SignInWithin blocks nothing.
func TestCarryUserLoginSignInAlreadyOpen(t *testing.T) {
	fakeCarryClaude(t)
	st, home, bead := userLoginYard(t)
	ctx := context.Background()
	seq := askApprove(t, st, bead, userLoginAskOf())
	standing, err := st.CreateBead(ctx, store.SignInBead("ada", "claude"), "mckean")
	if err != nil {
		t.Fatal(err)
	}
	spawn := &loginSpawn{}
	c := testCarrier(st, home, "boot-one")
	c.login = spawn.spawn
	carryOneLogin(t, c)
	rec := carryState(t, st, bead, seq)
	if rec.State != store.CarryFailed || !strings.Contains(rec.Result, standing.ID) ||
		!strings.Contains(rec.Result, "ends by itself within the hour") ||
		!strings.Contains(rec.Result, "no second sign-in was started and nothing was written") {
		t.Fatalf("the already-signing-in record = %+v", rec)
	}
	if spawn.calls != 0 {
		t.Errorf("a refused carry launched %d sign-ins", spawn.calls)
	}
	if _, err := os.Stat(filepath.Join(home, "logins", "ada")); !os.IsNotExist(err) {
		t.Errorf("a refused carry created the login folder: %v", err)
	}
	if _, err := st.GetGroup(ctx, "ada-builders"); err == nil {
		t.Error("a refused carry made ada's group")
	}

	st, home, bead = userLoginYard(t)
	seq = askApprove(t, st, bead, userLoginAskOf())
	if _, err := st.CreateBead(ctx, store.SignInBead("ada", "claude"), "mckean"); err != nil {
		t.Fatal(err)
	}
	spawn = &loginSpawn{}
	c = testCarrier(st, home, "boot-one")
	c.login = spawn.spawn
	c.clock = func() time.Time { return time.Now().Add(2 * time.Hour) }
	carryOneLogin(t, c)
	if rec := carryState(t, st, bead, seq); rec.State != store.CarryDone || !strings.Contains(rec.Result, "given a login of their own") {
		t.Fatalf("the stale-sign-in record = %+v", rec)
	}
	if spawn.calls != 1 {
		t.Errorf("a stale sign-in blocked the carry: %d sign-ins launched", spawn.calls)
	}
}

// An undo takes the routes, the groups and the env file it is given,
// and leaves a login folder that is not empty standing, named in the
// failure with the card's way back; an undo that empties its list
// names no taking back.
func TestUndoUserLogin(t *testing.T) {
	st, home, _ := userLoginYard(t)
	ctx := context.Background()
	folder := filepath.Join(home, "logins", "ada")
	env := filepath.Join(home, "groups", "ada.env")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "keep.txt"), []byte("theirs\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(env), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env, []byte("CLAUDE_CONFIG_DIR="+folder+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.PutGroup(ctx, store.Group{Name: "ada-builders", Runner: "herdr", Config: map[string]any{"kind": "claude", "login_for": "ada", "env_file": env}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	ada := "ada"
	route, err := st.AddRoute(ctx, store.Route{Stage: "new", Group: "ada-builders", Originator: &ada}, "mckean")
	if err != nil {
		t.Fatal(err)
	}
	c := testCarrier(st, home, "boot-one")
	left := c.undoUserLogin(ctx, userLoginMade{folder: folder, env: env, groups: []string{"ada-builders"}, routes: []string{route.ID}}, "mckean")
	if len(left) != 1 || left[0] != "the login folder "+folder {
		t.Fatalf("the undo left %v, want the login folder alone", left)
	}
	if _, err := st.GetGroup(ctx, "ada-builders"); err == nil {
		t.Error("the undo left group ada-builders")
	}
	if rs, err := st.Routes(ctx); err != nil || len(rs) != 1 {
		t.Errorf("the routes after the undo = %+v, %v, want the operator's one", rs, err)
	}
	if _, err := os.Stat(env); !os.IsNotExist(err) {
		t.Errorf("the undo left the env file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(folder, "keep.txt")); err != nil {
		t.Errorf("the undo took the folder that stands: %v", err)
	}
	if got := userLoginMadeFail("ada", "", "group ada-workers was not made.", left); !strings.Contains(got, "What still stands: the login folder "+folder) ||
		!strings.Contains(got, "Take back what stands with yardr user rm ada.") {
		t.Errorf("a failed undo is worded %q", got)
	}
	if got := userLoginMadeFail("ada", "", "group ada-workers was not made.", nil); !strings.Contains(got, "Nothing stands from it, so the card may be filed again.") ||
		strings.Contains(got, "Take back") {
		t.Errorf("an emptied undo is worded %q", got)
	}
}
