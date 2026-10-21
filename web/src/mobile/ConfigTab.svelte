<script lang="ts" module>
  import type { LightApp } from '../lib/api'
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

  // Outlives the component, which MobileApp unmounts on every tab switch:
  // coming back shows the last inventory at once and refreshes underneath,
  // rather than a loading screen for six round trips that may cross a tunnel.
  let cached: {
    apps: LightApp[]
    skills: Skill[]
    mcp: McpInfo[]
    workflows: Workflow[]
    memories: Memory[]
    trash: TrashEntry[]
  } | null = null
</script>

<script lang="ts">
  // Me tab: the server's capability inventory — Light Apps, skills, MCP
  // servers, workflows, memories — and trash as a root list drilling into
  // per-kind sections, followed by the app's own preferences and about. Remote-control scope: Light Apps open full-screen,
  // skills/MCP get enable switches plus the desktop's create / edit / use /
  // import / delete (agentic where the desktop is: a fresh chat runs the
  // skill-creator or mcp-creator skill); memories can be read, forgotten or
  // handed to the assistant to update, trashed files restored or deleted (the
  // desktop Profile / File Recall actions); workflows are read-only.
  import { onMount } from 'svelte'
  import { showToast, prependSession } from '../lib/stores'
  import { renderMarkdown } from '../lib/markdown'
  import * as api from '../lib/api'
  import { t, tr } from '../lib/i18n'
  import PageViewer from './PageViewer.svelte'
  import Prefs from './Prefs.svelte'
  import { pageSrc } from './pageURL'

  let { onAgentSession }: {
    // A fresh session plus the first message to send once its chat is open.
    onAgentSession: (id: string, prompt: string) => void
  } = $props()

  const mcpStatusKey: Record<McpInfo['status'], string> = {
    connected: 'm.mcp_connected', error: 'm.mcp_error', disabled: 'm.mcp_disabled',
    invalid: 'm.mcp_invalid', disconnected: 'm.mcp_disconnected',
  }

  type Section = 'root' | 'apps' | 'skills' | 'mcp' | 'workflows' | 'memory' | 'trash'
  let section = $state<Section>('root')

  let loading = $state(cached === null)
  let loadError = $state(false)
  let skills = $state<Skill[]>(cached?.skills ?? [])
  let mcp = $state<McpInfo[]>(cached?.mcp ?? [])
  let workflows = $state<Workflow[]>(cached?.workflows ?? [])
  let memories = $state<Memory[]>(cached?.memories ?? [])
  let trash = $state<TrashEntry[]>(cached?.trash ?? [])
  let apps = $state<api.LightApp[]>(cached?.apps ?? [])
  $effect(() => {
    if (!loading && !loadError) cached = { apps, skills, mcp, workflows, memories, trash }
  })

  async function load() {
    loading = cached === null
    loadError = false
    const [la, sk, mc, wf, me, trs] = await Promise.all([
      api.listLightApps().catch(() => null),
      api.listSkills().catch(() => null),
      api.listMcpServers().catch(() => null),
      api.listWorkflowsView().catch(() => null),
      api.getMemories().catch(() => null),
      api.listTrash().catch(() => null),
    ])
    if (la) apps = la
    if (sk) skills = sk
    if (mc) mcp = ((mc.servers ?? []) as unknown[]) as McpInfo[]
    if (wf) workflows = wf
    if (me) memories = me
    if (trs) trash = ((trs as any).files ?? trs ?? []) as TrashEntry[]
    const results = [la, sk, mc, wf, me, trs]
    const failed = results.filter(r => !r).length
    // A failed background refresh keeps the cached inventory on screen.
    loadError = failed === results.length && cached === null
    if (failed > 0 && failed < results.length) showToast(tr('m.partial_load_fail'), 'error')
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

  // ── memories: read on tap, forget, or hand to the assistant ───────────────
  let openMem = $state<string | null>(null)
  // null while loading; a failed read is dropped so reopening retries.
  let memBody = $state<Record<string, string | null>>({})
  async function toggleMem(m: Memory) {
    const key = m.source + m.name
    openMem = openMem === key ? null : key
    if (openMem !== key || memBody[key] !== undefined) return
    memBody = { ...memBody, [key]: null }
    try {
      const d = await api.getMemory(m.name, m.source)
      memBody = { ...memBody, [key]: d.content ?? '' }
    } catch {
      const { [key]: _, ...rest } = memBody
      memBody = rest
      if (openMem === key) openMem = null
      showToast(tr('m.load_failed'), 'error')
    }
  }
  // One pending confirmation at a time across both lists, keyed by row.
  let confirming = $state<string | null>(null)
  // The row whose request is in flight; every row's destructive actions wait
  // for it, so a second delete can't re-enable the first one's buttons.
  let busyRow = $state<string | null>(null)
  async function forgetMem(m: Memory) {
    const key = m.source + m.name
    confirming = null
    busyRow = key
    try {
      await api.deleteMemory(m.name, m.source)
      memories = memories.filter(x => x.source + x.name !== key)
      showToast(tr('m.memory_forgotten'))
    } catch (e: any) {
      showToast(e?.message ?? String(e), 'error')
    } finally {
      busyRow = null
    }
  }
  // The desktop's openAgentSession on the phone's own navigation: a fresh,
  // named session, opened in the chat with its first message.
  let opening = $state(false)
  async function openAgentic(prompt: string, name: string) {
    if (opening) return
    opening = true
    try {
      const sess = await api.createSession({ source: 'manual', name })
      prependSession(sess)
      onAgentSession(sess.id, prompt)
    } catch (e: any) {
      showToast(e?.message ?? tr('m.create_fail'), 'error')
    } finally {
      opening = false
    }
  }
  // The desktop Profile page's "update" action: the same ask.
  const reviewMemories = () =>
    openAgentic('Review my saved memories and help me update or remove any that are stale.', tr('profile.session_memories'))

  // ── skills and MCP servers: the desktop pages' actions ────────────────────
  // A row's actions show when its name is tapped; one row open at a time.
  let openRow = $state<string | null>(null)
  const toggleRow = (key: string) => {
    openRow = openRow === key ? null : key
    confirming = null
  }
  const createSkill = () => openAgentic('/skill-creator', tr('skills.session_new'))
  const useSkill = (name: string) => openAgentic(`/${name}`, name)
  const editSkill = (name: string) =>
    openAgentic(`/skill-creator Edit the existing "${name}" skill.`, tr('skills.session_edit').replace('{name}', name))
  const setupMcp = () => openAgentic('/mcp-creator', tr('mcp.session_setup'))
  const editMcp = (name: string) =>
    openAgentic(`/mcp-creator Edit the existing MCP server "${name}" — find its entry, show me the current config, then ask what I want changed.`, tr('mcp.session_edit').replace('{name}', name))

  async function deleteSkill(name: string) {
    confirming = null
    busyRow = `skill:${name}`
    try {
      await api.deleteSkill(name)
      skills = skills.filter(s => s.name !== name)
      showToast(tr('skills.toast_deleted').replace('{name}', name))
    } catch (e: any) {
      showToast(e?.message ?? String(e), 'error')
    } finally {
      busyRow = null
    }
  }
  async function deleteMcp(name: string) {
    confirming = null
    busyRow = `mcp:${name}`
    try {
      await api.deleteMcpServer(name)
      mcp = mcp.filter(m => m.name !== name)
      showToast(tr('mcp.toast_removed'))
    } catch {
      showToast(tr('mcp.toast_delete_fail'), 'error')
    } finally {
      busyRow = null
    }
  }

  // Import a skill from a GitHub link / owner/repo / server path, or a .zip
  // uploaded first — the desktop's import dialog, inline.
  let importOpen = $state(false)
  let importSource = $state('')
  let importBusy = $state(false)
  let importReplace = $state(false)
  let zipInput: HTMLInputElement | undefined = $state()
  async function pickZip(e: Event) {
    const input = e.currentTarget as HTMLInputElement
    const file = input.files?.[0]
    input.value = ''
    if (!file) return
    importBusy = true
    try {
      importSource = await api.uploadFile(file)
      importReplace = false
    } catch (err: any) {
      showToast(err?.message ?? String(err), 'error')
    } finally {
      importBusy = false
    }
  }
  async function doImport(force = false) {
    const source = importSource.trim()
    if (!source || importBusy) return
    importBusy = true
    importReplace = false
    try {
      const r = await api.importSkill(source, force)
      if (r.conflict && !force) {
        importReplace = true
        return
      }
      if (!r.ok) {
        showToast(tr('skills.import_error') + (r.error ?? 'unknown'), 'error')
        return
      }
      const fresh = await api.listSkills().catch(() => null)
      if (fresh) skills = fresh as Skill[]
      showToast(tr('skills.toast_imported').replace('{name}', r.name ?? ''))
      importOpen = false
      importSource = ''
    } catch (err: any) {
      showToast(tr('skills.import_error') + (err?.message ?? String(err)), 'error')
    } finally {
      importBusy = false
    }
  }

  // ── trash: restore (backing up whatever now sits at the path) or delete ───
  // A restore that finds the original path taken asks first, then backs the
  // current file up into the trash and restores — the desktop's lossless way.
  async function restore(f: TrashEntry, backup = false) {
    confirming = null
    busyRow = f.id
    try {
      const res = await api.restoreTrash(f.id, backup ? 'backup' : undefined)
      if (!res.ok) {
        confirming = `restore:${f.id}`
        return
      }
      trash = trash.filter(x => x.id !== f.id)
      showToast(tr('files.toast_restored'), 'success')
      // The backed-up file is a new trash entry.
      if (res.backedUpExisting) load()
    } catch (e: any) {
      showToast(e?.message ?? String(e), 'error')
    } finally {
      busyRow = null
    }
  }
  async function deleteForGood(f: TrashEntry) {
    confirming = null
    busyRow = f.id
    try {
      await api.deleteTrashItem(f.id)
      trash = trash.filter(x => x.id !== f.id)
      showToast(tr('files.toast_deleted'), 'success')
    } catch (e: any) {
      showToast(e?.message ?? String(e), 'error')
    } finally {
      busyRow = null
    }
  }

  // The app being viewed. Its page loads from a token URL the server grants:
  // the app's frame carries no access-key cookie (pageURL.ts).
  let viewing = $state<{ slug: string; name: string; src: string; failed: boolean } | null>(null)
  async function openApp(app: api.LightApp) {
    viewing = { slug: app.slug, name: app.name || app.slug, src: '', failed: false }
    try {
      const { url } = await api.grantLightApp(app.slug)
      if (viewing?.slug === app.slug) viewing = { ...viewing, src: pageSrc(url) }
    } catch {
      if (viewing?.slug === app.slug) viewing = { ...viewing, failed: true }
    }
  }

  const nEnabled = (a: number, b: number) =>
    $t('m.n_enabled').replace('{a}', String(a)).replace('{b}', String(b))
  const groups = $derived([
    { key: 'apps' as Section, label: $t('m.light_apps'), count: $t('m.count_apps').replace('{n}', String(apps.length)) },
    { key: 'skills' as Section, label: $t('m.skills'), count: nEnabled(skills.filter(s => s.enabled).length, skills.length) },
    { key: 'mcp' as Section, label: $t('m.mcp'), count: nEnabled(mcp.filter(m => !m.disabled).length, mcp.length) },
    { key: 'workflows' as Section, label: $t('m.workflows'), count: $t('m.count_wf').replace('{n}', String(workflows.length)) },
    { key: 'memory' as Section, label: $t('m.memory'), count: $t('m.count_mem').replace('{n}', String(memories.length)) },
  ])
  const trashRow = $derived({ key: 'trash' as Section, label: $t('m.trash'), count: $t('m.count_trash').replace('{n}', String(trash.length)) })
  const titleKeys: Record<Section, string> = {
    root: 'm.tab_me', apps: 'm.light_apps', skills: 'm.skills', mcp: 'm.mcp', workflows: 'm.workflows', memory: 'm.memory', trash: 'm.trash',
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
  {#if section === 'root'}
    <!-- Preferences render regardless of the inventory fetch: they are the
         app's own and must stay reachable when the server is not. -->
    {#if loading}
      <div class="empty">{$t('m.loading')}</div>
    {:else if loadError}
      <button class="empty retry" onclick={load}>{$t('m.load_retry')}</button>
    {:else}
      <p class="lbl">{$t('m.capabilities')}</p>
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
      <div class="card group">
        <button class="row nav" onclick={() => (section = trashRow.key)}>
          <span class="rlabel">{trashRow.label}</span>
          <span class="rval">{trashRow.count}</span>
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="var(--m-text-4)" stroke-width="2"><path d="M9 6l6 6-6 6"/></svg>
        </button>
      </div>
    {/if}
    <Prefs />
  {:else if section === 'apps'}
    {#if apps.length === 0}<div class="empty">{$t('m.no_apps')}</div>{:else}
      <div class="card group">
        {#each apps as app (app.slug)}
          <button class="row nav" onclick={() => openApp(app)}>
            <span class="appicon">{app.icon || '📦'}</span>
            <div class="names">
              <span class="rlabel">{app.name || app.slug}</span>
              {#if app.description}<span class="sub">{app.description}</span>{/if}
            </div>
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="var(--m-text-4)" stroke-width="2"><path d="M9 6l6 6-6 6"/></svg>
          </button>
        {/each}
      </div>
    {/if}
  {:else if section === 'skills'}
    <div class="acts top">
      <button class="pill" onclick={() => { importOpen = !importOpen; importReplace = false }}>{$t('skills.import')}</button>
      <button class="pill strong" disabled={opening} onclick={createSkill}>{$t('skills.create')}</button>
    </div>
    {#if importOpen}
      <div class="card import">
        <p class="ask">{$t('skills.import_hint')}</p>
        <input class="field" bind:value={importSource} oninput={() => (importReplace = false)} placeholder={$t('skills.import_placeholder')} aria-label={$t('skills.import_title')} />
        <input type="file" accept=".zip" hidden bind:this={zipInput} onchange={pickZip} />
        {#if importReplace}
          <p class="ask">{$t('skills.import_confirm_replace')}</p>
          <div class="acts">
            <button class="pill" onclick={() => (importReplace = false)}>{$t('m.cancel')}</button>
            <button class="pill danger" disabled={importBusy} onclick={() => doImport(true)}>{$t('skills.import_install')}</button>
          </div>
        {:else}
          <div class="acts">
            <button class="pill" disabled={importBusy} onclick={() => zipInput?.click()}>{$t('skills.import_browse')}</button>
            <button class="pill strong" disabled={importBusy || !importSource.trim()} onclick={() => doImport()}>
              {importBusy ? $t('skills.import_installing') : $t('skills.import_install')}
            </button>
          </div>
        {/if}
      </div>
    {/if}
    {#if skills.length === 0}<div class="empty">{$t('m.no_skills')}</div>{:else}
      <div class="card group">
        {#each skills as s (s.name)}
          {@const key = `skill:${s.name}`}
          <div class="item">
            <div class="row">
              <button class="names tap" onclick={() => toggleRow(key)} aria-expanded={openRow === key}>
                <span class="rlabel">{s.name} <span class="tag">{s.tagLabel}</span></span>
                {#if s.desc}<span class="sub">{s.desc}</span>{/if}
              </button>
              <button class="switch" class:on={s.enabled} role="switch" aria-checked={s.enabled}
                aria-label={s.name} disabled={togglingName !== null} onclick={() => toggleSkill(s)}
              ><span class="knob"></span></button>
            </div>
            {#if openRow === key}
              <div class="detail">
                {#if confirming === key}
                  <p class="ask">{$t('skills.confirm_delete').replace('{name}', s.name)}</p>
                  <div class="acts">
                    <button class="pill" onclick={() => (confirming = null)}>{$t('m.cancel')}</button>
                    <button class="pill danger" onclick={() => deleteSkill(s.name)}>{$t('common.delete')}</button>
                  </div>
                {:else}
                  <div class="acts">
                    <!-- System and expert skills ship with octo; the server refuses to delete them. -->
                    {#if s.source === 'user'}
                      <button class="pill danger" disabled={busyRow !== null} onclick={() => (confirming = key)}>{$t('common.delete')}</button>
                    {/if}
                    <button class="pill" disabled={opening} onclick={() => editSkill(s.name)}>{$t('skills.edit_with_agent')}</button>
                    <button class="pill strong" disabled={opening || !s.enabled} onclick={() => useSkill(s.name)}>{$t('skills.use')}</button>
                  </div>
                {/if}
              </div>
            {/if}
          </div>
        {/each}
      </div>
    {/if}
  {:else if section === 'mcp'}
    <div class="acts top">
      <button class="pill strong" disabled={opening} onclick={setupMcp}>{$t('mcp.ai_setup')}</button>
    </div>
    {#if mcp.length === 0}<div class="empty">{$t('m.no_mcp')}</div>{:else}
      <div class="card group">
        {#each mcp as m (m.name)}
          {@const key = `mcp:${m.name}`}
          <div class="item">
            <div class="row">
              <button class="names tap" onclick={() => toggleRow(key)} aria-expanded={openRow === key}>
                <span class="rlabel">
                  <span class="dot" class:ok={m.status === 'connected'} class:bad={m.status === 'error' || m.status === 'invalid'}></span>
                  {m.name}
                </span>
                <span class="sub">{m.transport || '—'} · {$t('m.n_tools').replace('{n}', String(m.tools))} · {mcpStatusKey[m.status] ? $t(mcpStatusKey[m.status]) : m.status}</span>
              </button>
              <button class="switch" class:on={!m.disabled} role="switch" aria-checked={!m.disabled}
                aria-label={m.name} disabled={togglingName !== null} onclick={() => toggleMcp(m)}
              ><span class="knob"></span></button>
            </div>
            {#if openRow === key}
              <div class="detail">
                {#if confirming === key}
                  <p class="ask">{$t('mcp.confirm_delete').replace('{name}', m.name)}</p>
                  <div class="acts">
                    <button class="pill" onclick={() => (confirming = null)}>{$t('m.cancel')}</button>
                    <button class="pill danger" onclick={() => deleteMcp(m.name)}>{$t('common.delete')}</button>
                  </div>
                {:else}
                  <div class="acts">
                    <button class="pill danger" disabled={busyRow !== null} onclick={() => (confirming = key)}>{$t('common.delete')}</button>
                    <button class="pill strong" disabled={opening} onclick={() => editMcp(m.name)}>{$t('skills.edit_with_agent')}</button>
                  </div>
                {/if}
              </div>
            {/if}
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
          {@const key = m.source + m.name}
          <div class="item">
            <button class="row nav" onclick={() => toggleMem(m)} aria-expanded={openMem === key}>
              <div class="names">
                <span class="rlabel">{m.name}</span>
                <span class="sub">{m.source} · {fmtBytes(m.size)}</span>
              </div>
              <span class="chev" class:open={openMem === key}>›</span>
            </button>
            {#if openMem === key}
              <div class="detail">
                {#if memBody[key] == null}<div class="sub">{$t('m.loading')}</div>{:else}<div class="md">{@html renderMarkdown(memBody[key] ?? "")}</div>{/if}
                {#if confirming === `forget:${key}`}
                  <p class="ask">{$t('profile.confirm_forget').replace('{name}', m.name)}</p>
                  <div class="acts">
                    <button class="pill" onclick={() => (confirming = null)}>{$t('m.cancel')}</button>
                    <button class="pill danger" onclick={() => forgetMem(m)}>{$t('profile.forget')}</button>
                  </div>
                {:else}
                  <div class="acts">
                    <button class="pill danger" disabled={busyRow !== null} onclick={() => (confirming = `forget:${key}`)}>{$t('profile.forget')}</button>
                  </div>
                {/if}
              </div>
            {/if}
          </div>
        {/each}
      </div>
    {/if}
    <p class="hint">{$t('profile.memories_hint')}</p>
    <button class="wide-btn" disabled={opening} onclick={reviewMemories}>{$t('profile.update')}</button>
  {:else if section === 'trash'}
    {#if trash.length === 0}<div class="empty">{$t('m.trash_empty')}</div>{:else}
      <div class="card group">
        {#each trash as f (f.id)}
          <div class="item">
            <div class="row">
              <div class="names">
                <span class="rlabel">{f.label || f.original.split('/').pop()}</span>
                <span class="sub">{fmtBytes(f.size)} · {fmtDate(f.deleted_at)}{f.orphan ? ` · ${$t('m.orphan_dir')}` : ''}</span>
              </div>
            </div>
            <div class="detail">
              {#if confirming === `restore:${f.id}`}
                <p class="ask">{$t('files.confirm_restore_conflict').replace('{name}', f.original)}</p>
                <div class="acts">
                  <button class="pill" onclick={() => (confirming = null)}>{$t('m.cancel')}</button>
                  <button class="pill strong" onclick={() => restore(f, true)}>{$t('files.restore')}</button>
                </div>
              {:else if confirming === `delete:${f.id}`}
                <p class="ask">{$t('files.confirm_delete_one')}</p>
                <div class="acts">
                  <button class="pill" onclick={() => (confirming = null)}>{$t('m.cancel')}</button>
                  <button class="pill danger" onclick={() => deleteForGood(f)}>{$t('files.delete_perm')}</button>
                </div>
              {:else}
                <div class="acts">
                  <button class="pill danger" disabled={busyRow !== null} onclick={() => (confirming = `delete:${f.id}`)}>{$t('files.delete_perm')}</button>
                  <button class="pill strong" disabled={busyRow !== null} onclick={() => restore(f)}>{$t('files.restore')}</button>
                </div>
              {/if}
            </div>
          </div>
        {/each}
      </div>
    {/if}
  {/if}
</div>

{#if viewing}
  <PageViewer
    title={viewing.name}
    src={viewing.src}
    loading={!viewing.src && !viewing.failed}
    failed={viewing.failed}
    onClose={() => (viewing = null)}
  />
{/if}

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
  .lbl { margin: 2px 2px 8px; font: 600 12px/1 system-ui; letter-spacing: .5px; text-transform: uppercase; color: var(--m-text-3); }
  .row { display: flex; align-items: center; gap: 12px; padding: 13px 16px; width: 100%; }
  .row + .row, .item + .item { border-top: 1px solid var(--m-divider); }
  .item .row.nav { border-top: none; }
  .chev { flex: none; color: var(--m-text-4); font-size: 18px; transition: transform .15s; }
  .chev.open { transform: rotate(90deg); }
  .detail { padding: 0 16px 12px; }
  .md { font-size: 13px; line-height: 1.5; color: var(--m-text-2); overflow-wrap: anywhere; }
  .md :global(p) { margin: 0 0 6px; }
  .md :global(pre) { white-space: pre-wrap; }
  .ask { margin: 6px 0 0; font-size: 12.5px; color: var(--m-text-2); }
  .acts { display: flex; justify-content: flex-end; gap: 8px; margin-top: 8px; }
  .pill {
    min-height: 34px; padding: 6px 14px; border-radius: 9999px;
    border: 1px solid var(--m-border); background: none;
    font-size: 12.5px; color: var(--m-text-2); font-family: inherit; cursor: pointer;
  }
  .pill.danger { color: var(--m-error); }
  .pill.strong { color: var(--m-accent); }
  .pill:disabled { opacity: .45; cursor: default; }
  .acts.top { margin: 0 0 10px; }
  .names.tap { background: none; border: none; padding: 0; font: inherit; text-align: left; cursor: pointer; }
  .card.import { padding: 12px 16px; margin-bottom: 12px; }
  .field {
    width: 100%; box-sizing: border-box; margin-top: 8px; padding: 9px 12px;
    border: 1px solid var(--m-border); border-radius: 10px; background: var(--m-bg);
    color: var(--m-text); font: inherit; font-size: 16px; outline: none;
  }
  .hint { margin: 12px 4px 0; font-size: 12px; color: var(--m-text-3); }
  .wide-btn {
    display: block; width: 100%; margin-top: 10px; min-height: 44px; border-radius: 12px;
    border: 1px solid var(--m-border); background: var(--m-surface); color: var(--m-accent);
    font: inherit; font-size: 14px; cursor: pointer;
  }
  .wide-btn:disabled { opacity: .5; }
  .row.nav { background: none; border-left: none; border-right: none; border-bottom: none; font-family: inherit; cursor: pointer; text-align: left; min-height: 48px; }
  .row.nav:first-child { border-top: none; }
  .names { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 2px; }
  .rlabel { flex: 1; font-size: 14.5px; color: var(--m-text); display: flex; align-items: center; gap: 6px; min-width: 0; }
  .rval { flex: none; font-size: 12.5px; color: var(--m-text-3); }
  .sub { font-size: 12px; color: var(--m-text-3); overflow: hidden; text-overflow: ellipsis; display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical; }
  .tag { flex: none; font-size: 10px; padding: 1px 6px; border-radius: 9999px; background: var(--m-surface-2); color: var(--m-text-3); }
  .appicon { flex: none; width: 28px; text-align: center; font-size: 20px; }
  .dot { flex: none; width: 7px; height: 7px; border-radius: 50%; background: var(--m-text-4); }
  .dot.ok { background: var(--m-success); }
  .dot.bad { background: var(--m-error); }
  .hint { margin: -4px 4px 18px; font-size: 12px; color: var(--m-text-4); text-align: center; }
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
