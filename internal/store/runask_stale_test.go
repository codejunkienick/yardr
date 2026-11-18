package store

import (
	"fmt"
	"strings"
	"testing"
)

// Changing a source tells the asker of an open run ask naming it, once, in
// words naming the ask and what changed; changing an unrelated source tells
// nothing, and a second change while the ask stands open tells nothing more.
// Taken back and asked again, the new ask is covered.
func TestSourceChangeTellsCrewAskerOfStaleRunAsk(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	seq := y.ask(t, listAsk("mckean", "source", "set", "crm", "calls_per_hour=200"), "yardmaster")
	if page, err := y.Store.Inbox(y.ctx, "yardmaster", 0); err != nil || page.Total != 0 {
		t.Fatalf("the asker's inbox after asking = %+v, %v, want empty", page, err)
	}
	// An unrelated source comes and changes without a tell.
	if _, err := y.PutSource(y.ctx, "phone.yaml", phoneDecl, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.PutSource(y.ctx, "phone.yaml", phoneDecl+"# another\n", "mckean"); err != nil {
		t.Fatal(err)
	}
	if page, err := y.Store.Inbox(y.ctx, "yardmaster", 0); err != nil || page.Total != 0 {
		t.Fatalf("the asker's inbox after an unrelated change = %+v, %v, want empty", page, err)
	}
	// Changing the named source tells once.
	if _, err := y.PutSource(y.ctx, "crm.yaml", runCRM+"# another\n", "mckean"); err != nil {
		t.Fatal(err)
	}
	page, err := y.Store.Inbox(y.ctx, "yardmaster", 0)
	if err != nil || page.Total != 1 {
		t.Fatalf("the asker's inbox = %+v, %v, want one tell", page, err)
	}
	e := page.Events[0]
	if e.Kind != "ask_stale" {
		t.Fatalf("the tell is a %s event, want ask_stale", e.Kind)
	}
	if got := Reason(e, "yardmaster"); got != ReasonAnswered {
		t.Fatalf("the asker heard %q, want %q", got, ReasonAnswered)
	}
	line, _ := e.Data["answer"].(string)
	for _, want := range []string{y.bead, fmt.Sprint(seq), "crm", "source crm"} {
		if !strings.Contains(line, want) {
			t.Errorf("the line has no %q:\n%s", want, line)
		}
	}
	if got := e.Data["source"]; got != "crm" {
		t.Errorf("the tell names source %v, want crm", got)
	}
	if got := e.Data["seen"]; got != "source crm" {
		t.Errorf("the tell names digest %v, want source crm", got)
	}
	switch got := e.Data["ask"].(type) {
	case float64:
		if uint64(got) != seq {
			t.Errorf("the tell names ask %v, want %d", got, seq)
		}
	case uint64:
		if got != seq {
			t.Errorf("the tell names ask %v, want %d", got, seq)
		}
	default:
		t.Errorf("the tell names ask %v (%T), want %d", got, got, seq)
	}
	// A second change while the ask stands open tells nothing more: the
	// first tell named it already.
	if _, err := y.PutSource(y.ctx, "crm.yaml", runCRM+"# a third\n", "mckean"); err != nil {
		t.Fatal(err)
	}
	if page, err := y.Store.Inbox(y.ctx, "yardmaster", 0); err != nil || page.Total != 1 {
		t.Fatalf("the asker's inbox after a second change = %+v, %v, want the one tell", page, err)
	}
	// The ask is indeed stale: its approval is refused and records nothing.
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil); err == nil || !strings.Contains(err.Error(), "Nothing ran") {
		t.Fatalf("approving the stale ask = %v, want a refusal that ran nothing", err)
	}
	// Taken back and asked again, the new ask is covered.
	if err := y.TakeBackAsk(y.ctx, y.bead, seq, "asking again", "yardmaster"); err != nil {
		t.Fatal(err)
	}
	again := y.ask(t, listAsk("mckean", "source", "set", "crm", "calls_per_hour=200"), "yardmaster")
	if _, err := y.PutSource(y.ctx, "crm.yaml", runCRM+"# a fourth\n", "mckean"); err != nil {
		t.Fatal(err)
	}
	page, err = y.Store.Inbox(y.ctx, "yardmaster", 0)
	if err != nil || page.Total != 2 {
		t.Fatalf("the asker's inbox = %+v, %v, want two tells", page, err)
	}
	line, _ = page.Events[1].Data["answer"].(string)
	if !strings.Contains(line, fmt.Sprint(again)) {
		t.Fatalf("the second tell names no ask %d:\n%s", again, line)
	}
}

// A user who asked is told in its notifications, with the same line; the
// crew member who did not ask hears nothing.
func TestSourceChangeTellsUserAskerOfStaleRunAsk(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	seq := y.ask(t, listAsk("mckean", "source", "set", "crm", "calls_per_hour=200"), "ada")
	if _, err := y.PutSource(y.ctx, "crm.yaml", runCRM+"# another\n", "mckean"); err != nil {
		t.Fatal(err)
	}
	evs, err := y.Store.Events(y.ctx, y.bead, 100)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range evs {
		if e.Kind != "ask_stale" || Notified(e, "ada") != ReasonAnswered {
			continue
		}
		found = true
		line, _ := e.Data["answer"].(string)
		for _, want := range []string{y.bead, fmt.Sprint(seq), "crm", "source crm"} {
			if !strings.Contains(line, want) {
				t.Errorf("the line has no %q:\n%s", want, line)
			}
		}
	}
	if !found {
		t.Fatal("ada was not told")
	}
	page, err := y.Store.UserNotifications(y.ctx, "ada", 0, "ada")
	if err != nil || page.Unread != 1 {
		t.Fatalf("ada's notifications = %+v, %v, want one unread", page, err)
	}
	text := page.Notifications[0].Text
	for _, want := range []string{y.bead, fmt.Sprint(seq), "crm"} {
		if !strings.Contains(text, want) {
			t.Errorf("the notification has no %q:\n%s", want, text)
		}
	}
	if page, err := y.Store.Inbox(y.ctx, "yardmaster", 0); err != nil || page.Total != 0 {
		t.Fatalf("the yardmaster's inbox = %+v, %v, want empty", page, err)
	}
}
