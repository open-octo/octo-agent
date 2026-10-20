<script lang="ts">
  // Connection state, bound to the shared ws stores. Hidden while connected —
  // the normal state needs no announcing — and shown when the socket is down so
  // the feed never silently shows stale data as if live. The delay keeps the
  // brief connecting phase of every app launch from flashing it.
  import { wsState, wsReconnect } from '../lib/ws'
  import { t } from '../lib/i18n'

  const SHOW_AFTER_MS = 1000

  let visible = $state(false)
  $effect(() => {
    if ($wsState === 'connected') {
      visible = false
      return
    }
    const timer = setTimeout(() => (visible = true), SHOW_AFTER_MS)
    return () => clearTimeout(timer)
  })

  const label = $derived(
    $wsState === 'connecting' || $wsReconnect ? $t('m.dev_connecting') : $t('m.dev_offline'),
  )
</script>

{#if visible}
  <div class="banner">
    <span class="d"></span>
    <span class="txt">{label}</span>
  </div>
{/if}

<style>
  .banner {
    display: flex;
    align-items: center;
    gap: 9px;
    background: var(--m-surface-2);
    border-radius: 12px;
    padding: 11px 14px;
    margin-bottom: 14px;
    font-size: 12.5px;
    color: var(--m-text-2);
  }
  .d {
    flex: none;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--m-text-4);
  }
  .txt { flex: 1; }
</style>
