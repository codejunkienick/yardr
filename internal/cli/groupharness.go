package cli

import (
	"fmt"
	"io"
	"strings"

	"yardr.dev/yardr/internal/agents"
	"yardr.dev/yardr/internal/store"
)

// effortWords are the words a group's effort reaches its agent as, from its
// kind's file: none where the group names no effort, and none where its kind
// takes none (the store refuses that where the effort is set, so only a
// group stored before a file changed is so).
func effortWords(cfg map[string]any) []string {
	level, _ := cfg["effort"].(string)
	h, ok := agents.Lookup(store.EffortKind(cfg))
	if level == "" || !ok || h.Flags.Effort == nil {
		return nil
	}
	return h.Flags.Effort.Words(level)
}

// harnessOf is the harness of yardr group show --json: the kind, the flags
// its file has every agent of it start with and, where the group names an
// effort, the words the kind's file makes of it.
func harnessOf(kind string, h store.Harness, cfg map[string]any) map[string]any {
	out := map[string]any{"kind": kind, "always": h.Flags.Always}
	if words := effortWords(cfg); len(words) > 0 {
		out["effort"] = words
	}
	return out
}

// harnessLines are the lines yardr group show prints beside a group's
// settings of what its kind's file adds to them: the flags it always passes,
// and the words the group's effort becomes. The group's setting says the
// level, and these say what an agent is started with.
func harnessLines(w io.Writer, kind string, cfg map[string]any) {
	if h, ok := agents.Lookup(kind); ok && len(h.Flags.Always) > 0 {
		fmt.Fprintf(w, "  harness %s always: %s\n", kind, strings.Join(h.Flags.Always, " "))
	}
	if words := effortWords(cfg); len(words) > 0 {
		fmt.Fprintf(w, "  harness %s passes effort %v as: %s\n", store.EffortKind(cfg), cfg["effort"], strings.Join(words, " "))
	}
}
