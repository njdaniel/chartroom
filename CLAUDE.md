# Chartroom

Chartroom is a self-hosted, open-source virtual tabletop for RPGs and miniature wargames. Every game, 5e included, is a ruleset package (data plus Starlark scripts), and AI agents take part as scoped seats at the table through MCP.

## Read first

- `docs/DESIGN.md` is the source of truth for the design. Do not contradict it; propose an ADR instead.
- `docs/adr/0000-governance.md` says what you may decide on your own and what needs Nick's sign-off. Read it before every session that writes code.
- `docs/adr/` holds accepted decisions. Check here before choosing a library or a format.

## Stack

- **Server:** Go. One binary for v1.
- **Storage:** SQLite in WAL mode.
- **Scripting:** Starlark, embedded in the server, for ruleset logic.
- **Client:** TypeScript in the browser, PixiJS for the 2D table, WebSocket to the server.
- **AI interface:** an MCP endpoint on the server (from M4).
- **Hosting for v1:** a private group over Tailscale. No public exposure and no account system yet.

Specific libraries (SQLite driver, WebSocket package, client build tooling, test frameworks) are not chosen yet. Propose them; they need sign-off as new dependencies.

## Invariants

These hold in every change. Breaking one is a bug, even if tests pass.

1. The server is authoritative. Clients send intents and render results.
2. All state changes are events in the log. State is a fold over the log.
3. Randomness comes only from the session's seeded dice source. Every roll is logged with inputs, modifiers, and result.
4. Each client, human or AI, receives only what its seat can see. Filter on the server, never in the client.
5. Ruleset scripts compute outcomes only. They cannot touch the filesystem, the network, the clock, or randomness.
6. No proprietary rules content in the repo. Rules content is CC-BY-4.0 (ours, the 5e SRD) or public domain (Little Wars).

## Licensing

- Engine code: AGPL-3.0 (`LICENSE` at the repo root).
- Rules content under `rulesets/`: CC-BY-4.0, with attribution for the SRD. Anything derived from LANDWAR must stay CC-BY-SA 4.0 and be marked as such.

## Workflow

- Work comes from GitHub issues on `njdaniel/chartroom`, grouped into milestones M0 to M5 (see the Milestones section of `docs/DESIGN.md`).
- Work only on the lowest open milestone. Its gate is the definition of done.
- Issue labels: `ready` → `in-progress` → `needs-review`, or `agent-failed` with a comment explaining why.
- One issue per branch and PR. The PR description links the issue and says if it touches a protected path from ADR-000.
- Keep PRs small enough to review in one sitting.

## Commands

Layout and dependencies are in `docs/adr/0001-repo-layout-and-dependencies.md`. Go 1.25+ and Node 24+.

- `make dev`: runs `chartroomd` on `127.0.0.1:8080` and the Vite dev server on `:5173`, which proxies `/ws`, `/api`, `/assets`, and `/healthz` to it. Open http://localhost:5173.
- `make build`: builds the client and embeds it in `bin/chartroomd` (build tag `chartroomweb`). A plain `go build` has no client, so Go work never needs Node.
- `make test`: `go test -race ./...` and the client's Vitest tests.
- `make lint`: gofmt, `go vet`, the dependency allowlist check, TypeScript typecheck, and ESLint.
- `make check`: lint plus test; the same checks CI runs.
- `make fmt`: gofmt the Go code.
- `go run ./cmd/chartroomd -addr 127.0.0.1:8080 -data data`: the server on its own. `-data` holds `chartroom.db` and `assets/`.

New dependencies go in `deps-allowlist.txt` in the same PR, with Nick's sign-off (ADR-000 item 2). `scripts/check-deps.sh` enforces it in CI.
