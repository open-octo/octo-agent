<script lang="ts">
  // An image that may live on the server's API. data:/blob: sources render
  // as-is; an /api path is fetched (the shim attaches the key) and shown from a
  // blob — an <img src> would carry no key (same reason as PageViewer).
  import { onDestroy } from 'svelte'

  let { src, alt = '' }: { src: string; alt?: string } = $props()

  let blobURL = $state('')
  const direct = $derived(!src.startsWith('/'))

  $effect(() => {
    const path = src
    if (!path.startsWith('/')) return
    let cancelled = false
    fetch(path)
      .then((r) => (r.ok ? r.blob() : Promise.reject(new Error(String(r.status)))))
      .then((b) => {
        if (cancelled) return
        if (blobURL) URL.revokeObjectURL(blobURL)
        blobURL = URL.createObjectURL(b)
      })
      .catch(() => {})
    return () => { cancelled = true }
  })
  onDestroy(() => { if (blobURL) URL.revokeObjectURL(blobURL) })
</script>

{#if direct || blobURL}
  <img src={direct ? src : blobURL} {alt} />
{:else}
  <span class="ph"></span>
{/if}

<style>
  img, .ph {
    display: block;
    width: 100%;
    height: 100%;
    object-fit: cover;
  }
  .ph { background: var(--m-surface-2); }
</style>
