# ghealth — Development Guide

## What is this

ghealth is a CLI wrapping the Google Health API v4. Primary users are AI agents (OpenClaw, Claude Code, Codex, coding agents) that need structured health data access — steps, heart rate, sleep, exercise, weight, SpO2, HRV. Human developers use it too.

The CLI handles OAuth, pagination, response simplification, and contextual hints so agents can focus on the health question, not API plumbing.

## Progressive disclosure

Information is layered so agents load only what they need:

1. `ghealth --help` → commands overview
2. `ghealth data --help` → list vs daily-rollup guidance, all 40 types
3. `ghealth data <type> --help` → available operations
4. `ghealth data <type> <op> --help` → flags for that operation
5. `ghealth schema types` → machine-readable type registry
6. `ghealth schema type <name>` → fields, parameters, scope for one type
7. `_hints` in responses → contextual next-step suggestions
8. `skills/ghealth/SKILL.md` → non-obvious patterns, gotchas (load once)

## Build

```bash
go build -o ghealth .
go vet ./...
```

Verify against the live API: `ghealth auth status`, then `ghealth data steps daily-rollup --from 2026-03-22 --to 2026-03-29`.

## Quality gates

`make check` runs everything CI enforces (needs `golangci-lint` v2, `govulncheck`, `shellcheck`; `gitleaks` for `make secrets`):

| Target | What it checks |
|--------|----------------|
| `make fmt-check` / `make fmt` | gofmt cleanliness / rewrite |
| `make vet`, `make lint` | `go vet`, golangci-lint (`.golangci.yml`; gosec exclusions are scoped and commented) |
| `make test` | unit tests with `-race` |
| `make cover` | tests + total-coverage gate (`COVERAGE_MIN`, currently 80%; measured 81.7%) |
| `make vuln` | `govulncheck ./...` |
| `make shellcheck` | `scripts/*.sh` |
| `make secrets` | gitleaks full-history scan |

External reviewers (both optional locally, both keep secrets out of the repo):

- **SonarQube**: `cp .env.example .env`, set `SONAR_TOKEN`, then `make sonar` (`scripts/sonar-scan.sh --fresh-coverage`; exit code mirrors the quality gate). Needs the local SonarQube container. Sonar reports *line* coverage, which differs from the Go statement coverage used by `make cover`.
- **CodeRabbit**: `make coderabbit` (`scripts/coderabbit-review.sh --base main`) runs the local CLI; the GitHub app reads `.coderabbit.yaml` on PRs.

Use `client.AsCLIError(err)` instead of `err.(*client.CLIError)`, and write token/secret files with `auth.WriteSecretFile` (mode 0600, atomic) — never a bare `os.WriteFile`.

## Workflow

- Feature branches + PRs — never commit to main directly
- Only register data types verified against the live API, not from docs alone
- Test new types with curl before adding to the registry

## Where to make changes

| Task | Start here |
|------|-----------|
| Add/remove a data type | `pkg/types/registry.go` |
| Change response format | `pkg/output/simplify.go` |
| Add a contextual hint | `pkg/output/hints.go` |
| Change CLI flags or help text | `cmd/root.go` (globals), `cmd/data.go` (operations) |
| OAuth or auth flow | `pkg/auth/auth.go` |

## Documentation to keep updated

| When you... | Update |
|-------------|--------|
| Add/remove a data type | `README.md` types table, `skills/ghealth/SKILL.md` |
| Change flags or commands | `README.md`, `skills/ghealth-shared/SKILL.md` |

## Reference

- [Google Health API docs](https://developers.google.com/health)
- [Health API setup guide](https://developers.google.com/health/setup)
- [skills/](skills/) — agent skill files
