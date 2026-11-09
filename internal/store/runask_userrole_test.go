package store

import (
	"slices"
	"strings"
	"testing"
)

func userRoleAsk(command, to string) Ask {
	return Ask{Question: "Make ada an operator?", To: to, Context: []string{"ada runs the yard on weekdays"},
		Run: &AskRun{Argv: []string{command}}}
}

// user set has two rows, the tell's and the role's, and parseRow takes a
// command to the one whose form it is: tell= to the older row, role= to the
// newer, and a refusal in the words of the row that recognised the form.
func TestParseRowTellsTheFormsOfUserSetApart(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		line string
		use  string
		argv []string
		want string // a refusal, when the line is refused
	}{
		{line: "user set ada tell=asks,mail", use: "tell=<parts>", argv: []string{"user", "set", "ada", "tell=mail,asks"}},
		{line: "yardr user set ada role=operator", use: "role=operator|member", argv: []string{"user", "set", "ada", "role=operator"}},
		{line: "user set ada role=member", use: "role=operator|member", argv: []string{"user", "set", "ada", "role=member"}},
		{line: "user set ada role=chief", want: `role="chief": a user's role is operator or member`},
		{line: "user set role=operator", want: "one user and one role=, not 1 words"},
		{line: "user set ada role=operator tell=asks", want: "one user and one role=, not 3 words"},
		{line: "user set ada role=operator --force", want: "user set with --force is the operator's to type in a terminal"},
		{line: "user set role=operator ada", want: "the user first, then role=<role>"},
		// What is neither form is refused in the older row's words.
		{line: "user set ada notify=talk", want: "user set with notify= is typed by the operator"},
		{line: "user set ada colour=blue", want: `"colour=blue" is no tell=`},
	} {
		row, argv, err := parseRow([]string{c.line})
		if c.want != "" {
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("%q = %v, want a refusal that says %q", c.line, err, c.want)
			}
			continue
		}
		if err != nil || !strings.Contains(row.use, c.use) || !slices.Equal(argv, c.argv) {
			t.Errorf("%q = %v, %v, %v; want the row %q with %v", c.line, row, argv, err, c.use, c.argv)
		}
	}
}

// A role card is weighed trust, strong, whichever way the role goes. It
// says what the role lets whoever speaks as the user do, how many of their
// devices speak with it, and what takes it back; the operator's approval
// sets the role as the command does, and logs it with the ask it came from.
// Giving the role of a user with a device and taking it back are one card
// each.
func TestRunAskUserRole(t *testing.T) {
	t.Parallel()
	y, _ := runYard(t)
	if _, err := y.AddDevice(y.ctx, "ada", "phone", "mckean"); err != nil {
		t.Fatal(err)
	}
	seq := y.ask(t, userRoleAsk("yardr user set ada role=operator", "mckean"), "yardmaster")
	got := y.asked(t, seq)
	run := got.Ask.Run
	if want := []string{"user", "set", "ada", "role=operator"}; !slices.Equal(run.Argv, want) {
		t.Fatalf("the ask stores %v, want %v", run.Argv, want)
	}
	if run.Description != "Set where, how and what a source tells a user, or their role" {
		t.Errorf("description %q", run.Description)
	}
	joined := strings.Join(run.Does, "\n")
	for _, want := range []string{
		"makes the user ada an operator (now: a member).",
		"administers the yard at the shell: whoever speaks as ada does so from then on.",
		"Their 1 device speak with it from then on.",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the card does not say %q; it says %q", want, run.Does)
		}
	}
	if run.Back != "yardr user set ada role=member" || run.BackBy != "only the operator" {
		t.Errorf("the card is taken back by %q, %q", run.Back, run.BackBy)
	}
	if len(got.Ask.Weight) != 1 || got.Ask.Weight[0].Class != WeightTrust || !StrongWeight(got.Ask.Weight) {
		t.Fatalf("the ask weighs %+v, want trust, which is strong", got.Ask.Weight)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil); err == nil || !strings.Contains(err.Error(), "weighty (trust)") {
		t.Fatalf("approving without the weight seen = %v", err)
	}
	if u, err := y.GetUser(y.ctx, "ada"); err != nil || u.IsOperator() {
		t.Fatalf("the refused approval left ada %+v, %v", u, err)
	}
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if done.Result != "user ada: operator" {
		t.Fatalf("the approval recorded %q", done.Result)
	}
	if u, err := y.GetUser(y.ctx, "ada"); err != nil || !u.IsOperator() {
		t.Fatalf("ada holds %+v, %v", u, err)
	}
	evs, err := y.EventsOfKind(y.ctx, "user_set", 1)
	if err != nil || len(evs) != 1 || evs[0].Data["user"] != "ada" || evs[0].Data["role"] != RoleOperator {
		t.Fatalf("user_set events = %+v, %v", evs, err)
	}
	y.approved(t, evs[0], seq)

	// The way back is a card of the same row, which names who remains.
	seq = y.ask(t, userRoleAsk("user set ada role=member", "mckean"), "yardmaster")
	got = y.asked(t, seq)
	if joined := strings.Join(got.Ask.Run.Does, "\n"); !strings.Contains(joined, "makes the user ada a member (now: an operator).") ||
		!strings.Contains(joined, "The operators that remain are mckean.") {
		t.Errorf("the card says %q", got.Ask.Run.Does)
	}
	if len(got.Ask.Weight) != 1 || !StrongWeight(got.Ask.Weight) {
		t.Errorf("a demotion weighs %+v, want strong", got.Ask.Weight)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	if u, err := y.GetUser(y.ctx, "ada"); err != nil || u.IsOperator() {
		t.Fatalf("ada holds %+v after the card that makes her a member, %v", u, err)
	}
}

