package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/config"
	"yardr.dev/yardr/internal/standin"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
	"yardr.dev/yardr/internal/yardkey"
)

// The server's hosted minter makes the one signed call through the
// edge by the key the yard pinned, checks the granted scopes before
// the token is held, and hands back the token and its expiry alone: an
// ungranted source is refused with its token in no error, and nothing
// is fetched again for it.
func TestHostedMintFor(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	st := storetest.NewIn(t, home)
	if err := st.SetSetting(ctx, store.OperatorSetting, "jo", "jo"); err != nil {
		t.Fatal(err)
	}
	edge, err := standin.NewEdge()
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(edge)
	t.Cleanup(srv.Close)
	granted := []string{"openid", "email", "https://www.googleapis.com/auth/drive.file",
		"https://www.googleapis.com/auth/documents", "https://www.googleapis.com/auth/presentations",
		"https://www.googleapis.com/auth/calendar.events", "https://www.googleapis.com/auth/calendar.calendarlist.readonly"}
	edge.Behave(func(b *standin.EdgeBehaviour) { b.MintToken, b.MintGranted = "ya29.hosted", granted })
	if _, _, err := yardkey.Ensure(config.At(home).PeerKey()); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, store.EdgeSetting, store.Edge{URL: srv.URL, Key: edge.PublicKey(), Handle: "mckean", Name: "mckean"}.String(), "jo"); err != nil {
		t.Fatal(err)
	}
	// The source declares the scopes its hosted mint must see granted.
	drive := "name: google-drive\nbase: https://google.example.com\ncredential:\n  env: GOOGLE_TOKEN\n  mint:\n    - name: GOOGLE_ACCOUNT\n      secret: false\n  mint_scopes: [https://www.googleapis.com/auth/drive.file]\ncalls:\n  files: { kind: read, request: { method: GET, path: /files } }\n"
	if _, err := st.PutSource(ctx, "google-drive.yaml", drive, "jo"); err != nil {
		t.Fatal(err)
	}
	mint := hostedMintFor(st, config.At(home))
	token, expires, err := mint(ctx, srv.URL, "google-drive", "ada@example.com")
	if err != nil || token != "ya29.hosted" || time.Until(expires) < 59*time.Minute {
		t.Fatalf("mint = %q, %v, %v", token, expires, err)
	}
	if s := edge.State(); len(s.Mints) != 1 || s.Mints[0].Account != "ada@example.com" || s.KeyFetches != 0 {
		t.Errorf("the edge took %+v, want the one mint by the pinned key", s)
	}
	if _, _, err := mint(ctx, srv.URL, "unlisted-source", "ada@example.com"); err == nil ||
		!strings.Contains(err.Error(), "declares no mint_scopes") ||
		strings.Contains(err.Error(), "yardr pack apply") {
		t.Fatalf("an unknown hosted source = %v", err)
	}
	if s := edge.State(); len(s.Mints) != 1 {
		t.Errorf("an unknown source reached the edge: %+v", s)
	}
	edge.Behave(func(b *standin.EdgeBehaviour) {
		b.MintGranted = []string{"openid", "email", "https://www.googleapis.com/auth/documents"}
	})
	if _, _, err := mint(ctx, srv.URL, "google-drive", "ada@example.com"); err == nil ||
		!strings.Contains(err.Error(), "google-drive needs https://www.googleapis.com/auth/drive.file") ||
		!strings.Contains(err.Error(), "missing from the Google connection's grant") ||
		strings.Contains(err.Error(), "ya29.hosted") {
		t.Fatalf("an ungranted mint = %v", err)
	}
	// A copy stored before its pack declared the scopes holds none, whatever
	// the edge would grant: it is refused without a call to the edge, with
	// the pack to apply again (the docs pack for slides), and with no pack
	// for the source whose pack declares no scope (mail, which mints
	// locally), since applying that one cures nothing.
	mints := len(edge.State().Mints)
	for _, c := range []struct{ source, cure string }{
		{"google-drive", "; re-apply its pack (`yardr pack apply google-drive`)"},
		{"google-slides", "; re-apply its pack (`yardr pack apply google-docs`)"},
		{"google-mail", ""},
	} {
		stored := "name: " + c.source + "\nbase: https://google.example.com\ncredential:\n  env: GOOGLE_TOKEN\n  mint:\n    - name: GOOGLE_ACCOUNT\n      secret: false\ncalls:\n  files: { kind: read, request: { method: GET, path: /files } }\n"
		if _, err := st.PutSource(ctx, c.source+".yaml", stored, "jo"); err != nil {
			t.Fatal(err)
		}
		want := "source " + c.source + " declares no mint_scopes, so the hosted mint does not serve it" + c.cure
		if _, _, err := mint(ctx, srv.URL, c.source, "ada@example.com"); err == nil || err.Error() != want {
			t.Errorf("%s stored without scopes = %v, want %q", c.source, err, want)
		}
	}
	if got := len(edge.State().Mints); got != mints {
		t.Errorf("a source with no scopes reached the edge: %d mints, want %d", got, mints)
	}
}
