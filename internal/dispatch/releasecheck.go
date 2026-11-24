package dispatch

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/version"
)

// The release check: the latest signed release's version, read from
// yardr.dev's latest.json, parsed and compared with this build's.
// It lives here, beside the daemon pass that mails of a newer one
// (releaseCheck below), so yardr upgrade and the daemon read the
// same latest.json the same way. Only the version is read here: no
// archive is fetched and no signature is checked, because the check
// installs nothing. A spoofed latest.json can at most cause a mail
// that yardr upgrade then refuses when it verifies the release for
// real: install-time verify is the trust point, and this check is
// only a bell.

// ReleaseSite is the one host a release is fetched from. Every
// address the fetch asks for is this host plus a path under
// /releases/ that the checks below have vetted, so a latest.json
// that names another host, or a path outside /releases/, fetches
// nothing.
const ReleaseSite = "https://yardr.dev"

// ReleaseBase is ReleaseSite as a test reaches it: a fixture release
// stands at another address. Production runs never change it, so
// the one-host rule holds there.
var ReleaseBase = ReleaseSite

// ReleaseHTTP fetches a release. A redirect is a refusal, as in
// footplate: the site serves each file directly, and following a
// redirect would leave the one named host. The bound is for a host
// that hangs, sized for the archive on a slow link.
var ReleaseHTTP = &http.Client{
	Timeout: 5 * time.Minute,
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// ReleaseCurrentTag is the release this build was made for, "" for
// a build from a checkout; tests name one.
var ReleaseCurrentTag = version.Tag

// ReleaseLatestMost is what is read of latest.json at the most.
const ReleaseLatestMost = 64 * 1024

// ReleaseVersion is a release's tag as three numbers.
type ReleaseVersion struct{ Major, Minor, Patch int }

// ParseReleaseVersion reads a tag v1.2.3 and nothing more: where a
// pre-release would stand against this build is not guessed at.
func ParseReleaseVersion(s string) (ReleaseVersion, bool) {
	var v ReleaseVersion
	rest, ok := strings.CutPrefix(s, "v")
	if !ok {
		return v, false
	}
	parts := strings.Split(rest, ".")
	if len(parts) != 3 {
		return v, false
	}
	nums := []*int{&v.Major, &v.Minor, &v.Patch}
	for i, p := range parts {
		if p == "" || len(p) > 9 {
			return v, false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return v, false
			}
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return v, false
		}
		*nums[i] = n
	}
	return v, true
}

