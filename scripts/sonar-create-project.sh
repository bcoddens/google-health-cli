#!/usr/bin/env bash
# sonar-create-project.sh — ensure the SonarQube project exists, via the REST API.
#
# Reads project identity (SONAR_PROJECT_KEY / SONAR_PROJECT_NAME) and the
# analysis token from .env — falling back to 1Password for the token (see
# scripts/lib/1password.sh) and to placeholder defaults for the project
# identity if .env hasn't been set up yet — verifies the server is
# reachable, and idempotently creates the project if it doesn't exist.
# Safe to run repeatedly: a second run against an existing project is a
# no-op.
#
# scripts/sonar-scan.sh also calls this automatically before every scan, so
# running it standalone is optional — useful right after `cp .env.example
# .env` to confirm setup without waiting for a full scan + coverage run.
#
# Usage:
#   scripts/sonar-create-project.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
readonly SCRIPT_DIR REPO_ROOT

usage() {
    sed -n '2,17p' "$0" | sed 's/^# \{0,1\}//'
    exit "${1:-0}"
}

case "${1:-}" in
    -h|--help) usage 0 ;;
    "") ;;
    *) echo "Unknown option: $1" >&2; usage 1 ;;
esac

# shellcheck source-path=SCRIPTDIR source=lib/sonar-env.sh
source "${SCRIPT_DIR}/lib/sonar-env.sh"

sonar_load_env "$REPO_ROOT" || exit 2
sonar_ensure_project || exit 2

echo
echo "Dashboard: ${SONAR_HOST}/dashboard?id=${PROJECT_KEY}"
