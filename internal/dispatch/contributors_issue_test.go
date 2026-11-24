package dispatch_test

// The issue flow of the contributors pack, as the dispatcher runs it: the
// pack's flow and its hook's tables, with GitHub's deliveries normalized
// through the github pack as a yard does. A bead is made by an issue's
// event, is accepted into the backlog, and is worked through the starter's
// stages, its diff read a second time for security where it needs it; a
// close on GitHub ends it wherever it waits.

import (
	"context"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/dispatch"
	"yardr.dev/yardr/internal/pack"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
	"yardr.dev/yardr/packs"
)

// loadContributorsPack is the contributors pack as the binary carries it.
func loadContributorsPack(t *testing.T) *pack.Pack {
	t.Helper()
	sub, err := fs.Sub(packs.FS, packs.Contributors)
	must(t, err)
	p, err := pack.Load(sub, packs.Embedded)
	must(t, err)
	return p
}

// followContributorsIssueFlow stores the pack's issue flow under the name an
// import gives it and points the depot's beads of the type issue at it.
func followContributorsIssueFlow(t *testing.T, api store.API, p *pack.Pack, depot string) {
	t.Helper()
	ctx := context.Background()
	for _, f := range p.Flows {
		if f.Name == "issue" {
			_, err := api.PutFlow(ctx, f.StoreFlow("contributors.issue"), "me")
			must(t, err)
		}
	}
	must(t, api.SetFlowPointer(ctx, depot, "issue", "contributors.issue", "me"))
}

// newContributorsIssueYard is a yard whose depot yard, of acme/yardr,
// follows the contributors pack for beads of the type issue: its flow, and
// the hook gh with the pack's tables. The yard has the groups the stages
// are worked by, as an import beside the starter leaves them: builders,
// reviewers and assembly run the fake runner, and backlog and decisions
// are held by hand.
func newContributorsIssueYard(t *testing.T) *ghDepot {
	t.Helper()
	r := newGHDepot(t)
	ctx := context.Background()
	p := loadContributorsPack(t)
	followContributorsIssueFlow(t, r.api, p, "yard")
	h, err := r.api.GetHook(ctx, "gh")
	must(t, err)
	h.Says, h.Makes = p.Sources[0].Says, p.Sources[0].Makes
	_, err = r.api.PutHook(ctx, *h, "me")
	must(t, err)
	r.d.DepotRepo = func(_ context.Context, q dispatch.DepotRepoQuery) (string, error) {
		if q.Depot.Name == "yard" {
			return "acme/yardr", nil
		}
		return "", nil
	}
	return r
}

// issueEvent edits the issues fixtures to issue number by sender, a user
// or a bot.
func issueEvent(number int, login, kind string) func(map[string]any) {
	return func(m map[string]any) {
		who := map[string]any{"login": login, "type": kind}
		m["sender"] = who
		issue := m["issue"].(map[string]any)
		issue["number"], issue["user"] = number, who
		issue["html_url"] = fmt.Sprintf("https://github.com/acme/yardr/issues/%d", number)
		issue["title"] = "Hook listener logs the secret path"
	}
}

