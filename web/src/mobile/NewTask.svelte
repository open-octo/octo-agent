<script lang="ts">
  // New-task sheet, opened by the feed's FAB. The phone fires a task in one
  // step: describe it, optionally pick model / permission mode / working
  // directory, and land in the streaming chat detail. Leaving the prompt
  // empty just creates a blank session (the old FAB behavior).
  import { get } from 'svelte/store'
  import { sessions, activeSessionId, activeSession, globalPermissionMode, showToast } from '../lib/stores'
  import * as api from '../lib/api'
  import { t, tr } from '../lib/i18n'

  let { onCancel, onCreated }: {
    onCancel: () => void
    onCreated: (id: string, prompt: string) => void
  } = $props()

  let prompt = $state('')
  let dir = $state('')
  let creating = $state(false)

  // '' = the server default sender; anything else is the composite
  // "<endpoint>::<model>" id updateSessionModel resolves.
  let modelId = $state('')
  let models = $state<{ id: string; label: string }[]>([])
  $effect(() => {
    api.getEndpoints()
      .then(d => {
        const flat: { id: string; label: string }[] = []
        for (const e of d.endpoints ?? []) {
          for (const m of e.models ?? []) flat.push({ id: `${e.id}::${m.model}`, label: m.model })
        }
        // The same model on two endpoints would render two identical options;
        // qualify duplicates with their endpoint id.
        const seen = new Map<string, number>()
        for (const m of flat) seen.set(m.label, (seen.get(m.label) ?? 0) + 1)
        models = flat.map(m => (seen.get(m.label)! > 1 ? { ...m, label: `${m.label} · ${m.id.split('::')[0]}` } : m))
      })
      .catch(() => {})
  })

  // globalPermissionMode is seeded 'ask' and only overwritten when config.yml
  // sets one of the engine modes — normalize before using it as the segment
  // default and the skip-if-unchanged baseline.
  const g = get(globalPermissionMode)
  const basePermMode = ['interactive', 'auto', 'strict'].includes(g) ? g : 'interactive'
  let permMode = $state(basePermMode)
  const permModes = [
    { m: 'interactive', key: 'm.perm_ask' },
    { m: 'auto', key: 'm.perm_auto' },
    { m: 'strict', key: 'm.perm_strict' },
  ]

  async function create() {
    if (creating) return
    creating = true
    try {
      const sess = await api.createSession({ source: 'manual' })
      sessions.update(ss => [sess, ...ss])
      activeSessionId.set(sess.id)
      activeSession.set(sess.id)
      // Best-effort per-session overrides: a failed override shouldn't strand
      // the already-created session, so toast and continue.
      if (modelId) {
        await api.updateSessionModel(sess.id, modelId)
          .then(res => sessions.update(list =>
            list.map(s => (s.id === sess.id ? { ...s, model: res.model, model_id: res.model_id } : s))))
          .catch((e: any) => showToast(e?.message ?? tr('m.model_fail'), 'error'))
      }
      if (permMode !== basePermMode) {
        await api.updateSessionPermissionMode(sess.id, permMode).catch((e: any) =>
          showToast(e?.message ?? tr('m.perm_fail'), 'error'))
      }
      if (dir.trim()) {
        await api.updateSessionWorkingDir(sess.id, dir.trim()).catch((e: any) =>
          showToast(e?.message ?? tr('m.dir_fail'), 'error'))
      }
      onCreated(sess.id, prompt.trim())
    } catch (e: any) {
      showToast(e?.message ?? tr('m.create_fail'), 'error')
      creating = false
    }
  }
</script>

<header class="head">
  <button class="back" aria-label={$t('m.cancel')} disabled={creating} onclick={onCancel}>
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2"><path d="M15 18l-6-6 6-6"/></svg>
  </button>
  <h1>{$t('m.new_task')}</h1>
</header>

<div class="scroll">
  <p class="lbl">{$t('m.task_desc')}</p>
  <div class="card">
    <textarea
      class="prompt"
      rows="5"
      placeholder={$t('m.task_ph')}
      aria-label={$t('m.task_desc')}
      bind:value={prompt}
    ></textarea>
  </div>

  <p class="lbl">{$t('m.model')}</p>
  <div class="card pad">
    <select class="select" bind:value={modelId} aria-label={$t('m.model')}>
      <option value="">{$t('m.default')}</option>
      {#each models as m (m.id)}
        <option value={m.id}>{m.label}</option>
      {/each}
    </select>
  </div>

  <p class="lbl">{$t('m.perm_mode')}</p>
  <div class="card pad">
    <div class="seg">
      {#each permModes as p (p.m)}
        <button class="segi" class:on={permMode === p.m} aria-pressed={permMode === p.m} onclick={() => (permMode = p.m)}>{$t(p.key)}</button>
      {/each}
    </div>
    <p class="note">
      {permMode === 'auto' ? $t('m.perm_note_auto')
        : permMode === 'strict' ? $t('m.perm_note_strict')
        : $t('m.perm_note_ask')}
    </p>
  </div>

  <p class="lbl">{$t('m.workdir')}</p>
  <div class="card pad">
    <input class="input" type="text" placeholder={$t('m.server_default')} bind:value={dir} aria-label={$t('m.workdir')} />
  </div>

  <button class="go" disabled={creating} onclick={create}>
    {creating ? $t('m.creating') : prompt.trim() ? $t('m.create_start') : $t('m.create_blank')}
  </button>
</div>

<style>
  .head { flex: none; display: flex; align-items: center; gap: 10px; padding: 8px 18px 12px; }
  .head h1 { margin: 0; font-size: 24px; font-weight: 600; color: var(--m-text-strong); }
  .back {
    width: 34px; height: 34px; border-radius: 50%; border: none; flex: none;
    background: var(--m-surface-2); color: var(--m-text); cursor: pointer;
    display: flex; align-items: center; justify-content: center;
  }
  .scroll { flex: 1; min-height: 0; overflow-y: auto; -webkit-overflow-scrolling: touch; padding: 0 16px 24px; }

  .lbl { margin: 2px 2px 8px; font: 600 12px/1 system-ui; letter-spacing: .5px; text-transform: uppercase; color: var(--m-text-3); }
  .card { background: var(--m-surface); border-radius: 14px; box-shadow: var(--m-shadow-card); margin-bottom: 18px; overflow: hidden; }
  .card.pad { padding: 12px 16px; }

  .prompt {
    display: block; width: 100%; border: none; resize: none; padding: 14px 16px;
    background: none; font: inherit; font-size: 15px; color: var(--m-text);
    outline: none;
  }
  .prompt::placeholder { color: var(--m-text-4); }

  .select, .input {
    display: block; width: 100%; border: none; background: none; outline: none;
    font: inherit; font-size: 14.5px; color: var(--m-text); padding: 2px 0;
  }
  .input::placeholder { color: var(--m-text-4); }

  .seg { display: flex; gap: 4px; background: var(--m-bg); border-radius: 8px; padding: 3px; }
  .segi {
    flex: 1; text-align: center; padding: 8px 0; border-radius: 6px; border: none;
    background: none; font-size: 13px; color: var(--m-text-2); font-family: inherit; cursor: pointer;
  }
  .segi.on { background: var(--m-accent); color: #fff; font-weight: 600; }
  .note { margin: 10px 2px 0; font-size: 12px; color: var(--m-text-3); }

  .go {
    display: block; width: 100%; border: none; border-radius: 12px; padding: 14px 0;
    background: var(--m-accent); color: #fff; font: 600 15px/1 inherit; font-family: inherit;
    cursor: pointer;
  }
  .go:disabled { opacity: .6; }
</style>
