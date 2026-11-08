package mcp

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"yardr.dev/yardr/internal/store"
)

// workStates are the states the account tells a piece of work in, in the
// order it lists them. workState tries them in the same order, so the
// first a piece fits is its own: a held or blocked piece is stuck, and a
// parked one takes precedence over its stage.
var workStates = []struct{ state, heading string }{
	{"finished", "Finished lately"},
	{"stuck", "Stuck"},
	{"parked", "Parked"},
	{"waiting", "Waiting"},
	{"not started", "Not started"},
	{"under way", "Under way"},
}

// WhatIsHappening is what_is_happening: a short account of the
// person's own work, each piece in the state it is in (workStates) and
// with the project it belongs to, or, with work set, that one thing in
// full with its notes. It keeps no record of when the person last
// looked: each call is answered from the window it is given.
func (s *Service) WhatIsHappening(ctx context.Context, work, sinceText string) (*Result, error) {
	if err := s.requireUser(); err != nil {
		return nil, err
	}
	ctx = s.ctx(ctx)
	now := s.now()
	if work != "" {
		return s.onePiece(ctx, work, now)
	}
	since := now.Add(-24 * time.Hour)
	if sinceText != "" {
		t, err := time.Parse(time.RFC3339, sinceText)
		if err != nil {
			return nil, fmt.Errorf("since %q: say the time as 2026-11-01T09:00:00Z", sinceText)
		}
		since = t
	}
	operator := ""
	settings, err := s.Yard.Settings(ctx)
	if err != nil {
		return nil, err
	}
	operator = settings[store.OperatorSetting]
	beads, err := s.Yard.ListBeads(ctx, store.BeadFilter{All: true, Viewer: s.User})
	if err != nil {
		return nil, err
	}
	// A question may wait for the person on work of any standing,
	// finished work too, and the standing alone would hide it.
	queue, err := s.Yard.UserAsks(ctx, s.User, s.User, s.User)
	if err != nil {
		return nil, err
	}
	waiting := map[string][]uint64{}
	for _, open := range queue.Open {
		waiting[open.Bead] = append(waiting[open.Bead], open.Ask)
	}
	own := func(b store.Bead) bool {
		originator := b.Originator
		if originator == "" {
			// A piece that names nobody is the yard operator's, as
			// the store reads it.
			originator = operator
		}
		return originator == s.User
	}
	items := map[string][]map[string]any{}
	lines := map[string][]string{}
	for _, b := range beads {
		if !own(b) {
			continue
		}
		state, detail, err := s.workState(ctx, b, since, now)
		if err != nil {
			return nil, err
		}
		if state == "" {
			continue
		}
		// The line and the item say the same: the detail is what stands
		// in brackets, so a client that shows only the structured part
		// loses no reason the words give, a question that waits for the
		// person among them.
		asks := waiting[b.ID]
		detail += waitsLine(asks, state == "finished")
		item := map[string]any{"work": b.ID, "title": b.Title, "updated": b.UpdatedAt.UTC().Format(time.RFC3339), "state": state, "detail": detail}
		if len(asks) > 0 {
			item["waiting_questions"] = asks
		}
		line := "- " + b.Title
		if b.Depot != nil {
			item["project"] = *b.Depot
			line += " in " + *b.Depot
		}
		items[state] = append(items[state], item)
		lines[state] = append(lines[state], fmt.Sprintf("%s (%s)", line, detail))
	}
	var b strings.Builder
	b.WriteString("Here is what is happening with your work.\n")
	list := []map[string]any{}
	for _, st := range workStates {
		if len(lines[st.state]) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n%s:\n%s\n", st.heading, strings.Join(lines[st.state], "\n"))
		list = append(list, items[st.state]...)
	}
	if len(list) == 0 {
		return &Result{
			Text:       fmt.Sprintf("Nothing of yours is open, and nothing has finished since %s.", when(since, now)),
			Structured: map[string]any{"work_list": list},
		}, nil
	}
	return &Result{
		Text:       strings.TrimRight(b.String(), "\n"),
		Structured: map[string]any{"work_list": list},
	}, nil
}

