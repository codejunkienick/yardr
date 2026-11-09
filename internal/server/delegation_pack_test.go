package server

import (
	"context"
	"io/fs"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/pack"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/packs"
)

// A delegated pack apply through the server's own reading of the packs
// yardr embeds: the yardmaster applies telegram for the operator on her
// cited mail, the record the store derives from that reading stands
// done with the plan's digest, and a start naming objects the reading
// has no plan for refuses.
func TestDelegatedPackApplyThroughTheServerReading(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	st := packReaderYard(t)
	st.SetPackReader(packReaderFor(home))
	dctx := delegatedServerYard(t, st, "yardr pack apply telegram")

	reading, err := readEmbeddedPack(ctx, "telegram", "", store.Mappings{}, st, home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartDelegatedPack(dctx, store.DelegatedPackStart{Pack: "telegram", Digest: reading.Digest,
		Objects: []store.DelegatedPackObject{{Kind: "group", Name: "builders", Action: "create"}}}, "yardmaster"); err == nil || !strings.Contains(err.Error(), "no object group builders") {
		t.Errorf("a start naming an object outside the server's source plan: %v", err)
	}
	if _, err := st.StartDelegatedPack(dctx, store.DelegatedPackStart{Pack: "telegram", Digest: "wrong"}, "yardmaster"); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Errorf("a start with a caller's source digest: %v", err)
	}
	if _, err := st.StartDelegatedPack(dctx, store.DelegatedPackStart{Pack: "telegram", Depot: "web", Digest: reading.Digest}, "yardmaster"); err == nil || !strings.Contains(err.Error(), "reading is for depot") {
		t.Errorf("a source start with a depot: %v", err)
	}

	sub, err := fs.Sub(packs.FS, "telegram")
	if err != nil {
		t.Fatal(err)
	}
	p, err := pack.Load(sub, packs.Embedded)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := pack.Build(ctx, st, p, pack.Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pack.Apply(dctx, st, plan, pack.ApplyOptions{Actor: "yardmaster"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetSource(ctx, "telegram"); err != nil {
		t.Fatalf("telegram was not applied: %v", err)
	}
	var rec *store.Delegation
	for id := uint64(64); id > 0 && rec == nil; id-- {
		if d, err := st.Delegation(ctx, id, "jo"); err == nil {
			rec = d
		}
	}
	if rec == nil || rec.State != "done" || rec.DelegatedBody.Pack != "telegram" || rec.DelegatedBody.Digest != plan.Digest {
		t.Fatalf("record = %+v, want telegram done with digest %s", rec, plan.Digest)
	}
}
