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
	"sync/atomic"
	"testing"
	"time"

	"yardr.dev/yardr/internal/peer"
	"yardr.dev/yardr/internal/scripttest"
	"yardr.dev/yardr/internal/store"
)

// dirMoves numbers the times moveDir gives, so no two are alike.
var dirMoves atomic.Int64

// moveDir gives dir a modification time it never had. A pin tells a
// directory that gained or lost an entry by its time, and a filesystem that
// stamps by a coarse clock gives two changes inside one tick the same time:
// a test whose change must be seen sets the time by hand after it, as the
// candidate rewrites below do for a file.
func moveDir(t *testing.T, dir string) {
	t.Helper()
	at := time.Unix(1_000_000_000+dirMoves.Add(1), 0)
	if err := os.Chtimes(dir, at, at); err != nil {
		t.Fatal(err)
	}
}

// appendLine appends text to the transcript at path and gives it a
// modification time it never had, as moveDir does for a directory: another
// session writing to its own transcript between two passes.
func appendLine(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	at := fi.ModTime().Add(time.Second)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

// closeDirs makes each of dirs searchable but not readable, so a stat of a
// path under it works and a listing of it finds nothing, and restores them
// when the test ends. A caller skips as root first: root lists what mode 111
// closes.
func closeDirs(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.Chmod(d, 0o111); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, d := range dirs {
			_ = os.Chmod(d, 0o700)
		}
	})
}

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
		// A session herdr names an id for is found once and then known by its
		// file: with its directories searchable but not readable, a glob for
		// the id would find nothing, and the reading still comes.
		projects, project := filepath.Join(home, "projects"), filepath.Dir(path)
		closeDirs(t, projects, project)
		check(55)
		if err := os.Chmod(projects, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(project, 0o700); err != nil {
			t.Fatal(err)
		}
		// A file that has moved is found again by the glob, read in full.
		moved := filepath.Join(projects, "-tmp-x", "c1.jsonl")
		if err := os.MkdirAll(filepath.Dir(moved), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(path, moved); err != nil {
			t.Fatal(err)
		}
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
		moveDir(t, filepath.Dir(filepath.Dir(m2)))
		check(0, false) // a second candidate drops the pin
		if err := os.Remove(m1); err != nil {
			t.Fatal(err)
		}
		check(111, true) // the one file left is matched and pinned
		write(m1, metadata(startedAt.Add(time.Second))+calls(700000))
		moveDir(t, filepath.Dir(m1)) // m1's directory stood empty at the pin
		check(0, false)              // ambiguous again
		if err := os.Remove(m2); err != nil {
			t.Fatal(err)
		}
		check(700000, true)
		// A file that does not qualify leaves the reading: the dir changed,
		// so the match runs again and finds m1 alone, whose last reading
		// still skips its read.
		write(m3, metadata(startedAt.Add(-24*time.Hour))+calls(222))
		moveDir(t, filepath.Dir(filepath.Dir(m3)))
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
		// The pin now rests on m2 and on every directory a candidate stands
		// in or could appear through, empty ones too. m1's directory has
		// stood empty since its file went: a qualifying file written into
		// it moves nothing above it, only its own time.
		again := func(want int, has bool, what string) {
			t.Helper()
			used, window, ok, err := fresh.Context(ctx, handle, startedAt)
			if err != nil || used != want || ok != has || (has && window != 1007997) || (!has && window != 0) {
				t.Fatalf("%s: context = %d, %d, %v, %v; want %d, %v", what, used, window, ok, err, want, has)
			}
		}
		write(m1, metadata(startedAt.Add(time.Second))+calls(700000))
		moveDir(t, filepath.Dir(m1))
		again(0, false, "a file in an empty session directory")
		if err := os.Remove(m1); err != nil {
			t.Fatal(err)
		}
		again(111, true, "the empty session directory again")
		// A day directory made since the pin is caught by the month it
		// stands in; one that stood empty at the pin, by its own time.
		day11 := filepath.Join(home, "sessions", "2026", "11", "11")
		write(filepath.Join(day11, "m4", "session.jsonl"), metadata(startedAt.Add(3*time.Second))+calls(222))
		moveDir(t, filepath.Dir(day11))
		again(0, false, "a candidate in a new day directory")
		if err := os.RemoveAll(day11); err != nil {
			t.Fatal(err)
		}
		again(111, true, "the new day directory gone")
		day12 := filepath.Join(home, "sessions", "2026", "11", "12")
		if err := os.Mkdir(day12, 0o700); err != nil {
			t.Fatal(err)
		}
		moveDir(t, filepath.Dir(day12))
		again(111, true, "an empty day directory made") // the pin is taken again with it in
		write(filepath.Join(day12, "m5", "session.jsonl"), metadata(startedAt.Add(4*time.Second))+calls(333))
		moveDir(t, day12)
		again(0, false, "a candidate in an empty day directory")
		if err := os.RemoveAll(day12); err != nil {
			t.Fatal(err)
		}
		again(111, true, "the empty day directory gone")
		// The pinned file going is no candidate going: nothing is left.
		if err := os.Remove(m2); err != nil {
			t.Fatal(err)
		}
		again(0, false, "the pinned file gone")
	})

	t.Run("pinned match is checked with no listing", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root lists what chmod 111 closes")
		}
		home := t.TempDir()
		startedAt := time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC)
		metadata := func(at time.Time) string {
			return fmt.Sprintf("{\"recorded_at\":%d,\"payload_type\":\"runtime.session.metadata\",\"payload\":{\"record\":{\"workspace_root\":\"/work/crew\"}}}\n", at.UnixMicro())
		}
		calls := func(input int) string {
			return fmt.Sprintf("{\"payload\":{\"event\":{\"kind\":\"model_completed\",\"usage\":{\"input_tokens\":%d}}}}\n", input)
		}
		sessions := filepath.Join(home, "sessions")
		day := filepath.Join(sessions, "2026", "11", "10")
		m1, m0 := filepath.Join(day, "m1", "session.jsonl"), filepath.Join(day, "m0", "session.jsonl")
		write(m1, metadata(startedAt.Add(time.Second))+calls(700000))
		write(m0, metadata(startedAt.Add(-24*time.Hour))+calls(1)) // another session's, not a match
		// Muse's own index tree, which it rewrites while a session runs.
		view := filepath.Join(sessions, ".msp-view-g11", "m1")
		write(filepath.Join(view, "HEAD.json"), "{}\n")
		h := &Herdr{AgentHomes: map[string]string{"muse": home}, Bin: fakeHerdrBin(t, `echo '{"id":"x","result":{"type":"agent_info","agent":{"agent":"muse","agent_status":"idle"}}}'`)}
		handle := map[string]any{"agent": "muse", "worktree": "/work/crew"}
		check := func(want int, what string) {
			t.Helper()
			used, window, ok, err := h.Context(ctx, handle, startedAt)
			if err != nil || !ok || used != want || window != 1007997 {
				t.Fatalf("%s: context = %d, %d, %v, %v; want %d, 1007997, true, nil", what, used, window, ok, err, want)
			}
		}
		check(700000, "the match") // and its pin
		// Every directory a candidate stands in is searchable but not
		// readable from here on: a stat of a path under it still works, and
		// a listing finds no candidate at all. A check that listed would
		// see the pinned file's directory empty, drop the pin and match
		// nothing; the reading below comes from stats alone.
		closeDirs(t, sessions, filepath.Join(sessions, "2026"), filepath.Join(sessions, "2026", "11"), day, filepath.Dir(m1), filepath.Dir(m0))
		check(700000, "the pin checked with no listing")
		// The pinned transcript still grows, and a rotation or truncation of
		// it still reads from the start, under the pin.
		write(m1, metadata(startedAt.Add(time.Second))+calls(700000)+calls(800000))
		check(800000, "a grown pinned transcript")
		write(m1, metadata(startedAt.Add(time.Second))+calls(5))
		check(5, "a shrunk pinned transcript")
		// The hidden tree is no part of what the pin rests on, whatever it
		// does: its directory changing is no candidate appearing.
		write(filepath.Join(view, "snapshot.json"), "{}\n")
		moveDir(t, view)
		check(5, "a hidden directory changed")
	})

	t.Run("pin holds while another transcript grows", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root lists what chmod 111 closes")
		}
		home := t.TempDir()
		startedAt := time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC)
		metadata := func(worktree string, at time.Time) string {
			return fmt.Sprintf("{\"recorded_at\":%d,\"payload_type\":\"runtime.session.metadata\",\"payload\":{\"record\":{\"workspace_root\":%q}}}\n", at.UnixMicro(), worktree)
		}
		calls := func(input int) string {
			return fmt.Sprintf("{\"payload\":{\"event\":{\"kind\":\"model_completed\",\"usage\":{\"input_tokens\":%d}}}}\n", input)
		}
		sessions := filepath.Join(home, "sessions")
		day := filepath.Join(sessions, "2026", "11", "10")
		m1 := filepath.Join(day, "m1", "session.jsonl")
		other := filepath.Join(day, "m0", "session.jsonl")
		write(m1, metadata("/work/crew", startedAt.Add(time.Second))+calls(700000))
		write(other, metadata("/work/other", startedAt.Add(2*time.Second))+calls(111))
		h := &Herdr{AgentHomes: map[string]string{"muse": home}, Bin: fakeHerdrBin(t, `echo '{"id":"x","result":{"type":"agent_info","agent":{"agent":"muse","agent_status":"idle"}}}'`)}
		handle := map[string]any{"agent": "muse", "worktree": "/work/crew"}
		check := func(want int, has bool, what string) {
			t.Helper()
			used, window, ok, err := h.Context(ctx, handle, startedAt)
			if err != nil || used != want || ok != has || (has && window != 1007997) || (!has && window != 0) {
				t.Fatalf("%s: context = %d, %d, %v, %v; want %d, %v", what, used, window, ok, err, want, has)
			}
		}
		check(700000, true, "the match")
		// Another session writes to its own transcript between passes, as
		// every other crew and bead session of the kind does all the time.
		// Its file still names another worktree, so the match stands.
		appendLine(t, other, calls(222))
		// A full match would find nothing now: the directories list no
		// candidate. The reading below comes from the pin alone, so it
		// holds only if the pin survived the other transcript's growth.
		closeDirs(t, sessions, filepath.Join(sessions, "2026"), filepath.Join(sessions, "2026", "11"), day, filepath.Dir(m1), filepath.Dir(other))
		check(700000, true, "the pin after another transcript grew")
	})

	t.Run("transcript grown to qualify drops the pin", func(t *testing.T) {
		home := t.TempDir()
		startedAt := time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC)
		metadata := func(worktree string, at time.Time) string {
			return fmt.Sprintf("{\"recorded_at\":%d,\"payload_type\":\"runtime.session.metadata\",\"payload\":{\"record\":{\"workspace_root\":%q}}}\n", at.UnixMicro(), worktree)
		}
		calls := func(input int) string {
			return fmt.Sprintf("{\"payload\":{\"event\":{\"kind\":\"model_completed\",\"usage\":{\"input_tokens\":%d}}}}\n", input)
		}
		day := filepath.Join(home, "sessions", "2026", "11", "10")
		m1 := filepath.Join(day, "m1", "session.jsonl")
		other := filepath.Join(day, "m2", "session.jsonl")
		write(m1, metadata("/work/crew", startedAt.Add(time.Second))+calls(700000))
		write(other, metadata("/work/other", startedAt.Add(2*time.Second))+calls(111))
		h := &Herdr{AgentHomes: map[string]string{"muse": home}, Bin: fakeHerdrBin(t, `echo '{"id":"x","result":{"type":"agent_info","agent":{"agent":"muse","agent_status":"idle"}}}'`)}
		handle := map[string]any{"agent": "muse", "worktree": "/work/crew"}
		used, window, ok, err := h.Context(ctx, handle, startedAt)
		if err != nil || !ok || used != 700000 || window != 1007997 {
			t.Fatalf("context = %d, %d, %v, %v; want 700000, 1007997, true, nil", used, window, ok, err)
		}
		// The other transcript grows into a match for this worktree inside
		// the window: a full match now is ambiguous and answers none, so
		// the pin must go rather than keep answering m1.
		appendLine(t, other, metadata("/work/crew", startedAt.Add(3*time.Second)))
		used, window, ok, err = h.Context(ctx, handle, startedAt)
		if err != nil || ok || used != 0 || window != 0 {
			t.Fatalf("context after the other transcript qualified = %d, %d, %v, %v; want 0, 0, false, nil", used, window, ok, err)
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

	t.Run("least-recently-touched entries are evicted past the cap", func(t *testing.T) {
		const extra = 10
		var zero store.ContextSource
		// Worktree pins, each on its own transcript.
		home := t.TempDir()
		day := filepath.Join(home, "sessions", "2026", "11", "10")
		paths := make([]string, 0, contextCacheCap+extra)
		for i := range contextCacheCap + extra {
			p := filepath.Join(day, fmt.Sprintf("m%d", i), "session.jsonl")
			write(p, "{}\n")
			paths = append(paths, p)
		}
		h := &Herdr{}
		wkey := func(i int) string { return fmt.Sprintf("worktree-%d", i) }
		// Each pin's wide names its own transcript alone: the pins still
		// take the worktree path (wide is set), without every insert
		// restating every other candidate.
		for i := range contextCacheCap {
			h.pinContext(wkey(i), paths[i], contextJudge{wide: paths[i]})
		}
		if n := len(h.contextMatch); n != contextCacheCap {
			t.Fatalf("worktree pins after %d inserts = %d, want %d", contextCacheCap, n, contextCacheCap)
		}
		for i := range 5 {
			if got := h.pinnedContext(wkey(i)); got != paths[i] {
				t.Fatalf("touched worktree pin %d = %q, want %q", i, got, paths[i])
			}
		}
		// Two last readings, one under the oldest untouched pin and one
		// under a touched pin: the evicted pin's reading dies with it.
		for _, i := range []int{5, 0} {
			if _, _, _, err := h.cachedContext(paths[i], zero); err != nil {
				t.Fatal(err)
			}
		}
		for i := contextCacheCap; i < contextCacheCap+extra; i++ {
			h.pinContext(wkey(i), paths[i], contextJudge{wide: paths[i]})
		}
		if n := len(h.contextMatch); n != contextCacheCap {
			t.Fatalf("worktree pins after %d more inserts = %d, want %d", extra, n, contextCacheCap)
		}
		for i := range 5 {
			if _, ok := h.contextMatch[wkey(i)]; !ok {
				t.Errorf("touched worktree pin %d was evicted", i)
			}
		}
		for i := 5; i < 5+extra; i++ {
			if _, ok := h.contextMatch[wkey(i)]; ok {
				t.Errorf("untouched worktree pin %d survived", i)
			}
		}
		if _, ok := h.contextLast[paths[5]]; ok {
			t.Error("evicted worktree pin's last reading survived")
		}
		if _, ok := h.contextLast[paths[0]]; !ok {
			t.Error("touched worktree pin's last reading is missing")
		}
		if n, m := len(h.contextMatch), len(h.contextMatchUsed); n != m {
			t.Errorf("worktree pins = %d with %d orderings, want the same", n, m)
		}
		// Known-id pins over one file.
		shared := filepath.Join(t.TempDir(), "c1.jsonl")
		write(shared, "{}\n")
		k := &Herdr{}
		kkey := func(i int) string { return fmt.Sprintf("known-%d", i) }
		for i := range contextCacheCap {
			k.pinContext(kkey(i), shared, contextJudge{})
		}
		for i := range 5 {
			if got := k.pinnedContext(kkey(i)); got != shared {
				t.Fatalf("touched known-id pin %d = %q, want %q", i, got, shared)
			}
		}
		for i := contextCacheCap; i < contextCacheCap+extra; i++ {
			k.pinContext(kkey(i), shared, contextJudge{})
		}
		if n := len(k.contextMatch); n != contextCacheCap {
			t.Fatalf("known-id pins after %d more inserts = %d, want %d", extra, n, contextCacheCap)
		}
		for i := range 5 {
			if _, ok := k.contextMatch[kkey(i)]; !ok {
				t.Errorf("touched known-id pin %d was evicted", i)
			}
		}
		for i := 5; i < 5+extra; i++ {
			if _, ok := k.contextMatch[kkey(i)]; ok {
				t.Errorf("untouched known-id pin %d survived", i)
			}
		}
		if n, m := len(k.contextMatch), len(k.contextMatchUsed); n != m {
			t.Errorf("known-id pins = %d with %d orderings, want the same", n, m)
		}
		// Last readings, each of its own file.
		ldir := t.TempDir()
		lpaths := make([]string, 0, contextCacheCap+extra)
		for i := range contextCacheCap + extra {
			p := filepath.Join(ldir, fmt.Sprintf("t%d.jsonl", i))
			write(p, "{}\n")
			lpaths = append(lpaths, p)
		}
		l := &Herdr{}
		for _, p := range lpaths[:contextCacheCap] {
			if _, _, _, err := l.cachedContext(p, zero); err != nil {
				t.Fatal(err)
			}
		}
		for _, p := range lpaths[:5] {
			if _, _, _, err := l.cachedContext(p, zero); err != nil {
				t.Fatal(err)
			}
		}
		for _, p := range lpaths[contextCacheCap:] {
			if _, _, _, err := l.cachedContext(p, zero); err != nil {
				t.Fatal(err)
			}
		}
		if n := len(l.contextLast); n != contextCacheCap {
			t.Fatalf("last readings after %d more inserts = %d, want %d", extra, n, contextCacheCap)
		}
		for i, p := range lpaths[:5] {
			if _, ok := l.contextLast[p]; !ok {
				t.Errorf("touched last reading %d was evicted", i)
			}
		}
		for i, p := range lpaths[5 : 5+extra] {
			if _, ok := l.contextLast[p]; ok {
				t.Errorf("untouched last reading %d survived", 5+i)
			}
		}
		if n, m := len(l.contextLast), len(l.contextLastUsed); n != m {
			t.Errorf("last readings = %d with %d orderings, want the same", n, m)
		}
	})
}

// museTranscripts is a Muse session dir for a benchmark: others sessions of
// other worktrees spread over days day directories, and a 20 MB transcript of
// the crew session's own. It returns the dir and the start the own
// transcript matches from.
func museTranscripts(b *testing.B, others, days int) (home string, startedAt time.Time) {
	b.Helper()
	home = b.TempDir()
	startedAt = time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC)
	dir := filepath.Join(home, "sessions", "2026", "11")
	old := fmt.Sprintf("{\"recorded_at\":%d,\"payload_type\":\"runtime.session.metadata\",\"payload\":{\"record\":{\"workspace_root\":\"/work/elsewhere\"}}}\n", startedAt.Add(-48*time.Hour).UnixMicro())
	for i := range others {
		p := filepath.Join(dir, fmt.Sprintf("%02d", 1+i%days), fmt.Sprintf("old-%d", i), "session.jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			b.Fatal(err)
		}
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
	path := filepath.Join(dir, "10", "m1", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o600); err != nil {
		b.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Size() < 20<<20 {
		b.Fatalf("transcript stat = %v, %v; want at least 20 MB", fi, err)
	}
	return home, startedAt
}

// benchContext times h.Context(handle) after one warm call that reads the
// transcript: every timed call is a later pass, with nothing new to read.
func benchContext(b *testing.B, h *Herdr, handle map[string]any, startedAt time.Time, want int) {
	b.Helper()
	ctx := context.Background()
	if used, _, ok, err := h.Context(ctx, handle, startedAt); err != nil || !ok || used != want {
		b.Fatalf("warm context = %d, %v, %v; want %d, true, nil", used, ok, err, want)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		if _, _, ok, err := h.Context(ctx, handle, startedAt); err != nil || !ok {
			b.Fatalf("context = %v, %v; want true, nil", ok, err)
		}
	}
}

// BenchmarkHerdrContextCached is the daemon's per-pass cost of one crew
// session's context reading on a crew-sized transcript: a session dir with
// fifty other sessions' files and a 20 MB transcript of its own. The first
// call matches and reads it; every timed call is a later pass. Each call also
// starts the stand-in herdr that names the agent, which dominates its time:
// allocs/op shows what the reading itself costs.
func BenchmarkHerdrContextCached(b *testing.B) {
	home, startedAt := museTranscripts(b, 50, 1)
	h := &Herdr{AgentHomes: map[string]string{"muse": home}, Bin: fakeHerdrBin(b, `echo '{"id":"x","result":{"type":"agent_info","agent":{"agent":"muse","agent_status":"idle"}}}'`)}
	benchContext(b, h, map[string]any{"agent": "muse", "worktree": "/work/crew"}, startedAt, 700000)
}

// BenchmarkHerdrContextCachedAtScale is the same pass on the session dir a
// yard has after weeks (three thousand sessions over thirty days), with a
// handle that names no agent, so no herdr is started and the time is the
// match's memory being checked.
func BenchmarkHerdrContextCachedAtScale(b *testing.B) {
	home, startedAt := museTranscripts(b, 3000, 30)
	h := &Herdr{AgentHomes: map[string]string{"muse": home}}
	benchContext(b, h, map[string]any{"kind": "muse", "worktree": "/work/crew"}, startedAt, 700000)
}

// BenchmarkHerdrContextCachedOthersWriting is the at-scale pass while
// other sessions write: between calls one other transcript, still naming
// another worktree, is appended to, as the yard's other crew and bead
// sessions of the kind write all the time. The pin must hold through it,
// so a timed call re-judges the one file that moved and no other.
func BenchmarkHerdrContextCachedOthersWriting(b *testing.B) {
	home, startedAt := museTranscripts(b, 3000, 30)
	metadata := fmt.Sprintf("{\"recorded_at\":%d,\"payload_type\":\"runtime.session.metadata\",\"payload\":{\"record\":{\"workspace_root\":\"/work/other\"}}}\n", startedAt.Add(time.Second).UnixMicro())
	call := "{\"payload\":{\"event\":{\"kind\":\"model_completed\",\"usage\":{\"input_tokens\":111}}}}\n"
	var actives []string
	for i := range 4 {
		p := filepath.Join(home, "sessions", "2026", "11", "10", fmt.Sprintf("active-%d", i), "session.jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(metadata+call), 0o600); err != nil {
			b.Fatal(err)
		}
		actives = append(actives, p)
	}
	h := &Herdr{AgentHomes: map[string]string{"muse": home}}
	handle := map[string]any{"kind": "muse", "worktree": "/work/crew"}
	ctx := context.Background()
	if used, _, ok, err := h.Context(ctx, handle, startedAt); err != nil || !ok || used != 700000 {
		b.Fatalf("warm context = %d, %v, %v; want 700000, true, nil", used, ok, err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := range b.N {
		f, err := os.OpenFile(actives[i%len(actives)], os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := f.WriteString(call); err != nil {
			b.Fatal(err)
		}
		if err := f.Close(); err != nil {
			b.Fatal(err)
		}
		if _, _, ok, err := h.Context(ctx, handle, startedAt); err != nil || !ok {
			b.Fatalf("context = %v, %v; want true, nil", ok, err)
		}
	}
}

// BenchmarkHerdrContextKnownID is the pass for a session herdr names an id
// for, on a Claude home that holds three hundred project directories: the
// one file the id names is found each time, then not read again.
func BenchmarkHerdrContextKnownID(b *testing.B) {
	home := b.TempDir()
	line := "{\"type\":\"assistant\",\"message\":{\"model\":\"m\",\"usage\":{\"input_tokens\":10,\"cache_read_input_tokens\":100,\"output_tokens\":7}}}\n"
	for i := range 300 {
		for _, id := range []string{fmt.Sprintf("old-%d-a", i), fmt.Sprintf("old-%d-b", i)} {
			p := filepath.Join(home, "projects", fmt.Sprintf("-work-%d", i), id+".jsonl")
			if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(line), 0o600); err != nil {
				b.Fatal(err)
			}
		}
	}
	own := filepath.Join(home, "projects", "-work-crew", "c1.jsonl")
	if err := os.MkdirAll(filepath.Dir(own), 0o700); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(own, []byte(line), 0o600); err != nil {
		b.Fatal(err)
	}
	h := &Herdr{AgentHomes: map[string]string{"claude": home}, Bin: fakeHerdrBin(b, `echo '{"id":"x","result":{"type":"agent_info","agent":{"agent":"claude","agent_session":{"kind":"id","value":"c1"},"agent_status":"idle"}}}'`)}
	benchContext(b, h, map[string]any{"agent": "crew"}, time.Time{}, 110)
}
