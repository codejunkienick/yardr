package standin

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

// A client's tests of what it does with Linear's file storage stand on this
// being as strict as the storage host is: a client that sent the bearer
// token to the signed address, forgot a named header or told the wrong size
// must be refused here, or its tests prove nothing. The stand-in answers
// fileUpload with a place, takes the file there only as that place says,
// and keeps what came of each file.
func TestLinearFileUpload(t *testing.T) {
	s := &Server{LinearKey: "tok"}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	// no redirect is followed: a refusal's Location is the client's to keep
	// from.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	ask := func(token string, vars map[string]any) (slot map[string]any, errs []any) {
		t.Helper()
		query := `mutation($contentType: String!, $filename: String!, $size: Int!) { fileUpload(contentType: $contentType, filename: $filename, size: $size) { success uploadFile { uploadUrl assetUrl headers { key value } } } }`
		raw, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/graphql", bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var got struct {
			Data   map[string]map[string]any `json:"data"`
			Errors []any                     `json:"errors"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		return got.Data["fileUpload"], got.Errors
	}

	// What it refuses to give a place for.
	good := map[string]any{"contentType": "image/png", "filename": "shed (2).png", "size": 4}
	for name, c := range map[string]struct {
		token string
		vars  map[string]any
	}{
		"another token": {"other", good},
		"no type":       {"tok", map[string]any{"filename": "a.png", "size": 4}},
		"a path":        {"tok", map[string]any{"contentType": "image/png", "filename": "dir/a.png", "size": 4}},
		"no name":       {"tok", map[string]any{"contentType": "image/png", "size": 4}},
		"a fraction":    {"tok", map[string]any{"contentType": "image/png", "filename": "a.png", "size": 4.5}},
		"a size's word": {"tok", map[string]any{"contentType": "image/png", "filename": "a.png", "size": "4"}},
		"no size":       {"tok", map[string]any{"contentType": "image/png", "filename": "a.png"}},
	} {
		if slot, errs := ask(c.token, c.vars); slot != nil || len(errs) == 0 {
			t.Errorf("%s: slot %v, errors %v, want a refusal", name, slot, errs)
		}
	}
	if got := s.LinearUploads(); len(got) != 0 {
		t.Fatalf("a refused ask left uploads: %+v", got)
	}

	slot, errs := ask("tok", good)
	if len(errs) != 0 || slot == nil {
		t.Fatalf("fileUpload: %v, %v", slot, errs)
	}
	file, _ := slot["uploadFile"].(map[string]any)
	uploadURL, _ := file["uploadUrl"].(string)
	if !strings.HasPrefix(uploadURL, srv.URL+"/uploads/u-1?sig=") || file["assetUrl"] != "https://uploads.linear.app/standin/u-1/shed (2).png" {
		t.Fatalf("uploadFile = %v", file)
	}
	headers, _ := file["headers"].([]any)
	if len(headers) != 2 {
		t.Fatalf("headers = %v, want the default two", headers)
	}

	body := []byte("png!")
	// put sends body to the signed address as a client should, after change
	// has had its way with the request.
	put := func(url string, change func(*http.Request)) (int, string, http.Header) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
		req.Header.Set("Content-Type", "image/png")
		req.Header.Set("Cache-Control", "public, max-age=31536000")
		for _, h := range s.LinearUploads()[0].Headers {
			req.Header.Set(h.Key, h.Value)
		}
		if change != nil {
			change(req)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		page, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(page), resp.Header
	}
	for _, c := range []struct {
		name   string
		url    string
		change func(*http.Request)
		want   int
	}{
		{"another signature", strings.Replace(uploadURL, "sig=", "sig=0", 1), nil, 403},
		{"the bearer token", uploadURL, func(r *http.Request) { r.Header.Set("Authorization", "Bearer tok") }, 400},
		{"another type", uploadURL, func(r *http.Request) { r.Header.Set("Content-Type", "application/pdf") }, 403},
		{"no cache control", uploadURL, func(r *http.Request) { r.Header.Del("Cache-Control") }, 403},
		{"a named header lost", uploadURL, func(r *http.Request) { r.Header.Del("X-Linear-Upload-Token") }, 403},
		{"a named header changed", uploadURL, func(r *http.Request) { r.Header.Set("X-Linear-Note", "another") }, 403},
		{"another length", uploadURL, func(r *http.Request) {
			r.Body, r.ContentLength = io.NopCloser(strings.NewReader("png")), 3
		}, 400},
	} {
		if status, _, _ := put(c.url, c.change); status != c.want {
			t.Errorf("a PUT with %s: %d, want %d", c.name, status, c.want)
		}
	}
	if got := s.LinearUploads()[0]; got.Sent || !got.Bearer {
		t.Fatalf("after the refusals: sent %v, bearer %v, want the file unsent and the bearer token noted", got.Sent, got.Bearer)
	}

	// A refused file is answered as told, with a page that gives back what
	// the request held, and a redirect names a place the stand-in notes if a
	// client goes there.
	s.RefuseLinearUpload("shed (2).png", 307)
	status, page, header := put(uploadURL, nil)
	sig := s.LinearUploads()[0].sig
	if status != 307 || !strings.Contains(page, sig) || !strings.Contains(page, s.LinearUploads()[0].Headers[0].Value) {
		t.Fatalf("a refused PUT: %d %q, want 307 and the page to repeat the address and the headers", status, page)
	}
	if where := header.Get("Location"); where != "/uploads/u-1/moved?sig="+sig {
		t.Fatalf("Location = %q", where)
	}
	s.RefuseLinearUpload("shed (2).png", 0)

	status, _, _ = put(uploadURL, nil)
	sum := sha256.Sum256(body)
	if got := s.LinearUploads()[0]; status != 200 || !got.Sent || got.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("the right PUT: %d, %+v", status, got)
	}
	if status, _, _ := put(srv.URL+"/uploads/u-9?sig="+sig, nil); status != 404 {
		t.Errorf("a PUT to a place never given: %d", status)
	}

	// A client that sets the headers fileUpload names for it gets them.
	s.SetLinearUploadHeaders([]LinearHeader{{"x-only", "one"}})
	if slot, errs := ask("tok", good); len(errs) != 0 || len(slot["uploadFile"].(map[string]any)["headers"].([]any)) != 1 {
		t.Fatalf("headers set to one: %v, %v", slot, errs)
	}

	want := []string{
		"linear refused", "linear upload shed (2).png image/png 4",
		"linear put shed (2).png 403", "linear put shed (2).png 400", "linear put shed (2).png 403", "linear put shed (2).png 403",
		"linear put shed (2).png 403", "linear put shed (2).png 403", "linear put shed (2).png 400",
		"linear put shed (2).png 307", "linear put shed (2).png 200", "linear upload shed (2).png image/png 4",
	}
	if got := s.Calls(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls = %q, want %q", got, want)
	}
}

// commentCreate takes the caller's id as the comment's own, so the same key
// twice posts once: the second call is answered with the comment the first
// left. A client that lost the first answer retries with its key instead
// of posting again.
func TestLinearCommentCreateIdempotent(t *testing.T) {
	fixed := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s := &Server{LinearKey: "tok", Now: func() time.Time { return fixed }}
	s.Add(Issue{Source: "linear", ID: "uuid-1", Key: "YRD-1", Title: "Web view", Status: "Todo"})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	post := func(issue, id, body string) map[string]any {
		t.Helper()
		query := `mutation($input: CommentCreateInput!) { commentCreate(input: $input) { success comment { id url } } }`
		raw, _ := json.Marshal(map[string]any{"query": query,
			"variables": map[string]any{"input": map[string]any{"id": id, "issueId": issue, "body": body}}})
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/graphql", bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer tok")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var got struct {
			Data   map[string]map[string]any `json:"data"`
			Errors []any                     `json:"errors"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if len(got.Errors) != 0 {
			t.Fatalf("commentCreate: errors %v", got.Errors)
		}
		return got.Data["commentCreate"]
	}

	const key = "9d4c3b2a-1f0e-4d5c-8b7a-6f5e4d3c2b1a"
	first := post("YRD-1", key, "answer")
	if first["success"] != true || first["comment"].(map[string]any)["id"] != key {
		t.Fatalf("first post = %v", first)
	}
	second := post("YRD-1", key, "answer")
	if !reflect.DeepEqual(second, first) {
		t.Fatalf("second post = %v, want the first call's answer %v", second, first)
	}
	if cs := s.LinearComments("YRD-1"); len(cs) != 1 || cs[0].ID != key || cs[0].Body != "answer" {
		t.Fatalf("comments = %+v, want the one post", cs)
	}
	// A retry that changed its words still writes nothing: the key names
	// the comment, not the attempt.
	if again := post("YRD-1", key, "other words"); !reflect.DeepEqual(again, first) {
		t.Fatalf("a retry with other words = %v, want %v", again, first)
	}
	if cs := s.LinearComments("YRD-1"); len(cs) != 1 || cs[0].Body != "answer" {
		t.Fatalf("comments = %+v, want the first post's words", cs)
	}
	// An id is unique across issues too: a reused key answers with the
	// first comment's URL instead of claiming it was posted on another.
	s.Add(Issue{Source: "linear", ID: "uuid-2", Key: "YRD-2", Title: "Other issue", Status: "Todo"})
	if again := post("YRD-2", key, "another issue"); !reflect.DeepEqual(again, first) {
		t.Fatalf("a retry on another issue = %v, want the first post's answer %v", again, first)
	}
	if cs := s.LinearComments("YRD-2"); len(cs) != 0 {
		t.Fatalf("comments on the other issue = %+v, want no post", cs)
	}
	// Another key is another comment.
	post("YRD-1", "7b2a9d4c-3f1e-4a5c-9d8b-6f5e4d3c2b1a", "second")
	if cs := s.LinearComments("YRD-1"); len(cs) != 2 {
		t.Fatalf("comments = %+v, want two posts", cs)
	}

	want := []string{
		"linear comment YRD-1",
		"linear comment YRD-1 " + key,
		"linear comment YRD-1 " + key,
		"linear comment YRD-2 " + key,
		"linear comment YRD-1",
	}
	if got := s.Calls(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls = %q, want %q", got, want)
	}
}
