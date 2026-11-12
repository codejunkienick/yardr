# Survey the daemon's round for redundancy

Design yardr-t4vs, proposed 2026-11-12. A proposal as it was decided on, not a
description of what the code does today; the decision is on the bead.

## 1. The question

Mckean asked for a list of cuts in the daemon's pass that he can pick from,
each one a mechanism removed or two made one, not lines packed tighter. This
is the first survey of the refactor process he agreed to on 11 Nov 2026
(mail 182653, 182662, 182673): survey per area, he picks at decide, and each
chosen cut becomes an ordinary `kind:refactor` bead. This survey covers the
daemon's round only. It proposes no code change in this bead.

A *pass* is one `Dispatcher.Reconcile` in
`internal/dispatch/dispatch.go:872`.
The *daemon* is the `yardr daemon` process running that pass; the *store* is
the single bbolt file the server owns, reached by the daemon over the yard's
unix socket. A *session* is one agent working one bead in one stage, or one
crew member's always-on agent. *Herdr* is the terminal runtime that starts
and reports those agents; the daemon asks it about each agent through the
herdr command. A *transcript* is the agent's own session file, where its
model, context use and token count are read from.

## 2. What exists today

Everything in this section can be opened. Line numbers are at the branch tip
this document was written on, which is the depot base `8d9ae16ac` plus this
document alone.

### 2.1. One pass, and what wakes it

`Dispatcher.Run`
(`internal/dispatch/dispatch.go:597`)
reconciles once on start, then once per store wake, per runner wake, and per
`Interval` tick (30 seconds by default). A wake only triggers a pass; every
pass re-reads state from the store and acts idempotently, so a missed
notification costs a delay, never a lost bead. Between passes Run waits on
the earliest due the last pass noted (`nextDue`,
`internal/dispatch/wake.go:9`:
timer, look, push, siding and gocache dues), with a one-second floor so a due
that cannot be fired is not spun on, beside the interval tick, a peer poll
(5 seconds), an hourly prune and a binary check (10 seconds).

`Reconcile` runs about twenty-five named steps in sequence: limits-clock
stamp and refresher nudge, start drains, home-snapshot begin, pane and
answerers caches, groups, reap, usage, teardown, cleanup, timers, the shared
bead and route listing, waits, sidings, quiet hooks, the upgrade gate, crew,
hook rules, CI, issue moves, pushes, peer bases, route, start settles,
backups, gocache removal, telemetry, peers, fetches, home-snapshot end and
the pass stamp. Each step is marked by name as it begins
(`internal/dispatch/passtime.go:82`),
and a pass longer than one interval logs one `slow pass` line naming where
its time went. That line is the cheapest ranking tool the daemon already
has; this survey did not need a new one.

### 2.2. What one pass asks herdr, per session

Reap judges every live bead session
(`internal/dispatch/reap.go:179`):
it lists the live sessions from the store, asks each session's runner for
its status in parallel workers (at most eight at once), then applies the
outcomes in session order through the session's limits, its pending prompt
and its notes. The limits step
(`internal/dispatch/limits.go:79`)
also records, once per session and only in its first fifteen minutes, the
id of the agent's own session file
(`internal/dispatch/model.go:79`)
and the model the agent reports
(`internal/dispatch/model.go:99`).

The crew step keeps every crew member's session
(`internal/dispatch/crew.go:104`,
`internal/dispatch/crew.go:233`)
in parallel workers of the same shape: status first, then, for a healthy
session, the first-prompt watch, rotation, context use on an idle pass
(`internal/dispatch/crewcontext.go:43`)
and the inbox. Both reap and crew read panes through one snapshot per pane
per pass (the `PaneCache`,
`internal/dispatch/panecache.go:18`,
filled by the runner's pane read), so the many pane checks of a pass —
prompt, input, dialog, login, harness error, stillness — share one
`herdr pane read` per pane.

On the runner's side, four calls read the agent's own session file, and
each one first finds that file. `Herdr.Status`
(`internal/runner/herdr_status.go:26`)
runs `herdr agent get <name>` and, for a working agent of a kind with known
error lines, reads the pane's end to judge whether the harness stopped.
`Herdr.Model`
(`internal/runner/model.go:43`),
`Herdr.SessionID`
(`internal/runner/model.go:67`)
and `Herdr.Context`
(`internal/runner/context.go:34`)
each run `herdr agent get <name>` again through `sessionKind`
(`internal/runner/model.go:173`),
and, when herdr names no session id, find the file by the session's
worktree through `matchWorkspace`
(`internal/runner/model.go:98`),
which lists the kind's whole session directory and opens every candidate:
`fileStart`
(`internal/runner/model.go:140`)
reads a candidate to its first timestamped line, and only a candidate that
started in the session's window is read again by `fileNamesWorktree`
(`internal/runner/model.go:157`)
to its worktree line or to the end of the file. `Herdr.Usage`
(`internal/runner/usage.go:72`),
which counts a stopped session's tokens, does the same worktree match when
the handle kept no session id, then reads the whole file once. The line
reader under all of them is `eachLineFrom`
(`internal/runner/model.go:246`).

Two earlier cures already cut this down, and their shape is the pattern the
candidates below extend. Yardr-16y0 gave `Context` a memory across passes:
the matched transcript is pinned while its file stands
(`pinnedContext`,
`internal/runner/context.go:135`),
and a transcript whose size and time are unchanged is not opened again,
while a grown one is read from where the last read ended
(`cachedContext`,
`internal/runner/context.go:278`).
Yardr-f5yh gave the pass one resolution of the yard's limits answerers
(the `AnswerersCache`,
`internal/dispatch/answererscache.go:24`,
read through `passAnswerers`,
`internal/dispatch/harness_limits.go:175`),
because route choosing judges every condition of every waiting bead and
each resolution lists every source and parses every declaration. The pass
itself never runs a limits command; a background refresher keeps the
answers (limits_refresh.go), and the pass reads only its memory through
`cachedLimits`
(`internal/dispatch/harness_limits.go:191`).

### 2.3. What one pass asks the store

The head of the pass lists the yard's groups once
(`internal/dispatch/dispatch.go:901`).
After that, the steps list overlapping slices of the same tables, each as
its own call over the socket, each decoding what it returns in the server:

- reap lists the live sessions (`Store.Sessions`,
  `internal/dispatch/reap.go:180`);
  usage lists the stopped sessions due a token count (`Store.Uncounted`,
  `internal/dispatch/usage.go:113`);
  teardown lists the ended sessions whose resources are still up
  (`Store.Unstopped`,
  `internal/dispatch/teardown.go:23`);
  cleanup lists stopped sessions of closed beads with a workspace left
  (`Store.ClosedWithWorkspace`,
  `internal/dispatch/dispatch.go:1088`).
- When a manual group waits or a siding can fold, one listing of every open
  bead is shared by waits and sidings (`passLists`,
  `internal/dispatch/sidings.go:52`),
  which also lists the routes and reads the settings. Route then lists the
  ready beads (`Store.Dispatchable`,
  `internal/dispatch/dispatch.go:1114`),
  lists the routes again
  (`internal/dispatch/dispatch.go:1121`),
  reads the settings again
  (`internal/dispatch/dispatch.go:1128`),
  asks which open beads are stranded in a stage their flow lacks
  (`Store.Stranded`,
  `internal/dispatch/dispatch.go:1141`),
  and lists the unstopped sessions a second time in the same pass through
  `liveUnstopped`
  (`internal/dispatch/teardown.go:137`,
  `internal/dispatch/teardown.go:138`).
  The settings are read twice more in the same pass besides: by the crew's
  job builder for the operator's name (`operatorName`,
  `internal/dispatch/crew.go:225`,
  `internal/dispatch/crew.go:226`)
  and by backups
  (`internal/dispatch/backups.go:35`).
  Inside the store, each of the bead listings decodes every open bead row
  again (`openBeads` in beads.go is cached only within one call's
  transaction), and `Stranded` resolves each bead's flow, reading a depot's
  repository flows with git where the flow lives there.
