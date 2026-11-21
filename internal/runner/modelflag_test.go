package runner

import (
	"slices"
	"testing"

	"yardr.dev/yardr/internal/dispatch"
)

// A model is refused when the kind's file names neither a flag nor a
// model environment for it. A kind with a flag, one with an environment,
// one with no file, and no model at all pass through. No kind in the
// catalogue names neither today, so the refused rows are the write-time
// table's (TestModelProblemsFlaglessKindWithoutEnv in internal/store);
// these rows hold the env kind to passing where it refused before.
func TestModelFlagRefused(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name           string
		subject, whose string
		cfg            map[string]any
		override, bead string
		want           string
	}{
		{"no model", "group g", "the group's kind", map[string]any{"kind": "opencode"}, "", "", ""},
		{"a flag", "group g", "the group's kind", map[string]any{"kind": "claude", "model": "opus"}, "", "", ""},
		{"no kind is claude's", "group g", "the group's kind", map[string]any{"model": "opus"}, "", "", ""},
		{"no file keeps the default", "group g", "the group's kind", map[string]any{"kind": "gemini", "model": "flash"}, "", "", ""},
		{"the group's, by environment", "group g", "the group's kind", map[string]any{"kind": "opencode", "model": "x"}, "", "", ""},
		{"the bead's, by environment", "group g", "the group's kind", map[string]any{"kind": "opencode"}, "opus", "yardr-1", ""},
		{"a crew member's, by environment", "crew member r", "the crew member's kind", map[string]any{"kind": "opencode", "model": "x"}, "", "", ""},
	} {
		err := modelFlagRefused(c.subject, c.whose, c.cfg, c.override, c.bead)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v, want none", c.name, err)
		case c.want != "" && (err == nil || !dispatch.IsBlocked(err) || err.Error() != c.want):
			t.Errorf("%s: %v, want Blocked %q", c.name, err, c.want)
		}
	}
}

// The entry a start sets for a kind that takes its model in the
// environment is the file's variable rendered with the group's model,
// the bead's override first; no model, a flag kind and a kind with no
// file set none.
func TestModelEnv(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		cfg      map[string]any
		override string
		want     []string
	}{
		{"the group's model", map[string]any{"kind": "opencode", "model": "opencode/big-pickle"}, "", []string{`OPENCODE_CONFIG_CONTENT={"model":"opencode/big-pickle"}`}},
		{"the bead's model first", map[string]any{"kind": "opencode", "model": "opencode/big-pickle"}, "other/model", []string{`OPENCODE_CONFIG_CONTENT={"model":"other/model"}`}},
		{"no model", map[string]any{"kind": "opencode"}, "", nil},
		{"a flag kind", map[string]any{"kind": "claude", "model": "opus"}, "", nil},
		{"no kind is claude's", map[string]any{"model": "opus"}, "", nil},
		{"a kind with no file", map[string]any{"kind": "gemini", "model": "flash"}, "", nil},
	} {
		if got := modelEnv(c.cfg, c.override); !slices.Equal(got, c.want) {
			t.Errorf("%s: modelEnv = %q, want %q", c.name, got, c.want)
		}
	}
}
