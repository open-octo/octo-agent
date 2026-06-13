<script lang="ts">
  import { get } from 'svelte/store'
  import {
    running, activeSessionId, chatStreaming, sessions,
    chatContextUsage, chatWorkingDir, chatPermMode, chatReasoningEffort,
  } from '../../lib/stores'
  import { ws } from '../../lib/ws'
  import { t as tr } from '../../lib/i18n'
  import StatusTag from '../ui/StatusTag.svelte'

  let { onSend }: { onSend?: (text: string, files?: any[]) => void } = $props()

  let text = $state('')
  let textareaEl = $state<HTMLTextAreaElement | null>(null)
  let fileInputEl = $state<HTMLInputElement | null>(null)
  let attachments = $state<{ name: string; data_url: string; mime_type: string }[]>([])

  // Called by ChatView when the user clicks "edit" on a prior message — loads
  // that text back into the composer for resend.
  export function setText(v: string) {
    text = v
    queueMicrotask(() => textareaEl?.focus())
  }

  function openAttach() {
    fileInputEl?.click()
  }

  function onFilesPicked(e: Event) {
    const input = e.target as HTMLInputElement
    const files = Array.from(input.files ?? [])
    for (const f of files) {
      const reader = new FileReader()
      reader.onload = () => {
        attachments = [...attachments, { name: f.name, data_url: String(reader.result), mime_type: f.type }]
      }
      reader.readAsDataURL(f)
    }
    input.value = ''
  }

  function removeAttachment(i: number) {
    attachments = attachments.filter((_, idx) => idx !== i)
  }

  // The "/" button inserts a slash so the user can type a skill/slash command.
  function insertSkill() {
    if (!text.startsWith('/')) text = '/' + text
    queueMicrotask(() => textareaEl?.focus())
  }

  // $store autosubscription is reactive inside $derived (get() is not).
  let sid = $derived($activeSessionId ?? '')
  let isStreaming = $derived($chatStreaming[sid] ?? false)
  let currentSession = $derived($sessions.find((s: any) => s.id === sid) ?? null)

  // Session meta chips — pull live values from per-session stores, fall back
  // to the session record, then to sensible defaults.
  let modelName = $derived(currentSession?.model || currentSession?.model_id || '—')
  let reasoning = $derived($chatReasoningEffort[sid] || currentSession?.reasoning_effort || 'medium')
  let workingDir = $derived($chatWorkingDir[sid] || currentSession?.working_dir || '')
  let permMode = $derived($chatPermMode[sid] || currentSession?.permission_mode || 'ask')
  let ctxUsage = $derived(Number($chatContextUsage[sid] ?? currentSession?.context_usage ?? 0))

  function cap(s: string): string {
    return s ? s[0].toUpperCase() + s.slice(1) : s
  }

  // Show only the last two path segments so a long working dir doesn't push
  // the chip row onto a second line. Full path is in the title tooltip.
  function shortDir(p: string): string {
    if (!p) return ''
    const parts = p.split('/').filter(Boolean)
    return parts.length <= 2 ? p : '…/' + parts.slice(-2).join('/')
  }

  function send() {
    if (!text.trim() && attachments.length === 0) return
    const v = text.trim()
    const files = attachments.length ? [...attachments] : undefined
    text = ''
    attachments = []
    if (onSend) {
      onSend(v, files)
    } else {
      running.set(true)
    }
  }

  function stop() {
    const s = get(activeSessionId)
    if (s) ws.interrupt(s)
    running.set(false)
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send() }
  }
</script>

