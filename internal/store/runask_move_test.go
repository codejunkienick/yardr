package store

import (
	"slices"
	"strings"
	"testing"
)

// moveAsk is a run ask for the operator that proposes command as one
// word, as the source move tests write it.
func moveAsk(command string) Ask {
	return Ask{Question: "Move them?", To: "mckean", Context: []string{"the source moved to a declared one"},
		Run: &AskRun{Argv: []string{command}}}
}

// recordsYard is a hook yard whose source linear keeps a hook, the identity
// of the crew member yardmaster and the identity of the user ada, and which
// has a second source, lin2, to fold them into.
func recordsYard(t *testing.T) *askYard {
	t.Helper()
	y := hookYard(t)
	if _, err := y.PutSource(y.ctx, "lin2.yaml", strings.Replace(hookLinear, "name: linear", "name: lin2", 1), "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.PutHook(y.ctx, Hook{Name: "linear", Source: "linear", SecretFile: HookSecretFile(y.home, "linear")}, "mckean"); err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]string{"yardmaster": "U1", "ada": "A1"} {
		if err := y.SetIdentity(y.ctx, name, "linear", Identity{ID: id}, "mckean"); err != nil {
			t.Fatal(err)
		}
	}
	return y
}

// A source move card counts each kind of record the fold moves, and is
// weighed by it: trust where a hook goes to the destination, as-user where
// a user's identity does. The operator's approval folds exactly the records
// the card counted, as MoveSourceRecords does, and answers the counts.
func TestRunAskSourceMove(t *testing.T) {
	t.Parallel()
	y := recordsYard(t)
	seq := y.ask(t, moveAsk("yardr source move linear lin2"), "yardmaster")
	got := y.asked(t, seq)
	run := got.Ask.Run
	if want := []string{"source", "move", "linear", "lin2"}; !slices.Equal(run.Argv, want) {
		t.Fatalf("the ask stores %v, want %v", run.Argv, want)
	}
	if want := "folds what the yard keeps under the source linear into the source lin2: 1 hook and 2 identities (1 of crew and 1 of users)."; !slices.Contains(run.Does, want) {
		t.Fatalf("the card says %q, want %q", run.Does, want)
	}
	if run.Back == "" || run.BackBy != "nobody" {
		t.Errorf("the card is taken back by %q, %q", run.Back, run.BackBy)
	}
	var classes []string
	for _, w := range got.Ask.Weight {
		classes = append(classes, w.Class)
	}
	if !slices.Equal(classes, []string{WeightTrust, WeightAsUser}) || !StrongWeight(got.Ask.Weight) {
		t.Fatalf("the ask weighs %v, want trust and as-user", classes)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil); err == nil || !strings.Contains(err.Error(), "weighty (trust, as-user)") {
		t.Fatalf("approving without the weight seen = %v", err)
	}
	if h, err := y.GetHook(y.ctx, "linear"); err != nil || h.Source != "linear" {
		t.Fatalf("the refused approval left the hook %+v, %v", h, err)
	}
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "moved the records of linear to lin2: 1 hook, 2 identities, 0 grants, 0 subscriptions, 0 threads"; done.Result != want {
		t.Fatalf("the approval recorded %q, want %q", done.Result, want)
	}
	if h, err := y.GetHook(y.ctx, "linear"); err != nil || h.Source != "lin2" {
		t.Fatalf("the hook = %+v, %v", h, err)
	}
	m, err := y.GetCrew(y.ctx, "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	if _, left := m.Identities["linear"]; left || m.Identities["lin2"].ID != "U1" {
		t.Fatalf("the crew member's identities = %+v", m.Identities)
	}
	u, err := y.GetUser(y.ctx, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if _, left := u.Identities["linear"]; left || u.Identities["lin2"].ID != "A1" {
		t.Fatalf("the user's identities = %+v", u.Identities)
	}
}

// A source move ask is refused, storing nothing, for what the row refuses
// and the fold refuses: a destination the yard does not declare, the same
// source twice, a name no source has, a source that keeps nothing, an
// identity the two sources hold and disagree on, the wrong number of words,
// an asker who may fold the records themselves, and a writer who is no one
// of the yard.
func TestRunAskSourceMoveRefusals(t *testing.T) {
	t.Parallel()
	y := recordsYard(t)
	if err := y.SetIdentity(y.ctx, "yardmaster", "lin2", Identity{ID: "U2"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, command, asker, want string
	}{
		{"a destination the yard does not declare", "source move linear ghost", "yardmaster", "source ghost: not found"},
		{"itself", "source move linear linear", "yardmaster", "move records from linear to itself: give two sources"},
		{"a name no source has", "source move linear Lin_2", "yardmaster", `source "Lin_2": use lowercase letters`},
		{"a clash of identities", "source move linear lin2", "yardmaster", "crew member yardmaster has one linear identity and another lin2 identity"},
		{"one word", "source move linear", "yardmaster", "two sources, not 1 words"},
		{"three words", "source move linear lin2 crm", "yardmaster", "two sources, not 3 words"},
		{"the operator asks", "source move linear lin2", "mckean", "needs no approval: run it yourself"},
		{"nobody", "source move linear lin2", "stranger", "a command for the operator to approve is proposed by a crew member or a user of the yard"},
	} {
		if _, err := y.AddAsk(y.ctx, y.bead, moveAsk(c.command), c.asker); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s = %v, want %q", c.name, err, c.want)
		}
	}
	// A source that keeps nothing folds nothing, so there is no card.
	y = recordsYard(t)
	if _, err := y.PutSource(y.ctx, "lin3.yaml", strings.Replace(hookLinear, "name: linear", "name: lin3", 1), "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.AddAsk(y.ctx, y.bead, moveAsk("source move lin3 linear"), "yardmaster"); err == nil ||
		!strings.Contains(err.Error(), "nothing is kept under the source lin3, so the command would change nothing") {
		t.Errorf("a source that keeps nothing = %v", err)
	}
	if notes, err := y.Notes(y.ctx, y.bead); err != nil || len(notes) != 0 {
		t.Fatalf("the refused asks left %d notes, %v", len(notes), err)
	}
}

// An approval is held to the counts the card showed: another record under the
// source since, or a destination replaced, is another card, and nothing is
// folded.
func TestRunAskSourceMoveStale(t *testing.T) {
	t.Parallel()
	y := recordsYard(t)
	seq := y.ask(t, moveAsk("source move linear lin2"), "yardmaster")
	if _, err := y.PutHook(y.ctx, Hook{Name: "second", Source: "linear", SecretFile: HookSecretFile(y.home, "second")}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil ||
		!strings.Contains(err.Error(), "was written when the yard kept other records under the source linear") {
		t.Fatalf("approving with a record added since = %v", err)
	}
	seq = y.ask(t, moveAsk("source move linear lin2"), "yardmaster")
	if _, err := y.PutSource(y.ctx, "lin2.yaml", strings.Replace(strings.Replace(hookLinear, "name: linear", "name: lin2", 1), "api.linear.app", "api.linear.example.net", 1), "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil ||
		!strings.Contains(err.Error(), "was written from another declaration of the source lin2") {
		t.Fatalf("approving with the destination replaced = %v", err)
	}
	for _, name := range []string{"linear", "second"} {
		if h, err := y.GetHook(y.ctx, name); err != nil || h.Source != "linear" {
			t.Fatalf("the refused approvals left the hook %s as %+v, %v", name, h, err)
		}
	}
}
