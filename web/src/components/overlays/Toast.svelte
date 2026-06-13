<script lang="ts">
  import { toast } from '../../lib/stores'
</script>

{#if $toast}
<div class="toast">
  <iconify-icon icon="ant-design:check-circle-filled" width="16" style="color:var(--success)"></iconify-icon>
  <span>{$toast}</span>
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
