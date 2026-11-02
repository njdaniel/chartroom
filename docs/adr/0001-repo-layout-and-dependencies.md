# ADR-001: Repo layout and initial dependencies

- **Status:** Proposed
- **Date:** 2026-11-02
- **Decider:** Nick (Tier-H: new ADR, new dependencies)

## Context

M0 needs a Go server, a TypeScript browser client, a place for ruleset content, and CI. The design doc fixes the stack (Go, SQLite in WAL mode, Starlark, TypeScript with PixiJS, WebSocket) but not the libraries. ADR-000 requires sign-off on every new dependency, including the first ones.

Conch already runs this stack in production, so where Conch has made a choice, this ADR reuses it unless Chartroom has a reason to differ.

## Decision

### Layout

```
cmd/
  chartroomd/        server binary (serves the API, the WebSocket, and the built client)
  schema-ts/         generates TypeScript types for the wire protocol from the Go types
internal/
  store/             SQLite open, WAL, migrations
  eventlog/          event envelope, append, read, upcasters, snapshots (ADR-002)
  table/             M0 table state and its fold (map, tokens, seats); becomes the entity model in M1
  dice/              per-session seeded dice source and dice expressions
  session/           per-session loop: intents in, events out, seats, authorization hook
  visibility/        per-seat filtering of events and state (ADR-003, invariant 4)
  protocol/          wire message types; the single source of truth for the client types
  server/            HTTP routes, WebSocket handler, asset storage, embedded client
web/                 browser client: Vite, TypeScript, PixiJS
  src/protocol.gen.ts   generated; never edited by hand
e2e/                 Playwright tests that drive real browsers against a real server
rulesets/            ruleset packages (CC-BY-4.0); README only in M0
docs/
  DESIGN.md, adr/, plans/
deps-allowlist.txt   approved direct dependencies (Go and npm), one per line
Makefile             build, test, lint, dev, e2e
```

- Everything lives under `internal/` until a second module needs it. The simulator (M5) runs in-process, so it doesn't force a public `pkg/`.
- The Starlark sandbox gets `internal/rules/` in M1. It isn't created in M0.
- The built client is embedded into `chartroomd` with `go:embed`, so v1 stays one binary.
- `go.mod` uses `ignore ./web` (as Conch does) so Go never reads `node_modules`.
- CI checks that every direct dependency in `go.mod` and `web/package.json` appears in `deps-allowlist.txt`. Adding a line there needs Nick's sign-off in the same PR.

### Go dependencies

| Module | Why | Rejected |
| --- | --- | --- |
| `modernc.org/sqlite` | Pure Go, no cgo, so cross-builds and single-binary distribution stay trivial. Same driver as Conch. | `mattn/go-sqlite3` needs cgo. `ncruces/go-sqlite3` is pure Go (via Wasm) and good, but adds a second SQLite stack to maintain alongside Conch's. |
| `github.com/coder/websocket` | Small, maintained, `context`-aware API, built-in ping. Same as Conch. | `gorilla/websocket` works but has no context support and an older API. `golang.org/x/net/websocket` lacks ping/close handling. |
| `go.starlark.net` | **Added in M1, listed now so the choice is on record.** Deterministic, sandboxed by default, embeds natively in Go. Matches DESIGN.md. | `gopher-lua` and `goja` (JS) both need work to make them deterministic and sandboxed. CEL and `expr` are expression languages, too small for turn structure. |

Standard library for everything else in M0: `net/http` routing, `encoding/json`, `testing` (no assertion library), `math/rand/v2` (ChaCha8) for dice, `crypto/rand` for seeds and ids.

### npm dependencies (`web/`, exact versions pinned)

| Package | Why |
| --- | --- |
| `pixi.js` (v8) | The 2D table renderer, per DESIGN.md. WebGL with a Canvas fallback, handles pan/zoom and many sprites. |
| `typescript` | Client language. |
| `vite` | Dev server and production build. Same as Conch. |
| `vitest` | Unit tests; shares Vite's config. Same as Conch. |
| `@playwright/test` | Two real browser contexts against one server. This is how the M0 gate gets tested. Same as Conch. |
| `eslint`, `typescript-eslint` | Lint in CI. Same as Conch. |

No UI framework in M0. The chrome around the table (dice log, roll box, seat name, map picker) is small enough for plain TypeScript and the DOM. React can come in by a later ADR if the UI outgrows that.

## Alternatives considered

- **A monorepo tool (Nx, Turborepo).** Rejected: one Go module and one npm package don't need one.
- **Hand-written TypeScript protocol types.** Rejected: they drift from the Go types. Generating them (Conch's `cmd/schema-ts` pattern) and checking in CI that the generated file is current removes that class of bug.
- **A separate static file server for the client.** Rejected: breaks the one-binary goal.

## Consequences

- Six npm packages and two Go modules need sign-off now. `go.starlark.net` needs sign-off again when M1 actually adds it.
- The `Makefile` targets become the "Commands" section of `CLAUDE.md` once this is accepted.
- `modernc.org/sqlite` is slower than the C driver. That's fine at tabletop scale (a few writes per second per session).
