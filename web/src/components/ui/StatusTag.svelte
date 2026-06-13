<script lang="ts">
  import type { TagStatus } from '../../lib/types'
  let { status = 'default', children }: { status?: TagStatus, children?: any } = $props()

  const styles: Record<TagStatus, string> = {
    success: 'background:var(--success-bg);border-color:var(--success-border);color:var(--success)',
    info:    'background:var(--blue-1);border-color:var(--info-border);color:var(--blue-6)',
    warning: 'background:var(--warning-bg);border-color:var(--warning-border);color:var(--warning)',
    error:   'background:var(--error-bg);border-color:var(--error-border);color:var(--error)',
    default: 'background:var(--bg-table-header);border-color:var(--border);color:var(--text)',
  }
</script>

<span style="display:inline-flex;align-items:center;height:22px;padding:0 8px;border:1px solid;border-radius:4px;font-size:12px;white-space:nowrap;{styles[status]}">
  {@render children?.()}
</span>
