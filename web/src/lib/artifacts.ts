// Artifacts panel data layer.
//
// Previewable files the agent writes ride the existing ui_payload stream: the
// write / edit / show_artifact tools each emit { type, path }. observeArtifact()
// picks those up (from both the live tool_result path and history replay),
// fetches the file body from the whitelisted GET /api/sessions/{id}/artifacts
// endpoint, and pushes a previewable entry into the `artifacts` store that
// ArtifactsPanel renders. Mirrors the old hand-written Artifacts.observe().
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
import { artifacts, panelContent, artifactSel } from './stores'
import { renderMarkdown } from './markdown'
import type { Artifact } from './types'

// Tracks which session the current artifacts belong to, so an async fetch that
// resolves after a session switch is discarded instead of polluting the new view.
export const artifactSelSession = writable<string | null>(null)

type Kind = 'html' | 'markdown' | 'image' | 'code'

const EXT_KIND: Record<string, Kind> = {
  html: 'html', htm: 'html',
  md: 'markdown', markdown: 'markdown',
  png: 'image', jpg: 'image', jpeg: 'image', gif: 'image', svg: 'image', webp: 'image',
  js: 'code', ts: 'code', jsx: 'code', tsx: 'code', mjs: 'code', cjs: 'code',
  css: 'code', scss: 'code', less: 'code',
  json: 'code', yaml: 'code', yml: 'code', toml: 'code',
  py: 'code', go: 'code', rs: 'code', sh: 'code', bash: 'code', zsh: 'code',
  txt: 'code', xml: 'code', csv: 'code',
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
    case 'code':     return 'ant-design:file-text-outlined'
    default:         return 'ant-design:file-text-outlined'
  }
}

function typeLabel(kind: Kind, path: string): string {
  if (kind === 'code') {
    const dot = path.lastIndexOf('.')
    return dot >= 0 ? path.slice(dot + 1).toUpperCase() : 'Code'
  }
  switch (kind) {
    case 'html':     return 'HTML'
    case 'markdown': return 'Markdown'
    case 'image':    return 'Image'
    default:         return 'File'
  }
}

