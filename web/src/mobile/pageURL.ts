// Page URLs on the phone. The server hands out paths (/_artifacts/<token>/,
// /api/...) relative to its own origin; in the app the frontend's origin is the
// WebView's (capacitor://localhost, https://localhost), so a frame src or an
// <img> must name the server outright. The app's boot layer publishes it as
// window.octoMobile.server (mobile/src/boot.ts); a phone browser reaching
// `octo serve` directly has none, and the path resolves against the page.
export function serverURL(path: string): string {
  const base = (globalThis as { octoMobile?: { server?: string } }).octoMobile?.server ?? ''
  return base + path
}

// The page URL a frame loads: the theme rides the query (the artifact-design
// contract the desktop frame follows too), and `v` changes when the agent
// rewrites the file so the frame reloads.
export function pageSrc(path: string, rev = 0): string {
  const theme = document.documentElement.getAttribute('data-theme') === 'dark' ? 'dark' : 'light'
  return `${serverURL(path)}?theme=${theme}&v=${rev}`
}
