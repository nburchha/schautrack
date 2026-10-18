import { describe, it, expect } from 'vitest';
import type { EntryComponent, Food } from '@/types';
import {
  portionNutrients, sumMacros, isIncomplete, kcalShares, energySplit, entryMacroTotals, formatAmount, foodPortion, servingGrams, parseGrams,
} from './components';

function comp(over: Partial<EntryComponent> & { grams: number; calories_per_100g: number }): EntryComponent {
  return {
    id: 1,
    food_id: 1,
    name: 'x',
    macros_per_100g: { protein_g: null, carbs_g: null, fat_g: null, fiber_g: null, sugar_g: null },
    ...over,
  };
}

const oats = comp({
  id: 1, grams: 50, calories_per_100g: 372,
  macros_per_100g: { protein_g: 13.5, carbs_g: 58, fat_g: 7, fiber_g: 10, sugar_g: 1 },
});
const milk = comp({ id: 2, food_id: null, grams: 200, calories_per_100g: 64 });

describe('portionNutrients', () => {
  it('scales per-100 g values by grams', () => {
    const p = portionNutrients(oats);
    expect(p.kcal).toBeCloseTo(186);
    expect(p.macros.protein).toBeCloseTo(6.75);
    expect(p.macros.fiber).toBeCloseTo(5);
  });

  it('keeps an unknown value unknown instead of 0', () => {
    expect(portionNutrients(milk).macros.fiber).toBeNull();
    expect(portionNutrients(milk).kcal).toBeCloseTo(128);
  });
});

describe('sumMacros', () => {
  it('adds the known values and counts who knew them', () => {
    const sum = sumMacros([oats, milk]);
    expect(sum.fiber).toEqual({ value: 5, known: 1, total: 2 });
    expect(isIncomplete(sum.fiber)).toBe(true);
  });

  it('is null when no component knows the macro', () => {
    const sum = sumMacros([milk]);
    expect(sum.protein.value).toBeNull();
    expect(isIncomplete(sum.protein)).toBe(false);
  });

  it('is complete when every component knows it', () => {
    const sum = sumMacros([oats, oats]);
    expect(sum.carbs.value).toBeCloseTo(58);
    expect(isIncomplete(sum.carbs)).toBe(false);
  });
});

describe('kcalShares', () => {
  it('splits the meal by calories', () => {
    const [a, b] = kcalShares([oats, milk]);
    expect(a).toBeCloseTo(186 / 314);
    expect(a + b).toBeCloseTo(1);
  });

  it('is all zero for a meal without calories', () => {
    expect(kcalShares([comp({ grams: 100, calories_per_100g: 0 })])).toEqual([0]);
    expect(kcalShares([])).toEqual([]);
  });
});

describe('energySplit', () => {
  it('uses 4/4/9 kcal per gram', () => {
    // 10 g protein = 40, 10 g carbs = 40, 10 g fat = 90 -> 170 kcal
    const s = energySplit({ protein: 10, carbs: 10, fat: 10 });
    expect(s?.protein).toBeCloseTo(40 / 170);
    expect(s?.fat).toBeCloseTo(90 / 170);
    expect((s?.protein ?? 0) + (s?.carbs ?? 0) + (s?.fat ?? 0)).toBeCloseTo(1);
  });

  it('treats unknown as 0 and returns null when nothing is known', () => {
    expect(energySplit({ protein: 10, carbs: null, fat: null })?.protein).toBe(1);
    expect(energySplit({ protein: null, carbs: null, fat: null })).toBeNull();
  });
});

describe('entryMacroTotals', () => {
  it('maps the API field names onto macro keys', () => {
    const t = entryMacroTotals({ protein_g: 7, carbs_g: null, fat_g: 3, fiber_g: 5, sugar_g: null });
    expect(t).toEqual({ protein: 7, carbs: null, fat: 3, fiber: 5, sugar: null });
  });
});

describe('formatAmount', () => {
  it('shows one decimal below 10 and whole numbers above', () => {
    expect(formatAmount(6.75, 'en')).toBe('6.8');
    expect(formatAmount(0.04, 'en')).toBe('0');
    expect(formatAmount(12.4, 'en')).toBe('12');
    expect(formatAmount(250, 'en')).toBe('250');
    expect(formatAmount(6.75, 'de')).toBe('6,8');
  });
});

describe('foodPortion', () => {
  it('equals what the saved component row will show', () => {
    const food: Food = {
      id: 9, source: 'off', source_code: '4311501062449', name: 'Skyr', source_classification: null,
      calories_per_100g: 63, macros_per_100g: { protein_g: 11, carbs_g: 4, fat_g: 0.2, fiber_g: null, sugar_g: 4 },
    };
    const saved = portionNutrients(comp({ grams: 90, calories_per_100g: 63, macros_per_100g: food.macros_per_100g }));
    expect(foodPortion(food, 90)).toEqual(saved);
    expect(foodPortion(food, 90).macros.fiber).toBeNull();
  });
});

describe('servingGrams', () => {
  it('rounds a usable serving to the hundredths the server stores', () => {
    expect(servingGrams(150)).toBe(150);
    expect(servingGrams(33.333333)).toBe(33.33);
  });

  it('rejects what the grams field would reject', () => {
    expect(servingGrams(0.005)).toBeNull();
    expect(servingGrams(25000)).toBeNull();
    expect(servingGrams(0)).toBeNull();
    expect(servingGrams(-3)).toBeNull();
    expect(servingGrams(Number.NaN)).toBeNull();
    expect(servingGrams(null)).toBeNull();
    expect(servingGrams(undefined)).toBeNull();
  });
});

describe('parseGrams', () => {
  it('keeps the hundredths of a plain decimal, with comma or point', () => {
    expect(parseGrams('12,5')).toBe(12.5);
    expect(parseGrams('0.25')).toBe(0.25);
    expect(parseGrams('.5')).toBe(0.5);
    expect(parseGrams(' 33.333 ')).toBe(33.33);
  });

  it('evaluates math and rejects what the server would', () => {
    expect(parseGrams('2*45')).toBe(90);
    for (const bad of ['', 'abc', '0', '0.001', '-5', '10000.01', '20000']) expect(parseGrams(bad)).toBeNull();
    expect(parseGrams('10000')).toBe(10000);
  });
});
