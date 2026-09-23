.PHONY: build build-ui build-go run dev test clean

# Build everything
build: build-ui build-go

# Build frontend
build-ui:
	cd ui && npm ci && npm run build

# Which build this is, shown in the dashboard footer and the startup log.
# The Dockerfile takes the same three values as build arguments.
BUILDINFO := github.com/sergey/cudascope/internal/buildinfo
LDFLAGS := -X $(BUILDINFO).Version=$(shell git describe --tags --always --dirty 2>/dev/null) \
	-X $(BUILDINFO).Revision=$(shell git rev-parse HEAD 2>/dev/null) \
	-X $(BUILDINFO).BuildTime=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# Build Go binary (embeds UI)
build-go:
	go build -ldflags "$(LDFLAGS)" -o bin/cudascope ./cmd/cudascope/

# Run the Go tests
test:
	go test -race ./internal/...

# Run locally
run: build
	./bin/cudascope

# Dev mode: run Go with live reload (no UI embed)
dev:
	go run ./cmd/cudascope/ --dev

clean:
	rm -rf bin/ ui/build/ ui/node_modules/
