<script lang="ts">
  import { manageCat, type ManageCategory } from '../lib/stores'
  import { t } from '../lib/i18n'
  import AgentsView from './AgentsView.svelte'
  import SkillsView from './SkillsView.svelte'
  import McpView from './McpView.svelte'
  import WorkflowsView from './WorkflowsView.svelte'
  import BrowserView from './BrowserView.svelte'
  import ChannelsView from './ChannelsView.svelte'

  // Consolidates the six agentic-config surfaces (each previously its own
  // top-level sidebar entry) behind one "Manage" entry with a category rail —
  // the sidebar was getting too crowded. Scheduled Tasks and the My Data
  // group (memory/light apps/file recall) stay as their own sidebar rows;
  // only the "configure via conversation" surfaces move here.
  const categories: { key: ManageCategory, icon: string, label: string }[] = [
    { key: 'agents',    icon: 'ant-design:robot-outlined',      label: 'nav.agents' },
    { key: 'skills',    icon: 'ant-design:thunderbolt-outlined', label: 'nav.skills' },
    { key: 'mcp',       icon: 'ant-design:api-outlined',        label: 'nav.mcp' },
    { key: 'workflows', icon: 'ant-design:partition-outlined',  label: 'nav.workflows' },
    { key: 'browser',   icon: 'ant-design:global-outlined',     label: 'nav.browser' },
    { key: 'channels',  icon: 'ant-design:mobile-outlined',     label: 'nav.channels' },
  ]
</script>

<div class="manage-view">
  <div class="rail">
    {#each categories as c (c.key)}
      <div class="scat" class:on={$manageCat === c.key} onclick={() => manageCat.set(c.key)}>
        <iconify-icon icon={c.icon} width="15"></iconify-icon>
        <span>{$t(c.label)}</span>
      </div>
    {/each}
  </div>

  <div class="pane">
    {#if $manageCat === 'agents'}
      <AgentsView />
    {:else if $manageCat === 'skills'}
      <SkillsView />
    {:else if $manageCat === 'mcp'}
      <McpView />
    {:else if $manageCat === 'workflows'}
      <WorkflowsView />
    {:else if $manageCat === 'browser'}
      <BrowserView />
    {:else if $manageCat === 'channels'}
      <ChannelsView />
    {/if}
  </div>
</div>

<style>
.manage-view { flex: 1; min-height: 0; display: flex; }

/* ── category rail ─────────────────────────────────────────────────────────── */
.rail {
  width: 190px; flex: 0 0 190px; border-right: 1px solid var(--border);
  background: var(--bg-layout); padding: 10px; display: flex; flex-direction: column; gap: 2px;
  overflow-y: auto;
}
.scat {
  display: flex; align-items: center; gap: 9px; padding: 7px 10px;
  border-radius: 7px; cursor: pointer; color: var(--text-tertiary);
}
.scat span { font-size: 13px; color: var(--text-secondary); }
.scat:hover { background: var(--hover-neutral); }
.scat.on { background: var(--active-blue-bg); }
.scat.on span { color: var(--blue-6); font-weight: 600; }
.scat.on { color: var(--blue-6); }

/* ── content pane ──────────────────────────────────────────────────────────── */
/* The child is whichever existing view is active — each already manages its
   own internal scrolling (.page or .view root), so this just needs to hand
   it the full available box without adding a second scroll container. */
.pane { flex: 1; min-width: 0; min-height: 0; overflow: hidden; display: flex; flex-direction: column; }
.pane > :global(*) { flex: 1; min-height: 0; }
</style>
