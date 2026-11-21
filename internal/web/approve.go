package web

// A weighty card's approval in the hosted web (design yardr-2nb1 5.1): the
// bead page approves run and setup cards, weight first with the yard's
// sentences, and a weighty card — any class in its stored weight — is
// approved only under a fresh sign-in. Approving one starts the gateway
// re-authentication unless the session's last authentication was a
// weighty-card re-authentication by this user inside the window,
// whichever weighty card started it; a standing login, however recent,
// never counts. The return state is one-use and bound to its card,
// consumed only for it, and the store takes the approval only with the
// request's fresh witness. Anywhere but the hosted web a weighty card
// stays terminal-only, in footplate or at a terminal.
//
// A secret card's giving on its bead's page rides the same sign-in: the
// typed values wait in a secret challenge for its short bound, and the
// return gives them with the same witness. StrongWeight is untouched:
// a secret ask weighs credential, never trust or code.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"yardr.dev/yardr/internal/store"
)

// freshPendingTTL bounds a challenge left at the gateway: one never
// finished is dropped after the hour, past any window it could have been
// used in.
const freshPendingTTL = time.Hour

// secretPendingTTL bounds a challenge that carries typed secret values, a
// secret ask's or a setup card's account: the gateway's fresh window, far shorter than an approval's hour, since
// the values wait in this view's memory alone until the sign-in returns.
// Past it the challenge and its values are dropped, on the next save,
// lookup or spend, whether or not another challenge ever comes.
const secretPendingTTL = 15 * time.Minute

// challengeTTL is how long ch may wait at the gateway: the short bound
// for one holding typed secret values (a secret ask's, a setup's
// account), any other's hour.
func challengeTTL(ch freshChallenge) time.Duration {
	if ch.secret || ch.typed {
		return secretPendingTTL
	}
	return freshPendingTTL
}

// freshChallenge is a sign-in waiting at the gateway: the card it was
// raised for and who raised it, when, and what it carries back (a weighty
// card's approval: a setup's preset pick, account and host, a run's
// values; or, where secret is set, a secret ask's typed values, given on
// the return instead of approved).
type freshChallenge struct {
	bead    string
	ask     uint64
	user    string
	at      time.Time
	option  int
	values  map[string]string
	account string
	host    string
	secret  bool
	// typed is whether values are a setup card's typed account secrets:
	// every setup card is weighty (its account is a credential), so each
	// one approved in the hosted web waits here with what was typed, and
	// waits no longer than a secret ask's values do.
	typed bool
}

// freshAuth is what a consumed challenge returned: the authentication time
// the gateway's token carried, and the challenge it answered. One fresh
// sign-in witnesses several weighty cards inside the window, so a later
// card whose token carries the same time approves without a new sign-in.
type freshAuth struct {
	auth  time.Time
	since time.Time
}

// freshState mints one-use return state for a challenge: 256 random bits,
// base64url, carrying nothing itself.
func freshState() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// putFresh saves ch under state, first dropping every challenge past its
// own bound (challengeTTL): a secret challenge's values go with it, so a
// typed value waits no longer than its short bound even where no return
// ever comes for it.
func (s *Server) putFresh(state string, ch freshChallenge) {
	s.freshMu.Lock()
	defer s.freshMu.Unlock()
	for st, old := range s.freshPending {
		if time.Since(old.at) > challengeTTL(old) {
			delete(s.freshPending, st)
		}
	}
	s.freshPending[state] = ch
}

// lookupFresh is the challenge waiting under state, spent where it is
// past its bound: an expired secret challenge's values are dropped with
// it here, whether or not another challenge comes.
func (s *Server) lookupFresh(state string) (freshChallenge, bool) {
	s.freshMu.Lock()
	defer s.freshMu.Unlock()
	ch, ok := s.freshPending[state]
	if ok && time.Since(ch.at) > challengeTTL(ch) {
		delete(s.freshPending, state)
		return freshChallenge{}, false
	}
	return ch, ok
}