// workState is the state one piece of work is told in, as the first of
// workStates it fits, and the words that say why or when. The stage is
// what separates the open pieces that are not moving: decide waits for
// a person's answer, siding for the fold, and backlog is not on the line
// yet. Every other open stage is work travelling the line. No rule
// reads the type, so a lead or a design in the backlog is not started
// like the rest. The state is "" for a closed piece outside the window,
// which the account leaves out.
func (s *Service) workState(ctx context.Context, b store.Bead, since, now time.Time) (state, detail string, err error) {
	if b.Status == "closed" {
		if b.ClosedAt == nil || b.ClosedAt.Before(since) || b.ClosedAt.After(now) {
			return "", "", nil
		}
		return "finished", "finished " + when(*b.ClosedAt, now), nil
	}
	if b.Hold {
		return "stuck", "it is on hold, waiting for a person", nil
	}
	blockers, err := s.Yard.OpenBlockers(ctx, b.ID)
	if err != nil {
		return "", "", err
	}
	touched := "last touched " + when(b.UpdatedAt, now)
	switch {
	case len(blockers) > 0:
		return "stuck", "it is waiting for other work to finish first", nil
	case slices.Contains(b.Labels, "parked"):
		return "parked", touched, nil
	case b.Stage == "decide":
		return "waiting", "it is waiting for a person to decide", nil
	case b.Stage == "siding":
		return "waiting", "it is waiting to be combined with other small work", nil
	case b.Stage == "backlog":
		return "not started", touched, nil
	}
	return "under way", touched, nil
}

// onePiece is what_is_happening for one named thing: its title, its
// standing in plain words, and its notes, as the piece's own page
// shows them.
func (s *Service) onePiece(ctx context.Context, work string, now time.Time) (*Result, error) {
	view, err := s.Yard.Bead(ctx, work, s.User)
	if err != nil {
		//lint:ignore ST1005 the words are a sentence for the person, the whole of the tool's result and never wrapped, like NoUserMessage
		return nil, fmt.Errorf("I can't find that piece of work among yours")
	}
	b, err := s.Yard.GetBead(ctx, view.ID)
	if err != nil {
		return nil, err
	}
	state := "under way"
	if b.Status == "closed" {
		state = "finished"
	} else if b.Hold {
		state = "stuck: it is on hold, waiting for a person"
	} else {
		blockers, err := s.Yard.OpenBlockers(ctx, b.ID)
		if err != nil {
			return nil, err
		}
		if len(blockers) > 0 {
			state = "stuck: it is waiting for other work to finish first"
		}
	}
	var asks []uint64
	for _, a := range view.Asks {
		if a.To == s.User && a.State == "open" {
			asks = append(asks, a.Ask)
		}
	}
	var out strings.Builder
	fmt.Fprintf(&out, "%s\nIt is %s%s (last touched %s).", view.Title, state, waitsLine(asks, b.Status == "closed"), when(b.UpdatedAt, now))
	if strings.TrimSpace(view.Body) != "" {
		fmt.Fprintf(&out, "\n\n%s", strings.TrimSpace(view.Body))
	}
	if len(view.Notes) > 0 {
		out.WriteString("\n\nNotes on it:")
		for _, n := range view.Notes {
			author, outside := authorMark(n.Author)
			if n.Via != "" {
				author = store.ViaMark(author, n.Via)
			}
			fmt.Fprintf(&out, "\n- %s, %s:", when(n.At, now), author)
			if outside {
				fmt.Fprintf(&out, " %s", outsideLine(n.Author))
			}
			fmt.Fprintf(&out, "\n  %s", n.Text)
			if len(n.Files) > 0 {
				names := make([]string, len(n.Files))
				for i, f := range n.Files {
					names[i] = f.Name
				}
				fmt.Fprintf(&out, "\n  (with: %s)", strings.Join(names, ", "))
			}
		}
	}
	structured := map[string]any{"work": view.ID, "title": view.Title, "state": state}
	if len(asks) > 0 {
		structured["waiting_questions"] = asks
	}
	return &Result{Text: out.String(), Structured: structured}, nil
}

// waitsLine is what follows a piece's standing where questions on it
// wait for the person ("; question 7 still waits for you" on finished
// work), and "" where none does.
func waitsLine(asks []uint64, finished bool) string {
	if len(asks) == 0 {
		return ""
	}
	nums := make([]string, len(asks))
	for i, a := range asks {
		nums[i] = strconv.FormatUint(a, 10)
	}
	still := ""
	if finished {
		still = "still "
	}
	if len(nums) == 1 {
		return fmt.Sprintf("; question %s %swaits for you", nums[0], still)
	}
	last := len(nums) - 1
	return fmt.Sprintf("; questions %s and %s %swait for you", strings.Join(nums[:last], ", "), nums[last], still)
}

