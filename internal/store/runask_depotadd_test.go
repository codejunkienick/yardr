package store

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func depotAddAsk(command, to string, test string) Ask {
	ask := Ask{Question: "Add the depot?", To: to, Context: []string{"the team has no shell"},
		Run: &AskRun{Argv: []string{command}}}
	if test != "" {
		ask.Run.Test = test
	}
	return ask
}

// depot add has one row, tried by its words: the name and the fetch in
// one stored order however they were typed, the kind git alone and the
// base defaulted as the terminal's, and a refusal in the row's words.
func TestParseDepotAdd(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		line string
		argv []string
		want string // a refusal, when the line is refused
	}{
		{line: "depot add web --clone file:///repos/web.git", argv: []string{"depot", "add", "web", "--clone", "file:///repos/web.git", "--kind", "git", "--base", "origin/main"}},
		{line: "yardr depot add web --base origin/trunk --clone https://github.com/ada/web.git --kind git", argv: []string{"depot", "add", "web", "--clone", "https://github.com/ada/web.git", "--kind", "git", "--base", "origin/trunk"}},
		{line: "depot add web --clone=git@github.com:ada/web.git", argv: []string{"depot", "add", "web", "--clone", "git@github.com:ada/web.git", "--kind", "git", "--base", "origin/main"}},
		{line: "depot add web", want: "one --clone <url>, not 0"},
		{line: "depot add web --clone file:///a.git --clone file:///b.git", want: "one --clone <url>, not 2"},
		{line: "depot add web notes --clone file:///a.git", want: "one depot, not 2 words"},
		{line: "depot add web --clone file:///a.git --kind dir", want: "a directory depot names a path on this machine"},
		{line: "depot add web --clone file:///a.git --kind bare", want: `--kind "bare": give git`},
		{line: "depot add web --clone file:///a.git --force", want: "depot add with --force is the operator's to type in a terminal"},
	} {
		row, argv, err := parseRow([]string{c.line})
		if c.want != "" {
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("%q = %v, want a refusal that says %q", c.line, err, c.want)
			}
			continue
		}
		if err != nil || row.use != depotAddUse || !slices.Equal(argv, c.argv) {
			t.Errorf("%q = %v, %v, %v; want the depot add row with %v", c.line, row, argv, err, c.argv)
		}
	}
	if validCloneURL("https://github.com/ada/web.git?access_token=stand-in-token") {
		t.Error("a clone URL with hidden query text was accepted")
	}
}

// A depot add card shows the name, the kind, the fetch, the clone place,
// the base and the test command whole or none, weighs outside always and
// code where a test rides, and its approval records a carrying out and
// records no depot itself: the server clones after it.
func TestRunAskDepotAdd(t *testing.T) {
	t.Parallel()
	y, _ := runYard(t)
	seq := y.ask(t, depotAddAsk("depot add web --clone file:///repos/web.git", "mckean", ""), "yardmaster")
	got := y.asked(t, seq)
	run := got.Ask.Run
	if want := []string{"depot", "add", "web", "--clone", "file:///repos/web.git", "--kind", "git", "--base", "origin/main"}; !slices.Equal(run.Argv, want) {
		t.Fatalf("the ask stores %v, want %v", run.Argv, want)
	}
	if run.Description != depotAddDescription {
		t.Errorf("description %q", run.Description)
	}
	joined := strings.Join(run.Does, "\n")
	for _, want := range []string{
		"adds the depot web (git): it clones file:///repos/web.git into " + filepath.Join(y.home, DepotsDir, "web") + ", and bead branches start from origin/main.",
		"it sets no test command: the depot is added now, and its tests are named later, on a depot test card.",
		"the clone is from this machine, and needs no credential.",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the card does not say %q; it says %q", want, run.Does)
		}
	}
	if run.Back != "yardr depot rm web" || run.BackBy != "only the operator" {
		t.Errorf("the card is taken back by %q, %q", run.Back, run.BackBy)
	}
	if len(got.Ask.Weight) != 1 || got.Ask.Weight[0].Class != WeightOutside {
		t.Fatalf("the ask weighs %+v, want outside alone", got.Ask.Weight)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil); err == nil || !strings.Contains(err.Error(), "weighty (outside)") {
		t.Fatalf("approving without the weight seen = %v", err)
	}
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if done.Result != CarryPending {
		t.Fatalf("the approval recorded %q, want %q", done.Result, CarryPending)
	}
	rec, err := y.CarryOf(y.ctx, y.bead, seq)
	if err != nil || rec == nil || rec.State != CarryApproved || rec.Actor != "mckean" || !slices.Equal(rec.Argv, run.Argv) {
		t.Fatalf("the carrying record = %+v, %v", rec, err)
	}
	if _, err := y.GetDepot(y.ctx, "web"); err == nil {
		t.Fatal("the approval recorded the depot itself: the clone is the carrying out's")
	}

	// A test command rides whole, and weighs code beside the fetch.
	seq = y.ask(t, depotAddAsk("depot add api --clone file:///repos/api.git", "mckean", "sh .yardr/test"), "yardmaster")
	got = y.asked(t, seq)
	if joined := strings.Join(got.Ask.Run.Does, "\n"); !strings.Contains(joined, "its test command will be sh .yardr/test, whole.") {
		t.Errorf("the card says %q", got.Ask.Run.Does)
	}
	classes := []string{}
	for _, w := range got.Ask.Weight {
		classes = append(classes, w.Class)
	}
	if !slices.Equal(classes, []string{WeightOutside, WeightCode}) || !StrongWeight(got.Ask.Weight) {
		t.Fatalf("the ask weighs %+v, want outside and code, which is strong", got.Ask.Weight)
	}
	if got.Ask.Run.Test != "sh .yardr/test" {
		t.Errorf("the ask carries the test %q", got.Ask.Run.Test)
	}
}

