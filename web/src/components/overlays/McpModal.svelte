<script lang="ts">
  import { mcpModalOpen, mcpModalState, mcpServers, showToast } from '../../lib/stores'
  import { t } from '../../lib/i18n'
  import * as api from '../../lib/api'

  let mode = $derived($mcpModalState.mode)
  let name = $state('')
  let command = $state('')
  let url = $state('')
  let transport = $state('stdio')
  let jsonText = $state('')
  let submitting = $state(false)
  let initFor = ''

  // Re-seed the form whenever the modal opens (add → blank, edit → prefilled).
  $effect(() => {
    if (!$mcpModalOpen) { initFor = ''; return }
    const key = $mcpModalState.mode + ':' + ($mcpModalState.server?.name ?? '')
    if (initFor === key) return
    initFor = key
    const srv = $mcpModalState.server
    if ($mcpModalState.mode === 'edit' && srv) {
      name = srv.name ?? ''
      command = srv.command ?? (Array.isArray(srv.args) ? `${srv.command ?? ''} ${srv.args.join(' ')}`.trim() : '')
      url = srv.url ?? ''
      transport = srv.transport || (srv.url ? 'http' : 'stdio')
    } else {
      name = ''; command = ''; url = ''; transport = 'stdio'
    }
    jsonText = ''
  })

  const title = $derived(mode === 'edit' ? $t('mcp.modal_edit') : mode === 'import' ? $t('mcp.modal_import') : $t('mcp.modal_add'))
  const isHttp = $derived(transport === 'http' || transport === 'sse')
  const canSubmit = $derived(
    mode === 'import'
      ? jsonText.trim().length > 0
      : !!name.trim() && (isHttp ? !!url.trim() : !!command.trim())
  )

  async function refresh() {
    const data = await api.listMcpServers()
    mcpServers.set(data.servers as any)
  }

  async function submit() {
    if (!canSubmit) return
    submitting = true
    try {
      if (mode === 'import') {
        let parsed: any
        try { parsed = JSON.parse(jsonText) } catch { showToast('Invalid JSON', 'error'); submitting = false; return }
        const servers = parsed.mcpServers ?? parsed
        await api.importMcpServers(servers)
        await refresh()
        showToast('Servers imported')
      } else if (mode === 'edit') {
        const server: Record<string, unknown> = isHttp ? { url: url.trim() } : { command: command.trim() }
        if (transport) server.transport = transport
        await api.updateMcpServer(name.trim(), { server })
        await refresh()
        showToast('Server updated')
      } else {
        await api.createMcpServer(isHttp ? { name: name.trim(), url: url.trim(), transport } : { name: name.trim(), command: command.trim(), transport })
        await refresh()
        showToast('Server added · connecting…')
      }
      mcpModalOpen.set(false)
    } catch (e: any) {
      showToast(e.message ?? 'Failed', 'error')
    } finally {
      submitting = false
    }
  }

  function close() {
    mcpModalOpen.set(false)
  }
</script>

