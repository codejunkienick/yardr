package dispatch_test

import (
	"context"
	"slices"
	"testing"

	"yardr.dev/yardr/internal/store"
)

// A group renamed while it works a bead goes on working it: the next pass
// finds the session under the new name, starts nothing, stops nothing and
// counts the session against the new name's limit, so a second bead waits for
// it; and when the first is handed on, the pass tears its session down, which
// it finds by the new name, and starts the second under it. What the daemon
// reads of a group, it reads from the session's record and the group's.
func TestGroupRenamedWhileItWorksABead(t *testing.T) {
	t.Parallel()
	s, f, d := setup(t)
	ctx := context.Background()
	must(t, s.PutGroup(ctx, store.Group{Name: "builders", Runner: "fake", Limit: 1}, "me"))
	must(t, s.PutGroup(ctx, store.Group{Name: "reviewers", Runner: "fake"}, "me"))
	_, err := s.AddRoute(ctx, store.Route{Stage: "new", Group: "builders"}, "me")
	must(t, err)
	_, err = s.AddRoute(ctx, store.Route{Stage: "review", Group: "reviewers"}, "me")
	must(t, err)

	first, err := s.CreateBead(ctx, store.NewBead{Title: "first"}, "me")
	must(t, err)
	must(t, d.Reconcile(ctx))
	got, err := s.GetBead(ctx, first.ID)
	must(t, err)
	if got.Session == nil {
		t.Fatalf("the first bead was not started: %+v", got)
	}
	session := *got.Session

	_, err = s.RenameGroup(ctx, "builders", "makers", "me")
	must(t, err)
	second, err := s.CreateBead(ctx, store.NewBead{Title: "second"}, "me")
	must(t, err)
	must(t, d.Reconcile(ctx))
	must(t, d.Reconcile(ctx))

	if starts := f.starts(); !slices.Equal(starts, []string{first.ID}) {
		t.Fatalf("starts after the rename = %v, want the first bead's alone: the second waits for the group's one place", starts)
	}
	if len(f.stopped) != 0 {
		t.Fatalf("the rename stopped %v: no session is released by it", f.stopped)
	}
	sess, err := s.GetSession(ctx, session)
	must(t, err)
	if sess.Group != "makers" || sess.State != "running" || sess.EndedAt != nil {
		t.Fatalf("the session after the passes = %+v, want it running, under makers", sess)
	}
	if got, err = s.GetBead(ctx, first.ID); err != nil || got.Group == nil || *got.Group != "makers" || got.Session == nil || *got.Session != session {
		t.Fatalf("the first bead = %+v, %v: makers' still, with the session", got, err)
	}
	if got, err = s.GetBead(ctx, second.ID); err != nil || got.Group != nil {
		t.Fatalf("the second bead = %+v, %v: it waits", got, err)
	}

	_, err = s.Advance(ctx, first.ID, "review", store.Expect{}, "builder")
	must(t, err)
	must(t, d.Reconcile(ctx))
	if !slices.Contains(f.stopped, session) {
		t.Fatalf("the first session was not torn down: stopped %v", f.stopped)
	}
	if got, err = s.GetBead(ctx, second.ID); err != nil || got.Group == nil || *got.Group != "makers" {
		t.Fatalf("the second bead = %+v, %v: it starts under makers once the place is free", got, err)
	}
}
