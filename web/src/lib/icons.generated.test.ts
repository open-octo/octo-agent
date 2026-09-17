// @vitest-environment node
//
// Guards the offline icon bundle against drift. The generated file is
// committed so dev and vitest work without running the generator, which means
// it can fall behind the source tree — and a missing icon fails silently, as a
// blank space, exactly the failure mode this whole change exists to remove.
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { buildBundle, collectCandidates } from '../../scripts/gen-icons.mjs'
import { iconCollections } from './icons.generated'

describe('bundled icon data', () => {
  it('is up to date with the source tree', () => {
    const { resolved } = buildBundle()
    // Compared as JSON: this is generated data, and the point is byte equality
    // with what a fresh `npm run gen:icons` would write.
    expect(JSON.stringify(resolved)).toBe(JSON.stringify(iconCollections))
  })

  it('resolves every icon name the repo actually uses', () => {
    const { missing } = buildBundle()
    // A candidate that resolves to nothing is either a false positive from the
    // loose regex (some other "word:word" string) or a real typo in an icon
    // name — which would render blank. Anything left here needs a look.
    expect(missing).toEqual([])
  })

  it('scans every place an icon name can be written', () => {
    // The generator only searches three roots. If markup somewhere else starts
    // naming icons, those render blank with no warning — so pin the coverage
    // this bundle was built on.
    const prefixes = collectCandidates()
    expect(prefixes.get('ant-design')?.size).toBeGreaterThan(50)
    expect(prefixes.get('lucide')?.size).toBeGreaterThan(5)
  })

  it('leaves no CDN reference in the page shell', () => {
    // The whole point: index.html must not pull the web component from
    // code.iconify.design any more.
    const html = readFileSync(
      fileURLToPath(new URL('../../index.html', import.meta.url)),
      'utf8',
    )
    expect(html).not.toContain('iconify.design')
    expect(html).not.toMatch(/<script[^>]+src="https?:/)
  })
})
