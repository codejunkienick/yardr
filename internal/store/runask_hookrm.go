// The hook rm row of the run-ask list (runList): removing a webhook
// endpoint. A hook decides whose deliveries of a source the yard hears and
// as whom it speaks, so removing one is trust, weighed as such though it
// narrows: it is approved on a card, never told after. It is also how a
// hook the yardmaster added for an operator on their word (a delegated
// hook add, design yardr-m90n 3.4) is taken back. The row is registered
// here, beside its functions, and not in runList's literal: runask.go
// stands at the file-size ceiling the test gate holds (.yardr/test).
//
// No delegated run removes a hook: ActRemoveHook stands under ruleOperator
// and no site marks it Delegate, so the yardmaster's call is refused in the
// words anyone's is.
package store

import (
	"context"
	"fmt"
	"slices"
	"strings"

	bolt "go.etcd.io/bbolt"
)

// hookRmUse is how a refusal names the hook rm a run ask takes.
const hookRmUse = "yardr hook rm <name>"

func init() {
	runList = append(runList, runCommand{
		words:       []string{"hook", "rm"},
		use:         hookRmUse,
		description: "Delete a hook (events it accepted are kept)",
		parse:       parseHookRm,
		check:       checkHookRm,
		card:        (*wtx).hookRmCard,
		stale:       (*wtx).hookRmStale,
		weigh:       hookRmWeight,
		run:         runHookRm,
	})
}

// parseHookRm reads the words of hook rm <name>: the hook alone.
func parseHookRm(rest []string) ([]string, error) {
	if len(rest) != 1 {
		return nil, fmt.Errorf("--run takes %s: one hook, not %d words", hookRmUse, len(rest))
	}
	if strings.HasPrefix(rest[0], "-") {
		return nil, fmt.Errorf("--run takes %s: %q is no hook name, and hook rm takes no flag", hookRmUse, rest[0])
	}
	return []string{rest[0]}, nil
}

// checkHookRm is removing a hook's own check as the asker: the operator's,
// which a run ask may propose.
func checkHookRm(ctx context.Context, w *wtx, argv []string, actor string) error {
	return w.may(ctx, actor, ActRemoveHook, Object{Kind: KindHook, Name: argv[2], Ask: Askable})
}

// hookIssueMoves is how many issue moves the hook still has to make: the
// ones its removal drops (dropIssueMoves).
func hookIssueMoves(tx *bolt.Tx, hook string) (int, error) {
	n := 0
	err := each(tx, bIssueMoves, "", func(_ []byte, m *IssueMove) error {
		if m.Hook == hook {
			n++
		}
		return nil
	})
	return n, err
}

// hookSafeShape reports whether the hook is one a hook add card can make
// again: no says, makes, map or acting crew member, and the secret file the
// yard owns for it (HookSecretFile).
func hookSafeShape(home string, h Hook) bool {
	return h.As == "" && len(h.Map) == 0 && len(h.Says) == 0 && len(h.Makes) == 0 && h.SecretFile == HookSecretFile(home, h.Name)
}

