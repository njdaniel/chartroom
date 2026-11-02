// The ruler is drawn only in your own browser (ADR-003): it sends nothing to
// the server. Server-side measurement arrives with the spatial engine.

import type { Point } from "./camera";

/** Straight-line distance between two world points, in inches. */
export function distanceIn(a: Point, b: Point): number {
  return Math.hypot(b.x - a.x, b.y - a.y);
}

/** Formats inches to one decimal place, e.g. "12.5 in". */
export function formatInches(d: number): string {
  return `${(Math.round(d * 10) / 10).toFixed(1)} in`;
}
