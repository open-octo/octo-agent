<script lang="ts">
  import { mcpModalOpen, mcpServers } from '../../lib/stores'
  import { t, tr } from '../../lib/i18n'
  import { confirmDialog } from '../../lib/confirm'
  import * as api from '../../lib/api'

  let jsonText = $state('')
  let submitting = $state(false)
  let allowArbitrary = $state(false)
  let errorMsg = $state('')

  // Re-seed the form (blank) whenever the modal opens.
  $effect(() => {
    if ($mcpModalOpen) {
      jsonText = ''
      allowArbitrary = false
      errorMsg = ''
    }
  })

  const canSubmit = $derived(jsonText.trim().length > 0 && !submitting)

  async function refresh() {
    const data = await api.listMcpServers()
    mcpServers.set(data.servers as any)
  }

  async function submit() {
    if (!canSubmit) return
    submitting = true
    errorMsg = ''
    try {
      let parsed: any
      try { parsed = JSON.parse(jsonText) } catch { errorMsg = $t('mcp.invalid_json'); submitting = false; return }
      const servers = parsed.mcpServers ?? parsed
      // Import silently overwrites a same-named entry in ~/.octo/mcp.json —
      // fine for a genuine re-paste, but this is now the only structured UI
      // path for adding even a single server, so warn before clobbering one
      // that already exists (project-scoped names don't collide: they live
      // in a different file this never touches).
      const existingUserNames = new Set(
        ($mcpServers as any[]).filter(s => s.source === 'user').map(s => s.name)
      )
      const collisions = Object.keys(servers).filter(name => existingUserNames.has(name))
      if (collisions.length > 0 && !(await confirmDialog(tr('mcp.confirm_import_overwrite').replace('{names}', collisions.join(', '))))) {
        submitting = false
        return
      }
      await api.importMcpServers(allowArbitrary
        ? Object.fromEntries(Object.entries(servers).map(([name, entry]) => [name, { ...entry, allow_arbitrary_command: true }]))
        : servers)
      await refresh()
      mcpModalOpen.set(false)
    } catch (e: any) {
      errorMsg = translateImportError(e.message)
    } finally {
      submitting = false
    }
  }

  // Backend import errors are English substrings from parseStdioCommand.
  // Map the known patterns to i18n keys; fall back to the raw message so
  // unexpected errors don't lose their text.
  function translateImportError(message: string): string {
    if (!message) return tr('mcp.import_failed')
    if (message.includes('absolute-path command rejected')) return tr('mcp.err_absolute_path')
    if (message.includes('not a well-known launcher')) return tr('mcp.err_not_allowlisted')
    if (message.includes('forbidden characters')) return tr('mcp.err_forbidden_chars')
    if (message.includes('Invalid JSON') || message.includes('expected {mcpServers')) return String(message)
    return tr('mcp.import_failed') + ': ' + String(message)
  }

  function close() {
    mcpModalOpen.set(false)
  }
</script>

{#if $mcpModalOpen}
<div class="backdrop" onclick={close}>
  <div class="modal" onclick={(e) => e.stopPropagation()}>
    <div class="modal-header">
      <span class="modal-title">{$t('mcp.modal_import')}</span>
      <button class="close-btn" onclick={close}>
        <iconify-icon icon="ant-design:close-outlined" width="14"></iconify-icon>
      </button>
    </div>
    <div class="modal-body">
      <div class="field">
        <label>{$t('mcp.paste_json')}</label>
        <textarea
          rows={9}
          class="json-area"
          placeholder={'{\n  "mcpServers": {\n    "github": { "command": "npx", "args": ["-y", "@modelcontextprotocol/server-github"] }\n  }\n}'}
          bind:value={jsonText}
        ></textarea>
      </div>
      <label class="checkbox-row">
        <input type="checkbox" bind:checked={allowArbitrary}>
        <span>{$t('mcp.allow_arbitrary')}</span>
      </label>
      {#if errorMsg}
        <div class="import-error">{errorMsg}</div>
      {/if}
    </div>
    <div class="modal-footer">
      <button class="btn-secondary" onclick={close} disabled={submitting}>{$t('common.cancel')}</button>
      <button class="btn-primary" onclick={submit} disabled={submitting || !canSubmit}>
        {submitting ? $t('common.saving') : $t('mcp.import')}
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
  width: 100%; max-width: 420px; background: var(--bg-container);
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
.json-area {
  width: 100%; padding: 8px 10px; border: 1px solid var(--border); border-radius: 6px;
  font-size: 12px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  color: var(--text); outline: none; background: var(--bg-container); box-sizing: border-box;
  resize: vertical; line-height: 1.6;
}
.json-area:focus { border-color: var(--blue-6); box-shadow: 0 0 0 2px rgba(5,145,255,0.1); }
.checkbox-row {
  display: flex; align-items: center; gap: 8px;
  font-size: 13px; color: var(--text-secondary); cursor: pointer;
  padding: 4px 0;
}
.checkbox-row input[type="checkbox"] {
  width: 14px; height: 14px; accent-color: var(--blue-6); cursor: pointer;
}
.import-error {
  padding: 8px 12px; border-radius: 6px;
  background: var(--error-bg); border: 1px solid var(--error-border);
  color: var(--error-dark); font-size: 12px; line-height: 1.5;
}
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
