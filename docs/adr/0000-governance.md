# ADR-000: Governance and authority boundaries

- **Status:** Accepted
- **Date:** 2026-11-02
- **Decider:** Nick (project principal)

## Context

Chartroom is built mostly by an AI coding agent (Claude Code) working from GitHub issues, with Nick as the human principal. The agent needs to move quickly on implementation without asking about every choice, while the decisions that are expensive to reverse stay with Nick.

Two formats in particular are costly to change once anything depends on them: the **event log schema** (every saved game is a fold over it) and the **ruleset package format** (every ruleset, including homebrew, is written against it). Both get explicit protection here.

## Decision

Work is split across three tiers, the same model used in Conch and Leviathan.

| Tier | Who | Scope |
| --- | --- | --- |
| Tier-C | CI | Build, `go vet`, lint, unit tests, ruleset package tests. A red check blocks merge, no exceptions. |
| Tier-A | Coding agent | Everything inside accepted ADRs and the current milestone (see below). |
| Tier-H | Nick | The sign-off list below. |

### The agent decides on its own

- Sequencing of issues within the current milestone.
- Implementation, internal package structure, and naming.
- Tests, refactors, and bug fixes that stay within accepted ADRs.
- Self-review of its own PRs, and proposing new issues.

### Nick signs off on

1. **New ADRs**, and any change to an accepted ADR or to `docs/DESIGN.md`.
2. **New dependencies** (Go modules and npm packages), including the first ones.
3. **Event log schema** changes: event types, fields, serialization, or versioning.
4. **Ruleset package format** changes: manifest, schema language, Starlark API surface.
5. **MCP tool surface** changes: adding, removing, or changing a tool AI agents can call.
6. **Merges that touch** dice and RNG, fog-of-war visibility filtering, or seat permissions. These are the paths where a bug lets someone cheat or see hidden information.
7. **Any rules content added to the repo**, with its source and license stated in the PR.
8. **Milestone closure.** Closing a milestone advances the project to the next phase.
9. **Releases and tags.**

When unsure whether something is on this list, treat it as if it is and ask.

## Invariants (not up for Tier-A change)

- The server is authoritative. Clients send intents; they never decide outcomes.
- All game state changes are events in the log. Nothing mutates state outside it.
- Randomness comes only from the session's seeded dice source, and every roll is logged.
- A client, human or AI, receives only what its seat is allowed to see.
- No proprietary rules content (Warhammer 40k, One Page Rules, WTJ, Bolt Action, and similar) is committed to the repo.

## Consequences

- The agent can complete most milestone work without waiting on Nick.
- Format changes are slower, deliberately. The first versions of the event schema and the ruleset package format each get their own ADR before code depends on them.
- PRs that touch a protected path must say so in the description so review is not missed.
