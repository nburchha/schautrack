import { describe, expect, it } from 'vitest';
import type { Food } from '@/types';
import { draftName, draftPayload, draftTotals, type DraftComponent } from './draft';

const food = (id: number, name: string, kcal: number, macros: Partial<Food['macros_per_100g']> = {}): Food => ({
  id, source: 'bls', source_code: null, name, source_classification: null, calories_per_100g: kcal,
  macros_per_100g: { protein_g: null, carbs_g: null, fat_g: null, fiber_g: null, sugar_g: null, ...macros },
});
const item = (key: number, f: Food, grams: number): DraftComponent => ({ key, food: f, grams });

describe('draftTotals', () => {
  it('sums grams / 100 * value, like the server', () => {
    const oats = food(1, 'Oats', 372, { protein_g: 13.5, fiber_g: 10 });
    const milk = food(2, 'Milk', 64, { protein_g: 3.4 });
    const t = draftTotals([item(1, oats, 50), item(2, milk, 200)]);
    expect(t.kcal).toBeCloseTo(314, 5);
    expect(t.macros.protein).toBeCloseTo(13.5 / 2 + 6.8, 5);
  });

  it('keeps a macro nobody knows unknown instead of zero', () => {
    const t = draftTotals([item(1, food(1, 'Oats', 372, { protein_g: 13.5 }), 50)]);
    expect(t.macros.fiber).toBeNull();
    expect(t.macros.protein).not.toBeNull();
  });

  it('adds only the components that know a value', () => {
    const a = food(1, 'A', 100, { fiber_g: 10 });
    const b = food(2, 'B', 100);
    expect(draftTotals([item(1, a, 100), item(2, b, 100)]).macros.fiber).toBe(10);
  });

  it('is zero for an empty draft', () => {
    expect(draftTotals([]).kcal).toBe(0);
  });
});

describe('draftName', () => {
  it('joins the distinct names in order', () => {
    const oats = food(1, 'Oats', 1);
    expect(draftName([item(1, oats, 10), item(2, food(2, 'Milk', 1), 10), item(3, oats, 10)])).toBe('Oats, Milk');
  });

  it('stays within the name limit', () => {
    const long = Array.from({ length: 20 }, (_, i) => item(i, food(i, `Ingredient number ${i}`, 1), 10));
    const name = draftName(long);
    expect(name.length).toBeLessThanOrEqual(120);
    expect(name.endsWith('…')).toBe(true);
  });
});

describe('draftPayload', () => {
  it('sends only the food id and grams', () => {
    expect(draftPayload([item(9, food(5, 'Oats', 1), 42.5)])).toEqual([{ food_id: 5, grams: 42.5 }]);
  });
});
