<script lang="ts">
  import { mcpModalOpen, mcpServers, showToast } from '../../lib/stores'
  import * as api from '../../lib/api'

  let name = $state('')
  let command = $state('')
  let transport = $state('stdio')
  let submitting = $state(false)

  async function add() {
    if (!name.trim() || !command.trim()) return
    submitting = true
    try {
      await api.createMcpServer({ name: name.trim(), command: command.trim() })
      // Reload the server list so McpView reflects the addition.
      const data = await api.listMcpServers()
      mcpServers.set(data.servers as any)
      mcpModalOpen.set(false)
      name = ''
      command = ''
      transport = 'stdio'
      showToast('Server added · connecting…')
    } catch (e: any) {
      showToast(e.message ?? 'Failed to add server', 'error')
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
      <span class="modal-title">Add MCP Server</span>
      <button class="close-btn" onclick={close}>
        <iconify-icon icon="ant-design:close-outlined" width="14"></iconify-icon>
      </button>
    </div>
    <div class="modal-body">
      <div class="field">
        <label>Name</label>
        <input placeholder="e.g. github" bind:value={name} />
      </div>
      <div class="field">
        <label>Launch command</label>
        <input placeholder="npx -y @modelcontextprotocol/server-…" bind:value={command} />
      </div>
      <div class="field">
        <label>Transport</label>
        <select bind:value={transport}>
          {#each ['stdio', 'http', 'sse'] as opt}<option>{opt}</option>{/each}
        </select>
      </div>
    </div>
    <div class="modal-footer">
      <button class="btn-secondary" onclick={close} disabled={submitting}>Cancel</button>
      <button class="btn-primary" onclick={add} disabled={submitting || !name.trim() || !command.trim()}>
        {submitting ? 'Adding…' : 'Add Server'}
      </button>
    </div>
  </div>
</div>
{/if}

<style>
.backdrop {
  position: fixed; inset: 0; z-index: 1000; background: rgba(0,0,0,0.45);
  display: flex; align-items: center; justify-content: center; padding: 24px;
}
.modal {
  width: 100%; max-width: 380px; background: #fff;
  border-radius: 12px; overflow: hidden;
  box-shadow: 0 16px 48px rgba(0,0,0,0.18);
  animation: octo-fadein 0.16s ease;
}
.modal-header {
  display: flex; align-items: center; gap: 8px;
  padding: 14px 18px; border-bottom: 1px solid #F0F0F0;
}
.modal-title { font-size: 15px; font-weight: 600; color: #1F1F1F; flex: 1; }
.close-btn {
  width: 28px; height: 28px; border: none; background: transparent;
  border-radius: 6px; display: flex; align-items: center; justify-content: center;
  cursor: pointer; color: rgba(0,0,0,0.45);
}
.close-btn:hover { background: rgba(0,0,0,0.04); }
.modal-body { padding: 16px 18px; display: flex; flex-direction: column; gap: 12px; }
.field { display: flex; flex-direction: column; gap: 6px; }
label { font-size: 12px; color: rgba(0,0,0,0.65); }
input, select {
  width: 100%; height: 32px; padding: 0 10px;
  border: 1px solid #D9D9D9; border-radius: 6px; font-size: 13px;
  color: rgba(0,0,0,0.88); font-family: inherit; outline: none; background: #fff;
  box-sizing: border-box;
}
input:focus, select:focus { border-color: #1677FF; box-shadow: 0 0 0 2px rgba(5,145,255,0.1); }
.modal-footer {
  padding: 12px 18px; border-top: 1px solid #F0F0F0;
  display: flex; justify-content: flex-end; gap: 8px;
}
.btn-secondary {
  height: 32px; padding: 0 14px; border: 1px solid #D9D9D9; background: #fff;
  border-radius: 6px; font-size: 14px; color: rgba(0,0,0,0.65); cursor: pointer; font-family: inherit;
}
.btn-secondary:hover:not(:disabled) { border-color: #4096FF; color: #4096FF; }
.btn-secondary:disabled { opacity: 0.5; cursor: not-allowed; }
.btn-primary {
  height: 32px; padding: 0 14px; border: none; background: #1677FF;
  border-radius: 6px; font-size: 14px; color: #fff; cursor: pointer; font-family: inherit;
}
.btn-primary:hover:not(:disabled) { background: #4096FF; }
.btn-primary:disabled { opacity: 0.5; cursor: not-allowed; }
</style>
