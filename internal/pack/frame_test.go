package pack_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/config"
	"yardr.dev/yardr/internal/pack"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/packs"
)

// pageFrame is a frame that meets the contract: its title block is its <h1>
// on line 8, its content on line 9. The braces in its script are its own.
const pageFrame = `<!DOCTYPE html>
<html>
<head>
<title>untitled</title>
<script>const who = "{{operator}}", other = "{{nope}}"; if (who) { go() }</script>
</head>
<body>
<h1><!--yardr:title--></h1>
<!--yardr:content-->
</body>
</html>
`

func framePack(t *testing.T, yaml, frame string) string {
	t.Helper()
	return writePack(t, map[string]string{"pack.yaml": yaml, "frames/page.html": frame})
}

// A pack's frame is copied as it stands: nothing is filled in it, it is not
// made executable since it is no program, and the plan names it and runs
// nothing. The digest pins it, the record keeps the bytes' sum, a second
// import changes nothing, a hand edit is refused, and a frame the pack drops
// is reported, and removed only when asked.
func TestAPageFrameIsCopiedAsItStandsAndRecorded(t *testing.T) {
	st, o := yard(t)
	const yaml = "name: framed\napi: 4\nframe: frames/page.html\n"
	dir := framePack(t, yaml, pageFrame)
	installed := filepath.Join(config.At(o.Home).Pack("framed"), "frames", "page.html")
	wantSum := sha256.Sum256([]byte(pageFrame))

	p := load(t, dir)
	if p.API != 4 || p.Frame == nil || *p.Frame != (pack.FrameFile{File: "frames/page.html", Bytes: len(pageFrame)}) || !slices.Equal(p.Files(), []string{"frames/page.html"}) {
		t.Fatalf("pack = api %d, frame %+v, files %v", p.API, p.Frame, p.Files())
	}
	if p.Filled("frames/page.html") {
		t.Error("the import would fill placeholders in the frame")
	}
	plan := build(t, st, p, o)
	if plan.Frame == nil || *plan.Frame != *p.Frame || plan.Programs == nil || len(plan.Programs) != 0 {
		t.Fatalf("plan frame %+v, programs %#v: want the frame named and no program", plan.Frame, plan.Programs)
	}
	if want := recordsDigest(map[string]string{"pack.yaml": yaml, "frames/page.html": pageFrame}); plan.Digest != want {
		t.Errorf("digest = %s, want %s: pack.yaml and the frame", plan.Digest, want)
	}
	f := find(t, plan, "file", "frames/page.html")
	if f.Action != pack.Create || f.File.Mode != "0644" || f.File.Fill || f.File.To != installed || f.File.SHA256 != hex.EncodeToString(wantSum[:]) {
		t.Fatalf("the frame's file object = %+v %+v", f, f.File)
	}

	res := apply(t, st, plan, nil)
	if res.Created != 1 || !res.Recorded {
		t.Fatalf("first import = %+v", res)
	}
	if text, err := os.ReadFile(installed); err != nil || string(text) != pageFrame {
		t.Fatalf("installed frame = %q, %v: want the frame byte for byte", text, err)
	}
	if info, err := os.Stat(installed); err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("installed frame: %v %v, want mode 0644 and no executable bit", info, err)
	}
	rec := record(t, st)
	if !slices.ContainsFunc(rec.Objects, func(po store.PackObject) bool {
		return po.Kind == "file" && po.Name == installed && po.SHA256 == hex.EncodeToString(wantSum[:])
	}) {
		t.Fatalf("record objects = %+v, want the frame's file with the sum of its bytes", rec.Objects)
	}

	// Again, with nothing changed.
	before := head(t, st)
	plan = build(t, st, load(t, dir), o)
	if got := actions(plan, pack.Unchanged); !slices.Equal(got, []string{"file frames/page.html"}) {
		t.Fatalf("second plan unchanged = %v", got)
	}
	if res = apply(t, st, plan, nil); res.Wrote() || res.Recorded {
		t.Fatalf("second import = %+v", res)
	}
	if got := kinds(t, st, before); !slices.Equal(got, []string{"pack_unchanged"}) {
		t.Fatalf("the second import logged %v, want pack_unchanged alone", got)
	}

	// A copy edited in the yard is the operator's: not overwritten unasked.
	if err := os.WriteFile(installed, []byte("edited in the yard\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan = build(t, st, load(t, dir), o)
	if f := find(t, plan, "file", "frames/page.html"); f.Action != pack.Change || !f.Edited {
		t.Fatalf("frame after a hand edit = %+v", f)
	}
	if _, err := pack.Apply(ctx, st, plan, pack.ApplyOptions{Actor: "me"}); err == nil || !strings.Contains(err.Error(), installed+" was edited by hand") {
		t.Fatalf("applying over a hand edit: %v", err)
	}
	if res, err := pack.Apply(ctx, st, plan, pack.ApplyOptions{Actor: "me", Overwrite: true}); err != nil || res.Changed != 1 {
		t.Fatalf("told to overwrite: %+v, %v", res, err)
	}

	// The pack drops its frame: reported, left, and removed only by --prune.
	edit(t, dir, "frame: frames/page.html\n", "")
	plan = build(t, st, load(t, dir), o)
	if plan.Frame != nil {
		t.Errorf("a pack without the key has plan frame %+v", plan.Frame)
	}
	if got := actions(plan, pack.Dropped); !slices.Equal(got, []string{"file frames/page.html"}) {
		t.Fatalf("dropped = %v", got)
	}
	if res = apply(t, st, plan, nil); len(res.Pruned) != 0 || !slices.Equal(res.Left, []string{"file frames/page.html"}) {
		t.Fatalf("without --prune: %+v", res)
	}
	if _, err := os.Stat(installed); err != nil {
		t.Fatalf("the frame was removed without --prune: %v", err)
	}
	plan = build(t, st, load(t, dir), o)
	res, err := pack.Apply(ctx, st, plan, pack.ApplyOptions{Actor: "me", Prune: true})
	if err != nil || !slices.Equal(res.Pruned, []string{"file frames/page.html"}) || len(res.Left) != 0 {
		t.Fatalf("with --prune: %+v, %v", res, err)
	}
	if _, err := os.Stat(installed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the pruned frame remains: %v", err)
	}
}

// A frame is no declaration and none of its programs, which an import reads
// and runs: naming either as the frame is refused with both names.
func TestAFrameIsNoDeclarationFile(t *testing.T) {
	t.Parallel()
	const decl = "name: acme\nbase: https://acme.test\ncredential: { env: ACME_TOKEN }\ncalls:\n  ping:\n    kind: read\n    run: [acme/run, ping]\n"
	for _, c := range []struct{ name, frame, what string }{
		{"a declaration", "acme.yaml", "the declaration of source acme"},
		{"a program of a declaration", "acme/run", "a program of the declaration of source acme"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := writePack(t, map[string]string{
				"pack.yaml": "name: acme-flow\napi: 4\nsources:\n  acme:\n    declaration: acme.yaml\nframe: " + c.frame + "\n",
				"acme.yaml": decl, "acme/run": pageFrame,
			})
			_, err := pack.LoadDir(dir)
			var errs pack.Errors
			if !errors.As(err, &errs) {
				t.Fatalf("err = %v, want the pack's errors", err)
			}
			want := filepath.Join(dir, "pack.yaml")
			if !slices.ContainsFunc(errs, func(e pack.Error) bool {
				return e.File == want && e.Line == 6 && e.Key == "frame" && strings.Contains(e.Msg, "frame "+c.frame+" is also "+c.what)
			}) {
				t.Fatalf("no error at pack.yaml:6 frame naming both:\n%v", errs)
			}
		})
	}
}

