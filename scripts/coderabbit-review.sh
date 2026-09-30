#!/usr/bin/env bash
# coderabbit-review.sh — run the local CodeRabbit CLI and summarize findings.
#
# Uses the installed `coderabbit` binary (already authenticated).
# Exits 0 when no actionable findings are present, 1 when findings exist.
#
# Usage:
#   scripts/coderabbit-review.sh                          # review all changes
#   scripts/coderabbit-review.sh --base master            # compare against master
#   scripts/coderabbit-review.sh --base HEAD~5            # compare last 5 commits
#   scripts/coderabbit-review.sh --type uncommitted --fast
#   scripts/coderabbit-review.sh --base master --fast

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
readonly SCRIPT_DIR REPO_ROOT

step()  { printf '\n\033[1;36m▸ %s\033[0m\n' "$*"; }
ok()    { printf '\033[0;32m✓\033[0m %s\n' "$*"; }
warn()  { printf '\033[1;33m⚠\033[0m %s\n' "$*" >&2; }
fail()  { printf '\033[0;31m✗\033[0m %s\n' "$*" >&2; }

BASE=""
TYPE=""
FAST=0

usage() {
    sed -n '2,13p' "$0" | sed 's/^# \{0,1\}//'
    exit "${1:-0}"
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --base)        BASE="${2:?--base requires a branch or commit}"; shift ;;
        --base=*)      BASE="${1#--base=}" ;;
        --type)        TYPE="${2:?--type must be committed or uncommitted}"; shift ;;
        --type=*)      TYPE="${1#--type=}" ;;
        --fast)        FAST=1 ;;
        -h|--help)     usage 0 ;;
        *)             echo "Unknown option: $1" >&2; usage 1 ;;
    esac
    shift
done

case "$TYPE" in
    ""|committed|uncommitted) ;;
    *)
        fail "--type must be committed or uncommitted, got: ${TYPE}"
        exit 2
        ;;
esac

# --- 1. Preflight ------------------------------------------------------------
step "CodeRabbit CLI preflight"

if ! command -v coderabbit >/dev/null 2>&1; then
    fail "coderabbit CLI not found. Install from https://coderabbit.ai/docs/getting-started/local-reviews"
    exit 2
fi

CR_VERSION="$(coderabbit --version 2>/dev/null)"
ok "coderabbit ${CR_VERSION}"

AUTH_STATUS="$(coderabbit auth status 2>/dev/null | grep -E '^Account' || true)"
if [[ -z "$AUTH_STATUS" ]]; then
    fail "Not authenticated. Run: coderabbit auth login"
    exit 2
fi
ok "${AUTH_STATUS}"

# --- 2. Build review command -------------------------------------------------
step "Running CodeRabbit review"

CR_ARGS=(--agent)
[[ "$FAST" -eq 1 ]] && CR_ARGS+=(--light)
[[ -n "$BASE" ]] && CR_ARGS+=(--base "$BASE")
case "$TYPE" in
    committed) CR_ARGS+=(--committed) ;;
    uncommitted) CR_ARGS+=(--uncommitted) ;;
esac

SCOPE_DESC="all local changes"
[[ -n "$BASE" ]] && SCOPE_DESC="changes since ${BASE}"
[[ -n "$TYPE" ]] && SCOPE_DESC="${TYPE} changes${BASE:+ since ${BASE}}"
ok "Scope: ${SCOPE_DESC}"

REVIEW_OUTPUT="$(mktemp)"
trap 'rm -f "$REVIEW_OUTPUT"' EXIT

cd "$REPO_ROOT"
if ! coderabbit review "${CR_ARGS[@]}" >"$REVIEW_OUTPUT" 2>&1; then
    # Non-zero exit is fine — means findings were found; captured in output
    true
fi

# --- 3. Parse findings -------------------------------------------------------
step "Findings"

EXIT_CODE=0
python3 - "$REVIEW_OUTPUT" <<'PY' || EXIT_CODE=$?
import json
import sys

raw = open(sys.argv[1]).read().strip()
if not raw:
    print("  (empty output from reviewer)")
    sys.exit(2)

