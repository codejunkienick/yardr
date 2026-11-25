package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// The --take half of the pack apply card. The cases here are steps of
// TestPackApplyDepotCardAndStale (a card that takes, and what moves under
// it), TestPackApplyDepotRefusals (what a card that takes refuses) and
// TestPackApplyRefusals (the words' own rows), as the --yes ones are.

// takeSum is the hex SHA-256 of text, as a plan's object holds it.
func takeSum(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// takeFile is a kept file of a plan as the server's reading carries one a card
// takes, with both its hashes, text and diff: one the yard runs (a role), or a
// stage file of the pack's flows, which the reading marks as the plan's object
// says (StageFile), whatever the file is called.
func takeFile(name, old, new string, runs, stage bool) AskPlanObject {
	return AskPlanObject{Kind: "file", Name: name, Action: "kept", Runs: runs, StageFile: stage,
		Keeps:      []string{"the yard's copy, changed since the last import (the pack's has not changed since); --take file/" + name + " takes the pack's"},
		FileOldSHA: takeSum(old), FileNewSHA: takeSum(new), Text: new, Diff: Unified(old, new)}
}

// takePlan is a depot plan that keeps what the yard changed of a role, a
// stage file and two flows, as the server's reading carries a kept object a
// card takes: the role and the stage file with both their hashes, text and
// diff, and the first flow with the digest of the one the yard has. The
// second flow is kept and not taken, so it carries nothing whole.
func takePlan() *AskPlan {
	return &AskPlan{Objects: []AskPlanObject{
		takeFile("roles/b.md", "role by hand\n", "role by the pack\n", true, false),
		takeFile("flows/default/new.md", "stage by hand\n", "stage by the pack\n", false, true),
		{Kind: "flow", Name: "starter.default", Action: "kept",
			Changes:    []AskChange{{Field: "stages", Old: "in, work, done", New: "in, done"}},
			Keeps:      []string{"the yard's flow, changed since the last import (the pack's has not changed since); --take flow/starter.default takes the pack's"},
			FlowOldSHA: strings.Repeat("c", 64)},
		{Kind: "flow", Name: "starter.train", Action: "kept",
			Keeps: []string{"the yard's flow, changed since the last import (the pack's has not changed since); --take flow/starter.train takes the pack's"}},
		{Kind: "file", Name: "roles/c.md", Action: "unchanged"},
	}}
}

// A card that takes shows each kept object it names whole, as the change it
// makes, says that approval keeps no other copy of what it overwrites, stores
// the plan as the plan that keeps them, and weighs as code whatever it takes:
// a flow, a role and a stage file alike. The words are sorted in the stored
// command, and the same take in another order is the same card. It is never
// a summary, and it stands beside --yes.
func packApplyTakeShowsKeptWhole(t *testing.T) {
	t.Helper()
	y, _ := runYard(t)
	y.firstInstallDepot(t)
	y.setPack(&fakePack{label: "embedded in yardr test1", kind: "git", depot: "web", plan: takePlan(), noSources: true})
	roleSHA := func(text string) string { return takeSum(text) }
	ask := packAsk()
	ask.Run.Argv = []string{"pack apply starter --depot web --take flow/starter.default --take file/roles/b.md"}
	seq := y.ask(t, ask, "yardmaster")
	got := y.asked(t, seq)
	run := got.Ask.Run
	if want := "pack apply starter --take file/roles/b.md --take flow/starter.default --depot web"; strings.Join(run.Argv, " ") != want {
		t.Errorf("the stored argv = %q, want %q", run.Argv, want)
	}
	if run.Pack == nil || !slices.Equal(run.Pack.Take, []string{"file/roles/b.md", "flow/starter.default"}) || run.Pack.Yes || run.Pack.Summary {
		t.Fatalf("the stored pack = %+v, want the two names sorted, no yes and no summary", run.Pack)
	}
	// The plan stays the plan that keeps: the card's take is its own, and
	// the carrying out rebuilds this plan and holds the take to it.
	for _, o := range run.Pack.Plan.Objects {
		if o.Action == "take" || o.Action == "change" {
			t.Errorf("the stored plan holds %s %s as %s: want it kept", o.Kind, o.Name, o.Action)
		}
	}
	joined := strings.Join(run.Does, "\n")
	for _, want := range []string{
		"file roles/b.md: take, sha256 " + roleSHA("role by hand\n") + " -> sha256 " + roleSHA("role by the pack\n") + " (1 insertions, 1 deletions).",
		"flow starter.default: take (stages: in, work, done -> in, done).",
		// Why the plan kept them stands under them, and so does what the
		// approval then does.
		"file roles/b.md: keeps the yard's copy, changed since the last import",
		"flow starter.default: keeps the yard's flow, changed since the last import",
		"file roles/b.md: approval overwrites the yard's changed copy with the pack's bytes and keeps no other copy of the change.",
		"flow starter.default: approval sets the pack's version over the yard's changed one and keeps no other copy of the change.",
		// What is kept and not taken stays kept.
		"flow starter.train: keeps the yard's flow",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the card lacks %q:\n%s", want, joined)
		}
	}
	for _, not := range []string{"flow starter.train: take", "flow starter.train: approval", "file flows/default/new.md: take"} {
		if strings.Contains(joined, not) {
			t.Errorf("the card says %q of what it does not take:\n%s", not, joined)
		}
	}
	lines := strings.Join(got.Lines(), "\n")
	for _, want := range []string{
		"the change to file roles/b.md (sha256 " + roleSHA("role by hand\n") + " -> " + roleSHA("role by the pack\n") + "), whole as carried:",
		"-role by hand",
		"+role by the pack",
		"the file roles/b.md as the pack would install it, whole as carried:",
	} {
		if !strings.Contains(lines, want) {
			t.Errorf("the card does not show the taken role whole; lacks %q:\n%s", want, lines)
		}
	}
	if !StrongWeight(got.Ask.Weight) {
		t.Errorf("weight = %+v, want strong", got.Ask.Weight)
	}
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
	if err != nil || done.Result != "approved, to be carried out" {
		t.Fatalf("the approval = %+v, %v", done, err)
	}
	// The same take typed in another order is the card that stands.
	swapped := packAsk()
	swapped.Run.Argv = []string{"pack apply starter --depot web --take=file/roles/b.md --take=flow/starter.default"}
	if again := y.asked(t, y.ask(t, swapped, "yardmaster")).Ask.Run; strings.Join(again.Argv, " ") != strings.Join(run.Argv, " ") || !slices.Equal(again.Does, run.Does) {
		t.Errorf("the same take in another order is another card: %q", again.Argv)
	}
	// A bare re-apply may take as well: the depot is the one that holds the pack.
	bare := packAsk()
	bare.Run.Argv = []string{"pack apply starter --take flow/starter.default"}
	if again := y.asked(t, y.ask(t, bare, "yardmaster")).Ask.Run; again.Pack == nil || !slices.Equal(again.Pack.Take, []string{"flow/starter.default"}) {
		t.Errorf("a bare re-apply that takes stored %+v", again.Pack)
	}
	// Each kind of thing a take sets is code, a flow, a role and a stage file
	// alike, with or without a depot named.
	for _, c := range []struct{ words, says string }{
		{"--take flow/starter.default", "the pack starter's version of flow/starter.default"},
		{"--take file/roles/b.md", "the pack starter's version of file/roles/b.md"},
		{"--take file/flows/default/new.md", "the pack starter's version of file/flows/default/new.md"},
		{"--depot web --take file/flows/default/new.md --take flow/starter.default", "file/flows/default/new.md and flow/starter.default"},
	} {
		weighed := packAsk()
		weighed.Run.Argv = []string{"pack apply starter " + c.words}
		ws := y.asked(t, y.ask(t, weighed, "yardmaster")).Ask.Weight
		i := slices.IndexFunc(ws, func(a AskWeight) bool { return a.Class == WeightCode })
		if i < 0 || !strings.Contains(ws[i].Line, c.says) || !StrongWeight(ws) {
			t.Errorf("%s: weight = %+v, want strong code saying %q", c.words, ws, c.says)
		}
	}
	// A stage file's text is kept in the stage history before the pack's goes
	// over it, and the card says so in place of "keeps no other copy"; a role
	// keeps today's sentence. Which file is a stage's is the reading's to say,
	// not its name's: a file called like one that the reading does not mark
	// keeps today's sentence too.
	lookalike := takePlan()
	lookalike.Objects = append(lookalike.Objects, takeFile("flows/default/decide.md", "decide by hand\n", "decide by the pack\n", true, false))
	y.setPack(&fakePack{label: "embedded in yardr test1", kind: "git", depot: "web", plan: lookalike, noSources: true})
	staged := packAsk()
	staged.Run.Argv = []string{"pack apply starter --depot web --take file/roles/b.md --take file/flows/default/new.md --take file/flows/default/decide.md"}
	stagedDoes := y.asked(t, y.ask(t, staged, "yardmaster")).Ask.Run.Does
	const overwrites = "approval overwrites the yard's changed copy with the pack's bytes"
	strongHas(t, stagedDoes,
		"file flows/default/decide.md: "+overwrites+" and keeps no other copy of the change.",
		"file flows/default/new.md: "+overwrites+"; the yard's text is kept in <home>/stage-history/, named by its sha256.",
		"file roles/b.md: "+overwrites+" and keeps no other copy of the change.")
	if joined := strings.Join(stagedDoes, "\n"); strings.Count(joined, "stage-history") != 1 {
		t.Errorf("the card names the stage history for more than the stage file:\n%s", joined)
	}

	// A take is a whole card's, as --yes is: where the pack's texts and diffs
	// run past the bound a card that takes is refused, never summarised.
	big := takePlan()
	big.Objects = append(big.Objects, AskPlanObject{Kind: "file", Name: "roles/big.md", Action: "create", FileNewSHA: strings.Repeat("d", 64), Text: strings.Repeat("a", MaxRunPackFiles+1)})
	y.setPack(&fakePack{label: "embedded in yardr test1", kind: "git", depot: "web", plan: big, noSources: true, digest: strings.Repeat("e", 64)})
	summarised := packAsk()
	summarised.Run.Argv = []string{"pack apply starter --depot web"}
	if r := y.asked(t, y.ask(t, summarised, "yardmaster")).Ask.Run; r.Pack == nil || !r.Pack.Summary {
		t.Fatalf("the pack past the bound with no take stored %+v, want the summary shape", r.Pack)
	}
	taking := packAsk()
	taking.Run.Argv = []string{"pack apply starter --depot web --take flow/starter.default"}
	if _, err := y.AddAsk(y.ctx, y.bead, taking, "yardmaster"); err == nil || !strings.Contains(err.Error(), "a card holds at most") {
		t.Errorf("a take past the whole-card bound: AddAsk = %v, want it refused, not summarised", err)
	}
	// With --yes over a script edited by hand: both are shown whole, each
	// with its own sentence, and the card is one.
	both := takePlan()
	edited := "script by hand\n"
	both.Edited = []string{"scripts/land was edited by hand since the last import, and the import would overwrite it"}
	both.Objects = append(both.Objects, AskPlanObject{Kind: "file", Name: "scripts/land", Action: "change", Edited: true, Runs: true,
		FileOldSHA: takeSum(edited), FileNewSHA: takeSum("script by the pack\n"), Text: "script by the pack\n", Diff: Unified(edited, "script by the pack\n")})
	y.setPack(&fakePack{label: "embedded in yardr test1", kind: "git", depot: "web", plan: both, noSources: true})
	combined := packAsk()
	combined.Run.Argv = []string{"pack apply starter --depot web --yes --take file/roles/b.md"}
	r := y.asked(t, y.ask(t, combined, "yardmaster")).Ask.Run
	if r.Pack == nil || !r.Pack.Yes || !slices.Equal(r.Pack.Take, []string{"file/roles/b.md"}) {
		t.Fatalf("the --yes and --take card stored %+v", r.Pack)
	}
	strongHas(t, r.Does,
		"file roles/b.md: take, sha256 "+takeSum("role by hand\n"),
		"file scripts/land: change, sha256 "+takeSum(edited),
		"file scripts/land: approval overwrites the copy edited by hand with the pack's bytes and keeps no other copy of the edit.",
		"file roles/b.md: approval overwrites the yard's changed copy with the pack's bytes and keeps no other copy of the change.")
	// A take does not lift the stop of a script edited by hand, and --yes
	// does not take: each asked alone refuses in its own words.
	alone := packAsk()
	alone.Run.Argv = []string{"pack apply starter --depot web --take file/roles/b.md"}
	if _, err := y.AddAsk(y.ctx, y.bead, alone, "yardmaster"); err == nil || !strings.Contains(err.Error(), "scripts/land was edited by hand") || !strings.Contains(err.Error(), "terminal") {
		t.Errorf("a take beside a script edited by hand, with no --yes: AddAsk = %v, want the terminal road", err)
	}
}

