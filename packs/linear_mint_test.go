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
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"yardr.dev/yardr/internal/scripttest"
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
// comments:create for reply, and app:mentionable and app:assignable, which
// keep the app's user offered in the mention and assignee menus.
const linearMintScope = "read,issues:create,comments:create,app:mentionable,app:assignable"

// fakeLinearToken is a fake https://api.linear.app/oauth/token: it
// answers mode (good, bad-request, unauthorized, bad-gateway, error,
// error-value, bad-json, no-token, no-expires, weird-token) with expiresIn seconds
// (2591999, the grant's own, where unset). A good answer holds the scope
// field scope, JSON as it is written (a string, an array, null), and none
// where it is unset. It keeps the forms it was sent.
type fakeLinearToken struct {
	t         *testing.T
	mu        sync.Mutex
	mode      string
	expiresIn int
	scope     string
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
	mode, expires, scope := f.mode, f.expiresIn, f.scope
	f.mu.Unlock()
	if expires == 0 {
		expires = 2591999
	}
	if scope != "" {
		scope = `,"scope":` + scope
	}
	w.Header().Set("Content-Type", "application/json")
	switch mode {
	case "bad-request":
		w.WriteHeader(400)
		io.WriteString(w, `{"error":"Error","error_description":"Client does not support the client_credentials grant type"}`)
	case "unauthorized":
		w.WriteHeader(401)
		io.WriteString(w, `{"error":"invalid_client","error_description":"Client authentication failed"}`)
	case "bad-gateway":
		w.WriteHeader(502)
		io.WriteString(w, `<html><body>Bad Gateway</body></html>`)
	case "error-value":
		w.WriteHeader(500)
		io.WriteString(w, `{"status":"error"}`)
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
		io.WriteString(w, `{"access_token":"`+fakeLinearAccess+`","expires_in":`+strconv.Itoa(expires)+`,"token_type":"Bearer"`+scope+`}`)
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

func (f *fakeLinearToken) setScope(scope string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scope = scope
}

func (f *fakeLinearToken) firstType() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.types) == 0 {
		return ""
	}
	return f.types[0]
}

// linearMintCopy is the script copied into a directory of its own, as the
// core's copy stands under <home>/sources/linear/: the script writes
// grants.log beside itself, so no test runs the repository's file where
// it is. The copy is the one launcher's link with the script beside it
// (scripttest), since a copy that is a file of its own takes hundreds of
// milliseconds to start on macOS, and a mint is run many times here.
func linearMintCopy(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile("linear/linear-mint")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	scripttest.Write(t, filepath.Join(dir, "linear-mint"), string(body))
	return dir
}

// linearMint runs a new copy of the script with stdin in an isolated
// pack-test environment, plus the endpoint. The core also passes through
// the proxy and CA variables when it runs a mint_command.
func linearMint(t *testing.T, stdin, endpoint string, env ...string) (string, string, error) {
	t.Helper()
	return linearMintIn(t, linearMintCopy(t), stdin, endpoint, env...)
}

// linearMintIn is linearMint with the copy in dir, for a test that mints
// more than once beside one record or reads it. The copy starts under
// umask 0, so that a record found 0600 is the script's own doing and not
// its caller's.
func linearMintIn(t *testing.T, dir, stdin, endpoint string, env ...string) (string, string, error) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", `umask 0; exec "$0"`, filepath.Join(dir, "linear-mint"))
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "LINEAR_TOKEN_ENDPOINT=" + endpoint}, env...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// linearRecords are the lines of the record beside the copy in dir, each
// less its UTC stamp, which is held to be about now: no file is no lines.
// The text is held to carry no secret of the mint, whatever the answer was.
func linearRecords(t *testing.T, dir string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "grants.log"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{fakeLinearID, fakeLinearSecret, fakeLinearAccess} {
		if strings.Contains(string(b), secret) {
			t.Errorf("the record holds %q: %q", secret, b)
		}
	}
	if !strings.HasSuffix(string(b), "\n") {
		t.Fatalf("the record ends in a line left open: %q", b)
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		stamp, rest, ok := strings.Cut(line, " ")
		at, err := time.Parse("2006-01-02T15:04:05Z", stamp)
		if !ok || err != nil || time.Since(at).Abs() > 2*time.Minute {
			t.Fatalf("record line %q does not begin with the UTC time of now", line)
		}
		lines = append(lines, rest)
	}
	return lines
}

