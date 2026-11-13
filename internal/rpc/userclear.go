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
// caller is a user who is not the operator. A member's giving crosses for the
// code of their own sign-in from a sealed device alone (memberLoginCode); every
// other is refused in one sentence, the same on a hosted yard and on any other,
// plain or sealed. On a hosted yard every secret crosses, for a device of the
// operator: the hosting service runs the edge and the machine the yard lives
// on, so a refusal would guard nothing it does not hold, and would leave a
// person with no shell on that machine unable to give a key at all (hosted).
// On a yard connected to the edge from the person's own machine one giving
// crosses from the operator's device, the code a harness's sign-in page shows
// (loginCode), which is used once, within minutes, and kept nowhere: the
// refusal of every other names that reason, answers on the yard's own machine
// as the fix, and says the edge saw what this one carried, so the key is
// remade. A setup approval that types the account is such a giving and is
// refused the same way; one that takes a candidate or keeps the account holds
// no secret and crosses.
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
func inTheClear(r *http.Request, name, router string, crosses, member bool) string {
	if name == "SourceGrants" {
		return sourceGrantsInTheClear(r, router)
	}
	if name == "SessionScreen" {
		return sessionScreenInTheClear(r, router)
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
			// a sealed device (memberLoginCode).
			if forwardedPlain(r) {
				return what + " is taken over https only, or from the yard's own machine: the proxy in front of the yard says this request came to it in plain HTTP. " +
					"Give the device the yard's https:// address." + crossed
			}
			return ""
		}
		const seen = " The edge saw what was sent with it: if it was a real key, make a new one at the service."
		if member {
			// The member has no shell on the yard's machine, so no ssh is
			// offered, and the operator's device gives only where the yard is
			// hosted. Nothing here says whether the call came plain or
			// sealed: a sealed device is told what makes one, and is never
			// told its own is refused.
			return "a secret from a member's device does not cross the edge, except the code of their own sign-in from a sealed device (pairing the device with the QR makes it one); " +
				"the operator gives any other on the yard's machine, or from their own paired device where the yard is hosted." + seen
		}
		return "a secret never crosses the edge: this request came down the link from the edge. " +
			"Answer on the yard's machine (ssh -t <host> yardr bead answer <bead> <ask> " + flag + ")." + seen
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
