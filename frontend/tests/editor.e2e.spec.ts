import { test, expect } from '@playwright/test';

async function waitForPreviewText(page, text: string) {
  const frame = page.frameLocator('#preview');
  await expect(frame.locator('body')).toContainText(text);
}

test.beforeEach(async ({ page }) => {
  await page.goto('/');
  await expect(page.locator('#editor')).toBeVisible();
  await expect(page.locator('#editor')).toHaveValue(/mock file content/);
});

test('updates preview when editing markdown', async ({ page }) => {
  const editor = page.locator('#editor');
  await editor.fill('# Hello Karte\n\n**Preview** works.');
  await waitForPreviewText(page, 'Hello Karte');
  const heading = page.frameLocator('#preview').locator('h1');
  await expect(heading).toHaveText('Hello Karte');
  await expect(page.frameLocator('#preview').locator('strong')).toHaveText('Preview');
});

test('renders CSV imports in preview', async ({ page }) => {
  const editor = page.locator('#editor');
  await editor.fill('CSV data below:\n\n@import data/sample.csv');

  const table = page.frameLocator('#preview').locator('table');
  await expect(table).toBeVisible();
  const rows = table.locator('tbody tr');
  await expect(rows).toHaveCount(3);
  await expect(rows.nth(0)).toContainText('Alice');
  await expect(rows.nth(0)).toContainText('Engineer');
  await expect(rows.nth(0)).toContainText('Tokyo');
});

test('saves edited file successfully', async ({ page }) => {
  const editor = page.locator('#editor');
  await editor.fill('# Save Test');
  await expect(page.locator('#saveBtn')).toHaveClass(/unsaved/);
  await page.click('#saveBtn');
  await expect(page.locator('#saveBtn')).not.toHaveClass(/unsaved/);
});
