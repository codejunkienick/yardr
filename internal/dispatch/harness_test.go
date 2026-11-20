package dispatch_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"yardr.dev/yardr/internal/dispatch"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
)

// A codex pane as it stood on 6 Oct 2026, when a reviewer's harness stopped
// and sat for an hour looking like any idle agent.
const atCapacity = "• Ran yardr prime --bead yardr-x1hy\n" +
	"  └ yardr-x1hy  A session waiting on its own tests is not idle\n" +
	"■ Selected model is at capacity. Please try a different model.\n" +
	"› Ask Codex to do anything\n" +
	"  gpt-5.5 high · 71% context left"

// harnessSetup is a yard with one group of agents of kind, whose runner
// reports status and has a pane to read. The agent works.
func harnessSetup(t *testing.T, kind string, cfg map[string]any) (store.API, *agentRunner, *dispatch.Dispatcher, store.Bead) {
	t.Helper()
	s := storetest.New(t)
	r := &agentRunner{statusRunner: &statusRunner{fakeRunner: &fakeRunner{dead: map[string]bool{}}, status: "working"}, worked: true}
	d := &dispatch.Dispatcher{Store: s, Runners: map[string]dispatch.Runner{"fake": r}, Backoff: time.Hour}
	t.Cleanup(func() { dispatch.WaitLimitsIdle(d) })
	conf := map[string]any{"kind": kind}
	for k, v := range cfg {
		conf[k] = v
	}
	ctx := context.Background()
	must(t, s.PutGroup(ctx, store.Group{Name: "g", Runner: "fake", Config: conf}, "me"))
	_, err := s.AddRoute(ctx, store.Route{Stage: "new", Group: "g"}, "me")
	must(t, err)
	b, err := s.CreateBead(ctx, store.NewBead{Title: "x"}, "me")
	must(t, err)
	must(t, d.Reconcile(ctx)) // start
	must(t, d.Reconcile(ctx)) // seen working
	return s, r, d, *b
}

// show sets what the agent's pane ends in and what herdr says of it.
func (a *agentRunner) show(status, tail string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status, a.tail = status, tail
}

// A harness that stops on an error of its own is idle to herdr. The yard
// reads its pane on the first pass that finds it idle and says what it
// stopped on, long before the idle limit, once per idle spell.
func TestHarnessErrorIsReportedAtTheFirstIdlePass(t *testing.T) {
	t.Parallel()
	s, r, d, b := harnessSetup(t, "codex", nil) // idle: the default, 30m
	ctx := context.Background()
	told := func() []store.Event { t.Helper(); return eventKinds(t, s, b.ID, "session_harness_error") }
	if n := len(told()); n != 0 {
		t.Fatalf("session_harness_error for a working agent: %d", n)
	}

	r.show("idle", atCapacity)
	must(t, d.Reconcile(ctx))
	evs := told()
	if len(evs) != 1 {
		t.Fatalf("session_harness_error events = %d, want 1 on the first idle pass", len(evs))
	}
	got, _ := s.GetBead(ctx, b.ID)
	data := evs[0].Data
	if data["reason"] != dispatch.ReasonAtCapacity || data["kind"] != "codex" || data["session"] != *got.Session ||
		data["group"] != "g" || data["stage"] != "new" ||
		data["line"] != "■ Selected model is at capacity. Please try a different model." ||
		data["tail"] != atCapacity || !strings.Contains(data["hint"].(string), "yardr bead release "+b.ID) {
		t.Fatalf("session_harness_error data = %v", data)
	}
	if n := len(eventKinds(t, s, b.ID, "session_idle")); n != 0 {
		t.Fatalf("session_idle before its limit: %d", n)
	}

	// Once per spell, and a daemon that replaced itself tells nobody again:
	// the mark is on the session's handle.
	must(t, d.Reconcile(ctx))
	d2 := &dispatch.Dispatcher{Store: s, Runners: map[string]dispatch.Runner{"fake": r}, Backoff: time.Hour}
	must(t, d2.Reconcile(ctx))
	must(t, d2.Reconcile(ctx))
	if n := len(told()); n != 1 {
		t.Fatalf("session_harness_error events = %d, want 1 per idle spell", n)
	}

	// Someone gets the agent going; when it stops again, on another error,
	// that is told.
	r.show("working", "• Working")
	must(t, d2.Reconcile(ctx))
	if sess, _ := s.GetSession(ctx, *got.Session); sess.Handle[dispatch.HarnessErrorKey] != nil {
		t.Fatalf("handle = %v; the mark outlived the idle spell", sess.Handle)
	}
	r.show("idle", "■ You've hit your usage limit. Upgrade to Plus to continue using Codex.")
	must(t, d2.Reconcile(ctx))
	if evs = told(); len(evs) != 2 || evs[1].Data["reason"] != dispatch.ReasonRateLimited {
		t.Fatalf("session_harness_error events = %v, want a second one, rate_limited", evs)
	}
}

// What is not a harness's error raises nothing: an agent that only idles,
// one that wrote about an error, an error line of another harness, a kind
// nothing is known of. And a group with idle off still hears of an error.
func TestHarnessErrorOnlyForAKnownLineOfTheKind(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		kind string
		cfg  map[string]any
		tail string
		want string
	}{
		"only idle":             {kind: "codex", tail: "• Committed the change. Waiting for review.\n› Ask Codex to do anything"},
		"writes about an error": {kind: "codex", tail: "• The reviewer saw \"Selected model is at capacity\" last night.\n› Ask Codex to do anything"},
		"muse idle":             {kind: "muse", tail: "◆ Finished the change.\n❯ "},
		"muse quotes a limit":   {kind: "muse", tail: "◆ The pane said 'Usage limit reached'.\n❯ "},
		"muse input":            {kind: "muse", tail: "❯ This turn stopped because your usage limit was reached.\n❯ "},
		"another harness's":     {kind: "claude", tail: atCapacity},
		"a kind with no lines":  {kind: "opencode", tail: atCapacity},
		"idle off":              {kind: "codex", cfg: map[string]any{"idle": "off"}, tail: atCapacity, want: dispatch.ReasonAtCapacity},
		"claude, the default kind": {kind: "", tail: "⏺ API Error: 529 {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n" +
			"────────\n❯ \n────────\n  ⏵⏵ auto mode on", want: dispatch.ReasonAtCapacity},
	} {
		t.Run(name, func(t *testing.T) {
			s, r, d, b := harnessSetup(t, tc.kind, tc.cfg)
			r.show("idle", tc.tail)
			must(t, d.Reconcile(context.Background()))
			must(t, d.Reconcile(context.Background()))
			evs := eventKinds(t, s, b.ID, "session_harness_error")
			if tc.want == "" && len(evs) != 0 {
				t.Fatalf("session_harness_error = %v, want none", evs[0].Data)
			}
			if tc.want != "" && (len(evs) != 1 || evs[0].Data["reason"] != tc.want) {
				t.Fatalf("session_harness_error = %v, want one with reason %s", evs, tc.want)
			}
		})
	}
}

// session_idle carries the end of the agent's pane, as session_stalled
// does: its reader sees why the agent sits there without reading the pane.
// A harness's error first seen then is told as well.
func TestIdleSessionCarriesItsPane(t *testing.T) {
	t.Parallel()
	s, r, d, b := harnessSetup(t, "codex", map[string]any{"idle": "30ms"})
	ctx := context.Background()
	const waits = "• Done. The tests passed; I wait for the reviewer's answer.\n› Ask Codex to do anything"
	r.show("idle", waits)
	must(t, d.Reconcile(ctx))
	time.Sleep(40 * time.Millisecond)
	// The error comes onto the screen late, as the idle limit passes.
	r.show("idle", atCapacity)
	must(t, d.Reconcile(ctx))
	evs := eventKinds(t, s, b.ID, "session_idle")
	if len(evs) != 1 || evs[0].Data["tail"] != atCapacity {
		t.Fatalf("session_idle = %v, want one with the pane's last lines in tail", evs)
	}
	if n := len(eventKinds(t, s, b.ID, "session_harness_error")); n != 1 {
		t.Fatalf("session_harness_error events = %d, want 1 from the pane read for session_idle", n)
	}
}

// The pane of a builder that waits for a command it started in the
// background, as Claude Code 2.1.288 drew it (yardr-6m7z, 13 Oct 2026), and
// the same pane with the shell gone.
const (
	shellRuns = "⏺ The measurement is running in the background; I'll carry on with the per-test tables when it finishes.\n" +
		"✻ Brewed for 48s · done 12:44 AM · 1 shell still running\n" +
		"                                             Update available! Run: brew upgrade claude-code@latest\n" +
		"────────────────────────────────────────\n❯ \n────────────────────────────────────────\n" +
		"  ⏵⏵ auto mode on · 1 shell · ← 1 agent · ↓ to manage"
	shellGone = "⏺ The measurement said 1 shell still running when I looked; what do you want me to do with it?\n" +
		"✻ Brewed for 48s · done 12:44 AM\n" +
		"────────────────────────────────────────\n❯ \n────────────────────────────────────────\n" +
		"  ⏵⏵ auto mode on · ← 1 agent · ↓ to manage"
)

