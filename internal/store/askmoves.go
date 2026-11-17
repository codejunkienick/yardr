// A bead's agent that sends its bead to decide with a question can say, with
// each option of the question it wrote, where picking that option sends the
// bead: one --move <where> for each option, in the order written, beside the
// ask's other flags (yardr bead ask <id> - --move <where>, the options on
// stdin). The words are the labels of the stage's own card of moves (movesOf):
// a stage, or an outcome where the stage names them. The ask is filed on the
// bead as any ask is, in the stage the agent works in, and its moves are
// checked then against what the stage its question outcome leads to offers
// (checkAskMoves), so a mistyped name is refused to the one who typed it.
//
// When the bead arrives in a stage that offers its moves as a card (a moves
// template), the yard does not put that card beside the question: the question
// becomes the card (absorb). Where every option named a move the stage still
// offers, the card is the agent's own, its question, context, options and
// recommendation, each option firing its move, so that one card carries one
// pick and the pick reaches whoever works the bead next as the option picked.
// Each option says what it does in the asker's words and then in the yard's,
// so that a label that disagrees with its move is seen by the one who picks.
// Where none did, or a move is no longer offered, it is the stage's card of
// moves with the question and its options quoted on it. Either way the hand
// ask is taken back in the same write, naming the card (consumeQuestion), so
// that nothing stays answerable on the bead for whoever works it next. The
// moves on an ask move nothing by themselves: the ask answered anywhere else
// records the pick, as a hand ask always did.
package store

import (
	"context"
	"fmt"
	"slices"
	"strings"

	bolt "go.etcd.io/bbolt"
)

// askMovesShape checks the moves a writer gave beside its options, and keeps
// them trimmed: every option names one, or none does, in the order the options
// are written. A mix would leave a card whose pick moves the bead for some
// options and not for others, which a person cannot tell from the card.
func askMovesShape(ask *Ask) error {
	if len(ask.Move) == 0 {
		return nil
	}
	switch {
	case len(ask.Options) == 0:
		return fmt.Errorf("--move says where picking an option sends the bead: the ask has no --option")
	case len(ask.Move) != len(ask.Options):
		return fmt.Errorf("--move goes with every --option or with none, one for each in the order written: the ask has %d options and %d moves", len(ask.Options), len(ask.Move))
	}
	moves := make([]string, len(ask.Move))
	for i, m := range ask.Move {
		if moves[i] = strings.TrimSpace(m); moves[i] == "" {
			return fmt.Errorf("--move %d is empty: where picking option %d sends the bead", i+1, i+1)
		}
	}
	ask.Move = moves
	return nil
}

// checkAskMoves refuses an ask whose moves are not ones the bead's decide
// stage offers, before the ask is written: the stage its question outcome
// leads to from the stage the bead is in, as the stage's own card of moves
// would offer them, the asking stage among them. The refusal names what is
// offered. Only the flow is read, never a stage file: which moves are legal is
// the flow's to say, and the stage's card is read again when the bead arrives
// (absorb), where a flow that changed meanwhile is met. The repository is read
// before the read of the store, as addAnswer's is, since git never runs inside
// a write.
func (s *Store) checkAskMoves(ctx context.Context, id string, ask Ask) error {
	if len(ask.Move) == 0 {
		return nil
	}
	pre, err := s.GetBead(ctx, id)
	if err != nil {
		return err
	}
	repo, err := s.repoFor(ctx, pre.Depot, pre.Type, PinnedFlow(pre))
	if err != nil {
		return err
	}
	return s.read(func(tx *bolt.Tx) error {
		row, err := loadBead(tx, id)
		if err != nil {
			return err
		}
		f, _, err := resolveFlow(tx, row.Depot, row.Type, PinnedFlow(&row.Bead), repo)
		if err != nil {
			return changedMeanwhile(err)
		}
		if f == nil {
			return fmt.Errorf("--move: no flow governs bead %s, so there is no stage its question goes to and no moves to name", row.ID)
		}
		to, err := f.Resolve(row.Stage, questionOutcome)
		if err != nil {
			return fmt.Errorf("--move says where a pick sends the bead from the stage its outcome %s takes it to, and bead %s in %s has none: %w", questionOutcome, row.ID, row.Stage, err)
		}
		var offered []string
		for _, o := range movesOf(f, to, row, row.Stage) {
			offered = append(offered, o.Outcome)
		}
		// The names as newAsk keeps them: it trims its own copy of the ask.
		for i, m := range ask.Move {
			if m = strings.TrimSpace(m); !slices.Contains(offered, m) {
				return fmt.Errorf("--move %q (option %d): stage %s offers %s", m, i+1, to, strings.Join(offered, ", "))
			}
		}
		return nil
	})
}

