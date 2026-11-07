package yardkey

import (
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/backkem/spake2-go"
	"golang.org/x/crypto/chacha20poly1305"

	"yardr.dev/yardr/internal/store"
)

// A typed or read-aloud pairing code is redeemed through a PAKE in
// place of the QR (docs/design/2026-11-05-yardr-device-e2e.md, section
// 3.1 candidate 1A and section 6 bead 16): the device and the yard run
// SPAKE2 with the code as the password, and exchange their static
// channel keys under the key it derives. The edge that relays the run
// — and may rewrite every message of it — gets one online guess a
// run, at the confirmation, and no offline test at all: a recorded
// transcript cannot be tried against candidate codes afterwards,
// which is the failure of the peer pairing's code-keyed HMAC
// (docs/peers.md, "What pairing rests on"). A pairing whose code the
// edge itself handed out is outside this: the router reads and
// returns a member pairing's code, so the edge holds the password
// there, and a member is sealed by the QR's key alone (section 3.1).
//
// The protocol is SPAKE2 as RFC 9382 specifies it, and none of it is
// written here: the run is the backkem spake2-go module's, held
// against the RFC's appendix B test vectors in that module's tests,
// under the same maintained-library rule as the Noise of channel.go
// (the design's question 6). The library states it has had no formal
// audit, and no text of this yard claims one for it.
//
// PAKESuite pins the choice the client beads implement against:
// SPAKE2-P256-SHA256-HKDF-HMAC of RFC 9382 section 6 — the P-256
// group, SHA-256, HKDF-HMAC-SHA256 and HMAC-SHA256, the suite's own
// M and N, identities "A" (the device) and "B" (the yard), and no
// additional authenticated data. The AAD is empty on purpose: this
// library folds a non-empty AAD into the transcript as well as into
// the confirmation KDF, which an RFC client would not do, so a
// non-empty one would pin the clients to this library's variant
// instead of the RFC's suite. The password is the code as
// store.CleanCode leaves it (its dash out, capitals, I and L as 1, O
// as 0), as eight ASCII bytes; w is derived from it as the suite's
// hash derives it. Every length in the transcript is the RFC's
// eight-byte little-endian one, and the derived Ke — the first half
// of the transcript's SHA-256 — is the key the static keys and the
// answer cross under, through pakeSeal: HKDF-SHA256 (no salt, the
// info named at each use) to a ChaCha20-Poly1305 key, a fresh nonce
// before each sealed message.
const PAKESuite = "SPAKE2-P256-SHA256-HKDF-HMAC"

// ErrPAKEConfirmation is a PAKE run whose confirmation does not
// hold: the other side's code was not this side's, or the message
// was rewritten on the way. It is the one failure of a run that
// proves a guess was made, so it is the one the pairing counts.
var ErrPAKEConfirmation = errors.New("the pairing confirmation does not hold")

const (
	pakeDeviceKeyInfo = "yardr pake device key v1"
	pakeAnswerInfo    = "yardr pake answer v1"
)

// pakeOptions is a fresh set of the pinned suite's options, as
// spake2-go's defaults state them: the P-256 suite, the identities
// above and no AAD.
func pakeOptions() *spake2.Options {
	return spake2.DefaultOptions()
}

// pakePassword is a pairing code, as a device typed it or as the
// yard holds it, in the form both sides run the PAKE on: the
// cleaned code's eight ASCII bytes.
func pakePassword(code string) ([]byte, error) {
	clean, err := store.CleanCode(code)
	if err != nil {
		return nil, err
	}
	return []byte(clean), nil
}

// pakeSeal seals plain under a key derived from the PAKE's Ke for
// info: the nonce, then the sealed bytes.
func pakeSeal(ke []byte, info string, plain []byte) ([]byte, error) {
	aead, err := pakeAEAD(ke, info)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, plain, nil), nil
}

// pakeOpen opens what pakeSeal sealed for info.
func pakeOpen(ke []byte, info string, sealed []byte) ([]byte, error) {
	aead, err := pakeAEAD(ke, info)
	if err != nil {
		return nil, err
	}
	if len(sealed) < aead.NonceSize() {
		return nil, fmt.Errorf("a sealed PAKE message of %d bytes holds no nonce", len(sealed))
	}
	return aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], nil)
}

// pakePoint refuses a SPAKE2 message that is no point of the pinned
// suite, before the library is handed it: ecdh takes only the 65-byte
// uncompressed encoding of a point on P-256 and refuses the point at
// infinity, where the library would take a lone byte or a prefix and
// zeros as that point, and index past the end of an empty message and
// panic. A message refused here is no guess at the code.
func pakePoint(msg []byte) error {
	if _, err := ecdh.P256().NewPublicKey(msg); err != nil {
		return fmt.Errorf("a pairing PAKE message of %d bytes is no point of %s", len(msg), PAKESuite)
	}
	return nil
}

func pakeAEAD(ke []byte, info string) (cipher.AEAD, error) {
	key, err := hkdf.Key(sha256.New, ke, nil, info, chacha20poly1305.KeySize)
	if err != nil {
		return nil, err
	}
	return chacha20poly1305.New(key)
}

// PairingPAKEOpening is a typed-code pairing a device has begun: its
// first message sent, its confirmation still to make. It is the
// device's half of a PAKE redemption, and the stand-in device of the
// tests', as PairingOpening is of a sealed one.
type PairingPAKEOpening struct {
	run    *spake2.SPAKE2
	device ChannelKey
}

