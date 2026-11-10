package dispatch_test

// The pr flow of the contributors pack, whole, as the dispatcher runs it: the
// pack's flow, its makes and says tables and the routes its yard adds for the
// yard's posting of the review, CI and merge, with GitHub's deliveries
// normalized through the github pack as a yard does. A bead is made by a pull
// request's event, is given to the posting in review-post and to the yard's CI
// in ciwait, and is routed from approved by its label, all by the events and
// reports a person or a script would make; what post-review, pr-ci and
// merge-pr do with a bead they are given is contrib/assembly's test.

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/dispatch"
	"yardr.dev/yardr/internal/pack"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/packs"
)

// newContributorsYard is a yard whose depot yard, of acme/yardr, follows the
// contributors pack for beads of the type pr: its flow, and the hook gh with
// the pack's tables. The yard has the groups and routes docs/line.md, "Posting
// a pull request's review", "The yard as the CI of a pull request" and
// "Merging pull requests", set up: post takes a pr bead in review-post, pr-ci
// one in ciwait, and pr-merges one in approved that carries merge-ok, as a
// yard default that outranks the depot's own route of a pr bead in approved to
// the decisions, at 11. The review stage has no route: the reviewer's report
// is made by the test, as is what a session of post or of pr-ci reports.
//
// The hook carries the pack's stage map as well as its says and makes, and the
// dispatcher logs to r.logged.
func newContributorsYard(t *testing.T) *ghDepot {
	t.Helper()
	return newContributorsYardIn(t, t.TempDir())
}

// newContributorsYardIn is newContributorsYard in the yard's home home.
func newContributorsYardIn(t *testing.T, home string) *ghDepot {
	t.Helper()
	r := newGHDepotIn(t, home)
	ctx := context.Background()
	sub, err := fs.Sub(packs.FS, packs.Contributors)
	must(t, err)
	p, err := pack.Load(sub, packs.Embedded)
	must(t, err)
	for _, f := range p.Flows {
		if f.Name == "pr" {
			_, err := r.api.PutFlow(ctx, f.StoreFlow("contributors.pr"), "me")
			must(t, err)
		}
	}
	must(t, r.api.SetFlowPointer(ctx, "yard", "pr", "contributors.pr", "me"))
	h, err := r.api.GetHook(ctx, "gh")
	must(t, err)
	h.Says, h.Makes, h.Map = p.Sources[0].Says, p.Sources[0].Makes, p.Sources[0].Map
	_, err = r.api.PutHook(ctx, *h, "me")
	must(t, err)
	// The operator of a yard that has its beads move their issues has let the
	// hook's crew member set a status in github. Without that grant a move the
	// map queued is skipped quietly (no_grant) and never reaches the source, so
	// a test could not see one fail and be tried again.
	_, err = r.api.PutCrew(ctx, store.CrewMember{Name: "yardmaster", Runner: "fake", Config: map[string]any{store.SetState: "github"}}, "me")
	must(t, err)
	r.d.Log = slog.New(slog.NewTextHandler(&r.logged, nil))

	must(t, r.api.PutGroup(ctx, store.Group{Name: "post", Runner: "fake", Limit: 1}, "me"))
	must(t, r.api.PutGroup(ctx, store.Group{Name: "pr-ci", Runner: "fake", Limit: 1}, "me"))
	must(t, r.api.PutGroup(ctx, store.Group{Name: "pr-merges", Runner: "fake", Limit: 1}, "me"))
	pr, merge, everyone := "pr", "merge-ok", store.OriginatorAny
	depot := "yard"
	_, err = r.api.AddRoute(ctx, store.Route{Stage: "review-post", Type: &pr, Originator: &everyone, Group: "post"}, "me")
	must(t, err)
	_, err = r.api.AddRoute(ctx, store.Route{Stage: "ciwait", Type: &pr, Originator: &everyone, Group: "pr-ci"}, "me")
	must(t, err)
	_, err = r.api.AddRoute(ctx, store.Route{Stage: "approved", Type: &pr, Label: &merge, Originator: &everyone, Group: "pr-merges"}, "me")
	must(t, err)
	_, err = r.api.AddRoute(ctx, store.Route{Stage: "approved", Type: &pr, Depot: &depot, Originator: &everyone, Group: "decisions"}, "me")
	must(t, err)
	r.d.DepotRepo = func(_ context.Context, q dispatch.DepotRepoQuery) (string, error) {
		if q.Depot.Name == "yard" {
			return "acme/yardr", nil
		}
		return "", nil
	}
	// The head of a pull request that no event has told is read live.
	r.d.PRHead = func(context.Context, dispatch.PRQuery) (string, error) { return fixtureSHA, nil }
	return r
}

// movedSHA is the head a push moves the fixtures' pull request to, from
// fixtureSHA.
const movedSHA = "c0ffee5a1e0f4b2d8a97163e5d4c3b2a1f0e9d8c"

// deliverJSON records fixture name as GitHub delivered it, its JSON changed
// by each of edits first.
func (r *ghDepot) deliverJSON(t *testing.T, event, name string, edits ...func(map[string]any)) {
	t.Helper()
	body, err := os.ReadFile("../hooks/testdata/github/" + name + ".json")
	must(t, err)
	var m map[string]any
	must(t, json.Unmarshal(body, &m))
	for _, edit := range edits {
		edit(m)
	}
	out, err := json.Marshal(m)
	must(t, err)
	r.record(t, "gh", event, string(out))
}

// prEvent edits the pull_request fixtures to pull request number by
// sender, a user or a bot, from a branch of the repository itself.
func prEvent(number int, login, kind string) func(map[string]any) {
	return func(m map[string]any) {
		who := map[string]any{"login": login, "type": kind}
		m["number"], m["sender"] = number, who
		pr := m["pull_request"].(map[string]any)
		pr["number"], pr["user"] = number, who
		pr["html_url"] = fmt.Sprintf("https://github.com/acme/yardr/pull/%d", number)
		pr["title"] = "build(deps): bump golang.org/x/net"
		pr["head"].(map[string]any)["ref"] = "dependabot/go_modules/x-net"
	}
}

// pushedBy edits the pull_request fixtures to a push by login, a user or a
// bot, which is not the author of the pull request when it follows prEvent.
func pushedBy(login, kind string) func(map[string]any) {
	return func(m map[string]any) {
		m["sender"] = map[string]any{"login": login, "type": kind}
	}
}

// atHead edits the pull_request, workflow_run and check_suite fixtures to the
// head commit sha: a push to the pull request, and the run of CI on it.
func atHead(sha string) func(map[string]any) {
	return func(m map[string]any) {
		if pr, ok := m["pull_request"].(map[string]any); ok {
			pr["head"].(map[string]any)["sha"] = sha
		}
		for _, key := range []string{"workflow_run", "check_suite"} {
			if run, ok := m[key].(map[string]any); ok {
				run["head_sha"] = sha
			}
		}
	}
}

// authorIs edits the pull_request fixtures to a pull request whose author is
// association to the repository, as GitHub spells it: OWNER, MEMBER, NONE.
func authorIs(association string) func(map[string]any) {
	return func(m map[string]any) {
		m["pull_request"].(map[string]any)["author_association"] = association
	}
}

// bearing edits the pull_request fixtures to a pull request that carries the
// labels, as GitHub lists a pull request's labels in every event about it.
func bearing(labels ...string) func(map[string]any) {
	return func(m map[string]any) {
		var list []any
		for _, l := range labels {
			list = append(list, map[string]any{"name": l})
		}
		m["pull_request"].(map[string]any)["labels"] = list
	}
}

// labeledBy edits the pull_request fixtures to login, a user or a bot, putting
// the label on the pull request.
func labeledBy(label, login, kind string) func(map[string]any) {
	return func(m map[string]any) {
		m["action"], m["label"] = "labeled", map[string]any{"name": label}
		bearing(label)(m)
		pushedBy(login, kind)(m)
	}
}

// ciRun edits the workflow_run and check_suite fixtures to a run that
// concluded, on the pull request number, or on none when number is 0, as a
// fork's runs are told.
func ciRun(key string, number int, conclusion string) func(map[string]any) {
	return func(m map[string]any) {
		r := m[key].(map[string]any)
		r["conclusion"], r["head_branch"] = conclusion, "dependabot/go_modules/x-net"
		r["pull_requests"] = []any{}
		if number > 0 {
			r["pull_requests"] = []any{map[string]any{"number": number, "head": map[string]any{"ref": "dependabot/go_modules/x-net"}, "base": map[string]any{"ref": "main"}}}
		}
	}
}

// reviewerSays reports bead id done as the reviewer's session would, with
// merge-ok when label is set, which the reviewer adds before it reports, and
// then what the session of post would report once it posted the stored review,
// before any pass has given it the bead: the bead is in ciwait. The posting
// itself is TestContributorsReviewIsPostedBeforeTheWait's.
func (r *ghDepot) reviewerSays(t *testing.T, id string, label bool) {
	t.Helper()
	ctx := context.Background()
	if label {
		_, err := r.api.UpdateBead(ctx, id, store.BeadPatch{AddLabels: []string{"merge-ok"}}, store.Expect{}, "me")
		must(t, err)
	}
	_, err := r.api.Done(ctx, id, "done", "Ready", store.Expect{Stage: "review"}, "me")
	must(t, err)
	r.postSays(t, id, "done")
}

