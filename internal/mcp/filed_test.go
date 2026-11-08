package mcp_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"yardr.dev/yardr/internal/mcp"
	"yardr.dev/yardr/internal/store"
)

// withdraw and reword reach a request the person filed themselves and no
// other; withdraw is refused while the request is landing and reword while
// a session holds it. They are driven here through a real store, as
// mcp_test.go drives the other tools.

// filed is a request the person files through the tool, and its work name.
func (y *yard) filed(t *testing.T, s *mcp.Service, request string) string {
	t.Helper()
	res, err := s.AskForSomething(y.ctx, request, "")
	if err != nil {
		t.Fatal(err)
	}
	return res.Structured["work"].(string)
}

// own is a task the person files themselves, at the stage a test needs:
// the request the tools reach, as work is the crew's filing for them.
func (y *yard) own(t *testing.T, nb store.NewBead) string {
	t.Helper()
	nb.Type, nb.For = "task", "ada"
	b, err := y.st.CreateBead(y.ctx, nb, "ada")
	if err != nil {
		t.Fatal(err)
	}
	return b.ID
}

// held puts a session of group on the bead, as the dispatcher's claim does.
func (y *yard) held(t *testing.T, bead, group string) *store.Session {
	t.Helper()
	b, err := y.st.GetBead(y.ctx, bead)
	if err != nil {
		t.Fatal(err)
	}
	if err := y.st.PutGroup(y.ctx, store.Group{Name: group, Runner: "herdr", Limit: 5}, "ada"); err != nil {
		t.Fatal(err)
	}
	sess, err := y.st.Claim(y.ctx, bead, group, b.Stage, "yard")
	if err != nil {
		t.Fatal(err)
	}
	return sess
}

