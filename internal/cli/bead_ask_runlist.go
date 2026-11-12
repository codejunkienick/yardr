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
  source rm <name>
      the source is removed with the grants of its calls and the counts
      and keys kept for it; the files of its commands go too, removed by
      the server after the approval, and the file its credential is read
      from stays; subscriptions, followed threads and hooks that name it
      stay and the card counts them
  source move <from> <to>
      what the yard keeps under the source <from> is folded into the source
      <to>, which the yard declares: its hooks, identities, crew grants of
      threads, subscriptions and followed threads, each counted on the card
      (trust for hooks, as-user for users' identities, where there are
      any); what both hold is kept once; there is no such command at a
      terminal (a pack apply folds the records of its own sources): the
      card is the way in, and the links of <from> move with link move
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
  pack apply <name> [--yes] [--sha256 <digest>] [--depot <depot> [--flow <flow>] [--crew <pack>=<yard>] [--group <pack>=<yard>] [--bind <spec>] [--members <group>=<a,b>] [--open-threads <crew>=<sources>] [--calls <who>=<source>:<calls>]]
      a pack yardr embeds, a directory inside a depot's main
      checkout, or the packs repository's URL pinned with --sha256,
      read at the ask and pinned by its digest: of kind
      source with no flag, a depot kind re-applied to the one depot
      that holds it, or a depot pack's first install to a named depot
      with the mappings that answer its plan; a relative directory is
      sent absolute, against where the ask is typed, and --sha256
      names the digest pack check printed, refusing where it names
      another; the card comes from the server's own reading and shows
      each source, mapping, object and file diff whole, and a
      directory past the whole-card bounds is refused, never
      summarized; the server carries it out; with --yes over files
      the plan says were edited by hand, which the card shows whole
      and approval overwrites, keeping no other copy, and a --yes
      that overwrites nothing is refused; any other URL, an imported
      pack, a directory outside every checkout, any other flag and a
      plan that asks stay in a terminal; --setup installs a pack of
      kind source, embedded or applied from a directory in a terminal
  hook add <name> --source <declared source> [--unknown unknown|allow|ignore] [--as <crew member>] [--map <type>:<stage>=<state>]
      a new webhook endpoint POST /hooks/<name> for the declared
      source, or the hook that stands replaced whole, verified with
      its signing secret given on the hook's own secret card
      (--secret-hook) into the file the yard owns for it, a
      replacement keeping the file it stands with; --says and --makes
      lines ride beside --run (--run-says and --run-makes, S: and M:
      on stdin), and the card shows each table now and after, whole;
      for a source that hears by identity, --unknown names what an
      unmapped sender is (unknown without it); a secret file of its
      own stays in a terminal
  hook rm <name>
      the hook is removed: POST /hooks/<name> takes no more deliveries,
      what it accepted stays, the issue moves it still had to make are
      dropped and counted, and its secret file stays where it is; the card
      names what was set on it (strong, trust), and it is how a hook the
      yardmaster added for the operator is taken back
  link move --from-source <source> --to-source <source>
      the links of one source are moved to another on the same host: the
      card counts the links and names the beads, the URLs stay as they are,
      and the whole move is refused where the command refuses it (a bead
      that would track two issues, a URL the source takes no issue at);
      moving one issue to another bead needs no approval
  subscribe <user> yardr [--done] [--where <key>=<value>]
      a user is told of the yard's own events in their inbox; with --done,
      --where or both
  user set <user> tell=<parts>
      which parts a source tells a user of outside the yard, on their
      phone: a list of notifications, mail and asks with commas between,
      or nothing after tell= for all three again
  user set <user> role=operator|member
      the user's role changes: an operator approves run asks, setup asks
      and secrets and administers the yard, and a member is a party of the
      yard and no more; the card says what the role lets whoever speaks as
      the user do and how many of their devices speak with it (strong,
      trust, whichever way it goes); the operator setting's user and the
      last operator are not made members, and the operator alone approves
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
  user device rm <user> <device>
      the device is removed: its token is refused from now on, the streams
      it held are closed and what was queued to tell it is dropped; what it
      did stands, and the card says how many devices the user keeps; asked
      of the user whose it is or of the operator, and either approves it
  user device pair <user> <device> [--url <address>]
      a pairing is opened for the device when approved: its code, good
      for ten minutes and for one device, is answered once to the
      approver alone and kept nowhere, and the card names the user, the
      device, the address the device reaches the yard at (--url, or the
      listener's, or the edge's where the yard listens nowhere) and every
      device the user then has; asked of the user whose it is or of the
      operator, and either approves it (strong, trust); user device add
      stays in a terminal, since its token has nowhere lawful to rest
  crew add <name> kind=<kind> model=<model> preset=<preset> dir=<dir>
      a new crew member on the kind the yard knows, with its model, the
      kind's named permission preset (never free args) and a directory
      under the yard's home or a depot; its role text goes beside --run,
      in --run-role <file> or as prompt=<file> whose text the ask
      carries, the card shows it whole (weighty, code), and approving
      writes those exact bytes to a read-only file of the yard's own,
      roles/<name>.<digest>.md, and starts the member with its prompt
      there; env, free args and any grant stay out, and a name in use,
      or one whose file stands already with other text than its name
      says, is refused, since this adds and never replaces
  group add <name> --runner <runner> [--limit <n>] [--set k=v ...]
      a new group on the runner manual, herdr or exec, with its limit and
      as --set cmd and args (one word each: the line is split at spaces),
      dir and worktrees (a directory under the yard's home or a depot),
      kind (one the yard knows) and prompt (an absolute path of a file
      under the yard's home) whose text the ask carries, read from that
      file; the card shows the command and the prompt text whole (weighty,
      code), and approving is refused when the file holds other text by
      then, and writes that text to a read-only file of the yard's own,
      roles/<name>.<digest>.md, which is the group's prompt: a later change
      to the file named reaches no session, and a file the yard wrote is
      not one to name. env_file, log and a peer group stay out (yardr
      group set <name> env_file=<file> in a terminal), and a name in use
      is refused, since this adds and never replaces
  crew set <name> open_threads=<sources> | set_state=<sources> | peers=<peers>
      a crew member may open threads or set the status of threads in the
      sources, or mail crew members of the peers
  crew set <name> prompt=<file> | preset=<preset> | kind=<kind> | model=<model> | dir=<dir>
      a crew member's prompt, the yardmaster's own included, or the
      permission preset of its kind that never asks (a kind's presets
      are listed by yardr harness show <kind>, and never free args); and,
      for the yardmaster's own entry alone, which the delegated settings
      never carry, its kind, its model, its directory (under the yard's
      home or a depot) and any preset of the kind it will then have: the
      card shows each now and as it will be, whole (weighty, code), the
      prompt and the flags among them (and says so when the prompt now is
      a file the yard wrote that holds other text than its name says),
      and approving sets exactly that; the prompt is a file under the
      yard's home, named by prompt=<file>, whose text the ask carries,
      read from it, and an approval is refused when the file holds other
      text by then; it writes that text to a read-only file of the yard's
      own, roles/<name>.<digest>.md, and sets it as the prompt, so a later
      change to the file named reaches no session, and a file the yard
      wrote is not one to name; no other setting rides in with it, and
      another member's kind, model and directory are the yardmaster's to
      run for the operator on their mail
  group set <name> prompt=<file> | cmd=<command> | preset=<preset> | effort=<level>
      the same for a group, and for an exec group's command (one word:
      the line is split at spaces); approving a prompt writes its carried
      text to a read-only file of the yard's own, roles/<name>.<digest>.md,
      and sets it as the prompt, so a later change to the file named
      reaches no session, and a file the yard wrote is not one to name; an
      effort is a level of the group's kind (yardr harness show <kind>
      lists them), which the card shows as the flags each session is given,
      or the word default, which returns the group to its harness's own
      effort, as a card takes no --unset: the card shows that no flag is
      given, and a group that sets no effort refuses it; a kind, a
      directory and the rest of a group are not this card's: the
      yardmaster sets those the operator has delegated on their mail, and
      the rest are typed in a terminal
  group rename <old> <new>
      the group is called by its new name and everything that names it
      follows, in one write: its routes, the beads it owns and their live
      sessions (which go on, none released), the grants its sessions hold,
      its arrival estimates, the subscriptions that filter on it, the lists
      of members that name a script group and the pack imports that recorded
      it; the old name is free after; a name in use, or one that pack imports
      give a depot's groups (<depot>-<name>), is refused
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
