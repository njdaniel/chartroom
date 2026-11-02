// The client's only way to read and write WebSocket frames. Every message
// type comes from protocol.gen.ts, which is generated from the server's Go
// types (`make gen`); none is written by hand.

import {
  TypeAck,
  TypeError,
  TypeEvent,
  TypePresence,
  TypeRejected,
  TypeWelcome,
  Version,
  type ClientMessage,
  type HelloMessage,
  type Intent,
  type IntentData,
  type ServerMessage,
} from "./protocol.gen";

export * from "./protocol.gen";

const serverTypes: ReadonlySet<string> = new Set([
  TypeWelcome,
  TypeAck,
  TypeRejected,
  TypeEvent,
  TypePresence,
  TypeError,
]);

/**
 * Reads a frame from the server. The server is trusted to send what its
 * types say; this only checks that the frame is a message type this client
 * knows, so a newer server's additions are reported rather than misread.
 */
export function parseServerMessage(text: string): ServerMessage {
  const msg: unknown = JSON.parse(text);
  if (
    typeof msg !== "object" ||
    msg === null ||
    !("type" in msg) ||
    typeof msg.type !== "string" ||
    !serverTypes.has(msg.type)
  ) {
    throw new Error(`unknown message from the server: ${text.slice(0, 200)}`);
  }
  return msg as ServerMessage;
}

/** Writes a frame for the server. */
export function encode(msg: ClientMessage): string {
  return JSON.stringify(msg);
}

/** The first frame on a connection. */
export function hello(session: string, displayName: string, seatToken?: string): HelloMessage {
  return {
    type: "hello",
    protocol: Version,
    session,
    display_name: displayName,
    ...(seatToken ? { seat_token: seatToken } : {}),
  };
}

/** An intent with its data checked against its kind. */
export function intent<K extends keyof IntentData>(id: string, kind: K, data: IntentData[K]): Intent {
  return { type: "intent", id, kind, data } as Intent;
}
