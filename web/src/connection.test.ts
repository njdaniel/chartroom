import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Connection, tokenKey, type KeyValue, type Socket, type Status } from "./connection";
import type { State } from "./protocol";

class FakeSocket implements Socket {
  onopen: ((ev: unknown) => void) | null = null;
  onmessage: ((ev: { data: unknown }) => void) | null = null;
  onclose: ((ev: unknown) => void) | null = null;
  sent: unknown[] = [];
  closed = false;
  send(data: string): void {
    this.sent.push(JSON.parse(data));
  }
  close(): void {
    this.closed = true;
  }
  // Server side of the fake.
  open(): void {
    this.onopen?.({});
  }
  push(msg: unknown): void {
    this.onmessage?.({ data: JSON.stringify(msg) });
  }
  drop(): void {
    this.onclose?.({});
  }
}

class MemoryStorage implements KeyValue {
  m = new Map<string, string>();
  getItem(k: string): string | null {
    return this.m.get(k) ?? null;
  }
  setItem(k: string, v: string): void {
    this.m.set(k, v);
  }
  removeItem(k: string): void {
    this.m.delete(k);
  }
}

const seat = { id: "seat_1", display_name: "Nick", role: "gm" };
const state: State = { session: "s_1", seq: 2, name: "Friday game", seats: { seat_1: seat }, map: null, tokens: {}, rolls: [] };
const welcome = (token = "tok_1", st: State = state) => ({ type: "welcome", protocol: 1, seat, seat_token: token, state: st, seq: st.seq });
const placedEvent = {
  type: "event",
  event: {
    v: 1, id: "e_3", session: "s_1", seq: 3, at: "2026-11-02T00:00:00Z", seat: "seat_1", cause: "i_x", type: "token.placed", data_v: 1,
    data: { token_id: "t_1", label: "Orc", color: "#aa0000", x: 1, y: 2, rotation: 0, size_in: 1 },
  },
};

function setup(storage = new MemoryStorage()) {
  const sockets: FakeSocket[] = [];
  const states: State[] = [];
  const statuses: Status[] = [];
  const conn = new Connection({
    url: "ws://test/ws",
    session: "s_1",
    displayName: "Nick",
    storage,
    socket: () => {
      const s = new FakeSocket();
      sockets.push(s);
      return s;
    },
    handlers: { state: (s) => states.push(s), status: (s) => statuses.push(s) },
    delayMs: 100,
    maxDelayMs: 400,
  });
  const last = () => {
    const s = sockets[sockets.length - 1];
    if (!s) throw new Error("no socket");
    return s;
  };
  return { conn, sockets, states, statuses, storage, last };
}

beforeEach(() => {
  vi.useFakeTimers();
});
afterEach(() => {
  vi.useRealTimers();
});

