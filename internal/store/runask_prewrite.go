// What a run ask's card needs from outside a write (git, the packs
// repository), read before the write that files or approves the ask, since
// neither may be read inside one.
package store

import "context"

// prewriteRun reads before the write what the row's card needs outside
// one (runCommand.ready and runCommand.fetch): the flows and the call's
// context carrying the fetched reading. A nil row reads none.
func prewriteRun(ctx context.Context, s *Store, run AskRun, c *runCommand, argv []string) (map[string]*RepoFlows, context.Context, error) {
	if c == nil {
		return nil, ctx, nil
	}
	var repos map[string]*RepoFlows
	if c.ready != nil {
		var err error
		if repos, err = c.ready(ctx, s, run, argv); err != nil {
			return nil, ctx, err
		}
	}
	if c.fetch != nil {
		read, err := c.fetch(ctx, s, argv)
		if err != nil {
			return nil, ctx, err
		}
		ctx = context.WithValue(ctx, urlPackKey{}, read)
	}
	return repos, ctx, nil
}

// approvalRepos reads, before the write that approves the run ask seq on
// the bead id, what its row's card needs of git or of the packs
// repository (prewriteRun). An ask that cannot be read here (no bead, no
// run) is left to the write, which refuses it in its own words.
func (s *Store) approvalRepos(ctx context.Context, id string, seq uint64) (map[string]*RepoFlows, context.Context, error) {
	notes, err := s.Notes(ctx, id)
	if err != nil {
		return nil, ctx, nil
	}
	for _, n := range notes {
		if n.Seq != seq || n.Kind != NoteAsk {
			continue
		}
		ask, err := ParseAsk(n)
		if err != nil || ask.Run == nil {
			return nil, ctx, nil
		}
		if c, argv, err := parseRow(ask.Run.Argv); err == nil {
			return prewriteRun(ctx, s, *ask.Run, c, argv)
		}
	}
	return nil, ctx, nil
}
