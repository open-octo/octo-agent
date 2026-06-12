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
  background: #FFFFFF;
  border-radius: 16px;
  box-shadow: 0 8px 24px rgba(15,23,42,0.03);
  padding: 20px 24px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.lbl { font-size: 13px; color: rgba(0,0,0,0.45); }
.val { font-size: 28px; font-weight: 600; color: #1F1F1F; }
.delta { font-size: 12px; color: rgba(0,0,0,0.45); }
.delta.up { color: #389E0D; }
.delta.down { color: #FF4D4F; }
</style>