func (y *yard) get(t *testing.T, bead string) *store.Bead {
	t.Helper()
	b, err := y.st.GetBead(y.ctx, bead)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// afterReadYard runs hook once, right after the service reads a piece of
// work: a write that someone else makes between the service's read and its
// own write.
type afterReadYard struct {
	mcp.Yard
	hook func()
}

func (y *afterReadYard) GetBead(ctx context.Context, id string) (*store.Bead, error) {
	b, err := y.Yard.GetBead(ctx, id)
	if hook := y.hook; hook != nil {
		y.hook = nil
		hook()
	}
	return b, err
}

// shown is a piece of work as a failure message reads it.
func shown(b *store.Bead) string {
	raw, _ := json.Marshal(b)
	return string(raw)
}

// refusesOthers holds a tool, called through try with a work name, to the
// rule both tools share: work that is not a request the person filed
// (another person's, the crew's filing for them, a name that matches
// nothing, a prefix that matches several) is refused in one set of words,
// which say nothing of whether such work exists, and is left as it was.
func refusesOthers(t *testing.T, y *yard, try func(work string) error) {
	t.Helper()
	bob := &mcp.Service{Yard: y.st, User: "bob", Now: time.Now}
	for _, id := range []string{"bobs-abcd1", "bobs-abcd2"} {
		if _, err := y.st.CreateBead(y.ctx, store.NewBead{ID: id, Title: "Bob's " + id, Type: "task", Depot: "yardr", For: "bob"}, "bob"); err != nil {
			t.Fatal(err)
		}
	}
	others := map[string]string{
		"another person's":           y.filed(t, bob, "Bob's request"),
		"filed by the crew for them": y.work(t, "Filed for ada by the crew", "yardr", "ada"),
		"a prefix matching several":  "bobs-abcd",
	}
	nothing := try("yardr-nosuch")
	if nothing == nil || !strings.Contains(nothing.Error(), "I can't find that among the requests you filed") {
		t.Fatalf("a name that matches nothing was answered %v", nothing)
	}
	plain(t, nothing.Error())
	for name, work := range others {
		var before *store.Bead
		if b, err := y.st.GetBead(y.ctx, work); err == nil {
			before = b
		}
		if err := try(work); err == nil || err.Error() != nothing.Error() {
			t.Errorf("work %s: %v, want the words for a name that matches nothing: %v", name, err, nothing)
		}
		if before != nil {
			if after := y.get(t, work); after.Revision != before.Revision {
				t.Errorf("work %s was changed: %s\nwas %s", name, shown(after), shown(before))
			}
		}
	}
}

func TestWithdraw(t *testing.T) {
	y := newYard(t)
	y.depot(t, "yardr")
	s := svc(y, "")

	// An open request of the person's own is closed, with their reason as it
	// stands, trimmed, as the close reason, in the work and in its history.
	// No builder was at work on it, so the words stop none.
	const reason = "the figures are in already"
	own := y.filed(t, s, "Write the report\n\nWhat done looks like: a page.")
	res, err := s.Withdraw(y.ctx, own, "  "+reason+"\n")
	if err != nil {
		t.Fatal(err)
	}
	plain(t, res.Text)
	if want := `Withdrawn: "Write the report". It is closed, with your reason kept on it.`; res.Text != want ||
		res.Structured["work"] != own || res.Structured["title"] != "Write the report" {
		t.Errorf("the withdrawal = %q %+v, want %q", res.Text, res.Structured, want)
	}
	closed := y.get(t, own)
	if closed.Status != "closed" || closed.CloseReason == nil || *closed.CloseReason != reason {
		t.Fatalf("the withdrawn request is %s, want it closed with the reason %q", shown(closed), reason)
	}
	evs, err := y.st.Events(y.ctx, own, 20)
	if err != nil {
		t.Fatal(err)
	}
	var closedBy string
	for _, e := range evs {
		if e.Kind == "closed" && e.Data["reason"] == reason {
			closedBy = e.Actor
		}
	}
	if closedBy != "ada" {
		t.Errorf("the history has the close by %q, want ada", closedBy)
	}

	// A request closed already is refused in plain words, and the reason it
	// was closed with stands, which a second close would write over.
	if _, err := s.Withdraw(y.ctx, own, "a second thought"); err == nil || !strings.Contains(err.Error(), "finished or withdrawn already") {
		t.Fatalf("withdrawing a closed request = %v", err)
	} else {
		plain(t, err.Error())
	}
	if again := y.get(t, own); again.CloseReason == nil || *again.CloseReason != reason {
		t.Errorf("the close reason after a second withdrawal is %s", shown(again))
	}

	// Another's request and a name that matches nothing are refused alike.
	refusesOthers(t, y, func(work string) error {
		_, err := s.Withdraw(y.ctx, work, "no longer wanted")
		return err
	})

	// Without the work or the reason nothing is closed.
	open := y.filed(t, s, "Still wanted")
	for _, c := range []struct{ work, reason, want string }{
		{open, "  ", "say why you are withdrawing it"},
		{" ", "no longer wanted", "say which piece of work"},
	} {
		if _, err := s.Withdraw(y.ctx, c.work, c.reason); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Withdraw(%q, %q) = %v, want %q", c.work, c.reason, err, c.want)
		}
	}
	if got := y.get(t, open); got.Status != "open" {
		t.Errorf("a withdrawal without its reason closed the request: %s", shown(got))
	}

	// While it is landing it is refused, in words that say so and send the
	// person to the yardmaster: it waits in the stage the assembly lands
	// from, or the depot's assembly holds it. Nothing changes, and the
	// assembly's session goes on.
	for _, c := range []struct{ name, stage, group string }{
		{"waiting to be landed", "approved", ""},
		{"held by the assembly", "new", "yardr-assembly"},
	} {
		work := y.own(t, store.NewBead{Title: "Landing, " + c.name, Depot: "yardr", Stage: c.stage})
		var sess *store.Session
		if c.group != "" {
			sess = y.held(t, work, c.group)
		}
		before := y.get(t, work)
		_, err := s.Withdraw(y.ctx, work, "changed my mind")
		if err == nil || !strings.Contains(err.Error(), "It is landing") || !strings.Contains(err.Error(), "Write to the yardmaster") {
			t.Errorf("withdrawing a request %s = %v", c.name, err)
			continue
		}
		plain(t, err.Error())
		if after := y.get(t, work); after.Revision != before.Revision || after.Status != "open" {
			t.Errorf("a request %s was changed: %s", c.name, shown(after))
		}
		if sess != nil {
			if live, err := y.st.GetSession(y.ctx, sess.ID); err != nil || live.EndedAt != nil {
				t.Errorf("the assembly's session after the refusal: %+v, %v", live, err)
			}
		}
	}

	// Under a builder's or a reviewer's session it goes through: the close
	// ends the session as any close does, and the words say so.
	for _, c := range []struct{ stage, group string }{{"new", "yardr-builders"}, {"review", "yardr-reviewers"}} {
		work := y.own(t, store.NewBead{Title: "At " + c.stage, Depot: "yardr", Stage: c.stage})
		sess := y.held(t, work, c.group)
		res, err := s.Withdraw(y.ctx, work, "not needed any more")
		if err != nil {
			t.Errorf("withdrawing a request in %s under a session: %v", c.stage, err)
			continue
		}
		plain(t, res.Text)
		if want := `Withdrawn: "At ` + c.stage + `". It is closed, with your reason kept on it. Its builder was stopped.`; res.Text != want {
			t.Errorf("the withdrawal under a session = %q, want %q", res.Text, want)
		}
		if got := y.get(t, work); got.Status != "closed" || got.Session != nil {
			t.Errorf("the request withdrawn under a session is %s", shown(got))
		}
		if ended, err := y.st.GetSession(y.ctx, sess.ID); err != nil || ended.State != "done" || ended.EndedAt == nil {
			t.Errorf("the session after the withdrawal: %+v, %v; want it ended", ended, err)
		}
	}

	// The close carries the standing the request was read at: one the
	// assembly claimed between the read and the close is refused as stale,
	// not closed from under the assembly.
	raced := y.filed(t, s, "Raced by the assembly")
	racing := &mcp.Service{Yard: &afterReadYard{Yard: y.st, hook: func() { y.held(t, raced, "yardr-assembly") }}, User: "ada", Now: time.Now}
	if _, err := racing.Withdraw(y.ctx, raced, "changed my mind"); err == nil || !strings.Contains(err.Error(), "It changed while I was doing that") {
		t.Errorf("withdrawing a request that changed under the call = %v", err)
	} else {
		plain(t, err.Error())
	}
	if got := y.get(t, raced); got.Status != "open" || got.Session == nil {
		t.Errorf("the request after the stale withdrawal is %s", shown(got))
	}
}

