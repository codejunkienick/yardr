package rpc

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/yardkey"
)

// PairPath is the one path that takes no credential, since it is where a
// device comes for one: there only while the operator has a pairing open
// for a device, and for whoever has that pairing's code, but for the one
// the pairing was opened for when the redemption names its presenter
// (PresenterHeader).
const PairPath = "/user/pair"

// maxPair bounds the body of a pairing's request: a code, a handshake
// message, or a PAKE message of the pinned suite.
const maxPair = 4 << 10

// The pairing redemption of a device (docs/users.md, "Enrolled by a
// code"), in its three forms. The plain one is the first yardr had: the
// code in {"code": ...}, the token in the answer, each readable by
// whoever carries the connection. The sealed one runs the code as the
// first message of a Noise handshake to the yard's channel key — the
// public half the pairing QR's fourth field carries — and seals the
// answer in reply: {"sealed": ...} each way, base64url of one
// handshake message, the first carrying the code as its payload and
// the device's own channel key sealed inside it, the second carrying
// the Paired answer. The PAKE one is for a code typed or read aloud,
// whose key no QR carried: {"pake": ...} in the steps of pairPAKE,
// SPAKE2 on the code (yardkey's pake.go), the two channel keys
// exchanged under its derived key and the answer sealed with it. A
// yard with no channel key serves the plain form alone, and a pairing
// redeemed with no key field is the plain one, exactly as before.

