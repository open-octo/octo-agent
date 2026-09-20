import './app.css'
import App from './App.svelte'
import { mount } from 'svelte'
import { initIcons } from './lib/icons'
import { initTheme, loadUserPacks } from './lib/theme'
import { initFramelessDrag } from './lib/framelessDrag'
import { installArtifactThemeRefresh } from './lib/artifacts'
import { applyTitlebarLift } from './lib/nativeWindow'

// Register the <iconify-icon> element and its bundled icon data before first
// paint — and shut the door on Iconify's API, which the CDN build used to call
// per icon. Nothing about the UI reaches a third party any more.
initIcons()

// Apply the persisted theme before first paint so there's no light-mode flash.
initTheme()

// Discover the user's own themes (~/.octo/themes) in the background. A stored
// id belonging to one of them cannot be honoured before its stylesheet exists,
// so loadUserPacks re-applies the choice once it has linked them; a user on a
// built-in pack never notices this ran.
void loadUserPacks()

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
