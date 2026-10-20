<script lang="ts">
  import { onMount } from 'svelte'
  import * as api from '../../lib/api'
  import { t } from '../../lib/i18n'
  import { showToast } from '../../lib/stores'
  import QrCode from '../ui/QrCode.svelte'

  // The mobile app connects straight to this server. The QR is the same
  // bootstrap link `octo serve` prints (<address>/?access_key=…): the app takes
  // the address and key from it. Which address a phone can reach is the user's
  // network, not something the server can know — it lists its LAN addresses,
  // and the user can type any other (a Tailscale name, a cloudflared URL).

  const CUSTOM_KEY = 'octo:mobile-address'

  let info = $state<api.MobileConnectInfo | null>(null)
  let selected = $state('')
  let custom = $state('')

  onMount(async () => {
    try {
      custom = localStorage.getItem(CUSTOM_KEY) ?? ''
    } catch {
      // ignore
    }
    try {
      info = await api.getMobileConnect()
    } catch (e: any) {
      showToast(e.message ?? 'Failed to load', 'error')
      return
    }
    // A page already reached over the network is reached at an address that
    // works from elsewhere — the best default.
    const here = location.hostname
    const remote = here !== 'localhost' && here !== '127.0.0.1' && here !== '[::1]'
    selected = custom ? 'custom' : remote ? location.origin : (info.urls[0] ?? 'custom')
  })

  function normalize(addr: string): string {
    const a = addr.trim().replace(/\/+$/, '')
    if (!a) return ''
    return /^https?:\/\//i.test(a) ? a : `http://${a}`
  }

  const base = $derived(selected === 'custom' ? normalize(custom) : selected)
  const link = $derived(info && base ? `${base}/?access_key=${encodeURIComponent(info.access_key)}` : '')

  // The current page's own origin is offered when it is not already listed.
  const options = $derived.by(() => {
    if (!info) return []
    const list = [...info.urls]
    const here = location.hostname
    if (here !== 'localhost' && here !== '127.0.0.1' && here !== '[::1]' && !list.includes(location.origin)) {
      list.unshift(location.origin)
    }
    return list
  })

  function saveCustom() {
    try {
      localStorage.setItem(CUSTOM_KEY, custom.trim())
    } catch {
      // ignore
    }
  }

  async function copyLink() {
    if (!link) return
    try {
      await navigator.clipboard.writeText(link)
      showToast($t('settings.mobile.copied'), 'success')
    } catch {
      showToast('Copy failed', 'error')
    }
  }
</script>

<div class="mobile">
  {#if info}
    {#if info.loopback_only}
      <p class="note">{$t('settings.mobile.loopback_only')}</p>
    {/if}

    <div class="addrs">
      <span class="lbl">{$t('settings.mobile.address')}</span>
      {#each options as u (u)}
        <label class="opt">
          <input type="radio" name="mobile-addr" value={u} bind:group={selected} />
          <span class="mono">{u}</span>
        </label>
      {/each}
      <label class="opt">
        <input type="radio" name="mobile-addr" value="custom" bind:group={selected} />
        <input
          class="sinput mono"
          type="text"
          placeholder={$t('settings.mobile.custom_placeholder')}
          bind:value={custom}
          onfocus={() => (selected = 'custom')}
          onchange={saveCustom}
        />
      </label>
    </div>

    {#if link}
      <div class="pair">
        <QrCode text={link} />
        <div class="side">
          <p class="scan">{$t('settings.mobile.scan')}</p>
          <p class="hint">{$t('settings.mobile.key_warning')}</p>
          <button class="btns" onclick={copyLink}>{$t('settings.mobile.copy_url')}</button>
        </div>
      </div>
    {/if}
  {/if}
</div>

<style>
.mobile { display: flex; flex-direction: column; gap: 16px; padding: 6px 2px; }
.note {
  margin: 0; padding: 10px 12px; border-radius: 8px; font-size: 12px; line-height: 1.5;
  color: var(--text-secondary); background: var(--bg-layout); border: 1px solid var(--border-secondary);
}
.addrs { display: flex; flex-direction: column; gap: 8px; }
.lbl { font-size: 13px; font-weight: 500; color: var(--text); }
.opt { display: flex; align-items: center; gap: 8px; font-size: 13px; color: var(--text); cursor: pointer; min-width: 0; }
.opt .mono { word-break: break-all; }
.pair { display: flex; gap: 20px; align-items: flex-start; flex-wrap: wrap; }
.side { display: flex; flex-direction: column; gap: 10px; min-width: 0; flex: 1; }
.scan { margin: 0; font-size: 14px; color: var(--text); }
.hint { margin: 0; font-size: 12px; color: var(--text-tertiary); line-height: 1.5; }
.btns {
  align-self: flex-start;
  padding: 5px 12px; border-radius: 8px; font-size: 12px; cursor: pointer;
  background: var(--bg-container); border: 1px solid var(--border-secondary); color: var(--text);
  white-space: nowrap;
}
.btns:hover { border-color: var(--blue-6); }
.sinput {
  padding: 7px 10px; font-size: 13px; color: var(--text);
  background: var(--bg-layout); border: 1px solid var(--border-secondary); border-radius: 8px;
  flex: 1; min-width: 0;
}
.sinput:focus { outline: none; border-color: var(--blue-6); }
</style>
