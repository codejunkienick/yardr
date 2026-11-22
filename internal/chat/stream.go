package chat

import (
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"yardr.dev/yardr/internal/store"
)

// What the view follows after it starts: the yard's stream, event by event
// (event: mail, mail being read, reactions, the crew changing), and the pane
// of the selected crew member while it waits there (watch).

// watch keeps the pane of the selected crew member read while it waits there
// (Waits), and forgets it once it does not: the notice goes with it. It is
// asked after everything that can change who is selected or what they do.
func (m *Model) watch() tea.Cmd {
	r := m.member(m.sel)
	if r == nil || !Waits(*r) {
		if m.watched.name != "" {
			m.watched = paneOf{gen: m.watched.gen + 1}
			m.clamp()
		}
		return nil
	}
	if m.watched.name == r.Name && m.watched.busy {
		return nil
	}
	if m.watched.name != r.Name {
		m.watched = paneOf{name: r.Name, gen: m.watched.gen + 1}
		m.clamp()
	}
	return m.tail()
}

func (m *Model) tail() tea.Cmd {
	r := m.member(m.watched.name)
	if r == nil || m.opt.Tail == nil {
		m.watched.busy = false
		return nil
	}
	m.watched.busy = true
	tail, ctx, gen, handle := m.opt.Tail, m.ctx, m.watched.gen, r.Handle
	return func() tea.Msg {
		text, err := tail(ctx, handle, paneRows)
		return tailMsg{gen: gen, text: text, err: err}
	}
}

func (m *Model) event(e store.Event) tea.Cmd {
	if store.Notified(e, m.opt.Me) != "" {
		// The event says that it became a notification for the user, and
		// not the text yardr made of it: that is read from the store. No
		// event is both that and one of those below.
		return m.readNotes()
	}
	switch {
	case e.Kind == "mail":
		return m.mail(e)
	case e.Kind == "reaction":
		m.react(e)
		return nil
	case e.Kind == "noted":
		return m.loadAsks()
	case e.Kind == "mail_read":
		reader, _ := e.Data["reader"].(string)
		// A receipt names one letter read (seq) or a position read through
		// (through): the first marks exactly its letter and moves no
		// cursor, since a single read takes no older mail with it.
		seq := number(e.Data["seq"])
		through := number(e.Data["through"])
		if reader == m.opt.Me {
			if seq != 0 {
				delete(m.unread, seq)
			} else {
				m.acked(through)
			}
			return nil
		}
		// A crew member's conversation reads only that user's slice. A
		// receipt for another conversation may pass this user's mail by
		// sequence, but it did not read it.
		if session, _ := e.Data["session"].(string); session != "" && session != m.opt.Me {
			return nil
		}
		c := m.convo[reader]
		if seq != 0 {
			for i := range c {
				if c[i].To == reader && c[i].Seq == seq {
					c[i].Read = true
				}
			}
			return nil
		}
		for i := range c {
			if c[i].To == reader && c[i].Seq <= through {
				c[i].Read = true
			}
		}
	case e.Kind == "notifications_read":
		if reader, _ := e.Data["reader"].(string); reader == m.opt.Me {
			m.notesAcked(number(e.Data["through"]))
		}
	case store.EventKind(e.Kind) == "crew_status":
		name, _ := e.Data["crew"].(string)
		status, _ := e.Data["status"].(string)
		if r := m.member(name); r != nil && r.Live() {
			r.Status = status
			m.statusAt[name] = e.Seq
		}
	case strings.HasPrefix(store.EventKind(e.Kind), "crew_"), strings.HasPrefix(e.Kind, "session_prompt") && e.Data["crew"] != nil && e.Data["crew"] != "":
		// A session started, stopped or failed to start, the list itself
		// changed, or a crew member's first prompt arrived or was given up
		// (its handle then no longer holds it, or says so): one read of
		// the crew for the event, since what they now are is more
		// than the event carries.
		st, ctx := m.st, m.ctx
		return func() tea.Msg {
			head, err := st.Head(ctx)
			if err != nil {
				return crewMsg{err: err}
			}
			rs, err := st.Crew(ctx)
			return crewMsg{head: head, crew: rs, err: err}
		}
	case strings.HasPrefix(e.Kind, "peer_"):
		return m.link(e)
	}
	return nil
}

// mail takes a mail event: one the user sent, or one to the user. Mail
// the user wrote to another yard is the conversation with <name>@<peer>,
// queued as the event tells of it, and mail from there has that address
// as its sender already. Either is in the conversation of the address's
// row (thread). A name trusted as the viewer is how the viewer spoke: what
// it wrote to someone of this yard, and what they wrote to it, is in that
// one's row, as the store answers it (store.Conversation).
func (m *Model) mail(e store.Event) tea.Cmd {
	to, _ := e.Data["to"].(string)
	text, _ := e.Data["text"].(string)
	toUser, _ := e.Data["to_user"].(bool)
	l := store.Letter{Seq: e.Seq, At: e.At, From: e.Actor, To: to, Text: text, Via: store.Via(e), Files: store.EventFiles(e)}
	me := m.opt.Me
	if peer, _ := e.Data["to_peer"].(string); peer != "" {
		l.To, l.Delivery = to+"@"+peer, store.MailQueued
		switch {
		case e.Actor == me:
			m.add(m.thread(l.To), l)
		case m.as[l.To] == me:
			m.add(e.Actor, l)
		}
		return nil
	}
	switch {
	case e.Actor == me:
		m.add(to, l)
	case m.as[e.Actor] == me && to != me:
		l.As = store.SpeaksAs(e)
		m.add(to, l)
	case to == me && toUser:
		l.Read, l.As = e.Seq <= m.cursor, store.SpeaksAs(e)
		m.add(m.thread(e.Actor), l)
		if !l.Read && m.unread[e.Seq] == nil {
			// In the open conversation it is seen as it arrives.
			m.unread[e.Seq] = &waiting{from: e.Actor, seen: m.thread(e.Actor) == m.sel}
			return m.ack()
		}
	}
	return nil
}

// react takes a reaction event: the target letter's reactions gain the
// reactor's emoji, or lose it on a clear. The letter is in one row; the
// others are left as they are.
func (m *Model) react(e store.Event) {
	target := number(e.Data["seq"])
	emoji, _ := e.Data["emoji"].(string)
	for _, c := range m.convo {
		for i := range c {
			if c[i].Seq != target {
				continue
			}
			var kept []store.Reaction
			for _, r := range c[i].Reactions {
				if r.From != e.Actor {
					kept = append(kept, r)
				}
			}
			if emoji != "" {
				kept = append(kept, store.Reaction{From: e.Actor, Emoji: emoji})
				slices.SortFunc(kept, func(a, b store.Reaction) int { return strings.Compare(a.From, b.From) })
			}
			c[i].Reactions = kept
		}
	}
	m.clamp()
}
