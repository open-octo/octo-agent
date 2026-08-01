<script lang="ts">
  import { t, tr, pickLocalized, pickLocalizedList } from '../../lib/i18n'
  import { summonAgent } from '../../lib/stores'
  import * as api from '../../lib/api'

  let { agent, onClose }: { agent: api.Agent; onClose: () => void } = $props()

  let name = $derived(pickLocalized(agent.name, agent.name_en))
  let description = $derived(pickLocalized(agent.description, agent.description_en))
  let tags = $derived(pickLocalizedList(agent.tags, agent.tags_en))
  let examplePrompts = $derived(pickLocalizedList(agent.example_prompts, agent.example_prompts_en))

  function agentInitial(n: string): string {
    return n.charAt(0).toUpperCase()
  }
  function agentIconColor(n: string): string {
    const colors = ['#1677ff', '#722ed1', '#13c2c2', '#52c41a', '#eb2f96', '#fa8c16', '#2f54eb', '#a0d911']
    let hash = 0
    for (let i = 0; i < n.length; i++) hash = n.charCodeAt(i) + ((hash << 5) - hash)
    return colors[Math.abs(hash) % colors.length]
  }

  async function summon(prompt?: string) {
    await summonAgent(agent.id, name, prompt)
    onClose()
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') { e.preventDefault(); onClose() }
  }
</script>

<div class="backdrop" role="presentation" onclick={onClose}>
  <div class="modal" onclick={(e) => e.stopPropagation()} onkeydown={onKeydown} role="dialog" aria-modal="true" tabindex="-1">
    <button class="close-btn" onclick={onClose} aria-label={tr('common.close')}>
      <iconify-icon icon="ant-design:close-outlined" width="16"></iconify-icon>
    </button>

    <div class="modal-header">
      {#if agent.icon}
        <span class="agent-icon" style="background-color: {agentIconColor(name)}11; color: {agentIconColor(name)}">
          <iconify-icon icon={agent.icon} width="20"></iconify-icon>
        </span>
      {:else}
        <span class="agent-icon" style="background-color: {agentIconColor(name)}11; color: {agentIconColor(name)}">
          {agentInitial(name)}
        </span>
      {/if}
      <div class="header-text">
        <span class="agent-name">{name}</span>
        {#if agent.source === 'default'}
          <span class="official-badge">{$t('agents.official_badge')}</span>
        {/if}
      </div>
    </div>

    <div class="modal-body">
      <section>
        <h4>{$t('agents.capability_intro')}</h4>
        <p class="desc-text">{description}</p>
      </section>

      {#if tags.length > 0}
        <section>
          <h4>{$t('agents.good_at')}</h4>
          <div class="tag-row">
            {#each tags as tag}
              <span class="tag-chip">{tag}</span>
            {/each}
          </div>
        </section>
      {/if}

      {#if examplePrompts.length > 0}
        <section>
          <h4>{$t('agents.try_asking')}</h4>
          <div class="prompt-list">
            {#each examplePrompts as prompt}
              <button class="prompt-btn" onclick={() => summon(prompt)}>
                <span>{prompt}</span>
                <iconify-icon icon="ant-design:arrow-right-outlined" width="13"></iconify-icon>
              </button>
            {/each}
          </div>
        </section>
      {/if}
    </div>

    <div class="modal-footer">
      <button class="btn-summon" onclick={() => summon()}>
        {$t('agents.summon').replace('{name}', name)}
      </button>
    </div>
  </div>
</div>

<style>
.backdrop {
  position: fixed; inset: 0; z-index: 1100;
  background: var(--scrim);
  display: flex; align-items: center; justify-content: center;
  padding: 24px;
}
.modal {
  width: 100%; max-width: 460px; position: relative;
  background: var(--bg-container);
  border-radius: 12px;
  overflow: hidden;
  box-shadow: 0 16px 48px rgba(0,0,0,0.18);
  animation: octo-fadein 0.16s ease;
}
.modal:focus { outline: none; }
.close-btn {
  position: absolute; top: 12px; right: 12px; z-index: 1;
  width: 28px; height: 28px; border: none; background: transparent;
  border-radius: 7px; display: flex; align-items: center; justify-content: center;
  cursor: pointer; color: var(--text-tertiary);
}
.close-btn:hover { background: var(--hover-neutral); color: var(--text); }

.modal-header {
  display: flex; align-items: center; gap: 12px;
  padding: 20px 20px 16px;
}
.agent-icon {
  width: 44px; height: 44px; flex: 0 0 44px; border-radius: 12px;
  display: flex; align-items: center; justify-content: center;
  font-size: 17px; font-weight: 600;
}
.header-text { display: flex; align-items: center; gap: 8px; min-width: 0; }
.agent-name { font-size: 17px; font-weight: 700; color: var(--text-heading); }
.official-badge {
  height: 18px; padding: 0 6px; border-radius: 4px;
  background: var(--blue-1); color: var(--blue-6); font-size: 11px;
  display: flex; align-items: center; flex: 0 0 auto;
}

.modal-body {
  padding: 0 20px 16px;
  display: flex; flex-direction: column; gap: 16px;
  max-height: 50vh; overflow-y: auto;
}
h4 { margin: 0 0 8px; font-size: 12px; font-weight: 600; color: var(--text-tertiary); }
.desc-text { margin: 0; font-size: 13px; line-height: 1.6; color: var(--text-secondary); }

.tag-row { display: flex; gap: 6px; flex-wrap: wrap; }
.tag-chip {
  height: 22px; padding: 0 9px; background: var(--bg-table-header); color: var(--text-secondary);
  border: 1px solid var(--border-secondary);
  border-radius: 5px; display: flex; align-items: center; font-size: 12px;
}

.prompt-list { display: flex; flex-direction: column; gap: 6px; }
.prompt-btn {
  display: flex; align-items: center; justify-content: space-between; gap: 8px;
  width: 100%; padding: 9px 12px;
  border: 1px solid var(--border-table); background: var(--bg-table-header);
  border-radius: 8px;
  font-size: 12.5px; color: var(--text); line-height: 1.5;
  text-align: left; cursor: pointer; font-family: inherit;
  transition: border-color 0.15s, background 0.15s;
}
.prompt-btn:hover { border-color: var(--blue-2); background: var(--blue-1); color: var(--blue-6); }
.prompt-btn iconify-icon { flex: 0 0 auto; opacity: 0.6; }

.modal-footer { padding: 14px 20px; border-top: 1px solid var(--border-table); }
.btn-summon {
  width: 100%; height: 38px; border: none; background: var(--blue-6);
  border-radius: 8px; font-size: 14px; font-weight: 600; color: #fff; cursor: pointer;
  font-family: inherit;
}
.btn-summon:hover { background: var(--blue-5); }
</style>
