// ─────────────────────────────────────────────────────────────────────────
// Artifacts — right-side panel collecting previewable files the agent wrote
//
// Design: dev-docs/web-artifacts-panel-design.md.
//
// Detection rides the existing ui_payload stream: sessions.js calls
// Artifacts.observe(uiPayload, {live}) from both the live tool_result path
// and history replay, and Artifacts.reset(sessionId) on session switch.
// Content is fetched from GET /api/sessions/{id}/artifacts?path=… which only
// serves paths this session's transcript actually wrote.
//
// Security: artifact HTML is model-authored and therefore untrusted. It is
// ONLY rendered inside <iframe sandbox="allow-scripts"> via srcdoc — never
// with allow-same-origin, so scripts run in an opaque origin with no reach
// into the app (no cookies, no localStorage, no session API).
// ─────────────────────────────────────────────────────────────────────────
const Artifacts = (() => {
  const EXT_KIND = {
    html: "html", htm: "html",
    md: "markdown", markdown: "markdown",
    png: "image", jpg: "image", jpeg: "image", gif: "image", svg: "image", webp: "image",
  };

  let _items = new Map(); // path -> { path, name, kind, time }
  let _sessionId = null;
  let _selectedPath = null;
  let _autoOpened = false; // auto-open at most once per session
  let _objectURL = null;   // revoke previous image blob URL on swap

  function _kindOf(path) {
    const dot = path.lastIndexOf(".");
    if (dot < 0) return null;
    return EXT_KIND[path.slice(dot + 1).toLowerCase()] || null;
  }

  function _basename(path) {
    return path.split(/[\\/]/).pop();
  }

  /** Ingest one tool ui_payload. opts.live distinguishes live turns from
   *  history replay (only live arrivals auto-open the panel). */
  function observe(uiPayload, opts) {
    if (!uiPayload || (uiPayload.type !== "write" && uiPayload.type !== "edit")) return;
    const path = uiPayload.path;
    if (!path) return;
    const kind = _kindOf(path);
    if (!kind) return;

    _items.delete(path); // re-insert so iteration order tracks last write
    _items.set(path, { path, name: _basename(path), kind, time: Date.now() });
    _renderList();

    const live = !!(opts && opts.live);
    if (live && !_autoOpened) {
      _autoOpened = true;
      open();
    }
    // A rewrite of the previewed file refreshes the preview in place.
    if (live && _selectedPath === path && _isOpen()) _preview(path);
  }

  /** Clear state for a session switch; history replay repopulates. */
  function reset(sessionId) {
    _items = new Map();
    _sessionId = sessionId;
    _selectedPath = null;
    _autoOpened = false;
    _clearPreview();
    _renderList();
    close();
  }

  function _isOpen() {
    const p = $("artifacts-panel");
    return p && !p.hidden;
  }

  function open() {
    const p = $("artifacts-panel");
    if (!p || _items.size === 0) return;
    p.hidden = false;
    _updatePill();
    // Default-select the newest artifact so the panel never opens empty.
    if (!_selectedPath) {
      const newest = Array.from(_items.keys()).pop();
      if (newest) _select(newest);
    }
  }

  function close() {
    const p = $("artifacts-panel");
    if (p) p.hidden = true;
    _updatePill();
  }

  function _updatePill() {
    const pill = $("artifacts-pill");
    if (!pill) return;
    const show = _items.size > 0 && !_isOpen();
    pill.hidden = !show;
    if (show) $("artifacts-pill-count").textContent = String(_items.size);
  }

  function _renderList() {
    const list = $("artifacts-list");
    if (!list) return;
    list.innerHTML = "";
    // Newest first.
    const items = Array.from(_items.values()).reverse();
    for (const it of items) {
      const row = document.createElement("div");
      row.className = "artifact-row" + (it.path === _selectedPath ? " selected" : "");
      row.title = it.path;

      const icon = document.createElement("span");
      icon.className = "artifact-icon";
      icon.textContent = it.kind === "html" ? "🌐" : it.kind === "image" ? "🖼" : "📄";

      const name = document.createElement("span");
      name.className = "artifact-name";
      name.textContent = it.name;

      const dl = document.createElement("button");
      dl.className = "artifact-action";
      dl.title = I18n.t("artifacts.download");
      dl.textContent = "⬇";
      dl.addEventListener("click", (e) => { e.stopPropagation(); _download(it.path); });

      row.appendChild(icon);
      row.appendChild(name);
      row.appendChild(dl);
      row.addEventListener("click", () => _select(it.path));
      list.appendChild(row);
    }
    const empty = $("artifacts-empty");
    if (empty) empty.hidden = _items.size > 0;
    _updatePill();
  }

  function _select(path) {
    _selectedPath = path;
    _renderList();
    _preview(path);
  }

  function _contentURL(path) {
    return `/api/sessions/${encodeURIComponent(_sessionId)}/artifacts?path=${encodeURIComponent(path)}`;
  }

  function _clearPreview() {
    const pv = $("artifacts-preview");
    if (pv) pv.innerHTML = "";
    if (_objectURL) { URL.revokeObjectURL(_objectURL); _objectURL = null; }
  }

  async function _preview(path) {
    const pv = $("artifacts-preview");
    const it = _items.get(path);
    if (!pv || !it || !_sessionId) return;
    _clearPreview();

    try {
      const res = await fetch(_contentURL(path));
      if (!res.ok) {
        const msg = res.status === 413 ? I18n.t("artifacts.tooLarge") : `HTTP ${res.status}`;
        pv.innerHTML = `<div class="artifact-error">${msg}</div>`;
        return;
      }
      if (it.kind === "html") {
        const text = await res.text();
        const frame = document.createElement("iframe");
        frame.className = "artifact-frame";
        // Opaque origin: scripts may run, but cannot touch the app origin.
        frame.setAttribute("sandbox", "allow-scripts");
        frame.srcdoc = text;
        pv.appendChild(frame);
      } else if (it.kind === "markdown") {
        const text = await res.text();
        const div = document.createElement("div");
        div.className = "artifact-md";
        div.innerHTML = marked.parse(text, { breaks: true, gfm: true });
        pv.appendChild(div);
      } else {
        const blob = await res.blob();
        _objectURL = URL.createObjectURL(blob);
        const img = document.createElement("img");
        img.className = "artifact-img";
        img.src = _objectURL;
        pv.appendChild(img);
      }
    } catch (e) {
      console.error("[Artifacts] preview failed", e);
      pv.innerHTML = `<div class="artifact-error">${I18n.t("artifacts.loadError")}</div>`;
    }
  }

  async function _download(path) {
    try {
      const res = await fetch(_contentURL(path));
      if (!res.ok) { alert(`HTTP ${res.status}`); return; }
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = _basename(path);
      a.click();
      URL.revokeObjectURL(url);
    } catch (e) {
      console.error("[Artifacts] download failed", e);
    }
  }

  function init() {
    const closeBtn = $("btn-artifacts-close");
    if (closeBtn) closeBtn.addEventListener("click", close);
    const refreshBtn = $("btn-artifacts-refresh");
    if (refreshBtn) refreshBtn.addEventListener("click", () => { if (_selectedPath) _preview(_selectedPath); });
    const pill = $("artifacts-pill");
    if (pill) pill.addEventListener("click", open);
  }

  return { observe, reset, open, close, init };
})();

document.addEventListener("DOMContentLoaded", Artifacts.init);