// AskForSomething is ask_for_something: the person's request filed as
// work for them, in their words. The project is the one thing the
// person cannot name in the yard's word for it, so it is resolved
// here, against the project's names, exactly as the design fixes:
// the yard's one project where it has one, the one the project field
// names where it has several, and a refusal listing the projects
// where it is missing, names none or names several (design
// yardr-ts4d 3.2).
func (s *Service) AskForSomething(ctx context.Context, request, project string) (*Result, error) {
	if err := s.requireUser(); err != nil {
		return nil, err
	}
	request = strings.TrimSpace(request)
	if request == "" {
		return nil, fmt.Errorf("say what you would like done, in your words")
	}
	ctx = s.ctx(ctx)
	depots, err := s.Yard.Depots(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(depots))
	for i, d := range depots {
		names[i] = d.Name
	}
	refuse := func() (*Result, error) {
		list := "none yet"
		if len(names) > 0 {
			list = strings.Join(names, ", ")
		}
		//lint:ignore ST1005 the words are a sentence for the person, the whole of the tool's result and never wrapped, and quoted in docs/design/2026-11-04-yardr-ts4d-mcp-connector.md
		return nil, fmt.Errorf("I don't know which of your projects this belongs to. Your projects are: %s. Tell me which one you mean, and I'll file it there", list)
	}
	depot := ""
	switch project = strings.TrimSpace(project); {
	case len(depots) == 0:
		return refuse()
	case project == "" && len(depots) == 1:
		depot = depots[0].Name
	default:
		var matches []string
		for _, d := range depots {
			if strings.EqualFold(d.Name, project) {
				matches = append(matches, d.Name)
			}
		}
		if len(matches) != 1 {
			return refuse()
		}
		depot = matches[0]
	}
	// The first line is the title and the words after it are the body, so
	// the title is not said twice.
	title, body := request, ""
	if i := strings.Index(request, "\n"); i >= 0 {
		title, body = strings.TrimSpace(request[:i]), strings.TrimSpace(request[i+1:])
	}
	title = cutTitle(title)
	// The priority is the one any bead is made at where none is asked for
	// (yardr bead create's): the zero value is P0, which a depot's P0
	// reserve route takes ahead of any label and any load condition, so
	// every request would jump the line. The tool takes no priority; a
	// person who wants it sooner says so to the yardmaster.
	bead, err := s.Yard.CreateBead(ctx, store.NewBead{
		Title:    title,
		Body:     body,
		Type:     "task",
		Priority: store.DefaultPriority,
		Depot:    depot,
		For:      s.User,
	}, s.User)
	if err != nil {
		return nil, err
	}
	return &Result{
		Text: fmt.Sprintf("Filed %q in %s. It is yours, and the people who do this work will see it. Ask what is happening to follow it.", bead.Title, depot),
		Structured: map[string]any{
			"work":    bead.ID,
			"title":   bead.Title,
			"project": depot,
		},
	}, nil
}

// maxTitle is how many characters a title the tools write keeps.
const maxTitle = 160

// cutTitle is title cut short to maxTitle characters, with an ellipsis
// where it was cut: a title is a line a person reads in a list, in
// filing and in rewording alike.
func cutTitle(title string) string {
	if r := []rune(title); len(r) > maxTitle {
		return strings.TrimSpace(string(r[:maxTitle])) + "…"
	}
	return title
}

