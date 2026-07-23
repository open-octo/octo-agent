---
title: "What We Fixed Before Record & Replay Survived a Real Website"
description: "A demo that looked like half an hour of work — record yourself publishing an article, replay it with a different one — broke twenty-odd times across Zhihu and Xiaohongshu. This post walks every trap in the order we hit it: noopener new tabs, frozen renderers, programmatic-click poisoning, SPA route echoes, closed shadow-DOM dead zones, click actionability, the network-idle race — up to the server-side validation that only evidence could pin down. With a diagnostic decision tree, and one honest lesson about chasing ghosts."
pubDate: 2026-07-23
author: "octo-agent team"
tags: ["deep-dive", "browser", "engineering", "octo-agent"]
locale: en
originalSlug: record-replay-in-the-wild
---

# What We Fixed Before Record & Replay Survived a Real Website

## Opening: A Demo That Should Have Taken Half an Hour

The ask was one sentence: record a real demo of octo's browser Record & Replay — I publish an article on a website by hand while octo records; then hand it a different article and let it replay the whole thing on its own.

Two real websites (Zhihu's column editor, Xiaohongshu's creator platform) later turned into twenty-odd commits spanning four layers: the recorder, the compiler, the replay engine, and the self-healer. **Not one failure was a typo in our code — every one was some real mechanism of the modern web fighting automation.** The part most worth writing down is the ending: we shipped a chain of click fixes that were each correct and each had a test, only to find they were chasing a ghost, and the actual culprit took evidence — not guessing — to catch.

This post recounts them in the order we discovered them. If you build browser automation — with octo or not — this list will probably save you a few days.

## Trap 1: Click "Write Article," and the Recorder Goes Deaf

**Symptom**: recording a publish flow on Zhihu. After clicking "Write Article" in the nav bar and landing in the editor, nothing else was recorded. The recording stopped dead the moment the new tab opened.

**Root cause**: Zhihu's editor opens in a new tab, and that tab is `noopener`. Our recorder watches for new targets with CDP's `Target.setAutoAttach` (freeze-on-create) — but that call was scoped to the **recorded page's session**, and session-level auto-attach only fires for targets Chrome considers *related*. The load-bearing fact: **since Chrome 88, `target="_blank"` implies `rel="noopener"`.** So the most ordinary "open in a new tab" link on the web creates an unrelated, standalone target, and `Target.attachedToTarget` never fires. The recorder wasn't missing new-tab handling — its handling only ever covered the small class of `window.open` popups that keep an opener (OAuth dialogs and the like).

**Fix**: during recording, also request freeze-on-create auto-attach at the **browser level** (the one with an empty `sessionId`), which fires for every new target regardless of opener. Turn it back off when recording stops.

## Trap 2: A Frozen Tab Won't Talk to You

With Trap 1 fixed, new tabs attached — but every call to inject the capture script timed out.