// An agent that waits for a shell of its own is idle to herdr, and its
// harness wakes it when the shell exits (yardr-zjhw: a builder whose depot
// test ran longer than its group's idle was told of every five minutes).
// While its pane says so, as its kind's file declares, no session_idle is
// raised and a grace does not end the session. Once the pane no longer says
// so, the limit applies as to any idle agent.
func TestIdleSessionWithBackgroundWorkIsNotIdle(t *testing.T) {
	t.Parallel()
	s, r, d, b := harnessSetup(t, "claude", map[string]any{"idle": "30ms", "grace": "0", "stall": "off"})
	ctx := context.Background()
	told := func() int { t.Helper(); return len(eventKinds(t, s, b.ID, "session_idle")) }
	r.show("idle", shellRuns)
	must(t, d.Reconcile(ctx))
	for range 3 {
		time.Sleep(40 * time.Millisecond)
		must(t, d.Reconcile(ctx))
	}
	if n := told(); n != 0 {
		t.Fatalf("session_idle for an agent that waits for its background shell: %d", n)
	}
	if got, _ := s.GetBead(ctx, b.ID); got.Session == nil {
		t.Fatal("session stopped at its grace while its background shell ran")
	}

	// The shell is gone and the agent still sits there: idle from the last
	// look that saw the shell, told of at the limit and stopped at the grace.
	r.show("idle", shellGone)
	must(t, d.Reconcile(ctx))
	time.Sleep(40 * time.Millisecond)
	must(t, d.Reconcile(ctx))
	if n := told(); n != 1 {
		t.Fatalf("session_idle events = %d, want 1 once the pane shows no background work", n)
	}
	if got, _ := s.GetBead(ctx, b.ID); got.Session != nil {
		t.Fatal("session still runs past its grace with no background work")
	}
}

// What a pane says of background work counts for the kind that declares
// it, in what its harness draws around its input: a kind with no such lines
// is told of at the limit whatever its pane shows, and so is an agent
// further up whose pane the words stand, in what it wrote.
func TestBackgroundWorkIsItsKindsToDeclare(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct{ kind, pane string }{
		"a kind that declares none": {"codex", shellRuns},
		"no background work":        {"claude", shellGone},
		"the agent's own words": {"claude", "⏺ Earlier: done 12:44 AM · 1 shell still running\n" +
			"⏺ one\n⏺ two\n⏺ three\n⏺ four\n⏺ five\n⏺ six\n❯ "},
	} {
		t.Run(name, func(t *testing.T) {
			s, r, d, b := harnessSetup(t, c.kind, map[string]any{"idle": "30ms", "stall": "off"})
			ctx := context.Background()
			r.show("idle", c.pane)
			must(t, d.Reconcile(ctx))
			time.Sleep(40 * time.Millisecond)
			must(t, d.Reconcile(ctx))
			evs := eventKinds(t, s, b.ID, "session_idle")
			if len(evs) != 1 || evs[0].Data["tail"] != c.pane {
				t.Fatalf("session_idle = %v, want one at the limit with the pane in tail", evs)
			}
		})
	}
	if line, ok := dispatch.HarnessBackground("claude", shellRuns); !ok || line != "⏵⏵ auto mode on · 1 shell · ← 1 agent · ↓ to manage" {
		t.Fatalf("background work in the pane = %q, %v; want the status line", line, ok)
	}
}

// tallyRunner is an agentRunner whose agents also take nudges, as herdr's
// do, keeping the nudges and counting the pane reads.
type tallyRunner struct {
	*agentRunner
	mu     sync.Mutex
	nudges []string
	tails  int
}

func (n *tallyRunner) Nudge(_ context.Context, _ map[string]any, text string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.nudges = append(n.nudges, text)
	return nil
}

func (n *tallyRunner) Tail(ctx context.Context, h map[string]any, lines int) (string, error) {
	n.mu.Lock()
	n.tails++
	n.mu.Unlock()
	return n.agentRunner.Tail(ctx, h, lines)
}

func (n *tallyRunner) told() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string{}, n.nudges...)
}

func (n *tallyRunner) reads() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.tails
}

// The pane of a builder that waits for a command it started in the
// background, as Muse Code 1.4.9 drew it (read live from pane w114:p2, 20
// Oct 2026), and the same pane with the command gone. museRuns1411 is the
// same wait as 1.4.11 draws it, with ◈ for ◆ (read live from pane
// w18H:p2, 21 Oct 2026).
const (
	museRuns = "◆ Still blocked on the gate run — no result yet.\n" +
		"── Voice input (alt(option)+v to start) ──…\n" +
		"❯ \n" +
		"───…\n" +
		"main · ↓ to select\n" +
		"└ ◆ Run depot test gate in worktree  running          27m 03s\n" +
		"  muse-spark-1.3 · max · /tmp/work · Auto-review"
	museRuns1411 = "◆ Still blocked on the gate run — no result yet.\n" +
		"── Voice input (alt(option)+v to start) ──…\n" +
		"❯ \n" +
		"───…\n" +
		"main · ↓ to select\n" +
		"└ ◈ Run depot test gate in worktree  running          27m 03s\n" +
		"  muse-spark-1.3 · max · /tmp/work · Auto-review"
	museGone = "◆ The gate passed; handing on now.\n" +
		"── Voice input (alt(option)+v to start) ──…\n" +
		"❯ \n" +
		"───…\n" +
		"  muse-spark-1.3 · max · /tmp/work · Auto-review"
)

// An agent that waits for a shell of its own is not nudged to hand on
// (yardr-kh12: each answer ended the agent's turn, which started a new
// idle spell, so every pass nudged again). Once the pane no longer says
// so, the nudge goes as ever, once; and a pass with nothing to send reads
// no pane.
func TestIdleSessionWithBackgroundWorkIsNotNudged(t *testing.T) {
	t.Parallel()
	s, r, d, b := harnessSetup(t, "muse", map[string]any{"idle": "1h", "stall": "off"})
	n := &tallyRunner{agentRunner: r}
	d.Runners["fake"] = n
	ctx := context.Background()
	sess := liveSession(t, s, b.ID)
	must(t, s.SetHandle(ctx, sess, map[string]any{"session": sess, "agent": "yard-x-new"}))
	now := time.Now()
	dispatch.SetClock(d, func() time.Time { return now })
	nudged := func() int { t.Helper(); return len(eventKinds(t, s, b.ID, "session_idle_nudge")) }

	r.show("idle", museRuns)
	must(t, d.Reconcile(ctx))
	// The agent answers the silence, as it answered each nudge: a new
	// spell, still waiting on its shell.
	r.show("working", "◆ Working")
	must(t, d.Reconcile(ctx))
	r.show("idle", museRuns)
	must(t, d.Reconcile(ctx))
	// The same wait as 1.4.11 draws it: a new spell, still no nudge.
	r.show("working", "◆ Working")
	must(t, d.Reconcile(ctx))
	r.show("idle", museRuns1411)
	must(t, d.Reconcile(ctx))
	if got := n.told(); len(got) != 0 || nudged() != 0 {
		t.Fatalf("nudges = %q, session_idle_nudge events = %d; want none while a background shell runs", got, nudged())
	}

	// The shell is gone and the agent still sits there: the nudge goes,
	// once, as today.
	r.show("idle", museGone)
	must(t, d.Reconcile(ctx))
	if got := n.told(); len(got) != 1 || !strings.Contains(got[0], "write your note and hand on") {
		t.Fatalf("nudges = %q, want the handover nudge once the pane shows no background work", got)
	}
	if n := nudged(); n != 1 {
		t.Fatalf("session_idle_nudge events = %d, want 1", n)
	}

	// Past the spell's young looks, a pass with nothing to send reads no
	// pane: the nudge's own read went only when it was due.
	now = now.Add(3 * time.Minute)
	reads := n.reads()
	must(t, d.Reconcile(ctx))
	must(t, d.Reconcile(ctx))
	if got := n.reads(); got != reads {
		t.Fatalf("pane reads = %d, want still %d on passes with nothing to send", got, reads)
	}
	if got := n.told(); len(got) != 1 {
		t.Fatalf("nudges = %q, want still the one", got)
	}
}

// A runner with no pane to read gives a session_idle without one.
func TestIdleSessionWithoutAPane(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	r := &statusRunner{fakeRunner: &fakeRunner{dead: map[string]bool{}}, status: "idle"}
	d := &dispatch.Dispatcher{Store: s, Runners: map[string]dispatch.Runner{"fake": r}, Backoff: time.Hour}
	ctx := context.Background()
	must(t, s.PutGroup(ctx, store.Group{Name: "g", Runner: "fake", Config: map[string]any{"idle": "30ms", "stall": "off"}}, "me"))
	_, _ = s.AddRoute(ctx, store.Route{Stage: "new", Group: "g"}, "me")
	b, _ := s.CreateBead(ctx, store.NewBead{Title: "x"}, "me")
	must(t, d.Reconcile(ctx))
	must(t, d.Reconcile(ctx))
	time.Sleep(40 * time.Millisecond)
	must(t, d.Reconcile(ctx))
	evs := eventKinds(t, s, b.ID, "session_idle")
	if len(evs) != 1 {
		t.Fatalf("session_idle events = %d, want 1", len(evs))
	}
	if _, has := evs[0].Data["tail"]; has {
		t.Fatalf("session_idle data = %v; a tail from a runner that has no pane", evs[0].Data)
	}
}

