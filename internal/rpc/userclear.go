package rpc

import (
	"net"
	"net/http"
	"strings"
)

// The listener's in-the-clear judgments, moved out of user.go, which
// stands at the file-size ceiling the test gate holds (.yardr/test):
// whether a call that carries a secret, an approval or the operator's
// token is taken from the request that brought it (inTheClear and its
// kin), and the address a request down the link from the edge says.

// linkRemoteAddr is the RemoteAddr of a request that came down the link
// from the edge. It is the link's own address said here, not imported:
// the link's tests serve this package's user server, so the import would
// cycle. A test holds the two equal.
const linkRemoteAddr = "edge:0"

// inTheClear says why the call name of OperatorCalls is not taken from the
// request r, "" when it is taken. router is the yard's router_addr setting:
// the address of its router on the user's network, "" when unset.
// yardr does not do TLS: the listener speaks plain HTTP, and what ends TLS
// stands in front of it. So a secret, and an approval that runs a command
// as the operator, is taken only from a request
// that reached the listener from this machine, which is the proxy or the
// tunnel that ended TLS here, or a client on the machine itself; and not
// from one whose proxy says it came in plain (X-Forwarded-Proto, read for a
// no and never for a yes: whoever asks writes it). It is what the listener
// can see of the client's rule, "https, or loopback" (docs/api.md), and no
// more than that: a proxy on this machine that takes plain HTTP from
// outside and does not say so is not seen.
//
// A hosted yard's router is the one peer besides this machine a secret is
// taken from: the address the setting names, saying the request came to it
// over https (fromRouter). An approval is not taken from it: from the
// router's peer ApproveAsk and ApproveSetup are refused as from anywhere
// else, whatever the setting names.
//
// An approval holds no secret of its own, but for the account a setup's
// approval may type (ApproveSetup's values), which is a giving's. It is held
// to the same rule for what it does: a token read off a network in the clear
// would otherwise have the operator's commands run from wherever it was read.
//
// The refusal comes after the request has crossed, so it says that too.
//
// Over the link from the edge the listener's reason does not hold: the
// request came down the link. Which secrets cross is the yard's to say, and
// only call can say it, since the ask, or the account typed, is in the body:
// crosses is its word that this giving, or this setup approval with the
// account typed, is one the yard takes down the link, and it passes false
// for every call it judges before the body is read. member is whether the
// caller is a user who is not the operator, and google whether the call
// names that member's own Google sign-in. A member's giving crosses for
// the code of their own sign-in from a sealed device (memberLoginCode),
// or for their own Google sign-in from a device paired with the QR
// (memberGoogle); every other is refused in one sentence, the same on a
// hosted yard and on any other, except that a sealed call's refusal
// drops the sentence saying the edge saw what was sent, which crossed
// as ciphertext. A Google giving refused here is refused in its own
// sentence: it names the QR-paired device and the terminal command,
// and never the operator, who is refused that giving. On a hosted yard every secret crosses, for a device of the
// operator: the hosting service runs the edge and the machine the yard lives
// on, so a refusal would guard nothing it does not hold, and would leave a
// person with no shell on that machine unable to give a key at all (hosted).
// On a yard connected to the edge from the person's own machine a giving
// crosses from the operator's device when the call came sealed, whatever it
// gives, since the edge carried it as ciphertext (crossesConnected); a call
// that came plain crosses for one giving alone, the code a harness's sign-in
// page shows (loginCode), which is used once, within minutes, and kept nowhere.
// The refusal of every other plain giving names the two fixes, a sealed device
// and the yard's own machine, and says the edge saw what this one carried, so
// the key is remade. A setup approval that types the account is such a giving
// and is refused sealed or plain, since sealing does not make it cross: its
// refusal answers on the yard's own machine as the fix, and says the edge saw
// what was typed for a plain call alone, as a sealed call's has no such
// sentence, what it sent having crossed the edge as ciphertext. One that takes
// a candidate or keeps the account holds no secret and crosses.
// An approval crosses on either: a paired device of the operator is the
// operator (design yardr-xpf8), pairing is the grant and `yardr user device
// rm` is the revoke, and the command an approval runs is fixed on the ask. A
// session's screen crosses to the same device: pairing is the grant there
// too, and a member's device is answered as for a method the yard does not
// have (operatorCall). The teammates list and a teammate's removal filing cross
// to the same device: a hosted yard's teammates are read and removed from
// the operator's paired device. The edge sees the device's token with
// each, and what an approval or a giving writes says it came by the edge
// (call). If the edge says the device reached it over plain HTTP, the
// approval, the secret, the screen, the list and the filing are still
// refused.
func inTheClear(r *http.Request, name, router string, crosses, member, google bool) string {
	if name == "SourceGrants" {
		return sourceGrantsInTheClear(r, router)
	}
	if name == "SessionScreen" {
		return sessionScreenInTheClear(r, router)
	}
	if name == "Line" {
		return lineInTheClear(r, router)
	}
	if name == "Teammates" {
		return teammatesInTheClear(r, router, "the teammates list", "Read it on the yard's machine.")
	}
	if name == "UninviteTeammate" {
		return teammatesInTheClear(r, router, "filing a teammate's removal", "File it on the yard's machine.")
	}
	if name == "TelemetryShow" {
		return telemetryShowInTheClear(r, router)
	}
	if name == "TelemetrySet" {
		return telemetrySetInTheClear(r)
	}
	what, flag := "a secret", "--secret"
	crossed := " What was sent may have crossed a network in the clear: if it was a real key, make a new one at the service."
	if name == "ApproveAsk" || name == "ApproveSetup" {
		what, flag = "an approval", "--approve"
		if name == "ApproveSetup" {
			flag = "<n> --secret"
		}
		crossed = " The device's token was sent with it and may have crossed a network in the clear: if it did, remove the device and add it again (yardr user device rm)."
	}
	if r.RemoteAddr == linkRemoteAddr {
		if name == "ApproveAsk" || (name == "ApproveSetup" || name == "GiveSecret") && crosses {
			// A device of the operator reaches here (operatorCall), and a
			// member's only for their own pairing approval (ownPairApproval)
			// and, as call says crosses, the code of their own sign-in from
			// a sealed device (memberLoginCode) or their own Google
			// sign-in from a device paired with the QR (memberGoogle).
			if forwardedPlain(r) {
				return what + " is taken over https only, or from the yard's own machine: the proxy in front of the yard says this request came to it in plain HTTP. " +
					"Give the device the yard's https:// address." + crossed
			}
			return ""
		}
		const seen = " The edge saw what was sent with it: if it was a real key, make a new one at the service."
		// A call that came sealed crossed the edge as ciphertext, so the
		// edge read nothing of what was sent with it and the sentence
		// saying it did is dropped; a plain call keeps it. Only
		// serveInner sets the session, so nothing a client sends claims
		// a call came sealed.
		tail := seen
		if _, sealed := r.Context().Value(e2eSessionKey{}).(*e2eSession); sealed {
			tail = ""
		}
		if member {
			if google {
				// The operator is refused this giving, so none is named
				// as its giver: the doors named are the member's own, a
				// device paired with the QR and a terminal of theirs.
				return "your Google sign-in does not cross the edge from this device: it crosses only from a device paired with the QR (pairing the device with the QR makes it one), " +
					"or in a terminal (yardr bead answer <bead> <ask> --secret); it is yours alone to give, and the operator does not give it." + tail
			}
			// The member has no shell on the yard's machine, so no ssh is
			// offered, and the operator's device gives from a sealed call, or
			// from any call where the yard is hosted. The sentence is one
			// either way; only the tail differs, since a sealed call crossed
			// as ciphertext.
			return "a secret from a member's device does not cross the edge, except the code of their own sign-in from a sealed device (pairing the device with the QR makes it one); " +
				"the operator gives any other on the yard's machine, from their own sealed device, or from any paired device of theirs where the yard is hosted." + tail
		}
		if name == "GiveSecret" {
			// The operator's sealed giving crossed above (crossesConnected),
			// so this one came plain: a sealed device is a way to give it
			// beside the yard's machine.
			return "a secret does not cross the edge in a call that is not sealed: this request came down the link from the edge. " +
				"Give it from a sealed device (pairing the device with the QR makes it one), or answer on the yard's machine (ssh -t <host> yardr bead answer <bead> <ask> " + flag + ")." + tail
		}
		// An approval that types the account is refused sealed or plain:
		// sealing the device is no way to give it, so the machine is the one
		// fix named.
		return "an account typed into an approval never crosses the edge: this request came down the link from the edge. " +
			"Answer on the yard's machine (ssh -t <host> yardr bead answer <bead> <ask> " + flag + ")." + tail
	}
	if ip := net.ParseIP(remote(r)); ip == nil || !ip.IsLoopback() {
		if name == "GiveSecret" && fromRouter(r, router) {
			return ""
		}
		return what + " is taken over https only, or from the yard's own machine: this request reached the yard's listener in plain HTTP from another machine. " +
			"Put the proxy or tunnel that ends TLS on the yard's machine, or answer there (ssh -t <host> yardr bead answer <bead> <ask> " + flag + ")." + crossed
	}
	if forwardedPlain(r) {
		return what + " is taken over https only, or from the yard's own machine: the proxy in front of the yard says this request came to it in plain HTTP. " +
			"Give the device the yard's https:// address." + crossed
	}
	return ""
}

