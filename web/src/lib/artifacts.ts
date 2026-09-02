// Artifacts panel data layer.
//
// Previewable files the agent writes ride the existing ui_payload stream: the
// write / edit / show_artifact tools each emit { type, path }. observeArtifact()
// picks those up (from both the live tool_result path and history replay) and
// pushes a metadata-only entry into the `artifacts` store that ArtifactsPanel
// renders. The body — fetched from the whitelisted GET
// /api/sessions/{id}/artifacts endpoint, and for markdown/HTML the preview
// document built from it — is produced lazily on first selection
// (hydrateArtifact), so history replay costs no network and a session's
// unopened artifacts hold no data: URIs.
//
// Constraint on every preview document built here: it must not reference
// /api/* — nothing inside the sandboxed srcdoc iframe can authenticate. The
// iframe has no allow-same-origin, so its subresource requests carry an opaque
// origin and count as cross-site; the SameSite=Strict access-key cookie is
// withheld and the endpoint answers 401 for every client that isn't exempt as
// loopback. It works on localhost and breaks over a tunnel or on the LAN.
//
// The same goes for any local file the artifact itself points at — a markdown
// `![](shot.png)` or an HTML `<img src="chart.png">` resolves against the host
// page rather than the file's own directory, and the /api/ path it would need
// can't authenticate from in there either. Two ways out: render outside the
// iframe (what `src` is for) or inline the bytes as a data: URI (what
// inlineLocalRefs does).

import { get, writable } from 'svelte/store'
import { artifacts, panelContent, panelExpanded, artifactSel } from './stores'
import { renderMarkdown } from './markdown'
import type { Artifact } from './types'

// The sandbox every srcdoc preview frame runs with — artifact previews (panel,
// modal, mobile) and saved Light Apps alike, since an app is exercised in the
// preview before it is saved and must behave the same afterwards.
//
// No allow-same-origin: the origin stays opaque, so the document reaches no
// storage, cookies or host state (the bridges in laStorage.ts / laDownload.ts
// exist because of this). No allow-popups, no allow-top-navigation.
//
// allow-forms: without it the submit event never fires at all — the sandboxed
// forms check runs before the event is dispatched — so <form onsubmit> plus
// Enter is silently dead. allow-modals: without it confirm() returns false,
// prompt() null and alert() no-ops, so a "sure?" before delete can never be
// answered yes. What allow-modals hands the document in return: native
// dialogs (alert/confirm/prompt, print(), the beforeunload prompt), which are
// tab-level and can block the host UI while open. Accepted: the app is one the
// user asked their own agent to write.
export const ARTIFACT_SANDBOX = 'allow-scripts allow-forms allow-modals'

// Tracks which session the current artifacts belong to, so an async fetch that
// resolves after a session switch is discarded instead of polluting the new view.
export const artifactSelSession = writable<string | null>(null)

type Kind = 'html' | 'markdown' | 'image'

// Only kinds the panel can render are artifacts. Source, config, and data
// files are deliberately absent: they are the routine bulk of a coding
// session, would bury the reports and pages the panel exists for, and the
// panel's Git Diff mode already shows code changes with context. Must match
// artifactContentTypes in internal/tools/artifact.go — a kind the client knows
// but the endpoint refuses makes the fetch 404 and the artifact silently
// vanish (#1895).
const EXT_KIND: Record<string, Kind> = {
  html: 'html', htm: 'html',
  md: 'markdown', markdown: 'markdown',
  png: 'image', jpg: 'image', jpeg: 'image', gif: 'image', svg: 'image', webp: 'image',
}

// Once-per-session guard so a live write auto-opens the panel only the first time.
let autoOpened = false

// How many times each image path has been observed this session, used as the
// cache-busting revision in its src. Cleared with the artifacts themselves.
const imageRevisions = new Map<string, number>()

function kindOf(path: string): Kind | null {
  const dot = path.lastIndexOf('.')
  if (dot < 0) return null
  return EXT_KIND[path.slice(dot + 1).toLowerCase()] ?? null
}

function basename(path: string): string {
  const norm = path.replace(/\\/g, '/')
  return norm.slice(norm.lastIndexOf('/') + 1)
}

function iconFor(kind: Kind): string {
  switch (kind) {
    case 'html':     return 'ant-design:html5-outlined'
    case 'markdown': return 'ant-design:file-markdown-outlined'
    case 'image':    return 'ant-design:file-image-outlined'
    default:         return 'ant-design:file-text-outlined'
  }
}

function typeLabel(kind: Kind): string {
  switch (kind) {
    case 'html':     return 'HTML'
    case 'markdown': return 'Markdown'
    case 'image':    return 'Image'
    default:         return 'File'
  }
}

