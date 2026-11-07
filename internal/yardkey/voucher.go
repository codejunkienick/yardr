package yardkey

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"yardr.dev/yardr/internal/store"
)

// A push registration voucher is the yard's short statement, for the push
// relay at the edge, that this user's device is registering for push at
// this yard until an expiry (docs/design/2026-11-05-yardr-device-e2e.md,
// section 3.3, candidate (b)). It takes the place of the probe by which
// the edge's router learnt the same three facts, asking the yard with the
// device's token: a device that calls through a channel no longer hands
// the edge that token, so the yard says it ahead instead, and the router
// can check the signature against the yard's public key, which the edge's
// map already holds for the yard's signed calls.
//
// The statement is signed, not concealed. The user, the device, the yard
// and the expiry stand in the voucher as plain lines, and the signature
// covers exactly those bytes, because the router must read the yard to
// check the signature against its map and must name the device its push
// rows stand for. A signature authenticates the fields and conceals none
// of them: whoever carries a voucher reads who is registering at which
// yard and until when, which the register call's own yard header and the
// probe's answer already told the relay. Nothing of the device's token,
// its channel key or anything else of the yard is in it, and the yard
// takes it as no credential: it vouches for the device to the relay and
// opens nothing at the yard.
//
// The label is the signature's own, so it is no request's, link's, frame's
// or channel key's, and theirs are not a voucher's. The edge's router is
// to check these bytes from a copy of its own; the tests pin a vector for
// it to pin, as the link's are pinned.

const voucherLabel = "yardr push voucher v1"

// VoucherPrefix is how a voucher begins: what tells one from a device
// token (store.DeviceTokenPrefix) where both can stand in the same place,
// before anything is decoded.
const VoucherPrefix = "yardr-voucher-"

// maxVoucherStatement bounds the statement a voucher holds: a voucher is
// short, and a yard named at length is refused a voucher, not given an
// unbounded bearer credential.
const maxVoucherStatement = 1 << 10

// ErrVoucherExpired is the reason a voucher that was signed is refused at
// or after its expiry. VerifyVoucher answers it beside store.ErrSignature,
// so a receiver that asks only whether the voucher stands has one error
// and one that asks why can tell a lapsed voucher from a forged one.
var ErrVoucherExpired = errors.New("the voucher has expired")

// Voucher is a registration voucher's statement, signed and not
// concealed: whose device is registering, at which yard, and until when.
// Yard is the name the yard gives itself (what /user/me says), a claim by
// the key that signed it and no identity: a receiver binds a voucher to
// the yard whose key checked it.
type Voucher struct {
	User    string
	Device  string
	Yard    string
	Expires time.Time
}

// statement is the bytes a voucher carries and its signature covers: the
// label and the four fields, a line each, the expiry as Unix seconds. No
// field holds a line break (check), so no two statements are the same
// text.
func (v Voucher) statement() []byte {
	return []byte(strings.Join([]string{voucherLabel, v.User, v.Device, v.Yard, strconv.FormatInt(v.Expires.Unix(), 10)}, "\n"))
}

// check refuses a statement a voucher cannot carry: a field that is empty
// or holds a line break, an expiry that is no time and a statement over
// maxVoucherStatement.
func (v Voucher) check() error {
	for _, f := range []struct{ name, value string }{{"user", v.User}, {"device", v.Device}, {"yard", v.Yard}} {
		if f.value == "" {
			return fmt.Errorf("a voucher names a %s", f.name)
		}
		if strings.ContainsAny(f.value, "\r\n") {
			return fmt.Errorf("a voucher's %s holds a line break", f.name)
		}
	}
	if v.Expires.Unix() <= 0 {
		return errors.New("a voucher has an expiry")
	}
	if n := len(v.statement()); n > maxVoucherStatement {
		return fmt.Errorf("a voucher's statement is %d bytes, over the %d it may be", n, maxVoucherStatement)
	}
	return nil
}

// SignVoucher signs v with the yard's identity and answers the voucher as
// it travels: VoucherPrefix, the statement and the signature over it, the
// two base64url without padding and joined by a dot. It is the yard's
// alone to make; a statement it cannot carry is refused.
func SignVoucher(identity ed25519.PrivateKey, v Voucher) (string, error) {
	if err := v.check(); err != nil {
		return "", err
	}
	stmt := v.statement()
	return VoucherPrefix + enc.EncodeToString(stmt) + "." + enc.EncodeToString(ed25519.Sign(identity, stmt)), nil
}

// VerifyVoucher checks that the yard whose public key is yardPublic (the
// key the edge's map holds for it, as store.Public writes it) signed this
// voucher and that it has not expired at now, and answers the statement
// it makes. A voucher expires at its expiry itself. Every failure is
// store.ErrSignature, with the reason in its text for the receiver's own
// log; an expired one is ErrVoucherExpired too.
func VerifyVoucher(yardPublic, voucher string, now time.Time) (Voucher, error) {
	// Bound the wire value before decoding it: the statement's limit would
	// otherwise be checked only after base64 has allocated for untrusted input.
	maxWire := len(VoucherPrefix) + enc.EncodedLen(maxVoucherStatement) + 1 + enc.EncodedLen(ed25519.SignatureSize)
	if len(voucher) > maxWire {
		return Voucher{}, fmt.Errorf("%w: voucher is too long", store.ErrSignature)
	}
	pub, err := store.ParsePublic(yardPublic)
	if err != nil {
		return Voucher{}, fmt.Errorf("%w: bad key", store.ErrSignature)
	}
	body, ok := strings.CutPrefix(voucher, VoucherPrefix)
	if !ok {
		return Voucher{}, fmt.Errorf("%w: not a voucher", store.ErrSignature)
	}
	// A voucher with no dot has no signature, which no key verifies.
	encStatement, encSig, _ := strings.Cut(body, ".")
	stmt, err := enc.Strict().DecodeString(encStatement)
	if err != nil || enc.EncodeToString(stmt) != encStatement {
		return Voucher{}, fmt.Errorf("%w: a voucher with a bad statement", store.ErrSignature)
	}
	sig, err := enc.Strict().DecodeString(encSig)
	if err != nil || enc.EncodeToString(sig) != encSig || !ed25519.Verify(pub, stmt, sig) {
		return Voucher{}, fmt.Errorf("%w: bad signature on the voucher", store.ErrSignature)
	}
	// After the signature: the statement is then the yard's own.
	v, err := parseVoucher(stmt)
	if err != nil {
		return Voucher{}, fmt.Errorf("%w: %v", store.ErrSignature, err)
	}
	if !now.Before(v.Expires) {
		return Voucher{}, fmt.Errorf("%w: %w", store.ErrSignature, ErrVoucherExpired)
	}
	return v, nil
}

// parseVoucher reads a statement as statement writes it.
func parseVoucher(stmt []byte) (Voucher, error) {
	lines := strings.Split(string(stmt), "\n")
	if len(lines) != 5 || lines[0] != voucherLabel {
		return Voucher{}, errors.New("a signed statement that is no voucher's")
	}
	sec, err := strconv.ParseInt(lines[4], 10, 64)
	if err != nil {
		return Voucher{}, errors.New("a voucher with a bad expiry")
	}
	v := Voucher{User: lines[1], Device: lines[2], Yard: lines[3], Expires: time.Unix(sec, 0).UTC()}
	if err := v.check(); err != nil {
		return Voucher{}, err
	}
	return v, nil
}
