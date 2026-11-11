package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// A watch is a preview that runs nothing: the supervisor serves the files
// of a directory of a bead's worktree itself, read-only, and reloads the
// page when one changes, so a person watches a site take shape while its
// builder works (internal/preview, watch.go). There is no command, port or
// approval to give: no code of the builder's runs. It is a row of the
// previews bucket of kind PreviewKindWatch, so the endpoint, the edge's
// host for it, stopping it and the supervisor's reaper are the previews'.

// WatchApp is the name a bead's watch has among the previews: the bead's
// id folded to the shape CheckPreviewApp takes (a depot named yardr.dev
// makes beads named yardr.dev-02ye), and, if that is too long, its end,
// where the part that tells one bead from another is.
func WatchApp(bead string) string {
	var b strings.Builder
	dash := true // no leading dash
	for _, r := range strings.ToLower(bead) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	app := strings.TrimSuffix(b.String(), "-")
	if len(app) > 30 {
		app = strings.TrimLeft(app[len(app)-30:], "-")
	}
	return app
}

// WatchPreview starts a watch of bead's files, or starts its watch again:
// the directory dir of the bead's worktree (empty: the worktree itself)
// is served through the preview endpoint, until the bead leaves the stage
// it is in now, it goes PreviewIdle without a request, or the watch is
// PreviewAgeLimit old. dir is read against the worktree and must resolve
// inside it, to a directory, with no part of its path starting with a dot.
// The worktree is the one the bead's newest session recorded. through, when
// not empty, is the stage the watch lives past its start into: the watch
// then ends when the bead is in neither, and a stage the bead's flow does
// not have is refused.
//
// An agent working the bead, the bead's originator and an operator may
// watch it (ActWatchPreview). A watch of the same bead that has ended is
// started in place, so the address it had stands; a preview of another
// kind, or another bead's watch, under the name is refused.
func (s *Store) WatchPreview(ctx context.Context, bead, dir, through, actor string) (*Preview, error) {
	var id string
	var depot *string
	var typ, pin string
	// Asked before anything of the machine is read, so a caller the gate
	// refuses learns nothing of the worktree.
	err := s.read(func(tx *bolt.Tx) error {
		row, err := loadBead(tx, bead)
		if err != nil {
			return err
		}
		id = row.ID
		depot, typ, pin = row.Depot, row.Type, PinnedFlow(&row.Bead)
		resolved, err := paneAuthor(tx, callerOf(ctx), actor)
		if err != nil {
			return err
		}
		return s.may(ctx, tx, resolved, ActWatchPreview, watchObject(tx, row))
	})
	if err != nil {
		return nil, err
	}
	// The repository flows the through check resolves the bead's flow
	// through, read before the write: git must not run inside one.
	repos := map[string]*RepoFlows{}
	if through != "" && depot != nil {
		// Best effort, as the blockers' are (withUntilRepos): a checkout
		// git cannot read leaves the through stage unverified, and the
		// watch then ends on the first move past a stage its flow lacks,
		// as a through stage the bead never enters does.
		if rf, err := s.repoFor(ctx, depot, typ, pin); err == nil && rf != nil {
			repos[*depot] = rf
		}
	}
	app := WatchApp(id)
	if err := CheckPreviewApp(app); err != nil {
		return nil, fmt.Errorf("bead %s has no name a watch can take: %w", id, err)
	}
	served, err := s.watchDir(ctx, id, dir)
	if err != nil {
		return nil, err
	}
	p := new(Preview)
	err = s.write(func(w *wtx) error {
		resolved, err := paneAuthor(w.Tx, callerOf(ctx), actor)
		if err != nil {
			return err
		}
		actor = resolved
		row, err := w.loadBead(id)
		if err != nil {
			return err
		}
		if err := w.may(ctx, actor, ActWatchPreview, watchObject(w.Tx, row)); err != nil {
			return err
		}
		if row.ClosedAt != nil {
			return fmt.Errorf("bead %s is closed: nothing is being built for a watch to show", row.ID)
		}
		if through != "" {
			// Membership only, as a creation's is (checkCreateStage): where
			// no flow governs the bead, or none could be read, any stage
			// may be entered, as a move may. Later-ness is not enforced.
			look := &untilLooks{repos: repos, memo: flowMemo{}}
			if f := look.flowOf(w.Tx, row); f != nil && !slices.Contains(f.Stages, through) {
				return fmt.Errorf("bead %s: stage %s is not in flow %s; stages: %s", row.ID, through, f.Name, strings.Join(f.Stages, ", "))
			}
		}
		var old Preview
		if ok, err := get(w.Tx, bPreviews, app, &old); err != nil {
			return err
		} else if ok && (old.Kind != PreviewKindWatch || old.Bead != row.ID) {
			return fmt.Errorf("preview %s: a preview of that name is already in the yard (yardr preview list); forget it first if it should go", app)
		}
		at := now()
		// The host is the edge's for the watch's name and outlives the watch:
		// the one it had stands when the watch is started again.
		*p = Preview{App: app, Kind: PreviewKindWatch, Bead: row.ID, Stage: row.Stage, Through: through, Dir: served, Host: old.Host,
			State: PreviewRunning, Proposer: actor, ProposedAt: at, StartedAt: &at}
		if row.Depot != nil {
			p.Depot = *row.Depot
		}
		if err := put(w.Tx, bPreviews, app, p); err != nil {
			return err
		}
		data := map[string]any{"app": app, "bead": row.ID, "stage": row.Stage, "dir": served}
		if through != "" {
			data["through"] = through
		}
		return w.event("", "preview_watched", actor, data)
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

// watchObject is the bead as the gate of a watch reads it: whose it is, and
// the row the calling session is compared with.
func watchObject(tx *bolt.Tx, row *beadRow) Object {
	return Object{Kind: KindBead, Name: row.ID, Whose: originatorOf(tx, row.UUID), row: row}
}

// watchDir is the directory a watch of bead serves: dir read against the
// bead's worktree, with every link resolved. A directory that is not inside
// the worktree, one reached through a part starting with a dot (.git, .env),
// and one that is not a directory are refused.
func (s *Store) watchDir(ctx context.Context, bead, dir string) (string, error) {
	sessions, err := s.ListSessions(ctx, SessionFilter{Bead: bead, All: true})
	if err != nil {
		return "", err
	}
	root, at := "", time.Time{}
	for _, sess := range sessions {
		d, _ := sess.Handle["worktree"].(string)
		if d == "" {
			// A dir depot has no worktree: its bead is worked in the
			// depot's directory itself.
			d, _ = sess.Handle["dir"].(string)
		}
		if d == "" || sess.Cleaned || !sess.StartedAt.After(at) {
			continue
		}
		if real, err := filepath.EvalSymlinks(d); err == nil {
			if st, err := os.Stat(real); err == nil && st.IsDir() {
				root, at = real, sess.StartedAt
			}
		}
	}
	if root == "" {
		return "", fmt.Errorf("bead %s has no worktree to watch: none of its sessions recorded one that is still there", bead)
	}
	target, asked := root, or(dir, "the worktree")
	if dir != "" {
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(root, dir)
		}
		if target, err = filepath.EvalSymlinks(dir); err != nil {
			return "", fmt.Errorf("bead %s: %s is not a directory of its worktree", bead, asked)
		}
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("bead %s: %s is not inside its worktree; a watch serves a directory of it", bead, asked)
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part != "." && strings.HasPrefix(part, ".") {
			return "", fmt.Errorf("bead %s: %s is reached through %s, which a watch never serves", bead, asked, part)
		}
	}
	if st, err := os.Stat(target); err != nil || !st.IsDir() {
		return "", fmt.Errorf("bead %s: %s is not a directory", bead, asked)
	}
	return target, nil
}
