// The flow copy-repo row of the run list (runList): the flows of a depot's
// repository copied into the store on a card, and copied only on the
// operator's approval. What the card does is CopyRepoFlows, so the card is
// weighty code, always: the copies are what every bead of the depot then
// follows, their stage files are what whoever works one is shown to do, and
// the repository's own files stop governing while the copies stand. The card
// shows each file and the store flow it becomes, what is kept as it is, and
// the commands that point each pointer back, which only the operator runs.
//
// The row is registered here, beside its functions, as the flow set row is
// (runask_flow.go), and not in runList's literal: runask.go stands at the
// file-size ceiling the test gate holds (.yardr/test).
package store

import (
	"context"
	"fmt"
	"strings"
)

const flowCopyUse = "yardr flow copy-repo <depot>"

func init() {
	runList = append(runList, runCommand{
		words:       []string{"flow", "copy-repo"},
		use:         flowCopyUse,
		description: "Copy a depot's repository flows into the yard store",
		parse:       parseFlowCopy,
		checkRun:    checkFlowCopy,
		cardRun:     (*wtx).flowCopyCard,
		stale:       (*wtx).flowCopyStale,
		weigh:       flowCopyWeight,
		runRun:      runFlowCopy,
		ready:       readyFlowCopy,
	})
}

// parseFlowCopy reads the words of flow copy-repo <depot>: the depot alone.
func parseFlowCopy(rest []string) ([]string, error) {
	args, _, err := runWords(flowCopyUse, "flow copy-repo", "the operator's", rest, nil)
	if err != nil {
		return nil, err
	}
	if len(args) != 1 {
		return nil, fmt.Errorf("--run takes %s: one depot, not %d words", flowCopyUse, len(args))
	}
	return []string{args[0]}, nil
}

// readyFlowCopy reads, before the write, the depot's repository flows, which
// the card is made from: git does not run inside a write. A repository that
// cannot be read refuses the card in git's words, since there is no copy to
// describe without it.
func readyFlowCopy(ctx context.Context, s *Store, _ AskRun, argv []string) (map[string]*RepoFlows, error) {
	repo, err := s.RepoFlows(ctx, argv[2])
	if err != nil {
		return nil, err
	}
	return map[string]*RepoFlows{argv[2]: repo}, nil
}

// flowCopyPlan is the plan of a copy of the depot the ask names, from the
// repository flows read before the write and the yard as it stands.
func (w *wtx) flowCopyPlan(run AskRun) (*copyPlan, error) {
	depot := run.Argv[2]
	repo := w.repos[depot]
	if repo == nil {
		return nil, fmt.Errorf("the flows of depot %s's repository were not read for this card: ask again", depot)
	}
	return w.planCopy(repo)
}

// checkFlowCopy is copying the flows as the asker: what names what runs on
// every bead of a depot is the operator's, which a run ask may propose. The
// plan is held first, so a depot with nothing to copy, a file that does not
// parse and a flow somebody follows are refused in their own words whoever
// asks.
func checkFlowCopy(ctx context.Context, w *wtx, run AskRun, actor string) error {
	plan, err := w.flowCopyPlan(run)
	if err != nil {
		return err
	}
	return w.may(ctx, actor, ActChangeFlow, Object{Kind: KindFlow, Name: plan.items[0].target, Ask: Askable})
}

// copyFilesWords counts stage files as a sentence says them.
func copyFilesWords(n int) string {
	switch n {
	case 0:
		return "no stage files"
	case 1:
		return "1 stage file"
	}
	return fmt.Sprintf("%d stage files", n)
}

// flowCopyCard is the card of flow copy-repo <depot>: each repository file
// and the store flow it becomes, with its stage files and the pointer that
// leads to it, which of them replaces an unused earlier copy, what stays as
// it is in the store, that the repository's files then stop governing those
// flows, and the commands that point each pointer back, the depot's before
// the types' since a pointer of the depot stands in front of their files. The
// approval is held to the repository as it was read, and to the flows, the
// pointers and the targets as the card saw them (Seen).
func (w *wtx) flowCopyCard(run AskRun, _ bool) (AskRun, error) {
	plan, err := w.flowCopyPlan(run)
	if err != nil {
		return AskRun{}, err
	}
	depot := run.Argv[2]
	does := []string{fmt.Sprintf("copies the flows that govern depot %s from its repository, %s, into the yard store with their stage files, and points the depot at the copies, all at once. The copies are then changed with yardr flow set and yardr flow set-stage.", depot, plan.source())}
	var backs []string
	for _, it := range plan.items {
		whose := "the depot's beads"
		if it.df.Type != "" {
			whose = "its beads of type " + it.df.Type
		}
		line := fmt.Sprintf("%s becomes the store flow %s, for %s, with %s under %s.", it.df.Source.Path, it.target, whose, copyFilesWords(len(it.files)), HomeStageDir(w.home, it.target))
		if it.pointer != "" {
			line += fmt.Sprintf(" The pointer that leads %s to the repository's flow %s is replaced by one to the copy.", whose, it.pointer)
		}
		if it.replaced {
			line += " A store flow of that name stands in no use, the leftover of an earlier copy: it is replaced whole, and so are its stage files, any of which the repository has no file for being removed."
		}
		does = append(does, line)
		backs = append(backs, it.back)
	}
	if len(plan.keptWords) > 0 {
		does = append(does, "Already in the store, and left as they are: "+strings.Join(plan.keptWords, ", ")+".")
	}
	does = append(does, "While the copies stand, the repository's files above no longer govern those flows: a change that lands in "+RepoFlowDir+" is not read for them, and the copies change in the yard only. A flow pointed back at the repository leaves its copy in the store, unused, and a later copy replaces it.")
	copyKey, err := w.copySeen(plan)
	if err != nil {
		return AskRun{}, err
	}
	return AskRun{
		Argv:   run.Argv,
		Does:   does,
		Back:   strings.Join(backs, "; "),
		BackBy: "only the operator",
		Seen:   map[string]string{"repo " + depot: repoSeen(plan.repo), "copy " + depot: copyKey},
	}, nil
}

