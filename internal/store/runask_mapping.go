// The user identity row of the run-ask list (runList): a user's
// identity in a source. It has what a row has: how its words are read
// into one stored form, the command's own check as the asker, the card
// the yard writes from its records, why an approval of an older card
// is refused, and the store function that makes the change.
package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// userIdentityUse is how a refusal names the user identity a run ask takes.
const userIdentityUse = "yardr user identity <user> --source <source> [--id <id>] [--key <key>]"

// parseUserIdentity reads the words of user identity <user> with its
// --source and --id and --key, one of the two at least, as the crew
// row reads its. Listing (--source left out) and removing (--rm) are
// no run ask: one needs no approval, and the other is typed.
func parseUserIdentity(rest []string) ([]string, error) {
	args, values, err := runWords(userIdentityUse, "user identity", "the operator's", rest, map[string]bool{"--source": true, "--id": true, "--key": true})
	if err != nil {
		return nil, err
	}
	switch {
	case len(args) != 1:
		return nil, fmt.Errorf("--run takes %s: one user, not %d words", userIdentityUse, len(args))
	case len(values["--source"]) != 1:
		return nil, fmt.Errorf("--run takes %s: one --source, not %d", userIdentityUse, len(values["--source"]))
	case len(values["--id"]) > 1:
		return nil, fmt.Errorf("--run takes %s: --id once, not %d times", userIdentityUse, len(values["--id"]))
	case len(values["--key"]) > 1:
		return nil, fmt.Errorf("--run takes %s: --key once, not %d times", userIdentityUse, len(values["--key"]))
	case len(values["--id"]) == 0 && len(values["--key"]) == 0:
		return nil, fmt.Errorf("--run takes %s: --id and/or --key", userIdentityUse)
	}
	out := []string{args[0], "--source", values["--source"][0]}
	for _, flag := range []string{"--id", "--key"} {
		if len(values[flag]) > 0 {
			out = append(out, flag, values[flag][0])
		}
	}
	return out, nil
}

// userIdentityOf is the user, the source and the id and key of a parsed
// user identity, each "" when the command leaves it out.
func userIdentityOf(argv []string) (user, source, id, key string) {
	user = argv[2]
	for i := 3; i+1 < len(argv); i += 2 {
		switch argv[i] {
		case "--source":
			source = argv[i+1]
		case "--id":
			id = argv[i+1]
		case "--key":
			key = argv[i+1]
		}
	}
	return user, source, id, key
}

// checkUserIdentity is saying who a user is in a source's own check as
// the asker: it is that user's to say, or the operator's for them
// (mayIdentify), and a crew member's proposal of it is the one's to
// approve whom the ask is put to. It returns nil where the asker may
// say it themselves, which askedRun refuses as needing no approval.
func checkUserIdentity(ctx context.Context, w *wtx, argv []string, actor string) error {
	user, _, _, _ := userIdentityOf(argv)
	if err := w.may(ctx, actor, ActIdentify, Object{Kind: KindUser, Name: user}); err != nil {
		// A crew member (or anyone else) may not say it, so the ask
		// stands: say whose approval it takes, in mayIdentify's words.
		return &OperatorOnlyError{msg: err.Error(), ask: Askable}
	}
	return nil
}

