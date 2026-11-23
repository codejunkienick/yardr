// The generic set-a-field row of the run list: a command that sets some
// fields of one record and keeps the rest, carded from its plan
// (runask_plan.go). A row of it says how to plan the command from the yard's
// records and the ask, how to word each field it changes, whose gate the
// asker meets, how heavy the card is and how the approval applies the plan;
// the engine writes the rest of the row as every hand-written row writes it:
// the card from the plan, the command that takes it back from the plan's
// befores, an approval held to the plan, and a refusal of a command that
// would change nothing.
package store

import (
	"context"
	"errors"
	"fmt"

	bolt "go.etcd.io/bbolt"
)

// setRow is one set-a-field command of the run list. T is what the row's
// plan keeps for its own apply beside the plan it stores: the record as it
// stands and as the approval leaves it.
type setRow[T any] struct {
	words            []string
	use, description string
	parse            func(rest []string) ([]string, error)
	// command is how the terminal names the command, "crew set", for the
	// card's Back; kind and key name the record in the yard's events
	// (lastEvent), for the refusal of a stale approval.
	command, kind, key string
	// opens is the card's first sentence, with %s for the plan's Of.
	opens string
	// plan reads the ask against the yard's records as they stand: the
	// plan, and what the row keeps for its apply. It refuses an ask the
	// yard would refuse, in its own words, whoever asks: it is the card's
	// validation. It reads no record of the asker's.
	plan func(w *wtx, run AskRun) (RunPlan, T, error)
	// check is the store's own gate for the change as actor, as the
	// command's store function holds it: nil lets the asker run it, and an
	// operator-only error is the one a card proposes.
	check func(ctx context.Context, w *wtx, t T, actor string) error
	// lines word one field of the plan for the card, in the order of
	// order; a field without its own words takes the engine's (fieldLines).
	lines map[string]func(w *wtx, run AskRun, p RunPlan, c PlanChange) []string
	order []string
	// backOrder is the order of the config fields in the card's Back, as
	// the terminal's own cards wrote it.
	backOrder []string
	backBy    string
	// seen is what the card keeps of the record in AskRun.Seen, for the
	// tell that a change stales an open ask (runask_stale.go), which reads
	// a record and not a plan; nil for none.
	seen func(run AskRun, t T) map[string]string
	// weigh is the card's weight, from the card the row wrote.
	weigh func(run AskRun) []AskWeight
	// apply makes the change the plan says as actor, through the store
	// function the terminal's command calls, and answers what it prints.
	apply func(ctx context.Context, w *wtx, run AskRun, t T, actor string, by *approvedOn) (string, error)
}

// row is the setRow as a row of the run list.
func (r setRow[T]) row() runCommand {
	c := runCommand{
		words: r.words, use: r.use, description: r.description, parse: r.parse,
		checkRun: r.checkRun, cardRun: func(w *wtx, run AskRun, _ bool) (AskRun, error) { return r.card(w, run) },
		stale: r.stale, runRun: r.runRun, plan: r.card,
	}
	if r.weigh != nil {
		c.weigh = func(_ *wtx, run AskRun) ([]AskWeight, error) { return r.weigh(run), nil }
	}
	return c
}

// checkRun refuses what the plan refuses and a change of nothing, and then
// asks the store's gate as the asker: what it holds to the operator alone is
// what this card proposes, so the card marks it askable here and nowhere
// else.
func (r setRow[T]) checkRun(ctx context.Context, w *wtx, run AskRun, actor string) error {
	p, t, err := r.plan(w, run)
	if err != nil {
		return err
	}
	// The gate lets a change of nothing through as it lets anyone's, which
	// would read as a command to run yourself.
	if len(p.Changes) == 0 {
		return fmt.Errorf("%s is set so already, so the command would change nothing", p.Of)
	}
	err = r.check(ctx, w, t, actor)
	var o *OperatorOnlyError
	if errors.As(err, &o) {
		return &OperatorOnlyError{msg: o.msg, ask: Askable}
	}
	return err
}

// card is the card of the plan: the opening sentence, then each field the
// plan changes in the row's words, the command that takes the plan back, and
// the plan itself.
func (r setRow[T]) card(w *wtx, run AskRun) (AskRun, error) {
	p, t, err := r.plan(w, run)
	if err != nil {
		return AskRun{}, err
	}
	does := []string{fmt.Sprintf(r.opens, p.Of)}
	for _, field := range r.order {
		c, ok := p.change(field)
		if !ok {
			continue
		}
		if words, ok := r.lines[field]; ok {
			does = append(does, words(w, run, p, c)...)
		} else {
			does = append(does, fieldLines(c)...)
		}
	}
	card := AskRun{
		Argv:   run.Argv,
		Does:   does,
		Back:   planBack(r.command, run.Argv[2], p, r.backOrder),
		BackBy: r.backBy,
		Role:   run.Role,
		Plan:   &p,
	}
	if r.seen != nil {
		card.Seen = r.seen(run, t)
	}
	return card, nil
}

// fieldLines are the engine's words for a field a row does not word itself.
func fieldLines(c PlanChange) []string {
	return []string{
		fmt.Sprintf("Its %s is %s now.", c.Field, or(c.Before, "not set")),
		fmt.Sprintf("It will be %s.", or(c.After, "not set")),
	}
}

// stale carries an approval on whose plan reads as the card's, and refuses
// one whose plan reads otherwise now, naming the record's last change and
// what moved: the yard then asks again with the plan it holds (askedAgain).
func (r setRow[T]) stale(w *wtx, seq uint64, was, now AskRun) error {
	if samePlan(was.Plan, now.Plan) {
		return nil
	}
	name := was.Argv[2]
	e := lastEvent(w.Tx, r.kind, naming(r.key, name))
	return &replanError{seq: seq, why: fmt.Sprintf("ask %d was written when the %s was another: its definition was set since%s, and %s", seq, now.Plan.Of, since(e), planMoved(was.Plan, now.Plan))}
}

// runRun plans once more in the write that approves, which held the plan to
// the card's, and applies it.
func (r setRow[T]) runRun(ctx context.Context, w *wtx, run AskRun, actor string, by *approvedOn) (string, error) {
	_, t, err := r.plan(w, run)
	if err != nil {
		return "", err
	}
	return r.apply(ctx, w, run, t, actor, by)
}

// PlanRun is the card a run ask of run.Argv would carry, drawn from the
// yard's records as they stand and stored nowhere: the plan, the sentences
// and the command that takes it back, for a terminal's dry run. Nothing is
// written and nobody's check is asked, so it says nothing of who may approve
// it. Only a row of the set-a-field engine has a plan; any other command is
// refused, as words that name no row are.
func (s *Store) PlanRun(ctx context.Context, run AskRun) (*AskRun, error) {
	c, argv, err := parseRow(run.Argv)
	if err == errNotRow {
		return nil, fmt.Errorf("%s has no plan: only some crew set settings are planned (prompt, preset, kind, model, dir)", runCommandLine(run.Argv))
	}
	if err != nil {
		return nil, err
	}
	if c.plan == nil {
		return nil, fmt.Errorf("%s has no plan: only some crew set settings are planned (prompt, preset, kind, model, dir)", runCommandLine(argv))
	}
	var card AskRun
	err = s.read(func(tx *bolt.Tx) error {
		w := &wtx{Tx: tx, store: s, home: s.home}
		card, err = c.plan(w, AskRun{Argv: argv, Role: run.Role})
		return err
	})
	if err != nil {
		return nil, err
	}
	card.Description = c.description
	return &card, nil
}
