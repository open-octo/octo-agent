<script lang="ts">
  // Recursively dispatches a single GenUI node to its leaf component by
  // node.type, via a plain if/else-if chain over a small, closed,
  // whitelisted set — no dynamic-registry indirection, since octo-agent has
  // no third-party-registered-component extensibility requirement in scope
  // (unlike dsh-genui's render-node.tsx, which supports one). Every
  // text-bearing field renders through ordinary Svelte interpolation in the
  // leaf components below, never {@html} — GenUI content never touches the
  // markdown/DOMPurify path at all.
  import type { GenuiNode as GenuiNodeType } from '../../lib/genui/types'
  // Self-import for recursion (row/col/card children render via this same
  // dispatcher) — Svelte components don't see their own name inside their
  // own file without one.
  import GenuiNode from './GenuiNode.svelte'
  import GenuiText from './GenuiText.svelte'
  import GenuiRow from './GenuiRow.svelte'
  import GenuiCard from './GenuiCard.svelte'
  import GenuiList from './GenuiList.svelte'
  import GenuiTable from './GenuiTable.svelte'
  import GenuiKeyValue from './GenuiKeyValue.svelte'
  import GenuiStat from './GenuiStat.svelte'
  import GenuiBadge from './GenuiBadge.svelte'
  import GenuiProgress from './GenuiProgress.svelte'
  import GenuiCallout from './GenuiCallout.svelte'

  let { node }: { node: GenuiNodeType } = $props()
</script>

{#if node.type === 'text'}
  <GenuiText {node} />
{:else if node.type === 'row' || node.type === 'col'}
  <GenuiRow {node}>
    {#each node.children as child, i (i)}
      <GenuiNode node={child} />
    {/each}
  </GenuiRow>
{:else if node.type === 'card'}
  <GenuiCard {node}>
    {#each node.children as child, i (i)}
      <GenuiNode node={child} />
    {/each}
  </GenuiCard>
{:else if node.type === 'list'}
  <GenuiList {node} />
{:else if node.type === 'table'}
  <GenuiTable {node} />
{:else if node.type === 'keyvalue'}
  <GenuiKeyValue {node} />
{:else if node.type === 'stat'}
  <GenuiStat {node} />
{:else if node.type === 'badge'}
  <GenuiBadge {node} />
{:else if node.type === 'progress'}
  <GenuiProgress {node} />
{:else if node.type === 'callout'}
  <GenuiCallout {node} />
{/if}
