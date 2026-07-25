<script lang="ts">
  import { onMount } from 'svelte'
  import { t } from '../lib/i18n'
  import * as api from '../lib/api'

  interface Props {
    agent: api.Agent | null
    onSave: (agent: api.Agent) => void
    onCancel: () => void
  }

  let { agent, onSave, onCancel }: Props = $props()

  let name = $state('')
  let description = $state('')
  let model = $state('')
  let systemPrompt = $state('')

  // Channel binding state.
  interface BindEntry {
    platform: string
    adapter_id: string
    chat_id: string
  }
  let bindings: BindEntry[] = $state([])
  let availableBots: { platform: string; adapter_id: string; label: string }[] = $state([])

  // Checklist items loaded from the Default Agent's resource pool.
  interface ChecklistItem {
    name: string
    description: string
  }
  let availableTools: ChecklistItem[] = $state([])
  let availableSkills: ChecklistItem[] = $state([])

  // Selected tool/skill names (from the agent's current allowlists).
  let selectedTools: Set<string> = $state(new Set<string>())
  let selectedSkills: Set<string> = $state(new Set<string>())

  // Load available resources from the Default Agent pool.
  async function loadResources() {
    try {
      const [toolDefs, skillDefs, channelList] = await Promise.all([
        api.fetchAvailableTools(),
        api.listSkills(),
        api.listChannels(),
      ])
      availableTools = toolDefs.map(t => ({
        name: t.name,
        description: t.description || '',
      }))
      availableSkills = skillDefs.map(s => ({
        name: s.name,
        description: s.desc || '',
      }))
      availableBots = channelList
        .filter(c => c.enabled)
        .map(c => ({
          platform: c.platform,
          adapter_id: c.adapter_id || c.platform,
          label: c.adapter_id && c.adapter_id !== c.platform ? `${c.platform} / ${c.adapter_id}` : c.platform,
        }))
    } catch { /* degrade gracefully */ }
  }

  // Reset form fields when the agent prop changes.
  $effect(() => {
    name = agent?.name ?? ''
    description = agent?.description ?? ''
    model = agent?.model ?? ''
    systemPrompt = agent?.system_prompt ?? ''
    selectedTools = new Set(agent?.tools ?? [])
    selectedSkills = new Set(agent?.tool_skills ?? [])
    bindings = (agent?.channel_bindings ?? []).map(b => ({
      platform: b.platform,
      adapter_id: b.adapter_id || '',
      chat_id: b.chat_id,
    }))
  })

  onMount(loadResources)

  function toggleTool(name: string) {
    const next = new Set(selectedTools)
    if (next.has(name)) next.delete(name)
    else next.add(name)
    selectedTools = next
  }

  function toggleSkill(name: string) {
    const next = new Set(selectedSkills)
    if (next.has(name)) next.delete(name)
    else next.add(name)
    selectedSkills = next
  }

  function addBinding() {
    bindings = [...bindings, { platform: '', adapter_id: '', chat_id: '' }]
  }

  function removeBinding(i: number) {
    bindings = bindings.filter((_, idx) => idx !== i)
  }

  function handleSubmit(e: Event) {
    e.preventDefault()
    const selTools = [...selectedTools]
    const selSkills = [...selectedSkills]
    const body: any = {
      name: name.trim(),
      description: description.trim(),
      model: model.trim() || undefined,
      tools: selTools.length > 0 ? selTools : undefined,
      tool_skills: selSkills.length > 0 ? selSkills : undefined,
      system_prompt: systemPrompt.trim() || undefined,
      channel_bindings: bindings.filter(b => b.platform && b.chat_id).map(b => ({
        platform: b.platform,
        adapter_id: b.adapter_id || undefined,
        chat_id: b.chat_id,
      })),
    }
    if (!body.name || !body.description) {
      alert('Name and description are required')
      return
    }
    onSave(body as api.Agent)
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

      <!-- Tools checklist -->
      <div class="field">
        <label>{$t('agents.tools')}</label>
        {#if availableTools.length > 0}
          <div class="checklist">
            {#each availableTools as t}
              <label class="check-item" title={t.description}>
                <input
                  type="checkbox"
                  checked={selectedTools.has(t.name)}
                  onchange={() => toggleTool(t.name)}
                />
                <span class="check-name">{t.name}</span>
                <span class="check-hint">{t.description}</span>
              </label>
            {/each}
          </div>
        {:else}
          <small>{$t('agents.tools_loading')}</small>
        {/if}
      </div>

      <!-- Skills checklist -->
      <div class="field">
        <label>{$t('agents.tool_skills')}</label>
        {#if availableSkills.length > 0}
          <div class="checklist">
            {#each availableSkills as s}
              <label class="check-item" title={s.description}>
                <input
                  type="checkbox"
                  checked={selectedSkills.has(s.name)}
                  onchange={() => toggleSkill(s.name)}
                />
                <span class="check-name">{s.name}</span>
                <span class="check-hint">{s.description}</span>
              </label>
            {/each}
          </div>
        {:else}
          <small>{$t('agents.skills_loading')}</small>
        {/if}
      </div>

      <!-- Channel bindings -->
      <div class="field">
        <label>{$t('agents.channel_bindings')}</label>
        {#each bindings as b, i}
          <div class="bind-row">
            <select
              class="bind-select"
              value={`${b.platform}|${b.adapter_id || b.platform}`}
              onchange={(e) => {
                const val = (e.target as HTMLSelectElement).value
                const parts = val.split('|')
                bindings[i] = { ...bindings[i], platform: parts[0], adapter_id: parts[1] || '' }
              }}
            >
              <option value="">{$t('agents.select_platform')}</option>
              {#each availableBots as bot}
                <option value={`${bot.platform}|${bot.adapter_id}`}>{bot.label}</option>
              {/each}
            </select>
            <input
              bind:value={bindings[i].chat_id}
              placeholder={$t('agents.chat_id_placeholder')}
              class="bind-input"
            />
            <button type="button" class="btn-secondary bind-del" onclick={() => removeBinding(i)} title={$t('common.delete')}>
              <iconify-icon icon="ant-design:delete-outlined" width="12"></iconify-icon>
            </button>
          </div>
        {/each}
        <button type="button" class="btn-secondary" style="margin-top:6px" onclick={addBinding}>
          <iconify-icon icon="ant-design:plus-outlined" width="12"></iconify-icon>
          {$t('agents.add_binding')}
        </button>
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
    max-width: 600px; width: 90%; max-height: 80vh; overflow-y: auto;
  }
  h3 { margin: 0 0 20px; font-size: 18px; font-weight: 600; }
  .field { margin-bottom: 16px; }
  label { display: block; font-size: 12px; font-weight: 500; color: var(--text-secondary); margin-bottom: 4px; }
  input, textarea {
    width: 100%; padding: 8px 12px; border: 1px solid var(--border-secondary);
    border-radius: 6px; background: var(--bg-primary); color: var(--text-primary);
    font-size: 13px; font-family: inherit;
  }
  input:focus, textarea:focus { outline: none; border-color: var(--blue-6); }
  small { display: block; font-size: 11px; color: var(--text-tertiary); margin-top: 2px; }

  /* ── checklist ──────────────────────────────────────────────────────────── */
  .checklist {
    border: 1px solid var(--border-secondary); border-radius: 8px;
    max-height: 200px; overflow-y: auto; padding: 4px;
    background: var(--bg-primary);
  }
  .check-item {
    display: flex; align-items: center; gap: 8px;
    padding: 6px 8px; border-radius: 5px; cursor: pointer;
    font-size: 13px;
  }
  .check-item:hover { background: var(--hover-neutral); }
  .check-item input[type="checkbox"] { width: auto; margin: 0; flex: 0 0 auto; }
  .check-name {
    flex: 0 0 auto; min-width: 110px;
    font-family: var(--font-mono, 'SF Mono', Monaco, monospace);
    font-size: 12px; color: var(--text-primary);
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .check-hint {
    flex: 1; min-width: 0;
    font-size: 11px; color: var(--text-tertiary);
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }

  /* ── bindings ────────────────────────────────────────────────────────────── */
  .bind-row {
    display: flex; gap: 6px; align-items: center; margin-bottom: 6px;
  }
  .bind-select {
    flex: 0 0 140px; height: 30px; padding: 0 8px;
    border: 1px solid var(--border-secondary); border-radius: 6px;
    background: var(--bg-primary); color: var(--text-primary); font-size: 12px;
    font-family: inherit;
  }
  .bind-input {
    flex: 1; height: 30px; padding: 0 10px;
    border: 1px solid var(--border-secondary); border-radius: 6px;
    font-size: 12px; font-family: inherit;
  }
  .bind-del {
    flex: 0 0 30px; width: 30px; height: 30px; padding: 0;
    display: flex; align-items: center; justify-content: center;
  }

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
