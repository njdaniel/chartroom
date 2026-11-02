// The client's copy of the table: the welcome state, with each event folded
// in as it arrives. It mirrors the server's fold (internal/table), but only
// for display: the server has already checked every event.

import type { KnownEvent, State } from "./protocol";

/**
 * Returns state with e applied. state is not changed. Events at or before
 * state.seq are ignored. Gaps in seq are normal: they are events this seat
 * may not see.
 */
export function applyEvent(state: State, e: KnownEvent): State {
  if (e.seq <= state.seq) {
    return state;
  }
  const s: State = { ...state, seq: e.seq };
  switch (e.type) {
    case "seat.joined":
      s.seats = {
        ...s.seats,
        [e.data.seat_id]: { id: e.data.seat_id, display_name: e.data.display_name, role: e.data.role },
      };
      break;
    case "map.loaded":
      s.map = e.data;
      break;
    case "token.placed": {
      const d = e.data;
      s.tokens = {
        ...s.tokens,
        [d.token_id]: {
          id: d.token_id,
          label: d.label,
          color: d.color,
          ...(d.asset ? { asset: d.asset } : {}),
          pose: { x: d.x, y: d.y, rotation: d.rotation },
          size_in: d.size_in,
        },
      };
      break;
    }
    case "token.moved": {
      const t = s.tokens[e.data.token_id];
      if (t) {
        s.tokens = { ...s.tokens, [t.id]: { ...t, pose: e.data.to } };
      }
      break;
    }
    case "token.removed": {
      const gone = e.data.token_id;
      s.tokens = Object.fromEntries(Object.entries(s.tokens).filter(([id]) => id !== gone));
      break;
    }
    case "dice.rolled":
      s.rolls = [...s.rolls, { seq: e.seq, seat: e.seat ?? "", ...e.data }];
      break;
    default: {
      // A compile error here means EventData has a type this switch misses.
      const unhandled: never = e;
      return unhandled;
    }
  }
  return s;
}