# The --agent output is JSONL. Completion records carry a numeric finding
# count; individual finding records carry a finding payload.
objects = []
for line in raw.splitlines():
    try:
        objects.append(json.loads(line))
    except json.JSONDecodeError:
        continue

if not objects:
    print(raw)
    sys.exit(2)

findings = []
reported_count = 0
completed = False
skipped = False
for obj in objects:
    if isinstance(obj, list):
        findings.extend(obj)
        continue
    if not isinstance(obj, dict):
        continue
    if obj.get("type") == "complete":
        status = obj.get("status")
        if status == "review_completed":
            completed = True
        elif status == "review_skipped" and obj.get("findings") == 0:
            completed = True
            skipped = True
    reported = obj.get("findings")
    if isinstance(reported, list):
        findings.extend(reported)
    elif isinstance(reported, dict):
        findings.append(reported)
    elif isinstance(reported, int):
        reported_count = max(reported_count, reported)
    elif obj.get("type") == "finding":
        findings.append(obj)

if not completed:
    print(raw[:3000])
    sys.exit(2)
if skipped:
    print("  (no changes selected for review)")
    sys.exit(0)
if not findings and reported_count:
    print(raw[:3000])
    sys.exit(1)
if not findings:
    print("  (no actionable findings)")
    sys.exit(0)

# Group by severity
SEVERITY_ORDER = ["critical", "major", "minor", "info"]
by_sev = {}
for f in findings:
    sev = (f.get("severity") or f.get("level") or "info").lower()
    by_sev.setdefault(sev, []).append(f)

total = len(findings)
print(f"  Total findings: {total}")
print()

for sev in SEVERITY_ORDER + [s for s in by_sev if s not in SEVERITY_ORDER]:
    if sev not in by_sev:
        continue
    items = by_sev[sev]
    label = sev.upper()
    print(f"  [{label}] ({len(items)})")
    for item in items:
        # Field names vary across CodeRabbit finding schemas (observed both
        # `path`/`file` and `fileName`; `line`/`start_line` and `lineNumber`;
        # `message`/`title` and `description`/`body`/`comment`). Checking
        # only the first-guessed name silently dropped real location/message
        # data for any finding shaped differently -- fall back to the FULL
        # (not 200-char-truncated) json.dumps of the item so no content is
        # ever lost, even for a schema this script doesn't recognise yet.
        path = (
            item.get("path") or item.get("file") or item.get("fileName")
            or item.get("filePath") or ""
        )
        line = (
            item.get("line") or item.get("start_line") or item.get("lineNumber")
            or item.get("startLine") or ""
        )
        loc = f"{path}:{line}" if path and line else path or ""
        # CodeRabbit's agent stream puts actionable remediation in
        # codegenInstructions; `comment` is only the fallback for a finding
        # without instructions. Preserve both documented shapes rather than
        # emitting metadata with the actual issue discarded.
        msg = (
            item.get("codegenInstructions") or item.get("message")
            or item.get("title") or item.get("description") or item.get("body")
            or item.get("comment") or item.get("detail") or item.get("summary")
        )
        if msg is None:
            msg = json.dumps(item, ensure_ascii=False)
        if loc:
            print(f"    {loc}")
        print(f"      {msg}")
    print()

# Findings were printed -> signal them via exit code
sys.exit(1)
PY

# Also check if plain-text has findings markers.
if [[ "$EXIT_CODE" -eq 0 ]]; then
    PLAIN_FINDINGS="$(grep -cE '^\s*(CRITICAL|MAJOR|MINOR|INFO|BUG|ISSUE):' "$REVIEW_OUTPUT" 2>/dev/null || true)"
    [[ "${PLAIN_FINDINGS:-0}" -gt 0 ]] && EXIT_CODE=1
fi

echo
echo "Previous findings: coderabbit review findings"
echo "Dashboard: https://app.coderabbit.ai"

case "$EXIT_CODE" in
    0)
        ok "Review complete — no actionable findings"
        exit 0
        ;;
    1)
        warn "Findings present — address or dismiss before merging"
        exit 1
        ;;
    *)
        fail "Review did not complete; inspect the reviewer output above."
        exit 2
        ;;
esac