// StartPairingPAKE begins a PAKE run for the device key on the
// pairing code, as the device typed it, and answers its first
// message: the blinded P-256 point of the pinned suite, 65 bytes
// uncompressed. The device's own channel key crosses later, sealed
// under the derived key, in SealKey.
func StartPairingPAKE(device ChannelKey, code string) ([]byte, *PairingPAKEOpening, error) {
	password, err := pakePassword(code)
	if err != nil {
		return nil, nil, err
	}
	run := spake2.NewClient(password, pakeOptions())
	first, err := run.Start()
	if err != nil {
		return nil, nil, err
	}
	return first, &PairingPAKEOpening{run: run, device: device}, nil
}

// Finish takes the yard's second message and answers the device's
// confirmation of the run: 32 bytes, the HMAC of the transcript
// under its confirmation key (RFC 9382, section 4).
func (o *PairingPAKEOpening) Finish(second []byte) ([]byte, error) {
	if err := pakePoint(second); err != nil {
		return nil, err
	}
	confirm, err := o.run.Finish(second)
	if err != nil {
		return nil, fmt.Errorf("a pairing PAKE second message that does not open: %w", err)
	}
	return confirm, nil
}

// Verify checks the yard's confirmation of the run. Only a run that
// passes it has a yard at its other end that holds the code: until
// then the device seals nothing and trusts nothing it is answered.
func (o *PairingPAKEOpening) Verify(yardConfirm []byte) error {
	if err := o.run.Verify(yardConfirm); err != nil {
		if errors.Is(err, spake2.ErrInvalidConfirmation) {
			return fmt.Errorf("%w: %w", ErrPAKEConfirmation, err)
		}
		return fmt.Errorf("a pairing PAKE confirmation could not be checked: %w", err)
	}
	return nil
}

// SealKey seals the device's channel public key under the run's
// derived key, for the yard to record beside the token it issues, as
// a sealed redemption records the key its handshake proved.
func (o *PairingPAKEOpening) SealKey() ([]byte, error) {
	ke, err := o.run.SharedKey()
	if err != nil {
		return nil, err
	}
	return pakeSeal(ke, pakeDeviceKeyInfo, o.device.Public)
}

// OpenAnswer opens the yard's sealed answer to this run: the Paired
// JSON a sealed redemption answers, with the yard's channel public
// key beside it, for the device to pin.
func (o *PairingPAKEOpening) OpenAnswer(sealed []byte) ([]byte, error) {
	ke, err := o.run.SharedKey()
	if err != nil {
		return nil, err
	}
	plain, err := pakeOpen(ke, pakeAnswerInfo, sealed)
	if err != nil {
		return nil, fmt.Errorf("a pairing PAKE answer that does not open: %w", err)
	}
	return plain, nil
}

// PairingPAKEResponder is a typed-code pairing the yard has
// answered: its second message sent, the device's confirmation
// still to check.
type PairingPAKEResponder struct {
	run *spake2.SPAKE2
}

// AnswerPairingPAKE answers a PAKE run's first message with the
// yard's second, on the code of the pairing it has open. A first
// message that is no point of the suite is refused here, before any
// confirmation can be tried against it: it is no guess at the code.
func AnswerPairingPAKE(code string, first []byte) ([]byte, *PairingPAKEResponder, error) {
	if err := pakePoint(first); err != nil {
		return nil, nil, err
	}
	password, err := pakePassword(code)
	if err != nil {
		return nil, nil, err
	}
	run := spake2.NewServer(password, pakeOptions())
	second, err := run.Exchange(first)
	if err != nil {
		return nil, nil, fmt.Errorf("a pairing PAKE first message that does not open: %w", err)
	}
	return second, &PairingPAKEResponder{run: run}, nil
}

// Confirm checks the device's confirmation of the run and answers
// the yard's. A confirmation that does not hold is
// ErrPAKEConfirmation: one online guess at the code, spent. The
// device key is opened only after this passes, and the answer is
// sealed only in a run that passed it.
func (r *PairingPAKEResponder) Confirm(confirm []byte) ([]byte, error) {
	yardConfirm, err := r.run.Confirm(confirm)
	if err != nil {
		if errors.Is(err, spake2.ErrInvalidConfirmation) {
			return nil, fmt.Errorf("%w: %w", ErrPAKEConfirmation, err)
		}
		return nil, fmt.Errorf("a pairing PAKE confirmation could not be checked: %w", err)
	}
	return yardConfirm, nil
}

// OpenKey opens the device's sealed channel public key in a
// confirmed run, as ChannelPublic writes it: the key the device is
// recorded with.
func (r *PairingPAKEResponder) OpenKey(sealed []byte) (string, error) {
	ke, err := r.run.SharedKey()
	if err != nil {
		return "", err
	}
	plain, err := pakeOpen(ke, pakeDeviceKeyInfo, sealed)
	if err != nil {
		return "", fmt.Errorf("a pairing PAKE device key that does not open: %w", err)
	}
	if len(plain) != 32 {
		return "", fmt.Errorf("a pairing PAKE device key of %d bytes is no channel public key", len(plain))
	}
	return enc.EncodeToString(plain), nil
}

// SealAnswer seals payload as a confirmed run's answer: the Paired
// JSON of a redemption, readable by the device that ran it alone.
func (r *PairingPAKEResponder) SealAnswer(payload []byte) ([]byte, error) {
	ke, err := r.run.SharedKey()
	if err != nil {
		return nil, err
	}
	return pakeSeal(ke, pakeAnswerInfo, payload)
}
