package store

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// routeYard is a list yard with a depot web, a depot api, and the
// herdr group spares beside listYard's builders and reviewers.
func routeYard(t *testing.T) *askYard {
	t.Helper()
	y := listYard(t)
	for _, depot := range []string{"web", "api"} {
		if err := y.PutDepot(y.ctx, Depot{Name: depot}, "mckean"); err != nil {
			t.Fatal(err)
		}
	}
	if err := y.PutGroup(y.ctx, Group{Name: "spares", Runner: "herdr", Config: map[string]any{"kind": "claude"}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if err := y.PutGroup(y.ctx, Group{Name: "locals", Runner: "herdr", Config: map[string]any{"kind": "claude"}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	return y
}

func routesByID(t *testing.T, y *askYard) map[string]Route {
	t.Helper()
	rs, err := y.Routes(y.ctx)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]Route{}
	for _, r := range rs {
		out[r.ID] = r
	}
	return out
}

// A route set card moves the one named rule of a depot to the named
// group from the next claim, its filters and conditions unchanged,
// and leaves a second rule of the stage serving its beads. It is
// weighty code, stale by its rule alone, and a yard-wide rule is no
// card's to change where it stands.
func TestRunAskRouteSetMovesOneNamedRule(t *testing.T) {
	t.Parallel()
	y := routeYard(t)
	web := "web"
	eyes := "eyes"
	plain, err := y.AddRoute(y.ctx, Route{Stage: "new", Depot: &web, Group: "builders"}, "mckean")
	if err != nil {
		t.Fatal(err)
	}
	labelled, err := y.AddRoute(y.ctx, Route{Stage: "new", Depot: &web, Label: &eyes, Group: "reviewers"}, "mckean")
	if err != nil {
		t.Fatal(err)
	}
	seq := y.ask(t, listAsk("mckean", "route", "set", plain.ID, "--group", "spares"), "yardmaster")
	r := y.asked(t, seq)
	if want := []string{"route", "set", plain.ID, "--group", "spares"}; !slices.Equal(r.Ask.Run.Argv, want) {
		t.Errorf("stored command = %q, want %q", r.Ask.Run.Argv, want)
	}
	if len(r.Ask.Weight) != 1 || r.Ask.Weight[0].Class != WeightCode || !StrongWeight(r.Ask.Weight) {
		t.Fatalf("weight = %+v, want one code line, which is strong", r.Ask.Weight)
	}
	strongHas(t, r.Ask.Run.Does,
		"changes what serves the stage new for the beads route "+plain.ID+" takes",
		"Route "+plain.ID+" is this depot's own rule in depot web, for the stage new.",
		"It takes type any (unset) · labels none (unset) · bead priority any (unset) · for every originator.",
		"Its chain is builders now",
		"On approval its chain is spares: ",
		"every filter and every condition stands as it is.")
	if r.Ask.Run.Description != "Change where a route's beads go and keep the rest" || r.Ask.Run.BackBy != "only the operator" {
		t.Errorf("description %q, taken back by %q", r.Ask.Run.Description, r.Ask.Run.BackBy)
	}
	if want := "yardr route set " + plain.ID + " --group builders"; r.Ask.Run.Back != want {
		t.Errorf("taken back by %q, want %q", r.Ask.Run.Back, want)
	}
	// A weighty card approved without its person seeing the weight
	// runs nothing.
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil); err == nil || !strings.Contains(err.Error(), "weighty") {
		t.Fatalf("unweighed approval = %v, want a weighty refusal", err)
	}
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(done.Result, "route "+plain.ID+" saved") {
		t.Errorf("approval printed %q", done.Result)
	}
	got := routesByID(t, y)
	if got[plain.ID].Group != "spares" || got[plain.ID].Worker() != "spares" {
		t.Errorf("the named rule = %+v, want it serving through spares", got[plain.ID])
	}
	if got[labelled.ID].Group != "reviewers" || got[labelled.ID].Label == nil || *got[labelled.ID].Label != "eyes" {
		t.Errorf("the second rule = %+v, want it unchanged serving its labelled beads", got[labelled.ID])
	}
	// The operator at a terminal has no card to ask for.
	if _, err := y.AddAsk(y.ctx, y.bead, listAsk("mckean", "route", "set", plain.ID, "--group", "builders"), "mckean"); err == nil ||
		!strings.Contains(err.Error(), "needs no approval: run it yourself") {
		t.Errorf("the operator's ask = %v", err)
	}
	// A yard-wide rule is refused, naming the doors that stand.
	wide, err := y.AddRoute(y.ctx, Route{Stage: "review", Group: "reviewers"}, "mckean")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := y.AddAsk(y.ctx, y.bead, listAsk("mckean", "route", "set", wide.ID, "--group", "spares"), "yardmaster"); err == nil ||
		!strings.Contains(err.Error(), "is yard-wide") || !strings.Contains(err.Error(), "route add --depot <depot>") {
		t.Errorf("a yard-wide rule's card = %v", err)
	}
}

// A route set card is stale by its rule alone: the stage's other rule
// set since leaves it approvable, and its own rule set since refuses
// it, running nothing.
func TestRunAskRouteSetIsStaleByItsRuleAlone(t *testing.T) {
	t.Parallel()
	y := routeYard(t)
	web := "web"
	eyes := "eyes"
	plain, err := y.AddRoute(y.ctx, Route{Stage: "new", Depot: &web, Group: "builders"}, "mckean")
	if err != nil {
		t.Fatal(err)
	}
	labelled, err := y.AddRoute(y.ctx, Route{Stage: "new", Depot: &web, Label: &eyes, Group: "reviewers"}, "mckean")
	if err != nil {
		t.Fatal(err)
	}
	seq := y.ask(t, listAsk("mckean", "route", "set", plain.ID, "--group", "spares"), "yardmaster")
	g := RouteGroup{Name: "spares"}
	if _, err := y.SetRoute(y.ctx, labelled.ID, RouteEdit{Group: &g}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err != nil {
		t.Fatalf("an approval after the other rule moved = %v", err)
	}
	if got := routesByID(t, y); got[plain.ID].Group != "spares" || got[labelled.ID].Group != "spares" {
		t.Errorf("routes = %+v", got)
	}
	seq = y.ask(t, listAsk("mckean", "route", "set", plain.ID, "--group", "builders"), "yardmaster")
	g = RouteGroup{Name: "reviewers"}
	if _, err := y.SetRoute(y.ctx, plain.ID, RouteEdit{Group: &g}, "mckean"); err != nil {
		t.Fatal(err)
	}
	y.refusedStale(t, seq, "was written when route "+plain.ID+" was another: it was set since")
	if got := routesByID(t, y); got[plain.ID].Group != "reviewers" {
		t.Errorf("a refused approval changed the rule: %+v", got[plain.ID])
	}
}

// A set-aside card approved for a yard-wide rule writes only the one
// depot's own rule: the yard-wide rule and the other depot stand, the
// depot's other keys still take the yard-wide rule, and the card
// names the rule, its scope and its key-kept sentence. Filing refuses
// where an own rule for the key already stands, and the card is stale
// where the yard-wide rule moved or an own rule appeared.
func TestRunAskRouteAddSetsAYardWideRuleAsideForOneDepot(t *testing.T) {
	t.Parallel()
	y := routeYard(t)
	for _, name := range []string{"web-spares", "api-spares"} {
		if err := y.PutGroup(y.ctx, Group{Name: name, Runner: "herdr", Config: map[string]any{"kind": "claude"}}, "mckean"); err != nil {
			t.Fatal(err)
		}
	}
	typ, security, docs := "task", "security", "docs"
	priority := 2
	wide, err := y.AddRoute(y.ctx, Route{Stage: "review", Type: &typ, Label: &security, BeadPriority: &priority, Group: "reviewers", When: "Weekly<80",
		Else: []RouteGroup{{Name: "{depot}-spares", When: "Weekly<90"}}}, "mckean")
	if err != nil {
		t.Fatal(err)
	}
	other, err := y.AddRoute(y.ctx, Route{Stage: "review", Type: &typ, Label: &docs, Group: "reviewers"}, "mckean")
	if err != nil {
		t.Fatal(err)
	}
	ask := listAsk("mckean", "route", "add", "--stage", "review", "--type", "task", "--label", "security", "--bead-priority", "2", "--group", "locals", "--depot", "web")
	seq := y.ask(t, ask, "yardmaster")
	r := y.asked(t, seq)
	if len(r.Ask.Weight) != 1 || r.Ask.Weight[0].Class != WeightCode || !StrongWeight(r.Ask.Weight) {
		t.Fatalf("weight = %+v, want one code line, which is strong", r.Ask.Weight)
	}
	strongHas(t, r.Ask.Run.Does,
		"sets aside the yard-wide rule "+wide.ID+" for depot web alone",
		"Route "+wide.ID+" is yard-wide, for the stage review: it takes type task · labels security · beads P0-P2 · for the operator (unset)",
		"Its chain is reviewers@Weekly<80 else {depot}-spares@Weekly<90:",
		"The own rule will take the same beads in depot web to ",
		"with only the first group changed",
		"If the yard-wide rule's filters change later, this own rule keeps the key it is filed with",
		"no field on either rule points at the other",
		"The yard-wide rule itself, every other depot that keeps it, and depot web's beads of any other key stand as they are.")
	strongHas(t, []string{r.Ask.Run.Back}, "yardr route rm <its id> in a terminal")
	if r.Ask.Run.BackBy != "only the operator" {
		t.Errorf("taken back by %q", r.Ask.Run.BackBy)
	}
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := y.Routes(y.ctx)
	if err != nil {
		t.Fatal(err)
	}
	groups, err := y.Groups(y.ctx)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Group{}
	for _, g := range groups {
		byName[g.Name] = g
	}
	var own *Route
	for i := range rs {
		if rs[i].Depot != nil && *rs[i].Depot == "web" && rs[i].Stage == "review" {
			own = &rs[i]
		}
	}
	if own == nil || own.Group != "locals" || own.GroupList() != "locals@Weekly<80 else web-spares@Weekly<90" ||
		own.Type == nil || *own.Type != typ || own.Label == nil || *own.Label != security || own.BeadPriority == nil || *own.BeadPriority != priority {
		t.Fatalf("the own rule in web = %+v, want the source chain resolved for web with only its first group changed", own)
	}
	if !strings.Contains(done.Result, "route "+own.ID+" added") || !strings.Contains(done.Result, "setting the yard-wide rule "+wide.ID+" aside") {
		t.Errorf("approval printed %q, want it to name the new rule and the rule set aside", done.Result)
	}
	if got := routesByID(t, y); got[wide.ID].Group != "reviewers" || got[wide.ID].Depot != nil {
		t.Errorf("the yard-wide rule moved: %+v", got[wide.ID])
	}
	// The matching key moves to web's own rule, while api and web's
	// different label key keep the yard-wide routes they had before.
	for _, tc := range []struct {
		depot, label, want string
	}{{"web", security, own.ID}, {"api", security, wide.ID}, {"web", docs, other.ID}} {
		bead := Bead{Stage: "review", Type: typ, Priority: 1, Depot: &tc.depot, Labels: []string{tc.label}, Originator: "mckean"}
		got := MatchRoute(rs, bead, "mckean", byName)
		if got == nil || got.ID != tc.want {
			t.Errorf("depot %s label %s matched %+v, want route %s", tc.depot, tc.label, got, tc.want)
		}
	}
	// Filing again refuses: an own rule for the key already stands.
	if _, err := y.AddAsk(y.ctx, y.bead, ask, "yardmaster"); err == nil || !strings.Contains(err.Error(), "already takes these beads in depot web") {
		t.Errorf("filing over an own rule = %v", err)
	}
	people := Group{Name: "people", Runner: "manual", Members: []string{"mckean"}}
	if err := y.PutGroup(y.ctx, people, "mckean"); err != nil {
		t.Fatal(err)
	}
	operatorRule, err := y.AddRoute(y.ctx, Route{Stage: "manual-review", Group: "reviewers"}, "mckean")
	if err != nil {
		t.Fatal(err)
	}
	seq = y.ask(t, listAsk("mckean", "route", "add", "--stage", "manual-review", "--group", "people", "--originator", "mckean", "--depot", "web"), "yardmaster")
	r = y.asked(t, seq)
	strongHas(t, r.Ask.Run.Does, "Route "+operatorRule.ID+" is yard-wide", "for the operator (unset)")
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	rs, err = y.Routes(y.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(rs, func(r Route) bool {
		return r.Stage == "manual-review" && r.Depot != nil && *r.Depot == "web" && r.Originator == nil
	}) {
		t.Errorf("the manual set-aside did not keep the yard-wide rule's operator-only key: %+v", rs)
	}
}

// A set-aside card is stale where the yard-wide rule moved since the
// ask, and where an own rule for its key appeared in the depot since:
// each approval runs nothing and leaves the yard as it found it.
func TestRunAskRouteAddSetAsideIsHeldToItsRules(t *testing.T) {
	t.Parallel()
	mk := func(t *testing.T) (*askYard, *Route) {
		y := routeYard(t)
		wide, err := y.AddRoute(y.ctx, Route{Stage: "review", Group: "reviewers"}, "mckean")
		if err != nil {
			t.Fatal(err)
		}
		return y, wide
	}
	askFor := func() Ask {
		return listAsk("mckean", "route", "add", "--stage", "review", "--group", "locals", "--depot", "web")
	}
	y, wide := mk(t)
	seq := y.ask(t, askFor(), "yardmaster")
	g := RouteGroup{Name: "spares"}
	if _, err := y.SetRoute(y.ctx, wide.ID, RouteEdit{Group: &g}, "mckean"); err != nil {
		t.Fatal(err)
	}
	y.refusedStale(t, seq, "was written when the yard-wide rule "+wide.ID+" was another: it was set since")
	if rs := routesByID(t, y); len(rs) != 1 {
		t.Errorf("a refused approval wrote a rule: %v", rs)
	}
	y, wide = mk(t)
	_ = wide
	seq = y.ask(t, askFor(), "yardmaster")
	web := "web"
	if _, err := y.AddRoute(y.ctx, Route{Stage: "review", Depot: &web, Group: "spares"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	y.refusedStale(t, seq, "stated no own route for these beads: one has appeared since")
	if rs, _ := y.Routes(y.ctx); len(rs) != 2 {
		t.Errorf("routes = %v, want the yard-wide rule and the own one that appeared", rs)
	}
}

// A route add card where no rule stands adds the rule in its scope,
// yard-wide without --depot and the depot's own with it.
func TestRunAskRouteAddWhereNoRuleStands(t *testing.T) {
	t.Parallel()
	y := routeYard(t)
	seq := y.ask(t, listAsk("mckean", "route", "add", "--stage", "fresh", "--group", "builders"), "yardmaster")
	r := y.asked(t, seq)
	strongHas(t, r.Ask.Run.Does, "adds a rule for the stage fresh where none stands in its scope: a yard default")
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	seq = y.ask(t, listAsk("mckean", "route", "add", "--stage", "fresh", "--group", "builders", "--originator", "mckean", "--depot", "api"), "yardmaster")
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	rs, err := y.Routes(y.ctx)
	if err != nil {
		t.Fatal(err)
	}
	var scopes []string
	for _, r := range rs {
		if r.Stage != "fresh" {
			continue
		}
		if r.Depot == nil {
			scopes = append(scopes, "yard-wide")
		} else {
			scopes = append(scopes, *r.Depot)
		}
	}
	slices.Sort(scopes)
	if !slices.Equal(scopes, []string{"api", "yard-wide"}) {
		t.Errorf("the fresh rules stand in %v, want a yard default and api's own", scopes)
	}
	people := Group{Name: "people", Runner: "manual", Members: []string{"mckean"}}
	if err := y.PutGroup(y.ctx, people, "mckean"); err != nil {
		t.Fatal(err)
	}
	seq = y.ask(t, listAsk("mckean", "route", "add", "--stage", "fresh-manual", "--group", "people"), "yardmaster")
	r = y.asked(t, seq)
	strongHas(t, r.Ask.Run.Does, "for every originator")
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	rs, err = y.Routes(y.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(rs, func(r Route) bool {
		return r.Stage == "fresh-manual" && r.Originator != nil && *r.Originator == OriginatorAny
	}) {
		t.Errorf("manual route add did not keep the card's all-originator key: %+v", rs)
	}
	web := "web"
	if _, err := y.AddRoute(y.ctx, Route{Stage: "tied-manual", Depot: &web, Group: "people"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.AddAsk(y.ctx, y.bead, listAsk("mckean", "route", "add", "--stage", "tied-manual", "--group", "people", "--depot", "web"), "yardmaster"); err == nil || !strings.Contains(err.Error(), "already takes these beads") {
		t.Errorf("a duplicate manual route key was filed: %v", err)
	}
	if _, err := y.AddAsk(y.ctx, y.bead, listAsk("mckean", "route", "add", "--stage", "bad-originator", "--group", "builders", "--originator", "missing"), "yardmaster"); err == nil {
		t.Error("a route for a user the yard does not have was filed")
	}
}

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
