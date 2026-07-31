<script lang="ts">
  import { t } from '../lib/i18n'
  import StatusTag from '../components/ui/StatusTag.svelte'
  import Switch from '../components/ui/Switch.svelte'
  import Segment from '../components/ui/Segment.svelte'

  // Living acceptance page for the design tokens (dev-docs/design/desktop-redesign.md).
  // Every swatch/control renders through the semantic vars, so this page shows the
  // real values of whichever theme is active — flip light/dark to verify both.
  const swatches = [
    { labelKey: 'components.color_window', varName: '--bg-layout', bordered: true },
    { labelKey: 'components.color_card', varName: '--bg-container', bordered: true },
    { labelKey: 'components.color_accent', varName: '--blue-6' },
    { labelKey: 'components.color_text', varName: '--text' },
    { labelKey: 'components.color_secondary', varName: '--text-secondary' },
    { labelKey: 'components.color_success', varName: '--success' },
    { labelKey: 'components.color_danger', varName: '--error' },
  ]

  let segValue = $state('auto')
  let switchOn = $state(true)
  let switchOff = $state(false)
  let activeTab = $state('soul')
  const tabs = [
    { id: 'soul', labelKey: 'components.tab_soul' },
    { id: 'user', labelKey: 'components.tab_user' },
    { id: 'memories', labelKey: 'components.tab_memories' },
  ]
</script>

