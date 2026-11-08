summary: The yard posts the reviewer's stored review on the pull request as a comment, then sends it on.

The review the reviewer stored on the bead is posted here, in public, on the
contributor's pull request. No agent and no person works this stage:
`post-review`, the `cmd` of an exec group of the yard's own, takes the bead,
posts the stored review on the pull request as the yard's GitHub App, and sends
the bead on (docs/line.md, "Posting a pull request's review"). It is the only
place a review is posted from, and the only holder of the App's key besides the
scripts that test and merge: the reviewer holds none, reads a stranger's text
and runs nothing that posts, so a reviewer misled by what it reads cannot act
as the App.

- **What is posted.** One GitHub review on the pull request the bead tracks,
  made from the stored review (the bead's metadata `review`: `outcome`, `body`,
  `comments`) and from nothing else of the bead: its text and its notes are not
  read. The verdict (`Approved`, `Changes needed`) is written as the first line.
  It is always a comment review, never an approval or a request for changes:
  merging is a maintainer's, and a request for changes would stand on the pull
  request after its author has acted, since nothing posts again. The review is
  the reviewer's, as it stored it, and nobody reads it between.
- **Whose pull request.** The one the bead tracks, which must be the link the
  hook made when it made the bead. A link made by anyone else, a person or an
  agent of the yard, is not posted on: the bead is held. The pull request is the
  contributor's, from a fork or a branch of theirs, so its branch is not looked
  at.
- **On the head that was read.** The review is of the head the reviewer
  recorded (`reviewed_head`), and the script posts it only when GitHub says the
  pull request has that head now, anchored to it.
- **Once.** When the review is posted the stored review is emptied, so that no
  later round finds it, and the bead is reported.
- **No label.** The script gives the bead no `merge-ok` and takes none off. The
  reviewer's grant for a bot's bump of Go modules is the reviewer's own, and a
  maintainer's yes is theirs.

Outcomes, the review's own, and `failed`:

- `done`: posted, with `Approved` as its first line. The bead goes to `ciwait`,
  where the yard runs the depot's tests on the head that was read. The word is
  the verdict on the change and not a yes to merge it.
- `changes`: posted, with `Changes needed` as its first line. The bead goes to
  `contributor` and comes back to `review` when the pull request has new
  commits.
- `failed`: the review could not be posted as the reviewer stored it, and the
  note says why: none stored, a field wrong, an outcome this stage does not take
  (`done-safe` is no outcome of this flow), no head recorded, a pull request that
  has moved since the head was read, or a review GitHub refuses as written (a
  comment on a line that is not in the diff). The bead goes back to `review`,
  once, to be read at the head the pull request has now: the note says so and
  never asks the reviewer to push, since the pull request is its author's. The
  flow counts the rounds back to `review`, whichever outcome made them, so a
  round past the limit goes to `decide` with the reason in the note: the second
  failed post, or the first of a bead that a push or its contributor's new
  commits already brought back to be read. A person who has put it right sends
  the bead on from `decide` to `review-post`, which posts it again.
- `merged`: not the script's to report. The pull request was merged on GitHub
  while the bead was here, and the source says so. The bead goes to `merged`, and
  the run is stopped with it: a report the script still makes is refused, as it is
  after a push, since the bead has left the stage.
- `dropped`: likewise, for a pull request closed unmerged. The bead goes to
  `closed`.

A push to the pull request while the script runs says nothing to this stage:
the script's check of the head fails a review of a head the pull request no
longer has back to `review`, and the yard's CI holds a head nobody read.

A run that is no verdict on the review holds the bead here with a note and
reports nothing: a bead the script was not given in this stage, a link that is
not the hook's, a pull request that is not open, no token (the group's
environment has no `GITHUB_APP_ID` and `GITHUB_APP_PRIVATE_KEY_FILE`), a mint
that fails, or GitHub refusing the App. That is the yard's to put right: act on
what the note says, then `yardr bead unhold <id>` runs the script again. A hold
after the review was posted says so, and the bead is then moved on by hand
(`yardr bead advance <id> --to ciwait`, or `--to contributor`), since posting
it again would put it on the pull request twice. A pull request that is no
longer open is usually the source's to say first (`merged`, `dropped`), but a
run can find it over before the source has, and no event moves a held bead:
advance it by hand, `yardr bead advance <id> --to merged`, or `--to closed`.
