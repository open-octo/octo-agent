import { vitePreprocess } from '@sveltejs/vite-plugin-svelte'

export default {
  preprocess: vitePreprocess(),
  compilerOptions: {
    warningFilter: (w) => !w.code.startsWith('a11y_'),
  },
}
