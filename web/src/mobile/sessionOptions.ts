// Model and permission-mode choices shared by the new-task sheet and the open
// chat's session settings.
import * as api from '../lib/api'

export type ModelOption = { id: string; label: string }

// Every configured model as a composite "<endpoint>::<model>" id, which
// updateSessionModel resolves. The same model on two endpoints would render two
// identical options, so duplicates are qualified with their endpoint's name
// (what the user named it in Settings; its id only for an unnamed one).
export async function loadModelOptions(): Promise<ModelOption[]> {
  const d = await api.getEndpoints()
  const flat: { id: string; label: string; endpoint: string }[] = []
  for (const e of d.endpoints ?? []) {
    const ep = e.name || e.id
    for (const m of e.models ?? []) flat.push({ id: `${e.id}::${m.model}`, label: m.model, endpoint: ep })
  }
  const seen = new Map<string, number>()
  for (const m of flat) seen.set(m.label, (seen.get(m.label) ?? 0) + 1)
  return flat.map(m => ({ id: m.id, label: seen.get(m.label)! > 1 ? `${m.label} · ${m.endpoint}` : m.label }))
}

export const PERM_MODES = [
  { m: 'interactive', key: 'm.perm_ask', note: 'm.perm_note_ask' },
  { m: 'auto', key: 'm.perm_auto', note: 'm.perm_note_auto' },
  { m: 'strict', key: 'm.perm_strict', note: 'm.perm_note_strict' },
] as const

// One of the engine modes. globalPermissionMode is seeded 'ask' and a session
// may carry none (it then follows the global), so anything else falls back.
export function normalizePermMode(mode: string | undefined, fallback = 'interactive'): string {
  return PERM_MODES.some(p => p.m === mode) ? mode! : fallback
}
