<script lang="ts">
  import type { FeedItem, FeedKind } from './feedGroups'

  let { item, onOpen }: { item: FeedItem; onOpen: (id: string, kind: FeedKind) => void } = $props()

  const s = $derived(item.session)
  const title = $derived(s.title || s.name || '未命名会话')

  function ago(iso: string): string {
    if (!iso) return ''
    const ms = Date.now() - new Date(iso).getTime()
    if (Number.isNaN(ms)) return ''
    const m = Math.floor(ms / 60000)
    if (m < 1) return '刚刚'
    if (m < 60) return `${m} 分钟前`
    const h = Math.floor(m / 60)
    if (h < 24) return `${h} 小时前`
    return `${Math.floor(h / 24)} 天前`
  }
</script>

<button
  class="card"
  class:approval={item.kind === 'approval'}
  class:reply={item.kind === 'reply'}
  onclick={() => onOpen(s.id, item.kind)}
>
  <div class="top">
    <span class="title">{title}</span>
    {#if item.kind === 'reply'}
      <span class="tag tag-accent">待你回复</span>
    {:else if item.kind === 'approval'}
      <span class="tag tag-warn">待审批</span>
    {:else if item.kind === 'running'}
      <span class="pulse"></span>
    {:else}
      <svg class="done" width="18" height="18" viewBox="0 0 24 24"><circle cx="12" cy="12" r="10" fill="var(--m-success)"/><path d="m8 12 2.5 2.5L16 9" stroke="#fff" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"/></svg>
    {/if}
  </div>
  <div class="meta">
    {#if item.kind === 'running'}
      <span class="running">agent 正在处理<span class="blink">•••</span></span>
    {:else}
      <span class="mono">{s.model || s.model_id || ''}</span>
    {/if}
    <span class="dot">·</span>
    <span>{ago(s.updated_at)}</span>
  </div>
</button>

<style>
  .card {
    display: block;
    width: 100%;
    text-align: left;
    background: var(--m-surface);
    border: 1px solid transparent;
    border-radius: 14px;
    padding: 14px 16px;
    margin-bottom: 12px;
    cursor: pointer;
    font-family: inherit;
    box-shadow: var(--m-shadow-card);
  }
  .card.approval { border-color: var(--m-tag-warn-border); }
  .card.reply { border-color: var(--m-accent); }
  .top {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
  }
  .title {
    font-size: 15px;
    font-weight: 600;
    color: var(--m-text);
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
  }
  .tag {
    flex: none;
    font-size: 11px;
    font-weight: 600;
    border-radius: 4px;
    padding: 2px 7px;
  }
  .tag-accent { color: #fff; background: var(--m-accent); }
  .tag-warn {
    color: var(--m-tag-warn-text);
    background: var(--m-tag-warn-bg);
    border: 1px solid var(--m-tag-warn-border);
  }
  .done { flex: none; }
  .pulse {
    flex: none;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--m-accent);
    position: relative;
  }
  .pulse::after {
    content: "";
    position: absolute;
    inset: -4px;
    border-radius: 50%;
    border: 2px solid var(--m-accent);
    animation: pr 1.6s ease-out infinite;
  }
  @keyframes pr { 0% { transform: scale(.6); opacity: .8 } 100% { transform: scale(1.8); opacity: 0 } }
  .meta {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-top: 8px;
    font-size: 12px;
    color: var(--m-text-3);
  }
  .running { color: var(--m-accent); }
  .blink { animation: bk 1.1s steps(1) infinite; letter-spacing: 2px; margin-left: 3px; }
  @keyframes bk { 50% { opacity: .2 } }
  .mono { font-family: ui-monospace, Menlo, monospace; }
  .dot { color: var(--m-text-4); }
  @media (prefers-reduced-motion: reduce) {
    .pulse::after, .blink { animation: none; }
  }
</style>
