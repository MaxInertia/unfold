.PHONY: all build build-web build-go install-web run dev clean

# Default: full build (frontend embedded into single Go binary).
all: build

build: build-web build-go

# Build the frontend and copy artifacts into the embed directory.
build-web:
	cd web && bun run build
	rm -rf internal/server/static/dist
	mkdir -p internal/server/static/dist
	cp -r web/dist/. internal/server/static/dist/

# Build the Go binary. Frontend assets must already be in the embed dir.
build-go:
	go build -o unfold ./cmd/cli

install-web:
	cd web && bun install

# Run the binary against the current directory's Go module.
run: build
	./unfold ./...

# Dev loop: start the Go server bound to a fixed port; in another shell run
# `cd web && bun run dev` to get a hot-reloading frontend that proxies /api to
# the Go server at $UNFOLD_API (default http://127.0.0.1:7777).
dev:
	go run ./cmd/cli --addr 127.0.0.1:7777 --no-open ./...

clean:
	rm -rf unfold web/dist web/node_modules
	rm -rf internal/server/static/dist/*
	# Restore the placeholder so the embed still compiles.
	@printf '%s\n' \
	  '<!doctype html>' \
	  '<html><body><h1>unfold</h1><p>Run <code>make build</code>.</p></body></html>' \
	  > internal/server/static/dist/index.html