// An issue closed on GitHub ends its bead wherever the bead waits past
// triage, which had the edge first: in the backlog, under build, under
// review, under the second review for security and in decide, where a
// question leaves it for a person. The close is the decision, made by
// someone who may close that repository's issues. Where the bead is
// worked its session ends with it: the builder's and the reviewers' are
// stopped. Approved is the assembly's to close, so a close while the bead
// waits there matches no line and is silent, as is the yard's own close
// when a bead is merged, which finds the bead already terminal.
func TestContributorsClosedIssueEndsItsBead(t *testing.T) {
	t.Parallel()
	r := newContributorsIssueYard(t)
	ctx := context.Background()
	// Backlog and decide are stages of people, crossed as the hook's crew
	// member: the members of backlog and of the decisions include it, as
	// an import's must for the label accepted to move a bead. Their
	// runner starts nothing of the yard's, as the daemon's manual one.
	r.d.Runners["manual"] = &fakeRunner{dead: map[string]bool{}}
	must(t, r.api.PutGroup(ctx, store.Group{Name: "backlog", Runner: "manual", Limit: 50, Members: []string{"mckean", "yardmaster"}}, "me"))
	must(t, r.api.PutGroup(ctx, store.Group{Name: "decisions", Runner: "manual", Limit: 50, Members: []string{"mckean", "yardmaster"}}, "me"))
	issue, everyone := "issue", store.OriginatorAny
	peopleGroup := map[string]string{"backlog": "backlog", "decide": "decisions"}
	for stage, group := range peopleGroup {
		_, err := r.api.AddRoute(ctx, store.Route{Stage: stage, Type: &issue, Originator: &everyone, Group: group}, "me")
		must(t, err)
	}
	// The worked stages, given to the yard's groups, which start a
	// session each, as the starter's routes do.
	for stage, group := range map[string]string{"new": "builders", "review": "reviewers", "review-security": "security-reviewers", "approved": "assembly"} {
		must(t, r.api.PutGroup(ctx, store.Group{Name: group, Runner: "fake", Limit: 5}, "me"))
		_, err := r.api.AddRoute(ctx, store.Route{Stage: stage, Type: &issue, Originator: &everyone, Group: group}, "me")
		must(t, err)
	}

	// arrive makes the bead of a new issue and takes it to stage as its
	// events and reports would: opened, labeled accepted into the
	// backlog, taken up, reported on, or asked about.
	number := 100
	arrive := func(stage string) *store.Bead {
		number++
		r.deliverJSON(t, "issues", "issues_opened", issueEvent(number, "jane-doe", "User"))
		r.pass(t)
		b := r.made(t, fmt.Sprintf("https://github.com/acme/yardr/issues/%d", number))
		if b.Type != "issue" || b.Stage != "triage" {
			t.Fatalf("issue %d made %s of type %s in %s, want an issue in triage", number, b.ID, b.Type, b.Stage)
		}
		r.deliverJSON(t, "issues", "issues_labeled", issueEvent(number, "mckean", "User"))
		r.pass(t)
		if st := r.stageOf(t, b.ID); st != "backlog" {
			t.Fatalf("%s in %s after its issue was labeled accepted, want backlog", b.ID, st)
		}
		if stage == "backlog" {
			return b
		}
		_, err := r.api.Advance(ctx, b.ID, "new", store.Expect{}, "yardmaster")
		must(t, err)
		switch stage {
		case "review":
			_, err := r.api.Done(ctx, b.ID, "done", "built", store.Expect{Stage: "new"}, "me")
			must(t, err)
		case "review-security":
			_, err := r.api.Done(ctx, b.ID, "done", "built", store.Expect{Stage: "new"}, "me")
			must(t, err)
			_, err = r.api.Done(ctx, b.ID, "done", "needs the second review", store.Expect{Stage: "review"}, "me")
			must(t, err)
		case "approved":
			_, err := r.api.Done(ctx, b.ID, "done", "built", store.Expect{Stage: "new"}, "me")
			must(t, err)
			_, err = r.api.Done(ctx, b.ID, "done", "needs the second review", store.Expect{Stage: "review"}, "me")
			must(t, err)
			_, err = r.api.Done(ctx, b.ID, "done", "looks good", store.Expect{Stage: "review-security"}, "me")
			must(t, err)
		case "decide":
			_, err := r.api.Done(ctx, b.ID, "question", "is this change wanted?", store.Expect{Stage: "new"}, "me")
			must(t, err)
		}
		r.pass(t)
		if st := r.stageOf(t, b.ID); st != stage {
			t.Fatalf("%s in %s, want %s", b.ID, st, stage)
		}
		return b
	}

	// workedIn are the sessions of the yard that worked bead id in stage:
	// the builder's, the reviewers', the assembly's. Nobody of the yard
	// works the stages of people.
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

	for _, stage := range []string{"backlog", "new", "review", "review-security", "decide"} {
		b := arrive(stage)
		worked := workedIn(b.ID, stage)
		if _, people := peopleGroup[stage]; people != (len(worked) == 0) {
			t.Fatalf("%s in %s worked by %v", b.ID, stage, worked)
		}

		r.deliverJSON(t, "issues", "issues_closed", issueEvent(number, "mckean", "User"))
		r.pass(t)
		got := r.bead(t, b.ID)
		if got.Stage != "closed" || got.Status != "closed" {
			t.Errorf("%s: a closed issue left its bead in %s (%s), want closed", stage, got.Stage, got.Status)
		}
		wantRules(t, r.rules(t, b.ID), "issues made "+b.ID, "accepted triage -> backlog", "dropped "+stage+" -> closed")

		// Whatever worked the stage is stopped with the bead.
		r.pass(t)
		for _, s := range worked {
			if !slices.Contains(r.stoppedSessions(), s) {
				t.Errorf("%s: session %s of the old stay still runs: stopped %v", stage, s, r.stoppedSessions())
			}
		}
	}

	// Approved is the assembly's to close: a close while the bead waits
	// there matches no line, so it moves and logs nothing.
	waiting := arrive("approved")
	beforeApproved := r.rules(t, waiting.ID)
	r.deliverJSON(t, "issues", "issues_closed", issueEvent(number, "mckean", "User"))
	r.pass(t)
	if got := r.bead(t, waiting.ID); got.Stage != "approved" || got.Status != "open" {
		t.Errorf("a close at approved left %s in %s (%s), want approved open", waiting.ID, got.Stage, got.Status)
	}
	if after := r.rules(t, waiting.ID); !slices.Equal(after, beforeApproved) {
		t.Errorf("hook_rule events %q after a close at approved, want %q", after, beforeApproved)
	}

	// The yard's own close when a bead is merged: the closed event finds
	// the bead already terminal and is refused quietly, moving and
	// logging nothing.
	landed := arrive("approved")
	_, err := r.api.Done(ctx, landed.ID, "done", "landed", store.Expect{Stage: "approved"}, "me")
	must(t, err)
	r.pass(t)
	if st := r.stageOf(t, landed.ID); st != "merged" {
		t.Fatalf("%s in %s after its bead was landed, want merged", landed.ID, st)
	}
	before := r.rules(t, landed.ID)
	r.deliverJSON(t, "issues", "issues_closed", issueEvent(number, "mckean", "User"))
	r.pass(t)
	if got := r.bead(t, landed.ID); got.Stage != "merged" || got.Status != "closed" {
		t.Errorf("a close at merged left %s in %s (%s), want merged closed", landed.ID, got.Stage, got.Status)
	}
	if after := r.rules(t, landed.ID); !slices.Equal(after, before) {
		t.Errorf("hook_rule events %q after a close at merged, want %q", after, before)
	}
}

