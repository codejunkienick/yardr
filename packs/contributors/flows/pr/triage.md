summary: A pull request from outside waits here for a maintainer to judge its author and its code.

A pull request someone outside the yard opened waits here for a maintainer.
No agent works this stage, and nothing of the pull request has been fetched,
built or run. The bead tracks it: its title, its text and its address are
on it. A pull request a maintainer accepted comes back here when it is pushed
to (below).

This stage is the hold, and it is this pack's advice: nothing of the yard
runs outside code before a person has judged it. A maintainer checks here whether the person and the code
are legit, before anything of the pull request is run anywhere. The
reviewer after this stage reads the code in a sandbox and runs none of it.
Only then does the yard run the depot's tests on it (`ciwait`), and running
it further belongs to `approved`: a person pulling the code, by their own
judgement.

What to look at, on GitHub and not in a checkout:

- the person: who they are to the project, what else they have opened;
- the size and the shape of the change, against what its text says it is;
- what a reader of code would not be shown the danger of: workflows and
  other files CI runs, build and install scripts, hooks, lockfiles and new
  dependencies, binaries and generated files.

The bead leaves on what comes of the pull request on GitHub:

- `accepted`: the label `accepted` on the pull request, put on by a person:
  a label a bot puts is no maintainer's yes. The bead goes to `review`, and
  keeps the commit the label was put on as `accepted_head`. The yes is of
  that commit, so look at the head you are accepting. The card in Decide
  (below) is the other way to the same yes: its pick of `accepted` sends the
  bead to `review` too, and keeps the head the card showed as `accepted_head`.
- `merged`: the pull request is merged on GitHub, without waiting for
  the yard. The bead goes to `merged`; nothing of the yard read or ran it.
- `dropped`: the pull request is closed unmerged. The bead goes to `closed`.
- `pushed`: new commits on the pull request while its bead waits here,
  whoever pushed. The source's word, not a maintainer's to report: the bead
  stays, as a new stay, with a card of the head the pull request has now.

New commits on a pull request you accepted bring its bead back here, from
`review`, `review-post`, `ciwait` and `contributor`, whoever pushed, even if
the label `accepted` was removed: the yes was of one commit, and nothing of
another is read or run until a person has said yes to it. The bead comes back
with a card of the new head. After looking at it, pick `accepted` on that
card, or add the label `accepted` again; if it is still there, take it off
and put it on again.

The card is the bead waiting here, put where maintainers already decide. It
names the head it would accept: the short commit, who pushed it and when, and
how large the change is, as the delivery of that push told it; nothing of the
pull request is fetched or run to draw it. One pick says yes: it records that
commit as `accepted_head` and sends the bead to `review` in the same step.
Another closes the bead and leaves the pull request on GitHub as it is: close
it there too if it should end. Later and Decline leave the bead here.

The yes is of the head shown. A push while the card waits makes a fresh stay
here: the card of the old head is taken back and one of the new head is put.
A pick of a card whose head the pull request has left is refused and accepts
nothing. A card that shows no head, because no delivery told it, cannot accept
one: use the label, or the hand path below. The label keeps working as it did,
beside the card.

Or by hand, with no label and no card: `yardr bead advance <id> --to review`, or
`--to closed`. The yard's CI tests a pull request that waited here at the
commit a maintainer accepted and no other, and a bead moved by hand has none
recorded: write the commit you looked at first,
`yardr bead update <id> --meta accepted_head=<sha>`, or the CI run holds the
bead and asks for it.

None of this is a rule of yardr. Which stage a pull request's bead starts in
is a line of the pack's `sources.github.makes`: as the pack is, the pull
request of an owner or a member of the repository starts in `review` and
never comes here, and a maintainer who trusts more authors adds them there.

## Ask
kind: choice
question: Accept this pull request at the head shown?
accepts: accepted
context:
- Judge the person and the code on GitHub first, as this stage's text says; nothing of the pull request has been fetched, built or run.
- The yes is of the head shown. A push after this card was put makes a fresh card of the new head, and a pick of this one is then refused.
- Later and Decline leave the bead here; the label accepted on the pull request still says yes, as it did.
accepted: The bead goes to review, with the head shown recorded as accepted_head, the commit the yard reads and tests.
dropped: The bead closes. The pull request on GitHub is left as it is: close it there to end it.
