package dispatch_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"yardr.dev/yardr/internal/dispatch"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
)

// releaseSite stands in for yardr.dev's releases: it serves one
// latest.json the test writes, and counts what was asked for.
type releaseSite struct {
	mu   sync.Mutex
	body []byte
	asks int
	hook func()
	srv  *httptest.Server
}

func newReleaseSite(t *testing.T, version string) *releaseSite {
	t.Helper()
	s := &releaseSite{}
	s.serve(version)
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.asks++
		body, hook := s.body, s.hook
		s.mu.Unlock()
		if hook != nil {
			hook()
		}
		if r.URL.Path != "/releases/latest.json" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *releaseSite) serve(version string) {
	b, _ := json.Marshal(map[string]any{
		"version":   version,
		"checksums": "/releases/" + version + "/checksums.txt",
		"targets":   map[string]any{},
	})
	s.mu.Lock()
	s.body = b
	s.mu.Unlock()
}

func (s *releaseSite) serveRaw(body string) {
	s.mu.Lock()
	s.body = []byte(body)
	s.mu.Unlock()
}

// onAsk runs f as each ask arrives, before the site answers it: a test
// changes the yard in the middle of the check's ask with it.
func (s *releaseSite) onAsk(f func()) {
	s.mu.Lock()
	s.hook = f
	s.mu.Unlock()
}

func (s *releaseSite) asked() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.asks
}

// releaseYard is a dispatcher on a throwaway store whose release
// check asks a stand-in site for a build the test names, by a clock
// the test moves. The operator jo and a yardmaster user stand to
// receive its mail.
type releaseYard struct {
	t    *testing.T
	s    store.API
	d    *dispatch.Dispatcher
	site *releaseSite
	home string
	log  *lockedWriter
	now  time.Time
}

func newReleaseYard(t *testing.T, version, current string) *releaseYard {
	t.Helper()
	home := t.TempDir()
	y := &releaseYard{t: t, s: storetest.NewIn(t, home), home: home, log: &lockedWriter{}, now: time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)}
	y.site = newReleaseSite(t, version)
	ctx := context.Background()
	must(t, y.s.SetSetting(ctx, store.OperatorSetting, "jo", "jo"))
	if _, err := y.s.AddUser(ctx, "yardmaster", "jo"); err != nil {
		t.Fatal(err)
	}
	// The hooks the check asks with, replaced for this test alone:
	// the stand-in's address and client, and the build's tag. They
	// are package hooks, so these tests run in sequence.
	oldBase, oldHTTP, oldTag := dispatch.ReleaseBase, dispatch.ReleaseHTTP, dispatch.ReleaseCurrentTag
	dispatch.ReleaseBase, dispatch.ReleaseHTTP = y.site.srv.URL, y.site.srv.Client()
	dispatch.ReleaseCurrentTag = func() string { return current }
	t.Cleanup(func() {
		dispatch.ReleaseBase, dispatch.ReleaseHTTP, dispatch.ReleaseCurrentTag = oldBase, oldHTTP, oldTag
	})
	y.d = &dispatch.Dispatcher{Store: y.s, Runners: map[string]dispatch.Runner{}, Home: home, Log: slog.New(slog.NewTextHandler(y.log, nil))}
	dispatch.SetClock(y.d, func() time.Time { return y.now })
	return y
}

func (y *releaseYard) notify() {
	y.t.Helper()
	must(y.t, y.s.SetSetting(context.Background(), store.AutoUpgradeSetting, "notify", "jo"))
}

func (y *releaseYard) pass(at time.Time) {
	y.t.Helper()
	y.now = at
	must(y.t, y.d.Reconcile(context.Background()))
}

// mails are the mail events the daemon sent, oldest first.
func (y *releaseYard) mails() []store.Event {
	y.t.Helper()
	evs, err := y.s.Events(context.Background(), "", 0)
	must(y.t, err)
	var out []store.Event
	for _, e := range evs {
		if e.Kind == "mail" && e.Actor == "daemon" {
			out = append(out, e)
		}
	}
	return out
}

func (y *releaseYard) state() string {
	y.t.Helper()
	b, err := os.ReadFile(store.ReleaseCheckState(y.home))
	if os.IsNotExist(err) {
		return ""
	}
	must(y.t, err)
	return string(b)
}

