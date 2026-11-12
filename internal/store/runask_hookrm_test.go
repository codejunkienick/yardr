package store

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// hookRmYard is a hook yard with two hooks: linear, as a hook add card makes
// it, and crm, which stands apart from that shape (a secret file of the
// operator's, and a line it says of a bead). linear has an issue move still
// to make.
func hookRmYard(t *testing.T) *askYard {
	t.Helper()
	y := hookYard(t)
	if _, err := y.PutHook(y.ctx, Hook{Name: "linear", Source: "linear", SecretFile: HookSecretFile(y.home, "linear")}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.PutHook(y.ctx, Hook{Name: "crm", Source: "crm", SecretFile: filepath.Join(t.TempDir(), "crm.secret"),
		Says: []Say{{Where: map[string]string{"stage": "review"}, Outcome: "done"}}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if err := y.write(func(w *wtx) error {
		return put(w.Tx, bIssueMoves, "1/linear", &IssueMove{ID: "1/linear", Seq: 1, Hook: "linear", At: time.Now()})
	}); err != nil {
		t.Fatal(err)
	}
	return y
}

func hookRmAsk(command string) Ask {
	return Ask{Question: "Remove the hook?", To: "mckean", Context: []string{"the yard stopped hearing the source"},
		Run: &AskRun{Argv: []string{command}}}
}

// A hook rm card is weighed trust, so strong: the hook goes with what it was
// set to do, the issue moves it still had are counted and dropped, only the
// hook named is removed, and the approval is held to the hook as the card
// saw it. It is the way back from a hook add, and says so: a hook made as
// the card makes it is added again by that card, one set to do more comes
// back on a card carrying its table, and only a secret file of the
// operator's is typed in a terminal.
func TestRunAskHookRm(t *testing.T) {
	t.Parallel()
	y := hookRmYard(t)
	seq := y.ask(t, hookRmAsk("yardr hook rm linear"), "yardmaster")
	got := y.asked(t, seq)
	run := got.Ask.Run
	if want := []string{"hook", "rm", "linear"}; !slices.Equal(run.Argv, want) {
		t.Fatalf("the ask stores %v, want %v", run.Argv, want)
	}
	if run.Description != "Delete a hook (events it accepted are kept)" {
		t.Errorf("description %q", run.Description)
	}
	for _, want := range []string{
		"removes the hook linear: POST /hooks/linear takes no more deliveries from the source linear",
		"it drops the 1 issue move it still had to make, which is not made later.",
		"Its signing secret stays in " + HookSecretFile(y.home, "linear") + ": no card removes a file.",
	} {
		if !strings.Contains(strings.Join(run.Does, "\n"), want) {
			t.Errorf("the card does not say %q; it says %q", want, run.Does)
		}
	}
	if want := "yardr hook add linear --source linear (its signing secret is given again on the hook's own secret card)"; run.Back != want || run.BackBy != "only the operator" {
		t.Errorf("the card is taken back by %q, %q; want %q", run.Back, run.BackBy, want)
	}
	if len(got.Ask.Weight) != 1 || got.Ask.Weight[0].Class != WeightTrust || !StrongWeight(got.Ask.Weight) {
		t.Fatalf("the ask weighs %+v, want trust, which is strong", got.Ask.Weight)
	}
	t.Run("registration address uses the source host", func(t *testing.T) {
		var hits []registerHit
		status := 200
		y := registerYard(t, &hits, &status)
		src, err := y.GetSource(y.ctx, "bot")
		if err != nil {
			t.Fatal(err)
		}
		text := strings.Replace(src.Text, "credential:", "base_template: https://{host}/api\ncredential:", 1)
		if _, err := y.PutSource(y.ctx, "bot.yaml", text, "mckean"); err != nil {
			t.Fatal(err)
		}
		if _, err := y.SetSource(y.ctx, "bot", SourceHost, "nas.local", "mckean"); err != nil {
			t.Fatal(err)
		}
		if _, err := y.PutHook(y.ctx, Hook{Name: "bot", Source: "bot", SecretFile: HookSecretFile(y.home, "bot")}, "mckean"); err != nil {
			t.Fatal(err)
		}
		seq := y.ask(t, hookRmAsk("hook rm bot"), "yardmaster")
		line := strings.Join(y.asked(t, seq).Ask.Run.Does, "\n")
		if want := "The webhook the yard registered at https://nas.local/api (host nas.local) for it stays registered there"; !strings.Contains(line, want) {
			t.Fatalf("the card says %q, want it to name %q", line, want)
		}
	})
	// ApproveAsk holds the hook's lock across the approval, as DeleteHook
	// does, for this ask and no other.
	if name, ok := y.hookRmTarget(y.bead, seq); !ok || name != "linear" {
		t.Errorf("the hook the ask removes = %q, %v", name, ok)
	}
	if other := y.ask(t, runAsk("peer allow airy --mail-limit 300"), "yardmaster"); true {
		if name, ok := y.hookRmTarget(y.bead, other); ok {
			t.Errorf("a peer allow ask removes the hook %q", name)
		}
	}
	// A weighty card is approved where its weight is shown.
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil); err == nil || !strings.Contains(err.Error(), "weighty (trust)") {
		t.Fatalf("approving without the weight seen = %v", err)
	}
	if _, err := y.GetHook(y.ctx, "linear"); err != nil {
		t.Fatalf("the refused approval removed the hook: %v", err)
	}
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if done.Result != "linear: removed" {
		t.Fatalf("the approval recorded %q", done.Result)
	}
	if _, err := y.GetHook(y.ctx, "linear"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the hook still stands: %v", err)
	}
	if _, err := y.GetHook(y.ctx, "crm"); err != nil {
		t.Fatalf("the other hook was removed with it: %v", err)
	}
	if moves, err := y.IssueMoves(y.ctx); err != nil || len(moves) != 0 {
		t.Fatalf("the hook's issue moves = %+v, %v", moves, err)
	}
	evs, err := y.EventsOfKind(y.ctx, "hook_removed", 2)
	if err != nil || len(evs) != 1 || evs[0].Data["hook"] != "linear" || evs[0].Data["issue_moves_dropped"] != float64(1) {
		t.Fatalf("hook_removed events = %+v, %v", evs, err)
	}
	y.approved(t, evs[0], seq)

	// The hook that stands apart with a secret file of the operator's is
	// described by what goes with it, and no card brings it back.
	seq = y.ask(t, hookRmAsk("hook rm crm"), "ada")
	run = y.asked(t, seq).Ask.Run
	if !slices.Contains(run.Does, "What it was set to do goes with it: 1 line it says of a bead.") {
		t.Errorf("the card says %q", run.Does)
	}
	if !strings.HasPrefix(run.Back, "no card brings it back") || run.BackBy != "nobody" {
		t.Errorf("the card is taken back by %q, %q", run.Back, run.BackBy)
	}

	// One set to do more, with the file the yard owns for it, comes back
	// on a hook add card carrying its table.
	if _, err := y.PutHook(y.ctx, Hook{Name: "tab", Source: "linear", SecretFile: HookSecretFile(y.home, "tab"),
		Says: []Say{{Where: map[string]string{"stage": "review"}, Outcome: "done"}}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	seq = y.ask(t, hookRmAsk("hook rm tab"), "ada")
	run = y.asked(t, seq).Ask.Run
	want := "a hook add card carrying the table that stood brings it back: yardr bead ask <bead> - with Q: <question> and R: yardr hook add tab --source linear and S: and M: lines of that table (the hook_added event holds what stood) on stdin (its signing secret is given again on the hook's own secret card)"
	if run.Back != want || run.BackBy != "only the operator" {
		t.Errorf("the card is taken back by %q, %q; want %q", run.Back, run.BackBy, want)
	}

	t.Run("source removed", func(t *testing.T) {
		y := hookRmYard(t)
		if _, err := y.PutHook(y.ctx, Hook{Name: "tab", Source: "linear", SecretFile: HookSecretFile(y.home, "tab"),
			Says: []Say{{Where: map[string]string{"stage": "review"}, Outcome: "done"}}}, "mckean"); err != nil {
			t.Fatal(err)
		}
		if err := y.DeleteSource(y.ctx, "linear", "mckean"); err != nil {
			t.Fatal(err)
		}
		for _, command := range []string{"hook rm linear", "hook rm tab"} {
			seq := y.ask(t, hookRmAsk(command), "yardmaster")
			run := y.asked(t, seq).Ask.Run
			want := "no hook add card brings it back: the source linear is gone; add it and its hook declaration in a terminal first"
			if run.Back != want || run.BackBy != "only the operator" {
				t.Errorf("%s is taken back by %q, %q; want %q", command, run.Back, run.BackBy, want)
			}
		}
	})

	t.Run("source no longer declares a hook", func(t *testing.T) {
		y := hookRmYard(t)
		withoutHook := `name: linear
base: https://api.linear.app
credential: { env: LINEAR_API_KEY }
calls:
  viewer: { kind: read, request: { method: GET, path: /viewer } }
`
		if _, err := y.PutSource(y.ctx, "linear.yaml", withoutHook, "mckean"); err != nil {
			t.Fatal(err)
		}
		seq := y.ask(t, hookRmAsk("hook rm linear"), "yardmaster")
		run := y.asked(t, seq).Ask.Run
		want := "no hook add card brings it back: the source linear has no hook declaration; add it in a terminal first"
		if run.Back != want || run.BackBy != "only the operator" {
			t.Errorf("the card is taken back by %q, %q; want %q", run.Back, run.BackBy, want)
		}
	})

	t.Run("source no longer hears by identity", func(t *testing.T) {
		y, _ := runYard(t)
		identityDeclaration := hookLinear + `hear:
  field: team
  by: identity
`
		if _, err := y.PutSource(y.ctx, "linear.yaml", identityDeclaration, "mckean"); err != nil {
			t.Fatal(err)
		}
		if _, err := y.PutHook(y.ctx, Hook{Name: "linear", Source: "linear", SecretFile: HookSecretFile(y.home, "linear")}, "mckean"); err != nil {
			t.Fatal(err)
		}
		if _, err := y.PutHook(y.ctx, Hook{Name: "tab", Source: "linear", SecretFile: HookSecretFile(y.home, "tab"),
			Says: []Say{{Where: map[string]string{"stage": "review"}, Outcome: "done"}}}, "mckean"); err != nil {
			t.Fatal(err)
		}
		if _, err := y.PutSource(y.ctx, "linear.yaml", hookLinear, "mckean"); err != nil {
			t.Fatal(err)
		}
		seq := y.ask(t, hookRmAsk("hook rm linear"), "yardmaster")
		run := y.asked(t, seq).Ask.Run
		want := "no hook add card brings it back: the source linear no longer hears by identity, so its --unknown setting cannot be restored; restore identity hearing in a terminal first"
		if run.Back != want || run.BackBy != "only the operator" {
			t.Errorf("the card is taken back by %q, %q; want %q", run.Back, run.BackBy, want)
		}
		seq = y.ask(t, hookRmAsk("hook rm tab"), "yardmaster")
		run = y.asked(t, seq).Ask.Run
		if run.Back != want || run.BackBy != "only the operator" {
			t.Errorf("the table card is taken back by %q, %q; want %q", run.Back, run.BackBy, want)
		}
	})

	t.Run("source registers its own secret", func(t *testing.T) {
		var hits []registerHit
		status := 200
		y := registerYard(t, &hits, &status)
		if _, err := y.PutHook(y.ctx, Hook{Name: "bot", Source: "bot", SecretFile: HookSecretFile(y.home, "bot")}, "mckean"); err != nil {
			t.Fatal(err)
		}
		seq := y.ask(t, hookRmAsk("hook rm bot"), "yardmaster")
		run := y.asked(t, seq).Ask.Run
		want := "yardr hook add bot --source bot (the yard makes the signing secret again when it registers the hook)"
		if run.Back != want || run.BackBy != "only the operator" {
			t.Errorf("the card is taken back by %q, %q; want %q", run.Back, run.BackBy, want)
		}
	})

	t.Run("registration address unset", func(t *testing.T) {
		var hits []registerHit
		status := 200
		y := registerYard(t, &hits, &status)
		if _, err := y.PutHook(y.ctx, Hook{Name: "bot", Source: "bot", SecretFile: HookSecretFile(y.home, "bot")}, "mckean"); err != nil {
			t.Fatal(err)
		}
		if err := y.SetSetting(y.ctx, HookPublicURLSetting, "", "mckean"); err != nil {
			t.Fatal(err)
		}
		seq := y.ask(t, hookRmAsk("hook rm bot"), "yardmaster")
		run := y.asked(t, seq).Ask.Run
		want := "no hook add card brings it back: hook_public_url has no valid public hook address; set one in a terminal first"
		if run.Back != want || run.BackBy != "only the operator" {
			t.Errorf("the card is taken back by %q, %q; want %q", run.Back, run.BackBy, want)
		}
		if _, err := y.PutHook(y.ctx, Hook{Name: "bot-table", Source: "bot", SecretFile: HookSecretFile(y.home, "bot-table"),
			Says: []Say{{Where: map[string]string{"stage": "review"}, Outcome: "done"}}}, "mckean"); err != nil {
			t.Fatal(err)
		}
		seq = y.ask(t, hookRmAsk("hook rm bot-table"), "yardmaster")
		run = y.asked(t, seq).Ask.Run
		if run.Back != want || run.BackBy != "only the operator" {
			t.Errorf("the table card is taken back by %q, %q; want %q", run.Back, run.BackBy, want)
		}
	})
}

// A hook rm ask is refused, storing nothing, for what the row refuses: a
// hook the yard has not, a flag or more than one hook, an asker who may
// remove it themselves, and a writer that is no one of the yard.
func TestRunAskHookRmRefusals(t *testing.T) {
	t.Parallel()
	y := hookRmYard(t)
	for _, c := range []struct {
		name, command, asker, want string
	}{
		{"no such hook", "hook rm ghost", "yardmaster", "hook ghost: not found"},
		{"a flag", "hook rm --force linear", "yardmaster", "one hook, not 2 words"},
		{"only a flag", "hook rm -f", "yardmaster", `"-f" is no hook name, and hook rm takes no flag`},
		{"two hooks", "hook rm linear crm", "yardmaster", "one hook, not 2 words"},
		{"none", "hook rm", "yardmaster", "one hook, not 0 words"},
		{"the operator asks", "hook rm linear", "mckean", "yardr hook rm linear needs no approval: run it yourself"},
		{"nobody", "hook rm linear", "stranger", "a command for the operator to approve is proposed by a crew member or a user of the yard"},
	} {
		if _, err := y.AddAsk(y.ctx, y.bead, hookRmAsk(c.command), c.asker); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s = %v, want %q", c.name, err, c.want)
		}
	}
	if notes, err := y.Notes(y.ctx, y.bead); err != nil || len(notes) != 0 {
		t.Fatalf("the refused asks left %d notes, %v", len(notes), err)
	}
}

// An approval is held to the hook the card named: one added again under the
// name with another source or another secret file is another hook, and one
// removed since leaves nothing to approve. Either way nothing runs and the
// other hook stands.
func TestRunAskHookRmStale(t *testing.T) {
	t.Parallel()
	y := hookRmYard(t)
	seq := y.ask(t, hookRmAsk("hook rm linear"), "yardmaster")
	if _, err := y.PutHook(y.ctx, Hook{Name: "linear", Source: "crm", SecretFile: HookSecretFile(y.home, "linear")}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil ||
		!strings.Contains(err.Error(), "was written when the hook linear was another") {
		t.Fatalf("approving stale = %v", err)
	}
	if h, err := y.GetHook(y.ctx, "linear"); err != nil || h.Source != "crm" {
		t.Fatalf("the refused approval left %+v, %v", h, err)
	}
	if err := y.DeleteHook(y.ctx, "linear", "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil || !strings.Contains(err.Error(), "hook linear: not found") {
		t.Fatalf("approving a hook removed since = %v", err)
	}
	if _, err := y.GetHook(y.ctx, "crm"); err != nil {
		t.Fatalf("the other hook was removed: %v", err)
	}
}
