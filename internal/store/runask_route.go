// The route rows of the run list (runList): what serves a stage, changed
// on a card and set only on the operator's approval (design yardr-y1cn
// 3.3, as decided on ask 26833: every such card is weighty code, a
// reorder-only one included). route set moves one named rule of a depot,
// never a stage in the abstract, and never a yard-wide rule where it
// stands; route add files a new rule where a stage has none, and, with
// --depot naming a yard-wide rule's key, that depot's own set-aside of
// it. A card shows the rule whole as it stands and as it would stand,
// and is stale by the rule it names alone. The yardmaster files these
// cards from the operator's own mail only; mail from anyone else gets
// no filing.
//
// The rows are registered here, beside their functions, as the group
// set row is (runask_groupset.go), and not in runList's literal:
// runask.go stands at the file-size ceiling the test gate holds
// (.yardr/test).
package store

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// ActChangeRoute is the gate the route cards ask through may
// (policy.go): what names what runs is the operator's, beside its row
// here as the row is registered here.
const ActChangeRoute Action = "change what serves a stage"

const routeSetUse = "yardr route set <id> --group <group>"
const routeAddUse = "yardr route add --stage <stage> --group <group> [--depot <depot>]"

func init() {
	runList = append(runList,
		runCommand{
			words:       []string{"route", "set"},
			use:         routeSetUse,
			description: "Change where a route's beads go and keep the rest",
			parse:       parseRouteSet,
			check:       checkRouteSet,
			card:        (*wtx).routeSetCard,
			stale:       (*wtx).routeSetStale,
			weigh:       routeSetWeight,
			run:         runRouteSet,
		},
		runCommand{
			words:       []string{"route", "add"},
			use:         routeAddUse,
			description: "Send open, unblocked beads in a stage to a group",
			parse:       parseRouteAdd,
			check:       checkRouteAdd,
			card:        (*wtx).routeAddCard,
			stale:       (*wtx).routeAddStale,
			weigh:       routeAddWeight,
			run:         runRouteAdd,
		})
}

// parseRouteSet reads the words of route set <id> --group <group>. The
// group is a name alone: the condition the route's first group carries
// is the route's, and stands as the card has it.
func parseRouteSet(rest []string) ([]string, error) {
	args, values, err := runWords(routeSetUse, "route set", "the operator's", rest, map[string]bool{"--group": true})
	if err != nil {
		return nil, err
	}
	if len(args) != 1 {
		return nil, fmt.Errorf("--run takes %s: one route id, not %d words", routeSetUse, len(args))
	}
	if len(values["--group"]) != 1 {
		return nil, fmt.Errorf("--run takes %s: one --group <group>", routeSetUse)
	}
	group := values["--group"][0]
	if strings.Contains(group, "@") {
		return nil, fmt.Errorf("--run takes %s: %q names a condition with its group, and this card carries the route's conditions unchanged: name the group alone", routeSetUse, group)
	}
	return []string{args[0], "--group", group}, nil
}

// routeSetOf is the route id and the group of a parsed route set.
func routeSetOf(argv []string) (id, group string) { return argv[2], argv[4] }