// A frame may hold 256 KiB, whatever its own bytes are, and not a byte more:
// it is copied into every page made under it.
func TestAFrameMayHoldMaxFrameBytes(t *testing.T) {
	t.Parallel()
	padded := func(n int) string {
		frame := strings.Replace(pageFrame, "<script>", "<style>"+strings.Repeat("a", n-len(pageFrame)-len("<style></style>\n"))+"</style>\n<script>", 1)
		if len(frame) != n {
			t.Fatalf("frame is %d bytes, want %d", len(frame), n)
		}
		return frame
	}
	const yaml = "name: framed\napi: 4\nframe: frames/page.html\n"
	if p, err := pack.LoadDir(framePack(t, yaml, padded(pack.MaxFrame))); err != nil || p.Frame.Bytes != pack.MaxFrame {
		t.Fatalf("a frame of %d bytes: %v", pack.MaxFrame, err)
	}
	_, err := pack.LoadDir(framePack(t, yaml, padded(pack.MaxFrame+1)))
	if want := fmt.Sprintf("frame frames/page.html holds %d bytes: at most %d", pack.MaxFrame+1, pack.MaxFrame); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("a frame of %d bytes: %v, want %q", pack.MaxFrame+1, err, want)
	}
}

// frameVariant is pageFrame with another document title, so two packs'
// frames differ byte for byte and sum for sum.
func frameVariant(name string) string { return strings.Replace(pageFrame, "untitled", name, 1) }

