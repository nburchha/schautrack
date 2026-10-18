import { expect, type Locator } from '@playwright/test';

// The Today panel never signals a miss with colour: a min-goal that is not
// reached yet is no failure, and an exceeded limit is shown by its numbers.
// So nothing inside it may be painted red (destructive) or amber (warning).
const WARNING_COLORS = new Set([
  'rgb(239, 68, 68)', // #ef4444 destructive
  'rgb(245, 158, 11)', // #f59e0b warning
]);

/** Fails when the locator or any descendant is painted in a warning colour. */
export async function expectNoWarningColors(root: Locator) {
  const painted = await root.evaluate((el, warning) => {
    const found: string[] = [];
    for (const node of [el, ...Array.from(el.querySelectorAll('*'))]) {
      // Theme colours can compute to oklch(), so also reject status class names.
      if (/destructive|danger|warning|\bred-|\bamber-|macro-stat--/.test(node.getAttribute('class') ?? '')) {
        found.push(`${node.tagName}.class=${node.getAttribute('class')}`);
      }
      const style = getComputedStyle(node);
      for (const prop of ['color', 'backgroundColor', 'borderTopColor', 'boxShadow'] as const) {
        if (warning.some((w) => style[prop].includes(w))) found.push(`${node.tagName}.${prop}=${style[prop]}`);
      }
    }
    return found;
  }, [...WARNING_COLORS]);
  expect(painted).toEqual([]);
}
