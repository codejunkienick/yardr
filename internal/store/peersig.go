package store

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The peer wire's signatures are HTTP Message Signatures (RFC 9421) with
// ed25519, in the profile docs/design/2026-10-26-yardr-4687-peering-on-
// standards.md §3.2 fixes: a request covers its method, the path and query
// of the URL actually sent, the intended receiver (Yardr-Peer-To), a fresh
// nonce (Yardr-Peer-Nonce) and, for a request with a body, the body's
// Content-Digest (RFC 9530); an answer covers its own status, digest and
// echoed nonce beside every component the request covered, each with the
// ;req parameter. @signature-params is the base's last line and is never
// a covered component. This file is the encoding both ends and the store's
// own re-check of an answer share; yardkey wraps it for callers outside
// this package, since yardkey already builds on the store's key parsing
// and the store may not import yardkey back.

const (
	// PeerSigLabel is the one signature label a peer message carries.
	PeerSigLabel = "sig1"
	// PeerReqTag and PeerResTag are the tag parameters of a request and
	// of an answer.
	PeerReqTag = "yardr-peer-req"
	PeerResTag = "yardr-peer-res"
	// PeerValidity is a request signature's stated life: expires is
	// created plus this long (MaxSkew's window, as a validity).
	PeerValidity = 300 * time.Second
)

var stdEnc = base64.StdEncoding

// PeerAuthority returns the RFC 9421 @authority value for a request target.
// The scheme matters because its default port is omitted; the hosted edge
// forwards X-Forwarded-Proto so a yard can recover the external scheme.
func PeerAuthority(scheme, authority string) (string, error) {
	scheme = strings.ToLower(scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("peer authority: unsupported scheme %q", scheme)
	}
	u, err := url.Parse(scheme + "://" + authority)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("peer authority: invalid authority %q", authority)
	}
	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("peer authority: invalid authority %q", authority)
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil {
			return "", fmt.Errorf("peer authority: invalid port in %q", authority)
		}
		if scheme == "http" && n == 80 || scheme == "https" && n == 443 {
			port = ""
		}
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		host = strings.ReplaceAll(addr.String(), "%", "%25")
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
	} else {
		host = strings.ToLower(host)
	}
	if port != "" {
		host += ":" + port
	}
	return host, nil
}

// PeerRequestSig is everything of a peer request its signature covers and
// everything an answer's signature needs of the request it answers: the
// daemon hands it to the store with a pull's answer (PeerBatch) and with
// a post's (PeerAnswer), so the store checks the answer itself.
type PeerRequestSig struct {
	Method    string `json:"method"`
	Authority string `json:"authority"`
	Path      string `json:"path"`
	Query     string `json:"query,omitempty"`  // raw, without "?"
	To        string `json:"to"`               // Yardr-Peer-To: the receiver's signing key
	Nonce     string `json:"nonce"`            // Yardr-Peer-Nonce, fresh per request
	Digest    string `json:"digest,omitempty"` // Content-Digest of the body, "" for none
	Created   int64  `json:"created"`
	Expires   int64  `json:"expires"`
}

// sigComponent is one covered component: its identifier and whether it is
// taken from the request an answer answers (;req).
type sigComponent struct {
	id  string
	req bool
}

func (c sigComponent) String() string {
	if c.req {
		return strconv.Quote(c.id) + ";req"
	}
	return strconv.Quote(c.id)
}

// requestComponents is a request's covered list, in profile order.
func requestComponents(r PeerRequestSig) []sigComponent {
	comps := []sigComponent{{id: "@method"}, {id: "@path"}, {id: "@query"}, {id: "yardr-peer-to"}, {id: "yardr-peer-nonce"}}
	if r.Digest != "" {
		comps = append(comps, sigComponent{id: "content-digest"})
	}
	return comps
}

