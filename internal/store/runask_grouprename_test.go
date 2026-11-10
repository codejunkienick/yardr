package store

import (
	"slices"
	"strings"
	"testing"
)

// A rename is asked in the yard's form, and its card names what follows the
// name, as the rename reads it from the yard's records: the routes, the beads
// that go on, and what else is moved, each only where there is some. It counts
// no session, since they start and end by the minute, so one that starts after
// the card was written leaves it as it was and the approval stands. Approving
// renames the group as the operator does, logs the one event with the ask it
// was approved on, and answers what the command prints. The card's way back is
// the reverse rename, unless the old name is one pack imports give a depot's
// groups, which no group may be renamed to: nobody can take that back.
func TestRunAskGroupRename(t *testing.T) {
	t.Parallel()
	y, ids := renameYard(t)
	if err := y.PutGroup(y.ctx, Group{Name: "web-assembly", Runner: "exec", Config: map[string]any{"cmd": "true"}}, "mckean"); err != nil {
		t.Fatal(err)
	}

	seq := y.ask(t, listAsk("mckean", "yardr group rename builders makers"), "yardmaster")
	run := y.asked(t, seq).Ask.Run
	if want := []string{"group", "rename", "builders", "makers"}; !slices.Equal(run.Argv, want) {
		t.Fatalf("the ask holds %q, want %q", run.Argv, want)
	}
	if run.Description != groupRenameDescription {
		t.Errorf("the card describes the command as %q", run.Description)
	}
	want := []string{
		"the group builders (runner herdr) is called makers from now on: its settings, members, limit and subscriptions stay as they are.",
		"the routes that name it, as their first group or after it, name makers: " + strings.Join(slices.Sorted(slices.Values(ids)), ", ") + ". Their conditions stay.",
		"the arrival estimates from group=builders stand under group=makers.",
		"The beads it owns and their live sessions go on under the new name: none is released, and no session or pane is touched. A session running now keeps the old name in its environment (YARDR_GROUP) until it ends.",
		"The old name is free afterwards and reaches nothing, and the log and the notes keep it: what names it outside the yard's records, a script or a prompt, is for the operator to change.",
	}
	if !slices.Equal(run.Does, want) {
		t.Errorf("the card says:\n%s\nwant:\n%s", strings.Join(run.Does, "\n"), strings.Join(want, "\n"))
	}
	if run.Back != "yardr group rename makers builders" || run.BackBy != "only the operator" {
		t.Errorf("the way back is %q by %q", run.Back, run.BackBy)
	}

	// A session that starts after the card was written changes nothing it says.
	y.claim(t, "builders")
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(done.Result, "\n")
	if len(lines) != 4 || lines[0] != "group builders is makers now: the old name is free, and reaches nothing" ||
		lines[1] != "routes that name it now: "+strings.Join(slices.Sorted(slices.Values(ids)), ", ") ||
		lines[2] != "its arrival estimates from the old name stand under the new one" ||
		lines[3] != "its open beads (1) and live sessions (1) go on under the new name; none is released" {
		t.Errorf("the approval ran %q", done.Result)
	}
	if _, err := y.GetGroup(y.ctx, "makers"); err != nil {
		t.Errorf("makers after the approval = %v", err)
	}
	evs, err := y.EventsOfKind(y.ctx, "group_renamed", 10)
	if err != nil || len(evs) != 1 {
		t.Fatalf("group_renamed events = %d, %v", len(evs), err)
	}
	y.approved(t, evs[0], seq)

	// What a depot's imports call a group cannot be taken back by renaming.
	seq = y.ask(t, listAsk("mckean", "group rename web-assembly assembly"), "yardmaster")
	back := y.asked(t, seq).Ask.Run
	if want := "web-assembly is a name pack imports give the groups they make for depot web, so no group may be renamed to it"; back.Back != want || back.BackBy != "nobody" {
		t.Errorf("the way back of a group of the importer's namespace is %q by %q", back.Back, back.BackBy)
	}

	// The operator renames in a terminal, so the ask is no card for them.
	if _, err := y.AddAsk(y.ctx, y.bead, listAsk("mckean", "group rename makers east"), "mckean"); err == nil || !strings.Contains(err.Error(), "run it yourself") {
		t.Errorf("the operator's own ask = %v, want it refused as one they run themselves", err)
	}
}
