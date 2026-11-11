summary: The yard runs the depot's tests on the pull request's head the reviewers read, merged into the base; a pass goes on, a fail back.

A bead whose reviews passed waits here while the yard runs the depot's own
test command on its pull request's head merged into the base, the command
every landing of the depot runs, and counts that run as the pull request's
CI, whether or not GitHub Actions also runs on the repository. No agent and
no person works this stage: `pr-ci`, the `cmd` of an exec group, takes the
bead, tests the head GitHub says the pull request has merged into the base,
and reports how it went (docs/line.md, "The yard as the CI of a pull
request").

The head the reviewers read is merged into the base before the test. The
reviewer records the commit it read as the bead's `reviewed_head` right before
it reports, and `pr-ci`
compares it with the head GitHub says the pull request has when it starts. A
pull request pushed to since, or a bead with no `reviewed_head`, is held
before anything is fetched or run: `yardr bead advance <id> --to review` has
it read as it now is, and `yardr bead unhold <id>` lets a reviewer have it.

What runs is the base commit's copy of `.yardr/test`, in a checkout of that
head merged into the base, with `YARDR_LANDING=1`: the pull request's landing
run, since no landing by the assembly follows it. The base is fetched fresh
and the head merged into it, a commit of two parents, the base and the head,
that no ref names; the merge is what is tested because it is the nearest a
test gets to what landing makes. The head stays what is reviewed, accepted,
recorded (`ci_head`) and merged; only the tree that is tested is the merge.

Outcomes:

- `done`: the tests passed on the head merged into the base, and GitHub
  still says it is the head. The bead records that head commit as `ci_head`
  and goes to `approved`; the yard merges a head `ci_head` names and no
  other.
- `changes`: the tests failed or ran past their deadline, with the end of
  their output in the note, the pull request got new commits while they
  ran, or the head does not merge into the base, with the conflicts and
  the cure, merging the base into the pull request's branch. The bead goes
  back to `new`; its next push updates the same pull request. The flow
  limits the rounds back to `new` (`max 3 then decide`).

A run that is no verdict on the pull request holds the bead here with a note
and no outcome (the tests could not run, no token, a head the reviewers did
not read). That is the yard's to put right: act on what the note says, then
`yardr bead unhold <id>` runs the tests again.
