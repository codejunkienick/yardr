package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/yardkey"
)

// memberRefusal is what a member's device is told of a secret it gives down
// the link from the edge that is not the code of its own sign-in from a sealed
// device: one sentence, the same on a hosted yard and on any other, with no
// ssh in it, since a member has no shell on the yard's machine. A call that
// came plain is told the edge saw what was sent with it; a sealed call's
// refusal (memberRefusalSealed) drops that sentence, since it crossed as
// ciphertext.
const memberRefusal = "a secret from a member's device does not cross the edge, except the code of their own sign-in from a sealed device (pairing the device with the QR makes it one); " +
	"the operator gives any other on the yard's machine, from their own sealed device, or from any paired device of theirs where the yard is hosted." +
	" The edge saw what was sent with it: if it was a real key, make a new one at the service."

// memberRefusalSealed is memberRefusal for a call that came sealed: the one
// sentence without the tail, since the edge read nothing of what crossed as
// ciphertext.
const memberRefusalSealed = "a secret from a member's device does not cross the edge, except the code of their own sign-in from a sealed device (pairing the device with the QR makes it one); " +
	"the operator gives any other on the yard's machine, from their own sealed device, or from any paired device of theirs where the yard is hosted."

// memberGoogleRefusal is what a member's device is told of their own
// Google sign-in given down the link from a device that is no QR-paired
// one: it names the QR-paired device and the terminal command, and never
// the operator, who is refused that giving. A plain call is told the
// edge saw what was sent with it; a sealed one's (memberGoogleRefusalSealed)
// drops that sentence, since it crossed as ciphertext.
const memberGoogleRefusal = "your Google sign-in does not cross the edge from this device: it crosses only from a device paired with the QR (pairing the device with the QR makes it one), " +
	"or in a terminal (yardr bead answer <bead> <ask> --secret); it is yours alone to give, and the operator does not give it." +
	" The edge saw what was sent with it: if it was a real key, make a new one at the service."

const memberGoogleRefusalSealed = "your Google sign-in does not cross the edge from this device: it crosses only from a device paired with the QR (pairing the device with the QR makes it one), " +
	"or in a terminal (yardr bead answer <bead> <ask> --secret); it is yours alone to give, and the operator does not give it."

// The stand-in export sam pastes from their own machine, and the refresh
// token inside it: what a giving must never print.
const (
	samGoogleExport  = "{\n  \"client_id\": \"123-fake.apps.googleusercontent.com\",\n  \"client_secret\": \"GOCSPX-fake-sam-secret\",\n  \"refresh_token\": \"1//fake-sam-refresh\",\n  \"type\": \"authorized_user\"\n}"
	samGoogleRefresh = "1//fake-sam-refresh"
)

// memberYard is a yard whose member sam has a login of their own waiting for
// the code its sign-in page shows: the group that carries sam's login, the ask
// for the code on a bead, and the wait registered under claude-sam. Every call
// is made down the simulated link from the edge.
type memberYard struct {
	*pairYard
	login  uint64      // the ask for the code of sam's own sign-in, of sam
	waited chan string // what sam's waiting login is handed, once
}

const memberCode = "code-fr0m-sam-s-device"

// newMemberYard makes a memberYard: hosted when hosted, and otherwise a yard
// connected to the edge from the person's own machine. Its sources are the
// crm's credential and the push service's address, so that sam has asks of
// those kinds too.
func newMemberYard(t *testing.T, hosted bool) *memberYard {
	t.Helper()
	ctx := context.Background()
	y := &memberYard{pairYard: newPairYard(t), waited: make(chan string, 1)}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for file, decl := range map[string]string{"crm.yaml": secretDecl, "push.yaml": addressDecl} {
		_, err := y.st.PutSource(ctx, file, decl, "jo")
		must(err)
	}
	_, err := y.st.AddUser(ctx, "kim", "jo")
	must(err)
	_, err = y.st.PutCrew(ctx, store.CrewMember{Name: "yardmaster", Runner: "herdr", Config: map[string]any{"kind": "claude"}}, "jo")
	must(err)
	_, err = y.st.CreateBead(ctx, store.NewBead{Title: "Read the CRM", ID: "yd-crm"}, "yardmaster")
	must(err)
	must(y.st.PutGroup(ctx, store.Group{Name: "sam-builders", Runner: "herdr", Config: map[string]any{"kind": "claude", "login_for": "sam"}}, "jo"))
	y.login = y.ask(t, "sam", &store.AskSecret{Login: "claude-sam", As: "sam"})
	if hosted {
		must(y.st.SetSetting(ctx, store.HostedSetting, "true", "jo"))
	}
	// sam's login waits for its code, the wait registered by its leader.
	wait, stop := context.WithCancel(ctx)
	t.Cleanup(stop)
	go func() {
		if got, err := y.st.LoginWait(wait, "claude-sam", "sam-builders"); err == nil {
			y.waited <- got
		}
	}()
	ready, leave := context.WithTimeout(ctx, 10*time.Second)
	defer leave()
	if err := y.st.LoginWaitReady(ready, "claude-sam"); err != nil {
		t.Fatalf("sam's login never waited: %v", err)
	}
	return y
}

