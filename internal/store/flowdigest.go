package store

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
)

// Digest is the sha256 of the flow as the store keeps it, in hex: its text in
// the line format (String) after the normalization PutFlow does on a save.
// A pack builds its flow raw and the store normalizes it as it writes it, so
// a digest of the raw text would differ from the stored flow's wherever
// normalizing changes something (a stage the walk adds, a transition named
// twice) and the stored flow would read as edited by the yard for good. This
// one is the same for both, which makes it what an import records of the
// flow it wrote and a later plan reads back of the flow the yard has.
//
// Normalizing runs on a copy: a Flow's maps are shared with whoever else
// holds it, and PutFlow's normalize changes them where they stand.
func (f *Flow) Digest() (string, error) {
	c := *f
	c.Stages = slices.Clone(f.Stages)
	c.Terminal = slices.Clone(f.Terminal)
	c.Transitions = make(map[string][]string, len(f.Transitions))
	for s, next := range f.Transitions {
		c.Transitions[s] = slices.Clone(next)
	}
	c.Outcomes = make(map[string][]Outcome, len(f.Outcomes))
	for s, outcomes := range f.Outcomes {
		c.Outcomes[s] = slices.Clone(outcomes)
	}
	if err := c.normalize(); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(c.String()))
	return hex.EncodeToString(sum[:]), nil
}
