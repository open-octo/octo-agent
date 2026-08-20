// Render ids for mermaid diagrams.
//
// Module-level, not per-component: mermaid.render(id, …) builds a temporary
// DOM node under that id and writes it into both the produced SVG's `id` and
// the `#id …` selectors of the <style> it embeds. Two panels — or two
// diagrams in one panel — rendering under the same id would collide in the
// DOM and cross-contaminate each other's styles. A counter that lives with
// the component instance restarts at zero for every instance, which is
// exactly the collision it looks like it prevents.
let seq = 0

export function nextMermaidId(): string {
  return `genui-mermaid-${seq++}`
}

/** Test seam. */
export function resetMermaidIds(): void {
  seq = 0
}
