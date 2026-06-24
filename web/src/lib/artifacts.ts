// Artifacts panel data layer.
//
// Previewable files the agent writes ride the existing ui_payload stream: the
// write / edit / show_artifact tools each emit { type, path }. observeArtifact()
// picks those up (from both the live tool_result path and history replay),
// fetches the file body from the whitelisted GET /api/sessions/{id}/artifacts
// endpoint, and pushes a previewable entry into the `artifacts` store that
// ArtifactsPanel renders. Mirrors the old hand-written Artifacts.observe().

import { get, writable } from 'svelte/store'
import { artifacts, artifactsOpen, artifactSel } from './stores'
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
// load inside a sandboxed srcdoc iframe that has no same-origin access.
const EXTERNAL_REF_RE = /<(script[^>]+src|link[^>]+href)=["'](?!data:|blob:|#)[^"']/i
function hasExternalRefs(html: string): boolean {
  return EXTERNAL_REF_RE.test(html)
}

// Clear artifacts on session switch; history replay then repopulates. The
// session marker gates in-flight fetches so a late response can't leak into the
// newly-selected session.
export function resetArtifacts(sessionId: string): void {
  artifacts.set([])
  artifactSel.set(0)
  artifactsOpen.set(false)
  autoOpened = false
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

  const url = `/api/sessions/${encodeURIComponent(sessionId)}/artifacts?path=${encodeURIComponent(path)}`

  let code = ''
  let preview = ''
  try {
    if (kind === 'image') {
      // The sandboxed iframe loads the image from the same-host endpoint.
      code = url
      preview = `<body style="margin:0;display:flex;align-items:center;justify-content:center;background:#1e1e1e;height:100vh"><img style="max-width:100%;max-height:100vh" src="${url}"></body>`
    } else {
      const res = await fetch(url)
      if (!res.ok) return
      code = await res.text()
      if (kind === 'html') {
        if (hasExternalRefs(code)) {
          // External scripts/stylesheets can't load inside a sandboxed srcdoc
          // iframe without same-origin access. Show a warning + the raw source.
          const escaped = code.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
          preview = `<body style="margin:0;padding:16px;font:13px/1.5 system-ui,sans-serif;color:#555;background:#fafafa">
<div style="padding:10px 14px;background:#fff8e1;border:1px solid #f0c040;border-radius:6px;margin-bottom:14px;font-size:13px;color:#7a5c00">
⚠️ This file references external resources and cannot be previewed here. Use <b>Open in new tab</b> or switch to <b>Code</b> view.
</div>
<pre style="margin:0;padding:12px;background:#f5f5f5;border-radius:6px;overflow:auto;font:12px/1.6 'SFMono-Regular',Menlo,monospace;color:#333;white-space:pre-wrap">${escaped}</pre>
</body>`
        } else {
          preview = code
        }
      } else if (kind === 'markdown') {
        preview = `<body style="margin:0;padding:16px;font:14px/1.6 system-ui,-apple-system,sans-serif;color:#1f1f1f">${renderMarkdown(code)}</body>`
      } else {
        // code kind: show with a dark monospace style
        const escaped = code.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
        preview = `<body style="margin:0;background:#1e1e1e"><pre style="margin:0;padding:16px;color:#d4d4d4;font:13px/1.6 'SFMono-Regular',Menlo,monospace;white-space:pre-wrap;word-break:break-all">${escaped}</pre></body>`
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
  }

  artifacts.update(list => {
    const next = list.filter(a => a.path !== path)
    next.push(entry)
    return next
  })
  artifactSel.set(get(artifacts).length - 1)

  if (live && !autoOpened) {
    autoOpened = true
    artifactsOpen.set(true)
  }
}
