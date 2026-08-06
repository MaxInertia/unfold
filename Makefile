.PHONY: all build build-web build-go build-tsindexer install-web install-tsindexer run dev clean

# Default: full build (frontend embedded into single Go binary, plus the
# TypeScript indexer sidecar binary).
all: build

build: build-web build-go build-tsindexer

# Every target used to be phony, so `make build` re-ran the frontend bundler
# and re-compiled the sidecar on every invocation whether or not anything had
# changed — around 4.5 of the 5 seconds. The Go build was already the cheap
# part, because the Go build cache does this properly.
#
# The phony names below are kept as aliases so the documented commands still
# work; the real targets are the files they produce, with the sources that
# actually determine them as prerequisites. `find` runs at parse time, which
# is fine at this repo's size and means a new file is picked up without
# anyone maintaining a list.
WEB_SRC := $(shell find web/src -type f 2>/dev/null) \
           web/index.html web/package.json web/vite.config.ts \
           web/tsconfig.json web/tsconfig.app.json web/tsconfig.node.json
TS_SRC := $(shell find tsindexer -maxdepth 1 -name '*.ts' 2>/dev/null) tsindexer/package.json
GO_SRC := $(shell find . -name '*.go' -not -path './web/*' -not -path './goland-plugin/*' 2>/dev/null) \
          go.mod go.sum

# The stamp, rather than dist/index.html, is what records "the frontend in the
# embed directory is current". index.html is a checked-in placeholder that the
# build overwrites and `git checkout` can restore, which would leave a file
# newer than every source and a stale placeholder compiled into the binary.
WEB_STAMP := internal/server/static/dist/.built

# Compile the TypeScript indexer sidecar (Bun + ts-morph) to a standalone
# binary next to the unfold binary. The Go TS engine locates it there; no
# Node runtime is required. Set $UNFOLD_TSINDEXER to override the path
# (a .ts path is run via `bun run`).
build-tsindexer: unfold-tsindexer

unfold-tsindexer: $(TS_SRC) tsindexer/node_modules
	cd tsindexer && bun build --compile main.ts --outfile ../unfold-tsindexer

tsindexer/node_modules: tsindexer/package.json
	cd tsindexer && bun install
	@touch $@

# Build the frontend and copy artifacts into the embed directory.
build-web: $(WEB_STAMP)

$(WEB_STAMP): $(WEB_SRC)
	cd web && bun run build
	rm -rf internal/server/static/dist
	mkdir -p internal/server/static/dist
	cp -r web/dist/. internal/server/static/dist/
	@touch $@

# Build the Go binary. Frontend assets must already be in the embed dir.
build-go: unfold

unfold: $(GO_SRC) $(WEB_STAMP)
	go build -o unfold ./cmd/cli

install-web:
	cd web && bun install

install-tsindexer:
	cd tsindexer && bun install

# Run the binary against the current directory's Go module.
run: build
	./unfold ./...

# Dev loop: start the Go server bound to a fixed port; in another shell run
# `cd web && bun run dev` to get a hot-reloading frontend that proxies /api to
# the Go server at $UNFOLD_API (default http://127.0.0.1:7777).
dev:
	go run ./cmd/cli --addr 127.0.0.1:7777 --no-open ./...

clean:
	rm -rf unfold unfold-tsindexer web/dist web/node_modules tsindexer/node_modules
	rm -rf internal/server/static/dist/* $(WEB_STAMP)
	# Restore the placeholder so the embed still compiles.
	@printf '%s\n' \
	  '<!doctype html>' \
	  '<html><body><h1>unfold</h1><p>Run <code>make build</code>.</p></body></html>' \
	  > internal/server/static/dist/index.html
