package web_test

import (
	"context"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/preview"
	"yardr.dev/yardr/internal/previewtest"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
	"yardr.dev/yardr/internal/web"
)

// previewFixture serves a yard with the operator ada, whose web view and
// supervisor share the yard's home, as production's do: the page reads
// the log tail where the supervisor writes it.
func previewFixture(t *testing.T) *fixture {
	t.Helper()
	home := t.TempDir()
	st, own := storetest.NewOwnedIn(t, home)
	if err := st.SetSetting(context.Background(), store.OperatorSetting, "ada", "ada"); err != nil {
		t.Fatal(err)
	}
	srv, err := web.NewServer(st, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(func() { shut(ts) })
	f := &fixture{t: t, st: st, own: own, ts: ts, home: home}
	// A wait that times out says what it waited for; the cards say why it
	// never came (a preview the supervisor gave up on, and what it logged).
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(f.cards())
		}
	})
	return f
}

// cards is what the registry shows of each preview and what it logged last.
func (f *fixture) cards() string {
	all, err := f.own.Previews(context.Background())
	if err != nil {
		return "the registry is not read: " + err.Error()
	}
	if len(all) == 0 {
		return "no preview in the registry"
	}
	var b strings.Builder
	for _, p := range all {
		fmt.Fprintf(&b, "preview %s: %s %q, %d restarts\n", p.App, p.State, p.StateDetail, p.RestartCount)
		lines, _ := preview.LogTail(filepath.Join(f.home, "previews", p.App+".log"), 20)
		for _, l := range lines {
			fmt.Fprintf(&b, "    %s\n", l)
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// bindWait is how long the supervisor of these tests lets a preview take to
// listen: its own default, named here so that the waits are written from it.
// The app listens at once, so it is a fuse against a start that never comes,
// and a wait for the app to answer is no longer than it: the supervisor has
// given the preview up by then, and the card says so.
const bindWait = 10 * time.Second

// supervise runs the yard's supervisor against the fixture's store, with
// shortened clocks as the supervisor's own tests run it.
func (f *fixture) supervise(t *testing.T) *preview.Supervisor {
	t.Helper()
	sup := preview.New(f.own, f.home, nil)
	sup.Poll = 10 * time.Millisecond
	sup.BindWait = bindWait
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go sup.Run(ctx)
	return sup
}

func waitPreview(t *testing.T, what string, cond func() bool) {
	t.Helper()
	waitPreviewWithin(t, what, 15*time.Second, cond)
}

func waitPreviewWithin(t *testing.T, what string, within time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// gitDepot is a directory with one commit, for a proposal to read its
// depot state from.
func gitDepot(t *testing.T) (dir, head string) {
	t.Helper()
	dir = t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "a@b.c"}, {"config", "user.name", "a"},
		{"commit", "-q", "--allow-empty", "-m", "one"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return dir, strings.TrimSpace(string(out))
}

// cardOf is the article of the page that draws app's card, so that a link
// or a line is held to the card it belongs to and not to the page.
func cardOf(t *testing.T, page, app string) string {
	t.Helper()
	for _, a := range strings.Split(page, "<article")[1:] {
		if strings.Contains(a, `<span class="bead">`+app+`</span>`) {
			card, _, _ := strings.Cut(a, "</article>")
			return card
		}
	}
	t.Fatalf("the page has no card of %s:\n%s", app, page)
	return ""
}

// A pending proposal appears as one card, in JSON and HTML, showing the
// start command in full, the directory, the port, the depot commit and
// dirty mark, and the proposer. Approving it starts the preview through
// the supervisor; refusing another leaves the row refused with nothing
// started. A view that reads only draws no button and takes no write.
func TestPreviewsPage(t *testing.T) {
	t.Parallel()
	f := previewFixture(t)
	ctx := context.Background()
	dir, head := gitDepot(t)
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	port := previewtest.FreePort(t, "127.0.0.1")
	command := previewtest.App("127.0.0.1", port)
	if _, err := f.st.ProposePreview(ctx, store.NewPreview{
		App: "demo", Command: command, Dir: dir, Port: port,
	}, "builder"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.ProposePreview(ctx, store.NewPreview{
		App: "other", Command: "serve .", Dir: dir, Port: previewtest.FreePort(t, "127.0.0.1"),
	}, "builder"); err != nil {
		t.Fatal(err)
	}
	acting := f.as(t, "ada")
	var got web.Previews
	acting.getJSON("/previews", &got)
	if got.As != "ada" || len(got.Previews) != 2 {
		t.Fatalf("previews = %+v", got)
	}
	card := got.Previews[0]
	if card.App != "demo" || card.Command != command ||
		card.Dir != dir || card.Port != port || card.Commit != head || !card.Dirty ||
		card.Proposer != "builder" || card.State != store.PreviewPending || card.Head != head || card.Stale {
		t.Fatalf("pending card = %+v", card)
	}
	if got.Idle != "4h" || got.MaxAge != "7d" || got.Head == 0 {
		t.Fatalf("previews lifetime = %q/%q head %d", got.Idle, got.MaxAge, got.Head)
	}
	page := acting.get("/previews").body
	// The page draws the command escaped: the same text, unless the path of
	// the test binary in it holds a character the page escapes.
	for _, want := range []string{
		template.HTMLEscapeString(card.Command), dir, fmt.Sprint(port), head[:12], "(dirty)", "builder",
		"idle 4h · age 7d", `name="action" value="approve">Approve`,
		`name="action" value="refuse">Refuse`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page does not show %q", want)
		}
	}
	// The yard-side path the edge forwards to is no address of this view's:
	// it serves nothing there, so the card names it nowhere.
	for _, not := range []string{"/preview/", "<dt>opens at</dt>"} {
		if strings.Contains(page, not) {
			t.Errorf("the pending card shows %q", not)
		}
	}
	// A view that reads only draws no button and takes no write.
	plain := f.get("/previews").body
	if strings.Contains(plain, `name="action"`) {
		t.Error("the read-only page draws a button")
	}
	if r := f.do("POST", "/previews"); r.status != http.StatusMethodNotAllowed {
		t.Errorf("POST /previews without --as: status %d, want 405", r.status)
	}
	sup := f.supervise(t)
	form := func(app, action string) url.Values { return url.Values{"app": {app}, "action": {action}} }
	if r := acting.postForm("/previews", form("demo", "approve"), "Origin", acting.ts.URL); r.status != http.StatusSeeOther || r.header.Get("Location") != "/previews" {
		t.Fatalf("approve: status %d, location %q", r.status, r.header.Get("Location"))
	}
	waitPreview(t, "approved preview running", func() bool {
		p, err := f.st.GetPreview(ctx, "demo")
		return err == nil && p.State == store.PreviewRunning
	})
	if r := acting.postForm("/previews", form("other", "refuse"), "Origin", acting.ts.URL); r.status != http.StatusSeeOther {
		t.Fatalf("refuse: status %d: %s", r.status, r.body)
	}
	if got, err := f.st.GetPreview(ctx, "other"); err != nil || got.State != store.PreviewRefused || got.Approval != nil {
		t.Fatalf("refused = %+v, %v", got, err)
	}
	if sup.Running("other") {
		t.Error("the refused preview started")
	}
	// One request through the app, so its log has a line to show: the
	// stand-in logs each request, and nothing before the first.
	waitPreviewWithin(t, "app answering", bindWait, func() bool {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
		if err != nil {
			return false
		}
		resp.Body.Close()
		return true
	})
	// The running card shows its log tail and its Stop and Restart, and in
	// Open's place the line that says why it has none: no edge gave it a
	// host; the refused card stays listed with no button.
	var running web.Previews
	waitPreview(t, "log tail on the card", func() bool {
		// Decoded fresh: a reused value would keep a field the
		// answer omits.
		var cur web.Previews
		acting.getJSON("/previews", &cur)
		if len(cur.Previews) == 2 && len(cur.Previews[0].Log) > 0 {
			running = cur
			return true
		}
		return false
	})
	page = acting.get("/previews").body
	if tail := template.HTMLEscapeString(running.Previews[0].Log[0]); !strings.Contains(page, tail) {
		t.Errorf("the page does not show the log tail %q", tail)
	}
	for _, want := range []string{`<span class="faint">no address from the edge yet</span>`, `value="stop">Stop`, `value="restart">Restart`} {
		if !strings.Contains(page, want) {
			t.Errorf("the running card does not show %q", want)
		}
	}
	for _, not := range []string{">Open<", "/preview/"} {
		if strings.Contains(page, not) {
			t.Errorf("the running card with no host shows %q", not)
		}
	}
	if refused := running.Previews[1]; refused.App != "other" || refused.State != store.PreviewRefused || len(refused.Log) != 0 {
		t.Fatalf("refused card = %+v", refused)
	}
	// A view that reads only shows the cards without the start command
	// and the log tail, which are the operator's to read: the approval's
	// command with the card's.
	if running.Previews[0].Approval == nil || running.Previews[0].Approval.Command == "" {
		t.Fatalf("the running card carries no approval command: %+v", running.Previews[0])
	}
	var readOnly web.Previews
	f.getJSON("/previews", &readOnly)
	if len(readOnly.Previews) != 2 {
		t.Fatalf("read-only previews = %+v", readOnly)
	}
	for _, c := range readOnly.Previews {
		if c.Command != "" || len(c.Log) != 0 {
			t.Errorf("the read-only card shows the command or the log: %+v", c)
		}
		if c.Approval != nil && c.Approval.Command != "" {
			t.Errorf("the read-only card shows the approval's command: %+v", c.Approval)
		}
	}
	if body := f.get("/previews").body; strings.Contains(body, template.HTMLEscapeString(card.Command)) {
		t.Error("the read-only page shows the start command")
	}
}

// A running watch is a card of its own kind: the bead it follows and the
// directory it serves, its address and lifetime, Open and Stop, and no
// command, commit or Restart. One that ended leaves the page.
func TestPreviewsPageDrawsARunningWatch(t *testing.T) {
	t.Parallel()
	f := previewFixture(t)
	ctx := context.Background()
	flow, err := store.ParseFlow("backlog -> new\nnew -> review on done | backlog\nreview -> approved on done | new on changes\napproved -> merged\nmerged terminal\n")
	if err != nil {
		t.Fatal(err)
	}
	flow.Name = "site"
	if err := f.st.PutDepot(ctx, store.Depot{Name: "site", Base: "main"}, "ada"); err != nil {
		t.Fatal(err)
	}
	if err := f.st.PutGroup(ctx, store.Group{Name: "builders", Runner: "herdr"}, "ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.PutFlow(ctx, *flow, "ada"); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetFlowPointer(ctx, "site", "", "site", "ada"); err != nil {
		t.Fatal(err)
	}
	b, err := f.st.CreateBead(ctx, store.NewBead{Title: "site", Depot: "site", Stage: "new"}, "ada")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := f.st.Claim(ctx, b.ID, "builders", "new", "site")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tree, "mockups"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetHandle(ctx, sess.ID, map[string]any{"worktree": tree}); err != nil {
		t.Fatal(err)
	}
	p, err := f.st.WatchPreview(ctx, b.ID, "mockups", "", "ada")
	if err != nil {
		t.Fatal(err)
	}

	acting := f.as(t, "ada")
	var got web.Previews
	acting.getJSON("/previews", &got)
	if len(got.Previews) != 1 {
		t.Fatalf("previews = %+v", got)
	}
	card := got.Previews[0]
	if card.App != p.App || card.Kind != store.PreviewKindWatch || card.Bead != b.ID || card.Stage != "new" ||
		card.Dir != filepath.Join(tree, "mockups") || card.State != store.PreviewRunning ||
		card.Command != "" || card.Port != 0 || card.Head != "" || card.Stale || card.Approval != nil {
		t.Fatalf("watch card = %+v", card)
	}
	// No edge has given the watch a host, so its card has no Open and says so
	// in Open's place; it names the yard-side path nowhere, for this view
	// serves nothing there.
	page := acting.get("/previews").body
	for _, want := range []string{
		"watch of", b.ID + " in new", filepath.Join(tree, "mockups"),
		`<span class="faint">no address from the edge yet</span>`, `value="stop">Stop`, "idle 4h · age 7d", "started by",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page does not show %q", want)
		}
	}
	for _, not := range []string{"<dt>command</dt>", "<dt>commit</dt>", "<dt>head</dt>", "<dt>proposed by</dt>", "Restart", "Approve", ">Open<", "/preview/", "<dt>opens at</dt>"} {
		if strings.Contains(page, not) {
			t.Errorf("the watch's card shows %q", not)
		}
	}
	// A host the edge gave the watch is its address, as a link, and Open goes
	// there; a watch it gave none shows no address.
	if strings.Contains(page, "<dt>address</dt>") {
		t.Error("a watch the edge gave no host shows an address")
	}
	host := "p-12.preview.example.net"
	if _, err := f.own.UpdatePreview(ctx, p.App, store.PreviewUpdate{Host: &host}); err != nil {
		t.Fatal(err)
	}
	got = web.Previews{}
	acting.getJSON("/previews", &got)
	if got.Previews[0].Host != host {
		t.Fatalf("watch card host = %q, want %q", got.Previews[0].Host, host)
	}
	page = acting.get("/previews").body
	for _, want := range []string{"<dt>address</dt>", `href="https://` + host + `/"`, `<a href="https://` + host + `/">Open</a>`} {
		if !strings.Contains(page, want) {
			t.Errorf("the watch's card does not show %q", want)
		}
	}
	for _, not := range []string{"no address from the edge yet", "/preview/"} {
		if strings.Contains(page, not) {
			t.Errorf("the watch's card with a host shows %q", not)
		}
	}
	// A refusal of the edge's is the running card's status, in the detail
	// the card shows for a preview that has one, and the host goes: Open with
	// it, and the line in its place carries the refusal.
	none, refusal := "", "the edge refused the report: no previews here (403)"
	if _, err := f.own.UpdatePreview(ctx, p.App, store.PreviewUpdate{Host: &none, StateDetail: &refusal}); err != nil {
		t.Fatal(err)
	}
	page = acting.get("/previews").body
	if !strings.Contains(page, "running ("+refusal+")") || strings.Contains(page, "<dt>address</dt>") {
		t.Errorf("a running watch the edge refused: want its refusal for the status and no address:\n%s", page)
	}
	if want := `<span class="faint">no address from the edge yet (` + refusal + `)</span>`; !strings.Contains(page, want) || strings.Contains(page, ">Open<") {
		t.Errorf("a running watch the edge refused: want %q in Open's place and no Open:\n%s", want, page)
	}
	if r := acting.postForm("/previews", url.Values{"app": {p.App}, "action": {"restart"}}, "Origin", acting.ts.URL); r.status != http.StatusOK ||
		!strings.Contains(r.body, "has no approved form to restart") {
		t.Fatalf("restart of a watch: status %d: %s", r.status, r.body)
	}

	// Stopped, it leaves the page, and it is still a row `yardr preview list` has.
	if r := acting.postForm("/previews", url.Values{"app": {p.App}, "action": {"stop"}}, "Origin", acting.ts.URL); r.status != http.StatusSeeOther {
		t.Fatalf("stop: status %d: %s", r.status, r.body)
	}
	got = web.Previews{}
	acting.getJSON("/previews", &got)
	if len(got.Previews) != 0 {
		t.Fatalf("an ended watch is on the page: %+v", got.Previews)
	}
	if row, err := f.st.GetPreview(ctx, p.App); err != nil || row.State != store.PreviewStopped {
		t.Fatalf("the ended watch = %+v, %v", row, err)
	}
}

// An app's card shows the host the edge gave it as its address, a link, and
// Open goes there. A card the edge gave none has no address and no Open, and
// says why in Open's place, with the edge's refusal after it where the detail
// holds one; a proposal that waits for approval has neither, for it has
// Approve and Refuse. A view that reads only draws no Open: the address is
// its link.
func TestPreviewsPageShowsTheHostTheEdgeGave(t *testing.T) {
	t.Parallel()
	f := previewFixture(t)
	ctx := context.Background()
	dir := t.TempDir()
	for _, app := range []string{"hosted", "plain", "unaddressed"} {
		if _, err := f.st.ProposePreview(ctx, store.NewPreview{App: app, Command: "serve .", Dir: dir, Port: previewtest.FreePort(t, "127.0.0.1")}, "builder"); err != nil {
			t.Fatal(err)
		}
	}
	for _, app := range []string{"hosted", "unaddressed"} {
		if _, err := f.st.ApprovePreview(ctx, app, "ada"); err != nil {
			t.Fatal(err)
		}
	}
	host := "p-3.preview.example.net"
	if _, err := f.own.UpdatePreview(ctx, "hosted", store.PreviewUpdate{Host: &host}); err != nil {
		t.Fatal(err)
	}
	acting := f.as(t, "ada")
	var got web.Previews
	acting.getJSON("/previews", &got)
	if len(got.Previews) != 3 || got.Previews[0].App != "hosted" || got.Previews[0].Host != host || got.Previews[1].Host != "" || got.Previews[2].Host != "" {
		t.Fatalf("previews = %+v, want the first to carry the host the edge gave and the others none", got.Previews)
	}
	page := acting.get("/previews").body
	if n := strings.Count(page, "<dt>address</dt>"); n != 1 || !strings.Contains(page, `href="https://`+host+`/"`) {
		t.Errorf("the page shows %d addresses, want the one link to %s:\n%s", n, host, page)
	}
	const why = "no address from the edge yet"
	if c := cardOf(t, page, "hosted"); !strings.Contains(c, `<a href="https://`+host+`/">Open</a>`) || strings.Contains(c, why) {
		t.Errorf("the card with a host: want Open to https://%s/ and no reason line:\n%s", host, c)
	}
	if c := cardOf(t, page, "plain"); strings.Contains(c, ">Open<") || strings.Contains(c, why) || !strings.Contains(c, `value="approve">Approve`) {
		t.Errorf("the pending card: want Approve, with no Open and no reason line:\n%s", c)
	}
	if c := cardOf(t, page, "unaddressed"); !strings.Contains(c, `<span class="faint">`+why+`</span>`) || strings.Contains(c, ">Open<") || strings.Contains(c, "<dt>address</dt>") {
		t.Errorf("the card with no host: want the reason line, with no Open and no address:\n%s", c)
	}
	// What the edge refused with is kept in the card's state detail, and the
	// line carries it after its own words.
	refusal := "the edge refused the report: previews need a yard on its own server: this yard is docker (403)"
	if _, err := f.own.UpdatePreview(ctx, "unaddressed", store.PreviewUpdate{StateDetail: &refusal}); err != nil {
		t.Fatal(err)
	}
	if c := cardOf(t, acting.get("/previews").body, "unaddressed"); !strings.Contains(c, `<span class="faint">`+why+` (`+refusal+`)</span>`) || strings.Contains(c, ">Open<") {
		t.Errorf("the card the edge refused: want the reason line with the refusal after it, and no Open:\n%s", c)
	}
	// A view that reads only has no buttons, Open among them: the card's
	// address is what it opens from.
	read := f.get("/previews").body
	if c := cardOf(t, read, "hosted"); !strings.Contains(c, `<dd><a href="https://`+host+`/">`+host+`</a></dd>`) || strings.Contains(c, ">Open<") {
		t.Errorf("the card on a view that reads only: want the address link and no Open:\n%s", c)
	}
}

// A crew session's approval attempt is refused on the page, as the
// store's operator rule refuses it: the page comes back redrawn with
// why, and the proposal stays pending.
func TestPreviewsCrewApprovalRefused(t *testing.T) {
	t.Parallel()
	f := previewFixture(t)
	ctx := context.Background()
	if _, err := f.st.AddUser(ctx, "builder", "ada"); err != nil {
		t.Fatal(err)
	}
	port := previewtest.FreePort(t, "127.0.0.1")
	if _, err := f.st.ProposePreview(ctx, store.NewPreview{
		App: "demo", Command: "serve .", Dir: t.TempDir(), Port: port,
	}, "builder"); err != nil {
		t.Fatal(err)
	}
	acting := f.as(t, "builder")
	r := acting.postForm("/previews", url.Values{"app": {"demo"}, "action": {"approve"}}, "Origin", acting.ts.URL)
	if r.status != http.StatusOK {
		t.Fatalf("crew approve: status %d, want the redrawn page: %s", r.status, r.body)
	}
	if !strings.Contains(r.body, "may approve a preview") {
		t.Errorf("the redrawn page does not carry the store's refusal: %s", r.body)
	}
	if got, err := f.st.GetPreview(ctx, "demo"); err != nil || got.State != store.PreviewPending {
		t.Fatalf("after crew approve = %+v, %v", got, err)
	}
}

// After the depot HEAD moves past the running commit, the card carries
// the stale mark. Restart does not run the approved command at the new
// commit: the approval was given on the old one, so the page comes back
// with why, the card is pending again at the new commit with its process
// stopped, and the preview runs at that commit once approved anew.
func TestPreviewsStaleAndRestart(t *testing.T) {
	t.Parallel()
	f := previewFixture(t)
	ctx := context.Background()
	dir, first := gitDepot(t)
	port := previewtest.FreePort(t, "127.0.0.1")
	if _, err := f.st.ProposePreview(ctx, store.NewPreview{
		App: "demo", Command: previewtest.App("127.0.0.1", port),
		Dir: dir, Port: port,
	}, "builder"); err != nil {
		t.Fatal(err)
	}
	acting := f.as(t, "ada")
	sup := f.supervise(t)
	if r := acting.postForm("/previews", url.Values{"app": {"demo"}, "action": {"approve"}}, "Origin", acting.ts.URL); r.status != http.StatusSeeOther {
		t.Fatalf("approve: status %d: %s", r.status, r.body)
	}
	waitPreview(t, "preview running", func() bool {
		p, err := f.st.GetPreview(ctx, "demo")
		return err == nil && p.State == store.PreviewRunning
	})
	if out, err := exec.Command("git", "-C", dir, "commit", "-q", "--allow-empty", "-m", "two").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	second := strings.TrimSpace(string(out))
	var got web.Previews
	acting.getJSON("/previews", &got)
	if len(got.Previews) != 1 || !got.Previews[0].Stale || got.Previews[0].Head != second || got.Previews[0].Commit != first {
		t.Fatalf("stale card = %+v", got.Previews)
	}
	if page := acting.get("/previews").body; !strings.Contains(page, "stale") {
		t.Error("the page does not mark the stale preview")
	}
	r := acting.postForm("/previews", url.Values{"app": {"demo"}, "action": {"restart"}}, "Origin", acting.ts.URL)
	if r.status != http.StatusOK || !strings.Contains(r.body, "approval is taken back") {
		t.Fatalf("restart on the moved depot: status %d, want the page redrawn with why: %s", r.status, r.body)
	}
	if p, err := f.st.GetPreview(ctx, "demo"); err != nil || p.State != store.PreviewPending ||
		p.StateDetail != store.PreviewTreeChangedDetail || p.Approval != nil || p.Commit != second {
		t.Fatalf("after the refused restart = %+v, %v, want pending at %q with no approval", p, err, second)
	}
	// Stopped means gone from the supervisor and the port it held answering
	// no more: the approval given next starts on that port again.
	waitPreview(t, "the taken-back preview's process stopped", func() bool {
		if sup.Running("demo") {
			return false
		}
		c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)), time.Second)
		if err != nil {
			return true
		}
		c.Close()
		return false
	})
	// The pending card is the one the page offers Approve on.
	if page := acting.get("/previews").body; !strings.Contains(page, `value="approve">Approve`) || !strings.Contains(page, store.PreviewTreeChangedDetail) {
		t.Errorf("the page does not offer the taken-back card for approval: %s", page)
	}
	if r := acting.postForm("/previews", url.Values{"app": {"demo"}, "action": {"approve"}}, "Origin", acting.ts.URL); r.status != http.StatusSeeOther {
		t.Fatalf("approve anew: status %d: %s", r.status, r.body)
	}
	waitPreview(t, "start at the new commit", func() bool {
		p, err := f.st.GetPreview(ctx, "demo")
		return err == nil && p.State == store.PreviewRunning && p.Commit == second
	})
	// Decoded fresh: the earlier decode's stale mark must not survive
	// into this answer, which omits it.
	var after web.Previews
	acting.getJSON("/previews", &after)
	if len(after.Previews) != 1 || after.Previews[0].Stale {
		t.Fatalf("after restart = %+v, want no stale mark", after.Previews)
	}
}
