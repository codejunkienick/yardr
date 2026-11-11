package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/pack"
	"yardr.dev/yardr/internal/store"
)

// A directory pack is read from the directory as it stands: its kind,
// each declared source's whole text with each command file's bytes, its
// plan and its digest, labelled with the directory it was read from.
func TestDirectoryPackReading(t *testing.T) {
	// Sequential: dirPack pins the test's git ceiling with t.Setenv.
	ctx := context.Background()
	st, home, _ := carryYard(t)
	dir := dirPack(t)
	reading, err := readDirectoryPack(ctx, dir, "", store.Mappings{}, st, home)
	if err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reading.Name != "dirmail" || reading.Kind != "source" || reading.Plan == nil || len(reading.Digest) != 64 {
		t.Fatalf("the reading = %+v", reading)
	}
	if !strings.HasPrefix(reading.Label, "directory "+abs+" in yardr ") || reading.Build == "" {
		t.Fatalf("the label = %q, build %q", reading.Label, reading.Build)
	}
	if len(reading.Sources) != 1 || reading.Sources[0].Name != "dirmail" || len(reading.Sources[0].Files) != 1 ||
		reading.Sources[0].Files[0].Program != "dirmail-mint" || reading.Sources[0].Files[0].Text != dirPackMint {
		t.Fatalf("the sources = %+v", reading.Sources)
	}
	if _, err := readDirectoryPack(ctx, t.TempDir(), "", store.Mappings{}, st, home); err == nil ||
		!strings.Contains(err.Error(), "pack.yaml") {
		t.Fatalf("an empty directory reads as %v", err)
	}
}

// dirPackDigestOf is the digest the stored pack apply card pins.
func dirPackDigestOf(t *testing.T, st *store.Store, bead string, seq uint64) string {
	t.Helper()
	notes, err := st.Notes(context.Background(), bead)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range store.ResolveAsks(notes, time.Now()) {
		if r.Note.Seq == seq && r.Ask.Run != nil && r.Ask.Run.Pack != nil {
			return r.Ask.Run.Pack.Digest
		}
	}
	t.Fatalf("no pack card %d on %s", seq, bead)
	return ""
}

// A directory pack of a depot kind goes from the ask to the installed
// files: the card shows the plan whole with the digest it pins, and the
// approval carries out exactly what the card showed. A pin round-trips,
// and a file changed since the ask stales the approval with nothing run.
func TestDirectoryPackApplyEndToEnd(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, home, bead := carryYard(t)
	st.SetDirectoryPackReader(directoryReaderFor(home))
	checkout := t.TempDir()
	dir := filepath.Join(checkout, "packs", "mini")
	writeMiniPack(t, dir, "build v1\n", "yardmaster v1\n")
	if err := st.PutDepot(ctx, store.Depot{Name: "packs", Path: checkout}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if err := st.PutDepot(ctx, store.Depot{Name: "yardr", Path: t.TempDir()}, "mckean"); err != nil {
		t.Fatal(err)
	}
	// The standing import, as the operator's terminal made it.
	p, err := pack.LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := pack.Build(ctx, st, p, pack.Options{Depot: "yardr", Home: home})
	if err != nil {
		t.Fatal(err)
	}
	answers := pack.Answers{}
	for _, q := range plan.Questions {
		switch q.Field {
		case "kind":
			answers[q.ID] = "claude"
		case "dir":
			answers[q.ID] = home
		default:
			answers[q.ID] = ""
		}
	}
	if _, err := pack.Apply(ctx, st, plan, pack.ApplyOptions{Answers: answers, Actor: "mckean"}); err != nil {
		t.Fatal(err)
	}
	writeMiniPack(t, dir, "build v2\n", "yardmaster v2\n")
	ask := packAskOf(dir)
	seq, err := st.AddAsk(ctx, bead, ask, "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	lines := resolvedLines(t, st, bead, seq)
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	digest := dirPackDigestOf(t, st, bead, seq)
	if len(digest) != 64 {
		t.Fatalf("the card pins %q", digest)
	}
	for _, want := range []string{
		"pack mini (directory " + resolved + " in yardr ",
		"of no kind, into depot yardr",
		"digest sha256:" + digest,
		"file roles/builder.md: change",
	} {
		if !strings.Contains(lines, want) {
			t.Errorf("the card lacks %q:\n%s", want, lines)
		}
	}
	if _, err := st.ApproveAsk(ctx, bead, seq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	drain(t, st, home, "boot-one")
	if rec := carryState(t, st, bead, seq); rec.State != store.CarryDone {
		t.Fatalf("the record = %+v", rec)
	}
	for name, want := range map[string]string{"builder.md": "build v2\n", "yardmaster.md": "yardmaster v2\n"} {
		data, err := os.ReadFile(filepath.Join(home, "packs", "mini", "roles", name))
		if err != nil || string(data) != want {
			t.Fatalf("the installed %s = %q, %v", name, data, err)
		}
	}
	// The pin round-trips: the digest the card printed pins the next ask,
	// and another digest refuses it.
	pinned := packAskOf(dir)
	pinned.Run.Argv = []string{"pack apply " + dir + " --sha256 " + digest}
	if _, err := st.AddAsk(ctx, bead, pinned, "yardmaster"); err != nil {
		t.Errorf("the card's digest pins no ask: %v", err)
	}
	wrong := packAskOf(dir)
	wrong.Run.Argv = []string{"pack apply " + dir + " --sha256 " + strings.Repeat("0", 64)}
	if _, err := st.AddAsk(ctx, bead, wrong, "yardmaster"); err == nil || !strings.Contains(err.Error(), "the pin is") {
		t.Errorf("another digest pins an ask: %v", err)
	}
	// A file changed since the ask stales the approval, and nothing runs.
	stale := packAskOf(dir)
	staleSeq, err := st.AddAsk(ctx, bead, stale, "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "roles", "builder.md"), []byte("build v3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveAsk(ctx, bead, staleSeq, "mckean", true, nil); err == nil ||
		!strings.Contains(err.Error(), "differs at the object file roles/builder.md") || !strings.Contains(err.Error(), "Nothing ran") {
		t.Fatalf("a changed file: ApproveAsk = %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(home, "packs", "mini", "roles", "builder.md")); err != nil || string(data) != "build v2\n" {
		t.Fatalf("the stale approval applied: %q, %v", data, err)
	}
}
