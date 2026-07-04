<script lang="ts">
  import { confirmModal } from '../../lib/stores'
  import { ws } from '../../lib/ws'
  import { t } from '../../lib/i18n'
  import StatusTag from '../ui/StatusTag.svelte'

  let modalEl = $state<HTMLDivElement | null>(null)

  // Focus the modal whenever it (re)opens, same as AuthGate/CommandPalette —
  // review caught that a window-level keydown listener without stealing
  // focus leaves the chat composer's textarea focused underneath the
  // backdrop. Enter there both sends the composer's draft AND (via bubbling)
  // approved the pending tool call — approving blind again, just via
  // keyboard instead of the backdrop click this fix closed off.
  $effect(() => {
    if ($confirmModal && modalEl) modalEl.focus()
  })

  function deny() {
    if (!$confirmModal) return
    ws.answerConfirmation($confirmModal.id, 'deny')
    confirmModal.set(null)
  }

  function answer(result: string) {
    if (!$confirmModal) return
    ws.answerConfirmation($confirmModal.id, result)
    confirmModal.set(null)
  }

  // #1105: Esc / Enter, matching AuthGate / CommandPalette. Bound on the
  // modal element itself (focused via the $effect above), not on
  // svelte:window — a window-level listener fires *in addition to*
  // whatever element already has focus, which was the composer textarea.
  // Enter maps to the primary action — for a permission ask that's now
  // "allow once" (the least-privilege choice), not "allow for session".
  function onKeydown(e: KeyboardEvent) {
    if (!$confirmModal) return
    if (e.key === 'Escape') {
      e.preventDefault()
      $confirmModal.kind === 'ok' ? answer('ok') : deny()
    } else if (e.key === 'Enter') {
      e.preventDefault()
      answer($confirmModal.kind === 'ok' ? 'ok' : 'yes')
    }
  }

  // #1105: diff line classification, same rule ToolGroup.svelte uses for
  // the post-execution edit card — kept in sync so the preview and the
  // result look identical.
  function diffLineClass(line: string): string {
    if (line.startsWith('@@')) return 'diff-hdr'
    if (line.startsWith('-')) return 'diff-line rm'
    if (line.startsWith('+')) return 'diff-line add'
    return 'diff-line plain'
  }
</script>

