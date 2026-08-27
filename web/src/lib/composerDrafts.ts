// Where a half-typed message waits while the composer isn't on screen.
// App.svelte renders exactly one view at a time, so opening Light Apps (or
// Skills, Tasks, …) unmounts the chat view and the composer with it — the
// draft has to live somewhere the unmount can't take with it. The same shelf
// carries drafts across session switches, which is why entries are keyed by
// session id; '' is the new chat whose session doesn't exist yet.

// A staged attachment. Images carry inline as a base64 data URL (the model
// gets an image block); every other type is uploaded to the server and
// referenced by `path` (an /api/uploads/<name> URL) so the agent opens it
// with read_file/terminal — mirroring how it works against the CLI's
// filesystem. Exactly one of data_url / path is set once ready. `uploading`
// marks a placeholder whose upload is still in flight; `id` keys that
// placeholder so its async result lands on the right entry (see addAttachment).
// local_path is a real local path (native dialog on desktop, or the in-app
// file picker on localhost web) — the agent reads it in place, no upload
// (mirrors the folder picker). Set instead of data_url/path when same-machine.
export type Attachment = { id?: string; name: string; mime_type?: string; data_url?: string; path?: string; local_path?: string; uploading?: boolean }

export type Draft = { text: string; attachments: Attachment[] }

const parked = new Map<string, Draft>()

// An empty box is nothing to hold on to: dropping the entry instead of storing
// a blank one keeps the shelf from growing a row per session ever visited, and
// keeps staged images (base64 data URLs, up to 32 MB each) from outliving the
// draft that staged them.
export function parkDraft(sid: string, text: string, attachments: Attachment[]): void {
  if (text === '' && attachments.length === 0) parked.delete(sid)
  else parked.set(sid, { text, attachments })
}

// Read-and-remove: whoever takes a draft owns it from then on. A copy left
// behind would resurface later — a message typed in a not-yet-created chat and
// then sent would reappear in the NEXT new chat, attachments and all.
export function takeDraft(sid: string): Draft {
  const d = parked.get(sid)
  parked.delete(sid)
  return d ?? { text: '', attachments: [] }
}

// An attachment read/upload that resolves after the composer is gone still has
// to land on its own session's parked draft. Without this the placeholder it
// staged stays `uploading` forever — a chip with no remove button (it only
// grows one once the upload clears) that send() refuses to send past, i.e. an
// input box wedged until the page reloads.
export function patchParkedAttachments(sid: string, fn: (list: Attachment[]) => Attachment[]): void {
  const d = parked.get(sid)
  if (!d) return
  parkDraft(sid, d.text, fn(d.attachments))
}
