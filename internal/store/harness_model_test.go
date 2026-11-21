package store

import (
	"encoding/json"
	"strings"
	"testing"
)

// A write is judged by the model and kind it leaves, and one that leaves
// both as they were stored is not: a group stored before a file dropped
// its kind's flag can still be changed, one key at a time. A config that
// names no kind is claude's, which is what the runner starts, and a kind
// with no file keeps the runner's --model default, so only a file that
// names neither a flag nor a model environment refuses.
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
		{"a model by environment", map[string]any{"kind": "opencode", "model": "opencode/big-pickle"}, nil, ""},
		{"a kind with no file keeps the default", map[string]any{"kind": "gemini", "model": "gemini-3"}, nil, ""},
		{"an empty model is unset", map[string]any{"kind": "opencode", "model": ""}, nil, ""},
		{"a model that is no string is the type's to refuse", map[string]any{"kind": "opencode", "model": 5}, nil, ""},
		// Stored as it is: nothing it changes, so nothing to refuse.
		{"a model that was valid", map[string]any{"kind": "opencode", "model": "gpt", "idle": "5m"}, map[string]any{"kind": "opencode", "model": "gpt"}, ""},
		{"the kind changed to an env kind", map[string]any{"kind": "opencode", "model": "gpt"}, map[string]any{"kind": "claude", "model": "gpt"}, ""},
		{"the model changed on an env kind", map[string]any{"kind": "opencode", "model": "gpt"}, map[string]any{"kind": "opencode", "model": "opus"}, ""},
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

// No kind in the catalogue is flagless without a model environment
// today, so the refusal a file that names neither still gives is driven
// on a kind this test adds to the tables alone. Not parallel: the
// tables are read by the parallel tests of this package, and a write
// beside their reads would be a race.
func TestModelProblemsFlaglessKindWithoutEnv(t *testing.T) {
	HarnessNoModelFlag["flagless"] = true
	defer delete(HarnessNoModelFlag, "flagless")
	cfg := map[string]any{"kind": "flagless", "model": "x"}
	if got := modelProblems("group g", cfg, nil); len(got) != 1 ||
		!strings.Contains(got[0].Error(), "and flagless is not: its file names neither") {
		t.Fatalf("flagless without env: %v, want a refusal", got)
	}
	HarnessModelEnvs["flagless"] = ModelEnv{Var: "FLAGLESS_MODEL", Value: "{model}"}
	defer delete(HarnessModelEnvs, "flagless")
	if got := modelProblems("group g", cfg, nil); len(got) != 0 {
		t.Errorf("flagless with env: %v, want none", got)
	}
}

// The entry a model environment renders is its variable with the model
// in its value, as Effort.Words renders a level into its words.
func TestModelEnvEntry(t *testing.T) {
	t.Parallel()
	e := ModelEnv{Var: "OPENCODE_CONFIG_CONTENT", Value: `{"model":"{model}"}`}
	if got, want := e.Entry("opencode/big-pickle"), `OPENCODE_CONFIG_CONTENT={"model":"opencode/big-pickle"}`; got != want {
		t.Errorf("Entry = %q, want %q", got, want)
	}
	model := "opencode/\"big-pickle\"\\edge"
	entry := e.Entry(model)
	_, value, ok := strings.Cut(entry, "=")
	var config struct {
		Model string `json:"model"`
	}
	if !ok || json.Unmarshal([]byte(value), &config) != nil || config.Model != model {
		t.Errorf("Entry = %q, want JSON carrying model %q", entry, model)
	}
}