// reviewerAsksChanges reports bead id changes as the reviewer's session would,
// and then what the session of post would report once it posted the stored
// review: the bead is with its contributor.
func (r *ghDepot) reviewerAsksChanges(t *testing.T, id string) {
	t.Helper()
	_, err := r.api.Done(context.Background(), id, "changes", "needs work", store.Expect{Stage: "review"}, "me")
	must(t, err)
	r.postSays(t, id, "changes")
}

// postSays reports bead id from review-post as the session of post would after
// it posted the stored review on the pull request.
func (r *ghDepot) postSays(t *testing.T, id, outcome string) {
	t.Helper()
	_, err := r.api.Done(context.Background(), id, outcome, "post-review: posted", store.Expect{Stage: "review-post"}, "me")
	must(t, err)
}

// ciSays reports bead id as the session of pr-ci would after the depot's tests
// ran on its head: passed, with the head recorded as ci_head, or not.
func (r *ghDepot) ciSays(t *testing.T, id string, passed bool) {
	t.Helper()
	ctx := context.Background()
	if !passed {
		_, err := r.api.Done(ctx, id, "changes", "the tests failed", store.Expect{Stage: "ciwait"}, "me")
		must(t, err)
		return
	}
	_, err := r.api.UpdateBead(ctx, id, store.BeadPatch{Metadata: map[string]any{"ci_head": fixtureSHA}}, store.Expect{}, "me")
	must(t, err)
	_, err = r.api.Done(ctx, id, "done", "the tests passed", store.Expect{Stage: "ciwait"}, "me")
	must(t, err)
}

// madeIn is the bead the pull request number made, in want, after the pass.
func (r *ghDepot) madeIn(t *testing.T, number int, want string) *store.Bead {
	t.Helper()
	r.pass(t)
	b := r.made(t, fmt.Sprintf("https://github.com/acme/yardr/pull/%d", number))
	if b.Type != "pr" || b.Stage != want {
		t.Fatalf("pull request %d made %s of type %s in %s, want a pr in %s", number, b.ID, b.Type, b.Stage, want)
	}
	return b
}

// routedTo is the group the yard's routes give bead id to now.
func (r *ghDepot) routedTo(t *testing.T, id string) string {
	t.Helper()
	routes, err := r.api.Routes(context.Background())
	must(t, err)
	dispatch.SortRoutes(routes)
	b := r.bead(t, id)
	if route := dispatch.Match(routes, *b, "", nil); route != nil {
		return route.Group
	}
	return ""
}

// reviewedBy gives the yard the group of pr-reviewers of a fake runner, and a
// route to it for pull requests in review, which the yard of these tests
// leaves unrouted: they report the reviewer's verdict themselves.
func (r *ghDepot) reviewedBy(t *testing.T, group string) {
	t.Helper()
	ctx := context.Background()
	must(t, r.api.PutGroup(ctx, store.Group{Name: group, Runner: "fake", Limit: 5}, "me"))
	pr, everyone := "pr", store.OriginatorAny
	_, err := r.api.AddRoute(ctx, store.Route{Stage: "review", Type: &pr, Originator: &everyone, Group: group}, "me")
	must(t, err)
}

// peopleStages gives the stages of the pr flow that people work their groups
// and routes, and returns the group of each. Triage, contributor and decide
// are stages of people, crossed as the hook's crew member: the members of the
// maintainers and of the decisions include it, as an import's must for the
// label accepted to move a bead, and the contributor's group has none. Their
// runner starts nothing of the yard's, as the daemon's manual one.
func (r *ghDepot) peopleStages(t *testing.T) map[string]string {
	t.Helper()
	ctx := context.Background()
	r.d.Runners["manual"] = &fakeRunner{dead: map[string]bool{}}
	must(t, r.api.PutGroup(ctx, store.Group{Name: "maintainers", Runner: "manual", Limit: 50, Members: []string{"mckean", "yardmaster"}}, "me"))
	must(t, r.api.PutGroup(ctx, store.Group{Name: "decisions", Runner: "manual", Limit: 50, Members: []string{"mckean", "yardmaster"}}, "me"))
	must(t, r.api.PutGroup(ctx, store.Group{Name: "outside", Runner: "manual", Limit: 50}, "me"))
	pr, everyone := "pr", store.OriginatorAny
	groups := map[string]string{"triage": "maintainers", "contributor": "outside", "decide": "decisions"}
	for stage, group := range groups {
		_, err := r.api.AddRoute(ctx, store.Route{Stage: stage, Type: &pr, Originator: &everyone, Group: group}, "me")
		must(t, err)
	}
	return groups
}

// sessionsFor are the sessions the daemon started for bead id, oldest first.
func (r *ghDepot) sessionsFor(id string) []string {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	var sessions []string
	for _, j := range r.f.jobs {
		if j.Bead.ID == id {
			sessions = append(sessions, j.Session.ID)
		}
	}
	return sessions
}

// stoppedSessions are the sessions the daemon stopped.
func (r *ghDepot) stoppedSessions() []string {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	return slices.Clone(r.f.stopped)
}

// startedFor are the groups the daemon started sessions for bead id with.
func (r *ghDepot) startedFor(id string) []string {
	var groups []string
	for _, j := range r.f.jobs {
		if j.Bead.ID == id {
			groups = append(groups, j.Group.Name)
		}
	}
	return groups
}

// threadUntouched fails the test when bead id, which ended in stage, has had
// anything done about its pull request's thread for entering it: a move
// queued and waiting, a move made or given up (a thread_state or a
// thread_state_skipped event), or one the daemon logged as made or tried again.
// The thread command refuses a state asked of a pull request, which the daemon
// reads as the source failing and tries again (TestIssueMoveRefusedForGood), so
// the pack's stage map has no line for a pr bead: a pull request is never
// opened or closed by a stage (docs/links.md, "Forward only"). The pass in
// between is the daemon's chance to make a move that was queued, so what it
// did shows.
func (r *ghDepot) threadUntouched(t *testing.T, id, stage string) {
	t.Helper()
	ctx := context.Background()
	waiting := func() {
		t.Helper()
		moves, err := r.api.IssueMoves(ctx)
		must(t, err)
		for _, m := range moves {
			if m.Bead == id && m.Stage == stage {
				t.Errorf("%s ended in %s and a move of its pull request waits: state %s, %d tries, error %q", id, stage, m.State, m.Attempts, m.Error)
			}
		}
	}
	waiting()
	r.pass(t)
	waiting()
	evs, err := r.api.Events(ctx, id, 200)
	must(t, err)
	for _, e := range evs {
		if (e.Kind == "thread_state" || e.Kind == "thread_state_skipped") && e.Data["stage"] == stage {
			t.Errorf("%s ended in %s and its pull request has a %s event: %v", id, stage, e.Kind, e.Data)
		}
	}
	for _, line := range strings.Split(r.logged.String(), "\n") {
		if strings.Contains(line, "bead="+id) && strings.Contains(line, `msg="issue `) {
			t.Errorf("%s ended in %s and the daemon logged of its pull request: %s", id, stage, line)
		}
	}
}

// The bead of a bot's bump of Go modules: skips triage into review, is
// reported done by the reviewer, which has marked it merge-ok, and is given to
// the yard's CI in ciwait, where no run of GitHub's CI, cancelled or not,
// moves it. The pass of the yard's own sends it to approved, where the label
// has the daemon give it to pr-merges.
func TestContributorsBotBumpFlowsFromOpenedToTheMerge(t *testing.T) {
	t.Parallel()
	r := newContributorsYard(t)
	r.deliverJSON(t, "pull_request", "pull_request_opened", prEvent(17, "dependabot[bot]", "Bot"))
	b := r.madeIn(t, 17, "review")

	r.reviewerSays(t, b.ID, true)
	if st := r.stageOf(t, b.ID); st != "ciwait" {
		t.Fatalf("%s in %s after the reviewer reported done, want ciwait", b.ID, st)
	}
	r.pass(t)
	if got := r.startedFor(b.ID); !slices.Equal(got, []string{"pr-ci"}) {
		t.Fatalf("%s given to %v in ciwait, want pr-ci", b.ID, got)
	}

	// Runs of GitHub's CI say nothing, whatever they conclude; the bead stays
	// for the yard's.
	r.deliverJSON(t, "check_suite", "check_suite_cancelled", ciRun("check_suite", 17, "cancelled"))
	r.deliverJSON(t, "workflow_run", "workflow_run_success", ciRun("workflow_run", 17, "success"))
	r.pass(t)
	if st := r.stageOf(t, b.ID); st != "ciwait" {
		t.Fatalf("%s in %s after runs of GitHub's CI, want ciwait", b.ID, st)
	}

	r.ciSays(t, b.ID, true)
	r.pass(t)
	if got := r.bead(t, b.ID); got.Stage != "approved" || got.Metadata["ci_head"] != fixtureSHA {
		t.Fatalf("%s in %s with ci_head %v after the yard's CI passed, want approved at %s", b.ID, got.Stage, got.Metadata["ci_head"], fixtureSHA)
	}
	if got := r.startedFor(b.ID); !slices.Equal(got, []string{"pr-ci", "pr-merges"}) {
		t.Fatalf("%s given to %v, want pr-ci in ciwait and pr-merges in approved with merge-ok", b.ID, got)
	}
}

