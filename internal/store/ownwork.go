package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// A person's own work over the network (design yardr-ts4d 3.4): filing
// for oneself alone, and the originator-scoped list that carries
// what_is_happening there. Both are in rpc's Calls, so a device may make
// them, and the socket serves them as it serves every call. Filing takes
// the person's plain-words project and resolves the depot itself, so the
// local window and the later remote one share one resolution and one
// refusal; the list takes the window and returns what is theirs inside
// it, keeping no cursor and no last-view state anywhere.

// FileBead files one piece of work for the caller alone: a task with no
// stage named, so it starts where its depot's flow starts, exactly as a
// bead filed by any client does (there is no filing rule of its own).
// The project is the one thing the person cannot name in the yard's word
// for it, so it is resolved here, against the depot names: the yard's
// one depot where it has one and no project is given, the depot an equal
// name (in any case) picks, else the depot a partial name picks where it
// picks exactly one. Where the project is missing on a yard with several
// depots, names none, or names several, and where the yard has no depot
// at all, nothing is filed and the refusal lists the person's projects
// by name, so they can name one in the next call. The bead is filed for
// the actor through the same path every filing takes (checkFor refuses
// another person's from a bead agent's pane, and the members gate holds),
// at the priority a bead made without one has.
func (s *Store) FileBead(ctx context.Context, title, body, project, actor string) (*Bead, error) {
	if strings.TrimSpace(title) == "" {
		return nil, fmt.Errorf("a bead needs a title")
	}
	// A bead agent may not file for a person from its pane. Check before
	// resolving the project, whose refusal names every depot, so a failed
	// filing cannot be used to enumerate them.
	if err := s.CheckFor(ctx, actor); err != nil {
		return nil, err
	}
	depots, err := s.Depots(ctx)
	if err != nil {
		return nil, err
	}
	depot, err := resolveProject(depots, project)
	if err != nil {
		return nil, err
	}
	// The priority is the one any bead is made at where none is asked
	// for (yardr bead create's): the zero value is P0, which a depot's
	// P0 reserve route takes ahead of any label and any load condition,
	// so every request would jump the line. The call takes no priority;
	// a person who wants it sooner says so to the yardmaster.
	return s.CreateBead(ctx, NewBead{
		Title:    title,
		Body:     body,
		Type:     "task",
		Priority: DefaultPriority,
		Depot:    depot,
		For:      actor,
	}, actor)
}

// resolveProject is the depot project names, in the person's words: the
// yard's one depot where it has one and no project is given, the depot
// an equal name (in any case) picks, else the depot a partial name picks
// where it picks exactly one. An exact name always wins over a partial
// one, so a depot's own name files there even where another depot's name
// holds it. Anything else files nothing and refuses in the words that
// name the way on, listing the person's projects.
func resolveProject(depots []Depot, project string) (string, error) {
	names := make([]string, len(depots))
	for i, d := range depots {
		names[i] = d.Name
	}
	refuse := func() (string, error) {
		list := "none yet"
		if len(names) > 0 {
			list = strings.Join(names, ", ")
		}
		//lint:ignore ST1005 the words are a sentence for the person, the whole of the call's refusal and never wrapped, and quoted in docs/design/2026-11-04-yardr-ts4d-mcp-connector.md
		return "", fmt.Errorf("I don't know which of your projects this belongs to. Your projects are: %s. Tell me which one you mean, and I'll file it there", list)
	}
	switch project = strings.TrimSpace(project); {
	case len(depots) == 0:
		return refuse()
	case project == "" && len(depots) == 1:
		return depots[0].Name, nil
	case project == "":
		return refuse()
	default:
		for _, d := range depots {
			if strings.EqualFold(d.Name, project) {
				return d.Name, nil
			}
		}
		var matches []string
		lower := strings.ToLower(project)
		for _, d := range depots {
			if strings.Contains(strings.ToLower(d.Name), lower) {
				matches = append(matches, d.Name)
			}
		}
		if len(matches) != 1 {
			return refuse()
		}
		return matches[0], nil
	}
}