// freshRedirect sends the browser to the gateway's re-authentication,
// returning to returnTo: a 303, except for a request htmx made (the bead
// page's forms), which is answered 200 with HX-Redirect so the browser
// navigates whole, as approveStrong's step has always been.
func freshRedirect(w http.ResponseWriter, r *http.Request, returnTo string) {
	reauth := "/edge/reauth?return_to=" + url.QueryEscape(returnTo)
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", reauth)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, reauth, http.StatusSeeOther)
}

// beadApprove approves the run or setup card ask, as the request's user. A
// routine card is approved as it always was, asking for no witness; a
// weighty card goes through its sign-in (approveStrong).
func (s *Server) beadApprove(w http.ResponseWriter, r *http.Request, b *store.Bead, ask store.BeadAsk) {
	ctx := r.Context()
	user := s.acting(ctx)
	option, values, account, host, ok := s.approveParams(w, r, b, ask)
	if !ok {
		return
	}
	if !store.Weighty(ask.Weight) {
		done, err := s.approveNow(ctx, b.ID, ask, option, values, account, host, user, store.FreshWitness{}, false)
		if err != nil {
			s.beadAgain(w, r, b.ID, err.Error())
			return
		}
		if done != nil && done.Pairing != nil {
			s.pairingApproved(w, r, b.ID, done.Pairing)
			return
		}
		http.Redirect(w, r, "/beads/"+url.PathEscape(b.ID), http.StatusSeeOther)
		return
	}
	s.approveStrong(w, r, b, ask, option, values, account, host)
}

// pairingOwn reports whether ask is a device pair card asked of user
// themselves: the one strong card a member approves in the hosted web
// without being an operator, for their own first device, under the
// same fresh sign-in (design yardr-6maq 3.4, question 3). Its fresh
// witness is judged as an operator's is, without the operator's
// session the fresh gate otherwise stands on (freshSession).
func pairingOwn(ask store.BeadAsk, user string) bool {
	return ask.To == user && ask.Run != nil && store.IsDevicePairRun(ask.Run.Argv) && ask.Run.Argv[3] == user
}

// userkeyOwn reports whether ask is a user key card for user's own
// address in a source, asked of user themselves: the weighty card a
// member approves in the hosted web beside their own pairing, as
// their own first-device pairing is approved there, under the same
// fresh sign-in. The card's user (its argv's, store.IsUserKeyRun)
// must be the user the card is asked of and the user acting.
func userkeyOwn(ask store.BeadAsk, user string) bool {
	return ask.To == user && ask.Run != nil && store.IsUserKeyRun(ask.Run.Argv) && ask.Run.Argv[2] == user
}

// ownCard reports whether ask is a card user may approve as their own
// in the hosted web without being an operator: their own device
// pairing, or their own user key.
func ownCard(ask store.BeadAsk, user string) bool {
	return pairingOwn(ask, user) || userkeyOwn(ask, user)
}

// freshFor is the fresh sign-in's word for ask as user: the operator's
// (freshSession), or the named user's own on their own pairing or
// user key card.
func (s *Server) freshFor(ctx context.Context, ask store.BeadAsk, user string, since time.Time) bool {
	if s.freshSession(ctx, since) {
		return true
	}
	if s.jwt == nil || !ownCard(ask, user) {
		return false
	}
	return jwtIdentityOf(ctx).freshAt(time.Now(), s.jwt.privilegedSessionMaxAge, since)
}

// approveAgain is what goes after "yardr bead answer <bead> <ask>" to
// approve ask where a gate stands: a run's approval, a setup's preset
// pick with its account, and the host typed beside it where it is one word of
// the characters a host is made of. Anything else the form held is left out of
// a command a person will copy: the card says how a host is given, and the
// command refuses what is no host.
func approveAgain(ask store.BeadAsk, host string) string {
	switch {
	case ask.Setup == nil:
		return "--approve"
	case host != "" && strings.Trim(host, hostWordChars) == "":
		return "<n> --secret --host " + host
	}
	return "<n> --secret"
}

