.PHONY: all build test clean demo fmt vet

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
