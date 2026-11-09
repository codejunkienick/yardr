// The source move row of the run-ask list (runList): folding the records a
// yard keeps under one source into another, for a yard whose source moved.
// There is no such command at a terminal: a pack apply folds the records of
// the sources it brings (MoveSourceRecords), and the card is the way in for
// an operator who needs the fold on its own. Moving records decides whose
// history a source owns, so it is the operator's alone (ActMoveSource stands
// under ruleOperator and no site marks it Delegate), and no delegated run
// folds records: the yardmaster proposes it and the operator approves it.
//
// The card shows the fold by running it dry (foldRecords), the way the fold
// runs it, so it counts what the fold folds and refuses what the fold
// refuses (an identity under both names that disagrees, a clash of speakers).
// The approval is held to the counts it showed, and its run folds the
// records itself and gives up, with nothing written, if what it folded is
// not what the card counted.
//
// The row is registered here, beside its functions, and not in runList's
// literal: runask.go stands at the file-size ceiling the test gate holds
// (.yardr/test).
package store

import (
	"context"
	"fmt"
)

// sourceMoveUse is how a refusal names the source move a run ask takes.
const sourceMoveUse = "yardr source move <from> <to>"

func init() {
	runList = append(runList, runCommand{
		words:       []string{"source", "move"},
		use:         sourceMoveUse,
		description: "Move the records a yard keeps under one source to another source",
		parse:       parseSourceMove,
		check:       checkSourceMove,
		card:        (*wtx).sourceMoveCard,
		stale:       (*wtx).sourceMoveStale,
		weigh:       sourceMoveWeight,
		run:         runSourceMove,
	})
}

// parseSourceMove reads the words of source move <from> <to>: the source
// the records are under and the source they go to.
func parseSourceMove(rest []string) ([]string, error) {
	if len(rest) != 2 {
		return nil, fmt.Errorf("--run takes %s: two sources, not %d words", sourceMoveUse, len(rest))
	}
	if err := checkRecordsMove(rest[0], rest[1]); err != nil {
		return nil, fmt.Errorf("--run takes %s: %w", sourceMoveUse, err)
	}
	return []string{rest[0], rest[1]}, nil
}

// checkSourceMove is moving a source's records as the asker: the check
// MoveSourceRecords refuses with, which a run ask may propose.
func checkSourceMove(ctx context.Context, w *wtx, argv []string, actor string) error {
	return w.may(ctx, actor, ActMoveSource, Object{Kind: KindSource, Name: argv[2], To: argv[3], Ask: Askable})
}

// foldsOf is the records fold of source move <from> <to>, run dry on the
// yard as it is. The destination must be a source the yard declares: a card
// does not fold what a source keeps into a name that reads nothing.
func (w *wtx) foldsOf(from, to string) (*recordsFold, error) {
	if _, _, err := sourceIn(w.Tx, to); err != nil {
		return nil, err
	}
	return foldRecords(w.Tx, from, to, true)
}