// hookRmCard is the card of hook rm <name>: the hook goes, and with it
// what it says, makes, maps and acts as; the deliveries it accepted stay,
// the issue moves it still had to make are dropped and counted, and its
// secret file and a webhook the yard registered at the service stay where
// they are, since no card removes a file or calls the service. The
// command that takes it back is the hook add card where the hook stood in
// the shape that card makes, and none otherwise. The approval is held to
// the hook whole and to the moves it had still to make (Seen).
func (w *wtx) hookRmCard(argv []string, asking bool) (AskRun, error) {
	name := argv[2]
	var h Hook
	if had, err := get(w.Tx, bHooks, name, &h); err != nil {
		return AskRun{}, err
	} else if !had {
		return AskRun{}, fmt.Errorf("hook %s: %w", name, ErrNotFound)
	}
	moves, err := hookIssueMoves(w.Tx, name)
	if err != nil {
		return AskRun{}, err
	}
	sentences := []string{fmt.Sprintf("removes the hook %s: POST /hooks/%s takes no more deliveries from the source %s, and the yard hears nothing from the source through it. What it accepted before stays.", name, name, h.Source)}
	if moves > 0 {
		verb := "is"
		if moves > 1 {
			verb = "are"
		}
		sentences = append(sentences, fmt.Sprintf("it drops the %s it still had to make, which %s not made later.", countNoun(moves, "issue move"), verb))
	}
	var goes []string
	if n := len(h.Says); n > 0 {
		goes = append(goes, countNoun(n, "line")+" it says of a bead")
	}
	if n := len(h.Makes); n > 0 {
		goes = append(goes, countNoun(n, "line")+" it makes beads by")
	}
	if n := len(h.Map); n > 0 {
		goes = append(goes, "its stage map of "+countNoun(n, "entry"))
	}
	if h.As != "" {
		goes = append(goes, "that it acts as the crew member "+h.As)
	}
	if len(goes) > 0 {
		sentences = append(sentences, "What it was set to do goes with it: "+andList(goes)+".")
	}
	sentences = append(sentences, fmt.Sprintf("Its signing secret stays in %s: no card removes a file.", h.SecretFile))
	if _, d, err := sourceIn(w.Tx, h.Source); err == nil && d.Hook != nil && d.Hook.Register != nil {
		sentences = append(sentences, fmt.Sprintf("The webhook the yard registered at %s for it stays registered there: remove it at the service.", d.Address()))
	}
	back, backBy := w.hookRmBack(h)
	return AskRun{
		Argv:   argv,
		Does:   sentences,
		Back:   back,
		BackBy: backBy,
		Seen: map[string]string{
			"hook " + name:            seen(h.Source, h.SecretFile, h.Unknown, h.As, h.Map, h.Says, h.Makes),
			"hook " + name + " moves": seen(moves),
		},
	}, nil
}

// hookRmBack is the way back a card can offer for removing h. A safe shape
// is not enough when its source is gone or no longer declares a hook; and a
// source that registers its own secret makes it again without a secret card.
// What it was set to do comes back on a hook add card carrying its table;
// only a secret file of the operator's is typed in a terminal.
func (w *wtx) hookRmBack(h Hook) (string, string) {
	if h.SecretFile != HookSecretFile(w.home, h.Name) {
		return "no card brings it back: a secret file of the operator's is typed by the operator in a terminal (yardr hook add)", "nobody"
	}
	_, d, err := sourceIn(w.Tx, h.Source)
	if err != nil {
		return fmt.Sprintf("no hook add card brings it back: the source %s is gone; add it and its hook declaration in a terminal first", h.Source), "only the operator"
	}
	if d.Hook == nil {
		return fmt.Sprintf("no hook add card brings it back: the source %s has no hook declaration; add it in a terminal first", h.Source), "only the operator"
	}
	if h.Unknown != "" && !d.HearsByIdentity() {
		return fmt.Sprintf("no hook add card brings it back: the source %s no longer hears by identity, so its --unknown setting cannot be restored; restore identity hearing in a terminal first", h.Source), "only the operator"
	}
	if !hookSafeShape(w.home, h) {
		back := "a hook add card carrying the table that stood brings it back: yardr bead ask <bead> - with Q: <question> and R: " + runCommandLine(hookTableRestoreArgv(h))
		if len(h.Says) > 0 || len(h.Makes) > 0 {
			back += " and S: and M: lines of that table (the hook_added event holds what stood)"
		}
		back += " on stdin"
		if d.Hook.RegistersSecret() {
			if _, err := w.hookPublicURL(h.Name); err != nil {
				return fmt.Sprintf("no hook add card brings it back: %s has no valid public hook address; set one in a terminal first", HookPublicURLSetting), "only the operator"
			}
			return back + " (the yard makes the signing secret again when it registers the hook)", "only the operator"
		}
		return back + " (its signing secret is given again on the hook's own secret card)", "only the operator"
	}
	back := runCommandLine(hookAddArgv(h))
	if d.Hook.RegistersSecret() {
		if _, err := w.hookPublicURL(h.Name); err != nil {
			return fmt.Sprintf("no hook add card brings it back: %s has no valid public hook address; set one in a terminal first", HookPublicURLSetting), "only the operator"
		}
		return back + " (the yard makes the signing secret again when it registers the hook)", "only the operator"
	}
	return back + " (its signing secret is given again on the hook's own secret card)", "only the operator"
}

// hookAddArgv is the hook add that makes the hook h again in the shape the
// hook add card makes.
func hookAddArgv(h Hook) []string {
	argv := []string{"hook", "add", h.Name, "--source", h.Source}
	if h.Unknown != "" {
		argv = append(argv, "--unknown", h.Unknown)
	}
	return argv
}

