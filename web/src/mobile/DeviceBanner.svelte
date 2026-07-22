<script lang="ts">
  // Connection state, bound to the shared ws stores. The full "connected to
  // <host>, N sessions" form and the push-woken offline flow arrive with the
  // managed tunnel (M1); for now this reflects the live socket state so the feed
  // never silently shows stale data as if live.
  import { wsState, wsReconnect } from '../lib/ws'

  const label = $derived(
    $wsState === 'connected'
      ? '已连接远程主机'
      : $wsState === 'connecting' || $wsReconnect
        ? '连接中…'
        : '已断开 · 等待重连',
  )
</script>

<div class="banner" class:off={$wsState !== 'connected'}>
  <span class="d" class:live={$wsState === 'connected'}></span>
  <span class="txt">{label}</span>
</div>

<style>
  .banner {
    display: flex;
    align-items: center;
    gap: 9px;
    background: var(--m-accent-soft);
    border-radius: 12px;
    padding: 11px 14px;
    margin-bottom: 14px;
    font-size: 12.5px;
    color: var(--m-text);
  }
  .banner.off { background: var(--m-surface-2); color: var(--m-text-2); }
  .d {
    flex: none;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--m-text-4);
  }
  .d.live { background: var(--m-success); }
  .txt { flex: 1; }
</style>
