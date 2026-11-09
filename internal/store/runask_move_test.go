package store

import (
	"slices"
	"strings"
	"testing"
)

// moveLinear and moveLinearNew are two declared sources on one host, as a
// yard whose source moved has them; moveElsewhere is a link source on
// another.
const moveLinear = `name: linear
base: https://api.linear.app
link:
  host: linear.app
  issue: '^/([^/]+)/issue/([A-Z][A-Z0-9]*-[0-9]+)'
  fixed: "https://linear.app/{1}/issue/{2}"
  thread: "{2}"
calls:
  ping: { kind: read, request: { method: GET, path: /me } }
`

const moveElsewhere = `name: gh
base: https://api.github.com
link:
  host: github.com
  issue: '^/([^/]+)/([^/]+)/issues/([0-9]+)'
  fixed: "https://github.com/{1}/{2}/issues/{3}"
  thread: "{1}/{2}#{3}"
calls:
  ping: { kind: read, request: { method: GET, path: /me } }
`

func moveAsk(command string) Ask {
	return Ask{Question: "Move them?", To: "mckean", Context: []string{"the source moved to a declared one"},
		Run: &AskRun{Argv: []string{command}}}
}

// moveYard is a run yard with the link sources linear and lin-new on one
// host and gh on another, and two beads linked to issues of linear.
func moveYard(t *testing.T) (y *askYard, beads [2]string) {
	t.Helper()
	y, _ = runYard(t)
	for name, text := range map[string]string{"linear.yaml": moveLinear, "lin-new.yaml": strings.Replace(moveLinear, "name: linear", "name: lin-new", 1), "gh.yaml": moveElsewhere} {
		if _, err := y.PutSource(y.ctx, name, text, "mckean"); err != nil {
			t.Fatal(err)
		}
	}
	beads = [2]string{y.bead, y.makeBead(t, NewBead{Title: "the second"})}
	for i, bead := range beads {
		url := []string{"https://linear.app/acme/issue/YRD-1", "https://linear.app/acme/issue/YRD-2"}[i]
		if _, err := y.AddLink(y.ctx, bead, NewLink{Kind: LinkTracks, Source: "linear", URL: url}, "mckean"); err != nil {
			t.Fatal(err)
		}
	}
	return y, beads
}

// A link move card is routine: it counts the links and names the beads that
// hold them, says the URLs stay on the host the sources share, and is taken
// back by the same move the other way. The operator's approval moves the
// links as the command does (they log unlinked and linked on their beads),
// and answers what the command prints.
func TestRunAskLinkMove(t *testing.T) {
	t.Parallel()
	y, beads := moveYard(t)
	seq := y.ask(t, moveAsk("yardr link move --to-source lin-new --from-source linear"), "yardmaster")
	got := y.asked(t, seq)
	run := got.Ask.Run
	if want := []string{"link", "move", "--from-source", "linear", "--to-source", "lin-new"}; !slices.Equal(run.Argv, want) {
		t.Fatalf("the ask stores %v, want %v", run.Argv, want)
	}
	if run.Description != "Make another bead the issue's tracker, or move the yard's links from one source to another" {
		t.Errorf("description %q", run.Description)
	}
	joined := strings.Join(run.Does, "\n")
	for _, want := range []string{
		"moves 2 links of the source linear to the source lin-new, on 2 beads (" + beads[0] + ", " + beads[1] + ").",
		"The URLs stay as they are: both sources are on linear.app.",
	} {
		if !strings.Contains(joined, want) && !strings.Contains(joined, strings.Replace(want, beads[0]+", "+beads[1], beads[1]+", "+beads[0], 1)) {
			t.Errorf("the card does not say %q; it says %q", want, run.Does)
		}
	}
	if run.Back != "yardr link move --from-source lin-new --to-source linear" || run.BackBy != "only the operator" {
		t.Errorf("the card is taken back by %q, %q", run.Back, run.BackBy)
	}
	if len(got.Ask.Weight) != 0 {
		t.Fatalf("the ask weighs %+v, want routine", got.Ask.Weight)
	}
	h := head(t, y)
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if done.Result != "moved 2 links from linear to lin-new" {
		t.Fatalf("the approval recorded %q", done.Result)
	}
	for _, bead := range beads {
		ls, err := y.Links(y.ctx, bead)
		if err != nil || len(ls) != 1 || ls[0].Source != "lin-new" || ls[0].Kind != LinkTracks {
			t.Fatalf("the links of %s = %+v, %v", bead, ls, err)
		}
	}
	evs, err := y.EventsSince(y.ctx, h, 0)
	if err != nil {
		t.Fatal(err)
	}
	var linked int
	for _, e := range evs {
		if e.Kind == "linked" && e.Data["moved_source_from"] == "linear" && e.Actor == "mckean" {
			linked++
		}
	}
	if linked != 2 {
		t.Fatalf("the approval logged %d moved links among %+v", linked, evs)
	}
}

