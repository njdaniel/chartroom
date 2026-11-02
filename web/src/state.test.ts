import { describe, expect, it } from "vitest";
import type { EventData, KnownEvent, State } from "./protocol";
import { applyEvent } from "./state";

const empty: State = { session: "s_1", seq: 2, name: "Friday game", seats: {}, map: null, tokens: {}, rolls: [] };

function ev<K extends keyof EventData>(seq: number, type: K, data: EventData[K], seat: string | null = "seat_1"): KnownEvent {
  return { v: 1, id: `e_${seq}`, session: "s_1", seq, at: "2026-11-02T00:00:00Z", seat, data_v: 1, type, data } as KnownEvent;
}

const placed = ev(3, "token.placed", { token_id: "t_1", label: "Orc", color: "#aa0000", x: 1, y: 2, rotation: 90, size_in: 1 });

describe("applyEvent", () => {
  it("seat.joined adds a seat", () => {
    const s = applyEvent(empty, ev(3, "seat.joined", { seat_id: "seat_2", display_name: "Sam", role: "player" }));
    expect(s.seats["seat_2"]).toEqual({ id: "seat_2", display_name: "Sam", role: "player" });
    expect(s.seq).toBe(3);
  });

  it("map.loaded sets the map", () => {
    const map = { asset: "ab".repeat(32), width_in: 48, height_in: 36, grid: { kind: "square", size_in: 1, origin_x: 0, origin_y: 0 } };
    expect(applyEvent(empty, ev(3, "map.loaded", map)).map).toEqual(map);
  });

  it("token.placed adds a token", () => {
    expect(applyEvent(empty, placed).tokens["t_1"]).toEqual({
      id: "t_1",
      label: "Orc",
      color: "#aa0000",
      pose: { x: 1, y: 2, rotation: 90 },
      size_in: 1,
    });
    const withImage = ev(3, "token.placed", { token_id: "t_2", label: "", color: "#000000", asset: "cd".repeat(32), x: 0, y: 0, rotation: 0, size_in: 2 });
    expect(applyEvent(empty, withImage).tokens["t_2"]?.asset).toBe("cd".repeat(32));
  });

  it("token.moved moves a token", () => {
    const s = applyEvent(
      applyEvent(empty, placed),
      ev(4, "token.moved", { token_id: "t_1", from: { x: 1, y: 2, rotation: 90 }, to: { x: 5, y: 6, rotation: 180 } }),
    );
    expect(s.tokens["t_1"]?.pose).toEqual({ x: 5, y: 6, rotation: 180 });
    expect(s.tokens["t_1"]?.label).toBe("Orc");
  });

  it("token.removed removes a token", () => {
    const s = applyEvent(applyEvent(empty, placed), ev(4, "token.removed", { token_id: "t_1" }));
    expect(s.tokens).toEqual({});
  });

  it("dice.rolled adds to the dice log, with who rolled", () => {
    const roll = { expr: "2d6+3", label: "damage", terms: [{ count: 2, sides: 6, results: [4, 1] }], modifier: 3, total: 8 };
    const s = applyEvent(empty, ev(3, "dice.rolled", roll, "seat_2"));
    expect(s.rolls).toEqual([{ seq: 3, seat: "seat_2", ...roll }]);
  });

  it("does not change the state it was given", () => {
    const before = applyEvent(empty, placed);
    const snapshot = structuredClone(before);
    applyEvent(before, ev(4, "token.moved", { token_id: "t_1", from: { x: 1, y: 2, rotation: 90 }, to: { x: 9, y: 9, rotation: 0 } }));
    applyEvent(before, ev(4, "token.removed", { token_id: "t_1" }));
    expect(before).toEqual(snapshot);
  });

  it("ignores events it already has, and accepts gaps", () => {
    const s = applyEvent(empty, placed);
    expect(applyEvent(s, placed)).toBe(s);
    expect(applyEvent(s, ev(2, "token.removed", { token_id: "t_1" }))).toBe(s);
    expect(applyEvent(s, ev(9, "token.removed", { token_id: "t_1" })).seq).toBe(9);
  });
});
