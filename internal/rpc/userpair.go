package rpc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/yardkey"
)

// The pairing redemption of a device (docs/users.md, "Enrolled by a
// code"), in its two forms. The plain one is the first yardr had: the
// code in {"code": ...}, the token in the answer, each readable by
// whoever carries the connection. The sealed one runs the code as the
// first message of a Noise handshake to the yard's channel key — the
// public half the pairing QR's fourth field carries — and seals the
// answer in reply: {"sealed": ...} each way, base64url of one
// handshake message, the first carrying the code as its payload and
// the device's own channel key sealed inside it, the second carrying
// the Paired answer. A yard with no channel key serves the plain form
// alone, and a pairing redeemed with no key field is the plain one,
// exactly as before.

// Paired is the answer to the code of a device's pairing: the token, which
// is in no later answer, and what /user/me says of whoever carries it.
type Paired struct {
	Token  string `json:"token"`
	User   string `json:"user"`
	Device string `json:"device"`
	Yard   string `json:"yard"`
}

// PresenterHeader is the edge's word, on a redemption of a pairing's code
// that it forwards down the link, for who presents it: the address of the
// signed-in account the edge resolved. pair reads it to hold the redemption
// to the user the pairing was opened for. No other call reads it, and it is
// no identity: it makes nobody a caller.
//
// Why it is believed. Down the link the edge alone speaks: every request
// frame is checked against the key this yard pinned for the edge, headers in
// the signature (edgelink.Link, yardkey.HeaderDigest), and the edge takes out
// any copy a client sent before it sets its own. On the yard's own listener a
// client may send it, and it is believed there too, which is harmless only
// because the code alone already redeems there. Either way it can only
// narrow redemption and never widen it: the code is still required, and a
// mapped presenter must be the pairing's user, so a header from the wrong
// party gets a redemption refused and nothing more. An address that maps to
// nobody is refused only for a pairing whose user the mapping names; where
// neither is named the code redeems as with no presenter, which rests on
// every presenter the edge forwards being the owner or an active member and
// every active member being mapped.
const PresenterHeader = "X-Edge-Presenter"

// SetChannelKeyFile tells the server the file of the yard's channel
// key (config's ChannelKey), the X25519 static the pairing QR's fourth
// field carries, which a sealed redemption is opened with. It is read
// for each sealed redemption, never held: a key made after the server
// started — the pairing command makes it, on the yard's own machine —
// is the key the next redemption opens with. With no file told, or
// none there, a sealed redemption is refused as a bad request and the
// plain one is served as it always was.
func (u *UserServer) SetChannelKeyFile(path string) { u.channelPath = path }

// pairingChannel is the yard's channel key, read from the file
// SetChannelKeyFile named.
func (u *UserServer) pairingChannel() (yardkey.ChannelKey, error) {
	if u.channelPath == "" {
		return yardkey.ChannelKey{}, fmt.Errorf("this yard was told no channel key file")
	}
	return yardkey.LoadChannel(u.channelPath)
}

// pair takes the code of the yard's open device pairing, as the body
// {"code": ...}, and answers the device it was opened for with its token
// (docs/users.md, "Enrolled by a code"). A wrong code is 403 and counted by
// the pairing, which the fifth ends; the right one is good once. What is no
// code at all is 400 and no try. Nothing of a code is logged or counted.
// A redemption that names its presenter is redeemed for the pairing's user
// alone; anyone else it names is refused, and counted, as a wrong code,
// but for an address that maps to nobody where the pairing's user is mapped
// to nobody too, which redeems as with no presenter.
//
// A body of {"sealed": ...} is the sealed form (pairSealed): the code
// inside a Noise handshake to the yard's channel key, the answer sealed
// in reply, and the device recorded with the key the handshake proved.
// The two forms share the pairing, its presenter rule and its counting:
// which one a redemption takes is the body's own field.
func (u *UserServer) pair(w http.ResponseWriter, r *http.Request) {
	var asked struct {
		Code   string `json:"code"`
		Sealed string `json:"sealed"`
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPair))
	if err == nil {
		err = json.Unmarshal(body, &asked)
	}
	if err == nil && asked.Sealed != "" {
		u.pairSealed(w, r, asked.Sealed)
		return
	}
	var d *store.NewDevice
	if err == nil {
		d, err = u.redeem(r, asked.Code)
	}
	if u.answerPairRefusal(w, r, err) {
		return
	}
	if err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	u.log.Info("a device was paired", "user", d.User, "device", d.Name)
	// The one answer that holds the token: nothing between keeps it.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, Paired{Token: d.Token, User: d.User, Device: d.Name, Yard: u.yard(r.Context())})
}

// answerPairRefusal answers err as a pairing refusal is answered, and
// reports whether it was one: no pairing is 404, as the path is gone;
// a wrong code is 403 and the rest of the pairing's refusals 409, each
// as {"refused": reason}. A refusal is logged with its reason alone.
func (u *UserServer) answerPairRefusal(w http.ResponseWriter, r *http.Request, err error) bool {
	var no *store.Refused
	switch {
	case errors.As(err, &no) && no.Reason == store.NoPairing:
		// It ended between the look and the call: the path is gone.
		http.NotFound(w, r)
		return true
	case errors.As(err, &no):
		u.log.Warn("a device's pairing call refused", "reason", no.Reason)
		status := http.StatusConflict
		if no.Reason == store.WrongCode {
			status = http.StatusForbidden
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"refused": no.Reason})
		return true
	}
	return false
}

