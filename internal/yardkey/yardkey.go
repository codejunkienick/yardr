// Package yardkey makes the yard's key and signs what yards send each
// other: requests, the link to an edge, and the edge's frames
// (docs/peers.md). The key file, invites and public-key parsing live in
// internal/store, where the store checks a signature itself, and this
// package builds on those. It knows nothing of HTTP servers.
//
// A signature proves who sent a request or an answer and that it is whole.
// It hides nothing: over plain http the body is readable by whoever carries
// it.
package yardkey

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"yardr.dev/yardr/internal/store"
)

// The headers of a request between yards: the sender's public key, by
// which the receiver finds the link, when it was signed (Unix seconds), and
// the signature. An answer carries HeaderSig alone. These are the edge
// link's and a pairing's headers still; a peer message's are the RFC 9421
// pair below (docs/peers.md "The requests").
const (
	HeaderKey  = "Yardr-Peer-Key"
	HeaderTime = "Yardr-Peer-Time"
	HeaderSig  = "Yardr-Peer-Signature"
)

// The headers of a request between peers on the sealed wire: the RFC 9421
// pair, the body's digest, and the two yardr headers the profile covers —
// the intended receiver and the fresh nonce an answer echoes.
const (
	HeaderSigInput      = "Signature-Input"
	HeaderSignature     = "Signature"
	HeaderContentDigest = "Content-Digest"
	HeaderPeerTo        = "Yardr-Peer-To"
	HeaderPeerNonce     = "Yardr-Peer-Nonce"
)

// MaxSkew is how far a request's timestamp may be from the receiver's
// clock, either way, which is also how long a recorded request can be
// played again.
const MaxSkew = 5 * time.Minute

// enc, clip and bodySum are the store's own (internal/store, yardkey.go),
// which this package cannot use unexported: the encoding every signature
// is written in, the shortening of what an error quotes, and the sha256,
// hex, of a body.
var enc = base64.RawURLEncoding

func clip(s string) string {
	if len(s) > 48 {
		return s[:48] + "..."
	}
	return s
}

func bodySum(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// Ensure returns the yard's private key from the file at path, making the
// pair first when there is no file: once, so the key a yard gave out stays
// its key. The file holds the seed, base64, and is read under the rule of
// every file yardr reads a secret from (0600 or stricter, see
// store.ReadSecretFile).
func Ensure(path string) (key ed25519.PrivateKey, made bool, err error) {
	key, err = store.Load(path)
	if !errors.Is(err, fs.ErrNotExist) {
		return key, false, err
	}
	_, key, err = ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, false, err
	}
	// O_EXCL: of two commands making the key at once, one makes it and the
	// other reads that one's.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, fs.ErrExist) {
		key, err = store.Load(path)
		return key, false, err
	} else if err != nil {
		return nil, false, err
	}
	_, werr := f.WriteString(base64.StdEncoding.EncodeToString(key.Seed()) + "\n")
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(path)
		return nil, false, werr
	}
	return key, true, nil
}

// request is what a request's signature covers. to is the receiver's public
// key: a request signed for one peer is then no request to another the
// sender is linked with.
func request(to, method, path, ts string, body []byte) []byte {
	return []byte(strings.Join([]string{"yardr-peer-request-1", to, method, path, ts, bodySum(body)}, "\n"))
}

// SignRequest signs a request to the yard whose public key is to: its
// method, its path with the query, the time and the body's sha256. It
// returns the values of HeaderTime and HeaderSig.
func SignRequest(key ed25519.PrivateKey, to, method, path string, at time.Time, body []byte) (ts, sig string) {
	ts = strconv.FormatInt(at.Unix(), 10)
	return ts, enc.EncodeToString(ed25519.Sign(key, request(to, method, path, ts, body)))
}

// SignPeerRequest signs a peer request in the RFC 9421 profile (store
// signs it; this is its name outside the store): r describes the request
// as sent — method, authority, the path and query of the URL actually
// sent to, the intended receiver and a fresh nonce (store.NewPeerNonce) —
// and body is the sealed envelope that crosses, or nil for a bodyless
// request. It returns the Signature-Input and Signature header values.
func SignPeerRequest(key ed25519.PrivateKey, r *store.PeerRequestSig, body []byte, at time.Time) (input, sig string) {
	return store.SignPeerRequest(key, r, body, at)
}

// VerifyPeerRequest checks a peer request's RFC 9421 signature: from
// signed the request r describes, within its stated validity at now.
func VerifyPeerRequest(from string, r store.PeerRequestSig, input, sig string, now time.Time) error {
	return store.VerifyPeerRequest(from, r, input, sig, now)
}

// SignPeerResponse signs a peer answer in the RFC 9421 profile: its
// status, the digest of body (the sealed envelope) and the nonce of the
// request it answers, beside every component of that request with ;req.
// It returns the Signature-Input, Signature and Content-Digest values;
// the answer's Yardr-Peer-Nonce header is r.Nonce, echoed.
func SignPeerResponse(key ed25519.PrivateKey, status int, r store.PeerRequestSig, body []byte) (input, sig, digest string) {
	return store.SignPeerResponse(key, status, r, body)
}

