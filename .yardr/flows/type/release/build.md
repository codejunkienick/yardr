summary: The release group tags the base, waits for CI's draft release, adds the darwin archives it builds and signs on its Mac, sets the notes and notes the five signing commands.

The release group works this stage
(`contrib/release/stage build`, docs/install.md "Making a release"): it
refuses, before it tags anything, where the signing setup (the identity
and the notary profile, in the group's `env_file`) is not whole; then it
tags the depot's base commit, pushes the tag, and waits for the draft
release CI's release workflow makes for the tag, bounded: the two linux
archives and `checksums.txt`, with every file the checksums name present.
The workflow runs the depot tests, builds the two linux archives and
`checksums.txt` reproducibly
(with `install.sh` copied beside them and named in `checksums.txt` too,
so that the hand signing at the sign stage covers it), attests the
archives and drafts the release. Before it builds darwin, the stage checks
that CI's checksums name both linux archives and the installer. It then
builds the two darwin archives on the Mac it runs on, signed with the
Developer ID and notarized by Apple, adds them to the draft and makes
`checksums.txt`
name all four archives and `install.sh`; it sets the draft's notes from
the bead's body, notes the five signing commands on the bead, for the
version, and reports done. No agent works this stage by hand.

Before the bead leaves as `done`:

- the draft release exists, with the four archives, `checksums.txt` and
  `install.sh`, unsigned, the bead's body as its notes, and
  `checksums.txt` names the four archives and `install.sh` once each;
- a note on the bead has the five signing commands.

Outcomes:

- `done`: both hold. The bead goes to `sign`, where mckean is asked to sign
  the checksums.
- `failed`: the version, the tag, the tree, the signing setup, CI, the
  darwin build or the draft stood in the way; the note says which, and a
  failed CI run names its URL. The bead goes to `decide`, to a human.
- `question`: the bead itself is wrong, as a rule its title names no
  version. Note what is wrong first; the bead goes to `decide`, to a human.
