<script lang="ts">
  import { toasts, dismissToast } from '../../lib/stores'
  import { t } from '../../lib/i18n'

  // The store holds a stack of { id, msg, type }; render each, and pick the
  // icon from type.
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

<!-- aria-live announces new toasts to assistive tech without stealing focus;
     "polite" so it doesn't interrupt a screen reader mid-sentence — even an
     error toast is a transient notice, not a blocking alert. -->
<div class="toast-stack" role="status" aria-live="polite">
  {#each $toasts as toast (toast.id)}
    <div class="toast">
      <iconify-icon icon={ICON[toast.type] ?? ICON.success} width="16" style="color:{COLOR[toast.type] ?? COLOR.success}"></iconify-icon>
      <span>{toast.msg}</span>
      <button class="toast-close" onclick={() => dismissToast(toast.id)} aria-label={$t('common.close')}>
        <iconify-icon icon="ant-design:close-outlined" width="12"></iconify-icon>
      </button>
    </div>
  {/each}
</div>

<style>
.toast-stack {
  position: fixed; right: 24px; bottom: 24px; z-index: 1100;
  display: flex; flex-direction: column-reverse; gap: 8px;
  align-items: flex-end;
}
.toast {
  display: flex; align-items: center; gap: 10px;
  padding: 11px 12px 11px 14px; background: var(--bg-container);
  border: 1px solid var(--border-secondary); border-radius: 8px;
  box-shadow: 0 8px 24px rgba(15,23,42,0.14);
  animation: octo-fadein 0.2s ease;
  font-size: 13px; color: var(--text);
  max-width: 360px;
}
.toast-close {
  border: none; background: transparent; padding: 2px; margin-left: 2px;
  display: flex; align-items: center; justify-content: center;
  border-radius: 4px; cursor: pointer; color: var(--text-tertiary); flex: 0 0 auto;
}
.toast-close:hover { background: var(--hover-neutral); color: var(--text); }
</style>