// handQuestion is the question a stage's card of moves takes over: the plain
// ask with options that stands, open or put aside, as the one hand ask on the
// bead, whose notes are given. Nil where none stands, where the ask wants
// words or is a secret, a command, a setup, a depot push or a mapping, and
// where a second hand ask stands, since the card cannot say which of two
// questions it is: those keep what the card of moves always did (putStageAsks).
func handQuestion(notes []Note) *ResolvedAsk {
	var found *ResolvedAsk
	resolved := ResolveAsks(notes, now())
	for i := range resolved {
		r := &resolved[i]
		if r.AskErr != nil || r.Ask.Stage != "" || (r.State != AskOpen && r.State != AskAside) {
			continue
		}
		if found != nil {
			return nil
		}
		found = r
	}
	if found == nil || len(found.Ask.Options) == 0 {
		return nil
	}
	a := found.Ask
	if a.Secret != nil || a.Run != nil || a.Setup != nil || a.DepotPush != nil || a.Identity != nil {
		return nil
	}
	return found
}

// absorb is the template of the card that takes over the question q, from
// tmpl, the stage's card of moves with its options set (movesOf). It reports
// whether the card is the question's own: every option of q named a move that
// tmpl still offers, so the card is q's question, context, options,
// recommendation and about-beads, each option firing the move it named, and
// not the stage's, with no You decide and no move appended. Otherwise it is a
// copy of tmpl that quotes q in its lines: its question, context and numbered
// options with the recommendation marked, and what no longer applies where a
// named move is no longer offered, so that nothing the asker wrote is lost
// with the ask.
func absorb(tmpl *AskTemplate, q Ask) (*AskTemplate, bool) {
	// The writer's options: yardr's own You decide, last, is not one of them.
	options := q.Options
	if n := len(options); n > 0 && strings.EqualFold(options[n-1].Label, YouDecide) {
		options = options[:n-1]
	}
	card := *tmpl
	var chosen []AskTemplateOption
	var gone []string
	if len(q.Move) > 0 && len(q.Move) == len(options) {
		for i, name := range q.Move {
			j := slices.IndexFunc(tmpl.Options, func(o AskTemplateOption) bool { return o.Outcome == name })
			if j < 0 {
				gone = append(gone, name)
				continue
			}
			// The asker's words and the yard's, side by side: the person is told
			// what the pick does by the yard, not only by the one who asked.
			chosen = append(chosen, AskTemplateOption{
				Outcome: options[i].Label, move: tmpl.Options[j].fires(),
				Consequence: options[i].Consequence + " (" + asClause(tmpl.Options[j].Consequence) + ")",
			})
		}
		if len(gone) == 0 {
			card.Kind, card.Accepts = AskChoice, ""
			card.Question, card.Context = q.Question, slices.Clone(q.Context)
			card.Options, card.Recommend, card.Why = chosen, q.Recommend, q.Why
			card.about = slices.Clone(q.About)
			return &card, true
		}
	}
	card.quote = quoteQuestion(q, options, gone)
	return &card, false
}

// asClause is the yard's sentence for a move ("The bead moves to new.") as a
// clause to follow an option's own words ("the bead moves to new").
func asClause(sentence string) string {
	sentence = strings.TrimSuffix(sentence, ".")
	if sentence == "" {
		return sentence
	}
	return strings.ToLower(sentence[:1]) + sentence[1:]
}

// quoteQuestion is the question as lines for a card's context: each one line,
// as a context line is, the numbered options after the asker's own context
// with the recommendation marked as an ask shows it (ResolvedAsk.Lines), and
// a last line naming the moves that are no longer offered, where there are
// any.
func quoteQuestion(q Ask, options []AskOption, gone []string) []string {
	lines := []string{"The question: " + OneLine(q.Question)}
	for _, line := range q.Context {
		lines = append(lines, OneLine(line))
	}
	for i, o := range options {
		line := fmt.Sprintf("%d. %s - %s", i+1, OneLine(o.Label), OneLine(o.Consequence))
		if i+1 == q.Recommend {
			line += fmt.Sprintf(" (recommended: %s)", OneLine(q.Why))
		}
		lines = append(lines, line)
	}
	if len(gone) > 0 {
		lines = append(lines, fmt.Sprintf("The moves it named are no longer offered here (%s): pick where the bead goes.", strings.Join(gone, ", ")))
	}
	return lines
}

// consumeQuestion takes the hand ask seq back on the bead row, authored yard,
// naming the cards the entry put in its place: the stage asks of stage that
// carry entry. It is the take-back of the moves card's counterpart
// (withdrawMoves), run once the cards are put, so that no question stays
// answerable on the bead beside them or for whoever works it next.
func (w *wtx) consumeQuestion(row *beadRow, seq uint64, stage string, entry uint64) error {
	notes, err := beadNotes(w.Tx, row.UUID)
	if err != nil {
		return err
	}
	var cards []string
	for _, r := range ResolveAsks(notes, now()) {
		if r.AskErr == nil && r.Ask.Stage == stage && r.Ask.Entry == entry {
			cards = append(cards, fmt.Sprint(r.Note.Seq))
		}
	}
	if len(cards) == 0 {
		return nil
	}
	which := "ask " + cards[0] + " is"
	if len(cards) > 1 {
		which = "asks " + strings.Join(cards[:len(cards)-1], ", ") + " and " + cards[len(cards)-1] + " are"
	}
	return w.yardTakesBack(row, seq, which+" the question now, with its options")
}
