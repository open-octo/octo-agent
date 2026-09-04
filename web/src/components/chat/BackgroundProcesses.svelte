<script lang="ts">
  import { t } from '../../lib/i18n'

  // Real background tasks come from the background_tasks_update WS event.
  // Each task: { handle_id, command, startedAt } — ChatView anchors the
  // server's one-shot `elapsed` to a local timestamp, since the server only
  // re-broadcasts on tool calls and process exits.
  // onkill fires with the handle_id once the user has confirmed; the parent
  // owns the session id and the socket.
  let { tasks = [], now = 0, onkill }: { tasks?: any[]; now?: number; onkill?: (handleId: string) => void } = $props()

  let open = $state(false)
  let rootEl: HTMLElement | undefined = $state()
  let triggerEl: HTMLButtonElement | undefined = $state()

  // Two-step kill without a modal: the first click on a row's × turns it into a
  // "confirm" button, the second click kills. Only one row can be armed at a
  // time, and the arm is dropped when the popover closes or the row vanishes
  // (process exited on its own), so a stale arm can't fire on the wrong row.
  let armedId = $state<string | null>(null)
  $effect(() => {
    if (!open) armedId = null
    else if (armedId && !tasks.some(p => p.handle_id === armedId)) armedId = null
  })
  function onKillClick(handleId: string) {
    if (armedId !== handleId) {
      armedId = handleId
      return
    }
    armedId = null
    onkill?.(handleId)
  }

  function fmtElapsed(startedAt: number): string {
    const sec = now > 0 && startedAt > 0 ? (now - startedAt) / 1000 : 0
    if (!sec || sec < 0) return '0s'
    if (sec < 60) return `${Math.floor(sec)}s`
    const m = Math.floor(sec / 60)
    const s = Math.floor(sec % 60)
    return `${m}m ${s.toString().padStart(2, '0')}s`
  }

  // rootEl wraps only the trigger and the popover — not the empty width of the
  // row — so clicking anywhere else, that blank space included, closes it. The
  // trigger being inside means its own click never counts as "outside".
  //
  // Listened for during capture: the composer's menu chips stopPropagation on
  // their own clicks (that's how their menus survive the composer's own
  // window-level close), and a bubble-phase listener would never see those —
  // leaving this popover open underneath a freshly opened menu.
  function onDocClick(e: MouseEvent) {
    if (open && rootEl && !rootEl.contains(e.target as Node)) open = false
  }
  function onKeydown(e: KeyboardEvent) {
    if (!open || e.key !== 'Escape') return
    open = false
    triggerEl?.focus()
  }
</script>

<svelte:window onclickcapture={onDocClick} onkeydown={onKeydown} />

<div class="bg-line">
  <div class="bg-line-inner">
    <div class="bg-anchor" bind:this={rootEl}>
      <button
        class="bg-trigger"
        class:open
        bind:this={triggerEl}
        aria-expanded={open}
        aria-haspopup="true"
        aria-controls={open ? 'bg-proc-list' : undefined}
        onclick={() => (open = !open)}
      >
        <span class="dot"></span>
        <span class="lbl">{$t(tasks.length === 1 ? 'bgtask.n_process' : 'bgtask.n_processes').replace('{n}', String(tasks.length))}</span>
      </button>
      {#if open}
        <div class="bg-pop" id="bg-proc-list">
          {#each tasks as p (p.handle_id)}
          <div class="proc-row">
            <span class="proc-dot"></span>
            <div class="proc-info">
              <span class="proc-cmd mono">{p.command}</span>
            </div>
            <span class="proc-time">{$t('bgtask.running_elapsed').replace('{elapsed}', fmtElapsed(p.startedAt))}</span>
            <button
              class="proc-kill"
              class:armed={armedId === p.handle_id}
              title={armedId === p.handle_id ? $t('bgtask.kill_confirm') : $t('bgtask.kill')}
              aria-label={armedId === p.handle_id ? $t('bgtask.kill_confirm') : $t('bgtask.kill')}
              onclick={() => onKillClick(p.handle_id)}
            >
              {#if armedId === p.handle_id}
                <span class="proc-kill-lbl">{$t('bgtask.kill_confirm')}</span>
              {:else}
                <iconify-icon icon="ant-design:close-outlined" width="12"></iconify-icon>
              {/if}
            </button>
          </div>
          {/each}
        </div>
      {/if}
    </div>
  </div>
</div>

<style>
.bg-line { flex: 0 0 auto; }
.bg-line-inner { max-width: var(--chat-content-max-width, 1080px); width: 100%; margin: 0 auto; padding: 2px 24px; }
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
.proc-kill {
  display: inline-flex; align-items: center; justify-content: center;
  min-width: 20px; height: 20px; padding: 0 4px; margin-left: 2px; flex: 0 0 auto;
  border: none; border-radius: 4px; background: transparent;
  font-family: inherit; font-size: 12px; color: var(--text-secondary);
  cursor: pointer;
}
.proc-kill:hover { color: var(--error); background: var(--bg-table-header); }
.proc-kill.armed { color: #fff; background: var(--error); }
.proc-kill.armed:hover { filter: brightness(0.92); }
.proc-kill-lbl { white-space: nowrap; line-height: 1; }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
</style>
