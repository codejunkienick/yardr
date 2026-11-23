package dispatch

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"yardr.dev/yardr/internal/source"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
)

func TestUnacceptedPullRequestHeadUsesLegacyVisited(t *testing.T) {
	const accepted = "9f2c9e1a7b3d5e6f8091a2b3c4d5e6f708192a3b"
	const pushed = "c0ffee5a1e0f4b2d8a97163e5d4c3b2a1f0e9d8c"
	h := &store.Hook{Says: []store.Say{{
		Where: map[string]string{
			"event": "pull_request", "action": "synchronize", "label": "accepted",
			"stage": "review", "type": "pr",
		},
		Outcome: outcomeUnaccepted,
	}}}
	ev := ruleEvent{
		typ: "pull_request", action: "synchronize", sha: pushed, tellsHead: true,
		data: map[string]any{
			"type": "pull_request", "action": "synchronize",
			"fields": map[string]any{"head_sha": []string{pushed}},
		},
	}
	bead := &store.Bead{
		Type: "pr", Stage: "review", Visited: []string{"triage", "review"},
		Metadata: map[string]any{metaAcceptedHead: accepted},
	}
	line := unacceptedPullRequestHead(h, bead, ev)
	if line == nil || line.Outcome != outcomeUnaccepted {
		t.Fatalf("line = %+v, want the unaccepted outcome for a legacy bead that visited triage", line)
	}

	// Any tell of a new head is judged as synchronize is, and a tell of
	// the recorded head is no push at all.
	edited := ruleEvent{
		typ: "pull_request", action: "edited", sha: pushed, tellsHead: true,
		data: map[string]any{
			"type": "pull_request", "action": "edited",
			"fields": map[string]any{"head_sha": []string{pushed}},
		},
	}
	seen := &store.Bead{
		Type: "pr", Stage: "review", Visited: []string{"triage", "review"},
		Metadata: map[string]any{metaAcceptedHead: accepted, metaPRHead: accepted},
	}
	if line := unacceptedPullRequestHead(h, seen, edited); line == nil || line.Outcome != outcomeUnaccepted {
		t.Fatalf("edited new head: line = %+v, want the unaccepted outcome", line)
	}
	seen.Metadata[metaPRHead] = pushed
	if line := unacceptedPullRequestHead(h, seen, edited); line != nil {
		t.Fatalf("edited recorded head: line = %+v, want none", line)
	}
	// An explicit synchronize still triggers when pr_head was already
	// recorded (e.g. while held), as long as accepted_head differs.
	if line := unacceptedPullRequestHead(h, seen, ev); line == nil || line.Outcome != outcomeUnaccepted {
		t.Fatalf("synchronize at already-recorded unaccepted head: line = %+v, want the unaccepted outcome", line)
	}
	// pull_request_review is github's other head_from type and matches the
	// same unaccepted line when it tells a new head.
	review := ruleEvent{
		typ: "pull_request_review", action: "submitted", sha: pushed, tellsHead: true,
		data: map[string]any{
			"type": "pull_request_review", "action": "submitted",
			"fields": map[string]any{"event": []string{"pull_request_review"}, "head_sha": []string{pushed}},
		},
	}
	seen.Metadata[metaPRHead] = accepted
	if line := unacceptedPullRequestHead(h, seen, review); line == nil || line.Outcome != outcomeUnaccepted {
		t.Fatalf("pull_request_review new head: line = %+v, want the unaccepted outcome", line)
	}
}