func linearStdin(id, secret, scope string) string {
	return `{"inputs":{"LINEAR_CLIENT_ID":"` + id + `","LINEAR_CLIENT_SECRET":"` + secret + `"},"scope":"` + scope + `"}`
}

// A good exchange: the token of the endpoint's answer, asked for with
// the grant, the fixed scope set and the client credentials, and
// nothing on stderr. The grant's 30 days are kept a day: expires_at is
// about a day out, not about thirty. The record beside the script is
// one line, in a file only its owner reads and writes, and a record
// that cannot be written fails nothing.
func TestLinearMintGoodExchange(t *testing.T) {
	endpoint := &fakeLinearToken{t: t}
	srv := httptest.NewServer(http.HandlerFunc(endpoint.handler))
	defer srv.Close()
	dir := linearMintCopy(t)
	before := time.Now()
	stdout, stderr, err := linearMintIn(t, dir, linearStdin(fakeLinearID, fakeLinearSecret, ""), srv.URL)
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
	// This fake answers with no scope field, so the record says so.
	if got := linearRecords(t, dir); len(got) != 1 || got[0] != "status 200, error field absent, scope: absent" {
		t.Fatalf("record = %q", got)
	}
	info, err := os.Stat(filepath.Join(dir, "grants.log"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("grants.log is mode %v, want -rw------- (the test started the script under umask 0)", info.Mode().Perm())
	}
	// A record that cannot be written (a directory stands where the file
	// would) is no failed mint: the token is the product, the record a
	// diagnostic.
	if err := os.Remove(filepath.Join(dir, "grants.log")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "grants.log"), 0o700); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err = linearMintIn(t, dir, linearStdin(fakeLinearID, fakeLinearSecret, ""), srv.URL)
	if err != nil || stderr != "" || !strings.Contains(stdout, fakeLinearAccess) {
		t.Fatalf("mint beside a record that cannot be written: %v\n%s\n%s", err, stdout, stderr)
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
// would replace the app's credentials. The three mints share a copy, as
// the server's and the daemon's do, and each appends its own line to its
// record.
func TestLinearMintScopeIgnored(t *testing.T) {
	endpoint := &fakeLinearToken{t: t}
	srv := httptest.NewServer(http.HandlerFunc(endpoint.handler))
	defer srv.Close()
	dir := linearMintCopy(t)
	for _, scope := range []string{"", "admin", "read,write"} {
		stdout, stderr, err := linearMintIn(t, dir, linearStdin(fakeLinearID, fakeLinearSecret, scope), srv.URL)
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
	if got := linearRecords(t, dir); len(got) != 3 {
		t.Fatalf("record after three mints = %q, want three lines", got)
	}
}

// The scope Linear says it granted is written as it is printed, and
// whether it names each agent scope is read from whole names. A string
// (spaced or with commas) and an array are all read, one over several
// lines too; a name that only holds an agent scope in it does not count; a
// value with a character no scope list has is counted and never shown, the
// secret or token echoed as a scope among them; and no answer holds nothing
// to read. Every line is one line, and holds no secret.
func TestLinearMintRecordsGrant(t *testing.T) {
	endpoint := &fakeLinearToken{t: t}
	srv := httptest.NewServer(http.HandlerFunc(endpoint.handler))
	defer srv.Close()
	const lead = "status 200, error field absent, "
	for name, tc := range map[string]struct{ scope, want string }{
		"string, spaced": {`"read issues:create comments:create app:mentionable app:assignable"`,
			lead + `app:mentionable yes, app:assignable yes, scope: "read issues:create comments:create app:mentionable app:assignable"`},
		"string, commas": {`"` + linearMintScope + `"`,
			lead + `app:mentionable yes, app:assignable yes, scope: "` + linearMintScope + `"`},
		"array": {`["read","issues:create","comments:create","app:mentionable","app:assignable"]`,
			lead + `app:mentionable yes, app:assignable yes, scope: ["read","issues:create","comments:create","app:mentionable","app:assignable"]`},
		"array, spaced": {`[ "read", "app:assignable" ]`,
			lead + `app:mentionable no, app:assignable yes, scope: [ "read", "app:assignable" ]`},
		"array over lines": {"[\n  \"read\",\n  \"app:mentionable\"\n]",
			lead + `app:mentionable yes, app:assignable no, scope: [  "read",  "app:mentionable"]`},
		"brackets, no quotes": {`[app:mentionable,app:assignable]`,
			lead + `app:mentionable yes, app:assignable yes, scope: [app:mentionable,app:assignable]`},
		"mentionable only": {`"read app:mentionable"`,
			lead + `app:mentionable yes, app:assignable no, scope: "read app:mentionable"`},
		"neither": {`"read"`,
			lead + `app:mentionable no, app:assignable no, scope: "read"`},
		"no scopes": {`""`,
			lead + `app:mentionable no, app:assignable no, scope: ""`},
		"longer names": {`["xapp:mentionable","app:assignable_x","app:mentionable:extra"]`,
			lead + `app:mentionable no, app:assignable no, scope: ["xapp:mentionable","app:assignable_x","app:mentionable:extra"]`},
		"outside the alphabet": {`"read;app:mentionable"`,
			lead + `scope: unparseable (22 chars)`},
		"escaped": {`"read` + `\` + `u0020app:mentionable"`,
			lead + `scope: unparseable (27 chars)`},
		"the secret": {strconv.Quote(fakeLinearSecret),
			lead + `scope: unparseable (` + strconv.Itoa(len(fakeLinearSecret)+2) + ` chars)`},
		"the token": {strconv.Quote(fakeLinearAccess),
			lead + `scope: unparseable (` + strconv.Itoa(len(fakeLinearAccess)+2) + ` chars)`},
		"null":  {`null`, lead + `scope: absent`},
		"unset": {``, lead + `scope: absent`},
	} {
		endpoint.setScope(tc.scope)
		dir := linearMintCopy(t)
		stdout, stderr, err := linearMintIn(t, dir, linearStdin(fakeLinearID, fakeLinearSecret, ""), srv.URL)
		if err != nil || stderr != "" || !strings.Contains(stdout, fakeLinearAccess) {
			t.Errorf("%s: mint: %v\n%s\n%s", name, err, stdout, stderr)
			continue
		}
		if got := linearRecords(t, dir); len(got) != 1 || got[0] != tc.want {
			t.Errorf("%s: record = %q, want %q", name, got, tc.want)
		}
	}

	// A secret made only of characters allowed in scope names must still be
	// withheld from the record, just like the token above.
	const alphabeticSecret = "allAlphabeticSecret123"
	endpoint.setScope(strconv.Quote(alphabeticSecret))
	dir := linearMintCopy(t)
	stdout, stderr, err := linearMintIn(t, dir, linearStdin(fakeLinearID, alphabeticSecret, ""), srv.URL)
	if err != nil || stderr != "" || !strings.Contains(stdout, fakeLinearAccess) {
		t.Fatalf("mint with a secret-shaped scope: %v\n%s\n%s", err, stdout, stderr)
	}
	want := lead + "scope: unparseable (" + strconv.Itoa(len(alphabeticSecret)+2) + " chars)"
	if got := linearRecords(t, dir); len(got) != 1 || got[0] != want {
		t.Fatalf("record for an alphabetic secret = %q, want %q", got, want)
	}
}

// Every refusal fails with a word of its own and prints nothing. Where
// Linear was asked, its answer is a line of the record, status and error
// field alone, whatever the body held; where it was not, there is none.
// A 401 is told apart, as a refusal of the credentials and not of the set.
func TestLinearMintRefusals(t *testing.T) {
	endpoint := &fakeLinearToken{t: t}
	srv := httptest.NewServer(http.HandlerFunc(endpoint.handler))
	defer srv.Close()
	ok := linearStdin(fakeLinearID, fakeLinearSecret, "")
	const answered200 = "status 200, error field absent, scope: absent"
	for name, tc := range map[string]struct {
		stdin  string
		mode   string
		want   string
		record string
	}{
		"no client id":         {`{"inputs":{"LINEAR_CLIENT_SECRET":"s"},"scope":""}`, "good", "its inputs name no LINEAR_CLIENT_ID", ""},
		"no client secret":     {`{"inputs":{"LINEAR_CLIENT_ID":"id"},"scope":""}`, "good", "its inputs name no LINEAR_CLIENT_SECRET", ""},
		"no scope":             {`{"inputs":{"LINEAR_CLIENT_ID":"id","LINEAR_CLIENT_SECRET":"s"}}`, "good", "got no scope", ""},
		"no JSON":              {`{`, "good", "its inputs name no LINEAR_CLIENT_ID", ""},
		"nothing":              {``, "good", "got nothing", ""},
		"token refused":        {ok, "bad-request", "minting a token was answered 400", "status 400, error field present"},
		"token unauthorized":   {ok, "unauthorized", "credentials refused: minting a token was answered 401", "status 401, error field present"},
		"token echoes request": {ok, "echo-request", "minting a token was answered 400", "status 400, error field absent"},
		"token failing":        {ok, "error", "minting a token was answered 500", "status 500, error field present"},
		"token bad gateway":    {ok, "bad-gateway", "minting a token was answered 502", "status 502, error field absent"},
		"token error as value": {ok, "error-value", "minting a token was answered 500", "status 500, error field absent"},
		"token bad JSON":       {ok, "bad-json", "minting a token came back with no token", answered200},
		"token no token":       {ok, "no-token", "minting a token came back with no token", answered200},
		"token no expires":     {ok, "no-expires", "minting a token came back with no token", answered200},
		"token weird":          {ok, "weird-token", "the token Linear answered is no token", answered200},
	} {
		endpoint.setMode(tc.mode)
		dir := linearMintCopy(t)
		stdout, stderr, err := linearMintIn(t, dir, tc.stdin, srv.URL)
		if err == nil || !strings.Contains(stderr, tc.want) {
			t.Errorf("%s: %v\n%s", name, err, stderr)
		}
		var record []string
		if tc.record != "" {
			record = []string{tc.record}
		}
		if got := linearRecords(t, dir); !slices.Equal(got, record) {
			t.Errorf("%s: record = %q, want %q", name, got, record)
		}
		if name != "token unauthorized" && strings.Contains(stderr, "credentials refused") {
			t.Errorf("%s calls the credentials refused: %q", name, stderr)
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
// exit, and still prints no secret. Linear gave no status, so there is
// nothing of its to record.
func TestLinearMintUnreachable(t *testing.T) {
	dir := linearMintCopy(t)
	stdout, stderr, err := linearMintIn(t, dir, linearStdin(fakeLinearID, fakeLinearSecret, ""), "http://127.0.0.1:1")
	if err == nil || !strings.Contains(stderr, "could not reach http://127.0.0.1:1 (curl exit 7)") {
		t.Fatalf("err = %v\n%s", err, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q", stdout)
	}
	if got := linearRecords(t, dir); got != nil {
		t.Fatalf("record = %q, want none", got)
	}
	for _, secret := range []string{fakeLinearID, fakeLinearSecret, fakeLinearAccess} {
		if strings.Contains(stderr, secret) {
			t.Fatalf("stderr shows %q", secret)
		}
	}
}
