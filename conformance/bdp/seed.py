"""Seed a yard with the graph BDP's bd conformance seed creates (seedBdWorkspace)."""
import json, os, subprocess, sys
fixture, yard = sys.argv[1], sys.argv[2]
f = json.load(open(fixture))
# BDP's fixtures name ids "beads/demo-a" up to its commit 8f30b0e and "demo-a" since; take either.
def bare(i): return i.split("/", 1)[1] if i.startswith(("beads/", "links/")) else i
ids = {bare(b["localId"]) for b in f["beads"]}
def y(*args):
    r = subprocess.run([yard, *args, "--json", "--actor", "bdp-conformance"], capture_output=True, text=True)
    if r.returncode: sys.exit(f"yardr {' '.join(args)}: {r.stdout}{r.stderr}")
    return json.loads(r.stdout) if r.stdout.strip() else None
for b in f["beads"]:
    p = b["properties"]; bid = bare(b["localId"])
    y("bead", "create", p["title"], "--id", bid, "--type", p.get("issue_type", "task"), "--priority", str(p.get("priority", 2)))
    if p["status"] == "closed": y("bead", "close", bid, "--reason", "fixture")
    elif p["status"] == "deferred": y("bead", "hold", bid)
skipped = []
for l in f["links"]:
    s, t = l["source"], l["target"]
    if not (isinstance(s, str) and isinstance(t, str) and bare(s) in ids and bare(t) in ids):
        skipped.append(l["localId"]); continue
    kind = l["type"].rsplit("/", 1)[1]
    # bd's conformance seed: reference "relates" is bd's "related"; no cites.
    if kind == "cites":
        skipped.append(l["localId"]); continue
    kind = {"relates": "related"}.get(kind, kind)
    # BDP: source depends on target. yard: dep add <blocker> <waiting>.
    y("dep", "add", bare(t), bare(s), "--kind", kind)
print("skipped links yard cannot express (external/pinned endpoints):", skipped)
