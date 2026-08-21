<script lang="ts">
  import type { AgentTrailStep } from '../../lib/stores'
  import { defaultToolOpen, toolOpenState, applyToolToggle } from '../../lib/toolFold'
  import { renderMarkdown } from '../../lib/markdown'
  import { t } from '../../lib/i18n'

  // One agent's trail: tool steps (input + capped output) interleaved with its
  // assistant text blocks, plus the final result. Shared by the live
  // SubAgentsCard, the WorkflowsCard's nested agent rows, and (via the
  // transcript tool cards) the after-the-fact review surface.
  let { steps = [], running = false, result = '', resultError = '' }: {
    steps?: AgentTrailStep[]
    running?: boolean
    result?: string
    resultError?: string
  } = $props()

  // Per-step fold overrides on top of the toolFold defaults (errors open, the
  // latest step of a running trail open, everything else closed). Steps only
  // ever append, so the index is a stable identity.
  let overrides = $state<Record<string, boolean>>({})

  let lastToolIdx = $derived.by(() => {
    for (let i = steps.length - 1; i >= 0; i--) {
      if (steps[i].kind === 'tool') return i
    }
    return -1
  })

  function foldable(st: AgentTrailStep): boolean {
    return st.kind === 'tool' && (!!st.output || !!st.input && Object.keys(st.input).length > 0)
  }

  function toolLike(st: AgentTrailStep & { kind: 'tool' }, i: number) {
    return { id: String(i), error: st.error ? 'err' : undefined }
  }

  function inputPreview(input?: Record<string, any>): string {
    if (!input) return ''
    const entries = Object.entries(input)
    if (entries.length === 0) return ''
    return entries.map(([k, v]) => `${k}: ${JSON.stringify(v)}`).join(', ')
  }

  function prettyInput(input?: Record<string, any>): string {
    if (!input || Object.keys(input).length === 0) return ''
    return JSON.stringify(input, null, 2)
  }
</script>

<div class="trail">
  {#if steps.length === 0}
    <span class="empty mono">{$t('agent.no_tools_yet')}</span>
  {/if}
  {#each steps as st, i}
    {#if st.kind === 'text'}
      <div class="trail-text">{@html renderMarkdown(st.text)}</div>
    {:else if foldable(st)}
      <details
        class="trail-tool"
        class:err={st.error}
        open={toolOpenState(overrides, toolLike(st, i), String(lastToolIdx), running)}
        ontoggle={(e) => applyToolToggle(overrides, toolLike(st, i), String(lastToolIdx), running, (e.currentTarget as HTMLDetailsElement).open)}
      >
        <summary class="step mono">
          {#if st.error}
            <iconify-icon icon="ant-design:close-circle-outlined" width="12" style="color:var(--error)"></iconify-icon>
          {:else if !st.done && running}
            <iconify-icon icon="ant-design:loading-outlined" width="12" style="color:var(--blue-6);animation:octo-spin 0.8s linear infinite"></iconify-icon>
          {:else}
            <iconify-icon icon="ant-design:check-circle-outlined" width="12" style="color:var(--success)"></iconify-icon>
          {/if}
          <span class="tool-name">{st.name}</span>
          <span class="tool-preview">{inputPreview(st.input)}</span>
        </summary>
        <div class="tool-detail">
          {#if prettyInput(st.input)}
            <pre class="io input mono">{prettyInput(st.input)}</pre>
          {/if}
          {#if st.output}
            <pre class="io output mono" class:err={st.error}>{st.output}</pre>
          {/if}
        </div>
      </details>
    {:else}
      <div class="step mono bare">
        {#if st.error}
          <iconify-icon icon="ant-design:close-circle-outlined" width="12" style="color:var(--error)"></iconify-icon>
        {:else if !st.done && running}
          <iconify-icon icon="ant-design:loading-outlined" width="12" style="color:var(--blue-6);animation:octo-spin 0.8s linear infinite"></iconify-icon>
        {:else}
          <iconify-icon icon="ant-design:check-circle-outlined" width="12" style="color:var(--success)"></iconify-icon>
        {/if}
        <span class="tool-name">{st.name}</span>
      </div>
    {/if}
  {/each}

  {#if resultError}
    <div class="result err">
      <div class="result-label mono">{$t('agent.trail_error')}</div>
      <pre class="io output err mono">{resultError}</pre>
    </div>
  {:else if result}
    <details class="result" open={!running}>
      <summary class="result-label mono">{$t('agent.trail_result')}</summary>
      <div class="result-body">{@html renderMarkdown(result)}</div>
    </details>
  {/if}
</div>

<style>
.trail { display: flex; flex-direction: column; gap: 6px; min-width: 0; }
.empty { font-size: 12px; color: var(--text-tertiary); }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }

.step { display: flex; align-items: center; gap: 8px; font-size: 12px; color: var(--text-secondary); min-width: 0; }
.step.bare { cursor: default; }
.trail-tool > summary { cursor: pointer; user-select: none; list-style: none; }
.trail-tool > summary::-webkit-details-marker { display: none; }
.tool-name { font-weight: 500; flex: 0 0 auto; }
.tool-preview {
  font-size: 11px; color: var(--text-tertiary); opacity: 0.8;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0;
}
.trail-tool.err .tool-name { color: var(--error-dark); }

.tool-detail { display: flex; flex-direction: column; gap: 4px; padding: 4px 0 2px 20px; }
.io {
  margin: 0; padding: 6px 8px; font-size: 11px; line-height: 1.5;
  border-radius: 6px; background: var(--bg-container);
  border: 1px solid var(--border-table);
  max-height: 240px; overflow: auto; white-space: pre-wrap; word-break: break-word;
}
.io.input { color: var(--text-tertiary); max-height: 120px; }
.io.output { color: var(--text-secondary); }
.io.err { color: var(--error-dark); border-color: var(--error-bg); }

.trail-text { font-size: 12.5px; color: var(--text-secondary); line-height: 1.55; overflow-wrap: break-word; }
.trail-text :global(p) { margin: 0 0 4px; }
.trail-text :global(p:last-child) { margin-bottom: 0; }

.result { border-top: 1px dashed var(--border-table); padding-top: 6px; }
.result-label { font-size: 11px; font-weight: 600; color: var(--text-tertiary); cursor: pointer; user-select: none; }
.result > summary { list-style: none; }
.result > summary::-webkit-details-marker { display: none; }
.result-body { font-size: 12.5px; color: var(--text-primary); line-height: 1.55; padding-top: 4px; overflow-wrap: break-word; }
.result-body :global(p) { margin: 0 0 4px; }
.result.err .result-label { color: var(--error-dark); cursor: default; }
</style>
