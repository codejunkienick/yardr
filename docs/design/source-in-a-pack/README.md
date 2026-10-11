# Sketch: a source a pack brings (yardr-s48v)

Not for merging. Files to read and argue with, written by the yardmaster for
mckean on 11 Oct 2026. No yardr reads them.

The direction is mckean's (11 Oct): **a source's logic is commands the
declaration names.** For Linear a shell script or Linear's own CLI, for
another source Lua, or anything else that reads JSON and prints JSON. The
first commit of this branch tried the other way, all of it as data, and ran
out at six places; this one has none.

- [`pack.yaml`](pack.yaml): the `sources` part of a pack.
- [`sources/linear.yaml`](sources/linear.yaml): the declaration, 70 lines.
- [`sources/linear/normalize`](sources/linear/normalize): a webhook to the
  envelope, in jq. Tried on two made-up payloads, a comment and an issue
  update; not on a real delivery.
- [`sources/linear/thread`](sources/linear/thread): reply, posted, read,
  open, state, in bash with curl and jq. `bash -n` passes; no request in it
  was sent to Linear.

## The shape

| In the declaration | Replaces | Is |
| -- | -- | -- |
| `hook.verify`, `hook.bodies` | `Adapter.Verify`, `Bodies` | data, checked by the core |
| `hook.normalize` | `Adapter.Normalize` | a command |
| `thread.reply`, `posted`, `read`, `open`, `state` | `Replier`, `Opener`, `Stater`, `Forwarder` | a command each |
| `link` | `Linker` | data: a pattern and two templates |
| `calls` | nothing, as today | declared requests an agent may be granted |

The contract of a command is the one `limits.command` has today: a list run
without a shell, one JSON object on stdin, one on stdout, exit 0, a time
limit; on anything else the yard reports the command's stderr.

## What I chose, and why

1. **Verifying a webhook stays data in the core.** The signing secret is
   handed to no command and an unverified body reaches none. The cost is a
   list of schemes in Go (HMAC over the body, a timestamp window, GitHub's
   `sha256=` prefix); a source with a scheme not on it needs a line of Go.
2. **A command gets one credential and nothing else of the yard**: the
   variable `credential.env` names, from the env file of whoever acts.
   `normalize` gets none.
3. **`calls` stay as they are and stay separate.** A declared request is
   what an agent may be granted, one by one, and what the yard can show
   before it is made. A command is the yard's own, and no agent calls one.
4. **Idempotency is the command's.** The yard gives a key and asks `posted`
   before it tries again, as it does today.

## Open, and mckean's

1. **Does a pack carry the scripts?** The docs say "a pack brings no command
   the yard runs", and `pack check` refuses a `limits` key in a pack. But a
   pack already brings one: `groups.assembly: { run: scripts/merge-local }`
   is a file of the pack that the yard runs. The difference is what it is
   handed. A landing script gets a worktree; a source's command gets a
   credential. My proposal: a pack carries them, as it carries `scripts/*`;
   the import shows each command and the file behind it, and only the
   operator's apply adds them, which is already the rule for a declared
   source. A command with an absolute path is one the operator installed,
   and the import only checks that it is there.
2. **A process per webhook delivery** for `normalize`. Cheap at a yard's
   rate (the sketch's takes 20 ms); a source that sends hundreds a minute
   would want it kept running, which is a protocol, and not proposed.
3. **GitHub's dispatcher rules** (`internal/dispatch/github.go`, `ci.go`,
   1,100 lines: the pull request a train tracks, its head, CI as state) are
   not an adapter method and are not in this sketch. They need their own
   answer: more commands, or events the pack's `says` table already routes.
4. **`limits` in a pack.** If 1 is yes, the refusal there has lost its
   reason and could go.

## Order of work, if this is the direction

1. Close the places where the core branches on a source's name outside
   `internal/hooks` (no behaviour change).
2. The command contract and `thread.*`, proven by Linear running from the
   declaration beside the Go adapter, same events and same replies.
3. `hook.verify` and `hook.normalize`; then the Go Linear adapter goes.
4. GitHub, with point 3 above answered first.
