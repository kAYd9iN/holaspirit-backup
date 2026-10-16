#!/usr/bin/env bash
# check-api-schema.sh
#
# Compares the *published* OpenAPI contract of the Talkspirit Partner API
# (https://partner-api.talkspirit.com/v1/openapi.json — public, exempt from rate
# limiting) against the stored baseline (docs/api-snapshot.json). No API key or
# other credentials are required.
#
# Tracked: every route the tool actually calls (see internal/api/endpoints.go) —
# its response fields and its query parameters.
# Sentinel values in the snapshot:
#   __ENDPOINT_MISSING__   — route no longer documented (removed/deprecated)
#   __SCHEMA_UNPARSEABLE__ — response schema shape changed beyond recognition
#
# Usage (local): ./scripts/check-api-schema.sh
#
# Exit codes:
#   0 — no drift (or baseline just created)
#   1 — drift detected (CI opens a PR)
#   2 — spec download failed — no snapshot written

set -euo pipefail

SNAPSHOT="docs/api-snapshot.json"
UA="talkspirit-backup-api-check (+https://github.com/kAYd9iN/holaspirit-backup)"
SPEC_URL="https://partner-api.talkspirit.com/v1/openapi.json"

# Pick a python that actually runs (on Windows, `python3` may resolve to the
# Microsoft Store alias stub, which only prints an install hint).
PYTHON=""
for candidate in python3 python; do
  if "$candidate" -c "pass" >/dev/null 2>&1; then PYTHON="$candidate"; break; fi
done
[[ -n "$PYTHON" ]] || { echo "ERROR: no working python3 found" >&2; exit 2; }

WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

echo "Fetching the published Talkspirit Partner API contract..." >&2
curl -sfL -A "$UA" --max-time 60 "$SPEC_URL" -o "$WORKDIR/openapi.json"   || { echo "ERROR: failed to download $SPEC_URL" >&2; exit 2; }

"$PYTHON" - "$WORKDIR/openapi.json" > "$WORKDIR/snapshot.json" <<'PY'
import json, sys, datetime

try:
    spec = json.load(open(sys.argv[1], encoding="utf-8"))
except ValueError:
    sys.stderr.write("ERROR: the OpenAPI contract is not valid JSON\n")
    sys.exit(2)

def deref(obj, depth=0):
    while isinstance(obj, dict) and "$ref" in obj and depth < 30:
        target = spec
        for part in obj["$ref"].lstrip("#/").split("/"):
            target = target[part]
        obj = target
        depth += 1
    return obj

def props_of(schema, depth=0):
    """Resolve a schema to its property map, merging allOf/anyOf members."""
    if depth > 10:
        return {}
    schema = deref(schema)
    props = dict(schema.get("properties", {}))
    for key in ("allOf", "anyOf", "oneOf"):
        for member in schema.get(key, []):
            props.update(props_of(member, depth + 1))
    return props

def describe(route):
    item = spec.get("paths", {}).get(route)
    if not item or "get" not in item:
        return {"fields": ["__ENDPOINT_MISSING__"], "params": []}
    get = item["get"]
    params = sorted(p.get("name", "?") for p in get.get("parameters", []) if p.get("in") == "query")
    try:
        schema = get["responses"]["200"]["content"]["application/json"]["schema"]
        props = props_of(schema)
        # Partner API list responses: {data: [...], pagination: {...}}
        if "data" in props:
            data = deref(props["data"])
            props = props_of(data.get("items", {})) if data.get("type") == "array" else props_of(data)
        return {"fields": sorted(props.keys()) or ["__SCHEMA_UNPARSEABLE__"], "params": params}
    except (KeyError, TypeError):
        return {"fields": ["__SCHEMA_UNPARSEABLE__"], "params": params}

