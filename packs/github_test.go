package packs_test

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"yardr.dev/yardr/internal/source"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/packs"
)

const ghToken = "gh-test-token"

// longPRBody is a pull request description over the envelope's 500-rune
// cut, as PR #38's 6,721 characters are: pr_body must return it whole,
// where the envelope and the bead filed from it keep only 500 runes
// (internal/hooks' truncate tests).
var longPRBody = strings.Repeat("the harness ", 60) + "the end"

// githubStandin answers as api.github.com does for the declaration's
// who-acts calls and its three pull request reads, and keeps the last
// request it was sent: /user for a personal token, /app for an App
// credential's JWT, and /repos/acme/web/pulls/38 with its reviews and
// inline comments for the pr_ calls.
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
	s.last = r.Method + " " + r.URL.RequestURI()
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch r.Method + " " + r.URL.Path {
	case "GET /user":
		io.WriteString(w, `{"login":"ada","id":123,"type":"User","site_admin":false}`)
	case "GET /app":
		io.WriteString(w, `{"id":456,"slug":"yard","name":"Yard","owner":{"login":"acme"}}`)
	case "GET /repos/acme/web/pulls/38":
		body, _ := json.Marshal(longPRBody)
		io.WriteString(w, `{"number":38,"title":"A long pull request","body":`+string(body)+`,"user":{"login":"ada","id":7},"state":"open","html_url":"https://github.com/acme/web/pull/38"}`)
	case "GET /repos/acme/web/pulls/38/reviews":
		io.WriteString(w, `[{"id":11,"user":{"login":"coderabbit","id":8},"state":"COMMENTED","body":"Four findings on the diff.","submitted_at":"2026-11-21T07:00:00Z","html_url":"https://github.com/acme/web/pull/38#pullrequestreview-11"},`+
			`{"id":12,"user":{"login":"hal","id":9},"state":"APPROVED","body":"","submitted_at":"2026-11-21T08:00:00Z","html_url":"https://github.com/acme/web/pull/38#pullrequestreview-12"}]`)
	case "GET /repos/acme/web/pulls/38/comments":
		io.WriteString(w, `[{"id":21,"user":{"login":"coderabbit","id":8},"body":"This line leaks the file.","path":"main.go","line":12,"created_at":"2026-11-21T07:01:00Z","updated_at":"2026-11-21T07:01:00Z","html_url":"https://github.com/acme/web/pull/38#discussion_r21"}]`)
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

// The declaration's calls, against the stand-in: user asks GET /user
// and answers the token's login and kind, app asks GET /app and
// answers the App's slug and id, and the three pr_ calls read a pull
// request whole. Each answers its select alone, whatever else the
// service said. pr_body's fixture body is over the envelope's
// 500-rune cut and arrives whole here, where the envelope and the
// bead filed from it keep only 500 runes.
func TestGitHubCallsAgainstTheStandin(t *testing.T) {
	if len([]rune(longPRBody)) <= 500 {
		t.Fatalf("fixture body is %d runes, want over the 500-rune cut", len([]rune(longPRBody)))
	}
	standin := &githubStandin{t: t}
	srv := httptest.NewServer(http.HandlerFunc(standin.handler))
	defer srv.Close()
	d := githubDeclaration(t, srv.URL)
	pr := map[string]string{"owner": "acme", "repo": "web", "number": "38"}
	for _, tc := range []struct {
		call       string
		params     map[string]string
		path       string
		wantAnswer string
	}{
		{"user", nil, "GET /user", `{"login":"ada","type":"User"}`},
		{"app", nil, "GET /app", `{"id":456,"slug":"yard"}`},
		{"pr_body", pr, "GET /repos/acme/web/pulls/38",
			`{"body":` + strconv.Quote(longPRBody) + `,"number":38,"title":"A long pull request","user.login":"ada"}`},
		{"pr_reviews", pr, "GET /repos/acme/web/pulls/38/reviews?per_page=100",
			`[{"body":"Four findings on the diff.","state":"COMMENTED","submitted_at":"2026-11-21T07:00:00Z","user.login":"coderabbit"},` +
				`{"body":"","state":"APPROVED","submitted_at":"2026-11-21T08:00:00Z","user.login":"hal"}]`},
		{"pr_comments", pr, "GET /repos/acme/web/pulls/38/comments?per_page=100",
			`[{"body":"This line leaks the file.","created_at":"2026-11-21T07:01:00Z","line":12,"path":"main.go","user.login":"coderabbit"}]`},
	} {
		res, err := source.Do(context.Background(), d, tc.call, tc.params, ghToken, store.ReadSecretFile)
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