// WhatWaitsForMe is what_waits_for_me: the questions the yard is
// waiting for this person to answer, each in plain words with its
// choices. The queue rows carry no choices, so each question's card is
// read with it, as a person's page reads it.
func (s *Service) WhatWaitsForMe(ctx context.Context) (*Result, error) {
	if err := s.requireUser(); err != nil {
		return nil, err
	}
	ctx = s.ctx(ctx)
	queue, err := s.Yard.UserAsks(ctx, s.User, s.User, s.User)
	if err != nil {
		return nil, err
	}
	if len(queue.Open) == 0 {
		return &Result{
			Text:       "Nothing is waiting for you.",
			Structured: map[string]any{"questions": []map[string]any{}},
		}, nil
	}
	now := s.now()
	var b strings.Builder
	fmt.Fprintf(&b, "%s waiting for you:", countWords(len(queue.Open), "question"))
	questions := make([]map[string]any, 0, len(queue.Open))
	for _, open := range queue.Open {
		b.WriteString("\n\n")
		// Each question's object carries what the card gives a person
		// (its words whole, under any outside mark, its choices, who
		// asked and when), since a client may show its model only this.
		q := map[string]any{"work": open.Bead, "question": open.Ask, "title": open.BeadTitle}
		var card *store.BeadAsk
		if view, err := s.Yard.Bead(ctx, open.Bead, s.User); err == nil {
			for i := range view.Asks {
				if view.Asks[i].Ask == open.Ask {
					card = &view.Asks[i]
					q["title"] = view.Title
				}
			}
		}
		var text string
		if card != nil {
			text = questionText(*card, q["title"].(string), now)
			q["asked_by"], _ = authorMark(card.Asker)
			q["asked_at"] = card.Since.UTC().Format(time.RFC3339)
			q["choices"] = choices(*card)
			if card.Recommend >= 1 && card.Recommend <= len(card.Options) {
				q["recommended"] = card.Recommend
			}
		} else {
			// The row alone, where the card cannot be read: the
			// question and who asked it, in the row's words.
			var row strings.Builder
			asker, outside := authorMark(open.Asker)
			fmt.Fprintf(&row, "Question %d, on %q (asked by %s, %s)\n", open.Ask, open.BeadTitle, asker, when(open.Since, now))
			if outside {
				row.WriteString(outsideLine(open.Asker) + "\n")
			}
			row.WriteString(open.Question)
			text = row.String()
			q["asked_by"] = asker
			q["asked_at"] = open.Since.UTC().Format(time.RFC3339)
			q["choices"] = []map[string]any{}
		}
		q["text"] = text
		b.WriteString(text)
		questions = append(questions, q)
	}
	return &Result{
		Text:       b.String(),
		Structured: map[string]any{"questions": questions},
	}, nil
}

// AnswerInput is one answer, in the person's forms: a pick, words, an
// approval, putting the question aside, or declining it. Exactly one
// form is given (words may ride with a pick or a decline).
type AnswerInput struct {
	Work     string
	Question uint64
	Pick     int
	Words    string
	Approve  bool
	PutAside bool
	Decline  bool
}

