<script lang="ts">
  import type { TagStatus } from '../../lib/types'
  let { status = 'default', children }: { status?: TagStatus, children?: any } = $props()

  const styles: Record<TagStatus, string> = {
    success: 'background:var(--success-bg);color:var(--success-text)',
    info:    'background:var(--active-blue-bg);color:var(--blue-6)',
    warning: 'background:var(--warning-bg);color:var(--warning-text)',
    error:   'background:var(--error-bg);color:var(--error-text)',
    default: 'background:var(--hover-neutral);color:var(--text-secondary)',
  }
</script>

<span style="display:inline-flex;align-items:center;gap:4px;padding:2px 9px;border-radius:999px;font-size:11px;font-weight:500;white-space:nowrap;{styles[status]}">
  {@render children?.()}
</span>