// responseComponents is an answer's covered list: its own status, digest
// and echoed nonce, then every request component with ;req.
func responseComponents(r PeerRequestSig) []sigComponent {
	comps := []sigComponent{{id: "@status"}, {id: "content-digest"}, {id: "yardr-peer-nonce"},
		{id: "@method", req: true}, {id: "@authority", req: true}, {id: "@path", req: true},
		{id: "yardr-peer-to", req: true}, {id: "yardr-peer-nonce", req: true}, {id: "@query", req: true}}
	if r.Digest != "" {
		comps = append(comps, sigComponent{id: "content-digest", req: true})
	}
	return comps
}

// sigParams writes an inner list with its parameters, as Signature-Input
// carries it and as the base's last line repeats it.
func sigParams(comps []sigComponent, params string) string {
	parts := make([]string, len(comps))
	for i, c := range comps {
		parts[i] = c.String()
	}
	return "(" + strings.Join(parts, " ") + ")" + params
}

// peerSigBase is a signature base (RFC 9421 §2.5): one line per covered
// component, then @signature-params. values is keyed by sigComponent.
func peerSigBase(comps []sigComponent, values map[sigComponent]string, params string) []byte {
	lines := make([]string, 0, len(comps)+1)
	for _, c := range comps {
		lines = append(lines, c.String()+": "+values[c])
	}
	lines = append(lines, `"@signature-params": `+sigParams(comps, params))
	return []byte(strings.Join(lines, "\n"))
}

func queryValue(raw string) string {
	return "?" + raw
}

// requestValues is a request's component values.
func requestValues(r PeerRequestSig) map[sigComponent]string {
	return map[sigComponent]string{
		{id: "@method"}:          r.Method,
		{id: "@path"}:            r.Path,
		{id: "@query"}:           queryValue(r.Query),
		{id: "yardr-peer-to"}:    r.To,
		{id: "yardr-peer-nonce"}: r.Nonce,
		{id: "content-digest"}:   r.Digest,
	}
}

// ContentDigest is a body's Content-Digest header value (RFC 9530,
// sha-256). The signature covers this value, not the body, so a receiver
// can verify from headers alone before reading a large body.
func ContentDigest(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha-256=:" + stdEnc.EncodeToString(sum[:]) + ":"
}

// NewPeerNonce mints a request's fresh nonce: 128 bits, unpadded base64url.
func NewPeerNonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return enc.EncodeToString(b[:]), nil
}

// SignPeerRequest signs the request r describes, with body (nil for a
// bodyless one) under key, at at: it fills r's digest, created and
// expires, and returns the Signature-Input and Signature header values.
func SignPeerRequest(key ed25519.PrivateKey, r *PeerRequestSig, body []byte, at time.Time) (input, sig string) {
	if body != nil {
		r.Digest = ContentDigest(body)
	}
	r.Created = at.Unix()
	r.Expires = r.Created + int64(PeerValidity/time.Second)
	params := fmt.Sprintf(";created=%d;expires=%d;keyid=%s;alg=\"ed25519\";tag=%q", r.Created, r.Expires, strconv.Quote(Public(key)), PeerReqTag)
	comps := requestComponents(*r)
	base := peerSigBase(comps, requestValues(*r), params)
	return PeerSigLabel + "=" + sigParams(comps, params), PeerSigLabel + "=:" + stdEnc.EncodeToString(ed25519.Sign(key, base)) + ":"
}