// What is not a bot's bump of Go modules waits at approved for a person: a
// pull request without merge-ok falls to the depot's route to decisions, and
// is given to pr-merges only once the yes is said, the label and the sha of
// the head together.
func TestContributorsUnlabeledPullRequestWaitsForTheYes(t *testing.T) {
	t.Parallel()
	r := newContributorsYard(t)
	r.deliverJSON(t, "pull_request", "pull_request_opened", prEvent(18, "dependabot[bot]", "Bot"))
	b := r.madeIn(t, 18, "review")
	r.reviewerSays(t, b.ID, false)
	r.ciSays(t, b.ID, true)
	r.pass(t)

	if got := r.bead(t, b.ID); got.Stage != "approved" {
		t.Fatalf("%s in %s, want approved", b.ID, got.Stage)
	}
	if to := r.routedTo(t, b.ID); to != "decisions" {
		t.Fatalf("%s in approved without merge-ok is routed to %q, want decisions", b.ID, to)
	}
	if started := r.startedFor(b.ID); len(started) != 0 {
		t.Fatalf("a session started for a bead without merge-ok: %v", started)
	}

	// The yes, as the maintainers' one command does it.
	_, err := r.api.UpdateBead(context.Background(), b.ID, store.BeadPatch{AddLabels: []string{"merge-ok"},
		Metadata: map[string]any{"merge_head": fixtureSHA}}, store.Expect{}, "me")
	must(t, err)
	r.pass(t)
	if to := r.routedTo(t, b.ID); to != "pr-merges" {
		t.Fatalf("%s with the yes is routed to %q, want pr-merges", b.ID, to)
	}
	if started := r.startedFor(b.ID); !slices.Equal(started, []string{"pr-merges"}) {
		t.Fatalf("%s given to %v after the yes, want pr-merges", b.ID, started)
	}
}

// The yard's CI sends a pull request back to its contributor when the tests
// failed; a person's push while they wait or run does too, before a pass for
// the head it had can approve it, and a report that comes after the push is
// refused as stale. The runs of GitHub's CI say nothing at any stage, failed or
// not: a failure under review or in the wait moves no bead, and a success
// approves nothing. A bot's push starts the read again instead
// (TestContributorsBotPushStartsTheReadAgain).
func TestContributorsRedCIAndPushesSendItBack(t *testing.T) {
	t.Parallel()
	r := newContributorsYard(t)
	open := func(n int) *store.Bead {
		r.deliverJSON(t, "pull_request", "pull_request_opened", prEvent(n, "dependabot[bot]", "Bot"))
		return r.madeIn(t, n, "review")
	}

	// Tests that failed.
	red := open(21)
	r.reviewerSays(t, red.ID, false)
	r.ciSays(t, red.ID, false)
	if st := r.stageOf(t, red.ID); st != "contributor" {
		t.Fatalf("%s in %s after the yard's tests failed, want contributor", red.ID, st)
	}
	if got := r.bead(t, red.ID); got.Metadata["ci_head"] != nil {
		t.Fatalf("%s has ci_head %v after tests that failed", red.ID, got.Metadata["ci_head"])
	}

	// A person's push while it waits bounces it; the report of the run that was
	// of the head it had is refused, and approves nothing.
	pushed := open(23)
	r.reviewerSays(t, pushed.ID, false)
	r.deliverJSON(t, "pull_request", "pull_request_synchronize", prEvent(23, "dependabot[bot]", "Bot"), pushedBy("a-maintainer", "User"))
	r.pass(t)
	if st := r.stageOf(t, pushed.ID); st != "contributor" {
		t.Fatalf("%s in %s after the pull request changed while it waited on CI, want contributor", pushed.ID, st)
	}
	if note := r.lastNote(t, pushed.ID); !strings.Contains(note, "the PR changed while waiting on CI") {
		t.Fatalf("note of the move: %q", note)
	}
	if _, err := r.api.Done(context.Background(), pushed.ID, "done", "the tests passed", store.Expect{Stage: "ciwait"}, "me"); err == nil {
		t.Fatalf("a report from ciwait on %s was taken after it left for the contributor", pushed.ID)
	}
	if st := r.stageOf(t, pushed.ID); st != "contributor" {
		t.Fatalf("%s in %s after a late report, want contributor", pushed.ID, st)
	}

	// And a person's push while the reviewer still reads.
	changed := open(26)
	r.deliverJSON(t, "pull_request", "pull_request_synchronize", prEvent(26, "dependabot[bot]", "Bot"), pushedBy("a-maintainer", "User"))
	r.pass(t)
	if st := r.stageOf(t, changed.ID); st != "contributor" {
		t.Fatalf("%s in %s after a person pushed under review, want contributor", changed.ID, st)
	}
	if note := r.lastNote(t, changed.ID); !strings.Contains(note, "the PR changed under review") {
		t.Fatalf("note of the move: %q", note)
	}

	// Runs of GitHub's CI move nothing: not a failure under review, not one
	// in the wait, not a success while the reviewer still reads.
	unread, waiting := open(24), open(25)
	r.reviewerSays(t, waiting.ID, false)
	for _, n := range []int{24, 25} {
		r.deliverJSON(t, "workflow_run", "workflow_run_failure", ciRun("workflow_run", n, "failure"))
		r.deliverJSON(t, "check_suite", "check_suite_cancelled", ciRun("check_suite", n, "failure"))
		r.deliverJSON(t, "workflow_run", "workflow_run_success", ciRun("workflow_run", n, "success"))
		r.deliverJSON(t, "check_suite", "check_suite_cancelled", ciRun("check_suite", n, "success"))
	}
	r.pass(t)
	if st := r.stageOf(t, unread.ID); st != "review" {
		t.Fatalf("%s in %s after runs of GitHub's CI while it was read, want review", unread.ID, st)
	}
	if st := r.stageOf(t, waiting.ID); st != "ciwait" {
		t.Fatalf("%s in %s after runs of GitHub's CI while it waited, want ciwait", waiting.ID, st)
	}
}

// A bot's push to a pull request while it is read, or while it waits on CI,
// starts the read again at the new head, where it would otherwise park the
// bead in contributor for a push that a bot, whose rebase is the only one it
// makes, does not send. From the review the session on the old head is
// stopped and a new one starts, as a new stay in the same stage; from the wait
// the session of the yard's CI on the old head is stopped and a report it still
// makes is refused, the bead goes back to review, and once it is read again the
// yard's CI runs on it afresh, and its pass approves it.
func TestContributorsBotPushStartsTheReadAgain(t *testing.T) {
	t.Parallel()
	r := newContributorsYard(t)
	r.reviewedBy(t, "pr-reviewers")
	now := time.Now()
	dispatch.SetClock(r.d, func() time.Time { return now })
	open := func(n int) *store.Bead {
		r.deliverJSON(t, "pull_request", "pull_request_opened", prEvent(n, "dependabot[bot]", "Bot"))
		return r.madeIn(t, n, "review")
	}

	// While the reviewer reads.
	reading := open(41)
	first := r.sessionsFor(reading.ID)
	if len(first) != 1 {
		t.Fatalf("%s read by %v before the push, want one session", reading.ID, first)
	}
	r.deliverJSON(t, "pull_request", "pull_request_synchronize", prEvent(41, "dependabot[bot]", "Bot"), atHead(movedSHA))
	r.pass(t)
	if st := r.stageOf(t, reading.ID); st != "review" {
		t.Fatalf("%s in %s after a bot pushed under review, want review", reading.ID, st)
	}
	if note := r.lastNote(t, reading.ID); !strings.Contains(note, "a bot pushed to the PR") {
		t.Fatalf("note of the move: %q", note)
	}
	// The dispatcher waits before it reads again a bead its flow sent back to
	// the stage it is in, and doubles the wait for each return in a row; the
	// clock passes it.
	now = now.Add(time.Hour)
	r.pass(t)
	if got := r.sessionsFor(reading.ID); len(got) != 2 || got[0] != first[0] {
		t.Fatalf("%s read by %v after a bot pushed under review, want a second session after %v", reading.ID, got, first)
	}
	if !slices.Contains(r.stoppedSessions(), first[0]) {
		t.Fatalf("session %s of the old head still runs: stopped %v", first[0], r.stoppedSessions())
	}

	// While the yard's CI runs: its session on the old head is stopped, the
	// bead goes back to review and is read at the new head, and the yard's CI
	// on it, not a report of the old run, approves it.
	waiting := open(42)
	r.reviewerSays(t, waiting.ID, false)
	r.pass(t)
	run := r.sessionsFor(waiting.ID)
	if got := r.startedFor(waiting.ID); !slices.Equal(got, []string{"pr-reviewers", "pr-ci"}) {
		t.Fatalf("%s given to %v before the push, want the reviewer and then pr-ci", waiting.ID, got)
	}
	r.deliverJSON(t, "pull_request", "pull_request_synchronize", prEvent(42, "dependabot[bot]", "Bot"), atHead(movedSHA))
	r.pass(t)
	if st := r.stageOf(t, waiting.ID); st != "review" {
		t.Fatalf("%s in %s after a bot pushed while the yard's CI ran, want review", waiting.ID, st)
	}
	if note := r.lastNote(t, waiting.ID); !strings.Contains(note, "a bot pushed to the PR") {
		t.Fatalf("note of the move: %q", note)
	}
	if _, err := r.api.Done(context.Background(), waiting.ID, "done", "the tests passed", store.Expect{Stage: "ciwait"}, "me"); err == nil {
		t.Fatalf("a report from ciwait on %s was taken after it went back to review", waiting.ID)
	}
	r.pass(t)
	if got := r.startedFor(waiting.ID); !slices.Equal(got, []string{"pr-reviewers", "pr-ci", "pr-reviewers"}) {
		t.Fatalf("%s given to %v after a bot pushed while it waited, want a second reviewer", waiting.ID, got)
	}
	if !slices.Contains(r.stoppedSessions(), run[1]) {
		t.Fatalf("session %s of the yard's CI on the old head still runs: stopped %v", run[1], r.stoppedSessions())
	}
	r.reviewerSays(t, waiting.ID, false)
	r.pass(t)
	if got := r.startedFor(waiting.ID); !slices.Equal(got, []string{"pr-reviewers", "pr-ci", "pr-reviewers", "pr-ci"}) {
		t.Fatalf("%s given to %v once it was read again, want the yard's CI to run on the new head", waiting.ID, got)
	}
	r.ciSays(t, waiting.ID, true)
	r.pass(t)
	if st := r.stageOf(t, waiting.ID); st != "approved" {
		t.Fatalf("%s in %s after the yard's CI passed on the new head, want approved", waiting.ID, st)
	}
}

