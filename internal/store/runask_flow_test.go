package store

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func flowSetAsk(name, text string) Ask {
	return Ask{Question: "Set the flow?", To: "mckean", Context: []string{"the line changes"},
		Run: &AskRun{Argv: []string{"flow", "set", name}, Flow: text}}
}

// A flow set card carries the whole flow, approves a reorder-only
// change as code, and is stale where the flow moved since the ask,
// running nothing.
func TestRunAskFlowSetCarriesTheWholeFlow(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	f, err := ParseFlow("in -> work\nwork -> done\ndone terminal\n")
	if err != nil {
		t.Fatal(err)
	}
	f.Name = "code"
	if _, err := y.PutFlow(y.ctx, *f, "mckean"); err != nil {
		t.Fatal(err)
	}
	seq := y.ask(t, flowSetAsk("code", `{"stages":["work","in","done"],"transitions":{"in":["work"],"work":["done"]},"terminal":["done"]}`), "yardmaster")
	r := y.asked(t, seq)
	if len(r.Ask.Weight) != 1 || r.Ask.Weight[0].Class != WeightCode || !StrongWeight(r.Ask.Weight) {
		t.Fatalf("weight = %+v, want one code line, which is strong, for a reorder alone", r.Ask.Weight)
	}
	strongHas(t, r.Ask.Run.Does,
		"changes the flow code whole",
		"Its stages stand in the order in, work, done now; on approval they stand work, in, done.",
		"No stage is added or removed: the order changes",
		"The flow that stands, whole:", "in -> work", "work -> done",
		"On approval the flow stands as the lines below write it, whole:", "work -> done", "in -> work")
	if !strings.Contains(r.Ask.Run.Back, "yardr flow set code") || r.Ask.Run.BackBy != "only the operator" {
		t.Errorf("taken back by %q (%s)", r.Ask.Run.Back, r.Ask.Run.BackBy)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil); err == nil || !strings.Contains(err.Error(), "weighty") {
		t.Fatalf("unweighed approval = %v, want a weighty refusal", err)
	}
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if done.Result != "flow code: 3 stages" {
		t.Errorf("approval printed %q", done.Result)
	}
	got, err := y.GetFlow(y.ctx, "code")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Stages, []string{"work", "in", "done"}) {
		t.Errorf("the flow stands %v, want the card's order", got.Stages)
	}
	// Moved since the ask, a second card is stale and runs nothing.
	seq = y.ask(t, flowSetAsk("code", "in -> work\nwork -> done\ndone terminal\n"), "yardmaster")
	other, err := ParseFlow("in -> done\ndone terminal\n")
	if err != nil {
		t.Fatal(err)
	}
	other.Name = "code"
	if _, err := y.PutFlow(y.ctx, *other, "mckean"); err != nil {
		t.Fatal(err)
	}
	y.refusedStale(t, seq, "was written when the flow code was another: it was set since")
	if got, _ := y.GetFlow(y.ctx, "code"); !slices.Equal(got.Stages, []string{"in", "done"}) {
		t.Errorf("a refused approval changed the flow: %v", got.Stages)
	}
}

