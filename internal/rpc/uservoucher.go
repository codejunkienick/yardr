package rpc

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/yardkey"
)

// The push registration voucher the yard issues to a device (docs/api.md,
// "A registration voucher"; the bytes are yardkey's, voucher.go): the
// yard's statement, signed and not concealed, that this user's device is
// registering for push at this yard until an expiry. It is for the edge's
// push relay to check against the yard's public key in place of the probe
// it makes of the yard with the device's token, which a device that calls
// through its channel no longer hands the edge.
//
// A device gets one where it pinned a channel key: in the answer of its
// pairing, sealed with the token (answerSealed, pakeFinish), and a fresh
// one whenever it asks, as often as it registers, by a call sealed to the
// yard (pushVoucher). The statement in it is the one /user/me gives the
// device that holds the token: its user, its name and the yard's.

// VoucherLife is how long a voucher stands. A device asks for one each time
// it registers, so it need only outlast the way to that call, and a
// removed device's voucher ends by itself: nothing recalls one, and the
// relay's own rows for the device are deleted with its removal. It is the
// pairing code's own ten minutes; a receiver reads a voucher's expiry and
// assumes no lifetime.
const VoucherLife = 10 * time.Minute

// SetIdentityKeyFile tells the server the file of the yard's identity key
// (config's PeerKey), which signs a voucher. It is read for each voucher,
// never held, as the channel key is. With no file told, or none readable,
// the yard issues no voucher.
func (u *UserServer) SetIdentityKeyFile(path string) { u.identityPath = path }

// voucherFor signs a voucher that the device of user is registering at the
// yard named yard, good for VoucherLife.
func (u *UserServer) voucherFor(yard, user, device string) (string, error) {
	if u.identityPath == "" {
		return "", fmt.Errorf("this yard was told no identity key file")
	}
	identity, err := store.Load(u.identityPath)
	if err != nil {
		return "", err
	}
	return yardkey.SignVoucher(identity, yardkey.Voucher{User: user, Device: device, Yard: yard, Expires: u.now().Add(VoucherLife)})
}

// pairedSealed is the answer to the redemption of device d for a device
// that pinned a channel key: Paired, with a voucher beside the token. A yard
// that cannot sign one answers without, and says so: the token is the one
// answer that is never lost for want of an extra, and the device asks for a
// voucher when it registers.
func (u *UserServer) pairedSealed(ctx context.Context, d *store.NewDevice) Paired {
	p := Paired{Token: d.Token, User: d.User, Device: d.Name, Yard: u.yard(ctx)}
	voucher, err := u.voucherFor(p.Yard, p.User, p.Device)
	if err != nil {
		u.log.Error("a device's pairing answer holds no push voucher", "err", err)
		return p
	}
	p.Voucher = voucher
	return p
}

// pushVoucher answers a device's request for a fresh registration voucher,
// {"voucher": ...}, for its own user and device, as the channel resolved
// them. It answers a call that came sealed and no other: a plain call,
// whether down the link or on the yard's own listener, is 403 and names the
// fix. A token proves the device only to whoever holds the token, and the
// edge read every token a device sent before it held a channel, so a
// voucher asked for by one would be the edge's to mint for the devices the
// channel is meant to keep it from. A voucher is no credential at the yard,
// whose calls it opens none of. A device removed after its call was
// admitted may be given one that call's worth, as any admitted call may
// finish (e2eCall): it lapses by VoucherLife.
func (u *UserServer) pushVoucher(w http.ResponseWriter, r *http.Request) {
	if _, sealed := r.Context().Value(e2eSessionKey{}).(*e2eSession); !sealed {
		http.Error(w, "a push registration voucher is issued only to a call sealed to this yard's channel key, never to a plain one", http.StatusForbidden)
		return
	}
	c := callerOf(r)
	voucher, err := u.voucherFor(u.yard(r.Context()), c.User, c.SaidBy)
	if err != nil {
		u.log.Error("a push voucher could not be signed", "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]string{"voucher": voucher})
}
