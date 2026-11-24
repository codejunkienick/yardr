package cli

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/store"
)

// matrixYard is a yard with two crew members, a user (jo) and a manual
// group that share one subscription and differ in the others. clerk has
// the shared one through the group only, and yardmaster has held both in
// --on and as a subscription.
func matrixYard(t *testing.T) (text string, m subMatrix) {
	t.Helper()
	st := userYard(t)
	declareGitHubPack(t, st, "", "", "jo")
	// init made the yardmaster, with the default --on.
	mustAs(t, st, "jo", "crew", "set", "yardmaster", "--on", "gave_up,held")
	mustAs(t, st, "jo", "crew", "add", "clerk", "--set", "kind=claude", "--on", "held")
	mustAs(t, st, "jo", "group", "add", "approvers", "--runner", "manual", "--members", "jo,clerk")
	for _, args := range [][]string{
		{"yardmaster", "yardr", "--done"},
		{"jo", "yardr", "--done"},
		{"approvers", "yardr", "--done"},
		{"yardmaster", "github"},
		{"yardmaster", "yardr", "--where", "kind=held"},
		{"jo", "yardr", "--where", "kind=move_refused"},
		{"approvers", "yardr", "--where", "stage=approved"},
	} {
		mustAs(t, st, "jo", append([]string{"subscribe"}, args...)...)
	}
	text = mustAs(t, st, "jo", "subscribe")
	if err := json.Unmarshal([]byte(mustAs(t, st, "jo", "subscribe", "--json")), &m); err != nil {
		t.Fatalf("subscribe --json: %v", err)
	}
	return text, m
}

// Bare yardr subscribe lays every subscriber's --on kinds and subscriptions
// side by side: the row all of them have is marked in each column and
// counted, a row one of them has is marked once, and a kind a crew member
// has both ways says so (yardr-a3s2).
func TestSubscribeMatrix(t *testing.T) {
	t.Parallel()
	text, m := matrixYard(t)
	row := func(sub string) subRow {
		t.Helper()
		for _, r := range m.Rows {
			if r.Subscription.String() == sub {
				return r
			}
		}
		t.Fatalf("no row %q in:\n%s", sub, text)
		return subRow{}
	}
	marks := func(r subRow) string {
		var out []string
		for _, name := range sortedKeys(r.Marks) {
			out = append(out, name+"="+strings.Join(r.Marks[name], "+"))
		}
		return strings.Join(out, " ")
	}
	for _, tc := range []struct{ sub, marks, reaches string }{
		{"yardr all state=done", "approvers (group)=sub clerk=via approvers jo (user)=sub yardmaster=sub", "clerk,jo,yardmaster"},
		{"github involved", "yardmaster=sub", "yardmaster"},
		{"yardr all stage=approved", "approvers (group)=sub clerk=via approvers", "clerk"},
		{"yardr all kind=held", "clerk=on yardmaster=on+sub", "clerk,yardmaster"},
		{"yardr all kind=gave_up", "yardmaster=on", "yardmaster"},
		{"yardr all kind=move_refused", "jo (user)=sub", "jo"},
	} {
		r := row(tc.sub)
		if got := marks(r); got != tc.marks {
			t.Errorf("%s: marks %q, want %q", tc.sub, got, tc.marks)
		}
		if got := strings.Join(r.Reaches, ","); got != tc.reaches {
			t.Errorf("%s: reaches %q, want %q", tc.sub, got, tc.reaches)
		}
	}
	// The group's column says whose inbox it is: its members that are crew
	// members, so not jo.
	var cols []string
	for _, c := range m.Subscribers {
		cols = append(cols, c.String())
		if c.Name == "approvers" && !slices.Equal(c.Reaches, []string{"clerk"}) {
			t.Errorf("approvers reaches %v, want clerk", c.Reaches)
		}
	}
	if got := strings.Join(cols, ","); got != "clerk,yardmaster,jo (user),approvers (group)" {
		t.Errorf("columns %q", got)
	}
	// The text is the same data: the shared row with its count and a mark
	// in every column, in the order of the header.
	lines := map[string][]string{}
	for _, l := range strings.Split(text, "\n") {
		if f := strings.Split(l, "  "); len(f) > 1 {
			f = slices.DeleteFunc(f, func(s string) bool { return strings.TrimSpace(s) == "" })
			for i := range f {
				f[i] = strings.TrimSpace(f[i])
			}
			lines[f[0]] = f[1:]
		}
	}
	for sub, want := range map[string]string{
		"SUBSCRIPTION":             "REACHES|clerk|yardmaster|jo (user)|approvers (group)",
		"yardr all state=done":     "3|via approvers|sub|sub|sub",
		"github involved":          "1|-|sub|-|-",
		"yardr all kind=held":      "2|on|on+sub|-|-",
		"yardr all kind=gave_up":   "1|-|on|-|-",
		"yardr all stage=approved": "1|via approvers|-|-|sub",
	} {
		if got := strings.Join(lines[sub], "|"); got != want {
			t.Errorf("%s: %q, want %q in:\n%s", sub, got, want, text)
		}
	}
	if !strings.Contains(text, "approvers (group) reaches: clerk\n") {
		t.Errorf("no line for the group's crew members:\n%s", text)
	}
}

