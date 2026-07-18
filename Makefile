# olb — build automation
#
# `make build` produces a local binary; `make dist` cross-compiles all five
# supported targets (cgo-free, from one machine); `make notices` regenerates the
# embedded THIRD_PARTY_NOTICES before a release build.

BINARY  := olb
PKG     := github.com/krisiasty/olb
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X $(PKG)/internal/version.Version=$(VERSION) \
	-X $(PKG)/internal/version.Commit=$(COMMIT) \
	-X $(PKG)/internal/version.Date=$(DATE)

# The five supported OS/arch targets (see spec §Platform support).
TARGETS := windows/amd64 darwin/amd64 darwin/arm64 linux/amd64 linux/arm64

.PHONY: build test vet fmt lint dist notices check-licenses clean

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

test:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

lint: vet
	test -z "$$(gofmt -l .)" || (gofmt -l .; exit 1)

# Cross-compile every supported target into dist/.
dist: notices
	@mkdir -p dist
	@for t in $(TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; \
		ext=; [ "$$os" = windows ] && ext=.exe; \
		echo "  build $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 \
			go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-$$os-$$arch$$ext . ; \
	done

# Regenerate the embedded attribution file from the real module graph.
notices:
	./scripts/gen-notices.sh

# Authoritative license gate (same check CI runs).
check-licenses:
	go-licenses check . --disallowed_types=forbidden,restricted,unknown

clean:
	rm -rf $(BINARY) dist
