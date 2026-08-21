// Test-only helper: a $state-backed props object, so component tests can
// mutate props after mount() and observe the component react. Runes compile
// only inside .svelte / .svelte.(js|ts) modules, which is why this one-liner
// cannot live in the *.test.ts file that uses it.
export function reactiveProps<T extends Record<string, unknown>>(initial: T): T {
  const props = $state(initial)
  return props
}
