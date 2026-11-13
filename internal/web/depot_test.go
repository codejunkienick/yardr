package web_test

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/web"
)

// depotYard is the populated yard with an operator, ready for asks: the
// depot line with the code and bugs flows, the depot loose with none, the
// held bead in backlog, and the dropped bead closed.
func depotYard(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.populate()
	must(t, f.st.SetSetting(context.Background(), store.OperatorSetting, "mckean", "me"))
	return f
}

// The depot's page as JSON: its four counts, its flows with their strips,
// what waits on the viewer, what landed. Waiting is the rail pill's
// predicate over this depot's beads: the held bead and the bead parked in
// a people stage carry no ask and stand in none of it, and neither does
// the asked bead with no depot.
func TestDepotJSON(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := depotYard(t)
	bs := f.byTitle()
	_, err := f.st.AddAsk(ctx, bs["three"].ID, pickAsk(), "yardmaster")
	must(t, err)
	adrift, err := f.st.CreateBead(ctx, store.NewBead{Title: "adrift", Stage: "new"}, "me")
	must(t, err)
	_, err = f.st.AddAsk(ctx, adrift.ID, pickAsk(), "yardmaster")
	must(t, err)
	must(t, f.st.PutGroup(ctx, store.Group{Name: "decisions", Runner: "manual", Members: []string{"mckean"}}, "me"))
	_, err = f.st.AddRoute(ctx, store.Route{Stage: "backlog", Group: "decisions"}, "me")
	must(t, err)
	seven, err := f.st.CreateBead(ctx, store.NewBead{Title: "seven", Stage: "review", Depot: "line"}, "me")
	must(t, err)
	_, err = f.st.AdvanceNote(ctx, seven.ID, "merged", "assembly: merged 70dabcd", store.Expect{}, "assembly")
	must(t, err)

	var got web.DepotPage
	f.getJSON("/depots/line", &got)
	if got.Name != "line" || got.Kind != store.DepotGit || got.Tag != "" || got.Error != "" {
		t.Errorf("depot %+v, want line, git, no tag, no error", got)
	}
	if got.ActiveFlows != 2 || got.QuietFlows != 0 || len(got.Quiet) != 0 {
		t.Errorf("flows %d active, %d quiet (%v), want 2 active, none quiet", got.ActiveFlows, got.QuietFlows, got.Quiet)
	}
	// Waiting 1: three's ask. Transit 1: three past backlog. Backlog 3:
	// one, two and five in their first stages. Landed 1: seven; four was
	// dropped.
	if got.Waiting != 1 || got.Transit != 1 || got.Backlog != 3 || got.Landed != 1 {
		t.Errorf("counts waiting %d, transit %d, backlog %d, landed %d, want 1, 1, 3, 1",
			got.Waiting, got.Transit, got.Backlog, got.Landed)
	}
	wantFlows := []web.DepotFlowRow{
		{Name: "code", Src: "store", OnYou: 1, Transit: 1, Backlog: 2, Landed: 1, Stages: []web.DepotStage{
			{Stage: "backlog", Open: 2, Kind: "first"},
			{Stage: "new", Open: 1, Kind: "ask"},
			{Stage: "review", Open: 0, Kind: "empty"},
			{Stage: "merged", Open: 1, Kind: "merged"},
		}},
		{Name: "bugs", Type: "bug", Src: "bug · store, type bug", Backlog: 1, Stages: []web.DepotStage{
			{Stage: "triage", Open: 1, Kind: "first"},
			{Stage: "fixed", Open: 0, Kind: "empty"},
		}},
	}
	if !reflect.DeepEqual(got.Flows, wantFlows) {
		t.Errorf("flows\n got %+v\nwant %+v", got.Flows, wantFlows)
	}
	if len(got.Waits) != 1 || got.Waits[0].Bead != bs["three"].ID || got.Waits[0].Flow != "code" || got.Waits[0].Stage != "new" {
		t.Errorf("waits %+v, want three's ask in code · new", got.Waits)
	}
	if len(got.Landings) != 1 || got.Landings[0].ID != seven.ID || got.Landings[0].Title != "seven" {
		t.Errorf("landings %+v, want seven", got.Landings)
	} else if got.Landings[0].Age == "" || time.Since(got.Landings[0].ClosedAt) > time.Minute {
		t.Errorf("landing %+v, want a fresh close with its age", got.Landings[0])
	}
	// Waiting equals the line's asked beads in this depot: the rail
	// pill's predicate restricted to it, with the depot-less ask left
	// out.
	var l web.Line
	f.getJSON("/line", &l)
	inDepot, total := 0, 0
	for _, ws := range [][]web.Waiting{l.Waits, l.Parked} {
		for _, w := range ws {
			if w.HasAsk() {
				total++
				if w.Depot == "line" {
					inDepot++
				}
			}
		}
	}
	if inDepot != got.Waiting || total != 2 {
		t.Errorf("waiting %d, line asked in depot %d of %d, want 1 of 2", got.Waiting, inDepot, total)
	}
}