// framedDir writes a frame-only pack of the name, carrying frameVariant(name).
func framedDir(t *testing.T, name string) string {
	t.Helper()
	return framePack(t, "name: "+name+"\napi: 4\nframe: frames/page.html\n", frameVariant(name))
}

// recordOf is the yard's record of packName in depot ("" for the yard).
func recordOf(t *testing.T, st store.API, packName, depot string) store.PackImport {
	t.Helper()
	recs, err := st.Packs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if r.Pack == packName && r.Depot == depot {
			return r
		}
	}
	t.Fatalf("no record of %s in %q; records = %+v", packName, depot, recs)
	return store.PackImport{}
}

// resolve is the frame depot's pages would be made in.
func resolve(t *testing.T, st store.API, home, depot string) pack.ResolvedFrame {
	t.Helper()
	f, err := pack.FrameFor(ctx, st, home, depot)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func sumOf(text string) string {
	s := sha256.Sum256([]byte(text))
	return hex.EncodeToString(s[:])
}

// Resolution takes the depot's active frame over the yard's over the
// embedded default, and a scope with no depot (a bead with none, a mail
// page) takes the yard's over the default. The sum frameFor returns is of
// the bytes it read: a hand-edited installed frame is used as it stands,
// so its sum moves away from the sum the import recorded on the pack
// object, which stays the checked and imported one.
func TestFrameForResolvesDepotThenYardThenDefault(t *testing.T) {
	st, o := yard(t)
	embedded, err := fs.ReadFile(packs.FS, packs.PageFrame+"/frames/page.html")
	if err != nil {
		t.Fatal(err)
	}

	// Nothing imported: the embedded default, read from the binary.
	for _, depot := range []string{"web", ""} {
		if f := resolve(t, st, o.Home, depot); f.Pack != packs.PageFrame || f.Path != "" || string(f.Bytes) != string(embedded) || f.SHA256 != sumOf(string(embedded)) {
			t.Fatalf("the default for %q = pack %s, path %q, %d bytes: want pageframe from the binary", depot, f.Pack, f.Path, len(f.Bytes))
		}
	}

	// A frame-only pack applied with no depot is the yard's frame.
	yardOpts := pack.Options{Home: o.Home}
	apply(t, st, build(t, st, load(t, framedDir(t, "brand-yard")), yardOpts), nil)
	yardInstalled := filepath.Join(config.At(o.Home).Pack("brand-yard"), "frames", "page.html")
	if rec := recordOf(t, st, "brand-yard", ""); rec.Frame != yardInstalled || !rec.FrameActive {
		t.Fatalf("the yard's record = frame %q, active %v", rec.Frame, rec.FrameActive)
	}
	for _, depot := range []string{"web", ""} {
		if f := resolve(t, st, o.Home, depot); f.Pack != "brand-yard" || f.Path != yardInstalled || f.SHA256 != sumOf(frameVariant("brand-yard")) {
			t.Fatalf("the yard's frame for %q = %+v", depot, f)
		}
	}

	// A frame pack applied into the depot is the depot's, and the yard's
	// choice is untouched.
	apply(t, st, build(t, st, load(t, framedDir(t, "brand-a")), o), nil)
	installed := filepath.Join(config.At(o.Home).Pack("brand-a"), "frames", "page.html")
	if rec := recordOf(t, st, "brand-a", "web"); rec.Frame != installed || !rec.FrameActive {
		t.Fatalf("brand-a's record = frame %q, active %v", rec.Frame, rec.FrameActive)
	}
	if rec := recordOf(t, st, "brand-yard", ""); !rec.FrameActive {
		t.Fatalf("the yard's record lost its choice: %+v", rec)
	}
	if f := resolve(t, st, o.Home, "web"); f.Pack != "brand-a" || f.Path != installed {
		t.Fatalf("web's frame = %+v, want brand-a's", f)
	}
	if f := resolve(t, st, o.Home, ""); f.Pack != "brand-yard" {
		t.Fatalf("the yard's frame = %+v, want brand-yard's still", f)
	}

	// Edited by hand in the yard: used as it stands, under the sum of the
	// bytes read, while the record keeps the sum of the bytes imported.
	edited := frameVariant("edited by hand")
	if err := os.WriteFile(installed, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if f := resolve(t, st, o.Home, "web"); f.SHA256 != sumOf(edited) || string(f.Bytes) != edited {
		t.Fatalf("the edited frame resolved to %q: want the edited bytes and their sum", f.SHA256)
	}
	rec := recordOf(t, st, "brand-a", "web")
	if !slices.ContainsFunc(rec.Objects, func(po store.PackObject) bool {
		return po.Kind == "file" && po.Name == installed && po.SHA256 == sumOf(frameVariant("brand-a"))
	}) {
		t.Fatalf("the record's objects = %+v, want the imported sum %s apart from the edited bytes' %s", rec.Objects, sumOf(frameVariant("brand-a")), sumOf(edited))
	}
}

// A record names its frame by an absolute path, and any Admin caller of
// the socket (a crew member's pane among them) can write one: resolution
// reads a frame only inside its pack's installed directory, so a record
// naming another file, by its path, by a pack name that climbs out, or by a
// symlink in the pack's directory, is refused and the file's bytes never
// reach a page.
func TestFrameForReadsOnlyInsideThePacksDirectory(t *testing.T) {
	st, o := yard(t)
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := config.At(o.Home).Pack("brand-a")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "page.html")); err != nil {
		t.Fatal(err)
	}
	for _, rec := range []store.PackImport{
		{Pack: "brand-a", Depot: "web", Frame: secret, FrameActive: true},
		{Pack: "brand-a", Depot: "web", Frame: filepath.Join(dir, "..", "..", "..", secret), FrameActive: true},
		{Pack: "../../" + filepath.Base(filepath.Dir(secret)), Depot: "web", Frame: secret, FrameActive: true},
		{Pack: "brand-a", Depot: "web", Frame: filepath.Join(dir, "page.html"), FrameActive: true},
	} {
		if _, err := st.RecordPack(ctx, rec, true, "me"); err != nil {
			t.Fatal(err)
		}
		f, err := pack.FrameFor(ctx, st, o.Home, "web")
		if err == nil || strings.Contains(string(f.Bytes), "a key") {
			t.Fatalf("pack %q, frame %q resolved to %q, %v: want it refused", rec.Pack, rec.Frame, f.Bytes, err)
		}
	}
}