// OwnBead is one piece of a person's own work, as OwnBeads lists it: the
// listing and standing facts a window renders an account from, and no
// body, no notes and no worker claims, which the bead's own view and the
// boards carry. The depot is the project it belongs to, the filer beside
// the originator is what narrows the account to what the person filed
// themselves, and the closed fields say when finished work finished and,
// where it was closed with one, why.
type OwnBead struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Type        string     `json:"type"`
	Stage       string     `json:"stage"`
	Status      string     `json:"status"`
	Depot       *string    `json:"depot,omitempty"`
	Labels      []string   `json:"labels"`
	Hold        bool       `json:"hold,omitempty"`
	CreatedBy   *string    `json:"created_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ClosedAt    *time.Time `json:"closed_at,omitempty"`
	CloseReason *string    `json:"close_reason,omitempty"`
}

// WithdrawnBead is what WithdrawBead answers: the closed bead, and
// whether the close stopped a session that held the request.
type WithdrawnBead struct {
	Bead *Bead `json:"bead"`
	// Stopped is true where a session held the request and the close
	// ended it, as any close ends the session it closes under.
	Stopped bool `json:"stopped,omitempty"`
}

// RewordedBead is what RewordBead answers: the bead after the write, and
// whether the write changed its title or its body.
type RewordedBead struct {
	Bead *Bead `json:"bead"`
	// Changed is true where the title or the body differs from what they
	// were; where nothing differs nothing was written and no event was
	// logged, as an update that changes nothing writes nothing.
	Changed bool `json:"changed,omitempty"`
}

const (
	// landingStage is where a request waits for the assembly to land it;
	// dispatch's quiet pass reads it by this name too.
	landingStage = "approved"
	// assemblyGroup is the group the yard's default route for landingStage
	// names for a depot's work (DepotTemplate), the one that lands it.
	assemblyGroup = "{depot}-assembly"
	// maxTitle is how many runes of a title the yard keeps, here and in
	// the adapters that shape one before they call: the yard cuts, and an
	// adapter follows the yard's cut.
	maxTitle = 160
)

// CutTitle is title cut short to maxTitle runes, with an ellipsis where
// it was cut: a title is a line a person reads in a list, in filing and
// in rewording alike. The adapters cut with this before they call, and
// RewordBead cuts with it again, so the yard's cut is the one that stands
// whatever the caller sent.
func CutTitle(title string) string {
	if r := []rune(title); len(r) > maxTitle {
		return strings.TrimSpace(string(r[:maxTitle])) + "…"
	}
	return title
}

// ownRefusal is a refusal in the person's words: a sentence, whole, that
// is the call's result as it stands and is never wrapped, so it starts
// with a capital where an error string would not.
func ownRefusal(format string, a ...any) error { return fmt.Errorf(format, a...) }

// ownFiled is the request actor means: the piece of work bead names, when
// they filed it and it is still open, read under the write it is checked
// for. A name that matches nothing, one that matches several and one that
// is another person's are told alike, so the calls are no way to learn
// that other work exists. None of the refusals wraps a sentinel: over the
// wire they answer with the code error, never not-found or ambiguous.
func ownFiled(tx *bolt.Tx, bead, actor string) (*beadRow, error) {
	row, err := loadBead(tx, bead)
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrAmbiguous):
		row = nil
	case err != nil:
		return nil, err
	}
	if row == nil || row.CreatedBy == nil || *row.CreatedBy != actor {
		return nil, ownRefusal("I can't find that among the requests you filed, so nothing was changed. This only works on a request you filed yourself; for anyone else's, write to the yardmaster")
	}
	if row.Status == "closed" {
		return nil, ownRefusal("That one is finished or withdrawn already, so there is nothing to change")
	}
	return row, nil
}

// ownLanding says b is being landed, or waits to be: it is in the stage
// the assembly lands from, or the group holding it is the depot's
// assembly. Closing it then could leave its change on the main line with
// the request marked withdrawn.
func ownLanding(b *Bead) bool {
	if b.Stage == landingStage {
		return true
	}
	return b.Group != nil && b.Depot != nil && *b.Group == ResolveGroup(assemblyGroup, *b.Depot)
}

// WithdrawBead closes a request the actor filed themselves, with their
// reason as the close reason, as CloseBead would close it: the same
// close, the same history, ending the session it closes under. It reaches
// a request the person filed (Bead.CreatedBy) and no other: work the crew
// filed for them, anyone else's, a name that matches nothing and a prefix
// that matches several are all refused in one blind sentence, and a
// request finished or withdrawn already is refused too, so a second close
// cannot write over the reason it was closed with.
//
// It is allowed in any open stage, under a live session too: the close
// ends the session as any close does, and stopping a runaway build on
// one's own subscription is the point. A request that is landing is the
// exception, refused in words that send the person to the yardmaster.
//
// The rules are checked in the write they guard, so a claim that lands
// between the caller's read and this call is seen: one the assembly holds
// is refused as landing, not closed from under it. From a session's or a
// crew member's pane the close is recorded as that session's actor or
// that crew member, whatever actor says; from anywhere else, as given
// (paneAuthor). A blank bead or reason is refused as the adapters refuse
// it, in the same words, since the yard is the authority behind their
// early refusal.
func (s *Store) WithdrawBead(ctx context.Context, bead, reason, actor string) (*WithdrawnBead, error) {
	bead, reason = strings.TrimSpace(bead), strings.TrimSpace(reason)
	switch {
	case bead == "":
		return nil, fmt.Errorf("say which piece of work: its work name, from an earlier answer")
	case reason == "":
		return nil, fmt.Errorf("say why you are withdrawing it, in your words")
	}
	// The repository flows the close's gate evaluation resolves blockers'
	// flows through, read before the write: git must not run inside one.
	look := &untilLooks{repos: s.gateRepos(ctx, s.blocksTargets(ctx, bead)...), memo: flowMemo{}}
	var out *WithdrawnBead
	err := s.writeVia(ctx, func(w *wtx) error {
		resolved, err := paneAuthor(w.Tx, callerOf(ctx), actor)
		if err != nil {
			return err
		}
		actor = resolved
		row, err := ownFiled(w.Tx, bead, actor)
		if err != nil {
			return err
		}
		if ownLanding(&row.Bead) {
			return ownRefusal("It is landing: its change is going in for good, and closing it now could leave the change in with the request marked withdrawn. Nothing was changed. Write to the yardmaster (ask_the_yardmaster) and they will see to it")
		}
		stopped := row.Session != nil
		b, err := w.closeBead(row, reason, actor, look)
		if err != nil {
			return err
		}
		out = &WithdrawnBead{Bead: b, Stopped: stopped}
		return nil
	})
	return out, err
}

// RewordBead replaces the title, the body or both of a request the actor
// filed themselves with the words given, and nothing else about it
// changes. A blank title or body is one not given, so a client that
// sends "" for what it leaves out cannot wipe a body; both blank, and a
// title of more than one line, are refused as the adapters refuse them,
// in the same words, since the yard is the authority behind their early
// refusal. A long title is cut as filing cuts it (CutTitle).
//
// It reaches a request the person filed (Bead.CreatedBy) and no other,
// told as WithdrawBead tells one it does not reach, and a request
// finished or withdrawn already is refused too. It is refused while any
// session holds the request: a builder at work would be judged against
// words it never read.
//
// The rules are checked in the write they guard, so a claim that lands
// between the caller's read and this call is seen: one a session holds
// is refused in those words, not reworded under it. From a session's or
// a crew member's pane the write is recorded as that session's actor or
// that crew member, whatever actor says; from anywhere else, as given
// (paneAuthor).
func (s *Store) RewordBead(ctx context.Context, bead, title, body, actor string) (*RewordedBead, error) {
	bead, title, body = strings.TrimSpace(bead), strings.TrimSpace(title), strings.TrimSpace(body)
	switch {
	case bead == "":
		return nil, fmt.Errorf("say which piece of work: its work name, from an earlier answer")
	case title == "" && body == "":
		return nil, fmt.Errorf("say the new title, the new details, or both, in your words")
	case strings.ContainsAny(title, "\r\n"):
		return nil, fmt.Errorf("title: say it as one line, and put the rest in the details")
	}
	title = CutTitle(title)
	var out *RewordedBead
	err := s.writeVia(ctx, func(w *wtx) error {
		resolved, err := paneAuthor(w.Tx, callerOf(ctx), actor)
		if err != nil {
			return err
		}
		actor = resolved
		// The row the rules judge is read under this write, so a claim
		// that landed since the caller read is seen. changeBead loads it
		// again for the write; resolving the UUID it names loads the same
		// row.
		row, err := ownFiled(w.Tx, bead, actor)
		if err != nil {
			return err
		}
		if row.Session != nil {
			return ownRefusal("Someone is working on it right now, and changing the request under them would leave their work checked against words they never read. Nothing was changed. Write to the yardmaster (ask_the_yardmaster) if the request has to change")
		}
		var titleChanged, bodyChanged bool
		b, err := w.changeBead(row.UUID, actor, func(_ *wtx, b *beadRow) (string, map[string]any, error) {
			// The patch carries a title and a body and no other field,
			// and the event is the update's own (see UpdateBead): what
			// each was before it is kept under was, the only place an
			// old title or body is kept. A body changed is carried as
			// true, never the body itself.
			changed, was := map[string]any{}, map[string]any{}
			if title != "" {
				if title != b.Title {
					titleChanged = true
				}
				was["title"] = b.Title
				b.Title, changed["title"] = title, title
			}
			if body != "" {
				if body != b.Body {
					bodyChanged = true
				}
				was["body"] = b.Body
				b.Body, changed["body"] = body, true
			}
			if len(was) > 0 {
				changed["was"] = was
			}
			return "updated", changed, nil
		})
		if err != nil {
			return err
		}
		out = &RewordedBead{Bead: b, Changed: titleChanged || bodyChanged}
		return nil
	})
	return out, err
}

// OwnBeads lists user's own work alone: the beads whose originator is
// them, open or finished inside the window since opens. A bead without an
// originator reads as the operator's, as it does everywhere else. Open
// work is listed whole, whatever the window; finished work only where it
// finished inside it, from since until now. A since left out is the last
// 24 hours. Only the user reads it, and an operator any user's
// (mayReadInbox); what the reader may not see is left out, as if it were
// not there. Journals and memories are no work and are left out, as a
// listing leaves them. It keeps no cursor and no record of when the
// person last looked: each call is answered from the window it is given.
func (s *Store) OwnBeads(ctx context.Context, user string, since time.Time, actor string) ([]OwnBead, error) {
	if err := s.read(func(tx *bolt.Tx) error {
		if _, err := getUser(tx, user); err != nil {
			return err
		}
		_, err := s.mayReadInbox(ctx, tx, user, actor, "read")
		return err
	}); err != nil {
		return nil, err
	}
	settings, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	operator := settings[OperatorSetting]
	present := now()
	if since.IsZero() {
		since = present.Add(-24 * time.Hour)
	}
	beads, err := s.ListBeads(ctx, BeadFilter{All: true, Viewer: actor})
	if err != nil {
		return nil, err
	}
	out := []OwnBead{}
	for _, b := range beads {
		originator := b.Originator
		if originator == "" {
			originator = operator
		}
		if originator != user {
			continue
		}
		if b.Status == "closed" && (b.ClosedAt == nil || b.ClosedAt.Before(since) || b.ClosedAt.After(present)) {
			continue
		}
		out = append(out, OwnBead{
			ID: b.ID, Title: b.Title, Type: b.Type, Stage: b.Stage, Status: b.Status,
			Depot: b.Depot, Labels: b.Labels, Hold: b.Hold, CreatedBy: b.CreatedBy,
			CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt,
			ClosedAt: b.ClosedAt, CloseReason: b.CloseReason,
		})
	}
	return out, nil
}
