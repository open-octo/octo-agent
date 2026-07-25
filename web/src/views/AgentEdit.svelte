<script lang="ts">
  import { t } from '../lib/i18n'
  import type { Agent } from '../lib/api'

  interface Props {
    agent: Agent | null
    onSave: (agent: Agent) => void
    onCancel: () => void
  }

  let { agent, onSave, onCancel }: Props = $props()

  let name = $state('')
  let description = $state('')
  let model = $state('')
  let tools = $state('')
  let toolSkills = $state('')
  let mentionAs = $state('')
  let systemPrompt = $state('')

  // Reset form fields when the agent prop changes (Svelte 5 captures prop
  // values only at initialisation; without this, editing a different agent
  // keeps the previous form content).
  $effect(() => {
    name = agent?.name ?? ''
    description = agent?.description ?? ''
    model = agent?.model ?? ''
    tools = agent?.tools?.join(', ') ?? ''
    toolSkills = agent?.tool_skills?.join(', ') ?? ''
    mentionAs = agent?.mention_as?.join(', ') ?? ''
    systemPrompt = agent?.system_prompt ?? ''
  })

  function handleSubmit(e: Event) {
    e.preventDefault()
    const body = {
      name: name.trim(),
      description: description.trim(),
      model: model.trim() || undefined,
      tools: tools.trim() ? tools.split(',').map(s => s.trim()).filter(Boolean) : undefined,
      tool_skills: toolSkills.trim() ? toolSkills.split(',').map(s => s.trim()).filter(Boolean) : undefined,
      mention_as: mentionAs.trim() ? mentionAs.split(',').map(s => s.trim()).filter(Boolean) : undefined,
      system_prompt: systemPrompt.trim() || undefined,
    }
    if (!body.name || !body.description) {
      alert('Name and description are required')
      return
    }
    onSave(body as Agent)
  }
</script>

<div class="modal-backdrop" onclick={onCancel}>
  <div class="modal" onclick={(e) => e.stopPropagation()}>
    <h3>{agent ? $t('agents.edit') : $t('agents.create')}</h3>
    <form onsubmit={handleSubmit}>
      <div class="field">
        <label>{$t('agents.name')} *</label>
        <input bind:value={name} placeholder={$t('agents.name_placeholder')} required />
      </div>
      <div class="field">
        <label>{$t('agents.description')} *</label>
        <textarea bind:value={description} placeholder={$t('agents.description_placeholder')} rows="2" required></textarea>
      </div>
      <div class="field">
        <label>{$t('agents.system_prompt')}</label>
        <textarea bind:value={systemPrompt} placeholder={$t('agents.system_prompt_placeholder')} rows="4"></textarea>
      </div>
      <div class="field">
        <label>{$t('agents.model')}</label>
        <input bind:value={model} placeholder={$t('agents.model_placeholder')} />
      </div>
      <div class="field">
        <label>{$t('agents.tools')}</label>
        <input bind:value={tools} placeholder={$t('agents.tools_placeholder')} />
        <small>{$t('agents.tools_hint')}</small>
      </div>
      <div class="field">
        <label>{$t('agents.tool_skills')}</label>
        <input bind:value={toolSkills} placeholder={$t('agents.tool_skills_placeholder')} />
      </div>
      <div class="field">
        <label>{$t('agents.mention_as')}</label>
        <input bind:value={mentionAs} placeholder={$t('agents.mention_as_placeholder')} />
        <small>{$t('agents.mention_as_hint')}</small>
      </div>
      <div class="actions">
        <button type="button" class="btn-secondary" onclick={onCancel}>{$t('common.cancel')}</button>
        <button type="submit" class="btn-primary">{$t('common.save')}</button>
      </div>
    </form>
  </div>
</div>

<style>
  .modal-backdrop {
    position: fixed; inset: 0; background: rgba(0,0,0,0.5);
    display: flex; align-items: center; justify-content: center; z-index: 1000;
  }
  .modal {
    background: var(--bg-container); border-radius: 12px; padding: 24px;
    max-width: 480px; width: 90%; max-height: 80vh; overflow-y: auto;
  }
  h3 { margin: 0 0 20px; font-size: 18px; font-weight: 600; }
  .field { margin-bottom: 16px; }
  label { display: block; font-size: 12px; font-weight: 500; color: var(--text-secondary); margin-bottom: 4px; }
  input, textarea {
    width: 100%; padding: 8px 12px; border: 1px solid var(--border-secondary);
    border-radius: 6px; background: var(--bg-primary); color: var(--text-primary);
    font-size: 13px; font-family: inherit;
    &:focus { outline: none; border-color: var(--blue-6); }
  }
  small { display: block; font-size: 11px; color: var(--text-tertiary); margin-top: 2px; }
  .actions { display: flex; gap: 8px; justify-content: flex-end; margin-top: 20px; }
  .btn-primary {
    height: 32px; padding: 0 14px; border: none; background: var(--blue-6); border-radius: 6px;
    font-size: 14px; color: #fff; cursor: pointer; font-family: inherit;
  }
  .btn-primary:hover:not(:disabled) { background: var(--blue-5); }
  .btn-primary:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-secondary {
    height: 32px; padding: 0 14px; border: 1px solid var(--border); background: var(--bg-container);
    border-radius: 6px; font-size: 13px; color: var(--text-secondary); cursor: pointer; font-family: inherit;
  }
  .btn-secondary:hover:not(:disabled) { border-color: var(--blue-5); color: var(--blue-5); }
  .btn-secondary:disabled { opacity: 0.5; cursor: not-allowed; }
</style>
