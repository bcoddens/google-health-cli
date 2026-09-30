#!/bin/bash
# Secure 1Password integration for this project.
# Provides secure key resolution without exposing secrets in environment

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Run an op command from a Windows-accessible working directory to avoid WSL UNC path issues.
# When Windows executables are invoked from a WSL UNC path (\\wsl.localhost\...),
# CMD.EXE rejects the path. Running from /mnt/c avoids this.
# OP_CMD is unset to prevent the /usr/local/bin/op wrapper from forwarding it via WSLENV,
# which would confuse op.exe since it treats OP_* vars as its own configuration.
_run_op() {
    local cmd="${1}"
    shift
    (cd /mnt/c 2>/dev/null || cd /tmp || exit; env -u OP_CMD "$cmd" "$@")
}

# Detect and setup op command for different environments
setup_op_command() {
    # If op is already available and runnable, use it.
    # Some environments provide a broken wrapper that exists in PATH but cannot execute.
    if command -v op >/dev/null 2>&1; then
        if _run_op op --version >/dev/null 2>&1; then
            echo "op"
            return 0
        fi
    fi

    # Check if op is an alias
    if type op >/dev/null 2>&1; then
        # Extract the actual command from alias
        local alias_output
        alias_output=$(type op 2>/dev/null)
        if [[ "$alias_output" =~ op\ is\ aliased\ to\ [\'\"](.+)[\'\"] ]]; then
            echo "${BASH_REMATCH[1]}"
            return 0
        fi
    fi

    # Prefer WinGet links path (stable, no spaces in path components)
    local user_candidates=(
        "${USER:-}"
        "${LOGNAME:-}"
    )

    local user_name
    for user_name in "${user_candidates[@]}"; do
        [[ -z "$user_name" ]] && continue
        local links_path="/mnt/c/Users/${user_name}/AppData/Local/Microsoft/WinGet/Links/op.exe"
        if [[ -x "$links_path" ]]; then
            echo "$links_path"
            return 0
        fi
    done

    # Check common Windows/WSL paths
    local common_paths=(
        "/mnt/c/Program Files/1Password CLI/op.exe"
        "/mnt/c/Program Files (x86)/1Password CLI/op.exe"
    )

    for path in "${common_paths[@]}"; do
        if [[ -x "$path" ]]; then
            echo "$path"
            return 0
        fi
    done

    return 1
}

# Check if 1Password CLI is available and authenticated
check_op_cli() {
    # Setup the op command
    local op_cmd
    if ! op_cmd=$(setup_op_command); then
        echo -e "${RED}Error: 1Password CLI (op) not found${NC}" >&2
        echo "Please install 1Password CLI: https://developer.1password.com/docs/cli/get-started/" >&2
        echo "Or ensure 'op' is available as an alias or in PATH" >&2
        return 1
    fi

    # Store the command for later use
    export OP_CMD="$op_cmd"

    # Check if user is authenticated (suppress output to avoid prompts)
    if ! _run_op "$op_cmd" account list >/dev/null 2>&1; then
        return 1
    fi

    return 0
}

# Check if 1Password CLI is available (without authentication check)
check_op_cli_available() {
    local op_cmd
    if ! op_cmd=$(setup_op_command); then
        return 1
    fi
    export OP_CMD="$op_cmd"
    return 0
}

# Check authentication status without prompting
is_op_authenticated() {
    # Use the global OP_CMD if set, otherwise use 'op'
    local cmd_to_use="${OP_CMD:-op}"
    _run_op "$cmd_to_use" account list >/dev/null 2>&1
}

# Securely resolve a 1Password reference
resolve_op_ref() {
    local ref="$1"
    local default_value="${2:-}"

    # Check if it's already a resolved value (not an op:// reference)
    if [[ ! "$ref" =~ ^op:// ]]; then
        echo "$ref"
        return 0
    fi

    # Check 1Password CLI availability first
    if ! check_op_cli_available; then
        echo -e "${RED}Error: 1Password CLI not available${NC}" >&2
        if [[ -n "$default_value" ]]; then
            echo "$default_value"
            return 0
        fi
        return 1
    fi

    # Resolve the reference directly. Skipping a separate is_op_authenticated pre-check
    # because the WSL/Windows op.exe bridge allows only ~2 rapid consecutive connections
    # to the 1Password desktop app; a pre-check would consume one and cause read to fail.
    local value
    local op_cmd="${OP_CMD:-op}"
    local op_stderr
    op_stderr=$(mktemp)

    if value=$(_run_op "$op_cmd" read "$ref" 2>"$op_stderr"); then
        rm -f "$op_stderr"
        echo "$value"
        return 0
    else
        local err
        err=$(<"$op_stderr")
        rm -f "$op_stderr"
        if [[ "$err" == *"cannot connect"* || "$err" == *"not authenticated"* || "$err" == *"sign in"* ]]; then
            echo -e "${YELLOW}Warning: 1Password CLI not authenticated, skipping key resolution${NC}" >&2
        else
            echo -e "${YELLOW}Warning: Failed to resolve 1Password reference: $ref${NC}" >&2
        fi
        if [[ -n "$default_value" ]]; then
            echo "$default_value"
            return 0
        fi
        return 1
    fi
}

# Load variables from a dotenv file without executing its values.
#
# The format is deliberately small and predictable: blank lines and comments
# are ignored; assignments are KEY=VALUE; optional matching outer single or
# double quotes are removed. Values are data — `$()`, backticks, `$VAR`, and
# shell metacharacters are never evaluated. A value already present in the
# caller's environment wins, so CI secrets are not replaced by an empty or
# stale local .env entry.
load_env_secure() {
    local env_file="${1:-.env}"
    local line key value resolved

    if [[ ! -f "$env_file" ]]; then
        echo -e "${YELLOW}Warning: Environment file not found: $env_file${NC}" >&2
        return 0
    fi

    while IFS= read -r line || [[ -n "$line" ]]; do
        # Trim leading/trailing whitespace without parsing it as shell syntax.
        line="${line#"${line%%[![:space:]]*}"}"
        line="${line%"${line##*[![:space:]]}"}"
        [[ -z "$line" || "$line" == \#* ]] && continue
        if [[ "$line" != *=* ]]; then
            echo -e "${RED}Error: Invalid dotenv line in ${env_file}: ${line}${NC}" >&2
            return 1
        fi

        key="${line%%=*}"
        value="${line#*=}"
        key="${key#"${key%%[![:space:]]*}"}"
        key="${key%"${key##*[![:space:]]}"}"
        value="${value#"${value%%[![:space:]]*}"}"
        value="${value%"${value##*[![:space:]]}"}"
        if [[ ! "$key" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
            echo -e "${RED}Error: Invalid dotenv variable name in ${env_file}: ${key}${NC}" >&2
            return 1
        fi

        # Keep literal apostrophes and shell syntax intact; only peel a
        # matching pair of outer quotes.
        if [[ ${#value} -ge 2 ]]; then
            if [[ "${value:0:1}" == '"' && "${value: -1}" == '"' ]] ||
                [[ "${value:0:1}" == "'" && "${value: -1}" == "'" ]]; then
                value="${value:1:${#value}-2}"
            fi
        fi

        # Explicit environment values, including an intentionally empty one,
        # have higher precedence than local developer configuration.
        if [[ -v "$key" ]]; then
            continue
        fi

        if [[ "$value" == op://* ]]; then
            if ! resolved="$(resolve_op_ref "$value")" || [[ -z "$resolved" ]]; then
                echo -e "${YELLOW}Warning: op:// reference for ${key} resolved to empty${NC}" >&2
                return 1
            fi
            value="$resolved"
        fi
        printf -v "$key" '%s' "$value"
        export "${key?}"
    done < "$env_file"
}

# Execute a command with securely resolved environment variables
exec_with_secure_env() {
    local env_file="${1:-.env}"
    shift

    # Load environment securely
    if ! load_env_secure "$env_file"; then
        echo -e "${RED}Error: Failed to load environment from $env_file${NC}" >&2
        return 1
    fi

    # Execute the command
    exec "$@"
}

# Validate that required 1Password references can be resolved
validate_op_refs() {
    local env_file="${1:-.env}"
    local missing_refs=()

    if [[ ! -f "$env_file" ]]; then
        echo -e "${YELLOW}Warning: Environment file not found: $env_file${NC}" >&2
        return 0
    fi

    # Check 1Password CLI availability first
    if ! check_op_cli_available; then
        echo -e "${RED}Error: 1Password CLI not available${NC}" >&2
        return 1
    fi

    # Check authentication status
    if ! is_op_authenticated; then
        echo -e "${YELLOW}Warning: 1Password CLI not authenticated${NC}" >&2
        echo "Run 'op signin' to authenticate, then retry validation" >&2
        return 1
    fi

    # Check each op:// reference
    while IFS= read -r line || [[ -n "$line" ]]; do
        [[ -z "${line// }" ]] && continue
        [[ "$line" =~ ^[[:space:]]*# ]] && continue

        if [[ "$line" =~ ^[[:space:]]*[A-Za-z_][A-Za-z0-9_]*[[:space:]]*=[[:space:]]*(op://.*)[[:space:]]*$ ]]; then
            local ref="${BASH_REMATCH[1]}"
            if ! resolve_op_ref "$ref" >/dev/null 2>&1; then
                missing_refs+=("$ref")
            fi
        fi
    done < "$env_file"

    if [[ ${#missing_refs[@]} -gt 0 ]]; then
        echo -e "${RED}Error: Failed to resolve the following 1Password references:${NC}" >&2
        for ref in "${missing_refs[@]}"; do
            echo "  - $ref" >&2
        done
        return 1
    fi

    echo -e "${GREEN}✓ All 1Password references resolved successfully${NC}"
    return 0
}

# Quick validation of environment (non-intrusive)
validate_env_quick() {
    local env_file="${1:-.env}"

    if ! check_op_cli_available; then
        echo -e "${RED}❌ 1Password CLI not available${NC}" >&2
        return 1
    fi

    if ! is_op_authenticated; then
        echo -e "${YELLOW}⚠️ 1Password CLI not authenticated${NC}" >&2
        echo "Run 'op signin' or use smart_voyage_auth" >&2
        return 1
    fi

    echo -e "${GREEN}✅ 1Password CLI ready for secure key resolution${NC}"
    return 0
}
