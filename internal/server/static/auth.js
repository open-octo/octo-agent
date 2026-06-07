// auth.js — Access-key authentication (removed in v0.16.0)
//
// Kept as a no-op module so existing callers (ws.js, app.js) do not break.
// The module always reports "passed" and never prompts the user.
//
const Auth = (() => {
  function check() { return Promise.resolve(true); }
  function getHeaders() { return {}; }
  function getKey() { return null; }
  function reset() {}

  return {
    check,
    getHeaders,
    getKey,
    reset,
    get passed() { return true; },
  };
})();
