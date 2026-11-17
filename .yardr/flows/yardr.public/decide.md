A bead here waits for a person: Christopher decides. No agent works this
stage. The bead carries the question and the options, written by whoever sent
it here; if it does not, ask them before deciding.

A bead leaves once the answer is on it, as a note. The stage has no outcomes;
the person answering moves it (`yardr review -g decisions`, or
`yardr bead advance <id> --to <stage>`):

- to `new`, when a builder can now finish it;
- to `backlog`, when the answer changes what the bead is and the yardmaster must
  reshape it.

When a bead arrives the yard puts a card for the decisions on it, offering
these moves, and picking one moves the bead: the person's words with the pick
stay on the bead for whoever works it next. A bead with an ask already open
gets no card, unless that ask is the only one and a question of options:
where it names a move for each option (`yardr bead ask --move`) and the stage
offers each, it becomes the card, its options the choices and each firing its
move; where it names none, or a move no longer offered, it is quoted on the
stage's card of moves instead. Either way it is taken back, so it is answered
once, on the card. A card gives way to an ask put after it, and any other ask
is what the person answers: the bead is moved by hand once it is.

A decision that is not about one piece of code is a bead created straight
into `decide`, and is closed with the answer as its reason
(`yardr bead close <id> --reason - <<'REASON'`, the answer, then `REASON`
on a line of its own).

## Ask
kind: moves
question: Where should this bead go next?
context:
- The bead waits for a person; what it needs is in its notes.
- Pick where it goes. Your words with the pick stay on the bead for whoever works it next.
