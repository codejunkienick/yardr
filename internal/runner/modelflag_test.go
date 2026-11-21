package runner

import (
	"testing"

	"yardr.dev/yardr/internal/dispatch"
)

// A model is refused when the kind's file names no flag for it. A kind with
// a flag, one with no file, and no model at all pass through.
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
		{"the group's", "group g", "the group's kind", map[string]any{"kind": "opencode", "model": "x"}, "", "",
			"group g: model is for the kinds whose harness file names a model flag, and opencode is not: its file names none. Unset model, or set the group's kind to one that takes it. Opencode takes its model from its own configuration"},
		{"the bead's", "group g", "the group's kind", map[string]any{"kind": "opencode"}, "opus", "yardr-1",
			"group g: bead yardr-1 carries model=opus, but opencode names no model flag: its harness file names none, so the model reaches no flag. Take model off the bead, or set the group's kind to one that takes it. Opencode takes its model from its own configuration"},
		{"a crew member's", "crew member r", "the crew member's kind", map[string]any{"kind": "opencode", "model": "x"}, "", "",
			"crew member r: model is for the kinds whose harness file names a model flag, and opencode is not: its file names none. Unset model, or set the crew member's kind to one that takes it. Opencode takes its model from its own configuration"},
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
