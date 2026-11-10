package runner

import (
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/peer"
	"yardr.dev/yardr/internal/store"
)

// Context reads the last use the agent's session file holds, found by the
// session id herdr reports. The lines are trimmed from a real transcript
// of Claude Code 2.1.
func TestHerdrContextReadsTheLastUse(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	claude := t.TempDir()
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(claude, "projects", "-tmp-w", "c1.jsonl"), `{"type":"user","message":{"role":"user","content":"Read the brief."}}
{"type":"assistant","message":{"model":"claude-opus-5-5","usage":{"input_tokens":10,"cache_read_input_tokens":100,"cache_creation_input_tokens":5,"output_tokens":7}}}
{"type":"assistant","message":{"model":"<synthetic>","content":[{"type":"text","text":"API Error"}]}}
{"type":"assistant","message":{"model":"claude-opus-5-5","usage":{"input_tokens":2,"cache_read_input_tokens":200,"output_tokens":3}}}
`)
	write(filepath.Join(claude, "projects", "-tmp-w", "c2.jsonl"), `{"type":"user","message":{"role":"user","content":"Read the brief."}}
`)
	h := &Herdr{AgentHomes: map[string]string{"claude": claude}, Bin: fakeHerdrBin(t, `agent() { echo '{"id":"x","result":{"type":"agent_info","agent":{"agent":"'"$1"'","agent_session":{"kind":"id","value":"'"$2"'"},"agent_status":"idle"}}}'; }
case "$3" in
  used)     agent claude c1 ;;
  silent)   agent claude c2 ;;
  nofile)   agent claude c3 ;;
  glob)     agent claude '*' ;;
  kimi)     agent kimi session_1 ;;
  starting) echo '{"id":"x","result":{"type":"agent_info","agent":{"agent":"claude","agent_status":"idle"}}}' ;;
  gone)     echo '{"error":{"code":"agent_not_found","message":"agent target gone not found"},"id":"x"}' >&2; exit 1 ;;
  *)        echo '{"error":{"code":"server_not_running","message":"no herdr server"},"id":"x"}' >&2; exit 1 ;;
esac`)}
	for agent, want := range map[string]struct {
		used int
		ok   bool
	}{
		"used":     {202, true}, // the last line's input and cache, not what it said
		"silent":   {},          // prompted, no reply yet
		"nofile":   {},          // no session file yet
		"glob":     {},          // an id that is no id finds nothing
		"kimi":     {},          // a kind with no source
		"starting": {},          // herdr has no session id for it yet
		"gone":     {},
	} {
		used, window, ok, err := h.Context(ctx, map[string]any{"agent": agent}, time.Time{})
		if err != nil || used != want.used || ok != want.ok || window != 0 {
			t.Errorf("%s: context = %d, %v, %v; want %d, %v", agent, used, ok, err, want.used, want.ok)
		}
	}
	if _, _, _, err := h.Context(ctx, map[string]any{"agent": "down"}, time.Time{}); err == nil {
		t.Error("herdr down must be an error, not a reading")
	}

	t.Run("Codex per-call input", func(t *testing.T) {
		codex := t.TempDir()
		path := filepath.Join(codex, "sessions", "2026", "11", "10", "rollout-example-c1.jsonl")
		h := &Herdr{AgentHomes: map[string]string{"codex": codex}, Bin: fakeHerdrBin(t, `echo '{"id":"x","result":{"type":"agent_info","agent":{"agent":"codex","agent_session":{"kind":"id","value":"c1"},"agent_status":"idle"}}}'`)}
		handle := map[string]any{"agent": "codex"}
		check := func(wantUsed, wantWindow int, wantOK bool) {
			t.Helper()
			used, window, ok, err := h.Context(ctx, handle, time.Time{})
			if err != nil || ok != wantOK || used != wantUsed || window != wantWindow {
				t.Fatalf("Codex context = %d, %d, %v, %v; want %d, %d, %v, nil", used, window, ok, err, wantUsed, wantWindow, wantOK)
			}
		}
		write(path, `{"type":"turn_context","payload":{"model":"gpt-6-luna"}}`+"\n")
		check(0, 0, false)
		calls := `{"type":"turn_context","payload":{"model":"gpt-6-luna"}}
{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":180000,"cached_input_tokens":170000,"output_tokens":300},"total_token_usage":{"input_tokens":995515},"model_context_window":258400}}}
{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":89874,"cached_input_tokens":89472,"output_tokens":200},"total_token_usage":{"input_tokens":1085389},"model_context_window":258400}}}
{"type":"event_msg","payload":{"type":"token_count","info":null}}
{"payload":{"type":"token_usage_record","info":{"last_token_usage":{"input_tokens":999999},"model_context_window":1}}}
`
		write(path, calls)
		check(89874, 258400, true)
		// A complete call without its window invalidates the previous percentage.
		write(path, calls+`{"payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":12000}}}}`+"\n")
		check(0, 0, false)
		write(path, calls+`{"payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":12000},"model_context_window":null}}}`+"\n")
		check(0, 0, false)
		// A later call carries its own window, including after a model change.
		write(path, calls+`{"payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":285000,"cached_input_tokens":280000},"model_context_window":475000}}}`+"\n")
		check(285000, 475000, true)
		write(path, calls+`{"payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":12000},"model_context_window":"private text"}}}`+"\n")
		used, window, ok, err := h.Context(ctx, handle, time.Time{})
		if err == nil || ok || used != 0 || window != 0 || !strings.Contains(err.Error(), "model_context_window") || strings.Contains(err.Error(), "private text") {
			t.Fatalf("invalid Codex window = %d, %d, %v, %v", used, window, ok, err)
		}
	})

	t.Run("Muse worktree match", func(t *testing.T) {
		muse := t.TempDir()
		shell, err := peer.Describe(os.Getpid())
		if err != nil {
			t.Fatal(err)
		}
		ps := exec.Command("ps", "-o", "lstart=", "-p", fmt.Sprint(os.Getpid()))
		ps.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC0")
		raw, err := ps.Output()
		if err != nil {
			t.Fatal(err)
		}
		shellAt, err := time.Parse(time.ANSIC, strings.TrimSpace(string(raw)))
		if err != nil {
			t.Fatal(err)
		}
		startedAt := shellAt.Add(20 * time.Second)
		metadataAt := func(offset time.Duration) string {
			return fmt.Sprintf("{\"recorded_at\":%d,\"payload_type\":\"runtime.session.metadata\",\"payload\":{\"record\":{\"workspace_root\":\"/work/crew\"}}}\n", startedAt.Add(offset).UnixMicro())
		}
		metadata := metadataAt(time.Second)
		path := filepath.Join(muse, "sessions", "2026", "11", "10", "m1", "session.jsonl")
		write(path, metadata)
		h := &Herdr{AgentHomes: map[string]string{"muse": muse}, Bin: fakeHerdrBin(t, `if [ "$3" = muse-id ]; then
  echo '{"id":"x","result":{"type":"agent_info","agent":{"agent":"muse","agent_session":{"kind":"id","value":"m1"},"agent_status":"idle"}}}'
else
  echo '{"id":"x","result":{"type":"agent_info","agent":{"agent":"muse","agent_status":"idle"}}}'
fi`)}
		handle := map[string]any{"agent": "muse", "kind": "muse", "dir": "/work/crew"}
		check := func(want int, has bool) {
			t.Helper()
			used, window, ok, err := h.Context(ctx, handle, startedAt)
			if err != nil || used != want || ok != has || (has && window != 1007997) || (!has && window != 0) {
				t.Fatalf("Muse context = %d, %v, %v; want %d, %v", used, ok, err, want, has)
			}
		}
		check(0, false) // No completed call yet is no reading.
		calls := `{"payload":{"event":{"kind":"model_completed","usage":{"input_tokens":650000,"cached_tokens":600000,"output_tokens":300}}}}
{"payload":{"event":{"kind":"model_completed","usage":{"input_tokens":700000,"cached_tokens":680000,"output_tokens":200}}}}
{"payload":{"event":{"kind":"goal_usage_attribution","usage":{"input_tokens":900000}}}}
`
		write(path, metadata+calls)
		check(700000, true)
		for _, tc := range []struct {
			name        string
			start, file time.Duration
			shellStart  any
			used        int
		}{
			{name: "missing launch boundary", start: 20 * time.Second, file: 15 * time.Second},
			{name: "reused shell PID", start: 20 * time.Second, file: 15 * time.Second, shellStart: float64(shell.Start + 1)},
			{name: "previous crew session", start: 8 * time.Second, file: -time.Microsecond, shellStart: float64(shell.Start)},
			{name: "shell precision", start: 8 * time.Second, file: 2*time.Second - time.Microsecond, shellStart: float64(shell.Start)},
			{name: "shell lower edge", start: 8 * time.Second, file: 2 * time.Second, shellStart: float64(shell.Start), used: 700000},
			{name: "pre-record file", start: 8 * time.Second, file: 3850 * time.Millisecond, shellStart: float64(shell.Start), used: 700000},
		} {
			t.Run(tc.name, func(t *testing.T) {
				match := maps.Clone(handle)
				if tc.shellStart != nil {
					match["shell_pid"], match["shell_start"] = float64(os.Getpid()), tc.shellStart
				}
				write(path, metadataAt(shellAt.Add(tc.file).Sub(startedAt))+calls)
				used, window, ok, err := h.Context(ctx, match, shellAt.Add(tc.start))
				if err != nil || used != tc.used || ok != (tc.used > 0) || (ok && window != 1007997) || (!ok && window != 0) {
					t.Fatalf("legacy Muse context = %d, %d, %v, %v; want %d tokens", used, window, ok, err, tc.used)
				}
			})
		}
		handle["shell_pid"] = float64(os.Getpid())
		handle["shell_start"] = float64(shell.Start)
		// Older crew handles have only the store's time, recorded after launch.
		for _, offset := range []time.Duration{-4150 * time.Millisecond, -10 * time.Second} {
			metadata = metadataAt(offset)
			write(path, metadata+calls)
			check(700000, true)
		}
		write(filepath.Join(muse, "sessions", "2026", "11", "09", "old", "session.jsonl"), metadataAt(-24*time.Hour)+calls)
		check(700000, true) // Yesterday's crew session does not make this ambiguous.
		for _, offset := range []time.Duration{-24 * time.Hour, -10*time.Second - time.Microsecond, 15*time.Minute + time.Microsecond} {
			write(path, metadataAt(offset)+calls)
			check(0, false)
		}
		write(path, metadata+calls)
		handle["context_started_at"] = startedAt.Format(time.RFC3339Nano)
		check(0, false) // A known launch time excludes even a recent earlier run.
		delete(handle, "context_started_at")
		handle["worktree"] = handle["dir"]
		check(0, false) // A bead's worktree retains its exact start boundary.
		delete(handle, "worktree")
		write(path, metadataAt(15*time.Minute)+calls)
		check(700000, true) // The post-start window retains its inclusive end.
		metadata = metadataAt(time.Second)
		// A compaction's next call reads low without special handling.
		write(path, metadata+calls+`{"payload":{"event":{"kind":"model_completed","usage":{"input_tokens":12000}}}}`+"\n")
		check(12000, true)
		if _, has := handle["worktree"]; has {
			t.Fatal("context matching changed the crew handle")
		}
		handle["worktree"] = "/work/another"
		check(0, false) // An explicit worktree takes precedence over dir.
		handle["worktree"] = handle["dir"]
		check(12000, true)
		delete(handle, "worktree")
		write(filepath.Join(muse, "sessions", "2026", "11", "10", "m2", "session.jsonl"), metadataAt(-4150*time.Millisecond)+calls)
		check(0, false) // Two qualifying files never choose one arbitrarily.
		handle["agent"] = "muse-id"
		handle["context_started_at"] = "invalid"
		check(12000, true) // A reported id takes precedence over the match.
	})

	t.Run("Muse crew startup", func(t *testing.T) {
		dir, muse := t.TempDir(), t.TempDir()
		h, st := shellHerdr(t, dir)
		h.YardHome = t.TempDir()
		h.AgentHomes = map[string]string{"muse": muse}
		h.Bin = fakeHerdrBin(t, `case "$1 $2" in
  "agent get") echo '{"result":{"agent":{"agent":"muse","agent_status":"idle"}}}' ;;
  "pane process-info") echo '{"result":{"process_info":{"pane_id":"p1","shell_pid":`+fmt.Sprint(os.Getpid())+`}}}' ;;
  *) echo '{"result":{"type":"ok"}}' ;;
esac`)
		def, err := st.PutCrew(ctx, store.CrewMember{
			Name: "yardmaster", Runner: "herdr", Dir: dir,
			Config: map[string]any{"kind": "muse", "model": "muse-wonder-1.0", "trust": false},
		}, "me")
		if err != nil {
			t.Fatal(err)
		}
		beforeLaunch := time.Now()
		handle, err := h.StartCrew(ctx, *def, "")
		if err != nil {
			t.Fatal(err)
		}
		// Muse can write its first call before the dispatcher records the start.
		metadataAt := time.Now().UTC().Truncate(time.Microsecond)
		metadata := fmt.Sprintf("{\"recorded_at\":%d,\"payload_type\":\"runtime.session.metadata\",\"payload\":{\"record\":{\"workspace_root\":%q}}}\n", metadataAt.UnixMicro(), dir)
		calls := "{\"payload\":{\"event\":{\"kind\":\"model_completed\",\"usage\":{\"input_tokens\":700000,\"cached_tokens\":680000}}}}\n"
		path := filepath.Join(muse, "sessions", "2026", "11", "10", "m1", "session.jsonl")
		write(path, metadata+calls)
		// An earlier run in the same directory must stay outside the match.
		old := fmt.Sprintf("{\"recorded_at\":%d,\"payload_type\":\"runtime.session.metadata\",\"payload\":{\"record\":{\"workspace_root\":%q}}}\n", beforeLaunch.Add(-time.Second).UnixMicro(), dir)
		write(filepath.Join(muse, "sessions", "2026", "11", "10", "old", "session.jsonl"), old+calls)
		if err := st.CrewStartedFor(ctx, def.Name, "", handle); err != nil {
			t.Fatal(err)
		}
		live, err := st.GetCrew(ctx, def.Name)
		if err != nil || live.StartedAt == nil {
			t.Fatalf("started crew = %+v, %v", live, err)
		}
		if !metadataAt.Before(*live.StartedAt) {
			t.Fatalf("metadata at %s must precede persisted start %s", metadataAt, live.StartedAt)
		}
		check := func(want int, has bool) {
			t.Helper()
			used, window, ok, err := h.Context(ctx, live.Handle, *live.StartedAt)
			if err != nil || used != want || ok != has || (has && window != 1007997) || (!has && window != 0) {
				t.Fatalf("started Muse crew context = %d, %v, %v; want %d, %v", used, ok, err, want, has)
			}
		}
		check(700000, true)
		write(filepath.Join(muse, "sessions", "2026", "11", "10", "m2", "session.jsonl"), metadata+calls)
		check(0, false)
		for _, at := range []any{"invalid", "", nil, 123} {
			live.Handle["context_started_at"] = at
			if used, window, ok, err := h.Context(ctx, live.Handle, *live.StartedAt); err == nil || ok || used != 0 || window != 0 {
				t.Fatalf("invalid context start %v: context = %d, %v, %v", at, used, ok, err)
			}
		}
	})
}