// parseRouteAdd reads the words of route add as the terminal takes
// them: the stage, the group, and the filters the rule sets, each as
// route add spells it. A set-aside is read from them, not named: with
// --depot, a rule whose key a yard-wide rule already takes carries
// that rule's key and chain whole (routeAddProposal).
func parseRouteAdd(rest []string) ([]string, error) {
	args, values, err := runWords(routeAddUse, "route add", "the operator's", rest, map[string]bool{
		"--stage": true, "--group": true, "--depot": true, "--type": true,
		"--label": true, "--bead-priority": true, "--originator": true, "--else": true})
	if err != nil {
		return nil, err
	}
	if len(args) != 0 {
		return nil, fmt.Errorf("--run takes %s: it takes no word but its flags, not %q", routeAddUse, args[0])
	}
	for _, flag := range []string{"--stage", "--group", "--depot", "--type", "--bead-priority", "--originator"} {
		if len(values[flag]) > 1 {
			return nil, fmt.Errorf("--run takes %s: %s once", routeAddUse, flag)
		}
	}
	if len(values["--stage"]) != 1 || len(values["--group"]) != 1 {
		return nil, fmt.Errorf("--run takes %s: --stage and --group, each once", routeAddUse)
	}
	out := []string{"--stage", values["--stage"][0]}
	if v := values["--type"]; len(v) == 1 {
		out = append(out, "--type", v[0])
	}
	if v := values["--depot"]; len(v) == 1 {
		out = append(out, "--depot", v[0])
	}
	for _, label := range values["--label"] {
		out = append(out, "--label", label)
	}
	if v := values["--bead-priority"]; len(v) == 1 {
		if _, err := strconv.Atoi(v[0]); err != nil {
			return nil, fmt.Errorf("--run takes %s: --bead-priority wants a number, not %q", routeAddUse, v[0])
		}
		out = append(out, "--bead-priority", v[0])
	}
	if v := values["--originator"]; len(v) == 1 {
		out = append(out, "--originator", v[0])
	}
	out = append(out, "--group", values["--group"][0])
	for _, e := range values["--else"] {
		out = append(out, "--else", e)
	}
	return out, nil
}

// routeAddOf is the route a parsed route add names, before its id: the
// match key and the chain whole, as AddRoute would store them.
func routeAddOf(argv []string) Route {
	var r Route
	for i := 2; i+1 < len(argv); i += 2 {
		switch argv[i] {
		case "--stage":
			r.Stage = argv[i+1]
		case "--type":
			r.Type = &argv[i+1]
		case "--depot":
			r.Depot = &argv[i+1]
		case "--label":
			r.AndLabels = append(r.AndLabels, argv[i+1])
		case "--bead-priority":
			n, _ := strconv.Atoi(argv[i+1])
			r.BeadPriority = &n
		case "--originator":
			r.Originator = &argv[i+1]
		case "--group":
			g := ParseRouteGroup(argv[i+1])
			r.Group, r.When = g.Name, g.When
		case "--else":
			r.Else = append(r.Else, ParseRouteGroup(argv[i+1]))
		}
	}
	if len(r.AndLabels) > 0 {
		r.Label, r.AndLabels = &r.AndLabels[0], r.AndLabels[1:]
		if len(r.AndLabels) == 0 {
			r.AndLabels = nil
		}
	}
	return r
}

// routeFiltersSays is a rule's filters as a card names them, each shown
// as unset where it is unset, never silently dropped.
func routeFiltersSays(r Route, operator string) string {
	typ := "type any (unset)"
	if r.Type != nil {
		typ = "type " + *r.Type
	}
	labels := "labels none (unset)"
	if ls := r.Labels(); len(ls) > 0 {
		labels = "labels " + strings.Join(ls, ", ")
	}
	priority := "bead priority any (unset)"
	if r.BeadPriority != nil {
		priority = "beads " + r.BeadPriorities()
	}
	originator := "for the operator (unset)"
	switch resolved := ResolvedOriginator(r.Originator, operator); {
	case resolved == OriginatorAny:
		originator = "for every originator"
	case resolved == operator:
	default:
		originator = "for " + resolved
	}
	return strings.Join([]string{typ, labels, priority, originator}, " · ")
}

// routeGroupSays is one group of a chain as a card names it: its runner,
// and for a herdr group its kind and model, for an exec group its cmd
// whole, for a manual group its members, for a peer group its yard.
func (w *wtx) routeGroupSays(name string) string {
	var g Group
	if ok, err := get(w.Tx, bGroups, name, &g); err != nil || !ok {
		return "the group " + name
	}
	says := fmt.Sprintf("the group %s (%s", name, g.Runner)
	switch g.Runner {
	case "herdr":
		if kind, _ := g.Config["kind"].(string); kind != "" {
			says += ", kind " + kind
		}
		if model, _ := g.Config["model"].(string); model != "" {
			says += ", model " + model
		}
	case "exec":
		if cmd, _ := g.Config["cmd"].(string); cmd != "" {
			says += ", cmd " + strconv.Quote(cmd)
		}
	case "manual":
		if len(g.Members) > 0 {
			says += ", members " + strings.Join(g.Members, ", ")
		}
	default:
		if yard := g.AtPeer(); yard != "" {
			says += ", yard " + yard
		}
	}
	return says + ")"
}

