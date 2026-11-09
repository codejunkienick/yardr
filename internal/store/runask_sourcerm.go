// The source rm row of the run-ask list (runList): removing a declared
// source, so that an operator with no terminal can take back one a card or
// a pack brought. A source is where a credential of the yard is sent, so
// removing one stays the operator's alone and never a delegated run
// (ActRemoveSource stands under maySource, which no site marks Delegate);
// the yardmaster proposes it and the operator approves it.
//
// The approval removes the source in its own write (deleteSource), as the
// command does. The files of its commands under the yard's home are the
// server's to remove after it, as the files of a source add are to write
// (carry.go): a store write removes no file, and the apply lock the server
// holds keeps the removal from a carrying out that writes the same copies.
//
// The row is registered here, beside its functions, and not in runList's
// literal: runask.go stands at the file-size ceiling the test gate holds
// (.yardr/test).
package store

import (
	"context"
	"fmt"
	"path/filepath"
)

// sourceRmUse is how a refusal names the source rm a run ask takes.
const sourceRmUse = "yardr source rm <name>"

func init() {
	runList = append(runList, runCommand{
		words:       []string{"source", "rm"},
		use:         sourceRmUse,
		description: "Remove a source and the files of its commands (the file its credential is in stays)",
		parse:       parseSourceRm,
		check:       checkSourceRm,
		card:        (*wtx).sourceRmCard,
		stale:       (*wtx).sourceRmStale,
		run:         runSourceRm,
	})
}

// parseSourceRm reads the words of source rm <name>: the source alone, by
// a name a source can have, since the files of its commands are found by it.
func parseSourceRm(rest []string) ([]string, error) {
	if len(rest) != 1 {
		return nil, fmt.Errorf("--run takes %s: one source, not %d words", sourceRmUse, len(rest))
	}
	if err := checkName("source", rest[0]); err != nil {
		return nil, fmt.Errorf("--run takes %s: %w", sourceRmUse, err)
	}
	return []string{rest[0]}, nil
}

// checkSourceRm is removing a source's own check as the asker: the check
// deleteSource refuses with, which a run ask may propose.
func checkSourceRm(ctx context.Context, w *wtx, argv []string, actor string) error {
	return w.may(ctx, actor, ActRemoveSource, Object{Kind: KindSource, Name: argv[2], Ask: Askable})
}

// sourceRefs are what still names a source after it is removed, which the
// removal leaves where it is: subscriptions of crew, groups and users,
// the threads crew members follow in it, and the hooks that serve it.
type sourceRefs struct{ subscriptions, participations, hooks int }

func (w *wtx) sourceReferences(name string) (sourceRefs, error) {
	var refs sourceRefs
	count := func(subs []Subscription) {
		for _, sub := range subs {
			if sub.Source == name {
				refs.subscriptions++
			}
		}
	}
	if err := each(w.Tx, bCrew, "", func(_ []byte, r *CrewMember) error { count(r.Subscriptions); return nil }); err != nil {
		return refs, err
	}
	if err := each(w.Tx, bGroups, "", func(_ []byte, g *Group) error { count(g.Subscriptions); return nil }); err != nil {
		return refs, err
	}
	if err := each(w.Tx, bUsers, "", func(_ []byte, u *User) error { count(u.Subscriptions); return nil }); err != nil {
		return refs, err
	}
	if err := each(w.Tx, bThreads, "", func(k []byte, _ *Participation) error {
		if _, source, _, ok := splitThreadKey(string(k)); ok && source == name {
			refs.participations++
		}
		return nil
	}); err != nil {
		return refs, err
	}
	err := each(w.Tx, bHooks, "", func(_ []byte, h *Hook) error {
		if h.Source == name {
			refs.hooks++
		}
		return nil
	})
	return refs, err
}

// sourceCopies reports whether the source name has command files the
// removal takes from disk: its declaration names some, or copies stand
// under the home, and the inventory of them.
func (w *wtx) sourceCopies(src *Source) ([]AskInventory, bool, error) {
	inv, _, err := sourceInventory(w.home, src.Name, false)
	if err != nil {
		return nil, false, err
	}
	d, derr := src.Declaration()
	return inv, len(inv) > 0 || (derr == nil && d.HasCommandFiles()), nil
}

