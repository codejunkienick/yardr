package pack_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"yardr.dev/yardr/internal/pack"
)

// serveRepository serves dir as the pack name of a stand-in packs
// repository, at <origin><name>/, read live from dir: a test that changes a
// file there changes what the origin serves next. It answers the origin and
// how many requests the origin has seen.
func serveRepository(t *testing.T, name, dir string) (string, *atomic.Int64) {
	t.Helper()
	hits := new(atomic.Int64)
	prefix := "/yardr-packs/main/" + name + "/"
	files := http.StripPrefix(prefix, http.FileServer(http.Dir(dir)))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		files.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/yardr-packs/main/", hits
}

// A pack of the packs repository is named by its origin and one pack's
// name and nothing else: another host, a look-alike host, a plain-http
// scheme, another ref (a commit of a fork is served under the repository's
// own path), a sub-path, a dot-dot, an escaped slash, a query and a
// fragment each refuse, and a bare name or a directory is no URL at all.
func TestRepositoryPackNamesOnlyItsOrigin(t *testing.T) {
	const origin = "https://repo.example.com/yardr-packs/main/"
	for _, tc := range []struct {
		name, from string
		want       string // the refusal's words; "" accepts
	}{
		{"with its slash", origin + "webdesign/", ""},
		{"without its slash", origin + "webdesign", ""},
		{"another host", "https://evil.example.com/yardr-packs/main/webdesign/", "is not in the packs repository"},
		{"a host it is a prefix of", "https://repo.example.com.evil.example/yardr-packs/main/webdesign/", "is not in the packs repository"},
		{"user information", "https://repo.example.com@evil.example/yardr-packs/main/webdesign/", "is not in the packs repository"},
		{"plain http", "http://repo.example.com/yardr-packs/main/webdesign/", "is not in the packs repository"},
		{"another ref", "https://repo.example.com/yardr-packs/0123456789abcdef0123456789abcdef01234567/webdesign/", "is not in the packs repository"},
		{"a sub-path", origin + "webdesign/roles/", "want one pack's name"},
		{"the repository itself", origin, "want one pack's name"},
		{"a dot-dot", origin + "../webdesign/", "want one pack's name"},
		{"an escaped slash", origin + "web%2Fdesign/", "want one pack's name"},
		{"a query", origin + "webdesign/?ref=other", "want one pack's name"},
		{"a fragment", origin + "webdesign/#roles", "want one pack's name"},
		{"a bare name", "webdesign", "is not in the packs repository"},
		{"a directory", "./webdesign", "is not in the packs repository"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name, url, err := pack.RepositoryPack(origin, tc.from)
			if tc.want == "" {
				if err != nil || name != "webdesign" || url != origin+"webdesign/" {
					t.Fatalf("RepositoryPack = %q, %q, %v; want webdesign at %swebdesign/", name, url, err, origin)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("RepositoryPack = %q, %q, %v; want a refusal saying %q", name, url, err, tc.want)
			}
		})
	}
}

// Loading a pack of the repository holds it to its pin before anything of
// it is read for a plan: no pin, another digest, another name than its
// directory's and another origin each refuse, the last before any request
// goes out; and a pack that passes is read from the first answer of the
// server, whatever the server answers afterwards.
func TestLoadRepositoryHoldsThePin(t *testing.T) {
	dir := copyStarter(t)
	origin, hits := serveRepository(t, "starter", dir)
	from := origin + "starter/"
	st, o := yard(t)
	digest := build(t, st, load(t, dir), o).Digest

	p, err := pack.LoadRepository(origin, from, digest)
	if err != nil || p.Name != "starter" || p.Dir != from {
		t.Fatalf("LoadRepository = %+v, %v; want starter read from %s", p, err, from)
	}
	for _, tc := range []struct{ name, digest, want string }{
		{"no pin", "", "applied pinned"},
		{"another digest", strings.Repeat("a", 64), "is not the one pinned"},
		{"no digest at all", "xyz", "not a digest"},
	} {
		if _, err := pack.LoadRepository(origin, from, tc.digest); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v; want a refusal saying %q", tc.name, err, tc.want)
		}
	}
	before := hits.Load()
	if _, err := pack.LoadRepository("https://repo.example.com/yardr-packs/main/", origin+"starter/", digest); err == nil {
		t.Error("a URL that is not the origin's was loaded")
	}
	if hits.Load() != before {
		t.Errorf("a refused origin cost %d requests; want none", hits.Load()-before)
	}

	// A pack whose own name is another than its directory's is refused:
	// the directory is the name its import records.
	otherOrigin, _ := serveRepository(t, "other", dir)
	if _, err := pack.LoadRepository(otherOrigin, otherOrigin+"other/", digest); err == nil || !strings.Contains(err.Error(), "is called what its directory is") {
		t.Errorf("a pack at another name's directory: err = %v", err)
	}

	// The origin answers differently once the digest was checked: the
	// plan and the install read what the digest named.
	role := filepath.Join(dir, "roles", "builder.md")
	first, err := p.ReadFile("roles/builder.md")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(role, []byte("a second answer"), 0o644); err != nil {
		t.Fatal(err)
	}
	if again, err := p.ReadFile("roles/builder.md"); err != nil || string(again) != string(first) {
		t.Errorf("ReadFile after the origin changed = %q, %v; want the first answer", again, err)
	}
}
