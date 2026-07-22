<script lang="ts">
  import { t, tr } from '../../lib/i18n'
  import { activeSessionId, showToast } from '../../lib/stores'
  import { ws } from '../../lib/ws'
  import * as api from '../../lib/api'
  import { toolOpenState, applyToolToggle } from '../../lib/toolFold'

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
        return r ? `${r.length} results` : ''
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

  function isTerminalTool(tool: any): boolean {
    return tool.name === 'terminal' || tool.name === 'bash'
  }

  // Per-tool open/closed override, seeded by the default (see lib/toolFold).
  // Binding <details open> straight to the default would let every streaming
  // re-render revert a manual collapse of the auto-opened last tool.
  let toolOpen = $state<Record<string, boolean>>({})

  // The last tool card auto-collapses the instant its turn stops running,
  // which — bound straight to <details open> — hides the content in one
  // frame and reads as a flash/bug. `closingIds` keeps that one card's
  // <details> forced open for a beat while a CSS transition (see
  // .tool-body.auto-closing) shrinks it first, so the native collapse that
  // finally lands is invisible.
  //
  // This only fires on the group-level running->not-running edge (the turn
  // actually finishing), not on every default flip: the last tool also loses
  // its auto-open default the instant a *later* tool call starts and takes
  // over as the new last one — collapsing the outgoing card there stays
  // instant, matching how it always worked, because animating it made the
  // outgoing card's shrink and the incoming card's arrival read as if both
  // were "expanding" together.
  //
  // A user-driven click still closes instantly (native <details> behaviour,
  // untouched here) — checking toolOpen[id] here skips a card the user
  // already has an explicit override on.
  //
  // This must be $effect.pre, not $effect: a post-DOM effect would let the
  // running->false render close the <details> first and force it back open a
  // flush later. That reopen fires a toggle whose open=true diverges from the
  // now-false default, so applyToolToggle records it as a user "keep open"
  // override — and when the animation ends and closingIds clears, that stale
  // override snaps the card back open (animated shrink, then pop back). Running
  // before the DOM update keeps `open` true continuously, so no toggle fires
  // at all.
  // Slightly longer than the CSS transition so the shrink always finishes
  // before the <details> is allowed to natively close.
  let closingIds = $state<Record<string, boolean>>({})
  let prevRunning: boolean | undefined
  const AUTO_CLOSE_MS = 750
  $effect.pre(() => {
    const ts = tools ?? []
    if (ts.length === 0) return
    const running = ts.some((t) => !t.done && !t.error)
    const last = ts[ts.length - 1]
    if (prevRunning === true && running === false && last && !last.error && toolOpen[last.id] === undefined) {
      const id = last.id
      closingIds = { ...closingIds, [id]: true }
      setTimeout(() => {
        const next = { ...closingIds }
        delete next[id]
        closingIds = next
      }, AUTO_CLOSE_MS)
    }
    prevRunning = running
  })

  // Toggle handler that is animation-aware: while a card is mid-animated-close,
  // the only genuine toggle is the user clicking to collapse it early — end the
  // animation so the native close sticks, and record no override either way
  // (the forced-open state must never be mistaken for a user choice).
  function onToggle(tool: any, lastId: string | undefined, running: boolean, open: boolean) {
    if (closingIds[tool.id]) {
      if (!open) {
        const next = { ...closingIds }
        delete next[tool.id]
        closingIds = next
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

  // Long output folding: show the first FOLD_LINES, reveal the rest on click.
  // For terminal/bash (tail=true) show the LAST FOLD_LINES instead — errors
  // and summaries land at the bottom of shell output, not the top, so a
  // head-fold buries exactly the lines the user needs to see (#1106).
  // `foldable`/`expanded` ride along so the caller can always offer a way back
  // to collapsed, even once `hidden` drops to 0 because the user expanded it
  // (#1114 — expand used to be a one-way trip).
  const FOLD_LINES = 14
  const DIFF_FOLD_LINES = 12
  let expanded = $state<Record<string, boolean>>({})
  function foldInfo(id: string, text: string, tail = false, foldLines = FOLD_LINES) {
    const lines = text.split('\n')
    const foldable = lines.length > foldLines
    const isExpanded = !!expanded[id]
    if (!foldable || isExpanded) {
      return { shown: text, hidden: 0, foldable, expanded: isExpanded }
    }
    if (tail) {
      return { shown: lines.slice(-foldLines).join('\n'), hidden: lines.length - foldLines, foldable, expanded: false }
    }
    return { shown: lines.slice(0, foldLines).join('\n'), hidden: lines.length - foldLines, foldable, expanded: false }
  }

  function toggleFold(id: string) {
    expanded[id] = !expanded[id]
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
      {@const isClosing = !!closingIds[tool.id]}
      <details open={toolOpenState(toolOpen, tool, lastId, anyRunning) || isClosing} ontoggle={(e) => onToggle(tool, lastId, anyRunning, (e.currentTarget as HTMLDetailsElement).open)} class="tool-item">
        <summary class="tool-summary">
          <iconify-icon icon="lucide:chevron-right" width="13" class="chev" style="color:var(--text-tertiary)"></iconify-icon>
          <iconify-icon icon={toolIcon(tool.name)} width="14" style="color:var(--text-tertiary);flex:0 0 auto"></iconify-icon>
          <span class="tool-name mono">{tool.name}</span>
          {#if argText}
            <span class="tool-arg mono" title={argText}>{argText}</span>
          {/if}
          {#if meta && !fErr && !tErr}<span class="tool-meta" style="margin-left:auto">{meta}</span>{/if}
          <span style="{(meta && !fErr && !tErr) ? '' : 'margin-left:auto;'}flex:0 0 auto;display:flex;align-items:center">
            {#if tool.error}
              <span style="display:flex;align-items:center;gap:4px;font-size:12px;color:var(--error)">
                <iconify-icon icon="ant-design:close-circle-outlined" width="14"></iconify-icon>
                {$t('tools.failed')}
              </span>
            {:else if fErr}
              <span style="display:flex;align-items:center;gap:4px;font-size:12px;color:var(--warning)">
                <iconify-icon icon="ant-design:warning-outlined" width="14"></iconify-icon>
                {fErr}
              </span>
            {:else if tErr}
              <span style="display:flex;align-items:center;gap:4px;font-size:12px;color:var(--warning)">
                <iconify-icon icon="ant-design:warning-outlined" width="14"></iconify-icon>
                {tErr}
              </span>
            {:else if tool.done}
              <iconify-icon icon="ant-design:check-circle-outlined" width="14" style="color:var(--success)"></iconify-icon>
            {:else}
              <span style="display:flex;align-items:center;gap:6px;font-size:12px;color:var(--blue-6)">
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

        <div class="tool-body" class:auto-closing={isClosing}><div class="tool-body-inner">
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
        {:else if tool.ui_payload?.diff}
          {@const fold = foldInfo(tool.id, tool.ui_payload.diff, false, DIFF_FOLD_LINES)}
          <div class="diff-block">
            {#each fold.shown.split('\n') as line}
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
          {#if fold.hidden > 0}
            <button class="fold-btn" onclick={() => toggleFold(tool.id)}>
              <iconify-icon icon="lucide:chevron-down" width="13"></iconify-icon>
              {$t('tools.show_more').replace('{n}', String(fold.hidden))}
            </button>
          {:else if fold.foldable && fold.expanded}
            <button class="fold-btn" onclick={() => toggleFold(tool.id)}>
              <iconify-icon icon="lucide:chevron-up" width="13"></iconify-icon>
              {$t('tools.show_less')}
            </button>
          {/if}
          {#if tool.ui_payload?.undo_id}
            <button class="undo-btn" disabled={undone[tool.id]} onclick={() => undoOverwrite(tool.ui_payload.undo_id, tool.id)}>
              <iconify-icon icon="ant-design:undo-outlined" width="12"></iconify-icon>
              {undone[tool.id] ? $t('tools.undo_done') : $t('tools.undo_overwrite')}
            </button>
          {/if}
        {:else if tool.stdout && tool.stdout.length > 0}
          {@const full = tool.stdout.join('\n')}
          {@const isTerm = isTerminalTool(tool)}
          {@const fold = foldInfo(tool.id, full, isTerm)}
          <div class="term-wrap">
            {#if isTerm && (fold.hidden > 0 || fold.expanded)}
              <button class="fold-btn" onclick={() => toggleFold(tool.id)}>
                <iconify-icon icon={fold.hidden > 0 ? 'lucide:chevron-up' : 'lucide:chevron-down'} width="13"></iconify-icon>
                {fold.hidden > 0 ? $t('tools.show_earlier').replace('{n}', String(fold.hidden)) : $t('tools.show_less')}
              </button>
            {/if}
            <pre class="terminal-output">{#each fold.shown.split('\n') as line, i}{#if i === 0 && fold.hidden === 0 && (line.startsWith('$ ') || line === '$')}<span class="term-prompt">$</span>{line.slice(1)}{:else}{line}{/if}
{/each}{#if !tool.done}<span class="blink-caret"></span>{/if}</pre>
            {#if !isTerm && (fold.hidden > 0 || fold.expanded)}
              <button class="fold-btn" onclick={() => toggleFold(tool.id)}>
                <iconify-icon icon={fold.hidden > 0 ? 'lucide:chevron-down' : 'lucide:chevron-up'} width="13"></iconify-icon>
                {fold.hidden > 0 ? $t('tools.show_more').replace('{n}', String(fold.hidden)) : $t('tools.show_less')}
              </button>
            {/if}
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
        {:else if tool.result}
          {@const pretty = prettyResult(tool.result)}
          {@const isTerm = isTerminalTool(tool)}
          {@const fold = foldInfo(tool.id, pretty, isTerm)}
          <div>
            {#if isTerm && (fold.hidden > 0 || fold.expanded)}
              <button class="fold-btn light" onclick={() => toggleFold(tool.id)}>
                <iconify-icon icon={fold.hidden > 0 ? 'lucide:chevron-up' : 'lucide:chevron-down'} width="13"></iconify-icon>
                {fold.hidden > 0 ? $t('tools.show_earlier').replace('{n}', String(fold.hidden)) : $t('tools.show_less')}
              </button>
            {/if}
            <pre class="tool-output">{fold.shown}</pre>
            {#if !isTerm && (fold.hidden > 0 || fold.expanded)}
              <button class="fold-btn light" onclick={() => toggleFold(tool.id)}>
                <iconify-icon icon={fold.hidden > 0 ? 'lucide:chevron-down' : 'lucide:chevron-up'} width="13"></iconify-icon>
                {fold.hidden > 0 ? $t('tools.show_more').replace('{n}', String(fold.hidden)) : $t('tools.show_less')}
              </button>
            {/if}
          </div>
        {/if}
        </div></div>
      </details>
    {/each}
  </div>
{/if}

<style>
.tool-group { border: 1px solid var(--border-table); border-radius: 10px; background: var(--bg-container); overflow: hidden; }
.group-header {
  display: flex; align-items: center; gap: 8px;
  padding: 9px 12px; background: var(--bg-table-header); border-bottom: 1px solid var(--border-table);
  font-size: 13px; color: var(--text-secondary);
}
.hdr-label { flex: 0 0 auto; }
.hdr-time { font-size: 12px; color: var(--text-tertiary); margin-left: 10px; }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.tool-item { border-bottom: 1px solid var(--border-table); }
.tool-item:last-child { border-bottom: none; }
.tool-summary {
  list-style: none; display: flex; align-items: center; gap: 8px;
  padding: 9px 12px; cursor: pointer; user-select: none;
}
.tool-summary:hover { background: var(--hover-neutral); }
.tool-name { font-size: 13px; color: var(--text); }
.tool-arg { font-size: 12px; color: var(--text-tertiary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; user-select: text; cursor: text; }
.tool-meta { font-size: 12px; color: var(--text-tertiary); }
.tool-output {
  margin: 0; padding: 10px 14px; border-top: 1px solid var(--border-table);
  background: var(--bg-sidebar); font-size: 12px; line-height: 1.7;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  color: var(--text-secondary); overflow-x: auto; white-space: pre-wrap; word-break: break-word;
  max-height: 420px; overflow-y: auto;
}
/* Chevron rotates from ▸ (collapsed) to ▾ (open). */
.chev { transition: transform 0.15s ease; flex: 0 0 auto; }
details[open] > summary .chev { transform: rotate(90deg); }
/* Auto-collapse animation: when the turn finishes, this wrapper shrinks to
   nothing before the <details> is actually allowed to close (AUTO_CLOSE_MS
   in the script keeps it forced open just past this duration), so the native
   collapse that follows is invisible instead of an instant flash. A user
   click still closes natively/instantly — untouched here. The base rule pins
   transition-duration to 0s explicitly (not just omits it) — some engines
   resolve a class-removal transition from the style being left rather than
   the one being entered, which would otherwise replay the shrink in reverse
   as a spurious "expand" animation once auto-closing is cleared. */
.tool-body { display: grid; grid-template-rows: 1fr; transition: grid-template-rows 0s; }
.tool-body.auto-closing { grid-template-rows: 0fr; transition: grid-template-rows 0.6s ease; }
.tool-body-inner { overflow: hidden; min-height: 0; }
/* todo_write checklist */
.todo-list { border-top: 1px solid var(--border-table); padding: 10px 14px; display: flex; flex-direction: column; gap: 8px; }
.todo-step { display: flex; align-items: center; gap: 8px; font-size: 13px; color: var(--text); }
.todo-done { color: var(--text-tertiary); text-decoration: line-through; }
.todo-pending { color: var(--text-tertiary); }
/* Long-output fold button */
.term-wrap { display: flex; flex-direction: column; }
.fold-btn {
  width: 100%; padding: 8px 12px; border: none; border-top: 1px solid var(--border-table);
  background: var(--bg-table-header); display: flex; align-items: center; justify-content: center;
  gap: 6px; font-size: 12px; color: var(--blue-6); cursor: pointer; font-family: inherit;
}
.fold-btn:hover { background: var(--active-blue-bg); }
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
.search-results {
  border-top: 1px solid var(--border-table); padding: 10px 14px;
  display: flex; flex-direction: column; gap: 10px;
}
.search-row { display: flex; flex-direction: column; gap: 2px; }
.search-title { font-size: 13px; color: var(--blue-6); cursor: pointer; text-decoration: none; }
.search-title:hover { text-decoration: underline; }
.search-title-plain { font-size: 13px; color: var(--text); }
.search-url { font-size: 11px; color: var(--text-tertiary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.diff-block { border-top: 1px solid var(--border-table); font-size: 12px; line-height: 1.7; overflow-x: auto; }
.diff-hdr { padding: 4px 14px; color: var(--text-tertiary); border-bottom: 1px solid var(--border-table); }
.diff-line { padding: 1px 14px; }
.diff-line.rm { background: var(--error-bg); color: var(--error-dark); border-left: 2px solid var(--error); }
.diff-line.add { background: var(--success-bg); color: var(--success-text); border-left: 2px solid var(--success); }
.terminal-output {
  margin: 0; padding: 12px 14px; border-top: 1px solid var(--border-table);
  background: var(--terminal-bg); color: var(--terminal-text); font-size: 12px; line-height: 1.6;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace; overflow-x: auto;
  max-height: 420px; overflow-y: auto;
}
.blink-caret {
  display: inline-block; width: 7px; height: 13px;
  background: var(--terminal-text); vertical-align: -2px;
  animation: octo-blink 1s step-end infinite;
}
.error-output {
  border-top: 1px solid var(--border-table); background: var(--error-bg);
  border-left: 2px solid var(--error); padding: 10px 14px;
  font-size: 12px; line-height: 1.6; color: var(--error-dark); overflow-x: auto;
}
/* web_fetch that hit an HTTP error: the tool ran, the page didn't. */
.warning-output {
  margin: 0; border-top: 1px solid var(--border-table); background: var(--warning-bg);
  border-left: 2px solid var(--warning); padding: 10px 14px;
  font-size: 12px; line-height: 1.6; color: var(--warning-text);
  overflow-x: auto; white-space: pre-wrap; word-break: break-word;
  max-height: 280px; overflow-y: auto;
}
.promote-btn {
  height: 20px; padding: 0 8px;
  border: 1px solid var(--blue-6); background: transparent;
  border-radius: 3px; font-size: 11px; color: var(--blue-6);
  cursor: pointer; font-family: inherit; line-height: 1;
}
.promote-btn:hover { background: var(--blue-1); }
</style>
