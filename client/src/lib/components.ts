import type { ComponentMacros, EntryComponent, Food } from '@/types';
import { MACRO_KEYS, type MacroKey } from '@/lib/macros';
import { activeLocale, formatNumber } from '@/lib/format';
import { parseAmount } from '@/lib/mathParser';

// Pure maths for the meal detail page. A component stores nutrients per 100 g
// as captured; what a portion contributes is grams / 100 * value. An unknown
// value (null) stays unknown. Treating it as 0 would show "0 g fiber" for a
// food that simply has no fiber figure.

export const MACRO_FIELD: Record<MacroKey, keyof ComponentMacros> = {
  protein: 'protein_g',
  carbs: 'carbs_g',
  fat: 'fat_g',
  fiber: 'fiber_g',
  sugar: 'sugar_g',
};

/** Atwater factors (kcal per gram) for the energy split. Fiber is left out on purpose. */
const KCAL_PER_GRAM = { protein: 4, carbs: 4, fat: 9 } as const;

export interface PortionNutrients {
  kcal: number;
  macros: Record<MacroKey, number | null>;
}

/** What `component.grams` of the food contributes. */
export function portionNutrients(c: Pick<EntryComponent, 'grams' | 'calories_per_100g' | 'macros_per_100g'>): PortionNutrients {
  const factor = c.grams / 100;
  const macros = {} as Record<MacroKey, number | null>;
  for (const key of MACRO_KEYS) {
    const per100 = c.macros_per_100g[MACRO_FIELD[key]];
    macros[key] = per100 == null ? null : per100 * factor;
  }
  return { kcal: c.calories_per_100g * factor, macros };
}

/**
 * What `grams` of a catalog food will contribute once it is a component: the
 * same arithmetic as the saved row, so a preview matches what gets stored.
 */
export function foodPortion(food: Food, grams: number): PortionNutrients {
  return portionNutrients({ grams, calories_per_100g: food.calories_per_100g ?? 0, macros_per_100g: food.macros_per_100g });
}

export interface MacroSum {
  /** null when no component knows the value. */
  value: number | null;
  /** How many components know the value. */
  known: number;
  total: number;
}

/** Per-macro sums over the components, with how many of them knew the value. */
export function sumMacros(components: EntryComponent[]): Record<MacroKey, MacroSum> {
  const out = {} as Record<MacroKey, MacroSum>;
  for (const key of MACRO_KEYS) out[key] = { value: null, known: 0, total: components.length };
  for (const c of components) {
    const p = portionNutrients(c);
    for (const key of MACRO_KEYS) {
      const v = p.macros[key];
      if (v == null) continue;
      out[key].value = (out[key].value ?? 0) + v;
      out[key].known += 1;
    }
  }
  return out;
}

/** True when some, but not all, components know the macro: the sum is a lower bound. */
export function isIncomplete(sum: MacroSum): boolean {
  return sum.known > 0 && sum.known < sum.total;
}

/** Each component's share of the meal's calories, 0..1, in input order. */
export function kcalShares(components: EntryComponent[]): number[] {
  const kcals = components.map((c) => portionNutrients(c).kcal);
  const total = kcals.reduce((a, b) => a + b, 0);
  return kcals.map((k) => (total > 0 ? k / total : 0));
}

export interface EnergySplit {
  protein: number;
  carbs: number;
  fat: number;
}

/**
 * Share of the macro energy (protein 4, carbs 4, fat 9 kcal/g) that comes from
 * each macro, as fractions summing to 1. null when there is nothing to split.
 * Unknown macros count as 0 here: the split describes what is known.
 */
export function energySplit(macros: { protein: number | null; carbs: number | null; fat: number | null }): EnergySplit | null {
  const p = (macros.protein ?? 0) * KCAL_PER_GRAM.protein;
  const c = (macros.carbs ?? 0) * KCAL_PER_GRAM.carbs;
  const f = (macros.fat ?? 0) * KCAL_PER_GRAM.fat;
  const total = p + c + f;
  if (total <= 0) return null;
  return { protein: p / total, carbs: c / total, fat: f / total };
}

/** Macro totals from the entry payload (server-rounded grams), keyed like MACRO_KEYS. */
export function entryMacroTotals(macros: ComponentMacros): Record<MacroKey, number | null> {
  const out = {} as Record<MacroKey, number | null>;
  for (const key of MACRO_KEYS) out[key] = macros[MACRO_FIELD[key]];
  return out;
}

/** Round for display: whole numbers from 10 up, one decimal below. */
export function roundAmount(value: number): number {
  return Math.abs(value) >= 10 ? Math.round(value) : Math.round(value * 10) / 10;
}

/** roundAmount, formatted in the UI locale. */
export function formatAmount(value: number, locale: string = activeLocale()): string {
  return formatNumber(roundAmount(value), locale);
}

export const MIN_GRAMS = 0.01;
export const MAX_GRAMS = 10000;

const PLAIN_DECIMAL_RE = /^(\d+([.,]\d*)?|[.,]\d+)$/;

/** "2*45" and "12,5" both work; the server stores hundredths of a gram. */
export function parseGrams(raw: string): number | null {
  const text = raw.trim();
  let value: number;
  if (PLAIN_DECIMAL_RE.test(text)) {
    // parseAmount rounds to whole numbers, which would turn "12,5" into 13.
    value = Number(text.replace(',', '.'));
  } else {
    const parsed = parseAmount(text.replace(/,/g, '.'), { maxAbs: MAX_GRAMS });
    if (!parsed.ok) return null;
    value = parsed.value;
  }
  const grams = Math.round(value * 100) / 100;
  return grams >= MIN_GRAMS && grams <= MAX_GRAMS ? grams : null;
}

export const DEFAULT_GRAMS = 100;

/**
 * A product's serving as grams, rounded the way the server stores it. null when
 * Open Food Facts has none, or one the grams field would reject (0.005 g,
 * 25 000 g): a crowdsourced value must not become the starting amount.
 */
export function servingGrams(servingQuantity: number | null | undefined): number | null {
  if (servingQuantity == null || !(servingQuantity >= MIN_GRAMS && servingQuantity <= MAX_GRAMS)) return null;
  return parseGrams(String(servingQuantity));
}
