<script lang="ts">
  import { questionModals } from '../../lib/stores'
  import QuestionCard from './QuestionCard.svelte'

  // With global broadcast, a question can originate from ANY session — render
  // every pending question in the store so the user can answer any of them
  // regardless of which session tab is active.
  const entries = $derived(Object.entries($questionModals))
</script>

{#each entries as [sid, entry] (sid)}
  <QuestionCard {entry} />
{/each}
