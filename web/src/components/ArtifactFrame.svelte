<script lang="ts">
  // The preview frame for a text artifact, in its three shapes:
  //
  //   - an HTML artifact with a grant renders by src from the artifact origin
  //     (lib/artifacts.ts header, internal/server/artifact_origin.go);
  //   - an HTML artifact this browser cannot reach that origin from shows the
  //     local-only notice — the code view beside it still works;
  //   - a Markdown preview renders by srcdoc, as before.
  import type { Artifact } from '../lib/types'
  import { t } from '../lib/i18n'
  import { ARTIFACT_SANDBOX, ARTIFACT_ORIGIN_SANDBOX, probeArtifactOrigin, themeRev } from '../lib/artifacts'
  import { registerArtifactFrame, unregisterLaIframe } from '../lib/laStorage'
  import { activeSessionId } from '../lib/stores'

  let { artifact }: { artifact: Artifact } = $props()

  // The origin frame's bridge lets the page push state and receive deliveries
  // — but only if the host registers it: the router serves registered windows
  // only, and the (session, path) identity comes from here, never from the
  // page. Unregistering also drops the page from the mirror the model reads.
  let frameEl = $state<HTMLIFrameElement | null>(null)
  $effect(() => {
    const el = frameEl
    const sid = $activeSessionId
    if (!el || !sid) return
    registerArtifactFrame(el.contentWindow, sid, artifact.path)
    return () => unregisterLaIframe(el.contentWindow)
  })

  // The banner the origin bakes into a gated page carries theme colours, and
  // the origin has no other way to learn the app's theme than its URL. A
  // theme switch changes src, which reloads the frame — the same thing the
  // Markdown path does by dropping its preview and rebuilding.
  const theme = $derived.by(() => {
    void $themeRev
    return document.documentElement.getAttribute('data-theme') === 'dark' ? 'dark' : 'light'
  })
  // `v` is the observation count: a re-write of the same path yields a
  // different URL, so the frame reloads even though the origin did not change.
  const src = $derived(artifact.originURL ? `${artifact.originURL}?theme=${theme}&v=${artifact.rev ?? 0}` : '')

  // Only the browser knows whether the origin's hostname resolves here; the
  // probe (once per URL) flips the entry to the local-only notice when it
  // does not. See probeArtifactOrigin.
  $effect(() => {
    probeArtifactOrigin(artifact)
  })
</script>

{#if artifact.originURL}
  <iframe bind:this={frameEl} {src} sandbox={ARTIFACT_ORIGIN_SANDBOX} allow="fullscreen; clipboard-write" title={artifact.name}></iframe>
{:else if artifact.originUnavailable}
  <div class="local-only">
    <iconify-icon icon="ant-design:desktop-outlined" width="28"></iconify-icon>
    <span>{$t('artifacts.local_only')}</span>
  </div>
{:else}
  <iframe srcdoc={artifact.preview} sandbox={ARTIFACT_SANDBOX} allow="fullscreen; clipboard-write" title={artifact.name}></iframe>
{/if}

<style>
iframe { border: 0; width: 100%; height: 100%; display: block; }
.local-only {
  width: 100%; height: 100%; box-sizing: border-box; padding: 16px;
  display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 10px;
  text-align: center; color: var(--text-tertiary); font-size: 13px; background: var(--bg-layout);
}
.local-only span { max-width: 85%; }
</style>