// repoSeen is what a card keeps of the repository it read: the base commit
// whole for a git depot, and for a depot of a directory the files' own stamp
// (their paths, times and sizes), which moves with any of them.
func repoSeen(repo *RepoFlows) string {
	if repo.Commit != "" {
		return "repo@" + repo.Commit
	}
	return "repo (directory) " + seen(repo.stamp)
}

// repoWords says a repoSeen as a refusal names it.
func repoWords(kept string) string {
	if sha, ok := strings.CutPrefix(kept, "repo@"); ok {
		return "repo@" + shortSHA(sha)
	}
	return "repo (directory)"
}

// copySeen is what a card keeps of the copy it describes, whole: each flow
// as the repository has it, with its stage files, the store flow it becomes
// and whether that replaces one, the depot's pointers as they stand, and
// what stays in the store. Nothing the copy itself changes on disk is in it
// but whether a target is replaced, which a retried write reads back from its
// first attempt (copyMarked), so that a retry reads as the card it was.
func (w *wtx) copySeen(plan *copyPlan) (string, error) {
	depot := plan.repo.Depot
	var pointers [][2]string
	if err := each(w.Tx, bFlowPtrs, join(depot, ""), func(_ []byte, p *FlowPointer) error {
		if p.Depot == depot {
			pointers = append(pointers, [2]string{p.Type, p.Flow})
		}
		return nil
	}); err != nil {
		return "", err
	}
	type itemSeen struct {
		Type, From, Pointer, Target string
		Replaced                    bool
		Flow                        *Flow
		Files                       [][2]string
	}
	var items []itemSeen
	for _, it := range plan.items {
		is := itemSeen{it.df.Type, it.df.Source.Path, it.pointer, it.target, it.replaced, it.df.Flow, nil}
		for _, f := range it.files {
			is.Files = append(is.Files, [2]string{f.stage, stageDigest([]byte(f.text))})
		}
		items = append(items, is)
	}
	return seen(pointers, items, plan.keptWords), nil
}

// flowCopyStale says why the ask seq for flow copy-repo is approved no more:
// a commit landed in the depot's repository, or its directory's files
// changed, so that what the card copies is another, or the depot's pointers,
// a flow the copy would write over or the flows that stay in the store are
// not as the card saw them. What else the card says is the card's own again
// at approval (staleCard).
func (w *wtx) flowCopyStale(seq uint64, was, now AskRun) error {
	depot := was.Argv[2]
	if key := "repo " + depot; was.Seen[key] != now.Seen[key] {
		wasAt, nowAt := repoWords(was.Seen[key]), repoWords(now.Seen[key])
		if wasAt == nowAt {
			return fmt.Errorf("ask %d was written from the flow files of depot %s's directory as they were: they were changed since, so what it copies is another. %s", seq, depot, nothingRan)
		}
		return fmt.Errorf("ask %d was written from the repository of depot %s at %s, and it is at %s now: a commit landed since, so what it copies is another. %s", seq, depot, wasAt, nowAt, nothingRan)
	}
	if key := "copy " + depot; was.Seen[key] != now.Seen[key] {
		e := lastEvent(w.Tx, "flow_pointer_set", naming("depot", depot))
		return fmt.Errorf("ask %d was written when the flows that point depot %s, or the store flows the copy would write over, were another: they were changed since%s. %s", seq, depot, since(e), nothingRan)
	}
	return staleCard(seq, was, now)
}

// flowCopyWeight weighs a flow copy-repo card: the flows decide what runs on
// every bead of the depot, and their stage files are what it is worked by, so
// it is code, always.
func flowCopyWeight(_ *wtx, run AskRun) ([]AskWeight, error) {
	return []AskWeight{weighs(WeightCode, fmt.Sprintf("the flows that govern depot %s, which every bead of the depot then travels as the copies say, and their stage files, which whoever works a bead is shown as what to do there", run.Argv[2]))}, nil
}

// runFlowCopy copies the flows as actor, through the store function the
// terminal's flow copy-repo calls (copyRepoFlows, inside this write): the plan
// held once more, the operator's gate asked again, and flow_set and
// flow_pointer_set recorded for each flow, so what is written is what the card
// showed. It answers the flows it copied, as a line.
func runFlowCopy(ctx context.Context, w *wtx, run AskRun, actor string, _ *approvedOn) (string, error) {
	depot := run.Argv[2]
	repo := w.repos[depot]
	if repo == nil {
		return "", fmt.Errorf("the flows of depot %s's repository were not read for this approval: approve again", depot)
	}
	res, err := w.copyRepoFlows(ctx, repo, actor)
	if err != nil {
		return "", err
	}
	names := make([]string, len(res.Flows))
	for i, f := range res.Flows {
		names[i] = f.Flow
	}
	noun := "flows"
	if len(names) == 1 {
		noun = "flow"
	}
	return fmt.Sprintf("depot %s: copied %d %s from %s into the store: %s", depot, len(names), noun, res.Source, strings.Join(names, ", ")), nil
}
