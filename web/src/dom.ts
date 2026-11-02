// A small helper for building DOM without a framework (ADR-001).

type Child = Node | string | null | undefined | false;

export function h<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  attrs: Record<string, string> = {},
  ...children: Child[]
): HTMLElementTagNameMap[K] {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v);
  for (const c of children) {
    if (c) el.append(c);
  }
  return el;
}

/** The display name this browser uses for new seats. */
export const nameKey = "chartroom.name";

export function storage(): Storage | null {
  try {
    return window.localStorage;
  } catch {
    return null; // blocked; seats then last only as long as the tab
  }
}
