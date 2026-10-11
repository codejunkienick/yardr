# Sketch: a source a pack brings (yardr-s48v)

Not for merging. Two files to read and argue with, written by the yardmaster
for mckean on 11 Oct 2026. No yardr reads them, and the GraphQL in them was
not run.

- [`pack.yaml`](pack.yaml): the `sources` part of a pack once Linear is a
  declared source.
- [`sources/linear.yaml`](sources/linear.yaml): the Linear adapter as one
  declaration, 306 lines of `internal/hooks/linear.go` and the Linear halves
  of `reply.go`, `open.go`, `state.go`, `forward.go` and `issue.go`.

## What is new in the declaration

A declaration today says the service's address, its credential, its calls,
and two capabilities that are no call of an agent's: `notify` and `limits`.
The sketch adds three keys of the same kind.

| Key | Replaces | New mechanism it needs |
| -- | -- | -- |
| `hook.verify` | `Adapter.Verify` | a short list of schemes in the core (HMAC over the body, a timestamp window) |
| `hook.envelope` | `Adapter.Normalize` | paths into JSON, first-of lists, a walk over a list, regular expressions for mentions |
| `thread` | `Replier`, `Opener`, `Stater`, `Forwarder` | a capability bound to a call, as `notify` is; and `steps`, which is new |
| `link` | `Linker` | one pattern and two templates |

## Where the file runs out

Each is marked `CANNOT` in `linear.yaml`. Linear is the easy source, and it
still has six:

1. Mentions by id, from a ProseMirror document (`bodyData`).
2. An update's text, "changed title, description", from the keys of
   `updatedFrom`.
3. Cutting the fragment off a URL.
4. A thread of more than one page.
5. Opening an issue: the team's key must become an id first (two calls).
6. Setting a status: read the team's statuses, pick one by type and
   position, never move backwards (two calls and a rule).

1 to 4 are small and could be words of the format. 5 and 6 are the real
question: the sketch invents `steps` and `pick` for them, and `pick` is a
Linear rule written in YAML. GitHub adds the App's JWT and installation
tokens, pull request and CI tracking (`internal/dispatch/github.go`, `ci.go`,
1,100 lines), none of which a file of this kind says.

## A correction to what I proposed

I proposed a declared command as the escape for logic, "as `limits` already
is". That holds only for a file the operator adds. A pack may not bring a
command: `yardr pack check` refuses a declaration with a `limits` key,
because the yard runs what it names (docs/integrations.md, "It is the
operator's"). So the choices for 5 and 6 are:

- **Grow the format** (`steps`, `pick`). Stays data, a pack can bring it,
  and the format becomes a small language that each new source stretches.
- **A command the operator installs**, which the pack's declaration names
  and does not carry. The import asks for it as it asks for a group's agent.
  Any logic, and a source is no longer added by a file alone.
- **Keep such rules in the core as named behaviours** (`state: by_type`),
  chosen by the file. Smallest, and the core still knows how Linear thinks,
  under another name.

## What is new in the pack

One thing: `declaration` may stand beside `as`, `hears` and `says`. Today a
source with `declaration` has no other key, and `hears` and `says` are for
the Go adapters only.