func TestReword(t *testing.T) {
	y := newYard(t)
	y.depot(t, "yardr")
	s := svc(y, "")

	// The title and the body are replaced with the words given, trimmed, and
	// nothing else about the request changes. It starts with what a reword
	// must leave alone set to something the filing did not set: a priority,
	// labels and metadata, as the yardmaster sets them.
	own := y.filed(t, s, "Write the report\n\nWhat done looks like: a page.")
	priority := 1
	if _, err := y.st.UpdateBead(y.ctx, own, store.BeadPatch{
		Priority: &priority, AddLabels: []string{"parked", "size:small"}, Metadata: map[string]any{"origin": "chat"},
	}, store.Expect{}, "yardmaster"); err != nil {
		t.Fatal(err)
	}
	before := y.get(t, own)
	res, err := s.Reword(y.ctx, own, " Write the annual report ", "What done looks like: a page and a chart.\n\nNot a slide deck.\n")
	if err != nil {
		t.Fatal(err)
	}
	plain(t, res.Text)
	if want := `Reworded: it is now "Write the annual report", with new details.`; res.Text != want ||
		res.Structured["work"] != own || res.Structured["title"] != "Write the annual report" {
		t.Errorf("the reword = %q %+v, want %q", res.Text, res.Structured, want)
	}
	after := y.get(t, own)
	want := *before
	want.Title, want.Body = "Write the annual report", "What done looks like: a page and a chart.\n\nNot a slide deck."
	want.UpdatedAt, want.Revision = after.UpdatedAt, after.Revision
	if !reflect.DeepEqual(*after, want) {
		t.Errorf("after the reword\n%s\nwant every other field as it was:\n%s", shown(after), shown(&want))
	}

	// A title alone leaves the details as they are, and the details alone
	// leave the title.
	res, err = s.Reword(y.ctx, own, "Write the yearly report", "")
	if err != nil {
		t.Fatal(err)
	}
	if want := `Reworded: it is now "Write the yearly report".`; res.Text != want {
		t.Errorf("the title alone = %q, want %q", res.Text, want)
	}
	if got := y.get(t, own); got.Title != "Write the yearly report" || got.Body != after.Body {
		t.Errorf("after the title alone: %s", shown(got))
	}
	res, err = s.Reword(y.ctx, own, "", "Just a page.")
	if err != nil {
		t.Fatal(err)
	}
	if want := `Reworded: the details of "Write the yearly report" are changed.`; res.Text != want {
		t.Errorf("the details alone = %q, want %q", res.Text, want)
	}
	if got := y.get(t, own); got.Title != "Write the yearly report" || got.Body != "Just a page." {
		t.Errorf("after the details alone: %s", shown(got))
	}

	// The words it already has change nothing and log nothing, and the
	// result says so.
	logged := func() int {
		t.Helper()
		evs, err := y.st.Events(y.ctx, own, 100)
		if err != nil {
			t.Fatal(err)
		}
		return len(evs)
	}
	events := logged()
	res, err = s.Reword(y.ctx, own, "Write the yearly report", "Just a page.")
	if err != nil {
		t.Fatal(err)
	}
	if want := `It already says that, so nothing was changed. It stays "Write the yearly report".`; res.Text != want || logged() != events {
		t.Errorf("rewording to the words it has = %q, %d events (were %d), want %q and no new event", res.Text, logged(), events, want)
	}

	// A blank title and body are words not given, so a client that sends ""
	// for what it leaves out wipes nothing, and a title is one line.
	for _, c := range []struct{ title, body, want string }{
		{"", "", "say the new title, the new details, or both"},
		{"  ", "\n", "say the new title, the new details, or both"},
		{"One line\nand another", "", "title: say it as one line"},
	} {
		if _, err := s.Reword(y.ctx, own, c.title, c.body); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Reword(%q, %q) = %v, want %q", c.title, c.body, err, c.want)
		}
	}
	if _, err := s.Reword(y.ctx, " ", "A title", ""); err == nil || !strings.Contains(err.Error(), "say which piece of work") {
		t.Errorf("rewording no work = %v", err)
	}
	if logged() != events {
		t.Errorf("a refused reword wrote to the history")
	}

	// A long title is cut as filing cuts it.
	long := strings.Repeat("word ", 40) + "end"
	if _, err := s.Reword(y.ctx, own, long, ""); err != nil {
		t.Fatal(err)
	}
	if got := y.get(t, own); !strings.HasSuffix(got.Title, "…") || utf8.RuneCountInString(got.Title) > 161 || !strings.HasPrefix(long, strings.TrimSuffix(got.Title, "…")) {
		t.Errorf("a long title was kept as %q", got.Title)
	}

	// Before a session claims it, at any stage, it is rewordable, and the
	// stage is not moved.
	for _, stage := range []string{"backlog", "siding", "decide", "new"} {
		work := y.own(t, store.NewBead{Title: "At " + stage, Depot: "yardr", Stage: stage})
		if _, err := s.Reword(y.ctx, work, "Reworded at "+stage, ""); err != nil {
			t.Errorf("rewording a request in %s: %v", stage, err)
		} else if got := y.get(t, work); got.Title != "Reworded at "+stage || got.Stage != stage {
			t.Errorf("the request reworded in %s is %s", stage, shown(got))
		}
	}

	// Once a session holds it, the words are not changed under it.
	building := y.own(t, store.NewBead{Title: "Under a builder", Depot: "yardr", Stage: "new"})
	sess := y.held(t, building, "yardr-builders")
	held := y.get(t, building)
	if _, err := s.Reword(y.ctx, building, "A new title", ""); err == nil || !strings.Contains(err.Error(), "Someone is working on it right now") ||
		!strings.Contains(err.Error(), "Write to the yardmaster") {
		t.Errorf("rewording a request a session holds = %v", err)
	} else {
		plain(t, err.Error())
	}
	if got := y.get(t, building); got.Revision != held.Revision || got.Session == nil || *got.Session != sess.ID {
		t.Errorf("the request a session holds after the refusal is %s", shown(got))
	}

	// The write carries the standing the request was read at: one a session
	// claimed between the read and the write is refused as stale, not
	// reworded under the builder that has just begun.
	raced := y.filed(t, s, "Raced by a builder")
	racing := &mcp.Service{Yard: &afterReadYard{Yard: y.st, hook: func() { y.held(t, raced, "yardr-builders") }}, User: "ada", Now: time.Now}
	if _, err := racing.Reword(y.ctx, raced, "Changed under the builder", ""); err == nil || !strings.Contains(err.Error(), "It changed while I was doing that") {
		t.Errorf("rewording a request that changed under the call = %v", err)
	} else {
		plain(t, err.Error())
	}
	if got := y.get(t, raced); got.Title != "Raced by a builder" {
		t.Errorf("the request after the stale reword is %s", shown(got))
	}

	// A request withdrawn or finished is not reworded, and another's is
	// refused as withdraw refuses it.
	gone := y.filed(t, s, "Withdrawn before long")
	if _, err := s.Withdraw(y.ctx, gone, "no longer wanted"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reword(y.ctx, gone, "Back again", ""); err == nil || !strings.Contains(err.Error(), "finished or withdrawn already") {
		t.Errorf("rewording a closed request = %v", err)
	}
	if got := y.get(t, gone); got.Title != "Withdrawn before long" {
		t.Errorf("a closed request was reworded: %s", shown(got))
	}
	refusesOthers(t, y, func(work string) error {
		_, err := s.Reword(y.ctx, work, "A new title", "")
		return err
	})
}
