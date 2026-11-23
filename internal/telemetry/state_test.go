package telemetry

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMintID(t *testing.T) {
	t.Parallel()
	shape := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	first, err := MintID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := MintID()
	if err != nil {
		t.Fatal(err)
	}
	if !shape.MatchString(first) {
		t.Errorf("minted id = %q", first)
	}
	if first == second {
		t.Error("two minted ids are equal")
	}
}

func TestDeriveUUID(t *testing.T) {
	t.Parallel()
	shape := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	a := DeriveUUID("id", "yardr_daily", "2026-10-28")
	b := DeriveUUID("id", "yardr_daily", "2026-10-28")
	if a != b || !shape.MatchString(a) {
		t.Errorf("derived uuid = %q, %q", a, b)
	}
	for _, other := range []string{
		DeriveUUID("other", "yardr_daily", "2026-10-28"),
		DeriveUUID("id", "yardr_daily", "2026-10-29"),
		DeriveUUID("id", "yardr_daily", "2026-10-28", "claude"),
	} {
		if other == a {
			t.Errorf("derived uuid does not vary: %q", other)
		}
	}
}

func TestStateRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "telemetry.json")
	if state, err := LoadState(path); err != nil || state != nil {
		t.Fatalf("missing state = %+v, %v", state, err)
	}
	want := &State{
		InstallID: "id", Minted: "2026-10-26",
		Grants: []string{"2026-10-26T09:30:00Z"}, Withdrawals: []string{"2026-10-27T09:30:00Z"},
		EligibleFrom: "2026-10-27", LastAttemptDay: "2026-10-28", LastDaySent: "2026-10-27",
	}
	if err := want.Save(path); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("state file mode = %v, %v", fi.Mode(), err)
	}
	got, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.InstallID != want.InstallID || len(got.Grants) != 1 || len(got.Withdrawals) != 1 ||
		got.EligibleFrom != want.EligibleFrom || got.LastAttemptDay != want.LastAttemptDay ||
		got.LastDaySent != want.LastDaySent || got.Minted != want.Minted {
		t.Errorf("round trip = %+v", got)
	}
	stale := path + ".tmp"
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(stale)
	if err := want.Save(path); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("replaced state file mode = %v, %v", fi.Mode(), err)
	}
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(path); err == nil {
		t.Error("a corrupt state file loads")
	} else if got := err.Error(); !strings.Contains(got, path) {
		t.Errorf("corrupt state error = %q", got)
	}
}

// The sender's two writes go by the file as it stands: the attempt is
// recorded once a day, the cursor only moves forward, and an off and an on
// (or a forget) that happened while a request was out is not undone by the
// copy the batch was built from.
func TestMarkAttemptAndMarkSent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "telemetry.json")
	now := time.Date(2026, 10, 28, 15, 0, 0, 0, time.UTC)
	built := &State{InstallID: "id", Minted: "2026-10-20", Grants: []string{"2026-10-20T09:00:00Z"}, EligibleFrom: "2026-10-21"}
	if err := built.Save(path); err != nil {
		t.Fatal(err)
	}

	if ok, err := MarkAttempt(path, "id", now); !ok || err != nil {
		t.Fatalf("MarkAttempt = %v, %v", ok, err)
	}
	if ok, err := MarkAttempt(path, "id", now.Add(time.Hour)); ok || err != nil {
		t.Fatalf("a second attempt on the same day = %v, %v", ok, err)
	}
	if ok, err := MarkAttempt(path, "someone else", now.AddDate(0, 0, 1)); ok || err != nil {
		t.Fatalf("an attempt under another id = %v, %v", ok, err)
	}
	if got, _ := LoadState(path); got.LastAttemptDay != "2026-10-28" {
		t.Fatalf("last_attempt_day = %q", got.LastAttemptDay)
	}

	// The request is out; meanwhile an off and an on moved eligible_from.
	// The sender writes its cursor onto that file, not over it.
	fresh, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	fresh.Withdrawals = append(fresh.Withdrawals, "2026-10-28T15:00:03Z")
	fresh.Grants = append(fresh.Grants, "2026-10-28T15:00:05Z")
	fresh.EligibleFrom = "2026-10-29"
	if err := fresh.Save(path); err != nil {
		t.Fatal(err)
	}
	if err := MarkSent(path, "id", "2026-10-27", nil); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastDaySent != "2026-10-27" || got.EligibleFrom != "2026-10-29" || len(got.Grants) != 2 || len(got.Withdrawals) != 1 || got.LastAttemptDay != "2026-10-28" {
		t.Fatalf("after the cursor: %+v", got)
	}
	if err := MarkSent(path, "id", "2026-10-25", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := LoadState(path); got.LastDaySent != "2026-10-27" {
		t.Fatalf("the cursor moved back to %q", got.LastDaySent)
	}
}

