<script lang="ts">
  import { PACKS, getPack, setPack } from '../../lib/theme'
  import { t } from '../../lib/i18n'

  let selected = $state(getPack())

  function pick(id: string) {
    selected = id
    setPack(id)
  }
</script>

<div class="packs" role="radiogroup" aria-label={$t('settings.pack')}>
  {#each PACKS as pack}
    <button
      class="pack"
      class:active={selected === pack.id}
      role="radio"
      aria-checked={selected === pack.id}
      title={$t(pack.labelKey)}
      onclick={() => pick(pack.id)}
    >
      <span
        class="swatch"
        style="--sw-accent: {pack.swatch[0]}; --sw-surface: {pack.swatch[1]}"
      ></span>
      <span class="name">{$t(pack.labelKey)}</span>
    </button>
  {/each}
</div>

<style>
/* A fixed 2x2 grid rather than a wrapping row: the settings rows leave the
   control about 280px, which fits three pills and drops the fourth onto a
   ragged second line. Two even columns read as one block at any width. */
.packs {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 6px;
  flex: 0 0 auto;
}

.pack {
  display: flex; align-items: center; gap: 7px;
  padding: 5px 11px 5px 6px;
  border: 1px solid var(--border);
  border-radius: var(--radius-pill);
  background: var(--bg-container);
  color: var(--text-secondary);
  font-family: inherit;
  font-size: 12px;
  cursor: pointer;
  transition: border-color 0.15s, color 0.15s, background 0.15s;
}
.pack:hover { background: var(--hover-neutral); }
.pack.active {
  border-color: var(--blue-6);
  color: var(--text);
  background: var(--active-blue-bg);
}

/* Accent over surface, split on the diagonal — enough of each pack's palette
   to tell them apart at 18px without applying one to find out. */
.swatch {
  width: 18px; height: 18px; flex: 0 0 auto;
  border-radius: 50%;
  border: 1px solid var(--border);
  background: linear-gradient(135deg, var(--sw-accent) 0 50%, var(--sw-surface) 50% 100%);
}

.name { white-space: nowrap; }
</style>