// External scripts and stylesheets — <script src> / <link rel=stylesheet href>
// — are allowed only from the CDN allowlist below; a reference to any other
// host is stripped before a sandboxed frame renders the page, and the page
// renders without it under a banner saying so. Both frames that show
// agent-written HTML go through this (selfContainedDocument): the artifact
// preview and the Light App view, so a page's external references are treated
// the same wherever it is opened.
//
// Why an allowlist rather than fully open or fully closed: the sandbox itself
// could load any cross-origin https:// script, but an artifact must also
// render on a machine with a poor route to the wider internet — offline, on a
// LAN, over a tunnel, behind a national firewall — and must still render
// years later once saved as a Light App. Well-known CDNs (with mainland-China
// mirrors included) are the pragmatic middle: they unlock real libraries
// (React, ECharts, …) that can never be inlined by a model, while keeping the
// page's fate out of arbitrary hosts' hands. A local reference (`./style.css`,
// `app.js` beside the page) really can't load: the srcdoc frame resolves it
// against the host page, and the /api/ path it would need can't authenticate
// from an opaque origin (see the file-header note).
//
// The list must stay in sync with the guidance the model reads:
// internal/prompt/base.md (Light Apps constraints) and
// internal/skills/defaults/artifact-design/SKILL.md.
const CDN_ALLOWLIST = new Set([
  'cdnjs.cloudflare.com',
  'cdn.jsdelivr.net',
  'unpkg.com',
  'fonts.googleapis.com',
  'fonts.gstatic.com',
  // Mainland-China mirrors — the global CDNs above are flaky or blocked there.
  'cdn.bootcdn.net',
  'cdn.staticfile.org',
  'cdn.staticfile.net',
  'registry.npmmirror.com',
])

// Only an explicit https:// URL on an allowlisted host passes. URL parsing —
// not string prefixing — decides the host, so `https://cdn.jsdelivr.net@evil`
// and friends resolve to their real hostname and fail the lookup.
function isAllowedRef(url: string): boolean {
  let u: URL
  try {
    u = new URL(url.trim())
  } catch {
    return false
  }
  return u.protocol === 'https:' && CDN_ALLOWLIST.has(u.hostname.toLowerCase())
}

