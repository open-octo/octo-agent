<script lang="ts">
  // Version badge + self-upgrade flow. Mirrors the pre-Svelte version.js:
  //   idle           → version text (amber dot when an update is available)
  //   upgrading      → spinner; popover streams upgrade_log lines
  //   needs_restart  → orange dot; popover offers Restart
  //   reconnecting   → restart sent, polling /api/version until the server is back
  //   restart_failed → 30s elapsed, server still down → recovery options
  //   done           → server reconnected → reload
  //
  // /api/version and the upgrade_log / upgrade_complete WS globals are the
  // server contract (internal/server/version_upgrade_handlers.go); /api/restart
  // triggers the supervisor respawn.
  import { onMount } from 'svelte'
  import * as api from '../../lib/api'
  import { ws } from '../../lib/ws'
  import { t } from '../../lib/i18n'
  import { nativeShell, localAccess, isDesktopShell } from '../../lib/stores'

  type Phase = 'idle' | 'upgrading' | 'needs_restart' | 'reconnecting' | 'restart_failed' | 'done'

  let current = $state('')
  let latest = $state('')
  let needsUpdate = $state(false)
  let cliCommand = $state('octo')
  // upgradeMode is 'cli' (octo serve — the in-place swap below is valid) or
  // 'installer' (desktop build — a swap would clobber the running binary, so we
  // link to the download page instead). downloadUrl is that download page.
  let upgradeMode = $state<'cli' | 'installer'>('cli')
  let downloadUrl = $state('')
  let phase = $state<Phase>('idle')
  let logLines = $state<string[]>([])
  let open = $state(false)
  let logEl = $state<HTMLElement | null>(null)

  const RECONNECT_TIMEOUT_MS = 30_000

  // The hub reports native=true to every client, but only the desktop-shell
  // webview should behave as native (OS file dialog, OS notifications, header
  // inset past the traffic lights) — hence the isDesktopShell gate on top.

  let versionLabel = $derived(current ? `v${current}` : '')
  // Locked open: the user must not dismiss the popover mid-install or while the
  // server is restarting (the flow would keep running with no surface).
  let locked = $derived(phase === 'upgrading' || phase === 'reconnecting')

  async function checkVersion() {
    try {
      const d = await api.getVersion() as any
      current = d.current ?? (d.version ?? '').replace(/^v/, '')
      latest = d.latest ?? ''
      needsUpdate = !!d.needs_update
      if (d.cli_command) cliCommand = d.cli_command
      upgradeMode = d.upgrade_mode === 'installer' ? 'installer' : 'cli'
      downloadUrl = d.download_url ?? ''
      nativeShell.set(d.native === true && isDesktopShell)
      localAccess.set(d.local === true)
    } catch { /* badge stays minimal */ }
  }

  // Installer mode: open the release download page instead of swapping the
  // binary in place. A loopback peer (the desktop window, or a localhost
  // browser) routes through the native bridge; a remote browser can't drive the
  // desktop process, so it opens the page in its own new tab.
  async function downloadUpdate() {
    if (!downloadUrl) return
    if ($localAccess) {
      try { await api.openExternal(downloadUrl); open = false; return } catch { /* fall through to window.open */ }
    }
    window.open(downloadUrl, '_blank', 'noopener')
    open = false
  }

  onMount(() => {
    checkVersion()
    // upgrade_log / upgrade_complete are global broadcasts (no session_id); the
    // WS dispatch is by type, so these fire regardless of the active session.
    const offLog = ws.on('upgrade_log', (ev: any) => {
      logLines = [...logLines, ev.line ?? '']
      queueMicrotask(() => { if (logEl) logEl.scrollTop = logEl.scrollHeight })
    })
    const offDone = ws.on('upgrade_complete', (ev: any) => {
      if (ev.success) { needsUpdate = false; phase = 'needs_restart' }
      else { phase = 'idle' } // failure: badge stays update-available
    })
    return () => { offLog(); offDone() }
  })

  async function startUpgrade() {
    if (phase === 'upgrading') return
    phase = 'upgrading'
    logLines = []
    open = true
    try {
      const res = await fetch('/api/version/upgrade', { method: 'POST' })
      // 409 = another upgrade already in flight; its broadcasts drive the UI.
      // Any other non-ok sends no completion event, so unwind here.
      if (!res.ok && res.status !== 409) phase = 'idle'
    } catch { phase = 'idle' }
  }

  function startRestart() {
    phase = 'reconnecting'
    open = true
    fetch('/api/restart', { method: 'POST' }).catch(() => {})
    waitForReconnect()
  }

  function waitForReconnect() {
    const deadline = Date.now() + RECONNECT_TIMEOUT_MS
    const tick = async () => {
      if (phase !== 'reconnecting') return
      if (Date.now() > deadline) { phase = 'restart_failed'; return }
      try {
        const res = await fetch('/api/version', { cache: 'no-store' })
        if (res.ok) { phase = 'done'; setTimeout(() => location.reload(), 800); return }
      } catch { /* server not back yet */ }
      setTimeout(tick, 2000)
    }
    // Give the process a head start before the first poll.
    setTimeout(tick, 2500)
  }

  function retryReconnect() { phase = 'reconnecting'; waitForReconnect() }

  function toggle() {
    if (open && locked) return
    open = !open
  }
  function close() {
    if (locked) return
    open = false
  }
