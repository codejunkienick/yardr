package cli

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/rpc"
)

func testReplyFormats(t *testing.T) {
	a, st, seqs, dir, own := replyYardOwned(t)
	ctx := context.Background()
	t.Setenv("ACME_TOKEN", "tok-yardmaster")
	a.env["YARDR_CREW"] = "yardmaster"
	refuse := func(mode, want string) {
		t.Helper()
		if _, err := run(t, a.replyCmd(), seqs["acme"], "answer", "--format", mode); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("format %q: %v, want %s", mode, err, want)
		}
	}
	refuse("markdown", "thread.formats")
	decl := strings.Replace(acmeCLI, "thread:\n", "thread:\n  formats: [markdown, plain]\n", 1)
	if _, err := st.PutSource(ctx, "acme.yaml", decl, "me"); err != nil {
		t.Fatal(err)
	}
	refuse("html", "thread.formats")
	refuse("", "--format requires a mode")
	if stdinRuns(t, dir, "reply") != 0 {
		t.Fatal("refused format sent a reply")
	}
	const body = "    code  \n\n"
	cmd := a.replyCmd()
	cmd.SetIn(strings.NewReader(body))
	if _, err := run(t, cmd, seqs["acme"], "-", "--format", "markdown"); err != nil {
		t.Fatal(err)
	}
	in := lastStdin(t, dir, "reply")
	want := map[string]any{"thread": "issue-1", "parent": "c1", "body": body, "format": "markdown", "key": in["key"]}
	if !reflect.DeepEqual(in, want) || !uuid4.MatchString(in["key"].(string)) {
		t.Fatalf("formatted command input: %v", in)
	}
	if _, err := run(t, a.replyCmd(), seqs["acme"], "  legacy  \n"); err != nil {
		t.Fatal(err)
	}
	in = lastStdin(t, dir, "reply")
	want = map[string]any{"thread": "issue-1", "parent": "c1", "body": "legacy", "key": in["key"]}
	if !reflect.DeepEqual(in, want) {
		t.Fatalf("legacy command input: %v", in)
	}

	// The real RPC handler answers all calls except the method an older server lacks.
	short, err := os.MkdirTemp("", "yrf")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(short) })
	sock := filepath.Join(short, "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	real := rpc.Handler(own, nil)
	srv := &http.Server{ConnContext: rpc.ConnContext, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/call/ReplyingWithFormat" {
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "error", "message": "no such method ReplyingWithFormat"}})
			return
		}
		real.ServeHTTP(w, r)
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	client := rpc.NewClient(sock)
	t.Cleanup(func() { client.Close(ctx) })
	a.st = client
	head, err := st.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	refuse("markdown", "server must support ReplyingWithFormat")
	if now, err := st.Head(ctx); err != nil || now != head || stdinRuns(t, dir, "reply") != 2 {
		t.Fatalf("older server changed reservations or sent a reply: head %d -> %d, %v", head, now, err)
	}
}
