# ADR-004: Ruleset package format, v1

- **Status:** Proposed
- **Date:** 2026-11-02
- **Decider:** Nick (Tier-H: new ADR; ruleset package format, ADR-000 item 4)

## Context

DESIGN.md says every game, 5e included, is a ruleset package: a manifest, schemas, data, Starlark scripts, and tests. Every ruleset and every homebrew patch will be written against this format, so ADR-000 protects it. M1 needs it for Little Wars, the first ruleset, and its gate is that Little Wars plays end to end and replays from the log.

ADR-005 covers what scripts can do at runtime (the Starlark API). This ADR covers what a package is on disk, how the server loads it, and how a session pins it.

## Decision

### Layout

A package is a directory:

```
rulesets/little_wars/
  ruleset.json      manifest
  schema.json       entity kinds, sides, and their components
  data/*.json       units, presets, tables: anything the scripts read
  scripts/*.star    Starlark; scripts/main.star is the entry point
  tests/*.json      scenarios with expected outcomes (see Tests)
  README.md         what the game is, the source, and the license
```

### Manifest (`ruleset.json`)

```json
{
  "format": 1,
  "name": "little_wars",
  "version": "0.1.0",
  "title": "Little Wars",
  "engine_api": 1,
  "license": "CC-BY-4.0",
  "attribution": "Adapted from H. G. Wells, Little Wars (1913), public domain.",
  "extends": null
}
```

- `format` is this ADR's version. `engine_api` is ADR-005's Starlark API version. The server refuses a package whose `format` or `engine_api` it doesn't know.
- `name` matches `^[a-z][a-z0-9_]*$` and is unique on a server. `version` is semver.
- `license` and `attribution` are required (invariant 6). Packages in this repo must be `CC-BY-4.0`, or `CC-BY-SA-4.0` if derived from LANDWAR.
- `extends` names a parent package (for homebrew on top of the SRD). M1 accepts only `null`; how extension merges schemas and scripts is decided with M2 or M5, when a package first needs it.

### Data format: JSON

The manifest, schema, data, and tests are JSON. DESIGN.md allows YAML or JSON. JSON needs no new dependency, is what an AI drafts most reliably, and is checked the same way everywhere. Authors lose comments; `README.md` and Starlark comments carry the explanation instead. YAML can be added later by an ADR if hand-authoring needs it.

### Schema language (`schema.json`)

The schema says what entity kinds exist, what each one's base is, and which components (typed fields) it carries. The engine validates every created or changed entity against it, without knowing what the fields mean.

```json
{
  "sides": ["red", "blue"],
  "game": {
    "move": { "type": "int", "min": 0, "default": 0 },
    "score": { "type": "map", "of": { "type": "float" } }
  },
  "kinds": {
    "infantry": {
      "base": { "shape": "circle", "diameter_in": 0.75 },
      "components": {
        "prisoner_of": { "type": "enum", "of": ["red", "blue"], "optional": true },
        "armed": { "type": "bool", "default": true }
      }
    },
    "gun": {
      "base": { "shape": "rect", "width_in": 1.5, "length_in": 3 },
      "components": { "fired_this_move": { "type": "bool", "default": false } }
    }
  }
}
```

- **Types:** `int`, `float`, `bool`, `string` (with optional `max_len`), `enum` (`of` lists the values), `entity` (an entity id, checked to exist), `list` and `map` (`of` gives the element type; map keys are strings). Numbers may have `min` and `max`.
- A component is required unless it has `"optional": true` or a `default`.
- **Bases:** `circle` (`diameter_in`) or `rect` (`width_in`, `length_in`, with length along the facing). Bases are what the engine measures between (ADR-005).
- `game` declares session-level components, such as the move number or scores, that belong to no entity.
- `sides` lists the sides seats can join. Every entity may have a side.

This is a small language of our own, not JSON Schema: it covers what the engine needs to validate and nothing it can't enforce.

### Tests (`tests/*.json`)

Each file is one scenario: setup options, a dice seed, a list of actions with the side taking each, and expectations, either on the events produced or on the state afterwards (component values, entities present or absent, positions within a tolerance). An action can also be expected to be rejected with a given code. The runner uses the real engine, so a test proves the scripts under the same rules as a game. CI runs every package's tests (Tier-C in ADR-000).

### Built-in packages and pinning

- Packages under `rulesets/` are built into `chartroomd` (`rulesets/embed.go`, `go:embed`). No package is loaded from elsewhere in M1. Private imports and uploads come later, with their own ADR.
- A package's **content hash** is the SHA-256 of its files, taken in sorted path order with each file's path and length. It changes when any file changes.
- When a session is created with a ruleset, the server stores the package's files in a `ruleset_packages` table keyed by hash, and the session's log records the name, version, and hash (ADR-005). The session then always runs that exact copy, even after the server ships a newer version. Moving a session to a newer version is a later feature.
- Replay doesn't need the scripts at all: the log holds outcomes (ADR-002). Pinning matters for continuing a game consistently, not for replay.

### Tools

- `chartroomd ruleset check <dir>` validates a package (manifest, schema, data, that the scripts load) and runs its tests. It's the same code the server runs at startup on the built-in packages.
- `make rulesets` runs it for every package under `rulesets/`. CI runs it.

## Alternatives considered

- **YAML.** Friendlier to write by hand, but it needs a dependency, and its implicit typing (`no` becomes false, `1.10` becomes 1.1) is a real hazard for rules data. Deferred, not rejected.
- **Data written in Starlark.** No new dependency and comments are allowed, but data stops being inspectable without running code, and DESIGN.md separates data from scripts.
- **Full JSON Schema.** Too large to implement ourselves and too large to validate cheaply. The engine only needs typed fields, ranges, enums, and references.
- **Sessions referring to a package by name only.** Simpler, but upgrading the server would change the rules of games in progress.
- **Packages as zip files.** Useful for sharing and uploads later. A plain directory diffs well in git, which is where M1's packages live.

## Consequences

- Every ruleset, from Little Wars to the SRD to homebrew, uses this layout and the schema language. Changing either is a Tier-H change with a `format` bump.
- Packages are validated at startup and in CI, so a broken built-in package fails the build rather than a game.
- Old sessions keep their own copy of the rules, at the cost of storing each used version once in the database.
- `extends` is reserved but unusable until a later ADR defines merging.
