package store

import (
	"slices"
	"strings"
	"testing"
)

// An effort's words put the level where the file says, and a group's args
// hold the effort wherever the kind could be told of it already: the flag
// alone or with its value, an alias of the flag, and the setting under any
// spelling of the flag that carries settings.
func TestEffortWordsAndHeld(t *testing.T) {
	t.Parallel()
	claude, codex, muse := HarnessEfforts["claude"], HarnessEfforts["codex"], HarnessEfforts["muse"]
	for _, c := range []struct {
		name   string
		effort Effort
		level  string
		want   []string
	}{
		{"a flag and its value", claude, "max", []string{"--effort", "max"}},
		{"a setting", codex, "high", []string{"-c", "model_reasoning_effort=high"}},
		{"a flag with an alias", muse, "xhigh", []string{"--reasoning-effort", "xhigh"}},
	} {
		if got := c.effort.Words(c.level); !slices.Equal(got, c.want) {
			t.Errorf("%s: Words(%q) = %q, want %q", c.name, c.level, got, c.want)
		}
	}
	for _, c := range []struct {
		name   string
		effort Effort
		args   []string
		want   string
	}{
		{"no args", claude, nil, ""},
		{"other flags", claude, []string{"--permission-mode", "auto", "--model", "opus"}, ""},
		{"the flag and a value", claude, []string{"--permission-mode", "auto", "--effort", "low"}, "--effort"},
		{"the flag with its value in the word", claude, []string{"--effort=low"}, "--effort=low"},
		{"a flag that only begins like it", claude, []string{"--effort-hint", "x"}, ""},
		{"the setting after -c", codex, []string{"-c", "model_reasoning_effort=high"}, "model_reasoning_effort=high"},
		{"the setting quoted as TOML", codex, []string{"-c", `model_reasoning_effort="high"`}, `model_reasoning_effort="high"`},
		{"the setting after --config", codex, []string{"--config", "model_reasoning_effort=low"}, "model_reasoning_effort=low"},
		{"the setting in one word", codex, []string{"--config=model_reasoning_effort=low"}, "--config=model_reasoning_effort=low"},
		{"the setting with -c in one word", codex, []string{"-c=model_reasoning_effort=low"}, "-c=model_reasoning_effort=low"},
		{"the setting attached to -c", codex, []string{"-cmodel_reasoning_effort=low"}, "-cmodel_reasoning_effort=low"},
		{"another setting of -c", codex, []string{"-c", `default_permissions="yardr_read_only"`}, ""},
		{"a longer setting name", codex, []string{"-c", "default_model_reasoning_effort=high"}, ""},
		{"the setting in another option", codex, []string{"--label=model_reasoning_effort=high"}, ""},
		{"the setting in a prompt", codex, []string{"--prompt", "model_reasoning_effort=high"}, ""},
		{"the flag of another kind", codex, []string{"--effort", "max"}, ""},
		{"the flag", muse, []string{"--reasoning-effort", "low"}, "--reasoning-effort"},
		{"its alias", muse, []string{"--yolo", "--effort", "low"}, "--effort"},
		{"its alias with the value in the word", muse, []string{"--effort=low"}, "--effort=low"},
	} {
		if got := c.effort.Held(c.args); got != c.want {
			t.Errorf("%s: Held(%q) = %q, want %q", c.name, c.args, got, c.want)
		}
	}
}