// A depot add ask is refused, storing nothing, for what the row refuses:
// the operator as the asker, a member as the one asked, a depot or a
// clone place the yard has, a name, fetch or base git takes none of, a
// directory kind, and a test command no card shows whole.
func TestRunAskDepotAddRefusals(t *testing.T) {
	t.Parallel()
	y, _ := runYard(t)
	if err := y.PutDepot(y.ctx, Depot{Name: "web", Path: "/srv/web", Base: "origin/main"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(y.home, DepotsDir, "taken"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, command, to, asker, test, want string
	}{
		{"the operator asks", "depot add api --clone file:///repos/api.git", "mckean", "mckean", "", "needs no approval: run it yourself"},
		{"a member is asked", "depot add api --clone file:///repos/api.git", "ada", "yardmaster", "", "a command is the operator's to approve (mckean)"},
		{"nobody", "depot add api --clone file:///repos/api.git", "mckean", "stranger", "", "a command for the operator to approve is proposed by a crew member or a user of the yard"},
		{"a depot the yard has", "depot add web --clone file:///repos/web.git", "mckean", "yardmaster", "", "there is a depot web already"},
		{"a taken clone place", "depot add taken --clone file:///repos/taken.git", "mckean", "yardmaster", "", "already stands where its clone would go"},
		{"no depot name", "depot add Web --clone file:///repos/web.git", "mckean", "yardmaster", "", "use lowercase letters, digits"},
		{"no fetch host path", "depot add api --clone https://github.com", "mckean", "yardmaster", "", "names no fetch git can clone"},
		{"no branch base", "depot add api --clone file:///repos/api.git --base origin/../main", "mckean", "yardmaster", "", "is not a branch name"},
		{"a directory kind", "depot add api --clone file:///repos/api.git --kind dir", "mckean", "yardmaster", "", "a directory depot names a path on this machine"},
		{"a test past the card", "depot add api --clone file:///repos/api.git", "mckean", "yardmaster", strings.Repeat("x", MaxRunDepotTest+1), "a card shows it whole and holds at most"},
		{"a test of two lines", "depot add api --clone file:///repos/api.git", "mckean", "yardmaster", "make test\nmake vet", "holds a line break or a control character"},
	} {
		ask := depotAddAsk(c.command, c.to, c.test)
		if _, err := y.AddAsk(y.ctx, y.bead, ask, c.asker); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s = %v, want %q", c.name, err, c.want)
		}
	}
	for _, cloneURL := range []string{
		"https://mckean:stand-in-token@github.com/ada/web.git",
		"mckean:stand-in-token@github.com:ada/web.git",
	} {
		_, err := y.AddAsk(y.ctx, y.bead, depotAddAsk("depot add api --clone "+cloneURL, "mckean", ""), "yardmaster")
		if err == nil || !strings.Contains(err.Error(), "clone URL must not carry credentials") || strings.Contains(err.Error(), "stand-in-token") {
			t.Errorf("a credential-bearing clone URL was not refused without repeating the credential: %v", err)
		}
	}
	if notes, err := y.Notes(y.ctx, y.bead); err != nil || len(notes) != 0 {
		t.Fatalf("the refused asks left %d notes, %v", len(notes), err)
	}
}

// An https fetch answers with its push source: the card holds its
// fingerprint, and no source for its host, or a source with no token
// standing, refuses the ask naming the fix.
func TestRunAskDepotAddPrivate(t *testing.T) {
	t.Parallel()
	y, _ := runYard(t)
	if _, err := y.PutSource(y.ctx, "github.yaml", depotPushDecl, "mckean"); err != nil {
		t.Fatal(err)
	}
	// No token stands yet: the ask is refused naming the secret card.
	if _, err := y.AddAsk(y.ctx, y.bead, depotAddAsk("depot add web --clone https://github.com/ada/web.git", "mckean", ""), "yardmaster"); err == nil ||
		!strings.Contains(err.Error(), "no GITHUB_TOKEN stands for the source github") || !strings.Contains(err.Error(), "secret card for the source github") {
		t.Fatalf("an https ask with no token = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(y.home, "sources"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(y.home, "sources", "github.env"), []byte("GITHUB_TOKEN=stand-in-token-7f3a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	seq := y.ask(t, depotAddAsk("depot add web --clone https://github.com/ada/web.git", "mckean", ""), "yardmaster")
	got := y.asked(t, seq)
	if got.Ask.Run.Seen["push source"] == "" {
		t.Errorf("the card holds no push source fingerprint: %v", got.Ask.Run.Seen)
	}
	if joined := strings.Join(got.Ask.Run.Does, "\n"); !strings.Contains(joined, "the clone answers with the credential of the source github for https://github.com") ||
		!strings.Contains(joined, "no token stands in the card, the config or a log") {
		t.Errorf("the card says %q", got.Ask.Run.Does)
	}
	if err := os.WriteFile(filepath.Join(y.home, "sources", "github.env"), []byte("GITHUB_TOKEN=stand-in-token-7f3a\nOTHER=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A yard with no source for the host refuses an https ask outright.
	y2, _ := runYard(t)
	if _, err := y2.AddAsk(y2.ctx, y2.bead, depotAddAsk("depot add web --clone https://github.com/ada/web.git", "mckean", ""), "yardmaster"); err == nil ||
		!strings.Contains(err.Error(), "no source gives git a credential for github.com") || !strings.Contains(err.Error(), "add a source for that host first") {
		t.Fatalf("an https ask with no source = %v", err)
	}
}

// An approval is held to the push source the card was written from: one
// replaced since leaves the ask stale, and a depot added since refuses
// it, each with nothing recorded and no carrying out.
func TestRunAskDepotAddStale(t *testing.T) {
	t.Parallel()
	y, _ := runYard(t)
	if _, err := y.PutSource(y.ctx, "github.yaml", depotPushDecl, "mckean"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(y.home, "sources"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(y.home, "sources", "github.env"), []byte("GITHUB_TOKEN=stand-in-token-7f3a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	seq := y.ask(t, depotAddAsk("depot add web --clone https://github.com/ada/web.git", "mckean", ""), "yardmaster")
	changed := strings.Replace(depotPushDecl, "https://api.github.com", "https://api.github.example.com", 1)
	if _, err := y.PutSource(y.ctx, "github.yaml", changed, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil ||
		!strings.Contains(err.Error(), "the push source for https://github.com/ada/web.git stood otherwise") {
		t.Fatalf("approving with the source replaced = %v", err)
	}
	if rec, err := y.CarryOf(y.ctx, y.bead, seq); err != nil || rec != nil {
		t.Fatalf("the refused approval left a carrying record %+v, %v", rec, err)
	}
	seq = y.ask(t, depotAddAsk("depot add web --clone https://github.com/ada/web.git", "mckean", ""), "yardmaster")
	if err := y.PutDepot(y.ctx, Depot{Name: "web", Path: "/srv/web", Base: "origin/main"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil ||
		!strings.Contains(err.Error(), "there is a depot web already") {
		t.Fatalf("approving with the depot added since = %v", err)
	}
}
