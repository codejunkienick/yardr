package mcp

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// AddNote is add_a_note: the person's words as a note on one piece of
// work they filed or may see, leaving any question on it open, as
// footplate's note does (a choice ask can take words as a note without
// closing: the answer is still the person's pick). It takes the piece
// the person means, as the work name of an earlier result names it, and
// their words, and it answers no ask. In words alone: files ride a later
// bead. A name that matches nothing, one that matches several and one
// that is not theirs to see are told alike, so the tool is no way to
// learn that other work exists.
func (s *Service) AddNote(ctx context.Context, work, note string) (*Result, error) {
	if err := s.requireUser(); err != nil {
		return nil, err
	}
	work, note = strings.TrimSpace(work), strings.TrimSpace(note)
	switch {
	case work == "":
		return nil, errors.New(sayWhich)
	case note == "":
		return nil, fmt.Errorf("say the note, in your words")
	}
	// No work id holds whitespace (a handle, UUID or prefix), so what
	// holds any is a title in the id's place, not a piece that is not
	// theirs: the distinction is purely syntactic and tells nothing of
	// what exists.
	if strings.IndexFunc(work, unicode.IsSpace) >= 0 {
		return nil, refusal("that is no work id: say the id of the piece, from what_is_happening")
	}
	ctx = s.ctx(ctx)
	view, err := s.Yard.Bead(ctx, work, s.User)
	if err != nil {
		return nil, refusal("I can't find that piece of work among yours")
	}
	if err := s.Yard.AddNote(ctx, view.ID, note, s.User); err != nil {
		return nil, err
	}
	var asks []uint64
	for _, a := range view.Asks {
		if a.To == s.User && a.State == "open" {
			asks = append(asks, a.Ask)
		}
	}
	text := fmt.Sprintf("Noted on %q.", view.Title)
	if open := openWords(asks); open != "" {
		text += " " + open
	}
	structured := map[string]any{"work": view.ID, "title": view.Title}
	if len(asks) > 0 {
		structured["waiting_questions"] = asks
	}
	return &Result{Text: text, Structured: structured}, nil
}

// openWords says which questions on the piece still wait for the person
// after their note, since the note answers none of them; "" where none
// does.
func openWords(asks []uint64) string {
	nums := make([]string, len(asks))
	for i, a := range asks {
		nums[i] = strconv.FormatUint(a, 10)
	}
	switch len(nums) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("Question %s is still open; the answer tool answers it.", nums[0])
	default:
		last := len(nums) - 1
		return fmt.Sprintf("Questions %s and %s are still open; the answer tool answers them.", strings.Join(nums[:last], ", "), nums[last])
	}
}
