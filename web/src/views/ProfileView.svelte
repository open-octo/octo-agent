<script lang="ts">
  import { onMount } from 'svelte'
  import { memTab, showToast, openAgentSession } from '../lib/stores'
  import StatusTag from '../components/ui/StatusTag.svelte'
  import { renderMarkdown } from '../lib/markdown'
  import * as api from '../lib/api'
  import type { Memory } from '../lib/types'
  import { t } from '../lib/i18n'

  // --- state ---
  interface SoulData {
    content: string
    path: string
  }

  let soulData    = $state<SoulData | null>(null)
  let userData    = $state<SoulData | null>(null)
  let memFiles    = $state<Memory[]>([])
  let loadingSoul = $state(false)
  let loadingUser = $state(false)
  let loadingMem  = $state(false)
  let memLoaded   = $state(false)
  // Lazily-fetched memory bodies, keyed by file name.
  let memContent = $state<Record<string, string>>({})

  // Load on tab change
  $effect(() => {
    const tab = $memTab
    if (tab === 'soul'     && !soulData)  loadSoul()
    if (tab === 'user'     && !userData)  loadUser()
    if (tab === 'memories' && !memLoaded) loadMems()
  })

  async function loadSoul() {
    loadingSoul = true
    try {
      soulData = await api.getProfileSoul() as any
    } catch (e: any) {
      showToast(`Could not load soul.md: ${e.message}`, 'error')
    } finally {
      loadingSoul = false
    }
  }

  async function loadUser() {
    loadingUser = true
    try {
      userData = await api.getProfileUser() as any
    } catch (e: any) {
      showToast(`Could not load user.md: ${e.message}`, 'error')
    } finally {
      loadingUser = false
    }
  }

  async function loadMems() {
    loadingMem = true
    try {
      memFiles = await api.getMemories()
      memLoaded = true
    } catch (e: any) {
      showToast(`Could not load memories: ${e.message}`, 'error')
    } finally {
      loadingMem = false
    }
  }

  // Keyed by the file's full path, not its name: the same filename can exist in
  // both the project and inherited memory dirs, so name alone collides.
  async function toggleMemory(e: Event, f: Memory) {
    const open = (e.currentTarget as HTMLDetailsElement).open
    if (open && memContent[f.path] === undefined) {
      memContent = { ...memContent, [f.path]: '' }   // mark in-flight
      try {
        const d = await api.getMemory(f.name, f.source)
        memContent = { ...memContent, [f.path]: d.content ?? '' }
      } catch {
        memContent = { ...memContent, [f.path]: '_Could not load memory._' }
      }
    }
  }

  async function forgetMemory(f: Memory) {
    try {
      // #1109: was a raw fetch() with no res.ok check — a failing delete
      // (404/500) still reported "Memory removed" and the row reappeared on
      // reload. api.deleteMemory throws on non-2xx via request().
      await api.deleteMemory(f.name, f.source)
      memFiles = memFiles.filter(m => m.path !== f.path)
      showToast('Memory removed', 'success')
    } catch (e: any) {
      showToast(`Failed to remove memory: ${e.message}`, 'error')
    }
  }

  // Agentic-first: open a fresh chat and auto-send the curate command. soul/user
  // run the onboard skill scoped to that one file; memories opens a freeform turn.
  function openAssistantChat(prompt: string, name = 'Profile update') {
    openAgentSession(prompt, name)
  }

  function fmtDate(iso: string): string {
    try {
      return new Date(iso).toLocaleDateString()
    } catch { return iso }
  }

  function iconForMemory(f: Memory): string {
    if (f.source === 'inherited') return 'ant-design:global-outlined'
    return 'ant-design:file-text-outlined'
  }

  function isCustom(data: SoulData | null): boolean {
    // A custom override means the file exists and has non-trivial content
    return !!data && data.content.trim().length > 0
  }

  onMount(() => {
    // Start loading the first visible tab right away
    if ($memTab === 'soul') loadSoul()
  })
</script>