// forwardedPlain reports whether any forwarded-proto value says the
// request arrived over plain HTTP. The header can refuse a call, never
// establish that a request was secure.
func forwardedPlain(r *http.Request) bool {
	for _, proto := range r.Header.Values("X-Forwarded-Proto") {
		for _, p := range strings.Split(proto, ",") {
			if !strings.EqualFold(strings.TrimSpace(p), "https") {
				return true
			}
		}
	}
	return false
}

// sourceGrantsInTheClear keeps the operator's bearer token under the same
// transport rule as the other operator calls, while naming this read's
// command and the device to replace if the token crossed in plain.
func sourceGrantsInTheClear(r *http.Request, router string) string {
	const crossed = " The device's token was sent with it and may have crossed a network in the clear: if it did, remove the device and add it again (yardr user device rm)."
	const readHere = "Read it on the yard's machine (`yardr source grants`)."
	if r.RemoteAddr == linkRemoteAddr {
		return "the operator's grant list is not read over the link: this request came down the link from the edge. " +
			readHere + " The edge saw the device's token with it: remove the device and add it again (yardr user device rm)."
	}
	if ip := net.ParseIP(remote(r)); ip == nil || !ip.IsLoopback() {
		if fromRouter(r, router) {
			return ""
		}
		return "the operator's grant list is taken over https only, or from the yard's own machine: this request reached the yard's listener in plain HTTP from another machine. " +
			"Put the proxy or tunnel that ends TLS on the yard's machine, or " + readHere + crossed
	}
	for _, proto := range r.Header.Values("X-Forwarded-Proto") {
		for _, p := range strings.Split(proto, ",") {
			if !strings.EqualFold(strings.TrimSpace(p), "https") {
				return "the operator's grant list is taken over https only, or from the yard's own machine: the proxy in front of the yard says this request came to it in plain HTTP. " +
					"Give the device the yard's https:// address, or " + readHere + crossed
			}
		}
	}
	return ""
}

