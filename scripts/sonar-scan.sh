#!/usr/bin/env bash
# sonar-scan.sh — full SonarQube scan + quality-gate verification.
#
# Resolves the SonarQube token from .env (falling back to 1Password), verifies
# the server is reachable, (optionally) regenerates Go coverage and the test
# report, regenerates the golangci-lint report, runs sonar-scanner, polls the
# compute-engine task to completion, then prints a quality-gate summary,
# project measures, and the new-code issues.
#
# Exit code mirrors the quality gate: 0 = OK, 1 = ERROR.
#
# Usage:
#   scripts/sonar-scan.sh                    # use existing reports/coverage.out if present
#   scripts/sonar-scan.sh --fresh-coverage   # run go test -race -cover (+ -json report) first
#   scripts/sonar-scan.sh --skip-coverage    # ignore coverage/test reports entirely
#   scripts/sonar-scan.sh --skip-lint-reports # don't regenerate the golangci-lint report
#   scripts/sonar-scan.sh --no-wait          # upload and exit (don't poll/summarize)
#   scripts/sonar-scan.sh --from-scratch     # wipe .scannerwork + disable analysis cache

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
readonly SCRIPT_DIR REPO_ROOT

# shellcheck source-path=SCRIPTDIR source=lib/sonar-env.sh
source "${SCRIPT_DIR}/lib/sonar-env.sh"

readonly SCANNER_IMAGE="${SCANNER_IMAGE:-sonarsource/sonar-scanner-cli:latest}"
readonly COVERAGE_FILE="${REPO_ROOT}/reports/coverage.out"

FRESH_COVERAGE=0
SKIP_COVERAGE=0
SKIP_LINT_REPORTS=0
WAIT_FOR_RESULT=1
FROM_SCRATCH=0

usage() {
    sed -n '2,18p' "$0" | sed 's/^# \{0,1\}//'
    exit "${1:-0}"
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --fresh-coverage) FRESH_COVERAGE=1 ;;
        --skip-coverage) SKIP_COVERAGE=1 ;;
        --skip-lint-reports) SKIP_LINT_REPORTS=1 ;;
        --no-wait) WAIT_FOR_RESULT=0 ;;
        --from-scratch) FROM_SCRATCH=1 ;;
        -h|--help) usage 0 ;;
        *) echo "Unknown option: $1" >&2; usage 1 ;;
    esac
    shift
done

# --- 1-3. Load env, verify server, resolve token, ensure project exists ----
# step()/ok()/warn()/fail() and the token/project resolution logic come from
# lib/sonar-env.sh, shared with scripts/sonar-duplications.sh and
# scripts/sonar-create-project.sh so project identity can't drift between
# them — SONAR_PROJECT_KEY / SONAR_PROJECT_NAME are read from .env there.
sonar_load_env "$REPO_ROOT" || exit 2
sonar_ensure_project || exit 2

# --- 4. Coverage ------------------------------------------------------------
coverage_args=()
if [[ "$SKIP_COVERAGE" -eq 1 ]]; then
    warn "Skipping coverage (per --skip-coverage)"
    # sonar-project.properties names the normal report paths. Explicit blank
    # command-line properties override them, preventing stale coverage from
    # silently influencing this scan's metrics or quality gate.
    coverage_args=(-Dsonar.go.coverage.reportPaths= -Dsonar.go.tests.reportPaths=)
elif [[ "$FRESH_COVERAGE" -eq 1 ]]; then
    step "Regenerating reports/coverage.out via go test -race -cover"
    mkdir -p "${REPO_ROOT}/reports"
    # -coverpkg=./... attributes coverage across packages (cmd tests exercise
    # pkg/*), matching `make cover`. -json feeds Sonar's test-execution report.
    (cd "$REPO_ROOT" && go test -race -count=1 -covermode=atomic -coverpkg=./... \
        -coverprofile=reports/coverage.out -json ./... > reports/test-report.json) \
        || { fail "go test failed (see reports/test-report.json)"; exit 3; }
    ok "Wrote reports/coverage.out and reports/test-report.json"
