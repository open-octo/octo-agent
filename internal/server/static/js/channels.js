// ── Channels — IM channel management panel ──────────────────────────────
//
// Lists configured IM channels. The backend adapter implementations
// (DingTalk, Feishu, etc.) will be added in a future phase.
// ─────────────────────────────────────────────────────────────────────────

const Channels = (() => {
  function init() {
    render();
  }

  function render() {
    const container = $("channels-list");
    if (!container) return;
    container.innerHTML = `
      <div style="padding:24px 32px;color:var(--text-secondary);text-align:center">
        <p style="font-size:16px;margin-bottom:12px">🔌 IM channel adapters coming soon</p>
        <p>DingTalk, Feishu, Telegram, WeCom, and more will be supported in a future update.</p>
      </div>`;
  }

  return { init, render };
})();