// pairSealed is pair for a redemption inside a Noise handshake: sealed
// is the base64url of the handshake's first message, whose payload is
// the pairing code and whose sealed static is the device's channel
// key. The yard opens it with its channel key, redeems the code it
// carries — for the pairing's user alone, where the request names its
// presenter, as a plain redemption is — and answers the base64url of
// the second message, whose payload is the Paired JSON, so neither the
// code nor the token crosses readable. The device is recorded with
// the key the handshake proved pinned beside its token's hash.
//
// A message that does not open is 400 and no try, as a body that is no
// code is: the code inside it was never read. A refusal of the code it
// carries is answered as a plain refusal is, in the clear of the
// status and its reason alone; the token is the only secret a pairing
// answer holds, and it only ever crosses sealed. A transcript
// recorded and replayed buys nothing: the code is used by the first
// redemption, so the replay meets a pairing that is done, or counts
// as a wrong code against the next one, and no second device is
// added or token answered.
func (u *UserServer) pairSealed(w http.ResponseWriter, r *http.Request, sealed string) {
	first, err := base64.RawURLEncoding.DecodeString(sealed)
	if err == nil {
		var channel yardkey.ChannelKey
		channel, err = u.pairingChannel()
		if err == nil {
			var opened *yardkey.PairingHandshake
			opened, err = yardkey.OpenPairingHandshake(channel, first)
			if err == nil {
				u.answerSealed(w, r, opened)
				return
			}
		}
	}
	u.log.Warn("a device's sealed pairing call refused", "err", err)
	http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
}

// answerSealed redeems the code an opened pairing handshake carried
// and seals the answer into it.
func (u *UserServer) answerSealed(w http.ResponseWriter, r *http.Request, opened *yardkey.PairingHandshake) {
	d, err := u.redeemKeyed(r, opened.Code, opened.DeviceKey)
	if u.answerPairRefusal(w, r, err) {
		return
	}
	if err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	answer, err := json.Marshal(Paired{Token: d.Token, User: d.User, Device: d.Name, Yard: u.yard(r.Context())})
	var reply []byte
	if err == nil {
		reply, err = opened.SealAnswer(answer)
	}
	if err != nil {
		u.log.Error("a device's sealed pairing answer could not be sealed", "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	u.log.Info("a device was paired", "user", d.User, "device", d.Name)
	// The one answer that holds the token, sealed: nothing between
	// keeps it, and nothing between can read it.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]string{"sealed": base64.RawURLEncoding.EncodeToString(reply)})
}

// redeem is the store's redemption of code for the request r: on the code
// alone where r names no presenter, for the user the presenter's address
// maps to (presenter) where it maps to one, whom the store holds to the
// pairing's own, and through the unmapped path where it maps to nobody. A
// header sent twice or empty, and a mapping that cannot be read, name
// nobody the strict way, so sending it empty is no way past the check.
func (u *UserServer) redeem(r *http.Request, code string) (*store.NewDevice, error) {
	return u.redeemKeyed(r, code, "")
}

// redeemKeyed is redeem for a redemption whose device pins deviceKey:
// "" is the plain redemption, and any other is the sealed one, which
// records the key with the device it adds.
func (u *UserServer) redeemKeyed(r *http.Request, code, deviceKey string) (*store.NewDevice, error) {
	named := r.Header.Values(PresenterHeader)
	sealed := deviceKey != ""
	if len(named) == 0 {
		if sealed {
			return u.st.DevicePairRedeemSealed(r.Context(), code, deviceKey)
		}
		return u.st.DevicePairRedeem(r.Context(), code)
	}
	user, mapped, ok := u.presenter(r.Context(), named)
	if !ok {
		if sealed {
			return u.st.DevicePairRedeemSealedBy(r.Context(), code, "", deviceKey)
		}
		return u.st.DevicePairRedeemBy(r.Context(), code, "")
	}
	if user != "" {
		if sealed {
			return u.st.DevicePairRedeemSealedBy(r.Context(), code, user, deviceKey)
		}
		return u.st.DevicePairRedeemBy(r.Context(), code, user)
	}
	if sealed {
		return u.st.DevicePairRedeemSealedUnmapped(r.Context(), code, mapped, deviceKey)
	}
	return u.st.DevicePairRedeemUnmapped(r.Context(), code, mapped)
}

// presenter is the yard user the address in a PresenterHeader maps to in
// web_jwt_users, matched as the web view matches a token's email, without
// regard to case, with the users the mapping names for an address that maps
// to nobody. ok is false for a header sent twice or empty and for a mapping
// that cannot be read, each refused the strict way, which is the safe way
// to fail where the header can only narrow. An unmapped address is ok with
// "" and its mapping, non-nil even where the yard set none; the store
// refuses it only for a pairing whose user the mapping names, and logs it
// there, so no warn here would know whether it redeems. The presenter is
// told a wrong code, so the log is where the operator learns why; it holds
// no address.
func (u *UserServer) presenter(ctx context.Context, named []string) (user string, mapped map[string]bool, ok bool) {
	if len(named) != 1 {
		return "", nil, false
	}
	if strings.TrimSpace(named[0]) == "" {
		u.log.Warn("a pairing's presenter maps to no user", "setting", store.WebJWTUsersSetting)
		return "", nil, false
	}
	all, err := u.st.Settings(ctx)
	var users map[string]string
	// ParseWebJWTUsers takes no empty mapping; a yard that set none maps
	// nobody.
	if mapping := all[store.WebJWTUsersSetting]; err == nil && mapping != "" {
		users, err = store.ParseWebJWTUsers(mapping)
	}
	if err != nil {
		u.log.Error("a pairing's presenter could not be mapped", "setting", store.WebJWTUsersSetting, "err", err)
		return "", nil, false
	}
	if user := users[strings.ToLower(strings.TrimSpace(named[0]))]; user != "" {
		return user, nil, true
	}
	mapped = map[string]bool{}
	for _, name := range users {
		mapped[name] = true
	}
	return "", mapped, true
}
