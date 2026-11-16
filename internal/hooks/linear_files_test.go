package hooks_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/hooks"
	"yardr.dev/yardr/internal/source"
	"yardr.dev/yardr/internal/standin"
)

// A reply with files in a Linear thread: the pack's thread.reply uploads
// each file to Linear's storage and posts one comment that holds them. It
// is held here against the stand-in's fileUpload and its signed address
// (internal/standin), which refuse what a storage host refuses.

var (
	linearPDF  = []byte("%PDF-1.4\n1 0 obj\n<< /Type /Catalog >>\nendobj\ntrailer\n<< /Root 1 0 R >>\n%%EOF\n")
	linearJPEG = []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0xff, 0xd9}
)

// linearPNG is a 1x1 picture, which the file program reads as image/png.
func linearPNG(t *testing.T) []byte {
	t.Helper()
	raw, err := hex.DecodeString(telegramPNG)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// linearFile writes data as the file called name in a directory of its own:
// where a reply puts each file for the command, under the name the file had.
func linearFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// linearSecrets are what a reply with files keeps out of everything it
// prints and every argument it gives a process: the credential, and of each
// upload so far the signed address, its signature and its headers' values.
func linearSecrets(g *linearGolden, cred string) []string {
	out := []string{cred}
	for _, u := range g.s.LinearUploads() {
		out = append(out, u.URL, u.URL[strings.Index(u.URL, "sig=")+len("sig="):])
		for _, h := range u.Headers {
			out = append(out, h.Value)
		}
	}
	return out
}

func noSecrets(t *testing.T, what, text string, secrets []string) {
	t.Helper()
	for _, s := range secrets {
		if strings.Contains(text, s) {
			t.Errorf("%s holds %q", what, s)
		}
	}
}

// linearReplyIn is the object thread.reply is handed for a reply to the
// golden issue.
func linearReplyIn(key, body string, files ...string) map[string]any {
	in := map[string]any{"thread": "uuid-1", "parent": "", "body": body, "key": key}
	if files != nil {
		in["files"] = files
	}
	return in
}

// runLinearReply runs thread.reply as the yard does, by hand, so that all
// it prints on stdout and stderr is seen: the yard keeps stdout as the
// answer, and stderr only for a command that failed.
func runLinearReply(t *testing.T, g *linearGolden, cred string, in map[string]any) (stdout, stderr string, err error) {
	t.Helper()
	argv := slices.Clone(g.dec.D.Thread.Reply)
	argv[0] = source.Command{Key: "thread.reply", Argv: g.dec.D.Thread.Reply}.Program(g.dec.Home, g.dec.D.Name)
	raw, merr := json.Marshal(in)
	if merr != nil {
		t.Fatal(merr)
	}
	var out, errs bytes.Buffer
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = g.dec.D.CommandEnv(cred), bytes.NewReader(raw), &out, &errs
	err = cmd.Run()
	return out.String(), errs.String(), err
}

// newLinearComments are the comments of the golden issue after its own two.
func newLinearComments(g *linearGolden) []standin.LinearComment {
	return g.s.LinearComments("YRD-1")[2:]
}

// linearUploadSummary is what the stand-in kept of each file asked for,
// without the secrets of it: those are different on every run.
func linearUploadSummary(g *linearGolden) []map[string]any {
	out := []map[string]any{}
	for _, u := range g.s.LinearUploads() {
		out = append(out, map[string]any{"id": u.ID, "filename": u.Filename, "contentType": u.ContentType,
			"size": u.Size, "sent": u.Sent, "sha256": u.SHA256, "bearer": u.Bearer})
	}
	return out
}

// runFilesStory is the pack replying with files: a picture, a document and a
// photograph in one comment; names made of markdown's own characters, and
// types that are not what the names say; an upload the host refuses, a
// retry of it, a file over the limit and a redirect. Each failure is shown
// with the words it ends in, and none leaves a comment.
func runFilesStory(t *testing.T) []goldenStep {
	t.Helper()
	g := newLinearGolden(t)
	ctx := context.Background()
	const cred = "lin_api_test"
	issue := goldenIssue()
	png := linearPNG(t)
	var steps []goldenStep
	emit := func(name string, result any) {
		steps = append(steps, goldenStep{Name: name, Calls: g.calls(), Result: result})
	}
	reply := func(key, body string, files ...string) (hooks.Message, error) {
		return g.dec.ReplyFiles(ctx, issue, "", body, key, files, cred)
	}
	// refused is a reply that must fail with a comment of none, and the
	// words it failed with.
	refused := func(name, key, body string, files ...string) {
		t.Helper()
		before := len(newLinearComments(g))
		_, err := reply(key, body, files...)
		if err == nil {
			t.Fatalf("%s: the reply was posted", name)
		}
		noSecrets(t, name+"'s error", err.Error(), linearSecrets(g, cred))
		if n := len(newLinearComments(g)); n != before {
			t.Fatalf("%s: %d comments before, %d after", name, before, n)
		}
		emit(name, map[string]any{"error": err.Error(), "comments": before, "uploads": linearUploadSummary(g)})
	}

	shot := linearFile(t, "shot.png", png)
	notes := linearFile(t, "notes.pdf", linearPDF)
	photo := linearFile(t, "photo.jpg", linearJPEG)
	m, err := reply("9d4c3b2a-1f0e-4d5c-8b7a-6f5e4d3c2b01", "the shed", shot, notes, photo)
	if err != nil {
		t.Fatal(err)
	}
	emit("reply-files", map[string]any{"message": m, "comments": newLinearComments(g), "uploads": linearUploadSummary(g)})

	// A name is only a name: the type is what the bytes say, so a picture
	// called .txt is a picture and a text called .png is a link; and a name
	// that holds what markdown makes links of is escaped into the text, and
	// written as % into the address, so it breaks neither.
	if _, err := reply("9d4c3b2a-1f0e-4d5c-8b7a-6f5e4d3c2b02", "names",
		linearFile(t, "shot (2).png", png),
		linearFile(t, "really-a-picture.txt", png),
		linearFile(t, "not-a-picture.png", []byte("plain words\n")),
		linearFile(t, "odd ](x) [y]_*`.md", []byte("more plain words\n"))); err != nil {
		t.Fatal(err)
	}
	emit("reply-names", map[string]any{"comments": newLinearComments(g)[1:], "uploads": linearUploadSummary(g)[3:]})

	// The host refuses the second of two files: the first is up, nothing is
	// posted, and the same reply run again (the same key: the core asks
	// posted first, which finds nothing) uploads both again and posts once.
	const key = "9d4c3b2a-1f0e-4d5c-8b7a-6f5e4d3c2b03"
	a, b := linearFile(t, "a.png", png), linearFile(t, "b.pdf", linearPDF)
	g.s.RefuseLinearUpload("b.pdf", 403)
	refused("refused", key, "two files", a, b)
	if f, err := g.dec.Posted(ctx, issue, key, cred); err != nil || f != nil {
		t.Fatalf("posted after a refused upload: %+v, %v", f, err)
	}
	emit("posted-none", "none")
	g.s.RefuseLinearUpload("b.pdf", 0)
	if _, err := reply(key, "two files", a, b); err != nil {
		t.Fatal(err)
	}
	if f, err := g.dec.Posted(ctx, issue, key, cred); err != nil || f == nil || f.ID != key {
		t.Fatalf("posted after the retry: %+v, %v", f, err)
	}
	emit("retry", map[string]any{"comments": newLinearComments(g)[2:], "uploads": linearUploadSummary(g)[7:]})

	// Over the limit by a byte: nothing is uploaded, not even the file that
	// came before it.
	big := linearFile(t, "big.bin", make([]byte, 8<<20+1))
	refused("over-limit", "9d4c3b2a-1f0e-4d5c-8b7a-6f5e4d3c2b04", "too big", a, big)

	// A redirect is no answer, and none is followed.
	g.s.RefuseLinearUpload("c.png", 307)
	refused("redirect", "9d4c3b2a-1f0e-4d5c-8b7a-6f5e4d3c2b05", "moved", linearFile(t, "c.png", png))
	for _, c := range steps[len(steps)-1].Calls {
		if strings.Contains(c, "followed") {
			t.Fatalf("a redirect was followed: %v", steps[len(steps)-1].Calls)
		}
	}
	return steps
}

func TestLinearPackReplyFiles(t *testing.T) {
	if _, err := exec.LookPath("file"); err != nil {
		t.Skip("file is not installed, and the pack's thread command reads a reply file's type with it")
	}
	checkGolden(t, "files.json", runFilesStory(t))
}

// What a reply with files prints and what it refuses with is held to the
// secrets of the exchange: the credential, the signed address and the
// headers it names. A reply that works prints one object and nothing on
// stderr; one that fails says what failed and the HTTP status, and nothing
// of what Linear answered, which holds the address and the headers: the
// stand-in's page for a refused upload gives both back, as a host's does.
func TestLinearPackReplyFilesKeepsSecretsOut(t *testing.T) {
	if _, err := exec.LookPath("file"); err != nil {
		t.Skip("file is not installed, and the pack's thread command reads a reply file's type with it")
	}
	const cred = "lin_api_MARKER-q7ld-token"
	png := linearPNG(t)
	shot, notes := linearFile(t, "shot.png", png), linearFile(t, "notes.pdf", linearPDF)
	big := linearFile(t, "big.bin", make([]byte, 8<<20+1))
	nowhere := filepath.Join(t.TempDir(), "gone.png")

	for _, c := range []struct {
		name   string
		setup  func(g *linearGolden)
		files  []string
		stderr string // what stderr says, "" for nothing at all
		puts   int    // the files the stand-in took
		curlrc bool   // whether curl's user config tries to follow redirects
	}{
		{name: "it works", files: []string{shot, notes}, puts: 2},
		{name: "a refused upload", files: []string{shot, notes}, puts: 1,
			setup:  func(g *linearGolden) { g.s.RefuseLinearUpload("notes.pdf", 403) },
			stderr: "the upload of file 2 of 2 was refused (HTTP 403)\n"},
		{name: "a redirect", files: []string{shot}, setup: func(g *linearGolden) { g.s.RefuseLinearUpload("shot.png", 307) },
			stderr: "the upload of file 1 of 1 was refused (HTTP 307)\n"},
		{name: "a curl config cannot follow a redirect", files: []string{shot}, curlrc: true,
			setup:  func(g *linearGolden) { g.s.RefuseLinearUpload("shot.png", 307) },
			stderr: "the upload of file 1 of 1 was refused (HTTP 307)\n"},
		{name: "a refused fileUpload", files: []string{shot},
			setup:  func(g *linearGolden) { g.s.LinearKey = "another" },
			stderr: "fileUpload for file 1 of 1 gave no place to send the file (HTTP 200)\n"},
		{name: "a header it cannot send", files: []string{shot},
			setup: func(g *linearGolden) {
				g.s.SetLinearUploadHeaders([]standin.LinearHeader{{Key: "x-note", Value: "one\ntwo"}})
			},
			stderr: "fileUpload for file 1 of 1 gave no place to send the file (HTTP 200)\n"},
		{name: "a file over the limit", files: []string{shot, big}, stderr: fmt.Sprintf("file 2 of 2 is %d bytes, over the limit of 8388608\n", 8<<20+1)},
		{name: "no file", files: []string{shot, nowhere}, stderr: "thread.reply found no file 2 of 2\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := newLinearGolden(t)
			g.s.LinearKey = cred
			if c.setup != nil {
				c.setup(g)
			}
			if c.curlrc {
				home := t.TempDir()
				if err := os.WriteFile(filepath.Join(home, ".curlrc"), []byte("location\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("HOME", home)
			}
			stdout, stderr, err := runLinearReply(t, g, cred, linearReplyIn("9d4c3b2a-1f0e-4d5c-8b7a-6f5e4d3c2b99", "the words", c.files...))
			secrets := linearSecrets(g, cred)
			noSecrets(t, "stdout", stdout, secrets)
			noSecrets(t, "stderr", stderr, secrets)
			if c.stderr == "" {
				var answer map[string]any
				if err != nil || stderr != "" || json.Unmarshal([]byte(stdout), &answer) != nil || len(answer) != 2 || len(newLinearComments(g)) != 1 {
					t.Fatalf("it should have worked: %v\nstdout %q\nstderr %q", err, stdout, stderr)
				}
			} else if err == nil || stdout != "" || stderr != c.stderr || len(newLinearComments(g)) != 0 {
				t.Fatalf("it should have failed with %q and no comment: %v\nstdout %q\nstderr %q", c.stderr, err, stdout, stderr)
			}
			puts := 0
			for _, u := range g.s.LinearUploads() {
				if u.Sent {
					puts++
				}
				if u.Bearer {
					t.Errorf("the token went to the signed address of %s", u.Filename)
				}
			}
			if puts != c.puts {
				t.Errorf("%d files were taken, want %d", puts, c.puts)
			}
		})
	}
}