// userIdentityCard is the card of user identity: which user and which
// source, that threads opened there for them are then assigned to the
// id or key, and what stood. Where the source hears by identity it
// says the sender is then heard as theirs, and where the id or key
// stands on the source's ignore list it says the mapping clears it, as
// the write does. Asking refuses an identity that would change nothing.
func (w *wtx) userIdentityCard(argv []string, asking bool) (AskRun, error) {
	user, source, id, key := userIdentityOf(argv)
	src, d, err := sourceIn(w.Tx, source)
	if err != nil {
		return AskRun{}, err
	}
	u, err := getUser(w.Tx, user)
	if err != nil {
		return AskRun{}, err
	}
	unfolded, folded := key, foldIdentityKey(w.Tx, source, key)
	key = folded
	who := identitySays(id, key)
	cur, had := u.Identities[source]
	if asking && had && cur.ID == id && cur.Key == key {
		return AskRun{}, fmt.Errorf("user %s is %s in %s already: the command would change nothing", user, who, source)
	}
	does := []string{fmt.Sprintf("says the user %s is %s in %s: a thread opened there for them is assigned to %s.", user, who, source, who)}
	if d.HearsByIdentity() {
		does = append(does, fmt.Sprintf("%s hears by identity, so a delivery from %s is then heard as theirs.", source, who))
	}
	if unfolded != folded {
		does = append(does, fmt.Sprintf("%s carries keys lowercase, so %s is stored as %s.", source, unfolded, folded))
	}
	if had {
		does = append(does, fmt.Sprintf("They are %s in %s now.", identitySays(cur.ID, cur.Key), source))
	} else {
		does = append(does, fmt.Sprintf("They have no identity in %s now.", source))
	}
	var cleared []string
	for _, held := range []string{id, key} {
		if held != "" && slices.Contains(src.Ignored, held) {
			cleared = append(cleared, held)
		}
	}
	if len(cleared) > 0 {
		does = append(does, fmt.Sprintf("%s is ignored in %s now, and the mapping clears that entry in the same write.", strings.Join(cleared, " and "), source))
	}
	back := []string{"user", "identity", user, "--source", source}
	if had {
		if cur.ID != "" {
			back = append(back, "--id", cur.ID)
		}
		if cur.Key != "" {
			back = append(back, "--key", cur.Key)
		}
	} else {
		back = append(back, "--rm")
	}
	fingerprint, err := w.sourceSeen(src)
	if err != nil {
		return AskRun{}, err
	}
	return AskRun{
		Argv:   argv,
		Does:   does,
		Back:   runCommandLine(back),
		BackBy: user + " or the operator",
		Seen:   map[string]string{"user " + user: seen(cur.ID, cur.Key), "source " + source: fingerprint},
	}, nil
}

// userIdentityStale says why the ask seq for user identity is approved
// no more: the source is another under its name, or the user holds
// another identity there than the card says.
func (w *wtx) userIdentityStale(seq uint64, was, now AskRun) error {
	user, source, _, _ := userIdentityOf(was.Argv)
	if err := w.staleSource(seq, source, was, now); err != nil {
		return err
	}
	if key := "user " + user; was.Seen[key] != now.Seen[key] {
		match := func(e Event) bool { return e.Data["user"] == user && e.Data["source"] == source }
		set, removed := lastEvent(w.Tx, "identity_set", match), lastEvent(w.Tx, "identity_removed", match)
		switch {
		case set != nil && (removed == nil || set.Seq > removed.Seq):
			return fmt.Errorf("ask %d was written when the user %s held another identity in %s: it was set since%s. %s", seq, user, source, since(set), nothingRan)
		case removed != nil:
			return fmt.Errorf("ask %d was written when the user %s held another identity in %s: it was removed since%s. %s", seq, user, source, since(removed), nothingRan)
		}
		return fmt.Errorf("ask %d was written when the user %s held another identity in %s. %s", seq, user, source, nothingRan)
	}
	return staleCard(seq, was, now)
}

// runUserIdentity says who the user is in the source as actor through
// setUserIdentity, and answers the identity as it is stored, with each
// ignore entry the mapping cleared beside it.
func runUserIdentity(ctx context.Context, w *wtx, argv []string, actor string, by *approvedOn) (string, error) {
	user, source, id, key := userIdentityOf(argv)
	cleared, _, err := w.setUserIdentity(ctx, user, source, Identity{ID: id, Key: key}, actor, by)
	if err != nil {
		return "", err
	}
	out := fmt.Sprintf("user %s is %s in %s", user, identitySays(id, foldIdentityKey(w.Tx, source, key)), source)
	for _, held := range cleared {
		out += fmt.Sprintf("\n%s is no longer ignored in %s", held, source)
	}
	return out, nil
}
