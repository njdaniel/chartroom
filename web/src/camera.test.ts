import { describe, expect, it } from "vitest";
import { MAX_SCALE, MIN_SCALE, fit, pan, toScreen, toWorld, zoomAt } from "./camera";
import { distanceIn, formatInches } from "./ruler";

describe("camera", () => {
  const c = { x: 100, y: 50, scale: 10 };

  it("converts between screen and world", () => {
    expect(toWorld(c, { x: 150, y: 70 })).toEqual({ x: 5, y: 2 });
    expect(toScreen(c, { x: 5, y: 2 })).toEqual({ x: 150, y: 70 });
  });

  it("zooms around the cursor", () => {
    const p = { x: 300, y: 200 };
    const z = zoomAt(c, p, 2);
    expect(z.scale).toBe(20);
    const before = toWorld(c, p);
    const after = toWorld(z, p);
    expect(after.x).toBeCloseTo(before.x);
    expect(after.y).toBeCloseTo(before.y);
  });

  it("clamps zoom", () => {
    expect(zoomAt(c, { x: 0, y: 0 }, 1e6).scale).toBe(MAX_SCALE);
    expect(zoomAt(c, { x: 0, y: 0 }, 1e-6).scale).toBe(MIN_SCALE);
  });

  it("pans", () => {
    expect(pan(c, 5, -5)).toEqual({ x: 105, y: 45, scale: 10 });
  });

  it("fits a map in the view, centred", () => {
    const f = fit(48, 36, { width: 1000, height: 1000 });
    expect(f.scale).toBeCloseTo(900 / 48);
    const centre = toWorld(f, { x: 500, y: 500 });
    expect(centre.x).toBeCloseTo(24);
    expect(centre.y).toBeCloseTo(18);
  });
});

describe("ruler", () => {
  it("measures straight-line inches", () => {
    expect(distanceIn({ x: 0, y: 0 }, { x: 3, y: 4 })).toBe(5);
    expect(formatInches(distanceIn({ x: 1, y: 1 }, { x: 1, y: 13.26 }))).toBe("12.3 in");
    expect(formatInches(0)).toBe("0.0 in");
  });
});