// Stripping beats refusing to render: a page with one disallowed link still
// shows its content, and the user can judge at a glance whether the design
// survived. Only <link> rel values the document needs in order to render
// count: a favicon, manifest, preconnect, or canonical link loads nothing the
// preview depends on and stays (#1896). preload rides along with stylesheet
// because the rel="preload" onload="this.rel='stylesheet'" idiom makes it one.
//
// The judgment runs on a parsed DOM, not on tag regexes: the reference that
// matters is the one the iframe's own HTML parser will fetch, and only a
// parser agrees with it on what that is. A string scan does not — a decoy
// `src=` inside another attribute's quoted value, a `>` inside a quoted
// value truncating the apparent tag, `<script/src=…>` with no whitespace —
// each would make a regex judge one URL while the browser fetches another,
// turning the allowlist fail-open. Parsing costs one DOMParser pass, which
// inlineLocalRefs already spends on image-bearing documents anyway.
//
// A src/href of data:, blob:, or # stays: it loads nothing external. So does
// an empty one. Everything else — absolute http(s), protocol-relative, and
// local/relative paths (which cannot load from the srcdoc frame at all, see
// the file-header note) — must pass the allowlist or go.
const KEEP_SRC_RE = /^(?:data:|blob:|#|$)/i
const RENDER_REL_RE = /(?:^|\s)(?:stylesheet|preload|modulepreload)(?:\s|$)/i

// Returns the document with its disallowed external scripts and stylesheets
// removed and how many were removed; 0 means the input came back untouched —
// the identity return is what lets selfContainedDocument skip the banner and
// the serialize round-trip for the common self-contained page.
function stripExternalRefs(html: string): { html: string; removed: number } {
  if (!/<script|<link/i.test(html)) return { html, removed: 0 }
  const doc = new DOMParser().parseFromString(html, 'text/html')
  let removed = 0
  for (const el of Array.from(doc.querySelectorAll('script[src]'))) {
    const src = (el.getAttribute('src') ?? '').trim()
    if (KEEP_SRC_RE.test(src) || isAllowedRef(src)) continue
    el.remove()
    removed++
  }
  for (const el of Array.from(doc.querySelectorAll('link[href]'))) {
    if (!RENDER_REL_RE.test(el.getAttribute('rel') ?? '')) continue
    const href = (el.getAttribute('href') ?? '').trim()
    if (KEEP_SRC_RE.test(href) || isAllowedRef(href)) continue
    el.remove()
    removed++
  }
  if (removed === 0) return { html, removed: 0 }
  const doctype = doc.doctype ? `<!DOCTYPE ${doc.doctype.name}>` : ''
  return { html: doctype + doc.documentElement.outerHTML, removed }
}

// The banner a stripped page renders under. It goes right after the <body>
// start tag when there is one (so the page's own layout still applies to the
// content below it). Without one it must still land after the DOCTYPE, or
// the parser leaves initial mode on the <div>, ignores the DOCTYPE when it
// then arrives, and drops the whole page into quirks mode — so the fallbacks
// are, in order, after </head>, after <html …>, after <!DOCTYPE …>, and only
// for a bare fragment the very top. Normal flow, not fixed: a fixed bar would
// sit on top of whatever the page puts at y=0.
const BANNER_ANCHORS = [/<body\b[^>]*>/i, /<\/head\s*>/i, /<html\b[^>]*>/i, /<!doctype\b[^>]*>/i]
function withStrippedBanner(html: string, removed: number, isDark: boolean): string {
  const bg = isDark ? '#2b2111' : '#fff8e1'
  const border = isDark ? '#594214' : '#f0c040'
  const color = isDark ? '#e8b339' : '#7a5c00'
  const what = removed === 1 ? '1 external script/stylesheet was' : `${removed} external scripts/stylesheets were`
  const banner = `<div style="padding:8px 12px;font:12px/1.5 system-ui,sans-serif;color:${color};background:${bg};border-bottom:1px solid ${border}">` +
    `⚠️ ${what} removed — only well-known CDNs (cdnjs, jsdelivr, unpkg, bootcdn, …) load here. ` +
    `The page may look or behave differently; the file itself is unchanged.</div>`
  for (const re of BANNER_ANCHORS) {
    const m = re.exec(html)
    if (!m) continue
    const at = m.index + m[0].length
    return html.slice(0, at) + banner + html.slice(at)
  }
  return banner + html
}

// The document a sandboxed frame actually renders for agent-written HTML:
// external references stripped, banner added when any were, the input handed
// back untouched otherwise. Theme is read at call time; callers re-run on a
// theme switch — artifact previews via installArtifactThemeRefresh dropping
// them to unloaded, the Light App view by depending on themeRev.
export function selfContainedDocument(html: string): string {
  const { html: stripped, removed } = stripExternalRefs(html)
  if (removed === 0) return html
  const isDark = document.documentElement.getAttribute('data-theme') === 'dark'
  return withStrippedBanner(stripped, removed, isDark)
}

function artifactURL(sessionId: string, path: string): string {
  return `/api/sessions/${encodeURIComponent(sessionId)}/artifacts?path=${encodeURIComponent(path)}`
}

// ─── Local file references inside a preview document ────────────────────────
//
// A markdown or HTML artifact can point at its own images: `![shot](shot.png)`,
// `<img src="chart.png">`, `background-image:url(bg.png)`. None of those survive
// inside the preview iframe — a relative path resolves against the host page,
// and the /api/ path it would need can't authenticate from in there (see the
// file-header note) — so the bytes are inlined as data: URIs, the one form the
// iframe can read.
//
// Two gates on what gets inlined: the reference must resolve to an image (the
// endpoint also serves .html and .md, and an artifact must not be able to pull
// those in), and the session itself must have written it, since the endpoint
// serves nothing else. That covers the case that matters: a report the agent
// wrote beside the screenshots it took. Everything else is left exactly as
// written and simply doesn't render, same as today. (A local .css or .js never
// gets this far: stripExternalRefs has already removed it.)
//
// The budget counts raw file bytes, not what they cost once resident: a data:
// URI carries base64's ~1.37x, doubled again by UTF-16, and the srcdoc
// attribute holds a second copy — so a full budget is several times its own
// size in memory for as long as the artifact stays in the store. It is also
// per-artifact, so a session with many image-bearing documents accumulates.
const inlineRefBudget = 6 << 20
const inlineRefMax = 40

// Cheap pre-check: skip the parse/serialize round-trip entirely for a document
// with nothing to rewrite, which is most of them.
const LOCAL_REF_HINT = /<img\b|<image\b|url\(/i
// A quoted url() body may legally contain ')' — an SVG data URI holding
// rgb(...), a file name with parentheses — so the quoted forms get their own
// alternatives (#1892). The unquoted form may not, short of CSS escapes this
// best-effort pass doesn't attempt.
const CSS_URL_RE = /url\(\s*(?:"([^"]*)"|'([^']*)'|([^'")]*?))\s*\)/gi

