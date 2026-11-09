package store

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	httpsig "github.com/sixfathoms/httpsig/golang"
)

// The RFC 9421 Appendix B.2.6 ed25519 vector verifies against the base
// this package builds: the published key, base and signature, none of
// them yardr's.
func TestPeerSigRFC9421Vector(t *testing.T) {
	pub, err := ParsePublic("JrQLj5P_89iXES9-vFgrIy29clF9CC_oPPsw3c5D0bs")
	if err != nil {
		t.Fatal(err)
	}
	comps := []sigComponent{{id: "date"}, {id: "@method"}, {id: "@path"}, {id: "@authority"}, {id: "content-type"}, {id: "content-length"}}
	values := map[sigComponent]string{
		{id: "date"}:           "Tue, 20 Apr 2021 02:07:55 GMT",
		{id: "@method"}:        "POST",
		{id: "@path"}:          "/foo",
		{id: "@authority"}:     "example.com",
		{id: "content-type"}:   "application/json",
		{id: "content-length"}: "18",
	}
	base := peerSigBase(comps, values, `;created=1618884473;keyid="test-key-ed25519"`)
	want := "\"date\": Tue, 20 Apr 2021 02:07:55 GMT\n\"@method\": POST\n\"@path\": /foo\n\"@authority\": example.com\n\"content-type\": application/json\n\"content-length\": 18\n\"@signature-params\": (\"date\" \"@method\" \"@path\" \"@authority\" \"content-type\" \"content-length\");created=1618884473;keyid=\"test-key-ed25519\""
	if string(base) != want {
		t.Fatalf("base =\n%s\nwant:\n%s", base, want)
	}
	sig, err := base64.StdEncoding.DecodeString("wqcAqbmYJ2ji2glfAMaRy4gruYYnx2nEFN2HN6jrnDnQCK1u02Gb04v9EDgwUPiu4A0w6vuQv5lIp5WPpBKRCw==")
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(pub, base, sig) {
		t.Fatal("the RFC 9421 B.2.6 signature does not verify over the base built here")
	}
}