// routeSeen is what a route card keeps of the rule it names: the route
// whole, so any change of it, and no change of any other rule, makes
// the card stale.
func routeSeen(r Route) string { return seen(r) }

// routeSetSpec is a route set proposal as the yard holds it: the rule
// as it stands and as the approval would leave it, its first group
// changed and every filter and condition carried unchanged.
type routeSetSpec struct {
	have, want Route
}

// routeSetProposal holds a route set ask to the yard as it stands: the
// rule is there and is this depot's own, the group it would serve is
// there, and the chain that results is one the store would save
// (checkRouteGroups, as SetRoute holds it).
func (w *wtx) routeSetProposal(argv []string) (routeSetSpec, error) {
	id, group := routeSetOf(argv)
	spec := routeSetSpec{}
	if ok, err := get(w.Tx, bRoutes, id, &spec.have); err != nil {
		return routeSetSpec{}, err
	} else if !ok {
		return routeSetSpec{}, fmt.Errorf("route %s: %w", id, ErrNotFound)
	}
	if spec.have.Depot == nil {
		return routeSetSpec{}, fmt.Errorf("route %s is yard-wide: it serves every depot that states no own route for its beads, and a card does not change it where it stands; file route add --depot <depot> to set it aside for one depot, or change it in a terminal with yardr route set %s", id, id)
	}
	spec.want = spec.have
	spec.want.Group = group
	if _, err := checkRouteGroups(w.Tx, spec.want); err != nil {
		return routeSetSpec{}, err
	}
	return spec, nil
}

// checkRouteSet is changing what serves a stage as the asker: what
// names what runs is the operator's, which a run ask may propose.
func checkRouteSet(ctx context.Context, w *wtx, argv []string, actor string) error {
	spec, err := w.routeSetProposal(argv)
	if err != nil {
		return err
	}
	if spec.want.Group == spec.have.Group {
		return fmt.Errorf("route %s sends its beads to %s already, so the command would change nothing", spec.have.ID, spec.have.Group)
	}
	return w.may(ctx, actor, ActChangeRoute, Object{Kind: KindRoute, Name: spec.have.ID, Ask: Askable})
}

// routeSetCard is the card of route set <id> --group <group>: the rule
// named by its id and its scope, its filters and its chain whole as
// they stand and as they would stand, and what takes the change back.
func (w *wtx) routeSetCard(argv []string, asking bool) (AskRun, error) {
	spec, err := w.routeSetProposal(argv)
	if err != nil {
		return AskRun{}, err
	}
	have, want := spec.have, spec.want
	if asking && want.Group == have.Group {
		return AskRun{}, fmt.Errorf("route %s sends its beads to %s already, so the command would change nothing", have.ID, have.Group)
	}
	operator, err := operatorIn(w.Tx)
	if err != nil {
		return AskRun{}, err
	}
	open, err := w.openInStage(*have.Depot, have.Stage)
	if err != nil {
		return AskRun{}, err
	}
	does := []string{
		fmt.Sprintf("changes what serves the stage %s for the beads route %s takes, and nothing else of it.", have.Stage, have.ID),
		fmt.Sprintf("Route %s is this depot's own rule in depot %s, for the stage %s.", have.ID, *have.Depot, have.Stage),
		"It takes " + routeFiltersSays(have, operator) + ".",
		fmt.Sprintf("Its chain is %s now: %s serves its beads first.", have.GroupList(), w.routeGroupSays(have.Worker())),
		fmt.Sprintf("On approval its chain is %s: %s serves its beads first, and every filter and every condition stands as it is.", want.GroupList(), w.routeGroupSays(want.Worker())),
		fmt.Sprintf("%d open beads stand in the stage %s in depot %s; a bead claimed through the route keeps its group until its session ends, and the next claim takes the new chain.", open, have.Stage, *have.Depot),
	}
	return AskRun{
		Argv:   argv,
		Does:   does,
		Back:   runCommandLine([]string{"route", "set", have.ID, "--group", have.Groups()[0].String()}),
		BackBy: "only the operator",
		Seen:   map[string]string{"route " + have.ID: routeSeen(have)},
	}, nil
}