- For each ready bead it would start, route reads the bead whole, its
  family, its links, its failure count and its flow's next stages and map
  (`nextStages`,
  `internal/dispatch/dispatch.go:1397`,
  which calls `Store.NextStages` and `Store.FlowMap` as two calls, each
  resolving the same flow and, for a repository flow, the same checkout),
  checks its return backoff from its latest events (`backingOff`,
  `internal/dispatch/backoff.go:24`,
  200 events), and only then claims it. A session the pass sees for the
  first time costs one more event read, for whether it was last reported
  blocked (`wasBlocked`,
  `internal/dispatch/reap.go:438`).
  A crew session whose trust history is not yet remembered costs the
  member journal's whole event list and then every later event
  (`crewWorked`,
  `internal/dispatch/crew.go:546`).
- The tail steps each list their own queue whether or not anything is in
  it: hooks and train beads for CI
  (`internal/dispatch/ci.go:218`,
  `internal/dispatch/ci.go:225`),
  the push queue (`Store.Pushes`,
  `internal/dispatch/pushes.go:58`),
  the peers and the yard's key file (`Store.Peers`,
  `internal/dispatch/peers.go:68`;
  the key is read on every pass by design, in case it was made or loosened
  since), and the rotated cache directories
  (`Store.GOCACHEOldDirs`,
  `internal/dispatch/gocache.go:76`).
  The hook rules read only the events since the last pass
  (`internal/dispatch/rules.go:248`),
  which is the one tail step already shaped as a cursor, not a listing.

The pass also keeps two in-process snapshots beside the store. The home
snapshot (homesessions.go) clones every herdr session's handle map into the
pass's map so the daemon can close what it opened if its home is removed
under it; reap and crew fill it as they go. The per-session memory maps on
the dispatcher (status, idle spells, drafts, dialogs, logins and the rest)
are copied into a per-session clone for each reap and crew worker and
merged back after the join
(`internal/dispatch/reap.go:76`,
`internal/dispatch/crewkeep.go:134`).

### 2.4. What the live profile shows

Yardr-0bgq has landed: the daemon serves Go profiles on its own unix
socket when `YARDR_DAEMON_PPROF` names the path
(`internal/pprof/pprof.go`).
Mckean set that variable and reinstalled the daemon on 12 Nov 2026, so
this survey's ranking is by profile, not by reading alone. Two
consecutive sixty-second CPU profiles were taken from the live daemon
(pid 2334, build `09fa95be0` per `~/.yardr/daemon.pass`) at 11:50 and
11:52 CET on 12 Nov 2026, through
`curl --unix-socket ~/.yardr/daemon-pprof.sock 'http://x/debug/pprof/profile?seconds=60'`,
while four bead sessions were running (three builders and this draft).
The profile files themselves are not committed; the numbers below are
quoted from `go tool pprof -top -cum`. The runner files the profiles
name are unchanged between the branch tip's base (`8d9ae16ac`) and the
live build, so the file and line claims at that base hold for the
profiled code.

Both profiles tell the same story. The first gathered 5.03 seconds of
samples in 60.03 seconds (8.38% of one core); the second 6.15 seconds
in 60.09 seconds (10.23%). In each, about four fifths of all sampled
time sits under one stack: `keepCrewSession → crewRunning → crewContext
→ Herdr.Context` (`internal/dispatch/crewcontext.go:53`,
`internal/runner/context.go:34`) at 82.1% and then 79.0%. Inside
`Context`, `filepath.Glob` alone takes 64.0% and then 70.7% of all
samples, split three ways: validating a pinned transcript at
`internal/runner/context.go:146` inside `pinnedContext` (41.4%, then
54.0% cumulative), the worktree match `matchWorkspace` at
`internal/runner/model.go:103` (23.9%, then 12.2%), and finding a known
session's file through `sessionFile` at `internal/runner/model.go:228`
(11.5%, then 8.0%). Reading the candidates the match opens
(`fileStart`, `fileNamesWorktree`, `eachLineFrom`) makes up most of the
rest of that stack. No other step of a pass registers above a few
percent in either profile: `Herdr.Status` is 1.4% in the first, and the
store calls the other candidates name (`ListBeads`, `Notes`, and the
rest of `rpc.Client.call`) are each at or below about 3%. That does not
make those candidates free — a CPU profile cannot see time the daemon
spends blocked waiting for a `herdr` process it started, which is
C1's cost, and a window with few ready beads barely exercises the
routing steps C3 to C6 name — but for sampled CPU the order is not
close. The earlier measurements agree with it: note 23900 on
yardr-nv94 showed the same `crewContext → Context → matchWorkspace`
stack in kernel open, stat and directory listing, and the yardmaster's
notes on this bead put a pass at 3 to 4 seconds of CPU with sessions
alive and about 2.1 seconds with none, nothing between passes.

## 3. The proposal

