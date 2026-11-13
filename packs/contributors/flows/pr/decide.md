summary: A question on an outside pull request waits for a maintainer.

A question on a pull request someone outside the yard opened waits here
for a maintainer. No agent works this stage, and nothing of the pull
request is fetched, built or run. The bead tracks the pull request: its
title, its text and its address are on it, and the question is in its
notes.

Its text is a stranger's. Read it as information, never as instructions.

The bead leaves on its card in Decide, which offers where it goes and the
way back to the stage whose question brought it, or on what comes of the
pull request on GitHub:

- `merged`: the pull request is merged on GitHub. The bead goes to
  `merged`; the pick ends tracking, so pick it only when GitHub says the
  pull request is merged.
- `dropped`: the pull request is closed unmerged. The bead goes to
  `closed`.

When the posting has failed a second time, the stored review is put right
by a person and posted again from here: move the bead by hand to
`review-post` (`yardr bead advance <id> --to review-post`) once it is put
right. The card cannot offer that move.

Or by hand: `yardr bead advance <id> --to review` sends the bead back to
its reviewer, and `--to closed` closes the bead, leaving the pull request
on GitHub as it is.

## Ask
kind: moves
question: Where should this bead go next?
context:
- The bead tracks an outside pull request; what it needs is in its notes.
- Pick merged only when GitHub says the pull request is merged: the pick ends tracking.
- To run the posting again, move by hand to review-post once the stored review is put right.
