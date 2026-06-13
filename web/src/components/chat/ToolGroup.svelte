<script lang="ts">
  // A collapsible group of tool calls for one agent turn.
  // Accepts optional `tools` + `streaming` props for real data;
  // falls back to static prototype content when called without props.

  let { tools = null, streaming: groupStreaming = false }: {
    tools?: any[] | null,
    streaming?: boolean,
  } = $props()

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

  // When tools prop provided, use it for counts
  $effect(() => {})
</script>

{#if tools !== null && tools.length > 0}
  <!-- Real data rendering -->
  {@const errorCount = tools.filter((t: any) => t.error).length}
  <div class="tool-group">
    <div class="group-header">
      <iconify-icon icon="ant-design:tool-outlined" width="14" style="color:rgba(0,0,0,0.45)"></iconify-icon>
      <span class="hdr-label">{tools.length} {tools.length === 1 ? 'tool' : 'tools'} used</span>
      {#if errorCount > 0}
        <span class="err-badge">
          <iconify-icon icon="ant-design:close-circle-outlined" width="12"></iconify-icon>
          {errorCount} failed
        </span>
      {/if}
      {#if groupStreaming}
        <span style="margin-left:auto;display:flex;align-items:center;gap:5px;font-size:12px;color:#1677FF">
          <iconify-icon icon="ant-design:loading-outlined" width="13" style="animation:octo-spin 0.8s linear infinite"></iconify-icon>
          running
        </span>
      {/if}
    </div>

    {#each tools as tool (tool.id)}
      <details open class="tool-item">
        <summary class="tool-summary">
          <iconify-icon icon="lucide:chevron-down" width="13" style="color:rgba(0,0,0,0.35)"></iconify-icon>
          <iconify-icon icon={toolIcon(tool.name)} width="14" style="color:rgba(0,0,0,0.45)"></iconify-icon>
          <span class="tool-name mono">{tool.name}</span>
          {#if tool.summary}
            <span class="tool-arg mono">{tool.summary}</span>
          {:else if tool.args}
            <span class="tool-arg mono">{typeof tool.args === 'string' ? tool.args : JSON.stringify(tool.args)}</span>
          {/if}
          <span style="margin-left:auto;flex:0 0 auto">
            {#if tool.error}
              <span style="display:flex;align-items:center;gap:4px;font-size:12px;color:#FF4D4F">
                <iconify-icon icon="ant-design:close-circle-outlined" width="14"></iconify-icon>
                failed
              </span>
            {:else if tool.done}
              <iconify-icon icon="ant-design:check-circle-outlined" width="14" style="color:#52C41A"></iconify-icon>
            {:else}
              <span style="display:flex;align-items:center;gap:4px;font-size:12px;color:#1677FF">
                <iconify-icon icon="ant-design:loading-outlined" width="13" style="animation:octo-spin 0.8s linear infinite"></iconify-icon>
                running
              </span>
            {/if}
          </span>
        </summary>

        {#if tool.error}
          <div class="error-output mono">{tool.error}</div>
        {:else if tool.diff}
          <div class="diff-block">
            {#each tool.diff.split('\n') as line}
              {#if line.startsWith('@@')}
                <div class="diff-hdr mono">{line}</div>
              {:else if line.startsWith('-')}
                <div class="diff-line rm mono">{line}</div>
              {:else if line.startsWith('+')}
                <div class="diff-line add mono">{line}</div>
              {:else}
                <div class="diff-line mono" style="padding:1px 14px;color:rgba(0,0,0,0.65)">{line}</div>
              {/if}
            {/each}
          </div>
        {:else if tool.stdout && tool.stdout.length > 0}
          <pre class="terminal-output">{tool.stdout.join('\n')}{#if !tool.done}<span class="blink-caret"></span>{/if}</pre>
        {:else if tool.result}
          <pre class="tool-output">{tool.result}</pre>
        {/if}
      </details>
    {/each}
  </div>
{/if}

<style>
.tool-group { border: 1px solid #F0F0F0; border-radius: 10px; background: #fff; overflow: hidden; }
.group-header {
  display: flex; align-items: center; gap: 8px;
  padding: 9px 12px; background: #FAFAFA; border-bottom: 1px solid #F0F0F0;
  font-size: 13px; color: rgba(0,0,0,0.65);
}
.hdr-label { flex: 0 0 auto; }
.err-badge { margin-left: auto; display: flex; align-items: center; gap: 4px; font-size: 12px; color: #FF4D4F; }
.hdr-time { font-size: 12px; color: rgba(0,0,0,0.45); margin-left: 10px; }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.tool-item { border-bottom: 1px solid #F0F0F0; }
.tool-item:last-child { border-bottom: none; }
.tool-summary {
  list-style: none; display: flex; align-items: center; gap: 8px;
  padding: 9px 12px; cursor: pointer; user-select: none;
}
.tool-summary:hover { background: rgba(0,0,0,0.02); }
.tool-name { font-size: 13px; color: rgba(0,0,0,0.88); }
.tool-arg { font-size: 12px; color: rgba(0,0,0,0.45); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tool-meta { font-size: 12px; color: rgba(0,0,0,0.45); }
.tool-output {
  margin: 0; padding: 10px 14px; border-top: 1px solid #F0F0F0;
  background: #FBFBFB; font-size: 12px; line-height: 1.7;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  color: rgba(0,0,0,0.65); overflow-x: auto;
}
.diff-block { border-top: 1px solid #F0F0F0; font-size: 12px; line-height: 1.7; overflow-x: auto; }
.diff-hdr { padding: 4px 14px; color: rgba(0,0,0,0.45); border-bottom: 1px solid #F0F0F0; }
.diff-line { padding: 1px 14px; }
.diff-line.rm { background: #FFF1F0; color: #CF1322; border-left: 2px solid #FF4D4F; }
.diff-line.add { background: #F6FFED; color: #389E0D; border-left: 2px solid #52C41A; }
.terminal-output {
  margin: 0; padding: 12px 14px; border-top: 1px solid #F0F0F0;
  background: #1F1F1F; color: #E6E6E6; font-size: 12px; line-height: 1.6;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace; overflow-x: auto;
}
.blink-caret {
  display: inline-block; width: 7px; height: 13px;
  background: #E6E6E6; vertical-align: -2px;
  animation: octo-blink 1s step-end infinite;
}
.error-output {
  border-top: 1px solid #F0F0F0; background: #FFF1F0;
  border-left: 2px solid #FF4D4F; padding: 10px 14px;
  font-size: 12px; line-height: 1.6; color: #CF1322; overflow-x: auto;
}
</style>
