<script lang="ts">
  import { t, tr } from '../../lib/i18n'
  import { activeSessionId, showToast, agentRunTrails } from '../../lib/stores'
  import type { SubAgentState, WorkflowTrailState } from '../../lib/stores'
  import { ws } from '../../lib/ws'
  import * as api from '../../lib/api'
  import { toolOpenState, applyToolToggle, keepOpenAction } from '../../lib/toolFold'
  import { sanitizeSpec, READ_ONLY_NODE_TYPES } from '../../lib/genui/guard'
  import GenuiBlock from '../genui/GenuiBlock.svelte'
  import AgentTrail from './AgentTrail.svelte'

  // Tracks which overwrite-undo buttons have already fired, keyed by tool id.
  let undone = $state<Record<string, boolean>>({})

  // Undo an overwrite: restore the pre-write version from the trash, moving the
  // just-written file into the trash first so the undo itself is reversible.
  async function undoOverwrite(undoId: string, toolId: string) {
    try {
      const res = await api.restoreTrash(undoId, 'backup')
      if (res.ok) {
        undone = { ...undone, [toolId]: true }
        showToast(tr('tools.undo_done'), 'success')
      }
    } catch (e: any) {
      showToast(`Undo failed: ${e.message}`, 'error')
    }
  }
  // A collapsible group of tool calls for one agent turn.
  // Accepts optional `tools` + `streaming` props for real data;
  // falls back to static prototype content when called without props.

  let { tools = null, streaming: groupStreaming = false }: {
    tools?: any[] | null,
    streaming?: boolean,
  } = $props()

  // Trails claimed by agent_id / run_id from the tool result's ui_payload —
  // the full sub-agent / workflow output, hydrated from the server and kept
  // current by the live event stream (see stores.agentRunTrails).
  let sessionTrails = $derived($agentRunTrails[$activeSessionId ?? ''])
  function subAgentTrail(tool: any): SubAgentState | undefined {
    const id = tool.ui_payload?.agent_id
    return id ? sessionTrails?.subAgents?.[id] : undefined
  }
  function workflowTrail(tool: any): WorkflowTrailState | undefined {
    const id = tool.ui_payload?.run_id
    return id ? sessionTrails?.workflows?.[id] : undefined
  }

  // Fold overrides for the nested workflow-agent rows, keyed
  // "runId/agentId" — driven only by the native <details> toggle, with the
  // override dropped when it realigns with the status default, so re-renders
  // re-asserting `open` can't swallow or invert a user's fold (same policy
  // as toolFold / SubAgentsCard / WorkflowsCard).
  let wfAgentFolds = $state<Record<string, boolean>>({})

  function wfAgentOpen(key: string, dflt: boolean): boolean {
    return wfAgentFolds[key] ?? dflt
  }

  function onWfAgentToggle(key: string, dflt: boolean, open: boolean) {
    if (open === dflt) {
      if (key in wfAgentFolds) {
        const next = { ...wfAgentFolds }
        delete next[key]
        wfAgentFolds = next
      }
    } else if (wfAgentFolds[key] !== open) {
      wfAgentFolds = { ...wfAgentFolds, [key]: open }
    }
  }

  function promoteTerminal() {
    const sid = $activeSessionId
    if (sid) ws.promoteSyncTerminal(sid)
  }

  function promoteSubAgent() {
    const sid = $activeSessionId
    if (sid) ws.promoteSyncSubAgent(sid)
  }

  const TOOL_ICONS: Record<string, string> = {
    grep: 'ant-design:search-outlined',
    glob: 'ant-design:search-outlined',
    read_file: 'ant-design:file-text-outlined',
    edit_file: 'ant-design:edit-outlined',
    write_file: 'ant-design:edit-outlined',
    bash: 'ant-design:code-outlined',
    terminal: 'ant-design:code-outlined',
    web_search: 'ant-design:global-outlined',
    web_fetch: 'ant-design:global-outlined',
    remember: 'ant-design:save-outlined',
    sub_agent: 'ant-design:partition-outlined',
    launch_agent: 'ant-design:partition-outlined',
  }

  function toolIcon(name: string): string {
    return TOOL_ICONS[name] ?? 'ant-design:tool-outlined'
  }

  // Localized card titles for well-known tools; anything else (MCP tools,
  // future additions) keeps its raw mono name, which is at least exact.
  const TOOL_TITLE_KEYS: Record<string, string> = {
    read_file: 'tools.title.read_file',
    write_file: 'tools.title.write_file',
    edit_file: 'tools.title.edit_file',
    terminal: 'tools.title.terminal',
    bash: 'tools.title.bash',
    grep: 'tools.title.grep',
    glob: 'tools.title.glob',
    web_search: 'tools.title.web_search',
    web_fetch: 'tools.title.web_fetch',
    remember: 'tools.title.remember',
    sub_agent: 'tools.title.sub_agent',
    launch_agent: 'tools.title.launch_agent',
    browser: 'tools.title.browser',
    todo_write: 'tools.title.todo_write',
    todowrite: 'tools.title.todo_write',
    skill: 'tools.title.skill',
  }

  // Build a friendly one-line summary of a tool's arguments instead of dumping
  // raw JSON. Falls back to compact JSON for unknown shapes.
  function argSummary(name: string, args: any): string {
    let a = args
    if (typeof a === 'string') {
      const s = a.trim()
      if (s.startsWith('{') || s.startsWith('[')) {
        try { a = JSON.parse(s) } catch { return s }
      } else {
        return s
      }
    }
    if (!a || typeof a !== 'object') return a == null ? '' : String(a)
    // Pick the most meaningful field per tool.
    const pick = (...keys: string[]) => {
      for (const k of keys) if (a[k] != null && a[k] !== '') return String(a[k])
      return ''
    }
    switch (name) {
      case 'web_search': return pick('query', 'q')
      case 'web_fetch':  return pick('url')
      // browser is a multiplexed tool: the sub-command (action) is the headline
      // — "replay zhihu-publish {…}" not a bare recording name or raw JSON —
      // followed by that action's salient argument.
      case 'browser': {
        const act = pick('action')
        if (!act) {
          const compact = JSON.stringify(a)
          return compact.length > 80 ? compact.slice(0, 77) + '…' : compact
        }
        const sel = a.frame ? `${a.frame} >>> ${pick('selector')}` : pick('selector')
        let detail = ''
        switch (act) {
          case 'navigate': detail = pick('url'); break
          case 'click': case 'hover': case 'clear': case 'scroll': case 'download':
            detail = sel; break
          case 'wait':
            detail = sel || (a.network_idle ? 'network idle' : (a.timeout_ms ? `${a.timeout_ms}ms` : '')); break
          case 'type':
            detail = sel + (a.text ? ` "${a.text}"` : ''); break
          case 'select':
            detail = sel + (a.value ? ` "${a.value}"` : ''); break
          case 'key': detail = pick('keys'); break
          case 'eval': detail = pick('js'); break
          case 'upload':
            detail = Array.isArray(a.files) && a.files.length ? a.files.join(', ') : sel; break
          case 'select_page':
            detail = a.index != null ? `#${a.index}` : ''; break
          case 'replay': case 'run_skill': {
            detail = pick('name')
            if (a.params && typeof a.params === 'object' && Object.keys(a.params).length) {
              detail += ' ' + JSON.stringify(a.params)
            }
            break
          }
          case 'record_start': case 'record_stop': case 'record_cancel':
            detail = pick('name'); break
          // observe / screenshot / ax / cookies / pages / back / close:
          // the action name alone says it all.
        }
        return detail ? `${act}  ${detail}` : act
      }
      case 'grep':       return pick('pattern', 'query') + (a.path ? `  ${a.path}` : '')
      case 'glob':       return pick('pattern', 'glob')
      case 'read_file': case 'write_file': case 'edit_file':
        return pick('path', 'file', 'filename')
      case 'bash': case 'terminal':
        return pick('command', 'cmd')
      case 'remember':   return pick('content', 'text', 'name')
      default: {
        const v = pick('query', 'path', 'command', 'url', 'name', 'pattern')
        if (v) return v
        const compact = JSON.stringify(a)
        return compact.length > 80 ? compact.slice(0, 77) + '…' : compact
      }
    }
  }

  // Pretty-print a result string when it is JSON; otherwise return as-is.
  function prettyResult(result: any): string {
    if (result == null) return ''
    if (typeof result !== 'string') {
      try { return JSON.stringify(result, null, 2) } catch { return String(result) }
    }
    const s = result.trim()
    if (s.startsWith('{') || s.startsWith('[')) {
      try { return JSON.stringify(JSON.parse(s), null, 2) } catch { return result }
    }
    return result
  }

  // web_search ui_payload → structured result list (title + url).
  function searchResults(tool: any): Array<{ title: string; url: string }> | null {
    const p = tool.ui_payload
    let arr: any = null
    if (p && Array.isArray(p.results)) arr = p.results
    else if (typeof tool.result === 'string') {
      try { const j = JSON.parse(tool.result); if (Array.isArray(j.results)) arr = j.results } catch { /* not json */ }
    }
    if (!arr) return null
    return arr.map((r: any) => ({
      title: r.title ?? r.name ?? r.url ?? '(untitled)',
      url: r.url ?? r.link ?? '',
    })).filter((r: any) => r.title || r.url)
  }

  // web_search ui_payload → which backend answered. 'duckduckgo' / 'bing' mean
  // the zero-key HTML-scrape path, whose results are markedly worse than a real
  // index; naming it is the only signal the user gets that a (free) key would
  // help. Mirrors the TUI card's dim meta — see cmd/octo/toolcards.go.
  function searchProvider(tool: any): string {
    const p = tool.ui_payload
    if (p && typeof p.provider === 'string') return p.provider
    if (typeof tool.result === 'string') {
      try { const j = JSON.parse(tool.result); if (typeof j.provider === 'string') return j.provider } catch { /* not json */ }
    }
    return ''
  }
  function searchScraped(tool: any): boolean {
    const p = searchProvider(tool)
    return p === 'duckduckgo' || p === 'bing'
  }

  // Per-tool right-aligned meta count ("12 matches" / "64 lines" / …), derived
  // from the result/payload since the server sends no explicit counter.
  function nonEmptyLines(s: string): number {
    return s ? s.split('\n').filter(l => l.trim() !== '').length : 0
  }
  function toolMeta(tool: any): string {
    if (!tool.done || tool.error) return ''
    const res = typeof tool.result === 'string' ? tool.result : ''
    switch (tool.name) {
      case 'web_search': {
        const r = searchResults(tool)
        if (!r) return ''
        const provider = searchProvider(tool)
        return provider ? `${r.length} results · ${provider}` : `${r.length} results`
      }
      case 'grep':       return res ? `${nonEmptyLines(res)} matches` : ''
      case 'read_file':  return res ? `${nonEmptyLines(res)} lines` : ''
      case 'bash': case 'terminal': {
        const out = (tool.stdout && tool.stdout.length) ? tool.stdout.join('\n') : res
        return out ? `${nonEmptyLines(out)} lines` : ''
      }
      case 'edit_file': {
        const diff = tool.ui_payload?.diff
        if (!diff) return ''
        const changes = diff.split('\n').filter((l: string) => l.startsWith('+') || l.startsWith('-')).length
        return changes ? `${changes} changes` : ''
      }
      case 'write_file': {
        const p = tool.ui_payload
        if (!p) return ''
        const lines = p.line_count ? `${p.line_count} lines` : ''
        const b = p.size_bytes ?? 0
        const bytes = b < 1024 ? `${b} B` : `${(b / 1024).toFixed(1)} KB`
        return [lines, bytes].filter(Boolean).join(' · ')
      }
      default: return ''
    }
  }

  // Group elapsed = sum of per-tool durations (only known for live calls; a
  // replayed history transcript has no timing so this stays empty there).
  function groupElapsed(ts: any[]): string {
    const total = ts.reduce((s, t) => s + (typeof t.elapsed === 'number' ? t.elapsed : 0), 0)
    return total > 0 ? `${total.toFixed(1)}s` : ''
  }

  // web_fetch returns the page body as a normal result even when the target
  // responded with an HTTP error — the tool succeeded, the page didn't. Detect
  // that "Warning: Target URL returned error NNN" line so the card can show it
  // as a warning instead of a green check + plain text.
  function fetchError(tool: any): string | null {
    if (tool.name !== 'web_fetch' || tool.error) return null
    const r = typeof tool.result === 'string' ? tool.result : ''
    const m = r.match(/Target URL returned error\s*([0-9]{3}[^\n]*)/i)
    return m ? m[1].trim() : null
  }

  // A non-zero exit is not a tool error either — terminal reports it via the
  // structured ui_payload.status rather than tool.error, and appends a
  // trailing "[exit: …]" marker to the output (internal/tools/terminal.go).
  // Without this, a failed command got the same green check as a success,
  // with the marker buried behind the fold.
  function terminalFailure(tool: any): string | null {
    const p = tool.ui_payload
    if (!p || p.type !== 'terminal' || p.status !== 'failed' || tool.error) return null
    const src = typeof tool.result === 'string' ? tool.result
      : (typeof p.output_preview === 'string' ? p.output_preview : '')
    const m = src.match(/\[exit: ([^\]]+)\]\s*$/)
    if (!m) return 'failed'
    // The marker embeds Go's *exec.ExitError.Error() text verbatim: a normal
    // nonzero exit reads "exit status N" (not a bare "N"), a killed process
    // reads "signal: NAME". Strip the verbose "exit status" wrapper down to
    // "exit N"; "signal: NAME" already reads fine standalone.
    const reason = m[1].trim()
    const code = reason.match(/^exit status (\d+)$/)
    return code ? `exit ${code[1]}` : reason
  }

  // Per-tool open/closed override, seeded by the default (see lib/toolFold).
  // Binding <details open> straight to the default would let every streaming
  // re-render revert a manual collapse of the auto-opened last tool.
  let toolOpen = $state<Record<string, boolean>>({})

  // The default open state collapses the last card the instant the group stops
  // running (defaultToolOpen is false once streaming ends). Bound straight to
  // <details open>, that collapse yanked the page around right as the user was
  // reading the output — so on the running->not-running edge the last card is
  // *pinned* open instead and simply stays expanded.
  //
  // "Stops running" means no in-flight tool in the group right now — usually
  // the turn finishing, but the same dip also happens between sequential tool
  // rounds inside one turn (result received, next call still an LLM round-trip
  // away). When the next round starts, the 'clear' branch in keepOpenAction
  // drops the pins, so the previous card collapses as the new last tool opens —
  // the same handoff as mid-turn.
  //
  // A user click still closes a pinned card instantly (onToggle drops the pin);
  // keepOpenAction skips a card the user holds an explicit override on.
  //
  // This must be $effect.pre, not $effect: a post-DOM effect would let the
  // running->false render close the <details> first and force it back open a
  // flush later. That reopen fires a toggle whose open=true diverges from the
  // now-false default, so applyToolToggle would record it as a user "keep
  // open" override — which the 'clear' on the next round could not collapse.
  // Running before the DOM update keeps `open` true continuously, so no toggle
  // fires at all.
  let pinnedIds = $state<Record<string, boolean>>({})
  let prevRunning: boolean | undefined
  $effect.pre(() => {
    const ts = tools ?? []
    if (ts.length === 0) return
    const running = ts.some((t) => !t.done && !t.error)
    const action = keepOpenAction(prevRunning, running, ts, toolOpen, Object.keys(pinnedIds).length)
    prevRunning = running
    if (action?.kind === 'pin') {
      pinnedIds = { ...pinnedIds, [action.id]: true }
    } else if (action?.kind === 'clear') {
      pinnedIds = {}
    }
  })

  // Toggle handler that is pin-aware: while a card is pinned open, the only
  // genuine toggle is the user clicking to collapse it — drop the pin so the
  // native close sticks, and record no override either way (the pinned state
  // must never be mistaken for a user choice).
  function onToggle(tool: any, lastId: string | undefined, running: boolean, open: boolean) {
    if (pinnedIds[tool.id]) {
      if (!open) {
        const next = { ...pinnedIds }
        delete next[tool.id]
        pinnedIds = next
      }
      return
    }
    applyToolToggle(toolOpen, tool, lastId, running, open)
  }

  // todo_write renders its checklist from the tool args.
  function todoItems(tool: any): Array<{ status: string; content: string }> | null {
    if (tool.name !== 'todo_write' && tool.name !== 'todowrite') return null
    let a = tool.args
    if (typeof a === 'string') { try { a = JSON.parse(a) } catch { return null } }
    const list = a?.todos ?? a?.items ?? (Array.isArray(a) ? a : null)
    if (!Array.isArray(list)) return null
    return list.map((t: any) => ({ status: t.status ?? 'pending', content: t.content ?? t.text ?? String(t) }))
  }

  // Long output is NOT truncated: every body renders in full and its container
  // is capped at ~10 lines with a scrollbar (see the max-height rules below).
  // For terminal/bash the scroll starts pinned to the BOTTOM — errors and
  // summaries land at the end of shell output, not the top (#1106) — and stays
  // pinned while streaming until the user scrolls up.
  function pinBottom(node: HTMLElement) {
    let userScrolled = false
    const onScroll = () => {
      userScrolled = node.scrollHeight - node.scrollTop - node.clientHeight > 24
    }
    node.addEventListener('scroll', onScroll)
    const obs = new MutationObserver(() => {
      if (!userScrolled) node.scrollTop = node.scrollHeight
    })
    obs.observe(node, { childList: true, characterData: true, subtree: true })
    node.scrollTop = node.scrollHeight
    return {
      destroy() {
        obs.disconnect()
        node.removeEventListener('scroll', onScroll)
      },
    }
  }