func head(t *testing.T, y *askYard) uint64 {
	t.Helper()
	evs, err := y.Events(y.ctx, "", 1)
	if err != nil || len(evs) == 0 {
		t.Fatalf("the head of the log = %v, %v", evs, err)
	}
	return evs[0].Seq
}

// A link move ask is refused, storing nothing, for what the command refuses
// and the move refuses: a source that links no issues, two hosts, one source
// twice, the source peer, no link to move, an issue form and a missing flag.
// Moving one issue to another bead needs no card.
func TestRunAskLinkMoveRefusals(t *testing.T) {
	t.Parallel()
	y, _ := moveYard(t)
	if _, err := y.PutSource(y.ctx, "ghost.yaml", "name: ghost\nbase: https://ghost.example.com\ncalls:\n  ping: { kind: read, request: { method: GET, path: /me } }\n", "mckean"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, command, asker, want string
	}{
		{"no link source", "link move --from-source linear --to-source ghost", "yardmaster", "ghost is no link source (links go to gh, lin-new, linear)"},
		{"another host", "link move --from-source linear --to-source gh", "yardmaster", "linear is on linear.app and gh on github.com: a move keeps the URLs, so both sources must share the host"},
		{"itself", "link move --from-source linear --to-source linear", "yardmaster", "move links from linear to itself: give two sources"},
		{"the peer source", "link move --from-source peer --to-source linear", "yardmaster", "nothing moves them"},
		{"nothing to move", "link move --from-source lin-new --to-source linear", "yardmaster", "no link names the source lin-new"},
		{"the issue form", "link move yd-1 --to yd-2", "yardmaster", "needs no approval, run it yourself"},
		{"an issue and sources", "link move yd-1 --from-source linear --to-source lin-new", "yardmaster", "needs no approval, run it yourself"},
		{"one flag", "link move --from-source linear", "yardmaster", "both sources, each flag once"},
		{"a flag twice", "link move --from-source linear --from-source lin-new --to-source gh", "yardmaster", "both sources, each flag once"},
		{"another flag", "link move --from-source linear --to-source lin-new --force", "yardmaster", "link move with --force is the operator's to type in a terminal"},
		{"the operator asks", "link move --from-source linear --to-source lin-new", "mckean", "needs no approval: run it yourself"},
		{"nobody", "link move --from-source linear --to-source lin-new", "stranger", "a command for the operator to approve is proposed by a crew member or a user of the yard"},
	} {
		if _, err := y.AddAsk(y.ctx, y.bead, moveAsk(c.command), c.asker); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s = %v, want %q", c.name, err, c.want)
		}
	}
	if notes, err := y.Notes(y.ctx, y.bead); err != nil || len(notes) != 0 {
		t.Fatalf("the refused asks left %d notes, %v", len(notes), err)
	}
}

