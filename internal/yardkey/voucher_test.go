package yardkey

import (
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/store"
)

// voucherExpiry is the expiry the tests' vouchers carry: 2026-11-07T20:15:00Z.
var voucherExpiry = time.Unix(1794082500, 0).UTC()

func testVoucher() Voucher {
	return Voucher{User: "sam", Device: "phone", Yard: "south", Expires: voucherExpiry}
}

// forgeVoucher is a voucher whose statement is the text stmt, signed by
// key as a yard would sign it: what SignVoucher refuses to make, to see
// that VerifyVoucher refuses it too.
func forgeVoucher(key ed25519.PrivateKey, stmt string) string {
	return VoucherPrefix + enc.EncodeToString([]byte(stmt)) + "." + enc.EncodeToString(ed25519.Sign(key, []byte(stmt)))
}

// The voucher's wire format, pinned. Ed25519 signs deterministically, so a
// change to the label, to the order or text of the statement or to the
// encoding no longer makes this string, and the router that checks the
// statement from its own copy would refuse every voucher the yard issues.
// The statement is checked here against the standard library's verifier,
// not the code under test, and read back from the voucher: it stands in
// the open, beside its signature, as the design states.
func TestVoucherWireFormatIsPinned(t *testing.T) {
	const (
		statement = "yardr push voucher v1\nsam\nphone\nsouth\n1794082500"
		voucher   = "yardr-voucher-eWFyZHIgcHVzaCB2b3VjaGVyIHYxCnNhbQpwaG9uZQpzb3V0aAoxNzk0MDgyNTAw.dZsBs3v2aotlAKP5ZqYdlR4FVW9QIBKK5sjjgXnEWL_-QVfzz2F0F45LZZkCb_ia6lgJ_iISKfFYNbPP06UrDg"
	)
	yard := seeded(1)
	got, err := SignVoucher(yard, testVoucher())
	if err != nil || got != voucher {
		t.Fatalf("the voucher signs as %s (%v)", got, err)
	}
	body, _ := strings.CutPrefix(voucher, VoucherPrefix)
	encStatement, encSig, _ := strings.Cut(body, ".")
	stmt, err := enc.DecodeString(encStatement)
	if err != nil || string(stmt) != statement {
		t.Fatalf("the voucher's statement reads %q (%v), want %q", stmt, err, statement)
	}
	sig, err := enc.DecodeString(encSig)
	if err != nil || !ed25519.Verify(yard.Public().(ed25519.PublicKey), []byte(statement), sig) {
		t.Errorf("the voucher's signature is not the yard's over the statement as written (%v)", err)
	}
	v, err := VerifyVoucher(store.Public(yard), voucher, voucherExpiry.Add(-time.Minute))
	if err != nil || v != testVoucher() {
		t.Errorf("the pinned voucher verifies as %+v (%v)", v, err)
	}
}

// A voucher the yard signed verifies against the yard's public key, as the
// edge's map holds it, and answers the statement it makes; a voucher is
// good until its expiry and not at it.
func TestVoucherVerifiesUntilItsExpiry(t *testing.T) {
	yard := newKey(t)
	voucher, err := SignVoucher(yard, testVoucher())
	if err != nil || !strings.HasPrefix(voucher, VoucherPrefix) {
		t.Fatalf("SignVoucher = %q, %v", voucher, err)
	}
	for _, at := range []time.Duration{-24 * time.Hour, -time.Minute, -time.Second} {
		v, err := VerifyVoucher(store.Public(yard), voucher, voucherExpiry.Add(at))
		if err != nil || v != testVoucher() {
			t.Errorf("%s before its expiry: %+v, %v", -at, v, err)
		}
	}
	for _, at := range []time.Duration{0, time.Second, 24 * time.Hour} {
		_, err := VerifyVoucher(store.Public(yard), voucher, voucherExpiry.Add(at))
		if !errors.Is(err, ErrVoucherExpired) || !errors.Is(err, store.ErrSignature) {
			t.Errorf("%s after its expiry: %v, want ErrVoucherExpired and ErrSignature", at, err)
		}
	}
}