</script>

{#if tools !== null && tools.length > 0}
  <!-- Real data rendering -->
  {@const lastId = tools[tools.length - 1]?.id}
  <!-- "running" reflects whether a tool is still in flight, NOT the group's
       message-level `streaming` flag: that flag stays true until the whole turn
       completes (finishAllTools), so a group whose tools all finished — or one
       rebuilt done-but-streaming on reconnect replay — would otherwise show a
       perpetual "running". -->
  {@const anyRunning = tools.some((t) => !t.done && !t.error)}
  <div class="tool-group">
    <div class="group-header">
      <iconify-icon icon="ant-design:tool-outlined" width="14" style="color:var(--text-tertiary)"></iconify-icon>
      <span class="hdr-label">{$t(tools.length === 1 ? 'tools.n_used_one' : 'tools.n_used').replace('{n}', String(tools.length))}</span>
      {#if anyRunning}
        <span style="margin-left:auto;display:flex;align-items:center;gap:5px;font-size:12px;color:var(--blue-6)">
          <iconify-icon icon="ant-design:loading-outlined" width="13" style="animation:octo-spin 0.8s linear infinite"></iconify-icon>
          {$t('tools.running')}
        </span>
      {:else}
        {@const elapsed = groupElapsed(tools)}
        {#if elapsed}<span class="hdr-time mono" style="margin-left:auto">{elapsed}</span>{/if}
      {/if}
    </div>

    {#each tools as tool (tool.id)}
      {@const meta = toolMeta(tool)}
      {@const todos = todoItems(tool)}
      {@const fErr = fetchError(tool)}
      {@const tErr = terminalFailure(tool)}
      <!-- Full arg text lives in the DOM either way — the CSS only visually
           ellipsizes it. Surfacing it via `title` + selectable text lets the
           user read/copy the whole thing despite the truncation. -->
      {@const argText = tool.summary || (tool.args ? argSummary(tool.name, tool.args) : '')}
      <details open={toolOpenState(toolOpen, tool, lastId, anyRunning) || !!pinnedIds[tool.id]} ontoggle={(e) => onToggle(tool, lastId, anyRunning, (e.currentTarget as HTMLDetailsElement).open)} class="tool-item">
        <summary class="tool-summary">
          <iconify-icon icon="lucide:chevron-right" width="13" class="chev" style="color:var(--text-tertiary)"></iconify-icon>
          <span class="tool-well" class:accent={tool.name === 'edit_file' || tool.name === 'write_file'}>
            <iconify-icon icon={toolIcon(tool.name)} width="16"></iconify-icon>
          </span>
          <div class="tool-head">
            <div class="tool-title-row">
              {#if TOOL_TITLE_KEYS[tool.name]}
                <span class="tool-title">{$t(TOOL_TITLE_KEYS[tool.name])}</span>
              {:else}
                <span class="tool-title mono">{tool.name}</span>
              {/if}
              {#if argText}
                <span class="tool-arg mono" title={argText} onclick={(e) => e.stopPropagation()}>{argText}</span>
              {/if}
            </div>
            {#if meta && !fErr && !tErr}<div class="tool-submeta">{meta}</div>{/if}
          </div>
          <span class="tool-status">
            {#if tool.error}
              <span class="st err">
                <iconify-icon icon="ant-design:close-circle-outlined" width="14"></iconify-icon>
                {$t('tools.failed')}
              </span>
            {:else if fErr}
              <span class="st warn">
                <iconify-icon icon="ant-design:warning-outlined" width="14"></iconify-icon>
                {fErr}
              </span>
            {:else if tErr}
              <span class="st warn">
                <iconify-icon icon="ant-design:warning-outlined" width="14"></iconify-icon>
                {tErr}
              </span>
            {:else if tool.done}
              <span class="st ok">
                <iconify-icon icon="lucide:check" width="13"></iconify-icon>
                {$t('tools.done')}
              </span>
            {:else}
              <span class="st run">
                <iconify-icon icon="ant-design:loading-outlined" width="13" style="animation:octo-spin 0.8s linear infinite"></iconify-icon>
                {$t('tools.running')}
                {#if tool.name === 'terminal' || tool.name === 'bash'}
                  <button class="promote-btn" onclick={(e) => { e.preventDefault(); e.stopPropagation(); promoteTerminal() }}>
                    {$t('tools.background')}
                  </button>
                {:else if tool.name === 'sub_agent'}
                  <button class="promote-btn" onclick={(e) => { e.preventDefault(); e.stopPropagation(); promoteSubAgent() }}>
                    {$t('tools.background')}
                  </button>
                {/if}
              </span>
            {/if}
          </span>
        </summary>

        <div class="tool-body"><div class="tool-body-inner">
        {#if tool.error}
          <div class="error-output mono">{tool.error}</div>
        {:else if fErr}
          <pre class="warning-output mono">{tool.result}</pre>
        {:else if todos}
          <div class="todo-list">
            {#each todos as step}
              <div class="todo-step">
                {#if step.status === 'completed'}
                  <iconify-icon icon="ant-design:check-circle-outlined" width="14" style="color:var(--success)"></iconify-icon>
                  <span class="todo-done">{step.content}</span>
                {:else if step.status === 'in_progress'}
                  <iconify-icon icon="ant-design:loading-outlined" width="14" style="color:var(--blue-6);animation:octo-spin 0.8s linear infinite"></iconify-icon>
                  <span>{step.content}</span>
                {:else}
                  <iconify-icon icon="lucide:circle" width="14" style="color:var(--text-quaternary)"></iconify-icon>
                  <span class="todo-pending">{step.content}</span>
                {/if}
              </div>
            {/each}
          </div>
        {:else if tool.name === 'sub_agent' && subAgentTrail(tool)}
          {@const trail = subAgentTrail(tool)!}
          <div class="trail-wrap">
            <AgentTrail steps={trail.steps} running={trail.status === 'running'} result={trail.result ?? ''} />
          </div>
        {:else if tool.name === 'workflow' && workflowTrail(tool)}
          {@const wt = workflowTrail(tool)!}
          <div class="trail-wrap">
            {#each wt.logs.filter(l => !l.startsWith('→ ') && !l.startsWith('✓ ')) as line}
              <div class="wf-log mono">{line}</div>
            {/each}
            {#each wt.agents as a (a.id)}
              <details class="wf-agent" open={wfAgentOpen(wt.id + '/' + a.id, a.status === 'running')}
                ontoggle={(e) => onWfAgentToggle(wt.id + '/' + a.id, a.status === 'running', (e.currentTarget as HTMLDetailsElement).open)}>
                <summary class="wf-agent-summary">
                  {#if a.status === 'running'}
                    <iconify-icon icon="ant-design:loading-outlined" width="12" style="color:var(--blue-6);animation:octo-spin 0.8s linear infinite"></iconify-icon>
                  {:else if a.status === 'error'}
                    <iconify-icon icon="ant-design:close-circle-outlined" width="12" style="color:var(--error)"></iconify-icon>
                  {:else}
                    <iconify-icon icon="ant-design:check-circle-outlined" width="12" style="color:var(--success)"></iconify-icon>
                  {/if}
                  <span class="wf-agent-id mono">{a.id}</span>
                  <span class="wf-agent-label">{a.label}</span>
                </summary>
                <div class="wf-agent-body">
                  <AgentTrail steps={a.steps} running={a.status === 'running'} result={a.reply ?? ''} resultError={a.error ?? ''} />
                </div>
              </details>
            {/each}
          </div>
        {:else if tool.ui_payload?.diff}
          <div class="diff-block">
            {#each tool.ui_payload.diff.split('\n') as line}
              {#if line.startsWith('@@')}
                <div class="diff-hdr mono">{line}</div>
              {:else if line.startsWith('-')}
                <div class="diff-line rm mono">{line}</div>
              {:else if line.startsWith('+')}
                <div class="diff-line add mono">{line}</div>
              {:else}
                <div class="diff-line mono" style="padding:1px 14px;color:var(--text-secondary)">{line}</div>
              {/if}
            {/each}
          </div>
          {#if tool.ui_payload?.undo_id}
            <button class="undo-btn" disabled={undone[tool.id]} onclick={() => undoOverwrite(tool.ui_payload.undo_id, tool.id)}>
              <iconify-icon icon="ant-design:undo-outlined" width="12"></iconify-icon>
              {undone[tool.id] ? $t('tools.undo_done') : $t('tools.undo_overwrite')}
            </button>
          {/if}
        {:else if tool.stdout && tool.stdout.length > 0}
          {@const full = tool.stdout.join('\n')}
          <div class="term-wrap">
            <pre class="terminal-output" use:pinBottom>{#each full.split('\n') as line, i}{#if i === 0 && (line.startsWith('$ ') || line === '$')}<span class="term-prompt">$</span>{line.slice(1)}{:else}{line}{/if}
{/each}{#if !tool.done}<span class="blink-caret"></span>{/if}</pre>
          </div>
        {:else if tool.name === 'web_search' && searchResults(tool)}
          {@const results = searchResults(tool)}
          <div class="search-results">
            {#each results ?? [] as r}
              <div class="search-row">
                {#if r.url}
                  <a href={r.url} target="_blank" rel="noopener noreferrer" class="search-title">{r.title}</a>
                  <span class="search-url mono">{r.url}</span>
                {:else}
                  <span class="search-title-plain">{r.title}</span>
                {/if}
              </div>
            {/each}
            {#if searchScraped(tool)}
              <div class="search-hint">
                <iconify-icon icon="lucide:info" width="12"></iconify-icon>
                {$t('tools.search_scraped')}
                <a href="https://brave.com/search/api/" target="_blank" rel="noopener noreferrer">{$t('tools.search_get_key')}</a>
              </div>
            {/if}
          </div>
        {:else if tool.name === 'write_file' && tool.ui_payload?.preview != null}
          <div class="term-wrap">
            <pre class="tool-output">{tool.ui_payload.preview}</pre>
            {#if tool.ui_payload.preview_truncated}
              <div class="fold-info">
                <iconify-icon icon="lucide:chevron-down" width="13"></iconify-icon>
                {$t('tools.n_more_lines').replace('{n}', String(tool.ui_payload.line_count - 30))}
              </div>
            {/if}
          </div>
          {#if tool.ui_payload?.undo_id}
            <button class="undo-btn" disabled={undone[tool.id]} onclick={() => undoOverwrite(tool.ui_payload.undo_id, tool.id)}>
              <iconify-icon icon="ant-design:undo-outlined" width="12"></iconify-icon>
              {undone[tool.id] ? $t('tools.undo_done') : $t('tools.undo_overwrite')}
            </button>
          {/if}
        {:else if tool.name === 'render_ui' && tool.ui_payload?.spec}
          {@const guarded = sanitizeSpec(tool.ui_payload.spec, READ_ONLY_NODE_TYPES).spec}
          {#if guarded}
            <div class="genui-card-wrap">
              <GenuiBlock spec={guarded} />
            </div>
          {:else if tool.result}
            <pre class="tool-output">{prettyResult(tool.result)}</pre>
          {/if}
        {:else if tool.result}
          <pre class="tool-output">{prettyResult(tool.result)}</pre>
        {/if}
        </div></div>
      </details>
    {/each}
  </div>
{/if}

<style>
/* Cards, not rows: each tool is its own bordered card in a column, headed by
   a slim label line (the design's tool-call treatment). */
.tool-group { display: flex; flex-direction: column; gap: 8px; }
.group-header {
  display: flex; align-items: center; gap: 8px;
  padding: 0 2px;
  font-size: 12px; color: var(--text-secondary);
}
.hdr-label { flex: 0 0 auto; }
.hdr-time { font-size: 12px; color: var(--text-tertiary); margin-left: 10px; }
.mono { font-family: var(--font-mono); }
.tool-item {
  border: 1px solid var(--border); border-radius: 10px;
  background: var(--bg-container); box-shadow: var(--card-shadow);
  overflow: hidden; transition: 0.12s;
}
.tool-summary {
  list-style: none; display: flex; align-items: center; gap: 10px;
  padding: 8px 12px 8px 10px; cursor: pointer; user-select: none;
}
.tool-summary:hover { background: var(--hover-neutral); }
.tool-well {
  width: 30px; height: 30px; flex: 0 0 30px; border-radius: var(--radius-sm);
  display: grid; place-items: center;
  background: var(--hover-neutral); color: var(--text-secondary);
}
.tool-well.accent { background: var(--active-blue-bg); color: var(--blue-6); }
.tool-head { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 1px; }
.tool-title-row { display: flex; align-items: baseline; gap: 8px; min-width: 0; }
.tool-title { font-size: 13px; font-weight: 600; color: var(--text); flex: 0 0 auto; }
.tool-arg { font-size: 11px; color: var(--text-secondary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; user-select: text; cursor: text; min-width: 0; }
.tool-submeta { font-size: 11px; color: var(--text-secondary); }
.tool-status { margin-left: auto; flex: 0 0 auto; display: flex; align-items: center; }
.st { display: inline-flex; align-items: center; gap: 4px; font-size: 11px; font-weight: 500; }
.st.ok { color: var(--success); }
.st.err { color: var(--error); }
.st.warn { color: var(--warning); }
.st.run { color: var(--blue-6); font-size: 12px; gap: 6px; }
/* Body regions are capped at ~10 text lines; longer content scrolls. */
.tool-output {
  margin: 0; padding: 10px 14px; border-top: 1px solid var(--border-table);
  background: var(--bg-sidebar); font-size: 12px; line-height: 1.7;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  color: var(--text-secondary); overflow-x: auto; white-space: pre-wrap; word-break: break-word;
  max-height: 224px; overflow-y: auto;
}
/* Chevron rotates from ▸ (collapsed) to ▾ (open). */
.chev { transition: transform 0.15s ease; flex: 0 0 auto; }
details[open] > summary .chev { transform: rotate(90deg); }
.tool-body { display: grid; grid-template-rows: 1fr; }
.tool-body-inner { overflow: hidden; min-height: 0; }
/* todo_write checklist */
.todo-list { border-top: 1px solid var(--border-table); padding: 10px 14px; display: flex; flex-direction: column; gap: 8px; max-height: 224px; overflow-y: auto; }
.todo-step { display: flex; align-items: center; gap: 8px; font-size: 13px; color: var(--text); }
.todo-done { color: var(--text-tertiary); text-decoration: line-through; }
.todo-pending { color: var(--text-tertiary); }
.term-wrap { display: flex; flex-direction: column; }
.undo-btn {
  width: 100%; padding: 7px 12px; border: none; border-top: 1px solid var(--border-table);
  background: var(--bg-table-header); display: flex; align-items: center; justify-content: center;
  gap: 6px; font-size: 12px; color: var(--text-secondary); cursor: pointer; font-family: inherit;
}
.undo-btn:hover:not(:disabled) { background: var(--active-blue-bg); color: var(--blue-6); }
.undo-btn:disabled { color: var(--text-quaternary); cursor: default; }
.fold-info {
  width: 100%; padding: 8px 12px; border-top: 1px solid var(--border-table);
  background: var(--bg-table-header); display: flex; align-items: center; justify-content: center;
  gap: 6px; font-size: 12px; color: var(--text-tertiary); font-family: inherit;
}
.term-prompt { color: var(--success); }
.genui-card-wrap {
  border-top: 1px solid var(--border-table); padding: 10px 14px;
}
.search-results {
  border-top: 1px solid var(--border-table); padding: 10px 14px;
  display: flex; flex-direction: column; gap: 10px;
  max-height: 224px; overflow-y: auto;
}
.search-row { display: flex; flex-direction: column; gap: 2px; }
.search-title { font-size: 13px; color: var(--blue-6); cursor: pointer; text-decoration: none; }
.search-title:hover { text-decoration: underline; }
.search-title-plain { font-size: 13px; color: var(--text); }
.search-url { font-size: 11px; color: var(--text-tertiary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.search-hint { display: flex; align-items: center; gap: 5px; font-size: 11px; color: var(--text-tertiary); }
.search-hint a { color: var(--blue-6); text-decoration: none; }
.search-hint a:hover { text-decoration: underline; }
.diff-block { border-top: 1px solid var(--border-table); font-size: 12px; line-height: 1.7; overflow: auto; max-height: 224px; }
.diff-hdr { padding: 4px 14px; color: var(--text-tertiary); border-bottom: 1px solid var(--border-table); }
.diff-line { padding: 1px 14px; }
.diff-line.rm { background: var(--error-bg); color: var(--error-dark); border-left: 2px solid var(--error); }
.diff-line.add { background: var(--success-bg); color: var(--success-text); border-left: 2px solid var(--success); }
.terminal-output {
  margin: 0; padding: 12px 14px; border-top: 1px solid var(--border-table);
  background: var(--terminal-bg); color: var(--terminal-text); font-size: 12px; line-height: 1.6;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace; overflow-x: auto;
  max-height: 216px; overflow-y: auto;
}
.blink-caret {
  display: inline-block; width: 7px; height: 13px;
  background: var(--terminal-text); vertical-align: -2px;
  animation: octo-blink 1s step-end infinite;
}
.error-output {
  border-top: 1px solid var(--border-table); background: var(--error-bg);
  border-left: 2px solid var(--error); padding: 10px 14px;
  font-size: 12px; line-height: 1.6; color: var(--error-dark); overflow: auto;
  max-height: 216px;
}
/* web_fetch that hit an HTTP error: the tool ran, the page didn't. */
.warning-output {
  margin: 0; border-top: 1px solid var(--border-table); background: var(--warning-bg);
  border-left: 2px solid var(--warning); padding: 10px 14px;
  font-size: 12px; line-height: 1.6; color: var(--warning-text);
  overflow-x: auto; white-space: pre-wrap; word-break: break-word;
  max-height: 216px; overflow-y: auto;
}
.promote-btn {
  height: 20px; padding: 0 8px;
  border: 1px solid var(--blue-6); background: transparent;
  border-radius: 3px; font-size: 11px; color: var(--blue-6);
  cursor: pointer; font-family: inherit; line-height: 1;
}
.promote-btn:hover { background: var(--blue-1); }

.trail-wrap { display: flex; flex-direction: column; gap: 6px; padding: 2px 0; }
.wf-log {
  font-size: 12px; color: var(--text-secondary); word-break: break-word;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}
.wf-agent { border: 1px solid var(--border-table); border-radius: 8px; background: var(--bg-container); }
.wf-agent-summary {
  list-style: none; display: flex; align-items: center; gap: 7px;
  padding: 6px 10px; cursor: pointer; user-select: none; font-size: 12px; min-width: 0;
}
.wf-agent-summary::-webkit-details-marker { display: none; }
.wf-agent-summary:hover { background: var(--hover-neutral); border-radius: 8px; }
.wf-agent-id { color: var(--blue-6); font-weight: 600; font-size: 11px; flex: 0 0 auto; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.wf-agent-label {
  color: var(--text-heading); font-weight: 500; flex: 1; min-width: 0;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.wf-agent-body { border-top: 1px solid var(--border-table); padding: 8px 10px; }
</style>
