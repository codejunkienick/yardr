// The group rename row of the run list (runList): a group called by another
// name, proposed on a card and renamed only on the operator's approval, as a
// peer's is. The command is two names and nothing else, so it rides argv as
// the terminal's own words and pastes into a terminal. The card says what
// follows the name (grouprename.go): the routes that name the group, what its
// sessions may call, the subscriptions that filter on it and the pack imports
// that recorded it; its beads and live sessions go on, and it says so, since
// that is what renaming by hand cannot do. It counts no session: they start
// and end by the minute, and a card that named their number would be stale
// before it was read.
//
// The row is registered here, beside its functions, as the group set row is
// (runask_groupset.go), and not in runList's literal: runask.go stands at the
// file-size ceiling the test gate holds (.yardr/test).
package store

import (
	"context"
	"fmt"
	"strings"
)

// groupRenameUse is how a refusal names the group rename row.
const groupRenameUse = "yardr group rename <old> <new>"

// groupRenameDescription is the command's Short in internal/cli, which a test
// holds the row to.
const groupRenameDescription = "Rename a group: its routes, beads, sessions and records follow"

func init() {
	runList = append(runList, runCommand{
		words:       []string{"group", "rename"},
		use:         groupRenameUse,
		description: groupRenameDescription,
		parse:       parseGroupRename,
		check:       checkGroupRenameAsk,
		card:        (*wtx).groupRenameCard,
		stale:       (*wtx).groupRenameStale,
		run:         runGroupRename,
	})
}

// parseGroupRename reads the words of group rename <old> <new>.
func parseGroupRename(rest []string) ([]string, error) {
	if len(rest) != 2 {
		return nil, fmt.Errorf("--run takes %s: two names, not %d words", groupRenameUse, len(rest))
	}
	return []string{rest[0], rest[1]}, nil
}

// checkGroupRenameAsk is renaming a group's own check as the asker.
func checkGroupRenameAsk(ctx context.Context, w *wtx, argv []string, actor string) error {
	return w.may(ctx, actor, ActRenameGroup, Object{Kind: KindGroup, Name: argv[2], Ask: Askable})
}

// groupRenameCard is the card of group rename <old> <new>: what follows the
// name, as the rename reads it from the yard's records, and in the store's own
// words for what it refuses.
func (w *wtx) groupRenameCard(argv []string, _ bool) (AskRun, error) {
	name, to := argv[2], argv[3]
	p, err := w.planGroupRename(name, to)
	if err != nil {
		return AskRun{}, err
	}
	rep := p.report()
	does := []string{fmt.Sprintf("the group %s (runner %s) is called %s from now on: its settings, members, limit and subscriptions stay as they are.", name, p.group.Runner, to)}
	if len(rep.Routes) > 0 {
		does = append(does, fmt.Sprintf("the routes that name it, as their first group or after it, name %s: %s. Their conditions stay.", to, strings.Join(rep.Routes, ", ")))
	}
	does = append(does, fmt.Sprintf("the arrival estimates from group=%s stand under group=%s.", name, to))
	if len(rep.Members) > 0 {
		does = append(does, fmt.Sprintf("the groups %s list it among their members, which is how its script may move the beads of their stages: they list %s.", strings.Join(rep.Members, ", "), to))
	}
	does = append(does, "The beads it owns and their live sessions go on under the new name: none is released, and no session or pane is touched. A session running now keeps the old name in its environment (YARDR_GROUP) until it ends.")
	if rep.Grants > 0 {
		does = append(does, fmt.Sprintf("the grants of source calls that its sessions hold (%d) move with it.", rep.Grants))
	}
	if len(rep.Subscriptions) > 0 {
		does = append(does, fmt.Sprintf("the subscriptions of %s that filter on group=%s filter on group=%s.", strings.Join(rep.Subscriptions, ", "), name, to))
	}
	if len(rep.Packs) > 0 {
		does = append(does, fmt.Sprintf("the pack imports that recorded it (%s) record %s, so that their next import finds the renamed group and brings no group called %s back.", strings.Join(rep.Packs, ", "), to, name))
	}
	for _, u := range rep.Unserved {
		does = append(does, fmt.Sprintf("the default route %s (%s -> %s) names its group by the depot template, so it no longer serves depot %s: no group is called %s there.", u.Route.ID, u.Route.Stage, u.Route.GroupList(), u.Depot, name))
	}
	does = append(does, "The old name is free afterwards and reaches nothing, and the log and the notes keep it: what names it outside the yard's records, a script or a prompt, is for the operator to change.")
	back, backBy := runCommandLine([]string{"group", "rename", to, name}), "only the operator"
	if depot := w.importerNamespace(name); depot != "" {
		back, backBy = fmt.Sprintf("%s is a name pack imports give the groups they make for depot %s, so no group may be renamed to it", name, depot), "nobody"
	}
	return AskRun{Argv: argv, Does: does, Back: back, BackBy: backBy}, nil
}

// groupRenameStale says why the ask seq for group rename <old> <new> is
// approved no more: what follows the name is not what the card says.
func (w *wtx) groupRenameStale(seq uint64, was, now AskRun) error {
	return staleCard(seq, was, now)
}

// runGroupRename renames the group as actor through renameGroup, and answers
// what the command prints at a terminal.
func runGroupRename(ctx context.Context, w *wtx, argv []string, actor string, by *approvedOn) (string, error) {
	out, err := w.renameGroup(ctx, argv[2], argv[3], actor, by)
	if err != nil {
		return "", err
	}
	return out.String(), nil
}
