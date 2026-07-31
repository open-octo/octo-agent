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
  border: 1px solid var(--border);
  border-radius: var(--radius-card);
  box-shadow: var(--card-shadow);
  padding: 16px 18px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.lbl { font-size: 12px; color: var(--text-secondary); }
.val { font-size: 26px; font-weight: 700; letter-spacing: -0.02em; color: var(--text-heading); }
.delta { font-size: 12px; color: var(--text-tertiary); }
.delta.up { color: var(--success-text); }
.delta.down { color: var(--error); }
</style>