// The reviewer's verdict is not the end of the review of a pull request from
// outside: done and changes both take the bead to review-post, where the post
// group is given it, and the script's report sends it on the way the stored
// review says, done to the wait for the yard's CI and changes to its
// contributor, neither before the script has posted. A question is a person's
// and is never posted. A review the script cannot post goes back to the
// reviewer once and the second time to a person, and the flow counts the
// rounds back to review whichever outcome made them, so a bead that a push
// already brought back to be read has used its retry. A bot's push while the
// script runs sends the bead back to be read at the new head, as under review,
// and the run is stopped; a person's push to a pull request no maintainer
// accepted says nothing to the stage: the script's own check of the head is
// what stops a review of another head.
func TestContributorsReviewIsPostedBeforeTheWait(t *testing.T) {
	t.Parallel()
	r := newContributorsYard(t)
	r.reviewedBy(t, "pr-reviewers")
	now := time.Now()
	dispatch.SetClock(r.d, func() time.Time { return now })
	ctx := context.Background()
	open := func(n int) *store.Bead {
		r.deliverJSON(t, "pull_request", "pull_request_opened", prEvent(n, "dependabot[bot]", "Bot"))
		return r.madeIn(t, n, "review")
	}
	reviewerSays := func(id, outcome string) {
		t.Helper()
		_, err := r.api.Done(ctx, id, outcome, "the review", store.Expect{Stage: "review"}, "me")
		must(t, err)
	}
	// givenTo says, after a pass, which groups the daemon has given bead id, in
	// order.
	givenTo := func(id string, want ...string) {
		t.Helper()
		r.pass(t)
		if got := r.startedFor(id); !slices.Equal(got, want) {
			t.Fatalf("%s given to %v, want %v", id, got, want)
		}
	}
	inStage := func(id, want string) {
		t.Helper()
		if st := r.stageOf(t, id); st != want {
			t.Fatalf("%s in %s, want %s", id, st, want)
		}
	}

	// Ready: posted, then the yard's CI runs on the head that was read.
	ready := open(81)
	reviewerSays(ready.ID, "done")
	inStage(ready.ID, "review-post")
	givenTo(ready.ID, "pr-reviewers", "post")
	r.postSays(t, ready.ID, "done")
	inStage(ready.ID, "ciwait")
	givenTo(ready.ID, "pr-reviewers", "post", "pr-ci")

	// Changes asked: posted, then it waits for its contributor, whose new
	// commits are what bring it back; the yard's CI never runs.
	changes := open(82)
	reviewerSays(changes.ID, "changes")
	inStage(changes.ID, "review-post")
	givenTo(changes.ID, "pr-reviewers", "post")
	r.postSays(t, changes.ID, "changes")
	inStage(changes.ID, "contributor")
	givenTo(changes.ID, "pr-reviewers", "post")

	// A question is for a person and nothing is posted.
	asked := open(83)
	reviewerSays(asked.ID, "question")
	inStage(asked.ID, "decide")
	givenTo(asked.ID, "pr-reviewers")

	// A person's push to a pull request no maintainer accepted, while the script
	// runs, moves nothing: no line of the table names it. The script's check of
	// the head fails a review of the old head back to the reviewer.
	racing := open(84)
	reviewerSays(racing.ID, "done")
	givenTo(racing.ID, "pr-reviewers", "post")
	r.deliverJSON(t, "pull_request", "pull_request_synchronize", prEvent(84, "dependabot[bot]", "Bot"), atHead(movedSHA), pushedBy("a-maintainer", "User"))
	r.pass(t)
	inStage(racing.ID, "review-post")
	r.postSays(t, racing.ID, "failed")
	inStage(racing.ID, "review")

	// A bot's push is the source's word, pushed: the bead goes back to be read at
	// the new head, as it does from the read, and the session of the posting is
	// stopped, so that the report it still makes is refused.
	bumped := open(87)
	reviewerSays(bumped.ID, "done")
	r.pass(t)
	given := r.startedFor(bumped.ID)
	if len(given) == 0 || given[len(given)-1] != "post" {
		t.Fatalf("%s given to %v, want the posting last", bumped.ID, given)
	}
	posting := r.sessionsFor(bumped.ID)[len(given)-1]
	r.deliverJSON(t, "pull_request", "pull_request_synchronize", prEvent(87, "dependabot[bot]", "Bot"), atHead(movedSHA), pushedBy("an-app[bot]", "Bot"))
	r.pass(t)
	inStage(bumped.ID, "review")
	if note := r.lastNote(t, bumped.ID); !strings.Contains(note, "a bot pushed to the PR") {
		t.Fatalf("note of the move: %q", note)
	}
	if _, err := r.api.Done(ctx, bumped.ID, "done", "post-review: posted", store.Expect{Stage: "review-post"}, "me"); err == nil {
		t.Fatalf("a report from review-post on %s was taken after it went back to review", bumped.ID)
	}
	r.pass(t)
	if !slices.Contains(r.stoppedSessions(), posting) {
		t.Fatalf("session %s of the posting on the old head still runs: stopped %v", posting, r.stoppedSessions())
	}

	// A review that cannot be posted goes back to the reviewer once, with the
	// reason in a note, and the second time to a person.
	stuck := open(85)
	for _, want := range []string{"review", "decide"} {
		reviewerSays(stuck.ID, "done")
		inStage(stuck.ID, "review-post")
		r.pass(t)
		_, err := r.api.Done(ctx, stuck.ID, "failed", "post-review: the review stored on "+stuck.ID+" is not postable", store.Expect{Stage: "review-post"}, "me")
		must(t, err)
		inStage(stuck.ID, want)
		if note := r.lastNote(t, stuck.ID); !strings.Contains(note, "is not postable") {
			t.Fatalf("note of the move to %s: %q", want, note)
		}
		now = now.Add(time.Hour)
	}

	// A bead that a bot's push already brought back to be read has used the
	// retry: its first failed post goes to a person.
	again := open(86)
	r.deliverJSON(t, "pull_request", "pull_request_synchronize", prEvent(86, "dependabot[bot]", "Bot"), atHead(movedSHA))
	r.pass(t)
	inStage(again.ID, "review")
	reviewerSays(again.ID, "done")
	r.postSays(t, again.ID, "failed")
	inStage(again.ID, "decide")
}