// Landed keeps the beads that landed in the depot in the rolling 24
// hours, the newest first: an older landing, a dropped bead, an open one,
// one with no close time, and beads of another depot or none stand in
// none of it.
func TestDepotLandedWindow(t *testing.T) {
	t.Parallel()
	now := time.Now()
	at := func(d time.Duration) *time.Time { t := now.Add(-d); return &t }
	line, loose := "line", "loose"
	mk := func(id, stage, status string, depot *string, closedAt *time.Time) store.Bead {
		return store.Bead{ID: id, Title: id, Stage: stage, Status: status, Depot: depot, ClosedAt: closedAt}
	}
	beads := []store.Bead{
		mk("fresh", "merged", "closed", &line, at(time.Hour)),
		mk("old", "merged", "closed", &line, at(25*time.Hour)),
		mk("edge", "merged", "closed", &line, at(24*time.Hour)),
		mk("dropped", "dropped", "closed", &line, at(time.Hour)),
		mk("open", "merged", "open", &line, nil),
		mk("noclose", "merged", "closed", &line, nil),
		mk("elsewhere", "merged", "closed", &loose, at(time.Hour)),
		mk("adrift", "merged", "closed", nil, at(time.Hour)),
	}
	var ids []string
	for _, b := range web.LandedSince(beads, "line", now) {
		ids = append(ids, b.ID)
	}
	if !reflect.DeepEqual(ids, []string{"fresh", "edge"}) {
		t.Errorf("landed %v, want fresh and edge, newest first", ids)
	}
}

