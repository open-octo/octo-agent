<script lang="ts">
  import { mcpModalOpen, mcpServers, showToast } from '../../lib/stores'
  import { t, tr } from '../../lib/i18n'
  import { confirmDialog } from '../../lib/confirm'
  import * as api from '../../lib/api'

  let jsonText = $state('')
  let submitting = $state(false)

  // Re-seed the form (blank) whenever the modal opens.
  $effect(() => {
    if ($mcpModalOpen) jsonText = ''
  })

  const canSubmit = $derived(jsonText.trim().length > 0)

  async function refresh() {
    const data = await api.listMcpServers()
    mcpServers.set(data.servers as any)
  }

  async function submit() {
    if (!canSubmit) return
    submitting = true
    try {
      let parsed: any
      try { parsed = JSON.parse(jsonText) } catch { showToast('Invalid JSON', 'error'); submitting = false; return }
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
      await api.importMcpServers(servers)
      await refresh()
      showToast('Servers imported')
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
