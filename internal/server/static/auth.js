// auth.js — access-key authentication for non-loopback clients.
//
// The browser authenticates via the octo_access_key cookie: loopback visits
// never see a 401 (the server exempts loopback peers), so the prompt only
// appears when the server is reached over the network. A ?access_key= query
// parameter (from the URL the server prints on startup) is adopted into
// storage and stripped from the address bar; the cookie then rides every
// fetch and the WebSocket handshake.
//
const Auth = (() => {
  const COOKIE_NAME      = 'octo_access_key';
  const STORAGE_KEY      = 'octo_access_key';
  const PROBE_ENDPOINT   = '/api/sessions?limit=1';
  const MAX_PROMPT_TRIES = 3;

  let _passed       = false;
  let _checkPromise = null;

  const Cookie = {
    set(key) {
      const secure = location.protocol === 'https:' ? '; Secure' : '';
      document.cookie =
        `${COOKIE_NAME}=${encodeURIComponent(key)}; path=/; SameSite=Strict${secure}`;
    },
    clear() {
      document.cookie = `${COOKIE_NAME}=; path=/; max-age=0; SameSite=Strict`;
    },
  };

  // Adopt a bootstrap-link key, then strip it so it doesn't linger in the
  // address bar or browser history.
  function _adoptQueryKey() {
    const params = new URLSearchParams(location.search);
    const key = params.get('access_key');
    if (!key) return;
    localStorage.setItem(STORAGE_KEY, key);
    Cookie.set(key);
    params.delete('access_key');
    const qs = params.toString();
    history.replaceState(null, '',
      location.pathname + (qs ? `?${qs}` : '') + location.hash);
  }

  async function _probe() {
    try {
      const r = await fetch(PROBE_ENDPOINT);
      if (r.ok)             return 'ok';
      if (r.status === 401) return 'unauthorized';
      return 'server_error';
    } catch {
      return 'network_error';
    }
  }

  async function _askUserForKey() {
    const message = (typeof I18n !== 'undefined')
      ? I18n.t('auth.accessKeyRequired')
      : 'Access key required:';

    const el = document.getElementById('prompt-modal-input');
    if (el) el.type = 'password';
    try {
      const input = (typeof Modal !== 'undefined' && Modal.prompt)
        ? await Modal.prompt(message)
        : prompt(message);
      return input?.trim() || null;
    } finally {
      if (el) el.type = 'text';
    }
  }

  async function _check() {
    _adoptQueryKey();
    // Re-seed the cookie from localStorage in case it was cleared.
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored) Cookie.set(stored);

    let status = await _probe();
    if (status !== 'unauthorized') {
      // ok — or a server/network error, which is not an auth failure and
      // gets surfaced by whichever call hits it; don't block boot on it.
      _passed = true;
      return true;
    }

    for (let i = 0; i < MAX_PROMPT_TRIES; i++) {
      const key = await _askUserForKey();
      if (!key) break; // user cancelled
      Cookie.set(key);
      status = await _probe();
      if (status === 'ok') {
        localStorage.setItem(STORAGE_KEY, key);
        _passed = true;
        return true;
      }
    }
    Cookie.clear();
    localStorage.removeItem(STORAGE_KEY);
    return false;
  }

  function check() {
    if (!_checkPromise) _checkPromise = _check();
    return _checkPromise;
  }

  // Re-run the probe/prompt flow (used by ws.js when a dial is rejected —
  // e.g. the cookie expired or the key changed server-side).
  function recheck() {
    _checkPromise = null;
    _passed = false;
    return check();
  }

  function getKey() { return localStorage.getItem(STORAGE_KEY); }

  // Explicit headers for non-cookie callers; the cookie already covers
  // same-origin fetches.
  function getHeaders() {
    const k = getKey();
    return k ? { 'Authorization': `Bearer ${k}` } : {};
  }

  function reset() {
    _passed = false;
    _checkPromise = null;
    Cookie.clear();
    localStorage.removeItem(STORAGE_KEY);
  }

  return {
    check,
    recheck,
    getHeaders,
    getKey,
    reset,
    get passed() { return _passed; },
  };
})();
