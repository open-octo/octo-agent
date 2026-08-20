<script lang="ts">
  // Filtering and sorting happen here, over the rows already in the spec —
  // never by asking the model for anything. `filterBy` reads a field the
  // panel's own controls set; `sortable` needs no field at all, which is why
  // it survives on the render_ui tool-card path where filtering does not.
  import type { GenuiTableNode } from '../../lib/genui/types'
  import { useGenuiFieldContext } from '../../lib/genui/context'
  import { tableRows, nextSort, type TableSort } from '../../lib/genui/table-view'

  let { node }: { node: GenuiTableNode } = $props()
  const ctx = useGenuiFieldContext()

  let sort = $state<TableSort | null>(null)
  const rows = $derived(tableRows(node, ctx?.fields ?? {}, sort))

  function toggleSort(column: number) {
    if (!node.sortable) return
    sort = nextSort(sort, column)
  }
</script>

<div class="table-scroll">
  <table>
    <thead>
      <tr>
        {#each node.columns as col, i (i)}
          <th
            class:sortable={node.sortable}
            role={node.sortable ? 'button' : undefined}
            tabindex={node.sortable ? 0 : undefined}
            aria-sort={sort?.column === i ? (sort.direction === 'asc' ? 'ascending' : 'descending') : undefined}
            onclick={() => toggleSort(i)}
            onkeydown={e => {
              if (node.sortable && (e.key === 'Enter' || e.key === ' ')) {
                e.preventDefault()
                toggleSort(i)
              }
            }}
          >
            {col}
            {#if node.sortable && sort?.column === i}
              <span class="sort-caret">{sort.direction === 'asc' ? '▲' : '▼'}</span>
            {/if}
          </th>
        {/each}
      </tr>
    </thead>
    <tbody>
      {#each rows as row, ri (ri)}
        <tr>
          {#each row as cell, ci (ci)}
            <td>{cell}</td>
          {/each}
        </tr>
      {/each}
    </tbody>
  </table>
  {#if rows.length === 0 && node.rows.length > 0}
    <div class="table-empty">no rows match</div>
  {/if}
</div>

<style>
  /* Scoped to this component's own markup — the shared .rich-answer/
     .think-body ancestor-scoped overflow rule (ChatView.svelte) does not
     reach a GenUI table rendered inside a tool card, which sits outside
     .rich-answer entirely. Reusing the .table-scroll class name (not
     inventing a new one) keeps a GenUI table visually identical to a
     markdown table; this block supplies the same overflow behavior
     locally so it works regardless of where GenuiTable is mounted. */
  .table-scroll {
    overflow-x: auto;
    margin: 0;
  }
  table {
    border-collapse: collapse;
    width: 100%;
    font-size: 13px;
  }
  th,
  td {
    border: 1px solid var(--border-table, var(--border));
    padding: 4px 8px;
    text-align: left;
    white-space: nowrap;
  }
  th {
    background: var(--bg-table-header, var(--bg-container));
    color: var(--text-secondary);
    font-weight: 600;
  }
  td {
    color: var(--text);
  }
  th.sortable {
    cursor: pointer;
    user-select: none;
  }
  .sort-caret {
    font-size: 9px;
    color: var(--text-secondary);
  }
  .table-empty {
    padding: 6px 8px;
    font-size: 12px;
    color: var(--text-secondary);
  }
</style>
