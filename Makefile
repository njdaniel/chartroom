.PHONY: build test lint fmt dev web-install check

# Build the client, then the server binary into bin/.
build: web-install
	cd web && npm run build
	go build -o bin/chartroomd ./cmd/chartroomd

# Go and client unit tests.
test: web-install
	go test -race ./...
	cd web && npm test

# Everything CI checks besides tests.
lint: web-install
	@unformatted=$$(gofmt -l cmd internal); \
	  if [ -n "$$unformatted" ]; then echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi
	go vet ./...
	scripts/check-deps.sh
	cd web && npm run typecheck && npm run lint

fmt:
	gofmt -w cmd internal

# Server on :8080 and the Vite dev server (which proxies to it) together.
# Ctrl-C stops both.
dev: web-install
	@trap 'kill 0' EXIT; \
	  go run ./cmd/chartroomd & \
	  (cd web && npm run dev) & \
	  wait

web-install:
	@[ -d web/node_modules ] || (cd web && npm ci)

check: lint test