// mode picks how the result is serialized: an HTML artifact is a whole document
// (doctype and <head> must survive), markdown output is a body fragment.
async function inlineLocalRefs(
  html: string,
  sessionId: string,
  basePath: string,
  mode: 'document' | 'fragment',
): Promise<string> {
  if (!LOCAL_REF_HINT.test(html)) return html
  const doc = new DOMParser().parseFromString(html, 'text/html')

  // A <base href> re-aims every relative reference, and the iframe honors it,
  // so the inliner must resolve the same way (#1892). A local base substitutes
  // its directory for the artifact's own; a base with a scheme (or protocol-
  // relative) means every relative reference is remote — a load the sandboxed
  // iframe performs fine — so there is nothing local left to inline.
  const baseHref = doc.querySelector('base[href]')?.getAttribute('href')?.trim()
  if (baseHref) {
    if (baseHref.startsWith('//')) return html
    if (/^[a-z][a-z0-9+.-]*:/i.test(baseHref) && !/^[a-z]:[\\/]/i.test(baseHref)) return html
    // URL semantics: the base itself resolves against the document, and
    // references then resolve against everything up to its last slash — so
    // "assets/" appends a directory while a slashless "assets" changes nothing.
    basePath = isAbsolutePath(baseHref) ? baseHref : `${dirOf(basePath)}/${baseHref}`
  }

  // null caches a failed lookup so a repeated broken reference is fetched once.
  const seen = new Map<string, string | null>()
  let spent = 0
  let count = 0
  let changed = false

  const inline = async (raw: string): Promise<string | null> => {
    const abs = localFilePath(raw, basePath)
    if (!abs) return null
    // Images only, enforced rather than assumed. The endpoint also serves .html
    // and .md, so without this an artifact could name a sibling document here
    // and have the host page — which is authenticated — fetch it and hand the
    // bytes to a preview iframe that runs scripts and can reach the network.
    // The artifact would be reading files it was never granted.
    if (kindOf(abs) !== 'image') return null
    const cached = seen.get(abs)
    if (cached !== undefined) return cached
    if (count >= inlineRefMax || spent >= inlineRefBudget) return null
    let dataUrl: string | null = null
    try {
      const res = await fetch(artifactURL(sessionId, abs))
      if (res.ok) {
        const blob = await res.blob()
        dataUrl = await blobToDataURL(blob)
        spent += blob.size
        count++
      }
    } catch {
      // Leave the reference as written — a broken image beats no preview.
    }
    seen.set(abs, dataUrl)
    return dataUrl
  }

  for (const img of Array.from(doc.querySelectorAll('img'))) {
    const src = img.getAttribute('src')
    let dataUrl = await inline(src ?? '')
    // A srcset-only <img> has nothing in src to inline, and generating data:
    // URIs *inside* a srcset is what's hairy (their commas collide with the
    // candidate separator). Promoting one candidate to src isn't: inline the
    // first candidate and let the srcset removal below make it take effect
    // (#1892). Only when src is absent — a present-but-remote src is a load
    // the iframe performs fine and must not be second-guessed.
    if (!dataUrl && !src) dataUrl = await inline(firstSrcsetCandidate(img.getAttribute('srcset')))
    if (!dataUrl) continue
    img.setAttribute('src', dataUrl)
    // Whatever outranks the src has to go, or the rewrite achieves nothing: a
    // srcset beats src, and a <picture>'s <source> beats both. Their candidates
    // are unreachable for exactly the reason the src was, so dropping them is
    // what makes the inlined src take effect.
    img.removeAttribute('srcset')
    const parent = img.parentElement
    if (parent && parent.tagName === 'PICTURE') {
      for (const s of Array.from(parent.querySelectorAll('source'))) s.remove()
    }
    changed = true
  }
  // SVG's <image> is the same reference in a different attribute.
  for (const el of Array.from(doc.querySelectorAll('image'))) {
    const dataUrl = await inline(el.getAttribute('href') ?? el.getAttribute('xlink:href') ?? '')
    if (!dataUrl) continue
    if (el.hasAttribute('href')) el.setAttribute('href', dataUrl)
    if (el.hasAttribute('xlink:href')) el.setAttribute('xlink:href', dataUrl)
    changed = true
  }
  for (const el of Array.from(doc.querySelectorAll('style'))) {
    const css = el.textContent ?? ''
    const next = await inlineCSSURLs(css, inline)
    if (next === css) continue
    el.textContent = next
    changed = true
  }
  for (const el of Array.from(doc.querySelectorAll('[style]'))) {
    const css = el.getAttribute('style') ?? ''
    const next = await inlineCSSURLs(css, inline)
    if (next === css) continue
    el.setAttribute('style', next)
    changed = true
  }

  // Hand back the original text when nothing was rewritten, so a document with
  // no local references is never reshaped by the round-trip.
  if (!changed) return html
  if (mode === 'fragment') return doc.body.innerHTML
  return serializeDocument(doc)
}

// First URL in a srcset, parsed just far enough to promote it to src. Split
// on commas and take each candidate's first token: a data: URI candidate
// (whose own commas defeat the split) yields fragments that resolve to
// nothing and fail the inliner's gates, same as any other broken reference.
function firstSrcsetCandidate(srcset: string | null): string {
  for (const part of (srcset ?? '').split(',')) {
    const url = part.trim().split(/\s+/)[0]
    if (url) return url
  }
  return ''
}

// documentElement.outerHTML alone drops everything at document level: the
// doctype and any comments beside it (a build stamp above <html>, say).
// Serialize the document's own children instead (#1892) — the parser keeps no
// document-level whitespace, so the pieces butt against each other, same as
// the old doctype + <html> pair did.
function serializeDocument(doc: Document): string {
  let out = ''
  for (const node of Array.from(doc.childNodes)) {
    if (node.nodeType === Node.DOCUMENT_TYPE_NODE) out += serializeDoctype(node as DocumentType)
    else if (node.nodeType === Node.COMMENT_NODE) out += `<!--${(node as Comment).data}-->`
    else if (node.nodeType === Node.ELEMENT_NODE) out += (node as Element).outerHTML
  }
  return out
}

// The doctype is rebuilt with its public/system identifiers, since those are
// what decide the rendering mode and a name-only `<!DOCTYPE html>` would
// silently switch a legacy file to standards mode.
function serializeDoctype(dt: DocumentType | null): string {
  if (!dt) return ''
  let out = `<!DOCTYPE ${dt.name}`
  if (dt.publicId) out += ` PUBLIC "${dt.publicId}"`
  else if (dt.systemId) out += ' SYSTEM'
  if (dt.systemId) out += ` "${dt.systemId}"`
  return out + '>'
}

