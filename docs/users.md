# Users: people with an inbox

A user is a person as a party of the yard: a name and an inbox, as a
[crew member](crew.md) has. Crew members write to a user through yardr, and
the user reads it, and the whole exchange with a crew member, without going to
the crew member's pane. The yard itself tells a user of the events they
subscribed to, as notifications, in a part of the inbox apart from the mail.
A user who names a source for it is also told outside the yard, on a phone,
of what lands in that inbox. A user's [device](#devices) is a program of
theirs that calls the yard from outside it, let in by a token.

```sh
yardr user list                          # the users, each one's role, unread mail, where each is told; then whom the linked yards expose
yardr user add ada                       # an operator adds a user
yardr user add ada --login claude        # or adds them whole, on a login of their own (below)
yardr user login ada                     # their sign-in again, if it did not finish
yardr user set ada role=operator         # an operator makes ada one too: ada approves and administers
yardr user show ada
yardr user rm ada                        # the mail stays in the log; what carries their own login goes, the login folder stays
yardr user key ada pushover && yardr user set ada notify=pushover   # told on a phone, through a source
yardr user set ada tell=notifications    # of the notifications only: no mail and no asks on the phone
yardr user identity ada --source linear --id 4d3a2b1c-…   # who ada is in Linear: whom thread open --assign ada assigns
yardr user device add ada phone          # an operator: a device of ada's, and its token, shown once
yardr user device pair ada phone --qr    # or a code the device calls with, and is given its token for
yardr user device list                   # every device, when added, when and from where last used
yardr user device rm ada phone           # an operator or ada: the token is refused from now on

yardr mail mckean -                      # a crew member's session, or a user; the words on stdin
yardr inbox                              # as a user: Mail and Notifications, each with its unread
yardr inbox --mail --ack 412             # one part, and mark read what is through 412
yardr subscribe ada yardr --done         # ada is told of every bead that becomes done
yardr mail --with yardmaster                  # the exchange with the yardmaster, both directions
yardr mail --with yardmaster --after 412 --json
```

## The record