// Answer is the answer tool: every form of an answer, held to the
// store's own rules as chat holds them (AnswerAsk, DeferAsk,
// DeclineAsk, ApproveAsk as the actor, who is this connection's user).
// weighed is the client's word that its person has seen a weighty
// card's weight and typed its number back (see Result.Weighed): the
// yard checks the word exactly as it checks every other client's.
//
// A secret is never answered here: where the question asks for one,
// the answer is refused before any secret call, the question stays
// open, and the words name where the secret is given (design
// yardr-ts4d 3.3). A setup question, whose approval types its account
// unseen beside the pick, is refused the same way and for the same
// reason.
func (s *Service) Answer(ctx context.Context, in AnswerInput, weighed bool) (*Result, error) {
	if err := s.requireUser(); err != nil {
		return nil, err
	}
	if in.Work == "" || in.Question == 0 {
		return nil, fmt.Errorf("say which question: its work name and its number, from what_waits_for_me")
	}
	if in.Pick < 0 {
		return nil, fmt.Errorf("choice: say its number, from the question's choices")
	}
	ctx = s.ctx(ctx)
	view, err := s.Yard.Bead(ctx, in.Work, s.User)
	if err != nil {
		return nil, fmt.Errorf("that question is not among the ones waiting for you; it may be answered already, or not yours to answer")
	}
	var ask *store.BeadAsk
	for i := range view.Asks {
		if view.Asks[i].Ask == in.Question {
			ask = &view.Asks[i]
			break
		}
	}
	if ask == nil {
		return nil, fmt.Errorf("that question is not among the ones waiting for you; it may be answered already, or not yours to answer")
	}
	if ask.State != "open" && ask.State != "put aside" {
		return nil, fmt.Errorf("question %d was already dealt with (%s)", in.Question, ask.State)
	}
	answered := func(text, state string) (*Result, error) {
		return &Result{
			Text:       text,
			Structured: map[string]any{"work": view.ID, "question": in.Question, "state": state},
		}, nil
	}
	switch {
	case in.PutAside:
		if in.Pick > 0 || in.Approve || in.Decline || in.Words != "" {
			return nil, fmt.Errorf("putting a question aside takes nothing with it: no pick, no words, no approval")
		}
		if err := s.Yard.DeferAsk(ctx, in.Work, in.Question, s.User); err != nil {
			return nil, err
		}
		return answered(fmt.Sprintf("Put aside: question %d on %q. It comes back in a day, or sooner if the asker writes on it again.", in.Question, view.Title), "put aside")
	case in.Decline:
		if in.Pick > 0 || in.Approve {
			return nil, fmt.Errorf("declining takes no pick and no approval: it ends the question, with your reason in words if you gave one")
		}
		if err := s.Yard.DeclineAsk(ctx, in.Work, in.Question, in.Words, s.User); err != nil {
			return nil, err
		}
		return answered(fmt.Sprintf("Declined: question %d on %q. The asker is told it is not yours to decide.", in.Question, view.Title), "declined")
	}
	// What is left approves or answers, so it is here that a secret is
	// refused: before any call that could carry one.
	if ask.Secret != nil {
		return nil, fmt.Errorf("that question asks for a secret, and secrets are never given here, so nothing was sent and question %d is still open. "+
			"Give it in the yard's window in your browser, or in a terminal on the machine the yard runs on: yardr bead answer %s %d --secret. "+
			"What you type there is not shown, and no copy of it passes through here", in.Question, view.ID, in.Question)
	}
	if ask.Setup != nil {
		return nil, fmt.Errorf("that question sets up %s, and its approval types its account unseen, which never happens here, so nothing was sent and question %d is still open. "+
			"Approve it in the yard's window in your browser, or in a terminal on the machine the yard runs on: yardr bead answer %s %d <choice> --secret", ask.Setup.Pack, in.Question, view.ID, in.Question)
	}
	if ask.Run != nil || ask.DepotPush != nil {
		if !in.Approve {
			return nil, fmt.Errorf("question %d proposes a command, and is answered by approving it, putting it aside, or declining it: no pick and no words alone", in.Question)
		}
		if in.Pick > 0 || in.Words != "" {
			return nil, fmt.Errorf("approving question %d takes nothing with it: no pick and no words", in.Question)
		}
		if len(ask.Weight) > 0 && !weighed {
			return &Result{Weighed: &WeighedPrompt{Number: in.Question, Message: weighedMessage(*ask, view.Title)}}, nil
		}
		done, err := s.Yard.ApproveAsk(ctx, in.Work, in.Question, s.User, weighed, nil)
		if err != nil {
			return nil, err
		}
		ran := store.AskRan{Argv: done.Argv}
		text := fmt.Sprintf("Approved: question %d on %q. The yard ran it: %s", in.Question, view.Title, ran.Command())
		if done.Result != "" {
			text += "\n" + done.Result
		}
		return answered(text, "answered")
	}
	switch {
	case in.Approve:
		return nil, fmt.Errorf("question %d proposes no command to approve: pick a choice, give words, put it aside, or decline it", in.Question)
	case in.Pick > 0:
		if in.Pick > len(ask.Options) {
			return nil, fmt.Errorf("choice %d: question %d has %s", in.Pick, in.Question, countWords(len(ask.Options), "choice"))
		}
		if len(ask.Weight) > 0 && !weighed {
			return &Result{Weighed: &WeighedPrompt{Number: in.Question, Message: weighedMessage(*ask, view.Title)}}, nil
		}
		if err := s.Yard.AnswerAsk(ctx, in.Work, in.Question, in.Pick, in.Words, s.User, weighed); err != nil {
			return nil, err
		}
		return answered(fmt.Sprintf("Answered: you picked %q on question %d (%q).", ask.Options[in.Pick-1].Label, in.Question, view.Title), "answered")
	case in.Words != "":
		if len(ask.Options) > 0 {
			return nil, fmt.Errorf("question %d has choices: pick one by number (words can go with the pick)", in.Question)
		}
		if err := s.Yard.AnswerAsk(ctx, in.Work, in.Question, 0, in.Words, s.User, false); err != nil {
			return nil, err
		}
		return answered(fmt.Sprintf("Answered: your words are on question %d (%q).", in.Question, view.Title), "answered")
	}
	return nil, fmt.Errorf("nothing to record: pick a choice, give words, approve, put it aside, or decline it")
}