// sourceRmCard is the card of source rm <name>: the source goes with the
// grants of its calls and the counts and keys the yard kept for it, the
// files of its commands go too where it has any, and what stays is named:
// the file its credential is read from, and the subscriptions, followed
// threads and hooks that still name it. The approval is held to the source
// whole, with the copies of its commands (Seen), so a source replaced, or a
// copy changed, since the card was written is another card.
func (w *wtx) sourceRmCard(argv []string, asking bool) (AskRun, error) {
	name := argv[2]
	var src Source
	if had, err := get(w.Tx, bSources, name, &src); err != nil {
		return AskRun{}, err
	} else if !had {
		return AskRun{}, fmt.Errorf("source %s: %w", name, ErrNotFound)
	}
	fingerprint, err := w.sourceSeen(&src)
	if err != nil {
		return AskRun{}, err
	}
	inv, files, err := w.sourceCopies(&src)
	if err != nil {
		return AskRun{}, err
	}
	refs, err := w.sourceReferences(name)
	if err != nil {
		return AskRun{}, err
	}
	grants := len(keys(w.Tx, bSourceGrants, name+"\x00"))
	called := ""
	if d, err := src.Declaration(); err == nil {
		called = " (" + src.CalledAt(d) + ")"
	}
	sentences := []string{fmt.Sprintf("removes the source %s%s: its declaration goes, with %s of its calls and the counts and keys the yard kept for it.", name, called, countNoun(grants, "grant"))}
	seenMap := map[string]string{"source " + name: fingerprint}
	if files {
		sentences = append(sentences, fmt.Sprintf("The files of its commands under %s go too, removed by the yard's server after the approval.", filepath.Join(w.home, "sources", name)))
		seenMap["source "+name+" files"] = seen(inv)
	}
	if src.EnvFile != "" {
		sentences = append(sentences, fmt.Sprintf("The file its credential is read from stays where it is: %s.", src.EnvFile))
	}
	var left []string
	if refs.subscriptions > 0 {
		left = append(left, countNoun(refs.subscriptions, "subscription"))
	}
	if refs.participations > 0 {
		left = append(left, countNoun(refs.participations, "followed thread"))
	}
	if refs.hooks > 0 {
		left = append(left, countNoun(refs.hooks, "hook"))
	}
	if len(left) > 0 {
		sentences = append(sentences, fmt.Sprintf("%s still name it, and stay: nothing is heard from a source that is not there (yardr unsubscribe, yardr thread unfollow and yardr hook rm remove them).", andList(left)))
	}
	return AskRun{
		Argv:   argv,
		Does:   sentences,
		Back:   "the source is added again from its declaration (yardr source add <file>): its grants, its settings and the counts of its calls are not brought back",
		BackBy: "only the operator",
		Seen:   seenMap,
	}, nil
}

// sourceRmStale says why the ask seq for source rm is approved no more: the
// source is another under its name, or its command copies are not the ones
// the card named.
func (w *wtx) sourceRmStale(seq uint64, was, now AskRun) error {
	name := was.Argv[2]
	if err := w.staleSource(seq, name, was, now); err != nil {
		return err
	}
	return staleCard(seq, was, now)
}

// runSourceRm removes the source as actor through deleteSource, and answers
// what the command prints at a terminal: that it is removed, and how many
// subscriptions and followed threads still name it. A source with command
// files is carried out by the server after the approval (needsCarry), which
// answers in its place once the files are gone.
func runSourceRm(ctx context.Context, w *wtx, argv []string, actor string, by *approvedOn) (string, error) {
	name := argv[2]
	var src Source
	if had, err := get(w.Tx, bSources, name, &src); err != nil {
		return "", err
	} else if !had {
		return "", fmt.Errorf("source %s: %w", name, ErrNotFound)
	}
	_, files, err := w.sourceCopies(&src)
	if err != nil {
		return "", err
	}
	refs, err := w.sourceReferences(name)
	if err != nil {
		return "", err
	}
	if err := w.deleteSource(ctx, name, actor); err != nil {
		return "", err
	}
	if files {
		return CarryPending, nil
	}
	return fmt.Sprintf("%s: removed; %s and %s still name this source.", name, countNoun(refs.subscriptions, "subscription"), countNoun(refs.participations, "participation")), nil
}
