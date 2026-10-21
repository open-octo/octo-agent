// Images ride inline as base64 data URLs inside the WebSocket chat message,
// so their encoded size must stay well under the server's wsMaxMessageSize
// (4 MB) — a raw phone photo (3–10 MB → +33% base64) blows straight past it
// and the server drops the connection. Canvas-compress before staging:
// long edge capped, re-encoded to JPEG. The server's own NewImageBlock
// normalization uses the SAME cap, so an image compressed here passes
// through untouched. GIFs/SVGs are exempt (canvas would kill the
// animation/vector); they rely on the 32 MB attachment cap and the WS
// headroom instead.
const IMAGE_COMPRESS_MAX_EDGE = 1568
const IMAGE_COMPRESS_QUALITY = 0.8

export function readAsDataURL(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result))
    reader.onerror = () => reject(reader.error)
    reader.readAsDataURL(file)
  })
}

// compressImage returns a data URL for the image: JPEG-re-encoded and
// downscaled when possible, the original data URL when the canvas path is
// unavailable (or the format shouldn't be re-encoded). Never throws — a
// failed compress must not eat the attachment.
export async function compressImage(file: File): Promise<string> {
  if (file.type === 'image/gif' || file.type === 'image/svg+xml') {
    return readAsDataURL(file)
  }
  try {
    const url = URL.createObjectURL(file)
    try {
      const img = await new Promise<HTMLImageElement>((resolve, reject) => {
        const el = new Image()
        el.onload = () => resolve(el)
        el.onerror = () => reject(new Error('image decode failed'))
        el.src = url
      })
      const scale = Math.min(1, IMAGE_COMPRESS_MAX_EDGE / Math.max(img.naturalWidth, img.naturalHeight))
      const w = Math.max(1, Math.round(img.naturalWidth * scale))
      const h = Math.max(1, Math.round(img.naturalHeight * scale))
      const canvas = document.createElement('canvas')
      canvas.width = w
      canvas.height = h
      const ctx = canvas.getContext('2d')
      if (!ctx) return await readAsDataURL(file)
      // JPEG has no alpha channel — flatten onto white or transparent PNG
      // regions would come out black.
      ctx.fillStyle = '#ffffff'
      ctx.fillRect(0, 0, w, h)
      ctx.drawImage(img, 0, 0, w, h)
      const out = canvas.toDataURL('image/jpeg', IMAGE_COMPRESS_QUALITY)
      // A rare pathological case (already-tiny JPEG) can grow under
      // re-encoding; keep whichever is smaller.
      if (out.length >= file.size * 1.37) return await readAsDataURL(file)
      return out
    } finally {
      URL.revokeObjectURL(url)
    }
  } catch {
    return readAsDataURL(file)
  }
}
