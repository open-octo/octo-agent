<script lang="ts">
  import type { TagStatus } from '../../lib/types'
  let { status = 'default', children }: { status?: TagStatus, children?: any } = $props()

  const styles: Record<TagStatus, string> = {
    success: 'background:#F6FFED;border-color:#B7EB8F;color:#52C41A',
    info:    'background:#E6F4FF;border-color:#91CAFF;color:#1677FF',
    warning: 'background:#FFFBE6;border-color:#FFE58F;color:#FAAD14',
    error:   'background:#FFF1F0;border-color:#FFCCC7;color:#FF4D4F',
    default: 'background:#FAFAFA;border-color:#D9D9D9;color:rgba(0,0,0,0.88)',
  }
</script>

<span style="display:inline-flex;align-items:center;height:22px;padding:0 8px;border:1px solid;border-radius:4px;font-size:12px;white-space:nowrap;{styles[status]}">
  {@render children?.()}
</span>
