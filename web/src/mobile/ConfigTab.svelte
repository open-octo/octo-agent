<script lang="ts">
  // Config tab: the server's capability inventory — skills, MCP servers,
  // workflows, memories, trash — as a root list drilling into per-kind
  // sections. Remote-control scope: skills/MCP get enable switches, the rest
  // are read-only inventories; installing/editing stays on desktop.
  import { onMount } from 'svelte'
  import { showToast } from '../lib/stores'
  import * as api from '../lib/api'
  import type { Skill, Workflow, McpServer, Memory } from '../lib/types'

  // The trash endpoint's real wire shape ({ files: [...] }); api.listTrash's
  // declared RecallFile[] doesn't match it (the desktop view casts around the
  // same gap, see FileRecallView.svelte).
  interface TrashEntry {
    id: string
    original: string
    deleted_at: string
    size: number
    orphan: boolean
  }

  type Section = 'root' | 'skills' | 'mcp' | 'workflows' | 'memory' | 'trash'
  let section = $state<Section>('root')

  let loading = $state(true)
  let loadError = $state(false)
  let skills = $state<Skill[]>([])
  let mcp = $state<McpServer[]>([])
  let workflows = $state<Workflow[]>([])
  let memories = $state<Memory[]>([])
  let trash = $state<TrashEntry[]>([])

  async function load() {
    loading = true
    loadError = false
    const [sk, mc, wf, me, tr] = await Promise.all([
      api.listSkills().catch(() => null),
      api.listMcpServers().catch(() => null),
      api.listWorkflowsView().catch(() => null),
      api.getMemories().catch(() => null),
      api.listTrash().catch(() => null),
    ])
    if (sk) skills = sk
    if (mc) mcp = mc.servers ?? []
    if (wf) workflows = wf
    if (me) memories = me
    if (tr) trash = ((tr as any).files ?? tr ?? []) as TrashEntry[]
    loadError = !sk && !mc && !wf && !me && !tr
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
      showToast(e?.message ?? '更新技能失败', 'error')
    } finally {
      togglingName = null
    }
  }
  async function toggleMcp(m: McpServer) {
    if (togglingName) return
    togglingName = m.name
    const next = !m.enabled
    try {
      await api.toggleMcpServer(m.name, next)
      // Enable/disable changes connection state server-side; refetch for the
      // real status instead of guessing the tag locally.
      const d = await api.listMcpServers().catch(() => null)
      if (d) mcp = d.servers ?? []
      else mcp = mcp.map(r => (r.name === m.name ? { ...r, enabled: next } : r))
    } catch (e: any) {
      showToast(e?.message ?? '更新 MCP 失败', 'error')
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

  const groups = $derived([
    { key: 'skills' as Section, label: '技能', count: `${skills.filter(s => s.enabled).length}/${skills.length} 启用` },
    { key: 'mcp' as Section, label: 'MCP 服务器', count: `${mcp.filter(m => m.enabled).length}/${mcp.length} 启用` },
    { key: 'workflows' as Section, label: '工作流', count: `${workflows.length} 个` },
    { key: 'memory' as Section, label: '记忆', count: `${memories.length} 条` },
    { key: 'trash' as Section, label: '回收站', count: `${trash.length} 项` },
  ])
  const titles: Record<Section, string> = {
    root: '配置', skills: '技能', mcp: 'MCP 服务器', workflows: '工作流', memory: '记忆', trash: '回收站',
  }
</script>

<header class="head">
  {#if section !== 'root'}
    <button class="back" aria-label="返回" onclick={() => (section = 'root')}>
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2"><path d="M15 18l-6-6 6-6"/></svg>
    </button>
  {/if}
  <h1>{titles[section]}</h1>
</header>

<div class="scroll">
  {#if loading}
    <div class="empty">加载中…</div>
  {:else if section === 'root'}
    {#if loadError}
      <button class="empty retry" onclick={load}>加载失败 · 点击重试</button>
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
      <p class="hint">安装、编辑与删除请在桌面端操作</p>
    {/if}
  {:else if section === 'skills'}
    {#if skills.length === 0}<div class="empty">没有技能</div>{:else}
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
    {#if mcp.length === 0}<div class="empty">没有 MCP 服务器</div>{:else}
      <div class="card group">
        {#each mcp as m (m.name)}
          <div class="row">
            <div class="names">
              <span class="rlabel">
                <span class="dot" class:ok={m.tagStatus === 'success'} class:bad={m.tagStatus === 'error'}></span>
                {m.name}
              </span>
              <span class="sub">{m.transport} · {m.tools} 个工具 · {m.tagLabel}</span>
            </div>
            <button class="switch" class:on={m.enabled} role="switch" aria-checked={m.enabled}
              aria-label={m.name} disabled={togglingName !== null} onclick={() => toggleMcp(m)}
            ><span class="knob"></span></button>
          </div>
        {/each}
      </div>
    {/if}
  {:else if section === 'workflows'}
    {#if workflows.length === 0}<div class="empty">没有工作流</div>{:else}
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
    {#if memories.length === 0}<div class="empty">还没有记忆</div>{:else}
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
    {#if trash.length === 0}<div class="empty">回收站是空的</div>{:else}
      <div class="card group">
        {#each trash as f (f.id)}
          <div class="row">
            <div class="names">
              <span class="rlabel">{f.original.split('/').pop()}</span>
              <span class="sub">{fmtBytes(f.size)} · {fmtDate(f.deleted_at)}{f.orphan ? ' · 原目录已不存在' : ''}</span>
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