A user has a name, an inbox and the subscriptions that fill its
notifications ([subscriptions.md](subscriptions.md#users-as-subscribers));
`yardr user show <name>` lists them. It also has the source that tells
the user outside the yard and the path of the file their address is read
from ([below](#told-outside-the-yard)), and their
[devices](#devices). The record is where more about a
person goes later: a user who comes from another yard, which is not
built.

**Who a user is in a source** is on the record too, as `yardr crew
identity` says it for a crew member: `yardr user identity <name> --source
<source> --id <id> --key <key>` sets it, `--rm` removes it, and with no
`--source` it lists them. Only the user and the operator set or remove
it, never a crew member or a bead's agent: a thread assigned to the user
goes to whom it names. It has three uses: `yardr thread open <source>
--assign <name>` opens a thread assigned to them
([integrations.md](integrations.md#reply-open-a-thread-set-a-threads-status)),
by the id in Linear and by the key, the login, in GitHub; a hook
delivery whose author uniquely matches bills that user's login when a
herdr group and a route for the made bead use that login, else the
operator's, since a bystander needs no subscription; and, in a source
that hears by identity (Linear, Slack), it names who spoke: a delivery
from their id is kept with them as its speaker, one id naming one speaker
([integrations.md](integrations.md#what-the-service-sends-in)), and a
subscription there may filter `speaker=<name>`. A sender who is a crew member
of a linked yard is mapped on the link, never to a user
(`yardr peer identity`, [peers.md](peers.md#a-sender-who-is-a-crew-member-of-the-other-yard)). An author linked to
more than one user stays the operator's because the yard cannot tell whose
login to bill. A user is still reached by a subscription of scope `all`
alone. Anyone who may write to the yard sets it, as a crew member's.

**Who is an operator.** Each user has a role, `operator` or `member`. An
operator approves through a door (footplate, the web view, a
[device](#devices)): run asks, setup asks, secrets, and reading the grants.
An operator administers the yard at the shell: users, depots, sources and
their grants, settings, peers, hooks. A member is a party of the yard and
no more. There may be several operators, and every check of who may do
what reads the role, so an approval waits on none of them in particular.
`yardr user list` shows each user's role, and `yardr user show` says which
user the setting names.

**An ask is answered only by whom it was put to.** A plain ask has one
addressee (`--to`), and the store takes its answer only from them or an
operator; anyone else's is refused, naming the addressee. Run asks keep
their own rule for approving — any operator approves one, whoever of them it
was put to — but a run, secret, setup or depot-push ask is put aside or
declined by its addressee or an operator, as a plain ask is answered. A stage
ask is its members' to answer
([chat.md](chat.md#questions-for-you)). An operator reads and acknowledges
any user's inbox ([below](#the-inbox)); a member reads only their own.
Only the user reads their queue of decisions
([`UserAsks`](api.md#userasks)), and an operator any user's; on the socket
the reader is the call's `actor`, as for the inbox.

`yardr user set <user> role=operator` or `role=member` is an operator's to
give: typed at the shell, or approved on a card (`yardr bead ask --run "user
set <user> role=operator"`, [chat.md](chat.md#questions-for-you)), which the
yard weighs strong (`trust`) whichever way the role goes. A teammate invite may also create an operator with
`--role operator`, named on its approval card. Nobody sets their own role.
The yard refuses to make a member of, or to remove, its last operator: with
none, it would take every change from anyone who is no agent of its own.

**The `operator` setting names one operator**, the one an ask with no
`--to` goes to, and whose login and inbox the yard uses where it needs one
person's (a failed install is told to them, a crew member's login is theirs
unless `login_for` says otherwise). `yardr init` and the onboarding add
the user it names, and `yardr settings set operator <name>` adds the user
of a new name. Naming a user there makes them an operator, and that user
is neither made a member nor removed until the setting names another. A
yard from before users gets its operator's user when its server next
starts (event `user_added` by `yardr`). A yard from before roles makes the
user its setting names its first operator, and every other user a member,
when its server next starts.

**Only an operator adds and removes users**, under the check `yardr
crew add` uses for what a crew member may do: the command's actor is a
user whose role is `operator`. Removing a user removes their devices, and
the event `user_removed` names them (`devices`), and what carried their own
login (`login`: `groups`, `routes`, `crew`, and the `env_files` that stay).
A crew member that wants a user added or removed proposes it with a run ask
([chat.md](chat.md#questions-for-you)), which an operator approves.

**Teammates are invited and removed by card, not at a terminal.** There
is no `yardr user invite` or `yardr user uninvite` to type: each is one
run ask an operator approves, and approval does the yard user, the
`web_jwt_users` mapping and the report to the edge as one operation.
A crew member proposes one as any other run ask:

```sh
yardr bead ask <id> "Invite Eve?" --context "she works with us daily" \
    --run "user invite eve --email eve@example.net"
yardr bead ask <id> "Remove Eve?" --context "she has moved on" \
    --run "user uninvite eve"
```

`user add` alone makes an inbox and admits no one, so a person is brought
in by the invite card, and the operator's approval is the whole invite: no
request to the people who make yardr is needed.

An invite makes the user a member, or an operator with `--role
operator` (the card then says the user can approve and administer this
yard), maps the invited address to them in `web_jwt_users`, and only
then reports the invite to the edge this yard is connected to. The
card refuses, before anything is written, a name a user or crew member
already holds, an address the mapping already maps to anyone (an
address is never re-pointed to a new user), and a yard connected to no
edge, which would have nowhere to report. Until the report lands the
card reads as not yet at the edge and nobody is admitted; if the link
is down the user and the mapping stand and the yard retries the report
until it lands. An uninvite refuses anyone the mapping does not name:
that is a plain user, removed with `yardr user rm` instead. Its
approval reports the removal to the edge first; once that lands, the
user, their devices and the mapping entry go in one write. If the edge
cannot be reached the user and the mapping still go (from then on the
member acts as nobody here) and the report is retried until it lands,
a later removal the edge no longer holds counting as landed, not as
an error. A signed refusal that waiting cannot cure (the report naming
another yard, a membership already standing, an unverified address)
fails the card with the edge's own words. Once the mapping stands, the
teammate signs in as themselves; a device of theirs is paired by a QR
the yard's operator opens on the yard's own screen, as the web
pairing page now tells them (see [Devices](#devices)). The yard
side here reports through the edge's signed member door at `/member/add`
and `/member/remove`; the connected edge must support those reports.

A plain user already under the name goes first (`yardr user rm <name>`,
also a run ask); where the yard has no edge, a person comes in by `yardr
user add` and a device pairing ([Devices](#devices)). A new address gets a
set-password mail from the edge; an address with a verified account gets
none, so whoever invited the person tells them to sign in, and the crew
member says so when it reports the invite.

**A user with a login of their own.** A person who builds on their own seat
of a Claude plan (a Team or Enterprise seat, say) needs more than a
user: a folder their login lands in, a group that runs on it, routes that send
their beads to that group, a conversation with the yardmaster on that login,
and a sign-in they finish themselves. One command makes all of it, in that
order, and says what it made:

```
$ yardr user add ada --login claude
user ada added
  login folder ~/.yardr/logins/ada (0700), env file ~/.yardr/groups/ada.env (0600)
  group ada-builders (a copy of builders: kind claude, login_for ada, limit 1)
  route x0qbbmkrr69f79ff30ds: new, for ada's beads -> ada-builders
  yardmaster serves ada (env_for_ada)
  ada has no device yet, and reads the page and gives the code at one: yardr user device pair ada <device> --url <the address the yard is reached at>
  the page to sign in on is mailed to ada
  the code ada pastes back is ask 1 on yd-7m2k; the sign-in waits for it up to 10m0s
  ada is signed in as ada@example.com, and one call to Claude Code answered (the login is in ~/.yardr/groups/ada.env)
  ada's conversation with yardmaster starts again on the login
```

The page to sign in on is mailed to the person, and the code it shows comes
back as an ask of their own, which goes to the waiting login and is kept
nowhere. They read the mail and give the code in a [device](#devices) (footplate,
or the web view), so a new user is paired one while the sign-in waits, which
takes up to a quarter of an hour. The sign-in ends with one short call to
Claude Code on their seat, which proves the login answers, and with their
conversation with the yardmaster started again on it: it started on a folder
with no login in it, and the operator is asked nothing about it meanwhile. One
that did not finish, or whose call did not answer, ends with how, and `yardr
user login <name>` opens it again. [groups.md](groups.md#a-person-with-a-login-of-their-own)
has what each step is by hand, and what the group is a copy of.

`yardr user rm <name>` takes it back in the same write that removes the user:
the group whose `login_for` is the user with its routes, every route for their
beads, and the user from the yardmaster's `for=` and `env_for_<name>`. It
leaves the login folder and the env file on disk, with the transcripts in
them, and says where they are: no command erases them yet. A user added again
under the name finds them and is refused until they are moved away, since a
name is reusable and the first's login must not be the second's.

**One name, one party.** Crew members and users share their names, because
mail to a name reaches one inbox: `user add` refuses a crew member's name,
`crew add` a user's, and `settings set operator` a crew member's. A yard
that already had a crew member named as its operator keeps the crew member and
gets no user; mail to the name is the crew member's, and `yardr doctor` notes
it with what to rename.

## Who is acting

A command acts as a user when its actor is a user's name: `--actor`, else
`$YARDR_ACTOR`, else the login name, as for every command. yardr does not
authenticate anyone: whoever can run yardr on the yard's machine can name
any actor. A grant made as a user (`yardr source grant … --as <user>`) is
the same line, not a wall: only that user may give it, and the refusal
catches a mistake, not a terminal that claims their name.

An agent's command is known by more than its actor: the yard tells the
pane of a bead's session, and a crew member's, by the process that called
it, and takes the pane's actor whatever the command says. What a bead's
agent may call at all is the class of each call ([api.md](api.md#who-may-make-a-call)):
it notes, asks and works its bead, and does not change groups, routes,
flows or crew. That too is a line, not a wall: every agent runs as the
OS user the yard runs as, and one that starts a process outside its
pane's process tree is believed about who it is.

When the actor is the login name (neither `--actor` nor `$YARDR_ACTOR`
given) and it is neither a user nor a crew member, a command that acts as
one (`inbox`, `mail`, `reply`, `chat`) refuses in one line: who acts and
where that came from, the yard's users, and both ways to act as one:

```
$ USER=kloni yardr inbox
yardr: kloni (from $USER) is neither a user nor a crew member of this yard; its users are konrad. Act as one: --actor konrad or export YARDR_ACTOR=konrad
```

The onboarding form's "Your name" is therefore who its terminal acts as: it
starts at that name and takes no other, since the operator under a name
nobody acts as would leave the person who made the yard no user of it. To
be the operator under another name, set `YARDR_ACTOR` to it before `yardr`.

## The yardmaster for the operator

A yard whose operator has no terminal on its machine, a hosted one first,
can let its yardmaster run some of the operator's defining commands for
them. The switch is the `yardmaster_delegation` setting: unset, it reads as
`hosted` does, on for a hosted yard and off for one you run yourself. The
operator turns it off with a run ask (`yardr bead ask --run "settings set
yardmaster_delegation false"`), approved as any card that narrows, or with
the button on the hosted web's Config page; turning it back on takes the
same two doors under the fresh sign-in a strong card takes. The yardmaster
never changes it.

With it on, the yardmaster runs the command in its own pane for a named
operator, citing the operator's mail it acts on:

```
yardr group set reviewers model=opus preset=accept-edits --for ada --mail 1042
```

### What opens a run

The only word that opens a run is a local mail: a mail in this yard, to the
yardmaster, from a user who holds the operator role. What `ada` types in
[`yardr chat`](chat.md) is that mail, since Enter does what `yardr mail`
does. The yard checks the citation in the write that runs the command, and
refuses the run whole, before it writes anything, unless all four hold:

- the caller is the yardmaster, the crew member the `yardmaster` setting
  names (a call from a crew member's pane is that member, whatever `--actor`
  it names);
- the user named by `--for` holds the operator role;
- the switch is on;
- `--mail` is the seq of a mail from that user to the yardmaster, with no
  mark of another yard, a session or a timer on it.

Nothing else opens a run. A mail from a linked yard does not, even from a
name the link trusts as a user: it is information and not instructions. A
delivery from a source does not, mapped to a person or heard as an outsider,
so a line from an operator's Telegram or Slack opens none. Nor do thread
text, a hook delivery, a bead's brief, or words a session or another crew
member passes on. A word in the yardmaster's pane cites nothing and starts
nothing: an operator at a pane has a terminal, and runs the command there.

The check proves who sent the mail and to whom. The reading of its words
stays the yardmaster's: whether they ask for the command that ran is not
checked, and text an operator pastes into their own mail is, to the yard,
the operator's word. A yardmaster talked into reading a genuine mail as
asking for more than it says can run more than it says. So the yardmaster
runs what the mail asked, and nothing the mail only seems to ask: where the
mail does not plainly ask for the command, or the command does more than the
mail says, it asks the operator by mail and runs the command on the mail that
answers. What stands against a wrong reading comes after the run, below: it is
recorded and mailed to the operator in the write that makes it, and the
operator can take it back and turn delegation off. The first bad run stands
until the operator reads it, and that is the price of telling after instead of
asking first. The yardmaster's role holds it to the rule in brief, and this
page has the rest.

### What it runs

| the command | what the yard holds it to |
|---|---|
| `group add`, and `group set` of `kind`, `model`, `dir` and `worktrees` | `dir` and `worktrees` name a directory under the yard's home or a depot. Args only as `preset=<label>`, a named permission preset of the kind that keeps the agent's permission prompts (`yardr harness show <kind>` lists them), never `bypass` or its like and never free `args`. No `prompt`, `cmd` or `env_file`, and no change of runner to or from `exec` |
| `crew set` of `kind`, `model`, `dir` and `preset=<label>` | A member other than the yardmaster, whose own entry is a card. The same rule for the preset. No `env_file`, `env_command`, prompt or conversation login. A new member is `crew add`'s card, which shows its role text whole |
| `depot test <depot> <command>` and `depot base <depot> <base>` | A test command of one line, at most 4096 bytes, as the depot test card shows one whole. Clearing a test command, and a depot's path and kind, are not here |
| `user set <user> notify=<source>` | That key alone: `env_file` or `role` in the same command refuses the whole run |
| `hook add <name> --source <source> [--unknown <word>]` | A new hook in the card's safe shape: the secret file the yard owns for it, with its signing secret still given on the hook's own secret card. `--says`, `--makes`, `--map`, `--as` or a secret file of its own refuse the run, and so does the name of a hook that stands |
| `pack apply` | A pack yardr embeds, by name, or a pack of the packs repository, by its URL and `--sha256` ([below](#packs-run-for-the-operator)), with `--depot` and its mappings and no other flag, past the pack card's bounds. A plan that still asks after the mappings is refused whole, and no answer path is built for it |

A prompt, a group's `cmd`, a preset that skips permission prompts and every
setting of the yardmaster's own entry are not runs. They rewrite what every
later session does, so the yardmaster proposes them in a run ask (`group
set`, `crew set`; [groups.md](groups.md), [crew.md](crew.md)) and they run
when the operator approves. A group's `effort` is held the same way, since it is
the operator's as the `args` it was a flag of are: the yardmaster proposes
`group set <name> effort=<level>` in a run ask, whose card weighs nothing, and
does not run it.

On a delegated run the approval is the operator's own cited mail, so the run
skips the card the same command carries today: the code card of `group add`,
the trust card of a safe-shape `hook add`, the code card of `depot test` and
the card of `depot base`. Those cards stay, weighed as before, for the same
command proposed by anyone else, and for an operator who would rather
approve first.

### What stays with the operator

Everything not in the table above is still what it was. Where a hosted
operator has no terminal, each row names the door they have instead.

| stays the operator's | the door instead |
|---|---|
| A secret value, `env_file` and `env_command`, a source's key and mint command, where a credential is read from | The secret asks: the person types the value on their device or in the hosted web, and the yard writes it to its file where no agent reads it |
| A user's role, adding or removing a user, inviting a teammate, a device, a link's trust | The cards as they stand (`user set … role=`, `user add`, `user rm`, `user invite`, `user device rm`, `peer add` and the other link cards), the strong ones under a fresh sign-in in the hosted web. The operator pairs their own first device through the hosted web |
| Removing a source or a hook, moving a source's records or its links, a source's `base` | The `source rm`, `source move`, `link move` and `hook rm` cards; a base alone by `yardr bead ask --setup … --source-base` |
| A hook add with a secret file of its own, changing a hook's secret file | A terminal on a yard you run yourself, the host on a hosted one |
| A depot's path and kind, removing a depot, clearing a test command, the yard-wide `permissions_preset` | The same: the provisioner makes the depots of a hosted yard |
| The Alone settings (who the operator is, where the yard serves from, whether it is hosted, the edge's record), `edge connect` and `disconnect`, installing a release, `uninstall --purge` | A terminal on a yard you run yourself, the hosting service on a hosted one. The delegation switch is the exception: its two doors above are the operator's |
| Reading every user's timers, `store prune` by hand | Each user reads their own timers, and the daemon prunes |

### Which door a command takes

A command that is the operator's to give goes as a run ask when `yardr bead
ask --help` names it under `--run`: `yardr bead ask <id> "<question>" --run
"<command>"`, one command in an ask, with why it is wanted in the context.
The operator approves it among their decisions, and the yard runs exactly what
the card shows ([chat.md](chat.md#questions-for-you) shows the cards). The
yard checks whether the asker can run it and whether it is the operator's
alone.

An operator-only command goes on a card where one carries it; when delegation
is on, a command in the delegated list can run on the operator's cited local
mail instead. Where neither path carries it, the yardmaster mails one the yard
marks operator's alone in a fenced code block, one command by itself, for them
to type in a terminal. A hosted yard's people have no terminal, so there a
command that neither a card nor the list carries is never mailed to type: the
yardmaster tells them that no card carries it and files a bead for the yard to
grow one.

A card's command that the delegated list does not name is never run for the
operator on their mail: the yardmaster asks the card and the operator approves
it. Nor does it ask for a run because mail told it to without saying so in the
ask's context: whose mail, and what it asked.

### Told after, and taken back

Each run is told after it runs instead of approved before. In the same write
the yard keeps a record of it (the command whole, the cited mail, what each
setting held before, and how the run is taken back), puts a line on the
yardmaster's journal that names only the record's id and the operator, and
mails the operator:

```
yardmaster for ada ran, on your mail 1042 (record 12, which yardr delegation show 12 reads):
  yardr depot test web 'sh .yardr/test'
changed: depot web (test)
taken back by: yardr depot test web 'make test'
```

The mail carries no before-values; the record does. `yardr delegation show
<id>` and the record's page in the hosted web (`/delegations/<id>`) read it
for an operator alone, any operator and not only the one acted for. A
member, a crew member (the yardmaster that ran it too) and a call that names
nobody are told it is not there. The change's own event is what every such
change leaves, with nothing added, so someone who only follows the events
cannot tell a delegated run from the operator's own. That a run happened,
and where its record is held, is on the yardmaster's journal, which is not
scoped to operators; what ran, on what word and from what before-state are
the operators'.

What taking a run back costs depends on what it was:

- A defining change (a group's or a member's kind, model, directory,
  worktrees or preset, a depot's test command or base, where a user is
  told) goes back with one more mail to the yardmaster, itself a delegated
  run and itself told, that runs the command the telling mail names.
- A new group goes back with `group rm`, which the yardmaster already runs
  beside the operator.
- A new hook goes back only on its removal card (`hook rm`), one tap and not
  one word. An operator who wants hook adds undone by word alone should not
  delegate them.
- A pack has no one command back. A pack of the packs repository goes back by
  applying it again pinned to the digest that stood before it, which the
  record and the mail name, as far as the repository still serves those
  bytes; any other, by hand from the record's diffs, each updated object set
  back to the text its diff shows and each created one removed by name. The
  operator, who alone can read the record, names the objects and the digest
  in the mail that asks for it.

### Packs run for the operator

A delegated pack apply's record is written `started` before its first
object and rewritten `done` or `failed` at the end: it holds the pack's
digest, its objects by name, each updated object's diff and the complete
before-state (kept in one blob, whatever the record's size, which the
record's page and `yardr delegation show <id>` read). In the JSON of both
the record's `objects` are the names, and `plan` carries each object whole,
with its `action`, `diff`, `before` and `had`. A record that would
pass the yard's blob cap refuses before anything writes. The operator is
mailed when the run starts and how it ended; a failure names the objects
written, the partial import stays, and applying again finishes it. A
record left `started` reads as unfinished, never as done. No one command
takes a pack apply back: the operator sets each updated object back from
its before-state, or re-applies the pack that stood before. The store
takes the record's plan from its own reading of the pack the binary
embeds, never from the caller: a plan that still asks after the mappings
refuses there too, the record names the reading's objects exactly, each
write under it (a group, crew member, flow, pointer, route, source, hook
or the import's own record among them) is held to the object and the
values that reading names for it, and a grant passes only where the
run's mappings answered it. A command applying an embedded pack from a
different build than the server's, as in the seconds between an install
and the server's restart, is refused at the start with nothing written,
both builds named: apply again once they are one. A record left `started`
carries no write after an hour.

A pack of the packs repository is applied the same way, named by its URL and
pinned: `yardr pack apply https://raw.githubusercontent.com/yardr-dev/yardr-packs/main/<name>/
--sha256 <digest> --depot <depot> --for ada --mail 1042`. The URL is that
repository's and nowhere else's (not another host, ref or path, and no
directory), and `--sha256` is the digest `yardr pack check` prints, which the
operator's mail names or the yardmaster reads from the repository; no other
flag comes with them. The yard fetches the pack itself, before the store's
write and never in it, and holds it to the digest before it reads anything
of it, so what the store takes the run's objects and values from, and what
is installed, is those bytes and not a second answer of the server. Its
record also names the URL and the digest that stood before it, the latest
delegated run of that pack into the depot that finished from the repository,
and mails both. The import it leaves names that URL as the source a later apply
by name reads the pack from again, and the store refuses any other source and
any credential there. Re-applying the pack pinned to that digest takes the run
back, as far as the repository still serves those bytes: its `main` moves,
so after its next commit that digest is refused with nothing changed, and the
record's before-state is the way back by hand. A commit's URL is refused on
purpose: GitHub serves a fork's commit under its parent's path, so only the
branch the owner writes stands for the owner.

A pack of the packs repository has no card to read it on. An operator who
wants to read every line of one before it runs reads the repository first, or
does not delegate packs. [packs.md](packs.md) says where this road stands
beside the card.

## Mail

`yardr mail <user> <text|->` puts a message in the user's inbox: one mail,
or numbered mails for a text over one mail's size (4 KB), as
[`yardr chat`](chat.md#a-long-text) sends a long text. A crew member's
session sends it, and so does a user. **An agent working
a bead is refused**: what it has to say to a person goes on its bead
(`yardr bead note`), and a question ends its stage with the `question`
outcome. That is a rule of the `mail` command, not of the user's record.

### Answering, and what a mail is for

A crew member answers mail with `yardr reply <seq> <text|->`, the number
its inbox line starts with: the reply is mail to that mail's sender, or,
when the sender is a bead's session, a note on that bead.
`yardr mail <user> -` is for writing first. A person who talks to a crew
member in its pane is answered in the pane, and that conversation is not
mailed as well. Mail is what `yardr chat` shows a person, and all it shows.

An answer goes once, where the person wrote. After a reply by mail, a
question on a bead (`yardr bead ask`) or a reply in a thread, the crew
member does not write it again in its pane as a summary: who wrote by mail
does not read the pane, and who watches the pane saw the command that sent
it. The pane still shows what the crew member is doing.

Nothing nudges a person inside the yard, so what waits for their decision
goes as an ask (`yardr bead ask`), which stands among their decisions until
it is answered. A person who wants to be told of events subscribes to them
(`yardr subscribe <user> yardr --done`) and finds them among the
notifications. Mail is for talk: a decision that is theirs, a fault they
should know, an answer. It does not say what a notification says.

What a person is to run, paste or type goes in a fenced code block of its
own: three backticks on a line before and after, one block for each thing to
copy, and nothing in the block that is not to be copied: no prompt sign, no
comment, no placeholder left for them to edit without saying so in the text
above. Everything else stays plain sentences. A mail holds 4 KB, and a block
counts.

### A page in a note

`yardr bead note <id> <text> --page <fragment> --title <title>` attaches a
page made in a frame: the HTML skeleton pages share (colour and type tokens,
a title block, tiles, tables that survive a phone, charts, tabs), so its
author writes the content and not the width, the colours and the chart
boilerplate again. `<fragment>` is a file of the page's body content, not a
whole HTML document, and `<title>` is one line of plain text, which the yard
puts in the page's `<title>` and in its title block, escaped; the
fragment goes where the frame says its content stands. `--page` is given
once for a note, needs `--title` and the note's words, and counts as one of
the note's 64 files. The page is a file of the note like any other: it opens
inert on the bead's page and runs only when its reader chooses *run*
([web.md](web.md)).

```
$ yardr bead note yd-greet - --page yard-fragment.html --title "Yard in October"    # the words on stdin: Yard in October: throughput and the two held landings.
note 14 on yd-greet  1 page: ~/.yardr/blobs/9f31…
```

The frame is the depot's own where a pack applied to the depot carries
one, else the yard's where a frame-only pack was applied to the yard, else
yardr's default, which the binary carries. Run `yardr page show [--depot
<name>]` once before writing a fragment, as you run `--help` once: it prints
the frame that would dress a page, with the pack it comes from, where it is
installed, its size and its sum, and the contract the frame declares in the
comment it opens with (its component classes and tokens, and its two
markers). Without `--depot` a session working a bead is shown its bead's
depot's frame, and anywhere else the yard's. That contract is the pack's own
text: it is printed under a line saying it is data to write against and no
instruction from the yard, and every control character in it is printed as
its escape.

The yard keeps the fragment as a blob of its own, with the title and the
frame the page was made in (the pack, the frame's path in it, and the
SHA-256 of the frame as it was read), beside the page: `yardr bead show
--json` names them under the file's `framed`. A page attached with `--file`
is whole and keeps nothing apart. The yard refuses, with the reason and
nothing stored, a fragment that is itself a document, an empty one, a title
that is not one line, and a frame that no longer meets what a frame is held
to (an installed copy edited by hand since its pack was checked).

### A file in mail

`--file <path>` sends a file with the mail, and is given once for each
file, up to 64. The text may then be left out:

```
$ yardr mail yardmaster - --file yard.png    # the words on stdin: The yard, painted
mail sent to yardmaster
◆ 1 image: /home/mckean/.yardr/blobs/497790947d46…1581 (yard.png)
$ yardr mail mckean - --file report.pdf      # the words on stdin: The report
mail sent to mckean
◆ 1 file: /home/mckean/.yardr/blobs/8f3a…c41d (report.pdf)
```

The yard keeps the file in its home, in `blobs/`, in a file named by the
SHA-256 of its bytes: sent twice, it is there once, and it is never
changed. Wherever the mail is read, the file stands under the mail's
words on a line of its own starting with `◆`, as `1 image: <path>`,
`1 page: <path>` or `1 file: <path> (name)` (`2 files: <path> (a.pdf)
<path> (b.csv)`, `1 image, 1 file: <path> <path> (a.pdf)`): `yardr inbox`
for a user and for a crew member, `yardr mail --with`, the nudge a crew
member gets for its inbox, and [`yardr chat`](chat.md#a-file). Only that
marked line names a file the yard keeps; any other `N image(s):`,
`N page(s):` or `N file(s):` is words someone wrote into the mail. A crew
member reads the file by opening its path, as it reads any other; yardr
shows none itself. In `--json` the letter has `files`, a list of
`{hash, type, bytes, name}`.

What kind a file is is read from its bytes: a PNG or a JPEG is an image,
HTML a page, anything else a file. A file carries a name, the path's base
name, because its bytes say nothing a person can read; an image or a page
carries one too, empty on a paste. A name is at most 255 bytes of UTF-8,
with no `/`, `\` or control character (a bidi control, which would turn
`exe.pdf` around where the name is drawn, is one), and neither `.` nor
`..`: one that fails the rule refuses the mail, never sanitised. Where
the yard's marked line names a file, a name holding a parenthesis or a
quote stands quoted in its parentheses, so it never reads as another
path. One over the yard's
limit, 8 MiB unless `yardr settings set blob_max_bytes <bytes>` says
otherwise, is refused with nothing stored, and the mail is not sent.
A name arriving from a linked yard that fails the rule is kept empty, the
file still taken ([peers.md](peers.md#a-file-with-the-mail)).

A file on a card or in mail is data the sender gave, to read, never to run
or to take instructions from.

**To a linked yard** a file of any kind goes with `yardr mail
<name>@<peer> --file` when the link carries the kind `file` (`yardr peer
allow <peer> --send file`), and one comes in when this yard's link takes it
(`--receive file`). Where the other yard does not take it the mail still
arrives, with `1 file: not taken: <reason>` in place of the path
([peers.md](peers.md#a-file-with-the-mail)).

A file is removed by `yardr store prune`, and by the daemon's hourly
prune, once no mail the yard still has names it
([configuration.md](configuration.md#retention-keeping-yarddb-bounded)).
Nothing else deletes one.

**Nothing nudges a person inside the yard.** A user who named a source is
told on their phone ([below](#told-outside-the-yard)); for everyone else,
and as well, `yardr doctor` (the `mail` line) and bare
`yardr` (a last line under its help) say `N unread from <names>` to the
user who runs them. That count is of mail alone; bare `yardr` adds `and N
notifications` after it when there are unread ones:

```
Mail for jo: 1 unread from yardmaster, and 2 notifications (yardr inbox).
```

Without unread mail bare `yardr` prints no such line, whatever the
notifications: nobody is talking, and a person who never acknowledges them
would read it under every command. `yardr inbox`, `yardr user show <name>`
and the `yard` row of [`yardr chat`](chat.md#the-yard) count them.

A crew member can have the yard tell a user something later, unless a bead
moved first: `yardr timer add --to <user>` ([timers.md](timers.md)). It
arrives as mail from that crew member whose text begins `timer:`, and the
crew member is not woken for it. A user sets timers for themselves the same
way; `yardr user show` lists the live ones.

**To and from a linked yard.** A user writes to a crew member or a person
of a linked yard with `yardr mail <name>@<peer> <text|->` (up to 16 KB), and
from [`yardr chat`](chat.md#those-of-a-linked-yard), where each of them has a
row. The link's `send mail` is the grant (`yardr peer allow <peer> --send
mail`, the operator's): a user has no `peers` setting, that one is a crew
member's. The mail is queued and the daemon delivers it; what the other yard
does not take comes back to the user's inbox as mail from `yard` that says
why. An agent working a bead is refused here too.

Whom a user can write to is the roster each linked yard last said
([peers.md](peers.md#the-roster-whom-a-peer-may-see-and-write-to)), and
`yardr user list` prints it under this yard's users:

```text
NAME  ROLE              UNREAD
ada   member            0
jo    operator (asked)  1

OF A LINKED YARD  IS      MAIL  ROLE
mckean@north      person  yes   operator
yardmaster@north  crew    yes   Yardmaster: shapes the beads
```

`ROLE` is `operator` or `member`; `(asked)` marks the operator the
`operator` setting names, whom an ask with no `--to` goes to.

`MAIL` is whether mail from this yard reaches them: `no (yardr peer allow
north --send mail)` when this yard's link sends none, `no (north takes none
from this yard)` when theirs takes none. A peer that exposes nobody adds no
rows, and a yard none of whose peers do prints its users alone. It is `user
list` that has them and not `crew list`: that one is the table of this
yard's sessions (state, pane, runner), none of which a roster says, and
this is the list of whom one writes to. `--json` stays the list of this
yard's users; the roster is in `yardr peer list --json` (`said.roster`).

The answer of someone there lands in the user's inbox as mail from
`<name>@<peer>` once this yard's link takes mail from that yard and exposes
the user to it (`yardr peer allow <peer> --receive mail --expose <user>`),
and is one conversation with what the user wrote: `yardr mail --with
<name>@<peer>`, and the row in `yardr chat`
([peers.md](peers.md#mail-between-crew-members)).

## The inbox

A user's inbox has two parts, and `yardr inbox`, run as a user with neither
`--crew` nor `$YARDR_CREW` naming a crew member, shows both, each
under its heading with its unread count:

- **Mail**: what a crew member or another user wrote to this user, and to
  nobody else. Somebody is talking, and can be answered.
- **Notifications**: what the yard reports of the events the user
  subscribed to
  ([subscriptions.md](subscriptions.md#users-as-subscribers)), one line
  each. Nobody sent them, so there is nobody to answer.

Each part lists its unread, oldest first, then the latest read, the newest
of them first, marked `(read)`; `-n` bounds how many in each. `--mail` and
`--notifications` show one part.

**Only the user reads their inbox**, and only they acknowledge it: anyone
else's read or ack is refused, naming the user. An operator reads and
acknowledges any user's. So `yardr user list` shows another user's unread
count as `-` to anyone but an operator, and `yardr user show <name>` of
another user says the inbox is not shown. Over the network a device reads
as its user ([api.md](api.md#over-the-network)); on the socket the reader
is the call's `actor`, taken at its word like every actor.

```
Mail: nothing unread; the latest read:
    50 10-06 01:02  mail from yardmaster (read): One question on web-hk7z when you have a minute.
    39 10-06 01:00  mail from yardmaster (read): Shall I add a reviewer group?

Notifications: 2 unread
    51 10-06 01:02  web-hk7z advanced (all) by yardmaster from=new to=decide on "Which port for the hook listener"
    53 10-06 01:02  web-3cps closed (done) by yardmaster reason=merged 1a2b3c4 on "Chat: a row for the yard"
    43 10-06 01:00  (read) web-wh4c closed (done) by yardmaster reason=merged 1234567 on "Narrow one"
```

A part with nothing in it is `Mail: none` or `Notifications: none`. The
first column is the seq, the same in both parts: it orders a mail and a
notification against each other.

**Each part is acknowledged apart.** `--ack <seq>` marks read what is
through `<seq>`: with `--mail` the mail, with `--notifications` the
notifications, with neither both. Name the last seq you have read, so what
arrived since stays unread; the command says how much newer stays unread.
`--ack` with no seq is refused, naming the newest seq and the command to
ack through it. Acknowledging the mail leaves the
notifications unread, and the other way round: a user has two positions,
one a part. So a stream of notifications nobody reads does not hold back
what counts as read mail, and reading through the notifications marks no
mail read.

A yard from before notifications kept one position for the whole inbox.
Its store is migrated when the server next opens it (schema 4): what a
user had acknowledged is acknowledged in both parts.

The notifications are bounded: the newest 500 are kept and the oldest
dropped, read or not. Mail is not.

## Told outside the yard

A user names a source that can tell a person
([integrations.md](integrations.md#tell-a-user-outside-the-yard)), and from
then on what lands in their inbox is sent through it: to a phone, through
the `telegram` or `pushover` pack ([pushover.md](pushover.md) sets Pushover
up from nothing).

```sh
yardr user key ada pushover                 # ada's own address in it, typed unseen: ~/.yardr/users/ada.env
yardr user set ada notify=pushover
yardr source check pushover --user ada      # one real test message
yardr user show ada                         # notify, the env file, the parts, and how the last message ended
yardr user set ada tell=notifications       # of the notifications only; tell= is all three parts again
yardr user set ada notify=                  # nowhere; what was still to be told is dropped
```

`yardr user list` shows where each user is told, and of which parts when
they name some. `notify` is the source.
`env_file` is a file of `KEY=VALUE` lines that sets
the variable the source's file names for a person's address
(`notify.address.env`; for Pushover `PUSHOVER_USER`, the person's own user
key). It is held to the rule of every env file: a regular file of mode 0600
or stricter, read at each send. The store keeps the path, never what is in
it, and a file that is open to others or does not set the name is refused
when it is set. The source's own credential is the yard's and in another
file (`yardr source key <source>`): the application's token is one thing,
a person's address another.

**Nobody writes that file by hand.** `yardr user key <user> <source>`
asks for the address and shows nothing as it is typed (a script gives it as
one `NAME=value` line on standard input; it is never an argument), and the
yard's server writes it: into the user's `env_file`, with every other line
kept, or with none set into `~/.yardr/users/<user>.env`, mode 0600, which
is then the user's `env_file`. It is refused for a source with no `notify`
key. An agent that needs the address asks for it with a secret ask,
`yardr bead ask <id> "<question>" --secret <source> --for-user <user>`,
which stands among that user's decisions (or the operator's, with `--to`)
and is answered where the field shows dots: in footplate, in `yardr chat`
with `ctrl+o`, or with `yardr bead answer <id> <ask> --secret`
([chat.md](chat.md#questions-for-you)). Giving the address does not set
`notify`: the card and the answer name `yardr user set <user>
notify=<source>`, which stays the person's to run, or the yardmaster's for an
operator on their mail ([below](#the-yardmaster-for-the-operator)). The value
is in that file and nowhere else: no note, event, mail or log has it. A file you keep
yourself is named with `env_file=<file>` as before.

**Who may.** The user themselves and an operator, for the two settings
and for giving the address, by command or on a card. It is refused from the
pane of an agent, whoever the command says it is, and from a crew member:
whoever sets these decides where a person's inbox is sent. The one crew
member who sets `notify` is the yardmaster, in a delegated run for an
operator on that operator's cited mail, and never `env_file`
([below](#the-yardmaster-for-the-operator)). Like
every such check in yardr it is a line, not a wall: every agent runs as the
one OS user ([Who is acting](#who-is-acting)).

**What is told.** Mail to the user, their own too, so also a timer they
set for themselves; and the notifications of their subscriptions, which
never include what the user did themselves
([subscriptions.md](subscriptions.md#users-as-subscribers)). And every
**ask for the user** (`yardr bead ask ... --to ada`), which is in no inbox
but in their queue of decisions: a question is what waits for a person, so
it is told without a subscription. The yard that puts the ask in the
person's queue tells it, for its own asks and for those
[another yard asked](peers.md#questions-for-a-person-of-another-yard).
Nothing else: to be told of a kind of event, subscribe to it; not to be,
do not.

**Which parts.** Those are three parts, and `tell` says which of them the
source tells: a list of `notifications`, `mail` and `asks`, with commas
between. A user who never set it is told of all three, and `tell=` with
nothing after it is that again. Any other name is refused, with the three.

```sh
yardr subscribe ada yardr --done            # a notification when a bead is done
yardr user set ada tell=notifications       # and the phone has that, not each mail of an evening's talk
yardr user set ada tell=notifications,asks  # that, and what waits for her decision
```

The inbox and the queue of decisions are filled as ever: `tell` is only
about what leaves the yard. Nothing is queued for a part that is left out,
and what still waited to be told of it when it was left out is dropped. An
ask whose note a subscription made a notification of is told as that
notification where `asks` is left out and `notifications` is not. The name
is `tell` beside `notify`: `notify` is where, `tell` is what, and `told` is
taken, by the line of `yardr user list` and `show` that says where
(`"told"` in `--json`).

`tell` is the user's own to set, or the operator's for them, and never an
agent's. A crew member that thinks a person wants less or more on their
phone proposes it with a run ask ([chat.md](chat.md#questions-for-you)),
of that user or of the operator (`--to`), and the one asked approves it:

```sh
yardr bead ask <id> "Leave mail off your phone?" \
    --context "You said an evening's mail is too much there." \
    --run "user set ada tell=notifications,asks" --to ada
```

The card says in the yard's words of which parts the source tells them
now and of which it then would, or that no source tells them, in which
case the change tells them nothing until `notify` is set; that what still
waited to be told of a part that is dropped goes; and the command that
sets the parts that stand now, which takes it back. It is refused when it
is asked for a user the yard has not, a name that is no part, or the
parts the user is told of already, and the approval is refused when the
user's `tell` or `notify` was set in between. `notify` and `env_file` are
no part of a run ask: they decide where a person's inbox goes and with
which credential, and are typed by the user or the operator.

**What leaves the machine.** A fixed line that says who and what kind, and
the first words of the text: the
message goes to the service's servers and onto a lock screen. Of a part
the user's `tell` leaves out, nothing does.

| what | part (`tell`) | title | text |
|---|---|---|---|
| mail | `mail` | `mail from yardmaster` | the first 200 characters; `(with files)` for mail of files alone, which has no text |
| a fired timer | `mail` | `timer from mckean` | the first 200 characters (`timer: …`) |
| a notification of a bead | `notifications` | `web-hk7z advanced` | its line as the inbox has it: `web-hk7z advanced (all) by yardmaster from=new to=decide on "Which port for the hook listener"` |
| a notification of a hook delivery | `notifications` | `github hook` | its line, without the link it ends with |
| an ask for the user | `asks` | `yardmaster asks`; `yardmaster@north asks` for another yard's | the first 200 characters of the question; not its context, its options, its bead or the bead's title |
| several | those told | `5 new in your yardr inbox`; `5 new in yardr` with an ask among them | each kind once, with how many: `mail from yardmaster (4), web-hk7z advanced` |

No link is sent. The source cuts a title or a text to what its call takes
(a parameter's `max`).

A tell carries one picture at most, and only two kinds of tell carry
one: a mail's first image file, and the newest picture on the notes of
a bead the message says is done. Of several due at once the first
picture in order goes; the rest stay in the yard, read where the mail
or the bead is read. The `telegram` and `pushover` packs name a picture
command; a source whose file names none tells words alone, as ever. So
does one that does not take the picture (Telegram takes a photo of 10 MB
at most, and 10000 pixels in width and height together; Pushover an
attachment of 5,242,880 bytes at most, and leaves it to the sender to
make a larger one fit, so the pack's command refuses it) and one that is
too slow over it. The words are told next, not lost with the picture,
and the daemon's log says why (`user told without the picture`). A
picture has the 10 seconds a tell gets, and the words, if the picture
fails, have 10 seconds of their own.

**When.** The daemon sends, so it must be running. A thing is told once it
has been in the inbox for 20 seconds and is still unread: what was
acknowledged by then, by `yardr chat` showing it or `yardr inbox --ack <seq>`, is
not told. An ask is told once it has been in the queue for 20 seconds and is
still open: one that was answered, declined, put aside or taken back by
then is not told, and reading the inbox does not answer it. It is told once, and not
again when one put aside comes back. Where a subscription made a
notification of the same note, the two are one item, the ask. Everything
due for one user goes as **one message**, and a
message waits for a burst to end: it goes once nothing new came for 20
seconds, at the latest 60 seconds after its oldest item. At most **20
messages an hour** for one user; the twentieth says it is the last of the
hour, and what is over waits and goes as one when the hour has room.

**When the source fails.** The inbox is filled first, in the same write as
the mail or notification, and never waits for a source: with the source
down, misconfigured or removed, `yardr inbox` and `yardr chat` show
everything at once. A send that gets no answer, a 5xx, a 408 or a 429 is
tried again after 30 seconds, then with doubling waits, for up to **8
tries** and until the thing is **6 hours** old; then it is given up. Any
other 4xx is final at once, and so is a user or a source that is not set
up (a missing env file, one open to others, a name it does not set). Giving
up is the event `push_failed`, about no bead: the user, the source, the
reason (`not_set_up`, `refused`, `gave_up`, `expired`), the status, how
many of the inbox it covered. The address, the token, the title and the
text are in no event and no log, and nothing of a request is. A message
that was taken leaves no event.

**How it stands.** `yardr user show <name>` has `last told: <when> through
<source>, taken (status 200, 2 of the inbox in one message)`, or `failed
(…); next try <time>` while the daemon still tries, or `not sent: <reason>
(…)`. `yardr doctor` notes a user whose last message was not taken
([doctor.md](doctor.md)). A crew member hears of `push_failed` with `--on
push_failed`; a user cannot subscribe to it, so a failed message never
queues another.

None of the numbers above is a setting, and there is one source per user.

## Devices

A device is one program of one user on one machine or phone that calls the
yard from outside it: a window that is not on the yard's machine. The yard
gives it a token, and a call that carries the token is made as that user.
A yard needs nothing else for it, no identity service and no login.

**Where it calls.** On the yard's listener, the address of the setting
`hook_addr` ([configuration.md](configuration.md#settings)), under
`/user/`, with the token in `Authorization: Bearer <token>`. The paths, the
answers and what a client must do about TLS are in
[api.md](api.md#over-the-network). A yard without `hook_addr` listens
nowhere. There a token lets nothing in, unless the yard is connected to
[the edge](edge.md), whose link carries a device's calls with no listener;
a yard without a device answers 404 under `/user/`, over the link too.
Webhooks reach a yard that listens nowhere the same way: through the edge
they arrive with no listener at all.

```text
$ yardr user device add mckean phone
device phone of mckean. Its token, shown once:

  yardr-dev-7Kq2…<43 characters>

Give it to the device with this yard's address, the one the device will reach it at.
It calls as mckean until: yardr user device rm mckean phone

$ yardr user device list
USER    DEVICE  ADDED        LAST USED
mckean  airy    10-12 19:05  never
mckean  phone   10-12 19:02  10-12 19:40 from 100.64.0.7

$ yardr user device rm mckean phone
device phone of mckean removed: its token is refused from now on, and the streams it held are closed
```

**The token** is 32 random bytes after `yardr-dev-`, printed once, by `device
add` (with `--json` the field `token`), or given once to the device itself,
which called with a code ([below](#enrolled-by-a-code)). The store keeps its SHA-256 beside
the device's name, its user, who added it and when, and when and from which
address it was last used; it never keeps the token, and no command, event
or log has it. A token that is lost is not read again: remove the device
and add it anew. The yard does not know the address it is reached at from
outside, since a proxy or a tunnel of yours may stand in front, so `device
add --url <address>` only prints the address under the token, to hand both
over together.

**The name** is yours to choose (`phone`, `laptop`), of the characters a
user's name has, and one of a kind among that user's devices: a second
`phone` of one user is refused, and two users may each have one. `yardr user
device list <user>` lists one user's.

**A token does not end by itself.** It is refused once its device is
removed, or its user (`yardr user rm`). `LAST USED` is when a call last
carried it and the address that call came from, written at most once a
minute while the address stays the same; behind a proxy that is the proxy's
address. `yardr doctor` notes a device that has not been used for 30 days,
or was never used 30 days after it was added, with the command that removes
it ([doctor.md](doctor.md)); nothing is removed for you.

**Who may.** Only an operator adds a device, with `device add` or by
opening a pairing for it, under the check `yardr user
add` uses, and never from the pane of an agent, whoever the command says it
is, as for [`user set notify=`](#told-outside-the-yard): whoever adds a
device decides who may speak as a person. The user themselves does not add
one. An operator and the user whose it is remove one. Like every such
check in yardr it is a line, not a wall ([Who is acting](#who-is-acting)).

**What a device reads and writes.** The calls of
[api.md](api.md#the-calls), the event stream and the files of its user's
own mail, of notes and of kept hook deliveries
([api.md](api.md#over-the-network)), and nothing else the yard's socket
serves: no move of a bead is on the listener, whoever calls, and of the
settings only the telemetry switch. A device of an operator (a user whose
role is `operator`, any of them) has more calls, and no other device has
them ([api.md](api.md#for-the-operator-alone)). It gives the credential a
[secret ask](chat.md#questions-for-you) asks for, typed where the operator
sits and written by the yard into the source's file. It approves a
[run ask](chat.md#questions-for-you), whichever operator it was put to,
and the yard then runs the one command that ask holds, as that device's
operator. It approves a [setup
ask](chat.md#setting-up-a-packs-sources) with the preset pick and the
account together, and the yard then applies the pack, takes the account
and grants the preset picked, as the operator. It reads every grant of the
yard's sources, the whole of who may call what. It reads what `yardr
telemetry show` prints and turns the telemetry switch on or off, as the
terminal does. Approving is the only way
a command of the operator's is run through the listener, and the commands
a run ask may hold are a fixed list, which the table below has whole. The
giving is there too for a device of any user, for an ask with
[`secret.user` or `secret.as`](api.md#givesecret) that names them: their
[own address in a source](#told-outside-the-yard) or their [own credential
in one](integrations.md#a-call-by-a-session-or-a-crew-member), which goes
to a file of theirs and to no credential of the yard. No approval is
there for a device of another user, also not of a subscription of their
own, which they approve at the yard. No call reads a credential or an
address back, for any device.

It writes as its user and as nobody else: mail to a crew member, a user or
a name a linked yard exposes, an answer to an ask (a pick, words, put aside,
declined), a note on a bead, and the acknowledgement of its user's own mail
and notifications. A call that names another user or a crew member as its
writer, or another user's inbox, is refused. The yardmaster does what the
operator asks by mail, so a device of the operator can have work shaped,
built and landed. Of what is [refused from an agent's
pane](#who-is-acting) it can have run what a run ask may hold, by
approving one, what a setup approval carries, and what the yardmaster runs
for the operator on that mail ([below](#the-yardmaster-for-the-operator)),
and nothing else: no other such command is among the calls.

It reads every bead with its notes, the crew, the users, and through the
stream the mail of its user alone: its own mail, the reactions on it and
its own read receipts, nothing of anyone else's. `device add` for a user
who is not an operator says that it reads every bead. It is also what a
lost token gives away until its device is removed. Four things at the
terminal a device does not read whole: the mail of others; the candidates
of a setup card, the yard's other accounts the card offers to reuse, each
a file of the yard's with the values it holds, and the file the account of
an approved setup is set in, which `Bead` and `UserAsks` tell to a device of
a current operator alone, as the stream does the file a `source_set`
points a source at and the command it mints its token with, the file a
`user_set` points a user at and the secret file a `hook_blocked` names (to
any other the card reads as one with none, the setup's result says the
account is set and not where, or that it failed and no more, and the events
name the source and not its file or command, the user and not their file,
the hook and not its secret file, so a lost token of another user, or of
one who was the operator, gives away no address or file of an account the
yard keeps); the users and the crew, whose `Users`, `GetUser`, `Crew` and
`GetCrew` tell a device of an operator alone the file a user's address is
read from, the directory a crew member runs in, the files and commands its
config names and the handles of its sessions (any other device reads the
records without them, so these calls give a lost token of another user no
file or command of a user or a crew member); and
`Settings`, which answers a device the promised keys
alone: `operator`, `blob_max_bytes` and `files_max`, and to a device of
an operator alone the operator's own words to their agents,
`operator_style` and `operator_note`, and whether the yard is hosted,
`hosted`. No other setting is told to a device
(a lost token gives away no `edge`, `hook_public_url` or `backup_dir`),
and the `setting_changed` event of such a setting comes with its key and
without its value.

**What a lost token of a user who is no operator gives away of an
operator's configuration.** The yard's records of it (the settings, the
users, the crew, the depots, the sources, the hooks and the links to other
yards) reach a device in the results of its calls and in the events of the
stream. A device that is no operator's is told no file, command or
secret from either. The stream sends only the keys of each event that its
kind lists: the names of what the event is about, numbers, and words from a
fixed vocabulary. It holds back the rest, such as the handle of a crew
member's session, the error of a failed send, a depot's path and test
command, a backup's directory, a workspace's worktree, a refused move's
command, a pane's screen, the files a removed user's login leaves on disk
(the names of what went with them are sent), and any error text that names a
file or a command.
A kind with no row sends no data at all, so a kind added later says only that
it happened until its author lists what it tells, and a test fails until they
do. The calls hold back the same. `Flows` says that a depot's flows could not
be read and not why. `Peers` has no address and no error text. `DeliveryFiles`
says that a file was not taken and not why. `GiveSecret`, which a device of
such a user has for their own address, credential or login code only, answers
with the names it wrote and not the file they are in, and refuses a value the
file cannot hold without naming the file. `Bead` and `UserAsks` tell a run
ask as its asker typed it, without the yard's sentences about what it
changes, the files it carries, the plan of a pack, or the result of its
approval, except one that sets a setting, adds a source or adds a group,
whose typed words carry a value or a path: those words, and the sentence of
its approval, are the operator's alone. A notification's text is made from what its person is
told of the event. A device of an operator gets all of it whole.

What a device can still read is text written about one thing that happened,
as it was written. That is the command a run ask's asker typed, but for one
that sets a setting or adds a source, whose value or path is the operator's
alone; a note the yard wrote on a bead about what it did or what failed,
such as a landing's line, which names the test command it ran, or a fetch
that did not work; and the words of an error a call returns when the yard's
own file operation failed, such as a depot it could not read. Rows from
earlier builds were remade on upgrade. What a person writes (a
note, a mail, a question and its options) is sent as written to the readers
of its bead or the parties of the mail, and is not an operator's
configuration.

**A stolen device of the operator can replace a source's credential, and
cannot read one.** Until `yardr user device rm`, whoever holds its token
can answer a secret ask that is not declined or taken back with a value of
their own, and the source's next call is made with it: to the host the
operator declared, with a key that is not the operator's. That includes an
ask that was answered already. Giving again replaces what was given. They
cannot ask for a secret (an ask is an agent's or the terminal's to write),
cannot name a file or a source unless an ask that is not declined or taken
back names it, and read no credential by any call. Each such giving is an
event with `via <device>` and the names, never the value. A device of any
other user, and a credential that anything but the yard's own record of a
device vouches for, cannot do it at all. What a stolen device of another
user can replace is that user's own address in a source or their own
credential in one, while an ask for it is not declined or taken back: what
the yard tells that user then goes to an address of the thief's, and a call
made as that user is made with a credential of the thief's, each at the
host the operator declared. The value crosses the network under the TLS in
front of the listener and nothing more
([api.md](api.md#tls-is-yours-and-the-clients-rule)); nothing secret
crosses a link to another yard.

**A stolen device of the operator can approve run and setup asks.** Whoever
holds its token can approve every open run ask that is asked of the operator,
a strong one included (below). They need not wait for one: a device cannot
write a run ask, but it writes mail as the operator, and the yardmaster does
what the operator asks by mail, so the holder can ask the yardmaster to
propose a command and then approve it. Where the yardmaster's delegation is
on, the holder can ask it to run the commands of [its list](#what-it-runs)
itself, with no card to approve: the yard checks that the cited mail is the
operator's, and a stolen token is the operator's sender, so the holder's
mail passes as the operator's own. The table below lists each command a
run ask may hold, and the runs that need none, with what a stolen device can
have run with one agent's help. Some changes it makes stand after
`yardr user device rm`. With no
terminal at hand, a hosted yard's operator signs the device out at the edge
from `/ready` on another device. That cuts it off from the yards behind the
edge within about a minute. A yard it reaches without the edge still needs
`yardr user device rm` (see A device that is lost, below). Setup asks are
covered below under Strong cards. Row by row:

| the command | what a holder of the token gains |
|---|---|
| `peer add <name> <invite>`: a peer linked from an invite | The yard the invite names is linked, and its calls and mail then cross as the invite says. The card is strong (trust), and the link stands after the device is removed: a way in for that yard |
| `peer set <name> --url <url>`: where a peer is called moved | The link calls the moved address and the old address is stranded: the calls and mail for the peer go to an address the holder chose. The card is strong (trust), and the move stands after the device is removed |
| `peer allow <peer> --mail-limit <n>`: a link's mail limit raised | A yard that is linked already may write here more often. Nothing of the holder's own |
| `peer allow <peer> --send <kinds>` or `--receive <kinds>`: what a link carries widened, of any kind | Mail may flow where it did not: out to a linked yard's crew through a crew member the holder directs by mail, or in from the linked yard. With `bead` beads may cross, and only once an intake says where they go does one arrive; with `ask`, `file` and `thread` the linked yard's questions stand among this yard's decisions, its files land in this yard's blobs and what it says of a bead is kept on it. Nothing of the holder's own |
| `peer allow <peer> --expose <names>`, `--trust <names>` or `--reach <name>@<peer>=<user>`: a link's trust widened | Who is exposed, which names are trusted and whose asks are sent widen as the card showed. The card is strong (trust), and the widening stands after the device is removed; narrowing is anyone's and no card |
| `peer intake <peer> --depot <depot> --flow <flow>`: where a linked yard's beads go | The beads a linked yard sends, once the link takes `bead`, become beads of a depot of this yard, pinned to a flow, and whoever the routes lead the flow's first stage to works them. Where that is a group of agents, work starts on the other yard's text, which becomes the brief of an agent that runs with this yard's permissions, with nobody of this yard looking first: the card names the group. A linked yard the holder runs can then start work here. Anyone takes an intake away again, and only the operator replaces one |
| `source set <source> calls_per_hour=<n>` or `updates_per_hour=<n>`: a source's calls an hour raised | Calls that are granted already may be made more often. Only the operator lowers it again |
| `source grant <source> read\|<call> --crew <name>` or `--group <name>`: a grant of a source's calls | The yard's credential for that source now works for a crew member or a group that the holder directs by mail: the holder can have the source read, and for an update call written to, and reads the results in the notes. Without the grant the holder has only what was granted before |
| `source add <file>`: a new source declared by a file with relative command programs and a card that fits | A source the holder names stands that did not, its declaration and each command file it names installed as the card showed them. The yard's own credential for it is still unset, so nothing is read or written until the operator gives a key and grants a call; but what then runs and where it sends are the file the holder had installed |
| `source rm <name>`: a source removed | The source's declaration and the files of its commands go, with the grants of its calls and the counts and keys the yard kept for it; the file its credential is read from stays, and what names the source (subscriptions, followed threads, hooks) stays naming it. Calls the source made possible are no longer made. Only the operator declares it again |
| `source move <from> <to>`: a source's records folded into another's | The hooks, identities, crew grants of threads, subscriptions and followed threads the yard keeps under the first source are the second's, as the card counted them. A hook so moved is verified by the second source's declaration, and a user's identity moved makes a sender in the second source count as that user: the card is weighed `trust` and `as-user` for them, and it is not taken back by any command |
| `link move --from-source <source> --to-source <source>`: the yard's links moved between sources | The links of one source are the other's, on the same host: the same issues, read by another source's declaration. Moved back by the same move the other way |
| `hook rm <name>`: a hook removed | That endpoint takes no more deliveries, and what it was set to do (what it says, makes, maps and acts as) is gone with it, to be typed again by the operator unless it stood in the shape the `hook add` card makes. The card is strong (`trust`). It is also how a hook the yardmaster added for the operator is taken back |
| `source ignore <source> --rm <id>`: a sender of a source no longer ignored | The sender's next message follows the hook's word again: under `unknown` a fresh mapping card is offered, under `allow` it is heard as an outsider. The rest stay ignored, and the sender is ignored again only from the mapping card their next message offers |
| `source add <file>`, replacing a source that stands: a source's declaration replaced | The new declaration and each command file it names are installed as the card's diff showed them. The card is strong (code) in every replacement form: what the source then runs and where it sends are the holder's, and stand after the device is removed |
| `pack apply <name>`, with no flag: an embedded pack whose card fits 40 lines | Of kind source, the pack's sources added, or replaced with their diffs on the card, with the pack's command files installed from the yardr build the yard runs. Of a depot kind, re-applied to the one depot that holds it where its plan asks nothing and is not refused or blocked by edited files, the plan's objects created and changed as the card showed them: flows, groups, roles and scripts. Either still grants no call, makes no hook and sets no credential's file |
| `pack apply <name>`, first install of an embedded depot pack to a named depot | The pack is installed to the named depot with the mappings and plan the card showed. Where the pack brings a program or trust the card is strong, and what the install made stands after the device is removed; a credential by itself does not make a card strong. A pack too large for a whole card, whether first installed or re-applied to a named depot, is a summary card, gated by its weight. A pack of kind source named with `--depot` is not this row's: it is sent to `bead ask --setup`, and named bare it is the row above's, with its sources whole on the card |
| `hook add <name> --source <source>`: a new webhook endpoint for a declared source | A URL on the yard, `POST /hooks/<name>`, that runs the source's normalize command on each delivery it verifies with the signing secret the holder gave on the hook's own secret card: the holder knows the secret, so can send deliveries the yard hears as the source's, which reach whoever subscribes to it. Where the yard registers the hook itself the secret is the yard's made one, which the holder never sees, and the approval registers it at the service with the values the holder gives the card's asked names. It maps no stage and acts as no crew member; a hook with a table is the next row's |
| `hook add <name> --source <source>`, replacing the hook that stands or carrying `--says`, `--makes`, `--map` or `--as`: a hook's table replaced | The events of the source move and make beads as the holder wrote: each says line reports its outcome for the bead the event is about, each makes line makes a bead where none tracks the thread, and each map entry moves a tracked issue as the named crew member. The card is strong (`trust`) and shows each table now and after, whole. It stands after the device is removed, and only the operator takes it back, with another card carrying the table that stood |
| `subscribe <user> yardr …`, asked of the operator: a subscription to the yard's own events, the operator's or another user's | That user is told of more of what happens in the yard, as notifications in their inbox and, where a source tells them, outside the yard. The holder reads those events in the stream already |
| `user set <user> tell=<parts>`, asked of the operator: which parts a source tells the operator or another user of outside the yard | That user's phone is told of more or of less, and what still waited to be told of a part that is dropped is not told. Where it is told and with which address stay as they are, and the inbox and the queue of decisions are filled as before. The holder gains nothing to read |
| `user key <user> <source>`: a user's own address in a source | The given address is written into that user's 0600 env file, and the yard's hearer may be subscribed to its deliveries beside it. Only the operator gives another address |
| `source check <source> --user <user>`, asked of the operator: one real test message to the user through the source | That user's phone is told once, with the test message the card carries whole. The holder gains nothing to read, and no record of the yard changes |
| `user set <user> role=operator\|member`: a user's role changed | The user becomes an operator, and so does whoever speaks as them, their devices among them, approving run and setup asks and administering the yard; or an operator becomes a member and approves nothing from then on. The card is strong (`trust`) either way, the user the `operator` setting names and the last operator are not made members, and what an operator approved stands after the role is taken back. Only the operator takes it back |
| `user add <name>`: a user added | A user the holder names reads mail from now on, and may be subscribed to the yard's events. The holder reads the yard's mail already; the user is told nothing outside the yard until `notify` is set, which no run ask holds |
| `user rm <name>`: a user removed | That user is no more: their subscriptions, devices and telling outside the yard go, and what waited to be told them; so do the group that carries their own login with its routes, every route for their beads, and the yardmaster's conversation with them. The mail stays in the log, and so do their login folder and env file on disk. The operator is not removed this way |
| `user invite <name> --email <address> [--role operator]`: a teammate invited | A user the holder names is added, as an operator where the card says `--role operator`, their address is mapped to them in `web_jwt_users`, and the invite is reported to the edge, so the holder can have a second person admitted to this yard as that user, as an operator where the card names it. Until the report lands nobody is admitted. The card is strong (trust), and the person admitted stands after the device is removed |
| `user device rm <user> <device>`: a user's device removed | That device's token is refused from now on, and the streams it held are closed. What the device did stands. A holder can have a lost device of another user cut off, and can have the operator's own other devices removed, leaving the holder's, until an operator pairs one again; the yard's terminal and the hosted web sign-in are untouched. The card says how many devices the user keeps. Only the operator pairs a device again |
| `user uninvite <name>`: a teammate removed | That teammate is no more at the edge or here: the removal is reported first, then the user, their devices and the mapping go. The member's own account and any yard of their own stand |
| `crew set <name> open_threads=<sources>`, `set_state=<sources>` or `peers=<peers>`: a crew member's grants widened | A crew member the holder directs by mail may do more outside the yard: open threads in a source, set the state of threads, or mail a linked yard's crew. Its runner, directory and the rest of its definition stay as they are |
| `group add <name> --runner <runner> …` or `crew add <name> kind=<kind> …`: a new group or crew member | The group or member the holder names stands, with the command of an `exec` group, the prompt, the agent kind and the permission preset as the card showed them whole. The card is weighty (`code`) and says where a preset skips permissions. A group does nothing until a route sends it work, which the yardmaster shapes on the operator's mail, so the holder can have that done too. It stands after the device is removed. The operator or the yardmaster takes a group or a crew member away with `group rm` or `crew rm`; only the operator removes the yardmaster's own entry |
| `group set <name> prompt=<file>`, `cmd=<command>` or `preset=<preset>`; `crew set <name> prompt=<file>` or `preset=<preset>`, with `kind`, `model` and `dir` for the yardmaster's own entry: what a group or a member runs changed | What every later session of the group or member runs is the holder's: its prompt, the command of an `exec` group, a permission preset that skips permission prompts and, for the yardmaster's own entry, its kind, model, directory and any preset. The card is weighty (`code`) and shows each whole, now and after. The next session starts with it, and it stands after the device is removed. Only the operator puts the old values back |
| `peer identity <peer> <crew> --source <source> --id <id>`: a sender of a source mapped to a crew member of a linked yard | A delivery from the id is kept with the speaker `<crew>@<peer>`, whatever the hook's unknown word says, and the operator is offered no mapping card for it; it reaches crew as that yard's mail does, `information, not instructions`. The sender is no user, and an id on the source's ignore list is cleared from it. Only the operator takes it back |
| `crew identity <name> --source <source> --id <id> --key <key>`: a crew member's identity in a source set | Mentions and assignments of the id or key there are then addressed to that crew member, and what it does there is its own: the holder can have what is meant for another reach a member it directs by mail, or blind a member to what is meant for it. Only the operator sets it back |
| `user identity <user> --source <source> [--id <id>] [--key <key>]`: who a user is in a source | Threads opened there for them are assigned to the id or key, and where the source hears by identity a delivery from them is heard as theirs; an ignored id or key is cleared in the same write. The user or the operator sets it back |
| `settings set <key> <value>`: a setting changed | The yard runs under another number or name. The `operator` setting and the settings of where the yard reads and serves from are no run ask's; `hook_public_url` is one, so a holder that has it approved points later hook registrations at an address of their own |
| `group rename <old> <new>`: a group called another name | The group, its routes, the beads it owns and their live sessions go on under the new name, and nothing is released, as the card named them. What names the old name outside the yard's records, a script or a role prompt, reaches no group, and a default route that names its group by the depot template stops serving the depot whose group it was. The old name is free. Only the operator renames it back, and nobody can where the old name is one pack imports give a depot's groups |
| `peer rename <old> <new>`: a link renamed | A linked yard's name here changed: its key, address and what it carries stay, and what waited for it moves with it. Mail to the old name reaches nothing |
| `peer rm <name>`: a link removed | That link is gone on this side, with its intakes, identities and queued mail; the beads of this yard that were there end as undelivered, and the beads it sent stay and say their link is removed. The other yard stands, and still has this one as a peer until its operator removes it there; its requests are refused here from now on. Only the operator adds it back, from a fresh invite |
| `bead update <bead> --originator <user>`: a bead's originator changed | A bead is for another user. Whose work it counts as changes; the work stands |
| `timer rm <id>`: a timer removed | What the timer would have told is not told |
| `up`: the yard started | Herdr's server, the yard's server and the daemon start where they are not running, with the web view and the BDP server where their addresses are set, and what runs already is left alone. Taken back by `down` |
| `crew restart <name> [--for <user>]`: a crew member's session restarted | A fresh session starts once the current one is idle, of the line session or, with `--for`, of one conversation. A requested restart cannot be taken back |
| `depot test <depot>`: a depot's test command changed | The holder's command stands as what tests the depot: a script group's command gets it as YARDR_TEST, and builders' primes tell them to run it. Only the operator takes it back, with another card carrying the command that stood |
| `depot base <depot> <base>`: a depot's base changed | Bead branches of the depot start from the holder's base instead. Only the operator takes it back, with the base that stood |
| `group add` without a prompt, or `group set` of `kind`, `model`, `dir`, `worktrees` or a named preset that keeps the agent's permission prompts; `crew set` of `kind`, `model`, `dir` or such a preset; `depot test`; `depot base`; `user set <user> notify=<source>`; a new `hook add` in the card's safe shape: run by the yardmaster on the holder's mail, within [the delegated bounds](#what-it-runs) and with no card | The change stands as the yardmaster's delegated run, recorded for the operator and mailed to them in the write that makes it. The holder needed no card and no approval, and the weight of the `group add`, `hook add` and `depot test` cards goes with the card. What it made (a group's or a member's new definition, a depot's new gate, where a user is told, a hook's endpoint) stands after the device is removed, until the operator has it taken back: with one more mail for the defining changes, and for the hook only by approving its removal card |
| `pack apply` of an embedded pack, or of a packs-repository pack pinned to its digest: run by the yardmaster on the holder's mail, with no card | The pack's objects stand in the depot as an approval would have put them, whatever the card's bounds: a first install's groups, roles and scripts are among them, unread on any card. The digest, the plan and each updated object's diff are in the record. They stand after the device is removed, and no one command takes them back: a pinned re-apply to the digest that stood before it, or the record's diffs by hand |

**Strong cards.** A card the yard weighs `trust` or `code` — it widens
who the yard listens to, or it brings a program the yard will run — is
approved from a paired device as any card is. A weighty card asks of a
device that the call says its person saw the card's weight (`weighed`,
[api.md](api.md#weighty-cards)), and a stolen token says that as easily
as a person does. So a stolen phone that is paired and unlocked can
approve any card, one that installs a program or adds trust among them,
and what the approval made stands after `yardr user device rm` has ended
the device. The hosted web asks more of a strong card than a device does:
it is approved there in a session the gateway authenticated for a strong
card no more than fifteen minutes earlier, which the yard reads from the
signed time the gateway gives and from no token of the yard's; one fresh
sign-in witnesses every strong card approved inside those fifteen
minutes. On any yard it is approved on the yard's socket, at a terminal on
the yard's machine, as any card is; a self-hosted yard's web view names
that terminal instead of approving it. An approval says how it was
witnessed on its event (`witness: fresh sign-in` in the hosted web,
`witness: terminal` on the socket), and an approval through a device says
`via` and claims no witness. The bead and event stream show the approval
to clients that read or follow them; an outside notification depends on
the operator's existing subscriptions and `notify` and `tell` settings,
and successful delivery, so no alert to every paired device or outside
address is promised. A card weighed neither trust nor code — a credential
or an update taken alone, a routine card, and every narrowing, `peer rm`
among them — is approved as it always was. A delegated run is no card, so
it takes none of these gates: the operator's cited mail is its approval
([above](#what-it-runs)).

A setup approval is no run ask, and gives a holder nothing past what one
decision carries: an embedded pack of sources applied (the `pack apply`
row), its account typed (the giving above) or taken from a source the yard
has one for, whose file the pack's sources then read, and the picked
preset's calls granted (the `source grant` row). It is asked as a run ask
is, by mail to the yardmaster.

What it still cannot have run, by a card or by the yardmaster on its mail:
adding a device or pairing one, changing the operator, where a user's
address is read from, a hook add that keeps a secret file of its own,
reading a source's
credential or setting where it is read from or the command that mints one,
free `args` of a group or a crew member that stands and any env setting of
one, and applying a pack from a directory, or from a URL but the packs
repository's, with a flag beyond the depot, the mappings and the pin, or
where its plan asks. The delegation switch it can move either way, as it
approves any card, through the operator's own doors
([above](#the-yardmaster-for-the-operator)). A stolen device still cannot
pair another device or change the `operator` setting. It can approve any card,
though, and a card can make a way in: one that links a yard of the
holder's (`peer add`), admits a second person as an operator (`user invite
… --role operator`, or `user set … role=operator` for a user the yard has),
opens an endpoint (`hook add`) or installs a program
the yard then runs (`source add`, `pack apply`). What such an approval
made outlives the device. `yardr user device rm` ends further approvals at
the device's next request and undoes none of what it approved: that has
to be found and undone by hand. Each approval through a device is marked
`via <device>` (below), which says where to look, and the card and `yardr
prime --bead` name the command that takes each change back.

What stands against it besides: the yard runs the command stored on the
ask and takes no word of it from the call; an approval is refused when
what the command names or changes is no longer what the card said; a
command runs once for one ask; an approval is taken only from a request
that reached the listener from the yard's own machine, where TLS ends, or
down the link from the edge ([edge.md](edge.md#footplate-through-the-link);
a secret is taken besides from the yard's router, and down the link when the
yard is hosted or the secret is a harness login's code, a setup approval that
types the account being such a secret,
[api.md](api.md#tls-is-yours-and-the-clients-rule));
and each approval through a device is marked `via
<device>` on the answer and on the change's own event, which names the
ask (`via phone via edge` down the link). `yardr user device list` says
when and from which address a device was last used. A device of any other
user, and a credential that anything but the yard's own record of a device
vouches for, approves nothing.

**Approvals cross the edge.** A paired-device token approves run and setup
asks from wherever it reaches the yard, down the link from the edge
included, a strong card among them, but for the account a setup types, a
secret that crosses a hosted yard's link and not a connected yard's
(below). The same physical device may open the hosted web after a fresh
account sign-in, which is how a strong card is approved there. The edge
sees the device's token on every call, so a
dishonest or compromised edge, or a stolen phone that is unlocked, can
approve exactly the command stored on any ask, a strong one too. What
holds it is that the command is fixed on the ask, that the operator paired
the device, that the phone's unlock stands in front of the approval, which
the yard cannot verify, and that `yardr user device rm` ends it at the
next request. Nothing holds a strong card harder than that. The fresh
sign-in and the terminal are other ways to approve one, not further
conditions of a device's approval.

**A harness login's code crosses the edge too.** When the yard leads a
harness's login for the operator ([crew.md](crew.md#the-yard-leads-a-harnesss-login)),
a paired device of the operator gives the code the sign-in page shows, down
the link as from the yard's own machine. The edge reads the code with the
device's token. The code is used once, within minutes, and the yard hands it
to the waiting login in its memory and keeps it nowhere. On a yard the
operator runs and has connected to the edge it is the only secret that
crosses: an API key, a source's credential, the account a setup approval
types, a hook's signing secret and a user's own login code are refused down
the link and answered at the yard's own machine. A setup approved by taking a
candidate or keeping the account types nothing, and crosses. `yardr user
device rm` ends a device's reach at its next request.

**On a hosted yard every secret crosses the edge.** A hosted yard is one the
service made and runs, and its `hosted` setting is `true`
([configuration.md](configuration.md#settings)). The service runs the edge
and the machine the yard lives on, and the operator has no shell on that
machine to answer at, so a paired device of the operator gives any secret ask
down the link: an API key, a source's credential, the account a setup
approval types, a hook's signing secret, a user's own address or credential,
and the login's code. The edge reads the
value with the device's token, as it reads every call; the yard writes it to
its file, as it does for a giving at the yard's own machine. A device of a
user who is not the operator still gives nothing down the link, hosted or
not, and a call that the edge says came in plain HTTP is refused as ever.
`yardr user device rm` ends a device's reach at its next request.

**A session's screen crosses the edge too.** A paired device of the operator
reads a running session's screen down the link as from the yard's own
machine, so a terminal opened over the edge shows what the session shows.
The edge reads the screen with the device's token, as it reads every call.
A screen can show what an agent printed, a secret among it, so a member's
device is answered as for a method the yard does not have. `yardr user
device rm` ends a device's reach at its next request.

**A stolen device of the operator can read every grant, and no credential
with it.** Until `yardr user device rm`, whoever holds its token can read
the whole grant graph ([api.md](api.md#sourcegrants)): for each source,
which crew members and groups may make which of its calls, as whom, and who
gave each call and on which card. A grant is a record of names and times,
so the holder learns who may reach which source and as whom, and never a
credential or an address, which no record holds. A device of any other
user cannot read it.

**A stolen device of the operator can turn telemetry on, and read the
install id.** Until `yardr user device rm`, whoever holds its token can
switch the yard's telemetry on ([api.md](api.md#telemetryset)), so the
daemon starts sending the daily batch of counts, and can read the install
id the batch carries ([api.md](api.md#telemetryshow)). The batch holds
counts and fixed names only, and the holder learns no bead, no error's
words and no actor's name from it. A device of any other user can do
neither.

**A write says how it came.** What a device writes carries the device's
name: the event has `via` in its data, and wherever the writer is shown the
mark stands behind the name, for a person and for an agent alike:

```
    52 10-12 19:41  mail from mckean (via phone): Land yardr-1a2b when the tests are green
```

in `yardr inbox` (a crew member's and a user's), in `yardr mail --with` and
`yardr chat`, in the notes and answers of `yardr prime --bead` and `yardr
review` (`[2026-10-12 19:42 mckean (via phone)]`, `mckean (via phone)
picked 1`), and on a bead's page in the web view. Down the link from the
edge the mark says that too: `mckean (via phone via edge)`. A client reads
it as `via` ([api.md](api.md#over-the-network)). It is a record of the way
in.
Nothing is decided by it: mail `via` a device is its user's word as mail
from the terminal is.

**A device that is lost.** `yardr user device rm <user> <device>`, at the
yard's terminal or by asking the operator. The token is refused at the next
request, and the event streams the device held are closed at once. The yard
also resolves the token of an open stream again every minute, and closes
the stream when it no longer resolves.

**A device that is lost, with no terminal at hand.** `yardr user device rm`
needs a terminal on the yard's machine, and the operator of a hosted yard has
none there. The operator can still have the yard remove it: they ask the
yardmaster for the card (`yardr bead ask --run "user device rm <user>
<device>"`) and approve it from another paired device, which ends the token
on the yard itself ([chat.md](chat.md#questions-for-you) has the card). The
person whose device it is can also cut it off at [the
edge](edge.md#footplate-through-the-link), whose gate in front of a yard is
their account's session. On another device they sign in at the edge, open
`/ready` and sign the lost one out in the list of the other places the
account is signed in. The edge then refuses the device's next call to the
yards behind it within about a minute, and ends an event stream it holds open
within about two minutes. The device's token stays valid, so a yard the
device reaches without the edge, at the yard's own listener, still answers it
until `yardr user device rm` removes it.

**What is refused** is answered 401 before the request's body is read, and
is counted by its reason alone: `no_credential` (no `Authorization: Bearer`
header, or more than one) and `unknown_credential` (a token the yard does not
know, a removed device's among them). A lookup that does not finish in time
(`resolve_timeout`) is answered 503 with `Retry-After`, since the token may
be fine. If the store fails while checking a credential, the yard answers
500: that is a yard fault, not a token to replace. Nothing is kept for the address a request
came from or for the token it carried, and no request is slowed or answered
429, so wrong tokens from an address cost a device at that address nothing.
The refusals are logged in sums, as [a hook's](subscriptions.md#a-refused-delivery)
are: the event `user_refused`, about no bead, with `reason`, `status`,
`count` (how many since the last such event for that reason) and `last`, at
most one for a reason in ten minutes.

**Where they are listed.** `yardr user device list`; `yardr user show
<name>`, one line a device (`device: phone, added 10-12 19:02, last used
10-12 19:40 from 100.64.0.7`); and `yardr status`, in its part `devices`.
A device that pins a channel key is said to be `sealed (paired)` after its
last use where it pinned the key at a pairing, and `sealed (in place)` where
it registered it with its token ([below](#sealed-in-place)); a device that
pins none is said to be neither.
The events are `device_added` and `device_removed`, about no bead, each
with `user` and `device`; a pairing's are [below](#enrolled-by-a-code).

From Go the commands are `store.API`'s `AddDevice`, `Devices`,
`RemoveDevice`, `DevicePairOpen` and `DevicePairClose`. Resolving a token to its device and user is the server's
own (`(*store.Store).ResolveDevice`) and is no call on the socket; the
handler of the listener is `rpc.UserServer`.

### Footplate on another machine

A person reads mail and answers asks in footplate. On the yard's own
machine it talks to the yard over its socket, and nothing needs pairing. On another machine the device calls the yard's listener:
`hook_addr` sets where it listens, and the operator must make it reachable
from the device. The `--url` to give is an HTTPS one the device can reach:
over HTTPS the connection protects the code and the device's token, as the
sealed form of the QR code does by itself ([below](#enrolled-by-a-code)).
Only an operator opens a pairing, with `yardr user device pair <user>
<device> --url <address>`, and reads the code to the person, who types it
into footplate. A pairing is never opened from an agent's pane, whoever the
command says it is: a crew member hands the command to the operator. This
is not `yardr peer pair`, which links two yards
([peers.md](peers.md#pairing)).

### Enrolled by a code

`device add` prints a token for you to carry to the device. `device pair`
has the device fetch it: the operator opens a pairing for one device of one
user, the yard shows a short code, and the device that calls the yard with
the code is answered with its token. Nobody types or pastes the token, and
no command prints it. It is the pairing by which [two yards are
linked](peers.md#pairing), with what a device has no use for left out.

```text
$ yardr user device pair mckean phone --url https://mac.example.net --qr
Pairing is open for 10 minutes, until 14:32:10, for the device phone of mckean. Give the device this yard's address and the code:

  address  https://mac.example.net
  code     K7QM-2XPD
  channel  <the yard's channel public key>

  <the QR code>

The code is good once: a device that scans the QR code is answered inside a sealed channel, and whoever carries the connection reads neither the code nor the device token. 5 wrong codes end the pairing, and so does: yardr user device pair --end
It then calls as mckean until: yardr user device rm mckean phone
```

- **The code** is one of a pairing of yards: eight characters, good for 10
  minutes and for one device. A yard has one device pairing open at a time,
  beside the one it may have open with another yard; `yardr user device pair
  --end` ends it. A second opening while one stands names whose it is: `a
  pairing is already open for ada, try again in a few minutes` — the
  holder's yard user alone, never the device, the code or when it runs out.
  The holder opening again is told of their own pairing instead, with its
  device and when its code runs out. A wrong code is counted, and the fifth
  ends the pairing.
  The code is in the server's memory until it is used, and nowhere else: not
  in the store, not in an event, not in a log. A server that stops takes an
  open pairing with it. The command does not wait for the device: `yardr
  user device list` has it once the code was used.
- **`--url`** is the address the device reaches this yard at, given to the
  device with the code. Without it, it is the listener's own address, which
  is right on one machine or one network, or the edge's when the yard's link
  is up and it listens nowhere; a listener on every address (`:8790`) needs
  it. A yard with neither opens no pairing. Where the address is the edge's,
  the device is also given this yard's handle and name at the edge, printed
  as a `yard` line beside the address, so it knows which of the account's
  yards it pairs with.
- **The device is added when the code is used**, as by the operator who
  opened the pairing, and is then a device as `device add` makes one: the
  store keeps the token's SHA-256, `device list` and `device rm` know it.
  A device that redeemed sealed is recorded with its channel public key
  pinned beside that hash (`channel_key` where a device is read as JSON);
  a plain one pins none.
  What `device add` refuses is refused when the pairing is opened: a user
  the yard has not, a second device of the name. If either becomes true
  while the pairing is open, the code is used and nothing is added.
- **`--json`** prints `{"pairing", "user", "device", "code", "address",
  "yard", "channel_key", "channel_signature", "expires", "qr"}`, where
  `qr` is what the QR code holds, `yard` this yard's handle and name at
  the edge where the address is the edge's (absent otherwise),
  `channel_key` the yard's channel public key the QR's fourth field
  holds and `channel_signature` the yard identity's signature of it,
  and draws nothing. Where the yard's channel key could not be made or
  read the two channel fields are absent and the pairing is the plain
  one below, with no key in its QR code.

**What the QR code holds.** With `--qr` the command also draws a QR code in
the terminal, for a phone to read in place of the address and the code
being typed. It holds one line of text and nothing else:

```text
<address>#<code>               https://mac.example.net#K7QM-2XPD
<address>#<code>#<yard>        https://my.yardr.dev#K7QM-2XPD#mckean/airy
<address>#<code>#<yard>#<channel>        https://my.yardr.dev#K7QM-2XPD#mckean/airy#<channel public key>
<address>#<code>##<channel>   https://mac.example.net#K7QM-2XPD##<channel public key>
```

The second is where the address is the edge's: the device reads which of
the account's yards it pairs with from the text, and says so with each
call. `<address>` is the yard's address as the command prints it: `http://`
or `https://`, a host, perhaps a port and a path, no query, no `#` of its
own and no slash at its end. `<code>` is the code as it is printed, with
its dash. `<yard>` is the yard's handle and name at the edge, `handle/name`.
`<channel>` is the yard's channel public key: 43 characters of base64url,
the public half of the one X25519 key the yard holds for this beside its
identity, which signs it. A client splits the text at its `#`s: the
address, the code, the yard where a third field stands and is not empty,
and the channel key where a fourth stands — empty in the third place
where no yard is named, as the fourth example shows. The first two, with
no key field, are the pairings of before and are redeemed plainly, exactly
as they always were. The QR code is of error correction level M.
QR readers must accept this optional fourth field; a reader that requires
exactly two or three fields will not read a new pairing QR.

The yard's channel key stands in a file of its own beside the yard's
identity (`channel.key` beside `peer.key` in the yard's home, mode
0600), made once, the first time a pairing needs it, and never changed
by a later pairing: the key a yard gave out in a QR stays its key. Its
public half is signed by the yard's identity, as `channel_signature` in
`--json` carries it, so a device that later learns the key from anywhere
but the yard's own screen can check it against the identity it holds. A
yard whose identity or channel key cannot be read or made opens a plain
pairing instead, with no key field, and says so.

**What a device does with them.** One request, without a credential:

```text
POST <address>/user/pair
{"code": "K7QM-2XPD"}
```

```sh
curl -X POST https://mac.example.net/user/pair -d '{"code": "K7QM-2XPD"}'
```

The code is read as it is typed: the dash and spaces may be left out, small
letters are capitals, and `I` and `L` are read as `1`, `O` as `0`.

**Sealed, by the QR's key.** A device that took the channel key from the
QR's fourth field redeems sealed instead, and never sends that plain
request. It makes itself an X25519 key pair for this yard, pins it, and
opens a Noise handshake to the yard's channel key (pattern IK, X25519
with ChaChaPoly and SHA-256, run on the yard by `github.com/flynn/noise`
and on the device by its own Noise library): the pairing code is the
first message's payload, and the device's own public key is sealed
inside that message, where only the yard holding the channel private
key can read either. The one request, without a credential:

```text
POST <address>/user/pair
{"sealed": "<base64url of the handshake's first message>"}
```

The yard opens the message, redeems the code it carries — for the
pairing's user alone, where the request names its presenter, exactly as
a plain redemption — records the device with the public key the
handshake proved pinned beside its token's hash, and answers the
handshake's second message with the `{"token", "user", "device",
"yard"}` of the table below, and the yard's registration voucher for
the device beside them as `voucher` ([api.md](api.md#a-registration-voucher)),
as its payload, sealed to that device:

```text
200 {"sealed": "<base64url of the handshake's second message>"}
```

Neither the code nor the token crosses readable, to the edge or to any
other carrier, and the sealed answer opens only with the device private
key of the handshake. A sealed code is held as a plain one is: a wrong
code inside a handshake is answered in the table's words (403
`wrong_code`, or 409 `not_added`, in the clear of the status and the
reason alone) and counted, the fifth ending the pairing; bytes that are
no handshake to this yard's key are 400 and no try, as a body that is no
code is. A transcript recorded and replayed buys nothing: the code is
used by the first redemption, so the replay meets a pairing that is
done, or counts as a wrong code against the next one, and no second
device is added and no second token answered. A yard with no channel
key file, or one its server was told nothing of, serves the plain form
alone and answers a sealed body 400.

**By a typed code, through a PAKE.** A code that is typed or read
aloud took no key from a QR, so the device and the yard agree on one
from the code itself: they run SPAKE2 (RFC 9382) with the code as
the password, in three steps of this same path, and exchange their
static channel keys under the key the run derives. This holds only
for a code the edge never saw — shown on the yard's own screen, in
its terminal, or read aloud from there. It does not cover a member
pairing opened from the app: the router reads that code and returns
it, so the edge holds the password and could sit in the run as
either end; a member is sealed by the QR an operator opens on the
yard's own screen, as above, and no other way yet. The suite is
pinned here for the clients that implement it:
SPAKE2-P256-SHA256-HKDF-HMAC of RFC 9382 section 6 — P-256,
SHA-256, HKDF-HMAC-SHA256
and HMAC-SHA256, the suite's own M and N, the identities `A` (the
device) and `B` (the yard), and no additional authenticated data —
with the transcript and key schedule of the RFC's sections 3.3 and
4. The password is the code as it is typed cleaned: its dash and
spaces out, capitals, `I` and `L` as `1`, `O` as `0`, as eight ASCII
bytes. On the yard the run is `github.com/backkem/spake2-go` at
v0.0.1, which its tests hold against the RFC's appendix B vectors;
no PAKE code is the yard's own, and the library states it has had
no formal audit, so none is claimed for it. The device makes itself
an X25519 key pair for this yard, as a sealed device does, and runs:

```text
POST <address>/user/pair
{"pake": {"step": "start", "msg": "<base64url of the first message>"}}

200 {"pake": {"step": "exchange", "session": "<base64url of 16 bytes>", "msg": "<base64url of the second message>"}}
```

The first and second messages are the run's two blinded points, 65
bytes uncompressed each, base64url without padding. The yard answers
from the code of the pairing it has open; a first message that is no
point of the suite is 400 and no try. The session names the one run
the yard holds — a new start replaces it, it lives two minutes at
most, and it is bound to the pairing it began under, so a run whose
pairing ended, ran out or was replaced redeems nothing and counts
nothing from then on: its finish, or a confirmation counted against
it, finds no such pairing.

```text
POST <address>/user/pair
{"pake": {"step": "confirm", "session": "...", "confirm": "<base64url of the device's confirmation>"}}

200 {"pake": {"step": "confirmed", "confirm": "<base64url of the yard's confirmation>"}}
```

The confirmations are the RFC's `cA` and `cB`, 32 bytes each. This
is the step a wrong code spends its one online guess at: a
confirmation that does not hold is 403 `wrong_code`, counted as any
form's wrong code is, the fifth ending the pairing, and the run is
spent either way. A step that names no live session is 400 and no
try. The device checks the yard's confirmation before it sends
anything more: only then has the other end proved it holds the code.

```text
POST <address>/user/pair
{"pake": {"step": "finish", "session": "...", "key": "<base64url of the sealed device key>"}}

200 {"pake": {"step": "done", "answer": "<base64url of the sealed answer>"}}
```

Under the run's derived key — the first half of the transcript's
SHA-256, through HKDF-SHA256 with no salt to a ChaCha20-Poly1305
key, a fresh 12-byte nonce before each sealed message — the device
seals its channel public key (its 32 raw bytes; the HKDF's info is
`yardr pake device key v1`), and the yard, which opens it only in a
confirmed run, answers the `{"token", "user", "device", "yard"}` of
the table below with the yard's own channel public key beside it as
`channel_key` and its registration voucher as `voucher`, sealed the same
way (the info `yardr pake answer v1`), for the device to pin as a QR
device pins the QR's key. The
finish of a confirmed run spends it whatever it carries, and one
sent before its run is confirmed is 400 with the run still live;
a sealed key that does not open is 400 and no try; the presenter
rule is every form's,
judged at the finish before the code is spent; and a confirmed run
redeems as a sealed one does, its device recorded with the key the
run exchanged pinned beside its token's hash. The run proves the
code, not that the device holds the key it sends, so the device
makes a fresh key pair for the run: a key another device of the yard
pins already is 409 `not_added`, and the code is spent. Neither the
code nor the token crosses readable at any step. A recorded transcript buys
no offline test of the code — that is the run's defining property,
and the failure of a code-keyed HMAC's transcript — and replayed it
buys nothing either: its session is spent, its confirmation does not
hold in a new run, and no second device is added. A yard with no
channel key serves no PAKE run and answers its start 400: it has no
static key to exchange.

| answer | what it says |
|---|---|
| 200 `{"token", "user", "device", "yard"}` | the device's token, the one time it is anywhere, and what [`/user/me`](api.md#over-the-network) then says of it: the user it calls as, its own name, the yard's name. The answer carries `Cache-Control: no-store`. Keep the token where the device keeps a secret. A device that redeemed sealed or through a PAKE has one field more, `voucher`: the yard's push registration voucher for it, a statement of its user, device, yard and expiry that is signed, not concealed ([api.md](api.md#a-registration-voucher)). A plain redemption's answer has none |
| 403 `{"refused": "wrong_code"}` | not the code. It is counted, and the fifth ends the pairing |
| 409 `{"refused": "not_added"}` | the code was right and is used, and the device could not be added. The operator opens another pairing |
| 400 | no code at all: a body that is not `{"code": ...}`, or a code that is not eight of a code's characters. It is no try |
| 404, or 401 | no pairing is open: none was opened, the code was used, it ran out, five wrong codes or the operator ended it. The path is there only while a pairing is open, and otherwise is answered as [any path under `/user/`](api.md#let-in-or-not): 404 in a yard without a device, 401 without a credential. The operator opens another pairing |

An answer that does not arrive leaves a device nobody has the token of:
remove it (`yardr user device rm`) and pair again.

**Through the edge.** A redemption the edge forwards down the link names who
presents it, in the header `X-Edge-Presenter`: the address of the signed-in
account the edge resolved. The yard looks the address up in `web_jwt_users`,
the table the web view looks a token's email up in, and redeems the code for
that user alone, the user the pairing was opened for. A member pairs by a
QR the yard's operator opens on the yard's own screen (`yardr user device
pair --qr` there) and scans that: the web view's pairing page opens no
pairing now, because the page and any QR it showed reach the member
through the edge, which could read or rewrite the code and the key. The
page says so: pairing from it no longer gives an app that can call
through the edge, and to ask the yard's operator to open a pairing QR on
the yard's own screen and scan that. The yard answers any
other presenter as it answers a wrong code, 403 `wrong_code` with no device
added, and counts it with them: the fifth refusal ends the pairing. An address
the table maps to nobody is refused the same way where the pairing's user is
one it names, and where neither the presenter nor the pairing's user is named,
as for an owner's pairing redeemed in the owner's session, the code redeems as
with no presenter. An invite puts a teammate's address there, and the operator
adds their own to sign in on the web ([Settings](configuration.md#settings)).
This rests on every presenter the edge forwards being the owner or an active
member, and every active member being mapped: where a teammate's line is
removed while the edge still holds them active, or before the edge records a
removal, that holder redeems the owner's pairing with its code, which is still
required. A refusal leaves the code available to the pairing's user until the
fifth refusal ends the pairing. A redemption with no `X-Edge-Presenter` redeems
on the code alone, as it does on the yard's own listener. The yard reads it on
this call and no other. It can only narrow who redeems, never widen it, since
the code is still required, so a client that sends it straight to the
listener gains nothing by it.

**What it rests on.** On the listener a plain code is all a device needs:
whoever has it within its ten minutes, and reaches it, gets the token. Show
the code, and the QR code, to the device and to nobody else. There is no
check for two people to compare, as a pairing of yards has. That check
keeps someone between two yards from putting a key of their own in place
of a yard's; a device that pairs by the QR's key needs none, because the
key reaches it on the yard's own screen, where the carrier cannot rewrite
it, and its code and token then cross sealed. A device that pairs by a
typed code needs none either, for the PAKE's reason: the code alone
holds against someone in the middle, who gets one guess a run and no
test of a recorded one, and the keys it pins crossed under the key the
run derived — but only where the code itself never crossed the carrier,
as a member pairing's code crosses the router that hands it out. A device paired plainly has
no key of its own in the yard's keeping: what it is given is a bearer token, which whoever
reads the connection reads on every call after this one. So such a device rests
on the connection, for the code as for the token: `https` off the yard's
machine ([api.md](api.md#over-the-network)). The command says so when the
address is plain `http` and not this machine's. Five tries at a code of 40
bits are what someone who can reach the listener without the code has; they
can end a pairing with them, and no more.

The events, about no bead, each with `pairing`, `user` and `device`:
`device_pair_opened` (with `expires`), and `device_pair_refused` for a
pairing that ended with no device, with `reason` (`wrong_code`, `timed_out`,
`closed`, `not_added`) and `wrong_codes` when there were any. The
`device_added` of a device enrolled so names its `pairing`.

### Sealed in place

A device paired before the yard had a channel, plainly or by a token added
by hand, holds a token and no channel key. Updated, it makes its key pair
and registers the public half once, in a call its token authenticates
(`RegisterChannelKey`, [api.md](api.md#a-channel-key-registered-in-place)).
The yard pins the key on the device's record and answers its own channel key
signed by its identity; from then on the device's calls are sealed, as a
device paired by the QR's key calls.

What it does not have is a QR's guarantee. The edge carries that one call,
and an edge active at it, putting its own key in place of the device's or of
the yard's, could hold the device's channel: read every call the device
makes from then on, undetected, until the device is re-paired by QR. An edge
that only logged the call cannot. So the device list keeps the difference:

```text
$ yardr user device list
USER  DEVICE  ADDED        LAST USED
sam   phone   11-05 09:12  11-05 09:40 from the edge, sealed (paired)
sam   tablet  10-28 18:03  11-04 22:15 from the edge, sealed (in place)
```

The label lasts for as long as the device keeps the key it registered.
Re-pairing it by QR lifts it (`yardr user device rm`, then `yardr user
device pair --qr`); re-pairing is never required.

The registration is refused, leaving the record as it was, for a device that
pins a key already, whether a pairing or an earlier registration pinned it:
it is registered once, and cannot be replayed to take a device back to plain.
A repeat of that registration is not a second one. A device whose answer was
lost on the way back sends the same key again, and the yard answers it with
its keys, as it did the first time. The record does not change and no second
event follows. The answer is the yard's public keys, so the repeat grants
nothing. Another key, a device a pairing pinned, and a call that came sealed
are refused. It is refused for a key that is no X25519 public key and for one
another device pins, and by a yard that cannot read or make its channel key,
or read its identity key to sign it with, which says why. It is the one
plain device call the yard still takes down the link from the edge beside
the pairing redemption. `yardr user device rm` deletes the pinned key with
the token, as for a device paired by QR. The event is `device_channel_pinned`,
with `user`, `device` and `pinned` (`in_place`).

## A conversation

`yardr mail --with <name>` sends nothing. It prints the mail between who
runs it and that crew member or user, both directions, oldest first:

```
   412 10-05 14:02  read    mckean: deploys are frozen until Friday
   415 10-05 14:03  read    yardmaster: noted. Until when on Friday?
   431 10-05 16:40  unread  mckean: until the release is tagged
```

The columns are the mail's seq, when it was sent, whether its receiver has
read it, the sender and the text; on mail that carries
[a file](#a-file-in-mail) it stands under the text, on a marked
line of its own. A crew member's session reads the same exchange with the
user's name: `yardr mail --with mckean`.

`--after <seq>` prints only what is newer, and `--json` prints a list of
`{seq, at, from, to, text, read}`: a view asks again with the last seq it
has.

`yardr mail --with <name>@<peer>` is the same for a crew member or a person
of a linked yard ([peers.md](peers.md#mail-between-crew-members)); what was sent
there says `queued`, `sent` or `failed` in place of read.

**One history for one person.** A user who is at another yard too, under a
name the operator [trusts as
them](peers.md#trusting-a-name-as-a-person-of-this-yard) (`yardr peer allow
airy --trust mckean@airy=mckean`), has one conversation with each party of
this yard, whatever name they wrote under. `yardr mail --with yardmaster`,
run by mckean, has the mail between mckean and the yardmaster here, what
mckean wrote the yardmaster as `mckean@airy`, and what the yardmaster wrote
to `mckean@airy`, in the order written. The yardmaster reads the same with
`yardr mail --with mckean`. Each line says how the mail went:

```
   412 10-05 14:02  read    mckean: deploys are frozen until Friday
   415 10-05 14:03  read    yardmaster: noted. Until when on Friday?
   502 10-05 19:10  read    mckean@airy (as mckean): until the release is tagged
   507 10-05 19:12  sent    yardmaster to mckean@airy: then I land the shed on Monday
```

- **Nothing of it is unread for the user here.** What they wrote as
  `mckean@airy` is in the inbox of whom they wrote, and read as that one
  reads it. What was written to `mckean@airy` left for the other yard: it
  has `queued`, `sent` or `failed`, never a reading, was never in the user's
  inbox in this yard, and `yardr inbox` neither counts it nor acknowledges
  it.
- **The address alone is still there.** `yardr mail --with mckean@airy`
  prints the mail with that one address, as before, trusted or not.
- **It goes by what the link said when the mail was written.** Trust taken
  back keeps the mail of its time in the user's conversation, since it was
  their word then; what the name writes afterwards, and what is written to
  it, is the name's alone. Mail from before the grant is in it too: the
  grant says who the name is, and nothing of since when. Once the link is
  removed its mail is in no conversation, as nothing reaches the address.
- **Mail kept only at the other yard is not here.** What mckean wrote at
  airy to airy's own crew never crossed the link.

`--json`, [`Conversation`](api.md#conversation) and [`yardr
chat`](chat.md#a-name-trusted-as-a-user-of-this-yard) have the same letters.

**Read** means acknowledged in the receiver's inbox with
`yardr inbox --ack <seq>`. A user or an operator may acknowledge a user's
inbox; a crew member acknowledges their own. A crew member's acknowledgement
covers its whole inbox through the seq named. Mail to a party that has since
been removed counts as unread. An acknowledgement that passes mail is an event,
`mail_read` (with `reader` and `through`): `reader` names the inbox whose
cursor moved, and the event actor names who acknowledged it. A view that
follows the stream learns of it. One that passes a user's notifications is
`notifications_read`, with the same two fields.

[`yardr chat`](chat.md) is this exchange as a view in the terminal, with a
line to type.

From Go the same reads are `store.API`'s `Conversation`, `UserInbox`,
`AckUserInbox`, `Users` and `GetUser`; the notifications are
`UserNotifications` and `AckUserNotifications`.

## Members of groups

A manual group's `--members` are names, taken as they are given, whether or
not a user or crew member has them ([groups.md](groups.md)). `yardr doctor`
notes a member that is neither, since nobody of that name can be mailed;
nothing is refused.
