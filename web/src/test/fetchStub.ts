// Stand-ins for fetch results in tests.
//
// **Never build a real `Response` around a binary body.** jsdom provides no
// fetch, so `new Response(bytes)` is undici's — and on Node 22 its `.blob()`
// returns undici's private Blob, which jsdom's FileReader rejects on a brand
// check ("parameter 1 is not of type 'Blob'"). Node 26 unified the two classes,
// so the failure appears only on CI, usually as an assertion about something
// else entirely while the real exception is swallowed by a `catch`.
//
// This has cost four rounds now (#1925, #1930, #1931, #2501), each time because
// the helper that fixes it lived as a local function in whichever test file had
// been burned last. It lives here so the next one is an import.
//
// A text body is fine either way: `new Response('# hello')` never touches Blob.

/** A successful response whose body is a Blob minted in the test's own realm. */
export function blobResponse(body: BlobPart, type = 'image/png'): Response {
  return {
    ok: true,
    status: 200,
    blob: async () => new Blob([body], { type }),
  } as unknown as Response
}

/** A failed response. `blob()` rejects, so a caller that ignores `ok` still fails. */
export function errorResponse(status = 404): Response {
  return {
    ok: false,
    status,
    blob: async () => {
      throw new Error(`no body: HTTP ${status}`)
    },
  } as unknown as Response
}
