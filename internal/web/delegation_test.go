package web_test

import (
	"context"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/store"
)

// The Config hub shows an operator the yardmaster's delegation switch, and
// its form turns it off on the operator check alone and back on only under
// a fresh sign-in: a request to turn it on without one starts the sign-in
// and writes nothing. A member is refused both ways, and nothing but the
// switch changes.
func TestWebDelegationSwitch(t *testing.T) {
	t.Parallel()
	y := newApproveYard(t, true)
	ctx := context.Background()
	jo, sam := y.token("jo@example.net"), y.token("sam@example.net")
	setting := func() string {
		t.Helper()
		s, err := y.st.Settings(ctx)
		must(t, err)
		return s[store.DelegationSetting]
	}
	others := func() map[string]string {
		t.Helper()
		s, err := y.st.Settings(ctx)
		must(t, err)
		delete(s, store.DelegationSetting)
		return s
	}
	before := others()

	// Unset on a hosted yard, it reads on.
	if r := y.getNoFollow("/config", jo); r.status != http.StatusOK || !strings.Contains(r.body, "Yardmaster for the operator") || !strings.Contains(r.body, `value="off"`) {
		t.Fatalf("GET /config as jo: %d:\n%s", r.status, r.body)
	}
	if r := y.getNoFollow("/config", sam); strings.Contains(r.body, "Yardmaster for the operator") {
		t.Errorf("GET /config as sam shows the switch:\n%s", r.body)
	}
	for _, action := range []string{"off", "on"} {
		if r := y.post("/config/delegation", sam, url.Values{"action": {action}}); r.status != http.StatusOK || !strings.Contains(r.body, "the operator&#39;s to switch") {
			t.Errorf("sam turns it %s: %d:\n%s", action, r.status, r.body)
		}
	}
	if got := setting(); got != "" {
		t.Fatalf("a member's posts set the switch to %q", got)
	}

	// Off by the operator, with no witness.
	if r := y.post("/config/delegation", jo, url.Values{"action": {"off"}}); r.status != http.StatusSeeOther || r.header.Get("Location") != "/config" {
		t.Fatalf("jo turns it off: %d %q: %s", r.status, r.header.Get("Location"), r.body)
	}
	if got := setting(); got != "false" {
		t.Fatalf("after off the switch is %q", got)
	}

	// On without a fresh sign-in starts one and writes nothing.
	r := y.post("/config/delegation", jo, url.Values{"action": {"on"}})
	loc, err := url.Parse(r.header.Get("Location"))
	if r.status != http.StatusSeeOther || err != nil || loc.Path != "/edge/reauth" {
		t.Fatalf("jo turns it on: %d %q, want the gateway re-authentication", r.status, r.header.Get("Location"))
	}
	back := loc.Query().Get("return_to")
	if !strings.HasPrefix(back, "/config/delegation/fresh/") {
		t.Fatalf("the return is %q, want one-use state bound to the toggle", back)
	}
	if got := setting(); got != "false" {
		t.Fatalf("the challenge set the switch to %q", got)
	}
	// A return under a standing login is not fresh, and writes nothing.
	if r := y.getNoFollow(back, jo); r.status != http.StatusOK || !strings.Contains(r.body, "not fresh") || setting() != "false" {
		t.Fatalf("the return with no fresh sign-in: %d:\n%s", r.status, r.body)
	}
	// A callback challenged while hosted must not turn it on after the
	// operator has moved the yard off-hosted.
	must(t, y.st.SetSetting(ctx, store.HostedSetting, "false", "jo"))
	fresh := y.token("jo@example.net", authAt(time.Now().Add(5*time.Second)))
	if r := y.getNoFollow(back, fresh); !strings.Contains(r.body, "at a terminal") || setting() != "false" {
		t.Fatalf("the hosted callback after the yard moved off-hosted: %d:\n%s", r.status, r.body)
	}
	must(t, y.st.SetSetting(ctx, store.HostedSetting, "true", "jo"))
	r = y.post("/config/delegation", jo, url.Values{"action": {"on"}})
	loc, err = url.Parse(r.header.Get("Location"))
	if r.status != http.StatusSeeOther || err != nil || loc.Path != "/edge/reauth" {
		t.Fatalf("the second challenge: %d %q", r.status, r.header.Get("Location"))
	}
	back = loc.Query().Get("return_to")
	// With the witness it writes true, once.
	fresh = y.token("jo@example.net", authAt(time.Now().Add(5*time.Second)))
	if r := y.getNoFollow(back, fresh); r.status != http.StatusSeeOther || r.header.Get("Location") != "/config" {
		t.Fatalf("the fresh return: %d %q: %s", r.status, r.header.Get("Location"), r.body)
	}
	if got := setting(); got != "true" {
		t.Fatalf("after the fresh return the switch is %q", got)
	}
	if r := y.getNoFollow(back, fresh); !strings.Contains(r.body, "used already") {
		t.Errorf("replaying the return: %d:\n%s", r.status, r.body)
	}
	if after := others(); !maps.Equal(before, after) {
		t.Errorf("the toggle changed other settings: %v -> %v", before, after)
	}

	// Off a hosted yard, turning it on stays a terminal's.
	z := newApproveYard(t, false)
	must(t, z.st.SetSetting(ctx, store.DelegationSetting, "false", "jo"))
	if r := z.post("/config/delegation", z.token("jo@example.net"), url.Values{"action": {"on"}}); r.status != http.StatusOK || !strings.Contains(r.body, "at a terminal") {
		t.Errorf("turning it on off a hosted yard: %d:\n%s", r.status, r.body)
	}
}

// A delegated record's page is an operator's alone; a member, and a
// record that is not there, are answered not found.
func TestWebDelegationRecord(t *testing.T) {
	t.Parallel()
	y := newApproveYard(t, true)
	ctx := context.Background()
	must(t, y.st.PutGroup(ctx, store.Group{Name: "builders", Runner: "herdr", Config: map[string]any{"kind": "claude"}}, "jo"))
	must(t, y.st.Mail(ctx, "yardmaster", "put the builders on codex", "jo"))
	mail, err := y.st.Head(ctx)
	must(t, err)
	dctx := store.WithDelegation(ctx, store.DelegatedCall{For: "jo", Mail: mail, Command: "yardr group set builders kind=codex"})
	_, err = y.st.SetGroup(dctx, "builders", store.GroupEdit{Set: map[string]any{"kind": "codex"}}, "yardmaster")
	must(t, err)

	r := y.getNoFollow("/delegations/1", y.token("jo@example.net"))
	for _, want := range []string{"Record 1: yardmaster for jo", "yardr group set builders kind=codex", "claude → codex", "yardr group set builders &#39;kind=claude&#39;"} {
		if r.status != http.StatusOK || !strings.Contains(r.body, want) {
			t.Errorf("jo's record page lacks %q: %d:\n%s", want, r.status, r.body)
		}
	}
	for _, target := range []string{"/delegations/1", "/delegations/2", "/delegations/x"} {
		token := y.token("sam@example.net")
		if target != "/delegations/1" {
			token = y.token("jo@example.net")
		}
		if r := y.getNoFollow(target, token); r.status != http.StatusNotFound || strings.Contains(r.body, "codex") {
			t.Errorf("GET %s: %d:\n%s", target, r.status, r.body)
		}
	}
}