<div class="page">
  <div class="inner">
    <div class="page-header">
      <h2>{$t('profile.title')}</h2>
      <p>{$t('profile.subtitle')}</p>
    </div>

    <!-- Tabs -->
    <div class="tabs">
      <div class="tab" class:active={$memTab === 'soul'}     onclick={() => memTab.set('soul')}>{$t('profile.soul')}</div>
      <div class="tab" class:active={$memTab === 'user'}     onclick={() => memTab.set('user')}>{$t('profile.user')}</div>
      <div class="tab" class:active={$memTab === 'memories'} onclick={() => memTab.set('memories')}>{$t('profile.memories')}</div>
    </div>

    {#if $memTab === 'soul'}
      <div class="section-card">
        <div class="card-header">
          <span class="card-title">{$t('profile.soul_title')}</span>
          <span style="margin-left:auto"></span>
          {#if soulData}
            <span class="file-path mono">{soulData.path}</span>
            <StatusTag status={isCustom(soulData) ? 'success' : 'default'}>
              {isCustom(soulData) ? $t('profile.custom_override') : $t('profile.default')}
            </StatusTag>
          {/if}
        </div>
        {#if loadingSoul}
          <div class="card-loading">Loading…</div>
        {:else if soulData}
          <div class="card-body md-content">{@html renderMarkdown(soulData.content)}</div>
        {:else}
          <div class="card-loading">{$t('profile.soul_empty')}</div>
        {/if}
        <div class="card-footer">
          <span class="footer-hint">{$t('profile.soul_hint')}</span>
          <button class="btn-primary" onclick={() => openAssistantChat('/onboard scope:soul', 'Update soul.md')}>
            {$t('profile.update')}
          </button>
        </div>
      </div>
    {/if}

    {#if $memTab === 'user'}
      <div class="section-card">
        <div class="card-header">
          <span class="card-title">{$t('profile.user_title')}</span>
          <span style="margin-left:auto"></span>
          {#if userData}
            <span class="file-path mono">{userData.path}</span>
            <StatusTag status={isCustom(userData) ? 'success' : 'default'}>
              {isCustom(userData) ? $t('profile.custom_override') : $t('profile.default')}
            </StatusTag>
          {/if}
        </div>
        {#if loadingUser}
          <div class="card-loading">Loading…</div>
        {:else if userData}
          <div class="card-body md-content">{@html renderMarkdown(userData.content)}</div>
        {:else}
          <div class="card-loading">{$t('profile.user_empty')}</div>
        {/if}
        <div class="card-footer">
          <span class="footer-hint">{$t('profile.user_hint')}</span>
          <button class="btn-primary" onclick={() => openAssistantChat('/onboard scope:user', 'Update user.md')}>
            {$t('profile.update')}
          </button>
        </div>
      </div>
    {/if}

    {#if $memTab === 'memories'}
      <div class="section-card">
        <div class="card-header">
          <div style="display:flex;align-items:center;gap:8px;">
            <span class="card-title">{$t('profile.memories_title')}</span>
            <span class="mem-count">{memFiles.length} entries</span>
          </div>
          <span class="auto-badge">
            <iconify-icon icon="ant-design:thunderbolt-outlined" width="13"></iconify-icon>
            {$t('profile.captured_auto')}
          </span>
        </div>
        {#if loadingMem}
          <div class="card-loading">{$t('profile.memories_loading')}</div>
        {:else if memFiles.length === 0}
          <div class="card-loading">{$t('profile.memories_empty')}</div>
        {:else}
          {#each memFiles as f (f.path)}
            <details class="mem-row" ontoggle={(e) => toggleMemory(e, f)}>
              <summary class="mem-summary">
                <span class="mem-icon">
                  <iconify-icon icon={iconForMemory(f)} width="14"></iconify-icon>
                </span>
                <div class="mem-content">
                  <span class="mem-text mono">{f.name}</span>
                  <span class="mem-meta">{f.source} · {fmtDate(f.updated_at)}</span>
                </div>
                <StatusTag status="default">{f.source}</StatusTag>
                <button class="forget-btn" onclick={(e) => { e.preventDefault(); forgetMemory(f) }}>
                  <iconify-icon icon="ant-design:close-circle-outlined" width="13"></iconify-icon>
                  {$t('profile.forget')}
                </button>
                <iconify-icon icon="lucide:chevron-right" width="14" class="mem-chevron" style="color:var(--text-tertiary)"></iconify-icon>
              </summary>
              <div class="mem-body">
                {#if memContent[f.path] === undefined || memContent[f.path] === ''}
                  <span class="mem-loading">Loading…</span>
                {:else}
                  <div class="md-content">{@html renderMarkdown(memContent[f.path])}</div>
                {/if}
              </div>
            </details>
          {/each}
        {/if}
        <div class="card-footer">
          <span class="footer-hint">{$t('profile.memories_hint')}</span>
          <button class="btn-primary" onclick={() => openAssistantChat('Review my saved memories and help me update or remove any that are stale.', 'Update memories')}>
            {$t('profile.update')}
          </button>
        </div>
      </div>
    {/if}
  </div>
</div>

<style>
.page { flex: 1; overflow-y: auto; min-height: 0; }
.inner { max-width: 800px; margin: 0 auto; padding: 24px; display: flex; flex-direction: column; gap: 20px; }
.page-header { display: flex; flex-direction: column; gap: 4px; }
h2 { margin: 0; font-size: 24px; font-weight: 600; color: var(--text-heading); }
p { margin: 0; font-size: 14px; color: var(--text-secondary); }
.tabs { display: flex; align-items: center; gap: 24px; border-bottom: 1px solid var(--border-secondary); }
.tab {
  padding: 0 2px 10px; margin-bottom: -1px; cursor: pointer;
  font-size: 14px; color: var(--text-secondary);
  border-bottom: 2px solid transparent;
}
.tab.active { font-weight: 600; color: var(--blue-6); border-bottom-color: var(--blue-6); }
.tab:hover:not(.active) { color: var(--text); }
.section-card { background: var(--bg-container); border-radius: 16px; box-shadow: var(--card-shadow); overflow: hidden; }
.card-header {
  display: flex; align-items: center; gap: 12px;
  padding: 16px 24px; border-bottom: 1px solid var(--border-table);
  flex-wrap: wrap;
}
.card-title { font-size: 16px; font-weight: 600; color: var(--text-heading); }
.file-path { font-size: 12px; color: var(--text-tertiary); }
.card-body { padding: 20px 24px; font-size: 14px; line-height: 1.7; color: var(--text); }
.card-loading { padding: 24px; text-align: center; color: var(--text-tertiary); font-size: 14px; }

/* Rendered markdown (soul / user / memory bodies) */
.md-content { font-size: 14px; line-height: 1.7; color: var(--text); }
:global(.md-content > :first-child) { margin-top: 0; }
:global(.md-content > :last-child) { margin-bottom: 0; }
:global(.md-content h1), :global(.md-content h2), :global(.md-content h3) { font-size: 15px; margin: 14px 0 6px; }
:global(.md-content p) { margin: 8px 0; }
:global(.md-content ul), :global(.md-content ol) { margin: 8px 0; padding-left: 20px; }
:global(.md-content li) { margin: 3px 0; }
:global(.md-content code) {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12.5px;
  background: var(--bg-table-header); border: 1px solid var(--border-table); border-radius: 4px; padding: 1px 5px;
}
:global(.md-content pre) {
  margin: 8px 0; padding: 12px 14px; overflow-x: auto; background: var(--bg-sidebar);
  border: 1px solid var(--border-table); border-radius: 8px; font-size: 12.5px; line-height: 1.6;
}
:global(.md-content a) { color: var(--blue-6); }
.card-footer {
  display: flex; align-items: center; gap: 16px;
  padding: 16px 24px; border-top: 1px dashed var(--border-secondary);
}
.footer-hint { font-size: 13px; color: var(--text-tertiary); flex: 1; min-width: 0; }
.btn-primary { height: 32px; padding: 0 14px; border: none; background: var(--blue-6); border-radius: 6px; font-size: 13px; color: #fff; cursor: pointer; font-family: inherit; white-space: nowrap; }
.btn-primary:hover { background: var(--blue-5); }
.mem-count { font-size: 12px; color: var(--text-tertiary); }
.auto-badge { display: flex; align-items: center; gap: 6px; font-size: 12px; color: var(--text-tertiary); margin-left: auto; }
.mem-row { border-bottom: 1px solid var(--border-table); background: var(--bg-container); }
.mem-summary {
  list-style: none; display: flex; align-items: center; gap: 12px;
  padding: 14px 24px; cursor: pointer; user-select: none;
}
.mem-summary::-webkit-details-marker { display: none; }
.mem-summary:hover { background: var(--active-blue-bg); }
.mem-row[open] .mem-chevron { transform: rotate(90deg); }
.mem-chevron { flex: 0 0 auto; transition: transform 0.15s; }
.mem-body { padding: 4px 24px 16px 52px; border-top: 1px solid var(--bg-layout); }
.mem-loading { font-size: 13px; color: var(--text-tertiary); }
.mem-icon {
  width: 28px; height: 28px; flex: 0 0 28px; border-radius: 9999px;
  background: var(--blue-1); color: var(--blue-6); display: flex; align-items: center; justify-content: center;
  margin-top: 1px;
}
.mem-content { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 4px; }
.mem-text { font-size: 14px; line-height: 1.5; color: var(--text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.mem-meta { font-size: 12px; color: var(--text-tertiary); }
.forget-btn {
  flex: 0 0 auto; margin-top: 1px; height: 28px; padding: 0 10px;
  border: 1px solid var(--border-secondary); background: var(--bg-container); border-radius: 6px;
  display: flex; align-items: center; gap: 6px; font-size: 12px;
  color: var(--text-tertiary); cursor: pointer; font-family: inherit;
}
.forget-btn:hover { border-color: var(--error); color: var(--error); }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
</style>
