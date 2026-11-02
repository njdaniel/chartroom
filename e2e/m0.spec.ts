// The M0 gate (DESIGN.md, issue #12): two players in two browsers share one
// table through a real chartroomd, and the table survives a server restart,
// rebuilt from the event log.
//
// Needs bin/chartroomd built with the client embedded: `make build`, or run
// it all with `make e2e`.

import { expect, test, type Browser, type Page } from "@playwright/test";
import { spawn, type ChildProcess } from "node:child_process";
import { existsSync, mkdtempSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import type { ChartroomHandle } from "../web/src/page";
import type { State } from "../web/src/protocol";

// Playwright runs from web/.
const binary = resolve(process.cwd(), "..", "bin", "chartroomd");

/** A chartroomd on its own port and data directory. */
class Server {
  private proc: ChildProcess | null = null;
  constructor(
    readonly port: number,
    readonly dataDir: string,
  ) {}

  get url(): string {
    return `http://127.0.0.1:${this.port}`;
  }

  async start(): Promise<void> {
    const proc = spawn(binary, ["-addr", `127.0.0.1:${this.port}`, "-data", this.dataDir], { stdio: "pipe" });
    proc.stderr?.on("data", (d) => console.log(`[chartroomd] ${d.toString().trimEnd()}`));
    this.proc = proc;
    for (let i = 0; i < 100; i++) {
      if (proc.exitCode !== null) throw new Error(`chartroomd exited with ${proc.exitCode}`);
      try {
        if ((await fetch(`${this.url}/healthz`)).ok) return;
      } catch {
        // not up yet
      }
      await new Promise((r) => setTimeout(r, 100));
    }
    throw new Error("chartroomd did not start");
  }

  /** Kills the server without a clean shutdown, so it writes no snapshot. */
  async crash(): Promise<void> {
    const proc = this.proc;
    if (!proc || proc.exitCode !== null) return;
    const exited = new Promise((r) => proc.once("exit", r));
    proc.kill("SIGKILL");
    await exited;
    this.proc = null;
  }
}

function freePort(): Promise<number> {
  return new Promise((ok) => {
    const s = createServer();
    s.listen(0, "127.0.0.1", () => {
      const a = s.address();
      s.close(() => ok(typeof a === "object" && a ? a.port : 0));
    });
  });
}

declare global {
  interface Window {
    chartroom?: ChartroomHandle;
  }
}

const tableOf = (p: Page): Promise<State | null> => p.evaluate(() => window.chartroom?.state() ?? null);

async function tokenNamed(p: Page, label: string) {
  const st = await tableOf(p);
  return Object.values(st?.tokens ?? {}).find((t) => t.label === label) ?? null;
}

const pageErrors: string[] = [];

/** A new player, in their own browser context, at the lobby. */
async function openLobby(browser: Browser, who: string): Promise<Page> {
  const page = await (await browser.newContext()).newPage();
  page.on("pageerror", (err) => pageErrors.push(`${who}: ${err.message}`));
  await page.goto(server.url + "/");
  return page;
}

async function enterName(page: Page, name: string): Promise<void> {
  await page.getByLabel("Your name").fill(name);
  await page.getByRole("button", { name: "Join" }).click();
  await expect(page.locator(".status")).toHaveText("Connected");
}

let server: Server;
let dataDir: string;

test.beforeAll(async () => {
  if (!existsSync(binary)) throw new Error(`${binary} is missing: run \`make build\` first`);
  dataDir = mkdtempSync(join(tmpdir(), "chartroom-e2e-"));
  server = new Server(await freePort(), dataDir);
  await server.start();
});

test.afterAll(async () => {
  await server.crash();
  rmSync(dataDir, { recursive: true, force: true });
});

test("two players share a table, and it survives a restart", async ({ browser }) => {
  // Both players join one session from the lobby link.
  const a = await openLobby(browser, "Alice");
  await a.getByLabel("Session name").fill("Friday game");
  await a.getByRole("button", { name: "Start a session" }).click();
  await enterName(a, "Alice");

  const b = await openLobby(browser, "Bob");
  await b.getByRole("link", { name: "Friday game" }).click();
  await enterName(b, "Bob");
  expect(b.url()).toBe(a.url());
  await expect(a.locator(".people")).toContainText("Bob");

  // Player A places and moves a token; player B sees it move.
  await a.getByLabel("Token label").fill("Orc");
  await a.getByRole("button", { name: "Place at centre" }).click();
  await expect.poll(() => tokenNamed(b, "Orc")).not.toBeNull();
  const placed = await tokenNamed(a, "Orc");
  if (!placed) throw new Error("A does not see the Orc");

  const from = await a.evaluate(([x, y]) => window.chartroom?.toPage(x, y), [placed.pose.x, placed.pose.y] as const);
  if (!from) throw new Error("no table handle");
  await a.mouse.move(from.x, from.y);
  await a.mouse.down();
  await a.mouse.move(from.x + 120, from.y + 60, { steps: 6 });
  await a.mouse.up();

  await expect.poll(async () => (await tokenNamed(b, "Orc"))?.pose.x).toBeGreaterThan(placed.pose.x + 1);
  const moved = await tokenNamed(b, "Orc");
  await expect.poll(() => tokenNamed(a, "Orc")).toEqual(moved); // either may hear first

  // Player B rolls 2d6+3; both see the same dice and total in the log.
  await b.getByLabel("Dice", { exact: true }).fill("2d6+3");
  await b.getByRole("button", { name: "Roll" }).click();
  await expect(a.locator(".dice-log li")).toHaveCount(1);
  await expect(b.locator(".dice-log li")).toHaveCount(1);
  const logA = await a.locator(".dice-log").innerText();
  expect(logA).toMatch(/Bob[\s\S]*2d6\+3[\s\S]*2d6 \[\d, \d\] \+ 3 = \d+/);
  expect(await b.locator(".dice-log").innerText()).toBe(logA);

  const before = await tableOf(a);
  expect(await tableOf(b)).toEqual(before);

  // The server crashes and restarts. With no snapshot written, the table
  // is rebuilt from the event log alone. Both players reconnect by
  // themselves and see the same table and dice log.
  await server.crash();
  await expect(a.locator(".status")).toContainText("Reconnecting");
  await server.start();
  for (const p of [a, b]) {
    await expect(p.locator(".status")).toHaveText("Connected", { timeout: 15_000 });
    await expect.poll(() => tableOf(p)).toEqual(before);
    expect(await p.locator(".dice-log").innerText()).toBe(logA);
  }
  await expect(a.locator(".toolbar .who")).toHaveText("Alice (GM)");
  await expect(b.locator(".toolbar .who")).toHaveText("Bob (player)");
  expect(pageErrors).toEqual([]);
});
