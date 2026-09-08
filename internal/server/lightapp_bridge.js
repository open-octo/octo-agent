// The script the server appends to a Light App served from its own origin
// (`<slug>.apps.localhost`, lightapp_origin.go). Configuration arrives in
// `window.__octoLightApp = { ns, download }` from the inline script just
// before it; the host side of both channels is web/src/lib/laStorage.ts and
// web/src/lib/laDownload.ts. The messages keep the `__laBridge` envelope those
// modules already route, so nothing on the host had to learn a new protocol.
//
// Plain ES5 on purpose: it runs inside whatever the app is, including pages
// that predate modules, and the desktop webviews are not all evergreen.
(function () {
  if (window.parent === window) return; // opened top-level: nothing to bridge
  var cfg = window.__octoLightApp || {};
  var NS = String(cfg.ns || '');
  function post(msg) {
    try { window.parent.postMessage(msg, '*'); } catch (e) {}
  }

  // ── One-time storage migration ────────────────────────────────────────────
  //
  // Before the app had an origin of its own, its localStorage lived in the
  // host page's IndexedDB behind a shim. Now the real localStorage is the
  // store; the host still holds the old data and sends it once, on request,
  // for keys the app has not written since. The app booted against empty
  // storage, so when anything was written it is reloaded to start from the
  // migrated state — the host marks the namespace migrated, so the reload's
  // own request gets nothing back.
  if (window.addEventListener) {
    window.addEventListener('message', function (ev) {
      var d = ev.data;
      if (!d || d.__laBridge !== 1 || !d.res || d.op !== 'migrate' || ev.source !== window.parent) return;
      var entries = d.value || {}, written = 0;
      try {
        for (var k in entries) {
          if (!Object.prototype.hasOwnProperty.call(entries, k)) continue;
          if (localStorage.getItem(k) === null) { localStorage.setItem(k, String(entries[k])); written++; }
        }
      } catch (e) {}
      post({ __laBridge: 1, id: 0, ns: NS, op: 'migrated', count: written });
      if (written > 0 && window.location && window.location.reload) window.location.reload();
    });
    post({ __laBridge: 1, id: 0, ns: NS, op: 'migrate-ready' });
  }

  // ── Download bridge (desktop shell only) ──────────────────────────────────
  //
  // The desktop webview has no download delegate, so an `<a download>` click
  // does nothing there even from a real origin. The textbook idiom is
  // intercepted and the bytes handed to the host, which opens the OS save
  // dialog (deliverLaDownload). In a browser the origin's own downloads work
  // and this half is not injected.
  if (!cfg.download) return;
  function send(name, blob) {
    try { window.parent.postMessage({ __laBridge: 1, id: 0, ns: NS, op: 'download', name: name, blob: blob }, '*'); }
    catch (e) { console.warn('[octo] download bridge failed', e); }
  }
  // Resolve the anchor's target to bytes and hand them to the host. Returns
  // false when the anchor has nothing to download, so the caller leaves the
  // event alone.
  function save(a) {
    if (!a.getAttribute('href')) return false;
    var name = a.getAttribute('download') || '';
    fetch(a.href).then(function (r) { return r.blob(); })
      .then(function (b) { send(name, b); })
      .catch(function (e) { console.warn('[octo] download failed', e); });
    return true;
  }
  document.addEventListener('click', function (ev) {
    if (ev.defaultPrevented) return;
    // composedPath so an anchor inside a shadow root is found; target alone
    // is retargeted to the shadow host.
    var t = ev.composedPath ? ev.composedPath()[0] : ev.target;
    var a = t && t.closest ? t.closest('a[download]') : null;
    if (a && save(a)) ev.preventDefault();
  });
  var origClick = HTMLAnchorElement.prototype.click;
  HTMLAnchorElement.prototype.click = function () {
    if (!this.isConnected && this.hasAttribute('download') && save(this)) return;
    return origClick.apply(this, arguments);
  };
})();