// A flow set card that removes a stage with an open bead in it is
// refused naming the bead, at the ask and again at approval, and a flow
// no order can be made of is refused at the ask. A flow a pack manages
// gets the same refusals, at the ask.
func TestRunAskFlowSetRefusesToStrandABeadTwice(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	if err := y.PutDepot(y.ctx, Depot{Name: "web"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	f, err := ParseFlow("in -> work\nwork -> done\ndone terminal\n")
	if err != nil {
		t.Fatal(err)
	}
	f.Name = "code"
	if _, err := y.PutFlow(y.ctx, *f, "mckean"); err != nil {
		t.Fatal(err)
	}
	if err := y.SetFlowPointer(y.ctx, "web", "", "code", "mckean"); err != nil {
		t.Fatal(err)
	}
	removing := "in -> done\ndone terminal\n"
	// At approval: the bead arrives after the ask was filed.
	seq := y.ask(t, flowSetAsk("code", removing), "yardmaster")
	bead, err := y.CreateBead(y.ctx, NewBead{Title: "stranded", Depot: "web", Stage: "work"}, "mckean")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil ||
		!strings.Contains(err.Error(), bead.ID) || !strings.Contains(err.Error(), nothingRan) {
		t.Fatalf("approving a stranding = %v, want it refused naming %s", err, bead.ID)
	}
	if got, _ := y.GetFlow(y.ctx, "code"); !slices.Contains(got.Stages, "work") {
		t.Errorf("a refused approval changed the flow: %v", got.Stages)
	}
	// At the ask: the bead already stands there.
	if _, err := y.AddAsk(y.ctx, y.bead, flowSetAsk("code", removing), "yardmaster"); err == nil ||
		!strings.Contains(err.Error(), bead.ID) || !strings.Contains(err.Error(), "lacks the stage of open beads") {
		t.Errorf("asking a stranding = %v, want it refused naming %s", err, bead.ID)
	}
	// An unorderable flow is refused in the terminal command's words.
	unorderable := "{\"stages\":[],\"transitions\":{\"a\":[\"b\"],\"c\":[\"d\"]}}"
	if _, err := y.AddAsk(y.ctx, y.bead, flowSetAsk("code", unorderable), "yardmaster"); err == nil ||
		!strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("an unorderable flow = %v", err)
	}
	// A flow a pack manages is held to both as well: no pack refusal comes
	// before them, and they still refuse in their own words.
	if _, err := y.RecordPack(y.ctx, PackImport{Pack: "starter", Depot: "web",
		Objects: []PackObject{{Kind: "flow", Name: "code"}}}, true, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.AddAsk(y.ctx, y.bead, flowSetAsk("code", removing), "yardmaster"); err == nil ||
		!strings.Contains(err.Error(), bead.ID) || !strings.Contains(err.Error(), "lacks the stage of open beads") {
		t.Errorf("asking a stranding of a pack's flow = %v, want it refused naming %s", err, bead.ID)
	}
	if _, err := y.AddAsk(y.ctx, y.bead, flowSetAsk("code", unorderable), "yardmaster"); err == nil ||
		!strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("an unorderable pack flow = %v", err)
	}
}

// A flow set card carries a flow a pack manages as it carries any store
// flow, and says so: whose the flow is, what pack apply then does with the
// yard's version, and the door to the pack's (--take). The approval saves
// the flow and leaves the import record as the import wrote it, which is
// what lets the next pack apply read the change as the yard's and keep it.
// A card filed before the flow was a pack's is another card once it is, and
// one whose flow a --take moved is stale; neither runs anything.
func TestRunAskFlowSetCarriesAPackManagedFlow(t *testing.T) {
	t.Parallel()
	y := listYard(t)
	if err := y.PutDepot(y.ctx, Depot{Name: "web"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	f, err := ParseFlow("in -> work\nwork -> done\ndone terminal\n")
	if err != nil {
		t.Fatal(err)
	}
	f.Name = "code"
	if _, err := y.PutFlow(y.ctx, *f, "mckean"); err != nil {
		t.Fatal(err)
	}
	text := `{"stages":["work","in","done"],"transitions":{"in":["work"],"work":["done"]},"terminal":["done"]}`
	early := y.ask(t, flowSetAsk("code", text), "yardmaster")
	// The import wrote the flow as it stands, so its record holds the digest.
	digest, err := f.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := y.RecordPack(y.ctx, PackImport{Pack: "starter", Depot: "web",
		Objects: []PackObject{{Kind: "flow", Name: "code", SHA256: digest}}}, true, "mckean"); err != nil {
		t.Fatal(err)
	}
	y.refusedStale(t, early, `the yard would say "The flow is the pack starter's`)

	// Asked of the pack's flow, the card carries.
	seq := y.ask(t, flowSetAsk("code", text), "yardmaster")
	r := y.asked(t, seq)
	strongHas(t, r.Ask.Run.Does,
		"changes the flow code whole",
		"The flow is the pack starter's, imported into depot web: this sets the yard's version of it, and changes nothing of the pack.",
		"yardr pack apply then keeps the yard's version, whatever the pack moves, and sets the pack's again only where it is told to (--take flow/code).",
		"Its stages stand in the order in, work, done now; on approval they stand work, in, done.")
	for _, want := range []string{"yardr flow set code with the flow that stands", "yardr pack apply starter --depot web --take flow/code"} {
		if !strings.Contains(r.Ask.Run.Back, want) {
			t.Errorf("taken back by %q, want it to name %q", r.Ask.Run.Back, want)
		}
	}
	before, err := y.Packs(y.ctx)
	if err != nil {
		t.Fatal(err)
	}
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if done.Result != "flow code: 3 stages" {
		t.Errorf("approval printed %q", done.Result)
	}
	if got, _ := y.GetFlow(y.ctx, "code"); !slices.Equal(got.Stages, []string{"work", "in", "done"}) {
		t.Errorf("the flow stands %v, want the card's order", got.Stages)
	}
	if after, err := y.Packs(y.ctx); err != nil || !reflect.DeepEqual(before, after) {
		t.Errorf("the approval changed the import record: %+v, then %+v (%v)", before, after, err)
	}

	// Writing the pack's version after the ask moves the flow the card saw:
	// the card is stale and the flow stays as the write left it.
	seq = y.ask(t, flowSetAsk("code", "in -> done\ndone terminal\n"), "yardmaster")
	if _, err := y.PutFlow(y.ctx, *f, "mckean"); err != nil {
		t.Fatal(err)
	}
	y.refusedStale(t, seq, "was written when the flow code was another: it was set since")
	if got, _ := y.GetFlow(y.ctx, "code"); !slices.Equal(got.Stages, []string{"in", "work", "done"}) {
		t.Errorf("a refused approval changed the flow: %v", got.Stages)
	}

	// A flow a pack's record lists that the store lacks is added in the same
	// words, with the same door to the pack's version.
	if _, err := y.RecordPack(y.ctx, PackImport{Pack: "extra", Depot: "web",
		Objects: []PackObject{{Kind: "flow", Name: "ghost"}}}, true, "mckean"); err != nil {
		t.Fatal(err)
	}
	seq = y.ask(t, flowSetAsk("ghost", "in -> done\ndone terminal\n"), "yardmaster")
	r = y.asked(t, seq)
	strongHas(t, r.Ask.Run.Does, "adds the flow ghost", "The flow is the pack extra's, imported into depot web", "(--take flow/ghost)")
	if !strings.Contains(r.Ask.Run.Back, "yardr flow rm ghost") || !strings.Contains(r.Ask.Run.Back, "yardr pack apply extra --depot web --take flow/ghost") {
		t.Errorf("taken back by %q", r.Ask.Run.Back)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := y.GetFlow(y.ctx, "ghost"); err != nil || !slices.Equal(got.Stages, []string{"in", "done"}) {
		t.Errorf("the flow the pack's record lists stands %v (%v), want the card's", got, err)
	}
}