describe("Connection", () => {
  it("says hello without a token, then keeps the one it is given", () => {
    const t = setup();
    t.conn.start();
    t.last().open();
    expect(t.last().sent[0]).toEqual({ type: "hello", protocol: 1, session: "s_1", display_name: "Nick" });
    t.last().push(welcome("tok_1"));
    expect(t.storage.getItem(tokenKey("s_1"))).toBe("tok_1");
    expect(t.states).toEqual([state]);
    expect(t.conn.seat).toEqual(seat);
  });

  it("comes back to the same seat after a reload", () => {
    const storage = new MemoryStorage();
    storage.setItem(tokenKey("s_1"), "tok_1");
    const t = setup(storage);
    t.conn.start();
    t.last().open();
    expect(t.last().sent[0]).toMatchObject({ type: "hello", seat_token: "tok_1" });
  });

  it("applies events, and moves nothing until they arrive", async () => {
    const t = setup();
    t.conn.start();
    t.last().open();
    t.last().push(welcome());

    const sent = t.conn.send("token.move", { token_id: "t_1", x: 5, y: 5, rotation: 0 });
    expect(t.last().sent[1]).toMatchObject({ type: "intent", kind: "token.move", data: { token_id: "t_1", x: 5 } });
    expect(t.states).toHaveLength(1); // sending changed nothing

    t.last().push(placedEvent);
    expect(t.conn.state?.tokens["t_1"]?.pose).toEqual({ x: 1, y: 2, rotation: 0 });
    const id = (t.last().sent[1] as { id: string }).id;
    t.last().push({ type: "ack", intent: id, seqs: [3] });
    await expect(sent).resolves.toEqual({ ok: true, seqs: [3] });
  });

  it("passes rejections back to the sender", async () => {
    const t = setup();
    t.conn.start();
    t.last().open();
    t.last().push(welcome());
    const sent = t.conn.send("dice.roll", { expr: "1d20 5" });
    const id = (t.last().sent[1] as { id: string }).id;
    t.last().push({ type: "rejected", intent: id, code: "bad_dice_expr", message: "space inside" });
    await expect(sent).resolves.toMatchObject({ ok: false, rejected: { code: "bad_dice_expr", message: "space inside" } });
  });

  it("reconnects with backoff and a fresh welcome", async () => {
    const t = setup();
    t.conn.start();
    t.last().open();
    t.last().push(welcome());
    const inFlight = t.conn.send("dice.roll", { expr: "1d20" });

    t.last().drop();
    await expect(inFlight).resolves.toMatchObject({ ok: false, rejected: { code: "disconnected" } });
    expect(t.statuses.at(-1)).toEqual({ kind: "retrying", inMs: 100 });
    await expect(t.conn.send("dice.roll", { expr: "1d20" })).resolves.toMatchObject({ ok: false });

    vi.advanceTimersByTime(100);
    expect(t.sockets).toHaveLength(2);
    t.last().drop(); // the server is still down
    expect(t.statuses.at(-1)).toEqual({ kind: "retrying", inMs: 200 });
    vi.advanceTimersByTime(200);
    t.last().drop();
    expect(t.statuses.at(-1)).toEqual({ kind: "retrying", inMs: 400 });
    vi.advanceTimersByTime(400);
    t.last().drop();
    expect(t.statuses.at(-1)).toEqual({ kind: "retrying", inMs: 400 }); // capped

    vi.advanceTimersByTime(400);
    t.last().open();
    expect(t.last().sent[0]).toMatchObject({ seat_token: "tok_1" });
    const later = { ...state, seq: 9, name: "Friday game" };
    t.last().push(welcome("tok_1", later));
    expect(t.conn.state).toEqual(later);
    expect(t.statuses.at(-1)).toEqual({ kind: "open" });

    // A successful welcome resets the backoff.
    t.last().drop();
    expect(t.statuses.at(-1)).toEqual({ kind: "retrying", inMs: 100 });
  });

  it("forgets a stale seat token and joins afresh", () => {
    const storage = new MemoryStorage();
    storage.setItem(tokenKey("s_1"), "stale");
    const t = setup(storage);
    t.conn.start();
    t.last().open();
    t.last().push({ type: "error", code: "unknown_seat", message: "no" });
    t.last().drop();
    expect(storage.getItem(tokenKey("s_1"))).toBeNull();
    vi.advanceTimersByTime(0);
    t.last().open();
    expect(t.last().sent[0]).not.toHaveProperty("seat_token");
  });

  it("gives up on an unknown session", () => {
    const t = setup();
    t.conn.start();
    t.last().open();
    t.last().push({ type: "error", code: "unknown_session", message: "no session with that id" });
    t.last().drop();
    expect(t.statuses.at(-1)).toEqual({ kind: "failed", message: "no session with that id" });
    vi.advanceTimersByTime(60_000);
    expect(t.sockets).toHaveLength(1);
  });

  it("stops for good", () => {
    const t = setup();
    t.conn.start();
    t.conn.stop();
    expect(t.sockets[0]?.closed).toBe(true);
    vi.advanceTimersByTime(60_000);
    expect(t.sockets).toHaveLength(1);
  });
});
