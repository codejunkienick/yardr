package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"yardr.dev/yardr/internal/store"
)

// withdraw and reword are the person's own say over a request they made.
// They reach a request the person filed themselves and no other: the
// store records who filed a piece of work (Bead.CreatedBy) apart from
// whose subscription builds it (Bead.Originator), and work the crew filed
// for the person, or anyone else's, stays the yardmaster's to change.
// There is no operator exception: the operator's window for it is the
// CLI, which these tools leave as it is. Both calls are made as the
// person, through the store's scoped calls for a person's own filing, so
// this is a narrower window and no new permission.

// sayWhich is the refusal for a call that names no work.
const sayWhich = "say which piece of work: its work name, from an earlier answer"

// filedBy says user filed b themselves, by the one record of it: the
// filer, not whom the work is for. It is what narrows
// what_is_happening to the person's own filing; withdraw and reword reach
// by it through the store's own check, so the two cannot differ. A piece
// with no filer on record is nobody's filing.
func filedBy(b *store.Bead, user string) bool {
	return b.CreatedBy != nil && *b.CreatedBy == user
}

// Withdraw is withdraw: a request the person filed is closed, with their
// reason as the close reason, as yardr bead close would close it. It is
// allowed in any open stage, under a live session too: the close ends the
// session as any close does, and stopping a runaway build on one's own
// subscription is the point. A request that is landing is the exception,
// refused in words that send the person to the yardmaster. The rules are
// the store's (WithdrawBead), checked in the write they guard, so a
// request the assembly claimed since the person read it is refused as
// landing, not closed from under it.
func (s *Service) Withdraw(ctx context.Context, work, reason string) (*Result, error) {
	if err := s.requireUser(); err != nil {
		return nil, err
	}
	work, reason = strings.TrimSpace(work), strings.TrimSpace(reason)
	switch {
	case work == "":
		return nil, errors.New(sayWhich)
	case reason == "":
		return nil, fmt.Errorf("say why you are withdrawing it, in your words")
	}
	ctx = s.ctx(ctx)
	withdrawn, err := s.Yard.WithdrawBead(ctx, work, reason, s.User)
	if err != nil {
		return nil, err
	}
	text := fmt.Sprintf("Withdrawn: %q. It is closed, with your reason kept on it.", withdrawn.Bead.Title)
	if withdrawn.Stopped {
		text += " Its builder was stopped."
	}
	return &Result{
		Text:       text,
		Structured: map[string]any{"work": withdrawn.Bead.ID, "title": withdrawn.Bead.Title},
	}, nil
}

// Reword is reword: the title, the body or both of a request the person
// filed are replaced with the words they give, and nothing else about it
// changes (the patch carries a title and a body and no other field). A
// blank title or body is one not given, so a client that sends "" for
// what it leaves out cannot wipe a body. It is refused while any session
// holds the request: a builder at work would be judged against words it
// never read. The rules are the store's (RewordBead), checked in the
// write they guard, so a request a session claimed since the person read
// it is refused in those words, not reworded under it.
func (s *Service) Reword(ctx context.Context, work, title, body string) (*Result, error) {
	if err := s.requireUser(); err != nil {
		return nil, err
	}
	work, title, body = strings.TrimSpace(work), strings.TrimSpace(title), strings.TrimSpace(body)
	switch {
	case work == "":
		return nil, errors.New(sayWhich)
	case title == "" && body == "":
		return nil, fmt.Errorf("say the new title, the new details, or both, in your words")
	case strings.ContainsAny(title, "\r\n"):
		return nil, fmt.Errorf("title: say it as one line, and put the rest in the details")
	}
	ctx = s.ctx(ctx)
	var wantTitle, wantBody bool
	if title != "" {
		title, wantTitle = store.CutTitle(title), true
	}
	if body != "" {
		wantBody = true
	}
	reworded, err := s.Yard.RewordBead(ctx, work, title, body, s.User)
	if err != nil {
		return nil, err
	}
	var text string
	switch {
	case !reworded.Changed:
		text = fmt.Sprintf("It already says that, so nothing was changed. It stays %q.", reworded.Bead.Title)
	case wantTitle && wantBody:
		text = fmt.Sprintf("Reworded: it is now %q, with new details.", reworded.Bead.Title)
	case wantTitle:
		text = fmt.Sprintf("Reworded: it is now %q.", reworded.Bead.Title)
	default:
		text = fmt.Sprintf("Reworded: the details of %q are changed.", reworded.Bead.Title)
	}
	return &Result{
		Text:       text,
		Structured: map[string]any{"work": reworded.Bead.ID, "title": reworded.Bead.Title},
	}, nil
}