elif [[ -f "$COVERAGE_FILE" ]]; then
    if mtime_epoch=$(stat -c %Y "$COVERAGE_FILE" 2>/dev/null); then
        mtime_human=$(stat -c %y "$COVERAGE_FILE" | cut -d. -f1)
    else
        # BSD/macOS stat
        mtime_epoch=$(stat -f %m "$COVERAGE_FILE")
        mtime_human=$(date -r "$mtime_epoch" '+%Y-%m-%d %H:%M:%S')
    fi
    age_days=$(( ( $(date +%s) - mtime_epoch ) / 86400 ))
    if [[ "$age_days" -gt 1 ]]; then
        warn "Using reports/coverage.out that is ${age_days} days old (--fresh-coverage to regenerate)"
    else
        ok "Using reports/coverage.out (${mtime_human})"
    fi
else
    warn "No reports/coverage.out present (--fresh-coverage to generate)"
fi

# --- 5. Lint report ---------------------------------------------------------
# Regenerated fresh on every run so it can never be stale relative to the code
# being scanned, unlike reports/coverage.out which is deliberately reusable.
# The scope and config (.golangci.yml) are the same as CI's lint step, so a
# SonarQube-imported finding always matches a local `golangci-lint run`.
#
# golangci-lint exits non-zero when it finds issues, which is the whole point
# of importing its report - `|| true` stops that from tripping `set -e`.
lint_args=()
if [[ "$SKIP_LINT_REPORTS" -eq 1 ]]; then
    warn "Skipping lint report (per --skip-lint-reports)"
    # Override the default report path from sonar-project.properties so stale
    # external-analyzer output is not imported after an explicit skip.
    lint_args=(-Dsonar.go.golangci-lint.reportPaths=)
elif ! command -v golangci-lint >/dev/null 2>&1; then
    warn "golangci-lint not found — skipping lint report (go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest)"
    lint_args=(-Dsonar.go.golangci-lint.reportPaths=)
else
    step "Regenerating golangci-lint report"
    mkdir -p "${REPO_ROOT}/reports"
    (cd "$REPO_ROOT" && golangci-lint run \
        --output.text.path=stdout \
        --output.checkstyle.path=reports/golangci-lint-report.xml ./...) || true
    ok "Report written: reports/golangci-lint-report.xml"
fi

# --- 6. Run scanner ---------------------------------------------------------
# Derive the version from git rather than a separately maintained property, so
# it can't silently drift. Untagged repos report the short commit hash.
project_version=$(git -C "$REPO_ROOT" describe --tags --always 2>/dev/null || echo "dev")
version_args=(-Dsonar.projectVersion="$project_version")

identity_args=(-Dsonar.projectKey="$PROJECT_KEY" -Dsonar.projectName="$PROJECT_NAME")
scratch_args=()
if [[ "$FROM_SCRATCH" -eq 1 ]]; then
    step "Forcing from-scratch analysis"
    rm -rf "${REPO_ROOT}/.scannerwork"
    ok "Removed .scannerwork"
    scratch_args=(-Dsonar.analysisCache.enabled=false)
fi

step "Running sonar-scanner"
scanner_log="$(mktemp)"
trap 'rm -f "$scanner_log"' EXIT
(
    cd "$REPO_ROOT"
    if command -v sonar-scanner >/dev/null 2>&1; then
        sonar-scanner \
            -Dsonar.host.url="$SONAR_HOST" \
            -Dsonar.token="$SONAR_TOKEN" \
            "${identity_args[@]}" \
            "${coverage_args[@]}" \
            "${lint_args[@]}" \
            "${scratch_args[@]}" \
            "${version_args[@]}" 2>&1 | tee "$scanner_log"
    else
        docker run --rm --network host \
            -e SONAR_TOKEN \
            -v "${REPO_ROOT}:/usr/src" \
            -w /usr/src \
            "${SCANNER_IMAGE}" \
            -Dsonar.host.url="$SONAR_HOST" \
            "${identity_args[@]}" \
            "${coverage_args[@]}" \
            "${lint_args[@]}" \
            "${scratch_args[@]}" \
            "${version_args[@]}" 2>&1 | tee "$scanner_log"
    fi
) || { fail "sonar-scanner failed"; exit 3; }

