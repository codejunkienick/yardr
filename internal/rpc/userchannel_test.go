package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/yardkey"
)

// registerPath is where a device makes RegisterChannelKey.
const registerPath = "/user/call/RegisterChannelKey"

// registered reads a call's reply as RegisterChannelKey answers: its
// result, or the error's code ("" on a result) and message. A reply
// that is no JSON object is answered as code "http" with the body.
func registered(t *testing.T, status int, body string) (code, message string, got ChannelRegistered) {
	t.Helper()
	var reply struct {
		Result *ChannelRegistered `json:"result"`
		Error  *wireError         `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &reply); err != nil {
		return "http", fmt.Sprintf("%d %s", status, body), got
	}
	if reply.Error != nil {
		return reply.Error.Code, reply.Error.Message, got
	}
	if reply.Result == nil {
		t.Fatalf("RegisterChannelKey answered %d %s: neither a result nor an error", status, body)
	}
	return "", "", *reply.Result
}

// linkRegister makes a device's plain RegisterChannelKey down the
// simulated link, with its token, as the edge would carry it.
func (y *pairYard) linkRegister(t *testing.T, token, key string) (code, message string, got ChannelRegistered) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"channel_key": key})
	r := httptest.NewRequest(http.MethodPost, registerPath, strings.NewReader(string(body)))
	r.RemoteAddr = linkRemoteAddr
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	y.u.Handler().ServeHTTP(w, r)
	return registered(t, w.Code, strings.TrimSpace(w.Body.String()))
}

// listenerRegister is linkRegister on the yard's own listener.
func (y *pairYard) listenerRegister(t *testing.T, token, key string) (code, message string, got ChannelRegistered) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"channel_key": key})
	status, answer := y.post(t, registerPath, token, string(body))
	return registered(t, status, answer)
}

// pinnedAs is how the device user/name is pinned, as the list says.
func (y *pairYard) pinnedAs(t *testing.T, user, name string) (key, pinned string) {
	t.Helper()
	list, err := y.st.Devices(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range list {
		if d.Name == name {
			return d.ChannelKey, d.ChannelPinned
		}
	}
	t.Fatalf("%s has no device %s", user, name)
	return "", ""
}

func newChannelKey(t *testing.T) yardkey.ChannelKey {
	t.Helper()
	k, err := yardkey.GenerateChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// A device paired before the channel registers its key in place by a
// plain RegisterChannelKey down the link, its token the proof: the
// record pins the key in place, the answer is the yard's channel key
// signed by its identity, and from then on the device handshakes with
// that key and calls sealed as a QR-paired device does. A second
// registration is refused whatever key it sends, plain or sealed, and
// so are a key that is none and one another device pins; no refusal
// changes the record.
func TestRegisterChannelKeyInPlace(t *testing.T) {
	y := newPairYard(t)
	ctx := context.Background()
	phone := newE2EDevice(t, y, "jo", "phone")
	tablet, err := y.st.AddDevice(ctx, "sam", "tablet", "jo")
	if err != nil {
		t.Fatal(err)
	}
	key := newChannelKey(t)

	for _, c := range []struct {
		name, key, code, says string
	}{
		{"a key that is none", "not a key", "error", "is no channel public key"},
		{"no key", "", "error", "is no channel public key"},
		{"the phone's key", yardkey.ChannelPublic(phone.key), "conflict", "pinned by another device"},
	} {
		if code, msg, _ := y.linkRegister(t, tablet.Token, c.key); code != c.code || !strings.Contains(msg, c.says) {
			t.Errorf("%s = %s %s, want %s saying %q", c.name, code, msg, c.code, c.says)
		}
	}
	if code, msg, _ := y.linkRegister(t, "yardr-dev-nobody", yardkey.ChannelPublic(key)); code != "http" || !strings.HasPrefix(msg, "401 ") {
		t.Errorf("a token of no device = %s %s, want 401", code, msg)
	}
	if k, pinned := y.pinnedAs(t, "sam", "tablet"); k != "" || pinned != "" {
		t.Fatalf("a refusal pinned %q (%s)", k, pinned)
	}

	code, msg, got := y.linkRegister(t, tablet.Token, yardkey.ChannelPublic(key))
	if code != "" {
		t.Fatalf("the registration down the link = %s %s", code, msg)
	}
	if got.User != "sam" || got.Device != "tablet" || got.ChannelKey != yardkey.ChannelPublic(y.channel) || got.YardKey != store.Public(y.identity) {
		t.Fatalf("the answer = %+v, want sam's tablet, the yard's channel key and its identity", got)
	}
	if err := yardkey.VerifyChannelKey(store.Public(y.identity), got.ChannelKey, got.ChannelSignature); err != nil {
		t.Fatalf("the answer's signature does not verify against the yard's identity: %v", err)
	}
	if k, pinned := y.pinnedAs(t, "sam", "tablet"); k != yardkey.ChannelPublic(key) || pinned != store.PinnedInPlace {
		t.Fatalf("the tablet pins %q (%s), want its key in place", k, pinned)
	}

	// The migrated key founds a channel session for the tablet, and its
	// sealed calls are served as a QR-paired device's: as its user, and
	// with a voucher on request.
	link := y.u.Handler()
	migrated := (&e2eDevice{y: y, key: key, handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = linkRemoteAddr
		link.ServeHTTP(w, r)
	})}).rehandshake(t)
	plain := bindFor("", false)
	if outer, _, status, _, body := migrated.call(t, http.MethodGet, "/user/me", nil, nil, plain, plain); outer != http.StatusOK || status != http.StatusOK ||
		!strings.Contains(string(body), `"user":"sam","said_by":"tablet"`) {
		t.Fatalf("the migrated tablet's sealed /user/me = outer %d, inner %d %s", outer, status, body)
	}
	if outer, _, status, _, body := migrated.call(t, http.MethodPost, "/user/push/voucher", nil, nil, plain, plain); outer != http.StatusOK || status != http.StatusOK {
		t.Fatalf("the migrated tablet's sealed voucher = outer %d, inner %d %s", outer, status, body)
	}

	// Once pinned, every registration is refused: the same key, another,
	// down the link, on the listener, and sealed.
	sealed := func(d *e2eDevice) (string, string, ChannelRegistered) {
		args, _ := json.Marshal(map[string]string{"channel_key": yardkey.ChannelPublic(newChannelKey(t))})
		_, _, status, _, body := d.call(t, http.MethodPost, registerPath, nil, args, plain, plain)
		return registered(t, status, string(body))
	}
	for _, again := range []struct {
		name string
		send func() (string, string, ChannelRegistered)
	}{
		{"the same key down the link", func() (string, string, ChannelRegistered) {
			return y.linkRegister(t, tablet.Token, yardkey.ChannelPublic(key))
		}},
		{"another key down the link", func() (string, string, ChannelRegistered) {
			return y.linkRegister(t, tablet.Token, yardkey.ChannelPublic(newChannelKey(t)))
		}},
		{"another key on the listener", func() (string, string, ChannelRegistered) {
			return y.listenerRegister(t, tablet.Token, yardkey.ChannelPublic(newChannelKey(t)))
		}},
		{"the tablet sealed", func() (string, string, ChannelRegistered) { return sealed(migrated) }},
		{"the QR-paired phone sealed", func() (string, string, ChannelRegistered) { return sealed(phone) }},
	} {
		if code, msg, _ := again.send(); code != "conflict" || !strings.Contains(msg, "pins a channel key already") {
			t.Errorf("%s = %s %s, want conflict", again.name, code, msg)
		}
	}
	if k, pinned := y.pinnedAs(t, "sam", "tablet"); k != yardkey.ChannelPublic(key) || pinned != store.PinnedInPlace {
		t.Fatalf("after the refusals the tablet pins %q (%s), want its first key in place", k, pinned)
	}
	if k, pinned := y.pinnedAs(t, "jo", "phone"); k != yardkey.ChannelPublic(phone.key) || pinned != store.PinnedPaired {
		t.Fatalf("the phone pins %q (%s), want its paired key", k, pinned)
	}

	// On the yard's own listener a device registers as down the link.
	laptop, err := y.st.AddDevice(ctx, "jo", "laptop", "jo")
	if err != nil {
		t.Fatal(err)
	}
	if code, msg, got := y.listenerRegister(t, laptop.Token, yardkey.ChannelPublic(newChannelKey(t))); code != "" || got.Device != "laptop" {
		t.Fatalf("the registration on the listener = %s %s %+v", code, msg, got)
	}
}

// The yard's channel key is had before a key is pinned: one not yet
// made is made, and a yard that holds none and cannot make one, or no
// identity to sign it with, refuses and says why, pinning nothing.
func TestRegisterChannelKeyNeedsTheYardsKeys(t *testing.T) {
	y := newPairYard(t)
	ctx := context.Background()
	tablet, err := y.st.AddDevice(ctx, "sam", "tablet", "jo")
	if err != nil {
		t.Fatal(err)
	}
	key := yardkey.ChannelPublic(newChannelKey(t))
	dir := t.TempDir()
	for _, c := range []struct {
		name, channel, identity, says string
	}{
		{"no channel key file told", "", "keep", "holds no channel key and cannot make one"},
		{"a channel key file that cannot be made", filepath.Join(dir, "no", "such", "dir", "channel.key"), "keep", "could not read or make its channel key"},
		{"no identity key file told", "keep", "", "holds no identity key"},
		{"an identity key file that cannot be read", "keep", filepath.Join(dir, "no", "identity.key"), "could not read its identity key"},
	} {
		t.Run(c.name, func(t *testing.T) {
			channelPath, identityPath := y.u.channelPath, y.u.identityPath
			t.Cleanup(func() { y.u.channelPath, y.u.identityPath = channelPath, identityPath })
			if c.channel != "keep" {
				y.u.SetChannelKeyFile(c.channel)
			}
			if c.identity != "keep" {
				y.u.SetIdentityKeyFile(c.identity)
			}
			if code, msg, _ := y.linkRegister(t, tablet.Token, key); code != "error" || !strings.Contains(msg, c.says) {
				t.Fatalf("the registration = %s %s, want refused saying %q", code, msg, c.says)
			} else {
				for _, path := range []string{c.channel, c.identity} {
					if path != "" && path != "keep" && strings.Contains(msg, path) {
						t.Fatalf("the refusal exposed the yard's key file path %q: %s", path, msg)
					}
				}
			}
			if k, _ := y.pinnedAs(t, "sam", "tablet"); k != "" {
				t.Fatalf("a refused registration pinned %q", k)
			}
		})
	}

	fresh := filepath.Join(dir, "channel.key")
	y.u.SetChannelKeyFile(fresh)
	code, msg, got := y.linkRegister(t, tablet.Token, key)
	if code != "" {
		t.Fatalf("the registration where the channel key is yet to be made = %s %s", code, msg)
	}
	made, err := yardkey.LoadChannel(fresh)
	if err != nil || got.ChannelKey != yardkey.ChannelPublic(made) {
		t.Fatalf("the answer %+v does not carry the key made (%v)", got, err)
	}
}

// RegisterChannelKey is the one plain device call named for the
// refusal of plain calls down the link to read, and the socket, which
// has no device token, does not serve it.
func TestRegisterChannelKeyIsThePlainException(t *testing.T) {
	if len(PlainDeviceCallsDown) != 1 || PlainDeviceCallsDown[0] != "RegisterChannelKey" || !listenerOnly["RegisterChannelKey"] {
		t.Fatalf("PlainDeviceCallsDown = %v, listenerOnly = %v: want RegisterChannelKey alone, served on the listener alone", PlainDeviceCallsDown, listenerOnly)
	}
	if _, ok := newAPI(nil, StreamWriteTimeout).methods["RegisterChannelKey"]; ok {
		t.Fatal("the socket serves RegisterChannelKey")
	}
}
