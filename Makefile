.PHONY: all build test clean demo fmt vet build-all ci-local

BUILD_DIR := bin
PKG := ./...

all: build

# Builds both binaries: daimond (the daemon) and daimon (the CLI).
# bin/daimon auto-spawns bin/daimond from the same directory in dev mode
# (see cmd/daimon/spawn.go::resolveDaimond).
#
# VERSION is derived from `git describe --tags --dirty --always`:
#   - clean tagged commit:    "v0.2.0-dev.2"
#   - between tags:           "v0.2.0-dev.2-3-g93a91f5"
#   - dirty working tree:     "v0.2.0-dev.2-3-g93a91f5-dirty"
#   - no tags / no git:       falls back to the sha or "dev"
# The release workflow uses the same pattern so locally-built binaries
# and CI-built artifacts report identical version strings for the same
# commit.
VERSION := $(shell git describe --tags --dirty --always 2>/dev/null || echo dev)

build:
	@mkdir -p $(BUILD_DIR)
	go build -ldflags "-X main.version=$(VERSION)" -o $(BUILD_DIR)/daimond ./cmd/daimond
	go build -ldflags "-X main.version=$(VERSION)" -o $(BUILD_DIR)/daimon  ./cmd/daimon

test:
	go test $(PKG)

fmt:
	go fmt $(PKG)

vet:
	go vet $(PKG)

# Runs the self-contained 8-step demo (ephemeral identity, temp socket).
# For the production lifecycle, use bin/daimon init && bin/daimon unlock.
demo: build
	./$(BUILD_DIR)/daimond demo

clean:
	rm -rf $(BUILD_DIR)

# Builds everything: daimond + daimon + x402-mock-server. The mock
# server is excluded from the default `make build` (it's a CI/example
# binary, not part of the user-facing product), but having a one-shot
# build for it is useful when running the x402 smoke locally.
build-all: build
	go build -ldflags "-X main.version=$(VERSION)" -o $(BUILD_DIR)/x402-mock-server ./cmd/x402-mock-server

# Runs everything CI runs, in roughly the same order. Useful for
# pre-push verification — beats waiting for the GitHub Actions
# round-trip on every push. Stops on the first failure (set -e
# semantics via the standard Make recipe behaviour).
#
# Mirror of .github/workflows/ci.yml's 10-shard matrix, minus the
# install-script shard (that one needs the published GitHub Release
# and can't run pre-push by definition).
#
# Total runtime: ~2 minutes on an M1 / M2 laptop. The x402-smoke
# step dominates (binary builds + mock server spin-up + two SDK
# round-trips); skip it via SKIP_SMOKE=1 if you're iterating fast
# and only need the Go + SDK suites.
ci-local: build-all
	@echo "=== go vet ==="
	go vet $(PKG)
	@echo "=== go test -race ==="
	go test -race $(PKG)
	@echo "=== Python SDK suite ==="
	@if [ -d sdk/python ]; then \
		cd sdk/python && \
		python3 scripts/gen_version.py && \
		python3 -m pytest -q; \
	fi
	@echo "=== TypeScript SDK suite ==="
	@if [ -d sdk/typescript ]; then \
		cd sdk/typescript && \
		npm run typecheck && \
		npm test; \
	fi
	@if [ "$$SKIP_SMOKE" = "1" ]; then \
		echo "=== x402-smoke (SKIPPED via SKIP_SMOKE=1) ==="; \
	else \
		echo "=== x402 cross-language smoke ==="; \
		bash examples/x402-smoke/run.sh; \
	fi
	@echo
	@echo "ci-local: all checks passed (version=$(VERSION))."
