import { test, expect } from '@playwright/test';
import { psql, createIsolatedUser, loginUser } from './fixtures/helpers';
import { expectNoWarningColors } from './fixtures/panel';

// The Today panel (Dashboard/TodayPanel.tsx) shows calories as a header line and
// protein/carbs/fat as "eaten / goal g", with fiber as its own thin bar. Macro
// segments carry their own fixed colour; status colour is deliberately absent:
//   - a min-goal (target mode) not yet reached is not a warning,
//   - a limit exceeded stays visible as numbers only ("N kcal over", "120 / 67 g").
// These specs pin that semantics, not the old red/amber/green fills.

const TODAY = new Date().toLocaleDateString('en-CA', { timeZone: 'UTC' });

type SeededUser = { email: string; password: string; id: string };
let targetUser: SeededUser;
let limitUser: SeededUser;
let fatUser: SeededUser;

test.describe('Today panel status semantics', () => {
  test.beforeAll(() => {
    // protein in "target" mode, goal = 100
    targetUser = createIsolatedUser('macro-colors-target');
    psql(`UPDATE users SET
      macros_enabled = '{"calories": true, "protein": true, "carbs": false, "fat": false, "fiber": false, "sugar": false, "auto_calc_calories": false}',
      macro_goals = '{"calories": 2000, "protein": 100, "carbs": 0, "fat": 0, "fiber": 0, "sugar": 0, "calories_mode": "limit", "protein_mode": "target", "carbs_mode": "limit", "fat_mode": "limit"}',
      daily_goal = 2000
      WHERE id = ${targetUser.id}`);

    // calories in "limit" mode, goal = 1000, no macros
    limitUser = createIsolatedUser('macro-colors-limit');
    psql(`UPDATE users SET
      macros_enabled = '{"calories": true, "protein": false, "carbs": false, "fat": false, "fiber": false, "sugar": false, "auto_calc_calories": false}',
      macro_goals = '{"calories": 1000, "protein": 0, "carbs": 0, "fat": 0, "fiber": 0, "sugar": 0, "calories_mode": "limit", "protein_mode": "limit", "carbs_mode": "limit", "fat_mode": "limit"}',
      daily_goal = 1000
      WHERE id = ${limitUser.id}`);

    // fat in "limit" mode, goal = 67
    fatUser = createIsolatedUser('macro-colors-fat');
    psql(`UPDATE users SET
      macros_enabled = '{"calories": true, "protein": false, "carbs": false, "fat": true, "fiber": false, "sugar": false, "auto_calc_calories": false}',
      macro_goals = '{"calories": 2000, "protein": 0, "carbs": 0, "fat": 67, "fiber": 0, "sugar": 0, "calories_mode": "limit", "protein_mode": "limit", "carbs_mode": "limit", "fat_mode": "limit"}',
      daily_goal = 2000
      WHERE id = ${fatUser.id}`);
  });

  test.afterAll(() => {
    for (const u of [targetUser, limitUser, fatUser]) {
      psql(`DELETE FROM calorie_entries WHERE user_id = ${u.id}`);
    }
  });

  test('a min-goal that is not reached yet carries no warning styling', async ({ browser }) => {
    psql(`INSERT INTO calorie_entries (user_id, entry_date, amount, protein_g, entry_name)
          VALUES (${targetUser.id}, '${TODAY}', 200, 10, 'Light snack')`);

    const { context: ctx, page } = await loginUser(browser, targetUser.email, targetUser.password);
    await page.goto('/dashboard');
    await page.waitForLoadState('domcontentloaded');

    const protein = page.getByTestId('today-macro-protein');
    await expect(protein).toHaveText(/10 \/ 100 g/, { timeout: 10000 });
    await expectNoWarningColors(page.getByRole('region', { name: 'Daily nutrition' }));

    await ctx.close();
  });

  test('a min-goal that is reached shows its numbers without a status colour', async ({ browser }) => {
    psql(`DELETE FROM calorie_entries WHERE user_id = ${targetUser.id}`);
    psql(`INSERT INTO calorie_entries (user_id, entry_date, amount, protein_g, entry_name)
          VALUES (${targetUser.id}, '${TODAY}', 500, 120, 'High protein meal')`);

    const { context: ctx, page } = await loginUser(browser, targetUser.email, targetUser.password);
    await page.goto('/dashboard');
    await page.waitForLoadState('domcontentloaded');

    await expect(page.getByTestId('today-macro-protein')).toHaveText(/120 \/ 100 g/, { timeout: 10000 });
    await expectNoWarningColors(page.getByRole('region', { name: 'Daily nutrition' }));

    await ctx.close();
  });

  test('calories over the limit are shown as "N kcal over", not as a red panel', async ({ browser }) => {
    psql(`INSERT INTO calorie_entries (user_id, entry_date, amount, entry_name)
          VALUES (${limitUser.id}, '${TODAY}', 1200, 'Over limit meal')`);

    const { context: ctx, page } = await loginUser(browser, limitUser.email, limitUser.password);
    await page.goto('/dashboard');
    await page.waitForLoadState('domcontentloaded');

    await expect(page.getByTestId('today-eaten')).toHaveText('1,200', { timeout: 10000 });
    await expect(page.getByTestId('today-goal')).toHaveText('/ 1,000 kcal');
    await expect(page.getByTestId('today-remaining')).toHaveText('200 kcal over');
    await expectNoWarningColors(page.getByRole('region', { name: 'Daily nutrition' }));

    await ctx.close();
  });

  test('a limit macro over its goal stays visible as numbers only', async ({ browser }) => {
    psql(`INSERT INTO calorie_entries (user_id, entry_date, amount, fat_g, entry_name)
          VALUES (${fatUser.id}, '${TODAY}', 1100, 120, 'Fatty meal')`);

    const { context: ctx, page } = await loginUser(browser, fatUser.email, fatUser.password);
    await page.goto('/dashboard');
    await page.waitForLoadState('domcontentloaded');

    await expect(page.getByTestId('today-macro-fat')).toHaveText(/120 \/ 67 g/, { timeout: 10000 });
    await expectNoWarningColors(page.getByRole('region', { name: 'Daily nutrition' }));

    await ctx.close();
  });
});
