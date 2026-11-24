package hooks_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/scripttest"
	"yardr.dev/yardr/internal/source"
	"yardr.dev/yardr/internal/standin"
	"yardr.dev/yardr/internal/store"
)

// The core sends a declared call's credential in the header the
// declaration's credential names and nowhere else a call has, so a
// declaration with no header reached Linear unauthenticated through yardr
// call linear viewer and yardr source check linear. Linear takes an OAuth
// access token as a Bearer token. The thread script builds its own header
// (a personal key bare, anything else as Bearer), which
// TestLinearPackAuthHeader holds.
func TestLinearPackDeclaredCallSendsTheCredential(t *testing.T) {
	dir := filepath.Join("..", "..", "packs", "linear")
	text, err := os.ReadFile(filepath.Join(dir, "linear.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := source.Parse(filepath.Join(dir, "linear.yaml"), text)
	if err != nil {
		t.Fatal(err)
	}
	if c := d.Credential; c == nil || c.Env != "LINEAR_API_KEY" || c.Header != "Authorization: Bearer {credential}" {
		t.Errorf("credential = %+v", d.Credential)
	}
	if d.Check != "viewer" {
		t.Fatalf("check = %q, want the viewer call", d.Check)
	}

	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"viewer":{"id":"u-yardr","name":"Yardr"}}}`)
	}))
	defer srv.Close()
	d.Base = srv.URL
	const cred = "lin_oauth_test"
	res, err := source.Do(context.Background(), d, d.Check, nil, cred, store.ReadSecretFile)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() || !strings.Contains(string(res.Answer), "u-yardr") {
		t.Errorf("the check call was answered %d with %s", res.Status, res.Answer)
	}
	if auth != "Bearer "+cred {
		t.Errorf("the check call was sent with Authorization %q, want %q", auth, "Bearer "+cred)
	}
}

// The pack's declared comment and read-comments calls, as an exec group
// calls them under its grants: comment posts one top-level comment with
// the key as its own id, the same key twice posting once, and refuses a
// key that is no UUID, an issue that is no identifier and an over-long
// body before anything is sent; with a picture the call's build uploads
// it first and the comment carries its image line under the words, and
// a refused upload or a file that is no PNG or JPEG posts nothing;
// read-comments carries the newest page's query, pages past it with an
// after cursor, and takes a long answer whole, top-level errors beside
// data included.
func TestLinearPackDeclaredCalls(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is not installed, and the comment call's build command is written in it")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl is not installed, and the comment call's build command calls Linear with it")
	}
	if _, err := exec.LookPath("file"); err != nil {
		t.Skip("file is not installed, and the comment call's build command reads a picture's type with it")
	}
	dir := filepath.Join("..", "..", "packs", "linear")
	text, err := os.ReadFile(filepath.Join(dir, "linear.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := source.Parse(filepath.Join(dir, "linear.yaml"), text)
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := d.Call("comment"); !ok || c.Kind != source.Update {
		t.Fatalf("comment = %+v, %v, want an update call", c, ok)
	}
	// The comment call is built, not templated: its build script uploads
	// the picture and prints the commentCreate body.
	cc, _ := d.Call("comment")
	if strings.Join(cc.Build, " ") != "linear/build-comment" || cc.Request.Body != nil {
		t.Fatalf("comment build = %v with body %v, want [linear/build-comment] and no request body", cc.Build, cc.Request.Body)
	}
	var pic *source.Param
	for i := range cc.Params {
		if cc.Params[i].Name == "pictures" {
			pic = &cc.Params[i]
		}
	}
	if pic == nil || pic.Type != "file" || pic.Max != 8388608 || !pic.Optional {
		t.Fatalf("pictures = %+v, want an optional file of at most 8388608 bytes", pic)
	}
	if c, ok := d.Call("read-comments"); !ok || c.Kind != source.Read {
		t.Fatalf("read-comments = %+v, %v, want a read call", c, ok)
	}
	// The page past the first is read with an after cursor, in the
	// recordings call's shape: an optional string, left out of the first
	// page's variables.
	rc, _ := d.Call("read-comments")
	var after *source.Param
	for i := range rc.Params {
		if rc.Params[i].Name == "after" {
			after = &rc.Params[i]
		}
	}
	if after == nil || after.Type != "string" || after.Max != 512 || !after.Optional || len(after.Default) != 0 {
		t.Fatalf("after = %+v, want an optional string of at most 512 with no default", after)
	}

	s := &standin.Server{LinearKey: "lin_api_test"}
	s.Add(standin.Issue{Source: "linear", ID: "uuid-1", Key: "YRD-1", Title: "Web view", Status: "Todo"})
	var bodies []string
	inner := s.Handler()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/graphql") {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			bodies = append(bodies, string(raw))
		}
		inner.ServeHTTP(w, r)
	}))
	defer srv.Close()
	d.Base = srv.URL
	home := t.TempDir()
	if err := d.CopyCommandFiles(dir, home); err != nil {
		t.Fatal(err)
	}
	scripttest.Relink(t, filepath.Join(home, "sources", d.Name))
	ctx := context.Background()
	const cred = "lin_api_test"
	call := func(name string, params map[string]string) (*source.Result, error) {
		return source.Do(ctx, d, name, params, cred, store.ReadSecretFile)
	}
	// comment goes as the store sends a built call: the build prints the
	// body, which is then sent as it stands.
	comment := func(params map[string]string) (*source.Result, error) {
		body, err := source.BuildBody(ctx, d, "comment", params, cred, home, store.ReadSecretFile)
		if err != nil {
			return nil, err
		}
		return source.DoKeyedBody(ctx, d, "comment", params, cred, "", body, store.ReadSecretFile)
	}

	// v4key renders name as the design's key: the sha256 of a stable name
	// in 8-4-4-4-12 with the version and variant nibbles of a UUID v4.
	v4key := func(name string) string {
		sum := sha256.Sum256([]byte(name))
		h := hex.EncodeToString(sum[:16])
		return h[0:8] + "-" + h[8:12] + "-4" + h[13:16] + "-8" + h[17:20] + "-" + h[20:32]
	}
	key := v4key("report-YRD-1-claim")
	res, err := comment(map[string]string{"issue": "YRD-1", "body": "I have this. The post will follow here as a comment.", "key": key})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() || !strings.Contains(string(res.Answer), key) || !strings.Contains(string(res.Answer), `"success":true`) {
		t.Fatalf("comment = %+v", res)
	}
	sent := bodies[len(bodies)-1]
	for _, want := range []string{"commentCreate", `"id":"` + key + `"`, `"issueId":"YRD-1"`, "The post will follow here"} {
		if !strings.Contains(sent, want) {
			t.Errorf("the comment sent %s, which holds no %q", sent, want)
		}
	}
	if strings.Contains(sent, "parentId") {
		t.Errorf("the comment sent %s, which names a parent", sent)
	}
	// The same key twice posts once, and the second call is answered
	// with what the first left.
	again, err := comment(map[string]string{"issue": "YRD-1", "body": "I have this. The post will follow here as a comment.", "key": key})
	if err != nil || string(again.Answer) != string(res.Answer) {
		t.Fatalf("the same key twice = %+v, %v, want the first call's answer", again, err)
	}
	if cs := s.LinearComments("YRD-1"); len(cs) != 1 || cs[0].ID != key {
		t.Fatalf("comments = %+v, want the one post", cs)
	}

	// What the call refuses before anything is sent: the errors name the
	// parameter and the bound, never the value.
	n := len(bodies)
	for _, c := range []struct {
		name   string
		params map[string]string
		want   string
		secret string
	}{
		{"a key that is no UUID", map[string]string{"issue": "YRD-1", "body": "words", "key": "no-such-key"}, "parameter key does not match its pattern", "no-such-key"},
		{"a missing key", map[string]string{"issue": "YRD-1", "body": "words"}, "parameter key is missing", ""},
		{"a lowercase issue", map[string]string{"issue": "yrd-1", "body": "words", "key": key}, "parameter issue does not match its pattern", "yrd-1"},
		{"a UUID issue", map[string]string{"issue": "9d4c3b2a-1f0e-4d5c-8b7a-6f5e4d3c2b1a", "body": "words", "key": key}, "parameter issue does not match its pattern", "9d4c3b2a"},
		{"an over-long body", map[string]string{"issue": "YRD-1", "body": strings.Repeat("w", 65537), "key": key}, "parameter body is 65537 characters, and at most 65536", strings.Repeat("w", 100)},
	} {
		_, err := comment(c.params)
		var ce *source.CallError
		switch {
		case !errors.As(err, &ce) || ce.Reason != source.BadParam:
			t.Errorf("%s: err = %v, want a bad_param refusal", c.name, err)
		case !strings.Contains(err.Error(), c.want):
			t.Errorf("%s: err = %v, want it to say %q", c.name, err, c.want)
		case c.secret != "" && strings.Contains(err.Error(), c.secret):
			t.Errorf("%s: err = %v, which echoes the value", c.name, err)
		}
	}
	if len(bodies) != n {
		t.Fatalf("a refused call sent %d requests", len(bodies)-n)
	}
	// At the bound the body posts: refused is over, never at.
	if _, err := comment(map[string]string{"issue": "YRD-1", "body": strings.Repeat("w", 65536), "key": v4key("report-YRD-1-bound")}); err != nil {
		t.Fatalf("a body of 65536 characters = %v", err)
	}

	// A picture rides the build: uploaded first, then one image line
	// under the words in the one comment the call posts. The stand-in's
	// uploads are this test's first, so the picture is u-1.
	png := linearPNG(t)
	res, err = comment(map[string]string{"issue": "YRD-1", "body": "the result", "key": v4key("report-YRD-1-picture"), "pictures": linearFile(t, "shot.png", png)})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() {
		t.Fatalf("comment with a picture = %+v", res)
	}
	posted := s.LinearComments("YRD-1")
	wantBody := "the result\n\n![shot.png](https://uploads.linear.app/standin/u-1/shot.png)"
	if len(posted) != 3 || posted[2].Body != wantBody {
		t.Fatalf("comments = %+v, want the picture comment %q last", posted, wantBody)
	}
	if ups := s.LinearUploads(); len(ups) != 1 || !ups[0].Sent || ups[0].Bearer ||
		ups[0].Filename != "shot.png" || ups[0].ContentType != "image/png" || ups[0].Size != len(png) {
		t.Fatalf("uploads = %+v, want the one picture, sent with no token", ups)
	}
	noSecrets(t, "the picture call's answer", string(res.Answer), linearSecrets(&linearGolden{s: s}, cred))

	// An upload the host refuses fails the build and posts nothing, and
	// its words hold the status alone: no signed address, no header, no
	// token.
	s.RefuseLinearUpload("refused.png", 403)
	before := len(s.LinearComments("YRD-1"))
	_, err = comment(map[string]string{"issue": "YRD-1", "body": "the result", "key": v4key("report-YRD-1-refused"), "pictures": linearFile(t, "refused.png", png)})
	if err == nil {
		t.Fatal("a refused upload was posted")
	}
	var ce *source.CallError
	if !errors.As(err, &ce) || ce.Reason != source.BuildRefused || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("a refused upload = %v, want a build_refused refusal naming HTTP 403", err)
	}
	noSecrets(t, "the refused upload's error", err.Error(), linearSecrets(&linearGolden{s: s}, cred))
	if n := len(s.LinearComments("YRD-1")); n != before {
		t.Fatalf("a refused upload left %d comments, want %d", n, before)
	}
	s.RefuseLinearUpload("refused.png", 0)

	// A file that is no picture is refused before anything is uploaded,
	// and a picture over the bound is refused by the yard before the
	// build runs: neither posts.
	uploadsBefore := len(s.LinearUploads())
	_, err = comment(map[string]string{"issue": "YRD-1", "body": "the result", "key": v4key("report-YRD-1-nonimage"), "pictures": linearFile(t, "notes.pdf", linearPDF)})
	if err == nil || !strings.Contains(err.Error(), "not a PNG or JPEG picture") {
		t.Fatalf("a non-picture = %v, want its refusal", err)
	}
	noSecrets(t, "the non-picture's error", err.Error(), linearSecrets(&linearGolden{s: s}, cred))
	_, err = comment(map[string]string{"issue": "YRD-1", "body": "the result", "key": v4key("report-YRD-1-big"), "pictures": linearFile(t, "big.png", make([]byte, 8388608+1))})
	if !errors.As(err, &ce) || ce.Reason != source.BadParam ||
		!strings.Contains(err.Error(), "parameter pictures is 8388609 bytes, and at most 8388608") {
		t.Fatalf("an over-limit picture = %v, want a bad_param refusal naming the bound", err)
	}
	if n := len(s.LinearComments("YRD-1")); n != before {
		t.Fatalf("the refused pictures left %d comments, want %d", n, before)
	}
	if n := len(s.LinearUploads()); n != uploadsBefore {
		t.Fatalf("the refused pictures uploaded %d files, want none", n-uploadsBefore)
	}

	// read-comments carries the newest page's query: first:50 with the
	// user and botActor subtrees and hasNextPage beside endCursor, and no
	// totalCount, which stands on no connection but the search payloads.
	// Without after the call reads the first page, as before: its
	// variables carry the issue alone, no cursor.
	s.Add(standin.Issue{Source: "linear", ID: "uuid-2", Key: "YRD-2", Title: "Links", Status: "Backlog",
		LinearComments: []standin.LinearComment{
			{ID: "c1", Body: "look?", By: "Ann", ByID: "u-ann"},
			{ID: "c2", Body: "on it", By: "App", ByID: "a-app", Bot: true},
		}})
	res, err = call("read-comments", map[string]string{"issue": "YRD-2"})
	if err != nil {
		t.Fatal(err)
	}
	sent = bodies[len(bodies)-1]
	for _, want := range []string{"comments(first: 50, after: $c)", "query($id: String!, $c: String)", "user { id name }", "botActor { id name }", "pageInfo { hasNextPage endCursor }", `"variables":{"id":"YRD-2"}`} {
		if !strings.Contains(sent, want) {
			t.Errorf("the read sent %s, which holds no %q", sent, want)
		}
	}
	if strings.Contains(sent, "totalCount") {
		t.Errorf("the read sent %s, which asks for totalCount", sent)
	}
	if strings.Contains(sent, `"c":`) {
		t.Errorf("the first page sent %s, which names a cursor", sent)
	}
	// Nothing selected away: the whole answer arrives, both authors with it.
	for _, want := range []string{"look?", "on it", "botActor", `"hasNextPage":false`} {
		if !strings.Contains(string(res.Answer), want) {
			t.Errorf("the read answered %s, which holds no %q", res.Answer, want)
		}
	}

	// Past the 64KB default the raised max takes the answer, refused, not cut, past its own.
	big := strings.Repeat("w", 25000)
	s.Add(standin.Issue{Source: "linear", ID: "uuid-3", Key: "YRD-3", Title: "Long", Status: "Backlog",
		LinearComments: []standin.LinearComment{{ID: "c1", Body: big}, {ID: "c2", Body: big}, {ID: "c3", Body: big}}})
	res, err = call("read-comments", map[string]string{"issue": "YRD-3"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Answer) <= 64<<10 {
		t.Fatalf("the long read answered %d bytes, want past the 64KB default", len(res.Answer))
	}

	// A two-page read: the first page's endCursor goes back as after,
	// and the second page follows it. The stand-in answers every issue
	// at once, so a paging stub stands in for Linear here.
	var paged []string
	pager := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		paged = append(paged, string(raw))
		var req struct {
			Variables map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(raw, &req)
		w.Header().Set("Content-Type", "application/json")
		if c, _ := req.Variables["c"].(string); c == "" {
			_, _ = io.WriteString(w, `{"data":{"issue":{"comments":{"nodes":[{"id":"c1","body":"first words"}],"pageInfo":{"hasNextPage":true,"endCursor":"p2"}}}}}`)
		} else {
			_, _ = io.WriteString(w, `{"data":{"issue":{"comments":{"nodes":[{"id":"c2","body":"older words"}],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}`)
		}
	}))
	defer pager.Close()
	d.Base = pager.URL
	first, err := call("read-comments", map[string]string{"issue": "YRD-2"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"hasNextPage":true`, `"endCursor":"p2"`, "first words"} {
		if !strings.Contains(string(first.Answer), want) {
			t.Errorf("the first page answered %s, which holds no %q", first.Answer, want)
		}
	}
	if strings.Contains(paged[len(paged)-1], `"c":`) {
		t.Errorf("the first page sent %s, which names a cursor", paged[len(paged)-1])
	}
	second, err := call("read-comments", map[string]string{"issue": "YRD-2", "after": "p2"})
	if err != nil {
		t.Fatal(err)
	}
	if sent := paged[len(paged)-1]; !strings.Contains(sent, `"c":"p2"`) {
		t.Errorf("the second page sent %s, which carries no cursor", sent)
	}
	for _, want := range []string{`"hasNextPage":false`, "older words"} {
		if !strings.Contains(string(second.Answer), want) {
			t.Errorf("the second page answered %s, which holds no %q", second.Answer, want)
		}
	}
	// An over-long cursor is refused before anything is sent, as any
	// over-long value is.
	n = len(paged)
	if _, err := call("read-comments", map[string]string{"issue": "YRD-2", "after": strings.Repeat("c", 513)}); err == nil {
		t.Error("a cursor of 513 characters was sent")
	} else {
		var ce *source.CallError
		switch {
		case !errors.As(err, &ce) || ce.Reason != source.BadParam:
			t.Errorf("a cursor of 513 characters = %v, want a bad_param refusal", err)
		case !strings.Contains(err.Error(), "parameter after is 513 characters, and at most 512"):
			t.Errorf("a cursor of 513 characters = %v, want it to name the bound", err)
		}
	}
	if len(paged) != n {
		t.Fatalf("a refused read sent %d requests", len(paged)-n)
	}

	// GraphQL errors ride HTTP 200 beside data, and arrive whole: no
	// select drops them.
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"commentCreate":null},"errors":[{"message":"no such issue"}]}`)
	}))
	defer failing.Close()
	d.Base = failing.URL
	res, err = comment(map[string]string{"issue": "YRD-9", "body": "words", "key": key})
	if err != nil || !res.OK() {
		t.Fatalf("errors beside data = %+v, %v", res, err)
	}
	for _, want := range []string{`"commentCreate":null`, "no such issue"} {
		if !strings.Contains(string(res.Answer), want) {
			t.Errorf("errors beside data answered %s, which holds no %q", res.Answer, want)
		}
	}
}
