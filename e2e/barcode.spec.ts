import { test, expect } from './fixtures/auth';
import { login } from './fixtures/auth';
import { openAddFood } from './fixtures/helpers';

test.describe('Barcode Lookup', () => {
  test('a scanned product becomes a component of the meal being built', async ({ page }) => {
    await login(page);
    await openAddFood(page);

    // The scanner sits beside the food search: a scan adds a component.
    await page.getByTestId('food-search-scan').click();

    // Modal should open. Scoped to the scanner layer: the add-food sheet that
    // holds the scan button is a dialog too.
    const modal = page.locator('[data-modal-layer="scanner"]');
    await expect(modal).toBeVisible();

    // Switch to Manual tab
    await modal.getByRole('button', { name: 'Manual' }).click();

    // Enter barcode and look up
    await modal.locator('input[inputmode="numeric"]').fill('4000417025005');
    await modal.getByRole('button', { name: 'Look up' }).click();

    // Wait for result (external API call)
    await expect(modal.getByTestId('barcode-kcal')).toBeVisible({ timeout: 15000 });

    // Choose the amount and add it
    await modal.getByTestId('barcode-grams').fill('40');
    await modal.getByTestId('barcode-pick').click();

    // Modal closes; the product is a draft component with those grams.
    await expect(modal).not.toBeVisible();
    await expect(page.getByTestId('draft-component')).toHaveCount(1);
    await expect(page.getByTestId('draft-component-grams')).toHaveValue('40');
  });
});
