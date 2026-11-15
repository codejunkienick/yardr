package packs_test

// These test linear-mint, the mint command the linear pack's declaration
// brings (credential.mint_command), against a fake token endpoint. No
// test reaches Linear.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The fake client credentials the script mints from, and the fake access
// token the fake endpoint answers: what the script must never print but
// as the answer's token. The secret carries characters the form encoding
// must survive.
const (
	fakeLinearID     = "a1b2c3d4e5f6-client-id"
	fakeLinearSecret = "s3cr3t+with/special=chars%20here"
	fakeLinearAccess = "lin-oauth-fake-access-token-0123456789abcdef"
)

// linearMintScope is the one scope set every mint sends, whatever scope
// the core passes: read for the reads, issues:create for open and state,
// comments:create for reply.
const linearMintScope = "read,issues:create,comments:create"

// fakeLinearToken is a fake https://api.linear.app/oauth/token: it
// answers mode (good, bad-request, error, bad-json, no-token,
// no-expires, weird-token) with expiresIn seconds (2591999, the grant's
// own, where unset) and keeps the forms it was sent.
type fakeLinearToken struct {
	t         *testing.T
	mu        sync.Mutex
	mode      string
	expiresIn int
	forms     []url.Values
	types     []string
}

func (f *fakeLinearToken) handler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	form, err := url.ParseQuery(string(body))
	if err != nil {
		f.t.Errorf("the endpoint was sent %q: %v", body, err)
	}
	f.mu.Lock()
	f.forms = append(f.forms, form)
	f.types = append(f.types, r.Header.Get("Content-Type"))
	mode, expires := f.mode, f.expiresIn
	f.mu.Unlock()
	if expires == 0 {
		expires = 2591999
	}
	w.Header().Set("Content-Type", "application/json")
	switch mode {
	case "bad-request":
		w.WriteHeader(400)
		io.WriteString(w, `{"error":"Error","error_description":"Client does not support the client_credentials grant type"}`)
	case "error":
		w.WriteHeader(500)
		io.WriteString(w, `{"error":"internal"}`)
	case "echo-request":
		w.WriteHeader(400)
		_, _ = w.Write(body)
	case "bad-json":
		io.WriteString(w, `{`)
	case "no-token":
		io.WriteString(w, `{"token_type":"Bearer"}`)
	case "no-expires":
		io.WriteString(w, `{"access_token":"`+fakeLinearAccess+`"}`)
	case "weird-token":
		io.WriteString(w, `{"access_token":"has space","expires_in":3599}`)
	case "short":
		io.WriteString(w, `{"access_token":"`+fakeLinearAccess+`","expires_in":3600,"token_type":"Bearer"}`)
	default:
		io.WriteString(w, `{"access_token":"`+fakeLinearAccess+`","expires_in":`+strconv.Itoa(expires)+`,"token_type":"Bearer"}`)
	}
}

func (f *fakeLinearToken) sent() []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]url.Values(nil), f.forms...)
}

func (f *fakeLinearToken) setMode(mode string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mode = mode
}

func (f *fakeLinearToken) firstType() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.types) == 0 {
		return ""
	}
	return f.types[0]
}

// linearMint runs the script with stdin in an isolated pack-test
// environment, plus the endpoint. The core also passes through the
// proxy and CA variables when it runs a mint_command.
func linearMint(t *testing.T, stdin, endpoint string, env ...string) (string, string, error) {
	t.Helper()
	cmd := exec.Command("./linear/linear-mint")
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "LINEAR_TOKEN_ENDPOINT=" + endpoint}, env...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func linearStdin(id, secret, scope string) string {
	return `{"inputs":{"LINEAR_CLIENT_ID":"` + id + `","LINEAR_CLIENT_SECRET":"` + secret + `"},"scope":"` + scope + `"}`
}

