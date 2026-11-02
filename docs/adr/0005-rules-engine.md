# ADR-005: Rules engine, v1: entities, actions, the floor, and the Starlark API

- **Status:** Proposed
- **Date:** 2026-11-02
- **Decider:** Nick (Tier-H: new ADR; event log schema, Starlark API surface, new dependency, dice and seat-permission paths; ADR-000 items 1–4 and 6)

## Context

M1's scope is the entity model, ruleset packages, the Starlark sandbox, and the event log, with Little Wars as the first ruleset. ADR-004 defines what a package is. This ADR defines what happens at runtime: what an entity is, how a player's action becomes events, who may act when (the floor), what scripts may call, and what goes in the log.

The invariants shape everything here. The server is authoritative (1). State changes only through events (2). Randomness comes only from the session's dice source (3). Scripts compute outcomes and can't touch the filesystem, network, clock, or randomness (5).

## Decision

### Entities

An entity is a piece on the table: one infantry-man, a gun, later a 5e monster or a battalion.

- **Engine fields:** `id` (assigned by the server), `kind` (from the package schema), `side` (optional), `parent` (optional entity id, so units can contain models), `pose` (`x`, `y`, `rotation`, as in M0), and `base` (from the kind).
- **Components:** the typed fields the schema declares for the kind. The engine checks them on every change and doesn't interpret them.
- **Game components:** session-level fields such as the move number and scores, declared under `game` in the schema.
- M0's tokens and map stay as they are: free annotations any seat can place, outside the rules. Entities are the game pieces. Merging the two models is left until a ruleset needs it.

### Sides and seats

A session with a ruleset has the sides its schema lists. A seat joins a side with the `side.join` intent. M1 allows one seat per side; a seat with no side can watch. Which side a seat is on is game state, so it's an event.

### The floor

The floor is the engine's one turn-structure primitive (DESIGN.md): who may act now. In v1 it is a side (or `null` before the game starts) and a phase name, both set by scripts. The phase name means something only to the ruleset, such as Little Wars's `guns` and `men`.

`authorize` (ADR-003) enforces it. An `action.take` from a seat whose side doesn't hold the floor is rejected with `not_your_turn`, before any script runs. Each action also declares which phases it's allowed in, and the engine checks that too. Scripts can refuse anything else.

### Actions

An action is something a ruleset lets a side do, like move men, fire a gun, resolve a melee, or end the move.

