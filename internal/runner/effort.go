package runner

import "yardr.dev/yardr/internal/store"

// withEffort is args with the words the kind's file says give an agent the
// group's effort (config.effort) after them, so that they come last, as the
// model's flag does. A config that names no effort, and a kind that takes
// none, leave args as they were. The store holds a group's effort to its
// kind's levels and its args to not passing the flag already
// (store.ConfigProblems), so the words are added once; a level the store
// would refuse still goes through, since an agent that is told a level it
// does not know says so at its start, where a silently dropped one would
// leave the group at the harness's own effort without a word.
func withEffort(args []string, cfg map[string]any, kind store.Harness) []string {
	level, _ := cfg["effort"].(string)
	if level == "" || kind.Flags.Effort == nil {
		return args
	}
	return append(args, kind.Flags.Effort.Words(level)...)
}
