summary: The release group checks the signature against the release key, the archives against the checksums, each linux archive's CI attestation and each darwin archive's signature and notarization.

The release group works this stage (`contrib/release/stage verify`): it
downloads `checksums.txt` and `checksums.txt.minisig` from the release,
checks the signature against the release key, checks the release's
archives against the checksums, and then each archive by who built it.
A linux archive is CI's, and its build-provenance attestation is checked
(`gh attestation verify`), requiring the yardr release workflow and the
tag being verified. A darwin archive is the group's own, built on its Mac,
and has none: its `yardr` must pass `codesign --verify --strict`, be signed
by the identity the group's `env_file` names and be accepted by Gatekeeper
as notarized (`spctl -a -vv -t install`, which asks Apple). `install.sh` is
no archive: it is not counted with the archives, and no attestation is
asked of it. The checksums name it, so the check against them covers its
bytes too (a release made before they did has no line for it, and passes
with a note saying so). It is held to the tag besides: the release's
copy is the tagged commit's own file, byte for byte, and the release has
one where the tag has one. No agent works this stage by hand.

Outcomes:

- `done`: the signature is good, the archives match the checksums,
  `install.sh` is the tag's own, every linux archive's attestation
  verifies, and every darwin archive's binary is signed with the group's
  identity and notarized. The bead goes to `publish`.
- `failed`: the signature is missing or bad; the note carries what minisign
  said. The bead goes back to `sign`, where mckean is asked again.
- `question`: the release itself is wrong (the archives do not match the
  checksums, `install.sh` is not the tag's own, a linux archive's
  attestation does not verify, a darwin binary is not signed and
  notarized) or the check could not run at all. The note says which. The
  bead goes to `decide`, to a human: a release whose archives are wrong is
  remade by hand (the release and the tag deleted, the bead back to
  `build`).
