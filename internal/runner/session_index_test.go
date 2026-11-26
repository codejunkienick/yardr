package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// An indexed session locator pins the conversation a crew session owns:
// its directory key and its launch's first prompt, since several panes
// can share the directory. A transcript that started before the launch is
// an earlier session's record, never a match, and an index that names no
// plain id is an error, never a guess. A bead's worktree pins nothing.
func TestHerdrSessionIDIndexed(t *testing.T) {
	ctx := context.Background()
	launch := time.Date(2026, 11, 24, 10, 41, 0, 0, time.UTC)
	prompt := crewSessionReference("7f3a9c1e5b2d4a68") + " You are the yardr crew member \"yardmaster\". Run `yardr prime` and follow it."
	first := func(at time.Time, content string) string {
		return fmt.Sprintf(`{"type":"USER_INPUT","created_at":%q,"content":%q}`+"\n", at.UTC().Format(time.RFC3339Nano), content)
	}
	wrapped := func(prompt string) string { return "<USER_REQUEST>\n" + prompt + "\n</USER_REQUEST>\n<metadata/>" }
	home := func(t *testing.T, index string, transcripts map[string]string) string {
		t.Helper()
		home := t.TempDir()
		write := func(path, body string) {
			t.Helper()
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if index != "" {
			write(filepath.Join(home, "cache", "last_conversations.json"), index)
		}
		for id, body := range transcripts {
			write(filepath.Join(home, "brain", id, ".system_generated", "logs", "transcript.jsonl"), body)
		}
		return home
	}
	crew := func(dir string) map[string]any {
		return map[string]any{"kind": "agy", "dir": dir, launchReference: "7f3a9c1e5b2d4a68",
			firstPrompt: prompt, contextStartedAt: launch.Format(time.RFC3339Nano)}
	}
	askCrew := func(t *testing.T, h *Herdr, handle map[string]any) (string, error) {
		t.Helper()
		// The dispatcher's start can lag the launch the transcript saw;
		// the crew branch reads the launch off the handle instead.
		return h.SessionID(ctx, handle, launch.Add(time.Hour))
	}

	own := first(launch.Add(time.Second), wrapped(prompt))
	t.Run("bead worktree pins nothing", func(t *testing.T) {
		h := &Herdr{AgentHomes: map[string]string{"agy": home(t, `{"/work/bead":"own"}`, map[string]string{"own": own})}}
		if id, err := h.SessionID(ctx, map[string]any{"kind": "agy", "worktree": "/work/bead"}, launch); id != "" || err != nil {
			t.Fatalf("session = %q, %v; want \"\", nil", id, err)
		}
	})
	t.Run("earlier record is no match", func(t *testing.T) {
		old := first(launch.Add(-time.Second), wrapped(prompt))
		h := &Herdr{AgentHomes: map[string]string{"agy": home(t, `{"/work/crew":"old"}`, map[string]string{"old": old})}}
		if id, err := askCrew(t, h, crew("/work/crew")); id != "" || err != nil {
			t.Fatalf("session = %q, %v; want \"\", nil", id, err)
		}
	})
	t.Run("no index is no session", func(t *testing.T) {
		h := &Herdr{AgentHomes: map[string]string{"agy": home(t, "", map[string]string{"own": own})}}
		if id, err := askCrew(t, h, crew("/work/crew")); id != "" || err != nil {
			t.Fatalf("session = %q, %v; want \"\", nil", id, err)
		}
	})
	t.Run("no transcript is no session", func(t *testing.T) {
		h := &Herdr{AgentHomes: map[string]string{"agy": home(t, `{"/work/crew":"gone"}`, nil)}}
		if id, err := askCrew(t, h, crew("/work/crew")); id != "" || err != nil {
			t.Fatalf("session = %q, %v; want \"\", nil", id, err)
		}
	})
	t.Run("invalid index is an error", func(t *testing.T) {
		h := &Herdr{AgentHomes: map[string]string{"agy": home(t, `null`, map[string]string{"own": own})}}
		id, err := askCrew(t, h, crew("/work/crew"))
		if id != "" || err == nil || !strings.Contains(err.Error(), "invalid JSON object") {
			t.Fatalf("session = %q, %v; want an invalid index error", id, err)
		}
	})
	t.Run("hostile id is an error", func(t *testing.T) {
		h := &Herdr{AgentHomes: map[string]string{"agy": home(t, `{"/work/crew":"../escape"}`, map[string]string{"own": own})}}
		id, err := askCrew(t, h, crew("/work/crew"))
		if id != "" || err == nil || !strings.Contains(err.Error(), "not a plain session id") || strings.Contains(err.Error(), "escape") {
			t.Fatalf("session = %q, %v; want a plain id error without the file's text", id, err)
		}
	})
	t.Run("herdr id wins over the index", func(t *testing.T) {
		h := &Herdr{Bin: fakeHerdrBin(t, `echo '{"result":{"agent":{"agent":"agy","agent_session":{"kind":"id","value":"named"},"agent_status":"idle"}}}'`)}
		id, err := h.SessionID(ctx, map[string]any{"agent": "crew", "kind": "agy", "worktree": "/work/bead"}, launch)
		if id != "named" || err != nil {
			t.Fatalf("session = %q, %v; want named, nil", id, err)
		}
	})
	t.Run("crew verifies its launch prompt", func(t *testing.T) {
		otherPrompt := strings.Replace(prompt, "7f3a9c1e5b2d4a68", "a-different-launch", 1)
		other := first(launch.Add(time.Second), wrapped(otherPrompt))
		h := &Herdr{AgentHomes: map[string]string{"agy": home(t, `{"/work/crew":"other"}`, map[string]string{"other": other, "own": own})}}
		if id, err := askCrew(t, h, crew("/work/crew")); id != "" || err != nil {
			t.Fatalf("another launch's conversation = %q, %v; want \"\", nil", id, err)
		}
		h.AgentHomes["agy"] = home(t, `{"/work/crew":"own"}`, map[string]string{"other": other, "own": own})
		if id, err := askCrew(t, h, crew("/work/crew")); id != "own" || err != nil {
			t.Fatalf("session = %q, %v; want own, nil", id, err)
		}
	})
	t.Run("crew without a reference pins nothing", func(t *testing.T) {
		h := &Herdr{AgentHomes: map[string]string{"agy": home(t, `{"/work/crew":"own"}`, map[string]string{"own": own})}}
		handle := crew("/work/crew")
		delete(handle, launchReference)
		if id, err := askCrew(t, h, handle); id != "" || err != nil {
			t.Fatalf("session = %q, %v; want \"\", nil", id, err)
		}
		handle = crew("/work/crew")
		handle[firstPrompt] = "You are the yardr crew member \"yardmaster\"."
		if id, err := askCrew(t, h, handle); id != "" || err != nil {
			t.Fatalf("session = %q, %v; want \"\", nil", id, err)
		}
	})
	t.Run("crew without a launch time is an error", func(t *testing.T) {
		h := &Herdr{AgentHomes: map[string]string{"agy": home(t, `{"/work/crew":"own"}`, map[string]string{"own": own})}}
		handle := crew("/work/crew")
		handle[contextStartedAt] = "not a time"
		id, err := askCrew(t, h, handle)
		if id != "" || err == nil || !strings.Contains(err.Error(), "invalid crew launch timestamp") {
			t.Fatalf("session = %q, %v; want an invalid timestamp error", id, err)
		}
	})
	t.Run("crew without a directory pins nothing", func(t *testing.T) {
		h := &Herdr{AgentHomes: map[string]string{"agy": home(t, `{"/work/crew":"own"}`, map[string]string{"own": own})}}
		handle := crew("")
		delete(handle, "dir")
		if id, err := askCrew(t, h, handle); id != "" || err != nil {
			t.Fatalf("session = %q, %v; want \"\", nil", id, err)
		}
	})
	t.Run("first line that is no prompt pins nothing", func(t *testing.T) {
		generic := first(launch.Add(time.Second), "done")
		generic = strings.Replace(generic, "USER_INPUT", "GENERIC", 1)
		h := &Herdr{AgentHomes: map[string]string{"agy": home(t, `{"/work/crew":"own"}`, map[string]string{"own": generic})}}
		if id, err := askCrew(t, h, crew("/work/crew")); id != "" || err != nil {
			t.Fatalf("session = %q, %v; want \"\", nil", id, err)
		}
	})
}

// Antigravity's first user message wraps the prompt the pane was sent, with
// its metadata after the wrapper; anything else is another session's file.
func TestAgyFirstPrompt(t *testing.T) {
	t.Parallel()
	wrap := func(content string) string {
		return `{"type":"USER_INPUT","content":"<USER_REQUEST>\n` + content + `\n</USER_REQUEST>"}` + "\n"
	}
	for _, tc := range []struct {
		name string
		line string
		want bool
	}{
		{"wrapped", wrap("go"), true},
		{"metadata after", `{"type":"USER_INPUT","content":"<USER_REQUEST>\ngo\n</USER_REQUEST>\n<metadata/>"}` + "\n", true},
		{"another prompt", wrap("stay"), false},
		{"unwrapped", `{"type":"USER_INPUT","content":"go"}` + "\n", false},
		{"unclosed", `{"type":"USER_INPUT","content":"<USER_REQUEST>\ngo"}` + "\n", false},
		{"another line", `{"type":"GENERIC","content":"<USER_REQUEST>\ngo\n</USER_REQUEST>"}` + "\n", false},
		{"no JSON", "not json\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := agyFirstPrompt([]byte(tc.line), "go"); got != tc.want {
				t.Errorf("agyFirstPrompt = %v, want %v", got, tc.want)
			}
		})
	}
}
