import { test, expect, Browser } from '@playwright/test';
import { createIsolatedUser, loginUser, openAddFood, psql } from './fixtures/helpers';

const baseURL = process.env.E2E_BASE_URL || 'http://localhost:3001';
const ADMIN_STORAGE = 'e2e/.auth/admin.json';

let user: { email: string; password: string; id: string };

/**
 * Flip a global admin setting through the admin API rather than with a direct
 * INSERT.
 *
 * The server memoises admin_settings for a minute (database.SettingsCache) and
 * only invalidates that cache on writes it performs itself. A raw psql write
 * therefore takes effect at some unpredictable point up to 60s later — for the
 * disable step that means asserting against a setting that may not be live yet,
 * and for the restore step it means leaving enable_barcode reading `false` for
 * up to a minute after this spec is done, which is exactly what would break the
 * barcode readers that `playwright.config.ts` schedules right after it.
 */
async function setAdminSetting(browser: Browser, key: string, value: string) {
  const ctx = await browser.newContext({ storageState: ADMIN_STORAGE });
  try {
    const csrfRes = await ctx.request.get(`${baseURL}/api/csrf`);
    const { token } = await csrfRes.json();
    const res = await ctx.request.post(`${baseURL}/admin/settings`, {
      headers: { 'X-CSRF-Token': token, 'Content-Type': 'application/json' },
      data: JSON.stringify({ settings: { [key]: value } }),
    });
    expect(res.status(), `failed to set ${key}=${value}`).toBe(200);
  } finally {
    await ctx.close();
  }
}

test.describe('Barcode Extended', () => {
  test.describe.configure({ mode: 'serial' });

  test.beforeAll(() => {
    user = createIsolatedUser('barcode-ext');
  });

  test.afterAll(async ({ browser }) => {
    // Restore barcode setting
    await setAdminSetting(browser, 'enable_barcode', 'true');
  });

  test('barcode button is hidden when admin disables barcode scanning', async ({ browser }) => {
    await setAdminSetting(browser, 'enable_barcode', 'false');

    const { context: ctx, page } = await loginUser(browser, user.email, user.password);
    await page.goto('/dashboard');
    await page.waitForLoadState('domcontentloaded');

    // Barcode button should NOT be visible. It lives in the add-food sheet, so
    // open that first — asserting against a closed sheet would pass either way.
    await openAddFood(page);
    await expect(page.getByTestId('food-search-input')).toBeVisible();
    await expect(page.getByTestId('food-search-scan')).not.toBeVisible({ timeout: 5000 });

    // Restore
    await setAdminSetting(browser, 'enable_barcode', 'true');
    await ctx.close();
  });

  test('a scanned product is tracked as a component with the chosen grams', async ({ browser }) => {
    // What the server does on a scan: the product is a global catalog food,
    // and the response carries it as `food`. Seeded under a made-up EAN so the
    // spec never calls Open Food Facts.
    const ean = '5901234123457';
    psql(`DELETE FROM foods WHERE source = 'off' AND source_code = '${ean}'`);
    const foodId = psql(
      `INSERT INTO foods (source, source_code, name, kcal_100g, protein_100g, carbs_100g, fat_100g)
       VALUES ('off', '${ean}', 'Test Cereal Bar (Zyx)', 150, 3, 25, 5) RETURNING id`,
    );

    const { context: ctx, page } = await loginUser(browser, user.email, user.password);
    await page.goto('/dashboard');
    await page.waitForLoadState('domcontentloaded');

    // Mock the barcode API AFTER page is loaded
    await page.route('**/api/barcode/*', (route) => {
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          ok: true,
          name: 'Test Cereal Bar (Zyx)',
          caloriesPer100g: 150,
          macrosPer100g: { protein: 3, carbs: 25, fat: 5 },
          servingSize: '40g',
          servingQuantity: 40,
          food: {
            id: Number(foodId),
            source: 'off',
            source_code: ean,
            name: 'Test Cereal Bar (Zyx)',
            source_classification: null,
            calories_per_100g: 150,
            macros_per_100g: { protein_g: 3, carbs_g: 25, fat_g: 5, fiber_g: null, sugar_g: null },
          },
        }),
      });
    });

    await openAddFood(page);
    const scanButton = page.getByTestId('food-search-scan');
    await expect(scanButton).toBeVisible({ timeout: 10000 });
    await scanButton.click();

    // Scoped to the scanner layer: the add-food sheet is a dialog too.
    const modal = page.locator('[data-modal-layer="scanner"]');
    await expect(modal).toBeVisible({ timeout: 5000 });

    await modal.getByRole('button', { name: 'Manual' }).click();
    const barcodeInput = modal.getByPlaceholder('Enter barcode number');
    await expect(barcodeInput).toBeVisible({ timeout: 5000 });
    await barcodeInput.fill(ean);
    await modal.getByRole('button', { name: 'Look up' }).click();

    await expect(modal.getByTestId('barcode-product-name')).toHaveText('Test Cereal Bar (Zyx)', { timeout: 5000 });
    // The serving size is the starting amount; the unknown fiber shows a dash.
    await expect(modal.getByTestId('barcode-grams')).toHaveValue('40');
    await expect(modal.getByTestId('barcode-kcal')).toHaveText('60 cal');

    await modal.getByTestId('barcode-grams').fill('80');
    await expect(modal.getByTestId('barcode-kcal')).toHaveText('120 cal');
    await modal.getByTestId('barcode-pick').click();

    // Modal closes; the product is now a draft component, not a pre-filled entry.
    await expect(modal).not.toBeVisible();
    await expect(page.getByTestId('draft-component')).toHaveCount(1);
    await expect(page.locator('#entry-calories')).toHaveValue('120');

    await page.locator('form button[type="submit"]').click();
    await expect(page.getByText('Entry tracked')).toBeVisible();

    const row = page.getByTestId('entry-row').filter({ hasText: 'Test Cereal Bar (Zyx)' });
    await expect(row.getByTestId('entry-component-count')).toHaveText('1 component');
    const linked = psql(
      `SELECT count(*) FROM entry_components c JOIN calorie_entries e ON e.id = c.entry_id
       WHERE e.user_id = ${user.id} AND c.food_id = ${foodId} AND c.grams = 80`,
    );
    expect(linked).toBe('1');

    psql(`DELETE FROM calorie_entries WHERE user_id = ${user.id}`);
    psql(`DELETE FROM foods WHERE id = ${foodId}`);
    await ctx.close();
  });
});