// A good exchange: the token of the endpoint's answer, asked for with
// the grant, the fixed scope set and the client credentials, and
// nothing on stderr. The grant's 30 days are kept a day: expires_at is
// about a day out, not about thirty.
func TestLinearMintGoodExchange(t *testing.T) {
	endpoint := &fakeLinearToken{t: t}
	srv := httptest.NewServer(http.HandlerFunc(endpoint.handler))
	defer srv.Close()
	before := time.Now()
	stdout, stderr, err := linearMint(t, linearStdin(fakeLinearID, fakeLinearSecret, ""), srv.URL)
	if err != nil {
		t.Fatalf("mint: %v\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q", stderr)
	}
	var o struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal([]byte(stdout), &o); err != nil || o.Token != fakeLinearAccess {
		t.Fatalf("answer = %q: %v", stdout, err)
	}
	expires, err := time.Parse(time.RFC3339, o.ExpiresAt)
	if err != nil || expires.Before(before.Add(86400*time.Second).Add(-2*time.Minute)) || expires.After(before.Add(86400*time.Second).Add(2*time.Minute)) {
		t.Fatalf("expires_at = %q, want about a day out", o.ExpiresAt)
	}
	forms := endpoint.sent()
	if len(forms) != 1 {
		t.Fatalf("exchanges: %d, want 1", len(forms))
	}
	if got := forms[0]; got.Get("grant_type") != "client_credentials" || got.Get("scope") != linearMintScope ||
		got.Get("client_id") != fakeLinearID || got.Get("client_secret") != fakeLinearSecret {
		t.Fatalf("form = %v", got)
	}
	if got := endpoint.firstType(); got != "application/x-www-form-urlencoded" {
		t.Fatalf("content type = %q", got)
	}
	for _, secret := range []string{fakeLinearID, fakeLinearSecret} {
		if strings.Contains(stdout, secret) {
			t.Errorf("the answer holds %q", secret)
		}
	}
}

// A token shorter than the cap keeps its own life: expires_at is about
// an hour out for an hour's token.
func TestLinearMintShortTTL(t *testing.T) {
	endpoint := &fakeLinearToken{t: t, mode: "short"}
	srv := httptest.NewServer(http.HandlerFunc(endpoint.handler))
	defer srv.Close()
	before := time.Now()
	stdout, stderr, err := linearMint(t, linearStdin(fakeLinearID, fakeLinearSecret, ""), srv.URL)
	if err != nil {
		t.Fatalf("mint: %v\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q", stderr)
	}
	var o struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal([]byte(stdout), &o); err != nil || o.Token != fakeLinearAccess {
		t.Fatalf("answer = %q: %v", stdout, err)
	}
	expires, err := time.Parse(time.RFC3339, o.ExpiresAt)
	if err != nil || expires.Before(before.Add(3600*time.Second).Add(-2*time.Minute)) || expires.After(before.Add(3600*time.Second).Add(2*time.Minute)) {
		t.Fatalf("expires_at = %q, want about an hour out", o.ExpiresAt)
	}
}

// The scope the core passes is read and ignored: an empty one, and one
// naming other scopes, both mint with the fixed set. A varying set
// would replace the app's credentials.
func TestLinearMintScopeIgnored(t *testing.T) {
	endpoint := &fakeLinearToken{t: t}
	srv := httptest.NewServer(http.HandlerFunc(endpoint.handler))
	defer srv.Close()
	for _, scope := range []string{"", "admin", "read,write"} {
		stdout, stderr, err := linearMint(t, linearStdin(fakeLinearID, fakeLinearSecret, scope), srv.URL)
		if err != nil {
			t.Fatalf("mint scope %q: %v\n%s", scope, err, stderr)
		}
		var o struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal([]byte(stdout), &o); err != nil || o.Token != fakeLinearAccess {
			t.Fatalf("mint scope %q: answer = %q: %v", scope, stdout, err)
		}
	}
	forms := endpoint.sent()
	if len(forms) != 3 {
		t.Fatalf("exchanges: %d, want 3", len(forms))
	}
	for i, form := range forms {
		if got := form.Get("scope"); got != linearMintScope {
			t.Fatalf("exchange %d scope = %q, want %q", i, got, linearMintScope)
		}
	}
}

