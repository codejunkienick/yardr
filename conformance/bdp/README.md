# BDP conformance for yardr

`run.sh` runs [BDP](https://github.com/gastownhall/bdp)'s historical Read-profile
conformance suite, black-box over HTTP, against `yardr bdp serve`. BDP no
longer has a standalone Read profile; reads are part of Core. This historical
suite stops before testing it (the last paragraph says why), so check out
caf5ca7, the last commit verified:

```sh
git clone https://github.com/gastownhall/bdp ~/src/bdp
git -C ~/src/bdp checkout caf5ca7
(cd ~/src/bdp && pnpm install --frozen-lockfile && pnpm build)
BDP_REPO=~/src/bdp conformance/bdp/run.sh
```

BDP pins its toolchain (`package.json`: Node 24.16.0, pnpm 11.20.0) and its
`.npmrc` sets `engine-strict`, so the install refuses any other Node.

How it works:

1. `seed.py` builds a throwaway yard holding the graph BDP's bd-backed lane
   seeds (`seedBdWorkspace`): beads A–F, I, J, K, their `blocks` links and the
   E→F `related` link, with C and K closed and E deferred (held).
2. `yardr bdp serve` serves it under the Scope `https://scope.example/acme/`.
3. `make-fixture.py` derives the fixture from BDP's bd fixture
   (`read-bdpbd-v1.json`). Only implementation-specific literals change: bd's
   link ids become yard's, bd's content hashes become yard's revisions, and
   demo-a's property body and revision provenance become yard's. Every membership,
   direction, readiness and ordering expectation stays bd's. Yard's demo-a
   row and revision provenance are pinned in the script, not copied from the server
   under test; only run-time values (link ids and revisions, demo-a's uuid
   and timestamps) are read from it, each checked for its shape. The type
   inventory is bd's plus the Types a fresh yard has for links to outside
   issues (`relates`, `tracks`). They are pinned in the
   script too, not read from the server, so a Type that appears or goes
   fails `read.types.inventory`: a source the yard declares adds its
   `tracks-<source>`, and the script's list changes with it.
4. `external-read.test.mts` runs the suite against the live server.

Journals and memories are not served, nor links touching them: they are not
work, and a bd client would count an open one as ready.

Of the catalog's 49 rows at caf5ca7, 31 run. The rest, by what keeps them out:

- Not applicable, 8: the row's applicability names a capability the fixture
  does not declare. Aliases (`read.alias.resolution`), disclosure
  (`read.disclosure.gate`, `.gone`), owned links (`read.owned.closure`,
  `read.owned-wildcard.closure`, `.present-entries`) and content-derived
  revisions (`read.numeric-model.declared-token-model`) yard does not
  claim. `read.attribution.link` it could: yard's links carry revision
  provenance, but the capability list is bd's, which has no
  `attribution-links-v1`.
- In-process only, 11 (three of them also among the 8): rows with a
  `lifecycle` action, which BDP drives through test hooks no live server
  has. These are `read.discovery.optional-limits`,
  `read.collections.selector`, `read.collections.pagination-snapshot`,
  `read.collections.cursor-errors`, `read.bead-links.pagination`,
  `read.resource.not-found-nondisclosure`, `read.http.problem-table`,
  `read.scope.restore-identity`, `read.disclosure.gate`,
  `read.owned.closure`, and `read.owned-wildcard.closure`. BDP calls them
  self-certified and runs them inside its own two servers.
- No setup on a live server, 2: `read.http.internal-fault` needs the server
  started with a fault injected, and `read.resource.external-endpoint`
  needs a seeded link to a URI that is no issue (yard's only out-of-Scope
  endpoints are the issues of [links](../../docs/links.md), tested in
  internal/bdp).

Last run (BDP caf5ca7, 2026-11-07, yardr 332543b0): 31 of 31 runnable scenarios pass.
BDP's own `bdp bd ready --json` client, pointed at `yardr bdp serve`, returns
J, D, A: the same ready set bd itself does.

BDP's main 029d176, 7 Nov 2026, has no standalone Read profile: reads are part
of Core. BDP says its historical conformance seals "do not qualify the new
composition". The historical suite stops during fixture seeding against it.
Since BDP's commit 8f30b0e of 4 Nov 2026 the fixtures name beads without the
`beads/` prefix, so `seed.py` stops at the first bead with an `IndexError` on
`b["localId"].split("/", 1)[1]`, before the BDP scenarios run. Nothing here has
been tested against BDP's current Core, whose inventories are `bdp/beads` and
`bdp/links`. yardr serves neither. BDP's current 19-case Core/ordinary Write
provider kit requires a trusted adapter and is a bounded interoperability
slice, not a replacement full-conformance result.
[docs/bdp.md](../../docs/bdp.md#conformance) says what yardr claims.