// The lines each harness is known to stop on, as it prints them, and lines
// that only look like one.
func TestHarnessErrorLines(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ kind, line, want string }{
		{"codex", "■ Selected model is at capacity. Please try a different model.", dispatch.ReasonAtCapacity},
		{"codex", "■ You've hit your usage limit. Visit https://chatgpt.com/codex/settings/usage to purchase more credits", dispatch.ReasonRateLimited},
		{"codex", "■ exceeded retry limit, last status: 429 Too Many Requests", dispatch.ReasonRateLimited},
		{"codex", "■ Your access token could not be refreshed. Please log out and sign in again.", dispatch.ReasonNotLoggedIn},
		{"codex", "  Sign in with ChatGPT", dispatch.ReasonNotLoggedIn},
		{"codex", "■ stream disconnected before completion: Incomplete response returned", dispatch.ReasonNetwork},
		{"codex", "■ unexpected status 503 Service Unavailable", dispatch.ReasonOther},
		{"codex", "• I added a test for the retry limit.", ""},
		{"claude", "Not logged in · Run /login", dispatch.ReasonNotLoggedIn},
		{"claude", "  ⎿  Invalid API key · Please run /login", dispatch.ReasonNotLoggedIn},
		{"claude", "⏺ API Error: 401 {\"type\":\"error\",\"error\":{\"type\":\"authentication_error\",\"message\":\"OAuth token has expired.\"}} · Please run /login", dispatch.ReasonNotLoggedIn},
		{"claude", "  ⎿  You've hit your limit · resets 3pm (Europe/Berlin)", dispatch.ReasonRateLimited},
		{"claude", "  ⎿  5-hour limit reached ∙ resets 3pm", dispatch.ReasonRateLimited},
		{"claude", "⏺ API Error: 429 {\"type\":\"error\",\"error\":{\"type\":\"rate_limit_error\"}}", dispatch.ReasonRateLimited},
		{"claude", "⏺ API Error: Repeated 529 Overloaded errors", dispatch.ReasonAtCapacity},
		{"claude", "● API Error: Connection dropped (ECONNRESET). A VPN, firewall, or security software can cause this. Check those", dispatch.ReasonNetwork},
		{"claude", "● API Error: Connection lost mid-response. The response above may be incomplete.", dispatch.ReasonNetwork},
		{"claude", "● API Error: Request blocked before it reached", dispatch.ReasonNetwork},
		{"claude", "⏺ " + modelError, dispatch.ReasonOther},
		{"claude", "  ⎿  Credit balance is too low", dispatch.ReasonOther},
		{"claude", "⏺ API Error: 500 {\"type\":\"error\",\"error\":{\"type\":\"api_error\"}}", dispatch.ReasonOther},
		{"claude", "⏺ The session limit reached its end; see the API Error: 529 above.", ""},
		{"muse", "model failed: server_error: The model failed to generate a response. (request id: 688dfffa-1833-4175-96d6-ae6513c47149, response: resp_6ad2e591f2d29e424980460b)", dispatch.ReasonOther},
		{"muse", "◆ model failed: server_error: The model failed to generate a response. (request id: 688dfffa-1833-4175-96d6-ae6513c47149, response: resp_6ad2e591f2d29e424980460b)", dispatch.ReasonOther},
		{"muse", "model failed: stream protocol error: function_call_arguments.done name mismatch for `fc_0192a3f4` (request id: 9a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d)", dispatch.ReasonOther},
		{"muse", "◆ model failed: stream protocol error: function_call_arguments.done name mismatch for `fc_0192a3f4` (request id: 9a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d)", dispatch.ReasonOther},
		{"muse", "◆ model failed: network connection could not be opened within 15 seconds (after 4 provider attempts)", dispatch.ReasonNetwork},
		{"muse", "◆ Usage limit reached", dispatch.ReasonRateLimited},
		{"muse", "│ This turn stopped because your usage limit was reached.", dispatch.ReasonRateLimited},
		{"muse", "◆ The pane said 'Usage limit reached' earlier.", ""},
		{"muse", "◆ 'Usage limit reached' is the error we handle.", ""},
		{"muse", "❯ Usage limit reached", ""},
		{"muse", "◆ Usage limit reached in the example, not this turn.", ""},
		{"muse", "◆ The pane said model failed: server_error last night.", ""},
		{"muse", "❯ Why did it say model failed: server_error?", ""},
		{"pi", " Error: Retry failed after 3 attempts: 529: {\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}", dispatch.ReasonOther},
		{"pi", " Error: Retry failed after 3 attempts: 529:", dispatch.ReasonOther},
		{"pi", " Error: Retry failed after 10 attempts: Unknown error", dispatch.ReasonOther},
		{"pi", " Error: 529: {\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}", ""},
		{"pi", " Error: ENOENT: no such file or directory, open 'go.mod'", ""},
		{"pi", " Retrying (2/3) in 4s... (esc to cancel)", ""},
		{"pi", " The code draws Error: Retry failed after 3 attempts when it gives up.", ""},
		{"pi", "❯ Why did it say Retry failed after 3 attempts?", ""},
		{"claude", " Error: Retry failed after 3 attempts: 529:", ""},
		{"kimi", "   Error: [provider.auth_error] 403 You've reached your", dispatch.ReasonRateLimited},
		{"kimi", "   Error: [provider.auth_error] 403 You've reached your 5-hour usage limit. Your quota will reset when the current 5-hour window ends.", dispatch.ReasonRateLimited},
		{"kimi", "   Error: [provider.rate_limit] 429 Too many requests", dispatch.ReasonRateLimited},
		{"kimi", "   Error: [provider.overloaded] 529 The engine is", dispatch.ReasonAtCapacity},
		{"kimi", "   Error: [provider.auth_error] 401 Invalid", dispatch.ReasonNotLoggedIn},
		{"kimi", "   Error: [auth.login_required] Login required", dispatch.ReasonNotLoggedIn},
		{"kimi", "   Error: [provider.api_error] 500 Internal error", ""},
		{"kimi", " ● The pane said Error: [provider.auth_error] 403 You've reached your limit.", ""},
		{"kimi", " 5-hour usage limit. Your quota will reset when the current", ""},
		{"claude", "   Error: [provider.auth_error] 403 You've reached your", ""},
		{"claude", "❯ Why did it say Not logged in?", ""},
	} {
		reason, line, ok := dispatch.HarnessError(tc.kind, "⏺ Reading the brief.\n"+tc.line+"\n\n❯ \n")
		if reason != tc.want || ok != (tc.want != "") || (ok && line != strings.TrimSpace(tc.line)) {
			t.Errorf("%s: %q: reason %q line %q, want %q", tc.kind, tc.line, reason, line, tc.want)
		}
	}
	const museStop = "◆ Usage limit reached\n│ This turn stopped because your usage limit was reached."
	for _, tc := range []struct{ name, kind, pane, want string }{
		{"muse took a continuation", "muse", museStop + "\n❯ continue\n◆ Work completed\n❯ ", ""},
		{"muse stopped again", "muse", museStop + "\n❯ continue\n◆ Work completed\n│ Usage limit reached\n❯ ", "│ Usage limit reached"},
		{"muse input draft", "muse", museStop + "\n❯ half a thought", "│ This turn stopped because your usage limit was reached."},
		{"muse input hint", "muse", museStop + "\n❯ Send a message", "│ This turn stopped because your usage limit was reached."},
		{"muse empty message", "muse", museStop + "\n❯ \n◆ Work completed\n❯ ", "│ This turn stopped because your usage limit was reached."},
		{"claude took a continuation", "claude", "⎿ You've hit your limit · resets 3pm\n❯ continue\n⏺ Work completed\n❯ ", ""},
		{"codex took a continuation", "codex", "■ You've hit your usage limit.\n› continue\n• Work completed\n› ", ""},
		{"kimi took a continuation", "kimi", "Error: [provider.rate_limit] 429 Too many requests\n✨ continue\n● Work completed\n> ", ""},
		{"pi has no message mark", "pi", "Error: Retry failed after 3 attempts: 529:\n❯ continue\nWork completed\n❯ ", "Error: Retry failed after 3 attempts: 529:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reason, line, ok := dispatch.HarnessError(tc.kind, tc.pane)
			wantReason := dispatch.ReasonRateLimited
			if tc.want == "" {
				wantReason = ""
			} else if tc.kind == "pi" {
				wantReason = dispatch.ReasonOther
			}
			if reason != wantReason || line != tc.want || ok != (tc.want != "") {
				t.Errorf("reason %q line %q found %t, want %q %q %t", reason, line, ok, wantReason, tc.want, tc.want != "")
			}
		})
	}
	// Only the pane's end counts: what the agent quoted while it still
	// worked is further up.
	old := "■ Selected model is at capacity. Please try a different model.\n" + strings.Repeat("• Edited a file\n", 20)
	if reason, _, ok := dispatch.HarnessError("codex", old); ok {
		t.Errorf("an error line 21 lines up was taken for what the harness stopped on: %s", reason)
	}
	// Kimi's, broken by Kimi into lines of the pane's width, begins 12
	// lines from the end of a pane of 60 columns, and further up in a
	// narrower one.
	if reason, line, ok := dispatch.HarnessError("kimi", kimiLimit); reason != dispatch.ReasonRateLimited || !ok ||
		line != "Error: [provider.auth_error] 403 You've reached your" {
		t.Errorf("kimi's usage limit: reason %q line %q", reason, line)
	}
	narrow := strings.Replace(kimiLimit, " or upgrade your plan:\n", " or upgrade\n your plan:\n", 1)
	narrow = strings.Replace(narrow, " file with us for diagnosis.", " file with us\n for diagnosis.", 1)
	if reason, _, _ := dispatch.HarnessError("kimi", narrow); reason != dispatch.ReasonRateLimited {
		t.Errorf("kimi's usage limit 14 lines up: reason %q", reason)
	}
	// pi's, in the pane it was drawn in: broken at the pane's width after
	// "529:", below the error lines of the attempts that failed before it,
	// which are the provider's words and end no turn on their own. Those
	// alone, or a pane with nothing wrong, are no stop.
	if reason, line, ok := dispatch.HarnessError("pi", piRetryFailed); reason != dispatch.ReasonOther || !ok ||
		line != "Error: Retry failed after 3 attempts: 529:" {
		t.Errorf("pi's failed turn: reason %q line %q", reason, line)
	}
	for name, pane := range map[string]string{"attempts only": piAttempts + piBox, "nothing wrong": piIdle} {
		if reason, line, ok := dispatch.HarnessError("pi", pane); ok {
			t.Errorf("pi, %s: taken for a stop: reason %q line %q", name, reason, line)
		}
	}
}