// Import frame pack A into a depot, then frame pack B, then re-apply A
// unchanged: A is active again, the plan says the choice would move and
// the result says it moved though no file was written. Re-applying the
// already-active pack unchanged stays pack_unchanged.
func TestReapplyingADisplacedFramePackMovesTheChoice(t *testing.T) {
	st, o := yard(t)
	dirA, dirB := framedDir(t, "brand-a"), framedDir(t, "brand-b")

	plan := build(t, st, load(t, dirA), o)
	if want := "frame: depot web would use brand-a's frame (was pageframe's)"; plan.FrameUse != want {
		t.Fatalf("the first plan's frame line = %q, want %q", plan.FrameUse, want)
	}
	apply(t, st, plan, nil)
	apply(t, st, build(t, st, load(t, dirB), o), nil)
	if rec := recordOf(t, st, "brand-a", "web"); rec.FrameActive {
		t.Fatalf("brand-a is still the choice after brand-b: %+v", rec)
	}
	if f := resolve(t, st, o.Home, "web"); f.Pack != "brand-b" {
		t.Fatalf("web's frame = %s, want brand-b", f.Pack)
	}

	// A again, unchanged: no file to write, the choice alone moves.
	before := head(t, st)
	plan = build(t, st, load(t, dirA), o)
	if want := "frame: depot web would use brand-a's frame (was brand-b's)"; plan.FrameUse != want {
		t.Fatalf("the re-apply plan's frame line = %q, want %q", plan.FrameUse, want)
	}
	if f := find(t, plan, "file", "frames/page.html"); f.Action != pack.Unchanged {
		t.Fatalf("brand-a's frame again = %+v, want unchanged", f)
	}
	res := apply(t, st, plan, nil)
	if res.Wrote() || !res.Recorded || !res.FrameMoved {
		t.Fatalf("re-applying the displaced pack = %+v, want no file written, the record written and the choice moved", res)
	}
	if got := kinds(t, st, before); !slices.Equal(got, []string{"pack_applied"}) {
		t.Fatalf("the re-apply logged %v, want pack_applied alone", got)
	}
	if rec := recordOf(t, st, "brand-a", "web"); !rec.FrameActive {
		t.Fatalf("brand-a is not the choice again: %+v", rec)
	}
	if rec := recordOf(t, st, "brand-b", "web"); rec.FrameActive {
		t.Fatalf("brand-b is still the choice: %+v", rec)
	}

	// The already-active pack again: nothing moves, nothing is written.
	before = head(t, st)
	plan = build(t, st, load(t, dirA), o)
	if plan.FrameUse != "" {
		t.Fatalf("the active pack's plan has a frame line %q", plan.FrameUse)
	}
	if res := apply(t, st, plan, nil); res.Wrote() || res.Recorded || res.FrameMoved {
		t.Fatalf("re-applying the active pack = %+v", res)
	}
	if got := kinds(t, st, before); !slices.Equal(got, []string{"pack_unchanged"}) {
		t.Fatalf("the second re-apply logged %v, want pack_unchanged alone", got)
	}
}

