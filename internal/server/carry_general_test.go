package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/config"
	"yardr.dev/yardr/internal/store"
)

// generalYard is a carry yard with general cards on.
func generalCarrierYard(t *testing.T) (*store.Store, string, string) {
	t.Helper()
	st, home, bead := carryYard(t)
	if err := st.SetSetting(context.Background(), store.GeneralCardsSetting, "true", "mckean"); err != nil {
		t.Fatal(err)
	}
	return st, home, bead
}

func generalAskOf(words ...string) store.Ask {
	return store.Ask{Question: "Run it?", To: "mckean", Context: []string{"no card fits it"},
		Run: &store.AskRun{General: words}}
}

// generalScript stands in for the yard's binary: body runs with the
// re-exec's argv, directory, environment and closed stdin.
func generalScript(t *testing.T, body string) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "yardr-standin")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	old := executable
	executable = func() (string, error) { return script, nil }
	t.Cleanup(func() { executable = old })
}

// An approved general card is carried out once, as typed: the binary
// with the home and the approver, the words in order, from the home,
// with exactly the five yardr variables dropped and stdin closed.
func TestCarryGeneralRunsAsTyped(t *testing.T) {
	ctx := context.Background()
	st, home, bead := generalCarrierYard(t)
	generalScript(t, "printf 'args:%s\\ndir:%s\\nkeep:%s\\nsession:%s bead:%s actor:%s home:%s crew:%s card:%s\\nstdin:[%s]\\n' \"$*\" \"$(pwd)\" \"${GENERAL_KEEP-}\" \"${YARDR_SESSION-UNSET}\" \"${YARDR_BEAD-UNSET}\" \"${YARDR_ACTOR-UNSET}\" \"${YARDR_HOME-UNSET}\" \"${YARDR_CREW-UNSET}\" \"${YARDR_GENERAL_CARD-UNSET}\" \"$(cat)\"\n")
	t.Setenv("GENERAL_KEEP", "kept")
	t.Setenv("YARDR_SESSION", "pane-1")
	t.Setenv("YARDR_BEAD", "yardr-other")
	t.Setenv("YARDR_ACTOR", "yardmaster")
	t.Setenv("YARDR_HOME", "/elsewhere")
	t.Setenv("YARDR_CREW", "yardmaster")
	seq, err := st.AddAsk(ctx, bead, generalAskOf("bead", "create", "Made", "by", "card"), "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveAsk(ctx, bead, seq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	c := testCarrier(st, home, "boot-one")
	if did, err := c.carryNext(ctx); err != nil || !did {
		t.Fatalf("carryNext = %v, %v", did, err)
	}
	rec := carryState(t, st, bead, seq)
	if rec.State != store.CarryDone {
		t.Fatalf("the carrying record = %+v", rec)
	}
	dir, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"args:--home " + home + " --actor mckean bead create Made by card",
		"dir:" + dir,
		"keep:kept",
		"session:UNSET bead:UNSET actor:UNSET home:UNSET crew:UNSET card:1",
		"stdin:[]",
	} {
		if !strings.Contains(rec.Result, want) {
			t.Errorf("what ran does not hold %q:\n%s", want, rec.Result)
		}
	}
	if lines := resolvedLines(t, st, bead, seq); !strings.Contains(lines, "args:--home") {
		t.Errorf("the card does not read what ran:\n%s", lines)
	}
}

// The dropped list is exactly the five, and nothing else goes.
func TestGeneralEnvDropsExactlyFive(t *testing.T) {
	in := []string{"PATH=/bin", "YARDR_SESSION=s", "YARDR_BEAD=b", "YARDR_ACTOR=a", "YARDR_HOME=h", "YARDR_CREW=c", "YARDR_OTHER=o", "HOME=/home"}
	want := []string{"PATH=/bin", "YARDR_OTHER=o", "HOME=/home"}
	if got := scrubbedGeneralEnv(in); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("scrubbedGeneralEnv = %v, want %v", got, want)
	}
	if len(generalDropped) != 5 {
		t.Errorf("generalDropped = %v, want the five", generalDropped)
	}
}

