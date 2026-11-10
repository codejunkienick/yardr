package store

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// LevelArg is where the level goes in the words of an Effort.
const LevelArg = "{level}"

// Effort is how a harness kind takes a reasoning effort, as its file says
// (flags.effort): the words yardr adds to the arguments of an agent of the
// kind whose group names effort=<level>, and the levels it takes. A kind
// whose file names none takes none, and a group of it refuses the key.
type Effort struct {
	// Args are the words added after the group's own, with LevelArg in the
	// one that carries the level: ["--effort", "{level}"] where the harness
	// takes a flag, ["-c", "model_reasoning_effort={level}"] where it takes
	// a setting.
	Args []string `yaml:"args" json:"args"`
	// Levels are the levels the harness takes, weakest first, as its own
	// help or catalogue names them. A harness that checks none itself (Codex
	// hands the word to the model's API) still has this list: it is what
	// yardr accepts, so a misspelt level is told where it is typed instead of
	// at the session's first request.
	Levels []string `yaml:"levels" json:"levels"`
	// Aliases are other flags of the harness that set the same effort, for a
	// group's args to be held to as the flag in Args is: Muse's --effort is
	// its --reasoning-effort.
	Aliases []string `yaml:"aliases" json:"aliases,omitempty"`
}

// Words are the arguments that give an agent the level.
func (e Effort) Words(level string) []string {
	out := make([]string, len(e.Args))
	for i, a := range e.Args {
		out[i] = strings.ReplaceAll(a, LevelArg, level)
	}
	return out
}

// Held is the word of args that already sets the effort, as a flag of the
// kind or as the setting it names, or "" where none does. A flag holds it as
// the word alone (--effort max), or with its value in the word
// (--effort=max); a setting holds it when a Codex config flag carries the
// assignment (-c key=value, --config key=value, or --config=key=value).
func (e Effort) Held(args []string) string {
	var flags []string
	var setting, settingFlag string
	for i, a := range e.Args {
		before, _, found := strings.Cut(a, LevelArg)
		switch {
		case !found:
		case before == "" && i > 0:
			flags = append(flags, e.Args[i-1])
		default:
			setting = before
			if i > 0 {
				settingFlag = e.Args[i-1]
			}
		}
	}
	flags = append(flags, e.Aliases...)
	for i, arg := range args {
		if setting != "" {
			setters := []string{settingFlag}
			if settingFlag == "-c" {
				setters = append(setters, "--config")
			}
			for _, setter := range setters {
				switch {
				case arg == setter && i+1 < len(args) && strings.HasPrefix(args[i+1], setting):
					return args[i+1]
				case strings.HasPrefix(arg, setter+"=") && strings.HasPrefix(strings.TrimPrefix(arg, setter+"="), setting):
					return arg
				case strings.HasPrefix(setter, "-") && !strings.HasPrefix(setter, "--") &&
					strings.HasPrefix(arg, setter) && strings.HasPrefix(strings.TrimPrefix(arg, setter), setting):
					return arg
				}
			}
		}
		for _, f := range flags {
			if arg == f || strings.HasPrefix(arg, f+"=") {
				return arg
			}
		}
	}
	return ""
}

// check holds the effort of a harness file to what Words and Held can read:
// a flag first, the level in exactly one word, levels that are plain words.
func (e *Effort) check() error {
	if e == nil {
		return nil
	}
	switch {
	case len(e.Args) == 0 || !strings.HasPrefix(e.Args[0], "-"):
		return fmt.Errorf("flags.effort.args: the first word is the flag that takes the effort")
	case strings.Count(strings.Join(e.Args, " "), LevelArg) != 1:
		return fmt.Errorf("flags.effort.args: %s is in one word, once: where the level goes", LevelArg)
	case len(e.Levels) == 0:
		return fmt.Errorf("flags.effort.levels: the levels the harness takes are needed")
	}
	for _, l := range e.Levels {
		if l == "" || strings.ContainsAny(l, " ={}") {
			return fmt.Errorf("flags.effort.levels: %q is no level a flag takes", l)
		}
	}
	for _, a := range e.Aliases {
		if !strings.HasPrefix(a, "-") {
			return fmt.Errorf("flags.effort.aliases: %q is no flag", a)
		}
	}
	return nil
}

// HarnessEfforts are the kinds that take an effort, as their files say it
// (flags.effort). The store holds a group's effort to its kind's levels, so
// that it imports no internal package; a test in agents pins this table to
// the catalogue, so a file that changes a flag or a level fails until this
// follows. A kind that is not here takes none. Read-only.
var HarnessEfforts = map[string]Effort{
	"claude": {Args: []string{"--effort", LevelArg}, Levels: []string{"low", "medium", "high", "xhigh", "max"}},
	"codex":  {Args: []string{"-c", "model_reasoning_effort=" + LevelArg}, Levels: []string{"low", "medium", "high", "xhigh", "max", "ultra"}},
	"muse": {Args: []string{"--reasoning-effort", LevelArg}, Aliases: []string{"--effort"},
		Levels: []string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"}},
}

// EffortKind is the kind that takes the effort of a herdr config: the one it
// names, else the one the runner starts for a config that names none (claude:
// agents.DefaultKind, which a test there holds to this).
func EffortKind(cfg map[string]any) string {
	if kind, _ := cfg["kind"].(string); kind != "" {
		return kind
	}
	return "claude"
}

// effortProblems holds the effort of a herdr group's config cfg to its kind:
// the kind takes one, the level is one of its levels, and the group's args do
// not already pass it (two words for one setting would leave which wins to the
// harness). A write that leaves effort, kind and args as old holds them is not
// judged, so a group stored before a harness file changed can still be
// changed, one key at a time, as configProblems has it.
func effortProblems(subject string, cfg, old map[string]any) []error {
	level, ok := cfg["effort"].(string)
	if !ok {
		return nil
	}
	if was, had := old["effort"].(string); had && was == level && EffortKind(old) == EffortKind(cfg) &&
		slices.Equal(argWords(old["args"]), argWords(cfg["args"])) {
		return nil
	}
	kind := EffortKind(cfg)
	e, takes := HarnessEfforts[kind]
	switch {
	case level == "":
		// No level is --unset's, which is what returns an agent to its
		// harness's own; an empty one would stand in the config and say nothing.
		return []error{fmt.Errorf("%s: effort is empty: name a level, or unset it to return to the harness's own", subject)}
	case !takes:
		return []error{fmt.Errorf("%s: effort is for the kinds that take one (%s), and %s is not: its harness file names no flag for it. Unset effort, or set the group's kind to one that takes it",
			subject, strings.Join(slices.Sorted(maps.Keys(HarnessEfforts)), ", "), kind)}
	case !slices.Contains(e.Levels, level):
		return []error{fmt.Errorf("%s: effort %q is no level %s takes (it takes: %s)", subject, level, kind, strings.Join(e.Levels, ", "))}
	}
	if held := e.Held(argWords(cfg["args"])); held != "" {
		return []error{fmt.Errorf("%s: its args already pass %q, which effort sets: remove it from args first, so that the flag is not passed twice", subject, held)}
	}
	return nil
}