// openInStage counts the open beads in a stage of a depot.
func (w *wtx) openInStage(depot, stage string) (int, error) {
	n := 0
	err := each(w.Tx, bBeads, "", func(_ []byte, r *beadRow) error {
		if r.Status != "closed" && r.Stage == stage && r.Depot != nil && *r.Depot == depot {
			n++
		}
		return nil
	})
	return n, err
}

// routeSetStale says why the ask seq for route set is approved no
// more: the rule it names was set since. Nothing else makes it stale:
// a change of any other rule of the stage, and of the beads standing
// in it, is not this card's.
func (w *wtx) routeSetStale(seq uint64, was, now AskRun) error {
	id, _ := routeSetOf(was.Argv)
	if key := "route " + id; was.Seen[key] != now.Seen[key] {
		e := lastEvent(w.Tx, "route_set", func(e Event) bool {
			wasID, _ := e.Data["was"].(map[string]any)
			isID, _ := e.Data["is"].(map[string]any)
			return wasID["id"] == id || isID["id"] == id
		})
		return fmt.Errorf("ask %d was written when route %s was another: it was set since%s. %s", seq, id, since(e), nothingRan)
	}
	return nil
}

// routeSetWeight weighs a route set card: what it chooses is what runs
// next on every bead the rule takes, so it is code, always.
func routeSetWeight(_ *wtx, run AskRun) ([]AskWeight, error) {
	id, group := routeSetOf(run.Argv)
	return []AskWeight{weighs(WeightCode, fmt.Sprintf("what serves a stage: route %s sends its beads to the group %s from the next claim, as the card shows", id, group))}, nil
}

// runRouteSet sets the rule's first group as actor, in the approval's
// write, in the shape SetRoute writes (its was and is event), so what
// is saved is what the card showed.
func runRouteSet(ctx context.Context, w *wtx, argv []string, actor string, _ *approvedOn) (string, error) {
	spec, err := w.routeSetProposal(argv)
	if err != nil {
		return "", err
	}
	if err := put(w.Tx, bRoutes, spec.want.ID, spec.want); err != nil {
		return "", err
	}
	if err := w.event("", "route_set", actor, map[string]any{"was": routeEvent(spec.have), "is": routeEvent(spec.want)}); err != nil {
		return "", err
	}
	return fmt.Sprintf("route %s saved\n  --group: %s -> %s", spec.want.ID, spec.have.Groups()[0], spec.want.Groups()[0]), nil
}

// routeAddSpec is a route add proposal as the yard holds it: the rule
// it would add, and, where it sets a yard-wide rule aside for a depot,
// that rule whole.
type routeAddSpec struct {
	want   Route
	source *Route
}

