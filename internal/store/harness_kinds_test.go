package store

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// skipsPrompts says whether arg is a flag that skips an agent's permission
// prompts (design yardr-m90n 3.5): the --dangerously- flags,
// --always-approve, --yolo, --trust-all-tools and its --trust-tools form,
// --disable-approval, and --auto, which the files of kimi and opencode both
// give the meaning of asking nothing (claude's own auto is the value of
// --permission-mode, no flag of this name). The test names them itself and
// reads none from the code it holds, so the mark cannot be right by
// following a list that is wrong.
func skipsPrompts(arg string) bool {
	if strings.HasPrefix(arg, "--dangerously-") || strings.HasPrefix(arg, "--trust-tools") {
		return true
	}
	return slices.Contains([]string{"--always-approve", "--yolo", "--auto", "--trust-all-tools", "--disable-approval"}, arg)
}

// strongMarkProblems are the presets of presets whose Strong mark is not
// what their flags say: one that passes a flag that skips the agent's
// permission prompts without the mark, and one marked Strong that passes
// none.
func strongMarkProblems(presets map[string][]Permission) []string {
	var problems []string
	for kind, list := range presets {
		for _, p := range list {
			var skipping []string
			for _, arg := range p.Args {
				if skipsPrompts(arg) {
					skipping = append(skipping, arg)
				}
			}
			switch {
			case len(skipping) > 0 && !p.Strong:
				problems = append(problems, fmt.Sprintf("%s preset %q passes %s, which skips the agent's permission prompts, and is not marked Strong", kind, p.Label, strings.Join(skipping, " ")))
			case len(skipping) == 0 && p.Strong:
				problems = append(problems, fmt.Sprintf("%s preset %q is marked Strong and passes no flag that skips the agent's permission prompts", kind, p.Label))
			}
		}
	}
	slices.Sort(problems)
	return problems
}

// A preset is Strong exactly when its flags skip the agent's permission
// prompts, so a preset added later cannot reach a group or a crew member
// without the card that approves it, by omission or by a mark it does not
// earn (design yardr-m90n 3.5). The check is run on the table and on a
// doctored one, which it must fail twice, once for each way to be wrong.
func TestHarnessPresetsAreStrongWhenTheirFlagsAskNothing(t *testing.T) {
	t.Parallel()
	if problems := strongMarkProblems(HarnessPresets); len(problems) > 0 {
		t.Errorf("HarnessPresets: %s", strings.Join(problems, "; "))
	}
	// The lookup the cards use hands the mark on with the preset.
	for _, c := range []struct {
		kind, label string
		strong      bool
	}{{"claude", "bypass", true}, {"claude", "auto", false}, {"codex", "approve for me", false}, {"kimi", "auto", true}} {
		if p, ok := HarnessPreset(c.kind, c.label); !ok || p.Strong != c.strong {
			t.Errorf("HarnessPreset(%q, %q) = %+v, %v, want Strong %v", c.kind, c.label, p, ok, c.strong)
		}
	}
	doctored := map[string][]Permission{"x": {
		{Label: "quiet", Args: []string{"--yolo"}},
		{Label: "careful", Args: []string{"--permission-mode", "auto"}, Strong: true},
		{Label: "asks", Args: []string{"--permission-mode", "acceptEdits"}},
		{Label: "trusting", Args: []string{"--trust-tools=*"}, Strong: true},
	}}
	want := []string{
		`x preset "careful" is marked Strong and passes no flag that skips the agent's permission prompts`,
		`x preset "quiet" passes --yolo, which skips the agent's permission prompts, and is not marked Strong`,
	}
	if got := strongMarkProblems(doctored); !slices.Equal(got, want) {
		t.Errorf("strongMarkProblems(doctored) = %q, want %q", got, want)
	}
	for _, arg := range []string{"--dangerously-skip-permissions", "--dangerously-bypass-approvals-and-sandbox", "--always-approve", "--yolo", "--auto", "--trust-all-tools", "--trust-tools=*", "--trust-tools", "--disable-approval"} {
		if !skipsPrompts(arg) {
			t.Errorf("%s is not read as skipping the agent's permission prompts", arg)
		}
	}
	for _, arg := range []string{"--permission-mode", "auto", "acceptEdits", "--approve-for-me", "--settings", "--enable", "-c", "--model"} {
		if skipsPrompts(arg) {
			t.Errorf("%s is read as skipping the agent's permission prompts", arg)
		}
	}
}
