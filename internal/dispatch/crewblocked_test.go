package dispatch_test

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"yardr.dev/yardr/internal/dispatch"
	"yardr.dev/yardr/internal/store"
)

// notifyRunner is a crewRunner that also shows notifications, recording
// them: the fake of a runtime that tells a human at its machine.
type notifyRunner struct {
	*crewRunner
	mu       sync.Mutex
	notified []string
}

func (n *notifyRunner) Notify(_ context.Context, title, body string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.notified = append(n.notified, title+"\n"+body)
	return nil
}

func (n *notifyRunner) shown() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.notified...)
}

// blockedSetup starts the yardmaster's line session under a runner that
// records its notifications, and leaves its agent blocked: two passes to
// start it, the block, one pass to record it. operator, when named, is
// added as a user and set as the yard's operator.
func blockedSetup(t *testing.T, operator string) (store.API, *notifyRunner, *dispatch.Dispatcher, string) {
	t.Helper()
	s, f, d := crewSetup(t, store.CrewMember{})
	ctx := context.Background()
	if operator != "" {
		if _, err := s.AddUser(ctx, operator, "me"); err != nil {
			t.Fatal(err)
		}
		if err := s.SetSetting(ctx, store.OperatorSetting, operator, "me"); err != nil {
			t.Fatal(err)
		}
	}
	n := &notifyRunner{crewRunner: f}
	d.Runners["fake"] = n
	must(t, d.Reconcile(ctx))
	must(t, d.Reconcile(ctx))
	agent := agentOf(t, s)
	f.set(agent, "blocked")
	must(t, d.Reconcile(ctx))
	return s, n, d, agent
}

