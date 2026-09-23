<script lang="ts">
  // The preview frame for a text artifact, in its three shapes:
  //
  //   - an HTML artifact with a grant renders by src from its page URL
  //     (lib/artifacts.ts header, internal/server/artifact_pages.go);
  //   - an HTML artifact whose grant failed says the preview could not load —
  //     the code view beside it still works;
  //   - a Markdown preview renders by srcdoc, as before.
  import type { Artifact } from '../lib/types'
  import { t } from '../lib/i18n'
  import { ARTIFACT_SANDBOX, ARTIFACT_ORIGIN_SANDBOX, themeRev } from '../lib/artifacts'

  let { artifact }: { artifact: Artifact } = $props()

  // The page reads the resolved theme off its URL (the artifact-design
  // skill's contract). A theme switch changes src, which reloads the frame —
  // the same thing the Markdown path does by dropping its preview and
  // rebuilding.
  const theme = $derived.by(() => {
    void $themeRev
    return document.documentElement.getAttribute('data-theme') === 'dark' ? 'dark' : 'light'
  })
  // `v` is the observation count: a re-write of the same path yields a
  // different URL, so the frame reloads even though the token did not change.
  const src = $derived(artifact.originURL ? `${artifact.originURL}?theme=${theme}&v=${artifact.rev ?? 0}` : '')

</script>

{#if artifact.originURL}
  <iframe {src} sandbox={ARTIFACT_ORIGIN_SANDBOX} allow="fullscreen; clipboard-write" title={artifact.name}></iframe>
{:else if artifact.originUnavailable}
  <div class="local-only">
    <iconify-icon icon="ant-design:desktop-outlined" width="28"></iconify-icon>
    <span>{$t('artifacts.preview_unavailable')}</span>
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
