<script lang="ts">
  import { running, activeSessionId, chatStreaming } from '../../lib/stores'
  import { ws } from '../../lib/ws'
  import { get } from 'svelte/store'
  import StatusTag from '../ui/StatusTag.svelte'

  let { onSend }: { onSend?: (text: string) => void } = $props()

  let text = $state('')

  // Derive streaming state from store
  let isStreaming = $derived.by(() => {
    const sid = get(activeSessionId)
    if (!sid) return false
    return get(chatStreaming)[sid] ?? false
  })

  function send() {
    if (!text.trim()) return
    const t = text.trim()
    text = ''
    if (onSend) {
      onSend(t)
    } else {
      running.set(true)
    }
  }

  function stop() {
    const sid = get(activeSessionId)
    if (sid) ws.interrupt(sid)
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
      <span>claude-sonnet-4.6</span>
      <iconify-icon icon="lucide:chevron-down" width="12"></iconify-icon>
    </button>
    <button class="chip">
      <span>Reasoning: Medium</span>
      <iconify-icon icon="lucide:chevron-down" width="12"></iconify-icon>
    </button>
    <span class="chip static"><span class="mono">~/code/octo-agent</span></span>
    <span class="chip static context-chip">
      <span>Context</span>
      <span class="ctx-bar"><span class="ctx-fill" style="width:38%"></span></span>
      <span class="mono">38%</span>
    </span>
    <span style="margin-left:auto;"></span>
    <StatusTag status="warning">Ask Mode</StatusTag>
  </div>

  <div class="input-wrap">
    <div class="input-card">
      <textarea
        rows={2}
        placeholder="Message Octo… (Enter to send · Shift+Enter for newline · / for skills)"
        bind:value={text}
        onkeydown={onKeydown}
      ></textarea>
      <div class="input-footer">
        <button class="tool-btn" title="Attach file">
          <iconify-icon icon="ant-design:paper-clip-outlined" width="15"></iconify-icon>
        </button>
        <button class="tool-btn skill-btn" title="Insert skill">/</button>
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
