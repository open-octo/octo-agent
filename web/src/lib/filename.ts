// Build a filename stem out of user-supplied text — a session title, for the
// transcript exports.
//
// Only what is genuinely illegal in a filename is removed. Windows' reserved
// punctuation is a superset of POSIX's (which forbids just `/` and NUL), so one
// rule serves every platform octo ships on, and the name still has to survive
// being handed to an OS save dialog that may rewrite it.
//
// What this must not do is filter down to \w. That erases a title holding no
// ASCII letters: a session called 会话导出测试 exported as `_.json`, which is the
// bug this function exists to fix.
export function filenameStem(title: string, fallback = 'session'): string {
  const cleaned = title
    // Whitespace first, and only then control characters: a newline is itself a
    // control character, so stripping those first would weld the words either
    // side of it together ("sync\nlater" -> "synclater").
    .replace(/\s+/g, ' ')
    // Illegal on Windows, plus `/` on POSIX, plus what control characters remain.
    .replace(/[<>:"/\\|?*]|\p{Cc}/gu, '')
    .trim()
    // A leading dot hides the file on POSIX; Windows silently discards trailing
    // dots and spaces, renaming the file out from under the save dialog.
    .replace(/^\.+/, '')
    .replace(/[. ]+$/, '')

  // Cap by code point rather than UTF-16 unit so truncation can't split an emoji
  // into a lone surrogate. 80 code points of CJK is 240 UTF-8 bytes, inside the
  // 255-byte name limit of every filesystem we target — and the cut can expose a
  // fresh trailing dot or space, so tidy the tail again after it.
  const capped = Array.from(cleaned).slice(0, 80).join('').replace(/[. ]+$/, '')
  return capped || fallback
}