// hookRmWeight weighs a hook rm card trust, as the design does (yardr-m90n
// 3.3): a hook decides whose deliveries the yard hears and as whom it
// speaks, and removing one is approved before it is done, whether it was
// made by hand or by the yardmaster for the operator.
func hookRmWeight(w *wtx, run AskRun) ([]AskWeight, error) {
	return []AskWeight{weighs(WeightTrust, fmt.Sprintf("the hook %s decides whose deliveries the yard hears at /hooks/%s and as whom it speaks, and removing it changes that", run.Argv[2], run.Argv[2]))}, nil
}

// hookRmStale says why the ask seq for hook rm is approved no more: the
// hook is another under its name, or had other issue moves still to make.
func (w *wtx) hookRmStale(seq uint64, was, now AskRun) error {
	name := was.Argv[2]
	if key := "hook " + name; was.Seen[key] != now.Seen[key] {
		e := lastEvent(w.Tx, "hook_added", naming("hook", name))
		return fmt.Errorf("ask %d was written when the hook %s was another: it was added again or changed since%s. %s", seq, name, since(e), nothingRan)
	}
	if key := "hook " + name + " moves"; was.Seen[key] != now.Seen[key] {
		return fmt.Errorf("ask %d was written when the hook %s had other issue moves still to make: they were made or added since. %s", seq, name, nothingRan)
	}
	return staleCard(seq, was, now)
}

// runHookRm removes the hook as actor through deleteHook, and answers what
// the command prints at a terminal.
func runHookRm(ctx context.Context, w *wtx, argv []string, actor string, by *approvedOn) (string, error) {
	if err := w.deleteHook(ctx, argv[2], actor, by); err != nil {
		return "", err
	}
	return argv[2] + ": removed", nil
}

// deleteHook is DeleteHook inside the transaction, so that an approved run
// ask removes the hook in the write that records its approval. by is that
// ask, nil for a hook removed by hand. It removes the one hook named and
// nothing else: the deliveries it accepted stay, and the issue moves it had
// still to make go with it, counted on the event.
func (w *wtx) deleteHook(ctx context.Context, name, actor string, by *approvedOn) error {
	resolved, err := paneAuthor(w.Tx, callerOf(ctx), actor)
	if err != nil {
		return err
	}
	actor = resolved
	if err := w.may(ctx, actor, ActRemoveHook, Object{Kind: KindHook, Name: name}); err != nil {
		return err
	}
	if w.Bucket(bHooks).Get([]byte(name)) == nil {
		return fmt.Errorf("hook %s: %w", name, ErrNotFound)
	}
	if err := w.Bucket(bHooks).Delete([]byte(name)); err != nil {
		return err
	}
	data := map[string]any{"hook": name}
	// What the hook had still to do is not done behind the back of
	// whoever removed it.
	dropped, err := dropIssueMoves(w.Tx, name)
	if err != nil {
		return err
	}
	if dropped > 0 {
		data["issue_moves_dropped"] = dropped
	}
	if by != nil {
		data["approved"] = by
	}
	return w.event("", "hook_removed", actor, data)
}

// hookRmTarget reports whether ask seq on bead id is a hook rm, and the
// hook it removes, for ApproveAsk to hold the hook's lock across the
// approval as DeleteHook holds it: a registration in its phases is not
// removed from under its own call.
func (s *Store) hookRmTarget(id string, seq uint64) (string, bool) {
	argv := s.runArgvOf(id, seq)
	if len(argv) == 3 && slices.Equal(argv[:2], []string{"hook", "rm"}) {
		return argv[2], true
	}
	return "", false
}

// runArgvOf is the command of the run ask seq on bead id as the yard parsed
// it, nil for a bead or an ask that is not there, one that proposes none,
// and any other ask. It is a read, for a caller that decides before the
// write what the write will need.
func (s *Store) runArgvOf(id string, seq uint64) []string {
	var argv []string
	_ = s.read(func(tx *bolt.Tx) error {
		w := &wtx{Tx: tx, home: s.home, store: s}
		row, err := w.loadBead(id)
		if err != nil {
			return nil
		}
		notes, err := beadNotes(tx, row.UUID)
		if err != nil {
			return nil
		}
		for _, r := range ResolveAsks(notes, now()) {
			if r.Note.Seq != seq || r.AskErr != nil || r.Ask.Run == nil {
				continue
			}
			if _, parsed, err := parseRun(r.Ask.Run.Argv); err == nil {
				argv = parsed
			}
		}
		return nil
	})
	return argv
}
