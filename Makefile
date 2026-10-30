# `make check` runs everything the CI job ran: go vet, staticcheck, go test -race against
# throwaway Postgres + Redis (fails if any test was skipped), govulncheck, gosec (against
# .gosec-baseline.json) and gitleaks over the full history. Each step is also its own target.
# Needs Go and Docker. `make test-down` stops the test containers.

SHELL := bash
.SHELLFLAGS := -eo pipefail -c

STATICCHECK := honnef.co/go/tools/cmd/staticcheck@v0.8.2
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.9.0
GOSEC := github.com/securego/gosec/v2/cmd/gosec@v2.29.0
GITLEAKS := ghcr.io/gitleaks/gitleaks:v8.30.1@sha256:c00b6bd0aeb3071cbcb79009cb16a60dd9e0a7c60e2be9ab65d25e6bc8abbb7f

COMPOSE := docker compose -f docker-compose.test.yml
# Always the throwaway containers, never a URL from the environment.
export TEST_DATABASE_URL = postgres://postgres:test@127.0.0.1:55432/postgres?sslmode=disable
export TEST_REDIS_URL = redis://127.0.0.1:56379/0

.PHONY: check vet staticcheck test vuln gosec secrets test-up test-down

check: vet staticcheck test vuln gosec secrets
	@echo "check: all passed"

vet:
	go vet ./...

staticcheck:
	go run $(STATICCHECK) ./...

test: test-up
	@log=$$(mktemp); trap 'rm -f "$$log"' EXIT; \
	go test -race -count=1 -v ./... > "$$log" 2>&1 || { cat "$$log"; exit 1; }; \
	grep -E '^(ok|FAIL)' "$$log"; \
	if grep -q -- '--- SKIP' "$$log"; then grep -- '--- SKIP' "$$log"; echo "tests were skipped"; exit 1; fi

vuln:
	go run $(GOVULNCHECK) ./...

gosec:
	scripts/gosec-check.sh $(GOSEC)

secrets:
	docker run --rm -u "$$(id -u):$$(id -g)" -v "$(CURDIR):/repo:ro" $(GITLEAKS) \
		git --no-banner --redact --exit-code 1 -c /repo/.gitleaks.toml /repo

test-up:
	$(COMPOSE) up -d --wait

test-down:
	$(COMPOSE) down
