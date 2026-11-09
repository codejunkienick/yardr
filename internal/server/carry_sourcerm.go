// The carrying out of an approved source rm (runask_sourcerm.go in the
// store): the approval removed the source in its own write, and what is
// left is the files of its commands under the yard's home, which no store
// write removes. They are removed here, under the apply lock the carrying
// out holds, so that no carrying out that writes the same copies is cut
// across, and only while no source of the name stands: a source added again
// since owns the directory the removal would take.
package server

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"yardr.dev/yardr/internal/source"
	"yardr.dev/yardr/internal/store"
)

// isSourceRmCarry reports whether the approved command is a source rm.
func isSourceRmCarry(argv []string) bool {
	return len(argv) == 3 && argv[0] == "source" && argv[1] == "rm"
}

// carrySourceRm removes the files of the commands of the source the run ask
// removed. A source of the name in the yard again leaves them where they
// are, as a holding failure; a removal that fails says which directory to
// remove by hand.
func (c *carrier) carrySourceRm(ctx context.Context, run *store.AskRun) (string, string) {
	name := run.Argv[2]
	dir := filepath.Join(c.home, "sources", name)
	terminal := "rm -r " + dir
	if _, err := c.st.GetSource(ctx, name); err == nil {
		return "", staleWords(fmt.Sprintf("source %s is in the yard again since the ask was approved, and the files under %s are its", name, dir), terminal)
	} else if !errors.Is(err, store.ErrNotFound) {
		return "", failWords(fmt.Sprintf("source %s was removed, but the yard could not tell whether it stands again: %v.", name, err), terminal)
	}
	if err := source.RemoveCommandFiles(c.home, name); err != nil {
		return "", failWords(fmt.Sprintf("source %s was removed, but the files of its commands were not: %v.", name, err), terminal)
	}
	return fmt.Sprintf("%s: removed, and the files of its commands with it", name), ""
}
