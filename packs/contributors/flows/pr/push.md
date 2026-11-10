summary: The yard pushes the fix onto the pull request's branch for a fresh accept.

The fix the builder committed on the bead is pushed here, in public, onto the
contributor's pull request branch. A pull request already in `approved` can
take the hand path `approved` → `contributor` → `fix` described in
[approved.md](approved.md); the same pick and head checks apply. No agent and
no person works this stage:
`push-fix`, the `cmd` of an exec group of the yard's own, takes the bead,
makes the recorded fix commit again as the yard's GitHub App's own, pushes it
onto the pull request's branch as the App, records it as the bead's
`fix_commit`, waits for its own push to arrive as the bead's head, and sends
the bead back to `triage` for a fresh accept of the new head.

- **The pick.** The label `fix-ok` and the head `fix_head`, a maintainer's,
  name the round: the script pushes only when the picked head is the head the
  maintainer accepted (`accepted_head`) and the head the reviewer read
  (`reviewed_head`), all one commit, and the pull request still has it. A pick
  whose heads disagree, or a head that moved since, holds the bead: the fix of
  an old head is never pushed.
- **Whose branch.** The one the live pull request names, read when the script
  runs and never from local config: the head repository and branch of the pull
  request the bead tracks. The push names that URL and that ref, and only that
  ref. A pull request whose branch forbids maintainer edits is held, not
  pushed to.
- **Whose commit.** The builder's recorded `fix_commit`, fetched by its sha
  into a temporary bare repository the script owns. It shares no local Git
  config with the depot and checks out no pull request files, so a builder's
  config or attributes cannot run commands or redirect the push. The script
  runs nothing of the builder's worktree. What it pushes is that commit made
  again as the App (next), fast-forward only, never forced: a push GitHub
  refuses (new commits meanwhile, a branch protection, a fix touching
  `.github/workflows` without the grant) holds the bead in git's words.
- **Whose name.** The App's. The script asks GitHub who its token is (the
  bot's login, and the id of that bot's user) and makes each commit of the fix,
  the first parents from the recorded commit back to the picked head, again
  with the App as its author and committer: the same tree, message and dates,
  and the parents that are not on that line as they are (a merge of the base
  keeps the base's commits, none of which is rewritten). A commit that is the
  App's already is kept. A signature on a commit it makes again is dropped, not
  carried over: it would verify against nothing once the commit's headers
  change. Once the push is out it records the commit it pushed as the new
  `fix_commit`, so that the recorded commit is the one on the branch; a run
  that finds that commit there already, and not recorded, records it and
  pushes nothing again.
- **Whose head afterwards.** The script advances only once the bead's head is
  the pushed commit: its own push arrives as an event a few seconds later and
  moves `pr_head` with no stage change, and the script waits for it, bounded
  at five minutes. A timeout holds the bead and names the wait. The script
  never writes `pr_head` itself.

Outcomes:

- `done`: pushed, and the bead's head is the pushed commit. The bead goes to
  `triage` with a card of the new head: the fresh accept IS the human look at
  it, and `review`, the yard's CI and a fresh maintainer's yes follow. The
  push is public before all of that: the commit is on the contributor's branch
  where anyone can read it.
- `merged`: not the script's to report. The pull request was merged on GitHub
  while the bead waited here, and the source says so. The bead goes to
  `merged`, and the run is stopped with it: a report the script still makes is
  refused, as it is after a push, since the bead has left the stage.
- `dropped`: likewise, for a pull request closed unmerged. The bead goes to
  `closed`.

A push to the pull request while its bead waits here says nothing to this
stage: new commits move the bead's head and leave it where it is, and the
script's check of the head holds on one that is not the picked one.

A run that pushed nothing holds the bead here with a note and reports nothing,
for the yard to put right and unhold: no pick, heads that disagree, a head
that moved, maintainer edits off, no token, a mint or fetch that failed, a
pull request that is not open, GitHub that will not say who the token is, or a
commit it cannot make again as the App. But a push GitHub refused, or a push whose head
never arrived, is resumed by hand to `triage`
(`yardr bead advance <id> --to triage`), never by unholding here: the round
starts again from a fresh accept of the head the branch has now. No hand path
leads back into `push`: every push is a fix round's. A pull request that is no
longer open is usually the source's to say first (`merged`, `dropped`), but a
run can find it over before the source has, and no event moves a held bead:
advance it by hand.

The maintainer's yes gates the yard's merge only: not the publication, which
is already on the branch, and not the author's own merge on GitHub, which
needs no yes from the yard.
