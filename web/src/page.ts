// A session's page: ask for a display name if this browser has none, then
// connect and show the table.

import { Connection, type KeyValue, type Status } from "./connection";
import { h, nameKey, storage } from "./dom";
import { Panel } from "./panel";
import type { IntentData, Seat, State } from "./protocol";
import { TableView } from "./table";

export async function showTable(root: HTMLElement, session: string): Promise<void> {
  const store: KeyValue = storage() ?? memoryStore();
  const displayName = store.getItem(nameKey) ?? (await askName(root));
  store.setItem(nameKey, displayName);

  const status = h("span", { class: "status" }, "Connecting…");
  const who = h("span", { class: "who" });
  const title = h("strong", {}, "");
  const rulerButton = h("button", { type: "button", "aria-pressed": "false", title: "Measure (or hold Shift and drag)" }, "Ruler");
  const fitButton = h("button", { type: "button", title: "Fit the map to the window" }, "Fit");
  const stage = h("div", { class: "stage" });
  const notice = h("div", { class: "notice", role: "status" });
  const sidebar = h("aside", { class: "panel" });

  root.replaceChildren(
    h(
      "div",
      { class: "table-page" },
      h("header", { class: "toolbar" }, h("a", { href: "/" }, "Lobby"), title, rulerButton, fitButton, who, status),
      stage,
      sidebar,
      notice,
    ),
  );

  // Every intent goes through here, so a rejection always reaches the
  // person who sent it.
  const act = async <K extends keyof IntentData>(kind: K, data: IntentData[K]) => {
    const outcome = await conn.send(kind, data);
    if (!outcome.ok) show(notice, outcome.rejected.message);
    return outcome;
  };

  const view = new TableView({
    move: (tokenId, to) => act("token.move", { token_id: tokenId, x: to.x, y: to.y, rotation: to.rotation }),
    select: (tokenId) => {
      view.setSelected(tokenId);
      panel.select(tokenId);
    },
  });
  const panel = new Panel({ act, notify: (m) => show(notice, m), centre: () => view.viewCentre() });
  sidebar.replaceWith(panel.el);
  await view.mount(stage);

  document.addEventListener("keydown", (e) => {
    const typing = e.target instanceof HTMLInputElement || e.target instanceof HTMLSelectElement || e.target instanceof HTMLTextAreaElement;
    if (!typing && (e.key === "Delete" || e.key === "Backspace")) panel.removeSelected();
  });

  rulerButton.addEventListener("click", () => {
    view.rulerMode = !view.rulerMode;
    rulerButton.setAttribute("aria-pressed", String(view.rulerMode));
  });
  fitButton.addEventListener("click", () => view.recenter());

  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  const conn = new Connection({
    url: `${proto}//${location.host}/ws`,
    session,
    displayName,
    storage: store,
    handlers: {
      state: (st, seat) => {
        document.title = `${st.name} · Chartroom`;
        title.textContent = st.name;
        who.textContent = `${seat.display_name} (${seat.role === "gm" ? "GM" : "player"})`;
        view.setState(st);
        panel.setState(st);
      },
      presence: (seats) => panel.setPresence(seats),
      status: (s) => {
        status.textContent = statusText(s);
        status.dataset["kind"] = s.kind;
      },
    },
  });
  conn.start();

  // A read-only handle for the end-to-end tests (e2e/), and for poking at
  // the table from the browser console. It exposes nothing the page does
  // not already have.
  (window as unknown as { chartroom: ChartroomHandle }).chartroom = {
    state: () => conn.state,
    seat: () => conn.seat,
    toPage: (x, y) => view.toPage({ x, y }),
  };
}

/** window.chartroom: see showTable. */
export interface ChartroomHandle {
  state(): State | null;
  seat(): Seat | null;
  toPage(x: number, y: number): { x: number; y: number };
}

function statusText(s: Status): string {
  switch (s.kind) {
    case "connecting":
      return "Connecting…";
    case "open":
      return "Connected";
    case "retrying":
      return `Reconnecting in ${Math.ceil(s.inMs / 1000)} s…`;
    case "failed":
      return s.message;
  }
}

let noticeTimer: ReturnType<typeof setTimeout> | undefined;

function show(el: HTMLElement, message: string): void {
  el.textContent = message;
  el.classList.add("shown");
  clearTimeout(noticeTimer);
  noticeTimer = setTimeout(() => el.classList.remove("shown"), 4000);
}

function askName(root: HTMLElement): Promise<string> {
  return new Promise((resolve) => {
    const input = h("input", { name: "display_name", required: "", maxlength: "40", "aria-label": "Your name", autofocus: "" });
    const form = h("form", { class: "create" }, input, h("button", { type: "submit" }, "Join"));
    form.addEventListener("submit", (e) => {
      e.preventDefault();
      const name = input.value.trim();
      if (name) resolve(name);
    });
    root.replaceChildren(h("main", { class: "lobby" }, h("h1", {}, "What should the table call you?"), form));
    input.focus();
  });
}

function memoryStore(): KeyValue {
  const m = new Map<string, string>();
  return {
    getItem: (k) => m.get(k) ?? null,
    setItem: (k, v) => void m.set(k, v),
    removeItem: (k) => void m.delete(k),
  };
}
