#!/usr/bin/env bash
# sonar-duplications.sh — find which files SonarQube's duplication metric
# actually points to, working around a Browse-restricted analysis token.
#
# The project-level `duplicated_lines`/`duplicated_blocks`/`duplicated_files`
# measures (shown by scripts/sonar-scan.sh) are just totals — the endpoints
# that normally break them down per file (api/components/tree,
# api/duplications/show, api/navigation/component, api/project_analyses/search)
# all require "Browse" permission on the project. A pure analysis token
# (global perms just ["provisioning","scan"], the common case for a
# CI/scanner-only token) gets "Insufficient privileges" on every one of them.
#
# Workaround: api/measures/component accepts a *direct component key*
# (`<projectKey>:<path>`) for a single known file, and that lookup is not
# permission-gated the way tree/list/search endpoints are. So: enumerate
# every file under sonar.sources (same set sonar-scan.sh analyzes), query
# each one's duplicated_lines/duplicated_blocks directly, and report the
# ones that are nonzero. That's the actual pair (or more) contributing to
# the project total — no Browse permission required.
#
# If SONAR_ADMIN_TOKEN (a user token with Browse) is available, this script
# also fetches exact cross-reference block data via api/duplications/show
# for each flagged file. Without it, you get the flagged files + counts and
# must eyeball/diff them yourself — usually enough, since a file's
# duplicated_lines count this large is rarely subtle.
#
# Usage:
#   scripts/sonar-duplications.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
readonly SCRIPT_DIR REPO_ROOT

# shellcheck source-path=SCRIPTDIR source=lib/sonar-env.sh
source "${SCRIPT_DIR}/lib/sonar-env.sh"

readonly PROPERTIES_FILE="${REPO_ROOT}/sonar-project.properties"

# --- 1. Resolve env + a query token ------------------------------------------
# sonar_load_env (lib/sonar-env.sh, shared with sonar-scan.sh and
# sonar-create-project.sh) loads .env, resolves SONAR_HOST/PROJECT_KEY from
# there, and validates SONAR_TOKEN. Layer SONAR_ADMIN_TOKEN (Browse-capable,
# needed for step 4's exact block detail) on top and only re-validate when
# it's genuinely a different token than the one already validated.
sonar_load_env "$REPO_ROOT" || exit 2

QUERY_TOKEN="${SONAR_ADMIN_TOKEN:-$SONAR_TOKEN}"
export QUERY_TOKEN
if [[ "$QUERY_TOKEN" != "$SONAR_TOKEN" ]]; then
    if ! curl -fsS -u "${QUERY_TOKEN}:" "${SONAR_HOST}/api/authentication/validate" \
            | grep -q '"valid":true'; then
        fail "SONAR_ADMIN_TOKEN did not validate against ${SONAR_HOST}"
        exit 2
    fi
fi
if [[ -n "${SONAR_ADMIN_TOKEN:-}" ]]; then
    ok "Using SONAR_ADMIN_TOKEN (Browse-capable) — will also fetch block detail"
else
    ok "Using SONAR_TOKEN — no SONAR_ADMIN_TOKEN set, so block detail may be" \
       "unavailable (per-file counts still work)"
fi

# --- 2. Read sonar.sources from sonar-project.properties --------------------
step "Reading sonar.sources from ${PROPERTIES_FILE#"${REPO_ROOT}/"}"
if [[ ! -f "$PROPERTIES_FILE" ]]; then
    fail "sonar-project.properties not found at ${PROPERTIES_FILE}"
    exit 2
fi
SOURCES_LINE=$(grep '^sonar.sources=' "$PROPERTIES_FILE" | cut -d= -f2-)
if [[ -z "$SOURCES_LINE" ]]; then
    fail "No sonar.sources= line found in ${PROPERTIES_FILE}"
    exit 2
fi
ok "sonar.sources=${SOURCES_LINE}"

mapfile -t FILES < <(
    cd "$REPO_ROOT"
    IFS=',' read -ra SRC_PATHS <<< "$SOURCES_LINE"
    for src in "${SRC_PATHS[@]}"; do
        if [[ -d "$src" ]]; then
            find "$src" -name "*.go" ! -name "*_test.go" | sort
        elif [[ -f "$src" ]]; then
            echo "$src"
        fi
    done
)
ok "${#FILES[@]} Go files to check"

# --- 3. Sweep: direct component-key measures query per file -----------------
step "Querying duplicated_lines/duplicated_blocks per file"
RESULTS_FILE="$(mktemp)"
trap 'rm -f "$RESULTS_FILE"' EXIT

