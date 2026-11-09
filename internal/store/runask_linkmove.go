// The link move row of the run-ask list (runList): moving the yard's links
// from one source to another, for a yard whose source moved. It is the form
// of link move that names two sources and no issue; the form that moves one
// issue to another bead is anyone's (MoveLink) and needs no card. Moving
// links is the operator's alone (ActMoveLinks stands under ruleOperator and
// no site marks it Delegate), so no delegated run moves one: the yardmaster
// proposes the move and the operator approves it.
//
// The card shows the move by running the part of MoveLinkSource that reads
// and checks, as the move runs it (planLinkSourceMove), so it refuses what
// the move refuses and counts what the move moves. The approval is held to
// the links it counted and runs the move itself, in the write that records
// the approval.
//
// The row is registered here, beside its functions, and not in runList's
// literal: runask.go stands at the file-size ceiling the test gate holds
// (.yardr/test).
package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// linkMoveUse is how a refusal names the link move a run ask takes.
const linkMoveUse = "yardr link move --from-source <source> --to-source <source>"

// linkMoveDescription is the help line of link move that the card keeps
// (internal/cli holds it to the command's Short).
const linkMoveDescription = "Make another bead the issue's tracker, or move the yard's links from one source to another"

func init() {
	runList = append(runList, runCommand{
		words:       []string{"link", "move"},
		use:         linkMoveUse,
		description: linkMoveDescription,
		parse:       parseLinkMove,
		check:       checkLinkMove,
		card:        (*wtx).linkMoveCard,
		stale:       (*wtx).linkMoveStale,
		run:         runLinkMove,
	})
}

// parseLinkMove reads the words of link move --from-source <source>
// --to-source <source>, each flag once and no issue: the form that names an
// issue and --to is another command, which needs no approval.
func parseLinkMove(rest []string) ([]string, error) {
	args, values, err := runWords(linkMoveUse, "link move", "the operator's", rest, map[string]bool{"--from-source": true, "--to-source": true, "--to": true})
	if err != nil {
		return nil, err
	}
	if len(args) != 0 || len(values["--to"]) != 0 {
		return nil, fmt.Errorf("--run takes %s: moving one issue to another bead (link move <issue> --to <bead>) needs no approval, run it yourself", linkMoveUse)
	}
	if len(values["--from-source"]) != 1 || len(values["--to-source"]) != 1 {
		return nil, fmt.Errorf("--run takes %s: both sources, each flag once", linkMoveUse)
	}
	from, to := values["--from-source"][0], values["--to-source"][0]
	if err := checkLinkSourceMove(from, to); err != nil {
		return nil, err
	}
	return []string{"--from-source", from, "--to-source", to}, nil
}

// linkMoveOf is the two sources of a parsed link move.
func linkMoveOf(argv []string) (from, to string) { return argv[3], argv[5] }

// checkLinkMove is moving links as the asker: the check MoveLinkSource
// refuses with, which a run ask may propose.
func checkLinkMove(ctx context.Context, w *wtx, argv []string, actor string) error {
	from, to := linkMoveOf(argv)
	return w.may(ctx, actor, ActMoveLinks, Object{Kind: KindSource, Name: from, To: to, Ask: Askable})
}

// linkSourcesShareAHost refuses a move of links between sources that are no
// link sources, or not on one host: the URLs stay as they are, so the new
// source must read them as the old one did. It is the check yardr link move
// makes before it moves them, from the declared sources of the yard, and
// answers the host they share.
func (w *wtx) linkSourcesShareAHost(from, to string) (string, error) {
	var hosts []string
	for _, name := range []string{from, to} {
		_, d, err := sourceIn(w.Tx, name)
		if err == nil && d.Link != nil {
			hosts = append(hosts, d.Link.Host)
			continue
		}
		if err != nil && !errors.Is(err, ErrNotFound) {
			return "", err
		}
		var linking []string
		if err := each(w.Tx, bSources, "", func(_ []byte, src *Source) error {
			if d, err := src.Declaration(); err == nil && d.Link != nil {
				linking = append(linking, src.Name)
			}
			return nil
		}); err != nil {
			return "", err
		}
		slices.Sort(linking)
		return "", fmt.Errorf("%s is no link source (links go to %s)", name, or(strings.Join(linking, ", "), "none"))
	}
	if !strings.EqualFold(hosts[0], hosts[1]) {
		return "", fmt.Errorf("%s is on %s and %s on %s: a move keeps the URLs, so both sources must share the host", from, hosts[0], to, hosts[1])
	}
	return hosts[1], nil
}

