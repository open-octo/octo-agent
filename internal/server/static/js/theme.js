// ── Theme — light/dark theme toggle ──────────────────────────────────────
//
// Reads/writes localStorage("octo-theme"). Detects system preference
// on first visit. The inline script in index.html sets the initial
// data-theme attribute before CSS renders to prevent flash.
// ─────────────────────────────────────────────────────────────────────────

const Theme = (() => {
  const KEY = "octo-theme";

  function init() {
    // Listen for system preference changes.
    window.matchMedia("(prefers-color-scheme: light)").addEventListener("change", (e) => {
      // Only auto-switch if user hasn't explicitly chosen.
      if (!localStorage.getItem(KEY)) {
        apply(e.matches ? "light" : "dark");
      }
    });
  }

  function get() {
    return document.documentElement.getAttribute("data-theme") || "dark";
  }

  function apply(theme) {
    document.documentElement.setAttribute("data-theme", theme);
  }

  function set(theme) {
    localStorage.setItem(KEY, theme);
    apply(theme);
  }

  function toggle() {
    set(get() === "light" ? "dark" : "light");
  }

  return { init, get, set, toggle };
})();

Theme.init();
