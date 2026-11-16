package hooks_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
// body before anything is sent; read-comments carries the newest page's
// query and takes a long answer whole, top-level errors beside data
// included.
func TestLinearPackDeclaredCalls(t *testing.T) {
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
	if c, ok := d.Call("read-comments"); !ok || c.Kind != source.Read {
		t.Fatalf("read-comments = %+v, %v, want a read call", c, ok)
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
	ctx := context.Background()
	const cred = "lin_api_test"
	call := func(name string, params map[string]string) (*source.Result, error) {
		return source.Do(ctx, d, name, params, cred, store.ReadSecretFile)
	}

	// v4key renders name as the design's key: the sha256 of a stable name
	// in 8-4-4-4-12 with the version and variant nibbles of a UUID v4.
	v4key := func(name string) string {
		sum := sha256.Sum256([]byte(name))
		h := hex.EncodeToString(sum[:16])
		return h[0:8] + "-" + h[8:12] + "-4" + h[13:16] + "-8" + h[17:20] + "-" + h[20:32]
	}
	key := v4key("report-YRD-1-claim")
	res, err := call("comment", map[string]string{"issue": "YRD-1", "body": "I have this. The post will follow here as a comment.", "key": key})
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
	again, err := call("comment", map[string]string{"issue": "YRD-1", "body": "I have this. The post will follow here as a comment.", "key": key})
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
		_, err := call("comment", c.params)
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
	if _, err := call("comment", map[string]string{"issue": "YRD-1", "body": strings.Repeat("w", 65536), "key": v4key("report-YRD-1-bound")}); err != nil {
		t.Fatalf("a body of 65536 characters = %v", err)
	}

	// read-comments carries the newest page's query: first:50 with the
	// user and botActor subtrees and hasNextPage, and no totalCount,
	// which stands on no connection but the search payloads.
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
	for _, want := range []string{"comments(first: 50)", "user { id name }", "botActor { id name }", "pageInfo { hasNextPage }", `"id":"YRD-2"`} {
		if !strings.Contains(sent, want) {
			t.Errorf("the read sent %s, which holds no %q", sent, want)
		}
	}
	if strings.Contains(sent, "totalCount") {
		t.Errorf("the read sent %s, which asks for totalCount", sent)
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

	// GraphQL errors ride HTTP 200 beside data, and arrive whole: no
	// select drops them.
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"commentCreate":null},"errors":[{"message":"no such issue"}]}`)
	}))
	defer failing.Close()
	d.Base = failing.URL
	res, err = call("comment", map[string]string{"issue": "YRD-9", "body": "words", "key": key})
	if err != nil || !res.OK() {
		t.Fatalf("errors beside data = %+v, %v", res, err)
	}
	for _, want := range []string{`"commentCreate":null`, "no such issue"} {
		if !strings.Contains(string(res.Answer), want) {
			t.Errorf("errors beside data answered %s, which holds no %q", res.Answer, want)
		}
	}
}
