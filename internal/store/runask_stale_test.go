package store

import (
	"encoding/json"
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
