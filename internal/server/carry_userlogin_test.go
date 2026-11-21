package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

	// Again: the card now opens the sign-in alone, writes no group or
	// route, and launches a second detached sign-in on a new bead.
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
	if rec := carryState(t, st, bead, seq); rec.State != store.CarryFailed || !strings.Contains(rec.Result, "was not launched") {
		t.Fatalf("the failed record = %+v", rec)
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
