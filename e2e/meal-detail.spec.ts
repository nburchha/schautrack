import { test, expect } from '@playwright/test';
import { psql, createIsolatedUser, loginUser } from './fixtures/helpers';

// The meal detail page: open an entry from the day list, read its components
// and macros (fiber included), change grams, remove a component.

const TODAY = new Date().toLocaleDateString('en-CA', { timeZone: 'UTC' });

let user: { email: string; password: string; id: string };
let friend: { email: string; password: string; id: string };
let porridgeId: string;
let directId: string;
let friendEntryId: string;

/** Inserts an entry with components and sets its totals to their sum, as the server does. */
function seedMeal(userId: string, name: string, parts: Array<[string, number, number, number | null, number | null, number | null, number | null, number | null]>): string {
  const id = psql(
    `INSERT INTO calorie_entries (user_id, entry_date, entry_name, amount) VALUES (${userId}, '${TODAY}', :'name', 0) RETURNING id`,
    { name },
  );
  for (const [pname, grams, kcal, protein, carbs, fat, fiber, sugar] of parts) {
    const n = (v: number | null) => (v === null ? 'NULL' : v);
    psql(
      `INSERT INTO entry_components (entry_id, name, grams, kcal_100g, protein_100g, carbs_100g, fat_100g, fiber_100g, sugar_100g)
       VALUES (${id}, :'pname', ${grams}, ${kcal}, ${n(protein)}, ${n(carbs)}, ${n(fat)}, ${n(fiber)}, ${n(sugar)})`,
      { pname },
    );
  }
  psql(`UPDATE calorie_entries e SET
      amount = ROUND(s.k)::int, protein_g = ROUND(s.p)::int, carbs_g = ROUND(s.c)::int,
      fat_g = ROUND(s.f)::int, fiber_g = ROUND(s.fi)::int, sugar_g = ROUND(s.su)::int
    FROM (SELECT SUM(grams*kcal_100g/100) k, SUM(grams*protein_100g/100) p, SUM(grams*carbs_100g/100) c,
                 SUM(grams*fat_100g/100) f, SUM(grams*fiber_100g/100) fi, SUM(grams*sugar_100g/100) su
          FROM entry_components WHERE entry_id = ${id}) s
    WHERE e.id = ${id}`);
  return id;
}

