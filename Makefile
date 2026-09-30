# Developer entry points. CI runs these same targets, so a green `make check`
# locally is the same bar the pipeline enforces.

# Minimum total statement coverage (percent). Keep this below the measured
# project coverage so the gate catches regressions without becoming flaky.
COVERAGE_MIN ?= 80

GO_FILES := $(shell git ls-files '*.go')

.PHONY: check fmt fmt-check vet lint test cover e2e-local vuln shellcheck secrets sonar coderabbit

check: fmt-check vet lint test cover vuln shellcheck ## everything CI runs (except secrets)

fmt: ## rewrite Go files with gofmt
	gofmt -w $(GO_FILES)

fmt-check: ## fail if any Go file is not gofmt-clean
	@out="$$(gofmt -l $(GO_FILES))"; \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet:
	go vet ./...

lint: ## golangci-lint (config: .golangci.yml)
	golangci-lint run ./...

test: ## unit tests with the race detector
	go test -race -count=1 ./...

e2e-local: ## compiled-binary E2E against a local fake Health API
	go run -tags local_e2e ./tools/local-e2e

cover: ## tests + total-coverage gate (writes reports/coverage.out)
	@mkdir -p reports
	go test -race -count=1 -covermode=atomic -coverpkg=./... -coverprofile=reports/coverage.out ./...
	@total="$$(go tool cover -func=reports/coverage.out | awk '/^total:/ {gsub("%","",$$3); print $$3}')"; \
	echo "total coverage: $$total% (minimum $(COVERAGE_MIN)%)"; \
	awk -v t="$$total" -v m="$(COVERAGE_MIN)" 'BEGIN { exit (t+0 >= m+0) ? 0 : 1 }' \
		|| { echo "coverage $$total% is below the $(COVERAGE_MIN)% gate"; exit 1; }

vuln: ## known-vulnerability scan of reachable dependencies
	govulncheck ./...

shellcheck:
	shellcheck -x scripts/*.sh scripts/lib/*.sh

secrets: ## full-history secret scan
	gitleaks detect --source . --config .gitleaks.toml --no-banner --redact

sonar: ## SonarQube scan (needs local SonarQube + token, see .env.example)
	scripts/sonar-scan.sh --fresh-coverage

coderabbit: ## local CodeRabbit review of changes against main
	scripts/coderabbit-review.sh --base main
