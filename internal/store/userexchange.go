package store

import (
	"cmp"
	"context"
	"encoding/binary"
	"slices"

	bolt "go.etcd.io/bbolt"
)

// Exchange is what MailExchange answers: one letter of a user's mail with
// the exchange it belongs to, oldest first — the letters it answers and
// the answers it drew, both directions, the user's own sent replies among
// them.
type Exchange struct {
	Letters []Letter `json:"letters"`
	// Truncated is whether the exchange's oldest end is cut short: an
	// ancestor was pruned from the log, so earlier mail of the thread is
	// no longer in the yard.
	Truncated bool `json:"truncated"`
}

// MailExchange serves the user's letter seq with its exchange: the anchor
// with every letter of its answered-chain, oldest first. The anchor is
// verified as to-or-from the user — sent replies live in the
// correspondent's box, so "in my inbox" is the wrong check — and every
// letter is re-checked on the walk. A number that is no mail of theirs —
// missing, what is not mail, and another's — is refused in ReplyMail's
// blind sentence, told alike so the call is no way to learn what the
// number is.
//
// Ancestors follow Answered; replies are scanned newest-first, bounded to
// the two parties' inbox keys, which the pairwise shape of every chain
// (MailReply answers mail to its actor, to its sender) makes complete. A
// chain broken on a pruned letter ends truncated. Only the user reads
// their exchange, and an operator any user's (mayReadInbox); actor is who
// reads, and left out is the user. It is in rpc's Calls, as UserInbox is.
func (s *Store) MailExchange(ctx context.Context, user string, seq uint64, actor string) (*Exchange, error) {
	ex := &Exchange{Letters: []Letter{}}
	err := s.read(func(tx *bolt.Tx) error {
		if _, err := getUser(tx, user); err != nil {
			return err
		}
		if _, err := s.mayReadInbox(ctx, tx, user, actor, "read"); err != nil {
			return err
		}
		var anchor Event
		if ok, err := get(tx, bEvents, seqKey(seq), &anchor); err != nil {
			return err
		} else if !ok || anchor.Kind != "mail" || (MailReceiver(anchor) != user && anchor.Actor != user) {
			return ownRefusal("I can't find message %d among your mail; read_my_mail lists the numbers", seq)
		}
		chain := map[uint64]bool{anchor.Seq: true}
		var ancestors []Event
		cur := anchor
		for {
			a := answeredSeq(cur)
			if a == 0 || chain[a] {
				break
			}
			var e Event
			if ok, err := get(tx, bEvents, seqKey(a), &e); err != nil {
				return err
			} else if !ok {
				ex.Truncated = true
				break
			} else if e.Kind != "mail" || (MailReceiver(e) != user && e.Actor != user) {
				break
			}
			chain[a] = true
			ancestors = append(ancestors, e)
			cur = e
		}
		keys := mailKeys(tx, anchor.Actor, 0)
		if to := MailReceiver(anchor); to != anchor.Actor {
			keys = append(keys, mailKeys(tx, to, 0)...)
		}
		slices.Sort(keys) // big-endian seqs sort as numbers
		keys = slices.Compact(keys)
		byAnswered := map[uint64][]Event{}
		for _, k := range keys {
			var e Event
			if ok, err := get(tx, bEvents, k, &e); err != nil {
				return err
			} else if !ok || e.Kind != "mail" {
				continue
			}
			if a := answeredSeq(e); a != 0 {
				byAnswered[a] = append(byAnswered[a], e)
			}
		}
		queue := []uint64{anchor.Seq}
		for _, e := range ancestors {
			queue = append(queue, e.Seq)
		}
		var rest []Event
		for len(queue) > 0 {
			next := queue[0]
			queue = queue[1:]
			for _, e := range byAnswered[next] {
				if chain[e.Seq] {
					continue
				}
				if MailReceiver(e) != user && e.Actor != user {
					continue
				}
				chain[e.Seq] = true
				queue = append(queue, e.Seq)
				rest = append(rest, e)
			}
		}
		all := append(append([]Event{anchor}, ancestors...), rest...)
		slices.SortFunc(all, func(a, b Event) int { return cmp.Compare(a.Seq, b.Seq) })
		rd := &readers{tx: tx}
		for _, e := range all {
			l, err := rd.letter(e)
			if err != nil {
				return err
			}
			ex.Letters = append(ex.Letters, l)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ex, nil
}

// AckUserMail marks the user's single mail seq read, and nothing else: the
// named letter of a one-letter read, where AckUserInbox's through-cursor
// would take older unread with it. A number that is no received mail of
// theirs is no refusal and marks nothing, as an acknowledgement through a
// cursor with no mail in between is none: the blind sentence is
// MailExchange's, which is read first. Only the user acknowledges their
// inbox, and an operator any user's (mayReadInbox); actor is who does, and
// left out is the user. It is in rpc's Calls, as AckUserInbox is.
func (s *Store) AckUserMail(ctx context.Context, user string, seq uint64, actor string) error {
	return s.writeVia(ctx, func(w *wtx) error {
		u, err := getUser(w.Tx, user)
		if err != nil {
			return err
		}
		by, err := s.mayReadInbox(ctx, w.Tx, user, actor, "acknowledge")
		if err != nil {
			return err
		}
		var e Event
		if ok, err := get(w.Tx, bEvents, seqKey(seq), &e); err != nil {
			return err
		} else if !ok || e.Kind != "mail" || MailReceiver(e) != user {
			return nil
		}
		if seq <= u.Cursor || slices.Contains(u.MailRead, seq) {
			return nil
		}
		u.MailRead = append(u.MailRead, seq)
		if err := put(w.Tx, bUsers, user, u); err != nil {
			return err
		}
		return w.mailReadOne(user, by, seq)
	})
}

// mailRead logs that by acknowledged name's inbox from after was through
// seq, when mail is in between: the event mail_read names the inbox in
// reader and the person who acknowledged it as its actor. It is what tells
// a view that follows the stream that mail it shows as sent has been read;
// an acknowledgement that passes no mail is not an event.
func (w *wtx) mailRead(name, by string, was, through uint64) error {
	read := false
	for _, k := range mailKeys(w.Tx, name, was) {
		if binary.BigEndian.Uint64([]byte(k)) > through {
			break
		}
		var e Event
		if ok, err := get(w.Tx, bEvents, k, &e); err != nil {
			return err
		} else if ok && MailReceiver(e) == name {
			read = true
			break
		}
	}
	if !read {
		return nil
	}
	return w.event("", "mail_read", by, map[string]any{"reader": name, "through": through})
}

// mailReadOne logs that by acknowledged name's single mail seq: the event
// mail_read names the inbox in reader and the letter in seq, beside the
// through of a through-acknowledgement. Only a newly marked letter is an
// event, as only mail in between is for mailRead.
func (w *wtx) mailReadOne(name, by string, seq uint64) error {
	return w.event("", "mail_read", by, map[string]any{"reader": name, "seq": seq})
}
