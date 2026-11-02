// The lobby: every session on this server, and a form to start one.

import { h } from "./dom";

interface SessionSummary {
  id: string;
  name: string;
  link: string;
}

export async function showLobby(root: HTMLElement): Promise<void> {
  document.title = "Chartroom";
  const list = h("ul", { class: "sessions" });
  const error = h("p", { class: "error", role: "alert" });
  const name = h("input", { name: "name", placeholder: "Friday game", required: "", maxlength: "100", "aria-label": "Session name" });

  const form = h("form", { class: "create" }, name, h("button", { type: "submit" }, "Start a session"));
  form.addEventListener("submit", (e) => {
    e.preventDefault();
    void create(name.value, error);
  });

  root.replaceChildren(
    h("main", { class: "lobby" }, h("h1", {}, "Chartroom"), form, error, h("h2", {}, "Sessions"), list),
  );

  try {
    const res = await fetch("/api/sessions");
    if (!res.ok) throw new Error(`${res.status} ${await res.text()}`);
    const sessions = (await res.json()) as SessionSummary[];
    if (sessions.length === 0) list.append(h("li", { class: "empty" }, "No sessions yet."));
    for (const s of sessions) list.append(h("li", {}, h("a", { href: s.link }, s.name)));
  } catch (err) {
    error.textContent = `Could not list sessions: ${String(err)}`;
  }
}

async function create(name: string, error: HTMLElement): Promise<void> {
  error.textContent = "";
  const res = await fetch("/api/sessions", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name }),
  });
  if (!res.ok) {
    error.textContent = (await res.text()).trim();
    return;
  }
  const s = (await res.json()) as SessionSummary;
  location.assign(s.link);
}