// sessionScreenInTheClear keeps a session's screen under the same transport
// rule as the other operator calls: a screen can show what an agent
// printed, a secret among it, so it is read over https or from the yard's
// own machine, and down the link from the edge from a paired device of the
// operator, as an approval is.
func sessionScreenInTheClear(r *http.Request, router string) string {
	const crossed = " The device's token was sent with it and may have crossed a network in the clear: if it did, remove the device and add it again (yardr user device rm)."
	const readHere = "Read it on the yard's machine."
	if r.RemoteAddr == linkRemoteAddr {
		// Only a device of the operator reaches here (operatorCall).
		if forwardedPlain(r) {
			return "a session's screen is taken over https only, or from the yard's own machine: the proxy in front of the yard says this request came to it in plain HTTP. " +
				"Give the device the yard's https:// address, or " + readHere + crossed
		}
		return ""
	}
	if ip := net.ParseIP(remote(r)); ip == nil || !ip.IsLoopback() {
		if fromRouter(r, router) {
			return ""
		}
		return "a session's screen is taken over https only, or from the yard's own machine: this request reached the yard's listener in plain HTTP from another machine. " +
			"Put the proxy or tunnel that ends TLS on the yard's machine, or " + readHere + crossed
	}
	for _, proto := range r.Header.Values("X-Forwarded-Proto") {
		for _, p := range strings.Split(proto, ",") {
			if !strings.EqualFold(strings.TrimSpace(p), "https") {
				return "a session's screen is taken over https only, or from the yard's own machine: the proxy in front of the yard says this request came to it in plain HTTP. " +
					"Give the device the yard's https:// address, or " + readHere + crossed
			}
		}
	}
	return ""
}

