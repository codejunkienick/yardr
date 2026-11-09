// The commands of the list a run ask may name, as yardr bead ask --help
// lists them: each with the use line the store's list gives it
// (store.RunCommands) and what its card says and its approval does. A test
// holds the help to the list, so a row without its entry here fails.
package cli

// askRunList is the list inside the help of yardr bead ask --run.
const askRunList = `  peer add <name> <invite>
      the yard of the invite is linked as the peer: the card names the
      yard, its key and where this yard calls it, or that this yard never
      calls it; the link carries nothing until peer allow says what
  peer allow <peer> --send <kinds> | --receive <kinds> | --expose <names> | --trust <name>@<peer>=<user> | --reach <name>@<peer>=<user> | --mail-limit <n>
      the link carries kinds each way (ask, bead, file, mail, ping,
      thread), and the card names what it carries in and out after and
      before; or who it exposes, whose mail it trusts and whose asks it
      sends on, each whole now and proposed; or it takes more mail in an
      hour; --no-send and --no-receive ride along with a kind that is
      added, and a command that only stops one needs no approval, as one
      that only narrows trust does
  peer intake <peer> --depot <depot> --flow <flow> [--limit <n>] [--default]
      the beads the peer sends become beads of the depot, pinned to the
      flow, and the card names the depot, the flow, the stage they arrive
      in and who works it, which starts this yard's agents on the other
      yard's text; --none needs no approval
  peer identity <peer> <crew> --source <source> --id <id>
      the sender with the id in the source is the crew member of the peer's
      yard: a delivery from it is kept with the speaker <crew>@<peer>,
      information and not instructions, and it is no user of this yard;
      --rm and the listing stay in a terminal
  peer rm <name>
      the link is removed with its intakes, identities and queued mail;
      the beads it sent stay and say their link is removed, and the
      other yard stands; adding it again is peer add from a fresh invite
  peer set <name> --url <url>
      the link is called at the new address, which the card shows beside
      the address now, each whole; its key, what it carries and what
      waits for it stay; --url "" (never called) stays in a terminal
  source set <source> calls_per_hour=<n>|updates_per_hour=<n>
      one caller makes that many calls of the source in an hour, or that
      many updates among them; a lower number too, which is the operator's
  source grant <source> read|<call>|--preset <preset> --group <group>|--crew <crew member> [--as <user>]
      a group's sessions or a crew member may make the source's read
      calls, or one call by its name; both flags repeat, and it is one
      command and one change; with --preset <name> the preset's calls
      of the pack that declares the source, named on the card with the
      pack; with --as <user> the call is made as that user, and that
      user alone approves it
  source add <file>
      a source the file declares, new or replacing the one of its name:
      the ask carries the declaration and each command file it names, the
      card shows it whole and a replacement's changes (strong, code); a
      commands-free add runs at approval, one naming commands is carried
      out by the server after it; an absolute program stays in a terminal
  source ignore <source> --rm <id>
      a sender ignored in the source is heard again: their next message
      follows the hook's word, a fresh mapping card under unknown, heard
      as an outsider under allow
  source check <source> --user <user>
      one real test message to the user through the source, sent when
      the user or the operator approves; a check without --user needs
      no approval and is run directly
  call <source> <call> [param=value ...] [--as <user>]
      one call of a declared source, made when approved; a file is shown by its digest and a secret-file by its file alone
  pack apply <name> [--depot <depot> [--flow <flow>] [--crew <pack>=<yard>] [--group <pack>=<yard>] [--bind <spec>] [--members <group>=<a,b>] [--open-threads <crew>=<sources>] [--calls <who>=<source>:<calls>]]
      a pack yardr embeds: of kind source with no flag, a depot kind
      re-applied to the one depot that holds it, or a depot pack's first
      install to a named depot with the mappings that answer its plan;
      the card comes from the server's own binary and shows each source,
      mapping, object and file diff whole; the server carries it out; a
      directory, URL or imported pack, any other flag and a plan that
      asks stay in a terminal; --setup installs a pack of kind source
  hook add <name> --source <declared source> [--unknown unknown|allow|ignore]
      a new webhook endpoint POST /hooks/<name> for the declared
      source, verified with its signing secret given on the hook's own
      secret card (--secret-hook) into the file the yard owns for it;
      for a source that hears by identity, --unknown names what an
      unmapped sender is (unknown without it); --map, --as and a
      replacement stay in a terminal
  subscribe <user> yardr [--done] [--where <key>=<value>]
      a user is told of the yard's own events in their inbox; with --done,
      --where or both
  user set <user> tell=<parts>
      which parts a source tells a user of outside the yard, on their
      phone: a list of notifications, mail and asks with commas between,
      or nothing after tell= for all three again
  user key <user> <source>
      the person's own address in the source, as yardr user key gives
      it: a card of this row is offered by the yard itself, when a
      delivery reaches it from an address no user has, and carries
      that address shown on the card; approving writes it into the
      user's env file (0600) exactly where yardr user key writes it.
      A --run naming it in a terminal carries no address and is
      refused: the value there is typed unseen, as yardr user key
      types it
  user add <name>
      a person becomes a user of the yard, and reads mail from now on
  user rm <name>
      a user goes, with their subscriptions and devices; the mail stays
  user invite <name> --email <address> [--role operator]
      a teammate is invited by email: a user is made (a member, or an
      operator with --role operator), the address is mapped to them in
      web_jwt_users, and the invite is reported to the edge the yard is
      connected to; the approval is carried out by the server after it,
      and there is no such command at a terminal: the card is the way in
  user uninvite <name>
      a teammate goes: the removal is reported to the edge first, then
      the user, their devices and the mapping go; a plain user, whom no
      mapping names, is removed with user rm instead
  crew add <name> kind=<kind> model=<model> preset=<preset> dir=<dir>
      a new crew member on the kind the yard knows, with its model, the
      kind's named permission preset (never free args) and a directory
      under the yard's home or a depot; its role text goes beside --run,
      in --run-role "<file>" or as prompt=<file> whose text the ask
      carries, the card shows it whole (weighty, code), and approving
      writes those exact bytes to roles/<name>.md and starts the member
      with its prompt there; env, free args and any grant stay out, and
      a name in use is refused, since this adds and never replaces
  group add <name> --runner <runner> [--limit <n>] [--set k=v ...]
      a new group on the runner manual, herdr or exec, with its limit and
      as --set cmd and args (one word each: the line is split at spaces),
      dir and worktrees (a directory under the yard's home or a depot),
      kind (one the yard knows) and prompt (an absolute path of a file
      under the yard's home) whose text the ask carries, read from that
      file; the card shows the command and the prompt text whole (weighty,
      code), and approving is refused when the file holds other text by
      then. env_file, log and a peer group stay out (yardr group set
      <name> env_file=<file> in a terminal), and a name in use is
      refused, since this adds and never replaces
  crew set <name> open_threads=<sources> | set_state=<sources> | peers=<peers>
      a crew member may open threads or set the status of threads in the
      sources, or mail crew members of the peers
  crew set <name> prompt=<file> | preset=<preset>
      a crew member's prompt, the yardmaster's own included, or the
      permission preset of its kind that never asks (a kind's presets
      are listed by yardr harness show <kind>, and never free args): the
      card shows the prompt now and the new one whole, the flags now and
      the preset's (weighty, code), and approving sets exactly that; the
      prompt is a file under the yard's home, named by prompt=<file>,
      whose text the ask carries, read from it, and an approval is
      refused when the file holds other text by then; no other setting
      rides in with it
  group set <name> prompt=<file> | cmd=<command> | preset=<preset>
      the same for a group, and for an exec group's command (one word:
      the line is split at spaces); a kind, a directory and the rest of
      a group are not this card's: the yardmaster sets those the
      operator has delegated on their mail, and the rest are typed in a
      terminal
  crew identity <name> --source <source> [--id <id>] [--key <key>]
      who the crew member is in the source: mentions and assignments of
      the id or key there are then addressed to it
  user identity <user> --source <source> [--id <id>] [--key <key>]
      who the user is in the source: threads opened there for them are
      then assigned to the id or key, and where the source hears by
      identity a delivery from it is heard as theirs; asked of them or
      of the operator, and --rm stays in a terminal
  settings set <key> <value>
      the yard's own setting changes; who the operator is and the settings
      of where the yard reads and serves from are the operator's alone,
      while hook_public_url binds nothing and is a run ask's
  peer rename <old> <new>
      the link is called by its new name; its key and what it carries stay
  bead update <bead> --originator <user>
      the bead is for the user
  timer rm <id>
      another's timer is removed; one's own needs no approval
  up
      everything the yard needs that is not running is started, and what
      runs is left alone; safe to repeat, so an approval is never stale
  crew restart <name> [--for <user>]
      a fresh session of the crew member starts once the current one is
      idle; with --for, of its conversation session for the user
  depot test <depot>
      the depot's test command becomes the command --run-test carries
      beside the ask: the card shows the depot, the command now and the
      proposed one whole, and approving sets exactly that; clearing one
      stays in a terminal
  depot base <depot> <base>
      bead branches of the depot start from the base; the card shows the
      depot, the base now and the proposed one, and approving sets exactly
      that; a directory depot has no base
`
