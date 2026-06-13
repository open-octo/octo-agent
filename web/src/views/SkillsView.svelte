<script lang="ts">
  import { skills, showToast, view, sessions, activeSessionId } from '../lib/stores'
  import * as api from '../lib/api'
  import StatusTag from '../components/ui/StatusTag.svelte'
  import Switch from '../components/ui/Switch.svelte'
  import Segment from '../components/ui/Segment.svelte'

  let loading = $state(true)
  let scope = $state('My Skills')
  let showSystem = $state(false)
  let fileInput: HTMLInputElement

  $effect(() => {
    api.listSkills()
      .then(list => skills.set(list))
      .catch(err => showToast(err.message, 'error'))
      .finally(() => { loading = false })
  })

  async function handleToggle(name: string, currentEnabled: boolean) {
    const next = !currentEnabled
    try {
      await api.toggleSkill(name, next)
      skills.update(list => list.map(s => s.name === name ? { ...s, enabled: next } : s))
    } catch (err: any) {
      showToast(err.message, 'error')
    }
  }

  async function handleDelete(name: string) {
    if (!confirm(`Delete skill "${name}"?`)) return
    try {
      await api.deleteSkill(name)
      skills.update(list => list.filter(s => s.name !== name))
      showToast(`Skill "${name}" deleted`)
    } catch (err: any) {
      showToast(err.message, 'error')
    }
  }

  function handleCreateSkill() {
    view.set('chat')
  }

  // Export downloads the skill folder as a .zip from the server.
  function handleExport(name: string) {
    const a = document.createElement('a')
    a.href = `/api/skills/${encodeURIComponent(name)}/export`
    a.download = `${name}.zip`
    document.body.appendChild(a)
    a.click()
    a.remove()
  }

  // Editing a skill's files is agent-assisted: open a session pointed at it.
  // (There is no in-browser file editor; the agent edits the skill on disk.)
  async function handleEdit(name: string) {
    try {
      const sess = await api.createSession({ name: `Edit skill: ${name}` })
      sessions.update(s => [sess, ...s])
      activeSessionId.set(sess.id)
      view.set('chat')
    } catch (e: any) {
      showToast(e.message ?? 'Could not open session', 'error')
    }
  }

  function handleImportClick() {
    fileInput.value = ''
    fileInput.click()
  }

  async function handleFileChange(e: Event) {
    const file = (e.target as HTMLInputElement).files?.[0]
    if (!file) return
    try {
      await api.importSkill(file)
      const list = await api.listSkills()
      skills.set(list)
      showToast(`Skill imported from "${file.name}"`)
    } catch (err: any) {
      showToast(err.message, 'error')
    }
  }
</script>