// The default --on kinds nobody has are listed as reaching no one: --on
// replaces the list, so a crew member given two kinds lost the rest. A kind
// someone has as a subscription instead is not among them (yardr-a3s2).
func TestSubscribeMatrixReachesNoOne(t *testing.T) {
	t.Parallel()
	text, m := matrixYard(t)
	var want []string
	for _, k := range store.AttentionKinds {
		// The operator's default subscription reaches the failure kinds.
		if !slices.Contains([]string{"gave_up", "held", "move_refused", store.InstallFailed, store.RolledBack}, k) {
			want = append(want, k)
		}
	}
	if !slices.Equal(m.ReachesNoOne, want) {
		t.Errorf("reaches no one = %v, want %v", m.ReachesNoOne, want)
	}
	if !strings.Contains(text, "): "+strings.Join(want, ", ")+"\n") || !strings.Contains(text, "reaches no one (") {
		t.Errorf("text does not list them:\n%s", text)
	}
}

// A subscription typed without its subscriber is refused, not answered with
// the view as if it had been made.
func TestSubscribeBareTakesNoFlags(t *testing.T) {
	t.Parallel()
	st := userYard(t)
	for _, args := range [][]string{{"--done"}, {"--source", "yardr"}, {"--where", "kind=held"}, {"--scope", "all"}, {"--crew"}, {"--group"}} {
		refusedAs(t, st, "jo", "name who subscribes", append([]string{"subscribe"}, args...)...)
	}
}

// A filtered receipt kind in --on keeps its mark but reaches no inbox: the
// matrix reports what forCrew matches. mail_read still reaches, as the
// released pack API promises.
func TestSubscribeMatrixFilteredReceipt(t *testing.T) {
	t.Parallel()
	crew := []store.CrewMember{{Name: "yardmaster", On: []string{"mail_read", "inbox_read", "inbox_shown"}}}
	m := subscriptionMatrix(crew, nil, nil)
	row := func(kind string) subRow {
		t.Helper()
		for _, r := range m.Rows {
			if r.Kind == kind {
				return r
			}
		}
		t.Fatalf("no row for kind %q", kind)
		return subRow{}
	}
	for _, kind := range []string{"mail_read", "inbox_read", "inbox_shown"} {
		if got := strings.Join(row(kind).Marks["yardmaster"], "+"); got != "on" {
			t.Errorf("%s: mark = %q, want on", kind, got)
		}
	}
	if r := row("mail_read"); !slices.Equal(r.Reaches, []string{"yardmaster"}) {
		t.Errorf("mail_read reaches %v, want [yardmaster]", r.Reaches)
	}
	for _, kind := range []string{"inbox_read", "inbox_shown"} {
		if r := row(kind); len(r.Reaches) != 0 {
			t.Errorf("%s reaches %v, want none", kind, r.Reaches)
		}
	}
}

