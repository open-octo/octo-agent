<script lang="ts">
  // A single question scored in the browser. Selecting an option writes to
  // the field (so visibleWhen can react to it) and immediately reveals
  // whether it matches `correct`.
  //
  // Scoring locally means `correct` is present in the page — a determined
  // reader can see the answer before choosing. That is the accepted trade:
  // these are comprehension aids inside a conversation the user is already
  // having with the model, not assessments with an adversary, and a
  // server-scored quiz would need a model round-trip per question — exactly
  // the interaction this design exists to remove.
  import { untrack } from 'svelte'
  import type { GenuiQuizNode } from '../../lib/genui/types'
  import { useGenuiFieldContext } from '../../lib/genui/context'

  let { node }: { node: GenuiQuizNode } = $props()
  const ctx = useGenuiFieldContext()

  // Seeded from persisted state, so a reload does not un-answer the question.
  let choice = $state(untrack(() => ctx?.initialString(node.field, '') ?? ''))

  const answered = $derived(choice !== '')
  const correct = $derived(answered && choice === node.correct)

  $effect(() => {
    ctx?.setFieldValue(node.field, choice)
  })

  function pick(value: string) {
    if (!ctx?.interactive || answered) return
    choice = value
  }
</script>

<div class="genui-quiz">
  <div class="genui-quiz-q">{node.question}</div>
  <div class="genui-quiz-options">
    {#each node.options as opt, i (i)}
      <button
        type="button"
        class="genui-quiz-option"
        class:picked={choice === opt.value}
        class:right={answered && opt.value === node.correct}
        class:wrong={answered && choice === opt.value && opt.value !== node.correct}
        disabled={!ctx?.interactive || answered}
        onclick={() => pick(opt.value)}
      >
        {opt.label}
      </button>
    {/each}
  </div>
  {#if answered}
    <div class="genui-quiz-verdict" class:right={correct}>
      {correct ? '✓' : '✕'}
      {#if node.explanation}<span class="genui-quiz-explain">{node.explanation}</span>{/if}
    </div>
  {/if}
</div>

<style>
  .genui-quiz {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 10px 12px;
    border: 1px solid var(--border);
    border-radius: 8px;
  }
  .genui-quiz-q {
    font-size: 13px;
    font-weight: 600;
    color: var(--text);
  }
  .genui-quiz-options {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .genui-quiz-option {
    text-align: left;
    padding: 6px 10px;
    font-size: 13px;
    color: var(--text);
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 6px;
    cursor: pointer;
  }
  .genui-quiz-option:disabled {
    cursor: default;
  }
  .genui-quiz-option.picked {
    border-color: var(--blue-6);
  }
  .genui-quiz-option.right {
    border-color: var(--green-6, #52c41a);
  }
  .genui-quiz-option.wrong {
    border-color: var(--red-6, #f5222d);
  }
  .genui-quiz-verdict {
    font-size: 12px;
    color: var(--red-6, #f5222d);
    display: flex;
    gap: 6px;
  }
  .genui-quiz-verdict.right {
    color: var(--green-6, #52c41a);
  }
  .genui-quiz-explain {
    color: var(--text-secondary);
  }
</style>
