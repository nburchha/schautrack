import { test, expect } from '@playwright/test';
import { psql, createIsolatedUser, loginUser, openAddFood } from './fixtures/helpers';

// Tracking from the catalog: search foods inside the add-food form, build a
// meal from them with grams, pick when it was eaten, and undo a delete that
// carried components. Foods are seeded as the user's own under made-up names,
// so the spec neither depends on the BLS import nor changes when it has been run.

const TODAY = new Date().toLocaleDateString('en-CA', { timeZone: 'UTC' });
const PAST = '2026-01-15';

let user: { email: string; password: string; id: string };

function seedFood(name: string, kcal: number, protein: number): void {
  psql(
    `INSERT INTO foods (user_id, source, name, kcal_100g, protein_100g) VALUES (${user.id}, 'own', :'name', ${kcal}, ${protein})`,
    { name },
  );
}

async function pickFood(page: import('@playwright/test').Page, query: string, grams: string) {
  const input = page.getByTestId('food-search-input');
  await input.fill(query);
  await page.getByTestId('food-search-result').first().click();
  await page.getByTestId('food-search-grams').fill(grams);
  await page.getByTestId('food-search-add').click();
}

test.describe('Entry form with the food catalog', () => {
  test.beforeAll(() => {
    user = createIsolatedUser('entry-catalog');
    seedFood('Zyx Flocken Zart', 372, 13.5);
    seedFood('Qjk banane roh', 90, 1.1);
  });

  test.beforeEach(() => {
    psql(`DELETE FROM calorie_entries WHERE user_id = ${user.id}`);
  });

  test.afterAll(() => {
    if (!user?.id) return;
    psql(`DELETE FROM calorie_entries WHERE user_id = ${user.id}`);
    psql(`DELETE FROM foods WHERE user_id = ${user.id}`);
  });

  test('builds a meal from searched foods and tracks it with components', async ({ browser }) => {
    const { context, page } = await loginUser(browser, user.email, user.password);
    await page.goto('/dashboard');
    await openAddFood(page);

    await pickFood(page, 'ZyxFlocken', '50');
    await pickFood(page, 'Qjkbanane', '120');

    await expect(page.getByTestId('draft-component')).toHaveCount(2);
    // 50 g oats (186) + 120 g banana (108); the totals are read-only.
    await expect(page.locator('#entry-calories')).toHaveValue('294');
    await expect(page.locator('#entry-calories')).toHaveAttribute('readonly', '');

    // Changing the grams of a draft row updates the total.
    await page.getByTestId('draft-component-grams').first().fill('100');
    await expect(page.locator('#entry-calories')).toHaveValue('480');

    await page.locator('form button[type="submit"]').click();
    await expect(page.getByText('Entry tracked')).toBeVisible();

    const row = page.getByTestId('entry-row').filter({ hasText: 'Zyx Flocken Zart, Qjk banane roh' });
    await expect(row.getByTestId('entry-component-count')).toHaveText('2 components');
    await expect(row).toContainText('480');

    await row.getByTestId('entry-open').click();
    await expect(page.getByTestId('meal-component')).toHaveCount(2);
    await context.close();
  });

  test('another day starts at 09:00 and the time is stored', async ({ browser }) => {
    const { context, page } = await loginUser(browser, user.email, user.password);
    await page.goto('/dashboard');
    await openAddFood(page);

    await expect(page.getByTestId('entry-form-time-input')).toHaveValue('');
    await page.locator('form input[type="date"]').fill(PAST);
    await expect(page.getByTestId('entry-form-time-input')).toHaveValue('09:00');

    await page.locator('form input[placeholder="Breakfast, snack..."]').fill('Old dinner');
    await page.locator('#entry-calories').fill('650');
    await page.locator('form button[type="submit"]').click();
    await expect(page.getByText('Entry tracked')).toBeVisible();

    const stored = psql(
      `SELECT entry_date::text || ' ' || to_char(eaten_at AT TIME ZONE 'UTC', 'HH24:MI') FROM calorie_entries WHERE user_id = ${user.id} AND entry_name = 'Old dinner'`,
    );
    expect(stored).toBe(`${PAST} 09:00`);
    await context.close();
  });

  test('today without a time leaves the eaten time to the moment of tracking', async ({ browser }) => {
    const { context, page } = await loginUser(browser, user.email, user.password);
    await page.goto('/dashboard');
    await openAddFood(page);

    await page.locator('form input[placeholder="Breakfast, snack..."]').fill('Right now');
    await page.locator('#entry-calories').fill('100');
    await page.locator('form button[type="submit"]').click();
    await expect(page.getByText('Entry tracked')).toBeVisible();

    expect(psql(`SELECT eaten_at IS NULL FROM calorie_entries WHERE user_id = ${user.id} AND entry_name = 'Right now'`)).toBe('t');
    expect(psql(`SELECT entry_date::text FROM calorie_entries WHERE user_id = ${user.id} AND entry_name = 'Right now'`)).toBe(TODAY);
    await context.close();
  });

  test('undoing a delete brings the components and the time back', async ({ browser }) => {
    const { context, page } = await loginUser(browser, user.email, user.password);
    await page.goto('/dashboard');
    await openAddFood(page);
    await pickFood(page, 'ZyxFlocken', '50');
    await pickFood(page, 'Qjkbanane', '120');
    await page.getByTestId('entry-form-time-input').fill('07:30');
    await page.locator('form button[type="submit"]').click();
    await expect(page.getByText('Entry tracked')).toBeVisible();

    const row = page.getByTestId('entry-row').first();
    await row.getByRole('button', { name: 'Delete entry' }).click();
    await expect(page.getByTestId('entry-row')).toHaveCount(0);

    await page.getByRole('button', { name: 'Undo' }).click();
    const restored = page.getByTestId('entry-row').first();
    await expect(restored.getByTestId('entry-component-count')).toHaveText('2 components');
    await expect(restored).toContainText('294');
    expect(psql(`SELECT to_char(eaten_at AT TIME ZONE 'UTC', 'HH24:MI') FROM calorie_entries WHERE user_id = ${user.id}`)).toBe('07:30');
    await context.close();
  });
});
