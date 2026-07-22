// Produces www/, the Capacitor webDir, from two inputs:
//   1. the built octo web frontend (internal/server/webdist) — copied in as-is,
//   2. the mobile boot layer (src/boot.ts) — bundled to www/octo-boot.js and
//      injected as the first <head> script of the frontend's index.html, so it
//      installs the local shim before any frontend module runs.
// Run `make web-build` at the repo root first to produce webdist. www/ is a
// build artifact and is gitignored.
import { cp, rm, stat, readFile, writeFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { build } from 'esbuild'

const src = fileURLToPath(new URL('../../internal/server/webdist', import.meta.url))
const dst = fileURLToPath(new URL('../www', import.meta.url))
const boot = fileURLToPath(new URL('../src/boot.ts', import.meta.url))
const bootOut = fileURLToPath(new URL('../www/octo-boot.js', import.meta.url))
const indexHtml = fileURLToPath(new URL('../www/index.html', import.meta.url))

try {
  const s = await stat(src)
  if (!s.isDirectory()) throw new Error('not a directory')
} catch {
  console.error(
    `bundle-web: ${src} not found.\n` +
      'Build the web frontend first: run `make web-build` at the repo root.',
  )
  process.exit(1)
}

// 1. Copy the frontend in.
await rm(dst, { recursive: true, force: true })
await cp(src, dst, { recursive: true })

// 2. Bundle the boot layer as a classic (IIFE) script so it runs synchronously
//    ahead of the frontend's deferred module scripts.
await build({
  entryPoints: [boot],
  bundle: true,
  format: 'iife',
  platform: 'browser',
  target: 'es2020',
  outfile: bootOut,
  legalComments: 'none',
})

// 3. Inject the boot script as the first child of <head>.
const TAG = '<script src="./octo-boot.js"></script>'
let html = await readFile(indexHtml, 'utf8')
if (!html.includes('octo-boot.js')) {
  if (/<head[^>]*>/i.test(html)) {
    html = html.replace(/(<head[^>]*>)/i, `$1${TAG}`)
  } else {
    // No <head>: prepend the tag so it still runs before the app.
    html = TAG + html
  }
  await writeFile(indexHtml, html)
}

console.log(`bundle-web: webdist + octo-boot.js -> ${dst}`)
