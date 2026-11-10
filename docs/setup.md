# yardr setup

`yardr setup` takes a yard from its onboarding to a first bead in work, as a
short run of forms in the terminal. It is the same setup the yardmaster does
with you in conversation, and you can change from one to the other after any
step.

```sh
yardr setup
yardr setup --json | jq -r '.next.next'   # the command for the next step, no form
```

The onboarding ends in one of the two: once the yardmaster is up, its last
question is "Talk to the yardmaster" or "Keep going here". "Talk to the
yardmaster" opens [`yardr chat`](chat.md#from-the-onboarding) on the
yardmaster in the same terminal, and "Keep going here" is `yardr setup` in the
same run. Both act as the operator the onboarding made, since its "Your name"
takes only who the terminal's commands act as: your login name, or
`$YARDR_ACTOR` ([users.md](users.md#who-is-acting)).

The yardmaster's first job is the setup, as a conversation with you: it asks for
the repository and its base branch, registers the depot, proposes setting it
up from the yard's default pack, and offers an approval card when that pack is
embedded in yardr. A selected pack from a directory or non-embedded import
is set up by you in a terminal with `yardr depot init <depot>`. The setup
imports the flow, the groups that build, review and land, their roles and
their routes, all kept in the yard and nothing in the repository; it checks
with you what tests the project and offers a first bead to try the line. It
asks before anything outward, such as a commit.

What the onboarding itself asks, how it starts the yard and what it does
when the yardmaster does not come up is in
[configuration.md](configuration.md#creating-a-yard-the-onboarding-and-yardr-init).
A harness run for the first time on a machine may show a screen of its own
first (a welcome, a login): the wait for the yardmaster says so once it sees
that, with the pane's last lines and how to reach it, and goes on waiting
for you to answer it in the pane.

## One list of steps

The steps are the ones [`yardr doctor`](doctor.md#setup) prints, read from
the store by one function. `yardr setup` prints them, each `done` or `todo`,
and takes the first one not done. It keeps no state of its own, so it goes on
where the yard stands, whoever did the steps before: these forms, the yardmaster
in conversation with you, or you on the command line. Run it again at any time.

The yardmaster's first run reads the same list through `yardr doctor`. There
is no second list to keep in step with it.

## The setup as a conversation

The yardmaster's line session does the setup with the operator; a
conversation session has none to do. It reads `yardr doctor` when it starts.
The "Setup" part of that list says of each step `done` or `todo` and names the
next: while a step is `todo`, the yardmaster's job is the setup, with the
operator. It goes on from the first step not done, never from the top, since
someone may have done part of it already, and reads `yardr doctor` again after
each step. The other parts of `yardr doctor` say what is wrong with the yard
itself. The yardmaster tells the operator of every line marked `FAULT` (a
program not installed, one that does not start) before the setup: the line
says where to get what is missing, which is the operator's to install.

The onboarding ends in [`yardr chat`](chat.md#from-the-onboarding), which shows
the operator the mail between them and the yardmaster and nothing of its pane.
So when the yardmaster's first prompt asks it to greet them, it does so by
mail: `yardr mail <operator> - <<'MAIL'`, then what it needs from them for the
next step, and `MAIL` on a line of its own. The setup is then a conversation
by mail: the operator answers in the chat, the answer reaches the
yardmaster's inbox, and it writes back with `yardr mail <operator> -`. It
stays by mail for as long as they write by mail, since what the yardmaster
prints in its pane they do not see. If they write in its pane instead, it
answers there.

The operator can take the same steps in their own terminal, as the
[forms](#the-pages) below: `yardr setup` reads the same list, does the first
step not done and offers after each to come back to the yardmaster. So a step
may be done when the yardmaster looks again, and when a step is quicker for
the operator there (the import asks its questions as one form) the
yardmaster says so. In its pane `yardr setup` asks nothing and prints the
list ([below](#where-no-form-runs)).

The steps, as the yardmaster takes them:

1. **A depot.** It asks for the repository's path and its base branch, then
   `yardr depot add <depot> <path> --base <branch>`.
2. **The depot's way of working.** The depot is set up in one import, from
   the yard's default pack: the flow and its stage files, the groups that
   build, review and land, and their routes. That is every step of the list
   from the depot's files to the routes
   ([one page](#one-page-sets-the-depot-up) does it for the operator). The
   default pack is the setting `default_pack` (`yardr settings get`); unset,
   it is `starter`, and `dir` for a depot that is a plain directory. Only
   the operator records an import. For an embedded depot pack, the yardmaster
   proposes a run card for the operator to approve: `yardr bead ask --new
   "Set up depot <depot> from <pack>?" --context "the operator must approve
   the pack's import" --run "pack apply <pack> --depot <depot>"`, with the
   mappings that answer its plan
   ([packs.md](packs.md#proposing-an-embedded-pack); `yardr bead ask --help`).
   A card carries no questions, so
   the yardmaster first reads `yardr pack check <pack> --depot <depot>`
   ([packs.md](packs.md#yardr-pack-check)): it prints the plan and, under
   "an import would ask", what is open. The operator gives those answers by
   mail (the agent and model of each group of agents, who is in each group of
   people), and the card carries them: `--bind builders=<kind>[:<model>]
   --bind reviewers=<kind>`, `--members backlog= --members decisions=<them>`
   and `--crew yardmaster=<the yardmaster's own name>`. Approving runs the
   import as the operator, so the record is the operator's. The card sets no
   test command: that is the next step's. When the selected default pack
   resolves outside the yardr binary, the operator runs `yardr depot init
   <depot>` in a terminal; it reads that pack and its plan there.
3. **The test command.** `yardr depot show <depot>` prints it as `test:`. For
   a git depot an import in a terminal sets one when the repository says how
   it is tested ([below](#the-test-command)): a guess, so the yardmaster asks
   the operator whether it is what tests the project. The card of the step
   before sets none, so there the yardmaster asks what tests the project
   instead. To change it, or to set one where the import set none on a depot
   the yard holds already, it proposes the change as a card (`depot test
   <depot>` with `--run-test`; `yardr bead ask --help`). On a depot the yard
   has not, the test command is the operator's to type: `yardr depot add
   <depot> <path> --base <base> --test '<command>'`, with the path and base
   `yardr depot list` shows, since an add without a flag drops what the flag
   set. Builders and reviewers run it before they hand on, and the assembly
   runs it on what lands; a depot with none lands untested.
4. **A first bead.** It offers a small one to try the line end to end
   (`yardr bead create - -b - --depot <depot> <<'BEAD'`, then the title, a
   blank line, the goal and what done means, and `BEAD` on a line of its
   own), and advances it to `new` once it is shaped, as with any bead. The
   setup is over when that bead is merged: `yardr doctor` then says so.

## The pages

Each page asks what its command needs, shows the command line as you would
type it (after `$`, as `yardr --home <home> …` for a home other than the
default) and runs that command.

On a yard whose `operator_style` is `guide`
([configuration.md](configuration.md#how-agents-work-with-you)) the pages
print no command lines: the questions and what each command prints say what
happens. The commands run are the same. With any other style, and with none,
the pages show them.

| step | the page asks | and runs |
|---|---|---|
| a depot | the repository's path (it starts at the directory you are in, when that is a checkout's top), the depot's name (the directory's, in lowercase), the base branch (the branch checked out) | `yardr depot add <depot> <path> --base <branch>`. A directory that is no git checkout is asked for no base and becomes a dir depot |
| the depot's `.yardr` files on its base; a group that builds; a group that reviews; a group or person that lands approved beads; routes for new, review and approved | what the import asks (below) | `yardr depot init <depot>`, which takes the yard's default pack |
| a first bead | its title and its goal | `yardr bead create "<title>" --depot <depot> -b "<goal>"`, then `yardr bead advance <id> --to new` |

The subscriptions' limits (`yardr source add aiquokka`, once aiquokka is on
PATH) are an optional step with no page: the setup does not wait for it, so
no form stops at it, and it is listed as a `note` until the source is
declared. [doctor.md](doctor.md#setup) says what it is done by.

The last step, a first merged bead, has no page: it is the line's to do.
There the forms end and say where the first bead is and how to watch it:
`yardr session list`, `yardr bead show <id>`, `yardr watch`, and
`yardr web serve` for the yard in a browser ([web.md](web.md); once the web
view is installed as a login service, its address instead). They also say
so when no daemon is running, since nothing is dispatched then.

### One page sets the depot up

Five of the steps are one import, so they have one page between them:
[`yardr depot init <depot>`](packs.md#yardr-depot-init)
with the yard's default pack: the `default_pack` setting, else the pack
yardr carries for the depot's kind, `starter` for a git depot and `dir` for
a dir depot ([packs.md](packs.md#the-default-pack)). It gives the depot its flow and stage files, the
groups that build, review and (in a git depot) land, the two groups of
people the flow names, and a route for each stage. The roles, the flow and
the assembly's `merge-local` are the pack's, installed under
`<home>/packs/<pack>/` and `<home>/flows/`; nothing of them goes into the
repository.

The command prints its plan first: every file, group, flow and route it
would write. Then it asks what [`yardr pack apply`](packs.md#yardr-pack-apply)
asks:

- for each group of agents, the agent, the model and the permissions (it
  starts at the yard's `default_kind`, with the flags that keep it from
  stopping to ask);
- for each group of people, who is in it (empty for anyone);
- which crew member takes the pack's yardmaster, when the yard has more than
  one;
- `Apply pack <pack> to depot <depot>?`. Nothing is written before this yes.

A yard where part of this was done by hand gets the same page for the rest,
under the title of the first step not done. The plan says what the import
would change there: a group that exists under the pack's name is changed,
and a role the depot has under `.yardr/roles/` is named as shadowed
([packs.md](packs.md#how-a-pack-maps-to-the-yard)). A step counts as done by its
route alone, so a group that was added and never routed is not a group that
builds yet. To finish such a yard by hand instead, add the routes
(`yardr route add --stage <stage> --depot <depot> --group <group>`) and the
steps turn done. A script or a group of people bills no subscription, so a
route to one takes every originator's beads, and its command says so with
`--originator any`, as the release routes
[below](#letting-the-yard-cut-releases) do. `route add` makes it any when the
flag is left out. A route to agents takes the operator's beads alone unless
it names an originator ([groups.md](groups.md#which-beads-a-route-takes)).

The pack's assembly lands on local branches only. When the depot's base is
not one (a remote's branch such as `origin/main`), the page says so before
the import runs: every approved bead would be held
([line.md](line.md#4-approved-the-assembly-lands-it-on-main)).

### The test command

For a git depot with no test command, the import sets one when the
repository says how it is tested: a test script of its own (`.yardr/test`,
`scripts/test`), `go.mod`, a `package.json` with a test script, `Cargo.toml`,
a Makefile with a `test` target. It is a guess, named in the import's last
question and printed when the import is done, with the command that changes
it; `yardr depot show <depot>` prints it afterwards. A repository that says
none of that gets no test command, and its beads land untested until you set
one ([configuration.md](configuration.md#tests)). Nothing is written into
the checkout either way, and a dir depot gets no guess.

## After each page

It asks how to go on with the step that is next:

- **Keep going here**: the next page.
- **Talk to the yardmaster**: opens [`yardr chat`](chat.md) on the yardmaster
  in this terminal. The yardmaster goes on from the same step: write to it
  what you want next. Leaving the chat (ctrl+c) ends the run with one line,
  how to come back (`yardr chat`) and that the yardmaster keeps running. With
  `yardr setup --no-chat`, and when who runs it is no user of the yard (the
  chat is a user's; it says so, and how to open it as the operator), it prints
  where the yardmaster is in herdr (agent, workspace, tab, pane) and brings
  its pane to the front instead. When the yardmaster is not running it opens
  no chat: it prints what [`yardr doctor`](doctor.md) says of the yard (the
  `FAULT` lines and the yardmaster's line) and how to start it
  (`yardr init --start`).
- **Stop here**: ends; `yardr setup` goes on from that step later.

## Being told when beads land

Not a step either. Where the forms end, before the question below, `yardr
setup` asks a [user](users.md) "Be told when beads land?", which starts at
no. Yes runs `yardr subscribe <you> yardr --done --where depot=<depot>` for the
setup's depot: each bead of it that becomes done is then a notification in
your inbox, apart from your mail
([subscriptions.md](subscriptions.md#users-as-subscribers)). It is not
asked of who already has a `--done` subscription to yardr, nor of who is
no user of the yard. The onboarding does not ask it.

## Herdr's server at login

Not a step either. Once the first bead has merged, `yardr setup` lists,
under `Optional`, the login service for herdr's server: whether one starts
it at login, yardr's own or a file of yours, and else that `yardr up
--login` installs it and the daemon's
([install.md](install.md#keep-it-running-after-a-reboot)). It is never a
`todo`: the setup is complete with or without it.

## The machine's disk

Not a step either: the yard sees to it. Every landing, before its tests
run, checks the free space on the volume holding GOCACHE. Under
`disk_keep_gb` (30 GB while unset) it points GOCACHE at a fresh sibling
directory with `go env -w`, so builders that start afterwards use it, and
records the old directory with the time; a landing that starts 30 minutes
or more after a rotation removes the recorded directories, never the live
one; and it clears go-build directories in GOTMPDIR older than 3 hours.
A rotation less than 30 minutes after the last frees nothing (the
directory it leaves cannot go yet) and starts one more cache from empty, so
the landing skips it. It still removes and clears what it can, and its
event carries `not_rotated` with the reason.
What it did is a `disk_kept` event (`yardr events`, `yardr watch`), with
the free space before and after and what was removed, and a gave-up line
in the assembly's log when free space is still under 10 GB afterwards.

A rotated directory does not wait for the next landing to go: the daemon
removes it when its 30 minutes are over, with no landing running and
whatever `disk_keep_gb` is set to, since a directory past its grace has no
use. It wakes for the end of the earliest grace, removes in a goroutine of its
own (a cache of tens of gigabytes takes a while, and no pass or stop waits
for it), never takes the live cache or a path that is no rotated sibling of
it, and logs the same `disk_kept` event, whose `not_rotated` says that
nothing was rotated. A removal that fails, or a daemon that cannot ask `go`
which cache is live (`go` is not on the PATH it runs with), is told once in
the daemon's log and tried again after a wait that doubles up to an hour. The
rotation itself stays the landing's keeping, under `disk_keep_gb`.

The 30 minutes are a guess that a build which started before a rotation is
over by then, and while the disk is short the guess costs more than it guards.
The test gate refuses to start under 20 GB free
([the disk floor](testing.md#the-disk-floor)), so while free space on
GOCACHE's volume is under that floor, a landing's keeping and the daemon also
take a rotated directory inside its 30 minutes when no go process can be using
it. `go` reads GOCACHE once, as it starts, and `go test` does not hand it to its
test binaries, so only a go process that started before the rotation can be
writing to the directory the rotation left, and the process table (`ps`) says
whether one is running. The directory of the oldest rotation goes first, and
none goes once the volume is over the floor. Its record stays until the 30
minutes are over, so the rotation it marks still holds the next one off, and
the `disk_keep_gb` keeping does not rotate again at once. The `disk_kept` event
lists the directories taken this way as `removed_early`. A process table that
cannot be read (no `ps`, one that fails, a start time it cannot make out) holds
every directory to its 30 minutes. The daemon looks at the disk on each pass,
which reads the free space and the directories and no process table, and reads
the table only when it could take a directory; it looks again no sooner than
half a minute after the last, and after a look that failed at a wait that
doubles up to an hour. The floor is the gate's: `YARDR_TEST_FLOOR_GIB` in the
daemon's or the keeping's environment gives another one, and `0` turns this
off.

The table does not show everything, and the 30 minutes do not guard it
either. A virtual machine or a container that mounts the directory can be using
a directory the host's table shows nobody for. A go command that was given the
directory as its GOCACHE in its own environment is not the rotation's to know:
the rule that GOCACHE comes from the go env file (below) guards the keeping and
the yard's own processes, not a person's. And a tool that is not `go`, a linter
or an analyzer, can read files from the paths `go list -export` gave it before
the rotation. A build that has a cache directory taken from under it fails
(`could not import ... no such file`) and passes when it is run again. All
three are a rarer exposure than the 30 minutes' own guess, and they apply only
while the disk is under the floor.

GOCACHE belongs to the machine and the record of rotations to the yard, so
two yards on one machine each see only their own rotations. A landing
therefore also records every rotated cache it finds beside the live one that
no record names, whichever yard left it: a real directory named as the live
cache is, with a lower number, holding the README Go writes into every cache
it makes. The moment it finds one counts as the moment it was left, so it
holds that landing's own rotation and is removed 30 minutes later, or sooner
where the disk is under the floor and no go process is running at all, since
none then started before that moment. The event lists these as `adopted`. Name
a Go cache of your own differently:
`go-build-3` beside a live `go-build-9` is taken for a rotated cache.

Two things are the operator's. First, GOCACHE must come from the go env
file, not the environment: a GOCACHE set in the environment wins over
`go env -w`, and the rotation does not take (the keeping says so and does
nothing). Second, GOTMPDIR must be set for the pruning to have a
directory: `go env -w GOTMPDIR=$HOME/.cache/go-tmp`, or wherever the
machine keeps it; unset, that step is skipped. `yardr disk keep` runs the
same keeping by hand, and prints what it did. Docker volumes and cargo
targets are none of its business: it keeps Go's caches alone.

## Linking with another yard

Not a step: a yard is complete without it. Where the forms end (every step
done, or the first bead on its way) `yardr setup` asks "Link with another
yard?", which starts at no. Yes opens the form of `yardr peer pair`
([peers.md](peers.md#the-form)): one yard shows a code, the other enters it,
both operators compare a number, and each says what the link may carry and
whom of their yard the other may write to. The
onboarding asks the same once the yardmaster is up, after the daemon at login
and the web view.

It is asked once a run, of the operator (only the operator pairs), and not
on a yard that has a peer. A pairing that fails says why and the setup goes
on; `yardr peer pair` in a terminal opens the form at any time.

## Cancelling

A page that is cancelled (ctrl+c, or no at its last question) has run none
of its commands: the yard is as the finished pages made it, and
`yardr setup` goes on from the same step. Leaving the import, at any of its
questions or with a no at the last, has written nothing: no group, no file
under the home, no test command. The forms then stop and print the step and its
command rather than ask again.

A command that fails ends the run with its error; what the page ran before
it stays done.

## Where no form runs

Without a terminal, with `--json`, and in an agent's own session
(`YARDR_SESSION` or `YARDR_CREW` is set: the yardmaster's pane is a terminal
too) `yardr setup` asks nothing. It prints the steps and the command for the
next one; `--json` prints `home`, `steps` and `next`, as
[`yardr doctor --json`](doctor.md#json) names them.

On a home with no yard it is refused like any other command (exit 2), or, in
a terminal, runs the onboarding first.

## For a program that drives the first run

A program can take a person from no yard to a first bead in work by running
yardr itself, as [footplate](https://github.com/yardr-dev/footplate) does: it
asks the questions in its own window and runs the commands below, in this
order, each with `--json` and with stdin closed. No command of the list asks
anything then, and each prints one JSON value on stdout and nothing else
there. `contrib/isolated-yard/firstrun_test.go` runs the whole list so, and
reads nothing but the JSON and the exit status.

| exit status | means | with `--json`, on stdout |
|---|---|---|
| 0 | done | the command's result, below |
| 1 | it failed, or what it was given is refused (an unknown harness, a base that does not resolve, a move the flow does not allow) | `{"error": "<one line, in yardr's words>"}` |
| 2 | it had something to ask and no terminal to ask it in: the home has no yard yet, or the import has answers open. Nothing was written, and the same command with what it names goes through | `{"error": …}`; for the import also `"missing"`, the flags to add |
| 3 | what it names is not there: the depot, the bead, the user | `{"error": …}` |

The error line is for the person; the status and `missing` are for the
program.

Who the commands act as: the operator is a [user](users.md#who-is-acting), and
a user is who commands act as, the login name unless `--actor <name>` or
`$YARDR_ACTOR` says otherwise. A program that lets the person type another
name than their login passes `--actor <that name>` on `yardr init`, with it
as `--operator`, and on every command after: the yard's settings are the
operator's alone to change, and a user's subscriptions their own or the
operator's.

| | the program runs | which prints |
|---|---|---|
| 1 | `yardr doctor --json` | `yard`: `false` on a home with no yard, and nothing is created by asking. `machine`: each program with `found` (a harness that answers its version flag as another program is not found: [doctor.md](doctor.md#a-program-that-answers-as-another)) and, for a harness, `harness`, the kind to give `init` ([doctor.md](doctor.md#json)). Exit 1 while something the yard needs is missing |
| 2 | `yardr init --harness <kind> [--operator <name>] [--yardmaster <name>] [--model <model>] [--style <style>] [--about "<line>"] --json` | `home`, `harness`, `yardmaster`, `operator`, `operator_style`, `operator_note`, and what a second run kept: `yardmaster_kept` and `settings_kept` (a list, empty on a new yard). Run again it makes nothing twice. A run that finished one cut short has `found` and `added` ([configuration.md](configuration.md#creating-a-yard-the-onboarding-and-yardr-init)). It needs no herdr server and starts nothing without `--start` |
| 3 | `yardr setup --json`, here and after any step | `home`, `steps` (each `key`, `title`, `done`, `detail`, `next`, and `optional` on a step the setup does not wait for) and `next`, the first step not done that the setup waits for, with its `depot` once there is one. The keys, in order: `depot`, `depot_files`, `builders`, `reviewers`, `landing`, `routes`, `limits` (optional), `first_bead`, `merged_bead`. Exit 2 before step 2 |
| 4 | `yardr depot add <depot> <path> --base <branch> --json`; for a directory that is no git checkout, without `--base` | `name`, `path`, `kind` (`git` or `dir`) and `base` (`""` for a dir depot). Run again it changes the depot to what it says, except that only the operator changes its path, base or test command |
| 5 | `yardr depot init <depot> --yes --json` | exit 2 and `missing`, a list of flags, each `--<flag> <name>=<what is wanted>`: `--bind <group>=<kind>[:<model>]` wants a harness for a group of agents, `--members <group>=<users>` the users of a group of people, by comma, or nothing after the `=` for anyone |
| 6 | the same with a flag for each entry of `missing`, the name kept: `yardr depot init <depot> --yes --bind builders=claude --bind reviewers=claude --members backlog= --members decisions=<operator> --json` | `result`, with `pack`, `depot` and how many objects were `created`, `changed` and `unchanged`; `plan` and `answers`, what it wrote and with which answers; `test`, the depot's test command where the repository says one ([above](#the-test-command)). Run again, `created` is 0. Still exit 2 with `missing`: ask for those too |
| 7 | `yardr bead create "<title>" --depot <depot> -b "<goal>" --json` | the bead: `id`, `title`, `stage` (the flow's first, `backlog`), `depot`, `status` |
| 8 | `yardr bead advance <id> --to new --json` | the bead, its `stage` now `new`. A second run is exit 1: the bead is there already, which `yardr bead show <id> --json` says |
| 9 | `yardr subscribe <operator> yardr --done --where depot=<depot> --json`, if the person wants to be told when beads land | the user's subscriptions, a list of `source`, `scope` and `where`; this one has `where.state` `done`. Run again it adds nothing |

After step 8 `setup --json` has `next.key` `merged_bead`, the line's own
step: a running yard takes the bead from there
([configuration.md](configuration.md#starting-the-yard)), and `yardr status
--json` says what runs.

What the list leaves to the program: which directory and which base branch
(yardr reads no repository before step 4), and the answers to step 5: for a
group of agents the yard's harness, `harness` of step 2, is the answer that
needs no further question, and for a group of people the operator, or
nobody.

## As a script

Scripts skip the forms and the yardmaster's conversation (`yardr init --help`):

```sh
yardr init --harness claude [--yardmaster yardmaster] [--dir ~/projects] [--model opus] [--operator Jo] [--style show] [--about "..."]
yardr depot add webapp ~/projects/webapp --base main

# the depot's way of working, from the yard's default pack (starter, unless
# yardr settings set default_pack names another): the groups that build, review
# and land, their roles, the flow and the routes go into the yard, and nothing
# into the repository; the depot's test command is guessed from it
yardr depot init webapp --yes --bind builders=claude:opus --bind reviewers=codex \
  --members backlog= --members decisions=jo
yardr depot show webapp                 # test: what the builders, the reviewers and the assembly run

yardr daemon &                          # routes and supervises; starts the yardmaster (or: yardr init --start)
yardr watch                             # live event stream
yardr session list --running            # what agents and scripts work on now; without the flag, also what a person holds and what another yard works
yardr bead list -a                      # closed beads too: the newest 200 (-n <N> for more, --all for every one); session list -a likewise

yardr bead create "Add login page" --depot webapp -t feature -b "Use the OAuth flow"
# it starts in the flow's first stage, backlog; the yardmaster shapes it and advances it to new
```

From there the bead travels its depot's flow: the starter pack's, the one
`yardr depot init` sets a depot up with, is drawn and read in
[flows.md](flows.md#drawing-a-flow) (`yardr flow draw starter`), and
[line.md](line.md) follows a bead through it.

## Letting the yard cut releases

Not a step: a yard is complete without it. Once depot yardr's base carries
the release flow (`.yardr/flows/type/release.flow`), the yardmaster adds one
exec group and four routes; from then on a release bead cuts the release
([install.md](install.md#with-the-yard)):

```sh
yardr group add yardr-release --runner exec --limit 1 \
  --set cmd='contrib/release/stage $YARDR_STAGE' \
  --set log="$HOME/.yardr/logs/release.log" \
  --set env_file=~/.yardr/release.env
yardr route add --stage build --group yardr-release --depot yardr --originator any
yardr route add --stage verify --group yardr-release --depot yardr --originator any
yardr route add --stage publish --group yardr-release --depot yardr --originator any
yardr route add --stage sign --group decisions --depot yardr --originator any
```

The group runs the depot's own script, one release at a time; the stage in
`cmd` says which of `build`, `verify` or `publish` it is. The script and the
people bill no subscription, so every route takes `--originator any`: a
release bead of any user is worked, not only the operator's. The build tags
the base and waits for CI's draft release, then builds the darwin archives
itself and adds them to it; it refuses a checkout that is not clean at the
remote's `main`, so the script it runs is `main`'s.
`sign` goes to the manual group mckean is in, whose Done says the signature
is uploaded; `backlog` and `decide` ride the depot's existing routes. The
group runs where `jq`, `git`, `gh` (logged in) and `minisign` are, and on a
Mac that holds the Developer ID, with Go and the Xcode command line tools:
the darwin binaries are signed and notarized there, and `build` refuses
before it tags until the `env_file` above names the identity and the notary
profile (`YARDR_RELEASE_SIGN_IDENTITY` and `YARDR_RELEASE_NOTARY_PROFILE`;
[install.md](install.md#signed-darwin-binaries) has the file and what the
Mac needs).
