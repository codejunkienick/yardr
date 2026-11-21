package web

import (
	"math"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// The claim is read as seconds since the epoch, whole or with a fraction,
// and as no time at all where it is absent, is not a number, or names no
// time a gateway signs. No other claim stands in for it.
func TestAuthTimeOf(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		claims jwt.MapClaims
		want   time.Time
	}{
		{"whole seconds", jwt.MapClaims{"auth_time": float64(1_700_000_000)}, time.Unix(1_700_000_000, 0)},
		{"seconds with a fraction", jwt.MapClaims{"auth_time": 1_700_000_000.5}, time.Unix(1_700_000_000, 500_000_000)},
		{"the last second RFC 3339 writes", jwt.MapClaims{"auth_time": float64(authTimeMax)}, time.Unix(authTimeMax, 0)},
		{"absent", jwt.MapClaims{}, time.Time{}},
		{"absent beside a new token's iat", jwt.MapClaims{"iat": float64(1_700_000_000), "exp": float64(1_700_000_300)}, time.Time{}},
		{"a string", jwt.MapClaims{"auth_time": "1700000000"}, time.Time{}},
		{"a boolean", jwt.MapClaims{"auth_time": true}, time.Time{}},
		{"the epoch", jwt.MapClaims{"auth_time": float64(0)}, time.Time{}},
		{"before the epoch", jwt.MapClaims{"auth_time": float64(-1)}, time.Time{}},
		{"past the last second", jwt.MapClaims{"auth_time": float64(authTimeMax + 1)}, time.Time{}},
		{"too large for a time", jwt.MapClaims{"auth_time": 1e300}, time.Time{}},
		{"not a number", jwt.MapClaims{"auth_time": math.NaN()}, time.Time{}},
	} {
		if got := authTimeOf(c.claims); !got.Equal(c.want) {
			t.Errorf("%s: authTimeOf = %v, want %v", c.name, got, c.want)
		}
	}
}

// A session is fresh while the window has not run out on its authentication
// time and the challenge came before it, and never where nothing is set to
// be fresh against: no time, no window, no challenge.
func TestFreshAt(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	const window = 15 * time.Minute
	// A challenge long ago, so that a row is about the window unless it
	// moves the challenge.
	long := now.Add(-time.Hour)
	for _, c := range []struct {
		name   string
		auth   time.Time
		window time.Duration
		since  time.Time
		want   bool
	}{
		{"just now", now, window, long, true},
		{"inside the window", now.Add(-10 * time.Minute), window, long, true},
		{"at the edge of the window", now.Add(-window), window, long, true},
		{"a moment past the window", now.Add(-window - time.Nanosecond), window, long, false},
		{"no authentication time", time.Time{}, window, long, false},
		{"after the challenge", now.Add(-time.Minute), window, now.Add(-2 * time.Minute), true},
		{"a moment after the challenge", now.Add(-2*time.Minute + time.Nanosecond), window, now.Add(-2 * time.Minute), true},
		{"at the challenge", now.Add(-2 * time.Minute), window, now.Add(-2 * time.Minute), false},
		{"before the challenge", now.Add(-3 * time.Minute), window, now.Add(-time.Minute), false},
		{"no challenge", now, window, time.Time{}, false},
		{"no window", now, 0, long, false},
		{"a window below zero", now, -window, long, false},
		{"ahead of now by the skew", now.Add(jwtLeeway), window, long, true},
		{"ahead of now past the skew", now.Add(jwtLeeway + time.Nanosecond), window, long, false},
	} {
		if got := (jwtIdentity{authTime: c.auth}).freshAt(now, c.window, c.since); got != c.want {
			t.Errorf("%s: freshAt = %v, want %v", c.name, got, c.want)
		}
	}
}

// A return state is spent once even when two gateway callbacks arrive at
// the same time; an expired state cannot be spent either.
func TestFreshChallengeReturnStateIsOneUse(t *testing.T) {
	t.Parallel()
	challenge := freshChallenge{bead: "yardr-one", ask: 3, user: "jo", at: time.Now()}
	s := &Server{
		freshPending: map[string]freshChallenge{"state": challenge},
		freshStrong:  map[string]freshAuth{},
	}
	auth := challenge.at.Add(time.Second)
	start := make(chan struct{})
	results := make(chan bool, 32)
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- s.consumeFresh("state", challenge, "jo", auth)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	succeeded := 0
	for ok := range results {
		if ok {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("concurrent returns consumed the state %d times, want once", succeeded)
	}
	if len(s.freshPending) != 0 {
		t.Fatal("the consumed return state remains pending")
	}
	if got := s.freshStrong["jo"]; !got.auth.Equal(auth) || !got.since.Equal(challenge.at) {
		t.Fatalf("the returned authentication is %+v, want auth %v since %v", got, auth, challenge.at)
	}

	expired := freshChallenge{bead: "yardr-two", ask: 4, user: "jo", at: time.Now().Add(-freshPendingTTL - time.Second)}
	s.freshPending["expired"] = expired
	if s.consumeFresh("expired", expired, "jo", auth) {
		t.Fatal("an expired return state was consumed")
	}
	if _, ok := s.freshPending["expired"]; ok {
		t.Fatal("the expired return state remains pending")
	}

	// A secret challenge is spent by its own short bound, not the hour:
	// past it the state and the values it held are gone, on a lookup as
	// on a spend, whether or not another challenge comes.
	held := map[string]string{"CRM_TOKEN": "held-value"}
	secretExpired := freshChallenge{bead: "yardr-three", ask: 5, user: "jo", at: time.Now().Add(-secretPendingTTL - time.Second), secret: true, values: held}
	s.freshPending["secret-expired"] = secretExpired
	if _, ok := s.lookupFresh("secret-expired"); ok {
		t.Fatal("a secret return state past its short bound was looked up")
	}
	if _, ok := s.freshPending["secret-expired"]; ok {
		t.Fatal("the expired secret return state remains pending")
	}
	s.freshPending["secret-expired"] = secretExpired
	if s.consumeFresh("secret-expired", secretExpired, "jo", auth) {
		t.Fatal("a secret return state past its short bound was consumed")
	}
	if _, ok := s.freshPending["secret-expired"]; ok {
		t.Fatal("the consumed-expired secret return state remains pending")
	}
	// Inside the bound a secret state still spends, as any other does.
	secretLive := freshChallenge{bead: "yardr-three", ask: 5, user: "jo", at: time.Now(), secret: true, values: held}
	s.freshPending["secret-live"] = secretLive
	if !s.consumeFresh("secret-live", secretLive, "jo", auth) {
		t.Fatal("a secret return state inside its bound was not consumed")
	}
	// A non-secret challenge at the same age as the expired secret one
	// is still inside its hour.
	old := freshChallenge{bead: "yardr-two", ask: 4, user: "jo", at: time.Now().Add(-secretPendingTTL - time.Second)}
	s.freshPending["old"] = old
	if !s.consumeFresh("old", old, "jo", auth) {
		t.Fatal("a non-secret return state past the secret bound but inside its hour was not consumed")
	}
	// Saving a new challenge drops an expired secret one with its values.
	s.freshPending["secret-stale"] = secretExpired
	s.putFresh("new", freshChallenge{bead: "yardr-four", ask: 6, user: "jo", at: time.Now()})
	if _, ok := s.freshPending["secret-stale"]; ok {
		t.Fatal("saving a challenge left an expired secret state pending")
	}
}
