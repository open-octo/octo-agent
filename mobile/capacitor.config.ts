import type { CapacitorConfig } from '@capacitor/cli'

// webDir is the bundled octo web frontend, produced by `npm run bundle-web`,
// which copies internal/server/webdist into www/. It is a build artifact (kept
// out of git). The app serves it from capacitor://localhost (iOS) or
// https://localhost (Android); the local shim (src/shim.ts) points its /api +
// /ws calls at the user's own octo serve.
const config: CapacitorConfig = {
  appId: 'dev.octo.mobile',
  appName: 'octo',
  webDir: 'www',
  android: {
    // The server is often plain http (a LAN IP, a Tailscale 100.x address),
    // and the bundle is served from https://localhost, so those calls are
    // mixed content. Allowing it is what makes http servers reachable at all.
    allowMixedContent: true,
  },
}

export default config
