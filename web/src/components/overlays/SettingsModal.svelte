<script lang="ts">
  import Segment from '../ui/Segment.svelte'
  import Switch from '../ui/Switch.svelte'
  import EndpointsSection from '../settings/EndpointsSection.svelte'
  import QrCode from '../ui/QrCode.svelte'
  import FileRecallView from '../../views/FileRecallView.svelte'
  import { get } from 'svelte/store'
  import { showToast, nativeShell, settingsModalOpen, onboardPhase, sessions, collapsedSessions, activeSessionId, view, clearPendingSessionOpts } from '../../lib/stores'
  import { setLocale, t, tr } from '../../lib/i18n'
  import { getMode, setMode, type ThemeMode } from '../../lib/theme'
  import { notificationsEnabled, setNotificationsEnabled } from '../../lib/notifications'
  import { openUrl } from '../../lib/externalLinks'
  import { confirmDialog } from '../../lib/confirm'
  import { ago } from '../../lib/relTime'
  import * as api from '../../lib/api'

  const LICENSE_URL = 'https://github.com/open-octo/octo-agent/blob/main/LICENSE.txt'

  const fontZoomMap: Record<string, string> = { Small: '0.9', Medium: '1', Large: '1.1' }
  const modeToThemeLabel: Record<string, string> = { light: 'Light', dark: 'Dark', system: 'System' }

  // This modal is mounted unconditionally at app root, so the applying
  // $effects at the bottom run at boot, not on open. Seeding fontSize/theme
  // from the persisted prefs (not hardcoded defaults) is what keeps a page
  // reload from overwriting the stored choice before Settings is ever opened
  // (#2087) — and since nothing else reads octo.fontSize, this seed is also
  // the font-size restore path.
  function storedFontSize(): string {
    const v = localStorage.getItem('octo.fontSize')
    return v === 'Small' || v === 'Medium' || v === 'Large' ? v : 'Medium'
  }

  // --- local state ---
  let language      = $state('en')
  let fontSize      = $state(storedFontSize())
  let theme         = $state(modeToThemeLabel[getMode()] ?? 'Light')
  let autostart     = $state(false) // desktop shell only
  let versionStr    = $state('')
  let latestStr     = $state('')
  let updateAvail   = $state(false)
  let downloadUrl   = $state('')
  let checkingUpdate = $state(false)
  // 'installer' (desktop build) downloads the release page; 'cli' (plain
  // `octo serve`) can swap itself in place — same distinction VersionBadge
  // uses, which stays mounted (Sidebar.svelte) and picks up this modal's
  // triggered upgrade via the same global upgrade_log/upgrade_complete WS
  // broadcasts, so its badge reflects progress after this modal closes.
  let upgradeMode   = $state<'cli' | 'installer'>('cli')
  let loading       = $state(true)

  let cat = $state<'general' | 'endpoints' | 'agent' | 'mobile' | 'data' | 'about'>('general')
  let modalEl = $state<HTMLDivElement | null>(null)

  // 数据管理 has its own two-level nav — a list of managed things, and one
  // sub-view per thing — because unlike every other category here it isn't a
  // handful of settings but potentially long lists (archived sessions, trashed
  // files). Reset whenever the category (or the whole modal) changes, so
  // leaving and returning to 数据管理 always lands on the list, not wherever
  // you left off.
  let dataSubView = $state<'none' | 'archived' | 'trash'>('none')
  const archivedSessions = $derived($sessions.filter(s => $collapsedSessions.includes(s.id)))

  async function unarchiveSession(id: string) {
    const before = get(collapsedSessions)
    collapsedSessions.set(before.filter(x => x !== id))
    try {
      await api.setSessionCollapsed(id, false)
    } catch (e: any) {
      collapsedSessions.set(before)
      showToast(e?.message ?? tr('sidebar.collapse_failed'), 'error')
    }
  }

  async function deleteArchivedSession(id: string) {
    if (!(await confirmDialog(tr('sidebar.confirm_delete')))) return
    try {
      await api.deleteSession(id)
      sessions.update(ss => ss.filter(s => s.id !== id))
      collapsedSessions.update(ids => ids.filter(x => x !== id))
      if (get(activeSessionId) === id) {
        activeSessionId.set(null)
        clearPendingSessionOpts()
        view.set('chat')
      }
    } catch (e: any) {
      showToast(e.message, 'error')
    }
  }

  // Managed-tunnel pairing material (null until fetched; .enabled false when
  // the server was not started with --tunnel).
  let tunnelPairing = $state<api.TunnelPairing | null>(null)

  // ── Agent defaults — each control saves immediately on change (see the
  // save* functions below), no separate Save button.
  let reasoningEffort  = $state('off')
  let permissionMode   = $state('interactive')
  let showReasoningVal = $state(true)
  let coauthorVal      = $state(true)
  let workspaceDir        = $state('')
  // Resolved default new sessions get when workspaceDir is empty (~/Octo,
  // expanded server-side) — shown as the input's placeholder instead of a
  // bare "auto" that's easy to mistake for an actually-saved value.
  let workspaceDirDefault = $state('')

  const langOptions = [
    { value: 'en', label: 'English' },
    { value: 'zh', label: '简体中文' },
  ]

  const categories: { key: typeof cat, icon: string, label: string }[] = [
    { key: 'general',   icon: 'ant-design:sliders-outlined',       label: 'settings.general' },
    { key: 'endpoints', icon: 'ant-design:api-outlined',           label: 'settings.endpoints.title' },
    { key: 'agent',     icon: 'ant-design:robot-outlined',         label: 'settings.agent' },
    { key: 'mobile',    icon: 'ant-design:mobile-outlined',        label: 'settings.mobile' },
    { key: 'data',      icon: 'ant-design:database-outlined',       label: 'settings.data' },
    { key: 'about',     icon: 'ant-design:info-circle-outlined',   label: 'settings.about' },
  ]

  // Re-seed on every open, same as the other global modals — reflects
  // whatever config was saved elsewhere (agent chat, another window) since
  // the modal last closed.
  $effect(() => {
    if ($settingsModalOpen) {
      cat = 'general'
      dataSubView = 'none'
      loadConfig()
      loadVersion()
      if (get(nativeShell)) api.getAutostart().then(v => (autostart = v)).catch(() => {})
      api.getTunnelPairing().then(p => { tunnelPairing = p }).catch(() => {})
      theme = modeToThemeLabel[getMode()] ?? 'Light'
      fontSize = storedFontSize()
      modalEl?.focus()
    }
  })

  async function loadConfig() {
    loading = true
    try {
      const cfg = await api.getConfig() as any
      // Absent = old server dropping "" through omitempty = reasoning off.
      reasoningEffort  = cfg.reasoning_effort ?? 'off'
      permissionMode   = cfg.permission_mode ?? 'interactive'
      showReasoningVal = cfg.show_reasoning ?? true
      coauthorVal      = cfg.coauthor ?? true
      // Legacy installer-seeded "auto" resolves to the same default as ""
      // (see tools.ResolveWorkspaceDir) — show it as the empty input with
      // the resolved-default placeholder, not as a literal "auto" the user
      // would mistake for a real path.
      const wd = cfg.workspace_dir ?? ''
      workspaceDir        = wd.trim().toLowerCase() === 'auto' ? '' : wd
      workspaceDirDefault = cfg.workspace_dir_default ?? ''
      if (cfg.language) language = cfg.language
      setLocale(cfg.language === 'zh' || cfg.language === 'zh-TW' ? 'zh' : 'en')
    } catch (e: any) {
      showToast(`Failed to load config: ${e.message}`, 'error')
    } finally {
      loading = false
    }
  }

  async function loadVersion() {
    try {
      const v = await api.getVersion() as any
      versionStr = v.current ?? v.version ?? ''
      latestStr = v.latest ?? ''
      updateAvail = !!v.needs_update
      downloadUrl = v.download_url ?? ''
      upgradeMode = v.upgrade_mode === 'installer' ? 'installer' : 'cli'
    } catch { /* non-critical */ }
  }

  async function checkUpdate() {
    if (checkingUpdate) return
    checkingUpdate = true
    try {
      await loadVersion()
      if (!updateAvail) {
        showToast($t('settings.update.uptodate'), 'success')
        return
      }
      if (upgradeMode === 'installer') {
        if (downloadUrl) {
          try { await api.openExternal(downloadUrl) }
          catch { window.open(downloadUrl, '_blank', 'noopener') }
        }
      } else {
        // Same endpoint VersionBadge's badge uses (POST /api/version/upgrade)
        // — fire it here too and let the badge (always mounted) show live
        // progress via the WS broadcasts it already listens for. Close this
        // modal so the badge's progress popover isn't hidden behind it (#2120).
        // 409 = an upgrade is already in flight; its broadcasts drive the
        // badge, so treat it as started. Any other failure sends no
        // broadcasts — keep the modal open instead of claiming success.
        const res = await fetch('/api/version/upgrade', { method: 'POST' })
        if (res.ok || res.status === 409) {
          showToast($t('settings.update.started'), 'success')
          settingsModalOpen.set(false)
        }
      }
    } finally {
      checkingUpdate = false
    }
  }

  async function copyPairURL() {
    if (!tunnelPairing?.pair_url) return
    try {
      await navigator.clipboard.writeText(tunnelPairing.pair_url)
      showToast($t('settings.mobile.copied'), 'success')
    } catch {
      showToast('Copy failed', 'error')
    }
  }

  async function toggleAutostart(v: boolean) {
    try {
      await api.setAutostart(v)
      autostart = v
    } catch (e: any) {
      showToast(e.message ?? 'Failed to change autostart', 'error')
      autostart = !v
    }
  }

  // ── agent defaults — each saves immediately via its own PUT endpoint
  // (internal/skills/defaults/config-setup/SKILL.md documents the same 5
  // endpoints for the conversational path; this is the identical contract,
  // just called directly instead of through the agent).
  async function saveReasoningEffort(v: string) {
    try {
      await api.updateReasoningEffort(v)
      reasoningEffort = v
    } catch (e: any) {
      showToast(e.message ?? 'Failed to update reasoning effort', 'error')
    }
  }

  async function savePermissionMode(v: string) {
    try {
      await api.updatePermissionMode(v)
      permissionMode = v
    } catch (e: any) {
      showToast(e.message ?? 'Failed to update permission mode', 'error')
    }
  }

  async function saveShowReasoning(v: boolean) {
    try {
      await api.updateShowReasoning(v)
      showReasoningVal = v
    } catch (e: any) {
      showToast(e.message ?? 'Failed to update show-reasoning', 'error')
    }
  }

  async function saveCoauthor(v: boolean) {
    try {
      await api.updateCoauthor(v)
      coauthorVal = v
    } catch (e: any) {
      showToast(e.message ?? 'Failed to update coauthor', 'error')
    }
  }

  async function saveWorkspaceDir(v: string) {
    try {
      await api.updateWorkspaceDir(v)
      workspaceDir = v
    } catch (e: any) {
      showToast(e.message ?? 'Failed to update workspace directory', 'error')
    }
  }

  // Persist the language like every other config-backed field in this modal.
  // The $effect below already applies it to the live locale store; without
  // this PUT the choice only lived in memory and a refresh reverted to the
  // server's stored language (#2076). FirstRunSetup persists through the same
  // endpoint.
  async function saveLanguage(v: string) {
    try {
      await api.updateLanguage(v)
    } catch (e: any) {
      showToast(e.message ?? 'Failed to update language', 'error')
    }
  }

  function close() {
    settingsModalOpen.set(false)
  }

  // Re-runs the same blocking first-run panel a fresh install shows (App.svelte
  // gates <FirstRunSetup /> on this store) — lets a configured user redo the
  // model/browser setup + personalization chat without wiping any config first.
  function rerunFirstRun() {
    settingsModalOpen.set(false)
    onboardPhase.set('key_setup')
  }

  $effect(() => {
    const zoom = fontZoomMap[fontSize] ?? '1'
    ;(document.documentElement.style as any).zoom = zoom
    // Viewport units are not compensated for zoom — publish the factor so
    // vh/vw-sized boxes can divide themselves back onto the real viewport.
    document.documentElement.style.setProperty('--font-zoom', zoom)
    localStorage.setItem('octo.fontSize', fontSize)
  })

  const themeLabelToMode: Record<string, ThemeMode> = { Light: 'light', Dark: 'dark', System: 'system' }
  $effect(() => {
    setMode(themeLabelToMode[theme] ?? 'light')
  })

  $effect(() => {
    setLocale(language === 'zh' || language === 'zh-TW' ? 'zh' : 'en')
  })
