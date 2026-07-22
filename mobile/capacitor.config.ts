import type { CapacitorConfig } from '@capacitor/cli'

// webDir is the bundled octo web frontend, produced by `npm run bundle-web`,
// which copies internal/server/webdist into www/. It is a build artifact (kept
// out of git). The app serves it from capacitor://localhost so the frontend
// keeps its same-origin assumptions; the local shim (src/shim.ts) tunnels its
// /api + /ws out to the remote octo serve.
const config: CapacitorConfig = {
  appId: 'dev.octo.mobile',
  appName: 'octo',
  webDir: 'www',
}

export default config
