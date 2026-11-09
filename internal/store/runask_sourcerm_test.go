package store

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/source"
)

func sourceRmAsk(command string) Ask {
	return Ask{Question: "Remove the source?", To: "mckean", Context: []string{"nothing is told through it any more"},
		Run: &AskRun{Argv: []string{command}}}
}

// sourceRmYard is a list yard where ada and the crew member yardmaster hold
// what names the source crm: a grant, a subscription, and a hook.
func sourceRmYard(t *testing.T) *askYard {
	t.Helper()
	y := listYard(t)
	if _, err := y.GrantSource(y.ctx, "crm", "read", Grantee{Crew: "yardmaster"}, "", "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.PutHook(y.ctx, Hook{Name: "crm", Source: "crm", SecretFile: HookSecretFile(y.home, "crm")}, "mckean"); err != nil {
		t.Fatal(err)
	}
	return y
}

// A source rm card is routine, since it narrows: it names the source, the
// grants and records that go with it and what stays that names it. The
// approval removes the source in its own write, as the command does, and
// answers what the command prints; the hook that serves it stays.
func TestRunAskSourceRm(t *testing.T) {
	t.Parallel()
	y := sourceRmYard(t)
	seq := y.ask(t, sourceRmAsk("yardr source rm crm"), "yardmaster")
	got := y.asked(t, seq)
	run := got.Ask.Run
	if want := []string{"source", "rm", "crm"}; !slices.Equal(run.Argv, want) {
		t.Fatalf("the ask stores %v, want %v", run.Argv, want)
	}
	if run.Description != "Remove a source and the files of its commands (the file its credential is in stays)" {
		t.Errorf("description %q", run.Description)
	}
	joined := strings.Join(run.Does, "\n")
	for _, want := range []string{
		"removes the source crm (https://crm.example.com/v1): its declaration goes, with 1 grant of its calls and the counts and keys the yard kept for it.",
		"1 hook still name it, and stay: nothing is heard from a source that is not there",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the card does not say %q; it says %q", want, run.Does)
		}
	}
	if strings.Contains(joined, "The files of its commands") {
		t.Errorf("the card names command files for a source with none: %q", run.Does)
	}
	if _, has := run.Seen["source crm files"]; has {
		t.Errorf("the card holds the copies of a source with none: %v", run.Seen)
	}
	if run.BackBy != "only the operator" || !strings.Contains(run.Back, "yardr source add <file>") {
		t.Errorf("the card is taken back by %q, %q", run.Back, run.BackBy)
	}
	if len(got.Ask.Weight) != 0 {
		t.Fatalf("the ask weighs %+v, want routine", got.Ask.Weight)
	}
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "crm: removed; 0 subscriptions and 0 participations still name this source."; done.Result != want {
		t.Fatalf("the approval recorded %q, want %q", done.Result, want)
	}
	if _, err := y.GetSource(y.ctx, "crm"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the source still stands: %v", err)
	}
	if grants, err := y.SourceGrants(y.ctx, "crm"); err != nil || len(grants) != 0 {
		t.Fatalf("grants of the removed source = %+v, %v", grants, err)
	}
	if _, err := y.GetHook(y.ctx, "crm"); err != nil {
		t.Fatalf("the hook that served it was removed: %v", err)
	}
	// The removal is on the event stream, with the ask it was approved on.
	evs, err := y.EventsOfKind(y.ctx, "source_removed", 2)
	if err != nil || len(evs) != 1 || evs[0].Data["source"] != "crm" {
		t.Fatalf("source_removed events = %+v, %v", evs, err)
	}
	y.approved(t, evs[0], seq)
	if rec, err := y.CarryOf(y.ctx, y.bead, seq); err != nil || rec != nil {
		t.Fatalf("a source with no command files left a carrying record: %+v, %v", rec, err)
	}
}

