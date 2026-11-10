summary: A builder fixes a reviewed pull request's findings on the picked head.

A pull request from outside that the reviewer found wanting is fixed here, on
the head its maintainer picked. The bead comes by a maintainer's hand from
`contributor`, with the label `fix-ok` and the picked head recorded as
`fix_head`:

    yardr bead update <id> --add-label fix-ok --meta fix_head=<sha>
    yardr bead advance <id> --to fix

That is how a pick reaches the bead, whoever types it. A bead's agent may not:
`fix-ok` and `fix_head` are refused to it on any bead, its own included, and
the push script checks the pick before it pushes anything, so a stray move
here holds.

The builder holds no key: a pull request from outside is a stranger's text,
and what talks the builder into something must not have the yard's GitHub App.
It fetches anonymously, as the reviewer does, and pushes nothing: the yard
pushes its fix in the next stage.

Its checkout is the picked head, detached, never the live tip: the yard puts
the worktree on `fix_head` when the session starts. Before working, it proves
it (`git rev-parse HEAD` is the `fix_head` on the bead), and works on nothing
else: a head nobody picked is not read or run here.

What is fixed is the reviewer's findings, the note on the bead and the posted
review. The builder may run the depot's tests on its checkout, which is
reviewed code and its own fix, and runs nothing else of the pull request.

The fix is committed here, on no branch, and its commit recorded as the
bead's `fix_commit` in the update before the builder reports:

    yardr bead update <id> --meta fix_commit=<sha>

One commit is the plain way; what is pushed is the recorded commit, on the
picked head. A pull request that conflicts with the base is resolved by
merging the base into the checkout, never by rebasing it: a merge is on the
picked head and pushes fast-forward, and a rebase is neither, so the push
script holds it.

Outcomes:

- `done`: the fix is committed and recorded. The bead goes to `push`, where
  the yard pushes the recorded commit onto the pull request's branch.
- `question`: it needs a decision only {{operator}} or a maintainer can
  make. The note has the question and the options, and the bead goes to
  `decide`. Nothing is pushed.
- `merged`: not the builder's to report. The pull request was merged on
  GitHub while it was fixed, and the source says so: the session ends and the
  bead goes to `merged`.
- `dropped`: likewise, for a pull request closed unmerged. The bead goes to
  `closed`.

A push to the pull request while its bead waits here says nothing to this
stage: new commits move the bead's head and leave it where it is, with no
outcome for them. The push script holds on a head that is not the picked one,
so the fix of an old head is never pushed; the new head returns through
`triage` for a fresh accept, and the round starts again from a fresh pick.

The push the next stage makes is public before the re-review: the commit lands
on the contributor's branch where anyone can read it, and comes back through
`triage` for a fresh accept, then `review`, the yard's CI and a fresh
maintainer's yes. That yes gates the yard's merge only: not the publication,
and not the author's own merge on GitHub, which needs no yes from the yard.
