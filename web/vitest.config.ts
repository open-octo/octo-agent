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
    // Without this Vitest resolves Svelte's server build and mount() throws
    // lifecycle_function_unavailable, which is why components had no render
    // tests before. Verified against the full suite when it was added.
    conditions: ['browser'],
    alias: {
      '@lib': fileURLToPath(new URL('./src/lib', import.meta.url)),
    },
  },
  test: {
    // jsdom, not happy-dom: happy-dom's parser/serializer is not spec-compliant
    // enough for structural assertions — DOMPurify under it drops every
    // outermost <div>, and DOMParser round-trips reshape documents in ways real
    // browsers don't (#1897). jsdom keeps sanitized and re-serialized DOM close
    // to what ships.
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    globals: true,
    include: ['src/**/*.test.ts'],
  },
})
