package edgelink

import "testing"

// The scope check every hosted mint makes holds the granted scopes to what
// the source's declaration needs, in words the setup's probe and the
// in-process mint share. A source that declares none is refused whatever was
// granted: with the pack to apply again where the caller names one, and in
// the same sentence without it. The pack is read for that refusal alone.
func TestCheckGoogleScopes(t *testing.T) {
	const (
		edge   = "https://edge.example.net"
		drive  = "https://www.googleapis.com/auth/drive.file"
		docs   = "https://www.googleapis.com/auth/documents"
		events = "https://www.googleapis.com/auth/calendar.events"
	)
	for _, c := range []struct {
		name, source, pack string
		needed, granted    []string
		want               string // "" is no refusal
	}{
		{"no scope declared, no pack named", "google-drive", "", nil, nil,
			"source google-drive declares no mint_scopes, so the hosted mint does not serve it"},
		{"no scope declared, its pack named", "google-slides", "google-docs", nil, nil,
			"source google-slides declares no mint_scopes, so the hosted mint does not serve it; re-apply its pack (`yardr pack apply google-docs`)"},
		{"no scope declared, whatever was granted", "google-drive", "google-drive", []string{}, []string{drive, docs},
			"source google-drive declares no mint_scopes, so the hosted mint does not serve it; re-apply its pack (`yardr pack apply google-drive`)"},
		{"a scope missing, no pack named", "google-calendar", "", []string{events, docs}, []string{"openid"},
			"google-calendar needs " + events + ", " + docs + ", missing from the Google connection's grant: reconnect at " + edge + "/google"},
		{"a scope missing, a pack named", "google-calendar", "google-calendar", []string{events, docs}, []string{"openid"},
			"google-calendar needs " + events + ", " + docs + ", missing from the Google connection's grant: reconnect at " + edge + "/google"},
		{"every scope granted", "google-drive", "google-drive", []string{drive}, []string{"openid", drive}, ""},
	} {
		err := CheckGoogleScopes(c.source, edge, c.needed, c.granted, c.pack)
		got := ""
		if err != nil {
			got = err.Error()
		}
		if got != c.want {
			t.Errorf("%s: CheckGoogleScopes = %q, want %q", c.name, got, c.want)
		}
	}
}
