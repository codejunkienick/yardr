package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/dispatch"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
)

// An agy crew session pins the conversation its own launch started: the
// index may name another pane's conversation in the same directory, but
// the pin needs this launch's first prompt. The reading follows the
// pinned transcript through model changes and compactions, and a handoff
// is asked at the threshold once idle.
func TestCrewAgyPinsItsOwnConversation(t *testing.T) {
	ctx := context.Background()
	t.Setenv("HOME", t.TempDir())
	home, dir := t.TempDir(), t.TempDir()
	rule := "────────────────────────────────────────────────────────────\n"
	empty := rule + "\x1b[94m>\x1b[m \x1b[90mAuto mode: edits & safe commands auto-approved (shift+tab to cycle)\x1b[m\n" + rule
	h, calls, _ := inputHerdr(t, ">", empty)
	h.YardHome, h.YardBin = t.TempDir(), "/bin/yardr"
	h.AgentHomes = map[string]string{"agy": home}
	status := filepath.Join(t.TempDir(), "status")
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(status, "idle")
	h.Bin = deafHerdrBin(t, `case "$1 $2" in
  "pane process-info") echo '{"result":{"process_info":{"pane_id":"p1","shell_pid":`+fmt.Sprint(os.Getpid())+`}}}' ;;
  "agent get") printf '{"result":{"agent":{"agent":"agy","agent_status":"%s"}}}\n' "$(cat '`+status+`')" ;;
  *) exec '`+h.Bin+`' "$@" ;;
esac`)
	st := storetest.New(t)
	h.Store = st
	def := store.CrewMember{Name: "yardmaster", Runner: "herdr", Dir: dir,
		Config: map[string]any{"kind": "agy", "model": "gemini-4-hydrogen-high", "trust": false}}
	if _, err := st.PutCrew(ctx, def, "me"); err != nil {
		t.Fatal(err)
	}
	d := &dispatch.Dispatcher{Store: st, Runners: map[string]dispatch.Runner{"herdr": h}, PromptSettle: -1, PromptWait: -1}
	pass := func() *store.CrewMember {
		t.Helper()
		if err := d.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
		r, err := st.GetCrew(ctx, def.Name)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	r := pass()
	if !r.Live() || r.Handle["dir"] != dir || r.Handle["worktree"] != nil {
		t.Fatalf("want live crew dir handle, got %+v", r)
	}
	prompt, _ := r.Handle[firstPrompt].(string)
	if prompt == "" || !strings.Contains(calls(), prompt) {
		t.Fatal("the first prompt did not reach the crew pane")
	}
	launch, _ := r.Handle[launchReference].(string)
	if launch == "" || !strings.Contains(prompt, crewSessionReference(launch)) {
		t.Fatalf("the first prompt carries no launch reference: %+v", r.Handle)
	}
	competingPrompt := strings.Replace(prompt, launch, "a-different-launch", 1)
	at := r.StartedAt.Add(time.Second).UTC().Format(time.RFC3339Nano)
	first := func(prompt string) string {
		return fmt.Sprintf(`{"type":"USER_INPUT","created_at":%q,"content":%q}`+"\n", at,
			"<USER_REQUEST>\n"+prompt+"\n</USER_REQUEST>\n<metadata/>")
	}
	response := func(model string, input int) string {
		return fmt.Sprintf(`{"type":"PLANNER_RESPONSE","model":%q,"input_tokens":%d,"cache_read_tokens":900}`+"\n", model, input)
	}
	index := filepath.Join(home, "cache", "last_conversations.json")
	pointAt := func(id string) { write(index, fmt.Sprintf(`{%q:%q}`, dir, id)) }
	transcript := func(id string) string {
		return filepath.Join(home, "brain", id, ".system_generated", "logs", "transcript.jsonl")
	}
	own := first(prompt) + response("gemini-4-hydrogen-high", 100)
	// Even the same crew prompt in the same directory may be from another launch.
	write(transcript("other"), first(competingPrompt)+response("gemini-4-hydrogen-high", 659100)+first(prompt))
	pointAt("other")
	r = pass()
	if r.Handle[store.HandleAgentSession] != nil || r.ContextAt != nil || strings.Contains(calls(), "Your context is") {
		t.Fatalf("unverified conversation produced a pin or reading: %+v", r)
	}
	write(transcript("own"), own)
	pointAt("own")
	r = pass()
	if r.Handle[store.HandleAgentSession] != "own" || r.ContextWindow != 1048576 || r.ContextPct != 0 || r.ContextAt == nil {
		t.Fatalf("own conversation was not pinned and read: %+v", r)
	}
	pointAt("other")
	r = pass()
	if r.Handle[store.HandleAgentSession] != "own" || r.ContextPct != 0 || strings.Contains(calls(), "Your context is") {
		t.Fatalf("changed index moved a live pin or reading: %+v", r)
	}
	write(status, "working")
	own += response("gemini-4-neon-medium", 319100)
	write(transcript("own"), own)
	r = pass()
	if r.ContextPct != 61 || r.ContextWindow != 524288 || r.ContextAskedAt != nil || strings.Contains(calls(), "Your context is") {
		t.Fatalf("Neon working reading should record its use and wait for idle: %+v", r)
	}
	own += `{"type":"CHECKPOINT","input_tokens":319100,"cache_read_tokens":900}` + "\n"
	write(transcript("own"), own)
	write(status, "idle")
	r = pass()
	if r.ContextAskedAt != nil || strings.Contains(calls(), "Your context is") {
		t.Fatalf("compaction left a stale handoff eligible at idle: %+v", r)
	}
	own += response("gemini-4-neon-medium", 100)
	write(transcript("own"), own)
	r = pass()
	if r.ContextPct != 0 || r.ContextAskedAt != nil {
		t.Fatalf("post-compaction response did not refresh the reading: %+v", r)
	}
	write(transcript("own"), own+response("gemini-4-neon-medium", 319100))
	r = pass()
	if r.ContextAskedAt == nil || r.ContextPct != 61 || r.ContextWindow != 524288 ||
		strings.Count(calls(), "Your context is 61% of the window.") != 1 {
		t.Fatalf("own conversation did not get its idle handoff: %+v", r)
	}
}

// An agy crew pane whose env_file sets HOME apart from the daemon's keeps
// the pane's HOME on its handle, and the pin lookup and context read
// resolve it there: the daemon's home holds no index, so a lookup under
// it finds nothing.
func TestCrewAgyPinsWithSeparatePaneHome(t *testing.T) {
	ctx := context.Background()
	t.Setenv("HOME", t.TempDir()) // the daemon's home holds no index
	pane, dir := t.TempDir(), t.TempDir()
	home := filepath.Join(pane, ".gemini/antigravity-cli")
	rule := "────────────────────────────────────────────────────────────\n"
	empty := rule + "\x1b[94m>\x1b[m \x1b[90mAuto mode: edits & safe commands auto-approved (shift+tab to cycle)\x1b[m\n" + rule
	h, calls, _ := inputHerdr(t, ">", empty)
	h.YardHome, h.YardBin = t.TempDir(), "/bin/yardr"
	status := filepath.Join(t.TempDir(), "status")
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(status, "idle")
	h.Bin = deafHerdrBin(t, `case "$1 $2" in
  "pane process-info") echo '{"result":{"process_info":{"pane_id":"p1","shell_pid":`+fmt.Sprint(os.Getpid())+`}}}' ;;
  "agent get") printf '{"result":{"agent":{"agent":"agy","agent_status":"%s"}}}\n' "$(cat '`+status+`')" ;;
  *) exec '`+h.Bin+`' "$@" ;;
esac`)
	envFile := filepath.Join(t.TempDir(), "pane.env")
	write(envFile, "HOME="+pane+"\n")
	st := storetest.New(t)
	h.Store = st
	def := store.CrewMember{Name: "yardmaster", Runner: "herdr", Dir: dir,
		Config: map[string]any{"kind": "agy", "model": "gemini-4-hydrogen-high", "trust": false, "env_file": envFile}}
	if _, err := st.PutCrew(ctx, def, "me"); err != nil {
		t.Fatal(err)
	}
	d := &dispatch.Dispatcher{Store: st, Runners: map[string]dispatch.Runner{"herdr": h}, PromptSettle: -1, PromptWait: -1}
	pass := func() *store.CrewMember {
		t.Helper()
		if err := d.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
		r, err := st.GetCrew(ctx, def.Name)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	r := pass()
	if !r.Live() || r.Handle[paneHome] != pane {
		t.Fatalf("want live crew with the pane's home on the handle, got %+v", r)
	}
	prompt, _ := r.Handle[firstPrompt].(string)
	if prompt == "" || !strings.Contains(calls(), prompt) {
		t.Fatal("the first prompt did not reach the crew pane")
	}
	at := r.StartedAt.Add(time.Second).UTC().Format(time.RFC3339Nano)
	own := fmt.Sprintf(`{"type":"USER_INPUT","created_at":%q,"content":%q}`+"\n", at,
		"<USER_REQUEST>\n"+prompt+"\n</USER_REQUEST>\n<metadata/>") +
		`{"type":"PLANNER_RESPONSE","model":"gemini-4-hydrogen-high","input_tokens":100,"cache_read_tokens":900}` + "\n"
	write(filepath.Join(home, "brain", "own", ".system_generated", "logs", "transcript.jsonl"), own)
	write(filepath.Join(home, "cache", "last_conversations.json"), fmt.Sprintf(`{%q:%q}`, dir, "own"))
	r = pass()
	if r.Handle[store.HandleAgentSession] != "own" || r.ContextWindow != 1048576 || r.ContextPct != 0 || r.ContextAt == nil {
		t.Fatalf("own conversation was not pinned and read from the pane's home: %+v", r)
	}
}
