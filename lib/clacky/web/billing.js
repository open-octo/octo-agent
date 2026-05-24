// billing.js — Billing panel logic
// Handles displaying billing summary, daily breakdown, and usage statistics.

const Billing = (() => {
  let _summary = null;
  let _daily = [];
  let _currentPeriod = "month";

  // ── Currency Settings ─────────────────────────────────────────────────────
  const CURRENCY_STORAGE_KEY = "clacky-currency";
  const USD_TO_CNY_RATE = 6.7944; // Exchange rate: 1 USD ≈ 6.7944 CNY

  function _getCurrency() {
    try { return localStorage.getItem(CURRENCY_STORAGE_KEY) || "USD"; } catch (_) { return "USD"; }
  }

  function _convertCost(usdCost) {
    const currency = _getCurrency();
    if (currency === "CNY") {
      return usdCost * USD_TO_CNY_RATE;
    }
    return usdCost;
  }

  function _getCurrencySymbol() {
    return _getCurrency() === "CNY" ? "¥" : "$";
  }

  // ── Public API ──────────────────────────────────────────────────────────────

  function open() {
    _load();
    // Listen for currency changes
    document.removeEventListener("currencychange", _onCurrencyChange);
    document.addEventListener("currencychange", _onCurrencyChange);
  }

  function _onCurrencyChange() {
    if (_summary) _render();
  }

  // ── Data Loading ────────────────────────────────────────────────────────────

  async function _load() {
    const container = document.getElementById("billing-content");
    if (!container) return;

    container.innerHTML = `<div class="billing-loading">${I18n.t("billing.loading") || "Loading billing data..."}</div>`;

    try {
      const [summaryRes, dailyRes] = await Promise.all([
        fetch(`/api/billing/summary?period=${_currentPeriod}`),
        fetch("/api/billing/daily?days=30")
      ]);

      _summary = await summaryRes.json();
      const dailyData = await dailyRes.json();
      _daily = dailyData.days || [];

      _render();
    } catch (e) {
      container.innerHTML = `<div class="billing-error">${I18n.t("billing.error") || "Failed to load billing data"}: ${e.message}</div>`;
    }
  }

  // ── Rendering ───────────────────────────────────────────────────────────────

  function _render() {
    const container = document.getElementById("billing-content");
    if (!container || !_summary) return;

    const periodOptions = ["day", "week", "month", "year", "all"].map(p => 
      `<option value="${p}" ${p === _currentPeriod ? "selected" : ""}>${_periodLabel(p)}</option>`
    ).join("");

    container.innerHTML = `
      <div class="billing-header">
        <h2>${I18n.t("billing.title") || "💰 Billing"}</h2>
        <select id="billing-period-select" class="billing-period-select">
          ${periodOptions}
        </select>
      </div>

      <div class="billing-summary-cards">
        <div class="billing-card billing-card-primary">
          <div class="billing-card-label">${I18n.t("billing.totalCost") || "Total Cost"}</div>
          <div class="billing-card-value">${_getCurrencySymbol()}${_formatCost(_convertCost(_summary.total_cost))}</div>
        </div>
        <div class="billing-card">
          <div class="billing-card-label">${I18n.t("billing.totalTokens") || "Total Tokens"}</div>
          <div class="billing-card-value">${_formatNumber(_summary.total_tokens)}</div>
        </div>
        <div class="billing-card">
          <div class="billing-card-label">${I18n.t("billing.requests") || "API Requests"}</div>
          <div class="billing-card-value">${_formatNumber(_summary.record_count)}</div>
        </div>
      </div>

      <div class="billing-details">
        <div class="billing-section">
          <h3>${I18n.t("billing.tokenBreakdown") || "Token Breakdown"}</h3>
          <div class="billing-token-grid">
            <div class="billing-token-item">
              <span class="billing-token-label">📥 ${I18n.t("billing.promptTokens") || "Prompt"}</span>
              <span class="billing-token-value">${_formatNumber(_summary.prompt_tokens)}</span>
            </div>
            <div class="billing-token-item">
              <span class="billing-token-label">📤 ${I18n.t("billing.completionTokens") || "Completion"}</span>
              <span class="billing-token-value">${_formatNumber(_summary.completion_tokens)}</span>
            </div>
            <div class="billing-token-item">
              <span class="billing-token-label">🗄️ ${I18n.t("billing.cacheRead") || "Cache Read"}</span>
              <span class="billing-token-value">${_formatNumber(_summary.cache_read_tokens)}</span>
            </div>
            <div class="billing-token-item">
              <span class="billing-token-label">📝 ${I18n.t("billing.cacheWrite") || "Cache Write"}</span>
              <span class="billing-token-value">${_formatNumber(_summary.cache_write_tokens)}</span>
            </div>
          </div>
        </div>

        ${_renderModelBreakdown()}
        ${_renderDailyChart()}
      </div>
    `;

    // Bind period change handler
    document.getElementById("billing-period-select")?.addEventListener("change", (e) => {
      _currentPeriod = e.target.value;
      _load();
    });
  }

  function _renderModelBreakdown() {
    if (!_summary.by_model || Object.keys(_summary.by_model).length === 0) {
      return "";
    }

    const rows = Object.entries(_summary.by_model)
      .sort((a, b) => (b[1].cost || b[1]) - (a[1].cost || a[1]))
      .map(([model, data]) => {
        const cost = typeof data === "object" ? data.cost : data;
        const requests = typeof data === "object" ? data.requests : "—";
        return `
          <tr>
            <td class="billing-model-name">${_esc(model)}</td>
            <td class="billing-model-cost">${_getCurrencySymbol()}${_formatCost(_convertCost(cost))}</td>
            <td class="billing-model-requests">${requests}</td>
          </tr>
        `;
      }).join("");

    return `
      <div class="billing-section">
        <h3>${I18n.t("billing.byModel") || "By Model"}</h3>
        <table class="billing-model-table">
          <thead>
            <tr>
              <th>${I18n.t("billing.model") || "Model"}</th>
              <th>${I18n.t("billing.cost") || "Cost"}</th>
              <th>${I18n.t("billing.requests") || "Requests"}</th>
            </tr>
          </thead>
          <tbody>
            ${rows}
          </tbody>
        </table>
      </div>
    `;
  }

  function _renderDailyChart() {
    if (!_daily || _daily.length === 0) {
      return "";
    }

    // Get last 14 days with activity
    const recentDays = _daily.filter(d => d.cost > 0).slice(-14);
    if (recentDays.length === 0) {
      return "";
    }

    const maxCost = Math.max(...recentDays.map(d => d.cost), 0.01);

    const bars = recentDays.map(d => {
      const height = Math.max((d.cost / maxCost) * 100, 2);
      const date = d.date.slice(5); // MM-DD
      return `
        <div class="billing-chart-bar-wrapper" title="${d.date}: ${_getCurrencySymbol()}${_formatCost(_convertCost(d.cost))}">
          <div class="billing-chart-bar" style="height: ${height}%"></div>
          <div class="billing-chart-label">${date}</div>
        </div>
      `;
    }).join("");

    return `
      <div class="billing-section">
        <h3>${I18n.t("billing.dailyUsage") || "Daily Usage"}</h3>
        <div class="billing-chart">
          ${bars}
        </div>
      </div>
    `;
  }

  // ── Helpers ─────────────────────────────────────────────────────────────────

  function _formatCost(cost) {
    if (cost == null || cost === 0) return "0.0000";
    return cost.toFixed(4);
  }

  function _formatNumber(num) {
    if (num == null || num === 0) return "0";
    return num.toLocaleString();
  }

  function _periodLabel(period) {
    const labels = {
      day: I18n.t("billing.period.day") || "Today",
      week: I18n.t("billing.period.week") || "This Week",
      month: I18n.t("billing.period.month") || "This Month",
      year: I18n.t("billing.period.year") || "This Year",
      all: I18n.t("billing.period.all") || "All Time"
    };
    return labels[period] || period;
  }

  function _esc(str) {
    if (!str) return "";
    const div = document.createElement("div");
    div.textContent = str;
    return div.innerHTML;
  }

  // ── Expose Public API ───────────────────────────────────────────────────────

  return { 
    open,
    // Expose currency utilities for other modules
    getCurrency: _getCurrency,
    convertCost: _convertCost,
    getCurrencySymbol: _getCurrencySymbol,
    USD_TO_CNY_RATE
  };
})();