// What a card that takes shows, and what it is held to, stales its approval
// when it moves between the ask and the approval: the file a take overwrites
// edited again, the flow it sets changed, and an object that is no longer
// kept. Nothing runs, and each says what moved.
func packApplyTakeStale(t *testing.T) {
	t.Helper()
	y, _ := runYard(t)
	y.firstInstallDepot(t)
	f := &fakePack{label: "embedded in yardr test1", kind: "git", depot: "web", plan: takePlan(), noSources: true}
	y.setPack(f)
	ask := packAsk()
	ask.Run.Argv = []string{"pack apply starter --depot web --take file/roles/b.md --take flow/starter.default"}
	for _, c := range []struct {
		name   string
		moves  func(p *AskPlan)
		wants  []string
		reason string
	}{
		{"a taken file edited again", func(p *AskPlan) { p.Objects[0].FileOldSHA = takeSum("role by hand again\n") },
			[]string{"the object file roles/b.md", "differs at"}, ""},
		{"a taken flow changed again", func(p *AskPlan) { p.Objects[2].FlowOldSHA = strings.Repeat("f", 64) },
			[]string{"the object flow starter.default", "differs at"}, ""},
		{"the pack's file moved under a take", func(p *AskPlan) { p.Objects[0].FileNewSHA = takeSum("role by the pack, again\n") },
			[]string{"the object file roles/b.md"}, ""},
		{"a taken file that is no longer kept", func(p *AskPlan) { p.Objects[0].Action = "unchanged" },
			[]string{"--take file/roles/b.md: the plan does not keep it, so there is nothing to take"}, ""},
	} {
		f.plan = takePlan()
		seq := y.ask(t, ask, "yardmaster")
		c.moves(f.plan)
		_, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
		if err == nil || !strings.Contains(err.Error(), nothingRan) {
			t.Errorf("%s: ApproveAsk = %v, want it refused with nothing run", c.name, err)
			continue
		}
		for _, want := range c.wants {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: ApproveAsk = %v, want it to say %q", c.name, err, want)
			}
		}
	}
}