// A general command that fails fails the carry with its stderr tail
// and the terminal line; one past the cap names the cap.
func TestCarryGeneralFailures(t *testing.T) {
	ctx := context.Background()
	st, home, bead := generalCarrierYard(t)
	generalScript(t, "echo went-wrong >&2\nexit 3\n")
	seq, err := st.AddAsk(ctx, bead, generalAskOf("bead", "create", "Nope"), "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveAsk(ctx, bead, seq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	c := testCarrier(st, home, "boot-one")
	if did, err := c.carryNext(ctx); err != nil || !did {
		t.Fatalf("carryNext = %v, %v", did, err)
	}
	rec := carryState(t, st, bead, seq)
	if rec.State != store.CarryFailed || !strings.Contains(rec.Result, "went-wrong") ||
		!strings.Contains(rec.Result, "Finish it with yardr bead create Nope in a terminal") {
		t.Fatalf("the carrying record = %+v", rec)
	}

	st2, home2, bead2 := generalCarrierYard(t)
	generalScript(t, "head -c 70000 /dev/zero | tr '\\0' a\n")
	seq2, err := st2.AddAsk(ctx, bead2, generalAskOf("bead", "create", "Loud"), "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st2.ApproveAsk(ctx, bead2, seq2, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	if did, err := testCarrier(st2, home2, "boot-one").carryNext(ctx); err != nil || !did {
		t.Fatalf("carryNext = %v, %v", did, err)
	}
	if rec := carryState(t, st2, bead2, seq2); rec.State != store.CarryFailed ||
		!strings.Contains(rec.Result, "printed more than 65536 bytes") {
		t.Fatalf("the loud carrying record = %+v", rec)
	}
}

// A full cap of output still completes: the answer note holds it
// whole, under no local note bound but the cap itself.
func TestCarryGeneralHoldsFullCap(t *testing.T) {
	ctx := context.Background()
	st, home, bead := generalCarrierYard(t)
	generalScript(t, "head -c 65536 /dev/zero | tr '\\0' a\n")
	seq, err := st.AddAsk(ctx, bead, generalAskOf("bead", "create", "Full"), "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveAsk(ctx, bead, seq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	if did, err := testCarrier(st, home, "boot-one").carryNext(ctx); err != nil || !did {
		t.Fatalf("carryNext = %v, %v", did, err)
	}
	if rec := carryState(t, st, bead, seq); rec.State != store.CarryDone || len(rec.Result) != 65536 {
		t.Fatalf("the full-cap record = state %s, %d bytes", rec.State, len(rec.Result))
	}
}

// Past its timeout the group is killed and the carry fails with the
// timeout's words, in the test's lowered bound.
func TestCarryGeneralTimeoutKillsGroup(t *testing.T) {
	ctx := context.Background()
	st, home, bead := generalCarrierYard(t)
	generalScript(t, "sleep 30\n")
	old := generalTimeout
	generalTimeout = 200 * time.Millisecond
	t.Cleanup(func() { generalTimeout = old })
	seq, err := st.AddAsk(ctx, bead, generalAskOf("bead", "create", "Slow"), "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveAsk(ctx, bead, seq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	if did, err := testCarrier(st, home, "boot-one").carryNext(ctx); err != nil || !did {
		t.Fatalf("carryNext = %v, %v", did, err)
	}
	if took := time.Since(began); took > 10*time.Second {
		t.Fatalf("the carry took %v: the group was not killed at the timeout", took)
	}
	if rec := carryState(t, st, bead, seq); rec.State != store.CarryFailed ||
		!strings.Contains(rec.Result, "no answer within") {
		t.Fatalf("the timed-out record = %+v", rec)
	}
}

// A general setup takes the general leg, never the setup carry, and a
// general carry completes with the apply lock held elsewhere.
func TestCarryGeneralLegAndNoLock(t *testing.T) {
	ctx := context.Background()
	st, home, bead := generalCarrierYard(t)
	generalScript(t, "echo carried\n")
	seq, err := st.AddAsk(ctx, bead, generalAskOf("setup", "x"), "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveAsk(ctx, bead, seq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	release, err := config.AcquireApplyLock(home)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if did, err := testCarrier(st, home, "boot-one").carryNext(ctx); err != nil || !did {
		t.Fatalf("carryNext with the lock held = %v, %v", did, err)
	}
	if rec := carryState(t, st, bead, seq); rec.State != store.CarryDone || !strings.Contains(rec.Result, "carried") {
		t.Fatalf("the carrying record = %+v", rec)
	}
}
