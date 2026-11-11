package runner

import (
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"yardr.dev/yardr/internal/peer"
	"yardr.dev/yardr/internal/scripttest"
	"yardr.dev/yardr/internal/store"
)

// Context reads the last use the agent's session file holds, found by the
// session id herdr reports. The lines are trimmed from a real transcript
// of Claude Code 2.1.
func TestHerdrContextReadsTheLastUse(t *testing.T) {
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
	t.Run("older crew ps deadline", func(t *testing.T) {
		// This test changes PATH for the stand-in ps, so the enclosing test is
		// serial rather than parallel.
		shell, err := peer.Describe(os.Getpid())
		if err != nil {
			t.Fatal(err)
		}
		// This process is the crew shell: alive, with the start the handle
		// names, so the call gets past its guards to ps.
		handle := map[string]any{"shell_pid": float64(os.Getpid()), "shell_start": float64(shell.Start)}
		shellAt := time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC)
		startedAt := shellAt.Add(20 * time.Second)
		// exec, so the sleep is the process the deadline kills and no child is
		// left holding ps's output open.
		bin := t.TempDir()
		scripttest.Write(t, filepath.Join(bin, "ps"), fmt.Sprintf(`if [ -n "$FAKE_PS_HANG" ]; then exec sleep 30; fi
echo '%s'
`, shellAt.Format(time.ANSIC)))
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

		// An answering ps gives the 10 s cap, proving the call passes its
		// guards and reaches ps. Give this step room for a loaded machine.
		got, err := crewContextAllowance(context.Background(), handle, startedAt, time.Minute)
		if err != nil || got != 10*time.Second {
			t.Fatalf("allowance with a ps that answers = %s, %v; want 10s, nil", got, err)
		}

		// The shorter wait cuts off the stand-in without making this test wait
		// for its 30 s sleep, whether or not the command has started yet.
		t.Setenv("FAKE_PS_HANG", "1")
		start := time.Now()
		got, err = crewContextAllowance(context.Background(), handle, startedAt, 500*time.Millisecond)
		if took := time.Since(start); took > 10*time.Second {
			t.Fatalf("allowance returned after %s; the deadline did not hold", took)
		}
		if err != nil || got != 0 {
			t.Fatalf("allowance with a ps that never answers = %s, %v; want 0, nil", got, err)
		}

		// A caller's cancellation and deadline are still errors; only the
		// timeout set for ps itself is forgiven.
		cancelled, cancelNow := context.WithCancel(context.Background())
		cancelNow()
		if got, err := crewContextAllowance(cancelled, handle, startedAt, time.Minute); err == nil {
			t.Fatalf("allowance after the caller cancelled = %s, %v; want an error", got, err)
		}
		callerDeadline, cancelDeadline := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancelDeadline()
		if got, err := crewContextAllowance(callerDeadline, handle, startedAt, time.Minute); err == nil {
			t.Fatalf("allowance after the caller deadline = %s, %v; want an error", got, err)
		}

		// The production wait is checked here; the injected waits above keep
		// the test from waiting for it.
		if crewShellStartTimeout > 10*time.Second {
			t.Fatalf("crewShellStartTimeout = %s; a stuck ps holds the crew pass that long", crewShellStartTimeout)
		}
	})
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
		bin := fakeHerdrBin(t, `echo '{"id":"x","result":{"type":"agent_info","agent":{"agent":"codex","agent_session":{"kind":"id","value":"c1"},"agent_status":"idle"}}}'`)
		handle := map[string]any{"agent": "codex"}
		// Each check reads with a fresh Herdr: Context remembers its
		// transcripts across calls, and these writes replace the file
		// rather than grow it, as a transcript never does.
		check := func(wantUsed, wantWindow int, wantOK bool) {
			t.Helper()
			h := &Herdr{AgentHomes: map[string]string{"codex": codex}, Bin: bin}
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
		h := &Herdr{AgentHomes: map[string]string{"codex": codex}, Bin: bin}
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
		bin := fakeHerdrBin(t, `if [ "$3" = muse-id ]; then
  echo '{"id":"x","result":{"type":"agent_info","agent":{"agent":"muse","agent_session":{"kind":"id","value":"m1"},"agent_status":"idle"}}}'
else
  echo '{"id":"x","result":{"type":"agent_info","agent":{"agent":"muse","agent_status":"idle"}}}'
fi`)
		handle := map[string]any{"agent": "muse", "kind": "muse", "dir": "/work/crew"}
		// Each check reads with a fresh Herdr: Context remembers its
		// transcripts across calls, and these writes replace the file
		// rather than grow it, as a transcript never does.
		check := func(want int, has bool) {
			t.Helper()
			h := &Herdr{AgentHomes: map[string]string{"muse": muse}, Bin: bin}
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
				h := &Herdr{AgentHomes: map[string]string{"muse": muse}, Bin: bin}
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
		// Each check reads with a fresh Herdr: Context pins the transcript
		// it matched while the file stands, and the second check adds a
		// file that makes a fresh match ambiguous.
		check := func(want int, has bool) {
			t.Helper()
			fresh := &Herdr{AgentHomes: h.AgentHomes, Bin: h.Bin}
			used, window, ok, err := fresh.Context(ctx, live.Handle, *live.StartedAt)
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

	t.Run("unchanged transcript is not reopened", func(t *testing.T) {
		home := t.TempDir()
		path := filepath.Join(home, "projects", "-tmp-w", "c1.jsonl")
		first := "{\"type\":\"assistant\",\"message\":{\"model\":\"m\",\"usage\":{\"input_tokens\":10,\"cache_read_input_tokens\":100,\"output_tokens\":7}}}\n"
		write(path, first)
		h := &Herdr{AgentHomes: map[string]string{"claude": home}, Bin: fakeHerdrBin(t, `echo '{"id":"x","result":{"type":"agent_info","agent":{"agent":"claude","agent_session":{"kind":"id","value":"c1"},"agent_status":"idle"}}}'`)}
		handle := map[string]any{"agent": "crew"}
		check := func(want int) {
			t.Helper()
			used, window, ok, err := h.Context(ctx, handle, time.Time{})
			if err != nil || !ok || used != want || window != 0 {
				t.Fatalf("context = %d, %d, %v, %v; want %d, 0, true, nil", used, window, ok, err, want)
			}
		}
		check(110)
		// The same bytes at the same size and time are not read again:
		// different numbers under them still read as the old ones.
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		swapped := strings.Replace(first, `"input_tokens":10`, `"input_tokens":20`, 1)
		swapped = strings.Replace(swapped, `"cache_read_input_tokens":100`, `"cache_read_input_tokens":200`, 1)
		if len(swapped) != len(first) {
			t.Fatalf("swapped line is %d bytes, want %d", len(swapped), len(first))
		}
		write(path, swapped)
		if err := os.Chtimes(path, fi.ModTime(), fi.ModTime()); err != nil {
			t.Fatal(err)
		}
		check(110)
		// Unreadable but statable: the reading comes from no open file.
		if os.Geteuid() == 0 {
			t.Skip("root opens what chmod 000 closes")
		}
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		check(110)
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
		// Growth is still followed.
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		write(path, string(raw)+"{\"type\":\"assistant\",\"message\":{\"model\":\"m\",\"usage\":{\"input_tokens\":5,\"cache_read_input_tokens\":50}}}\n")
		check(55)
	})

	t.Run("grown transcript resumes at the remembered offset", func(t *testing.T) {
		home := t.TempDir()
		path := filepath.Join(home, "sessions", "2026", "11", "10", "rollout-2026-11-10T20-10-46-c1.jsonl")
		first := "{\"type\":\"turn_context\",\"payload\":{\"model\":\"gpt-6\"}}\n" +
			"{\"type\":\"event_msg\",\"payload\":{\"type\":\"token_count\",\"info\":{\"last_token_usage\":{\"input_tokens\":100},\"model_context_window\":1000}}}\n"
		write(path, first)
		h := &Herdr{AgentHomes: map[string]string{"codex": home}, Bin: fakeHerdrBin(t, `echo '{"id":"x","result":{"type":"agent_info","agent":{"agent":"codex","agent_session":{"kind":"id","value":"c1"},"agent_status":"idle"}}}'`)}
		handle := map[string]any{"agent": "crew"}
		used, window, ok, err := h.Context(ctx, handle, time.Time{})
		if err != nil || !ok || used != 100 || window != 1000 {
			t.Fatalf("context = %d, %d, %v, %v; want 100, 1000, true, nil", used, window, ok, err)
		}
		// The grown file's already-read prefix is never decoded again: it
		// holds a line that errors if read, padded to the byte the last
		// read ended at.
		bad := "{\"payload\":{\"type\":\"token_count\",\"info\":{\"last_token_usage\":{\"input_tokens\":1},\"model_context_window\":\"bogus\"}}}\n"
		if len(first) <= len(bad) {
			t.Fatalf("first read is %d bytes, want more than the %d-byte erroring line", len(first), len(bad))
		}
		filler := bad
		for rest := len(first) - len(bad); rest > 0; {
			n := min(rest, 61)
			filler += strings.Repeat(" ", n-1) + "\n"
			rest -= n
		}
		tail := "{\"type\":\"event_msg\",\"payload\":{\"type\":\"token_count\",\"info\":{\"last_token_usage\":{\"input_tokens\":777},\"model_context_window\":888}}}\n"
		write(path, filler+tail)
		used, window, ok, err = h.Context(ctx, handle, time.Time{})
		if err != nil || !ok || used != 777 || window != 888 {
			t.Fatalf("resumed context = %d, %d, %v, %v; want 777, 888, true, nil", used, window, ok, err)
		}
	})

	t.Run("half-written trailing line is read whole", func(t *testing.T) {
		home := t.TempDir()
		path := filepath.Join(home, "projects", "-tmp-w", "c1.jsonl")
		first := "{\"type\":\"assistant\",\"message\":{\"model\":\"m\",\"usage\":{\"input_tokens\":10}}}\n"
		half := "{\"type\":\"assistant\",\"message\":{\"model\":\"m\",\"usage\":{\"input_tokens\":50"
		write(path, first+half)
		h := &Herdr{AgentHomes: map[string]string{"claude": home}, Bin: fakeHerdrBin(t, `echo '{"id":"x","result":{"type":"agent_info","agent":{"agent":"claude","agent_session":{"kind":"id","value":"c1"},"agent_status":"idle"}}}'`)}
		handle := map[string]any{"agent": "crew"}
		used, _, ok, err := h.Context(ctx, handle, time.Time{})
		if err != nil || !ok || used != 10 {
			t.Fatalf("context = %d, %v, %v; want 10, true, nil", used, ok, err)
		}
		// The agent finishes the line and says nothing countable after it:
		// the resumed read takes the line whole, not from mid-line.
		rest := ",\"cache_read_input_tokens\":5}}}\n{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"go on\"}}\n"
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		write(path, string(raw)+rest)
		used, _, ok, err = h.Context(ctx, handle, time.Time{})
		if err != nil || !ok || used != 55 {
			t.Fatalf("resumed context = %d, %v, %v; want 55, true, nil", used, ok, err)
		}
	})

	t.Run("shrunk transcript reads from the start", func(t *testing.T) {
		home := t.TempDir()
		path := filepath.Join(home, "projects", "-tmp-w", "c1.jsonl")
		line := func(input, cache int) string {
			return fmt.Sprintf("{\"type\":\"assistant\",\"message\":{\"model\":\"m\",\"usage\":{\"input_tokens\":%d,\"cache_read_input_tokens\":%d}}}\n", input, cache)
		}
		write(path, line(10, 100)+line(20, 200))
		h := &Herdr{AgentHomes: map[string]string{"claude": home}, Bin: fakeHerdrBin(t, `echo '{"id":"x","result":{"type":"agent_info","agent":{"agent":"claude","agent_session":{"kind":"id","value":"c1"},"agent_status":"idle"}}}'`)}
		handle := map[string]any{"agent": "crew"}
		check := func(want int) {
			t.Helper()
			used, window, ok, err := h.Context(ctx, handle, time.Time{})
			if err != nil || !ok || used != want || window != 0 {
				t.Fatalf("context = %d, %d, %v, %v; want %d, 0, true, nil", used, window, ok, err, want)
			}
		}
		check(220)
		// A rotation or truncation rereads the whole file, not the tail.
		write(path, line(1, 32))
		check(33)
	})

	t.Run("match pins while the session dir stands", func(t *testing.T) {
		home := t.TempDir()
		startedAt := time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC)
		metadata := func(at time.Time) string {
			return fmt.Sprintf("{\"recorded_at\":%d,\"payload_type\":\"runtime.session.metadata\",\"payload\":{\"record\":{\"workspace_root\":\"/work/crew\"}}}\n", at.UnixMicro())
		}
		calls := func(input int) string {
			return fmt.Sprintf("{\"payload\":{\"event\":{\"kind\":\"model_completed\",\"usage\":{\"input_tokens\":%d}}}}\n", input)
		}
		m1 := filepath.Join(home, "sessions", "2026", "11", "10", "m1", "session.jsonl")
		m2 := filepath.Join(home, "sessions", "2026", "11", "10", "m2", "session.jsonl")
		m3 := filepath.Join(home, "sessions", "2026", "11", "10", "m3", "session.jsonl")
		h := &Herdr{AgentHomes: map[string]string{"muse": home}, Bin: fakeHerdrBin(t, `echo '{"id":"x","result":{"type":"agent_info","agent":{"agent":"muse","agent_status":"idle"}}}'`)}
		handle := map[string]any{"agent": "muse", "worktree": "/work/crew"}
		check := func(want int, has bool) {
			t.Helper()
			used, window, ok, err := h.Context(ctx, handle, startedAt)
			if err != nil || used != want || ok != has || (has && window != 1007997) || (!has && window != 0) {
				t.Fatalf("context = %d, %d, %v, %v; want %d, %v", used, window, ok, err, want, has)
			}
		}
		write(m1, metadata(startedAt.Add(time.Second))+calls(700000))
		check(700000, true)
		write(m2, metadata(startedAt.Add(2*time.Second))+calls(111))
		check(0, false) // a second candidate drops the pin
		if err := os.Remove(m1); err != nil {
			t.Fatal(err)
		}
		check(111, true) // the one file left is matched and pinned
		write(m1, metadata(startedAt.Add(time.Second))+calls(700000))
		check(0, false) // ambiguous again
		if err := os.Remove(m2); err != nil {
			t.Fatal(err)
		}
		check(700000, true)
		// A file that does not qualify leaves the reading: the dir changed,
		// so the match runs again and finds m1 alone, whose last reading
		// still skips its read.
		write(m3, metadata(startedAt.Add(-24*time.Hour))+calls(222))
		check(700000, true)
		// An existing transcript can become a candidate when its metadata
		// is completed. Its path set is unchanged, but the pin must re-match.
		m3Info, err := os.Stat(m3)
		if err != nil {
			t.Fatal(err)
		}
		write(m3, metadata(startedAt.Add(3*time.Second))+calls(222))
		changedAt := m3Info.ModTime().Add(time.Second)
		if err := os.Chtimes(m3, changedAt, changedAt); err != nil {
			t.Fatal(err)
		}
		check(0, false)
		write(m3, metadata(startedAt.Add(-24*time.Hour))+calls(222))
		changedAt = changedAt.Add(time.Second)
		if err := os.Chtimes(m3, changedAt, changedAt); err != nil {
			t.Fatal(err)
		}
		check(700000, true)
		// Ambiguity itself never pins: a fresh reader sees none, then the
		// one file left.
		write(m2, metadata(startedAt.Add(2*time.Second))+calls(111))
		fresh := &Herdr{AgentHomes: map[string]string{"muse": home}, Bin: h.Bin}
		used, window, ok, err := fresh.Context(ctx, handle, startedAt)
		if err != nil || ok || used != 0 || window != 0 {
			t.Fatalf("ambiguous context = %d, %d, %v, %v; want 0, 0, false, nil", used, window, ok, err)
		}
		if err := os.Remove(m1); err != nil {
			t.Fatal(err)
		}
		used, window, ok, err = fresh.Context(ctx, handle, startedAt)
		if err != nil || !ok || used != 111 || window != 1007997 {
			t.Fatalf("resolved context = %d, %d, %v, %v; want 111, 1007997, true, nil", used, window, ok, err)
		}
	})

	t.Run("concurrent sessions read side by side", func(t *testing.T) {
		home := t.TempDir()
		startedAt := time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC)
		file := func(id, worktree string, at time.Time, input int) {
			t.Helper()
			lines := fmt.Sprintf("{\"recorded_at\":%d,\"payload_type\":\"runtime.session.metadata\",\"payload\":{\"record\":{\"workspace_root\":%q}}}\n", at.UnixMicro(), worktree) +
				fmt.Sprintf("{\"payload\":{\"event\":{\"kind\":\"model_completed\",\"usage\":{\"input_tokens\":%d}}}}\n", input)
			write(filepath.Join(home, "sessions", "2026", "11", "10", id, "session.jsonl"), lines)
		}
		file("m1", "/work/a", startedAt.Add(time.Second), 700000)
		file("m2", "/work/b", startedAt.Add(2*time.Second), 111)
		h := &Herdr{AgentHomes: map[string]string{"muse": home}, Bin: fakeHerdrBin(t, `echo '{"id":"x","result":{"type":"agent_info","agent":{"agent":"muse","agent_status":"idle"}}}'`)}
		// The daemon keeps crew sessions side by side: concurrent reads
		// share the cache, and each session keeps its own reading.
		var wg sync.WaitGroup
		for w := range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range 5 {
					worktree, want := "/work/a", 700000
					if (w+i)%2 == 1 {
						worktree, want = "/work/b", 111
					}
					used, window, ok, err := h.Context(ctx, map[string]any{"agent": "muse", "worktree": worktree}, startedAt)
					if err != nil || !ok || used != want || window != 1007997 {
						t.Errorf("context = %d, %d, %v, %v; want %d, 1007997, true, nil", used, window, ok, err, want)
						return
					}
				}
			}()
		}
		wg.Wait()
	})
}

