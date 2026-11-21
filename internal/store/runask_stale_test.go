package store

import (
	"encoding/json"
	"fmt"
	"os"
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

// Changing a group tells the askers of the open and put-aside run asks
// naming it, once each, in words naming the ask and the group; changing an
// unrelated group tells nothing, and a second change while the asks stand
// tells nothing more. Taken back and asked again, the new ask is covered,
// and a fresh ask taken back before the change is not.
func TestGroupChangeTellsCrewAskerOfStaleRunAsk(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	open := y.ask(t, listAsk("mckean", "group", "set", "builders", "cmd=./deploy.sh"), "yardmaster")
	aside := y.ask(t, listAsk("mckean", "group", "set", "builders", "cmd=./other.sh"), "yardmaster")
	tells := func() []Event {
		t.Helper()
		evs, err := y.Store.Events(y.ctx, y.bead, 100)
		if err != nil {
			t.Fatal(err)
		}
		var out []Event
		for _, e := range evs {
			if e.Kind == "ask_stale" {
				out = append(out, e)
			}
		}
		return out
	}
	wantTells := func(want int) {
		t.Helper()
		if got := tells(); len(got) != want {
			t.Fatalf("the bead holds %d tells, want %d: %v", len(got), want, got)
		}
	}
	if page, err := y.Store.Inbox(y.ctx, "yardmaster", 0); err != nil || page.Total != 0 {
		t.Fatalf("the asker's inbox after asking = %+v, %v, want empty", page, err)
	}
	y.answer(t, Answer{Ask: aside, Later: true}, "mckean")
	// An unrelated group comes and changes without a tell.
	if err := y.PutGroup(y.ctx, Group{Name: "others", Runner: "exec", Config: map[string]any{"cmd": "true"}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.SetGroup(y.ctx, "others", GroupEdit{Set: map[string]any{"cmd": "false"}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	wantTells(0)
	// Changing the named group tells the open ask and the aside one.
	if _, err := y.SetGroup(y.ctx, "builders", GroupEdit{Set: map[string]any{"cmd": "make test"}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	page, err := y.Store.Inbox(y.ctx, "yardmaster", 0)
	if err != nil {
		t.Fatal(err)
	}
	// The inbox holds the aside answer beside the two tells.
	if page.Total != 3 {
		t.Fatalf("the asker's inbox = %+v, %v, want the aside answer and two tells", page, err)
	}
	told := map[uint64]bool{}
	for _, e := range tells() {
		if got := Reason(e, "yardmaster"); got != ReasonAnswered {
			t.Fatalf("the asker heard %q, want %q", got, ReasonAnswered)
		}
		if got := e.Data["kind"]; got != "group" {
			t.Errorf("the tell names kind %v, want group", got)
		}
		if got := e.Data["name"]; got != "builders" {
			t.Errorf("the tell names name %v, want builders", got)
		}
		if got := e.Data["seen"]; got != "group builders" {
			t.Errorf("the tell names digest %v, want group builders", got)
		}
		if _, has := e.Data["source"]; has {
			t.Errorf("the group tell names a source: %v", e.Data)
		}
		var seq uint64
		switch n := e.Data["ask"].(type) {
		case float64:
			seq = uint64(n)
		case uint64:
			seq = n
		default:
			t.Fatalf("the tell names ask %v (%T)", e.Data["ask"], e.Data["ask"])
		}
		told[seq] = true
		line, _ := e.Data["answer"].(string)
		for _, want := range []string{y.bead, fmt.Sprint(seq), "builders", "group builders"} {
			if !strings.Contains(line, want) {
				t.Errorf("the line has no %q:\n%s", want, line)
			}
		}
	}
	if !told[open] || !told[aside] {
		t.Fatalf("the tells name %v, want the open ask %d and the aside ask %d", told, open, aside)
	}
	// A second change while the asks stand tells nothing more.
	if _, err := y.SetGroup(y.ctx, "builders", GroupEdit{Set: map[string]any{"cmd": "make more"}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	wantTells(2)
	// The open ask is indeed stale: its approval is refused, running nothing.
	if _, err := y.ApproveAsk(y.ctx, y.bead, open, "mckean", true, nil); err == nil || !strings.Contains(err.Error(), "Nothing ran") {
		t.Fatalf("approving the stale ask = %v, want a refusal that ran nothing", err)
	}
	// Taken back and asked again, the new ask is covered.
	if err := y.TakeBackAsk(y.ctx, y.bead, open, "asking again", "yardmaster"); err != nil {
		t.Fatal(err)
	}
	again := y.ask(t, listAsk("mckean", "group", "set", "builders", "cmd=./deploy.sh"), "yardmaster")
	if _, err := y.SetGroup(y.ctx, "builders", GroupEdit{Set: map[string]any{"cmd": "make again"}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	wantTells(3)
	if got := tells()[2]; fmt.Sprint(got.Data["ask"]) != fmt.Sprint(again) && got.Data["ask"] != uint64(again) {
		// The stored ask number reads back as a float; either form names it.
		if n, isFloat := got.Data["ask"].(float64); !isFloat || uint64(n) != again {
			t.Fatalf("the third tell names %v, want ask %d", got.Data["ask"], again)
		}
	}
	// A fresh ask taken back before the next change is not told of it.
	gone := y.ask(t, listAsk("mckean", "group", "set", "builders", "cmd=./final.sh"), "yardmaster")
	if err := y.TakeBackAsk(y.ctx, y.bead, gone, "no longer wanted", "yardmaster"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.SetGroup(y.ctx, "builders", GroupEdit{Set: map[string]any{"cmd": "make last"}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	wantTells(3)
}

// A user who asked is told in its notifications, with the same line; the
// crew member who did not ask hears nothing.
func TestGroupChangeTellsUserAskerOfStaleRunAsk(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	seq := y.ask(t, listAsk("mckean", "group", "set", "builders", "cmd=./deploy.sh"), "ada")
	if _, err := y.SetGroup(y.ctx, "builders", GroupEdit{Set: map[string]any{"cmd": "make test"}}, "mckean"); err != nil {
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
		for _, want := range []string{y.bead, fmt.Sprint(seq), "builders"} {
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
	for _, want := range []string{y.bead, fmt.Sprint(seq), "builders"} {
		if !strings.Contains(text, want) {
			t.Errorf("the notification has no %q:\n%s", want, text)
		}
	}
	if page, err := y.Store.Inbox(y.ctx, "yardmaster", 0); err != nil || page.Total != 0 {
		t.Fatalf("the yardmaster's inbox = %+v, %v, want empty", page, err)
	}
}

// Renaming a group tells a hold of its old name, and removing the renamed
// group tells a hold of its new one; a hold of another group is told of
// neither. A hold written against the new name's absence is told when the
// rename stands it there.
func TestGroupRenameAndDeleteTellAsker(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	oldSeq := y.ask(t, listAsk("mckean", "group", "set", "builders", "cmd=./deploy.sh"), "yardmaster")
	control := y.ask(t, listAsk("mckean", "group", "set", "reviewers", "effort=max"), "yardmaster")
	// A hold against the new name's absence, as no card files one today: its
	// Seen is rewritten to the absence the nil rule names, so the rename's
	// second tell has a hold to reach.
	absent := y.ask(t, listAsk("mckean", "group", "set", "reviewers", "effort=high"), "yardmaster")
	absentText, err := func() (string, error) {
		var raw string
		err := y.write(func(w *wtx) error {
			row, err := w.loadBead(y.bead)
			if err != nil {
				return err
			}
			var n Note
			if ok, err := get(w.Tx, bNotes, join(row.UUID, seqKey(absent)), &n); err != nil || !ok {
				if err == nil {
					err = fmt.Errorf("no note %d", absent)
				}
				return err
			}
			ask, err := ParseAsk(n)
			if err != nil {
				return err
			}
			ask.Run.Seen = map[string]string{"group makers": seen("no group", "makers")}
			text, err := json.Marshal(ask)
			if err != nil {
				return err
			}
			n.Text = string(text)
			raw = n.Text
			return put(w.Tx, bNotes, join(row.UUID, seqKey(absent)), n)
		})
		return raw, err
	}()
	if err != nil || absentText == "" {
		t.Fatalf("rewriting the absence hold: %q, %v", absentText, err)
	}
	if _, err := y.RenameGroup(y.ctx, "builders", "makers", "mckean"); err != nil {
		t.Fatal(err)
	}
	evs, err := y.Store.Events(y.ctx, y.bead, 100)
	if err != nil {
		t.Fatal(err)
	}
	byAsk := map[uint64][]Event{}
	for _, e := range evs {
		if e.Kind != "ask_stale" {
			continue
		}
		var seq uint64
		switch n := e.Data["ask"].(type) {
		case float64:
			seq = uint64(n)
		case uint64:
			seq = n
		}
		byAsk[seq] = append(byAsk[seq], e)
	}
	if got := byAsk[oldSeq]; len(got) != 1 || got[0].Data["name"] != "builders" {
		t.Fatalf("the old name's tells = %v, want one for builders", got)
	}
	if got := byAsk[absent]; len(got) != 1 || got[0].Data["name"] != "makers" {
		t.Fatalf("the new name's tells = %v, want one for makers", got)
	}
	if got := byAsk[control]; len(got) != 0 {
		t.Fatalf("the control hold was told: %v", got)
	}
	// The control hold still approves: the rename staled nothing of it.
	if _, err := y.ApproveAsk(y.ctx, y.bead, control, "mckean", false, nil); err != nil {
		t.Fatalf("approving the control after the rename: %v", err)
	}
	// Removing the renamed group tells a fresh hold of its new name.
	fresh := y.ask(t, listAsk("mckean", "group", "set", "makers", "cmd=./deploy.sh"), "yardmaster")
	if _, err := y.DeleteGroup(y.ctx, "makers", "mckean"); err != nil {
		t.Fatal(err)
	}
	evs, err = y.Store.Events(y.ctx, y.bead, 100)
	if err != nil {
		t.Fatal(err)
	}
	var deleted int
	for _, e := range evs {
		if e.Kind != "ask_stale" {
			continue
		}
		if n, isFloat := e.Data["ask"].(float64); isFloat && uint64(n) == fresh && e.Data["name"] == "makers" {
			deleted++
		}
		if n, isUint := e.Data["ask"].(uint64); isUint && n == fresh && e.Data["name"] == "makers" {
			deleted++
		}
	}
	if deleted != 1 {
		t.Fatalf("the removal told the fresh hold %d times, want once", deleted)
	}
}

// A write tells only the holds whose own reading of the group it moved: a
// setting outside the group set card's strong few (a log file) moves what
// grant holds read of the group whole and leaves a group set hold standing,
// which then still approves, while the grant hold's approval refuses.
func TestGroupWriteTellsOnlyTheHoldsWhoseReadingMoved(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	grant := y.ask(t, listAsk("mckean", "source", "grant", "crm", "read", "--group=builders"), "yardmaster")
	set := y.ask(t, listAsk("mckean", "group", "set", "builders", "cmd=./deploy.sh"), "yardmaster")
	g, err := y.GetGroup(y.ctx, "builders")
	if err != nil {
		t.Fatal(err)
	}
	next := *g
	next.Config = map[string]any{"cmd": "make build", "log": "/tmp/builders.log"}
	if err := y.PutGroup(y.ctx, next, "mckean"); err != nil {
		t.Fatal(err)
	}
	evs, err := y.Store.Events(y.ctx, y.bead, 100)
	if err != nil {
		t.Fatal(err)
	}
	var grantTold, setTold bool
	for _, e := range evs {
		if e.Kind != "ask_stale" {
			continue
		}
		switch n := e.Data["ask"].(type) {
		case float64:
			grantTold = grantTold || uint64(n) == grant
			setTold = setTold || uint64(n) == set
		case uint64:
			grantTold = grantTold || n == grant
			setTold = setTold || n == set
		}
	}
	if !grantTold || setTold {
		t.Fatalf("the log write told grant %v and set %v, want the grant alone", grantTold, setTold)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, grant, "mckean", false, nil); err == nil || !strings.Contains(err.Error(), "Nothing ran") {
		t.Fatalf("approving the told grant = %v, want a refusal that ran nothing", err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, set, "mckean", true, nil); err != nil {
		t.Fatalf("approving the standing set hold: %v", err)
	}
	// The approval's own group write answers this hold; it must not also tell
	// the asker that the hold it just approved went stale.
	evs, err = y.Store.Events(y.ctx, y.bead, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Kind == "ask_stale" && fmt.Sprint(e.Data["ask"]) == fmt.Sprint(set) {
			t.Fatalf("the approved set hold told itself stale: %v", e.Data)
		}
	}
	if g, _ := y.GetGroup(y.ctx, "builders"); g.Config["cmd"] != "./deploy.sh" || g.Config["log"] != "/tmp/builders.log" {
		t.Fatalf("group after the standing approval = %+v", g)
	}
}

// Subscription writes to a group never tell its holds: what they write is
// the group's subscriptions alone, which no reading of the group a hold
// carries ever names, and the hold still approves afterwards.
func TestSubscriptionWriteNeverTellsGroupHold(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	if err := y.PutGroup(y.ctx, Group{Name: "people", Runner: "manual", Config: map[string]any{}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	seq := y.ask(t, listAsk("mckean", "source", "grant", "crm", "read", "--group=people"), "yardmaster")
	sub := Subscription{Source: SourceYardr, Scope: ScopeAll}
	if err := y.AddSubscriptionKind(y.ctx, "people", "group", sub, "mckean"); err != nil {
		t.Fatal(err)
	}
	if n, err := y.RemoveSubscriptionsKind(y.ctx, "people", "group", SourceYardr, "", nil, "mckean"); err != nil || n != 1 {
		t.Fatalf("unsubscribing the group = %d, %v", n, err)
	}
	evs, err := y.Store.Events(y.ctx, y.bead, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Kind == "ask_stale" {
			t.Fatalf("a subscription write told the hold: %v", e.Data)
		}
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil); err != nil {
		t.Fatalf("approving after the subscription writes: %v", err)
	}
}

// A depot-push hold is told on its group when the group's command beyond
// its push assignments moves, as a run hold is; turning push itself, which
// its reading leaves out, tells it nothing.
func TestDepotPushHoldToldOnGroupChange(t *testing.T) {
	t.Parallel()
	y := depotPushYard(t)
	seq := y.ask(t, depotPushAsk("web", false), "yardmaster")
	if _, err := y.SetGroup(y.ctx, "web-assembly", GroupEdit{Set: map[string]any{"cmd": depotPushCmd + " --slow"}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	evs, err := y.Store.Events(y.ctx, y.bead, 100)
	if err != nil {
		t.Fatal(err)
	}
	var told int
	for _, e := range evs {
		if e.Kind != "ask_stale" || e.Data["name"] != "web-assembly" {
			continue
		}
		if n, isFloat := e.Data["ask"].(float64); isFloat && uint64(n) == seq {
			told++
		}
		if n, isUint := e.Data["ask"].(uint64); isUint && n == seq {
			told++
		}
	}
	if told != 1 {
		t.Fatalf("the command write told the depot-push hold %d times, want once", told)
	}
	// Turning push on in a fresh yard moves only what the hold leaves out,
	// so it is told nothing and still approves to its carrying record.
	fresh := depotPushYard(t)
	freshSeq := fresh.ask(t, depotPushAsk("web", false), "yardmaster")
	if changed, err := fresh.SetAssemblyCmd(fresh.ctx, "web", false, "mckean"); err != nil || !changed {
		t.Fatalf("turning the cmd half on = %v, %v, want a change", changed, err)
	}
	evs, err = fresh.Store.Events(fresh.ctx, fresh.bead, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Kind == "ask_stale" {
			t.Fatalf("turning push told the hold: %v", e.Data)
		}
	}
	if _, err := fresh.ApproveAsk(fresh.ctx, fresh.bead, freshSeq, "mckean", true, nil); err != nil {
		t.Fatalf("approving after turning push on: %v", err)
	}
}

// peerTells are the ask_stale events on y's bead, oldest first.
func peerTells(t *testing.T, y *askYard) []Event {
	t.Helper()
	evs, err := y.Store.Events(y.ctx, y.bead, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []Event
	for _, e := range evs {
		if e.Kind == "ask_stale" {
			out = append(out, e)
		}
	}
	return out
}

// toldSeq is the ask a tell names, as it reads back (a number, in either
// stored form).
func toldSeq(e Event) uint64 {
	switch n := e.Data["ask"].(type) {
	case float64:
		return uint64(n)
	case uint64:
		return n
	}
	return 0
}

// Changing a peer tells the askers of the open and put-aside run asks
// naming it, once each, in words naming the ask and the peer; changing an
// unrelated peer tells nothing, and a second change while the asks stand
// tells nothing more. Taken back and asked again, the new ask is covered,
// and a fresh ask taken back before the change is not.
func TestPeerChangeTellsCrewAskerOfStaleRunAsk(t *testing.T) {
	t.Parallel()
	y := peerRuleYard(t)
	open := y.ask(t, listAsk("mckean", "peer", "allow", "airy", "--receive", "bead"), "yardmaster")
	aside := y.ask(t, listAsk("mckean", "peer", "allow", "airy", "--send", "mail"), "yardmaster")
	if page, err := y.Store.Inbox(y.ctx, "yardmaster", 0); err != nil || page.Total != 0 {
		t.Fatalf("the asker's inbox after asking = %+v, %v, want empty", page, err)
	}
	y.answer(t, Answer{Ask: aside, Later: true}, "mckean")
	// An unrelated peer changes without a tell.
	if _, err := y.AllowPeer(y.ctx, "north", PeerAllow{Send: []string{PeerBead}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if got := peerTells(t, y); len(got) != 0 {
		t.Fatalf("the bead holds %d tells after an unrelated change, want none", len(got))
	}
	// Changing the named peer tells the open ask and the aside one.
	if _, err := y.AllowPeer(y.ctx, "airy", PeerAllow{Send: []string{PeerBead}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	page, err := y.Store.Inbox(y.ctx, "yardmaster", 0)
	if err != nil {
		t.Fatal(err)
	}
	// The inbox holds the aside answer beside the two tells.
	if page.Total != 3 {
		t.Fatalf("the asker's inbox = %+v, want the aside answer and two tells", page)
	}
	told := map[uint64]bool{}
	for _, e := range peerTells(t, y) {
		if got := Reason(e, "yardmaster"); got != ReasonAnswered {
			t.Fatalf("the asker heard %q, want %q", got, ReasonAnswered)
		}
		if got := e.Data["kind"]; got != "peer" {
			t.Errorf("the tell names kind %v, want peer", got)
		}
		if got := e.Data["name"]; got != "airy" {
			t.Errorf("the tell names name %v, want airy", got)
		}
		if got := e.Data["seen"]; got != "peer airy carries" {
			t.Errorf("the tell names digest %v, want peer airy carries", got)
		}
		if _, has := e.Data["source"]; has {
			t.Errorf("the peer tell names a source: %v", e.Data)
		}
		seq := toldSeq(e)
		told[seq] = true
		line, _ := e.Data["answer"].(string)
		for _, want := range []string{y.bead, fmt.Sprint(seq), "airy", "peer airy"} {
			if !strings.Contains(line, want) {
				t.Errorf("the line has no %q:\n%s", want, line)
			}
		}
	}
	if !told[open] || !told[aside] {
		t.Fatalf("the tells name %v, want the open ask %d and the aside ask %d", told, open, aside)
	}
	// A second change while the asks stand tells nothing more.
	if _, err := y.AllowPeer(y.ctx, "airy", PeerAllow{Send: []string{PeerMail}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if got := peerTells(t, y); len(got) != 2 {
		t.Fatalf("the bead holds %d tells after a second change, want the two", len(got))
	}
	// The open ask is indeed stale: its approval is refused, running nothing.
	if _, err := y.ApproveAsk(y.ctx, y.bead, open, "mckean", true, nil); err == nil || !strings.Contains(err.Error(), "Nothing ran") {
		t.Fatalf("approving the stale ask = %v, want a refusal that ran nothing", err)
	}
	// Taken back and asked again, the new ask is covered.
	if err := y.TakeBackAsk(y.ctx, y.bead, open, "asking again", "yardmaster"); err != nil {
		t.Fatal(err)
	}
	again := y.ask(t, listAsk("mckean", "peer", "allow", "airy", "--receive", "bead"), "yardmaster")
	if _, err := y.AllowPeer(y.ctx, "airy", PeerAllow{Receive: []string{PeerMail}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if got := peerTells(t, y); len(got) != 3 || toldSeq(got[2]) != again {
		t.Fatalf("the bead holds %v after the re-ask changed, want a third tell of ask %d", got, again)
	}
	// A fresh ask taken back before the next change is not told of it.
	gone := y.ask(t, listAsk("mckean", "peer", "allow", "airy", "--receive", "ask"), "yardmaster")
	if err := y.TakeBackAsk(y.ctx, y.bead, gone, "no longer wanted", "yardmaster"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.AllowPeer(y.ctx, "airy", PeerAllow{Receive: []string{PeerBead}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if got := peerTells(t, y); len(got) != 3 {
		t.Fatalf("the bead holds %d tells after the taken-back ask's change, want the three", len(got))
	}
}

// A user who asked about a peer is told in its notifications, with the
// same line; the crew member who did not ask hears nothing.
func TestPeerChangeTellsUserAskerOfStaleRunAsk(t *testing.T) {
	t.Parallel()
	y := peerRuleYard(t)
	seq := y.ask(t, listAsk("mckean", "peer", "allow", "airy", "--receive", "bead"), "ada")
	if _, err := y.AllowPeer(y.ctx, "airy", PeerAllow{Send: []string{PeerBead}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range peerTells(t, y) {
		if Notified(e, "ada") != ReasonAnswered {
			continue
		}
		found = true
		line, _ := e.Data["answer"].(string)
		for _, want := range []string{y.bead, fmt.Sprint(seq), "airy"} {
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
	for _, want := range []string{y.bead, fmt.Sprint(seq), "airy"} {
		if !strings.Contains(text, want) {
			t.Errorf("the notification has no %q:\n%s", want, text)
		}
	}
	if page, err := y.Store.Inbox(y.ctx, "yardmaster", 0); err != nil || page.Total != 0 {
		t.Fatalf("the yardmaster's inbox = %+v, %v, want empty", page, err)
	}
}

// Adding a peer where none stood tells a hold written against its
// absence (a peer add ask), and its approval is then refused; renaming
// tells a hold of the old name and a hold of the new name written against
// its absence, and removing the renamed peer tells a fresh hold of it.
func TestPeerAbsentCreateRenameAndDeleteTellAsker(t *testing.T) {
	t.Parallel()
	y, _ := runYard(t)
	southKey := Public(newKey(t))
	pending := y.ask(t, runAsk("peer add south "+Invite{Name: "south-yard", Key: southKey}.String()), "yardmaster")
	if _, err := y.AddPeer(y.ctx, Peer{Name: "south", Key: southKey, Calls: "south-yard"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if got := peerTells(t, y); len(got) != 1 || toldSeq(got[0]) != pending || got[0].Data["name"] != "south" || got[0].Data["seen"] != "peer south" {
		t.Fatalf("the absent-create tells = %v, want one of ask %d for south", got, pending)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, pending, "mckean", false, nil); err == nil || !strings.Contains(err.Error(), "Nothing ran") {
		t.Fatalf("approving the told add = %v, want a refusal that ran nothing", err)
	}
	pairedKey := Public(newKey(t))
	paired := y.ask(t, runAsk("peer add paired "+Invite{Name: "paired-yard", Key: pairedKey}.String()), "yardmaster")
	if _, err := y.PairPeer(y.ctx, Peer{Name: "paired", Key: pairedKey, Calls: "paired-yard"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	var pairTold bool
	for _, e := range peerTells(t, y) {
		if toldSeq(e) == paired && e.Data["name"] == "paired" && e.Data["seen"] == "peer paired" {
			pairTold = true
		}
	}
	if !pairTold {
		t.Fatalf("pairing did not tell the absent-name hold %d", paired)
	}
	// Renaming airy tells its hold and an absence hold of the new name.
	oldSeq := y.ask(t, listAsk("mckean", "peer", "allow", "airy", "--receive", "bead"), "yardmaster")
	control := y.ask(t, listAsk("mckean", "peer", "allow", "south", "--receive", "bead"), "yardmaster")
	eastKey := Public(newKey(t))
	absent := y.ask(t, runAsk("peer add east "+Invite{Name: "east-yard", Key: eastKey}.String()), "yardmaster")
	if _, err := y.RenamePeer(y.ctx, "airy", "east", "mckean"); err != nil {
		t.Fatal(err)
	}
	byAsk := map[uint64][]Event{}
	for _, e := range peerTells(t, y) {
		byAsk[toldSeq(e)] = append(byAsk[toldSeq(e)], e)
	}
	if got := byAsk[oldSeq]; len(got) != 1 || got[0].Data["name"] != "airy" {
		t.Fatalf("the old name's tells = %v, want one for airy", got)
	}
	if got := byAsk[absent]; len(got) != 1 || got[0].Data["name"] != "east" {
		t.Fatalf("the new name's tells = %v, want one for east", got)
	}
	if got := byAsk[control]; len(got) != 0 {
		t.Fatalf("the control hold was told: %v", got)
	}
	// The control hold still approves: the rename staled nothing of it.
	if _, err := y.ApproveAsk(y.ctx, y.bead, control, "mckean", true, nil); err != nil {
		t.Fatalf("approving the control after the rename: %v", err)
	}
	// Removing the renamed peer tells a fresh hold of its new name.
	fresh := y.ask(t, listAsk("mckean", "peer", "allow", "east", "--receive", "bead"), "yardmaster")
	if err := y.DeletePeer(y.ctx, "east", "mckean"); err != nil {
		t.Fatal(err)
	}
	var deleted int
	for _, e := range peerTells(t, y) {
		if toldSeq(e) == fresh && e.Data["name"] == "east" {
			deleted++
		}
	}
	if deleted != 1 {
		t.Fatalf("the removal told the fresh hold %d times, want once", deleted)
	}
}

// A peer write tells only the holds whose own reading of the link it
// moved: an address change tells a peer set hold and leaves a peer allow
// hold standing, which then still approves; the approval's own write
// tells the other hold and not the ask it approves.
func TestPeerWriteTellsOnlyTheHoldsWhoseReadingMoved(t *testing.T) {
	t.Parallel()
	y := peerRuleYard(t)
	setHold := y.ask(t, listAsk("mckean", "peer", "set", "airy", "--url", "https://peer.example.com"), "yardmaster")
	allowHold := y.ask(t, listAsk("mckean", "peer", "allow", "airy", "--receive", "bead"), "yardmaster")
	if _, err := y.SetPeerURL(y.ctx, "airy", "https://elsewhere.example.com", "mckean"); err != nil {
		t.Fatal(err)
	}
	var setTold, allowTold bool
	for _, e := range peerTells(t, y) {
		setTold = setTold || toldSeq(e) == setHold
		allowTold = allowTold || toldSeq(e) == allowHold
		if toldSeq(e) == setHold && e.Data["seen"] != "peer airy address" {
			t.Errorf("the address tell names %v, want peer airy address", e.Data["seen"])
		}
	}
	if !setTold || allowTold {
		t.Fatalf("the address write told set %v and allow %v, want the set hold alone", setTold, allowTold)
	}
	// The standing allow hold still approves, and its own write tells
	// the set hold nothing more while telling no one of itself: it is
	// the approving ask, left out of its own write's tell.
	second := y.ask(t, listAsk("mckean", "peer", "allow", "airy", "--send", "mail"), "yardmaster")
	if _, err := y.ApproveAsk(y.ctx, y.bead, allowHold, "mckean", true, nil); err != nil {
		t.Fatalf("approving the standing allow hold: %v", err)
	}
	var selfTold, secondTold bool
	for _, e := range peerTells(t, y) {
		selfTold = selfTold || toldSeq(e) == allowHold
		secondTold = secondTold || toldSeq(e) == second
	}
	if selfTold || !secondTold {
		t.Fatalf("the approval told itself %v and the other hold %v, want the other alone", selfTold, secondTold)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, setHold, "mckean", false, nil); err == nil || !strings.Contains(err.Error(), "Nothing ran") {
		t.Fatalf("approving the told set hold = %v, want a refusal that ran nothing", err)
	}
}

// An identity change tells a hold that read that crew member in that
// source and no intake hold; moving the source's records tells it too,
// under its new source. Cursor writes never tell, while a roster change
// that removes reach from a still-trusted crew member does.
func TestPeerIdentityCursorAndRosterWritesTell(t *testing.T) {
	t.Parallel()
	y := intakeYard(t)
	if _, err := y.SetPeerIdentity(y.ctx, "airy", PeerIdentity{Source: "talk", Crew: "mayor", ID: "U1"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	identityHold := y.ask(t, listAsk("mckean", "peer", "identity", "airy", "mayor", "--source", "talk", "--id", "U2"), "yardmaster")
	intakeHold := y.ask(t, listAsk("mckean", "peer", "intake", "airy", "--depot", "web", "--flow", "incoming"), "yardmaster")
	if _, err := y.SetPeerIdentity(y.ctx, "airy", PeerIdentity{Source: "talk", Crew: "mayor", ID: "U9"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	var identityTold, intakeTold bool
	for _, e := range peerTells(t, y) {
		identityTold = identityTold || toldSeq(e) == identityHold
		intakeTold = intakeTold || toldSeq(e) == intakeHold
		if toldSeq(e) == identityHold && e.Data["seen"] != "peer airy mayor in talk" {
			t.Errorf("the identity tell names %v, want peer airy mayor in talk", e.Data["seen"])
		}
	}
	if !identityTold || intakeTold {
		t.Fatalf("the identity write told identity %v and intake %v, want the identity hold alone", identityTold, intakeTold)
	}
	// Moving talk's records into chat moves the identity with them: a
	// fresh hold that read mayor in talk is told by the fold.
	movedHold := y.ask(t, listAsk("mckean", "peer", "identity", "airy", "mayor", "--source", "talk", "--id", "U10"), "yardmaster")
	if _, err := y.PutSource(y.ctx, "chat.yaml", strings.Replace(talkFile, "name: talk", "name: chat", 1), "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.MoveSourceRecords(y.ctx, "talk", "chat", "mckean"); err != nil {
		t.Fatal(err)
	}
	var moved int
	for _, e := range peerTells(t, y) {
		if toldSeq(e) == movedHold && e.Data["seen"] == "peer airy mayor in talk" {
			moved++
		}
	}
	if moved != 1 {
		t.Fatalf("the record move told the fresh identity hold %d times, want once naming peer airy mayor in talk", moved)
	}
	if _, err := y.SetPeerIntake(y.ctx, "airy", &PeerIntake{Depot: "web", Flow: "incoming", Limit: 3}, "mckean"); err != nil {
		t.Fatal(err)
	}
	var intakeAdded int
	for _, e := range peerTells(t, y) {
		if toldSeq(e) == intakeHold && e.Data["seen"] == "peer airy intakes" {
			intakeAdded++
		}
	}
	if intakeAdded != 1 {
		t.Fatalf("adding the intake told its absence hold %d times, want once", intakeAdded)
	}
	removeHold := y.ask(t, runAsk("peer intake airy --depot web --flow incoming --limit 5"), "yardmaster")
	if _, err := y.RemovePeerIntake(y.ctx, "airy", "web", "mckean"); err != nil {
		t.Fatal(err)
	}
	var intakeRemoved int
	for _, e := range peerTells(t, y) {
		if toldSeq(e) == removeHold && e.Data["seen"] == "peer airy intakes" {
			intakeRemoved++
		}
	}
	if intakeRemoved != 1 {
		t.Fatalf("removing the intake told its change hold %d times, want once", intakeRemoved)
	}
	removeIdentityHold := y.ask(t, runAsk("peer identity airy mayor --source chat --id U11"), "yardmaster")
	if _, err := y.RemovePeerIdentity(y.ctx, "airy", "mayor", "chat", "mckean"); err != nil {
		t.Fatal(err)
	}
	var identityRemoved int
	for _, e := range peerTells(t, y) {
		if toldSeq(e) == removeIdentityHold && e.Data["seen"] == "peer airy mayor in chat" {
			identityRemoved++
		}
	}
	if identityRemoved != 1 {
		t.Fatalf("removing the identity told its change hold %d times, want once", identityRemoved)
	}
	// Cursor writes never tell: a message taken from the peer and what
	// it says of its build move no fingerprint a hold carries.
	y2, key := runYard(t)
	hold := y2.ask(t, listAsk("mckean", "peer", "allow", "airy", "--receive", "bead"), "yardmaster")
	if _, err := y2.PeerReceive(y2.ctx, "airy", key, []PeerMessage{{ID: "m1", Kind: PeerPing, Body: json.RawMessage("{}")}}); err != nil {
		t.Fatal(err)
	}
	if err := y2.PeerHeard(y2.ctx, "airy", key, Says("v1")); err != nil {
		t.Fatal(err)
	}
	if got := peerTells(t, y2); len(got) != 0 {
		t.Fatalf("cursor writes told the hold: %v", got)
	}
	if _, err := y2.ApproveAsk(y2.ctx, y2.bead, hold, "mckean", true, nil); err != nil {
		t.Fatalf("approving after the cursor writes: %v", err)
	}
	// The roster still has mckean, so trust stays; its role changing from
	// user to crew removes reach and tells a hold that read the reach map.
	says := Says("v1")
	says.Roster = []PeerRosterEntry{
		{Name: "ada", Kind: RosterUser, Role: "user", Mail: true},
		{Name: "mckean", Kind: RosterUser, Role: "user", Mail: true},
	}
	if err := y2.PeerHeard(y2.ctx, "airy", key, says); err != nil {
		t.Fatal(err)
	}
	if _, err := y2.AllowPeer(y2.ctx, "airy", PeerAllow{Trust: map[string]string{"ada": "ada", "mckean": "mckean"}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y2.AllowPeer(y2.ctx, "airy", PeerAllow{Reach: map[string]string{"mckean": "mckean"}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	reachHold := y2.ask(t, listAsk("mckean", "peer", "allow", "airy", "--reach", "ada@airy=ada"), "yardmaster")
	changed := Says("v2")
	changed.Roster = []PeerRosterEntry{
		{Name: "ada", Kind: RosterUser, Role: "user", Mail: true},
		{Name: "mckean", Kind: RosterCrew, Role: "agent", Mail: true},
	}
	if err := y2.PeerHeard(y2.ctx, "airy", key, changed); err != nil {
		t.Fatal(err)
	}
	got := peerTells(t, y2)
	if len(got) != 1 || toldSeq(got[0]) != reachHold || got[0].Data["seen"] != "peer airy reaches" {
		t.Fatalf("the roster change's tells = %v, want one ask %d naming peer airy reaches", got, reachHold)
	}
	p := y2.peerOf(t, "airy")
	if len(p.Trust) != 2 || len(p.Reach) != 0 {
		t.Fatalf("the roster change left trust %v and reach %v, want trust intact and reach removed", p.Trust, p.Reach)
	}
}

// Removing a trusted user tells trust holds, and removal of a user exposed
// and trusted on the same link tells each affected ask once.
func TestPeerUntrustOnUserRemovalTellsAsker(t *testing.T) {
	t.Parallel()
	y := peerRuleYard(t)
	key := y.peerOf(t, "airy").Key
	says := Says("v1")
	says.Roster = []PeerRosterEntry{{Name: "mckean", Kind: RosterUser, Role: "user", Mail: true}}
	if err := y.PeerHeard(y.ctx, "airy", key, says); err != nil {
		t.Fatal(err)
	}
	if _, err := y.AllowPeer(y.ctx, "airy", PeerAllow{Trust: map[string]string{"mckean": "ada"}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	hold := y.ask(t, listAsk("mckean", "peer", "allow", "airy", "--trust", "mckean@airy=mckean"), "yardmaster")
	if err := y.DeleteUser(y.ctx, "ada", "mckean"); err != nil {
		t.Fatal(err)
	}
	got := peerTells(t, y)
	if len(got) != 1 || toldSeq(got[0]) != hold || got[0].Data["seen"] != "peer airy trusts" {
		t.Fatalf("the user removal's tells = %v, want one of ask %d naming peer airy trusts", got, hold)
	}
	y2, _ := runYard(t)
	key2 := y2.peerOf(t, "airy").Key
	says2 := Says("v1")
	says2.Roster = []PeerRosterEntry{{Name: "mckean", Kind: RosterUser, Role: "user", Mail: true}}
	if err := y2.PeerHeard(y2.ctx, "airy", key2, says2); err != nil {
		t.Fatal(err)
	}
	if _, err := y2.AllowPeer(y2.ctx, "airy", PeerAllow{Expose: []string{"ada"}, Trust: map[string]string{"mckean": "ada"}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	trustHold := y2.ask(t, listAsk("mckean", "peer", "allow", "airy", "--trust", "mckean@airy=mckean"), "yardmaster")
	exposeHold := y2.ask(t, listAsk("mckean", "peer", "allow", "airy", "--expose", "mckean"), "yardmaster")
	if err := y2.DeleteUser(y2.ctx, "ada", "mckean"); err != nil {
		t.Fatal(err)
	}
	got = peerTells(t, y2)
	seen := map[uint64]string{}
	for _, e := range got {
		if _, duplicate := seen[toldSeq(e)]; duplicate {
			t.Errorf("ask %d was told more than once: %v", toldSeq(e), e.Data)
		}
		seen[toldSeq(e)] = fmt.Sprint(e.Data["seen"])
	}
	if len(got) != 2 || seen[trustHold] != "peer airy exposes" || seen[exposeHold] != "peer airy exposes" {
		t.Fatalf("the user removal's tells = %v, want one exposure tell for each ask", got)
	}
}

// Renaming a peer tells the grant holds of the groups that send to it,
// whose config whole names the peer, through the rename that rewrote them.
func TestPeerRenameTellsSendingGroupHolds(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	if _, err := y.AllowPeer(y.ctx, "airy", PeerAllow{Send: []string{PeerBead}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if err := y.PutGroup(y.ctx, Group{Name: "senders", Runner: PeerRunner, Config: map[string]any{GroupPeer: "airy"}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	grant := y.ask(t, listAsk("mckean", "source", "grant", "crm", "read", "--group=senders"), "yardmaster")
	control := y.ask(t, listAsk("mckean", "group", "set", "builders", "cmd=./deploy.sh"), "yardmaster")
	if _, err := y.RenamePeer(y.ctx, "airy", "airy2", "mckean"); err != nil {
		t.Fatal(err)
	}
	evs, err := y.Store.Events(y.ctx, y.bead, 100)
	if err != nil {
		t.Fatal(err)
	}
	var grantTold, controlTold bool
	for _, e := range evs {
		if e.Kind != "ask_stale" {
			continue
		}
		switch n := e.Data["ask"].(type) {
		case float64:
			grantTold = grantTold || uint64(n) == grant
			controlTold = controlTold || uint64(n) == control
		case uint64:
			grantTold = grantTold || n == grant
			controlTold = controlTold || n == control
		}
		if e.Data["name"] == "senders" {
			if got := e.Data["kind"]; got != "group" {
				t.Errorf("the peer rename's tell names kind %v, want group", got)
			}
		}
	}
	if !grantTold || controlTold {
		t.Fatalf("the peer rename told grant %v and control %v, want the grant alone", grantTold, controlTold)
	}
}

// crewTells are the ask_stale events on y's bead, as peerTells reads them.
func crewTells(t *testing.T, y *askYard) []Event {
	t.Helper()
	return peerTells(t, y)
}

// Changing a crew member tells the askers of the open and put-aside run
// asks naming it, once each, in words naming the ask and the member;
// changing an unrelated member tells nothing, and a second change while
// the asks stand tells nothing more. Taken back and asked again, the
// new ask is covered, and a fresh ask taken back before the change is
// not.
func TestCrewChangeTellsCrewAskerOfStaleRunAsk(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	open := y.ask(t, listAsk("mckean", "crew", "set", "yardmaster", "preset=bypass"), "yardmaster")
	aside := y.ask(t, listAsk("mckean", "crew", "set", "yardmaster", "preset=accept-edits"), "yardmaster")
	if page, err := y.Store.Inbox(y.ctx, "yardmaster", 0); err != nil || page.Total != 0 {
		t.Fatalf("the asker's inbox after asking = %+v, %v, want empty", page, err)
	}
	y.answer(t, Answer{Ask: aside, Later: true}, "mckean")
	// An unrelated member comes and changes without a tell.
	if _, err := y.PutCrew(y.ctx, CrewMember{Name: "clerk", Runner: "herdr", Dir: "/yard", Config: map[string]any{"kind": "claude"}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.PutCrew(y.ctx, CrewMember{Name: "clerk", Runner: "herdr", Dir: "/yard", Config: map[string]any{"kind": "claude", "model": "opus"}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if got := crewTells(t, y); len(got) != 0 {
		t.Fatalf("the bead holds %d tells after an unrelated change, want none", len(got))
	}
	// Changing the named member tells the open ask and the aside one.
	y.crew(t, map[string]any{"kind": "claude", "args": []string{"--safe"}})
	page, err := y.Store.Inbox(y.ctx, "yardmaster", 0)
	if err != nil {
		t.Fatal(err)
	}
	// The inbox holds the aside answer beside the two tells.
	if page.Total != 3 {
		t.Fatalf("the asker's inbox = %+v, want the aside answer and two tells", page)
	}
	told := map[uint64]bool{}
	for _, e := range crewTells(t, y) {
		if got := Reason(e, "yardmaster"); got != ReasonAnswered {
			t.Fatalf("the asker heard %q, want %q", got, ReasonAnswered)
		}
		if got := e.Data["kind"]; got != "crew" {
			t.Errorf("the tell names kind %v, want crew", got)
		}
		if got := e.Data["name"]; got != "yardmaster" {
			t.Errorf("the tell names name %v, want yardmaster", got)
		}
		if got := e.Data["seen"]; got != "crew yardmaster" {
			t.Errorf("the tell names digest %v, want crew yardmaster", got)
		}
		if _, has := e.Data["source"]; has {
			t.Errorf("the crew tell names a source: %v", e.Data)
		}
		seq := toldSeq(e)
		told[seq] = true
		line, _ := e.Data["answer"].(string)
		for _, want := range []string{y.bead, fmt.Sprint(seq), "yardmaster", "crew yardmaster"} {
			if !strings.Contains(line, want) {
				t.Errorf("the line has no %q:\n%s", want, line)
			}
		}
	}
	if !told[open] || !told[aside] {
		t.Fatalf("the tells name %v, want the open ask %d and the aside ask %d", told, open, aside)
	}
	// A second change while the asks stand tells nothing more.
	y.crew(t, map[string]any{"kind": "claude", "args": []string{"--safer"}})
	if got := crewTells(t, y); len(got) != 2 {
		t.Fatalf("the bead holds %d tells after a second change, want the two", len(got))
	}
	// The open ask is indeed stale: its approval is refused, running nothing.
	if _, err := y.ApproveAsk(y.ctx, y.bead, open, "mckean", true, nil); err == nil || !strings.Contains(err.Error(), "Nothing ran") {
		t.Fatalf("approving the stale ask = %v, want a refusal that ran nothing", err)
	}
	// Taken back and asked again, the new ask is covered.
	if err := y.TakeBackAsk(y.ctx, y.bead, open, "asking again", "yardmaster"); err != nil {
		t.Fatal(err)
	}
	again := y.ask(t, listAsk("mckean", "crew", "set", "yardmaster", "preset=bypass"), "yardmaster")
	y.crew(t, map[string]any{"kind": "claude", "args": []string{"--safe"}})
	if got := crewTells(t, y); len(got) != 3 || toldSeq(got[2]) != again {
		t.Fatalf("the bead holds %v after the re-ask changed, want a third tell of ask %d", got, again)
	}
	// A fresh ask taken back before the next change is not told of it.
	gone := y.ask(t, listAsk("mckean", "crew", "set", "yardmaster", "preset=bypass"), "yardmaster")
	if err := y.TakeBackAsk(y.ctx, y.bead, gone, "no longer wanted", "yardmaster"); err != nil {
		t.Fatal(err)
	}
	y.crew(t, map[string]any{"kind": "claude", "args": []string{"--safer"}})
	if got := crewTells(t, y); len(got) != 3 {
		t.Fatalf("the bead holds %d tells after the taken-back ask's change, want the three", len(got))
	}
}

// A user who asked about a crew member is told in its notifications, with
// the same line; the crew member who did not ask hears nothing.
func TestCrewChangeTellsUserAskerOfStaleRunAsk(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	seq := y.ask(t, listAsk("mckean", "crew", "set", "yardmaster", "preset=bypass"), "ada")
	y.crew(t, map[string]any{"kind": "claude", "args": []string{"--safe"}})
	var found bool
	for _, e := range crewTells(t, y) {
		if Notified(e, "ada") != ReasonAnswered {
			continue
		}
		found = true
		line, _ := e.Data["answer"].(string)
		for _, want := range []string{y.bead, fmt.Sprint(seq), "yardmaster"} {
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
	for _, want := range []string{y.bead, fmt.Sprint(seq), "yardmaster"} {
		if !strings.Contains(text, want) {
			t.Errorf("the notification has no %q:\n%s", want, text)
		}
	}
	if page, err := y.Store.Inbox(y.ctx, "yardmaster", 0); err != nil || page.Total != 0 {
		t.Fatalf("the yardmaster's inbox = %+v, %v, want empty", page, err)
	}
}

// A crew write tells only the holds whose own reading of the member it
// moved: an identity change tells an identity hold and leaves crew set
// and grant holds standing; the crew set approval's own write then tells
// the other crew set hold and not the ask it approves.
func TestCrewWriteTellsOnlyTheHoldsWhoseReadingMoved(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	if err := y.SetIdentity(y.ctx, "yardmaster", "crm", Identity{ID: "u1"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	setHold := y.ask(t, listAsk("mckean", "crew", "set", "yardmaster", "preset=bypass"), "yardmaster")
	grantHold := y.ask(t, listAsk("mckean", "source", "grant", "crm", "read", "--crew=yardmaster"), "yardmaster")
	identityHold := y.ask(t, listAsk("mckean", "crew", "identity", "yardmaster", "--source", "crm", "--id", "u2"), "yardmaster")
	second := y.ask(t, listAsk("mckean", "crew", "set", "yardmaster", "preset=accept-edits"), "yardmaster")
	if err := y.SetIdentity(y.ctx, "yardmaster", "crm", Identity{ID: "u9"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	var setTold, grantTold, identityTold, secondTold bool
	for _, e := range crewTells(t, y) {
		setTold = setTold || toldSeq(e) == setHold
		grantTold = grantTold || toldSeq(e) == grantHold
		identityTold = identityTold || toldSeq(e) == identityHold
		secondTold = secondTold || toldSeq(e) == second
		if toldSeq(e) == identityHold && e.Data["seen"] != "crew yardmaster" {
			t.Errorf("the identity tell names %v, want crew yardmaster", e.Data["seen"])
		}
	}
	if setTold || grantTold || !identityTold || secondTold {
		t.Fatalf("the identity write told set %v, grant %v, identity %v and second %v, want the identity hold alone", setTold, grantTold, identityTold, secondTold)
	}
	// The standing grant hold still approves, and the standing crew set
	// hold approves to the member its card shows.
	if _, err := y.ApproveAsk(y.ctx, y.bead, grantHold, "mckean", false, nil); err != nil {
		t.Fatalf("approving the standing grant hold: %v", err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, setHold, "mckean", true, nil); err != nil {
		t.Fatalf("approving the standing crew set hold: %v", err)
	}
	// The approval's own member write answers the crew set hold; it must
	// not also tell the asker that the hold it just approved went stale,
	// while the other crew set hold is told.
	var selfTold bool
	secondTold = false
	for _, e := range crewTells(t, y) {
		selfTold = selfTold || toldSeq(e) == setHold
		secondTold = secondTold || toldSeq(e) == second
	}
	if selfTold || !secondTold {
		t.Fatalf("the approval told itself %v and the other hold %v, want the other alone", selfTold, secondTold)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, second, "mckean", true, nil); err == nil || !strings.Contains(err.Error(), "Nothing ran") {
		t.Fatalf("approving the told crew set hold = %v, want a refusal that ran nothing", err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, identityHold, "mckean", false, nil); err == nil || !strings.Contains(err.Error(), "Nothing ran") {
		t.Fatalf("approving the told identity hold = %v, want a refusal that ran nothing", err)
	}
	// Changing model alone tells a crew set hold that sets model and leaves
	// one setting preset alone standing (the card's four combinations).
	presetOnly := y.ask(t, listAsk("mckean", "crew", "set", "yardmaster", "preset=accept-edits"), "yardmaster")
	withModel := y.ask(t, listAsk("mckean", "crew", "set", "yardmaster", "model=opus", "preset=accept-edits"), "yardmaster")
	cur, err := y.GetCrew(y.ctx, "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	cur.Config["model"] = "sonnet"
	if _, err := y.PutCrew(y.ctx, *cur, "mckean"); err != nil {
		t.Fatal(err)
	}
	var presetTold, modelTold bool
	for _, e := range crewTells(t, y) {
		presetTold = presetTold || toldSeq(e) == presetOnly
		modelTold = modelTold || toldSeq(e) == withModel
	}
	if presetTold || !modelTold {
		t.Fatalf("the model write told preset %v and model %v, want the model hold alone", presetTold, modelTold)
	}
}

// Adding a crew member where none stood tells a hold written against its
// absence, and removing a member tells a fresh hold of it.
func TestCrewAbsentCreateAndDeleteTellAsker(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	pending := y.ask(t, listAsk("mckean", "crew", "set", "yardmaster", "preset=bypass"), "yardmaster")
	// A hold against the new member's absence, as no card files one today:
	// its Seen is rewritten to the absence the nil rule names, so the
	// creation's tell has a hold to reach.
	err := y.write(func(w *wtx) error {
		row, err := w.loadBead(y.bead)
		if err != nil {
			return err
		}
		var n Note
		if ok, err := get(w.Tx, bNotes, join(row.UUID, seqKey(pending)), &n); err != nil || !ok {
			if err == nil {
				err = fmt.Errorf("no note %d", pending)
			}
			return err
		}
		ask, err := ParseAsk(n)
		if err != nil {
			return err
		}
		ask.Run.Seen = map[string]string{"crew newhire": seen("no crew", "newhire")}
		text, err := json.Marshal(ask)
		if err != nil {
			return err
		}
		n.Text = string(text)
		return put(w.Tx, bNotes, join(row.UUID, seqKey(pending)), n)
	})
	if err != nil {
		t.Fatalf("rewriting the absence hold: %v", err)
	}
	if _, err := y.PutCrew(y.ctx, CrewMember{Name: "newhire", Runner: "herdr", Dir: "/yard", Config: map[string]any{"kind": "claude"}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if got := crewTells(t, y); len(got) != 1 || toldSeq(got[0]) != pending || got[0].Data["name"] != "newhire" || got[0].Data["seen"] != "crew newhire" {
		t.Fatalf("the absent-create tells = %v, want one of ask %d for newhire", got, pending)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, pending, "mckean", true, nil); err == nil || !strings.Contains(err.Error(), "Nothing ran") {
		t.Fatalf("approving the told hold = %v, want a refusal that ran nothing", err)
	}
	// Removing the member tells both a fresh grant hold and an open identity
	// hold for a source where the member held no identity yet. Done from a
	// crew pane with a spoofed actor, the tell records the pane's resolved
	// actor, matching crew_deleted.
	fresh := y.ask(t, listAsk("mckean", "source", "grant", "crm", "read", "--crew=newhire"), "yardmaster")
	noID := y.ask(t, listAsk("mckean", "crew", "identity", "newhire", "--source", "crm", "--id", "u1"), "ada")
	if err := y.CrewStarted(y.ctx, "yardmaster", map[string]any{"shell_pid": os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	if err := y.DeleteCrew(WithCaller(y.ctx, os.Getpid()), "newhire", "", "mckean"); err != nil {
		t.Fatal(err)
	}
	var freshTold, noIDTold int
	for _, e := range crewTells(t, y) {
		if e.Data["name"] != "newhire" {
			continue
		}
		if toldSeq(e) == fresh {
			freshTold++
			if e.Actor != "yardmaster" {
				t.Errorf("the removal tell actor = %q, want resolved pane actor yardmaster", e.Actor)
			}
		}
		if toldSeq(e) == noID {
			noIDTold++
			if e.Actor != "yardmaster" {
				t.Errorf("the no-identity removal tell actor = %q, want resolved pane actor yardmaster", e.Actor)
			}
			if Notified(e, "ada") != ReasonAnswered {
				t.Errorf("ada was not notified of the no-identity removal tell: %v", e.Data)
			}
		}
	}
	if freshTold != 1 || noIDTold != 1 {
		t.Fatalf("the removal told grant %d times and no-identity %d times, want once each", freshTold, noIDTold)
	}
}

// Renaming or removing a peer tells the grant holds of the crew members
// that may mail it, whose config whole names the peer, through the rename
// or removal that rewrote them; a crew set hold, whose reading names none
// of it, is not told.
func TestPeerRenameTellsCrewGrantHold(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	y.crew(t, map[string]any{"kind": "claude", "args": []string{"--careful"}, "peers": "airy"})
	grant := y.ask(t, listAsk("mckean", "source", "grant", "crm", "read", "--crew=yardmaster"), "yardmaster")
	set := y.ask(t, listAsk("mckean", "crew", "set", "yardmaster", "preset=bypass"), "yardmaster")
	if _, err := y.RenamePeer(y.ctx, "airy", "east", "mckean"); err != nil {
		t.Fatal(err)
	}
	var grantTold, setTold bool
	for _, e := range crewTells(t, y) {
		if e.Data["kind"] != "crew" {
			continue
		}
		grantTold = grantTold || toldSeq(e) == grant
		setTold = setTold || toldSeq(e) == set
		if toldSeq(e) == grant && e.Data["seen"] != "crew yardmaster" {
			t.Errorf("the rename's crew tell names %v, want crew yardmaster", e.Data["seen"])
		}
	}
	if !grantTold || setTold {
		t.Fatalf("the peer rename told grant %v and set %v, want the grant alone", grantTold, setTold)
	}
	// Removing the renamed peer drops it from the member's config and tells
	// a fresh grant hold alone too.
	grant2 := y.ask(t, listAsk("mckean", "source", "grant", "crm", "read", "--crew=yardmaster"), "yardmaster")
	set2 := y.ask(t, listAsk("mckean", "crew", "set", "yardmaster", "preset=accept-edits"), "yardmaster")
	if err := y.DeletePeer(y.ctx, "east", "mckean"); err != nil {
		t.Fatal(err)
	}
	var grant2Told, set2Told bool
	for _, e := range crewTells(t, y) {
		if e.Data["kind"] != "crew" {
			continue
		}
		grant2Told = grant2Told || toldSeq(e) == grant2
		set2Told = set2Told || toldSeq(e) == set2
	}
	if !grant2Told || set2Told {
		t.Fatalf("the peer removal told grant %v and set %v, want the grant alone", grant2Told, set2Told)
	}
}

// Moving a source's records tells a grant hold of a member whose grants
// the fold rewrote, from the config whole it read. An identity the fold
// moves stands under its new source with the fingerprint the hold
// already carries, so no set crossing tells the identity hold, whose
// approval the card's own re-reading still refuses; a crew set hold,
// whose reading names neither, is not told.
func TestSourceMoveTellsCrewGrantHold(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	if err := y.SetIdentity(y.ctx, "yardmaster", "crm", Identity{ID: "u1"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	y.crew(t, map[string]any{"kind": "claude", "args": []string{"--careful"}, "open_threads": "crm"})
	grantHold := y.ask(t, listAsk("mckean", "source", "grant", "crm", "read", "--crew=yardmaster"), "yardmaster")
	identityHold := y.ask(t, listAsk("mckean", "crew", "identity", "yardmaster", "--source", "crm", "--id", "u2"), "yardmaster")
	setHold := y.ask(t, listAsk("mckean", "crew", "set", "yardmaster", "preset=bypass"), "yardmaster")
	if _, err := y.PutSource(y.ctx, "chat.yaml", strings.Replace(runCRM, "name: crm", "name: chat", 1), "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.MoveSourceRecords(y.ctx, "crm", "chat", "mckean"); err != nil {
		t.Fatal(err)
	}
	var grantTold, identityTold, setTold bool
	for _, e := range crewTells(t, y) {
		grantTold = grantTold || toldSeq(e) == grantHold
		identityTold = identityTold || toldSeq(e) == identityHold
		setTold = setTold || toldSeq(e) == setHold
	}
	if !grantTold || identityTold || setTold {
		t.Fatalf("the record move told grant %v, identity %v and set %v, want the grant hold alone", grantTold, identityTold, setTold)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, grantHold, "mckean", false, nil); err == nil || !strings.Contains(err.Error(), "Nothing ran") {
		t.Fatalf("approving the told grant hold = %v, want a refusal that ran nothing", err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, identityHold, "mckean", false, nil); err == nil || !strings.Contains(err.Error(), "Nothing ran") {
		t.Fatalf("approving the moved identity hold = %v, want a refusal that ran nothing", err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, setHold, "mckean", true, nil); err != nil {
		t.Fatalf("approving the standing crew set hold: %v", err)
	}
}

// Session, subscription and group-rename writes never tell a crew hold:
// a restart writes the session's runtime alone, enabling writes nothing
// a reading names, subscriptions are read by no crew fingerprint, and a
// group rename moves only a member's subscriptions. A restart hold is
// never told either: its session keys are no crew tell's business.
func TestSessionSubscriptionAndRenameWritesNeverTellCrewHold(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	setHold := y.ask(t, listAsk("mckean", "crew", "set", "yardmaster", "preset=bypass"), "yardmaster")
	addAsk := listAsk("mckean", "crew", "add", "porter", "kind=claude", "preset=bypass")
	addAsk.Run.Role = "You are the porter.\n"
	addHold := y.ask(t, addAsk, "yardmaster")
	restart := y.ask(t, listAsk("mckean", "crew", "restart", "yardmaster"), "ada")
	restart2 := y.ask(t, listAsk("mckean", "crew", "restart", "yardmaster"), "ada")
	if _, err := y.ApproveAsk(y.ctx, y.bead, restart, "mckean", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := y.SetCrewEnabled(y.ctx, "yardmaster", false, "", "mckean"); err != nil {
		t.Fatal(err)
	}
	if err := y.SetCrewEnabled(y.ctx, "yardmaster", true, "", "mckean"); err != nil {
		t.Fatal(err)
	}
	sub := Subscription{Source: SourceYardr, Scope: ScopeAll, Where: map[string]string{"group": "builders"}}
	if err := y.AddSubscriptionKind(y.ctx, "yardmaster", "crew", sub, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.RenameGroup(y.ctx, "builders", "makers", "mckean"); err != nil {
		t.Fatal(err)
	}
	if got := crewTells(t, y); len(got) != 0 {
		t.Fatalf("session, subscription or rename writes told the holds: %v", got)
	}
	// The crew hold still approves, and its own write tells neither the
	// restart hold nor the crew add preset hold: those keys stand apart from
	// a member's definition.
	if _, err := y.ApproveAsk(y.ctx, y.bead, setHold, "mckean", true, nil); err != nil {
		t.Fatalf("approving after the session and rename writes: %v", err)
	}
	for _, e := range crewTells(t, y) {
		if toldSeq(e) == restart2 || toldSeq(e) == addHold {
			t.Fatalf("a definition write told a non-member hold: %v", e.Data)
		}
	}
	if got := crewTells(t, y); len(got) != 0 {
		t.Fatalf("the crew set approval told a hold: %v", got)
	}
}

// Allowing a member to open threads and taking a served user out of its
// config each tell a grant hold of the member, whose reading is its
// config whole.
func TestAllowOpeningAndDropLoginTellCrewHold(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	grant := y.ask(t, listAsk("mckean", "source", "grant", "crm", "read", "--crew=yardmaster"), "yardmaster")
	if _, err := y.AllowOpening(y.ctx, "yardmaster", []string{"crm"}, "", "mckean"); err != nil {
		t.Fatal(err)
	}
	if got := crewTells(t, y); len(got) != 1 || toldSeq(got[0]) != grant {
		t.Fatalf("allowing opening told %v, want one tell of ask %d", got, grant)
	}
	// The member serves ada, and removing ada unserves her in the write
	// that removes the user: a fresh grant hold is told of it.
	def, err := y.GetCrew(y.ctx, "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	def.Config["for"] = "ada"
	def.Config["env_for_ada"] = "/yard/ada.env"
	if _, err := y.PutCrew(y.ctx, *def, "mckean"); err != nil {
		t.Fatal(err)
	}
	fresh := y.ask(t, listAsk("mckean", "source", "grant", "crm", "read", "--crew=yardmaster"), "yardmaster")
	if err := y.DeleteUser(y.ctx, "ada", "mckean"); err != nil {
		t.Fatal(err)
	}
	got := crewTells(t, y)
	if len(got) != 2 || toldSeq(got[1]) != fresh {
		t.Fatalf("the user removal's tells = %v, want a second tell of ask %d", got, fresh)
	}
}
