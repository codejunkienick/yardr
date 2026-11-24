package dispatch_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"yardr.dev/yardr/internal/dispatch"
	"yardr.dev/yardr/internal/store"
)

// A bead that has been in a stage of a manual group for longer than the
// group's wait raises waited once, by the daemon's clock, whether the group
// holds it or is at its limit, and the yardmaster has it in its inbox. A
// manual group with no wait raises nothing, as before (yardr-bygh).
func TestBeadThatWaitsLongInAManualGroupRaisesWaitedOnce(t *testing.T) {
	t.Parallel()
	s, _, d := crewSetup(t, store.CrewMember{On: []string{store.KindAttention}})
	d.Runners["manual"] = &fakeRunner{dead: map[string]bool{}}
	ctx := context.Background()
	must(t, s.PutGroup(ctx, store.Group{Name: "deciders", Runner: "manual", Limit: 1, Config: map[string]any{"wait": "1h"}}, "me"))
	must(t, s.PutGroup(ctx, store.Group{Name: "shapers", Runner: "manual"}, "me"))
	for stage, group := range map[string]string{"decide": "deciders", "backlog": "shapers"} {
		_, err := s.AddRoute(ctx, store.Route{Stage: stage, Group: group}, "me")
		must(t, err)
	}
	bead := func(title, stage string) *store.Bead {
		t.Helper()
		b, err := s.CreateBead(ctx, store.NewBead{Title: title, Stage: stage}, "me")
		must(t, err)
		return b
	}
	held, queued, quiet := bead("held by the group", "decide"), bead("past the group's limit", "decide"), bead("no wait", "backlog")
	start := time.Now()
	now := start
	dispatch.SetClock(d, func() time.Time { return now })
	waited := func(b *store.Bead) []store.Event { return eventKinds(t, s, b.ID, dispatch.Waited) }
	counts := func(want int, when string) {
		t.Helper()
		if h, q, n := len(waited(held)), len(waited(queued)), len(waited(quiet)); h != want || q != want || n != 0 {
			t.Fatalf("%s: waited raised %d times on the held bead, %d on the queued, want %d each; %d on the one whose group sets no wait, want 0",
				when, h, q, want, n)
		}
	}

	must(t, d.Reconcile(ctx))
	if b, err := s.GetBead(ctx, queued.ID); err != nil || b.Group != nil {
		t.Fatalf("the second bead is not left unclaimed by a group at its limit: %+v, %v", b, err)
	}
	now = start.Add(59 * time.Minute)
	must(t, d.Reconcile(ctx))
	counts(0, "before the hour")

	now = start.Add(61 * time.Minute)
	must(t, d.Reconcile(ctx))
	counts(1, "after the hour")
	e := waited(held)[0]
	since, err := time.Parse(time.RFC3339, e.Data["since"].(string))
	if err != nil || since.Sub(held.CreatedAt).Abs() > time.Minute || e.Data["group"] != "deciders" || e.Data["stage"] != "decide" {
		t.Fatalf("waited = %+v, %v; want group, stage and since when the bead was made", e.Data, err)
	}
	if got, err := time.ParseDuration(e.Data["waited"].(string)); err != nil || got < time.Hour || got > 62*time.Minute {
		t.Fatalf("waited says %v (%v), want a little over the hour", e.Data["waited"], err)
	}
	page, err := s.Inbox(ctx, "yardmaster", 0)
	must(t, err)
	inbox := 0
	for _, e := range page.Events {
		if e.Kind == dispatch.Waited {
			inbox++
		}
	}
	if inbox != 2 {
		t.Fatalf("the yardmaster, who hears the class attention, has %d waited in its inbox, want 2: %+v", inbox, page.Events)
	}

	// Not again: on a later pass, a day on, or by a daemon that has just
	// started and remembers nothing.
	must(t, d.Reconcile(ctx))
	now = start.Add(25 * time.Hour)
	must(t, d.Reconcile(ctx))
	counts(1, "a day later")
	fresh := &dispatch.Dispatcher{Store: s, Runners: d.Runners, PromptSettle: -1, PromptWait: -1}
	dispatch.SetClock(fresh, func() time.Time { return now })
	must(t, fresh.Reconcile(ctx))
	counts(1, "after the daemon was replaced")

	// A bead that leaves the stage and comes back begins a new stay: it is
	// told of again, and only once that stay is longer than the wait.
	for _, stage := range []string{"backlog", "decide"} {
		_, err := s.Advance(ctx, held.ID, stage, store.Expect{}, "me")
		must(t, err)
	}
	back := time.Now()
	now = back.Add(59 * time.Minute)
	must(t, d.Reconcile(ctx))
	if n := len(waited(held)); n != 1 {
		t.Fatalf("waited raised %d times on a bead that came back to the stage 59m ago, want the 1 of its first stay", n)
	}
	now = back.Add(61 * time.Minute)
	must(t, d.Reconcile(ctx))
	must(t, d.Reconcile(ctx))
	again := waited(held)
	if len(again) != 2 {
		t.Fatalf("waited raised %d times on a bead in its second stay past the wait, want 2", len(again))
	}
	if second, err := time.Parse(time.RFC3339, again[1].Data["since"].(string)); err != nil || second.Before(back.Add(-time.Minute)) {
		t.Fatalf("the second waited counts from %v (%v), want from when the bead came back, %v", again[1].Data["since"], err, back)
	}
	if n := len(waited(quiet)); n != 0 {
		t.Fatalf("waited raised %d times in a manual group with no wait", n)
	}
}

