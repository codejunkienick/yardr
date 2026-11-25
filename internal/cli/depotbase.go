package cli

import (
	"context"
	"fmt"
	"strings"

	"yardr.dev/yardr/internal/store"
)

// addBase is the base depot add registers the git checkout at path with:
// the one given, else the checkout's default. Branches for beads start from
// it, so it has to resolve, and one that does not is refused with the refs
// that would.
func addBase(ctx context.Context, path, given string) (string, error) {
	base := given
	if base == "" {
		base = defaultBase(ctx, path)
	}
	if _, err := store.ResolveRef(ctx, path, base+"^{commit}"); err != nil {
		refs, _ := git(ctx, path, "for-each-ref", "--format=%(refname:short)",
			"refs/remotes/origin/HEAD", "refs/remotes/origin/main", "refs/remotes/origin/master",
			"refs/heads/main", "refs/heads/master")
		return "", fmt.Errorf("base %q does not resolve in %s (found: %s); pass --base",
			base, path, strings.Join(strings.Fields(refs), ", "))
	}
	return base, nil
}

// defaultBase is the base of a git depot that was given none: the branch the
// checkout holds as origin's default (refs/remotes/origin/HEAD), else
// origin/main, which depot add gave every depot before it read one. git clone
// writes that ref from what the remote answers, so a repository whose default
// branch is master, trunk or develop is cloned and registered without anyone
// looking the branch up first, which is what lets a list of repositories be
// registered by a loop. The ref is read and nothing is fetched: a checkout
// whose remote moved its default since says the old one until git remote
// set-head origin --auto, and --base says another.
func defaultBase(ctx context.Context, path string) string {
	if b, _ := git(ctx, path, "symbolic-ref", "--short", "-q", "refs/remotes/origin/HEAD"); b != "" {
		return b
	}
	return "origin/main"
}