<div class="page">
  <div class="inner">
    <div class="page-header">
      <div class="title-block">
        <h2>Skills</h2>
        <p>Extend your assistant's capabilities with custom skills</p>
      </div>
      <div class="header-actions">
        <input
          bind:this={fileInput}
          type="file"
          accept=".zip,.md,.yaml,.yml"
          style="display:none"
          onchange={handleFileChange}
        />
        <button class="btn-secondary" onclick={handleImportClick}>Import</button>
        <button class="btn-primary" onclick={handleCreateSkill}>Create Skill</button>
      </div>
    </div>

    <div class="toolbar-row">
      <Segment options={['My Skills', 'System Skills']} bind:value={scope} />
      <div class="system-toggle">
        <Switch bind:checked={showSystem} />
        <span>Show system skills</span>
      </div>
    </div>

    <div class="table-card">
      <div class="table-header">
        <span>Skill</span>
        <span>Description</span>
        <span>Version</span>
        <span>Status</span>
        <span>Enabled</span>
        <span style="text-align:right">Actions</span>
      </div>

      {#if loading}
        <div class="empty-state">
          <div class="spinner"></div>
          <span>Loading skills…</span>
        </div>
      {:else if $skills.length === 0}
        <div class="empty-state">
          <span>No skills found. Create or import one to get started.</span>
        </div>
      {:else}
        {#each $skills as sk (sk.name)}
          <div class="table-row">
            <div class="skill-name-cell">
              <span class="skill-icon">
                <iconify-icon icon={sk.icon} width="14"></iconify-icon>
              </span>
              <span class="mono name">{sk.name}</span>
            </div>
            <span class="desc">{sk.desc}</span>
            <span class="mono ver">{sk.version}</span>
            <span><StatusTag status={sk.tagStatus}>{sk.tagLabel}</StatusTag></span>
            <span>
              <Switch
                checked={sk.enabled}
                onchange={() => handleToggle(sk.name, sk.enabled)}
              />
            </span>
            <div class="row-actions">
              <button class="act-btn" title="Edit with agent" onclick={() => handleEdit(sk.name)}><iconify-icon icon="ant-design:edit-outlined" width="15"></iconify-icon></button>
              <button class="act-btn" title="Export as .zip" onclick={() => handleExport(sk.name)}><iconify-icon icon="ant-design:export-outlined" width="15"></iconify-icon></button>
              <button class="act-btn del" title="Delete" onclick={() => handleDelete(sk.name)}>
                <iconify-icon icon="ant-design:delete-outlined" width="15"></iconify-icon>
              </button>
            </div>
          </div>
        {/each}
      {/if}
    </div>
  </div>
</div>

<style>
.page { flex: 1; overflow-y: auto; min-height: 0; }
.inner { max-width: 1080px; margin: 0 auto; padding: 24px; display: flex; flex-direction: column; gap: 24px; }
.page-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
.title-block { display: flex; flex-direction: column; gap: 4px; }
h2 { margin: 0; font-size: 24px; font-weight: 600; color: #1F1F1F; }
p { margin: 0; font-size: 14px; color: rgba(0,0,0,0.65); }
.header-actions { display: flex; align-items: center; gap: 8px; }
.btn-primary { height: 32px; padding: 0 14px; border: none; background: #1677FF; border-radius: 6px; font-size: 14px; color: #fff; cursor: pointer; font-family: inherit; }
.btn-primary:hover { background: #4096FF; }
.btn-secondary { height: 32px; padding: 0 14px; border: 1px solid #D9D9D9; background: #fff; border-radius: 6px; font-size: 13px; color: rgba(0,0,0,0.65); cursor: pointer; font-family: inherit; }
.btn-secondary:hover { border-color: #4096FF; color: #4096FF; }
.toolbar-row { display: flex; align-items: center; justify-content: space-between; }
.system-toggle { display: flex; align-items: center; gap: 8px; font-size: 13px; color: rgba(0,0,0,0.65); }
.table-card { background: #fff; border-radius: 16px; box-shadow: 0 8px 24px rgba(15,23,42,0.03); overflow: hidden; }
.table-header, .table-row {
  display: grid;
  grid-template-columns: minmax(150px,2.2fr) minmax(120px,3fr) 76px 96px 72px 100px;
  column-gap: 12px; align-items: center; padding: 0 24px;
}
.table-header { height: 44px; background: #FAFAFA; font-size: 12px; font-weight: 600; color: rgba(0,0,0,0.65); border-bottom: 1px solid #F0F0F0; }
.table-row { padding: 12px 24px; border-bottom: 1px solid #F0F0F0; background: #fff; min-width: 680px; }
.table-row:last-child { border-bottom: none; }
.table-row:hover { background: rgba(22,119,255,0.06); }
.skill-name-cell { display: flex; align-items: center; gap: 10px; min-width: 0; }
.skill-icon {
  width: 28px; height: 28px; flex: 0 0 28px; border-radius: 9999px;
  background: #E6F4FF; color: #1677FF; display: flex; align-items: center; justify-content: center;
}
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.name { font-size: 14px; color: rgba(0,0,0,0.88); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.desc { font-size: 13px; color: rgba(0,0,0,0.65); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; padding-right: 16px; }
.ver { font-size: 13px; color: rgba(0,0,0,0.45); }
.row-actions { display: flex; align-items: center; justify-content: flex-end; gap: 4px; }
.act-btn {
  width: 28px; height: 28px; border: none; background: transparent; border-radius: 6px;
  display: flex; align-items: center; justify-content: center; cursor: pointer; color: rgba(0,0,0,0.45);
}
.act-btn:hover { background: rgba(0,0,0,0.04); color: #1677FF; }
.act-btn.del:hover { color: #FF4D4F; }
.empty-state {
  display: flex; align-items: center; justify-content: center; gap: 10px;
  padding: 48px 24px; font-size: 14px; color: rgba(0,0,0,0.45);
}
.spinner {
  width: 18px; height: 18px; border: 2px solid rgba(22,119,255,0.2);
  border-top-color: #1677FF; border-radius: 50%;
  animation: spin 0.6s linear infinite; flex: 0 0 18px;
}
@keyframes spin { to { transform: rotate(360deg); } }
</style>