// A card that takes is refused where it cannot show what it overwrites or
// where approval would break what stands: an object the plan does not keep,
// a kept object the reading carried nothing of, and a flow whose pack version
// lacks the stage of an open bead, which is named; the same strands are
// refused again at approval where the bead arrives after the ask. Each of the
// plan's own refusals stands as it does for a card that takes nothing.
func packApplyTakeRefusals(t *testing.T) {
	t.Helper()
	y, _ := runYard(t)
	y.firstInstallDepot(t)
	f := &fakePack{label: "embedded in yardr test1", kind: "git", depot: "web", plan: takePlan(), noSources: true}
	y.setPack(f)
	for _, c := range []struct{ words, want string }{
		{"--take file/roles/c.md", "--take file/roles/c.md: the plan does not keep it, so there is nothing to take"},
		{"--take file/roles/nosuch.md", "--take file/roles/nosuch.md: the plan does not keep it, so there is nothing to take"},
		{"--take flow/starter.default --take flow/nosuch", "--take flow/nosuch: the plan does not keep it"},
		{"--take source/linear", "--take source/linear: the plan does not keep it"},
		{"--take flow/starter.train", "--take flow/starter.train: the reading carries nothing of it to show whole"},
	} {
		ask := packAsk()
		ask.Run.Argv = []string{"pack apply starter --depot web " + c.words}
		if _, err := y.AddAsk(y.ctx, y.bead, ask, "yardmaster"); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: AddAsk = %v, want %q", c.words, err, c.want)
		}
	}
	if notes, err := y.Notes(y.ctx, y.bead); err != nil || len(notes) != 0 {
		t.Fatalf("the refused asks left %d notes, %v", len(notes), err)
	}
	// A taken flow whose pack version lacks a stage an open bead stands in:
	// the pack's flow is the reading's target, and the beads that follow the
	// flow the yard has are the ones it would strand.
	have, err := ParseFlow("in -> work\nwork -> done\ndone terminal\n")
	if err != nil {
		t.Fatal(err)
	}
	have.Name = "starter.default"
	if _, err := y.PutFlow(y.ctx, *have, "mckean"); err != nil {
		t.Fatal(err)
	}
	if err := y.SetFlowPointer(y.ctx, "web", "", "starter.default", "mckean"); err != nil {
		t.Fatal(err)
	}
	pack, err := ParseFlow("in -> done\ndone terminal\n")
	if err != nil {
		t.Fatal(err)
	}
	pack.Name = "starter.default"
	raw, err := json.Marshal(pack)
	if err != nil {
		t.Fatal(err)
	}
	f.targets = map[string]string{"flow\x00starter.default": string(raw)}
	ask := packAsk()
	ask.Run.Argv = []string{"pack apply starter --depot web --take flow/starter.default"}
	seq := y.ask(t, ask, "yardmaster") // no bead stands in work yet
	bead, err := y.CreateBead(y.ctx, NewBead{Title: "stranded", Depot: "web", Stage: "work"}, "mckean")
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{
		func() error { _, err := y.AddAsk(y.ctx, y.bead, ask, "yardmaster"); return err }(),
		func() error { _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); return err }(),
	} {
		if err == nil || !strings.Contains(err.Error(), "--take flow/starter.default: the pack's flow lacks the stage of open beads") ||
			!strings.Contains(err.Error(), bead.ID+" (work)") || !strings.Contains(err.Error(), "yardr bead update <id> --meta flow=<name>") {
			t.Errorf("a take that strands %s = %v, want it refused naming the bead", bead.ID, err)
		}
	}
	// Taking a flow that strands no bead, or a pack version that keeps the
	// stage, is no refusal: the pre-check names only what the write would.
	keeps, err := ParseFlow("in -> work\nwork -> done\ndone terminal\n")
	if err != nil {
		t.Fatal(err)
	}
	keeps.Name = "starter.default"
	if raw, err = json.Marshal(keeps); err != nil {
		t.Fatal(err)
	}
	f.targets = map[string]string{"flow\x00starter.default": string(raw)}
	if _, err := y.AddAsk(y.ctx, y.bead, ask, "yardmaster"); err != nil {
		t.Errorf("a take of a flow that keeps the stage = %v, want a card", err)
	}
}
