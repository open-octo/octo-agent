<script lang="ts">
  import { get } from 'svelte/store'
  import { onMount } from 'svelte'
  import {
    running, activeSessionId, chatStreaming, sessions,
    chatContextUsage, chatWorkingDir, chatPermMode, chatReasoningEffort, chatShowReasoning, showToast,
  } from '../../lib/stores'
  import { ws } from '../../lib/ws'
  import * as api from '../../lib/api'
  import { t } from '../../lib/i18n'
  import StatusTag from '../ui/StatusTag.svelte'
  import type { McpServerDetail, McpTool } from '../../lib/types'
  import { getMcpServer } from '../../lib/api'

  let { onSend }: { onSend?: (text: string, files?: any[]) => void } = $props()

  let text = $state('')
  let textareaEl = $state<HTMLTextAreaElement | null>(null)
  let fileInputEl = $state<HTMLInputElement | null>(null)
  let attachments = $state<{ name: string; data_url: string; mime_type: string }[]>([])
  let dragOver = $state(false)

  // Called by ChatView when the user clicks "edit" on a prior message — loads
  // that text back into the composer for resend.
  export function setText(v: string) {
    text = v
    queueMicrotask(() => textareaEl?.focus())
  }

  function openAttach() {
    fileInputEl?.click()
  }

  function addAttachment(file: File, fallbackName?: string) {
    const reader = new FileReader()
    reader.onload = () => {
      attachments = [...attachments, { name: file.name || fallbackName || 'attachment', data_url: String(reader.result), mime_type: file.type }]
    }
    reader.readAsDataURL(file)
  }

  function onFilesPicked(e: Event) {
    const input = e.target as HTMLInputElement
    const files = Array.from(input.files ?? [])
    for (const f of files) addAttachment(f)
    input.value = ''
  }

  // Paste images from clipboard into the composer.
  function onPaste(e: ClipboardEvent) {
    const items = Array.from(e.clipboardData?.items ?? [])
    const imageItems = items.filter(it => it.kind === 'file' && it.type.startsWith('image/'))
    if (imageItems.length === 0) return
    e.preventDefault()
    for (const it of imageItems) {
      const f = it.getAsFile()
      if (!f) continue
      addAttachment(f, 'pasted-image')
    }
  }

  // Drop files onto the composer input card.
  function onDragOver(e: DragEvent) {
    e.preventDefault()
    dragOver = true
  }

  function onDragLeave(e: DragEvent) {
    const card = e.currentTarget as HTMLElement
    if (!card.contains(e.relatedTarget as Node)) dragOver = false
  }

  function onDrop(e: DragEvent) {
    e.preventDefault()
    dragOver = false
    const files = Array.from(e.dataTransfer?.files ?? [])
    for (const f of files) {
      if (f.type.startsWith('image/')) addAttachment(f)
    }
  }

  function removeAttachment(i: number) {
    attachments = attachments.filter((_, idx) => idx !== i)
  }

  // ── Slash autocomplete (skills + MCP servers/tools) ──────────────────────
  import type { Skill } from '../../lib/types'

  let skills = $state<Skill[]>([])
  let mcpServerNames = $state<string[]>([])
  let mcpToolCache = $state<Record<string, McpTool[]>>({})
  let slashMenu = $state(false)
  let slashMode = $state<'skills' | 'mcp-servers' | 'mcp-tools'>('skills')
  let slashQuery = $state('')
  let slashActiveIndex = $state(-1)
  let slashMcpServer = $state('')

  type SlashItem =
    | { kind: 'skill'; skill: Skill }
    | { kind: 'mcp-server'; name: string }
    | { kind: 'mcp-tool'; server: string; tool: McpTool }

  function normalizeSlash(value: string): string {
    return value.replace(/^[\uff0f\u3001]/, '/')
  }

  function parseSlashInput(value: string): { mode: SlashItem['kind'] | null; query: string; serverName?: string } {
    const trimmed = normalizeSlash(value)
    if (!trimmed.startsWith('/')) return { mode: null, query: '' }
    const rest = trimmed.slice(1)
    const lowerRest = rest.toLowerCase()
    if (lowerRest === 'mcp' || lowerRest.startsWith('mcp/') || lowerRest.startsWith('mcp ')) {
      const after = rest.slice(3).trimStart() // after "mcp"
      if (after === '' || after === '/') {
        return { mode: 'mcp-server', query: '' }
      }
      const withoutLeadingSlash = after.startsWith('/') ? after.slice(1) : after
      const spaceIdx = withoutLeadingSlash.search(/\s/)
      const serverName = spaceIdx >= 0 ? withoutLeadingSlash.slice(0, spaceIdx) : withoutLeadingSlash
      const query = spaceIdx >= 0 ? withoutLeadingSlash.slice(spaceIdx + 1).trimStart().toLowerCase() : ''
      return { mode: 'mcp-tool', query, serverName }
    }
    if (/^\/\S*$/.test(trimmed)) {
      return { mode: 'skill', query: rest.toLowerCase() }
    }
    return { mode: null, query: '' }
  }

  function scoreSkillMatch(skill: Skill, query: string): number {
    if (!query) return 50
    const q = query.toLowerCase()
    const name = skill.name.toLowerCase()
    if (name === q) return 100
    if (name.startsWith(q)) return 80
    if (name.includes(q)) return 60
    return 0
  }

  function filteredItems(): SlashItem[] {
    if (slashMode === 'skills') {
      const q = slashQuery
      let scored = skills
        .map(s => ({ skill: s, score: scoreSkillMatch(s, q) }))
        .filter(({ score }) => score > 0)
      scored.sort((a, b) => b.score - a.score || a.skill.name.localeCompare(b.skill.name))
      return scored.map(({ skill }) => ({ kind: 'skill', skill }))
    }
    if (slashMode === 'mcp-servers') {
      const q = slashQuery
      return mcpServerNames
        .filter(n => !q || n.toLowerCase().includes(q))
        .sort((a, b) => a.localeCompare(b))
        .map(name => ({ kind: 'mcp-server', name }))
    }
    if (slashMode === 'mcp-tools') {
      const tools = mcpToolCache[slashMcpServer] ?? []
      const q = slashQuery
      return tools
        .filter(t => !q || t.name.toLowerCase().includes(q))
        .map(tool => ({ kind: 'mcp-tool', server: slashMcpServer, tool }))
    }
    return []
  }

  function showSlashMenu(mode: 'skills' | 'mcp-servers' | 'mcp-tools', query: string, serverName = '') {
    slashMode = mode
    slashQuery = query
    slashMcpServer = serverName
    slashActiveIndex = -1
    slashMenu = true
  }

  function hideSlashMenu() {
    slashMenu = false
    slashActiveIndex = -1
    slashMcpServer = ''
  }

  async function maybeLoadMcpTools(serverName: string) {
    if (mcpToolCache[serverName]) return
    try {
      const detail = await getMcpServer(serverName)
      mcpToolCache[serverName] = detail.tool_list ?? []
    } catch {
      mcpToolCache[serverName] = []
    }
  }

  async function handleSlashInput() {
    const normalized = normalizeSlash(text)
    if (normalized !== text) text = normalized
    const parsed = parseSlashInput(text)
    if (parsed.mode === null) {
      hideSlashMenu()
      return
    }
    if (parsed.mode === 'skill') {
      showSlashMenu('skills', parsed.query)
      return
    }
    if (parsed.mode === 'mcp-server') {
      showSlashMenu('mcp-servers', parsed.query)
      return
    }
    if (parsed.mode === 'mcp-tool' && parsed.serverName) {
      await maybeLoadMcpTools(parsed.serverName)
      showSlashMenu('mcp-tools', parsed.query, parsed.serverName)
      return
    }
    hideSlashMenu()
  }

  function selectItem(item: SlashItem) {
    if (item.kind === 'skill') {
      text = '/' + item.skill.name + ' '
    } else if (item.kind === 'mcp-server') {
      text = '/mcp/' + item.name + ' '
    } else if (item.kind === 'mcp-tool') {
      text = `请帮我调用 mcp__${item.server}__${item.tool.name}，并说明需要什么参数`
    }
    hideSlashMenu()
    queueMicrotask(() => textareaEl?.focus())
  }

  function moveSlashActive(delta: number) {
    const items = filteredItems()
    if (!slashMenu || items.length === 0) return
    slashActiveIndex = (slashActiveIndex + delta + items.length) % items.length
  }

  // The "/" button opens skill autocomplete with "/" prefilled.
  function insertSkill() {
    text = '/'
    showSlashMenu('skills', '')
    queueMicrotask(() => textareaEl?.focus())
  }

  // Full-width slash replacement + autocomplete trigger on input.
  function onInput() {
    handleSlashInput()
  }

  // $store autosubscription is reactive inside $derived (get() is not).
  let sid = $derived($activeSessionId ?? '')
  let isStreaming = $derived($chatStreaming[sid] ?? false)
  let currentSession = $derived($sessions.find(s => s.id === sid) ?? null)

  // Session meta chips — pull live values from per-session stores, fall back
  // to the session record, then to sensible defaults.
  let modelName = $derived(currentSession?.model || currentSession?.model_id || '—')
  let reasoning = $derived($chatReasoningEffort[sid] || currentSession?.reasoning_effort || 'medium')
  let workingDir = $derived($chatWorkingDir[sid] || currentSession?.working_dir || '')
  let permMode = $derived($chatPermMode[sid] || currentSession?.permission_mode || 'ask')
  // Effective show-reasoning for this session: live store > session record > default true.
  let showReasoning = $derived($chatShowReasoning[sid] ?? currentSession?.show_reasoning ?? true)
  let ctxUsage = $derived(Number($chatContextUsage[sid] ?? currentSession?.context_usage ?? 0))

  function cap(s: string): string {
    return s ? s[0].toUpperCase() + s.slice(1) : s
  }

  // ── model + reasoning pickers ──────────────────────────────────────────────
  let models = $state<api.ModelEntry[]>([])
  let modelMenu = $state(false)
  let reasonMenu = $state(false)
  const reasoningLevels = ['low', 'medium', 'high', 'xhigh', 'max']
  const showReasoningIcon = $derived(showReasoning ? 'ant-design:eye-outlined' : 'ant-design:eye-invisible-outlined')

  onMount(async () => {
    try { models = (await api.getConfig()).models ?? [] } catch { /* leave empty */ }
    try { skills = await api.listSkills() } catch { /* leave empty */ }
    try {
      const data = await api.listMcpServers()
      mcpServerNames = (data.servers ?? [])
        .filter((s: any) => s.status === 'connected')
        .map((s: any) => s.name)
    } catch { /* leave empty */ }
  })

  async function pickModel(m: api.ModelEntry) {
    modelMenu = false
    if (!sid) return
    try {
      const res = await api.updateSessionModel(sid, m.id)
      sessions.update(list => list.map((s: any) => s.id === sid ? { ...s, model: res.model, model_id: res.model_id } : s))
    } catch (e: any) {
      showToast(e.message ?? 'Failed to switch model', 'error')
    }
  }

  async function pickReasoning(level: string) {
    if (!sid) return
    try {
      await api.updateSessionReasoningEffort(sid, level)
      chatReasoningEffort.update(r => ({ ...r, [sid]: level }))
    } catch (e: any) {
      showToast(e.message ?? 'Failed to set reasoning', 'error')
    }
  }

  async function toggleShowReasoning() {
    if (!sid) return
    const next = !showReasoning
    try {
      await api.updateSessionShowReasoning(sid, next)
      chatShowReasoning.update(r => ({ ...r, [sid]: next }))
    } catch (e: any) {
      showToast(e.message ?? 'Failed to toggle reasoning visibility', 'error')
    }
  }

  function closeMenus() { modelMenu = false; reasonMenu = false }

  // Show only the last two path segments so a long working dir doesn't push
  // the chip row onto a second line. Full path is in the title tooltip.
  function shortDir(p: string): string {
    if (!p) return ''
    const parts = p.split('/').filter(Boolean)
    return parts.length <= 2 ? p : '…/' + parts.slice(-2).join('/')
  }

  function send() {
    if (!text.trim() && attachments.length === 0) return
    const v = text.trim()
    const files = attachments.length ? [...attachments] : undefined
    text = ''
    attachments = []
    if (onSend) {
      onSend(v, files)
    } else {
      running.set(true)
    }
  }

  function stop() {
    const s = get(activeSessionId)
    if (s) ws.interrupt(s)
    running.set(false)
  }

  function onKeydown(e: KeyboardEvent) {
    // Slash menu navigation
    if (slashMenu) {
      if (e.key === 'ArrowDown') { e.preventDefault(); moveSlashActive(1); return }
      if (e.key === 'ArrowUp') { e.preventDefault(); moveSlashActive(-1); return }
      if (e.key === 'Escape') { e.preventDefault(); hideSlashMenu(); return }
      if ((e.key === 'Tab' || e.key === 'Enter') && slashActiveIndex >= 0) {
        const items = filteredItems()
        if (items[slashActiveIndex]) {
          e.preventDefault()
          selectItem(items[slashActiveIndex])
          return
        }
      }
    }

    // Enter to send (unless shift is held)
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      send()
      return
    }
  }

  // Click outside to close slash menu.
  function onWindowClick(e: MouseEvent) {
    const target = e.target as HTMLElement
    if (slashMenu && !target.closest('.skill-menu') && !target.closest('.skill-btn')) {
      hideSlashMenu()
    }
    closeMenus()
  }
