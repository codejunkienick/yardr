package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"yardr.dev/yardr/internal/rpc"
)

func testReplyFormats(t *testing.T) {
	for _, message := range []string{
		"no such method ReplyingWithFormatElse",
		"an unrelated refusal: no such method ReplyingWithFormat",
	} {
		if isNoSuchMethod(errors.New(message), "ReplyingWithFormat") {
			t.Errorf("unrelated error %q was recognized as a missing method", message)
		}
	}

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

	// The real RPC handler answers all calls, except while older is set the
	// method an older server lacks, in the words such a server sends. They are
	// written out here, not taken from rpc: servers already out there keep
	// saying them if rpc's are reworded.
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
	var older atomic.Bool
	older.Store(true)
	real := rpc.Handler(own, nil)
	srv := &http.Server{ConnContext: rpc.ConnContext, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if older.Load() && r.URL.Path == "/call/ReplyingWithFormat" {
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
	// A server old enough to lack the method is another build than this yardr,
	// and the client then puts its note of that in front of the server's words
	// (the shape a released yardr gets from an older server, where the bare
	// words above are a build with no revision, as a test binary is); the hint
	// goes with both.
	client.Revision = "ba9876543210"
	refuse("markdown", "run `yardr server stop` (no such method ReplyingWithFormat)")
	refuse("markdown", "server must support ReplyingWithFormat")
	client.Revision = ""
	if now, err := st.Head(ctx); err != nil || now != head || stdinRuns(t, dir, "reply") != 2 {
		t.Fatalf("older server changed reservations or sent a reply: head %d -> %d, %v", head, now, err)
	}

	// What the store itself refuses over the same socket is no sign of an older
	// server: it reaches the caller as the store said it, with no hint before it.
	older.Store(false)
	seq, err := strconv.ParseUint(seqs["acme"], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReplyingWithFormat(ctx, "yardmaster", seq, "", "answer", "markdown", a.who()); err != nil {
		t.Fatal(err)
	}
	_, direct := st.ReplyingWithFormat(ctx, "yardmaster", seq, "", "answer", "plain", a.who())
	if direct == nil || !strings.Contains(direct.Error(), "retry the original reply to reconcile it") {
		t.Fatalf("the store's refusal of another mode for an unresolved attempt: %v", direct)
	}
	if _, err := run(t, a.replyCmd(), seqs["acme"], "answer", "--format", "plain"); err == nil || err.Error() != direct.Error() {
		t.Fatalf("store refusal over the socket: %v, want it as the store said it: %v", err, direct)
	}
}
