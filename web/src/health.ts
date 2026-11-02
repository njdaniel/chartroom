export type Health = "ok" | "unreachable";

// Asks chartroomd whether it is up. `fetchFn` is injectable for tests.
export async function checkHealth(fetchFn: typeof fetch = fetch): Promise<Health> {
  try {
    const resp = await fetchFn("/healthz");
    if (!resp.ok) return "unreachable";
    const body: unknown = await resp.json();
    return isOk(body) ? "ok" : "unreachable";
  } catch {
    return "unreachable";
  }
}

function isOk(body: unknown): boolean {
  return typeof body === "object" && body !== null && (body as { status?: unknown }).status === "ok";
}
