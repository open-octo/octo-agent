// ── WS Dispatcher — route server events to feature modules ───────────────
//
// All server events flow through here. Each module registers handlers for
// event types it cares about. This keeps app.js lean and modules decoupled.
//
// Event type naming matches the Go ws_types.go exactly.
// ─────────────────────────────────────────────────────────────────────────

const WSDispatcher = (() => {
  // Map: event_type → array of handler functions
  const _registrations = {};

  /**
   * Register a handler for one or more event types.
   * @param {string|string[]} types  - event type(s) to listen for
   * @param {function} fn            - handler(event)
   */
  function on(types, fn) {
    const arr = Array.isArray(types) ? types : [types];
    arr.forEach(t => {
      if (!_registrations[t]) _registrations[t] = [];
      _registrations[t].push(fn);
    });
  }

  /**
   * Dispatch an event to all registered handlers for its type.
   */
  function dispatch(event) {
    const type = event.type;
    if (!type) return;

    // Always dispatch "all" handlers.
    const handlers = (_registrations["all"] || []).concat(
      _registrations[type] || []
    );
    handlers.forEach(fn => {
      try { fn(event); } catch (e) { console.error("[WSDispatcher]", type, e); }
    });
  }

  // Wire into WS on module load.
  WS.onEvent(dispatch);

  return { on, dispatch };
})();