// A pull request merged or closed on GitHub ends its bead wherever the bead
// waits but approved, which had the edges first: in triage, under review,
// while its review is posted, in the wait for the yard's CI, with the
// contributor, in fix and push, and in decide, where a reviewer's question
// leaves it for a person. The pack's two lines name no stage, so a stage
// without an edge for them left the event nowhere to go, and the bead open on
// a pull request that was over. Where the bead is worked its session ends
// with it: the reviewer's is stopped, and so are the posting's, the yard's
// CI, the builder's and the push's, whose report is then refused as one from
// a stage the bead has left, as it is after a push.
func TestContributorsClosedPullRequestEndsItsBead(t *testing.T) {
	t.Parallel()
	r := newContributorsYard(t)
	r.reviewedBy(t, "pr-reviewers")
	ctx := context.Background()
	peopleGroup := r.peopleStages(t)
	pr, everyone := "pr", store.OriginatorAny
	must(t, r.api.PutGroup(ctx, store.Group{Name: "builders", Runner: "fake", Limit: 50}, "me"))
	must(t, r.api.PutGroup(ctx, store.Group{Name: "push-fix", Runner: "fake", Limit: 1}, "me"))
	_, err := r.api.AddRoute(ctx, store.Route{Stage: "fix", Type: &pr, Originator: &everyone, Group: "builders"}, "me")
	must(t, err)
	_, err = r.api.AddRoute(ctx, store.Route{Stage: "push", Type: &pr, Originator: &everyone, Group: "push-fix"}, "me")
	must(t, err)

	// arrive makes the bead of a new pull request and takes it to stage as its
	// events and reports would: a stranger's waits in triage, a bot's goes
	// to the reader, whose verdict sends it on to the posting of its review,
	// then to the wait for CI or back to its contributor, to a fix round by a
	// maintainer's hand, or to a person with a question.
	number := 50
	arrive := func(stage string) *store.Bead {
		number++
		if stage == "triage" {
			r.deliverJSON(t, "pull_request", "pull_request_opened", prEvent(number, "someone", "User"), authorIs("NONE"))
			return r.madeIn(t, number, "triage")
		}
		r.deliverJSON(t, "pull_request", "pull_request_opened", prEvent(number, "dependabot[bot]", "Bot"))
		b := r.madeIn(t, number, "review")
		switch stage {
		case "review-post":
			_, err := r.api.Done(ctx, b.ID, "done", "Ready", store.Expect{Stage: "review"}, "me")
			must(t, err)
		case "ciwait":
			r.reviewerSays(t, b.ID, false)
		case "contributor":
			r.reviewerAsksChanges(t, b.ID)
		case "fix", "push":
			r.reviewerAsksChanges(t, b.ID)
			_, err := r.api.Advance(ctx, b.ID, "fix", store.Expect{}, "me")
			must(t, err)
			if stage == "push" {
				_, err = r.api.Done(ctx, b.ID, "done", "fixed", store.Expect{Stage: "fix"}, "me")
				must(t, err)
			}
		case "decide":
			_, err := r.api.Done(ctx, b.ID, "question", "is this change wanted?", store.Expect{Stage: "review"}, "me")
			must(t, err)
		}
		r.pass(t)
		if st := r.stageOf(t, b.ID); st != stage {
			t.Fatalf("%s in %s, want %s", b.ID, st, stage)
		}
		return b
	}

	// workedIn are the sessions of the yard that worked bead id in stage: the
	// reader's, the posting's, the yard's CI. Nobody of the yard works the
	// stages of people.
	workedIn := func(id, stage string) []string {
		r.f.mu.Lock()
		defer r.f.mu.Unlock()
		var sessions []string
		for _, j := range r.f.jobs {
			if j.Bead.ID == id && j.Session.Stage == stage {
				sessions = append(sessions, j.Session.ID)
			}
		}
		return sessions
	}

	for _, stage := range []string{"triage", "review", "review-post", "ciwait", "contributor", "fix", "push", "decide"} {
		for _, c := range []struct{ fixture, outcome, to string }{
			{"pull_request_closed_merged", "merged", "merged"},
			{"pull_request_closed_unmerged", "dropped", "closed"},
		} {
			b := arrive(stage)
			worked := workedIn(b.ID, stage)
			if _, people := peopleGroup[stage]; people != (len(worked) == 0) {
				t.Fatalf("%s in %s worked by %v", b.ID, stage, worked)
			}

			r.deliverJSON(t, "pull_request", c.fixture, prEvent(number, "someone", "User"))
			r.pass(t)
			got := r.bead(t, b.ID)
			if got.Stage != c.to || got.Status != "closed" {
				t.Errorf("%s: a pull request %s (%s) left its bead in %s (%s), want %s closed", stage, c.outcome, c.fixture, got.Stage, got.Status, c.to)
			}
			wantRules(t, r.rules(t, b.ID), "pull_request made", c.outcome+" "+stage+" -> "+c.to)
			// The bead ended with its pull request, merged or closed on GitHub:
			// no stage of it sets a status there.
			r.threadUntouched(t, b.ID, c.to)

			// Whatever worked the stage is stopped, and neither the posting nor
			// the yard's CI can report for a pull request that is over.
			r.pass(t)
			for _, s := range worked {
				if !slices.Contains(r.stoppedSessions(), s) {
					t.Errorf("%s: session %s of the old stay still runs: stopped %v", stage, s, r.stoppedSessions())
				}
			}
			if stage == "review-post" {
				if _, err := r.api.Done(ctx, b.ID, "done", "post-review: posted", store.Expect{Stage: "review-post"}, "me"); err == nil {
					t.Errorf("a report from review-post on %s was taken after its pull request was %s", b.ID, c.outcome)
				}
			}
			if stage == "ciwait" {
				if _, err := r.api.Done(ctx, b.ID, "done", "the tests passed", store.Expect{Stage: "ciwait"}, "me"); err == nil {
					t.Errorf("a report from ciwait on %s was taken after its pull request was %s", b.ID, c.outcome)
				}
				if got := r.bead(t, b.ID); got.Stage != c.to || got.Metadata["ci_head"] != nil {
					t.Errorf("%s in %s with ci_head %v after a late report, want %s with none", b.ID, got.Stage, got.Metadata["ci_head"], c.to)
				}
			}
			if stage == "fix" {
				if _, err := r.api.Done(ctx, b.ID, "done", "fixed", store.Expect{Stage: "fix"}, "me"); err == nil {
					t.Errorf("a report from fix on %s was taken after its pull request was %s", b.ID, c.outcome)
				}
			}
			if stage == "push" {
				if _, err := r.api.Done(ctx, b.ID, "done", "push-fix: pushed", store.Expect{Stage: "push"}, "me"); err == nil {
					t.Errorf("a report from push on %s was taken after its pull request was %s", b.ID, c.outcome)
				}
			}
		}
	}
}

// New commits on a pull request whose bead waits in fix or push move its
// head and nothing else: neither stage hears pushed or unaccepted, whoever
// pushed and whether or not a maintainer accepted the pull request, and the
// round's pick is checked by the push script against the head the pull
// request has when it runs.
func TestContributorsPushInFixAndPushMovesTheHeadOnly(t *testing.T) {
	t.Parallel()
	r := newContributorsYard(t)
	r.peopleStages(t)
	ctx := context.Background()
	number := 90
	outsider := func(n int, edits ...func(map[string]any)) []func(map[string]any) {
		return append([]func(map[string]any){prEvent(n, "someone", "User"), authorIs("NONE")}, edits...)
	}
	where := func(id string) (stage string, accepted, pr any) {
		r.pass(t)
		b := r.bead(t, id)
		return b.Stage, b.Metadata["accepted_head"], b.Metadata["pr_head"]
	}
	toFix := func(n int) *store.Bead {
		r.deliverJSON(t, "pull_request", "pull_request_opened", outsider(n)...)
		b := r.madeIn(t, n, "triage")
		r.deliverJSON(t, "pull_request", "pull_request_opened", outsider(n, labeledBy("accepted", "a-maintainer", "User"))...)
		r.pass(t)
		if st := r.stageOf(t, b.ID); st != "review" {
			t.Fatalf("%s in %s, want review", b.ID, st)
		}
		r.reviewerAsksChanges(t, b.ID)
		if _, err := r.api.Advance(ctx, b.ID, "fix", store.Expect{}, "me"); err != nil {
			t.Fatal(err)
		}
		return b
	}
	for _, stage := range []string{"fix", "push"} {
		for _, who := range []struct{ login, kind string }{{"someone", "User"}, {"an-app[bot]", "Bot"}} {
			number++
			b := toFix(number)
			if stage == "push" {
				_, err := r.api.Done(ctx, b.ID, "done", "fixed", store.Expect{Stage: "fix"}, "me")
				must(t, err)
			}
			if got := r.stageOf(t, b.ID); got != stage {
				t.Fatalf("%s in %s, want %s", b.ID, got, stage)
			}
			r.deliverJSON(t, "pull_request", "pull_request_synchronize", outsider(number, bearing("accepted"), atHead(movedSHA), pushedBy(who.login, who.kind))...)
			if got, accepted, pr := where(b.ID); got != stage || accepted != fixtureSHA || pr != movedSHA {
				t.Errorf("%s pushed to by a %s in %s: %s in %s with accepted_head %v and pr_head %v, want %s with %s and %s",
					b.ID, who.kind, stage, b.ID, got, accepted, pr, stage, fixtureSHA, movedSHA)
			}
		}
	}
	// The label may be gone before the push: the bead's triage history still
	// says unaccepted everywhere else, but fix hears nothing either way.
	number++
	b := toFix(number)
	r.deliverJSON(t, "pull_request", "pull_request_synchronize", outsider(number, bearing(), atHead(movedSHA))...)
	if got, accepted, pr := where(b.ID); got != "fix" || accepted != fixtureSHA || pr != movedSHA {
		t.Errorf("%s pushed to in fix after its accepted label was removed: %s with accepted_head %v and pr_head %v, want fix with %s and %s",
			b.ID, got, accepted, pr, fixtureSHA, movedSHA)
	}
}