// Applying a pack without a frame changes no scope's choice, and the pack
// that was the choice stays it.
func TestApplyingAPackWithoutAFrameChangesNoSelection(t *testing.T) {
	st, o := yard(t)
	apply(t, st, build(t, st, load(t, framedDir(t, "brand-a")), o), nil)

	plain := writePack(t, map[string]string{"pack.yaml": "name: plain\napi: 4\n"})
	plan := build(t, st, load(t, plain), o)
	if plan.Frame != nil || plan.FrameUse != "" {
		t.Fatalf("a frame-less pack's plan = frame %+v, line %q", plan.Frame, plan.FrameUse)
	}
	apply(t, st, plan, nil)
	if rec := recordOf(t, st, "plain", "web"); rec.Frame != "" || rec.FrameActive {
		t.Fatalf("the frame-less record = frame %q, active %v", rec.Frame, rec.FrameActive)
	}
	if f := resolve(t, st, o.Home, "web"); f.Pack != "brand-a" {
		t.Fatalf("web's frame = %s, want brand-a still", f.Pack)
	}
}

// When the active pack is next imported without a frame, its scope falls
// back a step and no other pack is promoted: brand-b displaced brand-a in
// the depot, drops its frame, and the depot takes the yard's frame, with
// brand-a inactive still.
func TestDroppingTheActiveFrameFallsBackWithNoPromotion(t *testing.T) {
	st, o := yard(t)
	apply(t, st, build(t, st, load(t, framedDir(t, "brand-yard")), pack.Options{Home: o.Home}), nil)
	dirB := framedDir(t, "brand-b")
	apply(t, st, build(t, st, load(t, framedDir(t, "brand-a")), o), nil)
	apply(t, st, build(t, st, load(t, dirB), o), nil)
	if f := resolve(t, st, o.Home, "web"); f.Pack != "brand-b" {
		t.Fatalf("web's frame = %s, want brand-b", f.Pack)
	}

	edit(t, dirB, "frame: frames/page.html\n", "")
	plan := build(t, st, load(t, dirB), o)
	if got := actions(plan, pack.Dropped); !slices.Equal(got, []string{"file frames/page.html"}) {
		t.Fatalf("dropped = %v, want the frame file alone", got)
	}
	res := apply(t, st, plan, nil)
	if len(res.Pruned) != 0 || !slices.Equal(res.Left, []string{"file frames/page.html"}) {
		t.Fatalf("without --prune: %+v", res)
	}
	if rec := recordOf(t, st, "brand-b", "web"); rec.Frame != "" || rec.FrameActive {
		t.Fatalf("brand-b's record after the drop = frame %q, active %v", rec.Frame, rec.FrameActive)
	}
	if rec := recordOf(t, st, "brand-a", "web"); rec.FrameActive {
		t.Fatalf("brand-a was promoted by brand-b's drop: %+v", rec)
	}
	if f := resolve(t, st, o.Home, "web"); f.Pack != "brand-yard" {
		t.Fatalf("web's frame after the drop = %s, want the yard's brand-yard", f.Pack)
	}
}

