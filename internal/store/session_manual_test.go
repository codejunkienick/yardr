package store

import (
	"encoding/json"
	"testing"
)

// A session's runner is the one it recorded, and, for a session from before
// sessions recorded one, the one its handle tells; a manual group's claim is
// the session whose runner is manual.
func TestSessionRunnerName(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		s    Session
		want string
	}{
		{"manual runner", Session{Runner: "manual"}, "manual"},
		{"old manual claim", Session{Handle: map[string]any{"manual": true}}, "manual"},
		{"herdr", Session{Runner: "herdr", Handle: map[string]any{"agent": "a"}}, "herdr"},
		{"exec", Session{Runner: "exec", Handle: map[string]any{"pid": 7}}, "exec"},
		{"old herdr session", Session{Handle: map[string]any{"agent": "a"}}, "herdr"},
		{"old exec session", Session{Handle: map[string]any{"pid": 7}}, "exec"},
		{"old peer session", Session{Handle: map[string]any{"peer": "north"}}, "peer"},
		{"the recorded runner over the handle", Session{Runner: "peer", Handle: map[string]any{"agent": "a", "manual": true}}, "peer"},
		{"starting, no handle yet", Session{}, ""},
	} {
		if got := c.s.RunnerName(); got != c.want {
			t.Errorf("%s: RunnerName() = %q, want %q", c.name, got, c.want)
		}
		if got := c.s.Manual(); got != (c.want == "manual") {
			t.Errorf("%s: Manual() = %v", c.name, got)
		}
	}
}

// RunnerShown is the runner and what it runs there, as `yardr session list`
// shows it: the agent's name or the pid where the handle holds one.
func TestSessionRunnerShown(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		s    Session
		want string
	}{
		{"herdr with its agent", Session{Runner: "herdr", Handle: map[string]any{"agent": "builder-2"}}, "herdr builder-2"},
		{"exec with its pid", Session{Runner: "exec", Handle: map[string]any{"pid": 7}}, "exec pid 7"},
		{"seven-digit pid through JSON", Session{Runner: "exec", Handle: jsonHandle(t, `{"pid": 2003381}`)}, "exec pid 2003381"},
		{"corrupt pid still shows", Session{Runner: "exec", Handle: map[string]any{"pid": "abc"}}, "exec pid abc"},
		{"agent takes precedence over pid", Session{Runner: "herdr", Handle: map[string]any{"agent": "builder-2", "pid": 7}}, "herdr builder-2"},
		{"starting, no handle yet", Session{Runner: "herdr"}, "herdr"},
		{"old herdr session", Session{Handle: map[string]any{"agent": "builder-2"}}, "herdr builder-2"},
		{"manual claim", Session{Runner: "manual", Handle: map[string]any{"manual": true}}, "manual"},
	} {
		if got := c.s.RunnerShown(); got != c.want {
			t.Errorf("%s: RunnerShown() = %q, want %q", c.name, got, c.want)
		}
	}
}

// jsonHandle is obj as the store hands a handle back: through JSON, so a
// pid is a float64.
func jsonHandle(t *testing.T, obj string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(obj), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
