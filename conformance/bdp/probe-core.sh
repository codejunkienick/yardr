#!/usr/bin/env bash
# Probe BDP's current Core reads against `yardr bdp serve`: seed a yard from the
# fixture of a BDP checkout, serve it, and run BDP's provider kit at it.
#
#   BDP_REPO=~/src/bdp conformance/bdp/probe-core.sh
#
# BDP_REPO is a gastownhall/bdp clone at the commit to probe. It is installed and
# built in place, as the kit's doc says (`pnpm install --frozen-lockfile && pnpm
# build`). BDP pins the Node (its .nvmrc) and the pnpm (its packageManager) to
# use, but pnpm only warns on another Node, so the report names the Node the kit
# ran under beside the one BDP pins. Needs go, python3, curl, node and pnpm.
# Apart from that build, everything runs in a temp dir on loopback; your
# ~/.yardr is never touched.
#
# The kit runs in its `--scope` mode: no adapter, and no --allow-mutation, which
# would make it create records in the yard. So nothing is sent that changes the
# yard, the durability and graph cases stay not-run and the Write cases are
# not-applicable or not-run; only the Core cases can pass or fail. A failed
# core.scope-discovery leaves every case after it not-run, which says nothing of
# those cases: the kit reads nothing else of a Scope whose discovery it refuses.
#
# One line per case is printed. A case that fails is the finding, not an error
# of the probe: the exit status is 0 once the kit has given a verdict and 1 when
# it has not. BDP_REPORT names a file to keep the kit's JSON report in, and
# BDP_PORT a port to serve on instead of a free one.
set -euo pipefail
: "${BDP_REPO:?set BDP_REPO to a gastownhall/bdp clone}"
BDP_REPO=$(cd "$BDP_REPO" && pwd)
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
work=$(mktemp -d)   # short path: the yardr server's unix socket lives here
# A free loopback port for each run, as run.sh picks one: free when picked, and
# could be taken before serve binds it, which a local tool accepts.
port=${BDP_PORT:-$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')}
# The kit dials the Scope URL's own origin and cannot be told to dial elsewhere,
# so unlike run.sh's, the Scope here is the address served.
scope=http://127.0.0.1:$port/acme/
report=${BDP_REPORT:-$work/report.json}

# Run from inside a yardr session (an agent's pane), the session's variables
# lead back to its yard and its listeners, and would make every seeded move a
# stale move of that session's bead.
while IFS= read -r v; do unset "$v"; done < <(compgen -e YARDR_)
export YARDR_HOME=$work/home
# The seed's first command that needs the store starts the server, and a loaded
# machine does not always leave it the real ten seconds to answer in.
export YARDR_TEST_START_WAIT=120s
cleanup() {
  if [[ -n ${serve_pid:-} ]]; then
    kill "$serve_pid" 2>/dev/null || true
    wait "$serve_pid" 2>/dev/null || true
  fi
  "$work/yardr" server stop >/dev/null 2>&1 || true
  rm -rf "${work:?}"
}
trap cleanup EXIT

# First, so that a Node or pnpm that BDP refuses fails before anything runs.
echo "probe: pnpm install and build in $BDP_REPO" >&2
if ! (cd "$BDP_REPO" && pnpm install --frozen-lockfile && pnpm build) >"$work/bdp-build.log" 2>&1; then
  tail -n 30 "$work/bdp-build.log" >&2
  echo "probe: BDP did not install and build; its package.json pins the Node and pnpm to use" >&2
  exit 1
fi

(cd "$root" && go build -o "$work/yardr" .)
# A fresh home has no yard yet, and nothing here has a terminal to onboard in.
"$work/yardr" init --harness claude --dir "$work" >/dev/null
python3 "$here/seed.py" "$BDP_REPO/packages/conformance/fixtures/read-reference-v1.json" "$work/yardr" >/dev/null
"$work/yardr" bdp serve --addr "127.0.0.1:$port" --scope "$scope" >"$work/serve.log" 2>&1 &
serve_pid=$!
for _ in $(seq 100); do
  curl -fs -o /dev/null "$scope" && break
  kill -0 "$serve_pid" 2>/dev/null || break
  sleep 0.1
done
if ! curl -fs -o /dev/null "$scope"; then
  tail -n 20 "$work/serve.log" >&2
  echo "probe: yardr bdp serve did not answer at $scope" >&2
  exit 1
fi

kit_rc=0
(cd "$BDP_REPO" && node scripts/current-provider-kit.mjs --scope "$scope") >"$report" 2>"$work/kit.err" || kit_rc=$?
# The kit exits 1 when a case failed, 2 when none did (it has no exit 0 yet: its
# coverage is incomplete by design) and 3 when it could not observe. A higher
# status is misuse (64), and a crash leaves no report: neither is a verdict.
if [[ $kit_rc -gt 3 || ! -s $report ]]; then
  cat "$work/kit.err" >&2
  echo "probe: the kit gave no report (exit $kit_rc)" >&2
  exit 1
fi

bdp_commit=$(git -C "$BDP_REPO" rev-parse --short HEAD 2>/dev/null || echo unknown)
yardr_commit=$(git -C "$root" rev-parse --short HEAD 2>/dev/null || echo unknown)
echo "probe: BDP $bdp_commit, yardr $yardr_commit, Scope $scope"
python3 -I - "$report" "$BDP_REPO" <<'EOF'
import json, sys
r = json.load(open(sys.argv[1]))
rows = r["obligations"]
width = max(len(x["id"]) for x in rows)
print(f'kit {r["kit"]}: {r["state"]}, slice {"complete" if r["sliceComplete"] else "incomplete"}')
try:
    pin = open(sys.argv[2] + "/.nvmrc").read().strip()
except OSError:
    pin = "unknown"
node = r.get("inputs", {}).get("actualNode", "unknown")
print(f"node {node}" + ("" if node == pin else f" (BDP pins {pin})"))
if "discovered" in r:
    print("discovered:", json.dumps(r["discovered"]))
for k in ("harnessFailure", "cleanupFailure"):
    if k in r:
        print(f"{k}: {r[k]}")
for x in rows:
    print(f'  {x["state"]:<14} {x["id"]:<{width}}  {x.get("reason", "")}')
# The Callables and Issue package rows never change the slice; say how they stood.
optional = r.get("optional") or []
if optional:
    tally = {}
    for x in optional:
        tally[x["state"]] = tally.get(x["state"], 0) + 1
    print("  optional rows (Callables, Issue package):", ", ".join(f"{n} {s}" for s, n in sorted(tally.items())))
    for x in optional:
        if x["state"] not in ("not-run", "not-applicable"):
            print(f'  {x["state"]:<14} {x["id"]:<{width}}  {x.get("reason") or x.get("note") or ""}')
EOF
if [[ $kit_rc -eq 3 ]]; then
  echo "probe: the kit could not observe (harness-error)" >&2
  exit 1
fi
