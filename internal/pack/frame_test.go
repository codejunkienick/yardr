package pack_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/config"
	"yardr.dev/yardr/internal/pack"
	"yardr.dev/yardr/internal/store"
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
