package dispatch

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"yardr.dev/yardr/internal/store"
)

// phaseRunner is a runner whose looks at a pane are scripted: a look
// lookUnknown fails.
type phaseRunner struct {
	looks           promptLooks
	status          string
	nudges, submits int
}

func (r *phaseRunner) Start(context.Context, Job) (map[string]any, error)  { return nil, nil }
func (r *phaseRunner) Alive(context.Context, map[string]any) (bool, error) { return true, nil }
func (r *phaseRunner) Stop(context.Context, map[string]any) error          { return nil }

func (r *phaseRunner) answer(l look) (bool, error) {
	if l == lookUnknown {
		return false, errors.New("pane read failed")
	}
	return l == lookYes, nil
}

func (r *phaseRunner) PromptShown(context.Context, map[string]any, string) (bool, error) {
	return r.answer(r.looks.shown)
}

func (r *phaseRunner) PromptTyped(context.Context, map[string]any, string) (bool, error) {
	return r.answer(r.looks.typed)
}

func (r *phaseRunner) PromptQueued(context.Context, map[string]any, string) (bool, error) {
	return r.answer(r.looks.queued)
}

func (r *phaseRunner) HarnessScreen(context.Context, map[string]any) (up, known bool) {
	return r.looks.screen == lookYes, r.looks.screen != lookUnknown
}

func (r *phaseRunner) InputDialog(context.Context, map[string]any) (dialog, line string, err error) {
	if at, err := r.answer(r.looks.dialog); !at {
		return "", "", err
	}
	return "rate_limit_switch", "Approaching rate limits", nil
}

func (r *phaseRunner) SubmitPrompt(context.Context, map[string]any) error { r.submits++; return nil }

func (r *phaseRunner) Nudge(context.Context, map[string]any, string) error { r.nudges++; return nil }

func (r *phaseRunner) Status(context.Context, map[string]any) (string, error) { return r.status, nil }

func (r *phaseRunner) Tail(context.Context, map[string]any, int) (string, error) { return "", nil }

// phaseRow is one row of the table: the phase as a handle stores it.
type phaseRow struct {
	name   string
	phase  promptPhase
	late   bool // queued: at or past its deadline
	ends   bool // a bead's session
	handle func(now time.Time) map[string]any
}

func phaseRows() []phaseRow {
	at := func(now time.Time, ago time.Duration) string { return now.Add(-ago).UTC().Format(time.RFC3339Nano) }
	base := func(now time.Time, more ...any) map[string]any {
		h := map[string]any{store.PendingPrompt: "read your brief", promptSends: 1, promptSentAt: at(now, time.Hour)}
		for i := 0; i < len(more); i += 2 {
			h[more[i].(string)] = more[i+1]
		}
		return h
	}
	return []phaseRow{
		{"delivering", phaseDelivering, false, true, func(now time.Time) map[string]any { return base(now) }},
		{"queued before its deadline", phaseQueued, false, true, func(now time.Time) map[string]any {
			return base(now, PromptQueuedAt, at(now, time.Second))
		}},
		{"queued past its deadline", phaseQueued, true, true, func(now time.Time) map[string]any {
			return base(now, PromptQueuedAt, at(now, time.Hour))
		}},
		{"given up, bead", phaseGivenUp, false, true, func(now time.Time) map[string]any {
			return base(now, promptLost, true, promptSends, PromptTries, PromptQueuedAt, at(now, time.Hour))
		}},
		{"given up, crew", phaseGivenUp, false, false, func(now time.Time) map[string]any {
			return base(now, promptLost, true, promptSends, PromptTries)
		}},
	}
}

// phaseOutcome is what a row came to, from the decision alone or from a
// whole pass.
type phaseOutcome struct {
	arrived, gaveUp, end bool
	nudges, submits      int
	handle               map[string]any // what is stored after the pass
}