async function inlineCSSURLs(
  css: string,
  inline: (raw: string) => Promise<string | null>,
): Promise<string> {
  const out: string[] = []
  let last = 0
  for (const m of Array.from(css.matchAll(CSS_URL_RE))) {
    const dataUrl = await inline(m[1] ?? m[2] ?? m[3] ?? '')
    if (!dataUrl) continue
    out.push(css.slice(last, m.index), `url("${dataUrl}")`)
    last = m.index + m[0].length
  }
  if (out.length === 0) return css
  out.push(css.slice(last))
  return out.join('')
}

// localFilePath resolves a reference to the absolute on-disk path to ask the
// endpoint for, or null when it isn't a local file. Anything carrying a scheme
// is left alone: an http(s) image is a cross-origin no-cors load, which a
// sandboxed iframe performs just fine.
function localFilePath(raw: string, basePath: string): string | null {
  const src = raw.trim()
  if (!src || src.startsWith('//') || src.startsWith('#')) return null
  if (/^[a-z][a-z0-9+.-]*:/i.test(src) && !/^[a-z]:[\\/]/i.test(src)) return null
  let rel = src.split(/[?#]/)[0]
  try {
    rel = decodeURIComponent(rel)
  } catch {
    // Malformed escapes: use the reference verbatim.
  }
  if (!rel) return null
  const abs = isAbsolutePath(rel) ? rel : `${dirOf(basePath)}/${rel}`
  const clean = cleanPath(abs)
  return isAbsolutePath(clean) ? clean : null
}

// A leading "/" or a Windows drive letter. Paths are normalised to forward
// slashes; the server runs filepath.Clean on its side, which re-separates them
// per platform before matching against the transcript.
function isAbsolutePath(p: string): boolean {
  return p.startsWith('/') || /^[a-z]:[\\/]/i.test(p)
}

function dirOf(p: string): string {
  const norm = p.replace(/\\/g, '/')
  const cut = norm.lastIndexOf('/')
  return cut < 0 ? '' : norm.slice(0, cut)
}

// Resolves "." and ".." segments and collapses repeats. Traversal needs no
// guarding here: the endpoint only serves paths this session's transcript
// records, so a resolved path outside the artifact's directory is simply a 404.
function cleanPath(p: string): string {
  const norm = p.replace(/\\/g, '/')
  const out: string[] = []
  for (const seg of norm.split('/')) {
    if (seg === '' || seg === '.') continue
    if (seg === '..') {
      out.pop()
      continue
    }
    out.push(seg)
  }
  return (norm.startsWith('/') ? '/' : '') + out.join('/')
}

function blobToDataURL(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(reader.result as string)
    reader.onerror = () => reject(reader.error)
    reader.readAsDataURL(blob)
  })
}

// Clear artifacts on session switch; history replay then repopulates. The
// session marker gates in-flight fetches so a late response can't leak into the
// newly-selected session.
export function resetArtifacts(sessionId: string): void {
  artifacts.set([])
  artifactSel.set(0)
  panelContent.set(null)
  // Closing the panel must clear the expanded flag with it, or the main column
  // stays hidden with nothing in its place: a full-width Light App is open,
  // switching to a chat resets the panel, and the layout paints blank.
  panelExpanded.set(false)
  autoOpened = false
  imageRevisions.clear()
  artifactSelSession.set(sessionId)
}

// Ingest one tool ui_payload. `live` distinguishes a current turn (auto-opens
// the panel once) from history replay (silent). Observing is metadata-only:
// text kinds land with an empty body and `loaded: false`, and the fetch +
// preview build run on first selection instead (hydrateArtifact). History
// replay over any number of artifacts therefore transfers nothing — and
// because nothing is awaited before the upsert, entries land in transcript
// order by construction (#1894: the old per-artifact fetch made the list come
// out in fetch-completion order; hydration now writes back in place and never
// reorders or reselects).
export function observeArtifact(
  sessionId: string,
  uiPayload: any,
  live: boolean,
): void {
  if (!sessionId || !uiPayload) return
  const t = uiPayload.type
  if (t !== 'write' && t !== 'edit' && t !== 'artifact') return
  const path: string = uiPayload.path
  if (!path) return
  const kind = kindOf(path)
  if (!kind) return

  let code = ''
  let src = ''
  let loaded = false
  if (kind === 'image') {
    // Images render as a plain <img> in the host document — see the
    // file-header note on why an <img src="/api/…"> inside the sandboxed
    // iframe 401s. The host document's own request is same-site and
    // authenticates normally, and observing costs no network at all: the URL
    // only becomes a request once the panel renders this artifact, and it
    // renders one at a time. There is no preview document to build either, so
    // an image observes as already loaded.
    //
    // The revision counter matters: re-observing a path (the agent overwrote
    // the file) otherwise yields a byte-identical src, so Svelte skips the
    // attribute update and the panel keeps showing the previous bytes — the
    // endpoint sends no validators, so nothing prompts a revalidation either.
    // Counting observations rather than stamping a clock keeps the URL stable
    // across a straight replay of the same transcript, so a reopened session
    // can still reuse whatever the browser happens to have cached.
    //
    // Dropping the iframe costs no isolation here. The endpoint pins
    // Content-Type and sends X-Content-Type-Options: nosniff, and an SVG
    // loaded through <img> runs no script and fetches no external resource.
    const rev = (imageRevisions.get(path) ?? 0) + 1
    imageRevisions.set(path, rev)
    src = `${artifactURL(sessionId, path)}&rev=${rev}`
    // A binary artifact has no source view, so `code` carries the on-disk
    // path instead: it is the one text form worth copying. Download saves
    // the bytes from `src`.
    code = path
    loaded = true
  }

  const name = basename(path)
  const entry: Artifact = {
    name,
    type: typeLabel(kind),
    ver: '',
    short: name.length > 22 ? name.slice(0, 21) + '…' : name,
    icon: iconFor(kind),
    code,
    preview: '',
    path,
    src: src || undefined,
    loaded,
  }

  artifacts.update(list => {
    const next = list.filter(a => a.path !== path)
    next.push(entry)
    return next
  })
  artifactSel.set(get(artifacts).length - 1)

  if (live && !autoOpened) {
    autoOpened = true
    panelContent.set('session')
  }
}