**Root cause**: freeze-on-create pauses a new tab at the instant of creation and resumes it once instrumentation is in place — a correct design (it stops the user from clicking a page before it's being watched). But an opener-carrying popup shares its opener's renderer and keeps answering CDP commands while frozen; a **noopener tab's fresh renderer answers nothing until it is resumed.** So "call the injection command, wait for the reply, then resume" deadlocked against its own resume.

**Fix**: the Puppeteer pattern — write the whole injection sequence to the socket **without waiting for replies**, write the resume command right after, and collect the responses afterward. CDP's wire order equals program order, so the queued injection is guaranteed to apply before any page code runs, and the anti-race freeze is preserved exactly.

A third trap fell out of this one: a single tab can carry **multiple CDP sessions** (one from auto-attach, one from an explicit attach), and one binding call on the page notifies **every** session that registered that binding — so each action was recorded twice. The dedup key has to be the target, not the session.

## Trap 4: A Self-Heal That Can't Mathematically Succeed

**Symptom**: replay fails with "no element matches the recorded fingerprint." But we *have* LLM self-heal — why didn't it fix it?

Two layers turned up. First, **observability**: self-heal had actually run and the model had answered, but on failure the code returned the original error verbatim — "heal failed" and "heal never ran" looked identical. Second, and more interesting: **succeeding didn't help either.** The healer only rewrites the selector, and on retry an anchored step has to pass the same fingerprint-scoring gate again — and the fingerprint (the recorded neighbor text, role, tag) is exactly what just failed to match. When the fingerprint itself has drifted, self-heal cannot mathematically pass.

**Fix**: the healer's replacement selector is **authoritative** — the retry uses it directly, and the stale fingerprint is dropped along with the repair and written back to the file. Every self-heal exit now names its own outcome in the error (`self-heal gave up: …`, `tried 3 rounds without success`).

## Trap 5: A Single `~` Killed the Whole Page

**Symptom**: at the upload step, Chrome threw up its "Aw, Snap! `RESULT_CODE_KILLED_BAD_MESSAGE`" page, and the whole replay hung for 400 seconds before timing out.

**Root cause**: the upload parameter's `~/Downloads/…` went verbatim to `DOM.setFileInputFiles`. Chrome's reaction to a path the browser process can't grant (an unexpanded `~`, a relative path, a nonexistent file) isn't an error — it **kills the renderer**, treating it as compromised. Worse was the cascade: a dead renderer leaves every CDP command waiting forever, and each step's wait-timeout **is itself implemented as a CDP poll**, so they all hang with it.

**Fix**: two layers. Normalize the path before it reaches Chrome (expand `~`, require absolute, check existence), turning a renderer-kill into a clean step error; and wrap every replay step in a hard-deadline backstop (8× the step timeout, or 2× the navigate timeout, whichever is larger), so a dead tab drags for at most two minutes instead of taking the whole turn down.

## Trap 6: The Page Clicks Itself

**Symptom**: switching to Xiaohongshu. Three recordings in a row compiled the upload step into an unreplayable bare `input` selector, while the actual visible upload zone the user clicked became an orphaned step.

This was the trickiest trap to crack, and it was cracked by the **raw-events sidecar** we'd added along the way (`events.json`, the unprocessed captured events written next to the compiled recording at record_stop). The event stream had an extra click nobody performed:

```
13 click  DIV    …upload-area          ← the user's real click on the zone
14 click  INPUT  input                 ← ???
15 upload INPUT  input
```

**Root cause**: when Xiaohongshu's upload zone is clicked, page JS **creates a file input dynamically and calls `.click()` on it programmatically.** That synthetic click has `isTrusted === false`, but the recorder took it anyway. The later "merge the click and the file-pick into one upload step" logic, walking backward, hit this fake click and merged the upload target down to the bare `input`.

**Fix**: capture only accepts `isTrusted` clicks and Enter keys. A programmatic event is the page's internal implementation, not the user's demonstration. (Change events stay unfiltered — select simulation and some framework controls legitimately dispatch synthetic change.)

## Trap 7: An SPA Echo Wiped Out the State

**Symptom**: replay fails to find the "New Draft" button. The screenshot shows the page stuck on the default "Upload Video" tab instead of the "Write Article" tab from the recording.

**Root cause**: clicking "Write Article" is an SPA tab switch, and the page re-announces its own URL via `pushState` (appending `&from=tab_switch`). The recorder captured that as a navigate step — and on replay that navigate **reloaded the whole page**, resetting the freshly-selected tab back to the default.

Fixing it took two passes. The first was a replay-side patch: skip the reload when the target URL equals the current location, plus a same-host grace window for the SPA's async URL update. It rescued some cases but only treated the symptom — on replay the page may not append the same query params, and if the wait times out it still force-loads. The second pass was the semantic answer: **`navigatedWithinDocument` (pushState/replaceState) can only be initiated by page JS** — an address-bar entry always triggers a full load. So every same-document navigation is an *effect* of the preceding action, not a user *action*; replay reproduces it by replaying that action, and the compiler should drop the whole class. The one known cost: browser back/forward on an SPA (which has no capturable DOM gesture anyway).

## Trap 8: A Step With No Semantics Is Beyond Saving

A root problem ran through several failures: the target element the recorder captured often had **no semantics at all** — the user clicked an svg icon inside a button, or a bare div in a portal overlay, and the recorded step was `click div:nth-of-type(4)` with no text, no role, no hint. Such a step dies the moment the page shifts, and self-heal is helpless: all the model gets is "click a div."

**Fix**, a bundle: clicks **retarget to the nearest interactive ancestor** (button/a/[role=button|menuitem|…]) before the selector and fingerprint are built — the semantics live on the ancestor; the selector builder **demotes volatile ids** (`react-aria-N`, `PopoverN-…`, `:rN:` — framework counters that change every session) in favor of aria-label/name/placeholder; and a Chinese form hint no longer slugs to an empty string and falls back to a name like `value` — `{{输入标题}}` ("enter title") is far more self-explanatory than `{{value}}`, and the model doesn't have to guess.

There's a companion lesson about params: record_stop's result originally didn't list the declared params, so when the model recited the plan to the user it would **make up a param name from memory** (calling `value` "title"), then replay with the name it invented. The fix was to spell the declared params verbatim into the tool result — where judgment is needed, feed it the evidence.

## Trap 9: Replay Is Faster Than a Human — Fast Enough to Hit States People Never See

With the first eight traps fixed, replay reliably reached the publish page: session, write-article, import document, title, body, auto-format, next — all passing. Only the last click remained: "Publish." And there it began a war of attrition — **18 steps green, and nothing in Xiaohongshu's note manager.**

The publish button `<xhs-publish-btn>` is a **closed shadow-DOM custom element**: the page context can't see inside it, `shadowRoot` returns null, `childElementCount` is forever 0, and no selector can reach the button within. For a control like this only one signal tells you it's "ready" — the standard `:defined` pseudo-class (matches only after the class upgrades); and only one way clicks it — coordinates on the host.

Around this button we added a whole **click actionability stack**, each item a real and general hardening, each with a regression test:

- **Faithful coordinates**: the recorder stores where inside the element's box the user pressed as fractions (`click_x`/`click_y`), and replay clicks that same spot rather than the geometric center — a closed-shadow host's center can be dead space.
- **Readiness wait**: before clicking, wait for `document.readyState === complete`, wait for a custom element to be `:defined`, and wait for the element and its close ancestors to leave any `disabled` / `*-loading` state.
- **Hit-test**: the click point must be one `document.elementFromPoint` actually resolves to the target — when a transition mask covers it, elementFromPoint returns the mask, so we wait for it to clear (this works through closed shadow).
- **A real gesture**: move the pointer to the target with `mouseMoved`, pause a frame-plus so pointer-entry handlers arm, then `mousePressed(buttons:1)` — a bare press/release is a no-op for a control that only activates on pointer entry.

Each fix nudged replay forward, but the publish click was **still** hit-or-miss. Hiding here is a class of bug unique to record-and-replay systems: **replay is faster than any human, fast enough to step into intermediate states people never see** — a component shell that's present but not hydrated, a page transition not yet complete, a mask still spinning. Every item above plugs one of those windows.

## Trap 10: wait-for-idle Returned Before the Request Even Started

An observation from the user pushed the fault one layer further upstream: after clicking "Next," the page hadn't changed and the "Next" button was still spinning — and step 18 (publish) **had already fired.** The "wait for network idle" between them passed instantly.

**Root cause**: in the gap before the click's triggered request had started, the network monitor saw "n=0, idle since page load" and immediately declared idle and returned. So the next step charged ahead while the page was still transitioning. This is the classic "wait for idle" race: **it declared idle without first confirming that activity had ever occurred.**

**Fix**: `WaitForNetworkIdle` now reads a monotonic activity generation and first gives activity a **bounded grace to appear** (the generation moves, or it catches the page busy) before waiting for it to settle; only if nothing starts within the grace does it take the fast path. With this in place, replay finally waits out each SPA transition — the wait that was supposed to bridge every transition all along, but structurally couldn't.

## Trap 11: A Chain of Correct Fixes, All Chasing a Ghost

By now replay reliably reached the publish page, the click actionability stack was complete, and the transition waits were solid — and the publish click **still** didn't take effect. We'd patched this one click three or four rounds; each patch was correct on its own and had a test, yet it kept finding new ways to fail.

I stopped and did what I should have done sooner: **added a temporary diagnostic** that logged which element replay actually resolved, the coordinates it clicked, what `elementFromPoint` returned there, and whether it was ready — then asked the user to replay once more. The data settled it:

```
selector=… > xhs-publish-btn  point=(649,791)
target=XHS-PUBLISH-BTN  targetRect=[229,749,680,90]
hitAtPoint=XHS-PUBLISH-BTN  sameSubtree=true  defined=true
```

Right element, point inside the host, hit-tests to the host, already `:defined` — **every gate passed, and the click still had no effect.** Meanwhile the user noticed a detail nobody had mentioned: a toast reading **"图片正在上传"** ("image still uploading").

That's where the truth flipped: **the click had been correct all along.** Publishing is an async precondition of Xiaohongshu's long-form flow — the platform's auto-generated cover image was still uploading, and the server-side **validation** blocked the publish. Our entire click actionability stack was, for this case, **chasing a ghost** — genuine, general hardening (each with a test, each making other cases sturdier), but the block here was never in the click layer; it was server-side.

What's worth remembering here isn't a patch — it's the method: **evidence beats guessing.** We ran several rounds on a "clicking too fast / missing the button" hunch until one diagnostic log plus a user's glance at a toast dragged the direction from client to server.

**Fix**: give the *decisive click* (the last click of a recording whose known `end_url` differs from where it fired — i.e. a click meant to navigate) a retry — when the outcome isn't reached, **wait for the network to settle, then click again**, until the URL actually advances or the attempts run out. The key is that it judges success by **URL advance**, not by network activity — a real page's background heartbeats and telemetry keep "did any network happen" perpetually true (exactly why an earlier network-based retry never rescued it). Publish blocked by "image uploading" → wait for the cover to finish → click again → the URL moves to `?published=true`.

## Trap 12: Replay Didn't Know It Had Failed

Trap 11 also exposed a structural problem: after the publish click no-ops, **the following wait step passes anyway** — a wait for network idle is satisfied instantly on a page where nothing happened. Replay reported every step successful when nothing had been published.

**Fix**: the recording stores the **demonstrated end URL** (`end_url` — including those pushState redirects that are correctly dropped and never compiled into steps, e.g. `…published=true`). The replay result reports `demonstrated_end_url` alongside `replay_end_url`, and on a mismatch attaches an explicit warning: verify the outcome on the page before reporting success. A mismatch doesn't fail the replay (query params can legitimately vary), but it turns a silent false success into a signal the model has to handle. The first replay after this shipped worked — octo's reply went from "replay complete ✅" to "replay complete, but there's something to confirm."

## Method: The Evidence Sidecar and Self-Service Diagnosis

Once the traps were cleared, what remained wasn't only the fixes — it was a body of observability infrastructure, itself the diagnostic method made concrete:

- **The `events.json` sidecar** — every recording writes its raw captured events next to the compiled one. Compilation is a lossy distillation; when something misbehaves, the sidecar is the only evidence that separates three failure classes: **event absent → capture gap, re-record; event present but the step is wrong → compile fault, edit the YAML; both fine → the page changed, fix the selector/waits.** Trap 6 was cracked by exactly this.
- **A per-step progress stream** — replay is no longer a 30-second black box; each step's start and every self-heal intervention reports live to the UI and the CLI.
- **Failures that name themselves** — errors carry the self-heal trace, the diagnostic file paths, and a decision tree; against a transient validation like "image uploading," or a closed-shadow control, they tell the model whether to wait and replay again or not to try clicking by text.

That last point is worth expanding: this evidence isn't only for us. octo's replay-failure output hands the model the YAML path, the events.json path, and the decision tree together — **problems the engine can't fix need judgment, and problems that need judgment need evidence.** When Record & Replay fails for a user, their octo gets the same diagnostic toolkit we used building it.

## Closing: An Adversary Checklist for Browser-Automation Authors

On that final replay, the recording octo generated entirely on its own, replayed on its own, and the article actually posted. Looking back, not one trap was a typo in our code — every one was a real mechanism of the modern web:

1. `target="_blank"` defaults to noopener (Chrome 88+); session-level auto-attach can't see it
2. a noopener new tab's renderer answers no CDP command until it's resumed
3. one target can have several sessions; a binding call broadcasts to all of them
4. `DOM.setFileInputFiles` with an ungrantable path kills the renderer instead of erroring
5. a dead renderer hangs CDP calls forever — and your timeout may itself be CDP-implemented
6. a page will click itself programmatically (`isTrusted=false` is the only dividing line)
7. an SPA's `pushState` replays the current URL — record it, and replay becomes a full page reload
8. framework counter ids (`react-aria-N`) change every session
9. closed shadow DOM retargets every event to the host; the host center may be dead space, `childCount` is forever 0, and `:defined` is the only readiness signal
10. rendered ≠ awake: clicks during `*-disabled`/`*-loading`, before hydration, or while a mask hasn't cleared are silently swallowed — and replay is always faster than a human
11. a bare press/release is a no-op for a control that activates on pointer entry — move first, pause a frame
12. "wait for network idle," if it doesn't first confirm activity happened, returns in the gap before the request starts
13. a decisive action can be blocked by a transient server-side validation ("image uploading") — detectable only by whether the outcome was reached, not by network activity, which a page's heartbeats will fool

And one methodological rule that matters more than any of the above: **when a chain of individually-correct, individually-tested fixes still won't move the same symptom, stop and gather evidence.** We chased three or four rounds of click-timing ghosts; the real culprit — a server's "image uploading" — was caught by one diagnostic log and one observation from the user.

(One test-environment trap: headless Chrome throttles JS timers, so a `setInterval`-driven animation gets stalled into looking motionless on a loaded CI runner, making tests that rely on "the element is moving" pass falsely — drive them with a CSS transition, which the rendering engine, not a JS timer, moves.)

octo-agent's Record & Replay is open source (`internal/browser/`), and every fix above ships with a regression test written against a minimal synthetic page. If you hit a trap that isn't on this list, come file an issue — the list clearly isn't finished.

**Related reading**: [octo-agent Deep Dive: The Genuinely Hard Parts of an AI Agent System](/blog/posts/en/architecture-deep-dive/) · [Octo Onboarding Series (10): Record & Replay in Practice](/blog/posts/en/onboarding-browser-record-and-replay/)
