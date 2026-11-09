package store

import (
	"crypto/ecdh"
	"crypto/hpke"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
)

// Bodies between peers are sealed with HPKE (RFC 9180, an IRTF
// Informational RFC and a published, widely implemented specification:
// the Go standard library carries it) in base mode, to the receiver's
// X25519 encryption key (docs/design/2026-10-26-yardr-4687-peering-on-
// standards.md §3.3). info is the protocol alone, so the receiver can
// choose how to open before it knows anything inside; the two signing
// keys, sender first, are the additional authenticated data, which binds
// the direction; the kind and id stay inside the seal. Opening needs the
// receiver's encryption private key, which this package's callers hold as
// the raw X25519 bytes EncPrivate writes and reads.

// HPKEInfo is the one info of every sealed peer body.
const HPKEInfo = "yardr-peer-hpke-1"

// ErrSealed is a body that does not open: sealed to another key, or
// changed since it was sealed.
var ErrSealed = errors.New("the sealed body does not open")

// encEnvelope is what crosses in place of a body.
type encEnvelope struct {
	V   int    `json:"v"`
	Enc string `json:"enc"`
	CT  string `json:"ct"`
}

// GenerateEnc makes an X25519 encryption pair: the private bytes to keep
// in peer-enc.key and the public half yards publish and pin, as Public
// writes a signing key.
func GenerateEnc() (priv []byte, pub string, err error) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	return k.Bytes(), enc.EncodeToString(k.PublicKey().Bytes()), nil
}

// ParseEncPublic reads an encryption public key as GenerateEnc writes it.
func ParseEncPublic(s string) (*ecdh.PublicKey, error) {
	b, err := enc.DecodeString(s)
	if err != nil || len(b) != 32 {
		return nil, fmt.Errorf("%q is no yard's encryption key", clipErr(s))
	}
	pub, err := ecdh.X25519().NewPublicKey(b)
	if err != nil {
		return nil, fmt.Errorf("%q is no yard's encryption key", clipErr(s))
	}
	return pub, nil
}

// EncPublicOf is the public half of an encryption private key, as
// GenerateEnc writes it.
func EncPublicOf(priv []byte) (string, error) {
	k, err := ecdh.X25519().NewPrivateKey(priv)
	if err != nil {
		return "", err
	}
	return enc.EncodeToString(k.PublicKey().Bytes()), nil
}

func encAAD(senderSign, receiverSign string) []byte {
	return []byte(HPKEInfo + "\n" + senderSign + "\n" + receiverSign)
}

// SealBody seals plaintext to the yard whose encryption public key is
// toEnc, from the yard whose signing key is fromSign to the yard whose
// signing key is toSign, and returns the envelope that crosses.
func SealBody(fromSign, toSign, toEnc string, plaintext []byte) ([]byte, error) {
	pub, err := ParseEncPublic(toEnc)
	if err != nil {
		return nil, err
	}
	pk, err := hpke.NewDHKEMPublicKey(pub)
	if err != nil {
		return nil, err
	}
	encBytes, sender, err := hpke.NewSender(pk, hpke.HKDFSHA256(), hpke.ChaCha20Poly1305(), []byte(HPKEInfo))
	if err != nil {
		return nil, err
	}
	ct, err := sender.Seal(encAAD(fromSign, toSign), plaintext)
	if err != nil {
		return nil, err
	}
	return json.Marshal(encEnvelope{V: 2, Enc: enc.EncodeToString(encBytes), CT: enc.EncodeToString(ct)})
}

// OpenBody opens an envelope sealed to this yard: priv is its encryption
// private key, fromSign the sender's signing key and toSign its own. A
// body sealed to another key, for the other direction, or changed,
// is ErrSealed.
func OpenBody(priv []byte, fromSign, toSign string, envelope []byte) ([]byte, error) {
	var env encEnvelope
	if err := json.Unmarshal(envelope, &env); err != nil || env.V != 2 {
		return nil, fmt.Errorf("%w: not a sealed envelope", ErrSealed)
	}
	encBytes, err := enc.DecodeString(env.Enc)
	if err != nil {
		return nil, fmt.Errorf("%w: bad encapsulated key", ErrSealed)
	}
	ct, err := enc.DecodeString(env.CT)
	if err != nil {
		return nil, fmt.Errorf("%w: bad ciphertext", ErrSealed)
	}
	key, err := ecdh.X25519().NewPrivateKey(priv)
	if err != nil {
		return nil, err
	}
	privKey, err := hpke.NewDHKEMPrivateKey(key)
	if err != nil {
		return nil, err
	}
	recipient, err := hpke.NewRecipient(encBytes, privKey, hpke.HKDFSHA256(), hpke.ChaCha20Poly1305(), []byte(HPKEInfo))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSealed, err)
	}
	plain, err := recipient.Open(encAAD(fromSign, toSign), ct)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSealed, err)
	}
	return plain, nil
}

// SealedSize is how large plaintext crosses sealed: the envelope encoding
// of it.
func SealedSize(plain int) int {
	ct := plain + 16 // HPKE's AEAD tag
	overhead := len(`{"v":2,"enc":"","ct":""}`) + enc.EncodedLen(32)
	return overhead + enc.EncodedLen(ct)
}

// SealedFileBound is the wire bound of a file message: the envelope
// encoding of MaxPeerFileBody. The plaintext bounds (MaxPeerFile,
// MaxPeerFileBody, MaxPeerBody) do not move; only what carries them does.
func SealedFileBound() int { return SealedSize(MaxPeerFileBody) }

// ReLinkLine is what peer ping, show and doctor say of a peer stored
// before sealed bodies (no encryption key): the one recovery, in one
// line, said before anything is dialled for it.
func ReLinkLine(name string) string {
	return fmt.Sprintf("%s was linked before sealed bodies: re-link it — yardr peer rm %s, then link it again by its address or its new invite", name, name)
}

// Sealed reports whether the stored link can carry sealed traffic: it
// pins the peer's encryption key.
func (p Peer) Sealed() bool { return p.Enc != "" }

// peerEncPriv is this yard's encryption private key, from peer-enc.key
// beside the signing key file the owning process named (SetYard): the
// store opens a peer's sealed answers itself, as it checks them.
func (s *Store) peerEncPriv() ([]byte, error) {
	if s.yardKeyFile == "" {
		return nil, errors.New("the store was not told this yard's key file, so it can open no sealed answer")
	}
	b, p, err := ReadSecretFile(filepath.Join(filepath.Dir(s.yardKeyFile), "peer-enc.key"))
	if err != nil {
		return nil, err
	}
	priv, err := base64.StdEncoding.DecodeString(string(b))
	if err != nil || len(priv) != 32 {
		return nil, fmt.Errorf("key file %s does not hold a yard's encryption key", p)
	}
	return priv, nil
}

// openPeerAnswer checks a peer's signed answer and opens its sealed body:
// from signed it, for the request req, echoing its nonce.
func (s *Store) openPeerAnswer(from string, status int, req PeerRequestSig, nonce, digest, input, sig string, body []byte) ([]byte, error) {
	if err := VerifyPeerResponse(from, status, req, nonce, digest, input, sig, body); err != nil {
		return nil, err
	}
	priv, err := s.peerEncPriv()
	if err != nil {
		return nil, err
	}
	return OpenBody(priv, from, s.yardKey(), body)
}
