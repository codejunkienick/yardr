package yardkey

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/flynn/noise"
	"golang.org/x/crypto/curve25519"

	"yardr.dev/yardr/internal/store"
)

// The yard's channel key: one X25519 static that stands beside its
// ed25519 identity (Ensure), in its own file of the same kind (0600, the
// base64 of the private half). It is the key a device pins from a
// pairing QR and opens a Noise handshake to (docs/design/
// 2026-11-05-yardr-device-e2e.md, sections 3.1 and 3.2): the identity
// signs its public half (SignChannelKey), so the public half a device
// later learns from the edge can be checked against the identity it
// already holds, and a yard-key change is a re-pairing, as a
// device-token change is. The constructions are flynn/noise's: no yard
// writes its own (the design's question 6).

// channelLabel is what a signature of a channel key covers: the label,
// then the public half as ChannelPublic writes it, so such a signature
// is no request's and no link's.
const channelLabel = "yardr channel key v1"

// ChannelKey is a static X25519 key pair, as noise keeps one: 32 bytes
// a half.
type ChannelKey = noise.DHKey

var channelSuite = noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256)

// GenerateChannelKey makes a fresh channel key pair, for the yard at
// EnsureChannel and for a device at StartPairingHandshake.
func GenerateChannelKey() (ChannelKey, error) {
	return noise.DH25519.GenerateKeypair(rand.Reader)
}

// deriveChannelPublic is the public half of a channel private key:
// Curve25519's base-point multiplication, the derivation DH25519's
// GenerateKeypair performs on the private half it draws.
func deriveChannelPublic(private []byte) ([]byte, error) {
	if len(private) != 32 {
		return nil, fmt.Errorf("a channel private key is 32 bytes, not %d", len(private))
	}
	return curve25519.X25519(private, curve25519.Basepoint)
}

// EnsureChannel returns the yard's channel key from the file at path,
// making the pair first when there is no file: once, so the key a yard
// gave out in a QR stays its key. The file holds the private half,
// base64, and is read under the rule of every file yardr reads a secret
// from (store.ReadSecretFile), as Ensure's is.
func EnsureChannel(path string) (key ChannelKey, made bool, err error) {
	key, err = LoadChannel(path)
	if !errors.Is(err, fs.ErrNotExist) {
		return key, false, err
	}
	key, err = GenerateChannelKey()
	if err != nil {
		return ChannelKey{}, false, err
	}
	// O_EXCL, as Ensure: of two commands making the key at once, one
	// makes it and the other reads that one's.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, fs.ErrExist) {
		key, err = LoadChannel(path)
		return key, false, err
	} else if err != nil {
		return ChannelKey{}, false, err
	}
	_, werr := f.WriteString(base64.StdEncoding.EncodeToString(key.Private) + "\n")
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(path)
		return ChannelKey{}, false, werr
	}
	return key, true, nil
}

// LoadChannel reads a channel key from the file EnsureChannel wrote;
// fs.ErrNotExist when the yard has made none. Errors name the file,
// never what it holds.
func LoadChannel(path string) (ChannelKey, error) {
	b, p, err := store.ReadSecretFile(path)
	if err != nil {
		return ChannelKey{}, err
	}
	private, err := base64.StdEncoding.DecodeString(string(b))
	if err != nil || len(private) != 32 {
		return ChannelKey{}, fmt.Errorf("channel key file %s does not hold a channel key", p)
	}
	pub, err := deriveChannelPublic(private)
	if err != nil {
		return ChannelKey{}, fmt.Errorf("channel key file %s does not hold a channel key", p)
	}
	return ChannelKey{Private: private, Public: pub}, nil
}

// ChannelPublic is the public half of a channel key as it travels: in
// a pairing QR's fourth field and in a device record beside its
// token's hash.
func ChannelPublic(key ChannelKey) string { return enc.EncodeToString(key.Public) }

// ParseChannelPublic reads a channel public key as ChannelPublic
// writes it.
func ParseChannelPublic(s string) ([]byte, error) {
	b, err := enc.Strict().DecodeString(s)
	if err != nil || len(b) != 32 {
		return nil, fmt.Errorf("%q is no channel public key", clip(s))
	}
	return b, nil
}

// channelSigned is what SignChannelKey signs.
func channelSigned(pub []byte) []byte {
	return []byte(strings.Join([]string{channelLabel, enc.EncodeToString(pub)}, "\n"))
}