task_id=$(grep -oE 'api/ce/task\?id=[a-f0-9-]+' "$scanner_log" | head -1 | cut -d= -f2 || true)
if [[ -z "${task_id:-}" ]]; then
    fail "Could not extract compute-engine task id from scanner output"
    exit 3
fi
ok "Analysis uploaded (task $task_id)"

if [[ "$WAIT_FOR_RESULT" -eq 0 ]]; then
    echo
    echo "Dashboard: ${SONAR_HOST}/dashboard?id=${PROJECT_KEY}"
    exit 0
fi

# --- 7. Poll compute engine -------------------------------------------------
# A transient HTTP failure here is not a scan failure: the report is already
# uploaded, and the server can legitimately return 500 for a few seconds while
# the compute engine (or the database behind it) restarts. Retry instead of
# letting `curl -fsS` + `set -e` abort the run, and surface the compute
# engine's own errorMessage when the task really does fail.
step "Waiting for compute-engine processing"
ce_status=""
analysis_id=""
poll_errors=0
for i in $(seq 1 60); do
    if ! task_json=$(curl -fsS -u "${SONAR_TOKEN}:" "${SONAR_HOST}/api/ce/task?id=${task_id}" 2>/dev/null); then
        poll_errors=$((poll_errors + 1))
        warn "Poll #${poll_errors} could not reach ${SONAR_HOST}/api/ce/task — retrying"
        sleep 2
        continue
    fi
    ce_status=$(SONAR_PAYLOAD="$task_json" python3 -c "import json,os; print(json.loads(os.environ['SONAR_PAYLOAD'])['task']['status'])")
    case "$ce_status" in
        SUCCESS)
            analysis_id=$(SONAR_PAYLOAD="$task_json" python3 -c "import json,os; print(json.loads(os.environ['SONAR_PAYLOAD'])['task'].get('analysisId', ''))")
            if [[ -z "$analysis_id" ]]; then
                fail "Compute-engine task succeeded but returned no analysisId"
                exit 3
            fi
            ok "Processed in ~$((i * 2))s (analysis $analysis_id)"
            break
            ;;
        FAILED|CANCELED)
            fail "Compute-engine task ended with status: $ce_status"
            SONAR_PAYLOAD="$task_json" python3 <<'PY'
import json, os
task = json.loads(os.environ["SONAR_PAYLOAD"])["task"]
for key in ("errorMessage", "errorType"):
    if task.get(key):
        print(f"  {key}: {task[key]}")
PY
            exit 3
            ;;
        *) sleep 2 ;;
    esac
done
if [[ "$ce_status" != "SUCCESS" ]]; then
    fail "Compute-engine task still '${ce_status:-unreachable}' after 120s"
    exit 3
fi

# --- 8. Quality gate + measures + new-code issues ---------------------------
step "Quality gate"
qg_json=$(curl -fsS -u "${SONAR_TOKEN}:" \
    "${SONAR_HOST}/api/qualitygates/project_status?analysisId=${analysis_id}")

qg_status=$(SONAR_PAYLOAD="$qg_json" python3 -c "import json,os; print(json.loads(os.environ['SONAR_PAYLOAD'])['projectStatus']['status'])")
SONAR_PAYLOAD="$qg_json" python3 <<'PY'
import json, os
data = json.loads(os.environ["SONAR_PAYLOAD"])["projectStatus"]
print(f"  Status: {data['status']}")
period = data.get("period") or {}
if period:
    since = f" since {period['date']}" if period.get("date") else ""
    print(f"  New-code period: {period.get('mode', '?')}{since}")
