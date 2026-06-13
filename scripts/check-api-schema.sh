#!/usr/bin/env bash
# check-api-schema.sh
#
# Compares Holaspirit's *published* OpenAPI spec against the stored baseline
# (docs/api-snapshot.json). The spec is embedded in the public API doc page
# (https://app.holaspirit.com/api/doc/), so no token, organization, or other
# credentials are required — the check runs entirely against public docs.
#
# Tracked: every endpoint the tool actually calls (see internal/api/endpoints.go).
# Sentinel values in the snapshot:
#   __ENDPOINT_MISSING__   — endpoint no longer documented (removed/deprecated)
#   __SCHEMA_UNPARSEABLE__ — response schema shape changed beyond recognition
#
# Usage (local): ./scripts/check-api-schema.sh
#
# Exit codes:
#   0 — no drift (or baseline just created)
#   1 — drift detected (CI opens a PR)
#   2 — spec download/extraction failed — no snapshot written

set -euo pipefail

SNAPSHOT="docs/api-snapshot.json"
UA="holaspirit-backup-api-check (+https://github.com/kAYd9iN/holaspirit-backup)"
DOC_URL="https://app.holaspirit.com/api/doc/"

# Pick a python that actually runs (on Windows, `python3` may resolve to the
# Microsoft Store alias stub, which only prints an install hint).
PYTHON=""
for candidate in python3 python; do
  if "$candidate" -c "pass" >/dev/null 2>&1; then PYTHON="$candidate"; break; fi
done
[[ -n "$PYTHON" ]] || { echo "ERROR: no working python3 found" >&2; exit 2; }

WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

echo "Fetching published Holaspirit API documentation..." >&2
curl -sfL -A "$UA" --max-time 60 "$DOC_URL" -o "$WORKDIR/doc.html" \
  || { echo "ERROR: failed to download API doc page ($DOC_URL)" >&2; exit 2; }

"$PYTHON" - "$WORKDIR/doc.html" > "$WORKDIR/snapshot.json" <<'PY'
import json, sys, datetime

html = open(sys.argv[1], encoding="utf-8").read()

# The OpenAPI spec is embedded in the Swagger UI bootstrap: `spec: {...}`.
# Extract it with a string-aware balanced-brace scan.
marker = html.find("spec: {")
if marker < 0:
    sys.stderr.write("ERROR: embedded OpenAPI spec not found in doc page\n")
    sys.exit(2)
start = html.find("{", marker)
depth, in_str, esc, end = 0, False, False, -1
for j in range(start, len(html)):
    c = html[j]
    if in_str:
        if esc:
            esc = False
        elif c == "\\":
            esc = True
        elif c == '"':
            in_str = False
    else:
        if c == '"':
            in_str = True
        elif c == "{":
            depth += 1
        elif c == "}":
            depth -= 1
            if depth == 0:
                end = j
                break
if end < 0:
    sys.stderr.write("ERROR: could not find end of embedded spec\n")
    sys.exit(2)
spec = json.loads(html[start:end + 1])

def deref(obj, depth=0):
    while isinstance(obj, dict) and "$ref" in obj and depth < 30:
        target = spec
        for part in obj["$ref"].lstrip("#/").split("/"):
            target = target[part]
        obj = target
        depth += 1
    return obj

def props_of(schema, depth=0):
    """Resolve a schema to its property map, merging allOf members."""
    if depth > 10:
        return {}
    schema = deref(schema)
    props = dict(schema.get("properties", {}))
    for member in schema.get("allOf", []):
        props.update(props_of(member, depth + 1))
    return props

def fields(path):
    item = spec.get("paths", {}).get(path)
    if not item or "get" not in item:
        return ["__ENDPOINT_MISSING__"]
    try:
        schema = item["get"]["responses"]["200"]["content"]["application/json"]["schema"]
        props = props_of(schema)
        # Holaspirit responses wrap payloads: {data: [...]|{...}, linked, meta, pagination}
        if "data" in props:
            data = deref(props["data"])
            if data.get("type") == "array":
                props = props_of(data.get("items", {}))
            else:
                props = props_of(data)
        return sorted(props.keys()) or ["__SCHEMA_UNPARSEABLE__"]
    except (KeyError, TypeError):
        return ["__SCHEMA_UNPARSEABLE__"]

# Every endpoint internal/api/endpoints.go calls, mapped to its spec path.
BASE = "/api/organizations/{organization_id}"
NAMES = [
    "organization", "circles", "circles-timespent", "roles", "members",
    "tensions", "policies", "meetings", "okrs", "node-okrs", "tasks",
    "boards", "columns", "checklists", "metrics", "publications",
    "categories", "attachments", "chartviews", "calendars", "backups",
]
endpoints = {
    name: fields(BASE if name == "organization" else f"{BASE}/{name}")
    for name in sorted(NAMES)
}

print(json.dumps({
    "generated": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    "sources": {
        "doc": {"url": "app.holaspirit.com/api/doc/", "version": spec.get("info", {}).get("version", "?")},
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