// SignChannelKey signs a channel public key with the yard's identity:
// the statement a device checks a channel key it did not take from the
// yard's own QR against.
func SignChannelKey(identity ed25519.PrivateKey, pub []byte) string {
	return enc.EncodeToString(ed25519.Sign(identity, channelSigned(pub)))
}

// VerifyChannelKey checks that the yard whose public key is yardPublic
// signed the channel public key channelPublic. Every failure is
// store.ErrSignature, as a request's is.
func VerifyChannelKey(yardPublic, channelPublic, sig string) error {
	pub, err := store.ParsePublic(yardPublic)
	if err != nil {
		return fmt.Errorf("%w: bad key", store.ErrSignature)
	}
	channel, err := ParseChannelPublic(channelPublic)
	if err != nil {
		return fmt.Errorf("%w: bad channel key", store.ErrSignature)
	}
	raw, err := enc.DecodeString(sig)
	if err != nil || !ed25519.Verify(pub, channelSigned(channel), raw) {
		return fmt.Errorf("%w: bad signature on the channel key", store.ErrSignature)
	}
	return nil
}

// A device pairing is redeemed inside a Noise handshake (the design's
// section 3.2, pattern IK): the device pins the yard's channel public
// key from the QR, makes its own key pair for that yard, and sends the
// pairing code as the first message's payload, its static key sealed
// inside that message where only the yard can read it. The yard opens
// the message, redeems the code it carries and seals its answer — the
// token, the user, the device and the yard — as the second message.
// Nothing of the code or the token crosses readable, and a transcript
// recorded and replayed buys nothing: the code the first message
// carries is good once, as any redemption's is, so the replay is
// refused by the pairing and no second device is added; the sealed
// answer opens only with the device private key the handshake proved.

// PairingHandshake is a device pairing's first message as the yard
// opened it: the code it carried and the device key it proved.
type PairingHandshake struct {
	hs        *noise.HandshakeState
	Code      string
	DeviceKey string
}

// OpenPairingHandshake opens the first message of a pairing handshake
// to the yard's channel key. A message that does not open — made to
// another yard's key, or no handshake at all — is refused here, before
// any code is read, so it is no try against the pairing.
func OpenPairingHandshake(yard ChannelKey, first []byte) (*PairingHandshake, error) {
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:   channelSuite,
		Random:        rand.Reader,
		Pattern:       noise.HandshakeIK,
		Initiator:     false,
		StaticKeypair: yard,
	})
	if err != nil {
		return nil, err
	}
	payload, _, _, err := hs.ReadMessage(nil, first)
	if err != nil {
		return nil, fmt.Errorf("a pairing handshake that does not open: %w", err)
	}
	device := hs.PeerStatic()
	if len(device) != 32 {
		return nil, fmt.Errorf("a pairing handshake with no device key")
	}
	return &PairingHandshake{hs: hs, Code: string(payload), DeviceKey: enc.EncodeToString(device)}, nil
}

// SealAnswer seals payload as the handshake's second message: the one
// answer that holds the device's token, readable by the device alone.
func (h *PairingHandshake) SealAnswer(payload []byte) ([]byte, error) {
	reply, _, _, err := h.hs.WriteMessage(nil, payload)
	return reply, err
}

// PairingOpening is a pairing handshake a device has begun: its first
// message sent, its answer still to open.
type PairingOpening struct {
	hs *noise.HandshakeState
}

// StartPairingHandshake begins a pairing handshake to the yard channel
// public key a device pinned (from the pairing QR), with the pairing
// code as the first message's payload: the device's half of a sealed
// redemption, and the stand-in device of the tests'.
func StartPairingHandshake(device ChannelKey, yardPublic []byte, code string) ([]byte, *PairingOpening, error) {
	if len(yardPublic) != 32 {
		return nil, nil, fmt.Errorf("a yard channel public key is 32 bytes, not %d", len(yardPublic))
	}
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:   channelSuite,
		Random:        rand.Reader,
		Pattern:       noise.HandshakeIK,
		Initiator:     true,
		StaticKeypair: device,
		PeerStatic:    yardPublic,
	})
	if err != nil {
		return nil, nil, err
	}
	first, _, _, err := hs.WriteMessage(nil, []byte(code))
	if err != nil {
		return nil, nil, err
	}
	return first, &PairingOpening{hs: hs}, nil
}

// OpenAnswer opens the yard's sealed answer to this opening.
func (o *PairingOpening) OpenAnswer(reply []byte) ([]byte, error) {
	payload, _, _, err := o.hs.ReadMessage(nil, reply)
	return payload, err
}