// Precision proof for the shared, provider-agnostic network error patterns:
// positive error lines covering every shared entry across all harnesses
// (including harnesses with no per-kind network lines and failures wrapped in
// generic other catch-alls), precedence over 401/429/529/auth/quota/capacity
// and non-gateway 5xx/numbers, and negative lines of ordinary agent prose and
// unsent drafts that must not match.
func TestSharedNetworkErrorPrecision(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, kind, line, want string
	}{
		// Positive: OS / Node / getaddrinfo errno codes on error lines across harnesses.
		{"grok ECONNRESET", "grok", "Error: read ECONNRESET", dispatch.ReasonNetwork},
		{"kiro ECONNREFUSED", "kiro", "Error: connect ECONNREFUSED 127.0.0.1:443", dispatch.ReasonNetwork},
		{"opencode ETIMEDOUT", "opencode", "Error: connect ETIMEDOUT 10.0.0.1:443", dispatch.ReasonNetwork},
		{"pi ECONNABORTED", "pi", " Error: Retry failed after 3 attempts: read ECONNABORTED", dispatch.ReasonNetwork},
		{"opencode EPIPE", "opencode", "Error: write EPIPE", dispatch.ReasonNetwork},
		{"kimi ENOTFOUND", "kimi", "   Error: getaddrinfo ENOTFOUND api.kimi.com", dispatch.ReasonNetwork},
		{"claude EAI_AGAIN", "claude", "● API Error: getaddrinfo EAI_AGAIN api.anthropic.com", dispatch.ReasonNetwork},
		{"codex ENETUNREACH", "codex", "■ stream error: connect ENETUNREACH", dispatch.ReasonNetwork},
		{"muse EHOSTUNREACH", "muse", "◆ model failed: connect EHOSTUNREACH 192.0.2.1:443", dispatch.ReasonNetwork},

		// Positive: Node socket hang up & Go net/http timeouts on error lines.
		{"opencode socket hang up", "opencode", "Error: socket hang up", dispatch.ReasonNetwork},
		{"claude socket hang up", "claude", "● API Error: socket hang up", dispatch.ReasonNetwork},
		{"grok TLS handshake timeout", "grok", "Error: net/http: TLS handshake timeout", dispatch.ReasonNetwork},
		{"kiro i/o timeout", "kiro", "Error: read tcp 10.0.0.2:51234->10.0.0.1:443: i/o timeout", dispatch.ReasonNetwork},

		// Positive: keyword pairs (connection|network|stream|socket) + transport verbs on error lines.
		{"muse stream connection reset", "muse", "◆ stream error: connection reset by peer", dispatch.ReasonNetwork},
		{"opencode connection refused", "opencode", "Error: connection refused", dispatch.ReasonNetwork},
		{"grok connection dropped", "grok", "Error: Connection dropped during response", dispatch.ReasonNetwork},
		{"kiro connection lost", "kiro", "Error: Connection lost while streaming", dispatch.ReasonNetwork},
		{"pi connection timed out", "pi", " Error: Retry failed after 3 attempts: connection timed out", dispatch.ReasonNetwork},
		{"opencode network unreachable", "opencode", "Error: Network is unreachable", dispatch.ReasonNetwork},
		{"kimi stream disconnected", "kimi", "   Error: stream disconnected unexpectedly", dispatch.ReasonNetwork},
		{"codex socket disconnected", "codex", "■ socket disconnected before response", dispatch.ReasonNetwork},

		// Positive: no route to host & unexpected connection close on error lines.
		{"grok no route to host", "grok", "Error: No route to host", dispatch.ReasonNetwork},
		{"kiro connection closed unexpectedly", "kiro", "error: connection closed unexpectedly", dispatch.ReasonNetwork},
		{"codex connection closed before message", "codex", "■ stream error: connection closed before message completed", dispatch.ReasonNetwork},
		{"claude connection closed by peer", "claude", "● API Error: connection closed by peer", dispatch.ReasonNetwork},

		// Positive: SDK/harness connection & network errors on error lines.
		{"claude API connection error", "claude", "● API Error: Connection error.", dispatch.ReasonNetwork},
		{"claude API request timed out", "claude", "● API Error: Request timed out.", dispatch.ReasonNetwork},
		{"opencode network error", "opencode", "Error: Network error", dispatch.ReasonNetwork},
		{"grok fetch failed", "grok", "TypeError: fetch failed", dispatch.ReasonNetwork},
		{"opencode network request failed", "opencode", "Error: network request failed", dispatch.ReasonNetwork},
		{"opencode bare fetch failed", "opencode", "fetch failed", dispatch.ReasonNetwork},
		{"grok request failed ECONNRESET", "grok", "request failed: read ECONNRESET", dispatch.ReasonNetwork},
		{"claude subline connection error", "claude", "  ⎿  Connection error.", dispatch.ReasonNetwork},

		// Positive: HTTP 502 / 504 gateway status phrases on error lines.
		{"codex 502 Bad Gateway", "codex", "■ exceeded retry limit, last status: 502 Bad Gateway", dispatch.ReasonNetwork},
		{"codex 504 status", "codex", "■ unexpected status 504 Gateway Timeout", dispatch.ReasonNetwork},
		{"claude API 502 Bad Gateway", "claude", "● API Error: 502 Bad Gateway", dispatch.ReasonNetwork},
		{"pi retry 504", "pi", " Error: Retry failed after 3 attempts: 504 Gateway Time-out", dispatch.ReasonNetwork},
		{"opencode 502 Bad Gateway", "opencode", "Error: 502 Bad Gateway", dispatch.ReasonNetwork},

		// No collisions: auth, quota, rate-limit, capacity, non-gateway 5xx and bare 502/504 keep their own reason.
		{"claude 401 auth", "claude", "● API Error: 401 {\"type\":\"error\",\"error\":{\"type\":\"authentication_error\"}} · Please run /login", dispatch.ReasonNotLoggedIn},
		{"claude 429 rate limit", "claude", "● API Error: 429 {\"type\":\"error\",\"error\":{\"type\":\"rate_limit_error\"}}", dispatch.ReasonRateLimited},
		{"claude 529 capacity", "claude", "● API Error: 529 {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}", dispatch.ReasonAtCapacity},
		{"claude 500 internal", "claude", "● API Error: 500 {\"type\":\"error\",\"error\":{\"type\":\"api_error\"}}", dispatch.ReasonOther},
		{"claude bare 502 json", "claude", "● API Error: 502 {\"type\":\"error\",\"message\":\"bad gateway\"}", dispatch.ReasonOther},
		{"codex 401 auth", "codex", "■ exceeded retry limit, last status: 401 Unauthorized", dispatch.ReasonNotLoggedIn},
		{"codex 429 rate limit", "codex", "■ exceeded retry limit, last status: 429 Too Many Requests", dispatch.ReasonRateLimited},
		{"codex 503 service unavailable", "codex", "■ unexpected status 503 Service Unavailable", dispatch.ReasonOther},
		{"codex 503 with 502 ms", "codex", "■ unexpected status 503 Service Unavailable (request duration: 502 ms)", dispatch.ReasonOther},
		{"codex 503 connection error", "codex", "■ unexpected status 503 Service Unavailable: connection error", dispatch.ReasonOther},
		{"codex 503 upstream connect error", "codex", "■ unexpected status 503 Service Unavailable: upstream connect error; connection refused", dispatch.ReasonOther},
		{"codex 529 overloaded connection error", "codex", "■ exceeded retry limit, last status: 529 Overloaded: connection error", dispatch.ReasonOther},
		{"codex quota exceeded", "codex", "■ Quota exceeded", dispatch.ReasonOther},
		{"kimi 403 quota", "kimi", "   Error: [provider.auth_error] 403 You've reached your", dispatch.ReasonRateLimited},
		{"kimi 529 overloaded", "kimi", "   Error: [provider.overloaded] 529 The engine is", dispatch.ReasonAtCapacity},
		{"pi 529 retry failed", "pi", " Error: Retry failed after 3 attempts: 529: {\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}", dispatch.ReasonOther},
		{"pi 503 connection error", "pi", " Error: Retry failed after 3 attempts: 503 Service Unavailable: connection error", dispatch.ReasonOther},
		{"opencode 429 connection reset", "opencode", "Error: 429 Too Many Requests: connection reset", ""},
		{"grok 403 connection refused", "grok", "Error: HTTP/1.1 403 Forbidden: connection refused", ""},
		{"opencode 500 connection error", "opencode", "Error: status 500: connection error", ""},
		{"opencode syntax error line 502", "opencode", "Error: syntax error at line 502", ""},
		{"opencode expected 504 got 503", "opencode", "Error: expected 504 records, got 503", ""},
		{"opencode 503 service unavailable", "opencode", "Error: 503 Service Unavailable", ""},

		// Negative: ordinary agent prose and non-error lines mentioning errno, transport pairs, or gateway phrases.
		{"prose network connection", "claude", "● I checked the network connection and confirmed TLS works.", ""},
		{"prose claude errno", "claude", "● Added a regression test for ECONNRESET handling.", ""},
		{"prose claude gateway quote", "claude", "● Confirmed the proxy returns 502 Bad Gateway in the integration test.", ""},
		{"prose claude connection error handling", "claude", "● Connection error handling was fixed.", ""},
		{"prose network handler dropped", "codex", "• The network layer drops duplicate packets cleanly.", ""},
		{"prose codex connection reset", "codex", "• Fixed the connection reset bug.", ""},
		{"prose reset connection pool", "grok", "I reset the connection pool before running the suite.", ""},
		{"prose kiro unmarked connection reset", "kiro", "The connection was reset before the operation began, but the test passed.", ""},
		{"prose kiro bullet connection reset", "kiro", "• The connection was reset before the operation began, but the test passed.", ""},
		{"prose connection closed cleanly", "kiro", "• The connection closed cleanly after the test finished.", ""},
		{"prose stream parser disconnected", "opencode", "The stream parser disconnected the mock reader during teardown.", ""},
		{"prose bare 502 Bad Gateway", "opencode", "502 Bad Gateway", ""},
		{"prose socket buffer reset", "muse", "◆ The socket buffer was reset between frames.", ""},
		{"prose network error handling", "claude", "● Added a regression test for network error retries.", ""},
		{"prose issue 502", "codex", "• Fixed bug 502 in the proxy handler and 504 in the timeout check.", ""},
		{"prose fetch failed in test", "pi", " Confirmed the fetch failed assertion passes when offline.", ""},
		{"prose 529 reference", "claude", "● The session limit reached its end; see the API Error: 529 above.", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reason, line, ok := dispatch.HarnessError(tc.kind, "⏺ Reading the brief.\n"+tc.line+"\n\n❯ \n")
			if reason != tc.want || ok != (tc.want != "") || (ok && line != strings.TrimSpace(tc.line)) {
				t.Errorf("%s (%s): %q -> reason %q line %q ok %v, want %q", tc.name, tc.kind, tc.line, reason, line, ok, tc.want)
			}
		})
	}
	// An unsent draft in the input line never counts as a harness error, even
	// when its text contains an errno code or error lead.
	for _, tc := range []struct{ name, kind, tail string }{
		{"claude draft errno", "claude", "❯ Please fix ECONNRESET handling"},
		{"claude draft error lead", "claude", "❯ Error: read ECONNRESET"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if reason, line, ok := dispatch.HarnessError(tc.kind, tc.tail); ok || reason != "" || line != "" {
				t.Errorf("%s: got reason=%q line=%q ok=%v, want none", tc.name, reason, line, ok)
			}
		})
	}
}

