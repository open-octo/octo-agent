import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

export default defineConfig({
  plugins: [svelte()],
  build: {
    outDir: '../internal/server/webdist',
    emptyOutDir: true,
    // The UI ships as one embedded bundle served from localhost; code-splitting buys nothing here.
    chunkSizeWarningLimit: 600,
  },
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:8088',
      '/ws':  { target: 'ws://localhost:8088', ws: true },
    },
  },
})