// BenchmarkHerdrContextCached is the daemon's per-pass cost of one crew
// session's context reading on a crew-sized transcript: a session dir with
// fifty other sessions' files and a 20 MB transcript of its own. The first
// call matches and reads it; every timed call is a later pass.
func BenchmarkHerdrContextCached(b *testing.B) {
	ctx := context.Background()
	home := b.TempDir()
	startedAt := time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC)
	dir := filepath.Join(home, "sessions", "2026", "11", "10")
	for i := range 50 {
		p := filepath.Join(dir, fmt.Sprintf("old-%d", i), "session.jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			b.Fatal(err)
		}
		old := fmt.Sprintf("{\"recorded_at\":%d,\"payload_type\":\"runtime.session.metadata\",\"payload\":{\"record\":{\"workspace_root\":\"/work/elsewhere\"}}}\n", startedAt.Add(-48*time.Hour).UnixMicro())
		if err := os.WriteFile(p, []byte(old), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	meta := fmt.Sprintf("{\"recorded_at\":%d,\"payload_type\":\"runtime.session.metadata\",\"payload\":{\"record\":{\"workspace_root\":\"/work/crew\"}}}\n", startedAt.Add(time.Second).UnixMicro())
	call := "{\"payload\":{\"event\":{\"kind\":\"model_completed\",\"usage\":{\"input_tokens\":700000,\"cached_tokens\":680000,\"output_tokens\":200}}}}\n"
	var sb strings.Builder
	sb.WriteString(meta)
	for sb.Len() < 20<<20 {
		sb.WriteString(call)
	}
	path := filepath.Join(dir, "m1", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o600); err != nil {
		b.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Size() < 20<<20 {
		b.Fatalf("transcript stat = %v, %v; want at least 20 MB", fi, err)
	}
	h := &Herdr{AgentHomes: map[string]string{"muse": home}, Bin: fakeHerdrBin(b, `echo '{"id":"x","result":{"type":"agent_info","agent":{"agent":"muse","agent_status":"idle"}}}'`)}
	handle := map[string]any{"agent": "muse", "worktree": "/work/crew"}
	if used, _, ok, err := h.Context(ctx, handle, startedAt); err != nil || !ok || used != 700000 {
		b.Fatalf("warm context = %d, %v, %v; want 700000, true, nil", used, ok, err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		if _, _, ok, err := h.Context(ctx, handle, startedAt); err != nil || !ok {
			b.Fatalf("context = %v, %v; want true, nil", ok, err)
		}
	}
}