// The strips show their kinds: the ask orange wins over the first-stage
// grey, merged green for a reopened landing, closed grey, an empty stage
// a tick. A flow with nothing in it folds into one quiet row.
func TestDepotStrips(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := depotYard(t)
	bs := f.byTitle()
	_, err := f.st.AddAsk(ctx, bs["one"].ID, pickAsk(), "yardmaster")
	must(t, err)
	three := bs["three"]
	_, err = f.st.AdvanceNote(ctx, three.ID, "review", "", store.Expect{}, "me")
	must(t, err)
	_, err = f.st.AdvanceNote(ctx, three.ID, "merged", "assembly: merged 70dabcd", store.Expect{}, "me")
	must(t, err)
	_, err = f.st.Reopen(ctx, three.ID, store.Expect{}, "me")
	must(t, err)
	ops, err := store.ParseFlow("open -> closed\n")
	must(t, err)
	ops.Name = "ops"
	_, err = f.st.PutFlow(ctx, *ops, "me")
	must(t, err)
	must(t, f.st.SetFlowPointer(ctx, "line", "op", "ops", "me"))
	_, err = f.st.CreateBead(ctx, store.NewBead{Title: "outage", Type: "op", Stage: "closed", Depot: "line"}, "me")
	must(t, err)
	docs, err := store.ParseFlow("draft -> done\ndone terminal\n")
	must(t, err)
	docs.Name = "docs"
	_, err = f.st.PutFlow(ctx, *docs, "me")
	must(t, err)
	must(t, f.st.SetFlowPointer(ctx, "line", "doc", "docs", "me"))

	var got web.DepotPage
	f.getJSON("/depots/line", &got)
	if got.Landed != 0 {
		t.Errorf("landed %d, want none: the landing was reopened and four was dropped", got.Landed)
	}
	var names []string
	for _, fl := range got.Flows {
		names = append(names, fl.Name)
	}
	if !reflect.DeepEqual(names, []string{"code", "bugs", "ops"}) {
		t.Fatalf("flows %v, want code, bugs and ops with docs folded", names)
	}
	if !reflect.DeepEqual(got.Quiet, []string{"docs"}) || got.ActiveFlows != 3 || got.QuietFlows != 1 {
		t.Errorf("quiet %v (%d active, %d quiet), want docs folded", got.Quiet, got.ActiveFlows, got.QuietFlows)
	}
	want := map[string][]web.DepotStage{
		"code": {
			{Stage: "backlog", Open: 2, Kind: "ask"},
			{Stage: "new", Open: 0, Kind: "empty"},
			{Stage: "review", Open: 0, Kind: "empty"},
			{Stage: "merged", Open: 1, Kind: "merged"},
		},
		"ops": {
			{Stage: "open", Open: 0, Kind: "empty"},
			{Stage: "closed", Open: 1, Kind: "closed"},
		},
	}
	for _, fl := range got.Flows {
		if stages, ok := want[fl.Name]; ok && !reflect.DeepEqual(fl.Stages, stages) {
			t.Errorf("%s strip\n got %+v\nwant %+v", fl.Name, fl.Stages, stages)
		}
	}
	if fl := got.Flows[2]; fl.Src != "op · store, type op" || fl.Transit != 1 || fl.Backlog != 0 {
		t.Errorf("ops %+v, want src op · store, type op with the bead in transit", fl)
	}
}