// A bead that would track two issues of the new source after the move
// refuses the card, as it refuses the move, and one approved after it came
// about refuses the approval with nothing moved.
func TestRunAskLinkMoveRefusesWhatTheMoveRefuses(t *testing.T) {
	t.Parallel()
	y, beads := moveYard(t)
	seq := y.ask(t, moveAsk("link move --from-source linear --to-source lin-new"), "yardmaster")
	if _, err := y.AddLink(y.ctx, beads[1], NewLink{Kind: LinkTracks, Source: "lin-new", URL: "https://linear.app/acme/issue/YRD-9"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.AddAsk(y.ctx, y.bead, moveAsk("link move --from-source linear --to-source lin-new"), "yardmaster"); err == nil ||
		!strings.Contains(err.Error(), beads[1]+" would track two lin-new issues after the move") {
		t.Fatalf("asking with a clash = %v", err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil); err == nil ||
		!strings.Contains(err.Error(), beads[1]+" would track two lin-new issues after the move") {
		t.Fatalf("approving with a clash = %v", err)
	}
	for _, bead := range beads {
		if ls, err := y.Links(y.ctx, bead); err != nil || !slices.ContainsFunc(ls, func(l Link) bool { return l.Source == "linear" }) {
			t.Fatalf("the refused approval left the links of %s as %+v, %v", bead, ls, err)
		}
	}
}

// An approval is held to the links the card counted: one added under the
// source since is another count, and a source replaced onto another host is
// another card. Nothing moves either way.
func TestRunAskLinkMoveStale(t *testing.T) {
	t.Parallel()
	y, beads := moveYard(t)
	seq := y.ask(t, moveAsk("link move --from-source linear --to-source lin-new"), "yardmaster")
	third := y.makeBead(t, NewBead{Title: "the third"})
	if _, err := y.AddLink(y.ctx, third, NewLink{Kind: LinkRelates, Source: "linear", URL: "https://linear.app/acme/issue/YRD-3"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil); err == nil ||
		!strings.Contains(err.Error(), "was written when other links named the source linear") {
		t.Fatalf("approving with a link added since = %v", err)
	}
	for _, bead := range append(beads[:], third) {
		if ls, err := y.Links(y.ctx, bead); err != nil || len(ls) != 1 || ls[0].Source != "linear" {
			t.Fatalf("the refused approval left the links of %s as %+v, %v", bead, ls, err)
		}
	}
	// A fresh card counts the three, and a declaration replaced onto
	// another host before the approval is another card.
	seq = y.ask(t, moveAsk("link move --from-source linear --to-source lin-new"), "yardmaster")
	if !strings.Contains(strings.Join(y.asked(t, seq).Ask.Run.Does, "\n"), "moves 3 links of the source linear to the source lin-new, on 3 beads") {
		t.Fatalf("the fresh card says %q", y.asked(t, seq).Ask.Run.Does)
	}
	elsewhere := strings.Replace(strings.Replace(moveLinear, "name: linear", "name: lin-new", 1), "host: linear.app", "host: linear.example.net", 1)
	if _, err := y.PutSource(y.ctx, "lin-new.yaml", elsewhere, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil); err == nil ||
		!strings.Contains(err.Error(), "linear is on linear.app and lin-new on linear.example.net") {
		t.Fatalf("approving with the destination on another host = %v", err)
	}
}

// recordsYard is a hook yard whose source linear keeps a hook, the identity
// of the crew member yardmaster and the identity of the user ada, and which
// has a second source, lin2, to fold them into.
func recordsYard(t *testing.T) *askYard {
	t.Helper()
	y := hookYard(t)
	if _, err := y.PutSource(y.ctx, "lin2.yaml", strings.Replace(hookLinear, "name: linear", "name: lin2", 1), "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.PutHook(y.ctx, Hook{Name: "linear", Source: "linear", SecretFile: HookSecretFile(y.home, "linear")}, "mckean"); err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]string{"yardmaster": "U1", "ada": "A1"} {
		if err := y.SetIdentity(y.ctx, name, "linear", Identity{ID: id}, "mckean"); err != nil {
			t.Fatal(err)
		}
	}
	return y
}

// A source move card counts each kind of record the fold moves, and is
// weighed by it: trust where a hook goes to the destination, as-user where
// a user's identity does. The operator's approval folds exactly the records
// the card counted, as MoveSourceRecords does, and answers the counts.
func TestRunAskSourceMove(t *testing.T) {
	t.Parallel()
	y := recordsYard(t)
	seq := y.ask(t, moveAsk("yardr source move linear lin2"), "yardmaster")
	got := y.asked(t, seq)
	run := got.Ask.Run
	if want := []string{"source", "move", "linear", "lin2"}; !slices.Equal(run.Argv, want) {
		t.Fatalf("the ask stores %v, want %v", run.Argv, want)
	}
	if want := "folds what the yard keeps under the source linear into the source lin2: 1 hook and 2 identities (1 of crew and 1 of users)."; !slices.Contains(run.Does, want) {
		t.Fatalf("the card says %q, want %q", run.Does, want)
	}
	if run.Back == "" || run.BackBy != "nobody" {
		t.Errorf("the card is taken back by %q, %q", run.Back, run.BackBy)
	}
	var classes []string
	for _, w := range got.Ask.Weight {
		classes = append(classes, w.Class)
	}
	if !slices.Equal(classes, []string{WeightTrust, WeightAsUser}) || !StrongWeight(got.Ask.Weight) {
		t.Fatalf("the ask weighs %v, want trust and as-user", classes)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil); err == nil || !strings.Contains(err.Error(), "weighty (trust, as-user)") {
		t.Fatalf("approving without the weight seen = %v", err)
	}
	if h, err := y.GetHook(y.ctx, "linear"); err != nil || h.Source != "linear" {
		t.Fatalf("the refused approval left the hook %+v, %v", h, err)
	}
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "moved the records of linear to lin2: 1 hook, 2 identities, 0 grants, 0 subscriptions, 0 threads"; done.Result != want {
		t.Fatalf("the approval recorded %q, want %q", done.Result, want)
	}
	if h, err := y.GetHook(y.ctx, "linear"); err != nil || h.Source != "lin2" {
		t.Fatalf("the hook = %+v, %v", h, err)
	}
	m, err := y.GetCrew(y.ctx, "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	if _, left := m.Identities["linear"]; left || m.Identities["lin2"].ID != "U1" {
		t.Fatalf("the crew member's identities = %+v", m.Identities)
	}
	u, err := y.GetUser(y.ctx, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if _, left := u.Identities["linear"]; left || u.Identities["lin2"].ID != "A1" {
		t.Fatalf("the user's identities = %+v", u.Identities)
	}
}

// A source move ask is refused, storing nothing, for what the row refuses
// and the fold refuses: a destination the yard does not declare, the same
// source twice, a name no source has, a source that keeps nothing, an
// identity the two sources hold and disagree on, the wrong number of words,
// an asker who may fold the records themselves, and a writer who is no one
// of the yard.
func TestRunAskSourceMoveRefusals(t *testing.T) {
	t.Parallel()
	y := recordsYard(t)
	if err := y.SetIdentity(y.ctx, "yardmaster", "lin2", Identity{ID: "U2"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, command, asker, want string
	}{
		{"a destination the yard does not declare", "source move linear ghost", "yardmaster", "source ghost: not found"},
		{"itself", "source move linear linear", "yardmaster", "move records from linear to itself: give two sources"},
		{"a name no source has", "source move linear Lin_2", "yardmaster", `source "Lin_2": use lowercase letters`},
		{"a clash of identities", "source move linear lin2", "yardmaster", "crew member yardmaster has one linear identity and another lin2 identity"},
		{"one word", "source move linear", "yardmaster", "two sources, not 1 words"},
		{"three words", "source move linear lin2 crm", "yardmaster", "two sources, not 3 words"},
		{"the operator asks", "source move linear lin2", "mckean", "needs no approval: run it yourself"},
		{"nobody", "source move linear lin2", "stranger", "a command for the operator to approve is proposed by a crew member or a user of the yard"},
	} {
		if _, err := y.AddAsk(y.ctx, y.bead, moveAsk(c.command), c.asker); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s = %v, want %q", c.name, err, c.want)
		}
	}
	// A source that keeps nothing folds nothing, so there is no card.
	y = recordsYard(t)
	if _, err := y.PutSource(y.ctx, "lin3.yaml", strings.Replace(hookLinear, "name: linear", "name: lin3", 1), "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.AddAsk(y.ctx, y.bead, moveAsk("source move lin3 linear"), "yardmaster"); err == nil ||
		!strings.Contains(err.Error(), "nothing is kept under the source lin3, so the command would change nothing") {
		t.Errorf("a source that keeps nothing = %v", err)
	}
	if notes, err := y.Notes(y.ctx, y.bead); err != nil || len(notes) != 0 {
		t.Fatalf("the refused asks left %d notes, %v", len(notes), err)
	}
}

// An approval is held to the counts the card showed: another record under the
// source since, or a destination replaced, is another card, and nothing is
// folded.
func TestRunAskSourceMoveStale(t *testing.T) {
	t.Parallel()
	y := recordsYard(t)
	seq := y.ask(t, moveAsk("source move linear lin2"), "yardmaster")
	if _, err := y.PutHook(y.ctx, Hook{Name: "second", Source: "linear", SecretFile: HookSecretFile(y.home, "second")}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil ||
		!strings.Contains(err.Error(), "was written when the yard kept other records under the source linear") {
		t.Fatalf("approving with a record added since = %v", err)
	}
	seq = y.ask(t, moveAsk("source move linear lin2"), "yardmaster")
	if _, err := y.PutSource(y.ctx, "lin2.yaml", strings.Replace(strings.Replace(hookLinear, "name: linear", "name: lin2", 1), "api.linear.app", "api.linear.example.net", 1), "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil ||
		!strings.Contains(err.Error(), "was written from another declaration of the source lin2") {
		t.Fatalf("approving with the destination replaced = %v", err)
	}
	for _, name := range []string{"linear", "second"} {
		if h, err := y.GetHook(y.ctx, name); err != nil || h.Source != "linear" {
			t.Fatalf("the refused approvals left the hook %s as %+v, %v", name, h, err)
		}
	}
}