// parseSigInput reads a Signature-Input value of the one label: the raw
// inner-list-and-parameters text (which the base repeats whole), its
// covered components and its parameters.
func parseSigInput(header string) (raw string, comps []sigComponent, params map[string]string, err error) {
	label, rest, ok := strings.Cut(header, "=")
	if !ok || label != PeerSigLabel {
		return "", nil, nil, fmt.Errorf("%w: no %s signature input", ErrSignature, PeerSigLabel)
	}
	raw = strings.TrimSpace(rest)
	if !strings.HasPrefix(raw, "(") {
		return "", nil, nil, fmt.Errorf("%w: bad signature input", ErrSignature)
	}
	end := strings.Index(raw, ")")
	if end < 0 {
		return "", nil, nil, fmt.Errorf("%w: bad signature input", ErrSignature)
	}
	list, paramText := raw[1:end], raw[end+1:]
	for _, item := range strings.Fields(list) {
		id, p, has := strings.Cut(item, ";")
		unq, uerr := strconv.Unquote(id)
		if uerr != nil {
			return "", nil, nil, fmt.Errorf("%w: bad covered component", ErrSignature)
		}
		if unq == "@signature-params" {
			return "", nil, nil, fmt.Errorf("%w: @signature-params is covered", ErrSignature)
		}
		c := sigComponent{id: unq}
		if has {
			if p != "req" {
				return "", nil, nil, fmt.Errorf("%w: a covered component has a parameter that is not req", ErrSignature)
			}
			c.req = true
		}
		comps = append(comps, c)
	}
	params = map[string]string{}
	for _, p := range strings.Split(paramText, ";") {
		if p == "" {
			continue
		}
		k, v, has := strings.Cut(p, "=")
		if !has {
			return "", nil, nil, fmt.Errorf("%w: a signature parameter has no value", ErrSignature)
		}
		if unq, uerr := strconv.Unquote(v); uerr == nil {
			v = unq
		}
		params[k] = v
	}
	return raw, comps, params, nil
}

// sigValue reads a Signature header's value for the one label.
func sigValue(header string) (string, error) {
	label, rest, ok := strings.Cut(header, "=")
	if !ok || label != PeerSigLabel {
		return "", fmt.Errorf("%w: no %s signature", ErrSignature, PeerSigLabel)
	}
	rest = strings.TrimSpace(rest)
	if len(rest) < 2 || rest[0] != ':' || rest[len(rest)-1] != ':' {
		return "", fmt.Errorf("%w: bad signature value", ErrSignature)
	}
	return rest[1 : len(rest)-1], nil
}

func sameComponents(a, b []sigComponent) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func verifySig(from string, comps []sigComponent, values map[sigComponent]string, raw, header string) error {
	pub, err := ParsePublic(from)
	if err != nil {
		return fmt.Errorf("%w: bad key", ErrSignature)
	}
	sig, err := sigValue(header)
	if err != nil {
		return err
	}
	rawSig, err := stdEnc.DecodeString(sig)
	if err != nil || !ed25519.Verify(pub, peerSigBase(comps, values, raw[strings.Index(raw, ")")+1:]), rawSig) {
		return fmt.Errorf("%w: bad signature", ErrSignature)
	}
	return nil
}

// VerifyPeerRequest checks a request's headers against the request they
// describe: the key that signed it is from, its covered list is the
// profile's for its method and body, its validity holds at now, and its
// signature holds. The receiver checks Yardr-Peer-To against its own key
// itself, and looks the peer up by the keyid it parses unverified first.
func VerifyPeerRequest(from string, r PeerRequestSig, input, sig string, now time.Time) error {
	_, comps, params, err := parseSigInput(input)
	if err != nil {
		return err
	}
	if params["keyid"] != from || params["alg"] != "ed25519" || params["tag"] != PeerReqTag {
		return fmt.Errorf("%w: a signature parameter is not the profile's", ErrSignature)
	}
	if !sameComponents(comps, requestComponents(r)) {
		return fmt.Errorf("%w: the covered components are not the profile's", ErrSignature)
	}
	created, err1 := strconv.ParseInt(params["created"], 10, 64)
	expires, err2 := strconv.ParseInt(params["expires"], 10, 64)
	if err1 != nil || err2 != nil || expires-created != int64(PeerValidity/time.Second) {
		return fmt.Errorf("%w: the signature's validity is not the profile's", ErrSignature)
	}
	raw, _, _, _ := parseSigInput(input)
	if err := verifySig(from, comps, requestValues(r), raw, sig); err != nil {
		return err
	}
	// After the signature: the times are then the sender's own.
	if d := now.Sub(time.Unix(created, 0)); d > PeerValidity || d < -PeerValidity {
		return fmt.Errorf("%w: signature created %s off", ErrSignature, d.Round(time.Second))
	}
	if now.Unix() > expires+int64(PeerValidity/time.Second) {
		return fmt.Errorf("%w: signature expired", ErrSignature)
	}
	return nil
}

