package server

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"yardr.dev/yardr/internal/pack"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/packs"
)

// serveRepositoryPack serves the pack yardr embeds under name as a pack of
// a stand-in packs repository, at <origin><name>/, and counts the requests
// the stand-in sees.
func serveRepositoryPack(t *testing.T, name string) (origin string, hits *atomic.Int64) {
	t.Helper()
	dir := t.TempDir()
	sub, err := fs.Sub(packs.FS, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(dir, sub); err != nil {
		t.Fatal(err)
	}
	hits = new(atomic.Int64)
	files := http.StripPrefix("/yardr-packs/main/"+name+"/", http.FileServer(http.Dir(dir)))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		files.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/yardr-packs/main/", hits
}

// delegatedServerYard stands the yard a delegated run needs on st (jo the
// operator, the yardmaster, delegation on, jo's mail to it) and answers the
// context of the yardmaster's command on that mail.
func delegatedServerYard(t *testing.T, st *store.Store, command string) context.Context {
	t.Helper()
	ctx := context.Background()
	if err := st.SetSetting(ctx, store.OperatorSetting, "jo", "jo"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddUser(ctx, "ada", "jo"); err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{"yardmaster", "clerk"} {
		if _, err := st.PutCrew(ctx, store.CrewMember{Name: r, Runner: "herdr"}, "jo"); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetSetting(ctx, store.DelegationSetting, "true", "jo"); err != nil {
		t.Fatal(err)
	}
	if err := st.Mail(ctx, "yardmaster", "apply the pack for me", "jo"); err != nil {
		t.Fatal(err)
	}
	mail, err := st.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return store.WithDelegation(ctx, store.DelegatedCall{For: "jo", Mail: mail, Command: command})
}

// A delegated pack apply of a pack of the packs repository through the
// server's own fetching: the pack is fetched by the server from the
// origin, held to its pin, and the store takes the run's objects and its
// source's text from that reading alone. The URL of another origin, a
// spelling the records do not keep, no pin and another digest each refuse
// before the store writes anything, the first before any request goes out.
func TestDelegatedPackApplyThroughTheServerFetcher(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	origin, hits := serveRepositoryPack(t, "telegram")
	from := origin + "telegram/"
	st := packReaderYard(t)
	// No reading of an embedded pack: the source's text is written from
	// the hash its record holds, and a write that read the pack again
	// inside the store's write would find nothing.
	st.SetPackFetcher(packFetcherFor(home, origin))
	dctx := delegatedServerYard(t, st, "yardr pack apply "+from+" --sha256 pinned")
	embedded, err := readEmbeddedPack(ctx, "telegram", "", store.Mappings{}, st, home)
	if err != nil {
		t.Fatal(err)
	}
	digest := embedded.Digest
	objects := []store.DelegatedPackObject{{Kind: "source", Name: "telegram", Action: "create"}}

	before := hits.Load()
	for name, tc := range map[string]struct {
		start store.DelegatedPackStart
		want  string
	}{
		"another origin on this host": {store.DelegatedPackStart{Pack: "telegram", Digest: digest, From: strings.Replace(from, "yardr-packs", "other-packs", 1), Objects: objects}, "is not in the packs repository"},
		"a spelling the records lack": {store.DelegatedPackStart{Pack: "telegram", Digest: digest, From: strings.TrimSuffix(from, "/"), Objects: objects}, "write it as " + from},
		"no pin":                      {store.DelegatedPackStart{Pack: "telegram", From: from, Objects: objects}, "applied pinned"},
	} {
		if _, err := st.StartDelegatedPack(dctx, tc.start, "yardmaster"); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
	if hits.Load() != before {
		t.Errorf("a start refused for its URL or its missing pin cost %d requests", hits.Load()-before)
	}
	if _, err := st.StartDelegatedPack(dctx, store.DelegatedPackStart{Pack: "telegram", Digest: strings.Repeat("a", 64), From: from, Objects: objects}, "yardmaster"); err == nil || !strings.Contains(err.Error(), "is not the one pinned") {
		t.Errorf("another digest: %v", err)
	}
	if _, err := st.StartDelegatedPack(dctx, store.DelegatedPackStart{Pack: "telegram", Digest: digest, From: from,
		Objects: []store.DelegatedPackObject{{Kind: "group", Name: "builders", Action: "create"}}}, "yardmaster"); err == nil || !strings.Contains(err.Error(), "no object group builders") {
		t.Errorf("an object outside the fetched plan: %v", err)
	}
	if _, err := st.Delegation(ctx, 1, "jo"); err == nil {
		t.Fatal("a refused start left a record")
	}

	p, err := pack.LoadRepository(origin, from, digest)
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
	rec, err := st.Delegation(ctx, 1, "jo")
	if err != nil || rec.State != "done" {
		t.Fatalf("record = %+v, %v; want it done", rec, err)
	}
	if body := recordBody(t, st, rec.ID); body.From != from || body.Digest != plan.Digest || body.Prior != "" {
		t.Fatalf("record body = %+v; want telegram from %s with digest %s", body, from, plan.Digest)
	}
}

// The reading of a pack of the repository is the reading of the same pack
// the binary embeds: its plan, its digest, its programs and its targets are
// the same, whether the files are read from the binary or from the first
// answer of the origin, apart from the hash that holds a declared source's
// text on the record.
func TestRepositoryReadingMatchesTheEmbeddedReading(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	origin, _ := serveRepositoryPack(t, "starter")
	from := origin + "starter/"
	st := packReaderYard(t)
	if err := st.PutDepot(ctx, store.Depot{Name: "web", Path: t.TempDir(), Base: "main"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	want, err := readEmbeddedPack(ctx, "starter", "web", store.Mappings{}, st, home)
	if err != nil {
		t.Fatal(err)
	}
	read, err := packFetcherFor(home, origin)(ctx, from, want.Digest)
	if err != nil {
		t.Fatal(err)
	}
	got, err := read(ctx, "starter", "web", store.Mappings{}, st)
	if err != nil {
		t.Fatal(err)
	}
	got.Label, want.Label = "", ""
	// The record of the import each reading says is the same apart from the
	// origin it names, which is where each pack came from.
	if got.Import == nil || want.Import == nil {
		t.Fatalf("a reading carries no record of the import: got %v, want %v", got.Import, want.Import)
	}
	got.Import.Source, want.Import.Source = "", ""
	for _, s := range got.Sources {
		delete(got.Targets, "source\x00"+s.Name)
	}
	if len(got.Targets) == 0 && len(want.Targets) == 0 {
		got.Targets, want.Targets = nil, nil
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the repository's reading differs from the embedded one:\n got %+v\nwant %+v", got, want)
	}
	if len(want.Plan.Objects) == 0 || want.Digest == "" {
		t.Errorf("the reading holds no plan: %+v", want)
	}
}
