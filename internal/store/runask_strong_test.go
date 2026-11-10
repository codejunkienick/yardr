package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

// strongLacks holds the lines of a card to the sentences it must not say.
func strongLacks(t *testing.T, does []string, unwanted ...string) {
	t.Helper()
	joined := strings.Join(does, "\n")
	for _, u := range unwanted {
		if strings.Contains(joined, u) {
			t.Errorf("the card says %q:\n%s", u, joined)
		}
	}
}

// rewrittenSaid is what a prompt card says of a file the yard wrote that
// holds other text than its name says.
const rewrittenSaid = "this file no longer holds the text its name says. It holds now:"

// A group set that changes a herdr group's prompt and sets a preset that
// never asks is a weighty code card that shows what stands now and what will,
// each whole. Approving it runs exactly that, as the terminal's group set
// would, and the yardmaster running the same change directly is refused. The
// prompt it sets is a file the yard writes from the text the card showed, so a
// write to the file the ask named reaches no session; the file of the prompt
// before stays for the card's Back to name, and a file the yard wrote is no
// file to propose.
func TestRunAskGroupSetCardsThePromptAndAStrongPreset(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	oldText, newText := "You scout.\nLook before you leap.\n", "You scout.\nLook twice.\nNever skip the map.\n"
	_, oldSaved := groupAddFile(t, y, "scout-old", oldText)
	newGiven, _ := groupAddFile(t, y, "scout-new", newText)
	newYard := yardPromptFile(t, y, "scouts", newText)
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
		"On approval the yard writes the text below to the file "+newYard+", read-only.",
		"Look twice.", "Never skip the map.",
		`It starts claude (Claude Code) agents with "--permission-mode" "auto" (the auto preset) now.`,
		`It will start them with "--dangerously-skip-permissions": the bypass preset, never asks; runs as you with full access.`,
		"It skips permissions: it runs without asking for permission.")
	// A path the yard never wrote is shown as it was.
	strongLacks(t, r.Ask.Run.Does, rewrittenSaid)
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
	for _, set := range []map[string]any{{"prompt": newYard}, {"args": []any{"--dangerously-skip-permissions"}}} {
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
	if _, err := os.Lstat(newYard); !os.IsNotExist(err) {
		t.Fatalf("a refused approval wrote the yard's prompt file: %v", err)
	}
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	strongHas(t, strings.Split(done.Result, "\n"), "group scouts saved",
		`args: ["--permission-mode","auto"] -> ["--dangerously-skip-permissions"]`, "prompt: "+oldSaved+" -> "+newYard)
	got, err := y.GetGroup(y.ctx, "scouts")
	if err != nil {
		t.Fatal(err)
	}
	if got.Config["prompt"] != newYard || fmt.Sprint(got.Config["args"]) != "[--dangerously-skip-permissions]" || got.Config["kind"] != "claude" {
		t.Errorf("group = %+v", got)
	}
	yardPromptHolds(t, newYard, newText)
	// The group is as the terminal's group set leaves its twin.
	if _, err := y.SetGroup(y.ctx, "typed", GroupEdit{Set: map[string]any{"prompt": newYard, "args": []any{"--dangerously-skip-permissions"}}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if twin, err := y.GetGroup(y.ctx, "typed"); err != nil || !reflect.DeepEqual(asStored(twin.Config), asStored(got.Config)) {
		t.Errorf("typed = %+v, %v; approved = %+v", twin, err, got)
	}
	// The file the ask named is where the text was read from: a write to it
	// after the approval reaches no session, which read the yard's own.
	if err := os.WriteFile(newGiven, []byte("Overwritten.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if g, _ := y.GetGroup(y.ctx, "scouts"); g.Config["prompt"] != newYard {
		t.Errorf("group after a write to the named file = %+v", g)
	}
	yardPromptHolds(t, newYard, newText)
	// A prompt after it is another file, and the card's Back names the one
	// before, which stays as it was written.
	thirdText := "You scout.\nCount your steps.\n"
	thirdGiven, _ := groupAddFile(t, y, "scout-third", thirdText)
	thirdYard := yardPromptFile(t, y, "scouts", thirdText)
	seq = y.ask(t, groupAddAsk(thirdText, "group", "set", "scouts", "prompt="+thirdGiven), "yardmaster")
	r = y.asked(t, seq)
	if back := r.Ask.Run.Back; back != "yardr group set scouts prompt="+newYard {
		t.Errorf("taken back by %q, want the file of the prompt before put back", back)
	}
	strongHas(t, r.Ask.Run.Does, "Its prompt is the file "+newYard+", which each session reads when it starts, and which holds now:",
		"Look twice.", "On approval the yard writes the text below to the file "+thirdYard+", read-only.", "Count your steps.")
	// A file the yard wrote that holds what its name says is shown as it was.
	strongLacks(t, r.Ask.Run.Does, rewrittenSaid)
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	if g, _ := y.GetGroup(y.ctx, "scouts"); g.Config["prompt"] != thirdYard {
		t.Errorf("group = %+v, want the third prompt", g)
	}
	yardPromptHolds(t, thirdYard, thirdText)
	yardPromptHolds(t, newYard, newText)
	// The text before comes back: its file stands with exactly that text and
	// is kept, not written again.
	if err := os.WriteFile(newGiven, []byte(newText), 0o600); err != nil {
		t.Fatal(err)
	}
	seq = y.ask(t, groupAddAsk(newText, "group", "set", "scouts", "prompt="+newGiven), "yardmaster")
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	if g, _ := y.GetGroup(y.ctx, "scouts"); g.Config["prompt"] != newYard {
		t.Errorf("group = %+v, want the prompt that came back", g)
	}
	yardPromptHolds(t, newYard, newText)
	// A file the yard wrote is no file to propose, rewritten or not: it
	// stays as it was written, and a rewritten one is never read as both the
	// prompt that runs and the one asked for.
	if err := os.Chmod(newYard, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newYard, []byte("Rewritten.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ file, text string }{{newYard, "Rewritten.\n"}, {thirdYard, thirdText}} {
		_, err := y.AddAsk(y.ctx, y.bead, groupAddAsk(c.text, "group", "set", "scouts", "prompt="+c.file), "yardmaster")
		if err == nil || !strings.Contains(err.Error(), "is one the yard wrote when it approved a prompt") {
			t.Errorf("a proposal naming %s = %v, want it refused", c.file, err)
		}
	}
	// The same text from another file is how a rewrite was passed off: the
	// card showed it as the prompt that stood and as the one asked for, the
	// same, with nothing to say it was not what the yard wrote. The card now
	// says so above the text it holds. It is no refusal, so the approval of
	// the card as shown still goes through.
	rewrittenGiven, _ := groupAddFile(t, y, "scout-rewritten", "Rewritten.\n")
	rewrittenYard := yardPromptFile(t, y, "scouts", "Rewritten.\n")
	seq = y.ask(t, groupAddAsk("Rewritten.\n", "group", "set", "scouts", "prompt="+rewrittenGiven), "yardmaster")
	strongHas(t, y.asked(t, seq).Ask.Run.Does,
		"Its prompt is the file "+newYard+", which each session reads when it starts.", rewrittenSaid, "Rewritten.",
		"On approval the yard writes the text below to the file "+rewrittenYard+", read-only.", "Rewritten.")
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	if g, _ := y.GetGroup(y.ctx, "scouts"); g.Config["prompt"] != rewrittenYard {
		t.Errorf("group = %+v, want the prompt the card showed", g)
	}
	yardPromptHolds(t, rewrittenYard, "Rewritten.\n")
}

// A prompt that stands where the card cannot show it is said so on the card,
// and what the card does show is the text that will run: a relative path is
// read against each depot, so it is no file the yard can open.
func TestRunAskGroupSetSaysWhatItCannotShow(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	text := "You review.\n"
	given, _ := groupAddFile(t, y, "reviewer", text)
	yardFile := yardPromptFile(t, y, "reviewers", text)
	seq := y.ask(t, groupAddAsk(text, "group", "set", "reviewers", "prompt="+given), "yardmaster")
	strongHas(t, y.asked(t, seq).Ask.Run.Does,
		"Its prompt is the file roles/reviewer.md, which each session reads when it starts. This card cannot show it: it is a relative path, read against each depot.",
		"On approval the yard writes the text below to the file "+yardFile+", read-only.", "You review.")
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	if g, _ := y.GetGroup(y.ctx, "reviewers"); g.Config["prompt"] != yardFile {
		t.Errorf("group = %+v", g)
	}
	// A group that has no prompt yet says so.
	if _, err := y.SetGroup(y.ctx, "reviewers", GroupEdit{Unset: []string{"prompt"}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	seq = y.ask(t, groupAddAsk(text, "group", "set", "reviewers", "prompt="+given), "yardmaster")
	strongHas(t, y.asked(t, seq).Ask.Run.Does, "It has no prompt now.", "On approval the yard writes the text below to the file "+yardFile)
	// A digest-named prompt is said to have changed even when its text is
	// hidden from the card or longer than the card can hold. Hashing the whole
	// file must not make the card carry more than its limit.
	current := yardFile
	for _, c := range []struct {
		name, rewrite, why string
	}{
		{"hidden", "You review.\n\x1bhidden\n", "it is not text a card shows as written"},
		{"long", strings.Repeat("x", MaxRunCrewRole+1), fmt.Sprintf("it is longer than the %d bytes a card shows", MaxRunCrewRole)},
	} {
		if err := os.Chmod(current, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(current, []byte(c.rewrite), 0o600); err != nil {
			t.Fatal(err)
		}
		group, err := y.GetGroup(y.ctx, "reviewers")
		if err != nil {
			t.Fatal(err)
		}
		if group.Config["prompt"] != current {
			if _, err := y.SetGroup(y.ctx, "reviewers", GroupEdit{Set: map[string]any{"prompt": current}}, "mckean"); err != nil {
				t.Fatal(err)
			}
		}
		nextText := "You review again: " + c.name + ".\n"
		nextGiven, _ := groupAddFile(t, y, "reviewer-"+c.name, nextText)
		nextYard := yardPromptFile(t, y, "reviewers", nextText)
		seq = y.ask(t, groupAddAsk(nextText, "group", "set", "reviewers", "prompt="+nextGiven), "yardmaster")
		strongHas(t, y.asked(t, seq).Ask.Run.Does,
			"Its prompt is the file "+current+", which each session reads when it starts.",
			"this file no longer holds the text its name says.",
			"This card cannot show it: "+c.why+".",
			"On approval the yard writes the text below to the file "+nextYard+", read-only.")
		if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err != nil {
			t.Fatal(err)
		}
		current = nextYard
	}
	// Replacing the digest-named file with a symlink to another regular file
	// under the home is a rewrite of the named prompt too.
	linked := filepath.Join(y.Store.home, "roles", "ordinary-prompt.md")
	if err := os.WriteFile(linked, []byte("A linked prompt.\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(current); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(linked, current); err != nil {
		t.Fatal(err)
	}
	nextText := "You review the linked prompt.\n"
	nextGiven, _ := groupAddFile(t, y, "reviewer-linked", nextText)
	seq = y.ask(t, groupAddAsk(nextText, "group", "set", "reviewers", "prompt="+nextGiven), "yardmaster")
	strongHas(t, y.asked(t, seq).Ask.Run.Does,
		"Its prompt is the file "+current+", which each session reads when it starts.", rewrittenSaid, "A linked prompt.")
}

// A group set card shows a command or effort now and the one it will run
// with, and approving sets exactly it. A value it already holds is no card.
func TestRunAskGroupSetCardsACommandAndEffort(t *testing.T) {
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

	// Effort is the operator's, as the args it was a flag of are, so the
	// yardmaster proposes it and the operator approves it. Its flags are on
	// the card, its take-back restores what the group held, and the card is
	// stale once the group was set since.
	seq = y.ask(t, listAsk("mckean", "group", "set", "reviewers", "effort=max"), "yardmaster")
	r = y.asked(t, seq)
	strongHas(t, r.Ask.Run.Does, "changes the group reviewers on the herdr runner",
		"It sets no effort now, so each session starts at its harness's own.",
		`Each session will start at the effort max: "-c" "model_reasoning_effort=max" are added after its other flags.`)
	if len(r.Ask.Weight) != 0 {
		t.Errorf("weight = %+v, want none for an effort alone", r.Ask.Weight)
	}
	if r.Ask.Run.Back != "yardr group set reviewers --unset effort" || r.Ask.Run.BackBy != "only the operator" {
		t.Errorf("taken back by %q (%s), want the effort unset", r.Ask.Run.Back, r.Ask.Run.BackBy)
	}
	// Run directly, the yardmaster's is refused, with a word for any effort.
	if _, err := y.SetGroup(y.ctx, "reviewers", GroupEdit{Set: map[string]any{"effort": "max"}}, "yardmaster"); err == nil || !strings.Contains(err.Error(), "only the operator") {
		t.Errorf("the yardmaster set the effort directly: %v", err)
	}
	// A weightless card needs no sight of its weight.
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "group reviewers saved\n  effort: (not set) -> max"; done.Result != want {
		t.Errorf("approval printed %q, want %q", done.Result, want)
	}
	if g, _ := y.GetGroup(y.ctx, "reviewers"); g.Config["effort"] != "max" || g.Config["kind"] != "codex" || g.Config["prompt"] != "roles/reviewer.md" {
		t.Errorf("group = %+v, want the effort set and the rest kept", g)
	}
	// Another level shows the one that stands, and is taken back to it.
	seq = y.ask(t, listAsk("mckean", "group", "set", "reviewers", "effort=high"), "yardmaster")
	r = y.asked(t, seq)
	strongHas(t, r.Ask.Run.Does, "Each session starts at the effort max now.",
		`Each session will start at the effort high: "-c" "model_reasoning_effort=high" are added`)
	if r.Ask.Run.Back != "yardr group set reviewers effort=max" {
		t.Errorf("taken back by %q", r.Ask.Run.Back)
	}
	// Set since, it is stale; one it holds already changes nothing.
	if _, err := y.SetGroup(y.ctx, "reviewers", GroupEdit{Set: map[string]any{"effort": "low"}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	y.refusedStale(t, seq, "was written when the group reviewers was another: it was set since")
	if _, err := y.AddAsk(y.ctx, y.bead, listAsk("mckean", "group", "set", "reviewers", "effort=low"), "yardmaster"); err == nil || !strings.Contains(err.Error(), "is set so already, so the command would change nothing") {
		t.Errorf("an effort it holds already = %v", err)
	}
	// Beside a prompt or a command it rides on that card, which weighs as it
	// did and names only what it runs.
	seq = y.ask(t, listAsk("mckean", "group", "set", "builders", "cmd=./deploy.sh"), "yardmaster")
	if r = y.asked(t, seq); len(r.Ask.Weight) != 1 || strings.Contains(r.Ask.Weight[0].Line, "effort") {
		t.Errorf("weight = %+v", r.Ask.Weight)
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
	// A file put since at the name the yard writes the prompt to is not the
	// text the card showed, and the approval does not use it: it is refused
	// and runs nothing, whether the file holds other text or is a symlink to
	// the very text, and goes through once the file is gone.
	seq = ask()
	yardFile := yardPromptFile(t, y, "held", newText)
	if err := os.WriteFile(yardFile, []byte("Planted.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil || !strings.Contains(err.Error(), "holds other text than its name says") || !strings.Contains(err.Error(), "Nothing ran") {
		t.Fatalf("approving over a planted file = %v", err)
	}
	if err := os.Remove(yardFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(newGiven, yardFile); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil || !strings.Contains(err.Error(), "is not one it can use") {
		t.Fatalf("approving over a symlink at the yard's name = %v", err)
	}
	if g, _ := y.GetGroup(y.ctx, "held"); g.Config["prompt"] != oldSaved || g.Config["args"] != nil {
		t.Errorf("a refused approval changed the group: %+v", g)
	}
	if err := os.Remove(yardFile); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	if g, _ := y.GetGroup(y.ctx, "held"); g.Config["prompt"] != yardFile {
		t.Errorf("group = %+v, want the yard's file", g)
	}
	yardPromptHolds(t, yardFile, newText)
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
	for _, g := range []Group{
		{Name: "plain", Runner: "herdr", Config: map[string]any{"kind": "pi"}},
		{Name: "heavy", Runner: "herdr", Config: map[string]any{"kind": "codex", "args": []any{"-c", "model_reasoning_effort=high"}}},
	} {
		if err := y.PutGroup(y.ctx, g, "mckean"); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct{ command, want string }{
		{"group set reviewers preset=bypass dir=/tmp", "--run takes " + groupSetUse + ": dir is no part of this card, which carries prompt, cmd, preset, effort and nothing else"},
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
		// An effort is a level of the group's kind, for a kind that takes
		// one, which its args do not pass already, on a runner that reads it.
		{"group set reviewers effort=turbo", `effort "turbo" is no level codex takes (it takes: low, medium, high, xhigh, max, ultra)`},
		{"group set reviewers effort=", "effort needs its value"},
		{"group set reviewers effort=5", "effort=5 reads as JSON"},
		{"group set reviewers effort=max effort=low", "effort is set twice"},
		{"group set builders effort=max", `unknown config key "effort": nothing reads it. A group of the exec runner takes:`},
		{"group set plain effort=max", "effort is for the kinds that take one (claude, codex, muse), and pi is not"},
		{"group set heavy effort=max", `its args already pass "model_reasoning_effort=high", which effort sets: remove it from args first`},
		{"group set builders preset=bypass", "args"},
		{"group set reviewers prompt=/etc/hosts", "the ask carries no prompt text"},
		{"crew set clerk preset=bypass kind=claude", "kind is on this card for the yardmaster's own entry alone, which the delegated leg never carries: " + strongApart},
		{"crew set clerk model=opus", "model is on this card for the yardmaster's own entry alone"},
		{"crew set clerk dir=/yard", "dir is on this card for the yardmaster's own entry alone"},
		{"crew set yardmaster preset=bypass peers=airy", "--run takes " + crewSetUse + ": peers is no part of this card, which carries prompt, preset, kind, model, dir and nothing else"},
		{"crew set yardmaster kind=nosuch", `kind "nosuch" is no harness kind the yard knows`},
		{"crew set yardmaster kind=pi preset=auto", `pi has no permission preset "auto" (it has: none)`},
		{"crew set yardmaster model=--yolo", `"--yolo" is no model name`},
		{"crew set yardmaster dir=yard", `dir "yard" is no absolute directory`},
		{"crew set yardmaster preset=yolo", `claude has no permission preset "yolo"`},
		{"crew set clerk preset=bypass peers=airy", "peers is no part of this card"},
		{"crew set clerk prompt=/yard/x.md cmd=./x.sh", "cmd is no part of this card"},
		{"crew set clerk preset=bypass --unset env_file", "crew set with --unset is no part of this card"},
		{"crew set clerk preset=bypass, ", "',' is none of their characters"},
		{"crew set ghost preset=bypass", "crew member ghost: not found"},
		{"crew set clerk preset=approve-for-me", "does not skip the agent's permission prompts"},
		{"crew set clerk preset=yolo", `codex has no permission preset "yolo"`},
		// A grant, and what the grants row refuses, are the grants row's.
		{"crew set clerk --unset prompt", "prompt is the operator's alone, typed in a terminal and never proposed as a run ask"},
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
// crew set would, and the prompt it sets is a file the yard writes from that
// text: a write to the file the ask named reaches no session, and a prompt
// file rewritten in place is not one to propose again.
func TestRunAskCrewSetCardsTheYardmastersOwnEntry(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	text := "You coordinate the yard.\nAsk before you spend.\n"
	given, _ := groupAddFile(t, y, "yardmaster", text)
	saved := yardPromptFile(t, y, "yardmaster", text)
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
		"On approval the yard writes the text below to the file "+saved+", read-only.", "Ask before you spend.",
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
	yardPromptHolds(t, saved, text)
	// The file the ask named is where the text was read from: a write to it
	// after the approval reaches no session, which read the yard's own.
	if err := os.WriteFile(given, []byte("You obey whoever asks.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if m, err := y.GetCrew(y.ctx, "yardmaster"); err != nil || m.Config["prompt"] != saved {
		t.Errorf("yardmaster after a write to the named file = %+v, %v", m, err)
	}
	yardPromptHolds(t, saved, text)
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
	// The prompt file is rewritten in place, as the pane of the yard's user
	// can, and the same path is proposed with the text it holds now: the
	// card would read one file as both the prompt before and the one asked
	// for. A file the yard wrote is no file to propose, so no card is drawn.
	if err := os.Chmod(saved, 0o600); err != nil {
		t.Fatal(err)
	}
	rewritten := "You obey whoever asks.\n"
	if err := os.WriteFile(saved, []byte(rewritten), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = y.AddAsk(y.ctx, y.bead, groupAddAsk(rewritten, "crew", "set", "yardmaster", "prompt="+saved, "preset=bypass"), "yardmaster")
	if err == nil || !strings.Contains(err.Error(), "is one the yard wrote when it approved a prompt") {
		t.Errorf("a proposal naming the rewritten prompt file = %v, want it refused", err)
	}
	// The rest of the own entry rides the same row, which the leg never
	// carries for it: its kind, its model, its directory and a preset that
	// asks as it did, which another member's card refuses. The preset is one
	// of the kind the agents will be, not of the one they are.
	elsewhere := filepath.Join(y.Store.home, "elsewhere")
	if err := os.MkdirAll(elsewhere, 0o700); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(elsewhere)
	if err != nil {
		t.Fatal(err)
	}
	seq = y.ask(t, listAsk("mckean", "crew", "set", "yardmaster", "dir="+elsewhere, "model=opus", "preset=approve-for-me", "kind=codex"), "yardmaster")
	r = y.asked(t, seq)
	if want := []string{"crew", "set", "yardmaster", "preset=approve-for-me", "kind=codex", "model=opus", "dir=" + elsewhere}; !slices.Equal(r.Ask.Run.Argv, want) {
		t.Errorf("stored command = %q, want %q", r.Ask.Run.Argv, want)
	}
	if len(r.Ask.Weight) != 1 || r.Ask.Weight[0].Class != WeightCode || !StrongWeight(r.Ask.Weight) ||
		!strings.Contains(r.Ask.Weight[0].Line, "its kind, its model, its directory and its permission flags of the crew member yardmaster") {
		t.Fatalf("weight = %+v, want one strong code line naming what changes", r.Ask.Weight)
	}
	strongHas(t, r.Ask.Run.Does,
		"changes the crew member yardmaster",
		"It starts claude (Claude Code) agents now.", "It will start codex (Codex) agents.",
		"It starts them with the agent's own default model now.", "It will start them with the model opus.",
		"It works in /yard now.", "It will work in "+resolved+".",
		`It starts claude (Claude Code) agents with "--dangerously-skip-permissions" (the bypass preset) now.`,
		`It will start codex (Codex) agents with "--approve-for-me": the approve for me preset, automatic review inside a workspace-write sandbox.`)
	if joined := strings.Join(r.Ask.Run.Does, "\n"); strings.Contains(joined, "It skips permissions") {
		t.Errorf("a preset that asks is said to skip permissions:\n%s", joined)
	}
	if want := `yardr crew set yardmaster 'args=["--dangerously-skip-permissions"]' kind=claude --dir /yard --unset model`; r.Ask.Run.Back != want {
		t.Errorf("taken back by %q, want %q", r.Ask.Run.Back, want)
	}
	if !RunArgvHeld(r.Ask.Run.Argv) || !RunArgvHeld([]string{"crew", "set", "yardmaster", "dir=/yard/x"}) || RunArgvHeld([]string{"crew", "set", "yardmaster", "kind=codex", "model=opus"}) {
		t.Error("RunArgvHeld holds the wrong crew set: a directory is a path of the yard's machine, a kind and a model are not")
	}
	// The member as it stands is not asked about again: the kind lines say
	// the flags stay where the card sets no preset.
	if kindOnly := y.ask(t, listAsk("mckean", "crew", "set", "yardmaster", "kind=pi"), "yardmaster"); kindOnly == seq {
		t.Fatal("two asks share a seq")
	} else {
		strongHas(t, y.asked(t, kindOnly).Ask.Run.Does, "It will start pi (pi) agents.",
			`It starts them with "--dangerously-skip-permissions", and this card does not change those flags.`)
	}
	done, err = y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	strongHas(t, strings.Split(done.Result, "\n"), "crew member yardmaster saved", "  --dir: /yard -> "+resolved,
		`  args: ["--dangerously-skip-permissions"] -> ["--approve-for-me"]`, "  kind: claude -> codex", "  model: (not set) -> opus")
	got, err = y.GetCrew(y.ctx, "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	if got.Dir != resolved || got.Config["kind"] != "codex" || got.Config["model"] != "opus" || fmt.Sprint(got.Config["args"]) != "[--approve-for-me]" || got.Config["prompt"] != saved {
		t.Errorf("yardmaster = %+v, want the card's kind, model, directory and flags and the prompt it had", got)
	}
	// A model on its own is what the gate lets anyone set of their own
	// entry, so it needs no card: the row changes the mark of the gate's
	// refusal and nothing else of it.
	if _, err := y.AddAsk(y.ctx, y.bead, listAsk("mckean", "crew", "set", "yardmaster", "model=sonnet"), "yardmaster"); err == nil || !strings.Contains(err.Error(), "needs no approval: run it yourself") {
		t.Errorf("a model on its own = %v, want it to need no approval", err)
	}
	// Another member's are the leg's or the operator's, on this card too.
	if _, err := y.AddAsk(y.ctx, y.bead, listAsk("mckean", "crew", "set", "typed", "kind=codex", "preset=bypass"), "yardmaster"); err == nil || !strings.Contains(err.Error(), "kind is on this card for the yardmaster's own entry alone") {
		t.Errorf("another member's kind = %v, want it refused", err)
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
