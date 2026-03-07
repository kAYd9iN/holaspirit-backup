#!/usr/bin/env bash
# check-api-schema.sh
#
# Hits all 21 Holaspirit endpoints and extracts the top-level JSON field names
# for each. Writes the result to docs/api-snapshot.json.
#
# Usage (local):
#   HOLASPIRIT_TOKEN=<token> HOLASPIRIT_ORG_ID=<org> ./scripts/check-api-schema.sh
#
# In CI the script is called the same way; HOLASPIRIT_TOKEN and HOLASPIRIT_ORG_ID
# come from repository secrets / variables.
#
# Exit codes:
#   0 — no drift (or snapshot just created)
#   1 — drift detected (CI should open an issue)

set -euo pipefail

TOKEN="${HOLASPIRIT_TOKEN:?HOLASPIRIT_TOKEN must be set}"
ORG_ID="${HOLASPIRIT_ORG_ID:?HOLASPIRIT_ORG_ID must be set}"
BASE="https://app.holaspirit.com/api/organizations/${ORG_ID}"
SNAPSHOT="docs/api-snapshot.json"
TMPFILE="$(mktemp)"
trap 'rm -f "$TMPFILE"' EXIT

fetch_keys() {
  local path="$1"
  local url
  if [[ "$path" == /api/organizations/${ORG_ID} ]]; then
    url="https://app.holaspirit.com${path}"
  else
    url="${BASE}${path#/api/organizations/${ORG_ID}}"
  fi

  local response
  response=$(curl -sf \
    -H "Authorization: Bearer ${TOKEN}" \
    -H "Accept: application/json" \
    --max-time 15 \
    "$url") || { echo "WARN: $url returned error — skipping" >&2; echo "[]"; return; }

  # Paginated responses have a "data" array; non-paginated are plain objects.
  # Extract keys from the first item of "data", or top-level keys otherwise.
  echo "$response" | jq -r '
    if .data | type == "array" and length > 0 then
      .data[0] | keys
    elif .data | type == "object" then
      .data | keys
    else
      keys
    end
  ' 2>/dev/null || echo "[]"
}

declare -A PATHS=(
  [organization]="/api/organizations/${ORG_ID}"
  [circles]="${BASE}/circles"
  [circles-timespent]="${BASE}/circles-timespent"
  [roles]="${BASE}/roles"
  [members]="${BASE}/members"
  [tensions]="${BASE}/tensions"
  [policies]="${BASE}/policies"
  [meetings]="${BASE}/meetings"
  [objectives]="${BASE}/objectives"
  [keyresults]="${BASE}/keyresults"
  [tasks]="${BASE}/tasks"
  [boards]="${BASE}/boards"
  [columns]="${BASE}/columns"
  [checklists]="${BASE}/checklists"
  [metrics]="${BASE}/metrics"
  [publications]="${BASE}/publications"
  [categories]="${BASE}/categories"
  [attachments]="${BASE}/attachments"
  [chartviews]="${BASE}/chartviews"
  [calendars]="${BASE}/calendars"
  [backups]="${BASE}/backups"
)

echo "Fetching API schema from Holaspirit..." >&2

# Build JSON object: { "generated": "...", "endpoints": { "name": [...keys] } }
ENDPOINTS_JSON="{}"
for name in "${!PATHS[@]}"; do
  keys=$(fetch_keys "${PATHS[$name]}")
  ENDPOINTS_JSON=$(echo "$ENDPOINTS_JSON" | jq --arg n "$name" --argjson k "$keys" '.[$n] = $k')
  echo "  $name: $(echo "$keys" | jq -r 'length') fields" >&2
done

# Sort endpoint names for deterministic output
NEW_SNAPSHOT=$(jq -n \
  --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --argjson ep "$ENDPOINTS_JSON" \
  '{"generated": $ts, "endpoints": $ep | to_entries | sort_by(.key) | from_entries}')

echo "$NEW_SNAPSHOT" > "$TMPFILE"

if [[ ! -f "$SNAPSHOT" ]]; then
  cp "$TMPFILE" "$SNAPSHOT"
  echo "Snapshot created at $SNAPSHOT — no baseline existed yet." >&2
  exit 0
fi

# Compare only the "endpoints" part (ignore "generated" timestamp)
OLD_EP=$(jq '.endpoints' "$SNAPSHOT")
NEW_EP=$(jq '.endpoints' "$TMPFILE")

if [[ "$OLD_EP" == "$NEW_EP" ]]; then
  echo "No API drift detected." >&2
  exit 0
fi

# Drift detected — print diff and exit 1 so CI can open an issue
echo "API DRIFT DETECTED:" >&2
diff <(echo "$OLD_EP" | jq -S .) <(echo "$NEW_EP" | jq -S .) >&2 || true

# Write the diff to a file so the CI workflow can use it in the issue body
diff <(echo "$OLD_EP" | jq -S .) <(echo "$NEW_EP" | jq -S .) > drift.diff || true

exit 1
