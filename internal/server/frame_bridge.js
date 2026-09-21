// The script the server appends to a page served from its own sandboxed
// origin — a Light App (`<slug>.apps.localhost`, lightapp_origin.go) or a
// session artifact (`<token>.artifacts.localhost`, artifact_origin.go).
// Configuration arrives in `window.__octoBridge = { kind, ns, download? }`
// from the inline script just before it, and the kind selects which halves
// boot:
//
//   lightapp  one-time storage migration, interaction (until the light-app
//             mirror's retirement completes), and — in the desktop shell
//             only, whose webview cannot download — the download bridge.
//   artifact  interaction only: the page describes itself to the model and
//             can be handed a file back (the artifact_state / view_artifact /
//             insert_into_artifact tools). An artifact has no storage
//             contract with the host and nothing to migrate.
//
// The host side is web/src/lib/laStorage.ts (registry + router), laState.ts
// (relay), laDelivery.ts and laDownload.ts. Messages keep the `__laBridge`
// envelope those modules already route — the name predates artifacts, and
// nothing on the host had to learn a new protocol.
//
// Plain ES5 on purpose: it runs inside whatever the page is, including pages
// that predate modules, and the desktop webviews are not all evergreen.
(function () {
  if (window.parent === window) return; // opened top-level: nothing to bridge
  var cfg = window.__octoBridge || {};
  var NS = String(cfg.ns || '');
  var KIND = cfg.kind === 'artifact' ? 'artifact' : 'lightapp';
  function post(msg) {
    try { window.parent.postMessage(msg, '*'); } catch (e) {}
  }

  // ── Interaction: state push (page → host) and delivery (host → page) ─────
  //
  // Lets a page describe itself to the model: the host mirrors the snapshot,
  // where the state/view tools read it. There is no reply and no read path —
  // the page never learns anything about the conversation from this.
  //
  //   octo.pushState({ digest: 'one line for the model',
  //                    summary: { any: 'json' },
  //                    image: blobOrCanvas })
  //
  // A canvas is accepted directly and converted; the host coalesces pushes, so
  // calling this on every change is fine.
  //
  // The other direction carries a file the agent produced — a generated image
  // to drop onto a canvas. Register a callback to receive it; ignore it and
  // nothing happens. It never carries anything about the conversation.
  //
  //   octo.onDelivery(function (d) { d.blob; d.name; d.note })
  function bootInteraction() {
    window.octo = window.octo || {};
    var deliveryHandlers = [];
    window.octo.onDelivery = function (fn) {
      if (typeof fn === 'function') deliveryHandlers.push(fn);
    };
    if (window.addEventListener) {
      window.addEventListener('message', function (ev) {
        var d = ev.data;
        if (!d || d.__laBridge !== 1 || d.op !== 'delivery' || ev.source !== window.parent) return;
        for (var i = 0; i < deliveryHandlers.length; i++) {
          try { deliveryHandlers[i]({ blob: d.blob, name: d.name, note: d.note }); }
          catch (e) { console.warn('[octo] delivery handler failed', e); }
        }
      });
    }

    window.octo.pushState = function (state) {
      var s = state || {};
      function send(blob) {
        post({
          __laBridge: 1, id: 0, ns: NS, op: 'state',
          digest: typeof s.digest === 'string' ? s.digest : '',
          summary: s.summary,
          image: blob || null,
        });
      }
      var img = s.image;
      if (img && typeof img.toBlob === 'function') {
        try { img.toBlob(function (b) { send(b); }, 'image/png'); return; } catch (e) {}
      }
      send(img && typeof Blob !== 'undefined' && img instanceof Blob ? img : null);
    };
  }

  // ── One-time storage migration (Light Apps only) ──────────────────────────
  //
  // Before the app had an origin of its own, its localStorage lived in the
  // host page's IndexedDB behind a shim. Now the real localStorage is the
  // store; the host still holds the old data and sends it once, on request,
  // for keys the app has not written since. The app booted against empty
  // storage, so when anything was written it is reloaded to start from the
  // migrated state — the host marks the namespace migrated, so the reload's
  // own request gets nothing back.
  function bootStorageMigration() {
    if (!window.addEventListener) return;
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

  // ── Download bridge (Light Apps in the desktop shell only) ────────────────
  //
  // The desktop webview has no download delegate, so an `<a download>` click
  // does nothing there even from a real origin. The textbook idiom is
  // intercepted and the bytes handed to the host, which opens the OS save
  // dialog (deliverLaDownload). In a browser the origin's own downloads work
  // and this half is not injected.
  function bootDownloadBridge() {
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
  }

  if (KIND === 'lightapp') {
    bootStorageMigration();
    bootDownloadBridge();
  }
  bootInteraction();
})();
