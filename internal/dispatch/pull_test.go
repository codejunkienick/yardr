package dispatch_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"yardr.dev/yardr/internal/dispatch"
	"yardr.dev/yardr/internal/store"
)

// pullBead is one bead of TestJobNamesThePullRequestTheBeadTracks: what
// it links to, the pull request its metadata says is its own (own_pr, none
// when empty), and the pull its job names, if any.
type pullBead struct {
	title, typ, kind, url, own string
	pull                       *dispatch.Pull
}

// The job of a bead that tracks a pull request names it, so the runner
// can put the worktree on its head. A bead that tracks an issue, one that
// only relates to a pull request, a train, whose pull request is of its own
// branch, and a bead that opened the pull request itself from its own branch
// (the public line's), whose sessions work on that branch, are started as
// ever.
func TestJobNamesThePullRequestTheBeadTracks(t *testing.T) {
	t.Parallel()
	const pr = "https://github.com/acme/web/pull/7"
	for _, tc := range []struct {
		name   string // the former test name
		source string // the shipped pack, or a declared one
		file   string // the declaration read from here; empty takes text
		text   string
		beads  []pullBead
	}{
		{
			// The pull request's shape is the pack's link to read.
			name:   "TestJobNamesThePullRequestTheBeadTracks",
			source: "github",
			file:   filepath.Join("..", "..", "packs", "github", "github.yaml"),
			beads: []pullBead{
				{"theirs", "", store.LinkTracks, pr, "", &dispatch.Pull{URL: pr, Number: 7}},
				// The pull request it opened from its own branch is its work, not
				// somebody else's; another it tracks, or an own_pr that names a
				// different one, is still read.
				{"its own", "", store.LinkTracks, "https://github.com/acme/web/pull/10", "https://github.com/acme/web/pull/10", nil},
				{"own is another", "", store.LinkTracks, "https://github.com/acme/web/pull/11", pr, &dispatch.Pull{URL: "https://github.com/acme/web/pull/11", Number: 11}},
				{"an issue", "", store.LinkTracks, "https://github.com/acme/web/issues/8", "", nil},
				{"relates", "", store.LinkRelates, pr, "", nil},
				{"no link", "", "", "", "", nil},
				{"train", store.TypeTrain, store.LinkTracks, "https://github.com/acme/web/pull/9", "", nil},
			},
		},
		{
			// A declared source's pull request is named as a github
			// one's is.
			name:   "TestJobNamesTheForgePullRequestTheBeadTracks",
			source: "forge",
			text:   forgeDecl,
			beads: []pullBead{
				{"theirs", "", store.LinkTracks, forgePull, "", &dispatch.Pull{URL: forgePull, Number: 7}},
				{"an issue", "", store.LinkTracks, forgeIssue, "", nil},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f, d := setup(t)
			ctx := context.Background()
			text := tc.text
			if tc.file != "" {
				b, err := os.ReadFile(tc.file)
				must(t, err)
				text = string(b)
			}
			_, err := s.PutSource(ctx, tc.source+".yaml", text, "me")
			must(t, err)
			must(t, s.PutGroup(ctx, store.Group{Name: "reviewers", Runner: "fake", Limit: 9}, "me"))
			_, err = s.AddRoute(ctx, store.Route{Stage: "new", Group: "reviewers"}, "me")
			must(t, err)

			bead := func(title, typ, kind, url, own string) string {
				t.Helper()
				nb := store.NewBead{Title: title, Type: typ}
				if own != "" {
					nb.Metadata = map[string]any{"own_pr": own}
				}
				b, err := s.CreateBead(ctx, nb, "me")
				must(t, err)
				if url != "" {
					_, err = s.AddLink(ctx, b.ID, store.NewLink{Kind: kind, Source: tc.source, URL: url}, "me")
					must(t, err)
				}
				return b.ID
			}
			want := map[string]*dispatch.Pull{}
			for _, b := range tc.beads {
				want[bead(b.title, b.typ, b.kind, b.url, b.own)] = b.pull
			}
			for range len(tc.beads) { // one start per pass for the group (starts.go)
				must(t, d.Reconcile(ctx))
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.jobs) != len(want) {
				t.Fatalf("%d jobs started, want %d", len(f.jobs), len(want))
			}
			for _, j := range f.jobs {
				w := want[j.Bead.ID]
				if (j.Pull == nil) != (w == nil) || (w != nil && *j.Pull != *w) {
					t.Errorf("%s (%s): Pull = %+v, want %+v", j.Bead.ID, j.Bead.Title, j.Pull, w)
				}
			}
		})
	}
}

