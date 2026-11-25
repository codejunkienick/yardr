// The flow set-stage row of the run list (runList): the stage file of a
// store flow's stage, created or replaced on a card and written only on the
// operator's approval. A stage file is what whoever works a bead in the
// stage is shown to do there, in every depot that follows the flow, so the
// card is weighty code, always. It carries the text beside the command
// (--run-flow, as a flow set card carries its flow), shows what stands and
// what is proposed, each whole, and holds the approval to the file as it
// stood when the card was written: a digest of it in Seen, and in the
// command's --digest where the asker named one. A flow of a depot's
// repository is refused, naming the path to change there: no card carries
// a branch and a commit.
//
// The row is registered here, beside its functions, as the flow set row is
// (runask_flow.go), and not in runList's literal: runask.go stands at the
// file-size ceiling the test gate holds (.yardr/test).
package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

const flowSetStageUse = "yardr flow set-stage <flow> <stage> [--digest <sha256|none>]"

func init() {
	runList = append(runList, runCommand{
		words:       []string{"flow", "set-stage"},
		use:         flowSetStageUse,
		description: "Set (create or replace) a store flow's stage file",
		parse:       parseFlowSetStage,
		checkRun:    checkFlowSetStage,
		cardRun:     (*wtx).flowSetStageCard,
		stale:       (*wtx).flowSetStageStale,
		weigh:       flowSetStageWeight,
		runRun:      runFlowSetStage,
		ready:       readyFlowSetStage,
	})
}

// parseFlowSetStage reads the words of flow set-stage <flow> <stage>
// [--digest <sha256|none>]: the flow, the stage and the digest the write is
// held to, as one stored form (the digest in lower case, after the stage as
// two words). The text itself goes beside argv, in the ask's --run-flow,
// since --run quotes nothing of it.
func parseFlowSetStage(rest []string) ([]string, error) {
	args, values, err := runWords(flowSetStageUse, "flow set-stage", "the operator's", rest, map[string]bool{"--digest": true})
	if err != nil {
		return nil, err
	}
	if len(args) != 2 {
		return nil, fmt.Errorf("--run takes %s: a flow and a stage, not %d words", flowSetStageUse, len(args))
	}
	if _, err := stageRel(args[1]); err != nil {
		return nil, err
	}
	out := []string{args[0], args[1]}
	switch digests := values["--digest"]; len(digests) {
	case 0:
	case 1:
		digest, err := stageExpect(digests[0])
		if err != nil {
			return nil, fmt.Errorf("--run takes %s: %w", flowSetStageUse, err)
		}
		out = append(out, "--digest", digest)
	default:
		return nil, fmt.Errorf("--run takes %s: --digest once, not %d times", flowSetStageUse, len(digests))
	}
	return out, nil
}

// flowSetStageOf is the flow, the stage and the digest ("" for none named)
// of a parsed flow set-stage.
func flowSetStageOf(argv []string) (flow, stage, digest string) {
	if len(argv) == 6 {
		digest = argv[5]
	}
	return argv[2], argv[3], digest
}

// flowSetStageSpec is a flow set-stage proposal as the yard holds it: the
// flow and stage, the text proposed and what stands now.
type flowSetStageSpec struct {
	flow, stage, expect, want string
	f                         *Flow
	st                        *stageStand
}

// flowSetStageProposal holds a flow set-stage ask to the yard as it stands:
// the text it carries is one a card shows whole, the flow is one of the
// store's and has the stage (a repository's is refused, naming the path to
// change there), and what stands there is read. Asking holds it further to
// what the writer meant: the digest it names is the file's, and the text
// is not the file's already. The approval does neither: a file that moved
// since the ask is stale, which the card's Seen says (flowSetStageStale),
// in its own words.
func (w *wtx) flowSetStageProposal(run AskRun, asking bool) (flowSetStageSpec, error) {
	flow, stage, expect := flowSetStageOf(run.Argv)
	spec := flowSetStageSpec{flow: flow, stage: stage, expect: expect, want: run.Flow}
	switch {
	case strings.TrimSpace(run.Flow) == "":
		return spec, fmt.Errorf("flow set-stage %s %s carries no text: the stage file goes beside --run, in --run-flow <file>; --run takes the flow, the stage and the digest alone", flow, stage)
	case len(run.Flow) > MaxRunFlow:
		return spec, fmt.Errorf("flow set-stage %s %s: the text it carries is %d bytes: a card shows it whole and holds at most %d, so this is the operator's to set in a terminal (yardr flow set-stage <flow> <stage> -f <file>)", flow, stage, len(run.Flow), MaxRunFlow)
	}
	if why := stageUnshown(run.Flow); why != "" {
		return spec, fmt.Errorf("flow set-stage %s %s: the text it carries is no text a card shows as written: %s", flow, stage, why)
	}
	var err error
	if spec.f, err = w.stageFlowOf(flow, stage); err != nil {
		return spec, err
	}
	if spec.st, err = standStage(w.home, flow, stage); err != nil {
		return spec, err
	}
	if asking {
		if err := spec.st.held(expect); err != nil {
			return spec, err
		}
		if spec.st.digest == stageDigest([]byte(run.Flow)) {
			return spec, fmt.Errorf("the stage file %s holds this text already, so the command would change nothing", spec.st.path)
		}
	}
	return spec, nil
}

