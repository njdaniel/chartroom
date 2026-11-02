import { describe, expect, it } from "vitest";
import { checkHealth } from "./health";

const respond = (status: number, body: unknown): typeof fetch =>
  async () => new Response(JSON.stringify(body), { status });

describe("checkHealth", () => {
  it("reports ok when the server says ok", async () => {
    expect(await checkHealth(respond(200, { status: "ok" }))).toBe("ok");
  });

  it("reports unreachable on an error status", async () => {
    expect(await checkHealth(respond(503, { status: "ok" }))).toBe("unreachable");
  });

  it("reports unreachable on an unexpected body", async () => {
    expect(await checkHealth(respond(200, { status: "starting" }))).toBe("unreachable");
  });

  it("reports unreachable when fetch throws", async () => {
    const failing: typeof fetch = async () => {
      throw new TypeError("network down");
    };
    expect(await checkHealth(failing)).toBe("unreachable");
  });
});