// VerifyPeerResponse checks a peer answer's RFC 9421 signature, as
// SignPeerResponse made it, over body as it crossed.
func VerifyPeerResponse(from string, status int, r store.PeerRequestSig, nonce, digest, input, sig string, body []byte) error {
	return store.VerifyPeerResponse(from, status, r, nonce, digest, input, sig, body)
}

// SealBody seals a peer body to the yard whose encryption key is toEnc:
// HPKE base mode with the two signing keys as additional data, through
// hpke.NewSender and Sender.Seal (the top-level hpke.Seal takes no such
// data and so cannot carry that binding). OpenBody is its mirror, with
// this yard's encryption private key (EnsureEnc).
func SealBody(fromSign, toSign, toEnc string, plaintext []byte) ([]byte, error) {
	return store.SealBody(fromSign, toSign, toEnc, plaintext)
}

func OpenBody(priv []byte, fromSign, toSign string, envelope []byte) ([]byte, error) {
	return store.OpenBody(priv, fromSign, toSign, envelope)
}

// EnsureEnc returns the yard's encryption private key from the file at
// path (peer-enc.key beside peer.key), making the X25519 pair first when
// there is no file: once, so the key peers pin stays its key. The file
// holds the private bytes, base64, under the same rule as Ensure's.
func EnsureEnc(path string) (priv []byte, pub string, made bool, err error) {
	priv, err = LoadEncBytes(path)
	if err == nil {
		pub, err = store.EncPublicOf(priv)
		return priv, pub, false, err
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, "", false, err
	}
	priv, pub, err = store.GenerateEnc()
	if err != nil {
		return nil, "", false, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, fs.ErrExist) {
		priv, err = LoadEncBytes(path)
		if err != nil {
			return nil, "", false, err
		}
		pub, err = store.EncPublicOf(priv)
		return priv, pub, false, err
	} else if err != nil {
		return nil, "", false, err
	}
	_, werr := f.WriteString(base64.StdEncoding.EncodeToString(priv) + "\n")
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(path)
		return nil, "", false, werr
	}
	return priv, pub, true, nil
}

// LoadEncBytes reads an encryption private key EnsureEnc wrote.
func LoadEncBytes(path string) ([]byte, error) {
	b, p, err := store.ReadSecretFile(path)
	if err != nil {
		return nil, err
	}
	priv, err := base64.StdEncoding.DecodeString(string(b))
	if err != nil || len(priv) != 32 {
		return nil, fmt.Errorf("key file %s does not hold a yard's encryption key", p)
	}
	return priv, nil
}

// VerifyRequest checks that the yard whose public key is from signed this
// request to the yard whose key is to, no more than MaxSkew from now. Every
// failure is store.ErrSignature, with the reason in its text for the
// receiver's own log.
func VerifyRequest(from, to, method, path, ts, sig string, body []byte, now time.Time) error {
	pub, err := store.ParsePublic(from)
	if err != nil {
		return fmt.Errorf("%w: bad key", store.ErrSignature)
	}
	raw, err := enc.DecodeString(sig)
	if err != nil || !ed25519.Verify(pub, request(to, method, path, ts, body), raw) {
		return fmt.Errorf("%w: bad signature", store.ErrSignature)
	}
	// After the signature: the timestamp is then the sender's own.
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: bad timestamp", store.ErrSignature)
	}
	if d := now.Sub(time.Unix(sec, 0)); d > MaxSkew || d < -MaxSkew {
		return fmt.Errorf("%w: timestamp %s off", store.ErrSignature, d.Round(time.Second))
	}
	return nil
}

// The link is the WebSocket a yard that connects to an edge keeps open to
// it. The signed upgrade that opens it can be played again for MaxSkew, so
// the socket is the yard's link only once each end has signed both nonces,
// the yard's own from its upgrade and the edge's from its challenge, under
// a label of the link's own: such a signature is no request's and no
// answer's, and theirs are not it. The two nonces together are the link's
// session, and every request the edge sends down it is signed over the
// session, so a frame recorded from one link is refused on the next.
//
// The edge defines these bytes (yardr-hosted, router/internal/yardkey) and
// this is their copy: the tests pin the vectors the router's tests pin, so
// a change on either side that the other has not made is seen.
const (
	linkLabel  = "yardr edge link v1"
	frameLabel = "yardr edge request v1"
)

// NonceSize is the length of each end's nonce on the link.
const NonceSize = 32

// link is what both ends of a link sign: the two nonces, the yard's first.
func link(yardNonce, edgeNonce []byte) []byte {
	return []byte(strings.Join([]string{linkLabel, enc.EncodeToString(yardNonce), enc.EncodeToString(edgeNonce)}, "\n"))
}

// SignLink signs the two nonces of one link: the edge's challenge, and the
// yard's hello that answers it.
func SignLink(key ed25519.PrivateKey, yardNonce, edgeNonce []byte) string {
	return enc.EncodeToString(ed25519.Sign(key, link(yardNonce, edgeNonce)))
}