// Dispatcher-level regression: ordinary agent prose or non-gateway diagnostics
// mentioning transport words or 502/504 numbers must never schedule or emit a
// network retry or send a resume prompt.
func TestNoNetworkRetryForOrdinaryOutput(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, kind, tail string
		wantErrorEvents  int
	}{
		{"codex prose", "codex", "• Fixed the connection reset bug.\n› ", 0},
		{"claude prose", "claude", "● Added a regression test for ECONNRESET handling.\n❯ ", 0},
		{"kiro prose", "kiro", "The connection was reset before the operation began, but the test passed.\n› ", 0},
		{"opencode syntax error", "opencode", "Error: syntax error at line 502", 0},
		{"codex 503 with 502 ms", "codex", "■ unexpected status 503 Service Unavailable (request duration: 502 ms)\n› ", 1},
		{"codex 503 connection error", "codex", "■ unexpected status 503 Service Unavailable: connection error\n› ", 1},
		{"codex 503 upstream connect error", "codex", "■ unexpected status 503 Service Unavailable: upstream connect error; connection refused\n› ", 1},
		{"codex 529 overloaded connection error", "codex", "■ exceeded retry limit, last status: 529 Overloaded: connection error\n› ", 1},
		{"pi 503 connection error", "pi", "Error: Retry failed after 3 attempts: 503 Service Unavailable: connection error\n❯ ", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, r, d, b, at := retrySetupKind(t, tc.kind, tc.tail)
			t.Cleanup(func() { dispatch.WaitLimitsIdle(d) })
			at(5*time.Minute + time.Second)
			must(t, d.Reconcile(context.Background()))
			retries := eventKinds(t, s, b.ID, "session_harness_retry")
			errs := eventKinds(t, s, b.ID, "session_harness_error")
			if len(r.nudges) != 0 || len(retries) != 0 || len(errs) != tc.wantErrorEvents {
				t.Errorf("prompts=%q retries=%d errs=%d; want 0 prompts, 0 retries, %d error events",
					r.nudges, len(retries), len(errs), tc.wantErrorEvents)
			}
			if tc.wantErrorEvents == 1 && len(errs) == 1 && errs[0].Data["reason"] != dispatch.ReasonOther {
				t.Errorf("error reason = %v, want %q", errs[0].Data["reason"], dispatch.ReasonOther)
			}
		})
	}
}

// The pane of the crew member brakeman (kimi, 60 columns) as herdr read it
// on 6 Oct 2026: its harness had stopped on its usage limit half an hour
// before, with a review waiting, and nobody had heard (yardr-r9sh). Kimi
// broke the error into these lines itself.
const kimiLimit = " ● Used Read (…dr/internal/store/yardrsource.go) · 130 lines\n" +
	"   Error: [provider.auth_error] 403 You've reached your\n" +
	" 5-hour usage limit. Your quota will reset when the current\n" +
	" 5-hour window ends. To continue now, purchase extra usage\n" +
	" or upgrade your plan:\n" +
	" https://www.kimi.com/membership/subscription?tab=quota\n" +
	"   If this persists, run `/export-debug-zip` and share the\n" +
	" file with us for diagnosis. Please don't share it publicly.\n" +
	" ╭──────────────────────────────────────────────────────────╮\n" +
	" │ >                                                        │\n" +
	" ╰──────────────────────────────────────────────────────────╯\n" +
	" Ask When Needed  K3 thinking: high  ~         ctrl+o expand\n" +
	"                                      context: 20% (198k/1M)"

// The pane of a builder (kimi, 60 columns) on 11 Oct 2026, which stopped on
// its usage limit mid-work and of which nobody heard for three and a half
// hours (yardr-re0h): Kimi draws the list of what the agent still meant to
// do under the error, above its input. The line was not what was missed:
// herdr called the agent working, so its pane was never read (see the herdr
// runner's stopped).
const kimiLimitTodo = "       Error: [provider.auth_error] 403 You've reached your\n" +
	"     5-hour usage limit. Your quota will reset when the current\n" +
	"     5-hour window ends. To continue now, purchase extra usage or\n" +
	"     upgrade your plan:\n" +
	"     https://www.kimi.com/membership/subscription?tab=quota\n" +
	"       If this persists, run `/export-debug-zip` and share the\n" +
	"     file with us for diagnosis. Please don't share it publicly.\n" +
	"\n" +
	"     ────────────────────────────────────────────────────────────\n" +
	"       Todo\n" +
	"       ✓ internal/source: runner + command files (command.go)\n" +
	"       ● declaration.go: thread/link keys + refusals\n" +
	"       ○ limits: use source runner, resolve program\n" +
	"       … +3 more (1 done · 2 pending) · ctrl+t to expand\n" +
	"     ╭──────────────────────────────────────────────────────────╮\n" +
	"     │ >                                                        │\n" +
	"     ╰──────────────────────────────────────────────────────────╯\n"

func TestKimiUsageLimitAboveItsTodoPanelIsFound(t *testing.T) {
	t.Parallel()
	if reason, line, ok := dispatch.HarnessError("kimi", kimiLimitTodo); reason != dispatch.ReasonRateLimited || !ok ||
		line != "Error: [provider.auth_error] 403 You've reached your" {
		t.Errorf("kimi's usage limit above its Todo panel: reason %q line %q", reason, line)
	}
}

// kimiIdle is such a pane with nothing wrong in it.
const kimiIdle = " ● The review is noted on the bead.\n" +
	" ╭──────────────────────────────────────────────────────────╮\n" +
	" │ >                                                        │\n" +
	" ╰──────────────────────────────────────────────────────────╯\n" +
	" Ask When Needed  K3 thinking: high  ~         ctrl+o expand"

// A bead's session of kind kimi that stops on its usage limit is told of as
// claude's and codex's are.
func TestKimiSessionAtItsUsageLimitIsReported(t *testing.T) {
	t.Parallel()
	s, r, d, b := harnessSetup(t, "kimi", nil)
	r.show("idle", kimiLimit)
	must(t, d.Reconcile(context.Background()))
	evs := eventKinds(t, s, b.ID, "session_harness_error")
	if len(evs) != 1 || evs[0].Data["reason"] != dispatch.ReasonRateLimited || evs[0].Data["kind"] != "kimi" ||
		evs[0].Data["line"] != "Error: [provider.auth_error] 403 You've reached your" {
		t.Fatalf("session_harness_error = %v, want one, rate_limited, with kimi's line", evs)
	}
}

// The pane of a builder (muse) on 17 Oct 2026, which stopped on a failed
// turn and of which nobody heard for four hours (yardr-thet): the model
// answered nothing, with a request id to find it by. Muse's own message
// begins with ◆, which the match takes off with the other error marks.
const museServerError = "◆ model failed: server_error: The model failed to generate a response. (request id:\n" +
	" 688dfffa-1833-4175-96d6-ae6513c47149, response: resp_6ad2e591f2d29e424980460b)\n" +
	"── Voice input (alt(option)+v to start) ──…\n" +
	"❯ \n" +
	"───…\n" +
	"    muse-spark-1.3 · max · /private/tmp/lyr0/work · Auto-review"

// The pane of pi 0.84.3 (60 columns) whose provider answered every request
// with an overload error, recorded against a stand-in provider on 4 Nov 2026.
// Each failed attempt drew the provider's message as an error line of its
// own, and with the retries used up pi drew the line it gives up with and
// waited for input. It breaks a line at the pane's width itself, after
// "529:" here, and draws a message after one space.
const (
	piAttempt = " Error: 529:\n" +
		" {\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}\n" +
		"\n"
	piAttempts = " say hello\n" +
		"\n" +
		"\n" +
		piAttempt + piAttempt + piAttempt + piAttempt
	piBox = "────────────────────────────────────────────────────────────\n" +
		"\n" +
		"────────────────────────────────────────────────────────────\n" +
		"/tmp/work\n" +
		"0.0%/128k (auto)                                          m1"
	piRetryFailed = piAttempts +
		" Error: Retry failed after 3 attempts: 529:\n" +
		" {\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}\n" +
		"\n" +
		piBox
	// piIdle is the same pane with nothing wrong in it: the agent answered.
	piIdle = " say hello\n" +
		"\n" +
		" Nothing in my inbox needs a reply.\n" +
		"\n" +
		piBox
)

