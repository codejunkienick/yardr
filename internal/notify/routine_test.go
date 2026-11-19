package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"yardr.dev/yardr/internal/hooks"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
)

// Through the routine, an operator's URL on this machine is told at, and
// a member's only where it is a public https name: a literal, a name
// without a dot, plain http and this machine are refused before anything
// is sent, with the URL in no refusal. The items ride beside the words.
func TestThroughRoutineGuard(t *testing.T) {
	var calls atomic.Int64
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)
	ctx := context.Background()
	st := storetest.New(t)
	for _, name := range []string{"op", "ada"} {
		if _, err := st.AddUser(ctx, name, "op"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.SetUser(ctx, "op", map[string]string{store.UserRole: store.RoleOperator}, "op"); err != nil {
		t.Fatal(err)
	}
	op, _ := st.GetUser(ctx, "op")
	ada, _ := st.GetUser(ctx, "ada")
	const secret = "s3cret-path-no-refusal-may-hold"
	loop := srv.URL + "/hook/" + secret
	mk := func(u *store.User, url string) *store.User {
		c := *u
		c.EnvFile = envFile(t, "ROUTINE_URL="+url+"\n", 0o600)
		return &c
	}
	msg := Message{Title: "mail from jo", Text: "hello", Items: []hooks.Item{{Kind: "mail", Seq: 3}}}
	if sent, err := Through(ctx, st, "", "routine", mk(op, loop), msg); err != nil || !sent.OK() || calls.Load() != 1 {
		t.Fatalf("an operator at a stand-in = %+v, %v (%d calls)", sent, err, calls.Load())
	}
	if got["title"] != "mail from jo" || got["text"] != "hello" {
		t.Fatalf("the stand-in got %v", got)
	}
	for _, url := range []string{
		loop, // plain http to this machine, and a literal
		"https://127.0.0.1/hook/" + secret,
		"https://10.0.0.9/hook/" + secret,
		"https://routine/hook/" + secret, // no dot
		"https://localhost/hook/" + secret,
		"http://routine.example.com/hook/" + secret, // not https
		// The resolver's own forms of this machine, which no DNS answers:
		// a short or hex literal, a trailing dot, a .localhost name.
		"https://127.1/hook/" + secret,
		"https://0x7f.1/hook/" + secret,
		"https://localhost./hook/" + secret,
		"https://routine.localhost/hook/" + secret,
		"https://10.0.0.9./hook/" + secret,
	} {
		if _, err := Through(ctx, st, "", "routine", mk(ada, url), msg); err == nil {
			t.Fatalf("a member at %q was told", url)
		} else {
			var not *NotSetUp
			if !errors.As(err, &not) || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), url) {
				t.Fatalf("a member at %q: err = %v", url, err)
			}
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("a refused member URL was sent to: %d calls", calls.Load())
	}
	// An operator is held as every address is: a query is refused too.
	if _, err := Through(ctx, st, "", "routine", mk(op, loop+"?x=1"), msg); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("an operator with a query: %v", err)
	}
}
