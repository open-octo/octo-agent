<script lang="ts">
  // The only node carrying a URL. The href was already checked against
  // markdown.ts's isSafeHref by the guard, so nothing here re-validates it —
  // but the attributes below still matter: rel="noopener noreferrer" keeps the
  // opened page from reaching back through window.opener, and target="_blank"
  // keeps the chat session from being navigated away from.
  //
  // Rendered through ordinary Svelte interpolation, so both the text and the
  // href are escaped by the framework rather than by us.
  import type { GenuiLinkNode } from '../../lib/genui/types'

  let { node }: { node: GenuiLinkNode } = $props()
</script>

<a class="genui-link" href={node.href} target="_blank" rel="noopener noreferrer">
  {node.text}
  <iconify-icon icon="ant-design:export-outlined" width="11"></iconify-icon>
</a>

<style>
  .genui-link {
    display: inline-flex;
    align-items: center;
    gap: 3px;
    color: var(--blue-6);
    text-decoration: none;
    font-size: 13px;
  }
  .genui-link:hover {
    text-decoration: underline;
  }
</style>