// ask puts a secret ask for sec on the bead, of to, and answers its number.
func (y *memberYard) ask(t *testing.T, to string, sec *store.AskSecret) uint64 {
	t.Helper()
	seq, err := y.st.AddAsk(context.Background(), "yd-crm", store.Ask{Question: "A secret?", To: to,
		Context: []string{"It is shown where the service makes it."}, Secret: sec}, "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	return seq
}

// giveArgs is the arguments of a giving of values to the ask, as a client
// sends them once its person has seen the weight: the actor is the listener's
// to fill in. change, when given, alters the arguments first.
func giveArgs(t *testing.T, ask uint64, values map[string]string, change ...func(map[string]any)) []byte {
	t.Helper()
	args := map[string]any{"bead": "yd-crm", "ask": ask, "values": values, "weighed": true}
	for _, c := range change {
		c(args)
	}
	b, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// giveReply reads a GiveSecret reply: what it gave, or the words it was
// refused in.
func giveReply(t *testing.T, raw []byte) (given *store.SourceKeyed, refused string) {
	t.Helper()
	var got struct {
		Result *store.SourceKeyed `json:"result"`
		Error  *wireError         `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(raw), &got); err != nil {
		t.Fatalf("the answer is %s (%v)", raw, err)
	}
	if got.Error != nil {
		return nil, got.Error.Message
	}
	return got.Result, ""
}

// sealedGive makes GiveSecret with body from the sealed device d, as the edge
// carries it: ciphertext, with no token.
func (y *memberYard) sealedGive(t *testing.T, d *e2eDevice, body []byte) (*store.SourceKeyed, string) {
	t.Helper()
	plain := bindFor("", false)
	outer, _, status, _, answer := d.call(t, http.MethodPost, "/user/call/GiveSecret", nil, body, plain, plain)
	if outer != http.StatusOK || status != http.StatusOK {
		t.Fatalf("the sealed giving is answered outer %d, inner %d %s", outer, status, answer)
	}
	return giveReply(t, answer)
}

// plainGive makes GiveSecret with body from a device's token, as the edge
// carries a call that came plain: whole, with the token.
func (y *memberYard) plainGive(t *testing.T, token string, body []byte) (*store.SourceKeyed, string) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/user/call/GiveSecret", bytes.NewReader(body))
	r.RemoteAddr = linkRemoteAddr
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	y.u.Handler().ServeHTTP(w, r)
	return giveReply(t, w.Body.Bytes())
}

// plainDevice is a device of user that holds no channel key: it calls plain.
func (y *memberYard) plainDevice(t *testing.T, user, name string) string {
	t.Helper()
	d, err := y.st.AddDevice(context.Background(), user, name, "jo")
	if err != nil {
		t.Fatal(err)
	}
	return d.Token
}

// inPlaceDevice is a device of user that pinned its channel key in place, as
// one paired before the channel does, and holds a session for it.
func (y *memberYard) inPlaceDevice(t *testing.T, user, name string) *e2eDevice {
	t.Helper()
	token, key := y.plainDevice(t, user, name), newChannelKey(t)
	if code, msg, _ := y.linkRegister(t, token, yardkey.ChannelPublic(key)); code != "" {
		t.Fatalf("%s's %s registers its key in place: %s %s", user, name, code, msg)
	}
	if _, pinned := y.pinnedAs(t, user, name); pinned != store.PinnedInPlace {
		t.Fatalf("%s's %s is pinned %q, want in place", user, name, pinned)
	}
	link := y.u.Handler()
	down := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = linkRemoteAddr
		link.ServeHTTP(w, r)
	})
	return (&e2eDevice{y: y.pairYard, key: key, handler: down}).rehandshake(t)
}

// answeredBy is who answered the bead's asks and the device each came through.
func (y *memberYard) answeredBy(t *testing.T) []string {
	t.Helper()
	notes, err := y.st.Notes(context.Background(), "yd-crm")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, n := range notes {
		if n.Kind == store.NoteAnswer {
			got = append(got, n.Author+" via "+n.Via)
		}
		if strings.Contains(n.Text, memberCode) {
			t.Errorf("note %d holds the code: %q", n.Seq, n.Text)
		}
	}
	return got
}

// nothingGiven fails the test when anything was given: the waiting login was
// handed nothing and no ask of the bead has an answer, so the call that was
// refused was not served.
func (y *memberYard) nothingGiven(t *testing.T, after string) {
	t.Helper()
	select {
	case got := <-y.waited:
		t.Errorf("%s: the waiting login was handed %q", after, got)
	default:
	}
	if got := y.answeredBy(t); len(got) != 0 {
		t.Errorf("%s: the bead is answered %q", after, got)
	}
	// A giving of sam's own credential or address records the file it wrote.
	if u, err := y.st.GetUser(context.Background(), "sam"); err != nil || u.EnvFile != "" {
		t.Errorf("%s: sam = %+v, %v: want no file of theirs", after, u, err)
	}
}

// takes gives sam's code from the sealed device d and checks that it was taken:
// the waiting login is handed it, the answer on the bead is by via, and the
// code is in no note.
func (y *memberYard) takes(t *testing.T, d *e2eDevice, via string) {
	t.Helper()
	given, refused := y.sealedGive(t, d, giveArgs(t, y.login, map[string]string{"INPUT": memberCode}))
	if refused != "" || given == nil || given.Login != "claude-sam" || given.File != "" || !slices.Equal(given.Given, []string{"INPUT"}) {
		t.Fatalf("the sealed giving is answered %+v, refused %q, want the login claude-sam given INPUT", given, refused)
	}
	select {
	case got := <-y.waited:
		if got != memberCode {
			t.Errorf("sam's login was handed %q, want the code that was given", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("sam's login was never handed the code")
	}
	if got := y.answeredBy(t); !slices.Equal(got, []string{via}) {
		t.Errorf("the giving is answered on the bead by %q, want %q", got, via)
	}
}

// samGoogleAsk puts the ask for sam's own Google sign-in on the bead and
// answers its number.
func (y *memberYard) samGoogleAsk(t *testing.T) uint64 {
	t.Helper()
	return y.ask(t, "sam", &store.AskSecret{Google: "sam@example.com", As: "sam"})
}

// samGoogleFile is where sam's pasted sign-in lands under home.
func samGoogleFile(home string) string {
	return filepath.Join(home, ".config", "gws-accounts", "sam@example.com", "credentials.json")
}

// googleLands checks a giving of sam's sign-in wrote its credentials
// file under home at 0600, stripped to one line, was answered on the
// bead by via, and holds in no note.
func (y *memberYard) googleLands(t *testing.T, home, via string) {
	t.Helper()
	file := samGoogleFile(home)
	st, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("the credentials file is mode %04o, want 0600", st.Mode().Perm())
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "\n") || !strings.Contains(string(raw), samGoogleRefresh) {
		t.Fatalf("the credentials file holds %q", raw)
	}
	if got := y.answeredBy(t); !slices.Equal(got, []string{via}) {
		t.Errorf("the giving is answered on the bead by %q, want %q", got, via)
	}
	notes, err := y.st.Notes(context.Background(), "yd-crm")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range notes {
		if strings.Contains(n.Text, samGoogleRefresh) || strings.Contains(n.Text, "GOCSPX-fake-sam-secret") {
			t.Errorf("note %d holds the pasted value: %q", n.Seq, n.Text)
		}
	}
}

// noGoogleFile fails the test when a refused giving wrote sam's
// credentials file or answered the bead.
func (y *memberYard) noGoogleFile(t *testing.T, home, after string) {
	t.Helper()
	if _, err := os.Stat(samGoogleFile(home)); !os.IsNotExist(err) {
		t.Errorf("%s: sam's credentials file stands: %v", after, err)
	}
	if got := y.answeredBy(t); len(got) != 0 {
		t.Errorf("%s: the bead is answered %q", after, got)
	}
}

// A member's device gives their own Google sign-in down the link from
// the edge when the call came sealed from a device paired with the QR,
// on a yard that is not hosted and on a hosted one alike: the pasted
// export lands as their credentials file. A device sealed in place is
// refused, since the export is a refresh token that is written to disk
// and lasts where the login code is used once and kept in memory; a
// call that came plain is refused too, each in the Google sentence,
// and neither spends the ask. Another member's ask and the yard's own
// are not there for a member's device, and the operator standing in is
// refused by the store, whose giving this is sam's alone.
func TestMemberGivesTheirOwnGoogleSignInDownTheLink(t *testing.T) {
	for _, hosted := range []bool{false, true} {
		name := "connected yard"
		if hosted {
			name = "hosted yard"
		}
		t.Run(name, func(t *testing.T) {
			values := map[string]string{"GOOGLE_CREDENTIALS": samGoogleExport}
			t.Run("sealed, paired with the QR", func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("HOME", home)
				y := newMemberYard(t, hosted)
				ask := y.samGoogleAsk(t)
				given, refused := y.sealedGive(t, newE2EDevice(t, y.pairYard, "sam", "phone"), giveArgs(t, ask, values))
				if refused != "" || given == nil || given.Google != "sam@example.com" || given.As != "sam" || !slices.Equal(given.Given, []string{"GOOGLE_CREDENTIALS"}) {
					t.Fatalf("the sealed giving is answered %+v, refused %q, want sam's Google sign-in given", given, refused)
				}
				y.googleLands(t, home, "sam via phone via edge")
			})
			t.Run("sealed in place", func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("HOME", home)
				y := newMemberYard(t, hosted)
				ask := y.samGoogleAsk(t)
				given, refused := y.sealedGive(t, y.inPlaceDevice(t, "sam", "tablet"), giveArgs(t, ask, values))
				if given != nil || refused != memberGoogleRefusalSealed {
					t.Fatalf("the in-place giving is answered %+v, refused %q, want it refused as %q", given, refused, memberGoogleRefusalSealed)
				}
				y.noGoogleFile(t, home, "a device sealed in place")
				// Nothing was spent: the QR-paired device gives it.
				given, refused = y.sealedGive(t, newE2EDevice(t, y.pairYard, "sam", "phone"), giveArgs(t, ask, values))
				if refused != "" || given == nil {
					t.Fatalf("the QR-paired giving after it is answered %+v, refused %q", given, refused)
				}
				y.googleLands(t, home, "sam via phone via edge")
			})
			t.Run("plain", func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("HOME", home)
				y := newMemberYard(t, hosted)
				ask := y.samGoogleAsk(t)
				token := y.plainDevice(t, "sam", "slate")
				given, refused := y.plainGive(t, token, giveArgs(t, ask, values))
				if given != nil || refused != memberGoogleRefusal {
					t.Fatalf("sam's plain giving is answered %+v, refused %q, want it refused as %q", given, refused, memberGoogleRefusal)
				}
				y.noGoogleFile(t, home, "a plain call")
				given, refused = y.sealedGive(t, newE2EDevice(t, y.pairYard, "sam", "phone"), giveArgs(t, ask, values))
				if refused != "" || given == nil {
					t.Fatalf("the sealed giving after it is answered %+v, refused %q", given, refused)
				}
				y.googleLands(t, home, "sam via phone via edge")
			})
			t.Run("another member's ask is not there", func(t *testing.T) {
				t.Setenv("HOME", t.TempDir())
				y := newMemberYard(t, hosted)
				ask := y.samGoogleAsk(t)
				given, refused := y.sealedGive(t, newE2EDevice(t, y.pairYard, "kim", "phone"), giveArgs(t, ask, values))
				if given != nil || refused != noSuchMethodMessage {
					t.Fatalf("kim's giving of sam's ask is answered %+v, refused %q, want it not there", given, refused)
				}
			})
			t.Run("the yard's own ask is not there", func(t *testing.T) {
				t.Setenv("HOME", t.TempDir())
				y := newMemberYard(t, hosted)
				ask := y.ask(t, "jo", &store.AskSecret{Google: "yard@example.com"})
				given, refused := y.sealedGive(t, newE2EDevice(t, y.pairYard, "sam", "phone"), giveArgs(t, ask, values))
				if given != nil || refused != noSuchMethodMessage {
					t.Fatalf("sam's giving of the yard's ask is answered %+v, refused %q, want it not there", given, refused)
				}
			})
			t.Run("the operator standing in is refused", func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("HOME", home)
				y := newMemberYard(t, hosted)
				ask := y.samGoogleAsk(t)
				given, refused := y.sealedGive(t, newE2EDevice(t, y.pairYard, "jo", "laptop"), giveArgs(t, ask, values))
				if given != nil || !strings.Contains(refused, "only sam may") {
					t.Fatalf("the operator's giving of sam's ask is answered %+v, refused %q, want sam's alone", given, refused)
				}
				y.noGoogleFile(t, home, "the operator standing in")
			})
		})
	}
}

// A member's device gives the code of its own sign-in down the link from the
// edge when the call came sealed, on a yard that is not hosted and on a hosted
// one alike: the waiting login is handed it, as it is the operator's from the
// operator's device. A device paired with the QR and one sealed in place are
// sealed. A call that came plain is refused in the one sentence, and is not
// served: the login still waits, and what it is given next from a sealed device
// is taken.
func TestMemberGivesTheirOwnLoginCodeDownTheLink(t *testing.T) {
	for _, hosted := range []bool{false, true} {
		name := "connected yard"
		if hosted {
			name = "hosted yard"
		}
		t.Run(name, func(t *testing.T) {
			t.Run("sealed, paired with the QR", func(t *testing.T) {
				y := newMemberYard(t, hosted)
				y.takes(t, newE2EDevice(t, y.pairYard, "sam", "phone"), "sam via phone via edge")
			})
			t.Run("sealed in place", func(t *testing.T) {
				y := newMemberYard(t, hosted)
				y.takes(t, y.inPlaceDevice(t, "sam", "tablet"), "sam via tablet via edge")
			})
			t.Run("plain", func(t *testing.T) {
				y := newMemberYard(t, hosted)
				token := y.plainDevice(t, "sam", "slate")
				given, refused := y.plainGive(t, token, giveArgs(t, y.login, map[string]string{"INPUT": memberCode}))
				if given != nil || refused != memberRefusal {
					t.Fatalf("sam's plain giving is answered %+v, refused %q, want the one sentence %q", given, refused, memberRefusal)
				}
				y.nothingGiven(t, "a plain call")
				// Nothing was spent: the login waits still, for a sealed call.
				y.takes(t, newE2EDevice(t, y.pairYard, "sam", "phone"), "sam via phone via edge")
			})
		})
	}
}

// Down the link from a member's sealed device only the code of its own sign-in
// is taken: the rest of what a member may give on the socket is refused in the
// one sentence without the tail a plain call is told, a login code of anyone
// else's ask is not there as it is not on the socket, and a body that is
// anything but that ask's values is refused. No refusal serves the call:
// the waiting login is handed nothing, the bead is not answered and sam
// has no file.
func TestMemberGiveSecretDownTheLinkIsOnlyTheirOwnLoginCode(t *testing.T) {
	for _, hosted := range []bool{false, true} {
		name := "connected yard"
		if hosted {
			name = "hosted yard"
		}
		t.Run(name, func(t *testing.T) {
			y := newMemberYard(t, hosted)
			sam := newE2EDevice(t, y.pairYard, "sam", "phone")
			own := map[string]string{"INPUT": memberCode}
			login := func(change ...func(map[string]any)) []byte { return giveArgs(t, y.login, own, change...) }
			for _, c := range []struct {
				name string
				body []byte
				want string // the words it is refused in
			}{
				{"their own source credential", giveArgs(t, y.ask(t, "sam", &store.AskSecret{Source: "crm", As: "sam"}), map[string]string{"CRM_TOKEN": memberCode}), memberRefusalSealed},
				{"their own address", giveArgs(t, y.ask(t, "sam", &store.AskSecret{Source: "push", User: "sam"}), map[string]string{"PUSH_USER": memberCode}), memberRefusalSealed},
				{"another user's login code", giveArgs(t, y.ask(t, "kim", &store.AskSecret{Login: "claude-kim", As: "kim"}), own), noSuchMethodMessage},
				{"the operator's login code", giveArgs(t, y.ask(t, "jo", &store.AskSecret{Login: "claude-op"}), own), noSuchMethodMessage},
				{"an extra value", login(func(a map[string]any) {
					a["values"] = map[string]string{"INPUT": memberCode, "EXTRA_TOKEN": memberCode}
				}), memberRefusalSealed},
				{"no value", login(func(a map[string]any) { a["values"] = map[string]string{} }), memberRefusalSealed},
				{"another name", login(func(a map[string]any) { a["values"] = map[string]string{"OTHER": memberCode} }), memberRefusalSealed},
				{"another actor", login(func(a map[string]any) { a["actor"] = "jo" }), memberRefusalSealed},
				{"an unweighed giving", login(func(a map[string]any) { a["weighed"] = false }), memberRefusalSealed},
				{"an unknown argument", login(func(a map[string]any) { a["extra"] = memberCode }), memberRefusalSealed},
			} {
				t.Run(c.name, func(t *testing.T) {
					given, refused := y.sealedGive(t, sam, c.body)
					if given != nil || refused != c.want {
						t.Errorf("answered %+v, refused %q, want it refused as %q", given, refused, c.want)
					}
					y.nothingGiven(t, c.name)
				})
			}
			// Nothing refused spent the wait: the code of its own is taken.
			y.takes(t, sam, "sam via phone via edge")
		})
	}
	// The operator's sealed device takes every secret here; the member cases
	// above still pin their own login code as the only secret they may give.
	t.Run("the operator's sealed device, on a connected yard", func(t *testing.T) {
		t.Run("a member's login code", func(t *testing.T) {
			y := newMemberYard(t, false)
			y.takes(t, newE2EDevice(t, y.pairYard, "jo", "laptop"), "jo via laptop via edge")
		})
		t.Run("every other ask", func(t *testing.T) {
			const value = "s3cret-0f-the-operator"
			ctx := context.Background()
			y := newMemberYard(t, false)
			for file, decl := range map[string]string{"tracker.yaml": trackerDecl, "linear.yaml": hookDecl} {
				if _, err := y.st.PutSource(ctx, file, decl, "jo"); err != nil {
					t.Fatal(err)
				}
			}
			laptop := newE2EDevice(t, y.pairYard, "jo", "laptop")
			rows := []struct {
				name   string
				sec    *store.AskSecret
				values map[string]string
				names  []string // what the answer says was given
				file   []string // what the file it names holds
			}{
				{"a source's credential", &store.AskSecret{Source: "crm"}, map[string]string{"CRM_TOKEN": value},
					[]string{"CRM_TOKEN"}, []string{"CRM_TOKEN=" + value}},
				{"the two names of a minted credential", &store.AskSecret{Source: "tracker"},
					map[string]string{"TRACKER_CLIENT_ID": "app-1d", "TRACKER_CLIENT_SECRET": value},
					[]string{"TRACKER_CLIENT_ID", "TRACKER_CLIENT_SECRET"}, []string{"TRACKER_CLIENT_ID=app-1d", "TRACKER_CLIENT_SECRET=" + value}},
				{"a harness's API key", &store.AskSecret{Harness: "claude", Crew: "yardmaster"}, map[string]string{"ANTHROPIC_API_KEY": value},
					[]string{"ANTHROPIC_API_KEY"}, []string{"ANTHROPIC_API_KEY=" + value}},
				{"a hook's signing secret", &store.AskSecret{Source: "linear", Hook: "linear"}, map[string]string{"LINEAR_SIGNING_SECRET": value},
					[]string{"LINEAR_SIGNING_SECRET"}, []string{value}},
				{"a user's address", &store.AskSecret{Source: "push", User: "sam"}, map[string]string{"PUSH_USER": value},
					[]string{"PUSH_USER"}, []string{"PUSH_USER=" + value}},
			}
			for _, c := range rows {
				given, refused := y.sealedGive(t, laptop, giveArgs(t, y.ask(t, "jo", c.sec), c.values))
				if refused != "" || given == nil || !slices.Equal(given.Given, c.names) {
					t.Errorf("%s: the sealed giving is answered %+v, refused %q, want %v given", c.name, given, refused, c.names)
					continue
				}
				written, err := os.ReadFile(given.File)
				if err != nil {
					t.Errorf("%s: the file it names: %v", c.name, err)
					continue
				}
				for _, want := range c.file {
					if !strings.Contains(string(written), want) {
						t.Errorf("%s: %s holds %q, want it to hold %q", c.name, given.File, written, want)
					}
				}
			}
			// A device sealed in place gives as one paired with the QR does.
			tablet := y.inPlaceDevice(t, "jo", "tablet")
			if given, refused := y.sealedGive(t, tablet, giveArgs(t, y.ask(t, "jo", &store.AskSecret{Source: "crm"}), map[string]string{"CRM_TOKEN": value})); refused != "" || given == nil {
				t.Errorf("sealed in place: the sealed giving is answered %+v, refused %q", given, refused)
			}
			// Each giving is on the bead as the device's, and none holds the value.
			var want []string
			for range len(rows) {
				want = append(want, "jo via laptop via edge")
			}
			want = append(want, "jo via tablet via edge")
			if got := y.answeredBy(t); !slices.Equal(got, want) {
				t.Errorf("the bead is answered by %q, want %q", got, want)
			}
			notes, err := y.st.Notes(ctx, "yd-crm")
			if err != nil {
				t.Fatal(err)
			}
			for _, n := range notes {
				if strings.Contains(n.Text, value) {
					t.Errorf("note %d holds the value: %q", n.Seq, n.Text)
				}
			}
			// The account a setup types is no giving, and sealing does not make it
			// one: it is refused with the machine as its one fix, and no tail, since
			// the call crossed as ciphertext.
			const typed = "an account typed into an approval never crosses the edge: this request came down the link from the edge. " +
				"Answer on the yard's machine (ssh -t <host> yardr bead answer <bead> <ask> <n> --secret)."
			plain := bindFor("", false)
			_, _, status, _, answer := laptop.call(t, http.MethodPost, "/user/call/ApproveSetup", nil,
				[]byte(`{"bead":"yd-crm","ask":1,"option":1,"values":{"CRM_TOKEN":"`+value+`"},"weighed":true}`), plain, plain)
			if given, refused := giveReply(t, answer); status != http.StatusOK || given != nil || refused != typed {
				t.Errorf("a setup's typed account, sealed, is answered %d %s, want it refused as %q", status, answer, typed)
			}
		})
	})
}

// trackerDecl is a source whose credential is minted from two inputs, an
// application's id and its secret, so the card that asks for them asks for two
// names at once.
const trackerDecl = `name: tracker
base: https://tracker.example.com/api
credential:
  env: TRACKER_TOKEN
  header: "Authorization: Bearer {credential}"
  mint:
    - name: TRACKER_CLIENT_ID
      secret: false
    - TRACKER_CLIENT_SECRET
  mint_command: [tracker-mint]
calls:
  ping: { kind: read, request: { method: GET, path: /me } }
`

// noSuchMethodMessage is the message of a call the yard does not have.
var noSuchMethodMessage = noSuchMethod("GiveSecret").Error.Message

// The ask for the input of a member's own login is taken for no other: the
// predicate compares the whole of the ask with the form the yard stores for
// it, so a field that AskSecret gains later is refused until this test is
// taught it, and not taken because nothing looked at the field.
func TestOwnLoginInputIsTheWholeAsk(t *testing.T) {
	valid := store.AskSecret{Env: []string{"INPUT"}, Login: "claude-sam", As: "sam"}
	if !ownLoginInput(valid, "sam") {
		t.Fatal("the form the yard stores for sam's login input is not taken for it")
	}
	typ := reflect.TypeOf(valid)
	for i := range typ.NumField() {
		name := typ.Field(i).Name
		if name == "Env" || name == "Login" || name == "As" {
			continue
		}
		bad := valid
		field := reflect.ValueOf(&bad).Elem().Field(i)
		switch field.Kind() {
		case reflect.String:
			field.SetString("x")
		case reflect.Bool:
			field.SetBool(true)
		case reflect.Slice:
			field.Set(reflect.Append(reflect.MakeSlice(field.Type(), 0, 1), reflect.Zero(field.Type().Elem())))
		case reflect.Pointer:
			field.Set(reflect.New(field.Type().Elem()))
		default:
			t.Fatalf("AskSecret.%s is a %s: teach this test to set one", name, field.Kind())
		}
		if ownLoginInput(bad, "sam") {
			t.Errorf("an ask with %s set is taken for sam's login input", name)
		}
	}
	for _, c := range []struct {
		name string
		ask  store.AskSecret
		user string
	}{
		{"another user's login", valid, "kim"},
		{"no user", valid, ""},
		{"the operator's form, with no one's own", store.AskSecret{Env: []string{"INPUT"}, Login: "claude-sam"}, "sam"},
		{"the operator's form, for no user", store.AskSecret{Env: []string{"INPUT"}, Login: "claude-sam"}, ""},
		{"no login", store.AskSecret{Env: []string{"INPUT"}, As: "sam"}, "sam"},
		{"another name", store.AskSecret{Env: []string{"OTHER"}, Login: "claude-sam", As: "sam"}, "sam"},
		{"two names", store.AskSecret{Env: []string{"INPUT", "OTHER"}, Login: "claude-sam", As: "sam"}, "sam"},
		{"no name", store.AskSecret{Login: "claude-sam", As: "sam"}, "sam"},
	} {
		if ownLoginInput(c.ask, c.user) {
			t.Errorf("%s: %+v is taken for %q's login input", c.name, c.ask, c.user)
		}
	}
	if valid.As != "sam" {
		t.Error("the predicate changed the ask it was given")
	}
}

// The ask for a member's own Google sign-in is taken for no other: the
// predicate compares the ask with the form the yard stores for it, but
// for the scopes the asker narrowed, so a field that AskSecret gains
// later is refused until this test is taught it, and not taken because
// nothing looked at the field.
func TestGoogleSignInOfIsTheWholeAsk(t *testing.T) {
	valid := store.AskSecret{Env: []string{"GOOGLE_CREDENTIALS"}, Google: "sam@example.com", As: "sam", Scopes: []string{"gmail", "drive"}}
	if !valid.GoogleSignInOf("sam") {
		t.Fatal("the form the yard stores for sam's Google sign-in is not taken for it")
	}
	typ := reflect.TypeOf(valid)
	for i := range typ.NumField() {
		name := typ.Field(i).Name
		if name == "Env" || name == "Google" || name == "As" || name == "Scopes" {
			continue
		}
		bad := valid
		field := reflect.ValueOf(&bad).Elem().Field(i)
		switch field.Kind() {
		case reflect.String:
			field.SetString("x")
		case reflect.Bool:
			field.SetBool(true)
		case reflect.Slice:
			field.Set(reflect.Append(reflect.MakeSlice(field.Type(), 0, 1), reflect.Zero(field.Type().Elem())))
		case reflect.Pointer:
			field.Set(reflect.New(field.Type().Elem()))
		default:
			t.Fatalf("AskSecret.%s is a %s: teach this test to set one", name, field.Kind())
		}
		if bad.GoogleSignInOf("sam") {
			t.Errorf("an ask with %s set is taken for sam's Google sign-in", name)
		}
	}
	for _, c := range []struct {
		name string
		ask  store.AskSecret
		user string
	}{
		{"another user's sign-in", valid, "kim"},
		{"no user", valid, ""},
		{"the yard's form, with no one's own", store.AskSecret{Env: []string{"GOOGLE_CREDENTIALS"}, Google: "sam@example.com", Scopes: []string{"gmail"}}, "sam"},
		{"no account", store.AskSecret{Env: []string{"GOOGLE_CREDENTIALS"}, As: "sam", Scopes: []string{"gmail"}}, "sam"},
		{"another name", store.AskSecret{Env: []string{"OTHER"}, Google: "sam@example.com", As: "sam", Scopes: []string{"gmail"}}, "sam"},
		{"two names", store.AskSecret{Env: []string{"GOOGLE_CREDENTIALS", "OTHER"}, Google: "sam@example.com", As: "sam", Scopes: []string{"gmail"}}, "sam"},
		{"no name", store.AskSecret{Google: "sam@example.com", As: "sam", Scopes: []string{"gmail"}}, "sam"},
		{"no scopes", store.AskSecret{Env: []string{"GOOGLE_CREDENTIALS"}, Google: "sam@example.com", As: "sam"}, "sam"},
		{"a service gws has none of", store.AskSecret{Env: []string{"GOOGLE_CREDENTIALS"}, Google: "sam@example.com", As: "sam", Scopes: []string{"bogus"}}, "sam"},
	} {
		if c.ask.GoogleSignInOf(c.user) {
			t.Errorf("%s: %+v is taken for %q's Google sign-in", c.name, c.ask, c.user)
		}
	}
	if valid.As != "sam" {
		t.Error("the predicate changed the ask it was given")
	}
}
