import './app.css'
import App from './App.svelte'
import { mount } from 'svelte'
import { initTheme } from './lib/theme'
import { initFramelessDrag } from './lib/framelessDrag'

// Apply the persisted theme before first paint so there's no light-mode flash.
initTheme()

// Desktop shell on Windows/Linux: window drag + edge resize (no-op elsewhere).
initFramelessDrag()

const app = mount(App, { target: document.getElementById('app')! })

export default app
