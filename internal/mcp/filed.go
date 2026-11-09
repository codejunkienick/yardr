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
// person, under the store's own checks as the CLI's are, so this is a
// narrower window and no new permission.

const (
	// landingStage is where a request waits for the assembly to land it;
	// dispatch's quiet pass reads it by this name too.
	landingStage = "approved"
	// assemblyGroup is the group the yard's default route for landingStage
	// names for a depot's work (store.DepotTemplate), the one that lands it.
	assemblyGroup = "{depot}-assembly"
	// sayWhich is the refusal for a call that names no work.
	sayWhich = "say which piece of work: its work name, from an earlier answer"
)

// refusal is a refusal in the person's words: a sentence, whole, that is
// the tool's result as it stands and is never wrapped, as NoUserMessage
// is, so it starts with a capital where an error string would not.
func refusal(format string, a ...any) error { return fmt.Errorf(format, a...) }

// staleWords tells a write the store refused because the work changed
// since the tool read it (store.ErrConflict) in the person's words:
// nothing was written. Any other error is the store's own.
func staleWords(err error) error {
	if errors.Is(err, store.ErrConflict) {
		return refusal("It changed while I was doing that, so nothing was changed. Ask what is happening to see where it stands, then say it again")
	}
	return err
}

// filedBy says user filed b themselves, by the one record of it: the
// filer, not whom the work is for. It is what reword and withdraw reach by,
// and what narrows what_is_happening to the person's own filing, so the
// two cannot differ. A piece with no filer on record is nobody's filing.
func filedBy(b *store.Bead, user string) bool {
	return b.CreatedBy != nil && *b.CreatedBy == user
}

// filed is the request the person means: the piece of work work names,
// when they filed it and it is still open. A name that matches nothing,
// one that matches several and one that is another person's are told
// alike, so the tools are no way to learn that other work exists.
func (s *Service) filed(ctx context.Context, work string) (*store.Bead, error) {
	b, err := s.Yard.GetBead(ctx, work)
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrAmbiguous):
		b = nil
	case err != nil:
		return nil, err
	}
	if b == nil || !filedBy(b, s.User) {
		return nil, refusal("I can't find that among the requests you filed, so nothing was changed. This only works on a request you filed yourself; for anyone else's, write to the yardmaster")
	}
	if b.Status == "closed" {
		return nil, refusal("That one is finished or withdrawn already, so there is nothing to change")
	}
	return b, nil
}

// landing says b is being landed, or waits to be: it is in the stage the
// assembly lands from, or the group holding it is the depot's assembly.
// Closing it then could leave its change on the main line with the
// request marked withdrawn.
func landing(b *store.Bead) bool {
	if b.Stage == landingStage {
		return true
	}
	return b.Group != nil && b.Depot != nil && *b.Group == store.ResolveGroup(assemblyGroup, *b.Depot)
}

// Withdraw is withdraw: a request the person filed is closed, with their
// reason as the close reason, as yardr bead close would close it. It is
// allowed in any open stage, under a live session too: the close ends the
// session as any close does, and stopping a runaway build on one's own
// subscription is the point. A request that is landing is the exception,
// refused in words that send the person to the yardmaster. The close
// carries the standing it was read at, so a request that began to land
// between the read and the close is refused as stale, not closed.
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
	b, err := s.filed(ctx, work)
	if err != nil {
		return nil, err
	}
	if landing(b) {
		return nil, refusal("It is landing: its change is going in for good, and closing it now could leave the change in with the request marked withdrawn. Nothing was changed. Write to the yardmaster (ask_the_yardmaster) and they will see to it")
	}
	closed, err := s.Yard.CloseBead(ctx, b.ID, reason, store.Expect{Stage: b.Stage, Revision: b.Revision}, s.User)
	if err != nil {
		return nil, staleWords(err)
	}
	text := fmt.Sprintf("Withdrawn: %q. It is closed, with your reason kept on it.", closed.Title)
	if b.Session != nil {
		text += " Its builder was stopped."
	}
	return &Result{
		Text:       text,
		Structured: map[string]any{"work": closed.ID, "title": closed.Title},
	}, nil
}

// Reword is reword: the title, the body or both of a request the person
// filed are replaced with the words they give, and nothing else about it
// changes (the patch carries a title and a body and no other field). A
// blank title or body is one not given, so a client that sends "" for
// what it leaves out cannot wipe a body. It is refused while any session
// holds the request: a builder at work would be judged against words it
// never read. The write carries the standing it was read at, so a
// request a session claimed between the read and the write is refused as
// stale, not reworded under it.
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
	b, err := s.filed(ctx, work)
	if err != nil {
		return nil, err
	}
	if b.Session != nil {
		return nil, refusal("Someone is working on it right now, and changing the request under them would leave their work checked against words they never read. Nothing was changed. Write to the yardmaster (ask_the_yardmaster) if the request has to change")
	}
	var patch store.BeadPatch
	if title != "" {
		title = cutTitle(title)
		patch.Title = &title
	}
	if body != "" {
		patch.Body = &body
	}
	reworded, err := s.Yard.UpdateBead(ctx, b.ID, patch, store.Expect{Stage: b.Stage, Revision: b.Revision}, s.User)
	if err != nil {
		return nil, staleWords(err)
	}
	var text string
	switch {
	case reworded.Title == b.Title && reworded.Body == b.Body:
		text = fmt.Sprintf("It already says that, so nothing was changed. It stays %q.", reworded.Title)
	case patch.Title != nil && patch.Body != nil:
		text = fmt.Sprintf("Reworded: it is now %q, with new details.", reworded.Title)
	case patch.Title != nil:
		text = fmt.Sprintf("Reworded: it is now %q.", reworded.Title)
	default:
		text = fmt.Sprintf("Reworded: the details of %q are changed.", reworded.Title)
	}
	return &Result{
		Text:       text,
		Structured: map[string]any{"work": reworded.ID, "title": reworded.Title},
	}, nil
}