The smallest version that answers the question is this document plus the
operator's pick at decide; no candidate below is proposed as already
decided. Each candidate is one mechanism removed or two made one. They are
ordered by what each removes from a pass's cost first and by product lines
removed second, as the bead asks: the cost order is the live profile's
(section 2.4), and where the profile cannot see a cost — time blocked on
a process started, or a step this window barely exercised — the reading
that follows each candidate says so and the candidate keeps the place
that reading earns it. Net lines are estimates of non-test Go
lines at this tip (negative removes lines), by reading the code named;
every builder re-measures with `sh .yardr/test sizes` before starting,
because `internal/dispatch/dispatch.go`
stands at its 1501-line ceiling and any cut that grows it needs a move
first, as yardr-0bgq's did.

### C2. The transcript match reads each candidate once, and a pin is checked without listing its directory

*Where.* `matchWorkspace`
(`internal/runner/model.go:98`)
with `fileStart`
(`internal/runner/model.go:140`)
and `fileNamesWorktree`
(`internal/runner/model.go:157`);
`pinnedContext`
(`internal/runner/context.go:135`)
with `contextFileStates`
(`internal/runner/context.go:199`);
and `sessionFile`
(`internal/runner/model.go:227`),
which `Context` calls for a session herdr already named
(`internal/runner/context.go:83`)
and `Model` and `Usage` call the same way. This is the profile's first
candidate: the three glob sites above are 64.0% and then 70.7% of all
sampled time (section 2.4), the pin check alone 41.4% and then 54.0%.

*What it duplicates.* Every candidate file is opened twice by the match:
once to its first timestamped line, and, if that start is in the window,
again to its worktree line or the end of the file. After yardr-16y0 the
pin removed the match itself on later passes, but checking the pin still
lists the whole session directory and stats every other candidate on every
`Context` call, to catch a second candidate appearing — a directory
listing and one stat per transcript, per idle crew session, per pass, for
as long as the session lives. The session directories hold every transcript
the agent ever wrote there, so this grows with the yard's age, not with the
pass's work. A third glob of the same kind rides along: when herdr
already names the session, `sessionFile` still expands the kind's
pattern — wildcards and all — to find the one file it names (11.5%,
then 8.0% of the profiles), where the pin, or one stat of the pattern
with the id filled in where the filesystem allows it, already answers.

*What replaces it.* Two readings made one, and the pin's check made
cheaper without losing any way a match can change. The match reads
each candidate in a single pass that collects both its start and whether
it names the worktree (both are `WorkspaceIn` lines of the same file), so
`fileStart` and `fileNamesWorktree` become one helper. The pin is then
validated without a glob. At pin install (`pinContext`,
`internal/runner/context.go:178`) the pin remembers, beside the
candidate files it already remembers: the pinned file's path, each other
candidate's size and modification time (as `contextFileState` already
records, `internal/runner/context.go:119`), and the path and
modification time of every directory a matching file could appear
through. That set is found once, at install, by globbing each directory
prefix of the kind's pattern — for Muse
(`harnesses/muse.yaml:106`, `sessions/*/*/*/{session}/session.jsonl`,
read with `{session}` as `*`) the prefixes `sessions`,
`sessions/*`, `sessions/*/*`, `sessions/*/*/*` and
`sessions/*/*/*/*`, the last being the session-ID directories the file
itself stands in — and statting every directory those globs return,
empty ones included. The per-pass check then stats only what it
remembered: the pinned file to see that it still stands (it may grow,
as today), each other candidate, and each remembered directory. No
directory is listed and no candidate is opened on that path. An
existing candidate can become a match as it is written, without the
path set changing — the case the code at
`internal/runner/context.go:119` names and the test at
`internal/runner/context_test.go:524` drives — so its own stat, not a
directory's time, is what catches it; a directory's time alone would
keep returning the pin where today's matcher reports ambiguity, and
this candidate does not propose that. A new or deleted candidate
creates or removes an entry in a remembered directory — the
session-ID directory for Muse, the date or project directory the file
stands in for the other kinds — and moves that directory's time.
That includes the case ancestor stats alone miss: a session-ID
directory that already stood empty when the pin was installed is
itself remembered, so writing its `session.jsonl` afterwards moves
its own time though it is no ancestor of any remembered file. A file
under a branch that stood empty at an earlier level is caught the
same way, because that branch's directory is remembered too, and a
wholly new branch is caught by its parent: the topmost directory it
creates stands in a directory that existed at install and was
remembered (the fixed prefix or a directory a prefix glob returned),
so creating it moves that parent's time. Only when the pinned file no
longer stands, or an other-candidate or remembered-directory stat
disagrees with the pin, does the pin fall back to today's full glob
and re-match, and the ambiguity rule itself does not change. Where a
pin already names the file, a known id reads that file instead of
globbing for it; `sessionFile`'s glob stays only for a session with
neither a pin nor a fillable pattern.

*Expected net product lines.* About +10: one of the two candidate readers
goes, but the pin's per-candidate state stays, because the check above
still needs it, and the remembered-directory state — the prefix globs
at install and one stat per directory on the check — is stored and read
beside it. This candidate buys the profile's largest cost, not lines;
the builder re-measures with `sh .yardr/test sizes` and quotes the
figure in the note.

*What could break, and what holds it.* A filesystem whose directory
time does not move when an entry is added or removed, or whose time
resolution hides a quick add inside a remembered directory, would hide
a new candidate until the pinned file goes or another remembered stat
moves; the fallback keeps today's glob wherever any remembered stat
disagrees with the pin, and the directory-time risk is named for
decide rather than hidden. The remembered set is a snapshot: a
directory created after install is not itself remembered, but its
creation moves its remembered parent, so the pin drops and the
re-match remembers the new set — a pass between the creation and the
file inside it may re-pin against the directory as it then stands,
which is correct, because no matching file stands in it yet. Held
by the context tests in context_test.go, including the second-candidate
and shrunk-file cases yardr-16y0 added, and
`BenchmarkHerdrContextCached`. The builder keeps the existing
candidate-becomes-a-match case (a rewrite and new times on a file
already in the glob, with the path set unchanged,
`internal/runner/context_test.go:524`) passing through the new path,
adds a new-candidate case through the remembered-directory path, adds
the case this revision names — a session-ID directory created before
the pin is installed and left empty, its qualifying file written
afterwards, where the next reading drops the pin and reports the
ambiguity a full glob would report — and proves the fast path lists
nothing in a way that can tell: making the other candidates' contents
unreadable cannot prove it, because the pin check today
(`internal/runner/context.go:135`) reads no contents — it globs and
stats — so the proof instead makes the candidate directories
searchable but not readable (stat of a known path, file or remembered
directory, still works, a listing finds nothing) after the pin is
installed, where a glob would return a differing list and drop the
pin, and the new check still returns the reading.