// linkMoveCard is the card of link move --from-source <from> --to-source
// <to>: how many links of the source move, on how many beads and which,
// that the URLs stay as they are on the host both sources share, and what
// the history of each bead says. Asking refuses a source that is no link
// source, sources on two hosts, a URL the new source takes no issue at, a
// bead that would link or track two issues, and a move of no link. The
// approval is held to the links it counted, each by its URL, and to the host
// (Seen).
func (w *wtx) linkMoveCard(argv []string, asking bool) (AskRun, error) {
	from, to := linkMoveOf(argv)
	host, err := w.linkSourcesShareAHost(from, to)
	if err != nil {
		return AskRun{}, err
	}
	plan, err := w.planLinkSourceMove(from, to)
	if err != nil {
		return AskRun{}, err
	}
	if asking && len(plan.targets) == 0 {
		return AskRun{}, fmt.Errorf("no link names the source %s, so the command would change nothing", from)
	}
	h := handlesIn(w.Tx)
	var handles, held []string
	for _, l := range plan.targets {
		if handle := h.of(l.Bead); !slices.Contains(handles, handle) {
			handles = append(handles, handle)
		}
		held = append(held, l.ID+"\x00"+plan.fixed[l.ID])
	}
	slices.Sort(handles)
	slices.Sort(held)
	beads := strings.Join(handles, ", ")
	if len(handles) > 8 {
		beads = fmt.Sprintf("%s and %d more", strings.Join(handles[:8], ", "), len(handles)-8)
	}
	return AskRun{
		Argv: argv,
		Does: []string{
			fmt.Sprintf("moves %s of the source %s to the source %s, on %s (%s).", countNoun(len(plan.targets), "link"), from, to, countNoun(plan.beads, "bead"), beads),
			fmt.Sprintf("The URLs stay as they are: both sources are on %s. Each bead's history logs its link as unlinked and linked again, and the bead is re-minted.", host),
		},
		Back:   runCommandLine([]string{"link", "move", "--from-source", to, "--to-source", from}),
		BackBy: "only the operator",
		Seen: map[string]string{
			"links " + from:      seen(held),
			"hosts " + from + to: seen(host),
		},
	}, nil
}

// linkMoveStale says why the ask seq for link move is approved no more: the
// links of the source are not the ones the card counted, or the sources are
// not on one host any more.
func (w *wtx) linkMoveStale(seq uint64, was, now AskRun) error {
	from, to := linkMoveOf(was.Argv)
	if key := "hosts " + from + to; was.Seen[key] != now.Seen[key] {
		return fmt.Errorf("ask %d was written when the sources %s and %s were on another host: a declaration was replaced since. %s", seq, from, to, nothingRan)
	}
	if key := "links " + from; was.Seen[key] != now.Seen[key] {
		e := lastEvent(w.Tx, "linked", func(Event) bool { return true })
		return fmt.Errorf("ask %d was written when other links named the source %s: links were added, moved or removed since%s. %s", seq, from, since(e), nothingRan)
	}
	return staleCard(seq, was, now)
}

// runLinkMove moves the links as actor through moveLinkSource, and answers
// what the command prints at a terminal.
func runLinkMove(ctx context.Context, w *wtx, argv []string, actor string, by *approvedOn) (string, error) {
	from, to := linkMoveOf(argv)
	moved, err := w.moveLinkSource(ctx, from, to, actor)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("moved %d links from %s to %s", moved, from, to), nil
}
