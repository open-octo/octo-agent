<script lang="ts">
  // Full-screen viewer for one page: a session artifact or a Light App. Three
  // shapes, matching the desktop artifact frame:
  //   - src:    a page on the server (/_artifacts/<token>/), framed by URL;
  //   - srcdoc: a Markdown preview document, framed inline;
  //   - image:  an image path on the server's API, fetched (the shim attaches
  //             the key) and shown from a blob — an <img src> would carry no key.
  import { onDestroy } from 'svelte'
  import { ARTIFACT_SANDBOX, ARTIFACT_ORIGIN_SANDBOX } from '../lib/artifacts'
  import { t } from '../lib/i18n'

  let { title, src = '', srcdoc = '', image = '', loading = false, failed = false, onClose }: {
    title: string
    src?: string
    srcdoc?: string
    image?: string
    loading?: boolean
    failed?: boolean
    onClose: () => void
  } = $props()

  let frame: HTMLIFrameElement | undefined = $state()
  let blobURL = $state('')
  let imageFailed = $state(false)

  $effect(() => {
    const path = image
    if (!path) return
    let cancelled = false
    imageFailed = false
    fetch(path)
      .then((r) => (r.ok ? r.blob() : Promise.reject(new Error(String(r.status)))))
      .then((b) => {
        if (cancelled) return
        if (blobURL) URL.revokeObjectURL(blobURL)
        blobURL = URL.createObjectURL(b)
      })
      .catch(() => { if (!cancelled) imageFailed = true })
    return () => { cancelled = true }
  })
  onDestroy(() => { if (blobURL) URL.revokeObjectURL(blobURL) })

  function reload() {
    if (frame && src) frame.src = src
  }
</script>

<div class="viewer">
  <header class="vhead">
    <button class="back" onclick={onClose} aria-label={$t('m.back')}>
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="var(--m-text)" stroke-width="2"><path d="m15 18-6-6 6-6"/></svg>
    </button>
    <span class="vtitle">{title}</span>
    {#if src}
      <button class="back" onclick={reload} aria-label={$t('m.reload')}>
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="var(--m-text)" stroke-width="2"><path d="M21 12a9 9 0 1 1-2.6-6.4M21 4v5h-5"/></svg>
      </button>
    {/if}
  </header>
  <div class="vbody">
    {#if failed || imageFailed}
      <div class="note">{$t('m.page_failed')}</div>
    {:else if loading}
      <div class="note">{$t('m.loading')}</div>
    {:else if src}
      <iframe bind:this={frame} {src} sandbox={ARTIFACT_ORIGIN_SANDBOX} allow="fullscreen; clipboard-write" {title}></iframe>
    {:else if srcdoc}
      <iframe {srcdoc} sandbox={ARTIFACT_SANDBOX} allow="fullscreen; clipboard-write" {title}></iframe>
    {:else if blobURL}
      <div class="imgwrap"><img src={blobURL} alt={title} /></div>
    {:else if image}
      <div class="note">{$t('m.loading')}</div>
    {/if}
  </div>
</div>

<style>
  .viewer {
    position: fixed;
    inset: 0;
    z-index: 50;
    display: flex;
    flex-direction: column;
    background: var(--m-bg);
    padding-top: env(safe-area-inset-top);
    padding-bottom: env(safe-area-inset-bottom);
  }
  .vhead {
    flex: none;
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 6px 14px 10px;
  }
  .back {
    flex: none;
    width: 34px;
    height: 34px;
    border-radius: 50%;
    border: none;
    background: var(--m-surface);
    display: flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;
    box-shadow: var(--m-shadow-card);
  }
  .vtitle {
    flex: 1;
    min-width: 0;
    font-size: 15px;
    font-weight: 600;
    color: var(--m-text);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .vbody { flex: 1; min-height: 0; position: relative; background: var(--m-surface); }
  iframe { border: 0; width: 100%; height: 100%; display: block; background: #fff; }
  /* margin:auto rather than align-items:center: a tall image centred by the
     flexbox overflows upward, out of reach of the scroll. */
  .imgwrap { width: 100%; height: 100%; overflow: auto; display: flex; }
  .imgwrap img { max-width: 100%; height: auto; display: block; margin: auto; }
  .note { padding: 40px 16px; text-align: center; font-size: 13px; color: var(--m-text-3); }
</style>