// lineInTheClear keeps a depot's line under the same transport rule as a
// session's screen: a cmd and a prompt are the operator's to read, so the
// line is read over https or from the yard's own machine, and down the
// link from the edge from a paired device of the operator, as an approval
// is. The rule is SessionScreen's; the words are the line's own.
func lineInTheClear(r *http.Request, router string) string {
	const crossed = " The device's token was sent with it and may have crossed a network in the clear: if it did, remove the device and add it again (yardr user device rm)."
	const readHere = "Read it on the yard's machine."
	if r.RemoteAddr == linkRemoteAddr {
		// Only a device of the operator reaches here (operatorCall).
		if forwardedPlain(r) {
			return "a depot's line is taken over https only, or from the yard's own machine: the proxy in front of the yard says this request came to it in plain HTTP. " +
				"Give the device the yard's https:// address, or " + readHere + crossed
		}
		return ""
	}
	if ip := net.ParseIP(remote(r)); ip == nil || !ip.IsLoopback() {
		if fromRouter(r, router) {
			return ""
		}
		return "a depot's line is taken over https only, or from the yard's own machine: this request reached the yard's listener in plain HTTP from another machine. " +
			"Put the proxy or tunnel that ends TLS on the yard's machine, or " + readHere + crossed
	}
	for _, proto := range r.Header.Values("X-Forwarded-Proto") {
		for _, p := range strings.Split(proto, ",") {
			if !strings.EqualFold(strings.TrimSpace(p), "https") {
				return "a depot's line is taken over https only, or from the yard's own machine: the proxy in front of the yard says this request came to it in plain HTTP. " +
					"Give the device the yard's https:// address, or " + readHere + crossed
			}
		}
	}
	return ""
}