// SignPeerResponse signs an answer of status to the request r, with body
// (the sealed envelope as it crosses) under key: the answer echoes r's
// nonce in its own Yardr-Peer-Nonce header, whose value nonce the caller
// sets from r.Nonce. It returns the answer's Signature-Input, Signature
// and Content-Digest header values.
func SignPeerResponse(key ed25519.PrivateKey, status int, r PeerRequestSig, body []byte) (input, sig, digest string) {
	digest = ContentDigest(body)
	params := fmt.Sprintf(";keyid=%s;alg=\"ed25519\";tag=%q", strconv.Quote(Public(key)), PeerResTag)
	comps := responseComponents(r)
	values := responseValues(status, r, digest)
	base := peerSigBase(comps, values, params)
	return PeerSigLabel + "=" + sigParams(comps, params), PeerSigLabel + "=:" + stdEnc.EncodeToString(ed25519.Sign(key, base)) + ":", digest
}

func responseValues(status int, r PeerRequestSig, digest string) map[sigComponent]string {
	return map[sigComponent]string{
		{id: "@status"}:                     strconv.Itoa(status),
		{id: "content-digest"}:              digest,
		{id: "yardr-peer-nonce"}:            r.Nonce,
		{id: "@method", req: true}:          r.Method,
		{id: "@authority", req: true}:       r.Authority,
		{id: "@path", req: true}:            r.Path,
		{id: "@query", req: true}:           queryValue(r.Query),
		{id: "yardr-peer-to", req: true}:    r.To,
		{id: "yardr-peer-nonce", req: true}: r.Nonce,
		{id: "content-digest", req: true}:   r.Digest,
	}
}

// VerifyPeerResponse checks an answer: signed by from, over its status,
// the digest of the body it was given (checked against digest first, so a
// changed body fails here), its echoed nonce (r's, in nonce) and every
// component of the request r with ;req. An answer made for any other
// request — another nonce, method, authority, path, query, digest or
// intended peer — does not verify for this one.
func VerifyPeerResponse(from string, status int, r PeerRequestSig, nonce, digest, input, sig string, body []byte) error {
	if body != nil && ContentDigest(body) != digest {
		return fmt.Errorf("%w: the answer's body is not its signed digest", ErrSignature)
	}
	raw, comps, params, err := parseSigInput(input)
	if err != nil {
		return err
	}
	if params["keyid"] != from || params["tag"] != PeerResTag {
		return fmt.Errorf("%w: a signature parameter is not the profile's", ErrSignature)
	}
	if nonce != r.Nonce {
		return fmt.Errorf("%w: the answer does not echo the request's nonce", ErrSignature)
	}
	if !sameComponents(comps, responseComponents(r)) {
		return fmt.Errorf("%w: the covered components are not the profile's", ErrSignature)
	}
	return verifySig(from, comps, responseValues(status, r, digest), raw, sig)
}

// PeerSigKeyID reads a Signature-Input's keyid without verifying it: how
// a receiver finds which link a request claims before it checks anything.
func PeerSigKeyID(input string) (string, error) {
	_, _, params, err := parseSigInput(input)
	if err != nil {
		return "", err
	}
	if params["keyid"] == "" {
		return "", fmt.Errorf("%w: no keyid", ErrSignature)
	}
	return params["keyid"], nil
}
