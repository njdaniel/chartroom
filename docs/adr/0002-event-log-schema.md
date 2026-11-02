# ADR-002: Event log schema, v1

- **Status:** Accepted (Nick, on PR #1, 2026-11-02)
- **Date:** 2026-11-02
- **Decider:** Nick (Tier-H: new ADR, event log schema)

## Context

Invariant 2: all state changes are events in the log, and state is a fold over the log. Every saved game depends on this format, so ADR-000 protects it. M0 needs only a handful of event types. M1 will add many more, including types defined by rulesets, and must be able to do so without changing the envelope.

## Decision

### What gets logged

The log records **outcomes, not requests**. A client's intent ("move token 7 to 12,4") is checked by the server. If it is accepted, the resulting events are logged. Rejected intents are not logged. Dice results are logged, so replay never re-rolls: replaying a session reads the same numbers back from the log.

### Envelope

Every event, in storage and on the wire, has this shape:

```json
{
  "v": 1,
  "id": "e_9f2c41d07a6b4e15b3c8a0d6f1e27c94",
  "session": "s_4b1e…",
  "seq": 42,
  "at": "2026-11-02T19:04:11.512Z",
  "seat": "seat_7a…",
  "cause": "i_client-intent-id",
  "type": "token.moved",
  "data_v": 1,
  "data": { }
}
```

| Field | Meaning |
| --- | --- |
| `v` | Envelope version. Only an ADR changes it. Readers reject versions they don't know. |
| `id` | 128 random bits from `crypto/rand`, hex, prefixed `e_`. Globally unique. |
| `session` | The session this event belongs to. |
| `seq` | Position in the session's log: starts at 1, gapless, assigned by the server when the event is appended. Seq is the order; nothing else is. |
| `at` | Server wall-clock time, UTC. **For display and audit only.** The fold never reads it, so replay doesn't depend on when it runs, and M1 ruleset scripts can't see the clock (invariant 5). |
| `seat` | The seat whose intent caused the event, or `null` for system events. |
| `cause` | The id of the client intent that produced it, if any. Lets a client match results to its requests. |
| `type` | Event type name (see naming below). |
| `data_v` | Version of this type's `data` shape. |
| `data` | The type-specific payload. |

### Storage

```sql
CREATE TABLE events (
  session_id TEXT    NOT NULL,
  seq        INTEGER NOT NULL,
  id         TEXT    NOT NULL UNIQUE,
  at         TEXT    NOT NULL,
  seat_id    TEXT,
  cause      TEXT,
  type       TEXT    NOT NULL,
  data_v     INTEGER NOT NULL,
  data       TEXT    NOT NULL,   -- JSON
  PRIMARY KEY (session_id, seq)
) WITHOUT ROWID;
```

- Envelope fields are columns, so they can be queried. The payload is JSON text.
- Triggers reject every `UPDATE` and `DELETE` on `events`. The log is append-only. Undo (later) is a new event that reverses an earlier one, never a deletion.
- One goroutine per session owns appends (ADR-003). It assigns `seq` and writes the events from one intent in a single transaction, so an intent's events are all-or-nothing.
- The envelope `v` is not stored per row: version 1 is the table's shape. A future envelope v2 is a migration plus an ADR.

### Snapshots

```sql
CREATE TABLE snapshots (
  session_id TEXT    NOT NULL,
  seq        INTEGER NOT NULL,   -- state after applying this event
  state_v    INTEGER NOT NULL,   -- version of the fold code that wrote it
  state      TEXT    NOT NULL,   -- JSON
  PRIMARY KEY (session_id, seq)
) WITHOUT ROWID;
```

- A snapshot is a cache. Deleting every snapshot must never lose anything.
- Loading a session: take the newest snapshot whose `state_v` matches the running code, then fold the events after its `seq`. If none matches, fold from seq 1.
- Snapshots are written every 500 events and on clean shutdown.
- A test checks that folding from zero gives the same state as snapshot-plus-tail, for generated event sequences.

### Versioning

- **Adding an optional field** to a type's `data` keeps its `data_v`. Readers ignore fields they don't know.
- **Any other change** bumps `data_v` and adds an *upcaster*: a pure function from the old shape to the new one. Upcasters run when events are read. Stored events are never rewritten. The fold only ever sees the newest version of each type.
- **An unknown `type` or `data_v`** on read is a hard error, never skipped. Skipping an event means building the wrong state.
- Every change to this list of types or their fields is an ADR-000 sign-off item.

### Type naming

`<area>.<past-tense verb>`, lowercase, dot-separated: `token.moved`, `dice.rolled`. Engine types use plain names. The prefix `rs.` is **reserved** for event types that rulesets define (`rs.<package>.<name>`). Their shape belongs to the M1 ruleset ADR. This ADR only guarantees that they fit in the same envelope.

### M0 event types (all `data_v: 1`)

Coordinates are inches on the table plane, `float64`. Rotation is degrees clockwise from north.

| Type | `data` fields |
| --- | --- |
| `session.created` | `name`, `dice_seed` (64 hex chars, 32 bytes) |
| `seat.joined` | `seat_id`, `display_name`, `role` (`"gm"` or `"player"`) |
| `map.loaded` | `asset` (sha256 of the image), `width_in`, `height_in`, `grid` (`null`, or `{kind: "square"\|"hex", size_in, origin_x, origin_y}`; drawn only in M0, no snapping) |
| `token.placed` | `token_id`, `label`, `color`, `asset` (optional image sha256), `x`, `y`, `rotation`, `size_in` (diameter) |
| `token.moved` | `token_id`, `from: {x, y, rotation}`, `to: {x, y, rotation}` (the server fills in `from` from current state) |
| `token.removed` | `token_id` |
| `dice.rolled` | `expr` (as typed, e.g. `"2d6+3"`), `label` (optional), `terms: [{count, sides, results: [int]}]`, `modifier`, `total` |

Ids such as `token_id` and `seat_id` are assigned by the server, never by the client.

Things that are **not** events: who is connected right now, cursor positions, and M0's rulers. These are not game state, so they stay out of the log (ADR-003).

### Dice and the log

- The seed is created with `crypto/rand` and recorded in `session.created`.
- Roll number *i* of a session (0 for the first) draws from a ChaCha8 generator keyed by SHA-256 of a fixed domain string, the seed, and *i*. The fold only counts `dice.rolled` events. A restarted server reads that count and continues the same sequence, without re-rolling or storing generator state. *(Amended on #5: the first version kept one running ChaCha8 state in the fold and in snapshots, which would have meant re-rolling every die on load.)*
- `dice_seed` is game state but secret: anyone holding it could predict every future roll. The visibility filter (ADR-003) removes it for every seat. Revealing it after a session ends, so players can check the rolls, is deferred until sessions have an end.

## Alternatives considered

- **Logging intents (commands) and re-running them on replay.** Rejected: replay would depend on the server code at replay time and on re-rolling dice. Logging outcomes makes replay a plain fold.
- **One table per event type.** Rejected: every new M1 type would need a migration.
- **Protobuf, CBOR, or MessagePack payloads.** Rejected for v1: they add dependencies and code generation, and the log can't be read with the `sqlite3` command line. JSON is compact enough at tabletop volumes.
- **A global sequence across sessions.** Rejected: sessions are independent, and per-session seq is what clients and replay need.

## Consequences

- M1 adds event types without touching the envelope or the tables.
- Folding is pure, and dice results come from the log, so replay, spectating, and headless simulation share one code path.
- The log only grows. Long campaigns rely on snapshots for load time; disk use is small (a few hundred bytes per event).
- Event `data` is part of the public format from day one. Careless field names are expensive later, which is why this ADR fixes them now.