// In-flight builds, keyed by entry identity: a live re-write replaces the
// entry object in the store, so a stale build can never land on the fresh
// entry, and the fresh entry is free to start its own.
const hydrating = new WeakSet<Artifact>()

// Shown instead of a spinner-forever when the body can't be fetched any more
// (the file was deleted, say). Grey on the panel's own background reads fine
// on either theme; a re-write of the path replaces the entry and retries.
const LOAD_FAILED_PREVIEW =
  '<body style="margin:0;padding:16px;font:13px/1.5 system-ui,sans-serif;color:#8b949e">' +
  '⚠️ This file could not be loaded for preview. It may have been deleted or moved.</body>'

// Builds an artifact's body on first selection rather than at observe time:
// the fetch — and for markdown/HTML the image inlining behind it, whose data:
// URIs stay resident for as long as the entry does — is paid only for what
// the user actually opens (#1893). Safe to call on every render; it no-ops
// once the entry is loaded and while a build is in flight.
export async function hydrateArtifact(a: Artifact | null | undefined): Promise<void> {
  if (!a || a.loaded || hydrating.has(a)) return
  const sessionId = get(artifactSelSession)
  const kind = kindOf(a.path)
  if (!sessionId || !kind || kind === 'image') return
  hydrating.add(a)
  let body: { code: string; preview: string } | null = null
  try {
    body = await buildTextBody(sessionId, a.path, kind)
  } catch {
    body = null
  } finally {
    hydrating.delete(a)
  }
  // The active session may have changed while the fetch was in flight — the
  // guard that used to live in observeArtifact moves here with the fetch. The
  // identity match below likewise drops the result when a re-write replaced
  // the entry meanwhile: the replacement hydrates itself with the new bytes.
  if (get(artifactSelSession) !== sessionId) return
  const next: Artifact = {
    ...a,
    ...(body ?? { code: '', preview: LOAD_FAILED_PREVIEW, loadFailed: true }),
    loaded: true,
  }
  artifacts.update(list => list.map(e => (e === a ? next : e)))
}

// Preview documents bake the resolved theme into their inline styles at build
// time (buildTextBody reads data-theme once), so a live theme switch left
// stale-themed previews in the panel until the artifact happened to be
// re-written. Watch the resolved <html data-theme> attribute — one observer
// catches every path that changes it (manual pick, pack change, the OS
// listener under "system") — and drop each built text preview back to
// unloaded. Replacing the entry object is the same move a live re-write
// makes: hydrateArtifact's in-flight WeakSet doesn't know the clone, so the
// visible artifact rebuilds immediately, and a stale in-flight build fails
// its identity-matched write-back instead of resurrecting the old theme.
// Image artifacts stay untouched: they carry src, never a themed preview,
// and hydrateArtifact would refuse to re-load them.
//
// themeRev ticks on the same event for documents built outside the store —
// the Light App frame derives its srcdoc from it so a banner baked with the
// old theme's colours is rebuilt too.
export const themeRev = writable(0)

export function installArtifactThemeRefresh(): void {
  if (typeof MutationObserver === 'undefined' || typeof document === 'undefined') return
  let last = document.documentElement.getAttribute('data-theme')
  const obs = new MutationObserver(() => {
    const cur = document.documentElement.getAttribute('data-theme')
    if (cur === last) return
    last = cur
    themeRev.update(n => n + 1)
    artifacts.update(list => list.map(e =>
      e.loaded && !e.src ? { ...e, loaded: false, loadFailed: false, preview: '', code: '' } : e))
  })
  obs.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
}

