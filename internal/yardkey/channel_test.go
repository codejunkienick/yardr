package yardkey

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/store"
)

// The channel key is made once, in a file of mode 0600 beside the yard's
// identity, and is the same key every time after; a file open to others
// is refused, not used, and not replaced.
func TestEnsureChannelMakesTheKeyOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "channel.key")
	k, made, err := EnsureChannel(path)
	if err != nil || !made {
		t.Fatalf("EnsureChannel = made %v, %v", made, err)
	}
	if len(k.Private) != 32 || len(k.Public) != 32 {
		t.Fatalf("the channel key is %d private and %d public bytes", len(k.Private), len(k.Public))
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("channel key file mode = %v, %v", st.Mode().Perm(), err)
	}
	again, made, err := EnsureChannel(path)
	if err != nil || made || ChannelPublic(again) != ChannelPublic(k) {
		t.Fatalf("second EnsureChannel = made %v, %v, same key %v", made, err, ChannelPublic(again) == ChannelPublic(k))
	}
	if got, err := LoadChannel(path); err != nil || ChannelPublic(got) != ChannelPublic(k) {
		t.Fatalf("LoadChannel = %v, %v", ChannelPublic(got), err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, fn := range map[string]func() error{
		"LoadChannel":   func() error { _, err := LoadChannel(path); return err },
		"EnsureChannel": func() error { _, _, err := EnsureChannel(path); return err },
	} {
		if err := fn(); err == nil || !strings.Contains(err.Error(), "open to others") {
			t.Errorf("%s of a 0644 channel key file = %v", name, err)
		}
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadChannel(path); err != nil || ChannelPublic(got) != ChannelPublic(k) {
		t.Fatalf("the channel key after the refused reads = %v, %v", ChannelPublic(got), err)
	}
}

// The yard's identity signs its channel public key: the signature checks
// against the yard's public key and the channel key it names, and against
// nothing else.
func TestChannelKeyIsSignedByTheIdentity(t *testing.T) {
	identity := newKey(t)
	channel, err := GenerateChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	sig := SignChannelKey(identity, channel.Public)
	if err := VerifyChannelKey(store.Public(identity), ChannelPublic(channel), sig); err != nil {
		t.Fatalf("VerifyChannelKey = %v", err)
	}
	other, err := GenerateChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string][3]string{
		"another channel key": {store.Public(identity), ChannelPublic(other), sig},
		"another yard":        {store.Public(newKey(t)), ChannelPublic(channel), sig},
		"a signature of junk": {store.Public(identity), ChannelPublic(channel), sig[:len(sig)-2] + "AA"},
		"no channel key":      {store.Public(identity), "not a key", sig},
	} {
		if err := VerifyChannelKey(tc[0], tc[1], tc[2]); !errors.Is(err, store.ErrSignature) {
			t.Errorf("VerifyChannelKey with %s = %v, want ErrSignature", name, err)
		}
	}
}

// A pairing handshake to the yard's channel key carries the code as its
// first payload and proves the device's static key, which the yard pins;
// the answer is sealed to that device alone, so a key that is not the
// device's opens nothing, and a handshake to another yard's key, or bytes
// that are no handshake, are refused before any code is read.
func TestPairingHandshakeSealsTheAnswer(t *testing.T) {
	yard, err := GenerateChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	device, err := GenerateChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	first, opening, err := StartPairingHandshake(device, yard.Public, "K7QM-2XPD")
	if err != nil {
		t.Fatal(err)
	}
	opened, err := OpenPairingHandshake(yard, first)
	if err != nil {
		t.Fatal(err)
	}
	if opened.Code != "K7QM-2XPD" || opened.DeviceKey != ChannelPublic(device) {
		t.Fatalf("the yard opened code %q from device %q", opened.Code, opened.DeviceKey)
	}
	reply, err := opened.SealAnswer([]byte(`{"token":"yardr-dev-x"}`))
	if err != nil {
		t.Fatal(err)
	}
	answer, err := opening.OpenAnswer(reply)
	if err != nil || string(answer) != `{"token":"yardr-dev-x"}` {
		t.Fatalf("the device opened %q, %v", answer, err)
	}

	// The recorded transcript opened with another device's key gives
	// nothing: that key was never the handshake's.
	stranger, err := GenerateChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	_, strangerOpening, err := StartPairingHandshake(stranger, yard.Public, "K7QM-2XPD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := strangerOpening.OpenAnswer(reply); err == nil {
		t.Error("another device's key opened the sealed answer")
	}

	// A handshake made to another yard's key is refused by this yard, as
	// bytes that are no handshake are.
	otherYard, err := GenerateChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	foreign, _, err := StartPairingHandshake(device, otherYard.Public, "K7QM-2XPD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenPairingHandshake(yard, foreign); err == nil {
		t.Error("a handshake to another yard's key opened here")
	}
	if _, err := OpenPairingHandshake(yard, []byte("not a handshake")); err == nil {
		t.Error("bytes that are no handshake opened")
	}
	if _, _, err := StartPairingHandshake(device, []byte("short"), "K7QM-2XPD"); err == nil {
		t.Error("a handshake to a key of no length started")
	}
}