// packDecl is the pack's declaration as it ships: its link shape is what
// reads an issue's URL, with or without the title slug a delivery carries.
func packDecl(t *testing.T, pack string) *source.Declaration {
	t.Helper()
	path := filepath.Join("..", "..", "packs", pack, pack+".yaml")
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := source.Parse(path, text)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

const (
	linearFixed  = "https://linear.app/acme/issue/ENG-34"
	linearSlug   = linearFixed + "/draft-the-quarterly-report"
	linearRename = linearFixed + "/the-quarterly-report"
)

// A delivery's URL is brought to its source's fixed form to find the bead
// that tracks it, with the source's own declaration; what the source reads
// no fixed form from is left as the delivery gave it.
func TestFixedThread(t *testing.T) {
	linear, github := packDecl(t, "linear"), packDecl(t, "github")
	for _, c := range []struct {
		name   string
		source string
		decl   *source.Declaration
		url    string
		want   string
	}{
		{"a slugged Linear issue loses its slug", "linear", linear, linearSlug, linearFixed},
		{"the fixed form is its own", "linear", linear, linearFixed, linearFixed},
		{"a renamed Linear issue reads the same", "linear", linear, linearRename, linearFixed},
		{"a GitHub pull request is its fixed form", "github", github, "https://github.com/acme/yardr/pull/17", "https://github.com/acme/yardr/pull/17"},
		{"a GitHub pull request's tab is no part of it", "github", github, "https://github.com/acme/yardr/pull/17/files", "https://github.com/acme/yardr/pull/17"},
		{"a URL no issue shape reads stays as given", "linear", linear, "https://linear.app/acme/team/ENG", "https://linear.app/acme/team/ENG"},
		{"a URL of another host stays as given", "linear", linear, "https://example.com/acme/issue/ENG-34", "https://example.com/acme/issue/ENG-34"},
		{"a source the yard declares none of keeps it", "ghost", nil, linearSlug, linearSlug},
	} {
		if got := fixedThread(ruleEvent{source: c.source, threadURL: c.url}, c.decl); got != c.want {
			t.Errorf("%s: fixed %q, want %q", c.name, got, c.want)
		}
	}
	// What is looked up for a thread: the fixed form, the URL as given where
	// that differs, and the other shape's URL of a source with pull requests,
	// none twice.
	for _, c := range []struct {
		name string
		ev   ruleEvent
		decl *source.Declaration
		want []string
	}{
		{"slugged", ruleEvent{source: "linear", threadURL: linearSlug}, linear, []string{linearFixed, linearSlug}},
		{"already fixed", ruleEvent{source: "linear", threadURL: linearFixed}, linear, []string{linearFixed}},
		{"a pull request has its issue form to try", ruleEvent{source: "github", threadURL: "https://github.com/acme/yardr/pull/17"}, github,
			[]string{"https://github.com/acme/yardr/pull/17", "https://github.com/acme/yardr/issues/17"}},
		{"no thread", ruleEvent{source: "linear"}, linear, nil},
	} {
		if got := threadURLs(c.ev, c.decl); !slices.Equal(got, c.want) {
			t.Errorf("%s: urls %q, want %q", c.name, got, c.want)
		}
	}
}

// A yard with the depot web, for beads that track threads by link.
func threadYard(t *testing.T) (*Dispatcher, store.API) {
	t.Helper()
	s := storetest.New(t)
	if err := s.PutDepot(context.Background(), store.Depot{Name: "web", Path: t.TempDir(), Kind: store.DepotDir}, "me"); err != nil {
		t.Fatal(err)
	}
	return &Dispatcher{Store: s, Log: slog.Default()}, s
}

// tracker makes a bead of the depot web that tracks the thread at url in source.
func tracker(t *testing.T, s store.API, title, source, url string) *store.Bead {
	t.Helper()
	b, err := s.CreateBead(context.Background(), store.NewBead{Title: title, Depot: "web",
		Links: []store.NewLink{{Kind: store.LinkTracks, Source: source, URL: url}}}, "me")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// One issue is one link whoever names it: the bead linked by hand (the fixed
// form, as yardr link stores it) is the one a delivery of the issue is about,
// whatever title slug its URL carries, and another issue's delivery is about
// no bead. What matched before still does: a bead made before links were
// stored fixed has the URL as the delivery gave it, and a bead of a source
// with pull requests is found by the other shape's URL.
func TestAboutMatchesByTheIssuesFixedURL(t *testing.T) {
	d, s := threadYard(t)
	ctx := context.Background()
	linear, github := packDecl(t, "linear"), packDecl(t, "github")
	hand := tracker(t, s, "linked by hand", "linear", linearFixed)
	legacy := tracker(t, s, "made with its slug", "linear", "https://linear.app/acme/issue/ENG-35/the-old-title")
	pull := tracker(t, s, "an issue linked by its issue form", "github", "https://github.com/acme/yardr/issues/17")
	for _, c := range []struct {
		name   string
		source string
		decl   *source.Declaration
		url    string
		want   *store.Bead // nil: about no bead
	}{
		{"a slugged delivery finds the bead linked by hand", "linear", linear, linearSlug, hand},
		{"a renamed issue finds it too", "linear", linear, linearRename, hand},
		{"a delivery with no slug finds it", "linear", linear, linearFixed, hand},
		{"another issue is about no bead", "linear", linear, "https://linear.app/acme/issue/ENG-36/other", nil},
		{"a bead made with its slug is still found by that URL", "linear", linear, "https://linear.app/acme/issue/ENG-35/the-old-title", legacy},
		{"a pull request finds the bead linked by its issue", "github", github, "https://github.com/acme/yardr/pull/17", pull},
	} {
		got, _, tracked := d.about(ctx, ruleEvent{source: c.source, threadURL: c.url}, c.decl)
		switch {
		case c.want == nil && got != nil:
			t.Errorf("%s: about %s, want none", c.name, got.ID)
		case c.want != nil && (got == nil || got.ID != c.want.ID || !tracked):
			t.Errorf("%s: about %v (tracked %v), want %s", c.name, got, tracked, c.want.ID)
		}
	}
}

// A bead an event makes tracks its thread by the issue's fixed URL, which the
// linked event shows, so a rename of the issue cuts it off from nothing: the
// next delivery, under the new title, is about that bead and makes no
// second.
func TestMadeBeadTracksTheFixedURL(t *testing.T) {
	d, s := threadYard(t)
	ctx := context.Background()
	linear := packDecl(t, "linear")
	line, err := store.ParseMake("event=assigned -> task:new@web")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutHook(ctx, store.Hook{Name: "lin", Source: "linear", SecretFile: "/s", Makes: []store.Make{line}}, "me"); err != nil {
		t.Fatal(err)
	}
	event := func(seq uint64, url string) ruleEvent {
		return ruleEvent{seq: seq, hook: "lin", source: "linear", typ: "assigned", threadURL: url, data: map[string]any{
			"type": "assigned", "text": "Assigned to Yardmaster.",
			"thread": map[string]any{"id": "ENG-34", "title": "Draft the quarterly report", "url": url}}}
	}
	pass := &hookPass{hooks: map[string]*store.Hook{}}
	d.hookEvent(ctx, event(1000, linearSlug), pass, linear)
	links, err := s.IssueLinks(ctx, linearFixed)
	if err != nil || len(links) != 1 || links[0].Kind != store.LinkTracks || links[0].URL != linearFixed {
		t.Fatalf("links to the issue: %+v, %v; want the made bead's tracks link, in the fixed form", links, err)
	}
	made := links[0].Bead
	evs, err := s.Events(ctx, made, 20)
	if err != nil {
		t.Fatal(err)
	}
	var linked bool
	for _, e := range evs {
		if e.Kind == "linked" {
			linked = e.Data["url"] == linearFixed
		}
	}
	if !linked {
		t.Fatalf("the linked event of %s does not show the fixed URL: %+v", made, evs)
	}

	// The issue is renamed, and assigned again: it is the same bead's.
	d.hookEvent(ctx, event(1001, linearRename), pass, linear)
	all, err := s.ListBeads(ctx, store.BeadFilter{})
	if err != nil || len(all) != 1 || all[0].ID != made {
		t.Fatalf("beads after a delivery under the new title: %+v, %v; want only %s", all, err, made)
	}
}
