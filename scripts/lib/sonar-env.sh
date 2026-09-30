#!/usr/bin/env bash
# sonar-env.sh — shared SonarQube environment resolution for scripts/sonar-*.sh.
#
# Loads .env (if present) *before* computing any default, so SONAR_TOKEN /
# SONAR_PROJECT_KEY / SONAR_PROJECT_NAME set there are never silently
# shadowed by a hardcoded fallback — then verifies the server is reachable
# and resolves+validates an auth token. Source this, call sonar_load_env
# once, then (optionally) sonar_ensure_project.
#
# Exposes after a successful sonar_load_env: SONAR_HOST, SONAR_OP_REF,
# PROJECT_KEY, PROJECT_NAME, SONAR_TOKEN (exported, validated).

SONAR_ENV_LIB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SONAR_ENV_LIB_DIR

# shellcheck source-path=SCRIPTDIR source=1password.sh
source "${SONAR_ENV_LIB_DIR}/1password.sh"

step()  { printf '\n\033[1;36m▸ %s\033[0m\n' "$*"; }
ok()    { printf '\033[0;32m✓\033[0m %s\n' "$*"; }
warn()  { printf '\033[1;33m⚠\033[0m %s\n' "$*" >&2; }
fail()  { printf '\033[0;31m✗\033[0m %s\n' "$*" >&2; }

# sonar_load_env <repo_root>
#
# Sets SONAR_HOST / PROJECT_KEY / PROJECT_NAME / SONAR_TOKEN (all exported).
# Returns 2 and prints a fail() message if the server is unreachable or no
# valid token can be resolved.
sonar_load_env() {
    local repo_root="$1"

    if [[ -f "${repo_root}/.env" ]]; then
        step "Loading configuration from .env"
        if ! load_env_secure "${repo_root}/.env"; then
            fail "Could not load ${repo_root}/.env"
            return 2
        fi
    fi

    # SONAR_PROJECT_KEY / SONAR_PROJECT_NAME set in .env (or the environment)
    # always win. The fallbacks below only matter for a project that hasn't
    # set up .env yet — .env.example carries the same values, so a normal
    # `cp .env.example .env` never hits this fallback.
    SONAR_HOST="${SONAR_HOST:-http://localhost:9000}"
    SONAR_OP_REF="${SONAR_OP_REF:-op://Private/SonarQube/api_key}"
    PROJECT_KEY="${SONAR_PROJECT_KEY:-google-health-cli}"
    PROJECT_NAME="${SONAR_PROJECT_NAME:-Google Health CLI}"
    export SONAR_HOST SONAR_OP_REF PROJECT_KEY PROJECT_NAME

    step "Checking SonarQube server at ${SONAR_HOST}"
    local status_json status
    if ! status_json=$(curl -fsS "${SONAR_HOST}/api/system/status" 2>/dev/null); then
        fail "Cannot reach ${SONAR_HOST}. Is the container running?"
        echo "  Try: docker start sonarqube" >&2
        return 2
    fi
    status=$(echo "$status_json" | python3 -c "import json,sys; print(json.load(sys.stdin)['status'])")
    if [[ "$status" != "UP" ]]; then
        fail "Server status: $status (expected UP)"
        return 2
    fi
    ok "Server is UP"

    if [[ -z "${SONAR_TOKEN:-}" ]]; then
        step "Resolving SonarQube token from 1Password"
        if ! SONAR_TOKEN="$(resolve_op_ref "$SONAR_OP_REF")" || [[ -z "${SONAR_TOKEN:-}" ]]; then
            fail "No SONAR_TOKEN found — add it to .env or sign in to 1Password (${SONAR_OP_REF})"
            return 2
        fi
    fi
    export SONAR_TOKEN
    if ! curl -fsS -u "${SONAR_TOKEN}:" "${SONAR_HOST}/api/authentication/validate" \
            | grep -q '"valid":true'; then
        fail "Token did not validate against ${SONAR_HOST}"
        return 2
    fi
    ok "Token validated (${#SONAR_TOKEN} chars)"
    ok "Project: ${PROJECT_KEY} (${PROJECT_NAME})"
}

# sonar_ensure_project
#
# Idempotently creates PROJECT_KEY/PROJECT_NAME via the REST API if it
# doesn't already exist. Requires sonar_load_env to have run first (needs
# SONAR_HOST/SONAR_TOKEN/PROJECT_KEY/PROJECT_NAME). SONAR_ADMIN_TOKEN, when
# supplied, is validated and used for both search and creation; otherwise the
# analysis token is used. A caller only succeeds after creation or confirmed
# existing-project status.
sonar_ensure_project() {
    step "Ensuring project '${PROJECT_KEY}' exists"
    local _provision_token="${SONAR_ADMIN_TOKEN:-$SONAR_TOKEN}"
    local _project_exists=0
    if [[ "$_provision_token" != "$SONAR_TOKEN" ]]; then
        if ! curl -fsS -u "${_provision_token}:" "${SONAR_HOST}/api/authentication/validate" \
                | grep -q '"valid":true'; then
            fail "SONAR_ADMIN_TOKEN did not validate against ${SONAR_HOST}"
            return 2
        fi
    fi
    if [[ -n "${SONAR_ADMIN_TOKEN:-}" ]]; then
        local _search_body _search_http _project_count
        _search_body=$(mktemp)
        _search_http=$(curl -sS -o "$_search_body" -w "%{http_code}" \
            -u "${_provision_token}:" \
            "${SONAR_HOST}/api/projects/search?projects=${PROJECT_KEY}")
        if [[ "$_search_http" == "200" ]]; then
            _project_count=$(python3 -c \
                "import json,sys; print(json.load(open('$_search_body'))['paging']['total'])" \
                2>/dev/null || echo "0")
            [[ "$_project_count" -gt 0 ]] && _project_exists=1
        else
            warn "Project search returned HTTP ${_search_http} — falling back to create"
        fi
        rm -f "$_search_body"
    fi

    if [[ "$_project_exists" -eq 1 ]]; then
        ok "Project already exists: ${PROJECT_KEY}"
        return 0
    fi

    local _create_body _create_http _create_err
    _create_body=$(mktemp)
    _create_http=$(curl -sS -o "$_create_body" -w "%{http_code}" \
        -u "${_provision_token}:" -X POST \
        "${SONAR_HOST}/api/projects/create" \
        --data-urlencode "name=${PROJECT_NAME}" \
        --data-urlencode "project=${PROJECT_KEY}" \
        --data-urlencode "visibility=private")
    _create_err=$(python3 -c \
        "import json,sys; d=json.load(open('$_create_body')); print(d.get('errors',[{}])[0].get('msg',''))" \
        2>/dev/null || true)
    rm -f "$_create_body"
    case "$_create_http" in
        200|201) ok "Project created: ${PROJECT_KEY}"; return 0 ;;
        400)
            if [[ "$_create_err" == *"already exists"* ]]; then
                ok "Project already exists: ${PROJECT_KEY}"
                return 0
            fi
            fail "Project creation failed (HTTP 400): ${_create_err}"
            return 2
            ;;
        403)
            fail "Token lacks 'Create Projects' permission and project '${PROJECT_KEY}' was not confirmed to exist"
            return 2
            ;;
        *)
            fail "Unexpected response from /api/projects/create (HTTP ${_create_http}): ${_create_err}"
            return 2
            ;;
    esac
}
