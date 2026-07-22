// Copies the built web frontend (internal/server/webdist) into www/, the
// Capacitor webDir. Run `make web-build` at the repo root first to produce
// webdist. www/ is a build artifact and is gitignored.
import { cp, rm, stat } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const src = fileURLToPath(new URL('../../internal/server/webdist', import.meta.url))
const dst = fileURLToPath(new URL('../www', import.meta.url))

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

await rm(dst, { recursive: true, force: true })
await cp(src, dst, { recursive: true })
console.log(`bundle-web: copied webdist -> ${dst}`)