// An issue closed and then reopened has a bead again. The close ended the
// old one (TestContributorsClosedIssueEndsItsBead), so no open bead tracks
// the thread, and the line that judged the issue when it was opened makes
// the next. The new bead starts clean, with no label and no note. The old
// bead is as the close left it except for its link, which it hands on and
// keeps as one that only relates. The close is still in the log, and a
// daemon that starts again reads it without ending the new bead.
func TestContributorsReopenedIssueStartsAFreshBead(t *testing.T) {
	t.Parallel()
	r := newContributorsIssueYard(t)
	ctx := context.Background()
	const number = 61
	const url = "https://github.com/acme/yardr/issues/61"

	r.deliverJSON(t, "issues", "issues_opened", issueEvent(number, "jane-doe", "User"))
	r.pass(t)
	old := r.made(t, url)
	if old.Type != "issue" || old.Stage != "triage" || old.Status != "open" {
		t.Fatalf("tracking %s: %s (%s, %s, %s), want a new open issue in triage", url, old.ID, old.Type, old.Stage, old.Status)
	}
	// What the old bead gathered before the close: a label and a note.
	_, err := r.api.UpdateBead(ctx, old.ID, store.BeadPatch{AddLabels: []string{"bug"}}, store.Expect{}, "me")
	must(t, err)
	must(t, r.api.AddNote(ctx, old.ID, "seen, will look", "me"))

	r.deliverJSON(t, "issues", "issues_closed", issueEvent(number, "mckean", "User"))
	r.pass(t)
	ended := r.bead(t, old.ID)
	endedNotes, err := r.api.Notes(ctx, old.ID)
	must(t, err)
	if ended.Stage != "closed" || ended.Status != "closed" || !slices.Contains(ended.Labels, "bug") || len(endedNotes) == 0 {
		t.Fatalf("%s after the close: %s (%s) with labels %v and %d notes, want it closed with what it had",
			old.ID, ended.Stage, ended.Status, ended.Labels, len(endedNotes))
	}

	// Reopened: a new bead tracks the thread, and it alone is open.
	r.deliverJSON(t, "issues", "issues_reopened", issueEvent(number, "jane-doe", "User"))
	r.pass(t)
	fresh := r.made(t, url)
	if fresh.ID == old.ID || fresh.Type != "issue" || fresh.Stage != "triage" || fresh.Status != "open" {
		t.Fatalf("tracking %s: %s (%s, %s, %s) beside %s, want a new open issue in triage", url, fresh.ID, fresh.Type, fresh.Stage, fresh.Status, old.ID)
	}
	if open, err := r.api.ListBeads(ctx, store.BeadFilter{}); err != nil || len(open) != 1 || open[0].ID != fresh.ID {
		t.Fatalf("open beads %v (%v), want %s alone", open, err, fresh.ID)
	}
	if _, relates := r.tracking(t, url); !slices.Equal(relates, []string{old.ID}) {
		t.Fatalf("beads relating to %s: %v, want the closed %s", url, relates, old.ID)
	}
	freshNotes, err := r.api.Notes(ctx, fresh.ID)
	must(t, err)
	if len(fresh.Labels) != 0 || len(freshNotes) != 0 || !slices.Equal(slices.Sorted(maps.Keys(fresh.Metadata)), []string{"hook_seq"}) {
		t.Fatalf("%s starts with labels %v, metadata %v and %d notes, want none but the event that made it", fresh.ID, fresh.Labels, fresh.Metadata, len(freshNotes))
	}
	wantRules(t, r.rules(t, fresh.ID), "issues made "+fresh.ID)

	// The old bead is as the close left it, and the reopen logged nothing on it.
	after := r.bead(t, old.ID)
	afterNotes, err := r.api.Notes(ctx, old.ID)
	must(t, err)
	if after.Stage != ended.Stage || after.Status != ended.Status || !slices.Equal(after.Labels, ended.Labels) ||
		!reflect.DeepEqual(after.Metadata, ended.Metadata) || !reflect.DeepEqual(afterNotes, endedNotes) {
		t.Fatalf("%s changed when the issue was reopened: %+v with notes %v, was %+v with notes %v", old.ID, after, afterNotes, ended, endedNotes)
	}
	wantRules(t, r.rules(t, old.ID), "issues made "+old.ID, "dropped triage -> closed")

	// A daemon that starts again reads the log from the start, the close
	// among it, and makes and moves nothing.
	again := &dispatch.Dispatcher{Store: r.api, Runners: r.d.Runners, PRHead: r.d.PRHead, CI: r.d.CI, DepotRepo: r.d.DepotRepo}
	must(t, again.Reconcile(ctx))
	if b := r.made(t, url); b.ID != fresh.ID || b.Stage != "triage" || b.Status != "open" {
		t.Fatalf("after a restart %s tracks %s: %s (%s), want %s open in triage", url, b.ID, b.Stage, b.Status, fresh.ID)
	}
	if open, err := r.api.ListBeads(ctx, store.BeadFilter{}); err != nil || len(open) != 1 {
		t.Fatalf("open beads after a restart: %d (%v), want one", len(open), err)
	}
}

