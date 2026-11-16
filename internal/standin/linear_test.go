package standin

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