// A request signed with a base built by hand from the RFC's text (not by
// SignPeerRequest) verifies, and the profile's refusals hold: another
// receiver, a tampered body digest, a replayed answer for a new nonce.
func TestPeerProfileAgainstHandBuiltBase(t *testing.T) {
	for _, tc := range []struct {
		scheme, authority, want string
	}{
		{"https", "EXAMPLE.com:443", "example.com"},
		{"http", "EXAMPLE.com:80", "example.com"},
		{"https", "EXAMPLE.com:8443", "example.com:8443"},
		{"http", "[2001:DB8::1]:80", "[2001:db8::1]"},
	} {
		got, err := PeerAuthority(tc.scheme, tc.authority)
		if err != nil || got != tc.want {
			t.Errorf("PeerAuthority(%q, %q) = %q, %v; want %q", tc.scheme, tc.authority, got, err, tc.want)
		}
	}
	// Use a deterministic key so the hand-built signer is stdlib alone.
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i + 1)
	}
	signer := ed25519.NewKeyFromSeed(seed)
	from := Public(signer)
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	body := []byte(`{"v":2,"enc":"x","ct":"y"}`)
	digest := ContentDigest(body)
	authority, err := PeerAuthority("https", "EXAMPLE.com:443")
	if err != nil {
		t.Fatal(err)
	}
	r := PeerRequestSig{Method: "POST", Authority: authority, Path: "/peers/messages", To: from, Nonce: "abc123nonce", Digest: digest, Created: now.Unix(), Expires: now.Unix() + 300}
	// Hand-built base, written from RFC 9421 §2.5 and the profile, signed
	// with crypto/ed25519 alone: the non-yardr check of VerifyPeerRequest.
	params := `;created=` + strconv.FormatInt(now.Unix(), 10) + `;expires=` + strconv.FormatInt(now.Unix()+300, 10) + `;keyid="` + from + `";alg="ed25519";tag="yardr-peer-req"`
	base := "\"@method\": POST\n\"@path\": /peers/messages\n\"@query\": ?\n\"yardr-peer-to\": " + from + "\n\"yardr-peer-nonce\": abc123nonce\n\"content-digest\": " + digest + "\n\"@signature-params\": (\"@method\" \"@path\" \"@query\" \"yardr-peer-to\" \"yardr-peer-nonce\" \"content-digest\")" + params
	input := `sig1=("@method" "@path" "@query" "yardr-peer-to" "yardr-peer-nonce" "content-digest")` + params
	sig := "sig1=:" + base64.StdEncoding.EncodeToString(ed25519.Sign(signer, []byte(base))) + ":"
	if err := VerifyPeerRequest(from, r, input, sig, now); err != nil {
		t.Fatalf("hand-built request: %v", err)
	}
	// The profile's encoder agrees with the independent RFC 9421 library,
	// including the required "?" value for an absent query component.
	ours, ourSig := SignPeerRequest(signer, &r, body, now)
	if ours != input || ourSig != sig {
		t.Fatalf("peer request differs from the hand-built signature: input %q, signature matches %v", ours, ourSig == sig)
	}
	peerMessage, err := http.NewRequest(r.Method, "https://EXAMPLE.com:443"+r.Path, nil)
	if err != nil {
		t.Fatal(err)
	}
	peerMessage.Header.Set("Yardr-Peer-To", r.To)
	peerMessage.Header.Set("Yardr-Peer-Nonce", r.Nonce)
	peerMessage.Header.Set("Content-Digest", r.Digest)
	peerMessage.Header.Set("Signature-Input", ours)
	peerMessage.Header.Set("Signature", ourSig)
	verifyKey := httpsig.KeyProvider(func(keyID string, alg httpsig.Algorithm) (httpsig.VerifyingKey, error) {
		if keyID != from || alg != httpsig.AlgorithmEd25519 {
			return nil, fmt.Errorf("unexpected key %q or algorithm %q", keyID, alg)
		}
		return httpsig.NewEd25519VerifyingKey(keyID, signer.Public().(ed25519.PublicKey)), nil
	})
	requestRequirements := &httpsig.SignatureRequirements{
		Components: []httpsig.ComponentIdentifier{
			httpsig.Component("@method"), httpsig.Component("@path"), httpsig.Component("@query"),
			httpsig.Component("yardr-peer-to"), httpsig.Component("yardr-peer-nonce"), httpsig.Component("content-digest"),
		},
		KeyID: from, Algorithm: httpsig.AlgorithmEd25519, Tag: PeerReqTag, RequireCreated: true, RequireExpires: true,
	}
	requestMessage := &httpsig.RequestMessage{Req: peerMessage}
	if _, err := httpsig.VerifyMessage(requestMessage, verifyKey, &httpsig.VerifyOptions{
		Requirements: requestRequirements, RequiredLabel: PeerSigLabel, MaxAge: PeerValidity,
		MaxClockSkew: PeerValidity, Now: func() time.Time { return now },
	}, nil); err != nil {
		t.Fatalf("independent RFC 9421 library rejected the request: %v", err)
	}
	answerInput, answerSig, answerDigest := SignPeerResponse(signer, 200, r, body)
	answer := &http.Response{StatusCode: 200, Header: make(http.Header)}
	answer.Header.Set("Content-Digest", answerDigest)
	answer.Header.Set("Yardr-Peer-Nonce", r.Nonce)
	answer.Header.Set("Signature-Input", answerInput)
	answer.Header.Set("Signature", answerSig)
	responseRequirements := &httpsig.SignatureRequirements{
		Components: []httpsig.ComponentIdentifier{
			httpsig.Component("@status"), httpsig.Component("content-digest"), httpsig.Component("yardr-peer-nonce"),
			httpsig.ComponentReq("@method"), httpsig.ComponentReq("@authority"), httpsig.ComponentReq("@path"),
			httpsig.ComponentReq("yardr-peer-to"), httpsig.ComponentReq("yardr-peer-nonce"),
			httpsig.ComponentReq("@query"), httpsig.ComponentReq("content-digest"),
		},
		KeyID: from, Algorithm: httpsig.AlgorithmEd25519, Tag: PeerResTag,
	}
	if _, err := httpsig.VerifyMessage(&httpsig.ResponseMessage{Resp: answer, Req: peerMessage}, verifyKey, &httpsig.VerifyOptions{
		Requirements: responseRequirements, RequiredLabel: PeerSigLabel,
	}, requestMessage); err != nil {
		t.Fatalf("independent RFC 9421 library rejected the answer: %v", err)
	}
	bad := r
	bad.To = "another-yard"
	if err := VerifyPeerRequest(from, bad, input, sig, now); !errors.Is(err, ErrSignature) {
		t.Errorf("signed for another receiver: %v", err)
	}
	bad = r
	bad.Digest = ContentDigest([]byte("tampered"))
	if err := VerifyPeerRequest(from, bad, input, sig, now); !errors.Is(err, ErrSignature) {
		t.Errorf("tampered digest: %v", err)
	}
	if err := VerifyPeerRequest(from, r, strings.Replace(input, `"content-digest"`, `"@signature-params"`, 1), sig, now); err == nil {
		t.Error("@signature-params as a covered component was accepted")
	}
	// The answer pair: signed here, replayed for a fresh nonce there.
	aInput, aSig, aDigest := SignPeerResponse(signer, 200, r, body)
	if err := VerifyPeerResponse(from, 200, r, r.Nonce, aDigest, aInput, aSig, body); err != nil {
		t.Fatalf("the answer as signed: %v", err)
	}
	later := r
	later.Nonce = "freshnonce"
	if err := VerifyPeerResponse(from, 200, later, later.Nonce, aDigest, aInput, aSig, body); !errors.Is(err, ErrSignature) {
		t.Errorf("an answer replayed for a new request: %v", err)
	}
	// The linked answer is the same pair on another route: signed for a
	// link request here, replayed for a fresh one there, it does not
	// verify either.
	link := PeerRequestSig{Method: "POST", Authority: "example.com", Path: "/peers/acct/b/link", To: from, Nonce: "linknonce1", Digest: digest, Created: now.Unix(), Expires: now.Unix() + 300}
	lInput, lSig, lDigest := SignPeerResponse(signer, 202, link, body)
	if err := VerifyPeerResponse(from, 202, link, link.Nonce, lDigest, lInput, lSig, body); err != nil {
		t.Fatalf("the linked answer as signed: %v", err)
	}
	fresh := link
	fresh.Nonce = "linknonce2"
	if err := VerifyPeerResponse(from, 202, fresh, fresh.Nonce, lDigest, lInput, lSig, body); !errors.Is(err, ErrSignature) {
		t.Errorf("a linked answer replayed for a new request: %v", err)
	}
}