// VerifyLink checks that the party whose public key is from signed these
// two nonces. A nonce of any other length is refused: each end draws
// NonceSize random bytes, and a short one would be a guessable session.
func VerifyLink(from string, yardNonce, edgeNonce []byte, sig string) error {
	pub, err := store.ParsePublic(from)
	if err != nil {
		return fmt.Errorf("%w: bad key", store.ErrSignature)
	}
	if len(yardNonce) != NonceSize || len(edgeNonce) != NonceSize {
		return fmt.Errorf("%w: a nonce is not %d bytes", store.ErrSignature, NonceSize)
	}
	raw, err := enc.DecodeString(sig)
	if err != nil || !ed25519.Verify(pub, link(yardNonce, edgeNonce), raw) {
		return fmt.Errorf("%w: bad signature on the link", store.ErrSignature)
	}
	return nil
}

// EncodeNonce writes a nonce as it travels: in the upgrade's header and in
// the challenge. DecodeNonce reads one, and refuses any other length.
func EncodeNonce(n []byte) string { return enc.EncodeToString(n) }

func DecodeNonce(s string) ([]byte, error) {
	b, err := enc.Strict().DecodeString(s)
	if err != nil || len(b) != NonceSize {
		return nil, fmt.Errorf("%q is not a link nonce", clip(s))
	}
	return b, nil
}

// Session is a link's session id: its two nonces, the yard's first.
func Session(yardNonce, edgeNonce []byte) string {
	return enc.EncodeToString(yardNonce) + "." + enc.EncodeToString(edgeNonce)
}

// Frame is what the edge's signature on a request frame covers: the link's
// session and the frame's place in it, the request as the sender made it
// (its headers by their digest, its body by its sha256), when the edge
// received it, and whether it waited in the edge's queue, which chooses the
// clock the yard checks the sender's timestamp against.
type Frame struct {
	Session  string
	Seq      uint64
	ID       string
	Method   string
	Path     string
	Header   map[string][]string
	Body     []byte
	Received string
	Queued   bool
}

// frame is the bytes a request frame's signature covers. None of the
// fields it joins holds a newline (VerifyFrame refuses one that does), so
// no two frames sign as the same text.
func frame(f Frame) []byte {
	return []byte(strings.Join([]string{
		frameLabel, f.Session, strconv.FormatUint(f.Seq, 10), f.ID, f.Method, f.Path,
		HeaderDigest(f.Header), bodySum(f.Body), f.Received, strconv.FormatBool(f.Queued),
	}, "\n"))
}

// SignFrame signs a request frame. The edge signs every one it sends; a
// yard signs none, and a stand-in edge does.
func SignFrame(key ed25519.PrivateKey, f Frame) string {
	return enc.EncodeToString(ed25519.Sign(key, frame(f)))
}

// VerifyFrame checks that the party whose public key is from signed this
// request frame.
func VerifyFrame(from string, f Frame, sig string) error {
	pub, err := store.ParsePublic(from)
	if err != nil {
		return fmt.Errorf("%w: bad key", store.ErrSignature)
	}
	for _, v := range []string{f.Session, f.ID, f.Method, f.Path, f.Received} {
		if strings.ContainsAny(v, "\r\n") {
			return fmt.Errorf("%w: a field holds a line break", store.ErrSignature)
		}
	}
	// HTTP allows neither in a header, and either would let two header
	// maps digest alike.
	seenHeaders := make(map[string]string, len(f.Header))
	for name, values := range f.Header {
		if strings.ContainsAny(name, ":\r\n") {
			return fmt.Errorf("%w: a header name holds a colon or a line break", store.ErrSignature)
		}
		lower := strings.ToLower(name)
		if prior, ok := seenHeaders[lower]; ok && prior != name {
			return fmt.Errorf("%w: header names %q and %q differ only in case", store.ErrSignature, prior, name)
		}
		seenHeaders[lower] = name
		for _, v := range values {
			if strings.ContainsAny(v, "\r\n") {
				return fmt.Errorf("%w: a header value holds a line break", store.ErrSignature)
			}
		}
	}
	raw, err := enc.DecodeString(sig)
	if err != nil || !ed25519.Verify(pub, frame(f), raw) {
		return fmt.Errorf("%w: bad signature on the frame", store.ErrSignature)
	}
	return nil
}

// HeaderDigest is the sha256, hex, of a header map's canonical form: names
// lowercased and sorted, one line name:value for each value, a name's
// values in the order they arrived, the lines joined by newlines. It is
// what makes the headers a frame carries as trustworthy as its body, since
// they hold what the yard verifies a delivery with. A map whose keys differ
// only in case (Go's own never does) lists them by the key as written, so
// the digest is the same however the map is ranged.
func HeaderDigest(h map[string][]string) string {
	names := make([]string, 0, len(h))
	for name := range h {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := strings.ToLower(names[i]), strings.ToLower(names[j])
		if a != b {
			return a < b
		}
		return names[i] < names[j]
	})
	var lines []string
	for _, name := range names {
		lower := strings.ToLower(name)
		for _, v := range h[name] {
			lines = append(lines, lower+":"+v)
		}
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}