for f in "${FILES[@]}"; do
    key="${PROJECT_KEY}:${f}"
    enc=$(python3 -c "import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1]))" "$key")
    if ! response=$(curl -fsS -u "${QUERY_TOKEN}:" \
        "${SONAR_HOST}/api/measures/component?component=${enc}&metricKeys=duplicated_lines,duplicated_blocks"); then
        fail "Could not query duplication measures for ${f}"
        exit 3
    fi
    printf '%s\n' "$response" >> "$RESULTS_FILE"
done

FLAGGED_FILE="$(mktemp)"
trap 'rm -f "$RESULTS_FILE" "$FLAGGED_FILE"' EXIT
SONAR_PAYLOAD_FILE="$RESULTS_FILE" python3 > "$FLAGGED_FILE" <<'PY'
import json, os

flagged = []
errors = []
with open(os.environ["SONAR_PAYLOAD_FILE"]) as fh:
    for line in fh:
        line = line.strip()
        if not line:
            continue
        try:
            data = json.loads(line)
        except json.JSONDecodeError as exc:
            errors.append(f"invalid JSON: {exc}")
            continue
        comp = data.get("component")
        if not comp:
            message = data.get("errors", [{}])[0].get("msg", "response has no component")
            errors.append(message)
            continue
        measures = {m["metric"]: int(float(m["value"])) for m in comp.get("measures", [])}
        dl = measures.get("duplicated_lines", 0)
        db = measures.get("duplicated_blocks", 0)
        if dl:
            flagged.append((dl, db, comp["key"]))

if errors:
    raise SystemExit("SonarQube duplication query failed: " + "; ".join(errors))

flagged.sort(reverse=True)
for dl, db, key in flagged:
    print(f"{key}\t{dl}\t{db}")
PY

FLAGGED_COUNT=$(wc -l < "$FLAGGED_FILE" | tr -d ' ')
if [[ "$FLAGGED_COUNT" -eq 0 ]]; then
    ok "No files with duplicated_lines > 0 — project has no duplication in this scope"
    exit 0
fi

step "Files with nonzero duplication"
printf '  %-55s %10s %10s\n' "FILE" "LINES" "BLOCKS"
while IFS=$'\t' read -r key dl db; do
    path="${key#"${PROJECT_KEY}":}"
    printf '  %-55s %10s %10s\n' "$path" "$dl" "$db"
done < "$FLAGGED_FILE"

echo
step "Sanity check against project totals"
proj_json=$(curl -fsS -u "${QUERY_TOKEN}:" \
    "${SONAR_HOST}/api/measures/component?component=${PROJECT_KEY}&metricKeys=duplicated_lines,duplicated_blocks,duplicated_files")
SONAR_PAYLOAD="$proj_json" python3 <<'PY'
import json, os
m = {x["metric"]: x["value"] for x in json.loads(os.environ["SONAR_PAYLOAD"])["component"]["measures"]}
print(f"  Project totals: duplicated_lines={m.get('duplicated_lines')} "
      f"duplicated_blocks={m.get('duplicated_blocks')} duplicated_files={m.get('duplicated_files')}")
PY
echo "  (sum of the per-file counts above should match duplicated_lines/duplicated_blocks;"
echo "   the row count should match duplicated_files — if not, some duplication may sit"
echo "   in a file outside sonar.sources, e.g. check sonar.tests scope separately)"

# --- 4. Optional: exact block cross-reference (needs Browse permission) -----
if [[ -n "${SONAR_ADMIN_TOKEN:-}" ]]; then
    step "Fetching exact duplicate block cross-references"
    while IFS=$'\t' read -r key _dl _db; do
        enc=$(python3 -c "import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1]))" "$key")
        echo
        echo "  ${key#"${PROJECT_KEY}":}"
        curl -s -u "${QUERY_TOKEN}:" "${SONAR_HOST}/api/duplications/show?key=${enc}" \
            | python3 -c "
import json, sys
data = json.load(sys.stdin)
files = data.get('files', {})
for block_group in data.get('duplications', []):
    for b in block_group['blocks']:
        fkey = files.get(b['_ref'], {}).get('key', b['_ref'])
        print(f\"    {fkey}:{b['from']}-{b['from']+b['size']-1}\")
    print()
"
    done < "$FLAGGED_FILE"
else
    warn "No SONAR_ADMIN_TOKEN — skipping exact block cross-reference." \
         "Read the flagged files above directly to find the duplicate blocks," \
         "or set SONAR_ADMIN_TOKEN to a user token with Browse permission on" \
         "the project for exact line-range detail."
fi
