package runner

import (
	"fmt"

	"yardr.dev/yardr/internal/agents"
	"yardr.dev/yardr/internal/dispatch"
)

// modelFlagRefused is the start backstop for a kind whose harness file
// names neither a model flag nor a model environment: a start with a
// model set is refused here, before anything is made, instead of
// appending a --model the program does not take and timing out at the
// pane (issue 41). subject owns the session ("group g", "crew member r")
// and whose names its kind ("the group's kind"); cfg is its config,
// override the bead's metadata.model for a bead session and "" for a
// crew member's, bead the bead's id. A kind with no file keeps the
// runner's --model default, as agentArgs does: only a file that names
// neither refuses. The remedy is the write-time refusal's (modelProblems
// in internal/store): the bead's own model, a grandfathered config and a
// whole write can still reach past that one.
func modelFlagRefused(subject, whose string, cfg map[string]any, override, bead string) error {
	kind := agents.Kind(cfg)
	h, ok := agents.Lookup(kind)
	if !ok || h.Flags.Model != "" || h.Flags.ModelEnv != nil {
		return nil
	}
	if modelFor(cfg, override) == "" {
		return nil
	}
	if override != "" {
		return dispatch.Blocked(fmt.Errorf("%s: bead %s carries model=%s, but %s names no model flag or model environment: its harness file names neither, so the model reaches nowhere. Take model off the bead, or set %s to one that takes it",
			subject, bead, override, kind, whose))
	}
	return dispatch.Blocked(fmt.Errorf("%s: model is for the kinds whose harness file names a model flag or a model environment, and %s is not: its file names neither. Unset model, or set %s to one that takes it",
		subject, kind, whose))
}

// modelEnv is the environment entry that gives an agent of cfg's kind
// its model, where the kind's file names a model environment for it:
// the variable its file names, rendered with the model the bead's
// override or the config names. nil where the kind takes a model by
// flag (or not at all), and where no model is set, as no flag is passed
// then either.
func modelEnv(cfg map[string]any, override string) []string {
	h, ok := agents.Lookup(agents.Kind(cfg))
	if !ok || h.Flags.ModelEnv == nil {
		return nil
	}
	model := modelFor(cfg, override)
	if model == "" {
		return nil
	}
	return []string{h.Flags.ModelEnv.Entry(model)}
}
