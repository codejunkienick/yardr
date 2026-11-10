package dispatch

import (
	"context"
	"strconv"
	"strings"

	"yardr.dev/yardr/internal/hooks"
	"yardr.dev/yardr/internal/store"
)

// metaOwnPR is the metadata key whose value is the address of a pull request
// the bead itself opened from its own branch (the public line's open-pr
// script writes it, docs/line.md). Such a pull request is the bead's work
// published, not somebody else's change, so the bead is not put on its head.
const metaOwnPR = "own_pr"

// metaFixHead is the head commit a maintainer picked to fix (the contributors
// pack's fix stage), as the bead carries it. The store refuses it to a bead's
// agent (its own metaFixHead); the dispatcher reads it here, where the store
// cannot be asked for a stage's checkout.
const metaFixHead = "fix_head"

// isFullSHA reports whether s is a commit id as the push script reads one
// (its fullsha): forty hex digits, or sixty-four where the repository hashes
// so, in either case. The pin is fetched by the runner, so it holds a commit
// id and nothing else.
func isFullSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !('0' <= s[i] && s[i] <= '9' || 'a' <= s[i] && s[i] <= 'f' || 'A' <= s[i] && s[i] <= 'F') {
			return false
		}
	}
	return true
}

// Pull is a pull request a bead tracks: somebody else's change, which the
// bead's sessions read and do not write.
type Pull struct {
	URL    string // as the bead's link has it
	Number int
	// Head is the commit the session's worktree was put on, the pull
	// request's head when the session started. The runner sets it; it is
	// empty where there is no worktree.
	Head string
	// Pinned is the commit the session's worktree is put on instead of the
	// pull request's live head: the maintainer's picked head, where a pr
	// bead is in fix and the pick is a commit id. Empty everywhere else.
	Pinned string
}

// pullOf is the pull request among a bead's links: the one it tracks, of a
// source that has pull requests. An issue it tracks is none, and neither is
// a link that only relates, nor the pull request the bead's metadata names
// as its own (metaOwnPR): a session on the bead stands on its branch, where
// its commits are the ones the pull request is updated from, instead of
// being told that a commit made on the pull request's head lands nowhere.
// A pr bead in fix is pinned to the head its maintainer picked, never the
// live tip: the push script pushes only what was picked, and the builder
// must read and run that head and no other.
func (d *Dispatcher) pullOf(ctx context.Context, links []store.Link, b *store.Bead) *Pull {
	own, _ := b.Metadata[metaOwnPR].(string)
	for _, l := range links {
		if l.Kind != store.LinkTracks || (own != "" && l.URL == own) {
			continue
		}
		p, ok := hooks.PullerFor(l.Source, d.declOf(ctx, l.Source))
		if !ok {
			continue
		}
		thread := p.PullOf(l.URL)
		if thread == "" {
			continue
		}
		_, num, _ := strings.Cut(thread, "#")
		if n, err := strconv.Atoi(num); err == nil {
			pull := &Pull{URL: l.URL, Number: n}
			if b.Type == "pr" && b.Stage == "fix" {
				if sha, _ := b.Metadata[metaFixHead].(string); isFullSHA(sha) {
					pull.Pinned = sha
				}
			}
			return pull
		}
	}
	return nil
}
