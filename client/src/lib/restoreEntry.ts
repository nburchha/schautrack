import type { createEntry, NewComponent } from '@/api/entries';
import type { Entry, EntryDetail } from '@/types';
import { localDateOf } from '@/lib/eatenAt';

type CreateBody = Parameters<typeof createEntry>[0];

/**
 * The POST /entries body that recreates a deleted entry, for Undo.
 *
 * `detail` is the entry as the server had it (components and the exact time
 * eaten). It is optional: without it the entry comes back as a direct entry at
 * the time of the undo, which is all the day list itself knows.
 *
 * Catalog components come back as food + grams, so they take the catalog's
 * current values per 100 g; components without a food (ad hoc, or whose food was
 * deleted) keep the values they were saved with.
 */
export function restorePayload(entry: Entry, detail: EntryDetail | null, tz: string | null | undefined): CreateBody {
  const body: CreateBody = { entry_date: entry.date };
  if (entry.name) body.entry_name = entry.name;

  // The server derives the day from eaten_at. An entry moved to another day
  // without a time falls back to its creation time, which can lie on a different
  // day than the entry; sending that would be refused as inconsistent.
  if (detail?.eaten_at && localDateOf(detail.eaten_at, tz) === entry.date) {
    body.eaten_at = detail.eaten_at;
  }

  if (detail && detail.components.length > 0) {
    body.components = detail.components.map((c): NewComponent =>
      c.food_id != null
        ? { food_id: c.food_id, grams: c.grams }
        : {
            name: c.name,
            grams: c.grams,
            calories_per_100g: c.calories_per_100g,
            protein_g: c.macros_per_100g.protein_g,
            carbs_g: c.macros_per_100g.carbs_g,
            fat_g: c.macros_per_100g.fat_g,
            fiber_g: c.macros_per_100g.fiber_g,
            sugar_g: c.macros_per_100g.sugar_g,
          });
    return body;
  }

  if (entry.amount) body.amount = entry.amount;
  const m = entry.macros;
  if (m) {
    if (m.protein != null) body.protein_g = m.protein;
    if (m.carbs != null) body.carbs_g = m.carbs;
    if (m.fat != null) body.fat_g = m.fat;
    if (m.fiber != null) body.fiber_g = m.fiber;
    if (m.sugar != null) body.sugar_g = m.sugar;
  }
  return body;
}
