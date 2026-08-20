<script lang="ts">
  // A code excerpt, highlighted through the same highlight.js core build
  // markdown.ts already imports — no new dependency, and no new language
  // registrations either: a `lang` outside the registered set renders as
  // plain monospaced text, the same degradation an unknown fence language
  // already gets in markdown.
  //
  // The highlighted markup is inserted with {@html}, which GenUI otherwise
  // never does (see GenuiNode.svelte). It is safe here for a narrower reason
  // than mermaid's: the string handed to {@html} is produced by highlight.js
  // from `node.code`, and highlight.js escapes the source text it wraps —
  // the input never reaches the DOM as markup. On the unregistered-language
  // path nothing is generated at all and the text renders through ordinary
  // interpolation.
  import hljs from '../../lib/highlight'
  import type { GenuiCodeNode } from '../../lib/genui/types'

  let { node }: { node: GenuiCodeNode } = $props()

  const highlighted = $derived.by(() => {
    const lang = node.lang
    if (!lang || !hljs.getLanguage(lang)) return null
    try {
      return hljs.highlight(node.code, { language: lang, ignoreIllegals: true }).value
    } catch {
      return null
    }
  })
</script>

<pre class="genui-code"><code>{#if highlighted !== null}{@html highlighted}{:else}{node.code}{/if}</code></pre>

<style>
  .genui-code {
    margin: 0;
    padding: 8px 10px;
    overflow-x: auto;
    font-size: 12px;
    line-height: 1.55;
    background: var(--bg-secondary, var(--bg));
    border: 1px solid var(--border);
    border-radius: 6px;
  }
  .genui-code code {
    font-family: var(--font-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
    white-space: pre;
  }
</style>