// prune applies the plan with --prune, as pack apply --prune does.
func prune(t *testing.T, st store.API, plan *pack.Plan) *pack.Result {
	t.Helper()
	res, err := pack.Apply(ctx, st, plan, pack.ApplyOptions{Answers: answered(t, plan, nil), Actor: "me", Prune: true})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// A dropped frame file is one installed copy shared by every scope: a
// prune in one scope keeps it while another scope's active record names
// it, the plan naming the holders, and only the last active scope's drop
// lets it go. The importing scope falls back at once, and the holding
// scope's next page is still framed. Two depots first, then a depot and
// the yard.
func TestPruningADroppedFrameIsHeldByTheScopesStillUsingIt(t *testing.T) {
	st, o := yard(t)
	if err := st.PutDepot(ctx, store.Depot{Name: "site", Base: "main"}, "me"); err != nil {
		t.Fatal(err)
	}
	site := pack.Options{Depot: "site", Home: o.Home}
	yardOpts := pack.Options{Home: o.Home}
	apply(t, st, build(t, st, load(t, framedDir(t, "brand-yard")), yardOpts), nil)

	// Two depots share brand's one installed copy.
	dir := framedDir(t, "brand")
	installed := filepath.Join(config.At(o.Home).Pack("brand"), "frames", "page.html")
	apply(t, st, build(t, st, load(t, dir), o), nil)
	apply(t, st, build(t, st, load(t, dir), site), nil)

	edit(t, dir, "frame: frames/page.html\n", "")
	plan := build(t, st, load(t, dir), o)
	dropped := find(t, plan, "file", "frames/page.html")
	if dropped.Action != pack.Dropped || dropped.Held != "still the active frame in depot site" {
		t.Fatalf("the dropped frame in web's plan = %+v, want held by depot site", dropped)
	}
	res := prune(t, st, plan)
	if !slices.Equal(res.Left, []string{"file frames/page.html"}) || len(res.Pruned) != 0 {
		t.Fatalf("pruning in web = %+v, want the held file left", res)
	}
	if _, err := os.Stat(installed); err != nil {
		t.Fatalf("the held frame was removed: %v", err)
	}
	if f := resolve(t, st, o.Home, "site"); f.Pack != "brand" {
		t.Fatalf("site's frame = %s, want brand still", f.Pack)
	}
	if f := resolve(t, st, o.Home, "web"); f.Pack != "brand-yard" {
		t.Fatalf("web's frame = %s, want the yard's brand-yard it fell back to", f.Pack)
	}

	// The last depot drops it: the file goes.
	plan = build(t, st, load(t, dir), site)
	if dropped := find(t, plan, "file", "frames/page.html"); dropped.Held != "" {
		t.Fatalf("the dropped frame in site's plan is held: %+v", dropped)
	}
	if res := prune(t, st, plan); !slices.Equal(res.Pruned, []string{"file frames/page.html"}) || len(res.Left) != 0 {
		t.Fatalf("pruning in site = %+v", res)
	}
	if _, err := os.Stat(installed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the frame remains after its last scope dropped it: %v", err)
	}
	if f := resolve(t, st, o.Home, "site"); f.Pack != "brand-yard" {
		t.Fatalf("site's frame = %s, want the yard's brand-yard", f.Pack)
	}

	// One depot and the yard share brand-two's copy the same way.
	dir = framedDir(t, "brand-two")
	installed = filepath.Join(config.At(o.Home).Pack("brand-two"), "frames", "page.html")
	apply(t, st, build(t, st, load(t, dir), o), nil)
	apply(t, st, build(t, st, load(t, dir), yardOpts), nil)
	edit(t, dir, "frame: frames/page.html\n", "")
	plan = build(t, st, load(t, dir), o)
	if dropped := find(t, plan, "file", "frames/page.html"); dropped.Held != "still the active frame in the yard" {
		t.Fatalf("the dropped frame in web's plan = %+v, want held by the yard", dropped)
	}
	if res := prune(t, st, plan); len(res.Pruned) != 0 {
		t.Fatalf("pruning in web pruned the held frame: %+v", res)
	}
	if f := resolve(t, st, o.Home, ""); f.Pack != "brand-two" {
		t.Fatalf("the yard's frame = %s, want brand-two still", f.Pack)
	}
	plan = build(t, st, load(t, dir), yardOpts)
	if res := prune(t, st, plan); !slices.Equal(res.Pruned, []string{"file frames/page.html"}) {
		t.Fatalf("pruning in the yard = %+v", res)
	}
	if _, err := os.Stat(installed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the frame remains after the yard dropped it: %v", err)
	}
}

// Applying a changed version of a pack under the same name replaces the
// one installed copy every scope that names it reads, and the plan names
// the other scopes before anything is written.
func TestAChangedFramePackServesEveryScopeThatNamesIt(t *testing.T) {
	st, o := yard(t)
	if err := st.PutDepot(ctx, store.Depot{Name: "site", Base: "main"}, "me"); err != nil {
		t.Fatal(err)
	}
	site := pack.Options{Depot: "site", Home: o.Home}
	dir := framedDir(t, "brand")
	installed := filepath.Join(config.At(o.Home).Pack("brand"), "frames", "page.html")
	apply(t, st, build(t, st, load(t, dir), o), nil)
	apply(t, st, build(t, st, load(t, dir), site), nil)
	apply(t, st, build(t, st, load(t, dir), pack.Options{Home: o.Home}), nil)

	if err := os.WriteFile(filepath.Join(dir, "frames", "page.html"), []byte(frameVariant("brand v2")), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := build(t, st, load(t, dir), o)
	if f := find(t, plan, "file", "frames/page.html"); f.Action != pack.Change {
		t.Fatalf("the changed frame = %+v, want a change", f)
	}
	if plan.FrameUse != "" {
		t.Fatalf("brand is already web's choice, and its plan has a frame line %q", plan.FrameUse)
	}
	if !slices.Equal(plan.FrameShared, []string{"depot site", "the yard"}) {
		t.Fatalf("the plan's shared scopes = %v, want depot site and the yard", plan.FrameShared)
	}
	apply(t, st, plan, nil)
	if text, err := os.ReadFile(installed); err != nil || string(text) != frameVariant("brand v2") {
		t.Fatalf("the installed copy = %q, %v: want the changed frame", text, err)
	}
	for _, depot := range []string{"site", ""} {
		if f := resolve(t, st, o.Home, depot); f.SHA256 != sumOf(frameVariant("brand v2")) {
			t.Fatalf("%q reads frame %s, want the changed copy's sum", depot, f.SHA256)
		}
	}

	// The same pack can move its frame key while other scopes still name
	// its old installed path. Those scopes keep that file and must not be
	// told that a change to the new path reaches them.
	newRel := "frames/another.html"
	newInstalled := filepath.Join(config.At(o.Home).Pack("brand"), filepath.FromSlash(newRel))
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(newRel)), []byte(frameVariant("brand v3")), 0o644); err != nil {
		t.Fatal(err)
	}
	edit(t, dir, "frame: frames/page.html\n", "frame: "+newRel+"\n")
	plan = build(t, st, load(t, dir), o)
	if f := find(t, plan, "file", newRel); f.Action != pack.Create {
		t.Fatalf("the new frame path = %+v, want a create", f)
	}
	if len(plan.FrameShared) != 0 {
		t.Fatalf("the new path is not used by site or the yard, but shared scopes = %v", plan.FrameShared)
	}
	if dropped := find(t, plan, "file", "frames/page.html"); !strings.Contains(dropped.Held, "depot site") || !strings.Contains(dropped.Held, "the yard") {
		t.Fatalf("the old frame path = %+v, want both scopes still using it", dropped)
	}
	apply(t, st, plan, nil)

	// Changing the new path still reaches only its active scopes, even
	// though all three records name the same pack.
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(newRel)), []byte(frameVariant("brand v4")), 0o644); err != nil {
		t.Fatal(err)
	}
	plan = build(t, st, load(t, dir), o)
	if f := find(t, plan, "file", newRel); f.Action != pack.Change {
		t.Fatalf("the changed new frame = %+v, want a change", f)
	}
	if len(plan.FrameShared) != 0 {
		t.Fatalf("site and the yard still name the old path, but shared scopes = %v", plan.FrameShared)
	}
	apply(t, st, plan, nil)
	apply(t, st, build(t, st, load(t, dir), site), nil)
	apply(t, st, build(t, st, load(t, dir), pack.Options{Home: o.Home}), nil)

	// If the shared installed file has gone missing, restoring it is also
	// a change for every other scope that names that path.
	if err := os.Remove(newInstalled); err != nil {
		t.Fatal(err)
	}
	plan = build(t, st, load(t, dir), o)
	if f := find(t, plan, "file", newRel); f.Action != pack.Create {
		t.Fatalf("the missing shared frame = %+v, want a create", f)
	}
	if !slices.Equal(plan.FrameShared, []string{"depot site", "the yard"}) {
		t.Fatalf("restoring the shared frame names %v, want depot site and the yard", plan.FrameShared)
	}
	apply(t, st, plan, nil)
	for _, depot := range []string{"web", "site", ""} {
		if f := resolve(t, st, o.Home, depot); f.SHA256 != sumOf(frameVariant("brand v4")) {
			t.Fatalf("%q reads frame %s, want the restored copy's sum", depot, f.SHA256)
		}
	}
}

