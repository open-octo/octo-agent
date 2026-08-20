<script lang="ts">
  // Renders a mermaid diagram.
  //
  // THIS IS THE ONE COMPONENT IN THE GENUI TREE THAT USES {@html}.
  // GenuiNode.svelte states the invariant: GenUI content renders through
  // ordinary Svelte interpolation and never touches an HTML-insertion path.
  // An SVG string cannot be inserted any other way, so the exemption is paid
  // for rather than waved through:
  //
  //   1. mermaid runs with securityLevel: 'strict', which sanitizes the
  //      labels it renders, and startOnLoad: false so nothing ever
  //      auto-executes against the surrounding document.
  //   2. The SVG it produces is then run through this project's own DOMPurify
  //      under an SVG profile before insertion — our policy, not only
  //      mermaid's. Two independent sanitizers on this path is the same
  //      belt-and-braces posture the spec guard already takes by running in
  //      both Go and TypeScript.
  //   3. `code` is capped by the guard like any other string field.
  //
  // mermaid is imported dynamically, mirroring html2canvas in ChatView.svelte:
  // it is by far the heaviest thing the frontend can pull in, and a session
  // that never renders a diagram should never parse it.
  import DOMPurify from 'dompurify'
  import type { GenuiMermaidNode } from '../../lib/genui/types'

  let { node }: { node: GenuiMermaidNode } = $props()

  let svg = $state<string | null>(null)
  let failed = $state(false)

  let seq = 0

  $effect(() => {
    const code = node.code
    let cancelled = false
    svg = null
    failed = false

    void (async () => {
      try {
        const { default: mermaid } = await import('mermaid')
        mermaid.initialize({ startOnLoad: false, securityLevel: 'strict' })
        const { svg: raw } = await mermaid.render(`genui-mermaid-${seq++}`, code)
        if (cancelled) return
        svg = DOMPurify.sanitize(raw, { USE_PROFILES: { svg: true, svgFilters: true } })
      } catch {
        if (!cancelled) failed = true
      }
    })()

    return () => {
      cancelled = true
    }
  })
</script>

{#if failed}
  <!-- A model writing invalid diagram syntax must never blank the panel. -->
  <div class="genui-mermaid-error">diagram could not be rendered</div>
{:else if svg !== null}
  <div class="genui-mermaid">{@html svg}</div>
{:else}
  <div class="genui-mermaid-loading">rendering diagram…</div>
{/if}

<style>
  .genui-mermaid {
    overflow-x: auto;
  }
  .genui-mermaid :global(svg) {
    max-width: 100%;
    height: auto;
  }
  .genui-mermaid-error,
  .genui-mermaid-loading {
    font-size: 12px;
    color: var(--text-secondary);
    padding: 6px 0;
  }
  .genui-mermaid-error {
    color: var(--red-6, #f5222d);
  }
</style>
