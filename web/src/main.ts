import { checkHealth } from "./health";

const el = document.getElementById("health");
if (el) {
  el.textContent = await checkHealth();
}
