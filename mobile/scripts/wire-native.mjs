// Applies octo-mobile's native settings to the generated iOS/Android projects
// after `npx cap add ios` / `add android`. The generated `ios/` and `android/`
// folders are gitignored, so this must be re-run each time they are regenerated.
//
// The app has no custom native code — it is the bundled frontend talking to
// octo serve over fetch/WebSocket — so all this does is grant two things:
//   - the camera, for scanning the connect QR (getUserMedia);
//   - plain-http networking, since a user's server is often http://<LAN or
//     Tailscale IP>:8088.
//
// Every mutation is idempotent (running twice is a no-op). A step that cannot
// find its anchor does not edit blindly; it prints what to add by hand.
//
// Usage:
//   node scripts/wire-native.mjs            # wire whichever of ios/ android/ exist
//   node scripts/wire-native.mjs --ios      # iOS only
//   node scripts/wire-native.mjs --android  # Android only
import { execFileSync } from 'node:child_process'
import { existsSync, readFileSync, writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { join } from 'node:path'

const mobileRoot = fileURLToPath(new URL('..', import.meta.url))
const iosRoot = join(mobileRoot, 'ios')
const androidRoot = join(mobileRoot, 'android')

const args = new Set(process.argv.slice(2))
const onlyIos = args.has('--ios')
const onlyAndroid = args.has('--android')
const wantIos = onlyIos || !onlyAndroid
const wantAndroid = onlyAndroid || !onlyIos

const warnings = []
const warn = (msg) => warnings.push(msg)
const done = []
const did = (msg) => done.push(msg)

// ---------------------------------------------------------------- iOS
function wireIos() {
  const plist = join(iosRoot, 'App', 'App', 'Info.plist')
  if (!existsSync(plist)) {
    warn('iOS: ios/App/App/Info.plist not found — run `npx cap add ios` first, then re-run.')
    return
  }
  if (process.platform !== 'darwin') {
    warn('iOS: Info.plist edits need macOS PlistBuddy; add NSCameraUsageDescription and NSAppTransportSecurity › NSAllowsArbitraryLoads by hand.')
    return
  }
  plistUpsert(plist, ':NSCameraUsageDescription', 'string', 'Scan the connect QR code.')
  // ATS blocks plain http by default; the user's server is often http.
  plistSet(plist, ':NSAppTransportSecurity:NSAllowsArbitraryLoads', 'bool', 'true')
  did('iOS: Info.plist camera usage + NSAllowsArbitraryLoads')
}

function plistBuddy(plist, cmd) {
  execFileSync('/usr/libexec/PlistBuddy', ['-c', cmd, plist])
}
function plistHas(plist, entry) {
  try {
    execFileSync('/usr/libexec/PlistBuddy', ['-c', `Print ${entry}`, plist], { stdio: 'ignore' })
    return true
  } catch {
    return false
  }
}
function plistUpsert(plist, entry, type, value) {
  if (plistHas(plist, entry)) return
  plistBuddy(plist, `Add ${entry} ${type} ${value}`)
}
function plistSet(plist, entry, type, value) {
  if (plistHas(plist, entry)) plistBuddy(plist, `Set ${entry} ${value}`)
  else plistBuddy(plist, `Add ${entry} ${type} ${value}`)
}

// ---------------------------------------------------------------- Android
function wireAndroid() {
  const manifest = join(androidRoot, 'app', 'src', 'main', 'AndroidManifest.xml')
  if (!existsSync(manifest)) {
    warn('Android: AndroidManifest.xml not found — run `npx cap add android` first, then re-run.')
    return
  }
  let text = readFileSync(manifest, 'utf8')
  let changed = false

  // Capacitor's WebChromeClient requests CAMERA at runtime for getUserMedia,
  // but only a permission the manifest declares can be granted.
  if (!text.includes('android.permission.CAMERA')) {
    text = text.replace(
      /(<manifest[^>]*>)/,
      '$1\n    <uses-permission android:name="android.permission.CAMERA" />\n' +
        '    <uses-feature android:name="android.hardware.camera" android:required="false" />',
    )
    changed = true
    did('Android: manifest camera permission + feature')
  }

  if (!/android:usesCleartextTraffic="true"/.test(text)) {
    if (/<application\b/.test(text)) {
      text = text.replace(/<application\b/, '<application android:usesCleartextTraffic="true"')
      changed = true
      did('Android: manifest usesCleartextTraffic')
    } else {
      warn('Android: no <application> in the manifest; add android:usesCleartextTraffic="true" by hand.')
    }
  }

  if (changed) writeFileSync(manifest, text)
}

// ---------------------------------------------------------------- run
if (wantIos) wireIos()
if (wantAndroid) wireAndroid()

console.log('wire-native: done.')
for (const d of done) console.log(`  ✓ ${d}`)
if (warnings.length) {
  console.log('\nwire-native: manual steps / notes:')
  for (const w of warnings) console.log(`  • ${w}\n`)
}