// Paired is the answer to the code of a device's pairing: the token, which
// is in no later answer, and what /user/me says of whoever carries it.
// Voucher is the yard's push registration voucher for the device
// (uservoucher.go), a statement of its user, device, yard and expiry that is
// signed, not concealed. Only the answer to a device that pinned a channel
// key holds one, so the plain answer is exactly what it was.
type Paired struct {
	Token   string `json:"token"`
	User    string `json:"user"`
	Device  string `json:"device"`
	Yard    string `json:"yard"`
	Voucher string `json:"voucher,omitempty"`
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
// A body of {"pake": ...} is the PAKE form (pairPAKE). The three forms
// share the pairing, its presenter rule and its counting: which one a
// redemption takes is the body's own field.
func (u *UserServer) pair(w http.ResponseWriter, r *http.Request) {
	var asked struct {
		Code   string   `json:"code"`
		Sealed string   `json:"sealed"`
		PAKE   *pakeAsk `json:"pake"`
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPair))
	if err == nil {
		err = json.Unmarshal(body, &asked)
	}
	if err == nil && asked.PAKE != nil {
		u.pairPAKE(w, r, asked.PAKE)
		return
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
	answer, err := json.Marshal(u.pairedSealed(r.Context(), d))
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

// pakeAsk is one step of a PAKE redemption as it crosses: the fields
// of the step it names, each base64url without padding — msg a SPAKE2
// message of the pinned suite (yardkey.PAKESuite), confirm a
// confirmation of it, key the device's channel public key sealed
// under the run's key. A step holds nothing else.
type pakeAsk struct {
	Step    string `json:"step"`
	Session string `json:"session,omitempty"`
	Msg     string `json:"msg,omitempty"`
	Confirm string `json:"confirm,omitempty"`
	Key     string `json:"key,omitempty"`
}

// pakeReply is a PAKE step as the yard answers it: exchange carries
// the yard's second message and the session the run continues under,
// confirmed the yard's confirmation of it, and done the sealed
// answer, in the field each names.
type pakeReply struct {
	Step    string `json:"step"`
	Session string `json:"session,omitempty"`
	Msg     string `json:"msg,omitempty"`
	Confirm string `json:"confirm,omitempty"`
	Answer  string `json:"answer,omitempty"`
}

// pakeAnswer is a PAKE run's sealed answer: the Paired answer of a
// redemption, with the yard's channel public key beside it
// (channel_key, as ChannelPublic writes it) for the device to pin —
// the key a device that took a QR had from the QR, learned here
// under the run's derived key instead.
type pakeAnswer struct {
	Paired
	ChannelKey string `json:"channel_key"`
}

// pakeSession is the server's one live PAKE run: the pairing it began
// under, the yard's half of the exchange and whether the device's
// confirmation has been checked in it. It is held under u.mu, lives
// pakeSessionLife at most, and is spent by one finish, a failed
// confirmation or a newer start.
type pakeSession struct {
	id            string
	pairing       string
	responder     *yardkey.PairingPAKEResponder
	confirmed     bool
	deviceConfirm []byte
	yardConfirm   []byte
	expires       time.Time
}

// pakeSessionLife is how long a PAKE run may take its steps: long
// enough for a person to finish typing at the device, well inside
// the pairing's own ten minutes, which bound it from the other side.
const pakeSessionLife = 2 * time.Minute

// pairPAKE is pair for a redemption through a PAKE on a typed or
// read-aloud code (yardkey's pake.go, suite yardkey.PAKESuite): three
// steps of one run, each a POST of this path, relayed by the edge as
// any pairing traffic is and held against it rewriting each one.
//
//	{"pake":{"step":"start","msg":...}}     -> {"pake":{"step":"exchange","session":...,"msg":...}}
//	{"pake":{"step":"confirm","session":...,"confirm":...}}
//	                                         -> {"pake":{"step":"confirmed","confirm":...}}
//	{"pake":{"step":"finish","session":...,"key":...}}
//	                                         -> {"pake":{"step":"done","answer":...}}
//
// The start is answered from the code of the pairing the yard has
// open, which its store hands the run and nothing else sees. A first
// message that does not open is 400 and no try, as a body that is no
// code is; a confirmation that does not hold is one wrong_code (403),
// counted as any form's wrong code is, the fifth ending the pairing,
// and the run is spent either way; a step that names no live session
// is 400 and no try, so a recorded transcript replayed — its session
// spent, or a new pairing's in its place — tries nothing. Only a
// confirmed run's finish redeems, under the presenter rule of every
// form (redeemPAKE), and its answer — the token and the yard's
// channel key — crosses sealed under the run's key alone, with
// Cache-Control: no-store as every token answer. A yard with no
// channel key serves no PAKE run: it has no static key to exchange.
func (u *UserServer) pairPAKE(w http.ResponseWriter, r *http.Request, ask *pakeAsk) {
	switch ask.Step {
	case "start":
		u.pakeStart(w, r, ask)
	case "confirm":
		u.pakeConfirm(w, r, ask)
	case "finish":
		u.pakeFinish(w, r, ask)
	default:
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
	}
}

// pakeStart answers a PAKE run's first message with the yard's
// second and the session the run continues under.
func (u *UserServer) pakeStart(w http.ResponseWriter, r *http.Request, ask *pakeAsk) {
	first, err := base64.RawURLEncoding.DecodeString(ask.Msg)
	if err == nil {
		_, err = u.pairingChannel()
	}
	var id, code string
	if err == nil {
		id, code, err = u.st.DevicePairPAKEBegin(r.Context())
		if u.answerPairRefusal(w, r, err) {
			return
		}
	}
	var second []byte
	var responder *yardkey.PairingPAKEResponder
	if err == nil {
		second, responder, err = yardkey.AnswerPairingPAKE(code, first)
	}
	var session string
	if err == nil {
		var raw [16]byte
		if _, err = rand.Read(raw[:]); err == nil {
			session = base64.RawURLEncoding.EncodeToString(raw[:])
			u.mu.Lock()
			u.pake = &pakeSession{id: session, pairing: id, responder: responder, expires: u.now().Add(pakeSessionLife)}
			u.mu.Unlock()
		}
	}
	if err != nil {
		u.log.Warn("a device's PAKE pairing call refused", "err", err)
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]pakeReply{"pake": {Step: "exchange", Session: session, Msg: base64.RawURLEncoding.EncodeToString(second)}})
}

// pakeSessionFor answers the live session ask names, and whether
// there is one: a spent, replaced or run-out session is none, and a
// run-out one is cleared as it is found.
func (u *UserServer) pakeSessionFor(ask *pakeAsk) (*pakeSession, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	s := u.pake
	if s == nil || s.id != ask.Session {
		return nil, false
	}
	if !u.now().Before(s.expires) {
		u.pake = nil
		return nil, false
	}
	return s, true
}

// pakeConfirm checks a run's device confirmation and answers the
// yard's: the step a wrong code spends its one online guess at.
func (u *UserServer) pakeConfirm(w http.ResponseWriter, r *http.Request, ask *pakeAsk) {
	s, ok := u.pakeSessionFor(ask)
	if !ok {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	confirm, err := base64.RawURLEncoding.DecodeString(ask.Confirm)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	yardConfirm, live, err := u.confirmPAKESession(s, confirm)
	if !live {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	if err != nil {
		if errors.Is(err, yardkey.ErrPAKEConfirmation) {
			// One wrong code, counted by the pairing the run began
			// under; a run whose pairing is gone is refused as one.
			u.answerPairRefusal(w, r, u.st.DevicePairPAKEWrong(r.Context(), s.pairing))
			return
		}
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]pakeReply{"pake": {Step: "confirmed", Confirm: base64.RawURLEncoding.EncodeToString(yardConfirm)}})
}

// confirmPAKESession checks one device confirmation while the run is
// still the server's live one. A repeated valid confirmation gets the
// same answer if a relay lost it; a replaced run or a different second
// confirmation is no live step and spends no try.
func (u *UserServer) confirmPAKESession(s *pakeSession, confirm []byte) ([]byte, bool, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.pake != s {
		return nil, false, nil
	}
	if s.confirmed {
		if !bytes.Equal(confirm, s.deviceConfirm) {
			return nil, false, nil
		}
		return append([]byte(nil), s.yardConfirm...), true, nil
	}
	yardConfirm, err := s.responder.Confirm(confirm)
	if err != nil {
		// The run is spent whether its confirmation failed or the
		// responder found an unexpected state error. Only a failed
		// confirmation is a wrong-code try at the caller.
		u.pake = nil
		return nil, true, err
	}
	s.confirmed = true
	s.deviceConfirm = append([]byte(nil), confirm...)
	s.yardConfirm = append([]byte(nil), yardConfirm...)
	return yardConfirm, true, nil
}

// pakeFinish redeems a confirmed run: the device's sealed channel
// key in, the token and the yard's channel key sealed out.
func (u *UserServer) pakeFinish(w http.ResponseWriter, r *http.Request, ask *pakeAsk) {
	s, ok := u.pakeSessionFor(ask)
	if !ok {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	// A confirmed run's finish spends it, whatever it carries; an
	// early one is refused and the run lives for its confirmation.
	// The run's responder is used under mu here and in pakeConfirm,
	// so a step sent twice at once cannot drive it from two
	// goroutines.
	u.mu.Lock()
	confirmed := u.pake == s && s.confirmed
	if confirmed {
		u.pake = nil
	}
	u.mu.Unlock()
	if !confirmed {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	sealedKey, err := base64.RawURLEncoding.DecodeString(ask.Key)
	var deviceKey string
	if err == nil {
		u.mu.Lock()
		deviceKey, err = s.responder.OpenKey(sealedKey)
		u.mu.Unlock()
	}
	// The yard's key is read before the code is spent, as a sealed
	// redemption's is: a yard that cannot read it answers no token
	// for a device it then has.
	var channel yardkey.ChannelKey
	if err == nil {
		channel, err = u.pairingChannel()
		if err != nil {
			u.log.Error("a device's PAKE pairing answer could not be sealed", "err", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
	}
	var d *store.NewDevice
	if err == nil {
		d, err = u.redeemPAKE(r, s.pairing, deviceKey)
	}
	if u.answerPairRefusal(w, r, err) {
		return
	}
	if err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	var answer []byte
	if err == nil {
		answer, err = json.Marshal(pakeAnswer{
			Paired:     u.pairedSealed(r.Context(), d),
			ChannelKey: yardkey.ChannelPublic(channel),
		})
	}
	var reply []byte
	if err == nil {
		u.mu.Lock()
		reply, err = s.responder.SealAnswer(answer)
		u.mu.Unlock()
	}
	if err != nil {
		u.log.Error("a device's PAKE pairing answer could not be sealed", "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	u.log.Info("a device was paired", "user", d.User, "device", d.Name)
	// The one answer that holds the token, sealed: nothing between
	// keeps it, and nothing between can read it.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]pakeReply{"pake": {Step: "done", Answer: base64.RawURLEncoding.EncodeToString(reply)}})
}

// redeemPAKE is redeem for the redemption of a confirmed PAKE run
// under pairing, the pairing id its begin handed out: the presenter
// narrowing of redeemKeyed, over the store's PAKE redemption.
func (u *UserServer) redeemPAKE(r *http.Request, pairing, deviceKey string) (*store.NewDevice, error) {
	named := r.Header.Values(PresenterHeader)
	if len(named) == 0 {
		return u.st.DevicePairRedeemPAKE(r.Context(), pairing, deviceKey)
	}
	user, mapped, ok := u.presenter(r.Context(), named)
	if !ok {
		return u.st.DevicePairRedeemPAKEBy(r.Context(), pairing, "", deviceKey)
	}
	if user != "" {
		return u.st.DevicePairRedeemPAKEBy(r.Context(), pairing, user, deviceKey)
	}
	return u.st.DevicePairRedeemPAKEUnmapped(r.Context(), pairing, mapped, deviceKey)
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
