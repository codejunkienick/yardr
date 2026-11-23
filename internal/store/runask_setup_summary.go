// The setup card's one-line words (runask_setup.go stands at its file-size
// ceiling, so these stand beside it): what takes it back, who its grants are
// for, and what approving it does in one line.
package store

import (
	"fmt"
	"strings"
)

// TakenBack is the card's line for what takes the setup back, with who may
// run it.
func (s AskSetup) TakenBack() string { return or(s.BackBy, "anyone") + " can take it back: " + s.Back }

// Grantees are who the setup's grants are for, in words.
func (s AskSetup) Grantees() string { return granteesWord(s.To) }

// granteesWord is who the grants are for, in words: each crew member and
// each group by name.
func granteesWord(to []Grantee) string {
	words := make([]string, len(to))
	for i, t := range to {
		if t.Crew != "" {
			words[i] = "crew " + t.Crew
			continue
		}
		words[i] = "group " + t.Group
	}
	return andList(words)
}

// Summary is what approving the setup does, in one line: the apply, the
// account and the grants.
func (s AskSetup) Summary() string {
	return fmt.Sprintf("yardr pack apply %s, the account %s, and the preset's grants to %s",
		s.Pack, strings.Join(s.Account, " and "), s.Grantees())
}
