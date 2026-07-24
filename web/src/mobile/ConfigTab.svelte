<script lang="ts">
  // Config tab: the server's capability inventory — skills, MCP servers,
  // workflows, memories, trash — as a root list drilling into per-kind
  // sections. Remote-control scope: skills/MCP get enable switches, the rest
  // are read-only inventories; installing/editing stays on desktop.
  import { onMount } from 'svelte'
  import { showToast } from '../lib/stores'
  import * as api from '../lib/api'
  import { t, tr } from '../lib/i18n'
  import type { Skill, Workflow, Memory } from '../lib/types'

  // The trash endpoint's real wire shape ({ files: [...] }); api.listTrash's
  // declared RecallFile[] doesn't match it (the desktop view casts around the
  // same gap, see FileRecallView.svelte).
  interface TrashEntry {
    id: string
    original: string
    deleted_at: string
    size: number
    orphan: boolean
    label?: string
  }

  // The MCP endpoint's real wire shape (mcpServerInfo in
  // internal/server/mcp_handlers.go): `disabled` + `status`, not the
  // enabled/tagStatus/tagLabel of the stale types.ts McpServer — the desktop
  // view (McpView.svelte) casts around the same gap and derives locally.
  interface McpInfo {
    name: string
    transport: string
    disabled: boolean
    status: 'connected' | 'error' | 'disabled' | 'invalid' | 'disconnected'
    tools: number
  }
  const mcpStatusKey: Record<McpInfo['status'], string> = {
    connected: 'm.mcp_connected', error: 'm.mcp_error', disabled: 'm.mcp_disabled',
    invalid: 'm.mcp_invalid', disconnected: 'm.mcp_disconnected',
  }

  type Section = 'root' | 'skills' | 'mcp' | 'workflows' | 'memory' | 'trash'
  let section = $state<Section>('root')

  let loading = $state(true)
  let loadError = $state(false)
  let skills = $state<Skill[]>([])
  let mcp = $state<McpInfo[]>([])
  let workflows = $state<Workflow[]>([])
  let memories = $state<Memory[]>([])
  let trash = $state<TrashEntry[]>([])

  async function load() {
    loading = true
    loadError = false
    const [sk, mc, wf, me, trs] = await Promise.all([
      api.listSkills().catch(() => null),
      api.listMcpServers().catch(() => null),
      api.listWorkflowsView().catch(() => null),
      api.getMemories().catch(() => null),
      api.listTrash().catch(() => null),
    ])
    if (sk) skills = sk
    if (mc) mcp = ((mc.servers ?? []) as unknown[]) as McpInfo[]
    if (wf) workflows = wf
    if (me) memories = me
    if (trs) trash = ((trs as any).files ?? trs ?? []) as TrashEntry[]
    const failed = [sk, mc, wf, me, trs].filter(r => !r).length
    loadError = failed === 5
    if (failed > 0 && failed < 5) showToast(tr('m.partial_load_fail'), 'error')
    loading = false
  }
  onMount(load)

  let togglingName = $state<string | null>(null)
  async function toggleSkill(s: Skill) {
    if (togglingName) return
    togglingName = s.name
    const next = !s.enabled
    try {
      await api.toggleSkill(s.name, next)
      skills = skills.map(r => (r.name === s.name ? { ...r, enabled: next } : r))
    } catch (e: any) {
      showToast(e?.message ?? tr('m.skill_update_fail'), 'error')
    } finally {
      togglingName = null
    }
  }
  async function toggleMcp(m: McpInfo) {
    if (togglingName) return
    togglingName = m.name
    try {
      await api.toggleMcpServer(m.name, m.disabled)
      // Enable/disable changes connection state server-side; refetch for the
      // real status instead of guessing it locally.
      const d = await api.listMcpServers().catch(() => null)
      if (d) mcp = ((d.servers ?? []) as unknown[]) as McpInfo[]
      else mcp = mcp.map(r => (r.name === m.name ? { ...r, disabled: !m.disabled } : r))
    } catch (e: any) {
      showToast(e?.message ?? tr('m.mcp_update_fail'), 'error')
    } finally {
      togglingName = null
    }
  }

  function fmtDate(iso: string): string {
    if (!iso) return '—'
    try {
      return new Intl.DateTimeFormat('zh-CN', {
        month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit',
      }).format(new Date(iso))
    } catch {
      return iso
    }
  }

  function fmtBytes(n: number): string {
    if (!n) return '0 B'
    if (n < 1024) return `${n} B`
    if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
    return `${(n / 1024 / 1024).toFixed(1)} MB`
  }

  const nEnabled = (a: number, b: number) =>
    $t('m.n_enabled').replace('{a}', String(a)).replace('{b}', String(b))
  const groups = $derived([
    { key: 'skills' as Section, label: $t('m.skills'), count: nEnabled(skills.filter(s => s.enabled).length, skills.length) },
    { key: 'mcp' as Section, label: $t('m.mcp'), count: nEnabled(mcp.filter(m => !m.disabled).length, mcp.length) },
    { key: 'workflows' as Section, label: $t('m.workflows'), count: $t('m.count_wf').replace('{n}', String(workflows.length)) },
    { key: 'memory' as Section, label: $t('m.memory'), count: $t('m.count_mem').replace('{n}', String(memories.length)) },
    { key: 'trash' as Section, label: $t('m.trash'), count: $t('m.count_trash').replace('{n}', String(trash.length)) },
  ])
  const titleKeys: Record<Section, string> = {
    root: 'm.tab_config', skills: 'm.skills', mcp: 'm.mcp', workflows: 'm.workflows', memory: 'm.memory', trash: 'm.trash',
  }
