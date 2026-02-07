.PHONY: build build-ui build-go run dev clean

# Build everything
build: build-ui build-go

# Build frontend
build-ui:
	cd ui && npm ci && npm run build

# Build Go binary (embeds UI)
build-go:
	go build -o bin/cudascope ./cmd/cudascope/

# Run locally
run: build
	./bin/cudascope

# Dev mode: run Go with live reload (no UI embed)
dev:
	go run ./cmd/cudascope/ --dev

clean:
	rm -rf bin/ ui/build/ ui/node_modules/
