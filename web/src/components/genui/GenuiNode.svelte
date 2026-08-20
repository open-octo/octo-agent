<script lang="ts">
  // Recursively dispatches a single GenUI node to its leaf component by
  // node.type, via a plain if/else-if chain over a small, closed,
  // whitelisted set — no dynamic-registry indirection, since octo-agent has
  // no third-party-registered-component extensibility requirement in scope
  // (unlike dsh-genui's render-node.tsx, which supports one). Every
  // text-bearing field renders through ordinary Svelte interpolation in the
  // leaf components below, never {@html} — GenUI content never touches the
  // markdown/DOMPurify path at all.
  //
  // Two components are deliberate exceptions, each documented at its own
  // insertion point: GenuiMermaid (an SVG string cannot be inserted any other
  // way; double-sanitized) and GenuiCode (highlight.js escapes the source it
  // wraps, so the model's text never reaches the DOM as markup).
  //
  // This component also decides whether a node renders at all: a node may
  // carry `visibleWhen`, evaluated here against the panel's live field map so
  // no leaf component has to know conditions exist.
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
  import GenuiButton from './GenuiButton.svelte'
  import GenuiInput from './GenuiInput.svelte'
  import GenuiSelect from './GenuiSelect.svelte'
  import GenuiCheckbox from './GenuiCheckbox.svelte'
  import GenuiSwitch from './GenuiSwitch.svelte'
  import GenuiRadio from './GenuiRadio.svelte'
  import GenuiTabs from './GenuiTabs.svelte'
  import GenuiSlider from './GenuiSlider.svelte'
  import GenuiNumber from './GenuiNumber.svelte'
  import GenuiTextarea from './GenuiTextarea.svelte'
  import GenuiQuiz from './GenuiQuiz.svelte'
  import GenuiCollapsible from './GenuiCollapsible.svelte'
  import GenuiCode from './GenuiCode.svelte'
  import GenuiDivider from './GenuiDivider.svelte'
  import GenuiPlot from './GenuiPlot.svelte'
  import GenuiMermaid from './GenuiMermaid.svelte'
  import { useGenuiFieldContext } from '../../lib/genui/context'
  import { evaluateCondition } from '../../lib/genui/condition'

  // `path` identifies this node's position in the tree ("0.2.1"). Components
  // holding UI state that is not a model-declared field — a tab index, a fold
  // state — key their reserved storage entry by it.
  let { node, path = '' }: { node: GenuiNodeType; path?: string } = $props()

  const ctx = useGenuiFieldContext()
  const visible = $derived(evaluateCondition(node.visibleWhen, ctx?.fields ?? {}))
</script>

{#if visible}
  {#if node.type === 'text'}
    <GenuiText {node} />
  {:else if node.type === 'row' || node.type === 'col'}
    <GenuiRow {node}>
      {#each node.children as child, i (i)}
        <GenuiNode node={child} path={`${path}.${i}`} />
      {/each}
    </GenuiRow>
  {:else if node.type === 'card'}
    <GenuiCard {node}>
      {#each node.children as child, i (i)}
        <GenuiNode node={child} path={`${path}.${i}`} />
      {/each}
    </GenuiCard>
  {:else if node.type === 'collapsible'}
    <GenuiCollapsible {node} {path}>
      {#each node.children as child, i (i)}
        <GenuiNode node={child} path={`${path}.${i}`} />
      {/each}
    </GenuiCollapsible>
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
  {:else if node.type === 'divider'}
    <GenuiDivider />
  {:else if node.type === 'code'}
    <GenuiCode {node} />
  {:else if node.type === 'plot'}
    <GenuiPlot {node} />
  {:else if node.type === 'mermaid'}
    <GenuiMermaid {node} />
  {:else if node.type === 'button'}
    <GenuiButton {node} />
  {:else if node.type === 'input'}
    <GenuiInput {node} />
  {:else if node.type === 'select'}
    <GenuiSelect {node} />
  {:else if node.type === 'checkbox'}
    <GenuiCheckbox {node} />
  {:else if node.type === 'switch'}
    <GenuiSwitch {node} />
  {:else if node.type === 'radio'}
    <GenuiRadio {node} />
  {:else if node.type === 'slider'}
    <GenuiSlider {node} />
  {:else if node.type === 'number'}
    <GenuiNumber {node} />
  {:else if node.type === 'textarea'}
    <GenuiTextarea {node} />
  {:else if node.type === 'quiz'}
    <GenuiQuiz {node} />
  {:else if node.type === 'tabs'}
    <GenuiTabs {node} {path} />
  {/if}
{/if}