<div class="composer">
  <div class="chips">
    <button class="chip">
      <iconify-icon icon="ant-design:robot-outlined" width="12"></iconify-icon>
      <span>{modelName}</span>
      <iconify-icon icon="lucide:chevron-down" width="12"></iconify-icon>
    </button>
    <button class="chip">
      <span>Reasoning: {cap(reasoning)}</span>
      <iconify-icon icon="lucide:chevron-down" width="12"></iconify-icon>
    </button>
    {#if workingDir}
      <span class="chip static" title={workingDir}><span class="mono">{shortDir(workingDir)}</span></span>
    {/if}
    <span class="chip static context-chip">
      <span>{tr('chat.context')}</span>
      <span class="ctx-bar"><span class="ctx-fill" style="width:{Math.min(ctxUsage, 100)}%"></span></span>
      <span class="mono">{ctxUsage}%</span>
    </span>
    <span style="margin-left:auto;"></span>
    {#if permMode === 'auto'}
      <StatusTag status="success">{tr('chat.auto_mode')}</StatusTag>
    {:else}
      <StatusTag status="warning">{tr('chat.ask_mode')}</StatusTag>
    {/if}
  </div>

  <div class="input-wrap">
    <div class="input-card">
      {#if attachments.length > 0}
        <div class="attachments">
          {#each attachments as a, i}
            <span class="attach-chip" title={a.name}>
              <iconify-icon icon="ant-design:paper-clip-outlined" width="12"></iconify-icon>
              <span class="attach-name">{a.name}</span>
              <button class="attach-x" title="Remove" onclick={() => removeAttachment(i)}>
                <iconify-icon icon="ant-design:close-outlined" width="11"></iconify-icon>
              </button>
            </span>
          {/each}
        </div>
      {/if}
      <textarea
        bind:this={textareaEl}
        rows={2}
        placeholder={tr('chat.placeholder')}
        bind:value={text}
        onkeydown={onKeydown}
      ></textarea>
      <input
        bind:this={fileInputEl}
        type="file"
        accept="image/*"
        multiple
        style="display:none"
        onchange={onFilesPicked}
      />
      <div class="input-footer">
        <button class="tool-btn" title="Attach image" onclick={openAttach}>
          <iconify-icon icon="ant-design:paper-clip-outlined" width="15"></iconify-icon>
        </button>
        <button class="tool-btn skill-btn" title="Insert slash command" onclick={insertSkill}>/</button>
        <span style="margin-left:auto;"></span>
        {#if isStreaming || $running}
          <button class="stop-btn" onclick={stop}>
            <span class="stop-sq"></span>
            Stop
          </button>
        {:else}
          <button class="send-btn" onclick={send}>Send</button>
        {/if}
      </div>
    </div>
  </div>
</div>

<style>
.composer {
  flex: 0 0 auto;
  background: rgba(255,255,255,0.96);
  backdrop-filter: blur(8px);
  border-top: 1px solid #EEEFF1;
}
.chips {
  max-width: 800px; margin: 0 auto;
  padding: 12px 24px 0;
  display: flex; align-items: center; gap: 8px; flex-wrap: wrap;
}
.chip {
  height: 24px; padding: 0 10px; border: 1px solid #D9D9D9; background: #fff;
  border-radius: 9999px; display: flex; align-items: center; gap: 6px;
  font-size: 12px; color: rgba(0,0,0,0.65); cursor: pointer; font-family: inherit;
}
.chip:hover { border-color: #4096FF; color: #4096FF; }
.chip.static { cursor: default; background: #FAFAFA; border-color: #EEEFF1; }
.chip.static:hover { border-color: #EEEFF1; color: rgba(0,0,0,0.65); }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.context-chip { gap: 8px; }
.ctx-bar { width: 56px; height: 4px; background: #F0F0F0; border-radius: 9999px; overflow: hidden; display: inline-block; }
.ctx-fill { display: block; height: 100%; background: #1677FF; border-radius: 9999px; }
.input-wrap { max-width: 800px; margin: 10px auto 0; padding: 0 24px 16px; }
.input-card {
  background: #fff; border: 1px solid #D9D9D9; border-radius: 12px;
  padding: 10px 12px; display: flex; flex-direction: column; gap: 8px;
}
.input-card:focus-within {
  border-color: #1677FF;
  box-shadow: 0 0 0 2px rgba(5,145,255,0.1);
}
textarea {
  border: none; outline: none; resize: none; font-size: 14px; line-height: 1.6;
  font-family: inherit; color: rgba(0,0,0,0.88); background: transparent; width: 100%;
}
.attachments { display: flex; flex-wrap: wrap; gap: 6px; }
.attach-chip {
  display: inline-flex; align-items: center; gap: 5px; max-width: 200px;
  height: 24px; padding: 0 6px 0 8px; background: #F0F7FF; border: 1px solid #BAE0FF;
  border-radius: 6px; font-size: 12px; color: rgba(0,0,0,0.65);
}
.attach-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.attach-x {
  border: none; background: transparent; cursor: pointer; padding: 0;
  display: flex; align-items: center; color: rgba(0,0,0,0.4);
}
.attach-x:hover { color: #FF4D4F; }
.input-footer { display: flex; align-items: center; gap: 4px; }
.tool-btn {
  width: 28px; height: 28px; border: none; background: transparent; border-radius: 6px;
  display: flex; align-items: center; justify-content: center;
  cursor: pointer; color: rgba(0,0,0,0.45);
}
.tool-btn:hover { background: rgba(0,0,0,0.04); color: rgba(0,0,0,0.65); }
.skill-btn { font-size: 14px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.send-btn {
  height: 32px; padding: 0 16px; border: none; background: #1677FF;
  border-radius: 6px; font-size: 14px; color: #fff; cursor: pointer; font-family: inherit;
}
.send-btn:hover { background: #4096FF; }
.stop-btn {
  height: 32px; padding: 0 14px; border: 1px solid #FFCCC7; background: #FFF1F0;
  border-radius: 6px; display: flex; align-items: center; gap: 7px;
  font-size: 14px; color: #FF4D4F; cursor: pointer; font-family: inherit;
}
.stop-btn:hover { border-color: #FF4D4F; }
.stop-sq { width: 9px; height: 9px; border-radius: 2px; background: #FF4D4F; }
</style>
