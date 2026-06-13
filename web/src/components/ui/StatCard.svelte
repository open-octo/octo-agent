<script lang="ts">
  let { label = '', value = '', delta = '', deltaDir = '' }: {
    label?: string
    value?: string
    delta?: string
    deltaDir?: 'up' | 'down' | ''
  } = $props()
</script>

<div class="card">
  <span class="lbl">{label}</span>
  <span class="val">{value}</span>
  {#if delta}
    <span class="delta" class:up={deltaDir === 'up'} class:down={deltaDir === 'down'}>{delta}</span>
  {/if}
</div>

<style>
.card {
  background: var(--bg-container);
  border-radius: 16px;
  box-shadow: var(--card-shadow);
  padding: 20px 24px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.lbl { font-size: 13px; color: var(--text-tertiary); }
.val { font-size: 28px; font-weight: 600; color: var(--text-heading); }
.delta { font-size: 12px; color: var(--text-tertiary); }
.delta.up { color: var(--success-text); }
.delta.down { color: var(--error); }
</style>