// A bead's session of kind muse that stops on a failed turn is told of as
// claude's, codex's and kimi's are.
func TestMuseSessionOnAFailedTurnIsReported(t *testing.T) {
	t.Parallel()
	s, r, d, b := harnessSetup(t, "muse", nil)
	r.show("idle", museServerError)
	must(t, d.Reconcile(context.Background()))
	evs := eventKinds(t, s, b.ID, "session_harness_error")
	if len(evs) != 1 || evs[0].Data["reason"] != dispatch.ReasonOther || evs[0].Data["kind"] != "muse" ||
		evs[0].Data["line"] != "◆ model failed: server_error: The model failed to generate a response. (request id:" {
		t.Fatalf("session_harness_error = %v, want one, other, with muse's line", evs)
	}
}

// A crew member has no idle limit, so one whose harness stopped on an error
// was never told of. Its pane is read when it is idle with its inbox
// unread, and the error raises session_harness_error on its journal, with
// the crew member named, to whoever hears that kind: once while it stands.
func TestCrewMemberAtItsUsageLimitIsReported(t *testing.T) {
	t.Parallel()
	s, f, d := crewSetup(t, store.CrewMember{Config: map[string]any{"kind": "kimi"}})
	ctx := context.Background()
	// Who hears the kind; its runner is none of the dispatcher's, so it is
	// an inbox and no session.
	_, err := s.PutCrew(ctx, store.CrewMember{Name: "ears", Runner: "none", On: []string{"session_harness_error"}}, "me")
	must(t, err)
	must(t, d.Reconcile(ctx))
	must(t, d.Reconcile(ctx))
	agent := agentOf(t, s)
	member := func() *store.CrewMember {
		t.Helper()
		r, err := s.GetCrew(ctx, "yardmaster")
		must(t, err)
		return r
	}
	told := func() []store.Event { t.Helper(); return eventKinds(t, s, member().Journal, "session_harness_error") }
	pass := func(want int, when string) {
		t.Helper()
		must(t, d.Reconcile(ctx))
		if evs := told(); len(evs) != want {
			t.Fatalf("%s: session_harness_error events = %d, want %d: %v", when, len(evs), want, evs)
		}
	}

	// Idle with nothing in its inbox is what a crew member mostly is: its
	// pane is not asked, whatever it ends in.
	f.show(kimiIdle)
	pass(0, "idle, an empty inbox, no error")
	f.show(kimiLimit)
	pass(0, "idle, an empty inbox")
	// Idle with mail and no error is nudged, and that is all.
	f.show(kimiIdle)
	must(t, s.Mail(ctx, "yardmaster", "review yardr-1my8", "chris"))
	pass(0, "idle, mail, no error")
	if len(f.nudges) != 1 {
		t.Fatalf("nudges = %v, want the inbox's", f.nudges)
	}

	// The nudge is answered with the usage limit.
	f.show(kimiLimit)
	pass(1, "idle, mail, the usage limit")
	data := told()[0].Data
	if data["crew"] != "yardmaster" || data["kind"] != "kimi" || data["reason"] != dispatch.ReasonRateLimited ||
		data["line"] != "Error: [provider.auth_error] 403 You've reached your" || data["tail"] != kimiLimit ||
		!strings.Contains(data["hint"].(string), "yardr crew restart yardmaster") {
		t.Fatalf("session_harness_error data = %v", data)
	}
	if member().Handle[dispatch.HarnessErrorKey] != dispatch.ReasonRateLimited {
		t.Fatalf("handle = %v, want the reason kept on it", member().Handle)
	}
	// It reaches who hears the kind, as a session's does.
	page, err := s.Inbox(ctx, "ears", 0)
	must(t, err)
	if len(page.Events) != 1 || page.Events[0].Kind != "session_harness_error" {
		t.Fatalf("inbox of who hears session_harness_error = %v, want the event", page.Events)
	}
	// A stopped harness answers what it is typed by working for a moment
	// and stopping on the same error: that is not told again. Mail that
	// comes while the error stands is not nudged, since a nudge would only
	// start the round that failed again.
	f.set(agent, "working")
	pass(1, "working for a moment")
	f.set(agent, "idle")
	pass(1, "stopped on the same error")
	must(t, s.Mail(ctx, "yardmaster", "are you there?", "chris"))
	pass(1, "more mail")
	pass(1, "more mail, a pass later")
	if len(f.nudges) != 1 {
		t.Fatalf("nudges = %v, want the first mail's alone: none while the error stands", f.nudges)
	}

	// A daemon that replaced itself reads the mark and does not tell twice.
	d2 := &dispatch.Dispatcher{Store: s, Runners: map[string]dispatch.Runner{"fake": f}, PromptSettle: -1, PromptWait: -1}
	must(t, d2.Reconcile(ctx))
	if n := len(told()); n != 1 {
		t.Fatalf("session_harness_error events = %d after a new daemon's pass, want 1", n)
	}

	// A pane seen without the error ends it: the next one is told.
	f.show(kimiIdle)
	pass(1, "got going again")
	if mark := member().Handle[dispatch.HarnessErrorKey]; mark != nil {
		t.Fatalf("handle keeps %v with the error gone from the pane", mark)
	}
	if len(f.nudges) != 2 || !strings.Contains(f.nudges[1], "2 new in your inbox") {
		t.Fatalf("nudges = %v, want the mail held back nudged once the error is gone", f.nudges)
	}
	f.show(kimiLimit)
	pass(2, "the usage limit again")
	// So does an inbox that was handled.
	mine, err := s.Inbox(ctx, "yardmaster", 0)
	must(t, err)
	must(t, s.AckInbox(ctx, "yardmaster", mine.Last))
	pass(2, "the inbox handled")
	if mark := member().Handle[dispatch.HarnessErrorKey]; mark != nil {
		t.Fatalf("handle keeps %v with the inbox handled", mark)
	}
}

// A pi crew member whose provider kept failing sits idle at the line pi gives
// up with, and a nudge would start the round that failed again. The line
// raises session_harness_error, once, and no nudge is typed while it ends the
// pane, however much mail comes: the items stay unread, and the pass that
// finds the pane without the line types the one nudge for them all, however
// long after the spell began.
func TestCrewMemberAtAFailedTurnIsNotNudged(t *testing.T) {
	t.Parallel()
	s, f, d := crewSetup(t, store.CrewMember{Config: map[string]any{"kind": "pi"}})
	ctx := context.Background()
	now := time.Now()
	dispatch.SetClock(d, func() time.Time { return now })
	must(t, d.Reconcile(ctx))
	must(t, d.Reconcile(ctx))
	agent := agentOf(t, s)
	member := func() *store.CrewMember {
		t.Helper()
		r, err := s.GetCrew(ctx, "yardmaster")
		must(t, err)
		return r
	}
	told := func() []store.Event { t.Helper(); return eventKinds(t, s, member().Journal, "session_harness_error") }
	pass := func(when string, wantTold, wantNudges int) {
		t.Helper()
		must(t, d.Reconcile(ctx))
		if evs := told(); len(evs) != wantTold || len(f.nudges) != wantNudges {
			t.Fatalf("%s: session_harness_error events = %d, nudges = %q; want %d and %d", when, len(evs), f.nudges, wantTold, wantNudges)
		}
	}

	// Mail arrives for a pi whose pane ends in the line: told, not nudged.
	f.show(piRetryFailed)
	must(t, s.Mail(ctx, "yardmaster", "review yardr-1my8", "chris"))
	pass("idle at the line, one unread", 1, 0)
	data := told()[0].Data
	if data["crew"] != "yardmaster" || data["kind"] != "pi" || data["reason"] != dispatch.ReasonOther ||
		data["line"] != "Error: Retry failed after 3 attempts: 529:" || data["tail"] != piRetryFailed ||
		!strings.Contains(data["hint"].(string), "yardr crew restart yardmaster") {
		t.Fatalf("session_harness_error data = %v", data)
	}
	if member().Handle[dispatch.HarnessErrorKey] != dispatch.ReasonOther {
		t.Fatalf("handle = %v, want the reason kept on it", member().Handle)
	}

	// More comes, passes go by, and a person gets the agent to work for a
	// moment, which ends at the same line: not told again, not nudged.
	must(t, s.Mail(ctx, "yardmaster", "are you there?", "chris"))
	pass("a second item", 1, 0)
	pass("a pass later", 1, 0)
	f.set(agent, "working")
	pass("working for a moment", 1, 0)
	f.set(agent, "idle")
	pass("stopped at the same line", 1, 0)

	// Long past the first minutes of the spell nothing has changed, and the
	// pane is looked at again on every pass: the line gone from it is seen
	// on the first pass after, and the mail is nudged once, for both items.
	now = now.Add(time.Hour)
	pass("an hour on", 1, 0)
	f.show(piIdle)
	pass("the pane clear", 1, 1)
	if !strings.Contains(f.nudges[0], "2 new in your inbox") {
		t.Fatalf("nudge = %q, want one for the two unread", f.nudges[0])
	}
	if mark := member().Handle[dispatch.HarnessErrorKey]; mark != nil {
		t.Fatalf("handle keeps %v with the line gone from the pane", mark)
	}
	pass("nudged already", 1, 1)

	// Nudged as before from then on, and a failure after it is told anew.
	must(t, s.Mail(ctx, "yardmaster", "and one more", "chris"))
	pass("more mail, the pane clear", 1, 2)
	f.show(piRetryFailed)
	pass("the line again under the nudge", 2, 2)
	must(t, s.Mail(ctx, "yardmaster", "last one", "chris"))
	pass("more mail, the line again", 2, 2)

	// What it was nudged about and left unread is nudged again after five
	// minutes (yardr-rx51), and not while its harness stands stopped: the
	// nudge said again would start the round that failed again as the first
	// did. It is typed on the pass that finds the line gone.
	f.show(piIdle)
	pass("the line gone", 2, 3)
	f.show(piRetryFailed)
	pass("the line under that nudge", 3, 3)
	now = now.Add(6 * time.Minute)
	pass("six minutes on, the line standing", 3, 3)
	f.show(piIdle)
	pass("the line gone again", 3, 4)
}

