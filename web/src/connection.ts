// One seat's connection to a session (ADR-003). It keeps the seat token so a
// reload or reconnect returns to the same seat, holds the table state, and
// reconnects with a fresh welcome whenever the socket drops.

import {
  ErrProtocolMismatch,
  ErrUnknownSeat,
  ErrUnknownSession,
  encode,
  hello,
  intent,
  parseServerMessage,
  type IntentData,
  type PresenceSeat,
  type RejectedMessage,
  type Seat,
  type State,
} from "./protocol";
import { applyEvent } from "./state";

/** The part of WebSocket this module uses, so tests can fake it. */
export interface Socket {
  onopen: ((ev: unknown) => void) | null;
  onmessage: ((ev: { data: unknown }) => void) | null;
  onclose: ((ev: unknown) => void) | null;
  send(data: string): void;
  close(): void;
}

/** The part of localStorage this module uses. */
export interface KeyValue {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

export type Status =
  | { kind: "connecting" }
  | { kind: "open" }
  | { kind: "retrying"; inMs: number }
  | { kind: "failed"; message: string }; // will not reconnect

export interface Handlers {
  /** After the welcome and after every event. */
  state?(state: State, seat: Seat): void;
  presence?(seats: PresenceSeat[]): void;
  status?(status: Status): void;
}

/** What became of an intent. */
export type Outcome = { ok: true; seqs: number[] } | { ok: false; rejected: RejectedMessage };

export interface Options {
  url: string; // ws://host/ws
  session: string;
  displayName: string;
  storage: KeyValue;
  handlers: Handlers;
  socket?: (url: string) => Socket;
  /** First reconnect delay; it doubles up to maxDelayMs. */
  delayMs?: number;
  maxDelayMs?: number;
}

export function tokenKey(session: string): string {
  return `chartroom.seat.${session}`;
}

let intentCounter = 0;

/** Ids only need to be unique per connection, so no randomness is used. */
function newIntentID(): string {
  intentCounter += 1;
  return `i_${Date.now().toString(36)}_${intentCounter}`;
}

export class Connection {
  private readonly o: Required<Omit<Options, "handlers">> & { handlers: Handlers };
  private sock: Socket | null = null;
  private stopped = false;
  private delay: number;
  private retryTimer: ReturnType<typeof setTimeout> | null = null;
  private pending = new Map<string, (o: Outcome) => void>();

  /** The table as of the latest event, once welcomed. */
  state: State | null = null;
  seat: Seat | null = null;

  constructor(opts: Options) {
    this.o = {
      socket: (url) => new WebSocket(url) as unknown as Socket,
      delayMs: 500,
      maxDelayMs: 10_000,
      ...opts,
    };
    this.delay = this.o.delayMs;
  }

  start(): void {
    this.stopped = false;
    this.connect();
  }

  stop(): void {
    this.stopped = true;
    if (this.retryTimer) clearTimeout(this.retryTimer);
    this.sock?.close();
    this.sock = null;
  }

  /**
   * Sends an intent. The promise settles when the server acks or rejects
   * it. If the connection drops first, it settles as rejected with code
   * "disconnected", since the server may or may not have applied it: the
   * fresh welcome shows which.
   */
  send<K extends keyof IntentData>(kind: K, data: IntentData[K]): Promise<Outcome> {
    const id = newIntentID();
    return new Promise((resolve) => {
      if (!this.sock || !this.seat) {
        resolve(disconnected(id));
        return;
      }
      this.pending.set(id, resolve);
      this.sock.send(encode(intent(id, kind, data)));
    });
  }

  private connect(): void {
    this.o.handlers.status?.({ kind: "connecting" });
    const sock = this.o.socket(this.o.url);
    this.sock = sock;
    sock.onopen = () => {
      const token = this.o.storage.getItem(tokenKey(this.o.session)) ?? undefined;
      sock.send(encode(hello(this.o.session, this.o.displayName, token)));
    };
    sock.onmessage = (ev) => {
      if (this.sock === sock && typeof ev.data === "string") this.receive(ev.data);
    };
    sock.onclose = () => {
      if (this.sock === sock) this.dropped();
    };
  }

  private receive(text: string): void {
    const msg = parseServerMessage(text);
    switch (msg.type) {
      case "welcome":
        this.o.storage.setItem(tokenKey(this.o.session), msg.seat_token);
        this.seat = msg.seat;
        this.state = msg.state;
        this.delay = this.o.delayMs;
        this.o.handlers.status?.({ kind: "open" });
        this.o.handlers.state?.(msg.state, msg.seat);
        break;
      case "event":
        if (this.state && this.seat) {
          this.state = applyEvent(this.state, msg.event);
          this.o.handlers.state?.(this.state, this.seat);
        }
        break;
      case "ack":
        this.settle(msg.intent, { ok: true, seqs: msg.seqs });
        break;
      case "rejected":
        this.settle(msg.intent, { ok: false, rejected: msg });
        break;
      case "presence":
        this.o.handlers.presence?.(msg.seats);
        break;
      case "error":
        if (msg.code === ErrUnknownSeat) {
          // The token is stale (say, the database was reset). Join afresh.
          this.o.storage.removeItem(tokenKey(this.o.session));
          this.delay = 0;
        } else if (msg.code === ErrUnknownSession || msg.code === ErrProtocolMismatch) {
          this.fail(msg.message);
        }
        break;
      default: {
        const unhandled: never = msg;
        throw new Error(`unhandled message ${JSON.stringify(unhandled)}`);
      }
    }
  }

  private settle(id: string, outcome: Outcome): void {
    const resolve = this.pending.get(id);
    this.pending.delete(id);
    resolve?.(outcome);
  }

  private dropped(): void {
    this.sock = null;
    this.seat = null;
    for (const [id, resolve] of this.pending) resolve(disconnected(id));
    this.pending.clear();
    if (this.stopped) return;
    const inMs = this.delay;
    this.delay = Math.min(this.o.maxDelayMs, Math.max(this.o.delayMs, this.delay * 2));
    this.o.handlers.status?.({ kind: "retrying", inMs });
    this.retryTimer = setTimeout(() => this.connect(), inMs);
  }

  private fail(message: string): void {
    this.stopped = true;
    this.o.handlers.status?.({ kind: "failed", message });
  }
}

function disconnected(id: string): Outcome {
  return {
    ok: false,
    rejected: { type: "rejected", intent: id, code: "disconnected", message: "not connected to the server" },
  };
}
