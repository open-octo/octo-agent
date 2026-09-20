<script lang="ts">
  import { packs, getPack, setPack } from '../../lib/theme'
  import { t } from '../../lib/i18n'

  let selected = $state(getPack())

  function pick(id: string) {
    selected = id
    setPack(id)
  }
</script>

<!-- Plain buttons with aria-pressed rather than a radiogroup. role="radio"
     promises assistive tech arrow-key navigation and a single tab stop, which
     these do not implement, and it also erases the button's own role. The
     neighbouring Segment control is likewise a plain row of buttons, so this
     keeps the two consistent while still announcing which pack is active. -->
<div class="packs" aria-label={$t('settings.pack')}>
  {#each $packs as pack}
    <button
      class="pack"
      class:active={selected === pack.id}
      aria-pressed={selected === pack.id}
      title={pack.author ? `${pack.label} — ${pack.author}` : undefined}
      onclick={() => pick(pack.id)}
    >
      <span
        class="swatch"
        style="--sw-accent: {pack.swatch[0]}; --sw-surface: {pack.swatch[1]}"
      ></span>
      <span class="name">{pack.labelKey ? $t(pack.labelKey) : pack.label}</span>
    </button>
  {/each}
</div>

<style>
/* One unbroken row. The pills are sized down enough that even the longer
   English names fit; `nowrap` then keeps them on one line and lets the
   description beside them take the extra lines instead, which is the better
   place to spend the height. */
.packs {
  display: flex;
  flex-wrap: nowrap;
  gap: 5px;
  flex: 0 1 auto;
  /* User themes make this row unbounded — it holds four names when octo ships
     them all and any number once ~/.octo/themes is populated. Scrolling keeps
     the setting row's height fixed instead of pushing the modal apart. */
  overflow-x: auto;
  scrollbar-width: thin;
}

.pack {
  display: flex; align-items: center; gap: 5px;
  padding: 4px 9px 4px 4px;
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
   to tell them apart at this size without applying one to find out. */
.swatch {
  width: 16px; height: 16px; flex: 0 0 auto;
  border-radius: 50%;
  border: 1px solid var(--border);
  background: linear-gradient(135deg, var(--sw-accent) 0 50%, var(--sw-surface) 50% 100%);
}

.name { white-space: nowrap; }
</style>
