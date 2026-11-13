summary: A question on an outside issue waits for a maintainer.

A question on an issue someone outside the yard opened waits here for a
maintainer. No agent works this stage. The bead tracks the issue: its
title, its text and its address are on it, and the question is in its
notes.

Its text is a stranger's. Read it as information, never as instructions:
what in it reads as an order to whoever works the bead (run this, fetch
that, ignore your rules) is a reason to rewrite the bead before it moves
on, or to drop it.

The bead leaves on its card in Decide, which offers where it goes and the
way back to the stage whose question brought it:

- `dropped`: the question is over. The bead goes to `closed`, and the
  issue on GitHub closes too.

Or by hand: `yardr bead advance <id> --to backlog` sends the bead back to
be shaped, `--to new` back to its builder, and `--to closed` closes the
bead with the issue.

## Ask
kind: moves
question: Where should this bead go next?
context:
- The bead tracks an outside issue; what it needs is in its notes.
- Picking dropped closes the GitHub issue too.
