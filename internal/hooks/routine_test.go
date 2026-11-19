package hooks_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/hooks"
)

// A routine is told as {title, text, items} at the URL its env names,
// once per call, and the URL stands in no error: not in one the routine
// did not answer, and not in one that is no address.
func TestRoutineNotify(t *testing.T) {
	var got map[string]any
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Errorf("the body is no JSON: %s", raw)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		w.WriteHeader(201)
	}))
	t.Cleanup(srv.Close)
	const secret = "s3cret-path-no-error-may-hold"
	url := srv.URL + "/hook/" + secret
	r := hooks.Routine{}
	if env := r.NotifyEnv(); len(env) != 1 || env[0] != "ROUTINE_URL" {
		t.Fatalf("NotifyEnv = %v", env)
	}
	status, err := r.Notify(context.Background(), map[string]string{"ROUTINE_URL": url},
		"mail from ada", "the tests are green", "https://x.example.com/ignored",
		[]hooks.Item{{Kind: "mail", Seq: 7}, {Kind: "ask", Bead: "yardr-n8ta", Ask: 9}})
	if err != nil || status != 201 || calls != 1 {
		t.Fatalf("Notify = %d, %v (%d calls)", status, err, calls)
	}
	if got["title"] != "mail from ada" || got["text"] != "the tests are green" {
		t.Fatalf("the routine got %v", got)
	}
	items, _ := got["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["kind"] != "mail" || items[0].(map[string]any)["seq"] != float64(7) ||
		items[1].(map[string]any)["bead"] != "yardr-n8ta" || items[1].(map[string]any)["ask"] != float64(9) {
		t.Fatalf("the routine got items %v", got["items"])
	}
	// No items yet: still an array, never null.
	if _, err := r.Notify(context.Background(), map[string]string{"ROUTINE_URL": url}, "t", "x", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["items"].([]any); !ok {
		t.Fatalf("a message about nothing carries items %v", got["items"])
	}

	// Down, refused as an address, and missing: an error each, none with
	// the URL in it.
	down := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	downURL := down.URL + "/hook/" + secret
	down.Close()
	for _, env := range []map[string]string{
		{"ROUTINE_URL": downURL},
		{"ROUTINE_URL": "://no address/" + secret},
		{},
	} {
		if _, err := r.Notify(context.Background(), env, "t", "x", "", nil); err == nil || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), downURL) {
			t.Fatalf("env %v: err = %v", env, err)
		}
	}
	// An answer that is no success is a status, not an error.
	s500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	t.Cleanup(s500.Close)
	if status, err := r.Notify(context.Background(), map[string]string{"ROUTINE_URL": s500.URL + "/hook/" + secret}, "t", "x", "", nil); err != nil || status != 500 {
		t.Fatalf("a 500 = %d, %v", status, err)
	}
	var redirected int
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected++ }))
	t.Cleanup(target.Close)
	jump := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", target.URL+"/hook/elsewhere")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	t.Cleanup(jump.Close)
	if status, err := r.Notify(context.Background(), map[string]string{"ROUTINE_URL": jump.URL + "/hook/" + secret}, "t", "x", "", nil); err != nil || status != http.StatusTemporaryRedirect || redirected != 0 {
		t.Fatalf("a redirect = %d, %v; target received %d requests", status, err, redirected)
	}
	// It takes no deliveries.
	if _, err := r.Verify(nil, nil, nil, time.Time{}); err == nil {
		t.Fatal("Verify took a delivery")
	}
	if _, err := r.Normalize(nil, []byte(`{}`)); err == nil {
		t.Fatal("Normalize took a delivery")
	}
	if _, ok := hooks.NotifierFor("routine"); !ok {
		t.Fatal("routine is no Notifier")
	}
	if _, ok := any(hooks.Routine{}).(hooks.Imager); ok {
		t.Fatal("routine takes a picture: it takes none")
	}
}