// A source whose commands were copied to the yard's home is removed in two
// parts, as one added with them is made: the approval removes the source and
// leaves a record for the server to remove the files under the apply lock,
// and answers that it is to be carried out. The card names the directory,
// and an approval is held to the copies it saw.
func TestRunAskSourceRmWithCommandFiles(t *testing.T) {
	t.Parallel()
	y := sourceRmYard(t)
	dir := filepath.Join(y.home, "sources", "crm")
	if err := source.WriteCopies(y.home, "crm", []source.FileCopy{{Program: "crm/limits", Data: []byte("#!/bin/sh\necho ok\n")}}); err != nil {
		t.Fatal(err)
	}
	seq := y.ask(t, sourceRmAsk("source rm crm"), "yardmaster")
	run := y.asked(t, seq).Ask.Run
	if want := "The files of its commands under " + dir + " go too, removed by the yard's server after the approval."; !slices.Contains(run.Does, want) {
		t.Fatalf("the card says %q, want %q", run.Does, want)
	}
	if run.Seen["source crm files"] == "" {
		t.Fatalf("the card holds no copies: %v", run.Seen)
	}
	// A copy changed since is another card.
	if err := os.WriteFile(filepath.Join(dir, "crm", "limits"), []byte("#!/bin/sh\necho changed\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil); err == nil ||
		!strings.Contains(err.Error(), "was written from another declaration of the source crm") {
		t.Fatalf("approving with a copy changed since = %v", err)
	}
	if _, err := y.GetSource(y.ctx, "crm"); err != nil {
		t.Fatalf("the refused approval removed the source: %v", err)
	}
	seq = y.ask(t, sourceRmAsk("source rm crm"), "yardmaster")
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if done.Result != CarryPending {
		t.Fatalf("the approval recorded %q, want it to be carried out", done.Result)
	}
	if _, err := y.GetSource(y.ctx, "crm"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the source still stands: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "crm", "limits")); err != nil {
		t.Fatalf("the approval removed the files itself: %v", err)
	}
	if rec, err := y.CarryOf(y.ctx, y.bead, seq); err != nil || rec == nil || rec.State != CarryApproved || !slices.Equal(rec.Argv, []string{"source", "rm", "crm"}) {
		t.Fatalf("the carrying record = %+v, %v", rec, err)
	}
}

// A source rm ask is refused, storing nothing, for what the row refuses: a
// source the yard has not, a name no source has, the wrong number of words,
// an asker who may remove it themselves, and a writer who is no one of the
// yard.
func TestRunAskSourceRmRefusals(t *testing.T) {
	t.Parallel()
	y := sourceRmYard(t)
	for _, c := range []struct {
		name, command, asker, want string
	}{
		{"no such source", "source rm erp", "yardmaster", "source erp: not found"},
		{"a path", "source rm ../crm", "yardmaster", `source "../crm": use lowercase letters`},
		{"a flag", "source rm -r", "yardmaster", `source "-r": use lowercase letters`},
		{"two sources", "source rm crm erp", "yardmaster", "one source, not 2 words"},
		{"none", "source rm", "yardmaster", "one source, not 0 words"},
		{"the operator asks", "source rm crm", "mckean", "yardr source rm crm needs no approval: run it yourself"},
		{"nobody", "source rm crm", "stranger", "a command for the operator to approve is proposed by a crew member or a user of the yard"},
	} {
		if _, err := y.AddAsk(y.ctx, y.bead, sourceRmAsk(c.command), c.asker); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s = %v, want %q", c.name, err, c.want)
		}
	}
	if notes, err := y.Notes(y.ctx, y.bead); err != nil || len(notes) != 0 {
		t.Fatalf("the refused asks left %d notes, %v", len(notes), err)
	}
}

// An approval is held to the source the card named: one replaced since, or
// whose command copies changed, is another source, and one removed since
// leaves nothing to approve.
func TestRunAskSourceRmStale(t *testing.T) {
	t.Parallel()
	y := sourceRmYard(t)
	seq := y.ask(t, sourceRmAsk("source rm crm"), "yardmaster")
	if _, err := y.PutSource(y.ctx, "crm.yaml", strings.Replace(runCRM, "https://crm.example.com/v1", "https://crm.example.org/v2", 1), "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil); err == nil ||
		!strings.Contains(err.Error(), "was written from another declaration of the source crm") {
		t.Fatalf("approving a source replaced since = %v", err)
	}
	if err := y.DeleteSource(y.ctx, "crm", "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil); err == nil || !strings.Contains(err.Error(), "source crm: not found") {
		t.Fatalf("approving a source removed since = %v", err)
	}
}