</script>

<header class="head">
  {#if section !== 'root'}
    <button class="back" aria-label={$t('m.back')} onclick={() => (section = 'root')}>
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2"><path d="M15 18l-6-6 6-6"/></svg>
    </button>
  {/if}
  <h1>{$t(titleKeys[section])}</h1>
</header>

<div class="scroll">
  {#if loading}
    <div class="empty">{$t('m.loading')}</div>
  {:else if section === 'root'}
    {#if loadError}
      <button class="empty retry" onclick={load}>{$t('m.load_retry')}</button>
    {:else}
      <div class="card group">
        {#each groups as g (g.key)}
          <button class="row nav" onclick={() => (section = g.key)}>
            <span class="rlabel">{g.label}</span>
            <span class="rval">{g.count}</span>
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="var(--m-text-4)" stroke-width="2"><path d="M9 6l6 6-6 6"/></svg>
          </button>
        {/each}
      </div>
      <p class="hint">{$t('m.desktop_hint')}</p>
    {/if}
  {:else if section === 'skills'}
    {#if skills.length === 0}<div class="empty">{$t('m.no_skills')}</div>{:else}
      <div class="card group">
        {#each skills as s (s.name)}
          <div class="row">
            <div class="names">
              <span class="rlabel">{s.name} <span class="tag">{s.tagLabel}</span></span>
              {#if s.desc}<span class="sub">{s.desc}</span>{/if}
            </div>
            <button class="switch" class:on={s.enabled} role="switch" aria-checked={s.enabled}
              aria-label={s.name} disabled={togglingName !== null} onclick={() => toggleSkill(s)}
            ><span class="knob"></span></button>
          </div>
        {/each}
      </div>
    {/if}
  {:else if section === 'mcp'}
    {#if mcp.length === 0}<div class="empty">{$t('m.no_mcp')}</div>{:else}
      <div class="card group">
        {#each mcp as m (m.name)}
          <div class="row">
            <div class="names">
              <span class="rlabel">
                <span class="dot" class:ok={m.status === 'connected'} class:bad={m.status === 'error' || m.status === 'invalid'}></span>
                {m.name}
              </span>
              <span class="sub">{m.transport || '—'} · {$t('m.n_tools').replace('{n}', String(m.tools))} · {mcpStatusKey[m.status] ? $t(mcpStatusKey[m.status]) : m.status}</span>
            </div>
            <button class="switch" class:on={!m.disabled} role="switch" aria-checked={!m.disabled}
              aria-label={m.name} disabled={togglingName !== null} onclick={() => toggleMcp(m)}
            ><span class="knob"></span></button>
          </div>
        {/each}
      </div>
    {/if}
  {:else if section === 'workflows'}
    {#if workflows.length === 0}<div class="empty">{$t('m.no_wf')}</div>{:else}
      <div class="card group">
        {#each workflows as w (w.name)}
          <div class="row">
            <div class="names">
              <span class="rlabel">{w.name} <span class="tag">{w.tagLabel}</span></span>
              {#if w.desc}<span class="sub">{w.desc}</span>{/if}
            </div>
          </div>
        {/each}
      </div>
    {/if}
  {:else if section === 'memory'}
    {#if memories.length === 0}<div class="empty">{$t('m.no_mem')}</div>{:else}
      <div class="card group">
        {#each memories as m (m.source + m.name)}
          <div class="row">
            <div class="names">
              <span class="rlabel">{m.name}</span>
              <span class="sub">{m.source} · {fmtBytes(m.size)}</span>
            </div>
          </div>
        {/each}
      </div>
    {/if}
  {:else if section === 'trash'}
    {#if trash.length === 0}<div class="empty">{$t('m.trash_empty')}</div>{:else}
      <div class="card group">
        {#each trash as f (f.id)}
          <div class="row">
            <div class="names">
              <span class="rlabel">{f.label || f.original.split('/').pop()}</span>
              <span class="sub">{fmtBytes(f.size)} · {fmtDate(f.deleted_at)}{f.orphan ? ` · ${$t('m.orphan_dir')}` : ''}</span>
            </div>
          </div>
        {/each}
      </div>
    {/if}
  {/if}
</div>

<style>
  .head { flex: none; display: flex; align-items: center; gap: 10px; padding: 8px 18px 12px; }
  .head h1 { margin: 0; font-size: 24px; font-weight: 600; color: var(--m-text-strong); }
  .back {
    width: 34px; height: 34px; border-radius: 50%; border: none; flex: none;
    background: var(--m-surface-2); color: var(--m-text); cursor: pointer;
    display: flex; align-items: center; justify-content: center;
  }
  .scroll { flex: 1; min-height: 0; overflow-y: auto; -webkit-overflow-scrolling: touch; padding: 0 16px 20px; }

  .card { background: var(--m-surface); border-radius: 14px; box-shadow: var(--m-shadow-card); }
  .group { overflow: hidden; margin-bottom: 14px; }
  .row { display: flex; align-items: center; gap: 12px; padding: 13px 16px; width: 100%; }
  .row + .row { border-top: 1px solid var(--m-divider); }
  .row.nav { background: none; border-left: none; border-right: none; border-bottom: none; font-family: inherit; cursor: pointer; text-align: left; min-height: 48px; }
  .names { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 2px; }
  .rlabel { flex: 1; font-size: 14.5px; color: var(--m-text); display: flex; align-items: center; gap: 6px; min-width: 0; }
  .rval { flex: none; font-size: 12.5px; color: var(--m-text-3); }
  .sub { font-size: 12px; color: var(--m-text-3); overflow: hidden; text-overflow: ellipsis; display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical; }
  .tag { flex: none; font-size: 10px; padding: 1px 6px; border-radius: 9999px; background: var(--m-surface-2); color: var(--m-text-3); }
  .dot { flex: none; width: 7px; height: 7px; border-radius: 50%; background: var(--m-text-4); }
  .dot.ok { background: var(--m-success); }
  .dot.bad { background: var(--m-error); }
  .hint { margin: 2px 4px; font-size: 12px; color: var(--m-text-4); text-align: center; }
  .empty { padding: 40px 16px; text-align: center; font-size: 13px; color: var(--m-text-3); }
  .retry { display: block; width: 100%; background: none; border: none; font-family: inherit; color: var(--m-accent); cursor: pointer; }

  .switch {
    flex: none; width: 42px; height: 24px; border-radius: 9999px; border: none;
    padding: 0; position: relative; cursor: pointer; background: var(--m-border);
    transition: background .15s;
  }
  .switch.on { background: var(--m-accent); }
  .switch .knob {
    position: absolute; top: 2px; left: 2px; width: 20px; height: 20px; border-radius: 50%;
    background: #fff; box-shadow: 0 1px 2px rgba(0,0,0,.2); transition: left .15s;
  }
  .switch.on .knob { left: 20px; }
</style>
