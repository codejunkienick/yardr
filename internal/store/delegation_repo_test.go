package store_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"yardr.dev/yardr/internal/store"
)

const repoURL = "https://repo.example.com/yardr-packs/main/starter/"

// repoFetcher is the fetching of the packs repository a test store stands
// with: it counts its calls, may refuse, may run something while it fetches,
// and answers the reading of starter at the digest it was asked for, as the
// server's fetcher answers the reading of the pack it fetched and held to
// that digest.
type repoFetcher struct {
	calls  atomic.Int32
	refuse error
	during func()
}

func (f *repoFetcher) fetch(_ context.Context, from, digest string) (store.PackReader, error) {
	f.calls.Add(1)
	if f.during != nil {
		f.during()
	}
	if f.refuse != nil {
		return nil, f.refuse
	}
	return func(ctx context.Context, name string, depot string, _ store.Mappings, yard store.API) (*store.PackReading, error) {
		r, err := fakePackReader(ctx, name, depot, store.Mappings{}, yard)
		if err != nil {
			return nil, err
		}
		r.Digest = digest
		return r, nil
	}, nil
}

// A delegated start of a pack of the packs repository is held to what the
// store's own fetching reads, before its write and never in it: it takes a
// pin and a registered fetcher, asks the delegation before it fetches, and
// refuses what the fetcher refuses with nothing written. Its record names
// where the pack came from and the digest that stood before it, the one a
// pinned re-apply takes the run back to: the latest run that finished from
// the repository, not a failed one and not one of a pack yardr embeds.
func TestDelegatedPackFromTheRepository(t *testing.T) {
	t.Parallel()
	y := newDelegationYard(t)
	ctx := y.as(y.mail, "yardr pack apply "+repoURL+" --sha256 abc123 --depot shed")
	objects := []store.DelegatedPackObject{
		{Kind: "group", Name: "builders", Action: "change"},
		{Kind: "file", Name: "roles/builder.md", Action: "create"},
	}
	// The command names the URL it read the pack at as the source, as it does
	// in pack.Apply: it is the URL the store fetches.
	start := func(digest string) store.DelegatedPackStart {
		return store.DelegatedPackStart{Pack: "starter", Depot: "shed", Digest: digest, From: repoURL, Source: repoURL, Objects: objects,
			Mappings: store.Mappings{Crew: map[string]string{"builder": "clerk"}}}
	}
	f := &repoFetcher{}

	// No fetcher, no pin, and a caller the write would refuse each start
	// no fetch.
	if _, err := y.api.StartDelegatedPack(ctx, start("abc123"), "yardmaster"); err == nil || !strings.Contains(err.Error(), "no server has registered its fetching") {
		t.Errorf("no fetcher registered: %v", err)
	}
	y.own.SetPackFetcher(f.fetch)
	if _, err := y.api.StartDelegatedPack(ctx, start(""), "yardmaster"); err == nil || !strings.Contains(err.Error(), "applied pinned") {
		t.Errorf("no pin: %v", err)
	}
	if _, err := y.api.StartDelegatedPack(y.ctx, start("abc123"), "yardmaster"); err == nil || !strings.Contains(err.Error(), "takes a delegated run") {
		t.Errorf("no delegation: %v", err)
	}
	if _, err := y.api.StartDelegatedPack(ctx, start("abc123"), "clerk"); err == nil || !strings.Contains(err.Error(), "is the yardmaster's") {
		t.Errorf("the clerk: %v", err)
	}
	if n := f.calls.Load(); n != 0 {
		t.Errorf("a start that was refused first fetched %d times", n)
	}

	// What the fetcher refuses refuses the start, with no record.
	f.refuse = errors.New("pack starter is not the one pinned: the pin is sha256:abc123, the pack read has sha256:fff")
	if _, err := y.api.StartDelegatedPack(ctx, start("abc123"), "yardmaster"); err == nil || !strings.Contains(err.Error(), "is not the one pinned") {
		t.Errorf("a fetcher that refuses: %v", err)
	}
	if _, err := y.api.Delegation(y.ctx, 1, "jo"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a refused fetch left a record: %v", err)
	}
	f.refuse = nil

	// The source the command read the pack from is held to the URL the store
	// fetches: another is refused as what it is, not as two builds, and
	// leaves no record.
	other := start("abc123")
	other.Source = "https://evil.example/starter/"
	if _, err := y.api.StartDelegatedPack(ctx, other, "yardmaster"); err == nil || strings.Contains(err.Error(), "two builds") ||
		!strings.Contains(err.Error(), `"`+other.Source+`"`) || !strings.Contains(err.Error(), `"`+repoURL+`"`) || !strings.HasSuffix(err.Error(), "nothing was written") {
		t.Errorf("a start whose command read the pack from another URL: %v", err)
	}
	if _, err := y.api.Delegation(y.ctx, 1, "jo"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a start refused for its source left a record: %v", err)
	}

	// The fetch runs before the store's write: a write made while it
	// runs completes at once. Were the fetch inside the start's write, that
	// write would wait for the start's own; it waits here in a goroutine of
	// its own, so that the failure is this message and not a package that
	// hangs.
	f.during = func() {
		wrote := make(chan error, 1)
		go func() { wrote <- y.api.Mail(y.ctx, "yardmaster", "written while the pack is fetched", "jo") }()
		select {
		case err := <-wrote:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("the pack was fetched inside the store's write: a write made meanwhile did not complete")
		}
	}
	firstID, err := y.api.StartDelegatedPack(ctx, start("abc123"), "yardmaster")
	f.during = nil
	if err != nil {
		t.Fatal(err)
	}
	if got := y.body(t, firstID); got.From != repoURL || got.Prior != "" || got.Digest != "abc123" || !strings.Contains(got.Back, "no earlier delegated run") {
		t.Errorf("first record's body = %+v, want it from %s with no prior digest", got, repoURL)
	}
	if text := y.telling(t); !strings.Contains(text, "from "+repoURL+"; the digest that stood before: none known") {
		t.Errorf("the first telling = %q", text)
	}
	// The import a repository run records names the URL it fetched the pack
	// from, which a later apply by name reads again: no other source, and no
	// credential, passes.
	packCtx := store.WithDelegatedPack(ctx, firstID)
	imp := store.PackImport{Pack: "starter", Depot: "shed", Source: repoURL, Objects: fakePackImportObjects("the role the pack installs")}
	for name, bad := range map[string]func(*store.PackImport){
		"another URL":       func(i *store.PackImport) { i.Source = "https://evil.example/starter/" },
		"the binary's name": func(i *store.PackImport) { i.Source = "embedded in yardr test" },
		"a directory":       func(i *store.PackImport) { i.Source = "/srv/packs/starter" },
		"no source":         func(i *store.PackImport) { i.Source = "" },
		"a credential":      func(i *store.PackImport) { i.Auth = "packs" },
	} {
		forged := imp
		bad(&forged)
		if _, err := y.api.RecordPack(packCtx, forged, true, "yardmaster"); err == nil || !strings.Contains(err.Error(), "no other source and no credential") {
			t.Errorf("an import record with %s: %v", name, err)
		}
	}
	// Its objects are the reading's of the fetched pack, as an embedded
	// pack's are: the hash of a file edited since refuses, and so does a
	// record that leaves the file out.
	edited := imp
	edited.Objects = fakePackImportObjects("the role as someone edited it")
	if _, err := y.api.RecordPack(packCtx, edited, true, "yardmaster"); err == nil || !strings.Contains(err.Error(), " with its sha256 as "+store.TextSum("the role the pack installs")) {
		t.Errorf("an import record of the hash of an edited file from %s: %v", repoURL, err)
	}
	unfiled := imp
	unfiled.Objects = imp.Objects[:1] // the group alone
	if _, err := y.api.RecordPack(packCtx, unfiled, true, "yardmaster"); err == nil || !strings.Contains(err.Error(), "leaves out") {
		t.Errorf("an import record that leaves the file out from %s: %v", repoURL, err)
	}
	if _, err := y.api.RecordPack(packCtx, imp, true, "yardmaster"); err != nil {
		t.Errorf("the import's own record from %s: %v", repoURL, err)
	}
	finish := func(id uint64, ok bool) {
		t.Helper()
		if err := y.api.FinishDelegatedPack(ctx, id, ok, nil, "the disk is full", "yardmaster"); err != nil {
			t.Fatal(err)
		}
	}
	finish(firstID, true)

	// The next run names the digest that stood, and the words that pin a
	// re-apply to it, with the mappings of the run that put it there.
	secondID, err := y.api.StartDelegatedPack(ctx, start("def456"), "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	second := y.body(t, secondID)
	if second.Prior != "abc123" {
		t.Errorf("second record's prior = %q, want abc123", second.Prior)
	}
	for _, want := range []string{"yardr pack apply " + repoURL, "--sha256 sha256:abc123", "--depot shed", "--crew builder=clerk"} {
		if !strings.Contains(second.Back, want) {
			t.Errorf("the taking-back words lack %q: %s", want, second.Back)
		}
	}
	if text := y.telling(t); !strings.Contains(text, "the digest that stood before: sha256:abc123") {
		t.Errorf("the second telling = %q", text)
	}

	// A run that failed stood nowhere: the one after it still names the
	// last that finished.
	finish(secondID, false)
	thirdID, err := y.api.StartDelegatedPack(ctx, start("ghi789"), "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	if third := y.body(t, thirdID); third.Prior != "abc123" {
		t.Errorf("third record's body = %+v; want prior abc123 over a failed run", third)
	}
	finish(thirdID, true)

	// A pack yardr embeds names no digest to pin: the repository run after
	// it has no prior.
	y.own.SetPackReader(fakePackReader)
	embedded := start("abc123")
	embedded.From, embedded.Source = "", ""
	embeddedID, err := y.api.StartDelegatedPack(ctx, embedded, "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	if rec := y.body(t, embeddedID); rec.From != "" || rec.Prior != "" || !strings.Contains(rec.Back, "no one command takes a pack apply back") {
		t.Errorf("an embedded record's body = %+v; want no origin and no prior", rec)
	}
	finish(embeddedID, true)
	fifthID, err := y.api.StartDelegatedPack(ctx, start("jkl012"), "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	if fifth := y.body(t, fifthID); fifth.Prior != "" || !strings.Contains(fifth.Back, "no earlier delegated run") {
		t.Errorf("a repository run after an embedded one = %+v; want no prior", fifth)
	}
}

// A declared source a pack of the repository brings is written from the
// text's hash its record holds: the pack is not fetched again inside the
// write, so there is no reading to read the text from, and a text beside
// the hash refuses.
func TestDelegatedPackSourceOfTheRepositoryIsHeldByItsHash(t *testing.T) {
	t.Parallel()
	const wire = "name: wire\nbase: https://api.example.com\ncredential: { env: WIRE_TOKEN }\ncalls:\n  ping: { kind: read, request: { method: GET, path: /ping } }\n"
	y := newDelegationYard(t)
	y.own.SetPackFetcher(func(_ context.Context, from, digest string) (store.PackReader, error) {
		return func(_ context.Context, name string, _ string, _ store.Mappings, _ store.API) (*store.PackReading, error) {
			return &store.PackReading{Name: "wire", Kind: "source", Digest: digest,
				Plan:    &store.AskPlan{Objects: []store.AskPlanObject{{Kind: "source", Name: "wire", Action: "create"}}},
				Sources: []store.AskSource{{Name: "wire", Text: wire}},
				Targets: map[string]string{"source\x00wire": store.TextSum(wire)}}, nil
		}, nil
	})
	from := "https://repo.example.com/yardr-packs/main/wire/"
	ctx := y.as(y.mail, "yardr pack apply "+from+" --sha256 abc123")
	id, err := y.api.StartDelegatedPack(ctx, store.DelegatedPackStart{Pack: "wire", Digest: "abc123", From: from,
		Objects: []store.DelegatedPackObject{{Kind: "source", Name: "wire", Action: "create"}}}, "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	packCtx := store.WithDelegatedPack(ctx, id)
	if _, err := y.api.PutSource(packCtx, "wire.yaml", strings.Replace(wire, "/ping", "/pong", 1), "yardmaster"); err == nil || !strings.Contains(err.Error(), "is not the text pack wire carries") {
		t.Errorf("a text beside the record's hash: %v", err)
	}
	if _, err := y.api.PutSource(packCtx, "wire.yaml", wire, "yardmaster"); err != nil {
		t.Errorf("the pack's own text: %v", err)
	}
	if err := y.api.FinishDelegatedPack(ctx, id, true, []string{"source wire"}, "", "yardmaster"); err != nil {
		t.Fatal(err)
	}
}
