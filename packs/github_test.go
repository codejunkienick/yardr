package packs_test

import (
	"context"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"yardr.dev/yardr/internal/source"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/packs"
)

const ghToken = "gh-test-token"

// githubStandin answers as api.github.com does for the declaration's two
// who-acts calls, and keeps the last request it was sent: /user for a
// personal token, /app for an App credential's JWT.
type githubStandin struct {
	t    *testing.T
	mu   sync.Mutex
	last string
}

func (s *githubStandin) handler(w http.ResponseWriter, r *http.Request) {
	if auth := r.Header.Get("Authorization"); auth != "Bearer "+ghToken {
		s.t.Errorf("%s %s: Authorization %q, want the Bearer token", r.Method, r.URL.Path, auth)
	}
	s.mu.Lock()
	s.last = r.Method + " " + r.URL.Path
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch r.Method + " " + r.URL.Path {
	case "GET /user":
		io.WriteString(w, `{"login":"ada","id":123,"type":"User","site_admin":false}`)
	case "GET /app":
		io.WriteString(w, `{"id":456,"slug":"yard","name":"Yard","owner":{"login":"acme"}}`)
	default:
		s.t.Errorf("the stand-in has no answer for %s %s", r.Method, r.URL.Path)
		http.Error(w, "no such recorded shape", 404)
	}
}

func (s *githubStandin) request() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

func githubDeclaration(t *testing.T, standin string) *source.Declaration {
	t.Helper()
	raw, err := fs.ReadFile(packs.FS, "github/github.yaml")
	if err != nil {
		t.Fatal(err)
	}
	d, err := source.Parse("github.yaml",
		[]byte(strings.Replace(string(raw), "base: https://api.github.com", "base: "+standin, 1)))
	if err != nil {
		t.Fatal(err)
	}
	if d.Base != standin {
		t.Fatalf("base %s, want the stand-in", d.Base)
	}
	return d
}

// The declaration's two who-acts calls, against the stand-in: user asks
// GET /user and answers the token's login and kind, app asks GET /app
// and answers the App's slug and id. Each answers its select alone,
// whatever else the service said.
func TestGitHubCallsAgainstTheStandin(t *testing.T) {
	standin := &githubStandin{t: t}
	srv := httptest.NewServer(http.HandlerFunc(standin.handler))
	defer srv.Close()
	d := githubDeclaration(t, srv.URL)
	for _, tc := range []struct {
		call       string
		path       string
		wantAnswer string
	}{
		{"user", "GET /user", `{"login":"ada","type":"User"}`},
		{"app", "GET /app", `{"id":456,"slug":"yard"}`},
	} {
		res, err := source.Do(context.Background(), d, tc.call, nil, ghToken, store.ReadSecretFile)
		if err != nil {
			t.Errorf("call %s: %v", tc.call, err)
			continue
		}
		if !res.OK() {
			t.Errorf("call %s: status %d, want a success", tc.call, res.Status)
			continue
		}
		if got := standin.request(); got != tc.path {
			t.Errorf("call %s sent %q, want %q", tc.call, got, tc.path)
		}
		if string(res.Answer) != tc.wantAnswer {
			t.Errorf("call %s answer %s, want %s", tc.call, res.Answer, tc.wantAnswer)
		}
	}
}
