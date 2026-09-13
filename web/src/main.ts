import './app.css'
import App from './App.svelte'
import { mount } from 'svelte'
import { initTheme } from './lib/theme'
import { initFramelessDrag } from './lib/framelessDrag'
import { installArtifactThemeRefresh } from './lib/artifacts'
import { applyTitlebarLift } from './lib/nativeWindow'

// Apply the persisted theme before first paint so there's no light-mode flash.
initTheme()

// Desktop shell on macOS: pin the titlebar rows' axis to the traffic lights
// before first paint — the inset and padding would otherwise wait on
// /api/version and flash the rows crammed under the lights at startup.
// No-op elsewhere.
applyTitlebarLift()

// Rebuild baked-theme artifact previews whenever the resolved theme changes.
installArtifactThemeRefresh()

// Desktop shell on Windows/Linux: window drag + edge resize (no-op elsewhere).
initFramelessDrag()

const app = mount(App, { target: document.getElementById('app')! })

export default app
