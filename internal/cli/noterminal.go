package cli

// The "No terminal" section of a hosted yard's crew prime: what it says, and
// which of its two readings a yard gets.

import (
	"context"

	"yardr.dev/yardr/internal/store"
)

// noTerminalPrefix is the part every hosted yard reads: the people there have
// no shell on the yard's machine. The packs' yardmaster roles are written for
// any yard, and for a command no card carries they say to mail it in a fenced
// code block for the operator to type in a terminal. The rule supersedes those
// lines in so many words instead of cutting them out: that covers every pack's
// roles, present and future, and a role stays right for a yard that is not
// hosted.
const noTerminalPrefix = "This is a hosted yard. Its people have no terminal and no ssh to the machine it runs on. " +
	"They read mail and answer cards in footplate and the web view, so a command mailed for them to type has nowhere to be typed.\n\n" +
	"This section supersedes every line of your role, or of anything else you read, that has you mail, show or hand a person " +
	"a command to type, paste or run, or sends them to a terminal. That includes the fenced code block for a command no card carries, " +
	"the one for a command that is the operator's alone, the rule that what a person is to run goes in a fenced code block, " +
	"and \"hand the command to the operator\".\n\n"

const noTerminalCardTypes = "- to approve a yardr command that `yardr bead ask --help` names under `--run`: a run ask (`settings set <key> <value>` is one such command);\n" +
	"- to give a credential, a harness login's code or a webhook's signing secret: a secret ask (`--secret`, `--secret-login`, `--secret-hook`);\n" +
	"- to set up a pack's source: a setup ask (`--setup <pack>`).\n\n"

// noTerminalHead keeps the original card rule for yards where delegation is
// off.
const noTerminalHead = noTerminalPrefix + "Every command for the operator goes as a card, by what it asks of them:\n\n" + noTerminalCardTypes

// noTerminalDelegatedHead limits the cards to requests that need the
// operator's approval, leaving the delegated run as a clear separate way.
const noTerminalDelegatedHead = noTerminalPrefix +
	"Requests needing the operator's approval go by card, by what they ask of them:\n\n" +
	noTerminalCardTypes

// noTerminalDelegated is the paragraph a yard reads where its delegation switch
// is on: the yardmaster's own run of the operator's command is a way beside the
// cards, and the way out where no card carries the command (a group's kind,
// model, dir and worktrees, another crew member's kind, model and dir), which
// the fallback below would have had filed as a bead. It names the yardmaster in
// the third person because the section is every crew member's, and the store
// refuses a delegated run from anyone else. The list is users.md's and the rule
// is the role's, so the paragraph points at both and copies neither; a yard
// whose role predates the role's section still has the doc, which the binary
// carries. Its last sentence keeps the fallback from claiming a command the list
// covers.
const noTerminalDelegated = "Delegation is on here, so the cards are not the only way. " +
	"A command on the delegated list that the operator asked for in a local mail to the yardmaster is the yardmaster's to run, " +
	"with no card and no bead filed for one: it cites that mail (`--for <sender> --mail <seq>`), and the yard tells the operator after it ran. " +
	"The list and the limits on each run are in `yardr docs users \"The yardmaster for the operator\"` " +
	"and in the yardmaster's role (Run an operator's command for them). " +
	"The cards stay for what the list leaves out, and a command that neither a card nor the list carries is the case below.\n\n"

// noTerminalFallback is what the rule says of a command nothing carries: it is
// reported and a bead is filed, and it is never mailed as text to type.
const noTerminalFallback = "A command no card carries is one the yard refuses as a run ask, or that `--help` does not name. " +
	"Tell whoever needed it, in plain words, that no card carries it, what it was for and what stays undone. " +
	"File a bead for the yard to grow the card (`yardr bead create`, with the command, what it does and why it is wanted) and give them its id. " +
	"The command itself is never mailed as text to type."

// noTerminalDelegatedFallback also rules out commands the delegated list
// carries, which have no card by design.
const noTerminalDelegatedFallback = "A command that neither a card nor the delegated list carries is one the yard refuses as a run ask, " +
	"or that `--help` does not name. " +
	"Tell whoever needed it, in plain words, that no card carries it, what it was for and what stays undone. " +
	"File a bead for the yard to grow the card (`yardr bead create`, with the command, what it does and why it is wanted) and give them its id. " +
	"The command itself is never mailed as text to type."

// noTerminalRule is what a hosted yard's crew prime says after the role where
// the yard's delegation switch is off: every command for the operator goes as a
// card, and one no card carries is filed as a bead.
const noTerminalRule = noTerminalHead + noTerminalFallback

// noTerminalDelegatedRule is what it says where the switch is on, which is
// where a hosted yard's is unless its operator turned it off: the rule above
// with the delegated run between the cards and the fallback.
const noTerminalDelegatedRule = noTerminalDelegatedHead + noTerminalDelegated + noTerminalDelegatedFallback

// noTerminalSection is the text of the crew prime's No terminal section for
// this yard, and empty where the yard is not hosted: a yard that is not has a
// terminal, whatever its delegation switch says, and the role's own section on
// delegation covers it. The switch is read as the store reads it for a run
// (delegationOn): as set, else as hosted reads, which on a hosted yard is on.
// A setting that cannot be read is an error, as elsewhere in the prime, never
// a yard whose people may be told to open a terminal.
func noTerminalSection(ctx context.Context, st store.API) (string, error) {
	all, err := st.Settings(ctx)
	if err != nil {
		return "", err
	}
	if all[store.HostedSetting] != "true" {
		return "", nil
	}
	if v, set := all[store.DelegationSetting]; set && v != "true" {
		return noTerminalRule, nil
	}
	return noTerminalDelegatedRule, nil
}
