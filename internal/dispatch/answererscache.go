package dispatch

import (
	"context"
	"sync"

	"yardr.dev/yardr/internal/limits"
)

// An AnswerersCache is one pass's resolution of the yard's limits
// answerers: route choosing judges every condition of every waiting bead,
// and each resolution lists every source and parses every declaration, so
// the pass resolves them once and every read of the pass goes by that.
// A crew worker's keep shares the pass's context, and its clone reads the
// pass's limits cache through its host already, so the resolution is the
// pass's there too.
//
// The cache lives on the pass's context (WithAnswerersCache, which
// Reconcile calls beside WithPaneCache): a read through passAnswerers
// resolves once per pass, and a context without one reads the sources
// directly, as before. It is forgotten with the pass on purpose, not kept
// until the sources change: a source written mid-pass resolves anew on the
// next, with nothing to invalidate and no stale routing.
type AnswerersCache struct {
	mu sync.Mutex
	as []limits.Answerer
	// err is why the sources could not be resolved: a pass whose store
	// cannot be listed resolves nothing until the next, as a read whose
	// store cannot be read is unresolvable.
	err  error
	done bool
}

type answerersCacheKey struct{}

// WithAnswerersCache returns ctx with an empty AnswerersCache for one pass.
func WithAnswerersCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, answerersCacheKey{}, &AnswerersCache{})
}

// AnswerersCacheFrom is the AnswerersCache ctx carries for this pass, nil
// when it carries none.
func AnswerersCacheFrom(ctx context.Context) *AnswerersCache {
	c, _ := ctx.Value(answerersCacheKey{}).(*AnswerersCache)
	return c
}

// get is the yard's limits answerers, resolved once and kept for the pass.
func (c *AnswerersCache) get(ctx context.Context, st limits.Store, home string) ([]limits.Answerer, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.done {
		c.as, c.err = limits.Answerers(ctx, st, home)
		c.done = true
	}
	return c.as, c.err
}