func TestLinearMintRefusals(t *testing.T) {
	endpoint := &fakeLinearToken{t: t}
	srv := httptest.NewServer(http.HandlerFunc(endpoint.handler))
	defer srv.Close()
	for name, tc := range map[string]struct {
		stdin string
		mode  string
		want  string
	}{
		"no client id":         {`{"inputs":{"LINEAR_CLIENT_SECRET":"s"},"scope":""}`, "good", "its inputs name no LINEAR_CLIENT_ID"},
		"no client secret":     {`{"inputs":{"LINEAR_CLIENT_ID":"id"},"scope":""}`, "good", "its inputs name no LINEAR_CLIENT_SECRET"},
		"no scope":             {`{"inputs":{"LINEAR_CLIENT_ID":"id","LINEAR_CLIENT_SECRET":"s"}}`, "good", "got no scope"},
		"no JSON":              {`{`, "good", "its inputs name no LINEAR_CLIENT_ID"},
		"nothing":              {``, "good", "got nothing"},
		"token refused":        {linearStdin(fakeLinearID, fakeLinearSecret, ""), "bad-request", "minting a token was answered 400"},
		"token echoes request": {linearStdin(fakeLinearID, fakeLinearSecret, ""), "echo-request", "minting a token was answered 400"},
		"token failing":        {linearStdin(fakeLinearID, fakeLinearSecret, ""), "error", "minting a token was answered 500"},
		"token bad JSON":       {linearStdin(fakeLinearID, fakeLinearSecret, ""), "bad-json", "minting a token came back with no token"},
		"token no token":       {linearStdin(fakeLinearID, fakeLinearSecret, ""), "no-token", "minting a token came back with no token"},
		"token no expires":     {linearStdin(fakeLinearID, fakeLinearSecret, ""), "no-expires", "minting a token came back with no token"},
		"token weird":          {linearStdin(fakeLinearID, fakeLinearSecret, ""), "weird-token", "the token Linear answered is no token"},
	} {
		endpoint.setMode(tc.mode)
		stdout, stderr, err := linearMint(t, tc.stdin, srv.URL)
		if err == nil || !strings.Contains(stderr, tc.want) {
			t.Errorf("%s: %v\n%s", name, err, stderr)
		}
		if !strings.HasPrefix(stderr, "linear-mint: ") {
			t.Errorf("%s names no command: %q", name, stderr)
		}
		if stdout != "" {
			t.Errorf("%s prints %q", name, stdout)
		}
		for _, secret := range []string{fakeLinearID, fakeLinearSecret, fakeLinearAccess} {
			if strings.Contains(stderr, secret) || strings.Contains(stdout, secret) {
				t.Errorf("%s shows %q", name, secret)
			}
		}
		// A response body is untrusted and could echo the form's
		// secrets; only the status code is reported.
		if (name == "token refused" || name == "token echoes request") && strings.Contains(stderr, "client_credentials") {
			t.Errorf("a refused exchange repeated the response body: %q", stderr)
		}
	}
}

func TestLinearMintRejectsRemoteTokenEndpoint(t *testing.T) {
	for _, endpoint := range []string{
		"https://attacker.example/token",
		"http://attacker.example/token",
		"http://127.0.0.1:/token",
		"http://127.0.0.1:abc/token",
		"http://localhost:8080/token",
	} {
		stdout, stderr, err := linearMint(t, linearStdin(fakeLinearID, fakeLinearSecret, ""), endpoint)
		if err == nil || !strings.Contains(stderr, "LINEAR_TOKEN_ENDPOINT is only allowed for a loopback test endpoint") {
			t.Errorf("%s: err = %v\n%s", endpoint, err, stderr)
		}
		if stdout != "" {
			t.Errorf("%s: stdout = %q", endpoint, stdout)
		}
		for _, secret := range []string{fakeLinearID, fakeLinearSecret, fakeLinearAccess} {
			if strings.Contains(stderr, secret) {
				t.Errorf("%s: stderr shows %q", endpoint, secret)
			}
		}
	}
}

// With no endpoint the exchange fails as unreachable, naming curl's
// exit, and still prints no secret.
func TestLinearMintUnreachable(t *testing.T) {
	stdout, stderr, err := linearMint(t, linearStdin(fakeLinearID, fakeLinearSecret, ""), "http://127.0.0.1:1")
	if err == nil || !strings.Contains(stderr, "could not reach http://127.0.0.1:1 (curl exit 7)") {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q", stdout)
	}
	for _, secret := range []string{fakeLinearID, fakeLinearSecret, fakeLinearAccess} {
		if strings.Contains(stderr, secret) {
			t.Fatalf("stderr shows %q", secret)
		}
	}
}
