package runner

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"yardr.dev/yardr/internal/agents"
	"yardr.dev/yardr/internal/peer"
	"yardr.dev/yardr/internal/store"
)

const contextStartedAt = "context_started_at"

// Context is the context the session's agent holds, in tokens: the last
// use and window its own session file holds, by the kind's context source
// (store.ContextSource). ok is false when the kind names no source, or
// the file says no use yet (no reply, or none with numbers). The file is
// found as the model source's is (see Model).
//
// The daemon asks on every idle pass, so both halves are remembered across
// calls: the transcript a worktree match found is pinned while its file
// stands and the session dir holds the same files, and a transcript whose
// size and time are as at the last read is not opened again. A grown file
// is read from where the last read ended.
func (h *Herdr) Context(ctx context.Context, handle map[string]any, startedAt time.Time) (used, window int, ok bool, err error) {
	kind, id, err := h.sessionKind(ctx, handle)
	if err != nil {
		return 0, 0, false, err
	}
	var path, key string
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
				preStart, err = crewContextAllowance(ctx, handle, startedAt, crewShellStartTimeout)
				if err != nil {
					return 0, 0, false, err
				}
			}
		}
		if kind.Kind == "" {
			// As matchSession resolves it: herdr named no agent, so the
			// handle's own kind says what the pin below is under.
			if name, _ := match[harnessKind].(string); name != "" {
				kind, _ = agents.Lookup(name)
			}
		}
		key = contextMatchKey(kind, match, startedAt)
		if kind.Model != nil {
			path = h.pinnedContext(key, kind.Model.Pattern(h.agentHome(kind), "*"))
		}
		if path == "" {
			kind, id, err = h.matchSession(kind, match, startedAt, preStart)
			if err != nil || id == "" {
				return 0, 0, false, err
			}
		}
	}
	if kind.Model == nil || kind.Context == nil {
		return 0, 0, false, nil
	}
	if path == "" {
		p, err := sessionFile(kind.Model.Pattern(h.agentHome(kind), id))
		if p == "" || err != nil {
			return 0, 0, false, err
		}
		path = p
		if key != "" {
			h.pinContext(key, p, kind.Model.Pattern(h.agentHome(kind), "*"))
		}
	}
	return h.cachedContext(path, *kind.Context)
}

// contextMatchKey identifies one session's transcript match: the kind the
// match runs under, the agent and worktree it matches by, and the start it
// matches from. The pre-start allowance is none of it: it narrows as the
// shell ages, and the pin stands past that.
func contextMatchKey(kind store.Harness, match map[string]any, startedAt time.Time) string {
	name := kind.Kind
	if name == "" {
		name, _ = match[harnessKind].(string)
	}
	agent, _ := match["agent"].(string)
	worktree, _ := match["worktree"].(string)
	return name + "\x00" + agent + "\x00" + worktree + "\x00" + strconv.FormatInt(startedAt.UnixNano(), 10)
}

// contextPin is the transcript a session's worktree match found. It keeps
// the sorted paths and the stat of every other candidate, so a later retry,
// by-hand run, or candidate completed after the match is seen on the next
// call.
type contextPin struct {
	path       string
	files      []string
	otherFiles []contextFileState
}

// contextFileState is a candidate transcript other than the pinned one. A
// candidate that was incomplete or did not qualify can become a match as it
// is written, without changing the glob's paths, so its stat must still be
// checked on each pass.
type contextFileState struct {
	path  string
	size  int64
	mtime int64
}

// pinnedContext is the transcript pinned for key, "" when none is, the
// pinned file no longer stands, or the candidate paths or their state
// changed. A dropped pin forgets the match but keeps the last reading, which
// is keyed by the file: a re-match that finds the same file alone still skips
// its read. The glob and stats open nothing; the match it stands in for opens
// every candidate.
func (h *Herdr) pinnedContext(key, pattern string) string {
	h.contextMu.Lock()
	pin, ok := h.contextMatch[key]
	h.contextMu.Unlock()
	if !ok {
		return ""
	}
	if _, err := os.Stat(pin.path); err != nil {
		h.dropContextPin(key, pin, true)
		return ""
	}
	files, err := filepath.Glob(pattern)
	if err == nil {
		slices.Sort(files)
	}
	if err != nil || !slices.Equal(files, pin.files) {
		h.dropContextPin(key, pin, false)
		return ""
	}
	otherFiles, err := contextFileStates(files, pin.path)
	if err != nil || !slices.Equal(otherFiles, pin.otherFiles) {
		h.dropContextPin(key, pin, false)
		return ""
	}
	return pin.path
}

