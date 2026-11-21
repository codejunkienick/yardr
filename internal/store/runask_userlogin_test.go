package store

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func userLoginAsk(command, to string) Ask {
	return Ask{Question: "Give ada a login of their own?", To: to, Context: []string{"ada builds on the operator's seat today"},
		Run: &AskRun{Argv: []string{command}}}
}

// fakeLoginClaude puts a stand-in claude on PATH, so the shared login
// planner finds a sign-in to lead. The tests that use it stay
// sequential: PATH is the process's.
func fakeLoginClaude(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// loginYardRoutes gives the yard of y an operator route to a herdr
// group, which a first login copies for its user.
func loginYardRoutes(t *testing.T, y *askYard) {
	t.Helper()
	if err := y.PutGroup(y.ctx, Group{Name: "builders", Runner: "herdr", Config: map[string]any{"kind": "claude", "prompt": "roles/builder.md"}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.AddRoute(y.ctx, Route{Stage: "new", Group: "builders"}, "mckean"); err != nil {
		t.Fatal(err)
	}
}

// A user login card for a user with no login of their own shows the
// whole first-time setup, weighs code (strong) and as-user, and its
// approval records CarryPending with a carrying record, running nothing
// itself.
func TestRunAskUserLoginSetup(t *testing.T) {
	fakeLoginClaude(t)
	y, _ := runYard(t)
	loginYardRoutes(t, y)
	seq := y.ask(t, userLoginAsk("user login ada", "mckean"), "yardmaster")
	got := y.asked(t, seq)
	run := got.Ask.Run
	if want := []string{"user", "login", "ada"}; !slices.Equal(run.Argv, want) {
		t.Fatalf("the ask stores %v, want %v", run.Argv, want)
	}
	if run.Description != userLoginDescription {
		t.Errorf("description %q", run.Description)
	}
	folder := filepath.Join(y.home, "logins", "ada")
	env := filepath.Join(y.home, "groups", "ada.env")
	joined := strings.Join(run.Does, "\n")
	for _, want := range []string{
		"gives ada a login of their own (Claude): the folder " + folder + " (0700) and the env file " + env + " (0600)",
		"ada-builders (kind claude, login_for ada, the env file, limit 1)",
		"1 twin route for ada's beads",
		"yardmaster serves ada on their login (env_for_ada)",
		"their sign-in is opened",
		"runs detached, as its own command, so approving does not wait for it",
		"the login folder and the env file stay on disk",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the card does not say %q; it says %q", want, run.Does)
		}
	}
	if run.Back != "yardr user rm ada" || run.BackBy != "only the operator" {
		t.Errorf("the card is taken back by %q, %q", run.Back, run.BackBy)
	}
	for _, key := range []string{"user ada", "user ada login", "login folder " + folder, "login env " + env, "mirrored groups", "twin routes", "yardmaster yardmaster"} {
		if run.Seen[key] == "" {
			t.Errorf("the card pins nothing under %q", key)
		}
	}
	if len(got.Ask.Weight) != 2 || got.Ask.Weight[0].Class != WeightCode || got.Ask.Weight[1].Class != WeightAsUser || !StrongWeight(got.Ask.Weight) {
		t.Fatalf("the ask weighs %+v, want code (strong) and as-user", got.Ask.Weight)
	}
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if done.Result != CarryPending {
		t.Fatalf("the approval recorded %q, want CarryPending", done.Result)
	}
	rec, err := y.CarryOf(y.ctx, y.bead, seq)
	if err != nil || rec == nil || rec.State != CarryApproved || rec.Actor != "mckean" {
		t.Fatalf("the carrying record = %+v, %v", rec, err)
	}
	if groups, err := y.Groups(y.ctx); err != nil || len(groups) != 1 {
		t.Fatalf("the approval made groups %+v, %v: it runs nothing itself", groups, err)
	}
}

// A user login card for a user whose login stands shows its env file,
// weighs as-user alone, and names user uninvite as its way back where
// the mapping maps the user.
func TestRunAskUserLoginAgain(t *testing.T) {
	y, _ := runYard(t)
	env := filepath.Join(y.home, "groups", "ada.env")
	if err := y.PutGroup(y.ctx, Group{Name: "ada-builders", Runner: "herdr", Config: map[string]any{"kind": "claude", "login_for": "ada", "env_file": env}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if err := y.SetSetting(y.ctx, WebJWTUsersSetting, "ada@example.net=ada", "mckean"); err != nil {
		t.Fatal(err)
	}
	seq := y.ask(t, userLoginAsk("user login ada", "mckean"), "yardmaster")
	got := y.asked(t, seq)
	run := got.Ask.Run
	joined := strings.Join(run.Does, "\n")
	if !strings.Contains(joined, "opens ada's sign-in again, on the login the env file "+env+" names") {
		t.Errorf("the card says %q", run.Does)
	}
	if run.Back != "yardr user uninvite ada" {
		t.Errorf("the card is taken back by %q, want the uninvite of a mapped teammate", run.Back)
	}
	if len(got.Ask.Weight) != 1 || got.Ask.Weight[0].Class != WeightAsUser || StrongWeight(got.Ask.Weight) {
		t.Fatalf("the ask weighs %+v, want as-user alone", got.Ask.Weight)
	}
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
	if err != nil || done.Result != CarryPending {
		t.Fatalf("the approval = %+v, %v, want CarryPending", done, err)
	}
}

// A user login ask is refused, storing nothing, for what the row
// refuses: a user the yard has not, a login on several env files, a
// folder that stands, the operator as the asker, and a member as the
// one asked.
func TestRunAskUserLoginRefusals(t *testing.T) {
	fakeLoginClaude(t)
	y, _ := runYard(t)
	loginYardRoutes(t, y)
	if err := y.PutGroup(y.ctx, Group{Name: "ada-other", Runner: "herdr", Config: map[string]any{"kind": "claude", "login_for": "ada", "env_file": filepath.Join(y.home, "groups", "ada-other.env")}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if err := y.PutGroup(y.ctx, Group{Name: "ada-builders", Runner: "herdr", Config: map[string]any{"kind": "claude", "login_for": "ada", "env_file": filepath.Join(y.home, "groups", "ada.env")}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, command, to, asker, want string
	}{
		{"no such user", "user login ghost", "mckean", "yardmaster", "user ghost: not found"},
		{"two env files", "user login ada", "mckean", "yardmaster", "different env files"},
		{"the operator asks", "user login ada", "mckean", "mckean", "needs no approval: run it yourself"},
		{"a member is asked", "user login ada", "ada", "yardmaster", "a command is the operator's to approve (mckean)"},
	} {
		if _, err := y.AddAsk(y.ctx, y.bead, userLoginAsk(c.command, c.to), c.asker); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s = %v, want %q", c.name, err, c.want)
		}
	}
	if _, err := y.DeleteGroup(y.ctx, "ada-other", "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.DeleteGroup(y.ctx, "ada-builders", "mckean"); err != nil {
		t.Fatal(err)
	}
	if err := y.PutGroup(y.ctx, Group{Name: "ada-empty", Runner: "herdr", Config: map[string]any{"kind": "claude", "login_for": "ada"}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.AddAsk(y.ctx, y.bead, userLoginAsk("user login ada", "mckean"), "yardmaster"); err == nil ||
		!strings.Contains(err.Error(), "no env_file") {
		t.Errorf("a login group with no env_file = %v, want the refusal", err)
	}
	if _, err := y.DeleteGroup(y.ctx, "ada-empty", "mckean"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(y.home, "logins", "ada"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := y.AddAsk(y.ctx, y.bead, userLoginAsk("user login ada", "mckean"), "yardmaster"); err == nil ||
		!strings.Contains(err.Error(), "exists") {
		t.Errorf("a login over a folder that stands = %v, want the planner's refusal", err)
	}
	if err := os.Remove(filepath.Join(y.home, "logins", "ada")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(y.home, "groups"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(y.home, "groups", "ada.env"), []byte("CLAUDE_CONFIG_DIR=/old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := y.AddAsk(y.ctx, y.bead, userLoginAsk("user login ada", "mckean"), "yardmaster"); err == nil ||
		!strings.Contains(err.Error(), "exists") {
		t.Errorf("a login over an env file that stands = %v, want the planner's refusal", err)
	}
	if notes, err := y.Notes(y.ctx, y.bead); err != nil || len(notes) != 0 {
		t.Fatalf("the refused asks left %d notes, %v", len(notes), err)
	}
}

// An approval is held to what its card pinned: a login group made since
// flips the mode, a changed yardmaster moves its conversation, a route
// type or env file appearing fails closed, and a yardmaster already
// serving the user does not duplicate them in for=.
func TestRunAskUserLoginStale(t *testing.T) {
	fakeLoginClaude(t)
	y, _ := runYard(t)
	loginYardRoutes(t, y)
	seq := y.ask(t, userLoginAsk("user login ada", "mckean"), "yardmaster")
	env := filepath.Join(y.home, "groups", "ada.env")
	if err := y.PutGroup(y.ctx, Group{Name: "ada-spares", Runner: "herdr", Config: map[string]any{"kind": "claude", "login_for": "ada", "env_file": env}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil ||
		!strings.Contains(err.Error(), "ada's login stood otherwise") {
		t.Fatalf("approving a first login after one stands = %v", err)
	}
	if _, err := y.DeleteGroup(y.ctx, "ada-spares", "mckean"); err != nil {
		t.Fatal(err)
	}
	seq = y.ask(t, userLoginAsk("user login ada", "mckean"), "yardmaster")
	ym, err := y.GetCrew(y.ctx, "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	def := *ym
	def.Config = map[string]any{"for": "ada"}
	if _, err := y.PutCrew(y.ctx, def, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil ||
		!strings.Contains(err.Error(), "the yardmaster's conversation with ada stood otherwise") {
		t.Fatalf("approving after the yardmaster changed = %v", err)
	}
	// With ada already in yardmaster's for=, the card does not duplicate her.
	seq = y.ask(t, userLoginAsk("user login ada", "mckean"), "yardmaster")
	got := y.asked(t, seq)
	if joined := strings.Join(got.Ask.Run.Does, "\n"); strings.Contains(joined, "joins its for=") {
		t.Errorf("yardmaster already serving ada still proposes adding her to for=: %q", joined)
	}
	// Adding a typed operator route after asking makes the card stale.
	bug := "bug"
	if _, err := y.AddRoute(y.ctx, Route{Stage: "new", Type: &bug, Group: "builders"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil ||
		!strings.Contains(err.Error(), "groups or routes a first login of ada copies stood otherwise") {
		t.Fatalf("approving after a typed route was added = %v", err)
	}
	// An env file appearing after asking fails closed at approval.
	seq = y.ask(t, userLoginAsk("user login ada", "mckean"), "yardmaster")
	if err := os.MkdirAll(filepath.Dir(env), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env, []byte("CLAUDE_CONFIG_DIR=/old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil ||
		!strings.Contains(err.Error(), "exists") {
		t.Fatalf("approving after env file appeared = %v", err)
	}
}