{#if $confirmModal}
<!-- #1105: backdrop no longer denies on click — a stray click used to
     silently cancel the pending tool call mid-turn. Dismissal now requires
     an explicit button or Esc. -->
<div class="backdrop" role="presentation">
  <div class="modal" role="dialog" aria-modal="true" tabindex="-1" bind:this={modalEl} onkeydown={onKeydown}>
    <div class="modal-header">
      <iconify-icon icon="ant-design:safety-outlined" width="16" style="color:var(--warning);flex-shrink:0"></iconify-icon>
      <span class="modal-title">{$t('perm.title')}</span>
      <span class="header-tag">
        <StatusTag status="warning">{$t('perm.awaiting')}</StatusTag>
      </span>
    </div>

    <div class="modal-body">
      {#if $confirmModal.message}
        <p class="desc">{$confirmModal.message}</p>
      {/if}
      {#if $confirmModal.command}
        <pre class="terminal"><span class="prompt">$</span> {$confirmModal.command}</pre>
      {:else if $confirmModal.diff}
        <div class="diff-block">
          {#each $confirmModal.diff.split('\n') as line}
            <div class={diffLineClass(line)}>{line}</div>
          {/each}
        </div>
      {:else if $confirmModal.input}
        <pre class="terminal">{$confirmModal.input}</pre>
      {:else if $confirmModal.content}
        <pre class="terminal">{$confirmModal.content}</pre>
      {/if}
    </div>

    <div class="modal-footer">
      {#if $confirmModal.kind === 'ok'}
        <button class="btn-primary" onclick={() => answer('ok')}>{$t('common.ok')}</button>
      {:else}
        <button class="btn-deny" onclick={deny}>
          <iconify-icon icon="ant-design:close-outlined" width="12"></iconify-icon>
          {$t('perm.deny')}
        </button>
        <span class="spacer"></span>
        <!-- Result strings are the wire contract with the server's mapConfirmResult:
             'yes' = allow once, 'always' = allow + remember for the session.
             Anything else (incl. 'deny') denies. Don't rename without updating the server.
             #1105: "allow once" is now the primary (blue) button — the most
             prominent action should be the least permissive one, not
             "allow for session". -->
        <button class="btn-primary" onclick={() => answer('yes')}>
          <iconify-icon icon="ant-design:check-outlined" width="12"></iconify-icon>
          {$t('perm.allow_once')}
        </button>
        <button class="btn-secondary" onclick={() => answer('always')}>{$t('perm.allow_session')}</button>
      {/if}
    </div>
  </div>
</div>
{/if}

<style>
.backdrop {
  position: fixed; inset: 0; z-index: 1100;
  background: var(--text-tertiary);
  display: flex; align-items: center; justify-content: center;
  padding: 24px;
}
.modal {
  width: 100%; max-width: 480px;
  background: var(--warning-bg);
  border: 1px solid var(--warning-border);
  border-radius: 12px;
  overflow: hidden;
  box-shadow: 0 16px 48px rgba(0,0,0,0.18);
  animation: octo-fadein 0.16s ease;
}
/* The modal itself is the focus target (see the $effect above) so Enter/Esc
   don't leak to whatever was focused underneath (e.g. the chat composer). */
.modal:focus { outline: none; }
.modal-header {
  display: flex; align-items: center; gap: 8px;
  padding: 12px 18px;
  border-bottom: 1px solid var(--warning-border);
}
.modal-title {
  font-size: 14px; font-weight: 600; color: var(--text-heading); flex: 1;
}
.header-tag {
  margin-left: auto; flex-shrink: 0;
}
.modal-body {
  padding: 16px 18px;
  display: flex; flex-direction: column; gap: 12px;
}
.desc {
  margin: 0;
  font-size: 13px; line-height: 1.6; color: var(--text-secondary);
}
.terminal {
  margin: 0; padding: 10px 12px;
  background: var(--terminal-bg); color: var(--terminal-text);
  border-radius: 6px;
  font-size: 12px; line-height: 1.6;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  overflow-x: auto; overflow-y: auto; max-height: 220px;
  white-space: pre-wrap; word-break: break-all;
}
.prompt { color: var(--success); }
/* #1105: edit_file preview — same classification/coloring ToolGroup.svelte
   uses for the post-execution diff card, scoped to this modal's spacing. */
.diff-block {
  border: 1px solid var(--border); border-radius: 6px;
  overflow: hidden; overflow-y: auto; max-height: 220px;
  font-size: 12px; line-height: 1.6;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}
.diff-hdr { padding: 4px 10px; color: var(--text-tertiary); }
.diff-line { padding: 1px 10px; white-space: pre-wrap; word-break: break-all; }
.diff-line.rm { background: var(--error-bg); color: var(--error-dark); border-left: 2px solid var(--error); }
.diff-line.add { background: var(--success-bg); color: var(--success-text); border-left: 2px solid var(--success); }
.diff-line.plain { color: var(--text-secondary); }
.modal-footer {
  padding: 12px 18px;
  border-top: 1px solid var(--warning-border);
  display: flex; align-items: center; gap: 8px;
}
.spacer { flex: 1; }
.btn-deny {
  height: 32px; padding: 0 12px;
  border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 6px;
  display: flex; align-items: center; gap: 6px;
  font-size: 13px; color: var(--text-secondary);
  cursor: pointer; font-family: inherit;
}
.btn-deny:hover { border-color: var(--error); color: var(--error); }
.btn-secondary {
  height: 32px; padding: 0 12px;
  border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 6px;
  font-size: 13px; color: var(--text-secondary);
  cursor: pointer; font-family: inherit;
}
.btn-secondary:hover { border-color: var(--blue-5); color: var(--blue-5); }
.btn-primary {
  height: 32px; padding: 0 14px;
  border: none; background: var(--blue-6);
  border-radius: 6px;
  display: flex; align-items: center; gap: 6px;
  font-size: 13px; color: #fff;
  cursor: pointer; font-family: inherit;
}
.btn-primary:hover { background: var(--blue-5); }
</style>
