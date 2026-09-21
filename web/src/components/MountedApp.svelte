<script lang="ts">
  // A Light App that claimed a full page in its manifest (`mount: "view"`),
  // rendered as its own origin's frame — the same frame the Light Apps panel
  // uses (ArtifactsPanel), under the same sandbox.
  //
  // The nav only offers a mounted entry where the app can actually load
  // (stores.mountedViews is empty otherwise), so this component does not carry
  // the panel's local-only notice: reaching it already means the origin
  // resolves. It still covers the case of a slug the list no longer has, which
  // is what a deleted app leaves behind in a bookmarked hash.
  import { lightapps, lightappURL, view } from '../lib/stores'
  import { ARTIFACT_ORIGIN_SANDBOX, themeRev } from '../lib/artifacts'
  import { registerLaIframe, unregisterLaIframe } from '../lib/laStorage'
  import { t } from '../lib/i18n'

  let { slug }: { slug: string } = $props()

  const app = $derived($lightapps.find((a) => a.slug === slug))
  // The app reads the resolved theme off its URL, so a theme switch has to
  // rebuild the src — the same thing the artifact frame does.
  const src = $derived.by(() => {
    void $themeRev
    return lightappURL(slug)
  })

  // The bridge is per-frame, and this frame is a second home for Light Apps —
  // the panel registers its own (ArtifactsPanel). Without this a mounted app
  // silently loses storage migration, the desktop download path, and the state
  // push the model reads.
  let frameEl = $state<HTMLIFrameElement | null>(null)
  $effect(() => {
    const el = frameEl
    if (!el) return
    registerLaIframe(el.contentWindow, slug)
    return () => unregisterLaIframe(el.contentWindow)
  })
</script>

{#if app}
  <div class="mounted-app">
    <!-- Chrome the desktop shell cannot provide: it has no browser back
         button, and an app opened from a landing shortcut or a bookmarked
         hash owns no nav row — without this bar the page is a dead end with
         no name, no way back and no way to close. -->
    <header class="appbar">
      <button type="button" class="back" onclick={() => view.set('chat')}>
        <iconify-icon icon="ant-design:arrow-left-outlined" width="13"></iconify-icon>
        <span>{$t('lightapps.back')}</span>
      </button>
      <span class="appname">
        {#if app.icon}<span class="appemoji">{app.icon}</span>{/if}
        {app.name || slug}
      </span>
    </header>
    <iframe bind:this={frameEl} {src} sandbox={ARTIFACT_ORIGIN_SANDBOX} allow="fullscreen; clipboard-write" title={app.name || slug}></iframe>
  </div>
{:else if $lightapps.length === 0}
  <!-- Boot lands here when the URL names a mounted app: the installed list is
       still on its way, and an empty list cannot yet say the slug is wrong. -->
  <div class="missing">
    <iconify-icon icon="ant-design:loading-outlined" width="28" class="spin"></iconify-icon>
    <span>{$t('common.loading')}</span>
  </div>
{:else}
  <div class="missing">
    <iconify-icon icon="ant-design:appstore-outlined" width="28"></iconify-icon>
    <span>{$t('lightapps.empty')}</span>
  </div>
{/if}

<style>
.mounted-app { width: 100%; height: 100%; display: flex; flex-direction: column; }
.appbar {
  display: flex; align-items: center; gap: 10px;
  height: 36px; flex: none; box-sizing: border-box; padding: 0 10px;
  border-bottom: 1px solid var(--border); background: var(--bg-container);
}
.back {
  display: flex; align-items: center; gap: 4px;
  padding: 4px 8px; border: 0; border-radius: var(--radius-sm);
  background: transparent; color: var(--text-secondary);
  font-family: inherit; font-size: 12px; cursor: pointer;
}
.back:hover { background: var(--hover-neutral); color: var(--text); }
.appname {
  display: flex; align-items: center; gap: 6px;
  font-size: 12px; font-weight: 500; color: var(--text);
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.appemoji { font-size: 13px; line-height: 1; }
iframe {
  border: 0;
  width: 100%;
  flex: 1;
  min-height: 0;
  display: block;
  background: var(--bg-container);
}
.spin { animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .spin { animation: none; } }
.missing {
  width: 100%; height: 100%; box-sizing: border-box; padding: 16px;
  display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 10px;
  text-align: center; color: var(--text-tertiary); font-size: 13px; background: var(--bg-layout);
}
</style>