// The same request without the header is the page: the breadcrumb, the
// title block, the four counts with only Waiting a link, the flows with
// their strips, the waiting rows to their beads, the landed rows plain.
func TestDepotHTML(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := depotYard(t)
	bs := f.byTitle()
	_, err := f.st.AddAsk(ctx, bs["three"].ID, pickAsk(), "yardmaster")
	must(t, err)
	seven, err := f.st.CreateBead(ctx, store.NewBead{Title: "seven", Stage: "review", Depot: "line"}, "me")
	must(t, err)
	_, err = f.st.AdvanceNote(ctx, seven.ID, "merged", "assembly: merged 70dabcd", store.Expect{}, "assembly")
	must(t, err)
	docs, err := store.ParseFlow("draft -> done\ndone terminal\n")
	must(t, err)
	docs.Name = "docs"
	_, err = f.st.PutFlow(ctx, *docs, "me")
	must(t, err)
	must(t, f.st.SetFlowPointer(ctx, "line", "doc", "docs", "me"))

	r := f.get("/depots/line")
	if r.status != 200 || !strings.HasPrefix(r.header.Get("Content-Type"), "text/html") {
		t.Fatalf("status %d, type %q", r.status, r.header.Get("Content-Type"))
	}
	s := squash(r.body)
	for _, want := range []string{
		"<title>line · depot</title>",
		`<a href="/depots">Depots</a><span aria-hidden="true">›</span> line`,
		`<h2>line</h2>`,
		`Depot · 2 active flows, 1 quiet`,
		`<a class="depot-card depot-wait" href="/"><span class="depot-card-label">Waiting on you</span><span class="depot-card-num">1</span></a>`,
		`<span class="depot-card-label">In transit</span><span class="depot-card-num depot-transit">1</span>`,
		`<span class="depot-card-label">Backlog · waits on shaping</span><span class="depot-card-num">3</span>`,
		`<span class="depot-card-label">Landed · last 24h</span><span class="depot-card-num">1</span>`,
		`bug · store, type bug`,
		`<span class="seg seg-ask" title="new · 1">1</span>`,
		`<span class="seg seg-merged" title="merged · 1">1</span>`,
		`<span class="seg seg-muted" title="backlog · 2">2</span>`,
		`<span class="seg seg-empty" title="review · empty"></span>`,
		`1 quiet flow`,
		`<span class="depot-sec-sub">1 in line</span>`,
		`<a class="depot-open" href="/">Open Decide</a>`,
		`<a href="/beads/` + bs["three"].ID + `"><span class="depot-dot" aria-hidden="true"></span><span class="depot-id">` + shortID(bs["three"].ID) + `</span><span class="depot-row-title">three</span><span class="depot-where">code · new</span></a>`,
		`<span class="depot-sec-sub">1 in the last 24h</span>`,
		`<span class="depot-row-title">seven</span>`,
		` ago</span></li>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	if n := strings.Count(s, `<div class="depot-card">`); n != 3 {
		t.Errorf("the page has %d plain cards, want 3: only Waiting links", n)
	}
	// The landed row is plain: seven's id stands short, and no link
	// carries it to its bead.
	if !strings.Contains(s, `<span class="depot-id">`+shortID(seven.ID)+`</span>`) {
		t.Errorf("the landed row names no short id for seven")
	}
	if strings.Contains(s, "/beads/"+seven.ID) {
		t.Errorf("the landed row links seven to its bead")
	}
	// No New bead button: not in this chain, nothing dead is drawn.
	if strings.Contains(s, "New bead") {
		t.Errorf("the page draws a New bead button")
	}
}

// The sidebar marks the depot the page stands on, and none on any other
// page.
func TestDepotSidebarCurrent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := depotYard(t)
	s := sidebarSection(t, squash(f.get("/depots/line").body))
	if want := `<a class="side-row" href="/depots/line" title="line — 1 in transit" aria-label="line — 1 in transit" aria-current="page">`; !strings.Contains(s, want) {
		t.Errorf("the sidebar lacks %s: %s", want, s)
	}
	if n := strings.Count(s, "aria-current"); n != 1 {
		t.Errorf("the sidebar marks %d rows current, want line alone: %s", n, s)
	}
	if s := sidebarSection(t, squash(f.get("/").body)); strings.Contains(s, "aria-current") {
		t.Errorf("the sidebar marks a depot current off its page: %s", s)
	}
	must(t, f.st.PutDepot(ctx, store.Depot{Name: "empty", Base: "origin/main"}, "me"))
	s = sidebarSection(t, squash(f.get("/depots/empty").body))
	if !strings.Contains(s, `<details class="side-quiet" open>`) {
		t.Errorf("the quiet-depot group is closed on its current page: %s", s)
	}
	if want := `<a class="side-row" href="/depots/empty" aria-current="page"><span class="side-label">empty</span></a>`; !strings.Contains(s, want) {
		t.Errorf("the quiet depot row is not marked current: %s", s)
	}
	if n := strings.Count(s, "aria-current"); n != 1 {
		t.Errorf("the sidebar marks %d rows current on a quiet depot page, want empty alone: %s", n, s)
	}
}

// A depot with no flow still has its page: every bead in transit, the
// table replaced by the line that says so, the sections empty.
func TestDepotNoFlows(t *testing.T) {
	t.Parallel()
	f := depotYard(t)
	var got web.DepotPage
	f.getJSON("/depots/loose", &got)
	if got.Name != "loose" || got.Waiting != 0 || got.Transit != 1 || got.Backlog != 0 || got.Landed != 0 {
		t.Errorf("loose %+v, want the bead in transit and nothing else", got)
	}
	if len(got.Flows) != 0 || len(got.Quiet) != 0 || got.ActiveFlows != 0 || got.QuietFlows != 0 {
		t.Errorf("loose flows %+v quiet %v, want none at all", got.Flows, got.Quiet)
	}
	s := squash(f.get("/depots/loose").body)
	for _, want := range []string{
		`Depot · 0 active flows, 0 quiet`,
		"No flow governs this depot.",
		"Nothing waits on you in this depot.",
		"Nothing landed in the last 24h.",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
}

// A depot nobody knows is a 404, as JSON and as HTML.
func TestDepotNotFound(t *testing.T) {
	t.Parallel()
	f := depotYard(t)
	r := f.get("/depots/nosuch")
	if r.status != http.StatusNotFound || !strings.Contains(r.body, "depot nosuch") {
		t.Errorf("HTML: status %d, want 404 naming the depot: %s", r.status, r.body)
	}
	r = f.get("/depots/nosuch", "Accept", "application/json")
	if r.status != http.StatusNotFound || !strings.Contains(r.body, "depot nosuch") {
		t.Errorf("JSON: status %d, want 404 naming the depot: %s", r.status, r.body)
	}
}

// gitRepo writes files under a fresh git repository and returns its
// directory and HEAD.
func gitRepo(t *testing.T, files map[string]string) (dir, head string) {
	t.Helper()
	dir = t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	for name, text := range files {
		p := filepath.Join(dir, name)
		must(t, os.MkdirAll(filepath.Dir(p), 0o755))
		must(t, os.WriteFile(p, []byte(text), 0o644))
	}
	run("add", "-A")
	run("commit", "-q", "-m", "flows")
	return dir, run("rev-parse", "HEAD")
}

// The title block's tag is the depot's own flow's commit, shortened. A
// dir depot reads its flows from its directory and carries none.
func TestDepotTag(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir, head := gitRepo(t, map[string]string{
		".yardr/flows/default.flow": "backlog -> new\nnew -> merged\nmerged terminal\n",
	})
	f := depotYard(t)
	must(t, f.st.PutDepot(ctx, store.Depot{Name: "site", Path: dir, Base: "main"}, "me"))
	_, err := f.st.CreateBead(ctx, store.NewBead{Title: "one", Stage: "backlog", Depot: "site"}, "me")
	must(t, err)
	plain := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(plain, ".yardr", "flows"), 0o755))
	must(t, os.WriteFile(filepath.Join(plain, ".yardr", "flows", "default.flow"), []byte("backlog -> merged\nmerged terminal\n"), 0o644))
	must(t, f.st.PutDepot(ctx, store.Depot{Name: "plain", Kind: store.DepotDir, Path: plain}, "me"))
	_, err = f.st.CreateBead(ctx, store.NewBead{Title: "two", Stage: "backlog", Depot: "plain"}, "me")
	must(t, err)

	var site web.DepotPage
	f.getJSON("/depots/site", &site)
	if want := "repo@" + head[:7]; site.Tag != want {
		t.Errorf("tag %q, want %q", site.Tag, want)
	}
	if len(site.Flows) != 1 || site.Flows[0].Name != "default" || site.Flows[0].Src != "repo@"+head[:7] {
		t.Errorf("flows %+v, want default at the commit", site.Flows)
	}
	if s := squash(f.get("/depots/site").body); !strings.Contains(s, "Depot · 1 active flow, 0 quiet · repo@"+head[:7]) {
		t.Errorf("the title block lacks the tag: %s", s)
	}
	var dirDepot web.DepotPage
	f.getJSON("/depots/plain", &dirDepot)
	if dirDepot.Tag != "" {
		t.Errorf("tag %q, want none for a dir depot", dirDepot.Tag)
	}
	if len(dirDepot.Flows) != 1 || dirDepot.Flows[0].Src != "repo (directory)" {
		t.Errorf("flows %+v, want default from the directory", dirDepot.Flows)
	}
}