// routeAddProposal holds a route add ask to the yard as it stands.
// With --depot, a rule whose key a yard-wide rule takes is that rule's
// set-aside: its key and its chain whole, with only the first group
// changed, and filing refuses where an own rule for the key already
// stands in the depot (checkRouteTie, which stays the backstop at the
// approval as at a terminal add). Without one, the rule adds where its
// scope stands. Every group it names is held as AddRoute holds it.
func (w *wtx) routeAddProposal(argv []string, asking bool) (routeAddSpec, error) {
	want := routeAddOf(argv)
	spec := routeAddSpec{want: want}
	if want.Stage == "" || want.Group == "" {
		return routeAddSpec{}, fmt.Errorf("--run takes %s: --stage and --group, each once", routeAddUse)
	}
	if want.BeadPriority != nil && *want.BeadPriority < 0 {
		return routeAddSpec{}, fmt.Errorf("bead priority %d: a bead's priority is 0 or more, 0 the most urgent", *want.BeadPriority)
	}
	labels := dedupeOrdered(want.Labels())
	want.Label, want.AndLabels = nil, nil
	if len(labels) > 0 {
		want.Label, want.AndLabels = &labels[0], labels[1:]
	}
	if len(want.AndLabels) == 0 {
		want.AndLabels = nil
	}
	operator, err := operatorIn(w.Tx)
	if err != nil {
		return routeAddSpec{}, err
	}
	if want.Depot != nil && w.Bucket(bDepots).Get([]byte(*want.Depot)) == nil {
		return routeAddSpec{}, fmt.Errorf("depot %s: %w", *want.Depot, ErrNotFound)
	}
	if want.Originator != nil && *want.Originator != OriginatorAny {
		if _, err := getUser(w.Tx, *want.Originator); err != nil {
			return routeAddSpec{}, fmt.Errorf("--originator: %w", err)
		}
	}
	// The first group's runner determines AddRoute's default originator.
	// Hold that default before finding a yard-wide key, checking ties or
	// writing the card, so the proposal and the saved route take the same
	// beads.
	firstOnly := want
	firstOnly.Else = nil
	first, err := checkRouteGroups(w.Tx, firstOnly)
	if err != nil {
		return routeAddSpec{}, err
	}
	if want.Originator == nil && first.Runner != "herdr" {
		any := OriginatorAny
		want.Originator = &any
	}
	if want.Depot != nil {
		key := want.matchKey(operator)
		if err := each(w.Tx, bRoutes, "", func(_ []byte, r *Route) error {
			if r.Depot == nil && r.matchKey(operator) == key {
				c := *r
				spec.source = &c
			}
			return nil
		}); err != nil {
			return routeAddSpec{}, err
		}
	}
	if spec.source != nil {
		src := spec.source
		if want.When != "" && want.When != src.When {
			return routeAddSpec{}, fmt.Errorf("a set-aside of the yard-wide rule %s carries its chain whole with only its first group changed: this card's first condition is %s, and the rule's is %s", src.ID, want.When, or(src.When, "none"))
		}
		carriedElse := slices.Clone(src.Else)
		for i := range carriedElse {
			carriedElse[i].Name = ResolveGroup(carriedElse[i].Name, *want.Depot)
		}
		if hasRouteFlag(argv, "--else") && !slices.Equal(want.Else, src.Else) && !slices.Equal(want.Else, carriedElse) {
			return routeAddSpec{}, fmt.Errorf("a set-aside of the yard-wide rule %s carries its chain whole with only its first group changed: this card's chain after the first group is %s, and the rule's is %s", src.ID, want.GroupList(), src.GroupList())
		}
		group, depot := want.Group, want.Depot
		want.Stage = src.Stage
		want.Type = src.Type
		want.Label = src.Label
		want.AndLabels = slices.Clone(src.AndLabels)
		want.BeadPriority = src.BeadPriority
		want.Originator = src.Originator
		want.Depot = depot
		want.Group = group
		want.When = src.When
		want.Else = carriedElse
		if want.Group == src.Group {
			return routeAddSpec{}, fmt.Errorf("route %s already takes these beads to %s: the command would change nothing", src.ID, src.GroupList())
		}
	}
	spec.want = want
	if _, err := checkRouteGroups(w.Tx, want); err != nil {
		return routeAddSpec{}, err
	}
	if asking {
		if err := checkRouteTie(w.Tx, want, operator); err != nil {
			return routeAddSpec{}, err
		}
	}
	return spec, nil
}

