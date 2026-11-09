package web

// The delegation switch and records in the hosted web (design yardr-m90n
// 3.1, 3.4). The Config hub shows the switch to an operator with the
// button for its other state. Turning it off narrows, and takes only the
// operator check the hub itself makes; turning it on widens, and takes the
// strong-card gate a strong card's approval takes (approve.go): a fresh
// sign-in by this operator, held in a one-use state bound to the toggle
// and spent on the write. Anywhere but a hosted web with a gateway,
// turning it on stays a terminal's, as a strong card does. A delegated
// record's page is an operator's alone, as its store read is.

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"yardr.dev/yardr/internal/store"
)

const delegationTerminal = "turning the yardmaster's delegation on takes a fresh sign-in, which this window cannot prove: turn it on at a terminal on the yard's machine (yardr settings set yardmaster_delegation true)"

// delegationChallenge is the card a toggle's sign-in is bound to in
// freshPending: no bead's, so no card's sign-in returns to it and its own
// returns to no card.
const delegationChallenge = "config/delegation"

// delegationSwitch is the switch as it reads, "on" or "off": as set, else
// as hosted reads, as the store reads it.
func delegationSwitch(settings map[string]string) string {
	v, ok := settings[store.DelegationSetting]
	if !ok {
		v = settings[store.HostedSetting]
	}
	if v == "true" {
		return "on"
	}
	return "off"
}

// handleDelegationPost takes the Config hub's delegation form: turn the
// switch off or on, as the request's user, who must be an operator. It is
// registered only on a view that acts, as the other POST routes are.
func (s *Server) handleDelegationPost(w http.ResponseWriter, r *http.Request) {
	if origin := r.Header.Get("Origin"); !ownOrigin(origin, r.Host) {
		http.Error(w, "yardr web takes delegation changes only from its own pages: the request's origin is "+quoted(origin)+", not this view's. A page on another site cannot switch the yardmaster's delegation as this view's user",
			http.StatusForbidden)
		return
	}
	ctx := r.Context()
	if !s.isOperator(ctx) {
		s.configAgain(w, r, "the yardmaster's delegation is the operator's to switch")
		return
	}
	switch action := r.PostFormValue("action"); action {
	case "off":
		s.writeDelegation(w, r, "false")
	case "on":
		s.delegationOn(w, r)
	case "":
		s.configAgain(w, r, "on or off: the button names the change")
	default:
		s.configAgain(w, r, fmt.Sprintf("no delegation change is named %q", action))
	}
}

// delegationOn turns the switch on under a fresh sign-in, as approveStrong
// approves a strong card: at once where the session's last authentication
// is already a strong re-authentication by this operator inside the
// window, else after the gateway authenticates now, the one-use state
// returning to this toggle alone. Nothing is written before then.
func (s *Server) delegationOn(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := s.acting(ctx)
	if s.jwt == nil {
		s.configAgain(w, r, delegationTerminal)
		return
	}
	settings, err := s.st.Settings(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if settings[store.HostedSetting] != "true" {
		s.configAgain(w, r, delegationTerminal)
		return
	}
	if _, since, ok := s.strongAuth(ctx, user); ok && s.freshSession(ctx, since) {
		s.writeDelegation(w, r, "true")
		return
	}
	state := freshState()
	s.freshMu.Lock()
	for st, ch := range s.freshPending {
		if time.Since(ch.at) > freshPendingTTL {
			delete(s.freshPending, st)
		}
	}
	s.freshPending[state] = freshChallenge{bead: delegationChallenge, user: user, at: time.Now()}
	s.freshMu.Unlock()
	reauth := "/edge/reauth?return_to=" + url.QueryEscape("/config/delegation/fresh/"+state)
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", reauth)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, reauth, http.StatusSeeOther)
}

// handleDelegationFresh takes the gateway's return after a toggle's
// sign-in: it spends the one-use state and turns the switch on, or
// re-reads the hub with why it did not.
func (s *Server) handleDelegationFresh(w http.ResponseWriter, r *http.Request) {
	state := r.PathValue("state")
	s.freshMu.Lock()
	ch, ok := s.freshPending[state]
	s.freshMu.Unlock()
	ctx := r.Context()
	user := s.acting(ctx)
	switch {
	case !ok:
		s.configAgain(w, r, "that sign-in was used already, expired, or never started: turn delegation on again")
	case ch.bead != delegationChallenge || ch.user != user:
		s.configAgain(w, r, "that sign-in was raised for something else: turn delegation on again")
	case !s.freshSession(ctx, ch.at):
		s.configAgain(w, r, "that sign-in is not fresh: the gateway authenticated before the toggle's challenge, or outside its window. Turn delegation on again")
	case !s.consumeFresh(state, ch, user, jwtIdentityOf(ctx).authTime):
		s.configAgain(w, r, "that sign-in was used already, expired, or never started: turn delegation on again")
	default:
		settings, err := s.st.Settings(ctx)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if settings[store.HostedSetting] != "true" {
			s.configAgain(w, r, delegationTerminal)
			return
		}
		s.writeDelegation(w, r, "true")
	}
}

// writeDelegation sets the switch to value as the request's user, in one
// store write that sets nothing else, and lands on the hub.
func (s *Server) writeDelegation(w http.ResponseWriter, r *http.Request, value string) {
	ctx := r.Context()
	if err := s.st.SetSetting(ctx, store.DelegationSetting, value, s.acting(ctx)); err != nil {
		s.configAgain(w, r, err.Error())
		return
	}
	http.Redirect(w, r, "/config", http.StatusSeeOther)
}

// configAgain re-reads the Config hub after a POST that wrote nothing,
// with the reason on the redrawn page.
func (s *Server) configAgain(w http.ResponseWriter, r *http.Request, why string) {
	s.config(w, r, why)
}

// DelegationPage is a delegated record's page (/delegations/{id}).
type DelegationPage struct {
	Record *store.Delegation `json:"record"`
}

// handleDelegation serves the delegated record id to an operator, the
// one it was for or another, and answers anyone else not found, as the
// store's read does.
func (s *Server) handleDelegation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	notFound := func() {
		s.respond(w, r, Page{Status: http.StatusNotFound, Template: "error", Title: "Not found",
			Data: errorData{Error: "no such delegated record"}})
	}
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil || id == 0 || !s.isOperator(ctx) {
		notFound()
		return
	}
	rec, err := s.st.Delegation(ctx, id, s.acting(ctx))
	if errors.Is(err, store.ErrNotFound) {
		notFound()
		return
	} else if err != nil {
		s.fail(w, r, err)
		return
	}
	if rec.Blob != nil {
		body, err := s.st.DelegationBlob(ctx, id, s.acting(ctx))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		rec.DelegatedBody = *body
	}
	s.respond(w, r, Page{Template: "delegation", Title: fmt.Sprintf("Delegated record %d", id), Menu: "config", Data: DelegationPage{Record: rec}})
}
