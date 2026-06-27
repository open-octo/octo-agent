<script lang="ts">
  import { t } from '../../lib/i18n'
  import { activeSessionId } from '../../lib/stores'
  import { ws } from '../../lib/ws'
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

  // While a turn is still running, keep the LATEST tool open (whether or not it
  // has finished) so its output stays readable until the next step replaces it
  // — a finished tool only collapses once the next tool appears, and the whole
  // group collapses once the turn completes (the assistant reply takes over).
  // Errors always open — they need attention.
  function defaultOpen(tool: any, lastId: string | undefined, streaming: boolean): boolean {
    if (tool.error) return true
    if (!streaming) return false
    return tool.id === lastId
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
  const FOLD_LINES = 14
  let expanded = $state<Record<string, boolean>>({})
  function foldInfo(id: string, text: string) {
    const lines = text.split('\n')
    if (lines.length <= FOLD_LINES || expanded[id]) {
      return { shown: text, hidden: 0 }
    }
    return { shown: lines.slice(0, FOLD_LINES).join('\n'), hidden: lines.length - FOLD_LINES }
  }


</script>

{#if tools !== null && tools.length > 0}
  <!-- Real data rendering -->
  {@const lastId = tools[tools.length - 1]?.id}
  <div class="tool-group">
    <div class="group-header">
      <iconify-icon icon="ant-design:tool-outlined" width="14" style="color:var(--text-tertiary)"></iconify-icon>
      <span class="hdr-label">{tools.length} {tools.length === 1 ? 'tool' : 'tools'} used</span>
      {#if groupStreaming}
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
      <details open={defaultOpen(tool, lastId, groupStreaming)} class="tool-item">
        <summary class="tool-summary">
          <iconify-icon icon="lucide:chevron-right" width="13" class="chev" style="color:var(--text-tertiary)"></iconify-icon>
          <iconify-icon icon={toolIcon(tool.name)} width="14" style="color:var(--text-tertiary);flex:0 0 auto"></iconify-icon>
          <span class="tool-name mono">{tool.name}</span>
          {#if tool.summary}
            <span class="tool-arg mono">{tool.summary}</span>
          {:else if tool.args}
            <span class="tool-arg mono">{argSummary(tool.name, tool.args)}</span>
          {/if}
          {#if meta && !fErr}<span class="tool-meta" style="margin-left:auto">{meta}</span>{/if}
          <span style="{(meta && !fErr) ? '' : 'margin-left:auto;'}flex:0 0 auto;display:flex;align-items:center">
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
            {:else if tool.done}
              <iconify-icon icon="ant-design:check-circle-outlined" width="14" style="color:var(--success)"></iconify-icon>
            {:else}
              <span style="display:flex;align-items:center;gap:6px;font-size:12px;color:var(--blue-6)">
                <iconify-icon icon="ant-design:loading-outlined" width="13" style="animation:octo-spin 0.8s linear infinite"></iconify-icon>
                {$t('tools.running')}
                {#if tool.name === 'terminal' || tool.name === 'bash'}
                  <button class="promote-btn" onclick={(e) => { e.preventDefault(); e.stopPropagation(); promoteTerminal() }}>
                    Background
                  </button>
                {:else if tool.name === 'sub_agent'}
                  <button class="promote-btn" onclick={(e) => { e.preventDefault(); e.stopPropagation(); promoteSubAgent() }}>
                    Background
                  </button>
                {/if}
              </span>
            {/if}
          </span>
        </summary>

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
        {:else if tool.stdout && tool.stdout.length > 0}
          {@const full = tool.stdout.join('\n')}
          {@const fold = foldInfo(tool.id, full)}
          <div class="term-wrap">
            <pre class="terminal-output">{#each fold.shown.split('\n') as line}{#if line.startsWith('$ ') || line === '$'}<span class="term-prompt">$</span>{line.slice(1)}{:else}{line}{/if}
{/each}{#if !tool.done}<span class="blink-caret"></span>{/if}</pre>
            {#if fold.hidden > 0}
              <button class="fold-btn" onclick={() => expanded[tool.id] = true}>
                <iconify-icon icon="lucide:chevron-down" width="13"></iconify-icon>
                Show {fold.hidden} more lines
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
                {tool.ui_payload.line_count - 30} more lines
              </div>
            {/if}
          </div>
        {:else if tool.result}
          {@const pretty = prettyResult(tool.result)}
          {@const fold = foldInfo(tool.id, pretty)}
          <div>
            <pre class="tool-output">{fold.shown}</pre>
            {#if fold.hidden > 0}
              <button class="fold-btn light" onclick={() => expanded[tool.id] = true}>
                <iconify-icon icon="lucide:chevron-down" width="13"></iconify-icon>
                Show {fold.hidden} more lines
              </button>
            {/if}
          </div>
        {/if}
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
.tool-arg { font-size: 12px; color: var(--text-tertiary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
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
