<script lang="ts">
  import { get } from 'svelte/store'
  import { onMount, untrack, tick } from 'svelte'
  import {
    running, activeSessionId, chatStreaming, sessions, sessionGroups,
    chatContextUsage, chatWorkingDir, chatPermMode, chatReasoningEffort, chatShowReasoning, showToast, chatGoal, chatModel,
    globalPermissionMode, nativeShell, localAccess, activeAgent, pendingModel, view, settingsModalOpen,
    pendingAgent, pendingWorkingDir, pendingGroupId, normalizeDir,
  } from '../../lib/stores'
  import { ws } from '../../lib/ws'
  import * as api from '../../lib/api'
  import { t } from '../../lib/i18n'
  import { submitIntent } from '../../lib/composerKeys'
  import { composeSlashCommand } from '../../lib/slashCompose'
  import FolderPickerModal from '../overlays/FolderPickerModal.svelte'
  import type { McpServerDetail, McpTool } from '../../lib/types'
  import { getMcpServer } from '../../lib/api'

  let { onSend }: { onSend?: (text: string, files?: any[], queued?: boolean) => void } = $props()

  // A staged attachment. Images carry inline as a base64 data URL (the model
  // gets an image block); every other type is uploaded to the server and
  // referenced by `path` (an /api/uploads/<name> URL) so the agent opens it
  // with read_file/terminal — mirroring how it works against the CLI's
  // filesystem. Exactly one of data_url / path is set once ready. `uploading`
  // marks a placeholder whose upload is still in flight; `id` keys that
  // placeholder so its async result lands on the right entry (see addAttachment).
  // local_path is a real local path (native dialog on desktop, or the in-app
  // file picker on localhost web) — the agent reads it in place, no upload
  // (mirrors the folder picker). Set instead of data_url/path when same-machine.
  type Attachment = { id?: string; name: string; mime_type?: string; data_url?: string; path?: string; local_path?: string; uploading?: boolean }

  // Reject oversized attachments client-side with a clear message rather than
  // letting them fail late. Images keep a conservative cap: they are canvas-
  // decoded for compression and ride inline as data URLs. Other files stream
  // to ~/.octo/uploads and are read from disk by the agent, so they only stop
  // at the server's much larger request cap — keep MAX_FILE_BYTES in sync with
  // maxUploadBytes in internal/server/upload_handler.go.
  const MAX_IMAGE_BYTES = 32 * 1024 * 1024
  const MAX_FILE_BYTES = 512 * 1024 * 1024

  let text = $state('')
  // Per-session composer draft: keyed by session id so switching sessions
  // doesn't carry a half-typed message (or its staged attachments) into — or
  // send them to — the wrong conversation. Plain objects, not $state —
  // nothing renders them directly, they are only read/written from the
  // session-switch effect below.
  let draftsBySession: Record<string, string> = {}
  let attachmentsBySession: Record<string, Attachment[]> = {}
  let draftSid = ''
  let textareaEl = $state<HTMLTextAreaElement | null>(null)
  let fileInputEl = $state<HTMLInputElement | null>(null)
  let skillMenuEl = $state<HTMLDivElement | null>(null)
  let attachments = $state<Attachment[]>([])
  let dragOver = $state(false)

  // Called by ChatView when the user clicks "edit" on a prior message — loads
  // that text back into the composer for resend.
  export function setText(v: string) {
    text = v
    queueMicrotask(() => textareaEl?.focus())
  }

  // Called by ChatView when the user retracts a pending steer message: reload
  // its text AND any attachments back into the composer for editing. The files
  // are the same already-staged Attachment objects (data_url / path / local_path
  // all resolved), so they can go straight back into the tray.
  export function restore(v: string, files?: Attachment[]) {
    text = v
    if (files && files.length) attachments = [...files]
    queueMicrotask(() => textareaEl?.focus())
  }

  // Whether the box already holds something the user typed. Callers that push
  // text back in unprompted (a turn error handing back the failed message) check
  // this first so they never clobber a message being composed.
  export function isEmpty(): boolean {
    return text.trim() === '' && attachments.length === 0
  }

  // Auto-grow the textarea with its content up to a max height, then scroll
  // inside (matches the max-height in CSS). The $effect re-runs on every text
  // change — typing, paste, send-clear, or programmatic setText.
  //
  // On Windows, setting height:'auto' then reading scrollHeight can return an
  // inflated value (the drag strip + font metrics differ), blowing the textarea
  // up to 5-6 lines on mount. Lock overflow to hidden while measuring so the
  // scrollbar never contributes to scrollHeight, and floor the result to a
  // single line so an empty box never opens tall.
  const MAX_TEXTAREA_PX = 156
  const MIN_TEXTAREA_PX = 24 // ≈ one line: 14px * 1.6 line-height + padding
  function autoResize() {
    const el = textareaEl
    if (!el) return
    el.style.overflow = 'hidden'
    el.style.height = 'auto'
    const h = el.scrollHeight
    el.style.height = Math.min(Math.max(h, MIN_TEXTAREA_PX), MAX_TEXTAREA_PX) + 'px'
    // Scrolling only exists once the content passes the height cap — below it
    // a rounding sliver (scrollHeight a px or two over the set height) would
    // otherwise paint a phantom scrollbar thumb beside the send button.
    el.style.overflowY = h > MAX_TEXTAREA_PX ? 'auto' : 'hidden'
    el.style.overflowX = ''
  }
  $effect(() => {
    text // track the bound value so the effect re-runs when it changes
    autoResize()
  })

  // The attach button. Same machine as the agent: attach by real path, no
  // upload (and no size cap — the agent reads the file in place).
  //  - desktop shell → native OS file dialog
  //  - localhost web → in-app file picker (server-side fs browse)
  // Remote web → browser upload (front and back aren't co-located).
  async function openAttach() {
    if (get(nativeShell)) {
      try {
        const res = await api.nativePickFile(workingDir)
        if (!res.cancelled && res.path) attachLocalFile(res.path)
      } catch (e: any) {
        showToast(e.message ?? 'Failed to open file dialog', 'error')
      }
      return
    }
    if (get(localAccess)) {
      pickerMode = 'file'
      pickerOpen = true
      return
    }
    fileInputEl?.click()
  }

  // Attach a real local file by its absolute path — the agent reads it in place
  // (see server parseUserFiles' local_path handling), no upload round-trip.
  function attachLocalFile(path: string) {
    const name = path.split(/[/\\]/).pop() || path
    attachTo(sid, { name, local_path: path })
  }

  function onFilesPicked(e: Event) {
    const input = e.target as HTMLInputElement
    const files = Array.from(input.files ?? [])
    for (const f of files) addAttachment(f)
    input.value = ''
  }

  // Attachment reads (image FileReader, non-image upload) resolve asynchronously.
  // These helpers land the result on the session that STARTED the read, not
  // whatever session is active when it finishes — otherwise switching sessions
  // (or sending) mid-upload leaks the file into the wrong conversation.
  let uploadSeq = 0
  function attachTo(originSid: string, att: Attachment) {
    if (originSid === sid) attachments = [...attachments, att]
    else attachmentsBySession[originSid] = [...(attachmentsBySession[originSid] ?? []), att]
  }
  function patchAttachment(originSid: string, id: string, patch: Partial<Attachment>) {
    const apply = (list: Attachment[]) => list.map(a => a.id === id ? { ...a, ...patch } : a)
    if (originSid === sid) attachments = apply(attachments)
    else if (attachmentsBySession[originSid]) attachmentsBySession[originSid] = apply(attachmentsBySession[originSid])
  }
  function dropAttachment(originSid: string, id: string) {
    const drop = (list: Attachment[]) => list.filter(a => a.id !== id)
    if (originSid === sid) attachments = drop(attachments)
    else if (attachmentsBySession[originSid]) attachmentsBySession[originSid] = drop(attachmentsBySession[originSid])
  }

  // Images ride inline as base64 data URLs inside the WebSocket chat message,
  // so their encoded size must stay well under the server's wsMaxMessageSize
  // (4 MB) — a raw phone photo (3–10 MB → +33% base64) blows straight past it
  // and the server drops the connection. Canvas-compress before staging:
  // long edge capped, re-encoded to JPEG. The server's own NewImageBlock
  // normalization uses the SAME cap, so an image compressed here passes
  // through untouched. GIFs/SVGs are exempt (canvas would kill the
  // animation/vector); they rely on the 32 MB attachment cap and the WS
  // headroom instead.
  const IMAGE_COMPRESS_MAX_EDGE = 1568
  const IMAGE_COMPRESS_QUALITY = 0.8

  function readAsDataURL(file: File): Promise<string> {
    return new Promise((resolve, reject) => {
      const reader = new FileReader()
      reader.onload = () => resolve(String(reader.result))
      reader.onerror = () => reject(reader.error)
      reader.readAsDataURL(file)
    })
  }

  // compressImage returns a data URL for the image: JPEG-re-encoded and
  // downscaled when possible, the original data URL when the canvas path is
  // unavailable (or the format shouldn't be re-encoded). Never throws — a
  // failed compress must not eat the attachment.
  async function compressImage(file: File): Promise<string> {
    if (file.type === 'image/gif' || file.type === 'image/svg+xml') {
      return readAsDataURL(file)
    }
    try {
      const url = URL.createObjectURL(file)
      try {
        const img = await new Promise<HTMLImageElement>((resolve, reject) => {
          const el = new Image()
          el.onload = () => resolve(el)
          el.onerror = () => reject(new Error('image decode failed'))
          el.src = url
        })
        const scale = Math.min(1, IMAGE_COMPRESS_MAX_EDGE / Math.max(img.naturalWidth, img.naturalHeight))
        const w = Math.max(1, Math.round(img.naturalWidth * scale))
        const h = Math.max(1, Math.round(img.naturalHeight * scale))
        const canvas = document.createElement('canvas')
        canvas.width = w
        canvas.height = h
        const ctx = canvas.getContext('2d')
        if (!ctx) return await readAsDataURL(file)
        // JPEG has no alpha channel — flatten onto white or transparent PNG
        // regions would come out black.
        ctx.fillStyle = '#ffffff'
        ctx.fillRect(0, 0, w, h)
        ctx.drawImage(img, 0, 0, w, h)
        const out = canvas.toDataURL('image/jpeg', IMAGE_COMPRESS_QUALITY)
        // A rare pathological case (already-tiny JPEG) can grow under
        // re-encoding; keep whichever is smaller.
        if (out.length >= file.size * 1.37) return await readAsDataURL(file)
        return out
      } finally {
        URL.revokeObjectURL(url)
      }
    } catch {
      return readAsDataURL(file)
    }
  }

  async function addAttachment(file: File, fallbackName?: string) {
    const name = file.name || fallbackName || 'attachment'
    const isImage = file.type.startsWith('image/')
    if (file.size > (isImage ? MAX_IMAGE_BYTES : MAX_FILE_BYTES)) {
      showToast($t(isImage ? 'chat.attach_img_too_large' : 'chat.attach_too_large'), 'error')
      return
    }
    const originSid = sid
    // Images ride inline as a data URL (decoded into an image block server-side).
    if (isImage) {
      // Stage a placeholder synchronously (same pattern as the upload branch
      // below): canvas compression takes ~100ms-1s for a large photo, and a
      // send() in that window must be blocked — not fire with text only while
      // the image silently lands on the NEXT message.
      const id = `up-${++uploadSeq}`
      attachTo(originSid, { id, name, mime_type: file.type, uploading: true })
      try {
        const dataUrl = await compressImage(file)
        patchAttachment(originSid, id, { data_url: dataUrl, mime_type: dataUrl.startsWith('data:image/jpeg') ? 'image/jpeg' : file.type, uploading: false })
      } catch (e: any) {
        dropAttachment(originSid, id)
        showToast(e?.message ?? `Failed to read ${name}`, 'error')
      }
      return
    }
    // Any other file (pdf, xlsx, zip, csv, …) uploads to ~/.octo/uploads and is
    // sent by path; the agent reads it from disk with its own tools. Stage a
    // visible placeholder immediately so the file is never silently lost while
    // the upload is in flight (send() refuses until it clears), then fill in the
    // path or drop the placeholder on failure.
    const id = `up-${++uploadSeq}`
    attachTo(originSid, { id, name, mime_type: file.type, uploading: true })
    try {
      const url = await api.uploadFile(file)
      patchAttachment(originSid, id, { path: url, uploading: false })
    } catch (e: any) {
      dropAttachment(originSid, id)
      showToast(e.message ?? `Failed to upload ${name}`, 'error')
    }
  }

  // Paste files from the clipboard into the composer (images or any other file).
  function onPaste(e: ClipboardEvent) {
    const items = Array.from(e.clipboardData?.items ?? [])
    const fileItems = items.filter(it => it.kind === 'file')
    if (fileItems.length === 0) return
    e.preventDefault()
    for (const it of fileItems) {
      const f = it.getAsFile()
      if (!f) continue
      addAttachment(f, f.type.startsWith('image/') ? 'pasted-image' : undefined)
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
    for (const f of files) addAttachment(f)
  }

  function removeAttachment(i: number) {
    attachments = attachments.filter((_, idx) => idx !== i)
  }

  // ── Slash autocomplete (skills + workflows via /wf + MCP servers/tools) ───
  import type { Skill } from '../../lib/types'

  let skills = $state<Skill[]>([])
  let workflows = $state<api.NamedWorkflow[]>([])
  let mcpServerNames = $state<string[]>([])
  let mcpToolCache = $state<Record<string, McpTool[]>>({})
  let slashMenu = $state(false)
  let slashMode = $state<'skills' | 'workflows' | 'mcp-servers' | 'mcp-tools' | 'agents'>('skills')
  let slashQuery = $state('')
  let slashActiveIndex = $state(-1)
  let slashMcpServer = $state('')
  // Draft parked under a picked command so it survives as the command's
  // argument. See composeSlashCommand.
  let slashDraftTail = $state('')

  type SlashItem =
    | { kind: 'builtin'; name: string; descKey: string; takesArgs: boolean }
    | { kind: 'skill'; skill: Skill }
    | { kind: 'workflow'; workflow: api.NamedWorkflow }
    | { kind: 'mcp-server'; name: string }
    | { kind: 'mcp-tool'; server: string; tool: McpTool }
    | { kind: 'agent'; id: string; name: string }
    | { kind: 'agent-create' }

  // Built-in slash commands that actually do something on web: /clear, /compact
  // and /goal are handled inline by the server; /loop falls through to the model
  // (backed by the schedule_wakeup tool). Unlike skills they aren't discoverable
  // on their own, so surface them in the same "/" menu — the TUI already lists
  // its built-ins in completion. Names only; descriptions are translated in the
  // template via descKey.
  // takesArgs=false marks the ones the server matches by exact equality
  // (handleSlashCommand): anything appended after them turns the command into
  // an ordinary chat message, so they never absorb a parked draft.
  const BUILTIN_COMMANDS: { name: string; descKey: string; takesArgs: boolean }[] = [
    { name: 'loop', descKey: 'composer.cmd_loop', takesArgs: true },
    { name: 'goal', descKey: 'composer.cmd_goal', takesArgs: true },
    { name: 'compact', descKey: 'composer.cmd_compact', takesArgs: false },
    { name: 'reload', descKey: 'composer.cmd_reload', takesArgs: false },
    { name: 'clear', descKey: 'composer.cmd_clear', takesArgs: false },
  ]

  function normalizeSlash(value: string): string {
    return value.replace(/^[\uff0f\u3001]/, '/').replace(/^\uff20/, '@')
  }

  function parseSlashInput(value: string): { mode: SlashItem['kind'] | null; query: string; serverName?: string } {
    const trimmed = normalizeSlash(value)
    // A leading "@" with no whitespace summons the agent picker — only
    // meaningful before the session is locked (see handleSlashInput's
    // agentLocked guard): agent_profile can be reassigned up until the
    // session's first turn runs, never after.
    if (/^@\S*$/.test(trimmed)) {
      return { mode: 'agent', query: trimmed.slice(1).toLowerCase() }
    }
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
    // "/wf" (own trigger, like "/mcp") lists named workflows. Checked before the
    // generic skill match so it isn't swallowed as a skill query.
    if (lowerRest === 'wf' || lowerRest.startsWith('wf/') || lowerRest.startsWith('wf ')) {
      const after = rest.slice(2).trimStart() // after "wf"
      const query = after.startsWith('/') ? after.slice(1) : after
      return { mode: 'workflow', query: query.toLowerCase() }
    }
    if (/^\/\S*$/.test(trimmed)) {
      return { mode: 'skill', query: rest.toLowerCase() }
    }
    return { mode: null, query: '' }
  }

  function scoreNameMatch(rawName: string, query: string): number {
    if (!query) return 50
    const q = query.toLowerCase()
    const name = rawName.toLowerCase()
    if (name === q) return 100
    if (name.startsWith(q)) return 80
    if (name.includes(q)) return 60
    return 0
  }

  function scoreSkillMatch(skill: Skill, query: string): number {
    return scoreNameMatch(skill.name, query)
  }

  function filteredItems(): SlashItem[] {
    if (slashMode === 'skills') {
      const q = slashQuery
      // Built-in commands share the generic "/" trigger with skills; list them
      // first so a bare "/" (or a prefix like "/l") surfaces them alongside
      // matching skills.
      const builtins: SlashItem[] = BUILTIN_COMMANDS
        .map(cmd => ({ cmd, score: scoreNameMatch(cmd.name, q) }))
        .filter(({ score }) => score > 0)
        .sort((a, b) => b.score - a.score || a.cmd.name.localeCompare(b.cmd.name))
        .map(({ cmd }) => ({ kind: 'builtin', name: cmd.name, descKey: cmd.descKey, takesArgs: cmd.takesArgs }))
      let scored = skills
        .map(s => ({ skill: s, score: scoreSkillMatch(s, q) }))
        .filter(({ score }) => score > 0)
      scored.sort((a, b) => b.score - a.score || a.skill.name.localeCompare(b.skill.name))
      const skillItems: SlashItem[] = scored.map(({ skill }) => ({ kind: 'skill', skill }))
      return [...builtins, ...skillItems]
    }
    if (slashMode === 'workflows') {
      const q = slashQuery
      return workflows
        .filter(w => !q || w.name.toLowerCase().includes(q) || w.description.toLowerCase().includes(q))
        .sort((a, b) => a.name.localeCompare(b.name))
        .map(workflow => ({ kind: 'workflow', workflow }))
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
    if (slashMode === 'agents') {
      const q = slashQuery
      // Mirrors the sidebar's new-session picker: "Default" first, the expert
      // agents, then a fixed create row that ignores the filter.
      const rows: SlashItem[] = [
        { kind: 'agent', id: 'default', name: 'Default' },
        ...agents.map(a => ({ kind: 'agent' as const, id: a.id, name: a.name })),
      ]
      const filtered = rows
        .map(r => ({ r, score: scoreNameMatch(r.kind === 'agent' ? r.name : '', q) }))
        .filter(({ score }) => score > 0)
        .sort((a, b) => b.score - a.score)
        .map(({ r }) => r)
      return [...filtered, { kind: 'agent-create' }]
    }
    return []
  }

  function showSlashMenu(mode: 'skills' | 'workflows' | 'mcp-servers' | 'mcp-tools' | 'agents', query: string, serverName = '') {
    // Every re-open re-derives the menu from the current text, so a draft
    // parked earlier is stale by now — keeping it would resurrect text the
    // user has since deleted.
    slashDraftTail = ''
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
    slashDraftTail = ''
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

  // Generation guard (like modelsFetchSeq): the mcp-tool branch awaits a
  // fetch mid-keystroke-stream, and a stale invocation resuming after the
  // user deleted the trigger or switched servers must not re-open its menu.
  let slashInputSeq = 0

  async function handleSlashInput() {
    const seq = ++slashInputSeq
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
    if (parsed.mode === 'agent') {
      // Once a session has run its first turn, agent_profile is locked —
      // "@" is just a character then, not a picker trigger.
      if (!agentLocked) showSlashMenu('agents', parsed.query)
      else hideSlashMenu()
      return
    }
    if (parsed.mode === 'workflow') {
      showSlashMenu('workflows', parsed.query)
      return
    }
    if (parsed.mode === 'mcp-server') {
      showSlashMenu('mcp-servers', parsed.query)
      return
    }
    if (parsed.mode === 'mcp-tool' && parsed.serverName) {
      await maybeLoadMcpTools(parsed.serverName)
      if (seq !== slashInputSeq) return
      showSlashMenu('mcp-tools', parsed.query, parsed.serverName)
      return
    }
    hideSlashMenu()
  }

  function selectItem(item: SlashItem) {
    const draft = slashDraftTail
    let command = ''
    let takesArgs = true
    if (item.kind === 'builtin') {
      takesArgs = item.takesArgs
      // Prefill "/name "; the no-arg ones (/clear, /compact) tolerate the
      // trailing space (the server trims before matching), and the arg-taking
      // ones (/loop, /goal) leave the caret ready for input.
      command = '/' + item.name + ' '
    } else if (item.kind === 'skill') {
      command = '/' + item.skill.name + ' '
    } else if (item.kind === 'workflow') {
      // Prefill an editable run instruction; the user adds any args, then sends,
      // and the agent calls the workflow tool by name. (agentic-first)
      command = `Run the "${item.workflow.name}" workflow`
    } else if (item.kind === 'mcp-server') {
      command = '/mcp/' + item.name + ' '
    } else if (item.kind === 'mcp-tool') {
      command = $t('composer.mcp_tool_prompt').replace('{server}', item.server).replace('{tool}', item.tool.name)
    } else if (item.kind === 'agent') {
      // Assign the agent and swallow the "@query" trigger — the text box goes
      // back to whatever draft the trigger was parked over (usually empty).
      pickAgent(item.id)
      text = draft
      hideSlashMenu()
      return
    } else if (item.kind === 'agent-create') {
      text = draft
      hideSlashMenu()
      view.set('agents')
      return
    }
    text = takesArgs ? composeSlashCommand(command, draft) : command
    hideSlashMenu()
    queueMicrotask(() => textareaEl?.focus())
  }

  function moveSlashActive(delta: number) {
    const items = filteredItems()
    if (!slashMenu || items.length === 0) return
    slashActiveIndex = (slashActiveIndex + delta + items.length) % items.length
    // Keyboard nav can move the active item outside the menu's scroll
    // viewport (max-height: 240px); scroll it back into view.
    tick().then(() => {
      skillMenuEl?.querySelector('.skill-menu-item.active')?.scrollIntoView({ block: 'nearest' })
    })
  }


  // Full-width slash replacement + autocomplete trigger on input.
  function onInput() {
    handleSlashInput()
  }

  // $store autosubscription is reactive inside $derived (get() is not).
  let sid = $derived($activeSessionId ?? '')

  // Swap the composer's live text + staged attachments for the session's own
  // draft on every session switch: save the departing session's in-progress
  // state, restore the incoming session's (or start blank). Also resets
  // input-history navigation, which is likewise per-session (see
  // recallOlder/recallNewer). `sid` is the only tracked dependency — reading
  // text/attachments inside untrack() keeps this from re-running on every
  // keystroke or attachment change.
  $effect(() => {
    const nextSid = sid
    if (nextSid === draftSid) return
    untrack(() => {
      if (draftSid) {
        draftsBySession[draftSid] = text
        attachmentsBySession[draftSid] = attachments
      }
      text = draftsBySession[nextSid] ?? ''
      attachments = attachmentsBySession[nextSid] ?? []
      draftSid = nextSid
      historyIndex = null
      // The menu describes the departing session's text. Keyboard switches
      // (⌘K) never fire the outside-click that would otherwise close it.
      hideSlashMenu()
    })
  })

  let isStreaming = $derived($chatStreaming[sid] ?? false)
  let currentSession = $derived($sessions.find(s => s.id === sid) ?? null)

  // Session meta chips — pull live values from per-session stores, fall back
  // to the session record, then to sensible defaults.
  // No session yet → show the pending pick (composite "<endpoint>::<model>"
  // id; display just the model half) that ensureActiveSession will apply.
  let modelName = $derived(
    $chatModel[sid] || currentSession?.model || currentSession?.model_id
    || ($pendingModel ? $pendingModel.split('::').pop() : '') || '—',
  )
  // A pending pick belongs to the blank new-chat view only. Once a session is
  // active (auto-created — which consumed it — or picked/created any other
  // way), drop any leftover so it can't leak into a later blank view.
  $effect(() => { if (sid) pendingModel.set('') })
  // "" (off) is a legitimate resolved value, not "no data yet" — only fall
  // back to the bootstrap default when neither source has reported anything
  // at all (?? only skips null/undefined, not ""). That default is 'off':
  // an absent reasoning_effort in config means reasoning disabled, so
  // claiming 'medium' before data arrives misreports the real state.
  let reasoning = $derived.by(() => {
    const v = $chatReasoningEffort[sid] ?? currentSession?.reasoning_effort
    return v === undefined ? 'off' : (v || 'off')
  })
  // The project (a session group carrying a working dir) this session belongs
  // to, if any. A project's directory governs every session in it, so the dir
  // chip below goes read-only rather than letting the user edit a value the
  // server would refuse.
  // On the landing page there is no session to look up membership for, so the
  // project comes from whichever pick is going to decide it. A group docked by
  // the sidebar's per-group "+" wins outright — the same precedence
  // ensureActiveSession applies — so show THAT project, or the chip would offer
  // to choose a directory whose value the send then discards. Otherwise the
  // project is whoever already owns the directory the user picked, which is the
  // match ensureActiveSession will make when it files the session.
  let project = $derived.by(() => {
    if (sid) return $sessionGroups.find(g => !!g.working_dir && g.session_ids.includes(sid)) ?? null
    const docked = $pendingGroupId
    if (docked) return $sessionGroups.find(g => g.id === docked && !!g.working_dir) ?? null
    const dir = $pendingWorkingDir
    if (!dir) return null
    // Excluding a scheduled task's cluster: it can carry a directory and is
    // still not a project to file a new session under.
    return $sessionGroups.find(g => !g.task_id && !!g.working_dir && normalizeDir(g.working_dir!) === normalizeDir(dir)) ?? null
  })
  // A docked group's own directory governs the session, exactly as it does for
  // a session already inside a project. A plain group has no directory to show,
  // but the user still just clicked ITS "+", so name it — otherwise the landing
  // page says nothing at all about where the session is headed.
  let dockedGroup = $derived(!sid ? ($sessionGroups.find(g => g.id === $pendingGroupId) ?? null) : null)
  let workingDir = $derived(
    sid
      ? (project?.working_dir || $chatWorkingDir[sid] || currentSession?.working_dir || '')
      : (project?.working_dir || $pendingWorkingDir),
  )
  let permMode = $derived($chatPermMode[sid] || currentSession?.permission_mode || $globalPermissionMode)
  // Effective show-reasoning for this session: live store > session record > default true.
  let showReasoning = $derived($chatShowReasoning[sid] ?? currentSession?.show_reasoning ?? true)
  let ctxUsage = $derived(Number($chatContextUsage[sid] ?? currentSession?.context_usage ?? 0))
  // Session goal chip: usage while active, status label otherwise. null/absent
  // hides the chip entirely.
  let goal = $derived($chatGoal[sid] ?? null)
  let goalChip = $derived.by(() => {
    if (!goal) return ''
    const compact = (n: number) =>
      n >= 1_000_000 ? `${(n / 1_000_000).toFixed(1).replace(/\.0$/, '')}M`
      : n >= 1_000 ? `${(n / 1_000).toFixed(1).replace(/\.0$/, '')}K`
      : `${n}`
    if (goal.status === 'active') {
      if (goal.token_budget > 0) return `${compact(goal.tokens_used ?? 0)}/${compact(goal.token_budget)}`
      const s = goal.time_used_seconds ?? 0
      return s < 60 ? `${s}s` : s < 3600 ? `${Math.floor(s / 60)}m` : `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`
    }
    return String(goal.status ?? '').replace('_', ' ')
  })

  function cap(s: string): string {
    return s ? s[0].toUpperCase() + s.slice(1) : s
  }

  // ── model + reasoning pickers ──────────────────────────────────────────────
  // PR5/PR6: the flat ModelEntry list is gone — read the two-level endpoint
  // view and flatten it into model rows the picker can show. Each row carries
  // the composite id "<endpoint>::<model>" so pickModel can pass it to
  // updateSessionModel, which resolves it via cfg.EntryByModel (composite-id
  // aware since PR2).
  let models = $state<{ id: string; model: string; endpoint: string }[]>([])
  // Composite id of the configured default entry (EndpointsResponse.default),
  // refreshed together with the model list.
  let defaultModelId = $state('')
  let modelMenu = $state(false)
  let reasonMenu = $state(false)
  let agentMenu = $state(false)
  let permMenu = $state(false)

  // The model menu groups rows under their endpoint, like the design mock.
  let modelGroups = $derived.by(() => {
    const out: { endpoint: string; items: { id: string; model: string }[] }[] = []
    for (const m of models) {
      let g = out.find(x => x.endpoint === m.endpoint)
      if (!g) { g = { endpoint: m.endpoint, items: [] }; out.push(g) }
      g.items.push({ id: m.id, model: m.model })
    }
    return out
  })

  // Composite id of the row the menu should highlight as current. Rows are
  // unique by composite id but NOT by model name (two endpoints may expose
  // the same model, #2141), so matching by name alone lights up both — prefer
  // the composite id whenever one is known: the session's own binding
  // (model_id), the pending pick for a yet-to-be-created session, or the
  // configured default for an unbound session. Empty means identity unknown
  // (legacy bare-model binding) and the menu falls back to name matching.
  let activeModelId = $derived.by(() => {
    const bound = currentSession?.model_id ?? ''
    if (bound.includes('::')) return bound
    if (bound) return ''
    if (!sid) return $pendingModel || defaultModelId
    // Unbound session: it runs on the default entry — trust that only while
    // the default still points at the model the session displays.
    return defaultModelId.split('::').pop() === modelName ? defaultModelId : ''
  })

  // ── agent assignment ───────────────────────────────────────────────────────
  // agent_profile can be (re)assigned right up until the session's first turn
  // runs — the server 409s a rebind past that point, since a turn may already
  // have applied the old profile's system prompt/tool allowlist (see
  // handleUpdateSessionAgentProfile). Before that point — no session yet, or
  // an existing session with turn_count 0 — the chip stays a live picker;
  // once locked, it becomes a read-only label of the session's own profile.
  let agents = $state<api.Agent[]>([])
  let agentLocked = $derived(!!currentSession && ((currentSession as any)?.turn_count ?? 0) > 0)
  let sessionAgent = $derived((currentSession as any)?.agent_profile ?? '')
  // Which id is "current" right now: the session's own profile once one
  // exists (even pre-lock), else the pending pick for the session about to be
  // created on send (ensureActiveSession prefers pendingAgent — what the
  // sidebar's "+" caret or this picker chose for THIS landing page — and falls
  // back to the globally active agent).
  let effectiveAgentId = $derived(currentSession ? sessionAgent : ($pendingAgent || $activeAgent))
  // Empty when the default agent applies — the chip only renders for an
  // expert agent.
  let agentLabel = $derived.by(() => {
    if (!effectiveAgentId || effectiveAgentId === 'default') return ''
    return agents.find(a => a.id === effectiveAgentId)?.name ?? effectiveAgentId
  })
  async function pickAgent(id: string) {
    agentMenu = false
    if (currentSession) {
      if (id !== sessionAgent) {
        try {
          await api.updateSessionAgentProfile(sid, id)
          sessions.update(list => list.map((s: any) => s.id === sid ? { ...s, agent_profile: id } : s))
        } catch (e: any) {
          showToast(e.message ?? 'Failed to change agent', 'error')
        }
      }
    } else {
      // Overwrite the sidebar caret's pick too, or ensureActiveSession would
      // prefer the parked one over what the composer now displays.
      activeAgent.set(id)
      pendingAgent.set(id)
    }
    queueMicrotask(() => textareaEl?.focus())
  }
  let dirSaving = $state(false)
  let pickerOpen = $state(false)
  let pickerMode = $state<'folder' | 'file'>('folder')
  const reasoningLevels = ['off', 'low', 'medium', 'high', 'xhigh', 'max']
  const showReasoningIcon = $derived(showReasoning ? 'ant-design:eye-outlined' : 'ant-design:eye-invisible-outlined')

  // Loaded at mount AND re-fetched every time the model menu opens: the list
  // otherwise freezes at page load, so an endpoint/model configured in
  // Settings never appeared (and rows for since-edited endpoints kept stale
  // composite ids that no longer resolve) until a full reload (#2066).
  // The sequence guard drops out-of-order responses — reopening the menu
  // quickly fires overlapping fetches, and a slow first response must not
  // overwrite a newer one.
  let modelsFetchSeq = 0
  async function refreshModels() {
    const seq = ++modelsFetchSeq
    try {
      const ep = await api.getEndpoints()
      if (seq !== modelsFetchSeq) return
      const flat: { id: string; model: string; endpoint: string }[] = []
      for (const e of ep.endpoints) {
        for (const m of e.models) {
          flat.push({ id: `${e.id}::${m.model}`, model: m.model, endpoint: e.id })
        }
      }
      models = flat
      // Default is echoed verbatim from config — a hand-written file may
      // carry a bare model name, which no menu row's composite id can ever
      // equal. Treat that as identity-unknown so name matching still applies.
      defaultModelId = ep.default?.includes('::') ? ep.default : ''
    } catch { /* keep the previous list */ }
  }

  onMount(async () => {
    await refreshModels()
    try { skills = await api.listSkills() } catch { /* leave empty */ }
    try { agents = await api.listAgents() } catch { /* leave empty */ }
    try { workflows = await api.listWorkflows() } catch { /* leave empty */ }
    try {
      const data = await api.listMcpServers()
      mcpServerNames = (data.servers ?? [])
        .filter((s: any) => s.status === 'connected')
        .map((s: any) => s.name)
    } catch { /* leave empty */ }
  })

  async function pickModel(m: { id: string; model: string; endpoint: string }) {
    modelMenu = false
    if (!sid) {
      // No session yet: remember the pick for the session about to be
      // auto-created (ensureActiveSession in ChatView), mirroring pickAgent.
      // Silently returning here made the menu look broken (#2066).
      pendingModel.set(m.id)
      queueMicrotask(() => textareaEl?.focus())
      return
    }
    try {
      // m.id is the composite id "<endpoint>::<model>" — the backend's
      // handleUpdateSessionModel resolves it via cfg.EntryByModel (composite-
      // id aware since PR2), binding the session to that endpoint's sender.
      const res = await api.updateSessionModel(sid, m.id)
      sessions.update(list => list.map((s: any) => s.id === sid ? { ...s, model: res.model, model_id: res.model_id } : s))
      chatModel.update(mx => ({ ...mx, [sid]: res.model }))
    } catch (e: any) {
      showToast(e.message ?? 'Failed to switch model', 'error')
    }
  }

  async function pickReasoning(level: string) {
    if (!sid) return
    try {
      await api.updateSessionReasoningEffort(sid, level)
      chatReasoningEffort.update(r => ({ ...r, [sid]: level }))
      // Off has no trace to show — the server forces show_reasoning off too
      // (see handleUpdateSessionReasoningEffort); mirror it locally so the
      // toggle doesn't flash "on" until the session_update broadcast lands.
      if (level === 'off') {
        chatShowReasoning.update(r => ({ ...r, [sid]: false }))
      }
    } catch (e: any) {
      showToast(e.message ?? 'Failed to set reasoning', 'error')
    }
  }

  async function toggleShowReasoning() {
    if (!sid || reasoning === 'off') return
    const next = !showReasoning
    try {
      await api.updateSessionShowReasoning(sid, next)
      chatShowReasoning.update(r => ({ ...r, [sid]: next }))
    } catch (e: any) {
      showToast(e.message ?? 'Failed to toggle reasoning visibility', 'error')
    }
  }

  // Explicit dropdown for the three engine modes (the old chip cycled on
  // click, which hid what the next state would be — #1114's strict mode is a
  // plain menu row now).
  const PERM_MODES = ['interactive', 'auto', 'strict']
  const PERM_LABEL_KEY: Record<string, string> = {
    interactive: 'chat.ask_mode', auto: 'chat.auto_mode', strict: 'chat.strict_mode',
  }
  async function pickPermMode(next: string) {
    permMenu = false
    if (!sid || next === permMode) return
    try {
      await api.updateSessionPermissionMode(sid, next)
      chatPermMode.update(m => ({ ...m, [sid]: next }))
    } catch (e: any) {
      showToast(e.message ?? 'Failed to switch permission mode', 'error')
    }
  }

  function closeMenus() { modelMenu = false; reasonMenu = false; agentMenu = false; permMenu = false }

  // Shared by the typed input and the folder picker: PATCH the session working
  // dir and store the canonical path the server resolved (~ expanded, absolute).
  // Returns whether it succeeded so callers can close their own UI.
  async function applyWorkingDir(dir: string): Promise<boolean> {
    // Landing page: park the pick instead. ensureActiveSession consumes it to
    // decide which project the session is born in, and the server canonicalises
    // the path there (the pickers already hand back an absolute one).
    if (!sid) {
      pendingWorkingDir.set(dir)
      return true
    }
    dirSaving = true
    try {
      const res = await api.updateSessionWorkingDir(sid, dir)
      chatWorkingDir.update(w => ({ ...w, [sid]: res.working_dir }))
      showToast(`${$t('chat.dir_set_toast')} ${shortDir(res.working_dir)}`, 'success')
      return true
    } catch (e: any) {
      showToast(e.message ?? 'Failed to set working directory', 'error')
      return false
    } finally {
      dirSaving = false
    }
  }

  async function openPicker() {
    // The chip's onclick stops propagation, so the window-level closeMenus
    // never runs — close them here or a dropdown stays open behind the modal.
    closeMenus()
    // Desktop shell: use the OS folder dialog. Plain web: open the in-app tree.
    if (get(nativeShell)) {
      try {
        const res = await api.nativePickFolder(workingDir)
        if (!res.cancelled && res.path) await applyWorkingDir(res.path)
      } catch (e: any) {
        showToast(e.message ?? 'Failed to open folder dialog', 'error')
      }
      return
    }
    pickerMode = 'folder'
    pickerOpen = true
  }

  // The in-app picker either attaches a file (openAttach) or sets the session
  // working directory, depending on the mode it was opened in.
  async function onPickerSelect(path: string) {
    if (pickerMode === 'file') {
      attachLocalFile(path)
      closePicker()
      return
    }
    if (await applyWorkingDir(path)) closePicker()
  }

  // Dismissing the modal must hand focus back to the composer; otherwise it
  // lands on <body> and the next keystroke goes nowhere.
  function closePicker() {
    pickerOpen = false
    queueMicrotask(() => textareaEl?.focus())
  }

  // Show only the last two path segments so a long working dir doesn't push
  // the chip row onto a second line. Full path is in the title tooltip.
  function shortDir(p: string): string {
    if (!p) return ''
    const parts = p.split('/').filter(Boolean)
    return parts.length <= 2 ? p : '…/' + parts.slice(-2).join('/')
  }

  // queued=true parks the message as its own follow-up turn instead of steering
  // the turn in flight (Cmd/Ctrl+Enter — the web counterpart of the TUI's
  // Ctrl+Q). Idle it makes no difference: the server just starts the turn.
  function send(queued = false) {
    if (!text.trim() && attachments.length === 0) return
    // Don't send while an attachment upload is still in flight — the file
    // would be dropped and re-appear on the next message.
    if (attachments.some(a => a.uploading)) {
      showToast($t('chat.upload_in_progress'), 'error')
      return
    }
    // Everything staged rides one WS message bounded by wsMaxMessageSize
    // (4 MB server-side). Compressed images are ~600KB each, but exempt
    // GIFs/SVGs and large batches can still aggregate past the cap — refuse
    // cleanly here instead of letting the server drop the connection (1009)
    // and losing the whole message.
    let payload = text.length
    for (const a of attachments) payload += a.data_url?.length ?? 0
    if (payload > 3_500_000) {
      showToast($t('chat.attach_batch_too_big'), 'error')
      return
    }
    const v = text.trim()
    const files = attachments.length ? [...attachments] : undefined
    pushHistory(sid, v)
    // Enter sends whenever no menu row is highlighted, so the menu can outlive
    // the message it was opened over. Close it with the text it belongs to.
    hideSlashMenu()
    text = ''
    attachments = []
    if (onSend) {
      onSend(v, files, queued)
    } else {
      running.set(true)
    }
  }

  function stop() {
    const s = get(activeSessionId)
    if (s) ws.interrupt(s)
    running.set(false)
  }

  // ── Input history (↑/↓ recall of previously sent messages) ────────────────
  // Keyed by session id, like the draft above, so recall never surfaces
  // another conversation's messages. Plain object — not rendered, only read
  // from keyboard handlers.
  let sentHistory: Record<string, string[]> = {}
  let historyIndex = $state<number | null>(null)
  let historyDraft = ''

  function pushHistory(forSid: string, sent: string) {
    if (!forSid || !sent) return
    const list = sentHistory[forSid] ?? (sentHistory[forSid] = [])
    if (list[list.length - 1] !== sent) list.push(sent)
    historyIndex = null
  }

  function caretAtStart(el: HTMLTextAreaElement): boolean {
    return el.selectionStart === 0 && el.selectionEnd === 0
  }
  function caretAtEnd(el: HTMLTextAreaElement): boolean {
    return el.selectionStart === el.value.length && el.selectionEnd === el.value.length
  }

  // Recall the previous (older) sent message. Only armed when the caret sits
  // at the very start of the textarea so it doesn't hijack normal cursor
  // movement inside a multi-line draft.
  function recallOlder() {
    const list = sentHistory[sid] ?? []
    if (list.length === 0) return
    if (historyIndex === null) {
      historyDraft = text
      historyIndex = list.length - 1
    } else if (historyIndex > 0) {
      historyIndex -= 1
    } else {
      return
    }
    text = list[historyIndex]
    queueMicrotask(() => textareaEl?.setSelectionRange(text.length, text.length))
  }

  // Recall the next (newer) sent message, or restore the in-progress draft
  // once history is exhausted.
  function recallNewer() {
    if (historyIndex === null) return
    const list = sentHistory[sid] ?? []
    if (historyIndex < list.length - 1) {
      historyIndex += 1
      text = list[historyIndex]
    } else {
      historyIndex = null
      text = historyDraft
    }
    queueMicrotask(() => textareaEl?.setSelectionRange(text.length, text.length))
  }

  function onKeydown(e: KeyboardEvent) {
    // While an IME composition is active (CJK input via pinyin/kana/etc.), let
    // the IME own every key. The Enter that confirms a candidate must not also
    // send the message, and arrows must navigate candidates, not the slash menu.
    // keyCode 229 covers browsers that don't set isComposing on the final key.
    if (e.isComposing || e.keyCode === 229) return
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

    // Backspace on an empty box un-assigns the picked agent (back to Default)
    // instead of doing nothing — only while it's still changeable (agentLocked
    // false) and there's actually something assigned to clear. Lets the user
    // immediately reselect via "@" without hunting for the dropdown.
    if (e.key === 'Backspace' && text === '' && !slashMenu && !agentLocked && agentLabel) {
      e.preventDefault()
      pickAgent('default')
      return
    }

    // Enter sends (mid-turn: steers); Cmd/Ctrl+Enter queues as the next turn;
    // Shift+Enter falls through to the textarea's own newline. See composerKeys.
    const intent = submitIntent(e)
    if (intent) {
      e.preventDefault()
      send(intent === 'queue')
      return
    }

    // History recall — armed only at the start/end of the textarea so it
    // doesn't hijack cursor movement inside a multi-line draft.
    if (e.key === 'ArrowUp' && textareaEl && caretAtStart(textareaEl)) {
      e.preventDefault()
      recallOlder()
      return
    }
    if (e.key === 'ArrowDown' && textareaEl && historyIndex !== null && caretAtEnd(textareaEl)) {
      e.preventDefault()
      recallNewer()
      return
    }
  }

  // Click outside to close slash menu.
  function onWindowClick(e: MouseEvent) {
    const target = e.target as HTMLElement
    if (slashMenu && !target.closest('.skill-menu')) {
      hideSlashMenu()
    }
    closeMenus()
  }
</script>

<svelte:window onclick={onWindowClick} />

<div class="composer">
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
            <span class="attach-chip" class:uploading={a.uploading} title={a.name}>
              <iconify-icon icon={a.uploading ? 'ant-design:loading-outlined' : 'ant-design:paper-clip-outlined'} width="12"></iconify-icon>
              <span class="attach-name">{a.name}</span>
              {#if !a.uploading}
                <button class="attach-x" title={$t('chat.remove')} onclick={() => removeAttachment(i)}>
                  <iconify-icon icon="ant-design:close-outlined" width="11"></iconify-icon>
                </button>
              {/if}
            </span>
          {/each}
        </div>
      {/if}
      <div class="input-row">
        {#if agentLabel}
        {#if agentLocked}
        <span class="agent-chip" title={$t('composer.session_agent')}>
          <span class="agent-at">@</span>
          <span class="agent-name">{agentLabel}</span>
        </span>
        {:else}
        <div class="picker">
          <button class="agent-chip pickable" title={$t('composer.assign_agent')} onclick={(e) => { e.stopPropagation(); const open = agentMenu; closeMenus(); agentMenu = !open }}>
            <span class="agent-at">@</span>
            <span class="agent-name">{agentLabel}</span>
          </button>
          {#if agentMenu}
            <div class="menu agent-menu" onclick={(e) => e.stopPropagation()}>
              <div class="menu-label">{$t('composer.assign_agent')}</div>
              <button class="menu-item" class:active={effectiveAgentId === 'default'} onclick={() => pickAgent('default')}>
                <span class="mi-name">Default</span>
              </button>
              {#each agents as a (a.id)}
                <button class="menu-item" class:active={effectiveAgentId === a.id} onclick={() => pickAgent(a.id)}>
                  <span class="mi-name">{a.name}</span>
                </button>
              {/each}
              <div class="menu-divider"></div>
              <button class="menu-item manage" onclick={() => { agentMenu = false; view.set('agents') }}>
                <iconify-icon icon="ant-design:plus-outlined" width="12"></iconify-icon>
                <span class="mi-name">{$t('agents.create')}</span>
              </button>
            </div>
          {/if}
        </div>
        {/if}
        {/if}
        <textarea
          bind:this={textareaEl}
          rows={1}
          placeholder={isStreaming || $running ? $t('chat.placeholder_running') : $t('chat.placeholder')}
          bind:value={text}
          onkeydown={onKeydown}
          oninput={onInput}
          onpaste={onPaste}
        ></textarea>
        {#if isStreaming || $running}
          <!-- Mid-turn: Stop interrupts the running turn; Send stays available
               so a follow-up message steers the turn in flight (rides the
               running Agent's Inbox server-side), and Cmd/Ctrl+Enter queues it
               as a separate turn instead (the server's steer queue). -->
          <button class="stop-btn" title={$t('chat.stop')} onclick={stop}>
            <span class="stop-sq"></span>
          </button>
        {/if}
        <!-- Arrow-wrapped: a bare `onclick={send}` would hand the MouseEvent to
             the `queued` parameter and queue every click. -->
        <button class="send-btn" title={isStreaming || $running ? $t('chat.send_or_queue_hint') : $t('chat.send')} aria-label={$t('chat.send')} onclick={() => send()}>
          <iconify-icon icon="lucide:send" width="16"></iconify-icon>
        </button>
      </div>
      {#if slashMenu}
        <div class="skill-menu" bind:this={skillMenuEl}>
          {#each filteredItems() as item, i (item.kind + ':' + (item.kind === 'builtin' ? item.name : item.kind === 'skill' ? item.skill.name : item.kind === 'workflow' ? item.workflow.name : item.kind === 'mcp-server' ? item.name : item.kind === 'agent' ? item.id : item.kind === 'agent-create' ? '' : item.server + '/' + item.tool.name))}
            <button
              class="skill-menu-item"
              class:active={i === slashActiveIndex}
              onclick={() => selectItem(item)}
            >
              {#if item.kind === 'builtin'}
                <span class="skill-name">/{item.name}</span>
                <span class="skill-desc">{$t(item.descKey)}</span>
              {:else if item.kind === 'skill'}
                <span class="skill-name">/{item.skill.name}</span>
                {#if item.skill.desc}
                  <span class="skill-desc">{item.skill.desc}</span>
                {/if}
              {:else if item.kind === 'workflow'}
                <span class="skill-name">{item.workflow.name}</span>
                {#if item.workflow.description}
                  <span class="skill-desc">{item.workflow.description}</span>
                {/if}
              {:else if item.kind === 'agent'}
                <span class="skill-name">@{item.name}</span>
                <span class="skill-desc">{$t('composer.assign_agent')}</span>
              {:else if item.kind === 'agent-create'}
                <span class="skill-name create">
                  <iconify-icon icon="ant-design:plus-outlined" width="12"></iconify-icon>
                  {$t('agents.create')}
                </span>
              {:else if item.kind === 'mcp-server'}
                <span class="skill-name">/mcp/{item.name}</span>
                <span class="skill-desc">{$t('composer.label_mcp_server')}</span>
              {:else}
                <span class="skill-name">mcp__{item.server}__{item.tool.name}</span>
                {#if item.tool.description}
                  <span class="skill-desc">{item.tool.description}</span>
                {/if}
              {/if}
            </button>
          {:else}
            <div class="skill-menu-empty">
              {slashMode === 'skills' ? $t('composer.no_match_skills') : slashMode === 'workflows' ? $t('composer.no_match_workflows') : slashMode === 'mcp-servers' ? $t('composer.no_match_servers') : $t('composer.no_match_tools')}
            </div>
          {/each}
        </div>
      {/if}
      <div class="meta-row">
        <input
          bind:this={fileInputEl}
          type="file"
          multiple
          style="display:none"
          onchange={onFilesPicked}
        />
        <button class="meta-chip" title={$t('chat.attach_file')} onclick={openAttach}>
          <iconify-icon icon="ant-design:paper-clip-outlined" width="13"></iconify-icon>
        </button>
        <div class="picker">
          <button class="meta-chip" onclick={(e) => { e.stopPropagation(); const open = modelMenu; closeMenus(); modelMenu = !open; if (!open) void refreshModels() }}>
            <iconify-icon icon="ant-design:robot-outlined" width="13"></iconify-icon>
            <span class="mono">{modelName}</span>
            <iconify-icon icon="lucide:chevron-down" width="12"></iconify-icon>
          </button>
          {#if modelMenu}
            <div class="menu" onclick={(e) => e.stopPropagation()}>
              {#if models.length === 0}
                <div class="menu-empty">{$t('chat.no_models')}</div>
              {:else}
                {#each modelGroups as g (g.endpoint)}
                  <div class="menu-label mono">{g.endpoint}</div>
                  {#each g.items as m (m.id)}
                    <button class="menu-item" class:active={activeModelId ? m.id === activeModelId : m.model === modelName} onclick={() => pickModel({ id: m.id, model: m.model, endpoint: g.endpoint })}>
                      <span class="mi-name mono">{m.model}</span>
                    </button>
                  {/each}
                {/each}
                <div class="menu-divider"></div>
                <button class="menu-item manage" onclick={() => { modelMenu = false; settingsModalOpen.set(true) }}>
                  <span class="mi-name">{$t('composer.manage_models')}</span>
                </button>
              {/if}
            </div>
          {/if}
        </div>
        <div class="picker">
          <button class="meta-chip" onclick={(e) => { e.stopPropagation(); const open = reasonMenu; closeMenus(); reasonMenu = !open }}>
            <span>{cap(reasoning)}</span>
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
              <button
                class="menu-item toggle-item"
                disabled={reasoning === 'off'}
                onclick={() => toggleShowReasoning()}
              >
                <span class="mi-name">{$t('chat.show_reasoning')}</span>
                <span class="toggle" class:on={showReasoning && reasoning !== 'off'}>
                  <span class="toggle-knob"></span>
                </span>
              </button>
            </div>
          {/if}
        </div>
        {#if workingDir && project && (sid || dockedGroup)}
          <span class="meta-chip static" title={$t('chat.dir_from_project').replace('{name}', project.name) + ` — ${workingDir}`}>
            <iconify-icon icon="ant-design:folder-outlined" width="13"></iconify-icon>
            <span class="mono dir-path">{shortDir(workingDir)}</span>
            <span class="dir-owner">{project.name}</span>
          </span>
        {:else if !sid && !dockedGroup}
          <!-- Only the landing page offers a choice, and choosing there is what
               files the session under a project. Once a session exists the
               directory is the project's to set: a task that could point itself
               at a repo ran its tools there while its memory stayed in the
               shared tier, which is the split this removes. -->
          <button
            class="meta-chip"
            class:empty={!workingDir}
            title={workingDir ? $t('chat.dir_change') + ` — ${workingDir}` : $t('chat.dir_pick')}
            disabled={dirSaving}
            onclick={(e) => { e.stopPropagation(); openPicker() }}
          >
            <iconify-icon icon="ant-design:folder-outlined" width="13"></iconify-icon>
            {#if workingDir}
              <span class="mono dir-path">{shortDir(workingDir)}</span>
            {:else}
              <span>{$t('chat.dir_pick')}</span>
            {/if}
            {#if project}
              <span class="dir-owner">{project.name}</span>
            {/if}
          </button>
        {:else if workingDir}
          <!-- A task's directory, read-only: where its tools run, decided by the
               server's workspace setting rather than by this session. -->
          <span class="meta-chip static" title={$t('chat.dir_task_fixed') + ` — ${workingDir}`}>
            <iconify-icon icon="ant-design:folder-outlined" width="13"></iconify-icon>
            <span class="mono dir-path">{shortDir(workingDir)}</span>
          </span>
        {/if}
        {#if dockedGroup && !workingDir}
          <!-- A plain group: no directory to inherit, but naming it is the only
               signal on this page that the session is being filed there. -->
          <span class="meta-chip static" title={$t('chat.dir_from_project').replace('{name}', dockedGroup.name)}>
            <iconify-icon icon="ant-design:folder-outlined" width="13"></iconify-icon>
            <span class="dir-owner">{dockedGroup.name}</span>
          </span>
        {/if}
        {#if goalChip}
          <span class="meta-chip static" title={goal?.objective ?? ''}>
            <span>{$t('chat.goal')}</span>
            <span class="mono">{goalChip}</span>
          </span>
        {/if}
        <span style="margin-left:auto;"></span>
        <span class="meta-chip static" title={$t('chat.context')}>
          <iconify-icon icon="lucide:clock" width="13"></iconify-icon>
          <span>{$t('chat.context')}</span>
          <span class="mono ctx-pct">{ctxUsage}%</span>
        </span>
        <div class="picker">
          <button class="meta-chip" title={$t('chat.perm_toggle_hint')} onclick={(e) => { e.stopPropagation(); const open = permMenu; closeMenus(); permMenu = !open }}>
            <span class="perm-dot" data-mode={permMode}></span>
            <span>{$t(PERM_LABEL_KEY[permMode] ?? 'chat.ask_mode')}</span>
            <iconify-icon icon="lucide:chevron-down" width="12"></iconify-icon>
          </button>
          {#if permMenu}
            <div class="menu perm-menu" onclick={(e) => e.stopPropagation()}>
              {#each PERM_MODES as mode}
                <button class="menu-item" class:active={mode === permMode} onclick={() => pickPermMode(mode)}>
                  <span class="perm-dot" data-mode={mode}></span>
                  <span class="mi-name">{$t(PERM_LABEL_KEY[mode])}</span>
                </button>
              {/each}
            </div>
          {/if}
        </div>
      </div>
    </div>
  </div>
</div>

{#if pickerOpen}
  <FolderPickerModal
    initialPath={workingDir}
    mode={pickerMode}
    onSelect={onPickerSelect}
    onClose={closePicker}
  />
{/if}

<style>
.composer {
  flex: 0 0 auto;
  background: var(--bg-layout);
}
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
.menu-item:hover { background: var(--active-blue-bg); }
.menu-item.active { background: var(--active-blue-bg); }
.menu-divider { height: 1px; background: var(--border-secondary); margin: 4px 0; }
.menu-item.toggle-item { flex-direction: row; justify-content: space-between; align-items: center; }
.menu-item.toggle-item:disabled { cursor: default; opacity: 0.5; }
.menu-item.toggle-item:disabled:hover { background: none; }
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
.menu-empty { padding: 8px 10px; font-size: 12px; color: var(--text-tertiary); }
.reasoning-eye { color: var(--success); }
.mono { font-family: var(--font-mono); }
.dir-path { max-width: 200px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
/* Names the project that owns this directory, so the chip explains itself
   without needing the tooltip — both for a session already inside a project
   and on the landing page, where it previews where the first message lands. */
.dir-owner {
  max-width: 120px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  padding-left: 5px; margin-left: 1px; border-left: 1px solid var(--border-secondary);
  color: var(--text-tertiary);
}
.input-wrap { max-width: var(--chat-content-max-width, 1080px); margin: 0 auto; padding: 8px 24px 14px; }
.input-card {
  background: var(--bg-container); border: 1px solid var(--border); border-radius: 14px;
  padding: 8px 10px; display: flex; flex-direction: column; gap: 6px;
  position: relative; box-shadow: var(--card-shadow);
}
.input-card:focus-within {
  border-color: var(--blue-6);
  box-shadow: 0 0 0 2px var(--focus-ring);
}
.input-card.drag-over {
  border-color: var(--blue-6);
  background: var(--row-hover);
  box-shadow: 0 0 0 2px var(--focus-ring);
}
.input-row { display: flex; align-items: flex-end; gap: 4px; }
textarea {
  border: none; outline: none; resize: none; font-size: 14px; line-height: 1.6;
  font-family: inherit; color: var(--text); background: transparent;
  flex: 1; min-width: 0; margin: 4px 4px 5px;
  max-height: 156px; overflow-y: hidden; overflow-x: hidden; min-height: 24px; /* ≈ MIN_TEXTAREA_PX in autoResize */
}
.agent-chip {
  display: inline-flex; align-items: center; gap: 6px;
  height: 28px; padding: 0 10px 0 5px; margin: 0 2px 2px 0;
  background: var(--active-blue-bg); border: none; border-radius: 8px;
  color: var(--blue-6); font-size: 12px; font-weight: 600;
  font-family: inherit; flex: 0 0 auto;
}
.agent-chip.pickable { cursor: pointer; }
.agent-chip.pickable:hover { background: var(--row-hover); }
.agent-at {
  width: 16px; height: 16px; border-radius: 5px;
  background: var(--blue-6); color: var(--on-accent);
  display: grid; place-items: center; font-size: 11px; font-weight: 700; line-height: 1;
}
.agent-name { max-width: 120px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.agent-menu { min-width: 220px; }
.menu-label {
  font-size: 11px; font-weight: 600; color: var(--text-secondary);
  padding: 6px 10px 4px;
}
.menu-item.manage { flex-direction: row; align-items: center; gap: 6px; }
.menu-item.manage .mi-name { color: var(--blue-6); }
.perm-menu .menu-item { flex-direction: row; align-items: center; gap: 8px; }
.perm-dot { width: 7px; height: 7px; border-radius: 50%; flex: 0 0 auto; background: var(--text-quaternary); }
.perm-dot[data-mode="auto"] { background: var(--success); }
.perm-dot[data-mode="interactive"] { background: var(--warning); }
.perm-dot[data-mode="strict"] { background: var(--error); }
.attachments { display: flex; flex-wrap: wrap; gap: 6px; }
.attach-chip {
  display: inline-flex; align-items: center; gap: 5px; max-width: 200px;
  height: 24px; padding: 0 6px 0 8px; background: var(--surface-info); border: 1px solid var(--blue-2);
  border-radius: 6px; font-size: 12px; color: var(--text-secondary);
}
.attach-chip.uploading { opacity: 0.7; }
.attach-chip.uploading iconify-icon { animation: attach-spin 0.8s linear infinite; }
@keyframes attach-spin { to { transform: rotate(360deg); } }
.attach-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.attach-x {
  border: none; background: transparent; cursor: pointer; padding: 0;
  display: flex; align-items: center; color: var(--text-tertiary);
}
.attach-x:hover { color: var(--error); }
.meta-row {
  display: flex; align-items: center; gap: 12px; flex-wrap: wrap;
  padding: 7px 6px 2px; border-top: 1px solid var(--border-secondary);
}
.meta-chip {
  display: inline-flex; align-items: center; gap: 5px;
  padding: 2px 4px; border: none; border-radius: 6px; background: transparent;
  font-size: 12px; color: var(--text-secondary); cursor: pointer; font-family: inherit;
  white-space: nowrap;
}
.meta-chip:hover { color: var(--text); background: var(--hover-neutral); }
.meta-chip.static { cursor: default; }
.meta-chip.static:hover { color: var(--text-secondary); background: transparent; }
/* Nothing picked yet: reads as an invitation rather than as state. */
.meta-chip.empty { color: var(--text-tertiary); }
.ctx-pct { color: var(--text); font-weight: 600; }
.send-btn {
  width: 38px; height: 30px; flex: 0 0 auto; margin-bottom: 1px;
  border: none; background: var(--blue-6); border-radius: 9px;
  display: grid; place-items: center; color: var(--on-accent);
  cursor: pointer; font-family: inherit;
  box-shadow: 0 1px 2px var(--focus-ring);
}
.send-btn:hover { background: var(--blue-5); }
.send-btn:active { background: var(--blue-7); }
.stop-btn {
  width: 30px; height: 30px; flex: 0 0 auto; margin-bottom: 1px;
  border: 1px solid var(--error-border); background: var(--error-bg);
  border-radius: 9px; display: grid; place-items: center;
  cursor: pointer; font-family: inherit;
}
.stop-btn:hover { border-color: var(--error); }
.stop-sq { width: 10px; height: 10px; border-radius: 2px; background: var(--error); }

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
  background: var(--active-blue-bg);
}
.skill-name.create { color: var(--blue-6); display: inline-flex; align-items: center; gap: 6px; }
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