// mailsTo is the mail the log holds for to, oldest first.
func mailsTo(t *testing.T, s store.API, to string) []store.Event {
	t.Helper()
	evs, err := s.EventsOfKind(context.Background(), "mail", 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.Event
	for _, e := range evs {
		if e.Data["to"] == to {
			out = append(out, e)
		}
	}
	return out
}

// journalTells is the human_notified events on journal, oldest first.
func journalTells(t *testing.T, s store.API, journal string) []store.Event {
	t.Helper()
	evs, err := s.Events(context.Background(), journal, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.Event
	for _, e := range evs {
		if e.Kind == "human_notified" {
			out = append(out, e)
		}
	}
	return out
}

func journalOf(t *testing.T, s store.API) string {
	t.Helper()
	r, err := s.GetCrew(context.Background(), "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	return r.Journal
}

var blockedFor = regexp.MustCompile(`blocked for [0-9]+m[0-9]+s`)

// A crew session blocked past the limit tells once: mail to the operator
// naming the member, the session, how long and its restart, a notification
// beside it, and the event on its journal with the pane's tail. A later
// pass tells nothing more.
func TestCrewBlockedTellsOncePastLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, n, d, _ := blockedSetup(t, "jo")
	n.crewRunner.show("Allow this command? (y/n)")
	journal := journalOf(t, s)
	dispatch.SetClock(d, func() time.Time { return time.Now().Add(6 * time.Minute) })
	must(t, d.Reconcile(ctx))

	mails := mailsTo(t, s, "jo")
	if len(mails) != 1 {
		t.Fatalf("mail to jo = %d, want 1", len(mails))
	}
	text, _ := mails[0].Data["text"].(string)
	for _, want := range []string{"yardmaster", "line session", "yardr crew restart yardmaster"} {
		if !strings.Contains(text, want) {
			t.Errorf("mail %q does not name %q", text, want)
		}
	}
	if !blockedFor.MatchString(text) {
		t.Errorf("mail %q names no blocked-for duration", text)
	}
	if strings.Contains(text, "\n") {
		t.Errorf("mail %q is not one line", text)
	}
	if got := n.shown(); len(got) != 1 || !strings.Contains(got[0], text) {
		t.Fatalf("notifications = %q, want one carrying the mail's line", got)
	}
	tells := journalTells(t, s, journal)
	if len(tells) != 1 {
		t.Fatalf("human_notified on the journal = %d, want 1", len(tells))
	}
	data := tells[0].Data
	if data["crew"] != "yardmaster" {
		t.Errorf("tell data crew = %v, want yardmaster", data["crew"])
	}
	if _, ok := data["user"]; ok {
		t.Errorf("tell data carries user = %v, want none for the line session", data["user"])
	}
	if _, ok := data["blocked"].(string); !ok || data["blocked"] == "" {
		t.Errorf("tell data blocked = %v, want the duration", data["blocked"])
	}
	if data["tail"] != "Allow this command? (y/n)" {
		t.Errorf("tell data tail = %q, want the pane's end", data["tail"])
	}

	dispatch.SetClock(d, func() time.Time { return time.Now().Add(7 * time.Minute) })
	must(t, d.Reconcile(ctx))
	if mails := mailsTo(t, s, "jo"); len(mails) != 1 {
		t.Errorf("mail to jo after a further pass = %d, want 1", len(mails))
	}
	if got := n.shown(); len(got) != 1 {
		t.Errorf("notifications after a further pass = %d, want 1", len(got))
	}
	if tells := journalTells(t, s, journal); len(tells) != 1 {
		t.Errorf("human_notified after a further pass = %d, want 1", len(tells))
	}
}

// A crew session blocked for less than the limit tells nothing.
func TestCrewBlockedSilentUnderLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, n, d, _ := blockedSetup(t, "jo")
	journal := journalOf(t, s)
	if mails := mailsTo(t, s, "jo"); len(mails) != 0 {
		t.Fatalf("mail to jo for a fresh block = %d, want 0", len(mails))
	}
	dispatch.SetClock(d, func() time.Time { return time.Now().Add(4 * time.Minute) })
	must(t, d.Reconcile(ctx))
	if mails := mailsTo(t, s, "jo"); len(mails) != 0 {
		t.Errorf("mail to jo 4m into the block = %d, want 0", len(mails))
	}
	if got := n.shown(); len(got) != 0 {
		t.Errorf("notifications 4m into the block = %d, want 0", len(got))
	}
	if tells := journalTells(t, s, journal); len(tells) != 0 {
		t.Errorf("human_notified 4m into the block = %d, want 0", len(tells))
	}
}

// A block that ends and returns tells again: the next spell's start is
// past the earlier tell.
func TestCrewBlockedTellsAgainAfterNewSpell(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _, d, agent := blockedSetup(t, "jo")
	journal := journalOf(t, s)
	dispatch.SetClock(d, func() time.Time { return time.Now().Add(6 * time.Minute) })
	must(t, d.Reconcile(ctx))
	if mails := mailsTo(t, s, "jo"); len(mails) != 1 {
		t.Fatalf("mail to jo for the first spell = %d, want 1", len(mails))
	}
	fake := d.Runners["fake"].(*notifyRunner).crewRunner
	fake.set(agent, "idle")
	must(t, d.Reconcile(ctx))
	if r, err := s.GetCrew(ctx, "yardmaster"); err != nil || r.Status != "idle" {
		t.Fatalf("status after the idle pass = %q, want idle", r.Status)
	}
	fake.set(agent, "blocked")
	must(t, d.Reconcile(ctx))
	must(t, d.Reconcile(ctx))
	if mails := mailsTo(t, s, "jo"); len(mails) != 2 {
		t.Errorf("mail to jo for the returned spell = %d, want 2", len(mails))
	}
	if tells := journalTells(t, s, journal); len(tells) != 2 {
		t.Errorf("human_notified for the returned spell = %d, want 2", len(tells))
	}
}

// With no operator setting there is no mail to write: the pass logs and
// tells beside the machine only, and fails nothing.
func TestCrewBlockedWithoutOperatorTellsBesideTheMachine(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, n, d, _ := blockedSetup(t, "")
	journal := journalOf(t, s)
	dispatch.SetClock(d, func() time.Time { return time.Now().Add(6 * time.Minute) })
	must(t, d.Reconcile(ctx))
	if mails := mailsTo(t, s, "jo"); len(mails) != 0 {
		t.Errorf("mail without an operator = %d, want 0", len(mails))
	}
	if got := n.shown(); len(got) != 1 {
		t.Errorf("notifications without an operator = %d, want 1", len(got))
	}
	if tells := journalTells(t, s, journal); len(tells) != 1 {
		t.Errorf("human_notified without an operator = %d, want 1", len(tells))
	}
	must(t, d.Reconcile(ctx))
	if tells := journalTells(t, s, journal); len(tells) != 1 {
		t.Errorf("human_notified after a further pass = %d, want 1", len(tells))
	}
}

// The once-per memory is the journal, not the dispatcher's maps: a
// dispatcher that replaces the one that told tells nothing more.
func TestCrewBlockedTellSurvivesDispatcherReplacement(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, n, d, _ := blockedSetup(t, "jo")
	journal := journalOf(t, s)
	dispatch.SetClock(d, func() time.Time { return time.Now().Add(6 * time.Minute) })
	must(t, d.Reconcile(ctx))
	if tells := journalTells(t, s, journal); len(tells) != 1 {
		t.Fatalf("human_notified = %d, want 1", len(tells))
	}
	replacement := &dispatch.Dispatcher{Store: s, Runners: map[string]dispatch.Runner{"fake": n},
		PromptSettle: -1, PromptWait: -1}
	t.Cleanup(func() { dispatch.WaitLimitsIdle(replacement) })
	dispatch.SetClock(replacement, func() time.Time { return time.Now().Add(7 * time.Minute) })
	must(t, replacement.Reconcile(ctx))
	if mails := mailsTo(t, s, "jo"); len(mails) != 1 {
		t.Errorf("mail to jo after the replacement's pass = %d, want 1", len(mails))
	}
	if tells := journalTells(t, s, journal); len(tells) != 1 {
		t.Errorf("human_notified after the replacement's pass = %d, want 1", len(tells))
	}
}

// A conversation session is judged as its own session: the tell names
// its user and its restart.
func TestCrewBlockedConversationSessionTellsForItsUser(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, f, d := crewSetup(t, store.CrewMember{})
	if _, err := s.AddUser(ctx, "hal", "me"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddUser(ctx, "jo", "me"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, store.OperatorSetting, "jo", "me"); err != nil {
		t.Fatal(err)
	}
	serveCrew(t, s, "", map[string]string{"hal": "/tmp/hal.env"})
	n := &notifyRunner{crewRunner: f}
	d.Runners["fake"] = n
	must(t, d.Reconcile(ctx))
	must(t, d.Reconcile(ctx))
	r, err := s.GetCrew(ctx, "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	halAgent, _ := r.Conversations["hal"].Handle["agent"].(string)
	if halAgent == "" {
		t.Fatal("hal's conversation session did not start")
	}
	f.set(halAgent, "blocked")
	must(t, d.Reconcile(ctx))
	dispatch.SetClock(d, func() time.Time { return time.Now().Add(6 * time.Minute) })
	must(t, d.Reconcile(ctx))
	mails := mailsTo(t, s, "jo")
	if len(mails) != 1 {
		t.Fatalf("mail to jo = %d, want 1 (hal's session alone)", len(mails))
	}
	text, _ := mails[0].Data["text"].(string)
	for _, want := range []string{"conversation session for hal", "yardr crew restart yardmaster --for hal"} {
		if !strings.Contains(text, want) {
			t.Errorf("mail %q does not name %q", text, want)
		}
	}
	tells := journalTells(t, s, journalOf(t, s))
	if len(tells) != 1 {
		t.Fatalf("human_notified on the journal = %d, want 1", len(tells))
	}
	if tells[0].Data["user"] != "hal" {
		t.Errorf("tell data user = %v, want hal", tells[0].Data["user"])
	}
}