// In a manual group that asks its crew member first, the waits scan asks
// the group's people when the crew member passes the stage's ask up, and
// when it stays silent past the group's wait; before the wait, silence
// asks nobody new (yardr-115z).
func TestCrewFirstAskFallsThroughInTheWaitsScan(t *testing.T) {
	t.Parallel()
	s, _, d := crewSetup(t, store.CrewMember{On: []string{store.KindAttention}})
	d.Runners["manual"] = &fakeRunner{dead: map[string]bool{}}
	ctx := context.Background()
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q", "-b", "main")
	must(t, os.MkdirAll(filepath.Join(repo, ".yardr/flows/default"), 0o755))
	must(t, os.WriteFile(filepath.Join(repo, ".yardr/flows/default.flow"),
		[]byte("new -> review\nreview -> review | approved on done | new on changes\napproved -> merged\nmerged terminal\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(repo, ".yardr/flows/default/review.md"),
		[]byte("Review the diff.\n\n## Ask\nkind: choice\nquestion: Where should this bead go next?\ncontext:\n- The branch is ready either way.\ndone: The bead moves to approved.\nchanges: The bead goes back for changes.\n"), 0o644))
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "flow")
	must(t, s.PutDepot(ctx, store.Depot{Name: "web", Path: repo, Base: "main"}, "me"))
	must(t, s.SetSetting(ctx, store.OperatorSetting, "jo", "jo"))
	if _, err := s.AddUser(ctx, "ada", "jo"); err != nil {
		t.Fatal(err)
	}
	must(t, s.PutGroup(ctx, store.Group{Name: "deciders", Runner: "manual", Limit: 50,
		Members: []string{"yardmaster", "ada"},
		Config:  map[string]any{"ask_crew_first": true, "wait": "1h"}}, "me"))
	_, err := s.AddRoute(ctx, store.Route{Stage: "review", Group: "deciders"}, "me")
	must(t, err)
	enter := func() *store.Bead {
		t.Helper()
		b, err := s.CreateBead(ctx, store.NewBead{Title: "x", Depot: "web", Stage: "new"}, "me")
		must(t, err)
		b, err = s.Advance(ctx, b.ID, "review", store.Expect{}, "me")
		must(t, err)
		return b
	}
	openTo := func(id, name string) int {
		t.Helper()
		notes, err := s.Notes(ctx, id)
		must(t, err)
		n := 0
		for _, r := range store.ResolveAsks(notes, time.Now()) {
			if r.AskErr == nil && r.Ask.To == name && r.State == store.AskOpen {
				n++
			}
		}
		return n
	}
	start := time.Now()
	now := start
	dispatch.SetClock(d, func() time.Time { return now })

	// Silence before the wait asks nobody new.
	silent := enter()
	must(t, d.Reconcile(ctx))
	now = start.Add(30 * time.Minute)
	must(t, d.Reconcile(ctx))
	if n := openTo(silent.ID, "ada"); n != 0 {
		t.Fatalf("ada has %d open asks before the wait, want 0", n)
	}
	if n := openTo(silent.ID, "yardmaster"); n != 1 {
		t.Fatalf("the crew member has %d open asks, want 1", n)
	}
	// Silence past the wait falls through to the people, once.
	now = start.Add(61 * time.Minute)
	must(t, d.Reconcile(ctx))
	must(t, d.Reconcile(ctx))
	if n := openTo(silent.ID, "ada"); n != 1 {
		t.Fatalf("ada has %d open asks after the wait, want 1", n)
	}
	if n := openTo(silent.ID, "yardmaster"); n != 0 {
		t.Fatalf("the crew member has %d open asks after the fall-through, want 0", n)
	}

	// A pass-up asks the people on the next pass, before any wait.
	passed := enter()
	notes, err := s.Notes(ctx, passed.ID)
	must(t, err)
	var crewAsk uint64
	for _, r := range store.ResolveAsks(notes, time.Now()) {
		if r.AskErr == nil && r.Ask.To == "yardmaster" && r.State == store.AskOpen {
			crewAsk = r.Note.Seq
		}
	}
	if crewAsk == 0 {
		t.Fatalf("no open crew ask on the passed-up bead: %+v", notes)
	}
	must(t, s.TakeBackAsk(ctx, passed.ID, crewAsk, "a person should decide this", "yardmaster"))
	now = start.Add(5 * time.Minute)
	must(t, d.Reconcile(ctx))
	if n := openTo(passed.ID, "ada"); n != 1 {
		t.Fatalf("ada has %d open asks after the pass-up, want 1", n)
	}
}
