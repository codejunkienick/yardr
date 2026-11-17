// The flow set row of the run list (runList): a store flow's stages,
// changed on a card and set only on the operator's approval (design
// yardr-y1cn 3.3, as decided on ask 26833: every such card is weighty
// code, a reorder-only one included, since a reorder changes which
// stage a bead enters first and where a stranded bead rejoins). The
// card carries the whole flow as the line format writes it, its stage
// order first, shows what stands and what would stand whole, and
// takes the change back with the flow that stands, carried whole the
// same way. It refuses a flow a pack manages, at the ask and again at
// approval, and a removal that would strand a bead, naming the beads,
// as the terminal command does. The yardmaster files these cards from
// the operator's own mail only; mail from anyone else gets no filing.
//
// The row is registered here, beside its functions, as the group set
// row is (runask_groupset.go), and not in runList's literal: runask.go
// stands at the file-size ceiling the test gate holds (.yardr/test).
package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// ActChangeFlow is the gate the flow card asks through may
// (policy.go): what names what runs is the operator's, beside its row
// here as the row is registered here.
const ActChangeFlow Action = "change a flow's stages"

const flowSetUse = "yardr flow set <name>"

// MaxRunFlow is the most flow text a flow set ask carries: a card
// shows it whole, so past this the flow is set in a terminal.
const MaxRunFlow = 32 << 10

func init() {
	runList = append(runList, runCommand{
		words:       []string{"flow", "set"},
		use:         flowSetUse,
		description: "Set (replace) a named flow; depots and types point at it with 'yardr depot flow'",
		parse:       parseFlowSet,
		checkRun:    checkFlowSet,
		cardRun:     (*wtx).flowSetCard,
		stale:       (*wtx).flowSetStale,
		weigh:       flowSetWeight,
		runRun:      runFlowSet,
	})
}

// parseFlowSet reads the words of flow set <name>: the name alone. The
// flow itself goes beside argv, in the ask's --run-flow, since it
// needs the characters --run quotes nothing of.
func parseFlowSet(rest []string) ([]string, error) {
	if len(rest) != 1 {
		return nil, fmt.Errorf("--run takes %s: one flow name, not %d words", flowSetUse, len(rest))
	}
	return []string{rest[0]}, nil
}

// flowSetSpec is a flow set proposal as the yard holds it: the flow as
// it stands (nil where none does) and as the approval would leave it.
type flowSetSpec struct {
	have *Flow
	want Flow
}

// flowPackManaged is the pack that manages the store flow name, by
// the Packs join the Line read uses (flowPackName, over the imports
// into each depot the flow stands in: a flow the card may not edit
// stands in one of them), "" where none does.
func (w *wtx) flowPackManaged(name string) (pack, depot string, err error) {
	err = each(w.Tx, bPacks, "", func(_ []byte, p *PackImport) error {
		if pack == "" && slices.ContainsFunc(p.Objects, func(o PackObject) bool { return o.Kind == "flow" && o.Name == name }) {
			pack, depot = p.Pack, p.Depot
		}
		return nil
	})
	return pack, depot, err
}

// flowSetProposal holds a flow set ask to the yard as it stands: the
// flow is no pack's, the text it carries parses and orders (the checks
// the terminal command makes, in its words), and no open bead that
// follows it sits in a stage the new version lacks, the beads named.
// The pack refusal is the card's own, and load-bearing: PutFlow
// accepts such a write outside a delegated pack apply
// (packFlowWrite), so it is made here, when the ask is written and
// again when it is approved.
func (w *wtx) flowSetProposal(run AskRun) (flowSetSpec, error) {
	name := run.Argv[2]
	if pack, depot, err := w.flowPackManaged(name); err != nil {
		return flowSetSpec{}, err
	} else if pack != "" {
		where := "the yard"
		if depot != "" {
			where = "depot " + depot
		}
		return flowSetSpec{}, fmt.Errorf("flow %s is managed by the pack %s in %s: it is changed where the pack is kept and reaches this yard by yardr pack apply, so no flow set card carries it", name, pack, where)
	}
	if strings.TrimSpace(run.Flow) == "" {
		return flowSetSpec{}, fmt.Errorf("flow set %s carries no flow: the flow goes beside --run, in --run-flow <file> (F: lines on stdin); --run takes the name alone", name)
	}
	if len(run.Flow) > MaxRunFlow {
		return flowSetSpec{}, fmt.Errorf("flow set %s: the flow it carries is %d bytes: a card shows it whole and holds at most %d, so this is the operator's to type in a terminal", name, len(run.Flow), MaxRunFlow)
	}
	want, err := ParseFlow(run.Flow)
	if err != nil {
		return flowSetSpec{}, err
	}
	want.Name = name
	if err := want.normalize(); err != nil {
		return flowSetSpec{}, err
	}
	spec := flowSetSpec{want: *want}
	if ok, err := get(w.Tx, bFlows, name, &spec.have); err != nil {
		return flowSetSpec{}, err
	} else if !ok {
		spec.have = nil
	}
	if strays, err := w.flowCardStrays(spec.want); err != nil {
		return flowSetSpec{}, err
	} else if len(strays) > 0 {
		return flowSetSpec{}, fmt.Errorf("the new flow %s lacks the stage of open beads %s; advance them to a stage it has, or pin them to another flow (yardr bead update <id> --meta flow=<name>), then set the flow again",
			name, strings.Join(strays, "; "))
	}
	return spec, nil
}

