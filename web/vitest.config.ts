/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import { fileURLToPath } from 'node:url'

// Mirrors the production vite.config.ts svelte plugin so .svelte files compile
// identically under vitest. The build options (outDir, emptyOutDir) are omitted —
// tests never emit.
export default defineConfig({
  plugins: [svelte()],
  resolve: {
    alias: {
      '@lib': fileURLToPath(new URL('./src/lib', import.meta.url)),
    },
  },
  test: {
    environment: 'happy-dom',
    setupFiles: ['./src/test/setup.ts'],
    globals: true,
    include: ['src/**/*.test.ts'],
  },
})
