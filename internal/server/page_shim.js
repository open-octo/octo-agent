// The script the server splices into every artifact and Light App page, ahead
// of the page's own scripts (artifact_pages.go). Configuration arrives in
// `window.__octoPage = { ns, download }` from the inline script just before
// it.
//
// Plain ES5 plus Proxy on purpose: it runs inside whatever the page is, and
// the desktop webviews are not all evergreen.
(function () {
  var cfg = window.__octoPage || {};
  var NS = String(cfg.ns || '');

  // ── localStorage namespace ────────────────────────────────────────────────
  //
  // The page shares its origin — and so its localStorage — with the octo UI.
  // Every key it touches is moved under `octo.page.<ns>:`, so a page calling
  // clear() or picking a common key name cannot wipe the UI's settings or
  // another page's data. The store underneath is the real one: synchronous,
  // persistent, the browser's own quota.
  var real;
  try { real = window.localStorage; } catch (e) { real = null; }
  if (real) {
    var P = 'octo.page.' + NS + ':';
    var hasOwn = Object.prototype.hasOwnProperty;
    var own = function () {
      var out = [];
      for (var i = 0; i < real.length; i++) {
        var k = real.key(i);
        if (k !== null && k.indexOf(P) === 0) out.push(k.slice(P.length));
      }
      return out;
    };
    var api = {
      getItem: function (k) { return real.getItem(P + String(k)); },
      setItem: function (k, v) { real.setItem(P + String(k), String(v)); },
      removeItem: function (k) { real.removeItem(P + String(k)); },
      clear: function () { var ks = own(); for (var i = 0; i < ks.length; i++) real.removeItem(P + ks[i]); },
      key: function (i) { var ks = own(); return i >= 0 && i < ks.length ? ks[i] : null; }
    };
    // Property access (`localStorage.foo = 'x'`, `Object.keys(localStorage)`,
    // `'foo' in localStorage`) behaves as on a real Storage, under the prefix.
    var wrapped = new Proxy({}, {
      get: function (t, k) {
        if (k === 'length') return own().length;
        if (typeof k !== 'string') return undefined;
        if (hasOwn.call(api, k)) return api[k];
        var v = real.getItem(P + k);
        return v === null ? undefined : v;
      },
      set: function (t, k, v) { if (typeof k === 'string') real.setItem(P + k, String(v)); return true; },
      deleteProperty: function (t, k) { if (typeof k === 'string') real.removeItem(P + k); return true; },
      has: function (t, k) { return typeof k === 'string' && (k === 'length' || hasOwn.call(api, k) || real.getItem(P + k) !== null); },
      ownKeys: function () { return own(); },
      getOwnPropertyDescriptor: function (t, k) {
        if (typeof k !== 'string') return undefined;
        var v = real.getItem(P + k);
        return v === null ? undefined : { value: v, writable: true, enumerable: true, configurable: true };
      }
    });
    try {
      Object.defineProperty(window, 'localStorage', { configurable: true, enumerable: true, get: function () { return wrapped; } });
    } catch (e) {}
  }

  // ── Download bridge (Light Apps in the desktop shell only) ────────────────
  //
  // The desktop webview has no download delegate, so an `<a download>` click
  // does nothing there. The textbook idiom is intercepted and the bytes handed
  // to the host, which opens the OS save dialog (deliverLaDownload in
  // web/src/lib/laDownload.ts). In a browser the page downloads by itself and
  // this half stays off.
  if (!cfg.download || window.parent === window) return;
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