// Detects HTML that references external scripts or stylesheets — these fail to
// load inside a sandboxed srcdoc iframe that has no same-origin access. Only
// <link> rel values whose target the document needs in order to render count:
// a favicon, manifest, preconnect, or canonical link loads nothing the preview
// depends on, so it must not force the warning page (#1896). preload rides
// along with stylesheet because the rel="preload" onload="this.rel='stylesheet'"
// idiom makes it one.
const SCRIPT_SRC_RE = /<script[^>]+src=["'](?!data:|blob:|#)[^"']/i
const LINK_TAG_RE = /<link\b[^>]*>/gi
const LINK_HREF_RE = /\bhref\s*=\s*["'](?!data:|blob:|#)[^"']/i
const LINK_REL_RE = /\brel\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))/i
const RENDER_REL_RE = /(?:^|\s)(?:stylesheet|preload|modulepreload)(?:\s|$)/i
function hasExternalRefs(html: string): boolean {
  if (SCRIPT_SRC_RE.test(html)) return true
  for (const tag of html.match(LINK_TAG_RE) ?? []) {
    if (!LINK_HREF_RE.test(tag)) continue
    const m = LINK_REL_RE.exec(tag)
    if (RENDER_REL_RE.test(m?.[1] ?? m?.[2] ?? m?.[3] ?? '')) return true
  }
  return false
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
// endpoint also serves .html, .md, and the plain-text code kinds, and an
// artifact must not be able to pull those in), and the session itself must have
// written it, since the endpoint
// serves nothing else. That covers the case that matters: a report the agent
// wrote beside the screenshots it took. Everything else is left exactly as
// written and simply doesn't render, same as today — which is also the fallback
// for a local .css or .js, already routed to the warning page by
// hasExternalRefs.
//
// The budget counts raw file bytes, not what they cost once resident: a data:
// URI carries base64's ~1.37x, doubled again by UTF-16, and the srcdoc
// attribute holds a second copy — so a full budget is several times its own
// size in memory for as long as the artifact stays in the store. It is also
// per-artifact, so a session with many image-bearing documents accumulates.
// Both numbers are deliberately well under what one page needs.
const inlineRefBudget = 4 << 20
const inlineRefMax = 20

// Cheap pre-check: skip the parse/serialize round-trip entirely for a document
// with nothing to rewrite, which is most of them.
const LOCAL_REF_HINT = /<img\b|<image\b|url\(/i
const CSS_URL_RE = /url\(\s*(['"]?)([^'")]+)\1\s*\)/gi

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

  // null caches a failed lookup so a repeated broken reference is fetched once.
  const seen = new Map<string, string | null>()
  let spent = 0
  let count = 0
  let changed = false

  const inline = async (raw: string): Promise<string | null> => {
    const abs = localFilePath(raw, basePath)
    if (!abs) return null
    // Images only, enforced rather than assumed. The endpoint also serves .html,
    // .md, and the plain-text code kinds, so without this an artifact could name
    // a sibling document here and have the host page — which is authenticated —
    // fetch it and hand the bytes to a preview iframe that runs scripts and can
    // reach the network. The artifact would be reading files it was never granted.
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
    const dataUrl = await inline(img.getAttribute('src') ?? '')
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
  return serializeDoctype(doc.doctype) + doc.documentElement.outerHTML
}

// documentElement.outerHTML drops the doctype, so it gets rebuilt — with its
// public/system identifiers, since those are what decide the rendering mode and
// a name-only `<!DOCTYPE html>` would silently switch a legacy file to standards
// mode. Anything else above <html> (a build comment, say) is still lost.
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
    const dataUrl = await inline(m[2])
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
  autoOpened = false
  imageRevisions.clear()
  artifactSelSession.set(sessionId)
}

// Ingest one tool ui_payload. `live` distinguishes a current turn (auto-opens
// the panel once) from history replay (silent). Async: fetches the body, then
// upserts the artifact (newest selected).
export async function observeArtifact(
  sessionId: string,
  uiPayload: any,
  live: boolean,
): Promise<void> {
  if (!sessionId || !uiPayload) return
  const t = uiPayload.type
  if (t !== 'write' && t !== 'edit' && t !== 'artifact') return
  const path: string = uiPayload.path
  if (!path) return
  const kind = kindOf(path)
  if (!kind) return

  const url = artifactURL(sessionId, path)

  let code = ''
  let preview = ''
  let src = ''
  try {
    const isDark = document.documentElement.getAttribute('data-theme') === 'dark'
    if (kind === 'image') {
      // Images render as a plain <img> in the host document — see the
      // file-header note on why an <img src="/api/…"> inside the sandboxed
      // iframe 401s. The host document's own request is same-site and
      // authenticates normally, and observing costs no network at all: the URL
      // only becomes a request once the panel renders this artifact, and it
      // renders one at a time. History replay over a session full of multi-MB
      // screenshots therefore transfers none of them.
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
      src = `${url}&rev=${rev}`
      // A binary artifact has no source view, so `code` carries the on-disk
      // path instead: it is the one text form worth copying. Download saves
      // the bytes from `src`.
      code = path
    } else {
      const res = await fetch(url)
      if (!res.ok) return
      code = await res.text()
      if (kind === 'html') {
        if (hasExternalRefs(code)) {
          // External scripts/stylesheets can't load inside a sandboxed srcdoc
          // iframe without same-origin access. Show a warning + the raw source.
          const escaped = code.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
          const warnBodyBg = isDark ? '#0d1117' : '#fafafa'
          const warnBodyColor = isDark ? '#8b949e' : '#555'
          const warnCardBg = isDark ? '#2b2111' : '#fff8e1'
          const warnCardBorder = isDark ? '#594214' : '#f0c040'
          const warnCardColor = isDark ? '#e8b339' : '#7a5c00'
          const warnPreBg = isDark ? '#161b22' : '#f5f5f5'
          const warnPreColor = isDark ? '#c9d1d9' : '#333'
          preview = `<body style="margin:0;padding:16px;font:13px/1.5 system-ui,sans-serif;color:${warnBodyColor};background:${warnBodyBg}">
<div style="padding:10px 14px;background:${warnCardBg};border:1px solid ${warnCardBorder};border-radius:6px;margin-bottom:14px;font-size:13px;color:${warnCardColor}">
⚠️ This file references external resources and cannot be previewed here. Use <b>Open in new tab</b> or switch to <b>Code</b> view.
</div>
<pre style="margin:0;padding:12px;background:${warnPreBg};border-radius:6px;overflow:auto;font:12px/1.6 'SFMono-Regular',Menlo,monospace;color:${warnPreColor};white-space:pre-wrap">${escaped}</pre>
</body>`
        } else {
          // No unreachable scripts or stylesheets, but the file's own images
          // still need inlining to survive the iframe.
          preview = await inlineLocalRefs(code, sessionId, path, 'document')
        }
      } else if (kind === 'markdown') {
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
        const body = await inlineLocalRefs(renderMarkdown(code), sessionId, path, 'fragment')
        preview = `<style>${MD_STYLES}</style><body style="${bodyStyle}">${body}${COPY_SCRIPT}</body>`
      } else {
        // code kind: show with theme-aware monospace style
        const escaped = code.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
        const codeBg = isDark ? '#1e1e1e' : '#ffffff'
        const codeColor = isDark ? '#d4d4d4' : 'rgba(0,0,0,0.88)'
        preview = `<body style="margin:0;background:${codeBg}"><pre style="margin:0;padding:16px;color:${codeColor};font:13px/1.6 'SFMono-Regular',Menlo,monospace;white-space:pre-wrap;word-break:break-all">${escaped}</pre></body>`
      }
    }
  } catch {
    return
  }

  // The active session may have changed while the fetch was in flight.
  if (get(artifactSelSession) !== sessionId) return

  const name = basename(path)
  const entry: Artifact = {
    name,
    type: typeLabel(kind, path),
    ver: '',
    short: name.length > 22 ? name.slice(0, 21) + '…' : name,
    icon: iconFor(kind),
    code,
    preview,
    path,
    src: src || undefined,
  }

  artifacts.update(list => {
    const next = list.filter(a => a.path !== path)
    next.push(entry)
    return next
  })
  artifactSel.set(get(artifacts).length - 1)

  // Code kinds enter the list but never auto-open the panel: source-file
  // writes are the routine bulk of a coding session, and popping the sidebar
  // on the first one would make every such session open with it. They also
  // don't consume the once-per-session flag, so a later HTML report or chart
  // still auto-opens.
  if (live && !autoOpened && kind !== 'code') {
    autoOpened = true
    panelContent.set('session')
  }
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
