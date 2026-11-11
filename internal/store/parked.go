package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// WaitingBead is one row of a person's parked queue: a bead standing in a
// manual stage of theirs, with the moves they may make of it. A bead that
// carries an open ask to the person is not a row: it stands among their
// decisions (UserAsks), where answering the ask moves it or not.
type WaitingBead struct {
	Bead  string `json:"bead"`
	Title string `json:"title"`
	// Depot is the depot it is worked in, absent where it has none.
	Depot string `json:"depot,omitempty"`
	// Stage is where it stands, and Group the manual group whose stage it is.
	Stage string `json:"stage"`
	Group string `json:"group"`
	// Since is when the bead last changed: about when it began to wait.
	Since time.Time `json:"since"`
	// Revision is the bead's revision the row was read at: a move names it,
	// so that a bead that changed since is refused and read again.
	Revision string `json:"revision"`
	// Actions are the moves offered to the person, in review's order. None
	// where FlowError stands: no move exists until the flow is fixed.
	Actions []Move `json:"actions"`
	// FlowError says why the bead's flow does not resolve.
	FlowError string `json:"flow_error,omitempty"`
	// TemplateError is why entering the stage put no stage ask, where the
	// ask would stand (NextStages). The bead still moves by hand.
	TemplateError string `json:"template_error,omitempty"`
}

// repoRead is one depot's repository flows as read between the passes of
// Waiting: the flows, or why they could not be read.
type repoRead struct {
	flows *RepoFlows
	err   error
}

// Waiting reads the beads parked for the person name, longest waiting first
// (the bead's last change, then its id), for a client that is not yardr. It
// is what yardr review walks, less what is not a move of the person's to
// make:
//
//   - the bead is open and a work bead (a journal or a memory is none), and
//     the viewer may see it; an omitted viewer is the yard itself;
//   - a manual group works its stage (ManualGroup), and the person is one of
//     its members where it lists any; a group that lists none is anyone's;
//   - the bead stands past the first stage of the flow that governs it, so a
//     backlog, where beads start, is never here; no flow, no first stage;
//   - nothing is the matter with it: a held bead, and one in a stage its flow
//     lacks, are faults to mend and not moves to make, and stand among the
//     web's waits and never its parked beads, which this call keeps to;
//   - no ask to the person stands open on it, which would be among their
//     decisions;
//   - and the person's part in it is not in (PartIn): with another member
//     left in a group of several, a bead the person answered, declined or
//     put aside an ask to them on is the other member's to move. An ask its
//     asker took back is nobody's answer, and takes the bead from no one's
//     queue.
//
// Each flow is resolved once for the call, and each depot's repository once
// (between two reads of the store, since git must not run inside one), not
// once for each bead as NextStages would. Only the user reads their queue,
// and an operator any user's (mayReadInbox); actor is who reads, and left
// out is the user.
func (s *Store) Waiting(ctx context.Context, name, actor string, viewer ...string) ([]WaitingBead, error) {
	repos := map[string]repoRead{}
	for {
		rows, missing, err := s.parked(ctx, name, actor, firstViewer(viewer), repos)
		if err != nil || len(missing) == 0 {
			return rows, err
		}
		for _, depot := range missing {
			rf, err := s.RepoFlows(ctx, depot)
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			repos[depot] = repoRead{rf, err}
		}
	}
}

