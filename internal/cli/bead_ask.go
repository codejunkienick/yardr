package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"yardr.dev/yardr/internal/agents"
	"yardr.dev/yardr/internal/store"
)

func (a *app) newBeadAskCmd() *cobra.Command {
	var askTo, askWhy string
	var askContext, askOptions, askAbout []string
	var askRecommend int
	var askNew bool
	var askBead store.NewBead
	var askSecret, askForUser, askForCrew, askAs, askRun, askRunTest, askRunRole, askSecretLogin, askSecretHook, askHookSource string
	var askSetup string
	var askDepotPush, askDepotPushOff string
	var askCrew, askGroup []string
	var askSourceBase string
	var askEnv []string
	var askTakeBack bool
	ask := &cobra.Command{
		Use:   "ask <id> <question|-> | ask --new <question|-> | ask --take-back <id> <ask> <reason|->",
		Short: "Ask someone a question as data: options to pick, answered, put aside or declined, or taken back",
		Long: `Asks a person a question as data, on any bead in any stage: the question in
one line, who must answer, two to five lines of context, the options each
with its consequence in a line, the one recommended and why, and the other
beads it is about. The person answers by picking an option, puts the
question aside, or declines it (yardr bead answer); the answer is the
option, so it cannot be misread.

The question is the last argument, or - to read the ask from stdin, which is
how to write one that quotes code or file names: inside a double-quoted
argument the shell runs every backtick span and $( ) and puts what it prints
in the ask, where a heredoc with a quoted delimiter expands nothing. Stdin is
read once and holds every text of the ask. Its first line is the question and
each line after it a line of context:

  yardr bead ask <id> - <<'ASK'
  the question, in one line
  a line of context
  another line of context
  ASK

An ask with options, a recommendation or a command to run labels every line
instead, a letter and a space, in any order and one line a field, since no
field continues on the next:

  Q: the question, once
  C: a line of context (two to five, at least one)
  O: <label>: <consequence>, one line for each option
  W: why the recommended one, once (--recommend names it)
  R: the command a run ask proposes, once (--run)
  T: its test command, once, with R: a depot test (--run-test)

A line with no label among labeled ones, a label among unlabeled ones, an
unknown label and a label with no words are refused, and nothing is asked.
With - no text is a flag: --context, --option, --why, --run and --run-test
are refused beside it, and so is --new's -b -, which would read the same
stdin. The flags that name no prose (--to, --recommend, --about, --setup,
--crew and the rest) go beside it. The secret flags name a source, a user, a
crew member or a login, and never read stdin: the value is typed unseen
where it is given (yardr bead answer <id> <ask> --secret), so that no
history keeps it.

The ask is a note of kind ask; its text is JSON, one schema: question, to,
context, options (a label and a consequence each), recommend and why,
about. prime, review and chat render it as numbered text with the
recommendation marked and its state; nobody reads the raw form.

yardr adds the option "You decide" to every ask that has options; the
writer does not write it and cannot leave it out. Picking it records that
the person left the question to the asker, who decides and notes what it
took.

An ask with no --option wants words; it is answered with --words. An ask
needs --context (at least one line; write two to five) and a consequence
on every option, so the card is never empty; both are refused without.
--to defaults to the yard's operator. --recommend names an option's
number with --why saying why in a line; without them no option is
marked.

--to <person>@<peer> asks a person of a linked yard: the ask is stored on
the bead as any ask, and queued for the peer's yard in the same write,
where it stands among the person's decisions; their pick comes back onto
the bead. The link must carry asks out (yardr peer allow <peer> --send
ask). That is all a user of the yard needs; a crew member must be allowed
the peer as well (yardr crew set <crew member> peers=<peer>), and an
agent working a bead asks no other yard. The daemon delivers it, so the
command returns before it has arrived: yardr peer show <peer> lists what
still waits.

An ask for a user of this yard is also sent to each name of a linked yard
the operator gave reach for that user (yardr peer allow <peer> --reach
<name>@<peer>=<user>), whoever asks: it stands among their decisions there
too, and the answer from there lands on the bead as one given here does.
The command names where it was queued for.

--secret <source> asks the operator for the credential of a declared
source: an API key or a token, never a login. The ask says where its
answer goes and the answer is no words: the person types the value where
nothing shows it (yardr bead answer <id> <ask> --secret), the yard's server
writes it into the file the source's credential is read from, and the
answer on the bead holds the names that were given and never the value. So
the value is in no note, event, mail, brief or pane; an agent that needs a
key asks for it this way and never has the person paste it. The names are
what the source needs given: the mint's inputs where the declaration names
a mint (credential.mint), else every variable of its credential; the mint's
names and the static ones are alternatives, not all required, so a minted
token is never asked for. --env narrows the ask to any names the
declaration has. The asker names no path and invents no name. --context
says where the person gets the value. The card's line "a
secret: <names>, for the source <source> (<where its calls go>)" is the
yard's, from the declaration, whatever the question says.

A secret ask takes no --option, --recommend or --about. It is refused, and
nothing is stored, for a source the yard does not have (the operator adds
it: asking gives the asker nothing), for one whose declaration names no
credential, for --to anyone but the operator, and for --to <person>@<peer>:
nothing of a secret crosses a link, and no copy goes to a linked yard.

--secret <source> --for-user <user> asks for that user's own address in a
source that tells people instead: the one variable the source's notify key
names (notify.address.env), which the yard
reads from the user's env_file at each send. It is asked of the user, which
is where --to then defaults, or of the operator for them, and of nobody
else. The value typed goes into the user's env_file, or with none set into
~/.yardr/users/<user>.env, which is then set as yardr user set sets it. It
is --for-user beside --secret because the source still names the variable
and where the value is sent, and the user only whose file it is kept in.
Giving it does not say the user is told through the source: the card and
the answer name the command that does (yardr user set <user>
notify=<source>). It takes no --env, and is refused for a source with no
notify key and for a user the yard does not have.

--secret <source> --for-crew <crew member> asks for that crew member's own
credential in the source, which a reply answers with: what --secret takes
(the mint's inputs where the declaration names a mint, else every variable
of its credential), which --env narrows to any names the declaration has.
The value typed goes into the crew member's env_file, or
with none set into ~/.yardr/crew/<name>.env, which is then set as yardr
crew set sets it. The crew member's running session does not see the new
value: it takes effect when it next starts (rotation or yardr crew restart
<name>), as the card and the answer say. It is --for-crew beside --secret
because the source still names the variables, and the crew member only
whose file they are kept in. It is asked of the operator only, and is
refused for a crew member the yard has not and for a source with no
credential. A crew member with an env_command as well keeps both: the
env_file line is the one written, and for a name both set the command's
value is what its session runs with.

--secret <source> --as <user> asks for that user's own credential in the
source, which a call made as them reads: every name under credential.mint
where the declaration names a mint, else the static ones (credential.env,
and credential.update_env where it names one), which --env narrows to any
name the declaration has. The value typed goes into the user's env_file,
or with none set into ~/.yardr/users/<user>.env, which is then set as
yardr user set sets it. It is --as beside --secret because the source
still names the variables, and the user only whose file they are kept in
and whose calls read them. It is asked of the user, which is where --to
then defaults, or of the operator for them, and of nobody else. It is
refused for a source with no credential and for a user the yard does not
have. It is not --for-user: that asks for the user's own address in a
source that tells people, and this for the credential a call made as them
reads; a source may have both to ask for.

--secret-login <login> asks for the input of the harness login that waits
under that reference: the login its leader's input need names. The value
typed goes to the waiting login and is kept nowhere, not even in the
answer, which holds the names as any giving does. It is asked of the
operator. With --as <user> the login is that user's own, led for the group
that carries it (yardr user add --login, yardr user login): the code its
sign-in page shows is the person's to read, so it is asked of them, where
--to then defaults, or of the operator for them, and they give it. It takes
no --env, no --for-user and no --for-crew. At the giving it is refused when
no login waits under the reference, and when the login that waits is
another user's than the one the ask is for (the --as user, else the
operator): the yard records whose login each wait is when its leader
registers it, from the group the leader was given, and a giving it refuses
leaves the wait waiting. The yardmaster asks one per input need of a login
it relays.

--secret-hook <hook> --source <source> asks the operator for the signing secret
of the hook <hook> of the declared source <source>: the value the service signs
its webhooks with, pasted from where the service shows it.
The yard's server writes it to the file it owns for the hook,
<home>/hooks/<hook>.secret (mode 0600), which the hook then names; the
value is in no note, event, mail, brief or pane, as for --secret. It is asked of the operator, takes no --env, no --for-user, no --for-crew and
no --as, and is refused for a source the yard does not declare, one whose
declaration has no hook key, and a hook the yard has already. The hook itself
is a second ask on the same bead (--run hook add <hook> --source <source>, or
R: on stdin), asked after this one: the operator gives the secret first, then approves the
hook, whose approval runs only with the secret standing in its file.

--run <command>, or R: <command> on stdin, proposes one yardr command that is
the operator's to give, for the operator to approve: a crew member that needs it asks this way instead
of mailing the person a line to paste into a terminal. The
command is written as it is typed, with or without the leading yardr, and is
one of the list the yard runs from a card:

` + askRunList + `
Whether each is the operator's is the command's own check: when the asker
may run the command themselves the ask is refused with that. What is the
operator's alone is refused as a run ask: what defines what the yard runs
(a source's command, a change to what a group runs beyond its prompt,
command and a permission preset that never asks, a device added or paired,
another yard paired or called elsewhere), what takes a file path, and what
reaches another yard.
Such a command is typed in a terminal. A link card (peer add, peer allow,
peer intake, peer identity, peer rm, peer set) can be approved from this
yard in a terminal or from a paired device. Peer add and peer set are
always strong (trust): the hosted web approves them only under a fresh
sign-in, and a paired device approves them as it approves any card.
Approval through a peer's own reach is refused: it cannot add, widen, move
or remove its link, route its beads or identify its crew.
The subscription, the tell, the user's identity and the test
message to them are a user's own as they are the operator's, since a
crew member does not decide what a person is told or who they are in
a source: the ask is for that user or for the operator (--to), and
the one asked approves it. The yard splits the command at spaces and nowhere
else: it never interprets quotes or shell punctuation. A peer-add invite
stays one word and the yardkey invite reader validates all of it, including
its URL; line breaks are refused. A comma is accepted only in a list the
command takes or in the peer-add invite (the parts of tell=, the kinds peer
allow carries, the grants crew set gives). A depot's test command goes beside --run, in --run-test <command> or T: <command>, since it needs those
characters. It checks the
command against the yard as it stands, and refuses one the yard would
refuse and one that would change nothing (a peer, source, group, crew
member, user, setting, timer or bead the yard has not, a number out of
range, what stands already, a grant or a subscription that is held
already, the parts a user is told of already; what the asker may run
needs no approval). The ask is stored with the command in the yard's own form and with the yard's
sentences for what it changes, made from the yard's records, whatever the
question says. The card shows the command whole and those sentences, and
each --context line with the asker's name before it. Nothing runs until
the operator approves (yardr bead answer <id> <ask> --approve), and then
the yard runs exactly the command shown, as the operator; an approval is
refused when the yard changed since the ask, which is then asked again:
what the command changes is not what the card says it is, or what it
names was replaced or set under its name since: the source, the link,
the group, the crew member, the user, the setting, the bead or the
timer.

A run ask takes no --option, --recommend or --about, and at most five
--context lines: why the asker wants it. It is refused, and nothing is
stored, for --to anyone but the operator (for a user's subscription,
tell, identity or test message, that user or the operator) and for
--to <person>@<peer>; no copy goes to a
linked yard. A crew member or a user writes one. An agent
working a bead does not: it says what it needs in a note on the bead.

--setup <pack> sets up an embedded pack of kind source as one decision:
the apply, the account and the grants. It takes --crew <name> and --group
<group>, repeatable, at least one: who the grants are for. --source-base
<name> calls the pack's region base <name>, for a pack whose source
declares bases; the card shows the base it will call. --recommend
counts the card's preset options as yardr pack show prints them, with
--why saying why, as on any option ask. The card shows the pack's
declaration whole with its mint, the account to type unseen or the listed
candidates to take, and one option per preset, each rendered by the yard
from the declarations with the grantee, and You decide last, which grants
the recommended preset. The operator approves with the preset pick and
the account together (yardr bead answer <id> <ask> <n> --secret, or
--account <source> where the card lists one); the yard then applies the
pack, sets the base where one was chosen, takes the account, and grants
the preset picked, as the operator.
An approval is refused when the yard changed since the ask, which is then
asked again. A pack past 40 lines, one carrying sources under different
credentials, and one with no presets stay on the existing per-step flow:
a pack apply card, an account card per source, and a grant card per
source and kind. --as is refused: a grant as a user is that user's alone
to give, and this card is the operator's.

A setup ask takes no --option, --about or --as, and at most five
--context lines. It is refused, and nothing is stored, for --to anyone
but the operator and for --to <person>@<peer>; no copy goes to a linked
yard. A crew member or a user writes one. An agent working a bead does
not: it says what it needs in a note on the bead.

--depot-push <depot> turns one depot's push to its origin on as one
decision, and --depot-push-off <depot> turns it off again: the yard's
server, on the operator's approval, adds YARDR_PUSH=origin at the front
of the cmd of the group <depot>-assembly (the rest of the cmd standing
untouched, YARDR_LAND as it was) and points that depot's own git at the
credential helper of the source whose host its origin is on, for that
host alone, through git -C <depot> config; the off card takes exactly
those two back out. Which source that is, its host, its helper and
the variable its token is in are the pack's to say, read from its
declaration. The card carries the outside weight, is refused on a yard
with no edge, for a depot whose origin is not https on such a host,
with no token of that source standing, already fully on (or, off, not
on), with no <depot>-assembly group, or with YARDR_PUSH of another
value standing, and where a helper already answers for the host the
approval verifies it is the pack's helper answering with the source's
token and writes nothing. A carry that failed after one half is
completed by filing the card again. Nothing here runs git push, and
no token stands in the card, the cmd, the config or a log.

A depot-push ask takes no --option, --about or --as, and at most five
--context lines. It is refused, and nothing is stored, for --to anyone
but the operator and for --to <person>@<peer>; no copy goes to a linked
yard. A crew member or a user writes one. An agent working a bead does
not: it says what it needs in a note on the bead.

--new asks a question that is about no bead: it takes the question alone,
makes a bead titled with it and the ask on that bead in one write, and
prints both. An ask that would be refused makes no bead. -b gives the bead
a body and --label its labels (a topic: label is where the question shows
among the person's decisions). The bead is of the type question: it exists
to carry the question, so nothing routes it, no agent is started on it and
no group is told of it, in whatever stage it stands. Whoever asked closes
it when the answer is on it, with what they did with the answer as the
reason (yardr bead close). Without --depot it is of no depot and under no
flow, in the stage new; with --depot it stands where that depot's beads
start. --stage places the bead instead, as bead create does and as a task:
it is routed from that stage like any bead (a depot's decide stage, where
the group of people that stage is routed to holds it), and a stage the
depot's flow does not have is refused.

--take-back takes an open ask back: yardr bead ask --take-back <id> <ask> -,
with the reason on stdin, for a question that is asked no more (it was settled another
way, or asked again in other words). It is the asker's to do, and the
operator's; anyone else is refused, since nobody ends a question in the
person's place. The ask leaves the person's open decisions wherever it
stands and shows among the decided ones as "taken back by <who>: <reason>",
never as an answer of theirs; the bead's notes say the same. It takes no
answer afterwards: a pick that arrives late is refused with who took it
back and why. An ask that is answered, declined or taken back already is
refused; one put aside is taken back as an open one is. Where the ask was
also sent to a linked yard (--to <person>@<peer>, or by reach) that yard is
told in the same write, and it leaves the decisions there with "it was
taken back in <yard>'s yard". It is a flag of ask, not of answer: answer is
what the person asked does, and this is the asker's own.`,
		Example: `  yardr bead ask yardr-n8ta - --to mckean --recommend 1 <<'ASK'
  Q: Move this yard's links to the declared source now?
  C: The links file declares the new source; 64 links still point at the old one.
  C: The move was tested on a copy just now and passed; the old source goes away Friday.
  O: Move them now: all 64 links point at the new source tonight
  O: After Friday: keep the old source until the release is out
  W: the test passed; waiting only risks Friday's rush
  ASK
  yardr bead ask yardr-n8ta - --to mckean@airy <<'ASK'
  Q: Move them now?
  C: The links file declares the new source.
  O: Move them now: all 64 links point at the new source tonight
  ASK
  yardr bead ask yardr-k3vd - --secret crm <<'ASK'
  The API key for the CRM
  Create one at https://crm.example.com/account/api (read only).
  ASK
  yardr bead ask yardr-k3vd - --secret phone --for-user mckean <<'ASK'
  Your id at the phone service
  It is on the first page of the service after login.
  ASK
  yardr bead ask yardr-k3vd - --secret phone --for-crew yardmaster <<'ASK'
  The token it answers with
  Create one at https://phone.example.com/account/api.
  ASK
  yardr bead ask yardr-k3vd - --secret google-mail --as ada <<'ASK'
  Your Google account for your mail?
  The yard reads your mail as you when it is granted it as you.
  ASK
  yardr bead ask yardr-k3vd - --secret-login claude-a1b2c3d4 <<'ASK'
  The code the sign-in page showed
  The login waits for what the sign-in page showed.
  ASK
  yardr bead ask yardr-k3vd - --secret-hook <hook> --source <source> <<'ASK'
  The signing secret for it
  It is shown once where the webhook is made in the service.
  ASK
  yardr bead ask yardr-k3vd - <<'ASK'
  Q: Set up the webhook?
  C: The signing secret is asked for on the ask above; give it first.
  R: hook add <hook> --source <source>
  ASK
  yardr bead ask yardr-k3vd - --setup google-mail --crew yardmaster --recommend 1 <<'ASK'
  Q: Set up Gmail for the yardmaster?
  C: The yardmaster reads mail to work beads; drafts only, the person sends.
  W: reading is the job; drafts can follow
  ASK
  yardr bead ask yardr-k3vd - <<'ASK'
  Q: Take more mail from airy?
  C: airy's yard was refused 4 mails in the last hour: the link takes 30.
  R: peer allow airy --mail-limit 300
  ASK
  yardr bead ask --new - --label topic:words <<'ASK'
  Q: Rename 'depot'?
  C: The glossary keeps it as the yard's word for a repository.
  O: Keep depot: nothing changes
  O: Rename it: every command, page and document changes once
  ASK
  yardr bead ask --take-back yardr-n8ta 2913 - <<'REASON'
  the design went back to draft; a new question follows
  REASON`,
		Args: func(c *cobra.Command, args []string) error {
			if askTakeBack {
				// It asks nothing: no flag of a question goes with it.
				var other []string
				c.Flags().Visit(func(f *pflag.Flag) {
					if f.Name != "take-back" {
						other = append(other, "--"+f.Name)
					}
				})
				if len(other) > 0 {
					return fmt.Errorf("--take-back takes the bead, the ask and why, and nothing of a question: not %s", strings.Join(other, ", "))
				}
				if len(args) != 3 {
					return fmt.Errorf("--take-back takes the bead, the ask and why: %s --take-back <id> <ask> - with the reason on stdin (got %d arguments)", c.CommandPath(), len(args))
				}
				return nil
			}
			if askNew {
				if len(args) != 1 {
					return fmt.Errorf("--new takes the question alone: it makes the bead the question stands on (got %d arguments)", len(args))
				}
				return nil
			}
			for _, name := range []string{"body", "depot", "stage", "label"} {
				if c.Flags().Changed(name) {
					return fmt.Errorf("--%s is for the bead --new makes; the bead %s names is changed with yardr bead update", name, c.CommandPath())
				}
			}
			return cobra.ExactArgs(2)(c, args)
		},
		PreRunE: func(c *cobra.Command, args []string) error {
			// An ask on stdin first: the texts it holds are what the checks
			// below judge, as if they were typed.
			if err := askFromStdin(c, args); err != nil {
				return err
			}
			if len(askEnv) > 0 && askSecret == "" && !c.Flags().Changed("secret-login") && !c.Flags().Changed("secret-hook") {
				return fmt.Errorf("--env narrows a secret ask: it goes with --secret <source>")
			}
			switch {
			// A setup's own combinations first, so their reasons stand:
			// --as with --setup is refused as the card's, not as a
			// secret's missing flag.
			case c.Flags().Changed("setup") && c.Flags().Changed("run"):
				return fmt.Errorf("--setup sets up a pack and --run proposes a command: one of the two")
			case c.Flags().Changed("setup") && c.Flags().Changed("secret"):
				return fmt.Errorf("--setup sets up a pack and --secret asks for a value: one of the two")
			case c.Flags().Changed("setup") && c.Flags().Changed("secret-login"):
				return fmt.Errorf("--setup sets up a pack and --secret-login asks for a value: one of the two")
			case c.Flags().Changed("setup") && c.Flags().Changed("secret-hook"):
				return fmt.Errorf("--setup sets up a pack and --secret-hook asks for a value: one of the two")
			case c.Flags().Changed("depot-push") && c.Flags().Changed("depot-push-off"):
				return fmt.Errorf("--depot-push turns a depot's push on and --depot-push-off turns it off: one of the two")
			case (c.Flags().Changed("depot-push") || c.Flags().Changed("depot-push-off")) && c.Flags().Changed("setup"):
				return fmt.Errorf("--depot-push turns a depot's push on or off and --setup sets up a pack: one of the two")
			case (c.Flags().Changed("depot-push") || c.Flags().Changed("depot-push-off")) && c.Flags().Changed("run"):
				return fmt.Errorf("--depot-push turns a depot's push on or off and --run proposes a command: one of the two")
			case (c.Flags().Changed("depot-push") || c.Flags().Changed("depot-push-off")) && c.Flags().Changed("secret"):
				return fmt.Errorf("--depot-push turns a depot's push on or off and --secret asks for a value: one of the two")
			case (c.Flags().Changed("depot-push") || c.Flags().Changed("depot-push-off")) && c.Flags().Changed("secret-login"):
				return fmt.Errorf("--depot-push turns a depot's push on or off and --secret-login asks for a value: one of the two")
			case (c.Flags().Changed("depot-push") || c.Flags().Changed("depot-push-off")) && c.Flags().Changed("secret-hook"):
				return fmt.Errorf("--depot-push turns a depot's push on or off and --secret-hook asks for a value: one of the two")
			case (c.Flags().Changed("depot-push") || c.Flags().Changed("depot-push-off")) && askAs != "":
				return fmt.Errorf("--depot-push refuses --as: this card is the operator's")
			case c.Flags().Changed("setup") && askAs != "":
				return fmt.Errorf("--setup refuses --as: a grant as a user is that user's alone to give, and this card is the operator's")
			case c.Flags().Changed("setup") && askForUser != "":
				return fmt.Errorf("--for-user says whose own address in a source is asked for: it goes with --secret <source>, not --setup")
			case c.Flags().Changed("setup") && askForCrew != "":
				return fmt.Errorf("--for-crew says whose own credential in a source is asked for: it goes with --secret <source>, not --setup")
			case !c.Flags().Changed("setup") && (c.Flags().Changed("crew") || c.Flags().Changed("group")):
				return fmt.Errorf("--crew and --group name who a setup's grants are for: they go with --setup <pack>")
			case !c.Flags().Changed("setup") && c.Flags().Changed("source-base"):
				return fmt.Errorf("--source-base names the region base a setup's calls go to: it goes with --setup <pack>")
			case askForUser != "" && askSecret == "" && !c.Flags().Changed("secret-login"):
				return fmt.Errorf("--for-user says whose own address in a source is asked for: it goes with --secret <source>")
			case askForUser != "" && len(askEnv) > 0:
				return fmt.Errorf("--for-user takes no --env: the source's notify key names the one variable that is a user's address")
			case askForCrew != "" && askSecret == "" && !c.Flags().Changed("secret-login"):
				return fmt.Errorf("--for-crew says whose own credential in a source is asked for: it goes with --secret <source>")
			case askAs != "" && askSecret == "" && !c.Flags().Changed("secret-login"):
				return fmt.Errorf("--as says whose own credential in a source is asked for: it goes with --secret <source>, or whose login a waiting login is: --secret-login <login>")
			case askForUser != "" && askForCrew != "":
				return fmt.Errorf("--for-user asks for a user's own address and --for-crew for a crew member's credential: one of the two")
			case askForUser != "" && askAs != "":
				return fmt.Errorf("--for-user asks for a user's own address and --as for a user's own credential: one of the two")
			case askForCrew != "" && askAs != "":
				return fmt.Errorf("--for-crew asks for a crew member's credential and --as for a user's own credential: one of the two")
			case c.Flags().Changed("secret") && c.Flags().Changed("secret-login"):
				return fmt.Errorf("--secret asks for a source's credential and --secret-login for a waiting login's input: one of the two")
			case c.Flags().Changed("secret") && c.Flags().Changed("secret-hook"):
				return fmt.Errorf("--secret asks for a source's credential and --secret-hook for a hook's signing secret: one of the two")
			case c.Flags().Changed("secret-login") && c.Flags().Changed("secret-hook"):
				return fmt.Errorf("--secret-login asks for a waiting login's input and --secret-hook for a hook's signing secret: one of the two")
			case c.Flags().Changed("secret-login") && len(askEnv) > 0:
				return fmt.Errorf("--secret-login takes no --env: a login input is the one name it is asked for")
			case c.Flags().Changed("secret-login") && askForUser != "":
				return fmt.Errorf("--secret-login takes no --for-user: a login input is the login's to take, not a user's address")
			case c.Flags().Changed("secret-login") && askForCrew != "":
				return fmt.Errorf("--secret-login takes no --for-crew: a login input is the login's to take, not a crew member's credential")
			case c.Flags().Changed("secret-hook") && len(askEnv) > 0:
				return fmt.Errorf("--secret-hook takes no --env: the signing secret is one value")
			case c.Flags().Changed("secret-hook") && askForUser != "":
				return fmt.Errorf("--secret-hook takes no --for-user: a signing secret is the operator's to give")
			case c.Flags().Changed("secret-hook") && askForCrew != "":
				return fmt.Errorf("--secret-hook takes no --for-crew: a signing secret is the operator's to give")
			case c.Flags().Changed("secret-hook") && askAs != "":
				return fmt.Errorf("--secret-hook takes no --as: a signing secret is the operator's to give")
			case c.Flags().Changed("secret-hook") && strings.TrimSpace(askSecretHook) == "":
				return fmt.Errorf("--secret-hook needs a hook name: lowercase letters, digits, - and _")
			case c.Flags().Changed("secret-hook") && askHookSource == "":
				return fmt.Errorf("--secret-hook needs --source <source>: the declared source the hook serves")
			case !c.Flags().Changed("secret-hook") && c.Flags().Changed("source"):
				return fmt.Errorf("--source names the hook's source: it goes with --secret-hook <hook>")
			case c.Flags().Changed("run") && c.Flags().Changed("secret"):
				return fmt.Errorf("--run proposes a command and --secret asks for a value: one of the two")
			case c.Flags().Changed("run") && c.Flags().Changed("secret-login"):
				return fmt.Errorf("--run proposes a command and --secret-login asks for a value: one of the two")
			case c.Flags().Changed("run") && c.Flags().Changed("secret-hook"):
				return fmt.Errorf("--run proposes a command and --secret-hook asks for a value: one of the two; ask for the hook's secret and the hook as two asks, the secret first")
			case c.Flags().Changed("run-test") && (!c.Flags().Changed("run") || !depotTestCommand(askRun)):
				return fmt.Errorf("--run-test is the command the run ask depot test <depot> proposes: it goes with that command")
			case c.Flags().Changed("run-role") && (!c.Flags().Changed("run") || !crewAddCommand(askRun)):
				return fmt.Errorf("--run-role is the role file the run ask crew add <name> ... carries: it goes with that command")
			}
			return nil
		},
		RunE: func(c *cobra.Command, args []string) error {
			st, err := a.store(c.Context())
			if err != nil {
				return err
			}
			if askTakeBack {
				return a.takeBackAsk(c, st, args)
			}
			question := args[len(args)-1]
			var b *store.Bead
			if !askNew {
				if b, err = st.GetBead(c.Context(), args[0]); err != nil {
					return err
				}
			}
			who := askTo
			if who == "" {
				// Their own address or credential is theirs to give
				// before it is the operator's.
				who = askForUser
			}
			if who == "" {
				who = askAs
			}
			if who == "" {
				// The operator setting names whom an ask with no --to
				// goes to, whichever users hold the operator role.
				who = yardOperator(c.Context(), st)
			}
			var options []store.AskOption
			for _, o := range askOptions {
				label, consequence, ok := strings.Cut(o, ":")
				if !ok {
					return fmt.Errorf("option %q: want <label>: <consequence>, with the colon", o)
				}
				options = append(options, store.AskOption{Label: label, Consequence: consequence})
			}
			q := store.Ask{
				Question: question, To: who, Context: askContext,
				Options: options, Recommend: askRecommend, Why: askWhy, About: askAbout,
			}
			if c.Flags().Changed("secret") {
				q.Secret = &store.AskSecret{Source: askSecret, Env: askEnv, User: askForUser, Crew: askForCrew, As: askAs}
			}
			if c.Flags().Changed("secret-login") {
				q.Secret = &store.AskSecret{Login: askSecretLogin, As: askAs}
			}
			if c.Flags().Changed("secret-hook") {
				q.Secret = &store.AskSecret{Source: askHookSource, Hook: askSecretHook}
			}
			if c.Flags().Changed("setup") {
				// As named, in the order typed: the yard stores them once
				// each, the groups before the crew members.
				var to []store.Grantee
				for _, g := range askGroup {
					to = append(to, store.Grantee{Group: g})
				}
				for _, cm := range askCrew {
					to = append(to, store.Grantee{Crew: cm})
				}
				q.Setup = &store.AskSetup{Pack: askSetup, To: to, Base: askSourceBase}
			}
			if c.Flags().Changed("depot-push") {
				q.DepotPush = &store.AskDepotPush{Depot: askDepotPush}
			}
			if c.Flags().Changed("depot-push-off") {
				q.DepotPush = &store.AskDepotPush{Depot: askDepotPushOff, Off: true}
			}
			if c.Flags().Changed("run") {
				if q.Run, err = askRunCarried(askRun, askRunTest, c.Flags().Changed("run-test"), askRunRole, c.Flags().Changed("run-role")); err != nil {
					return err
				}
			}
			var seq uint64
			if askNew {
				nb := askBead
				nb.Priority = defaultPriority
				if nb.Body == "-" {
					b, err := io.ReadAll(c.InOrStdin())
					if err != nil {
						return err
					}
					nb.Body = strings.TrimRight(string(b), "\n")
				}
				// One call, so one write: a server from before it knows
				// no such call and makes nothing.
				made, err := st.AskNew(c.Context(), nb, q, a.who())
				if err != nil {
					return err
				}
				b, seq = made.Bead, made.Ask
			} else if seq, err = st.AddAsk(c.Context(), b.ID, q, a.who()); err != nil {
				return err
			}
			out := map[string]any{"bead": b.ID, "ask": seq, "to": who}
			if askNew {
				out["created"] = true
			}
			// Where the yard queued a copy of it, by the rule the write
			// that stored it went by (store.AskReach).
			var also []string
			// What the yard stored of a secret ask: the names are its own,
			// from the source's declaration.
			var secret *store.AskSecret
			if q.Secret != nil {
				if secret, err = askedSecret(c.Context(), st, b.ID, seq); err != nil {
					return err
				}
				out["secret"] = secret
			}
			// What the yard stored of a run ask: the command in its own
			// form, which is what an approval runs.
			var run *store.AskRun
			if q.Run != nil {
				if run, err = askedRun(c.Context(), st, b.ID, seq); err != nil {
					return err
				}
				out["run"] = run
			}
			// What the yard stored of a setup ask: the pack with the
			// yard's sentences for it, which is what an approval runs.
			var setup *store.AskSetup
			if q.Setup != nil {
				if setup, err = askedSetup(c.Context(), st, b.ID, seq); err != nil {
					return err
				}
				out["setup"] = setup
			}
			var depotPush *store.AskDepotPush
			if q.DepotPush != nil {
				if depotPush, err = askedDepotPush(c.Context(), st, b.ID, seq); err != nil {
					return err
				}
				out["depot_push"] = depotPush
			}
			if !strings.Contains(who, "@") && secret == nil && run == nil && setup == nil && depotPush == nil {
				peers, err := st.Peers(c.Context())
				if err != nil {
					return err
				}
				for _, t := range store.AskReach(peers, who, a.who()) {
					also = append(also, t.Address())
				}
				if len(also) > 0 {
					out["also"] = also
				}
			}
			return a.out(c.OutOrStdout(), out, func(w io.Writer) {
				if run != nil {
					on := fmt.Sprintf("ask %d on %s", seq, b.ID)
					if askNew {
						on = fmt.Sprintf("%s created; ask %d on it", b.ID, seq)
					}
					fmt.Fprintf(w, "%s for %s: to approve, %s\n", on, who, run.Command())
					return
				}
				if secret != nil {
					on := fmt.Sprintf("ask %d on %s", seq, b.ID)
					if askNew {
						on = fmt.Sprintf("%s created; ask %d on it", b.ID, seq)
					}
					if secret.Hook != "" {
						fmt.Fprintf(w, "%s for %s: a secret, the signing secret for the hook %s (POST /hooks/%s)\n", on, who, secret.Hook, secret.Hook)
						return
					}
					if secret.Login != "" {
						whose := "the"
						if secret.As != "" {
							whose = secret.As + "'s"
						}
						fmt.Fprintf(w, "%s for %s: a secret, %s for %s waiting login %s\n", on, who, secret.Names(), whose, secret.Login)
						return
					}
					if secret.User != "" {
						fmt.Fprintf(w, "%s for %s: a secret, %s, %s's own address in the source %s\n", on, who, secret.Names(), secret.User, secret.Source)
						return
					}
					if secret.Harness != "" {
						label := secret.Harness
						if h, ok := agents.Lookup(secret.Harness); ok && h.Label != "" {
							label = h.Label
						}
						fmt.Fprintf(w, "%s for %s: a secret, %s, the API key %s runs with on the operator's login, asked for crew member %s\n", on, who, secret.Names(), label, secret.Crew)
						return
					}
					if secret.Crew != "" {
						fmt.Fprintf(w, "%s for %s: a secret, %s, for crew member %s in the source %s\n", on, who, secret.Names(), secret.Crew, secret.Source)
						return
					}
					if secret.As != "" {
						fmt.Fprintf(w, "%s for %s: a secret, %s of the source %s, as %s\n", on, who, secret.Names(), secret.Source, secret.As)
						return
					}
					fmt.Fprintf(w, "%s for %s: a secret, %s of the source %s\n", on, who, secret.Names(), secret.Source)
					return
				}
				if setup != nil {
					on := fmt.Sprintf("ask %d on %s", seq, b.ID)
					if askNew {
						on = fmt.Sprintf("%s created; ask %d on it", b.ID, seq)
					}
					fmt.Fprintf(w, "%s for %s: to approve, yardr bead answer %s %d <preset> --secret\n", on, who, b.ID, seq)
					return
				}
				if depotPush != nil {
					on := fmt.Sprintf("ask %d on %s", seq, b.ID)
					if askNew {
						on = fmt.Sprintf("%s created; ask %d on it", b.ID, seq)
					}
					verb := "on"
					if depotPush.Off {
						verb = "off"
					}
					fmt.Fprintf(w, "%s for %s: to approve, yardr bead answer %s %d --approve (turns push %s for depot %s)\n", on, who, b.ID, seq, verb, depotPush.Depot)
					return
				}
				printAsk(w, c.ErrOrStderr(), c.Context(), st, b.ID, seq)
				if _, peer, outside := strings.Cut(who, "@"); outside {
					fmt.Fprintf(w, "ask for %s queued; the daemon delivers it, and yardr peer show %s lists it while it waits\n", who, peer)
				}
				switch {
				case askNew && len(also) > 0:
					fmt.Fprintf(w, "%s created; ask %d on it for %s; also queued for %s\n", b.ID, seq, who, strings.Join(also, ", "))
				case askNew:
					fmt.Fprintf(w, "%s created; ask %d on it for %s\n", b.ID, seq, who)
				case len(also) > 0:
					fmt.Fprintf(w, "ask %d on %s for %s; also queued for %s\n", seq, b.ID, who, strings.Join(also, ", "))
				}
			})
		},
	}
	ask.Flags().StringVar(&askTo, "to", "", "who must answer (default the yard's operator)")
	ask.Flags().StringArrayVar(&askContext, "context", nil, "what the question is about, a line (repeatable; write two to five, at least one; C: on stdin)")
	ask.Flags().StringArrayVar(&askOptions, "option", nil, "an option as \"<label>: <consequence>\" (repeatable; none wants words; O: on stdin)")
	ask.Flags().IntVar(&askRecommend, "recommend", 0, "the recommended option's number, with --why")
	ask.Flags().StringVar(&askWhy, "why", "", "why the recommended option, in a line (W: on stdin)")
	ask.Flags().StringSliceVar(&askAbout, "about", nil, "the other beads the question is about")
	ask.Flags().StringVar(&askSecret, "secret", "", "ask for the credential of this declared source: answered with a value typed unseen, never with words")
	ask.Flags().StringVar(&askSecretHook, "secret-hook", "", "with --source: ask for the signing secret of this hook of a declared source: answered with a value typed unseen, kept in the hook's secret file")
	ask.Flags().StringVar(&askHookSource, "source", "", "with --secret-hook: the declared source the hook serves")
	ask.Flags().StringVar(&askSecretLogin, "secret-login", "", "ask for the input of the harness login waiting under this reference (its leader's input need names it): handed to the login, kept nowhere; with --as, the login is that user's own")
	ask.Flags().StringVar(&askForUser, "for-user", "", "with --secret: ask for this user's own address in the source (the variable its notify key names), kept in the user's env_file")
	ask.Flags().StringVar(&askForCrew, "for-crew", "", "with --secret: ask for this crew member's own credential in the source (the variables its credential names), kept in the crew member's env_file")
	ask.Flags().StringVar(&askAs, "as", "", "with --secret: ask for this user's own credential in the source (the names a call made as them reads), kept in the user's env_file; with --secret-login: the login is this user's own, and theirs to give")
	ask.Flags().StringVar(&askRun, "run", "", "propose this yardr command for the operator to approve, as it is typed: one the yard runs from a card (yardr bead ask --help; R: on stdin)")
	ask.Flags().StringVar(&askRunTest, "run-test", "", "with --run depot test <depot> (R:): the test command the ask proposes (T:), carried beside the command since --run quotes nothing")
	ask.Flags().StringVar(&askRunRole, "run-role", "", "with --run crew add <name> ... (R:): the file whose role text the ask carries beside the command, shown whole on the card")
	ask.Flags().StringVar(&askSetup, "setup", "", "set up this embedded pack of kind source as one decision: the apply, the account and the grants, with --crew/--group naming who the grants are for")
	ask.Flags().StringVar(&askDepotPush, "depot-push", "", "turn this depot's push to its origin on as one decision for the operator to approve: YARDR_PUSH=origin in <depot>-assembly's cmd and the pack's helper in the depot's git config, carried out by the server")
	ask.Flags().StringVar(&askDepotPushOff, "depot-push-off", "", "turn this depot's push off as one decision for the operator to approve: the exact reverse of --depot-push for the depot")
	ask.Flags().StringArrayVar(&askCrew, "crew", nil, "with --setup: the grants are for this crew member (repeatable; at least one of --crew and --group)")
	ask.Flags().StringArrayVar(&askGroup, "group", nil, "with --setup: the grants are for this group's sessions (repeatable; at least one of --crew and --group)")
	ask.Flags().StringVar(&askSourceBase, "source-base", "", "with --setup: the setup's calls go to the pack's region base <name>, for a pack whose source declares bases")
	ask.Flags().StringSliceVar(&askEnv, "env", nil, "with --secret: only these variables of the source's credential (default the mint's inputs where the declaration names a mint, else every one it names)")
	ask.Flags().BoolVar(&askTakeBack, "take-back", false, "take an open ask back instead of asking: <id> <ask> <reason|-> (the asker's, or the operator's)")
	ask.Flags().BoolVar(&askNew, "new", false, "the question is about no bead: make one titled with it, and the ask on it, in one write")
	// The bead of --new, by bead create's flags and defaults.
	ask.Flags().StringVarP(&askBead.Body, "body", "b", "", "with --new: the bead's description (- reads stdin)")
	ask.Flags().StringVarP(&askBead.Depot, "depot", "d", "", "with --new: the depot the bead is of (default none: under no flow, in stage new)")
	ask.Flags().StringVarP(&askBead.Stage, "stage", "s", "", "with --new: place the bead in this stage as a task, routed as bead create's is (decide, for a depot's); default a question nothing routes")
	ask.Flags().StringSliceVarP(&askBead.Labels, "label", "l", nil, "with --new: the bead's labels")
	return ask
}