// A voucher verifies nowhere that it should not: against another yard's
// key, with its signature or any field of its statement altered, signed
// as another thing the yard signs, or made of anything but a statement of
// five lines under this label. Each is store.ErrSignature, and none is an
// expired voucher.
func TestVoucherWronglySignedVerifiesNowhere(t *testing.T) {
	yard, other := seeded(1), seeded(101)
	good, err := SignVoucher(yard, testVoucher())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := strings.CutPrefix(good, VoucherPrefix)
	encStatement, encSig, _ := strings.Cut(body, ".")
	statement, _ := enc.DecodeString(encStatement)
	sig, _ := enc.DecodeString(encSig)
	flipped := append([]byte(nil), sig...)
	flipped[0] ^= 1
	altered := func(old, replacement string) string {
		return VoucherPrefix + enc.EncodeToString([]byte(strings.Replace(string(statement), old, replacement, 1))) + "." + encSig
	}
	channel, err := GenerateChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	stmt := string(statement)
	cases := []struct{ name, key, voucher string }{
		{"another yard's key", store.Public(other), good},
		{"signed by another key", store.Public(yard), forgeVoucher(other, stmt)},
		{"a bit of the signature changed", store.Public(yard), VoucherPrefix + encStatement + "." + enc.EncodeToString(flipped)},
		{"the user changed", store.Public(yard), altered("sam", "jo")},
		{"the device changed", store.Public(yard), altered("phone", "tablet")},
		{"the yard changed", store.Public(yard), altered("south", "north")},
		{"the expiry lengthened", store.Public(yard), altered("1794082500", "1794086100")},
		{"the signature of a channel key", store.Public(yard), VoucherPrefix + encStatement + "." + enc.EncodeToString(ed25519.Sign(yard, channelSigned(channel.Public)))},
		{"a channel key's text as the statement", store.Public(yard), forgeVoucher(yard, string(channelSigned(channel.Public)))},
		{"another label", store.Public(yard), forgeVoucher(yard, strings.Replace(stmt, voucherLabel, "yardr push voucher v2", 1))},
		{"a sixth line", store.Public(yard), forgeVoucher(yard, stmt+"\nmore")},
		{"a missing line", store.Public(yard), forgeVoucher(yard, "yardr push voucher v1\nsam\nphone\n1794082500")},
		{"an empty yard", store.Public(yard), forgeVoucher(yard, "yardr push voucher v1\nsam\nphone\n\n1794082500")},
		{"a carriage return in the user", store.Public(yard), forgeVoucher(yard, "yardr push voucher v1\nsam\r\nphone\nsouth\n1794082500")},
		{"an expiry that is no number", store.Public(yard), forgeVoucher(yard, "yardr push voucher v1\nsam\nphone\nsouth\ntomorrow")},
		{"an expiry of zero", store.Public(yard), forgeVoucher(yard, "yardr push voucher v1\nsam\nphone\nsouth\n0")},
		{"a statement over the bound", store.Public(yard), forgeVoucher(yard, "yardr push voucher v1\nsam\nphone\n"+strings.Repeat("s", maxVoucherStatement)+"\n1794082500")},
		{"an oversized encoded voucher", store.Public(yard), VoucherPrefix + strings.Repeat("A", maxVoucherStatement*2)},
		{"no key", "", good},
		{"a key that is no key", "not-a-key", good},
		{"no prefix", store.Public(yard), strings.TrimPrefix(good, VoucherPrefix)},
		{"a device token's prefix", store.Public(yard), "yardr-dev-" + body},
		{"no dot", store.Public(yard), VoucherPrefix + encStatement + encSig},
		{"no signature", store.Public(yard), VoucherPrefix + encStatement + "."},
		{"no statement", store.Public(yard), VoucherPrefix + "." + encSig},
		{"a statement that is no base64", store.Public(yard), VoucherPrefix + "!!." + encSig},
		{"a signature that is no base64", store.Public(yard), VoucherPrefix + encStatement + ".!!"},
		{"a newline in the statement encoding", store.Public(yard), VoucherPrefix + encStatement[:8] + "\n" + encStatement[8:] + "." + encSig},
		{"a newline in the signature encoding", store.Public(yard), VoucherPrefix + encStatement + "." + encSig[:8] + "\n" + encSig[8:]},
		{"padding on the statement", store.Public(yard), VoucherPrefix + encStatement + "=." + encSig},
		{"nothing", store.Public(yard), ""},
	}
	for _, c := range cases {
		v, err := VerifyVoucher(c.key, c.voucher, voucherExpiry.Add(-time.Minute))
		if !errors.Is(err, store.ErrSignature) || errors.Is(err, ErrVoucherExpired) || v != (Voucher{}) {
			t.Errorf("%s: verifies as %+v, %v, want store.ErrSignature and no statement", c.name, v, err)
		}
	}
	// The pinned cases are not refused for a reason of their own: the voucher
	// they are made from verifies.
	if _, err := VerifyVoucher(store.Public(yard), good, voucherExpiry.Add(-time.Minute)); err != nil {
		t.Fatalf("the voucher the cases are made from: %v", err)
	}
}

// A forged voucher that is also past its expiry is refused for its
// signature, the thing it is wrong in first: the expiry of a statement
// the yard did not make says nothing.
func TestVoucherForgedAndExpiredIsRefusedAsForged(t *testing.T) {
	yard, other := seeded(1), seeded(101)
	forged, err := SignVoucher(other, testVoucher())
	if err != nil {
		t.Fatal(err)
	}
	_, err = VerifyVoucher(store.Public(yard), forged, voucherExpiry.Add(time.Hour))
	if !errors.Is(err, store.ErrSignature) || errors.Is(err, ErrVoucherExpired) {
		t.Errorf("a forged, lapsed voucher = %v, want a bad signature alone", err)
	}
}

// What SignVoucher cannot carry it refuses: a field that is empty or has
// a line break, which would make two statements the same text, an expiry
// that is no time and a statement past its bound.
func TestSignVoucherRefusesWhatItCannotCarry(t *testing.T) {
	yard := seeded(1)
	for name, v := range map[string]Voucher{
		"no user":                 {Device: "phone", Yard: "south", Expires: voucherExpiry},
		"no device":               {User: "sam", Yard: "south", Expires: voucherExpiry},
		"no yard":                 {User: "sam", Device: "phone", Expires: voucherExpiry},
		"a newline in the user":   {User: "sam\njo", Device: "phone", Yard: "south", Expires: voucherExpiry},
		"a newline in the device": {User: "sam", Device: "ph\none", Yard: "south", Expires: voucherExpiry},
		"a newline in the yard":   {User: "sam", Device: "phone", Yard: "so\nuth", Expires: voucherExpiry},
		"a return in the yard":    {User: "sam", Device: "phone", Yard: "so\ruth", Expires: voucherExpiry},
		"no expiry":               {User: "sam", Device: "phone", Yard: "south"},
		"a yard named at length":  {User: "sam", Device: "phone", Yard: strings.Repeat("s", maxVoucherStatement), Expires: voucherExpiry},
	} {
		if voucher, err := SignVoucher(yard, v); err == nil {
			t.Errorf("%s: signed %q", name, voucher)
		}
	}
}
