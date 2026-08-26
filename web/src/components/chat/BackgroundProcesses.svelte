<script lang="ts">
  import { t } from '../../lib/i18n'

  // Real background tasks come from the background_task_update WS event.
  // Each task: { handle_id, command, elapsed } (elapsed in seconds).
  let { tasks = [] }: { tasks?: any[] } = $props()

  let open = $state(false)
  let rootEl: HTMLElement | undefined = $state()

  function fmtElapsed(sec: number): string {
    if (!sec || sec < 0) return '0s'
    if (sec < 60) return `${Math.floor(sec)}s`
    const m = Math.floor(sec / 60)
    const s = Math.floor(sec % 60)
    return `${m}m ${s.toString().padStart(2, '0')}s`
  }

  // The trigger lives inside rootEl, so its own click never closes the popover.
  function onDocClick(e: MouseEvent) {
    if (open && rootEl && !rootEl.contains(e.target as Node)) open = false
  }
  function onKeydown(e: KeyboardEvent) {
    if (open && e.key === 'Escape') open = false
  }
</script>

<svelte:window onclick={onDocClick} onkeydown={onKeydown} />

<div class="bg-line" bind:this={rootEl}>
  <div class="bg-line-inner">
    <div class="bg-anchor">
      <button class="bg-trigger" class:open aria-expanded={open} onclick={() => (open = !open)}>
        <span class="dot"></span>
        <span class="lbl">{$t(tasks.length === 1 ? 'bgtask.n_process' : 'bgtask.n_processes').replace('{n}', String(tasks.length))}</span>
      </button>
      {#if open}
        <div class="bg-pop">
          {#each tasks as p (p.handle_id)}
          <div class="proc-row">
            <span class="proc-dot"></span>
            <div class="proc-info">
              <span class="proc-cmd mono">{p.command}</span>
            </div>
            <span class="proc-time">{$t('bgtask.running_elapsed').replace('{elapsed}', fmtElapsed(p.elapsed))}</span>
          </div>
          {/each}
        </div>
      {/if}
    </div>
  </div>
</div>

<style>
.bg-line { flex: 0 0 auto; }
.bg-line-inner { max-width: 800px; margin: 0 auto; padding: 2px 24px; }
.bg-anchor { position: relative; display: inline-block; }
.bg-trigger {
  display: inline-flex; align-items: center; gap: 8px;
  padding: 4px 0; border: none; background: transparent;
  font-family: inherit; font-size: 13px; color: var(--text-secondary);
  cursor: pointer;
}
.bg-trigger:hover, .bg-trigger.open { color: var(--blue-6); }
.dot {
  width: 7px; height: 7px; border-radius: 9999px; background: var(--success);
  animation: octo-dot 1.4s infinite; flex: 0 0 auto;
}
.bg-pop {
  position: absolute; bottom: calc(100% + 6px); left: 0; z-index: 50;
  min-width: 260px; max-width: 420px; max-height: 280px; overflow-y: auto;
  display: flex; flex-direction: column; gap: 6px;
  background: var(--bg-container); border: 1px solid var(--border-secondary); border-radius: 10px;
  box-shadow: 0 8px 24px rgba(15,23,42,0.14); padding: 6px;
}
.proc-row {
  display: flex; align-items: center; gap: 10px;
  padding: 8px 12px; border: 1px solid var(--border-table); border-radius: 8px; background: var(--bg-table-header);
}
.proc-dot {
  width: 7px; height: 7px; flex: 0 0 7px; border-radius: 9999px; background: var(--success);
  animation: octo-dot 1.4s infinite;
}
.proc-info { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 1px; }
.proc-cmd { font-size: 13px; color: var(--text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.proc-time { font-size: 12px; color: var(--success); flex: 0 0 auto; }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
</style>
