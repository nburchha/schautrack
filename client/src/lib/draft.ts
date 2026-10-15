import type { EntryComponent, Food } from '@/types';
import { MACRO_KEYS, type MacroKey } from '@/lib/macros';
import { portionNutrients } from '@/lib/components';

// The entry form builds a meal before anything is saved: foods picked from the
// catalog, each with grams. These helpers give the form the same arithmetic the
// server applies when it stores the entry (grams / 100 * value per 100 g, unknown
// values skipped), so the numbers shown while drafting match what gets saved.

export interface DraftComponent {
  /** Local identity for list keys and edits; not sent to the server. */
  key: number;
  food: Food;
  grams: number;
}

/** The server caps an entry at this many components. */
export const MAX_DRAFT_COMPONENTS = 50;

const MAX_NAME = 120;

function asComponent(d: DraftComponent): EntryComponent {
  return {
    id: d.key,
    food_id: d.food.id,
    name: d.food.name,
    grams: d.grams,
    calories_per_100g: d.food.calories_per_100g ?? 0,
    macros_per_100g: d.food.macros_per_100g,
  };
}

export interface DraftTotals {
  kcal: number;
  /** null when no component knows the value. */
  macros: Record<MacroKey, number | null>;
}

export function draftTotals(draft: DraftComponent[]): DraftTotals {
  let kcal = 0;
  const macros = {} as Record<MacroKey, number | null>;
  for (const key of MACRO_KEYS) macros[key] = null;
  for (const d of draft) {
    const p = portionNutrients(asComponent(d));
    kcal += p.kcal;
    for (const key of MACRO_KEYS) {
      const v = p.macros[key];
      if (v != null) macros[key] = (macros[key] ?? 0) + v;
    }
  }
  return { kcal, macros };
}

/** The entry name used when the user typed none: the component names, in order. */
export function draftName(draft: DraftComponent[]): string {
  const names: string[] = [];
  for (const d of draft) if (!names.includes(d.food.name)) names.push(d.food.name);
  const joined = names.join(', ');
  return joined.length <= MAX_NAME ? joined : `${joined.slice(0, MAX_NAME - 1).trimEnd()}…`;
}

/** The `components` array of POST /entries. */
export function draftPayload(draft: DraftComponent[]): Array<{ food_id: number; grams: number }> {
  return draft.map((d) => ({ food_id: d.food.id, grams: d.grams }));
}
