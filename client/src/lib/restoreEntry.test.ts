import { describe, expect, it } from 'vitest';
import type { Entry, EntryDetail } from '@/types';
import { restorePayload } from './restoreEntry';

const entry: Entry = {
  id: 7, date: '2026-08-30', time: '07:30', amount: 314, name: 'Porridge',
  macros: { protein: 14, carbs: 39, fat: null, fiber: 5, sugar: null }, componentCount: 2,
};

const detail = (over: Partial<EntryDetail> = {}): EntryDetail => ({
  id: 7, date: '2026-08-30', calories: 314, name: 'Porridge', eaten_at: '2026-08-30T05:30:00Z', local_time: '7:30 AM',
  macros: { protein_g: 14, carbs_g: 39, fat_g: null, fiber_g: 5, sugar_g: null },
  components: [
    { id: 1, food_id: 5, name: 'Oats', grams: 50, calories_per_100g: 372, macros_per_100g: { protein_g: 13.5, carbs_g: 58, fat_g: 7, fiber_g: 10, sugar_g: 1 } },
    { id: 2, food_id: null, name: 'Milk', grams: 200, calories_per_100g: 64, macros_per_100g: { protein_g: 3.4, carbs_g: 5, fat_g: 3.5, fiber_g: null, sugar_g: 5 } },
  ],
  ...over,
});

describe('restorePayload', () => {
  it('rebuilds a meal from its components and keeps the time eaten', () => {
    const body = restorePayload(entry, detail(), 'Europe/Berlin');
    expect(body.entry_date).toBe('2026-08-30');
    expect(body.entry_name).toBe('Porridge');
    expect(body.eaten_at).toBe('2026-08-30T05:30:00Z');
    expect(body.components).toEqual([
      { food_id: 5, grams: 50 },
      { name: 'Milk', grams: 200, calories_per_100g: 64, protein_g: 3.4, carbs_g: 5, fat_g: 3.5, fiber_g: null, sugar_g: 5 },
    ]);
  });

  it('leaves the sums out when there are components, because the server computes them', () => {
    const body = restorePayload(entry, detail(), 'Europe/Berlin');
    expect(body.amount).toBeUndefined();
    expect(body.protein_g).toBeUndefined();
  });

  it('restores a direct entry from its sums', () => {
    const body = restorePayload({ ...entry, componentCount: 0 }, detail({ components: [] }), 'Europe/Berlin');
    expect(body.components).toBeUndefined();
    expect(body.amount).toBe(314);
    expect(body).toMatchObject({ protein_g: 14, carbs_g: 39, fiber_g: 5 });
    expect(body.fat_g).toBeUndefined();
  });

  it('works without the server detail, as a direct entry', () => {
    const body = restorePayload(entry, null, 'Europe/Berlin');
    expect(body.eaten_at).toBeUndefined();
    expect(body.amount).toBe(314);
  });

  it('drops a time that lies on another day than the entry', () => {
    // Entry moved to the 30th without a time: it falls back to its creation time on the 31st.
    const body = restorePayload(entry, detail({ eaten_at: '2026-08-31T10:00:00Z', components: [] }), 'Europe/Berlin');
    expect(body.eaten_at).toBeUndefined();
    expect(body.entry_date).toBe('2026-08-30');
  });

  it('compares the day in the account\'s time zone, not UTC', () => {
    // 22:30 UTC on the 30th is already the 31st in Berlin.
    const body = restorePayload({ ...entry, date: '2026-08-31' }, detail({ eaten_at: '2026-08-30T22:30:00Z', components: [] }), 'Europe/Berlin');
    expect(body.eaten_at).toBe('2026-08-30T22:30:00Z');
  });
});