### C1. One herdr resolution per session per pass

*Where.* `Herdr.Status`
(`internal/runner/herdr_status.go:26`,
the `agent get` at line 31), `sessionKind`
(`internal/runner/model.go:173`,
the `agent get` at line 178) called by `Model`
(`internal/runner/model.go:43`),
`SessionID`
(`internal/runner/model.go:67`)
and `Context`
(`internal/runner/context.go:34`);
on the dispatch side, reap's status ask, then `agentSession` and `model`
(`internal/dispatch/model.go:79`,
`internal/dispatch/model.go:99`,
called from `internal/dispatch/limits.go:98`)
for a bead session, and status, `crewAgentSession` and `crewContext` for a
crew session.

*What it duplicates.* The same agent's `herdr agent get <name>` — a process
started, its JSON parsed — two or three times for one session in one pass,
and the worktree match (a directory listing and an open of every candidate)
up to twice more in the session's first fifteen minutes. In steady state a
live crew session costs two `agent get` processes per pass for ever
(status in `keepSession`, and `sessionKind` inside `Context`), and a young
bead session costs three plus two matches. This is the fork-and-open cost
note 23900's kernel profile shows. The live CPU profile barely sees it
(`Herdr.Status` at 1.4%) because the daemon blocks while the process it
started runs; it is priced here in processes per pass, not in samples,
and it stands second on that count alone. Nothing about the answers differs
between the calls in one pass: the kind and session id herdr names for an
agent cannot change while the pass judges it.

*What replaces it.* The mechanism the pass already has for exactly this:
a per-pass cache on the pass's context, as `PaneCache`
(`internal/dispatch/panecache.go:18`)
holds one pane snapshot and `AnswerersCache`
(`internal/dispatch/answererscache.go:24`)
one answerer resolution. One `agent get` per agent per pass fills a small
record (kind, session id, status); `Status`, `SessionID`, `Model` and
`Context` read it, and the worktree match runs only when the record names
no id. A context without the cache (every caller outside a pass) resolves
directly, as the pane and answerer caches already allow.

*Expected net product lines.* About +40: a cache type beside the two that
exist, against the call sites that stop resolving. This candidate buys
cost, not lines.

*What could break, and what holds it.* A stale kind or id within one pass
would misread a session whose agent restarted mid-pass; the pane cache
accepts the same bound for the same reason (a pass is one moment's
judgement, re-read next pass). Held by `TestHerdrModelReadsTheAgentsSessionFile`
(model_test.go), `TestHerdrContextReadsTheLastUse` and the cached-context
benchmark (context_test.go), `TestHerdrUsageReadsTheSessionsCount`
(usage_test.go), and the dispatch suites that count runner asks
(crew_test.go, dispatch_test.go). The builder adds a case: two reads of one
session in one pass start one `agent get` process, proved with the
stand-in herdr script's call log, as the runner tests already count calls.

### C3. The pass lists the yard once: routes, settings and the ended sessions are read one time each

*Where.* Routes at
`internal/dispatch/sidings.go:85`
and `internal/dispatch/dispatch.go:1121`;
settings at `internal/dispatch/crew.go:226`,
`internal/dispatch/sidings.go:89`,
`internal/dispatch/dispatch.go:1128`
and `internal/dispatch/backups.go:35`;
the unstopped sessions at
`internal/dispatch/teardown.go:23`
and again through `liveUnstopped` at
`internal/dispatch/teardown.go:138`,
called from `internal/dispatch/dispatch.go:1150`;
and the open-bead decodes under `ListBeads`
(`internal/dispatch/sidings.go:69`),
`Dispatchable`
(`internal/dispatch/dispatch.go:1114`)
and `Stranded`
(`internal/dispatch/dispatch.go:1141`).

