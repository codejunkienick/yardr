package dispatch

import "context"

// PromptQueuer is implemented by runners that can tell a prompt the harness
// took into its queue of inputs it holds back until it can start a turn (a
// herdr pane of Codex lists it under "Queued follow-up inputs") from one it
// did not take. Such a prompt was submitted and taken: typed again it would
// only queue again. PromptShown and PromptTyped of such a runner are false
// for it.
type PromptQueuer interface {
	PromptQueued(ctx context.Context, handle map[string]any, prompt string) (bool, error)
}

// PromptQueuedAt is a handle key next to store.PendingPrompt: when the
// prompt was first seen in the harness's queue. The harness took it, and
// lets it go from there only to start a turn on it: its turn may start, and
// its echo leave the pane, between two passes, and a prompt that left the
// queue, or whose agent is seen working, has arrived (see pendingPrompt).
const PromptQueuedAt = "prompt_queued_at"

// promptQueued reports whether the runner finds prompt in its harness's
// queue, and whether it could look: a runner that is no PromptQueuer has
// no queue to find it in.
func promptQueued(ctx context.Context, r Runner, handle map[string]any, p string) (queued, known bool) {
	q, ok := r.(PromptQueuer)
	if !ok {
		return false, true
	}
	done := askTimer(ctx, askPane)
	queued, err := q.PromptQueued(ctx, handle, p)
	done()
	return queued, err == nil
}
