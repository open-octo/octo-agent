// The first-run wizard renders this form with prefs hidden. Vision used to be
// sent only from inside the prefs block, so every model the wizard saved was
// stored as text-only; these pin what buildReq hands to onSubmit in that mode.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { mount, unmount, flushSync } from 'svelte'
import ModelConfigForm from './ModelConfigForm.svelte'
import type { ModelConfigInput, ProviderPreset } from '../../lib/api'
import { setLocale } from '../../lib/i18n'

const providers = [
  { id: 'custom', name: 'Custom', base_url: '', custom_endpoint: true, models: [] },
  { id: 'anthropic', name: 'Anthropic', base_url: 'https://api.anthropic.com', api: 'anthropic-messages', default_model: 'claude-x', models: ['claude-x'], model_vision: { 'claude-x': true } },
  { id: 'textonly', name: 'TextOnly', base_url: 'https://t.example/v1', default_model: 't-1', models: ['t-1'], model_vision: { 't-1': false } },
] as unknown as ProviderPreset[]

let target: HTMLElement
let app: Record<string, unknown> | null = null
let submitted: ModelConfigInput[]

function render() {
  submitted = []
  app = mount(ModelConfigForm, {
    target,
    props: { providers, showPrefs: false, submitLabel: 'Save', onSubmit: async (r: ModelConfigInput) => { submitted.push(r) } },
  }) as Record<string, unknown>
  flushSync()
}

function field(label: string): HTMLInputElement | HTMLSelectElement {
  const f = [...target.querySelectorAll('.field')].find((el) => el.querySelector('.field-label')?.textContent?.trim() === label)
  return f!.querySelector('input, select') as HTMLInputElement | HTMLSelectElement
}

function set(label: string, value: string) {
  const el = field(label)
  el.value = value
  el.dispatchEvent(new Event(el.tagName === 'SELECT' ? 'change' : 'input', { bubbles: true }))
  flushSync()
}

async function submit() {
  ;(target.querySelector('.btn-primary') as HTMLButtonElement).click()
  // handleSubmit awaits the test-connection fetch, then onSubmit.
  for (let i = 0; i < 3; i++) await new Promise((r) => setTimeout(r, 0))
  flushSync()
}

const visionSwitch = () => target.querySelector('.toggle-row [role=switch]') as HTMLButtonElement | null
const errorText = () => target.querySelector('.result.fail')?.textContent ?? ''

beforeEach(() => {
  setLocale('en')
  target = document.createElement('div')
  document.body.appendChild(target)
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ ok: true }), { status: 200 })))
})

afterEach(() => {
  if (app) unmount(app)
  app = null
  target.remove()
  vi.unstubAllGlobals()
})

describe('ModelConfigForm with prefs hidden', () => {
  it('sends a named vendor model with its catalogue vision', async () => {
    render()
    set('Provider', 'anthropic')
    set('API Key', 'sk-test')
    expect(visionSwitch()).toBeNull()
    await submit()
    expect(submitted[0]?.vision).toBe(true)
  })

  it('shows the vision switch for Custom and sends what it says', async () => {
    render()
    set('Provider', 'custom')
    set('Model', 'local-text')
    set('Base URL', 'http://x.example/v1')
    expect(visionSwitch()?.getAttribute('aria-checked')).toBe('true')
    visionSwitch()!.click()
    flushSync()
    await submit()
    expect(submitted[0]?.vision).toBe(false)
  })

  it('does not carry a text-only catalogue value over to Custom', () => {
    render()
    set('Provider', 'textonly')
    set('Provider', 'custom')
    expect(visionSwitch()?.getAttribute('aria-checked')).toBe('true')
  })

  it('sends a Custom context window, omits an empty one, and blocks an invalid one', async () => {
    render()
    set('Provider', 'custom')
    set('Model', 'big')
    set('Base URL', 'http://x.example/v1')

    set('Context window', '12')
    await submit()
    expect(submitted).toHaveLength(0)
    expect(errorText()).toContain('Context window')
    expect(fetch).not.toHaveBeenCalled()

    set('Context window', '1M')
    await submit()
    expect(submitted[0]?.context_window).toBe(1_000_000)
  })

  it('omits the window when left empty', async () => {
    render()
    set('Provider', 'custom')
    set('Model', 'big')
    set('Base URL', 'http://x.example/v1')
    await submit()
    expect(submitted[0]).toBeDefined()
    expect(submitted[0]).not.toHaveProperty('context_window')
  })
})
