#!/usr/bin/env bash
# Run BDP's current provider kit at `yardr bdp serve`: seed a yard, serve it, and
# let the kit read it as a Core Scope. This is yardr's BDP conformance check, the
# one CI runs (.github/workflows/ci.yml) at the BDP commit it pins.
#
#   BDP_REPO=~/src/bdp conformance/bdp/probe-core.sh
#
# BDP_REPO is a gastownhall/bdp clone at the commit to check. It is installed and
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
# not-applicable by discovery; only the Core cases can pass or fail, and of the
# seven the kit can run three here. A failed core.scope-discovery leaves every
# case after it not-run, which says nothing of those cases: the kit reads nothing
# else of a Scope whose discovery it refuses.
#
# One line per case is printed. The exit status is 0 when no case failed and the
# three Core read cases passed, and 1 when a case failed, when one of the three
# did not pass, or when the kit gave no verdict. A case that is not-run or
# not-applicable fails nothing, because the kit has no exit 0 yet: its coverage
# is incomplete by design. The three must pass all the same, or a Scope that held
# nothing to read would pass the run. BDP_REPORT names a file to keep the kit's
# JSON report in, and BDP_PORT a port to serve on instead of a free one.
set -euo pipefail
: "${BDP_REPO:?set BDP_REPO to a gastownhall/bdp clone}"
BDP_REPO=$(cd "$BDP_REPO" && pwd)
root=$(cd "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d)   # short path: the yardr server's unix socket lives here
# A free loopback port for each run, so that a run beside this one, or any
# other process on a fixed port, cannot make the server fail before the kit
# starts. The port is free when it is picked and could be taken before serve
# binds it, which a local tool accepts.
port=${BDP_PORT:-$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')}
# The kit dials the Scope URL's own origin and cannot be told to dial elsewhere,
# so the Scope here is the address served.
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
# Adding a source is the operator's, so the operator is named here and the seed
# adds its source as that name. The defaults would not do everywhere: with no
# user in the environment (a container) init makes the operator "operator" and
# a command acts as "unknown".
"$work/yardr" init --harness claude --operator bdp-operator --dir "$work" >/dev/null

# What the kit reads. It takes its subject from the inventories and never reads
# BDP's fixtures, whose ids have changed before, so the script seeds a graph of
# its own: beads that are open, closed and held, joined by blocks edges and one
# related edge, and two links to an outside issue. Both inventories then run
# past one page (the discovery document's limits.page.defaultItems is 50): 45
# more beads, each blocked by demo-a, make 54 beads and 56 links, and the kit
# follows a continuation.
y() { "$work/yardr" "$@" --actor bdp-conformance >/dev/null; }
y bead create A --id demo-a
y bead create B --id demo-b
y bead create C --id demo-c --type feature
y bead close demo-c --reason seeded
y bead create D --id demo-d
y bead create E --id demo-e --type bug
y bead hold demo-e
y bead create F --id demo-f --type decision
y bead create I --id demo-i
y bead create J --id demo-j
y bead create K --id demo-k
y bead close demo-k --reason seeded
# dep add takes the blocker first, then the bead that waits for it.
for pair in a:b c:a c:d e:f a:i c:i c:j k:j; do y dep add "demo-${pair%:*}" "demo-${pair#*:}"; done
y dep add demo-f demo-e --kind related
# A link to an outside issue is the other Link yardr serves: its target is out of
# the Scope and its Type is tracks-<source> or relates. A fresh yard refuses it
# for a host no source has issues on, so the one source the seed needs is added
# first, from a file that names no credential and is never called. The links
# are made by URL, so neither has the thread property that only a hook delivery
# gives. A bead tracks one issue per source, so the two are different issues.
"$work/yardr" source add "$root/conformance/bdp/issue-source.yaml" --actor bdp-operator >/dev/null
y link add demo-a https://tracker.example.com/issue/DEMO-1
y link add demo-b https://tracker.example.com/issue/DEMO-2 --relates
for i in $(seq 45); do y bead create "P$i" --id "demo-p$i" --blocked-by demo-a; done

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
# coverage is incomplete by design) and 3 when it could not observe, and the
# report says which. A higher status is misuse (64), and a crash leaves no
# report: neither is a verdict.
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
rows = r.get("obligations") or []
width = max((len(x["id"]) for x in rows), default=0)
print(f'kit {r.get("kit")}: {r.get("state")}, slice {"complete" if r.get("sliceComplete") else "incomplete"}')
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

# The verdict. These are the Core read cases the kit can run at a Scope with no
# adapter. A case the kit gains is added here when the pin moves to a commit
# that has it.
need = ("core.scope-discovery", "core.inventory-fixed-routes", "core.point-read")
state = {x["id"]: x["state"] for x in rows}
bad = {x["id"]: x["state"] for x in rows + optional if x["state"] in ("fail", "harness-error")}
for i in need:
    if state.get(i) != "pass":
        bad.setdefault(i, state.get(i, "missing"))
if r.get("state") in ("fail", "harness-error") and not bad:
    bad["the kit"] = r["state"]
if bad:
    print("probe: failed: " + ", ".join(f"{i} {s}" for i, s in bad.items()))
    sys.exit(1)
print("probe: passed: " + ", ".join(need))
EOF
