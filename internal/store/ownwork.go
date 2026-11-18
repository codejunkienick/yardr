package store

import (
	"context"
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