</script>

<svelte:window onclick={onWindowClick} />

<div class="composer">
  <div class="chips">
    <div class="picker">
      <button class="chip" onclick={(e) => { e.stopPropagation(); reasonMenu = false; modelMenu = !modelMenu }}>
        <iconify-icon icon="ant-design:robot-outlined" width="12"></iconify-icon>
        <span>{modelName}</span>
        <iconify-icon icon="lucide:chevron-down" width="12"></iconify-icon>
      </button>
      {#if modelMenu}
        <div class="menu" onclick={(e) => e.stopPropagation()}>
          {#if models.length === 0}
            <div class="menu-empty">{$t('chat.no_models')}</div>
          {:else}
            {#each models as m (m.id)}
              <button class="menu-item" onclick={() => pickModel(m)}>
                <span class="mi-name">{m.id}</span>
                <span class="mi-model mono">{m.model}</span>
              </button>
            {/each}
          {/if}
        </div>
      {/if}
    </div>
    <div class="picker">
      <button class="chip reasoning-chip" onclick={(e) => { e.stopPropagation(); modelMenu = false; reasonMenu = !reasonMenu }}>
        <span>{$t('chat.reasoning')} {cap(reasoning)}</span>
        <iconify-icon icon={showReasoningIcon} width="12" class="reasoning-eye"></iconify-icon>
        <iconify-icon icon="lucide:chevron-down" width="12"></iconify-icon>
      </button>
      {#if reasonMenu}
        <div class="menu" onclick={(e) => e.stopPropagation()}>
          {#each reasoningLevels as lvl}
            <button class="menu-item" class:active={lvl === reasoning} onclick={() => pickReasoning(lvl)}>
              <span class="mi-name">{cap(lvl)}</span>
            </button>
          {/each}
          <div class="menu-divider"></div>
          <button class="menu-item toggle-item" onclick={() => toggleShowReasoning()}>
            <span class="mi-name">{$t('chat.show_reasoning')}</span>
            <span class="toggle" class:on={showReasoning}>
              <span class="toggle-knob"></span>
            </span>
          </button>
        </div>
      {/if}
    </div>
    {#if workingDir}
      <span class="chip static" title={workingDir}><span class="mono">{shortDir(workingDir)}</span></span>
    {/if}
    <span class="chip static context-chip">
      <span>{$t('chat.context')}</span>
      <span class="ctx-bar"><span class="ctx-fill" style="width:{Math.min(ctxUsage, 100)}%"></span></span>
      <span class="mono">{ctxUsage}%</span>
    </span>
    <span style="margin-left:auto;"></span>
    {#if permMode === 'auto'}
      <StatusTag status="success">{$t('chat.auto_mode')}</StatusTag>
    {:else}
      <StatusTag status="warning">{$t('chat.ask_mode')}</StatusTag>
    {/if}
  </div>

  <div class="input-wrap">
    <div
      class="input-card"
      class:drag-over={dragOver}
      ondragover={onDragOver}
      ondragleave={onDragLeave}
      ondrop={onDrop}
    >
      {#if attachments.length > 0}
        <div class="attachments">
          {#each attachments as a, i}
            <span class="attach-chip" title={a.name}>
              <iconify-icon icon="ant-design:paper-clip-outlined" width="12"></iconify-icon>
              <span class="attach-name">{a.name}</span>
              <button class="attach-x" title={$t('chat.remove')} onclick={() => removeAttachment(i)}>
                <iconify-icon icon="ant-design:close-outlined" width="11"></iconify-icon>
              </button>
            </span>
          {/each}
        </div>
      {/if}
      <textarea
        bind:this={textareaEl}
        rows={1}
        placeholder={$t('chat.placeholder')}
        bind:value={text}
        onkeydown={onKeydown}
        oninput={onInput}
        onpaste={onPaste}
      ></textarea>
      {#if slashMenu}
        <div class="skill-menu">
          {#each filteredItems() as item, i (item.kind + ':' + (item.kind === 'skill' ? item.skill.name : item.kind === 'mcp-server' ? item.name : item.server + '/' + item.tool.name))}
            <button
              class="skill-menu-item"
              class:active={i === slashActiveIndex}
              onclick={() => selectItem(item)}
            >
              {#if item.kind === 'skill'}
                <span class="skill-name">/{item.skill.name}</span>
                {#if item.skill.desc}
                  <span class="skill-desc">{item.skill.desc}</span>
                {/if}
              {:else if item.kind === 'mcp-server'}
                <span class="skill-name">/mcp/{item.name}</span>
                <span class="skill-desc">MCP server</span>
              {:else}
                <span class="skill-name">mcp__{item.server}__{item.tool.name}</span>
                {#if item.tool.description}
                  <span class="skill-desc">{item.tool.description}</span>
                {/if}
              {/if}
            </button>
          {:else}
            <div class="skill-menu-empty">
              {slashMode === 'skills' ? 'No matching skills' : slashMode === 'mcp-servers' ? 'No matching MCP servers' : 'No tools found'}
            </div>
          {/each}
        </div>
      {/if}
      <input
        bind:this={fileInputEl}
        type="file"
        accept="image/*"
        multiple
        style="display:none"
        onchange={onFilesPicked}
      />
      <div class="input-footer">
        <button class="tool-btn" title={$t('chat.attach_image')} onclick={openAttach}>
          <iconify-icon icon="ant-design:paper-clip-outlined" width="15"></iconify-icon>
        </button>
        <button class="tool-btn skill-btn" title={$t('chat.insert_slash')} onclick={insertSkill}>/</button>
        <span style="margin-left:auto;"></span>
        {#if isStreaming || $running}
          <!-- Mid-turn: Stop interrupts the running turn; Send stays available
               so a follow-up message steers the turn in flight (rides the
               running Agent's Inbox server-side). -->
          <button class="stop-btn" onclick={stop}>
            <span class="stop-sq"></span>
            {$t('chat.stop')}
          </button>
        {/if}
        <button class="send-btn" onclick={send}>{$t('chat.send')}</button>
      </div>
    </div>
  </div>
</div>

<style>
.composer {
  flex: 0 0 auto;
  background: var(--bg-container);
  border-top: 1px solid var(--border-secondary);
}
.chips {
  max-width: var(--chat-content-max-width, 1080px); margin: 0 auto;
  padding: 12px 24px 0;
  display: flex; align-items: center; gap: 8px; flex-wrap: wrap;
}
.chip {
  height: 24px; padding: 0 10px; border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 9999px; display: flex; align-items: center; gap: 6px;
  font-size: 12px; color: var(--text-secondary); cursor: pointer; font-family: inherit;
}
.chip:hover { border-color: var(--blue-5); color: var(--blue-5); }
.chip.static { cursor: default; background: var(--bg-table-header); border-color: var(--border-secondary); }
.chip.static:hover { border-color: var(--border-secondary); color: var(--text-secondary); }
.picker { position: relative; }
.menu {
  position: absolute; bottom: calc(100% + 6px); left: 0; z-index: 50;
  min-width: 200px; max-width: 320px; max-height: 280px; overflow-y: auto;
  background: var(--bg-container); border: 1px solid var(--border-secondary); border-radius: 10px;
  box-shadow: 0 8px 24px rgba(15,23,42,0.14); padding: 4px;
}
.menu-item {
  width: 100%; display: flex; flex-direction: column; gap: 1px; align-items: flex-start;
  padding: 7px 10px; border: none; background: transparent; border-radius: 6px;
  cursor: pointer; font-family: inherit; text-align: left;
}
.menu-item:hover { background: rgba(22,119,255,0.08); }
.menu-item.active { background: var(--active-blue-bg); }
.menu-divider { height: 1px; background: var(--border-secondary); margin: 4px 0; }
.menu-item.toggle-item { flex-direction: row; justify-content: space-between; align-items: center; }
.toggle {
  width: 30px; height: 16px; border-radius: 9999px; background: var(--border);
  position: relative; cursor: pointer; transition: background 0.15s ease;
}
.toggle.on { background: var(--success); }
.toggle-knob {
  position: absolute; top: 2px; left: 2px;
  width: 12px; height: 12px; border-radius: 50%; background: #fff;
  box-shadow: 0 1px 2px rgba(0,0,0,0.15);
  transition: transform 0.15s ease;
}
.toggle.on .toggle-knob { transform: translateX(14px); }
.mi-name { font-size: 13px; color: var(--text); }
.mi-model { font-size: 11px; color: var(--text-tertiary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 280px; }
.menu-empty { padding: 8px 10px; font-size: 12px; color: var(--text-tertiary); }
.reasoning-chip { padding-right: 8px; }
.reasoning-eye { color: var(--success); }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.context-chip { gap: 8px; }
.ctx-bar { width: 56px; height: 4px; background: var(--border-table); border-radius: 9999px; overflow: hidden; display: inline-block; }
.ctx-fill { display: block; height: 100%; background: var(--blue-6); border-radius: 9999px; }
.input-wrap { max-width: var(--chat-content-max-width, 1080px); margin: 10px auto 0; padding: 0 24px 16px; }
.input-card {
  background: var(--bg-container); border: 1px solid var(--border); border-radius: 12px;
  padding: 10px 12px; display: flex; flex-direction: column; gap: 8px;
  position: relative;
}
.input-card:focus-within {
  border-color: var(--blue-6);
  box-shadow: 0 0 0 2px rgba(5,145,255,0.1);
}
.input-card.drag-over {
  border-color: var(--blue-6);
  background: rgba(5,145,255,0.06);
  box-shadow: 0 0 0 2px rgba(5,145,255,0.15);
}
textarea {
  border: none; outline: none; resize: none; font-size: 14px; line-height: 1.6;
  font-family: inherit; color: var(--text); background: transparent; width: 100%;
}
.attachments { display: flex; flex-wrap: wrap; gap: 6px; }
.attach-chip {
  display: inline-flex; align-items: center; gap: 5px; max-width: 200px;
  height: 24px; padding: 0 6px 0 8px; background: var(--surface-info); border: 1px solid var(--blue-2);
  border-radius: 6px; font-size: 12px; color: var(--text-secondary);
}
.attach-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.attach-x {
  border: none; background: transparent; cursor: pointer; padding: 0;
  display: flex; align-items: center; color: var(--text-tertiary);
}
.attach-x:hover { color: var(--error); }
.input-footer { display: flex; align-items: center; gap: 4px; }
.tool-btn {
  width: 28px; height: 28px; border: none; background: transparent; border-radius: 6px;
  display: flex; align-items: center; justify-content: center;
  cursor: pointer; color: var(--text-tertiary);
}
.tool-btn:hover { background: var(--hover-neutral); color: var(--text-secondary); }
.skill-btn { font-size: 14px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.send-btn {
  height: 32px; padding: 0 16px; border: none; background: var(--blue-6);
  border-radius: 6px; font-size: 14px; color: #fff; cursor: pointer; font-family: inherit;
}
.send-btn:hover { background: var(--blue-5); }
.stop-btn {
  height: 32px; padding: 0 14px; border: 1px solid var(--error-border); background: var(--error-bg);
  border-radius: 6px; display: flex; align-items: center; gap: 7px;
  font-size: 14px; color: var(--error); cursor: pointer; font-family: inherit;
}
.stop-btn:hover { border-color: var(--error); }
.stop-sq { width: 9px; height: 9px; border-radius: 2px; background: var(--error); }

/* Skill autocomplete dropdown */
.skill-menu {
  position: absolute;
  bottom: calc(100% + 4px);
  left: 0;
  right: 0;
  z-index: 50;
  max-height: 240px;
  overflow-y: auto;
  background: var(--bg-container);
  border: 1px solid var(--border-secondary);
  border-radius: 10px;
  box-shadow: 0 8px 24px rgba(15,23,42,0.14);
  padding: 4px;
}
.skill-menu-item {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 2px;
  align-items: flex-start;
  padding: 7px 10px;
  border: none;
  background: transparent;
  border-radius: 6px;
  cursor: pointer;
  font-family: inherit;
  text-align: left;
}
.skill-menu-item:hover,
.skill-menu-item.active {
  background: rgba(22,119,255,0.08);
}
.skill-name {
  font-size: 13px;
  color: var(--text);
  font-weight: 500;
}
.skill-desc {
  font-size: 11px;
  color: var(--text-tertiary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 100%;
}
.skill-menu-empty {
  padding: 8px 10px;
  font-size: 12px;
  color: var(--text-tertiary);
}
</style>
