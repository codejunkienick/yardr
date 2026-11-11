package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"yardr.dev/yardr/internal/agents"
	"yardr.dev/yardr/internal/dispatch"
	"yardr.dev/yardr/internal/store"
)

// startDir starts a bead of a dir depot. There is no branch and no worktree:
// the agent works in the depot's directory itself, in a workspace opened
// there for the bead (reused by its later stages), and its brief goes under
// the directory's .yardr/briefs/. A train's child is no different: a train
// in a dir depot only groups, so no train branch is made either.
func (h *Herdr) startDir(ctx context.Context, job dispatch.Job, hookEnv []string) (map[string]any, error) {
	depot := job.Depot
	name := AgentName(job.Bead.ID, job.Bead.Stage)
	if st, err := os.Stat(depot.Path); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("depot %s: directory %q does not exist", depot.Name, depot.Path)
	}
	secrets, err := h.groupEnv(job)
	if err != nil {
		return nil, err
	}
	label := WorkspaceLabel(depot.Name, job.Group, job.Train, job.Bead.ID)
	ws, err := h.dirWorkspace(ctx, depot, job.Bead.ID, label)
	if err != nil {
		return nil, err
	}
	handle := map[string]any{"agent": name, "dir": depot.Path, "depot": depot.Name, "bead": job.Bead.ID,
		"workspace": ws.Workspace.ID}

	// As for a worktree: the environment goes over the socket, never on a
	// command line (see call).
	tmp, err := h.sessionTmp(job.Group.Config, handle)
	if err != nil {
		return handle, err
	}
	raw, err := h.call(ctx, "tab.create", map[string]any{
		"workspace_id": ws.Workspace.ID, "cwd": depot.Path,
		"label": TabLabel(depot.Name, job.Bead.Stage, job.Train, job.Bead.ID),
		"env":   envMap(withMemoryOff(agents.Kind(job.Group.Config), withEnvFile(append(h.agentEnvWithHook(job, name, hookEnv), sessionTmpEnv(tmp)...), secrets))),
	})
	if err != nil {
		return handle, err
	}
	var t herdrResult
	if err := json.Unmarshal(raw, &t); err != nil {
		return handle, fmt.Errorf("tab result: %w", err)
	}
	pane := t.RootPane.ID
	handle["tab"], handle["pane"] = t.Tab.ID, pane
	h.shellOn(ctx, pane, handle)
	if err := h.shellKnown(handle, func(handle map[string]any) error {
		return h.Store.SetHandle(ctx, job.Session.ID, handle)
	}); err != nil {
		return handle, err
	}
	if !ws.AlreadyOpen && ws.Tab.ID != "" {
		// As for a worktree: the new workspace's root tab is an idle shell.
		_, _ = h.run(ctx, "tab", "close", ws.Tab.ID)
	}

	dir := DirBriefDir(depot.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return handle, err
	}
	// A dir depot may still be inside a checkout; its briefs are no change.
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*\n"), 0o600); err != nil {
		return handle, err
	}
	return h.brief(ctx, job, name, depot.Path, dir, pane, handle)
}

// dirWorkspace opens the herdr workspace a dir depot's bead works in: the one
// an earlier stage opened, while it is still open, else a new one in the
// depot's directory. herdr reports no checkout for a plain directory, so the
// earlier one is found by its label, which ends in the bead's id, and a
// pane in it that sits in the depot's directory. A reused workspace is
// relabelled for the current group.
func (h *Herdr) dirWorkspace(ctx context.Context, depot *store.Depot, bead, label string) (herdrResult, error) {
	var ws herdrResult
	id, err := h.findDirWorkspace(ctx, depot, bead)
	if err != nil {
		return ws, err
	}
	if id != "" {
		_, _ = h.run(ctx, "workspace", "rename", id, label) // cosmetic, as for a worktree
		ws.Workspace.ID, ws.AlreadyOpen = id, true
		return ws, nil
	}
	raw, err := h.run(ctx, "workspace", "create", "--cwd", depot.Path, "--label", label, "--no-focus")
	if err != nil {
		return ws, fmt.Errorf("workspace for %s in %s: %w", bead, depot.Path, err)
	}
	if err := json.Unmarshal(raw, &ws); err != nil {
		return ws, fmt.Errorf("workspace result: %w", err)
	}
	return ws, nil
}

// findDirWorkspace is the id of the open workspace of bead in the dir depot,
// or "" when there is none (see dirWorkspace).
func (h *Herdr) findDirWorkspace(ctx context.Context, depot *store.Depot, bead string) (string, error) {
	raw, err := h.run(ctx, "workspace", "list")
	if err != nil {
		return "", err
	}
	var wl struct {
		Workspaces []struct {
			ID       string          `json:"workspace_id"`
			Label    string          `json:"label"`
			Worktree json.RawMessage `json:"worktree"`
		} `json:"workspaces"`
	}
	if err := json.Unmarshal(raw, &wl); err != nil {
		return "", fmt.Errorf("workspace list: %w", err)
	}
	short := ShortID(depot.Name, bead)
	var ids []string
	for _, w := range wl.Workspaces {
		f := strings.Fields(w.Label)
		if len(w.Worktree) == 0 && len(f) > 0 && f[len(f)-1] == short {
			ids = append(ids, w.ID)
		}
	}
	if len(ids) == 0 {
		return "", nil
	}
	raw, err = h.run(ctx, "pane", "list")
	if err != nil {
		return "", err
	}
	var pl struct {
		Panes []struct {
			Workspace string `json:"workspace_id"`
			Cwd       string `json:"cwd"`
		} `json:"panes"`
	}
	if err := json.Unmarshal(raw, &pl); err != nil {
		return "", fmt.Errorf("pane list: %w", err)
	}
	for _, id := range ids {
		for _, p := range pl.Panes {
			if p.Workspace == id && sameDir(p.Cwd, depot.Path) {
				return id, nil
			}
		}
	}
	return "", nil
}

// sameDir reports whether a and b name the same directory, symlinks
// resolved where they can be.
func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	real := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return filepath.Clean(p)
	}
	return real(a) == real(b)
}
