import jsQR from 'jsqr'

// scanPairingURL opens the device camera in a fullscreen overlay and scans for
// an octo-pair:// QR — the pairing code the host renders in Settings › Mobile.
// It resolves with the decoded URL string (validated only as far as the scheme;
// parsePairingURL does the real parsing) and stops the camera. It rejects if the
// user cancels or the camera can't be opened. QR decoding runs entirely in JS
// (jsQR over getUserMedia frames), so it needs no native scanner plugin — just
// the CAMERA permission the Capacitor WebView requests on first getUserMedia.
export function scanPairingURL(): Promise<string> {
  return new Promise<string>((resolve, reject) => {
    let stream: MediaStream | null = null
    let raf = 0
    const root = document.createElement('div')
    root.style.cssText =
      'position:fixed;inset:0;z-index:2147483647;background:#000;display:flex;' +
      'flex-direction:column;align-items:center;justify-content:center;'

    const video = document.createElement('video')
    video.setAttribute('playsinline', 'true')
    video.muted = true
    video.style.cssText = 'width:100%;height:100%;object-fit:cover;'
    root.appendChild(video)

    const hint = document.createElement('div')
    hint.textContent = '将 Settings › Mobile 的二维码对准取景框'
    hint.style.cssText =
      'position:absolute;top:12%;left:0;right:0;text-align:center;color:#fff;' +
      'font:15px system-ui,sans-serif;text-shadow:0 1px 3px rgba(0,0,0,.6);padding:0 24px;'
    root.appendChild(hint)

    const cancel = document.createElement('button')
    cancel.textContent = '取消'
    cancel.style.cssText =
      'position:absolute;bottom:8%;padding:12px 32px;border:0;border-radius:24px;' +
      'background:#fff;color:#111;font:600 15px system-ui,sans-serif;'
    root.appendChild(cancel)

    const canvas = document.createElement('canvas')
    const ctx = canvas.getContext('2d', { willReadFrequently: true })

    const cleanup = () => {
      cancelAnimationFrame(raf)
      stream?.getTracks().forEach((t) => t.stop())
      root.remove()
    }
    cancel.onclick = () => {
      cleanup()
      reject(new Error('scan cancelled'))
    }

    const tick = () => {
      if (video.readyState === video.HAVE_ENOUGH_DATA && ctx) {
        canvas.width = video.videoWidth
        canvas.height = video.videoHeight
        ctx.drawImage(video, 0, 0, canvas.width, canvas.height)
        const img = ctx.getImageData(0, 0, canvas.width, canvas.height)
        const code = jsQR(img.data, img.width, img.height, { inversionAttempts: 'dontInvert' })
        if (code && code.data.startsWith('octo-pair://')) {
          const url = code.data
          cleanup()
          resolve(url)
          return
        }
      }
      raf = requestAnimationFrame(tick)
    }

    document.body.appendChild(root)
    navigator.mediaDevices
      .getUserMedia({ video: { facingMode: 'environment' } })
      .then((s) => {
        stream = s
        video.srcObject = s
        return video.play()
      })
      .then(() => {
        raf = requestAnimationFrame(tick)
      })
      .catch((err) => {
        cleanup()
        reject(err instanceof Error ? err : new Error(String(err)))
      })
  })
}
