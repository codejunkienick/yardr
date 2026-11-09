package store

import (
	"context"
	"slices"

	bolt "go.etcd.io/bbolt"
)

// NextStages is where a bead may go from its current stage.
type NextStages struct {
	Bead  string `json:"bead"`
	Stage string `json:"stage"`
	// Flow governs the bead; nil means no flow, and any stage is allowed.
	Flow     *Flow       `json:"flow,omitempty"`
	Source   *FlowSource `json:"source,omitempty"`   // where Flow came from
	Stages   []string    `json:"stages"`             // allowed next stages (empty without a flow)
	Terminal []string    `json:"terminal,omitempty"` // those of Stages that close the bead
	// Outcomes are how work in the stage can end, for `yardr bead done`;
	// empty when the flow leaves the stage to explicit moves. Each is read for
	// this bead: where reporting it takes the bead now, which a round limit
	// can change from the flow's edge (OutcomeNow).
	Outcomes []OutcomeNow `json:"outcomes,omitempty"`
	// StageFile is a repository flow's definition of Stage, when it has
	// one. A store flow's is a file in the yard's home, which the store
	// does not read: whoever knows the home asks HomeStageFile.
	StageFile *StageFile `json:"stage_file,omitempty"`
	// TemplateError is why entering Stage put no stage ask: the
	// bad_template event's reason for this stay, where the ask would
	// stand. Empty when the entry put its asks, or the stage has no
	// template. The bead still moves by hand.
	TemplateError string `json:"template_error,omitempty"`
	// Back is the stage whose question brought the bead to Stage, allowed
	// as an advance target alongside Stages; "" when none did (askerOf).
	Back string `json:"back,omitempty"`
}

// RoundsLines are the lines OutcomeNow.RoundsLine says for the stage's
// outcomes, in the order the flow gives them: one for each limited outcome
// whose stage has rounds counted. Nil next, and a stage with none, say none.
func (n *NextStages) RoundsLines() []string {
	if n == nil {
		return nil
	}
	var out []string
	for _, o := range n.Outcomes {
		if line := o.RoundsLine(n.Stage); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// NextStages reports the flow governing bead id, the stages it may be
// advanced to, and a repository flow's stage file of the stage it is in.
func (s *Store) NextStages(ctx context.Context, id string) (*NextStages, error) {
	b, err := s.GetBead(ctx, id)
	if err != nil {
		return nil, err
	}
	repo, err := s.repoFor(ctx, b.Depot, b.Type, PinnedFlow(b))
	if err != nil {
		return nil, err
	}
	var out *NextStages
	err = s.read(func(tx *bolt.Tx) error {
		row, err := loadBead(tx, id)
		if err != nil {
			return err
		}
		out = &NextStages{Bead: row.ID, Stage: row.Stage, Stages: []string{}}
		f, src, err := resolveFlow(tx, row.Depot, row.Type, PinnedFlow(&row.Bead), repo)
		if err != nil || f == nil {
			return changedMeanwhile(err)
		}
		return out.fill(tx, row, f, src, repo)
	})
	return out, err
}

// fill completes n, the bare answer for row, from the flow f that governs it,
// which came from src and was read with repo. It is the part of NextStages
// that follows the flow's resolution, so that a read of many beads (Waiting)
// resolves each flow once and asks it of each bead.
func (n *NextStages) fill(tx *bolt.Tx, row *beadRow, f *Flow, src *FlowSource, repo *RepoFlows) error {
	n.Flow, n.Source, n.Stages = f, src, f.Next(row.Stage)
	for _, o := range f.OutcomesFrom(row.Stage) {
		n.Outcomes = append(n.Outcomes, row.takes(o))
	}
	n.StageFile = stageFileOf(f, src, repo, row.Stage)
	for _, st := range n.Stages {
		if f.IsTerminal(st) {
			n.Terminal = append(n.Terminal, st)
		}
	}
	if asker, err := askingStage(tx, row.UUID, row.Stage, f); err != nil {
		return err
	} else if asker != "" {
		n.Back = asker
		if !slices.Contains(n.Stages, asker) {
			n.Stages = append(n.Stages, asker)
		}
	}
	entry, err := latestStayEntry(tx, row.UUID)
	if err != nil {
		return err
	}
	n.TemplateError, err = stayTemplateError(tx, row.UUID, row.Stage, entry)
	return err
}