// A crew member idle with unread mail stopped on a dropped network
// connection, where the harness asks for the message to be sent again, is
// tried again as a session is: the resume prompt, five minutes apart, up to
// three times, then session_harness_error with tries.
func TestCrewMemberAtNetworkErrorIsRetriedThenRaised(t *testing.T) {
	t.Parallel()
	const dropped = "● API Error: Connection dropped (ECONNRESET). A VPN, firewall, or security software can cause this. Check those\n" +
		"────────────────────────────────────────\n❯ Resend your message to try again\n────────────────────────────────────────\n" +
		"  ⏵⏵ auto mode on · ← 1 agent · ↓ to manage"
	const resume = "yardr: your harness stopped on a dropped network connection. Go on where you stopped."
	s, f, d := crewSetup(t, store.CrewMember{Config: map[string]any{"kind": "claude"}})
	ctx := context.Background()
	start := time.Now()
	now := start
	dispatch.SetClock(d, func() time.Time { return now })
	must(t, d.Reconcile(ctx))
	must(t, d.Reconcile(ctx))
	agent := agentOf(t, s)
	member := func() *store.CrewMember {
		t.Helper()
		r, err := s.GetCrew(ctx, "yardmaster")
		must(t, err)
		return r
	}
	told := func(kind string) []store.Event { t.Helper(); return eventKinds(t, s, member().Journal, kind) }
	f.show(dropped)
	must(t, s.Mail(ctx, "yardmaster", "review yardr-1my8", "chris"))
	must(t, d.Reconcile(ctx))
	if len(told("session_harness_error")) != 0 || len(told("session_harness_retry")) != 0 {
		t.Fatalf("raised or tried on the first sight: error %d, retry %d",
			len(told("session_harness_error")), len(told("session_harness_retry")))
	}
	if len(f.nudges) != 0 {
		t.Fatalf("nudges = %q, want none while the network try waits", f.nudges)
	}

	for i, min := range []int{5, 10, 15} {
		now = start.Add(time.Duration(min)*time.Minute + time.Second)
		must(t, d.Reconcile(ctx))
		must(t, d.Reconcile(ctx))
		if want := i + 1; len(told("session_harness_retry")) != want {
			t.Fatalf("after %dm: %d tries, want %d", min, len(told("session_harness_retry")), want)
		}
		if n := len(told("session_harness_error")); n != 0 {
			t.Fatalf("after %dm: session_harness_error raised with a try still to make or to wait for", min)
		}
		if want := agent + ": " + resume; f.nudges[len(f.nudges)-1] != want {
			t.Fatalf("nudges = %q, want the last to be the resume prompt", f.nudges)
		}
	}

	now = start.Add(20*time.Minute + 3*time.Second)
	must(t, d.Reconcile(ctx))
	must(t, d.Reconcile(ctx))
	evs := told("session_harness_error")
	if len(evs) != 1 {
		t.Fatalf("session_harness_error events = %d, want 1 once the tries are used up", len(evs))
	}
	data := evs[0].Data
	if data["crew"] != "yardmaster" || data["kind"] != "claude" || data["reason"] != dispatch.ReasonNetwork ||
		data["line"] != "● API Error: Connection dropped (ECONNRESET). A VPN, firewall, or security software can cause this. Check those" ||
		data["tries"] != float64(dispatch.HarnessRetries) ||
		!strings.Contains(data["hint"].(string), "prompted the agent again 3 times") {
		t.Fatalf("session_harness_error data = %v", data)
	}
	if member().Handle[dispatch.HarnessErrorKey] != dispatch.ReasonNetwork {
		t.Fatalf("handle = %v, want the reason kept on it", member().Handle)
	}
	if n := len(told("session_harness_retry")); n != dispatch.HarnessRetries {
		t.Fatalf("session_harness_retry events = %d, want %d", n, dispatch.HarnessRetries)
	}
}

// The pane is read at the start of a spell and after a nudge, not for as
// long as a crew member leaves its inbox unread: an error drawn into it
// under a nudge long after is still seen.
func TestCrewMemberPaneIsReadAgainAfterANudge(t *testing.T) {
	t.Parallel()
	s, f, d := crewSetup(t, store.CrewMember{Config: map[string]any{"kind": "kimi"}})
	ctx := context.Background()
	now := time.Now()
	dispatch.SetClock(d, func() time.Time { return now })
	must(t, d.Reconcile(ctx))
	must(t, d.Reconcile(ctx))
	r, err := s.GetCrew(ctx, "yardmaster")
	must(t, err)
	told := func() int { t.Helper(); return len(eventKinds(t, s, r.Journal, "session_harness_error")) }

	f.show(kimiIdle)
	must(t, s.Mail(ctx, "yardmaster", "one", "chris"))
	must(t, d.Reconcile(ctx)) // nudged, and it leaves the mail unread
	now = now.Add(time.Hour)
	f.show(kimiLimit)
	must(t, d.Reconcile(ctx))
	if n := told(); n != 0 {
		t.Fatalf("session_harness_error events = %d; the pane of a crew member idle for an hour was read", n)
	}
	f.show(kimiIdle)
	must(t, s.Mail(ctx, "yardmaster", "two", "chris"))
	must(t, d.Reconcile(ctx)) // nudged again
	f.show(kimiLimit)
	now = now.Add(time.Minute)
	must(t, d.Reconcile(ctx))
	if n := told(); n != 1 {
		t.Fatalf("session_harness_error events = %d, want 1 for the error under the nudge", n)
	}
}

// retryRunner is an agentRunner whose agents can be told their first prompt
// again: it knows the prompt, and keeps what was typed.
type retryRunner struct {
	*agentRunner
	nudges []string
	input  string // what is typed in the agent's input and not sent
}

func (n *retryRunner) FirstPrompt(map[string]any) string { return "Read brief.md and follow it." }

func (n *retryRunner) Nudge(_ context.Context, _ map[string]any, text string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.nudges = append(n.nudges, text)
	return nil
}

func (n *retryRunner) InputText(context.Context, map[string]any) (string, bool, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.input, true, nil
}

// retrySetup is a yard with one codex session whose agent answered its
// first prompt with tail and sits idle, having done nothing, and a clock
// the test moves: at(by) puts the dispatcher by after the start.
func retrySetup(t *testing.T, tail string) (s store.API, r *retryRunner, d *dispatch.Dispatcher, b store.Bead, at func(time.Duration)) {
	t.Helper()
	return retrySetupKind(t, "codex", tail)
}

// retrySetupKind is retrySetup for a session of kind.
func retrySetupKind(t *testing.T, kind, tail string) (s store.API, r *retryRunner, d *dispatch.Dispatcher, b store.Bead, at func(time.Duration)) {
	t.Helper()
	s = storetest.New(t)
	r = &retryRunner{agentRunner: &agentRunner{statusRunner: &statusRunner{fakeRunner: &fakeRunner{dead: map[string]bool{}},
		status: "idle"}, tail: tail}}
	d = &dispatch.Dispatcher{Store: s, Runners: map[string]dispatch.Runner{"fake": r}, Backoff: time.Hour}
	var skew time.Duration
	dispatch.SetClock(d, func() time.Time { return time.Now().Add(skew) })
	ctx := context.Background()
	must(t, s.PutGroup(ctx, store.Group{Name: "g", Runner: "fake", Config: map[string]any{"kind": kind}}, "me"))
	_, err := s.AddRoute(ctx, store.Route{Stage: "new", Group: "g"}, "me")
	must(t, err)
	bead, err := s.CreateBead(ctx, store.NewBead{Title: "x"}, "me")
	must(t, err)
	must(t, d.Reconcile(ctx)) // start
	must(t, d.Reconcile(ctx)) // seen idle, its pane read
	return s, r, d, *bead, func(by time.Duration) { skew = by }
}