// CompareReleaseVersion orders two release versions: -1, 0 or 1 as
// a is older than, the same as, or newer than b.
func CompareReleaseVersion(a, b ReleaseVersion) int {
	for _, pair := range [][2]int{{a.Major, b.Major}, {a.Minor, b.Minor}, {a.Patch, b.Patch}} {
		if pair[0] != pair[1] {
			if pair[0] < pair[1] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// ReleaseLatest is latest.json as sync-release writes it, of which
// only what leads to the files is read. Its per-target size and
// sha256 are for a person: never this fetch's trust, which is the
// signed checksums alone.
type ReleaseLatest struct {
	Version   string                   `json:"version"`
	Checksums string                   `json:"checksums"`
	Targets   map[string]ReleaseTarget `json:"targets"`
}

// ReleaseTarget is one system of a release in latest.json.
type ReleaseTarget struct {
	Path string `json:"path"`
}

// ReleaseAnswered is the site answering with any status but 200: the
// file is not served, directly or at all.
type ReleaseAnswered struct {
	Path   string
	Status int
	// Base is the host that was asked, named in Error.
	Base string
}

func (e ReleaseAnswered) Error() string {
	base := e.Base
	if base == "" {
		base = ReleaseBase
	}
	return fmt.Sprintf("%s did not serve %s directly (status %d), so yardr was not fetched", base, e.Path, e.Status)
}

// ReleaseGet reads the file the site has at path, of most bytes at
// the most, from base with client. path is the site's word, vetted
// by ReleaseFileUnder where it came from latest.json, and the
// constant /releases/latest.json where it did not: both stay under
// /releases/ on the one named host. A request or a read that fails
// is told by releaseFetchCause and not in the transport's words,
// which quote what the host sent.
func ReleaseGet(ctx context.Context, base string, client *http.Client, path string, most int64) ([]byte, error) {
	if ReleaseFileUnder(path) == "" {
		return nil, fmt.Errorf("%q is no file under /releases/ on %s; yardr was not fetched", path, base)
	}
	if client == nil {
		client = ReleaseHTTP
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(base, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s does not answer, so yardr was not fetched (%s); this needs the network once, and the old binary is left as it is", base, releaseFetchCause(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// The body of a refusal is not read as the file it is not.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, ReleaseAnswered{Path: path, Status: resp.StatusCode, Base: base}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, most+1))
	if err != nil {
		return nil, fmt.Errorf("%s's answer for %s could not be read, so yardr was not fetched (%s)", base, path, releaseFetchCause(err))
	}
	if int64(len(b)) > most {
		return nil, fmt.Errorf("%s's %s is larger than %d bytes, so yardr was not fetched", base, path, most)
	}
	return b, nil
}

// releaseFetchCause says in a word why a request to the site, or the
// read of its answer, failed, never in the error's own text: net/http
// quotes what the host sent into its errors (a first line that is no
// status line, a header line that is none) at any length, and a
// fault is logged, held by the daemon and printed by yardr upgrade.
// The words are the ones an operator acts on differently: a host that
// is slow, a name that is not found, a certificate or a handshake the
// client refuses, and the rest, which is the network. A TLS alert the
// host sends back is not told apart: crypto/tls gives it a type of its
// own that is not exported, and AlertError is QUIC's alone.
func releaseFetchCause(err error) string {
	var netErr net.Error
	var dnsErr *net.DNSError
	var certErr *tls.CertificateVerificationError
	var headerErr tls.RecordHeaderError
	switch {
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		return "timed out"
	case errors.As(err, &dnsErr) && dnsErr.IsNotFound:
		return "no such host"
	case errors.As(err, &certErr) || errors.As(err, &headerErr):
		return "TLS error"
	}
	return "network error"
}

// ReleaseFileUnder is the file's name, when path is a file under
// /releases/ written in letters, digits, ., _, - and / alone, with
// no empty or dotted part. The path goes into the first line of a
// request as it is, so nothing else is let through.
func ReleaseFileUnder(path string) string {
	rest, ok := strings.CutPrefix(path, "/releases/")
	if !ok {
		return ""
	}
	for _, b := range []byte(rest) {
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '.' || b == '_' || b == '-' || b == '/') {
			return ""
		}
	}
	parts := strings.Split(rest, "/")
	for _, p := range parts {
		if p == "" || strings.HasPrefix(p, ".") {
			return ""
		}
	}
	return parts[len(parts)-1]
}

// FetchLatest reads the latest release from base: latest.json only,
// proved no further than that it names a version. Callers that
// install verify the signed checksums before they trust it. A version
// that is none is refused in words of this file's own, never with the
// host's in them: the host may send as many bytes there as the body
// allows, and the error is told in the daemon's log and on a terminal.
func FetchLatest(ctx context.Context, base string, client *http.Client) (*ReleaseLatest, error) {
	b, err := ReleaseGet(ctx, base, client, "/releases/latest.json", ReleaseLatestMost)
	if err != nil {
		return nil, err
	}
	var latest ReleaseLatest
	if err := json.Unmarshal(b, &latest); err != nil {
		return nil, fmt.Errorf("yardr.dev's /releases/latest.json cannot be read: %v; yardr was not fetched", err)
	}
	if _, ok := ParseReleaseVersion(latest.Version); !ok {
		return nil, fmt.Errorf("yardr.dev's /releases/latest.json does not name a release version; yardr was not fetched")
	}
	return &latest, nil
}

// releaseCheckState is what the home state file holds: the version
// the yard last mailed of and the UTC day it last asked, so one
// release is mailed of once and the site is asked once a day.
type releaseCheckState struct {
	Told      string `json:"told,omitempty"`
	LastCheck string `json:"last_check,omitempty"`
}

func loadReleaseCheckState(path string) (*releaseCheckState, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	st := &releaseCheckState{}
	if err := json.Unmarshal(data, st); err != nil {
		return nil, fmt.Errorf("%s: %w; remove it to have the release check start over", path, err)
	}
	return st, nil
}

// saveReleaseCheckState writes st by a temp file renamed over path, so a
// write that fails leaves the file as it was: the check leans on that
// when the write of what it told comes after the mail.
func saveReleaseCheckState(path string, st *releaseCheckState) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".releasecheck-*.tmp")
	if err != nil {
		return releaseCheckWriteError(path, err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return releaseCheckWriteError(path, err)
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return releaseCheckWriteError(path, err)
	}
	if err := f.Close(); err != nil {
		return releaseCheckWriteError(path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return releaseCheckWriteError(path, err)
	}
	return nil
}

// releaseCheckWriteError says that path could not be written, and why.
// The path error of a step names the temp file, whose name is random,
// and the rename's link error names both files: a message with either in
// it differs on every pass, and releaseCheckFault, which says a fault
// once by comparing messages, would say it on every pass.
func releaseCheckWriteError(path string, err error) error {
	var pathErr *fs.PathError
	var linkErr *os.LinkError
	switch {
	case errors.As(err, &pathErr):
		err = pathErr.Err
	case errors.As(err, &linkErr):
		err = linkErr.Err
	}
	return fmt.Errorf("writing %s: %w", path, err)
}

// releaseCheckAsk bounds the daily ask for latest.json.
const releaseCheckAsk = 10 * time.Second

// releaseCheckDay is the UTC day of t, as the state file holds it.
func releaseCheckDay(t time.Time) string { return t.UTC().Format("2006-01-02") }

// releaseBase, releaseHTTP and releaseTag are what this pass asks
// with: the package hooks above, which a test replaces with a
// stand-in listener, its client and a named tag (the tests-replace
// pattern of upgrade.go's hooks), so no test reaches the real site.
func (d *Dispatcher) releaseBase() string { return ReleaseBase }

func (d *Dispatcher) releaseHTTP() *http.Client { return ReleaseHTTP }

func (d *Dispatcher) releaseTag() string { return ReleaseCurrentTag() }

// releaseCheck mails the operator and the yardmaster when a newer
// signed release is out, once per release, when the yard opted in
// with auto_upgrade=notify. It rides the telemetry pass's tail in
// telemetry.go: same cadence (every pass, throttled to one ask a
// day here), same state-file shape beside home, and the same
// fault-once telling. Off, the same release, one already told of,
// or an older one is silent and costs no fetch beyond the day's
// one. It fetches latest.json alone and verifies nothing: yardr
// upgrade verifies the signed release before it installs, so a
// spoofed answer mails at most and installs nothing.
func (d *Dispatcher) releaseCheck(ctx context.Context) {
	if d.Home == "" {
		return // no home, no state file: nothing is mailed
	}
	faulted := false
	defer func() {
		if !faulted {
			d.releaseCheckTold = ""
		}
	}()
	fault := func(what string, err error) {
		faulted = true
		d.releaseCheckFault(what, err)
	}
	settings, err := d.Store.Settings(ctx)
	if err != nil {
		fault("reading the settings", err)
		return
	}
	if settings[store.AutoUpgradeSetting] != "notify" {
		return
	}
	path := store.ReleaseCheckState(d.Home)
	state, err := loadReleaseCheckState(path)
	if err != nil {
		fault("reading the state file", err)
		return
	}
	now := d.now()
	today := releaseCheckDay(now)
	if state != nil && state.LastCheck == today {
		return // asked today already
	}
	told := ""
	if state != nil {
		told = state.Told
	}
	// Spend the day before the ask is made, as telemetry marks its
	// attempt before its request, whatever the ask will find: a bad
	// answer is not retried on every pass (its fault is told once, and
	// the next ask is tomorrow's), and a restarted daemon makes no
	// second ask today. A home that cannot be written makes no ask at
	// all: an ask whose day was never recorded would be made, and its
	// release mailed of, on every pass.
	if err := saveReleaseCheckState(path, &releaseCheckState{Told: told, LastCheck: today}); err != nil {
		fault("recording the check", err)
		return
	}
	// The round waits on this ask, and the client's own bound is sized
	// for an archive: latest.json is a few bytes, so a host that hangs
	// is given telemetry's ten seconds, not five minutes of the round.
	askCtx, cancel := context.WithTimeout(ctx, releaseCheckAsk)
	latest, err := FetchLatest(askCtx, d.releaseBase(), d.releaseHTTP())
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return // the daemon was told to stop; the day's ask is spent
		}
		fault("checking the latest release", err)
		return
	}
	curTag := d.releaseTag()
	cur, ok := ParseReleaseVersion(curTag)
	if !ok {
		// A build with no release tag cannot be ordered against a
		// release: it says nothing, and the day stays spent.
		return
	}
	ver, _ := ParseReleaseVersion(latest.Version)
	if CompareReleaseVersion(ver, cur) <= 0 {
		return // the same release, or an older one
	}
	if told == latest.Version {
		return // already mailed of this one
	}
	ym := settings[store.YardmasterSetting]
	if ym == "" {
		ym = "yardmaster"
	}
	op := settings[store.OperatorSetting]
	text := fmt.Sprintf("yardr %s is out (you run %s): yardr upgrade", latest.Version, curTag)
	mailed := true
	for _, to := range []string{ym, op} {
		if to == "" {
			continue
		}
		if err := d.Store.Mail(ctx, to, text, "daemon"); err != nil {
			fault("mailing of the new release", err)
			mailed = false
		}
	}
	if !mailed {
		// A release nobody was told of is not recorded as told: the
		// day is spent, and tomorrow's ask tries the mail again.
		return
	}
	// A write that fails here leaves the file the day was spent in, whole,
	// so no later pass today asks again or mails again; the release goes
	// unrecorded as told, and tomorrow's ask mails of it once more.
	if err := saveReleaseCheckState(path, &releaseCheckState{Told: latest.Version, LastCheck: today}); err != nil {
		fault("recording the check", err)
	}
}

// releaseCheckFault says a fault of the check's own, once until it
// is gone, as telemetryFault does: said on every pass, a state file
// that cannot be written would fill the log. err is a local error,
// never a request or an answer beyond the fetch's own words.
func (d *Dispatcher) releaseCheckFault(what string, err error) {
	msg := what
	var args []any
	if err != nil {
		msg += ": " + err.Error()
		args = []any{"err", err}
	}
	if msg == d.releaseCheckTold {
		return
	}
	d.releaseCheckTold = msg
	d.Log.Warn("release check: "+what, args...)
}
