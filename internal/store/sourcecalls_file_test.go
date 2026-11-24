package store_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
)

const docsToken = "tok-docs-not-to-be-seen"
const planBytes = "the plan bytes are no JSON"

// docsCalls is a document service with one file call and one JSON read.
const docsCalls = `name: docs
about: The document system.
base: BASE/api
credential:
  env: DOCS_TOKEN
  header: "Authorization: Bearer {credential}"
calls:
  content:
    kind: read
    about: Download one item's file into the working directory
    params:
      id: { type: string, pattern: "^[0-9]{1,12}$", log: true }
    request: { method: GET, path: "/items/{id}/content" }
    answer: { file: true }
  meta:
    kind: read
    params:
      id: { type: string, pattern: "^[0-9]{1,12}$" }
    request: { method: GET, path: "/items/{id}" }
    answer: { select: [id] }
`

// docs is the document stand-in: it counts what reaches it, takes
// docsToken alone, and answers the file call of item 7 with a named file.
func docs(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+docsToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/api/items/7/content":
			w.Header().Set("Content-Disposition", `attachment; filename="Plan.docx"`)
			io.WriteString(w, planBytes)
		case strings.HasPrefix(r.URL.Path, "/api/items/"):
			fmt.Fprintf(w, `{"id":%q}`, strings.TrimPrefix(r.URL.Path, "/api/items/"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// docsYard is a yard of the operator jo with the source docs against a
// stand-in, and the groups builders and reviewers.
func docsYard(t *testing.T, s store.API) (context.Context, *atomic.Int32) {
	t.Helper()
	ctx := context.Background()
	if err := s.SetSetting(ctx, store.OperatorSetting, "jo", "jo"); err != nil {
		t.Fatal(err)
	}
	srv, hits := docs(t)
	if _, err := s.PutSource(ctx, "docs.yaml", strings.Replace(docsCalls, "BASE", srv.URL, 1), "jo"); err != nil {
		t.Fatal(err)
	}
	env := filepath.Join(t.TempDir(), "docs.env")
	if err := os.WriteFile(env, []byte("DOCS_TOKEN="+docsToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetSource(ctx, "docs", store.SourceEnvFile, env, "jo"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutDepot(ctx, store.Depot{Name: "yard", Base: "origin/main"}, "jo"); err != nil {
		t.Fatal(err)
	}
	putFlow(t, s, "yard", "", outcomeFlow)
	for _, g := range []string{"builders", "reviewers"} {
		if err := s.PutGroup(ctx, store.Group{Name: g, Runner: "exec", Config: map[string]any{"cmd": "true"}, Limit: 5}, "jo"); err != nil {
			t.Fatal(err)
		}
	}
	return ctx, hits
}

// A file call granted by its name alone answers with the blob's hash, its
// size and the suggested name, and no bytes; the bytes stand as a blob,
// the event carries the file's bytes, and the call is counted as a read.
func TestSourceFileCallGrantedByName(t *testing.T) {
	t.Parallel()
	_, s := storetest.NewOwned(t)
	ctx, hits := docsYard(t, s)
	if _, err := s.GrantSource(ctx, "docs", "content", store.Grantee{Group: "builders"}, "", "jo"); err != nil {
		t.Fatal(err)
	}
	_, sess := session(t, s, "builders")
	res, err := s.CallSource(ctx, store.SourceCall{Source: "docs", Call: "content", Session: sess.ID, Actor: "x",
		Params: map[string]string{"id": "7"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.File == nil || len(res.File.Hash) != 64 || res.File.Name != "docs-content-Plan.docx" || res.File.Bytes != int64(len(planBytes)) ||
		len(res.Answer) != 0 || res.Status != 200 {
		t.Fatalf("answer = %+v", res)
	}
	data, err := os.ReadFile(s.BlobPath(res.File.Hash))
	if err != nil || string(data) != planBytes {
		t.Fatalf("the blob = %q, %v", data, err)
	}
	if _, data := lastCall(t, s); !strings.Contains(data, `"status":200`) || !strings.Contains(data, `"bytes":`+fmt.Sprint(len(planBytes))) {
		t.Fatalf("event = %s", data)
	}
	if hits.Load() != 1 {
		t.Fatalf("the service was reached %d times", hits.Load())
	}
	// Counted once, as a read: with one call an hour the next is refused.
	if _, err := s.SetSource(ctx, "docs", store.SourceCallsPerHour, "1", "jo"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CallSource(ctx, store.SourceCall{Source: "docs", Call: "content", Session: sess.ID, Actor: "x",
		Params: map[string]string{"id": "7"}}); err == nil || !strings.Contains(err.Error(), "has made the 1 calls") {
		t.Fatalf("the call after the file call = %v", err)
	}
	if _, data := lastCall(t, s); !strings.Contains(data, `"status":"limit"`) {
		t.Fatalf("event = %s", data)
	}
}

// A grant of read refuses a file call, which its own name then grants; a
// crew caller and a caller as a user are each granted by name as well.
func TestSourceFileCallGrants(t *testing.T) {
	t.Parallel()
	_, s := storetest.NewOwned(t)
	ctx, _ := docsYard(t, s)
	if _, err := s.GrantSource(ctx, "docs", store.GrantRead, store.Grantee{Group: "reviewers"}, "", "jo"); err != nil {
		t.Fatal(err)
	}
	_, reviewer := session(t, s, "reviewers")
	call := store.SourceCall{Source: "docs", Call: "content", Session: reviewer.ID, Actor: "x", Params: map[string]string{"id": "7"}}
	if _, err := s.CallSource(ctx, call); err == nil ||
		!strings.Contains(err.Error(), "group reviewers is not granted it; the operator grants it with: yardr source grant docs content --group reviewers") {
		t.Fatalf("a file call under read = %v", err)
	}
	if _, data := lastCall(t, s); !strings.Contains(data, `"status":"not_granted"`) {
		t.Fatalf("event = %s", data)
	}
	may, err := s.SessionCalls(ctx, reviewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range may.Calls {
		if c.Name == "content" {
			t.Fatalf("read lists the file call: %+v", may.Calls)
		}
	}
	if _, err := s.GrantSource(ctx, "docs", "content", store.Grantee{Group: "reviewers"}, "", "jo"); err != nil {
		t.Fatal(err)
	}
	res, err := s.CallSource(ctx, call)
	if err != nil || res.File == nil || res.File.Name != "docs-content-Plan.docx" {
		t.Fatalf("a file call granted by name = %+v, %v", res, err)
	}
	may, err = s.SessionCalls(ctx, reviewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range may.Calls {
		if c.Name == "content" && c.File {
			found = true
		}
	}
	if !found {
		t.Fatalf("the granted file call is not listed as one: %+v", may.Calls)
	}

	// A crew member, granted by name.
	if _, err := s.PutCrew(ctx, store.CrewMember{Name: "clerk", Runner: "herdr"}, "jo"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GrantSource(ctx, "docs", "content", store.Grantee{Crew: "clerk"}, "", "jo"); err != nil {
		t.Fatal(err)
	}
	res, err = s.CallSource(ctx, store.SourceCall{Source: "docs", Call: "content", Actor: "clerk", Params: map[string]string{"id": "7"}})
	if err != nil || res.File == nil {
		t.Fatalf("a crew member's file call = %+v, %v", res, err)
	}

	// A call as a user, granted as them by name: ada gives the grant, and
	// the call goes out with her credential.
	if _, err := s.AddUser(ctx, "ada", "jo"); err != nil {
		t.Fatal(err)
	}
	adaEnv := filepath.Join(t.TempDir(), "ada.env")
	if err := os.WriteFile(adaEnv, []byte("DOCS_TOKEN="+docsToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetUser(ctx, "ada", map[string]string{store.UserEnvFile: adaEnv}, "ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GrantSource(ctx, "docs", "content", store.Grantee{Group: "builders"}, "ada", "ada"); err != nil {
		t.Fatal(err)
	}
	_, builder := session(t, s, "builders")
	res, err = s.CallSource(ctx, store.SourceCall{Source: "docs", Call: "content", Session: builder.ID, Actor: "x", As: "ada",
		Params: map[string]string{"id": "7"}})
	if err != nil || res.File == nil {
		t.Fatalf("a file call as ada = %+v, %v", res, err)
	}
	if _, data := lastCall(t, s); !strings.Contains(data, `"as":"ada"`) || !strings.Contains(data, `"status":200`) {
		t.Fatalf("event = %s", data)
	}
}

// A session whose bead came from another yard makes no file call, though
// its group is granted the call by name.
func TestSourceFileCallFromPeerRefused(t *testing.T) {
	t.Parallel()
	y := newBeadYard(t)
	srv, _ := docs(t)
	if _, err := y.api.PutSource(y.ctx, "docs.yaml", strings.Replace(docsCalls, "BASE", srv.URL, 1), "jo"); err != nil {
		t.Fatal(err)
	}
	env := filepath.Join(t.TempDir(), "docs.env")
	if err := os.WriteFile(env, []byte("DOCS_TOKEN="+docsToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := y.api.SetSource(y.ctx, "docs", store.SourceEnvFile, env, "jo"); err != nil {
		t.Fatal(err)
	}
	if err := y.api.PutGroup(y.ctx, store.Group{Name: "builders", Runner: "exec", Config: map[string]any{"cmd": "true"}}, "jo"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.api.GrantSource(y.ctx, "docs", "content", store.Grantee{Group: "builders"}, "", "jo"); err != nil {
		t.Fatal(err)
	}
	y.take(t, beadMsg("b1", "web-k3f9", nil))
	b := y.arrived(t)
	sess, err := y.api.Claim(y.ctx, b.ID, "builders", b.Stage, "yard")
	if err != nil {
		t.Fatal(err)
	}
	_, err = y.api.CallSource(y.ctx, store.SourceCall{Source: "docs", Call: "content", Session: sess.ID, Actor: "x",
		Params: map[string]string{"id": "7"}})
	if err == nil || !strings.Contains(err.Error(), "came from another yard") {
		t.Fatalf("a file call from a peer's bead = %v", err)
	}
	if _, data := lastCall(t, y.api); !strings.Contains(data, `"status":"from_peer"`) {
		t.Fatalf("event = %s", data)
	}
}

// A file call's answer never suggests a bare name from outside: every
// usable suggestion is written <source>-<call>-<suggested>, so a name a
// harness started in the directory reads as its instructions (CLAUDE.md
// and the rest, in any case) lands as a file of no harness's, and one that
// cannot be used is the yard's own <source>-<call> with the bytes'
// extension.
func TestSuggestedFileName(t *testing.T) {
	t.Parallel()
	const prefix = "docs-content-"
	const fallback = "docs-content.pdf"
	pdf := []byte("%PDF-1.4 the converted bytes")
	room := store.MaxBlobName - len(prefix)
	type row struct{ suggested, want string }
	rows := []row{
		// An ordinary name is prefixed as well: no name goes through bare.
		{"Plan.docx", prefix + "Plan.docx"},
		{"Q3 plan (final).docx", prefix + "Q3 plan (final).docx"},
		{"LICENSE", prefix + "LICENSE"},
		{"a. b.docx", prefix + "a. b.docx"},
		// What a service's name may carry before its last segment is dropped.
		{"../../CLAUDE.md", prefix + "CLAUDE.md"},
		{"/etc/AGENTS.md", prefix + "AGENTS.md"},
		{`sub\dir\GEMINI.md`, prefix + "GEMINI.md"},
		// The dots and spaces that trail a name are dropped before the
		// prefix, and only those: a Windows path drops them as well.
		{"CLAUDE.md.", prefix + "CLAUDE.md"},
		{"CLAUDE.md ", prefix + "CLAUDE.md"},
		{"CLAUDE.md. . ", prefix + "CLAUDE.md"},
		{"Plan.docx...", prefix + "Plan.docx"},
		// Nothing left of the last segment once its trailing dots and
		// spaces are gone is no name.
		{"", fallback},
		{" ", fallback},
		{"dir/", fallback},
		{"dir/ . ", fallback},
		// A hidden name falls back, as it did before the prefix.
		{".envrc", fallback},
		{".mcp.json", fallback},
		{".git", fallback},
		{"..", fallback},
		{"../.envrc", fallback},
		{".envrc.", fallback},
		// So does one a file may not be named: a control character, a
		// direction override, or bytes that are no UTF-8.
		{"a\x00b.txt", fallback},
		{"a\nb.txt", fallback},
		{"a\u202eb.txt", fallback},
		{"a\u2028b.txt", fallback},
		{"a\xffb.txt", fallback},
		// A name over the limit is cut, not replaced: the whole name is at
		// most 255 bytes, and these reach it.
		{strings.Repeat("a", 300), prefix + strings.Repeat("a", room)},
		{strings.Repeat("a", room), prefix + strings.Repeat("a", room)},
		{strings.Repeat("a", room+1), prefix + strings.Repeat("a", room)},
		{strings.Repeat("a", room-1), prefix + strings.Repeat("a", room-1)},
		// The cut falls between characters: the 242 bytes the prefix
		// leaves hold 121 runes of 2 bytes, or 80 of 3 and 60 of 4 with 2
		// bytes over, so the rune the cut would split is dropped whole.
		{strings.Repeat("é", 150), prefix + strings.Repeat("é", 121)},
		{strings.Repeat("€", 100), prefix + strings.Repeat("€", 80)},
		{strings.Repeat("😀", 75), prefix + strings.Repeat("😀", 60)},
		// A cut that finds no rune to keep leaves no name.
		{strings.Repeat("\x80", 300), fallback},
	}
	// The names an agent's harness reads at its start, and the same in other
	// case: none is compared with, all are prefixed like the rest.
	for _, n := range []string{"CLAUDE.md", "AGENTS.md", "GEMINI.md", "AGENT.md", "CLAUDE.local.md", "QWEN.md", "WARP.md",
		"AGENTS.override.md", "claude.md", "Claude.MD", "agents.Md", "gemini.MD"} {
		rows = append(rows, row{n, prefix + n})
	}
	for _, r := range rows {
		got := store.SuggestedFileName("docs", "content", r.suggested, pdf)
		if got != r.want {
			t.Errorf("suggestedFileName(%q) = %q, want %q", r.suggested, got, r.want)
		}
		// Whatever came in, the name is one the command may write.
		if err := store.CheckBlobName(got); err != nil || got == "" || !utf8.ValidString(got) {
			t.Errorf("suggestedFileName(%q) = %q, which a file may not be named: %v", r.suggested, got, err)
		}
	}

	// The longest source and call names a declaration allows leave the
	// suggestion 125 bytes of the 255.
	src, call := strings.Repeat("s", 64), strings.Repeat("c", 64)
	want := src + "-" + call + "-" + strings.Repeat("a", 125)
	if got := store.SuggestedFileName(src, call, strings.Repeat("a", 300), pdf); got != want || len(got) != store.MaxBlobName {
		t.Errorf("suggestedFileName with the longest names = %q (%d bytes), want %q", got, len(got), want)
	}
}

// A file call whose yard's blob bound is no positive number is refused
// before anything is downloaded, naming the bound: nothing reaches the
// service, no staging file and no blob is left.
func TestSourceFileCallNoBound(t *testing.T) {
	t.Parallel()
	for _, bound := range []string{"0", "-5"} {
		t.Run(bound, func(t *testing.T) {
			t.Parallel()
			_, s := storetest.NewOwned(t)
			ctx, hits := docsYard(t, s)
			if err := s.PutSettingRaw(store.BlobMaxSetting, bound); err != nil {
				t.Fatal(err)
			}
			_, err := s.CallSource(ctx, store.SourceCall{Source: "docs", Call: "content", Actor: "jo",
				Params: map[string]string{"id": "7"}})
			if err == nil || !strings.Contains(err.Error(), store.BlobMaxSetting) {
				t.Fatalf("a file call with bound %s = %v", bound, err)
			}
			if hits.Load() != 0 {
				t.Fatalf("the service was reached %d times", hits.Load())
			}
			home := s.HomeDir()
			for _, dir := range []string{filepath.Join(home, "tmp"), filepath.Join(home, store.BlobsDir)} {
				entries, err := os.ReadDir(dir)
				if err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				if len(entries) != 0 {
					t.Fatalf("%s holds %v after the refusal", dir, entries)
				}
			}
			if _, data := lastCall(t, s); !strings.Contains(data, `"status":"file_bound"`) {
				t.Fatalf("event = %s", data)
			}
		})
	}
}