// dropContextPin forgets the pin only if it is still the one the caller
// checked: another concurrent call may have installed a newer match.
func (h *Herdr) dropContextPin(key string, pin contextPin, forgetRead bool) {
	h.contextMu.Lock()
	if current, ok := h.contextMatch[key]; ok && current.path == pin.path && slices.Equal(current.files, pin.files) && slices.Equal(current.otherFiles, pin.otherFiles) {
		delete(h.contextMatch, key)
		if forgetRead {
			delete(h.contextLast, pin.path)
		}
	}
	h.contextMu.Unlock()
}

// pinContext remembers path as the transcript key matched, with the other
// candidates as they are. Only an unambiguous match pins: ambiguity and no
// match return no id and so never reach here.
func (h *Herdr) pinContext(key, path, pattern string) {
	files, err := filepath.Glob(pattern)
	if err != nil {
		return
	}
	slices.Sort(files)
	if !slices.Contains(files, path) {
		return
	}
	otherFiles, err := contextFileStates(files, path)
	if err != nil {
		return
	}
	h.contextMu.Lock()
	defer h.contextMu.Unlock()
	if h.contextMatch == nil {
		h.contextMatch = map[string]contextPin{}
	}
	h.contextMatch[key] = contextPin{path: path, files: files, otherFiles: otherFiles}
}

// contextFileStates snapshots candidates other than the pinned transcript.
// The pinned transcript may grow normally; another candidate changing can
// make the match ambiguous, so a later pass must run the matcher again.
func contextFileStates(files []string, pinned string) ([]contextFileState, error) {
	states := make([]contextFileState, 0, len(files))
	for _, path := range files {
		if path == pinned {
			continue
		}
		fi, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		states = append(states, contextFileState{path: path, size: fi.Size(), mtime: fi.ModTime().UnixNano()})
	}
	return states, nil
}

// crewShellStartTimeout is how long Context gives the ps that reads an older
// crew handle's shell start time. The read runs in a crew session's keep, and
// the crew pass joins every keep before it hands starts on and routes beads,
// so a ps that never answers holds all of that for as long as it is let. ps
// names one pid in milliseconds: five seconds is far over that and costs the
// pass little.
const crewShellStartTimeout = 5 * time.Second

// crewContextAllowance excludes files from before an older handle's own pane
// existed. It gives no allowance (0, nil) where it cannot read the shell's
// start time: the shell is gone, or ps did not answer within wait.
func crewContextAllowance(parent context.Context, handle map[string]any, startedAt time.Time, wait time.Duration) (time.Duration, error) {
	pid, hasPID := uintOf(handle["shell_pid"])
	start, hasStart := uintOf(handle["shell_start"])
	if !hasPID || !hasStart || pid == 0 || uint64(int(pid)) != pid || !peer.HaveStat() || !peer.Alive(int(pid), start) {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(parent, wait)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ps", "-o", "lstart=", "-p", strconv.FormatUint(pid, 10))
	cmd.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC0")
	raw, err := cmd.Output()
	if !peer.Alive(int(pid), start) {
		return 0, nil
	}
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) && parent.Err() == nil {
		// ps did not answer in time. That is no failure of the read: the
		// session gets no allowance on this pass, as when its shell is gone,
		// and the next idle pass asks again. A caller cancellation or deadline
		// still gets the error below.
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

// contextRead is what the last read of one transcript saw: its size and
// time, the offset it read to at the last line boundary, and the use,
// window and reading it ended with. A call's use and window stay together
// as the window can change.
type contextRead struct {
	size         int64
	mtime        time.Time
	offset       int64
	used, window int
	ok           bool
}

// cachedContext is the last use and window the transcript at path holds,
// remembered across calls: an unchanged file (its size and time as at the
// last read) is not opened again, a grown one is read from where the last
// read ended, and a shrunk one (a rotation or truncation) from the start.
// The offset rests on line boundaries, so a line the last read left
// half-written is read whole.
func (h *Herdr) cachedContext(path string, source store.ContextSource) (used, window int, ok bool, err error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, 0, false, err
	}
	h.contextMu.Lock()
	last, seen := h.contextLast[path]
	h.contextMu.Unlock()
	if seen && fi.Size() == last.size && fi.ModTime().Equal(last.mtime) {
		return last.used, last.window, last.ok, nil
	}
	start := int64(0)
	if seen && fi.Size() >= last.size {
		start, used, window, ok = last.offset, last.used, last.window, last.ok
	}
	var readErr error
	end, err := eachLineFrom(path, start, func(line []byte) bool {
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
	h.contextMu.Lock()
	if h.contextLast == nil {
		h.contextLast = map[string]contextRead{}
	}
	h.contextLast[path] = contextRead{size: fi.Size(), mtime: fi.ModTime(), offset: end, used: used, window: window, ok: ok}
	h.contextMu.Unlock()
	return used, window, ok, nil
}
