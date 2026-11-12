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
	"yardr.dev/yardr/internal/dispatch"
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
// calls: the transcript is pinned while its file stands and, for a worktree
// match, nothing the match judged has moved into a match (see
// pinnedContext), and a transcript whose size and time are as at the last
// read is not opened again. A grown file is read from where the last read
// ended.
func (h *Herdr) Context(ctx context.Context, handle map[string]any, startedAt time.Time) (used, window int, ok bool, err error) {
	kind, id, err := h.sessionKind(ctx, handle)
	if err != nil {
		return 0, 0, false, err
	}
	// wide is the glob of every file a match judges, "" for an id herdr named.
	var path, key, wide string
	var judge contextJudge
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
			wide = kind.Model.Pattern(h.agentHome(kind), "*")
			judge = contextJudge{
				src: kind.Model, home: h.agentHome(kind),
				worktree: str(match, "worktree", ""), wide: wide,
				begin: startedAt.Add(-preStart), end: startedAt.Add(dispatch.ModelWindow),
			}
			path = h.pinnedContext(key)
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
	if key == "" {
		// Herdr named the session: the file its id names is the session's, and
		// no other file can make it another, so that pin rests on the file
		// alone.
		key = kind.Model.Pattern(h.agentHome(kind), id)
		path = h.pinnedContext(key)
	}
	if path == "" {
		p, err := sessionFile(kind.Model.Pattern(h.agentHome(kind), id))
		if p == "" || err != nil {
			return 0, 0, false, err
		}
		path = p
		h.pinContext(key, p, judge)
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

// contextPin is the transcript a session's lookup found, with what the lookup
// rested on, each by its stat, so that whatever could have made it answer
// otherwise is seen on the next call. A worktree match rests on every other
// candidate (one that was incomplete or did not qualify can become a match as
// it is written, with no new path) and on every directory a candidate stands
// in or could appear through, so a retry, a by-hand run or a candidate
// completed after the match is seen; a known id rests on its file alone.
// What moved under a pin is re-judged alone (see pinnedContext), and a moved
// file's new stat then replaces the one the pin holds, so the judgement the
// pin stands on is the match's own, kept beside it as judge.
type contextPin struct {
	path  string
	files []contextFileState
	dirs  []contextFileState
	judge contextJudge
}

// contextJudge is one worktree match's judgement of a candidate: the source
// and home the match read under, the worktree it matched, and the window a
// file's start had to fall in. It is empty for a known id's pin, which no
// other file can displace. begin and end are the pin's own, which the match
// key already fixes: the pre-start allowance narrows as a crew shell ages
// and the pin stands past that, and the window at pin time covers a later
// one, so a candidate kept out by it is kept out of a fresh match too.
type contextJudge struct {
	src      *store.ModelSource
	home     string
	worktree string
	wide     string
	begin    time.Time
	end      time.Time
}

// qualifies reports whether the match this judge is from would judge the
// candidate at path a match now, as matchWorkspace judges it.
func (j contextJudge) qualifies(path string) (bool, error) {
	in, err := fileNamesWorktree(j.src, path, j.worktree, j.begin, j.end)
	if err != nil || !in {
		return false, err
	}
	id, ok := j.src.SessionIn(j.home, path)
	return ok && plainSessionID(id), nil
}

// contextFileState is a file or directory a pin rests on, as it was.
type contextFileState struct {
	path  string
	size  int64
	mtime int64
}

// pinnedContext is the transcript pinned for key, "" when none is, the
// pinned file no longer stands, or what the pin rests on has moved into a
// match since. A dropped pin forgets the match but keeps the last reading,
// which is keyed by the file: a re-match that finds the same file alone
// still skips its read. Nothing moved is the ordinary pass, and it is stats
// alone: no directory is listed and no candidate opened, where the match it
// stands in for lists them all and opens each. What has moved is judged
// alone, and only it: a candidate file that moved is read again as the
// match would judge it, and a directory that moved (a candidate added or
// removed under it) lists the candidates again, so a new one is judged. A moved file that now qualifies drops the pin, as
// a fresh match would answer it or answer none for the ambiguity; one that
// still does not has its new stat stand in the pin in its old one's place,
// so the next pass that finds it quiet judges nothing. The pin is never
// answered from where a full match run now would answer another file or
// none.
func (h *Herdr) pinnedContext(key string) string {
	h.contextMu.Lock()
	pin := h.contextMatch[key]
	h.contextMu.Unlock()
	if pin == nil {
		return ""
	}
	if _, err := os.Stat(pin.path); err != nil {
		h.dropContextPin(key, pin, true)
		return ""
	}
	if pin.judge.wide == "" {
		return pin.path
	}
	var moved map[string]contextFileState
	dirMoved := false
	for _, was := range pin.files {
		now, err := contextStat(was.path)
		if err != nil {
			h.dropContextPin(key, pin, false)
			return ""
		}
		if now != was {
			if moved == nil {
				moved = make(map[string]contextFileState)
			}
			moved[was.path] = now
		}
	}
	for _, was := range pin.dirs {
		now, err := contextStat(was.path)
		if err != nil {
			h.dropContextPin(key, pin, false)
			return ""
		}
		if now != was {
			dirMoved = true
		}
	}
	if len(moved) == 0 && !dirMoved {
		return pin.path
	}
	// Work on a copy: the pin in the map is the one other calls check, and
	// it changes only here, whole, once the moved files are judged.
	updated := *pin
	updated.files = slices.Clone(pin.files)
	if dirMoved {
		files, err := filepath.Glob(pin.judge.wide)
		if err != nil || !slices.Contains(files, pin.path) {
			h.dropContextPin(key, pin, false)
			return ""
		}
		known := make(map[string]bool, len(files))
		for _, path := range files {
			known[path] = true
		}
		// A candidate the listing no longer holds but stats still find is
		// one the match would not judge as the pin did: the listing is
		// not the one the pin rested on, and the pin cannot stand on it.
		for _, was := range pin.files {
			if !known[was.path] {
				h.dropContextPin(key, pin, false)
				return ""
			}
		}
		held := make(map[string]bool, len(updated.files))
		for _, was := range updated.files {
			held[was.path] = true
		}
		for _, path := range files {
			if path == pin.path || held[path] {
				continue
			}
			now, err := contextStat(path)
			if err != nil {
				h.dropContextPin(key, pin, false)
				return ""
			}
			if moved == nil {
				moved = make(map[string]contextFileState)
			}
			moved[path] = now
			updated.files = append(updated.files, now)
		}
		dirs, err := contextFileStates(contextDirs(pin.judge.wide), "")
		if err != nil {
			h.dropContextPin(key, pin, false)
			return ""
		}
		updated.dirs = dirs
	}
	for path, now := range moved {
		qualifies, err := pin.judge.qualifies(path)
		if err != nil || qualifies {
			h.dropContextPin(key, pin, false)
			return ""
		}
		for i := range updated.files {
			if updated.files[i].path == path {
				updated.files[i] = now
				break
			}
		}
	}
	h.contextMu.Lock()
	if h.contextMatch[key] == pin {
		h.contextMatch[key] = &updated
	}
	h.contextMu.Unlock()
	return pin.path
}

// dropContextPin forgets the pin only if it is still the one the caller
// checked: another concurrent call may have installed a newer match.
func (h *Herdr) dropContextPin(key string, pin *contextPin, forgetRead bool) {
	h.contextMu.Lock()
	if h.contextMatch[key] == pin {
		delete(h.contextMatch, key)
		if forgetRead {
			delete(h.contextLast, pin.path)
		}
	}
	h.contextMu.Unlock()
}

// pinContext remembers path as the transcript key found, with the judgement
// that found it: judge.wide is the glob of every file the match judged, ""
// for a known id. Only an unambiguous match pins: ambiguity and no match
// return no id and so never reach here.
func (h *Herdr) pinContext(key, path string, judge contextJudge) {
	pin := &contextPin{path: path, judge: judge}
	if judge.wide != "" {
		files, err := filepath.Glob(judge.wide)
		if err != nil || !slices.Contains(files, path) {
			return
		}
		if pin.files, err = contextFileStates(files, path); err != nil {
			return
		}
		if pin.dirs, err = contextFileStates(contextDirs(judge.wide), ""); err != nil {
			return
		}
	}
	h.contextMu.Lock()
	defer h.contextMu.Unlock()
	if h.contextMatch == nil {
		h.contextMatch = map[string]*contextPin{}
	}
	h.contextMatch[key] = pin
}

// contextDirs are the directories a file of pattern stands in or could
// appear through: what each prefix of the pattern matches, from the file's
// own directory up to the fixed one, the empty ones too. A candidate added
// or removed moves the time of one of them.
func contextDirs(pattern string) (dirs []string) {
	for dir := filepath.Dir(pattern); ; dir = filepath.Dir(dir) {
		found, _ := filepath.Glob(dir) // the pattern itself globbed, so its prefixes are well formed
		dirs = append(dirs, found...)
		if !strings.ContainsAny(dir, "*?[") {
			return dirs
		}
	}
}

// contextFileStates snapshots paths other than the pinned transcript.
// The pinned transcript may grow normally; another candidate changing is
// judged again on its own before it can make the match ambiguous.
func contextFileStates(paths []string, pinned string) ([]contextFileState, error) {
	states := make([]contextFileState, 0, len(paths))
	for _, path := range paths {
		if path == pinned {
			continue
		}
		s, err := contextStat(path)
		if err != nil {
			return nil, err
		}
		states = append(states, s)
	}
	return states, nil
}

// contextStat is the state of the file or directory at path now.
func contextStat(path string) (contextFileState, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return contextFileState{}, err
	}
	return contextFileState{path: path, size: fi.Size(), mtime: fi.ModTime().UnixNano()}, nil
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