conditions = data.get("conditions") or []
if not conditions:
    print("  (no conditions evaluated - a first analysis has no new-code baseline)")
for c in conditions:
    mark = "✓" if c["status"] == "OK" else "✗"
    try:
        actual, threshold = float(c["actualValue"]), float(c["errorThreshold"])
    except (KeyError, TypeError, ValueError):
        breached = False
    else:
        comparator = c.get("comparator")
        breached = (comparator == "LT" and actual < threshold) or (
            comparator == "GT" and actual > threshold
        )
    # A condition reported OK whose actual value sits on the wrong side of its
    # threshold was ignored by the server (too few new lines to judge), not
    # passed - projectStatus.ignoredConditions is the top-level flag for it.
    note = "  <- IGNORED, not passed" if breached and c["status"] == "OK" else ""
    print(f"  {mark} {c['metricKey']:32s} {c['comparator']} {c['errorThreshold']:>6}  actual={c['actualValue']}{note}")
PY

step "Project measures"
measures_json=$(curl -fsS -u "${SONAR_TOKEN}:" "${SONAR_HOST}/api/measures/component?component=${PROJECT_KEY}&metricKeys=bugs,vulnerabilities,code_smells,security_hotspots,coverage,duplicated_lines_density,ncloc,reliability_rating,security_rating,sqale_rating")
SONAR_PAYLOAD="$measures_json" python3 <<'PY'
import json, os
m = {x["metric"]: x["value"] for x in json.loads(os.environ["SONAR_PAYLOAD"])["component"]["measures"]}
rating = lambda v: {"1.0":"A","2.0":"B","3.0":"C","4.0":"D","5.0":"E"}.get(v, v)
print(f"  Bugs: {m.get('bugs','-')}   Vulns: {m.get('vulnerabilities','-')}   "
      f"Hotspots: {m.get('security_hotspots','-')}   Code smells: {m.get('code_smells','-')}")
print(f"  Coverage: {m.get('coverage','-')}%   Duplication: {m.get('duplicated_lines_density','-')}%   "
      f"NCLOC: {m.get('ncloc','-')}")
print(f"  Ratings — Reliability: {rating(m.get('reliability_rating','-'))}   "
      f"Security: {rating(m.get('security_rating','-'))}   "
      f"Maintainability: {rating(m.get('sqale_rating','-'))}")
PY

step "New-code issues (open, in new-code period)"
if [[ -z "${SONAR_ADMIN_TOKEN:-}" ]]; then
    warn "Issue details unavailable: set SONAR_ADMIN_TOKEN with Browse permission to query them"
else
    issues_json=$(curl -fsS -u "${SONAR_ADMIN_TOKEN}:" \
        "${SONAR_HOST}/api/issues/search?componentKeys=${PROJECT_KEY}&inNewCodePeriod=true&issueStatuses=OPEN,CONFIRMED&ps=100")
    SONAR_PAYLOAD="$issues_json" python3 <<'PY'
import json, os
data = json.loads(os.environ["SONAR_PAYLOAD"])
issues = data["issues"]
print(f"  Total: {data['total']}")
if not issues:
    print("  (no open issues in new-code period)")
buckets = {}
for i in issues:
    buckets.setdefault(i["severity"], []).append(i)
for sev in ("BLOCKER","CRITICAL","MAJOR","MINOR","INFO"):
    if sev not in buckets: continue
    print(f"\n  [{sev}] ({len(buckets[sev])})")
    for i in buckets[sev]:
        comp = i["component"].split(":",1)[-1]
        line = i.get("line", "?")
        print(f"    {comp}:{line}  {i['rule']}")
        print(f"      {i['message']}")
PY
fi

echo
echo "Dashboard: ${SONAR_HOST}/dashboard?id=${PROJECT_KEY}"

if [[ "$qg_status" == "OK" ]]; then
    ok "Quality gate passed"
    exit 0
else
    fail "Quality gate failed (status: $qg_status)"
    exit 1
fi
