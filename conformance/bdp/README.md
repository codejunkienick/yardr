# BDP conformance for yardr

`probe-core.sh` runs [BDP](https://github.com/gastownhall/bdp)'s current provider
kit at `yardr bdp serve` and passes or fails on what the kit finds. CI runs it at
the BDP commit pinned below. To run it yourself:

```sh
git clone https://github.com/gastownhall/bdp ~/src/bdp
git -C ~/src/bdp checkout a4fd83a2ce3bc77c28f98c5f023ade997e4a1642
BDP_REPO=~/src/bdp conformance/bdp/probe-core.sh
```

The script installs and builds BDP in place (`pnpm install --frozen-lockfile &&
pnpm build`) and needs go, python3, curl, node and pnpm. BDP pins its Node in
`.nvmrc` and its pnpm in `package.json`. pnpm only warns on another Node, so the
report prints the Node the kit ran under beside BDP's pin. Check that they match
before you trust a run.

## What a run does

1. It builds yardr from this checkout into a temp dir and starts a fresh yard
   there. Your `~/.yardr` is not touched.
2. It seeds 54 beads and 54 links. Nine beads, some open, some closed and one
   held, are joined by `blocks` edges and one `related` edge. Each of the other 45
   is blocked by the first of the nine, so each inventory runs past its first page
   of 50 and the kit follows a continuation. The kit learns what the Scope holds
   from its inventories, so the script seeds a graph of its own and reads none of
   BDP's fixtures.
3. It serves the yard with `yardr bdp serve` on a free loopback port. The Scope
   is `http://127.0.0.1:<port>/acme/`, because the kit dials the Scope's own
   origin and cannot be pointed elsewhere.
4. It runs `node scripts/current-provider-kit.mjs --scope <Scope>`. The `--scope`
   mode needs no adapter, and without `--allow-mutation` the kit sends nothing
   that changes the yard.
5. It prints one line per row, then exits 0 when no row failed and the three Core
   read rows below passed, and 1 otherwise.

`BDP_REPORT` keeps the kit's JSON report in a file, and `BDP_PORT` names a port to
serve on.

## What it covers

At the pinned commit the kit has 28 required rows and 33 optional ones. Against a
Scope that advertises Core, with no adapter and no mutation:

- 3 required rows pass: `core.scope-discovery`, `core.inventory-fixed-routes` and
  `core.point-read`. The script requires all three.
- 5 required rows do not run. The kit has no case yet for
  `core.collections-and-native-pagination`, `core.link-and-alias-reads` or
  `core.http-and-selection`. `core.deleted-absence` needs a Write delete or an
  adapter's witness of a deleted resource, and `--scope` has neither.
  `coverage.complete-obligation-audit` never runs.
- 20 required rows are not applicable, because discovery says `core`: 16 `write.*`
  cases and 4 Write families. yardr serves no Write.
- The 33 optional rows (Callables, Issue package) are not applicable too.

BDP calls the kit a vertical slice. Its report is `incomplete` and says
`claimEligible: false`. A pass shows that discovery, the two inventories and a
point read are what the kit asserts of Core, and shows nothing of the rows it has
no case for. [docs/bdp.md](../../docs/bdp.md#conformance) says what yardr claims.

## The pin

`BDP_REF` in the `conformance` job of `.github/workflows/ci.yml` and `release.yml`
is the BDP commit CI checks out, and it is the one recorded here:
`a4fd83a2ce3bc77c28f98c5f023ade997e4a1642`, of 7 Nov 2026. BDP moves and the kit
gains rows, so a newer commit can fail where this one passes. To move the pin, run
the script at the new commit first, then change both workflows and this file in
one commit. A step of the job fails when this file does not contain `BDP_REF`, so
the two cannot drift apart unseen.

`pnpm spec:revision` in the BDP checkout prints the commit that `SpecRevision` in
`internal/bdp` should name. It is older than the pin when BDP's later commits left
Core's text alone.

Last run: BDP a4fd83a2, yardr 13b5d5d7, 8 Nov 2026. 3 pass, 5 not run, 53 not
applicable (20 required, 33 optional), 0 fail.

## History

Until the move to Core this directory held BDP's Read-profile conformance suite
run over HTTP against `yardr bdp serve`. It was retired and not ported. Its rows
are the Read profile's: BDP's `read.discovery.document` asks with
`Accept: application/json` and asserts `bdpVersion` 0 and the Read discovery
schema, which a Core server (0.1, with a media type of its own) fails by
construction. BDP keeps those rows as historical evidence and says they do not
qualify Core. A port would be new rows of our own, and the Core cases BDP has not
implemented in its kit are the ones that would cover the same ground: pagination,
Link reads and HTTP. `git log --diff-filter=D --stat -- conformance/bdp/run.sh`
finds the commit that removed the suite, and
[docs/bdp.md](../../docs/bdp.md#conformance) has its last result.