<div class="page">
  <div class="inner">
    <div class="page-header">
      <h2>{$t('nav.components')}</h2>
      <p>{$t('components.subtitle')}</p>
    </div>

    <!-- Colors -->
    <div class="card">
      <div class="card-title">{$t('components.colors')}</div>
      <div class="swatch-row">
        {#each swatches as s}
          <div class="swatch">
            <div class="swatch-chip" class:bordered={s.bordered} style="background:var({s.varName})"></div>
            <div class="swatch-label">{$t(s.labelKey)}<br /><span class="mono">{s.varName}</span></div>
          </div>
        {/each}
      </div>
    </div>

    <div class="grid2">
      <!-- Buttons -->
      <div class="card">
        <div class="card-title">{$t('components.buttons')}</div>
        <div class="demo-row">
          <button class="btn-primary">{$t('components.btn_primary')}</button>
          <button class="btn-secondary">{$t('components.btn_secondary')}</button>
          <button class="btn-secondary danger">{$t('components.btn_danger')}</button>
          <button class="icon-btn" aria-label="icon button">
            <iconify-icon icon="lucide:pencil" width="15"></iconify-icon>
          </button>
          <button class="btn-primary" disabled>{$t('components.btn_disabled')}</button>
        </div>
      </div>

      <!-- Segmented / switches -->
      <div class="card">
        <div class="card-title">{$t('components.controls')}</div>
        <div class="demo-row">
          <Segment options={['auto', 'on', 'off']} bind:value={segValue} />
          <Switch bind:checked={switchOn} />
          <Switch bind:checked={switchOff} />
        </div>
      </div>

      <!-- Status tags -->
      <div class="card">
        <div class="card-title">{$t('components.tags')}</div>
        <div class="demo-row">
          <StatusTag status="success">{$t('components.tag_connected')}</StatusTag>
          <StatusTag status="info">{$t('components.tag_running')}</StatusTag>
          <StatusTag status="warning">{$t('components.tag_stale')}</StatusTag>
          <StatusTag status="default">{$t('components.tag_paused')}</StatusTag>
          <StatusTag status="error">{$t('components.tag_error')}</StatusTag>
        </div>
      </div>

      <!-- Inputs -->
      <div class="card">
        <div class="card-title">{$t('components.inputs')}</div>
        <div class="demo-row">
          <input class="demo-input" placeholder={$t('components.input_placeholder')} />
          <select class="demo-input">
            <option>简体中文</option>
            <option>English</option>
          </select>
        </div>
      </div>

      <!-- Avatars -->
      <div class="card">
        <div class="card-title">{$t('components.avatars')}</div>
        <div class="demo-row">
          <span class="avatar user">乔</span>
          <span class="avatar bot">
            <iconify-icon icon="lucide:bot" width="15"></iconify-icon>
          </span>
          <span class="agent-chip"><span class="agent-at">@</span>写作助手</span>
        </div>
      </div>

      <!-- Tabs -->
      <div class="card">
        <div class="card-title">{$t('components.tabs')}</div>
        <div class="tab-row">
          {#each tabs as tab}
            <button class="tab" class:on={activeTab === tab.id} onclick={() => (activeTab = tab.id)}>
              {$t(tab.labelKey)}
            </button>
          {/each}
        </div>
      </div>
    </div>

    <!-- Tool call card -->
    <div class="card">
      <div class="card-title">{$t('components.tool_card')}</div>
      <div class="tool-card">
        <span class="tool-icon">
          <iconify-icon icon="lucide:file-text" width="16"></iconify-icon>
        </span>
        <div class="tool-body">
          <div class="tool-name">
            <span class="tool-title">{$t('components.tool_read')}</span>
            <span class="mono tool-arg">agent-harness.md</span>
          </div>
          <div class="tool-meta">{$t('components.tool_meta')}</div>
        </div>
        <span class="tool-status">
          <iconify-icon icon="lucide:check" width="13"></iconify-icon>
          {$t('components.tool_done')}
        </span>
      </div>
    </div>

    <!-- Empty state -->
    <div class="card">
      <div class="card-title">{$t('components.empty_state')}</div>
      <div class="empty">
        <iconify-icon icon="lucide:trash-2" width="30"></iconify-icon>
        <span>{$t('components.empty_trash')}</span>
      </div>
    </div>
  </div>
</div>

<style>
.page { flex: 1; overflow-y: auto; min-height: 0; background: var(--bg-layout); }
.inner { max-width: 1000px; margin: 0 auto; padding: 26px 28px 40px; display: flex; flex-direction: column; gap: 20px; }
.page-header { display: flex; flex-direction: column; gap: 4px; }
h2 { margin: 0; font-size: 22px; font-weight: 700; letter-spacing: -0.01em; color: var(--text-heading); }
p { margin: 0; font-size: 13px; color: var(--text-secondary); max-width: 60ch; }

.card {
  background: var(--bg-container); border: 1px solid var(--border);
  border-radius: var(--radius-card); box-shadow: var(--card-shadow); padding: 18px;
}
.card-title { font-size: 13px; font-weight: 600; margin-bottom: 14px; color: var(--text-heading); }
.grid2 { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; }
@media (max-width: 860px) { .grid2 { grid-template-columns: 1fr; } }

.demo-row { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; }

/* Swatches */
.swatch-row { display: flex; gap: 14px; flex-wrap: wrap; }
.swatch { text-align: center; }
.swatch-chip { width: 56px; height: 56px; border-radius: 10px; }
.swatch-chip.bordered { border: 1px solid var(--border); }
.swatch-label { font-size: 10px; color: var(--text-secondary); margin-top: 5px; line-height: 1.5; }
.mono { font-family: var(--font-mono); }

/* Buttons */
.btn-primary {
  display: inline-flex; align-items: center; gap: 6px; height: 32px; padding: 0 14px;
  background: var(--blue-6); border: none; border-radius: var(--radius-sm);
  color: var(--on-accent); font-size: 13px; font-weight: 600; cursor: pointer; font-family: inherit;
  box-shadow: 0 1px 2px var(--focus-ring); white-space: nowrap;
}
.btn-primary:hover:not(:disabled) { background: var(--blue-5); }
.btn-primary:active:not(:disabled) { background: var(--blue-7); }
.btn-primary:disabled { opacity: 0.4; cursor: not-allowed; }
.btn-secondary {
  display: inline-flex; align-items: center; gap: 6px; height: 32px; padding: 0 12px;
  background: var(--bg-container); border: 1px solid var(--border); border-radius: var(--radius-sm);
  color: var(--text); font-size: 13px; font-weight: 500; cursor: pointer; font-family: inherit; white-space: nowrap;
}
.btn-secondary:hover { background: var(--hover-neutral); }
.btn-secondary.danger { color: var(--error); }
.btn-secondary.danger:hover { background: var(--error-bg); border-color: var(--error-border); }
.icon-btn {
  width: 28px; height: 28px; display: grid; place-items: center; border: none;
  background: transparent; border-radius: 7px; color: var(--text-secondary); cursor: pointer;
}
.icon-btn:hover { background: var(--hover-neutral); color: var(--text); }

/* Inputs */
.demo-input {
  height: 32px; padding: 0 10px; border: 1px solid var(--border); border-radius: var(--radius-sm);
  font-size: 13px; font-family: inherit; background: var(--bg-container); color: var(--text);
  outline: none; width: 150px;
}
.demo-input:focus { border-color: var(--blue-6); box-shadow: 0 0 0 2px var(--focus-ring); }

/* Avatars */
.avatar {
  width: 26px; height: 26px; border-radius: 7px; display: grid; place-items: center;
  font-size: 12px; font-weight: 600; color: var(--on-accent);
}
.avatar.user { background: var(--text-quaternary); }
.avatar.bot { background: var(--blue-6); }
.agent-chip {
  display: inline-flex; align-items: center; gap: 6px; height: 26px; padding: 0 9px 0 5px;
  background: var(--active-blue-bg); border-radius: 7px; color: var(--blue-6);
  font-size: 12px; font-weight: 600;
}
.agent-at {
  width: 16px; height: 16px; border-radius: 5px; background: var(--blue-6); color: var(--on-accent);
  display: grid; place-items: center; font-size: 11px; font-weight: 700; line-height: 1;
}

/* Tabs */
.tab-row { display: flex; gap: 4px; border-bottom: 1px solid var(--border); }
.tab {
  padding: 6px 14px; font-size: 13px; color: var(--text-secondary); cursor: pointer;
  border: none; background: transparent; font-family: inherit;
  border-bottom: 2px solid transparent; margin-bottom: -1px;
}
.tab.on { color: var(--text); font-weight: 600; border-bottom-color: var(--blue-6); }

/* Tool call card */
.tool-card {
  display: flex; align-items: center; gap: 11px; padding: 10px 12px;
  border: 1px solid var(--border); background: var(--bg-container); border-radius: 10px;
}
.tool-icon {
  width: 30px; height: 30px; flex: none; background: var(--hover-neutral);
  border-radius: var(--radius-sm); display: grid; place-items: center; color: var(--text-secondary);
}
.tool-body { flex: 1; min-width: 0; }
.tool-name { font-size: 13px; }
.tool-title { font-weight: 600; }
.tool-arg { font-size: 11px; color: var(--text-secondary); }
.tool-meta { font-size: 11px; color: var(--text-secondary); }
.tool-status {
  display: inline-flex; align-items: center; gap: 4px;
  font-size: 11px; color: var(--success); font-weight: 500;
}

/* Empty state */
.empty {
  display: flex; flex-direction: column; align-items: center; gap: 10px;
  padding: 24px; color: var(--text-secondary); font-size: 13px;
}
</style>
