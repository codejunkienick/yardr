#!/usr/bin/env bash
# Drive probe-core.sh through the paths a healthy server never shows.
#
#   conformance/bdp/probe-core-test.sh
#
# The kit is a stand-in that prints a canned report in the real kit's shape and
# exits with a status of the scenario's choosing; pnpm is a stand-in too. The
# script itself is the committed one, run under env -i, so it really builds yardr,
# seeds the 54-bead yard and serves it before the stand-in kit is asked. The
# stand-in reads the served link inventory first and fails unless the seed's two
# links to an outside issue are in it: the real kit validates every link it is
# served but cannot tell that these were never seeded. Each scenario says what the
# exit status must be. Needs bash, go, python3, curl, jq and node.
set -u
ROOT=${ROOT:-$PWD}
script=$ROOT/conformance/bdp/probe-core.sh
[ -x "$script" ] || { echo "no $script"; exit 2; }
tmp=$(mktemp -d)
trap 'python3 -c "import shutil, sys; shutil.rmtree(sys.argv[1], ignore_errors=True)" "$tmp"' EXIT
mkdir -p "$tmp/bdp/scripts" "$tmp/bin" "$tmp/scn"
echo 24.22.1 >"$tmp/bdp/.nvmrc"

cat >"$tmp/bin/pnpm" <<'EOF'
#!/bin/sh
[ -z "$PNPM_FAIL" ] || { echo "ERR_PNPM_UNSUPPORTED_ENGINE stand-in refusal" >&2; exit 1; }
exit 0
EOF
chmod +x "$tmp/bin/pnpm"

cat >"$tmp/bdp/scripts/current-provider-kit.mjs" <<'EOF'
// Stand-in kit: prints the scenario file KIT_SCENARIO and exits with KIT_EXIT, or crashes with no report when KIT_EMPTY is set. Before it prints, it follows the link inventory and exits 70 unless the seed's links to an outside issue are served.
import { readFileSync } from "node:fs";
if (process.argv[2] !== "--scope" || process.argv.length !== 4) { console.error("stand-in: unexpected arguments " + process.argv.slice(2).join(" ")); process.exit(64); }
if (process.env.KIT_EMPTY) { console.error("stand-in kit crashed"); process.exit(1); }
const scope = process.argv[3];
const links = [];
for (let next = new URL("bdp/links", scope).href; next; ) {
  const page = await (await fetch(next)).json();
  links.push(...page.items);
  next = page.next && new URL(page.next, scope).href;
}
const outside = links.filter((l) => !l.target.startsWith(scope)).map((l) => `${l.type.slice(scope.length)} ${l.source.slice(scope.length)} ${l.target}`).sort();
const seeded = ["types/relates beads/demo-b https://tracker.example.com/issue/DEMO-2", "types/tracks-tracker beads/demo-a https://tracker.example.com/issue/DEMO-1"];
if (JSON.stringify(outside) !== JSON.stringify(seeded)) { console.error("stand-in: the links to an outside issue are " + JSON.stringify(outside) + ", want " + JSON.stringify(seeded)); process.exit(70); }
process.stdout.write(readFileSync(process.env.KIT_SCENARIO, "utf8"));
process.exitCode = Number(process.env.KIT_EXIT);
EOF

# A report as the real kit prints it for a healthy Core Scope (its fields, fewer rows).
cat >"$tmp/base.json" <<'EOF'
{"kit":"current-core-ordinary-write-slice-1","state":"incomplete","sliceComplete":false,"qualificationComplete":false,"claimEligible":false,
 "inputs":{"actualNode":"24.22.1"},"discovered":{"profile":"core","capabilities":[]},
 "obligations":[
  {"id":"core.scope-discovery","required":true,"state":"pass"},
  {"id":"core.point-read","required":true,"state":"pass"},
  {"id":"core.inventory-fixed-routes","required":true,"state":"pass"},
  {"id":"core.deleted-absence","required":true,"state":"not-run","reason":"No Write delete and no adapter witness"},
  {"id":"core.http-and-selection","required":true,"state":"not-run","reason":"not implemented in this slice"},
  {"id":"write.create","required":false,"state":"not-applicable","reason":"Not applicable by discovery"}],
 "optional":[{"id":"callables.cb-1","row":"CB-1","state":"not-applicable"},{"id":"issue.is-1","row":"IS-1","state":"not-applicable"}]}
EOF
b=$tmp/base.json
jq '.' "$b" >"$tmp/scn/pass.json"
jq '.obligations|=map(if .id=="core.point-read" then .state="fail"|.reason="Public assertion failed for core.point-read" else . end)|.state="fail"' "$b" >"$tmp/scn/fail-point-read.json"
jq '.obligations|=map(if .id=="core.point-read" then .state="not-run"|.reason="The Scope holds no Bead to read" else . end)' "$b" >"$tmp/scn/notrun-point-read.json"
jq '.optional[0].state="fail"|.optional[0].reason="stand-in optional failure"|.state="fail"' "$b" >"$tmp/scn/optional-fail.json"
jq '.obligations|=map(if .id=="core.scope-discovery" then .state="fail"|.reason="Public assertion failed for core.scope-discovery" elif (.id|startswith("core.")) then .state="not-run"|.reason="Prerequisite did not complete" else . end)|.state="fail"' "$b" >"$tmp/scn/discovery-fails.json"
jq -n '{kit:"current-core-ordinary-write-slice-1",state:"harness-error",sliceComplete:false,harnessFailure:"stand-in: provision failed"}' >"$tmp/scn/harness-error.json"

bad=0
# run <name> <kit exit> <expected script exit> [VAR=value ...]
run() {
  name=$1; kitexit=$2; want=$3; shift 3
  env -i HOME="$HOME" PATH="$tmp/bin:$PATH" GOCACHE="$(go env GOCACHE)" GOFLAGS=-trimpath \
    BDP_REPO="$tmp/bdp" KIT_SCENARIO="$tmp/scn/$name.json" KIT_EXIT="$kitexit" "$@" \
    /bin/bash "$script" >"$tmp/out" 2>"$tmp/err"
  got=$?
  verdict=ok; [ "$got" = "$want" ] || { verdict=WRONG; bad=1; }
  printf '%-5s%-20s %-12s kit exit %s, script exit %s (want %s)\n' "$verdict" "$name" "$*" "$kitexit" "$got" "$want"
  grep -hE '^probe: (passed|failed|the kit|BDP did not)|^harnessFailure|^stand-in' "$tmp/out" "$tmp/err" | sed 's/^/       /'
}
run pass              2 0
run fail-point-read   1 1
run notrun-point-read 2 1
run optional-fail     1 1
run discovery-fails   1 1
run harness-error     3 1
run pass              1 1 KIT_EMPTY=1
run pass              2 1 PNPM_FAIL=1
exit $bad