// An explicit yardr subscription naming only a filtered receipt kind reaches
// no inbox either: those receipts are beadless and never match an explicit
// yardr subscription. The marks stay (on+sub, on+via); mail_read, the
// attention class and a filter naming another kind beside a receipt still
// reach, as before.
func TestSubscribeMatrixFilteredReceiptExplicit(t *testing.T) {
	t.Parallel()
	yardr := func(where map[string]string) store.Subscription {
		return store.Subscription{Source: store.SourceYardr, Scope: store.ScopeAll, Where: where}
	}
	kind := func(k string) map[string]string { return map[string]string{"kind": k} }
	crew := []store.CrewMember{{
		Name: "yardmaster", On: []string{"mail_read", "inbox_read", "inbox_shown"},
		Subscriptions: []store.Subscription{
			yardr(kind("mail_read")),
			yardr(kind("inbox_read")),
			yardr(kind(store.KindAttention)),
			yardr(kind(store.JoinWhere([]string{"inbox_read", "held"}))),
		},
	}}
	groups := []store.Group{{
		Name: "approvers", Runner: "manual", Members: []string{"yardmaster"},
		Subscriptions: []store.Subscription{yardr(kind("inbox_shown"))},
	}}
	m := subscriptionMatrix(crew, nil, groups)
	row := func(sub store.Subscription) subRow {
		t.Helper()
		for _, r := range m.Rows {
			if r.Subscription.String() == sub.String() {
				return r
			}
		}
		t.Fatalf("no row for %q", sub.String())
		return subRow{}
	}
	r := row(yardr(kind("inbox_read")))
	if got := strings.Join(r.Marks["yardmaster"], "+"); got != "on+sub" {
		t.Errorf("inbox_read marks = %q, want on+sub", got)
	}
	if len(r.Reaches) != 0 {
		t.Errorf("inbox_read on+sub reaches %v, want none", r.Reaches)
	}
	r = row(yardr(kind("inbox_shown")))
	if got := strings.Join(r.Marks["yardmaster"], "+"); got != "on+via approvers" {
		t.Errorf("inbox_shown marks = %q, want on+via approvers", got)
	}
	if got := strings.Join(r.Marks["approvers (group)"], "+"); got != "sub" {
		t.Errorf("inbox_shown group mark = %q, want sub", got)
	}
	if len(r.Reaches) != 0 {
		t.Errorf("inbox_shown on+via reaches %v, want none", r.Reaches)
	}
	r = row(yardr(kind("mail_read")))
	if got := strings.Join(r.Marks["yardmaster"], "+"); got != "on+sub" {
		t.Errorf("mail_read marks = %q, want on+sub", got)
	}
	if !slices.Equal(r.Reaches, []string{"yardmaster"}) {
		t.Errorf("mail_read reaches %v, want [yardmaster]", r.Reaches)
	}
	for _, sub := range []store.Subscription{
		yardr(kind(store.KindAttention)),
		yardr(kind(store.JoinWhere([]string{"inbox_read", "held"}))),
	} {
		if r := row(sub); !slices.Equal(r.Reaches, []string{"yardmaster"}) {
			t.Errorf("%s reaches %v, want [yardmaster]", sub.String(), r.Reaches)
		}
	}
}

// A group and crew member may share a name. Their marks must stay in their
// own columns even though the group also delivers to that crew member.
func TestSubscribeMatrixSameNameGroupAndCrew(t *testing.T) {
	t.Parallel()
	crew := []store.CrewMember{{Name: "reviewers"}}
	groups := []store.Group{{
		Name: "reviewers", Runner: "manual", Members: []string{"reviewers"},
		Subscriptions: []store.Subscription{{Source: store.SourceYardr, Scope: store.ScopeAll, Where: map[string]string{"kind": "held"}}},
	}}
	m := subscriptionMatrix(crew, nil, groups)
	if len(m.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(m.Rows))
	}
	row := m.Rows[0]
	if got := strings.Join(row.Marks["reviewers"], "+"); got != "via reviewers" {
		t.Errorf("crew column mark = %q, want via reviewers", got)
	}
	if got := strings.Join(row.Marks["reviewers (group)"], "+"); got != "sub" {
		t.Errorf("group column mark = %q, want sub", got)
	}
	if !slices.Equal(row.Reaches, []string{"reviewers"}) {
		t.Errorf("reaches = %v, want [reviewers]", row.Reaches)
	}
}
