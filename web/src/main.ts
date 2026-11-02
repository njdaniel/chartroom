// Routes: / is the lobby, /s/<id> is a session's table.
import "./style.css";

import { showLobby } from "./lobby";
import { showTable } from "./page";

const app = document.getElementById("app");
if (!app) throw new Error("no #app element");

const match = /^\/s\/([^/]+)$/.exec(location.pathname);
if (match?.[1]) {
  await showTable(app, decodeURIComponent(match[1]));
} else {
  await showLobby(app);
}
