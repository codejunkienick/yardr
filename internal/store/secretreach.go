package store

import (
	"fmt"
	"slices"
)

// notifierSource reports whether name is a source yardr has in Go that
// can tell a person: what a user's webhook is held to (checkWebhook),
// read here for what a secret given for one reaches.
func notifierSource(name string) bool { return slices.Contains(notifyAdapters(), name) }

// A secret ask's card says what it asks for (Line). Reach says what the value
// reaches once it is given, and what it does not: the file it lands in, the
// reads that use it and the ones that look in another file, which no line of
// Line says. A person answering sees it before they type, and again in the
// answer: the answer of a giving made at a terminal prints the lines after the
// first (ReachGiven), which its own line says with the file itself. Card and
// answer are made from the one function, so they cannot disagree.
//
// Line itself is not the place for it: the weight of the card is made from
// Line (secretWeight), and a card written before this stands as it was
// weighed, so changing Line would refuse every secret ask that is open.

// mintReach is the line for a source that may mint: whether it does is the
// source's declaration's to say, which a card read from its note alone does
// not have, so the line says where it applies.
const mintReach = "mint: where the source mints, these names are its inputs; a static token beside them is left as it is, and not used while they are set"

// Reach is what the value of a secret ask reaches, in short lines for a card
// to carry under Line: where it lands, which reads use it, and which do not.
// A harness key and a waiting login's input have none beside the line their
// card already has (cardLines): each says where its value goes and who reads
// it. For a crew member's own credential Line says when a session already
// running sees the value.
func (s AskSecret) Reach() []string {
	switch {
	case s.Harness != "" || s.Login != "":
		return nil
	case s.Hook != "":
		return []string{
			fmt.Sprintf("lands in: hooks/%s.secret in the yard's home, mode 0600", s.Hook),
			fmt.Sprintf("read by: the yard, to check each delivery to POST /hooks/%s", s.Hook),
			"replaces: what the file holds; where the hook stands already, deliveries signed with the old secret are refused from then on",
			"not reached: any env_file, call or thread command",
		}
	case s.Google != "" && s.As != "":
		return []string{
			fmt.Sprintf("lands in: .config/gws-accounts/%s/credentials.json in the yard machine's home, mode 0600", s.Google),
			fmt.Sprintf("read by: google-mint, minting a token for calls made as %s (yardr call ... --as %s)", s.As, s.As),
			"not reached: calls made without --as, other users' accounts, any env_file",
		}
	case s.Google != "":
		return []string{
			"lands in: .config/gws/credentials.json in the yard machine's home, mode 0600",
			"read by: google-mint, minting a token for the yard's Google calls",
			"not reached: calls made as a user, which read that account's own file; any env_file",
		}
	case s.User != "":
		return []string{
			fmt.Sprintf("lands in: %s's env_file, or users/%s.env in the yard's home when it has none", s.User, s.User),
			fmt.Sprintf("read by: the yard, at each message it sends %s through the source %s", s.User, s.Source),
			"not reached: calls and thread commands: it is an address, not a credential",
		}
	case s.Crew != "":
		return []string{
			fmt.Sprintf("lands in: crew member %s's env_file, or crew/%s.env in the yard's home when it has none", s.Crew, s.Crew),
			fmt.Sprintf("read by: %s's thread commands (yardr reply, thread open, thread state)", s.Crew),
			fmt.Sprintf("not reached: declared calls (yardr call %s ...), which read the source's own file; other crew members' files; a session already running", s.Source),
			mintReach,
		}
	case s.As != "" && notifierSource(s.Source):
		// A source in Go that tells a person reads the value as the URL
		// it tells them at, not as a credential of a call made as them:
		// the notifier's line stands in the calls-made-as line's place.
		return []string{
			fmt.Sprintf("lands in: %s's env_file, or users/%s.env in the yard's home when it has none", s.As, s.As),
			fmt.Sprintf("read by: the yard, at each message it sends %s through the source %s, from the file at each send", s.As, s.Source),
			"not reached: calls made without --as, thread commands, other users' files",
		}
	case s.As != "":
		return []string{
			fmt.Sprintf("lands in: %s's env_file, or users/%s.env in the yard's home when it has none", s.As, s.As),
			fmt.Sprintf("read by: calls made as %s (yardr call %s ... --as %s) and yardr source check %s --as %s, from the file at each call", s.As, s.Source, s.As, s.Source, s.As),
			"not reached: calls made without --as, thread commands, other users' files",
			mintReach,
		}
	}
	return []string{
		fmt.Sprintf("lands in: the source's env_file, or sources/%s.env in the yard's home when it has none", s.Source),
		fmt.Sprintf("read by: declared calls (yardr call %s ...) and the source's check, from the file at each call", s.Source),
		"not reached: thread commands (yardr reply, thread open, thread state), which read the speaker's own file; a crew member's is given with --for-crew",
		mintReach,
	}
}

// ReachGiven is Reach for what is printed once the value is given: the first
// line of Reach says where it lands, which the giving's own line says already,
// with the file itself, so the lines after it are the rest.
func (s AskSecret) ReachGiven() []string {
	if reach := s.Reach(); len(reach) > 0 {
		return reach[1:]
	}
	return nil
}

// cardLines are the lines a card of the secret ask s carries under Line,
// without the margin: the pack's sign-in steps where its credential
// declares them, where its value goes, in the words that suit what it
// is, and for a value kept in a file its Reach.
func (s AskSecret) cardLines() []string {
	switch {
	case s.Harness != "":
		return []string{"the value is kept out of notes, events and logs; each such session receives it in its environment"}
	case s.Login != "":
		return []string{"it goes to the waiting login only; it is kept nowhere, and no agent is shown it"}
	}
	lines := s.LoginLines()
	lines = append(lines, "it goes to a file only the yard's server reads; no agent is shown it")
	return append(lines, s.Reach()...)
}