// The fetch + preview build for a text-kind artifact, extracted verbatim from
// the old eager observeArtifact. null means the body wasn't fetchable.
async function buildTextBody(
  sessionId: string,
  path: string,
  kind: Kind,
): Promise<{ code: string; preview: string } | null> {
  const res = await fetch(artifactURL(sessionId, path))
  if (!res.ok) return null
  const code = await res.text()
  const isDark = document.documentElement.getAttribute('data-theme') === 'dark'
  let preview = ''
  if (kind === 'html') {
    // External scripts and stylesheets come out (see stripExternalRefs); the
    // rest of the page renders, under a banner when anything was removed.
    // The file's own images still need inlining to survive the iframe —
    // and a page that lost its CDN stylesheet is exactly the one whose
    // remaining content the user wants to see.
    preview = await inlineLocalRefs(selfContainedDocument(code), sessionId, path, 'document')
  } else {
    // Only markdown reaches this branch: hydrateArtifact never calls in for an
    // image, and html was handled above.
    // Markdown is rendered inside a sandboxed srcdoc iframe which has no
    // access to the host app's CSS or JS.  Inline the highlight.js theme
    // CSS, code-block layout, and a copy-button handler so syntax
    // highlighting and the "Copy" button actually work in preview mode.
    const MD_STYLES = isDark ? darkMDStyles() : lightMDStyles()
    const bodyStyle = isDark
      ? 'margin:0;padding:16px;font:14px/1.6 system-ui,-apple-system,sans-serif;color:#d4d4d4;background:#1e1e1e'
      : 'margin:0;padding:16px;font:14px/1.6 system-ui,-apple-system,sans-serif;color:rgba(0,0,0,0.88);background:#ffffff'
    // Bound to document.body, not '.body': this is a hand-inlined copy of
    // setupCopyButtons(), whose caller passes the host app's container, and
    // that selector matched nothing here — querySelector returned null and
    // the whole handler died on load, so the button never worked at all.
    //
    // The clipboard call has a fallback because this document's origin is
    // opaque; whether the async Clipboard API is available to it varies by
    // browser even with clipboard-write delegated on the iframe. execCommand
    // is deprecated but needs no permission, only the click's activation.
    //
    // The .code-block lookup is null-guarded like setupCopyButtons' is; the
    // old unguarded form would throw if the wrapper ever went missing.
    const COPY_SCRIPT = `<script>
	document.body.addEventListener('click',function(e){
	  var b=e.target.closest('.copy-btn');if(!b)return;
	  var k=b.closest('.code-block');
	  var c=k&&k.querySelector('pre code');
	  var t=c?c.textContent:'';
	  var done=function(ok){
	    var o=b.textContent;b.textContent=ok?'Copied!':'Copy failed';
	    setTimeout(function(){b.textContent=o},1500);
	  };
	  var legacy=function(){
	    var ta=document.createElement('textarea');
	    ta.value=t;ta.setAttribute('readonly','');
	    ta.style.position='fixed';ta.style.opacity='0';
	    document.body.appendChild(ta);ta.select();
	    var ok=false;try{ok=document.execCommand('copy')}catch(err){}
	    ta.remove();done(ok);
	  };
	  if(navigator.clipboard&&navigator.clipboard.writeText){
	    navigator.clipboard.writeText(t).then(function(){done(true)},legacy)
	  }else{legacy()}
	});
	<\/script>`
    // rawHtml: the document's own tags are content here, not injection — this
    // iframe is sandboxed and styled by MD_STYLES alone, and inlineLocalRefs
    // below has to see an <img src="chart.png"> to rewrite it to a data: URI.
    // The chat's own bubbles get the escaping default instead (markdown.ts).
    const body = await inlineLocalRefs(renderMarkdown(code, true, { rawHtml: true }), sessionId, path, 'fragment')
    preview = `<style>${MD_STYLES}</style><body style="${bodyStyle}">${body}${COPY_SCRIPT}</body>`
  }
  return { code, preview }
}

// darkMDStyles returns inline CSS for code blocks and syntax highlighting
// in dark mode — mirrors highlight.js github-dark.css.
function darkMDStyles(): string {
  return [
    `.code-block{border:1px solid #30363d;border-radius:8px;overflow:hidden;background:#1e1e1e;margin:10px 0}`,
    `.code-header{display:flex;align-items:center;gap:8px;padding:6px 8px 6px 12px;background:#2d2d2d;border-bottom:1px solid #30363d}`,
    `.code-lang{font-size:11px;color:#888;font-family:ui-monospace,SFMono-Regular,Menlo,monospace}`,
    `.copy-btn{margin-left:auto;height:24px;padding:0 8px;border:none;background:transparent;border-radius:5px;font-size:11px;color:#888;cursor:pointer;font-family:inherit}`,
    `.copy-btn:hover{background:#3d3d3d;color:#58a6ff}`,
    // Markdown tables — mirrors the app's .rich-answer/.md-content table skin
    // (see ChatView/ProfileView) with the preview iframe's own palette.
    `.table-scroll{overflow-x:auto;margin:10px 0}`,
    `table{width:max-content;min-width:100%;max-width:none;border-collapse:collapse;border-spacing:0;font-size:13.5px;line-height:1.55}`,
    `th,td{padding:7px 14px;text-align:left;vertical-align:top;border:1px solid #30363d}`,
    `th{background:#2d2d2d;font-weight:600;color:#e6edf3;white-space:nowrap}`,
    `tbody tr:nth-child(even) td{background:#282a2e}`,
    `pre{margin:0;padding:12px 14px;overflow-x:auto;font-size:13px;line-height:1.6;font-family:ui-monospace,SFMono-Regular,Menlo,monospace;color:#d4d4d4}`,
    `code.hljs{background:transparent;padding:0}`,
    `.hljs{color:#c9d1d9;background:#0d1117}`,
    `.hljs-keyword,.hljs-doctag,.hljs-meta\\ .hljs-keyword,.hljs-template-tag,.hljs-template-variable,.hljs-type,.hljs-variable\\.language_{color:#ff7b72}`,
    `.hljs-title,.hljs-title\\.class_,.hljs-title\\.class_\\.inherited__,.hljs-title\\.function_{color:#d2a8ff}`,
    `.hljs-attr,.hljs-attribute,.hljs-literal,.hljs-meta,.hljs-number,.hljs-operator,.hljs-variable,.hljs-selector-attr,.hljs-selector-class,.hljs-selector-id{color:#79c0ff}`,
    `.hljs-regexp,.hljs-string,.hljs-meta\\ .hljs-string{color:#a5d6ff}`,
    `.hljs-built_in,.hljs-symbol{color:#ffa657}`,
    `.hljs-comment,.hljs-code,.hljs-formula{color:#8b949e}`,
    `.hljs-name,.hljs-quote,.hljs-selector-tag,.hljs-selector-pseudo,.hljs-tag{color:#7ee787}`,
    `.hljs-subst{color:#c9d1d9}`,
    `.hljs-section{color:#1f6feb;font-weight:700}`,
    `.hljs-bullet{color:#f2cc60}`,
    `.hljs-emphasis{font-style:italic}`,
    `.hljs-strong{font-weight:700}`,
    `.hljs-addition{color:#aff5b4;background:#033a16}`,
    `.hljs-deletion{color:#ffdcd7;background:#67060c}`,
  ].join('')
}