// hasRouteFlag reports whether argv contains a route add flag, apart from
// the value that follows it.
func hasRouteFlag(argv []string, flag string) bool {
	for _, arg := range argv {
		if arg == flag {
			return true
		}
	}
	return false
}

// ownRouteForKey is the own rule of depot that takes the match key of
// want, "" where none stands: what a set-aside's card is held to.
func (w *wtx) ownRouteForKey(want Route, operator string) (string, error) {
	key, id := want.matchKey(operator), ""
	err := each(w.Tx, bRoutes, "", func(_ []byte, r *Route) error {
		if r.Depot != nil && *r.Depot == *want.Depot && r.matchKey(operator) == key {
			id = r.ID
		}
		return nil
	})
	return id, err
}

// checkRouteAdd is adding what serves a stage as the asker: what
// names what runs is the operator's, which a run ask may propose.
func checkRouteAdd(ctx context.Context, w *wtx, argv []string, actor string) error {
	if _, err := w.routeAddProposal(argv, true); err != nil {
		return err
	}
	return w.may(ctx, actor, ActChangeRoute, Object{Kind: KindRoute, Name: routeAddOf(argv).Stage, Ask: Askable})
}

// routeAddCard is the card of route add: the rule it would add whole,
// and, for a set-aside, the yard-wide rule it sets aside named by its
// id and its scope in its first lines, what does not move, and what
// takes the change back.
func (w *wtx) routeAddCard(argv []string, asking bool) (AskRun, error) {
	spec, err := w.routeAddProposal(argv, asking)
	if err != nil {
		return AskRun{}, err
	}
	want := spec.want
	operator, err := operatorIn(w.Tx)
	if err != nil {
		return AskRun{}, err
	}
	seenMap := map[string]string{}
	if spec.source == nil {
		scope, where := "a yard default, serving every depot that states no own route for these beads", "yard-wide"
		if want.Depot != nil {
			scope, where = fmt.Sprintf("this depot's own rule in depot %s", *want.Depot), *want.Depot
		}
		holder, err := w.routeKeyHolder(want, operator)
		if err != nil {
			return AskRun{}, err
		}
		seenMap["route key "+where+" "+want.Stage] = seen(holder)
		does := []string{
			fmt.Sprintf("adds a rule for the stage %s where none stands in its scope: %s.", want.Stage, scope),
			"It takes " + routeFiltersSays(want, operator) + ".",
			fmt.Sprintf("Its chain is %s: %s serves its beads first.", want.GroupList(), w.routeGroupSays(want.Worker())),
			"No rule of its scope takes these beads now, and every other rule stands as it is.",
		}
		return AskRun{
			Argv:   argv,
			Does:   does,
			Back:   "delete the rule this card adds with yardr route rm <its id> in a terminal; the approval names that id, and no card carries that deletion",
			BackBy: "only the operator",
			Seen:   seenMap,
		}, nil
	}
	src := spec.source
	own, err := w.ownRouteForKey(want, operator)
	if err != nil {
		return AskRun{}, err
	}
	seenMap["route "+src.ID] = routeSeen(*src)
	seenMap["route own "+*want.Depot+" "+want.Stage] = seen(own)
	does := []string{
		fmt.Sprintf("sets aside the yard-wide rule %s for depot %s alone: it writes this depot's own rule for that rule's beads, and changes nothing else.", src.ID, *want.Depot),
		fmt.Sprintf("Route %s is yard-wide, for the stage %s: it takes %s.", src.ID, src.Stage, routeFiltersSays(*src, operator)),
		fmt.Sprintf("Its chain is %s: %s serves its beads first.", src.GroupList(), w.routeGroupSays(src.Worker())),
		fmt.Sprintf("The own rule will take the same beads in depot %s to %s: its chain is %s, with only the first group changed, and every filter and every condition stands as the yard-wide rule has it.", *want.Depot, w.routeGroupSays(want.Worker()), want.GroupList()),
		"If the yard-wide rule's filters change later, this own rule keeps the key it is filed with, as any own route does: no field on either rule points at the other.",
		fmt.Sprintf("The yard-wide rule itself, every other depot that keeps it, and depot %s's beads of any other key stand as they are.", *want.Depot),
	}
	return AskRun{
		Argv:   argv,
		Does:   does,
		Back:   "delete the own rule this card adds with yardr route rm <its id> in a terminal, after which the yard-wide rule serves the depot again as before; the approval names that id, and no card carries that deletion",
		BackBy: "only the operator",
		Seen:   seenMap,
	}, nil
}