// The 6 Oct case (yardr-f7aw): a harness that answers its first prompt with
// "at capacity" is given the prompt again by yardr, five minutes on. The
// agent gets going, and the yardmaster hears nothing: no
// session_harness_error, and no session_stalled for the wait either.
func TestAtCapacityIsPromptedAgain(t *testing.T) {
	t.Parallel()
	s, r, d, b, at := retrySetup(t, atCapacity)
	ctx := context.Background()
	tries := func() []store.Event { t.Helper(); return eventKinds(t, s, b.ID, "session_harness_retry") }
	quiet := func(when string) {
		t.Helper()
		for _, kind := range []string{"session_harness_error", "session_stalled"} {
			if evs := eventKinds(t, s, b.ID, kind); len(evs) != 0 {
				t.Fatalf("%s: %s = %v, want none", when, kind, evs[0].Data)
			}
		}
	}

	// The wait is not over: nothing is typed.
	at(4 * time.Minute)
	must(t, d.Reconcile(ctx))
	if len(tries()) != 0 || len(r.nudges) != 0 {
		t.Fatalf("after 4m: %d tries, typed %q; the wait is 5m", len(tries()), r.nudges)
	}
	quiet("in the wait")

	at(5*time.Minute + time.Second)
	must(t, d.Reconcile(ctx))
	must(t, d.Reconcile(ctx))
	evs := tries()
	if len(evs) != 1 || len(r.nudges) != 1 || r.nudges[0] != "Read brief.md and follow it." {
		t.Fatalf("after 5m: tries = %v, typed %q; want the first prompt given again once", evs, r.nudges)
	}
	got, _ := s.GetBead(ctx, b.ID)
	if data := evs[0].Data; data["try"] != float64(1) || data["of"] != float64(dispatch.HarnessRetries) ||
		data["reason"] != dispatch.ReasonAtCapacity || data["session"] != *got.Session || data["kind"] != "codex" ||
		data["line"] != "■ Selected model is at capacity. Please try a different model." {
		t.Fatalf("session_harness_retry data = %v", data)
	}

	// It works this time, and what it then stops for is its own business.
	r.show("working", "• Reading the brief")
	must(t, d.Reconcile(ctx))
	r.show("idle", "• Done. Reported the outcome.\n› Ask Codex to do anything")
	at(30 * time.Minute)
	must(t, d.Reconcile(ctx))
	must(t, d.Reconcile(ctx))
	if len(tries()) != 1 || len(r.nudges) != 1 {
		t.Fatalf("an agent that got going was prompted again: tries %d, typed %q", len(tries()), r.nudges)
	}
	quiet("after the try worked")
}

// The model stays at capacity: three tries, five minutes apart, each an
// event; then session_harness_error once, saying how often yardr tried. A
// daemon that replaces itself on the way neither starts the count again nor
// forgets the wait.
func TestAtCapacityIsRaisedWhenTheTriesAreUsedUp(t *testing.T) {
	t.Parallel()
	s, r, d, b, at := retrySetup(t, atCapacity)
	ctx := context.Background()
	count := func(kind string) int { t.Helper(); return len(eventKinds(t, s, b.ID, kind)) }

	at(5*time.Minute + time.Second)
	must(t, d.Reconcile(ctx))
	if count("session_harness_retry") != 1 {
		t.Fatalf("tries after 5m = %d, want 1", count("session_harness_retry"))
	}

	var skew time.Duration
	d2 := &dispatch.Dispatcher{Store: s, Runners: map[string]dispatch.Runner{"fake": r}, Backoff: time.Hour}
	dispatch.SetClock(d2, func() time.Time { return time.Now().Add(skew) })
	for i, min := range []int{6, 10, 11, 15, 16} {
		skew = time.Duration(min)*time.Minute + 2*time.Second
		must(t, d2.Reconcile(ctx))
		must(t, d2.Reconcile(ctx))
		if want := []int{1, 2, 2, 3, 3}[i]; count("session_harness_retry") != want || len(r.nudges) != want {
			t.Fatalf("after %dm: %d tries, typed %d times; want %d", min, count("session_harness_retry"), len(r.nudges), want)
		}
		if n := count("session_harness_error"); n != 0 {
			t.Fatalf("after %dm: session_harness_error raised with a try still to make or to wait for", min)
		}
	}

	skew = 20*time.Minute + 3*time.Second
	must(t, d2.Reconcile(ctx))
	must(t, d2.Reconcile(ctx))
	skew = 40 * time.Minute
	must(t, d2.Reconcile(ctx))
	evs := eventKinds(t, s, b.ID, "session_harness_error")
	if len(evs) != 1 {
		t.Fatalf("session_harness_error events = %d, want 1 once the tries are used up", len(evs))
	}
	data := evs[0].Data
	if data["reason"] != dispatch.ReasonAtCapacity || data["tries"] != float64(dispatch.HarnessRetries) ||
		data["tail"] != atCapacity || !strings.Contains(data["hint"].(string), "first prompt again 3 times") ||
		!strings.Contains(data["hint"].(string), "yardr bead release "+b.ID) {
		t.Fatalf("session_harness_error data = %v", data)
	}
	if count("session_harness_retry") != dispatch.HarnessRetries || len(r.nudges) != dispatch.HarnessRetries {
		t.Fatalf("%d tries, typed %d times; want %d", count("session_harness_retry"), len(r.nudges), dispatch.HarnessRetries)
	}
}

// A network error stops a session mid-work, where the first prompt is the
// wrong thing to say and the harness asks for the message to be sent again.
// The agent is given a short resume prompt instead, five minutes on, up to
// three times in the session's life; when the error still stands after the
// third try, session_harness_error is raised with tries.
func TestNetworkErrorMidWorkIsRetriedThenRaised(t *testing.T) {
	t.Parallel()
	const dropped = "◆ model failed: network connection could not be opened within 15 seconds (after 4 provider attempts)\n❯ "
	const resume = "yardr: your harness stopped on a dropped network connection. Go on where you stopped."
	s, r, d, b, at := retrySetupKind(t, "muse", "◆ Finished the change.\n❯ ")
	ctx := context.Background()
	r.show("working", "◆ Reading the brief")
	must(t, d.Reconcile(ctx))
	r.mu.Lock()
	r.worked = true
	r.mu.Unlock()
	r.show("idle", dropped)
	must(t, d.Reconcile(ctx))
	count := func(kind string) int { t.Helper(); return len(eventKinds(t, s, b.ID, kind)) }
	if count("session_harness_error") != 0 || count("session_harness_retry") != 0 {
		t.Fatalf("raised or tried on the first sight: error %d, retry %d", count("session_harness_error"), count("session_harness_retry"))
	}

	for i, min := range []int{5, 10, 15} {
		at(time.Duration(min)*time.Minute + time.Second)
		must(t, d.Reconcile(ctx))
		must(t, d.Reconcile(ctx))
		if want := i + 1; count("session_harness_retry") != want || len(r.nudges) != want {
			t.Fatalf("after %dm: %d tries, typed %d times; want %d", min, count("session_harness_retry"), len(r.nudges), want)
		}
		if n := count("session_harness_error"); n != 0 {
			t.Fatalf("after %dm: session_harness_error raised with a try still to make or to wait for", min)
		}
		if r.nudges[i] != resume {
			t.Fatalf("typed %q, want the resume prompt", r.nudges[i])
		}
	}

	at(20*time.Minute + 3*time.Second)
	must(t, d.Reconcile(ctx))
	must(t, d.Reconcile(ctx))
	evs := eventKinds(t, s, b.ID, "session_harness_error")
	if len(evs) != 1 {
		t.Fatalf("session_harness_error events = %d, want 1 once the tries are used up", len(evs))
	}
	data := evs[0].Data
	got, _ := s.GetBead(ctx, b.ID)
	if data["reason"] != dispatch.ReasonNetwork || data["tries"] != float64(dispatch.HarnessRetries) ||
		data["tail"] != dropped || data["session"] != *got.Session || data["kind"] != "muse" ||
		data["line"] != "◆ model failed: network connection could not be opened within 15 seconds (after 4 provider attempts)" ||
		!strings.Contains(data["hint"].(string), "prompted the agent again 3 times") {
		t.Fatalf("session_harness_error data = %v", data)
	}
	tries := eventKinds(t, s, b.ID, "session_harness_retry")
	if len(tries) != dispatch.HarnessRetries || len(r.nudges) != dispatch.HarnessRetries {
		t.Fatalf("%d tries, typed %d times; want %d", len(tries), len(r.nudges), dispatch.HarnessRetries)
	}
	if tries[0].Data["reason"] != dispatch.ReasonNetwork || tries[0].Data["try"] != float64(1) {
		t.Fatalf("session_harness_retry data = %v", tries[0].Data)
	}
}

// Only at_capacity on a first prompt, and network, are retried. Another
// reason, a session at capacity that has done something (it stopped
// mid-work, where the first prompt is not what it needs), and one with text
// in its input are raised at once, as they always were, and nothing is
// typed however long they sit.
func TestHarnessErrorThatIsNotPromptedAgain(t *testing.T) {
	t.Parallel()
	const limited = "■ You've hit your usage limit. Upgrade to Plus to continue using Codex.\n› Ask Codex to do anything"
	for name, tc := range map[string]struct {
		tail   string
		worked bool
		input  string
		want   string
	}{
		"another reason": {tail: limited, want: dispatch.ReasonRateLimited},
		"mid-work":       {tail: atCapacity, worked: true, want: dispatch.ReasonAtCapacity},
		"text in input":  {tail: atCapacity, input: "wait, try the other", want: dispatch.ReasonAtCapacity},
	} {
		t.Run(name, func(t *testing.T) {
			s, r, d, b, at := retrySetup(t, "› Ask Codex to do anything")
			ctx := context.Background()
			r.show("working", "• Reading the brief")
			must(t, d.Reconcile(ctx))
			r.mu.Lock()
			r.worked, r.input = tc.worked, tc.input
			r.mu.Unlock()
			r.show("idle", tc.tail)
			must(t, d.Reconcile(ctx))
			evs := eventKinds(t, s, b.ID, "session_harness_error")
			if len(evs) != 1 || evs[0].Data["reason"] != tc.want {
				t.Fatalf("session_harness_error = %v, want one at once with reason %s", evs, tc.want)
			}
			if _, has := evs[0].Data["tries"]; has {
				t.Fatalf("session_harness_error data = %v; tries of a session that was never prompted again", evs[0].Data)
			}
			at(6 * time.Minute)
			must(t, d.Reconcile(ctx))
			at(12 * time.Minute)
			must(t, d.Reconcile(ctx))
			if n := len(eventKinds(t, s, b.ID, "session_harness_retry")); n != 0 || len(r.nudges) != 0 {
				t.Fatalf("%d tries, typed %q; want none", n, r.nudges)
			}
			if n := len(eventKinds(t, s, b.ID, "session_harness_error")); n != 1 {
				t.Fatalf("session_harness_error events = %d, want 1", n)
			}
		})
	}
}