// flowCardStrays names the open beads that follow the stored flow
// want.Name and sit in a stage want lacks, as PutFlow names them
// (flowStrays), read against the flow the card proposes instead of
// one already saved: "in depot yard: <id> (<stage>), ..." per pointer
// at the name, then "pinning it: ..." for beads that name it.
func (w *wtx) flowCardStrays(want Flow) ([]string, error) {
	var out []string
	var ptrs []FlowPointer
	if err := each(w.Tx, bFlowPtrs, "", func(_ []byte, p *FlowPointer) error {
		if p.Flow == want.Name {
			ptrs = append(ptrs, *p)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	for _, p := range ptrs {
		var strays []string
		err := each(w.Tx, bBeads, "", func(_ []byte, r *beadRow) error {
			switch {
			case r.Status == "closed" || NotWork[r.Type] || PinnedFlow(&r.Bead) != "":
			case r.Depot == nil || *r.Depot != p.Depot:
			case p.Type != "" && r.Type != p.Type:
			case p.Type == "" && w.Bucket(bFlowPtrs).Get([]byte(join(p.Depot, r.Type))) != nil:
			case slices.Contains(want.Stages, r.Stage):
			default:
				strays = append(strays, fmt.Sprintf("%s (%s)", r.ID, r.Stage))
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		if len(strays) > 0 {
			out = append(out, "in "+p.String()+": "+strings.Join(strays, ", "))
		}
	}
	var pinned []string
	err := each(w.Tx, bBeads, "", func(_ []byte, r *beadRow) error {
		if r.Status != "closed" && !NotWork[r.Type] && PinnedFlow(&r.Bead) == want.Name && !slices.Contains(want.Stages, r.Stage) {
			pinned = append(pinned, fmt.Sprintf("%s (%s)", r.ID, r.Stage))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(pinned) > 0 {
		out = append(out, "pinning it: "+strings.Join(pinned, ", "))
	}
	return out, nil
}

// checkFlowSet is changing a flow's stages as the asker: what names
// what runs is the operator's, which a run ask may propose. The
// proposal is held first, so a pack's flow, an unorderable one and a
// stranding are refused in their own words whoever asks.
func checkFlowSet(ctx context.Context, w *wtx, run AskRun, actor string) error {
	spec, err := w.flowSetProposal(run)
	if err != nil {
		return err
	}
	if spec.have != nil && flowsEqual(*spec.have, spec.want) {
		return fmt.Errorf("flow %s is set so already, so the command would change nothing", spec.want.Name)
	}
	return w.may(ctx, actor, ActChangeFlow, Object{Kind: KindFlow, Name: spec.want.Name, Ask: Askable})
}

// flowsEqual says whether two flows stand the same, as stored.
func flowsEqual(a, b Flow) bool {
	return slices.Equal(a.Stages, b.Stages) && slices.Equal(a.Terminal, b.Terminal) &&
		fmt.Sprint(a.Transitions) == fmt.Sprint(b.Transitions) && fmt.Sprint(a.Outcomes) == fmt.Sprint(b.Outcomes)
}

// flowSetCard is the card of flow set <name>: the flow's stages in
// order first, what is added, removed and reordered, each stage's next
// stages as they stand and as they would be, the flow that stands and
// the one proposed whole, and what takes the change back. A flow that
// stands in no store yet says so.
func (w *wtx) flowSetCard(run AskRun, asking bool) (AskRun, error) {
	spec, err := w.flowSetProposal(run)
	if err != nil {
		return AskRun{}, err
	}
	want := spec.want
	if asking && spec.have != nil && flowsEqual(*spec.have, want) {
		return AskRun{}, fmt.Errorf("flow %s is set so already, so the command would change nothing", want.Name)
	}
	var does []string
	var seenFlow string
	if spec.have == nil {
		does = append(does,
			fmt.Sprintf("adds the flow %s, which no flow of the store stands under yet: its stages in order are %s.", want.Name, strings.Join(want.Stages, ", ")),
			"On approval the flow stands as the lines below write it, whole:")
		does = append(does, textLines(strings.TrimSpace(want.String()))...)
		seenFlow = seen("")
	} else {
		have := *spec.have
		seenFlow = seen(have)
		does = append(does, fmt.Sprintf("changes the flow %s whole: its stages, their order and where each leads, as the lines below say, and nothing else of it.", want.Name))
		does = append(does, flowDiffLines(have, want)...)
		does = append(does,
			"The flow that stands, whole:",
		)
		does = append(does, textLines(strings.TrimSpace(have.String()))...)
		does = append(does, "On approval the flow stands as the lines below write it, whole:")
		does = append(does, textLines(strings.TrimSpace(want.String()))...)
		for _, s := range want.Stages {
			does = append(does, fmt.Sprintf("stage %s leads to %s (now: %s).", s, or(strings.Join(want.Transitions[s], ", "), "nowhere"), or(strings.Join(have.Transitions[s], ", "), "nowhere")))
		}
	}
	back := "yardr flow set " + want.Name + " with the flow that stands, carried whole as this card shows it (at a terminal, yardr flow set " + want.Name + " -f <file> with that text)"
	if spec.have == nil {
		back = "yardr flow rm " + want.Name + " in a terminal: no flow stood under the name"
	}
	return AskRun{
		Argv:   run.Argv,
		Does:   does,
		Back:   back,
		BackBy: "only the operator",
		Seen:   map[string]string{"flow " + want.Name: seenFlow},
		Flow:   run.Flow,
	}, nil
}

// flowDiffLines are the card's lines for what a flow change adds,
// removes and reorders, before the two flows whole.
func flowDiffLines(have, want Flow) []string {
	var added, removed []string
	for _, s := range want.Stages {
		if !slices.Contains(have.Stages, s) {
			added = append(added, s)
		}
	}
	for _, s := range have.Stages {
		if !slices.Contains(want.Stages, s) {
			removed = append(removed, s)
		}
	}
	lines := []string{fmt.Sprintf("Its stages stand in the order %s now; on approval they stand %s.", strings.Join(have.Stages, ", "), strings.Join(want.Stages, ", "))}
	switch {
	case len(added) == 0 && len(removed) == 0 && slices.Equal(have.Stages, want.Stages):
	case len(added) == 0 && len(removed) == 0:
		lines = append(lines, "No stage is added or removed: the order changes, and with it which stage a bead enters first and where a bead in a stage the flow lacks rejoins it.")
	}
	if len(added) > 0 {
		lines = append(lines, "Stages added: "+strings.Join(added, ", ")+". A stage added on this card stands in the flow with no stage file.")
	}
	if len(removed) > 0 {
		lines = append(lines, "Stages removed: "+strings.Join(removed, ", ")+".")
	}
	return lines
}

// flowSetStale says why the ask seq for flow set is approved no more:
// the flow was set since. The pack refusal and the stranding are the
// card's own again at approval (flowSetProposal), in their words.
func (w *wtx) flowSetStale(seq uint64, was, now AskRun) error {
	name := was.Argv[2]
	if key := "flow " + name; was.Seen[key] != now.Seen[key] {
		e := lastEvent(w.Tx, "flow_set", naming("flow", name))
		return fmt.Errorf("ask %d was written when the flow %s was another: it was set since%s. %s", seq, name, since(e), nothingRan)
	}
	return staleCard(seq, was, now)
}

// flowSetWeight weighs a flow set card: a flow decides what runs on
// every bead that follows it, so it is code, always, a reorder-only
// card included.
func flowSetWeight(_ *wtx, run AskRun) ([]AskWeight, error) {
	return []AskWeight{weighs(WeightCode, fmt.Sprintf("the stages of the flow %s, which every bead that follows it then travels as the card shows", run.Argv[2]))}, nil
}

// runFlowSet sets the flow as actor, through the store function the
// terminal's flow set calls in its shape (PutFlow, inside this
// write): the pack refusal and the stranding held once more, the flow
// saved, and flow_set recorded, so what is saved is what the card
// showed.
func runFlowSet(ctx context.Context, w *wtx, run AskRun, actor string, _ *approvedOn) (string, error) {
	spec, err := w.flowSetProposal(run)
	if err != nil {
		return "", err
	}
	if err := w.packPutFlow(ctx, spec.want, actor); err != nil {
		return "", err
	}
	strays, err := flowStrays(w.Tx, spec.want)
	if err != nil {
		return "", err
	}
	if len(strays) > 0 {
		return "", fmt.Errorf("the new flow %s lacks the stage of open beads %s; advance them to a stage it has, or pin them to another flow (yardr bead update <id> --meta flow=<name>), then set the flow again",
			spec.want.Name, strings.Join(strays, "; "))
	}
	if err := w.event("", "flow_set", actor, map[string]any{"flow": spec.want.Name}); err != nil {
		return "", err
	}
	return fmt.Sprintf("flow %s: %d stages", spec.want.Name, len(spec.want.Stages)), nil
}