{#if $mcpModalOpen}
<div class="backdrop" onclick={close}>
  <div class="modal" onclick={(e) => e.stopPropagation()}>
    <div class="modal-header">
      <span class="modal-title">{title}</span>
      <button class="close-btn" onclick={close}>
        <iconify-icon icon="ant-design:close-outlined" width="14"></iconify-icon>
      </button>
    </div>
    <div class="modal-body">
      {#if mode === 'import'}
        <div class="field">
          <label>{$t('mcp.paste_json')}</label>
          <textarea
            rows={9}
            class="json-area"
            placeholder={'{\n  "mcpServers": {\n    "github": { "command": "npx", "args": ["-y", "@modelcontextprotocol/server-github"] }\n  }\n}'}
            bind:value={jsonText}
          ></textarea>
        </div>
      {:else}
        <div class="field">
          <label>{$t('mcp.field_name')}</label>
          <input placeholder={$t('mcp.field_name_ph')} bind:value={name} disabled={mode === 'edit'} />
        </div>
        <div class="field">
          <label>{$t('mcp.field_transport')}</label>
          <select bind:value={transport}>
            {#each ['stdio', 'http', 'sse'] as opt}<option>{opt}</option>{/each}
          </select>
        </div>
        {#if isHttp}
          <div class="field">
            <label>{$t('mcp.field_url')}</label>
            <input placeholder="https://example.com/mcp" bind:value={url} />
          </div>
        {:else}
          <div class="field">
            <label>{$t('mcp.field_command')}</label>
            <input placeholder="npx -y @modelcontextprotocol/server-…" bind:value={command} />
          </div>
        {/if}
      {/if}
    </div>
    <div class="modal-footer">
      <button class="btn-secondary" onclick={close} disabled={submitting}>{$t('common.cancel')}</button>
      <button class="btn-primary" onclick={submit} disabled={submitting || !canSubmit}>
        {submitting ? $t('common.saving') : mode === 'edit' ? $t('common.save') : mode === 'import' ? $t('mcp.import') : $t('mcp.add')}
      </button>
    </div>
  </div>
</div>
{/if}

<style>
.backdrop {
  position: fixed; inset: 0; z-index: 1000; background: var(--text-tertiary);
  display: flex; align-items: center; justify-content: center; padding: 24px;
}
.modal {
  width: 100%; max-width: 380px; background: var(--bg-container);
  border-radius: 12px; overflow: hidden;
  box-shadow: 0 16px 48px rgba(0,0,0,0.18);
  animation: octo-fadein 0.16s ease;
}
.modal-header {
  display: flex; align-items: center; gap: 8px;
  padding: 14px 18px; border-bottom: 1px solid var(--border-table);
}
.modal-title { font-size: 15px; font-weight: 600; color: var(--text-heading); flex: 1; }
.close-btn {
  width: 28px; height: 28px; border: none; background: transparent;
  border-radius: 6px; display: flex; align-items: center; justify-content: center;
  cursor: pointer; color: var(--text-tertiary);
}
.close-btn:hover { background: var(--hover-neutral); }
.modal-body { padding: 16px 18px; display: flex; flex-direction: column; gap: 12px; }
.field { display: flex; flex-direction: column; gap: 6px; }
label { font-size: 12px; color: var(--text-secondary); }
input, select {
  width: 100%; height: 32px; padding: 0 10px;
  border: 1px solid var(--border); border-radius: 6px; font-size: 13px;
  color: var(--text); font-family: inherit; outline: none; background: var(--bg-container);
  box-sizing: border-box;
}
input:focus, select:focus { border-color: var(--blue-6); box-shadow: 0 0 0 2px rgba(5,145,255,0.1); }
input:disabled { background: var(--bg-table-header); color: var(--text-tertiary); }
.json-area {
  width: 100%; padding: 8px 10px; border: 1px solid var(--border); border-radius: 6px;
  font-size: 12px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  color: var(--text); outline: none; background: var(--bg-container); box-sizing: border-box;
  resize: vertical; line-height: 1.6;
}
.json-area:focus { border-color: var(--blue-6); box-shadow: 0 0 0 2px rgba(5,145,255,0.1); }
.modal { max-width: 420px; }
.modal-footer {
  padding: 12px 18px; border-top: 1px solid var(--border-table);
  display: flex; justify-content: flex-end; gap: 8px;
}
.btn-secondary {
  height: 32px; padding: 0 14px; border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 6px; font-size: 14px; color: var(--text-secondary); cursor: pointer; font-family: inherit;
}
.btn-secondary:hover:not(:disabled) { border-color: var(--blue-5); color: var(--blue-5); }
.btn-secondary:disabled { opacity: 0.5; cursor: not-allowed; }
.btn-primary {
  height: 32px; padding: 0 14px; border: none; background: var(--blue-6);
  border-radius: 6px; font-size: 14px; color: #fff; cursor: pointer; font-family: inherit;
}
.btn-primary:hover:not(:disabled) { background: var(--blue-5); }
.btn-primary:disabled { opacity: 0.5; cursor: not-allowed; }
</style>