// checkPhase holds an outcome to the properties every row must have.
func checkPhase(t *testing.T, label string, row phaseRow, l promptLooks, status string, o phaseOutcome) {
	t.Helper()
	unknown := l.shown == lookUnknown || l.typed == lookUnknown || l.queued == lookUnknown || l.screen == lookUnknown || l.dialog == lookUnknown
	idle := status == "idle" || status == "done"
	held := row.phase == phaseQueued || (row.phase == phaseGivenUp && row.ends)
	fail := func(format string, args ...any) {
		t.Helper()
		t.Errorf("%s: %s, looks %+v, %s: %s", label, row.name, l, status, fmt.Sprintf(format, args...))
	}
	// P1: nothing typed and no Enter once queued or given up; an unknown
	// queue look never sends.
	if row.phase != phaseDelivering && (o.nudges != 0 || o.submits != 0) {
		fail("typed %d times and submitted %d, want nothing", o.nudges, o.submits)
	}
	if l.queued == lookUnknown && o.nudges != 0 {
		fail("sent %d times on a queue that could not be read", o.nudges)
	}
	// A delivery the harness queued is stored as queued, whatever the
	// status, and neither sent nor taken as arrived on that pass.
	queuedLook := l.shown == lookNo && l.typed == lookNo && l.queued == lookYes
	if label == "pass" && row.phase == phaseDelivering && queuedLook &&
		(o.handle == nil || o.handle[PromptQueuedAt] == nil || o.nudges != 0 || o.arrived) {
		fail("handle %v after %d sends, arrived=%v, want it stored as queued", o.handle, o.nudges, o.arrived)
	}
	// P2: an unknown look decides nothing, and drops nothing stored.
	if held && unknown && status != "working" && l.shown != lookYes && (o.arrived || o.gaveUp) {
		fail("arrived=%v gaveUp=%v on an unknown look", o.arrived, o.gaveUp)
	}
	if o.handle != nil && held && !o.arrived {
		if o.handle[PromptQueuedAt] == nil || o.handle[store.PendingPrompt] == nil {
			fail("handle %v dropped what was stored", o.handle)
		}
	}
	if o.handle != nil && row.phase == phaseGivenUp && !o.arrived && o.handle[promptLost] != true {
		fail("handle %v dropped prompt_lost", o.handle)
	}
	// P3: a readable prompt still queued past its deadline is given up; a
	// bead's given-up session is ended on every row. A dialog hides the
	// queue: a pane at one is not readable.
	readable := l.shown == lookNo && l.typed == lookNo && l.screen == lookYes && l.dialog == lookNo
	readableQueued := readable && l.queued == lookYes
	if row.phase == phaseQueued && row.late && readableQueued && idle && (!o.gaveUp || !o.end) {
		fail("gaveUp=%v end=%v, want it given up and ended", o.gaveUp, o.end)
	}
	if row.phase == phaseQueued && o.gaveUp && !(row.late && readableQueued && idle) {
		fail("given up, want it waiting")
	}
	if row.phase == phaseGivenUp && row.ends && (!o.end || o.arrived) {
		fail("end=%v arrived=%v, want it ended", o.end, o.arrived)
	}
	if row.phase == phaseGivenUp && !row.ends && o.end {
		fail("a crew member's session is ended")
	}
	// P4: a queued prompt arrives when its agent works, or when a readable
	// screen shows it nowhere.
	nowhere := readable && l.queued == lookNo
	if row.phase == phaseQueued && (status == "working" || l.shown == lookYes || (nowhere && idle)) && !o.arrived {
		fail("not arrived")
	}
	if row.phase == phaseQueued && o.arrived && !(status == "working" || l.shown == lookYes || (nowhere && idle)) {
		fail("arrived, want it waiting")
	}
}

// The first prompt's phase decides before the looks do: every phase, every
// look (the dialog's included) yes, no or unknown, every status, through the decision and through
// a whole pass, each held to the properties of checkPhase (yardr-w6w3).
func TestPromptPhaseTable(t *testing.T) {
	t.Parallel()
	looks := []look{lookYes, lookNo, lookUnknown}
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	rows := 0
	for _, row := range phaseRows() {
		for _, shown := range looks {
			for _, typed := range looks {
				for _, queued := range looks {
					for _, screen := range looks {
						for _, dialog := range looks {
							for _, status := range []string{"idle", "working", "blocked", "done"} {
								rows++
								l := promptLooks{shown, typed, queued, screen, dialog}
								handle := row.handle(now)
								if got := promptPhaseOf(handle); got != row.phase {
									t.Fatalf("%s: phase %s, want %s", row.name, got, row.phase)
								}

								// The decision alone, on the looks a pass takes.
								r := &phaseRunner{looks: l, status: status}
								c := promptCase{phase: row.phase, status: status, sent: true, now: now,
									budget: PromptTries * time.Minute, wait: time.Minute, ends: row.ends,
									looks: (&Dispatcher{}).promptLooks(context.Background(), r, handle, "read your brief")}
								c.looks.screen, c.looks.dialog = screen, dialog
								if at, ok := handle[PromptQueuedAt].(string); ok {
									c.queuedAt, _ = time.Parse(time.RFC3339Nano, at)
								}
								dec := decidePrompt(c)
								if (dec.act == actDeliver) != (row.phase == phaseDelivering) {
									t.Errorf("%s, %+v, %s: act %d", row.name, l, status, dec.act)
								}
								checkPhase(t, "decision", row, l, status, phaseOutcome{
									arrived: dec.act == actArrive, gaveUp: dec.act == actGiveUp,
									end: dec.act == actEnd || (dec.act == actGiveUp && row.ends)})

								// A whole pass.
								d := &Dispatcher{PromptSettle: -1, PromptWait: time.Minute}
								d.defaults()
								SetClock(d, func() time.Time { return now })
								pass := d.pendingPrompt(context.Background(), r, "s1", handle, "read your brief", status, nil, true, false, row.ends)
								checkPhase(t, "pass", row, l, status, phaseOutcome{
									arrived: pass.arrived, gaveUp: pass.gaveUp, end: pass.end,
									nudges: r.nudges, submits: r.submits, handle: pass.handle})
							}
						}
					}
				}
			}
		}
	}
	if rows != 5*243*4 {
		t.Fatalf("%d rows", rows)
	}
}
