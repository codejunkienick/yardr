package rpc

import (
	"context"
	"fmt"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/yardkey"
)

// The in-place migration of a device paired before the channel (docs/
// design/2026-11-05-yardr-device-e2e.md, section 3.4, candidate (b)):
// a device that holds a token and pins no channel key registers its
// own key once, by RegisterChannelKey, a call its token authenticates,
// and is answered the yard's channel public key signed by the yard's
// identity. From then on it handshakes with that key, and its calls
// are sealed as a QR-paired device's are; the device list says
// `sealed (in place)` of it, for an edge active at that one call could
// have put its own key in either place and so hold the device's
// channel until it is re-paired by QR. Re-pairing lifts the label and
// is never required.

// PlainDeviceCallsDown is every device call a plain request down the
// link is still taken for once the yard refuses plain device calls
// there: RegisterChannelKey, section 3.5's second exception beside the
// pairing redemption, which runs inside its handshake and is answered
// before any call is judged (PairPath), so is not one. It is one call
// per pre-release device, and its identical repeat where the answer was
// lost, which pins nothing; it is refused for any other call of a device
// that pins a key, so it is no way back to plain.
var PlainDeviceCallsDown = []string{"RegisterChannelKey"}

// ChannelRegistered is the answer to RegisterChannelKey: the device it
// pinned, the yard's channel public key with the yard identity's
// signature of it (yardkey.SignChannelKey), as the pairing QR's answer
// carries them, and the yard identity's public key, which the
// signature verifies against (yardkey.VerifyChannelKey).
type ChannelRegistered struct {
	User             string `json:"user"`
	Device           string `json:"device"`
	ChannelKey       string `json:"channel_key"`
	ChannelSignature string `json:"channel_signature"`
	YardKey          string `json:"yard_key"`
}

// registerChannelKey is RegisterChannelKey: it pins channelKey on the
// calling device, found by the token that resolved it
// (store.PinChannelKeyInPlace), and answers ChannelRegistered. A device
// registers only itself, by its own token: no argument names a device.
// It is no voucher issuer: the device asks for its voucher afterwards,
// on a call sealed to the key it learnt here (pushVoucher).
//
// The yard's keys are had before anything is pinned, so a device is
// never pinned to a channel it was not told: a yard that cannot read
// or make its channel key (yardkey.EnsureChannel makes one where there
// is none), or cannot read its identity key to sign with, refuses and
// says which. A device that pins a key already is refused, with one
// exception that the store makes: the key it registered in place, sent
// again because the answer was lost, is answered the yard's keys as they
// stand, and nothing is pinned twice (store.PinChannelKeyInPlace). A call
// that came sealed is from such a device and is refused whatever key it
// sends, before the key's encoding is checked: it proved it holds the
// channel, so it lost nothing. A key that is none, and one another
// device pins, are refused too.
func (u *UserServer) registerChannelKey(ctx context.Context, channelKey string) (*ChannelRegistered, error) {
	if _, sealed := ctx.Value(e2eSessionKey{}).(*e2eSession); sealed {
		return nil, fmt.Errorf("%w: this device pins a channel key already: a key is registered in place once, and only by a device that pins none. Pair it again by QR to change its key", store.ErrConflict)
	}
	c, _ := ctx.Value(callerKey{}).(*Caller)
	if c == nil || !c.device || c.token == "" {
		return nil, fmt.Errorf("a channel key is registered in place by a device's own token alone")
	}
	if _, err := yardkey.ParseChannelPublic(channelKey); err != nil {
		return nil, fmt.Errorf("%w: send the base64url of the device's 32-byte X25519 public key", err)
	}
	if u.channelPath == "" {
		u.log.Error("a channel key registration refused: this yard was told no channel key file")
		return nil, fmt.Errorf("this yard holds no channel key and cannot make one: it was told no file for it. Keep calling as before, and update the yard")
	}
	channel, _, err := yardkey.EnsureChannel(u.channelPath)
	if err != nil {
		u.log.Error("a channel key registration refused: no channel key could be had", "err", err)
		return nil, fmt.Errorf("this yard could not read or make its channel key. Keep calling as before, and ask the operator to check the yard's channel key file")
	}
	if u.identityPath == "" {
		u.log.Error("a channel key registration refused: this yard was told no identity key file")
		return nil, fmt.Errorf("this yard holds no identity key to sign its channel key with. Keep calling as before, and update the yard")
	}
	identity, err := store.Load(u.identityPath)
	if err != nil {
		u.log.Error("a channel key registration refused: the identity key could not be read", "err", err)
		return nil, fmt.Errorf("this yard could not read its identity key to sign its channel key with. Keep calling as before, and ask the operator to check the yard's identity key file")
	}
	d, err := u.st.PinChannelKeyInPlace(ctx, c.token, channelKey)
	if err != nil {
		return nil, err
	}
	return &ChannelRegistered{User: d.User, Device: d.Name,
		ChannelKey:       yardkey.ChannelPublic(channel),
		ChannelSignature: yardkey.SignChannelKey(identity, channel.Public),
		YardKey:          store.Public(identity)}, nil
}
