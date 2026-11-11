package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"yardr.dev/yardr/internal/store"
)

// MoveAction is one way the view's user may send the bead on, as `yardr
// review` offers it: an outcome the flow names (done), or a stage it
// allows next (advance).
type MoveAction = store.Move

// moveActions is what the view's user may do with b, as `yardr review`
// offers it (store.MovesFor): the outcomes its flow names for the stage, as
// they stand for the bead, else the stages it allows for this bead. A bead
// that waits for no manual group the user belongs to, or a closed one,
// offers none. next is the bead's NextStages, read by the caller; a flow
// error leaves no move, as review's queue does.
func (s *Server) moveActions(ctx context.Context, b *store.Bead, next *store.NextStages) ([]MoveAction, error) {
	if b.Status != "open" {
		return nil, nil
	}
	routes, err := s.st.Routes(ctx)
	if err != nil {
		return nil, err
	}
	store.SortRoutes(routes)
	settings, err := s.st.Settings(ctx)
	if err != nil {
		return nil, err
	}
	operator := settings[store.OperatorSetting]
	groups, err := s.st.Groups(ctx)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]store.Group, len(groups))
	for _, g := range groups {
		byName[g.Name] = g
	}
	g, ok := store.ManualGroup(routes, *b, operator, byName)
	if !ok {
		return nil, nil
	}
	if len(g.Members) > 0 && !slices.Contains(g.Members, s.acting(ctx)) {
		return nil, nil
	}
	return store.MovesFor(b, next, routes, operator), nil
}

// openBeadAsks are the bead's open asks to the user the request acts as,
// oldest first, as the socket's Bead reads them. The page shows the card's
// weight beside the question, so the yard's mark goes from the question
// where it carries it, as on the card: a line starting with the mark is
// the yard's.
func (s *Server) openBeadAsks(ctx context.Context, id string) ([]store.BeadAsk, error) {
	v, err := s.st.Bead(ctx, id, s.acting(ctx))
	if err != nil {
		return nil, err
	}
	var out []store.BeadAsk
	for _, a := range v.Asks {
		if a.State == "open" && a.To == s.acting(ctx) {
			a.Question = store.WithoutWeightMark(a.Question)
			out = append(out, a)
		}
	}
	return out, nil
}