// checkFlowSetStage is setting a stage file as the asker: what a stage says
// to do is what runs on every bead in it, which is the operator's to set,
// as the flow's stages are. The proposal is held first, so a stage the flow
// lacks and a repository's flow are refused in their own words whoever asks.
func checkFlowSetStage(ctx context.Context, w *wtx, run AskRun, actor string) error {
	spec, err := w.flowSetStageProposal(run, true)
	if err != nil {
		return err
	}
	return w.may(ctx, actor, ActChangeFlow, Object{Kind: KindFlow, Name: spec.flow, Ask: Askable})
}

// readyFlowSetStage reads, before the write, what the depots' repositories
// say of the flow where it is none of the store's, so that the card names a
// repository's flow as one (stageRepos): git does not run inside a write.
// A flow of the store reads none.
func readyFlowSetStage(ctx context.Context, s *Store, _ AskRun, argv []string) (map[string]*RepoFlows, error) {
	flow, _, _ := flowSetStageOf(argv)
	return s.stageRepos(ctx, flow)
}

// flowSetStageCard is the card of flow set-stage: whether the file is added
// or replaced and where it stands, the text that stands and the text
// proposed, each whole, and what takes the change back. A stage file of a
// flow a pack manages says so, with what pack apply then does with the yard's
// copy and the door to the pack's; those words are the card's, so a flow that
// becomes a pack's after the ask reads as another card at approval
// (staleCard), and nothing runs. So is a ## Ask section the text breaks.
func (w *wtx) flowSetStageCard(run AskRun, asking bool) (AskRun, error) {
	spec, err := w.flowSetStageProposal(run, asking)
	if err != nil {
		return AskRun{}, err
	}
	st, target := spec.st, spec.st.path
	var does []string
	var back string
	if st.digest == stageNone {
		does = append(does, fmt.Sprintf("adds the stage file of stage %s of the flow %s, where none stands: %s. Whoever works a bead in the stage is shown it, in every depot that follows the flow.", spec.stage, spec.flow, target))
		back = fmt.Sprintf("remove %s in a terminal: no stage file stood for stage %s of flow %s", target, spec.stage, spec.flow)
	} else {
		does = append(does, fmt.Sprintf("replaces the stage file of stage %s of the flow %s whole: %s. Whoever works a bead in the stage is shown the new text in place of the text that stands, in every depot that follows the flow.", spec.stage, spec.flow, target))
		if why := stageUnshown(st.text); why != "" || len(st.text) > MaxRunFlow {
			if why == "" {
				why = fmt.Sprintf("it is %d bytes, and a card shows at most %d", len(st.text), MaxRunFlow)
			}
			does = append(does, fmt.Sprintf("This card cannot show the standing stage file: %s.", why))
		} else {
			does = append(does, "The text that stands, whole:")
			does = append(does, textLines(st.text)...)
		}
		// Shown or not, the approval keeps the text that stands in the home's
		// stage history before it replaces it (putStageFile), and one more save
		// of that file puts it back. A save takes text alone (stageText), so for
		// a file that is no UTF-8 the way back is a copy.
		if utf8.ValidString(st.text) {
			back = fmt.Sprintf("yardr flow set-stage %s %s -f %s, where the yard keeps the text that stands once this is approved", spec.flow, spec.stage, st.history)
		} else {
			back = fmt.Sprintf("cp %s %s in a terminal, where the yard keeps the text that stands once this is approved: it is not valid UTF-8, which yardr flow set-stage does not take", st.history, target)
		}
	}
	does = append(does, "On approval the file holds the text below, whole:")
	does = append(does, textLines(spec.want)...)
	if broken := (&StageFile{Path: target, Text: spec.want}).BrokenAsk(spec.f, spec.stage); broken != "" {
		does = append(does, fmt.Sprintf("Warning: the ## Ask section of this text is broken (%s).", broken))
	}
	if pack, depot, err := w.flowPackManaged(spec.flow); err != nil {
		return AskRun{}, err
	} else if pack != "" {
		lines, door := stagePackWords(pack, depot)
		does = slices.Insert(does, 1, lines...)
		back += "; or, for the pack's own copy instead, " + door + " in a terminal"
	}
	return AskRun{
		Argv:   run.Argv,
		Does:   does,
		Back:   back,
		BackBy: "only the operator",
		Seen:   map[string]string{"stage " + spec.flow + " " + spec.stage: st.digest},
		Flow:   run.Flow,
	}, nil
}