# Every route internal/api/endpoints.go calls: backup file name -> OpenAPI
# route. Must match endpoints.go (enforced by the Go test
# TestDriftCheckCoversAllEndpoints).
ENDPOINTS = {
    "whoami": "/v1/whoami",
    "custom-fields-circle": "/v1/custom-fields",
    "custom-fields-role": "/v1/custom-fields",
    "custom-fields-user": "/v1/custom-fields",
    "circles": "/v1/circles",
    "circle-members": "/v1/circles/{id}/members",
    "roles": "/v1/roles",
    "role-templates": "/v1/role-templates",
    "users": "/v1/users",
    "memberships": "/v1/memberships",
    "tensions": "/v1/tensions",
    "meetings": "/v1/meetings",
    "meeting-agenda-items": "/v1/meetings/{id}/agenda-items",
    "tasks": "/v1/tasks",
    "subtasks": "/v1/tasks",
    "task-comments": "/v1/tasks/{id}/comments",
    "projects": "/v1/projects",
    "sections": "/v1/sections",
    "labels": "/v1/labels",
    "project-comments": "/v1/projects/{id}/comments",
    "goals": "/v1/goals",
    "goal-key-results": "/v1/goals/{id}/key-results",
    "goal-comments": "/v1/goals/{id}/comments",
    "time-periods": "/v1/time-periods",
    "documents": "/v1/documents",
    "document-content": "/v1/documents/{id}/content",
    "document-comments": "/v1/documents/{id}/comments",
    "audit-logs": "/v1/audit-logs",
}

# The contract names path parameters per resource ({circle_id}, {goal_id});
# match routes with any parameter name.
def find_route(route):
    want = route.split("/")
    for p in spec.get("paths", {}):
        got = p.split("/")
        if len(got) == len(want) and all(a == b or (a.startswith("{") and b.startswith("{")) for a, b in zip(got, want)):
            return p
    return route

endpoints = {name: describe(find_route(route)) for name, route in sorted(ENDPOINTS.items())}

# Routes published but not backed up: new data the tool may need to learn.
# Role members and per-user memberships are already in roles.json
# (?include=members) and memberships.json.
known = {find_route(r) for r in ENDPOINTS.values()}
known |= {find_route("/v1/roles/{id}/members"), find_route("/v1/users/{id}/memberships")}
untracked = sorted(p for p, item in spec.get("paths", {}).items()
                   if "get" in item and p not in known and p.startswith("/v1/")
                   and not p.endswith("_id}") and "/structure-exports" not in p)
endpoints["__untracked_get_routes__"] = {"fields": untracked, "params": []}

print(json.dumps({
    "generated": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    "sources": {
        "openapi": {"url": "partner-api.talkspirit.com/v1/openapi.json", "version": spec.get("info", {}).get("version", "?")},
    },
    "endpoints": endpoints,
}, indent=2))
PY

if [[ ! -f "$SNAPSHOT" ]]; then
  cp "$WORKDIR/snapshot.json" "$SNAPSHOT"
  echo "Snapshot created at $SNAPSHOT — no baseline existed yet." >&2
  exit 0
fi

extract_endpoints() {
  "$PYTHON" -c "import json,sys; print(json.dumps(json.load(open(sys.argv[1], encoding='utf-8'))['endpoints'], indent=2, sort_keys=True))" "$1"
}
OLD_EP=$(extract_endpoints "$SNAPSHOT")
NEW_EP=$(extract_endpoints "$WORKDIR/snapshot.json")

if [[ "$OLD_EP" == "$NEW_EP" ]]; then
  echo "No API drift detected." >&2
  exit 0
fi

echo "API DRIFT DETECTED:" >&2
diff <(echo "$OLD_EP") <(echo "$NEW_EP") >&2 || true
diff <(echo "$OLD_EP") <(echo "$NEW_EP") > drift.diff || true

# Update the snapshot in place — CI commits it as part of the drift PR.
cp "$WORKDIR/snapshot.json" "$SNAPSHOT"

exit 1
