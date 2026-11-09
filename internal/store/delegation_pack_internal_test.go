package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
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

// An import record under a started delegated pack record is held in every
// field but its objects, its time and its actor, the ones no socket caller
// can set among them (ApprovedBead and ApprovedAsk are json:"-"): a field
// added to PackImport later is held until the reading says it, and this
// test says which sample to forge it with.
func TestPackImportHeldHoldsEveryField(t *testing.T) {
	rec := &Delegation{ID: 7, Import: &PackImport{Pack: "starter", Depot: "shed", Source: "embedded in yardr test"}}
	imp := PackImport{Pack: "starter", Depot: "shed", Source: "embedded in yardr test", Objects: []PackObject{{Kind: "group", Name: "builders"}}, At: time.Now(), By: "yardmaster"}
	if err := packImportHeld(rec, imp); err != nil {
		t.Fatalf("the held record itself: %v", err)
	}
	samples := map[reflect.Type]string{
		reflect.TypeOf(""):                           `"x"`,
		reflect.TypeOf(false):                        `true`,
		reflect.TypeOf(uint64(0)):                    `1`,
		reflect.TypeOf(map[string]string{}):          `{"a":"b"}`,
		reflect.TypeOf(map[string]map[string]bool{}): `{"a":{"b":true}}`,
	}
	typ := reflect.TypeOf(imp)
	for i := range typ.NumField() {
		field := typ.Field(i)
		if field.Name == "Objects" || field.Name == "At" || field.Name == "By" {
			continue
		}
		sample, ok := samples[field.Type]
		if !ok {
			t.Fatalf("PackImport.%s is a %s: add a sample of it to this test", field.Name, field.Type)
		}
		forged := imp
		if err := json.Unmarshal([]byte(sample), reflect.ValueOf(&forged).Elem().Field(i).Addr().Interface()); err != nil {
			t.Fatal(err)
		}
		if err := packImportHeld(rec, forged); err == nil || !strings.Contains(err.Error(), "record 7") {
			t.Errorf("a record with %s forged: %v", field.Name, err)
		}
	}
}
