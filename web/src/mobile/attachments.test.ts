import { describe, it, expect, beforeEach, vi } from 'vitest'
import { get } from 'svelte/store'

const uploadFile = vi.fn()
const compressImage = vi.fn()
vi.mock('../lib/api', () => ({ uploadFile: (f: File) => uploadFile(f) }))
vi.mock('../lib/imageCompress', () => ({ compressImage: (f: File) => compressImage(f) }))

import { toasts } from '../lib/stores'
import { AttachmentTray } from './attachments.svelte'

const file = (name: string, type: string, size = 10) => new File([new Uint8Array(size)], name, { type })

// A promise the test settles by hand, to hold an upload in flight.
function deferred<T>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>((r) => (resolve = r))
  return { promise, resolve }
}

beforeEach(() => {
  uploadFile.mockReset()
  compressImage.mockReset()
  toasts.set([])
})

describe('AttachmentTray', () => {
  it('stages an image as a data URL and any other file by its upload path', async () => {
    compressImage.mockResolvedValue('data:image/jpeg;base64,AAAA')
    uploadFile.mockResolvedValue('/api/uploads/1_report.pdf')
    const tray = new AttachmentTray()
    await Promise.all([tray.add(file('shot.png', 'image/png')), tray.add(file('report.pdf', 'application/pdf'))])
    expect(tray.items.map(({ name, data_url, path, uploading }) => ({ name, data_url, path, uploading }))).toEqual([
      { name: 'shot.png', data_url: 'data:image/jpeg;base64,AAAA', path: undefined, uploading: false },
      { name: 'report.pdf', data_url: undefined, path: '/api/uploads/1_report.pdf', uploading: false },
    ])
  })

  it('refuses to send while an upload is in flight', async () => {
    const up = deferred<string>()
    uploadFile.mockReturnValue(up.promise)
    const tray = new AttachmentTray()
    const done = tray.add(file('report.pdf', 'application/pdf'))
    expect(tray.uploading).toBe(true)
    expect(tray.ready('hi')).toBe(false)
    expect(get(toasts)).toHaveLength(1)
    up.resolve('/api/uploads/1_report.pdf')
    await done
    expect(tray.ready('hi')).toBe(true)
  })

  it('refuses a batch past the WebSocket message cap', async () => {
    compressImage.mockResolvedValue('data:image/jpeg;base64,' + 'A'.repeat(3_600_000))
    const tray = new AttachmentTray()
    await tray.add(file('big.jpg', 'image/jpeg'))
    expect(tray.ready('')).toBe(false)
  })

  it('drops an upload that lands after clear()', async () => {
    const up = deferred<string>()
    uploadFile.mockReturnValue(up.promise)
    const tray = new AttachmentTray()
    const done = tray.add(file('report.pdf', 'application/pdf'))
    tray.clear()
    up.resolve('/api/uploads/1_report.pdf')
    await done
    expect(tray.items).toEqual([])
  })

  it('removes the placeholder and toasts when an upload fails', async () => {
    uploadFile.mockRejectedValue(new Error('boom'))
    const tray = new AttachmentTray()
    await tray.add(file('report.pdf', 'application/pdf'))
    expect(tray.items).toEqual([])
    expect(get(toasts)[0]?.msg).toBe('boom')
  })

  it('rejects an image format the server cannot take', async () => {
    compressImage.mockResolvedValue('data:image/svg+xml;base64,AAAA')
    const tray = new AttachmentTray()
    await tray.add(file('logo.svg', 'image/svg+xml'))
    expect(tray.items).toEqual([])
    expect(get(toasts)).toHaveLength(1)
  })

  it('rejects an oversized file without staging it', async () => {
    const tray = new AttachmentTray()
    await tray.add(file('huge.png', 'image/png', 33 * 1024 * 1024))
    expect(tray.items).toEqual([])
    expect(compressImage).not.toHaveBeenCalled()
  })
})
