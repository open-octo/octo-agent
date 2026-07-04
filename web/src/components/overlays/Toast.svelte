<script lang="ts">
  import { toast } from '../../lib/stores'

  // The store holds { msg, type }; render msg, and pick the icon from type.
  const ICON: Record<string, string> = {
    success: 'ant-design:check-circle-filled',
    error:   'ant-design:close-circle-filled',
    warning: 'ant-design:warning-outlined',
    info:    'ant-design:info-circle-filled',
  }
  const COLOR: Record<string, string> = {
    success: 'var(--success)',
    error:   'var(--error)',
    warning: 'var(--warning)',
    info:    'var(--blue-6)',
  }
</script>

{#if $toast}
<div class="toast">
  <iconify-icon icon={ICON[$toast.type] ?? ICON.success} width="16" style="color:{COLOR[$toast.type] ?? COLOR.success}"></iconify-icon>
  <span>{$toast.msg}</span>
</div>
{/if}

<style>
.toast {
  position: fixed; right: 24px; bottom: 24px; z-index: 1100;
  display: flex; align-items: center; gap: 10px;
  padding: 11px 14px; background: var(--bg-container);
  border: 1px solid var(--border-secondary); border-radius: 8px;
  box-shadow: 0 8px 24px rgba(15,23,42,0.14);
  animation: octo-fadein 0.2s ease;
  font-size: 13px; color: var(--text);
}
</style>