// A maintainer's yes to a pull request from outside is of a commit, and only
// a person's label says it: the label accepted put on it by a user moves its
// bead out of triage and records the head the event named as accepted_head,
// where one put by a bot, or another label, moves nothing. New commits on a
// pull request that bears the label, whoever pushed and wherever the bead
// waits (read, posted, run, or with the contributor), take the bead back to triage
// with the accepted head as it was, where pr_head follows the last event; a
// person's label says yes again, to the head it then has. A pull request no
// maintainer accepted keeps the lines for its author's pushes.
func TestContributorsAcceptanceIsOfACommit(t *testing.T) {
	t.Parallel()
	r := newContributorsYard(t)
	r.peopleStages(t)
	ctx := context.Background()
	number := 70
	// outsider is the edits of an event about a pull request from a stranger.
	outsider := func(n int, edits ...func(map[string]any)) []func(map[string]any) {
		return append([]func(map[string]any){prEvent(n, "someone", "User"), authorIs("NONE")}, edits...)
	}
	// where is the stage bead id is in after the pass, with the heads it carries.
	where := func(id string) (stage string, accepted, pr any) {
		r.pass(t)
		b := r.bead(t, id)
		return b.Stage, b.Metadata["accepted_head"], b.Metadata["pr_head"]
	}
	// accept makes the bead of a new pull request from outside, which waits in
	// triage with no head accepted, and has a person put the label on it at
	// fixtureSHA: it is in review with that head accepted.
	accept := func() (*store.Bead, int) {
		number++
		r.deliverJSON(t, "pull_request", "pull_request_opened", outsider(number)...)
		b := r.madeIn(t, number, "triage")
		if stage, accepted, _ := where(b.ID); accepted != nil {
			t.Fatalf("%s in %s has accepted_head %v before anyone accepted it", b.ID, stage, accepted)
		}
		r.deliverJSON(t, "pull_request", "pull_request_opened", outsider(number, labeledBy("accepted", "a-maintainer", "User"))...)
		if stage, accepted, pr := where(b.ID); stage != "review" || accepted != fixtureSHA || pr != fixtureSHA {
			t.Fatalf("%s in %s with accepted_head %v and pr_head %v after the label, want review with both %s", b.ID, stage, accepted, pr, fixtureSHA)
		}
		return b, number
	}

	// The yes is a person's: a label a bot put, and another label, leave the
	// bead in triage with nothing recorded.
	number++
	r.deliverJSON(t, "pull_request", "pull_request_opened", outsider(number)...)
	waiting := r.madeIn(t, number, "triage")
	r.deliverJSON(t, "pull_request", "pull_request_opened", outsider(number, labeledBy("accepted", "a-labeler[bot]", "Bot"))...)
	r.deliverJSON(t, "pull_request", "pull_request_opened", outsider(number, labeledBy("bug", "a-maintainer", "User"))...)
	if stage, accepted, _ := where(waiting.ID); stage != "triage" || accepted != nil {
		t.Fatalf("%s in %s with accepted_head %v after a bot's label and another label, want triage with none", waiting.ID, stage, accepted)
	}

	// settle takes the bead of an accepted pull request, in review, to stage,
	// by the reviewer's report and what the scripts would report after it. The
	// posting is not reported in review-post: it is run, and the bead waits.
	settle := func(b *store.Bead, stage string) {
		t.Helper()
		switch stage {
		case "review-post":
			_, err := r.api.Done(ctx, b.ID, "done", "Ready", store.Expect{Stage: "review"}, "me")
			must(t, err)
		case "ciwait":
			r.reviewerSays(t, b.ID, false)
		case "contributor":
			r.reviewerAsksChanges(t, b.ID)
		}
	}

	// New commits in each stage the bead can wait in after the yes, from a
	// person and from a bot: back to triage, nothing recorded anew.
	var last *store.Bead
	var lastN int
	for _, stage := range []string{"review", "review-post", "ciwait", "contributor"} {
		for _, who := range []struct{ login, kind string }{{"someone", "User"}, {"an-app[bot]", "Bot"}} {
			b, n := accept()
			settle(b, stage)
			if got := r.stageOf(t, b.ID); got != stage {
				t.Fatalf("%s in %s, want %s", b.ID, got, stage)
			}
			r.deliverJSON(t, "pull_request", "pull_request_synchronize", outsider(n, bearing("accepted"), atHead(movedSHA), pushedBy(who.login, who.kind))...)
			if got, accepted, pr := where(b.ID); got != "triage" || accepted != fixtureSHA || pr != movedSHA {
				t.Errorf("%s pushed to by a %s in %s: %s in %s with accepted_head %v and pr_head %v, want triage with %s and %s",
					b.ID, who.kind, stage, b.ID, got, accepted, pr, fixtureSHA, movedSHA)
			}
			if note := r.lastNote(t, b.ID); !strings.Contains(note, "new commits since a maintainer accepted the PR") {
				t.Errorf("%s pushed to in %s: note of the move: %q", b.ID, stage, note)
			}
			if stage == "ciwait" || stage == "review-post" {
				// The yard's CI or the posting stopped with the move, and a report
				// it still makes is refused: nothing approves the head nobody
				// accepted, and the review of the old head is not posted.
				if _, err := r.api.Done(ctx, b.ID, "done", "the script reported", store.Expect{Stage: stage}, "me"); err == nil {
					t.Errorf("a report from %s on %s was taken after it went back to triage", stage, b.ID)
				}
			}
			last, lastN = b, n
		}
	}

	// The label may be removed before a later push. The bead's triage history
	// still sends the changed head back for a person's acceptance.
	for _, stage := range []string{"review", "review-post", "ciwait", "contributor"} {
		b, n := accept()
		settle(b, stage)
		r.deliverJSON(t, "pull_request", "pull_request_synchronize", outsider(n, bearing(), atHead(movedSHA))...)
		if got, accepted, pr := where(b.ID); got != "triage" || accepted != fixtureSHA || pr != movedSHA {
			t.Errorf("%s pushed to in %s after its accepted label was removed: %s with accepted_head %v and pr_head %v, want triage with %s and %s",
				b.ID, stage, got, accepted, pr, fixtureSHA, movedSHA)
		}
	}

	// Back in triage, the label is still on the pull request: a bot's label
	// leaves it there, and a person's says yes to the head it now has.
	r.deliverJSON(t, "pull_request", "pull_request_synchronize", outsider(lastN, labeledBy("size/M", "a-labeler[bot]", "Bot"), bearing("accepted", "size/M"), atHead(movedSHA))...)
	if stage, accepted, _ := where(last.ID); stage != "triage" || accepted != fixtureSHA {
		t.Fatalf("%s in %s with accepted_head %v after a bot labeled it, want triage with %s", last.ID, stage, accepted, fixtureSHA)
	}
	r.deliverJSON(t, "pull_request", "pull_request_opened", outsider(lastN, labeledBy("accepted", "a-maintainer", "User"), atHead(movedSHA))...)
	if stage, accepted, _ := where(last.ID); stage != "review" || accepted != movedSHA {
		t.Fatalf("%s in %s with accepted_head %v after a person's label, want review with %s", last.ID, stage, accepted, movedSHA)
	}

	// A pull request no maintainer accepted has no label: a trusted author's
	// own pushes keep the lines they had, and nothing is recorded for it.
	number++
	r.deliverJSON(t, "pull_request", "pull_request_opened", prEvent(number, "a-member", "User"), authorIs("MEMBER"))
	member := r.madeIn(t, number, "review")
	for _, c := range []struct{ push, to string }{{"first", "contributor"}, {"second", "review"}} {
		r.deliverJSON(t, "pull_request", "pull_request_synchronize", prEvent(number, "a-member", "User"), authorIs("MEMBER"), atHead(movedSHA))
		if got, accepted, _ := where(member.ID); got != c.to || accepted != nil {
			t.Fatalf("%s after its author's %s push: %s with accepted_head %v, want %s with none", member.ID, c.push, got, accepted, c.to)
		}
	}
	// A matching label alone does not make a trusted author's pull request one
	// that waited in triage, so their push keeps its existing route.
	r.deliverJSON(t, "pull_request", "pull_request_synchronize", prEvent(number, "a-member", "User"), authorIs("MEMBER"), bearing("accepted"), atHead(movedSHA))
	if got, accepted, _ := where(member.ID); got != "contributor" || accepted != nil {
		t.Fatalf("%s after its author's push with the accepted label: %s with accepted_head %v, want contributor with none", member.ID, got, accepted)
	}
}

