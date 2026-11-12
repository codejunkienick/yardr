package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"yardr.dev/yardr/internal/store"
)

func (a *app) newBeadAnswerCmd() *cobra.Command {
	var ansWords, ansDecline, ansAccount string

	var ansLater, ansSecret, ansApprove bool
	var ansValue []string
	answer := &cobra.Command{
		Use:   "answer <id> <ask> [<n> [words]]",
		Short: "Answer an ask: pick an option, answer with words, give a secret, approve a command, put it aside, or decline it",
		Long: `Answers an ask on a bead: picks option <n> with any words, answers with
words alone, puts the ask aside, or declines it. The answer is a note of
kind answer naming who picked which option; a newer pick supersedes the
older one for display, and every pick stays as a note. Nothing is deleted.

  yardr bead answer <id> <ask> <n> [words]      pick option n with words alongside
  yardr bead answer <id> <ask> --words -        answer an ask with no options, the words on stdin
  yardr bead answer <id> <ask> --later             put it aside for 24 hours
  yardr bead answer <id> <ask> --decline -      refuse to decide it, the words on stdin
  yardr bead answer <id> <ask> --secret            give the secret a secret ask asks for
  yardr bead answer <id> <ask> <n> --secret        pick a setup ask's preset with the account typed
  yardr bead answer <id> <ask> <n> --account <source>   pick a setup ask's preset, taking a listed account
  yardr bead answer <id> <ask> <n>                 pick a drift card's preset, keeping the standing account
  yardr bead answer <id> <ask> --approve           approve the command a run ask proposes
  yardr bead answer <id> <ask> --approve --value <name>=<value>   approve it giving the values the card asks for

<ask> is the ask note's seq, as prime, review and chat print it. <n> counts
the ask's options as shown, "You decide" last where it has one.

Words alone on an ask with options are refused. They go as a plain note
(yardr bead note) and leave the ask open. Talk about the question stays
where talk lives, in the conversation with the asker.

--words and --decline may be - to read their words from stdin, which is how
to write words that quote code or file names: inside a double-quoted
argument the shell runs every backtick span and $( ) and puts what it prints
in the words, where a heredoc with a quoted delimiter expands nothing. A
pick with words is <n> --words -:

  yardr bead answer <id> <ask> --words - <<'WORDS'
  the words, as they are
  WORDS

Stdin is read once, so only one of the two is -. Words with nothing on stdin
are refused, and the ask stays open. A secret is never given as words:
--secret takes nothing else, and is refused beside either.

--later leaves the person's queue for 24 hours or until the asker's next
note on the bead, whichever is first; answering early is a pick like any
other. --decline ends the ask with no option picked. Wherever the ask is
shown it reads "<user> declined to decide this: <words>". A declined ask
and an ask answered with words take no further answer; a picked ask takes
only a newer pick.

--secret answers a secret ask (yardr bead ask --secret), as yardr source
key gives a credential: each name the ask asks for is asked for in turn at
a terminal and nothing is shown as it is typed, once the gate below is
passed. The value is never an argument and never a variable of the
command's environment. The
yard's server writes it into the file the source's credential is read
from, and the answer on the bead says who gave which names: the value is
in that file and nowhere else. It is the operator's to give, and is
refused from the pane of an agent. An ask for a user's own address in a
source (yardr bead ask --secret <source> --for-user <user>) is that user's
to give, or the operator's, into the user's env_file, and names the
command that tells the user through the source when that is still to be
run. An ask for a crew member's own credential in a source (yardr bead ask
--secret <source> --for-crew <name>) is the operator's to give, into the
crew member's env_file, and takes effect when the crew member next starts.
An ask for a user's own credential in a source (yardr bead ask --secret
<source> --as <user>) is that user's to give, or the operator's, into the
user's env_file, and names the check that tries it as them.
An ask for a waiting login's input (yardr bead ask --secret-login
<login>) is handed to the login and kept nowhere; with --as <user> it is
that user's to give, or the operator's. Giving again replaces what was
given.
When the yard wrote the file and could not record it, it says so with the
names and the file: give it again. A secret ask takes no pick and no
words, which are refused and kept nowhere; --later puts it aside as any
ask, and it is declined with --decline '', without words.

<n> --secret answers a setup ask (yardr bead ask --setup): the preset
pick with the account typed unseen beside it, as --secret types it.
<n> --account <source> answers it taking the named candidate's account
instead, one the card lists with its address; where the card lists any,
exactly one of the two settles the account, and where it lists none the
typed value does. A drift card, on a pack already applied, is kept with a
bare <n> where its file holds every name, typed missing names where the
update newly needs them, or a candidate's file taken as ever. The yard
then applies the pack, takes the account, and grants the preset picked,
as the operator, and records what ran. It is the operator's to give, and
is refused from the pane of an agent. A pick with --secret or --account
is allowed on a setup ask only, as a bare pick that keeps is; everywhere
else a secret answer still takes no pick.

--approve answers a run ask (yardr bead ask --run): the yard runs the
command the ask holds, exactly as its card shows it, as the operator, and
records on the bead what ran and what it printed. It is the operator's to
give and nobody else's, and is refused from the pane of an agent whatever
--actor says. A user's own subscription, and which parts they are told
of, is approved by the one it was asked of, that user or the operator, and runs as them. A command runs once for one ask: an ask that was approved,
declined or taken back takes no approval. It is refused too, and nothing
runs, when the yard is no longer what the card was written from: what the
command changes was changed in between, or what it names was replaced
under its name. The ask is then declined and asked again. What the command
itself would refuse at a terminal is refused in the same words. A run ask
takes no pick and no words, which are refused; --later puts it aside and
--decline <words> ends it, as for any ask. The card names the command
that takes the change back and who may run it: anyone, but for a source's
limits (the operator) and a user's subscription (the user or the
operator).

A card that asks for values in the clear (a hook add the yard registers
itself, whose register call wants the project's id) is approved with
--value <name>=<value>, repeatable, one for each name the card shows:
the yard sends them to the register call with the URL and the secret,
and keeps none. A name the card asks for nothing of, and a missing one,
refuse the approval and nothing runs.

A weighty card is one whose approval widens what reaches outside the yard,
whose word or credential counts as a person's, or what runs inside it; its
WEIGHTY lines say how, and every secret ask and every setup ask is one. The
approvals above stand behind a gate for it: --approve, --secret, <n> --secret,
<n> --account <source>, a bare <n> on a drift card, and a pick on a mapping
ask that writes a user's identity. At a terminal the command prints the
card's weight and asks for the ask's number typed back, before it reads a
value, and goes on only when it is: friction against approving on reflex,
and no authentication, since the terminal is the yard's own machine and its
shell is the operator's. Without a terminal (standard input is not one) it
declines, and says to approve the card in footplate, or in a
terminal on the yard's machine. No flag gets round that: a gate that a
script walks through is not a gate. A routine card has no gate.

An ask another yard asked is answered as <bead>@<peer>, the ref the
decisions queue shows: the answer is queued home for that yard instead
of written as a note, and the question stands decided here from then
on. Only the person the question is for answers it, the link must carry
asks out (yardr peer allow <peer> --send ask), and --later there is 24
hours by this yard's clock and nothing else. Words alone on an ask with
options go as mail to the asker instead (yardr mail <asker>@<peer>).`,
		Args: cobra.RangeArgs(2, 4),
		RunE: func(c *cobra.Command, args []string) error {
			st, err := a.store(c.Context())
			if err != nil {
				return err
			}
			// A bead@peer ref names a question another yard asked: it
			// stands on no bead of this yard, so there is no bead to
			// read, and the answer is queued home instead of noted.
			id := args[0]
			if _, _, foreign := strings.Cut(id, "@"); !foreign {
				b, err := st.GetBead(c.Context(), id)
				if err != nil {
					return err
				}
				id = b.ID
			}
			askSeq, err := strconv.ParseUint(args[1], 10, 64)
			if err != nil || askSeq == 0 {
				return fmt.Errorf("ask %q: want the ask's seq, as prime, review and chat print it", args[1])
			}
			declined := c.Flags().Changed("decline")
			if ansSecret && ansAccount != "" {
				return fmt.Errorf("--secret types the account and --account takes a listed one: one of the two")
			}
			if ansAccount != "" {
				if len(args) != 3 || ansWords != "" || ansLater || declined || len(ansValue) > 0 {
					return fmt.Errorf("--account takes a candidate of a setup ask with its preset pick: yardr bead answer %s %d <n> --account <source>", id, askSeq)
				}
				n, pickErr := strconv.Atoi(args[2])
				if pickErr != nil || n < 1 {
					return fmt.Errorf("--account takes a candidate of a setup ask with its preset pick: yardr bead answer %s %d <n> --account <source>", id, askSeq)
				}
				return a.approveSetupAccount(c, st, id, askSeq, n, ansAccount)
			}
			if ansSecret {
				if len(args) > 2 || ansWords != "" || ansLater || declined || len(ansValue) > 0 {
					// A pick with --secret settles a setup ask alone.
					if len(args) == 3 && ansWords == "" && !ansLater && !declined && len(ansValue) == 0 {
						if n, pickErr := strconv.Atoi(args[2]); pickErr == nil && n >= 1 {
							return a.approveSetup(c, st, id, askSeq, n)
						}
					}
					return fmt.Errorf("--secret gives the secret and takes nothing else: the value is typed at a terminal")
				}
				if setup, err := askedSetup(c.Context(), st, id, askSeq); err == nil && setup != nil {
					return fmt.Errorf("ask %d sets up a pack: pick its preset with the account: yardr bead answer %s %d <n> --secret (or --account <source> where the card lists one)", askSeq, id, askSeq)
				}
				return a.giveSecret(c, st, id, askSeq)
			}
			if ansApprove {
				if len(args) > 2 || ansWords != "" || ansLater || declined {
					return fmt.Errorf("--approve runs the command the ask holds, with --value alone beside it")
				}
				values := map[string]string{}
				for _, kv := range ansValue {
					name, value, ok := strings.Cut(kv, "=")
					if !ok || name == "" || value == "" {
						return fmt.Errorf("--value %q: want <name>=<value>, one the card asks for", kv)
					}
					values[name] = value
				}
				if card, ok := askedCard(c.Context(), st, id, askSeq); ok && card.Run != nil && store.IsDevicePairRun(card.Run.Argv) {
					// A terminal is on the yard's own machine: no edge carries
					// this approval or its answer, and the code it shows is
					// shown in this sitting alone.
					fmt.Fprintln(c.ErrOrStderr(), "a pairing approval at this terminal shows its code here alone: no edge carries it, and it is kept nowhere.")
				}
				weighed, err := a.gate(c, st, id, askSeq, "approve", "--approve")
				if err != nil {
					return err
				}
				done, err := st.ApproveAsk(c.Context(), id, askSeq, a.who(), weighed, values)
				if err != nil {
					return err
				}
				return a.out(c.OutOrStdout(), done, func(w io.Writer) {
					ran := store.AskRan{Argv: done.Argv}
					fmt.Fprintf(w, "ask %d, asked by %s: %s\n", done.Ask, done.Asker, ran.Command())
					for _, does := range done.Does {
						fmt.Fprintln(w, does)
					}
					fmt.Fprintf(w, "%s approved it, and the yard ran it:\n%s\n", a.who(), done.Result)
					if p := done.Pairing; p != nil {
						// The pairing's code, answered once to the approver
						// alone and kept nowhere: shown here, in this
						// sitting, as a device token is shown once.
						fmt.Fprintf(w, "\nPairing is open for the device %s of %s, until %s. Give the device this yard's address and the code:\n\n  address  %s\n", p.Device, p.User, p.Expires.Local().Format("15:04:05"), p.Address)
						if p.Yard != "" {
							fmt.Fprintf(w, "  yard     %s\n", p.Yard)
						}
						fmt.Fprintf(w, "  code     %s\n", p.Code)
						if p.ChannelKey != "" {
							fmt.Fprintf(w, "  channel  %s\n", p.ChannelKey)
						}
						fmt.Fprintf(w, "\nShow the code to the device and to nobody else: whoever has it within its minutes is answered with that device's token.\n")
					}
				})
			}
			if len(ansValue) > 0 {
				return fmt.Errorf("--value gives the values an approval carries, with --approve")
			}
			// Past the branches that give a secret or approve, so that an
			// answer that types a secret never reads stdin.
			if ansWords, ansDecline, err = answerStdinWords(c, id, askSeq, ansWords, ansDecline); err != nil {
				return err
			}
			ans := store.Answer{Ask: askSeq}
			if len(args) >= 3 {
				n, err := strconv.Atoi(args[2])
				if err != nil || n < 1 {
					return fmt.Errorf("%q is not an option number: yardr bead answer %s %d <n> [words], or --words, --later, --decline",
						args[2], id, askSeq)
				}
				ans.Option = n
				ans.Words = ansWords
				if len(args) == 4 {
					if ansWords != "" {
						return fmt.Errorf("words after the option and --words: one of the two")
					}
					ans.Words = args[3]
				}
				if ansLater || declined {
					return fmt.Errorf("an option with --later or --decline: pick, put aside, or decline, one of the three")
				}
				if len(args) == 3 && ans.Words == "" {
					if setup, err := askedSetup(c.Context(), st, id, askSeq); err == nil && setup != nil && setup.Given && len(setup.Missing) == 0 {
						return a.approveSetupKeep(c, st, id, askSeq, n)
					}
				}
			} else {
				switch {
				case ansLater && (ansWords != "" || declined):
					return fmt.Errorf("--later puts the ask aside with nothing else")
				case declined && ansWords != "":
					return fmt.Errorf("--decline ends the ask with its own words, not --words")
				case !ansLater && !declined && ansWords == "":
					return fmt.Errorf("nothing to record: pick <n> [words], answer with --words, put it aside with --later, or decline it")
				}
				ans.Words, ans.Later, ans.Decline = ansWords, ansLater, declined
				if ans.Decline {
					ans.Words = ansDecline
				}
			}
			if ans.Option != 0 {
				// A pick that writes a user's identity on a mapping card is
				// a weighty approval: it takes its person's word that the
				// weight was seen, which here is the gate. Any other pick
				// asks for none.
				weighed := false
				if card, ok := askedCard(c.Context(), st, id, askSeq); ok && card.PickWrites(ans.Option) {
					var err error
					if weighed, err = a.weighed(c, id, askSeq, card.Weight, "pick", fmt.Sprintf("%d [words]", ans.Option)); err != nil {
						return err
					}
				}
				if err := st.AnswerAsk(c.Context(), id, askSeq, ans.Option, ans.Words, a.who(), weighed); err != nil {
					return err
				}
			} else if err := st.AddAnswer(c.Context(), id, ans, a.who()); err != nil {
				return err
			}
			return a.out(c.OutOrStdout(), map[string]any{"bead": id, "ask": askSeq, "state": askState(c.Context(), st, id, askSeq)}, func(w io.Writer) {
				printAsk(w, c.ErrOrStderr(), c.Context(), st, id, askSeq)
			})
		},
	}
	answer.Flags().StringVar(&ansWords, "words", "", "words alone, answering an ask with no options (or with a pick, in place of words after it), or - for stdin")
	answer.Flags().BoolVar(&ansLater, "later", false, "put the ask aside for 24 hours, or until the asker's next note on the bead")
	answer.Flags().StringVar(&ansDecline, "decline", "", "end the ask with no option picked: why you will not decide it, or - for stdin ('' on a secret ask, which takes no words)")
	answer.Flags().BoolVar(&ansApprove, "approve", false, "approve the command a run ask proposes: the yard runs it as you, exactly as the card shows it")
	answer.Flags().StringArrayVar(&ansValue, "value", nil, "with --approve: <name>=<value> the approval gives in the clear, one the card asks for (repeatable)")
	answer.Flags().BoolVar(&ansSecret, "secret", false, "give the secret a secret ask asks for: typed unseen at a terminal, after its card's gate")
	answer.Flags().StringVar(&ansAccount, "account", "", "with a preset pick on a setup ask: take this listed candidate's account instead of typing one")
	return answer
}
