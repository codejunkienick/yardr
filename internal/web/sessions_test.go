package web_test

import (
	"encoding/json"
	"testing"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/web"
)

// The sessions page shows an exec session's pid whole, even a seven-digit
// one the store hands back as a float64; a handle it cannot read still
// shows rather than hides.
func TestRunnerOfPid(t *testing.T) {
	t.Parallel()
	var floatHandle map[string]any
	if err := json.Unmarshal([]byte(`{"pid": 2003381}`), &floatHandle); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		s    store.Session
		want string
	}{
		{"herdr with its agent", store.Session{Runner: "herdr", Handle: map[string]any{"agent": "builder-2"}}, "herdr builder-2"},
		{"exec with its pid", store.Session{Runner: "exec", Handle: map[string]any{"pid": 7}}, "exec pid 7"},
		{"seven-digit pid through JSON", store.Session{Runner: "exec", Handle: floatHandle}, "exec pid 2003381"},
		{"corrupt pid still shows", store.Session{Runner: "exec", Handle: map[string]any{"pid": "abc"}}, "exec pid abc"},
		{"starting, no handle yet", store.Session{Runner: "herdr"}, "herdr"},
	} {
		if got := web.RunnerOf(c.s); got != c.want {
			t.Errorf("%s: RunnerOf() = %q, want %q", c.name, got, c.want)
		}
	}
}