// giveSecret is yardr bead answer --secret: past the gate of a weighty card
// (weighed.go), it reads the value of each name the ask asks for as yardr
// source key reads one, unseen from a terminal or as NAME=value lines from
// anything else, and gives them to the yard. What
// it prints is who gave which names and where the credential is set, never
// a value. A login input is read the same way, with no source to read it
// against, and handed to the waiting login.
func (a *app) giveSecret(c *cobra.Command, st store.API, id string, seq uint64) error {
	secret, err := askedSecret(c.Context(), st, id, seq)
	if err != nil {
		return err
	}
	// Every secret ask is weighty: its gate comes before a value is typed,
	// so a person sees what the card weighs first, and a command with no
	// terminal declines before it reads any.
	weighed, err := a.gate(c, st, id, seq, "give", "--secret")
	if err != nil {
		return err
	}
	if secret.Hook != "" {
		values, err := readHookKeys(secret, c.InOrStdin(), typedUnseen(c.InOrStdin(), c.ErrOrStderr()))
		if err != nil {
			return err
		}
		keyed, err := st.GiveSecret(c.Context(), id, seq, values, a.who(), weighed)
		if err != nil {
			return secretErr(err)
		}
		return a.out(c.OutOrStdout(), keyed, func(w io.Writer) {
			fmt.Fprintf(w, "%s gave %s · it is set in %s\n", a.who(), strings.Join(keyed.Given, " and "), keyed.File)
		})
	}
	if secret.Login != "" {
		values, err := readLoginKeys(secret, c.InOrStdin(), typedUnseen(c.InOrStdin(), c.ErrOrStderr()))
		if err != nil {
			return err
		}
		keyed, err := st.GiveSecret(c.Context(), id, seq, values, a.who(), weighed)
		if err != nil {
			return secretErr(err)
		}
		return a.out(c.OutOrStdout(), keyed, func(w io.Writer) {
			fmt.Fprintf(w, "%s gave %s · it went to the waiting login %s, and is kept nowhere\n", a.who(), strings.Join(keyed.Given, " and "), keyed.Login)
		})
	}
	if secret.Harness != "" {
		values, err := readHarnessKeys(secret, c.InOrStdin(), typedUnseen(c.InOrStdin(), c.ErrOrStderr()))
		if err != nil {
			return err
		}
		keyed, err := st.GiveSecret(c.Context(), id, seq, values, a.who(), weighed)
		if err != nil {
			return secretErr(err)
		}
		return a.out(c.OutOrStdout(), keyed, func(w io.Writer) {
			line := fmt.Sprintf("%s gave %s · it is set in %s", a.who(), strings.Join(keyed.Given, " and "), keyed.File)
			if keyed.Missing != "" {
				line += " · " + keyed.Missing
			}
			fmt.Fprintln(w, line)
			if keyed.Next != "" {
				fmt.Fprintln(w, "next: "+keyed.Next)
			}
		})
	}
	src, err := st.GetSource(c.Context(), secret.Source)
	if err != nil {
		return err
	}
	d, err := src.Declaration()
	if err != nil {
		return err
	}
	values, err := readKeys(d, secret.Env, c.InOrStdin(), typedUnseen(c.InOrStdin(), c.ErrOrStderr()), "")
	if err != nil {
		return err
	}
	keyed, err := st.GiveSecret(c.Context(), id, seq, values, a.who(), weighed)
	if err != nil {
		return secretErr(err)
	}
	return a.out(c.OutOrStdout(), keyed, func(w io.Writer) {
		line := fmt.Sprintf("%s gave %s · it is set in %s", a.who(), strings.Join(keyed.Given, " and "), keyed.File)
		if keyed.Missing != "" {
			line += " · " + keyed.Missing
		}
		fmt.Fprintln(w, line)
		if keyed.Next != "" {
			fmt.Fprintln(w, "next: "+keyed.Next)
		}
	})
}

// approveSetup is yardr bead answer <id> <ask> <n> --secret on a setup
// ask: it reads the account values as yardr source key reads them, unseen
// from a terminal or as NAME=value lines from anything else, and approves
// the setup with the preset pick. What it prints is the preset picked and
// who gave which names, never a value. Where the card lists candidates,
// --account <source> takes one instead (approveSetupAccount); where a drift
// card names missing names, only those are read.
