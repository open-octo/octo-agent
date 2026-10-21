// Staged attachments for a mobile composer (chat reply or new task). Images
// ride inline as a compressed data URL; any other file uploads to
// ~/.octo/uploads and is sent by path — the desktop Composer's two paths.
import { showToast } from '../lib/stores'
import * as api from '../lib/api'
import { tr } from '../lib/i18n'
import { compressImage } from '../lib/imageCompress'
import type { Attachment } from '../lib/composerDrafts'

// Same caps as the desktop Composer (keep in sync with its MAX_*_BYTES and
// maxUploadBytes in internal/server/upload_handler.go).
const MAX_IMAGE_BYTES = 32 * 1024 * 1024
const MAX_FILE_BYTES = 512 * 1024 * 1024
// Everything rides one WS message bounded by the server's 4 MB cap.
const MAX_BATCH_CHARS = 3_500_000
// What the server's agent.NewImageBlock accepts. Anything else (SVG, or a
// HEIC the WebView can't canvas-decode) would be dropped server-side, and a
// text-less message would then get no reply at all — refuse it here instead.
const SENDABLE_IMAGE = /^data:image\/(jpeg|png|gif|webp)[;,]/

let seq = 0

export class AttachmentTray {
  items = $state<Attachment[]>([])
  uploading = $derived(this.items.some(a => a.uploading))
  // Bumped by clear(): a result that lands after it belongs to files the
  // user already sent or abandoned, so it is dropped.
  #gen = 0

  async add(file: File) {
    const name = file.name || 'attachment'
    const isImage = file.type.startsWith('image/')
    if (file.size > (isImage ? MAX_IMAGE_BYTES : MAX_FILE_BYTES)) {
      showToast(tr(isImage ? 'chat.attach_img_too_large' : 'chat.attach_too_large'), 'error')
      return
    }
    const gen = this.#gen
    const id = `up-${++seq}`
    this.items = [...this.items, { id, name, mime_type: file.type, uploading: true }]
    const patch = (p: Partial<Attachment>) => {
      if (gen === this.#gen) this.items = this.items.map(a => (a.id === id ? { ...a, ...p } : a))
    }
    try {
      if (isImage) {
        const dataUrl = await compressImage(file)
        if (!SENDABLE_IMAGE.test(dataUrl)) throw new Error(tr('m.attach_img_unsupported'))
        patch({ data_url: dataUrl, mime_type: dataUrl.startsWith('data:image/jpeg') ? 'image/jpeg' : file.type, uploading: false })
      } else {
        patch({ path: await api.uploadFile(file), uploading: false })
      }
    } catch (e: any) {
      if (gen === this.#gen) this.remove(id)
      showToast(e?.message ?? name, 'error')
    }
  }

  // For an <input type="file"> change handler.
  addFromInput(e: Event) {
    const input = e.currentTarget as HTMLInputElement
    for (const f of Array.from(input.files ?? [])) this.add(f)
    // Reset so picking the same file again still fires change.
    input.value = ''
  }

  remove(id: string | undefined) {
    this.items = this.items.filter(a => a.id !== id)
  }

  clear() {
    this.#gen++
    this.items = []
  }

  // Whether text plus the staged files can go out now; toasts why not.
  ready(text: string): boolean {
    if (this.uploading) {
      showToast(tr('chat.upload_in_progress'), 'error')
      return false
    }
    let payload = text.length
    for (const a of this.items) payload += a.data_url?.length ?? 0
    if (payload > MAX_BATCH_CHARS) {
      showToast(tr('chat.attach_batch_too_big'), 'error')
      return false
    }
    return true
  }
}