test.describe('Meal detail page', () => {
  test.beforeAll(() => {
    user = createIsolatedUser('meal-detail');
    friend = createIsolatedUser('meal-detail-friend');
    // friend is the target, so friend's outgoing shares live in target_shares.
    psql(`INSERT INTO account_links (requester_id, target_id, status, target_shares)
          VALUES (${user.id}, ${friend.id}, 'accepted', '{"nutrition": true}') ON CONFLICT DO NOTHING`);

    // 50 g oats + 200 g milk (no fiber figure): 314 kcal, fiber only from the oats.
    porridgeId = seedMeal(user.id, 'Porridge', [
      ['Oats', 50, 372, 13.5, 58, 7, 10, 1],
      ['Milk', 200, 64, 3.4, 5, 3.5, null, 5],
    ]);
    directId = psql(
      `INSERT INTO calorie_entries (user_id, entry_date, entry_name, amount) VALUES (${user.id}, '${TODAY}', 'Canteen lunch', 750) RETURNING id`,
    );
    friendEntryId = seedMeal(friend.id, 'Friend lunch', [['Rice', 150, 130, 2.7, 28, 0.3, 0.4, 0.1]]);
  });

  test.afterAll(() => {
    if (!user?.id) return;
    psql(`DELETE FROM calorie_entries WHERE user_id IN (${user.id}, ${friend.id})`);
    psql(`DELETE FROM account_links WHERE requester_id = ${user.id} OR target_id = ${user.id}`);
  });

  test('opens from the day list and shows components with all macros', async ({ browser }) => {
    const { context, page } = await loginUser(browser, user.email, user.password);
    await page.goto('/dashboard');

    const row = page.getByTestId('entry-row').filter({ hasText: 'Porridge' });
    await expect(row.getByTestId('entry-component-count')).toHaveText('2 components');
    // The direct entry has nothing to count.
    await expect(page.getByTestId('entry-row').filter({ hasText: 'Canteen lunch' }).getByTestId('entry-component-count')).toHaveCount(0);

    // Clicking the card's empty space opens the page, not just the chevron.
    await row.getByTestId('entry-open').focus();
    await row.click({ position: { x: 4, y: 4 } });
    await expect(page).toHaveURL(new RegExp(`/entries/${porridgeId}$`));

    await expect(page.getByTestId('meal-title')).toHaveText('Porridge');
    await expect(page.getByTestId('meal-component')).toHaveCount(2);
    await expect(page.getByTestId('meal-total-fiber')).toContainText('5');
    // Milk has no fiber value, so the total is only a lower bound.
    await expect(page.getByTestId('meal-total-fiber')).toHaveAttribute('title', /only 1 of 2/i);
    await expect(page.getByTestId('meal-total-protein')).toContainText('14');
    await expect(page.getByTestId('meal-component-kcal').first()).toHaveText('186');

    await page.getByTestId('meal-back').click();
    await expect(page).toHaveURL(/\/dashboard$/);
    await context.close();
  });

  test('changing grams recomputes the meal and the day', async ({ browser }) => {
    const { context, page } = await loginUser(browser, user.email, user.password);
    await page.goto(`/entries/${porridgeId}`);

    await page.getByTestId('meal-component-grams').first().click();
    const input = page.getByTestId('meal-component-grams-input');
    await input.fill('100');
    await input.press('Enter');

    await expect(page.getByTestId('meal-component-kcal').first()).toHaveText('372');
    await expect(page.getByRole('img', { name: /: 500 kcal/ })).toBeVisible();
    await expect(page.getByTestId('meal-total-fiber')).toContainText('10');

    // The server stored it: the day list shows the new total after a reload.
    await page.goto('/dashboard');
    await expect(page.getByTestId('entry-row').filter({ hasText: 'Porridge' })).toContainText('500');
    await context.close();
  });

  test('removing a component asks first and updates the totals', async ({ browser }) => {
    const { context, page } = await loginUser(browser, user.email, user.password);
    await page.goto(`/entries/${porridgeId}`);

    await page.getByTestId('meal-component-remove').last().click();
    await expect(page.getByTestId('meal-component')).toHaveCount(2); // still there until confirmed
    await page.getByTestId('meal-component-remove-confirm').click();

    await expect(page.getByTestId('meal-component')).toHaveCount(1);
    await expect(page.getByTestId('meal-total-fiber')).not.toHaveAttribute('title', /.+/);
    await context.close();
  });

  test('a direct entry explains itself and the plus button leads to the food search', async ({ browser }) => {
    const { context, page } = await loginUser(browser, user.email, user.password);
    await page.goto(`/entries/${directId}`);

    await expect(page.getByTestId('meal-direct')).toBeVisible();
    await expect(page.getByTestId('meal-component')).toHaveCount(0);

    await page.getByTestId('meal-add-component').click();
    await expect(page).toHaveURL(new RegExp(`/entries/${directId}/add$`));
    await expect(page.getByTestId('food-search-input')).toBeVisible();
    await page.getByTestId('add-component-back').click();
    await expect(page.getByTestId('meal-direct')).toBeVisible();
    await context.close();
  });

  test('a linked account\'s meal is readable but not editable, and strangers get nothing', async ({ browser }) => {
    const { context, page } = await loginUser(browser, user.email, user.password);

    await page.goto(`/entries/${friendEntryId}?user=${friend.id}`);
    await expect(page.getByTestId('meal-title')).toHaveText('Friend lunch');
    await expect(page.getByTestId('meal-add-component')).toHaveCount(0);
    await expect(page.getByTestId('meal-component-remove')).toHaveCount(0);
    await expect(page.getByTestId('meal-component-grams')).toBeDisabled();

    // Without ?user= the same id is not the viewer's entry.
    await page.goto(`/entries/${friendEntryId}`);
    await expect(page.getByText(/doesn't exist/i)).toBeVisible();
    await context.close();
  });
});