// A pull request waiting in triage is a card for the maintainers, with the
// pack's own stage file: it names the head the deliveries told, a pick of
// accepted records that head and sends the bead to review, and a push after the
// card was put never lets the old card accept: the bead stays, the old card is
// taken back and a card of the new head is put. A pull request returned to
// triage by a push after a yes comes back with a card of the new head. The label
// still says yes, beside the card, which it then takes back.
func TestContributorsTriageCardIsOfTheHead(t *testing.T) {
	t.Parallel()
	const lastSHA = "5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e"
	home := t.TempDir()
	r := newContributorsYardIn(t, home)
	ctx := context.Background()
	// The pack's own stage file, its ## Ask included, where an import puts it.
	file, err := fs.ReadFile(packs.FS, packs.Contributors+"/flows/pr/triage.md")
	must(t, err)
	stages := store.HomeStageDir(home, "contributors.pr")
	must(t, os.MkdirAll(stages, 0o755))
	must(t, os.WriteFile(filepath.Join(stages, "triage.md"), file, 0o644))
	r.peopleStages(t)
	must(t, r.api.SetSetting(ctx, store.OperatorSetting, "jo", "jo"))
	_, err = r.api.AddUser(ctx, "mckean", "jo")
	must(t, err)
	outsider := func(n int, edits ...func(map[string]any)) []func(map[string]any) {
		return append([]func(map[string]any){prEvent(n, "someone", "User"), authorIs("NONE")}, edits...)
	}
	// cards are the open cards on the bead, oldest first.
	cards := func(id string) []store.ResolvedAsk {
		t.Helper()
		notes, err := r.api.Notes(ctx, id)
		must(t, err)
		var open []store.ResolvedAsk
		for _, a := range store.ResolveAsks(notes, time.Now()) {
			if a.State == store.AskOpen {
				open = append(open, a)
			}
		}
		return open
	}
	// theCard is the one open card of the bead, which shows head and no other.
	theCard := func(id, head string) store.ResolvedAsk {
		t.Helper()
		open := cards(id)
		if len(open) != 1 || open[0].Ask.Head == nil || open[0].Ask.Head.Sha != head || open[0].Ask.Head.Accepts != "accepted" {
			var shown []string
			for _, c := range open {
				if c.Ask.Head != nil {
					shown = append(shown, c.Ask.Head.Sha)
				}
			}
			t.Fatalf("%s has %d open cards, showing the heads %v, want one that accepts %s", id, len(open), shown, head)
		}
		return open[0]
	}
	accepted := func(id string) any { return r.bead(t, id).Metadata["accepted_head"] }

	// Opened by a stranger: the bead waits in triage, and its card names the head
	// and what the delivery said of it, drawn from the event alone.
	number := 80
	r.deliverJSON(t, "pull_request", "pull_request_opened", outsider(number)...)
	b := r.madeIn(t, number, "triage")
	card := theCard(b.ID, fixtureSHA)
	if line := card.Ask.Context[1]; !strings.HasPrefix(line, "Head: 4f2c9e1, opened by someone at ") || !strings.HasSuffix(line, " UTC, +12 -0 in 1 file, as the delivery that told it said.") {
		t.Errorf("the card names the head as %q", line)
	}
	if told, _ := r.bead(t, b.ID).Metadata["pr_head_told"].(map[string]any); told["by"] != "someone" || told["action"] != "opened" || told["sha"] != fixtureSHA {
		t.Errorf("the bead records the delivery as %v", told)
	}
	if accepted(b.ID) != nil {
		t.Fatalf("accepted_head %v before anyone accepted", accepted(b.ID))
	}

	// A person's pick of accepted: the head shown is the one accepted, and the
	// bead is in review.
	must(t, r.api.AddAnswer(ctx, b.ID, store.Answer{Ask: card.Note.Seq, Option: 1}, "mckean"))
	if got := r.stageOf(t, b.ID); got != "review" || accepted(b.ID) != fixtureSHA {
		t.Fatalf("after the pick %s is in %s with accepted_head %v, want review with %s", b.ID, got, accepted(b.ID), fixtureSHA)
	}

	// New commits after the yes bring it back to triage with a card of the new
	// head, though no label was ever put on it; the old yes stays recorded until
	// a person gives another.
	r.deliverJSON(t, "pull_request", "pull_request_synchronize", outsider(number, atHead(movedSHA))...)
	r.pass(t)
	if got := r.stageOf(t, b.ID); got != "triage" || accepted(b.ID) != fixtureSHA {
		t.Fatalf("after the push %s is in %s with accepted_head %v, want triage with the earlier %s", b.ID, got, accepted(b.ID), fixtureSHA)
	}
	card = theCard(b.ID, movedSHA)
	if line := card.Ask.Context[1]; !strings.HasPrefix(line, "Head: c0ffee5, pushed by someone at ") || !strings.HasSuffix(line, " UTC, +120 -30 in 4 files, as the delivery that told it said.") {
		t.Errorf("the card of the pushed head names it as %q", line)
	}

	// A push while the card waits: the bead stays in triage, the card of the head
	// before is taken back and the card of the new head is put. The pick of the
	// old card is refused and accepts nothing.
	r.deliverJSON(t, "pull_request", "pull_request_synchronize", outsider(number, atHead(lastSHA))...)
	r.pass(t)
	if got := r.stageOf(t, b.ID); got != "triage" {
		t.Fatalf("after a push in triage %s is in %s, want triage", b.ID, got)
	}
	fresh := theCard(b.ID, lastSHA)
	if err := r.api.AddAnswer(ctx, b.ID, store.Answer{Ask: card.Note.Seq, Option: 1}, "mckean"); err == nil {
		t.Fatalf("the pick of the card of %s was taken after the pull request moved to %s", movedSHA, lastSHA)
	}
	if got := r.stageOf(t, b.ID); got != "triage" || accepted(b.ID) != fixtureSHA {
		t.Fatalf("after the refused pick %s is in %s with accepted_head %v, want triage with the earlier %s", b.ID, got, accepted(b.ID), fixtureSHA)
	}
	must(t, r.api.AddAnswer(ctx, b.ID, store.Answer{Ask: fresh.Note.Seq, Option: 1}, "mckean"))
	if got := r.stageOf(t, b.ID); got != "review" || accepted(b.ID) != lastSHA {
		t.Fatalf("after the pick of the new card %s is in %s with accepted_head %v, want review with %s", b.ID, got, accepted(b.ID), lastSHA)
	}

	// The label is the other way to the same yes: it records the head its event
	// named, and takes the card back with the move.
	number++
	r.deliverJSON(t, "pull_request", "pull_request_opened", outsider(number)...)
	labeled := r.madeIn(t, number, "triage")
	theCard(labeled.ID, fixtureSHA)
	r.deliverJSON(t, "pull_request", "pull_request_opened", outsider(number, labeledBy("accepted", "a-maintainer", "User"))...)
	r.pass(t)
	if got := r.stageOf(t, labeled.ID); got != "review" || accepted(labeled.ID) != fixtureSHA || len(cards(labeled.ID)) != 0 {
		t.Fatalf("after the label %s is in %s with accepted_head %v and %d cards, want review with %s and none", labeled.ID, got, accepted(labeled.ID), len(cards(labeled.ID)), fixtureSHA)
	}

	// The card's other pick closes the bead and accepts nothing.
	number++
	r.deliverJSON(t, "pull_request", "pull_request_opened", outsider(number)...)
	closing := r.madeIn(t, number, "triage")
	must(t, r.api.AddAnswer(ctx, closing.ID, store.Answer{Ask: theCard(closing.ID, fixtureSHA).Note.Seq, Option: 2}, "mckean"))
	if got := r.bead(t, closing.ID); got.Stage != "closed" || got.Status != "closed" || accepted(closing.ID) != nil {
		t.Fatalf("after the close %s is %s (%s) with accepted_head %v, want closed with none", closing.ID, got.Stage, got.Status, accepted(closing.ID))
	}
	// The pick closes the bead and leaves the pull request open on GitHub, as
	// the stage file says: nothing is asked of its thread.
	r.threadUntouched(t, closing.ID, "closed")
}

