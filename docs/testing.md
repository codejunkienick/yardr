# The test rules

Seven rules keep the suite fast, each with the number that justifies it
(docs/design/2026-10-16-yardr-635g-test-time.md measured them). The gate is
the depot's test command, `sh .yardr/test`: builders and reviewers run it in
their worktree, without `-race`; the assembly runs it with `YARDR_LANDING=1`
on what lands, with it (R4). R1 and R2 are script behavior: R2 is enforced
on every run, R1 on every race leg; the rest are rules the gate, the roles
and the assembly keep between them.

An allow-list in `.yardr/test` names what is over budget today. Each entry
says why; entries with a fix name the bead that brings them under. A fix
deletes its entries; a row whose site is gone is deleted with the fix, not
left for later. Two rows have a fix bead: `internal/cli`'s (yardr-2xke) and
`contrib/assembly`'s (yardr-4n6h). One row has none: `contrib/isolated-yard`'s,
since the yard-shape matrix is long by design and runs only in the quiet run
(R6).

R1. A package's tests finish in 60 s under `-race`, or the gate fails.
Only a race leg is judged, and only the landing and the quiet run (R6) have
one (R4): a builder's or reviewer's run, and a landing that changed nothing
compiled (R5), test their selection without `-race` and so reach no R1
verdict. A package that slips over 60 s shows at its landing, not before.
The wall is the slowest package. When a non-allow-listed package takes over
60 s in the race leg, the gate runs it again alone under the leg's lock.
Several over-budget packages are re-run up to three at a time, and every
one is judged by the single load pair sampled around the batch. With
`YARDR_R1_STRICT` set, which only the quiet run does (R6), they are re-run
one after another instead, each judged by the pair sampled around its own
re-run, so a package's line names the load that package sat in: three race
suites side by side make a load and times of their own, and a time over
60 s must be the package's own to judge it by (yardr-t4c0).
Over 60 s alone fails the run and names both times; 60 s or less passes
with a `test: R1:` line naming both times and marking the first run as loaded.
Load multiplies package times up to ~2.5x, so the first run's time by
itself does not fail the gate.
The re-measurement can be loaded too: the race lock holds off the other
gate legs, not every test on the machine. Over 60 s alone on a loaded
machine -- the one-minute load average past the core count -- passes with
a `test: R1:` line naming both times and the load instead of failing
(yardr-wq5h), unless `YARDR_R1_STRICT` is set by the quiet run (R6): then
over 60 s alone on a loaded machine is refused the verdict, in a
`test: R1:` line naming both times and the load, and the run ends neither
passed nor failed (exit 3); over 60 s alone on a quiet machine fails as
before. The load is sampled when
the re-runs start and again when the last ends, and the machine is loaded when
either sample is (yardr-f5f7): the average lags, so the end sample alone
read a load that dipped before the check as quiet, while the start sample
holds the loaded minutes the re-run was about to sit in. The line names the
start sample's load where that is loaded, else the end's. Under the strict
verdict a refusal names both times and the load, a quiet-sample failure
both times, and a failure beside a refusal still fails the run. Unknown
load counts as quiet and fails as before. A landing's `-p` follows the same load: past the
core count the race leg runs `go test -race -p=N`, N the cores squared over the
load, rounded down (at least 2, at most the cores), and logs one `test: go test
-p=N (load A on C cores)` line; a quiet or unknown load runs go's default and
logs nothing (yardr-y6js).

R2. A test sleeps no more than 500 ms at a site, and a poll loop polls a
condition with a deadline. Exactly 500 ms complies; over fails unless
allow-listed. A test that waits on time takes a clock it sets: the fake
clocks the packages already own (the dispatcher's clock, which tests set;
`Receiver.SetClock` in hooks and yards; the peer clock in the dispatch
tests). A test that must use the real clock is allow-listed with its
reason, and the gate counts those entries. The gate reads literal
durations in `*_test.go` files; a sleep it cannot read, such as a
variable, is not judged and still owes the budget. A test waits for the
effect it asserts: it polls the condition (a store event, a file, a held
question, a ticket) with a deadline that is only a fuse against a hang,
never the measurement. In the contrib suites that fuse is written for an
idle box and goes through `testload.Scale`, which multiplies it by the
larger of two factors, each the machine's load over its cores from 1 to
4: the one the race leg is given (`YARDR_TEST_LOAD`; `.yardr/test` reads
it as the leg starts and names it in a line), and the machine's own live
load, which `testload` reads as the fuse is armed, in a plain run too
(`/proc/loadavg`, else `sysctl vm.loadavg`; a load it cannot read counts
1). So a fuse is sized to the load the suite runs in, not the load its leg
started in, and is not raised by hand after it flakes in a landing. The
deadline a test gives the program under test is that program's input, not
a fuse, and is not scaled. A wait on a program that makes a hundred process
starts, and so takes as long as the machine makes it (contrib/isolated-yard's
down), is a fuse against its standing still, not against its taking long: it
gives up when the program's own processes have not changed for the fuse
counted in looks, not seconds, since a load stretches the time between looks
without the program standing still, as `waitWhileDown` does there. Where the
code owns the time the test waits on,
the test sets that clock -- the fake clocks named above -- and advances
it. A deadline taken from the load is the last resort, and only where
the thing waited on is a program outside the test (a harness binary, a
browser): there the bound is product code, its failure under load is a
product question, and the test stubs the bound long (as the doctor
tests stub thirty seconds, and `storetest.NameTestBinary` the ten seconds
a command waits for a server it starts, `YARDR_TEST_START_WAIT`) instead
of inheriting whatever load leaves of it. Where a test must still tell a
slow machine from a slow program, as contrib/shot's real Chrome does to
skip a miss of its deadline, it asks `testload.Loaded`: the factor above
1, the leg's or the machine's own as it is asked, so a plain run on a
loaded box is found loaded too, and a load that cannot be read is not. A
test whose result depends on that factor pins the machine with
`YARDR_TEST_LOADAVG` and `YARDR_TEST_CORES`, as the gate's tests do, so
that it does not turn on what else the machine is running. A count inside
a fixed wall-clock window, and a sleep followed
by asserting the slept-for event exists, are both the first shape
written wrong. A reviewer checks the pick without running anything, as
the sleep scan is checked: a diff that adds a test names one of the
three shapes for every new sleep over the scan, every count asserted
inside a fixed window, and every sleep-then-assert on the slept-for
event -- the polled condition and its fuse, the set clock, or the
stubbed product bound with its bead. The worked example is yardr-x51w's
peerhold fix (`internal/dispatch/peerhold_test.go`): wait for six polls
in whatever time the machine takes, and keep only the one elapsed-time
assertion (at least 200 ms over five 50 ms pauses) whose direction load
cannot falsify.

