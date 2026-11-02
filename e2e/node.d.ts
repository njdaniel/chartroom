// The few Node APIs the end-to-end test uses. Declared here rather than
// adding @types/node, which is not an approved dependency.

declare module "node:child_process" {
  interface ChildProcess {
    readonly pid?: number;
    readonly exitCode: number | null;
    kill(signal?: "SIGTERM" | "SIGKILL"): boolean;
    once(event: "exit", fn: (code: number | null) => void): this;
    stdout: { on(event: "data", fn: (chunk: { toString(): string }) => void): void } | null;
    stderr: { on(event: "data", fn: (chunk: { toString(): string }) => void): void } | null;
  }
  export function spawn(command: string, args: string[], options?: { stdio?: "pipe" | "inherit" }): ChildProcess;
  export type { ChildProcess };
}

declare module "node:fs" {
  export function mkdtempSync(prefix: string): string;
  export function rmSync(path: string, options: { recursive: boolean; force: boolean }): void;
  export function existsSync(path: string): boolean;
}

declare module "node:os" {
  export function tmpdir(): string;
}

declare module "node:path" {
  export function join(...parts: string[]): string;
  export function resolve(...parts: string[]): string;
}

declare module "node:net" {
  interface Server {
    listen(port: number, host: string, fn: () => void): void;
    address(): { port: number } | string | null;
    close(fn: () => void): void;
  }
  export function createServer(): Server;
}

declare const process: { cwd(): string; env: Record<string, string | undefined> };