// Setting off asks nothing and mails nothing; notify with a newer
// release mails the operator and the yardmaster once, naming the
// version and the command; the same day and the next ask again but
// mail nothing more, since the release stands told of.
func TestReleaseCheckMailsOncePerRelease(t *testing.T) {
	y := newReleaseYard(t, "v1.3.0", "v1.2.3")
	day := y.now

	// Off (unset reads as off): no fetch, no mail, no state file.
	y.pass(day)
	if y.site.asked() != 0 || len(y.mails()) != 0 || y.state() != "" {
		t.Fatalf("off: asked %d, mails %v, state %q", y.site.asked(), y.mails(), y.state())
	}

	y.notify()
	y.pass(day.Add(time.Hour))
	mails := y.mails()
	if len(mails) != 2 {
		t.Fatalf("mails = %v, want operator and yardmaster", mails)
	}
	tos := map[string]bool{}
	for _, m := range mails {
		tos[m.Data["to"].(string)] = true
		if got := m.Data["text"].(string); got != "yardr v1.3.0 is out (you run v1.2.3): yardr upgrade" {
			t.Errorf("mail text = %q", got)
		}
	}
	if !tos["jo"] || !tos["yardmaster"] {
		t.Errorf("mailed to %v, want jo and yardmaster", tos)
	}
	if !strings.Contains(y.state(), `"told":"v1.3.0"`) {
		t.Errorf("state = %q, want v1.3.0 told", y.state())
	}

	// Same day: throttled, no second ask. Next day: asked again,
	// already told, no second mail.
	asks := y.site.asked()
	y.pass(day.Add(2 * time.Hour))
	if y.site.asked() != asks {
		t.Errorf("same-day pass asked again: %d, want %d", y.site.asked(), asks)
	}
	y.pass(day.AddDate(0, 0, 1))
	if y.site.asked() != asks+1 || len(y.mails()) != 2 {
		t.Errorf("next day: asked %d, mails %d, want one more ask and no more mail", y.site.asked(), len(y.mails()))
	}

	// A still newer release the next day mails again, once.
	y.site.serve("v1.4.0")
	y.pass(day.AddDate(0, 0, 2))
	if len(y.mails()) != 4 {
		t.Fatalf("mails after v1.4.0 = %d, want 4", len(y.mails()))
	}
}

// The same release and an older one are silent, as is a newer one
// the state file already tells of.
func TestReleaseCheckSilentCases(t *testing.T) {
	for _, tc := range []struct{ name, latest, current, told string }{
		{"same", "v1.2.3", "v1.2.3", ""},
		{"older", "v1.2.2", "v1.2.3", ""},
		{"told", "v1.3.0", "v1.2.3", "v1.3.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			y := newReleaseYard(t, tc.latest, tc.current)
			if tc.told != "" {
				// Yesterday it was told of: today's ask finds it so.
				prev := `{"told":"` + tc.told + `","last_check":"2026-11-23"}` + "\n"
				must(t, os.WriteFile(store.ReleaseCheckState(y.home), []byte(prev), 0o600))
			}
			y.notify()
			y.pass(y.now)
			if len(y.mails()) != 0 {
				t.Fatalf("mails = %v, want none", y.mails())
			}
			if y.site.asked() != 1 {
				t.Fatalf("asked %d, want the day's one ask", y.site.asked())
			}
		})
	}
}

