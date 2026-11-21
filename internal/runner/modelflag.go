package runner

import (
	"fmt"

	"yardr.dev/yardr/internal/agents"
	"yardr.dev/yardr/internal/dispatch"
)

// modelFlagRefused is the start backstop for a kind whose harness file
// names no model flag: a start with a model set is refused here, before
// anything is made, instead of appending a --model the program does not
// take and timing out at the pane (issue 41). subject owns the session
// ("group g", "crew member r") and whose names its kind ("the group's
// kind"); cfg is its config, override the bead's metadata.model for a bead
// session and "" for a crew member's, bead the bead's id. A kind with no
// file keeps the runner's --model default, as agentArgs does: only a file
// that names no flag refuses. The remedy is the write-time refusal's
// (modelProblems in internal/store): the bead's own model, a grandfathered
// config and a whole write can still reach past that one.
func modelFlagRefused(subject, whose string, cfg map[string]any, override, bead string) error {
	kind := agents.Kind(cfg)
	h, ok := agents.Lookup(kind)
	if !ok || h.Flags.Model != "" {
		return nil
	}
	if modelFor(cfg, override) == "" {
		return nil
	}
	note := ""
	if kind == "opencode" {
		note = ". Opencode takes its model from its own configuration"
	}
	if override != "" {
		return dispatch.Blocked(fmt.Errorf("%s: bead %s carries model=%s, but %s names no model flag: its harness file names none, so the model reaches no flag. Take model off the bead, or set %s to one that takes it%s",
			subject, bead, override, kind, whose, note))
	}
	return dispatch.Blocked(fmt.Errorf("%s: model is for the kinds whose harness file names a model flag, and %s is not: its file names none. Unset model, or set %s to one that takes it%s",
		subject, kind, whose, note))
}
