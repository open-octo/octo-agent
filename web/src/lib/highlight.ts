// The one place highlight.js is configured. Both consumers — markdown.ts's
// fenced code blocks and GenuiCode.svelte's `code` node — import hljs from
// here, so neither depends on the other having been loaded first to have any
// languages registered.
//
// The core build plus an explicit language list, rather than the full bundle:
// the full one carries nearly two hundred grammars for the seven that
// actually show up in this product.
import hljs from "highlight.js/lib/core"
import javascript from "highlight.js/lib/languages/javascript"
import typescript from "highlight.js/lib/languages/typescript"
import go from "highlight.js/lib/languages/go"
import python from "highlight.js/lib/languages/python"
import bash from "highlight.js/lib/languages/bash"
import json from "highlight.js/lib/languages/json"
import xml from "highlight.js/lib/languages/xml"
// Side-effect import: bundles the GitHub Dark highlight.js CSS theme so that
// hljs-* class names (hljs-keyword, hljs-string, etc.) are styled in the main
// DOM (ChatView, ProfileView, etc.). Without this, highlight() runs and
// produces the right HTML spans but the colors are invisible.
import "highlight.js/styles/github-dark.css"

hljs.registerLanguage("javascript", javascript)
hljs.registerLanguage("typescript", typescript)
hljs.registerLanguage("go", go)
hljs.registerLanguage("python", python)
hljs.registerLanguage("bash", bash)
hljs.registerLanguage("json", json)
hljs.registerLanguage("xml", xml)

export default hljs