// parked is one pass of Waiting over the repositories it has read so far.
// missing names the depots whose repository the flows of some bead depend on
// and that the pass lacked: the rows are then not final, and the caller reads
// those repositories and passes again.
func (s *Store) parked(ctx context.Context, name, actor, viewer string, repos map[string]repoRead) ([]WaitingBead, []string, error) {
	var rows []WaitingBead
	var missing []string
	err := s.read(func(tx *bolt.Tx) error {
		rows, missing = []WaitingBead{}, nil
		if _, err := getUser(tx, name); err != nil {
			return err
		}
		if _, err := s.mayReadInbox(ctx, tx, name, actor, "read"); err != nil {
			return err
		}
		routes, err := sortedRoutes(tx)
		if err != nil {
			return err
		}
		operator, err := operatorIn(tx)
		if err != nil {
			return err
		}
		groups := map[string]Group{}
		if err := each(tx, bGroups, "", func(_ []byte, g *Group) error {
			groups[g.Name] = *g
			return nil
		}); err != nil {
			return err
		}
		open, err := s.openBeads(tx)
		if err != nil {
			return err
		}
		memo, at := flowMemo{}, now()
		for _, b := range open {
			if NotWork[b.Type] || b.Hold {
				continue
			}
			g, ok := ManualGroup(routes, b.Bead, operator, groups)
			if !ok || len(g.Members) > 0 && !slices.Contains(g.Members, name) {
				continue
			}
			if s.may(ctx, tx, viewer, ActSee, Object{Kind: KindBead, Name: b.ID}) != nil {
				continue
			}
			var repo *RepoFlows
			if b.Depot != nil {
				repo = repos[*b.Depot].flows
			}
			f, src, flowErr := memo.resolve(tx, b.Depot, b.Type, PinnedFlow(&b.Bead), repo)
			if errors.Is(flowErr, errNeedRepo) && b.Depot != nil {
				read, ok := repos[*b.Depot]
				if !ok {
					if !slices.Contains(missing, *b.Depot) {
						missing = append(missing, *b.Depot)
					}
					continue
				}
				// The repository could not be read: the flow is not known.
				flowErr = read.err
			}
			if flowErr == nil && f != nil && (!slices.Contains(f.Stages, b.Stage) || f.Stages[0] == b.Stage) {
				// Stranded, which is a fault, or at the flow's first stage,
				// where its beads start.
				continue
			}
			notes, err := beadNotes(tx, b.UUID)
			if err != nil {
				return err
			}
			asked, tookPart := asksOf(notes, name, at)
			if asked || PartIn(g, name, tookPart) {
				continue
			}
			w := WaitingBead{Bead: b.ID, Title: b.Title, Stage: b.Stage, Group: g.Name,
				Since: b.UpdatedAt, Revision: b.Revision, Actions: []Move{}}
			if b.Depot != nil {
				w.Depot = *b.Depot
			}
			if flowErr != nil {
				w.FlowError = flowErr.Error()
			} else {
				next := &NextStages{Bead: b.ID, Stage: b.Stage, Stages: []string{}}
				if f != nil {
					if err := next.fill(tx, b, f, src, repo); err != nil {
						return err
					}
				}
				w.TemplateError = next.TemplateError
				if moves := MovesFor(&b.Bead, next, routes, operator); moves != nil {
					w.Actions = moves
				}
			}
			rows = append(rows, w)
		}
		slices.SortStableFunc(rows, func(a, b WaitingBead) int {
			if c := a.Since.Compare(b.Since); c != 0 {
				return c
			}
			return strings.Compare(a.Bead, b.Bead)
		})
		return nil
	})
	return rows, missing, err
}

// asksOf says, from a bead's notes, whether an ask to the person stands open
// on it and whether they took part in one (partStates).
func asksOf(notes []Note, name string, at time.Time) (open, tookPart bool) {
	for _, r := range ResolveAsks(notes, at) {
		if r.AskErr != nil || r.Ask.To != name {
			continue
		}
		if r.State == AskOpen {
			open = true
		} else if r.State.tookPart() {
			tookPart = true
		}
	}
	return open, tookPart
}

// partStates are the states in which an ask stands for a part the person it
// is put to took in it: they answered it, declined it or put it aside. One
// still open has none of theirs yet, and one taken back never will: its asker
// ended it, which is no answer of the person, though UserAsks lists it among
// the decided. A state a later build adds is none of theirs until it is
// listed here, so that a bead is never left to nobody by a word this build
// does not know.
var partStates = []AskState{AskAnswered, AskAside, AskDeclined}

// tookPart reports whether an ask in this state is one the person took part
// in (partStates).
func (s AskState) tookPart() bool { return slices.Contains(partStates, s) }

// TookPart reports whether the person took part in the ask (partStates), as
// State says it in words, which is all a client of the socket holds.
func (d DecidedAsk) TookPart() bool {
	return slices.ContainsFunc(partStates, func(s AskState) bool { return s.String() == d.State })
}

// PartIn reports whether the person's part in a bead parked in a stage of the
// manual group g is in, which leaves the bead to another member to move: g
// lists several members, the person is one of them, and they took part in an
// ask to them on it (asksOf, DecidedAsk.TookPart). It says nothing of an ask
// open to the person: while one stands the bead is theirs to answer, and each
// caller deals with that its own way (Waiting leaves the bead among their
// decisions, the web's waits shows the ask with the wait). Both ask PartIn
// the rest, so that Home, /line and the call count the same beads.
func PartIn(g Group, name string, tookPart bool) bool {
	return tookPart && len(g.Members) > 1 && slices.Contains(g.Members, name)
}
