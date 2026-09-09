BIN := claude-codex-usage
DIST := dist
GOCACHE ?= /tmp/claude-codex-go-cache

.PHONY: build test vet check clean release

build:
	GOCACHE=$(GOCACHE) CGO_ENABLED=0 go build -trimpath -o $(DIST)/$(BIN) ./cmd/claude-codex-usage

test:
	GOCACHE=$(GOCACHE) go test ./...

vet:
	GOCACHE=$(GOCACHE) go vet ./...

check: test vet

release:
	GOCACHE=$(GOCACHE) ./scripts/build-release.sh

clean:
	rm -rf $(DIST)
