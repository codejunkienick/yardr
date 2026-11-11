package cli

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"yardr.dev/yardr/internal/store"
)

// primeNoteTextLines bounds a plain note's text lines in prime --bead. A
// bead back from a failed landing carries the assembly's whole run log
// before the handoff saying why it came back, and the log alone can pass
// what the reader prints; the bound keeps the handoff on the page.
const primeNoteTextLines = 19

// primeNoteBlocks renders a bead's notes for prime --bead: latest first,
// so the handoff (why the bead came back, what to do) precedes any run
// log, and each plain note's text bound to primeNoteTextLines with a
// pointer to the full text. An ask is the decision's substance — its
// carried declaration, its take-back line — so asks and answers print
// whole. The blocks are built oldest first and reversed after: an ask's
// current answer resolves in note order, so the notes go in as they stand.
// Bead show and --json keep every note whole; only this rendering bounds.
func primeNoteBlocks(id string, notes []store.Note, as map[string]string, home string) []string {
	bounded := make([]store.Note, len(notes))
	for i, n := range notes {
		bounded[i] = n
		if n.Kind != store.NoteNote {
			continue
		}
		lines := strings.Split(strings.TrimSpace(n.Text), "\n")
		if len(lines) <= primeNoteTextLines {
			continue
		}
		more := len(lines) - primeNoteTextLines
		noun := "more lines"
		if more == 1 {
			noun = "more line"
		}
		bounded[i].Text = strings.Join(lines[:primeNoteTextLines], "\n") +
			fmt.Sprintf("\n... and %d %s: yardr bead show %s has the note whole", more, noun, id)
	}
	blocks := store.NoteBlocksMarked(bounded, time.Now(), as, home)
	slices.Reverse(blocks)
	return blocks
}
