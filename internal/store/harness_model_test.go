package store

import (
	"strings"
	"testing"
)

// A write is judged by the model and kind it leaves, and one that leaves
// both as they were stored is not: a group stored before a file dropped
// its kind's flag can still be changed, one key at a time. A config that
// names no kind is claude's, which is what the runner starts, and a kind
// with no file keeps the runner's --model default, so only a file that
// names no flag refuses.
func TestModelProblems(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		cfg, old map[string]any
		want     string
	}{
		{"no model", map[string]any{"kind": "opencode"}, nil, ""},
		{"a model", map[string]any{"kind": "claude", "model": "opus"}, nil, ""},
		{"no kind is claude's", map[string]any{"model": "opus"}, nil, ""},
		{"no model flag", map[string]any{"kind": "opencode", "model": "opencode/big-pickle"}, nil, "and opencode is not: its file names none. Unset model, or set the group's kind to one that takes it. Opencode takes its model from its own configuration"},
		{"a kind with no file keeps the default", map[string]any{"kind": "gemini", "model": "gemini-3"}, nil, ""},
		{"an empty model is unset", map[string]any{"kind": "opencode", "model": ""}, nil, ""},
		{"a model that is no string is the type's to refuse", map[string]any{"kind": "opencode", "model": 5}, nil, ""},
		// Stored as it is: nothing it changes, so nothing to refuse.
		{"a model that was valid", map[string]any{"kind": "opencode", "model": "gpt", "idle": "5m"}, map[string]any{"kind": "opencode", "model": "gpt"}, ""},
		{"the kind changed", map[string]any{"kind": "opencode", "model": "gpt"}, map[string]any{"kind": "claude", "model": "gpt"}, "and opencode is not"},
		{"the model changed", map[string]any{"kind": "opencode", "model": "gpt"}, map[string]any{"kind": "opencode", "model": "opus"}, "and opencode is not"},
	} {
		got := modelProblems("group g", c.cfg, c.old)
		switch {
		case c.want == "" && len(got) != 0:
			t.Errorf("%s: %v, want none", c.name, got)
		case c.want != "" && (len(got) != 1 || !strings.Contains(got[0].Error(), c.want)):
			t.Errorf("%s: %v, want %q", c.name, got, c.want)
		}
	}
}