// A pull request closed unmerged and then reopened has a bead again. The
// close ended the old one (TestContributorsClosedPullRequestEndsItsBead), so no
// open bead tracks the thread, and the lines that judged the pull request when
// it was opened make the next. The new bead starts clean, with no label, no
// note and none of the heads a merge is decided on, because what was read and
// tested before was of a head the pull request may no longer have. The old bead
// is as the close left it except for its link, which it hands on and keeps as
// one that only relates. The close is still in the log, and a daemon that
// starts again reads it without ending the new bead.
func TestContributorsReopenedPullRequestStartsAFreshBead(t *testing.T) {
	t.Parallel()
	r := newContributorsYard(t)
	ctx := context.Background()
	const url = "https://github.com/acme/yardr/pull/61"
	bot := prEvent(61, "dependabot[bot]", "Bot")

	// A bot's bump, read, passed by the yard's CI and said yes to. It sits in
	// approved with everything a merge is decided on.
	r.deliverJSON(t, "pull_request", "pull_request_opened", bot)
	old := r.madeIn(t, 61, "review")
	r.reviewerSays(t, old.ID, true)
	r.ciSays(t, old.ID, true)
	_, err := r.api.UpdateBead(ctx, old.ID, store.BeadPatch{Metadata: map[string]any{
		"reviewed_head": fixtureSHA, "merge_head": fixtureSHA}}, store.Expect{}, "me")
	must(t, err)
	r.pass(t)

	r.deliverJSON(t, "pull_request", "pull_request_closed_unmerged", bot)
	r.pass(t)
	ended := r.bead(t, old.ID)
	endedNotes, err := r.api.Notes(ctx, old.ID)
	must(t, err)
	if ended.Stage != "closed" || ended.Status != "closed" || !slices.Contains(ended.Labels, "merge-ok") || len(endedNotes) == 0 {
		t.Fatalf("%s after the close: %s (%s) with labels %v and %d notes, want it closed with what it had",
			old.ID, ended.Stage, ended.Status, ended.Labels, len(endedNotes))
	}
	for _, k := range []string{"reviewed_head", "ci_head", "merge_head"} {
		if ended.Metadata[k] != fixtureSHA {
			t.Fatalf("%s after the close has %s %v, want %s", old.ID, k, ended.Metadata[k], fixtureSHA)
		}
	}

	// Reopened, at a head that moved while it was closed.
	r.deliverJSON(t, "pull_request", "pull_request_reopened", bot, atHead(movedSHA))
	r.pass(t)
	fresh := r.made(t, url)
	if fresh.ID == old.ID || fresh.Type != "pr" || fresh.Stage != "review" || fresh.Status != "open" {
		t.Fatalf("tracking %s: %s (%s, %s, %s) beside %s, want a new open pr in review", url, fresh.ID, fresh.Type, fresh.Stage, fresh.Status, old.ID)
	}
	if open, err := r.api.ListBeads(ctx, store.BeadFilter{}); err != nil || len(open) != 1 || open[0].ID != fresh.ID {
		t.Fatalf("open beads %v (%v), want %s alone", open, err, fresh.ID)
	}
	if _, relates := r.tracking(t, url); !slices.Equal(relates, []string{old.ID}) {
		t.Fatalf("beads relating to %s: %v, want the closed %s", url, relates, old.ID)
	}
	freshNotes, err := r.api.Notes(ctx, fresh.ID)
	must(t, err)
	// What it has of the event that made it is the event's seq and the head that
	// event told, with what the delivery said of it (the card of a pull request
	// in triage shows both): the head the reopen gave, none of the old bead's.
	if len(fresh.Labels) != 0 || len(freshNotes) != 0 || !slices.Equal(slices.Sorted(maps.Keys(fresh.Metadata)), []string{"hook_seq", "pr_head", "pr_head_told"}) || fresh.Metadata["pr_head"] != movedSHA {
		t.Fatalf("%s starts with labels %v, metadata %v and %d notes, want none but the event that made it and the head %s it told", fresh.ID, fresh.Labels, fresh.Metadata, len(freshNotes), movedSHA)
	}
	wantRules(t, r.rules(t, fresh.ID), "pull_request made "+fresh.ID)

	// The old bead is as the close left it, and the reopen logged nothing on it.
	after := r.bead(t, old.ID)
	afterNotes, err := r.api.Notes(ctx, old.ID)
	must(t, err)
	if after.Stage != ended.Stage || after.Status != ended.Status || !slices.Equal(after.Labels, ended.Labels) ||
		!reflect.DeepEqual(after.Metadata, ended.Metadata) || !reflect.DeepEqual(afterNotes, endedNotes) {
		t.Fatalf("%s changed when the pull request was reopened: %+v with notes %v, was %+v with notes %v", old.ID, after, afterNotes, ended, endedNotes)
	}
	wantRules(t, r.rules(t, old.ID), "pull_request made "+old.ID, "dropped approved -> closed")

	// A daemon that starts again reads the log from the start, the close
	// among it, and makes and moves nothing.
	again := &dispatch.Dispatcher{Store: r.api, Runners: r.d.Runners, PRHead: r.d.PRHead, CI: r.d.CI, DepotRepo: r.d.DepotRepo}
	must(t, again.Reconcile(ctx))
	if b := r.made(t, url); b.ID != fresh.ID || b.Stage != "review" || b.Status != "open" {
		t.Fatalf("after a restart %s tracks %s: %s (%s), want %s open in review", url, b.ID, b.Stage, b.Status, fresh.ID)
	}
	if open, err := r.api.ListBeads(ctx, store.BeadFilter{}); err != nil || len(open) != 1 {
		t.Fatalf("open beads after a restart: %d (%v), want one", len(open), err)
	}
	t.Run("close and reopen before one pass", testContributorsReopenedPullRequestInOnePassStartsAFreshBead)
}

// A pull request closed and reopened before the pass that reads either has
// a bead again, as when a pass runs between the two events (above): the
// close ends the old bead, and the reopen, later in the log, makes the
// next, which starts clean. Judging the reopen against when the pass ran
// would take it for an event from before the close and make nothing.
func testContributorsReopenedPullRequestInOnePassStartsAFreshBead(t *testing.T) {
	t.Helper()
	r := newContributorsYard(t)
	ctx := context.Background()
	const url = "https://github.com/acme/yardr/pull/62"
	bot := prEvent(62, "dependabot[bot]", "Bot")

	r.deliverJSON(t, "pull_request", "pull_request_opened", bot)
	old := r.madeIn(t, 62, "review")

	// Closed and reopened while the dispatcher is busy or down: one pass
	// reads both.
	r.deliverJSON(t, "pull_request", "pull_request_closed_unmerged", bot)
	r.deliverJSON(t, "pull_request", "pull_request_reopened", bot)
	r.pass(t)

	ended := r.bead(t, old.ID)
	if ended.Stage != "closed" || ended.Status != "closed" {
		t.Fatalf("%s after the close: %s (%s), want it closed", old.ID, ended.Stage, ended.Status)
	}
	fresh := r.made(t, url)
	if fresh.ID == old.ID || fresh.Type != "pr" || fresh.Stage != "review" || fresh.Status != "open" {
		t.Fatalf("tracking %s: %s (%s, %s, %s) beside %s, want a new open pr in review", url, fresh.ID, fresh.Type, fresh.Stage, fresh.Status, old.ID)
	}
	if open, err := r.api.ListBeads(ctx, store.BeadFilter{}); err != nil || len(open) != 1 || open[0].ID != fresh.ID {
		t.Fatalf("open beads %v (%v), want %s alone", open, err, fresh.ID)
	}
	if _, relates := r.tracking(t, url); !slices.Equal(relates, []string{old.ID}) {
		t.Fatalf("beads relating to %s: %v, want the closed %s", url, relates, old.ID)
	}
	freshNotes, err := r.api.Notes(ctx, fresh.ID)
	must(t, err)
	if len(fresh.Labels) != 0 || len(freshNotes) != 0 || !slices.Equal(slices.Sorted(maps.Keys(fresh.Metadata)), []string{"hook_seq", "pr_head", "pr_head_told"}) || fresh.Metadata["pr_head"] != fixtureSHA {
		t.Fatalf("%s starts with labels %v, metadata %v and %d notes, want none but the event that made it and the head %s it told", fresh.ID, fresh.Labels, fresh.Metadata, len(freshNotes), fixtureSHA)
	}
	wantRules(t, r.rules(t, fresh.ID), "pull_request made "+fresh.ID)

	// The reopen logged nothing on the old bead, and the close recorded
	// its seq there, which the reopen was judged against.
	wantRules(t, r.rules(t, old.ID), "pull_request made "+old.ID, "dropped review -> closed")
	if ended.Metadata["close_seq"] == nil {
		t.Fatalf("%s carries no close_seq after its event closed it: %v", old.ID, ended.Metadata)
	}

	// A daemon that starts again reads the log from the start, the close
	// among it, and makes and moves nothing.
	again := &dispatch.Dispatcher{Store: r.api, Runners: r.d.Runners, PRHead: r.d.PRHead, CI: r.d.CI, DepotRepo: r.d.DepotRepo}
	must(t, again.Reconcile(ctx))
	if b := r.made(t, url); b.ID != fresh.ID || b.Stage != "review" || b.Status != "open" {
		t.Fatalf("after a restart %s tracks %s: %s (%s), want %s open in review", url, b.ID, b.Stage, b.Status, fresh.ID)
	}
	if open, err := r.api.ListBeads(ctx, store.BeadFilter{}); err != nil || len(open) != 1 {
		t.Fatalf("open beads after a restart: %d (%v), want one", len(open), err)
	}
}

// Who skips the maintainer, through the normalizing a yard does: the pull
// request of an owner or a member of the repository, as GitHub spells their
// association (OWNER, MEMBER) and the source's normalize lowercases it, and a
// bot's go to review; a human's from outside waits in triage. The pack's
// table is matched with fields typed by hand in packs_test.go, which cannot
// see whether the line is spelled as normalize writes it. Closed and reopened,
// each is judged by the same lines, and sender is whoever reopened it, so a
// person who reopens a bot's pull request leaves it to the maintainer.
func TestContributorsWhoSkipTriage(t *testing.T) {
	t.Parallel()
	r := newContributorsYard(t)
	for _, c := range []struct {
		number                   int
		login, kind, association string
		opened, reopened         string
		reopenedBy               string // a person other than its author who reopens it
	}{
		{31, "someone", "User", "NONE", "triage", "triage", ""},
		{32, "renovate[bot]", "Bot", "", "review", "review", ""},
		{33, "a-member", "User", "MEMBER", "review", "review", ""},
		{34, "the-owner", "User", "OWNER", "review", "review", ""},
		{35, "renovate[bot]", "Bot", "", "review", "triage", "a-maintainer"},
	} {
		author := []func(map[string]any){prEvent(c.number, c.login, c.kind)}
		if c.association != "" {
			author = append(author, authorIs(c.association))
		}
		r.deliverJSON(t, "pull_request", "pull_request_opened", author...)
		first := r.madeIn(t, c.number, c.opened)

		r.deliverJSON(t, "pull_request", "pull_request_closed_unmerged", author...)
		r.pass(t)
		if got := r.bead(t, first.ID); got.Status != "closed" {
			t.Fatalf("pull request %d closed: %s is %s in %s, want it closed", c.number, first.ID, got.Status, got.Stage)
		}
		reopen := author
		if c.reopenedBy != "" {
			reopen = append(slices.Clone(author), pushedBy(c.reopenedBy, "User"))
		}
		r.deliverJSON(t, "pull_request", "pull_request_reopened", reopen...)
		if again := r.madeIn(t, c.number, c.reopened); again.ID == first.ID {
			t.Fatalf("pull request %d reopened: %s is still its bead", c.number, again.ID)
		}
	}
}