</script>

<div class="vb-wrap">
  {#if open}
    <button class="vb-scrim" onclick={close} aria-label="close" tabindex="-1"></button>
    <div class="vb-pop" role="dialog" aria-modal="false">
      {#if phase === 'restart_failed'}
        <p class="vb-title warn">⚠ {$t('upgrade.restart.timeout.title')}</p>
        <p class="vb-desc">{$t('upgrade.restart.timeout.desc')}</p>
        <ul class="vb-list">
          <li>{$t('upgrade.restart.timeout.tray')}</li>
          <li>{$t('upgrade.restart.timeout.cli')} <code class="vb-cmd">{cliCommand} serve</code></li>
        </ul>
        <div class="vb-actions">
          <button class="vb-btn-primary" onclick={retryReconnect}>{$t('upgrade.restart.timeout.retry')}</button>
        </div>
      {:else if phase === 'reconnecting'}
        <div class="vb-center">
          <div class="vb-spinner"></div>
          <p class="vb-desc">{$t('upgrade.reconnecting')}</p>
        </div>
      {:else if phase === 'upgrading'}
        <div class="vb-prog-head">
          <div class="vb-dot upgrading"></div>
          <span>{$t('upgrade.installing')}</span>
        </div>
        <pre class="vb-log" bind:this={logEl}>{logLines.join('\n')}</pre>
      {:else if phase === 'needs_restart' || phase === 'done'}
        <p class="vb-title"><span class="vb-ok">✓</span> {$t('upgrade.done')}</p>
        <div class="vb-actions">
          <button class="vb-btn-primary" onclick={startRestart}>{$t('upgrade.btn.restart')}</button>
        </div>
      {:else if needsUpdate}
        <p class="vb-desc">{$t('upgrade.desc')}</p>
        <p class="vb-versions">v{current} <span class="vb-arrow">→</span> v{latest}</p>
        <div class="vb-actions">
          {#if upgradeMode === 'installer'}
            <button class="vb-btn-primary" onclick={downloadUpdate}>{$t('upgrade.btn.download')}</button>
          {:else}
            <button class="vb-btn-primary" onclick={startUpgrade}>{$t('upgrade.btn.upgrade')}</button>
          {/if}
          <button class="vb-btn-cancel" onclick={close}>{$t('upgrade.btn.cancel')}</button>
        </div>
      {:else}
        <p class="vb-title"><span class="vb-ok">✓</span> {$t('upgrade.uptodate')}</p>
      {/if}
    </div>
  {/if}

  {#if versionLabel}
    <button
      class="vb-badge"
      class:has-update={needsUpdate && phase === 'idle'}
      onclick={toggle}
      title={phase === 'upgrading' ? $t('upgrade.tooltip.upgrading')
        : phase === 'needs_restart' ? $t('upgrade.tooltip.needs_restart')
        : phase === 'done' ? $t('upgrade.tooltip.done')
        : needsUpdate ? $t('upgrade.tooltip.new')
        : $t('upgrade.tooltip.ok')}
    >
      <span class="vb-text">{versionLabel}</span>
      {#if phase === 'upgrading'}
        <span class="vb-dot upgrading"></span>
      {:else if phase === 'needs_restart'}
        <span class="vb-dot restart"></span>
      {:else if phase === 'done'}
        <span class="vb-check">✓</span>
      {:else if needsUpdate}
        <span class="vb-dot update"></span>
      {/if}
    </button>
  {/if}
</div>

<style>
.vb-wrap { position: relative; margin-left: auto; }
.vb-badge {
  display: flex; align-items: center; gap: 5px;
  background: none; border: none; padding: 2px 4px;
  font-size: 11px; color: var(--text-tertiary);
  cursor: pointer; font-family: inherit; border-radius: 4px;
}
.vb-badge:hover { color: var(--text-secondary); background: var(--hover-neutral); }
.vb-text { font-variant-numeric: tabular-nums; }
.vb-dot {
  width: 7px; height: 7px; border-radius: 50%; flex-shrink: 0;
}
.vb-dot.update { background: var(--warning); animation: vb-pulse 1.6s ease-in-out infinite; }
.vb-dot.restart { background: #fa8c16; animation: vb-bounce 1s ease-in-out infinite; }
.vb-dot.upgrading {
  background: none; width: 9px; height: 9px;
  border: 2px solid var(--border); border-top-color: var(--blue-6);
  border-radius: 50%; animation: octo-spin 0.7s linear infinite;
}
.vb-check { color: var(--success); font-size: 11px; }
@keyframes vb-pulse { 0%,100% { opacity: 1; } 50% { opacity: 0.35; } }
@keyframes vb-bounce { 0%,100% { transform: translateY(0); } 50% { transform: translateY(-2px); } }

.vb-scrim { position: fixed; inset: 0; z-index: 60; background: none; border: none; cursor: default; }
.vb-pop {
  position: absolute; bottom: calc(100% + 8px); right: 0; z-index: 61;
  width: 260px; padding: 14px;
  background: var(--bg-container); border: 1px solid var(--border);
  border-radius: 10px; box-shadow: 0 12px 32px rgba(0,0,0,0.16);
  animation: octo-fadein 0.14s ease;
}
.vb-title { margin: 0 0 8px; font-size: 13px; font-weight: 600; color: var(--text-heading); }
.vb-title.warn { color: var(--warning-text, #d46b08); }
.vb-ok { color: var(--success); }
.vb-desc { margin: 0 0 8px; font-size: 12px; line-height: 1.6; color: var(--text-secondary); }
.vb-versions { margin: 0 0 12px; font-size: 13px; color: var(--text-primary); font-variant-numeric: tabular-nums; }
.vb-arrow { color: var(--text-tertiary); margin: 0 4px; }
.vb-list { margin: 0 0 12px; padding-left: 18px; font-size: 12px; line-height: 1.7; color: var(--text-secondary); }
.vb-cmd {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px; padding: 1px 5px; border-radius: 4px;
  background: var(--hover-neutral); color: var(--text-primary);
}
.vb-actions { display: flex; gap: 8px; }
.vb-btn-primary {
  height: 30px; padding: 0 14px; border: none; background: var(--blue-6);
  border-radius: 6px; font-size: 12px; color: #fff; cursor: pointer; font-family: inherit;
}
.vb-btn-primary:hover { background: var(--blue-5); }
.vb-btn-cancel {
  height: 30px; padding: 0 12px; border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 6px; font-size: 12px; color: var(--text-secondary); cursor: pointer; font-family: inherit;
}
.vb-btn-cancel:hover { border-color: var(--blue-5); color: var(--blue-5); }
.vb-prog-head { display: flex; align-items: center; gap: 8px; margin-bottom: 8px; font-size: 12px; color: var(--text-secondary); }
.vb-log {
  margin: 0; max-height: 160px; overflow-y: auto;
  padding: 8px 10px; background: var(--terminal-bg); color: var(--terminal-text);
  border-radius: 6px; font-size: 11px; line-height: 1.5;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  white-space: pre-wrap; word-break: break-all;
}
.vb-center { display: flex; flex-direction: column; align-items: center; gap: 10px; padding: 6px 0; }
.vb-spinner {
  width: 24px; height: 24px; border: 3px solid var(--border);
  border-top-color: var(--blue-6); border-radius: 50%; animation: octo-spin 0.7s linear infinite;
}
</style>