// A frame-only pack applies with no depot and records a yard record; a
// frame beside anything of a depot's own is no frame-only pack and takes
// a depot, and a pack of kind source carries no frame at all.
func TestAFrameOnlyPackAppliesToTheYard(t *testing.T) {
	st, o := yard(t)
	dir := framedDir(t, "brand-yard")
	p := load(t, dir)
	if !p.FrameOnly() || !p.YardWide() {
		t.Fatalf("a frame alone: FrameOnly %v, YardWide %v, want both", p.FrameOnly(), p.YardWide())
	}
	yardOpts := pack.Options{Home: o.Home}
	plan := build(t, st, p, yardOpts)
	if want := "frame: the yard would use brand-yard's frame (was pageframe's)"; plan.FrameUse != want {
		t.Fatalf("the yard plan's frame line = %q, want %q", plan.FrameUse, want)
	}
	if f := find(t, plan, "file", "frames/page.html"); f.Action != pack.Create || f.File.Mode != "0644" || f.File.Fill {
		t.Fatalf("the yard's frame object = %+v %+v, want a plain 0644 copy", f, f.File)
	}
	apply(t, st, plan, nil)
	installed := filepath.Join(config.At(o.Home).Pack("brand-yard"), "frames", "page.html")
	if text, err := os.ReadFile(installed); err != nil || string(text) != frameVariant("brand-yard") {
		t.Fatalf("the yard's installed frame = %q, %v", text, err)
	}
	if rec := recordOf(t, st, "brand-yard", ""); rec.Frame != installed || !rec.FrameActive {
		t.Fatalf("the yard's record = frame %q, active %v", rec.Frame, rec.FrameActive)
	}
	if f := resolve(t, st, o.Home, ""); f.Pack != "brand-yard" || f.Path != installed {
		t.Fatalf("the yard's frame = %+v", f)
	}

	// A frame beside a group is applied per depot, not to the yard.
	busy := writePack(t, map[string]string{
		"pack.yaml":        "name: busy\napi: 4\nframe: frames/page.html\ngroups:\n  builders: { agent: roles/builders.md }\n",
		"frames/page.html": pageFrame, "roles/builders.md": "# Builders\n",
	})
	if p := load(t, busy); p.FrameOnly() {
		t.Fatal("a frame beside a group is a frame-only pack")
	}
	if _, err := pack.Build(ctx, st, load(t, busy), yardOpts); err == nil || !strings.Contains(err.Error(), "plan it with --depot <depot>") {
		t.Fatalf("building a frame beside a group with no depot: %v", err)
	}

	// A pack of kind source carries sources alone, frame included.
	const decl = "name: acme\nbase: https://acme.test\ncredential: { env: ACME_TOKEN }\ncalls:\n  ping:\n    kind: read\n    run: [acme/run, ping]\n"
	source := writePack(t, map[string]string{
		"pack.yaml": "name: acme-flow\napi: 4\nkind: source\nsources:\n  acme:\n    declaration: acme.yaml\nframe: frames/page.html\n",
		"acme.yaml": decl, "acme/run": "#!/bin/sh\n", "frames/page.html": pageFrame,
	})
	if _, err := pack.LoadDir(source); err == nil || !strings.Contains(err.Error(), "a pack of kind source carries sources alone, and no frame") {
		t.Fatalf("a source pack with a frame: %v", err)
	}
}
