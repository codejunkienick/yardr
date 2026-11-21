package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"yardr.dev/yardr/internal/config"
	"yardr.dev/yardr/internal/store"
)

// The server's carrying out of an approved depot add card: it clones the
// repository the card names into the yard's depots directory as the
// yard's user, wires the push source's credential helper for a private
// fetch, and records the depot. A clone is no store write, so the
// approval records it to be carried out (store needsCarry) and this runs
// after it, under the apply lock as a pack apply does. No failure here
// names a terminal: a no-terminal operator files the card again.

func isDepotAddCarry(argv []string) bool { return store.IsDepotAddCarry(argv) }

// carryDepotAddClaimed runs a claimed depot add carrying out under the
// apply lock, as carryDepotPushClaimed does for a depot-push.
func (c *carrier) carryDepotAddClaimed(ctx context.Context, rec *store.Carry) (bool, error) {
	release, err := config.AcquireApplyLock(c.home)
	if err != nil {
		return true, fmt.Errorf("carrying out ask %d on %s: the apply lock: %w", rec.Ask, rec.Bead, err)
	}
	defer release()
	result, failure := c.carryDepotAdd(ctx, rec)
	if failure != "" {
		if err := c.st.FailCarry(ctx, rec.Bead, rec.Ask, c.boot, failure); err != nil {
			return true, err
		}
		c.log.Warn("carrying out a depot add ask failed", "bead", rec.Bead, "ask", rec.Ask, "failure", failure)
		return true, nil
	}
	if err := c.st.CompleteCarry(ctx, rec.Bead, rec.Ask, c.boot, result); err != nil {
		return true, err
	}
	c.log.Info("carried out a depot add ask", "bead", rec.Bead, "ask", rec.Ask, "depot", rec.Argv[2])
	return true, nil
}

// depotAddFail words a depot add carrying failure: what happened, and
// that filing the card again adds it. It names no terminal.
func depotAddFail(format string, args ...any) string {
	return fmt.Sprintf(format, args...) + " File the card again to add it."
}

// depotCloneDisplay is a fetch URL with any userinfo left out, as the
// store's card names one: a clone's words stand in the answer and the
// log, and neither holds a credential a URL was written with.
func depotCloneDisplay(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return raw
	}
	host := u.Hostname()
	if port := u.Port(); port != "" {
		host += ":" + port
	}
	return u.Scheme + "://" + host + u.Path
}

func (c *carrier) carryDepotAdd(ctx context.Context, rec *store.Carry) (string, string) {
	run, err := c.askRun(ctx, rec)
	if err != nil {
		return "", depotAddFail("ask %d on %s holds no depot add to carry out", rec.Ask, rec.Bead)
	}
	name, cloneURL, _, base, ok := store.DepotAddOf(run.Argv)
	if !ok {
		return "", depotAddFail("the approved card names no depot to add")
	}
	if _, err := c.st.GetDepot(ctx, name); err == nil {
		return "", depotAddFail("depot %s is in the yard already, so there is nothing to add", name)
	} else if !errors.Is(err, store.ErrNotFound) {
		return "", depotAddFail("depot %s could not be read: %v", name, err)
	}
	path := filepath.Join(c.home, store.DepotsDir, name)
	if _, err := os.Stat(path); err == nil {
		return "", depotAddFail("depot %s: %s already stands where its clone would go", name, path)
	}
	sources, err := c.st.Sources(ctx)
	if err != nil {
		return "", depotAddFail("the yard's sources are not read: %v", err)
	}
	ps, found, err := store.DepotPushSourceFor(sources, c.home, cloneURL)
	if err != nil {
		return "", depotAddFail("the push source for depot %s could not be checked", name)
	}
	fingerprint := ""
	if found {
		fingerprint = store.DepotPushSourceFingerprint(ps)
	}
	if fingerprint != run.Seen["push source"] {
		return "", depotAddFail("the push source for depot %s changed after approval", name)
	}
	if found && !store.PushTokenStanding(c.home, ps) {
		return "", depotAddFail("no %s stands for the source %s, so its credential helper has no token", ps.TokenName, ps.Name)
	}
	// The clone is this carrying out's alone from here: a failure takes
	// it away again, so a card filed again clones into a free place.
	removeClone := func() { _ = os.RemoveAll(path) }
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", depotAddFail("the depots directory of depot %s was not made: %v", name, err)
	}
	var cloneArgs []string
	if found {
		cloneArgs = append(cloneArgs, "-c", ps.HelperKey+"="+ps.Helper)
	}
	cloneArgs = append(cloneArgs, "clone", "--", cloneURL, path)
	cmd := exec.CommandContext(ctx, "git", cloneArgs...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		removeClone()
		words := strings.TrimSpace(strings.ReplaceAll(stderr.String(), cloneURL, depotCloneDisplay(cloneURL)))
		if words == "" {
			words = err.Error()
		}
		return "", depotAddFail("cloning %s into %s failed: %s", depotCloneDisplay(cloneURL), path, words)
	}
	if _, err := store.ResolveRef(ctx, path, base+"^{commit}"); err != nil {
		removeClone()
		return "", depotAddFail("depot %s: base %s does not resolve in the clone of %s: name the branch the repository has", name, base, depotCloneDisplay(cloneURL))
	}
	if found {
		if _, err := gitConfig(path, ps.HelperKey, ps.Helper); err != nil {
			removeClone()
			return "", depotAddFail("the helper line in depot %s's git config was not written: %v", name, err)
		}
		localHelpers, ok := gitConfigGetAll(path, ps.HelperKey, true)
		if !ok || len(localHelpers) != 1 || !store.IsPackHelper(localHelpers[0], ps.Helper) {
			removeClone()
			return "", depotAddFail("the pack's helper does not stand in depot %s's git config after writing it", name)
		}
	}
	depot := store.Depot{Name: name, Path: path, Kind: store.DepotGit, Base: base, Test: run.Test}
	if err := c.st.PutDepot(ctx, depot, rec.Actor); err != nil {
		removeClone()
		return "", depotAddFail("depot %s was not recorded: %v", name, err)
	}
	result := fmt.Sprintf("depot %s → %s (git, base %s)", name, path, base)
	if run.Test != "" {
		result += "\ntest: " + run.Test
	}
	return result, ""
}
