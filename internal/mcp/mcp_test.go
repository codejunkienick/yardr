package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/mcp"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
	"yardr.dev/yardr/internal/yardkey"
)

// The tests drive the tools through a real store (a throwaway one, as
// the depot's fixtures give), at the cheapest level that reaches the
// behaviour: a Service call, not a process. The wire shapes (both
// protocol eras, the typed-back number over the wire) are server_test.go's.

type yard struct {
	st    store.API
	owned *store.Store
	ctx   context.Context
}

type mailHookYard struct {
	mcp.Yard
	afterMail func(context.Context, string, string, string) error
}

func (y mailHookYard) Mail(ctx context.Context, to, text, actor string, files ...store.BlobFile) error {
	if err := y.Yard.Mail(ctx, to, text, actor, files...); err != nil {
		return err
	}
	if y.afterMail != nil {
		return y.afterMail(ctx, to, text, actor)
	}
	return nil
}

type readErrorYard struct {
	mcp.Yard
	settingsErr error
	crewErr     error
	blockersErr error
	sessionsErr error
}

func (y readErrorYard) ListSessions(ctx context.Context, f store.SessionFilter) ([]store.Session, error) {
	if y.sessionsErr != nil {
		return nil, y.sessionsErr
	}
	return y.Yard.ListSessions(ctx, f)
}

func (y readErrorYard) Settings(ctx context.Context) (map[string]string, error) {
	if y.settingsErr != nil {
		return nil, y.settingsErr
	}
	return y.Yard.Settings(ctx)
}

func (y readErrorYard) Crew(ctx context.Context) ([]store.CrewMember, error) {
	if y.crewErr != nil {
		return nil, y.crewErr
	}
	return y.Yard.Crew(ctx)
}

func (y readErrorYard) OpenBlockers(ctx context.Context, id string) ([]string, error) {
	if y.blockersErr != nil {
		return nil, y.blockersErr
	}
	return y.Yard.OpenBlockers(ctx, id)
}

func newYard(t *testing.T) *yard {
	t.Helper()
	api, owned := storetest.NewOwned(t)
	y := &yard{st: api, owned: owned, ctx: context.Background()}
	if _, err := y.st.AddUser(y.ctx, "ada", "ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.st.AddUser(y.ctx, "bob", "ada"); err != nil {
		t.Fatal(err)
	}
	if err := y.st.SetSetting(y.ctx, store.OperatorSetting, "ada", "ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.st.PutCrew(y.ctx, store.CrewMember{Name: "yardmaster", Runner: "herdr"}, "ada"); err != nil {
		t.Fatal(err)
	}
	return y
}

func (y *yard) depot(t *testing.T, name string) {
	t.Helper()
	if err := y.st.PutDepot(y.ctx, store.Depot{Name: name, Kind: store.DepotDir, Path: t.TempDir()}, "ada"); err != nil {
		t.Fatal(err)
	}
}

func (y *yard) work(t *testing.T, title, depot, owner string) string {
	t.Helper()
	return y.bead(t, store.NewBead{Title: title, Depot: depot, For: owner})
}

// bead files a task as the yardmaster does, for the tests that need what
// work leaves out: a stage, a label, a blocker, no project.
func (y *yard) bead(t *testing.T, nb store.NewBead) string {
	t.Helper()
	nb.Type = "task"
	b, err := y.st.CreateBead(y.ctx, nb, "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	return b.ID
}

func (y *yard) ask(t *testing.T, bead string, ask store.Ask) uint64 {
	t.Helper()
	seq, err := y.st.AddAsk(y.ctx, bead, ask, "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	return seq
}

// linkNorth links the peer north, receiving mail and exposing ada, and
// answers the key the intake is signed with.
func (y *yard) linkNorth(t *testing.T) string {
	t.Helper()
	k, _, err := yardkey.Ensure(filepath.Join(t.TempDir(), "north.key"))
	if err != nil {
		t.Fatal(err)
	}
	key := store.Public(k)
	if _, err := y.st.AddPeer(y.ctx, store.Peer{Name: "north", Key: key, URL: "http://north.test"}, "ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.st.AllowPeer(y.ctx, "north", store.PeerAllow{Receive: []string{"mail"}, Expose: []string{"ada"}}, "ada"); err != nil {
		t.Fatal(err)
	}
	return key
}

// takePeerMail takes one mail from the peer, as the receiver takes it.
func (y *yard) takePeerMail(t *testing.T, key, id, to, from, text string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"to": to, "from": from, "text": text})
	got, err := y.owned.PeerReceive(y.ctx, "north", key, []store.PeerMessage{{ID: id, Kind: store.PeerMail, Body: body}})
	if err != nil || got[0].Refused != "" {
		t.Fatalf("PeerReceive = %+v, %v", got, err)
	}
}

// sessionPane is the pid of the pane the session-mail tests make up: no
// process has it, so none of the test's own calls is taken for the
// session's.
const sessionPane = 1 << 30

// sessionMail is mail to to from the agent working yd-1, written from its
// pane, so the mail names its bead.
func (y *yard) sessionMail(t *testing.T, to, text string) {
	t.Helper()
	if err := y.st.PutGroup(y.ctx, store.Group{Name: "g", Runner: "fake"}, "ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.st.CreateBead(y.ctx, store.NewBead{ID: "yd-1", Title: "the shed", Stage: "new"}, "ada"); err != nil {
		t.Fatal(err)
	}
	sess, err := y.st.Claim(y.ctx, "yd-1", "g", "new", "yard")
	if err != nil {
		t.Fatal(err)
	}
	if err := y.st.MarkRunning(y.ctx, sess.ID, map[string]any{"agent": "yd-1-new", "shell_pid": sessionPane}); err != nil {
		t.Fatal(err)
	}
	if err := y.owned.Mail(store.WithCaller(y.ctx, sessionPane), to, text, "yd-1-new"); err != nil {
		t.Fatal(err)
	}
}

func svc(y *yard, connection string) *mcp.Service {
	return &mcp.Service{Yard: y.st, User: "ada", Connection: connection, Now: time.Now}
}

// plain holds a result to the plain-words bar: no yard noun unexplained.
// The one allowed shape is the terminal command a refusal names, which
// is the person's own to type.
func plain(t *testing.T, text string) {
	t.Helper()
	lower := strings.ToLower(strings.ReplaceAll(text, "yardr bead answer", ""))
	for _, word := range []string{"bead", "depot", "flow", "stage", "seq", "label"} {
		if strings.Contains(lower, word) {
			t.Errorf("the result uses the yard word %q:\n%s", word, text)
		}
	}
}

// whenWords is a time as the account says it ("Sun 8 Nov at 12:30"), so
// a test can compare the words around it exactly, without the clock.
var whenWords = regexp.MustCompile(`(Mon|Tue|Wed|Thu|Fri|Sat|Sun) \d{1,2} [A-Z][a-z]{2}( \d{4})? at \d{2}:\d{2}`)

// accountSection reads one section of the account (its heading, a pattern,
// without the colon): the project heads in the order they come, and the
// lines under each, without their dash. A section the account lacks reads
// as no heads at all.
func accountSection(text, heading string) (heads []string, lines map[string][]string) {
	loc := regexp.MustCompile(`\n` + heading + `:\n`).FindStringIndex(text)
	if loc == nil {
		return nil, nil
	}
	body, _, _ := strings.Cut(text[loc[1]:], "\n\n")
	lines = map[string][]string{}
	head := ""
	for _, l := range strings.Split(body, "\n") {
		if line, ok := strings.CutPrefix(l, "- "); ok {
			lines[head] = append(lines[head], line)
			continue
		}
		head = strings.TrimSuffix(l, ":")
		heads = append(heads, head)
	}
	return heads, lines
}

func TestNoUserRefusesEveryTool(t *testing.T) {
	y := newYard(t)
	s := &mcp.Service{Yard: y.st, User: ""}
	calls := []func() error{
		func() error { _, err := s.WhatIsHappening(y.ctx, "", ""); return err },
		func() error { _, err := s.AskForSomething(y.ctx, "a thing", ""); return err },
		func() error { _, err := s.WhatWaitsForMe(y.ctx); return err },
		func() error {
			_, err := s.Answer(y.ctx, mcp.AnswerInput{Work: "x", Question: 1, Pick: 1}, false)
			return err
		},
		func() error { _, err := s.AskTheYardmaster(y.ctx, "hi", true, nil); return err },
		func() error { _, err := s.ReadMyMail(y.ctx, 0); return err },
		func() error { _, err := s.ReplyToMail(y.ctx, 1, "thanks"); return err },
		func() error { _, err := s.Reword(y.ctx, "x", "a new title", ""); return err },
		func() error { _, err := s.Withdraw(y.ctx, "x", "no longer wanted"); return err },
	}
	for i, call := range calls {
		if err := call(); err == nil || err.Error() != mcp.NoUserMessage {
			t.Errorf("call %d = %v, want the one no-user message", i, err)
		}
	}
}

