// The sidebar: the dice box and shared dice log, who is here, and the map
// and token tools. Every action is an intent; what it changes appears when
// the server's event arrives, like everything else.

import type { Point } from "./camera";
import type { Outcome } from "./connection";
import { rollHeading, rollText } from "./dicelog";
import { h } from "./dom";
import type { IntentData, PresenceSeat, State } from "./protocol";

export interface PanelOptions {
  /** Sends an intent; a rejection's message has already been shown. */
  act<K extends keyof IntentData>(kind: K, data: IntentData[K]): Promise<Outcome>;
  /** Shows a message to this person only. */
  notify(message: string): void;
  /** Where a new token goes. */
  centre(): Point;
}

export class Panel {
  readonly el: HTMLElement;
  private readonly log = h("ol", { class: "dice-log", "aria-label": "Dice log" });
  private readonly people = h("ul", { class: "people" });
  private readonly selection = h("div", { class: "selection" });
  private state: State | null = null;
  private presence: PresenceSeat[] = [];
  private shownRolls = 0;
  private selected: string | null = null;

  constructor(private readonly o: PanelOptions) {
    this.el = h(
      "aside",
      { class: "panel" },
      this.diceBox(),
      this.log,
      h("h2", {}, "At the table"),
      this.people,
      h("h2", {}, "Token"),
      this.tokenTools(),
      this.selection,
      h("h2", {}, "Map"),
      this.mapTools(),
    );
    this.renderSelection();
  }

  setState(st: State): void {
    // The dice log only grows, and a reconnect's welcome holds all of it,
    // so only new rolls are added. A shorter log means a different table.
    if (this.state?.session !== st.session || st.rolls.length < this.shownRolls) {
      this.log.replaceChildren();
      this.shownRolls = 0;
    }
    this.state = st;
    for (const r of st.rolls.slice(this.shownRolls)) {
      this.log.append(
        h(
          "li",
          { "data-seq": String(r.seq) },
          h("span", { class: "who" }, rollHeading(r, st.seats)),
          h("span", { class: "expr" }, r.expr),
          h("span", { class: "dice" }, rollText(r)),
        ),
      );
    }
    if (st.rolls.length > this.shownRolls) this.log.scrollTop = this.log.scrollHeight;
    this.shownRolls = st.rolls.length;
    if (this.selected && !(this.selected in st.tokens)) this.selected = null;
    this.renderPeople();
    this.renderSelection();
  }

  setPresence(seats: PresenceSeat[]): void {
    this.presence = seats;
    this.renderPeople();
  }

  select(tokenId: string | null): void {
    this.selected = tokenId;
    this.renderSelection();
  }

  /** Removes the selected token, if any. */
  removeSelected(): void {
    if (this.selected) void this.o.act("token.remove", { token_id: this.selected });
  }

  private diceBox(): HTMLElement {
    const expr = h("input", { name: "expr", placeholder: "2d6+3", "aria-label": "Dice", autocomplete: "off", maxlength: "100" });
    const label = h("input", { name: "label", placeholder: "label (optional)", "aria-label": "Roll label", maxlength: "64" });
    const form = h("form", { class: "dice-box" }, expr, label, h("button", { type: "submit" }, "Roll"));
    form.addEventListener("submit", (e) => {
      e.preventDefault();
      if (!expr.value.trim()) return;
      const data = label.value.trim() ? { expr: expr.value, label: label.value.trim() } : { expr: expr.value };
      void this.o.act("dice.roll", data).then((out) => {
        if (out.ok) label.value = ""; // keep the expression: rolling it again is common
      });
    });
    return h("section", {}, h("h2", {}, "Dice"), form);
  }

  private tokenTools(): HTMLElement {
    const label = h("input", { name: "token_label", placeholder: "Label", "aria-label": "Token label", maxlength: "64" });
    const color = h("input", { type: "color", name: "color", value: "#c0392b", "aria-label": "Token colour" });
    const size = h("input", { type: "number", name: "size_in", value: "1", min: "0.25", max: "1000", step: "0.25", "aria-label": "Size in inches" });
    const image = h("input", { type: "file", name: "token_image", accept: "image/png,image/jpeg,image/webp", "aria-label": "Token image (optional)" });
    const form = h(
      "form",
      { class: "tools" },
      label,
      h("label", {}, "Colour ", color),
      h("label", {}, "Size (in) ", size),
      h("label", {}, "Image ", image),
      h("button", { type: "submit" }, "Place at centre"),
    );
    form.addEventListener("submit", (e) => {
      e.preventDefault();
      void (async () => {
        const file = image.files?.[0];
        const asset = file ? await this.upload(file) : "";
        if (file && !asset) return;
        const at = this.o.centre();
        const out = await this.o.act("token.place", {
          label: label.value.trim(),
          color: color.value,
          ...(asset ? { asset } : {}),
          x: round(at.x),
          y: round(at.y),
          rotation: 0,
          size_in: Number(size.value),
        });
        if (out.ok) {
          label.value = "";
          image.value = "";
        }
      })();
    });
    return form;
  }

