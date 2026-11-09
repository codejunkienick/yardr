package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// A started delegated pack record carries its import's writes for
// delegatedPackTTL alone: past it, a write under the record is refused
// as any write of the yardmaster's is, and the record still finishes.
func TestDelegatedPackRecordExpires(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "yard.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	ctx := context.Background()
	if err := s.SetSetting(ctx, OperatorSetting, "jo", "jo"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddUser(ctx, "ada", "jo"); err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{"yardmaster", "clerk"} {
		if _, err := s.PutCrew(ctx, CrewMember{Name: r, Runner: "herdr"}, "jo"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PutGroup(ctx, Group{Name: "builders", Runner: "herdr", Limit: 1, Config: map[string]any{"kind": "claude", "model": "sonnet"}}, "jo"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, DelegationSetting, "true", "jo"); err != nil {
		t.Fatal(err)
	}
	s.SetPackReader(func(_ context.Context, name string, depot string, _ Mappings, _ API) (*PackReading, error) {
		return &PackReading{Name: "starter", Kind: "git", Depot: depot, Digest: "abc123", Plan: &AskPlan{Objects: []AskPlanObject{
			{Kind: "group", Name: "builders", Action: "change"},
		}}}, nil
	})
	if err := s.Mail(ctx, "yardmaster", "apply the pack for me", "jo"); err != nil {
		t.Fatal(err)
	}
	mail, err := s.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	dctx := WithDelegation(ctx, DelegatedCall{For: "jo", Mail: mail, Command: "yardr pack apply starter --depot shed"})
	id, err := s.StartDelegatedPack(dctx, DelegatedPackStart{Pack: "starter", Depot: "shed", Digest: "abc123",
		Objects: []DelegatedPackObject{{Kind: "group", Name: "builders", Action: "change"}}}, "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	packCtx := WithDelegatedPack(dctx, id)
	if err := s.PutGroup(packCtx, Group{Name: "builders", Runner: "herdr", Limit: 1, Config: map[string]any{"kind": "claude", "model": "sonnet"}}, "yardmaster"); err != nil {
		t.Fatalf("a write under a fresh record: %v", err)
	}

	old := delegatedPackTTL
	delegatedPackTTL = -time.Hour
	t.Cleanup(func() { delegatedPackTTL = old })
	if err := s.PutGroup(packCtx, Group{Name: "builders", Runner: "herdr", Limit: 1, Config: map[string]any{"kind": "claude", "model": "sonnet"}}, "yardmaster"); err == nil {
		t.Error("a write under an expired started record was let through")
	}
	if err := s.FinishDelegatedPack(dctx, id, false, nil, "the run timed out", "yardmaster"); err != nil {
		t.Errorf("an expired record no longer finishes: %v", err)
	}
	if rec, err := s.Delegation(ctx, id, "jo"); err != nil || rec.State != "failed" {
		t.Errorf("record = %+v, %v; want failed", rec, err)
	}
}
