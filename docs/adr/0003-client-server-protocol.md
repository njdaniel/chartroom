# ADR-003: Client–server protocol for M0

- **Status:** Proposed
- **Date:** 2026-11-02
- **Decider:** Nick (Tier-H: new ADR; touches seat permissions and visibility filtering)

## Context

Invariant 1 says the server is authoritative: clients send intents and render results. Invariant 4 says each client gets only what its seat can see, and filtering happens on the server. M0 has no fog of war, but the filtering hook must exist from day one so that M3 and M4 add rules to it rather than retrofit it.

v1 runs on a private tailnet with no accounts (DESIGN.md), so "identity" here means "which seat is this browser", not authentication.

## Decision

### Transport

- One WebSocket per client at `GET /ws`. Each frame is one JSON text message with a `type` field.
- Message types are Go structs in `internal/protocol`. `cmd/schema-ts` generates the TypeScript types, and CI fails if the generated file is out of date.
- The server pings every 20 s and drops a connection that misses two pings.

### Joining a session

```
client → server   hello    { protocol: 1, session: "s_…", seat_token?: "…", display_name: "Nick" }
server → client   welcome  { protocol: 1, seat: { id, display_name, role }, seat_token, state: {…}, seq }
```

- **No `seat_token`:** the server creates a seat, appends `seat.joined`, and returns a new `seat_token`. The first seat in a session gets `role: "gm"` and later seats get `"player"`.
- **With a `seat_token`:** the server returns the existing seat. The client keeps the token in `localStorage`, so a reload or reconnect puts you back in the same seat.
- `seat_token` is 128 random bits. Only its hash is stored. It identifies a seat; it doesn't authenticate a person.
- `state` is the session's current state **filtered for this seat**, as of `seq`. After `welcome`, the server streams every event after `seq`.
- A `protocol` mismatch gets `error { code: "protocol_mismatch" }` and the connection is closed.

### Intents and results

```
client → server   intent    { id: "i_…", kind: "token.move", data: { token_id, x, y, rotation } }
server → client   ack       { intent: "i_…", seqs: [43] }
server → client   rejected  { intent: "i_…", code: "unknown_token", message: "…" }
server → client   event     { event: <ADR-002 envelope, filtered for this seat> }
```

- The client creates `id`, so it can match the reply. Every intent gets exactly one `ack` or `rejected`, even when the resulting events are hidden from the sender.
- Clients don't move tokens optimistically in M0. A token moves when its event arrives. On a tailnet the round trip is a few milliseconds, and showing only server-confirmed state avoids rollback code. While you drag, your client draws a ghost token that isn't sent to anyone else.

M0 intent kinds:

| Kind | `data` | Produces |
| --- | --- | --- |
| `map.load` | `asset`, `width_in`, `height_in`, `grid` | `map.loaded` |
| `token.place` | `label`, `color`, `asset?`, `x`, `y`, `rotation`, `size_in` | `token.placed` |
| `token.move` | `token_id`, `x`, `y`, `rotation` | `token.moved` |
| `token.remove` | `token_id` | `token.removed` |
| `dice.roll` | `expr`, `label?` | `dice.rolled` |

Dice expressions in M0 are sums of `NdM` terms and integer modifiers (`1d20+5`, `2d6+1d4-1`), with N ≤ 100 and 2 ≤ M ≤ 1000. Anything else gets `rejected { code: "bad_dice_expr" }`.

### Inside the server

- Each open session has one goroutine, the **session loop**. Every intent for that session goes through it in order. The loop:
  1. calls `authorize(seat, intent, state)`, which allows everything for any seat in the session in M0 but is the single place seat permissions will live;
  2. validates the intent against current state and draws dice from the session source if needed;
  3. appends the resulting events in one transaction (ADR-002);
  4. applies them to the in-memory state;
  5. for each connected seat, calls `visibility.FilterEvent(seat, event, state)` and sends the result, if any.
- `welcome` uses the same rules through `visibility.FilterState(seat, state)`.
- **M0 filter rules:** remove `dice_seed` from `session.created` and from state, for every seat. Everything else passes through. Tests cover the seed redaction so the hook is exercised from the start.
- If a client's send buffer fills (a stuck tab), the server closes that connection. When the client reconnects it gets a fresh `welcome`, so the session loop never waits on a slow client.

### Things outside the log

- **Presence.** `presence { seats: [{ id, display_name, online }] }` is sent when someone connects or disconnects. It is not an event.
- **Rulers.** In M0 a ruler is drawn only in your own browser, showing straight-line inches. Server-side measurement arrives with the spatial engine.

### Lobby and assets (HTTP)

- `GET /` lists sessions. `POST /api/sessions { name }` creates one (appending `session.created`) and returns the `/s/<id>` link to share.
- `POST /api/assets` accepts a PNG, JPEG, or WebP up to 20 MB. The file type is checked from its bytes, not its name. The file is stored as `<data-dir>/assets/<sha256>`, and the hash is returned. `GET /assets/<sha256>` serves it with immutable caching.
- Events refer to images by hash only, so the log stays small and a replay finds the same image.

## Alternatives considered

- **Sending full state after every change.** Simpler for the client, but it doesn't scale to M3 battles, and it hides the event stream that replay and spectating need.
- **Filtering in the client.** Ruled out by invariant 4.
- **Resuming a reconnect from the last seq instead of sending a fresh `welcome`.** More efficient, but with filtering a seat's stream has gaps, so a client can't tell "hidden" from "lost". A fresh `welcome` is correct by construction. We can revisit if state gets large.
- **Socket.IO or a JSON-RPC library.** Rejected: adds dependencies for features we don't need.

## Consequences

- Every outgoing event and state goes through one filter function, so fog of war (M3) and AI seats (M4) add rules there rather than new code paths.
- Hidden events leave gaps in a seat's `seq`, which shows that *something* happened. That's acceptable for M0. M3 decides whether fog needs more than that.
- Anyone on the tailnet with a session link can join it, and the first seat becomes GM. That matches v1's trust model (a private group) and is listed as a question for Nick.
- M4's MCP endpoint will be another client of the same session loop and filter, not a second path into state.