  private mapTools(): HTMLElement {
    const file = h("input", { type: "file", name: "map_image", accept: "image/png,image/jpeg,image/webp", "aria-label": "Map image" });
    const width = h("input", { type: "number", name: "width_in", value: "24", min: "1", max: "10000", step: "any", "aria-label": "Map width in inches" });
    const height = h("input", { type: "number", name: "height_in", value: "18", min: "1", max: "10000", step: "any", "aria-label": "Map height in inches" });
    const grid = h(
      "select",
      { name: "grid", "aria-label": "Grid" },
      h("option", { value: "" }, "No grid"),
      h("option", { value: "square" }, "Square grid"),
      h("option", { value: "hex" }, "Hex grid"),
    );
    const cell = h("input", { type: "number", name: "grid_size_in", value: "1", min: "0.1", max: "1000", step: "any", "aria-label": "Grid size in inches" });
    let aspect = 0; // height / width of the chosen image

    file.addEventListener("change", () => {
      const f = file.files?.[0];
      if (!f) return;
      const img = new Image();
      img.onload = () => {
        aspect = img.naturalHeight / img.naturalWidth;
        height.value = String(round(Number(width.value) * aspect));
        URL.revokeObjectURL(img.src);
      };
      img.src = URL.createObjectURL(f);
    });
    width.addEventListener("input", () => {
      if (aspect) height.value = String(round(Number(width.value) * aspect));
    });

    const form = h(
      "form",
      { class: "tools" },
      file,
      h("label", {}, "Width (in) ", width),
      h("label", {}, "Height (in) ", height),
      h("div", { class: "grid-row" }, grid, cell, "in"),
      h("button", { type: "submit" }, "Load map"),
    );
    form.addEventListener("submit", (e) => {
      e.preventDefault();
      void (async () => {
        const f = file.files?.[0];
        if (!f) {
          this.o.notify("Choose a map image first.");
          return;
        }
        const asset = await this.upload(f);
        if (!asset) return;
        await this.o.act("map.load", {
          asset,
          width_in: Number(width.value),
          height_in: Number(height.value),
          grid: grid.value === "square" || grid.value === "hex"
            ? { kind: grid.value, size_in: Number(cell.value), origin_x: 0, origin_y: 0 }
            : null,
        });
      })();
    });
    return form;
  }

  /** Uploads an image and returns its hash, or "" after saying why not. */
  private async upload(file: File): Promise<string> {
    const res = await fetch("/api/assets", { method: "POST", body: file });
    if (!res.ok) {
      this.o.notify((await res.text()).trim() || `Upload failed (${res.status})`);
      return "";
    }
    return ((await res.json()) as { asset: string }).asset;
  }

  private renderPeople(): void {
    const seats = this.state?.seats ?? {};
    const online = new Set(this.presence.filter((p) => p.online).map((p) => p.id));
    this.people.replaceChildren(
      ...Object.values(seats)
        .sort((a, b) => Number(online.has(b.id)) - Number(online.has(a.id)) || a.display_name.localeCompare(b.display_name))
        .map((s) =>
          h(
            "li",
            { class: online.has(s.id) ? "online" : "offline" },
            s.display_name,
            s.role === "gm" ? h("span", { class: "tag" }, "GM") : null,
          ),
        ),
    );
  }

  private renderSelection(): void {
    const t = this.selected ? this.state?.tokens[this.selected] : undefined;
    if (!t) {
      this.selection.replaceChildren(h("p", { class: "hint" }, "Click a token to select it. Drag to move it."));
      return;
    }
    const remove = h("button", { type: "button" }, "Remove");
    remove.addEventListener("click", () => this.removeSelected());
    this.selection.replaceChildren(h("p", {}, `Selected: ${t.label || "unlabelled token"} `, remove, h("span", { class: "hint" }, " (or press Delete)")));
  }
}

function round(n: number): number {
  return Math.round(n * 100) / 100;
}
