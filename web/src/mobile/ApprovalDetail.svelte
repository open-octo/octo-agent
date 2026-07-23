<script lang="ts">
  // Mobile approval detail: the high-value phone scenario — approve or reject a
  // pending high-risk command. Reads the same `confirmModal` store the desktop
  // ConfirmModal uses and answers over the same ws.answerConfirmation contract
  // ('yes' = allow once, 'always' = allow for session, anything else = deny).
  // The desktop ConfirmModal overlay is suppressed on mobile (App.svelte) so this
  // is the single approval surface.
  import { activeSessionId } from '../lib/stores'
  import { ws } from '../lib/ws'
  import { getSessionConfirmation, type SessionConfirmation } from '../lib/api'

  let { onBack }: { onBack: () => void } = $props()

  const sid = $derived($activeSessionId ?? '')
  let c = $state<SessionConfirmation | null>(null)
  let loading = $state(true)

  // The feed isn't subscribed to the session (request_confirmation only reaches
  // subscribers), so fetch the pending confirmation over REST when this opens.
  $effect(() => {
    const s = sid
    if (!s) { c = null; loading = false; return }
    loading = true
    getSessionConfirmation(s)
      .then(resp => { c = resp?.pending ? resp : null; loading = false })
      .catch(() => { c = null; loading = false })
  })

  function answer(result: string) {
    if (c?.id) ws.answerConfirmation(c.id, result)
    onBack()
  }

  function diffClass(line: string): string {
    if (line.startsWith('@@')) return 'hdr'
    if (line.startsWith('-')) return 'rm'
    if (line.startsWith('+')) return 'add'
    return 'plain'
  }
</script>

<header class="dhead">
  <button class="back" onclick={onBack} aria-label="返回">
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="var(--m-text)" stroke-width="2"><path d="m15 18-6-6 6-6"/></svg>
  </button>
  <span class="dtitle">审批</span>
</header>

<div class="scroll">
  {#if c}
    <div class="warn">
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="var(--m-warning)" stroke-width="2"><path d="M12 9v4M12 17h.01"/><path d="M10.3 3.9 2 18a2 2 0 0 0 1.7 3h16.6A2 2 0 0 0 22 18L13.7 3.9a2 2 0 0 0-3.4 0z"/></svg>
      <span>agent 请求执行高风险操作,需要你确认。</span>
    </div>

    {#if c.message}<p class="desc">{c.message}</p>{/if}

    {#if c.command}
      <div class="label">命令</div>
      <pre class="term"><span class="p">$</span> {c.command}</pre>
    {:else if c.diff}
      <div class="label">改动</div>
      <div class="diff">
        {#each c.diff.split('\n') as line}
          <div class="dl {diffClass(line)}">{line}</div>
        {/each}
      </div>
    {:else if c.input}
      <div class="label">输入</div>
      <pre class="term">{c.input}</pre>
    {/if}

    <div class="actions">
      {#if c.kind === 'ok'}
        <button class="btnP" onclick={() => answer('ok')}>好的</button>
      {:else}
        <button class="btnP" onclick={() => answer('yes')}>批准执行</button>
        <button class="btnD" onclick={() => answer('deny')}>拒绝</button>
        <button class="btnG" onclick={() => answer('always')}>本次会话都允许</button>
      {/if}
    </div>
  {:else if loading}
    <div class="empty">加载中…</div>
  {:else}
    <div class="empty">没有待审批的请求(可能已在别处处理)。</div>
    <button class="btnD full" onclick={onBack}>返回</button>
  {/if}
</div>

<style>
  .dhead { flex: none; display: flex; align-items: center; gap: 10px; padding: 6px 14px 12px; }
  .back { flex: none; width: 34px; height: 34px; border-radius: 50%; border: none; background: var(--m-surface); display: flex; align-items: center; justify-content: center; cursor: pointer; box-shadow: var(--m-shadow-card); }
  .dtitle { font-size: 15px; font-weight: 600; color: var(--m-text); }
  .scroll { flex: 1; min-height: 0; overflow-y: auto; -webkit-overflow-scrolling: touch; padding: 6px 16px 20px; }
  .warn { display: flex; align-items: center; gap: 8px; background: var(--m-tag-warn-bg); border: 1px solid var(--m-tag-warn-border); border-radius: 12px; padding: 12px 14px; margin-bottom: 14px; font-size: 13px; color: var(--m-text); }
  .warn svg { flex: none; }
  .desc { margin: 0 0 14px; font-size: 13px; line-height: 1.6; color: var(--m-text-2); }
  .label { font-size: 12px; color: var(--m-text-3); margin: 0 2px 6px; }
  .term { margin: 0 0 16px; padding: 12px 14px; background: var(--m-terminal-bg); color: var(--m-terminal-fg); border-radius: 12px; font-size: 12.5px; line-height: 1.7; font-family: ui-monospace, Menlo, monospace; overflow-x: auto; white-space: pre-wrap; word-break: break-all; }
  .term .p { color: var(--m-success); }
  .diff { margin-bottom: 16px; border: 1px solid var(--m-border); border-radius: 12px; overflow: hidden; font-size: 12.5px; line-height: 1.7; font-family: ui-monospace, Menlo, monospace; }
  .dl { padding: 1px 12px; white-space: pre-wrap; word-break: break-all; }
  .dl.hdr { color: var(--m-text-3); }
  .dl.rm { background: var(--m-tag-warn-bg); color: var(--m-error); }
  .dl.add { background: var(--m-tag-ok-bg); color: var(--m-tag-ok-text); }
  .dl.plain { color: var(--m-text-2); }
  .actions { display: flex; flex-direction: column; gap: 10px; margin-top: 4px; }
  .btnP { padding: 12px 0; border-radius: 10px; border: none; background: var(--m-accent); color: #fff; font-size: 15px; font-weight: 600; font-family: inherit; cursor: pointer; }
  .btnD { padding: 12px 0; border-radius: 10px; border: 1px solid var(--m-border); background: var(--m-surface); color: var(--m-text); font-size: 15px; font-weight: 500; font-family: inherit; cursor: pointer; }
  .btnG { padding: 10px 0; border-radius: 10px; border: none; background: none; color: var(--m-text-3); font-size: 13px; font-family: inherit; cursor: pointer; }
  .full { width: 100%; margin-top: 14px; }
  .empty { padding: 40px 8px; text-align: center; font-size: 13px; color: var(--m-text-3); }
</style>