// A yard that forgot its id (the file deleted) or never had one is not
// given a file by the sender, and no lock file is left where nothing was
// ever turned on.
func TestMarkLeavesNoStateWhereThereIsNone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "telemetry.json")
	now := time.Date(2026, 10, 28, 15, 0, 0, 0, time.UTC)
	if ok, err := MarkAttempt(path, "id", now); ok || err != nil {
		t.Fatalf("MarkAttempt without a file = %v, %v", ok, err)
	}
	if err := MarkSent(path, "id", "2026-10-27", nil); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("the home holds %v, %v", entries, err)
	}
}

// A command is counted under its path without the leading "yardr ":
// nested commands keep their whole remainder, and bare yardr counts as
// itself.
func TestCommandName(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ path, want string }{
		{"yardr", "yardr"},
		{"yardr version", "version"},
		{"yardr bead advance", "bead advance"},
		{"yardr telemetry show", "telemetry show"},
		{"yardr bead ask approve", "bead ask approve"},
	} {
		if got := CommandName(tc.path); got != tc.want {
			t.Errorf("CommandName(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// Runs recorded in the state file accumulate by name, under its lock, so
// concurrent yardr processes each land theirs; a yard with no state file
// gains neither a file nor a lock file from being counted.
func TestRecordCommand(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "telemetry.json")
	if err := RecordCommand(path, "yardr version"); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("counting with no state file left %v, %v", entries, err)
	}
	if err := (&State{InstallID: "id"}).Save(path); err != nil {
		t.Fatal(err)
	}
	const writers = 20
	var wg sync.WaitGroup
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := RecordCommand(path, "yardr bead advance"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if err := RecordCommand(path, "yardr bead advance"); err != nil {
		t.Fatal(err)
	}
	if err := RecordCommand(path, "yardr version"); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Commands["bead advance"] != writers+1 || got.Commands["version"] != 1 || len(got.Commands) != 2 {
		t.Fatalf("commands = %v", got.Commands)
	}
}

// An accepted batch drains only what it carried: counts recorded while
// its request was out survive for the next batch, and a batch that was
// never accepted (no MarkSent) keeps its counts whole.
func TestMarkSentDrainsWhatWasSent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "telemetry.json")
	state := &State{InstallID: "id", Commands: map[string]int{"bead list": 2, "version": 1}}
	if err := state.Save(path); err != nil {
		t.Fatal(err)
	}
	// The request is out carrying bead list 2, version 1; meanwhile two
	// more bead lists and a show are counted.
	for _, p := range []string{"yardr bead list", "yardr bead list", "yardr telemetry show"} {
		if err := RecordCommand(path, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := MarkSent(path, "id", "2026-10-27", map[string]int{"bead list": 2, "version": 1}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastDaySent != "2026-10-27" {
		t.Errorf("last_day_sent = %q", got.LastDaySent)
	}
	if len(got.Commands) != 2 || got.Commands["bead list"] != 2 || got.Commands["telemetry show"] != 1 {
		t.Fatalf("commands after the drain = %v", got.Commands)
	}
	// A following batch drains the rest, with no source day of its own.
	if err := MarkSent(path, "id", "", map[string]int{"bead list": 2, "telemetry show": 1}); err != nil {
		t.Fatal(err)
	}
	got, err = LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Commands != nil || got.LastDaySent != "2026-10-27" {
		t.Fatalf("after the second drain = %+v", got)
	}
}

// Writers that read the file, change it and write it back hold its lock
// from the read to the write, so changes made at once are all there after:
// none writes back a copy older than another's change.
func TestStateWritersDoNotLoseEachOthersChanges(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "telemetry.json")
	if err := (&State{InstallID: "id"}).Save(path); err != nil {
		t.Fatal(err)
	}
	const writers = 20
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := update(path, "id", func(s *State) bool {
				s.Grants = append(s.Grants, fmt.Sprint(i))
				return true
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got, err := LoadState(path); err != nil || len(got.Grants) != writers {
		t.Fatalf("%d of %d changes survived: %+v, %v", len(got.Grants), writers, got, err)
	}
}