// teammatesInTheClear keeps a teammates call under the same transport rule
// as an approval: the list names the yard's addresses, and the filing is
// the operator's, so both are read and filed over https or from the yard's
// own machine, and down the link from the edge from a paired device of the
// operator, as an approval is. what names the call in a refusal, and
// readHere the fix on the yard's machine.
func teammatesInTheClear(r *http.Request, router, what, readHere string) string {
	const crossed = " The device's token was sent with it and may have crossed a network in the clear: if it did, remove the device and add it again (yardr user device rm)."
	if r.RemoteAddr == linkRemoteAddr {
		// Only a device of the operator reaches here (operatorCall).
		if forwardedPlain(r) {
			return what + " is taken over https only, or from the yard's own machine: the proxy in front of the yard says this request came to it in plain HTTP. " +
				"Give the device the yard's https:// address, or " + readHere + crossed
		}
		return ""
	}
	if ip := net.ParseIP(remote(r)); ip == nil || !ip.IsLoopback() {
		if fromRouter(r, router) {
			return ""
		}
		return what + " is taken over https only, or from the yard's own machine: this request reached the yard's listener in plain HTTP from another machine. " +
			"Put the proxy or tunnel that ends TLS on the yard's machine, or " + readHere + crossed
	}
	for _, proto := range r.Header.Values("X-Forwarded-Proto") {
		for _, p := range strings.Split(proto, ",") {
			if !strings.EqualFold(strings.TrimSpace(p), "https") {
				return what + " is taken over https only, or from the yard's own machine: the proxy in front of the yard says this request came to it in plain HTTP. " +
					"Give the device the yard's https:// address, or " + readHere + crossed
			}
		}
	}
	return ""
}

// operatorInTheClear is inTheClear for a call of OperatorCalls that the
// listener judges before it reads the body, which is every one but a giving
// and a setup approval down the link. A save of a stage file and a copy of a
// depot's repository flows have a rule of their own (stageWriteInTheClear),
// which asks whether the yard is hosted only down the link, and only when the
// answer decides. Any other call is judged by inTheClear as it always was and
// reads of the yard's settings the router's address alone, so a call that
// has no use for the hosted setting costs no read of it.
func (u *UserServer) operatorInTheClear(r *http.Request, name string) string {
	if name == "PutStageFile" || name == "CopyRepoFlows" {
		return stageWriteInTheClear(r, name, func() bool { return u.hosted(r.Context()) })
	}
	return inTheClear(r, name, u.routerAddr(r.Context()), false, false, false)
}

// stageWriteInTheClear says why a save of a stage file or a copy of a depot's
// repository flows (PutStageFile, CopyRepoFlows) is not taken from the request
// r, "" when it is. What they write is what every bead of a flow is shown and
// run by, and the operator's device token goes with the request, so they are
// held to an approval's rule, which teammatesInTheClear holds whatever the call:
// over https or from the yard's own machine, never from a request whose proxy
// says it came in plain, and never from the router's peer, which the empty
// router passed here names none of.
//
// Down the link from the edge a device of the operator (operatorCall) is taken
// on a hosted yard, whose service runs the edge and the machine the yard lives
// on, and on any other yard only when the call came sealed, since the edge
// carries it as ciphertext. A plain call there lets an edge that reads the
// device's token save text the yard's agents are then told, so it is refused,
// naming the two fixes, a sealed device and the yard's own machine, and saying
// that the token crossed in plain. hosted is asked only of a call that came
// down the link and is not sealed: a sealed call is taken on any yard, and off
// the link the answer decides nothing.
func stageWriteInTheClear(r *http.Request, name string, hosted func() bool) string {
	what, here := "saving a stage file", "save it on the yard's machine (`yardr flow set-stage`)."
	if name == "CopyRepoFlows" {
		what, here = "copying a depot's repository flows", "copy them on the yard's machine (`yardr flow copy-repo`)."
	}
	if r.RemoteAddr == linkRemoteAddr {
		// Only serveInner sets the session, so nothing a client sends claims
		// a call came sealed.
		if _, sealed := r.Context().Value(e2eSessionKey{}).(*e2eSession); !sealed && !hosted() {
			return what + " does not cross the edge in a call that is not sealed: this request came down the link from the edge. " +
				"Make it from a sealed device (pairing the device with the QR makes it one), or " + here +
				" The edge saw the device's token with it: remove the device and add it again (yardr user device rm)."
		}
	}
	return teammatesInTheClear(r, "", what, here)
}

