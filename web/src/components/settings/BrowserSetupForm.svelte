<script lang="ts">
  import { onMount } from 'svelte'
  import { t, tr } from '../../lib/i18n'
  import * as api from '../../lib/api'
  import type { BrowserVerifyResult } from '../../lib/api'

  // Shared browser-automation setup body: the chrome://inspect instructions, a
  // port field, and a Verify button that probes CDP (and persists connect_port
  // on success). Used by both the Settings modal and the first-run wizard step.
  // The parent supplies the secondary action (Cancel / Skip) and the success
  // handler, so the same verify flow drives both contexts.
  // secondaryLabel/onSecondary are optional: the modal and wizard supply a
  // Cancel/Skip action, but the standalone Browser view embeds this inline with
  // no secondary action.
  let { secondaryLabel, onSecondary, onVerified }:
    {
      secondaryLabel?: string
      onSecondary?: () => void
      onVerified: (res: BrowserVerifyResult) => void | Promise<void>
    } = $props()

  // The chrome://inspect "Allow remote debugging" toggle always serves CDP on
  // 127.0.0.1:9222, so the port is fixed — letting users type one just invites a
  // typo that can't connect. Advanced custom-port setups use config.yml.
  const port = 9222
  let chromeAvailable = $state(true)
  let verifying = $state(false)
  let error = $state('')
  let errorDetail = $state('')

  onMount(async () => {
    try {
      const st = await api.getBrowserStatus()
      chromeAvailable = st.chrome_available
    } catch { /* defaults are fine */ }
  })

  async function runVerify() {
    verifying = true
    error = ''
    errorDetail = ''
    try {
      const res = await api.verifyBrowser(port)
      if (res.ok) {
        await onVerified(res)
      } else {
        error = tr('settings.browser.verify_fail')
        errorDetail = res.detail ?? ''
      }
    } catch (e: any) {
      error = tr('settings.browser.verify_fail')
      errorDetail = e?.message ?? ''
    } finally {
      verifying = false
    }
  }
</script>

<p class="bs-intro">{$t('settings.browser.modal_intro')}</p>
<ol class="bs-steps">
  <li>{$t('settings.browser.step_open')} <code>chrome://inspect/#remote-debugging</code></li>
  <li><span class="bs-step-highlight">{$t('settings.browser.step_toggle')}</span></li>
  <li>{$t('settings.browser.step_verify')}</li>
  <li>{$t('settings.browser.step_approve')}</li>
</ol>
{#if !chromeAvailable}
  <div class="bs-note">
    <iconify-icon icon="ant-design:warning-outlined" width="14"></iconify-icon>
    {$t('settings.browser.no_chrome')}
  </div>
{/if}
{#if verifying}
  <div class="bs-hint">
    <iconify-icon icon="ant-design:loading-outlined" width="14"></iconify-icon>
    {$t('settings.browser.verify_hint')}
  </div>
{/if}
{#if error}
  <div class="bs-error">
    <div>{error}</div>
    {#if errorDetail}<div class="bs-error-detail">{errorDetail}</div>{/if}
  </div>
{/if}
<div class="bs-actions">
  {#if onSecondary}
    <button class="btn-ghost" onclick={onSecondary} disabled={verifying}>{secondaryLabel}</button>
  {/if}
  <button class="btn-primary" onclick={runVerify} disabled={verifying}>
    {verifying ? $t('settings.browser.verifying') : $t('settings.browser.verify')}
  </button>
</div>

<style>
.bs-intro { margin: 0 0 12px; font-size: 13px; color: var(--text-secondary); line-height: 1.5; }
.bs-steps { margin: 0 0 16px; padding-left: 20px; display: flex; flex-direction: column; gap: 6px; font-size: 13px; color: var(--text); line-height: 1.5; }
.bs-steps code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12px; background: var(--bg-table-header); padding: 1px 5px; border-radius: 4px; }
.bs-note { display: flex; align-items: center; gap: 8px; margin: 0 0 16px; padding: 10px 12px; border-radius: 8px; font-size: 12px; color: var(--warning); background: var(--warning-bg); }
.bs-step-highlight { display: inline-block; font-weight: 600; color: var(--blue-6); background: var(--blue-1); padding: 2px 6px; border-radius: 4px; }
.bs-hint { margin-top: 12px; font-size: 12px; color: var(--blue-6); display: flex; align-items: center; gap: 6px; }
.bs-error { margin-top: 12px; font-size: 12px; color: var(--error); }
.bs-error-detail { margin-top: 2px; font-size: 11px; color: var(--text-secondary); }
.bs-actions { display: flex; justify-content: flex-end; gap: 10px; margin-top: 20px; }
.btn-ghost {
  height: 32px; padding: 0 16px; border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 6px; font-size: 14px; color: var(--text-secondary); cursor: pointer; font-family: inherit;
}
.btn-ghost:hover:not(:disabled) { border-color: var(--blue-5); color: var(--blue-5); }
.btn-ghost:disabled { opacity: 0.5; cursor: not-allowed; }
.btn-primary { height: 32px; padding: 0 16px; border: none; background: var(--blue-6); border-radius: 6px; font-size: 14px; color: #fff; cursor: pointer; font-family: inherit; }
.btn-primary:hover:not(:disabled) { background: var(--blue-5); }
.btn-primary:disabled { opacity: 0.5; cursor: not-allowed; }
</style>