// A harness file's effort is held to what Words and Held can read: a flag
// first, the level in one word and once, levels that are plain words, and
// aliases that are flags.
func TestEffortCheck(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		effort *Effort
		want   string
	}{
		{"none", nil, ""},
		{"a flag", &Effort{Args: []string{"--effort", LevelArg}, Levels: []string{"low"}}, ""},
		{"a setting with an alias", &Effort{Args: []string{"-c", "e=" + LevelArg}, Levels: []string{"low", "max"}, Aliases: []string{"--e"}}, ""},
		{"no words", &Effort{Levels: []string{"low"}}, "the first word is the flag"},
		{"no flag first", &Effort{Args: []string{LevelArg, "--effort"}, Levels: []string{"low"}}, "the first word is the flag"},
		{"no level", &Effort{Args: []string{"--effort", "max"}, Levels: []string{"low"}}, "{level} is in one word, once"},
		{"the level twice", &Effort{Args: []string{"--effort", LevelArg, LevelArg}, Levels: []string{"low"}}, "{level} is in one word, once"},
		{"the level twice in a word", &Effort{Args: []string{"-c", LevelArg + "=" + LevelArg}, Levels: []string{"low"}}, "{level} is in one word, once"},
		{"no levels", &Effort{Args: []string{"--effort", LevelArg}}, "the levels the harness takes are needed"},
		{"an empty level", &Effort{Args: []string{"--effort", LevelArg}, Levels: []string{"low", ""}}, `"" is no level a flag takes`},
		{"a level with a space", &Effort{Args: []string{"--effort", LevelArg}, Levels: []string{"very high"}}, `"very high" is no level`},
		{"a level with an equals sign", &Effort{Args: []string{"--effort", LevelArg}, Levels: []string{"a=b"}}, `"a=b" is no level`},
		{"an alias that is no flag", &Effort{Args: []string{"--effort", LevelArg}, Levels: []string{"low"}, Aliases: []string{"effort"}}, `"effort" is no flag`},
	} {
		err := c.effort.check()
		if c.want == "" && err != nil || c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s: check = %v, want %q", c.name, err, c.want)
		}
	}
}

// A write is judged by the effort, kind and args it leaves, and one that
// leaves all three as they were stored is not: a group stored before a file
// changed what its kind takes can still be changed, one key at a time. A
// config that names no kind is claude's, which is what the runner starts.
func TestEffortProblems(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		cfg, old map[string]any
		want     string
	}{
		{"no effort", map[string]any{"kind": "pi"}, nil, ""},
		{"a level", map[string]any{"kind": "codex", "effort": "ultra"}, nil, ""},
		{"no kind is claude's", map[string]any{"effort": "max"}, nil, ""},
		{"no level", map[string]any{"kind": "claude", "effort": ""}, nil, "effort is empty: name a level, or unset it"},
		{"no kind is claude's levels", map[string]any{"effort": "ultra"}, nil, `effort "ultra" is no level claude takes`},
		{"a kind with none", map[string]any{"kind": "kimi", "effort": "low"}, nil, "and kimi is not"},
		{"a kind with no file", map[string]any{"kind": "gemini", "effort": "low"}, nil, "and gemini is not"},
		{"args as a string", map[string]any{"kind": "claude", "effort": "low", "args": "--yolo --effort high"}, nil, `its args already pass "--effort"`},
		{"setting text in another option", map[string]any{"kind": "codex", "effort": "max", "args": []any{"--label=model_reasoning_effort=high"}}, nil, ""},
		{"args as a list", map[string]any{"kind": "claude", "effort": "low", "args": []any{"--yolo"}}, nil, ""},
		// Stored as it is: nothing it changes, so nothing to refuse.
		{"a level that was valid", map[string]any{"kind": "pi", "effort": "low", "idle": "5m"}, map[string]any{"kind": "pi", "effort": "low"}, ""},
		{"the kind changed", map[string]any{"kind": "pi", "effort": "low"}, map[string]any{"kind": "claude", "effort": "low"}, "and pi is not"},
		{"the effort changed", map[string]any{"kind": "pi", "effort": "high"}, map[string]any{"kind": "pi", "effort": "low"}, "and pi is not"},
		{"the args changed", map[string]any{"kind": "claude", "effort": "low", "args": []any{"--effort", "max"}}, map[string]any{"kind": "claude", "effort": "low"}, `its args already pass "--effort"`},
	} {
		got := effortProblems("group g", c.cfg, c.old)
		switch {
		case c.want == "" && len(got) != 0:
			t.Errorf("%s: %v, want none", c.name, got)
		case c.want != "" && (len(got) != 1 || !strings.Contains(got[0].Error(), c.want)):
			t.Errorf("%s: %v, want %q", c.name, got, c.want)
		}
	}
}
