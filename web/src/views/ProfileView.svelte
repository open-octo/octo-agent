<script lang="ts">
  import { onMount } from 'svelte'
  import { memTab, memories, showToast, view, sessions, activeSessionId } from '../lib/stores'
  import StatusTag from '../components/ui/StatusTag.svelte'
  import * as api from '../lib/api'

  // --- state ---
  interface MemFile {
    name: string
    path: string
    size: number
    updated_at: string
    source: string
  }

  interface SoulData {
    content: string
    path: string
  }

  let soulData    = $state<SoulData | null>(null)
  let userData    = $state<SoulData | null>(null)
  let memFiles    = $state<MemFile[]>([])
  let loadingSoul = $state(false)
  let loadingUser = $state(false)
  let loadingMem  = $state(false)

  // Load on tab change
  $effect(() => {
    const tab = $memTab
    if (tab === 'soul'     && !soulData)  loadSoul()
    if (tab === 'user'     && !userData)  loadUser()
    if (tab === 'memories' && !memFiles.length) loadMems()
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
      const data = await api.getMemories() as any
      memFiles = data.files ?? data ?? []
    } catch (e: any) {
      showToast(`Could not load memories: ${e.message}`, 'error')
    } finally {
      loadingMem = false
    }
  }

  async function forgetMemory(name: string) {
    try {
      await fetch(`/api/memories/${encodeURIComponent(name)}`, { method: 'DELETE' })
      memFiles = memFiles.filter(f => f.name !== name)
      showToast('Memory removed', 'success')
    } catch (e: any) {
      showToast(`Failed to remove memory: ${e.message}`, 'error')
    }
  }

  async function openAssistantChat(prompt: string) {
    try {
      const sess = await api.createSession({ name: 'Memory update' })
      sessions.update(s => [sess, ...s])
      activeSessionId.set(sess.id)
      view.set('chat')
      // The draft is set on the input by the ChatView reading a store —
      // for now we just open the session; the user will type.
    } catch (e: any) {
      showToast(`Could not open session: ${e.message}`, 'error')
    }
  }

  function fmtDate(iso: string): string {
    try {
      return new Date(iso).toLocaleDateString()
    } catch { return iso }
  }

  function iconForMemory(f: MemFile): string {
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
      <h2>Assistant Memory</h2>
      <p>A window into the assistant's inner life — who it is, who you are, and what it remembers about your work together.</p>
    </div>

    <!-- Tabs -->
    <div class="tabs">
      <div class="tab" class:active={$memTab === 'soul'}     onclick={() => memTab.set('soul')}>Soul</div>
      <div class="tab" class:active={$memTab === 'user'}     onclick={() => memTab.set('user')}>User</div>
      <div class="tab" class:active={$memTab === 'memories'} onclick={() => memTab.set('memories')}>Memories</div>
    </div>

    {#if $memTab === 'soul'}
      <div class="section-card">
        <div class="card-header">
          <span class="card-title">Who the assistant is</span>
          <span style="margin-left:auto"></span>
          {#if soulData}
            <span class="file-path mono">{soulData.path}</span>
            <StatusTag status={isCustom(soulData) ? 'success' : 'default'}>
              {isCustom(soulData) ? 'Custom Override Active' : 'Default'}
            </StatusTag>
          {/if}
        </div>
        {#if loadingSoul}
          <div class="card-loading">Loading…</div>
        {:else if soulData}
          <div class="card-body">
            <pre class="file-content">{soulData.content}</pre>
          </div>
        {:else}
          <div class="card-loading">soul.md not found — ask the assistant to create one.</div>
        {/if}
        <div class="card-footer">
          <span class="footer-hint">Want a different working style? Ask the assistant to revise how it shows up.</span>
          <button class="btn-primary" onclick={() => openAssistantChat('Please update my soul.md')}>
            Have the Assistant Update This
          </button>
        </div>
      </div>
    {/if}

    {#if $memTab === 'user'}
      <div class="section-card">
        <div class="card-header">
          <span class="card-title">Who you are</span>
          <span style="margin-left:auto"></span>
          {#if userData}
            <span class="file-path mono">{userData.path}</span>
            <StatusTag status={isCustom(userData) ? 'success' : 'default'}>
              {isCustom(userData) ? 'Custom Override Active' : 'Default'}
            </StatusTag>
          {/if}
        </div>
        {#if loadingUser}
          <div class="card-loading">Loading…</div>
        {:else if userData}
          <div class="card-body">
            <pre class="file-content">{userData.content}</pre>
          </div>
        {:else}
          <div class="card-loading">user.md not found — ask the assistant to create one.</div>
        {/if}
        <div class="card-footer">
          <span class="footer-hint">Changed jobs? Picked up new interests? Let the assistant update your profile.</span>
          <button class="btn-primary" onclick={() => openAssistantChat('Please update my user.md')}>
            Have the Assistant Update This
          </button>
        </div>
      </div>
    {/if}

    {#if $memTab === 'memories'}
      <div class="section-card">
        <div class="card-header">
          <div style="display:flex;align-items:center;gap:8px;">
            <span class="card-title">What it remembers</span>
            <span class="mem-count">{memFiles.length} entries</span>
          </div>
          <span class="auto-badge">
            <iconify-icon icon="ant-design:thunderbolt-outlined" width="13"></iconify-icon>
            Captured automatically as you work
          </span>
        </div>
        {#if loadingMem}
          <div class="card-loading">Loading memories…</div>
        {:else if memFiles.length === 0}
          <div class="card-loading">No memory files yet.</div>
        {:else}
          {#each memFiles as f (f.name)}
            <div class="mem-row">
              <span class="mem-icon">
                <iconify-icon icon={iconForMemory(f)} width="14"></iconify-icon>
              </span>
              <div class="mem-content">
                <span class="mem-text mono">{f.name}</span>
                <span class="mem-meta">{f.source} · {fmtDate(f.updated_at)}</span>
              </div>
              <StatusTag status="default">{f.source}</StatusTag>
              <button class="forget-btn" onclick={() => forgetMemory(f.name)}>
                <iconify-icon icon="ant-design:close-circle-outlined" width="13"></iconify-icon>
                Forget
              </button>
            </div>
          {/each}
        {/if}
        <div class="card-footer">
          <span class="footer-hint">Octo writes these as you work together. To add or correct a memory, just tell it in a conversation.</span>
          <button class="btn-primary" onclick={() => openAssistantChat('Please update my memories')}>
            Have the Assistant Update This
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
h2 { margin: 0; font-size: 24px; font-weight: 600; color: #1F1F1F; }
p { margin: 0; font-size: 14px; color: rgba(0,0,0,0.65); }
.tabs { display: flex; align-items: center; gap: 24px; border-bottom: 1px solid #EEEFF1; }
.tab {
  padding: 0 2px 10px; margin-bottom: -1px; cursor: pointer;
  font-size: 14px; color: rgba(0,0,0,0.65);
  border-bottom: 2px solid transparent;
}
.tab.active { font-weight: 600; color: #1677FF; border-bottom-color: #1677FF; }
.tab:hover:not(.active) { color: rgba(0,0,0,0.88); }
.section-card { background: #fff; border-radius: 16px; box-shadow: 0 8px 24px rgba(15,23,42,0.03); overflow: hidden; }
.card-header {
  display: flex; align-items: center; gap: 12px;
  padding: 16px 24px; border-bottom: 1px solid #F0F0F0;
  flex-wrap: wrap;
}
.card-title { font-size: 16px; font-weight: 600; color: #1F1F1F; }
.file-path { font-size: 12px; color: rgba(0,0,0,0.45); }
.card-body { padding: 20px 24px; font-size: 14px; line-height: 1.7; color: rgba(0,0,0,0.88); }
.file-content {
  margin: 0; white-space: pre-wrap; word-break: break-word;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 13px; line-height: 1.6; color: rgba(0,0,0,0.80);
}
.card-loading { padding: 24px; text-align: center; color: rgba(0,0,0,0.45); font-size: 14px; }
.card-footer {
  display: flex; align-items: center; gap: 16px;
  padding: 16px 24px; border-top: 1px dashed #EEEFF1;
}
.footer-hint { font-size: 13px; color: rgba(0,0,0,0.45); flex: 1; min-width: 0; }
.btn-primary { height: 32px; padding: 0 14px; border: none; background: #1677FF; border-radius: 6px; font-size: 13px; color: #fff; cursor: pointer; font-family: inherit; white-space: nowrap; }
.btn-primary:hover { background: #4096FF; }
.mem-count { font-size: 12px; color: rgba(0,0,0,0.45); }
.auto-badge { display: flex; align-items: center; gap: 6px; font-size: 12px; color: rgba(0,0,0,0.45); margin-left: auto; }
.mem-row {
  display: flex; align-items: flex-start; gap: 12px;
  padding: 14px 24px; border-bottom: 1px solid #F0F0F0; background: #fff;
}
.mem-row:hover { background: rgba(22,119,255,0.06); }
.mem-icon {
  width: 28px; height: 28px; flex: 0 0 28px; border-radius: 9999px;
  background: #E6F4FF; color: #1677FF; display: flex; align-items: center; justify-content: center;
  margin-top: 1px;
}
.mem-content { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 4px; }
.mem-text { font-size: 14px; line-height: 1.5; color: rgba(0,0,0,0.88); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.mem-meta { font-size: 12px; color: rgba(0,0,0,0.45); }
.forget-btn {
  flex: 0 0 auto; margin-top: 1px; height: 28px; padding: 0 10px;
  border: 1px solid #EEEFF1; background: #fff; border-radius: 6px;
  display: flex; align-items: center; gap: 6px; font-size: 12px;
  color: rgba(0,0,0,0.45); cursor: pointer; font-family: inherit;
}
.forget-btn:hover { border-color: #FF4D4F; color: #FF4D4F; }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
</style>