*What it duplicates.* One pass reads the routes twice, the settings four
times and the ended-but-unstopped sessions twice, and decodes every open
bead row three times in the server (the store's per-call cache dies with
each call's transaction). Each duplicate is a socket round trip plus a
bbolt read plus a decode of the same rows the pass already held. None of
the second readings can see anything the first did not: the pass is the
only writer of most of what it reads, and where another process wrote
meanwhile, the next pass — woken by that very write — reads it then, which
is the pass's standing bargain everywhere else.

*What replaces it.* The sharing `passLists` already does for waits and
sidings, widened to the pass: `Reconcile` reads groups, routes, settings
and the session listings once at its head and hands them to the steps that
take them today as arguments, as reap already takes the groups and route
already takes the active counts. `liveUnstopped` filters the teardown
listing the pass already made instead of listing again. The bead listings
stay separate calls — they answer different questions — but are named as
the remaining triple decode for a store-side follow-up, not hidden.

*Expected net product lines.* About −30: four call sites and one helper
go; the head of `Reconcile` grows by the reads themselves, in a file at
its ceiling, so the builder first moves a step out of dispatch.go, as
yardr-0bgq moved code out of ops.go.

*What could break, and what holds it.* A step that today sees a setting or
route changed by an earlier step of the same pass would see the pass's
start-of-pass value instead; the steps that write settings (none in the
pass) and routes (none) make this theoretical, and the claim path already
re-reads the one bead it claims. Held by dispatch_test.go's routing
suites (`TestBeadThroughStages`, `TestLimitsBlockersAndRouteSpecificity`,
`TestStrandedBeadIsReportedOnceAndNotRouted`), sidings_test.go and
waits_test.go; the builder adds a counting-store case, in the shape of
`TestOnePassReadsTheSourcesDeclarationsOnce` (quota_test.go), that one
pass calls `Routes`, `Settings` and `Unstopped` once each.

### C4. Next stages and the flow map are one store call, not two resolutions of one flow

*Where.* `nextStages`
(`internal/dispatch/dispatch.go:1397`):
`Store.NextStages` and `Store.FlowMap` timed as two calls at the end of
the function, each starting from the bead (nextstages.go reads the bead
and its repository flows again) and each answering for a repository flow
by reading the depot's checkout with git.

*What it duplicates.* For every bead route starts, the same flow is
resolved twice and the same repository read twice — once for where the
bead may go, once for the map of where it has been — a few steps after
`Stranded` resolved the same flows a third time for the same beads. The
answers cannot disagree: both describe the flow governing one bead at one
revision.

*What replaces it.* Two calls made one. Today the two calls make
separate reads and resolve the flow separately: `Store.NextStages`
(`internal/store/nextstages.go:56`) reads the bead, resolves its
repository flows and, inside its own read, resolves the flow again
before filling its answer, and `Store.FlowMap`
(`internal/store/flowmap.go:219`) does the same work again in a second
read before drawing the map from that second resolution. The cut moves
the map's construction — the stage loop and the bead's path, today in
`FlowMap`'s read — into the read `NextStages` already makes, drawn
from the one resolution that read holds, so one store call returns
both and the map rides on the existing answer. `Stranded`'s earlier
resolution is left alone in this cut; joining it too is C3's
store-side follow-up, not this bead.

*Expected net product lines.* About −40 in product code once the second
client call and its server entry go; `internal/rpc/client_gen.go` is
regenerated, never edited, as AGENTS.md requires.

*What could break, and what holds it.* The combined answer must stay the
answer each call gives today, including a bead whose flow is broken
(`bad_flow` skips one bead, never the pass). Held by
`TestJobCarriesNextStages`, `TestJobCarriesTheStageFile` and
`TestBadFlowSkipsOnlyThatBead` (dispatch_test.go) and the store's
flowmap and nextstages tests; because the call is in `docs/api.md`'s
generated list, the builder runs the golden update the API test names and
says so in the note. This is the one candidate that changes the socket
contract's shape, which is why it is its own bead and its own question.

### C5. A group's env file is read once per pass, not once per judgement

*Where.* `groupEnv`
(`internal/dispatch/harness_limits.go:85`),
which opens and parses the group's `env_file` on every call; called by
`routeQuota`
(`internal/dispatch/quota.go:232`)
inside the closure route choosing runs per group per ready bead, and by
the session and reset paths beside it. Yardr-nv94 named this leftover
explicitly: the per-pass costs left after yardr-f5yh, `groupEnv`'s read
per judgement among them, matter only times the pass count.

*What it duplicates.* One small file opened, read and parsed once for
every (ready bead × gated group) judgement of a pass, though its entries
cannot change between two judgements of the same pass in any way the pass
could act on — and, where the file cannot be read, the same failure
re-derived and re-worded each time. The answerers beside it were cured of
exactly this by yardr-f5yh; the env file was left.

*What replaces it.* The same per-pass cache, keyed by the file's path:
the first judgement of a pass reads the file, the rest read the memory,
in the `AnswerersCache`'s shape (a context without one reads directly).
The cache holds entries, never the path's contents beyond the pass, so a
person who fixes the file is seen next pass, as a fixed source is today.

*Expected net product lines.* About −10: the cache is the third of its
kind, which is itself a smell decide may prefer to cure by generalising
the two caches that exist into one per-pass memo (see the open questions);
either way the per-judgement open goes.

*What could break, and what holds it.* A secret in an env file would live
in the daemon's memory for one pass longer than today — it already lives
in every answerer's `Env` for the pass by the same route — and the cache
key is the path, never the contents, as `limitsEnvScope` already keeps
only a digest. Held by quota_test.go
(`TestQuotaConditionGatesOnTheGroupsOwnWindows`,
`TestQuotaMemoryIsSharedAcrossGroupsWithTheSameLogin`,
`TestLimitsAreAskedOnceInFiveMinutesAndOnlyForAGatedRoute`) and
harness_limits_test.go; the builder adds a case that one pass opens one
env file once for two beads, proved by making the file unreadable after
the first judgement, as C2's no-open case proves its read.

### C6. A bead's events are read once per pass, not once per question asked of them

*Where.* `backingOff`
(`internal/dispatch/backoff.go:24`,
200 events per ready bead that reaches it), `wasBlocked`
(`internal/dispatch/reap.go:438`,
one page per session first seen), and the waits stay reader
(`internal/dispatch/waits.go:35`,
1000 events per bead whose revision changed); beside them,
considered and left out of this cut, `crewWorked`
(`internal/dispatch/crew.go:546`,
the member journal's whole list, once per crew session per daemon life).

*What it duplicates.* Three questions of a bead — has this bead been
sent back, was this session last reported blocked, how long has this
bead waited — each fetch the same bead's recent events as their own
socket call and decode. A fourth question stands beside them and is not
part of this cut: `crewWorked` asks whether a crew session has ever
worked, from the member's whole journal
(`internal/dispatch/crew.go:546`), not from a bead's page. On a quiet
pass few of the three are asked; on the pass after a daemon
replacement, every live session asks `wasBlocked` at once (and every
crew session asks `crewWorked`, on its own read), which is the most
expensive pass the daemon ever runs, run at the moment it can least
afford it.

*What replaces it.* One per-pass, per-bead event read in the
`PaneCache`'s shape: the first question for a bead in a pass reads its
latest page, the rest read from it. `crewWorked` keeps its own whole-journal
read — it asks a different, once-per-life question — and is named here so
decide sees it was considered, not as part of the cut.

*Expected net product lines.* About +20, as C1: cost, not lines. The
restart-pass storm is the cost; a yard with few sessions sees little.

*What could break, and what holds it.* An event written by an earlier step
of the same pass would be missing from a later step's answer; today each
step re-reads, so a `session_blocked` written by reap is visible to the
same pass's later steps only where they re-read that bead, which none of
the three does. Held by `TestBlockedSessionIsReportedOnce`,
`TestBlockedSessionAcrossRestart` (dispatch_test.go), waits_test.go and
the crew trust cases in crew_test.go; the builder adds a counting-store
case that a pass asking two questions of one bead reads its events once.

### C7. Two stillness memories become one

*Where.* The runner's `stopped`
(`internal/runner/herdr_status.go:95`,
remembering each working pane's end and since when in `Herdr.stops`, read
on every status ask of a working agent), and the dispatcher's `stillIdle`
(`internal/dispatch/limits.go:360`,
remembering each working session's pane end and since when in
`Dispatcher.still`, read on every pass of a working session). Both read
the pane's last 20 lines (`stopLines`, `stallTail` — the same 20), compare
the text with the remembered one, and judge by how long it has stood.

*What it duplicates.* One idea kept twice, in two processes' halves of the
same judgement: a working agent whose pane stands still is not working.
The runner's copy exists to catch a harness that stopped on an error
herdr still calls working; the dispatcher's exists to count such a
session idle whatever herdr says. In one pass both read the same snapshot
(the pane cache makes it one herdr read), both compare the same text and
both keep the same pair of values. Neither reads the other's answer.

*What replaces it.* Two mechanisms made one, in the dispatcher, for
both kinds of session. The runner already answers `idle` for the
stopped-on-error case from inside `Status`; the dispatcher's
`stillIdle` is the general form of the same answer (any still pane,
error line or not, past the group's `still` limit), but today it judges
bead sessions only — its own comment says so
(`internal/dispatch/limits.go:351`). Crew sessions have no second
judgement waiting behind `Status`: in `keepSession` a crew session
enters its idle handling — context reading, the inbox, and the
harness-error handling at `internal/dispatch/crew.go:779` — only when
the status it is given is `idle` or `done`
(`internal/dispatch/crew.go:247`), and `Herdr.Status` applies the
runner's `stopped` check to crew handles too
(`internal/runner/herdr_status.go:26`). Simply removing `stopped`
would leave a crew agent whose harness stopped on an error reported
as `working` on every pass, never reaching that handling, so this cut
does not propose that. It keeps the dispatcher's judgement and
generalises it: one helper judges a pane (its end, how long it has
stood, a limit), used by `stillIdle` for a bead session at its group's
`still` limit and by `keepSession` for a crew session at the same
default (the member's own `still` setting where one is named, through
the same `limit` helper), keyed by each session's own identity in the
one `still` memory. A crew session judged still is then treated as
idle for the handling at `internal/dispatch/crew.go:779`, exactly as
a status of `idle` treats it today. Only then do the runner's narrower
memory, its per-status pane read for kinds with error lines, and the
`stopStill` constant go, and `Status` returns herdr's word. The
error-line distinction survives where it is used: `HarnessError` is
still read from the tail at the idle spell's start, as today.

*Expected net product lines.* About −60, the largest line removal here:
the runner's memory, its lock and its read leave herdr_status.go, and
the dispatcher gains the crew application of the judgement it already
makes (the shared helper and the `keepSession` wiring) in exchange.

*What could break, and what holds it.* A stopped-on-error session, bead
or crew, would be told of at the `still` limit (10 minutes by default)
instead of `stopStill` (1 minute): slower word of a stuck agent, in
both populations, in exchange for one mechanism. For a crew member
that delay is the difference between the yardmaster being seen as
stuck in a minute and in ten, so the trade is decide's, stated for
crew as well as for beads, not as a bead-only judgement. Held by
stopped_test.go and stall_test.go on the runner side, the dispatch
stall cases, and the crew suites in crew_test.go; the builder moves
the stopped cases to the dispatcher's table or says which it deleted
and why, adds a crew case (a crew session herdr calls working, its
pane showing a harness error and standing unchanged, reaches the
context, inbox and harness-error handling once the still limit
passes, and does not before it), and keeps a case that fails on the
old code (a working agent with an error tail is idle only after the
still limit) passing on the new, for a crew session as well as a bead
session.

### C8. The limits warm-up at daemon start goes; the first pass warms itself

*Where.* `warmLimits`
(`internal/dispatch/limits_refresh.go:393`),
run from `Run` before the first pass: it lists the groups and sources and
registers a want for every kind, provider and source the yard might judge,
so the first pass's gated routes read fresh answers.

*What it duplicates.* The registration every cached read already does for
itself (`registerLimitWant` from `cachedLimits` and `cachedSource`): the
warm-up is a second, speculative copy of that work, run whether or not
the first pass judges any of those keys, and holding each warmed want on
a lease until its first answer lands. It exists for one pass's benefit —
the first — and costs a group listing, an env read per group and a
refresher run per key at every daemon start, including every
self-replacement.

*What replaces it.* Nothing: the mechanism is removed, and the first pass
registers the wants it actually judges, as every later pass does. The
refresher, the cache and the leases all stay; only the speculation goes.
What a cold read then does is not what a stale read does.
`cachedLimits` and `cachedSource` register the want and return
`limitNone` without waiting for the refresh they started
(`internal/dispatch/harness_limits.go:191`), and `unknownQuota` keeps a
last-known answer only where the key has one: a report younger than ten
minutes is kept whole, and past that a window used up until a reset
still ahead is kept (`internal/dispatch/quota.go:272`). A key that has
never been answered has no last report at all — `known()` returns
no report and `LastKnown` is unset — so the route's quota is wholly
unknown and `Choose` fails open on it with nothing held back, where a
stale key still holds what it last knew.

*Expected net product lines.* About −70: the warm-up, its per-kind and
per-source helpers and its `Run` call leave limits_refresh.go. The
test-only `WarmLimitsForTest` helper (export_test.go) is reworked to
register and wait explicitly, not silently deleted with them; only
the one test below goes.

*What could break, and what holds it.* Not one gated route on one pass:
every key first judged in the first pass is cold at its judgement.
That is one key per gated group (its kind, provider and env scope),
one per source a route's condition names for itself, and the crew
kinds' keys the warm-up also registers
(`internal/dispatch/limits_refresh.go:393`) — each is wholly unknown,
with no last-known window to hold on, until its refresher answer
lands. An answer that lands between the start of the pass and a
later judgement in the same pass rescues that judgement, but none is
promised, so every ready bead gated on any of those keys in the first
pass after a daemon start — including every self-replacement — may
start a session a fresh answer would have held, and it judges fresh
only from the pass after its key's answer. The warm-up today does not
wait either; what it buys is a head start, the refresh already
running while the pass's head steps run, and this cut gives that head
start up. Held today by `TestWarmedLimitsGateFromTheFirstPass`
(quota_test.go), which the builder would delete as the behaviour it
pins goes — decide should read that deletion as the cost, stated
here, not discover it in the diff. Every other quota test holds the
steady state either way.

## 4. Left out

- No code change in this bead, as the bead requires. Every candidate is a
  proposal for decide; none is described as decided anywhere above.
- Nothing outside the daemon's pass. The server's read paths are
  yardr-149t's, and the store's own per-call costs (the open-bead decode
  C3 names, the flow resolution C4 joins) are proposed only where the
  pass's calls are the cause; a store-internal survey is a bead of its
  own if decide wants one.
- No new mechanism unless it removes two. C1 and C6 add a per-pass cache
  of an existing shape instead of a new idea, and each is flagged where a
  decider may prefer to generalise the existing caches instead (see the
  open questions). No candidate adds a setting, an interval or a flag:
  yardr-16y0's rule stands, that the reading should be cheap, not rarer.
- The wait loop's dues are not proposed for removal. Yardr-nv94 measured
  them innocent — the once-a-second waker was a store write from another
  process — and this reading found no due that fires without its step
  doing its work when woken.
- The per-session clones (`reapClone`, `crewClone`) and the home snapshot
  are not candidates. They cost allocation per session per pass, which is
  small beside one process start, and each carries a correctness bargain
  (no worker writes the pass's maps; the daemon can close its sessions
  with the store gone) that a cut would have to re-argue, not just
  re-price. They are named here so decide knows they were read.

## 5. Open questions

Each question is the operator's to decide, with the options, what each
costs, and the one I would take. Nothing above decides any of them.

**Q1. Which cuts go ahead, and in what order?** Options: (a) C2 first
(the profile's stack, four fifths of sampled time), then C1, then C3–C6
as ordinary refactor beads, C7 and C8 only if their stated trades are
accepted; (b) all eight, in the order of section 6; (c) only the
mechanism removals (C7, C8) and none of the cost cuts. Each cut is one
bead; the order in section 6 already respects the file conflicts. I
would take (a): it spends the first bead where both live profiles
measured the cost, and leaves the two judgement-trades (C7's slower
tell, C8's first pass) for an explicit yes.

**Q2. For C1 and C6, a third and fourth per-pass cache, or one generalised
memo?** Options: (a) a small cache of each existing shape beside
`PaneCache` and `AnswerersCache` (fast to build, two more types to keep);
(b) one per-pass memo type the four caches share (a bigger first bead,
one mechanism afterwards — the form this survey's own rule prefers).
I would take (b) if C1 and C6 both go ahead, and (a) if only one does.

**Q3. Is C4's combined call worth a change to the socket contract's
shape?** Options: (a) yes — two resolutions of one flow for one answer is
the duplication, and the generated client and `docs/api.md` golden are
re-run by the builder; (b) no — leave the two calls, and let C3's
store-side follow-up count the cost without curing it. I would take (a):
the contract gains nothing by answering one question in two calls.

**Q4. C7: is one stillness judgement, in the dispatcher for bead and
crew sessions alike, worth a slower word of a stuck agent (10 minutes
instead of 1, for a crew member as well as a bead session)?** Options:
(a) yes, one mechanism, with the dispatcher's judgement generalised to
crew sessions as C7 now proposes, so a stopped-on-error crew agent
still reaches its idle handling, only later; (b) no, keep both
mechanisms, and this candidate is refused, not deferred. I would take
(a), because the 10-minute `still` limit is already the yard's chosen
bound for the same judgement in its general form — but a yard that
feels the one-minute tell is the point, for its crew above all, should
refuse it plainly. This is not a bead-only trade: crew sessions have
no `stillIdle` behind `Status` today, and the candidate stands or
falls on replacing that path, as C7 states.

**Q5. C8: may the first pass after a daemon start fail open, wholly
unknown, on every gated route whose key that pass first judges?** A
cold key has no last-known answer to hold on (see C8), so this is not
the stale-key bargain. Options: (a) yes, remove the warm-up, and
accept that every ready bead gated on a cold key — each gated group's
provider under its env, each source a condition names, in the first
pass after every start and self-replacement — may start until the
answers land; (b) no, the first pass keeps the warm-up's head start,
and the warm-up stays. I would take (b). A cold read does not fail
open on a last known answer as a stale one does — it has none — so
that equivalence cannot carry (a). The warm-up costs a listing and a
registration per start and waits for nothing; what it buys is that
first-pass gating usually has an answer, and wholly unknown gating
across every cold key at every self-replacement is a larger price
than the mechanism it removes. A yard that would rather have the
lines back may still take (a) with the scope above in view.

**Q6. Should a fresh profile be taken after C2 lands, to re-rank what
is left?** The profile this ranking rests on is already taken (section
2.4), so this question is no longer whether to profile first. Options:
(a) yes — after C2 lands, take the same two sixty-second profiles and
let decide, or the yardmaster, re-order C3 to C8 by them before those
beads are shaped (costs one profile session on a yard with sessions
running); (b) no — C3 to C8 keep the order reading gives them here,
with the `slow pass` line as the check each builder quotes before and
after. I would take (b) unless C2's landing leaves the daemon still
above the yardmaster's per-pass figures: the remaining candidates were
each below a few percent in both profiles, so a re-rank would mostly
measure which steps a busier routing window happens to exercise.

## 6. The beads it would become

In the order they would land. Each is small enough that a builder finishes
it without asking; each says which existing test it extends before writing
a new function, as the depot's rules require. The order is a landing
order, not only a preference: the beads decide picks land one at a time
in this order, so any two that touch the same file are serialised by it
and must not be run in parallel against that file. Each bead below names
the same-file beads it follows; in particular C1 follows C2 on the
runner files, C7 follows C1 (on `internal/runner/herdr_status.go`, and
on the dispatcher's limits and crew callers) and C3 (on
`internal/dispatch/crew.go`), C6 follows C1 on `internal/dispatch/reap.go`,
and C4 and then C8 follow C3 — and C8 follows C4 — on
`internal/dispatch/dispatch.go`. A bead decide refuses simply drops out;
what remains keeps this order. The live profile (section 2.4) confirms
the first two places and does not separate the rest, each of which sat
below a few percent in both windows; their order below is by reading
and by file conflict, as before.

1. **C2 — Read each transcript candidate once, and check a pin without
   listing its directory.** Goal: the worktree match opens each candidate
   once; a pinned transcript is validated by the pinned file still
   standing, a stat of each remembered other candidate (so one that
   becomes a match by being written, with the path set unchanged,
   still drops the pin), and a stat of each directory remembered at
   pin install — every directory a prefix glob of the kind's pattern
   returned, empty session-ID and empty branch directories included,
   so a new or deleted candidate, and a qualifying file written into
   a session-ID directory that stood empty at install, drop it too —
   falling back to today's full glob and re-match when any remembered
   file or directory moved. A session whose file a pin already names
   is read from the pin instead of a fresh glob of its kind's pattern.
   Where to look:
   `matchWorkspace`, `fileStart`, `fileNamesWorktree`, `sessionFile`
   (runner/model.go), `pinnedContext`, `pinContext`, `contextFileStates`
   (runner/context.go), and the pattern's directory prefixes from the
   kind's glob (for Muse, `harnesses/muse.yaml:106`). Done when: the
   context tests pass with the candidate-becomes-a-match case
   (`internal/runner/context_test.go:524`, a rewrite of an
   already-globbed file with the path set unchanged) and the
   shrunk-file cases held through the new path, a new-candidate case
   passes through the remembered-directory path, a case that creates
   the session-ID directory before the pin is installed, leaves it
   empty, and writes its qualifying file afterwards passes with the
   pin dropped, the no-listing proof of C2 (searchable but unreadable
   candidate directories) passes, the benchmark stands in the note
   beside its before figure, the note quotes a fresh sixty-second
   profile's `Context` share beside the 79–82% above, and
   `sh .yardr/test` passes. Touches runner/model.go and
   runner/context.go; must land before C1, which touches both.

2. **C1 — Resolve one agent once per pass.** Goal: one `herdr agent get`
   per agent per pass, read by status, session-id, model and context
   through a per-pass cache in the pane cache's shape (or the generalised
   memo of Q2). Where to look: `Status` (runner/herdr_status.go),
   `sessionKind`, `Model`, `SessionID` (runner/model.go), `Context`
   (runner/context.go), and their dispatch callers in reap, limits and
   crew. Done when: a runner test proves two reads of one session in one
   pass start one `agent get`, the suites named in C1 pass, and the note
   quotes a live yard's `slow pass` split before and after. After C2, on
   the runner files both touch; before C7, which touches
   `runner/herdr_status.go` (`Status` reads the per-pass record this
   bead fills) and the same dispatch callers in limits and crew, and
   before C6, which touches reap's caller in the same file. Those beads
   follow in this order and do not run in parallel with this one on
   those files.

3. **C3 — List the yard once per pass.** Goal: routes, settings and the
   unstopped sessions are each read once per pass and handed to the steps
   that take them; `liveUnstopped` filters the teardown listing. Where to
   look: `Reconcile` and `route` (dispatch.go, at its ceiling — move a
   step out first), `passLists` (sidings.go), `teardown` and
   `liveUnstopped` (teardown.go), `operatorName` (crew.go), `backups`
   (backups.go). Done when: a counting-store test in
   `TestOnePassReadsTheSourcesDeclarationsOnce`'s shape proves one call
   each per pass, and the routing suites pass. Third in this order:
   touches dispatch.go (before C4 and C8, which touch `nextStages` and
   `Run` in the same file — its move out of dispatch.go gives them
   room) and crew.go through `operatorName` (before C7, which touches
   `crew.go`'s `keepSession`); it does not run in parallel with any of
   them on those files.

4. **C5 — Read a group's env file once per pass.** Goal: `groupEnv`'s
   answer is cached per pass by path, in the answerers cache's shape (or
   the Q2 memo). Where to look: `groupEnv` (harness_limits.go),
   `routeQuota` (quota.go), the session and reset callers. Done when: a
   quota test proves one open per pass for two beads, and the quota
   suites pass. Fourth in this order; it shares no file with C2, C1 or
   C3, and through Q2's shared memo it lands after whichever of C1 or
   C6 builds that memo — in this order, after C1 where C1 builds it.

5. **C4 — Answer next stages and the flow map in one store call.** Goal:
   `Store.NextStages` returns the flow map drawn from the same
   resolution; the dispatcher's second call goes. Where to look:
   `nextStages` (dispatch.go), nextstages.go and flowmap.go in the
   store, the generated client (`go generate ./internal/rpc`), and the
   API golden. Done when: the dispatch and store suites named in C4
   pass, the golden is regenerated by its test's command and named in
   the note, and `sh .yardr/test` passes. Fifth in this order: after C3
   and before C8, both on dispatch.go (`nextStages` here, `Run` there);
   it does not run in parallel with either on that file.

6. **C6 — Read a bead's events once per pass.** Goal: the backoff,
   blocked and wait questions of one bead in one pass share one event
   page, in the pane cache's shape (or the Q2 memo). Where to look:
   `backingOff` (backoff.go), `wasBlocked` (reap.go), `waits`
   (waits.go). Done when: a counting-store test proves one read per bead
   per pass across two questions, and the blocked and waits suites pass.
   Sixth in this order: after C1, whose reap caller it shares
   `internal/dispatch/reap.go` with (and after C1 also where Q2
   generalises the memo); it does not run in parallel with C1 on that
   file.

7. **C7 — Keep one stillness judgement, for bead and crew sessions.**
   Goal: the runner's stopped-on-error memory and its per-status pane
   read go; a working session whose pane stands is counted idle by the
   dispatcher's one `still` judgement, generalised in `keepSession` to
   crew sessions (which today reach idle handling only through
   `Status`), so a stopped-on-error crew session is treated as idle for
   its context, inbox and harness-error handling at the still limit.
   Where to look: `stopped` (runner/herdr_status.go), `stillIdle`
   (dispatch/limits.go), `keepSession` and the idle handling at
   dispatch/crew.go:779, `crewStatus` (dispatch/crew.go:873). Done
   when: the stopped cases stand in the dispatcher's table at the still
   limit or are deleted with the reason in the note, the crew case of
   C7 passes (a crew session called working with an unchanged error
   tail reaches that handling only after the still limit), a case for
   a bead session and a crew session that fails on the old code passes
   on the new, and `sh .yardr/test` passes. Seventh in this order:
   touches runner/herdr_status.go, dispatch/limits.go and
   dispatch/crew.go, so it lands after C1 (which fills the per-pass
   record `Status` in herdr_status.go reads, and touches the limits
   and crew callers) and after C3 (which touches crew.go through
   `operatorName`), and does not run in parallel with either on those
   files; the `Status` it leaves returns herdr's word, which is what
   C1's record then carries. Refused, not deferred, if Q4 answers no.

8. **C8 — Remove the limits warm-up.** Goal: `warmLimits` and its helpers
   go; the first pass registers the wants it judges, as every pass does,
   accepting the wholly unknown first-pass gating C8 states (no
   last-known answer on any cold key). Where to look: `warmLimits`
   (limits_refresh.go), its call in `Run` (dispatch.go), and
   `WarmLimitsForTest` (export_test.go), reworked to register and wait
   explicitly. Done when: `TestWarmedLimitsGateFromTheFirstPass` is
   deleted as the behaviour it pins goes, the remaining quota suites
   pass (on the reworked helper where they used it), and the note says
   the first-pass trade in Q5's words, with its full scope. Eighth and
   last in this order: touches dispatch.go's `Run`, so it lands after
   C3 (`Reconcile` and `route` in the same file) and after C4
   (`nextStages` in the same file), and does not run in parallel with
   either on that file; where C3 or C4 is refused it follows whichever
   selected bead last touched dispatch.go, and alone only where none
   did.

No bead here depends on a new profile being taken first (Q6 asks
whether one is taken after C2, not before it), on a new
setting, or on any other bead outside this list. If decide picks none of
them, this document is the record that the round was read, and the
`slow pass` line remains the instrument that would reopen it.
