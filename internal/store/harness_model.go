package store

import (
	"fmt"
)

// HarnessNoModelFlag are the kinds whose harness file names no model flag:
// the file's flags carry no model, so no flag gives their agents a model.
// A group of such a kind refuses model=. The store holds a group's model
// to its kind, so that it imports no internal package; a test in agents
// pins this table to the catalogue, so a file that drops or gains a flag
// fails until this follows. A kind that is not here takes a model, with
// its file's flag, or the runner's --model default where it has no file.
// Read-only.
var HarnessNoModelFlag = map[string]bool{
	"opencode": true,
}

// ModelKind is the kind whose flag a herdr config's model would go to: the
// one it names, else the one the runner starts for a config that names none
// (claude: agents.DefaultKind, which a test there holds to this).
func ModelKind(cfg map[string]any) string {
	if kind, _ := cfg["kind"].(string); kind != "" {
		return kind
	}
	return "claude"
}

// modelProblems holds the model of a herdr group's config cfg to its kind:
// the kind's file names a model flag for it to go to. A write that leaves
// model and kind as old holds them is not judged, so a group stored before
// a harness file dropped its kind's flag can still be changed, one key at
// a time, as configProblems has it.
func modelProblems(subject string, cfg, old map[string]any) []error {
	model, ok := cfg["model"].(string)
	if !ok || model == "" {
		return nil
	}
	if was, had := old["model"].(string); had && was == model && ModelKind(old) == ModelKind(cfg) {
		return nil
	}
	kind := ModelKind(cfg)
	if !HarnessNoModelFlag[kind] {
		return nil
	}
	remedy := "Unset model, or set the group's kind to one that takes it"
	if kind == "opencode" {
		remedy += ". Opencode takes its model from its own configuration"
	}
	return []error{fmt.Errorf("%s: model is for the kinds whose harness file names a model flag, and %s is not: its file names none. %s",
		subject, kind, remedy)}
}