// An accepted issue's bead is read for what it does in review and, where
// its diff needs it, a second time for security alone: done goes on to
// review-security, and from there to approved, while done-safe goes past it.
// The store holds done-safe to the depot's .yardr/security_paths, read at
// the tip of the base, by the outcome the stage offers and not by the name of
// the flow, so the pack's flow is held as the starter's is: a diff that
// touches the list is refused, with the way on, and the second review has no
// way past itself. The rules are the store's, so the yard is a store with a
// depot that is a git repository and the pack's flow, and no daemon.
func TestContributorsIssueSecurityReview(t *testing.T) {
	t.Parallel()
	api := storetest.New(t)
	ctx := context.Background()

	// The base lists what always has the second review. Each bead has a
	// branch of its own off the base, with one commit that changes what its
	// row says.
	repo := t.TempDir()
	write := func(dir, file, text string) {
		path := filepath.Join(dir, file)
		must(t, os.MkdirAll(filepath.Dir(path), 0o755))
		must(t, os.WriteFile(path, []byte(text), 0o644))
	}
	gitIn(t, repo, "init", "-q", "-b", "main")
	write(repo, store.SecurityPathsFile, "# what the second review may not be skipped for\ninternal/store/**\npacks/*/*.yaml\n")
	write(repo, "internal/store/auth.go", "package store\n")
	write(repo, "docs/a.md", "a\n")
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "base")
	must(t, api.PutDepot(ctx, store.Depot{Name: "yard", Path: repo, Base: "main"}, "me"))
	followContributorsIssueFlow(t, api, loadContributorsPack(t), "yard")

	const onTheList, offTheList = "internal/store/auth.go", "docs/a.md"
	ids := map[string]string{}
	for i, tc := range []struct {
		name    string
		stage   string   // where the bead waits when it reports
		outcome string   // what it reports
		files   []string // what its branch changes
		want    string   // where it waits afterwards
		refusal []string // what the refusal says, for a report that is refused
	}{
		{"done from review goes on to the second review", "review", "done", []string{offTheList}, "review-security", nil},
		{"done-safe, with a diff off the list, goes to approved", "review", "done-safe", []string{offTheList}, "approved", nil},
		{"done-safe, with a diff on the list, is refused", "review", "done-safe", []string{offTheList, onTheList}, "review", []string{
			"done-safe is refused", onTheList, store.SecurityPathsFile, "report done instead, which sends it to review-security"}},
		{"done from the second review goes to approved", "review-security", "done", []string{onTheList}, "approved", nil},
		{"the second review has no way past itself", "review-security", "done-safe", []string{offTheList}, "review-security", []string{"has no outcome done-safe for stage review-security"}},
		{"changes from the second review goes back to its builder", "review-security", "changes", []string{onTheList}, "new", nil},
		{"question from the second review goes to a person", "review-security", "question", []string{onTheList}, "decide", nil},
	} {
		id := fmt.Sprintf("yard-s%d", i)
		ids[tc.name] = id
		_, err := api.CreateBead(ctx, store.NewBead{ID: id, Title: tc.name, Type: "issue", Depot: "yard", Stage: tc.stage}, "me")
		must(t, err)
		gitIn(t, repo, "checkout", "-q", "-b", store.Branch(id), "main")
		for _, f := range tc.files {
			write(repo, f, "changed by "+id+"\n")
		}
		gitIn(t, repo, "add", "-A")
		gitIn(t, repo, "commit", "-q", "-m", id)
		gitIn(t, repo, "checkout", "-q", "main")

		_, err = api.Done(ctx, id, tc.outcome, "", store.Expect{Stage: tc.stage}, "me")
		switch {
		case len(tc.refusal) == 0:
			must(t, err)
		case err == nil:
			t.Errorf("%s: the report of %s at %s was taken", tc.name, tc.outcome, tc.stage)
		default:
			for _, want := range tc.refusal {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%s: the refusal lacks %q: %v", tc.name, want, err)
				}
			}
		}
		b, err := api.GetBead(ctx, id)
		must(t, err)
		if b.Stage != tc.want {
			t.Errorf("%s: %s waits in %s, want %s", tc.name, id, b.Stage, tc.want)
		}
	}

	// Done is always there to report instead: the refused bead goes on to the
	// second review.
	refused := ids["done-safe, with a diff on the list, is refused"]
	if b, err := api.Done(ctx, refused, "done", "", store.Expect{Stage: "review"}, "me"); err != nil || b.Stage != "review-security" {
		t.Fatalf("done after the refusal: %+v, %v", b, err)
	}
}
