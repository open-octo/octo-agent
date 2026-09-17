// Offline icons.
//
// The UI used to pull the Iconify web component from code.iconify.design and
// let it fetch each icon's path data from Iconify's public API on demand. Two
// problems with that: it is an unprompted third-party request from a product
// that advertises self-hosting and zero telemetry, and every icon rendered
// blank on an isolated network. Both go away by carrying the icons.
//
// Importing `iconify-icon` registers the <iconify-icon> custom element from
// node_modules instead of the CDN. icons.generated.ts carries the path data
// for every icon this repo names (see scripts/gen-icons.mjs).
import { _api, addCollection } from 'iconify-icon'
import { iconCollections } from './icons.generated'

/** initIcons registers the bundled icon data and closes the network path. */
export function initIcons(): void {
  for (const collection of iconCollections) addCollection(collection)

  // Belt and braces. addCollection covers every icon the repo names, so
  // nothing should ever need loading — but an agent profile's `icon:` is
  // user-authored YAML and can name anything, and the component's reflex for
  // an unknown icon is to ask the API for it. Replacing the fetch
  // implementation makes that impossible for any prefix, bundled or not:
  // an unknown icon renders as nothing, which is what it did offline anyway.
  _api.setFetch((async () => {
    throw new Error('iconify: icon data is bundled; the API is not reachable by design')
  }) as unknown as typeof fetch)
}