// stagePackWords are the card's lines for a stage file of a flow a pack
// manages, which stand after its first line, and the terminal command that
// sets the pack's copy: pack apply of that pack into the depot whose import
// lists the flow, taking the file by the name yardr pack check prints it by.
// The card changes the yard's copy only, never the pack where it is kept; the
// lines say what pack apply then does with it, so an approver reads it before
// approving.
func stagePackWords(pack, depot string) (lines []string, door string) {
	into, check, door := "the yard", "yardr pack check "+pack, "yardr pack apply "+pack
	if depot != "" {
		into = "depot " + depot
		check += " --depot " + depot
		door += " --depot " + depot
	}
	door += " --take file/<name>"
	return []string{
		fmt.Sprintf("The flow is the pack %s's, imported into %s: this sets the yard's copy of the stage file, and changes nothing of the pack.", pack, into),
		fmt.Sprintf("yardr pack apply then keeps the yard's copy of it, whatever the pack moves, and sets the pack's again only where it is told to (--take file/<name>, the name %s gives the file).", check),
	}, door
}

// flowSetStageStale says why the ask seq for flow set-stage is approved no
// more: the stage file was written, replaced, emptied or removed since, a
// pack's apply included. Only a batch's retry of this same approval
// (w.stageWrote) sees the file its own first attempt wrote and passes through
// so the commit finishes; any other write, even of identical text, is stale
// and refused. The rest of the card (a flow that became a pack's, a section
// the text breaks) is the card's own again at approval, in its words.
func (w *wtx) flowSetStageStale(seq uint64, was, now AskRun) error {
	flow, stage, _ := flowSetStageOf(was.Argv)
	if key := "stage " + flow + " " + stage; was.Seen[key] != now.Seen[key] {
		if w.stageWrote != nil && w.stageWrote.key == key && now.Seen[key] == stageDigest([]byte(was.Flow)) {
			return nil
		}
		e := lastEvent(w.Tx, "flow_set", func(e Event) bool { return e.Data["flow"] == flow && e.Data["stage"] == stage })
		switch {
		case was.Seen[key] == stageNone:
			return fmt.Errorf("ask %d was written when no stage file stood for stage %s of the flow %s: one appeared since%s. %s", seq, stage, flow, since(e), nothingRan)
		case now.Seen[key] == stageNone:
			return fmt.Errorf("ask %d was written when stage %s of the flow %s had a stage file: it was removed or emptied since%s. %s", seq, stage, flow, since(e), nothingRan)
		}
		return fmt.Errorf("ask %d was written when the stage file of stage %s of the flow %s was another: it was changed since%s. %s", seq, stage, flow, since(e), nothingRan)
	}
	return staleCard(seq, was, now)
}

// flowSetStageWeight weighs a flow set-stage card: a stage file is what
// every bead in the stage is worked by, so it is code, always.
func flowSetStageWeight(_ *wtx, run AskRun) ([]AskWeight, error) {
	flow, stage, _ := flowSetStageOf(run.Argv)
	return []AskWeight{weighs(WeightCode, fmt.Sprintf("the stage file of stage %s of the flow %s, which whoever works a bead in the stage is shown as what to do there", stage, flow))}, nil
}

// runFlowSetStage writes the stage file as actor, through the store function
// the terminal's flow set-stage calls (putStageFile, inside this write): the
// flow and stage held once more, what stands held to the digest the ask
// names, and flow_set recorded, so what is written is what the card showed.
func runFlowSetStage(ctx context.Context, w *wtx, run AskRun, actor string, _ *approvedOn) (string, error) {
	flow, stage, expect := flowSetStageOf(run.Argv)
	sf, err := w.putStageFile(ctx, flow, stage, run.Flow, expect, actor)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("stage file %s/%s: %s", flow, stage, sf.Path), nil
}
