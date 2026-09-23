# Mermaid diagrams in chat

A ` ```mermaid ` fence in an assistant reply renders as a diagram in the Web UI's chat. It is ordinary markdown: the model writes the fence it would write anywhere else, and every other surface (IM, the TUI, Markdown artifact previews, exported transcripts) shows the fence as a code block.

## Rendering path

1. `renderMarkdown` (`web/src/lib/markdown.ts`) renders a **closed** mermaid fence as the usual code block with an extra `mermaid-block` class. An unclosed fence — the tail of a reply still streaming — gets no class, so a half-written diagram is never handed to mermaid.
2. `setupMermaid` (`web/src/lib/mermaid.ts`) is a Svelte action on the chat's markdown containers (`.rich-answer`, `.think-body`, via `setupAssistantEl` in `ChatView.svelte`). It hydrates every untagged `mermaid-block` on mount and on each DOM mutation, since `{@html}` replaces the nodes whenever the rendered string changes.
3. Hydration appends a `.mermaid-diagram` holding the SVG and marks the block `data-mermaid-rendered`; CSS then hides the block's `<pre>`. The source stays in the DOM, so the header's Copy button still copies the diagram source.

The rendered SVG is cached by theme + source. A streamed reply re-renders its markdown every 80 ms; each pass rebuilds the block, and the cached SVG is re-attached synchronously instead of re-running mermaid, so the diagram doesn't flicker. A source mermaid rejects is cached as a failure and not retried.

## Failure behaviour

A block mermaid cannot render stays the code block it already is — no error line, nothing blanked — and logs a `console.warn`. `suppressErrorRendering: true` is required for this: without it mermaid draws its own "Syntax error" diagram and leaves it attached to `document.body`, below the whole app.

A source mermaid rejects is cached as a failure. A failure to load mermaid itself (a lazy chunk that 404s after an upgrade) is not: it says nothing about the source, and a later attempt may load.

`mermaid.initialize()` sets global config that `render()` reads while it runs, so renders go through one queue; two renders with different themes never overlap, and a diagram is never cached under the wrong theme.

## Security

The SVG reaches the document through `innerHTML`, so it is sanitized twice:

- **mermaid** runs with `securityLevel: 'strict'` and `startOnLoad: false`.
- **DOMPurify** then sanitizes the SVG under `USE_PROFILES: { svg: true, svgFilters: true }` — the project's policy, not only mermaid's.

Two settings carry weight beyond that:

- **`htmlLabels: false`.** With html labels on (mermaid's default, independent of `securityLevel`), flowchart labels render inside a `foreignObject`, which DOMPurify's SVG profile removes together with its contents. The diagram would survive as boxes and arrows with no words. `mermaid-sanitize.test.ts` pins this.
- **An extended `secure` list.** `theme`, `themeCSS` and `themeVariables` are settable from inside the source via a `%%{init: …}%%` directive and are not in mermaid's default `secure` list. `themeCSS` lands in a `<style>` that survives sanitizing and could pull an external `url()`; `theme` is locked so a diagram can't override the one matching the app.

## Theme

The diagram uses mermaid's `dark` theme when `<html data-theme="dark">`, `default` otherwise, read at render time. mermaid's light-theme lines are unreadable on a dark background and the reverse, and a finished message never re-renders, so `setupMermaid` also watches `data-theme` and redraws every diagram in its container when it changes.

## Compatibility

`mermaid` is no longer a GenUI node type. Both spec guards (`guard.ts`, `internal/tools/genui/guard.go`) turn a `mermaid` node into a `code` node with `lang: "mermaid"`, so a panel saved while the node existed shows the diagram source instead of losing it, and a model that still emits one gets the same.

## Dependency

`mermaid` is loaded with a dynamic `import()` on the first diagram, so it is emitted entirely as lazy chunks; a session that never shows a diagram never loads or parses it. `go:embed all:webdist` still ships those chunks in the binary.