// hostWordChars are the characters of a host a terminal command may carry as
// one word with no quoting: the letters, digits and punctuation of a name, an
// IPv4 address or a port.
const hostWordChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.-_:"

// approveParams reads the approval the form posts for ask: a setup's
// preset pick with the account typed beside it or the candidate taken
// instead, and the host of a server of the operator's own where the card
// asked one, a run's values where the card asks for any. An ask that is
// neither is refused with the page redrawn.
func (s *Server) approveParams(w http.ResponseWriter, r *http.Request, b *store.Bead, ask store.BeadAsk) (option int, values map[string]string, account, host string, ok bool) {
	switch {
	case ask.Setup != nil:
		n, err := strconv.Atoi(r.PostFormValue("option"))
		if err != nil || n < 1 || n > len(ask.Options) {
			s.beadAgain(w, r, b.ID, fmt.Sprintf("ask %d sets up %s: pick one of its %d presets", ask.Ask, ask.Setup.Pack, len(ask.Options)))
			return 0, nil, "", "", false
		}
		option = n
		for _, name := range ask.Setup.Account {
			if v := r.PostFormValue("secret-" + name); v != "" {
				if values == nil {
					values = map[string]string{}
				}
				values[name] = v
			}
		}
		account = r.PostFormValue("account")
		return option, values, account, strings.TrimSpace(r.PostFormValue("host")), true
	case ask.Run != nil:
		for _, name := range ask.Run.Ask {
			if values == nil {
				values = map[string]string{}
			}
			values[name] = r.PostFormValue("value-" + name)
		}
		return 0, values, "", "", true
	default:
		s.beadAgain(w, r, b.ID, fmt.Sprintf("ask %d is answered, not approved: pick an option, answer with words, put it aside with Later, or decline it", ask.Ask))
		return 0, nil, "", "", false
	}
}