</script>

{#if $settingsModalOpen}
<div class="backdrop" role="presentation" onclick={close}>
  <div
    class="modal"
    bind:this={modalEl}
    role="dialog"
    aria-modal="true"
    tabindex="-1"
    onclick={(e) => e.stopPropagation()}
    onkeydown={(e) => { if (e.key === 'Escape') { e.preventDefault(); close() } }}
  >
    <div class="modal-header">
      <span class="modal-title">{$t('settings.title')}</span>
      <button class="modal-close" onclick={close} aria-label={$t('common.close')}>
        <iconify-icon icon="ant-design:close-outlined" width="14"></iconify-icon>
      </button>
    </div>

    <div class="modal-body">
      <div class="rail">
        {#each categories as c (c.key)}
          <div class="scat" class:on={cat === c.key} onclick={() => { cat = c.key; dataSubView = 'none' }}>
            <iconify-icon icon={c.icon} width="15"></iconify-icon>
            <span>{$t(c.label)}</span>
          </div>
        {/each}
      </div>

      <div class="pane">
        {#if loading}
          <div class="loading-state">{$t('settings.loading')}</div>
        {:else if cat === 'general'}
          <div class="setrow">
            <div class="seti">
              <span class="setl">{$t('settings.language')}</span>
              <span class="setd">{$t('settings.language_desc')}</span>
            </div>
            <select class="sinput" bind:value={language} onchange={() => saveLanguage(language)}>
              {#each langOptions as o}
                <option value={o.value}>{o.label}</option>
              {/each}
            </select>
          </div>
          <div class="setrow">
            <div class="seti">
              <span class="setl">{$t('settings.font_size')}</span>
              <span class="setd">{$t('settings.font_size_desc')}</span>
            </div>
            <Segment options={['Small', 'Medium', 'Large']} labels={{ Small: $t('settings.fs_small'), Medium: $t('settings.fs_medium'), Large: $t('settings.fs_large') }} bind:value={fontSize} />
          </div>
          <div class="setrow">
            <div class="seti">
              <span class="setl">{$t('settings.theme')}</span>
              <span class="setd">{$t('settings.theme_desc')}</span>
            </div>
            <Segment options={['Light', 'Dark', 'System']} labels={{ Light: $t('settings.theme_light'), Dark: $t('settings.theme_dark'), System: $t('settings.theme_system') }} bind:value={theme} />
          </div>
          {#if $nativeShell}
            <div class="setrow">
              <div class="seti">
                <span class="setl">{$t('settings.autostart')}</span>
                <span class="setd">{$t('settings.autostart_desc')}</span>
              </div>
              <Switch checked={autostart} onchange={(v) => toggleAutostart(v)} />
            </div>
          {/if}
          <div class="setrow">
            <div class="seti">
              <span class="setl">{$t('settings.notifications')}</span>
              <span class="setd">{$t('settings.notifications_desc')}</span>
            </div>
            <Switch checked={$notificationsEnabled} onchange={(v) => setNotificationsEnabled(v)} />
          </div>

        {:else if cat === 'endpoints'}
          <EndpointsSection />

        {:else if cat === 'agent'}
          <div class="setrow">
            <div class="seti">
              <span class="setl">{$t('settings.reasoning')}</span>
              <span class="setd">{$t('settings.reasoning_desc')}</span>
            </div>
            <Segment
              options={['low', 'medium', 'high', 'xhigh', 'max']}
              labels={{
                low: $t('models.reasoning.low'), medium: $t('models.reasoning.medium'), high: $t('models.reasoning.high'),
                xhigh: $t('models.reasoning.xhigh'), max: $t('models.reasoning.max'),
              }}
              value={reasoningEffort}
              onchange={(v) => saveReasoningEffort(v)}
            />
          </div>
          <div class="setrow">
            <div class="seti">
              <span class="setl">{$t('settings.perm_mode')}</span>
              <span class="setd">{$t('settings.perm_mode_desc')}</span>
            </div>
            <Segment
              options={['interactive', 'auto', 'strict']}
              labels={{
                interactive: $t('settings.perm_mode.interactive'), auto: $t('settings.perm_mode.auto'), strict: $t('settings.perm_mode.strict'),
              }}
              value={permissionMode}
              onchange={(v) => savePermissionMode(v)}
            />
          </div>
          <div class="setrow">
            <div class="seti">
              <span class="setl">{$t('settings.show_reasoning')}</span>
              <span class="setd">{$t('settings.show_reasoning_desc')}</span>
            </div>
            <Switch checked={showReasoningVal} onchange={(v) => saveShowReasoning(v)} />
          </div>
          <div class="setrow">
            <div class="seti">
              <span class="setl">{$t('settings.coauthor')}</span>
              <span class="setd">{$t('settings.coauthor_desc')}</span>
            </div>
            <Switch checked={coauthorVal} onchange={(v) => saveCoauthor(v)} />
          </div>
          <div class="setrow">
            <div class="seti">
              <span class="setl">{$t('settings.workspace_dir')}</span>
              <span class="setd">{$t('settings.workspace_dir_desc')}</span>
            </div>
            <input
              class="sinput mono"
              type="text"
              placeholder={workspaceDirDefault}
              value={workspaceDir}
              onchange={(e) => saveWorkspaceDir(e.currentTarget.value)}
            />
          </div>

        {:else if cat === 'mobile'}
          {#if tunnelPairing?.enabled && tunnelPairing.pair_url}
            <div class="mobile-pair">
              <QrCode text={tunnelPairing.pair_url} />
              <div class="mobile-info">
                <p class="mobile-scan">{$t('settings.mobile.scan')}</p>
                <div class="mobile-meta mono">
                  <div>{$t('settings.mobile.relay')}: {tunnelPairing.relay}</div>
                  <div>{$t('settings.mobile.tunnel_id')}: {tunnelPairing.tunnel_id}</div>
                </div>
                <button class="btns" onclick={copyPairURL}>{$t('settings.mobile.copy_url')}</button>
              </div>
            </div>
          {:else}
            <div class="mobile-disabled">{$t('settings.mobile.disabled')}</div>
          {/if}

        {:else if cat === 'data'}
          {#if dataSubView === 'none'}
            <div class="data-row">
              <div class="data-row-main">
                <iconify-icon icon="lucide:archive" width="15"></iconify-icon>
                <span class="setl">{$t('settings.data.archived')}</span>
                <span class="data-count">{archivedSessions.length}</span>
              </div>
              <button class="link-btn" onclick={() => (dataSubView = 'archived')}>{$t('settings.data.manage')}</button>
            </div>
            <div class="data-row">
              <div class="data-row-main">
                <iconify-icon icon="ant-design:delete-outlined" width="15"></iconify-icon>
                <span class="setl">{$t('nav.file_recall')}</span>
              </div>
              <button class="link-btn" onclick={() => (dataSubView = 'trash')}>{$t('settings.data.manage')}</button>
            </div>
          {:else}
            <div class="data-subhead">
              <button class="back-btn" onclick={() => (dataSubView = 'none')}>
                <iconify-icon icon="ant-design:left-outlined" width="14"></iconify-icon>
              </button>
              <span class="data-subtitle">
                {dataSubView === 'archived' ? $t('settings.data.archived') : $t('nav.file_recall')}
              </span>
            </div>
            {#if dataSubView === 'archived'}
              {#if archivedSessions.length === 0}
                <div class="data-empty">{$t('settings.data.archived_empty')}</div>
              {:else}
                <div class="archived-list">
                  {#each archivedSessions as s (s.id)}
                    <div class="archived-row">
                      <div class="archived-info">
                        <span class="archived-name">{(s as any).name || (s as any).title || s.id}</span>
                        <span class="archived-meta mono">{s.id} · {ago((s as any).updated_at, $t)}</span>
                      </div>
                      <div class="archived-actions">
                        <button class="btns danger" onclick={() => deleteArchivedSession(s.id)}>{$t('common.delete')}</button>
                        <button class="btns" onclick={() => unarchiveSession(s.id)}>{$t('sidebar.uncollapse')}</button>
                      </div>
                    </div>
                  {/each}
                </div>
              {/if}
            {:else}
              <FileRecallView embedded />
            {/if}
          {/if}

        {:else if cat === 'about'}
          <div class="card2">
            <div class="setrow">
              <div class="seti">
                <span class="setl">{$t('common.version')}</span>
                <span class="setd">{$t('settings.about.version_desc')}</span>
              </div>
              <span class="setver mono">v{versionStr}</span>
            </div>
            <div class="setrow">
              <div class="seti">
                <span class="setl">{$t('settings.update')}</span>
                <span class="setd">{$t('settings.update_desc')}</span>
              </div>
              <button class="btns" onclick={checkUpdate} disabled={checkingUpdate}>
                {checkingUpdate ? $t('settings.update.checking')
                  : !updateAvail ? $t('settings.update.check')
                  : upgradeMode === 'installer' ? $t('upgrade.btn.download')
                  : $t('upgrade.btn.upgrade')}
              </button>
            </div>
            {#if updateAvail}
              <div class="setrow">
                <div class="seti">
                  <span class="setl">{$t('settings.update_available')} v{latestStr}</span>
                </div>
              </div>
            {/if}
            <div class="setrow">
              <div class="seti">
                <span class="setl">{$t('settings.about.firstrun')}</span>
                <span class="setd">{$t('settings.about.firstrun_desc')}</span>
              </div>
              <button class="btns" onclick={rerunFirstRun}>{$t('settings.about.firstrun_btn')}</button>
            </div>
            <div class="setrow">
              <div class="seti">
                <span class="setl">{$t('settings.about.license')}</span>
                <span class="setd">{$t('settings.about.license_desc')}</span>
              </div>
              <button class="link-btn" onclick={() => openUrl(LICENSE_URL)}>{$t('settings.about.license_view')}</button>
            </div>
          </div>
          <div class="about-footer">
            {$t('settings.about.footer').replace('{tagline}', $t('nav.workbench')).replace('{year}', String(new Date().getFullYear()))}
          </div>
        {/if}
      </div>
    </div>
  </div>
</div>
{/if}

<style>
.backdrop {
  position: fixed; inset: 0; z-index: 1000; background: var(--scrim);
  display: flex; align-items: center; justify-content: center; padding: 24px;
}
.modal {
  /* Fixed height (not max-height) so switching between categories with very
     different content lengths (关于 vs 端点) never resizes the modal itself
     — each pane scrolls internally instead. */
  width: 100%; max-width: 760px; height: calc(78vh / var(--font-zoom));
  background: var(--bg-container); border: 1px solid var(--border); border-radius: var(--radius-card);
  box-shadow: 0 24px 48px rgba(15,23,42,0.18);
  display: flex; flex-direction: column; overflow: hidden;
  animation: octo-fadein 0.16s ease;
}
.modal:focus { outline: none; }
.modal-header {
  display: flex; align-items: center; justify-content: space-between; flex: 0 0 auto;
  padding: 16px 20px; border-bottom: 1px solid var(--border);
}
.modal-title { font-size: 16px; font-weight: 700; letter-spacing: -0.01em; color: var(--text-heading); }
.modal-close {
  width: 28px; height: 28px; border: none; background: transparent; border-radius: 7px;
  display: flex; align-items: center; justify-content: center; cursor: pointer; color: var(--text-secondary);
}
.modal-close:hover { background: var(--hover-neutral); color: var(--text); }
.modal-body { flex: 1; min-height: 0; display: flex; }

/* ── category rail ─────────────────────────────────────────────────────────── */
.rail {
  width: 190px; flex: 0 0 190px; border-right: 1px solid var(--border);
  background: var(--bg-layout); padding: 10px; display: flex; flex-direction: column; gap: 2px;
  overflow-y: auto;
}
.scat {
  display: flex; align-items: center; gap: 9px; padding: 7px 10px;
  border-radius: 7px; cursor: pointer; color: var(--text-tertiary);
}
.scat span { font-size: 13px; color: var(--text-secondary); }
.scat:hover { background: var(--hover-neutral); }
.scat.on { background: var(--active-blue-bg); }
.scat.on span { color: var(--blue-6); font-weight: 600; }
.scat.on { color: var(--blue-6); }

/* ── content pane ──────────────────────────────────────────────────────────── */
.pane { flex: 1; min-width: 0; overflow-y: auto; padding: 20px 24px; display: flex; flex-direction: column; gap: 0; }
.loading-state { padding: 40px; text-align: center; color: var(--text-tertiary); font-size: 14px; }

.setrow {
  display: flex; align-items: center; justify-content: space-between;
  gap: 20px; padding: 14px 2px; border-bottom: 1px solid var(--border-secondary);
}
.setrow:last-child { border-bottom: none; }
/* Rows nested in a card (About) need the card's own inset — the flat rows
   above sit directly in .pane, which already provides that inset itself. */
.card2 .setrow { padding: 16px 20px; }
.seti { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.setl { font-size: 13px; color: var(--text); }
.setd { font-size: 12px; color: var(--text-secondary); margin-top: 2px; line-height: 1.45; max-width: 42ch; }
.setver { font-size: 13px; color: var(--text-tertiary); flex: 0 0 auto; }
.link-btn {
  border: none; background: transparent; color: var(--blue-6); font-size: 13px;
  font-weight: 500; cursor: pointer; font-family: inherit; padding: 0; flex: 0 0 auto;
}
.link-btn:hover { text-decoration: underline; }
.about-footer { padding: 28px 2px 4px; text-align: center; font-size: 12px; color: var(--text-tertiary); }
.sinput {
  width: 220px; flex: 0 0 auto; height: 32px; padding: 0 10px;
  border: 1px solid var(--border); border-radius: 8px; font-size: 13px;
  color: var(--text); font-family: inherit; background: var(--bg-container); outline: none;
}
select.sinput { cursor: pointer; }
.sinput:focus { border-color: var(--blue-6); box-shadow: 0 0 0 2px var(--focus-ring); }

/* ── buttons ───────────────────────────────────────────────────────────────── */
.btns {
  height: 30px; padding: 0 12px; border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 8px; font-size: 13px; font-weight: 500; color: var(--text); cursor: pointer;
  font-family: inherit; display: flex; align-items: center; gap: 6px;
}
.btns:hover:not(:disabled) { background: var(--hover-neutral); border-color: var(--text-quaternary); }
.btns:disabled { opacity: 0.5; cursor: not-allowed; }

/* ── about card ────────────────────────────────────────────────────────────── */
.card2 { background: var(--bg-container); border: 1px solid var(--border); border-radius: var(--radius-card); overflow: hidden; }

/* ── mobile ─────────────────────────────────────────────────────────────────── */
.mobile-pair { display: flex; gap: 20px; padding: 6px 2px; align-items: flex-start; flex-wrap: wrap; }
.mobile-info { display: flex; flex-direction: column; gap: 12px; min-width: 0; flex: 1; }
.mobile-scan { margin: 0; font-size: 14px; color: var(--text); }
.mobile-meta { font-size: 12px; color: var(--text-tertiary); display: flex; flex-direction: column; gap: 4px; word-break: break-all; }
.mobile-meta > div { min-width: 0; }
.mobile-info .btns { align-self: flex-start; }
.mobile-disabled { padding: 28px 16px; text-align: center; font-size: 13px; color: var(--text-tertiary); }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }

/* ── data management ─────────────────────────────────────────────────────── */
.data-row {
  display: flex; align-items: center; justify-content: space-between;
  gap: 16px; padding: 12px 10px; margin: 0 -10px; border-radius: 8px;
}
.data-row:hover { background: var(--hover-neutral); }
.data-row-main { display: flex; align-items: center; gap: 9px; min-width: 0; color: var(--text-tertiary); }
.data-count { font-size: 12px; color: var(--text-tertiary); }
.data-subhead { display: flex; align-items: center; gap: 8px; padding: 2px 0 14px; }
.back-btn {
  width: 26px; height: 26px; border: none; background: transparent; border-radius: 7px;
  display: flex; align-items: center; justify-content: center; cursor: pointer; color: var(--text-secondary);
}
.back-btn:hover { background: var(--hover-neutral); color: var(--text); }
.data-subtitle { font-size: 14px; font-weight: 600; color: var(--text-heading); }
.data-empty { padding: 32px 4px; text-align: center; font-size: 13px; color: var(--text-tertiary); }
.archived-list { display: flex; flex-direction: column; }
.archived-row {
  display: flex; align-items: center; justify-content: space-between;
  gap: 16px; padding: 12px 2px; border-bottom: 1px solid var(--border-secondary);
}
.archived-row:last-child { border-bottom: none; }
.archived-info { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.archived-name { font-size: 13px; color: var(--text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.archived-meta { font-size: 11px; color: var(--text-tertiary); }
.archived-actions { display: flex; align-items: center; gap: 8px; flex: 0 0 auto; }
.btns.danger { color: var(--error); }
.btns.danger:hover:not(:disabled) { background: var(--error-bg); border-color: var(--error-border); }
</style>
