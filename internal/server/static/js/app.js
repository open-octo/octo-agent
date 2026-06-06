// ── app.js — Main entry point ──────────────────────────────────────────────
//
// Coordinates WS, Sessions, Tasks, Skills and Settings modules.
// Handles WS event dispatch and wires up all DOM event listeners.
//
// Load order (in index.html):
//   auth.js → ws.js → ws-dispatcher.js → app.js
// ─────────────────────────────────────────────────────────────────────────

// ── DOM helper (shared by all modules loaded after this) ──────────────────
const $ = id => document.getElementById(id);

// ── Utilities (shared) ────────────────────────────────────────────────────
function escapeHtml(str) {
  return String(str)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
}

// ── Markdown rendering (uses marked.js if available) ─────────────────────
function renderMarkdown(text) {
  if (typeof marked !== "undefined" && marked.parse) {
    return marked.parse(text);
  }
  // Fallback: simple HTML-escaping + newlines.
  return escapeHtml(text).replace(/\n/g, "<br>");
}

// ── Router ────────────────────────────────────────────────────────────────
//
// Single source of truth for panel visibility and URL hash.
//
// Views:
//   welcome            → /#
//   session/{id}       → /#session/{id}
//   tasks              → /#tasks
//   skills             → /#skills
//   channels           → /#channels
//   trash              → /#trash
//   profile            → /#profile
//   settings           → /#settings
//
// Usage:
//   Router.navigate("session", { id: "abc123" })
//   Router.navigate("tasks")
//   Router.navigate("welcome")
//
// All panels must be listed in PANELS so they are hidden before the active
// one is shown. Modules must NOT touch panel display styles directly.
// ─────────────────────────────────────────────────────────────────────────
const PANELS = [
  "setup-panel",
  "onboard-panel",
  "welcome",
  "chat-panel",
  "task-detail-panel",
  "skills-panel",
  "channels-panel",
  "trash-panel",
  "profile-panel",
  "settings-panel",
];

const Router = (() => {
  let _activePanel = null;

  function hideAll() {
    PANELS.forEach(id => {
      const el = $(id);
      if (el) el.style.display = "none";
    });
  }

  function navigate(view, params) {
    hideAll();

    switch (view) {
      case "welcome":
        const welcome = $("welcome");
        if (welcome) welcome.style.display = "";
        window.location.hash = "";
        _activePanel = "welcome";
        break;

      case "session":
        const chatPanel = $("chat-panel");
        if (chatPanel) chatPanel.style.display = "";
        window.location.hash = `#session/${params?.id || ""}`;
        _activePanel = "chat-panel";
        if (params?.id && typeof Sessions !== "undefined") {
          Sessions.select(params.id);
        }
        break;

      case "tasks":
        const tasksPanel = $("task-detail-panel");
        if (tasksPanel) tasksPanel.style.display = "";
        window.location.hash = "#tasks";
        _activePanel = "task-detail-panel";
        break;

      case "skills":
        const skillsPanel = $("skills-panel");
        if (skillsPanel) skillsPanel.style.display = "";
        window.location.hash = "#skills";
        _activePanel = "skills-panel";
        break;

      case "channels":
        const channelsPanel = $("channels-panel");
        if (channelsPanel) channelsPanel.style.display = "";
        window.location.hash = "#channels";
        _activePanel = "channels-panel";
        break;

      case "trash":
        const trashPanel = $("trash-panel");
        if (trashPanel) trashPanel.style.display = "";
        window.location.hash = "#trash";
        _activePanel = "trash-panel";
        break;

      case "profile":
        const profilePanel = $("profile-panel");
        if (profilePanel) profilePanel.style.display = "";
        window.location.hash = "#profile";
        _activePanel = "profile-panel";
        break;

      case "settings":
        const settingsPanel = $("settings-panel");
        if (settingsPanel) settingsPanel.style.display = "";
        window.location.hash = "#settings";
        _activePanel = "settings-panel";
        break;

      default:
        break;
    }
  }

  function getActivePanel() {
    return _activePanel;
  }

  return { navigate, hideAll, getActivePanel };
})();

// ── Boot sequence ────────────────────────────────────────────────────────
async function bootUI() {
  const app = $("app");
  if (app) app.style.visibility = "visible";

  // Hide offline banner once we're rendering.
  const banner = $("offline-banner");
  if (banner) banner.style.display = "none";

  // Show welcome screen.
  Router.navigate("welcome");

  // Initialize all feature modules.
  if (typeof Sessions !== "undefined" && Sessions.init) {
    Sessions.init();
  }
  if (typeof Skills !== "undefined" && Skills.init) {
    Skills.init();
  }
  if (typeof Tasks !== "undefined" && Tasks.init) {
    Tasks.init();
  }
  if (typeof Channels !== "undefined" && Channels.init) {
    Channels.init();
  }
  if (typeof Settings !== "undefined" && Settings.init) {
    Settings.init();
  }
}

// ── Main IIFE ────────────────────────────────────────────────────────────
(async function main() {
  // Auth check must happen first — WS connect depends on the access key.
  const authOk = await Auth.check();
  if (!authOk) {
    // User cancelled auth prompt — stop boot.
    return;
  }

  // Connect WebSocket.
  WS.connect();

  await bootUI();
})();