// approveStrong approves a weighty card under a fresh sign-in. Anywhere but
// the hosted web — no gateway, or a yard that is not hosted — the card
// stays terminal-only, in footplate or at a terminal. Where the session's
// last authentication is already a weighty-card re-authentication by this
// user inside the window, the card approves at once; otherwise the gateway
// authenticates now, and the one-use state returns to this card alone. The
// step to the gateway is a 303, except for a request htmx made (the bead
// page's forms), which is answered 200 with HX-Redirect so the browser
// navigates whole. A non-operator's click on a card that is not their own
// pairing or user key is refused here, after the gateless refusals and
// before any sign-in round trip: an operator's sign-in is what the page
// takes for a weighty card that is not the clicker's own.
func (s *Server) approveStrong(w http.ResponseWriter, r *http.Request, b *store.Bead, ask store.BeadAsk, option int, values map[string]string, account, host string) {
	ctx := r.Context()
	user := s.acting(ctx)
	terminal := fmt.Sprintf("ask %d is %s: a weighty card is not approved in this window. Approve it in footplate, or at a terminal on the yard's machine: yardr bead answer %s %d %s",
		ask.Ask, store.WeightyWords(ask.Weight), b.ID, ask.Ask, approveAgain(ask, host))
	if s.jwt == nil {
		s.beadAgain(w, r, b.ID, terminal)
		return
	}
	settings, err := s.st.Settings(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if settings[store.HostedSetting] != "true" {
		s.beadAgain(w, r, b.ID, terminal)
		return
	}
	if !s.isOperator(ctx) && !ownCard(ask, user) {
		s.beadAgain(w, r, b.ID, fmt.Sprintf("ask %d is %s: an operator's sign-in is what this page takes for a weighty card that is not your own. Approve it in footplate, or at a terminal on the yard's machine: yardr bead answer %s %d %s",
			ask.Ask, store.WeightyWords(ask.Weight), b.ID, ask.Ask, approveAgain(ask, host)))
		return
	}
	if auth, since, ok := s.strongAuth(ctx, user); ok && s.freshFor(ctx, ask, user, since) {
		fw := store.FreshWitness{User: user, AuthTime: auth}
		done, err := s.approveNow(ctx, b.ID, ask, option, values, account, host, user, fw, true)
		if err != nil {
			s.beadAgain(w, r, b.ID, err.Error())
			return
		}
		if done != nil && done.Pairing != nil {
			s.pairingApproved(w, r, b.ID, done.Pairing)
			return
		}
		http.Redirect(w, r, "/beads/"+url.PathEscape(b.ID), http.StatusSeeOther)
		return
	}
	state := freshState()
	s.putFresh(state, freshChallenge{bead: b.ID, ask: ask.Ask, user: user, at: time.Now(),
		option: option, values: values, account: account, host: host, typed: ask.Setup != nil && len(values) > 0})
	to := "/beads/" + url.PathEscape(b.ID) + "/fresh/" + strconv.FormatUint(ask.Ask, 10) + "/" + state
	// htmx follows a 303 as an XHR, and the sign-in page it reaches has no
	// #bead for the form's hx-select to take: the bead's contents are
	// swapped for nothing, the card and its form vanish, and the person
	// never gets to the sign-in. HX-Redirect makes htmx navigate to the URL
	// as a whole page (freshRedirect).
	freshRedirect(w, r, to)
}

// strongAuth is the authentication time the acting user's last consumed
// challenge returned, with the challenge it answered: ok false
// where no challenge has returned one for them on this view, or the
// request's token carries another time. A standing login, however recent,
// is never one a challenge returned.
func (s *Server) strongAuth(ctx context.Context, user string) (auth, since time.Time, ok bool) {
	id := jwtIdentityOf(ctx)
	if id.authTime.IsZero() {
		return time.Time{}, time.Time{}, false
	}
	s.freshMu.Lock()
	defer s.freshMu.Unlock()
	st, ok := s.freshStrong[user]
	if !ok || !st.auth.Equal(id.authTime) {
		return time.Time{}, time.Time{}, false
	}
	return st.auth, st.since, true
}

// handleFresh takes the gateway's return after a weighty card's sign-in:
// the one-use state the approve minted, bound to its card. It consumes
// the state and approves the card with the request's fresh witness, and
// lands on the bead; a replayed or foreign state, a stale sign-in, or a
// card that moved on re-reads the bead with the reason.
func (s *Server) handleFresh(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	n, err := strconv.ParseUint(r.PathValue("ask"), 10, 64)
	if err != nil || n == 0 {
		s.beadAgain(w, r, id, "that sign-in names no ask: approve the card again")
		return
	}
	state := r.PathValue("state")
	ch, ok := s.lookupFresh(state)
	if !ok {
		s.beadAgain(w, r, id, "that sign-in was used already, expired, or never started: approve the card again")
		return
	}
	ctx := r.Context()
	user := s.acting(ctx)
	if ch.bead != id || ch.ask != n || ch.user != user {
		s.beadAgain(w, r, id, "that sign-in was raised for another card: approve this one again")
		return
	}
	// The card may have moved while its operator signed in: approve what
	// stands now, as the bead's page reads it, and let the store judge the
	// rest.
	asks, err := s.openBeadAsks(ctx, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var ask *store.BeadAsk
	for i := range asks {
		if asks[i].Ask == ch.ask {
			ask = &asks[i]
		}
	}
	if ask == nil {
		s.beadAgain(w, r, id, fmt.Sprintf("ask %d on bead %s is answered already, or asked of another; read it again", ch.ask, id))
		return
	}
	if ch.secret {
		s.giveFreshSecret(w, r, state, ch, *ask, user)
		return
	}
	if !s.freshFor(ctx, *ask, user, ch.at) {
		s.beadAgain(w, r, id, "that sign-in is not fresh: the gateway authenticated before the card's challenge, or outside its window. Approve the card again")
		return
	}
	auth := jwtIdentityOf(ctx).authTime
	if !s.consumeFresh(state, ch, user, auth) {
		s.beadAgain(w, r, id, "that sign-in was used already, expired, or never started: approve the card again")
		return
	}
	fw := store.FreshWitness{User: user, AuthTime: auth}
	done, err := s.approveNow(ctx, id, *ask, ch.option, ch.values, ch.account, ch.host, user, fw, true)
	if err != nil {
		s.beadAgain(w, r, id, err.Error())
		return
	}
	if done != nil && done.Pairing != nil {
		s.pairingApproved(w, r, id, done.Pairing)
		return
	}
	http.Redirect(w, r, "/beads/"+url.PathEscape(id), http.StatusSeeOther)
}

// giveFreshSecret is handleFresh for a secret challenge: it gives the
// values the challenge held, under the return's fresh sign-in, and lands
// on the bead. It stands on the operator's fresh session alone (a member
// gives no secret in the hosted web), spends the one-use state before it
// gives, and clears the held values whatever the giving does. No error
// it shows carries a value: the store's refusals name no value, and the
// redrawn page's fields stand empty.
func (s *Server) giveFreshSecret(w http.ResponseWriter, r *http.Request, state string, ch freshChallenge, ask store.BeadAsk, user string) {
	ctx := r.Context()
	id := ch.bead
	if ask.Secret == nil {
		s.beadAgain(w, r, id, fmt.Sprintf("ask %d asks for no secret: give the secret again", ch.ask))
		return
	}
	if !s.freshSession(ctx, ch.at) {
		s.beadAgain(w, r, id, "that sign-in is not fresh: the gateway authenticated before the card's challenge, or outside its window. Give the secret again")
		return
	}
	auth := jwtIdentityOf(ctx).authTime
	if !s.consumeFresh(state, ch, user, auth) {
		s.beadAgain(w, r, id, "that sign-in was used already, expired, or never started: give the secret again")
		return
	}
	values := ch.values
	_, err := s.st.GiveSecret(store.WithFreshWitness(ctx, user, auth), id, ch.ask, values, user, true)
	for name := range values {
		delete(values, name)
	}
	ch.values = nil
	if err != nil {
		s.beadAgain(w, r, id, err.Error())
		return
	}
	http.Redirect(w, r, "/beads/"+url.PathEscape(id), http.StatusSeeOther)
}

// consumeFresh atomically spends the one-use return state and remembers the
// authentication it returned. Two callbacks racing on the same state cannot
// both carry that sign-in to an approval.
func (s *Server) consumeFresh(state string, challenge freshChallenge, user string, auth time.Time) bool {
	s.freshMu.Lock()
	defer s.freshMu.Unlock()
	current, ok := s.freshPending[state]
	if !ok || current.bead != challenge.bead || current.ask != challenge.ask ||
		current.user != challenge.user || current.user != user || !current.at.Equal(challenge.at) {
		return false
	}
	if time.Since(current.at) > challengeTTL(current) {
		delete(s.freshPending, state)
		return false
	}
	delete(s.freshPending, state)
	s.freshStrong[user] = freshAuth{auth: auth, since: current.at}
	return true
}

// approveNow approves the run or setup card ask with the store, saying the
// person's word where the card is weighty, and carrying the fresh witness
// where the sign-in is fresh. The store judges the card again in the
// write. A run approval's return is the approving call's alone: where it
// carries a pairing, the caller answers with it in that response instead
// of the redirect to the bead (pairingApproved). A setup approval's is
// nil, as every other run approval's is beside its error.
func (s *Server) approveNow(ctx context.Context, id string, ask store.BeadAsk, option int, values map[string]string, account, host, user string, fw store.FreshWitness, fresh bool) (*store.AskApproved, error) {
	if fresh {
		ctx = store.WithFreshWitness(ctx, fw.User, fw.AuthTime)
	}
	weighed := len(ask.Weight) > 0
	if ask.Run != nil {
		return s.st.ApproveAsk(ctx, id, ask.Ask, user, weighed, values)
	}
	_, err := s.st.ApproveSetup(ctx, id, ask.Ask, option, values, account, user, weighed, host)
	return nil, err
}
