import { test, expect } from '@playwright/test';
import { psql, createIsolatedUser, loginUser } from './fixtures/helpers';

// Fresh browser contexts logged in through the API: the specs below change the
// account's goals and must not share the suite's saved session.
test.describe('Goal adjustment sheet', () => {
  test('adjusts the day, reuses it as a quick pick and resets to the Basisziel', async ({ browser }) => {
    const user = createIsolatedUser('goal-adjust');
    // Base: 2000 kcal, protein 150, carbs 250, fat 65, fiber 25, sugar 50.

    const { context: ctx, page } = await loginUser(browser, user.email, user.password);
    await page.goto('/dashboard');
    await page.waitForLoadState('domcontentloaded');

    await expect(page.getByTestId('today-goal')).toHaveText('/ 2,000 kcal', { timeout: 10000 });
    await expect(page.getByTestId('today-adjusted')).toHaveCount(0);

    // Open the sheet from the panel's pencil: fields start from the Tagesziel.
    await page.getByTestId('today-edit').click();
    const sheet = page.getByRole('dialog', { name: 'Adjust goal for this day' });
    await expect(sheet).toBeVisible();
    await expect(sheet.getByTestId('goal-adjust-calories')).toHaveValue('2000');
    await expect(sheet.getByTestId('goal-adjust-carbs')).toHaveValue('250');
    await expect(sheet.getByTestId('goal-adjust-reset')).toHaveCount(0);
    await expect(sheet.getByTestId('goal-adjust-save')).toBeDisabled();

    // Set an adjustment with a reason.
    await sheet.getByTestId('goal-adjust-calories').fill('3100');
    await sheet.getByTestId('goal-adjust-carbs').fill('480');
    await sheet.getByTestId('goal-adjust-reason').fill('Lauf 25 km');
    await sheet.getByTestId('goal-adjust-save').click();
    await expect(sheet).toBeHidden();

    // The panel shows the new Tagesziel, the chip with reason and the Basisziel.
    await expect(page.getByTestId('today-goal')).toHaveText('/ 3,100 kcal');
    await expect(page.getByTestId('today-adjusted')).toContainText('adjusted: Lauf 25 km');
    await expect(page.getByTestId('today-adjusted')).toContainText('Base 2,000');
    await expect(page.getByTestId('today-macro-carbs')).toContainText('/ 480 g');
    // Unchanged fields kept their value.
    await expect(page.getByTestId('today-macro-protein')).toContainText('/ 150 g');

    // Reopen on the adjusted day: the fields show the adjustment, reset is offered.
    await page.getByTestId('today-edit').click();
    await expect(sheet.getByTestId('goal-adjust-calories')).toHaveValue('3100');
    await expect(sheet.getByTestId('goal-adjust-reason')).toHaveValue('Lauf 25 km');
    await expect(sheet.getByTestId('goal-adjust-reset')).toBeVisible();
    await sheet.getByRole('button', { name: 'Cancel' }).click();
    await expect(sheet).toBeHidden();
    await expect(page.getByTestId('today-goal')).toHaveText('/ 3,100 kcal');

    // On another day the earlier adjustment is a quick pick (picks come from
    // adjustments that still exist). Picking only pre-fills the sheet.
    const yesterday = new Date(Date.now() - 86400000).toLocaleDateString('en-CA', { timeZone: 'UTC' });
    await page.getByLabel('Entry date').fill(yesterday);
    await expect(page.getByTestId('today-goal')).toHaveText('/ 2,000 kcal');
    await page.getByTestId('today-edit').click();
    await expect(sheet.getByTestId('goal-adjust-calories')).toHaveValue('2000');
    await sheet.getByTestId('goal-adjust-pick').filter({ hasText: 'Lauf 25 km' }).click();
    await expect(sheet.getByTestId('goal-adjust-calories')).toHaveValue('3100');
    await expect(sheet.getByTestId('goal-adjust-carbs')).toHaveValue('480');
    await expect(sheet.getByTestId('goal-adjust-reason')).toHaveValue('Lauf 25 km');
    await expect(page.getByTestId('today-goal')).toHaveText('/ 2,000 kcal');
    await expect(page.getByTestId('today-adjusted')).toHaveCount(0);

    // Edit before saving: the pick is a starting point, not a commitment.
    await sheet.getByTestId('goal-adjust-calories').fill('3300');
    await sheet.getByTestId('goal-adjust-save').click();
    await expect(sheet).toBeHidden();
    await expect(page.getByTestId('today-goal')).toHaveText('/ 3,300 kcal');
    await expect(page.getByTestId('today-adjusted')).toContainText('adjusted: Lauf 25 km');

    // Reset that day to the Basisziel; today's adjustment is untouched.
    await page.getByTestId('today-edit').click();
    await sheet.getByTestId('goal-adjust-reset').click();
    await expect(sheet).toBeHidden();
    await expect(page.getByTestId('today-goal')).toHaveText('/ 2,000 kcal');
    await expect(page.getByTestId('today-adjusted')).toHaveCount(0);
    await page.getByLabel('Entry date').fill(new Date().toLocaleDateString('en-CA', { timeZone: 'UTC' }));
    await expect(page.getByTestId('today-goal')).toHaveText('/ 3,100 kcal');

    await ctx.close();
  });

  test('derives and locks the calories for an auto-calc account', async ({ browser }) => {
    const user = createIsolatedUser('goal-adjust-auto');
    psql(`UPDATE users SET
      macros_enabled = '{"calories": true, "protein": true, "carbs": true, "fat": true, "fiber": true, "sugar": false, "auto_calc_calories": true}'
      WHERE id = ${user.id}`);

    const { context: ctx, page } = await loginUser(browser, user.email, user.password);
    await page.goto('/dashboard');
    await page.waitForLoadState('domcontentloaded');
    await expect(page.getByTestId('today-edit')).toBeVisible({ timeout: 10000 });

    await page.getByTestId('today-edit').click();
    const sheet = page.getByRole('dialog', { name: 'Adjust goal for this day' });
    const calories = sheet.getByTestId('goal-adjust-calories');
    // 150 g protein, 250 g carbs and 65 g fat: 4*150 + 4*250 + 9*65.
    await expect(calories).toHaveValue('2185');
    await expect(calories).not.toBeEditable();
    // Sugar is not tracked by this account, so there is no field for it.
    await expect(sheet.getByTestId('goal-adjust-sugar')).toHaveCount(0);

    await sheet.getByTestId('goal-adjust-carbs').fill('300');
    await expect(calories).toHaveValue('2385');
    await sheet.getByTestId('goal-adjust-save').click();
    await expect(sheet).toBeHidden();
    await expect(page.getByTestId('today-goal')).toHaveText('/ 2,385 kcal');

    await ctx.close();
  });
});