// handleBeadPost takes the bead page's forms, and the needs rows' own:
// an ask's answer, or a move to the next stage. It is registered only on a
// view that acts (ActAs, or UseJWT for JWT mode): without either the mux
// answers 405 itself, as on every other route. A write answered, the page
// lands on the bead; a write refused re-reads the bead with the reason, as
// 200 so a form puts it in place.
func (s *Server) handleBeadPost(w http.ResponseWriter, r *http.Request) {
	if origin := r.Header.Get("Origin"); !ownOrigin(origin, r.Host) {
		http.Error(w, "yardr web takes answers and moves only from its own pages: the request's origin is "+quoted(origin)+", not this view's. A page on another site cannot answer or move as this view's user",
			http.StatusForbidden)
		return
	}
	ctx := r.Context()
	id := r.PathValue("id")
	b, err := s.st.GetBead(ctx, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// The revision the page was drawn from: a bead that changed since
	// (another tab answered, a note came in) is read again instead of
	// written to.
	rev := r.PostFormValue("rev")
	if rev == "" || rev != b.Revision {
		s.beadAgain(w, r, b.ID, fmt.Sprintf("bead %s is at revision %s, not %s; read it again", b.ID, b.Revision, revOrNone(rev)))
		return
	}
	if outcome, to := r.PostFormValue("outcome"), r.PostFormValue("to"); outcome != "" || to != "" {
		s.beadMove(w, r, b, outcome, to, r.PostFormValue("note"), rev)
		return
	}
	s.beadAnswer(w, r, b, rev)
}

// beadMove reports the outcome, or advances to the stage, as `yardr review`
// does: with the note, from the bead's stage and revision, as the request's
// user. The move must be one the page offers: a bead that waits for
// another group, or an outcome its flow does not name, is refused.
func (s *Server) beadMove(w http.ResponseWriter, r *http.Request, b *store.Bead, outcome, to, note, rev string) {
	ctx := r.Context()
	next, err := s.st.NextStages(ctx, b.ID)
	if err != nil {
		s.beadAgain(w, r, b.ID, "no move is allowed until the flow is fixed: "+err.Error())
		return
	}
	moves, err := s.moveActions(ctx, b, next)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var want *MoveAction
	for i := range moves {
		m := &moves[i]
		if outcome != "" && m.Kind == "done" && m.Outcome == outcome {
			want = m
		}
		if outcome == "" && m.Kind == "advance" && m.To == to {
			want = m
		}
	}
	if want == nil {
		s.beadAgain(w, r, b.ID, "bead "+b.ID+" does not wait for "+s.acting(ctx)+" with that move; read it again")
		return
	}
	if want.NoteRequired && strings.TrimSpace(note) == "" {
		s.beadAgain(w, r, b.ID, "sending back to "+want.To+" needs a note saying what should change")
		return
	}
	exp := store.Expect{Stage: b.Stage, Revision: rev}
	var moved *store.Bead
	if want.Kind == "done" {
		moved, err = s.st.Done(ctx, b.ID, want.Outcome, note, exp, s.acting(ctx))
	} else {
		moved, err = s.st.AdvanceNote(ctx, b.ID, want.To, note, exp, s.acting(ctx))
	}
	if err != nil {
		s.beadAgain(w, r, b.ID, err.Error())
		return
	}
	http.Redirect(w, r, "/beads/"+url.PathEscape(moved.ID), http.StatusSeeOther)
}

// beadAnswer answers the form's ask through AnswerAsk, DeferAsk,
// DeclineAsk or GiveSecret, or approves its run or setup card, as the
// request's user. The ask must be open to them: one answered already, or
// asked of another, is refused.
func (s *Server) beadAnswer(w http.ResponseWriter, r *http.Request, b *store.Bead, rev string) {
	ctx := r.Context()
	seq, err := strconv.ParseUint(r.PostFormValue("ask"), 10, 64)
	if err != nil || seq == 0 {
		s.beadAgain(w, r, b.ID, "an answer needs the ask")
		return
	}
	asks, err := s.openBeadAsks(ctx, b.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	i := slices.IndexFunc(asks, func(a store.BeadAsk) bool { return a.Ask == seq })
	if i < 0 {
		s.beadAgain(w, r, b.ID, fmt.Sprintf("ask %d on bead %s is answered already, or asked of another; read it again", seq, b.ID))
		return
	}
	// An approval carries its preset pick as an option too: it is read by
	// the approval, not as a pick.
	if r.PostFormValue("action") == "approve" {
		s.beadApprove(w, r, b, asks[i])
		return
	}
	if r.PostFormValue("action") == "send" {
		ask := asks[i]
		if len(ask.Options) == 0 || ask.Secret != nil || ask.Run != nil || ask.Setup != nil {
			s.beadAgain(w, r, b.ID, fmt.Sprintf("ask %d does not take words as a note", seq))
			return
		}
		words := r.PostFormValue("words")
		if strings.TrimSpace(words) == "" {
			s.beadAgain(w, r, b.ID, "a note needs words")
			return
		}
		// A choice ask can take words as a note without closing: the
		// answer is still the person's pick, so a note leaves it open.
		if err := s.st.AddNote(ctx, b.ID, words, s.acting(ctx)); err != nil {
			s.beadAgain(w, r, b.ID, err.Error())
			return
		}
		http.Redirect(w, r, "/beads/"+url.PathEscape(b.ID), http.StatusSeeOther)
		return
	}
	answer := store.Answer{Ask: seq, Words: r.PostFormValue("words")}
	if opt := r.PostFormValue("option"); opt != "" {
		n, err := strconv.Atoi(opt)
		if err != nil {
			s.beadAgain(w, r, b.ID, "an option is a number, not "+opt)
			return
		}
		answer.Option = n
	} else {
		switch r.PostFormValue("action") {
		case "answer":
			// Words alone answer an ask that offers none.
		case "later":
			answer.Later = true
		case "decline":
			answer.Decline = true
		case "give":
			s.beadGiveSecret(w, r, b, asks[i])
			return
		default:
			s.beadAgain(w, r, b.ID, "pick an option, answer with words, put it aside with Later, or decline it")
			return
		}
	}
	if err := s.st.AddAnswerExpected(ctx, b.ID, answer, s.acting(ctx), rev); err != nil {
		s.beadAgain(w, r, b.ID, err.Error())
		return
	}
	http.Redirect(w, r, "/beads/"+url.PathEscape(b.ID), http.StatusSeeOther)
}

// beadGiveSecret gives the secret ask asks for through GiveSecret, as the
// request's user: each name the ask asks for is read from its own field,
// which does not echo. The values land in the file the source's credential
// is read from, and the answer on the bead holds the names that were given
// and never a value, as a terminal giving does. An ask that asks for no
// secret, and a name with no value, are refused with the page redrawn, and
// neither refusal repeats a value that was sent.
//
// The page has no gate to put in front of a weighty card (every secret ask
// is one), so it says nothing of its person's having weighed it: the yard
// refuses the giving in its own words, which the page shows, and the card
// is given in a terminal as its line names.
func (s *Server) beadGiveSecret(w http.ResponseWriter, r *http.Request, b *store.Bead, ask store.BeadAsk) {
	ctx := r.Context()
	if ask.Secret == nil {
		s.beadAgain(w, r, b.ID, fmt.Sprintf("ask %d asks for no secret: pick an option, answer with words, put it aside with Later, or decline it", ask.Ask))
		return
	}
	values := make(map[string]string, len(ask.Secret.Env))
	for _, name := range ask.Secret.Env {
		values[name] = r.PostFormValue("secret-" + name)
		if values[name] == "" {
			s.beadAgain(w, r, b.ID, fmt.Sprintf("ask %d asks for %s: %s was not given", ask.Ask, strings.Join(ask.Secret.Env, " and "), name))
			return
		}
	}
	if _, err := s.st.GiveSecret(ctx, b.ID, ask.Ask, values, s.acting(ctx), false); err != nil {
		s.beadAgain(w, r, b.ID, err.Error())
		return
	}
	http.Redirect(w, r, "/beads/"+url.PathEscape(b.ID), http.StatusSeeOther)
}

// beadAgain re-reads the bead page after a POST that wrote nothing, with
// the reason on the redrawn page.
func (s *Server) beadAgain(w http.ResponseWriter, r *http.Request, id, why string) {
	d, err := s.beadPage(r.Context(), id, r.Host)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d.Error = why
	s.respond(w, r, Page{Template: "bead", Title: id, Menu: "beads", Data: d, head: d.Head, stream: "seq"})
}

// revOrNone is the revision a form carried as the refusal names it: what
// a request without one sent.
func revOrNone(rev string) string {
	if rev == "" {
		return "none"
	}
	return rev
}
