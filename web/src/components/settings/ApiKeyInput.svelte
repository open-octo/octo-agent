<script lang="ts">
  import { t } from '../../lib/i18n'

  // Password input with a show/hide eye toggle, shared by the model config
  // form and the endpoint editor. Visibility state lives here — both callers
  // mount fresh per modal open, so it resets naturally.
  let {
    value = $bindable(''),
    placeholder = '',
    disabled = false,
  }: {
    value?: string
    placeholder?: string
    disabled?: boolean
  } = $props()

  let show = $state(false)
</script>

<div class="key-row">
  <input
    class="key-input"
    type={show ? 'text' : 'password'}
    {placeholder}
    bind:value
    {disabled}
  />
  <button type="button" class="key-toggle" onclick={() => (show = !show)} title={show ? $t('common.hide') : $t('common.show')}>
    <iconify-icon icon={show ? 'ant-design:eye-invisible-outlined' : 'ant-design:eye-outlined'} width="15"></iconify-icon>
  </button>
</div>

<style>
.key-row { display: flex; align-items: center; gap: 8px; }
.key-input {
  flex: 1; min-width: 0; height: 36px; padding: 0 12px;
  border: 1px solid var(--border); border-radius: 8px; font-size: 13px;
  color: var(--text); background: var(--bg-container); outline: none;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  transition: border-color 0.15s, box-shadow 0.15s;
}
.key-input:focus { border-color: var(--blue-6); box-shadow: 0 0 0 3px var(--active-blue-bg); }
.key-input:disabled { background: var(--bg-table-header); cursor: not-allowed; }
.key-toggle {
  width: 36px; height: 36px; flex: 0 0 36px; border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 8px; display: flex; align-items: center; justify-content: center;
  cursor: pointer; color: var(--text-tertiary);
}
.key-toggle:hover { border-color: var(--blue-5); color: var(--blue-5); }
</style>
