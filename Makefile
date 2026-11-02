.PHONY: build web-embed gen test e2e lint fmt dev web-install check

# Build the client, then bin/chartroomd with the client embedded.
build: web-embed
	go build -tags chartroomweb -o bin/chartroomd ./cmd/chartroomd

# Build the client and copy it where internal/webclient embeds it from.
web-embed: web-install
	cd web && npm run build
	rm -rf internal/webclient/dist
	cp -r web/dist internal/webclient/dist

# Regenerate the client's protocol types from internal/protocol. A Go test
# (cmd/schema-ts) fails if you forget.
gen:
	go run ./cmd/schema-ts

# Go and client unit tests.
test: web-install
	go test -race ./...
	cd web && npm test

# The M0 gate: two browsers against a real chartroomd (e2e/). Needs
# Playwright's Chromium once: cd web && npx playwright install chromium
e2e: build
	cd web && npm run e2e

# Everything CI checks besides tests.
lint: web-install
	@unformatted=$$(gofmt -l cmd internal); \
	  if [ -n "$$unformatted" ]; then echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi
	go vet ./...
	scripts/check-deps.sh
	scripts/check-randomness.sh
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