// A fault the check meets is said once, and the day is spent before the
// ask is made: two passes the same day make the asks and send the mails
// the row names and say the fault once, by the fault-once telling the
// telemetry pass twins. A row's check reads what the first pass held as
// its fault, which the second pass the same day may have cleared.
func TestReleaseCheckFaultsOnce(t *testing.T) {
	// host is what a host that means harm sends as a version: a marker,
	// as many times as the row says.
	const host = "HOSTTEXT"
	// quiet says none of the host's text reached the log or the held
	// fault, and that what did is short: told in the host's words, a
	// fault would fill the log and the yard's memory of it with as many
	// bytes as the host likes.
	quiet := func(t *testing.T, y *releaseYard, told string) {
		if strings.Contains(told, host) || strings.Contains(y.log.String(), host) {
			t.Errorf("the host's text reached the log or the held fault %.300q", told)
		}
		if len(told) >= 256 {
			t.Errorf("held fault is %d bytes, want under 256: %q", len(told), told)
		}
		for _, line := range strings.Split(y.log.String(), "\n") {
			if len(line) >= 256 {
				t.Errorf("a log line is %d bytes, want under 256: %.300s", len(line), line)
			}
		}
	}
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, y *releaseYard)
		fault string // the log line said once
		asks  int
		mails int
		check func(t *testing.T, y *releaseYard, told string)
	}{
		{
			name:  "a latest.json that cannot be read",
			setup: func(t *testing.T, y *releaseYard) { y.site.serveRaw(`{"version":`) },
			fault: "checking the latest release",
			asks:  1,
		},
		{
			// No home takes no file, as root or not, so the day cannot be
			// spent: nothing is asked and nothing mailed, and the fault is
			// the same words on the second pass.
			name: "a state file that cannot be written before the ask",
			setup: func(t *testing.T, y *releaseYard) {
				y.d.Home = filepath.Join(t.TempDir(), "missing")
			},
			fault: "recording the check",
			check: func(t *testing.T, y *releaseYard, told string) {
				// The state file and the cause, never the temp file, whose
				// name is random and would make every pass's words its own.
				want := "writing " + filepath.Join(y.d.Home, "releasecheck.json") + ": "
				if !strings.Contains(told, want) || strings.Contains(told, ".releasecheck-") {
					t.Errorf("held fault = %q, want it to name %q and no temp file", told, want)
				}
			},
		},
		{
			// The day is spent when the ask arrives, and the directory is
			// read-only from then: the write of what was told finds no room
			// for its temp file, and the file the day was spent in stands.
			name: "a state file that cannot be written after the mail",
			setup: func(t *testing.T, y *releaseYard) {
				if os.Geteuid() == 0 {
					t.Skip("root writes into a directory made read-only")
				}
				// The check's own directory: the store's file is not in it.
				dir := t.TempDir()
				y.home, y.d.Home = dir, dir
				t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
				y.site.onAsk(func() {
					if err := os.Chmod(dir, 0o500); err != nil {
						t.Error(err)
					}
				})
			},
			fault: "recording the check",
			asks:  1,
			mails: 2,
			check: func(t *testing.T, y *releaseYard, told string) {
				if st := y.state(); !strings.Contains(st, `"last_check":"2026-11-24"`) || strings.Contains(st, "told") {
					t.Errorf("state = %q, want the day spent and no release told", st)
				}
			},
		},
		{
			// The most a latest.json is read to is 64 KiB: a version that
			// fills it is one the check reads, and refuses.
			name: "a version of 60 KiB",
			setup: func(t *testing.T, y *releaseYard) {
				y.site.serveRaw(`{"version":"` + strings.Repeat(host, 60*1024/len(host)) + `"}`)
			},
			fault: "checking the latest release",
			asks:  1,
			check: quiet,
		},
		{
			// A release that is newer, as a latest.json over the most it is
			// read to: refused, and so not mailed of.
			name: "a latest.json over 64 KiB",
			setup: func(t *testing.T, y *releaseYard) {
				y.site.serveRaw(`{"version":"v1.3.0","more":"` + strings.Repeat(host, 64*1024/len(host)+1) + `"}`)
			},
			fault: "checking the latest release",
			asks:  1,
			check: quiet,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			y := newReleaseYard(t, "v1.3.0", "v1.2.3")
			tc.setup(t, y)
			y.notify()
			y.pass(y.now)
			told := dispatch.ReleaseCheckTold(y.d)
			y.pass(y.now.Add(time.Hour))
			if got := y.site.asked(); got != tc.asks {
				t.Errorf("asked %d, want %d", got, tc.asks)
			}
			if got := len(y.mails()); got != tc.mails {
				t.Errorf("mails = %v, want %d", y.mails(), tc.mails)
			}
			if n := strings.Count(y.log.String(), "release check: "+tc.fault); n != 1 {
				t.Errorf("fault said %d times, want 1:\n%s", n, y.log.String())
			}
			if tc.check != nil {
				tc.check(t, y, told)
			}
		})
	}
}

// Parse and compare of a release's version, the moved check's own
// arithmetic.
func TestParseCompareReleaseVersion(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"v1.2.3", "v1.2.3", 0},
		{"v1.2.4", "v1.2.3", 1},
		{"v1.3.0", "v1.2.9", 1},
		{"v2.0.0", "v1.9.9", 1},
		{"v1.2.3", "v1.2.4", -1},
	} {
		av, ok := dispatch.ParseReleaseVersion(tc.a)
		if !ok {
			t.Fatalf("ParseReleaseVersion(%q) refused", tc.a)
		}
		bv, _ := dispatch.ParseReleaseVersion(tc.b)
		if got := dispatch.CompareReleaseVersion(av, bv); got != tc.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
	for _, bad := range []string{"", "1.2.3", "v1.2", "v1.2.3.4", "v1.2.x", "v1..3"} {
		if _, ok := dispatch.ParseReleaseVersion(bad); ok {
			t.Errorf("ParseReleaseVersion(%q) taken", bad)
		}
	}
}