// A role ask is refused, storing nothing, for what the row refuses: a user
// the yard has not, the role the user has, a demotion the store refuses (the
// operator setting's user, and the last operator), a form the row does not
// take, the operator as the asker, and a member as the one asked.
func TestRunAskUserRoleRefusals(t *testing.T) {
	t.Parallel()
	y, _ := runYard(t)
	for _, c := range []struct {
		name, command, to, asker, want string
	}{
		{"no such user", "user set ghost role=operator", "mckean", "yardmaster", "user ghost: not found"},
		{"the role they have", "user set ada role=member", "mckean", "yardmaster", "ada is a member already, so the command would change nothing"},
		{"the operator setting's user", "user set mckean role=member", "mckean", "yardmaster", "user mckean is the yard's operator and is not made a member; name another operator first"},
		{"a role of nobody's", "user set ada role=chief", "mckean", "yardmaster", `role="chief": a user's role is operator or member`},
		{"with notify", "user set ada role=operator notify=pushover", "mckean", "yardmaster", "one user and one role=, not 3 words"},
		{"the operator asks", "user set ada role=operator", "mckean", "mckean", "needs no approval: run it yourself"},
		{"a member is asked", "user set ada role=operator", "ada", "yardmaster", "a command is the operator's to approve (mckean): the yard runs it as them, so ada cannot be asked to"},
		{"nobody", "user set ada role=operator", "mckean", "stranger", "a command for the operator to approve is proposed by a crew member or a user of the yard"},
	} {
		if _, err := y.AddAsk(y.ctx, y.bead, userRoleAsk(c.command, c.to), c.asker); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s = %v, want %q", c.name, err, c.want)
		}
	}
	// A yard's last operator is not made a member even where the operator
	// setting names no one: the role is what the yard holds.
	if _, err := y.SetUser(y.ctx, "ada", map[string]string{UserRole: RoleOperator}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if err := y.SetSetting(y.ctx, OperatorSetting, "ada", "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.AddAsk(y.ctx, y.bead, userRoleAsk("user set ada role=member", "mckean"), "yardmaster"); err == nil ||
		!strings.Contains(err.Error(), "user ada is the yard's operator and is not made a member") {
		t.Errorf("demoting the operator the setting names = %v", err)
	}
	if notes, err := y.Notes(y.ctx, y.bead); err != nil || len(notes) != 0 {
		t.Fatalf("the refused asks left %d notes, %v", len(notes), err)
	}
}

// An approval is held to the role the card saw: the role set since, by hand
// or on another card, leaves the ask stale, and a role already as proposed
// has nothing left to change. A demotion is also held to the operators that
// stand: another made since changes who remains, and the card says so.
func TestRunAskUserRoleStale(t *testing.T) {
	t.Parallel()
	y, _ := runYard(t)
	seq := y.ask(t, userRoleAsk("user set ada role=operator", "mckean"), "yardmaster")
	if _, err := y.AddUser(y.ctx, "eve", "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.SetUser(y.ctx, "ada", map[string]string{UserRole: RoleOperator}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil ||
		!strings.Contains(err.Error(), "was written when the role of ada was another") {
		t.Fatalf("approving a role set since = %v", err)
	}
	// The demotion card names who remains; another operator makes it stale.
	seq = y.ask(t, userRoleAsk("user set ada role=member", "mckean"), "yardmaster")
	if _, err := y.SetUser(y.ctx, "eve", map[string]string{UserRole: RoleOperator}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil ||
		!strings.Contains(err.Error(), "was written when the yard had other operators") {
		t.Fatalf("approving a demotion with another operator made since = %v", err)
	}
	if u, err := y.GetUser(y.ctx, "ada"); err != nil || !u.IsOperator() {
		t.Fatalf("the refused approval left ada %+v, %v", u, err)
	}
}
