import './app.css'
import App from './App.svelte'
import { mount } from 'svelte'
import { initTheme } from './lib/theme'

// Apply the persisted theme before first paint so there's no light-mode flash.
initTheme()

const app = mount(App, { target: document.getElementById('app')! })

export default app