- `scripts/main.star` defines an `actions` dict. Each entry has an args schema (in ADR-004's schema language, plus `point` and `entities`), the phases it's allowed in, and a `run(ctx, args)` function.
- The client sends `action.take { action, args }`. The engine checks the floor and phase, validates `args` against the schema, and calls `run` on the session loop.
- `run` reads state and emits **effects** through `ctx`. Effects apply to a working copy as they're emitted, so later reads in the same action see them.
- When `run` returns, the engine turns the effects into events and appends them, with one `action.resolved` event first, as **one transaction**. If the script fails, runs out of steps, or emits an invalid effect (a component of the wrong type, an unknown entity), the intent is rejected with `rule_error` and nothing is logged.
- A script refuses an action by calling `ctx.reject(message)`. The player sees it as `rejected { code: "illegal_action", message }`.
- After every action the engine calls the ruleset's optional `after_action(ctx)` hook, in the same transaction, for checks such as victory or end-of-move cleanup.
- Rulesets also define `setup(ctx, options)`, which runs once when a session is created. It sets the initial floor, game components, and starting entities, such as preset armies.

### The Starlark sandbox

- **Library:** `go.starlark.net`, pinned to one commit at least two weeks old, chosen in the implementing PR. It's listed in ADR-001 and gets its `deps-allowlist.txt` line in that PR, under the sign-off this ADR asks for. It needs Go 1.25, which is what we use.
- **Modules:** scripts get the Starlark core language, `math`, `json`, and the `ctx` argument. They get no `time`, `random`, file, network, or environment access. `load()` works only for other `.star` files in the same package.
- **Limits:** each call runs on a fresh thread with a step limit (10 million steps to start with). Running out is a `rule_error`. Values passed in are frozen, so scripts can't change state behind the engine's back.
- **Determinism:** Starlark iterates dicts in insertion order and has no hidden randomness. The engine passes entities sorted by id. The same state, arguments, and dice therefore always produce the same effects. Replay doesn't depend on this, because it reads outcomes from the log, but package tests and simulation (M5) do.

### Script API v1 (`engine_api: 1`)

Reading (`ctx.state` is read-only):

| Call | Returns |
| --- | --- |
| `ctx.entities(kind=None, side=None)` | entities, sorted by id |
| `ctx.entity(id)` | one entity, or `None` |
| `ctx.game` | the game components |
| `ctx.floor` | `{side, phase}` |
| `ctx.actor` | the side taking this action |
| `ctx.data(name)` | the parsed `data/<name>.json` |

Measuring (computed by the engine from bases, so every ruleset measures the same way):

| Call | Returns |
| --- | --- |
| `ctx.distance(a, b, method="edge")` | inches between two entities or points; `method` is `"edge"` (closest points of the bases) or `"centre"` |
| `ctx.within(where, radius, kind=None, side=None)` | entities whose base is within `radius` of an entity or point, nearest first |
| `ctx.sweep(start, end, width)` | entities whose base a `width`-wide path from `start` to `end` touches, in order along the path |

Randomness:

| Call | Effect |
| --- | --- |
| `ctx.roll(expr, label)` | rolls through the session's dice source (ADR-002) and returns the result. It's logged as a normal `dice.rolled` event, in order with the action's other events, so every player sees every roll a rule made. |

Effects:

| Call | Becomes |
| --- | --- |
| `ctx.create(kind, pose, side=None, parent=None, **components)` | `entity.created`; returns the new id |
| `ctx.move(id, pose)` | `entity.moved` |
| `ctx.update(id, **components)` | `entity.updated` |
| `ctx.remove(id, reason)` | `entity.removed` |
| `ctx.set_game(**components)` | `game.updated` |
| `ctx.set_floor(side, phase)` | `floor.set` |
| `ctx.end_game(winner, scores, summary)` | `game.ended`; no actions are accepted after it |
| `ctx.note(text)` | `rule.noted`, a line in the game log explaining a ruling, such as "Melee: 9 Red against 11 Blue; 2 Red taken prisoner" |
| `ctx.reject(message)` | refuses the action (see above) |

Adding a call is backward compatible. Changing or removing one bumps `engine_api` and needs sign-off (ADR-000 item 4).

### New event types (all `data_v: 1`)

These extend ADR-002's list. Ids are assigned by the server.

| Type | `data` fields |
| --- | --- |
| `session.ruleset_selected` | `name`, `version`, `hash`, `options` (the setup options); seq 2 in a ruleset session |
| `seat.side_joined` | `seat_id`, `side` |
| `action.resolved` | `action`, `side`, `args`; heads each action's events |
| `entity.created` | `entity_id`, `kind`, `side`, `parent`, `pose: {x, y, rotation}`, `components` |
| `entity.moved` | `entity_id`, `from`, `to` (poses) |
| `entity.updated` | `entity_id`, `set` (changed components, full values), `unset` (names) |
| `entity.removed` | `entity_id`, `reason` |
| `game.updated` | `set`, `unset` |
| `floor.set` | `side` (or `null`), `phase` |
| `rule.noted` | `text` |
| `game.ended` | `winner` (side, or `null` for a draw), `scores` (side → number), `summary` |

The `rs.` prefix stays reserved. M1 rulesets can't define their own event types: everything Little Wars does fits the generic events above, and a fold that knows every type is simpler and safer.

### Protocol additions (ADR-003)

- New intents: `side.join { side }` and `action.take { action, args }`. New rejection codes: `not_your_turn`, `illegal_action`, `rule_error`, `game_over`.
- `POST /api/sessions` takes an optional `ruleset` and `options`. `GET /api/rulesets` lists the built-in packages.
- `welcome` gains a `ruleset` field: the package's name and version, its sides, kinds and bases, game components, and its actions with their args schemas and phases. The client builds its action buttons and forms from this catalogue. It is not filtered: rules aren't secret.
- No "legal actions" query in M1. The client offers an action's form during the action's allowed phases, and the server explains any refusal. A query for legal actions arrives with MCP in M4, which needs it for AI agents.
- The visibility filter passes every new event unchanged, since Little Wars has no hidden information. Fog of war (M3) adds rules in the same filter.

### Replay

- `GET /api/sessions/{id}/events` returns the log as a spectator would see it: every event through the visibility filter, for a seat with no side. In M1 that means everything except the dice seed.
- The client folds those events with the same reducer it uses live, so a replay is the same code path stepped to any seq.

## Alternatives considered

- **Scripts mutate state directly.** Simpler to write, but nothing could be validated or logged as outcomes, and a buggy script could corrupt state. Effects keep the engine as the only writer.
- **Rulesets define their own event types (`rs.*`).** More expressive. It also means the fold runs ruleset code or treats data as opaque, so replay depends on the package. Deferred until a ruleset needs something the generic events can't say.
- **Re-running scripts on replay.** Rejected in ADR-002: replay would depend on script versions and re-roll dice.
- **Lua (gopher-lua) or JavaScript (goja).** Considered and rejected in ADR-001: making them deterministic and sandboxed takes work that Starlark gives by default.
- **A server-side legal-actions query in M1.** Better UI, but every ruleset would have to implement action enumeration before it's needed. M4's AI agents need it, so it arrives then.
- **Several seats per side, or seats switching sides.** Not needed for a two-player game. A later ADR can add it.

## Consequences

- The engine knows nothing about Little Wars, and Little Wars knows nothing about storage, networking, or dice internals. Another ruleset (5e in M2) is another package against the same API.
- Every rule outcome, including each die a rule rolled and each ruling note, is in the log and visible to every player.
- A script bug can refuse an action but can't corrupt state or the log.
- The client gets a generic action UI, which is plain but works for any ruleset.
- Rulesets are limited to generic events in M1. If one needs more, that's a new ADR.
