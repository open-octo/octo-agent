<script lang="ts">
  // Reusable octo brand mark — the same octopus head that appears in favicon.svg,
  // exposed as a Svelte component so the header and sidebar stay in sync.
  interface Props {
    size?: number | string
    class?: string
  }
  let { size = 26, class: className = '' }: Props = $props()
</script>

<svg
  class={className}
  width={size}
  height={size}
  viewBox="0 0 100 100"
  fill="none"
  xmlns="http://www.w3.org/2000/svg"
  aria-label="octo"
>
  <rect width="100" height="100" rx="22" fill="currentColor"/>
  <path fill="#ffffff" d="M50 18 C34 18 26 30 26 44 C26 54 32 60 36 62 C34 70 28 78 20 82 C18 83 18 86 20 88 C22 89 25 89 27 87 C33 83 39 76 42 68 C44 69 47 70 50 70 L50 82 C50 85 53 87 56 86 C58 85 59 83 58 81 L56 70 C59 70 62 69 64 68 C67 76 73 83 79 87 C81 89 84 89 86 88 C88 86 88 83 86 82 C78 78 72 70 70 62 C74 60 80 54 80 44 C80 30 72 18 56 18 Z"/>
  <circle cx="40" cy="42" r="5" fill="currentColor"/>
  <circle cx="60" cy="42" r="5" fill="currentColor"/>
</svg>
