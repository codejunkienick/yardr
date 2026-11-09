package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// strongHas holds the lines of a card to the sentences it must say, in the
// order they are listed.
func strongHas(t *testing.T, does []string, want ...string) {
	t.Helper()
	joined := strings.Join(does, "\n")
	at := 0
	for _, w := range want {
		i := strings.Index(joined[at:], w)
		if i < 0 {
			t.Errorf("the card does not say %q after %q:\n%s", w, joined[:at], joined)
			continue
		}
		at += i + len(w)
	}
}

// A group set that changes a herdr group's prompt and sets a preset that
// never asks is a weighty code card that shows what stands now and what will,
// each whole. Approving it runs exactly that, as the terminal's group set
// would, and the yardmaster running the same change directly is refused.
func TestRunAskGroupSetCardsThePromptAndAStrongPreset(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	oldText, newText := "You scout.\nLook before you leap.\n", "You scout.\nLook twice.\nNever skip the map.\n"
	_, oldSaved := groupAddFile(t, y, "scout-old", oldText)
	newGiven, newSaved := groupAddFile(t, y, "scout-new", newText)
	scouts := map[string]any{"kind": "claude", "prompt": oldSaved, "args": []any{"--permission-mode", "auto"}}
	for _, name := range []string{"scouts", "typed"} {
		if err := y.PutGroup(y.ctx, Group{Name: name, Runner: "herdr", Config: scouts}, "mckean"); err != nil {
			t.Fatal(err)
		}
	}
	// The words in any order, the yard stores them in its own.
	seq := y.ask(t, groupAddAsk(newText, "group", "set", "scouts", "preset=Bypass", "prompt="+newGiven), "yardmaster")
	r := y.asked(t, seq)
	if want := []string{"group", "set", "scouts", "prompt=" + newGiven, "preset=bypass"}; !slices.Equal(r.Ask.Run.Argv, want) {
		t.Errorf("stored command = %q, want %q", r.Ask.Run.Argv, want)
	}
	if len(r.Ask.Weight) != 1 || r.Ask.Weight[0].Class != WeightCode || !StrongWeight(r.Ask.Weight) {
		t.Fatalf("weight = %+v, want one code line, which is strong", r.Ask.Weight)
	}
	strongHas(t, r.Ask.Run.Does,
		"changes the group scouts on the herdr runner",
		"Its prompt is the file "+oldSaved+", which each session reads when it starts, and which holds now:",
		"Look before you leap.",
		"It will read the file "+newSaved+" when each session starts, and that holds:",
		"Look twice.", "Never skip the map.",
		`It starts claude (Claude Code) agents with "--permission-mode" "auto" (the auto preset) now.`,
		`It will start them with "--dangerously-skip-permissions": the bypass preset, never asks; runs as you with full access.`,
		"It skips permissions: it runs without asking for permission.")
	if r.Ask.Run.Description != "Change some settings of a group and keep the rest" || r.Ask.Run.BackBy != "only the operator" {
		t.Errorf("description %q, taken back by %q", r.Ask.Run.Description, r.Ask.Run.BackBy)
	}
	if back := r.Ask.Run.Back; !strings.HasPrefix(back, "yardr group set scouts ") || !strings.Contains(back, oldSaved) || !strings.HasSuffix(back, `'args=["--permission-mode","auto"]'`) {
		t.Errorf("taken back by %q, want the old prompt and flags put back", back)
	}
	// Its words are a path of the yard's machine: a device that is no
	// operator's is told none of them.
	if !RunArgvHeld(r.Ask.Run.Argv) {
		t.Error("RunArgvHeld does not hold the words of a group set")
	}
	// Run directly, the yardmaster's change is refused whatever it names.
	for _, set := range []map[string]any{{"prompt": newSaved}, {"args": []any{"--dangerously-skip-permissions"}}} {
		if _, err := y.SetGroup(y.ctx, "scouts", GroupEdit{Set: set}, "yardmaster"); err == nil || !strings.Contains(err.Error(), "only the operator") {
			t.Errorf("the yardmaster set %v directly: %v", set, err)
		}
	}
	// A weighty card approved without its person seeing the weight runs
	// nothing.
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil); err == nil || !strings.Contains(err.Error(), "weighty") {
		t.Fatalf("unweighed approval = %v, want a weighty refusal", err)
	}
	if g, _ := y.GetGroup(y.ctx, "scouts"); g.Config["prompt"] != oldSaved {
		t.Fatalf("a refused approval changed the group: %+v", g)
	}
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	strongHas(t, strings.Split(done.Result, "\n"), "group scouts saved",
		`args: ["--permission-mode","auto"] -> ["--dangerously-skip-permissions"]`, "prompt: "+oldSaved+" -> "+newSaved)
	got, err := y.GetGroup(y.ctx, "scouts")
	if err != nil {
		t.Fatal(err)
	}
	if got.Config["prompt"] != newSaved || fmt.Sprint(got.Config["args"]) != "[--dangerously-skip-permissions]" || got.Config["kind"] != "claude" {
		t.Errorf("group = %+v", got)
	}
	// The group is as the terminal's group set leaves its twin.
	if _, err := y.SetGroup(y.ctx, "typed", GroupEdit{Set: map[string]any{"prompt": newSaved, "args": []any{"--dangerously-skip-permissions"}}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if twin, err := y.GetGroup(y.ctx, "typed"); err != nil || !reflect.DeepEqual(asStored(twin.Config), asStored(got.Config)) {
		t.Errorf("typed = %+v, %v; approved = %+v", twin, err, got)
	}
}

// A prompt that stands where the card cannot show it is said so on the card,
// and what the card does show is the text that will run: a relative path is
// read against each depot, so it is no file the yard can open.
func TestRunAskGroupSetSaysWhatItCannotShow(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	text := "You review.\n"
	given, saved := groupAddFile(t, y, "reviewer", text)
	seq := y.ask(t, groupAddAsk(text, "group", "set", "reviewers", "prompt="+given), "yardmaster")
	strongHas(t, y.asked(t, seq).Ask.Run.Does,
		"Its prompt is the file roles/reviewer.md, which each session reads when it starts. This card cannot show it: it is a relative path, read against each depot.",
		"It will read the file "+saved+" when each session starts, and that holds:", "You review.")
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	if g, _ := y.GetGroup(y.ctx, "reviewers"); g.Config["prompt"] != saved {
		t.Errorf("group = %+v", g)
	}
	// A group that has no prompt yet says so.
	if _, err := y.SetGroup(y.ctx, "reviewers", GroupEdit{Unset: []string{"prompt"}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	seq = y.ask(t, groupAddAsk(text, "group", "set", "reviewers", "prompt="+given), "yardmaster")
	strongHas(t, y.asked(t, seq).Ask.Run.Does, "It has no prompt now.", "It will read the file "+saved)
}

// A group set of an exec group's command shows the command now and the one
// it will run, and approving sets exactly it. A command it already runs is
// no card.
func TestRunAskGroupSetCardsACommand(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	seq := y.ask(t, listAsk("mckean", "group", "set", "builders", "cmd=./deploy.sh"), "yardmaster")
	r := y.asked(t, seq)
	strongHas(t, r.Ask.Run.Does, "changes the group builders on the exec runner",
		`Each session runs sh -c with "make build" now.`,
		`Each session will run sh -c with "./deploy.sh", as its program stands when the session starts.`)
	if r.Ask.Run.Back != "yardr group set builders 'cmd=make build'" {
		t.Errorf("taken back by %q", r.Ask.Run.Back)
	}
	if len(r.Ask.Weight) != 1 || r.Ask.Weight[0].Class != WeightCode || !strings.Contains(r.Ask.Weight[0].Line, "its command of the group builders") {
		t.Errorf("weight = %+v", r.Ask.Weight)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	if g, _ := y.GetGroup(y.ctx, "builders"); g.Config["cmd"] != "./deploy.sh" {
		t.Errorf("group = %+v", g)
	}
	_, err := y.AddAsk(y.ctx, y.bead, listAsk("mckean", "group", "set", "builders", "cmd=./deploy.sh"), "yardmaster")
	if err == nil || !strings.Contains(err.Error(), "group builders is set so already, so the command would change nothing") {
		t.Errorf("a command it runs already = %v", err)
	}
	// The operator at a terminal has no card to ask for.
	_, err = y.AddAsk(y.ctx, y.bead, listAsk("mckean", "group", "set", "builders", "cmd=./other.sh"), "mckean")
	if err == nil || !strings.Contains(err.Error(), "needs no approval: run it yourself") {
		t.Errorf("the operator's ask = %v", err)
	}
	// A JSON-looking command is still a string in the terminal's config.
	// The take-back word must preserve that type for ParseSets.
	if _, err := y.SetGroup(y.ctx, "builders", GroupEdit{Set: map[string]any{"cmd": "true"}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	seq = y.ask(t, listAsk("mckean", "group", "set", "builders", "cmd=./restore.sh"), "yardmaster")
	r = y.asked(t, seq)
	if want := `yardr group set builders 'cmd="true"'`; r.Ask.Run.Back != want {
		t.Errorf("take-back command = %q, want %q", r.Ask.Run.Back, want)
	}
	sets, err := ParseSets([]string{`cmd="true"`})
	if err != nil || sets["cmd"] != "true" {
		t.Errorf("taking the command back reads cmd as %v, %v; want the string true", sets["cmd"], err)
	}
}

// An approval is held to the card: the group set since, the file the card
// carries changed since, and the file the card showed as the prompt now
// changed since each leave the ask open and run nothing.
func TestRunAskGroupSetIsHeldToItsCard(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	oldText, newText := "Old.\n", "New.\n"
	_, oldSaved := groupAddFile(t, y, "held-old", oldText)
	newGiven, _ := groupAddFile(t, y, "held-new", newText)
	if err := y.PutGroup(y.ctx, Group{Name: "held", Runner: "herdr", Config: map[string]any{"kind": "claude", "prompt": oldSaved}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	ask := func() uint64 {
		return y.ask(t, groupAddAsk(newText, "group", "set", "held", "prompt="+newGiven, "preset=bypass"), "yardmaster")
	}
	// Set since.
	seq := ask()
	if _, err := y.SetGroup(y.ctx, "held", GroupEdit{Set: map[string]any{"kind": "codex"}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	y.refusedStale(t, seq, "was written when the group held was another: it was set since")
	// The new prompt's file changed since.
	if _, err := y.SetGroup(y.ctx, "held", GroupEdit{Set: map[string]any{"kind": "claude"}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	seq = ask()
	if err := os.WriteFile(newGiven, []byte("Changed.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil || !strings.Contains(err.Error(), "holds other text than the ask carries") {
		t.Fatalf("approving over a changed file = %v", err)
	}
	if err := os.WriteFile(newGiven, []byte(newText), 0o600); err != nil {
		t.Fatal(err)
	}
	// The prompt that stands now was rewritten since: the card said what it
	// held.
	if err := os.WriteFile(oldSaved, []byte("Rewritten.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	y.refusedStale(t, seq, "the card is from before that changed")
	if g, _ := y.GetGroup(y.ctx, "held"); g.Config["prompt"] != oldSaved || g.Config["args"] != nil {
		t.Errorf("a refused approval changed the group: %+v", g)
	}
}

// A group set names the strong few and nothing else: a setting beside them
// that is the operator's alone or the yardmaster's for the operator is
// refused, naming it, and so is a preset that asks as it did, a runner that
// reads none of what it sets, a group the yard has not and a value the
// terminal would store as another type.
func TestRunAskStrongSetRefusals(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	if _, err := y.PutCrew(y.ctx, CrewMember{Name: "clerk", Runner: "herdr", Dir: "/yard", Config: map[string]any{"kind": "codex"}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ command, want string }{
		{"group set reviewers preset=bypass dir=/tmp", "--run takes " + groupSetUse + ": dir is no part of this card, which carries prompt, cmd, preset and nothing else"},
		{"group set reviewers preset=bypass kind=claude", "kind is no part of this card"},
		{"group set reviewers preset=bypass --limit 3", "group set with --limit is no part of this card"},
		{"group set reviewers preset=bypass --unset idle", "group set with --unset is no part of this card"},
		{"group set reviewers preset=bypass preset=bypass", "preset is set twice"},
		{"group set reviewers preset=", "preset needs its value"},
		{"group set reviewers preset=bypass, ", "',' is none of their characters"},
		{"group set preset=bypass reviewers", `"preset=bypass" is no group name`},
		{"group set reviewers preset=5", "preset=5 reads as JSON"},
		{"group set nobody preset=bypass", "group nobody: not found"},
		{"group set reviewers preset=auto", `codex has no permission preset "auto" (it has: approve for me, bypass, read only)`},
		{"group set reviewers preset=approve-for-me", "the codex preset approve for me does not skip the agent's permission prompts, so it is no card of the strong few"},
		{"group set reviewers cmd=./x.sh", "cmd"},
		{"group set builders preset=bypass", "args"},
		{"group set reviewers prompt=/etc/hosts", "the ask carries no prompt text"},
		{"crew set clerk preset=bypass kind=claude", "--run takes " + crewSetUse + ": kind is no part of this card, which carries prompt, preset and nothing else"},
		{"crew set clerk preset=bypass peers=airy", "peers is no part of this card"},
		{"crew set clerk prompt=/yard/x.md cmd=./x.sh", "cmd is no part of this card"},
		{"crew set clerk preset=bypass --unset env_file", "crew set with --unset is no part of this card"},
		{"crew set clerk preset=bypass, ", "',' is none of their characters"},
		{"crew set ghost preset=bypass", "crew member ghost: not found"},
		{"crew set clerk preset=approve-for-me", "does not skip the agent's permission prompts"},
		{"crew set clerk preset=yolo", `codex has no permission preset "yolo"`},
		// A grant, and what the grants row refuses, are the grants row's.
		{"crew set clerk --unset prompt", "prompt is the operator's alone, typed in a terminal and never proposed as a run ask"},
		{"crew set clerk kind=claude", "kind is the operator's alone, typed in a terminal and never proposed as a run ask"},
	} {
		if _, err := y.AddAsk(y.ctx, y.bead, listAsk("mckean", c.command), "yardmaster"); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("--run %q = %v, want %q", c.command, err, c.want)
		}
	}
	if notes, err := y.Notes(y.ctx, y.bead); err != nil || len(notes) != 0 {
		t.Fatalf("the refused asks left %d notes, %v", len(notes), err)
	}
}

// A crew set of a member's prompt and a preset that never asks is a weighty
// code card, and the yardmaster's own entry is proposable on it: the leg of
// delegation never carries that entry, so this card is the only way it is
// proposed. Approving sets exactly what the card showed, as a terminal's
// crew set would.
func TestRunAskCrewSetCardsTheYardmastersOwnEntry(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	text := "You coordinate the yard.\nAsk before you spend.\n"
	given, saved := groupAddFile(t, y, "yardmaster", text)
	for _, name := range []string{"typed"} {
		if _, err := y.PutCrew(y.ctx, CrewMember{Name: name, Runner: "herdr", Dir: "/yard", Config: map[string]any{"kind": "claude", "args": []string{"--careful"}}}, "mckean"); err != nil {
			t.Fatal(err)
		}
	}
	// The yardmaster proposes a change of its own entry.
	seq := y.ask(t, groupAddAsk(text, "crew", "set", "yardmaster", "prompt="+given, "preset=bypass"), "yardmaster")
	r := y.asked(t, seq)
	if want := []string{"crew", "set", "yardmaster", "prompt=" + given, "preset=bypass"}; !slices.Equal(r.Ask.Run.Argv, want) {
		t.Errorf("stored command = %q, want %q", r.Ask.Run.Argv, want)
	}
	if len(r.Ask.Weight) != 1 || r.Ask.Weight[0].Class != WeightCode || !StrongWeight(r.Ask.Weight) {
		t.Fatalf("weight = %+v, want one code line, which is strong", r.Ask.Weight)
	}
	strongHas(t, r.Ask.Run.Does,
		"changes the crew member yardmaster",
		"It has no prompt now.",
		"It will read the file "+saved+" when each session starts, and that holds:", "Ask before you spend.",
		`It starts claude (Claude Code) agents with "--careful" now.`,
		`It will start them with "--dangerously-skip-permissions": the bypass preset`,
		"It skips permissions")
	if r.Ask.Run.Description != "Change some settings of a crew member and keep the rest" {
		t.Errorf("description = %q", r.Ask.Run.Description)
	}
	if back := r.Ask.Run.Back; !strings.HasPrefix(back, "yardr crew set yardmaster ") || !strings.Contains(back, `'args=["--careful"]'`) || !strings.HasSuffix(back, " --unset prompt") {
		t.Errorf("taken back by %q", back)
	}
	if !RunArgvHeld(r.Ask.Run.Argv) || RunArgvHeld([]string{"crew", "set", "yardmaster", "peers=airy"}) || !RunArgvHeld([]string{"crew", "set", "yardmaster", "prompt=/yard/x.md"}) {
		t.Error("RunArgvHeld holds the wrong crew set")
	}
	// The member running it directly, itself or by its pane, is refused.
	def, err := y.GetCrew(y.ctx, "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	def.Config["args"] = []any{"--dangerously-skip-permissions"}
	if _, err := y.PutCrew(y.ctx, *def, "yardmaster"); err == nil || !strings.Contains(err.Error(), "only the operator") {
		t.Errorf("the yardmaster set its own preset directly: %v", err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil); err == nil || !strings.Contains(err.Error(), "weighty") {
		t.Fatalf("unweighed approval = %v, want a weighty refusal", err)
	}
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	strongHas(t, strings.Split(done.Result, "\n"), `args: ["--careful"] -> ["--dangerously-skip-permissions"]`, "prompt: (not set) -> "+saved)
	got, err := y.GetCrew(y.ctx, "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	if got.Config["prompt"] != saved || fmt.Sprint(got.Config["args"]) != "[--dangerously-skip-permissions]" || got.Config["kind"] != "claude" || got.Dir != "/yard" {
		t.Errorf("yardmaster = %+v", got)
	}
	// It is what the terminal's crew set leaves: the same keys written.
	twin, err := y.GetCrew(y.ctx, "typed")
	if err != nil {
		t.Fatal(err)
	}
	twin.Config["prompt"], twin.Config["args"] = saved, []any{"--dangerously-skip-permissions"}
	if _, err := y.PutCrew(y.ctx, *twin, "mckean"); err != nil {
		t.Fatal(err)
	}
	if twin, err = y.GetCrew(y.ctx, "typed"); err != nil || !reflect.DeepEqual(asStored(twin.Config), asStored(got.Config)) {
		t.Errorf("typed = %+v, %v; approved = %+v", twin, err, got)
	}
	// The grants row still proposes a grant, and the strong few are not
	// among its words.
	if _, err := y.AddAsk(y.ctx, y.bead, listAsk("mckean", "crew", "set", "yardmaster", "peers=airy"), "yardmaster"); err != nil {
		t.Errorf("a grant on crew set: %v", err)
	}
}

// A card of the strong few is stale when the member was defined since.
func TestRunAskCrewSetIsHeldToItsCard(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	seq := y.ask(t, listAsk("mckean", "crew", "set", "yardmaster", "preset=bypass"), "yardmaster")
	y.crew(t, map[string]any{"kind": "claude", "args": []string{"--other"}})
	y.refusedStale(t, seq, "was written when the crew member yardmaster was another: its definition was set since")
	if m, _ := y.GetCrew(y.ctx, "yardmaster"); fmt.Sprint(m.Config["args"]) != "[--other]" {
		t.Errorf("a refused approval changed the member: %+v", m)
	}
	// A kind that has no preset of that name refuses where the card is
	// drawn, whoever asks.
	y.crew(t, map[string]any{"kind": "pi"})
	_, err := y.AddAsk(y.ctx, y.bead, listAsk("mckean", "crew", "set", "yardmaster", "preset=bypass"), "yardmaster")
	if err == nil || !strings.Contains(err.Error(), `pi has no permission preset "bypass" (it has: none)`) {
		t.Errorf("a kind with no presets = %v", err)
	}
}

// What a row of the strong few runs is the store's own gate: the policy a
// deployment sets decides it, so a rule that lets someone else define a group
// lets them propose the card as well, and one that refuses the approver
// refuses the approval. The card changes the mark of the gate's refusal and
// nothing else of it.
func TestRunAskStrongSetKeepsTheStoresGate(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	seq := y.ask(t, listAsk("mckean", "group", "set", "builders", "cmd=./deploy.sh"), "yardmaster")
	y.SetPolicy(storePolicyFunc(func(ctx context.Context, tx *bolt.Tx, actor string, act Action, obj Object) error {
		if act == ActChangeGroup && obj.Kind == KindGroup {
			return errors.New("a deployment's rule refuses the group")
		}
		return CorePolicy.May(ctx, tx, actor, act, obj)
	}))
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil || !strings.Contains(err.Error(), "a deployment's rule refuses the group") {
		t.Fatalf("approving under a refusing policy = %v", err)
	}
	if g, _ := y.GetGroup(y.ctx, "builders"); g.Config["cmd"] != "make build" {
		t.Errorf("a refused approval changed the group: %+v", g)
	}
}