func TestAskForSomethingDepotResolution(t *testing.T) {
	y := newYard(t)
	s := svc(y, "")
	// No project at all: refuse, listing none, and file nothing.
	if _, err := s.AskForSomething(y.ctx, "Write the report", ""); err == nil ||
		!strings.Contains(err.Error(), "Your projects are: none yet") {
		t.Fatalf("no projects: %v", err)
	}
	y.depot(t, "yardr")
	// The one project needs no name, and takes the bead.
	res, err := s.AskForSomething(y.ctx, "Write the report\n\nWhat done looks like: a page the team reads.\n\nNot a slide deck.", "")
	if err != nil {
		t.Fatal(err)
	}
	plain(t, res.Text)
	if res.Structured["project"] != "yardr" || res.Structured["title"] != "Write the report" {
		t.Fatalf("filed as %+v", res.Structured)
	}
	// The first line is the title and the words after it, trimmed, are the
	// body, which does not say the title again. The work is filed at the
	// priority a bead made without one has (yardr bead create's), not P0,
	// which a route takes before it reads a label.
	filed, err := y.st.GetBead(y.ctx, res.Structured["work"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if want := "What done looks like: a page the team reads.\n\nNot a slide deck."; filed.Body != want {
		t.Fatalf("the filed work's body = %q, want %q", filed.Body, want)
	}
	if filed.Priority != store.DefaultPriority {
		t.Fatalf("the filed work is P%d, want P%d", filed.Priority, store.DefaultPriority)
	}
	// Cutting the stored title does not move the original first line into
	// the body.
	long := strings.Repeat("word ", 40) + "end"
	for _, tc := range []struct{ request, body string }{
		{request: long},
		{request: long + "\nand what done looks like", body: "and what done looks like"},
	} {
		res, err := s.AskForSomething(y.ctx, tc.request, "")
		if err != nil {
			t.Fatal(err)
		}
		filed, err := y.st.GetBead(y.ctx, res.Structured["work"].(string))
		if err != nil {
			t.Fatal(err)
		}
		cut, wasCut := strings.CutSuffix(filed.Title, "…")
		if !wasCut || !strings.HasPrefix(tc.request, cut) || filed.Body != tc.body {
			t.Fatalf("filed %q as %q with the body %q, want its first line cut short for the title and body %q", tc.request, filed.Title, filed.Body, tc.body)
		}
	}
	// A name that is not the one project refuses the same way.
	if _, err := s.AskForSomething(y.ctx, "Other", "shop"); err == nil ||
		!strings.Contains(err.Error(), "Your projects are: yardr") {
		t.Fatalf("wrong project: %v", err)
	}
	y.depot(t, "shop")
	// Several projects: missing names none, a named one files there.
	// (The names are listed as the yard lists its projects.)
	if _, err := s.AskForSomething(y.ctx, "Other", ""); err == nil ||
		!strings.Contains(err.Error(), "Your projects are:") ||
		!strings.Contains(err.Error(), "yardr") || !strings.Contains(err.Error(), "shop") {
		t.Fatalf("several, unnamed: %v", err)
	}
	res, err = s.AskForSomething(y.ctx, "Stock the shelves", "Shop")
	if err != nil {
		t.Fatal(err)
	}
	if res.Structured["project"] != "shop" {
		t.Fatalf("filed as %+v, want the named project", res.Structured)
	}
	bead, err := y.st.GetBead(y.ctx, res.Structured["work"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if bead.Originator != "ada" || bead.Type != "task" {
		t.Fatalf("the filed work = %+v, want ada's task", bead)
	}
}

func TestWhatIsHappening(t *testing.T) {
	y := newYard(t)
	y.depot(t, "yardr")
	y.depot(t, "shop")
	s := svc(y, "")
	mine := y.work(t, "Write the report", "yardr", "ada")
	y.work(t, "Bob's thing", "yardr", "bob")
	done := y.work(t, "Old report", "yardr", "ada")
	if _, err := y.st.CloseBead(y.ctx, done, "done", store.Expect{}, "ada"); err != nil {
		t.Fatal(err)
	}
	held := y.work(t, "Stuck thing", "yardr", "ada")
	if _, err := y.st.SetHold(y.ctx, held, true, store.Expect{}, "ada"); err != nil {
		t.Fatal(err)
	}
	// The other states are reached by what the account reads: an open
	// blocker, the parked label, a stage. The first that fits wins, so a
	// held piece that is also parked is stuck, a blocked piece in the
	// backlog is stuck, and a parked piece in the decide stage is parked.
	// The last piece has no project.
	y.bead(t, store.NewBead{Title: "Wait for the figures", Depot: "yardr", Stage: "backlog", BlockedBy: []string{mine}, For: "ada"})
	heldParked := y.bead(t, store.NewBead{Title: "Held and parked", Depot: "shop", Stage: "backlog", Labels: []string{"parked"}, For: "ada"})
	if _, err := y.st.SetHold(y.ctx, heldParked, true, store.Expect{}, "ada"); err != nil {
		t.Fatal(err)
	}
	y.bead(t, store.NewBead{Title: "Set the logo aside", Depot: "shop", Stage: "backlog", Labels: []string{"parked"}, For: "ada"})
	y.bead(t, store.NewBead{Title: "Parked decision", Depot: "yardr", Stage: "decide", Labels: []string{"parked"}, For: "ada"})
	y.bead(t, store.NewBead{Title: "Pick the colours", Depot: "yardr", Stage: "decide", For: "ada"})
	y.bead(t, store.NewBead{Title: "Tidy the footer", Depot: "yardr", Stage: "siding", For: "ada"})
	// Two more stages belong to people: ready, where small work folded into
	// one waits to be sent to build, and triage, where a report or a change
	// from outside waits to be accepted.
	y.bead(t, store.NewBead{Title: "Three small fixes, folded", Depot: "yardr", Stage: "ready", For: "ada"})
	y.bead(t, store.NewBead{Title: "A stranger's report", Depot: "yardr", Stage: "triage", For: "ada"})
	y.bead(t, store.NewBead{Title: "Plan the stall", Depot: "shop", Stage: "backlog", For: "ada"})
	y.bead(t, store.NewBead{Title: "A thought with no project", Stage: "backlog", For: "ada"})
	res, err := s.WhatIsHappening(y.ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	plain(t, res.Text)
	for _, want := range []string{"Write the report", "Old report", "Stuck thing", "on hold"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("the account lacks %q:\n%s", want, res.Text)
		}
	}
	if strings.Contains(res.Text, "Bob's thing") {
		t.Errorf("another person's work is in the account:\n%s", res.Text)
	}
	// Each piece is told in its own state, under that state's heading and
	// its project's head, with the words that say why or when. The item and
	// the line carry the same words, so a client that shows only the
	// structured part loses nothing. The cases are in the order of the
	// states, which is the order of the headings and of the list.
	states := []struct{ state, heading string }{
		{"finished", "Finished lately"},
		{"stuck", "Stuck"},
		{"parked", "Parked"},
		{"waiting", "Waiting"},
		{"not started", "Not started"},
		{"under way", "Under way"},
	}
	cases := []struct{ title, state, project, detail string }{
		{"Old report", "finished", "yardr", "finished <when>"},
		{"Stuck thing", "stuck", "yardr", "it is on hold, waiting for a person"},
		{"Wait for the figures", "stuck", "yardr", "it is waiting for other work to finish first"},
		{"Held and parked", "stuck", "shop", "it is on hold, waiting for a person"},
		{"Set the logo aside", "parked", "shop", "last touched <when>"},
		{"Parked decision", "parked", "yardr", "last touched <when>"},
		{"Pick the colours", "waiting", "yardr", "it is waiting for a person to decide"},
		{"Tidy the footer", "waiting", "yardr", "it is waiting to be combined with other small work"},
		{"Three small fixes, folded", "waiting", "yardr", "it is waiting for a person to send it to build"},
		{"A stranger's report", "waiting", "yardr", "it is waiting for a person to accept it"},
		{"Plan the stall", "not started", "shop", "last touched <when>"},
		{"A thought with no project", "not started", "", "last touched <when>"},
		{"Write the report", "under way", "yardr", "last touched <when>"},
	}
	headings := map[string]string{}
	// What changed comes after the counts and before the first state.
	changed := "Changed since " + whenWords.String()
	last := -1
	if loc := regexp.MustCompile(`\n` + changed + `:\n`).FindStringIndex(res.Text); loc == nil {
		t.Errorf("the account lacks what changed:\n%s", res.Text)
	} else {
		last = loc[0]
	}
	for _, st := range states {
		headings[st.state] = st.heading
		at := strings.Index(res.Text, "\n"+st.heading+":\n")
		if at <= last {
			t.Errorf("the heading %q is missing or out of order:\n%s", st.heading, res.Text)
		}
		last = at
	}
	list, _ := res.Structured["work_list"].([]map[string]any)
	if len(list) != len(cases) {
		t.Fatalf("work_list has %d pieces, want %d: %+v", len(list), len(cases), list)
	}
	byTitle := map[string]map[string]any{}
	for i, item := range list {
		byTitle[item["title"].(string)] = item
		if item["state"] != cases[i].state {
			t.Errorf("work_list[%d] is %v, want a piece that is %s", i, item["state"], cases[i].state)
		}
	}
	for _, c := range cases {
		item := byTitle[c.title]
		if item == nil {
			t.Errorf("the account lacks %q:\n%s", c.title, res.Text)
			continue
		}
		var project any // absent, not empty, for a piece with no project
		if c.project != "" {
			project = c.project
		}
		detail, _ := item["detail"].(string)
		if item["state"] != c.state || item["project"] != project || whenWords.ReplaceAllString(detail, "<when>") != c.detail {
			t.Errorf("%q = %+v, want state %q, project %q, detail %q", c.title, item, c.state, c.project, c.detail)
		}
		head := "With no project"
		if c.project != "" {
			head = "In " + c.project
		}
		_, lines := accountSection(res.Text, headings[c.state])
		if !slices.Contains(lines[head], c.title+" ("+detail+")") {
			t.Errorf("under %q, %q the account lacks the line %q:\n%s", headings[c.state], head, c.title+" ("+detail+")", res.Text)
		}
	}
	// The account opens with the counts, by state and by project in each:
	// the project with most pieces first, ties by name, and the pieces with
	// no project last. The sections go in the same order, and the structured
	// part has the counts, and their words too.
	counts := "Your work: 1 finished (1 in yardr), 3 stuck (2 in yardr, 1 in shop), 2 parked (1 in shop, 1 in yardr), " +
		"4 waiting (4 in yardr), 2 not started (1 in shop, 1 with no project), 1 under way (1 in yardr)."
	if first, _, _ := strings.Cut(res.Text, "\n"); first != counts {
		t.Errorf("the account opens with\n%s\nwant\n%s", first, counts)
	}
	wantStates := []map[string]any{
		{"state": "finished", "count": 1, "projects": []map[string]any{{"project": "yardr", "count": 1}}},
		{"state": "stuck", "count": 3, "projects": []map[string]any{{"project": "yardr", "count": 2}, {"project": "shop", "count": 1}}},
		{"state": "parked", "count": 2, "projects": []map[string]any{{"project": "shop", "count": 1}, {"project": "yardr", "count": 1}}},
		{"state": "waiting", "count": 4, "projects": []map[string]any{{"project": "yardr", "count": 4}}},
		{"state": "not started", "count": 2, "projects": []map[string]any{{"project": "shop", "count": 1}, {"count": 1}}},
		{"state": "under way", "count": 1, "projects": []map[string]any{{"project": "yardr", "count": 1}}},
	}
	if summary, _ := res.Structured["summary"].(map[string]any); summary["text"] != counts || !reflect.DeepEqual(summary["states"], wantStates) {
		t.Errorf("the summary is %+v, want the counts %q and the states %+v", summary, counts, wantStates)
	}
	for heading, want := range map[string][]string{
		"Stuck":       {"In yardr", "In shop"},
		"Parked":      {"In shop", "In yardr"},
		"Not started": {"In shop", "With no project"},
	} {
		if heads, _ := accountSection(res.Text, heading); !slices.Equal(heads, want) {
			t.Errorf("under %q the heads are %v, want %v", heading, heads, want)
		}
	}
	// The window is the last day when none is asked for, and every piece
	// here was just touched, so every piece changed inside it: the changed
	// section lists them all, whatever their state, and each item says when.
	// Its lines give the state in the brackets, since no state's heading
	// stands over them.
	heads, lines := accountSection(res.Text, changed)
	if !slices.Equal(heads, []string{"In yardr", "In shop", "With no project"}) ||
		len(lines["In yardr"]) != 9 || len(lines["In shop"]) != 3 || len(lines["With no project"]) != 1 {
		t.Errorf("the changed section is %v %v, want all %d pieces under their projects:\n%s", heads, lines, len(cases), res.Text)
	}
	if got := whenWords.ReplaceAllString(strings.Join(lines["In shop"], "\n"), "<when>"); got !=
		"Held and parked (stuck, changed <when>)\nSet the logo aside (parked, changed <when>)\nPlan the stall (not started, changed <when>)" {
		t.Errorf("the changed lines under In shop are\n%s", got)
	}
	for _, item := range list {
		if item["changed"] != item["updated"] {
			t.Errorf("%v changed %v, want it at its update, %v", item["title"], item["changed"], item["updated"])
		}
	}
	// A window that opens after most of the work was last touched lists
	// only what changed in it. The note below is the one write after it:
	// that piece is changed, the rest are not, and all are still told in
	// their states.
	opens := time.Now()
	if err := y.st.AddNote(y.ctx, mine, "the figures arrived", "bob"); err != nil {
		t.Fatal(err)
	}
	res, err = s.WhatIsHappening(y.ctx, "", opens.UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	plain(t, res.Text)
	heads, lines = accountSection(res.Text, changed)
	if !slices.Equal(heads, []string{"In yardr"}) || len(lines["In yardr"]) != 1 ||
		whenWords.ReplaceAllString(lines["In yardr"][0], "<when>") != "Write the report (under way, changed <when>)" {
		t.Errorf("the changed section is %v %v, want the one piece the note touched:\n%s", heads, lines, res.Text)
	}
	for _, item := range res.Structured["work_list"].([]map[string]any) {
		if _, isChanged := item["changed"]; isChanged != (item["work"] == mine) {
			t.Errorf("%v: changed is %v, want it on %q alone", item["title"], item["changed"], "Write the report")
		}
	}
	if _, lines := accountSection(res.Text, "Stuck"); len(lines["In yardr"]) != 2 {
		t.Errorf("a window that opens late hides the pieces it did not change from their states:\n%s", res.Text)
	}
	// A closed piece outside the finished window still appears when it is
	// updated inside it; it belongs in Changed since, with its finished
	// state and update time, but not under Finished lately.
	oldClosed := y.work(t, "An older finished piece", "yardr", "ada")
	if _, err := y.st.CloseBead(y.ctx, oldClosed, "done", store.Expect{}, "ada"); err != nil {
		t.Fatal(err)
	}
	oldSince := time.Now()
	if err := y.st.AddNote(y.ctx, oldClosed, "a later note", "bob"); err != nil {
		t.Fatal(err)
	}
	res, err = s.WhatIsHappening(y.ctx, "", oldSince.UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	plain(t, res.Text)
	if _, lines := accountSection(res.Text, changed); len(lines["In yardr"]) != 1 ||
		whenWords.ReplaceAllString(lines["In yardr"][0], "<when>") != "An older finished piece (finished, changed <when>)" {
		t.Errorf("an older finished piece changed inside the window is %v:\n%s", lines, res.Text)
	}
	if _, lines := accountSection(res.Text, "Finished lately"); len(lines["In yardr"]) != 0 {
		t.Errorf("an older finished piece appeared under Finished lately:\n%s", res.Text)
	}
	summary, _ := res.Structured["summary"].(map[string]any)
	summaryText, _ := summary["text"].(string)
	if !strings.HasPrefix(summaryText, "Your work: 1 finished (1 in yardr),") {
		t.Errorf("an older finished piece is missing from the summary: %+v", summary)
	}
	foundOldClosed := false
	for _, item := range res.Structured["work_list"].([]map[string]any) {
		if item["work"] == oldClosed {
			foundOldClosed = item["state"] == "finished" && item["changed"] == item["updated"]
		}
	}
	if !foundOldClosed {
		t.Errorf("the structured account lacks the changed finished piece %q: %+v", oldClosed, res.Structured["work_list"])
	}
	// The window ends at the clock's now: with the clock an hour before the
	// work was touched, none of it is changed, and the piece finished after
	// that is not finished lately, while the open work is still told.
	early := &mcp.Service{Yard: y.st, User: "ada", Now: func() time.Time { return opens.Add(-time.Hour) }}
	res, err = early.WhatIsHappening(y.ctx, "", opens.Add(-2*time.Hour).UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	if heads, _ := accountSection(res.Text, changed); heads != nil || strings.Contains(res.Text, "Old report") || !strings.Contains(res.Text, "Write the report") {
		t.Errorf("a window that ends before the work was touched is\n%s", res.Text)
	}
	// One piece in full, with its notes.
	res, err = s.WhatIsHappening(y.ctx, mine, "")
	if err != nil {
		t.Fatal(err)
	}
	plain(t, res.Text)
	if !strings.Contains(res.Text, "the figures arrived") || res.Structured["work"] != mine {
		t.Fatalf("the one piece:\n%s\n%+v", res.Text, res.Structured)
	}
	// Asked for by name, each piece is told in the state the account tells
	// it in, in the account's sentence ("It is <state> (<detail>)"), and its
	// structured part carries the same work, title, state, project and
	// detail as the account's item: a piece not started, parked, waiting or
	// stuck is not "under way" for being asked about alone.
	for _, c := range cases {
		id, _ := byTitle[c.title]["work"].(string)
		res, err := s.WhatIsHappening(y.ctx, id, "")
		if err != nil {
			t.Fatal(err)
		}
		plain(t, res.Text)
		var project any // absent, not empty, for a piece with no project
		if c.project != "" {
			project = c.project
		}
		detail, _ := res.Structured["detail"].(string)
		if res.Structured["work"] != id || res.Structured["title"] != c.title || res.Structured["state"] != c.state ||
			res.Structured["project"] != project || whenWords.ReplaceAllString(detail, "<when>") != c.detail {
			t.Errorf("%q asked for alone = %+v, want state %q, project %q, detail %q", c.title, res.Structured, c.state, c.project, c.detail)
		}
		if want := fmt.Sprintf("%s\nIt is %s (%s).", c.title, c.state, detail); !strings.HasPrefix(res.Text, want) {
			t.Errorf("%q asked for alone is\n%s\nwant it to start\n%s", c.title, res.Text, want)
		}
	}
	// A question waiting for the person on finished work is said
	// beside its standing, in the account and in the one piece.
	seq := y.ask(t, done, choicesAsk("ada"))
	waits := fmt.Sprintf("finished; question %d still waits for you", seq)
	res, err = s.WhatIsHappening(y.ctx, done, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "It is "+waits) || fmt.Sprint(res.Structured["waiting_questions"]) != fmt.Sprintf("[%d]", seq) {
		t.Errorf("the finished piece hides its waiting question:\n%s\n%+v", res.Text, res.Structured)
	}
	res, err = s.WhatIsHappening(y.ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, lines := accountSection(res.Text, "Finished lately"); !slices.ContainsFunc(lines["In yardr"], func(line string) bool {
		return strings.HasPrefix(line, "Old report (finished ") && strings.HasSuffix(line, fmt.Sprintf("; question %d still waits for you)", seq))
	}) {
		t.Errorf("the account hides the question waiting on finished work:\n%s", res.Text)
	}
	// A window that excludes the finished work drops it.
	res, err = s.WhatIsHappening(y.ctx, "", time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Text, "Old report") {
		t.Errorf("finished work outside the window is shown:\n%s", res.Text)
	}

	// With nothing open and nothing finished the account says so. Work
	// that has not started is open work, so it is not that account.
	quiet := newYard(t)
	quiet.depot(t, "shop")
	res, err = svc(quiet, "").WhatIsHappening(quiet.ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	// Its structured part has the same shape, so a client reads the counts
	// the same way: the words, and no state.
	empty, ok := res.Structured["work_list"].([]map[string]any)
	summary, _ = res.Structured["summary"].(map[string]any)
	noStates, _ := summary["states"].([]map[string]any)
	if !strings.HasPrefix(res.Text, "Nothing of yours is open, and nothing has finished since ") || !ok || empty == nil || len(empty) != 0 ||
		summary["text"] != res.Text || noStates == nil || len(noStates) != 0 {
		t.Errorf("an empty account is\n%s\n%+v", res.Text, res.Structured)
	}
	quiet.bead(t, store.NewBead{Title: "Plan the stall", Depot: "shop", Stage: "backlog", For: "ada"})
	res, err = svc(quiet, "").WhatIsHappening(quiet.ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, lines := accountSection(res.Text, "Not started"); strings.Contains(res.Text, "Nothing of yours") || len(lines["In shop"]) != 1 ||
		!strings.HasPrefix(lines["In shop"][0], "Plan the stall (last touched ") {
		t.Errorf("an account of work that has not started is\n%s", res.Text)
	}

	settingsErr := errors.New("settings unavailable")
	s.Yard = readErrorYard{Yard: y.st, settingsErr: settingsErr}
	if _, err := s.WhatIsHappening(y.ctx, "", ""); !errors.Is(err, settingsErr) {
		t.Fatalf("a failed owner lookup = %v, want its settings error", err)
	}
	blockersErr := errors.New("blockers unavailable")
	s.Yard = readErrorYard{Yard: y.st, blockersErr: blockersErr}
	if _, err := s.WhatIsHappening(y.ctx, "", ""); !errors.Is(err, blockersErr) {
		t.Fatalf("a failed blocker lookup = %v, want its error", err)
	}
	if _, err := s.WhatIsHappening(y.ctx, mine, ""); !errors.Is(err, blockersErr) {
		t.Fatalf("a failed one-piece blocker lookup = %v, want its error", err)
	}
	sessionsErr := errors.New("sessions unavailable")
	s.Yard = readErrorYard{Yard: y.st, sessionsErr: sessionsErr}
	if _, err := s.WhatIsHappening(y.ctx, "", ""); !errors.Is(err, sessionsErr) {
		t.Fatalf("a failed session lookup = %v, want its error", err)
	}
	checkWhatIsHappeningBuildingNow(t)
}

// A piece is building now while a session of the yard is starting or
// running on it, and only then: a manual group's claim, which a person
// holds, a session that ended and a piece nothing works on are not. No other
// test starts a session through the account, so the claims are made here.
func checkWhatIsHappeningBuildingNow(t *testing.T) {
	t.Helper()
	y := newYard(t)
	y.depot(t, "yardr")
	for _, g := range []store.Group{{Name: "builders", Runner: "herdr", Limit: 5}, {Name: "desk", Runner: "manual", Limit: 5}} {
		if err := y.st.PutGroup(y.ctx, g, "ada"); err != nil {
			t.Fatal(err)
		}
	}
	claim := func(title, group string) (bead string, sess *store.Session) {
		t.Helper()
		bead = y.bead(t, store.NewBead{Title: title, Depot: "yardr", Stage: "new", For: "ada"})
		sess, err := y.st.Claim(y.ctx, bead, group, "new", "yard")
		if err != nil {
			t.Fatal(err)
		}
		return bead, sess
	}
	idle := y.bead(t, store.NewBead{Title: "Nobody on it", Depot: "yardr", Stage: "new", For: "ada"})
	starting, _ := claim("Just claimed", "builders")
	running, sess := claim("Being worked", "builders")
	if err := y.st.MarkRunning(y.ctx, sess.ID, map[string]any{"agent": "worker"}); err != nil {
		t.Fatal(err)
	}
	held, _ := claim("A person has it", "desk")
	ended, sess := claim("Its session died", "builders")
	if err := y.st.EndSession(y.ctx, sess.ID, "died", "the pane closed"); err != nil {
		t.Fatal(err)
	}

	res, err := svc(y, "").WhatIsHappening(y.ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	plain(t, res.Text)
	building := map[string]bool{starting: true, running: true}
	titles := map[string]string{idle: "Nobody on it", starting: "Just claimed", running: "Being worked", held: "A person has it", ended: "Its session died"}
	items := map[string]map[string]any{}
	for _, item := range res.Structured["work_list"].([]map[string]any) {
		items[item["work"].(string)] = item
	}
	// The mark is in the item and in its lines, in its state's section and
	// in the changed one; the item carries it only while it holds.
	_, under := accountSection(res.Text, "Under way")
	_, changed := accountSection(res.Text, "Changed since "+whenWords.String())
	for work, title := range titles {
		item := items[work]
		if item == nil {
			t.Errorf("the account lacks %q:\n%s", title, res.Text)
			continue
		}
		line := func(lines []string) string {
			for _, l := range lines {
				if strings.HasPrefix(l, title+" (") {
					return l
				}
			}
			return ""
		}
		inState, inChanged := line(under["In yardr"]), line(changed["In yardr"])
		if inState == "" || inChanged == "" {
			t.Errorf("%q is not in both sections:\n%s", title, res.Text)
		}
		if got := strings.Contains(inState, "building now"); got != building[work] {
			t.Errorf("%q: its line %q says building now = %v, want %v", title, inState, got, building[work])
		}
		if got := strings.HasSuffix(inChanged, ", building now)"); got != building[work] {
			t.Errorf("%q: its changed line %q says building now = %v, want %v", title, inChanged, got, building[work])
		}
		detail, _ := item["detail"].(string)
		if got := strings.HasSuffix(detail, "; building now"); got != building[work] {
			t.Errorf("%q: its detail %q says building now = %v, want %v", title, detail, got, building[work])
		}
		if flag, present := item["building"]; present != building[work] || present && flag != true {
			t.Errorf("%q: building is %v (present %v), want it true on the pieces being built alone", title, flag, present)
		}
	}
	if ended := items[ended]; ended["state"] != "under way" {
		t.Errorf("the piece whose session died is %v, want it under way again", ended["state"])
	}
}

func choicesAsk(to string) store.Ask {
	return store.Ask{
		Question: "Move the links now?",
		To:       to,
		Context:  []string{"The links still point at the old place."},
		Options: []store.AskOption{
			{Label: "Move them now", Consequence: "all links point at the new place tonight"},
			{Label: "After Friday", Consequence: "the old place stays until the release is out"},
		},
		Recommend: 1, Why: "the test passed",
	}
}

func TestAnswerForms(t *testing.T) {
	y := newYard(t)
	y.depot(t, "yardr")
	s := svc(y, "")

	// A pick, with words.
	bead := y.work(t, "Move the links", "yardr", "ada")
	seq := y.ask(t, bead, choicesAsk("ada"))
	res, err := s.Answer(y.ctx, mcp.AnswerInput{Work: bead, Question: seq, Pick: 1, Words: "tonight"}, false)
	if err != nil {
		t.Fatal(err)
	}
	plain(t, res.Text)
	if res.Structured["state"] != "answered" {
		t.Fatalf("the pick's structured state = %v, want answered", res.Structured["state"])
	}
	q, err := y.st.UserAsks(y.ctx, "ada", "ada")
	if err != nil || len(q.Open) != 0 || len(q.Decided) != 1 || q.Decided[0].Option != 1 {
		t.Fatalf("after the pick: %+v, %v", q, err)
	}

	// Words alone on a question with choices is refused, in plain words.
	bead2 := y.work(t, "Pick a colour", "yardr", "ada")
	seq2 := y.ask(t, bead2, choicesAsk("ada"))
	if _, err := s.Answer(y.ctx, mcp.AnswerInput{Work: bead2, Question: seq2, Words: "blue"}, false); err == nil {
		t.Fatal("words alone on a choices question was answered")
	}
	// Put aside, then decline after it comes back to the queue's words.
	aside, err := s.Answer(y.ctx, mcp.AnswerInput{Work: bead2, Question: seq2, PutAside: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if aside.Structured["state"] != "put aside" {
		t.Fatalf("the put-aside structured state = %v, want put aside", aside.Structured["state"])
	}
	declined, err := s.Answer(y.ctx, mcp.AnswerInput{Work: bead2, Question: seq2, Decline: true, Words: "not mine"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if declined.Structured["state"] != "declined" {
		t.Fatalf("the declined structured state = %v, want declined", declined.Structured["state"])
	}
	q, err = y.st.UserAsks(y.ctx, "ada", "ada")
	if err != nil || len(q.Open) != 0 {
		t.Fatalf("after aside and decline: %+v, %v", q, err)
	}

	// Words alone where the question wants words.
	bead3 := y.work(t, "Tone of the greeting", "yardr", "ada")
	seq3 := y.ask(t, bead3, store.Ask{Question: "What tone?", To: "ada", Context: []string{"the greeting."}})
	if _, err := s.Answer(y.ctx, mcp.AnswerInput{Work: bead3, Question: seq3, Words: "warm"}, false); err != nil {
		t.Fatal(err)
	}
	// Another person's question is not a member's to answer (an
	// operator may answer any, as in every window; that is the
	// store's rule, not this window's).
	bead4 := y.work(t, "Ada's question", "yardr", "ada")
	seq4 := y.ask(t, bead4, choicesAsk("ada"))
	bobSvc := &mcp.Service{Yard: y.st, User: "bob", Now: time.Now}
	if _, err := bobSvc.Answer(y.ctx, mcp.AnswerInput{Work: bead4, Question: seq4, Pick: 1}, false); err == nil {
		t.Fatal("another person's question was answered by a member")
	} else {
		plain(t, err.Error())
	}
}

const crmSource = `name: crm
base: https://crm.example.com/v1
credential: { env: CRM_TOKEN, header: "Authorization: Bearer {credential}" }
calls:
  ping: { kind: read, request: { method: GET, path: /me } }
`

// fakeGiveSecret is unreachable through the tools by construction: the
// Yard interface has no secret call. This test holds the observable
// half: the refusal, in its plain words, with the question left open.
func TestSecretAnswerIsRefusedAndStaysOpen(t *testing.T) {
	y := newYard(t)
	y.depot(t, "yardr")
	if _, err := y.st.PutSource(y.ctx, "crm.yaml", crmSource, "ada"); err != nil {
		t.Fatal(err)
	}
	bead := y.work(t, "Wire the CRM", "yardr", "ada")
	seq := y.ask(t, bead, store.Ask{
		Question: "The API key for the CRM", To: "ada",
		Context: []string{"Create one at https://crm.example.com/account/api (read only)."},
		Secret:  &store.AskSecret{Source: "crm"},
	})
	s := svc(y, "")
	_, err := s.Answer(y.ctx, mcp.AnswerInput{Work: bead, Question: seq, Pick: 1}, false)
	if err == nil {
		t.Fatal("a secret answer was taken through MCP")
	}
	for _, want := range []string{"secrets are never given here", "still open", "yardr bead answer " + bead, "--secret"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal lacks %q: %v", want, err)
		}
	}
	q, err := y.st.UserAsks(y.ctx, "ada", "ada")
	if err != nil || len(q.Open) != 1 || q.Open[0].Ask != seq {
		t.Fatalf("the secret question after the refusal: %+v, %v", q, err)
	}
	// Putting it aside or declining it stays the person's to do here.
	if _, err := s.Answer(y.ctx, mcp.AnswerInput{Work: bead, Question: seq, PutAside: true}, false); err != nil {
		t.Fatalf("putting a secret question aside: %v", err)
	}
}

func TestRunApprovalAsOperator(t *testing.T) {
	y := newYard(t)
	y.depot(t, "yardr")
	bead := y.work(t, "Add a person", "yardr", "ada")
	seq := y.ask(t, bead, store.Ask{
		Question: "Add grace as a person of the yard?", To: "ada",
		Context: []string{"Grace will read her own inbox."},
		Run:     &store.AskRun{Argv: []string{"user", "add", "grace"}},
	})
	s := svc(y, "")
	res, err := s.Answer(y.ctx, mcp.AnswerInput{Work: bead, Question: seq, Approve: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Weighed != nil {
		// Weighed, as a weighty card is: the typed-back number, then
		// the approval goes through with the word set.
		res, err = s.Answer(y.ctx, mcp.AnswerInput{Work: bead, Question: seq, Approve: true}, true)
		if err != nil {
			t.Fatal(err)
		}
	}
	plain(t, res.Text)
	if _, err := y.st.GetUser(y.ctx, "grace"); err != nil {
		t.Fatalf("the approved command did not run: %v", err)
	}
}

// An own-subscription run ask is its person's to approve on this
// path, as it is on the socket (design yardr-ts4d 3.3): a member
// approves the one that spends their own, and nobody else's.
func TestOwnSubscriptionApprovalAsItsPerson(t *testing.T) {
	y := newYard(t)
	y.depot(t, "yardr")
	bead := y.work(t, "How bob is told", "yardr", "bob")
	seq := y.ask(t, bead, store.Ask{
		Question: "Tell bob of mail only?", To: "bob",
		Context: []string{"Bob asked to hear of mail, and nothing else."},
		Run:     &store.AskRun{Argv: []string{"user", "set", "bob", "tell=mail"}},
	})
	bobSvc := &mcp.Service{Yard: y.st, User: "bob", Now: time.Now}
	res, err := bobSvc.Answer(y.ctx, mcp.AnswerInput{Work: bead, Question: seq, Approve: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Weighed != nil {
		res, err = bobSvc.Answer(y.ctx, mcp.AnswerInput{Work: bead, Question: seq, Approve: true}, true)
		if err != nil {
			t.Fatal(err)
		}
	}
	plain(t, res.Text)
	u, err := y.st.GetUser(y.ctx, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Tell) != 1 || u.Tell[0] != "mail" {
		t.Fatalf("bob's tell after his own approval = %v", u.Tell)
	}
}

func TestWhatWaitsForMe(t *testing.T) {
	y := newYard(t)
	y.depot(t, "yardr")
	bead := y.work(t, "Move the links", "yardr", "ada")
	seq := y.ask(t, bead, choicesAsk("ada"))
	res, err := svc(y, "").WhatWaitsForMe(y.ctx)
	if err != nil {
		t.Fatal(err)
	}
	plain(t, res.Text)
	for _, want := range []string{"Move the links now?", "Move them now", "Recommended: 1"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("the waiting list lacks %q:\n%s", want, res.Text)
		}
	}
	qs, _ := res.Structured["questions"].([]map[string]any)
	if len(qs) != 1 || qs[0]["work"] != bead || qs[0]["question"] != seq {
		t.Fatalf("the structured questions = %+v", res.Structured)
	}
	// The question's object is the card a person reads: its words,
	// its numbered choices, who asked and when.
	q := qs[0]
	if text, _ := q["text"].(string); !strings.Contains(text, "Move the links now?") || !strings.Contains(text, "Move them now") {
		t.Errorf("the structured question's words = %q", q["text"])
	}
	choices, _ := q["choices"].([]map[string]any)
	// The store adds its own last choice (leave it to the asker).
	if len(choices) != 3 || choices[0]["number"] != 1 || choices[0]["label"] != "Move them now" || choices[1]["label"] != "After Friday" || choices[2]["number"] != 3 {
		t.Errorf("the structured choices = %+v", q["choices"])
	}
	if q["asked_by"] != "yardmaster" || q["asked_at"] == "" || q["recommended"] != 1 || q["title"] != "Move the links" {
		t.Errorf("the structured question = %+v", q)
	}
	if _, ok := qs[0]["question_text"]; ok {
		t.Fatalf("the structured result duplicates question text without its outside mark: %+v", qs[0])
	}
}

func TestReadMyMailAcksThroughWhatItShowed(t *testing.T) {
	y := newYard(t)
	s := svc(y, "")
	if err := y.st.Mail(y.ctx, "ada", "first note", "yardmaster"); err != nil {
		t.Fatal(err)
	}
	if err := y.st.Mail(y.ctx, "ada", "second note", "bob"); err != nil {
		t.Fatal(err)
	}
	box, err := y.st.UserInbox(y.ctx, "ada", 0, "ada")
	if err != nil || len(box.Mail) != 2 {
		t.Fatalf("ada's inbox = %+v, %v", box, err)
	}
	first, second := box.Mail[0].Seq, box.Mail[1].Seq
	res, err := s.ReadMyMail(y.ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	plain(t, res.Text)
	if !strings.Contains(res.Text, "first note") || !strings.Contains(res.Text, "second note") {
		t.Fatalf("the unread mail:\n%s", res.Text)
	}
	// Each message carries its number both ways: "Mail <seq>" on the
	// From line and seq in the structured message.
	for _, want := range []string{
		fmt.Sprintf("Mail %d from yardmaster", first),
		fmt.Sprintf("Mail %d from bob", second),
	} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("the unread mail lacks %q:\n%s", want, res.Text)
		}
	}
	messages, _ := res.Structured["messages"].([]map[string]any)
	if len(messages) != 2 || messages[0]["seq"] != first || messages[1]["seq"] != second {
		t.Fatalf("the structured numbers = %+v, want %d and %d", res.Structured, first, second)
	}
	// Both senders read, and marked read: nothing waits now.
	if n, err := s.UnreadCount(y.ctx); err != nil || n != 0 {
		t.Fatalf("unread after reading = %d, %v", n, err)
	}
	res, err = s.ReadMyMail(y.ctx, 0)
	if err != nil || !strings.Contains(res.Text, "no unread mail") {
		t.Fatalf("the second read: %v\n%s", err, res.Text)
	}
	// What arrives after is the next call's, unacknowledged until then.
	if err := y.st.Mail(y.ctx, "ada", "third note", "yardmaster"); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.UnreadCount(y.ctx); n != 1 {
		t.Fatalf("unread = %d, want the new one", n)
	}
	res, err = s.ReadMyMail(y.ctx, 0)
	if err != nil || !strings.Contains(res.Text, "third note") || strings.Contains(res.Text, "first note") {
		t.Fatalf("the third read: %v\n%s", err, res.Text)
	}
}

func TestReadMyMailRecentLooksBackWithoutAcking(t *testing.T) {
	y := newYard(t)
	s := svc(y, "")
	if err := y.st.Mail(y.ctx, "ada", "first note", "yardmaster"); err != nil {
		t.Fatal(err)
	}
	if err := y.st.Mail(y.ctx, "ada", "second note", "bob"); err != nil {
		t.Fatal(err)
	}
	// A plain call reads the first two, so the look-back spans both states.
	if _, err := s.ReadMyMail(y.ctx, 0); err != nil {
		t.Fatal(err)
	}
	if err := y.st.Mail(y.ctx, "ada", "third note", "yardmaster"); err != nil {
		t.Fatal(err)
	}
	box, err := y.st.UserInbox(y.ctx, "ada", 0, "ada")
	if err != nil {
		t.Fatal(err)
	}
	var thirdSeq, secondSeq uint64
	for _, l := range box.Mail {
		switch l.Text {
		case "third note":
			thirdSeq = l.Seq
		case "second note":
			secondSeq = l.Seq
		}
	}
	res, err := s.ReadMyMail(y.ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	plain(t, res.Text)
	// Bounded to two, unread first: the new note, then the latest read.
	if !strings.Contains(res.Text, "Your 2 recent messages:") {
		t.Fatalf("the look-back header:\n%s", res.Text)
	}
	third := strings.Index(res.Text, "third note")
	second := strings.Index(res.Text, "second note")
	if third < 0 || second < 0 || third > second || strings.Contains(res.Text, "first note") {
		t.Fatalf("the looked-back mail:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "(unread)") || !strings.Contains(res.Text, "(read)") {
		t.Fatalf("the look-back marks nothing read or unread:\n%s", res.Text)
	}
	messages, _ := res.Structured["messages"].([]map[string]any)
	if len(messages) != 2 {
		t.Fatalf("the structured look-back = %+v", res.Structured)
	}
	if messages[0]["text"] != "third note" || messages[0]["read"] != false {
		t.Fatalf("the structured unread = %+v", messages[0])
	}
	if messages[1]["text"] != "second note" || messages[1]["read"] != true {
		t.Fatalf("the structured read = %+v", messages[1])
	}
	for _, m := range messages {
		if m["from"] == "" || m["at"] == "" || m["text"] == "" {
			t.Fatalf("a structured message lacks its words: %+v", m)
		}
	}
	if messages[0]["seq"] != thirdSeq || messages[1]["seq"] != secondSeq {
		t.Fatalf("the structured numbers = %+v, want %d and %d", res.Structured, thirdSeq, secondSeq)
	}
	for _, want := range []string{fmt.Sprintf("Mail %d from", thirdSeq), fmt.Sprintf("Mail %d from", secondSeq)} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("the look-back lacks %q:\n%s", want, res.Text)
		}
	}
	// The look-back moved no cursor: the plain call still shows the unread.
	if n, err := s.UnreadCount(y.ctx); err != nil || n != 1 {
		t.Fatalf("unread after the look-back = %d, %v", n, err)
	}
	res, err = s.ReadMyMail(y.ctx, 0)
	if err != nil || !strings.Contains(res.Text, "third note") || strings.Contains(res.Text, "second note") {
		t.Fatalf("the plain call after the look-back: %v\n%s", err, res.Text)
	}
	// A non-positive count is refused in plain words.
	if _, err := s.ReadMyMail(y.ctx, -1); err == nil {
		t.Fatal("a negative recent was served")
	} else {
		plain(t, err.Error())
	}
}

// Answering a mail by its number sends mail to its sender, carrying the
// answered number: the sender reads it back as the reply's link. What is
// no message of the inbox, and nothing to say, is refused in plain words,
// and nothing is sent.
func TestReplyToMailAnswersByNumber(t *testing.T) {
	y := newYard(t)
	s := svc(y, "")
	if err := y.st.Mail(y.ctx, "ada", "when is the freeze over?", "bob"); err != nil {
		t.Fatal(err)
	}
	box, err := y.st.UserInbox(y.ctx, "ada", 0, "ada")
	if err != nil || len(box.Mail) != 1 {
		t.Fatalf("ada's inbox = %+v, %v", box, err)
	}
	seq := box.Mail[0].Seq
	res, err := s.ReplyToMail(y.ctx, seq, "Friday.")
	if err != nil {
		t.Fatal(err)
	}
	plain(t, res.Text)
	if want := fmt.Sprintf("Sent to bob as a reply to message %d.", seq); res.Text != want {
		t.Fatalf("the answer = %q, want %q", res.Text, want)
	}
	if res.Structured["to"] != "bob" || res.Structured["reply_to"] != seq {
		t.Fatalf("the structured answer = %+v, want to bob replying to %d", res.Structured, seq)
	}
	bob, err := y.st.UserInbox(y.ctx, "bob", 0, "bob")
	if err != nil || len(bob.Mail) != 1 || bob.Mail[0].From != "ada" ||
		bob.Mail[0].Text != "Friday." || bob.Mail[0].Answered != seq {
		t.Fatalf("bob's inbox = %+v, %v", bob, err)
	}
	bobService := &mcp.Service{Yard: y.st, User: "bob", Now: time.Now}
	read, err := bobService.ReadMyMail(y.ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("Mail %d from ada re %d", bob.Mail[0].Seq, seq); !strings.Contains(read.Text, want) {
		t.Errorf("read_my_mail does not show the reply's thread link %q:\n%s", want, read.Text)
	}
	messages, _ := read.Structured["messages"].([]map[string]any)
	if len(messages) != 1 || messages[0]["seq"] != bob.Mail[0].Seq || messages[0]["answered"] != seq {
		t.Errorf("read_my_mail does not carry the reply's thread link: %+v", read.Structured)
	}
	for _, tc := range []struct {
		name    string
		mail    uint64
		message string
		want    string
	}{
		{"no message of that number", 999999, "Friday.", "can't find message 999999"},
		{"no number", 0, "Friday.", "say which message"},
		{"nothing to say", seq, "  ", "say what you would like to answer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.ReplyToMail(y.ctx, tc.mail, tc.message)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ReplyToMail = %v, want %q", err, tc.want)
			}
			plain(t, err.Error())
		})
	}
	bob, _ = y.st.UserInbox(y.ctx, "bob", 0, "bob")
	if len(bob.Mail) != 1 {
		t.Fatalf("bob's inbox after the refusals = %+v", bob)
	}
}

// Mail from another yard is not answered here: the refusal names the
// terminal and the commands that answer there, and nothing is sent.
func TestReplyToMailRefusesPeerMail(t *testing.T) {
	y := newYard(t)
	s := svc(y, "")
	key := y.linkNorth(t)
	y.takePeerMail(t, key, "m1", "ada", "clerk", "is the ledger closed?")
	box, err := y.st.UserInbox(y.ctx, "ada", 0, "ada")
	if err != nil || len(box.Mail) != 1 {
		t.Fatalf("ada's inbox = %+v, %v", box, err)
	}
	_, err = s.ReplyToMail(y.ctx, box.Mail[0].Seq, "Friday.")
	for _, want := range []string{"another yard", "clerk@north", "terminal", "yardr mail clerk@north", "yardr reply"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("ReplyToMail = %v, want %q", err, want)
		}
	}
	plain(t, err.Error())
	mails, merr := y.st.EventsOfKind(y.ctx, "mail", 10)
	if merr != nil || len(mails) != 1 {
		t.Fatalf("mail events after the refusal = %+v, %v", mails, merr)
	}
}

// Mail from a session doing the person's work is not answered by mail:
// its answer is a note, and the refusal names the ways.
func TestReplyToMailRefusesSessionMail(t *testing.T) {
	y := newYard(t)
	s := svc(y, "")
	y.sessionMail(t, "ada", "may I narrow it to the shed?")
	box, err := y.st.UserInbox(y.ctx, "ada", 0, "ada")
	if err != nil || len(box.Mail) != 1 {
		t.Fatalf("ada's inbox = %+v, %v", box, err)
	}
	_, err = s.ReplyToMail(y.ctx, box.Mail[0].Seq, "Yes: only the shed.")
	for _, want := range []string{"a note on that work", "footplate", "yardmaster"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("ReplyToMail = %v, want %q", err, want)
		}
	}
	plain(t, err.Error())
}

// Mail from a yard no longer linked is refused as the command refuses
// it: no address would reach its sender.
func TestReplyToMailRefusesUnlinkedMail(t *testing.T) {
	y := newYard(t)
	s := svc(y, "")
	key := y.linkNorth(t)
	y.takePeerMail(t, key, "m1", "ada", "clerk", "is the ledger closed?")
	if err := y.st.DeletePeer(y.ctx, "north", "ada"); err != nil {
		t.Fatal(err)
	}
	box, err := y.st.UserInbox(y.ctx, "ada", 0, "ada")
	if err != nil || len(box.Mail) != 1 {
		t.Fatalf("ada's inbox = %+v, %v", box, err)
	}
	_, err = s.ReplyToMail(y.ctx, box.Mail[0].Seq, "Friday.")
	for _, want := range []string{"no longer linked", "was north", "no address would reach its sender"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("ReplyToMail = %v, want %q", err, want)
		}
	}
	plain(t, err.Error())
}

func TestAskTheYardmasterWait(t *testing.T) {
	y := newYard(t)
	// The reply inside the wait is the result.
	replyYard := mailHookYard{Yard: y.st, afterMail: func(ctx context.Context, to, _ string, actor string) error {
		if to != "yardmaster" {
			return nil
		}
		return y.st.Mail(ctx, actor, "the answer is 42", to)
	}}
	s := &mcp.Service{Yard: replyYard, User: "ada", Wait: 5 * time.Second,
		PollInterval: 10 * time.Millisecond, ProgressInterval: time.Hour}
	settingsErr := errors.New("settings unavailable")
	s.Yard = readErrorYard{Yard: y.st, settingsErr: settingsErr}
	if _, err := s.AskTheYardmaster(y.ctx, "what is the answer?", true, nil); !errors.Is(err, settingsErr) {
		t.Fatalf("a failed yardmaster setting lookup = %v, want its error", err)
	}
	crewErr := errors.New("crew unavailable")
	s.Yard = readErrorYard{Yard: y.st, crewErr: crewErr}
	if _, err := s.AskTheYardmaster(y.ctx, "what is the answer?", true, nil); !errors.Is(err, crewErr) {
		t.Fatalf("a failed yardmaster roster lookup = %v, want its error", err)
	}
	// Every result says its words in the structured part too, for a client
	// that shows only that part (Claude Code showed {"waiting": true}).
	wordsInBoth := func(res *mcp.Result, waiting bool) {
		t.Helper()
		if res.Structured["message"] != res.Text || res.Structured["waiting"] != waiting {
			t.Errorf("the structured part = %v, want waiting %v and the result's words:\n%s", res.Structured, waiting, res.Text)
		}
	}
	s.Yard = replyYard
	var progress []string
	res, err := s.AskTheYardmaster(y.ctx, "what is the answer?", true, func(msg string) { progress = append(progress, msg) })
	if err != nil {
		t.Fatal(err)
	}
	plain(t, res.Text)
	if !strings.Contains(res.Text, "the answer is 42") || len(progress) == 0 || progress[0] != "asked; waiting for the answer" {
		t.Fatalf("the waited reply:\n%s\nprogress %v", res.Text, progress)
	}
	wordsInBoth(res, false)
	if res.Structured["reply"] != "the answer is 42" {
		t.Errorf("the structured reply = %v", res.Structured["reply"])
	}
	// Finding the reply marks nothing read: that is read_my_mail's.
	if box, err := y.st.UserInbox(y.ctx, "ada", 0, "ada"); err != nil || box.Unread != 1 {
		t.Fatalf("the inbox after the wait found the reply = %+v, %v; want the reply still unread", box, err)
	}
	// Without a wait the call ends once the message is sent. The reply
	// that lands at once, which a wait finds on its first look, is not
	// looked for (it is read_my_mail's), no progress line says a wait is
	// standing, and the message stands in the conversation.
	progress = nil
	res, err = s.AskTheYardmaster(y.ctx, "an FYI for you", false, func(msg string) { progress = append(progress, msg) })
	if err != nil {
		t.Fatal(err)
	}
	plain(t, res.Text)
	if res.Text != "Sent to the yardmaster; the reply comes by mail. Ask me to read your mail later." || len(progress) != 0 {
		t.Fatalf("the send without a wait:\n%s\nprogress %v", res.Text, progress)
	}
	wordsInBoth(res, true)
	if _, found := res.Structured["reply"]; found {
		t.Errorf("the send without a wait found a reply: %v", res.Structured)
	}
	standing, _, err := y.st.Conversation(y.ctx, "ada", "yardmaster", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var fyi bool
	for _, l := range standing {
		fyi = fyi || (l.From == "ada" && l.Text == "an FYI for you")
	}
	if !fyi {
		t.Errorf("the conversation after the send without a wait lacks the message: %+v", standing)
	}
	// The reply that message drew is there, unread, with the first.
	if box, err := y.st.UserInbox(y.ctx, "ada", 0, "ada"); err != nil || box.Unread != 2 {
		t.Fatalf("the inbox after the send without a wait = %+v, %v; want both replies unread", box, err)
	}
	// A reply the person's other window (footplate on a laptop) read
	// before the wait's first look is still the reply: it is lost to the
	// connection both ways otherwise, for read_my_mail shows unread mail only.
	readElsewhere := func(ctx context.Context, user string) error {
		head, err := y.st.Head(ctx)
		if err != nil {
			return err
		}
		return y.st.AckUserInbox(store.WithVia(ctx, user, "laptop"), user, head, user)
	}
	s.Yard = mailHookYard{Yard: y.st, afterMail: func(ctx context.Context, to, _ string, actor string) error {
		if to != "yardmaster" {
			return nil
		}
		if err := y.st.Mail(ctx, actor, "it is filed", to); err != nil {
			return err
		}
		return readElsewhere(ctx, actor)
	}}
	res, err = s.AskTheYardmaster(y.ctx, "is it filed?", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Structured["reply"] != "it is filed" {
		t.Fatalf("the reply read elsewhere before the wait looked:\n%s", res.Text)
	}
	wordsInBoth(res, false)
	// With two replies, the first is the reply even when it is the one
	// read elsewhere and the later one is still unread, which the inbox
	// lists first.
	s.Yard = mailHookYard{Yard: y.st, afterMail: func(ctx context.Context, to, _ string, actor string) error {
		if to != "yardmaster" {
			return nil
		}
		if err := y.st.Mail(ctx, actor, "first answer", to); err != nil {
			return err
		}
		if err := readElsewhere(ctx, actor); err != nil {
			return err
		}
		return y.st.Mail(ctx, actor, "second answer", to)
	}}
	res, err = s.AskTheYardmaster(y.ctx, "and the rest?", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Structured["reply"] != "first answer" {
		t.Fatalf("the first of two replies, read elsewhere:\n%s", res.Text)
	}
	// The wait ending says the answer will come, and keeps the message.
	s.Yard = y.st
	s.Wait = 150 * time.Millisecond
	res, err = s.AskTheYardmaster(y.ctx, "and this one?", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "The answer will come") {
		t.Fatalf("the ended wait:\n%s", res.Text)
	}
	wordsInBoth(res, true)
	// The message itself stands in the conversation, read or not.
	letters, _, err := y.st.Conversation(y.ctx, "ada", "yardmaster", 0, 0, 0)
	if err != nil || len(letters) == 0 {
		t.Fatalf("the conversation after the wait: %v letters, %v", len(letters), err)
	}
	// Cancelling the wait keeps the message standing too.
	ctx, cancel := context.WithCancel(y.ctx)
	s.Wait = time.Minute
	sent := make(chan struct{}, 1)
	s.Yard = mailHookYard{Yard: y.st, afterMail: func(context.Context, string, string, string) error {
		sent <- struct{}{}
		return nil
	}}
	done := make(chan *mcp.Result, 1)
	go func() {
		res, _ := s.AskTheYardmaster(ctx, "a third question", true, nil)
		done <- res
	}()
	select {
	case <-sent:
	case <-time.After(5 * time.Second):
		t.Fatal("the question was not sent before cancelling")
	}
	cancel()
	select {
	case res := <-done:
		if !strings.Contains(res.Text, "still with the yardmaster") {
			t.Fatalf("the cancelled wait:\n%s", res.Text)
		}
		wordsInBoth(res, true)
	case <-time.After(5 * time.Second):
		t.Fatal("the cancelled wait did not end")
	}
}

func TestConnectionMarkOnWrites(t *testing.T) {
	y := newYard(t)
	y.depot(t, "yardr")

	// A write under a connection name reads "via" it; without a name,
	// as the user alone. Reads (what waits) leave no mark either way.
	for _, conn := range []string{"openclaw", ""} {
		s := svc(y, conn)
		res, err := s.AskForSomething(y.ctx, "File a thing "+conn, "")
		if err != nil {
			t.Fatal(err)
		}
		beadID := res.Structured["work"].(string)
		evs, err := y.st.Events(y.ctx, beadID, 10)
		if err != nil {
			t.Fatal(err)
		}
		var via string
		for _, e := range evs {
			if e.Kind == "created" {
				via = store.Via(e)
			}
		}
		if via != conn {
			t.Errorf("connection %q: the filed work's via = %q", conn, via)
		}
		// The person's own answer on a question is marked the same way.
		seq := y.ask(t, beadID, choicesAsk("ada"))
		if _, err := s.Answer(y.ctx, mcp.AnswerInput{Work: beadID, Question: seq, Pick: 2}, false); err != nil {
			t.Fatal(err)
		}
		notes, err := y.st.Notes(y.ctx, beadID)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range notes {
			if n.Kind == store.NoteAnswer && n.Via != conn {
				t.Errorf("connection %q: the answer note's via = %q", conn, n.Via)
			}
		}
		// So are rewording the filed work and withdrawing it.
		if _, err := s.Reword(y.ctx, beadID, "Reworded "+conn, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Withdraw(y.ctx, beadID, "not needed"); err != nil {
			t.Fatal(err)
		}
		evs, err = y.st.Events(y.ctx, beadID, 20)
		if err != nil {
			t.Fatal(err)
		}
		marked := map[string]bool{}
		for _, e := range evs {
			if e.Kind == "updated" || e.Kind == "closed" {
				marked[e.Kind] = true
				if got := store.Via(e); got != conn {
					t.Errorf("connection %q: the %s event's via = %q", conn, e.Kind, got)
				}
			}
		}
		if !marked["updated"] || !marked["closed"] {
			t.Errorf("connection %q: the history lacks the reword or the withdrawal: %+v", conn, evs)
		}
		if _, err := s.WhatWaitsForMe(y.ctx); err != nil {
			t.Fatal(err)
		}
		head, err := y.st.Head(y.ctx)
		if err != nil {
			t.Fatal(err)
		}
		_ = head
	}

	// A read leaves no mark: what waits, read under a connection,
	// writes no event at all, and reading mail marks none of its
	// bookkeeping with the connection.
	s := svc(y, "openclaw")
	before, err := y.st.Head(y.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.WhatWaitsForMe(y.ctx); err != nil {
		t.Fatal(err)
	}
	if err := y.st.Mail(y.ctx, "ada", "one letter", "yardmaster"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadMyMail(y.ctx, 0); err != nil {
		t.Fatal(err)
	}
	evs, err := y.st.EventsSince(y.ctx, before, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if store.Via(e) == "openclaw" {
			t.Errorf("a read left a marked event: %+v", e)
		}
	}
}

func TestFooterCounts(t *testing.T) {
	if got := mcp.Footer(0); got != "" {
		t.Errorf("Footer(0) = %q, want silence", got)
	}
	if got := mcp.Footer(1); !strings.Contains(got, "1 unread message waiting. Call read_my_mail to read it") {
		t.Errorf("Footer(1) = %q", got)
	}
	if got := mcp.Footer(3); !strings.Contains(got, "3 unread messages waiting. Call read_my_mail to read them") {
		t.Errorf("Footer(3) = %q", got)
	}
}