// AskTheYardmaster is ask_the_yardmaster: mail to the yardmaster, as
// chat sends it, then a bounded wait inside the open call for the
// reply. Progress says the wait is standing, often enough to hold the
// client's idle window. Cancelling the wait never takes the message
// back: the question stands, and the reply will come as mail. Finding
// the reply marks nothing read: reading mail is read_my_mail's.
//
// waitForReply false is for a note or an FYI that needs no answer now:
// the call ends once the message is sent, so it holds nobody's
// conversation, and any reply is read_my_mail's.
func (s *Service) AskTheYardmaster(ctx context.Context, message string, waitForReply bool, progress func(string)) (*Result, error) {
	if err := s.requireUser(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(message) == "" {
		return nil, fmt.Errorf("say what you would like to ask, in your words")
	}
	ctx = s.ctx(ctx)
	to, err := s.yardmaster(ctx)
	if err != nil {
		return nil, err
	}
	head, err := s.Yard.Head(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.Yard.Mail(ctx, to, message, s.User); err != nil {
		return nil, err
	}
	// Every result carries its words in the structured part too: a client
	// that shows only that part (Claude Code showed {"waiting": true} and
	// nothing else) then shows words, and the two cannot differ.
	said := func(text string, structured map[string]any) *Result {
		structured["message"] = text
		return &Result{Text: text, Structured: structured}
	}
	// No wait, no progress line and no look at the inbox: the message
	// stands, and waiting stays true because a reply, if the yardmaster
	// gives one, comes as mail.
	if !waitForReply {
		return said("Sent to the yardmaster; the reply comes by mail. Ask me to read your mail later.",
			map[string]any{"waiting": true}), nil
	}
	wait := s.Wait
	if wait <= 0 {
		wait = DefaultWait
	}
	poll := s.PollInterval
	if poll <= 0 {
		poll = time.Second
	}
	progressEvery := s.ProgressInterval
	if progressEvery <= 0 {
		progressEvery = 15 * time.Second
	}
	if progress != nil {
		progress("asked; waiting for the answer")
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	pollTick := time.NewTicker(poll)
	defer pollTick.Stop()
	progressTick := time.NewTicker(progressEvery)
	defer progressTick.Stop()
	for {
		select {
		case <-ctx.Done():
			return said("You stopped the wait; your message is still with the yardmaster, and the answer will come. Ask me to read your mail later, and I will tell you what it says.",
				map[string]any{"waiting": true}), nil
		case <-deadline.C:
			return said("Your question is with the yardmaster. The answer will come; ask me to read your mail later, and I will tell you what it says.",
				map[string]any{"waiting": true}), nil
		case <-progressTick.C:
			if progress != nil {
				progress("asked; waiting for the answer")
			}
		case <-pollTick.C:
			box, err := s.Yard.UserInbox(ctx, s.User, 0, s.User)
			if err != nil {
				continue
			}
			// The reply is the first letter from the yardmaster after the
			// question, read or not: another reader of this person's mail
			// (footplate on their phone or laptop) may have marked it read
			// before this poll, and a reply passed over for that is lost to
			// the connection both ways, since read_my_mail shows unread
			// mail only. The inbox lists unread letters first, so the
			// first match is not the oldest: the lowest seq is.
			var reply *store.Letter
			for i, l := range box.Mail {
				if l.From == to && l.Seq > head && (reply == nil || l.Seq < reply.Seq) {
					reply = &box.Mail[i]
				}
			}
			if reply != nil {
				return said(fmt.Sprintf("The yardmaster replied (%s):\n%s", when(reply.At, s.now()), reply.Text),
					map[string]any{"waiting": false, "reply": reply.Text}), nil
			}
		}
	}
}

// yardmaster is who ask_the_yardmaster writes to: the yard's
// yardmaster setting, else its one crew member, else the name every
// yard starts with, as the CLI resolves it.
func (s *Service) yardmaster(ctx context.Context) (string, error) {
	settings, err := s.Yard.Settings(ctx)
	if err != nil {
		return "", err
	}
	if settings[store.YardmasterSetting] != "" {
		return settings[store.YardmasterSetting], nil
	}
	crew, err := s.Yard.Crew(ctx)
	if err != nil {
		return "", err
	}
	if len(crew) == 1 {
		return crew[0].Name, nil
	}
	return "yardmaster", nil
}

// ReadMyMail is read_my_mail: the person's whole unread inbox, from
// every sender, shown in order, and the one inbox cursor advanced
// through what it showed and no further, as chat advances it. The tool
// keeps no cursor of its own: a later call returns what arrived since.
// With recent set, it looks back instead: that many recent messages,
// read or not, each saying whether it was already read, and the cursor
// left alone, so a later plain call still shows the unread.
func (s *Service) ReadMyMail(ctx context.Context, recent int) (*Result, error) {
	if err := s.requireUser(); err != nil {
		return nil, err
	}
	if recent < 0 {
		return nil, fmt.Errorf("recent: say how many recent messages to show, a number of 1 or more")
	}
	ctx = s.ctx(ctx)
	if recent > 0 {
		return s.recentMail(ctx, recent)
	}
	box, err := s.Yard.UserInbox(ctx, s.User, 0, s.User)
	if err != nil {
		return nil, err
	}
	var unread []store.Letter
	for _, l := range box.Mail {
		if !l.Read {
			unread = append(unread, l)
		}
	}
	if len(unread) == 0 {
		return &Result{
			Text:       "You have no unread mail.",
			Structured: map[string]any{"messages": []map[string]any{}},
		}, nil
	}
	now := s.now()
	var b strings.Builder
	fmt.Fprintf(&b, "%s you had not read:", countWords(len(unread), "message"))
	messages := make([]map[string]any, 0, len(unread))
	var through uint64
	for _, l := range unread {
		through = max(through, l.Seq)
		messages = append(messages, renderLetter(&b, l, now, false))
	}
	// Through what was shown, and no further: mail that arrived while
	// this was being read waits for the next call. The acknowledgement
	// is the reading's own bookkeeping, made under no connection name,
	// so a read leaves no "via" mark in the history, as no read does.
	ackCtx := store.WithConnection(ctx, "")
	if err := s.Yard.AckUserInbox(ackCtx, s.User, through, s.User); err != nil {
		return nil, err
	}
	return &Result{
		Text:       b.String(),
		Structured: map[string]any{"messages": messages},
	}, nil
}

// recentMail is read_my_mail's look-back: that many recent messages,
// read or not, as the inbox fills them — the unread oldest-first, then
// the latest read — each saying whether it was already read. It acks
// nothing: a look-back marks nothing read.
func (s *Service) recentMail(ctx context.Context, recent int) (*Result, error) {
	box, err := s.Yard.UserInbox(ctx, s.User, recent, s.User)
	if err != nil {
		return nil, err
	}
	if len(box.Mail) == 0 {
		return &Result{
			Text:       "You have no mail.",
			Structured: map[string]any{"messages": []map[string]any{}},
		}, nil
	}
	now := s.now()
	var b strings.Builder
	fmt.Fprintf(&b, "Your %s:", countWords(len(box.Mail), "recent message"))
	messages := make([]map[string]any, 0, len(box.Mail))
	for _, l := range box.Mail {
		messages = append(messages, renderLetter(&b, l, now, true))
	}
	return &Result{
		Text:       b.String(),
		Structured: map[string]any{"messages": messages},
	}, nil
}

// renderLetter writes one letter under an inbox header: its From line,
// its words, and what came with them. With marked, the From line and
// the structured message each say whether it was already read;
// without, neither does, as the unread inbox reads.
func renderLetter(b *strings.Builder, l store.Letter, now time.Time, marked bool) map[string]any {
	from, outside := authorMark(l.From)
	if l.As != "" {
		from = fmt.Sprintf("%s (writing as %s)", l.From, l.As)
		outside = false
	}
	if l.Via != "" {
		from = store.ViaMark(from, l.Via)
	}
	fmt.Fprintf(b, "\n\nFrom %s, %s", from, when(l.At, now))
	if marked {
		if l.Read {
			fmt.Fprintf(b, " (read)")
		} else {
			fmt.Fprintf(b, " (unread)")
		}
	}
	fmt.Fprintf(b, ":")
	if outside {
		fmt.Fprintf(b, " %s", outsideLine(l.From))
	}
	fmt.Fprintf(b, "\n%s", l.Text)
	if len(l.Files) > 0 {
		names := make([]string, len(l.Files))
		for i, f := range l.Files {
			names[i] = f.Name
		}
		fmt.Fprintf(b, "\n(with: %s)", strings.Join(names, ", "))
	}
	messageText := l.Text
	if outside {
		messageText = outsideLine(l.From) + "\n" + messageText
	}
	message := map[string]any{
		"from": from, "at": l.At.UTC().Format(time.RFC3339), "text": messageText,
	}
	if marked {
		message["read"] = l.Read
	}
	return message
}

// countWords is "1 question", "3 questions": the counts a result
// speaks, in words a person reads.
func countWords(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
