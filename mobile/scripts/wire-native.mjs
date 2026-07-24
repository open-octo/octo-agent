// Wires the native OctoTunnel plugin into the generated iOS/Android projects
// after `npx cap add ios` / `add android`. The generated `ios/` and `android/`
// folders are gitignored, so this must be re-run each time they are regenerated.
// It automates mobile/native/README.md — the source of truth for what each step
// does and why.
//
// Design: every mutation is idempotent (running twice is a no-op) and safe. A
// step that copies a file always succeeds; a step that injects into a generated
// file (Gradle / Manifest / Info.plist) first checks whether it is already there,
// and if it cannot find its anchor it does NOT blindly edit — it records a
// warning with the exact snippet to add by hand and moves on. One step is left
// to the human: the iOS plugin-instance registration (a CAPBridgeViewController
// subclass + storyboard custom class) depends on the generated project layout and
// the local toolchain (see the storyboard note in native/README.md), so we detect
// and instruct rather than rewrite it.
//
// Usage:
//   node scripts/wire-native.mjs            # wire whichever of ios/ android/ exist
//   node scripts/wire-native.mjs --ios      # iOS only
//   node scripts/wire-native.mjs --android  # Android only
//   node scripts/wire-native.mjs --local    # also inject the local-testing
//                                           # cleartext switches (NEVER for release)
import { execFileSync } from 'node:child_process'
import { existsSync, readFileSync, writeFileSync, mkdirSync, copyFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const mobileRoot = fileURLToPath(new URL('..', import.meta.url))
const nativeDir = join(mobileRoot, 'native')
const iosRoot = join(mobileRoot, 'ios')
const androidRoot = join(mobileRoot, 'android')

const args = new Set(process.argv.slice(2))
const local = args.has('--local')
const onlyIos = args.has('--ios')
const onlyAndroid = args.has('--android')
const wantIos = onlyIos || !onlyAndroid
const wantAndroid = onlyAndroid || !onlyIos

const warnings = []
const warn = (msg) => warnings.push(msg)
const done = []
const did = (msg) => done.push(msg)

// injectOnce inserts `insert` into the file at `path` only when `marker` is
// absent. It splices `insert` right after the first line matching `anchor`. If
// the anchor is missing it records a warning with the snippet and leaves the file
// untouched. Returns true when the file already had it or was edited, false when
// the anchor was not found.
function injectOnce(path, { marker, anchor, insert, label }) {
  const text = readFileSync(path, 'utf8')
  if (text.includes(marker)) return true
  const lines = text.split('\n')
  const idx = lines.findIndex((l) => anchor.test(l))
  if (idx === -1) {
    warn(`${label}: could not find anchor in ${path}. Add manually:\n${insert}`)
    return false
  }
  lines.splice(idx + 1, 0, insert)
  writeFileSync(path, lines.join('\n'))
  did(label)
  return true
}

// ---------------------------------------------------------------- iOS
function wireIos() {
  if (!existsSync(iosRoot)) {
    warn('iOS: ios/ not found — run `npx cap add ios` first, then re-run.')
    return
  }
  const appDir = join(iosRoot, 'App', 'App')
  if (!existsSync(appDir)) {
    warn(`iOS: ${appDir} not found — is this a Capacitor-generated ios/ project?`)
    return
  }

  // 1. Plugin source.
  const dst = join(appDir, 'OctoTunnelPlugin.swift')
  copyFileSync(join(nativeDir, 'OctoTunnelPlugin.swift'), dst)
  did(`iOS: copied OctoTunnelPlugin.swift -> ${dst}`)

  // 3. Info.plist — camera usage + octo-pair URL scheme (+ optional cleartext).
  //    PlistBuddy ships with macOS; on other hosts we can only instruct.
  const plist = join(appDir, 'Info.plist')
  if (process.platform !== 'darwin') {
    warn('iOS: Info.plist edits need macOS PlistBuddy; skipped. See native/README.md step 3-4.')
  } else if (existsSync(plist)) {
    plistUpsert(plist, ':NSCameraUsageDescription', 'string', 'Scan the pairing QR code.')
    // CFBundleURLTypes -> one entry declaring the octo-pair scheme.
    plistEnsureUrlScheme(plist, 'octo-pair')
    if (local) {
      plistSet(plist, ':NSAppTransportSecurity:NSAllowsLocalNetworking', 'bool', 'true')
      did('iOS: injected NSAllowsLocalNetworking (LOCAL testing only)')
    }
    did('iOS: Info.plist camera usage + octo-pair scheme ensured')
  } else {
    warn(`iOS: ${plist} not found; add NSCameraUsageDescription + CFBundleURLTypes by hand.`)
  }

  // 2. Plugin registration — left to the human (toolchain/storyboard dependent).
  warn(
    'iOS: register the plugin instance MANUALLY (native/README.md step 2): subclass\n' +
      '  CAPBridgeViewController, override capacitorDidLoad() with\n' +
      '  `bridge?.registerPluginInstance(OctoTunnelPlugin())`, and use that subclass as\n' +
      "  the bridge view controller (Main.storyboard custom class, or programmatic root VC).",
  )
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
function plistEnsureUrlScheme(plist, scheme) {
  // Idempotent: only build CFBundleURLTypes[0] when it is absent.
  if (plistHas(plist, ':CFBundleURLTypes:0:CFBundleURLSchemes:0')) return
  plistBuddy(plist, 'Add :CFBundleURLTypes array')
  plistBuddy(plist, 'Add :CFBundleURLTypes:0 dict')
  plistBuddy(plist, 'Add :CFBundleURLTypes:0:CFBundleURLSchemes array')
  plistBuddy(plist, `Add :CFBundleURLTypes:0:CFBundleURLSchemes:0 string ${scheme}`)
}

// ---------------------------------------------------------------- Android
function wireAndroid() {
  if (!existsSync(androidRoot)) {
    warn('Android: android/ not found — run `npx cap add android` first, then re-run.')
    return
  }

  // 3. Plugin source into the app package.
  const pkgDir = join(androidRoot, 'app', 'src', 'main', 'java', 'dev', 'octo', 'mobile')
  mkdirSync(pkgDir, { recursive: true })
  const dst = join(pkgDir, 'OctoTunnelPlugin.kt')
  copyFileSync(join(nativeDir, 'OctoTunnelPlugin.kt'), dst)
  did(`Android: copied OctoTunnelPlugin.kt -> ${dst}`)

  // 1. Kotlin support in the root build.gradle (Java-only template).
  const rootGradle = join(androidRoot, 'build.gradle')
  if (existsSync(rootGradle)) {
    injectOnce(rootGradle, {
      label: 'Android: kotlin-gradle-plugin classpath',
      marker: 'kotlin-gradle-plugin',
      anchor: /dependencies\s*\{/, // buildscript { dependencies { ... } }
      insert: "        classpath 'org.jetbrains.kotlin:kotlin-gradle-plugin:1.9.22'",
    })
  } else {
    warn(`Android: ${rootGradle} not found.`)
  }

  // 1+2. app/build.gradle — kotlin plugin, jvmTarget, and dependencies.
  const appGradle = join(androidRoot, 'app', 'build.gradle')
  if (existsSync(appGradle)) {
    injectOnce(appGradle, {
      label: 'Android: apply kotlin-android',
      marker: "apply plugin: 'kotlin-android'",
      anchor: /apply plugin: 'com\.android\.application'/,
      insert: "apply plugin: 'kotlin-android'",
    })
    injectOnce(appGradle, {
      label: 'Android: kotlinOptions jvmTarget',
      marker: 'kotlinOptions',
      anchor: /^android\s*\{/m,
      // Must match javac's target, which defaults to the running JDK —
      // Android Studio's bundled JBR is 21, and AGP hard-fails the build on
      // a kotlinc/javac target mismatch.
      insert: '    kotlinOptions { jvmTarget = "21" }',
    })
    injectOnce(appGradle, {
      label: 'Android: dependencies (kotlin-stdlib, noise-java, okhttp)',
      marker: 'com.github.auties00:noise-java',
      anchor: /^dependencies\s*\{/m,
      insert:
        '    implementation "org.jetbrains.kotlin:kotlin-stdlib:1.9.22"\n' +
        '    implementation "com.github.auties00:noise-java:1.2"\n' +
        '    implementation "com.squareup.okhttp3:okhttp:4.12.0"',
    })
  } else {
    warn(`Android: ${appGradle} not found.`)
  }

  // 3. Register the plugin in MainActivity before super.onCreate.
  const mainActivity = join(
    androidRoot, 'app', 'src', 'main', 'java', 'dev', 'octo', 'mobile', 'MainActivity.java',
  )
  if (existsSync(mainActivity)) {
    const text = readFileSync(mainActivity, 'utf8')
    if (text.includes('registerPlugin(OctoTunnelPlugin.class)')) {
      // already wired
    } else if (/extends BridgeActivity\s*\{\}/.test(text)) {
      // Capacitor 7's template is a bodyless one-liner with no onCreate —
      // expand it with the registration BEFORE super.onCreate, which is
      // where the bridge expects plugins to exist.
      const expanded = text
        .replace(
          /^import com\.getcapacitor\.BridgeActivity;/m,
          'import android.os.Bundle;\n\nimport com.getcapacitor.BridgeActivity;',
        )
        .replace(
          /extends BridgeActivity\s*\{\}/,
          'extends BridgeActivity {\n' +
            '    @Override\n' +
            '    public void onCreate(Bundle savedInstanceState) {\n' +
            '        // Must run before super.onCreate so the bridge sees the plugin\n' +
            '        // when it boots the webview (see mobile/native/README.md).\n' +
            '        registerPlugin(OctoTunnelPlugin.class);\n' +
            '        super.onCreate(savedInstanceState);\n' +
            '    }\n' +
            '}',
        )
      writeFileSync(mainActivity, expanded)
      did('Android: registerPlugin in MainActivity (expanded bodyless template)')
    } else {
      injectOnce(mainActivity, {
        label: 'Android: registerPlugin in MainActivity',
        marker: 'registerPlugin(OctoTunnelPlugin.class)',
        anchor: /super\.onCreate/,
        insert: '        registerPlugin(OctoTunnelPlugin.class);',
      })
      // The anchor inserts AFTER super.onCreate; native/README wants it
      // before, so if we injected, warn to move it up one line.
      warn('Android: ensure `registerPlugin(OctoTunnelPlugin.class);` sits BEFORE super.onCreate in MainActivity.')
    }
  } else {
    warn(`Android: ${mainActivity} not found (package may differ); register the plugin by hand.`)
  }

  // 4 (+5). AndroidManifest.xml — camera, deep-link intent-filter, cleartext.
  const manifest = join(androidRoot, 'app', 'src', 'main', 'AndroidManifest.xml')
  if (existsSync(manifest)) wireManifest(manifest)
  else warn(`Android: ${manifest} not found.`)
}

function wireManifest(manifest) {
  let text = readFileSync(manifest, 'utf8')
  let changed = false

  if (!text.includes('android.permission.CAMERA')) {
    text = text.replace(
      /(<manifest[^>]*>)/,
      '$1\n    <uses-permission android:name="android.permission.CAMERA" />\n' +
        '    <uses-feature android:name="android.hardware.camera" android:required="false" />',
    )
    changed = true
    did('Android: manifest camera permission + feature')
  }

  if (local && !/android:usesCleartextTraffic="true"/.test(text)) {
    text = text.replace(/<application\b/, '<application android:usesCleartextTraffic="true"')
    changed = true
    did('Android: manifest usesCleartextTraffic (LOCAL testing only)')
  }

  if (!text.includes('octo-pair')) {
    const intentFilter =
      '            <intent-filter>\n' +
      '                <action android:name="android.intent.action.VIEW" />\n' +
      '                <category android:name="android.intent.category.DEFAULT" />\n' +
      '                <category android:name="android.intent.category.BROWSABLE" />\n' +
      '                <data android:scheme="octo-pair" />\n' +
      '            </intent-filter>'
    // Insert just before the MainActivity's closing tag.
    if (/<\/activity>/.test(text)) {
      text = text.replace(/(\s*)<\/activity>/, `\n${intentFilter}$1</activity>`)
      changed = true
      did('Android: manifest octo-pair deep-link intent-filter')
    } else {
      warn('Android: no <activity> found in manifest; add the octo-pair intent-filter by hand.')
    }
  }

  if (changed) writeFileSync(manifest, text)
}

// ---------------------------------------------------------------- run
if (!existsSync(nativeDir)) {
  console.error(`wire-native: ${nativeDir} not found — run from the mobile/ project.`)
  process.exit(1)
}
if (wantIos) wireIos()
if (wantAndroid) wireAndroid()

console.log('wire-native: done.')
for (const d of done) console.log(`  ✓ ${d}`)
if (warnings.length) {
  console.log('\nwire-native: manual steps / notes:')
  for (const w of warnings) console.log(`  • ${w}\n`)
}
if (local) {
  console.log('NOTE: --local injected cleartext switches for simulator/emulator testing.')
  console.log('      These MUST be removed before any release build.')
}