// foldWords names what a fold moves, the parts that are not none.
func foldWords(f *recordsFold) []string {
	var parts []string
	if f.Hooks > 0 {
		parts = append(parts, countNoun(f.Hooks, "hook"))
	}
	if f.Identities > 0 {
		who := []string{}
		if f.crewIdentities > 0 {
			who = append(who, fmt.Sprintf("%d of crew", f.crewIdentities))
		}
		if f.userIdentities > 0 {
			who = append(who, fmt.Sprintf("%d of users", f.userIdentities))
		}
		if f.peerIdentities > 0 {
			who = append(who, fmt.Sprintf("%d of crew at linked yards", f.peerIdentities))
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", identitiesNoun(f.Identities), andList(who)))
	}
	if f.Grants > 0 {
		parts = append(parts, countNoun(f.Grants, "crew member")+" whose grants of threads")
	}
	if f.Subscriptions > 0 {
		parts = append(parts, countNoun(f.Subscriptions, "subscription"))
	}
	if f.Threads > 0 {
		parts = append(parts, countNoun(f.Threads, "followed thread"))
	}
	return parts
}

// identitiesNoun is n identities, as countNoun would say n of a word that
// takes its plural without an s.
func identitiesNoun(n int) string {
	if n == 1 {
		return "1 identity"
	}
	return fmt.Sprintf("%d identities", n)
}

// sourceMoveCard is the card of source move <from> <to>: the hooks,
// identities, crew grants, subscriptions and followed threads the fold moves
// to the destination, each counted, and how it keeps what both hold. Asking
// refuses a destination the yard does not declare, a fold the fold refuses
// and one that moves nothing. The approval is held to the counts and to the
// destination as it was (Seen).
func (w *wtx) sourceMoveCard(argv []string, asking bool) (AskRun, error) {
	from, to := argv[2], argv[3]
	f, err := w.foldsOf(from, to)
	if err != nil {
		return AskRun{}, err
	}
	if asking && !f.Moved() {
		return AskRun{}, fmt.Errorf("nothing is kept under the source %s, so the command would change nothing", from)
	}
	dest, _, err := sourceIn(w.Tx, to)
	if err != nil {
		return AskRun{}, err
	}
	fingerprint, err := w.sourceSeen(dest)
	if err != nil {
		return AskRun{}, err
	}
	return AskRun{
		Argv: argv,
		Does: []string{
			fmt.Sprintf("folds what the yard keeps under the source %s into the source %s: %s.", from, to, andList(foldWords(f))),
			fmt.Sprintf("Where both hold the same thing it is kept once: an identity the two share must agree, grants merge, identical subscriptions collapse, and a thread followed under both keeps its participation under %s. Hook events already logged stay as they are, and the links of %s move apart (link move --from-source %s --to-source %s).", to, from, from, to),
		},
		Back:   "no command takes it back: what was folded stays folded (grants merged, identical subscriptions collapsed)",
		BackBy: "nobody",
		Seen: map[string]string{
			"records " + from + " " + to: seen(f.SourceMove, f.crewIdentities, f.userIdentities, f.peerIdentities),
			"source " + to:               fingerprint,
		},
	}, nil
}

// sourceMoveWeight weighs a source move by what it moves: a hook moved is
// heard as the destination's, which decides whose deliveries it takes (trust),
// and an identity of a user moved makes the sender in the destination count as
// that user (as-user). Crew identities, grants, subscriptions and followed
// threads are routine.
func sourceMoveWeight(w *wtx, run AskRun) ([]AskWeight, error) {
	from, to := run.Argv[2], run.Argv[3]
	f, err := w.foldsOf(from, to)
	if err != nil {
		return nil, err
	}
	var out []AskWeight
	if f.Hooks > 0 {
		out = append(out, weighs(WeightTrust, fmt.Sprintf("%s of the source %s will take deliveries as the source %s, whose rules verify them", countNoun(f.Hooks, "hook"), from, to)))
	}
	if f.userIdentities > 0 {
		out = append(out, weighs(WeightAsUser, fmt.Sprintf("%s of users in %s will be their identities in %s, and a delivery from them there is heard as theirs", countNoun(f.userIdentities, "identity"), from, to)))
	}
	return out, nil
}

// sourceMoveStale says why the ask seq for source move is approved no more:
// the destination is another source, or the records are not the ones the
// card counted.
func (w *wtx) sourceMoveStale(seq uint64, was, now AskRun) error {
	from, to := was.Argv[2], was.Argv[3]
	if err := w.staleSource(seq, to, was, now); err != nil {
		return err
	}
	if key := "records " + from + " " + to; was.Seen[key] != now.Seen[key] {
		return fmt.Errorf("ask %d was written when the yard kept other records under the source %s: they were added, changed or moved since. %s", seq, from, nothingRan)
	}
	return staleCard(seq, was, now)
}

// runSourceMove folds the records as actor through moveSourceRecords. It
// counts the fold dry first, on the records the write holds, and gives up
// with nothing written if the fold then folds anything else: an approval
// runs exactly the fold its card counted.
func runSourceMove(ctx context.Context, w *wtx, argv []string, actor string, by *approvedOn) (string, error) {
	from, to := argv[2], argv[3]
	counted, err := w.foldsOf(from, to)
	if err != nil {
		return "", err
	}
	moved, err := w.moveSourceRecords(ctx, from, to, actor)
	if err != nil {
		return "", err
	}
	if *moved != counted.SourceMove {
		return "", fmt.Errorf("moving the records of %s to %s folded %+v, and the card counted %+v: nothing was moved", from, to, *moved, counted.SourceMove)
	}
	return fmt.Sprintf("moved the records of %s to %s: %s, %s, %s, %s, %s", from, to, countNoun(moved.Hooks, "hook"), identitiesNoun(moved.Identities),
		countNoun(moved.Grants, "grant"), countNoun(moved.Subscriptions, "subscription"), countNoun(moved.Threads, "thread")), nil
}
