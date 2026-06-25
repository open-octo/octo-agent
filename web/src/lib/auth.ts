// auth.ts — access-key authentication for non-loopback clients.
//
// The browser authenticates via the octo_access_key cookie: loopback visits
// never see a 401 (the server exempts loopback peers — see internal/server/
// auth.go), so the prompt only appears when the server is reached over the
// network. A ?access_key= query parameter (from the URL the server prints on
// startup) is adopted into storage and stripped from the address bar; the
// cookie then rides every same-origin fetch and the WebSocket handshake.

import { writable } from "svelte/store";

const COOKIE_NAME = "octo_access_key";
const STORAGE_KEY = "octo_access_key";
// A gated endpoint (unlike /api/health and /api/version, which are exempt) so a
// 401 here means "key needed", not "wrong path".
const PROBE_ENDPOINT = "/api/sessions?limit=1";
const MAX_PROMPT_TRIES = 3;

// Drives the AuthGate overlay. null = no prompt showing. `retry` is true once
// the user has already submitted a wrong key this session.
export const authPrompt = writable<{ retry: boolean } | null>(null);

let resolvePrompt: ((key: string | null) => void) | null = null;

// Called by the AuthGate overlay: a key (submit) or null (cancel) resolves the
// promise the check loop is awaiting.
export function submitAuthKey(key: string | null): void {
  authPrompt.set(null);
  const r = resolvePrompt;
  resolvePrompt = null;
  r?.(key);
}

function askUserForKey(retry: boolean): Promise<string | null> {
  return new Promise((resolve) => {
    resolvePrompt = resolve;
    authPrompt.set({ retry });
  });
}

function setCookie(key: string): void {
  const secure = location.protocol === "https:" ? "; Secure" : "";
  // encodeURIComponent matches the server's url.PathUnescape on read.
  document.cookie = `${COOKIE_NAME}=${encodeURIComponent(key)}; path=/; SameSite=Strict${secure}`;
}

function clearCookie(): void {
  document.cookie = `${COOKIE_NAME}=; path=/; max-age=0; SameSite=Strict`;
}

// Adopt a bootstrap-link key, then strip it so it doesn't linger in the address
// bar or browser history.
function adoptQueryKey(): void {
  const params = new URLSearchParams(location.search);
  const key = params.get("access_key");
  if (!key) return;
  localStorage.setItem(STORAGE_KEY, key);
  setCookie(key);
  params.delete("access_key");
  const qs = params.toString();
  history.replaceState(null, "", location.pathname + (qs ? `?${qs}` : "") + location.hash);
}

async function probe(): Promise<"ok" | "unauthorized" | "other"> {
  try {
    const r = await fetch(PROBE_ENDPOINT);
    if (r.ok) return "ok";
    if (r.status === 401) return "unauthorized";
    return "other";
  } catch {
    return "other";
  }
}

let checkPromise: Promise<boolean> | null = null;

// Resolve auth before the app makes any gated call. Returns false only when the
// server demands a key and the user couldn't provide a valid one.
export function checkAuth(): Promise<boolean> {
  if (!checkPromise) checkPromise = doCheck();
  return checkPromise;
}

async function doCheck(): Promise<boolean> {
  adoptQueryKey();
  // Re-seed the cookie from storage in case it was cleared.
  const stored = localStorage.getItem(STORAGE_KEY);
  if (stored) setCookie(stored);

  let status = await probe();
  // ok — or a server/network error, which is not an auth failure and gets
  // surfaced by whichever real call hits it; don't block boot on it.
  if (status !== "unauthorized") return true;

  for (let i = 0; i < MAX_PROMPT_TRIES; i++) {
    const key = await askUserForKey(i > 0);
    if (!key) break; // user cancelled
    setCookie(key);
    status = await probe();
    if (status === "ok") {
      localStorage.setItem(STORAGE_KEY, key);
      return true;
    }
  }
  clearCookie();
  localStorage.removeItem(STORAGE_KEY);
  return false;
}
