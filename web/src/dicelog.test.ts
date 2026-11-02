import { describe, expect, it } from "vitest";
import { rollHeading, rollText } from "./dicelog";
import type { Roll } from "./protocol";

const roll = (r: Partial<Roll>): Roll => ({ seq: 3, seat: "seat_1", expr: "", terms: [], modifier: 0, total: 0, ...r });

describe("dice log", () => {
  it("shows every die, the modifier, and the total", () => {
    expect(
      rollText(roll({ terms: [{ count: 2, sides: 6, results: [4, 1] }, { count: 1, sides: 4, results: [2] }], modifier: -1, total: 6 })),
    ).toBe("2d6 [4, 1] + 1d4 [2] − 1 = 6");
    expect(rollText(roll({ terms: [{ count: 2, sides: 6, results: [3, 5] }], modifier: 3, total: 11 }))).toBe("2d6 [3, 5] + 3 = 11");
    expect(rollText(roll({ terms: [{ count: 1, sides: 20, results: [17] }], total: 17 }))).toBe("1d20 [17] = 17");
  });

  it("says who rolled", () => {
    const names = { seat_1: { display_name: "Nick" } };
    expect(rollHeading(roll({ label: "attack" }), names)).toBe("Nick · attack");
    expect(rollHeading(roll({}), names)).toBe("Nick");
    expect(rollHeading(roll({ seat: "seat_gone" }), names)).toBe("Someone");
  });
});
