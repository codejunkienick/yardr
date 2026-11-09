package server

import (
	"context"
	"io/fs"
	"path/filepath"
	"reflect"
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
	// The pack is loaded under the label the reading carries, as the
	// command loads one yardr embeds: the import records it as its source.
	p, err := pack.Load(sub, reading.Label)
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
	if rec == nil || rec.State != "done" {
		t.Fatalf("record = %+v, want it done", rec)
	}
	if body := recordBody(t, st, rec.ID); body.Pack != "telegram" || body.Digest != plan.Digest {
		t.Fatalf("record body = %+v, want telegram with digest %s", body, plan.Digest)
	}
	t.Run("depot pack", delegatedDepotPackApplyRecordsWhatItDid)
	t.Run("frame pack", delegatedFramePackApplyRecordsItsFrame)
}

// recordBody is the body of the delegated record id as jo reads it, from
// its blob: where a pack apply's always stands, however small.
func recordBody(t *testing.T, st store.API, id uint64) store.DelegatedBody {
	t.Helper()
	body, err := st.DelegationBlob(context.Background(), id, "jo")
	if err != nil {
		t.Fatal(err)
	}
	return *body
}

// A delegated apply of a pack that names a depot, through the server's own
// reading, records the import as the same apply records it outside a
// delegation: the flow, the groups and crew the plan mapped, the setup
// notes and the answers it keeps. The record is held to the reading's, so
// this is where the plan the command builds and the one the server reads
// from the run's mappings must agree: for a first install answered by
// binds and members, and for a bare re-apply, whose mappings the command
// fills from its own plan.
func delegatedDepotPackApplyRecordsWhatItDid(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	st := packReaderYard(t)
	st.SetPackReader(packReaderFor(home))
	dctx := delegatedServerYard(t, st, "yardr pack apply starter --depot web")
	if err := st.PutDepot(ctx, store.Depot{Name: "web", Path: t.TempDir()}, "jo"); err != nil {
		t.Fatal(err)
	}
	// The yard hears linear and github, which the starter sets a stage map
	// on each of: a table of stages and a table of says lines whose filters
	// hold several values, objects a later plan starts from.
	for _, hears := range []string{"linear", "github"} {
		decl, err := fs.ReadFile(packs.FS, hears+"/"+hears+".yaml")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.PutSource(ctx, hears+".yaml", string(decl), "jo"); err != nil {
			t.Fatal(err)
		}
		if err := st.SetIdentity(ctx, "yardmaster", hears, store.Identity{ID: "u-yardmaster"}, "jo"); err != nil {
			t.Fatal(err)
		}
		if _, err := st.PutHook(ctx, store.Hook{Name: hears, Source: hears, SecretFile: filepath.Join(home, hears+".secret")}, "jo"); err != nil {
			t.Fatal(err)
		}
	}
	reading, err := readEmbeddedPack(ctx, "starter", "web", store.Mappings{}, st, home)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := fs.Sub(packs.FS, "starter")
	if err != nil {
		t.Fatal(err)
	}
	p, err := pack.Load(sub, reading.Label)
	if err != nil {
		t.Fatal(err)
	}
	apply := func(mappings store.Mappings) *store.PackImport {
		t.Helper()
		plan, err := pack.Build(ctx, st, p, packOptions("web", home, mappings))
		if err != nil {
			t.Fatal(err)
		}
		answers, open, calls, err := mappingAnswers(ctx, st, plan, mappings, home)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pack.Apply(dctx, st, plan, pack.ApplyOptions{Answers: answers, OpenThreads: open, Calls: calls, Expose: map[string]map[string]bool{},
			Actor: "yardmaster", DelegatedMappings: mappings}); err != nil {
			t.Fatal(err)
		}
		recs, err := st.Packs(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for i := range recs {
			if recs[i].Pack == "starter" && recs[i].Depot == "web" {
				return &recs[i]
			}
		}
		t.Fatalf("the import records = %+v, want starter in depot web", recs)
		return nil
	}

	first := apply(store.Mappings{Binds: []string{"builders=claude:opus", "reviewers=claude:opus"}, Members: []string{"backlog=jo", "decisions=jo"}})
	if first.Flow != "default" || first.Source != reading.Label || first.Groups["builders"] != "web-builders" || len(first.Objects) == 0 {
		t.Fatalf("the first install's record = %+v, want flow default from %q with the groups mapped", first, reading.Label)
	}
	// Its objects are held to the reading's too, and carry what a later plan
	// starts from: the hash of each file, the binding of the group that binds
	// as another, and the tables and crew member of the two hooks' maps.
	hooked := map[string]store.PackObject{}
	var hashed, bound int
	for _, o := range first.Objects {
		switch {
		case o.Kind == "map":
			hooked[o.Name] = o
		case o.Kind == "file" && o.SHA256 != "":
			hashed++
		case o.Kind == "group" && o.Binds != "":
			bound++
		}
	}
	if hashed == 0 || bound == 0 || len(hooked["linear"].Map) == 0 || len(hooked["github"].Says) == 0 || hooked["linear"].CrewMember != "yardmaster" || hooked["github"].CrewMember != "yardmaster" {
		t.Errorf("the first install's objects = %+v, want files with their hashes, a bound group and the maps of the linear and github hooks", first.Objects)
	}
	again := apply(store.Mappings{})
	if again.Flow != first.Flow || again.Source != first.Source || !reflect.DeepEqual(again.Groups, first.Groups) || !reflect.DeepEqual(again.Crew, first.Crew) {
		t.Errorf("the bare re-apply's record = %+v, want the first install's %+v", again, first)
	}
}

// The frame a delegated apply brings is recorded at the path the plan
// installs it at, as its scope's chosen one: the record is held to the
// reading's, so this is where the path the command's plan makes and the one
// the server's reading makes must agree (the store's own test refuses any
// other path, which is where the page frame is read from).
func delegatedFramePackApplyRecordsItsFrame(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	st := packReaderYard(t)
	st.SetPackReader(packReaderFor(home))
	dctx := delegatedServerYard(t, st, "yardr pack apply pageframe --depot web")
	if err := st.PutDepot(ctx, store.Depot{Name: "web", Path: t.TempDir()}, "jo"); err != nil {
		t.Fatal(err)
	}
	reading, err := readEmbeddedPack(ctx, "pageframe", "web", store.Mappings{}, st, home)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := fs.Sub(packs.FS, "pageframe")
	if err != nil {
		t.Fatal(err)
	}
	p, err := pack.Load(sub, reading.Label)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := pack.Build(ctx, st, p, packOptions("web", home, store.Mappings{}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pack.Apply(dctx, st, plan, pack.ApplyOptions{Actor: "yardmaster"}); err != nil {
		t.Fatal(err)
	}
	recs, err := st.Packs(ctx)
	if err != nil || len(recs) != 1 {
		t.Fatalf("the import records = %+v, %v", recs, err)
	}
	want := filepath.Join(home, "packs", "pageframe", p.Frame.File)
	if recs[0].Frame != want || !recs[0].FrameActive {
		t.Errorf("the frame recorded = %q (active %v), want %q chosen", recs[0].Frame, recs[0].FrameActive, want)
	}
}