// lightMDStyles returns inline CSS for code blocks and syntax highlighting
// in light mode — mirrors the main app's light theme and GitHub light syntax.
function lightMDStyles(): string {
  return [
    `.code-block{border:1px solid #d0d7de;border-radius:8px;overflow:hidden;background:#f6f8fa;margin:10px 0}`,
    `.code-header{display:flex;align-items:center;gap:8px;padding:6px 8px 6px 12px;background:#eaeef2;border-bottom:1px solid #d0d7de}`,
    `.code-lang{font-size:11px;color:#57606a;font-family:ui-monospace,SFMono-Regular,Menlo,monospace}`,
    `.copy-btn{margin-left:auto;height:24px;padding:0 8px;border:none;background:transparent;border-radius:5px;font-size:11px;color:#57606a;cursor:pointer;font-family:inherit}`,
    `.copy-btn:hover{background:#d0d7de;color:#0969da}`,
    // Markdown tables — mirrors the app's .rich-answer/.md-content table skin
    // (see ChatView/ProfileView) with the preview iframe's own palette.
    `.table-scroll{overflow-x:auto;margin:10px 0}`,
    `table{width:max-content;min-width:100%;max-width:none;border-collapse:collapse;border-spacing:0;font-size:13.5px;line-height:1.55}`,
    `th,td{padding:7px 14px;text-align:left;vertical-align:top;border:1px solid #d0d7de}`,
    `th{background:#eaeef2;font-weight:600;color:#1f2328;white-space:nowrap}`,
    `tbody tr:nth-child(even) td{background:#f6f8fa}`,
    `pre{margin:0;padding:12px 14px;overflow-x:auto;font-size:13px;line-height:1.6;font-family:ui-monospace,SFMono-Regular,Menlo,monospace;color:#24292f}`,
    `code.hljs{background:transparent;padding:0}`,
    // highlight.js GitHub (light) theme — simplified
    `.hljs{color:#24292f;background:#fff}`,
    `.hljs-keyword,.hljs-doctag,.hljs-meta\\ .hljs-keyword,.hljs-template-tag,.hljs-template-variable,.hljs-type,.hljs-variable\\.language_{color:#cf222e}`,
    `.hljs-title,.hljs-title\\.class_,.hljs-title\\.class_\\.inherited__,.hljs-title\\.function_{color:#8250df}`,
    `.hljs-attr,.hljs-attribute,.hljs-literal,.hljs-meta,.hljs-number,.hljs-operator,.hljs-variable,.hljs-selector-attr,.hljs-selector-class,.hljs-selector-id{color:#0550ae}`,
    `.hljs-regexp,.hljs-string,.hljs-meta\\ .hljs-string{color:#0a3069}`,
    `.hljs-built_in,.hljs-symbol{color:#953800}`,
    `.hljs-comment,.hljs-code,.hljs-formula{color:#6e7781}`,
    `.hljs-name,.hljs-quote,.hljs-selector-tag,.hljs-selector-pseudo,.hljs-tag{color:#116329}`,
    `.hljs-subst{color:#24292f}`,
    `.hljs-section{color:#0550ae;font-weight:700}`,
    `.hljs-bullet{color:#953800}`,
    `.hljs-emphasis{font-style:italic}`,
    `.hljs-strong{font-weight:700}`,
    `.hljs-addition{color:#1a7f37;background:#dafbe1}`,
    `.hljs-deletion{color:#cf222e;background:#ffebe9}`,
  ].join('')
}