A test that waits for a process it killed asks `testload.Running`, or
`testload.GroupRunning` for a process group, never `kill -0`: a killed
process nobody has waited for is a zombie, and `kill -0` answers for a zombie.
Whatever adopted an orphan reaps it, except in a container whose first process
does not (`go` itself, in a `docker run` without `--init`, which the Linux
recipe below always passes), where it stays one for the rest of the run and the
wait runs out. Both helpers count a zombie as
dead. A check that a process the test did not kill is still alive may use
`kill -0`.

A fix for a test that fails only under load is proved the way it failed:
`contrib/burn/burn -n 100 ./internal/dispatch TestName` runs the test a
hundred times beside five times as many busy loops as the machine has cpus
(contrib/burn/README.md; `-x` sets the factor, 5 by default), first on the
code as it was, to see it fail, then on the fix, and the note names the
command, the counts it printed and the load average before and after. A fix
for a data race is proved the same way with `contrib/burn/burn -r`, which
builds the test binary with the race detector even if GOFLAGS contains
`-race=false`. Without `-r`, burn does not request the detector, though an
inherited `GOFLAGS=-race` can still enable it; use `-r` so the command says
the detector is part of the proof. The loops are niced, but they still raise
the load average, and the builder routes wait on `machine.load<80`, so that
default factor runs outside a session, when the line is idle. Inside a
builder's or reviewer's session the burn runs at `-x 1` and names the one
suspect test (`contrib/burn/burn -x 1 PACKAGE TEST`, with `-r` for a race),
the rule in `.yardr/depot.md`.

A failure seen at a load of several times the cpu count may not show in a
burn at `-x 1`, which runs as many loops as there are cpus. A fix therefore
has two burn proofs, and its note records both, each as done or outstanding:
the in-session one (the command, the counts and the load it printed) and the
full-factor one (the default `-x`, the same test and count). The
full-factor burn is run by whoever is idle outside a session, when the line
is idle: the yardmaster on ask, or the operator. Its command, counts and
load go in a note on the fix's bead, and a full-factor burn that fails files
a new bead. A fix lands on the in-session proof with the full-factor one
outstanding: the assembly gate runs no burns, so landing never waited on
them, and the note, not the gate, carries what is still owed.

