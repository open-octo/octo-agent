<script lang="ts">
  import type { GenuiTableNode } from '../../lib/genui/types'

  let { node }: { node: GenuiTableNode } = $props()
</script>

<div class="table-scroll">
  <table>
    <thead>
      <tr>
        {#each node.columns as col, i (i)}
          <th>{col}</th>
        {/each}
      </tr>
    </thead>
    <tbody>
      {#each node.rows as row, ri (ri)}
        <tr>
          {#each row as cell, ci (ci)}
            <td>{cell}</td>
          {/each}
        </tr>
      {/each}
    </tbody>
  </table>
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
</style>
