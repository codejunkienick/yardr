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

// An object of an import record under a started delegated pack record is
// held in every field, now that the reading carries the objects: a field
// added to PackObject later is held until the reading says it, and this
// test says which sample to forge it with. The reading's object that stands
// under the same identity is the one the record is held to, an empty table
// is none, and no object of the reading is no object to hold it to.
func TestPackObjectHeldHoldsEveryField(t *testing.T) {
	group := PackObject{Kind: "group", Name: "builders", Binds: "reviewers"}
	rec := &Delegation{ID: 7, Import: &PackImport{Objects: []PackObject{group}}}
	if err := packObjectHeld(rec, "starter", group); err != nil {
		t.Fatalf("the held object itself: %v", err)
	}
	samples := map[reflect.Type]string{
		reflect.TypeOf(""):                  `"x"`,
		reflect.TypeOf(&Subscription{}):     `{"source":"x","scope":"y"}`,
		reflect.TypeOf(map[string]string{}): `{"a":"b"}`,
		reflect.TypeOf([]Say{}):             `[{"where":{"a":"b"},"outcome":"x"}]`,
		reflect.TypeOf([]Make{}):            `[{"where":{"a":"b"},"type":"x","stage":"y"}]`,
	}
	typ := reflect.TypeOf(group)
	for i := range typ.NumField() {
		field := typ.Field(i)
		sample, ok := samples[field.Type]
		if !ok {
			t.Fatalf("PackObject.%s is a %s: add a sample of it to this test", field.Name, field.Type)
		}
		forged := group
		if err := json.Unmarshal([]byte(sample), reflect.ValueOf(&forged).Elem().Field(i).Addr().Interface()); err != nil {
			t.Fatal(err)
		}
		if err := packObjectHeld(rec, "starter", forged); err == nil || !strings.Contains(err.Error(), "record 7") {
			t.Errorf("an object with %s forged: %v", field.Name, err)
		}
	}
	if err := packObjectHeld(&Delegation{ID: 7}, "starter", group); err == nil || !strings.Contains(err.Error(), "stands on no delegated pack record 7") {
		t.Errorf("an object against a record that holds none: %v", err)
	}

	// An empty table, an empty filter and none are the same object, as the
	// store keeps them and as a caller over the socket sends them; the
	// comparison changes neither the held object nor the caller's.
	hook := PackObject{Kind: "map", Name: "github", CrewMember: "yardmaster", Says: []Say{{Outcome: "done"}}, Makes: []Make{{Type: "task", Stage: "new"}}}
	sub := PackObject{Kind: "subscription", CrewMember: "yardmaster", Subscription: &Subscription{Source: "yardr", Scope: "all"}}
	rec.Import.Objects = append(rec.Import.Objects, hook, sub)
	emptied := hook
	emptied.Map, emptied.Says, emptied.Makes = map[string]string{}, []Say{{Outcome: "done", Where: map[string]string{}}}, []Make{{Type: "task", Stage: "new", Where: map[string]string{}}}
	subbed := sub
	subbed.Subscription = &Subscription{Source: "yardr", Scope: "all", Where: map[string]string{}}
	for name, po := range map[string]PackObject{"a map with empty tables": emptied, "a subscription with an empty filter": subbed} {
		if err := packObjectHeld(rec, "starter", po); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if emptied.Says[0].Where == nil || subbed.Subscription.Where == nil || rec.Import.Objects[1].Says[0].Where != nil || rec.Import.Objects[2].Subscription.Where != nil {
		t.Error("the comparison changed an object it was given")
	}

	// Objects the reading names under one identity (Key) are each the
	// record's to be: two scopes of one subscription are two subscriptions.
	other := PackObject{Kind: "subscription", CrewMember: "yardmaster", Subscription: &Subscription{Source: "yardr", Scope: "mine"}}
	rec.Import.Objects = append(rec.Import.Objects, other)
	for name, po := range map[string]PackObject{"the first scope": sub, "the second scope": other} {
		if err := packObjectHeld(rec, "starter", po); err != nil {
			t.Errorf("%s of a subscription the reading names twice: %v", name, err)
		}
	}
	third := PackObject{Kind: "subscription", CrewMember: "yardmaster", Subscription: &Subscription{Source: "yardr", Scope: "forged"}}
	if err := packObjectHeld(rec, "starter", third); err == nil || strings.Contains(err.Error(), "forged") || !strings.Contains(err.Error(), "with its subscription as") {
		t.Errorf("a third scope of a subscription the reading names twice: %v", err)
	}
}
