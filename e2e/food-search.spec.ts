import { test, expect } from '@playwright/test';
import { psql, createIsolatedUser, loginUser } from './fixtures/helpers';

// The catalog search on the add-component page: type, see hits update, pick a
// food, enter grams, and watch the meal's totals follow. Foods are seeded as
// the user's own, under made-up names, so the spec neither depends on the BLS
// import nor changes when it has been run.

const TODAY = new Date().toLocaleDateString('en-CA', { timeZone: 'UTC' });

let user: { email: string; password: string; id: string };
let stranger: { email: string; password: string; id: string };
let mealId: string;
let directId: string;

function seedFood(userId: string, name: string, kcal: number, protein: number | null): void {
  psql(
    `INSERT INTO foods (user_id, source, name, kcal_100g, protein_100g) VALUES (${userId}, 'own', :'name', ${kcal}, ${protein ?? 'NULL'})`,
    { name },
  );
}

test.describe('Food search', () => {
  test.beforeAll(() => {
    user = createIsolatedUser('food-search');
    stranger = createIsolatedUser('food-search-stranger');
    seedFood(user.id, 'Zyx Flocken Zart', 372, 13.5);
    seedFood(user.id, 'Zyxdrink', 45, 1);
    seedFood(user.id, 'Qjk banane roh', 90, 1.1);
    seedFood(stranger.id, 'Zwv brei Geheim', 100, 3);

    mealId = psql(
      `INSERT INTO calorie_entries (user_id, entry_date, entry_name, amount) VALUES (${user.id}, '${TODAY}', 'Porridge', 0) RETURNING id`,
    );
    psql(
      `INSERT INTO entry_components (entry_id, name, grams, kcal_100g) VALUES (${mealId}, 'Oats', 50, 372)`,
    );
    psql(`UPDATE calorie_entries SET amount = 186 WHERE id = ${mealId}`);
    directId = psql(
      `INSERT INTO calorie_entries (user_id, entry_date, entry_name, amount) VALUES (${user.id}, '${TODAY}', 'Canteen lunch', 750) RETURNING id`,
    );
  });

  test.afterAll(() => {
    if (!user?.id) return;
    psql(`DELETE FROM calorie_entries WHERE user_id IN (${user.id}, ${stranger.id})`);
    psql(`DELETE FROM foods WHERE user_id IN (${user.id}, ${stranger.id})`);
  });

  test('needs two letters, then finds foods while typing, ignoring spaces', async ({ browser }) => {
    const { context, page } = await loginUser(browser, user.email, user.password);
    await page.goto(`/entries/${mealId}/add`);

    const input = page.getByTestId('food-search-input');
    await input.pressSequentially('z');
    await expect(page.getByTestId('food-search-hint')).toBeVisible();
    await expect(page.getByTestId('food-search-result')).toHaveCount(0);

    await input.pressSequentially('yx');
    await expect(page.getByTestId('food-search-result')).toHaveCount(2);

    // "ZyxFlocken" has no space; the catalog name does.
    await input.fill('ZyxFlocken');
    await expect(page.getByTestId('food-search-result')).toHaveCount(1);
    await expect(page.getByTestId('food-search-result')).toContainText('Zyx Flocken Zart');
    await expect(page.getByTestId('food-search-result')).toContainText('372 kcal / 100 g');

    await input.fill('zzzzzz');
    await expect(page.getByTestId('food-search-empty')).toBeVisible();
    await context.close();
  });

  test('never shows another account\'s own foods', async ({ browser }) => {
    const { context, page } = await loginUser(browser, user.email, user.password);
    await page.goto(`/entries/${mealId}/add`);

    await page.getByTestId('food-search-input').fill('Zwvbrei');
    await expect(page.getByTestId('food-search-empty')).toBeVisible();
    await context.close();
  });

  test('adds a food with grams to an existing meal and the totals follow', async ({ browser }) => {
    const { context, page } = await loginUser(browser, user.email, user.password);
    await page.goto(`/entries/${mealId}`);
    await expect(page.getByTestId('meal-component')).toHaveCount(1);

    await page.getByTestId('meal-add-component').click();
    await page.getByTestId('food-search-input').fill('Qjkbanane');
    await page.getByTestId('food-search-result').click();

    await expect(page.getByTestId('food-search-grams')).toHaveValue('100');
    await page.getByTestId('food-search-grams').fill('2*60');
    await expect(page.getByTestId('food-search-kcal')).toContainText('108');
    await page.getByTestId('food-search-add').click();

    await expect(page).toHaveURL(new RegExp(`/entries/${mealId}$`));
    await expect(page.getByTestId('meal-component')).toHaveCount(2);
    // 50 g oats (186) + 120 g banana (108)
    await expect(page.getByRole('img', { name: /: 294 kcal/ })).toBeVisible();

    // Stored: the day list agrees after a reload.
    await page.goto('/dashboard');
    await expect(page.getByTestId('entry-row').filter({ hasText: 'Porridge' })).toContainText('294');
    await context.close();
  });

  test('turns a direct entry into a meal built from components', async ({ browser }) => {
    const { context, page } = await loginUser(browser, user.email, user.password);
    await page.goto(`/entries/${directId}/add`);

    await page.getByTestId('food-search-input').fill('Zyxdrink');
    await page.getByTestId('food-search-result').click();
    await page.getByTestId('food-search-grams').fill('200');
    await page.getByTestId('food-search-add').click();

    await expect(page.getByTestId('meal-component')).toHaveCount(1);
    await expect(page.getByRole('img', { name: /: 90 kcal/ })).toBeVisible();
    await context.close();
  });

  test('refuses grams the server would refuse', async ({ browser }) => {
    const { context, page } = await loginUser(browser, user.email, user.password);
    await page.goto(`/entries/${mealId}/add`);

    await page.getByTestId('food-search-input').fill('Qjkbanane');
    await page.getByTestId('food-search-result').click();
    for (const bad of ['0', '20000', 'abc']) {
      await page.getByTestId('food-search-grams').fill(bad);
      await expect(page.getByTestId('food-search-add')).toBeDisabled();
    }
    await context.close();
  });

  test('changes the time a meal was eaten from the meal page', async ({ browser }) => {
    const { context, page } = await loginUser(browser, user.email, user.password);
    await page.goto(`/entries/${mealId}`);

    const input = page.getByTestId('meal-time-input');
    await input.fill('07:30');
    await input.blur();

    await expect(input).toHaveValue('07:30');
    await page.reload();
    await expect(page.getByTestId('meal-time-input')).toHaveValue('07:30');
    await context.close();
  });
});