// The job of a pr bead in fix pins the head its maintainer picked, so the
// runner puts the worktree on fix_head and never the live tip. Anywhere else
// the job names no pin: a bead in review with a pick left from an earlier
// round reads the live head, and a fix with none recorded pins nothing (and
// the runner starts nothing for it). The pin reaches git's arguments, so a
// pick that is no commit id pins nothing either, whatever it holds.
func TestJobPinsThePickedHeadInFix(t *testing.T) {
	t.Parallel()
	s, f, d := setup(t)
	ctx := context.Background()
	b, err := os.ReadFile(filepath.Join("..", "..", "packs", "github", "github.yaml"))
	must(t, err)
	_, err = s.PutSource(ctx, "github.yaml", string(b), "me")
	must(t, err)
	must(t, s.PutGroup(ctx, store.Group{Name: "reviewers", Runner: "fake", Limit: 9}, "me"))
	must(t, s.PutGroup(ctx, store.Group{Name: "builders", Runner: "fake", Limit: 9}, "me"))
	_, err = s.AddRoute(ctx, store.Route{Stage: "review", Group: "reviewers"}, "me")
	must(t, err)
	_, err = s.AddRoute(ctx, store.Route{Stage: "fix", Group: "builders"}, "me")
	must(t, err)

	const picked = "4f2c9e1a7b3d5e6f8091a2b3c4d5e6f708192a3b"
	bead := func(title, stage string, n int, meta map[string]any) (string, *dispatch.Pull) {
		t.Helper()
		b, err := s.CreateBead(ctx, store.NewBead{Title: title, Type: "pr", Stage: stage, Metadata: meta}, "me")
		must(t, err)
		url := fmt.Sprintf("https://github.com/acme/web/pull/%d", n)
		_, err = s.AddLink(ctx, b.ID, store.NewLink{Kind: store.LinkTracks, Source: "github", URL: url}, "me")
		must(t, err)
		return b.ID, &dispatch.Pull{URL: url, Number: n}
	}
	want := map[string]*dispatch.Pull{}
	id, pull := bead("fix with a pick", "fix", 7, map[string]any{"fix_head": picked})
	pull.Pinned = picked
	want[id] = pull
	id, pull = bead("review with a stale pick", "review", 8, map[string]any{"fix_head": picked})
	want[id] = pull
	id, pull = bead("fix with no pick", "fix", 9, nil)
	want[id] = pull
	id, pull = bead("fix with a pick that is no sha", "fix", 10, map[string]any{"fix_head": "--upload-pack=id"})
	want[id] = pull
	id, pull = bead("fix with a short pick", "fix", 11, map[string]any{"fix_head": "4f2c9e1"})
	want[id] = pull
	id, pull = bead("fix with an uppercase pick", "fix", 12, map[string]any{"fix_head": "4F2C9E1A7B3D5E6F8091A2B3C4D5E6F708192A3B"})
	pull.Pinned = "4F2C9E1A7B3D5E6F8091A2B3C4D5E6F708192A3B"
	want[id] = pull
	for range len(want) {
		must(t, d.Reconcile(ctx))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.jobs) != len(want) {
		t.Fatalf("%d jobs started, want %d", len(f.jobs), len(want))
	}
	for _, j := range f.jobs {
		w := want[j.Bead.ID]
		if (j.Pull == nil) != (w == nil) || (w != nil && *j.Pull != *w) {
			t.Errorf("%s (%s): Pull = %+v, want %+v", j.Bead.ID, j.Bead.Title, j.Pull, w)
		}
	}
}
