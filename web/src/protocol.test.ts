import { describe, expect, it } from "vitest";
import {
  KindDiceRoll,
  KindTokenMove,
  TypeTokenMoved,
  encode,
  hello,
  intent,
  parseServerMessage,
} from "./protocol";

// Frames as the server writes them (from internal/server's tests).
const welcome = `{"type":"welcome","protocol":1,"seat":{"id":"seat_1","display_name":"Nick","role":"gm"},"seat_token":"abc","state":{"session":"s_1","seq":2,"name":"Friday game","seats":{"seat_1":{"id":"seat_1","display_name":"Nick","role":"gm"}},"map":null,"tokens":{},"rolls":[]},"seq":2}`;
const moved = `{"type":"event","event":{"v":1,"id":"e_1","session":"s_1","seq":3,"at":"2026-11-02T20:50:36Z","seat":"seat_1","cause":"i_1","type":"token.moved","data_v":1,"data":{"token_id":"t_1","from":{"x":1,"y":2,"rotation":0},"to":{"x":3,"y":4,"rotation":90}}}}`;

describe("parseServerMessage", () => {
  it("reads a welcome", () => {
    const msg = parseServerMessage(welcome);
    if (msg.type !== "welcome") throw new Error(msg.type);
    expect(msg.seat.role).toBe("gm");
    expect(msg.state.seats["seat_1"]?.display_name).toBe("Nick");
  });

  it("types event data by event type", () => {
    const msg = parseServerMessage(moved);
    if (msg.type !== "event") throw new Error(msg.type);
    const e = msg.event;
    if (e.type !== TypeTokenMoved) throw new Error(e.type);
    // e.data is TokenMoved here, with no cast.
    expect(e.data.to).toEqual({ x: 3, y: 4, rotation: 90 });
  });

  it("refuses frames it does not know", () => {
    expect(() => parseServerMessage(`{"type":"teleport"}`)).toThrow(/unknown message/);
    expect(() => parseServerMessage(`[]`)).toThrow(/unknown message/);
    expect(() => parseServerMessage(`"welcome"`)).toThrow(/unknown message/);
  });
});

describe("client frames", () => {
  it("writes hello with or without a seat token", () => {
    expect(JSON.parse(encode(hello("s_1", "Nick")))).toEqual({
      type: "hello",
      protocol: 1,
      session: "s_1",
      display_name: "Nick",
    });
    expect(JSON.parse(encode(hello("s_1", "Nick", "abc")))).toMatchObject({ seat_token: "abc" });
  });

  it("writes intents whose data matches their kind", () => {
    const roll = intent("i_1", KindDiceRoll, { expr: "2d6+1", label: "attack" });
    expect(JSON.parse(encode(roll))).toEqual({
      type: "intent",
      id: "i_1",
      kind: "dice.roll",
      data: { expr: "2d6+1", label: "attack" },
    });

    // @ts-expect-error token.move needs token_id, x, y, and rotation.
    intent("i_2", KindTokenMove, { expr: "1d20" });
  });
});
