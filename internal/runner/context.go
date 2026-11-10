package runner

import (
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"yardr.dev/yardr/internal/peer"
	"yardr.dev/yardr/internal/store"
)

const contextStartedAt = "context_started_at"

// Context is the context the session's agent holds, in tokens: the last
// use and window its own session file holds, by the kind's context source
// (store.ContextSource). ok is false when the kind names no source, or
// the file says no use yet (no reply, or none with numbers). The file is
// found as the model source's is (see Model).
func (h *Herdr) Context(ctx context.Context, handle map[string]any, startedAt time.Time) (used, window int, ok bool, err error) {
	kind, id, err := h.sessionKind(ctx, handle)
	if err != nil {
		return 0, 0, false, err
	}
	if id == "" {
		var preStart time.Duration
		if at, has := handle[contextStartedAt]; has {
			text, _ := at.(string)
			startedAt, err = time.Parse(time.RFC3339Nano, text)
			if err != nil {
				return 0, 0, false, fmt.Errorf("context start time: invalid timestamp")
			}
		}
		match := handle
		if str(handle, "worktree", "") == "" && str(handle, "dir", "") != "" {
			// Crew sessions run in a directory rather than a bead's worktree.
			match = maps.Clone(handle)
			match["worktree"] = handle["dir"]
			if _, has := handle[contextStartedAt]; !has {
				preStart, err = crewContextAllowance(ctx, handle, startedAt)
				if err != nil {
					return 0, 0, false, err
				}
			}
		}
		kind, id, err = h.matchSession(kind, match, startedAt, preStart)
		if err != nil || id == "" {
			return 0, 0, false, err
		}
	}
	if kind.Model == nil || kind.Context == nil {
		return 0, 0, false, nil
	}
	return lastContext(kind.Model.Pattern(h.agentHome(kind), id), *kind.Context)
}

// crewContextAllowance excludes files from before an older handle's own pane existed.
func crewContextAllowance(ctx context.Context, handle map[string]any, startedAt time.Time) (time.Duration, error) {
	pid, hasPID := uintOf(handle["shell_pid"])
	start, hasStart := uintOf(handle["shell_start"])
	if !hasPID || !hasStart || pid == 0 || uint64(int(pid)) != pid || !peer.HaveStat() || !peer.Alive(int(pid), start) {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ps", "-o", "lstart=", "-p", strconv.FormatUint(pid, 10))
	cmd.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC0")
	raw, err := cmd.Output()
	if !peer.Alive(int(pid), start) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("crew shell start time: %w", err)
	}
	at, err := time.Parse(time.ANSIC, strings.TrimSpace(string(raw)))
	if err != nil {
		return 0, fmt.Errorf("crew shell start time: invalid timestamp")
	}
	// Linux ps truncates both boot time and elapsed ticks to seconds; exclude both uncertain seconds.
	return min(10*time.Second, max(0, startedAt.Sub(at.Add(2*time.Second)))), nil
}

// lastContext keeps a call's use and window together as the window can change.
func lastContext(pattern string, source store.ContextSource) (used, window int, ok bool, err error) {
	path, err := sessionFile(pattern)
	if path == "" || err != nil {
		return 0, 0, false, err
	}
	var readErr error
	err = eachLine(path, func(line []byte) bool {
		n, w, has, e := source.ReadIn(line)
		if e != nil {
			readErr = e
			return true
		}
		if has {
			used, window, ok = n, w, true
			if source.WindowField != "" && w == 0 {
				// A newer call without its window cannot reuse an older reading.
				used, window, ok = 0, 0, false
			}
		}
		return false
	})
	if err != nil {
		return 0, 0, false, err
	}
	if readErr != nil {
		return 0, 0, false, fmt.Errorf("read session context: %w", readErr)
	}
	return used, window, ok, nil
}