// telemetryShowInTheClear keeps the telemetry preview under the same
// transport rule as the other operator calls: the install id groups the
// yard's events across time, so it is read over https or from the yard's
// own machine, and never down the link from the edge, on any yard: the
// id stays off the link with the grant list.
func telemetryShowInTheClear(r *http.Request, router string) string {
	const crossed = " The device's token was sent with it and may have crossed a network in the clear: if it did, remove the device and add it again (yardr user device rm)."
	const readHere = "Read it on the yard's machine (`yardr telemetry show`)."
	if r.RemoteAddr == linkRemoteAddr {
		return "the telemetry switch is not read over the link: this request came down the link from the edge. " +
			readHere + " The edge saw the device's token with it: remove the device and add it again (yardr user device rm)."
	}
	if ip := net.ParseIP(remote(r)); ip == nil || !ip.IsLoopback() {
		if fromRouter(r, router) {
			return ""
		}
		return "the telemetry switch is taken over https only, or from the yard's own machine: this request reached the yard's listener in plain HTTP from another machine. " +
			"Put the proxy or tunnel that ends TLS on the yard's machine, or " + readHere + crossed
	}
	for _, proto := range r.Header.Values("X-Forwarded-Proto") {
		for _, p := range strings.Split(proto, ",") {
			if !strings.EqualFold(strings.TrimSpace(p), "https") {
				return "the telemetry switch is taken over https only, or from the yard's own machine: the proxy in front of the yard says this request came to it in plain HTTP. " +
					"Give the device the yard's https:// address, or " + readHere + crossed
			}
		}
	}
	return ""
}

// telemetrySetInTheClear keeps the telemetry switch under the same
// transport rule as an approval: consent is changed over https or from
// the yard's own machine, and never down the link from the edge or from
// the router's peer, on any yard. No edge request may turn it on (design
// yardr-fwhv, section 3.6).
func telemetrySetInTheClear(r *http.Request) string {
	const crossed = " The device's token was sent with it and may have crossed a network in the clear: if it did, remove the device and add it again (yardr user device rm)."
	const changeHere = "Change it on the yard's machine (`yardr telemetry on` or `yardr telemetry off`)."
	if r.RemoteAddr == linkRemoteAddr {
		return "the telemetry switch is not changed over the link: this request came down the link from the edge. " +
			changeHere + " The edge saw the device's token with it: remove the device and add it again (yardr user device rm)."
	}
	if ip := net.ParseIP(remote(r)); ip == nil || !ip.IsLoopback() {
		return "the telemetry switch is taken over https only, or from the yard's own machine: this request reached the yard's listener in plain HTTP from another machine. " +
			"Put the proxy or tunnel that ends TLS on the yard's machine, or " + changeHere + crossed
	}
	for _, proto := range r.Header.Values("X-Forwarded-Proto") {
		for _, p := range strings.Split(proto, ",") {
			if !strings.EqualFold(strings.TrimSpace(p), "https") {
				return "the telemetry switch is taken over https only, or from the yard's own machine: the proxy in front of the yard says this request came to it in plain HTTP. " +
					"Give the device the yard's https:// address, or " + changeHere + crossed
			}
		}
	}
	return ""
}

// fromRouter reports whether the request r reached the listener from the
// yard's router: the peer the router_addr setting names, saying the
// request came to it over https. The header is read for a yes here, as it
// is read for a no from the yard's own machine: the router is the edge,
// and a secret is taken from it only for what came to it over https.
// Anything else — no header, or any word but https — is not from the
// router. The peer is compared as an address, not as words, so the
// setting's form of it does not matter.
func fromRouter(r *http.Request, router string) bool {
	addr := net.ParseIP(router)
	peer := net.ParseIP(remote(r))
	if addr == nil || peer == nil || !peer.Equal(addr) {
		return false
	}
	protos := r.Header.Values("X-Forwarded-Proto")
	if len(protos) == 0 {
		return false
	}
	for _, proto := range protos {
		for _, p := range strings.Split(proto, ",") {
			if !strings.EqualFold(strings.TrimSpace(p), "https") {
				return false
			}
		}
	}
	return true
}