// routeKeyHolder is the id of the rule of want's scope that takes its
// beads, "" where none stands.
func (w *wtx) routeKeyHolder(want Route, operator string) (string, error) {
	key, id := want.matchKey(operator), ""
	err := each(w.Tx, bRoutes, "", func(_ []byte, r *Route) error {
		if sameScope(*r, want) && r.matchKey(operator) == key {
			id = r.ID
		}
		return nil
	})
	return id, err
}

// routeAddStale says why the ask seq for route add is approved no
// more: the yard-wide rule it sets aside moved, an own rule for its
// key appeared in the depot, or a rule for its key appeared in its
// scope. Nothing else makes it stale.
func (w *wtx) routeAddStale(seq uint64, was, now AskRun) error {
	for key, held := range was.Seen {
		if now.Seen[key] == held {
			continue
		}
		switch {
		case strings.HasPrefix(key, "route own "):
			want := routeAddOf(was.Argv)
			return fmt.Errorf("ask %d was written when depot %s stated no own route for these beads: one has appeared since%s. %s", seq, *want.Depot, "", nothingRan)
		case strings.HasPrefix(key, "route key "):
			return fmt.Errorf("ask %d was written when no rule of its scope took these beads: one stands since. %s", seq, nothingRan)
		default:
			id := strings.TrimPrefix(key, "route ")
			e := lastEvent(w.Tx, "route_set", func(e Event) bool {
				wasID, _ := e.Data["was"].(map[string]any)
				isID, _ := e.Data["is"].(map[string]any)
				return wasID["id"] == id || isID["id"] == id
			})
			return fmt.Errorf("ask %d was written when the yard-wide rule %s was another: it was set since%s. %s", seq, id, since(e), nothingRan)
		}
	}
	return nil
}

// routeAddWeight weighs a route add card: what it chooses is what runs
// next on every bead the rule takes, so it is code, always.
func routeAddWeight(_ *wtx, run AskRun) ([]AskWeight, error) {
	want := routeAddOf(run.Argv)
	return []AskWeight{weighs(WeightCode, fmt.Sprintf("what serves a stage: a rule for the stage %s sends its beads to the group %s from the next claim, as the card shows", want.Stage, want.Group))}, nil
}

// runRouteAdd adds the rule as actor, in the approval's write, in the
// shape AddRoute writes (route_added, in routeEvent's shape), held to
// its tie check as a backstop, so what is saved is what the card
// showed. It answers the new rule's id, which its take-back names.
func runRouteAdd(ctx context.Context, w *wtx, argv []string, actor string, _ *approvedOn) (string, error) {
	spec, err := w.routeAddProposal(argv, true)
	if err != nil {
		return "", err
	}
	r := spec.want
	r.ID = newEdgeID()
	if err := put(w.Tx, bRoutes, r.ID, r); err != nil {
		return "", err
	}
	if err := w.event("", "route_added", actor, routeEvent(r)); err != nil {
		return "", err
	}
	if spec.source != nil {
		return fmt.Sprintf("route %s added: depot %s's own rule setting the yard-wide rule %s aside for the stage %s, to %s", r.ID, *r.Depot, spec.source.ID, r.Stage, r.GroupList()), nil
	}
	return fmt.Sprintf("route %s added: the stage %s to %s", r.ID, r.Stage, r.GroupList()), nil
}
