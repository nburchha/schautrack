import { test, expect } from './fixtures/auth';
import { login } from './fixtures/auth';
import { psql, createIsolatedUser, loginUser } from './fixtures/helpers';

test.describe('Dashboard State', () => {
  test('calorie panel shows current total', async ({ page }) => {
    await login(page);

    // The Today panel's calorie header carries the eaten total as its primary number
    const eaten = page.getByTestId('today-eaten');
    await expect(eaten).toBeVisible({ timeout: 5000 });
    await expect(eaten).toHaveText(/\d/);
  });

  test('entry list header shows date and user', async ({ page }) => {
    await login(page);

    // The entries header span should show today's date and "You"
    const header = page.locator('span').filter({ hasText: /\d{4}-\d{2}-\d{2}.*You/ });
    await expect(header).toBeVisible({ timeout: 5000 });
  });

  test('share card shows "You" label', async ({ page }) => {
    await login(page);

    // The user's share card should show "You"
    await expect(page.getByText('You', { exact: true })).toBeVisible({ timeout: 5000 });
  });

  test('today dot has ring indicator', async ({ page }) => {
    await login(page);

    // Today's dot should have a ring (ring-2 class)
    const todayDot = page.locator('button.ring-2');
    await expect(todayDot).toBeVisible({ timeout: 5000 });
  });

  test('Today panel shows the Tagesziel of an adjusted day with its reason and the Basisziel', async ({ browser }) => {
    const user = createIsolatedUser('dashboard-adjusted');
    psql(`UPDATE users SET
      macros_enabled = '{"calories": true, "protein": true, "carbs": true, "fat": true, "fiber": true, "sugar": false, "auto_calc_calories": false}',
      macro_goals = '{"calories": 2400, "protein": 150, "carbs": 300, "fat": 67, "fiber": 35, "sugar": 0, "calories_mode": "limit", "protein_mode": "target", "carbs_mode": "target", "fat_mode": "limit"}',
      daily_goal = 2400
      WHERE id = ${user.id}`);
    const today = new Date().toLocaleDateString('en-CA', { timeZone: 'UTC' });
    psql(`INSERT INTO goal_adjustments (user_id, goal_date, calories, protein, carbs, fat, fiber, reason)
          VALUES (${user.id}, '${today}', 3100, 150, 480, 67, 35, 'Lauf 25 km')`);
    psql(`INSERT INTO calorie_entries (user_id, entry_date, amount, protein_g, carbs_g, fat_g, fiber_g, entry_name)
          VALUES (${user.id}, '${today}', 1420, 92, 148, 41, 14, 'Breakfast and lunch')`);

    const { context: ctx, page } = await loginUser(browser, user.email, user.password);
    await page.goto('/dashboard');
    await page.waitForLoadState('domcontentloaded');

    await expect(page.getByTestId('today-eaten')).toHaveText('1,420', { timeout: 10000 });
    await expect(page.getByTestId('today-goal')).toHaveText('/ 3,100 kcal');
    await expect(page.getByTestId('today-remaining')).toHaveText('1,680 kcal left');
    await expect(page.getByTestId('today-adjusted')).toContainText('adjusted: Lauf 25 km');
    await expect(page.getByTestId('today-adjusted')).toContainText('Base 2,400');
    await expect(page.getByTestId('today-macro-protein')).toContainText('92 / 150 g');
    await expect(page.getByTestId('today-macro-carbs')).toContainText('148 / 480 g');
    await expect(page.getByTestId('today-macro-fat')).toContainText('41 / 67 g');
    await expect(page.getByTestId('today-fiber')).toContainText('14 / 35 g');
    await expect(page.getByRole('progressbar', { name: 'Fiber' })).toHaveAttribute('aria-valuenow', '14');

    // Another day has no adjustment: the Basisziel applies and there is no chip.
    const yesterday = new Date(Date.now() - 86400000).toLocaleDateString('en-CA', { timeZone: 'UTC' });
    await page.getByLabel('Entry date').fill(yesterday);
    await expect(page.getByTestId('today-goal')).toHaveText('/ 2,400 kcal');
    await expect(page.getByTestId('today-adjusted')).toHaveCount(0);

    await ctx.close();
  });
});
