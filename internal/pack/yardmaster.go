package pack

import (
	_ "embed"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"yardr.dev/yardr/internal/config"
)

// yardmasterBase is the yardmaster's role as yardr carries it: the starter
// pack's text, moved into the binary so that a yard's yardmaster prompt is
// seeded from it once and is then the yard's own file, which no pack
// overwrites. It is held to the packs' yardmaster ceiling in the tests.
//
//go:embed yardmaster_base.md
var yardmasterBase []byte

// YardmasterBase is the base a new yard's yardmaster prompt is seeded from,
// as yardr carries it now: later changes to it reach new yards only, since a
// seeded file is frozen as written. `yardr pack show starter` prints it.
func YardmasterBase() []byte { return slices.Clone(yardmasterBase) }

// LocalYardmaster is the yard's own yardmaster prompt, under its home beside
// the crew prompts the yard keeps for itself: the file init seeds from the
// base, and the one an import moves a yardmaster still prompted from a
// pack's role to.
func LocalYardmaster(home string) string {
	return filepath.Join(config.At(home).Roles(), "yardmaster.md")
}

// SeedYardmaster writes text as the yard's own yardmaster prompt, if there
// is none: a file already there is the operator's own and is kept as it is,
// never overwritten. wrote says the file was written now.
func SeedYardmaster(home string, text []byte) (path string, wrote bool, err error) {
	path = LocalYardmaster(home)
	if _, err := os.Lstat(path); err == nil {
		return path, false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", false, err
	}
	if wrote, err = installOnce(&FileOp{To: path}, text); err != nil {
		return "", false, err
	}
	return path, wrote, nil
}
