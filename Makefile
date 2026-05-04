.PHONY: all build test clean demo fmt vet

BUILD_DIR := bin
PKG := ./...

all: build

# Builds both binaries: daimond (the daemon) and daimon (the CLI).
# bin/daimon auto-spawns bin/daimond from the same directory in dev mode
# (see cmd/daimon/spawn.go::resolveDaimond).
build:
	@mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/daimond ./cmd/daimond
	go build -o $(BUILD_DIR)/daimon  ./cmd/daimon

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
