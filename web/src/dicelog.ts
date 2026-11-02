// How a roll reads in the dice log: every die, the modifier, and the total,
// exactly as the server logged them.

import type { Roll } from "./protocol";

/** "2d6 [4, 1] + 1d4 [2] − 1 = 6" */
export function rollText(r: Roll): string {
  const terms = r.terms.map((t) => `${t.count}d${t.sides} [${t.results.join(", ")}]`).join(" + ");
  const mod = r.modifier > 0 ? ` + ${r.modifier}` : r.modifier < 0 ? ` − ${-r.modifier}` : "";
  return `${terms}${mod} = ${r.total}`;
}

/** Who rolled, and the label if there is one: "Nick · attack". */
export function rollHeading(r: Roll, names: Record<string, { display_name: string } | undefined>): string {
  const who = names[r.seat]?.display_name ?? "Someone";
  return r.label ? `${who} · ${r.label}` : who;
}