A test can also fail on the first start of an executable file that was not
there before, such as a stand-in script it writes and runs or a binary it
copies. A burn cannot prove the fix for such a failure. On macOS the system
assesses an executable file the first time it is run. A process that pays for
that waits hundreds of milliseconds for each new file, and seconds on a busy
Mac (`internal/scripttest`'s package comment has the figures). A test that
starts several inside a deadline the product sets fails when the waits add up
to it, as yardr-xars, footplate-f2zx, footplate-yg50 and yd-rc1t did.

A builder's pane may not pay the cost. Likely reason, unverified: macOS skips
the first-launch check for processes started by a terminal on the Developer
Tools list, and a launchd job has no such parent. On 4 Nov 2026 a new
`#!/bin/sh` file took 2.4 ms to start from a pane (the median of 40 starts)
and 223-314 ms (median 252 ms) as a launchd job. So `go test -count`, a burn
and the gate run in a pane can all pass the old code. Burn runs the test binary
from the shell it is in, and its loops are cpu load. yd-rc1t's old code passed
1000 runs in one process and 200 under `contrib/burn/burn -x 1`. Whether every
session is exempt is open, since footplate-f2zx saw the cost in a pane on
23 Oct. So the pane proves nothing either way, and the recipe below is the
proof.

Prove the fix for such a failure as a launchd job on macOS, first on the code
as it was, to see it fail, then on the fix. Build the test binary beforehand
with `go test -c`, then submit it:

```sh
launchctl submit -l dev.yardr.<label> -o OUT -e ERR -- \
  <test binary> -test.run '^TestName$' -test.count=10 -test.v
# wait for PASS or FAIL in OUT
launchctl remove dev.yardr.<label>
```

The job is one stream, takes a few seconds and adds no load of its own.
launchd runs a submitted job again each time it exits, appending to OUT, until
the job is removed. Remove it as soon as OUT shows the result: a closing PASS
or FAIL, or a panic. Give the label a name of the run's own, the bead's id and
old or fix. The yard's services carry `dev.yardr.` labels too, and
`launchctl remove` stops whatever it is given. A job starts in `/`, with
launchd's short `PATH` and none of the pane's variables. A test that reads a
file of its package by a relative path needs the package directory, so put
`/bin/sh -c 'cd "$1" && shift && exec "$@"' sh <package dir>` before the test
binary.

A quiet Mac needs the deadline cut as well. The product's deadline is sized for
the seconds a busy Mac pays (`source.MintTimeout`, which bounds a source's mint
command, is 30 s). A quiet Mac pays about 150 ms a new file, so yd-rc1t's old
code passed there even as a job. Its test starts three new files and took half
a second. Build the test binary a second time with the deadline cut to a few
new files' worth of the quiet cost, which was 300 ms for that test. The old
code then fails at the cost a quiet Mac pays, with no load to raise it.

Make the cut with a throwaway build:
`go test -c -overlay <overlay file> -o <test binary> <package>`. The overlay
file maps the product's file to the copy with the deadline cut, both as
absolute paths:
`{"Replace": {"<the product file>": "<its copy with the deadline cut>"}}`.
Keep the copy outside the worktree and never commit it. Build the old code's
binary and the fix's with the same cut, so that the code is the only
difference. With it, yd-rc1t's old code failed 10 of 10 runs and its fix passed
10 of 10. The note names the label, the cut deadline and the counts for each.

A failure only Linux shows is proved the same way, in a container, by the
recipe in "Running a package's tests on Linux" below.

R3. A package over the R1 budget with over 100 tests runs its tests
parallel: `t.Parallel` on every test that owns its state, allow-list for
the rest. That binds the volume debt -- internal/dispatch, whose fix bead
parallelizes it following the assembly train-yard shape (temp homes, no
shared state); internal/cli was bound until yardr-ab6t threaded its output
and environment through its commands, and internal/store until yardr-2e9b
pinned the package clock to the tests that move time. Packages under
budget comply until they breach it, and the budget is their backstop then.

The allow-list for the rest has two tests, both in `contrib/assembly`:
`TestMergeLocalTestCommandStartsWithNoInheritedFile` and
`TestIntegrateTrainTestCommandStartsWithNoInheritedFile` (yardr-q9s4). Neither
calls `t.Parallel`, and their rows do. Each row forks a script that, under
dash, refuses to start the tests when a descriptor above 9 reaches it, and the
row marks every descriptor the test binary holds close-on-exec before it forks.
On macOS, Go marks an accepted connection close-on-exec only after `accept`
returns, outside `syscall.ForkLock`, so a connection that a yard server of the
package accepts as a row forks can reach the script. The package runs up to ten
tests per processor at once, each serving a yard to the `yardr` children it
starts, and nothing a test can take closes that gap, because it is inside Go's
accept. The runner finishes a test that does not call `t.Parallel`, rows and
all, before it resumes any test that does, so these two run while no other test
serves a yard. Linux has no gap (`accept4`), and the two run the same way there,
to keep one shape. That adds a second or two to the package's run, and no bead
brings it under. `TestFdTestsRunInTheSerialPhase` fails if either test calls
`t.Parallel` again.

R4. The depot test gate runs `go test -race` in its landing run
(`YARDR_LANDING=1`, the assembly's, on exactly what lands; for a pull request
from outside, pr-ci's run of its head, which no landing follows) and in the
quiet whole-suite run (R6). A builder or reviewer can request an earlier race
check with `contrib/burn/burn -r` (R2). A plain `.yardr/test` run by a builder
or reviewer tests the same selection without `-race`, says so in its first
line (`testing without -race (race runs at landing only, docs/testing.md
R4)`), and so never takes the machine-wide race lock for its race, nor
judges R1, which is defined under `-race`. A plain run whose step covers
the whole suite -- `go.mod` or `go.sum`, a file no row knows, or a selection
of at least 70% of the suite's packages -- still queues on that lock as class
1, behind landings (R5): two whole-suite runs at once overload the machine,
while a leaf run beside them is the normal tolerated load (yardr-f4tr).
The line was half the suite until yardr-13mx counted who waited: since
yardr-f4tr, 211 plain runs of half the suite or more had queued as class 1,
a median of 33 of 57 packages and only 18 of them 40 or more, and they
account for 11.4 of the 11.9 hours that builders and reviewers waited on the
lock. If two runs under the line overload the machine again, lower it.
Measured on 22 Oct 2026 over the last 200 beads'
notes: of 170 passing test lines, 66% of the gate's wall time (129k of
193k s) was queued on that lock, and 107 runs waited over half their run;
no bead or note among 1370 reports `WARNING: DATA RACE`. The detector still
stands in front of `main`: a race fails the landing without a rerun, and the
bead comes back to its builder. A builder who changes concurrent code and
wants the detector sooner runs `YARDR_LANDING=1 sh .yardr/test`, which
queues on the lock as a landing does.
A builder or review stage runs the plain suite once per sha. A stage whose
branch is unchanged since a green test line on that sha does not rerun it;
a stage that committed -- a review fix, a security fix -- still runs it on
its new sha. That covers a repeat builder pass too: a pass with no new
commit since a green line does not rerun (yardr-dx5f ran one sha three times
across a hold and unhold). Every note says `reran` or `names this tip`, so
the re-measure can tell a fresh run from a quoted line. A stage that
committed runs over its own commits alone, `YARDR_BASE=<the commit the last
test line names> sh .yardr/test`, so its selection is what those commits
changed and whatever imports or reads it (yardr-0cw8): a fix in a leaf
package takes seconds. A review base left behind by a rebase is compared
from the commit it and `HEAD` share, which lies before the tested commit,
so the run selects more, never less. If the named base is ahead of `HEAD`,
the merge base is `HEAD` and would select nothing, so the script says
"cannot compare" and the reviewer runs plainly from the session's base. The
assembly's runs are outside this rule: the landing always runs, on exactly
what lands, every time, including after a merger's rebase that changed
nothing
(`contrib/assembly/merge-local`: the test command "runs on exactly what
will land, every time"), and its landing gate differs from any plain
run (`whole()` in `.yardr/test`: the selection plus the six black-box
suites with `-race`, vet for linux too, the socket leg beside), so no
plain test line ever satisfies it. The test line carries the run's wall time
and race-lock wait, written by `.yardr/test` itself so every note holds
them: `test: sh .yardr/test @ <sha>: pass, <n> packages ok, 0 failed, 112 s,
waited 40 s`. The fields are the result, the packages passed and failed, the
run's wall seconds, and the seconds it queued on the machine-wide race lock:
`waited 0 s` on most builder and review lines -- a leaf selection takes no
lock -- and a wait on a landing's, or on a whole-suite plain run's that
queued behind one.

R5. The landing runs the incremental selection -- today's package pick --
plus always the six black-box suites (contrib/assembly, contrib/peer,
contrib/deploy, contrib/server-yard, contrib/isolated-yard,
contrib/release), plus the socket leg as today. Each of the six builds
the tree's binary and drives it without importing the changed packages,
so the pick alone misses them. A landing that changed nothing compiled
runs the plain selection alone, as before, without `-race`: no `.go` file,
embedded file or deletion is in it, so the binaries are the base's and only
a test that opens a changed file by path can differ, which the selection
holds. If that selection covers at least 70% of the suite's packages, its
plain go-test step queues as class 1; a smaller selection takes no ticket
(R4, yardr-f4tr). A landing whose change cannot be selected (go.mod or
go.sum, a deletion, a file no row knows) and one with nothing to compare
with still run the whole suite. The whole suite otherwise runs only when
the yard is quiet (R6 below).
One test runs for a change to its own files and for no other run.
`conformance/bdp`'s `TestProbeCoreFailurePaths` runs
`conformance/bdp/probe-core-test.sh`, the failure paths of BDP's conformance
probe. The script builds yardr, seeds a yard eight times over, and needs bash,
go, python3, curl, jq and node. The table's row for `conformance/` selects the
package, which nothing imports, for any path under it. When one is among what
changed, a deleted or moved path included, the gate sets `YARDR_CONFORMANCE=1`
for the run. Without the mark the test skips and says so, so `go test ./...`
(CI's plain jobs have no node), a whole suite chosen for another reason and
the quiet run (R6) do not run the script. A whole suite that a conformance
change shares with `go.mod` or a file no row knows is marked too. With the
mark, a missing tool fails the test, which names the tool and how to install
it, as the shellcheck step fails where shellcheck is missing. A skip would let
a broken seed through to CI's conformance job after the landing. The script
takes about half a minute, so the package has no R1 allow-list row. CI's
conformance job runs it too, and runs `probe-core.sh`, which needs BDP's
checkout and the network and stays CI's.
A landing's race leg takes the machine-wide race lock ahead of any other
run's (yardr-0cw8). A ticket is class 0 for a landing's race leg and class
1 for a whole-suite plain step, including an unraced landing's plain
selection; class 1 also accommodates tickets from a copy of the script
from before R4 (a branch not yet rebased). The first live ticket may take
the lock, so class 0 goes first and each class keeps its arrival order.
The run that holds the lock is never pre-empted. `sh .yardr/test
--race-queue` shows each waiter's class.

R6. The whole suite still runs, but when the yard is quiet, not on a timer
and not as a gate on any bead. The daemon's trigger
(`internal/dispatch/quiet.go`) subscribes to the event stream and judges
the yard on each event that can make it quiet or owe a run -- a bead
advancing, a bead closing, a session ending, or a change to
`quiet_landings` -- never by polling and never on a timer. A run for a
depot starts only when all hold at once:
no bead waits in `approved` for the assembly anywhere in the yard, no
bead session that runs something on this machine is live anywhere in
the yard, and at least `quiet_landings` code landings (ten while unset:
about two hours at the 60-140 landings a day of 14-16 Oct 2026) have
landed on that depot's base since its last judged quiet run started. The
sessions that count are the ones `max_sessions` counts
([groups.md](groups.md#how-many-sessions-the-machine-carries)): those of
a `herdr` or an `exec` group, a builder, a reviewer or an assembly
script. A `manual` group's claim holds a bead for a person (the backlog
alone holds dozens); a `peer` group's is a bead another yard works.
Neither runs anything here, so neither counts: counted, they would keep
the yard from ever being quiet (yardr-hqsn). A
threshold reached while the yard is busy stays in the log and fires at
the next quiet event. Quiet is the yard's, not the machine's: nothing
here waits for low load, and the run starts however loaded the host,
which is why it judges R1 strictly instead of loud-passing as a
landing may. The run tests the depot's base in a worktree of
its own with `YARDR_LANDING=1`, `YARDR_R1_STRICT=1` and
`YARDR_QUIET_RUN=1`, holding the
machine-wide race lock for its whole length, so it never overlaps a
landing leg, and it is
single-flight: while one quiet run runs, a further event does not start
a second, and the trigger re-arms only when the first finishes. A green
run is logged and told as `quiet_passed`, a red base as `quiet_failed`
naming the failing packages, the failing tests and the base commit, each
with its wall time and starting load. The strict setting, which only the
quiet run sets, turns off R1's loaded pass: a package over 60 s alone on
a loaded machine is refused the verdict instead of passing, in a
`test: R1:` line naming both times and the load, and the run ends neither
passed nor failed (exit 3). Such a run is told as `quiet_unjudged`,
beadless and of no attention class, carrying the refused packages with
their times and loads and the run's own output as `run.log`; the landings
are counted since the last judged run's start -- the last `quiet_started`
below the last `quiet_passed` or `quiet_failed` -- so a refused run leaves
the count; the trigger retries when the run ends if the yard is still
quiet. Three refusals in a row are told as `quiet_failed` instead, naming
the bound, so a machine that never quiets still accuses its base. On a
quiet host a package over
60 s alone fails as before, so a slowdown cannot hide behind a loaded host
(yardr-8gdt). The strict run also re-runs
the packages over budget one after another, not up to three at a time, so
that the time a package is judged by is its own and not what three race
suites side by side made of it, and the load a line names is the one
around that package's own re-run (R1, yardr-t4c0).

`YARDR_QUIET_RUN=1`, which like `YARDR_R1_STRICT` only the quiet run sets, is
the mark a test reads when it costs minutes and belongs to no other run.
Without it the test skips and says why, so `go test ./...`, a builder's gate
and a landing stay as fast as they were. Today that is `TestYardShapes`
(`contrib/isolated-yard/matrix_test.go`, yardr-n9ss), the yard-shape matrix.
It walks a yard with a git depot, one with a directory depot and one set up
as a server by `contrib/server-yard` through the same steps: init, doctor,
depot add and depot init, a bead created, listed and shown and then worked to
its end by the stand-in agent, status, and the daemon stopped, started and
stopped again. A shape that cannot do a step does not skip it. Its table
entry holds the refusal it gets, and the matrix pins the exit status and the
start of the error. A peer-linked yard and a hosted one are not in the table
yet.

A shape takes 35 to 50 s on the yard's machine and the three run beside one
another, which puts `contrib/isolated-yard` at about 75 s under `-race` (18 s
without the matrix), over R1's 60 s. The package is on R1's allow-list for
that, and no bead brings it under, because the matrix is an acceptance test
and the quiet run is where its minutes are spent. `YARDR_LANDING=1` with
`YARDR_R1_STRICT=1` and `YARDR_QUIET_RUN=1` beside it stays the one command
anyone can run.

R7. The rules are judged by re-measurement: the same per-package table one
week after the last fix bead lands, against the design's table and its
attached JSON, with the deltas per package, the new per-bead cost, and
the same-sha rerun rate counted against R4. Every rule that missed is a
filed follow-up or a closed wont-fix with the number.

## Running a package's tests on Linux

CI's Linux legs are the first to see what only Linux does, and a gate on a
Mac cannot: a file open for writing that a forked child carries into its exec
(`ETXTBSY`), one argument capped at 128 KiB, a `/proc` file that vanishes
under a read, `dash` for `sh` and uutils for `mkdir`, a seven-digit pid, a
`_linux_test.go` file the Mac never compiles. A builder's pane shows none of
them, so a fix for one is proved on Linux from the pane, as R2's launchd
recipe proves a macOS one: the package's tests in a container, in one stream,
first on the code as it was and then on the fix. The gate runs none of this.

The image is `ubuntu:26.04`, the userland of the CI runner that showed
uutils's `mkdir` losing a race: its `sh` is `dash`, and the git it installs is
2.5x. CI's own `ubuntu-latest` moves on, so the pin is the recipe's, held to
CI's by no test. The test binary is built for Linux on the Mac, which takes
seconds once its build cache holds the package's Linux build, and puts no
toolchain, module download or build cache in the container. A `docker` command
that runs Linux containers is the one requirement
([Docker's install page](https://docs.docker.com/get-started/get-docker/)
covers macOS and Linux). Set the package, the test and the count, then run
the commands as they stand:

```sh
PKG=internal/peer                        # the package, from the worktree's root
RUN='^TestProcessExitingMidReadIsGone$'  # -test.run: one test, or . for all
COUNT=1                                  # -test.count: runs in one process
T=$(mktemp -d)
GOOS=linux GOARCH=$(go env GOARCH) CGO_ENABLED=0 go test -c -o "$T/pkg.test" ./$PKG
docker run --rm --init --cpus 3 --user 1000:1000 \
  -v "$PWD":/src:ro -v "$T":/t:ro -w /src/$PKG ubuntu:26.04 \
  /t/pkg.test -test.run "$RUN" -test.count="$COUNT" -test.v -test.timeout 10m
```

Remove `$T` afterwards, as a command of its own (`T=<the directory>; rm -rf
"${T:?}"`). The build is seconds of the Mac's cpu and the run is the test's
own time on at most three cpus; the image is pulled once.

What each part is for:

- `GOARCH=$(go env GOARCH)`: Docker runs the Mac's own architecture, so the
  binary runs natively and nothing is emulated. `CGO_ENABLED=0`: the Mac has
  no C cross-compiler, and a static binary needs nothing of the image's libc.
- `-v "$PWD":/src:ro` and `-w /src/$PKG`: `go test` runs a package's tests in
  its directory, and they read `testdata` by a relative path. The tree is
  read-only, so a test that writes beside its source fails there, which is a
  finding.
- `--init`: without it the test binary is the container's PID 1 and reaps
  nothing, so a killed orphan stays a zombie that `kill -0` still answers for
  (R2, `testload.Running`).
- `--cpus 3`: the builders' cap, and Go's: it sizes GOMAXPROCS from the quota
  (3 here; 2 for `--cpus 1`), so the test sees three procs, not the Mac's.
  Run no second container beside it.
- `--user 1000:1000`: CI's runner is not root, and as root a test of a
  permission refusal has nothing to refuse and skips
  (`TestConnectDeniedFailsAtOnce` in `internal/server`). In this image that uid
  is its `ubuntu` user, with a home it can write.
- `-test.timeout 10m`: `go test` gives a test binary ten minutes and a bare
  binary has none, so a hang would hold three cpus until someone stopped it.
- `COUNT`: a rare failure is shown by `-test.count` in one process, the way
  R2's launchd job and `contrib/burn` run it, not by parallel copies of the
  container.

Two of the cases the recipe was drawn from fit it with those first lines set
and nothing else changed: `PKG=.` with
`RUN='^TestYardTestBudgets$/^over-budget_packages_are_re-run_up_to_three_at_a_time,_and_one_at_a_time_in_a_strict_run$'`
for the uutils `mkdir` that was no lock, and `PKG=internal/web` with
`RUN='^TestLimits'` for the `ETXTBSY` a fork of a parallel test caused, which
failed three runs in three hundred, so `COUNT` in the hundreds.

Where the case needs more:

- A test that starts `git`, `perl`, `ps`, `jq`, `shellcheck` or `go` (it
  builds yardr, or runs `go env`), a package that needs cgo, or the race
  detector. The bare image has none of them, and a test that starts one fails
  with `executable file not found in $PATH`; the Mac cannot cross-build cgo.
  Build an image that has them, on the same ubuntu for its git, with Go copied
  from the golang image, and run `go test` in it with a cache directory on the
  Mac: uid 1000 can write there, and cannot in a fresh Docker volume:

  ```sh
  docker build -q -t yardr-linux-test - <<'EOF'
  FROM ubuntu:26.04
  RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates git perl procps gawk jq shellcheck gcc libc6-dev \
    && rm -rf /var/lib/apt/lists/*
  COPY --from=golang:1.27 /usr/local/go /usr/local/go
  ENV PATH=/usr/local/go/bin:$PATH GOTOOLCHAIN=auto
  EOF
  T=$(mktemp -d); mkdir "$T/tmp"
  docker run --rm --init --cpus 3 --user 1000:1000 \
    -e GOPATH=/g -e GOCACHE=/g/cache -e GOTMPDIR=/g/tmp -e GOFLAGS=-buildvcs=false \
    -v "$PWD":/src:ro -v "$T":/g -w /src yardr-linux-test \
    go test -count="$COUNT" -run "$RUN" ./$PKG
  ```

  `-race` goes beside `-run`. The golang tag moves by patch release and
  go.mod names one, so the image's Go may be older: `GOTOOLCHAIN=auto` fetches
  the one go.mod names into `$T`, over the network, a few hundred MB, so keep
  `$T` for the bead's runs and remove it at the end, with `docker rmi
  yardr-linux-test`. Go keeps its module cache read-only, so `rm -rf` of `$T`
  needs `chmod -R u+w "${T:?}"` first, as a command of its own.
  `-buildvcs=false`: the tree is a worktree whose `.git` is a file naming a
  directory outside the mount, and Go would stop at it.
- A case about git. The golang image's Debian git is older than CI's, and a
  git before 2.48 reads a copied worktree's links differently, which broke
  fixtures until their copies wrote the links themselves. The image above
  takes Ubuntu's, as CI does. To see a difference between gits, run the same
  test binary in `golang:1.27-trixie` too, where uid 1000 has no home: add
  `-e HOME=/tmp`.
- A host that other gates load. A container's cpu quota is invisible to the
  load average `testload` reads, so a suite whose fuses scale with load (the
  contrib suites, R2) is given the factor by hand: `-e YARDR_TEST_LOAD=4`, the
  gate's cap.
- The code as it was. Build the first binary with `-overlay <file>` on the
  `go test -c` line, the file mapping the changed product file to the base's
  copy of it (`git show <base>:<path>`, kept outside the worktree), as R2 does
  for a cut deadline. The note names both runs, each with the image, the
  variables as set, and the counts it printed.

The race lock (R4, R5) is the gate's. `racelocked` in `.yardr/test` takes it
for a race leg, a landing's or the quiet run's, and for a plain step that
covers most of the suite, and no other command takes it: `contrib/burn` runs
beside it, and so does a leaf gate run. A run of this recipe is a leaf run, one
package or a few in one stream on three cpus, and takes no ticket. `sh
.yardr/test --race-queue` shows who holds the lock and who waits without taking
a place, and is worth a look before a run of minutes. A run that would cover
the whole suite, or most of it, is the load the lock exists for, and a
container run cannot queue for it: the lock is reached only through the gate's
own steps. CI's Linux legs run the whole suite after the landing.

## The disk floor

A gate writes gigabytes while it runs, and a disk that fills under it fails the
tests in a way that reads as the branch's own: on 9 Nov 2026 the data volume
went to 100% during a builder's gate over 40 packages (a full gate wrote about
13 GB in the minute), and test links and writes failed with "no space left on
device" in four packages, each reported as a failure of the change. So before
it selects anything the gate reads the free space of the volumes it writes to:
GOCACHE's and GOTMPDIR's, as `go env` gives them (the yard rotates GOCACHE with
`go env -w`, so the environment does not say), and TMPDIR's. Under the floor on
any of them (`floorgib` in `.yardr/test`, the one place the number is set) it
prints one line, naming the volume, the free space and the floor, and exits 2:

```text
test: cannot run: 6.2 GiB free on the volume of GOCACHE, under the floor of 20 GiB: the disk is too full to start, which says nothing of the change
```

The floor is 20 GiB, above the roughly 12.1 GiB a full gate wrote in that
minute. This leaves several GiB after that observed burst. About three in ten
keepings in the two days before this was written left less than 20 GiB free, so
those landing gates wait until there is room. A yard can set another floor by
exporting `YARDR_TEST_FLOOR_GIB=<n>` where its gates start (the daemon's
environment, a pane's profile); 0 turns the check off, and the gate's own tests
give 0. The daemon and a landing's keeping read the same variable for the floor
under which they take a rotated GOCACHE directory before its 30 minutes are over
(below); `diskkeep.FloorGB` repeats the default for them, and
`TestDiskkeepFloorIsTheGatesFloor` holds it to `floorgib`.

Exit 2 is "could not run" ([line.md](line.md#4-approved-the-assembly-lands-it-on-main)):
no test line, nothing ran, and the assembly holds a landing instead of sending
it back, since nothing a builder changes can fix it. A builder who meets it has
met the machine and not their change: they free nothing that is not their own,
and tell the yardmaster. The daemon removes a rotated GOCACHE directory itself
when its 30 minutes are over ([setup.md](setup.md#the-machines-disk)), so one
that stands beside the live cache past that is worth telling too. While the disk
is under the floor it also takes one inside its 30 minutes when no go process
started before it was rotated away from, so a gate that is stopped at the floor
by a cache nothing uses need not wait out the 30 minutes; setup.md says how it
knows, and what it cannot see. A rotation of
GOCACHE that nothing has built in yet does not exist, so each volume is read at
the nearest directory that does. A `df` or
`go env` that cannot be read lets the run go on, since a gate that cannot see
the disk must not refuse a healthy one. A plan (`YARDR_TEST_PLAN`), `sizes` and
`--race-queue` run no step and are never refused, nor is a CI runner
(`GITHUB_ACTIONS`), which is not the yard's shared machine and whose free space
is its provider's. The floor is read once, at the start: a run that waits on the
race lock is not read again, and several gates that start together each see the
same free space.

A disk that fills after the floor was read still fails tests, and the gate does
not take that for a flake. A failed test whose first-run output holds the exact
text `no space left on device` in its own block (its `--- FAIL:` line and the
lines under it), or a failed package with no test named whose output holds it,
is named as the disk's in one line beside the rerun's result, with how full the
fullest of the volumes the run writes to is: the three the floor reads, so that
a GOTMPDIR on another volume than TMPDIR shows the volume that is full, and
nothing where one of them cannot be read. It is left out of the `test: flaky:`
line, the flaky log and the `Flaky:` bead a repeat would file, and a test that
failed for another reason in the same run is a flake as before. The failed
packages still run again; one whose rerun fails with the same text fails the
step, with a second line for the disk's state, and nothing runs a third time.

## File size budget

Every run also judges Go file size: a non-test `.go` file over 1000 lines
needs a row in the `filesizes` table in `.yardr/test`, naming its ceiling
in lines (`wc -l`, the count the table was seeded with) and why it is
over. A file over its ceiling, or an unlisted file over 1000 lines, fails
every run: split it along a concept, one concept per file. A ceiling
standing above its file, or a row whose file is gone, only warns on a plain
run, and the step passes:

```text
test: warning: <file> has <n> lines, under its <m>-line ceiling: the landing lowers the row
test: warning: filesizes row <file> names a file that is gone: the landing deletes the row
```

A change that shrinks or deletes a listed file therefore leaves the table
alone, and two that shrink the same file, or neighbouring files, never
conflict on a row: the ceiling only falls, the landing lowers rows to
actual, and nothing else edits a row.

The assembly lowers them. Where the depot's test command is a file of the
branch that carries the table, as `.yardr/test` is,
`contrib/assembly/merge-local` and, for a train, `integrate-train` run
`sh .yardr/test sizes --rewrite` on what they land, from the branch's own
copy of the script, after the rebase and before the tests. Each row becomes
the smaller of itself and its file's `wc -l`; a row whose file is gone, or
has at most 1000 lines and so needs none, is deleted; none is added or
raised. A file over its row fails the rewrite, which changes nothing, and
the bead is held: that is growth, or a row cut below its file by hand, and
no landing settles it. merge-local amends the change into the branch's
head; integrate-train commits it as `assembly: lower filesizes rows to actual`,
once for the train, so a wagon landing on its train's branch leaves the
rows alone. The rewrite has a deadline of its own, `YARDR_REWRITE_TIMEOUT`
seconds, 120 unless set. A value that is not a whole number of seconds
holds the bead, or the train, before the rewrite runs. The tests that
follow have a deadline too, `YARDR_TEST_TIMEOUT`, which leaves out the
seconds the run queued on the race lock. The rewrite waits on no race
lock, so the script credits nothing to its deadline. Past the deadline the
script kills the run's whole process tree and holds the bead, or the train
under `integrate-train`, with a note that carries the last 4000 bytes of
what the rewrite printed.
The landing's tests then run the base's copy of `.yardr/test`,
whose rows still stand above the branch's shrunk files, so at landing
(`YARDR_LANDING=1`) those two cases pass with a printed note instead of the
warning:

```text
test: <file> has <n> lines, under its <m>-line ceiling: landing keeps the higher base row (the branch lowers it)
test: filesizes row <file> names a file that is gone: landing keeps the base row (the branch deletes it)
```

Over-ceiling and unlisted-over-1000 fail at landing as ever.

Two things say where a file stands before a change takes it over, so that a
split is planned and not found at the gate. `sh .yardr/test sizes [path]`
reports how much room the Go files have, in the seconds a find and a `wc`
take: none of a run's steps, no git and no go, and no test line (which would
pass for the record of a run). It prints a line for each listed file, as
`test: sizes: <file> <lines>/<ceiling> (<room>)` with the room signed (negative
for a file over its ceiling); for each row whose file is gone, as
`test: sizes: <file> gone, its row says <n>: the landing deletes the row`; and
for each file with no row once fewer than 50 lines are left before the 1000 it
fails at, marked `no row, fails over 1000`. With a path it prints that file's
line alone, whatever its size. The report judges nothing and always exits 0.
Run it before planning a change to a file over 1000 lines: a listed file has no
room, since the landing keeps its row at its size, so a change that grows one
splits it first. `sh .yardr/test sizes --rewrite` is a second mode of the same
entry, the landing's (above): it runs none of a run's steps either, and it is
the only mode that changes a file or can exit 1, for a file over its row.

The gate warns before formatting, vet, build and tests, whether it then passes
or fails, of every file with fewer than 50 lines of room under its ceiling, as
`test: warning: <file>
has <lines> lines, <n> under its <ceiling>-line ceiling ...`. A listed file's
ceiling is its filesizes row; a file with no row has a ceiling of 1000. The
warning appears even when formatting, vet or build fails first, so a builder
sees the file before planning a change. Listed files at their rows are warned
of too, since they have no room under their ceiling.

## Pruning tests

`contrib/testprune` finds which of one package's tests can go without
losing what the suite checks: a test whose covered statements all lie in
other tests of the package is a candidate, ranked by cost, and the
report names the greedy removal set, the near-duplicate pairs and the
coverage with and without the candidates (its README says how each is
measured). A candidate is not proven redundant: its assertions may check
something the others only run. Removals land as follow-up beads, one
package each, and each keeps the guard: the package's statement coverage
after the removal is not lower than before (`go test -cover`, same
flags), and the reviewer reads each removed test's assertions against
the tests that stay. The black-box suites (R5) stay whole.
